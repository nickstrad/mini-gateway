package proxy_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

var quietRule = admission.Config{Rule: admission.RuleTimeout, Target: 5 * admission.Second, Interval: 10 * admission.Second, LIFO: admission.LIFOOff}

func joinRig(t *testing.T, a admission.Config, edit func(*proxy.Config)) *coreRig {
	t.Helper()
	return newCoreRig(t, func(c *proxy.Config) {
		c.Admission = a
		if edit != nil {
			edit(c)
		}
	})
}

func (r *coreRig) agree() (proxy.Snapshot, admission.Snapshot) {
	r.t.Helper()
	s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
	if err := r.e.Invariants(); err != nil {
		r.t.Fatal(err)
	}
	if err := as.Check(); err != nil {
		r.t.Fatal(err)
	}
	if err := r.pool().Check(); err != nil {
		r.t.Fatal(err)
	}
	if s.Shed != as.ShedTotal() || s.BreakerFlushed != as.Shed[admission.ReasonBreaker] {
		r.t.Fatalf("engine shed %d (breaker %d), admission shed %d (breaker %d)", s.Shed, s.BreakerFlushed, as.ShedTotal(), as.Shed[admission.ReasonBreaker])
	}
	if s.Queued.Value != as.Queued.Value {
		r.t.Fatalf("engine queued %d, admission queued %d", s.Queued.Value, as.Queued.Value)
	}
	return s, as
}

func replied(a []proxy.Action, id proxy.ConnID, code string) bool {
	return bytes.Contains(written(a, proxy.ActClientWrite, id), []byte(code))
}

func TestJoinOverloadWithAHealthyPoolShedsByTheRuleAndLeavesThePoolUntouched(t *testing.T) {
	r := joinRig(t, admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOOff}, func(c *proxy.Config) { c.Connections = 1 })
	id := r.firstLease(10, "SELECT pg_sleep(1)")
	before := r.pool()
	queued := []proxy.ConnID{11, 12, 13, 14}
	for i, c := range queued {
		r.login(c, i%2)
		r.q(c, "SELECT 1")
	}
	if r.e.Snapshot().Queued.Value != int64(len(queued)) {
		t.Fatalf("queued %d", r.e.Snapshot().Queued.Value)
	}
	a := r.tick(time.Duration(admission.DefaultInterval))
	for _, c := range queued {
		if !replied(a, c, "53M01") {
			t.Fatalf("client %d got %q, want 53M01 from the rule", c, written(a, proxy.ActClientWrite, c))
		}
	}
	s, as := r.agree()
	if as.Shed[admission.ReasonRule] != int64(len(queued)) || s.BreakerFlushed != 0 {
		t.Fatalf("rule sheds %d, breaker flushes %d", as.Shed[admission.ReasonRule], s.BreakerFlushed)
	}
	p := r.pool()
	var closes int64
	for _, n := range p.Closes {
		closes += n
	}
	if p.Dials != before.Dials || p.DialFailures != 0 || closes != 0 || p.Breaker != int64(pool.BreakerClosed) || p.BreakerFailures != 0 ||
		p.Lent.Value != 1 || p.Checkouts != before.Checkouts || p.CheckoutsLent != before.CheckoutsLent {
		t.Fatalf("the pool moved under a queue overload: before %+v after %+v", before, p)
	}
	r.db(id, complete("SELECT 1", 'I'))
	r.finishReset(id)
	r.login(15, 0)
	a = r.q(15, "SELECT 2")
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, id), query("SELECT 2")) {
		t.Fatalf("after the overload the next request was not served: %q", written(a, proxy.ActDBWrite, id))
	}
	r.agree()
}

