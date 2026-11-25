package fakepg_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	simworld "github.com/nickstrad/mini-gateway/internal/sim/world"
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

// Proves transactional INSERT respects MaxRows (accounting for pending deletes) in explicit and implicit transactions.
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

// Proves COMMIT rolls back with ERROR 53100 without logging phantom writes when concurrent inserts filled the table.
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

// Proves exceeding MaxStatements inside an explicit transaction puts it in the failed ('E') state.
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

// Proves injecting FaultStallWindow extends statements already in service by the stall extra.
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

// Proves FaultStallWindow adds its extra delay to a statement currently paused by FaultFreeze.
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

// Proves a zero-service-time statement starting exactly at a stall window's From instant takes the stall extra.
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

// Proves FaultMaxConn is clamped at Config.MaxConnections and cannot raise the hard session ceiling.
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

// Proves a targeted FaultFragment does not clear an active server-wide FaultFragment for new connections.
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

// Proves a targeted FaultGarbage keeps its mode per backend without overwriting the server-wide garbage mode.
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

// Proves ready-only faults (FaultDirtyState, FaultParamStatus) targeting a starting backend count as NoTarget.
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

// Proves FaultBloat on a two-column read emits two-column DataRows matching its RowDescription.
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

// Proves FaultFreeze pauses a pending delayed reset timer until the server thaws.
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

// Proves Match rejects repeat counts whose byte-length product would overflow int64.
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

// Proves FaultTerminate under back pressure finishes already-framed bytes before emitting FATAL 57P01 and EOF.
func TestTerminateDuringABackPressuredReplyKeepsTheFraming(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.client()
	from := len(p.Events())
	p.StopReading(true)
	p.Query("SELECT repeat('x', 1000) FROM generate_series(1, 20000)")
	w.run(10 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultTerminate, Target: id(p)})
	w.run(time.Millisecond)
	p.StopReading(false)
	w.run(time.Second)
	got := p.TraceFrom(from)
	if strings.Contains(got, "protocol-error") || !strings.HasSuffix(got, "E:57P01 eof") {
		tail := got
		if len(tail) > 80 {
			tail = tail[len(tail)-80:]
		}
		t.Fatalf("after a terminate under back pressure: ...%s", tail)
	}
	w.done(p)
}

func requireExactWriteLog(t *testing.T, w *world) {
	t.Helper()
	if err := simworld.CheckWrites(w.srv.Config().Rows, w.srv.Rows(), w.srv.StatementLog(), w.srv.TransactionLog(), w.srv.WriteLog()); err != nil {
		t.Fatalf("%v", err)
	}
}

func requireRows(t *testing.T, w *world, want string) {
	t.Helper()
	if got := fmt.Sprintf("%v", w.srv.Rows()); got != want {
		t.Fatalf("rows %s, want %s", got, want)
	}
}

// Proves COMMIT rolls back with 53100 when sequential apply would exceed MaxRows midway before a later DELETE.
func TestCommitThatPassesMaxRowsMidwayIsRolledBack(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: []fakepg.Row{{Key: "k1", Value: "v1"}}})
	a, b := w.client(), w.client()
	w.exchange(a, "BEGIN", "INSERT INTO kv VALUES ('k2', 'v2')", "DELETE FROM kv WHERE k = 'k1'")
	w.exchange(b, "INSERT INTO kv VALUES ('k3', 'v3')")
	if got := w.exchange(a, "COMMIT"); got != "E:53100 ZI" {
		t.Fatalf("commit: %s, want E:53100 ZI", got)
	}
	requireRows(t, w, "[{k1 v1} {k3 v3}]")
	requireExactWriteLog(t, w)
	if s := w.srv.Snapshot(); s.TxCommitted != 0 || s.TxRolledBack != 1 || s.WritesApplied != 1 {
		t.Fatalf("%+v", s)
	}
	w.done(a, b)
}

