package adv_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Independent transcription of the 64 stop words specified in the builder brief.
var briefStopWords = []string{
	"into", "for", "with", "tablesample",
	"insert", "update", "delete", "merge", "truncate", "create", "alter", "drop", "grant",
	"revoke", "call", "do", "copy", "lock", "set", "reset", "show", "prepare", "execute",
	"deallocate", "listen", "notify", "unlisten", "declare", "fetch", "move", "close", "begin",
	"start", "commit", "rollback", "abort", "savepoint", "release", "discard", "load", "vacuum",
	"analyze", "explain", "refresh", "reindex", "cluster", "checkpoint", "comment", "import",
	"security", "values", "table",
	"current_date", "current_time", "current_timestamp", "localtime", "localtimestamp",
	"current_role", "current_user", "session_user", "system_user", "user", "current_catalog",
	"current_schema",
}

func isSafeAlphabetByte(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	switch c {
	case ' ', '_', ',', '*', '=', '<', '>', '(', ')', '\'':
		return true
	}
	return false
}

// --- Claim 1: Safe shape is exact ---

func TestAdv_1_LengthAndAlphabetBoundaries(t *testing.T) {
	_ = advSeed(t)

	// Length boundaries: 0, 1, 5, 6, ShareMaxLen-1, ShareMaxLen, ShareMaxLen+1, 4*ShareMaxLen.
	lenCases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"nil", nil, false},
		{"empty", []byte{}, false},
		{"1_byte", []byte("S"), false},
		{"5_bytes", []byte("SELEC"), false},
		{"6_bytes_bare_select", []byte("SELECT"), true},
		{"max_minus_1", []byte("SELECT " + strings.Repeat("a", pgwire.ShareMaxLen-8)), true},
		{"exact_max", []byte("SELECT " + strings.Repeat("a", pgwire.ShareMaxLen-7)), true},
		{"max_plus_1", []byte("SELECT " + strings.Repeat("a", pgwire.ShareMaxLen-6)), false},
		{"4x_max", []byte("SELECT " + strings.Repeat("a", pgwire.ShareMaxLen*4)), false},
		{"max_leading_spaces", []byte(strings.Repeat(" ", pgwire.ShareMaxLen-6) + "SELECT"), true},
		{"max_plus_1_leading_spaces", []byte(strings.Repeat(" ", pgwire.ShareMaxLen-5) + "SELECT"), false},
	}
	for _, tc := range lenCases {
		got := pgwire.ShareFits(tc.in)
		if got != tc.want {
			t.Errorf("%s (len=%d): ShareFits=%v, want %v", tc.name, len(tc.in), got, tc.want)
		}
		u := pgwire.ClassifyQuery(tc.in, false)
		if u.SafeRead != tc.want {
			t.Errorf("%s (len=%d): ClassifyQuery SafeRead=%v, want %v", tc.name, len(tc.in), u.SafeRead, tc.want)
		}
	}

	// Exhaustive 256-byte alphabet check at 4 positions in a fitting base query.
	positions := []struct {
		name   string
		before string
		after  string
		// When c is in the alphabet, whether the resulting query is still expected to fit
		// (only checked for non-alphabet bytes: ANY non-alphabet byte MUST fail).
	}{
		{"leading", "", "SELECT 1"},
		{"between_tokens", "SELECT ", " 1"},
		{"inside_string", "SELECT 'a", "b'"},
		{"trailing", "SELECT 1 ", ""},
	}
	for _, pos := range positions {
		for b := 0; b < 256; b++ {
			c := byte(b)
			if isSafeAlphabetByte(c) {
				continue
			}
			q := append(append([]byte(pos.before), c), pos.after...)
			if pgwire.ShareFits(q) {
				t.Errorf("pos=%s byte=0x%02x (%q): ShareFits=true for non-alphabet byte in %q", pos.name, c, c, q)
			}
			if u := pgwire.ClassifyQuery(q, false); u.SafeRead || u.Shareable {
				t.Errorf("pos=%s byte=0x%02x: ClassifyQuery SafeRead=%v Shareable=%v", pos.name, c, u.SafeRead, u.Shareable)
			}
		}
	}
}