func TestJoinBreakerOpeningRefusesTheStandingQueueWith53M03AndCountersAgree(t *testing.T) {
	r := joinRig(t, quietRule, func(c *proxy.Config) {
		c.Connections = 1
		c.Pool.BreakerFailures = 1
	})
	id := r.firstLease(10, "SELECT pg_sleep(1)")
	queued := []proxy.ConnID{11, 12, 13}
	for i, c := range queued {
		r.login(c, i%2)
		r.q(c, "SELECT 1")
	}
	if _, as := r.agree(); as.Queued.Value != 3 || as.Tenants.Value != 2 {
		t.Fatalf("standing queue %d in %d tenant queues", as.Queued.Value, as.Tenants.Value)
	}
	a := r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
	if r.pool().Breaker != int64(pool.BreakerOpen) {
		t.Fatalf("breaker %d after losing the lent connection", r.pool().Breaker)
	}
	for _, c := range queued {
		if !replied(a, c, "53M03") || replied(a, c, "53M01") {
			t.Fatalf("queued client %d got %q, want 53M03", c, written(a, proxy.ActClientWrite, c))
		}
	}
	if replied(a, 10, "53M03") || len(written(a, proxy.ActClientWrite, 10)) == 0 {
		t.Fatalf("the client whose statement was lost got %q", written(a, proxy.ActClientWrite, 10))
	}
	s, as := r.agree()
	if s.BreakerFlushed != 3 || as.Shed[admission.ReasonBreaker] != 3 || as.ShedTotal() != 3 || as.Served != 1 || as.Removed != 0 || as.Shedding != 0 {
		t.Fatalf("engine flushed %d; admission breaker %d shed %d served %d removed %d shedding %d", s.BreakerFlushed,
			as.Shed[admission.ReasonBreaker], as.ShedTotal(), as.Served, as.Removed, as.Shedding)
	}
	pushes := as.Pushes
	dials := len(r.dbs)
	a = r.q(11, "SELECT 2")
	if !replied(a, 11, "53M03") || len(r.dbs) != dials {
		t.Fatalf("an arrival while open got %q with %d new dials", written(a, proxy.ActClientWrite, 11), len(r.dbs)-dials)
	}
	s, as = r.agree()
	if as.Pushes != pushes || s.StageRefuse[proxy.StageBreaker] != 1 || s.BreakerFlushed != 3 {
		t.Fatalf("a refusal at the gate reached the queue: pushes %d -> %d, gate refusals %d", pushes, as.Pushes, s.StageRefuse[proxy.StageBreaker])
	}
	r.tick(pool.DefaultBreakerOpenFor)
	a = r.q(12, "SELECT 3")
	if replied(a, 12, "53M03") || len(r.dbs) != dials+1 {
		t.Fatalf("half-open: client got %q with %d dials, want it queued behind one trial", written(a, proxy.ActClientWrite, 12), len(r.dbs)-dials)
	}
	if _, as = r.agree(); as.Pushes != pushes+1 || as.Queued.Value != 1 {
		t.Fatalf("half-open arrival not in its tenant queue: pushes %d queued %d", as.Pushes, as.Queued.Value)
	}
	trial := r.dbs[len(r.dbs)-1]
	a = r.open(trial)
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, trial), query("SELECT 3")) || r.pool().Breaker != int64(pool.BreakerClosed) {
		t.Fatalf("after the trial: wrote %q, breaker %d", written(a, proxy.ActDBWrite, trial), r.pool().Breaker)
	}
	r.agree()
}

