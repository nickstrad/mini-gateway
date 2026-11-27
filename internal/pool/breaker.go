package pool

type BreakerState uint8

const (
	BreakerClosed BreakerState = iota
	BreakerOpen
	BreakerHalfOpen
	NumBreakerStates
)

var breakerNames = [NumBreakerStates]string{"closed", "open", "half-open"}

func (s BreakerState) String() string {
	if s < NumBreakerStates {
		return breakerNames[s]
	}
	return "?"
}

type Transition uint8

const (
	TransClosedOpen Transition = iota
	TransOpenHalfOpen
	TransHalfOpenClosed
	TransHalfOpenOpen
	NumTransitions
)

var transitionNames = [NumTransitions]string{"closed-open", "open-half-open", "half-open-closed", "half-open-open"}

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
