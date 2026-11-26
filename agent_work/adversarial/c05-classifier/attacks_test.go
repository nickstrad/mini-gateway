package adv_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const seed int64 = 0xC05A26

func mark(t *testing.T) { t.Helper(); t.Logf("seed=%d", seed) }
func root() string      { r, _ := filepath.Abs("../../.."); return r }
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root(), name))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func command(t *testing.T, name string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, c := context.WithTimeout(context.Background(), 45*time.Second)
	defer c()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = root()
	return cmd.CombinedOutput()
}
func unsafe(t *testing.T, q string) {
	t.Helper()
	for _, tx := range []bool{false, true} {
		u := p.ClassifyQuery([]byte(q), tx)
		if u.SafeRead || u.Shareable {
			t.Errorf("unsafe text %q tx=%v classified %+v", q, tx, u)
		}
	}
}

func TestAdv_1_QuoteStateCrossProducts(t *testing.T) {
	mark(t)
	for _, prefix := range []string{"E", "b", "X", "n", "abc", "7", "_"} {
		for _, body := range []string{"abc", "a''b", "", "NOW", "yesterday"} {
			unsafe(t, "SELECT "+prefix+"'"+body+"'")
		}
	}
	for _, body := range []string{"tomorrow", "today", "sNOWball", "yesterday", "x''today", "now''x"} {
		unsafe(t, "SELECT '"+body+"'")
	}
	for _, q := range []string{"SELECT 'abc", "SELECT abs('x') '"} {
		unsafe(t, q)
	}
}
func TestAdv_1_StopWordsAtTokenEdges(t *testing.T) {
	mark(t)
	words := strings.Fields("into for with tablesample insert update delete merge truncate create alter drop grant revoke call do copy lock set reset show prepare execute deallocate listen notify unlisten declare fetch move close begin start commit rollback abort savepoint release discard load vacuum analyze explain refresh reindex cluster checkpoint comment import security values table current_date current_time current_timestamp localtime localtimestamp current_role current_user session_user system_user user current_catalog current_schema")
	for _, w := range words {
		for _, edge := range []string{"SELECT (%s)", "SELECT 1,%s", "SELECT '%s',%s"} {
			q := fmt.Sprintf(edge, w)
			if strings.Count(edge, "%s") == 2 {
				q = fmt.Sprintf(edge, w, w)
			}
			unsafe(t, q)
		}
	}
	for _, q := range []string{"SELECT random ()", "SELECT 1 WHERE exists (SELECT set_config ('x','y',false))", "SELECT count(*) FROM kv WHERE k = evil (1)"} {
		unsafe(t, q)
	}
}
func TestAdv_1_LengthAndFlagBoundary(t *testing.T) {
	mark(t)
	for _, n := range []int{0, 1, 4095, 4096, 4097, 8192} {
		q := []byte(strings.Repeat(" ", n))
		if n >= 8 {
			copy(q, "SELECT 7")
		}
		for _, tx := range []bool{false, true} {
			u := p.ClassifyQuery(q, tx)
			want := n >= 8 && n <= 4096
			if u.SafeRead != want || u.Shareable != (want && !tx) {
				t.Fatalf("n=%d tx=%v got %+v", n, tx, u)
			}
		}
	}
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < 2000; i++ {
		q := []byte("SELECT 'abc', max(7)")
		at := r.Intn(len(q) + 1)
		b := byte(r.Intn(256))
		if strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _,*=<>()'", rune(b)) {
			continue
		}
		q = append(append(append([]byte{}, q[:at]...), b), q[at:]...)
		unsafe(t, string(q))
	}
}
func TestAdv_3_TransitiveRecognizerClosure(t *testing.T) {
	mark(t)
	f, e := parser.ParseFile(token.NewFileSet(), "share.go", read(t, "internal/pgwire/share.go"), 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(f.Imports) != 0 {
		t.Fatal("recognizer imports another package")
	}
	declarations := map[string]bool{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			declarations[d.Name.Name] = true
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if v, ok := s.(*ast.ValueSpec); ok {
					for _, n := range v.Names {
						declarations[n.Name] = true
					}
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := c.Fun.(*ast.Ident)
		if !ok {
			t.Errorf("nonlocal call %T", c.Fun)
		} else if !declarations[id.Name] && !strings.Contains(" len byte ", " "+id.Name+" ") {
			t.Errorf("external recognizer dependency %s", id.Name)
		}
		return true
	})
}
func TestAdv_3_LexerHostilityCannotWiden(t *testing.T) {
	mark(t)
	for _, q := range []string{"SELECT 7 --\n; DROP TABLE kv", "SELECT $$safe$$", "SELECT E'abc'", "SELECT \x00 7", "SELECT 1 /*"} {
		u := p.ClassifyQuery([]byte(q), false)
		if u.SafeRead || u.Shareable {
			t.Errorf("lexer-hostile %q became safe %+v", q, u)
		}
	}
}
func TestAdv_3_GatesUseRawArgument(t *testing.T) {
	mark(t)
	f, e := parser.ParseFile(token.NewFileSet(), "classify.go", read(t, "internal/pgwire/classify.go"), 0)
	if e != nil {
		t.Fatal(e)
	}
	found := 0
	ast.Inspect(f, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, l := range a.Lhs {
			s, ok := l.(*ast.SelectorExpr)
			owner, isUnit := func() (*ast.Ident, bool) {
				if !ok {
					return nil, false
				}
				id, good := s.X.(*ast.Ident)
				return id, good
			}()
			if !ok || s.Sel.Name != "SafeRead" || !isUnit || owner.Name != "u" {
				continue
			}
			found++
			c, ok := a.Rhs[i].(*ast.CallExpr)
			if !ok || len(c.Args) != 1 {
				t.Fatal("SafeRead is not a unary call")
			}
			fn, ok := c.Fun.(*ast.Ident)
			if !ok || fn.Name != "ShareFits" {
				t.Fatal("SafeRead bypasses recognizer")
			}
			arg, ok := c.Args[0].(*ast.Ident)
			if !ok || arg.Name != "text" {
				t.Fatal("SafeRead uses transformed lexer input")
			}
		}
		return true
	})
	if found != 1 {
		t.Fatalf("SafeRead assignments=%d", found)
	}
	for _, q := range []string{"SELECT 9", "SELECT abs(9)", "SELECT 9 FROM pg_temp"} {
		for _, tx := range []bool{false, true} {
			u := p.ClassifyQuery([]byte(q), tx)
			if u.Shareable != (p.ShareFits([]byte(q)) && !tx && u.Class != work.ClassReject) {
				t.Fatalf("gate mismatch %+v", u)
			}
		}
	}
}
func TestAdv_4_EveryHintByteMutation(t *testing.T) {
	mark(t)
	hint := []byte("/* mg:share */")
	for i := range hint {
		for b := 0; b < 256; b++ {
			if byte(b) == hint[i] {
				continue
			}
			q := append([]byte(nil), hint...)
			q[i] = byte(b)
			q = append(q, []byte(" SELECT 9")...)
			u := p.ClassifyQuery(q, false)
			if u.Hinted {
				t.Fatalf("mutation %d/%d still hinted", i, b)
			}
		}
	}
}
func TestAdv_4_HintOffsetAndTruncation(t *testing.T) {
	mark(t)
	h := "/* mg:share */"
	for i := 0; i < len(h); i++ {
		if p.ClassifyQuery([]byte(h[:i]), false).Hinted {
			t.Fatalf("prefix length %d hinted", i)
		}
	}
	for _, pre := range []string{" ", "\xef\xbb\xbf", "\x00", "/* x */", "SELECT '"} {
		if p.ClassifyQuery([]byte(pre+h), false).Hinted {
			t.Fatalf("hint recognized at offset %d", len(pre))
		}
	}
}
func TestAdv_4_HintedOpaqueAndRejectionKeyIsolation(t *testing.T) {
	mark(t)
	c := p.Classifier{Config: p.ClassifierConfig{KeyScope: work.ScopeTenant}}
	for _, tail := range []string{" SELECT 5", " SELECT '", " RESET ALL"} {
		q := []byte("/* mg:share */" + tail)
		for _, tx := range []bool{false, true} {
			u := c.Classify("alice", q, tx)
			wantKey := !tx && u.Class != work.ClassReject
			if !u.Hinted || u.SafeRead || u.Shareable || u.HasKey != wantKey {
				t.Errorf("tail %q tx=%v %+v", tail, tx, u)
			}
			if u.HasKey {
				copy(q[len(q)-1:], "x")
				if bytes.Equal(q, u.Key.Text) || u.Key.Tenant != "alice" {
					t.Fatal("key aliases input or lost tenant")
				}
				q = []byte("/* mg:share */" + tail)
			}
		}
	}
}