func TestJoinHalfOpenTrialWithQueuedRequestsFromTwoTenants(t *testing.T) {
	open := func(t *testing.T) (*coreRig, proxy.ConnID) {
		r := joinRig(t, quietRule, func(c *proxy.Config) { c.Pool.BreakerFailures = 1 })
		id := r.firstLease(10, "SELECT 1")
		r.db(id, complete("SELECT 1", 'I'))
		r.finishReset(id)
		r.db(id, []byte("X"))
		if r.pool().Breaker != int64(pool.BreakerOpen) {
			t.Fatal("breaker did not open")
		}
		r.tick(pool.DefaultBreakerOpenFor)
		dials := len(r.dbs)
		r.login(11, 0)
		r.login(12, 1)
		r.q(11, "SELECT 11")
		r.q(12, "SELECT 12")
		if _, as := r.agree(); as.Queued.Value != 2 || as.Tenants.Value != 2 || len(r.dbs) != dials+1 || r.pool().BreakerTrials != 1 {
			t.Fatalf("queued %d in %d tenant queues, %d dials, %d trials; want both tenants behind one trial", as.Queued.Value, as.Tenants.Value,
				len(r.dbs)-dials, r.pool().BreakerTrials)
		}
		return r, r.dbs[len(r.dbs)-1]
	}
	t.Run("trial-passes", func(t *testing.T) {
		r, trial := open(t)
		a := r.open(trial)
		if !bytes.HasSuffix(written(a, proxy.ActDBWrite, trial), query("SELECT 11")) || r.pool().Breaker != int64(pool.BreakerClosed) {
			t.Fatalf("the trial connection was not lent to the oldest request: %q", written(a, proxy.ActDBWrite, trial))
		}
		r.tick(time.Millisecond)
		if len(r.dbs) < 3 {
			t.Fatal("no dial for the second tenant's request after the breaker closed")
		}
		second := r.dbs[len(r.dbs)-1]
		a = r.open(second)
		if !bytes.HasSuffix(written(a, proxy.ActDBWrite, second), query("SELECT 12")) {
			t.Fatalf("the second tenant was not served: %q", written(a, proxy.ActDBWrite, second))
		}
		s, as := r.agree()
		if as.Served != 3 || as.ShedTotal() != 0 || s.BreakerFlushed != 0 || as.Queued.Value != 0 {
			t.Fatalf("served %d shed %d flushed %d queued %d", as.Served, as.ShedTotal(), s.BreakerFlushed, as.Queued.Value)
		}
	})
	t.Run("trial-fails", func(t *testing.T) {
		r, trial := open(t)
		a := r.run(r.ev(proxy.EvDBDialFailed, trial, nil, 0))
		if !replied(a, 11, "53M03") || !replied(a, 12, "53M03") {
			t.Fatalf("after a failed trial: %q / %q", written(a, proxy.ActClientWrite, 11), written(a, proxy.ActClientWrite, 12))
		}
		s, as := r.agree()
		if as.Shed[admission.ReasonBreaker] != 2 || s.BreakerFlushed != 2 || as.Queued.Value != 0 || as.Served != 1 {
			t.Fatalf("breaker sheds %d flushed %d queued %d served %d", as.Shed[admission.ReasonBreaker], s.BreakerFlushed, as.Queued.Value, as.Served)
		}
		if r.pool().Breaker != int64(pool.BreakerOpen) {
			t.Fatal("a failed trial did not re-open the breaker")
		}
	})
}

