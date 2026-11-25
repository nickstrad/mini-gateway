package workload_test

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

func one(statement string, requests int) workload.Tenant {
	return workload.Tenant{Name: "a", Clients: 1, Requests: requests, Interval: 50 * time.Millisecond, Statement: statement,
		Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k0"}, ConnectFirst: true}
}

func fault(s *sim.Sim, at time.Duration, kind sim.FaultKind, target, a1, a2 int64) {
	s.InjectAt(sim.Fault{At: sim.Time(at), Kind: kind, Target: target, Arg1: a1, Arg2: a2})
}

func TestClientFaultHalfMessage(t *testing.T) {
	w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{one("SELECT 1", 2)}})
	fault(w.s, 10*time.Millisecond, workload.FaultHalfMessage, 1, 0, 0)
	if err := w.s.RunFor(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	mid := w.srv.Snapshot()
	if mid.StatementsStarted != 1 || mid.StatementsFinished != 1 || mid.ConnsOpen.Value != 1 || mid.BytesIn == 0 {
		t.Fatalf("while the client holds half a message: %+v", mid)
	}
	w.finish(t)
	s, c := w.srv.Snapshot(), w.wl.Snapshot().Total
	if s.StatementsStarted != 1 || s.ConnsClosed != 1 || c.HalfMessages != 1 || c.ClientLeft != 1 || c.OK != 1 {
		t.Fatalf("server %+v\nclient %+v", s, c)
	}
	if w.wl.Snapshot().Faults.ByKind[0] != 1 {
		t.Fatal("fault not counted")
	}
}

func TestClientFaultStopsReading(t *testing.T) {
	tenant := one("SELECT repeat('x', 1000) FROM generate_series(1, 1000)", 1)
	w := build(t, 1, fakepg.Config{Service: fakepg.Service{Fixed: 20 * time.Millisecond}}, workload.Config{Tenants: []workload.Tenant{tenant}})
	fault(w.s, 5*time.Millisecond, workload.FaultStopReading, 1, 0, int64(300*time.Millisecond))
	if err := w.s.RunFor(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	mid, net := w.srv.Snapshot(), w.n.Snapshot()
	if mid.BytesOut > 2*int64(64<<10) || net.Down.WritesRefused == 0 || mid.StatementsFinished != 1 {
		t.Fatalf("the server did not stop at the window: bytes out %d, refused writes %d", mid.BytesOut, net.Down.WritesRefused)
	}
	w.finish(t)
	if s, c := w.srv.Snapshot(), w.wl.Snapshot().Total; s.BytesOut < 1_000_000 || c.OK != 1 || c.StoppedReading != 1 {
		t.Fatalf("after reading resumed: server %d bytes, client %+v", s.BytesOut, c)
	}
}

func TestClientFaultDisconnectMidStatement(t *testing.T) {
	for _, reset := range []bool{false, true} {
		w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{one("SELECT pg_sleep(0.1)", 1)}})
		fault(w.s, 50*time.Millisecond, workload.FaultDisconnect, 1, int64(workload.DisconnectNow), boolArg(reset))
		w.finish(t)
		s, c := w.srv.Snapshot(), w.wl.Snapshot().Total
		if c.ClientLeft != 1 || c.Disconnects != 1 || s.StatementsStarted != 1 {
			t.Fatalf("reset=%v: server %+v client %+v", reset, s, c)
		}
		if reset && (s.StatementsAborted != 1 || s.StatementsFinished != 0) {
			t.Fatalf("a reset must abort the statement in service: %+v", s)
		}
		if !reset && (s.StatementsFinished != 1 || s.StatementsAborted != 0) {
			t.Fatalf("a closed client is noticed only after the statement: %+v", s)
		}
	}
}

func TestClientFaultDisconnectRightAfterSending(t *testing.T) {
	for _, reset := range []bool{false, true} {
		w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{one("SELECT pg_sleep(0.1)", 1)}})
		fault(w.s, 0, workload.FaultDisconnect, 1, int64(workload.DisconnectAfterSend), boolArg(reset))
		w.finish(t)
		s, c := w.srv.Snapshot(), w.wl.Snapshot().Total
		want := int64(1)
		if reset {
			want = 0
		}
		if c.ClientLeft != 1 || s.StatementsStarted != want {
			t.Fatalf("reset=%v: statements the server saw %d, want %d; client %+v", reset, s.StatementsStarted, want, c)
		}
	}
}