func duration(q []byte, n int) time.Duration {
	start := time.Now()
	for i := 0; i < n; i++ {
		p.ClassifyQuery(q, false)
	}
	return time.Since(start)
}
func scaling(t *testing.T, makeQ func(int) []byte) {
	t.Helper()
	mark(t)
	small, large := makeQ(512), makeQ(8192)
	const reps = 2
	a, b := time.Duration(1<<63-1), time.Duration(1<<63-1)
	for i := 0; i < 3; i++ {
		a = min(a, duration(small, reps))
		b = min(b, duration(large, reps))
	}
	t.Logf("input=%d/%d duration=%v/%v ratio=%.1f", len(small), len(large), a, b, float64(b)/float64(a))
	if b > 96*a {
		t.Fatalf("16x input took %.1fx time; stated linear ceiling is 96x", float64(b)/float64(a))
	}
}
func TestAdv_5_OperatorSuffixRescanning(t *testing.T) {
	scaling(t, func(n int) []byte { return []byte("SELECT " + strings.Repeat("+", n)) })
}
func TestAdv_5_WholeSpanRescanning(t *testing.T) {
	scaling(t, func(n int) []byte { return []byte("SELECT " + strings.Repeat("pg_settings ", n)) })
}
func TestAdv_5_BinaryAndMemoryCampaign(t *testing.T) {
	mark(t)
	r := rand.New(rand.NewSource(seed))
	count := 10000
	if os.Getenv("ADV_LONG") == "1" {
		count = 150000
	}
	for i := 0; i < count; i++ {
		q := make([]byte, r.Intn(512))
		r.Read(q)
		l := p.LexerFor(q)
		end := 0
		for {
			tok, ok := l.Next()
			if !ok {
				break
			}
			if tok.Start != end || tok.End <= end || tok.End > len(q) {
				t.Fatalf("iteration=%d broken span %+v", i, tok)
			}
			end = tok.End
		}
		if end != len(q) {
			t.Fatalf("input not covered %d/%d", end, len(q))
		}
		p.ClassifyQuery(q, i%2 == 0)
		p.ShareFits(q)
	}
	for _, q := range [][]byte{[]byte(strings.Repeat("'x' ", 1<<15)), []byte(strings.Repeat("(1),", 1<<15)), []byte(strings.Repeat(";", 1<<17))} {
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		p.ClassifyQuery(q, false)
		runtime.ReadMemStats(&b)
		bound := uint64(80*len(q) + 65536)
		if b.TotalAlloc-a.TotalAlloc > bound {
			t.Errorf("allocated %d > %d for n=%d", b.TotalAlloc-a.TotalAlloc, bound, len(q))
		}
	}
}
func TestAdv_6_OpaqueWithoutKnownFeature(t *testing.T) {
	mark(t)
	for _, q := range []string{"\x00", "SELECT '\\", "$unfinished$xxx", "/* unterminated", "SELECT \x01 29"} {
		for _, tx := range []bool{false, true} {
			u := p.ClassifyQuery([]byte(q), tx)
			if !u.Class.Forwards() || !u.Opaque {
				t.Errorf("unknown %q tx=%v got %+v", q, tx, u)
			}
		}
	}
}
func TestAdv_6_UnknownFutureGrammar(t *testing.T) {
	mark(t)
	for _, q := range []string{"FUTURE COMMAND 7", "SELECT mysterious_2029(42)", "SELECT * FROM absent_object", "SELECT 7 QUALIFY other()"} {
		u := p.ClassifyQuery([]byte(q), false)
		if !u.Class.Forwards() {
			t.Errorf("unknown %q rejected %+v", q, u)
		}
	}
}
func TestAdv_6_UnreadableLocalArguments(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 6 narrowed to its precise form: a recognised reject-list feature with an unreadable argument is refused (RECONCILIATION.md R5). See agent_work/chunks/c05-classifier/ADVERSARIAL.md.")
	mark(t)
	for _, q := range []string{"SELECT set_config(lower('application_name'), 'x', true)", "SELECT set_config('search_path', lower('public'), true)"} {
		u := p.ClassifyQuery([]byte(q), false)
		if !u.Class.Forwards() {
			t.Errorf("unknown argument in transaction-local call %q rejected %+v; literal claim 6 says unmodelled constructs forward", q, u)
		}
	}
}

