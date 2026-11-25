package fakepg_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

func lastRows(msgs []testclient.Msg) []string {
	var out []string
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow && len(m.Values) == 1 {
			out = append(out, string(m.Values[0]))
		}
	}
	return out
}

func TestReadInServiceDoesNotSeeLaterCommit(t *testing.T) {
	w := newWorld(t, fakepg.Config{
		Rows:    []fakepg.Row{{Key: "a", Value: "1"}},
		Service: fakepg.Service{Fixed: time.Millisecond, PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 100 * time.Millisecond}}},
	})
	reader, writer := w.client(), w.client()
	reader.Query("SELECT v FROM kv WHERE k = 'a'")
	w.run(20 * time.Millisecond)
	if got := w.exchange(writer, "UPDATE kv SET v = '2' WHERE k = 'a'"); got != "C:UPDATE 1 ZI" {
		t.Fatalf("write: %s", got)
	}
	if v, _ := w.srv.Get("a"); v != "2" {
		t.Fatalf("write not applied: %s", v)
	}
	w.run(200 * time.Millisecond)
	if rows := lastRows(reader.Messages()); len(rows) != 1 || rows[0] != "1" {
		t.Fatalf("in-flight read returned %v, want the old value 1", rows)
	}
	w.exchange(reader, "SELECT v FROM kv WHERE k = 'a'")
	w.run(200 * time.Millisecond)
	if rows := lastRows(reader.Messages()); len(rows) != 2 || rows[1] != "2" {
		t.Fatalf("later read returned %v", rows)
	}
	w.done(reader, writer)
}

func TestTransactionWritesApplyAtCommitAndVanishAtRollback(t *testing.T) {
	w := newWorld(t, fakepg.Config{Rows: []fakepg.Row{{Key: "a", Value: "1"}}})
	a, b := w.client(), w.client()
	w.exchange(a, "BEGIN", "UPDATE kv SET v = '2' WHERE k = 'a'", "INSERT INTO kv VALUES ('b', 'x')")
	w.exchange(a, "SELECT v FROM kv WHERE k = 'a'")
	if rows := lastRows(a.Messages()); rows[len(rows)-1] != "2" {
		t.Fatalf("own write not visible: %v", rows)
	}
	w.exchange(b, "SELECT v FROM kv WHERE k = 'a'")
	if rows := lastRows(b.Messages()); rows[len(rows)-1] != "1" {
		t.Fatalf("uncommitted write visible to another session: %v", rows)
	}
	if len(w.srv.WriteLog()) != 0 {
		t.Fatal("write log has an uncommitted write")
	}
	w.exchange(a, "COMMIT")
	if v, _ := w.srv.Get("a"); v != "2" {
		t.Fatal("commit did not apply")
	}
	w.exchange(a, "BEGIN", "DELETE FROM kv WHERE k = 'a'", "ROLLBACK")
	w.exchange(a, "UPDATE kv SET v = 'z' WHERE k = 'b'; SELECT 1/0")
	if v, ok := w.srv.Get("a"); !ok || v != "2" {
		t.Fatal("rolled back delete applied")
	}
	if v, _ := w.srv.Get("b"); v != "x" {
		t.Fatal("write of a failed implicit transaction applied")
	}
	log := w.srv.WriteLog()
	if len(log) != 2 || log[0].Op != fakepg.KindUpdate || log[1].Op != fakepg.KindInsert || log[0].TxID == 0 || log[0].TxID != log[1].TxID {
		t.Fatalf("write log %+v", log)
	}
	w.done(a, b)
	snap := w.srv.Snapshot()
	if snap.TxBegun != 3 || snap.TxCommitted != 1 || snap.TxRolledBack != 2 || snap.WritesApplied != 2 {
		t.Fatalf("%+v", snap)
	}
}

func TestWriteLogHasEveryAppliedWriteOnce(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	a := w.client()
	w.exchange(a, "INSERT INTO kv (k, v) VALUES ('k1', 'v1')")
	w.exchange(a, "/*client-7*/ UPDATE kv SET v = 'v2' WHERE k = 'k1'")
	w.exchange(a, "UPDATE kv SET v = 'none' WHERE k = 'missing'")
	w.exchange(a, "INSERT INTO kv VALUES ('k1', 'dup')")
	w.exchange(a, "DELETE FROM kv WHERE k = 'k1'")
	log := w.srv.WriteLog()
	if len(log) != 3 {
		t.Fatalf("log %+v", log)
	}
	seen := map[int64]bool{}
	for i, r := range log {
		if r.Seq != int64(i+1) || seen[r.StmtSeq] || r.TxID != 0 || r.Backend != a.End().ID() {
			t.Fatalf("record %d: %+v", i, r)
		}
		seen[r.StmtSeq] = true
	}
	if log[1].Tag != "client-7" || !strings.Contains(log[1].Text, "UPDATE kv") {
		t.Fatalf("tag and text: %+v", log[1])
	}
	stmts := w.srv.StatementLog()
	if len(stmts) != 5 || stmts[3].Code != "23505" {
		t.Fatalf("statement log %+v", stmts)
	}
	w.done(a)
}

