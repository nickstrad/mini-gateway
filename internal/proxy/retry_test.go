package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/retry/retrytest"
)

const insertSQL = "INSERT INTO kv VALUES ('k', 'v')"

func retryOn(c *proxy.Config) { c.Retry = retry.Config{Kind: retry.KindPreForwardOnce} }

func newRetryRig(t *testing.T, edit func(*proxy.Config)) *coreRig {
	t.Helper()
	return newCoreRig(t, func(c *proxy.Config) {
		retryOn(c)
		if edit != nil {
			edit(c)
		}
	})
}

func (r *coreRig) idleConn(client proxy.ConnID) proxy.ConnID {
	r.t.Helper()
	id := r.firstLease(client, "SELECT 1")
	r.db(id, complete("SELECT 1", 'I'))
	r.finishReset(id)
	return id
}

func (r *coreRig) send(client proxy.ConnID, sql string) []proxy.Action {
	r.t.Helper()
	a := r.ev(proxy.EvClientData, client, query(sql), 0)
	for _, x := range a {
		if x.Kind == proxy.ActClientWrite {
			r.ev(proxy.EvClientWrote, x.Conn, nil, len(x.Data))
		}
	}
	return a
}

func (r *coreRig) refuseAndClose(id proxy.ConnID) []proxy.Action {
	r.t.Helper()
	a := r.ev(proxy.EvDBWriteRefused, id, nil, 0)
	return append(a, r.run(r.ev(proxy.EvDBClosed, id, nil, 0))...)
}

func (r *coreRig) retrySnap() retry.Snapshot {
	r.t.Helper()
	s, ok := r.e.RetrySnapshot()
	if !ok {
		r.t.Fatal("no retry core")
	}
	return s
}

func (r *coreRig) lastDial() proxy.ConnID {
	r.t.Helper()
	r.tick(2 * time.Millisecond)
	return r.dbs[len(r.dbs)-1]
}

// TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply
// proves a zero-byte write refusal queues one retry holding no connection,
// dials a replacement, forwards the statement, and delivers exactly one reply.
func TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	a := r.send(11, insertSQL)
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query(insertSQL)) {
		t.Fatalf("not lent to the idle connection: %q", written(a, proxy.ActDBWrite, id))
	}
	a = r.refuseAndClose(id)
	if len(written(a, proxy.ActClientWrite, 11)) != 0 {
		t.Fatalf("the client got a reply before its retry ran: %q", written(a, proxy.ActClientWrite, 11))
	}
	s := r.e.Snapshot()
	if s.RetryAsked != 1 || s.RetriedRequests != 1 || s.RetryPending.Value != 1 || s.Failed != 0 || s.Queued.Value != 1 {
		t.Fatalf("asked %d retried %d pending %d failed %d queued %d", s.RetryAsked, s.RetriedRequests, s.RetryPending.Value, s.Failed, s.Queued.Value)
	}
	if s.DBLent.Value != 0 {
		t.Fatal("the waiting retry holds a connection")
	}
	next := r.lastDial()
	if next == id {
		t.Fatal("no new dial for the retry")
	}
	a = r.open(next)
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, next), query(insertSQL)) {
		t.Fatalf("the retry was not forwarded on the new connection: %q", written(a, proxy.ActDBWrite, next))
	}
	a = r.db(next, complete("INSERT 0 1", 'I'))
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Equal(got, complete("INSERT 0 1", 'I')) {
		t.Fatalf("client reply %q", got)
	}
	s = r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndCompleted] != 1 || s.RetryPending.Value != 0 || s.Completed != 2 || s.ByRow[pgwire.ErrRowLostAfterRead] != 0 {
		t.Fatalf("ends %v pending %d completed %d rows %v", s.RetryEnds, s.RetryPending.Value, s.Completed, s.ByRow)
	}
	p := r.retrySnap()
	if p.Attempts != 2 || p.Retries != 1 || p.Withdrawals != retry.WithdrawTenths {
		t.Fatalf("policy attempts %d retries %d withdrawals %d", p.Attempts, p.Retries, p.Withdrawals)
	}
}

