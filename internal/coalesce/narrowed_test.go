package coalesce

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

func narrowedTable(t *testing.T, maxFlights int) *Table {
	return mustTable(t, Config{Mode: ModeAuto, Join: JoinStrict, MaxFlights: maxFlights, MaxReplyBytes: 64, Deadline: 1000,
		NoShareFor: 40, NoShareMax: 2})
}

func ownedRunning(t *testing.T, tb *Table, text string) {
	t.Helper()
	if n := tb.Running(work.ShareKey{Text: []byte(text)}); n > 1 {
		t.Fatalf("%d reads of %q run among the flights the table owns", n, text)
	}
}

func TestACancelledReadNoLongerOwnedLetsTheNextReadStartAndIsCounted(t *testing.T) {
	tb := narrowedTable(t, 8)
	f := tb.Join(0, 1, read("a", "k"))
	tb.Started(0, f.Flight)
	if l := tb.Leave(1, 1, LeaveCancel); l.Kind != LeftLast {
		t.Fatalf("left %v", l.Kind)
	}
	if s := tb.Snapshot(); s.Cancelled != 1 {
		t.Fatalf("cancelled %d", s.Cancelled)
	}
	g := tb.Join(1, 2, read("a", "k"))
	if g.Verdict != VerdictLead {
		t.Fatalf("after the last waiter left the next read got %s", g.Verdict)
	}
	tb.Started(1, g.Flight)
	ownedRunning(t, tb, "k")
}

func TestAKeyOnTheNoShareListPassesUnsharedAndIsCounted(t *testing.T) {
	tb := narrowedTable(t, 8)
	f := tb.Join(0, 1, read("a", "k"))
	tb.Started(0, f.Flight)
	tb.Stored(1, f.Flight, 65)
	if g := tb.Join(2, 2, read("a", "k")); g.Verdict != VerdictPass {
		t.Fatalf("a listed key got %s", g.Verdict)
	}
	if s := tb.Snapshot(); s.PassedNoShare != 1 {
		t.Fatalf("passed no-share %d", s.PassedNoShare)
	}
	ownedRunning(t, tb, "k")
}

func TestARequestArrivingAtAFullTablePassesUnsharedAndIsCounted(t *testing.T) {
	tb := narrowedTable(t, 1)
	tb.Join(0, 1, read("a", "occupier"))
	for id := RequestID(2); id <= 3; id++ {
		if g := tb.Join(1, id, read("a", "other")); g.Verdict != VerdictPass {
			t.Fatalf("request %d at a full table got %s", id, g.Verdict)
		}
	}
	if s := tb.Snapshot(); s.PassedFull != 2 || s.Overflows[CapFlights] != 2 {
		t.Fatalf("passed full %d, flights overflows %d", s.PassedFull, s.Overflows[CapFlights])
	}
	ownedRunning(t, tb, "other")
}

func TestAtOneInstantTheOrderOfEventsDecidesWhatALeaverGets(t *testing.T) {
	for _, resultFirst := range []bool{true, false} {
		tb := narrowedTable(t, 8)
		f := tb.Join(0, 1, read("a", "x"))
		tb.Join(0, 2, read("a", "x"))
		if resultFirst {
			tb.Finish(7, f.Flight, Result{})
		}
		tb.Leave(7, 2, LeaveCancel)
		if !resultFirst {
			tb.Finish(7, f.Flight, Result{})
		}
		replied := false
		for {
			n, ok := tb.TakeNotice()
			if !ok {
				break
			}
			replied = replied || n.Waiter == 2 && n.Kind == NoticeReply
		}
		s := tb.Snapshot()
		switch {
		case resultFirst && (!replied || s.WaiterReply != 2 || s.LeftUnknown != 1):
			t.Fatalf("result first: replied %v, replies %d, late leaves %d", replied, s.WaiterReply, s.LeftUnknown)
		case !resultFirst && (replied || s.WaiterReply != 1 || s.WaiterOwnError != 1):
			t.Fatalf("leave first: replied %v, replies %d, own errors %d", replied, s.WaiterReply, s.WaiterOwnError)
		}
	}
}
