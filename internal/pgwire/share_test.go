package pgwire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	shrSeed          = 0x5afe
	shrRandomTexts   = 200000
	shrGrammarTexts  = 5000
	shrRefAlphabet   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_,*=<>()' "
	shrRefWordBytes  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	shrLongestUnique = ShareMaxLen
)

var shrFitting = []string{
	"SELECT 1",
	"select 1",
	"SeLeCt 1",
	"  SELECT 1",
	"SELECT v FROM kv WHERE k = 'a'",
	"SELECT v FROM kv WHERE k = 'it''s'",
	"SELECT v FROM kv WHERE k = ''",
	"SELECT k, v FROM kv WHERE k <> 'x' AND v >= '1' ORDER BY k LIMIT 10",
	"SELECT count(*) FROM t",
	"SELECT count (*) FROM t",
	"SELECT a FROM t WHERE a IN (1, 2) OR NOT (a > 2)",
	"SELECT a FROM t WHERE EXISTS (SELECT 1 FROM u WHERE a = 1)",
	"SELECT coalesce(nullif(b, 'x'), greatest(a, 1), least(a, 2)) FROM t",
	"SELECT lower(b), upper(b), md5(b), length(b) FROM t GROUP BY b HAVING count(*) > 0",
	"SELECT CASE WHEN a > 1 THEN 'big' ELSE 'small' END FROM t",
	"SELECT a FROM t UNION SELECT a FROM u",
	"SELECT DISTINCT b FROM t",
	"SELECT * FROM t WHERE a BETWEEN 1 AND 3 LIMIT 1 OFFSET 1",
	"SELECT 'snaw', 'tod', 'Nawhere'' '",
	"SELECT x FROM (SELECT a AS x FROM t) AS s",
	"SELECT a FROM t WHERE a = ANY (SELECT a FROM u)",
	"SELECT " + strings.Repeat("a", ShareMaxLen-len("SELECT ")),
}

var shrNotFitting = []struct {
	rule string
	text string
}{
	{"1 length: empty", ""},
	{"1 length: one byte over the limit", "SELECT " + strings.Repeat("a", ShareMaxLen-len("SELECT ")+1)},
	{"2 alphabet: dot", "SELECT kv.v FROM kv"},
	{"2 alphabet: semicolon", "SELECT 1;"},
	{"2 alphabet: tab", "SELECT\t1"},
	{"2 alphabet: line break", "SELECT\n1"},
	{"2 alphabet: carriage return", "SELECT\r1"},
	{"2 alphabet: double quote", `SELECT "a" FROM t`},
	{"2 alphabet: backslash", `SELECT 'a\'`},
	{"2 alphabet: dollar", "SELECT $$a$$"},
	{"2 alphabet: minus", "SELECT 1 - 1"},
	{"2 alphabet: slash", "SELECT 1 / 1"},
	{"2 alphabet: comment", "SELECT 1 /* c */"},
	{"2 alphabet: colon", "SELECT a::text FROM t"},
	{"2 alphabet: plus", "SELECT 1 + 1"},
	{"2 alphabet: percent", "SELECT 'a%' FROM t"},
	{"2 alphabet: bracket", "SELECT a[1] FROM t"},
	{"2 alphabet: bar", "SELECT 'a' || 'b'"},
	{"2 alphabet: non-ASCII", "SELECT 'caf\xc3\xa9'"},
	{"2 alphabet: NUL", "SELECT 1\x00"},
	{"2 alphabet: hint", "/* mg:share */ SELECT 1"},
	{"3 quotes: ends inside a string", "SELECT 'a"},
	{"3 quotes: doubled quote leaves the string open", "SELECT 'a''"},
	{"4 prefix: E", "SELECT E'a'"},
	{"4 prefix: B", "SELECT B'01'"},
	{"4 prefix: X", "SELECT X'0f'"},
	{"4 prefix: N", "SELECT N'a'"},
	{"4 prefix: underscore", "SELECT _'a'"},
	{"4 prefix: digit", "SELECT 1'a'"},
	{"5 time: now", "SELECT a FROM t WHERE b < 'now'"},
	{"5 time: NOW in any case", "SELECT 'NoW'"},
	{"5 time: today", "SELECT 'today'"},
	{"5 time: tomorrow", "SELECT 'TOMORROW'"},
	{"5 time: yesterday", "SELECT 'yesterday 10'"},
	{"5 time: substring", "SELECT 'snow'"},
	{"5 time: across a doubled quote is not a match, but a later string is", "SELECT 'n''ow', 'now'"},
	{"6 lead: not select", "VALUES (1)"},
	{"6 lead: parenthesis first", "(SELECT 1)"},
	{"6 lead: string first", "'a' SELECT"},
	{"6 lead: selected", "SELECTED 1"},
	{"6 stop: into", "SELECT 1 INTO x"},
	{"6 stop: for", "SELECT a FROM t FOR SHARE"},
	{"6 stop: with", "SELECT 1 WITH x"},
	{"6 stop: tablesample", "SELECT a FROM t TABLESAMPLE BERNOULLI (1)"},
	{"6 stop: insert", "SELECT 1 insert"},
	{"6 stop: values", "SELECT * FROM (VALUES (1)) AS v"},
	{"6 stop: table", "SELECT a FROM table"},
	{"6 stop: current_timestamp", "SELECT current_timestamp"},
	{"6 stop: current_user", "SELECT a FROM t WHERE b = current_user"},
	{"6 stop: user", "SELECT user"},
	{"6 stop: localtime", "SELECT LOCALTIME"},
	{"6 stop: current_schema", "SELECT current_schema"},
	{"6 paren: unknown function", "SELECT nextval('s')"},
	{"6 paren: unknown function after spaces", "SELECT random  ()"},
	{"6 paren: now()", "SELECT now()"},
	{"6 paren: type name", "SELECT numeric(1)"},
	{"6 paren: by is not a paren keyword", "SELECT a FROM t GROUP BY (a)"},
	{"6 paren: join is not a paren keyword", "SELECT a FROM t JOIN (SELECT 1 AS z) AS s ON true"},
	{"6 paren: over is not a paren keyword", "SELECT count(*) OVER () FROM t"},
	{"6 paren: number before a parenthesis", "SELECT 1 (2)"},
	{"6 paren: cast", "SELECT CAST(a AS text) FROM t"},
	{"6 paren: advisory lock", "SELECT pg_advisory_lock(1)"},
	{"6 paren: set_config", "SELECT set_config('a', 'b', false)"},
}

