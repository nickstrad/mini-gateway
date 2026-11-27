package adv_test

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"testing"

	a "github.com/nickstrad/mini-gateway/internal/admission"
)

const seed int64 = 808271

func start(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./agent_work/adversarial/c08-admission", seed, t.Name())
}
func cfg(k a.RuleKind) a.Config {
	return a.Config{Rule: k, Capacity: 17, TenantCapacity: 7, MaxTenants: 3, Target: 1000, Interval: 10000, LIFOHold: 4000, Detail: true}
}
func queue(t *testing.T, c a.Config) *a.Queues {
	t.Helper()
	q, e := a.New(c)
	if e != nil {
		t.Fatal(e)
	}
	return q
}
func drain(q *a.Queues) []a.Shed {
	var out []a.Shed
	for {
		s, ok := q.TakeShed()
		if !ok {
			return out
		}
		out = append(out, s)
	}
}
func valid(t *testing.T, q *a.Queues) {
	t.Helper()
	if e := q.Check(); e != nil {
		t.Fatal(e)
	}
	if e := q.Snapshot().Check(); e != nil {
		t.Fatal(e)
	}
}

func TestAdv_1_ChurnBeyondCounterCap(t *testing.T) {
	start(t)
	c := cfg(a.RuleTimeout)
	c.Capacity = 4
	c.TenantCapacity = 1
	c.MaxTenants = 4
	q := queue(t, c)
	for i := 0; i < a.MaxTenantStats+8; i++ {
		q.Push(1, fmt.Sprint(i), int64(i))
		q.Pop(1)
	}
	for i := 0; i < 4; i++ {
		q.Push(1, fmt.Sprint(a.MaxTenantStats+100+i), int64(10000+i))
	}
	valid(t, q)
}
func TestAdv_1_PartialShedDrainMemory(t *testing.T) {
	start(t)
	c := cfg(a.RuleFIFO)
	c.Capacity = 3
	c.TenantCapacity = 3
	q := queue(t, c)
	for i := int64(1); i <= 6; i++ {
		q.Push(1, "t", i)
	}
	for i := int64(7); i < 10000; i++ {
		q.TakeShed()
		q.Push(1, "t", i)
	} // A consumer keeps a constant backlog of three sheds.
	v := reflect.ValueOf(q).Elem().FieldByName("pending")
	if v.Cap() > 4*(c.Capacity+2) {
		t.Fatalf("constant pending=%d but backing capacity grew to %d with queue cap=%d", q.Pending(), v.Cap(), c.Capacity)
	}
	valid(t, q)
}
func TestAdv_1_QuietRemovalAfterMixedExits(t *testing.T) {
	start(t)
	for _, k := range []a.RuleKind{a.RuleTimeout, a.RuleControlLaw} {
		q := queue(t, cfg(k))
		q.Push(1, "a", 1)
		q.Push(1, "b", 2)
		q.Push(1, "c", 3)
		q.Remove(2, 1)
		q.Expire(2, 2)
		q.Pop(2)
		q.Sweep(2 + q.Config().Quiet() + q.Config().LIFOHold)
		drain(q)
		if e := q.Quiesce(); e != nil {
			t.Fatal(k, e)
		}
	}
}

type loadResult struct {
	served, slots, maxWait int64
	sheds                  int
	snap                   a.Snapshot
}

