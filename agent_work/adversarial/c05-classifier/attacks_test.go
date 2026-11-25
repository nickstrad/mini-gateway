package adv_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const seed int64 = 5052026
const root = "../../.."

func begin(t *testing.T) { t.Helper(); t.Logf("seed=%d", seed) }
func reject(t *testing.T, qs ...string) {
	t.Helper()
	begin(t)
	for _, q := range qs {
		u := p.ClassifyQuery([]byte(q), false)
		if u.Class != work.ClassReject {
			t.Errorf("expected reject for %q; observed %+v", q, u)
		}
	}
}
func TestAdv_1_UnicodeFunctionNames(t *testing.T) {
	reject(t, `SELECT U&"pg_advisory_\006cock"(91)`, `SELECT pg_catalog.U&"pg_try_advisory_!006cock" UESCAPE '!' (92)`, `SELECT "pg_advisory_unlock_all"/* ; */()`)
}
func TestAdv_1_WholeMessageRejection(t *testing.T) {
	reject(t, `SELECT ';'; RESET/*x*/ALL; SELECT 51`, `VALUES (7); CREATE LOCAL TEMPORARY TABLE adv_hidden(a int)`, `SELECT $adv$; LISTEN false_alarm$adv$; LISTEN "actual"`)
}
func TestAdv_1_EscapeContinuation(t *testing.T) {
	reject(t, "SELECT E'ok'\n'\\'; SET application_name = 'adv_escape'; --'", "SELECT E'ok'\n'\\'; LISTEN adv_escape; --'")
}
func noProof(t *testing.T, qs ...string) {
	t.Helper()
	begin(t)
	for _, q := range qs {
		u := p.ClassifyQuery([]byte(q), false)
		if u.ReadOnly || u.Coalescable {
			t.Errorf("unsafe proof for %q: %+v", q, u)
		}
	}
}
func TestAdv_2_UnknownCallsInGrammar(t *testing.T) {
	noProof(t, `SELECT * FROM LATERAL unknown_adversary(8) AS x`, `WITH z(a) AS (VALUES (8)) SELECT unknown_adversary(a) FROM z`, `SELECT CASE WHEN true THEN unknown_adversary(8) ELSE 0 END`)
}
func TestAdv_2_NestedWriteAndLock(t *testing.T) {
	noProof(t, `WITH x AS (MERGE INTO dst USING src ON false WHEN NOT MATCHED THEN INSERT VALUES (1) RETURNING *) SELECT * FROM x`, `SELECT * FROM (SELECT * FROM pg_class FOR KEY SHARE) AS x`, `SELECT * FROM (SELECT pg_advisory_xact_lock(97)) AS x`)
}
func TestAdv_2_TimeDependentLiterals(t *testing.T) {
	begin(t)
	if os.Getenv("MG_TIER_R") == "1" {
		for _, oracle := range []string{
			"SELECT 1 / (('now'::timestamptz = transaction_timestamp())::int)",
			"SELECT 1 / ((CAST('today' AS date) = current_date)::int)",
		} {
			status, errs := realQuery(t, oracle)
			if status != 'I' || len(errs) > 0 {
				t.Fatalf("time literal oracle failed: %q status=%c errors=%v", oracle, status, errs)
			}
			t.Logf("Postgres confirms literal resolves to current transaction time/date: %s", oracle)
		}
	}
	for _, q := range []string{`SELECT 'now'::timestamp`, `SELECT CAST('today' AS date)`, `SELECT 'now'::timestamptz`} {
		u := p.ClassifyQuery([]byte(q), false)
		if u.Coalescable {
			t.Errorf("per-call time literal is coalescable: %q %+v", q, u)
		}
	}
}
func bad(t *testing.T, qs ...string) {
	t.Helper()
	begin(t)
	for _, q := range qs {
		_, clean := p.LexAll([]byte(q))
		if clean {
			t.Fatalf("attack must be untokenisable: %q", q)
		}
		for _, tx := range []bool{false, true} {
			u := p.ClassifyQuery([]byte(q), tx)
			if !u.Class.Forwards() || u.ReadOnly || u.Coalescable || u.HasKey {
				t.Errorf("claim 3 expects opaque forward tx=%v %q; got %+v", tx, q, u)
			}
		}
	}
}
func TestAdv_3_TruncatedRead(t *testing.T) {
	bad(t, `SELECT /* mg:share */ $abc$unclosed`, `SELECT E'backslash\`, `SELECT "unfinished`)
}
func TestAdv_3_RejectBeforeLexError(t *testing.T) {
	bad(t, `DISCARD ALL; SELECT 'unfinished`, `LISTEN adv; /* unfinished`, `RESET ALL; SELECT $tag$unfinished`)
}
func TestAdv_3_BadByteBetweenStatements(t *testing.T) {
	bad(t, "SELECT 82;\x00SELECT 83", "SELECT 82;\x01SELECT 83", "SELECT 82;\\SELECT 83")
}
func invariantPadding(t *testing.T, words []string) {
	t.Helper()
	begin(t)
	base := p.ClassifyQuery([]byte(strings.Join(words, " ")), false)
	r := rand.New(rand.NewSource(seed))
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
			t.Fatalf("iteration=%d %q base=%+v got=%+v", n, b.String(), base, got)
		}
	}
}
func TestAdv_4_TransactionPadding(t *testing.T) {
	invariantPadding(t, []string{"begin", "work", ";", "select", "19", ";", "rollback", "work", "and", "no", "chain"})
}
func TestAdv_4_LockPadding(t *testing.T) {
	invariantPadding(t, []string{"select", "*", "from", "pg_class", "for", "no", "key", "update"})
}
func TestAdv_4_FunctionPadding(t *testing.T) {
	invariantPadding(t, []string{"select", "pg_catalog", ".", "pg_advisory_lock", "(", "991", ")"})
}
func exerciseBytes(t *testing.T, b []byte) {
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
}
func TestAdv_5_AllBytePairs(t *testing.T) {
	begin(t)
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			exerciseBytes(t, []byte{byte(a), byte(b)})
		}
	}
}
func TestAdv_5_SeededHostileBytes(t *testing.T) {
	begin(t)
	count := 3000
	if os.Getenv("ADV_LONG") == "1" {
		count = 100000
	}
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < count; i++ {
		b := make([]byte, r.Intn(512))
		r.Read(b)
		exerciseBytes(t, b)
	}
}
func TestAdv_5_AllocationCeiling(t *testing.T) {
	begin(t)
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
func frames(t *testing.T, b []byte) [][]byte {
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
func TestAdv_7_AllRowsWithPrefixAndStatus(t *testing.T) {
	begin(t)
	var builder p.ReplyBuilder
	var count int64
	for id := p.ErrRowID(0); id < p.ErrRowCount; id++ {
		row := p.ErrorRows[id]
		for _, status := range []byte{'I', 'T', 'E'} {
			prefix := []byte{9, 8, 7}
			b, a, e := builder.Build(prefix, id, status)
			count++
			if row.Reply == p.ErrorRowPassThrough {
				if e == nil {
					t.Error("pass-through produced local message")
				}
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(b[:3], prefix) {
				t.Fatal("prefix overwritten")
			}
			fs := frames(t, b[3:])
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
func TestAdv_7_InvalidInputsDoNotPoisonReuse(t *testing.T) {
	begin(t)
	var b p.ReplyBuilder
	for i := 0; i < 256; i++ {
		out, _, e := b.Build([]byte{4}, p.ErrRowID(255), byte(i))
		if e == nil || !bytes.Equal(out, []byte{4}) {
			t.Fatal("invalid row emitted reply")
		}
	}
	out, _, e := b.Build(nil, p.ErrRowShed, 'E')
	if e != nil || len(frames(t, out)) != 2 || b.Snapshot().ByRow[p.ErrRowShed] != 1 {
		t.Fatal("builder failed reuse")
	}
}
func TestAdv_7_DiscardAllBytesAndRecover(t *testing.T) {
	begin(t)
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
func TestAdv_8_ErrorDocumentExact(t *testing.T) {
	begin(t)
	b, e := os.ReadFile(root + "/docs/error-contract.md")
	if e != nil {
		t.Fatal(e)
	}
	if string(b) != p.ErrorRowsMarkdown() {
		t.Fatal("error doc drift")
	}
}
func TestAdv_8_RejectDocumentExact(t *testing.T) {
	begin(t)
	b, e := os.ReadFile(root + "/docs/reject-list.md")
	if e != nil {
		t.Fatal(e)
	}
	if string(b) != p.RejectListMarkdown() {
		t.Fatal("reject doc drift")
	}
}
func TestAdv_8_GeneratorsTrackTableMutation(t *testing.T) {
	begin(t)
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
	allow := p.AllowList[0]
	defer func() { p.AllowList[0] = allow }()
	p.AllowList[0].Name = "adversary_allow_sentinel"
	if !strings.Contains(p.RejectListMarkdown(), "adversary_allow_sentinel") {
		t.Fatal("allow-list generator ignores code")
	}
}
func TestAdv_9_ImportClosureIsStandard(t *testing.T) {
	begin(t)
	cmd := exec.Command("go", "list", "-deps", "-json", "./internal/work")
	cmd.Dir = root
	b, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	for d.More() {
		var v struct {
			ImportPath string
			Standard   bool
		}
		if e = d.Decode(&v); e != nil {
			t.Fatal(e)
		}
		if !v.Standard && v.ImportPath != "github.com/nickstrad/mini-gateway/internal/work" {
			t.Fatalf("nonstandard dependency %s", v.ImportPath)
		}
	}
}
func TestAdv_9_OutputTypeClosure(t *testing.T) {
	begin(t)
	var f func([]byte, bool) work.Unit = p.ClassifyQuery
	_ = f
	var seen = map[reflect.Type]bool{}
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
func TestAdv_9_SourceHasNoProtocolImports(t *testing.T) {
	begin(t)
	files, e := filepath.Glob(root + "/internal/work/*.go")
	if e != nil || len(files) == 0 {
		t.Fatal("missing work sources")
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, e := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if i, ok := n.(*ast.ImportSpec); ok {
				path, _ := strconv.Unquote(i.Path.Value)
				if strings.Contains(path, ".") {
					t.Errorf("foreign import %s", path)
				}
			}
			return true
		})
	}
}
func TestAdv_2_ShareKeyOwnershipAndScope(t *testing.T) {
	begin(t)
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

func TestAdv_4_PostgresUnaryOperatorBoundary(t *testing.T) {
	begin(t)
	a, b := "SELECT 71+-3", "SELECT 71+ -3"
	if os.Getenv("MG_TIER_R") == "1" {
		for _, q := range []string{a, b} {
			status, errs := realQuery(t, q)
			if status != 'I' || len(errs) > 0 {
				t.Fatalf("invalid SQL oracle %q %c %v", q, status, errs)
			}
		}
	}
	x, y := p.ClassifyQuery([]byte(a), false), p.ClassifyQuery([]byte(b), false)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("whitespace between PostgreSQL + and unary - tokens changes classification: %q %+v versus %q %+v", a, x, b, y)
	}
}
