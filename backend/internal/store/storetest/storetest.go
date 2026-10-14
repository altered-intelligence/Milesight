// Package storetest is a conformance suite every store.Store must pass, so the
// in-memory and Postgres implementations stay behaviorally identical.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

func f(v float64) *float64 { return &v }
func b(v bool) *bool       { return &v }

// Run exercises the full contract against a fresh store from newStore.
func Run(t *testing.T, newStore func(t *testing.T) store.Store) {
	t.Run("users", func(t *testing.T) { testUsers(t, newStore(t)) })
	t.Run("refresh_tokens", func(t *testing.T) { testRefresh(t, newStore(t)) })
	t.Run("auth_states", func(t *testing.T) { testAuthStates(t, newStore(t)) })
	t.Run("vehicles", func(t *testing.T) { testVehicles(t, newStore(t)) })
	t.Run("telemetry", func(t *testing.T) { testTelemetry(t, newStore(t)) })
	t.Run("history", func(t *testing.T) { testHistory(t, newStore(t)) })
	t.Run("events_rules_places_devices", func(t *testing.T) { testMisc(t, newStore(t)) })
	t.Run("delete_user", func(t *testing.T) { testDeleteUser(t, newStore(t)) })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func setupVehicle(t *testing.T, s store.Store, ext string) (*models.User, *store.VehicleRecord) {
	t.Helper()
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, ext, ext+"@example.com", "sim")
	must(t, err)
	v := &store.VehicleRecord{VIN: "VIN" + ids.Secret(6), TeslaID: "1", DisplayName: "Car", Model: "Model 3", Region: "na"}
	must(t, s.UpsertVehicle(ctx, v))
	must(t, s.LinkVehicle(ctx, u.ID, v.ID, "owner"))
	return u, v
}

func testUsers(t *testing.T, s store.Store) {
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, "dev:a", "a@example.com", "sim")
	must(t, err)
	if u.Preferences.Currency != "USD" || u.Plan != "free" {
		t.Fatalf("defaults not applied: %+v", u)
	}
	again, err := s.UpsertUser(ctx, "dev:a", "", "sim")
	must(t, err)
	if again.ID != u.ID || again.Email != "a@example.com" {
		t.Fatalf("upsert should be idempotent and keep email: %+v", again)
	}
	p := u.Preferences
	p.DistanceUnit, p.EnergyRatePerKWh = "km", 0.31
	must(t, s.UpdatePreferences(ctx, u.ID, p))
	got, err := s.GetUser(ctx, u.ID)
	must(t, err)
	if got.Preferences.DistanceUnit != "km" || got.Preferences.EnergyRatePerKWh != 0.31 {
		t.Fatalf("prefs not saved: %+v", got.Preferences)
	}
	if _, err := s.GetUser(ctx, ids.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	must(t, s.SaveCredentials(ctx, &store.Credentials{UserID: u.ID, Transport: "fleet", AccessTokenEnc: []byte{1},
		RefreshTokenEnc: []byte{2}, Scopes: []string{"openid"}, Region: "eu", ExpiresAt: exp}))
	c, err := s.GetCredentials(ctx, u.ID)
	must(t, err)
	if c.Region != "eu" || !c.ExpiresAt.Equal(exp) || len(c.Scopes) != 1 || c.RefreshTokenEnc[0] != 2 {
		t.Fatalf("credentials roundtrip: %+v", c)
	}
}