// Independent discrete service scheduler: ten arrivals per service slot, phase configurable.
func load(t *testing.T, k a.RuleKind, service int64, capacity int, lifo a.LIFOMode, phase int64, tenants int) loadResult {
	t.Helper()
	c := cfg(k)
	c.Capacity = capacity
	c.TenantCapacity = capacity
	c.MaxTenants = tenants
	c.LIFO = lifo
	q := queue(t, c)
	r := loadResult{}
	enq := map[int64]int64{}
	var id int64
	step := service / 10
	if step < 1 {
		step = 1
	}
	for now := int64(1); now < 200*c.Interval; now += step {
		id++
		enq[id] = now
		q.Push(now, fmt.Sprint(id%int64(tenants)), id)
		r.sheds += len(drain(q))
		if (now-1)%service == phase {
			r.slots++
			x, ok := q.Pop(now)
			r.sheds += len(drain(q))
			if ok {
				r.served++
				r.maxWait = max(r.maxWait, now-enq[x])
			}
		}
	}
	r.snap = q.Snapshot()
	valid(t, q)
	return r
}
func TestAdv_2_PhaseLockedTimeout(t *testing.T) {
	start(t)
	r := load(t, a.RuleTimeout, 1000, 100, a.LIFOAdaptive, 0, 1)
	if r.maxWait >= 10000 || r.sheds == 0 {
		t.Fatalf("wait=%d sheds=%d", r.maxWait, r.sheds)
	}
}
func TestAdv_2_ManyTenantTimeout(t *testing.T) {
	start(t)
	r := load(t, a.RuleTimeout, 4000, 40, a.LIFOOff, 0, 7)
	if r.maxWait >= 10000 || r.served == 0 {
		t.Fatalf("wait=%d served=%d", r.maxWait, r.served)
	}
}
func TestAdv_2_FIFOControlHasGrowingWait(t *testing.T) {
	start(t)
	x := load(t, a.RuleFIFO, 1000, 100, a.LIFOOff, 0, 1)
	y := load(t, a.RuleTimeout, 1000, 100, a.LIFOOff, 0, 1)
	if x.maxWait <= y.maxWait || y.maxWait >= 10000 {
		t.Fatalf("FIFO wait=%d timeout=%d", x.maxWait, y.maxWait)
	}
}
func TestAdv_3_ControlLawLIFOOldTail(t *testing.T) {
	start(t)
	r := load(t, a.RuleControlLaw, 1000, 40, a.LIFOAdaptive, 0, 1)
	if r.maxWait >= 40*1000+10000 {
		t.Fatalf("served oldest tail wait=%d exceeds cap*lease+interval=%d", r.maxWait, 50000)
	}
}
func TestAdv_3_ControlLawCapacityOne(t *testing.T) {
	start(t)
	r := load(t, a.RuleControlLaw, 4000, 1, a.LIFOAdaptive, 0, 1)
	if r.maxWait >= 14000 || r.snap.Queued.Peak > 1 {
		t.Fatalf("wait=%d peak=%d", r.maxWait, r.snap.Queued.Peak)
	}
}
func TestAdv_3_ControlLawMultipleTenants(t *testing.T) {
	start(t)
	r := load(t, a.RuleControlLaw, 1000, 31, a.LIFOOff, 0, 3)
	if r.maxWait >= 41000 {
		t.Fatalf("wait=%d bound=41000", r.maxWait)
	}
}
func capacity(t *testing.T, k a.RuleKind, service int64, phase int64) {
	t.Helper()
	r := load(t, k, service, 32, a.LIFOAdaptive, phase, 1)
	if 100*r.served < 95*r.slots {
		t.Fatalf("10x load: served=%d service opportunities=%d (%0.2f%%)", r.served, r.slots, 100*float64(r.served)/float64(r.slots))
	}
}
func TestAdv_4_TargetEqualsService(t *testing.T) { start(t); capacity(t, a.RuleTimeout, 1000, 0) }
func TestAdv_4_LongServiceTimeout(t *testing.T)  { start(t); capacity(t, a.RuleTimeout, 20000, 0) }
func TestAdv_4_ControlLawOffset(t *testing.T)    { start(t); capacity(t, a.RuleControlLaw, 5000, 500) }

