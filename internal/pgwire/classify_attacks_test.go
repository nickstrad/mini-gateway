package pgwire_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const atkSeed int64 = 5052026

func atkReject(t *testing.T, qs ...string) {
	t.Helper()
	for _, q := range qs {
		for _, inTx := range []bool{false, true} {
			if u := p.ClassifyQuery([]byte(q), inTx); u.Class != work.ClassReject {
				t.Errorf("expected reject for %q (in tx %v); observed %+v", q, inTx, u)
			}
		}
	}
}

func atkNeverShared(t *testing.T, qs ...string) {
	t.Helper()
	for _, q := range qs {
		for _, inTx := range []bool{false, true} {
			if u := p.ClassifyQuery([]byte(q), inTx); u.SafeRead || u.Shareable {
				t.Errorf("%q (in tx %v) is safe-read or shareable: %+v", q, inTx, u)
			}
		}
	}
}

func atkForwardedOpaque(t *testing.T, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, clean := p.LexAll([]byte(q)); clean {
			t.Fatalf("attack must be untokenisable: %q", q)
		}
		for _, tx := range []bool{false, true} {
			u := p.ClassifyQuery([]byte(q), tx)
			if !u.Class.Forwards() || !u.Opaque || u.SafeRead || u.Shareable || u.HasKey {
				t.Errorf("an untokenisable message is forwarded as opaque and never shared, tx=%v %q; got %+v", tx, q, u)
			}
		}
	}
}

func TestClassifierRejectsUnicodeAndQuotedAdvisoryLockNames(t *testing.T) {
	atkReject(t, `SELECT U&"pg_advisory_\006cock"(91)`, `SELECT pg_catalog.U&"pg_try_advisory_!006cock" UESCAPE '!' (92)`, `SELECT "pg_advisory_unlock_all"/* ; */()`)
}

func TestClassifierRejectsTheWholeMessageForOneRejectedStatement(t *testing.T) {
	atkReject(t, `SELECT ';'; RESET/*x*/ALL; SELECT 51`, `VALUES (7); CREATE LOCAL TEMPORARY TABLE adv_hidden(a int)`, `SELECT $adv$; LISTEN false_alarm$adv$; LISTEN "actual"`)
}

func TestClassifierEscapeContinuationFollowsPostgres(t *testing.T) {
	for _, c := range []struct {
		q          string
		statements int
	}{
		{"SELECT E'ok'\n'\\'; SET application_name = 'adv_escape'; --'", 1},
		{"SELECT E'ok'\n'\\'; LISTEN adv_escape; --'", 1},
	} {
		u := p.ClassifyQuery([]byte(c.q), false)
		if u.Class != work.ClassStartLease || u.Statements != c.statements || u.Opaque {
			t.Errorf("%q: Postgres reads one escape string here and runs no session change; got %+v", c.q, u)
		}
	}
	atkReject(t, "SELECT E'ok'\n'\\' ; SELECT ' ; SET application_name = 'adv_escape' ; --'")
}

func TestClassifierNeverSharesUnknownCallsInGrammarPositions(t *testing.T) {
	atkNeverShared(t, `SELECT * FROM LATERAL unknown_adversary(8) AS x`, `WITH z(a) AS (VALUES (8)) SELECT unknown_adversary(a) FROM z`, `SELECT CASE WHEN true THEN unknown_adversary(8) ELSE 0 END`)
}

func TestClassifierNeverSharesNestedWritesAndLocks(t *testing.T) {
	atkNeverShared(t, `WITH x AS (MERGE INTO dst USING src ON false WHEN NOT MATCHED THEN INSERT VALUES (1) RETURNING *) SELECT * FROM x`, `SELECT * FROM (SELECT * FROM pg_class FOR KEY SHARE) AS x`, `SELECT * FROM (SELECT pg_advisory_xact_lock(97)) AS x`)
}