func examples(t *testing.T) []string {
	var out []string
	for _, line := range strings.Split(string(read(t, "docs/reject-list.md")), "\n") {
		if strings.HasPrefix(line, "## Allowed") {
			break
		}
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		start := strings.LastIndex(line, " | `")
		if start < 0 {
			continue
		}
		q := strings.TrimSuffix(line[start+4:], "` |")
		out = append(out, strings.ReplaceAll(q, "\\|", "|"))
	}
	if len(out) < 15 {
		t.Fatalf("only %d documented examples", len(out))
	}
	return out
}
func TestAdv_7_DocumentedExamplesNestedPadding(t *testing.T) {
	mark(t)
	for _, q := range examples(t) {
		for _, pad := range []string{"\v\r\n", "/* adversary /* nested ; SELECT */ */", "-- ignored ; COMMIT\r\n"} {
			u := p.ClassifyQuery([]byte(pad+q+pad), false)
			if u.Class != work.ClassReject {
				t.Errorf("documented %q with %q forwarded", q, pad)
			}
		}
	}
}
func TestAdv_7_CorpusRejectInMessageMiddle(t *testing.T) {
	mark(t)
	n := 0
	for _, line := range strings.Split(string(read(t, "internal/pgwire/testdata/classifier-corpus.txt")), "\n") {
		if !strings.HasPrefix(line, "R:") {
			continue
		}
		_, q, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatal(line)
		}
		if strings.HasPrefix(q, "\"") {
			var e error
			q, e = strconv.Unquote(q)
			if e != nil {
				t.Fatal(e)
			}
		}
		n++
		u := p.ClassifyQuery([]byte("SELECT 291; "+q+"\n; SELECT 592"), false)
		if u.Class != work.ClassReject {
			t.Errorf("reject record forwarded when embedded: %q", q)
		}
	}
	if n == 0 {
		t.Fatal("no rejected records")
	}
}
func TestAdv_7_KeywordCaseWithoutTouchingLiterals(t *testing.T) {
	mark(t)
	for _, q := range examples(t) {
		var out strings.Builder
		quote := false
		for i, b := range []byte(q) {
			if b == '\'' {
				quote = !quote
			}
			if !quote && i%2 == 0 && b >= 'A' && b <= 'Z' {
				b += 32
			}
			out.WriteByte(b)
		}
		if p.ClassifyQuery([]byte(out.String()), true).Class != work.ClassReject {
			t.Errorf("case variant forwarded %q", out.String())
		}
	}
}

