package admission

import "math/bits"

// RuleState is the externally visible congestion state reported by a QueueRule.
type RuleState uint8

const (
	// StateNormal means the tenant queue is not currently overloaded.
	StateNormal RuleState = iota
	// StateStanding means TimeoutRule has observed a non-empty queue for at least Interval.
	StateStanding
	// StateDropping means ControlLaw has entered its RFC 8289 dropping phase.
	StateDropping
	// StateOverloaded means FollyRule's previous interval minimum delay exceeded Target.
	StateOverloaded
	// NumRuleStates is the number of defined RuleState values.
	NumRuleStates
)

var stateNames = [NumRuleStates]string{"normal", "standing", "dropping", "overloaded"}

// String returns the canonical lowercase name of s, or "?" if unknown.
func (s RuleState) String() string {
	if s < NumRuleStates {
		return stateNames[s]
	}
	return "?"
}

// QueueRule defines the shedding policy owned by one tenant queue.
// Implementations are driven purely by explicit timestamps and oldest-item
// enqueue times without touching clocks or floating point.
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
		// Match upstream folly's Codel constructor, which seeds intervalEnd at
		// creation time with reset true.
		return &FollyRule{Target: cfg.Target, Interval: cfg.Interval, intervalEnd: now, reset: true}
	}
	return &TimeoutRule{Target: cfg.Target, Interval: cfg.Interval, firstBurstBug: cfg.Plant&PlantFirstBurst != 0}
}

// TimeoutRule implements the "Fail at Scale" request-queue rule: it keeps a
// single LastEmpty timestamp and allows each queued request to wait Interval
// while no standing queue exists, tightening to Target once the queue has stayed
// non-empty for at least Interval.
type TimeoutRule struct {
	Target        Time
	Interval      Time
	LastEmpty     Time
	firstBurstBug bool
}

// Kind returns RuleTimeout.
func (r *TimeoutRule) Kind() RuleKind { return RuleTimeout }

// Pushed records now as LastEmpty when an arrival lands in an empty queue so
// the first burst after an idle period receives the full Interval, not Target.
func (r *TimeoutRule) Pushed(now Time, wasEmpty bool) {
	if wasEmpty && !r.firstBurstBug {
		r.LastEmpty = now
	}
}

// Emptied records the instant the tenant queue became empty.
func (r *TimeoutRule) Emptied(now Time) { r.LastEmpty = now }

func (r *TimeoutRule) standing(now Time) bool { return now-r.LastEmpty >= r.Interval }

// Allowed returns the wait bound applicable to every entry in the tenant queue
// at now: Target while a standing queue exists, otherwise Interval.
func (r *TimeoutRule) Allowed(now Time) Time {
	if r.standing(now) {
		return r.Target
	}
	return r.Interval
}

// SweepHead reports whether the oldest queued entry has reached or passed
// Allowed(now). Equality counts as expired.
func (r *TimeoutRule) SweepHead(now, oldest Time) bool { return now-oldest >= r.Allowed(now) }

// AtPop always returns false; TimeoutRule sheds only from the head sweep.
func (r *TimeoutRule) AtPop(Time, Time) bool { return false }

// Next returns the earliest instant at which the head entry (enqueued at oldest)
// expires. Before standing begins, the head can expire only when the queue enters
// standing at LastEmpty+Interval (or later at oldest+Target once standing).
func (r *TimeoutRule) Next(oldest Time) Time { return max(r.LastEmpty+r.Interval, oldest+r.Target) }

// Overloaded reports whether the queue is non-empty and in a standing queue.
func (r *TimeoutRule) Overloaded(now Time, n int) bool { return n > 0 && r.standing(now) }

// State returns StateStanding while overloaded and StateNormal otherwise.
func (r *TimeoutRule) State(now Time, n int) RuleState {
	if r.Overloaded(now, n) {
		return StateStanding
	}
	return StateNormal
}

