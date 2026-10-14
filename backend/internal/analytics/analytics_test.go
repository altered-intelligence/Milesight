package analytics_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/analytics"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/sim"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/store/memory"
)

var t0 = time.Date(2026, 5, 4, 8, 0, 0, 0, time.UTC) // a Monday

type snap struct {
	at      time.Duration
	state   string
	shift   string
	speed   float64
	odo     float64
	pct     int
	rangeKm float64
	charge  string
	kw      float64
	energy  float64
	plugged bool
	fast    bool
	lat     float64
}

func (s snap) live() *models.LiveState {
	ls := &models.LiveState{State: s.state, UpdatedAt: t0.Add(s.at), OdometerKm: s.odo,
		Battery: models.Battery{Percent: s.pct, RangeKm: s.rangeKm, ChargeState: s.charge, PluggedIn: s.plugged}}
	if ls.State == "" {
		ls.State = models.StateOnline
	}
	if ls.Battery.ChargeState == "" {
		ls.Battery.ChargeState = models.ChargeDisconnected
	}
	ls.Drive = &models.Drive{ShiftState: s.shift, SpeedKph: s.speed}
	lat := s.lat
	if lat == 0 {
		lat = 37.0
	}
	ls.Location = &models.Location{Lat: lat, Lon: -122}
	if s.plugged {
		ls.Charging = &models.Charging{PowerKW: s.kw, EnergyAddedKWh: s.energy, FastCharger: s.fast}
	}
	return ls
}

func setup(t *testing.T) (*memory.Store, string) {
	st := memory.New()
	v := &store.VehicleRecord{VIN: "VIN-T", Model: "Model 3"}
	if err := st.UpsertVehicle(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	return st, v.ID
}

func env() analytics.Env {
	return analytics.Env{KWhPerRatedKm: 0.14, Currency: "USD", HomeRate: 0.20, FastRate: 0.50,
		Place: func(lat, lon float64) (string, *float64) {
			if math.Abs(lat-37.0) < 0.001 {
				r := 0.10
				return "Home", &r
			}
			return "", nil
		}}
}

func run(t *testing.T, st *memory.Store, vid string, snaps []snap) (*analytics.State, []analytics.Event) {
	t.Helper()
	p := &analytics.Processor{Sink: st}
	state := &analytics.State{}
	var all []analytics.Event
	for _, s := range snaps {
		evs, err := p.Process(context.Background(), state, vid, s.live(), env())
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, evs...)
	}
	return state, all
}

