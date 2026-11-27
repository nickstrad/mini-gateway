package admission

import (
	"fmt"
	"testing"
)

func churnPastTheStatsCap(t *testing.T, q *Queues, first string) Time {
	t.Helper()
	now := Time(1)
	q.Push(now, first, 1)
	q.Pop(now)
	for i := range MaxTenantStats {
		now++
		id := int64(10 + i)
		q.Push(now, fmt.Sprintf("churn-%d", i), id)
		q.Pop(now)
	}
	drainAll(q)
	return now
}

func TestFoldedOtherRowIsBoundedByTheTotalNotOneTenant(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, Capacity: 64, TenantCapacity: 1, MaxTenants: 8, Detail: true})
	now := churnPastTheStatsCap(t, q, "first")
	for i := range 4 {
		if !q.Push(now, fmt.Sprintf("late-%d", i), int64(100000+i)) {
			t.Fatalf("push %d refused", i)
		}
	}
	if err := q.Check(); err != nil {
		t.Fatalf("four live one-slot queues folded into %q: %v", OtherTenant, err)
	}
	if err := q.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	other := 0
	for _, r := range q.Snapshot().PerTenant {
		if r.Tenant == OtherTenant {
			other++
			if r.Depth != 4 {
				t.Fatalf("the fold row holds depth %d, want 4", r.Depth)
			}
		}
	}
	if other != 1 {
		t.Fatalf("%d rows named %q", other, OtherTenant)
	}
}

