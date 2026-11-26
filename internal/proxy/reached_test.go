package proxy_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func lostWrite(t *testing.T, refused bool) proxy.Snapshot {
	r := newRig(t, nil)
	r.client(11)
	a := r.query(11, "INSERT INTO kv VALUES ('k', 'v')")
	if !did(a, proxy.ActDBWrite, r.db) {
		t.Fatal("statement not forwarded")
	}
	if refused {
		r.ev(proxy.EvDBWriteRefused, r.db, nil, 0)
	}
	a = r.ev(proxy.EvDBClosed, r.db, nil, 0)
	s := r.e.Snapshot()
	want := pgwire.ErrRowLostAfterWrite
	if refused {
		want = pgwire.ErrRowLostAfterRead
	}
	if s.ByRow[want] != 1 {
		t.Fatalf("rows %v reply %q", s.ByRow, written(a, proxy.ActClientWrite, 11))
	}
	return s
}

func TestDatabaseClosedBeforeTheWriteReportIsMayHaveReached(t *testing.T) {
	if s := lostWrite(t, false); s.ReachedNo != 0 || s.ReachedYes != 1 {
		t.Fatalf("reached no %d yes %d", s.ReachedNo, s.ReachedYes)
	}
}

func TestWriteRefusedWithZeroBytesIsNotReached(t *testing.T) {
	if s := lostWrite(t, true); s.ReachedNo != 1 || s.ReachedYes != 0 {
		t.Fatalf("reached no %d yes %d", s.ReachedNo, s.ReachedYes)
	}
}

func TestWriteRefusedAfterPartBytesIsMayHaveReached(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	r.query(11, "INSERT INTO kv VALUES ('k', 'v')")
	r.ev(proxy.EvDBWrote, r.db, nil, 1)
	r.ev(proxy.EvDBWriteRefused, r.db, nil, 0)
	r.ev(proxy.EvDBClosed, r.db, nil, 0)
	if s := r.e.Snapshot(); s.ReachedNo != 0 || s.ByRow[pgwire.ErrRowLostAfterWrite] != 1 {
		t.Fatalf("reached no %d rows %v", s.ReachedNo, s.ByRow)
	}
}

func TestClientVanishedBeforeTheWriteReportIsMayHaveReached(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	r.query(11, "INSERT INTO kv VALUES ('k', 'v')")
	r.ev(proxy.EvClientClosed, 11, nil, 0)
	if s := r.e.Snapshot(); s.ReachedNo != 0 || s.ReachedYes != 1 {
		t.Fatalf("reached no %d yes %d", s.ReachedNo, s.ReachedYes)
	}
}