// Proves a re-creating UPDATE followed by a DELETE rolls back at COMMIT if its intermediate peak exceeds MaxRows.
func TestCommitWhoseRecreatedRowPassesMaxRowsIsRolledBack(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: []fakepg.Row{{Key: "k1", Value: "v1"}, {Key: "k2", Value: "v2"}}})
	a, b := w.client(), w.client()
	w.exchange(a, "BEGIN", "UPDATE kv SET v = 'v1b' WHERE k = 'k1'", "DELETE FROM kv WHERE k = 'k2'")
	w.exchange(b, "DELETE FROM kv WHERE k = 'k1'", "INSERT INTO kv VALUES ('k3', 'v3')")
	if got := w.exchange(a, "COMMIT"); got != "E:53100 ZI" {
		t.Fatalf("commit: %s, want E:53100 ZI", got)
	}
	requireRows(t, w, "[{k2 v2} {k3 v3}]")
	requireExactWriteLog(t, w)
	w.done(a, b)
}

// Proves INSERT then DELETE of the same key rolls back at COMMIT when a concurrent filler leaves no room for the peak.
func TestCommitInsertThenDeleteOfOneKeyAtAFullTableIsRolledBack(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: []fakepg.Row{{Key: "k1", Value: "v1"}}})
	a, b := w.client(), w.client()
	w.exchange(a, "BEGIN", "INSERT INTO kv VALUES ('k2', 'v2')", "DELETE FROM kv WHERE k = 'k2'")
	w.exchange(b, "INSERT INTO kv VALUES ('k3', 'v3')")
	if got := w.exchange(a, "COMMIT"); got != "E:53100 ZI" {
		t.Fatalf("commit: %s, want E:53100 ZI", got)
	}
	requireRows(t, w, "[{k1 v1} {k3 v3}]")
	requireExactWriteLog(t, w)
	w.done(a, b)
}

// Proves an implicit multi-statement transaction whose peak exceeds MaxRows midway rolls back with E:53100 before ReadyForQuery.
func TestImplicitCommitThatPassesMaxRowsMidwayIsRolledBack(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: []fakepg.Row{{Key: "k1", Value: "v1"}}, Service: fakepg.Service{Fixed: 50 * time.Millisecond}})
	a, b := w.client(), w.client()
	from := len(a.Events())
	a.Query("INSERT INTO kv VALUES ('k2', 'v2'); DELETE FROM kv WHERE k = 'k1'")
	w.run(10 * time.Millisecond)
	b.Query("INSERT INTO kv VALUES ('k3', 'v3')")
	w.run(time.Second)
	if got := a.TraceFrom(from); got != "C:INSERT 0 1 C:DELETE 1 E:53100 ZI" {
		t.Fatalf("implicit transaction: %s", got)
	}
	requireRows(t, w, "[{k1 v1} {k3 v3}]")
	requireExactWriteLog(t, w)
	w.done(a, b)
}

// Proves a delete-first transaction at a full table stays within MaxRows at every step and still commits.
func TestCommitThatDeletesFirstStillCommitsAtAFullTable(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: []fakepg.Row{{Key: "k1", Value: "v1"}}})
	a, b := w.client(), w.client()
	w.exchange(a, "BEGIN", "DELETE FROM kv WHERE k = 'k1'", "INSERT INTO kv VALUES ('k2', 'v2')")
	w.exchange(b, "INSERT INTO kv VALUES ('k3', 'v3')")
	if got := w.exchange(a, "COMMIT"); got != "C:COMMIT ZI" {
		t.Fatalf("commit: %s, want C:COMMIT ZI", got)
	}
	requireRows(t, w, "[{k2 v2} {k3 v3}]")
	requireExactWriteLog(t, w)
	w.done(a, b)
}

// Proves a net-zero INSERT-then-DELETE transaction commits with both writes logged when room exists for the peak.
func TestInsertThenDeleteOfOneKeyCommits(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxRows: 2, Rows: []fakepg.Row{{Key: "k1", Value: "v1"}}})
	a := w.client()
	if got := w.exchange(a, "BEGIN", "INSERT INTO kv VALUES ('k2', 'v2')", "DELETE FROM kv WHERE k = 'k2'", "COMMIT"); got != "C:BEGIN ZT C:INSERT 0 1 ZT C:DELETE 1 ZT C:COMMIT ZI" {
		t.Fatalf("exchange: %s", got)
	}
	requireRows(t, w, "[{k1 v1}]")
	if n := len(w.srv.WriteLog()); n != 2 {
		t.Fatalf("write log %d records, want 2: %v", n, w.srv.WriteLog())
	}
	requireExactWriteLog(t, w)
	w.done(a)
}
