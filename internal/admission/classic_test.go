package admission

import (
	"errors"
	"strings"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Queues {
	t.Helper()
	q, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func drainAll(q *Queues) []Shed {
	var out []Shed
	for {
		s, ok := q.TakeShed()
		if !ok {
			return out
		}
		out = append(out, s)
	}
}

type serveResult struct {
	served []Time
	shed   []Shed
}

func burstAfterIdle(t *testing.T, cfg Config, idle Time, burst int, service Time) serveResult {
	q := mustNew(t, cfg)
	now := idle
	for i := range burst {
		if !q.Push(now, "a", int64(i+1)) {
			t.Fatalf("push %d refused", i)
		}
	}
	var r serveResult
	start := now
	for q.Len() > 0 {
		if _, ok := q.Pop(now); ok {
			r.served = append(r.served, now-start)
		}
		r.shed = append(r.shed, drainAll(q)...)
		now += service
	}
	r.shed = append(r.shed, drainAll(q)...)
	return r
}

func TestFirstBurstAfterIdleGetsTheIntervalNotTheShortTarget(t *testing.T) {
	for _, rule := range []RuleKind{RuleTimeout, RuleControlLaw} {
		cfg := Config{Rule: rule, LIFO: LIFOOff}
		r := burstAfterIdle(t, cfg, 2*Second, 10, 9*Millisecond)
		if len(r.shed) != 0 || len(r.served) != 10 {
			t.Fatalf("%v: first burst after idle: %d served, %d shed %v", rule, len(r.served), len(r.shed), r.shed)
		}
		if last := r.served[len(r.served)-1]; last <= DefaultTarget {
			t.Fatalf("%v: the burst never waited past the short target (%d), the test proves nothing", rule, last)
		}
	}
}

func TestCanaryFirstBurstBugShedsTheFirstBurst(t *testing.T) {
	cfg := Config{Rule: RuleTimeout, LIFO: LIFOOff, Plant: PlantFirstBurst}
	r := burstAfterIdle(t, cfg, 2*Second, 10, 9*Millisecond)
	if len(r.shed) == 0 {
		t.Fatal("with lastEmpty updated only at dequeue the first burst must get the short target and be shed")
	}
}

func TestDeadlineAtEqualityHasExpired(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, LIFO: LIFOOff})
	q.Push(0, "a", 1)
	q.Sweep(DefaultInterval - 1)
	if len(drainAll(q)) != 0 {
		t.Fatal("shed before the interval")
	}
	q.Sweep(DefaultInterval)
	s := drainAll(q)
	if len(s) != 1 || s[0].Wait != DefaultInterval || s[0].Reason != ReasonRule {
		t.Fatalf("at exactly the interval: %v", s)
	}
}

func overloadLIFO(t *testing.T, plant Plant) (maxQueuedAge, maxServedWait Time, s Snapshot) {
	cfg := Config{Rule: RuleTimeout, Capacity: 4096, TenantCapacity: 4096, Plant: plant}
	q := mustNew(t, cfg)
	id := int64(0)
	for now := Time(0); now < 3*Second; now += Millisecond {
		for range 3 {
			id++
			q.Push(now, "a", id)
		}
		if now%(2*Millisecond) == 0 {
			if _, ok := q.Pop(now); ok {
				maxServedWait = max(maxServedWait, now-q.lastPop.e.enq)
			}
		}
		drainAll(q)
		for _, tq := range q.list {
			maxQueuedAge = max(maxQueuedAge, tq.oldestAge(now))
		}
	}
	return maxQueuedAge, maxServedWait, q.Snapshot()
}

func TestLIFOWithoutTheHeadSweepBuriesStaleRequests(t *testing.T) {
	age, wait, s := overloadLIFO(t, 0)
	if s.LIFOOn == 0 || s.Shed[ReasonRule] == 0 {
		t.Fatalf("the overload never switched to LIFO or shed: %+v", s.Shed)
	}
	if age >= DefaultInterval || wait >= DefaultInterval {
		t.Fatalf("with the head sweep an item lingered %d or was served after %d", age, wait)
	}
	buried, _, _ := overloadLIFO(t, PlantNoHeadSweep)
	if buried < 10*DefaultInterval {
		t.Fatalf("without the head sweep the oldest queued request is only %d old; LIFO should bury it", buried)
	}
}

