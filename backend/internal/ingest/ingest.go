// Package ingest keeps every linked vehicle's state current: one worker per
// vehicle reads it at a cadence chosen by what the car is doing (driving,
// charging, idle, asleep) and whether anyone is watching, lets idle cars fall
// asleep (ADR-013), and pushes each snapshot through telemetry storage,
// analytics, notifications and the realtime hub.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/analytics"
	"github.com/altered-intelligence/milesight/internal/geo"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/notify"
	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Sync statuses reported on vehicles.
const (
	SyncPolling   = "polling"
	SyncIdle      = "idle" // asleep/offline: state checks only
	SyncSuspended = "suspended"
	SyncError     = "error"
)

// Manager owns the per-vehicle workers.
type Manager struct {
	Store         store.Store
	Transports    transport.Registry
	Hub           *realtime.Hub
	Dispatcher    *notify.Dispatcher
	Log           *slog.Logger
	HistoryDays   int
	DiscoverEvery time.Duration
	Now           func() time.Time

	mu      sync.Mutex
	workers map[string]*worker
	kick    chan struct{}
}

type worker struct {
	vehicleID string
	cancel    context.CancelFunc
	poke      chan []time.Duration

	mu     sync.Mutex
	target store.PollTarget
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) init() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workers == nil {
		m.workers = map[string]*worker{}
	}
	if m.kick == nil {
		m.kick = make(chan struct{}, 1)
	}
	if m.DiscoverEvery <= 0 {
		m.DiscoverEvery = time.Minute
	}
}

// Run discovers pollable vehicles and supervises workers until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.init()
	t := time.NewTicker(m.DiscoverEvery)
	defer t.Stop()
	for {
		m.discover(ctx)
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, w := range m.workers {
				w.cancel()
			}
			m.mu.Unlock()
			return
		case <-t.C:
		case <-m.kick:
		}
	}
}