func TestJoinShutdownWithQueuedRequestsAndConnectionsInEveryPoolState(t *testing.T) {
	r := joinRig(t, quietRule, func(c *proxy.Config) {
		c.Connections = 16
		c.QueueCap = 16
		c.Pool.CheckAfterIdle = 50 * time.Millisecond
	})
	const lent = 13
	for i := 0; i < lent; i++ {
		c := proxy.ConnID(100 + i)
		r.login(c, i%2)
		r.q(c, "SELECT 1")
		r.tick(time.Millisecond)
		r.open(r.dbs[i])
	}
	for i := 0; i < 10; i++ {
		r.db(r.dbs[i], complete("SELECT 1", 'I'))
		r.finishReset(r.dbs[i])
	}
	r.db(r.dbs[10], complete("SELECT 1", 'I'))
	r.run(r.ev(proxy.EvClientClosed, 111, nil, 0))
	r.tick(60 * time.Millisecond)
	r.login(200, 0)
	r.login(201, 1)
	r.q(200, "SELECT 2")
	r.q(201, "SELECT 3")
	p := r.pool()
	_, as := r.agree()
	if p.Connecting.Value == 0 || p.Idle.Value == 0 || p.Lent.Value == 0 || p.Checking.Value == 0 || p.Cleaning.Value == 0 ||
		p.Resetting.Value == 0 || as.Queued.Value != 2 || as.Tenants.Value != 2 || p.CancelsInFlight.Value == 0 {
		t.Fatalf("premise: connecting %d idle %d lent %d checking %d cleaning %d resetting %d cancels %d; queued %d in %d tenant queues",
			p.Connecting.Value, p.Idle.Value, p.Lent.Value, p.Checking.Value, p.Cleaning.Value, p.Resetting.Value, p.CancelsInFlight.Value,
			as.Queued.Value, as.Tenants.Value)
	}
	open := p.Open.Value
	a := r.e.Shutdown(r.now, nil)
	for _, x := range a {
		if x.Kind == proxy.ActWake && x.At > r.now {
			t.Fatalf("shutdown armed a wake at %d", x.At)
		}
	}
	if err := r.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
	s, as := r.agree()
	if as.Removed != 2 || as.Pushes != as.Served+as.ShedTotal()+as.Removed || as.Tenants.Value != 0 {
		t.Fatalf("admission after shutdown: pushes %d served %d shed %d removed %d tenants %d", as.Pushes, as.Served, as.ShedTotal(), as.Removed, as.Tenants.Value)
	}
	p = r.pool()
	if p.Open.Value != 0 || p.Connecting.Value != 0 || p.Closes[pool.CloseShutdown] < open || p.CancelsInFlight.Value != 0 {
		t.Fatalf("pool after shutdown: open %d connecting %d shutdown closes %d of %d open, cancels %d", p.Open.Value, p.Connecting.Value,
			p.Closes[pool.CloseShutdown], open, p.CancelsInFlight.Value)
	}
	if s.InFlight.Value != 0 || s.Queued.Value != 0 || s.DBLent.Value != 0 || s.Resetting.Value != 0 || s.CancelConns.Value != 0 {
		t.Fatalf("engine after shutdown: in flight %d queued %d lent %d resetting %d cancel conns %d", s.InFlight.Value, s.Queued.Value,
			s.DBLent.Value, s.Resetting.Value, s.CancelConns.Value)
	}
}

func TestJoinEveryCoreEngineTestHoldsWithTheAdmissionQueue(t *testing.T) {
	for _, rule := range []admission.RuleKind{admission.RuleTimeout, admission.RuleControlLaw} {
		a := quietRule
		a.Rule = rule
		coreAdmission = a
		for _, tc := range []struct {
			name string
			run  func(*testing.T)
		}{
			{"TestCoreLifetimeEndingDuringAResetCountsTheResetOnce", TestCoreLifetimeEndingDuringAResetCountsTheResetOnce},
			{"TestCoreDrainDoesNotTakeTheTailOfASplitMessageForReadyForQuery", TestCoreDrainDoesNotTakeTheTailOfASplitMessageForReadyForQuery},
			{"TestCoreHalfOpenBreakerLetsARequestWaitForTheTrial", TestCoreHalfOpenBreakerLetsARequestWaitForTheTrial},
			{"TestCoreArbitrationOneRequestBoundsPreparation", TestCoreArbitrationOneRequestBoundsPreparation},
			{"TestCoreDialsOnDemandAndResetsAfterEveryLease", TestCoreDialsOnDemandAndResetsAfterEveryLease},
			{"TestCoreChecksALongIdleConnectionOffTheRequestPath", TestCoreChecksALongIdleConnectionOffTheRequestPath},
			{"TestCoreCheckThatHangsEndsAtItsDeadlineWithNoClientError", TestCoreCheckThatHangsEndsAtItsDeadlineWithNoClientError},
			{"TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses", TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses},
			{"TestCoreClientVanishedInsideATransactionIsRolledBackAndReused", TestCoreClientVanishedInsideATransactionIsRolledBackAndReused},
			{"TestCoreClientVanishedMidStatementCancelsTheStatement", TestCoreClientVanishedMidStatementCancelsTheStatement},
			{"TestCoreIdleInTransactionIsRolledBackWith25P03", TestCoreIdleInTransactionIsRolledBackWith25P03},
			{"TestCoreBreakerRefusesAtOnceWithNoDial", TestCoreBreakerRefusesAtOnceWithNoDial},
			{"TestCoreClientCancelReachesOnlyTheLentConnection", TestCoreClientCancelReachesOnlyTheLentConnection},
			{"TestCoreCancelWhileQueuedAnswers57014", TestCoreCancelWhileQueuedAnswers57014},
			{"TestCoreOnSwitchLendsTheSameTenantAsIsAndResetsOnASwitch", TestCoreOnSwitchLendsTheSameTenantAsIsAndResetsOnASwitch},
			{"TestCoreReseedFollowsTheResetAndIsJudged", TestCoreReseedFollowsTheResetAndIsJudged},
			{"TestCoreLossMidStatementInsideATransactionIsFatal08006UnlessItMayCommit", TestCoreLossMidStatementInsideATransactionIsFatal08006UnlessItMayCommit},
			{"TestCoreArbitrationTrialFailureRefusesWithIdleSibling", TestCoreArbitrationTrialFailureRefusesWithIdleSibling},
		} {
			t.Run(rule.String()+"/"+tc.name, tc.run)
		}
	}
	coreAdmission = admission.Config{}
}