func clDriveOverload(q *Queues, from, to Time, id *int64) {
	for now := from; now < to; now += Millisecond {
		*id++
		q.Push(now, "a", *id)
		*id++
		q.Push(now, "a", *id)
		q.Pop(now)
		drainAll(q)
	}
}

func clRule(q *Queues) *ControlLaw { return q.byName["a"].rule.(*ControlLaw) }

func TestControlLawCountAndNextDropAcrossLeavingAndReentering(t *testing.T) {
	cfg := Config{Rule: RuleControlLaw, LIFO: LIFOOff, Capacity: 4096, TenantCapacity: 4096}
	q := mustNew(t, cfg)
	id := int64(0)
	clDriveOverload(q, 0, Second, &id)
	r := clRule(q)
	if !r.Dropping || r.Count < 3 {
		t.Fatalf("not dropping after a second of overload: %+v", *r)
	}
	countBefore, lastCount := r.Count, r.LastCount
	now := Second
	for q.Len() > 0 {
		q.Pop(now)
		drainAll(q)
	}
	if r.Dropping {
		t.Fatal("still dropping with an empty queue")
	}
	reenter := now + 50*Millisecond
	q.Push(reenter, "a", id+1)
	q.Push(reenter, "a", id+2)
	firstAbove := reenter + DefaultTarget
	q.Sweep(firstAbove)
	if r.FirstAbove != firstAbove+DefaultInterval {
		t.Fatalf("first above %d, want %d", r.FirstAbove, firstAbove+DefaultInterval)
	}
	q.Sweep(r.FirstAbove)
	if !r.Dropping {
		t.Fatal("did not re-enter dropping")
	}
	want := countBefore - lastCount
	if want <= 1 {
		want = 1
	}
	if r.Count != want || r.DropNext != Law(firstAbove+DefaultInterval, DefaultInterval, want) {
		t.Fatalf("re-entry within 16 intervals: count %d drop next %d, want %d and %d", r.Count, r.DropNext, want, Law(firstAbove+DefaultInterval, DefaultInterval, want))
	}
	drainAll(q)
	for _, tq := range q.list {
		for !tq.empty() {
			q.Remove(r.FirstAbove, tq.ring.front().id)
		}
	}
	if r.Dropping {
		t.Fatal("still dropping after the queue was emptied")
	}
	late := r.DropNext + 17*DefaultInterval
	q.Push(late, "a", id+10)
	q.Push(late, "a", id+11)
	r = clRule(q)
	q.Sweep(late + DefaultTarget)
	q.Sweep(late + DefaultTarget + DefaultInterval)
	if r.Count != 1 || r.DropNext != Law(late+DefaultTarget+DefaultInterval, DefaultInterval, 1) {
		t.Fatalf("re-entry after 16 intervals: count %d, want 1", r.Count)
	}
}

func TestIntervalOverSqrtMatchesTheReferenceForEveryCountUpToTheMaximum(t *testing.T) {
	intervals := []Time{1_001, 7 * Millisecond, DefaultInterval, 999_999_937, MaxInterval}
	step := int64(1)
	if testing.Short() {
		step = 97
	}
	for _, iv := range intervals {
		for c := int64(1); c <= MaxDropCount; c += step {
			if got, want := Law(0, iv, c), refLaw(0, iv, c); got != want {
				t.Fatalf("interval %d count %d: %d, reference %d", iv, c, got, want)
			}
		}
	}
	if Law(10, DefaultInterval, 4) != 10+DefaultInterval/2 || Law(0, DefaultInterval, 1) != DefaultInterval {
		t.Fatal("exact squares")
	}
}

