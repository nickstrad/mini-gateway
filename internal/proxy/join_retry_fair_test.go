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
