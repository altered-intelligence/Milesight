package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/notify"
)

// ---------------------------------------------------------------- geofences

type geofenceInput struct {
	Name             *string  `json:"name"`
	Lat              *float64 `json:"lat"`
	Lon              *float64 `json:"lon"`
	RadiusM          *float64 `json:"radius_m"`
	EnergyRatePerKWh *float64 `json:"energy_rate_per_kwh"`
	ClearRate        bool     `json:"clear_energy_rate"`
}

func (in geofenceInput) apply(g *models.Geofence) string {
	if in.Name != nil {
		g.Name = strings.TrimSpace(*in.Name)
	}
	if in.Lat != nil {
		g.Lat = *in.Lat
	}
	if in.Lon != nil {
		g.Lon = *in.Lon
	}
	if in.RadiusM != nil {
		g.RadiusM = *in.RadiusM
	}
	if in.EnergyRatePerKWh != nil {
		v := *in.EnergyRatePerKWh
		g.EnergyRatePerKWh = &v
	}
	if in.ClearRate {
		g.EnergyRatePerKWh = nil
	}
	switch {
	case g.Name == "" || utf8.RuneCountInString(g.Name) > 60:
		return "name must be 1–60 characters"
	case g.Lat < -90 || g.Lat > 90 || math.IsNaN(g.Lat):
		return "lat must be between -90 and 90"
	case g.Lon < -180 || g.Lon > 180 || math.IsNaN(g.Lon):
		return "lon must be between -180 and 180"
	case g.RadiusM < 25 || g.RadiusM > 5000:
		return "radius_m must be between 25 and 5000"
	case g.EnergyRatePerKWh != nil && (*g.EnergyRatePerKWh < 0 || *g.EnergyRatePerKWh > 10):
		return "energy_rate_per_kwh must be between 0 and 10"
	}
	return ""
}

func (s *Server) listGeofences(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Store.ListGeofences(r.Context(), userID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if gs == nil {
		gs = []models.Geofence{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"geofences": gs})
}

func (s *Server) createGeofence(w http.ResponseWriter, r *http.Request) {
	var in geofenceInput
	if !decode(w, r, &in) {
		return
	}
	if in.Lat == nil || in.Lon == nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_geofence", "lat and lon are required")
		return
	}
	g := models.Geofence{RadiusM: 150}
	if msg := in.apply(&g); msg != "" {
		writeErr(w, r, http.StatusBadRequest, "invalid_geofence", msg)
		return
	}
	existing, _ := s.Store.ListGeofences(r.Context(), userID(r))
	if len(existing) >= 50 {
		writeErr(w, r, http.StatusBadRequest, "too_many_geofences", "at most 50 places per account")
		return
	}
	if err := s.Store.CreateGeofence(r.Context(), userID(r), &g); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) patchGeofence(w http.ResponseWriter, r *http.Request) {
	g, err := s.Store.GetGeofence(r.Context(), userID(r), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in geofenceInput
	if !decode(w, r, &in) {
		return
	}
	if msg := in.apply(g); msg != "" {
		writeErr(w, r, http.StatusBadRequest, "invalid_geofence", msg)
		return
	}
	if err := s.Store.UpdateGeofence(r.Context(), userID(r), g); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteGeofence(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteGeofence(r.Context(), userID(r), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- rules

func (s *Server) getRules(w http.ResponseWriter, r *http.Request) {
	stored, err := s.Store.GetRules(r.Context(), userID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": notify.Merge(stored)})
}

func (s *Server) putRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []models.Rule `json:"rules"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := notify.Validate(req.Rules); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_rules", err.Error())
		return
	}
	// Merge with what's stored so clients can send a subset.
	stored, err := s.Store.GetRules(r.Context(), userID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	merged := notify.Merge(append(stored, req.Rules...))
	if err := s.Store.SaveRules(r.Context(), userID(r), merged); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": merged})
}

// ---------------------------------------------------------------- push devices

type deviceInput struct {
	Token       string `json:"token"`
	Platform    string `json:"platform"`
	Environment string `json:"environment"`
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceInput
	if !decode(w, r, &in) {
		return
	}
	if in.Platform == "" {
		in.Platform = "ios"
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	switch {
	case in.Platform != "ios":
		writeErr(w, r, http.StatusBadRequest, "invalid_device", "platform must be ios")
		return
	case in.Environment != "sandbox" && in.Environment != "production":
		writeErr(w, r, http.StatusBadRequest, "invalid_device", "environment must be sandbox or production")
		return
	case len(in.Token) < 32 || len(in.Token) > 200 || !isHex(in.Token):
		writeErr(w, r, http.StatusBadRequest, "invalid_device", "token must be the hex APNs device token")
		return
	}
	d := &models.PushDevice{Token: strings.ToLower(in.Token), Platform: in.Platform, Environment: in.Environment}
	if err := s.Store.UpsertPushDevice(r.Context(), userID(r), d); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.Store.ListPushDevices(r.Context(), userID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ds == nil {
		ds = []models.PushDevice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": ds})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeletePushDevice(r.Context(), userID(r), strings.ToLower(r.PathValue("token"))); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- events & billing

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", 50, 1, 100)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var before int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		if before, err = strconv.ParseInt(c, 10, 64); err != nil || before <= 0 {
			writeErr(w, r, http.StatusBadRequest, "bad_request", "invalid cursor")
			return
		}
	}
	evs, err := s.Store.ListEvents(r.Context(), userID(r), r.URL.Query().Get("vehicle_id"), limit+1, before)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page := models.Page[models.Event]{Items: evs}
	if len(evs) > limit {
		page.Items = evs[:limit]
		page.NextCursor = strconv.FormatInt(evs[limit-1].ID, 10)
	}
	if page.Items == nil {
		page.Items = []models.Event{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) billingPlan(w http.ResponseWriter, r *http.Request) {
	u, ok := s.user(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": u.Plan, "status": "active", "store": "none"})
}
