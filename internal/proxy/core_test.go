package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

type coreRig struct {
	*rig
	dbs     []proxy.ConnID
	cancels []proxy.ConnID
}

func newCoreRig(t *testing.T, edit func(*proxy.Config)) *coreRig {
	t.Helper()
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 1, Tenants: 3})
	if err != nil {
		t.Fatal(err)
	}
	c := w.Proxy.Engine.Config()
	c.Connections = 2
	c.QueueCap = 4
	c.ReplyBufferCap = proxy.MinReplyBufferCap
	c.Pool = &pool.Config{RatePerSec: 1000, NoJitter: true}
	if edit != nil {
		edit(&c)
	}
	e, err := proxy.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := &coreRig{rig: &rig{t: t, e: e, now: 1}}
	if a := e.Start(r.now, nil); did(a, proxy.ActDBOpen, 1) {
		t.Fatal("the core pool dialled at start with no demand")
	}
	return r
}

func (r *coreRig) run(a []proxy.Action) []proxy.Action {
	r.t.Helper()
	all := a
	for _, x := range a {
		switch x.Kind {
		case proxy.ActClientWrite:
			all = append(all, r.ev(proxy.EvClientWrote, x.Conn, nil, len(x.Data))...)
		case proxy.ActDBWrite:
			all = append(all, r.ev(proxy.EvDBWrote, x.Conn, nil, len(x.Data))...)
		case proxy.ActDBOpen:
			r.dbs = append(r.dbs, x.Conn)
		case proxy.ActDBCancel:
			r.cancels = append(r.cancels, x.Conn)
		}
	}
	return all
}

func (r *coreRig) open(id proxy.ConnID) []proxy.Action {
	r.t.Helper()
	a := r.run(r.ev(proxy.EvDBOpened, id, nil, 0))
	return append(a, r.run(r.ev(proxy.EvDBData, id, pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I'), 0))...)
}

func (r *coreRig) login(id proxy.ConnID, tenant int) {
	r.t.Helper()
	r.ev(proxy.EvClientAccepted, id, nil, 0)
	b := pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: proxysim.TenantName(tenant)}, {Name: "database", Value: proxysim.Database}})
	r.run(r.ev(proxy.EvClientData, id, b[:proxy.FirstClientRead], 0))
	r.run(r.ev(proxy.EvClientData, id, b[proxy.FirstClientRead:], 0))
	if _, ok := r.e.Tenant(id); !ok {
		r.t.Fatal("client did not authenticate")
	}
}

func (r *coreRig) q(id proxy.ConnID, s string) []proxy.Action {
	return r.run(r.ev(proxy.EvClientData, id, pgwire.AppendQuery(nil, []byte(s)), 0))
}

func (r *coreRig) db(id proxy.ConnID, b []byte) []proxy.Action { return r.run(r.ev(proxy.EvDBData, id, b, 0)) }

func (r *coreRig) tick(d time.Duration) []proxy.Action {
	r.now += proxy.Instant(d)
	return r.run(r.ev(proxy.EvTimer, 0, nil, 0))
}

func (r *coreRig) pool() pool.Snapshot {
	s, ok := r.e.PoolSnapshot()
	if !ok {
		r.t.Fatal("no pool core")
	}
	return s
}

func query(s string) []byte { return pgwire.AppendQuery(nil, []byte(s)) }

func emptyReply() []byte {
	return pgwire.AppendReadyForQuery(message(pgwire.MsgEmptyQueryResponse, nil), 'I')
}

func (r *coreRig) firstLease(client proxy.ConnID, sql string) proxy.ConnID {
	r.t.Helper()
	r.login(client, 0)
	r.q(client, sql)
	if len(r.dbs) == 0 {
		r.t.Fatal("no dial for a waiting request")
	}
	id := r.dbs[len(r.dbs)-1]
	a := r.open(id)
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, id), query(sql)) {
		r.t.Fatalf("the request was not forwarded on the new connection: %q", written(a, proxy.ActDBWrite, id))
	}
	return id
}

func (r *coreRig) finishReset(id proxy.ConnID) {
	r.t.Helper()
	r.db(id, complete("DISCARD ALL", 'I'))
	if r.pool().Idle.Value == 0 {
		r.t.Fatal("connection not idle after its reset")
	}
}

