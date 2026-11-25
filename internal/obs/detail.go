package obs

const bytesPerBucket = 8

// DetailHistogram wraps a Histogram behind the obs.detail switch: when off, it
// allocates no bucket array and Record reduces to a single nil check.
type DetailHistogram struct {
	h *Histogram
}

// NewDetailHistogram creates a DetailHistogram covering [lo, hi] when detail is
// true, or a zero-memory disabled wrapper when detail is false.
func NewDetailHistogram(detail bool, lo, hi int64) (DetailHistogram, error) {
	if !detail {
		return DetailHistogram{}, nil
	}
	h, err := NewHistogram(lo, hi)
	if err != nil {
		return DetailHistogram{}, err
	}
	return DetailHistogram{h: h}, nil
}

// NewDetailDurationHistogram creates a duration DetailHistogram (1 µs–60 s)
// when detail is true, or a zero-memory disabled wrapper when detail is false.
func NewDetailDurationHistogram(detail bool) DetailHistogram {
	if !detail {
		return DetailHistogram{}
	}
	return DetailHistogram{h: NewDurationHistogram()}
}

// On reports whether bucket storage is allocated for d.
func (d DetailHistogram) On() bool { return d.h != nil }

// Record records v when d is enabled and does nothing when disabled.
func (d DetailHistogram) Record(v int64) {
	if d.h != nil {
		d.h.Record(v)
	}
}

// Snapshot returns the underlying HistogramSnapshot, or an empty one when disabled.
func (d DetailHistogram) Snapshot() HistogramSnapshot {
	if d.h == nil {
		return HistogramSnapshot{}
	}
	return d.h.Snapshot()
}

// HeldBytes returns the bucket-array memory held by d (0 when disabled).
func (d DetailHistogram) HeldBytes() int64 {
	if d.h == nil {
		return 0
	}
	return int64(d.h.BucketCount()) * bytesPerBucket
}

// Counter is a plain non-atomic int64 counter for single-threaded core components.
type Counter struct {
	n int64
}

// Inc increments the counter by 1.
func (c *Counter) Inc() { c.n++ }

// Add increases the counter by n.
func (c *Counter) Add(n int64) { c.n += n }

// Value returns the counter's current value.
func (c *Counter) Value() int64 { return c.n }