// TestRetryAtMostOncePerRequest proves a second zero-byte refusal on the
// replacement connection is refused with RefusedOnce and answered 08006.
func TestRetryAtMostOncePerRequest(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	next := r.lastDial()
	r.run(r.ev(proxy.EvDBOpened, next, nil, 0))
	a := r.ev(proxy.EvDBData, next, pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I'), 0)
	if !did(a, proxy.ActDBWrite, next) {
		t.Fatal("retry not forwarded")
	}
	a = r.refuseAndClose(next)
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08006")) {
		t.Fatalf("second refusal: client got %q", got)
	}
	s := r.e.Snapshot()
	p := r.retrySnap()
	if s.RetryAsked != 1 || s.RetryOffered != 2 || p.Refused[retry.RefusedOnce] != 1 || s.RetryEnds[proxy.RetryEndFailed] != 1 {
		t.Fatalf("asked %d offered %d once %d ends %v", s.RetryAsked, s.RetryOffered, p.Refused[retry.RefusedOnce], s.RetryEnds)
	}
}

// TestRetryNeverAfterBytesWereAccepted proves even one accepted byte marks the
// failure Reached == Yes, refuses with RefusedReached, and answers 08007.
func TestRetryNeverAfterBytesWereAccepted(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.ev(proxy.EvDBWrote, id, nil, 1)
	a := r.refuseAndClose(id)
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08007")) {
		t.Fatalf("client got %q", got)
	}
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedReached] != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryNeverOnACloseWithoutAWriteReport proves a backend close without
// EvDBWriteRefused is treated as Reached == Yes and never retried.
func TestRetryNeverOnACloseWithoutAWriteReport(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	a := r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08007")) {
		t.Fatalf("client got %q", got)
	}
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedReached] != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryNeverAfterARelayedByte proves relaying any reply byte prevents a
// retry even if EvDBWriteRefused arrives afterward.
func TestRetryNeverAfterARelayedByte(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, "SELECT v FROM kv")
	rowDesc := message(pgwire.MsgRowDescription, []byte{0, 0})
	r.run(r.ev(proxy.EvDBData, id, rowDesc, 0))
	r.ev(proxy.EvDBWriteRefused, id, nil, 0)
	r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedReached] != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryNeverOnStatementSilence proves a statement timeout is treated as
// Reached == Yes, refused with RefusedReached, and answered 08007.
func TestRetryNeverOnStatementSilence(t *testing.T) {
	r := newRetryRig(t, func(c *proxy.Config) { c.Timeouts.Statement = time.Second })
	id := r.idleConn(11)
	r.send(11, insertSQL)
	a := r.tick(time.Second)
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08007")) {
		t.Fatalf("client got %q", got)
	}
	_ = id
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedReached] != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryNeverInsideATransactionWhateverReachedSays proves a zero-byte
// refusal inside an open transaction is refused with RefusedInTx and FATAL 08006.
func TestRetryNeverInsideATransactionWhateverReachedSays(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.firstLease(11, "BEGIN")
	r.db(id, complete("BEGIN", 'T'))
	r.send(11, insertSQL)
	a := r.refuseAndClose(id)
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08006")) {
		t.Fatalf("client got %q", got)
	}
	s := r.e.Snapshot()
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedInTx] != 1 || s.ReachedNo != 1 {
		t.Fatalf("policy %+v reached-no %d", p, s.ReachedNo)
	}
}

// TestRetryNeverWhenTheLossOpensTheBreaker proves the pool breaker sees the
// loss before offerRetry, so a loss that opens the breaker is refused RefusedBreaker.
func TestRetryNeverWhenTheLossOpensTheBreaker(t *testing.T) {
	r := newRetryRig(t, func(c *proxy.Config) { c.Pool.BreakerFailures = 1 })
	id := r.idleConn(11)
	r.send(11, insertSQL)
	a := r.refuseAndClose(id)
	if r.pool().Breaker == int64(pool.BreakerClosed) {
		t.Fatal("the breaker did not open")
	}
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08006")) {
		t.Fatalf("client got %q", got)
	}
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedBreaker] != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryDecidedWithoutConsultingTheBreakersAllow proves checking the
// breaker for retries uses a pure read and neither starts a trial nor changes state.
func TestRetryDecidedWithoutConsultingTheBreakersAllow(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	before := r.pool()
	r.refuseAndClose(id)
	after := r.pool()
	if after.BreakerTrials != before.BreakerTrials || after.Breaker != int64(pool.BreakerClosed) {
		t.Fatalf("breaker trials %d -> %d, state %d", before.BreakerTrials, after.BreakerTrials, after.Breaker)
	}
	if p := r.retrySnap(); p.Retries != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryNeverWhileDraining proves a zero-byte refusal while the engine is
// draining is refused with RefusedDraining.
func TestRetryNeverWhileDraining(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.ev(proxy.EvDraining, 0, nil, 0)
	r.e.Step(r.now, proxy.Event{Kind: proxy.EvDraining, On: true}, nil)
	r.refuseAndClose(id)
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedDraining] != 1 {
		t.Fatalf("%+v", p)
	}
}

