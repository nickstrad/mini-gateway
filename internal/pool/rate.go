package pool

import "time"

type limiter struct {
	interval Instant
	tat      Instant
}

func newLimiter(perSec int) limiter {
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