func testRefresh(t *testing.T, s store.Store) {
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, "dev:r", "", "sim")
	must(t, err)
	fam := ids.New()
	now := time.Now().UTC()
	rt := &store.RefreshToken{UserID: u.ID, FamilyID: fam, Hash: []byte("h1"), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	must(t, s.InsertRefreshToken(ctx, rt))
	got, err := s.GetRefreshTokenByHash(ctx, []byte("h1"))
	must(t, err)
	if got.ID != rt.ID || got.FamilyID != fam {
		t.Fatalf("lookup mismatch %+v", got)
	}
	ok, err := s.MarkRefreshTokenUsed(ctx, rt.ID, now)
	must(t, err)
	if !ok {
		t.Fatal("first use should succeed")
	}
	ok, err = s.MarkRefreshTokenUsed(ctx, rt.ID, now)
	must(t, err)
	if ok {
		t.Fatal("second use must be rejected (reuse detection)")
	}
	rt2 := &store.RefreshToken{UserID: u.ID, FamilyID: fam, Hash: []byte("h2"), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	must(t, s.InsertRefreshToken(ctx, rt2))
	must(t, s.RevokeRefreshFamily(ctx, fam, now))
	got2, err := s.GetRefreshTokenByHash(ctx, []byte("h2"))
	must(t, err)
	if got2.RevokedAt == nil {
		t.Fatal("family revoke should revoke all members")
	}
}

func testAuthStates(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC()
	must(t, s.SaveAuthState(ctx, &store.AuthState{State: "st1", Transport: "fleet", Region: "na", RedirectURI: "https://x/cb",
		CodeVerifier: "v", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}))
	a, err := s.ConsumeAuthState(ctx, "st1", now)
	must(t, err)
	if a.RedirectURI != "https://x/cb" || a.CodeVerifier != "v" {
		t.Fatalf("auth state roundtrip %+v", a)
	}
	if _, err := s.ConsumeAuthState(ctx, "st1", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("auth state must be one-shot")
	}
	must(t, s.SaveAuthState(ctx, &store.AuthState{State: "st2", Transport: "fleet", RedirectURI: "r", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}))
	if _, err := s.ConsumeAuthState(ctx, "st2", now.Add(2*time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired state must be rejected")
	}
}

func testVehicles(t *testing.T, s store.Store) {
	ctx := context.Background()
	u, v := setupVehicle(t, s, "dev:v")
	// Re-upsert by VIN keeps id and user-chosen name.
	must(t, s.RenameVehicle(ctx, v.ID, "Midnight"))
	v2 := &store.VehicleRecord{VIN: v.VIN, TeslaID: "2", DisplayName: "Tesla Name", Color: "Red"}
	must(t, s.UpsertVehicle(ctx, v2))
	if v2.ID != v.ID {
		t.Fatalf("upsert by VIN must keep id: %s vs %s", v2.ID, v.ID)
	}
	list, err := s.ListUserVehicles(ctx, u.ID)
	must(t, err)
	if len(list) != 1 || list[0].DisplayName != "Midnight" || list[0].Color != "Red" || list[0].Role != "owner" {
		t.Fatalf("list: %+v", list)
	}
	other, err := s.UpsertUser(ctx, "dev:other", "", "sim")
	must(t, err)
	if _, _, err := s.GetUserVehicle(ctx, other.ID, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("non-member must not see vehicle")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	ls := &models.LiveState{VehicleID: v.ID, State: "online", Battery: models.Battery{Percent: 77, RangeKm: 400}, UpdatedAt: now}
	must(t, s.UpdateVehicleStatus(ctx, v.ID, "online", "polling", &now, ls))
	rec, role, err := s.GetUserVehicle(ctx, u.ID, v.ID)
	must(t, err)
	if role != "owner" || rec.State != "online" || rec.Latest == nil || rec.Latest.Battery.Percent != 77 || rec.LastSeen == nil {
		t.Fatalf("status: %+v", rec)
	}
	// Partial update keeps previous latest.
	must(t, s.UpdateVehicleStatus(ctx, v.ID, "asleep", "", nil, nil))
	rec, _ = s.GetVehicle(ctx, v.ID)
	if rec.State != "asleep" || rec.SyncStatus != "polling" || rec.Latest == nil {
		t.Fatalf("partial status: %+v", rec)
	}
	// Poll targets need credentials.
	pts, err := s.ListPollTargets(ctx)
	must(t, err)
	if len(pts) != 0 {
		t.Fatalf("no creds yet, want 0 targets, got %d", len(pts))
	}
	must(t, s.SaveCredentials(ctx, &store.Credentials{UserID: u.ID, Transport: "sim", AccessTokenEnc: []byte{0},
		RefreshTokenEnc: []byte{0}, ExpiresAt: now.Add(time.Hour)}))
	pts, err = s.ListPollTargets(ctx)
	must(t, err)
	if len(pts) != 1 || pts[0].UserID != u.ID || pts[0].Transport != "sim" {
		t.Fatalf("poll targets: %+v", pts)
	}
	if _, err := s.GetProcessorState(ctx, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("processor state should start empty")
	}
	must(t, s.SaveProcessorState(ctx, v.ID, []byte(`{"a":1}`)))
	ps, err := s.GetProcessorState(ctx, v.ID)
	must(t, err)
	if len(ps) == 0 {
		t.Fatal("processor state not saved")
	}
	users, err := s.VehicleUsers(ctx, v.ID)
	must(t, err)
	if len(users) != 1 || users[0].UserID != u.ID {
		t.Fatalf("vehicle users: %+v", users)
	}
}

func testTelemetry(t *testing.T, s store.Store) {
	ctx := context.Background()
	_, v := setupVehicle(t, s, "dev:t")
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	var pts []models.TelemetryPoint
	for i := 0; i < 10; i++ {
		pts = append(pts, models.TelemetryPoint{Time: base.Add(time.Duration(i) * 30 * time.Second), State: "online",
			BatteryPct: f(float64(80 - i)), SpeedKph: f(float64(i * 10)), ShiftState: "D", Locked: b(i%2 == 0)})
	}
	must(t, s.InsertTelemetry(ctx, v.ID, pts[:1]))
	must(t, s.InsertTelemetry(ctx, v.ID, pts[1:]))
	raw, err := s.QueryTelemetry(ctx, v.ID, base, base.Add(time.Hour), 0, 0)
	must(t, err)
	if len(raw) != 10 || *raw[3].BatteryPct != 77 || raw[3].ShiftState != "D" || raw[0].Lat != nil {
		t.Fatalf("raw telemetry: %d %+v", len(raw), raw[3])
	}
	bk, err := s.QueryTelemetry(ctx, v.ID, base, base.Add(time.Hour), 2*time.Minute, 0)
	must(t, err)
	// 10 points × 30 s = 5 min → buckets [0,2) [2,4) [4,5) minutes = 3 buckets.
	if len(bk) != 3 {
		t.Fatalf("buckets: %d", len(bk))
	}
	if *bk[0].BatteryPct != 78.5 || !bk[0].Time.Equal(base) || *bk[0].Locked != false {
		t.Fatalf("bucket 0: %+v battery=%v", bk[0], *bk[0].BatteryPct)
	}
	lim, err := s.QueryTelemetry(ctx, v.ID, base, base.Add(time.Hour), 0, 4)
	must(t, err)
	if len(lim) != 4 {
		t.Fatalf("limit: %d", len(lim))
	}
}

func testHistory(t *testing.T, s store.Store) {
	ctx := context.Background()
	_, v := setupVehicle(t, s, "dev:h")
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		start := base.Add(time.Duration(i) * 24 * time.Hour)
		end := start.Add(30 * time.Minute)
		eff := 150.0
		tr := &models.Trip{VehicleID: v.ID, StartedAt: start, EndedAt: &end, DistanceKm: 20, DurationS: 1800,
			StartLocation: &models.GeoPoint{Lat: 1, Lon: 2}, EfficiencyWhKm: &eff, StartPlace: "Home"}
		must(t, s.SaveTrip(ctx, tr))
		cost := 1.5
		ch := &models.ChargeSession{VehicleID: v.ID, StartedAt: end, EnergyAddedKWh: 10, Cost: &cost, Currency: "USD",
			Location: &models.GeoPoint{Lat: 3, Lon: 4}, FastCharger: i == 2}
		must(t, s.SaveChargeSession(ctx, ch))
	}
	trips, err := s.ListTrips(ctx, v.ID, store.ListOptions{Limit: 3})
	must(t, err)
	if len(trips) != 3 || !trips[0].StartedAt.Equal(base.Add(96*time.Hour)) || trips[0].StartLocation == nil || trips[0].EndLocation != nil {
		t.Fatalf("trips page 1: %+v", trips)
	}
	before := trips[2].StartedAt
	page2, err := s.ListTrips(ctx, v.ID, store.ListOptions{Limit: 3, Before: &before})
	must(t, err)
	if len(page2) != 2 {
		t.Fatalf("trips page 2: %d", len(page2))
	}
	ranged, err := s.ListTrips(ctx, v.ID, store.ListOptions{From: base.Add(24 * time.Hour), To: base.Add(72 * time.Hour)})
	must(t, err)
	if len(ranged) != 2 {
		t.Fatalf("ranged trips: %d", len(ranged))
	}
	// Upsert updates in place.
	tr := trips[0]
	tr.DistanceKm = 42
	must(t, s.SaveTrip(ctx, &tr))
	got, err := s.GetTrip(ctx, v.ID, tr.ID)
	must(t, err)
	if got.DistanceKm != 42 || got.EfficiencyWhKm == nil || *got.EfficiencyWhKm != 150 {
		t.Fatalf("trip upsert: %+v", got)
	}
	charges, err := s.ListChargeSessions(ctx, v.ID, store.ListOptions{})
	must(t, err)
	if len(charges) != 5 || charges[0].Cost == nil || charges[0].Location == nil {
		t.Fatalf("charges: %+v", charges)
	}
	c, err := s.GetChargeSession(ctx, v.ID, charges[2].ID)
	must(t, err)
	if !c.FastCharger || c.EndedAt != nil {
		t.Fatalf("charge get: %+v", c)
	}
	must(t, s.InsertDrain(ctx, models.DrainSegment{VehicleID: v.ID, StartedAt: base, EndedAt: base.Add(8 * time.Hour), RangeLostKm: 3, EnergyKWh: 0.4}))
	dr, err := s.ListDrain(ctx, v.ID, base.Add(time.Hour), base.Add(2*time.Hour))
	must(t, err)
	if len(dr) != 1 || dr[0].RangeLostKm != 3 {
		t.Fatalf("drain overlap: %+v", dr)
	}
}

func testMisc(t *testing.T, s store.Store) {
	ctx := context.Background()
	u, v := setupVehicle(t, s, "dev:m")
	stranger, err := s.UpsertUser(ctx, "dev:stranger", "", "sim")
	must(t, err)
	for i := 0; i < 3; i++ {
		e := &models.Event{VehicleID: v.ID, Type: models.EventChargeComplete, Title: "Done", OccurredAt: time.Now().UTC(),
			Payload: map[string]any{"pct": 80}}
		must(t, s.InsertEvent(ctx, e))
		if e.ID == 0 {
			t.Fatal("event id not set")
		}
	}
	evs, err := s.ListEvents(ctx, u.ID, "", 2, 0)
	must(t, err)
	if len(evs) != 2 || evs[0].ID < evs[1].ID || evs[0].Payload["pct"] == nil {
		t.Fatalf("events: %+v", evs)
	}
	older, err := s.ListEvents(ctx, u.ID, v.ID, 10, evs[1].ID)
	must(t, err)
	if len(older) != 1 {
		t.Fatalf("events before: %d", len(older))
	}
	none, err := s.ListEvents(ctx, stranger.ID, "", 10, 0)
	must(t, err)
	if len(none) != 0 {
		t.Fatal("strangers must not see events")
	}
	must(t, s.MarkEventDelivered(ctx, evs[0].ID, time.Now().UTC()))

	must(t, s.SaveRules(ctx, u.ID, []models.Rule{{Type: "low_battery", Enabled: true, Params: map[string]float64{"threshold_pct": 15}}, {Type: "trip_complete", Enabled: false}}))
	rules, err := s.GetRules(ctx, u.ID)
	must(t, err)
	if len(rules) != 2 {
		t.Fatalf("rules: %+v", rules)
	}
	for _, r := range rules {
		if r.Type == "low_battery" && r.Params["threshold_pct"] != 15 {
			t.Fatalf("rule params: %+v", r)
		}
	}

	rate := 0.12
	g := &models.Geofence{Name: "Home", Lat: 37.4, Lon: -122.1, RadiusM: 150, EnergyRatePerKWh: &rate}
	must(t, s.CreateGeofence(ctx, u.ID, g))
	if g.ID == "" {
		t.Fatal("geofence id not set")
	}
	g.Name = "Casa"
	must(t, s.UpdateGeofence(ctx, u.ID, g))
	gs, err := s.ListGeofences(ctx, u.ID)
	must(t, err)
	if len(gs) != 1 || gs[0].Name != "Casa" || gs[0].EnergyRatePerKWh == nil {
		t.Fatalf("geofences: %+v", gs)
	}
	if err := s.UpdateGeofence(ctx, stranger.ID, g); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("other users must not update geofence")
	}
	must(t, s.DeleteGeofence(ctx, u.ID, g.ID))

	d := &models.PushDevice{Token: "tok1", Platform: "ios", Environment: "sandbox"}
	must(t, s.UpsertPushDevice(ctx, u.ID, d))
	// Same token re-registered by another user moves ownership.
	must(t, s.UpsertPushDevice(ctx, stranger.ID, &models.PushDevice{Token: "tok1", Platform: "ios", Environment: "sandbox"}))
	mine, err := s.ListPushDevices(ctx, u.ID)
	must(t, err)
	if len(mine) != 0 {
		t.Fatalf("token should have moved: %+v", mine)
	}
	must(t, s.DeletePushToken(ctx, "tok1"))
	theirs, _ := s.ListPushDevices(ctx, stranger.ID)
	if len(theirs) != 0 {
		t.Fatal("token should be gone")
	}

	rec := &store.IdempotencyRecord{UserID: u.ID, Key: "k1", RequestHash: "h", StatusCode: 200, Body: []byte(`{}`), CreatedAt: time.Now().UTC()}
	must(t, s.SaveIdempotency(ctx, rec))
	if err := s.SaveIdempotency(ctx, rec); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate idempotency key: %v", err)
	}
	got, err := s.GetIdempotency(ctx, u.ID, "k1")
	must(t, err)
	if got.StatusCode != 200 || string(got.Body) != "{}" {
		t.Fatalf("idempotency get: %+v", got)
	}
	must(t, s.InsertCommandLog(ctx, &store.CommandLog{VehicleID: v.ID, UserID: u.ID, Name: "lock", Status: "ok", CreatedAt: time.Now().UTC()}))
}

