package proxy_test

import (
	"bytes"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type heldRetry struct {
	*coreRig
	first, second, other, spare proxy.ConnID
	arrived                     proxy.Instant
}

func (r *coreRig) heldTx(client proxy.ConnID, tenant int) proxy.ConnID {
	r.t.Helper()
	id := r.lease(client, tenant, "BEGIN")
	r.db(id, complete("BEGIN", 'T'))
	return id
}

func (r *coreRig) commit(client, id proxy.ConnID) []proxy.Action {
	r.t.Helper()
	a := r.q(client, "COMMIT")
	a = append(a, r.db(id, complete("COMMIT", 'I'))...)
	a = append(a, r.db(id, complete("DISCARD ALL", 'I'))...)
	if bytes.HasSuffix(written(a, proxy.ActDBWrite, id), query("")) {
		a = append(a, r.db(id, emptyReply())...)
	}
	return a
}

func retryForwardedOn(a []proxy.Action) (proxy.ConnID, bool) {
	for _, x := range a {
		if x.Kind == proxy.ActDBWrite && bytes.Contains(x.Data, query(insertSQL)) {
			return x.Conn, true
		}
	}
	return 0, false
}

func (r *coreRig) dials() int { return len(r.dbs) }

func retryHeldAtShare(t *testing.T, edit func(*proxy.Config)) *heldRetry {
	t.Helper()
	r := fairRig(t, 4, func(c *proxy.Config) {
		retryOn(c)
		if edit != nil {
			edit(c)
		}
	})
	h := &heldRetry{coreRig: r}
	h.first = r.heldTx(10, 0)
	h.second = r.heldTx(11, 0)
	h.spare = r.lease(12, 0, "SELECT 9")
	r.db(h.spare, complete("SELECT 9", 'I'))
	r.finishReset(h.spare)
	r.login(13, 0)
	h.arrived = r.now
	if a := r.send(13, insertSQL); !bytes.Equal(written(a, proxy.ActDBWrite, h.spare), query(insertSQL)) {
		t.Fatalf("premise: the write was not lent to the idle connection: %q", written(a, proxy.ActDBWrite, h.spare))
	}
	h.other = r.heldTx(14, 1)
	if tenantInFlight(r, 0) != 3 || tenantInFlight(r, 1) != 1 {
		t.Fatalf("premise: in flight %d / %d", tenantInFlight(r, 0), tenantInFlight(r, 1))
	}
	_, before := r.agree()
	if a := r.refuseAndClose(h.spare); len(written(a, proxy.ActClientWrite, 13)) != 0 {
		t.Fatalf("the client got a reply before its retry ran: %q", written(a, proxy.ActClientWrite, 13))
	}
	s, as := r.agree()
	if s.RetryAsked != 1 || s.RetryPending.Value != 1 || s.Queued.Value != 1 || r.queuedOf(0) != 1 {
		t.Fatalf("premise: asked %d pending %d queued %d", s.RetryAsked, s.RetryPending.Value, s.Queued.Value)
	}
	if tenantInFlight(r, 0) != 2 || tenantInFlight(r, 1) != 1 || as.Fair.InFlight.Value != 3 || as.Fair.LeaseEnds != before.Fair.LeaseEnds+1 {
		t.Fatalf("the lost lease was not reported ended before the retry waits: in flight %d / %d, total %d, ends %d -> %d",
			tenantInFlight(r, 0), tenantInFlight(r, 1), as.Fair.InFlight.Value, before.Fair.LeaseEnds, as.Fair.LeaseEnds)
	}
	dials := r.dials()
	r.noRetryForward(r.tick(2 * time.Millisecond))
	if r.dials() != dials {
		t.Fatalf("the retry of a tenant at its share was counted as demand: %d dials", r.dials()-dials)
	}
	return h
}

func (r *coreRig) queuedOf(tenant int) int {
	return r.e.TenantQueued(work.TenantID(proxysim.TenantName(tenant)))
}

func (h *heldRetry) noForwardOfTheRetry(a []proxy.Action) { h.noRetryForward(a) }

func (h *coreRig) noRetryForward(a []proxy.Action) {
	h.t.Helper()
	for _, x := range a {
		if x.Kind == proxy.ActDBWrite && bytes.Contains(x.Data, query(insertSQL)) {
			h.t.Fatalf("the retry was forwarded on %d", x.Conn)
		}
	}
}

func TestJoinARetryHeldBackAtItsShareOpensNoConnection(t *testing.T) {
	h := retryHeldAtShare(t, nil)
	dials, p := h.dials(), h.pool()
	for range 5 {
		h.noForwardOfTheRetry(h.tick(100 * time.Millisecond))
	}
	if h.dials() != dials || h.pool().Open.Value+h.pool().Connecting.Value != p.Open.Value+p.Connecting.Value {
		t.Fatalf("a held-back retry caused %d dials", h.dials()-dials)
	}
	if s, _ := h.agree(); s.RetryPending.Value != 1 || s.Queued.Value != 1 {
		t.Fatalf("pending %d queued %d", s.RetryPending.Value, s.Queued.Value)
	}
}

func TestJoinARetriedTenantAtItsShareLetsTheTenantBelowItsShareGoFirst(t *testing.T) {
	h := retryHeldAtShare(t, nil)
	dials := h.dials()
	h.login(15, 1)
	h.q(15, "SELECT 15")
	if h.dials() != dials+1 {
		t.Fatalf("no dial for the releasable request of the tenant below its share: %d", h.dials()-dials)
	}
	fresh := h.dbs[len(h.dbs)-1]
	a := h.open(fresh)
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, fresh), query("SELECT 15")) {
		t.Fatalf("the new connection went to %q, not to the tenant below its share", written(a, proxy.ActDBWrite, fresh))
	}
	if s, _ := h.agree(); s.RetryPending.Value != 1 || tenantInFlight(h.coreRig, 0) != 2 || tenantInFlight(h.coreRig, 1) != 2 {
		t.Fatalf("pending %d in flight %d / %d", s.RetryPending.Value, tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 1))
	}
	h.db(fresh, complete("SELECT 15", 'I'))
	h.noForwardOfTheRetry(h.db(fresh, complete("DISCARD ALL", 'I')))
	on, ok := retryForwardedOn(h.commit(10, h.first))
	if !ok || on != fresh {
		t.Fatalf("the retry was not lent to the idle connection when its tenant's own lease ended: forwarded %v on %d", ok, on)
	}
	a = h.db(fresh, complete("INSERT 0 1", 'I'))
	if got := written(a, proxy.ActClientWrite, 13); !bytes.Equal(got, complete("INSERT 0 1", 'I')) {
		t.Fatalf("client reply %q", got)
	}
	s, as := h.agree()
	if s.RetryEnds[proxy.RetryEndCompleted] != 1 || s.RetryPending.Value != 0 || s.Completed < 3 {
		t.Fatalf("ends %v pending %d completed %d", s.RetryEnds, s.RetryPending.Value, s.Completed)
	}
	if as.Fair.LeaseStarts != s.LeasesStarted || as.Fair.LeaseEnds != s.LeasesEnded || as.Fair.InFlight.Value != 2 {
		t.Fatalf("fair starts %d ends %d in flight %d, engine starts %d ends %d", as.Fair.LeaseStarts, as.Fair.LeaseEnds, as.Fair.InFlight.Value, s.LeasesStarted, s.LeasesEnded)
	}
	if tenantInFlight(h.coreRig, 0) != 1 || tenantInFlight(h.coreRig, 1) != 1 {
		t.Fatalf("in flight %d / %d after the retry and the other tenant's request completed", tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 1))
	}
}