func TestOverflowShedsTheHeadNotTheArrival(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, Capacity: 8, TenantCapacity: 3})
	for i := int64(1); i <= 4; i++ {
		if !q.Push(Time(i), "a", i) {
			t.Fatal("arrival refused")
		}
	}
	s := drainAll(q)
	if len(s) != 1 || s[0].ID != 1 || s[0].Reason != ReasonOverflow {
		t.Fatalf("tenant overflow shed %v, want the head (1)", s)
	}
	for i := int64(5); i <= 10; i++ {
		q.Push(Time(i), "b"+string(rune('0'+i)), i)
	}
	s = drainAll(q)
	if len(s) != 1 || s[0].ID != 2 || s[0].Reason != ReasonTotal {
		t.Fatalf("total overflow shed %v, want the oldest (2)", s)
	}
	if q.Len() != 8 {
		t.Fatalf("len %d", q.Len())
	}
}

func TestTenantCapShedsTheArrivalOnlyWhenNoQueueIsEmpty(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, MaxTenants: 2})
	q.Push(1, "a", 1)
	q.Push(1, "b", 2)
	if q.Push(1, "c", 3) {
		t.Fatal("a third tenant queue was created over the cap")
	}
	if s := q.Snapshot(); s.Shed[ReasonTenants] != 1 || s.Tenants.Peak != 2 {
		t.Fatalf("%+v", s)
	}
	q.Pop(2)
	if !q.Push(3, "c", 4) {
		t.Fatal("an empty tenant queue was not evicted for a new tenant")
	}
	if s := q.Snapshot(); s.TenantsEvicted != 1 {
		t.Fatalf("evicted %d", s.TenantsEvicted)
	}
}

func TestQuietTenantQueueIsRemovedAndItsRingReused(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleControlLaw, MaxTenants: 4})
	for round := range 50 {
		base := Time(round) * Second
		for k := range 4 {
			name := "t" + string(rune('a'+(round*4+k)%26))
			q.Push(base, name, int64(round*4+k+1))
		}
		for q.Len() > 0 {
			q.Pop(base + Millisecond)
		}
		q.Sweep(base + Millisecond + q.cfg.Quiet())
		if q.Tenants() != 0 {
			t.Fatalf("round %d: %d tenant queues left after the quiet time", round, q.Tenants())
		}
		if len(q.free) > q.cfg.MaxTenants {
			t.Fatalf("%d rings allocated, cap %d", len(q.free), q.cfg.MaxTenants)
		}
	}
	if err := q.Quiesce(); err != nil {
		t.Fatal(err)
	}
	if s := q.Snapshot(); s.TenantsRemoved != 200 || s.RuleStates[StateNormal] != 0 {
		t.Fatalf("%+v", s)
	}
}

func hysteresisRun(t *testing.T, plant Plant) Snapshot {
	cfg := Config{Rule: RuleTimeout, Plant: plant, LIFOHold: 300 * Millisecond}
	q := mustNew(t, cfg)
	id := int64(0)
	for cycle := range 40 {
		base := Time(cycle) * 2 * DefaultInterval
		for k := range 12 {
			id++
			q.Push(base+Time(k)*10*Millisecond, "a", id)
			if k%3 == 0 {
				q.Pop(base + Time(k)*10*Millisecond)
			}
			drainAll(q)
		}
		for q.Len() > 0 {
			q.Pop(base + 120*Millisecond)
			drainAll(q)
		}
	}
	return q.Snapshot()
}

func TestLIFOHysteresisDoesNotFlapAtTheThreshold(t *testing.T) {
	s := hysteresisRun(t, 0)
	if s.LIFOOn != 1 || s.LIFOOff != 0 {
		t.Fatalf("load sitting at the threshold switched LIFO on %d and off %d times, want 1 and 0", s.LIFOOn, s.LIFOOff)
	}
	planted := hysteresisRun(t, PlantNoLIFOHysteresis)
	if planted.LIFOOn < 10 {
		t.Fatalf("canary: without hysteresis LIFO switched on only %d times", planted.LIFOOn)
	}
}

func TestLIFOSwitchesOffOnlyAfterTheHold(t *testing.T) {
	cfg := Config{Rule: RuleTimeout, LIFOHold: 500 * Millisecond}
	q := mustNew(t, cfg)
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Sweep(DefaultInterval)
	if !q.byName["a"].lifo {
		t.Fatal("standing queue did not switch LIFO on")
	}
	drainAll(q)
	at := DefaultInterval + 500*Millisecond
	q.Sweep(at - 1)
	if !q.byName["a"].lifo {
		t.Fatal("LIFO off before the hold")
	}
	q.Sweep(at)
	if q.byName["a"].lifo {
		t.Fatal("LIFO still on at the hold")
	}
}