func burst(t *testing.T, how int) {
	t.Helper()
	for _, k := range []a.RuleKind{a.RuleTimeout, a.RuleControlLaw} {
		q := queue(t, cfg(k))
		q.Push(1, "t", 1)
		switch how {
		case 0:
			q.Remove(2, 1)
		case 1:
			q.Expire(2, 1)
		case 2:
			q.Pop(2)
		}
		at := int64(70000)
		q.Push(at, "t", 2)
		q.Sweep(at + 9999)
		if !q.Contains(2) || q.Pending() != 0 {
			t.Fatalf("%v burst lost before interval, contains=%v sheds=%v", k, q.Contains(2), drain(q))
		}
		id, ok := q.Pop(at + 9999)
		if !ok || id != 2 {
			t.Fatalf("burst pop=%d,%v", id, ok)
		}
	}
}
func TestAdv_5_IdleAfterCancellation(t *testing.T) { start(t); burst(t, 0) }
func TestAdv_5_IdleAfterDeadline(t *testing.T)     { start(t); burst(t, 1) }
func TestAdv_5_IdleAfterService(t *testing.T)      { start(t); burst(t, 2) }
func TestAdv_6_DeadlineVersusCancel(t *testing.T) {
	start(t)
	q := queue(t, cfg(a.RuleTimeout))
	q.Push(1, "t", 1)
	if q.Remove(10001, 1) {
		t.Fatal("cancellation won at exact rule deadline")
	}
	s := drain(q)
	if len(s) != 1 || s[0].Reason != a.ReasonRule {
		t.Fatalf("deadline sheds=%v", s)
	}
}
func TestAdv_6_UnpopPreservesDeadline(t *testing.T) {
	start(t)
	q := queue(t, cfg(a.RuleTimeout))
	q.Push(1, "t", 1)
	q.Pop(9001)
	if !q.Unpop() {
		t.Fatal("unpop failed")
	}
	if next := q.Next(); next > 10001 {
		t.Fatalf("restored original deadline=10001 but next sweep=%d", next)
	}
	q.Sweep(10001)
	if q.Contains(1) {
		t.Fatalf("restored request still queued at original interval deadline: next=%d", q.Next())
	}
}
func TestAdv_6_LargeTimeJumpSweepsAll(t *testing.T) {
	start(t)
	for _, k := range []a.RuleKind{a.RuleTimeout, a.RuleControlLaw} {
		q := queue(t, cfg(k))
		for i := int64(1); i <= 17; i++ {
			q.Push(1, fmt.Sprint(i%3), i)
		}
		drain(q)
		q.Sweep(10000000)
		if q.Len() != 0 {
			t.Fatalf("%v %d buried requests", k, q.Len())
		}
		drain(q)
		valid(t, q)
	}
}
func TestAdv_7_GlobalHeadTimestampTie(t *testing.T) {
	start(t)
	c := cfg(a.RuleFIFO)
	c.Capacity = 3
	c.TenantCapacity = 3
	q := queue(t, c)
	q.Push(1, "z", 10)
	q.Push(1, "a", 20)
	q.Push(1, "b", 30)
	q.Push(1, "a", 40)
	s := drain(q)
	if len(s) != 1 || s[0].ID != 10 || s[0].Reason != a.ReasonTotal || !q.Contains(40) {
		t.Fatalf("tie eviction=%v", s)
	}
}
func TestAdv_7_WrappedRingRemoval(t *testing.T) {
	start(t)
	c := cfg(a.RuleFIFO)
	c.Capacity = 4
	c.TenantCapacity = 4
	q := queue(t, c)
	for i := int64(1); i <= 4; i++ {
		q.Push(1, "t", i)
	}
	q.Pop(1)
	q.Pop(1)
	q.Push(1, "t", 5)
	q.Push(1, "t", 6)
	q.Remove(1, 4)
	q.Push(1, "t", 7)
	q.Push(1, "t", 8)
	s := drain(q)
	if len(s) != 1 || s[0].ID != 3 {
		t.Fatalf("wrapped head=%v", s)
	}
}
func TestAdv_7_OverflowWhileLIFO(t *testing.T) {
	start(t)
	c := cfg(a.RuleControlLaw)
	c.Capacity = 7
	c.TenantCapacity = 7
	q := queue(t, c)
	for i := int64(1); i <= 7; i++ {
		q.Push(1, "t", i)
	}
	q.Sweep(11001)
	drain(q)
	if q.Snapshot().LIFOTenants == 0 {
		t.Fatal("LIFO not exercised")
	}
	q.Push(11001, "t", 8)
	q.Push(11001, "t", 9)
	s := drain(q)
	if len(s) != 1 || s[0].ID != 2 || !q.Contains(9) {
		t.Fatalf("LIFO overflow=%v", s)
	}
}
func TestAdv_8_ConservationWithUnpopAndExpire(t *testing.T) {
	start(t)
	q := queue(t, cfg(a.RuleControlLaw))
	for i := int64(1); i <= 9; i++ {
		q.Push(1, "t", i)
		drain(q)
	}
	q.Pop(2)
	q.Unpop()
	q.Expire(3, 5)
	q.Remove(4, 6)
	q.Sweep(1000000)
	drain(q)
	valid(t, q)
}
func TestAdv_8_RefusedTenantCount(t *testing.T) {
	start(t)
	q := queue(t, cfg(a.RuleTimeout))
	for i := int64(1); i <= 20; i++ {
		q.Push(1, fmt.Sprint(i), i)
	}
	s := q.Snapshot()
	if s.Shed[a.ReasonTenants] != 17 {
		t.Fatalf("tenant refusals=%d want 17", s.Shed[a.ReasonTenants])
	}
	valid(t, q)
}
func TestAdv_8_FoldedStatsConservation(t *testing.T) {
	start(t)
	c := cfg(a.RuleFIFO)
	c.TenantCapacity = 1
	q := queue(t, c)
	for i := 0; i < a.MaxTenantStats+4; i++ {
		q.Push(1, fmt.Sprint(i), int64(i))
		q.Pop(1)
	}
	q.Push(2, "late-a", 10000)
	q.Push(2, "late-b", 10001)
	if e := q.Snapshot().Check(); e != nil {
		t.Fatalf("valid queues after stats fold violate reported conservation: %v", e)
	}
}
func hysteresis(t *testing.T, mode int) {
	t.Helper()
	c := cfg(a.RuleControlLaw)
	q := queue(t, c)
	for i := int64(1); i <= 7; i++ {
		q.Push(1, "t", i)
	}
	q.Sweep(11001)
	drain(q)
	for i := int64(1); i <= 7; i++ {
		q.Remove(11001, i)
	}
	switch mode {
	case 0:
		q.Sweep(15000)
		if q.Snapshot().LIFOTenants != 1 {
			t.Fatal("LIFO switched off before hold")
		}
	case 1:
		q.Sweep(15001)
		if q.Snapshot().LIFOTenants != 0 {
			t.Fatal("LIFO did not switch off at hold equality")
		}
	case 2:
		for at := int64(11002); at < 15001; at++ {
			q.Sweep(at)
		}
		s := q.Snapshot()
		if s.LIFOOn != 1 || s.LIFOOff != 0 {
			t.Fatalf("flapping without arrivals: on=%d off=%d", s.LIFOOn, s.LIFOOff)
		}
	}
}
func TestAdv_9_HoldMinusOne(t *testing.T)         { start(t); hysteresis(t, 0) }
func TestAdv_9_HoldEquality(t *testing.T)         { start(t); hysteresis(t, 1) }
func TestAdv_9_ObservationFrequency(t *testing.T) { start(t); hysteresis(t, 2) }
func sqrtCheck(t *testing.T, iv, c int64) {
	t.Helper()
	got := a.IntervalOverSqrt(iv, c)
	square := new(big.Int).Mul(big.NewInt(iv), big.NewInt(iv))
	want := new(big.Int).Sqrt(new(big.Int).Quo(square, big.NewInt(c))).Int64()
	if got != want {
		t.Fatalf("interval=%d count=%d result=%d want=%d", iv, c, got, want)
	}
}
func TestAdv_10_MaxIntervalAllCounts(t *testing.T) {
	start(t)
	for c := int64(1); c <= a.MaxDropCount; c++ {
		sqrtCheck(t, a.MaxInterval, c)
	}
}
func TestAdv_10_SeededLargeIntegerIntervals(t *testing.T) {
	start(t)
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < 4096; i++ {
		sqrtCheck(t, r.Int63n(math.MaxInt64-1)+1, r.Int63n(a.MaxDropCount)+1)
	}
}
func TestAdv_10_SquareBoundaryMonotonic(t *testing.T) {
	start(t)
	for n := int64(2); n <= 256; n++ {
		for _, c := range []int64{n*n - 1, n * n, n*n + 1} {
			if c <= a.MaxDropCount {
				sqrtCheck(t, 1000000007, c)
			}
		}
	}
}
func recoverQueue(t *testing.T, k a.RuleKind, how int) {
	t.Helper()
	q := queue(t, cfg(k))
	for i := int64(1); i <= 7; i++ {
		q.Push(1, "t", i)
	}
	q.Sweep(11001)
	drain(q)
	for i := int64(1); i <= 7; i++ {
		if how == 0 {
			q.Remove(11001, i)
		} else {
			q.Expire(11001, i)
		}
	}
	q.Sweep(35001)
	s := q.Snapshot()
	if s.LIFOTenants != 0 || s.Shedding != 0 || s.RuleStates[a.StateDropping] != 0 || s.RuleStates[a.StateStanding] != 0 {
		t.Fatalf("not recovered: LIFO=%d shedding=%d states=%v", s.LIFOTenants, s.Shedding, s.RuleStates)
	}
	q.Push(35001, "t", 100)
	id, ok := q.Pop(35001)
	if !ok || id != 100 {
		t.Fatalf("uncongested request failed: %d %v", id, ok)
	}
}
func TestAdv_11_RecoveryByCancellation(t *testing.T) { start(t); recoverQueue(t, a.RuleControlLaw, 0) }
func TestAdv_11_RecoveryByDeadline(t *testing.T)     { start(t); recoverQueue(t, a.RuleControlLaw, 1) }
func TestAdv_11_RecoveryTimeout(t *testing.T)        { start(t); recoverQueue(t, a.RuleTimeout, 0) }

// Mean arrival rate is exactly ten times service, but arrivals are clustered.
// This tests the unqualified capacity claim, not the spread-arrival benchmark.
func TestAdv_4_BurstyTenfoldLoad(t *testing.T) {
	start(t)
	c := cfg(a.RuleTimeout)
	c.Capacity = 128
	c.TenantCapacity = 128
	q := queue(t, c)
	var id, served, slots int64
	for now := int64(1); now < 1000001; now += 1000 {
		if (now-1)%100000 == 0 {
			for range 1000 {
				id++
				q.Push(now, "t", id)
				drain(q)
			}
		}
		slots++
		if _, ok := q.Pop(now); ok {
			served++
		}
		drain(q)
	}
	if id != 10*slots {
		t.Fatalf("invalid offered load: arrivals=%d slots=%d", id, slots)
	}
	if 100*served < 95*slots {
		t.Fatalf("10x mean load with clustered arrivals: served=%d capacity=%d, rule sheds=%d (claim requires >=95%%)", served, slots, q.Snapshot().Shed[a.ReasonRule])
	}
}