func TestJoinARetryHeldBackAtItsShareIsLentWhenItsOwnLeaseEnds(t *testing.T) {
	h := retryHeldAtShare(t, nil)
	h.noForwardOfTheRetry(h.tick(10 * time.Millisecond))
	_, before := h.agree()
	if on, ok := retryForwardedOn(h.commit(10, h.first)); !ok || on != h.first {
		t.Fatalf("the retry was not lent at the end of its tenant's own lease: forwarded %v on %d", ok, on)
	}
	_, as := h.agree()
	if as.Fair.LeaseStarts != before.Fair.LeaseStarts+1 || tenantInFlight(h.coreRig, 0) != 2 {
		t.Fatalf("the retry's lease was started %d times, tenant in flight %d", as.Fair.LeaseStarts-before.Fair.LeaseStarts, tenantInFlight(h.coreRig, 0))
	}
}

func TestJoinARetryHeldBackByTheDispatcherEndsAtTheRequestDeadline(t *testing.T) {
	h := retryHeldAtShare(t, func(c *proxy.Config) { c.Timeouts.Statement = time.Second })
	deadline := h.arrived + proxy.Instant(time.Second)
	h.noForwardOfTheRetry(h.tick(time.Duration(deadline-h.now) - 1))
	if s, _ := h.agree(); s.RetryPending.Value != 1 {
		t.Fatal("the retry ended before the request deadline")
	}
	a := h.tick(1)
	h.noForwardOfTheRetry(a)
	if got := written(a, proxy.ActClientWrite, 13); !bytes.Contains(got, []byte("53M01")) || h.now != deadline {
		t.Fatalf("the held-back retry was not ended at the request deadline: client got %q at %d", got, h.now-h.arrived)
	}
	s, as := h.agree()
	if s.RetryEnds[proxy.RetryEndShed] != 1 || s.RetryPending.Value != 0 || as.Fair.InFlight.Value != 3 {
		t.Fatalf("ends %v pending %d in flight %d", s.RetryEnds, s.RetryPending.Value, as.Fair.InFlight.Value)
	}
}

func TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseEndsWhileARetryIsHeldBack(t *testing.T) {
	for _, open := range []bool{false, true} {
		b := &switchBreaker{}
		h := retryHeldAtShare(t, func(c *proxy.Config) { c.Seams.Breaker = b })
		b.open = open
		a := h.commit(14, h.other)
		on, forwarded := retryForwardedOn(a)
		forwarded = forwarded && on == h.other
		s, _ := h.agree()
		if !open {
			if !forwarded || s.RetryEnds[proxy.RetryEndBreaker] != 0 {
				t.Fatalf("breaker closed: the other tenant's freed connection must go to the retry: wrote %q, ends %v", written(a, proxy.ActDBWrite, h.other), s.RetryEnds)
			}
			continue
		}
		h.noForwardOfTheRetry(a)
		if got := written(a, proxy.ActClientWrite, 13); !bytes.Contains(got, []byte("53M03")) {
			t.Fatalf("breaker open: client got %q", got)
		}
		if s.RetryEnds[proxy.RetryEndBreaker] != 1 || s.RetryPending.Value != 0 || s.Queued.Value != 0 || tenantInFlight(h.coreRig, 0) != 2 {
			t.Fatalf("ends %v pending %d queued %d in flight %d", s.RetryEnds, s.RetryPending.Value, s.Queued.Value, tenantInFlight(h.coreRig, 0))
		}
	}
}

func TestJoinThePoolBreakerOpeningFlushesARetryHeldBackByTheDispatcher(t *testing.T) {
	h := retryHeldAtShare(t, func(c *proxy.Config) { c.Pool.BreakerFailures = 2 })
	h.login(15, 1)
	h.q(15, "SELECT 15")
	a := h.run(h.ev(proxy.EvDBDialFailed, h.dbs[len(h.dbs)-1], nil, 0))
	h.noForwardOfTheRetry(a)
	if !replied(a, 13, "53M03") || !replied(a, 15, "53M03") {
		t.Fatalf("the breaker's flush did not answer both waiting requests: %q / %q", written(a, proxy.ActClientWrite, 13), written(a, proxy.ActClientWrite, 15))
	}
	s, as := h.agree()
	if s.RetryEnds[proxy.RetryEndShed] != 1 || s.RetryPending.Value != 0 || s.BreakerFlushed != 2 || as.Fair.InFlight.Value != 3 {
		t.Fatalf("ends %v pending %d flushed %d in flight %d", s.RetryEnds, s.RetryPending.Value, s.BreakerFlushed, as.Fair.InFlight.Value)
	}
}

func TestJoinShutdownWithAHeldBackRetryAndLeasesOfTwoTenants(t *testing.T) {
	h := retryHeldAtShare(t, nil)
	h.login(15, 1)
	h.q(15, "SELECT 15")
	h.open(h.dbs[len(h.dbs)-1])
	if tenantInFlight(h.coreRig, 0) != 2 || tenantInFlight(h.coreRig, 1) != 2 {
		t.Fatalf("premise: in flight %d / %d", tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 1))
	}
	h.run(h.e.Shutdown(h.now, nil))
	if err := h.e.Invariants(); err != nil {
		t.Fatal(err)
	}
	if err := h.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
	s, as := h.e.Snapshot(), h.e.AdmissionSnapshot()
	if err := as.Check(); err != nil {
		t.Fatal(err)
	}
	if s.RetryEnds[proxy.RetryEndGone] != 1 || s.RetryPending.Value != 0 || s.RetriedRequests != 1 {
		t.Fatalf("ends %v pending %d retried %d", s.RetryEnds, s.RetryPending.Value, s.RetriedRequests)
	}
	if as.Fair.InFlight.Value != 0 || as.Fair.States.Value != 0 || as.Fair.LeaseStarts != as.Fair.LeaseEnds || as.Fair.LeaseStarts != s.LeasesStarted || s.LeasesStarted != s.LeasesEnded {
		t.Fatalf("fair in flight %d states %d starts %d ends %d, engine starts %d ends %d", as.Fair.InFlight.Value, as.Fair.States.Value, as.Fair.LeaseStarts, as.Fair.LeaseEnds, s.LeasesStarted, s.LeasesEnded)
	}
}

