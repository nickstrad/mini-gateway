package obs

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func seeds() int {
	if testing.Short() {
		return 30
	}
	return 300
}

func randValue(r *rand.Rand, hi int64) int64 {
	switch r.IntN(3) {
	case 0:
		return r.Int64N(300)
	case 1:
		return r.Int64N(hi + 1)
	}
	return r.Int64N(int64(1) << uint(r.IntN(62)+1))
}

func TestRecordedValueFallsInItsBucket(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	prevHi := int64(-1)
	for idx := 0; idx < bucketIndex(MaxValue)+1; idx++ {
		lo, hi := bucketBounds(idx)
		if lo != prevHi+1 || hi < lo {
			t.Fatalf("bucket %d [%d, %d] does not follow %d", idx, lo, hi, prevHi)
		}
		prevHi = hi
		if bucketIndex(lo) != idx || bucketIndex(hi) != idx {
			t.Fatalf("bucket %d bounds map elsewhere", idx)
		}
	}
	for i := 0; i < 200000; i++ {
		v := randValue(r, MaxValue)
		lo, hi := bucketBounds(bucketIndex(v))
		if v < lo || v > hi {
			t.Fatalf("value %d outside its bucket [%d, %d]", v, lo, hi)
		}
		if (hi-lo)*ErrorBoundDenom > max(v, 1) && hi != lo {
			t.Fatalf("bucket [%d, %d] wider than v/%d for v=%d", lo, hi, ErrorBoundDenom, v)
		}
	}
}

func TestQuantileErrorBoundAgainstSortedReference(t *testing.T) {
	for seed := 0; seed < seeds(); seed++ {
		r := rand.New(rand.NewPCG(uint64(seed), 2))
		h := NewDurationHistogram()
		var ref []int64
		n := 1 + r.IntN(2000)
		for i := 0; i < n; i++ {
			v := DurationLo + r.Int64N(DurationHi-DurationLo+1)>>uint(r.IntN(36))
			h.Record(v)
			ref = append(ref, v)
		}
		slices.Sort(ref)
		for _, bp := range []int64{0, 1, 5000, 9000, 9900, 9990, 9999, 10000} {
			rank := (int64(n)*bp + BasisPoints - 1) / BasisPoints
			rank = max(rank, 1)
			want := ref[rank-1]
			got := h.Quantile(bp)
			if got < want || (got-want)*ErrorBoundDenom > want {
				t.Fatalf("seed %d bp %d: got %d, true %d: error over 1/%d", seed, bp, got, want, ErrorBoundDenom)
			}
		}
		if h.Percentile(99) != h.Quantile(9900) {
			t.Fatal("Percentile and Quantile disagree")
		}
	}
	if NewDurationHistogram().Quantile(5000) != 0 {
		t.Fatal("empty histogram")
	}
}

func TestMergeEqualsRecordingBoth(t *testing.T) {
	for seed := 0; seed < seeds(); seed++ {
		r := rand.New(rand.NewPCG(uint64(seed), 3))
		a, _ := NewHistogram(10, 1<<40)
		b, _ := NewHistogram(10, 1<<40)
		both, _ := NewHistogram(10, 1<<40)
		for i := 0; i < r.IntN(500); i++ {
			v := randValue(r, 1<<41)
			a.Record(v)
			both.Record(v)
		}
		for i := 0; i < r.IntN(500); i++ {
			v := randValue(r, 1<<41)
			b.Record(v)
			both.Record(v)
		}
		if err := a.Merge(b); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(a.counts, both.counts) || a.Snapshot().Count != both.Snapshot().Count ||
			a.min != both.min || a.max != both.max || a.clampedLow != both.clampedLow || a.clampedHigh != both.clampedHigh {
			t.Fatalf("seed %d: merge differs from recording both", seed)
		}
	}
	a, _ := NewHistogram(1, 100)
	b, _ := NewHistogram(1, 101)
	if !errors.Is(a.Merge(b), ErrRangeMismatch) {
		t.Fatal("range mismatch accepted")
	}
}

func TestClampsAreCounted(t *testing.T) {
	h := NewDurationHistogram()
	h.Record(5)
	h.Record(-3)
	h.RecordN(DurationHi+1, 4)
	h.Record(DurationLo)
	if h.ClampedLow() != 2 || h.ClampedHigh() != 4 || h.Count() != 7 {
		t.Fatalf("low %d high %d count %d", h.ClampedLow(), h.ClampedHigh(), h.Count())
	}
	if h.Quantile(BasisPoints) != DurationHi || h.Quantile(1) < DurationLo {
		t.Fatal("clamped values left the range")
	}
	sum := int64(0)
	for b := range h.Buckets() {
		if b.Lo < h.Lo() || b.Hi > h.Hi() || b.Lo > b.Hi {
			t.Fatalf("bucket %+v outside the range", b)
		}
		sum += b.Count
	}
	if sum != h.Count() {
		t.Fatal("bucket counts do not add up")
	}
	h.Reset()
	if h.Count() != 0 || h.ClampedHigh() != 0 || len(h.Snapshot().Buckets) != 0 {
		t.Fatal("reset")
	}
}

