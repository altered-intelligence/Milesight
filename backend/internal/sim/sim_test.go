package sim

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/transport"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time      { return f.t }
func (f *fakeClock) add(d time.Duration) { f.t = f.t.Add(d) }
func newSim(c *fakeClock) *Transport {
	return New(Options{HomeLat: 37.3947, HomeLon: -122.1503, Vehicles: 2, Now: c.now})
}
func vins(t *testing.T, s *Transport) []string { return vinsFor(t, s, "user-1") }

func vinsFor(t *testing.T, s *Transport, user string) []string {
	t.Helper()
	vs, err := s.ListVehicles(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range vs {
		if len(v.VIN) != 17 {
			t.Fatalf("VIN must be 17 chars: %q", v.VIN)
		}
		out = append(out, v.VIN)
	}
	return out
}

func TestFleetIsDeterministicPerUser(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	a, b := vins(t, newSim(c)), vins(t, newSim(c))
	if len(a) != 2 || a[0] != b[0] || a[1] != b[1] {
		t.Fatalf("fleet not deterministic: %v vs %v", a, b)
	}
	if other := vinsFor(t, newSim(c), "user-2"); other[0] == a[0] {
		t.Fatal("different users must get different VINs")
	}
}

func TestHistoryIsPlausible(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := newSim(c)
	for _, vin := range vins(t, s) {
		samples, err := s.SeedHistory(context.Background(), "user-1", vin, 30)
		if err != nil {
			t.Fatal(err)
		}
		if len(samples) < 1000 {
			t.Fatalf("too few samples: %d", len(samples))
		}
		var driving, charging, asleep int
		prev := samples[0]
		for i, smp := range samples {
			if smp.UpdatedAt.Before(prev.UpdatedAt) {
				t.Fatalf("time went backwards at %d", i)
			}
			if smp.OdometerKm+1e-6 < prev.OdometerKm {
				t.Fatalf("odometer went backwards at %d: %v → %v", i, prev.OdometerKm, smp.OdometerKm)
			}
			if smp.Battery.Percent < 3 || smp.Battery.Percent > 100 {
				t.Fatalf("battery out of range at %d: %d", i, smp.Battery.Percent)
			}
			if smp.IsDriving() {
				driving++
			}
			if smp.IsCharging() {
				charging++
				if smp.Charging == nil || smp.Charging.PowerKW <= 0 {
					t.Fatalf("charging sample without power at %d", i)
				}
			}
			if smp.State == models.StateAsleep {
				asleep++
			}
			prev = smp
		}
		if driving < 300 || charging < 30 || asleep < 25 {
			t.Fatalf("history lacks variety: driving=%d charging=%d asleep=%d", driving, charging, asleep)
		}
		dist := samples[len(samples)-1].OdometerKm - samples[0].OdometerKm
		if dist < 300 || dist > 4000 {
			t.Fatalf("30-day distance implausible: %.0f km", dist)
		}
		// Second seed is a no-op.
		again, _ := s.SeedHistory(context.Background(), "user-1", vin, 30)
		if len(again) != 0 {
			t.Fatal("history must only be seeded once")
		}
	}
}

func TestLiveLoopDrivesChargesAndSleeps(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)}
	s := newSim(c)
	ctx := context.Background()
	vin := vins(t, s)[0]
	seen := map[string]bool{}
	var lastOdo float64
	for i := 0; i < 60*60/5; i++ { // one simulated hour at 5 s polls
		c.add(5 * time.Second)
		st, err := s.VehicleState(ctx, "user-1", vin)
		if err != nil {
			t.Fatal(err)
		}
		if st == models.StateAsleep {
			seen["asleep"] = true
			if _, err := s.VehicleData(ctx, "user-1", vin); !errors.Is(err, transport.ErrVehicleUnavailable) {
				t.Fatal("asleep car must not return data")
			}
			continue
		}
		snap, err := s.VehicleData(ctx, "user-1", vin)
		if err != nil {
			t.Fatal(err)
		}
		ls := snap.State
		if ls.IsDriving() {
			seen["driving"] = true
		}
		if ls.IsCharging() {
			seen["charging"] = true
		}
		if ls.OdometerKm < lastOdo {
			t.Fatal("odometer went backwards")
		}
		lastOdo = ls.OdometerKm
	}
	for _, k := range []string{"driving", "charging", "asleep"} {
		if !seen[k] {
			t.Fatalf("live loop never reached %q within an hour: %v", k, seen)
		}
	}
}

func TestCommands(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)}
	s := newSim(c)
	ctx := context.Background()
	vin := vins(t, s)[0]
	cmd := func(name string, body map[string]any) error {
		return s.Command(ctx, "user-1", vin, transport.Command{Name: name, Body: body})
	}
	if err := cmd("door_unlock", nil); err != nil {
		t.Fatal(err)
	}
	if err := cmd("set_temps", map[string]any{"driver_temp": 22.5, "passenger_temp": 22.5}); err != nil {
		t.Fatal(err)
	}
	if err := cmd("auto_conditioning_start", nil); err != nil {
		t.Fatal(err)
	}
	if err := cmd("actuate_trunk", map[string]any{"which_trunk": "front"}); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.VehicleData(ctx, "user-1", vin)
	if snap.State.Closures.Locked || !snap.State.Climate.On || snap.State.Climate.DriverSetC != 22.5 || !snap.State.Closures.FrunkOpen {
		t.Fatalf("commands not applied: %+v %+v", snap.State.Closures, snap.State.Climate)
	}
	var ce *transport.CommandError
	if err := cmd("charge_start", nil); !errors.As(err, &ce) {
		t.Fatalf("charge_start unplugged should be refused: %v", err)
	}
	if err := cmd("set_charging_amps", map[string]any{"charging_amps": 99}); !errors.As(err, &ce) {
		t.Fatal("invalid amps should be refused")
	}
	if err := cmd("bogus", nil); !errors.As(err, &ce) {
		t.Fatal("unknown command should be refused")
	}
}