func forwardOrder(t *testing.T, refuse bool) []string {
	r := fairRig(t, 1, retryOn)
	id := r.idleConn(20)
	r.login(10, 0)
	if a := r.send(10, insertSQL); !bytes.Equal(written(a, proxy.ActDBWrite, id), query(insertSQL)) {
		t.Fatal("premise: the write was not lent")
	}
	for _, c := range []struct {
		client proxy.ConnID
		tenant int
	}{{11, 1}, {12, 0}, {13, 1}} {
		r.login(c.client, c.tenant)
		r.q(c.client, "SELECT "+strconv.Itoa(int(c.client)))
	}
	queries := []string{"SELECT 11", "SELECT 12", "SELECT 13", insertSQL}
	conn := id
	var a []proxy.Action
	if refuse {
		r.refuseAndClose(id)
		conn = r.lastDial()
		a = r.open(conn)
	} else {
		r.db(id, complete("INSERT 0 1", 'I'))
		a = r.db(id, complete("DISCARD ALL", 'I'))
	}
	var order []string
	for range 5 {
		w := written(a, proxy.ActDBWrite, conn)
		if len(w) == 0 {
			break
		}
		sql := ""
		for _, c := range queries {
			if bytes.HasSuffix(w, query(c)) {
				sql = c
			}
		}
		if sql == "" {
			t.Fatalf("unexpected write %q", w)
		}
		order = append(order, sql)
		r.db(conn, complete("OK", 'I'))
		a = r.db(conn, complete("DISCARD ALL", 'I'))
	}
	s, _ := r.agree()
	if s.RetryPending.Value != 0 || s.Queued.Value != 0 {
		t.Fatalf("pending %d queued %d", s.RetryPending.Value, s.Queued.Value)
	}
	return order
}

func TestJoinARetryTakesANewTurnBehindItsTenantsQueueAndNoTenantGainsService(t *testing.T) {
	plain := forwardOrder(t, false)
	retried := forwardOrder(t, true)
	wantPlain := []string{"SELECT 11", "SELECT 12", "SELECT 13"}
	wantRetried := []string{"SELECT 11", "SELECT 12", "SELECT 13", insertSQL}
	if !slices.Equal(plain, wantPlain) {
		t.Fatalf("without a retry the order is %q, want %q", plain, wantPlain)
	}
	if !slices.Equal(retried, wantRetried) {
		t.Fatalf("with a retry the order is %q, want %q: the retry waits behind its tenant's queued request and the other tenant keeps its turns", retried, wantRetried)
	}
}

