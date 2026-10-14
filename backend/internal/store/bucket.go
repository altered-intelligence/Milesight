package store

import (
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

// BucketOrigin aligns time buckets; matches date_bin's origin in the Postgres store.
var BucketOrigin = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// BucketStart returns the start of the bucket containing t.
func BucketStart(t time.Time, bucket time.Duration) time.Time {
	d := t.Sub(BucketOrigin)
	n := d / bucket
	if d < 0 && d%bucket != 0 {
		n--
	}
	return BucketOrigin.Add(n * bucket)
}

// numericFields enumerates the averaged telemetry columns.
var numericFields = []func(*models.TelemetryPoint) **float64{
	func(p *models.TelemetryPoint) **float64 { return &p.BatteryPct },
	func(p *models.TelemetryPoint) **float64 { return &p.UsableBatteryPct },
	func(p *models.TelemetryPoint) **float64 { return &p.RatedRangeKm },
	func(p *models.TelemetryPoint) **float64 { return &p.IdealRangeKm },
	func(p *models.TelemetryPoint) **float64 { return &p.EstRangeKm },
	func(p *models.TelemetryPoint) **float64 { return &p.Lat },
	func(p *models.TelemetryPoint) **float64 { return &p.Lon },
	func(p *models.TelemetryPoint) **float64 { return &p.HeadingDeg },
	func(p *models.TelemetryPoint) **float64 { return &p.SpeedKph },
	func(p *models.TelemetryPoint) **float64 { return &p.PowerKW },
	func(p *models.TelemetryPoint) **float64 { return &p.OdometerKm },
	func(p *models.TelemetryPoint) **float64 { return &p.InsideTempC },
	func(p *models.TelemetryPoint) **float64 { return &p.OutsideTempC },
	func(p *models.TelemetryPoint) **float64 { return &p.ChargerPowerKW },
	func(p *models.TelemetryPoint) **float64 { return &p.ChargerVoltage },
	func(p *models.TelemetryPoint) **float64 { return &p.ChargerAmps },
	func(p *models.TelemetryPoint) **float64 { return &p.ChargeEnergyAddedKWh },
	func(p *models.TelemetryPoint) **float64 { return &p.ChargeLimitPct },
}

// BucketPoints averages ascending points into fixed buckets. Numeric fields are
// averaged over non-null samples; strings/bools take the latest value.
func BucketPoints(pts []models.TelemetryPoint, bucket time.Duration) []models.TelemetryPoint {
	if bucket <= 0 || len(pts) == 0 {
		return pts
	}
	var (
		out    []models.TelemetryPoint
		cur    models.TelemetryPoint
		sums   = make([]float64, len(numericFields))
		counts = make([]int, len(numericFields))
		open   bool
	)
	flush := func() {
		for i, f := range numericFields {
			if counts[i] > 0 {
				v := sums[i] / float64(counts[i])
				*f(&cur) = &v
			} else {
				*f(&cur) = nil
			}
			sums[i], counts[i] = 0, 0
		}
		out = append(out, cur)
	}
	for _, p := range pts {
		start := BucketStart(p.Time, bucket)
		if !open || !start.Equal(cur.Time) {
			if open {
				flush()
			}
			cur = models.TelemetryPoint{Time: start}
			open = true
		}
		for i, f := range numericFields {
			if v := *f(&p); v != nil {
				sums[i] += *v
				counts[i]++
			}
		}
		if p.State != "" {
			cur.State = p.State
		}
		if p.ShiftState != "" {
			cur.ShiftState = p.ShiftState
		}
		if p.ChargeState != "" {
			cur.ChargeState = p.ChargeState
		}
		if p.ClimateOn != nil {
			cur.ClimateOn = p.ClimateOn
		}
		if p.Locked != nil {
			cur.Locked = p.Locked
		}
		if p.SentryMode != nil {
			cur.SentryMode = p.SentryMode
		}
	}
	if open {
		flush()
	}
	return out
}
