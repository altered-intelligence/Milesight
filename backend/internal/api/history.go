package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/altered-intelligence/milesight/internal/analytics"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

var resolutions = map[string]time.Duration{
	"raw": 0, "1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"1h": time.Hour, "6h": 6 * time.Hour, "1d": 24 * time.Hour,
}

// autoResolution picks a bucket that keeps a range under ~1500 points.
func autoResolution(span time.Duration) time.Duration {
	for _, d := range []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour} {
		if d == 0 && span <= 6*time.Hour {
			return 0
		}
		if d > 0 && span/d <= 1500 {
			return d
		}
	}
	return 24 * time.Hour
}

func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	now := s.now()
	to, err := timeParam(r, "to", now)
	if err == nil {
		var from time.Time
		from, err = timeParam(r, "from", to.Add(-24*time.Hour))
		if err == nil {
			s.serveTelemetry(w, r, v.ID, from, to)
			return
		}
	}
	writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
}

func (s *Server) serveTelemetry(w http.ResponseWriter, r *http.Request, vehicleID string, from, to time.Time) {
	span := to.Sub(from)
	if span <= 0 || span > 400*24*time.Hour {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "range must be positive and at most 400 days")
		return
	}
	res := autoResolution(span)
	if q := r.URL.Query().Get("resolution"); q != "" {
		d, ok := resolutions[q]
		if !ok {
			writeErr(w, r, http.StatusBadRequest, "bad_request", "resolution must be raw, 1m, 5m, 15m, 1h, 6h or 1d")
			return
		}
		if d == 0 && span > 7*24*time.Hour {
			writeErr(w, r, http.StatusBadRequest, "bad_request", "raw resolution is limited to 7 days")
			return
		}
		res = d
	}
	limit, err := intParam(r, "limit", 5000, 1, 20000)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	pts, err := s.Store.QueryTelemetry(r.Context(), vehicleID, from, to, res, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if pts == nil {
		pts = []models.TelemetryPoint{}
	}
	label := "raw"
	for k, d := range resolutions {
		if d == res && k != "raw" {
			label = k
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "resolution": label, "points": pts})
}

// listOptions parses from/to/limit/cursor.
func listOptions(r *http.Request) (store.ListOptions, int, error) {
	var o store.ListOptions
	var err error
	if o.From, err = timeParam(r, "from", time.Time{}); err != nil {
		return o, 0, err
	}
	if o.To, err = timeParam(r, "to", time.Time{}); err != nil {
		return o, 0, err
	}
	limit, err := intParam(r, "limit", 50, 1, 200)
	if err != nil {
		return o, 0, err
	}
	if o.Before, err = decodeCursor(r.URL.Query().Get("cursor")); err != nil {
		return o, 0, err
	}
	o.Limit = limit + 1 // one extra to know whether another page exists
	return o, limit, nil
}

