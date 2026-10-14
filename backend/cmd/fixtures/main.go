// Command fixtures captures real API responses from an in-process server
// (simulator + memory store) and writes them as JSON fixtures for client
// contract tests (ios/Packages/CoreKit/Tests/CoreKitTests/Fixtures).
//
//	go run ./cmd/fixtures -out ../ios/Packages/CoreKit/Tests/CoreKitTests/Fixtures
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/altered-intelligence/milesight/internal/app"
	"github.com/altered-intelligence/milesight/internal/config"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/notify"
)

func main() {
	out := flag.String("out", "fixtures", "output directory")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{
		Transport: "sim", Store: "memory", DevLogin: true, PublicURL: "http://localhost:8080",
		JWTSecret: []byte("fixtures-secret-0123456789"), EncryptionKey: bytes.Repeat([]byte{1}, 32),
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, AppRedirect: "milesight://auth/callback",
		Tesla: config.TeslaConfig{DefaultRegion: "na"},
		Sim:   config.SimConfig{HomeLat: 37.3947, HomeLon: -122.1503, Vehicles: 2, HistoryDays: 10},
	}
	a, err := app.New(ctx, cfg, app.Options{Version: "fixtures", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		log.Fatal(err)
	}
	go a.Ingest.Run(ctx)
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()

	token := ""
	call := func(method, path string, body any) []byte {
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rdr)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return raw
	}
	save := func(name string, raw []byte) {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			log.Fatalf("%s: %v: %s", name, err, raw)
		}
		pretty, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(filepath.Join(*out, name+".json"), append(pretty, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote", name)
	}
	// trim keeps the first n elements of an array field to keep fixtures small.
	trim := func(raw []byte, field string, n int) []byte {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if arr, ok := m[field].([]any); ok && len(arr) > n {
			m[field] = arr[:n]
		}
		b, _ := json.Marshal(m)
		return b
	}

	save("config", call("GET", "/v1/config", nil))
	sessRaw := call("POST", "/v1/auth/dev/login", map[string]string{"email": "fixtures@example.com"})
	var sess map[string]any
	_ = json.Unmarshal(sessRaw, &sess)
	token, _ = sess["access_token"].(string)
	// Real shape, placeholder secrets (keeps secret scanners quiet).
	sess["access_token"], sess["refresh_token"] = "eyJhbGciOiJIUzI1NiJ9.fixture.signature", "fixture-refresh-token"
	redacted, _ := json.Marshal(sess)
	save("session", redacted)

	// Wait for seeded history.
	var vehicleID string
	for i := 0; i < 200; i++ {
		var vl struct {
			Vehicles []struct {
				ID     string          `json:"id"`
				Latest json.RawMessage `json:"latest"`
			} `json:"vehicles"`
		}
		_ = json.Unmarshal(call("GET", "/v1/vehicles", nil), &vl)
		if len(vl.Vehicles) > 0 && len(vl.Vehicles[0].Latest) > 0 {
			vehicleID = vl.Vehicles[0].ID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if vehicleID == "" {
		log.Fatal("no vehicle with data")
	}
	vp := "/v1/vehicles/" + vehicleID
	// Wake the car and make it interesting: climate on, frunk open.
	cmd := call("POST", vp+"/commands/climate", map[string]any{"on": true, "temp_c": 21.5})
	save("command_result", cmd)
	save("command_refused", call("POST", vp+"/commands/charging", map[string]any{"action": "start"}))
	time.Sleep(1500 * time.Millisecond)
	save("vehicles", call("GET", "/v1/vehicles", nil))
	save("live", call("GET", vp+"/live", nil))

	tripsRaw := call("GET", vp+"/trips?limit=3", nil)
	save("trips_page", tripsRaw)
	var tp struct {
		Items []struct {
			ID      string  `json:"id"`
			EndedAt *string `json:"ended_at"`
		} `json:"items"`
	}
	_ = json.Unmarshal(tripsRaw, &tp)
	for _, t := range tp.Items {
		if t.EndedAt != nil {
			save("trip_detail", trim(call("GET", vp+"/trips/"+t.ID, nil), "route", 5))
			break
		}
	}
	chargesRaw := call("GET", vp+"/charges?limit=3", nil)
	save("charges_page", chargesRaw)
	var cp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(chargesRaw, &cp)
	if len(cp.Items) > 0 {
		save("charge_detail", trim(call("GET", vp+"/charges/"+cp.Items[0].ID, nil), "curve", 5))
	}
	save("stats_daily", trim(call("GET", vp+"/stats/daily?tz=America/Chicago", nil), "buckets", 3))
	save("battery_health", call("GET", vp+"/battery/health", nil))
	save("telemetry", trim(call("GET", vp+"/telemetry", nil), "points", 4))
	save("rules", call("GET", "/v1/rules", nil))
	save("geofence", call("POST", "/v1/geofences", map[string]any{"name": "Home", "lat": 37.3947, "lon": -122.1503, "radius_m": 200, "energy_rate_per_kwh": 0.12}))
	save("me", call("GET", "/v1/me", nil))
	save("error_not_found", call("GET", vp+"/trips/00000000-0000-0000-0000-000000000000", nil))

	// Generate an event through the real dispatcher (rendering, deep link, storage).
	v, err := a.Store.GetVehicle(ctx, vehicleID)
	if err != nil {
		log.Fatal(err)
	}
	d := &notify.Dispatcher{Store: a.Store, Pusher: notify.LogPusher{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.Deliver(ctx, v, models.DefaultPreferences(), []notify.Draft{{Type: models.EventChargeComplete, At: time.Now(),
		Payload: map[string]any{"battery_pct": 80, "energy_kwh": 21.4, "charge_id": cp.Items[0].ID, "cost": 3.21,
			"currency": "USD", "duration_s": 5400, "limit_pct": 80}}})
	save("events_page", call("GET", "/v1/events?limit=3", nil))

	// WebSocket: first state message.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + vp + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		log.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Fatal(err)
	}
	save("ws_state", msg)
	conn.Close()
}
