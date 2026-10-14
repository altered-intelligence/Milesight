package tesla

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/auth"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store/memory"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// A trimmed but field-accurate vehicle_data response (Model 3, charging at home).
const vehicleDataFixture = `{"response":{
 "id":1492931337,"id_s":"1492931337","vehicle_id":1234,"vin":"5YJ3E1EA7KF000001","display_name":"Nikola","state":"online",
 "charge_state":{"battery_level":64,"usable_battery_level":63,"battery_range":201.43,"ideal_battery_range":201.43,
   "est_battery_range":180.2,"charge_limit_soc":80,"charging_state":"Charging","charge_port_door_open":true,
   "charge_port_latch":"Engaged","charger_power":11,"charger_voltage":240,"charger_actual_current":48,
   "charge_current_request":48,"charge_current_request_max":48,"charger_phases":1,"charge_energy_added":12.34,
   "charge_miles_added_rated":49.5,"charge_rate":34.2,"minutes_to_full_charge":95,"fast_charger_present":false,
   "fast_charger_type":"<invalid>","conn_charge_cable":"SAE","timestamp":1760000000000},
 "climate_state":{"inside_temp":21.5,"outside_temp":12,"driver_temp_setting":21,"passenger_temp_setting":21,
   "is_climate_on":false,"is_preconditioning":false,"is_front_defroster_on":false,"fan_status":0},
 "drive_state":{"shift_state":null,"speed":null,"power":0,"latitude":37.3947,"longitude":-122.1503,"heading":271},
 "vehicle_state":{"locked":true,"odometer":12345.6,"df":0,"dr":0,"pf":0,"pr":0,"fd_window":0,"fp_window":0,
   "rd_window":0,"rp_window":1,"ft":0,"rt":0,"sentry_mode":false,"is_user_present":false,
   "car_version":"2025.32.3 abcdef123","software_update":{"status":"available","version":"2025.38.1"}},
 "vehicle_config":{"car_type":"model3","trim_badging":"74d","exterior_color":"MidnightSilver"}}}`

func TestToSnapshotMapsUnitsAndStates(t *testing.T) {
	var env struct {
		Response VehicleData `json:"response"`
	}
	if err := json.Unmarshal([]byte(vehicleDataFixture), &env); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snap := ToSnapshot(&env.Response, now)
	s := snap.State
	if s.State != "online" || s.Battery.Percent != 64 || s.Battery.UsablePercent != 63 || s.Battery.ChargeLimitPct != 80 {
		t.Fatalf("battery: %+v", s.Battery)
	}
	if s.Battery.RangeKm < 324.1 || s.Battery.RangeKm > 324.2 { // 201.43 mi
		t.Fatalf("range km: %v", s.Battery.RangeKm)
	}
	if s.Battery.ChargeState != models.ChargeCharging || !s.Battery.PluggedIn || !s.Battery.ChargePortOpen {
		t.Fatalf("charge state: %+v", s.Battery)
	}
	if s.Charging == nil || s.Charging.PowerKW != 11 || s.Charging.Amps != 48 || s.Charging.FastCharger || s.Charging.FastChargerType != "" {
		t.Fatalf("charging: %+v", s.Charging)
	}
	if s.OdometerKm < 19868 || s.OdometerKm > 19869 {
		t.Fatalf("odometer: %v", s.OdometerKm)
	}
	if s.Location == nil || s.Location.HeadingDeg != 271 || s.Drive.ShiftState != "" || s.Drive.SpeedKph != 0 {
		t.Fatalf("drive/location: %+v %+v", s.Drive, s.Location)
	}
	if !s.Closures.Locked || !s.Closures.WindowsOpen || s.Closures.DoorsOpen || s.Closures.TrunkOpen {
		t.Fatalf("closures: %+v", s.Closures)
	}
	if s.Software.Version != "2025.32.3" || !s.Software.UpdateAvailable || s.Software.UpdateVersion != "2025.38.1" {
		t.Fatalf("software: %+v", s.Software)
	}
	if s.Climate == nil || *s.Climate.InsideTempC != 21.5 || s.Climate.On {
		t.Fatalf("climate: %+v", s.Climate)
	}
	if snap.Model != "Model 3" || snap.Color != "MidnightSilver" || !s.UpdatedAt.Equal(now) {
		t.Fatalf("config: %+v", snap)
	}
}