func decode(t *testing.T, b []byte) []map[byte]string {
	t.Helper()
	var frames []map[byte]string
	for len(b) > 0 {
		if len(b) < 5 {
			t.Fatal("short frame")
		}
		n := int(binary.BigEndian.Uint32(b[1:5]))
		if n < 4 || n+1 > len(b) {
			t.Fatalf("bad length %d", n)
		}
		m := map[byte]string{0: string(b[0])}
		body := b[5 : n+1]
		if b[0] == 'E' {
			for len(body) > 1 {
				k := body[0]
				end := bytes.IndexByte(body[1:], 0)
				if end < 0 {
					t.Fatal("unterminated field")
				}
				if _, ok := m[k]; ok {
					t.Fatal("duplicate error field")
				}
				m[k] = string(body[1 : 1+end])
				body = body[end+2:]
			}
			if !bytes.Equal(body, []byte{0}) {
				t.Fatal("missing error terminator")
			}
		} else {
			m[1] = string(body)
		}
		frames = append(frames, m)
		b = b[n+1:]
	}
	return frames
}
func TestAdv_8_ErrorRowsRandomOrderAccounting(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: this test pinned refused reply builds as counted in Total and ByRow, which the repair removed as a defect found by the Neon pass (RECONCILIATION.md R7). See agent_work/chunks/c05-classifier/ADVERSARIAL.md.")
	mark(t)
	var b p.ReplyBuilder
	r := rand.New(rand.NewSource(seed))
	expected := [p.ErrRowCount]int64{}
	for round := 0; round < 9; round++ {
		for _, i := range r.Perm(int(p.ErrRowCount)) {
			id := p.ErrRowID(i)
			row := p.ErrorRows[id]
			out, act, e := b.Build(nil, id, "ITE"[round%3])
			expected[id]++
			if row.Reply == p.ErrorRowPassThrough {
				if e == nil {
					t.Fatal("fabricated pass-through")
				}
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			f := decode(t, out)
			if row.HasMessage() {
				if len(f) < 1 || f[0][0] != "E" || f[0]['C'] != row.Code || f[0]['S'] != row.Severity() {
					t.Fatalf("row %s frames=%v", row.ID, f)
				}
				if row.Reply == p.ErrorRowError {
					if len(f) != 2 || f[1][0] != "Z" || f[1][1] != string("ITE"[round%3]) {
						t.Fatalf("bad Ready row=%s %v", row.ID, f)
					}
				} else if len(f) != 1 {
					t.Fatal("unexpected end marker")
				}
			} else if len(out) != 0 || !act.Close {
				t.Fatal("close-only sent bytes")
			}
		}
	}
	s := b.Snapshot()
	if s.ByRow != expected || s.Total != 9*int64(p.ErrRowCount) || s.Failed != 9 {
		t.Fatalf("counters %+v", s)
	}
}
func TestAdv_8_BatchedAppendAndReadyIsolation(t *testing.T) {
	mark(t)
	var b p.ReplyBuilder
	var out []byte
	for i := 0; i < 200; i++ {
		var e error
		out, _, e = b.Build(out, p.ErrRowDraining, 'T')
		if e != nil {
			t.Fatal(e)
		}
	}
	f := decode(t, out)
	if len(f) != 400 {
		t.Fatalf("frames=%d", len(f))
	}
	for i := 0; i < 200; i++ {
		if f[i*2]['C'] != "53M05" || f[i*2+1][1] != "T" {
			t.Fatal("batch frame corruption")
		}
	}
	before := append([]byte(nil), out...)
	after, e := p.ReplyReady(out, 0)
	if e == nil || !bytes.Equal(before, after) {
		t.Fatal("invalid Ready changed existing stream")
	}
}
func TestAdv_8_SyncDiscardTerminationAndReuse(t *testing.T) {
	mark(t)
	var s p.SyncDiscard
	for round := 0; round < 100; round++ {
		if s.Step('B') != p.SyncDiscardRefuse {
			t.Fatal("Bind not refused")
		}
		if s.Step('Q') != p.SyncDiscardDrop {
			t.Fatal("query leaked during discard")
		}
		if s.Step('X') != p.SyncDiscardPass || s.Active() {
			t.Fatal("Terminate did not release discard")
		}
		if s.Step('Q') != p.SyncDiscardPass {
			t.Fatal("reuse poisoned")
		}
		if s.Step('S') != p.SyncDiscardRefuseAndReady {
			t.Fatal("bare Sync missing Ready")
		}
	}
	x := s.Snapshot()
	if x.Refused != 200 || x.Discarded != 100 || x.Synced != 0 {
		t.Fatalf("accounting %+v", x)
	}
}

func TestAdv_9_ErrorDocumentAgreesWithWire(t *testing.T) {
	mark(t)
	doc := string(read(t, "docs/error-contract.md"))
	var b p.ReplyBuilder
	for id := p.ErrRowID(0); id < p.ErrRowCount; id++ {
		r := p.ErrorRows[id]
		var line string
		for _, l := range strings.Split(doc, "\n") {
			if strings.Contains(l, "`"+r.ID+"`") {
				line = l
				break
			}
		}
		if line == "" {
			t.Errorf("missing row %s", r.ID)
			continue
		}
		out, _, _ := b.Build(nil, id, 'I')
		if r.HasMessage() {
			f := decode(t, out)
			if !strings.Contains(line, f[0]['C']) {
				t.Errorf("wire code %s missing from %s", f[0]['C'], line)
			}
		}
	}
	if doc != p.ErrorRowsMarkdown() {
		t.Fatal("error document bytes stale")
	}
}
func TestAdv_9_RejectDocumentTracksLiveRules(t *testing.T) {
	mark(t)
	doc := string(read(t, "docs/reject-list.md"))
	if doc != p.RejectListMarkdown() {
		t.Fatal("reject document stale")
	}
	old := p.RejectRules[0]
	defer func() { p.RejectRules[0] = old }()
	p.RejectRules[0].Example = "ADV_DOCUMENT_CANARY"
	if !strings.Contains(p.RejectListMarkdown(), "ADV_DOCUMENT_CANARY") {
		t.Fatal("generator ignores live rule example")
	}
	if strings.Contains(doc, "ADV_DOCUMENT_CANARY") {
		t.Fatal("canary collision")
	}
}
func TestAdv_9_ShapeDocumentTablesAndLimits(t *testing.T) {
	mark(t)
	doc := string(read(t, "docs/safe-shape.md"))
	if doc != p.SafeShapeMarkdown() {
		t.Fatal("shape document stale")
	}
	for _, list := range [][]string{p.ShareStopWords, p.ShareParenKeywords, p.ShareAllowList, p.ShareTimeWords} {
		for _, w := range list {
			if !strings.Contains(doc, "`"+w+"`") {
				t.Errorf("missing table word %q", w)
			}
		}
	}
	if !strings.Contains(doc, strconv.Itoa(p.ShareMaxLen)) {
		t.Fatal("missing length bound")
	}
	old := p.ShareTimeWords
	defer func() { p.ShareTimeWords = old }()
	p.ShareTimeWords = append(append([]string{}, old...), "adversary")
	if p.ShareFits([]byte("SELECT 'adversary'")) || !strings.Contains(p.SafeShapeMarkdown(), "`adversary`") {
		t.Fatal("behavior and documented time list disconnected")
	}
}

func TestAdv_10_ImportGraphUsesGoStandardFlag(t *testing.T) {
	mark(t)
	out, e := command(t, "go", "list", "-deps", "-json", "./internal/work")
	if e != nil {
		t.Fatalf("%v %s", e, out)
	}
	d := json.NewDecoder(bytes.NewReader(out))
	for d.More() {
		var x struct {
			ImportPath string
			Standard   bool
		}
		if e := d.Decode(&x); e != nil {
			t.Fatal(e)
		}
		if !x.Standard && x.ImportPath != "github.com/nickstrad/mini-gateway/internal/work" {
			t.Errorf("nonstandard dependency %s", x.ImportPath)
		}
	}
}
func TestAdv_10_PublicTypeClosure(t *testing.T) {
	mark(t)
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(x reflect.Type) {
		if seen[x] {
			return
		}
		seen[x] = true
		if x.PkgPath() != "" && x.PkgPath() != "github.com/nickstrad/mini-gateway/internal/work" {
			t.Errorf("foreign type %v (%s)", x, x.PkgPath())
		}
		switch x.Kind() {
		case reflect.Struct:
			for i := 0; i < x.NumField(); i++ {
				walk(x.Field(i).Type)
			}
		case reflect.Array, reflect.Slice, reflect.Pointer:
			walk(x.Elem())
		case reflect.Map:
			walk(x.Key())
			walk(x.Elem())
		}
	}
	for _, x := range []any{work.Unit{}, work.Outcome{}, work.ShareKey{}} {
		walk(reflect.TypeOf(x))
	}
	f := reflect.TypeOf(p.ClassifyQuery)
	if f.NumOut() != 1 || f.Out(0) != reflect.TypeOf(work.Unit{}) {
		t.Fatal("classifier output escaped work.Unit")
	}
}
func TestAdv_10_AllProductionFilesProtocolFree(t *testing.T) {
	mark(t)
	files, e := filepath.Glob(filepath.Join(root(), "internal/work/*.go"))
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		n++
		f, e := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			out, e := command(t, "go", "list", "-f", "{{.Standard}}", path)
			if e != nil || strings.TrimSpace(string(out)) != "true" {
				t.Errorf("production import %q is nonstandard", path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				s := strings.ToLower(id.Name)
				if strings.Contains(s, "postgres") || strings.Contains(s, "pgproto") {
					t.Errorf("protocol identifier %s", id.Name)
				}
			}
			return true
		})
	}
	if n == 0 {
		t.Fatal("no production files inspected")
	}
}

