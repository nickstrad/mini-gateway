package admission

import (
	"errors"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

const (
	fairPlantEnv   = "MG_FUZZ_PLANT"
	fairPlantOrder = "fairness-unpop-turn"
	fairRuns       = 400
)

func fairRules() []RuleKind { return []RuleKind{RuleFIFO, RuleTimeout, RuleControlLaw} }

func randomFairOps(r *rand.Rand, n int) []fairOp {
	b := make([]byte, n*fairOpBytes)
	for i := range b {
		b[i] = byte(r.IntN(256))
	}
	return decodeFairOps(b)
}

func fairTrail(w *fairWorld) string {
	if w == nil {
		return ""
	}
	return strings.Join(w.log, "\n")
}

func runFairProperty(t *testing.T, plant FairPlant, runs int) error {
	t.Helper()
	r := rand.New(rand.NewPCG(9, 9))
	for i := range runs {
		ops := randomFairOps(r, fairMaxOps)
		for _, rule := range fairRules() {
			if w, err := runFairOps(rule, i, plant, ops); err != nil {
				return errors.New(err.Error() + "\n" + rule.String() + " variant " + string(rune('0'+i%6)) + "\n" + fairTrail(w))
			}
		}
	}
	return nil
}

func TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim(t *testing.T) {
	runs := fairRuns
	if testing.Short() {
		runs = 100
	}
	if err := runFairProperty(t, 0, runs); err != nil {
		t.Fatal(err)
	}
}

func TestPropertyRunsReachEveryFairnessSituation(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 9))
	var heldShare, heldLimit, atShare, saturated, unpops, churn int64
	for i := range 100 {
		ops := randomFairOps(r, fairMaxOps)
		w, err := runFairOps(RuleFIFO, i, 0, ops)
		if err != nil {
			t.Fatal(err)
		}
		s := w.q.Snapshot()
		heldShare += s.Fair.HeldShare
		heldLimit += s.Fair.HeldLimit
		saturated += s.Fair.Saturated
		unpops += s.Unpops
		churn += s.TenantsRemoved
		for _, row := range s.PerTenant {
			atShare += row.AtShare
		}
	}
	for name, v := range map[string]int64{"held at a share": heldShare, "held at the limit": heldLimit, "time at share": atShare,
		"saturated time": saturated, "unpops": unpops, "tenant queues removed": churn} {
		if v == 0 {
			t.Errorf("the random operations never reached %s", name)
		}
	}
}

func TestCanaryNoShareBoundBreaksTheShareClaim(t *testing.T) {
	err := runFairProperty(t, FairPlantNoShareBound, fairRuns)
	if err == nil || !strings.Contains(err.Error(), "claim share") {
		t.Fatalf("the planted missing share bound was not caught by the share claim: %v", err)
	}
}

func TestCanaryNoRoundRobinStepIsCaught(t *testing.T) {
	err := runFairProperty(t, FairPlantNoRoundRobinStep, fairRuns)
	if err == nil {
		t.Fatal("the planted missing round-robin step passed")
	}
	if !strings.Contains(err.Error(), "the model gives") && !strings.Contains(err.Error(), "claim no starvation") && !strings.Contains(err.Error(), "rounds") {
		t.Fatalf("caught by something other than the order or the rounds: %v", err)
	}
}

func TestCanaryUnpopKeepingTheTurnIsCaught(t *testing.T) {
	err := runFairProperty(t, FairPlantUnpopKeepsTurn, fairRuns)
	if err == nil {
		t.Fatal("the planted unpop that keeps the turn passed")
	}
	if !strings.Contains(err.Error(), "the model gives") && !strings.Contains(err.Error(), "rounds") {
		t.Fatalf("caught by something other than the order or the rounds: %v", err)
	}
}

func fairCore(t *testing.T, limit int, weights ...TenantWeight) (*Queues, *Fair) {
	t.Helper()
	q, err := New(Config{Rule: RuleFIFO, Capacity: 64, TenantCapacity: 32, MaxTenants: 16, LIFO: LIFOOff})
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewFair(FairConfig{Limit: limit, MaxLeases: 16, Weights: weights})
	if err != nil {
		t.Fatal(err)
	}
	q.SetDispatcher(f)
	return q, f
}

