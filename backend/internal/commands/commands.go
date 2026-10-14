// Package commands validates remote commands, translates them to Tesla
// commands, wakes the car when needed, rate-limits per vehicle and records an
// audit trail.
package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Errors surfaced to the API layer.
var (
	ErrForbidden   = errors.New("viewers cannot send commands")
	ErrRateLimited = errors.New("too many commands for this vehicle; slow down")
	ErrWakeTimeout = errors.New("vehicle did not wake up in time")
)

// ValidationError is a bad command or parameter.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Plan translates an API command into one or more Tesla commands.
func Plan(name string, p models.CommandParams, latest *models.LiveState) ([]transport.Command, error) {
	cmd := func(n string, body map[string]any) transport.Command { return transport.Command{Name: n, Body: body} }
	switch name {
	case models.CmdClimate:
		if p.On == nil {
			return nil, invalid("climate requires \"on\"")
		}
		if !*p.On {
			return []transport.Command{cmd("auto_conditioning_stop", nil)}, nil
		}
		var out []transport.Command
		if p.TempC != nil {
			if *p.TempC < 15 || *p.TempC > 28 || math.IsNaN(*p.TempC) {
				return nil, invalid("temp_c must be between 15 and 28")
			}
			t := math.Round(*p.TempC*2) / 2
			out = append(out, cmd("set_temps", map[string]any{"driver_temp": t, "passenger_temp": t}))
		}
		return append(out, cmd("auto_conditioning_start", nil)), nil
	case models.CmdLock:
		return []transport.Command{cmd("door_lock", nil)}, nil
	case models.CmdUnlock:
		return []transport.Command{cmd("door_unlock", nil)}, nil
	case models.CmdFrunk:
		return []transport.Command{cmd("actuate_trunk", map[string]any{"which_trunk": "front"})}, nil
	case models.CmdTrunk:
		return []transport.Command{cmd("actuate_trunk", map[string]any{"which_trunk": "rear"})}, nil
	case models.CmdChargePort:
		return []transport.Command{cmd("charge_port_door_open", nil)}, nil
	case models.CmdChargePortClose:
		return []transport.Command{cmd("charge_port_door_close", nil)}, nil
	case models.CmdHonk:
		return []transport.Command{cmd("honk_horn", nil)}, nil
	case models.CmdFlashLights:
		return []transport.Command{cmd("flash_lights", nil)}, nil
	case models.CmdSentry:
		if p.On == nil {
			return nil, invalid("sentry requires \"on\"")
		}
		return []transport.Command{cmd("set_sentry_mode", map[string]any{"on": *p.On})}, nil
	case models.CmdCharging:
		switch p.Action {
		case "start":
			return []transport.Command{cmd("charge_start", nil)}, nil
		case "stop":
			return []transport.Command{cmd("charge_stop", nil)}, nil
		case "set_amps":
			if p.Amps == nil || *p.Amps < 1 || *p.Amps > 48 {
				return nil, invalid("amps must be between 1 and 48")
			}
			return []transport.Command{cmd("set_charging_amps", map[string]any{"charging_amps": *p.Amps})}, nil
		case "set_limit":
			if p.LimitPct == nil || *p.LimitPct < 50 || *p.LimitPct > 100 {
				return nil, invalid("limit_pct must be between 50 and 100")
			}
			return []transport.Command{cmd("set_charge_limit", map[string]any{"percent": *p.LimitPct})}, nil
		default:
			return nil, invalid("charging action must be start, stop, set_amps or set_limit")
		}
	case models.CmdWindows:
		if p.Action != "vent" && p.Action != "close" {
			return nil, invalid("windows action must be vent or close")
		}
		body := map[string]any{"command": p.Action, "lat": 0.0, "lon": 0.0}
		if latest != nil && latest.Location != nil { // Tesla requires proximity for closing
			body["lat"], body["lon"] = latest.Location.Lat, latest.Location.Lon
		}
		return []transport.Command{cmd("window_control", body)}, nil
	}
	return nil, invalid("unknown command %q", name)
}

// Poker is notified after commands so ingestion re-reads the car promptly.
type Poker interface {
	Poke(vehicleID string, schedule []time.Duration)
}