func TestTripSegmentation(t *testing.T) {
	st, vid := setup(t)
	state, events := run(t, st, vid, []snap{
		{at: 0, shift: "P", odo: 1000, pct: 80, rangeKm: 400},
		{at: 2 * time.Minute, shift: "D", speed: 30, odo: 1000.4, pct: 80, rangeKm: 399.5, lat: 37.01},
		{at: 3 * time.Minute, shift: "D", speed: 90, odo: 1002, pct: 79, rangeKm: 397, lat: 37.02},
		{at: 4 * time.Minute, shift: "D", speed: 0, odo: 1003, pct: 79, rangeKm: 395.9, lat: 37.03}, // red light
		{at: 12 * time.Minute, shift: "D", speed: 60, odo: 1010, pct: 77, rangeKm: 388, lat: 37.1},
		{at: 14 * time.Minute, shift: "P", odo: 1012, pct: 77, rangeKm: 386, lat: 37.12},
		{at: 20 * time.Minute, shift: "P", odo: 1012, pct: 77, rangeKm: 385.8, lat: 37.12},
	})
	if state.Trip != nil {
		t.Fatal("trip should be closed")
	}
	if len(events) != 2 || events[0].Kind != analytics.TripStarted || events[1].Kind != analytics.TripEnded {
		t.Fatalf("events: %+v", events)
	}
	trips, _ := st.ListTrips(context.Background(), vid, store.ListOptions{})
	if len(trips) != 1 {
		t.Fatalf("want 1 trip, got %d", len(trips))
	}
	tr := trips[0]
	if tr.DistanceKm != 12 || tr.StartOdometerKm != 1000 || tr.StartPlace != "Home" || tr.EndPlace != "" {
		t.Fatalf("trip: %+v", tr)
	}
	if tr.MaxSpeedKph != 90 || tr.EndedAt == nil || tr.EndBatteryPct != 77 {
		t.Fatalf("trip details: %+v", tr)
	}
	// energy = (400-386) × 0.14 = 1.96 kWh → 163.3 Wh/km over 12 km
	if math.Abs(tr.EnergyUsedKWh-1.96) > 1e-9 || tr.EfficiencyWhKm == nil || *tr.EfficiencyWhKm != 163.3 {
		t.Fatalf("energy/efficiency: %v %v", tr.EnergyUsedKWh, tr.EfficiencyWhKm)
	}
	// Departure happened between the parked sample (0 min) and first driving sample (2 min, 0.4 km in).
	if !tr.StartedAt.After(t0) || !tr.StartedAt.Before(t0.Add(2*time.Minute)) {
		t.Fatalf("start time not interpolated: %v", tr.StartedAt)
	}
	// The parked pair after arrival counts as drain.
	drain, _ := st.ListDrain(context.Background(), vid, t0, t0.Add(time.Hour))
	if len(drain) != 1 || math.Abs(drain[0].RangeLostKm-0.2) > 1e-9 {
		t.Fatalf("drain: %+v", drain)
	}
}

func TestTripEndsWhenCarFallsAsleep(t *testing.T) {
	st, vid := setup(t)
	_, events := run(t, st, vid, []snap{
		{at: 0, shift: "D", speed: 50, odo: 10, pct: 50, rangeKm: 250},
		{at: time.Minute, shift: "D", speed: 50, odo: 11, pct: 50, rangeKm: 249},
		{at: 30 * time.Minute, state: models.StateAsleep},
	})
	if len(events) != 2 || events[1].Kind != analytics.TripEnded {
		t.Fatalf("events: %+v", events)
	}
	if end := *events[1].Trip.EndedAt; !end.Equal(t0.Add(time.Minute)) {
		t.Fatalf("trip should end at the last data, got %v", end)
	}
}

func TestChargeSessionWithCostAndResume(t *testing.T) {
	st, vid := setup(t)
	_, events := run(t, st, vid, []snap{
		{at: 0, plugged: true, charge: models.ChargeStopped, odo: 5, pct: 40, rangeKm: 200}, // scheduled, waiting
		{at: 10 * time.Minute, plugged: true, charge: models.ChargeCharging, kw: 11, energy: 1.0, odo: 5, pct: 42, rangeKm: 208},
		{at: 70 * time.Minute, plugged: true, charge: models.ChargeCharging, kw: 11.5, energy: 12.0, odo: 5, pct: 57, rangeKm: 286},
		{at: 80 * time.Minute, plugged: true, charge: models.ChargeStopped, kw: 0, energy: 13.0, odo: 5, pct: 58, rangeKm: 291},
		// resume on the same plug: the energy counter keeps counting from 13.0
		{at: 90 * time.Minute, plugged: true, charge: models.ChargeCharging, kw: 11, energy: 13.5, odo: 5, pct: 59, rangeKm: 293},
		{at: 120 * time.Minute, plugged: true, charge: models.ChargeComplete, kw: 0, energy: 18.0, odo: 5, pct: 64, rangeKm: 320},
	})
	var kinds []analytics.EventKind
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if len(events) != 4 {
		t.Fatalf("want start/end ×2, got %v", kinds)
	}
	first, second := events[1].Charge, events[3].Charge
	if first.EndReason != "stopped" || second.EndReason != "complete" {
		t.Fatalf("reasons: %q %q", first.EndReason, second.EndReason)
	}
	if first.StartBatteryPct != 40 || !first.StartedAt.Equal(t0) {
		t.Fatalf("session should start from the plugged-waiting sample: %+v", first)
	}
	if first.EnergyAddedKWh != 13 || second.EnergyAddedKWh != 5 {
		t.Fatalf("energy: first=%v second=%v (offset handling)", first.EnergyAddedKWh, second.EnergyAddedKWh)
	}
	if first.Cost == nil || *first.Cost != 1.3 || first.Place != "Home" { // home geofence rate 0.10
		t.Fatalf("cost/place: %+v", first)
	}
	if first.MaxPowerKW != 11.5 || first.ChargerType != "ac" {
		t.Fatalf("power/type: %+v", first)
	}
}

