package adv_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	a "github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

const seed int64 = 909271

func core(t *testing.T, limit int, weights ...a.TenantWeight) *a.Queues {
	t.Helper()
	t.Logf("seed=%d", seed)
	q, e := a.New(a.Config{Rule: a.RuleFIFO, LIFO: a.LIFOOff, Capacity: 8192, TenantCapacity: 256, MaxTenants: 8192, Detail: true})
	if e != nil {
		t.Fatal(e)
	}
	f, e := a.NewFair(a.FairConfig{Limit: limit, MaxLeases: a.MaxFairLimit, Weights: weights})
	if e != nil {
		t.Fatal(e)
	}
	q.SetDispatcher(f)
	return q
}
func push(t *testing.T, q *a.Queues, n string, id int64) {
	t.Helper()
	if !q.Push(0, n, id) {
		t.Fatalf("push %s/%d refused", n, id)
	}
}
func lend(t *testing.T, q *a.Queues, n string) {
	t.Helper()
	if !q.LeaseStart(0, n) {
		t.Fatalf("lease %s refused", n)
	}
}
func check(t *testing.T, q *a.Queues) {
	t.Helper()
	if e := q.Check(); e != nil {
		t.Fatal(e)
	}
	if e := q.Snapshot().Check(); e != nil {
		t.Fatal(e)
	}
}

func TestAdv_1_WeightedLastSlot(t *testing.T) {
	q := core(t, 7, a.TenantWeight{Tenant: "big", Weight: 1000})
	push(t, q, "big", 1)
	push(t, q, "small", 2)
	for range 6 {
		lend(t, q, "big")
	}
	id, ok := q.Pop(0)
	if !ok || id != 2 {
		t.Fatalf("last slot chose %d/%v, want below-share small", id, ok)
	}
}
func TestAdv_1_LimitShrinksBelowOutstanding(t *testing.T) {
	q := core(t, 8)
	for range 7 {
		lend(t, q, "x")
	}
	push(t, q, "y", 1)
	q.SetLimit(0, 1)
	for range 7 {
		if id, ok := q.Pop(0); ok {
			t.Fatalf("lent %d above lowered limit", id)
		}
		q.LeaseEnd(0, "x")
	}
	if _, ok := q.Pop(0); !ok {
		t.Fatal("did not resume at zero")
	}
}
func TestAdv_1_MaximumWeightedArithmetic(t *testing.T) {
	q := core(t, a.MaxFairLimit, a.TenantWeight{Tenant: "x", Weight: 1000})
	push(t, q, "x", 1)
	push(t, q, "y", 2)
	if n := q.Fair().Share("x"); n != a.MaxFairLimit*1000/1001 {
		t.Fatalf("share overflow: %d", n)
	}
}

func TestAdv_2_StatsOverflowConserves(t *testing.T) {
	q := core(t, 8192)
	for i := 0; i < 4101; i++ {
		lend(t, q, fmt.Sprint(i))
	}
	check(t, q)
	for i := 4100; i >= 0; i-- {
		q.LeaseEnd(0, fmt.Sprint(i))
	}
	check(t, q)
	if q.InFlight() != 0 || q.Fair().States() != 0 {
		t.Fatal("state retained")
	}
}
func TestAdv_2_ShuffledLeaseEnds(t *testing.T) {
	q := core(t, 200)
	r := rand.New(rand.NewSource(seed))
	ns := make([]string, 200)
	for i := range ns {
		ns[i] = fmt.Sprint(r.Intn(19))
		lend(t, q, ns[i])
	}
	r.Shuffle(len(ns), func(i, j int) { ns[i], ns[j] = ns[j], ns[i] })
	for _, n := range ns {
		q.LeaseEnd(0, n)
		check(t, q)
	}
}
func TestAdv_2_AccountAtLimitChange(t *testing.T) {
	q := core(t, 2)
	lend(t, q, "x")
	q.SetLimit(17, 1)
	q.LeaseEnd(40, "x")
	s := q.Snapshot()
	if s.Fair.Saturated != 23 {
		t.Fatalf("saturation=%d want 23", s.Fair.Saturated)
	}
	check(t, q)
}

func TestAdv_3_RoundedRemainderIdles(t *testing.T) {
	q := core(t, 3)
	lend(t, q, "a")
	lend(t, q, "b")
	push(t, q, "a", 1)
	push(t, q, "b", 2)
	if _, ok := q.Pop(0); !ok {
		t.Fatalf("inflight=%d limit=3, two waiting, one idle below limit (original claim)", q.InFlight())
	}
}
func TestAdv_3_OnlyOverShareWaitingBelowLast(t *testing.T) {
	q := core(t, 9)
	lend(t, q, "b")
	for range 5 {
		lend(t, q, "a")
	}
	push(t, q, "a", 1)
	if id, ok := q.Pop(0); !ok || id != 1 {
		t.Fatalf("idle below last slot: %d/%v", id, ok)
	}
}
func TestAdv_3_CancelChangesEligibility(t *testing.T) {
	q := core(t, 3)
	lend(t, q, "a")
	lend(t, q, "a")
	push(t, q, "a", 1)
	push(t, q, "b", 2)
	q.Remove(0, 2)
	if id, ok := q.Pop(0); !ok || id != 1 {
		t.Fatalf("cancel did not release last slot: %d/%v", id, ok)
	}
}

