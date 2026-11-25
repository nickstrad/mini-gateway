package obs

import (
	"strconv"
	"testing"
)

func TestRecordingAllocatesNothing(t *testing.T) {
	var c Counter
	var g Gauge
	h := NewDurationHistogram()
	on := NewDetailDurationHistogram(true)
	off := NewDetailDurationHistogram(false)
	v := int64(1)
	cases := []struct {
		name string
		fn   func()
	}{
		{"counter increment", func() { c.Inc() }},
		{"gauge add", func() { g.Add(1) }},
		{"histogram record", func() { v = v*7 + 13; h.Record(v % DurationHi) }},
		{"detail histogram on", func() { v = v*7 + 13; on.Record(v % DurationHi) }},
		{"detail histogram off", func() { off.Record(v) }},
	}
	for _, tc := range cases {
		if n := testing.AllocsPerRun(1000, tc.fn); n != 0 {
			t.Errorf("%s: %v allocations per event", tc.name, n)
		}
	}
	if c.Value() == 0 || g.Peak == 0 || h.Count() == 0 {
		t.Fatal("recording did nothing")
	}
}

func BenchmarkRecordCounter(b *testing.B) {
	var c Counter
	for b.Loop() {
		c.Inc()
	}
}

func BenchmarkRecordHistogram(b *testing.B) {
	h := NewDurationHistogram()
	v := int64(1)
	for b.Loop() {
		v = v*7 + 13
		h.Record(v % DurationHi)
	}
}

func BenchmarkRecordDetail(b *testing.B) {
	for _, detail := range []bool{false, true} {
		b.Run("detail="+strconv.FormatBool(detail), func(b *testing.B) {
			h := NewDetailDurationHistogram(detail)
			v := int64(1)
			for b.Loop() {
				v = v*7 + 13
				h.Record(v % DurationHi)
			}
			b.ReportMetric(float64(h.HeldBytes()), "held-bytes")
		})
	}
}

func BenchmarkPublish(b *testing.B) {
	for _, tenants := range []int{1, 32, 1000} {
		for _, detail := range []bool{false, true} {
			b.Run("tenants="+strconv.Itoa(tenants)+"/detail="+strconv.FormatBool(detail), func(b *testing.B) {
				s := newSynth()
				for i := 0; i < tenants; i++ {
					for _, o := range synthOutcomes {
						s.apply(synthEvent{tenant: "t" + strconv.Itoa(i), outcome: o, value: int64(1000 * (i + 1))})
					}
				}
				c := synthCollector(b, "i", s, detail)
				b.ReportAllocs()
				for b.Loop() {
					if _, err := c.Publish(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