func mustPush(t *testing.T, q *Queues, now Time, tenant string, id int64) {
	t.Helper()
	if !q.Push(now, tenant, id) {
		t.Fatalf("push %d refused", id)
	}
}

func lendNext(t *testing.T, q *Queues, now Time, owner map[int64]string) (string, bool) {
	t.Helper()
	id, ok := q.Pop(now)
	if !ok {
		return "", false
	}
	if !q.LeaseStart(now, owner[id]) {
		t.Fatal("lease start refused")
	}
	return owner[id], true
}

func TestTheShareBindsExactlyWhenALendWouldReachTheLimit(t *testing.T) {
	q, _ := fairCore(t, 4)
	owner := map[int64]string{}
	for i := int64(1); i <= 6; i++ {
		owner[i] = "a"
		mustPush(t, q, 1, "a", i)
	}
	for range 4 {
		if _, ok := lendNext(t, q, 1, owner); !ok {
			t.Fatal("a lone tenant must reach the limit")
		}
	}
	owner[7] = "b"
	mustPush(t, q, 2, "b", 7)
	if _, ok := q.Pop(2); ok {
		t.Fatal("lent past the limit")
	}
	q.LeaseEnd(3, "a")
	q.LeaseEnd(3, "a")
	if name, _ := lendNext(t, q, 3, owner); name != "b" {
		t.Fatalf("below the limit by two, the turn goes to the newcomer b, got %q", name)
	}
	if got := q.Fair().Share("a"); got != 2 {
		t.Fatalf("share of a with two active tenants at limit 4: %d", got)
	}
	if _, ok := q.Pop(3); ok {
		t.Fatal("a has 2 in flight, total 3: lending a reaches the limit and puts a at 3, above its share 2")
	}
	if got := q.Snapshot().Fair.HeldShare; got != 1 {
		t.Fatalf("held at a share: %d", got)
	}
	q.LeaseEnd(4, "b")
	if name, ok := lendNext(t, q, 4, owner); !ok || name != "a" {
		t.Fatalf("b left: a's share is 4 again and it must be lent, got %q %v", name, ok)
	}
}

func TestBelowTheLimitTheShareDoesNotBind(t *testing.T) {
	q, _ := fairCore(t, 4)
	owner := map[int64]string{1: "a", 2: "a", 3: "a", 4: "b"}
	for i := int64(1); i <= 3; i++ {
		mustPush(t, q, 1, "a", i)
	}
	mustPush(t, q, 1, "b", 4)
	got := []string{}
	for range 3 {
		name, ok := lendNext(t, q, 1, owner)
		if !ok {
			t.Fatal("work-conserving: nothing lent below the limit")
		}
		got = append(got, name)
	}
	if strings.Join(got, ",") != "a,b,a" {
		t.Fatalf("lends %v, want a,b,a (oldest first, then turns)", got)
	}
	if q.TenantInFlight("a") != 2 {
		t.Fatalf("a in flight %d: above its share of 2 is allowed below the limit", q.TenantInFlight("a"))
	}
	if _, ok := q.Pop(1); ok {
		t.Fatal("the last slot must go to a tenant below its share, and none waits")
	}
}

func TestALeaseEndingAtTheInstantOfAPickFreesItsShareFirst(t *testing.T) {
	q, _ := fairCore(t, 2)
	owner := map[int64]string{1: "a", 2: "b", 3: "a"}
	mustPush(t, q, 1, "a", 1)
	mustPush(t, q, 1, "b", 2)
	lendNext(t, q, 1, owner)
	lendNext(t, q, 1, owner)
	mustPush(t, q, 5, "a", 3)
	if _, ok := q.Pop(5); ok {
		t.Fatal("at the limit")
	}
	q.LeaseEnd(10, "b")
	if _, ok := q.Pop(10); !ok {
		t.Fatal("b's lease ended at this instant: b is no longer active and a's share is 2")
	}
}

