package proxy_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const sharedRead = "SELECT v FROM kv WHERE k = 'a'"

func coalRig(t *testing.T, join coalesce.JoinMode) *rig {
	return newRig(t, func(c *proxy.Config) {
		c.Coalesce = coalesce.Config{Mode: coalesce.ModeAuto, Join: join}
	})
}

func readReply(v string) []byte {
	b := message(pgwire.MsgRowDescription, []byte{0, 0})
	row := append([]byte{0, 1, 0, 0, 0, byte(len(v))}, v...)
	b = append(b, message(pgwire.MsgDataRow, row)...)
	return append(b, complete("SELECT 1", 'I')...)
}

func TestFollowerGetsTheSameBytesAsTheRequestThatRanTheRead(t *testing.T) {
	r := coalRig(t, coalesce.JoinRelaxed)
	r.client(11)
	r.client(12)
	a := r.ack(r.query(11, sharedRead))
	if !did(a, proxy.ActDBWrite, r.db) {
		t.Fatal("the first request was not forwarded")
	}
	if a := r.query(12, sharedRead); did(a, proxy.ActDBWrite, r.db) {
		t.Fatal("the second identical read reached the database")
	}
	if s := r.e.Snapshot(); s.Coalesced.Value != 1 || s.Queued.Value != 0 || s.InFlight.Value != 1 {
		t.Fatalf("follower counted as %d coalesced, %d queued, %d in flight", s.Coalesced.Value, s.Queued.Value, s.InFlight.Value)
	}
	a = r.ack(r.ev(proxy.EvDBData, r.db, readReply("x"), 0))
	one, two := written(a, proxy.ActClientWrite, 11), written(a, proxy.ActClientWrite, 12)
	if !bytes.Equal(one, readReply("x")) || !bytes.Equal(one, two) {
		t.Fatalf("replies differ:\n %q\n %q", one, two)
	}
	s := r.e.Snapshot()
	if s.Completed != 2 || s.CoalesceReplies != 1 || s.Coalesced.Value != 0 {
		t.Fatalf("completed %d shared %d coalesced %d", s.Completed, s.CoalesceReplies, s.Coalesced.Value)
	}
}

func TestStrictHoldsARequestThatArrivesWhileTheReadRuns(t *testing.T) {
	r := coalRig(t, coalesce.JoinStrict)
	r.client(11)
	r.client(12)
	r.ack(r.query(11, sharedRead))
	r.query(12, sharedRead)
	if s := r.e.CoalesceSnapshot(); s.Held != 1 || s.Hits != 0 {
		t.Fatalf("held %d hits %d", s.Held, s.Hits)
	}
	a := r.ack(r.ev(proxy.EvDBData, r.db, readReply("old"), 0))
	if len(written(a, proxy.ActClientWrite, 12)) != 0 {
		t.Fatal("strict: the request that arrived while the read ran got its result")
	}
	if !bytes.Equal(written(a, proxy.ActDBWrite, r.db), pgwire.AppendQuery(nil, []byte("DISCARD ALL"))) {
		t.Fatal("no reset after the first lease")
	}
	a = r.ack(r.ev(proxy.EvDBData, r.db, complete("DISCARD ALL", 'I'), 0))
	if !bytes.Equal(written(a, proxy.ActDBWrite, r.db), pgwire.AppendQuery(nil, []byte(sharedRead))) {
		t.Fatalf("the held flight did not start when the running read ended: %q", written(a, proxy.ActDBWrite, r.db))
	}
	a = r.ack(r.ev(proxy.EvDBData, r.db, readReply("new"), 0))
	if !bytes.Equal(written(a, proxy.ActClientWrite, 12), readReply("new")) {
		t.Fatal("the held request did not get the next read's result")
	}
}

func TestALeaverAtTheInstantOfTheResultIsNotAnswered(t *testing.T) {
	r := coalRig(t, coalesce.JoinRelaxed)
	r.client(11)
	r.client(12)
	r.client(13)
	r.ack(r.query(11, sharedRead))
	r.query(12, sharedRead)
	r.query(13, sharedRead)
	r.at(50)
	r.ev(proxy.EvClientClosed, 12, nil, 0)
	a := r.ack(r.ev(proxy.EvDBData, r.db, readReply("x"), 0))
	if did(a, proxy.ActClientWrite, 12) {
		t.Fatal("a client that left was written to")
	}
	if !bytes.Equal(written(a, proxy.ActClientWrite, 13), readReply("x")) {
		t.Fatal("the waiter that stayed was not answered")
	}
	if s := r.e.Snapshot(); s.FailedNoReply != 1 || s.Completed != 2 {
		t.Fatalf("failed without reply %d completed %d", s.FailedNoReply, s.Completed)
	}
}

func TestTheOwnerLeavingHandsTheLeaseToAWaiter(t *testing.T) {
	r := coalRig(t, coalesce.JoinRelaxed)
	r.client(11)
	r.client(12)
	r.ack(r.query(11, sharedRead))
	r.query(12, sharedRead)
	a := r.ev(proxy.EvClientClosed, 11, nil, 0)
	if did(a, proxy.ActDBClose, r.db) {
		t.Fatal("the shared read was abandoned while a waiter remained")
	}
	if lent, ok := r.e.LentTo(r.db); !ok || lent != 12 {
		t.Fatalf("lent to %d, want the waiter", lent)
	}
	a = r.ack(r.ev(proxy.EvDBData, r.db, readReply("x"), 0))
	if !bytes.Equal(written(a, proxy.ActClientWrite, 12), readReply("x")) {
		t.Fatal("the waiter that took over was not answered")
	}
	if s := r.e.Snapshot(); s.CoalesceHandoffs != 1 {
		t.Fatalf("handoffs %d", s.CoalesceHandoffs)
	}
}

func TestInvariantsCoverFlightsAndWaiters(t *testing.T) {
	for _, c := range []struct {
		name    string
		corrupt func(*proxy.Engine) bool
		want    string
	}{
		{"follower entry", (*proxy.Engine).DropFollowerEntryForTest, "follower entry"},
		{"owner request", (*proxy.Engine).MisplaceOwnerForTest, "without carrying its request"},
		{"timer", (*proxy.Engine).DisarmCoalesceTimerForTest, "not armed"},
	} {
		r := coalRig(t, coalesce.JoinRelaxed)
		r.client(11)
		r.client(12)
		r.ack(r.query(11, sharedRead))
		r.query(12, sharedRead)
		if !c.corrupt(r.e) {
			t.Fatalf("%s: nothing to corrupt", c.name)
		}
		err := r.e.Invariants()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: Invariants() = %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

func TestEngineQuiescesWithAnEmptyFlightTableAndNoTimer(t *testing.T) {
	r := coalRig(t, coalesce.JoinRelaxed)
	r.client(11)
	r.client(12)
	r.ack(r.query(11, sharedRead))
	r.query(12, sharedRead)
	if err := r.e.Quiesce(); err == nil || !strings.Contains(err.Error(), "flights") {
		t.Fatalf("quiesce with a flight: %v", err)
	}
	r.ack(r.ev(proxy.EvDBData, r.db, readReply("x"), 0))
	r.ack(r.ev(proxy.EvDBData, r.db, complete("DISCARD ALL", 'I'), 0))
	r.ev(proxy.EvClientClosed, 11, nil, 0)
	r.ev(proxy.EvClientClosed, 12, nil, 0)
	if err := r.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
	r.e.Shutdown(r.now, nil)
	if err := r.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
}
