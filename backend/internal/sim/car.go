// Package sim is a physically plausible Tesla simulator implementing
// transport.Transport (ADR-010). It lets the backend, analytics and the iOS app
// run end-to-end without Tesla access: cars drive real routes, consume energy by
// speed and temperature, charge on AC/DC curves, fall asleep, and obey commands.
package sim

import (
	"math"
	"math/rand"
	"time"

	"github.com/altered-intelligence/milesight/internal/geo"
	"github.com/altered-intelligence/milesight/internal/models"
)

// spec describes a vehicle variant.
type spec struct {
	carType     string
	model       string
	trim        string
	capacityKWh float64
	fullRangeKm float64 // rated range at 100 %
	baseWhKm    float64 // consumption at ~50 km/h, mild weather
}

var specs = []spec{
	{carType: "model3", model: "Model 3", trim: "Long Range AWD", capacityKWh: 75, fullRangeKm: 534, baseWhKm: 98},
	{carType: "modely", model: "Model Y", trim: "Long Range AWD", capacityKWh: 75, fullRangeKm: 497, baseWhKm: 110},
	{carType: "models", model: "Model S", trim: "Plaid", capacityKWh: 95, fullRangeKm: 560, baseWhKm: 118},
}

type phase int

const (
	phaseParked phase = iota
	phaseDriving
	phaseCharging // plugged in (charging or complete)
	phaseAsleep
)

type point struct{ lat, lon float64 }

// route is a polyline with cumulative distances.
type route struct {
	pts    []point
	cum    []float64 // km from start to pts[i]
	length float64
	cruise float64   // km/h
	stops  []float64 // km marks of traffic stops
}

func newRoute(rng *rand.Rand, a, b point) *route {
	d := geo.DistanceM(a.lat, a.lon, b.lat, b.lon) / 1000
	bearing := geo.BearingDeg(a.lat, a.lon, b.lat, b.lon)
	n := 16
	bend := (rng.Float64()*2 - 1) * 0.12 * d * 1000 // meters of lateral bow
	r := &route{}
	for i := 0; i <= n; i++ {
		s := float64(i) / float64(n)
		lat, lon := geo.Offset(a.lat, a.lon, bearing, s*d*1000)
		if i > 0 && i < n {
			wiggle := math.Sin(s*math.Pi)*bend + (rng.Float64()*2-1)*0.01*d*1000
			lat, lon = geo.Offset(lat, lon, bearing+90, wiggle)
		}
		r.pts = append(r.pts, point{lat, lon})
	}
	r.cum = make([]float64, len(r.pts))
	for i := 1; i < len(r.pts); i++ {
		p, q := r.pts[i-1], r.pts[i]
		r.cum[i] = r.cum[i-1] + geo.DistanceM(p.lat, p.lon, q.lat, q.lon)/1000
	}
	r.length = r.cum[len(r.cum)-1]
	switch {
	case r.length < 8:
		r.cruise = 48
	case r.length < 30:
		r.cruise = 82
	default:
		r.cruise = 108
	}
	if r.length < 30 { // city driving: a stop every ~1.5-3 km
		for k := 1.5 + rng.Float64()*1.5; k < r.length-0.5; k += 1.5 + rng.Float64()*1.5 {
			r.stops = append(r.stops, k)
		}
	}
	return r
}

// at returns position and heading at distance km along the route.
func (r *route) at(km float64) (point, float64) {
	if km <= 0 {
		return r.pts[0], geo.BearingDeg(r.pts[0].lat, r.pts[0].lon, r.pts[1].lat, r.pts[1].lon)
	}
	for i := 1; i < len(r.pts); i++ {
		if km <= r.cum[i] || i == len(r.pts)-1 {
			p, q := r.pts[i-1], r.pts[i]
			seg := r.cum[i] - r.cum[i-1]
			f := 1.0
			if seg > 0 {
				f = math.Min(1, (km-r.cum[i-1])/seg)
			}
			return point{p.lat + (q.lat-p.lat)*f, p.lon + (q.lon-p.lon)*f}, geo.BearingDeg(p.lat, p.lon, q.lat, q.lon)
		}
	}
	last := r.pts[len(r.pts)-1]
	return last, 0
}

