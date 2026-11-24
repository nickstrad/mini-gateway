package sim

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

func propSeeds(t *testing.T) int {
	if testing.Short() {
		return 50
	}
	return 400
}

type refEntry struct {
	at  Time
	seq uint64
}

func TestHeapMatchesSortedReference(t *testing.T) {
	for seed := 0; seed < propSeeds(t); seed++ {
		r := NewStream(int64(seed), "heap")
		var q eventQueue
		var ref []refEntry
		live := map[uint64]*event{}
		var seq uint64
		for op := 0; op < 300; op++ {
			switch k := r.IntN(10); {
			case k < 5:
				seq++
				e := &event{at: Time(r.IntN(50)), seq: seq}
				q.push(e)
				live[seq] = e
				ref = append(ref, refEntry{e.at, seq})
			case k < 7 && len(ref) > 0:
				i := r.IntN(len(ref))
				victim := ref[i]
				if !q.remove(live[victim.seq]) {
					t.Fatalf("seed %d: remove of a queued entry failed", seed)
				}
				if q.remove(live[victim.seq]) {
					t.Fatalf("seed %d: second remove succeeded", seed)
				}
				delete(live, victim.seq)
				ref = slices.Delete(ref, i, i+1)
			case len(ref) > 0:
				slices.SortFunc(ref, func(a, b refEntry) int {
					if a.at != b.at {
						return int(a.at - b.at)
					}
					return int(a.seq) - int(b.seq)
				})
				e := q.pop()
				if e.at != ref[0].at || e.seq != ref[0].seq {
					t.Fatalf("seed %d: popped (%d,%d), reference (%d,%d)", seed, e.at, e.seq, ref[0].at, ref[0].seq)
				}
				delete(live, e.seq)
				ref = ref[1:]
			}
			if len(q) != len(ref) {
				t.Fatalf("seed %d: heap len %d, reference %d", seed, len(q), len(ref))
			}
			for i, e := range q {
				if e.hi != i {
					t.Fatalf("seed %d: index %d stored as %d", seed, i, e.hi)
				}
			}
		}
	}
}

func TestTimersFireInDeadlineThenCreationOrder(t *testing.T) {
	for seed := 0; seed < propSeeds(t); seed++ {
		s := New(Config{Seed: int64(seed)})
		r := s.Stream("timers")
		type want struct {
			at Time
			id int
		}
		var fired []want
		var expect []want
		cancelled := map[int]bool{}
		var timers []Timer
		n := 2000
		for i := 0; i < n; i++ {
			at := Time(r.IntN(100))
			id := i
			timers = append(timers, s.At(at, func() {
				if cancelled[id] {
					t.Fatalf("seed %d: cancelled timer %d fired", seed, id)
				}
				fired = append(fired, want{s.Now(), id})
			}))
			expect = append(expect, want{at, id})
		}
		for i := 0; i < n/3; i++ {
			id := r.IntN(n)
			if timers[id].Cancel() == cancelled[id] {
				t.Fatalf("seed %d: Cancel of timer %d returned the wrong answer", seed, id)
			}
			cancelled[id] = true
		}
		if err := s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
		expect = slices.DeleteFunc(expect, func(w want) bool { return cancelled[w.id] })
		slices.SortStableFunc(expect, func(a, b want) int { return int(a.at - b.at) })
		if !slices.Equal(fired, expect) {
			t.Fatalf("seed %d: firing order differs from (deadline, creation) order", seed)
		}
		snap := s.Snapshot()
		if snap.HeapLen != 0 || snap.Cancelled != int64(len(cancelled)) || snap.Fired != int64(len(expect)) {
			t.Fatalf("seed %d: snapshot %+v", seed, snap)
		}
		for id := range cancelled {
			if timers[id].Cancel() || timers[id].Pending() {
				t.Fatalf("seed %d: cancelled timer still cancellable", seed)
			}
		}
	}
}

