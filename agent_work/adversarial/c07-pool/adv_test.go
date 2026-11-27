package adv_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const seed = 70927
const ms = pool.Instant(time.Millisecond)

func cfg() pool.Config {
	return pool.Config{Cap: 3, MaxConnecting: 3, RatePerSec: 1000, NoJitter: true, Seed: seed, Reset: pool.ResetOnSwitch, CheckAfterIdle: time.Hour, IdleTimeout: time.Hour, MaxLifetime: 2 * time.Hour, BreakerFailures: 1000}
}
func fresh(t *testing.T, c pool.Config) *pool.Pool {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./agent_work/adversarial/c07-pool", seed, t.Name())
	p, e := pool.New(c)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func actions(a []pool.Action, k pool.ActKind) []pool.Action {
	var out []pool.Action
	for _, v := range a {
		if v.Kind == k {
			out = append(out, v)
		}
	}
	return out
}
func opened(t *testing.T, p *pool.Pool, n int) []pool.ConnID {
	t.Helper()
	var ids []pool.ConnID
	for i := 0; i < n; i++ {
		a := actions(p.Demand(pool.Instant(i)*ms, n, nil), pool.ActDial)
		if len(a) != 1 {
			t.Fatalf("setup dial %d: %v", i, a)
		}
		ids = append(ids, a[0].Conn)
	}
	p.Demand(pool.Instant(n)*ms, 0, nil)
	for _, id := range ids {
		p.Opened(pool.Instant(n)*ms, id, nil)
	}
	return ids
}
func lend(t *testing.T, p *pool.Pool, at pool.Instant, tenant work.TenantID) pool.ConnID {
	t.Helper()
	id, ok, a := p.Checkout(at, tenant, nil)
	if !ok {
		t.Fatalf("expected lend: %v", a)
	}
	return id
}
func invariant(t *testing.T, p *pool.Pool) {
	t.Helper()
	if e := p.Invariants(); e != nil {
		t.Fatal(e)
	}
}

func TestAdv_1_CloseOutstanding(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 1 narrowed: a connection counts toward the cap until its close is issued, not until the driver confirms it. See agent_work/chunks/c07-pool/ADVERSARIAL.md.")
	c := cfg()
	c.Cap = 1
	p := fresh(t, c)
	id := opened(t, p, 1)[0]
	a := p.Lost(2*ms, id, pool.LossFatal, nil)
	if len(actions(a, pool.ActClose)) != 1 {
		t.Fatal(a)
	}
	a = p.Demand(3*ms, 1, nil)
	if len(actions(a, pool.ActDial)) > 0 {
		t.Fatal("replacement dial issued before driver acknowledged closing the old descriptor: physical open+connecting=2, cap=1")
	}
}
func TestAdv_1_LateOpenAfterShutdown(t *testing.T) {
	p := fresh(t, cfg())
	a := p.Demand(0, 9, nil)
	p.Shutdown(ms, nil)
	for _, x := range a {
		p.Opened(2*ms, x.Conn, nil)
	}
	if p.Snapshot().OpenPlusConnecting.Value != 0 {
		t.Fatal("late open resurrected shutdown pool")
	}
	invariant(t, p)
}
func TestAdv_1_SeededChurn(t *testing.T) {
	p := fresh(t, cfg())
	r := rand.New(rand.NewPCG(seed, 1))
	var ids []pool.ConnID
	for i := 0; i < 5000; i++ {
		now := pool.Instant(i) * ms
		a := p.Demand(now, 100, nil)
		for _, x := range actions(a, pool.ActDial) {
			ids = append(ids, x.Conn)
		}
		if len(ids) > 0 {
			id := ids[r.IntN(len(ids))]
			if r.IntN(3) == 0 {
				p.DialFailed(now, id, nil)
			} else {
				p.Opened(now, id, nil)
				p.Lost(now, id, pool.LossClosed, nil)
			}
		}
		invariant(t, p)
	}
}

func TestAdv_2_NonDivisorRate(t *testing.T) {
	c := cfg()
	c.RatePerSec = 3
	p := fresh(t, c)
	var times []pool.Instant
	for i := 0; i < 4; i++ {
		at := pool.Instant(i) * 333333333
		for _, a := range actions(p.Demand(at, 99, nil), pool.ActDial) {
			times = append(times, at)
			p.DialFailed(at, a.Conn, nil)
		}
	}
	if len(times) > 3 && times[3]-times[0] < pool.Instant(time.Second) {
		t.Fatalf("4 dials in %dns; floor(W*3/1s)+1=3", times[3]-times[0])
	}
}
func TestAdv_2_ConcurrentPending(t *testing.T) {
	c := cfg()
	c.Cap = 100
	c.MaxConnecting = 2
	p := fresh(t, c)
	for i := 0; i < 1000; i++ {
		p.Demand(pool.Instant(i)*ms, 10000, nil)
		if p.Snapshot().Connecting.Value > 2 {
			t.Fatal("pending connects exceeded 2")
		}
	}
	if p.Snapshot().Connecting.Value != 2 {
		t.Fatal("did not exercise pending cap")
	}
}
func TestAdv_2_MassLossRate(t *testing.T) {
	p := fresh(t, cfg())
	ids := opened(t, p, 3)
	for _, id := range ids {
		p.Lost(4*ms, id, pool.LossFatal, nil)
	}
	count := 0
	for i := 0; i < 100; i++ {
		count += len(actions(p.Demand(4*ms+pool.Instant(i), 999, nil), pool.ActDial))
	}
	if count != 1 {
		t.Fatalf("100 simultaneous demands produced %d dials, expected 1", count)
	}
}

func expiry(t *testing.T, tiny bool) {
	c := cfg()
	c.NoJitter = false
	c.JitterPermille = 1
	c.Cap = 64
	c.MaxConnecting = 64
	c.MaxLifetime = time.Second
	if tiny {
		c.MaxLifetime = 999 * time.Nanosecond
	}
	p := fresh(t, c)
	ids := opened(t, p, 64)
	seen := map[pool.Instant]int{}
	_ = ids // Opened reports every connection at the same timestamp.
	for i := 0; i < 64 && p.Next() != 0; i++ {
		at := p.Next()
		n := len(actions(p.Tick(at, nil), pool.ActClose))
		seen[at] += n
	}
	for at, n := range seen {
		if n > 1 {
			t.Fatalf("jitter on: %d co-created connections expired together at %d", n, at)
		}
	}
}
func TestAdv_3_JitterCollisions(t *testing.T)     { expiry(t, false) }
func TestAdv_3_SubMicrosecondJitter(t *testing.T) { expiry(t, true) }
func TestAdv_3_ExpiryRefillBound(t *testing.T) {
	c := cfg()
	c.NoJitter = false
	c.MaxLifetime = 20 * time.Millisecond
	p := fresh(t, c)
	opened(t, p, 3)
	p.Demand(4*ms, 99, nil)
	last := pool.Instant(-10000000)
	for at := 5 * ms; at < 100*ms; at += ms / 10 {
		for _, a := range p.Tick(at, nil) {
			if a.Kind == pool.ActDial {
				if at-last < ms {
					t.Fatalf("dial spacing %d below 1ms", at-last)
				}
				last = at
				p.Opened(at, a.Conn, nil)
			}
		}
		invariant(t, p)
	}
}

func TestAdv_4_ResetDeadlineEquality(t *testing.T) {
	c := cfg()
	c.Reset = pool.ResetAlways
	p := fresh(t, c)
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "a")
	p.Release(3*ms, id, pool.End{Kind: pool.EndClean, Tenant: "a"}, nil)
	deadline := p.Next()
	p.StepDone(deadline, id, 'I', nil)
	if p.State(id) != 0 {
		t.Fatalf("reset completed at expired deadline %d; state=%s", deadline, p.State(id))
	}
}
func TestAdv_4_LifetimeEqualityCheckout(t *testing.T) {
	c := cfg()
	c.MaxLifetime = 10 * time.Millisecond
	p := fresh(t, c)
	opened(t, p, 1)
	id, ok, _ := p.Checkout(11*ms, "a", nil)
	if ok {
		t.Fatalf("expired connection %d lent exactly at lifetime", id)
	}
}
func TestAdv_4_UnreadAfterClean(t *testing.T) {
	p := fresh(t, cfg())
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "a")
	p.Release(3*ms, id, pool.End{Kind: pool.EndUnread, Tenant: "a"}, nil)
	if got, ok, _ := p.Checkout(4*ms, "a", nil); ok {
		t.Fatalf("unread connection %d reused", got)
	}
}