// car is one simulated vehicle.
type car struct {
	rng  *rand.Rand
	spec spec

	vin, name, color                                          string
	home, work, store, park, coast, supercharger, workCharger point
	tzOffset                                                  time.Duration

	// dynamic state
	ph          phase
	pos         point
	heading     float64
	odometerKm  float64
	soc         float64 // %
	chargeLimit int
	locked      bool
	sentry      bool
	climateOn   bool
	setTempC    float64
	insideC     float64
	frunkOpen   bool
	trunkOpen   bool
	windowsOpen bool
	portOpen    bool
	userPresent bool
	version     string
	updateVer   string

	// driving
	rt        *route
	progress  float64 // km
	speed     float64 // km/h
	powerKW   float64
	stopUntil time.Time
	nextStop  int
	cruiseJit float64

	// charging
	fast         bool
	chargeAmps   int
	chargeActive bool // false when stopped by user or complete
	chargeDone   bool
	energyAdded  float64
	chargerKW    float64

	lastActivity time.Time
	clock        time.Time
}

func (c *car) ratedKm() float64 { return c.soc / 100 * c.spec.fullRangeKm }

func (c *car) outsideTemp(t time.Time) float64 {
	local := t.Add(c.tzOffset)
	h := float64(local.Hour()) + float64(local.Minute())/60
	season := 4 * math.Cos(float64(local.YearDay()-200)/365*2*math.Pi) // warmer mid-year
	return 14 + season + 6*math.Sin((h-9)/24*2*math.Pi)
}

// advance integrates physics from c.clock to t in small steps.
func (c *car) advance(t time.Time) {
	for c.clock.Before(t) {
		dt := t.Sub(c.clock)
		maxStep := 5 * time.Second
		if c.ph == phaseAsleep || (c.ph == phaseParked && !c.climateOn) {
			maxStep = 5 * time.Minute
		}
		if dt > maxStep {
			dt = maxStep
		}
		c.step(dt)
		c.clock = c.clock.Add(dt)
	}
}

func (c *car) step(dt time.Duration) {
	h := dt.Hours()
	out := c.outsideTemp(c.clock)
	// Cabin temperature relaxes toward the setpoint (climate) or outside (+ greenhouse).
	target, tau := out+3, 1.5
	if c.climateOn {
		target, tau = c.setTempC, 0.12
	}
	c.insideC += (target - c.insideC) * (1 - math.Exp(-h/tau))

	hvacKW := 0.0
	if c.climateOn {
		hvacKW = 0.6 + 0.12*math.Abs(c.setTempC-out)
	}
	switch c.ph {
	case phaseDriving:
		c.drive(dt, out, hvacKW)
	case phaseCharging:
		c.charge(dt, hvacKW)
	case phaseParked:
		standby := 0.25 + hvacKW
		if c.sentry {
			standby += 0.25
		}
		c.useEnergy(standby * h)
		c.powerKW = 0
	case phaseAsleep:
		c.useEnergy(0.04 * h) // ~1 kWh/day asleep is realistic-ish; this is gentler
	}
}

func (c *car) useEnergy(kwh float64) {
	c.soc = math.Max(0, c.soc-kwh/c.spec.capacityKWh*100)
}

