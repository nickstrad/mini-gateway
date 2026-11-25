package obs

const bytesPerBucket = 8

type DetailHistogram struct {
	h *Histogram
}

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

func NewDetailDurationHistogram(detail bool) DetailHistogram {
	if !detail {
		return DetailHistogram{}
	}
	return DetailHistogram{h: NewDurationHistogram()}
}

func (d DetailHistogram) On() bool { return d.h != nil }

func (d DetailHistogram) Record(v int64) {
	if d.h != nil {
		d.h.Record(v)
	}
}

func (d DetailHistogram) Snapshot() HistogramSnapshot {
	if d.h == nil {
		return HistogramSnapshot{}
	}
	return d.h.Snapshot()
}

func (d DetailHistogram) HeldBytes() int64 {
	if d.h == nil {
		return 0
	}
	return int64(d.h.BucketCount()) * bytesPerBucket
}

type Counter struct {
	n int64
}

func (c *Counter) Inc()         { c.n++ }
func (c *Counter) Add(n int64)  { c.n += n }
func (c *Counter) Value() int64 { return c.n }