func checking(t *testing.T) (*pool.Pool, pool.ConnID, pool.Instant) {
	c := cfg()
	c.CheckAfterIdle = time.Millisecond
	p := fresh(t, c)
	id := opened(t, p, 1)[0]
	_, ok, a := p.Checkout(2*ms, "a", nil)
	if ok || len(actions(a, pool.ActCheck)) != 1 {
		t.Fatal("check not exercised")
	}
	return p, id, p.Next()
}
func TestAdv_5_LateCheckSuccess(t *testing.T) {
	p, id, deadline := checking(t)
	p.StepDone(deadline+ms, id, 'I', nil)
	if p.State(id) != 0 {
		t.Fatalf("hanging check survived deadline: state=%s", p.State(id))
	}
}
func TestAdv_5_CheckFailureNoLease(t *testing.T) {
	p, id, _ := checking(t)
	a := p.StepFailed(3*ms, id, pool.FailUnexpected, nil)
	if p.Snapshot().CheckoutsLent != 0 || len(actions(a, pool.ActClose)) != 1 {
		t.Fatal("failed check leaked or lent")
	}
	invariant(t, p)
}
func TestAdv_5_CheckTimeoutNoLeak(t *testing.T) {
	p, id, deadline := checking(t)
	p.Tick(deadline, nil)
	if p.State(id) != 0 || p.Snapshot().Checking.Value != 0 {
		t.Fatal("check still live at deadline")
	}
	if p.Snapshot().ChecksByResult[pool.CheckTimeout] != 1 {
		t.Fatal("timeout not counted")
	}
}

