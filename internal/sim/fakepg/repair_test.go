package fakepg_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

func tableMatchesWriteLog(t *testing.T, w *world, initial []fakepg.Row) {
	t.Helper()
	table := map[string]string{}
	for _, r := range initial {
		table[r.Key] = r.Value
	}
	for _, wr := range w.srv.WriteLog() {
		if wr.Op == fakepg.KindDelete {
			delete(table, wr.Key)
			continue
		}
		table[wr.Key] = wr.Value
	}
	rows := w.srv.Rows()
	if len(rows) != len(table) {
		t.Fatalf("replaying the write log gives %d rows, the table has %d: %v", len(table), len(rows), rows)
	}
	for _, r := range rows {
		if v, ok := table[r.Key]; !ok || v != r.Value {
			t.Fatalf("row %s=%s, the write log says %q (present %v)", r.Key, r.Value, v, ok)
		}
	}
	if int64(len(w.srv.WriteLog())) != w.srv.Snapshot().WritesApplied {
		t.Fatalf("write log %d records, counter %d", len(w.srv.WriteLog()), w.srv.Snapshot().WritesApplied)
	}
}

func TestInsertInsideATransactionRespectsMaxRows(t *testing.T) {
	initial := []fakepg.Row{{Key: "k1", Value: "v1"}, {Key: "k2", Value: "v2"}}
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: initial})
	p := w.client()
	got := w.exchange(p, "BEGIN", "INSERT INTO kv VALUES ('k3', 'v3')", "COMMIT")
	if got != "C:BEGIN ZT E:53100 ZE C:ROLLBACK ZI" {
		t.Fatalf("explicit transaction: %s", got)
	}
	got = w.exchange(p, "INSERT INTO kv VALUES ('k3', 'v3'); SELECT 1")
	if got != "E:53100 ZI" {
		t.Fatalf("implicit transaction: %s", got)
	}
	got = w.exchange(p, "BEGIN", "DELETE FROM kv WHERE k = 'k1'", "INSERT INTO kv VALUES ('k3', 'v3')", "COMMIT")
	if got != "C:BEGIN ZT C:DELETE 1 ZT C:INSERT 0 1 ZT C:COMMIT ZI" {
		t.Fatalf("delete then insert: %s", got)
	}
	if _, ok := w.srv.Get("k3"); !ok {
		t.Fatal("k3 not applied")
	}
	tableMatchesWriteLog(t, w, initial)
	w.done(p)
}

func TestCommitThatNoLongerFitsIsRolledBack(t *testing.T) {
	initial := []fakepg.Row{{Key: "a", Value: "1"}}
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: initial})
	a, b := w.client(), w.client()
	w.exchange(a, "BEGIN", "INSERT INTO kv VALUES ('x', '1')")
	w.exchange(b, "BEGIN", "INSERT INTO kv VALUES ('y', '1')")
	if got := w.exchange(a, "COMMIT"); got != "C:COMMIT ZI" {
		t.Fatalf("first commit: %s", got)
	}
	if got := w.exchange(b, "COMMIT"); got != "E:53100 ZI" {
		t.Fatalf("second commit: %s", got)
	}
	if _, ok := w.srv.Get("y"); ok {
		t.Fatal("a commit refused for a full table applied its write")
	}
	if got := w.exchange(b, "SELECT 1"); got != "T D C:SELECT 1 ZI" {
		t.Fatalf("after the refused commit: %s", got)
	}
	tableMatchesWriteLog(t, w, initial)
	w.done(a, b)
	if s := w.srv.Snapshot(); s.TxCommitted != 1 || s.TxRolledBack != 1 || s.TableRows != 2 {
		t.Fatalf("%+v", s)
	}
}

func TestStatementLimitErrorFailsTheTransaction(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.client()
	many := strings.Repeat("SELECT 1;", fakepg.MaxStatements+1)
	if got := w.exchange(p, "BEGIN", many); got != "C:BEGIN ZT E:54000 ZE" {
		t.Fatalf("got %s", got)
	}
	if got := w.exchange(p, "SELECT 1"); got != "E:25P02 ZE" {
		t.Fatalf("the transaction is not failed: %s", got)
	}
	if got := w.exchange(p, "ROLLBACK"); got != "C:ROLLBACK ZI" {
		t.Fatalf("rollback: %s", got)
	}
	if got := w.exchange(p, many); got != "E:54000 ZI" {
		t.Fatalf("outside a transaction: %s", got)
	}
	w.done(p)
}

func TestStallWindowExtendsStatementsAlreadyInService(t *testing.T) {
	w := newWorld(t, fakepg.Config{Service: fakepg.Service{Fixed: 50 * time.Millisecond}})
	p := w.client()
	from, at := len(p.Events()), w.s.Now()
	p.Query("SELECT 1")
	w.run(10 * time.Millisecond)
	if w.srv.Snapshot().InService.Value != 1 {
		t.Fatal("the statement is not in service")
	}
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultStallWindow, Arg1: int64(200 * time.Millisecond), Arg2: int64(150 * time.Millisecond)})
	w.run(time.Second)
	if got := elapsed(p, from, at); got != 200*time.Millisecond+100*time.Microsecond {
		t.Fatalf("reply after %s, want 50ms + 150ms + the round trip", got)
	}
	w.done(p)
}

func TestStallWindowExtendsAFrozenStatement(t *testing.T) {
	w := newWorld(t, fakepg.Config{Service: fakepg.Service{Fixed: 50 * time.Millisecond}})
	p := w.client()
	from, at := len(p.Events()), w.s.Now()
	p.Query("SELECT 1")
	w.run(10 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultFreeze, Arg2: int64(100 * time.Millisecond)})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultStallWindow, Arg1: int64(200 * time.Millisecond), Arg2: int64(150 * time.Millisecond)})
	w.run(time.Second)
	if got := elapsed(p, from, at); got != 300*time.Millisecond+100*time.Microsecond {
		t.Fatalf("reply after %s, want 50ms + 100ms frozen + 150ms + the round trip", got)
	}
	w.done(p)
}