func TestAdv_1_QuoteTogglePrefixAndTimeWordEdges(t *testing.T) {
	rng := advRNG(t, 102)

	// Rule 3 & 5: quote parity and time-word detection across doubled quotes and multiple strings.
	quoteCases := []struct {
		q    string
		want bool
	}{
		{"SELECT ''", true},
		{"SELECT ''''", true},
		{"SELECT ''''''", true},
		{"SELECT '''", false},
		{"SELECT '''''", false},
		{"SELECT 'a''b'", true},
		{"SELECT 'n''ow'", true},        // content is n'ow (does not contain "now")
		{"SELECT '''now'''", false},     // content is 'now' (contains "now")
		{"SELECT 'a''''now'", false},    // content is a''now (contains "now")
		{"SELECT 'now''''a'", false},    // content is now''a (contains "now")
		{"SELECT '' 'now'", false},      // second string has "now"
		{"SELECT 'ok', 'ToDaY'", false}, // mixed case in second string
		{"SELECT 'ok', 'xTOMORROWy'", false},
		{"SELECT 'ok', 'yEsTeRdAy'", false},
		{"SELECT 'now' ''", false},
		{"SELECT 'a'b'c'", false}, // 'b' precedes opening quote
		{"SELECT ('a')", true},
		{"SELECT ,'a'", true},
		{"SELECT ='a'", true},
		{"SELECT <'a'", true},
		{"SELECT >'a'", true},
		{"SELECT *'a'", true},
		{"SELECT )'a'", true},
		{"SELECT '' (now) ''", true}, // now outside strings is a plain column/ident, not a stop word
	}
	for _, tc := range quoteCases {
		if got := pgwire.ShareFits([]byte(tc.q)); got != tc.want {
			t.Errorf("%q: ShareFits=%v, want %v", tc.q, got, tc.want)
		}
	}

	// Rule 4: EVERY ASCII letter, digit, and '_' immediately before opening '\'' MUST fail.
	wordBytes := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for i := 0; i < len(wordBytes); i++ {
		q := "SELECT " + string(wordBytes[i]) + "'ok'"
		if pgwire.ShareFits([]byte(q)) {
			t.Errorf("prefix %q before quote: ShareFits=true for %q", wordBytes[i], q)
		}
		q2 := "SELECT 'first', " + string(wordBytes[i]) + "'second'"
		if pgwire.ShareFits([]byte(q2)) {
			t.Errorf("second-string prefix %q before quote: ShareFits=true for %q", wordBytes[i], q2)
		}
	}

	// Rule 5: randomized mixed-case time words embedded in random safe strings.
	timeWords := []string{"now", "today", "tomorrow", "yesterday"}
	safeInner := "abcdefghijklmopqrstuvxz0123456789_ ,*=<>()"
	for i := 0; i < 400; i++ {
		tw := []byte(timeWords[rng.IntN(len(timeWords))])
		for j := range tw {
			if rng.IntN(2) == 0 {
				tw[j] -= 'a' - 'A'
			}
		}
		pre := make([]byte, rng.IntN(8))
		suf := make([]byte, rng.IntN(8))
		for j := range pre {
			pre[j] = safeInner[rng.IntN(len(safeInner))]
		}
		for j := range suf {
			suf[j] = safeInner[rng.IntN(len(suf))]
		}
		q := "SELECT '" + string(pre) + string(tw) + string(suf) + "'"
		if pgwire.ShareFits([]byte(q)) {
			t.Errorf("iter=%d: ShareFits=true for time-word string %q", i, q)
		}
	}
}