func TestJoinEveryRetryEngineTestHoldsBehindTheFairDispatcher(t *testing.T) {
	defer func() { coreAdmission = admission.Config{} }()
	for _, rule := range []admission.RuleKind{admission.RuleTimeout, admission.RuleControlLaw} {
		a := quietRule
		a.Rule = rule
		coreAdmission = a
		if !newRetryRig(t, nil).e.AdmissionSnapshot().Fair.On {
			t.Fatal("the retry rig does not run the fair dispatcher")
		}
		for _, tc := range []struct {
			name string
			run  func(*testing.T)
		}{
			{"TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply", TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply},
			{"TestRetryAtMostOncePerRequest", TestRetryAtMostOncePerRequest},
			{"TestRetryNeverAfterBytesWereAccepted", TestRetryNeverAfterBytesWereAccepted},
			{"TestRetryNeverOnACloseWithoutAWriteReport", TestRetryNeverOnACloseWithoutAWriteReport},
			{"TestRetryNeverAfterARelayedByte", TestRetryNeverAfterARelayedByte},
			{"TestRetryNeverOnStatementSilence", TestRetryNeverOnStatementSilence},
			{"TestRetryNeverInsideATransactionWhateverReachedSays", TestRetryNeverInsideATransactionWhateverReachedSays},
			{"TestRetryNeverInsideATransactionMidStatement", TestRetryNeverInsideATransactionMidStatement},
			{"TestRetryNeverWhenTheLossOpensTheBreaker", TestRetryNeverWhenTheLossOpensTheBreaker},
			{"TestRetryDecidedWithoutConsultingTheBreakersAllow", TestRetryDecidedWithoutConsultingTheBreakersAllow},
			{"TestRetryNeverWhileDraining", TestRetryNeverWhileDraining},
			{"TestRetryRefusedAtTheRequestDeadlineExactly", TestRetryRefusedAtTheRequestDeadlineExactly},
			{"TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt", TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt},
			{"TestRetryForwardedStatementEndsAtTheRequestDeadline", TestRetryForwardedStatementEndsAtTheRequestDeadline},
			{"TestRetryShutdownWithAPendingRetryEndsItAndConserves", TestRetryShutdownWithAPendingRetryEndsItAndConserves},
			{"TestRetryClientGoneWhileTheRetryIsQueued", TestRetryClientGoneWhileTheRetryIsQueued},
			{"TestRetryOffByDefaultAnswersTheRow", TestRetryOffByDefaultAnswersTheRow},
			{"TestRetryEverythingAblationRerunsAWriteThatMayHaveReached", TestRetryEverythingAblationRerunsAWriteThatMayHaveReached},
			{"TestRetryHealthIsUnchangedByRetries", TestRetryHealthIsUnchangedByRetries},
			{"TestRetryBudgetExhaustionIsVisibleAndCounted", TestRetryBudgetExhaustionIsVisibleAndCounted},
			{"TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded", TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded},
			{"TestRetryStillForwardedWhenTheBreakerStaysClosed", TestRetryStillForwardedWhenTheBreakerStaysClosed},
			{"TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward", TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward},
			{"TestRetryQueuedAndCancelledByTheClientEndsFailed", TestRetryQueuedAndCancelledByTheClientEndsFailed},
			{"TestRetryQueuedWhenTheBreakerFlushesEndsShed", TestRetryQueuedWhenTheBreakerFlushesEndsShed},
			{"TestRetryInFlightWhenTheClientCloses", TestRetryInFlightWhenTheClientCloses},
		} {
			t.Run(rule.String()+"/"+tc.name, tc.run)
		}
	}
}

const secondInsertSQL = "INSERT INTO kv VALUES ('k2', 'v')"

func (r *coreRig) idleConns(tenant int, clients ...proxy.ConnID) []proxy.ConnID {
	r.t.Helper()
	var ids []proxy.ConnID
	for _, c := range clients {
		ids = append(ids, r.lease(c, tenant, "SELECT "+strconv.Itoa(int(c))))
	}
	for i, id := range ids {
		r.db(id, complete("SELECT "+strconv.Itoa(int(clients[i])), 'I'))
		r.finishReset(id)
	}
	return ids
}

func (r *coreRig) lendWrite(client proxy.ConnID, tenant int, sql string, on proxy.ConnID) {
	r.t.Helper()
	r.login(client, tenant)
	if a := r.send(client, sql); !bytes.Equal(written(a, proxy.ActDBWrite, on), query(sql)) {
		r.t.Fatalf("premise: %q was not lent to %d: %q", sql, on, written(a, proxy.ActDBWrite, on))
	}
}