func TestStallWindowCatchesAZeroTimeStatementAtItsStart(t *testing.T) {
	start := sim.Time(100 * time.Millisecond)
	w := newWorld(t, fakepg.Config{Service: fakepg.Service{Stalls: []fakepg.Stall{{From: start, To: start + sim.Time(200*time.Millisecond), Extra: 150 * time.Millisecond}}}})
	p := w.client()
	w.run(time.Duration(start-w.s.Now()) - 50*time.Microsecond)
	from, at := len(p.Events()), w.s.Now()
	p.Query("SELECT 1")
	w.run(time.Second)
	got := elapsed(p, from, at)
	if got < 150*time.Millisecond {
		t.Fatalf("a statement starting exactly at the window start took %s", got)
	}
	w.done(p)
}

func TestMaxConnFaultCannotRaiseTheConfiguredLimit(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxConnections: 2})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultMaxConn, Arg1: 5})
	a, b := w.client(), w.client()
	c := w.dial()
	c.Startup("app", "")
	w.run(time.Millisecond)
	if got := c.Trace(); got != "connected E:53300 eof" {
		t.Fatalf("third connection: %s", got)
	}
	w.done(a, b, c)
}

func TestTargetedFragmentKeepsTheServerWideFragment(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	a := w.client()
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultFragment, Arg2: 1})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultFragment, Target: id(a), Arg2: 4})
	before := w.n.Snapshot().Down
	b := w.client()
	after := w.n.Snapshot().Down
	if after.Written-before.Written == 0 || after.Chunks-before.Chunks != after.Written-before.Written {
		t.Fatalf("a new connection after a targeted fragment fault: %d pieces for %d bytes", after.Chunks-before.Chunks, after.Written-before.Written)
	}
	w.done(a, b)
}

func TestTargetedGarbageKeepsTheServerWideMode(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	a, b, c := w.client(), w.client(), w.client()
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultGarbage, Arg1: fakepg.GarbageShortLength})
	out := func(p *workload.Probe) (string, int64) {
		before := w.srv.Snapshot().BytesOut
		got := w.exchange(p, "SELECT 1")
		return got, w.srv.Snapshot().BytesOut - before
	}
	control, controlBytes := out(c)
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultGarbage, Target: id(a), Arg1: fakepg.GarbageRandom})
	got, gotBytes := out(b)
	if control != "T protocol-error" || got != control || gotBytes != controlBytes {
		t.Fatalf("server-wide garbage before the targeted fault: %s (%d bytes), after it: %s (%d bytes)", control, controlBytes, got, gotBytes)
	}
	w.done(a, b, c)
}

func TestReadyOnlyFaultsOnAStartingBackendCountAsNoTarget(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.dial()
	w.run(time.Millisecond)
	for _, k := range []sim.FaultKind{fakepg.FaultDirtyState, fakepg.FaultParamStatus} {
		w.s.ApplyFault(sim.Fault{Kind: k, Target: id(p)})
		if s := w.srv.Snapshot(); s.Fault(k) != 0 {
			t.Fatalf("%s counted on a backend still in startup", fakepg.FaultName(k))
		}
	}
	if s := w.srv.Snapshot(); s.Faults.NoTarget != 2 {
		t.Fatalf("no target %d, want 2", s.Faults.NoTarget)
	}
	w.done(p)
}

func TestBloatOnATwoColumnReadKeepsTheColumnCount(t *testing.T) {
	w := newWorld(t, fakepg.Config{Rows: []fakepg.Row{{Key: "a", Value: "1"}}})
	p := w.client()
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultBloat, Arg1: 3, Arg2: 5})
	w.exchange(p, "SELECT v, pg_sleep(0) IS NOT NULL FROM kv WHERE k = 'a'")
	cols := []int{}
	for _, m := range p.Messages() {
		switch m.Type {
		case testclient.MsgRowDesc:
			cols = append(cols, -len(m.Fields))
		case testclient.MsgDataRow:
			cols = append(cols, len(m.Values))
		}
	}
	if len(cols) != 5 || cols[0] != -2 || cols[1] != 2 || cols[2] != 2 || cols[3] != 2 || cols[4] != 2 {
		t.Fatalf("column counts (negative: row description) %v", cols)
	}
	w.done(p)
}

func TestFreezeHoldsADelayedReset(t *testing.T) {
	w := newWorld(t, fakepg.Config{AfterDelay: 10 * time.Millisecond})
	p := w.client()
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultReset, Target: id(p), Arg1: int64(fakepg.PointAfter)})
	p.Query("SELECT 1")
	w.run(time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultFreeze})
	w.run(time.Second)
	if p.Closed() {
		t.Fatalf("the server reset a socket while frozen: %s", p.Trace())
	}
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultFreeze)})
	w.run(time.Second)
	if !p.Closed() || !strings.HasSuffix(p.Trace(), "reset") {
		t.Fatalf("after the thaw: %s", p.Trace())
	}
	w.done(p)
}

func TestMatchRowsRefusesOverflowingSizes(t *testing.T) {
	for _, q := range []string{
		"SELECT repeat('ab', 5000000000000000000) FROM generate_series(1, 1)",
		"SELECT repeat('abc', 3074457345618258603) FROM generate_series(1, 1)",
	} {
		st := fakepg.Match(q)
		if st.Kind == fakepg.KindRows {
			t.Fatalf("%s matched with size %d", q, st.Size)
		}
	}
}
