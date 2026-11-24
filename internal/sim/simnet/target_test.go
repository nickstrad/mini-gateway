package simnet

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

func TestFaultsOnAClosedListenerHaveNoTarget(t *testing.T) {
	w := newWorld(1)
	l, _ := w.n.Listen("gone", nil)
	l.Close()
	for _, k := range []sim.FaultKind{FaultRefuse, FaultDialStall, FaultSilence} {
		w.s.ApplyFault(sim.Fault{Kind: k, Target: l.Index()})
		w.s.ApplyFault(sim.Fault{Kind: FaultClear, Target: l.Index(), Arg1: int64(k)})
	}
	if f := w.n.Snapshot().Faults; f != (FaultSnapshot{NoTarget: 6}) {
		t.Fatalf("%+v", f)
	}
}

func TestClearOnAnUnacceptedConnectionHasNoTarget(t *testing.T) {
	w := newWorld(1)
	e := w.n.Dial("srv", fixed(time.Millisecond), &recorder{})
	for _, k := range []sim.FaultKind{FaultStall, FaultChunk} {
		w.s.ApplyFault(sim.Fault{Kind: FaultClear, Target: e.ID(), Arg1: int64(k)})
	}
	if f := w.n.Snapshot().Faults; f != (FaultSnapshot{NoTarget: 2}) {
		t.Fatalf("%+v", f)
	}
	_ = w.s.RunFor(3 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: FaultClear, Target: e.ID(), Arg1: int64(FaultStall)})
	if f := w.n.Snapshot().Faults; f != (FaultSnapshot{NoTarget: 2, Clear: 1}) {
		t.Fatalf("after accept %+v", f)
	}
}