// This checks the ledger against both the old tree and live test declarations;
// semantic review of the assertions is recorded separately in RESULTS.md.
func TestAdv_9_TestLedgerHasLiveDestinations(t *testing.T) {
	mark(t)
	notes := string(read(t, "agent_work/chunks/c05-classifier/NOTES.md"))
	files, _ := filepath.Glob(filepath.Join(root(), "internal/pgwire/*_test.go"))
	wf, _ := filepath.Glob(filepath.Join(root(), "internal/work/*_test.go"))
	files = append(files, wf...)
	cf, _ := filepath.Glob(filepath.Join(root(), "internal/obs/clssrc/*_test.go"))
	files = append(files, cf...)
	live := map[string]bool{}
	for _, name := range files {
		f, e := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil && len(fn.Body.List) > 0 {
				live[fn.Name.Name] = true
			}
		}
	}
	re := regexp.MustCompile("`((?:Test|Fuzz)[A-Za-z0-9_]+)`")
	ledger := strings.Split(strings.Split(notes, "## Test ledger")[1], "## Product size")[0]
	var missing []string
	previous := ""
	for _, line := range strings.Split(ledger, "\n") {
		if !strings.HasPrefix(line, "| `") || strings.Contains(line, "| retired") {
			continue
		}
		if strings.Contains(line, "| same | as above |") {
			line += previous
		}
		previous = line
		names := re.FindAllStringSubmatch(line, -1)
		found := false
		for _, n := range names {
			found = found || live[n[1]]
		}
		if !found { // Hoisted rows sometimes put the destination name after a colon inside the backticks.
			for name := range live {
				if strings.Contains(line, name) {
					found = true
					break
				}
			}
		}
		if !found {
			missing = append(missing, line)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no live test destination: %s", m)
	}
}

