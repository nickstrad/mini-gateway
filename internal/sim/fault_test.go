package sim

import (
	"errors"
	"slices"
	"testing"
)

var testSpace = Space{Kinds: []FaultKind{3, 9, 12}, Targets: 5, Start: 100, Window: 1_000_000, MaxArg1: 50, MaxArg2: 7, MaxFaults: 20}

func checkSchedule(t *testing.T, data []byte, sp Space) []Fault {
	sched := ScheduleFromBytes(data, sp)
	limit := sp.MaxFaults
	if limit <= 0 {
		limit = DefaultMaxFaults
	}
	if len(sched) > limit || len(sched) > MaxScheduleLen {
		t.Fatalf("schedule of %d faults exceeds the bound", len(sched))
	}
	for i, f := range sched {
		if !slices.Contains(sp.Kinds, f.Kind) {
			t.Fatalf("kind %d not in the space", f.Kind)
		}
		if f.Target < 0 || (sp.Targets > 0 && f.Target >= sp.Targets) {
			t.Fatalf("target %d out of range", f.Target)
		}
		if f.At < sp.Start || (sp.Window > 0 && f.At >= sp.Start+sp.Window) {
			t.Fatalf("time %d out of the window", f.At)
		}
		if f.Arg1 < 0 || f.Arg1 > max(sp.MaxArg1, 0) || f.Arg2 < 0 || f.Arg2 > max(sp.MaxArg2, 0) {
			t.Fatalf("args out of range: %+v", f)
		}
		if i > 0 && sched[i-1].At > f.At {
			t.Fatal("schedule not sorted by time")
		}
	}
	return sched
}

func FuzzScheduleFromBytes(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1, 2, 3})
	f.Add(make([]byte, 4096))
	f.Fuzz(func(t *testing.T, data []byte) {
		sched := checkSchedule(t, data, testSpace)
		if !slices.Equal(sched, ScheduleFromBytes(data, testSpace)) {
			t.Fatal("decoding is not a function of the bytes")
		}
		checkSchedule(t, data, Space{Kinds: []FaultKind{1}})
		if ScheduleFromBytes(data, Space{}) != nil {
			t.Fatal("empty kind list must decode to an empty schedule")
		}
	})
}

func TestScheduleFromBytesIsTotal(t *testing.T) {
	r := NewStream(1, "bytes")
	for i := 0; i < 2000; i++ {
		data := make([]byte, r.IntN(400))
		for j := range data {
			data[j] = byte(r.Uint32())
		}
		checkSchedule(t, data, testSpace)
	}
	if n := len(ScheduleFromBytes(make([]byte, 1<<20), Space{Kinds: []FaultKind{1}, MaxFaults: 1 << 30})); n != MaxScheduleLen {
		t.Fatalf("unbounded: %d", n)
	}
}

func TestFaultsAppliedAtTheirInstantsAndTraced(t *testing.T) {
	s := New(Config{Seed: 1})
	var seen []Fault
	if err := s.RegisterFaultKind(7, "seven", func(f Fault) error { seen = append(seen, f); return nil }); err != nil {
		t.Fatal(err)
	}
	if s.RegisterFaultKind(7, "again", nil) == nil {
		t.Fatal("duplicate kind accepted")
	}
	var recs []Record
	s.SetTraceTap(func(r Record) { recs = append(recs, r) })
	s.InjectSchedule([]Fault{{At: 50, Kind: 7, Target: 2, Arg1: 3}, {At: 20, Kind: 7, Target: 1}})
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0].At != 20 || seen[1].At != 50 || seen[1].Arg1 != 3 {
		t.Fatalf("seen %+v", seen)
	}
	if len(recs) != 2 || recs[0].Kind != KindFault || recs[1].B != 2 || recs[1].At != 50 {
		t.Fatalf("trace %+v", recs)
	}
	if s.FaultKindName(7) != "seven" || s.FaultKindName(8) != "fault-8" || !slices.Equal(s.FaultKinds(), []FaultKind{7}) {
		t.Fatal("names")
	}
	s.InjectAt(Fault{At: 60, Kind: 99})
	err := s.RunUntilQuiet()
	var fe *FaultError
	if !errors.As(err, &fe) || !errors.Is(err, ErrUnknownFaultKind) {
		t.Fatalf("got %v", err)
	}
}

func TestShrinkReturnsOneMinimalFailingSchedule(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		r := NewStream(seed, "shrink")
		var sched []Fault
		for i := 0; i < 30; i++ {
			sched = append(sched, Fault{At: Time(i), Kind: FaultKind(r.IntN(6)), Target: int64(r.IntN(3))})
		}
		fails := func(s []Fault) bool {
			kinds := map[FaultKind]bool{}
			for _, f := range s {
				kinds[f.Kind] = true
			}
			return kinds[1] && kinds[4] || kinds[2] && len(s) > 12
		}
		if !fails(sched) {
			continue
		}
		out := Shrink(sched, fails)
		if !fails(out) {
			t.Fatalf("seed %d: shrunk schedule no longer fails", seed)
		}
		for i := range out {
			if fails(slices.Delete(slices.Clone(out), i, i+1)) {
				t.Fatalf("seed %d: fault %d could still be removed", seed, i)
			}
		}
		if len(out) > 13 {
			t.Fatalf("seed %d: %d faults left", seed, len(out))
		}
	}
}
