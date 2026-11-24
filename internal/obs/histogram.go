package obs

import (
	"errors"
	"fmt"
	"iter"
	"math/bits"
)

const (
	exactBuckets    = 256
	subBuckets      = 128
	MaxValue        = int64(1) << 62
	DurationLo      = int64(1_000)
	DurationHi      = int64(60_000_000_000)
	ErrorBoundDenom = subBuckets
	BasisPoints     = 10_000
)

var ErrRangeMismatch = errors.New("obs: histogram ranges differ")

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

func NewHistogram(lo, hi int64) (*Histogram, error) {
	if lo < 0 || hi <= lo || hi > MaxValue {
		return nil, fmt.Errorf("obs: histogram range [%d, %d] is not 0 <= lo < hi <= %d", lo, hi, MaxValue)
	}
	base := bucketIndex(lo)
	return &Histogram{lo: lo, hi: hi, base: base, counts: make([]int64, bucketIndex(hi)-base+1)}, nil
}

func NewDurationHistogram() *Histogram {
	h, _ := NewHistogram(DurationLo, DurationHi)
	return h
}

func (h *Histogram) Lo() int64          { return h.lo }
func (h *Histogram) Hi() int64          { return h.hi }
func (h *Histogram) Count() int64       { return h.total }
func (h *Histogram) ClampedLow() int64  { return h.clampedLow }
func (h *Histogram) ClampedHigh() int64 { return h.clampedHigh }
func (h *Histogram) BucketCount() int   { return len(h.counts) }

func (h *Histogram) Record(v int64) { h.RecordN(v, 1) }

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

func (h *Histogram) Reset() {
	clear(h.counts)
	h.total, h.clampedLow, h.clampedHigh, h.min, h.max = 0, 0, 0, 0, 0
}

func (h *Histogram) Quantile(bp int64) int64 {
	if h.total == 0 {
		return 0
	}
	bp = max(0, min(bp, BasisPoints))
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

func (h *Histogram) Percentile(p int64) int64 { return h.Quantile(p * 100) }

type Bucket struct {
	Lo    int64
	Hi    int64
	Count int64
}

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

func (h *Histogram) Snapshot() HistogramSnapshot {
	s := HistogramSnapshot{Lo: h.lo, Hi: h.hi, Count: h.total, ClampedLow: h.clampedLow, ClampedHigh: h.clampedHigh, Min: h.min, Max: h.max}
	for b := range h.Buckets() {
		s.Buckets = append(s.Buckets, b)
	}
	return s
}