func TestJoinARetryOverflowingItsTenantQueueShedsTheEarlierWaitingRetry(t *testing.T) {
	r := fairRig(t, 2, func(c *proxy.Config) {
		retryOn(c)
		c.Admission.TenantCapacity = 1
	})
	ids := r.idleConns(0, 20, 21)
	r.lendWrite(10, 0, insertSQL, ids[0])
	r.lendWrite(11, 0, secondInsertSQL, ids[1])
	if a := r.refuseAndClose(ids[0]); len(written(a, proxy.ActClientWrite, 10)) != 0 {
		t.Fatalf("the first write's client got a reply before its retry ran: %q", written(a, proxy.ActClientWrite, 10))
	}
	if s, _ := r.agree(); s.RetryPending.Value != 1 || r.queuedOf(0) != 1 {
		t.Fatalf("premise: pending %d queued %d", s.RetryPending.Value, r.queuedOf(0))
	}
	a := r.refuseAndClose(ids[1])
	if !replied(a, 10, "53M01") || len(written(a, proxy.ActClientWrite, 11)) != 0 {
		t.Fatalf("the second retry did not take the full tenant ring's only slot: client 10 got %q, client 11 got %q", written(a, proxy.ActClientWrite, 10), written(a, proxy.ActClientWrite, 11))
	}
	s, as := r.agree()
	if s.RetryAsked != 2 || s.RetryPending.Value != 1 || s.RetryEnds[proxy.RetryEndShed] != 1 || as.Shed[admission.ReasonOverflow] != 1 || r.queuedOf(0) != 1 {
		t.Fatalf("asked %d pending %d ends %v overflow %d queued %d", s.RetryAsked, s.RetryPending.Value, s.RetryEnds, as.Shed[admission.ReasonOverflow], r.queuedOf(0))
	}
	fresh := r.lastDial()
	a = r.open(fresh)
	if on, ok := retryForwardedOn(a); ok {
		t.Fatalf("the shed retry was forwarded on %d", on)
	}
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, fresh), query(secondInsertSQL)) {
		t.Fatalf("the waiting retry was not lent to the new connection: %q", written(a, proxy.ActDBWrite, fresh))
	}
	if got := written(r.db(fresh, complete("INSERT 0 1", 'I')), proxy.ActClientWrite, 11); !bytes.Equal(got, complete("INSERT 0 1", 'I')) {
		t.Fatalf("client reply %q", got)
	}
	s, as = r.agree()
	if s.RetryPending.Value != 0 || s.RetryEnds[proxy.RetryEndShed] != 1 || s.RetryEnds[proxy.RetryEndCompleted] != 1 || as.Fair.LeaseStarts != s.LeasesStarted || as.Fair.LeaseEnds != s.LeasesEnded {
		t.Fatalf("pending %d ends %v fair starts %d ends %d engine %d %d", s.RetryPending.Value, s.RetryEnds, as.Fair.LeaseStarts, as.Fair.LeaseEnds, s.LeasesStarted, s.LeasesEnded)
	}
}

func TestJoinARetryRefusedAtTheTenantCapIsShedAndEndsItsRetry(t *testing.T) {
	r := fairRig(t, 1, func(c *proxy.Config) {
		retryOn(c)
		c.Admission.MaxTenants = 2
	})
	id := r.idleConn(20)
	r.lendWrite(10, 0, insertSQL, id)
	for _, c := range []struct {
		client proxy.ConnID
		tenant int
	}{{11, 1}, {12, 2}} {
		r.login(c.client, c.tenant)
		r.q(c.client, "SELECT "+strconv.Itoa(int(c.client)))
	}
	if _, as := r.agree(); as.Queued.Value != 2 || r.queuedOf(0) != 0 {
		t.Fatalf("premise: queued %d, tenant 0 queued %d", as.Queued.Value, r.queuedOf(0))
	}
	a := r.refuseAndClose(id)
	if !replied(a, 10, "53M01") {
		t.Fatalf("the retry refused at the tenant cap was not answered 53M01: %q", written(a, proxy.ActClientWrite, 10))
	}
	r.noRetryForward(a)
	s, as := r.agree()
	if s.RetryAsked != 1 || s.RetryPending.Value != 0 || s.RetryEnds[proxy.RetryEndShed] != 1 || as.Shed[admission.ReasonTenants] != 1 || as.Queued.Value != 2 {
		t.Fatalf("asked %d pending %d ends %v refused at the tenant cap %d queued %d", s.RetryAsked, s.RetryPending.Value, s.RetryEnds, as.Shed[admission.ReasonTenants], as.Queued.Value)
	}
	if as.Fair.InFlight.Value != 0 || tenantInFlight(r, 0) != 0 {
		t.Fatalf("in flight %d, tenant 0 %d", as.Fair.InFlight.Value, tenantInFlight(r, 0))
	}
}