func TestComparisonArmsAreNotProductRules(t *testing.T) {
	for _, s := range []string{"fifo", "folly", "plain-fifo", ""} {
		if _, ok := ParseProductRule(s); ok {
			t.Fatalf("%q parsed as a product rule", s)
		}
	}
	for _, k := range []RuleKind{RuleTimeout, RuleControlLaw} {
		if got, ok := ParseProductRule(k.String()); !ok || got != k || k.ComparisonArm() {
			t.Fatalf("%v", k)
		}
	}
}

func TestConfigFailsClosedNamingTheKey(t *testing.T) {
	cases := map[string]Config{
		"queue.rule":            {Rule: 9},
		"queue.interval":        {Interval: -1},
		"queue.target":          {Target: DefaultInterval},
		"queue.capacity":        {Capacity: -1},
		"queue.tenant_capacity": {Capacity: 4, TenantCapacity: 5},
		"queue.max_tenants":     {MaxTenants: -2},
		"queue.lifo":            {LIFO: 7},
		"queue.lifo_hold":       {LIFOHold: -1},
	}
	for key, c := range cases {
		_, err := New(c)
		if err == nil || !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), key+":") {
			t.Errorf("%s: %v", key, err)
		}
	}
	q := mustNew(t, Config{})
	if c := q.Config(); c.Rule != RuleTimeout || c.Capacity != DefaultCapacity || c.TenantCapacity != DefaultTenantCapacity ||
		c.MaxTenants != DefaultMaxTenants || c.LIFO != LIFOAdaptive || c.LIFOHold != DefaultLIFOHold || c.Target != DefaultTarget {
		t.Fatalf("zero means the default: %+v", c)
	}
}

func TestFollyDetectorWithLIFOIsBlind(t *testing.T) {
	run := func(lifo LIFOMode) Snapshot {
		q := mustNew(t, Config{Rule: RuleFolly, LIFO: lifo, Capacity: 1 << 16, TenantCapacity: 1 << 16})
		id := int64(0)
		for now := Time(0); now < 2*Second; now += Millisecond {
			for range 2 {
				id++
				q.Push(now, "a", id)
			}
			q.Pop(now)
			drainAll(q)
		}
		return q.Snapshot()
	}
	fifo, lifo := run(LIFOOff), run(LIFOAdaptive)
	if fifo.Shed[ReasonAtPop] == 0 {
		t.Fatal("folly with FIFO dequeue never shed under 2x overload")
	}
	if lifo.Queued.Value < fifo.Queued.Value {
		t.Fatalf("folly with LIFO should not bound the queue better than FIFO: %d vs %d", lifo.Queued.Value, fifo.Queued.Value)
	}
}

func TestSnapshotConservesAndCountersMove(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, Detail: true, TenantCapacity: 2, Capacity: 3})
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Push(0, "a", 3)
	q.Push(0, "b", 4)
	q.Push(0, "c", 5)
	q.Pop(Millisecond)
	q.Remove(Millisecond, 4)
	q.Expire(Millisecond, 5)
	q.Sweep(Second)
	drainAll(q)
	s := q.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if s.Pushes != 5 || s.Served != 1 || s.Removed != 1 || s.Shed[ReasonOverflow] != 1 || s.Shed[ReasonDeadline] != 1 ||
		s.Shed[ReasonRule] == 0 && s.Queued.Value == 0 && s.ShedTotal()+s.Served+s.Removed != 5 {
		t.Fatalf("%+v", s)
	}
	if len(s.PerTenant) != 3 || s.PerTenant[0].Tenant != "a" || s.PerTenant[0].QueueWait.Count == 0 {
		t.Fatalf("per tenant %+v", s.PerTenant)
	}
	bad := s
	bad.Served++
	if bad.Check() == nil {
		t.Fatal("broken conservation not caught")
	}
}
