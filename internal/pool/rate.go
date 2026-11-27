package pool

import "time"

// limiter is a one-slot GCRA pacing dial attempts to at most one per interval.
type limiter struct {
	interval Instant
	tat      Instant
}

func newLimiter(perSec int) limiter {
	// Round up so rates that do not divide 1s still hold floor(W*rate/1s)+1.
	return limiter{interval: (Instant(time.Second) + Instant(perSec) - 1) / Instant(perSec)}
}

func (l *limiter) ready(now Instant) bool { return now >= l.tat }

func (l *limiter) take(now Instant) {
	if l.tat < now {
		l.tat = now
	}
	l.tat += l.interval
}

func (l *limiter) next() Instant { return l.tat }