func TestAdv_1_StopListParenListAndLeadEdges(t *testing.T) {
	rng := advRNG(t, 103)

	// Every stop word named in the brief MUST fail outside strings, in any case, in any position.
	for _, sw := range briefStopWords {
		mixed := []byte(sw)
		for i := range mixed {
			if mixed[i] >= 'a' && mixed[i] <= 'z' && rng.IntN(2) == 0 {
				mixed[i] -= 'a' - 'A'
			}
		}
		for _, q := range []string{
			"SELECT " + sw,
			"SELECT " + strings.ToUpper(sw),
			"SELECT " + string(mixed) + " FROM t",
			"SELECT a FROM t WHERE b = " + sw,
			"SELECT " + sw + "(1)",
		} {
			if pgwire.ShareFits([]byte(q)) {
				t.Errorf("stop word %q: ShareFits=true for %q", sw, q)
			}
		}
		// Inside a string literal, stop words do not trigger rule 6 (none contains a time word).
		if !pgwire.ShareFits([]byte("SELECT '" + sw + "'")) {
			t.Errorf("stop word %q inside string literal should fit", sw)
		}
	}

	// Non-SELECT leads and word-boundary checks on SELECT.
	badLeads := []string{
		"   ",
		"(SELECT 1)",
		"'a' SELECT 1",
		", SELECT 1",
		"* SELECT 1",
		"= SELECT 1",
		"SELECT1 1",
		"SELECT_ 1",
		"SELECTED 1",
		"1 SELECT",
		"_SELECT 1",
		"FROM t",
	}
	for _, q := range badLeads {
		if pgwire.ShareFits([]byte(q)) {
			t.Errorf("bad lead %q: ShareFits=true", q)
		}
	}

	// Unlisted function / keyword calls with 0, 1, and 5 spaces before '('.
	unlistedCalls := []string{
		"by", "join", "like", "is", "over", "filter", "cast", "extract", "trim",
		"substring", "overlay", "position", "numeric", "varchar", "timestamptz",
		"date", "now", "random", "nextval", "setval", "currval", "lastval",
		"set_config", "current_setting", "pg_sleep", "pg_backend_pid",
		"clock_timestamp", "statement_timestamp", "transaction_timestamp",
		"timeofday", "gen_random_uuid", "txid_current", "pg_notify",
		"pg_advisory_lock", "pg_advisory_xact_lock", "setseed", "version",
		"current_database", "generate_series", "unnest", "array_agg",
		"json_build_object", "to_char", "to_date", "to_timestamp", "date_trunc",
		"format", "encode", "decode", "1", "_", "custom_fn",
	}
	for _, fn := range unlistedCalls {
		for _, sp := range []string{"", " ", "     "} {
			q := "SELECT " + fn + sp + "(1)"
			if pgwire.ShareFits([]byte(q)) {
				t.Errorf("unlisted call %q: ShareFits=true", q)
			}
		}
	}
}

func TestAdv_1_ShareableContractWithRejectAndTx(t *testing.T) {
	rng := advRNG(t, 104)

	// Inputs that fit the safe shape (SafeRead==true) YET are rejected by the courtesy check.
	// Claim 1: Shareable MUST be false whenever rejected or inTx.
	safeYetRejected := []string{
		"SELECT pg_temp",
		"SELECT pg_temp_1",
		"SELECT pg_temp_99",
		"SELECT a FROM pg_temp_1",
		"SELECT local temp",
		"SELECT global temp",
		"SELECT local temporary",
		"SELECT global temporary",
		"SELECT replace temp",
		"SELECT replace temporary",
	}
	var c pgwire.Classifier
	for _, q := range safeYetRejected {
		if !pgwire.ShareFits([]byte(q)) {
			t.Fatalf("precondition failed: expected %q to fit ShareFits", q)
		}
		for _, inTx := range []bool{false, true} {
			u := c.Classify("tenant1", []byte(q), inTx)
			if !u.SafeRead {
				t.Errorf("%q (inTx=%v): SafeRead=false, want true", q, inTx)
			}
			if u.Class != work.ClassReject {
				t.Errorf("%q (inTx=%v): Class=%s, want reject", q, inTx, u.Class)
			}
			if u.Shareable {
				t.Errorf("%q (inTx=%v): Shareable=true despite ClassReject", q, inTx)
			}
			if u.HasKey {
				t.Errorf("%q (inTx=%v): HasKey=true despite ClassReject", q, inTx)
			}
		}
	}

	// Random safe-shape queries: Shareable == (SafeRead && !inTx && Class != ClassReject).
	cols := []string{"a", "b", "k", "v", "id"}
	fns := pgwire.ShareAllowList
	for i := 0; i < 300; i++ {
		fn := fns[rng.IntN(len(fns))]
		col := cols[rng.IntN(len(cols))]
		q := "SELECT " + fn + "(" + col + ") FROM kv WHERE k = 'key_" + string('a'+byte(rng.IntN(26))) + "'"
		for _, inTx := range []bool{false, true} {
			u := c.Classify("t", []byte(q), inTx)
			wantShare := u.SafeRead && !inTx && u.Class != work.ClassReject
			if u.Shareable != wantShare {
				t.Errorf("iter=%d %q inTx=%v: Shareable=%v want %v", i, q, inTx, u.Shareable, wantShare)
			}
			if u.HasKey != wantShare {
				t.Errorf("iter=%d %q inTx=%v: HasKey=%v want %v", i, q, inTx, u.HasKey, wantShare)
			}
		}
	}
	if err := c.Snapshot().Check(); err != nil {
		t.Fatalf("classifier snapshot conservation failed: %v", err)
	}
}