func TestClassifierNeverSharesTimeDependentLiterals(t *testing.T) {
	atkNeverShared(t, `SELECT 'now'::timestamp`, `SELECT CAST('today' AS date)`, `SELECT 'now'::timestamptz`, `SELECT a FROM t WHERE ts < 'now'`, `SELECT a FROM t WHERE d = 'TODAY'`)
}

func TestClassifierForwardsTruncatedMessagesAsOpaque(t *testing.T) {
	atkForwardedOpaque(t, `SELECT /* mg:share */ $abc$unclosed`, `SELECT E'backslash\`, `SELECT "unfinished`)
}

func TestClassifierForwardsBadBytesBetweenStatementsAsOpaque(t *testing.T) {
	atkForwardedOpaque(t, "SELECT 82;\x00SELECT 83", "SELECT 82;\x01SELECT 83", "SELECT 82;\\SELECT 83")
}

func TestClassifierRejectWinsOverUntokenisable(t *testing.T) {
	for _, q := range []string{`DISCARD ALL; SELECT 'unfinished`, `LISTEN adv; /* unfinished`, `RESET ALL; SELECT $tag$unfinished`} {
		u := p.ClassifyQuery([]byte(q), false)
		if u.Class != work.ClassReject || !u.Opaque || u.SafeRead || u.HasKey {
			t.Errorf("%q: a reject-list feature in the readable part is refused, and the message is still opaque; got %+v", q, u)
		}
	}
}

func atkInvariantPadding(t *testing.T, words []string) {
	t.Helper()
	base := p.ClassifyQuery([]byte(strings.Join(words, " ")), false)
	r := rand.New(rand.NewSource(atkSeed))
	pads := []string{" ", "\n", "/* adversarial ; $$ */", "/* one /* two */ end */", "-- adversarial\r\n"}
	for n := 0; n < 200; n++ {
		var b strings.Builder
		for i, w := range words {
			if i > 0 {
				b.WriteString(pads[r.Intn(len(pads))])
			}
			for _, c := range w {
				if c >= 'a' && c <= 'z' && r.Intn(2) == 0 {
					c -= 32
				}
				b.WriteRune(c)
			}
		}
		got := p.ClassifyQuery([]byte(b.String()), false)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("seed=%d iteration=%d %q base=%+v got=%+v", atkSeed, n, b.String(), base, got)
		}
	}
}

func TestClassifierPaddingInvariantTransactionControl(t *testing.T) {
	atkInvariantPadding(t, []string{"begin", "work", ";", "select", "19", ";", "rollback", "work", "and", "no", "chain"})
}

func TestClassifierPaddingInvariantLockingClause(t *testing.T) {
	atkInvariantPadding(t, []string{"select", "*", "from", "pg_class", "for", "no", "key", "update"})
}

func TestClassifierPaddingInvariantQualifiedLockFunction(t *testing.T) {
	atkInvariantPadding(t, []string{"select", "pg_catalog", ".", "pg_advisory_lock", "(", "991", ")"})
}

func atkExerciseBytes(t *testing.T, b []byte) {
	t.Helper()
	ts, _ := p.LexAll(b)
	end := 0
	for _, tok := range ts {
		if tok.Start != end || tok.End <= end || tok.End > len(b) {
			t.Fatalf("bad span %+v length=%d last=%d", tok, len(b), end)
		}
		end = tok.End
	}
	if end != len(b) {
		t.Fatal("lexer lost bytes")
	}
	p.ClassifyQuery(b, false)
	p.ClassifyQuery(b, true)
	p.ShareFits(b)
}

func TestClassifierEveryBytePairNeverPanicsAndCoversTheInput(t *testing.T) {
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			atkExerciseBytes(t, []byte{byte(a), byte(b)})
		}
	}
}

