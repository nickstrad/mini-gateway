package adv_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

const seed int64 = 6062026

type rig struct {
	t   *testing.T
	e   *proxy.Engine
	now proxy.Instant
	db  proxy.ConnID
	log []proxy.Action
}

func newRig(t *testing.T, edit func(*proxy.Config)) *rig {
	t.Helper()
	t.Logf("seed=%d", seed)
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	c := w.Proxy.Engine.Config()
	c.Connections = 1
	c.QueueCap = 2
	c.ReplyBufferCap = proxy.MinReplyBufferCap
	if edit != nil {
		edit(&c)
	}
	e, err := proxy.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, e: e, now: 1}
	a := e.Start(1, nil)
	r.save(a)
	for _, x := range a {
		if x.Kind == proxy.ActDBOpen {
			r.db = x.Conn
		}
	}
	if r.db == 0 {
		t.Fatal("no initial dial")
	}
	return r
}
func (r *rig) save(a []proxy.Action) {
	for _, x := range a {
		x.Data = bytes.Clone(x.Data)
		r.log = append(r.log, x)
	}
}
func (r *rig) ev(k proxy.EventKind, id proxy.ConnID, b []byte, n int) []proxy.Action {
	a := r.e.Step(r.now, proxy.Event{Kind: k, Conn: id, Data: b, N: n}, nil)
	r.save(a)
	return a
}
func (r *rig) ack(a []proxy.Action) {
	for _, x := range a {
		if x.Kind == proxy.ActClientWrite {
			r.ev(proxy.EvClientWrote, x.Conn, nil, len(x.Data))
		}
		if x.Kind == proxy.ActDBWrite {
			r.ev(proxy.EvDBWrote, x.Conn, nil, len(x.Data))
		}
	}
}
func (r *rig) ready() {
	r.ack(r.ev(proxy.EvDBOpened, r.db, nil, 0))
	r.ev(proxy.EvDBData, r.db, pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I'), 0)
	if r.e.Snapshot().DBIdle.Value != 1 {
		r.t.Fatal("database did not become idle")
	}
}
func (r *rig) client(id proxy.ConnID) {
	r.ev(proxy.EvClientAccepted, id, nil, 0)
	b := pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: "tenant0"}, {Name: "database", Value: "app"}})
	r.ack(r.ev(proxy.EvClientData, id, b[:8], 0))
	r.ack(r.ev(proxy.EvClientData, id, b[8:], 0))
	if _, ok := r.e.Tenant(id); !ok {
		r.t.Fatal("client did not authenticate")
	}
}
func (r *rig) query(id proxy.ConnID, s string) []proxy.Action {
	return r.ev(proxy.EvClientData, id, pgwire.AppendQuery(nil, []byte(s)), 0)
}
func msg(typ byte, b []byte) []byte {
	return append(pgwire.AppendMessageHeader(nil, typ, len(b)), b...)
}
func reply(tag string, status byte) []byte {
	return pgwire.AppendReadyForQuery(msg('C', append([]byte(tag), 0)), status)
}
func joined(a []proxy.Action, k proxy.ActionKind, id proxy.ConnID) []byte {
	var b []byte
	for _, x := range a {
		if x.Kind == k && x.Conn == id {
			b = append(b, x.Data...)
		}
	}
	return b
}
func has(a []proxy.Action, k proxy.ActionKind, id proxy.ConnID) bool {
	for _, x := range a {
		if x.Kind == k && x.Conn == id {
			return true
		}
	}
	return false
}
func (r *rig) finish(tag string, status byte) []proxy.Action {
	a := r.ev(proxy.EvDBData, r.db, reply(tag, status), 0)
	r.ack(a)
	return a
}
func (r *rig) reset() { r.ack(r.ev(proxy.EvDBData, r.db, reply("DISCARD ALL", 'I'), 0)) }
func (r *rig) wantOwner(id proxy.ConnID) {
	r.t.Helper()
	got, ok := r.e.LentTo(r.db)
	if !ok || got != id {
		r.t.Fatalf("owner=%d present=%v want=%d", got, ok, id)
	}
}