func TestCoreDialsOnDemandAndResetsAfterEveryLease(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "SELECT 1")
	a := r.db(id, complete("SELECT 1", 'I'))
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("DISCARD ALL")) {
		t.Fatalf("no reset at release: %q", written(a, proxy.ActDBWrite, id))
	}
	r.finishReset(id)
	a = r.q(11, "SELECT 2")
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("SELECT 2")) || len(r.dbs) != 1 {
		t.Fatal("the idle connection was not reused")
	}
	if s := r.e.Snapshot(); s.LeasesEndedReset != 1 || s.ResetsCompleted != 1 {
		t.Fatalf("leases %d resets %d", s.LeasesEndedReset, s.ResetsCompleted)
	}
}

func TestCoreChecksALongIdleConnectionOffTheRequestPath(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "SELECT 1")
	r.db(id, complete("SELECT 1", 'I'))
	r.finishReset(id)
	r.tick(pool.DefaultCheckAfterIdle)
	a := r.q(11, "SELECT 2")
	if got := written(a, proxy.ActDBWrite, id); !bytes.Equal(got, query("")) {
		t.Fatalf("wrote %q, want the empty-query check first", got)
	}
	a = r.db(id, emptyReply())
	if got := written(a, proxy.ActDBWrite, id); !bytes.Equal(got, query("SELECT 2")) {
		t.Fatalf("after the check passed: %q", got)
	}
	if s := r.pool(); s.ChecksByResult[pool.CheckOK] != 1 || s.IdleAgeAtLend.Count == 0 {
		t.Fatalf("checks %v", s.ChecksByResult)
	}
}

func TestCoreCheckThatHangsEndsAtItsDeadlineWithNoClientError(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "SELECT 1")
	r.db(id, complete("SELECT 1", 'I'))
	r.finishReset(id)
	r.tick(pool.DefaultCheckAfterIdle)
	r.q(11, "SELECT 2")
	a := r.tick(proxy.DefaultCheckTimeout - 1)
	if did(a, proxy.ActDBClose, id) {
		t.Fatal("closed before the check deadline")
	}
	a = r.tick(1)
	if !did(a, proxy.ActDBClose, id) || len(written(a, proxy.ActClientWrite, 11)) != 0 {
		t.Fatalf("at the check deadline: closed %v, client got %q", did(a, proxy.ActDBClose, id), written(a, proxy.ActClientWrite, 11))
	}
	if len(r.dbs) != 2 {
		t.Fatalf("no replacement dial: %v", r.dbs)
	}
	a = r.open(r.dbs[1])
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, r.dbs[1]), query("SELECT 2")) {
		t.Fatal("the queued request was not served by the replacement")
	}
	if s := r.pool(); s.ChecksByResult[pool.CheckTimeout] != 1 || s.Closes[pool.CloseCheckFailed] != 1 {
		t.Fatalf("checks %v closes %v", s.ChecksByResult, s.Closes)
	}
}

func TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) { c.Timeouts.Statement = time.Second })
	id := r.firstLease(11, "SELECT v FROM kv WHERE k = 'a'")
	a := r.tick(time.Second)
	if !bytes.Contains(written(a, proxy.ActClientWrite, 11), []byte("08006")) {
		t.Fatalf("client got %q, want 08006", written(a, proxy.ActClientWrite, 11))
	}
	if len(r.cancels) != 1 || did(a, proxy.ActDBClose, id) {
		t.Fatalf("cancels %v closed %v", r.cancels, did(a, proxy.ActDBClose, id))
	}
	cancelErr := pgwire.AppendErrorResponse(nil, "ERROR", "57014", "canceling statement due to user request")
	a = r.db(id, pgwire.AppendReadyForQuery(cancelErr, 'I'))
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("DISCARD ALL")) {
		t.Fatalf("after the drain: %q", written(a, proxy.ActDBWrite, id))
	}
	r.db(id, complete("DISCARD ALL", 'I'))
	if r.pool().Idle.Value != 0 {
		t.Fatal("idle while its cancel is still in flight")
	}
	r.run(r.ev(proxy.EvDBCancelDone, r.cancels[0], nil, 0))
	s := r.pool()
	if s.Idle.Value != 1 || s.NoReply != 1 || s.DirtyReused[pool.EndStatementDeadline] != 1 || r.e.Snapshot().NoReply != 1 {
		t.Fatalf("idle %d no reply %d dirty %v", s.Idle.Value, s.NoReply, s.DirtyReused)
	}
	if s.Suspect.Value != 1 {
		t.Fatalf("a zero-reply deadline must leave the connection suspect, got %d", s.Suspect.Value)
	}
}

