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

func TestKernelSnapshotCountersMove(t *testing.T) {
	s := New(Config{Seed: 1})
	_ = s.RegisterFaultKind(1, "noop", func(Fault) error { return nil })
	_ = s.RegisterInvariant("ok", func() error { return nil })
	bg := s.After(time.Hour, func() {})
	bg.MarkBackground()
	s.After(5, func() {}).Cancel()
	s.InjectAt(Fault{At: 2, Kind: 1})
	var mid Snapshot
	s.After(3, func() {
		s.Trace(K("x"), 1, 0, 0, 0)
		s.After(1, func() {})
		mid = s.Snapshot()
	})
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	end := s.Snapshot()
	check := func(name string, a, b int64) {
		if a == 0 && b == 0 {
			t.Errorf("%s never moved", name)
		}
	}
	check("Steps", mid.Steps, end.Steps)
	check("Scheduled", mid.Scheduled, end.Scheduled)
	check("Fired", mid.Fired, end.Fired)
	check("Cancelled", mid.Cancelled, end.Cancelled)
	check("HeapLen", mid.HeapLen, end.HeapLen)
	check("HeapPeak", mid.HeapPeak, end.HeapPeak)
	check("PendingTimers", mid.PendingTimers, end.PendingTimers)
	check("BackgroundTimers", mid.BackgroundTimers, end.BackgroundTimers)
	check("TraceRecords", mid.TraceRecords, end.TraceRecords)
	check("TraceRingLen", mid.TraceRingLen, end.TraceRingLen)
	check("FaultsApplied", mid.FaultsApplied, end.FaultsApplied)
	check("InvariantChecks", mid.InvariantChecks, end.InvariantChecks)
	if end.Fired+end.Cancelled+end.HeapLen != end.Scheduled {
		t.Fatalf("scheduled %d != fired %d + cancelled %d + queued %d", end.Scheduled, end.Fired, end.Cancelled, end.HeapLen)
	}
}

func TestTimerHandleSemantics(t *testing.T) {
	s := New(Config{Seed: 1})
	var zero Timer
	if zero.Pending() || zero.Cancel() || zero.When() != 0 {
		t.Fatal("zero timer")
	}
	zero.MarkBackground()
	tm := s.After(10, func() {})
	if !tm.Pending() || tm.When() != 10 {
		t.Fatal("pending timer")
	}
	tm.MarkBackground()
	tm.MarkBackground()
	if s.Snapshot().BackgroundTimers != 1 {
		t.Fatal("background counted twice")
	}
	if !tm.Cancel() || s.Snapshot().BackgroundTimers != 0 {
		t.Fatal("cancel of a background timer")
	}
	fired := s.After(5, func() {})
	_ = s.RunUntilQuiet()
	if fired.Pending() || fired.Cancel() {
		t.Fatal("fired timer still cancellable")
	}
}