// --- Claim 3: The recogniser does not depend on the lexer ---

func collectPackageDecls(t *testing.T, path string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	decls := map[string]bool{}
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Recv == nil {
				decls[x.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, sp := range x.Specs {
				switch s := sp.(type) {
				case *ast.ValueSpec:
					for _, id := range s.Names {
						decls[id.Name] = true
					}
				case *ast.TypeSpec:
					decls[s.Name.Name] = true
				}
			}
		}
	}
	return decls
}

func TestAdv_3_ShareGoZeroExternalIdentifiersAST(t *testing.T) {
	_ = advSeed(t)
	pgDir := filepath.Join(repoRoot(t), "internal", "pgwire")
	entries, err := os.ReadDir(pgDir)
	if err != nil {
		t.Fatal(err)
	}

	sharePath := filepath.Join(pgDir, "share.go")
	ownDecls := collectPackageDecls(t, sharePath)
	if len(ownDecls) == 0 {
		t.Fatal("share.go declared no identifiers")
	}

	otherDecls := map[string]string{}
	lexerClassifierDecls := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "share.go" {
			continue
		}
		for id := range collectPackageDecls(t, filepath.Join(pgDir, name)) {
			otherDecls[id] = name
			if name == "lex.go" || name == "classify.go" || name == "rejectlist.go" {
				lexerClassifierDecls[id] = name
			}
		}
	}

	src, err := os.ReadFile(sharePath)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sharePath, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Imports) != 0 {
		t.Errorf("share.go has %d imports, want 0", len(f.Imports))
	}

	// Walk every identifier in share.go; any reference to a symbol declared in another
	// file of internal/pgwire (and not locally declared) breaks structural independence.
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		// If id.Obj != nil and refers to a local var/param/label inside share.go, skip.
		if id.Obj != nil {
			return true
		}
		if file, bad := otherDecls[id.Name]; bad && !ownDecls[id.Name] {
			pos := fset.Position(id.Pos())
			t.Errorf("share.go:%d references %q declared in %s", pos.Line, id.Name, file)
		}
		return true
	})

	// Also verify share_doc.go references nothing from lex.go, classify.go, or rejectlist.go.
	docPath := filepath.Join(pgDir, "share_doc.go")
	docSrc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	docFile, err := parser.ParseFile(fset, docPath, docSrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(docFile, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Obj != nil {
			return true
		}
		if file, bad := lexerClassifierDecls[id.Name]; bad {
			pos := fset.Position(id.Pos())
			t.Errorf("share_doc.go:%d references %q declared in %s", pos.Line, id.Name, file)
		}
		return true
	})
}

