package sim

import (
	"fmt"
	"math/bits"
	"slices"
	"time"
)

const (
	MaxFaultKinds    = 64
	MaxScheduleLen   = 1024
	DefaultMaxFaults = 64
	faultBytes       = 8
)

var KindFault = K("fault")

type FaultKind uint16

type Fault struct {
	At     Time
	Kind   FaultKind
	Target int64
	Arg1   int64
	Arg2   int64
}

type faultKind struct {
	kind  FaultKind
	name  string
	apply func(Fault) error
}

type faultRegistry struct {
	kinds   []faultKind
	applied int64
}

func (r *faultRegistry) find(k FaultKind) (faultKind, bool) {
	for _, fk := range r.kinds {
		if fk.kind == k {
			return fk, true
		}
	}
	return faultKind{}, false
}

func (s *Sim) RegisterFaultKind(kind FaultKind, name string, apply func(Fault) error) error {
	if _, ok := s.faults.find(kind); ok {
		return fmt.Errorf("%w: fault kind %d", ErrDuplicateName, kind)
	}
	if len(s.faults.kinds) >= MaxFaultKinds {
		return fmt.Errorf("%w: more than %d fault kinds", ErrRegistryFull, MaxFaultKinds)
	}
	s.faults.kinds = append(s.faults.kinds, faultKind{kind: kind, name: name, apply: apply})
	return nil
}

func (s *Sim) FaultKinds() []FaultKind {
	out := make([]FaultKind, 0, len(s.faults.kinds))
	for _, fk := range s.faults.kinds {
		out = append(out, fk.kind)
	}
	slices.Sort(out)
	return out
}

func (s *Sim) FaultKindName(kind FaultKind) string {
	if fk, ok := s.faults.find(kind); ok {
		return fk.name
	}
	return fmt.Sprintf("fault-%d", kind)
}

type faultEvent struct {
	s *Sim
	f Fault
}

func (e *faultEvent) OnEvent(uint64) { e.s.ApplyFault(e.f) }

func (s *Sim) InjectAt(f Fault) Timer { return s.AtHandler(f.At, &faultEvent{s: s, f: f}, 0) }

func (s *Sim) InjectSchedule(sched []Fault) {
	for _, f := range sched {
		s.InjectAt(f)
	}
}

func (s *Sim) ApplyFault(f Fault) {
	f.At = s.now
	s.Trace(KindFault, int64(f.Kind), f.Target, f.Arg1, f.Arg2)
	s.faults.applied++
	fk, ok := s.faults.find(f.Kind)
	if !ok {
		s.Fail(&FaultError{Fault: f, Err: ErrUnknownFaultKind})
		return
	}
	if err := fk.apply(f); err != nil {
		s.Fail(&FaultError{Fault: f, Err: err})
	}
}

type Space struct {
	Kinds     []FaultKind
	Targets   int64
	Start     Time
	Window    Time
	MaxArg1   int64
	MaxArg2   int64
	MaxFaults int
}

func ScheduleFromBytes(data []byte, sp Space) []Fault {
	if len(sp.Kinds) == 0 {
		return nil
	}
	limit := sp.MaxFaults
	if limit <= 0 {
		limit = DefaultMaxFaults
	}
	if limit > MaxScheduleLen {
		limit = MaxScheduleLen
	}
	n := (len(data) + faultBytes - 1) / faultBytes
	if n > limit {
		n = limit
	}
	out := make([]Fault, 0, n)
	for i := 0; i < n; i++ {
		var rec [faultBytes]byte
		copy(rec[:], data[i*faultBytes:])
		out = append(out, decodeFault(rec, sp))
	}
	slices.SortStableFunc(out, func(a, b Fault) int {
		if a.At < b.At {
			return -1
		}
		if a.At > b.At {
			return 1
		}
		return 0
	})
	return out
}

func decodeFault(rec [faultBytes]byte, sp Space) Fault {
	f := Fault{Kind: sp.Kinds[int(rec[0])%len(sp.Kinds)]}
	if sp.Targets > 0 {
		f.Target = int64(rec[1]) % sp.Targets
	}
	at := int64(rec[2])<<8 | int64(rec[3])
	f.At = sp.Start
	if sp.Window > 0 {
		hi, lo := bits.Mul64(uint64(at), uint64(sp.Window))
		f.At = Later(sp.Start, time.Duration(hi<<48|lo>>16))
	}
	f.Arg1 = boundedArg(int64(rec[4])<<8|int64(rec[5]), sp.MaxArg1)
	f.Arg2 = boundedArg(int64(rec[6])<<8|int64(rec[7]), sp.MaxArg2)
	return f
}

func boundedArg(v, max int64) int64 {
	if max <= 0 {
		return 0
	}
	return v % (max + 1)
}

func Shrink(sched []Fault, stillFails func([]Fault) bool) []Fault {
	cur := slices.Clone(sched)
	for {
		removed := false
		for i := 0; i < len(cur); {
			cand := slices.Delete(slices.Clone(cur), i, i+1)
			if stillFails(cand) {
				cur = cand
				removed = true
				continue
			}
			i++
		}
		if !removed {
			return cur
		}
	}
}