// ControlLaw implements the RFC 8289 CoDel state machine driven at the queue
// head: once head sojourn stays at or above Target for Interval, it drops the
// oldest request and schedules subsequent drops every floor(Interval/sqrt(Count))
// using exact integer arithmetic.
type ControlLaw struct {
	Target     Time
	Interval   Time
	FirstAbove Time
	DropNext   Time
	Count      int64
	LastCount  int64
	Dropping   bool
	// advance defers updating DropNext until the next head is inspected after a
	// drop, mirroring RFC 8289's loop that checks the following packet's sojourn
	// before advancing drop_next.
	advance bool
}

// Kind returns RuleControlLaw.
func (r *ControlLaw) Kind() RuleKind { return RuleControlLaw }

// Pushed is a no-op; ControlLaw tracks sojourn from the head entry's enqueue time.
func (r *ControlLaw) Pushed(Time, bool) {}

// Emptied exits dropping and clears any pending above-target window.
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

// SweepHead evaluates RFC 8289's dropping state against the head entry and
// returns true when the head should be shed at now.
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
	// Re-entry within 16 intervals keeps the Linux CoDel delta (count - lastcount)
	// so repeated congestion resumes near the prior drop rate.
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

// AtPop always returns false; ControlLaw sheds only from the head sweep.
func (r *ControlLaw) AtPop(Time, Time) bool { return false }

// Next returns the next instant at which ControlLaw must inspect the head:
// DropNext while dropping, FirstAbove while timing a sojourn above Target,
// or oldest+Target to detect when the head reaches Target.
func (r *ControlLaw) Next(oldest Time) Time {
	switch {
	case r.Dropping:
		return r.DropNext
	case r.FirstAbove != 0:
		return r.FirstAbove
	}
	return oldest + r.Target
}

// Overloaded reports whether ControlLaw is currently in its dropping phase.
func (r *ControlLaw) Overloaded(Time, int) bool { return r.Dropping }

// State returns StateDropping while dropping and StateNormal otherwise.
func (r *ControlLaw) State(Time, int) RuleState {
	if r.Dropping {
		return StateDropping
	}
	return StateNormal
}

// Law returns t + floor(interval / sqrt(count)) using exact integer arithmetic.
func Law(t, interval Time, count int64) Time { return t + IntervalOverSqrt(interval, count) }

// IntervalOverSqrt computes floor(interval / sqrt(count)) without floating point
// by binary searching for the largest d satisfying d*d*count <= interval*interval.
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

// fitsUnder tests d*d*c <= (ih<<64 | il) in exact 128-bit arithmetic so large
// nanosecond intervals (up to MaxInterval) never overflow uint64.
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

// FollyRule is the simulator-only comparison arm reproducing facebook/folly's
// Codel detector: it tracks the minimum delay seen at dequeue per interval and
// sheds dequeued items whose wait exceeds 2*Target once a full interval's
// minimum exceeded Target.
type FollyRule struct {
	Target      Time
	Interval    Time
	MinDelay    Time
	overloaded  bool
	intervalEnd Time
	reset       bool
}

// Kind returns RuleFolly.
func (r *FollyRule) Kind() RuleKind { return RuleFolly }

// Pushed is a no-op; FollyRule observes delay only on pop.
func (r *FollyRule) Pushed(Time, bool) {}

// Emptied is a no-op; FollyRule tracks delay windows across pops.
func (r *FollyRule) Emptied(Time) {}

// SweepHead always returns false; FollyRule decides only at dequeue.
func (r *FollyRule) SweepHead(Time, Time) bool { return false }

// Next always returns 0 because FollyRule schedules no timers.
func (r *FollyRule) Next(Time) Time { return 0 }

// AtPop updates the per-interval minimum delay and reports whether the dequeued
// request should be shed (never shedding the first sample of an interval).
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

// Overloaded reports whether the queue is non-empty and the last closed interval
// finished with MinDelay above Target.
func (r *FollyRule) Overloaded(_ Time, n int) bool { return n > 0 && r.overloaded }

// State returns StateOverloaded while overloaded and StateNormal otherwise.
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