func TestAdv_9_FirstBuildCorpusWasNotRetired(t *testing.T) {
	mark(t)
	old, e := command(t, "git", "show", "d39ecfb:internal/pgwire/testdata/classifier-corpus.txt")
	if e != nil {
		t.Fatalf("old corpus unavailable: %v %s", e, old)
	}
	parse := func(raw []byte) map[string]string {
		m := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			flags, q, ok := strings.Cut(line, "\t")
			if !ok {
				t.Fatalf("bad corpus row %q", line)
			}
			m[q] = flags
		}
		return m
	}
	before, after := parse(old), parse(read(t, "internal/pgwire/testdata/classifier-corpus.txt"))
	if len(before) != 562 {
		t.Fatalf("unexpected old corpus size %d", len(before))
	}
	for q, flags := range before {
		now, ok := after[q]
		if !ok {
			t.Errorf("old input retired: %q", q)
			continue
		}
		if strings.HasPrefix(flags, "R:") && strings.Fields(flags)[0] != strings.Fields(now)[0] {
			t.Errorf("reject expectation changed: %q: %s -> %s", q, flags, now)
		}
		text := q
		if strings.HasPrefix(text, "\"") {
			text, e = strconv.Unquote(text)
			if e != nil {
				t.Fatal(e)
			}
		}
		if !strings.Contains(" "+flags+" ", " co ") {
			u := p.ClassifyQuery([]byte(text), false)
			if u.SafeRead || u.Shareable {
				t.Errorf("old never-coalescable input widened: %q %+v", text, u)
			}
		}
	}
}