func TestCoreClientVanishedInsideATransactionIsRolledBackAndReused(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "BEGIN")
	r.db(id, complete("BEGIN", 'T'))
	a := r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("ROLLBACK")) {
		t.Fatalf("wrote %q, want ROLLBACK", written(a, proxy.ActDBWrite, id))
	}
	a = r.db(id, complete("ROLLBACK", 'I'))
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("DISCARD ALL")) {
		t.Fatal("no reset after the rollback")
	}
	r.finishReset(id)
	if s := r.pool(); s.DirtyReused[pool.EndVanishedInTx] != 1 || s.Rollbacks != 1 {
		t.Fatalf("dirty %v", s.DirtyReused)
	}
}

func TestCoreClientVanishedMidStatementCancelsTheStatement(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "SELECT pg_sleep(5)")
	r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
	if len(r.cancels) != 1 {
		t.Fatal("no cancel for the vanished client's statement")
	}
	r.db(id, pgwire.AppendReadyForQuery(pgwire.AppendErrorResponse(nil, "ERROR", "57014", "cancel"), 'I'))
	r.db(id, complete("DISCARD ALL", 'I'))
	r.run(r.ev(proxy.EvDBCancelDone, r.cancels[0], nil, 0))
	if s := r.pool(); s.DirtyReused[pool.EndVanishedMidStatement] != 1 || s.Idle.Value != 1 {
		t.Fatalf("dirty %v idle %d", s.DirtyReused, s.Idle.Value)
	}
}

func TestCoreIdleInTransactionIsRolledBackWith25P03(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "BEGIN")
	r.db(id, complete("BEGIN", 'T'))
	a := r.tick(proxy.DefaultIdleInTx - 1)
	if did(a, proxy.ActClientClose, 11) {
		t.Fatal("closed before the idle-in-transaction deadline")
	}
	a = r.tick(1)
	if !bytes.Contains(written(a, proxy.ActClientWrite, 11), []byte("25P03")) || !did(a, proxy.ActClientClose, 11) {
		t.Fatalf("client got %q", written(a, proxy.ActClientWrite, 11))
	}
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("ROLLBACK")) {
		t.Fatal("no rollback on the database")
	}
	r.db(id, complete("ROLLBACK", 'I'))
	r.finishReset(id)
	if s := r.pool(); s.DirtyReused[pool.EndIdleInTx] != 1 {
		t.Fatalf("dirty %v", s.DirtyReused)
	}
}

func TestCoreBreakerRefusesAtOnceWithNoDial(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) { c.Pool.BreakerFailures = 2 })
	r.login(11, 0)
	r.q(11, "SELECT 1")
	for i := 0; i < 2; i++ {
		r.run(r.ev(proxy.EvDBDialFailed, r.dbs[len(r.dbs)-1], nil, 0))
		r.tick(10 * time.Millisecond)
	}
	if r.pool().Breaker != int64(pool.BreakerOpen) {
		t.Fatalf("breaker %d", r.pool().Breaker)
	}
	dials := len(r.dbs)
	a := r.q(11, "SELECT 2")
	if !bytes.Contains(written(a, proxy.ActClientWrite, 11), []byte("53M03")) || len(r.dbs) != dials {
		t.Fatalf("client got %q; dials %d -> %d", written(a, proxy.ActClientWrite, 11), dials, len(r.dbs))
	}
	if !r.e.Snapshot().DBDown {
		t.Fatal("database not reported down while the breaker is open")
	}
}

func TestCoreClientCancelReachesOnlyTheLentConnection(t *testing.T) {
	r := newCoreRig(t, nil)
	r.login(12, 1)
	id := r.firstLease(11, "SELECT pg_sleep(5)")
	key := r.clientKey(11)
	a := r.cancelFrom(21, key)
	if len(r.cancels) != 1 || !did(a, proxy.ActDBCancel, r.cancels[0]) {
		t.Fatal("no cancel forwarded")
	}
	var sent []byte
	for _, x := range a {
		if x.Kind == proxy.ActDBCancel {
			sent = x.Data
		}
	}
	if len(sent) != 16 {
		t.Fatalf("cancel request %x", sent)
	}
	r.cancelFrom(22, r.clientKey(12))
	if len(r.cancels) != 1 || r.e.Snapshot().CancelsIgnored != 1 {
		t.Fatalf("an idle client's cancel was forwarded: %v", r.cancels)
	}
	_ = id
}

func (r *coreRig) clientKey(id proxy.ConnID) pgwire.BackendKey {
	k, ok := r.e.ClientKey(id)
	if !ok {
		r.t.Fatal("no key")
	}
	return k
}

func (r *coreRig) cancelFrom(id proxy.ConnID, k pgwire.BackendKey) []proxy.Action {
	r.ev(proxy.EvClientAccepted, id, nil, 0)
	b := pgwire.AppendCancelRequest(nil, k)
	a := r.run(r.ev(proxy.EvClientData, id, b[:proxy.FirstClientRead], 0))
	return append(a, r.run(r.ev(proxy.EvClientData, id, b[proxy.FirstClientRead:], 0))...)
}

