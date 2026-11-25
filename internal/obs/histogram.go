// Package obs provides the measurement half of mini-gateway's observability
// spine: a zero-allocation integer log-linear histogram, plain snapshot
// helpers, gauges with peak tracking, and named conservation checks.
//
// Collector assembly and output adapters (JSON, CSV, Prometheus, OTLP) land in
// later chunks; components only depend on these plain types.
package obs

import (
	"errors"
	"fmt"
	"iter"
	"math/bits"
)

// Histogram precision, range, and quantile scale constants.
const (
	exactBuckets = 256
	subBuckets   = 128
	// MaxValue is the largest upper bound accepted by NewHistogram.
	MaxValue = int64(1) << 62
	// DurationLo is the lower bound of NewDurationHistogram (1 µs in ns).
	DurationLo = int64(1_000)
	// DurationHi is the upper bound of NewDurationHistogram (60 s in ns).
	DurationHi = int64(60_000_000_000)
	// ErrorBoundDenom is the denominator of the worst-case relative quantile
	// error bound: (reported - true) * ErrorBoundDenom <= true (< 0.79%).
	ErrorBoundDenom = subBuckets
	// BasisPoints is the full scale (100%) accepted by Histogram.Quantile.
	BasisPoints = 10_000
)

// ErrRangeMismatch is returned by Histogram.Merge when two histograms have
// different [Lo, Hi] bounds.
var ErrRangeMismatch = errors.New("obs: histogram ranges differ")

// bucketIndex maps v >= 0 into a contiguous log-linear index: values 0..255
// get exact single-integer buckets; each power-of-two octave above 255 is
// split into 128 equal-width sub-buckets so bucket width is always < v/128.
func bucketIndex(v int64) int {
	if v < exactBuckets {
		return int(v)
	}
	e := bits.Len64(uint64(v)) - 8
	m := v >> e
	return exactBuckets + (e-1)*subBuckets + int(m-subBuckets)
}

func bucketBounds(idx int) (int64, int64) {
	if idx < exactBuckets {
		return int64(idx), int64(idx)
	}
	k := idx - exactBuckets
	e := k/subBuckets + 1
	m := int64(k%subBuckets + subBuckets)
	return m << e, (m+1)<<e - 1
}

// Histogram is a fixed-size, integer-only log-linear histogram. Recording uses
// no floating point and allocates nothing; values outside [Lo, Hi] are clamped
// into the edge bucket and tallied separately.
type Histogram struct {
	lo          int64
	hi          int64
	base        int
	counts      []int64
	total       int64
	clampedLow  int64
	clampedHigh int64
	min         int64
	max         int64
}

// NewHistogram allocates a histogram covering the inclusive integer range
// [lo, hi], requiring 0 <= lo < hi <= MaxValue.
func NewHistogram(lo, hi int64) (*Histogram, error) {
	if lo < 0 || hi <= lo || hi > MaxValue {
		return nil, fmt.Errorf("obs: histogram range [%d, %d] is not 0 <= lo < hi <= %d", lo, hi, MaxValue)
	}
	base := bucketIndex(lo)
	return &Histogram{lo: lo, hi: hi, base: base, counts: make([]int64, bucketIndex(hi)-base+1)}, nil
}

// NewDurationHistogram constructs a histogram for nanosecond durations from
// DurationLo (1 µs) to DurationHi (60 s), using 3,302 buckets (26.4 KB).
func NewDurationHistogram() *Histogram {
	h, _ := NewHistogram(DurationLo, DurationHi)
	return h
}

// Lo returns the inclusive lower bound of the histogram's range.
func (h *Histogram) Lo() int64 { return h.lo }

// Hi returns the inclusive upper bound of the histogram's range.
func (h *Histogram) Hi() int64 { return h.hi }

// Count returns the total number of recorded samples, including clamped ones.
func (h *Histogram) Count() int64 { return h.total }

// ClampedLow returns how many recorded samples fell below Lo.
func (h *Histogram) ClampedLow() int64 { return h.clampedLow }

// ClampedHigh returns how many recorded samples exceeded Hi.
func (h *Histogram) ClampedHigh() int64 { return h.clampedHigh }

// BucketCount returns the fixed number of backing buckets in h.
func (h *Histogram) BucketCount() int { return len(h.counts) }

// Record records one occurrence of v without allocating.
func (h *Histogram) Record(v int64) { h.RecordN(v, 1) }

