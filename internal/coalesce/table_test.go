package coalesce

import (
	"errors"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

func read(tenant, text string) *work.Unit {
	return &work.Unit{Tenant: work.TenantID(tenant), Class: work.ClassStartLease, SafeRead: true, Shareable: true,
		HasKey: true, Key: work.ShareKey{Text: []byte(text)}}
}

func mustTable(t *testing.T, cfg Config) *Table {
	t.Helper()
	tb, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func notices(t *testing.T, tb *Table) []Notice {
	t.Helper()
	ns := takeAll(tb)
	if err := tb.Check(); err != nil {
		t.Fatal(err)
	}
	return ns
}

func TestStrictJoinsTheNextFlightRelaxedJoinsTheRunningOne(t *testing.T) {
	for _, join := range []JoinMode{JoinStrict, JoinRelaxed} {
		tb := mustTable(t, Config{Mode: ModeAuto, Join: join})
		lead := tb.Join(0, 1, read("a", "SELECT 1"))
		if lead.Verdict != VerdictLead {
			t.Fatalf("%v: first request %v", join, lead.Verdict)
		}
		if j := tb.Join(1, 2, read("a", "SELECT 1")); j.Verdict != VerdictFollow || j.Flight != lead.Flight {
			t.Fatalf("%v: a request before the flight starts joins it, got %+v", join, j)
		}
		tb.Started(2, lead.Flight)
		j := tb.Join(3, 3, read("b", "SELECT 1"))
		switch join {
		case JoinStrict:
			if j.Verdict != VerdictHold || j.Flight == lead.Flight {
				t.Fatalf("strict: a request after the read started waits for the next flight, got %+v", j)
			}
			if k := tb.Join(4, 4, read("b", "SELECT 1")); k.Flight != j.Flight || k.Verdict != VerdictFollow {
				t.Fatalf("strict: later requests join the waiting next flight, got %+v", k)
			}
			tb.Finish(5, lead.Flight, Result{})
			ns := notices(t, tb)
			if len(ns) != 2 || ns[0].Kind != NoticeReply || ns[0].Waiter != 2 || ns[1].Kind != NoticeStart || ns[1].Waiter != 3 {
				t.Fatalf("strict: the follower of the first flight gets the reply, then the next flight starts: %s", noticesString(ns))
			}
		case JoinRelaxed:
			if j.Verdict != VerdictFollow || j.Flight != lead.Flight {
				t.Fatalf("relaxed: a request joins the running read, got %+v", j)
			}
		}
		if n := tb.Running(work.ShareKey{Text: []byte("SELECT 1")}); n > 1 {
			t.Fatalf("%d reads of one key running", n)
		}
	}
}

func TestOnlyShareableUnitsJoinUnderAutoAndOnlyHintedUnderHinted(t *testing.T) {
	inTx := &work.Unit{Class: work.ClassContinueLease, SafeRead: true, HasKey: true, Key: work.ShareKey{Text: []byte("SELECT 1")}}
	write := &work.Unit{Class: work.ClassStartLease}
	hinted := &work.Unit{Class: work.ClassStartLease, Hinted: true, HasKey: true, Key: work.ShareKey{Text: []byte("/* mg:share */ SELECT now()")}}
	safe := read("", "SELECT 1")
	for _, c := range []struct {
		mode Mode
		u    *work.Unit
		lead bool
	}{
		{ModeAuto, safe, true}, {ModeAuto, inTx, false}, {ModeAuto, write, false}, {ModeAuto, hinted, false},
		{ModeHinted, hinted, true}, {ModeHinted, safe, false}, {ModeHinted, write, false},
		{ModeOff, safe, false}, {ModeOff, hinted, false},
	} {
		tb := mustTable(t, Config{Mode: c.mode})
		got := tb.Join(0, 1, c.u)
		if (got.Verdict == VerdictLead) != c.lead {
			t.Fatalf("mode %v unit %+v: %v", c.mode, c.u, got.Verdict)
		}
	}
}

func TestResultAtTheFlightDeadlineIsLate(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, Deadline: 100 * Millisecond})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	tb.Join(0, 2, read("", "SELECT 1"))
	tb.Started(1, lead.Flight)
	if at := tb.Next(); at != 100*Millisecond {
		t.Fatalf("next wake-up %d, want the flight deadline", at)
	}
	tb.Finish(100*Millisecond, lead.Flight, Result{})
	ns := notices(t, tb)
	if len(ns) != 2 || ns[0].Kind != NoticeExpireOwner || ns[0].Waiter != 1 || ns[1].Kind != NoticeExpired || ns[1].Waiter != 2 {
		t.Fatalf("a result at the deadline must find the flight expired: %s", noticesString(ns))
	}
	if s := tb.Snapshot(); s.Expired != 1 || s.Completed != 0 {
		t.Fatalf("expired %d completed %d", s.Expired, s.Completed)
	}
	if err := tb.Quiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestJoinAtTheInstantAFlightCompletesStartsANewFlight(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, Join: JoinRelaxed})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	tb.Started(0, lead.Flight)
	tb.Finish(10, lead.Flight, Result{})
	_ = notices(t, tb)
	if j := tb.Join(10, 2, read("", "SELECT 1")); j.Verdict != VerdictLead || j.Flight == lead.Flight {
		t.Fatalf("a join at the completion instant must not see the finished flight: %+v", j)
	}
}