func TestTimeAtShareAndSaturationAreMeasuredToTheInstant(t *testing.T) {
	q, _ := fairCore(t, 2)
	owner := map[int64]string{1: "a", 2: "b", 3: "a"}
	mustPush(t, q, 100, "a", 1)
	mustPush(t, q, 100, "b", 2)
	lendNext(t, q, 100, owner)
	lendNext(t, q, 100, owner)
	q.LeaseEnd(130, "b")
	q.LeaseEnd(170, "a")
	s := q.Snapshot()
	row := map[string]TenantSnapshot{}
	for _, r := range s.PerTenant {
		row[r.Tenant] = r
	}
	if s.Fair.Saturated != 30 {
		t.Fatalf("saturated %d, want 30 (two in flight from 100 to 130)", s.Fair.Saturated)
	}
	if row["a"].AtShare != 30 || row["b"].AtShare != 30 {
		t.Fatalf("time at share a %d b %d, want 30 and 30 (alone from 130, a has share 2 and holds 1)", row["a"].AtShare, row["b"].AtShare)
	}
}

func TestWeightsDivideTheLimit(t *testing.T) {
	q, f := fairCore(t, 8, TenantWeight{Tenant: "a", Weight: 3})
	owner := map[int64]string{}
	for i := int64(1); i <= 10; i++ {
		name := "a"
		if i%2 == 0 {
			name = "b"
		}
		owner[i] = name
		mustPush(t, q, 1, name, i)
	}
	if f.Share("a") != 6 || f.Share("b") != 2 {
		t.Fatalf("shares a %d b %d, want 6 and 2 (8 x 3/4, 8 x 1/4)", f.Share("a"), f.Share("b"))
	}
	for {
		if _, ok := lendNext(t, q, 1, owner); !ok {
			break
		}
	}
	if q.InFlight() > 8 || q.TenantInFlight("b") > 2+1 {
		t.Fatalf("in flight %d, b %d", q.InFlight(), q.TenantInFlight("b"))
	}
	if ShareOf(4, 1, 7) != 1 || ShareOf(7, 2, 3) != 4 || ShareOf(3, 1, 0) != 3 {
		t.Fatal("share rounding: floor, at least 1")
	}
}

func TestUnpopLeavesTurnsAndCountersAsIfThePopNeverHappened(t *testing.T) {
	a, _ := fairCore(t, 3)
	b, _ := fairCore(t, 3)
	for _, q := range []*Queues{a, b} {
		mustPush(t, q, 1, "x", 1)
		mustPush(t, q, 1, "y", 2)
		mustPush(t, q, 1, "x", 3)
	}
	if _, ok := a.Pop(2); !ok || !a.Unpop() {
		t.Fatal("pop and unpop")
	}
	sa, sb := a.Snapshot(), b.Snapshot()
	sa.Unpops, sa.QueueWait = 0, sb.QueueWait
	for i := range sa.PerTenant {
		sa.PerTenant[i].QueueWait = sb.PerTenant[i].QueueWait
	}
	if sa.Fair != sb.Fair {
		t.Fatalf("fair state after unpop %+v, never popped %+v", sa.Fair, sb.Fair)
	}
	var order [2][]int64
	for i, q := range []*Queues{a, b} {
		for {
			id, ok := q.Pop(3)
			if !ok {
				break
			}
			order[i] = append(order[i], id)
		}
	}
	if len(order[0]) != 3 || order[0][0] != order[1][0] || order[0][1] != order[1][1] || order[0][2] != order[1][2] {
		t.Fatalf("order after unpop %v, never popped %v", order[0], order[1])
	}
}

