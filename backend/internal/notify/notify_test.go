package notify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/analytics"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/store/memory"
)

var base = time.Date(2026, 6, 1, 18, 0, 0, 0, time.UTC)

func live(min int, mut func(*models.LiveState)) *models.LiveState {
	ls := &models.LiveState{State: models.StateOnline, UpdatedAt: base.Add(time.Duration(min) * time.Minute),
		Battery:  models.Battery{Percent: 60, RangeKm: 300, ChargeLimitPct: 80, ChargeState: models.ChargeDisconnected},
		Drive:    &models.Drive{ShiftState: "P"},
		Closures: &models.Closures{Locked: true},
		Location: &models.Location{Lat: 1, Lon: 1, Place: "Home"},
		Software: &models.Software{Version: "2025.38.6"},
	}
	if mut != nil {
		mut(ls)
	}
	return ls
}

func types(ds []Draft) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Type)
	}
	return out
}

func TestEvaluateLowBatteryHysteresis(t *testing.T) {
	st := &analytics.NotifyState{}
	rules := Merge(nil)
	seq := []int{30, 21, 20, 19, 21, 20, 26, 20}
	var fired []int
	for i, pct := range seq {
		ds := Evaluate(st, Input{Cur: live(i, func(l *models.LiveState) { l.Battery.Percent = pct }), Rules: rules})
		for _, d := range ds {
			if d.Type == models.EventLowBattery {
				fired = append(fired, pct)
			}
		}
	}
	if len(fired) != 2 || fired[0] != 20 || fired[1] != 20 {
		t.Fatalf("low battery should fire at 20 then again only after recovering above 25: %v", fired)
	}
}

func TestEvaluateLeftUnlockedAndOpen(t *testing.T) {
	st := &analytics.NotifyState{}
	rules := Merge([]models.Rule{{Type: models.EventLeftUnlocked, Enabled: true, Params: map[string]float64{"minutes": 5}}})
	unlocked := func(l *models.LiveState) { l.Closures.Locked = false; l.Closures.TrunkOpen = true }
	var got []string
	for _, m := range []int{0, 4, 5, 6, 12} {
		got = append(got, types(Evaluate(st, Input{Cur: live(m, unlocked), Rules: rules}))...)
	}
	want := "left_unlocked,left_open"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s (unlocked at 5 min, open at default 10 min, each once)", got, want)
	}
	// Locking resets; unlocking again re-arms.
	Evaluate(st, Input{Cur: live(13, nil), Rules: rules})
	if st.UnlockedSince != nil || st.OpenSince != nil {
		t.Fatal("state should reset once locked/closed")
	}
	// User present in the car never triggers.
	st2 := &analytics.NotifyState{}
	for _, m := range []int{0, 30} {
		if ds := Evaluate(st2, Input{Cur: live(m, func(l *models.LiveState) { l.Closures.Locked = false; l.Closures.UserPresent = true }), Rules: rules}); len(ds) != 0 {
			t.Fatalf("user present should suppress: %v", types(ds))
		}
	}
}

func TestEvaluateGeofenceUpdateAndSegments(t *testing.T) {
	st := &analytics.NotifyState{}
	rules := Merge(nil)
	// First sample only learns the place; software update fires once.
	if got := types(Evaluate(st, Input{Cur: live(0, func(l *models.LiveState) {
		l.Software.UpdateAvailable, l.Software.UpdateVersion = true, "2025.44.1"
	}), Rules: rules})); strings.Join(got, ",") != "software_update" {
		t.Fatalf("first eval: %v", got)
	}
	got := types(Evaluate(st, Input{Cur: live(1, func(l *models.LiveState) {
		l.Location.Place = "Work"
		l.Software.UpdateAvailable, l.Software.UpdateVersion = true, "2025.44.1"
	}), Rules: rules}))
	if strings.Join(got, ",") != "geofence_depart,geofence_arrive" {
		t.Fatalf("place change: %v", got)
	}
	end := base
	cost := 3.2
	ch := &models.ChargeSession{ID: "c1", EndBatteryPct: 80, EnergyAddedKWh: 21, Cost: &cost, Currency: "USD", EndReason: "complete", EndedAt: &end}
	eff := 150.0
	tr := &models.Trip{ID: "t1", DistanceKm: 16.09, DurationS: 1500, EfficiencyWhKm: &eff, EndPlace: "Work"}
	ds := Evaluate(st, Input{Cur: live(2, func(l *models.LiveState) { l.Location.Place = "Work" }), Rules: rules,
		Segments: []analytics.Event{{Kind: analytics.ChargeEnded, Charge: ch}, {Kind: analytics.TripEnded, Trip: tr}}})
	if strings.Join(types(ds), ",") != "charge_complete,trip_complete" {
		t.Fatalf("segments: %v", types(ds))
	}
	title, body := Render(ds[0].Type, ds[0].Payload, "Midnight", models.DefaultPreferences())
	if title != "Midnight finished charging" || body != "Charged to 80% · 21.0 kWh added · USD 3.20" {
		t.Fatalf("charge render: %q / %q", title, body)
	}
	_, body = Render(ds[1].Type, ds[1].Payload, "Midnight", models.DefaultPreferences())
	if body != "10.0 mi in 25 min · 241 Wh/mi · to Work" {
		t.Fatalf("trip render (imperial): %q", body)
	}
	km := models.DefaultPreferences()
	km.DistanceUnit = "km"
	if _, body = Render(ds[1].Type, ds[1].Payload, "Midnight", km); body != "16.1 km in 25 min · 150 Wh/km · to Work" {
		t.Fatalf("trip render (metric): %q", body)
	}
	if DeepLink("v", ds[1].Type, ds[1].Payload) != "milesight://vehicle/v/trips/t1" {
		t.Fatal("deep link")
	}
}