func TestAdv_7_ZeroReplyDoesNotTrip(t *testing.T) {
	c := cfg()
	c.BreakerFailures = 1
	p := fresh(t, c)
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "a")
	p.Release(3*ms, id, pool.End{Kind: pool.EndStatementDeadline, Mid: true, ZeroReply: true}, nil)
	if p.Breaker() != pool.BreakerClosed {
		t.Fatal("zero reply tripped breaker")
	}
}
func TestAdv_7_OpenRefusesAndOneTrial(t *testing.T) {
	c := cfg()
	c.BreakerFailures = 1
	p := fresh(t, c)
	a := p.Demand(0, 99, nil)
	p.DialFailed(ms, a[0].Conn, nil)
	for at := 2 * ms; at < 1000*ms; at += ms {
		if p.Allow(at) || len(actions(p.Demand(at, 99, nil), pool.ActDial)) != 0 {
			t.Fatal("open breaker allowed traffic")
		}
	}
	a = p.Tick(1001*ms, nil)
	if len(actions(a, pool.ActDial)) != 1 {
		t.Fatalf("trial: %v", a)
	}
	for i := 0; i < 99; i++ {
		if len(actions(p.Tick(1002*ms, nil), pool.ActDial)) != 0 {
			t.Fatal("multiple pending trials")
		}
	}
}
func TestAdv_7_SQLFailureViaCheck(t *testing.T) {
	c := cfg()
	c.BreakerFailures = 1
	c.CheckAfterIdle = time.Millisecond
	p := fresh(t, c)
	id := opened(t, p, 1)[0]
	p.Checkout(2*ms, "a", nil)
	p.StepFailed(3*ms, id, pool.FailError, nil)
	if p.Breaker() != pool.BreakerClosed {
		t.Fatal("SQL ErrorResponse in a check (FailError) opened breaker; claim excludes SQL errors")
	}
}

func TestAdv_8_SwitchDuringReset(t *testing.T) {
	p := fresh(t, cfg())
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "alpha")
	p.Release(3*ms, id, pool.End{Kind: pool.EndClean, Tenant: "alpha"}, nil)
	_, ok, a := p.Checkout(4*ms, "beta", nil)
	if ok || len(actions(a, pool.ActReset)) != 1 {
		t.Fatal("tenant switch skipped reset")
	}
	if _, ok, _ = p.Checkout(5*ms, "alpha", nil); ok {
		t.Fatal("old tenant borrowed during switch reset")
	}
	p.StepDone(6*ms, id, 'I', nil)
	lend(t, p, 7*ms, "gamma")
}
func TestAdv_8_AlwaysSameTenant(t *testing.T) {
	c := cfg()
	c.Reset = pool.ResetAlways
	p := fresh(t, c)
	opened(t, p, 1)
	for i := 0; i < 30; i++ {
		at := pool.Instant(2+i*3) * ms
		id := lend(t, p, at, "same")
		a := p.Release(at+ms, id, pool.End{Kind: pool.EndClean, Tenant: "same"}, nil)
		if len(actions(a, pool.ActReset)) != 1 {
			t.Fatal("same tenant skipped always reset")
		}
		p.StepDone(at+2*ms, id, 'I', nil)
	}
}
func TestAdv_8_ReseedSameTenant(t *testing.T) {
	c := cfg()
	c.Reseed = true
	p := fresh(t, c)
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "same")
	a := p.Release(3*ms, id, pool.End{Kind: pool.EndClean, Tenant: "same"}, nil)
	_, ok, b := p.Checkout(4*ms, "same", nil)
	a = append(a, b...)
	for _, x := range a {
		if x.Reseed {
			return
		}
	}
	if ok {
		t.Fatal("reseed enabled but same-tenant next lease gets no reseed: random seed residue remains")
	}
}