func TestLeaveAtTheInstantTheResultArrivesGetsOnlyTheLeaversOwnError(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	tb.Join(0, 2, read("", "SELECT 1"))
	tb.Join(0, 3, read("", "SELECT 1"))
	tb.Started(1, lead.Flight)
	if l := tb.Leave(50, 2, LeaveCancel); l.Kind != LeftFollower {
		t.Fatalf("leave %+v", l)
	}
	tb.Finish(50, lead.Flight, Result{})
	ns := notices(t, tb)
	if len(ns) != 1 || ns[0].Waiter != 3 || ns[0].Kind != NoticeReply {
		t.Fatalf("only the waiter that stayed is answered: %s", noticesString(ns))
	}
	if s := tb.Snapshot(); s.WaiterOwnError != 1 || s.WaiterReply != 2 {
		t.Fatalf("own error %d reply %d", s.WaiterOwnError, s.WaiterReply)
	}
}

func TestNinetyNineLeaveAndTheLastGetsTheReply(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	for id := RequestID(2); id <= 100; id++ {
		tb.Join(0, id, read("", "SELECT 1"))
	}
	tb.Started(1, lead.Flight)
	owner := RequestID(1)
	for id := RequestID(1); id <= 99; id++ {
		l := tb.Leave(Instant(id), id, LeaveCancel)
		if l.Kind == LeftLast {
			t.Fatalf("leave %d cancelled the work with waiters left", id)
		}
		if l.Kind == LeftHandoff {
			owner = l.NewOwner
		}
	}
	if owner != 100 {
		t.Fatalf("owner %d, want the one that stayed", owner)
	}
	tb.Finish(200, lead.Flight, Result{})
	if ns := notices(t, tb); len(ns) != 0 {
		t.Fatalf("the owner is answered by the engine, not by a notice: %s", noticesString(ns))
	}
	s := tb.Snapshot()
	if s.WaiterReply != 1 || s.WaiterOwnError != 99 || s.Cancelled != 0 || s.Completed != 1 {
		t.Fatalf("reply %d own %d cancelled %d completed %d", s.WaiterReply, s.WaiterOwnError, s.Cancelled, s.Completed)
	}
}

