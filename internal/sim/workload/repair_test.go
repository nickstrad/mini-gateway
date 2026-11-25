package workload_test

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

// Proves Retry.OnCodes retries non-FATAL query ERROR responses up to Retry.Max times.
func TestRetryOnCodesRetriesAQueryError(t *testing.T) {
	tenant := one("SELECT 1/0", 1)
	tenant.Retry = workload.Retry{Max: 3, Backoff: 5 * time.Millisecond, OnCodes: []string{"22012"}}
	w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.finish(t)
	db, c := w.srv.Snapshot(), w.wl.Snapshot().Total
	if c.Retries != 3 || db.StatementsByKind[fakepg.KindDivZero] != 4 || c.Errors != 1 || c.Code("22012") != 1 || c.Sent != 1 {
		t.Fatalf("server statements %d, client %+v", db.StatementsByKind[fakepg.KindDivZero], c)
	}
}

// Proves Retry.OnCodes does not retry SQLSTATE error codes absent from the list.
func TestRetryOnCodesIgnoresOtherCodes(t *testing.T) {
	tenant := one("SELECT 1/0", 1)
	tenant.Retry = workload.Retry{Max: 3, Backoff: 5 * time.Millisecond, OnCodes: []string{"57014"}}
	w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.finish(t)
	if c := w.wl.Snapshot().Total; c.Retries != 0 || c.Errors != 1 {
		t.Fatalf("%+v", c)
	}
}

// Proves a cancelled transaction that rolls back and retries re-runs its original statements instead of ROLLBACK.
func TestRetriedTransactionRunsItsOriginalStatements(t *testing.T) {
	tenant := workload.Tenant{Name: "a", Clients: 1, Requests: 1, ConnectFirst: true, Start: 10 * time.Millisecond,
		Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k0"}, Mix: []workload.Weight{{Op: workload.OpTx, Weight: 1}},
		TxStatements: 2, Retry: workload.Retry{Max: 1, Backoff: 5 * time.Millisecond, OnCodes: []string{"57014"}}}
	pg := fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}, Service: fakepg.Service{Fixed: 50 * time.Millisecond}}
	w := build(t, 1, pg, workload.Config{Tenants: []workload.Tenant{tenant}})
	fault(w.s, 80*time.Millisecond, workload.FaultCancel, 1, 0, 0)
	w.finish(t)
	db, c := w.srv.Snapshot(), w.wl.Snapshot().Total
	if db.CancelsApplied != 1 || c.Retries != 1 || c.OK != 1 || db.TxCommitted != 1 || db.TxRolledBack != 1 {
		t.Fatalf("server %+v\nclient %+v", db, c)
	}
	var kinds []fakepg.Kind
	for _, s := range w.srv.StatementLog() {
		kinds = append(kinds, s.Kind)
	}
	want := []fakepg.Kind{fakepg.KindBegin, fakepg.KindUpdate, fakepg.KindRollback, fakepg.KindBegin, fakepg.KindUpdate, fakepg.KindUpdate, fakepg.KindCommit}
	if sprint(kinds) != sprint(want) {
		t.Fatalf("statements %v, want %v", kinds, want)
	}
	if n := len(w.srv.WriteLog()); n != 2 {
		t.Fatalf("%d writes applied, want the retried transaction's 2", n)
	}
}

// Proves clearing a timed FaultStopReading cancels its resume timer so it cannot prematurely resume a later indefinite stop.
func TestStopReadingTimerOfAClearedFaultDoesNotResumeALaterOne(t *testing.T) {
	tenant := one("SELECT 1", 1)
	tenant.Start = 50 * time.Millisecond
	w := build(t, 1, fakepg.Config{}, workload.Config{Tenants: []workload.Tenant{tenant}})
	fault(w.s, time.Millisecond, workload.FaultStopReading, 1, 0, int64(100*time.Millisecond))
	fault(w.s, 10*time.Millisecond, workload.FaultClear, 1, int64(workload.FaultStopReading), 0)
	fault(w.s, 20*time.Millisecond, workload.FaultStopReading, 1, 0, 0)
	if err := w.s.RunFor(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if c := w.wl.Snapshot().Total; c.OK != 0 {
		t.Fatalf("the client read its reply while stopped until cleared: %+v", c)
	}
	fault(w.s, 400*time.Millisecond, workload.FaultClear, 1, int64(workload.FaultStopReading), 0)
	w.finish(t)
	if c := w.wl.Snapshot().Total; c.OK != 1 {
		t.Fatalf("%+v", c)
	}
}

// Proves requests waiting in retry backoff count against Tenant.Backlog so NotYetSent never exceeds the cap.
func TestBacklogCapCountsRetryingRequests(t *testing.T) {
	tenant := workload.Tenant{Name: "a", Clients: 1, Requests: 50, Interval: time.Millisecond, Backlog: 2,
		Keys:  workload.Keys{N: 1},
		Retry: workload.Retry{Max: 3, Backoff: 200 * time.Millisecond, OnConnect: true}}
	w := build(t, 1, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}}, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultMaxConn, Arg1: 0})
	if err := w.s.RunFor(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	c := w.wl.Snapshot().Total
	if c.Retries == 0 || c.Dropped == 0 || c.NotYetSent.Peak > 2 || c.NotYetSent.Value > 2 {
		t.Fatalf("with every connect refused: retries %d, dropped %d, not yet sent %v, intended %d", c.Retries, c.Dropped, c.NotYetSent, c.Intended)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultMaxConn)})
	w.finish(t)
	c = w.wl.Snapshot().Total
	if c.Intended != 50 || c.OK == 0 || c.Dropped == 0 || c.NotYetSent.Peak > 2 {
		t.Fatalf("after the clear: %+v", c)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
}
