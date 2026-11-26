package proxy_test

import (
	"bytes"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// TestBytesAfterReadyForQueryEndingALeaseCloseTheConnection proves trailing bytes in the same read after ReadyForQuery 'I' close the connection dirty without resetting.
func TestBytesAfterReadyForQueryEndingALeaseCloseTheConnection(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	r.client(12)
	r.ack(r.query(11, "SELECT 1"))
	r.query(12, "SELECT 2")
	b := append(complete("SELECT 1", 'I'), complete("DISCARD ALL", 'I')...)
	a := r.ev(proxy.EvDBData, r.db, b, 0)
	if !did(a, proxy.ActDBClose, r.db) || did(a, proxy.ActDBWrite, r.db) {
		t.Fatalf("a lease ended with unread bytes was not closed: %v", a)
	}
	s := r.e.Snapshot()
	if s.ResetsStarted != 0 || s.ResetsCompleted != 0 || s.DBCloses[proxy.CloseDBReleaseDirty] != 1 || s.LeasesEndedClose != 1 {
		t.Fatalf("resets %d/%d dirty closes %d", s.ResetsStarted, s.ResetsCompleted, s.DBCloses[proxy.CloseDBReleaseDirty])
	}
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Equal(got, complete("SELECT 1", 'I')) {
		t.Fatalf("client reply %q", got)
	}
	if owner, ok := r.e.LentTo(r.db); ok {
		t.Fatalf("closed connection lent to %d", owner)
	}
}

// TestBytesAfterTheResetReplyCloseTheConnection proves trailing bytes in the same read after DISCARD ALL's ReadyForQuery fail the reset and close the connection.
func TestBytesAfterTheResetReplyCloseTheConnection(t *testing.T) {
	r := newRig(t, nil)
	r.leaseThenReset(11, 12)
	a := r.ev(proxy.EvDBData, r.db, append(complete("DISCARD ALL", 'I'), complete("SELECT 1", 'I')...), 0)
	if !did(a, proxy.ActDBClose, r.db) || did(a, proxy.ActDBWrite, r.db) {
		t.Fatalf("a reset followed by unread bytes lent the connection: %q", written(a, proxy.ActDBWrite, r.db))
	}
	s := r.e.Snapshot()
	if s.ResetsCompleted != 0 || s.ResetsFailed[proxy.ResetFailSurprise] != 1 {
		t.Fatalf("completed %d failed %v", s.ResetsCompleted, s.ResetsFailed)
	}
}
