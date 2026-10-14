package sim

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/geo"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Options configure the simulator.
type Options struct {
	HomeLat  float64
	HomeLon  float64
	Vehicles int
	Now      func() time.Time
}

// Transport is the simulator transport. Each user gets their own deterministic
// fleet (seeded from the user id), so demos are repeatable.
type Transport struct {
	opts Options

	mu     sync.Mutex
	fleets map[string][]*liveCar
}

// liveCar wraps a car with the compressed live-demo schedule.
type liveCar struct {
	*car
	commuter   bool
	stage      int
	stageUntil time.Time
	cycle      int
	destIdx    int
	seeded     bool
	cafe, gym  point
}

// New returns a simulator.
func New(o Options) *Transport {
	if o.Vehicles <= 0 {
		o.Vehicles = 2
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Transport{opts: o, fleets: map[string][]*liveCar{}}
}

var _ transport.Transport = (*Transport)(nil)
var _ transport.HistorySeeder = (*Transport)(nil)

func (t *Transport) Name() string { return "sim" }

// Policy polls quickly: the simulator is free, and demos should feel live.
func (t *Transport) Policy() transport.PollPolicy {
	return transport.PollPolicy{
		Driving: 5 * time.Second, Charging: 10 * time.Second, OnlineActive: 10 * time.Second,
		OnlineIdle: 20 * time.Second, AsleepCheck: 20 * time.Second, Watching: 5 * time.Second,
		IdleBeforeSuspend: time.Hour, Suspend: time.Minute,
		AfterCommand: []time.Duration{time.Second, 3 * time.Second},
	}
}

func seedFor(parts ...string) int64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return int64(h.Sum64() & math.MaxInt64)
}

func vinFor(prefix, userID string, i int) string {
	const alphabet = "ABCDEFGHJKLMNPRSTUVWXYZ0123456789" // VINs exclude I, O, Q
	rng := rand.New(rand.NewSource(seedFor("vin", userID, fmt.Sprint(i))))
	var b strings.Builder
	b.WriteString(prefix)
	for b.Len() < 17 {
		b.WriteByte(alphabet[rng.Intn(len(alphabet))])
	}
	return b.String()
}

func (t *Transport) fleet(userID string) []*liveCar {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.fleets[userID]; ok {
		return f
	}
	now := t.opts.Now().UTC()
	home := point{t.opts.HomeLat, t.opts.HomeLon}
	names := []string{"Midnight", "Pearl", "Comet", "Nova", "Vega"}
	colors := []string{"MidnightSilver", "PearlWhite", "UltraRed", "DeepBlue", "SolidBlack"}
	prefixes := []string{"5YJ3E1EA", "7SAYGDEE", "5YJSA1E6", "5YJ3E1EB", "7SAYGDEF"}
	var f []*liveCar
	for i := 0; i < t.opts.Vehicles; i++ {
		vin := vinFor(prefixes[i%len(prefixes)], userID, i)
		rng := rand.New(rand.NewSource(seedFor("car", userID, vin)))
		sp := specs[i%2]
		if i == 2 {
			sp = specs[2]
		}
		at := func(bearing, km float64) point {
			lat, lon := geo.Offset(home.lat, home.lon, bearing+rng.Float64()*20-10, km*1000*(0.9+0.2*rng.Float64()))
			return point{lat, lon}
		}
		c := &car{
			rng: rng, spec: sp, vin: vin, name: names[i%len(names)], color: colors[i%len(colors)],
			home: home, work: at(135, 17), store: at(40, 5), park: at(300, 11), coast: at(250, 75),
			tzOffset:    time.Duration(math.Round(home.lon/15)) * time.Hour,
			pos:         home,
			odometerKm:  8000 + rng.Float64()*30000,
			soc:         55 + rng.Float64()*25,
			chargeLimit: 80, locked: true, setTempC: 21, version: "2025.38.6",
			clock: now, lastActivity: now,
		}
		c.supercharger = func() point {
			lat, lon := geo.Offset(home.lat, home.lon, geo.BearingDeg(home.lat, home.lon, c.coast.lat, c.coast.lon), 38000)
			return point{lat, lon}
		}()
		c.workCharger = at(150, 18.5)
		c.insideC = c.outsideTemp(now)
		if i == 1 {
			c.updateVer = "2025.44.2"
		}
		lc := &liveCar{car: c, commuter: i%2 == 0, cafe: at(70, 4), gym: at(200, 7)}
		// Stagger cars so they are not in lockstep.
		lc.stageUntil = now.Add(time.Duration(30+90*i) * time.Second)
		f = append(f, lc)
	}
	t.fleets[userID] = f
	return f
}

func (t *Transport) find(userID, vin string) (*liveCar, error) {
	for _, c := range t.fleet(userID) {
		if c.vin == vin {
			return c, nil
		}
	}
	return nil, transport.ErrNotFound
}

