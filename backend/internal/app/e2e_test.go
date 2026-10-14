package app_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/altered-intelligence/milesight/internal/app"
	"github.com/altered-intelligence/milesight/internal/config"
	"github.com/altered-intelligence/milesight/internal/models"
)

func testConfig() config.Config {
	return config.Config{
		Addr: ":0", PublicURL: "http://localhost", Transport: "sim", Store: "memory",
		JWTSecret: []byte("e2e-secret-0123456789"), EncryptionKey: bytes.Repeat([]byte{9}, 32),
		AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, DevLogin: true,
		AppRedirect: "milesight://auth/callback",
		Tesla:       config.TeslaConfig{DefaultRegion: "na", RedirectURI: "http://localhost/v1/auth/tesla/redirect"},
		Sim:         config.SimConfig{HomeLat: 37.3947, HomeLon: -122.1503, Vehicles: 2, HistoryDays: 7},
	}
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c *client) do(method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// json performs a request, asserts the status and decodes the body.
func (c *client) json(method, path string, body any, want int, out any) []byte {
	c.t.Helper()
	resp, raw := c.do(method, path, body, nil)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	if resp.Header.Get("X-Request-ID") == "" {
		c.t.Fatalf("%s %s: missing X-Request-ID", method, path)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode: %v: %s", method, path, err, raw)
		}
	}
	return raw
}