func TestJoinARetryAboveItsShareTakesAFreeLeaseButNotTheLastOneReserved(t *testing.T) {
	var q *admission.Queues
	r := fairRig(t, 6, func(c *proxy.Config) {
		retryOn(c)
		q = seamFair(t, c)
	})
	var held []proxy.ConnID
	for c := range proxy.ConnID(4) {
		held = append(held, r.heldTx(10+c, 0))
	}
	spare := r.idleConns(0, 20)[0]
	r.lendWrite(14, 0, insertSQL, spare)
	r.heldTx(15, 1)
	if tenantInFlight(r, 0) != 5 || tenantInFlight(r, 1) != 1 || r.e.AdmissionSnapshot().Fair.InFlight.Value != 6 {
		t.Fatalf("premise: in flight %d / %d", tenantInFlight(r, 0), tenantInFlight(r, 1))
	}
	dials := r.dials()
	r.refuseAndClose(spare)
	r.noRetryForward(r.tick(2 * time.Millisecond))
	s, as := r.agree()
	if s.RetryPending.Value != 1 || r.queuedOf(0) != 1 || tenantInFlight(r, 0) != 4 || as.Fair.InFlight.Value != 5 || q.Releasable() != 0 || r.dials() != dials {
		t.Fatalf("a retry of a tenant above its share took the last lease reserved for tenant 1: pending %d queued %d in flight %d releasable %d dials %d",
			s.RetryPending.Value, r.queuedOf(0), tenantInFlight(r, 0), q.Releasable(), r.dials()-dials)
	}
	on, ok := retryForwardedOn(r.commit(10, held[0]))
	if !ok || on != held[0] {
		t.Fatalf("two leases free: the retry of a tenant at its share was not lent the freed connection: forwarded %v on %d", ok, on)
	}
	if tenantInFlight(r, 0) != 4 || r.e.AdmissionSnapshot().Fair.InFlight.Value != 5 {
		t.Fatalf("in flight %d / total %d after the retry was lent", tenantInFlight(r, 0), r.e.AdmissionSnapshot().Fair.InFlight.Value)
	}
	if r.dials() != dials+1 {
		t.Fatalf("premise: one dial for the releasable retry while the freed connection was reset, got %d", r.dials()-dials)
	}
	last := r.dbs[len(r.dbs)-1]
	r.open(last)
	if r.pool().Idle.Value != 1 {
		t.Fatalf("premise: idle %d", r.pool().Idle.Value)
	}
	_, before := r.agree()
	r.login(16, 0)
	a := r.q(16, "SELECT 16")
	a = append(a, r.tick(2*time.Millisecond)...)
	_, as = r.agree()
	if len(written(a, proxy.ActDBWrite, last)) != 0 || r.queuedOf(0) != 1 || q.Releasable() != 0 || as.Fair.HeldShare == before.Fair.HeldShare || r.dials() != dials+1 {
		t.Fatalf("the last lease was not held back for tenant 1: wrote %q, tenant 0 queued %d, releasable %d, held at share %d -> %d, dials %d",
			written(a, proxy.ActDBWrite, last), r.queuedOf(0), q.Releasable(), before.Fair.HeldShare, as.Fair.HeldShare, r.dials()-dials)
	}
	r.login(17, 1)
	if a := r.q(17, "SELECT 17"); !bytes.HasSuffix(written(a, proxy.ActDBWrite, last), query("SELECT 17")) {
		t.Fatalf("the last lease went to %q, not to tenant 1 below its share", written(a, proxy.ActDBWrite, last))
	}
	if got := written(r.db(held[0], complete("INSERT 0 1", 'I')), proxy.ActClientWrite, 14); !bytes.Equal(got, complete("INSERT 0 1", 'I')) {
		t.Fatalf("client reply %q", got)
	}
	if s, _ := r.agree(); s.RetryPending.Value != 0 || s.RetryEnds[proxy.RetryEndCompleted] != 1 {
		t.Fatalf("pending %d ends %v", s.RetryPending.Value, s.RetryEnds)
	}
}

func seamFair(t *testing.T, c *proxy.Config) *admission.Queues {
	t.Helper()
	aq, err := proxy.NewAdmissionQueue(c.AdmissionConfig())
	if err != nil {
		t.Fatal(err)
	}
	f, err := admission.NewFair(c.FairConfig())
	if err != nil {
		t.Fatal(err)
	}
	aq.Core().SetDispatcher(f)
	c.Seams.Queue = aq
	return aq.Core()
}