// Live demo schedule (compressed so a full cycle takes ~30 minutes):
//
//	0 idle at home (precondition in the last 45 s) → 1 drive to cafe/gym → 2 parked
//	→ 3 drive to Supercharger if low, else home → 4 DC charge → 5 drive home
//	→ 6 AC charge at home → 7 unplug and sleep → back to 0.
const (
	stIdleHome = iota
	stDriveOut
	stParkedAway
	stDriveBack
	stSupercharge
	stDriveHomeFromSC
	stHomeCharge
	stAsleep
)

func (lc *liveCar) tick(now time.Time) {
	for lc.clock.Before(now) {
		next := lc.clock.Add(5 * time.Second)
		if lc.ph == phaseAsleep || (lc.ph == phaseParked && !lc.climateOn) {
			next = lc.clock.Add(30 * time.Second)
		}
		if next.After(now) {
			next = now
		}
		lc.advance(next)
		lc.plan()
	}
}

func (lc *liveCar) plan() {
	now := lc.clock
	switch lc.stage {
	case stIdleHome:
		if lc.ph == phaseAsleep && now.After(lc.stageUntil.Add(-90*time.Second)) {
			lc.wake()
		}
		if !lc.climateOn && now.After(lc.stageUntil.Add(-45*time.Second)) {
			lc.climateOn = true
			lc.lastActivity = now
		}
		if !now.Before(lc.stageUntil) {
			dest := lc.cafe
			if lc.destIdx%2 == 1 {
				dest = lc.gym
			}
			lc.destIdx++
			lc.startDrive(dest)
			lc.stage = stDriveOut
		}
	case stDriveOut:
		if lc.ph == phaseParked {
			lc.sentry = true
			lc.stage, lc.stageUntil = stParkedAway, now.Add(3*time.Minute)
		}
	case stParkedAway:
		if !now.Before(lc.stageUntil) {
			lc.sentry = false
			if lc.soc < 50 {
				lat, lon := geo.Offset(lc.pos.lat, lc.pos.lon, 90, 3000)
				lc.startDrive(point{lat, lon})
				lc.stage = stSupercharge
			} else {
				lc.startDrive(lc.home)
				lc.stage = stDriveBack
			}
		}
	case stSupercharge:
		if lc.ph == phaseParked {
			lc.plugIn(true)
			lc.stageUntil = now.Add(7 * time.Minute)
		}
		if lc.ph == phaseCharging && (!now.Before(lc.stageUntil) || lc.chargeDone) {
			lc.startDrive(lc.home)
			lc.stage = stDriveHomeFromSC
		}
	case stDriveBack, stDriveHomeFromSC:
		if lc.ph == phaseParked {
			lc.plugIn(false)
			lc.stage, lc.stageUntil = stHomeCharge, now.Add(5*time.Minute)
		}
	case stHomeCharge:
		if !now.Before(lc.stageUntil) {
			if lc.ph == phaseCharging {
				lc.unplug()
			}
			lc.sleep()
			lc.stage, lc.stageUntil = stAsleep, now.Add(4*time.Minute)
		}
	case stAsleep:
		if lc.ph != phaseAsleep { // woken by the user: give them a few minutes
			lc.stage, lc.stageUntil = stIdleHome, now.Add(4*time.Minute)
			return
		}
		if !now.Before(lc.stageUntil) {
			lc.cycle++
			lc.stage, lc.stageUntil = stIdleHome, now.Add(2*time.Minute)
		}
	}
}

// withCar advances the car to now and runs fn while holding the simulator lock,
// so every read and mutation of car state is serialized.
func (t *Transport) withCar(userID, vin string, fn func(lc *liveCar) error) error {
	lc, err := t.find(userID, vin)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	lc.tick(t.opts.Now().UTC())
	return fn(lc)
}