// Service executes commands.
type Service struct {
	Store      store.Store
	Transports transport.Registry
	Poker      Poker
	Log        *slog.Logger
	Now        func() time.Time
	// WakeTimeout bounds wake-then-send; WakePoll is the state-check interval.
	WakeTimeout time.Duration
	WakePoll    time.Duration
	Limit       int           // commands per window per vehicle
	Window      time.Duration // rate-limit window

	mu      sync.Mutex
	history map[string][]time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// allow implements a sliding-window limiter per vehicle (Tesla allows 30
// commands/min per vehicle across all apps; we stay well below).
func (s *Service) allow(vehicleID string) bool {
	limit, window := s.Limit, s.Window
	if limit <= 0 {
		limit = 12
	}
	if window <= 0 {
		window = time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history == nil {
		s.history = map[string][]time.Time{}
	}
	now := s.now()
	h := s.history[vehicleID]
	kept := h[:0]
	for _, t := range h {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.history[vehicleID] = kept
		return false
	}
	s.history[vehicleID] = append(kept, now)
	return true
}

// Execute validates, wakes if needed, sends and audits one command.
func (s *Service) Execute(ctx context.Context, userID string, v *store.VehicleRecord, role, name string, p models.CommandParams) (*models.CommandResult, error) {
	if role != "owner" {
		return nil, ErrForbidden
	}
	plan, err := Plan(name, p, v.Latest)
	if err != nil {
		return nil, err
	}
	if !s.allow(v.ID) {
		return nil, ErrRateLimited
	}
	cred, err := s.Store.GetCredentials(ctx, userID)
	if err != nil {
		return nil, transport.ErrReauthRequired
	}
	tr, err := s.Transports.Get(cred.Transport)
	if err != nil {
		return nil, err
	}
	res := &models.CommandResult{RequestID: ids.New(), Command: name, Status: "ok"}
	woke, err := s.ensureAwake(ctx, tr, userID, v.VIN)
	res.WokeUp = woke
	if err == nil {
		for _, c := range plan {
			if err = tr.Command(ctx, userID, v.VIN, c); err != nil {
				break
			}
		}
	}
	res.At = s.now()
	var ce *transport.CommandError
	switch {
	case err == nil:
	case errors.As(err, &ce):
		res.Status, res.Reason = "failed", ce.Reason
	default:
		res.Status, res.Reason = "failed", err.Error()
	}
	if logErr := s.Store.InsertCommandLog(ctx, &store.CommandLog{VehicleID: v.ID, UserID: userID, Name: name, Params: p,
		Status: res.Status, Reason: res.Reason, CreatedAt: res.At}); logErr != nil {
		s.Log.Warn("commands: audit log", "err", logErr)
	}
	if s.Poker != nil {
		s.Poker.Poke(v.ID, nil)
	}
	if err != nil && ce == nil {
		return res, err // transport-level failure: surfaced as an HTTP error
	}
	return res, nil
}

// ensureAwake wakes a sleeping car and waits until it reports online.
func (s *Service) ensureAwake(ctx context.Context, tr transport.Transport, userID, vin string) (bool, error) {
	state, err := tr.VehicleState(ctx, userID, vin)
	if err != nil {
		return false, err
	}
	if state == models.StateOnline {
		return false, nil
	}
	if err := tr.Wake(ctx, userID, vin); err != nil {
		return false, err
	}
	timeout, every := s.WakeTimeout, s.WakePoll
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	if every <= 0 {
		every = 2 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if st, err := tr.VehicleState(ctx, userID, vin); err == nil && st == models.StateOnline {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-deadline.C:
			return true, ErrWakeTimeout
		case <-tick.C:
		}
	}
}

// Wake wakes a vehicle on explicit user request.
func (s *Service) Wake(ctx context.Context, userID string, v *store.VehicleRecord) (bool, error) {
	cred, err := s.Store.GetCredentials(ctx, userID)
	if err != nil {
		return false, transport.ErrReauthRequired
	}
	tr, err := s.Transports.Get(cred.Transport)
	if err != nil {
		return false, err
	}
	woke, err := s.ensureAwake(ctx, tr, userID, v.VIN)
	if s.Poker != nil {
		s.Poker.Poke(v.ID, []time.Duration{0, 3 * time.Second})
	}
	return woke, err
}
