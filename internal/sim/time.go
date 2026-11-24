package sim

import (
	"math"
	"strconv"
	"time"
)

type Time int64

const (
	MaxTime Time = math.MaxInt64
	MinTime Time = math.MinInt64
)

func (t Time) Add(d time.Duration) Time { return Later(t, d) }

func (t Time) Sub(u Time) time.Duration {
	if u < 0 && int64(t) > math.MaxInt64+int64(u) {
		return math.MaxInt64
	}
	if u > 0 && int64(t) < math.MinInt64+int64(u) {
		return math.MinInt64
	}
	return time.Duration(t - u)
}

func (t Time) String() string { return strconv.FormatInt(int64(t), 10) + "ns" }

func Later(t Time, d time.Duration) Time {
	if d > 0 && int64(t) > math.MaxInt64-int64(d) {
		return MaxTime
	}
	if d < 0 && int64(t) < math.MinInt64-int64(d) {
		return MinTime
	}
	return t + Time(d)
}

func AddDurations(a, b time.Duration) time.Duration {
	return Time(a).Add(b).Sub(0)
}

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
	if d < -1 && k > math.MinInt64/int64(d) {
		return math.MinInt64
	}
	return time.Duration(k) * d
}
