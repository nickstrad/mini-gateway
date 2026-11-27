package admission

import "math/bits"

type RuleState uint8

const (
	StateNormal RuleState = iota
	StateStanding
	StateDropping
	StateOverloaded
	NumRuleStates
)

var stateNames = [NumRuleStates]string{"normal", "standing", "dropping", "overloaded"}

func (s RuleState) String() string {
	if s < NumRuleStates {
		return stateNames[s]
	}
	return "?"
}

type QueueRule interface {
	Kind() RuleKind
	Pushed(now Time, wasEmpty bool)
	Emptied(now Time)
	SweepHead(now, oldest Time) bool
	AtPop(now, wait Time) bool
	Next(oldest Time) Time
	Overloaded(now Time, n int) bool
	State(now Time, n int) RuleState
}

func newRule(cfg Config, now Time) QueueRule {
	switch cfg.Rule {
	case RuleControlLaw:
		return &ControlLaw{Target: cfg.Target, Interval: cfg.Interval}
	case RuleFIFO:
		return fifoRule{}
	case RuleFolly:
		return &FollyRule{Target: cfg.Target, Interval: cfg.Interval, intervalEnd: now, reset: true}
	}
	return &TimeoutRule{Target: cfg.Target, Interval: cfg.Interval, firstBurstBug: cfg.Plant&PlantFirstBurst != 0}
}

type TimeoutRule struct {
	Target        Time
	Interval      Time
	LastEmpty     Time
	firstBurstBug bool
}

func (r *TimeoutRule) Kind() RuleKind { return RuleTimeout }

func (r *TimeoutRule) Pushed(now Time, wasEmpty bool) {
	if wasEmpty && !r.firstBurstBug {
		r.LastEmpty = now
	}
}

func (r *TimeoutRule) Emptied(now Time) { r.LastEmpty = now }

func (r *TimeoutRule) standing(now Time) bool { return now-r.LastEmpty >= r.Interval }

func (r *TimeoutRule) Allowed(now Time) Time {
	if r.standing(now) {
		return r.Target
	}
	return r.Interval
}

func (r *TimeoutRule) SweepHead(now, oldest Time) bool { return now-oldest >= r.Allowed(now) }

func (r *TimeoutRule) AtPop(Time, Time) bool { return false }

func (r *TimeoutRule) Next(oldest Time) Time { return max(r.LastEmpty+r.Interval, oldest+r.Target) }

func (r *TimeoutRule) Overloaded(now Time, n int) bool { return n > 0 && r.standing(now) }

func (r *TimeoutRule) State(now Time, n int) RuleState {
	if r.Overloaded(now, n) {
		return StateStanding
	}
	return StateNormal
}

type ControlLaw struct {
	Target     Time
	Interval   Time
	FirstAbove Time
	DropNext   Time
	Count      int64
	LastCount  int64
	Dropping   bool
	advance    bool
}

func (r *ControlLaw) Kind() RuleKind { return RuleControlLaw }

func (r *ControlLaw) Pushed(Time, bool) {}

func (r *ControlLaw) Emptied(Time) {
	r.FirstAbove = 0
	r.Dropping = false
	r.advance = false
}

func (r *ControlLaw) okToDrop(now, oldest Time) bool {
	if now-oldest < r.Target {
		r.FirstAbove = 0
		return false
	}
	if r.FirstAbove == 0 {
		r.FirstAbove = now + r.Interval
		return false
	}
	return now >= r.FirstAbove
}

func (r *ControlLaw) SweepHead(now, oldest Time) bool {
	ok := r.okToDrop(now, oldest)
	if r.advance {
		r.advance = false
		if !ok {
			r.Dropping = false
			return false
		}
		r.DropNext = Law(r.DropNext, r.Interval, r.Count)
	}
	if r.Dropping {
		if !ok {
			r.Dropping = false
			return false
		}
		if now >= r.DropNext {
			r.Count = min(r.Count+1, MaxDropCount)
			r.advance = true
			return true
		}
		return false
	}
	if !ok {
		return false
	}
	delta := r.Count - r.LastCount
	r.Count = 1
	if delta > 1 && now-r.DropNext < QuietIntervals*r.Interval {
		r.Count = delta
	}
	r.DropNext = Law(now, r.Interval, r.Count)
	r.LastCount = r.Count
	r.Dropping = true
	return true
}

