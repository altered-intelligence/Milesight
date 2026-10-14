// Package analytics turns the raw telemetry stream into trips, charging
// sessions, vampire drain, rollups and battery-health estimates. Segmentation is
// a deterministic, incremental state machine so it works identically for live
// polling, streaming and backfilled history.
package analytics

import (
	"math"
	"sort"

	"github.com/altered-intelligence/milesight/internal/geo"
	"github.com/altered-intelligence/milesight/internal/models"
)

// Spec is the nominal pack and rated range for a model (used until enough
// charging data exists to learn the vehicle's own numbers).
type Spec struct {
	CapacityKWh float64
	FullRangeKm float64
}

// SpecFor returns nominal figures for a model display name.
func SpecFor(model string) Spec {
	switch model {
	case "Model 3":
		return Spec{CapacityKWh: 75, FullRangeKm: 534}
	case "Model Y":
		return Spec{CapacityKWh: 75, FullRangeKm: 497}
	case "Model S":
		return Spec{CapacityKWh: 95, FullRangeKm: 600}
	case "Model X":
		return Spec{CapacityKWh: 95, FullRangeKm: 539}
	case "Cybertruck":
		return Spec{CapacityKWh: 123, FullRangeKm: 547}
	default:
		return Spec{CapacityKWh: 75, FullRangeKm: 500}
	}
}

// KWhPerRatedKm is the energy represented by one km of rated range.
func (s Spec) KWhPerRatedKm() float64 { return s.CapacityKWh / s.FullRangeKm }

// LearnedKWhPerRatedKm estimates the vehicle's own energy-per-rated-km from
// completed AC charging sessions (energy added ÷ rated range added), falling
// back to the nominal spec with fewer than three usable sessions.
func LearnedKWhPerRatedKm(charges []models.ChargeSession, spec Spec) float64 {
	var ratios []float64
	for _, c := range charges {
		added := c.EndRangeKm - c.StartRangeKm
		if c.EndedAt == nil || added < 30 || c.EnergyAddedKWh < 4 {
			continue
		}
		r := c.EnergyAddedKWh / added
		// Reject outliers (bad samples): within ±40 % of nominal.
		if nom := spec.KWhPerRatedKm(); r > nom*0.6 && r < nom*1.4 {
			ratios = append(ratios, r)
		}
	}
	if len(ratios) < 3 {
		return spec.KWhPerRatedKm()
	}
	return median(ratios)
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n == 0 {
		return math.NaN()
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func ptr(v float64) *float64 { return &v }

func boolp(v bool) *bool { return &v }

// TelemetryFromLive flattens a live snapshot into a telemetry row.
func TelemetryFromLive(ls *models.LiveState) models.TelemetryPoint {
	p := models.TelemetryPoint{Time: ls.UpdatedAt, State: ls.State}
	if ls.State != models.StateOnline {
		return p
	}
	b := ls.Battery
	p.BatteryPct = ptr(float64(b.Percent))
	p.UsableBatteryPct = ptr(float64(b.UsablePercent))
	p.RatedRangeKm = ptr(b.RangeKm)
	if b.IdealRangeKm > 0 {
		p.IdealRangeKm = ptr(b.IdealRangeKm)
	}
	if b.EstRangeKm > 0 {
		p.EstRangeKm = ptr(b.EstRangeKm)
	}
	p.ChargeLimitPct = ptr(float64(b.ChargeLimitPct))
	p.ChargeState = b.ChargeState
	if ls.OdometerKm > 0 {
		p.OdometerKm = ptr(ls.OdometerKm)
	}
	if l := ls.Location; l != nil {
		p.Lat, p.Lon, p.HeadingDeg = ptr(l.Lat), ptr(l.Lon), ptr(float64(l.HeadingDeg))
	}
	if d := ls.Drive; d != nil {
		p.ShiftState = d.ShiftState
		p.SpeedKph = ptr(d.SpeedKph)
		p.PowerKW = ptr(d.PowerKW)
	}
	if c := ls.Climate; c != nil {
		p.InsideTempC, p.OutsideTempC, p.ClimateOn = c.InsideTempC, c.OutsideTempC, boolp(c.On)
	}
	if c := ls.Charging; c != nil {
		p.ChargerPowerKW = ptr(c.PowerKW)
		p.ChargerVoltage = ptr(float64(c.Voltage))
		p.ChargerAmps = ptr(float64(c.Amps))
		p.ChargeEnergyAddedKWh = ptr(c.EnergyAddedKWh)
	}
	if c := ls.Closures; c != nil {
		p.Locked, p.SentryMode = boolp(c.Locked), boolp(c.SentryMode)
	}
	return p
}

func round(v float64, n int) float64 { return geo.Round(v, n) }