// TestSafeShapeTable proves that every fitting example passes both ShareFits
// and the independent reference checker, and every single-rule violation fails both.
func TestSafeShapeTable(t *testing.T) {
	for _, q := range shrFitting {
		if !ShareFits([]byte(q)) {
			t.Errorf("%q does not fit, want it to fit", q)
		}
		if !shrReference([]byte(q)) {
			t.Errorf("the reference checker refuses %q", q)
		}
	}
	for _, c := range shrNotFitting {
		if ShareFits([]byte(c.text)) {
			t.Errorf("rule %s: %q fits", c.rule, c.text)
		}
		if shrReference([]byte(c.text)) {
			t.Errorf("rule %s: the reference checker accepts %q", c.rule, c.text)
		}
	}
}

// TestSafeShapeEveryStopWordAndUnlistedCallBreaksAFit proves that every stop
// word outside a string breaks a fit, every allow-listed call fits while near
// misses fail, and the tables are disjoint and sorted.
func TestSafeShapeEveryStopWordAndUnlistedCallBreaksAFit(t *testing.T) {
	for _, w := range ShareStopWords {
		for _, q := range []string{"SELECT " + w, "SELECT a FROM t WHERE " + strings.ToUpper(w) + " = 1", "SELECT 'x', " + w + " FROM t"} {
			if ShareFits([]byte(q)) {
				t.Errorf("stop word %q: %q fits", w, q)
			}
		}
		if !ShareFits([]byte("SELECT '" + w + "'")) {
			t.Errorf("stop word %q inside a string must not matter", w)
		}
	}
	for _, w := range ShareAllowList {
		if !ShareFits([]byte("SELECT " + w + "(a) FROM t")) {
			t.Errorf("allow-listed %q does not fit", w)
		}
		if ShareFits([]byte("SELECT " + w + "_x(a) FROM t")) {
			t.Errorf("near miss of %q fits", w)
		}
	}
	for _, w := range ShareParenKeywords {
		if slices.Contains(ShareStopWords, w) {
			t.Errorf("%q is both a paren keyword and a stop word", w)
		}
	}
	if !slices.IsSorted(ShareAllowList) {
		t.Error("the share allow-list is not sorted")
	}
	for _, bad := range []string{"now", "random", "nextval", "setval", "currval", "lastval", "set_config", "current_setting", "pg_sleep", "pg_backend_pid", "clock_timestamp", "statement_timestamp", "timeofday", "transaction_timestamp", "gen_random_uuid", "setseed", "pg_notify", "pg_advisory_lock", "txid_current", "version", "current_database", "age", "date_trunc", "to_timestamp", "to_date", "to_char"} {
		if slices.Contains(ShareAllowList, bad) {
			t.Errorf("%q must not be on the share allow-list", bad)
		}
	}
}