func (c *car) drive(dt time.Duration, outC, hvacKW float64) {
	s := dt.Seconds()
	r := c.rt
	remaining := r.length - c.progress
	// Cruise speed drifts smoothly (a random walk), like a real driver.
	c.cruiseJit = math.Max(-0.08, math.Min(0.08, c.cruiseJit+(c.rng.Float64()-0.5)*0.01))
	target := r.cruise * (1 + c.cruiseJit)
	// Traffic stops.
	if c.clock.Before(c.stopUntil) {
		target = 0
	} else if c.nextStop < len(r.stops) {
		toStop := r.stops[c.nextStop] - c.progress
		if toStop <= 0.02 {
			c.stopUntil = c.clock.Add(time.Duration(15+c.rng.Intn(40)) * time.Second)
			c.nextStop++
			target = 0
		} else if toStop < 0.15 {
			target = math.Min(target, toStop*300)
		}
	}
	if remaining < 0.25 { // arriving
		target = math.Min(target, math.Max(8, remaining*200))
	}
	prev := c.speed
	accel := 9.0 * s // km/h per second
	if target > c.speed {
		c.speed = math.Min(target, c.speed+accel)
	} else {
		c.speed = math.Max(target, c.speed-accel*1.4)
	}
	distKm := (prev + c.speed) / 2 / 3600 * s
	c.progress = math.Min(r.length, c.progress+distKm)
	c.odometerKm += distKm
	v := c.speed
	tempPenalty := 1 + math.Max(0, 15-outC)*0.012
	whKm := c.spec.baseWhKm * (0.82 + 0.0022*v + 0.000045*v*v) * tempPenalty
	drivePower := whKm * v / 1000
	// Kinetic energy: accelerating costs it (90 % drivetrain efficiency); braking
	// recovers ~60 % of it through regen.
	const massKg = 2000
	v1, v2 := prev/3.6, c.speed/3.6
	dKE := 0.5 * massKg * (v2*v2 - v1*v1) / s / 1000 // kW
	if dKE > 0 {
		drivePower += dKE / 0.9
	} else {
		drivePower = math.Max(-60, drivePower+dKE*0.6)
	}
	c.powerKW = geo.Round(drivePower+hvacKW, 1)
	c.useEnergy((drivePower + hvacKW) * dt.Hours())
	pos, heading := r.at(c.progress)
	c.pos, c.heading = pos, heading
	if c.progress >= r.length-0.001 && c.speed < 10 {
		c.arrive()
	}
}

func (c *car) arrive() {
	c.ph = phaseParked
	c.speed, c.powerKW = 0, 0
	c.pos = c.rt.pts[len(c.rt.pts)-1]
	c.rt = nil
	c.locked = true
	c.userPresent = false
	c.climateOn = false
	c.lastActivity = c.clock
}

func (c *car) startDrive(to point) {
	if c.ph == phaseCharging {
		c.unplug()
	}
	c.rt = newRoute(c.rng, c.pos, to)
	c.progress, c.speed, c.nextStop = 0, 0, 0
	c.stopUntil = time.Time{}
	c.ph = phaseDriving
	c.locked = false
	c.userPresent = true
	c.frunkOpen, c.trunkOpen, c.windowsOpen = false, false, false
	c.lastActivity = c.clock
}

func (c *car) plugIn(fast bool) {
	c.ph = phaseCharging
	c.fast = fast
	c.portOpen = true
	c.chargeActive = c.soc < float64(c.chargeLimit)
	c.chargeDone = !c.chargeActive
	c.energyAdded = 0
	if fast {
		c.chargeAmps = 0
	} else if c.chargeAmps == 0 {
		c.chargeAmps = 48
	}
	c.lastActivity = c.clock
}

func (c *car) unplug() {
	c.ph = phaseParked
	c.chargeActive, c.chargeDone = false, false
	c.chargerKW = 0
	c.energyAdded = 0
	c.portOpen = false
	c.lastActivity = c.clock
}

// dcCurve approximates a Supercharger V3 taper by state of charge.
func dcCurve(soc float64) float64 {
	switch {
	case soc < 15:
		return 250
	case soc < 35:
		return 250 - (soc-15)*4.5
	case soc < 60:
		return 160 - (soc-35)*3.2
	case soc < 80:
		return 80 - (soc-60)*1.75
	default:
		return math.Max(12, 45-(soc-80)*1.8)
	}
}

// chargePower is the instantaneous charger power for the current plug.
func (c *car) chargePower() float64 {
	if c.fast {
		return dcCurve(c.soc) * (0.95 + 0.05*c.rng.Float64())
	}
	return float64(c.chargeAmps) * 240 / 1000
}

func (c *car) charge(dt time.Duration, hvacKW float64) {
	h := dt.Hours()
	if !c.chargeActive {
		c.chargerKW = 0
		c.useEnergy((0.25 + hvacKW) * h)
		return
	}
	c.chargerKW = c.chargePower()
	net := c.chargerKW - hvacKW
	added := c.chargerKW * h
	c.energyAdded += added
	c.soc = math.Min(100, c.soc+net*h/c.spec.capacityKWh*100)
	if c.soc >= float64(c.chargeLimit) {
		c.soc = float64(c.chargeLimit)
		c.chargeActive, c.chargeDone = false, true
		c.chargerKW = 0
		c.lastActivity = c.clock
	}
}