func TestJoinARetryHeldByAFairLimitBelowTheConnectionsIsNoDemand(t *testing.T) {
	var q *admission.Queues
	r := fairRig(t, 4, func(c *proxy.Config) {
		retryOn(c)
		c.Fairness.Limit = 3
		q = seamFair(t, c)
	})
	releasable := func(want int, when string) {
		t.Helper()
		if got := q.Releasable(); got != want {
			t.Fatalf("%s: releasable %d, want %d", when, got, want)
		}
	}
	if !r.e.AdmissionSnapshot().Fair.On || q.Limit() != 3 {
		t.Fatalf("premise: fair %v limit %d", r.e.AdmissionSnapshot().Fair.On, q.Limit())
	}
	r.heldTx(10, 0)
	other := r.heldTx(11, 1)
	spare := r.idleConns(0, 20)[0]
	r.lendWrite(12, 0, insertSQL, spare)
	r.login(13, 2)
	r.q(13, "SELECT 13")
	dials := r.dials()
	r.tick(2 * time.Millisecond)
	releasable(0, "at the fair limit")
	if r.dials() != dials {
		t.Fatalf("premise: %d dials past the fair limit", r.dials()-dials)
	}
	r.refuseAndClose(spare)
	releasable(1, "the retry's lost lease ended")
	fresh := r.lastDial()
	if r.dials() != dials+1 {
		t.Fatalf("one lease free: %d dials, want 1", r.dials()-dials)
	}
	a := r.open(fresh)
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, fresh), query("SELECT 13")) {
		t.Fatalf("the free lease went to %q, not to tenant 2 below its share", written(a, proxy.ActDBWrite, fresh))
	}
	releasable(0, "the retry waits at the fair limit")
	for range 5 {
		r.noRetryForward(r.tick(100 * time.Millisecond))
	}
	if p := r.pool(); r.dials() != dials+1 || p.Open.Value+p.Connecting.Value != 3 {
		t.Fatalf("a retry held at the fair limit below pool.connections was demand: %d dials, open %d connecting %d", r.dials()-dials, p.Open.Value, p.Connecting.Value)
	}
	if s, as := r.agree(); s.RetryPending.Value != 1 || r.queuedOf(0) != 1 || as.Fair.InFlight.Value != 3 {
		t.Fatalf("pending %d queued %d in flight %d", s.RetryPending.Value, r.queuedOf(0), as.Fair.InFlight.Value)
	}
	on, ok := retryForwardedOn(r.commit(11, other))
	if !ok || on != other {
		t.Fatalf("the retry was not lent the connection freed below the fair limit: forwarded %v on %d", ok, on)
	}
	releasable(0, "the retry was lent")
	if r.dials() != dials+1 {
		t.Fatalf("%d dials after the retry was lent", r.dials()-dials)
	}
	r.db(other, complete("INSERT 0 1", 'I'))
	if s, _ := r.agree(); s.RetryPending.Value != 0 || s.RetryEnds[proxy.RetryEndCompleted] != 1 {
		t.Fatalf("pending %d ends %v", s.RetryPending.Value, s.RetryEnds)
	}
}

func TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseIsLostWhileARetryIsHeldBack(t *testing.T) {
	for _, open := range []bool{false, true} {
		b := &switchBreaker{}
		h := retryHeldAtShare(t, func(c *proxy.Config) { c.Seams.Breaker = b })
		h.login(15, 1)
		h.q(15, "SELECT pg_sleep(1)")
		running := h.dbs[len(h.dbs)-1]
		h.open(running)
		h.noForwardOfTheRetry(h.commit(14, h.other))
		if h.pool().Idle.Value != 1 || tenantInFlight(h.coreRig, 1) != 1 || h.queuedOf(0) != 1 {
			t.Fatalf("premise: idle %d, tenant 1 in flight %d, tenant 0 queued %d", h.pool().Idle.Value, tenantInFlight(h.coreRig, 1), h.queuedOf(0))
		}
		b.open = open
		a := h.run(h.ev(proxy.EvDBClosed, running, nil, 0))
		if bytes.Equal(written(a, proxy.ActDBWrite, h.other), query("")) {
			a = append(a, h.db(h.other, emptyReply())...)
		}
		on, forwarded := retryForwardedOn(a)
		s, _ := h.agree()
		if !open {
			if !forwarded || on != h.other || s.RetryEnds[proxy.RetryEndBreaker] != 0 {
				t.Fatalf("breaker closed: tenant 1's lease was lost and the idle connection must go to the retry: forwarded %v on %d, ends %v", forwarded, on, s.RetryEnds)
			}
			continue
		}
		h.noForwardOfTheRetry(a)
		if !replied(a, 13, "53M03") {
			t.Fatalf("breaker open: client got %q", written(a, proxy.ActClientWrite, 13))
		}
		if s.RetryEnds[proxy.RetryEndBreaker] != 1 || s.RetryPending.Value != 0 || s.Queued.Value != 0 || tenantInFlight(h.coreRig, 0) != 2 {
			t.Fatalf("ends %v pending %d queued %d in flight %d", s.RetryEnds, s.RetryPending.Value, s.Queued.Value, tenantInFlight(h.coreRig, 0))
		}
	}
}