func TestTheLastLeaverCancelsTheFlight(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	tb.Join(0, 2, read("", "SELECT 1"))
	tb.Started(1, lead.Flight)
	if l := tb.Leave(2, 1, LeaveGone); l.Kind != LeftHandoff || l.NewOwner != 2 {
		t.Fatalf("%+v", l)
	}
	if l := tb.Leave(3, 2, LeaveGone); l.Kind != LeftLast || l.Flight != lead.Flight {
		t.Fatalf("%+v", l)
	}
	if err := tb.Quiesce(); err != nil {
		t.Fatal(err)
	}
	if s := tb.Snapshot(); s.Cancelled != 1 || s.WaiterGone != 2 {
		t.Fatalf("cancelled %d gone %d", s.Cancelled, s.WaiterGone)
	}
}

func TestWaiterCapRefuses(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, MaxWaiters: 3})
	tb.Join(0, 1, read("", "SELECT 1"))
	tb.Join(0, 2, read("", "SELECT 1"))
	tb.Join(0, 3, read("", "SELECT 1"))
	if j := tb.Join(0, 4, read("", "SELECT 1")); j.Verdict != VerdictRefuse {
		t.Fatalf("the fourth waiter on a flight capped at 3: %v", j.Verdict)
	}
	if s := tb.Snapshot(); s.Overflows[CapWaiters] != 1 || s.Refused != 1 {
		t.Fatalf("%+v", s.Overflows)
	}
}

func TestReplyCapRefusesFollowersAndListsTheKeyForAWhile(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, MaxReplyBytes: 100, NoShareFor: Second})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	tb.Join(0, 2, read("", "SELECT 1"))
	tb.Started(0, lead.Flight)
	if !tb.Stored(1, lead.Flight, 100) {
		t.Fatal("a reply at the cap is still shared")
	}
	if tb.Stored(2, lead.Flight, 1) {
		t.Fatal("a reply past the cap must stop sharing")
	}
	ns := notices(t, tb)
	if len(ns) != 1 || ns[0].Kind != NoticeRefused || ns[0].Waiter != 2 {
		t.Fatalf("%s", noticesString(ns))
	}
	if j := tb.Join(3, 3, read("", "SELECT 1")); j.Verdict != VerdictPass {
		t.Fatalf("a listed key passes unshared, got %v", j.Verdict)
	}
	tb.Finish(4, lead.Flight, Result{})
	_ = notices(t, tb)
	if at := tb.Next(); at != 2+Second {
		t.Fatalf("the list wakes the engine at its expiry, got %d", at)
	}
	if j := tb.Join(2+Second, 4, read("", "SELECT 1")); j.Verdict != VerdictLead {
		t.Fatalf("at the expiry instant the key is shared again, got %v", j.Verdict)
	}
}

func TestNoShareListIsBoundedAndEvictsTheOldest(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, MaxReplyBytes: 1, NoShareMax: 2})
	for i, k := range []string{"SELECT 1", "SELECT 2", "SELECT 3"} {
		id := RequestID(i + 1)
		j := tb.Join(Instant(i), id, read("", k))
		tb.Stored(Instant(i), j.Flight, 2)
		tb.Finish(Instant(i), j.Flight, Result{})
		_ = notices(t, tb)
	}
	s := tb.Snapshot()
	if s.NoShare.Value != 2 || s.NoShareEvicted != 1 {
		t.Fatalf("listed %d evicted %d", s.NoShare.Value, s.NoShareEvicted)
	}
	if j := tb.Join(5, 9, read("", "SELECT 1")); j.Verdict != VerdictLead {
		t.Fatalf("the evicted key is shared again: %v", j.Verdict)
	}
}