func TestNormalizeChargeState(t *testing.T) {
	for in, want := range map[string]string{"": "disconnected", "Disconnected": "disconnected", "NoPower": "no_power",
		"Complete": "complete", "Starting": "starting", "Stopped": "stopped"} {
		if got := NormalizeChargeState(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

type staticTokens struct {
	forced atomic.Int32
}

func (s *staticTokens) AccessToken(_ context.Context, _ string, force bool) (string, string, error) {
	if force {
		s.forced.Add(1)
		return "fresh", "na", nil
	}
	return "stale", "na", nil
}

func fakeFleet(t *testing.T) (*httptest.Server, *RESTTransport, *staticTokens, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"token expired"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/1/vehicles", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"response":[{"id":1,"id_s":"1","vin":"VIN1","display_name":"A","state":"asleep"}],"count":1}`))
		}
	})
	mux.HandleFunc("GET /api/1/vehicles/{vin}/vehicle_data", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if !strings.Contains(r.URL.RawQuery, "location_data") {
			t.Errorf("vehicle_data must request location_data, got %q", r.URL.RawQuery)
		}
		if r.PathValue("vin") == "ASLEEP" {
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = w.Write([]byte(`{"response":null,"error":"vehicle unavailable: vehicle is offline or asleep"}`))
			return
		}
		_, _ = w.Write([]byte(vehicleDataFixture))
	})
	mux.HandleFunc("POST /api/1/vehicles/{vin}/command/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		switch r.PathValue("name") {
		case "honk_horn":
			w.WriteHeader(http.StatusTooManyRequests)
			w.Header().Set("Retry-After", "30")
		case "door_unlock":
			_, _ = w.Write([]byte(`{"response":{"result":false,"reason":"vehicle_unavailable"}}`))
		default:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.PathValue("name") == "set_temps" && body["driver_temp"] != 21.5 {
				t.Errorf("command body not forwarded: %v", body)
			}
			_, _ = w.Write([]byte(`{"response":{"result":true,"reason":""}}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	toks := &staticTokens{}
	tr, err := NewFleetTransport(toks, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tr.baseURL = func(string) string { return srv.URL }
	return srv, tr, toks, &calls
}

func TestFleetTransportRefreshesOnceOn401(t *testing.T) {
	_, tr, toks, _ := fakeFleet(t)
	ctx := context.Background()
	snap, err := tr.VehicleData(ctx, "u1", "VIN1")
	if err != nil {
		t.Fatal(err)
	}
	if toks.forced.Load() != 1 || snap.State.Battery.Percent != 64 {
		t.Fatalf("forced=%d snap=%+v", toks.forced.Load(), snap.State.Battery)
	}
}

func TestFleetTransportErrors(t *testing.T) {
	_, tr, _, _ := fakeFleet(t)
	ctx := context.Background()
	if _, err := tr.VehicleData(ctx, "u1", "ASLEEP"); !errors.Is(err, transport.ErrVehicleUnavailable) {
		t.Fatalf("asleep: %v", err)
	}
	err := tr.Command(ctx, "u1", "VIN1", transport.Command{Name: "door_unlock"})
	var ce *transport.CommandError
	if !errors.As(err, &ce) || ce.Reason != "vehicle_unavailable" {
		t.Fatalf("command refusal: %v", err)
	}
	err = tr.Command(ctx, "u1", "VIN1", transport.Command{Name: "honk_horn"})
	var rl *transport.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("rate limit: %v", err)
	}
	if err := tr.Command(ctx, "u1", "VIN1", transport.Command{Name: "set_temps", Body: map[string]any{"driver_temp": 21.5}}); err != nil {
		t.Fatal(err)
	}
	vs, err := tr.ListVehicles(ctx, "u1")
	if err != nil || len(vs) != 1 || vs[0].State != "asleep" || vs[0].TeslaID != "1" {
		t.Fatalf("list: %v %+v", err, vs)
	}
}

func TestOwnerTransportAddressesByID(t *testing.T) {
	srv, _, _, calls := fakeFleet(t)
	ow := NewOwnerTransport(&staticTokens{})
	ow.baseURL = func(string) string { return srv.URL }
	if _, err := ow.VehicleData(context.Background(), "u1", "VIN1"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range *calls {
		if c == "GET /api/1/vehicles/1/vehicle_data" {
			found = true
		}
	}
	if !found {
		t.Fatalf("owner transport should address by numeric id: %v", *calls)
	}
}

func fakeJWT(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(claims)) + ".sig"
}

func TestIdentityOf(t *testing.T) {
	id, err := IdentityOf(&Token{AccessToken: "opaque", IDToken: fakeJWT(`{"sub":"abc-123","email":"o@example.com"}`)})
	if err != nil || id.Subject != "abc-123" || id.Email != "o@example.com" {
		t.Fatalf("identity: %v %+v", err, id)
	}
	if _, err := IdentityOf(&Token{AccessToken: "opaque"}); err == nil {
		t.Fatal("tokens without subject must fail")
	}
}

func TestCredentialsRefreshIsSingleFlight(t *testing.T) {
	var refreshes atomic.Int32
	var lastBody string
	var mu sync.Mutex
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		mu.Lock()
		lastBody = buf.String()
		mu.Unlock()
		n := refreshes.Add(1)
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-new", "refresh_token": "rt-" + string(rune('0'+n)), "expires_in": 28800})
	}))
	defer tokenSrv.Close()

	ctx := context.Background()
	st := memory.New()
	u, _ := st.UpsertUser(ctx, "tesla:x", "", "fleet")
	ciph, _ := auth.NewCipher(bytes.Repeat([]byte{1}, 32))
	oa := NewFleetOAuth(nil, "cid", "secret", nil)
	oa.TokenURL = tokenSrv.URL
	creds := &Credentials{Store: st, Cipher: ciph, OAuth: map[string]*OAuth{"fleet": oa}}
	// Store an already-expired token.
	creds.Now = func() time.Time { return time.Now().Add(-9 * time.Hour) }
	if err := creds.Save(ctx, u.ID, "fleet", "eu", &Token{AccessToken: "at-old", RefreshToken: "rt-0", ExpiresIn: 3600, Scope: "openid vehicle_cmds"}); err != nil {
		t.Fatal(err)
	}
	creds.Now = nil

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, region, err := creds.AccessToken(ctx, u.ID, false)
			if err != nil || tok != "at-new" || region != "eu" {
				t.Errorf("token: %v %q %q", err, tok, region)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("expected exactly one refresh, got %d", refreshes.Load())
	}
	if !strings.Contains(lastBody, "refresh_token=rt-0") || !strings.Contains(lastBody, "grant_type=refresh_token") {
		t.Fatalf("refresh body: %s", lastBody)
	}
	c, _ := st.GetCredentials(ctx, u.ID)
	if len(c.Scopes) != 2 {
		t.Fatalf("scopes should be preserved across refresh: %v", c.Scopes)
	}
	if bytes.Contains(c.AccessTokenEnc, []byte("at-new")) {
		t.Fatal("token stored in plaintext")
	}
}
