package sim

import (
	"sort"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

type actionKind int

const (
	actWake actionKind = iota
	actClimate
	actDrive
	actPlug
	actUnplug
)

type action struct {
	at   time.Time
	kind actionKind
	to   point
	fast bool
}

// localDay returns the UTC instant for local wall-clock h:m on day.
func (c *car) localAt(day time.Time, h, m int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, time.UTC).Add(-c.tzOffset)
}

func (c *car) jitter(minutes int) time.Duration {
	return time.Duration(c.rng.Intn(2*minutes+1)-minutes) * time.Minute
}

// planDay builds a plausible day of driving for the car (local wall clock).
func (c *car) planDay(day time.Time, commuter bool) []action {
	var acts []action
	drive := func(at time.Time, to point) {
		acts = append(acts,
			action{at: at.Add(-12 * time.Minute), kind: actWake},
			action{at: at.Add(-10 * time.Minute), kind: actClimate},
			action{at: at, kind: actDrive, to: to})
	}
	wd := day.Weekday()
	weekend := wd == time.Saturday || wd == time.Sunday
	switch {
	case !weekend && commuter:
		drive(c.localAt(day, 7, 50).Add(c.jitter(25)), c.work)
		if c.rng.Float64() < 0.3 {
			lunch := c.localAt(day, 12, 15).Add(c.jitter(15))
			drive(lunch, c.workCharger)
			drive(lunch.Add(50*time.Minute), c.work)
		}
		drive(c.localAt(day, 17, 40).Add(c.jitter(30)), c.home)
		if c.rng.Float64() < 0.25 {
			e := c.localAt(day, 19, 30).Add(c.jitter(20))
			drive(e, c.store)
			drive(e.Add(35*time.Minute), c.home)
		}
	case !weekend:
		if c.rng.Float64() < 0.6 {
			e := c.localAt(day, 8, 10).Add(c.jitter(10))
			drive(e, c.store)
			drive(e.Add(25*time.Minute), c.home)
		}
		if c.rng.Float64() < 0.4 {
			e := c.localAt(day, 16, 0).Add(c.jitter(40))
			drive(e, c.park)
			drive(e.Add(80*time.Minute), c.home)
		}
	default:
		if c.rng.Float64() < 0.18 { // road trip with a Supercharger stop on the way back
			out := c.localAt(day, 9, 30).Add(c.jitter(30))
			drive(out, c.coast)
			back := out.Add(4 * time.Hour)
			drive(back, c.supercharger)
			acts = append(acts, action{at: back.Add(time.Minute), kind: actPlug, fast: true})
			drive(back.Add(45*time.Minute), c.home)
		} else {
			e := c.localAt(day, 10, 30).Add(c.jitter(45))
			drive(e, c.store)
			drive(e.Add(40*time.Minute), c.park)
			drive(e.Add(150*time.Minute), c.home)
		}
	}
	// Evening home charging when below ~65 %.
	acts = append(acts, action{at: c.localAt(day, 21, 30).Add(c.jitter(30)), kind: actPlug})
	return acts
}

// runHistory simulates [from, to) and returns online samples at realistic
// polling cadence, plus a sample whenever the car falls asleep.
func (c *car) runHistory(from, to time.Time, commuter bool) []models.LiveState {
	c.clock = from
	c.lastActivity = from
	var out []models.LiveState
	emit := func() { out = append(out, *c.snapshot()) }

	var queue []action
	for d := from.Add(c.tzOffset).Truncate(24 * time.Hour); d.Before(to.Add(c.tzOffset)); d = d.Add(24 * time.Hour) {
		for _, a := range c.planDay(d, commuter) {
			if !a.at.Before(from) && a.at.Before(to) {
				queue = append(queue, a)
			}
		}
	}
	sort.SliceStable(queue, func(i, j int) bool { return queue[i].at.Before(queue[j].at) })
	c.ph = phaseAsleep
	for c.clock.Before(to) {
		next := to
		if len(queue) > 0 && queue[0].at.Before(next) {
			next = queue[0].at
		}
		cadence := 5 * time.Minute
		switch {
		case c.ph == phaseDriving:
			cadence = 30 * time.Second
		case c.ph == phaseCharging && c.chargeActive:
			cadence = time.Minute
		case c.ph == phaseAsleep:
			cadence = next.Sub(c.clock)
		}
		step := c.clock.Add(cadence)
		if next.Before(step) && c.ph != phaseDriving {
			step = next
		}
		if !step.After(c.clock) {
			step = c.clock.Add(time.Second)
		}
		c.advance(step)
		if c.ph != phaseAsleep {
			emit()
		}
		// Fire due actions (drives wait for the car to arrive first).
		for len(queue) > 0 && !queue[0].at.After(c.clock) && c.ph != phaseDriving {
			a := queue[0]
			queue = queue[1:]
			c.apply(a)
			if c.ph != phaseAsleep {
				emit()
			}
		}
		// Fall asleep after 20 idle minutes (not while charging).
		idle := c.ph == phaseParked || (c.ph == phaseCharging && !c.chargeActive)
		if idle && !c.climateOn && c.clock.Sub(c.lastActivity) >= 20*time.Minute {
			c.sleep()
			emit()
		}
	}
	return out
}

func (c *car) apply(a action) {
	switch a.kind {
	case actWake:
		c.wake()
	case actClimate:
		c.wake()
		c.climateOn = true
		c.lastActivity = c.clock
	case actDrive:
		c.wake()
		c.startDrive(a.to)
	case actPlug:
		c.wake()
		if a.fast || (c.soc < 65 && c.ph != phaseCharging) {
			c.plugIn(a.fast)
		}
	case actUnplug:
		c.wake()
		if c.ph == phaseCharging {
			c.unplug()
		}
	}
}