func deadlineRig(t *testing.T) (*coreRig, proxy.ConnID) {
	r := newRetryRig(t, func(c *proxy.Config) {
		c.Connections = 1
		c.Timeouts.Statement = time.Second
	})
	id := r.idleConn(11)
	r.login(12, 1)
	r.send(11, "SELECT 2")
	r.send(12, insertSQL)
	if r.e.Snapshot().Queued.Value != 1 {
		t.Fatal("second request not queued")
	}
	return r, id
}

func (r *coreRig) releaseAndLendTo(id proxy.ConnID) {
	r.t.Helper()
	r.run(r.ev(proxy.EvDBData, id, complete("SELECT 1", 'I'), 0))
	a := r.ev(proxy.EvDBData, id, complete("DISCARD ALL", 'I'), 0)
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query(insertSQL)) {
		r.t.Fatalf("queued request not lent: %q", written(a, proxy.ActDBWrite, id))
	}
}

// TestRetryRefusedAtTheRequestDeadlineExactly proves a failure at exact
// equality (now == arrival + timeouts.statement) is refused with RefusedDeadline.
func TestRetryRefusedAtTheRequestDeadlineExactly(t *testing.T) {
	r, id := deadlineRig(t)
	arrived := r.now
	r.now += proxy.Instant(500 * time.Millisecond)
	r.releaseAndLendTo(id)
	r.now = arrived + proxy.Instant(time.Second)
	r.refuseAndClose(id)
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedDeadline] != 1 {
		t.Fatalf("%+v", p)
	}
}

// TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt proves a retry
// granted 1 ns before the request deadline is shed 53M01 at the exact deadline.
func TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt(t *testing.T) {
	r, id := deadlineRig(t)
	arrived := r.now
	r.now += proxy.Instant(500 * time.Millisecond)
	r.releaseAndLendTo(id)
	r.now = arrived + proxy.Instant(time.Second) - 1
	r.refuseAndClose(id)
	if p := r.retrySnap(); p.Retries != 1 {
		t.Fatalf("%+v", p)
	}
	a := r.tick(1)
	if got := written(a, proxy.ActClientWrite, 12); !bytes.Contains(got, []byte("53M01")) || r.now != arrived+proxy.Instant(time.Second) {
		t.Fatalf("the queued retry was not ended at the request deadline: client got %q at %d", got, r.now-arrived)
	}
	if s := r.e.Snapshot(); s.RetryEnds[proxy.RetryEndShed] != 1 {
		t.Fatalf("%v", s.RetryEnds)
	}
}

// TestRetryWaitsInTheTenantQueueWithTheAdmissionRule proves a granted retry
// re-enters the tenant's admission queue rather than a separate buffer.
func TestRetryWaitsInTheTenantQueueWithTheAdmissionRule(t *testing.T) {
	r := newRetryRig(t, func(c *proxy.Config) {
		c.Admission = admission.Config{Rule: admission.RuleTimeout, Interval: int64(10 * time.Second), Target: int64(5 * time.Second)}
	})
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	if s := r.e.AdmissionSnapshot(); s.Queued.Value != 1 {
		t.Fatalf("the retry is not in the tenant queue: queued %d", s.Queued.Value)
	}
	a := r.open(r.lastDial())
	if !did(a, proxy.ActDBWrite, r.lastDial()) {
		t.Fatal("retry not lent")
	}
}

