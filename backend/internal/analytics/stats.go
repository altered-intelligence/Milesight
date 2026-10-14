package analytics

import (
	"fmt"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

// Periods accepted by Rollup.
const (
	Daily   = "daily"
	Weekly  = "weekly"
	Monthly = "monthly"
)

// periodStart returns the start of the period containing t in loc.
func periodStart(period string, t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	day := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	switch period {
	case Weekly: // ISO weeks start Monday
		off := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -off)
	case Monthly:
		return time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, loc)
	default:
		return day
	}
}

func nextStart(period string, s time.Time) time.Time {
	switch period {
	case Weekly:
		return s.AddDate(0, 0, 7)
	case Monthly:
		return s.AddDate(0, 1, 0)
	default:
		return s.AddDate(0, 0, 1)
	}
}

// Rollup aggregates trips, charges and drain into calendar buckets covering
// [from, to) in loc. Only completed trips/sessions count.
func Rollup(period string, loc *time.Location, from, to time.Time,
	trips []models.Trip, charges []models.ChargeSession, drain []models.DrainSegment) ([]models.StatsBucket, models.StatsBucket, error) {

	switch period {
	case Daily, Weekly, Monthly:
	default:
		return nil, models.StatsBucket{}, fmt.Errorf("period must be daily, weekly or monthly")
	}
	if !to.After(from) {
		return nil, models.StatsBucket{}, fmt.Errorf("to must be after from")
	}
	var buckets []models.StatsBucket
	index := map[int64]int{}
	for s := periodStart(period, from, loc); s.Before(to); s = nextStart(period, s) {
		index[s.Unix()] = len(buckets)
		buckets = append(buckets, models.StatsBucket{Start: s.UTC(), End: nextStart(period, s).UTC()})
		if len(buckets) > 2000 {
			return nil, models.StatsBucket{}, fmt.Errorf("range too large for %s buckets", period)
		}
	}
	find := func(t time.Time) *models.StatsBucket {
		if t.Before(from) || !t.Before(to) {
			return nil
		}
		if i, ok := index[periodStart(period, t, loc).Unix()]; ok {
			return &buckets[i]
		}
		return nil
	}
	for _, t := range trips {
		if t.EndedAt == nil {
			continue
		}
		if b := find(t.StartedAt); b != nil {
			b.Trips++
			b.DistanceKm += t.DistanceKm
			b.DriveTimeS += t.DurationS
			b.EnergyUsedKWh += t.EnergyUsedKWh
		}
	}
	for _, c := range charges {
		if c.EndedAt == nil {
			continue
		}
		if b := find(c.StartedAt); b != nil {
			b.Charges++
			b.EnergyAddedKWh += c.EnergyAddedKWh
			if c.Cost != nil {
				b.ChargingCost += *c.Cost
			}
		}
	}
	// Drain segments can straddle bucket boundaries: split by time overlap.
	for _, d := range drain {
		total := d.EndedAt.Sub(d.StartedAt)
		if total <= 0 {
			continue
		}
		for i := range buckets {
			b := &buckets[i]
			lo, hi := maxTime(d.StartedAt, b.Start, from), minTime(d.EndedAt, b.End, to)
			if !hi.After(lo) {
				continue
			}
			f := float64(hi.Sub(lo)) / float64(total)
			b.DrainKm += d.RangeLostKm * f
			b.DrainKWh += d.EnergyKWh * f
			b.IdleHours += hi.Sub(lo).Hours()
		}
	}
	var tot models.StatsBucket
	if len(buckets) > 0 {
		tot.Start, tot.End = buckets[0].Start, buckets[len(buckets)-1].End
	}
	for i := range buckets {
		b := &buckets[i]
		finish(b)
		tot.Trips += b.Trips
		tot.DistanceKm += b.DistanceKm
		tot.DriveTimeS += b.DriveTimeS
		tot.EnergyUsedKWh += b.EnergyUsedKWh
		tot.Charges += b.Charges
		tot.EnergyAddedKWh += b.EnergyAddedKWh
		tot.ChargingCost += b.ChargingCost
		tot.DrainKm += b.DrainKm
		tot.DrainKWh += b.DrainKWh
		tot.IdleHours += b.IdleHours
	}
	finish(&tot)
	return buckets, tot, nil
}

func finish(b *models.StatsBucket) {
	b.DistanceKm = round(b.DistanceKm, 2)
	b.EnergyUsedKWh = round(b.EnergyUsedKWh, 3)
	b.EnergyAddedKWh = round(b.EnergyAddedKWh, 3)
	b.ChargingCost = round(b.ChargingCost, 2)
	b.DrainKm = round(b.DrainKm, 2)
	b.DrainKWh = round(b.DrainKWh, 3)
	b.IdleHours = round(b.IdleHours, 2)
	b.EfficiencyWhKm = nil
	if b.DistanceKm >= 1 {
		e := round(b.EnergyUsedKWh*1000/b.DistanceKm, 1)
		b.EfficiencyWhKm = &e
	}
}

func maxTime(ts ...time.Time) time.Time {
	m := ts[0]
	for _, t := range ts[1:] {
		if t.After(m) {
			m = t
		}
	}
	return m
}

func minTime(ts ...time.Time) time.Time {
	m := ts[0]
	for _, t := range ts[1:] {
		if t.Before(m) {
			m = t
		}
	}
	return m
}