func TestMaxConnectionsRefusesWithRealError(t *testing.T) {
	const limit = 5
	w := newWorld(t, fakepg.Config{MaxConnections: limit})
	var ps []*testProbe
	for i := 0; i < limit+3; i++ {
		p := w.dial()
		p.Startup("app", "")
		ps = append(ps, &testProbe{p})
	}
	w.run(5 * time.Millisecond)
	ok, refused := 0, 0
	for _, p := range ps {
		tr := p.Trace()
		switch {
		case strings.HasSuffix(tr, "K ZI"):
			ok++
		case tr == "connected E:53300 eof":
			refused++
			if msgs := p.Messages(); msgs[0].Severity != "FATAL" || msgs[0].Text != "sorry, too many clients already" {
				t.Fatalf("refusal %+v", msgs[0])
			}
		default:
			t.Fatalf("unexpected %s", tr)
		}
	}
	snap := w.srv.Snapshot()
	if ok != limit || refused != 3 || snap.Refused.TooMany != 3 || snap.Sessions.Peak != limit {
		t.Fatalf("ok %d refused %d %+v", ok, refused, snap)
	}
	for _, p := range ps {
		p.Terminate()
		p.Close()
	}
	w.quiet()
	late := w.client()
	w.done(late)
}

func TestUnknownUserAndDatabase(t *testing.T) {
	w := newWorld(t, fakepg.Config{Users: []string{"app"}, Databases: []string{"app"}})
	cases := []struct{ user, db, want string }{{"nobody", "app", "28000"}, {"app", "nodb", "3D000"}, {"", "app", "28000"}}
	for _, c := range cases {
		p := w.dial()
		p.Startup(c.user, c.db)
		w.run(time.Millisecond)
		if got := p.Trace(); got != "connected E:"+c.want+" eof" {
			t.Fatalf("%+v: %s", c, got)
		}
	}
	w.quiet()
	s := w.srv.Snapshot()
	if s.Refused.UnknownUser != 1 || s.Refused.UnknownDatabase != 1 || s.Refused.NoUser != 1 {
		t.Fatalf("%+v", s.Refused)
	}
}

func TestCancelHitsOnlyTheTargetBackend(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	a, b := w.client(), w.client()
	a.Query("SELECT pg_sleep(1)")
	b.Query("SELECT pg_sleep(1)")
	w.run(10 * time.Millisecond)
	wrong := w.dial()
	wrong.Cancel(a.Session().ProcessID(), []byte{0, 0, 0, 0})
	w.run(10 * time.Millisecond)
	right := w.dial()
	right.Cancel(a.Session().ProcessID(), a.Session().Secret())
	w.run(10 * time.Millisecond)
	if got := a.TraceFrom(len(a.Events()) - 2); got != "E:57014 ZI" {
		t.Fatalf("cancelled backend saw %s", a.Trace())
	}
	if strings.Contains(b.Trace(), "57014") {
		t.Fatal("other backend cancelled")
	}
	if wrong.Trace() != "connected eof" || right.Trace() != "connected eof" {
		t.Fatalf("cancel connections: %s / %s", wrong.Trace(), right.Trace())
	}
	w.run(2 * time.Second)
	if !strings.HasSuffix(b.Trace(), "C:SELECT 1 ZI") {
		t.Fatalf("other statement did not complete: %s", b.Trace())
	}
	idle := w.dial()
	idle.Cancel(a.Session().ProcessID(), a.Session().Secret())
	w.run(10 * time.Millisecond)
	w.done(a, b)
	s := w.srv.Snapshot()
	if s.CancelsReceived != 3 || s.CancelsApplied != 1 || s.CancelsNoMatch != 1 || s.CancelsIdle != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestCapacityQueuesStatementsInsideTheDatabase(t *testing.T) {
	w := newWorld(t, fakepg.Config{Capacity: 2, Service: fakepg.Service{Fixed: 10 * time.Millisecond}})
	var ps []*testProbe
	for i := 0; i < 4; i++ {
		ps = append(ps, &testProbe{w.client()})
	}
	start := w.s.Now()
	for _, p := range ps {
		p.Query("SELECT 1")
	}
	w.run(time.Millisecond)
	s := w.srv.Snapshot()
	if s.InService.Value != 2 || s.Waiting.Value != 2 {
		t.Fatalf("%+v", s)
	}
	w.run(50 * time.Millisecond)
	var lat []int64
	for _, p := range ps {
		ev := p.Events()
		lat = append(lat, int64(ev[len(ev)-1].At-start))
	}
	if lat[2] < 2*int64(10*time.Millisecond) || lat[0] >= lat[2] {
		t.Fatalf("latencies %v", lat)
	}
	for _, p := range ps {
		p.Terminate()
		p.Close()
	}
	w.quiet()
	if s := w.srv.Snapshot(); s.InService.Peak != 2 || s.Waiting.Peak != 2 {
		t.Fatalf("%+v", s)
	}
}

func TestStallWindowAddsToStatementsInService(t *testing.T) {
	w := newWorld(t, fakepg.Config{Service: fakepg.Service{Fixed: 10 * time.Millisecond, Stalls: []fakepg.Stall{{From: 0, To: 1 << 62, Extra: 150 * time.Millisecond}}}})
	p := w.client()
	from := w.s.Now()
	p.Query("SELECT 1")
	w.run(500 * time.Millisecond)
	ev := p.Events()
	if d := int64(ev[len(ev)-1].At - from); d < (160 * time.Millisecond).Nanoseconds() {
		t.Fatalf("stall not applied: %d", d)
	}
	w.done(p)
}

func TestLargeReplyIsStreamedUnderBackPressure(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.client()
	p.Query("SELECT repeat('x', 100) FROM generate_series(1, 20000)")
	w.run(2 * time.Second)
	rows := 0
	for _, m := range p.Messages() {
		if m.Type == testclient.MsgDataRow {
			rows++
			if len(m.Values[0]) != 100 {
				t.Fatal("row size")
			}
		}
	}
	if rows != 20000 || !strings.HasSuffix(p.Trace(), "C:SELECT 20000 ZI") {
		t.Fatalf("rows %d", rows)
	}
	w.done(p)
}