func TestSameInstantRunsInSequenceOrder(t *testing.T) {
	s := New(Config{Seed: 1})
	var got []int
	for i := 0; i < 100; i++ {
		i := i
		s.At(5, func() {
			got = append(got, i)
			if i%10 == 0 {
				s.After(0, func() { got = append(got, 1000+i) })
			}
		})
	}
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	var want []int
	for i := 0; i < 100; i++ {
		want = append(want, i)
	}
	for i := 0; i < 100; i += 10 {
		want = append(want, 1000+i)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestTimeSaturates(t *testing.T) {
	if Later(MaxTime-5, 10) != MaxTime || Later(MinTime+5, -10) != MinTime {
		t.Fatal("Later wrapped")
	}
	if MaxTime.Sub(-1) != math.MaxInt64 || MinTime.Sub(1) != math.MinInt64 {
		t.Fatal("Sub wrapped")
	}
	if Scaled(math.MaxInt64, 2) != math.MaxInt64 || Scaled(3, -math.MaxInt64) != math.MinInt64 || Scaled(-1, 5) != 0 {
		t.Fatal("Scaled wrapped")
	}
	r := NewStream(7, "sat")
	for i := 0; i < 10000; i++ {
		a := Time(r.Int64() - r.Int64()/2)
		d := time.Duration(r.Int64() - r.Int64()/2)
		got := Later(a, d)
		if d > 0 && got < a || d < 0 && got > a {
			t.Fatalf("Later(%d,%d) = %d went the wrong way", a, d, got)
		}
	}
	s := New(Config{Seed: 1})
	fired := false
	s.After(math.MaxInt64, func() { fired = true })
	s.At(10, func() { s.After(math.MaxInt64, func() {}) })
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if !fired || s.Now() != MaxTime {
		t.Fatalf("fired %v now %d", fired, s.Now())
	}
}

func TestStreamIsolation(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		b1 := NewStream(seed, "b")
		var plain []uint64
		for i := 0; i < 100; i++ {
			plain = append(plain, b1.Uint64())
		}
		a := NewStream(seed, "a")
		b2 := NewStream(seed, "b")
		var mixed []uint64
		for i := 0; i < 100; i++ {
			for j := 0; j <= i%3; j++ {
				a.Uint64()
			}
			mixed = append(mixed, b2.Uint64())
		}
		if !slices.Equal(plain, mixed) {
			t.Fatalf("seed %d: draws on stream a shifted stream b", seed)
		}
		if NewStream(seed, "a").Uint64() == NewStream(seed, "b").Uint64() {
			t.Fatalf("seed %d: two labels gave the same first value", seed)
		}
	}
}

func TestStepBudgetTurnsLivelockIntoStall(t *testing.T) {
	s := New(Config{Seed: 9, Scenario: "livelock", MaxSteps: 1000})
	var tick func()
	tick = func() { s.After(1, tick) }
	s.After(0, tick)
	err := s.RunUntilQuiet()
	var se *StallError
	if !errors.As(err, &se) || !errors.Is(err, ErrBudgetExhausted) || se.Seed != 9 {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "REPLAY: go run ./cmd/mgsim run livelock -seed 9") {
		t.Fatalf("no replay line: %v", err)
	}
	if _, again := s.Step(); again != err {
		t.Fatal("error not sticky")
	}

	f := New(Config{Seed: 9, MaxSameInstant: 500})
	var spin func()
	spin = func() { f.After(0, spin) }
	f.At(3, spin)
	err = f.RunUntilQuiet()
	if !errors.Is(err, ErrTimeFrozen) {
		t.Fatalf("got %v", err)
	}
	if f.Now() != 3 {
		t.Fatalf("time moved to %d", f.Now())
	}
}

func TestPanicBecomesTypedFailure(t *testing.T) {
	s := New(Config{Seed: 77, Replay: "REPLAY: custom"})
	s.After(5, func() {
		var m map[string]int
		m["x"]++
	})
	s.After(6, func() { t.Fatal("ran after the panic") })
	err := s.RunUntilQuiet()
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Seed != 77 || pe.Now != 5 || pe.Replay != "REPLAY: custom" {
		t.Fatalf("got %#v", err)
	}
	if !strings.Contains(err.Error(), "REPLAY: custom") || !strings.Contains(pe.Value, "nil map") {
		t.Fatalf("message %q", err.Error())
	}

	inv := New(Config{Seed: 3})
	inv.RegisterInvariant("boom", func() error {
		var p *Sim
		return p.err
	})
	inv.After(1, func() {})
	if err := inv.RunUntilQuiet(); !errors.As(err, &pe) {
		t.Fatalf("invariant panic not converted: %v", err)
	}
}

func TestInvariantsCheckedAfterEveryEventAndSampled(t *testing.T) {
	s := New(Config{Seed: 1})
	x := 0
	every, sampled := 0, 0
	s.RegisterInvariant("x small", func() error {
		every++
		if x > 5 {
			return errors.New("x too big")
		}
		return nil
	})
	s.RegisterSampledInvariant("sampled", 3, func() error { sampled++; return nil })
	for i := 0; i < 10; i++ {
		s.After(time.Duration(i), func() { x++ })
	}
	err := s.RunUntilQuiet()
	var ie *InvariantError
	if !errors.As(err, &ie) || ie.Name != "x small" || ie.Steps != 6 {
		t.Fatalf("got %v", err)
	}
	if every != 6 || sampled != 1 {
		t.Fatalf("every %d sampled %d", every, sampled)
	}
}

func TestQuiesceCatchesLeakedTimerButNotBackground(t *testing.T) {
	s := New(Config{Seed: 1})
	bg := s.After(time.Hour, func() {})
	bg.MarkBackground()
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckQuiesce(); err != nil {
		t.Fatalf("background timer flagged: %v", err)
	}
	leak := s.After(time.Hour, func() {})
	err := s.CheckQuiesce()
	var qe *QuiesceError
	if !errors.As(err, &qe) || qe.Name != "sim.timers" {
		t.Fatalf("leaked timer not caught: %v", err)
	}
	leak.Cancel()
	used := 1
	s.RegisterQuiesce("actor", func() error {
		if used != 0 {
			return errors.New("still in use")
		}
		return nil
	})
	if err := s.CheckQuiesce(); !errors.As(err, &qe) || qe.Name != "actor" {
		t.Fatalf("got %v", err)
	}
	used = 0
	if err := s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.BackgroundTimers != 1 || snap.PendingTimers != 0 {
		t.Fatalf("snapshot %+v", snap)
	}
}

func runTraceToy(seed int64, traceOff bool) *Sim {
	s := New(Config{Seed: seed, TraceOff: traceOff, TraceRing: 8})
	r := s.Stream("toy")
	total := int64(0)
	s.RegisterState("toy", func(w StateSink) { w.Int(total); w.String("end") })
	for i := 0; i < 50; i++ {
		v := r.Int64N(1000)
		s.After(time.Duration(r.IntN(100)), func() {
			total += v
			s.Trace(K("toy.add"), v, total, 0, 0)
		})
	}
	_ = s.RunUntilQuiet()
	return s
}

func TestTraceDigestsAndRing(t *testing.T) {
	a, b := runTraceToy(5, false), runTraceToy(5, false)
	if a.Digests() != b.Digests() {
		t.Fatal("same seed, different digests")
	}
	c := runTraceToy(6, false)
	if c.TraceDigest() == a.TraceDigest() || c.StateDigest() == a.StateDigest() {
		t.Fatal("different seeds, same digests")
	}
	off := runTraceToy(5, true)
	if off.StateDigest() != a.StateDigest() {
		t.Fatal("trace off changed the state digest")
	}
	if off.LogLen() != 0 || off.TraceDigest() == a.TraceDigest() {
		t.Fatal("trace off still recorded")
	}
	if a.LogLen() != 50 || len(a.TraceTail(100)) != 8 || len(a.LogTail(3)) != 3 {
		t.Fatalf("ring: len %d tail %d", a.LogLen(), len(a.TraceTail(100)))
	}
	tail := a.TraceTail(8)
	for i := 1; i < len(tail); i++ {
		if tail[i].At < tail[i-1].At {
			t.Fatal("ring tail out of order")
		}
	}
	if !strings.HasPrefix(a.LogTail(1)[0], "t=") || K("toy.add").String() != "toy.add" {
		t.Fatal("record formatting")
	}
	var sb strings.Builder
	if err := a.WriteLog(&sb); err != nil || strings.Count(sb.String(), "\n") != 8 {
		t.Fatal("WriteLog")
	}
	if a.RegisterState("toy", func(StateSink) {}) == nil {
		t.Fatal("duplicate state name accepted")
	}
}

func TestRunUntilAdvancesClock(t *testing.T) {
	s := New(Config{Seed: 1})
	ran := 0
	s.At(10, func() { ran++ })
	s.At(30, func() { ran++ })
	if err := s.RunUntil(20); err != nil || ran != 1 || s.Now() != 20 {
		t.Fatalf("ran %d now %d", ran, s.Now())
	}
	if err := s.RunFor(10); err != nil || ran != 2 || s.Now() != 30 {
		t.Fatalf("ran %d now %d", ran, s.Now())
	}
	s.At(5, func() {
		if s.Now() != 30 {
			t.Fatal("past event ran in the past")
		}
	})
	_ = s.RunUntilQuiet()
}

func TestHeapFullIsTypedFailure(t *testing.T) {
	s := New(Config{Seed: 1, MaxEvents: 10})
	for i := 0; i < 11; i++ {
		s.After(1, func() {})
	}
	if !errors.Is(s.Err(), ErrHeapFull) {
		t.Fatalf("got %v", s.Err())
	}
}

func TestThousandsOfTimers(t *testing.T) {
	s := New(Config{Seed: 1})
	n := 20000
	r := rand.New(rand.NewPCG(1, 2))
	var ts []Timer
	count := 0
	for i := 0; i < n; i++ {
		ts = append(ts, s.After(time.Duration(r.IntN(1000)), func() { count++ }))
	}
	for i := 0; i < n; i += 2 {
		ts[i].Cancel()
	}
	if s.Snapshot().HeapLen != int64(n/2) {
		t.Fatal("cancel did not remove from the heap")
	}
	_ = s.RunUntilQuiet()
	if count != n/2 {
		t.Fatalf("fired %d", count)
	}
}