func TestRunUntilQuietIgnoresOnlyBackground(t *testing.T) {
	s := New(Config{Seed: 1})
	ticks := 0
	var tick func()
	tick = func() {
		ticks++
		s.After(10, tick).MarkBackground()
	}
	s.After(0, tick).MarkBackground()
	done := false
	s.After(35, func() { done = true })
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if !done || ticks != 4 || s.Now() != 35 {
		t.Fatalf("done %v ticks %d now %d", done, ticks, s.Now())
	}
	if err := s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

type countHandler struct {
	codes []uint64
}

func (h *countHandler) OnEvent(code uint64) { h.codes = append(h.codes, code) }

func TestHandlerEventsCarryTheirCode(t *testing.T) {
	s := New(Config{Seed: 1})
	h := &countHandler{}
	s.AtHandler(5, h, 7)
	s.AfterHandler(3, h, 9)
	s.AtHandler(5, h, 8).Cancel()
	_ = s.RunUntilQuiet()
	if !slices.Equal(h.codes, []uint64{9, 7}) {
		t.Fatalf("codes %v", h.codes)
	}
}

func TestFailIsStickyAndFirstWins(t *testing.T) {
	s := New(Config{Seed: 1})
	first := errors.New("first")
	s.Fail(nil)
	s.Fail(first)
	s.Fail(errors.New("second"))
	if s.Err() != first {
		t.Fatal("first error lost")
	}
	if _, err := s.Step(); err != first {
		t.Fatal("step ran after a failure")
	}
	if s.RunUntil(100) != first || s.Now() != 0 {
		t.Fatal("clock moved after a failure")
	}
	if s.Verify() != first || s.CheckInvariants() != first {
		t.Fatal("verify")
	}
}

func TestReplayLines(t *testing.T) {
	if got := DefaultReplay("echo", -3); got != "REPLAY: go run ./cmd/mgsim run echo -seed -3" {
		t.Fatal(got)
	}
	if !strings.Contains(DefaultReplay("", 5), "seed 5") {
		t.Fatal("no scenario")
	}
	s := New(Config{Seed: 2, Scenario: "x"})
	s.SetReplay("REPLAY: other")
	if s.Replay() != "REPLAY: other" || s.Seed() != 2 {
		t.Fatal("SetReplay")
	}
}

func TestRegistriesAreBounded(t *testing.T) {
	s := New(Config{Seed: 1})
	var err error
	for i := 0; i <= MaxRegistrations && err == nil; i++ {
		err = s.RegisterInvariant("i", func() error { return nil })
	}
	if !errors.Is(err, ErrRegistryFull) || !errors.Is(s.Err(), ErrRegistryFull) {
		t.Fatalf("got %v", err)
	}
	s = New(Config{Seed: 1})
	err = nil
	for i := 0; i <= MaxFaultKinds && err == nil; i++ {
		err = s.RegisterFaultKind(FaultKind(i), "k", func(Fault) error { return nil })
	}
	if !errors.Is(err, ErrRegistryFull) {
		t.Fatalf("fault kinds: %v", err)
	}
}

func TestFaultApplyErrorFailsTheRun(t *testing.T) {
	s := New(Config{Seed: 1})
	bad := errors.New("cannot apply")
	_ = s.RegisterFaultKind(2, "bad", func(Fault) error { return bad })
	s.InjectAt(Fault{At: 4, Kind: 2, Target: 1})
	err := s.RunUntilQuiet()
	var fe *FaultError
	if !errors.As(err, &fe) || !errors.Is(err, bad) || fe.Fault.At != 4 {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "fault kind 2 target 1 at 4") {
		t.Fatal(err.Error())
	}
}

func TestKindAndRecordFormatting(t *testing.T) {
	if K("abcdefghijk").String() != "abcdefgh" || K("").String() != "" {
		t.Fatal("kind truncation")
	}
	r := Record{At: 5, Kind: K("net.wr"), A: 1, B: -2, C: 3, D: 4}
	if r.String() != "t=5 net.wr 1 -2 3 4" {
		t.Fatal(r.String())
	}
	d := Digests{}
	if !strings.HasPrefix(d.String(), "trace=0000") || len(d.Trace.String()) != 64 {
		t.Fatal(d.String())
	}
	if Time(5).String() != "5ns" || Time(5).Add(3) != 8 || Time(5).Sub(8) != -3 {
		t.Fatal("time helpers")
	}
}

func TestTraceRingKeepsTheLastRecordsInOrder(t *testing.T) {
	for _, ring := range []int{1, 3, 8, 100} {
		s := New(Config{Seed: 1, TraceRing: ring})
		for i := 0; i < 50; i++ {
			s.Trace(K("x"), int64(i), 0, 0, 0)
		}
		tail := s.TraceTail(ring)
		want := min(ring, 50)
		if len(tail) != want {
			t.Fatalf("ring %d: %d records", ring, len(tail))
		}
		for i, r := range tail {
			if r.A != int64(50-want+i) {
				t.Fatalf("ring %d: record %d is %d", ring, i, r.A)
			}
		}
		if len(s.TraceTail(2)) != min(2, want) {
			t.Fatal("short tail")
		}
	}
	big := New(Config{Seed: 1, TraceRing: MaxTraceRing * 4})
	if cap(big.trace.ring) != MaxTraceRing {
		t.Fatal("ring not bounded")
	}
}

func TestTraceDigestCoversEveryField(t *testing.T) {
	base := Record{At: 1, Kind: K("k"), A: 2, B: 3, C: 4, D: 5}
	digest := func(r Record) Digest {
		s := New(Config{Seed: 1})
		s.trace.add(r)
		return s.TraceDigest()
	}
	ref := digest(base)
	for i := 0; i < 6; i++ {
		r := base
		switch i {
		case 0:
			r.At++
		case 1:
			r.Kind = K("j")
		case 2:
			r.A++
		case 3:
			r.B++
		case 4:
			r.C++
		case 5:
			r.D++
		}
		if digest(r) == ref {
			t.Fatalf("field %d not in the digest", i)
		}
	}
}

func TestStateDigestFollowsRegistrationOrder(t *testing.T) {
	mk := func(order []string) Digest {
		s := New(Config{Seed: 1})
		for _, name := range order {
			name := name
			s.RegisterState(name, func(w StateSink) { w.String(name); w.Int(int64(len(name))) })
		}
		return s.StateDigest()
	}
	if mk([]string{"a", "bb"}) == mk([]string{"bb", "a"}) {
		t.Fatal("order ignored")
	}
	if mk([]string{"a", "bb"}) != mk([]string{"a", "bb"}) {
		t.Fatal("not a function")
	}
}

func TestShrinkEdgeCases(t *testing.T) {
	if out := Shrink(nil, func([]Fault) bool { return true }); len(out) != 0 {
		t.Fatal("empty")
	}
	in := []Fault{{Kind: 1}, {Kind: 2}}
	out := Shrink(in, func(f []Fault) bool { return true })
	if len(out) != 0 || len(in) != 2 {
		t.Fatal("always-failing predicate should shrink to nothing without touching the input")
	}
	out = Shrink(in, func(f []Fault) bool { return len(f) == 2 })
	if len(out) != 2 {
		t.Fatal("nothing removable")
	}
}

func TestScheduleWindowAndArguments(t *testing.T) {
	sp := Space{Kinds: []FaultKind{4}, Targets: 3, Start: 1000, Window: 65536, MaxArg1: 255, MaxArg2: 0}
	sched := ScheduleFromBytes([]byte{0, 5, 0xff, 0xff, 0, 7, 9, 9, 0, 1, 0, 0, 1, 0}, sp)
	if len(sched) != 2 {
		t.Fatalf("%d faults", len(sched))
	}
	if sched[0].At != 1000 || sched[1].At != 1000+65535 {
		t.Fatalf("times %d %d", sched[0].At, sched[1].At)
	}
	if sched[1].Target != 2 || sched[1].Arg1 != 7 || sched[1].Arg2 != 0 || sched[0].Arg1 != 256%256 {
		t.Fatalf("%+v", sched)
	}
	big := ScheduleFromBytes([]byte{0, 0, 0xff, 0xff}, Space{Kinds: []FaultKind{1}, Window: MaxTime})
	if big[0].At < 0 {
		t.Fatal("window overflowed")
	}
}

type modelTimer struct {
	at        Time
	id        int
	cancelled bool
	fired     bool
}

func TestLoopAgainstReferenceModel(t *testing.T) {
	for seed := int64(0); seed < int64(propSeeds(t)); seed++ {
		s := New(Config{Seed: seed})
		r := s.Stream("model")
		var model []*modelTimer
		var handles []Timer
		var fired []int
		for op := 0; op < 300; op++ {
			switch k := r.IntN(10); {
			case k < 5:
				id := len(model)
				at := s.Now() + Time(r.IntN(40))
				m := &modelTimer{at: at, id: id}
				model = append(model, m)
				handles = append(handles, s.At(at, func() {
					m.fired = true
					fired = append(fired, id)
				}))
			case k < 7 && len(model) > 0:
				i := r.IntN(len(model))
				want := !model[i].cancelled && !model[i].fired
				if handles[i].Cancel() != want {
					t.Fatalf("seed %d: Cancel of %d returned %v", seed, i, !want)
				}
				if want {
					model[i].cancelled = true
				}
			default:
				until := s.Now() + Time(r.IntN(30))
				var due []*modelTimer
				for _, m := range model {
					if !m.cancelled && !m.fired && m.at <= until {
						due = append(due, m)
					}
				}
				slices.SortStableFunc(due, func(a, b *modelTimer) int { return int(a.at - b.at) })
				before := len(fired)
				if err := s.RunUntil(until); err != nil {
					t.Fatal(err)
				}
				got := fired[before:]
				if len(got) != len(due) {
					t.Fatalf("seed %d: fired %d, model %d", seed, len(got), len(due))
				}
				for j, m := range due {
					if got[j] != m.id {
						t.Fatalf("seed %d: fired %v, model order differs at %d", seed, got, j)
					}
				}
				if s.Now() != until {
					t.Fatalf("seed %d: clock at %d, want %d", seed, s.Now(), until)
				}
			}
		}
		pending := int64(0)
		for i, m := range model {
			if !m.cancelled && !m.fired {
				pending++
				if !handles[i].Pending() || handles[i].When() != m.at {
					t.Fatalf("seed %d: timer %d not pending at %d", seed, i, m.at)
				}
			}
		}
		if s.Snapshot().PendingTimers != pending {
			t.Fatalf("seed %d: %d pending, model %d", seed, s.Snapshot().PendingTimers, pending)
		}
	}
}

func TestAddDurationsSaturates(t *testing.T) {
	cases := []struct{ a, b, want time.Duration }{
		{math.MaxInt64, 1, math.MaxInt64},
		{math.MaxInt64 - 100, 1000, math.MaxInt64},
		{1, math.MaxInt64, math.MaxInt64},
		{math.MinInt64, -1, math.MinInt64},
		{-5, -math.MaxInt64, math.MinInt64},
		{3, 4, 7},
		{math.MaxInt64, math.MinInt64, -1},
	}
	for _, c := range cases {
		if got := AddDurations(c.a, c.b); got != c.want {
			t.Fatalf("AddDurations(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
