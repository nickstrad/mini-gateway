package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func (r *coreRig) cancelledThenReset(client proxy.ConnID, sql string) proxy.ConnID {
	r.t.Helper()
	id := r.firstLease(client, sql)
	r.cancelFrom(client+10, r.clientKey(client))
	if len(r.cancels) != 1 {
		r.t.Fatal("no cancel forwarded")
	}
	r.db(id, complete("SELECT 1", 'I'))
	r.db(id, complete("DISCARD ALL", 'I'))
	if r.pool().Idle.Value != 0 {
		r.t.Fatal("idle while its cancel is in flight")
	}
	r.run(r.ev(proxy.EvDBCancelDone, r.cancels[0], nil, 0))
	return id
}

func TestCoreCancelDoneMakesTheConnectionIdleInTheEngineToo(t *testing.T) {
	t.Run("quiesce", func(t *testing.T) {
		r := newCoreRig(t, func(c *proxy.Config) { c.Timeouts.Statement = time.Second })
		id := r.firstLease(11, "SELECT v FROM kv WHERE k = 'a'")
		r.tick(time.Second)
		cancelErr := pgwire.AppendErrorResponse(nil, "ERROR", "57014", "canceling statement due to user request")
		r.db(id, pgwire.AppendReadyForQuery(cancelErr, 'I'))
		r.db(id, complete("DISCARD ALL", 'I'))
		r.run(r.ev(proxy.EvDBCancelDone, r.cancels[0], nil, 0))
		r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
		if s := r.e.Snapshot(); s.DBIdle.Value != 1 || r.pool().Suspect.Value != 1 {
			t.Fatalf("engine idle %d suspect %d after a zero-reply deadline", s.DBIdle.Value, r.pool().Suspect.Value)
		}
		q := newCoreRig(t, nil)
		q.cancelledThenReset(11, "SELECT pg_sleep(5)")
		q.run(q.ev(proxy.EvClientClosed, 11, nil, 0))
		if s := q.e.Snapshot(); s.DBIdle.Value != 1 || q.e.Quiesce() != nil {
			t.Fatalf("engine idle %d, quiesce %v", s.DBIdle.Value, q.e.Quiesce())
		}
	})
	t.Run("reuse", func(t *testing.T) {
		r := newCoreRig(t, nil)
		id := r.cancelledThenReset(11, "SELECT pg_sleep(5)")
		a := r.q(11, "SELECT 2")
		if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("SELECT 2")) || len(r.dbs) != 1 {
			t.Fatalf("not reused: %q dials %v", written(a, proxy.ActDBWrite, id), r.dbs)
		}
		if s := r.e.Snapshot(); s.DBIdle.Value != 0 || s.DBLent.Value != 1 {
			t.Fatalf("engine idle %d lent %d", s.DBIdle.Value, s.DBLent.Value)
		}
	})
	t.Run("unsolicited bytes", func(t *testing.T) {
		r := newCoreRig(t, nil)
		id := r.cancelledThenReset(11, "SELECT pg_sleep(5)")
		a := r.db(id, []byte("X"))
		if !did(a, proxy.ActDBClose, id) {
			t.Fatal("bytes on an idle connection did not close it")
		}
		r.q(11, "SELECT 2")
		r.tick(time.Millisecond)
		if len(r.dbs) != 2 {
			t.Fatalf("no new dial after the idle connection closed: %v", r.dbs)
		}
	})
}

func TestCoreLifetimeEndingDuringAResetCountsTheResetOnce(t *testing.T) {
	r := newCoreRig(t, func(c *proxy.Config) {
		c.Pool.MaxLifetime = 2 * time.Second
		c.Pool.IdleTimeout = time.Hour
		c.Pool.CheckAfterIdle = time.Hour
	})
	id := r.firstLease(11, "SELECT 1")
	r.tick(1900 * time.Millisecond)
	r.db(id, complete("SELECT 1", 'I'))
	r.tick(200 * time.Millisecond)
	a := r.db(id, complete("DISCARD ALL", 'I'))
	s := r.e.Snapshot()
	if !did(a, proxy.ActDBClose, id) || s.DBCloses[proxy.CloseDBLifetime] != 1 {
		t.Fatalf("closed %v lifetime closes %d", did(a, proxy.ActDBClose, id), s.DBCloses[proxy.CloseDBLifetime])
	}
	failed := int64(0)
	for _, v := range s.ResetsFailed {
		failed += v
	}
	if s.ResetsStarted != 1 || s.ResetsCompleted != 1 || failed != 0 {
		t.Fatalf("resets started %d completed %d failed %d", s.ResetsStarted, s.ResetsCompleted, failed)
	}
}

func TestCoreDrainDoesNotTakeTheTailOfASplitMessageForReadyForQuery(t *testing.T) {
	for _, tail := range [][]byte{{'Z', 0, 0, 0, 5, 'I'}, {'Z', 'I', 'x', 'y', 'z'}} {
		r := newCoreRig(t, nil)
		id := r.firstLease(11, "SELECT v FROM kv WHERE k = 'a'")
		body := append([]byte{0, 1, 0, 0, 0, byte(len(tail))}, tail...)
		row := message(pgwire.MsgDataRow, body)
		r.db(id, row[:len(row)-len(tail)])
		r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
		a := r.db(id, row[len(row)-len(tail):])
		if len(written(a, proxy.ActDBWrite, id)) != 0 || did(a, proxy.ActDBClose, id) {
			t.Fatalf("tail %q: wrote %q closed %v before the reply ended", tail, written(a, proxy.ActDBWrite, id), did(a, proxy.ActDBClose, id))
		}
		a = r.db(id, complete("SELECT 1", 'I'))
		if !bytes.Equal(written(a, proxy.ActDBWrite, id), query("DISCARD ALL")) {
			t.Fatalf("tail %q: after the reply ended wrote %q", tail, written(a, proxy.ActDBWrite, id))
		}
	}
}

func TestCoreHalfOpenBreakerLetsARequestWaitForTheTrial(t *testing.T) {
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
	r.tick(pool.DefaultBreakerOpenFor)
	dials := len(r.dbs)
	a := r.q(11, "SELECT 3")
	if bytes.Contains(written(a, proxy.ActClientWrite, 11), []byte("53M03")) || len(r.dbs) != dials+1 {
		t.Fatalf("half-open: client got %q, dials %d -> %d, want the request queued behind one trial", written(a, proxy.ActClientWrite, 11), dials, len(r.dbs))
	}
	trial := r.dbs[len(r.dbs)-1]
	a = r.open(trial)
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, trial), query("SELECT 3")) || r.pool().Breaker != int64(pool.BreakerClosed) {
		t.Fatalf("after the trial: wrote %q breaker %d", written(a, proxy.ActDBWrite, trial), r.pool().Breaker)
	}
}