func TestAdv_3_SafeShapeImpliesCleanSingleLexedStatement(t *testing.T) {
	rng := advRNG(t, 302)

	// Build diverse fitting safe-shape texts including lexer-tricky valid-alphabet tokens
	// (0x hex numbers, 1e2 exponents, 123abc, operator runs *=<>, doubled quotes, empty strings, bare SELECT).
	edgeFits := []string{
		"SELECT",
		"SELECT(1)",
		"SELECT ''''",
		"SELECT 0x1F, 0o77, 0b1010, 1_000, 1e2, 1E9",
		"SELECT 123abc",
		"SELECT 1=2<>3<=4>=5*6",
		"SELECT *=<>()",
		"SELECT _a, __b, a_1_2 FROM t_1",
		"SELECT 'a''b''c', '', ''''''",
		"SELECT " + strings.Repeat("a", pgwire.ShareMaxLen-7),
	}
	tokens := []string{
		"a", "b", "1", "0xFF", "1e3", "1_0", "''", "'abc'", "'it''s'",
		",", "*", "=", "<", ">", "<>", "<=", ">=", "(", ")", " ", "  ",
		"abs(a)", "coalesce(a, 1)", "count(*)", "from", "where", "and", "or",
	}
	total := 3000
	if advLong() {
		total = 20000
	}
	checkedFits := 0
	checkFit := func(q []byte) {
		if !pgwire.ShareFits(q) {
			return
		}
		checkedFits++
		toks, clean := pgwire.LexAll(q)
		if !clean {
			t.Fatalf("ShareFits=true but LexAll clean=false for %q", q)
		}
		end := 0
		for _, tk := range toks {
			if tk.Start != end || tk.End <= end || tk.End > len(q) {
				t.Fatalf("bad lex span %+v on safe-shape %q", tk, q)
			}
			end = tk.End
		}
		if end != len(q) {
			t.Fatalf("lexer stopped at %d/%d on safe-shape %q", end, len(q), q)
		}
		u := pgwire.ClassifyQuery(q, false)
		if u.Opaque || u.Statements != 1 || !u.SafeRead {
			t.Fatalf("safe-shape %q: Opaque=%v Statements=%d SafeRead=%v (want false, 1, true)", q, u.Opaque, u.Statements, u.SafeRead)
		}
	}
	for _, q := range edgeFits {
		if !pgwire.ShareFits([]byte(q)) {
			t.Fatalf("expected edge case %q to fit", q)
		}
		checkFit([]byte(q))
	}
	for i := 0; i < total; i++ {
		var b strings.Builder
		b.WriteString("SELECT ")
		n := 1 + rng.IntN(12)
		for j := 0; j < n; j++ {
			b.WriteString(tokens[rng.IntN(len(tokens))])
			if rng.IntN(2) == 0 {
				b.WriteByte(' ')
			}
		}
		checkFit([]byte(b.String()))
	}
	if checkedFits < 1000 {
		t.Fatalf("too few fitting texts checked: %d", checkedFits)
	}

	// Conversely, untokenisable texts must NEVER satisfy ShareFits or SafeRead or Shareable.
	badSnippets := []string{"'", `"`, "$$", "$a$", "/*", "\\", "{", "}", "\x00", "\x01", "E'\\", "U&'"}
	for i := 0; i < 1000; i++ {
		q := []byte("SELECT " + badSnippets[rng.IntN(len(badSnippets))] + " x")
		_, clean := pgwire.LexAll(q)
		if clean {
			continue
		}
		if pgwire.ShareFits(q) {
			t.Fatalf("untokenisable %q returned ShareFits=true", q)
		}
		if u := pgwire.ClassifyQuery(q, false); u.SafeRead || u.Shareable {
			t.Fatalf("untokenisable %q returned SafeRead=%v Shareable=%v", q, u.SafeRead, u.Shareable)
		}
	}
}

