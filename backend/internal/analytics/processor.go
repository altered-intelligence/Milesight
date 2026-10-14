package analytics

import (
	"context"
	"math"
	"time"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
)

// Sink persists derived records (implemented by store.Store).
type Sink interface {
	SaveTrip(ctx context.Context, t *models.Trip) error
	SaveChargeSession(ctx context.Context, c *models.ChargeSession) error
	InsertDrain(ctx context.Context, d models.DrainSegment) error
}

// EventKind enumerates segmentation events.
type EventKind string

// Segmentation events emitted by Process.
const (
	TripStarted   EventKind = "trip_started"
	TripEnded     EventKind = "trip_ended"
	ChargeStarted EventKind = "charge_started"
	ChargeEnded   EventKind = "charge_ended"
)

// Event reports a segment boundary.
type Event struct {
	Kind   EventKind
	Trip   *models.Trip
	Charge *models.ChargeSession
}

// Env is per-vehicle context for processing.
type Env struct {
	KWhPerRatedKm float64
	Currency      string
	HomeRate      float64 // default AC rate per kWh
	FastRate      float64 // DC fast charging rate per kWh
	// Place resolves a coordinate to a user place name and its optional energy rate.
	Place func(lat, lon float64) (name string, rate *float64)
}

// Sample is the subset of a snapshot the state machine needs.
type Sample struct {
	Time        time.Time `json:"t"`
	Online      bool      `json:"o"`
	Driving     bool      `json:"d"`
	Charging    bool      `json:"c"`
	Plugged     bool      `json:"p"`
	ChargeState string    `json:"cs,omitempty"`
	OdometerKm  float64   `json:"odo"`
	RangeKm     float64   `json:"r"`
	BatteryPct  int       `json:"b"`
	Lat         *float64  `json:"lat,omitempty"`
	Lon         *float64  `json:"lon,omitempty"`
	SpeedKph    float64   `json:"v"`
	ChargerKW   float64   `json:"kw"`
	EnergyAdded float64   `json:"e"`
	Fast        bool      `json:"f"`
	ChargerType string    `json:"ct,omitempty"`
	OutsideC    *float64  `json:"oc,omitempty"`
}

// SampleOf extracts a Sample from a snapshot.
func SampleOf(ls *models.LiveState) Sample {
	s := Sample{Time: ls.UpdatedAt, Online: ls.State == models.StateOnline}
	if !s.Online {
		return s
	}
	s.Driving, s.Charging = ls.IsDriving(), ls.IsCharging()
	s.Plugged, s.ChargeState = ls.Battery.PluggedIn, ls.Battery.ChargeState
	s.OdometerKm, s.RangeKm, s.BatteryPct = ls.OdometerKm, ls.Battery.RangeKm, ls.Battery.Percent
	if l := ls.Location; l != nil {
		lat, lon := l.Lat, l.Lon
		s.Lat, s.Lon = &lat, &lon
	}
	if d := ls.Drive; d != nil {
		s.SpeedKph = d.SpeedKph
	}
	if c := ls.Charging; c != nil {
		s.ChargerKW, s.EnergyAdded, s.Fast, s.ChargerType = c.PowerKW, c.EnergyAddedKWh, c.FastCharger, c.FastChargerType
	}
	if c := ls.Climate; c != nil && c.OutsideTempC != nil {
		t := *c.OutsideTempC
		s.OutsideC = &t
	}
	return s
}

// State is the persisted cursor of the state machine (JSON in vehicles.processor_state).
type State struct {
	Last     *Sample      `json:"last,omitempty"`
	LastData *Sample      `json:"last_data,omitempty"` // last online sample
	Trip     *tripAcc     `json:"trip,omitempty"`
	Charge   *chargeAcc   `json:"charge,omitempty"`
	Notify   *NotifyState `json:"notify,omitempty"`
}

// NotifyState is owned by the notification engine but persisted alongside.
type NotifyState struct {
	UnlockedSince   *time.Time `json:"unlocked_since,omitempty"`
	UnlockedAlerted bool       `json:"unlocked_alerted,omitempty"`
	OpenSince       *time.Time `json:"open_since,omitempty"`
	OpenAlerted     bool       `json:"open_alerted,omitempty"`
	LowFired        bool       `json:"low_fired,omitempty"` // re-armed once 5 points above threshold
	Place           string     `json:"place,omitempty"`
	PlaceKnown      bool       `json:"place_known,omitempty"`
	UpdateNotified  string     `json:"update_notified,omitempty"`
}