func TestAdv_9_CleanupDeadlineDrain(t *testing.T) {
	p := fresh(t, cfg())
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "a")
	p.Release(3*ms, id, pool.End{Kind: pool.EndVanishedMidStatement, Mid: true}, nil)
	at := p.Next()
	p.StepDone(at, id, 'I', nil)
	if p.State(id) != 0 {
		t.Fatalf("drain at cleanup deadline resurrected into %s", p.State(id))
	}
}
func TestAdv_9_AllDirtyEnds(t *testing.T) {
	for k := pool.EndVanishedMidStatement; k < pool.EndLost; k++ {
		p := fresh(t, cfg())
		opened(t, p, 1)
		id := lend(t, p, 2*ms, "a")
		p.Release(3*ms, id, pool.End{Kind: k, InTx: true}, nil)
		if _, ok, _ := p.Checkout(4*ms, "a", nil); ok {
			t.Fatalf("%s lent during rollback", k)
		}
		p.StepFailed(5*ms, id, pool.FailError, nil)
		if p.State(id) != 0 {
			t.Fatalf("%s failed cleanup leaked", k)
		}
		invariant(t, p)
	}
}
func TestAdv_9_CancelDrainResetPermutations(t *testing.T) {
	for _, first := range []bool{false, true} {
		p := fresh(t, cfg())
		opened(t, p, 1)
		id := lend(t, p, 2*ms, "a")
		a := p.Release(3*ms, id, pool.End{Kind: pool.EndVanishedMidStatement, Mid: true}, nil)
		cancel := actions(a, pool.ActCancel)[0].Cancel
		if first {
			p.CancelDone(4*ms, cancel, nil)
		}
		p.StepDone(5*ms, id, 'T', nil)
		p.StepDone(6*ms, id, 'I', nil)
		p.StepDone(7*ms, id, 'I', nil)
		if !first {
			if _, ok, _ := p.Checkout(8*ms, "b", nil); ok {
				t.Fatal("lent before cancel completed")
			}
			p.CancelDone(9*ms, cancel, nil)
		}
		lend(t, p, 10*ms, "b")
		invariant(t, p)
	}
}

func TestAdv_10_DuplicateCancelCap(t *testing.T) {
	c := cfg()
	c.MaxCancels = 1
	p := fresh(t, c)
	opened(t, p, 2)
	a := lend(t, p, 3*ms, "a")
	b := lend(t, p, 3*ms, "b")
	for i := 0; i < 100; i++ {
		p.ClientCancel(4*ms, a, nil)
	}
	ok, _ := p.ClientCancel(4*ms, b, nil)
	if ok || p.Snapshot().CancelsInFlight.Value != 1 || p.Snapshot().Cancels != 1 {
		t.Fatal("duplicate or second cancel exceeded cap")
	}
}
func TestAdv_10_LateCancelAfterExpiry(t *testing.T) {
	p := fresh(t, cfg())
	opened(t, p, 1)
	id := lend(t, p, 2*ms, "a")
	_, a := p.ClientCancel(3*ms, id, nil)
	cancel := actions(a, pool.ActCancel)[0].Cancel
	p.Tick(2004*ms, nil)
	p.Release(2005*ms, id, pool.End{Kind: pool.EndClean, Tenant: "a"}, nil)
	if _, ok, _ := p.Checkout(2006*ms, "a", nil); ok {
		t.Fatalf("connection reused while cancel %d has never reported completion; delayed cancel can hit next statement", cancel)
	}
}
func TestAdv_10_CancelStaleID(t *testing.T) {
	p := fresh(t, cfg())
	id := opened(t, p, 1)[0]
	p.Lost(2*ms, id, pool.LossClosed, nil)
	opened(t, p, 1)
	if ok, a := p.ClientCancel(5*ms, id, nil); ok || len(actions(a, pool.ActCancel)) > 0 {
		t.Fatal("stale ID targeted replacement")
	}
}

