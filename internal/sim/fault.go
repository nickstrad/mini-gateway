package sim

import (
	"fmt"
	"math/bits"
	"slices"
	"time"
)

// Fault registry and schedule-decoding caps.
const (
	// MaxFaultKinds is the maximum number of fault kinds per Sim instance.
	MaxFaultKinds = 64
	// MaxScheduleLen is the hard upper bound on faults decoded from bytes.
	MaxScheduleLen = 1024
	// DefaultMaxFaults is used when Space.MaxFaults is non-positive.
	DefaultMaxFaults = 64
	faultBytes       = 8
)

// KindFault is the trace record tag emitted whenever ApplyFault runs.
var KindFault = K("fault")

// FaultKind identifies a fault type registered on a Sim by a world package.
type FaultKind uint16

// Fault is a single scheduled or applied fault injection entry.
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

// RegisterFaultKind registers an apply callback for kind on this Sim. Keeping
// fault kinds per-Sim instead of in a package global prevents the available
// kind set from depending on which packages a binary happens to link.
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

// FaultKinds returns the registered fault kinds in ascending numeric order.
func (s *Sim) FaultKinds() []FaultKind {
	out := make([]FaultKind, 0, len(s.faults.kinds))
	for _, fk := range s.faults.kinds {
		out = append(out, fk.kind)
	}
	slices.Sort(out)
	return out
}

// FaultKindName returns the registered name for kind, or "fault-<id>" if
// unregistered.
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

// InjectAt schedules f to be applied at f.At (or Now if f.At is in the past).
func (s *Sim) InjectAt(f Fault) Timer { return s.AtHandler(f.At, &faultEvent{s: s, f: f}, 0) }

// InjectSchedule schedules every fault in sched at its respective At time.
func (s *Sim) InjectSchedule(sched []Fault) {
	for _, f := range sched {
		s.InjectAt(f)
	}
}

// ApplyFault immediately traces f at Now and dispatches it to its registered
// handler. Unknown kinds and handler errors fail the simulation via FaultError.
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

// Space defines the valid domain for ScheduleFromBytes: allowed fault kinds,
// target count, virtual-time window [Start, Start+Window), argument maxima,
// and maximum schedule length.
type Space struct {
	Kinds     []FaultKind
	Targets   int64
	Start     Time
	Window    Time
	MaxArg1   int64
	MaxArg2   int64
	MaxFaults int
}

// ScheduleFromBytes decodes an arbitrary byte slice into a valid, bounded,
// time-sorted fault schedule within sp. It is total: every byte slice decodes
// without panicking, and an empty sp.Kinds returns nil.
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
		// Scale the 16-bit offset via 128-bit multiplication (at*Window >> 16)
		// so Window can be as large as MaxTime without signed overflow.
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

// Shrink repeatedly deletes single faults from a copy of sched while
// stillFails holds, stopping only after a full pass removes nothing. The
// returned schedule still fails and is 1-minimal even for non-monotone
// predicates, and the caller's slice is never mutated.
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