func (s *Server) listTrips(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	o, limit, err := listOptions(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	trips, err := s.Store.ListTrips(r.Context(), v.ID, o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page := models.Page[models.Trip]{Items: trips}
	if len(trips) > limit {
		page.Items = trips[:limit]
		page.NextCursor = encodeCursor(trips[limit-1].StartedAt)
	}
	if page.Items == nil {
		page.Items = []models.Trip{}
	}
	writeJSON(w, http.StatusOK, page)
}

// downsample keeps at most n evenly spaced items (always keeping the last).
func downsample[T any](in []T, n int) []T {
	if len(in) <= n || n < 2 {
		return in
	}
	out := make([]T, 0, n)
	step := float64(len(in)-1) / float64(n-1)
	for i := 0; i < n; i++ {
		out = append(out, in[int(float64(i)*step+0.5)])
	}
	return out
}

func (s *Server) getTrip(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	t, err := s.Store.GetTrip(r.Context(), v.ID, r.PathValue("trip_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	end := s.now()
	if t.EndedAt != nil {
		end = t.EndedAt.Add(time.Second)
	}
	pts, err := s.Store.QueryTelemetry(r.Context(), v.ID, t.StartedAt.Add(-time.Minute), end, 0, 20000)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	route := []models.RoutePoint{}
	for _, p := range pts {
		if p.Lat == nil || p.Lon == nil || p.State != models.StateOnline {
			continue
		}
		rp := models.RoutePoint{Time: p.Time, Lat: *p.Lat, Lon: *p.Lon}
		if p.SpeedKph != nil {
			rp.SpeedKph = *p.SpeedKph
		}
		if p.PowerKW != nil {
			rp.PowerKW = *p.PowerKW
		}
		if p.BatteryPct != nil {
			rp.BatteryPct = *p.BatteryPct
		}
		route = append(route, rp)
	}
	writeJSON(w, http.StatusOK, models.TripDetail{Trip: *t, Route: downsample(route, 1000)})
}

func (s *Server) listCharges(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	o, limit, err := listOptions(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cs, err := s.Store.ListChargeSessions(r.Context(), v.ID, o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page := models.Page[models.ChargeSession]{Items: cs}
	if len(cs) > limit {
		page.Items = cs[:limit]
		page.NextCursor = encodeCursor(cs[limit-1].StartedAt)
	}
	if page.Items == nil {
		page.Items = []models.ChargeSession{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getCharge(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	c, err := s.Store.GetChargeSession(r.Context(), v.ID, r.PathValue("session_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	end := s.now()
	if c.EndedAt != nil {
		end = c.EndedAt.Add(time.Second)
	}
	pts, err := s.Store.QueryTelemetry(r.Context(), v.ID, c.StartedAt, end, 0, 20000)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	curve := []models.ChargePoint{}
	for _, p := range pts {
		if p.ChargerPowerKW == nil || p.BatteryPct == nil {
			continue
		}
		cp := models.ChargePoint{Time: p.Time, BatteryPct: *p.BatteryPct, PowerKW: *p.ChargerPowerKW}
		if p.ChargerVoltage != nil {
			cp.Voltage = *p.ChargerVoltage
		}
		if p.ChargerAmps != nil {
			cp.Amps = *p.ChargerAmps
		}
		if p.ChargeEnergyAddedKWh != nil {
			cp.EnergyAddedKWh = *p.ChargeEnergyAddedKWh
		}
		curve = append(curve, cp)
	}
	writeJSON(w, http.StatusOK, models.ChargeDetail{ChargeSession: *c, Curve: downsample(curve, 1000)})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	u, ok := s.user(w, r)
	if !ok {
		return
	}
	period := r.PathValue("period")
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		tz = u.Preferences.TimeZone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "tz must be an IANA time zone")
		return
	}
	now := s.now()
	var span time.Duration
	switch period {
	case analytics.Daily:
		span = 30 * 24 * time.Hour
	case analytics.Weekly:
		span = 12 * 7 * 24 * time.Hour
	case analytics.Monthly:
		span = 365 * 24 * time.Hour
	default:
		writeErr(w, r, http.StatusBadRequest, "bad_request", "period must be daily, weekly or monthly")
		return
	}
	to, err := timeParam(r, "to", now)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	from, err := timeParam(r, "from", to.Add(-span))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !to.After(from) || to.Sub(from) > 3*366*24*time.Hour {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "range must be positive and at most 3 years")
		return
	}
	ctx := r.Context()
	o := store.ListOptions{From: from.Add(-32 * 24 * time.Hour), To: to}
	trips, err := s.Store.ListTrips(ctx, v.ID, o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	charges, err := s.Store.ListChargeSessions(ctx, v.ID, o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	drain, err := s.Store.ListDrain(ctx, v.ID, from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	buckets, totals, err := analytics.Rollup(period, loc, from, to, trips, charges, drain)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models.StatsResponse{Period: period, TimeZone: tz, Currency: u.Preferences.Currency,
		From: from, To: to, Buckets: buckets, Totals: totals})
}

func (s *Server) batteryHealth(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	charges, err := s.Store.ListChargeSessions(r.Context(), v.ID, store.ListOptions{Limit: 1000})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, analytics.BatteryHealth(charges, analytics.SpecFor(v.Model), v.Latest))
}

func fmtF(p *float64, prec int) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', prec, 64)
}

func fmtT(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// export streams trips or charging sessions as CSV (SI units, UTC).
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "trips" && kind != "charges" {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "kind must be trips or charges")
		return
	}
	from, err := timeParam(r, "from", time.Time{})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	to, err := timeParam(r, "to", time.Time{})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	o := store.ListOptions{From: from, To: to, Limit: 100000}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="milesight-%s-%s.csv"`, kind, s.now().Format("20060102")))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if kind == "trips" {
		trips, err := s.Store.ListTrips(r.Context(), v.ID, o)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		_ = cw.Write([]string{"id", "started_at", "ended_at", "distance_km", "duration_s", "avg_speed_kph", "max_speed_kph",
			"start_battery_pct", "end_battery_pct", "energy_used_kwh", "efficiency_wh_km", "start_place", "end_place", "outside_temp_c"})
		for _, t := range trips {
			start := t.StartedAt
			_ = cw.Write([]string{t.ID, fmtT(&start), fmtT(t.EndedAt), strconv.FormatFloat(t.DistanceKm, 'f', 2, 64),
				strconv.Itoa(t.DurationS), strconv.FormatFloat(t.AvgSpeedKph, 'f', 1, 64), strconv.FormatFloat(t.MaxSpeedKph, 'f', 1, 64),
				strconv.Itoa(t.StartBatteryPct), strconv.Itoa(t.EndBatteryPct), strconv.FormatFloat(t.EnergyUsedKWh, 'f', 3, 64),
				fmtF(t.EfficiencyWhKm, 1), t.StartPlace, t.EndPlace, fmtF(t.OutsideTempC, 1)})
		}
		return
	}
	cs, err := s.Store.ListChargeSessions(r.Context(), v.ID, o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_ = cw.Write([]string{"id", "started_at", "ended_at", "duration_s", "start_battery_pct", "end_battery_pct", "energy_added_kwh",
		"max_power_kw", "avg_power_kw", "charger_type", "place", "cost", "currency", "end_reason"})
	for _, c := range cs {
		start := c.StartedAt
		_ = cw.Write([]string{c.ID, fmtT(&start), fmtT(c.EndedAt), strconv.Itoa(c.DurationS), strconv.Itoa(c.StartBatteryPct),
			strconv.Itoa(c.EndBatteryPct), strconv.FormatFloat(c.EnergyAddedKWh, 'f', 3, 64), strconv.FormatFloat(c.MaxPowerKW, 'f', 1, 64),
			strconv.FormatFloat(c.AvgPowerKW, 'f', 1, 64), c.ChargerType, c.Place, fmtF(c.Cost, 2), c.Currency, c.EndReason})
	}
}
