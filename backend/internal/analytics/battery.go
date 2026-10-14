package analytics

import (
	"math"
	"sort"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

type healthSample struct {
	at        time.Time
	capacity  *float64
	fullRange *float64
}

// BatteryHealth estimates usable capacity and full rated range from completed
// charging sessions (and the latest snapshot), smoothing with medians so single
// noisy sessions don't move the numbers.
//
//   - capacity  ≈ energy added ÷ (Δ state of charge), sessions with Δ ≥ 15 % and ≥ 5 kWh
//   - full range ≈ rated range ÷ state of charge, at ≥ 50 % SoC
func BatteryHealth(charges []models.ChargeSession, spec Spec, latest *models.LiveState) models.BatteryHealth {
	var samples []healthSample
	for _, c := range charges {
		if c.EndedAt == nil {
			continue
		}
		var s healthSample
		s.at = *c.EndedAt
		if d := c.EndBatteryPct - c.StartBatteryPct; d >= 15 && c.EnergyAddedKWh >= 5 {
			v := c.EnergyAddedKWh / (float64(d) / 100)
			if v > spec.CapacityKWh*0.5 && v < spec.CapacityKWh*1.3 {
				s.capacity = &v
			}
		}
		if c.EndBatteryPct >= 50 && c.EndRangeKm > 0 {
			v := c.EndRangeKm / (float64(c.EndBatteryPct) / 100)
			if v > spec.FullRangeKm*0.5 && v < spec.FullRangeKm*1.3 {
				s.fullRange = &v
			}
		}
		if s.capacity != nil || s.fullRange != nil {
			samples = append(samples, s)
		}
	}
	if latest != nil && latest.Battery.Percent >= 50 && latest.Battery.RangeKm > 0 {
		v := latest.Battery.RangeKm / (float64(latest.Battery.Percent) / 100)
		samples = append(samples, healthSample{at: latest.UpdatedAt, fullRange: &v})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].at.Before(samples[j].at) })

	h := models.BatteryHealth{
		NominalCapacityKWh: spec.CapacityKWh, NominalFullRangeKm: spec.FullRangeKm,
		Samples: len(samples), History: []models.BatteryHealthPoint{},
	}
	recent := func(get func(healthSample) *float64) *float64 {
		var vals []float64
		for i := len(samples) - 1; i >= 0 && len(vals) < 10; i-- {
			if v := get(samples[i]); v != nil {
				vals = append(vals, *v)
			}
		}
		if len(vals) == 0 {
			return nil
		}
		m := round(median(vals), 1)
		return &m
	}
	h.EstimatedCapacityKWh = recent(func(s healthSample) *float64 { return s.capacity })
	h.FullRangeKm = recent(func(s healthSample) *float64 { return s.fullRange })
	if h.FullRangeKm != nil {
		d := round(math.Max(0, (1-*h.FullRangeKm/spec.FullRangeKm)*100), 1)
		h.DegradationPct = &d
	} else if h.EstimatedCapacityKWh != nil {
		d := round(math.Max(0, (1-*h.EstimatedCapacityKWh/spec.CapacityKWh)*100), 1)
		h.DegradationPct = &d
	}

	// Monthly history (UTC months).
	type acc struct{ caps, ranges []float64 }
	months := map[time.Time]*acc{}
	var keys []time.Time
	for _, s := range samples {
		m := time.Date(s.at.Year(), s.at.Month(), 1, 0, 0, 0, 0, time.UTC)
		a, ok := months[m]
		if !ok {
			a = &acc{}
			months[m] = a
			keys = append(keys, m)
		}
		if s.capacity != nil {
			a.caps = append(a.caps, *s.capacity)
		}
		if s.fullRange != nil {
			a.ranges = append(a.ranges, *s.fullRange)
		}
	}
	for _, m := range keys {
		a := months[m]
		p := models.BatteryHealthPoint{Month: m, Samples: len(a.caps) + len(a.ranges)}
		if len(a.caps) > 0 {
			v := round(median(a.caps), 1)
			p.CapacityKWh = &v
		}
		if len(a.ranges) > 0 {
			v := round(median(a.ranges), 1)
			p.FullRangeKm = &v
		}
		h.History = append(h.History, p)
	}
	return h
}