func TestCoreCancelWhileQueuedAnswers57014(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) { c.Connections = 1 })
	id := r.firstLease(11, "SELECT 1")
	r.login(12, 1)
	r.q(12, "SELECT 2")
	a := r.cancelFrom(21, r.clientKey(12))
	if !bytes.Contains(written(a, proxy.ActClientWrite, 12), []byte("57014")) {
		t.Fatalf("queued client got %q", written(a, proxy.ActClientWrite, 12))
	}
	_ = id
}

func TestCoreOnSwitchLendsTheSameTenantAsIsAndResetsOnASwitch(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) { c.Reset = proxy.ResetOnSwitch; c.Connections = 1 })
	id := r.firstLease(11, "SELECT 1")
	a := r.db(id, complete("SELECT 1", 'I'))
	if len(written(a, proxy.ActDBWrite, id)) != 0 {
		t.Fatal("on-switch reset at release")
	}
	a = r.q(11, "SELECT 2")
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("SELECT 2")) {
		t.Fatal("same tenant not lent as is")
	}
	r.db(id, complete("SELECT 1", 'I'))
	r.login(12, 1)
	a = r.q(12, "SELECT 3")
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("DISCARD ALL")) {
		t.Fatalf("switching tenant wrote %q, want the reset first", written(a, proxy.ActDBWrite, id))
	}
	a = r.db(id, complete("DISCARD ALL", 'I'))
	if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("SELECT 3")) {
		t.Fatal("the other tenant was not served after the reset")
	}
	if s := r.pool(); s.TenantSwitches != 1 || s.LentAsIs != 1 {
		t.Fatalf("switches %d as-is %d", s.TenantSwitches, s.LentAsIs)
	}
}

func TestCoreReseedFollowsTheResetAndIsJudged(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) { c.Reseed = true })
	id := r.firstLease(11, "SELECT 1")
	a := r.db(id, complete("SELECT 1", 'I'))
	got := written(a, proxy.ActDBWrite, id)
	if !bytes.HasPrefix(got, query("DISCARD ALL")) || !bytes.Contains(got, []byte("SELECT setseed(0.")) {
		t.Fatalf("wrote %q", got)
	}
	r.db(id, complete("DISCARD ALL", 'I'))
	if r.pool().Idle.Value != 0 {
		t.Fatal("idle before the re-seed reply")
	}
	reply := append(message(pgwire.MsgRowDescription, []byte{0, 0}), message(pgwire.MsgDataRow, []byte{0, 1, 0xff, 0xff, 0xff, 0xff})...)
	r.db(id, append(reply, complete("SELECT 1", 'I')...))
	if r.pool().Idle.Value != 1 || r.e.Snapshot().Reseeds != 1 {
		t.Fatal("the re-seed reply did not complete the reset")
	}
}

func TestCoreLossMidStatementInsideATransactionIsFatal08006UnlessItMayCommit(t *testing.T) {
	for _, c := range []struct {
		sql  string
		code string
	}{{"UPDATE kv SET v = 'x' WHERE k = 'a'", "08006"}, {"COMMIT", "08007"}, {"end", "08007"}} {
		r := newCoreRig(t, nil)
		id := r.firstLease(11, "BEGIN")
		r.db(id, complete("BEGIN", 'T'))
		r.q(11, c.sql)
		a := r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
		out := written(a, proxy.ActClientWrite, 11)
		if !bytes.Contains(out, []byte(c.code)) {
			t.Fatalf("%s: client got %q, want %s", c.sql, out, c.code)
		}
		if c.code == "08006" && (!bytes.Contains(out, []byte("FATAL")) || !did(a, proxy.ActClientClose, 11)) {
			t.Fatalf("%s: want FATAL and a close, got %q", c.sql, out)
		}
	}
}

func TestCoreQuiesceAndShutdownLeaveNothing(t *testing.T) {
	r := newCoreRig(t, nil)
	id := r.firstLease(11, "SELECT 1")
	r.db(id, complete("SELECT 1", 'I'))
	r.finishReset(id)
	r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
	if err := r.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
	a := r.e.Shutdown(r.now, nil)
	if !did(a, proxy.ActDBClose, id) {
		t.Fatal("shutdown did not close the pooled connection")
	}
	if s := r.pool(); s.Open.Value != 0 || s.Closes[pool.CloseShutdown] != 1 {
		t.Fatalf("open %d closes %v", s.Open.Value, s.Closes)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}