func TestAdv_3_LexerTricksCannotWidenSafeReadOrShareable(t *testing.T) {
	_ = advSeed(t)

	// Inputs designed to exploit lexer constructs (string continuation, dollar quotes,
	// unicode escapes, trailing unary +/-, nested block comments, invalid UTF-8).
	tricky := []string{
		"SELECT 'a'\n'b'",
		"SELECT E'ok'\n'\\' ; SELECT 1'",
		"SELECT $$1$$",
		`SELECT "a" FROM t`,
		`SELECT U&"a\0062" FROM t`,
		"SELECT 1+-2",
		"SELECT 1 /* nested /* c */ */",
		"SELECT 1 -- comment\n",
		"SELECT \xff",
		"SELECT 1\x00",
		"SELECT pg_catalog.lower('a')",
		"SELECT 'n'\n'ow'",
		"SELECT 'a' || 'b'",
	}
	for _, q := range tricky {
		fits := pgwire.ShareFits([]byte(q))
		if fits {
			t.Errorf("lexer-tricky %q should not fit safe shape", q)
		}
		for _, inTx := range []bool{false, true} {
			u := pgwire.ClassifyQuery([]byte(q), inTx)
			if u.SafeRead != fits {
				t.Errorf("%q (inTx=%v): SafeRead=%v != ShareFits=%v", q, inTx, u.SafeRead, fits)
			}
			if u.Shareable {
				t.Errorf("%q (inTx=%v): Shareable=true when ShareFits=false", q, inTx)
			}
		}
	}
}

// --- Claim 4: The hint never widens ---

func TestAdv_4_HintExactPrefixOnly(t *testing.T) {
	_ = advSeed(t)
	hint := "/* mg:share */"

	// Every single-byte deletion from the hint must NOT set Hinted.
	for i := 0; i < len(hint); i++ {
		mut := hint[:i] + hint[i+1:] + " SELECT 1"
		if u := pgwire.ClassifyQuery([]byte(mut), false); u.Hinted {
			t.Errorf("deleted byte %d (%q): Hinted=true", i, mut)
		}
	}

	// Every single-byte insertion inside the hint must NOT set Hinted.
	for i := 0; i <= len(hint); i++ {
		for _, ins := range []byte{' ', '\t', '\n', 'x', '*'} {
			mut := hint[:i] + string(ins) + hint[i:] + " SELECT 1"
			if bytes.HasPrefix([]byte(mut), []byte(hint)) {
				continue
			}
			if u := pgwire.ClassifyQuery([]byte(mut), false); u.Hinted {
				t.Errorf("inserted %q at %d (%q): Hinted=true", ins, i, mut)
			}
		}
	}

	// Every ASCII letter case flip in the hint must NOT set Hinted.
	for i := 0; i < len(hint); i++ {
		if hint[i] >= 'a' && hint[i] <= 'z' {
			b := []byte(hint + " SELECT 1")
			b[i] -= 'a' - 'A'
			if u := pgwire.ClassifyQuery(b, false); u.Hinted {
				t.Errorf("case flip at %d (%q): Hinted=true", i, b)
			}
		}
	}

	// Every single-byte prefix before the hint (0x00..0xFF) must NOT set Hinted.
	for c := 0; c < 256; c++ {
		q := append([]byte{byte(c)}, []byte(hint+" SELECT 1")...)
		if u := pgwire.ClassifyQuery(q, false); u.Hinted {
			t.Errorf("prefix 0x%02x before hint: Hinted=true", c)
		}
	}

	// UTF-8 BOM, mid-query, trailing, nested comments.
	nonHints := []string{
		"\xef\xbb\xbf/* mg:share */ SELECT 1",
		"SELECT /* mg:share */ 1",
		"SELECT 1 /* mg:share */",
		"/* /* mg:share */ */ SELECT 1",
		"/*mg:share*/ SELECT 1",
		"/*  mg:share */ SELECT 1",
		"/* mg:share  */ SELECT 1",
		"/*\tmg:share */ SELECT 1",
		"/* mg:share\n*/ SELECT 1",
	}
	for _, q := range nonHints {
		if u := pgwire.ClassifyQuery([]byte(q), false); u.Hinted {
			t.Errorf("%q: Hinted=true, want false", q)
		}
	}
}

