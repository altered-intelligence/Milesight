package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/altered-intelligence/milesight/internal/analytics"
	"github.com/altered-intelligence/milesight/internal/geo"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

// Draft is a detected notification before it is stored and rendered.
type Draft struct {
	Type    string
	At      time.Time
	Payload map[string]any
}

// Input is everything Evaluate needs for one new snapshot.
type Input struct {
	Prev, Cur *models.LiveState // Prev may be nil
	Segments  []analytics.Event
	Rules     []models.Rule // owner's merged rules (thresholds)
}

// Evaluate detects notification-worthy transitions. It mutates st (persisted
// with the vehicle's processor state) so each condition fires once.
func Evaluate(st *analytics.NotifyState, in Input) []Draft {
	cur := in.Cur
	at := cur.UpdatedAt
	var out []Draft
	add := func(t string, p map[string]any) { out = append(out, Draft{Type: t, At: at, Payload: p}) }

	for _, seg := range in.Segments {
		switch seg.Kind {
		case analytics.ChargeStarted:
			c := seg.Charge
			add(models.EventChargingStarted, map[string]any{"battery_pct": c.StartBatteryPct, "charge_id": c.ID,
				"fast": c.FastCharger, "limit_pct": cur.Battery.ChargeLimitPct})
		case analytics.ChargeEnded:
			c := seg.Charge
			p := map[string]any{"battery_pct": c.EndBatteryPct, "energy_kwh": c.EnergyAddedKWh, "charge_id": c.ID,
				"duration_s": c.DurationS, "limit_pct": cur.Battery.ChargeLimitPct}
			if c.Cost != nil {
				p["cost"], p["currency"] = *c.Cost, c.Currency
			}
			switch c.EndReason {
			case "complete":
				add(models.EventChargeComplete, p)
			case "stopped":
				add(models.EventChargingStopped, p)
			}
		case analytics.TripEnded:
			t := seg.Trip
			if t.DistanceKm < 0.5 {
				continue
			}
			p := map[string]any{"distance_km": t.DistanceKm, "duration_s": t.DurationS, "energy_kwh": t.EnergyUsedKWh,
				"trip_id": t.ID, "end_place": t.EndPlace}
			if t.EfficiencyWhKm != nil {
				p["efficiency_wh_km"] = *t.EfficiencyWhKm
			}
			add(models.EventTripComplete, p)
		}
	}
	if cur.State != models.StateOnline {
		return out
	}

	// Low battery: fire once at/below the threshold; re-arm 5 points above it
	// (hysteresis stops alerts flapping around the threshold).
	threshold := Param(Find(in.Rules, models.EventLowBattery), "threshold_pct", 20)
	pct := float64(cur.Battery.Percent)
	switch {
	case cur.IsCharging() || pct >= threshold+5:
		st.LowFired = false
	case pct <= threshold && !st.LowFired && cur.Battery.Percent > 0:
		st.LowFired = true
		add(models.EventLowBattery, map[string]any{"battery_pct": cur.Battery.Percent, "range_km": cur.Battery.RangeKm,
			"threshold_pct": threshold})
	}

	parked := !cur.IsDriving()
	if c := cur.Closures; c != nil {
		minutes := func(t string) time.Duration {
			return time.Duration(Param(Find(in.Rules, t), "minutes", 10) * float64(time.Minute))
		}
		// Left unlocked while parked and nobody in the car.
		if parked && !c.Locked && !c.UserPresent {
			if st.UnlockedSince == nil {
				t := at
				st.UnlockedSince, st.UnlockedAlerted = &t, false
			} else if !st.UnlockedAlerted && at.Sub(*st.UnlockedSince) >= minutes(models.EventLeftUnlocked) {
				st.UnlockedAlerted = true
				add(models.EventLeftUnlocked, map[string]any{"since": st.UnlockedSince.UTC().Format(time.RFC3339),
					"minutes": int(at.Sub(*st.UnlockedSince).Minutes())})
			}
		} else {
			st.UnlockedSince, st.UnlockedAlerted = nil, false
		}
		// Doors, windows, frunk or trunk left open while parked.
		var open []string
		for name, v := range map[string]bool{"doors": c.DoorsOpen, "windows": c.WindowsOpen, "frunk": c.FrunkOpen, "trunk": c.TrunkOpen} {
			if v {
				open = append(open, name)
			}
		}
		if parked && len(open) > 0 && !c.UserPresent {
			if st.OpenSince == nil {
				t := at
				st.OpenSince, st.OpenAlerted = &t, false
			} else if !st.OpenAlerted && at.Sub(*st.OpenSince) >= minutes(models.EventLeftOpen) {
				st.OpenAlerted = true
				sortStrings(open)
				add(models.EventLeftOpen, map[string]any{"open": open, "minutes": int(at.Sub(*st.OpenSince).Minutes())})
			}
		} else {
			st.OpenSince, st.OpenAlerted = nil, false
		}
	}

	if sw := cur.Software; sw != nil && sw.UpdateAvailable && sw.UpdateVersion != "" && sw.UpdateVersion != st.UpdateNotified {
		st.UpdateNotified = sw.UpdateVersion
		add(models.EventSoftwareUpdate, map[string]any{"version": sw.UpdateVersion, "current": sw.Version})
	}

	if loc := cur.Location; loc != nil {
		if !st.PlaceKnown {
			st.Place, st.PlaceKnown = loc.Place, true
		} else if loc.Place != st.Place {
			if st.Place != "" {
				add(models.EventGeofenceDepart, map[string]any{"place": st.Place})
			}
			if loc.Place != "" {
				add(models.EventGeofenceArrive, map[string]any{"place": loc.Place})
			}
			st.Place = loc.Place
		}
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---------------------------------------------------------------- rendering

func distance(km float64, p models.Preferences) string {
	if p.DistanceUnit == "km" {
		return fmt.Sprintf("%.1f km", km)
	}
	return fmt.Sprintf("%.1f mi", geo.KmToMiles(km))
}

func efficiency(whKm float64, p models.Preferences) string {
	if p.DistanceUnit == "km" {
		return fmt.Sprintf("%.0f Wh/km", whKm)
	}
	return fmt.Sprintf("%.0f Wh/mi", whKm*1.609344)
}

func duration(s int) string {
	d := time.Duration(s) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d min", int(math.Max(1, math.Round(d.Minutes()))))
}

func num(p map[string]any, k string) float64 {
	switch v := p[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func str(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}

// Render produces the title and body for a notification in the reader's units.
func Render(eventType string, payload map[string]any, vehicleName string, p models.Preferences) (string, string) {
	pct := int(num(payload, "battery_pct"))
	switch eventType {
	case models.EventChargeComplete:
		body := fmt.Sprintf("Charged to %d%% · %.1f kWh added", pct, num(payload, "energy_kwh"))
		if c, ok := payload["cost"]; ok {
			body += fmt.Sprintf(" · %s %.2f", str(payload, "currency"), c)
		}
		return vehicleName + " finished charging", body
	case models.EventChargingStarted:
		return vehicleName + " started charging", fmt.Sprintf("At %d%%, charging to %d%%", pct, int(num(payload, "limit_pct")))
	case models.EventChargingStopped:
		return vehicleName + " stopped charging", fmt.Sprintf("Stopped at %d%% before reaching %d%%", pct, int(num(payload, "limit_pct")))
	case models.EventLowBattery:
		return vehicleName + " battery is low", fmt.Sprintf("%d%% · %s of range left", pct, distance(num(payload, "range_km"), p))
	case models.EventLeftUnlocked:
		return vehicleName + " is unlocked", fmt.Sprintf("Unlocked and parked for %d min", int(num(payload, "minutes")))
	case models.EventLeftOpen:
		var parts []string
		if list, ok := payload["open"].([]string); ok {
			parts = list
		} else if list, ok := payload["open"].([]any); ok {
			for _, v := range list {
				parts = append(parts, fmt.Sprint(v))
			}
		}
		return vehicleName + " is left open", fmt.Sprintf("Open: %s (%d min)", strings.Join(parts, ", "), int(num(payload, "minutes")))
	case models.EventSoftwareUpdate:
		return "Software update for " + vehicleName, fmt.Sprintf("Version %s is ready to install", str(payload, "version"))
	case models.EventGeofenceArrive:
		return vehicleName + " arrived", "Arrived at " + str(payload, "place")
	case models.EventGeofenceDepart:
		return vehicleName + " left", "Left " + str(payload, "place")
	case models.EventTripComplete:
		body := fmt.Sprintf("%s in %s", distance(num(payload, "distance_km"), p), duration(int(num(payload, "duration_s"))))
		if e := num(payload, "efficiency_wh_km"); e > 0 {
			body += " · " + efficiency(e, p)
		}
		if pl := str(payload, "end_place"); pl != "" {
			body += " · to " + pl
		}
		return vehicleName + " drive complete", body
	}
	return vehicleName, eventType
}

// DeepLink returns the in-app route for an event.
func DeepLink(vehicleID, eventType string, payload map[string]any) string {
	base := "milesight://vehicle/" + vehicleID
	switch eventType {
	case models.EventChargeComplete, models.EventChargingStarted, models.EventChargingStopped:
		if id := str(payload, "charge_id"); id != "" {
			return base + "/charges/" + id
		}
		return base + "/charging"
	case models.EventTripComplete:
		if id := str(payload, "trip_id"); id != "" {
			return base + "/trips/" + id
		}
	}
	return base
}

// ---------------------------------------------------------------- delivery

// Dispatcher stores drafts as events and pushes them to subscribed users.
type Dispatcher struct {
	Store  store.Store
	Pusher Pusher
	Log    *slog.Logger
	Now    func() time.Time
}

// Deliver records drafts for vehicle and pushes each to every member whose rule
// for that type is enabled. Push failures never fail ingestion.
func (d *Dispatcher) Deliver(ctx context.Context, v *store.VehicleRecord, owner models.Preferences, drafts []Draft) []models.Event {
	if len(drafts) == 0 {
		return nil
	}
	name := v.DisplayName
	if name == "" {
		name = "Your car"
	}
	members, err := d.Store.VehicleUsers(ctx, v.ID)
	if err != nil {
		d.Log.Warn("notify: list members", "vehicle", v.ID, "err", err)
	}
	type member struct {
		prefs   models.Preferences
		rules   []models.Rule
		devices []models.PushDevice
	}
	var ms []member
	for _, m := range members {
		u, err := d.Store.GetUser(ctx, m.UserID)
		if err != nil {
			continue
		}
		stored, _ := d.Store.GetRules(ctx, m.UserID)
		devs, _ := d.Store.ListPushDevices(ctx, m.UserID)
		ms = append(ms, member{prefs: u.Preferences, rules: Merge(stored), devices: devs})
	}
	var out []models.Event
	for _, dr := range drafts {
		title, body := Render(dr.Type, dr.Payload, name, owner)
		payload := map[string]any{}
		for k, v := range dr.Payload {
			payload[k] = v
		}
		payload["deep_link"] = DeepLink(v.ID, dr.Type, dr.Payload)
		ev := &models.Event{VehicleID: v.ID, Type: dr.Type, Title: title, Body: body, OccurredAt: dr.At.UTC(), Payload: payload}
		if err := d.Store.InsertEvent(ctx, ev); err != nil {
			d.Log.Error("notify: store event", "vehicle", v.ID, "type", dr.Type, "err", err)
			continue
		}
		delivered := false
		for _, m := range ms {
			if !Find(m.rules, dr.Type).Enabled {
				continue
			}
			t, b := Render(dr.Type, dr.Payload, name, m.prefs)
			for _, dev := range m.devices {
				err := d.Pusher.Push(ctx, dev, Notification{Title: t, Body: b, Type: dr.Type, VehicleID: v.ID,
					EventID: ev.ID, DeepLink: payload["deep_link"].(string)})
				switch {
				case err == nil:
					delivered = true
				case errors.Is(err, ErrDeviceGone):
					_ = d.Store.DeletePushToken(ctx, dev.Token)
				default:
					d.Log.Warn("notify: push failed", "type", dr.Type, "err", err)
				}
			}
		}
		if delivered {
			now := time.Now().UTC()
			if d.Now != nil {
				now = d.Now().UTC()
			}
			_ = d.Store.MarkEventDelivered(ctx, ev.ID, now)
			ev.DeliveredAt = &now
		}
		out = append(out, *ev)
	}
	return out
}