func shrIsAlphabet(c byte) bool { return strings.IndexByte(shrRefAlphabet, c) >= 0 }

func shrInsertOutsideAlphabet(t *testing.T, fits func([]byte) bool) int {
	t.Helper()
	misses := 0
	for _, base := range shrFitting {
		if len(base) >= ShareMaxLen {
			continue
		}
		for c := 0; c < 256; c++ {
			if shrIsAlphabet(byte(c)) {
				continue
			}
			for at := 0; at <= len(base); at++ {
				q := base[:at] + string([]byte{byte(c)}) + base[at:]
				if fits([]byte(q)) {
					misses++
				}
			}
		}
	}
	return misses
}

// TestSafeShapePropertyAnyByteOutsideTheAlphabetBreaksAFit proves that
// inserting any byte outside the safe alphabet at any position of a fitting
// query causes ShareFits to return false.
func TestSafeShapePropertyAnyByteOutsideTheAlphabetBreaksAFit(t *testing.T) {
	if n := shrInsertOutsideAlphabet(t, ShareFits); n != 0 {
		t.Fatalf("%d texts with a byte outside the alphabet fit", n)
	}
}

// TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails is the canary
// proving that skipping the alphabet check causes the alphabet property to fail.
func TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails(t *testing.T) {
	n := shrInsertOutsideAlphabet(t, func(b []byte) bool { return shareFits(b, true) })
	if n == 0 {
		t.Fatal("with the alphabet check skipped the property still holds: it cannot catch a missing alphabet check")
	}
	t.Logf("with the alphabet check skipped, %d texts with a byte outside the alphabet fit", n)
}