func TestAdv_4_HintNeverSetsSafeReadOrShareable(t *testing.T) {
	rng := advRNG(t, 402)
	hint := "/* mg:share */"

	// Prepend hint to fitting safe-shape queries, writes, volatile queries, rejects, opaque, >4096 bytes.
	payloads := []string{
		"",
		" ",
		"SELECT 1",
		" SELECT v FROM kv WHERE k = 'a'",
		" SELECT now()",
		" SELECT random()",
		" SELECT nextval('s')",
		" INSERT INTO kv VALUES ('a', 'b')",
		" DELETE FROM kv",
		" SET search_path = app",
		" SELECT 'unclosed",
		"\x00SELECT 1",
		" SELECT " + strings.Repeat("a", pgwire.ShareMaxLen+100),
	}
	for i := 0; i < 300; i++ {
		fn := pgwire.ShareAllowList[rng.IntN(len(pgwire.ShareAllowList))]
		payloads = append(payloads, " SELECT "+fn+"(a) FROM t")
	}

	for _, p := range payloads {
		q := []byte(hint + p)
		if pgwire.ShareFits(q) {
			t.Errorf("hinted %q: ShareFits=true", q)
		}
		for _, inTx := range []bool{false, true} {
			u := pgwire.ClassifyQuery(q, inTx)
			if !u.Hinted {
				t.Errorf("hinted %q (inTx=%v): Hinted=false", q, inTx)
			}
			if u.SafeRead {
				t.Errorf("hinted %q (inTx=%v): SafeRead=true (hint widened SafeRead!)", q, inTx)
			}
			if u.Shareable {
				t.Errorf("hinted %q (inTx=%v): Shareable=true (hint widened Shareable!)", q, inTx)
			}
		}
	}
}

func TestAdv_4_HintKeyScopeAndImmutability(t *testing.T) {
	_ = advSeed(t)

	for _, scope := range []work.KeyScope{work.ScopeGlobal, work.ScopeTenant} {
		c := pgwire.Classifier{Config: pgwire.ClassifierConfig{KeyScope: scope}}

		// Forwarded hinted (including opaque, empty, and >4096B) gets a copied key when !inTx.
		for _, text := range []string{
			"/* mg:share */",
			"/* mg:share */ SELECT now()",
			"/* mg:share */ SELECT 'unclosed",
			"/* mg:share */ SELECT " + strings.Repeat("x", pgwire.ShareMaxLen+50),
		} {
			buf := []byte(text)
			u1 := c.Classify("alice", buf, false)
			u2 := c.Classify("bob", buf, false)
			if !u1.Hinted || u1.SafeRead || u1.Shareable || !u1.HasKey {
				t.Fatalf("scope=%d %q: unexpected unit %+v", scope, text, u1)
			}
			buf[0] = '?'
			if string(u1.Key.Text) != text {
				t.Fatalf("scope=%d: key text aliased caller buffer", scope)
			}
			wantEqual := scope == work.ScopeGlobal
			if u1.Key.Equal(u2.Key) != wantEqual {
				t.Fatalf("scope=%d: alice vs bob key Equal=%v, want %v", scope, u1.Key.Equal(u2.Key), wantEqual)
			}

			// Inside transaction: Hinted=true, HasKey=false.
			uTx := c.Classify("alice", []byte(text), true)
			if !uTx.Hinted || uTx.HasKey || uTx.SafeRead || uTx.Shareable {
				t.Fatalf("scope=%d inTx=true %q: unexpected unit %+v", scope, text, uTx)
			}
		}

		// Rejected hinted: Hinted=true, Class=ClassReject, HasKey=false.
		uRej := c.Classify("alice", []byte("/* mg:share */ DISCARD ALL"), false)
		if !uRej.Hinted || uRej.Class != work.ClassReject || uRej.HasKey || uRej.SafeRead || uRej.Shareable {
			t.Fatalf("scope=%d rejected hinted: unexpected unit %+v", scope, uRej)
		}

		if err := c.Snapshot().Check(); err != nil {
			t.Fatalf("scope=%d snapshot conservation failed: %v", scope, err)
		}
	}
}