func TestClassifierSeededHostileBytesNeverPanic(t *testing.T) {
	count := 3000
	if os.Getenv("ADV_LONG") == "1" {
		count = 100000
	}
	r := rand.New(rand.NewSource(atkSeed))
	for i := 0; i < count; i++ {
		b := make([]byte, r.Intn(512))
		r.Read(b)
		atkExerciseBytes(t, b)
	}
}

func TestClassifierAllocationStaysUnderTheCeiling(t *testing.T) {
	for _, q := range []string{strings.Repeat("();", 1<<16), `SELECT "` + strings.Repeat(`a""`, 1<<16) + `"`, strings.Repeat("/*", 1<<16) + strings.Repeat("*/", 1<<16)} {
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		u := p.ClassifyQuery([]byte(q), false)
		runtime.KeepAlive(u)
		runtime.ReadMemStats(&b)
		limit := uint64(p.ClassifMemoryFactor*len(q) + p.ClassifMemoryConstant)
		if b.TotalAlloc-a.TotalAlloc > limit {
			t.Errorf("len=%d allocated=%d bound=%d", len(q), b.TotalAlloc-a.TotalAlloc, limit)
		}
	}
}

func atkFrames(t *testing.T, b []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(b) > 0 {
		if len(b) < 5 {
			t.Fatal("truncated frame")
		}
		n := int(binary.BigEndian.Uint32(b[1:5]))
		if n < 4 || n+1 > len(b) {
			t.Fatalf("bad frame length %d", n)
		}
		out = append(out, b[:n+1])
		b = b[n+1:]
	}
	return out
}

func TestReplyEveryRowWithPrefixAndStatusIsWellFormed(t *testing.T) {
	var builder p.ReplyBuilder
	var count int64
	for id := p.ErrRowID(0); id < p.ErrRowCount; id++ {
		row := p.ErrorRows[id]
		for _, status := range []byte{'I', 'T', 'E'} {
			prefix := []byte{9, 8, 7}
			b, a, e := builder.Build(prefix, id, status)
			if row.Reply == p.ErrorRowPassThrough {
				if e == nil {
					t.Error("pass-through produced local message")
				}
				continue
			}
			count++
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(b[:3], prefix) {
				t.Fatal("prefix overwritten")
			}
			fs := atkFrames(t, b[3:])
			if row.Reply == p.ErrorRowCloseOnly {
				if len(fs) != 0 || !a.Close {
					t.Fatal("close-only sent bytes")
				}
				continue
			}
			if len(fs) < 1 || fs[0][0] != 'E' {
				t.Fatal("missing ErrorResponse")
			}
			fields := map[byte]string{}
			body := fs[0][5:]
			for len(body) > 1 {
				end := bytes.IndexByte(body[1:], 0)
				if end < 0 {
					t.Fatal("unterminated field")
				}
				fields[body[0]] = string(body[1 : 1+end])
				body = body[end+2:]
			}
			if fields['C'] != row.Code || fields['M'] != row.Message || fields['S'] != row.Severity() || fields['D'] != p.ReplyDetailPrefix+row.ID {
				t.Fatalf("row %s fields=%v", row.ID, fields)
			}
			if row.Reply == p.ErrorRowError {
				if len(fs) != 2 || !bytes.Equal(fs[1], []byte{'Z', 0, 0, 0, 5, status}) {
					t.Fatalf("wrong Ready for %s: %x", row.ID, b)
				}
			} else if len(fs) != 1 {
				t.Fatalf("unexpected marker for %s", row.ID)
			}
		}
	}
	if builder.Snapshot().Total != count {
		t.Fatal("lost counts")
	}
}

func TestReplyInvalidInputsDoNotPoisonReuse(t *testing.T) {
	var b p.ReplyBuilder
	for i := 0; i < 256; i++ {
		out, _, e := b.Build([]byte{4}, p.ErrRowID(255), byte(i))
		if e == nil || !bytes.Equal(out, []byte{4}) {
			t.Fatal("invalid row emitted reply")
		}
	}
	out, _, e := b.Build(nil, p.ErrRowShed, 'E')
	if e != nil || len(atkFrames(t, out)) != 2 || b.Snapshot().ByRow[p.ErrRowShed] != 1 {
		t.Fatal("builder failed reuse")
	}
}