func TestFastChargeUsesFastRate(t *testing.T) {
	st, vid := setup(t)
	_, events := run(t, st, vid, []snap{
		{at: 0, plugged: true, fast: true, charge: models.ChargeCharging, kw: 180, energy: 0.5, odo: 5, pct: 20, rangeKm: 100, lat: 38},
		{at: 20 * time.Minute, plugged: true, fast: true, charge: models.ChargeCharging, kw: 90, energy: 40, odo: 5, pct: 72, rangeKm: 360, lat: 38},
		{at: 21 * time.Minute, charge: models.ChargeDisconnected, odo: 5, pct: 72, rangeKm: 360, lat: 38},
	})
	end := events[len(events)-1].Charge
	if end.EndReason != "unplugged" || !end.FastCharger || end.ChargerType != "dc" || *end.Cost != 20 {
		t.Fatalf("fast session: %+v cost=%v", end, *end.Cost)
	}
}

func TestRollupBucketsAndDrainSplit(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	end := func(t time.Time) *time.Time { return &t }
	from := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 14)
	cost := 2.0
	trips := []models.Trip{
		{StartedAt: from.Add(9 * time.Hour), EndedAt: end(from.Add(10 * time.Hour)), DistanceKm: 20, EnergyUsedKWh: 3, DurationS: 3600},
		{StartedAt: from.Add(33 * time.Hour), EndedAt: end(from.Add(34 * time.Hour)), DistanceKm: 10, EnergyUsedKWh: 1.5, DurationS: 1800},
		{StartedAt: from.Add(8 * 24 * time.Hour), EndedAt: end(from.Add(8*24*time.Hour + time.Hour)), DistanceKm: 50, EnergyUsedKWh: 8, DurationS: 3000},
		{StartedAt: from.Add(9 * 24 * time.Hour)}, // in progress: excluded
	}
	charges := []models.ChargeSession{{StartedAt: from.Add(20 * time.Hour), EndedAt: end(from.Add(22 * time.Hour)), EnergyAddedKWh: 10, Cost: &cost}}
	drain := []models.DrainSegment{{StartedAt: from.Add(22 * time.Hour), EndedAt: from.Add(26 * time.Hour), RangeLostKm: 4, EnergyKWh: 0.5}}

	days, tot, err := analytics.Rollup(analytics.Daily, loc, from, to, trips, charges, drain)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 14 || days[0].Trips != 1 || days[1].Trips != 1 || days[0].Charges != 1 {
		t.Fatalf("daily: %d buckets %+v", len(days), days[:2])
	}
	if days[0].DrainKm != 2 || days[1].DrainKm != 2 { // 22:00–02:00 split at midnight
		t.Fatalf("drain split: %v %v", days[0].DrainKm, days[1].DrainKm)
	}
	if tot.DistanceKm != 80 || tot.Trips != 3 || tot.ChargingCost != 2 || *tot.EfficiencyWhKm != 156.3 {
		t.Fatalf("totals: %+v eff=%v", tot, *tot.EfficiencyWhKm)
	}
	weeks, _, _ := analytics.Rollup(analytics.Weekly, loc, from, to, trips, charges, drain)
	if len(weeks) != 2 || weeks[0].Trips != 2 || weeks[1].Trips != 1 || !weeks[0].Start.Equal(from.UTC()) {
		t.Fatalf("weekly: %+v", weeks)
	}
	months, _, _ := analytics.Rollup(analytics.Monthly, loc, from, to, trips, charges, drain)
	if len(months) != 1 || months[0].Trips != 3 {
		t.Fatalf("monthly: %+v", months)
	}
	if _, _, err := analytics.Rollup("hourly", loc, from, to, nil, nil, nil); err == nil {
		t.Fatal("bad period accepted")
	}
}