func TestARealTenantNamedOtherSharesTheFoldRow(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, Capacity: 64, TenantCapacity: 2, MaxTenants: 8, Detail: true})
	now := churnPastTheStatsCap(t, q, OtherTenant)
	q.Push(now, "late", 99999)
	q.Push(now, OtherTenant, 99998)
	s := q.Snapshot()
	rows := 0
	for _, r := range s.PerTenant {
		if r.Tenant == OtherTenant {
			rows++
			if r.Pushes != 4 || r.Depth != 2 {
				t.Fatalf("the shared row: %+v", r)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("%d rows named %q, want one", rows, OtherTenant)
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingShedStorageStaysFixedUnderAPartialDrain(t *testing.T) {
	cfg := Config{Rule: RuleFIFO, Capacity: 3, TenantCapacity: 3}
	q := mustNew(t, cfg)
	want := cap(q.pending)
	if want != pendingCap(q.cfg) {
		t.Fatalf("pending storage %d, want %d", want, pendingCap(q.cfg))
	}
	for i := int64(1); i <= 6; i++ {
		q.Push(1, "t", i)
	}
	for i := int64(7); i < 10000; i++ {
		q.TakeShed()
		q.Push(1, "t", i)
		if cap(q.pending) != want || len(q.pending) > want {
			t.Fatalf("after push %d: pending %d, storage %d/%d, want fixed %d", i, q.Pending(), len(q.pending), cap(q.pending), want)
		}
	}
	if q.Pending() != 3 {
		t.Fatalf("the backlog is %d, the test meant three", q.Pending())
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingShedStorageHoldsTheWorstUndrainedBacklog(t *testing.T) {
	cfg := Config{Rule: RuleTimeout, Capacity: 4, TenantCapacity: 4, Target: Millisecond, Interval: 10 * Millisecond}
	q := mustNew(t, cfg)
	want := cap(q.pending)
	id := int64(0)
	for now := Time(0); now < Second; now += Millisecond {
		id++
		q.Push(now, "t", id)
		if cap(q.pending) != want {
			t.Fatalf("pending storage grew to %d from %d with %d sheds untaken", cap(q.pending), want, q.Pending())
		}
	}
	if q.Pending() < pendingRefuseAt(q.cfg) {
		t.Fatalf("only %d sheds untaken; the test never reached the refusal threshold %d", q.Pending(), pendingRefuseAt(q.cfg))
	}
}

func TestUnpopRestoresTheRuleStateAndTheDeadline(t *testing.T) {
	for _, rule := range []RuleKind{RuleTimeout, RuleControlLaw} {
		cfg := Config{Rule: rule, Target: 1000, Interval: 10000, LIFOHold: 4000, Capacity: 17, TenantCapacity: 7, MaxTenants: 3}
		q := mustNew(t, cfg)
		q.Push(1, "t", 1)
		q.Sweep(1500)
		before := q.Next()
		if _, ok := q.Pop(1500); !ok {
			t.Fatal("nothing popped")
		}
		if !q.Unpop() {
			t.Fatalf("%v: unpop refused", rule)
		}
		if got := q.Next(); got != before {
			t.Fatalf("%v: the deadline moved from %d to %d across a pop and unpop", rule, before, got)
		}
		q.Sweep(before)
		if q.Contains(1) {
			t.Fatalf("%v: the returned request was still queued at its deadline %d", rule, before)
		}
		if err := q.Check(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAOneSlotQueueUnderFloodReachesItsOverloadState(t *testing.T) {
	for _, rule := range []RuleKind{RuleTimeout} {
		q := mustNew(t, Config{Rule: rule, LIFO: LIFOOff, Capacity: 1, TenantCapacity: 1})
		id := int64(0)
		reached := false
		for now := Time(0); now < 4*DefaultInterval; now += Millisecond {
			id++
			q.Push(now, "a", id)
			drainAll(q)
			if q.Snapshot().RuleStates[StateNormal] == 0 {
				reached = true
			}
		}
		if !reached {
			t.Fatalf("%v: a one-slot queue overflowing every millisecond never left the normal state", rule)
		}
	}
}

func TestOverflowOfTheArrivingTenantByTheTotalCapKeepsItsRuleState(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, LIFO: LIFOOff, Capacity: 2, TenantCapacity: 2})
	q.Push(0, "a", 1)
	q.Push(1, "b", 2)
	q.Push(DefaultInterval/2, "a", 3)
	drainAll(q)
	if r := q.byName["a"].rule.(*TimeoutRule); r.LastEmpty != 0 {
		t.Fatalf("tenant a never emptied but its last-empty moved to %d", r.LastEmpty)
	}
}

func TestTheLoadGaugeFirstWindowIsOneInterval(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, LIFO: LIFOOff})
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Pop(Millisecond)
	q.Pop(DefaultTarget * 2)
	if got, want := q.Snapshot().Load, LoadFromDelay(Millisecond, DefaultTarget); got != want {
		t.Fatalf("load %d after two serves inside the first interval, want the minimum's %d", got, want)
	}
}

func TestTimeoutDeadlineAtEqualityWhileStanding(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, LIFO: LIFOOff})
	q.Push(0, "a", 1)
	q.Push(DefaultInterval-DefaultTarget, "a", 2)
	q.Sweep(DefaultInterval - 1)
	if s := drainAll(q); len(s) != 0 {
		t.Fatalf("shed before the interval: %v", s)
	}
	q.Sweep(DefaultInterval)
	s := drainAll(q)
	if len(s) != 2 || s[1].ID != 2 || s[1].Wait != DefaultTarget {
		t.Fatalf("an item aged exactly the short target while standing must be shed: %v", s)
	}
}

func TestControlLawAtEqualityOfTargetAndFirstAbove(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleControlLaw, LIFO: LIFOOff})
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Sweep(DefaultTarget)
	r := q.byName["a"].rule.(*ControlLaw)
	if r.FirstAbove != DefaultTarget+DefaultInterval {
		t.Fatalf("a sojourn equal to the target must start the interval: first-above %d", r.FirstAbove)
	}
	q.Sweep(r.FirstAbove - 1)
	if s := drainAll(q); len(s) != 0 {
		t.Fatalf("dropped before first-above: %v", s)
	}
	q.Sweep(DefaultTarget + DefaultInterval)
	if s := drainAll(q); len(s) != 1 || !r.Dropping {
		t.Fatalf("at exactly first-above the head must be dropped: %v dropping %v", s, r.Dropping)
	}
}