func TestSyncDiscardDropsEveryByteUntilSyncAndRecovers(t *testing.T) {
	for n := 0; n < 256; n++ {
		if n == 'S' || n == 'X' {
			continue
		}
		var d p.SyncDiscard
		if d.Step('P') != p.SyncDiscardRefuse {
			t.Fatal("Parse not refused")
		}
		if d.Step(byte(n)) != p.SyncDiscardDrop {
			t.Fatalf("byte %d escaped discard", n)
		}
		if d.Step('S') != p.SyncDiscardReady || d.Step('Q') != p.SyncDiscardPass {
			t.Fatal("no recovery")
		}
		s := d.Snapshot()
		if s.Refused != 1 || s.Discarded != 1 || s.Synced != 1 {
			t.Fatalf("counters %+v", s)
		}
	}
}

func TestContractGeneratorsTrackTableMutation(t *testing.T) {
	old := p.ErrorRows[0]
	defer func() { p.ErrorRows[0] = old }()
	before := p.ErrorRowsMarkdown()
	p.ErrorRows[0].Message = "adversary sentinel"
	if p.ErrorRowsMarkdown() == before || !strings.Contains(p.ErrorRowsMarkdown(), "adversary sentinel") {
		t.Fatal("error generator ignores code")
	}
	prev := p.RejectRules[0]
	defer func() { p.RejectRules[0] = prev }()
	p.RejectRules[0].Why = "adversary sentinel"
	if !strings.Contains(p.RejectListMarkdown(), "adversary sentinel") {
		t.Fatal("reject generator ignores code")
	}
	allow := p.ShareAllowList[0]
	defer func() { p.ShareAllowList[0] = allow }()
	p.ShareAllowList[0] = "adversary_allow_sentinel"
	if !strings.Contains(p.SafeShapeMarkdown(), "adversary_allow_sentinel") {
		t.Fatal("allow-list generator ignores code")
	}
	stop := p.ShareStopWords[0]
	defer func() { p.ShareStopWords[0] = stop }()
	p.ShareStopWords[0] = "adversary_stop_sentinel"
	if !strings.Contains(p.SafeShapeMarkdown(), "adversary_stop_sentinel") {
		t.Fatal("stop-list generator ignores code")
	}
}

func TestClassifierOutputIsExpressedInWorkTypesOnly(t *testing.T) {
	var f func([]byte, bool) work.Unit = p.ClassifyQuery
	_ = f
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(v reflect.Type) {
		if seen[v] {
			return
		}
		seen[v] = true
		if s := v.PkgPath(); s != "" && s != "github.com/nickstrad/mini-gateway/internal/work" {
			t.Errorf("foreign type %v", v)
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i).Type)
			}
		case reflect.Array, reflect.Slice, reflect.Pointer:
			walk(v.Elem())
		}
	}
	walk(reflect.TypeOf(work.Unit{}))
	walk(reflect.TypeOf(work.Outcome{}))
}

func TestClassifierShareKeyIsACopyAndScoped(t *testing.T) {
	for _, scope := range []work.KeyScope{work.ScopeGlobal, work.ScopeTenant} {
		c := p.Classifier{Config: p.ClassifierConfig{KeyScope: scope}}
		src := []byte("/* mg:share */ SELECT 103")
		a := c.Classify("a", src, false)
		b := c.Classify("b", src, false)
		if !a.HasKey || !a.Hinted || !bytes.Equal(a.Key.Text, src) {
			t.Fatalf("bad key %+v", a)
		}
		src[0] = '!'
		if a.Key.Text[0] != '/' {
			t.Fatal("aliased caller buffer")
		}
		if a.Key.Equal(b.Key) != (scope == work.ScopeGlobal) {
			t.Fatal("tenant isolation wrong")
		}
		if e := c.Snapshot().Check(); e != nil {
			t.Fatal(e)
		}
	}
}