func fragment(t *testing.T, mode int) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.ack(r.query(11, "SELECT 314159"))
	r.log = nil
	b := append(msg('T', []byte{0, 0}), msg('D', []byte{0, 1, 0, 0, 0, 6, '3', '1', '4', '1', '5', '9'})...)
	b = append(b, reply("SELECT 1", 'I')...)
	rnd := rand.New(rand.NewSource(seed))
	for off := 0; off < len(b); {
		n := 1
		if mode == 1 {
			n = 1 + rnd.Intn(13)
		}
		if mode == 2 {
			n = 5 - off%5
		}
		n = min(n, len(b)-off)
		r.ev(proxy.EvDBData, r.db, b[off:off+n], 0)
		off += n
	}
	got := joined(r.log, proxy.ActClientWrite, 11)
	if !bytes.Equal(got, b) {
		t.Fatalf("reply altered: got %x want %x", got, b)
	}
}
func TestAdv_1_ByteSplitsThroughRow(t *testing.T)  { fragment(t, 0) }
func TestAdv_1_SeededIrregularSplits(t *testing.T) { fragment(t, 1) }
func TestAdv_1_HeaderBoundarySplits(t *testing.T)  { fragment(t, 2) }

func transaction(t *testing.T, status byte, closeOwner bool) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.client(12)
	r.ack(r.query(11, "BEGIN"))
	r.finish("BEGIN", status)
	r.query(12, "SELECT 72")
	r.wantOwner(11)
	if closeOwner {
		a := r.ev(proxy.EvClientClosed, 11, nil, 0)
		if !has(a, proxy.ActDBClose, r.db) {
			t.Fatal("vanished transaction owner left reusable database")
		}
		return
	}
	r.ack(r.query(11, "ROLLBACK"))
	r.wantOwner(11)
	r.finish("ROLLBACK", 'I')
	if r.e.Snapshot().Resetting.Value != 1 {
		t.Fatal("transaction release did not enter reset")
	}
	r.reset()
	r.wantOwner(12)
}
func TestAdv_2_FailedTransactionKeepsOwner(t *testing.T) { transaction(t, 'E', false) }
func TestAdv_2_QueuedContenderCannotSteal(t *testing.T)  { transaction(t, 'T', false) }
func TestAdv_2_OwnerVanishClosesLease(t *testing.T)      { transaction(t, 'T', true) }

func resetRig(t *testing.T) *rig {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.client(12)
	r.ack(r.query(11, "SELECT 81"))
	r.query(12, "SELECT 82")
	a := r.finish("SELECT 1", 'I')
	if !bytes.Equal(joined(a, proxy.ActDBWrite, r.db), pgwire.AppendQuery(nil, []byte("DISCARD ALL"))) {
		t.Fatal("missing standalone reset")
	}
	return r
}
func TestAdv_3_DuplicateResetTag(t *testing.T) {
	r := resetRig(t)
	b := append(msg('C', []byte("DISCARD ALL\x00")), reply("DISCARD ALL", 'I')...)
	a := r.ev(proxy.EvDBData, r.db, b, 0)
	if !has(a, proxy.ActDBClose, r.db) || has(a, proxy.ActDBWrite, r.db) {
		t.Fatal("duplicate tag allowed reuse")
	}
}
func TestAdv_3_ResetWithTrailingReply(t *testing.T) {
	r := resetRig(t)
	b := append(reply("DISCARD ALL", 'I'), reply("SELECT 1", 'I')...)
	a := r.ev(proxy.EvDBData, r.db, b, 0)
	if has(a, proxy.ActDBWrite, r.db) {
		t.Fatalf("database lent with unread reset residue; wrote %q", joined(a, proxy.ActDBWrite, r.db))
	}
}
func TestAdv_3_ResetDeadlineTie(t *testing.T) {
	r := resetRig(t)
	r.now += proxy.Instant(time.Second)
	a := r.ev(proxy.EvDBData, r.db, reply("DISCARD ALL", 'I'), 0)
	if has(a, proxy.ActDBWrite, r.db) {
		t.Fatal("reset at expired deadline dispatched queued query")
	}
	if !has(a, proxy.ActDBClose, r.db) {
		t.Fatal("expired reset was not closed")
	}
}