func TestBatteryHealth(t *testing.T) {
	spec := analytics.SpecFor("Model 3")
	var charges []models.ChargeSession
	for i := 0; i < 6; i++ {
		e := t0.AddDate(0, i, 0)
		// 72 kWh pack: 30 % → 80 % adds 36 kWh; full range 500 km.
		charges = append(charges, models.ChargeSession{EndedAt: &e, StartBatteryPct: 30, EndBatteryPct: 80,
			EnergyAddedKWh: 36, EndRangeKm: 400})
	}
	h := analytics.BatteryHealth(charges, spec, nil)
	if h.EstimatedCapacityKWh == nil || *h.EstimatedCapacityKWh != 72 || *h.FullRangeKm != 500 {
		t.Fatalf("health: %+v", h)
	}
	if *h.DegradationPct != 6.4 || len(h.History) != 6 { // 1 − 500/534
		t.Fatalf("degradation/history: %v %d", *h.DegradationPct, len(h.History))
	}
	if empty := analytics.BatteryHealth(nil, spec, nil); empty.DegradationPct != nil || empty.History == nil {
		t.Fatalf("empty health: %+v", empty)
	}
}

// TestSimulatedMonth runs a month of simulator history through the processor
// end to end and checks the derived analytics are plausible.
func TestSimulatedMonth(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s := sim.New(sim.Options{HomeLat: 37.3947, HomeLon: -122.1503, Vehicles: 1, Now: func() time.Time { return now }})
	vs, _ := s.ListVehicles(context.Background(), "u")
	samples, err := s.SeedHistory(context.Background(), "u", vs[0].VIN, 30)
	if err != nil {
		t.Fatal(err)
	}
	st, vid := setup(t)
	p := &analytics.Processor{Sink: st}
	state := &analytics.State{}
	e := env()
	e.KWhPerRatedKm = analytics.SpecFor("Model 3").KWhPerRatedKm()
	e.Place = nil
	for i := range samples {
		if _, err := p.Process(context.Background(), state, vid, &samples[i], e); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	trips, _ := st.ListTrips(ctx, vid, store.ListOptions{})
	charges, _ := st.ListChargeSessions(ctx, vid, store.ListOptions{})
	if len(trips) < 40 || len(charges) < 5 {
		t.Fatalf("too few segments: trips=%d charges=%d", len(trips), len(charges))
	}
	var dist, energy float64
	for _, tr := range trips {
		if tr.EndedAt == nil {
			continue
		}
		dist += tr.DistanceKm
		energy += tr.EnergyUsedKWh
		if tr.DistanceKm <= 0 || tr.DurationS <= 0 {
			t.Fatalf("degenerate trip: %+v", tr)
		}
	}
	eff := energy * 1000 / dist
	if eff < 100 || eff > 260 {
		t.Fatalf("fleet efficiency implausible: %.1f Wh/km over %.0f km", eff, dist)
	}
	health := analytics.BatteryHealth(charges, analytics.SpecFor("Model 3"), nil)
	if health.EstimatedCapacityKWh == nil || *health.EstimatedCapacityKWh < 60 || *health.EstimatedCapacityKWh > 90 {
		t.Fatalf("capacity estimate implausible: %+v", health.EstimatedCapacityKWh)
	}
	t.Logf("30 days: %d trips, %.0f km, %.0f Wh/km, %d charges, capacity≈%.1f kWh",
		len(trips), dist, eff, len(charges), *health.EstimatedCapacityKWh)
}