func boolArg(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func TestClientFaultDisconnectWhileQueuedInTheDatabase(t *testing.T) {
	tenant := workload.Tenant{Name: "a", Clients: 2, Requests: 1, Statement: "SELECT pg_sleep(0.1)", ConnectFirst: true, Start: 5 * time.Millisecond,
		Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k0"}}
	w := build(t, 1, fakepg.Config{Capacity: 1}, workload.Config{Tenants: []workload.Tenant{tenant}})
	fault(w.s, 20*time.Millisecond, workload.FaultDisconnect, 2, int64(workload.DisconnectNow), 1)
	if err := w.s.RunFor(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if s := w.srv.Snapshot(); s.Waiting.Value != 1 || s.InService.Value != 1 {
		t.Fatalf("not queued: %+v", s)
	}
	w.finish(t)
	s, c := w.srv.Snapshot(), w.wl.Snapshot().Total
	if s.StatementsAborted != 1 || s.StatementsFinished != 1 || c.ClientLeft != 1 || c.OK != 1 {
		t.Fatalf("server %+v client %+v", s, c)
	}
}

func TestClientFaultDisconnectMidTransaction(t *testing.T) {
	tenant := workload.Tenant{Name: "a", Clients: 1, Requests: 2, Interval: 50 * time.Millisecond, Mix: []workload.Weight{{Op: workload.OpTx, Weight: 1}},
		TxStatements: 2, Keys: workload.Keys{N: 1}, Disconnect: []workload.Disconnect{{Request: 0, Point: workload.DisconnectInTx}}}
	w := build(t, 1, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.finish(t)
	s, c := w.srv.Snapshot(), w.wl.Snapshot().Total
	if s.TxLost != 1 || s.TxCommitted != 1 || s.WritesApplied != 2 || c.ClientLeft != 1 || c.OK != 1 || c.Reconnects != 1 {
		t.Fatalf("server %+v client %+v", s, c)
	}
}

func TestClientRetriesAggressively(t *testing.T) {
	tenant := workload.Tenant{Name: "a", Clients: 5, Requests: 1, Keys: workload.Keys{N: 1},
		Retry: workload.Retry{Max: 3, Backoff: 10 * time.Millisecond, OnConnect: true}}
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	w := buildOn(t, s, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultMaxConn, Arg1: 0})
	w.finish(t)
	db, c := w.srv.Snapshot(), w.wl.Snapshot().Total
	if db.Refused.TooMany != 5*4 || c.Retries != 5*3 || c.FailedBeforeSend != 5 || c.Code("53300") != 5 {
		t.Fatalf("server refused %d, client %+v", db.Refused.TooMany, c)
	}
	w2 := build(t, 1, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w2.s.ApplyFault(sim.Fault{Kind: fakepg.FaultMaxConn, Arg1: 0})
	w2.s.InjectAt(sim.Fault{At: sim.Time(15 * time.Millisecond), Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultMaxConn)})
	w2.finish(t)
	if c := w2.wl.Snapshot().Total; c.OK != 5 || c.Retries == 0 {
		t.Fatalf("after the clear the retries must succeed: %+v", c)
	}
}

func TestClientSendsACancel(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{one("SELECT pg_sleep(0.2)", 1)}})
		fault(w.s, 50*time.Millisecond, workload.FaultCancel, 1, boolArg(wrong), 0)
		w.finish(t)
		s, c := w.srv.Snapshot(), w.wl.Snapshot().Total
		if wrong {
			if s.CancelsNoMatch != 1 || c.OK != 1 {
				t.Fatalf("wrong key: server %+v client %+v", s, c)
			}
			continue
		}
		if s.CancelsApplied != 1 || c.Code("57014") != 1 || c.Errors != 1 || c.CancelsSent != 1 {
			t.Fatalf("server %+v client %+v", s, c)
		}
	}
}

func TestClientTimesOutOnItsOwnDeadline(t *testing.T) {
	tenant := one("SELECT pg_sleep(1)", 1)
	tenant.Deadline = 100 * time.Millisecond
	w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.finish(t)
	if c := w.wl.Snapshot().Total; c.TimedOut != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestBacklogCapCountsCouldNotSend(t *testing.T) {
	tenant := workload.Tenant{Name: "a", Clients: 1, Requests: 20, Interval: time.Millisecond, Backlog: 5, Keys: workload.Keys{N: 1}}
	w := build(t, 1, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}, Service: fakepg.Service{Fixed: 100 * time.Millisecond}},
		workload.Config{Tenants: []workload.Tenant{tenant}})
	w.finish(t)
	c := w.wl.Snapshot().Total
	if c.Dropped == 0 || c.Intended != 20 || c.Dropped+c.Sent != 20 || c.NotYetSent.Peak != 5 {
		t.Fatalf("%+v", c)
	}
}