type tripAcc struct {
	Trip    models.Trip `json:"trip"`
	TempSum float64     `json:"temp_sum"`
	TempN   int         `json:"temp_n"`
}

type chargeAcc struct {
	Session  models.ChargeSession `json:"session"`
	Offset   float64              `json:"offset"`
	PowerSum float64              `json:"power_sum"`
	PowerN   int                  `json:"power_n"`
	Rate     *float64             `json:"rate,omitempty"`
}

// Processor runs the segmentation state machine.
type Processor struct {
	Sink Sink
}

// maxGap ends an open trip if no driving data arrives for this long.
const maxGap = 15 * time.Minute

// Process consumes one snapshot for vehicleID and returns the boundaries it crossed.
func (p *Processor) Process(ctx context.Context, st *State, vehicleID string, ls *models.LiveState, env Env) ([]Event, error) {
	cur := SampleOf(ls)
	if st.Last != nil && !cur.Time.After(st.Last.Time) {
		return nil, nil // out-of-order or duplicate sample
	}
	var events []Event
	emit := func(e Event, err error) error {
		if err == nil && e.Kind != "" {
			events = append(events, e)
		}
		return err
	}

	// ---- trips
	if st.Trip != nil {
		lastDriveGap := cur.Time.Sub(st.Trip.Trip.StartedAt)
		if st.LastData != nil {
			lastDriveGap = cur.Time.Sub(st.LastData.Time)
		}
		switch {
		case cur.Driving && lastDriveGap <= maxGap:
			if err := p.updateTrip(ctx, st, cur, env); err != nil {
				return events, err
			}
		default:
			end := cur
			if !cur.Online || cur.Driving { // asleep, or a data hole: end where we last saw it
				end = *st.LastData
			}
			if err := emit(p.endTrip(ctx, st, end, env)); err != nil {
				return events, err
			}
		}
	}
	if st.Trip == nil && cur.Driving {
		if err := emit(p.startTrip(ctx, st, vehicleID, cur, env)); err != nil {
			return events, err
		}
	}

	// ---- charging
	if st.Charge != nil {
		if cur.Charging {
			p.updateCharge(st, cur)
			if err := p.Sink.SaveChargeSession(ctx, p.chargeSnapshot(st, cur, env, false)); err != nil {
				return events, err
			}
		} else {
			if err := emit(p.endCharge(ctx, st, cur, env)); err != nil {
				return events, err
			}
		}
	}
	if st.Charge == nil && cur.Charging {
		if err := emit(p.startCharge(ctx, st, vehicleID, cur, env)); err != nil {
			return events, err
		}
	}

	// ---- vampire drain: two consecutive parked, not-charging data samples with no movement.
	if cur.Online && st.LastData != nil {
		prev := st.LastData
		gap := cur.Time.Sub(prev.Time)
		parked := !prev.Driving && !cur.Driving && !prev.Charging && !cur.Charging &&
			math.Abs(cur.OdometerKm-prev.OdometerKm) < 0.1 && prev.RangeKm > 0 && cur.RangeKm > 0
		if parked && gap >= time.Minute {
			lost := prev.RangeKm - cur.RangeKm
			// Ignore implausible jumps (range recalibration / missed charge).
			if lost > -5 && lost < 40 {
				if err := p.Sink.InsertDrain(ctx, models.DrainSegment{VehicleID: vehicleID, StartedAt: prev.Time, EndedAt: cur.Time,
					RangeLostKm: round(lost, 2), EnergyKWh: round(math.Max(0, lost)*env.KWhPerRatedKm, 3)}); err != nil {
					return events, err
				}
			}
		}
	}

	c := cur
	st.Last = &c
	if cur.Online {
		st.LastData = &c
	}
	return events, nil
}

func (p *Processor) place(env Env, lat, lon *float64) (string, *float64) {
	if env.Place == nil || lat == nil || lon == nil {
		return "", nil
	}
	return env.Place(*lat, *lon)
}

func geoPoint(lat, lon *float64) *models.GeoPoint {
	if lat == nil || lon == nil {
		return nil
	}
	return &models.GeoPoint{Lat: *lat, Lon: *lon}
}