func rotation(t *testing.T, n int, weighted bool) {
	q := core(t, 1)
	r := rand.New(rand.NewSource(seed))
	order := r.Perm(n)
	for _, i := range order {
		push(t, q, fmt.Sprint(i), int64(i+1))
		push(t, q, fmt.Sprint(i), int64(n+i+1))
	}
	seen := map[int64]bool{}
	for range n {
		id, ok := q.Pop(0)
		if !ok {
			t.Fatal("eligible queue blocked")
		}
		tenant := (id - 1) % int64(n)
		if seen[tenant] {
			t.Fatalf("tenant %d repeated before all %d got a turn", tenant, n)
		}
		seen[tenant] = true
	}
	check(t, q)
}
func TestAdv_4_MoreTenantsThanSlots(t *testing.T) { rotation(t, 97, false) }
func TestAdv_4_CancelHeadPreservesTurn(t *testing.T) {
	q := core(t, 1)
	push(t, q, "a", 1)
	push(t, q, "a", 2)
	push(t, q, "b", 3)
	q.Remove(0, 1)
	id, _ := q.Pop(0)
	id2, _ := q.Pop(0)
	if id != 2 || id2 != 3 {
		t.Fatalf("cancel order %d,%d", id, id2)
	}
}
func TestAdv_4_ContinuousBetterGroup(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 4 narrowed: the bounded wait is within a group; a tenant at or above its share may wait while below-share tenants keep arriving. See agent_work/chunks/c09-fairness/ADVERSARIAL.md.")
	q := core(t, 4)
	lend(t, q, "a")
	lend(t, q, "a")
	push(t, q, "a", 1)
	for i := 0; i < 12; i++ {
		push(t, q, "b", int64(i+2))
		id, ok := q.Pop(0)
		if !ok {
			t.Fatal("b blocked")
		}
		if id == 1 {
			return
		}
	}
	t.Fatal("a still waiting after 12 choices with 2 active tenants (original unqualified claim)")
}

func held(t *testing.T, mode int) {
	q := core(t, 8)
	for i := int64(100); i < 106; i++ {
		push(t, q, "held", i)
		if id, ok := q.Pop(0); !ok || id != i {
			t.Fatalf("initial solo lend %d/%v", id, ok)
		}
		lend(t, q, "held")
	}
	switch mode {
	case 0:
		push(t, q, "new", 1)
	case 1:
		q.SetLimit(0, 2)
	case 2:
		push(t, q, "heavy", 1)
		q.LeaseStart(0, "heavy")
	}
	if n, s := q.TenantInFlight("held"), q.Fair().Share("held"); n > s {
		t.Fatalf("held leases=%d current share=%d: existing transactions exceed stated bound", n, s)
	}
}
func TestAdv_8_NewTenantShrinksShare(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 8 narrowed: no preemption; the share bounds new lends only. See agent_work/chunks/c09-fairness/ADVERSARIAL.md.")
	held(t, 0)
}
func TestAdv_8_LimitReductionWithHeldLeases(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 8 narrowed: no preemption; the share bounds new lends only. See agent_work/chunks/c09-fairness/ADVERSARIAL.md.")
	held(t, 1)
}
func TestAdv_8_HeldPeerChangesShare(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 8 narrowed: no preemption; the share bounds new lends only. See agent_work/chunks/c09-fairness/ADVERSARIAL.md.")
	held(t, 2)
}

func TestAdv_10_ChurnBeyondStatsCap(t *testing.T) {
	q := core(t, 1)
	for i := 0; i < 4200; i++ {
		n := fmt.Sprint(i)
		lend(t, q, n)
		q.LeaseEnd(0, n)
		if q.Fair().States() != 0 {
			t.Fatal("retained empty state")
		}
	}
	check(t, q)
}
func TestAdv_10_FullLimitRepeatedPicks(t *testing.T) {
	q := core(t, 1)
	lend(t, q, "a")
	push(t, q, "b", 1)
	before := q.Snapshot()
	for range 10000 {
		if _, ok := q.Pop(0); ok {
			t.Fatal("pop while full")
		}
	}
	s := q.Snapshot()
	if s.Served != before.Served || s.Fair.Rounds != before.Fair.Rounds || s.Fair.HeldLimit-before.Fair.HeldLimit != 10000 {
		t.Fatalf("repeated selection changed state: %+v", s.Fair)
	}
}
func TestAdv_10_ReuseAfterClose(t *testing.T) {
	q := core(t, 1)
	for i := int64(1); i <= 64; i++ {
		push(t, q, "a", i)
		q.Pop(0)
		lend(t, q, "a")
		q.LeaseEnd(0, "a")
		q.Close(0)
		if e := q.Quiesce(); e != nil {
			t.Fatal(e)
		}
	}
}

