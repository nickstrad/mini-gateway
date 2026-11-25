package sim

// Time helpers adapted from object-store internal/sim/clock.go and
// internal/env/env.go at commit 25fb899317955eb7d87186f940702e55dc0e3cde.

import (
	"math"
	"strconv"
	"time"
)

// Time is virtual time in nanoseconds since the start of a simulation run.
type Time int64

// MaxTime and MinTime are the representable bounds of Time. Arithmetic in this
// package saturates at these bounds rather than wrapping around.
const (
	MaxTime Time = math.MaxInt64
	MinTime Time = math.MinInt64
)

// Add returns t shifted by d, saturating at MinTime and MaxTime.
func (t Time) Add(d time.Duration) Time { return Later(t, d) }

// Sub returns t - u as a duration, saturating at math.MinInt64 and
// math.MaxInt64 instead of wrapping.
func (t Time) Sub(u Time) time.Duration {
	if u < 0 && int64(t) > math.MaxInt64+int64(u) {
		return math.MaxInt64
	}
	if u > 0 && int64(t) < math.MinInt64+int64(u) {
		return math.MinInt64
	}
	return time.Duration(t - u)
}

// String formats t as an integer nanosecond count with an "ns" suffix.
func (t Time) String() string { return strconv.FormatInt(int64(t), 10) + "ns" }

// Later returns t + d, saturating at MaxTime or MinTime on signed overflow.
func Later(t Time, d time.Duration) Time {
	if d > 0 && int64(t) > math.MaxInt64-int64(d) {
		return MaxTime
	}
	if d < 0 && int64(t) < math.MinInt64-int64(d) {
		return MinTime
	}
	return t + Time(d)
}

// AddDurations returns a + b, saturating at math.MinInt64 and math.MaxInt64.
// World packages should use this instead of '+' whenever durations come from
// configuration or random draws so a sum cannot wrap negative and fire at now.
func AddDurations(a, b time.Duration) time.Duration {
	return Time(a).Add(b).Sub(0)
}

// Scaled returns k * d, clamping negative k to 0 and saturating on overflow.
func Scaled(k int64, d time.Duration) time.Duration {
	if k == 0 || d == 0 {
		return 0
	}
	if k < 0 {
		return 0
	}
	if d > 0 && k > math.MaxInt64/int64(d) {
		return math.MaxInt64
	}
	// Guard d < -1 rather than d < 0 because math.MinInt64 / -1 overflows int64.
	if d < -1 && k > math.MinInt64/int64(d) {
		return math.MinInt64
	}
	return time.Duration(k) * d
}