func (p *Processor) startTrip(ctx context.Context, st *State, vehicleID string, cur Sample, env Env) (Event, error) {
	start := cur
	startedAt := cur.Time
	// Use the last parked sample as the origin when it is recent: it has the true
	// start odometer/battery/location (the first driving sample is mid-route).
	if prev := st.LastData; prev != nil && !prev.Driving && cur.Time.Sub(prev.Time) <= maxGap && prev.OdometerKm <= cur.OdometerKm {
		start = *prev
		if cur.OdometerKm-prev.OdometerKm >= 0.3 {
			// We missed the departure; it happened between the two samples.
			moved := cur.OdometerKm - prev.OdometerKm
			guess := time.Duration(moved/math.Max(cur.SpeedKph, 30)*3600) * time.Second
			startedAt = cur.Time.Add(-guess)
			if startedAt.Before(prev.Time) {
				startedAt = prev.Time
			}
		}
	}
	name, _ := p.place(env, start.Lat, start.Lon)
	acc := &tripAcc{Trip: models.Trip{
		ID: ids.New(), VehicleID: vehicleID, StartedAt: startedAt.UTC(),
		StartOdometerKm: start.OdometerKm, EndOdometerKm: cur.OdometerKm,
		StartBatteryPct: start.BatteryPct, EndBatteryPct: cur.BatteryPct,
		StartRangeKm: start.RangeKm, EndRangeKm: cur.RangeKm,
		StartLocation: geoPoint(start.Lat, start.Lon), StartPlace: name,
	}}
	st.Trip = acc
	p.accumulateTrip(acc, cur, env)
	if err := p.Sink.SaveTrip(ctx, &acc.Trip); err != nil {
		return Event{}, err
	}
	t := acc.Trip
	return Event{Kind: TripStarted, Trip: &t}, nil
}

func (p *Processor) accumulateTrip(acc *tripAcc, cur Sample, env Env) {
	t := &acc.Trip
	if cur.SpeedKph > t.MaxSpeedKph {
		t.MaxSpeedKph = cur.SpeedKph
	}
	if cur.OutsideC != nil {
		acc.TempSum += *cur.OutsideC
		acc.TempN++
	}
	if cur.OdometerKm >= t.StartOdometerKm {
		t.EndOdometerKm = cur.OdometerKm
	}
	t.EndBatteryPct, t.EndRangeKm = cur.BatteryPct, cur.RangeKm
	if cur.Lat != nil {
		t.EndLocation = geoPoint(cur.Lat, cur.Lon)
	}
	p.finishMetrics(acc, cur.Time, env)
}

func (p *Processor) finishMetrics(acc *tripAcc, at time.Time, env Env) {
	t := &acc.Trip
	t.DistanceKm = round(math.Max(0, t.EndOdometerKm-t.StartOdometerKm), 2)
	t.DurationS = int(math.Max(0, at.Sub(t.StartedAt).Seconds()))
	if t.DurationS > 0 {
		t.AvgSpeedKph = round(t.DistanceKm/(float64(t.DurationS)/3600), 1)
	}
	t.EnergyUsedKWh = round((t.StartRangeKm-t.EndRangeKm)*env.KWhPerRatedKm, 3)
	t.EfficiencyWhKm = nil
	if t.DistanceKm >= 0.5 {
		e := round(t.EnergyUsedKWh*1000/t.DistanceKm, 1)
		t.EfficiencyWhKm = &e
	}
	if acc.TempN > 0 {
		avg := round(acc.TempSum/float64(acc.TempN), 1)
		t.OutsideTempC = &avg
	}
}

func (p *Processor) updateTrip(ctx context.Context, st *State, cur Sample, env Env) error {
	p.accumulateTrip(st.Trip, cur, env)
	return p.Sink.SaveTrip(ctx, &st.Trip.Trip)
}

func (p *Processor) endTrip(ctx context.Context, st *State, end Sample, env Env) (Event, error) {
	acc := st.Trip
	st.Trip = nil
	if end.Online && end.OdometerKm >= acc.Trip.StartOdometerKm {
		acc.Trip.EndOdometerKm = end.OdometerKm
		acc.Trip.EndBatteryPct, acc.Trip.EndRangeKm = end.BatteryPct, end.RangeKm
		if end.Lat != nil {
			acc.Trip.EndLocation = geoPoint(end.Lat, end.Lon)
		}
	}
	endedAt := end.Time.UTC()
	if endedAt.Before(acc.Trip.StartedAt) {
		endedAt = acc.Trip.StartedAt
	}
	acc.Trip.EndedAt = &endedAt
	p.finishMetrics(acc, endedAt, env)
	if loc := acc.Trip.EndLocation; loc != nil {
		acc.Trip.EndPlace, _ = p.place(env, &loc.Lat, &loc.Lon)
	}
	if err := p.Sink.SaveTrip(ctx, &acc.Trip); err != nil {
		return Event{}, err
	}
	t := acc.Trip
	return Event{Kind: TripEnded, Trip: &t}, nil
}

