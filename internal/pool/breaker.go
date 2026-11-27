package pool

// BreakerState is the state of the pool's single database-down circuit breaker.
type BreakerState uint8

const (
	// BreakerClosed allows normal checkouts and dials.
	BreakerClosed BreakerState = iota
	// BreakerOpen refuses work with no dial until open_for elapses.
	BreakerOpen
	// BreakerHalfOpen admits requests to wait behind a single trial connection.
	BreakerHalfOpen
	// NumBreakerStates is the number of defined BreakerState values.
	NumBreakerStates
)

var breakerNames = [NumBreakerStates]string{"closed", "open", "half-open"}

// String returns the canonical lowercase name of s, or "?" if unknown.
func (s BreakerState) String() string {
	if s < NumBreakerStates {
		return breakerNames[s]
	}
	return "?"
}

// Transition identifies one state change of the circuit breaker for metrics.
type Transition uint8

const (
	// TransClosedOpen records consecutive connection failures opening the breaker.
	TransClosedOpen Transition = iota
	// TransOpenHalfOpen records open_for elapsing into half-open.
	TransOpenHalfOpen
	// TransHalfOpenClosed records a half-open trial succeeding and closing the breaker.
	TransHalfOpenClosed
	// TransHalfOpenOpen records a half-open trial failing and re-opening the breaker.
	TransHalfOpenOpen
	// NumTransitions is the number of defined Transition values.
	NumTransitions
)

var transitionNames = [NumTransitions]string{"closed-open", "open-half-open", "half-open-closed", "half-open-open"}

// String returns the canonical hyphenated name of t, or "?" if unknown.
func (t Transition) String() string {
	if t < NumTransitions {
		return transitionNames[t]
	}
	return "?"
}

type breaker struct {
	state     BreakerState
	threshold int
	openFor   Instant
	failures  int
	openUntil Instant
	trial     ConnID
	trialSent bool
	wanted    bool
	trans     [NumTransitions]int64
	trials    int64
	observed  int64
}

func (b *breaker) allow() bool { return b.state == BreakerClosed }

func (b *breaker) fail(now Instant) bool {
	b.observed++
	switch b.state {
	case BreakerClosed:
		b.failures++
		if b.failures >= b.threshold {
			b.open(now, TransClosedOpen)
			return true
		}
	case BreakerHalfOpen:
		b.open(now, TransHalfOpenOpen)
		return true
	}
	return false
}

func (b *breaker) open(now Instant, t Transition) {
	b.state = BreakerOpen
	b.trans[t]++
	b.openUntil = now + b.openFor
	b.trialSent = false
	// Clear demand so a subsequent quiet outage does not probe without new arrivals.
	b.wanted = false
	b.trial = 0
}

func (b *breaker) success() {
	if b.state == BreakerClosed {
		b.failures = 0
	}
}

func (b *breaker) trialDone(id ConnID, ok bool, now Instant) {
	if b.state != BreakerHalfOpen || !b.trialSent || b.trial != id {
		return
	}
	if ok {
		b.state = BreakerClosed
		b.failures = 0
		b.trans[TransHalfOpenClosed]++
		b.trialSent = false
		b.trial = 0
		return
	}
	b.open(now, TransHalfOpenOpen)
}

func (b *breaker) tick(now Instant) {
	if b.state == BreakerOpen && now >= b.openUntil {
		b.state = BreakerHalfOpen
		b.trans[TransOpenHalfOpen]++
	}
}