func TestClassifierUnaryOperatorBoundaryMatchesWhitespaceForm(t *testing.T) {
	a, b := "SELECT 71+-3", "SELECT 71+ -3"
	x, y := p.ClassifyQuery([]byte(a), false), p.ClassifyQuery([]byte(b), false)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("whitespace between PostgreSQL + and unary - tokens changes classification: %q %+v versus %q %+v", a, x, b, y)
	}
}

func atkPacket(kind byte, body []byte) []byte {
	b := make([]byte, 5+len(body))
	b[0] = kind
	binary.BigEndian.PutUint32(b[1:], uint32(4+len(body)))
	copy(b[5:], body)
	return b
}

func atkFakeFilter(t *testing.T, qs []string) {
	t.Helper()
	for _, ablate := range []bool{false, true} {
		s := sim.New(sim.Config{Seed: atkSeed})
		n := simnet.New(s)
		srv, e := fakepg.New(n, fakepg.Config{Addr: "adv", Users: []string{"adv"}, Databases: []string{"adv"}})
		if e != nil {
			t.Fatal(e)
		}
		client := workload.NewProbe(n, "adv", simnet.Options{})
		client.Startup("adv", "adv")
		run := func() {
			t.Helper()
			if e := s.RunUntilQuiet(); e != nil {
				t.Fatal(e)
			}
		}
		run()
		if srv.Snapshot().StartupsCompleted != 1 {
			t.Fatal("dead fake")
		}
		var classifier p.Classifier
		var replies p.ReplyBuilder
		for _, q := range qs {
			frame := atkPacket('Q', append([]byte(q), 0))
			u := classifier.Classify("adv", frame[5:len(frame)-1], false)
			if u.Class.Forwards() || ablate {
				client.Query(q)
			} else {
				b, _, e := replies.Build(nil, p.ErrRowUnsupportedFeature, 'I')
				if e != nil || len(atkFrames(t, b)) != 2 {
					t.Fatal("bad refusal")
				}
			}
			run()
		}
		before := srv.Snapshot().StatementsStarted
		if !ablate && before != 0 {
			t.Errorf("reject-list statements reached fake: %d queries=%q", before, qs)
		}
		if ablate && before == 0 {
			t.Fatal("ablation did not reach fake")
		}
		if !ablate && replies.Snapshot().Total != int64(len(qs)) {
			t.Errorf("missing refusals: %+v", replies.Snapshot())
		}
		client.Query("SELECT 1")
		run()
		if srv.Snapshot().StatementsStarted != before+1 {
			t.Fatal("no recovery read")
		}
		client.Terminate()
		run()
		client.Close()
		run()
		if e := s.CheckQuiesce(); e != nil {
			t.Fatal(e)
		}
		if e := classifier.Snapshot().Check(); e != nil {
			t.Fatal(e)
		}
	}
}

func TestClassifierMultiStatementRejectNeverReachesTheFake(t *testing.T) {
	atkFakeFilter(t, []string{"SELECT 41; SET application_name = 'adv'", "SELECT 42; LISTEN adv", "SELECT 43; DISCARD PLANS"})
}

func TestClassifierNestedFunctionRejectNeverReachesTheFake(t *testing.T) {
	atkFakeFilter(t, []string{"SELECT coalesce(pg_try_advisory_lock(818), false)", "SELECT (SELECT set_config('application_name','adv',false))"})
}

func TestClassifierSetHiddenBehindAnEscapeContinuationNeverReachesTheFake(t *testing.T) {
	atkFakeFilter(t, []string{"SELECT E'ok'\n'\\' ; SELECT ' ; SET application_name = 'adv_escape' ; --'"})
}