func eventually(t *testing.T, timeout time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(ctx, testConfig(), app.Options{Version: "test", Log: log})
	if err != nil {
		t.Fatal(err)
	}
	go a.Ingest.Run(ctx)
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	c := &client{t: t, base: srv.URL}

	// ---- public config & auth
	var cfg models.ServerConfig
	c.json("GET", "/v1/config", nil, 200, &cfg)
	if !cfg.DevLogin || cfg.TeslaOAuth || cfg.Transport != "sim" {
		t.Fatalf("config: %+v", cfg)
	}
	c.json("GET", "/v1/vehicles", nil, 401, nil)
	c.json("POST", "/v1/auth/tesla/authorize", map[string]string{}, 501, nil)
	c.json("POST", "/v1/auth/dev/login", map[string]string{"email": "not-an-email"}, 400, nil)
	var sess models.Session
	c.json("POST", "/v1/auth/dev/login", map[string]string{"email": "Driver@Example.com"}, 200, &sess)
	if sess.AccessToken == "" || sess.RefreshToken == "" || sess.User.Email != "driver@example.com" || sess.TokenType != "Bearer" {
		t.Fatalf("session: %+v", sess)
	}
	c.token = sess.AccessToken

	// ---- vehicles
	var vl struct{ Vehicles []models.Vehicle }
	c.json("GET", "/v1/vehicles", nil, 200, &vl)
	if len(vl.Vehicles) != 2 || vl.Vehicles[0].Role != "owner" {
		t.Fatalf("vehicles: %+v", vl.Vehicles)
	}
	v := vl.Vehicles[0]
	vp := "/v1/vehicles/" + v.ID

	// History is seeded by the ingestion worker shortly after sign-in.
	var trips models.Page[models.Trip]
	eventually(t, 15*time.Second, "seeded trips", func() bool {
		c.json("GET", vp+"/trips?limit=5", nil, 200, &trips)
		return len(trips.Items) == 5 && trips.NextCursor != ""
	})
	var page2 models.Page[models.Trip]
	c.json("GET", vp+"/trips?limit=5&cursor="+trips.NextCursor, nil, 200, &page2)
	if len(page2.Items) == 0 || !page2.Items[0].StartedAt.Before(trips.Items[4].StartedAt) {
		t.Fatal("cursor pagination must continue strictly older")
	}
	var closed models.Trip
	for _, tr := range append(trips.Items, page2.Items...) {
		if tr.EndedAt != nil && tr.DistanceKm > 1 {
			closed = tr
			break
		}
	}
	var td models.TripDetail
	c.json("GET", vp+"/trips/"+closed.ID, nil, 200, &td)
	if len(td.Route) < 3 || td.DistanceKm != closed.DistanceKm {
		t.Fatalf("trip detail route: %d points", len(td.Route))
	}
	c.json("GET", vp+"/trips/00000000-0000-0000-0000-000000000000", nil, 404, nil)

	var charges models.Page[models.ChargeSession]
	c.json("GET", vp+"/charges?limit=50", nil, 200, &charges)
	if len(charges.Items) == 0 {
		t.Fatal("no seeded charging sessions")
	}
	var cd models.ChargeDetail
	c.json("GET", vp+"/charges/"+charges.Items[len(charges.Items)-1].ID, nil, 200, &cd)
	if len(cd.Curve) == 0 {
		t.Fatal("charge curve empty")
	}

	for _, period := range []string{"daily", "weekly", "monthly"} {
		var sr models.StatsResponse
		c.json("GET", vp+"/stats/"+period+"?tz=America/Chicago", nil, 200, &sr)
		if len(sr.Buckets) == 0 || sr.Totals.Trips == 0 || sr.Totals.DistanceKm <= 0 || sr.Currency != "USD" {
			t.Fatalf("stats %s: %+v", period, sr.Totals)
		}
	}
	c.json("GET", vp+"/stats/hourly", nil, 400, nil)
	var health models.BatteryHealth
	c.json("GET", vp+"/battery/health", nil, 200, &health)
	if health.NominalCapacityKWh == 0 || health.History == nil {
		t.Fatalf("health: %+v", health)
	}
	var tele struct {
		Resolution string                  `json:"resolution"`
		Points     []models.TelemetryPoint `json:"points"`
	}
	c.json("GET", vp+"/telemetry?from="+time.Now().Add(-7*24*time.Hour).UTC().Format(time.RFC3339), nil, 200, &tele)
	if tele.Resolution != "15m" || len(tele.Points) == 0 {
		t.Fatalf("telemetry auto resolution %q with %d points", tele.Resolution, len(tele.Points))
	}
	c.json("GET", vp+"/telemetry?resolution=raw&from="+time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339), nil, 400, nil)
	resp, raw := c.do("GET", vp+"/export?kind=trips", nil, nil)
	rows, _ := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || len(rows) < 2 || rows[0][0] != "id" {
		t.Fatalf("export: %d %q rows=%d", resp.StatusCode, resp.Header.Get("Content-Type"), len(rows))
	}

	// ---- live state over WebSocket
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + vp + "/ws"
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil || resp.StatusCode != 401 {
		t.Fatal("websocket must require auth")
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": {"Bearer " + c.token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var first struct {
		Type string           `json:"type"`
		Data models.LiveState `json:"data"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&first); err != nil || first.Type != "state" || first.Data.VehicleID != v.ID {
		t.Fatalf("first ws message: %v %+v", err, first)
	}

	// ---- commands (the simulated car may be asleep: wake-then-send)
	var cr models.CommandResult
	idem := map[string]string{"Idempotency-Key": "lock-1"}
	resp, raw = c.do("POST", vp+"/commands/unlock", map[string]any{}, idem)
	if resp.StatusCode != 200 || json.Unmarshal(raw, &cr) != nil || cr.Status != "ok" {
		t.Fatalf("unlock: %d %s", resp.StatusCode, raw)
	}
	resp, raw2 := c.do("POST", vp+"/commands/unlock", map[string]any{}, idem)
	if resp.StatusCode != 200 || resp.Header.Get("Idempotent-Replayed") != "true" || string(raw2) != string(raw) {
		t.Fatalf("idempotent replay: %d %s", resp.StatusCode, resp.Header)
	}
	resp, _ = c.do("POST", vp+"/commands/unlock", map[string]any{"on": true}, idem)
	if resp.StatusCode != 422 {
		t.Fatalf("reused key with different body: %d", resp.StatusCode)
	}
	c.json("POST", vp+"/commands/climate", map[string]any{"on": true, "temp_c": 22}, 200, &cr)
	c.json("POST", vp+"/commands/climate", map[string]any{"on": true, "temp_c": 99}, 400, nil)
	c.json("POST", vp+"/commands/warp_drive", map[string]any{}, 400, nil)
	c.json("POST", vp+"/commands/charging", map[string]any{"action": "set_amps", "amps": 16}, 200, nil)

	// The live snapshot reflects the unlock (ingestion is poked after commands).
	eventually(t, 10*time.Second, "unlocked state", func() bool {
		var ls models.LiveState
		c.json("GET", vp+"/live", nil, 200, &ls)
		return ls.State == "online" && ls.Closures != nil && !ls.Closures.Locked && ls.Climate != nil && ls.Climate.On
	})
	// And the WebSocket delivered state + command messages.
	types := map[string]bool{}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !(types["state"] && types["command"]) {
		var m struct{ Type string }
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("ws stream: %v (got %v)", err, types)
		}
		types[m.Type] = true
	}

	// ---- rename, places, rules, devices, events
	var renamed models.Vehicle
	c.json("PATCH", vp, map[string]string{"display_name": "Daily Driver"}, 200, &renamed)
	if renamed.DisplayName != "Daily Driver" {
		t.Fatal("rename")
	}
	var g models.Geofence
	c.json("POST", "/v1/geofences", map[string]any{"name": "Home", "lat": 37.3947, "lon": -122.1503, "radius_m": 200, "energy_rate_per_kwh": 0.11}, 201, &g)
	c.json("POST", "/v1/geofences", map[string]any{"name": "Nowhere", "lat": 95, "lon": 0}, 400, nil)
	var patched models.Geofence
	c.json("PATCH", "/v1/geofences/"+g.ID, map[string]any{"name": "Casa", "clear_energy_rate": true}, 200, &patched)
	if patched.Name != "Casa" || patched.EnergyRatePerKWh != nil || patched.RadiusM != 200 {
		t.Fatalf("geofence patch: %+v", patched)
	}
	var gl struct{ Geofences []models.Geofence }
	c.json("GET", "/v1/geofences", nil, 200, &gl)
	if len(gl.Geofences) != 1 {
		t.Fatal("geofence list")
	}
	c.json("DELETE", "/v1/geofences/"+g.ID, nil, 204, nil)
	c.json("DELETE", "/v1/geofences/"+g.ID, nil, 404, nil)

	var rules struct{ Rules []models.Rule }
	c.json("GET", "/v1/rules", nil, 200, &rules)
	if len(rules.Rules) != 10 {
		t.Fatalf("rules: %d", len(rules.Rules))
	}
	c.json("PUT", "/v1/rules", map[string]any{"rules": []models.Rule{{Type: "trip_complete", Enabled: true}, {Type: "low_battery", Enabled: true, Params: map[string]float64{"threshold_pct": 15}}}}, 200, &rules)
	for _, r := range rules.Rules {
		if r.Type == "low_battery" && r.Params["threshold_pct"] != 15 || r.Type == "charge_complete" && !r.Enabled {
			t.Fatalf("rules after put: %+v", r)
		}
	}
	c.json("PUT", "/v1/rules", map[string]any{"rules": []models.Rule{{Type: "nope"}}}, 400, nil)

	token := strings.Repeat("ab", 32)
	c.json("POST", "/v1/push/devices", map[string]string{"token": token, "environment": "sandbox"}, 200, nil)
	c.json("POST", "/v1/push/devices", map[string]string{"token": "xyz"}, 400, nil)
	var devs struct{ Devices []models.PushDevice }
	c.json("GET", "/v1/push/devices", nil, 200, &devs)
	if len(devs.Devices) != 1 {
		t.Fatal("devices")
	}
	c.json("DELETE", "/v1/push/devices/"+token, nil, 204, nil)
	var evs models.Page[models.Event]
	c.json("GET", "/v1/events?limit=10", nil, 200, &evs)

	// ---- preferences
	var me models.User
	c.json("PATCH", "/v1/me", map[string]any{"preferences": map[string]any{"distance_unit": "km", "currency": "eur", "time_zone": "Europe/Berlin"}}, 200, &me)
	if me.Preferences.DistanceUnit != "km" || me.Preferences.Currency != "EUR" || me.Preferences.TimeZone != "Europe/Berlin" {
		t.Fatalf("prefs: %+v", me.Preferences)
	}
	c.json("PATCH", "/v1/me", map[string]any{"preferences": map[string]any{"time_zone": "Mars/Olympus"}}, 400, nil)

	// ---- isolation between accounts
	other := &client{t: t, base: srv.URL}
	var s2 models.Session
	other.json("POST", "/v1/auth/dev/login", map[string]string{"email": "someone@else.com"}, 200, &s2)
	other.token = s2.AccessToken
	other.json("GET", vp, nil, 404, nil)
	other.json("POST", vp+"/commands/unlock", map[string]any{}, 404, nil)
	other.json("GET", vp+"/trips", nil, 404, nil)

	// ---- session lifecycle
	var rotated models.Session
	c.json("POST", "/v1/auth/refresh", map[string]string{"refresh_token": sess.RefreshToken}, 200, &rotated)
	c.json("POST", "/v1/auth/refresh", map[string]string{"refresh_token": sess.RefreshToken}, 401, nil) // reuse
	c.json("POST", "/v1/auth/refresh", map[string]string{"refresh_token": rotated.RefreshToken}, 401, nil)
	c.json("DELETE", "/v1/account", nil, 200, nil)
	c.json("GET", "/v1/me", nil, 401, nil)
	other.json("GET", "/v1/vehicles", nil, 200, nil)
}