func TestAdv_4_PartialCommandThenLoss(t *testing.T) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.ack(r.query(11, "SELECT 21"))
	r.ev(proxy.EvDBData, r.db, msg('C', []byte("SELECT 1\x00")), 0)
	a := r.ev(proxy.EvDBClosed, r.db, nil, 0)
	if !has(a, proxy.ActClientClose, 11) || len(joined(a, proxy.ActClientWrite, 11)) != 0 {
		t.Fatalf("partial reply completed or remained open: %v", a)
	}
}
func TestAdv_4_QueueShedHasSingleEnd(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.QueueCap = 1 })
	r.ready()
	for i := proxy.ConnID(11); i < 14; i++ {
		r.client(i)
	}
	r.query(11, "SELECT 1")
	r.query(12, "SELECT 2")
	a := r.query(13, "SELECT 3")
	b := joined(a, proxy.ActClientWrite, 13)
	if !bytes.Contains(b, []byte("53M01")) || bytes.Count(b, pgwire.AppendReadyForQuery(nil, 'I')) != 1 {
		t.Fatalf("shed reply=%x", b)
	}
}
func TestAdv_4_RejectInsideFailedTransaction(t *testing.T) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.ack(r.query(11, "BEGIN"))
	r.finish("BEGIN", 'E')
	a := r.query(11, "LISTEN channel")
	b := joined(a, proxy.ActClientWrite, 11)
	if !bytes.Contains(b, []byte("0A000")) || !bytes.HasSuffix(b, pgwire.AppendReadyForQuery(nil, 'E')) {
		t.Fatalf("rejection lost failed transaction status: %x", b)
	}
}

func reach(t *testing.T, n int, replyByte bool) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.query(11, "UPDATE kv SET v='x' WHERE k='y'")
	if n > 0 {
		r.ev(proxy.EvDBWrote, r.db, nil, n)
	}
	if replyByte {
		r.ev(proxy.EvDBData, r.db, []byte{'C'}, 0)
	}
	r.ev(proxy.EvDBClosed, r.db, nil, 0)
	s := r.e.Snapshot()
	if s.Failed != 1 {
		t.Fatalf("failure not exercised: %d", s.Failed)
	}
	if (n > 0 || replyByte) && s.ReachedNo != 0 {
		t.Fatalf("observed query progress but reached_no=%d", s.ReachedNo)
	}
	if n == 0 && !replyByte && s.ReachedNo != 1 {
		t.Fatalf("zero accepted bytes reached_no=%d", s.ReachedNo)
	}
}
func TestAdv_5_FirstByteAccepted(t *testing.T)      { reach(t, 1, false) }
func TestAdv_5_ResponseBeforeWriteAck(t *testing.T) { reach(t, 0, true) }
func TestAdv_5_ZeroAcceptedBytes(t *testing.T)      { reach(t, 0, false) }

func TestAdv_6_QueueStorm(t *testing.T) {
	r := newRig(t, nil)
	r.ready()
	for i := proxy.ConnID(11); i < 211; i++ {
		r.client(i)
		r.query(i, fmt.Sprintf("SELECT %d", i))
	}
	s := r.e.Snapshot()
	if s.Queued.Value != 2 || s.Shed != 197 {
		t.Fatalf("queue=%d shed=%d", s.Queued.Value, s.Shed)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}
func TestAdv_6_StartupCapBurst(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.MaxStartup = 3 })
	for i := proxy.ConnID(11); i < 111; i++ {
		r.ev(proxy.EvClientAccepted, i, nil, 0)
	}
	s := r.e.Snapshot()
	if s.ClientsInStartup.Value != 3 || s.ClientsRefused != 97 {
		t.Fatalf("startup=%d refused=%d", s.ClientsInStartup.Value, s.ClientsRefused)
	}
}
func TestAdv_6_RowLargerThanRelay(t *testing.T) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	r.ack(r.query(11, "SELECT 8"))
	b := msg('D', append([]byte{0, 1, 0, 2, 0, 0}, bytes.Repeat([]byte{'x'}, 131072)...))
	paused := false
	for len(b) > 0 && !paused {
		n := min(proxy.MaxRead, len(b))
		a := r.ev(proxy.EvDBData, r.db, b[:n], 0)
		b = b[n:]
		paused = has(a, proxy.ActDBPause, r.db)
	}
	s := r.e.Snapshot()
	if !paused || s.RelayHighWater > s.RelayCap {
		t.Fatalf("pause=%v highwater=%d cap=%d", paused, s.RelayHighWater, s.RelayCap)
	}
}