// TestRetryShutdownWithAPendingRetryEndsItAndConserves proves Engine.Shutdown
// ends a queued retry under RetryEndGone so Invariants and Quiesce pass.
func TestRetryShutdownWithAPendingRetryEndsItAndConserves(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	r.e.Shutdown(r.now, nil)
	s := r.e.Snapshot()
	if s.RetryPending.Value != 0 || s.RetryEnds[proxy.RetryEndGone] != 1 {
		t.Fatalf("pending %d ends %v", s.RetryPending.Value, s.RetryEnds)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
	if err := r.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
}

// TestRetryClientGoneWhileTheRetryIsQueued proves a client closing while its
// retry waits in the queue records RetryEndGone and clears RetryPending.
func TestRetryClientGoneWhileTheRetryIsQueued(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
	if s := r.e.Snapshot(); s.RetryEnds[proxy.RetryEndGone] != 1 || s.RetryPending.Value != 0 {
		t.Fatalf("%v", s.RetryEnds)
	}
}

// TestRetryOffByDefaultAnswersTheRow proves the default Off policy refuses an
// eligible zero-byte loss with RefusedOff and answers 08006.
func TestRetryOffByDefaultAnswersTheRow(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	a := r.refuseAndClose(id)
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08006")) {
		t.Fatalf("client got %q", got)
	}
	s := r.e.Snapshot()
	p, _ := r.e.RetrySnapshot()
	if s.RetryAsked != 0 || s.RetryOffered != 1 || p.Refused[retry.RefusedOff] != 1 || p.Attempts != 2 {
		t.Fatalf("asked %d offered %d policy %+v", s.RetryAsked, s.RetryOffered, p)
	}
}

// TestRetryEverythingAblationRerunsAWriteThatMayHaveReached proves the
// test-only Everything ablation policy re-forwards an accepted write after close.
func TestRetryEverythingAblationRerunsAWriteThatMayHaveReached(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) { c.Seams.Retry = proxy.NewRetryCore(retrytest.NewEverything()) })
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.ev(proxy.EvDBWrote, id, nil, len(query(insertSQL)))
	r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
	a := r.open(r.lastDial())
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, r.lastDial()), query(insertSQL)) {
		t.Fatal("the ablation policy did not re-send the write")
	}
}

// TestRetryHealthIsUnchangedByRetries proves enabling retries leaves the
// engine's health inputs (DBDown, Draining, Breaker) identical to Off.
func TestRetryHealthIsUnchangedByRetries(t *testing.T) {
	on := newRetryRig(t, nil)
	off := newCoreRig(t, nil)
	for _, r := range []*coreRig{on, off} {
		id := r.idleConn(11)
		r.send(11, insertSQL)
		r.refuseAndClose(id)
	}
	a, b := on.e.Snapshot(), off.e.Snapshot()
	if a.DBDown != b.DBDown || a.Draining != b.Draining || on.pool().Breaker != off.pool().Breaker {
		t.Fatalf("health inputs differ: down %v/%v draining %v/%v breaker %d/%d", a.DBDown, b.DBDown, a.Draining, b.Draining,
			on.pool().Breaker, off.pool().Breaker)
	}
}

// TestRetryForwardedStatementEndsAtTheRequestDeadline proves a forwarded retry
// clamps its statement timer to arrival + timeouts.statement and answers 08007 there.
func TestRetryForwardedStatementEndsAtTheRequestDeadline(t *testing.T) {
	r, id := deadlineRig(t)
	arrived := r.now
	r.now += proxy.Instant(500 * time.Millisecond)
	r.releaseAndLendTo(id)
	r.now = arrived + proxy.Instant(700*time.Millisecond)
	r.refuseAndClose(id)
	next := r.lastDial()
	if a := r.open(next); !bytes.HasSuffix(written(a, proxy.ActDBWrite, next), query(insertSQL)) {
		t.Fatal("retry not forwarded")
	}
	deadline := arrived + proxy.Instant(time.Second)
	a := r.tick(time.Duration(deadline - r.now - 1))
	if len(written(a, proxy.ActClientWrite, 12)) != 0 {
		t.Fatal("answered before the request deadline")
	}
	a = r.tick(1)
	if got := written(a, proxy.ActClientWrite, 12); !bytes.Contains(got, []byte("08007")) || r.now != deadline {
		t.Fatalf("client got %q at %d", got, r.now-arrived)
	}
}

// TestRetryNeverInsideATransactionMidStatement proves a mid-statement backend
// close inside a transaction is refused with RefusedInTx and FATAL 08006.
func TestRetryNeverInsideATransactionMidStatement(t *testing.T) {
	r := newRetryRig(t, nil)
	id := r.firstLease(11, "BEGIN")
	r.db(id, complete("BEGIN", 'T'))
	r.send(11, insertSQL)
	r.ev(proxy.EvDBWrote, id, nil, len(query(insertSQL)))
	a := r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("08006")) || !bytes.Contains(got, []byte("FATAL")) {
		t.Fatalf("client got %q", got)
	}
	if p := r.retrySnap(); p.Retries != 0 || p.Refused[retry.RefusedInTx] != 1 {
		t.Fatalf("%+v", p)
	}
}