func TestJoinCancelWhileQueuedLeavesItsTenantQueueAsRemoved(t *testing.T) {
	r := joinRig(t, quietRule, func(c *proxy.Config) { c.Connections = 1 })
	r.firstLease(11, "SELECT 1")
	r.login(12, 1)
	r.q(12, "SELECT 2")
	a := r.cancelFrom(21, r.clientKey(12))
	if !replied(a, 12, "57014") {
		t.Fatalf("queued client got %q", written(a, proxy.ActClientWrite, 12))
	}
	s, as := r.agree()
	if as.Removed != 1 || as.Queued.Value != 0 || as.ShedTotal() != 0 || s.CancelsQueued != 1 {
		t.Fatalf("removed %d queued %d shed %d cancelled while queued %d", as.Removed, as.Queued.Value, as.ShedTotal(), s.CancelsQueued)
	}
}

func TestJoinQuiesceWaitsForTheQuietTenantQueueThenPasses(t *testing.T) {
	for _, rule := range []admission.RuleKind{admission.RuleTimeout, admission.RuleControlLaw} {
		r := joinRig(t, admission.Config{Rule: rule, LIFO: admission.LIFOOff}, nil)
		id := r.firstLease(11, "SELECT 1")
		r.db(id, complete("SELECT 1", 'I'))
		r.finishReset(id)
		r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
		if err := r.e.Quiesce(); err == nil {
			t.Fatalf("%v: quiesce passed with the tenant queue still in its quiet window", rule)
		}
		quiet := time.Duration(r.e.Config().AdmissionConfig().Quiet())
		r.tick(quiet)
		if err := r.e.Quiesce(); err != nil {
			t.Fatalf("%v: after the quiet window: %v", rule, err)
		}
		if p := r.pool(); p.Idle.Value != 1 || p.Open.Value != 1 {
			t.Fatalf("%v: pool idle %d open %d after the quiet window", rule, p.Idle.Value, p.Open.Value)
		}
		r.agree()
	}
}

func (r *coreRig) unchecked(k proxy.EventKind, id proxy.ConnID, b []byte) []proxy.Action {
	return r.e.Step(r.now, proxy.Event{Kind: k, Conn: id, Data: b}, nil)
}