func TestRulesMergeAndValidate(t *testing.T) {
	m := Merge([]models.Rule{{Type: models.EventTripComplete, Enabled: true}, {Type: models.EventLowBattery, Enabled: true, Params: map[string]float64{"threshold_pct": 12}}, {Type: "bogus", Enabled: true}})
	if !Find(m, models.EventTripComplete).Enabled || Param(Find(m, models.EventLowBattery), "threshold_pct", 0) != 12 || len(m) != len(Defaults()) {
		t.Fatalf("merge: %+v", m)
	}
	if Param(Find(Merge(nil), models.EventLowBattery), "threshold_pct", 0) != 20 {
		t.Fatal("defaults must not be mutated by earlier merges")
	}
	for _, bad := range [][]models.Rule{
		{{Type: "nope"}},
		{{Type: models.EventLowBattery, Params: map[string]float64{"threshold_pct": 90}}},
		{{Type: models.EventChargeComplete, Params: map[string]float64{"x": 1}}},
		{{Type: models.EventChargeComplete}, {Type: models.EventChargeComplete}},
	} {
		if Validate(bad) == nil {
			t.Fatalf("should reject %+v", bad)
		}
	}
}

type recPusher struct {
	mu   sync.Mutex
	sent []Notification
	gone map[string]bool
}

func (r *recPusher) Push(_ context.Context, d models.PushDevice, n Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gone[d.Token] {
		return ErrDeviceGone
	}
	r.sent = append(r.sent, n)
	return nil
}

func TestDispatcherRespectsPerUserRulesAndPrunesTokens(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	owner, _ := st.UpsertUser(ctx, "dev:o", "", "sim")
	viewer, _ := st.UpsertUser(ctx, "dev:v", "", "sim")
	v := &store.VehicleRecord{VIN: "V1", DisplayName: "Midnight"}
	_ = st.UpsertVehicle(ctx, v)
	_ = st.LinkVehicle(ctx, owner.ID, v.ID, "owner")
	_ = st.LinkVehicle(ctx, viewer.ID, v.ID, "viewer")
	_ = st.UpsertPushDevice(ctx, owner.ID, &models.PushDevice{Token: "owner-phone", Environment: "sandbox"})
	_ = st.UpsertPushDevice(ctx, owner.ID, &models.PushDevice{Token: "owner-old", Environment: "sandbox"})
	_ = st.UpsertPushDevice(ctx, viewer.ID, &models.PushDevice{Token: "viewer-phone", Environment: "sandbox"})
	_ = st.SaveRules(ctx, viewer.ID, []models.Rule{{Type: models.EventChargeComplete, Enabled: false}})

	p := &recPusher{gone: map[string]bool{"owner-old": true}}
	d := &Dispatcher{Store: st, Pusher: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	evs := d.Deliver(ctx, v, owner.Preferences, []Draft{{Type: models.EventChargeComplete, At: base,
		Payload: map[string]any{"battery_pct": 80, "energy_kwh": 30.0, "charge_id": "c9"}}})
	if len(evs) != 1 || evs[0].ID == 0 || evs[0].DeliveredAt == nil || evs[0].Payload["deep_link"] != "milesight://vehicle/"+v.ID+"/charges/c9" {
		t.Fatalf("events: %+v", evs)
	}
	if len(p.sent) != 1 || p.sent[0].Title != "Midnight finished charging" {
		t.Fatalf("only the owner's live device should get it: %+v", p.sent)
	}
	devs, _ := st.ListPushDevices(ctx, owner.ID)
	if len(devs) != 1 || devs[0].Token != "owner-phone" {
		t.Fatalf("dead token should be pruned: %+v", devs)
	}
	feed, _ := st.ListEvents(ctx, viewer.ID, "", 10, 0)
	if len(feed) != 1 {
		t.Fatal("viewer still sees the event in the feed")
	}
}

func TestAPNsSignsAndSends(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	parsed, err := ParseP8(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	var gotPath, gotTopic string
	var payload apnsPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotTopic = r.URL.Path, r.Header.Get("apns-topic")
		tok := strings.TrimPrefix(r.Header.Get("authorization"), "bearer ")
		parts := strings.Split(tok, ".")
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		rr, ss := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if len(sig) != 64 || !ecdsa.Verify(&key.PublicKey, sum[:], rr, ss) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if strings.HasSuffix(r.URL.Path, "/dead") {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}
	}))
	defer srv.Close()
	a := &APNs{keyID: "K1", teamID: "T1", topic: "com.milesight.ios", key: parsed, client: srv.Client(), now: time.Now,
		hosts: map[string]string{"sandbox": srv.URL, "production": srv.URL}}
	n := Notification{Title: "Hi", Body: "There", Type: "charge_complete", VehicleID: "v1", EventID: 7, DeepLink: "milesight://vehicle/v1"}
	if err := a.Push(context.Background(), models.PushDevice{Token: "abc", Environment: "sandbox"}, n); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/3/device/abc" || gotTopic != "com.milesight.ios" || payload.APS.Alert.Title != "Hi" || payload.EventID != 7 {
		t.Fatalf("request: %s %s %+v", gotPath, gotTopic, payload)
	}
	if err := a.Push(context.Background(), models.PushDevice{Token: "dead", Environment: "sandbox"}, n); err != ErrDeviceGone {
		t.Fatalf("410 should map to ErrDeviceGone, got %v", err)
	}
}
