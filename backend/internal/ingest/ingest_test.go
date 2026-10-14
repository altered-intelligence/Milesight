package ingest

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/notify"
	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/sim"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/store/memory"
	"github.com/altered-intelligence/milesight/internal/transport"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type countingPusher struct{ n int }

func (p *countingPusher) Push(context.Context, models.PushDevice, notify.Notification) error {
	p.n++
	return nil
}

// TestPipelineWithSimulator drives the real poll loop (with a virtual clock)
// against the simulator for several hours and checks every downstream effect.
func TestPipelineWithSimulator(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
	st := memory.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	simT := sim.New(sim.Options{HomeLat: 37.3947, HomeLon: -122.1503, Vehicles: 1, Now: clk.now})
	hub := realtime.NewHub()
	pusher := &countingPusher{}
	m := &Manager{
		Store: st, Transports: transport.Registry{"sim": simT}, Hub: hub, Log: log, HistoryDays: 2, Now: clk.now,
		Dispatcher: &notify.Dispatcher{Store: st, Pusher: pusher, Log: log, Now: clk.now},
	}
	u, _ := st.UpsertUser(ctx, "dev:pipeline", "p@example.com", "sim")
	if err := st.SaveCredentials(ctx, &store.Credentials{UserID: u.ID, Transport: "sim", AccessTokenEnc: []byte{0},
		RefreshTokenEnc: []byte{0}, ExpiresAt: clk.t.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertPushDevice(ctx, u.ID, &models.PushDevice{Token: "phone", Environment: "sandbox"})
	_ = st.SaveRules(ctx, u.ID, []models.Rule{{Type: models.EventTripComplete, Enabled: true}})
	// Geofence the simulator's home so places and notifications resolve.
	_ = st.CreateGeofence(ctx, u.ID, &models.Geofence{Name: "Home", Lat: 37.3947, Lon: -122.1503, RadiusM: 300})

	vehicles, err := m.SyncAccount(ctx, u.ID, "sim")
	if err != nil || len(vehicles) != 1 {
		t.Fatalf("sync: %v %+v", err, vehicles)
	}
	vid := vehicles[0].ID
	targets, _ := st.ListPollTargets(ctx)
	if len(targets) != 1 {
		t.Fatalf("targets: %+v", targets)
	}
	w := &worker{vehicleID: vid, target: targets[0], poke: make(chan []time.Duration, 1)}
	vs := &vstate{state: models.StateUnknown}
	m.loadState(ctx, w, vs)

	seeded, _ := st.ListTrips(ctx, vid, store.ListOptions{})
	if len(seeded) == 0 {
		t.Fatal("history seeding produced no trips")
	}

	sub := hub.Subscribe(vid)
	defer hub.Unsubscribe(sub)
	msgs := map[string]int{}
	drain := func() {
		for {
			select {
			case msg, ok := <-sub.C():
				if !ok {
					sub = hub.Subscribe(vid)
					return
				}
				msgs[msg.Type]++
			default:
				return
			}
		}
	}

	var polls, onlinePolls int
	end := clk.t.Add(3 * time.Hour)
	for clk.t.Before(end) {
		d := m.poll(ctx, w, vs, false)
		if d <= 0 {
			t.Fatalf("non-positive interval %v", d)
		}
		polls++
		if vs.state == models.StateOnline {
			onlinePolls++
		}
		drain()
		clk.t = clk.t.Add(d)
	}
	trips, _ := st.ListTrips(ctx, vid, store.ListOptions{From: end.Add(-3 * time.Hour)})
	charges, _ := st.ListChargeSessions(ctx, vid, store.ListOptions{From: end.Add(-3 * time.Hour)})
	var closedTrips int
	for _, tr := range trips {
		if tr.EndedAt != nil {
			closedTrips++
		}
	}
	if closedTrips < 3 || len(charges) < 2 {
		t.Fatalf("live segmentation: trips=%d (closed %d) charges=%d", len(trips), closedTrips, len(charges))
	}
	rec, _ := st.GetVehicle(ctx, vid)
	if rec.Model != "Model 3" || rec.LastSeen == nil || rec.Latest == nil {
		t.Fatalf("vehicle record not maintained: %+v", rec)
	}
	if msgs[realtime.TypeState] < 50 || msgs[realtime.TypeTrip] == 0 || msgs[realtime.TypeCharge] == 0 {
		t.Fatalf("hub messages: %v", msgs)
	}
	events, _ := st.ListEvents(ctx, u.ID, vid, 100, 0)
	var arrivals int
	for _, e := range events {
		if e.Type == models.EventGeofenceArrive {
			arrivals++
		}
	}
	if len(events) == 0 || pusher.n == 0 {
		t.Fatalf("expected events and pushes (trip_complete enabled): events=%d pushes=%d", len(events), pusher.n)
	}
	pts, _ := st.QueryTelemetry(ctx, vid, end.Add(-3*time.Hour), end, 0, 0)
	if len(pts) < onlinePolls/2 {
		t.Fatalf("telemetry rows %d for %d online polls", len(pts), onlinePolls)
	}
	t.Logf("3h live: %d polls (%d online), %d trips, %d charges, %d events (%d arrivals), %d pushes, hub=%v",
		polls, onlinePolls, len(trips), len(charges), len(events), arrivals, pusher.n, msgs)
}

func TestSuspendLetsIdleCarSleep(t *testing.T) {
	pol := transport.DefaultPolicy()
	if pol.IdleBeforeSuspend <= pol.OnlineIdle || pol.Suspend < 10*time.Minute {
		t.Fatalf("policy must let cars sleep: %+v", pol)
	}
}