func (c *car) sleep() {
	if c.ph == phaseDriving {
		return
	}
	// Plugged-in cars sleep too once charging has finished (portOpen remembers the plug).
	c.ph = phaseAsleep
	c.climateOn = false
	c.speed, c.powerKW, c.chargerKW = 0, 0, 0
}

func (c *car) wake() {
	if c.ph == phaseAsleep {
		if c.portOpen {
			c.ph = phaseCharging
		} else {
			c.ph = phaseParked
		}
	}
	c.lastActivity = c.clock
}

func (c *car) state() string {
	if c.ph == phaseAsleep {
		return models.StateAsleep
	}
	return models.StateOnline
}

// snapshot renders the current state in Milesight's model.
func (c *car) snapshot() *models.LiveState {
	now := c.clock.UTC()
	inside, outside := geo.Round(c.insideC, 1), geo.Round(c.outsideTemp(now), 1)
	rated := geo.Round(c.ratedKm(), 2)
	st := &models.LiveState{
		State: c.state(),
		Battery: models.Battery{
			Percent: int(math.Round(c.soc)), UsablePercent: int(math.Max(0, math.Round(c.soc-0.6))),
			RangeKm: rated, IdealRangeKm: geo.Round(rated*1.04, 2), EstRangeKm: geo.Round(rated*0.9, 2),
			ChargeLimitPct: c.chargeLimit, ChargeState: models.ChargeDisconnected, ChargePortOpen: c.portOpen,
		},
		Climate: &models.Climate{On: c.climateOn, InsideTempC: &inside, OutsideTempC: &outside,
			DriverSetC: c.setTempC, PassengerSetC: c.setTempC, Preconditioning: c.climateOn && c.ph != phaseDriving,
			FanStatus: map[bool]int{true: 4, false: 0}[c.climateOn]},
		Drive:    &models.Drive{ShiftState: "P", SpeedKph: 0, PowerKW: c.powerKW},
		Location: &models.Location{Lat: c.pos.lat, Lon: c.pos.lon, HeadingDeg: int(c.heading)},
		Closures: &models.Closures{Locked: c.locked, FrunkOpen: c.frunkOpen, TrunkOpen: c.trunkOpen,
			WindowsOpen: c.windowsOpen, SentryMode: c.sentry, UserPresent: c.userPresent},
		Software:   &models.Software{Version: c.version},
		OdometerKm: geo.Round(c.odometerKm, 2),
		UpdatedAt:  now,
	}
	if c.updateVer != "" {
		st.Software.UpdateAvailable, st.Software.UpdateVersion, st.Software.UpdateStatus = true, c.updateVer, "available"
	}
	if c.ph == phaseDriving {
		st.Drive.ShiftState = "D"
		st.Drive.SpeedKph = geo.Round(c.speed, 1)
	}
	if c.ph == phaseCharging || (c.ph == phaseAsleep && c.portOpen) {
		st.Battery.PluggedIn = true
		ch := &models.Charging{EnergyAddedKWh: geo.Round(c.energyAdded, 2), FastCharger: c.fast}
		switch {
		case c.chargeActive:
			if c.chargerKW == 0 { // just plugged in / started: no step integrated yet
				c.chargerKW = c.chargePower()
			}
			st.Battery.ChargeState = models.ChargeCharging
			ch.PowerKW = math.Round(c.chargerKW)
			if c.fast {
				ch.Voltage, ch.Amps = 380, int(c.chargerKW*1000/380)
				ch.FastChargerType = "Tesla"
			} else {
				ch.Voltage, ch.Amps, ch.AmpsRequest, ch.AmpsMax, ch.Phases = 240, c.chargeAmps, c.chargeAmps, 48, 1
			}
			ratePerH := c.chargerKW / c.spec.capacityKWh * c.spec.fullRangeKm
			ch.RateKph = geo.Round(ratePerH, 1)
			if c.chargerKW > 0 {
				ch.MinutesToLimit = int(math.Max(0, (float64(c.chargeLimit)-c.soc)/100*c.spec.capacityKWh/c.chargerKW*60))
			}
		case c.chargeDone:
			st.Battery.ChargeState = models.ChargeComplete
		default:
			st.Battery.ChargeState = models.ChargeStopped
		}
		ch.RangeAddedKm = geo.Round(c.energyAdded/c.spec.capacityKWh*c.spec.fullRangeKm, 1)
		st.Charging = ch
	}
	return st
}