func deadline(t *testing.T, kind int) {
	r := newRig(t, nil)
	id := proxy.ConnID(11)
	var want proxy.CloseReason
	switch kind {
	case 0:
		r.ev(proxy.EvClientAccepted, id, nil, 0)
		r.ev(proxy.EvClientData, id, []byte{0, 0, 0}, 0)
		want = proxy.CloseClientStartup
	case 1:
		r.client(id)
		r.ev(proxy.EvClientData, id, []byte{'Q', 0, 0}, 0)
		want = proxy.CloseClientViolation
	case 2:
		r.ready()
		r.client(id)
		r.ack(r.query(id, "BEGIN"))
		r.finish("BEGIN", 'T')
		want = proxy.CloseClientIdle
	}
	r.now = 1 + proxy.Instant(5*time.Second)
	if kind == 2 {
		r.now = 1 + proxy.Instant(10*time.Minute)
	}
	r.ev(proxy.EvTimer, 0, nil, 0)
	s := r.e.Snapshot()
	if s.ClientCloses[want] != 1 {
		t.Fatalf("deadline did not close with %s: %v", want, s.ClientCloses)
	}
}
func TestAdv_7_PartialStartupDeadline(t *testing.T)  { deadline(t, 0) }
func TestAdv_7_PartialQueryDeadline(t *testing.T)    { deadline(t, 1) }
func TestAdv_7_IdleTransactionDeadline(t *testing.T) { deadline(t, 2) }

func TestAdv_9_RequireAfterGSSRefusal(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.Client.TLS = pgwire.ClientTLSRequire })
	r.ev(proxy.EvClientAccepted, 11, nil, 0)
	r.ack(r.ev(proxy.EvClientData, 11, pgwire.AppendGSSRequest(nil), 0))
	b := pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: "tenant0"}})
	a := r.ev(proxy.EvClientData, 11, b, 0)
	if !bytes.Contains(joined(a, proxy.ActClientWrite, 11), []byte("28000")) {
		t.Fatalf("plaintext after GSS was not refused: %v", a)
	}
}
func TestAdv_9_SSLWithTrailingStartup(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.Client.TLS = pgwire.ClientTLSRequire })
	r.ev(proxy.EvClientAccepted, 11, nil, 0)
	b := append(pgwire.AppendSSLRequest(nil), pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: "tenant0"}})...)
	a := r.ev(proxy.EvClientData, 11, b, 0)
	if !has(a, proxy.ActClientClose, 11) {
		t.Fatalf("buffered plaintext survived TLS decision: %v", a)
	}
}
func TestAdv_9_VerifyFullRefusedTLS(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.Backend.TLS = pgwire.BackendTLSVerifyFull })
	r.ev(proxy.EvDBOpened, r.db, nil, 0)
	a := r.ev(proxy.EvDBData, r.db, []byte{'N'}, 0)
	if !has(a, proxy.ActDBClose, r.db) || r.e.Snapshot().DBIdle.Value != 0 {
		t.Fatal("verify-full continued after server refused encryption")
	}
}

func reject(t *testing.T, sql string) {
	r := newRig(t, nil)
	r.ready()
	r.client(11)
	a := r.query(11, sql)
	b := joined(a, proxy.ActClientWrite, 11)
	if has(a, proxy.ActDBWrite, r.db) || !bytes.Contains(b, []byte("0A000")) {
		t.Fatalf("documented rejection forwarded or wrong response: sql=%q actions=%v", sql, a)
	}
}
func TestAdv_10_CommentedMixedCaseListen(t *testing.T) {
	reject(t, "/* outer /* inner */ tail */ LiStEn \"odd channel\"")
}
func TestAdv_10_PrepareWithWhitespace(t *testing.T) { reject(t, "\r\nPREPARE\tq AS SELECT 909") }
func TestAdv_10_CopyAfterLineComment(t *testing.T)  { reject(t, "-- statement\r\nCOPY kv TO STDOUT") }