func shrCase(rng *rand.Rand, s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' && rng.IntN(2) == 0 {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

type shrGen struct {
	rng   *rand.Rand
	depth int
}

func (g *shrGen) pick(xs ...string) string { return xs[g.rng.IntN(len(xs))] }

func (g *shrGen) kw(s string) string { return shrCase(g.rng, s) }

func (g *shrGen) num() string { return g.pick("0", "1", "2", "3", "7", "10", "42") }

func (g *shrGen) str() string {
	return g.pick("'a'", "'b'", "'x'", "'y'", "'z'", "'1'", "'hello world'", "'it''s'", "''", "'ABC'")
}

func (g *shrGen) call(fn string, args ...string) string {
	return g.kw(fn) + g.pick("(", " (", "  (") + strings.Join(args, ", ") + ")"
}

func (g *shrGen) intExpr() string {
	if g.depth > 3 {
		return g.pick("a", g.num())
	}
	g.depth++
	defer func() { g.depth-- }()
	switch g.rng.IntN(10) {
	case 0:
		return g.call("abs", g.intExpr())
	case 1:
		return g.call("mod", g.intExpr(), g.pick("2", "3", "5"))
	case 2:
		return g.call(g.pick("greatest", "least"), g.intExpr(), g.intExpr())
	case 3:
		return g.call("coalesce", g.intExpr(), g.num())
	case 4:
		return g.call(g.pick("length", "char_length", "octet_length", "ascii", "bit_length"), g.textExpr())
	case 5:
		return "(" + g.intExpr() + ")"
	case 6:
		return g.intExpr() + " * " + g.num()
	case 7:
		return g.call("div", g.intExpr(), g.pick("2", "3"))
	case 8:
		return g.call("sign", g.intExpr())
	}
	return g.pick("a", g.num())
}

func (g *shrGen) textExpr() string {
	if g.depth > 3 {
		return g.pick("b", g.str())
	}
	g.depth++
	defer func() { g.depth-- }()
	switch g.rng.IntN(12) {
	case 0:
		return g.call(g.pick("lower", "upper", "initcap", "reverse", "md5", "btrim", "ltrim", "rtrim"), g.textExpr())
	case 1:
		return g.call("concat", g.textExpr(), g.textExpr())
	case 2:
		return g.call("coalesce", g.textExpr(), g.str())
	case 3:
		return g.call("replace", g.textExpr(), g.str(), g.str())
	case 4:
		return g.call(g.pick("left", "right"), g.textExpr(), g.num())
	case 5:
		return g.call(g.pick("lpad", "rpad"), g.textExpr(), g.pick("3", "5"), g.str())
	case 6:
		return g.call("nullif", g.textExpr(), g.str())
	case 7:
		return g.call("concat_ws", g.str(), g.textExpr(), g.textExpr())
	case 8:
		return g.call("repeat", g.textExpr(), g.pick("0", "1", "2"))
	case 9:
		return g.call("to_hex", g.intExpr())
	}
	return g.pick("b", g.str())
}

func (g *shrGen) cond() string {
	if g.depth > 2 {
		return g.intExpr() + " " + g.pick("=", "<", ">", "<=", ">=", "<>") + " " + g.intExpr()
	}
	g.depth++
	defer func() { g.depth-- }()
	switch g.rng.IntN(8) {
	case 0:
		return g.cond() + " " + g.kw(g.pick("and", "or")) + " " + g.cond()
	case 1:
		return g.kw("not") + " (" + g.cond() + ")"
	case 2:
		return "a " + g.kw("in") + " (" + g.num() + ", " + g.num() + ")"
	case 3:
		return g.kw("exists") + " (" + g.kw("select") + " 1 " + g.kw("from") + " u " + g.kw("where") + " a = " + g.num() + ")"
	case 4:
		return g.textExpr() + " " + g.pick("=", "<>") + " " + g.textExpr()
	case 5:
		return "a " + g.kw("between") + " " + g.num() + " " + g.kw("and") + " " + g.num()
	case 6:
		return "a = " + g.kw("any") + " (" + g.kw("select") + " a " + g.kw("from") + " u)"
	}
	return g.intExpr() + " " + g.pick("=", "<", ">", "<=", ">=", "<>") + " " + g.intExpr()
}

func (g *shrGen) selectText() string {
	s := g.kw("select")
	switch g.rng.IntN(5) {
	case 0:
		return s + " v " + g.kw("from") + " kv " + g.kw("where") + " k = " + g.pick("'a'", "'b'", "'missing'")
	case 1:
		q := s + " " + g.call("count", "*") + ", " + g.call(g.pick("sum", "max", "min"), "a") + ", " + g.call(g.pick("max", "min"), "b") + " " + g.kw("from") + " t"
		if g.rng.IntN(2) == 0 {
			q += " " + g.kw("where") + " " + g.cond()
		}
		return q
	case 2:
		return s + " " + g.textExpr() + ", " + g.intExpr() + " " + g.kw("from") + " t"
	}
	q := s + " "
	if g.rng.IntN(4) == 0 {
		q += g.kw("distinct") + " "
	}
	q += g.textExpr() + " " + g.kw("as") + " x, " + g.intExpr() + " " + g.kw("as") + " y " + g.kw("from") + " t"
	if g.rng.IntN(2) == 0 {
		q += " " + g.kw("where") + " " + g.cond()
	}
	q += " " + g.kw("order by") + " " + g.pick("1, 2", "2, 1", "x, y")
	if g.rng.IntN(2) == 0 {
		q += " " + g.kw("limit") + " " + g.num()
	}
	return q
}

func shrGenerate(seed uint64, n int) []string {
	g := &shrGen{rng: rand.New(rand.NewPCG(seed, 0x9a))}
	out := make([]string, 0, n)
	for len(out) < n {
		q := g.selectText()
		if len(q) <= ShareMaxLen {
			out = append(out, q)
		}
	}
	return out
}

// TestSafeShapePropertyGrammarTextsFit proves that 5,000 queries generated
// from a safe-shape SQL grammar fit ShareFits and lex as one clean statement.
func TestSafeShapePropertyGrammarTextsFit(t *testing.T) {
	texts := shrGenerate(shrSeed, shrGrammarTexts)
	for _, q := range texts {
		if !ShareFits([]byte(q)) {
			t.Errorf("generated %q does not fit", q)
		}
		shrCheckLexerAgrees(t, []byte(q))
	}
}

var shrPieces = []string{
	"select", "SELECT", " ", " ", " ", "a", "b", "1", "(", ")", "'", "''", ",", "*", "=", "<", ">",
	"from", "t", "where", "count", "lower", "now", "Today", "into", "for", "e", "x", "_", "nextval",
	"in", "join", "by", "\n", ";", ".", "\t", "\x00", "\xff", "$", "\\", "-", "/", "\"", ":",
}

func shrRandomText(rng *rand.Rand) []byte {
	if rng.IntN(4) == 0 {
		b := make([]byte, rng.IntN(24))
		for i := range b {
			b[i] = byte(rng.IntN(256))
		}
		return b
	}
	var b []byte
	if rng.IntN(3) > 0 {
		b = append(b, shrPieces[rng.IntN(2)]...)
	}
	for k := rng.IntN(16); k >= 0; k-- {
		p := shrPieces[rng.IntN(len(shrPieces))]
		if rng.IntN(3) > 0 {
			p = shrPieces[rng.IntN(20)]
		}
		b = append(b, p...)
	}
	return b
}

// TestSafeShapePropertyFitsAgreesWithTheReferenceChecker proves over 200,000
// seeded random texts that ShareFits matches the independent reference checker
// and every fitting text lexes as one clean statement.
func TestSafeShapePropertyFitsAgreesWithTheReferenceChecker(t *testing.T) {
	rng := rand.New(rand.NewPCG(shrSeed, 0xc))
	fits := 0
	for i := 0; i < shrRandomTexts; i++ {
		q := shrRandomText(rng)
		got, want := ShareFits(q), shrReference(q)
		if got != want {
			t.Fatalf("%q: ShareFits %v, reference %v (seed %#x)", q, got, want, shrSeed)
		}
		if got {
			fits++
			shrCheckLexerAgrees(t, q)
		}
	}
	if fits < 1000 || fits > shrRandomTexts-1000 {
		t.Fatalf("%d of %d random texts fit: the property was not exercised on both sides", fits, shrRandomTexts)
	}
	t.Logf("%d of %d random texts fit; ShareFits and the reference agreed on all", fits, shrRandomTexts)
}

func shrCheckLexerAgrees(t testing.TB, q []byte) {
	t.Helper()
	toks, clean := LexAll(q)
	if !clean {
		t.Fatalf("%q fits the safe shape but the lexer cannot tokenise it: a bug in one of the two", q)
	}
	significant := 0
	for _, tk := range toks {
		if tk.Kind == TokenPunct && q[tk.Start] == ';' {
			t.Fatalf("%q fits the safe shape but the lexer sees a statement separator", q)
		}
		if tk.Kind.Significant() {
			significant++
		}
	}
	if significant == 0 {
		t.Fatalf("%q fits the safe shape but the lexer sees no statement", q)
	}
	u := ClassifyQuery(q, false)
	if u.Statements != 1 || u.Opaque || !u.SafeRead {
		t.Fatalf("%q fits the safe shape but the classifier reports %s", q, clsDescribe(u))
	}
}

func shrRefStrings(q string) (contents []string, prefixed, open bool) {
	i := 0
	for i < len(q) {
		if q[i] != '\'' {
			i++
			continue
		}
		if i > 0 && strings.IndexByte(shrRefWordBytes, q[i-1]) >= 0 {
			prefixed = true
		}
		var b strings.Builder
		j := i + 1
		closed := false
		for j < len(q) {
			if q[j] == '\'' {
				if j+1 < len(q) && q[j+1] == '\'' {
					b.WriteByte('\'')
					j += 2
					continue
				}
				closed = true
				break
			}
			b.WriteByte(q[j])
			j++
		}
		if !closed {
			return contents, prefixed, true
		}
		contents = append(contents, b.String())
		i = j + 1
	}
	return contents, prefixed, false
}

func shrRefOutside(q string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(q); i++ {
		if q[i] == '\'' {
			in = !in
			b.WriteByte(0x01)
			continue
		}
		if in {
			b.WriteByte(0x01)
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String()
}

type shrRefWord struct {
	text  string
	paren bool
}

func shrRefWords(outside string) []shrRefWord {
	var out []shrRefWord
	isWord := func(c byte) bool { return strings.IndexByte(shrRefWordBytes, c) >= 0 }
	for i := 0; i < len(outside); {
		if !isWord(outside[i]) {
			i++
			continue
		}
		j := i
		for j < len(outside) && isWord(outside[j]) {
			j++
		}
		rest := strings.TrimLeft(outside[j:], " ")
		out = append(out, shrRefWord{text: strings.ToLower(outside[i:j]), paren: strings.HasPrefix(rest, "(")})
		i = j
	}
	return out
}

func shrReference(b []byte) bool {
	q := string(b)
	if len(q) < 1 || len(q) > ShareMaxLen {
		return false
	}
	for i := 0; i < len(q); i++ {
		if !shrIsAlphabet(q[i]) {
			return false
		}
	}
	contents, prefixed, open := shrRefStrings(q)
	if open || prefixed {
		return false
	}
	for _, c := range contents {
		lc := strings.ToLower(c)
		for _, w := range []string{"now", "today", "tomorrow", "yesterday"} {
			if strings.Contains(lc, w) {
				return false
			}
		}
	}
	trimmed := strings.TrimLeft(q, " ")
	if len(trimmed) < 6 || strings.ToLower(trimmed[:6]) != "select" || (len(trimmed) > 6 && strings.IndexByte(shrRefWordBytes, trimmed[6]) >= 0) {
		return false
	}
	for _, w := range shrRefWords(shrRefOutside(q)) {
		if slices.Contains(ShareStopWords, w.text) {
			return false
		}
		if w.paren && !slices.Contains(ShareParenKeywords, w.text) && !slices.Contains(ShareAllowList, w.text) {
			return false
		}
	}
	return true
}

func shrDeclaredIn(t *testing.T, file string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				out[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out[n.Name] = true
					}
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				}
			}
		}
	}
	return out
}

// TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier proves via
// go/ast that share.go has zero imports and references no identifier declared
// outside share.go itself.
func TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier(t *testing.T) {
	forbidden := map[string]string{}
	for _, f := range []string{"lex.go", "classify.go", "rejectlist.go"} {
		for name := range shrDeclaredIn(t, f) {
			forbidden[name] = f
		}
	}
	if len(forbidden) < 20 {
		t.Fatalf("only %d identifiers found in the lexer and classifier files", len(forbidden))
	}
	own := shrDeclaredIn(t, "share.go")
	package_ := map[string]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for name := range shrDeclaredIn(t, f) {
			package_[name] = true
		}
	}
	src, err := os.ReadFile("share.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "share.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Imports) != 0 {
		t.Errorf("share.go imports %d packages, want none", len(f.Imports))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if file, bad := forbidden[id.Name]; bad && !own[id.Name] {
			t.Errorf("share.go references %s, defined in %s", id.Name, file)
		}
		if package_[id.Name] && !own[id.Name] {
			t.Errorf("share.go references the package identifier %s, which it does not define", id.Name)
		}
		return true
	})
}

// FuzzSafeShape proves that ShareFits never panics, always matches the
// reference checker, and whenever it returns true the lexer reads one clean statement.
func FuzzSafeShape(f *testing.F) {
	for _, q := range shrFitting {
		f.Add([]byte(q))
	}
	for _, c := range shrNotFitting {
		f.Add([]byte(c.text))
	}
	for _, q := range shrGenerate(shrSeed, 32) {
		f.Add([]byte(q))
	}
	f.Fuzz(func(t *testing.T, q []byte) {
		got := ShareFits(q)
		if got != shrReference(q) {
			t.Fatalf("%q: ShareFits %v, reference %v", q, got, !got)
		}
		if got {
			shrCheckLexerAgrees(t, q)
		}
		_ = shareFits(q, true)
	})
}

// TestSafeShapeAllocatesNothing proves that ShareFits performs zero heap
// allocations on fitting, non-fitting, and over-length inputs.
func TestSafeShapeAllocatesNothing(t *testing.T) {
	for _, q := range append(append([]string{}, shrFitting...), "SELECT 'now'", "SELECT nextval('s')", strings.Repeat("a", ShareMaxLen+1)) {
		b := []byte(q)
		if n := testing.AllocsPerRun(20, func() { ShareFits(b) }); n != 0 {
			t.Errorf("%q: ShareFits allocated %v times", q, n)
		}
	}
}
