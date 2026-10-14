package commands

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/store/memory"
	"github.com/altered-intelligence/milesight/internal/transport"
)

func b(v bool) *bool       { return &v }
func i(v int) *int         { return &v }
func f(v float64) *float64 { return &v }
func names(cs []transport.Command) (out []string) {
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return
}

func TestPlan(t *testing.T) {
	cases := []struct {
		name string
		p    models.CommandParams
		want []string
		err  bool
	}{
		{models.CmdClimate, models.CommandParams{On: b(true), TempC: f(21.3)}, []string{"set_temps", "auto_conditioning_start"}, false},
		{models.CmdClimate, models.CommandParams{On: b(false)}, []string{"auto_conditioning_stop"}, false},
		{models.CmdClimate, models.CommandParams{}, nil, true},
		{models.CmdClimate, models.CommandParams{On: b(true), TempC: f(40)}, nil, true},
		{models.CmdFrunk, models.CommandParams{}, []string{"actuate_trunk"}, false},
		{models.CmdCharging, models.CommandParams{Action: "set_limit", LimitPct: i(85)}, []string{"set_charge_limit"}, false},
		{models.CmdCharging, models.CommandParams{Action: "set_limit", LimitPct: i(20)}, nil, true},
		{models.CmdCharging, models.CommandParams{Action: "set_amps", Amps: i(32)}, []string{"set_charging_amps"}, false},
		{models.CmdCharging, models.CommandParams{Action: "boost"}, nil, true},
		{models.CmdSentry, models.CommandParams{On: b(true)}, []string{"set_sentry_mode"}, false},
		{models.CmdWindows, models.CommandParams{Action: "close"}, []string{"window_control"}, false},
		{"self_destruct", models.CommandParams{}, nil, true},
	}
	for _, c := range cases {
		got, err := Plan(c.name, c.p, &models.LiveState{Location: &models.Location{Lat: 1, Lon: 2}})
		if (err != nil) != c.err {
			t.Fatalf("%s %+v: err=%v", c.name, c.p, err)
		}
		var ve *ValidationError
		if err != nil && !errors.As(err, &ve) {
			t.Fatalf("%s: want ValidationError, got %T", c.name, err)
		}
		if len(names(got)) != len(c.want) {
			t.Fatalf("%s: got %v want %v", c.name, names(got), c.want)
		}
	}
	got, _ := Plan(models.CmdClimate, models.CommandParams{On: b(true), TempC: f(21.3)}, nil)
	if got[0].Body["driver_temp"] != 21.5 {
		t.Fatalf("temperature should round to 0.5 °C: %v", got[0].Body)
	}
	got, _ = Plan(models.CmdWindows, models.CommandParams{Action: "close"}, &models.LiveState{Location: &models.Location{Lat: 1, Lon: 2}})
	if got[0].Body["lat"] != 1.0 {
		t.Fatalf("window close must carry the car's location: %v", got[0].Body)
	}
}

// fakeTransport records commands; the car starts asleep and wakes after N state checks.
type fakeTransport struct {
	mu        sync.Mutex
	asleep    bool
	wakeAfter int
	checks    int
	woke      bool
	sent      []string
	refuse    string
}

func (f *fakeTransport) Name() string { return "fake" }
func (f *fakeTransport) Policy() transport.PollPolicy {
	return transport.DefaultPolicy()
}
func (f *fakeTransport) ListVehicles(context.Context, string) ([]transport.Vehicle, error) {
	return nil, nil
}
func (f *fakeTransport) VehicleData(context.Context, string, string) (*transport.Snapshot, error) {
	return nil, transport.ErrUnsupported
}
func (f *fakeTransport) VehicleState(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.woke {
		f.checks++
		if f.checks >= f.wakeAfter {
			f.asleep = false
		}
	}
	if f.asleep {
		return models.StateAsleep, nil
	}
	return models.StateOnline, nil
}
func (f *fakeTransport) Wake(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.woke = true
	return nil
}
func (f *fakeTransport) Command(_ context.Context, _ string, _ string, c transport.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != "" {
		return &transport.CommandError{Reason: f.refuse}
	}
	f.sent = append(f.sent, c.Name)
	return nil
}

type poker struct{ n int }

func (p *poker) Poke(string, []time.Duration) { p.n++ }

func setup(t *testing.T, ft *fakeTransport) (*Service, *store.VehicleRecord, string, *poker) {
	ctx := context.Background()
	st := memory.New()
	u, _ := st.UpsertUser(ctx, "dev:c", "", "fake")
	_ = st.SaveCredentials(ctx, &store.Credentials{UserID: u.ID, Transport: "fake", AccessTokenEnc: []byte{0}, RefreshTokenEnc: []byte{0}, ExpiresAt: time.Now().Add(time.Hour)})
	v := &store.VehicleRecord{VIN: "VIN1"}
	_ = st.UpsertVehicle(ctx, v)
	_ = st.LinkVehicle(ctx, u.ID, v.ID, "owner")
	p := &poker{}
	return &Service{Store: st, Transports: transport.Registry{"fake": ft}, Poker: p,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), WakePoll: time.Millisecond, WakeTimeout: time.Second}, v, u.ID, p
}

func TestExecuteWakesThenSends(t *testing.T) {
	ft := &fakeTransport{asleep: true, wakeAfter: 3}
	s, v, uid, p := setup(t, ft)
	res, err := s.Execute(context.Background(), uid, v, "owner", models.CmdClimate, models.CommandParams{On: b(true), TempC: f(22)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.WokeUp || res.Status != "ok" || len(ft.sent) != 2 || p.n != 1 {
		t.Fatalf("res=%+v sent=%v pokes=%d", res, ft.sent, p.n)
	}
}

func TestExecuteRefusalViewerAndRateLimit(t *testing.T) {
	ft := &fakeTransport{refuse: "in_motion"}
	s, v, uid, _ := setup(t, ft)
	ctx := context.Background()
	res, err := s.Execute(ctx, uid, v, "owner", models.CmdTrunk, models.CommandParams{})
	if err != nil || res.Status != "failed" || res.Reason != "in_motion" {
		t.Fatalf("refusal: %v %+v", err, res)
	}
	if _, err := s.Execute(ctx, uid, v, "viewer", models.CmdLock, models.CommandParams{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer: %v", err)
	}
	ft.refuse = ""
	s.Limit, s.Window = 3, time.Minute
	s.history = nil
	for n := 0; n < 3; n++ {
		if _, err := s.Execute(ctx, uid, v, "owner", models.CmdHonk, models.CommandParams{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Execute(ctx, uid, v, "owner", models.CmdHonk, models.CommandParams{}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th command in a minute should be limited: %v", err)
	}
}

func TestWakeTimeout(t *testing.T) {
	ft := &fakeTransport{asleep: true, wakeAfter: 1 << 30}
	s, v, uid, _ := setup(t, ft)
	s.WakeTimeout = 20 * time.Millisecond
	res, err := s.Execute(context.Background(), uid, v, "owner", models.CmdLock, models.CommandParams{})
	if !errors.Is(err, ErrWakeTimeout) || res == nil || res.Status != "failed" || len(ft.sent) != 0 {
		t.Fatalf("wake timeout: %v %+v %v", err, res, ft.sent)
	}
}