func TestJoinInvariantsRunTheAdmissionChecksOnAnEngineWithThePoolCore(t *testing.T) {
	r := joinRig(t, quietRule, func(c *proxy.Config) {
		c.Connections = 1
		c.Pool.BreakerFailures = 1
		c.Plant |= proxy.PlantBreakerFlushPops
	})
	id := r.firstLease(10, "SELECT pg_sleep(1)")
	r.login(11, 0)
	r.q(11, "SELECT 1")
	r.login(12, 1)
	r.q(12, "SELECT 2")
	r.agree()
	r.unchecked(proxy.EvDBClosed, id, nil)
	if r.pool().Breaker != int64(pool.BreakerOpen) || r.e.Snapshot().BreakerFlushed != 2 {
		t.Fatalf("premise: breaker %d, flushed %d", r.pool().Breaker, r.e.Snapshot().BreakerFlushed)
	}
	err := r.e.Invariants()
	if err == nil || !strings.HasPrefix(err.Error(), "admission:") {
		t.Fatalf("a breaker flush counted as served passed Engine.Invariants: %v", err)
	}
}

func TestJoinInvariantsRunThePoolCoreChecksOnAnEngineWithTheAdmissionQueue(t *testing.T) {
	r := joinRig(t, quietRule, func(c *proxy.Config) {
		c.Connections = 4
		c.Pool.MaxConnecting = 1
		c.Pool.Plant = pool.PlantNoRate
	})
	r.login(10, 0)
	r.q(10, "SELECT 1")
	r.login(11, 1)
	r.unchecked(proxy.EvClientData, 11, query("SELECT 2"))
	if p := r.pool(); p.Connecting.Value != 2 || r.e.AdmissionSnapshot().Queued.Value != 2 {
		t.Fatalf("premise: %d connecting, %d queued", p.Connecting.Value, r.e.AdmissionSnapshot().Queued.Value)
	}
	err := r.e.Invariants()
	if err == nil || !strings.HasPrefix(err.Error(), "pool:") {
		t.Fatalf("a pool above max_connecting passed Engine.Invariants: %v", err)
	}
}

func TestJoinShutdownSweepsTheLastQueuedClientAtItsDeadline(t *testing.T) {
	for _, lifo := range []admission.LIFOMode{admission.LIFOOff, admission.LIFOAdaptive} {
		r := joinRig(t, admission.Config{Rule: admission.RuleTimeout, LIFO: lifo}, func(c *proxy.Config) { c.Connections = 1 })
		r.firstLease(10, "SELECT pg_sleep(1)")
		r.login(11, 1)
		r.q(11, "SELECT 1")
		queued := r.now
		if as := r.e.AdmissionSnapshot(); as.Queued.Value != 1 {
			t.Fatalf("%v: premise: queued %d", lifo, as.Queued.Value)
		}
		r.at(queued + proxy.Instant(admission.DefaultInterval))
		a := r.e.Shutdown(r.now, nil)
		if !replied(a, 11, "53M01") {
			t.Fatalf("%v: the queued client at its deadline got %q, want 53M01", lifo, written(a, proxy.ActClientWrite, 11))
		}
		if err := r.e.Invariants(); err != nil {
			t.Fatalf("%v: after shutdown: %v", lifo, err)
		}
		if err := r.e.Quiesce(); err != nil {
			t.Fatalf("%v: quiesce after shutdown: %v", lifo, err)
		}
		s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
		if as.Shed[admission.ReasonRule] != 1 || s.Shed != 1 || as.Removed != 0 || as.Pushes != as.Served+as.ShedTotal()+as.Removed {
			t.Fatalf("%v: engine shed %d; admission rule sheds %d removed %d pushes %d served %d", lifo, s.Shed,
				as.Shed[admission.ReasonRule], as.Removed, as.Pushes, as.Served)
		}
		if p := r.pool(); p.Open.Value != 0 || p.Connecting.Value != 0 {
			t.Fatalf("%v: pool open %d connecting %d after shutdown", lifo, p.Open.Value, p.Connecting.Value)
		}
	}
}