func invalid(t *testing.T, j, key string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	_, e := config.Parse([]byte(`{"fairness":` + j + `}`))
	if e == nil || !strings.Contains(e.Error(), key) {
		t.Fatalf("invalid fairness %s: error=%v, want key %s", j, e, key)
	}
}
func TestAdv_11_IntegerOverflowAtLoad(t *testing.T) {
	invalid(t, `{"default_weight":9223372036854775808}`, "default_weight")
}
func TestAdv_11_DuplicateEscapedNames(t *testing.T) {
	invalid(t, `{"weights":[{"tenant":"a","weight":1},{"tenant":"\u0061","weight":2}]}`, "fairness.weights[1].tenant")
}
func TestAdv_11_UnknownShareKey(t *testing.T) { invalid(t, `{"share":-1}`, "share") }

func TestAdv_12_UnpopAfterHeldSelection(t *testing.T) {
	q := core(t, 1)
	lend(t, q, "held")
	push(t, q, "a", 1)
	push(t, q, "b", 2)
	q.Pop(0)
	q.LeaseEnd(0, "held")
	before := q.Snapshot().Fair
	id, ok := q.Pop(0)
	if !ok || !q.Unpop() {
		t.Fatal("unpop refused")
	}
	after := q.Snapshot().Fair
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unpop changed fairness: before=%+v after=%+v", before, after)
	}
	again, _ := q.Pop(0)
	if again != id {
		t.Fatalf("turn changed %d to %d", id, again)
	}
}
func TestAdv_12_ExpiryWhileHeld(t *testing.T) {
	q := core(t, 1)
	lend(t, q, "a")
	push(t, q, "b", 1)
	q.Pop(0)
	if !q.Expire(1, 1) {
		t.Fatal("expiry lost held request")
	}
	s := q.Snapshot()
	if s.Shed[a.ReasonDeadline] != 1 || q.Len() != 0 || q.InFlight() != 1 {
		t.Fatalf("expiry moved lease or missed shed: %+v", s.Fair)
	}
	check(t, q)
}
func TestAdv_12_BreakerRefusalWhileHeld(t *testing.T) {
	q := core(t, 1)
	lend(t, q, "a")
	push(t, q, "b", 1)
	q.Pop(0)
	if !q.Refuse(1, 1) {
		t.Fatal("refusal lost held request")
	}
	s := q.Snapshot()
	if s.Shed[a.ReasonBreaker] != 1 || q.InFlight() != 1 || q.Len() != 0 {
		t.Fatal("refusal accounting differs")
	}
	q.LeaseEnd(1, "a")
	q.Close(1)
	if e := q.Quiesce(); e != nil {
		t.Fatal(e)
	}
}

func invariant(t *testing.T, n int) {
	t.Helper()
	t.Logf("seed=%d", seed)
	aq, e := proxy.NewAdmissionQueue(a.Config{Rule: a.RuleFIFO})
	if e != nil {
		t.Fatal(e)
	}
	f, e := a.NewFair(a.FairConfig{Limit: 8, MaxLeases: 8})
	if e != nil {
		t.Fatal(e)
	}
	aq.Core().SetDispatcher(f)
	w, e := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed, Tenants: 1, Engine: proxy.Config{Connections: 8, Seams: proxy.Seams{Queue: aq}}})
	if e != nil {
		t.Fatal(e)
	}
	eng := w.Proxy.Engine
	if e = eng.Invariants(); e != nil {
		t.Fatal(e)
	}
	if n == 3 {
		aq.Core().LeaseStart(0, "prior")
		aq.Core().LeaseEnd(0, "prior")
	}
	for i := 0; i < n; i++ {
		aq.Core().LeaseStart(0, fmt.Sprint(i))
	}
	if e = aq.Core().Check(); e != nil {
		t.Fatal(e)
	}
	if e = eng.Invariants(); e == nil || !strings.Contains(e.Error(), "fairness:") {
		t.Fatalf("engine accepted extra fair leases: %v", e)
	}
}
func TestAdv_13_ExtraFairLeaseDetected(t *testing.T)       { invariant(t, 1) }
func TestAdv_13_MultipleGhostTenantsDetected(t *testing.T) { invariant(t, 7) }
func TestAdv_13_CorruptionAfterBalancedHistory(t *testing.T) {
	q := core(t, 1)
	lend(t, q, "old")
	q.LeaseEnd(0, "old")
	check(t, q)
	invariant(t, 3)
}