func TestAdv_12_MaxInteger(t *testing.T) {
	for _, n := range []int{1, 2, 3, 97, math.MaxInt} {
		v, e := pool.Share(math.MaxInt, n)
		if e != nil || v < 1 || v > math.MaxInt/n {
			t.Fatalf("Share(max,%d)=%d,%v", n, v, e)
		}
	}
}
func TestAdv_12_InvalidSigns(t *testing.T) {
	for _, x := range [][2]int{{0, 1}, {-1, 1}, {1, 0}, {1, -1}, {math.MinInt, math.MaxInt}, {1, 2}} {
		if v, e := pool.Share(x[0], x[1]); e == nil {
			t.Fatalf("Share%v accepted %d", x, v)
		}
	}
}
func TestAdv_12_SeededRemainders(t *testing.T) {
	r := rand.New(rand.NewPCG(seed, 12))
	for i := 0; i < 10000; i++ {
		n := 1 + r.IntN(100000)
		q := 1 + r.IntN(100000)
		total := n*q + r.IntN(n)
		v, e := pool.Share(total, n)
		if e != nil || v != q || v*n > total {
			t.Fatalf("%d/%d -> %d,%v", total, n, v, e)
		}
	}
}

func TestAdv_13_CloseNoPeerReply(t *testing.T) {
	p := fresh(t, cfg())
	ids := opened(t, p, 3)
	for _, id := range ids {
		p.Lost(4*ms, id, pool.LossClosed, nil)
	}
	a := p.Demand(5*ms, 1, nil)
	if len(actions(a, pool.ActDial)) != 1 {
		t.Fatal("close waits for peer before replacement")
	}
}
func TestAdv_13_ShutdownInEveryState(t *testing.T) {
	for k := 0; k < 4; k++ {
		p := fresh(t, cfg())
		id := opened(t, p, 1)[0]
		if k > 0 {
			lend(t, p, 2*ms, "a")
		}
		if k > 1 {
			p.Release(3*ms, id, pool.End{Kind: pool.EndVanishedInTx, InTx: true}, nil)
		}
		if k > 2 {
			p.StepDone(4*ms, id, 'I', nil)
		}
		p.Shutdown(5*ms, nil)
		if p.Next() != 0 || p.Quiesce() != nil || p.Snapshot().OpenPlusConnecting.Value != 0 {
			t.Fatalf("shutdown state %d left resources", k)
		}
	}
}
func TestAdv_13_CloseDoesNotBlockSibling(t *testing.T) {
	p := fresh(t, cfg())
	ids := opened(t, p, 2)
	p.Lost(3*ms, ids[0], pool.LossClosed, nil)
	_, ok, a := p.Checkout(4*ms, "b", nil)
	if ok {
		t.Fatal("sibling should check after close")
	}
	checks := actions(a, pool.ActCheck)
	if len(checks) != 1 {
		t.Fatal("sibling check blocked by close")
	}
	p.StepDone(5*ms, checks[0].Conn, 'I', nil)
	lend(t, p, 6*ms, "b")
}

func coreReplay(t *testing.T) string {
	c := cfg()
	c.NoJitter = false
	p := fresh(t, c)
	ids := opened(t, p, 3)
	var trace []pool.Action
	for _, id := range ids {
		trace = append(trace, p.Lost(4*ms, id, pool.LossFatal, nil)...)
	}
	trace = append(trace, p.Demand(5*ms, 3, nil)...)
	trace = append(trace, p.Shutdown(9*ms, nil)...)
	return fmt.Sprintf("%v|%+v", trace, p.Snapshot())
}
func TestAdv_14_CoreReplay(t *testing.T) {
	a, b := coreReplay(t), coreReplay(t)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed changes actions or snapshot")
	}
}
func TestAdv_1_LongCampaign(t *testing.T) {
	t.Log("seeds=1..2000 PRNG stream=70927; REPLAY: ADV_LONG=1 go test -run ^TestAdv_1_LongCampaign$ ./agent_work/adversarial/c07-pool")
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1")
	}
	for s := uint64(1); s <= 2000; s++ {
		c := cfg()
		c.Seed = s
		c.NoJitter = false
		p, e := pool.New(c)
		if e != nil {
			t.Fatal(e)
		}
		r := rand.New(rand.NewPCG(s, seed))
		for i := 0; i < 200; i++ {
			at := pool.Instant(i) * ms
			for _, a := range p.Demand(at, 10, nil) {
				if a.Kind == pool.ActDial {
					if r.IntN(2) == 0 {
						p.DialFailed(at, a.Conn, nil)
					} else {
						p.Opened(at, a.Conn, nil)
						p.Lost(at, a.Conn, pool.LossFatal, nil)
					}
				}
			}
			if e := p.Invariants(); e != nil {
				t.Fatalf("seed=%d step=%d %v", s, i, e)
			}
		}
	}
}