func testDeleteUser(t *testing.T, s store.Store) {
	ctx := context.Background()
	u, v := setupVehicle(t, s, "dev:del")
	shared, err := s.UpsertUser(ctx, "dev:shared", "", "sim")
	must(t, err)
	v2 := &store.VehicleRecord{VIN: "SHARED" + ids.Secret(4)}
	must(t, s.UpsertVehicle(ctx, v2))
	must(t, s.LinkVehicle(ctx, u.ID, v2.ID, "owner"))
	must(t, s.LinkVehicle(ctx, shared.ID, v2.ID, "viewer"))
	must(t, s.InsertTelemetry(ctx, v.ID, []models.TelemetryPoint{{Time: time.Now().UTC(), BatteryPct: f(50)}}))
	must(t, s.DeleteUser(ctx, u.ID))
	if _, err := s.GetUser(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("user should be gone")
	}
	if _, err := s.GetVehicle(ctx, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("orphaned vehicle should be deleted")
	}
	if _, err := s.GetVehicle(ctx, v2.ID); err != nil {
		t.Fatal("shared vehicle must survive")
	}
	pts, _ := s.QueryTelemetry(ctx, v.ID, time.Time{}, time.Now().Add(time.Hour), 0, 0)
	if len(pts) != 0 {
		t.Fatal("telemetry of deleted vehicle should be gone")
	}
}
