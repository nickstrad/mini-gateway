package fakepg_test

import (
	"encoding/binary"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// Proves Kind.IsWrite is true only for UPDATE, INSERT, and DELETE and every Kind has a name.
func TestKindIsWriteOnlyForRowChangingStatements(t *testing.T) {
	writes := map[fakepg.Kind]bool{fakepg.KindUpdate: true, fakepg.KindInsert: true, fakepg.KindDelete: true}
	for k := fakepg.Kind(0); k < fakepg.NumKinds; k++ {
		if k.IsWrite() != writes[k] {
			t.Fatalf("%s: IsWrite %v", k, k.IsWrite())
		}
		if k.String() == "" || k.String() == "?" {
			t.Fatalf("kind %d has no name", k)
		}
	}
}

// Proves CodeValue maps valid 5-character SQLSTATE strings injectively to base-36 integers and rejects invalid inputs with -1.
func TestCodeValueIsTheBase36ValueOfTheCode(t *testing.T) {
	seen := map[int64]string{}
	for _, code := range []string{"00000", "22012", "23505", "25P02", "53300", "57014", "57P01", "ZZZZZ"} {
		want, _ := strconv.ParseInt(code, 36, 64)
		v := fakepg.CodeValue(code)
		if v != want || v < 0 {
			t.Fatalf("%s: %d, want %d", code, v, want)
		}
		if other, dup := seen[v]; dup {
			t.Fatalf("%s and %s share %d", code, other, v)
		}
		seen[v] = code
	}
	for _, bad := range []string{"", "57-14", "é"} {
		if fakepg.CodeValue(bad) != -1 {
			t.Fatalf("%q: %d, want -1", bad, fakepg.CodeValue(bad))
		}
	}
}

// Proves Config normalization applies defaults and hard caps and exposes the underlying simnet listener.
func TestConfigDefaultsAndLimits(t *testing.T) {
	w := newWorld(t, fakepg.Config{MaxReplyBytes: fakepg.MaxReplyBytesLimit + 1, WriteLog: fakepg.LogConfig{Cap: fakepg.MaxLogCap + 1}})
	c := w.srv.Config()
	if c.Addr != fakepg.DefaultAddr || w.srv.Addr() != fakepg.DefaultAddr || c.MaxConnections != fakepg.DefaultMaxConnections ||
		c.MaxRows != fakepg.DefaultMaxRows || c.MaxReplyBytes != fakepg.MaxReplyBytesLimit || c.MaxMessage != fakepg.DefaultMaxMessage ||
		c.ServerVersion != fakepg.DefaultServerVersion || c.AfterDelay != fakepg.DefaultAfterDelay ||
		c.WriteLog.Cap != fakepg.MaxLogCap || c.StatementLog.Cap != fakepg.DefaultLogCap {
		t.Fatalf("%+v", c)
	}
	if w.srv.Listener() == nil {
		t.Fatal("no listener")
	}
	w.s.ApplyFault(sim.Fault{Kind: simnet.FaultRefuse, Target: w.srv.Listener().Index()})
	p := w.dial()
	w.run(time.Millisecond)
	if p.Trace() != "refused" {
		t.Fatalf("a refuse fault on the listener did not refuse: %s", p.Trace())
	}
}

// Proves New rejects initial rows exceeding MaxRows and duplicate listener addresses.
func TestNewRefusesBadSetups(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	n := simnet.New(s)
	if _, err := fakepg.New(n, fakepg.Config{MaxRows: 1, Rows: []fakepg.Row{{Key: "a"}, {Key: "b"}}}); !errors.Is(err, fakepg.ErrTableFull) {
		t.Fatalf("more initial rows than MaxRows: %v", err)
	}
	if _, err := fakepg.New(n, fakepg.Config{Addr: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fakepg.New(n, fakepg.Config{Addr: "x"}); err == nil {
		t.Fatal("a second server on one address")
	}
}

// Proves FaultKinds and FaultName cover every pg.* fault kind with unique names and return "" outside the range.
func TestFaultNamesAndKinds(t *testing.T) {
	kinds := fakepg.FaultKinds()
	if len(kinds) != fakepg.NumFaults {
		t.Fatalf("%d kinds, NumFaults %d", len(kinds), fakepg.NumFaults)
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		name := fakepg.FaultName(k)
		if name == "" || seen[name] || name[:3] != "pg." {
			t.Fatalf("kind %d named %q", k, name)
		}
		seen[name] = true
	}
	if fakepg.FaultName(fakepg.FaultRefuse-1) != "" || fakepg.FaultName(fakepg.FaultClear+1) != "" {
		t.Fatal("a name outside the range")
	}
	if fakepg.FaultName(fakepg.FaultClear) != "pg.clear" {
		t.Fatal("clear")
	}
}

// Proves TransactionLog records committed, rolled-back, failed-commit, implicit, and lost transactions accurately.
func TestTransactionLogRecordsEveryOutcome(t *testing.T) {
	w := newWorld(t, fakepg.Config{Rows: []fakepg.Row{{Key: "a", Value: "1"}}})
	p, q := w.client(), w.client()
	w.exchange(p, "BEGIN", "UPDATE kv SET v = '2' WHERE k = 'a'", "COMMIT")
	w.exchange(p, "BEGIN", "DELETE FROM kv WHERE k = 'a'", "ROLLBACK")
	w.exchange(p, "UPDATE kv SET v = '3' WHERE k = 'a'; SELECT 1")
	w.exchange(p, "BEGIN", "SELECT 1/0", "COMMIT")
	w.exchange(q, "BEGIN", "UPDATE kv SET v = '9' WHERE k = 'a'")
	q.Reset()
	w.run(time.Millisecond)
	got := ""
	for _, r := range w.srv.TransactionLog() {
		got += strconv.FormatInt(r.TxID, 10) + ":" + strconv.Itoa(int(r.Outcome)) + "/" + strconv.FormatInt(r.Writes, 10) + " "
	}
	want := "1:1/1 2:2/1 3:1/1 4:2/0 5:3/1 "
	if got != want {
		t.Fatalf("transaction log %q, want %q (1 committed, 2 rolled back, 3 lost)", got, want)
	}
	if s := w.srv.Snapshot(); s.TxLogLen != 5 || s.TxLogDropped != 0 || s.TxCommitted != 2 || s.TxRolledBack != 2 || s.TxLost != 1 {
		t.Fatalf("%+v", s)
	}
	w.done(p)
}

// Proves LogRing retains the newest Cap entries in order while counting drops, and LogCountOnly drops all entries.
func TestLogRingKeepsTheNewestEntriesInOrder(t *testing.T) {
	w := newWorld(t, fakepg.Config{WriteLog: fakepg.LogConfig{Cap: 3}, StatementLog: fakepg.LogConfig{Mode: fakepg.LogCountOnly}})
	p := w.client()
	for i := 0; i < 5; i++ {
		w.exchange(p, "INSERT INTO kv VALUES ('k"+itoa(i)+"', 'v')")
	}
	log := w.srv.WriteLog()
	if len(log) != 3 || log[0].Seq != 3 || log[1].Seq != 4 || log[2].Seq != 5 || log[2].Key != "k4" {
		t.Fatalf("%+v", log)
	}
	s := w.srv.Snapshot()
	if s.WriteLogLen != 3 || s.WriteLogDropped != 2 || s.WritesApplied != 5 || s.StatementLogLen != 0 || s.StatementLogDropped != 5 || len(w.srv.StatementLog()) != 0 {
		t.Fatalf("%+v", s)
	}
	w.done(p)
}

// Proves Framer handles untyped startup packets, transitions to typed frames, and rejects impossible or oversized lengths.
func TestServerFramerSplitsStartupAndTypedMessages(t *testing.T) {
	var f fakepg.Framer
	f.Startup = true
	pkt := binary.BigEndian.AppendUint32(nil, 8)
	pkt = binary.BigEndian.AppendUint32(pkt, 80877103)
	q := append([]byte{'Q'}, binary.BigEndian.AppendUint32(nil, 6)...)
	q = append(q, 'x', 0)
	f.Feed(pkt[:3])
	if _, _, ok, err := f.Next(); ok || err != nil || f.Buffered() != 3 {
		t.Fatalf("partial startup: ok %v err %v buffered %d", ok, err, f.Buffered())
	}
	f.Feed(append(pkt[3:], q...))
	typ, body, ok, err := f.Next()
	if !ok || err != nil || typ != 0 || len(body) != 4 || f.Buffered() != len(q) {
		t.Fatalf("startup: %d %v %v %v buffered %d", typ, body, ok, err, f.Buffered())
	}
	f.Startup = false
	typ, body, ok, err = f.Next()
	if !ok || err != nil || typ != 'Q' || string(body) != "x\x00" || f.Buffered() != 0 {
		t.Fatalf("query: %c %q %v %v", typ, body, ok, err)
	}
	f.Feed([]byte{'Q', 0, 0, 0, 3})
	if _, _, _, err := f.Next(); !errors.Is(err, fakepg.ErrFrameLength) {
		t.Fatalf("length 3: %v", err)
	}
	g := fakepg.Framer{Max: 10}
	g.Feed([]byte{'Q', 0, 0, 0, 15})
	if _, _, _, err := g.Next(); !errors.Is(err, fakepg.ErrFrameTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	h := fakepg.Framer{Startup: true}
	h.Feed(binary.BigEndian.AppendUint32(nil, fakepg.MaxStartupPacket+5))
	if _, _, _, err := h.Next(); !errors.Is(err, fakepg.ErrFrameTooLarge) {
		t.Fatalf("startup over 10000 bytes: %v", err)
	}
}
