package adv_test

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

const seed int64 = 731921

func mark(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d; REPLAY: go test -count=1 ./agent_work/adversarial/c01-sim-kernel -run '^%s$' -v", seed, t.Name())
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func replayWork() sim.Digests {
	s := sim.New(sim.Config{Seed: seed, TraceRing: 3})
	r := s.Stream("adversary")
	var total int64
	s.RegisterState("total", func(w sim.StateSink) { w.Int(total) })
	for i := 0; i < 4096; i++ {
		v := r.Int64()
		tm := s.At(sim.Time(v%31), func() { total ^= v; s.Trace(sim.K("attack"), v, total, 0, 0) })
		if i%7 == 0 {
			tm.Cancel()
		}
	}
	if err := s.RunUntilQuiet(); err != nil {
		panic(err)
	}
	return s.Digests()
}
func TestAdv_1_RingWrapReplay(t *testing.T) {
	mark(t)
	a := replayWork()
	for i := 0; i < 7; i++ {
		if b := replayWork(); a != b {
			t.Fatalf("digests differ: %v / %v", a, b)
		}
	}
}
func TestAdv_1_ProcessReplay(t *testing.T) {
	mark(t)
	if os.Getenv("ADV_CHILD") == "1" {
		fmt.Println(replayWork())
		return
	}
	var first []byte
	for i := 0; i < 4; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestAdv_1_ProcessReplay$")
		c.Env = append(os.Environ(), "ADV_CHILD=1")
		b, e := c.CombinedOutput()
		must(t, e)
		if i > 0 && !bytes.Equal(first, b) {
			t.Fatalf("process outputs differ: %s / %s", first, b)
		}
		first = b
	}
}
func TestAdv_1_TraceToggle(t *testing.T) {
	mark(t)
	var ds [2]sim.Digest
	for i := range ds {
		s := sim.New(sim.Config{Seed: seed, TraceOff: i == 1})
		v := int64(0)
		s.RegisterState("x", func(w sim.StateSink) { w.Int(v) })
		for j := 0; j < 100; j++ {
			s.At(1, func() { v++; s.Trace(sim.K("x"), v, 0, 0, 0) })
		}
		must(t, s.RunUntilQuiet())
		ds[i] = s.StateDigest()
	}
	if ds[0] != ds[1] {
		t.Fatal("trace toggle changed state")
	}
}
func streamAttack(t *testing.T, labels []string, draws int) {
	mark(t)
	for _, label := range labels {
		a, b := sim.NewStream(seed, label), sim.NewStream(seed, label)
		noise := sim.NewStream(seed, "noise")
		for i := 0; i < draws; i++ {
			for j := 0; j < i%19; j++ {
				noise.Uint64()
			}
			if a.Uint64() != b.Uint64() {
				t.Fatalf("stream %q shifted at draw %d", label, i)
			}
		}
	}
}
func TestAdv_2_HostileLabels(t *testing.T) {
	streamAttack(t, []string{"", "\x00", "a/b", "a\x00b", string(bytes.Repeat([]byte("x"), 8192))}, 300)
}
func TestAdv_2_InterleavedConsumption(t *testing.T) {
	streamAttack(t, []string{"up", "down", "conn/999999"}, 10000)
}
func TestAdv_2_RecreatedStreams(t *testing.T) {
	mark(t)
	for _, sd := range []int64{math.MinInt64, -1, 0, math.MaxInt64} {
		a := sim.NewStream(sd, "persist")
		want := a.Uint64()
		for j := 0; j < 200; j++ {
			other := sim.NewStream(sd, fmt.Sprint(j))
			other.Uint64()
			if got := sim.NewStream(sd, "persist").Uint64(); got != want {
				t.Fatalf("seed=%d recreated stream differs", sd)
			}
		}
	}
}
func TestAdv_3_CancelInsideTie(t *testing.T) {
	mark(t)
	s := sim.New(sim.Config{Seed: seed})
	var victim sim.Timer
	var got []int
	s.At(7, func() {
		got = append(got, 1)
		if !victim.Cancel() {
			t.Error("pending victim not cancelled")
		}
		s.At(7, func() { got = append(got, 4) })
	})
	victim = s.At(7, func() { t.Error("cancelled callback fired") })
	s.At(7, func() { got = append(got, 3) })
	must(t, s.RunUntilQuiet())
	if !slices.Equal(got, []int{1, 3, 4}) {
		t.Fatalf("tie order %v", got)
	}
}
func TestAdv_3_BackgroundHandleCopies(t *testing.T) {
	mark(t)
	s := sim.New(sim.Config{Seed: seed})
	for i := 0; i < 10000; i++ {
		a := s.At(sim.MaxTime, func() { t.Error("cancelled timer fired") })
		b := a
		b.MarkBackground()
		a.MarkBackground()
		if !a.Cancel() || b.Cancel() || b.Pending() {
			t.Fatal("copy cancellation inconsistent")
		}
	}
	st := s.Snapshot()
	if st.HeapLen != 0 || st.BackgroundTimers != 0 {
		t.Fatalf("cancel residue %+v", st)
	}
	must(t, s.RunUntilQuiet())
}
func TestAdv_3_ExtremeTimeArithmetic(t *testing.T) {
	mark(t)
	vs := []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64}
	clamp := func(v *big.Int) int64 {
		if v.Cmp(big.NewInt(math.MaxInt64)) > 0 {
			return math.MaxInt64
		}
		if v.Cmp(big.NewInt(math.MinInt64)) < 0 {
			return math.MinInt64
		}
		return v.Int64()
	}
	for _, a := range vs {
		for _, b := range vs {
			want := clamp(new(big.Int).Add(big.NewInt(a), big.NewInt(b)))
			if got := int64(sim.Time(a).Add(time.Duration(b))); got != want {
				t.Fatalf("add %d %d=%d want %d", a, b, got, want)
			}
			want = clamp(new(big.Int).Sub(big.NewInt(a), big.NewInt(b)))
			if got := int64(sim.Time(a).Sub(sim.Time(b))); got != want {
				t.Fatalf("sub %d %d=%d want %d", a, b, got, want)
			}
		}
	}
}
func TestAdv_7_BackgroundMasksNoForeground(t *testing.T) {
	mark(t)
	s := sim.New(sim.Config{Seed: seed})
	for i := 0; i < 100; i++ {
		s.At(100, func() {}).MarkBackground()
	}
	tm := s.At(100, func() {})
	if s.CheckQuiesce() == nil {
		t.Fatal("foreground timer hidden among background timers")
	}
	tm.Cancel()
	must(t, s.CheckQuiesce())
}
func histogram(t *testing.T) *obs.Histogram {
	t.Helper()
	h, e := obs.NewHistogram(0, obs.MaxValue)
	must(t, e)
	return h
}
func TestAdv_8_PowerBoundaryError(t *testing.T) {
	mark(t)
	h := histogram(t)
	for k := 8; k <= 62; k++ {
		for d := int64(-2); d <= 2; d++ {
			v := (int64(1) << k) + d
			if v > obs.MaxValue {
				continue
			}
			h.Reset()
			h.Record(v)
			q := h.Quantile(9999)
			delta := new(big.Int).Mul(big.NewInt(q-v), big.NewInt(128))
			if q < v || delta.Cmp(big.NewInt(v)) > 0 {
				t.Fatalf("v=%d q=%d exceeds 1/128", v, q)
			}
		}
	}
}
func TestAdv_8_MergeTreeAndClamps(t *testing.T) {
	mark(t)
	a, b, c, ref := histogram(t), histogram(t), histogram(t), histogram(t)
	r := sim.NewStream(seed, "hist")
	for i := 0; i < 10000; i++ {
		v := r.Int64()
		if i%5 == 0 {
			v = -v
		}
		n := int64(i%9 + 1)
		[]*obs.Histogram{a, b, c}[i%3].RecordN(v, n)
		ref.RecordN(v, n)
	}
	must(t, b.Merge(c))
	must(t, a.Merge(b))
	if !reflect.DeepEqual(a.Snapshot(), ref.Snapshot()) {
		t.Fatal("merge tree disagrees with direct records")
	}
	if a.ClampedLow() == 0 || a.ClampedHigh() == 0 {
		t.Fatal("clamps did not move")
	}
}
func TestAdv_8_AllocationAndHugeRank(t *testing.T) {
	mark(t)
	h := histogram(t)
	if a := testing.AllocsPerRun(1000, func() { h.Record(123456789) }); a != 0 {
		t.Fatalf("record allocations=%g", a)
	}
	h.Reset()
	h.RecordN(0, math.MaxInt64/2)
	h.RecordN(obs.MaxValue, math.MaxInt64/2)
	if h.Quantile(5000) != 0 || h.Quantile(5001) != obs.MaxValue {
		t.Fatalf("large rank wrong: median=%d upper=%d", h.Quantile(5000), h.Quantile(5001))
	}
}
func shrinkCheck(t *testing.T, in []sim.Fault, p func([]sim.Fault) bool) {
	t.Helper()
	orig := slices.Clone(in)
	if !p(in) {
		t.Fatal("invalid attack: initial schedule does not fail")
	}
	out := sim.Shrink(in, p)
	if !p(out) {
		t.Fatalf("shrunk schedule no longer fails: %v", out)
	}
	for i := range out {
		c := slices.Delete(slices.Clone(out), i, i+1)
		if p(c) {
			t.Fatalf("removing fault %d still fails: %v", i, out)
		}
	}
	if !reflect.DeepEqual(orig, in) {
		t.Fatal("shrinker mutated caller input")
	}
}
func TestAdv_9_NonmonotonePredicate(t *testing.T) {
	mark(t)
	in := make([]sim.Fault, 25)
	for i := range in {
		in[i].Target = int64(i)
	}
	shrinkCheck(t, in, func(f []sim.Fault) bool {
		sum := int64(0)
		for _, x := range f {
			sum += x.Target
		}
		return len(f) > 0 && (sum%7 == 6 || len(f) == 25)
	})
}
func TestAdv_9_OrderedDuplicateTriggers(t *testing.T) {
	mark(t)
	in := []sim.Fault{{Target: 9}, {Target: 2}, {Target: 9}, {Target: 3}, {Target: 9}, {Target: 2}, {Target: 3}}
	shrinkCheck(t, in, func(fs []sim.Fault) bool {
		stage := 0
		for _, f := range fs {
			if f.Target == []int64{9, 2, 3}[stage] {
				stage++
				if stage == 3 {
					return true
				}
			}
		}
		return false
	})
}
func TestAdv_9_EmptyAndMaximumSchedule(t *testing.T) {
	mark(t)
	shrinkCheck(t, []sim.Fault{}, func([]sim.Fault) bool { return true })
	in := make([]sim.Fault, sim.MaxScheduleLen)
	for i := range in {
		in[i].Target = int64(i)
	}
	shrinkCheck(t, in, func(fs []sim.Fault) bool {
		for _, f := range fs {
			if f.Target == 513 {
				return true
			}
		}
		return false
	})
}