// RecordN records n occurrences of v; non-positive n is ignored. Values outside
// [Lo, Hi] are clamped to the nearest endpoint and counted as clamped.
func (h *Histogram) RecordN(v, n int64) {
	if n <= 0 {
		return
	}
	if v < h.lo {
		v = h.lo
		h.clampedLow += n
	} else if v > h.hi {
		v = h.hi
		h.clampedHigh += n
	}
	h.counts[bucketIndex(v)-h.base] += n
	if h.total == 0 || v < h.min {
		h.min = v
	}
	if h.total == 0 || v > h.max {
		h.max = v
	}
	h.total += n
}

// Merge adds every bucket count and clamp counter from o into h. Both
// histograms must share the same [Lo, Hi] range.
func (h *Histogram) Merge(o *Histogram) error {
	if o.lo != h.lo || o.hi != h.hi {
		return fmt.Errorf("%w: [%d, %d] and [%d, %d]", ErrRangeMismatch, h.lo, h.hi, o.lo, o.hi)
	}
	if o.total == 0 {
		return nil
	}
	for i, c := range o.counts {
		h.counts[i] += c
	}
	if h.total == 0 || o.min < h.min {
		h.min = o.min
	}
	if h.total == 0 || o.max > h.max {
		h.max = o.max
	}
	h.total += o.total
	h.clampedLow += o.clampedLow
	h.clampedHigh += o.clampedHigh
	return nil
}

// Reset zeroes all bucket counts and summary fields while keeping the backing
// slice allocated.
func (h *Histogram) Reset() {
	clear(h.counts)
	h.total, h.clampedLow, h.clampedHigh, h.min, h.max = 0, 0, 0, 0, 0
}

// Quantile returns the upper bound (clipped to Hi) of the bucket containing
// rank ceil(Count * bp / 10000), with bp in basis points [0, 10000]. An empty
// histogram returns 0.
func (h *Histogram) Quantile(bp int64) int64 {
	if h.total == 0 {
		return 0
	}
	bp = max(0, min(bp, BasisPoints))
	// Compute ceil(total*bp / BasisPoints) in 128 bits so large totals never overflow.
	hi, lo := bits.Mul64(uint64(h.total), uint64(bp))
	q, r := bits.Div64(hi, lo, BasisPoints)
	rank := int64(q)
	if r != 0 {
		rank++
	}
	rank = max(rank, 1)
	seen := int64(0)
	for i, c := range h.counts {
		seen += c
		if seen >= rank {
			_, upper := bucketBounds(i + h.base)
			return min(upper, h.hi)
		}
	}
	return h.hi
}

// Percentile returns Quantile(p * 100) for a whole-number percentile p in [0, 100].
func (h *Histogram) Percentile(p int64) int64 { return h.Quantile(p * 100) }

// Bucket is one non-empty [Lo, Hi] bin yielded by Histogram.Buckets and
// Histogram.Snapshot.
type Bucket struct {
	Lo    int64
	Hi    int64
	Count int64
}

// Buckets iterates over all non-empty buckets in ascending order with bounds
// clipped to [Lo, Hi].
func (h *Histogram) Buckets() iter.Seq[Bucket] {
	return func(yield func(Bucket) bool) {
		for i, c := range h.counts {
			if c == 0 {
				continue
			}
			lo, hi := bucketBounds(i + h.base)
			if !yield(Bucket{Lo: max(lo, h.lo), Hi: min(hi, h.hi), Count: c}) {
				return
			}
		}
	}
}

// HistogramSnapshot is a plain, value-copied export of a Histogram's state.
type HistogramSnapshot struct {
	Lo          int64
	Hi          int64
	Count       int64
	ClampedLow  int64
	ClampedHigh int64
	Min         int64
	Max         int64
	Buckets     []Bucket
}

// Snapshot returns a detached HistogramSnapshot containing only non-empty
// buckets so later recordings do not mutate the snapshot.
func (h *Histogram) Snapshot() HistogramSnapshot {
	s := HistogramSnapshot{Lo: h.lo, Hi: h.hi, Count: h.total, ClampedLow: h.clampedLow, ClampedHigh: h.clampedHigh, Min: h.min, Max: h.max}
	for b := range h.Buckets() {
		s.Buckets = append(s.Buckets, b)
	}
	return s
}