func TestUnshareableEndRefusesFollowers(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeHinted})
	u := &work.Unit{Hinted: true, HasKey: true, Key: work.ShareKey{Text: []byte("/* mg:share */ BEGIN")}}
	lead := tb.Join(0, 1, u)
	tb.Join(0, 2, u)
	tb.Finish(1, lead.Flight, Result{Unshareable: true})
	ns := notices(t, tb)
	if len(ns) != 1 || ns[0].Kind != NoticeRefused {
		t.Fatalf("%s", noticesString(ns))
	}
	if s := tb.Snapshot(); s.Overflows[CapUnshareable] != 1 || s.NoShare.Value != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestFailedFlightSharesItsErrorOnce(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto})
	lead := tb.Join(0, 1, read("", "SELECT 1"))
	for id := RequestID(2); id <= 10; id++ {
		tb.Join(0, id, read("", "SELECT 1"))
	}
	tb.Finish(1, lead.Flight, Result{Failed: true})
	ns := notices(t, tb)
	if len(ns) != 9 {
		t.Fatalf("%d notices", len(ns))
	}
	for _, n := range ns {
		if n.Kind != NoticeError {
			t.Fatalf("%s", noticesString(ns))
		}
	}
	if s := tb.Snapshot(); s.SharedErrors != 1 || s.Failed != 1 || s.WaiterSharedError != 10 || s.Started != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestFlightTableCapPassesUnshared(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, MaxFlights: 1})
	tb.Join(0, 1, read("", "SELECT 1"))
	if j := tb.Join(0, 2, read("", "SELECT 2")); j.Verdict != VerdictPass {
		t.Fatalf("%v", j.Verdict)
	}
	if s := tb.Snapshot(); s.Overflows[CapFlights] != 1 || s.PassedFull != 1 {
		t.Fatalf("%+v", s.Overflows)
	}
}

func TestTenantScopeKeysDoNotMeet(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto})
	a := read("a", "SELECT 1")
	a.Key.Tenant = "a"
	b := read("b", "SELECT 1")
	b.Key.Tenant = "b"
	if tb.Join(0, 1, a).Verdict != VerdictLead || tb.Join(0, 2, b).Verdict != VerdictLead {
		t.Fatal("tenant-scoped keys of two tenants must start two flights")
	}
}

func TestHitsAreCountedPerTenantAndHotKeysAreOptIn(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, HotKeys: 2})
	tb.Join(0, 1, read("a", "SELECT 1"))
	tb.Join(0, 2, read("b", "SELECT 1"))
	tb.Join(0, 3, read("b", "SELECT 1"))
	tb.Join(0, 4, read("a", "SELECT 2"))
	s := tb.Snapshot()
	if len(s.PerTenant) != 1 || s.PerTenant[0] != (TenantHits{Tenant: "b", Hits: 2}) {
		t.Fatalf("%+v", s.PerTenant)
	}
	if len(s.HotKeys) != 2 || s.HotKeys[0].Count != 3 || s.HotKeys[0].Text != "SELECT 1" {
		t.Fatalf("%+v", s.HotKeys)
	}
	off := mustTable(t, Config{Mode: ModeAuto})
	off.Join(0, 1, read("a", "SELECT 1"))
	if len(off.Snapshot().HotKeys) != 0 {
		t.Fatal("per-key hotness is off by default")
	}
}

func TestConfigValidation(t *testing.T) {
	if err := (Config{}).WithDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{{MaxFlights: -1}, {MaxWaiters: -3}, {MaxReplyBytes: -1}, {NoShareFor: -1}, {NoShareMax: -2},
		{HotKeys: -1}, {HotKeys: MaxHotKeys + 1}, {MaxWaiters: 1}, {Mode: NumModes}, {Join: NumJoinModes},
		{MaxFlights: MaxFlightsLimit + 1}} {
		err := c.WithDefaults().Validate()
		if !errors.Is(err, ErrConfig) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	if _, err := New(Config{MaxWaiters: -1}); err == nil {
		t.Fatal("New must refuse a bad configuration")
	}
}

func TestShutdownEmptiesTheTable(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, MaxReplyBytes: 1})
	j := tb.Join(0, 1, read("", "SELECT 1"))
	tb.Join(0, 2, read("", "SELECT 1"))
	tb.Stored(0, j.Flight, 5)
	tb.Shutdown(1)
	_ = notices(t, tb)
	if err := tb.Quiesce(); err != nil {
		t.Fatal(err)
	}
	if tb.Next() != 0 {
		t.Fatal("no wake-up after shutdown")
	}
	if err := tb.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
}