func (p *Processor) startCharge(ctx context.Context, st *State, vehicleID string, cur Sample, env Env) (Event, error) {
	start := cur
	offset := 0.0
	// Plugged-in but idle just before (scheduled charging / resume after stop):
	// start from that sample; reuse its energy counter as the session offset.
	if prev := st.LastData; prev != nil && prev.Plugged && !prev.Charging && cur.Time.Sub(prev.Time) <= maxGap {
		start = *prev
		if prev.EnergyAdded > 0 && cur.EnergyAdded >= prev.EnergyAdded {
			offset = prev.EnergyAdded
		}
	}
	name, rate := p.place(env, cur.Lat, cur.Lon)
	acc := &chargeAcc{Offset: offset, Rate: rate, Session: models.ChargeSession{
		ID: ids.New(), VehicleID: vehicleID, StartedAt: start.Time.UTC(),
		StartBatteryPct: start.BatteryPct, StartRangeKm: start.RangeKm,
		Location: geoPoint(cur.Lat, cur.Lon), Place: name, Currency: env.Currency,
	}}
	st.Charge = acc
	p.updateCharge(st, cur)
	s := p.chargeSnapshot(st, cur, env, false)
	if err := p.Sink.SaveChargeSession(ctx, s); err != nil {
		return Event{}, err
	}
	return Event{Kind: ChargeStarted, Charge: s}, nil
}

func (p *Processor) updateCharge(st *State, cur Sample) {
	acc := st.Charge
	s := &acc.Session
	if cur.ChargerKW > s.MaxPowerKW {
		s.MaxPowerKW = cur.ChargerKW
	}
	if cur.ChargerKW > 0 {
		acc.PowerSum += cur.ChargerKW
		acc.PowerN++
	}
	if cur.Fast {
		s.FastCharger = true
		if cur.ChargerType != "" {
			s.ChargerType = cur.ChargerType
		}
	}
	s.EndBatteryPct, s.EndRangeKm = cur.BatteryPct, cur.RangeKm
	if e := cur.EnergyAdded - acc.Offset; e >= s.EnergyAddedKWh {
		s.EnergyAddedKWh = round(e, 3)
	} else if cur.EnergyAdded > s.EnergyAddedKWh { // counter reset (re-plug) mid-session
		s.EnergyAddedKWh = round(cur.EnergyAdded, 3)
	}
}

// chargeSnapshot returns a copy of the session with derived fields filled.
func (p *Processor) chargeSnapshot(st *State, at Sample, env Env, final bool) *models.ChargeSession {
	acc := st.Charge
	s := acc.Session
	s.DurationS = int(math.Max(0, at.Time.Sub(s.StartedAt).Seconds()))
	if acc.PowerN > 0 {
		s.AvgPowerKW = round(acc.PowerSum/float64(acc.PowerN), 1)
	}
	if s.ChargerType == "" {
		s.ChargerType = "ac"
		if s.FastCharger {
			s.ChargerType = "dc"
		}
	}
	rate := env.HomeRate
	if s.FastCharger {
		rate = env.FastRate
	}
	if acc.Rate != nil {
		rate = *acc.Rate
	}
	cost := round(s.EnergyAddedKWh*rate, 2)
	s.Cost = &cost
	if final {
		e := at.Time.UTC()
		s.EndedAt = &e
	}
	return &s
}

func (p *Processor) endCharge(ctx context.Context, st *State, cur Sample, env Env) (Event, error) {
	end := cur
	reason := "asleep"
	if cur.Online {
		switch cur.ChargeState {
		case models.ChargeComplete:
			reason = "complete"
		case models.ChargeDisconnected:
			reason = "unplugged"
		default:
			reason = "stopped"
		}
		if !cur.Plugged {
			reason = "unplugged"
		}
		st.Charge.Session.EndBatteryPct, st.Charge.Session.EndRangeKm = cur.BatteryPct, cur.RangeKm
		if cur.Plugged && cur.EnergyAdded-st.Charge.Offset > st.Charge.Session.EnergyAddedKWh {
			st.Charge.Session.EnergyAddedKWh = round(cur.EnergyAdded-st.Charge.Offset, 3)
		}
	} else if st.LastData != nil {
		end = *st.LastData
	}
	st.Charge.Session.EndReason = reason
	s := p.chargeSnapshot(st, end, env, true)
	st.Charge = nil
	if err := p.Sink.SaveChargeSession(ctx, s); err != nil {
		return Event{}, err
	}
	return Event{Kind: ChargeEnded, Charge: s}, nil
}
