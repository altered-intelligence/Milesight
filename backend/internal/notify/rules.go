// Package notify turns state changes into notification events, stores them in
// the event log and pushes them to users' devices according to their rules.
package notify

import (
	"fmt"

	"github.com/altered-intelligence/milesight/internal/models"
)

// Defaults lists every rule type with its default setting.
func Defaults() []models.Rule {
	return []models.Rule{
		{Type: models.EventChargeComplete, Enabled: true},
		{Type: models.EventChargingStarted, Enabled: false},
		{Type: models.EventChargingStopped, Enabled: true},
		{Type: models.EventLowBattery, Enabled: true, Params: map[string]float64{"threshold_pct": 20}},
		{Type: models.EventLeftUnlocked, Enabled: true, Params: map[string]float64{"minutes": 10}},
		{Type: models.EventLeftOpen, Enabled: true, Params: map[string]float64{"minutes": 10}},
		{Type: models.EventSoftwareUpdate, Enabled: true},
		{Type: models.EventGeofenceArrive, Enabled: false},
		{Type: models.EventGeofenceDepart, Enabled: false},
		{Type: models.EventTripComplete, Enabled: false},
	}
}

// paramBounds limits user-supplied rule parameters.
var paramBounds = map[string]map[string][2]float64{
	models.EventLowBattery:   {"threshold_pct": {5, 50}},
	models.EventLeftUnlocked: {"minutes": {1, 120}},
	models.EventLeftOpen:     {"minutes": {1, 120}},
}

// Merge overlays stored settings on the defaults (unknown stored types are dropped).
func Merge(stored []models.Rule) []models.Rule {
	byType := map[string]models.Rule{}
	for _, r := range stored {
		byType[r.Type] = r
	}
	out := Defaults()
	for i, d := range out {
		s, ok := byType[d.Type]
		if !ok {
			continue
		}
		out[i].Enabled = s.Enabled
		for k := range d.Params {
			if v, ok := s.Params[k]; ok {
				out[i].Params[k] = v
			}
		}
	}
	return out
}

// Validate checks a user-supplied rule list.
func Validate(rules []models.Rule) error {
	known := map[string]bool{}
	for _, d := range Defaults() {
		known[d.Type] = true
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if !known[r.Type] {
			return fmt.Errorf("unknown rule type %q", r.Type)
		}
		if seen[r.Type] {
			return fmt.Errorf("duplicate rule type %q", r.Type)
		}
		seen[r.Type] = true
		for k, v := range r.Params {
			b, ok := paramBounds[r.Type][k]
			if !ok {
				return fmt.Errorf("rule %s has no parameter %q", r.Type, k)
			}
			if v < b[0] || v > b[1] {
				return fmt.Errorf("rule %s parameter %s must be between %g and %g", r.Type, k, b[0], b[1])
			}
		}
	}
	return nil
}

// Find returns the rule of type t from a merged list.
func Find(rules []models.Rule, t string) models.Rule {
	for _, r := range rules {
		if r.Type == t {
			return r
		}
	}
	return models.Rule{Type: t}
}

// Param returns a rule parameter or fallback.
func Param(r models.Rule, key string, fallback float64) float64 {
	if v, ok := r.Params[key]; ok {
		return v
	}
	return fallback
}
