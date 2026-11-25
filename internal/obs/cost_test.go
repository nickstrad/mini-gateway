package obs

import (
	"strconv"
	"testing"
)

// TestRecordingAllocatesNothing proves all five hot-path recording operations
// (counter, gauge, histogram, detail histogram on/off) allocate zero bytes.
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

// BenchmarkRecordCounter measures hot-path Counter.Inc latency.
func BenchmarkRecordCounter(b *testing.B) {
	var c Counter
	for b.Loop() {
		c.Inc()
	}
}

// BenchmarkRecordHistogram measures full-resolution duration histogram recording.
func BenchmarkRecordHistogram(b *testing.B) {
	h := NewDurationHistogram()
	v := int64(1)
	for b.Loop() {
		v = v*7 + 13
		h.Record(v % DurationHi)
	}
}

// BenchmarkRecordDetail compares DetailHistogram recording time and held memory
// with obs.detail off vs on.
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

// BenchmarkPublish measures Collector.Publish latency and allocations across
// 1, 32, and 1,000 tenants with obs.detail off vs on.
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