func TestRecordAllocatesNothing(t *testing.T) {
	h := NewDurationHistogram()
	v := int64(1)
	allocs := testing.AllocsPerRun(1000, func() {
		v = v*7 + 13
		h.Record(v % (2 * DurationHi))
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per Record", allocs)
	}
}

func TestHistogramRangeAndSize(t *testing.T) {
	for _, c := range [][2]int64{{-1, 5}, {5, 5}, {1, MaxValue + 1}} {
		if _, err := NewHistogram(c[0], c[1]); err == nil {
			t.Fatalf("range %v accepted", c)
		}
	}
	h := NewDurationHistogram()
	if h.BucketCount() > 4000 {
		t.Fatalf("%d buckets", h.BucketCount())
	}
	bytesHist, err := NewHistogram(0, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	bytesHist.Record(0)
	if bytesHist.Quantile(5000) != 0 {
		t.Fatal("zero not exact")
	}
}

func TestConservationNamesTheSideThatIsOff(t *testing.T) {
	c := Counters{Accepted: 10, Completed: 3, Failed: 2, Shed: 1, Queued: 1, InFlight: 3}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	c.Shed = 0
	err := c.Check()
	var ce *ConservationError
	if !errors.As(err, &ce) || ce.Sum != 9 {
		t.Fatalf("got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "accepted is higher by 1") || !strings.Contains(msg, "shed 0") {
		t.Fatalf("message %q", msg)
	}
	c.Shed = 3
	if msg := c.Check().Error(); !strings.Contains(msg, "the parts are higher by 2") {
		t.Fatalf("message %q", msg)
	}
	var g Gauge
	g.Add(5)
	g.Add(-3)
	g.Set(1)
	if g.Value != 1 || g.Peak != 5 {
		t.Fatalf("gauge %+v", g)
	}
}

func TestGenericRangesForBytesAndCounts(t *testing.T) {
	h, err := NewHistogram(1, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	for v := int64(1); v <= 255; v++ {
		h.Record(v)
	}
	for bp := int64(1); bp <= BasisPoints; bp += 37 {
		q := h.Quantile(bp)
		rank := (255*bp + BasisPoints - 1) / BasisPoints
		if q != max(rank, 1) {
			t.Fatalf("exact range: bp %d got %d want %d", bp, q, rank)
		}
	}
	counts, _ := NewHistogram(0, 1000)
	counts.RecordN(1000, 3)
	counts.RecordN(5, 0)
	counts.RecordN(5, -2)
	if counts.Count() != 3 || counts.Quantile(5000) != 1000 {
		t.Fatalf("count %d q %d", counts.Count(), counts.Quantile(5000))
	}
}

func TestQuantileArgumentClamped(t *testing.T) {
	h := NewDurationHistogram()
	h.Record(5000)
	h.Record(9_000_000)
	if h.Quantile(-5) != h.Quantile(0) || h.Quantile(20_000) != h.Quantile(BasisPoints) {
		t.Fatal("out-of-range quantile not clamped")
	}
	if h.Quantile(0) < 5000 || h.Quantile(0) > 5000+5000/ErrorBoundDenom {
		t.Fatal(h.Quantile(0))
	}
}

func TestBucketsIterationStopsEarlyAndSnapshotCopies(t *testing.T) {
	h := NewDurationHistogram()
	for _, v := range []int64{2000, 3000, 4_000_000, 4_000_001, 50_000_000_000} {
		h.Record(v)
	}
	n := 0
	for range h.Buckets() {
		n++
		if n == 2 {
			break
		}
	}
	if n != 2 {
		t.Fatal("early stop")
	}
	snap := h.Snapshot()
	if snap.Count != 5 || snap.Min != 2000 || snap.Max != 50_000_000_000 || snap.Lo != DurationLo || snap.Hi != DurationHi {
		t.Fatalf("%+v", snap)
	}
	if len(snap.Buckets) != 4 {
		t.Fatalf("%d buckets: %+v", len(snap.Buckets), snap.Buckets)
	}
	h.Record(7000)
	if len(snap.Buckets) != 4 || snap.Count != 5 {
		t.Fatal("snapshot shares state with the histogram")
	}
	prev := int64(-1)
	for _, b := range snap.Buckets {
		if b.Lo <= prev {
			t.Fatal("buckets out of order")
		}
		prev = b.Hi
	}
}

func TestMergeIntoEmptyAndFromEmpty(t *testing.T) {
	a := NewDurationHistogram()
	b := NewDurationHistogram()
	b.Record(10_000)
	b.Record(-1)
	if err := a.Merge(NewDurationHistogram()); err != nil || a.Count() != 0 {
		t.Fatal("merge of empty")
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if a.Count() != 2 || a.ClampedLow() != 1 || a.Snapshot().Min != DurationLo || a.Snapshot().Max != 10_000 {
		t.Fatalf("%+v", a.Snapshot())
	}
}

func TestDurationHistogramFootprint(t *testing.T) {
	h := NewDurationHistogram()
	if bytes := h.BucketCount() * 8; bytes > 27_000 {
		t.Fatalf("%d bytes of buckets", bytes)
	}
	if bucketIndex(DurationHi) >= bucketIndex(MaxValue) {
		t.Fatal("range")
	}
}

func TestConserveWithNoParts(t *testing.T) {
	if Conserve(T("x", 0)) != nil || Conserve(T("x", 1)) == nil {
		t.Fatal("empty parts")
	}
}