func TestFairStateExistsOnlyWhileATenantHasLeasesAndIsBounded(t *testing.T) {
	q, f := fairCore(t, 16)
	owner := map[int64]string{}
	for i := int64(1); i <= 400; i++ {
		name := "t" + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
		owner[i] = name
		mustPush(t, q, Time(i), name, i)
		if _, ok := lendNext(t, q, Time(i), owner); !ok {
			t.Fatal("nothing lent")
		}
		if f.States() > 16 || f.InFlight() > 16 {
			t.Fatalf("%d states, %d in flight above the lease cap", f.States(), f.InFlight())
		}
		if q.InFlight() == 16 {
			for _, ts := range f.list[:8] {
				q.LeaseEnd(Time(i), ts.name)
			}
		}
		if err := q.Check(); err != nil {
			t.Fatal(err)
		}
	}
	for q.InFlight() > 0 {
		q.LeaseEnd(1000, f.list[0].name)
	}
	if f.States() != 0 || len(f.byName) != 0 || len(f.free) > 16 {
		t.Fatalf("after every lease ended: %d states, %d indexed, %d recycled", f.States(), len(f.byName), len(f.free))
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
	q.Close(1000 + 2*q.Config().Quiet())
	if err := q.Quiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseAccountingRefusesWhatCannotBeTrue(t *testing.T) {
	q, err := New(Config{Rule: RuleFIFO})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := NewFair(FairConfig{Limit: 2, MaxLeases: 2})
	q.SetDispatcher(f)
	if q.LeaseEnd(1, "a") {
		t.Fatal("an end without a start was accepted")
	}
	if err := q.Check(); err == nil {
		t.Fatal("Check must report the unmatched end")
	}
	q2, _ := New(Config{Rule: RuleFIFO})
	f2, _ := NewFair(FairConfig{Limit: 2, MaxLeases: 2})
	q2.SetDispatcher(f2)
	q2.LeaseStart(1, "a")
	q2.LeaseStart(1, "b")
	if q2.LeaseStart(1, "c") {
		t.Fatal("a start past the lease cap was accepted")
	}
	if err := q2.Check(); err == nil {
		t.Fatal("Check must report the refused start")
	}
	q2.SetDispatcher(nil)
	if q2.Fair() != nil || q2.LeaseStart(2, "a") || q2.Limit() != 0 {
		t.Fatal("without the fair dispatcher there is no lease accounting")
	}
	if q2.SetLimit(2, 3) {
		t.Fatal("a limit without the fair dispatcher")
	}
}

func TestSetLimitBounds(t *testing.T) {
	q, _ := fairCore(t, 4)
	for _, n := range []int{0, -1, MaxFairLimit + 1} {
		if q.SetLimit(1, n) || q.Limit() != 4 {
			t.Fatalf("limit %d accepted", n)
		}
	}
	if !q.SetLimit(1, 1) || q.Limit() != 1 {
		t.Fatal("limit 1 refused")
	}
}

func TestFairConfigFailsClosedNamingTheKey(t *testing.T) {
	for _, c := range []struct {
		cfg FairConfig
		key string
	}{
		{FairConfig{Limit: -1}, "pool.connections"},
		{FairConfig{Limit: MaxFairLimit + 1}, "pool.connections"},
		{FairConfig{DefaultWeight: -2}, "fairness.default_weight"},
		{FairConfig{DefaultWeight: MaxWeight + 1}, "fairness.default_weight"},
		{FairConfig{Weights: []TenantWeight{{Tenant: "a", Weight: -1}}}, "fairness.weights[0].weight"},
		{FairConfig{Weights: []TenantWeight{{Tenant: "a", Weight: MaxWeight + 1}}}, "fairness.weights[0].weight"},
		{FairConfig{Weights: []TenantWeight{{Tenant: "", Weight: 1}}}, "fairness.weights[0].tenant"},
		{FairConfig{Weights: []TenantWeight{{Tenant: "a", Weight: 1}, {Tenant: "a", Weight: 2}}}, "fairness.weights[1].tenant"},
		{FairConfig{Weights: make([]TenantWeight, MaxWeights+1)}, "fairness.weights"},
		{FairConfig{MaxLeases: -1}, "pool.connections"},
	} {
		_, err := NewFair(c.cfg)
		if err == nil || !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%+v: %v, want an error naming %s", c.cfg.Limit, err, c.key)
		}
	}
	f, err := NewFair(FairConfig{Weights: []TenantWeight{{Tenant: "a"}}})
	if err != nil || f.weight("a") != DefaultWeight || f.Config().Limit != MaxFairLimit {
		t.Fatalf("zero weight means the default: %v", err)
	}
}

func fairFuzzPlant() FairPlant {
	if os.Getenv(fairPlantEnv) == fairPlantOrder {
		return FairPlantUnpopKeepsTurn
	}
	return 0
}

func fairFuzzSeeds() [][]byte {
	return [][]byte{
		{},
		{0, 0, 0, 1, 1, 0, 4, 0, 0, 4, 0, 0},
		{0, 0, 0, 0, 1, 0, 0, 2, 0, 4, 0, 0, 8, 0, 0, 4, 0, 0, 4, 0, 0},
		{0, 2, 0, 4, 0, 0, 8, 0, 194, 0, 2, 0, 4, 0, 0},
	}
}

func FuzzFairnessOps(f *testing.F) {
	for _, s := range fairFuzzSeeds() {
		f.Add(s)
	}
	plant := fairFuzzPlant()
	f.Fuzz(func(t *testing.T, b []byte) {
		ops := decodeFairOps(b)
		for variant := range 3 {
			for _, rule := range fairRules() {
				if w, err := runFairOps(rule, variant, plant, ops); err != nil {
					t.Fatalf("%v variant %d: %v\n%s", rule, variant, err, fairTrail(w))
				}
			}
		}
	})
}

func TestFairFuzzDecodingIsTotalBoundedAndLocal(t *testing.T) {
	long := make([]byte, 10_000)
	for i := range long {
		long[i] = byte(i * 13)
	}
	ops := decodeFairOps(long)
	if len(ops) != fairMaxOps {
		t.Fatalf("%d ops from a long input, cap %d", len(ops), fairMaxOps)
	}
	for _, o := range ops {
		if o.kind >= numFairOps || o.dt < 0 || o.dt > 2400*Millisecond {
			t.Fatalf("op out of range: %v", o)
		}
	}
	a := decodeFairOps(long[:30])
	mut := append([]byte(nil), long[:30]...)
	mut[7]++
	b := decodeFairOps(mut)
	diff := 0
	for i := range a {
		if a[i] != b[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Fatalf("a one-byte change altered %d operations", diff)
	}
	if len(decodeFairOps([]byte{1, 2})) != 0 {
		t.Fatal("a partial operation decoded")
	}
}

func TestFairFuzzExecutionCostIsBounded(t *testing.T) {
	worst := make([]byte, fairMaxOps*fairOpBytes)
	for i := 0; i < len(worst); i += fairOpBytes {
		worst[i], worst[i+1], worst[i+2] = byte(i%7), byte(i), 255
	}
	ops := decodeFairOps(worst)
	for _, rule := range fairRules() {
		w, err := runFairOps(rule, 1, 0, ops)
		if err != nil {
			t.Fatal(err)
		}
		if len(w.log) > 3*fairMaxOps {
			t.Fatalf("%v: %d log lines for %d ops", rule, len(w.log), len(ops))
		}
		if w.q.Snapshot().StepsCapped != 0 || w.f.States() > fairMaxLeases || w.q.Tenants() > fairTenants {
			t.Fatalf("%v: unbounded state", rule)
		}
	}
}

var unpopTurnOps = []byte{0, 0, 0, 0, 1, 0, 0, 0, 0, 7, 0, 0, 4, 0, 0}

func TestPlantedUnpopTurnBugIsCaughtByTheFairnessModel(t *testing.T) {
	for _, seed := range fairFuzzSeeds() {
		if _, err := runFairOps(RuleFIFO, 0, FairPlantUnpopKeepsTurn, decodeFairOps(seed)); err != nil {
			t.Fatalf("a committed seed finds the planted bug on its own, so the fuzzer is not shown finding it: %v", err)
		}
	}
	ops := decodeFairOps(unpopTurnOps)
	if _, err := runFairOps(RuleFIFO, 0, FairPlantUnpopKeepsTurn, ops); err == nil {
		t.Fatal("the planted unpop turn bug passed on its seed")
	}
	for _, rule := range fairRules() {
		if w, err := runFairOps(rule, 0, 0, ops); err != nil {
			t.Fatalf("%v: %v\n%s", rule, err, fairTrail(w))
		}
	}
}
