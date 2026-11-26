package proxy_test

import (
	"bytes"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// TestResetReplyAtItsDeadlineIsLate proves a DISCARD ALL reply arriving at the exact reset deadline fails the reset.
func TestResetReplyAtItsDeadlineIsLate(t *testing.T) {
	r := newRig(t, nil)
	r.leaseThenReset(11, 12)
	r.at(r.now + proxy.Instant(r.e.Config().Timeouts.Reset))
	a := r.ev(proxy.EvDBData, r.db, complete("DISCARD ALL", 'I'), 0)
	if did(a, proxy.ActDBWrite, r.db) || !did(a, proxy.ActDBClose, r.db) {
		t.Fatal("a reset reply at its deadline lent the connection")
	}
	if s := r.e.Snapshot(); s.ResetsFailed[proxy.ResetFailDeadline] != 1 || s.ResetsCompleted != 0 {
		t.Fatalf("failed %v completed %d", s.ResetsFailed, s.ResetsCompleted)
	}
}

// TestResetReplyJustBeforeItsDeadlineCompletes proves a DISCARD ALL reply arriving 1 ns before the deadline succeeds.
func TestResetReplyJustBeforeItsDeadlineCompletes(t *testing.T) {
	r := newRig(t, nil)
	r.leaseThenReset(11, 12)
	r.at(r.now + proxy.Instant(r.e.Config().Timeouts.Reset) - 1)
	a := r.ev(proxy.EvDBData, r.db, complete("DISCARD ALL", 'I'), 0)
	if !did(a, proxy.ActDBWrite, r.db) || r.e.Snapshot().ResetsCompleted != 1 {
		t.Fatal("a reset reply before its deadline did not complete")
	}
}

// TestStatementReplyAtItsDeadlineIsLate proves a statement reply arriving at the exact statement deadline times out.
func TestStatementReplyAtItsDeadlineIsLate(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	r.ack(r.query(11, "SELECT 1"))
	r.at(r.now + proxy.Instant(r.e.Config().Timeouts.Statement))
	a := r.ev(proxy.EvDBData, r.db, complete("SELECT 1", 'I'), 0)
	if bytes.Contains(written(a, proxy.ActClientWrite, 11), []byte("SELECT 1")) || !did(a, proxy.ActDBClose, r.db) {
		t.Fatalf("a reply at the statement deadline was relayed: %q", written(a, proxy.ActClientWrite, 11))
	}
	if s := r.e.Snapshot(); s.DBCloses[proxy.CloseDBStatementDeadline] != 1 || s.Completed != 0 {
		t.Fatalf("closes %v completed %d", s.DBCloses, s.Completed)
	}
}

// TestMessageRestAtTheMidMessageDeadlineIsLate proves trailing bytes of a partial message arriving at the mid-message deadline are late.
func TestMessageRestAtTheMidMessageDeadlineIsLate(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	q := pgwire.AppendQuery(nil, []byte("SELECT 1"))
	r.ev(proxy.EvClientData, 11, q[:3], 0)
	r.at(r.now + proxy.Instant(r.e.Config().Timeouts.ClientMidMessage))
	a := r.ev(proxy.EvClientData, 11, q[3:], 0)
	if !did(a, proxy.ActClientClose, 11) || did(a, proxy.ActDBWrite, r.db) {
		t.Fatal("the rest of a message at its deadline was accepted")
	}
}

// TestConnectionFreedAtTheQueueDeadlineDoesNotServeTheExpiredRequest proves a connection becoming idle at the queue deadline sheds the expired request.
func TestConnectionFreedAtTheQueueDeadlineDoesNotServeTheExpiredRequest(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	r.client(12)
	r.ack(r.query(11, "SELECT 1"))
	r.query(12, "SELECT 2")
	queued := r.now
	r.ack(r.ev(proxy.EvDBData, r.db, complete("SELECT 1", 'I'), 0))
	r.at(queued + proxy.Instant(r.e.Config().Timeouts.QueueWait))
	a := r.ev(proxy.EvDBData, r.db, complete("DISCARD ALL", 'I'), 0)
	if did(a, proxy.ActDBWrite, r.db) || !bytes.Contains(written(a, proxy.ActClientWrite, 12), []byte("53M01")) {
		t.Fatalf("expired request served: db %q client %q", written(a, proxy.ActDBWrite, r.db), written(a, proxy.ActClientWrite, 12))
	}
}