// Discover asks the supervisor to reconcile workers now (e.g. after sign-in).
func (m *Manager) Discover() {
	m.init()
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) discover(ctx context.Context) {
	targets, err := m.Store.ListPollTargets(ctx)
	if err != nil {
		m.Log.Error("ingest: list targets", "err", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	for _, t := range targets {
		seen[t.Vehicle.ID] = true
		if w, ok := m.workers[t.Vehicle.ID]; ok {
			w.mu.Lock()
			w.target = t
			w.mu.Unlock()
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		w := &worker{vehicleID: t.Vehicle.ID, cancel: cancel, poke: make(chan []time.Duration, 1), target: t}
		m.workers[t.Vehicle.ID] = w
		go m.runWorker(wctx, w)
	}
	for id, w := range m.workers {
		if !seen[id] {
			w.cancel()
			delete(m.workers, id)
		}
	}
}

// Poke schedules extra reads for a vehicle (after a command or wake), e.g.
// [2s, 6s, 15s] from now. Nil uses the transport's AfterCommand schedule.
func (m *Manager) Poke(vehicleID string, schedule []time.Duration) {
	m.init()
	m.mu.Lock()
	w, ok := m.workers[vehicleID]
	m.mu.Unlock()
	if !ok {
		return
	}
	if schedule == nil {
		w.mu.Lock()
		tr, err := m.Transports.Get(w.target.Transport)
		w.mu.Unlock()
		if err != nil {
			return
		}
		schedule = tr.Policy().AfterCommand
	}
	select {
	case w.poke <- schedule:
	default: // replace a pending poke with the newer schedule
		select {
		case <-w.poke:
		default:
		}
		select {
		case w.poke <- schedule:
		default:
		}
	}
}

// SyncAccount lists the user's vehicles from Tesla, links them and starts polling.
func (m *Manager) SyncAccount(ctx context.Context, userID, transportName string) ([]models.Vehicle, error) {
	tr, err := m.Transports.Get(transportName)
	if err != nil {
		return nil, err
	}
	list, err := tr.ListVehicles(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, v := range list {
		rec := &store.VehicleRecord{VIN: v.VIN, TeslaID: v.TeslaID, DisplayName: v.DisplayName, Model: v.Model, Trim: v.Trim, Color: v.Color}
		if err := m.Store.UpsertVehicle(ctx, rec); err != nil {
			return nil, err
		}
		if err := m.Store.LinkVehicle(ctx, userID, rec.ID, "owner"); err != nil {
			return nil, err
		}
		cur, err := m.Store.GetVehicle(ctx, rec.ID)
		if err == nil && cur.State == models.StateUnknown {
			_ = m.Store.UpdateVehicleStatus(ctx, rec.ID, v.State, SyncIdle, nil, nil)
		}
	}
	m.Discover()
	return m.Store.ListUserVehicles(ctx, userID)
}

// ---------------------------------------------------------------- worker

// vstate is a worker's in-memory view.
type vstate struct {
	proc           analytics.State
	last           *models.LiveState // last online snapshot
	state          string
	idleSince      time.Time
	suspendedUntil time.Time
	failures       int
	configSynced   bool

	cacheAt  time.Time
	prefs    models.Preferences
	rules    []models.Rule
	fences   []models.Geofence
	kwhPerKm float64
	effAt    time.Time
}

func (m *Manager) runWorker(ctx context.Context, w *worker) {
	vs := &vstate{state: models.StateUnknown}
	m.loadState(ctx, w, vs)
	var pokes []time.Time // absolute times of requested extra reads
	next := time.Duration(0)
	for {
		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case sched := <-w.poke:
			timer.Stop()
			now := m.now()
			pokes = pokes[:0]
			for _, d := range sched {
				pokes = append(pokes, now.Add(d))
			}
		case <-timer.C:
			forced := false
			now := m.now()
			for len(pokes) > 0 && !pokes[0].After(now.Add(250*time.Millisecond)) {
				pokes, forced = pokes[1:], true
			}
			interval := m.poll(ctx, w, vs, forced)
			next = interval
			if len(pokes) == 0 {
				continue
			}
		}
		// Wake up for the earliest of the regular poll and the next poke.
		if len(pokes) > 0 {
			if d := pokes[0].Sub(m.now()); d < next || next == 0 {
				next = max(d, 0)
			}
		}
	}
}

func (m *Manager) loadState(ctx context.Context, w *worker, vs *vstate) {
	raw, err := m.Store.GetProcessorState(ctx, w.vehicleID)
	if err == nil {
		if json.Unmarshal(raw, &vs.proc) != nil {
			vs.proc = analytics.State{}
		}
	}
	if v, err := m.Store.GetVehicle(ctx, w.vehicleID); err == nil {
		vs.state = v.State
		if v.Latest != nil && v.Latest.State == models.StateOnline {
			vs.last = v.Latest
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		m.seedHistory(ctx, w, vs)
	}
	if vs.proc.Notify == nil {
		vs.proc.Notify = &analytics.NotifyState{}
	}
}

func (m *Manager) targetOf(w *worker) store.PollTarget {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.target
}

// refreshContext reloads owner preferences, rules, geofences and the learned
// efficiency factor (cached; cheap relative to Tesla calls).
func (m *Manager) refreshContext(ctx context.Context, t store.PollTarget, vs *vstate) {
	now := m.now()
	if now.Sub(vs.cacheAt) > time.Minute {
		if u, err := m.Store.GetUser(ctx, t.UserID); err == nil {
			vs.prefs = u.Preferences
		}
		stored, _ := m.Store.GetRules(ctx, t.UserID)
		vs.rules = notify.Merge(stored)
		vs.fences, _ = m.Store.ListGeofences(ctx, t.UserID)
		vs.cacheAt = now
	}
	if now.Sub(vs.effAt) > time.Hour || vs.kwhPerKm == 0 {
		spec := analytics.SpecFor(t.Vehicle.Model)
		charges, _ := m.Store.ListChargeSessions(ctx, t.Vehicle.ID, store.ListOptions{Limit: 50})
		vs.kwhPerKm = analytics.LearnedKWhPerRatedKm(charges, spec)
		vs.effAt = now
	}
}

func (m *Manager) env(vs *vstate) analytics.Env {
	fences := vs.fences
	return analytics.Env{
		KWhPerRatedKm: vs.kwhPerKm, Currency: vs.prefs.Currency,
		HomeRate: vs.prefs.EnergyRatePerKWh, FastRate: vs.prefs.FastChargeRatePerKWh,
		Place: func(lat, lon float64) (string, *float64) {
			if f := geo.Match(fences, lat, lon); f != nil {
				return f.Name, f.EnergyRatePerKWh
			}
			return "", nil
		},
	}
}

func (m *Manager) resolvePlace(vs *vstate, ls *models.LiveState) {
	if ls.Location == nil {
		return
	}
	ls.Location.Place = ""
	if f := geo.Match(vs.fences, ls.Location.Lat, ls.Location.Lon); f != nil {
		ls.Location.Place = f.Name
	}
}

// seedHistory backfills synthetic history for transports that support it.
func (m *Manager) seedHistory(ctx context.Context, w *worker, vs *vstate) {
	t := m.targetOf(w)
	tr, err := m.Transports.Get(t.Transport)
	if err != nil {
		return
	}
	seeder, ok := tr.(transport.HistorySeeder)
	if !ok || m.HistoryDays <= 0 {
		return
	}
	samples, err := seeder.SeedHistory(ctx, t.UserID, t.Vehicle.VIN, m.HistoryDays)
	if err != nil || len(samples) == 0 {
		return
	}
	m.refreshContext(ctx, t, vs)
	env := m.env(vs)
	proc := &analytics.Processor{Sink: m.Store}
	// Store each chunk's telemetry before segmenting it, so a trip or charging
	// session is never visible before the samples its route/curve is built from.
	const chunk = 1000
	for lo := 0; lo < len(samples); lo += chunk {
		hi := min(lo+chunk, len(samples))
		batch := make([]models.TelemetryPoint, 0, hi-lo)
		for i := lo; i < hi; i++ {
			ls := &samples[i]
			ls.VehicleID = w.vehicleID
			m.resolvePlace(vs, ls)
			batch = append(batch, analytics.TelemetryFromLive(ls))
		}
		if err := m.Store.InsertTelemetry(ctx, w.vehicleID, batch); err != nil {
			m.Log.Error("ingest: seed telemetry", "err", err)
			return
		}
		for i := lo; i < hi; i++ {
			if _, err := proc.Process(ctx, &vs.proc, w.vehicleID, &samples[i], env); err != nil {
				m.Log.Error("ingest: seed process", "err", err)
				return
			}
		}
	}
	m.saveProc(ctx, w.vehicleID, vs)
	// Expose the last known values (marked with the car's current state) so
	// clients have something to show before the first live read.
	for i := len(samples) - 1; i >= 0; i-- {
		if samples[i].State == models.StateOnline {
			last := samples[i]
			vs.last = &last
			display := last
			if cur, err := m.Store.GetVehicle(ctx, w.vehicleID); err == nil && cur.State != models.StateUnknown {
				display.State = cur.State
			}
			_ = m.Store.UpdateVehicleStatus(ctx, w.vehicleID, "", "", &last.UpdatedAt, &display)
			break
		}
	}
	m.Log.Info("ingest: seeded simulator history", "vehicle", w.vehicleID, "samples", len(samples), "days", m.HistoryDays)
}

func (m *Manager) saveProc(ctx context.Context, vehicleID string, vs *vstate) {
	raw, _ := json.Marshal(vs.proc)
	if err := m.Store.SaveProcessorState(ctx, vehicleID, raw); err != nil {
		m.Log.Warn("ingest: save processor state", "vehicle", vehicleID, "err", err)
	}
}

// poll performs one read cycle and returns the delay until the next one.
func (m *Manager) poll(ctx context.Context, w *worker, vs *vstate, forced bool) time.Duration {
	t := m.targetOf(w)
	tr, err := m.Transports.Get(t.Transport)
	if err != nil {
		m.Log.Error("ingest: transport", "vehicle", t.Vehicle.ID, "err", err)
		return 10 * time.Minute
	}
	pol := tr.Policy()
	now := m.now()
	watching := m.Hub.Watching(t.Vehicle.ID, 2*time.Minute)
	suspended := now.Before(vs.suspendedUntil) && !watching && !forced
	rctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	asleepInterval := pol.AsleepCheck
	if watching {
		asleepInterval = min(pol.AsleepCheck, max(pol.Watching, 30*time.Second))
	}

	if vs.state != models.StateOnline || suspended {
		state, err := tr.VehicleState(rctx, t.UserID, t.Vehicle.VIN)
		if err != nil {
			return m.handleErr(ctx, t, vs, err)
		}
		vs.failures = 0
		if state != models.StateOnline {
			m.markAsleep(ctx, w, vs, state)
			return asleepInterval
		}
		if suspended {
			return pol.AsleepCheck // online but we're leaving it alone so it can sleep
		}
	}

	snap, err := tr.VehicleData(rctx, t.UserID, t.Vehicle.VIN)
	if errors.Is(err, transport.ErrVehicleUnavailable) {
		m.markAsleep(ctx, w, vs, models.StateAsleep)
		return asleepInterval
	}
	if err != nil {
		return m.handleErr(ctx, t, vs, err)
	}
	vs.failures = 0
	m.ingest(ctx, w, t, vs, snap)

	ls := snap.State
	active := ls.Climate != nil && ls.Climate.On ||
		ls.Closures != nil && (ls.Closures.UserPresent || !ls.Closures.Locked || ls.Closures.DoorsOpen || ls.Closures.TrunkOpen || ls.Closures.FrunkOpen)
	switch {
	case ls.IsDriving():
		vs.idleSince, vs.suspendedUntil = time.Time{}, time.Time{}
		return pol.Driving
	case ls.IsCharging():
		vs.idleSince, vs.suspendedUntil = time.Time{}, time.Time{}
		return min(pol.Charging, pickWatching(watching, pol))
	case watching:
		vs.idleSince = time.Time{}
		return pol.Watching
	case active || forced:
		vs.idleSince = time.Time{}
		return pol.OnlineActive
	}
	if vs.idleSince.IsZero() {
		vs.idleSince = now
	}
	if now.Sub(vs.idleSince) >= pol.IdleBeforeSuspend {
		vs.suspendedUntil = now.Add(pol.Suspend)
		vs.idleSince = time.Time{}
		_ = m.Store.UpdateVehicleStatus(ctx, t.Vehicle.ID, "", SyncSuspended, nil, nil)
		return pol.AsleepCheck
	}
	return pol.OnlineIdle
}

func pickWatching(watching bool, pol transport.PollPolicy) time.Duration {
	if watching {
		return pol.Watching
	}
	return pol.Charging
}

func (m *Manager) handleErr(ctx context.Context, t store.PollTarget, vs *vstate, err error) time.Duration {
	var rl *transport.RateLimitError
	switch {
	case errors.As(err, &rl):
		m.Log.Warn("ingest: rate limited", "vehicle", t.Vehicle.ID, "retry_after", rl.RetryAfter)
		return max(rl.RetryAfter, 30*time.Second)
	case errors.Is(err, transport.ErrReauthRequired), errors.Is(err, transport.ErrNotFound):
		m.Log.Warn("ingest: needs attention", "vehicle", t.Vehicle.ID, "err", err)
		_ = m.Store.UpdateVehicleStatus(ctx, t.Vehicle.ID, "", SyncError, nil, nil)
		return 30 * time.Minute
	default:
		vs.failures++
		backoff := time.Duration(math.Min(float64(10*time.Minute), float64(15*time.Second)*math.Pow(2, float64(vs.failures-1))))
		m.Log.Warn("ingest: poll failed", "vehicle", t.Vehicle.ID, "failures", vs.failures, "retry_in", backoff, "err", err)
		if vs.failures >= 3 {
			_ = m.Store.UpdateVehicleStatus(ctx, t.Vehicle.ID, "", SyncError, nil, nil)
		}
		return backoff
	}
}

// markAsleep records a sleep/offline transition: closes open trips/sessions
// and tells subscribers, keeping the last known values for display.
func (m *Manager) markAsleep(ctx context.Context, w *worker, vs *vstate, state string) {
	if state == "" {
		state = models.StateAsleep
	}
	changed := vs.state != state
	vs.state = state
	vs.idleSince, vs.suspendedUntil = time.Time{}, time.Time{}
	if !changed {
		return
	}
	t := m.targetOf(w)
	now := m.now()
	m.refreshContext(ctx, t, vs)
	pseudo := &models.LiveState{VehicleID: w.vehicleID, State: state, UpdatedAt: now}
	proc := &analytics.Processor{Sink: m.Store}
	segs, err := proc.Process(ctx, &vs.proc, w.vehicleID, pseudo, m.env(vs))
	if err != nil {
		m.Log.Error("ingest: process sleep", "vehicle", w.vehicleID, "err", err)
	}
	_ = m.Store.InsertTelemetry(ctx, w.vehicleID, []models.TelemetryPoint{analytics.TelemetryFromLive(pseudo)})
	drafts := notify.Evaluate(vs.proc.Notify, notify.Input{Prev: vs.last, Cur: pseudo, Segments: segs, Rules: vs.rules})
	m.saveProc(ctx, w.vehicleID, vs)

	var display *models.LiveState
	if vs.last != nil {
		cp := *vs.last
		cp.State = state
		display = &cp
	}
	_ = m.Store.UpdateVehicleStatus(ctx, w.vehicleID, state, SyncIdle, nil, display)
	if display == nil {
		display = pseudo
	}
	m.Hub.Publish(realtime.Message{Type: realtime.TypeState, VehicleID: w.vehicleID, Data: display})
	m.publishSegments(w.vehicleID, segs)
	m.deliver(ctx, t, vs, drafts)
}

func (m *Manager) ingest(ctx context.Context, w *worker, t store.PollTarget, vs *vstate, snap *transport.Snapshot) {
	ls := snap.State
	ls.VehicleID = w.vehicleID
	m.refreshContext(ctx, t, vs)
	m.resolvePlace(vs, ls)

	if !vs.configSynced {
		rec := &store.VehicleRecord{VIN: t.Vehicle.VIN, TeslaID: t.Vehicle.TeslaID, DisplayName: snap.DisplayName,
			Model: snap.Model, Trim: snap.Trim, Color: snap.Color, Region: t.Vehicle.Region}
		if err := m.Store.UpsertVehicle(ctx, rec); err == nil {
			vs.configSynced = true
		}
	}
	if err := m.Store.InsertTelemetry(ctx, w.vehicleID, []models.TelemetryPoint{analytics.TelemetryFromLive(ls)}); err != nil {
		m.Log.Error("ingest: telemetry", "vehicle", w.vehicleID, "err", err)
	}
	proc := &analytics.Processor{Sink: m.Store}
	segs, err := proc.Process(ctx, &vs.proc, w.vehicleID, ls, m.env(vs))
	if err != nil {
		m.Log.Error("ingest: process", "vehicle", w.vehicleID, "err", err)
	}
	drafts := notify.Evaluate(vs.proc.Notify, notify.Input{Prev: vs.last, Cur: ls, Segments: segs, Rules: vs.rules})
	m.saveProc(ctx, w.vehicleID, vs)

	sync := SyncPolling
	if err := m.Store.UpdateVehicleStatus(ctx, w.vehicleID, models.StateOnline, sync, &ls.UpdatedAt, ls); err != nil {
		m.Log.Error("ingest: status", "vehicle", w.vehicleID, "err", err)
	}
	vs.state, vs.last = models.StateOnline, ls
	m.Hub.Publish(realtime.Message{Type: realtime.TypeState, VehicleID: w.vehicleID, Data: ls})
	// Live trip/charge progress for subscribers.
	if vs.proc.Trip != nil {
		if tr, err := m.Store.GetTrip(ctx, w.vehicleID, vs.proc.Trip.Trip.ID); err == nil {
			m.Hub.Publish(realtime.Message{Type: realtime.TypeTrip, VehicleID: w.vehicleID, Data: tr})
		}
	}
	if vs.proc.Charge != nil {
		if cs, err := m.Store.GetChargeSession(ctx, w.vehicleID, vs.proc.Charge.Session.ID); err == nil {
			m.Hub.Publish(realtime.Message{Type: realtime.TypeCharge, VehicleID: w.vehicleID, Data: cs})
		}
	}
	m.publishSegments(w.vehicleID, segs)
	m.deliver(ctx, t, vs, drafts)
}

func (m *Manager) publishSegments(vehicleID string, segs []analytics.Event) {
	for _, s := range segs {
		switch {
		case s.Trip != nil && s.Kind == analytics.TripEnded:
			m.Hub.Publish(realtime.Message{Type: realtime.TypeTrip, VehicleID: vehicleID, Data: s.Trip})
		case s.Charge != nil && s.Kind == analytics.ChargeEnded:
			m.Hub.Publish(realtime.Message{Type: realtime.TypeCharge, VehicleID: vehicleID, Data: s.Charge})
		}
	}
}

func (m *Manager) deliver(ctx context.Context, t store.PollTarget, vs *vstate, drafts []notify.Draft) {
	if len(drafts) == 0 || m.Dispatcher == nil {
		return
	}
	v, err := m.Store.GetVehicle(ctx, t.Vehicle.ID)
	if err != nil {
		return
	}
	for _, ev := range m.Dispatcher.Deliver(ctx, v, vs.prefs, drafts) {
		m.Hub.Publish(realtime.Message{Type: realtime.TypeEvent, VehicleID: v.ID, Data: ev})
	}
}