// ListVehicles returns the user's simulated fleet.
func (t *Transport) ListVehicles(_ context.Context, userID string) ([]transport.Vehicle, error) {
	var out []transport.Vehicle
	for _, c := range t.fleet(userID) {
		err := t.withCar(userID, c.vin, func(lc *liveCar) error {
			out = append(out, transport.Vehicle{TeslaID: fmt.Sprint(seedFor(lc.vin) % 1e9), VIN: lc.vin, DisplayName: lc.name,
				State: lc.state(), Model: lc.spec.model, Trim: lc.spec.trim, Color: lc.color})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// VehicleState never wakes the car.
func (t *Transport) VehicleState(_ context.Context, userID, vin string) (string, error) {
	var st string
	err := t.withCar(userID, vin, func(lc *liveCar) error { st = lc.state(); return nil })
	return st, err
}

// VehicleData returns the live snapshot, or ErrVehicleUnavailable when asleep.
func (t *Transport) VehicleData(_ context.Context, userID, vin string) (*transport.Snapshot, error) {
	var snap *transport.Snapshot
	err := t.withCar(userID, vin, func(lc *liveCar) error {
		if lc.ph == phaseAsleep {
			return transport.ErrVehicleUnavailable
		}
		snap = &transport.Snapshot{State: lc.snapshot(), DisplayName: lc.name, Model: lc.spec.model, Trim: lc.spec.trim, Color: lc.color}
		return nil
	})
	return snap, err
}

// Wake wakes the car (it comes online immediately in the simulator).
func (t *Transport) Wake(_ context.Context, userID, vin string) error {
	return t.withCar(userID, vin, func(lc *liveCar) error { lc.wake(); return nil })
}

// Command applies a Tesla command name/body to the simulated car.
func (t *Transport) Command(_ context.Context, userID, vin string, cmd transport.Command) error {
	return t.withCar(userID, vin, func(lc *liveCar) error { return applyCommand(lc, cmd) })
}

func applyCommand(lc *liveCar, cmd transport.Command) error {
	if lc.ph == phaseAsleep {
		return transport.ErrVehicleUnavailable
	}
	num := func(k string) (float64, bool) {
		v, ok := cmd.Body[k].(float64)
		if !ok {
			if i, ok2 := cmd.Body[k].(int); ok2 {
				return float64(i), true
			}
		}
		return v, ok
	}
	refuse := func(reason string) error { return &transport.CommandError{Reason: reason} }
	lc.lastActivity = lc.clock
	switch cmd.Name {
	case "auto_conditioning_start":
		lc.climateOn = true
	case "auto_conditioning_stop":
		lc.climateOn = false
	case "set_temps":
		v, ok := num("driver_temp")
		if !ok || v < 15 || v > 28 {
			return refuse("invalid temperature")
		}
		lc.setTempC = v
	case "door_lock":
		lc.locked = true
	case "door_unlock":
		lc.locked = false
	case "actuate_trunk":
		if lc.ph == phaseDriving {
			return refuse("in_motion")
		}
		switch cmd.Body["which_trunk"] {
		case "front":
			lc.frunkOpen = true
		case "rear":
			lc.trunkOpen = !lc.trunkOpen
		default:
			return refuse("invalid which_trunk")
		}
	case "charge_port_door_open":
		lc.portOpen = true
	case "charge_port_door_close":
		if lc.ph == phaseCharging {
			return refuse("cable connected")
		}
		lc.portOpen = false
	case "charge_start":
		if lc.ph != phaseCharging {
			return refuse("not_plugged_in")
		}
		if lc.soc >= float64(lc.chargeLimit) {
			return refuse("complete")
		}
		if lc.chargeActive {
			return refuse("is_charging")
		}
		lc.chargeActive, lc.chargeDone = true, false
	case "charge_stop":
		if lc.ph != phaseCharging || !lc.chargeActive {
			return refuse("not_charging")
		}
		lc.chargeActive = false
	case "set_charging_amps":
		v, ok := num("charging_amps")
		if !ok || v < 1 || v > 48 {
			return refuse("invalid amps")
		}
		lc.chargeAmps = int(v)
	case "set_charge_limit":
		v, ok := num("percent")
		if !ok || v < 50 || v > 100 {
			return refuse("invalid percent")
		}
		lc.chargeLimit = int(v)
		if lc.ph == phaseCharging && lc.chargeDone && lc.soc < v {
			lc.chargeActive, lc.chargeDone = true, false
		}
	case "honk_horn", "flash_lights":
	case "set_sentry_mode":
		on, _ := cmd.Body["on"].(bool)
		lc.sentry = on
	case "window_control":
		switch cmd.Body["command"] {
		case "vent":
			lc.windowsOpen = true
		case "close":
			lc.windowsOpen = false
		default:
			return refuse("invalid window command")
		}
	default:
		return refuse("unknown command " + cmd.Name)
	}
	return nil
}

// SeedHistory synthesizes `days` of plausible history ending now, then moves
// the live car to the end of that history so odometer and battery are continuous.
func (t *Transport) SeedHistory(_ context.Context, userID, vin string, days int) ([]models.LiveState, error) {
	lc, err := t.find(userID, vin)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if lc.seeded || days <= 0 {
		return nil, nil
	}
	lc.seeded = true
	now := t.opts.Now().UTC()
	// A separate car with the same geography replays history in virtual time.
	h := *lc.car
	h.rng = rand.New(rand.NewSource(seedFor("history", userID, vin)))
	h.pos = h.home
	h.soc = 80
	h.odometerKm = lc.odometerKm - float64(days)*35
	h.ph, h.rt, h.climateOn, h.locked = phaseAsleep, nil, false, true
	h.portOpen, h.chargeActive, h.chargeDone, h.energyAdded = false, false, false, 0
	h.insideC = h.outsideTemp(now)
	start := now.Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Hour)
	end := now.Add(-10 * time.Minute)
	samples := h.runHistory(start, end, lc.commuter)

	// Continue live from where history ended (home, same odometer/battery).
	lc.odometerKm, lc.soc, lc.pos = h.odometerKm, h.soc, h.home
	if h.ph == phaseDriving {
		lc.pos = h.pos
	}
	lc.clock = now
	lc.ph, lc.rt, lc.portOpen, lc.chargeActive, lc.chargeDone = phaseAsleep, nil, false, false, false
	lc.stage, lc.stageUntil = stAsleep, now.Add(90*time.Second)
	for i := range samples {
		samples[i].VehicleID = ""
	}
	return samples, nil
}