func (r *ControlLaw) AtPop(Time, Time) bool { return false }

func (r *ControlLaw) Next(oldest Time) Time {
	switch {
	case r.Dropping:
		return r.DropNext
	case r.FirstAbove != 0:
		return r.FirstAbove
	}
	return oldest + r.Target
}

func (r *ControlLaw) Overloaded(Time, int) bool { return r.Dropping }

func (r *ControlLaw) State(Time, int) RuleState {
	if r.Dropping {
		return StateDropping
	}
	return StateNormal
}

func Law(t, interval Time, count int64) Time { return t + IntervalOverSqrt(interval, count) }

func IntervalOverSqrt(interval Time, count int64) Time {
	if count <= 1 {
		return interval
	}
	i := uint64(interval)
	c := uint64(count)
	ih, il := bits.Mul64(i, i)
	lo, hi := uint64(0), i
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if fitsUnder(mid, c, ih, il) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return Time(lo)
}

func fitsUnder(d, c, ih, il uint64) bool {
	sh, sl := bits.Mul64(d, d)
	ph, pl := bits.Mul64(sl, c)
	hh, carry := bits.Mul64(sh, c)
	ph, overflow := bits.Add64(ph, carry, 0)
	if hh != 0 || overflow != 0 {
		return false
	}
	if ph != ih {
		return ph < ih
	}
	return pl <= il
}

type fifoRule struct{}

func (fifoRule) Kind() RuleKind            { return RuleFIFO }
func (fifoRule) Pushed(Time, bool)         {}
func (fifoRule) Emptied(Time)              {}
func (fifoRule) SweepHead(Time, Time) bool { return false }
func (fifoRule) AtPop(Time, Time) bool     { return false }
func (fifoRule) Next(Time) Time            { return 0 }
func (fifoRule) Overloaded(Time, int) bool { return false }
func (fifoRule) State(Time, int) RuleState { return StateNormal }

type FollyRule struct {
	Target      Time
	Interval    Time
	MinDelay    Time
	overloaded  bool
	intervalEnd Time
	reset       bool
}

func (r *FollyRule) Kind() RuleKind            { return RuleFolly }
func (r *FollyRule) Pushed(Time, bool)         {}
func (r *FollyRule) Emptied(Time)              {}
func (r *FollyRule) SweepHead(Time, Time) bool { return false }
func (r *FollyRule) Next(Time) Time            { return 0 }

func (r *FollyRule) AtPop(now, delay Time) bool {
	if now > r.intervalEnd && !r.reset {
		r.reset = true
		r.intervalEnd = now + r.Interval
		r.overloaded = r.MinDelay > r.Target
	}
	if r.reset {
		r.reset = false
		r.MinDelay = delay
		return false
	}
	if delay < r.MinDelay {
		r.MinDelay = delay
	}
	return r.overloaded && delay > 2*r.Target
}

func (r *FollyRule) Overloaded(_ Time, n int) bool { return n > 0 && r.overloaded }

func (r *FollyRule) State(now Time, n int) RuleState {
	if r.Overloaded(now, n) {
		return StateOverloaded
	}
	return StateNormal
}

type ruleState struct {
	timeout TimeoutRule
	law     ControlLaw
	folly   FollyRule
}

func saveRule(r QueueRule) ruleState {
	var s ruleState
	switch v := r.(type) {
	case *TimeoutRule:
		s.timeout = *v
	case *ControlLaw:
		s.law = *v
	case *FollyRule:
		s.folly = *v
	}
	return s
}

func restoreRule(r QueueRule, s ruleState) {
	switch v := r.(type) {
	case *TimeoutRule:
		*v = s.timeout
	case *ControlLaw:
		*v = s.law
	case *FollyRule:
		*v = s.folly
	}
}
