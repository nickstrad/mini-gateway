package adv_test

import (
	"bufio"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// --- Claim 5: No panic, bounded work ---

func fastestClassify(q []byte) time.Duration {
	best := time.Duration(1<<63 - 1)
	for i := 0; i < 3; i++ {
		start := time.Now()
		u := pgwire.ClassifyQuery(q, false)
		runtime.KeepAlive(u)
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

func TestAdv_5_QuadraticSpanScanDoS(t *testing.T) {
	_ = advSeed(t)

	// NOTES.md states time is linear: 16x input must take at most 96x as long.
	// In classify.go tokenReject(), every occurrence of pg_settings calls spanHas(s, "update"),
	// and every occurrence of client_encoding / standard_conforming_strings calls
	// spanHas(s, "set") || spanHas(s, "reset"), scanning the entire statement span each time: O(N^2).
	const maxRatioFor16x = 96
	shapes := []struct {
		name string
		word string
	}{
		{"pg_settings", "pg_settings "},
		{"client_encoding", "client_encoding "},
		{"standard_conforming_strings", "standard_conforming_strings "},
	}

	smallSize := 16 << 10 // 16 KiB
	largeSize := 16 * smallSize
	for _, sh := range shapes {
		small := []byte("SELECT " + strings.Repeat(sh.word, smallSize/len(sh.word)) + "1")
		large := []byte("SELECT " + strings.Repeat(sh.word, largeSize/len(sh.word)) + "1")
		// Warm up once.
		_ = pgwire.ClassifyQuery(small, false)
		dSmall := fastestClassify(small)
		dLarge := fastestClassify(large)
		if dSmall < time.Microsecond {
			dSmall = time.Microsecond
		}
		ratio := float64(dLarge) / float64(dSmall)
		t.Logf("shape=%s lenSmall=%d (%v) lenLarge=%d (%v) ratio=%.1fx (linear bound <= %dx)",
			sh.name, len(small), dSmall, len(large), dLarge, ratio, maxRatioFor16x)
		if ratio > maxRatioFor16x {
			t.Errorf("CLAIM 5 BROKEN (%s): 16x input growth caused %.1fx runtime growth (%v -> %v), exceeding stated linear bound %dx (quadratic spanHas scan in tokenReject)",
				sh.name, ratio, dSmall, dLarge, maxRatioFor16x)
		}
	}
}

func TestAdv_5_MemoryCeilingHostileShapes(t *testing.T) {
	_ = advSeed(t)

	// Test hostile allocation shapes against ClassifMemoryFactor*len + ClassifMemoryConstant.
	shapes := []struct {
		name string
		gen  func(size int) []byte
	}{
		{"1byte_words_commas", func(n int) []byte { return []byte(strings.Repeat("a,", n/2)) }},
		{"open_paren_semicolon", func(n int) []byte { return []byte(strings.Repeat("(;", n/2)) }},
		{"word_semicolon", func(n int) []byte { return []byte(strings.Repeat("a;", n/2)) }},
		{"hinted_semicolons", func(n int) []byte { return []byte("/* mg:share */" + strings.Repeat(";", n-14)) }},
		{"unicode_idents", func(n int) []byte { return []byte(strings.Repeat(`U&"\0061" `, n/10)) }},
		{"doubled_quoted_idents", func(n int) []byte { return []byte(strings.Repeat(`"a""b"`, n/6)) }},
		{"nested_parens_calls", func(n int) []byte { return []byte(strings.Repeat("f(", n/2)) }},
	}

	for _, sh := range shapes {
		for _, size := range []int{4096, 1 << 16, 1 << 20} {
			q := sh.gen(size)
			var c pgwire.Classifier
			// Warm up.
			_ = c.Classify("t", q, false)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			u := c.Classify("t", q, false)
			runtime.KeepAlive(u)
			runtime.ReadMemStats(&after)
			used := int64(after.TotalAlloc - before.TotalAlloc)
			limit := int64(pgwire.ClassifMemoryFactor)*int64(len(q)) + int64(pgwire.ClassifMemoryConstant)
			if used > limit {
				t.Errorf("%s (len=%d): allocated %d bytes, exceeds ceiling %d (%.1f B/byte)",
					sh.name, len(q), used, limit, float64(used)/float64(len(q)))
			}
		}
	}

	// ShareFits must allocate 0 bytes even on 1 MiB inputs.
	shareInputs := [][]byte{
		[]byte("SELECT v FROM kv WHERE k = 'a'"),
		[]byte("SELECT " + strings.Repeat("a", pgwire.ShareMaxLen-7)),
		[]byte("SELECT " + strings.Repeat("a", 1<<20)),
		[]byte("SELECT 'now'"),
	}
	for _, in := range shareInputs {
		if allocs := testing.AllocsPerRun(20, func() { _ = pgwire.ShareFits(in) }); allocs != 0 {
			t.Errorf("ShareFits(len=%d) allocated %.1f times, want 0", len(in), allocs)
		}
	}
}

func TestAdv_5_ExtremeAndPathologicalInputsNoPanic(t *testing.T) {
	_ = advSeed(t)

	pathological := [][]byte{
		nil,
		{},
		[]byte("$" + strings.Repeat("a", 4000) + "$" + strings.Repeat("$"+strings.Repeat("a", 3999), 25)),
		[]byte(strings.Repeat("'a'\n", 10000) + "'z'"),
		[]byte("E'" + strings.Repeat(`\'`, 10000) + "'\n'z'"),
		[]byte(strings.Repeat("/*", 10000) + strings.Repeat("*/", 10000)),
		[]byte(strings.Repeat("(", 10000) + "1" + strings.Repeat(")", 10000)),
		[]byte("EXPLAIN " + strings.Repeat("(", 5000) + "ANALYZE" + strings.Repeat(")", 5000) + " EXECUTE q"),
		[]byte(`SELECT U&"" UESCAPE ''`),
		[]byte(`SELECT U&"\+" UESCAPE '+'`),
		[]byte(`SELECT U&"\+000000" UESCAPE '\'`),
		[]byte(`SELECT U&"\+110000" UESCAPE '\'`),
		[]byte(`SELECT U&"\D800" UESCAPE '\'`),
		[]byte("SELECT U&\"a\" UESCAPE 'a'\n'b'"),
		[]byte("SET LOCAL"),
		[]byte("SET LOCAL;"),
		[]byte("SET LOCAL ="),
		[]byte("SET LOCAL ."),
		[]byte("DECLARE"),
		[]byte("DECLARE c WITH"),
		[]byte("DECLARE c WITH HOLD"),
		[]byte("SELECT set_config("),
		[]byte("SELECT set_config('search_path',"),
		[]byte("SELECT set_config('search_path', 'a',"),
		[]byte("SELECT set_config('search_path', 'a', true"),
		[]byte("SELECT set_config('', '', true)"),
		[]byte("SELECT set_config('search_path', '', true)"),
	}
	for b := 0; b < 256; b++ {
		pathological = append(pathological, []byte{byte(b)})
	}

	var c pgwire.Classifier
	for idx, in := range pathological {
		toks, _ := pgwire.LexAll(in)
		end := 0
		for _, tk := range toks {
			if tk.Start != end || tk.End <= end || tk.End > len(in) {
				t.Fatalf("case %d (len=%d): invalid token span %+v", idx, len(in), tk)
			}
			end = tk.End
		}
		if end != len(in) {
			t.Fatalf("case %d: lexer covered %d of %d bytes", idx, end, len(in))
		}
		_ = pgwire.ShareFits(in)
		_ = c.Classify("t", in, false)
		_ = c.Classify("t", in, true)
	}
	if err := c.Snapshot().Check(); err != nil {
		t.Fatalf("conservation failed: %v", err)
	}
}

func TestAdv_5_SeededHostileFuzzCampaign(t *testing.T) {
	rng := advRNG(t, 504)
	iters := 5000
	if advLong() {
		iters = 100000
	}
	fragments := []string{
		"'", "''", "E'", `\'`, `"`, `""`, `U&"`, `U&'`, "UESCAPE", "$$", "$tag$",
		"/*", "*/", "--", "\n", "\r", "\x00", "\\", ";", "(", ")", ",", ".",
		"SELECT", "SET", "LOCAL", "search_path", "pg_temp", "set_config", "true",
		"EXPLAIN", "DECLARE", "WITH", "HOLD", "/* mg:share */", "now", "today",
	}
	var c pgwire.Classifier
	for i := 0; i < iters; i++ {
		var q []byte
		if i%2 == 0 {
			q = make([]byte, rng.IntN(256))
			for j := range q {
				q[j] = byte(rng.IntN(256))
			}
		} else {
			var b strings.Builder
			n := 1 + rng.IntN(16)
			for j := 0; j < n; j++ {
				b.WriteString(fragments[rng.IntN(len(fragments))])
				if rng.IntN(2) == 0 {
					b.WriteByte(' ')
				}
			}
			q = []byte(b.String())
		}
		toks, clean := pgwire.LexAll(q)
		end := 0
		for _, tk := range toks {
			if tk.Start != end || tk.End <= end || tk.End > len(q) {
				t.Fatalf("iter=%d bad span %+v len=%d", i, tk, len(q))
			}
			end = tk.End
		}
		if end != len(q) {
			t.Fatalf("iter=%d lost bytes %d/%d", i, end, len(q))
		}
		fits := pgwire.ShareFits(q)
		u := c.Classify("t", q, i%3 == 0)
		if u.SafeRead != fits {
			t.Fatalf("iter=%d SafeRead=%v != ShareFits=%v for %q", i, u.SafeRead, fits, q)
		}
		if u.Opaque == clean {
			t.Fatalf("iter=%d Opaque=%v == clean=%v for %q", i, u.Opaque, clean, q)
		}
	}
	if err := c.Snapshot().Check(); err != nil {
		t.Fatalf("snapshot conservation failed: %v", err)
	}
}

// --- Claim 6: Nothing is refused for being unknown ---

func TestAdv_6_UntokenisableWithoutRejectAlwaysForwarded(t *testing.T) {
	rng := advRNG(t, 601)
	badMiddles := []string{
		"'unclosed", `"unclosed`, "$$unclosed", "$tag$unclosed$other$",
		"/* unclosed", "/* nested /* still open */", `E'trailing\`,
		"U&'unclosed", `U&"unclosed`, `\`, "{", "}", "$", "\x00", "\x01", "\x07", "\x1b", "\x7f",
	}
	prefixes := []string{"", "SELECT 1 ", "BEGIN; SELECT ", "INSERT INTO t VALUES (1); ", "UPDATE t SET a = 1; "}
	for i := 0; i < 600; i++ {
		q := []byte(prefixes[rng.IntN(len(prefixes))] + badMiddles[rng.IntN(len(badMiddles))])
		if _, clean := pgwire.LexAll(q); clean {
			continue
		}
		for _, inTx := range []bool{false, true} {
			u := pgwire.ClassifyQuery(q, inTx)
			wantClass := work.ClassStartLease
			if inTx {
				wantClass = work.ClassContinueLease
			}
			if u.Class != wantClass || u.Reason != work.RejectNone || !u.Opaque || u.SafeRead || u.Shareable {
				t.Errorf("iter=%d inTx=%v %q: got %+v, want forwarded opaque", i, inTx, q, u)
			}
		}
	}
}

func TestAdv_6_UnmodelledSQLAndNonsenseForwarded(t *testing.T) {
	_ = advSeed(t)
	unmodelled := []string{
		"GRANT ALL ON TABLE kv TO PUBLIC",
		"REVOKE SELECT ON kv FROM PUBLIC",
		"VACUUM (FULL, ANALYZE) kv",
		"ANALYZE kv",
		"CLUSTER kv USING kv_pkey",
		"REINDEX TABLE CONCURRENTLY kv",
		"COMMENT ON TABLE kv IS 'note'",
		"SECURITY LABEL ON TABLE kv IS 'lbl'",
		"IMPORT FOREIGN SCHEMA s FROM SERVER srv INTO public",
		"CREATE SUBSCRIPTION sub CONNECTION 'host=x' PUBLICATION pub",
		"CREATE PUBLICATION pub FOR ALL TABLES",
		"ALTER SYSTEM SET work_mem = '8MB'",
		"REASSIGN OWNED BY a TO b",
		"DROP OWNED BY a",
		"CHECKPOINT",
		"REFRESH MATERIALIZED VIEW CONCURRENTLY mv",
		"CREATE EXTENSION IF NOT EXISTS pgcrypto",
		"CALL some_procedure(1, 'x')",
		"DO $$ BEGIN PERFORM 1; END $$",
		"MERGE INTO kv USING src ON kv.k = src.k WHEN MATCHED THEN UPDATE SET v = src.v",
		"FETCH FORWARD 10 FROM cur",
		"MOVE BACKWARD ALL IN cur",
		"CLOSE cur",
		"SAVEPOINT sp1",
		"RELEASE SAVEPOINT sp1",
		"ROLLBACK TO SAVEPOINT sp1",
		"COMMIT AND CHAIN",
		"ROLLBACK AND CHAIN",
		"VALUES (1, 'a'), (2, 'b')",
		"TABLE kv",
		"SHOW work_mem",
		"SELECT * FROM JSON_TABLE('[]', '$' COLUMNS (a int PATH '$.a'))",
		"FROBNICATE WIDGET 123 WITH (OPTION TRUE)",
		"QUUX BAR BAZ",
	}
	for _, q := range unmodelled {
		for _, inTx := range []bool{false, true} {
			u := pgwire.ClassifyQuery([]byte(q), inTx)
			if !u.Class.Forwards() || u.Reason != work.RejectNone {
				t.Errorf("%q (inTx=%v): refused (%s / %s), want forwarded", q, inTx, u.Class, u.Reason)
			}
		}
	}
}

func TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused(t *testing.T) {
	_ = advSeed(t)
	// On Postgres 17, `local`, `global`, `replace`, `temp`, `temporary` are all unreserved
	// keywords. A plain SELECT querying a table named `local`/`global`/`replace` with alias
	// `temp`/`temporary` uses NO temporary object and is NOT listed as a documented over-rejection
	// in docs/reject-list.md or NOTES.md — yet classify.go rejects it as RejectTempObject
	// (while ShareFits simultaneously marks it SafeRead=true!).
	plainSelects := []string{
		"SELECT a FROM local temp",
		"SELECT a FROM global temporary",
		"SELECT a FROM replace temp",
		"SELECT a FROM local temporary",
	}
	for _, q := range plainSelects {
		u := pgwire.ClassifyQuery([]byte(q), false)
		if u.Class == work.ClassReject {
			t.Errorf("CLAIM 6 BROKEN: plain SELECT %q (no CREATE/INTO, no temporary object; SafeRead=%v) was rejected with reason=%s",
				q, u.SafeRead, u.Reason)
		}
	}
}

// --- Claim 7: The courtesy list refuses what it documents ---

func parseDocsRejectTable(t *testing.T, section string) [][2]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "reject-list.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	inSection := false
	var rows [][2]string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			inSection = (ln == section)
			continue
		}
		if !inSection || !strings.HasPrefix(ln, "| ") || strings.HasPrefix(ln, "| ---") || strings.HasPrefix(ln, "| Reason ") || strings.HasPrefix(ln, "| Form ") {
			continue
		}
		// Unescape \| temporarily to split columns cleanly.
		safe := strings.ReplaceAll(ln, `\|`, "\x00")
		parts := strings.Split(strings.Trim(safe, "|"), "|")
		if len(parts) < 3 {
			continue
		}
		first := strings.TrimSpace(strings.Trim(strings.TrimSpace(parts[0]), "`"))
		last := strings.ReplaceAll(strings.TrimSpace(parts[len(parts)-1]), "\x00", "|")
		if strings.HasPrefix(last, "`") && strings.HasSuffix(last, "`") {
			last = last[1 : len(last)-1]
		}
		rows = append(rows, [2]string{first, last})
	}
	return rows
}

func advPadSQL(src string, rng *rand.Rand, padSet []string) string {
	toks, _ := pgwire.LexAll([]byte(src))
	var out strings.Builder
	for i, tk := range toks {
		if i > 0 && rng.IntN(2) == 0 {
			out.WriteString(padSet[rng.IntN(len(padSet))])
		}
		raw := append([]byte(nil), tk.Text([]byte(src))...)
		if tk.Kind == pgwire.TokenWord {
			for k := range raw {
				if raw[k] >= 'a' && raw[k] <= 'z' && rng.IntN(2) == 0 {
					raw[k] -= 'a' - 'A'
				} else if raw[k] >= 'A' && raw[k] <= 'Z' && rng.IntN(2) == 0 {
					raw[k] += 'a' - 'A'
				}
			}
		}
		out.Write(raw)
		if tk.Kind == pgwire.TokenBad && tk.End == len(src) && i == len(toks)-1 {
			return out.String()
		}
	}
	return out.String()
}

func TestAdv_7_DocsRejectListExamplesAllRefusedUnderAdversarialPadding(t *testing.T) {
	rng := advRNG(t, 701)
	rows := parseDocsRejectTable(t, "## Rejected")
	if len(rows) != 16 {
		t.Fatalf("parsed %d rows from docs/reject-list.md, want 16", len(rows))
	}
	pads := []string{
		"/**/",
		"/* outer /* inner ; SELECT 1; */ */",
		"-- cr only\r",
		"\f\v\t\r\n ",
		" /* x */ ",
	}
	wrappers := []string{
		"%s",
		"SELECT 1; %s",
		"%s; SELECT 2",
		"BEGIN; %s; COMMIT",
		"%s -- trailing comment with no newline",
	}

	sqlCount := 0
	for _, r := range rows {
		reason, example := r[0], r[1]
		if reason == "extended-protocol" {
			continue
		}
		sqlCount++
		for k := 0; k < 20; k++ {
			padded := advPadSQL(example, rng, pads)
			wrapped := strings.Replace(wrappers[k%len(wrappers)], "%s", padded, 1)
			for _, inTx := range []bool{false, true} {
				u := pgwire.ClassifyQuery([]byte(wrapped), inTx)
				if u.Class != work.ClassReject || u.Reason.String() != reason {
					t.Errorf("doc example %q (%s) variant %q inTx=%v: got class=%s reason=%s",
						example, reason, wrapped, inTx, u.Class, u.Reason)
				}
			}
		}
	}
	if sqlCount != 15 {
		t.Fatalf("expected 15 SQL reject examples in docs/reject-list.md, got %d", sqlCount)
	}
}

func TestAdv_7_CorpusRejectEntriesRefusedUnderAdversarialPadding(t *testing.T) {
	rng := advRNG(t, 702)
	f, err := os.Open(filepath.Join(repoRoot(t), "internal", "pgwire", "testdata", "classifier-corpus.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	pads := []string{"/**/", "/* a /* b */ c */", "-- cr\r", "\v\f\t\n"}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	rejects := 0
	lineNo := 0
	for sc.Scan() {
		lineNo++
		ln := sc.Text()
		if strings.HasPrefix(ln, "#") || strings.TrimSpace(ln) == "" {
			continue
		}
		flags, text, ok := strings.Cut(ln, "\t")
		if !ok {
			t.Fatalf("line %d missing tab", lineNo)
		}
		if strings.HasPrefix(text, `"`) {
			if text, err = strconv.Unquote(text); err != nil {
				t.Fatalf("line %d unquote: %v", lineNo, err)
			}
		}
		var wantReason string
		for _, fl := range strings.Fields(flags) {
			if strings.HasPrefix(fl, "R:") {
				wantReason = fl[2:]
			}
		}
		if wantReason == "" {
			continue
		}
		rejects++
		for k := 0; k < 5; k++ {
			padded := advPadSQL(text, rng, pads)
			for _, inTx := range []bool{false, true} {
				u := pgwire.ClassifyQuery([]byte(padded), inTx)
				if u.Class != work.ClassReject || u.Reason.String() != wantReason {
					t.Errorf("corpus line %d (%s) padded %q inTx=%v: got class=%s reason=%s",
						lineNo, wantReason, padded, inTx, u.Class, u.Reason)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if rejects < 180 {
		t.Fatalf("expected >= 180 reject entries in corpus, saw %d", rejects)
	}
}

func TestAdv_7_DocumentedFeaturesAndAllowedFormsBoundary(t *testing.T) {
	_ = advSeed(t)

	// Test every specific feature explicitly named in docs/reject-list.md Feature column.
	documented := []struct {
		reason work.RejectReason
		sql    string
	}{
		{work.RejectSessionSet, "SET SESSION work_mem = '4MB'"},
		{work.RejectSessionSet, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL SERIALIZABLE"},
		{work.RejectSessionSet, "SET ROLE myrole"},
		{work.RejectSessionSet, "SET SESSION AUTHORIZATION DEFAULT"},
		{work.RejectSessionSet, "SET TIME ZONE 'UTC'"},
		{work.RejectSessionSet, "SET NAMES 'UTF8'"},
		{work.RejectSessionSet, "SET SCHEMA 'app'"},
		{work.RejectSessionSet, "SET local.x = 1"},
		{work.RejectSessionSet, "SET transaction.y = 2"},
		{work.RejectSessionSet, "SET constraints.z = 3"},
		{work.RejectSessionSet, "SET LOCAL NAMES 'UTF8'"},
		{work.RejectSessionSet, "UPDATE pg_settings SET setting = 'app' WHERE name = 'search_path'"},
		{work.RejectSessionSet, "SET LOCAL standard_conforming_strings = off"},
		{work.RejectSessionSet, "SET LOCAL client_encoding = 'LATIN1'"},
		{work.RejectReset, "RESET ALL"},
		{work.RejectPrepare, "PREPARE p (int) AS SELECT $1"},
		{work.RejectExecute, "EXPLAIN (COSTS OFF) EXECUTE p"},
		{work.RejectExecute, "CREATE TABLE dst AS EXECUTE p"},
		{work.RejectDeallocate, "DEALLOCATE PREPARE p"},
		{work.RejectListen, "LISTEN ch"},
		{work.RejectUnlisten, "UNLISTEN ch"},
		{work.RejectCursorWithHold, "DECLARE cur NO SCROLL CURSOR WITH HOLD FOR SELECT 1"},
		{work.RejectTempObject, "CREATE GLOBAL TEMP TABLE gt (a int)"},
		{work.RejectTempObject, "CREATE LOCAL TEMPORARY SEQUENCE ls"},
		{work.RejectTempObject, "CREATE OR REPLACE TEMP VIEW tv AS SELECT 1"},
		{work.RejectTempObject, "SELECT 1 INTO TEMP tt"},
		{work.RejectTempObject, "SELECT * FROM pg_temp.tt"},
		{work.RejectTempObject, "SET LOCAL search_path = 'PG_TEMP'"},
		{work.RejectTempObject, `SET LOCAL SCHEMA E'a\x5fb'`},
		{work.RejectTempObject, `SET LOCAL search_path = U&'a\005fb'`},
		{work.RejectTempObject, "SELECT set_config('search_path', 'a' || 'b', true)"},
		{work.RejectAdvisoryLock, "SELECT pg_advisory_lock_shared(1)"},
		{work.RejectAdvisoryLock, "SELECT pg_try_advisory_lock(1, 2)"},
		{work.RejectAdvisoryLock, "SELECT pg_try_advisory_lock_shared(1)"},
		{work.RejectAdvisoryLock, "SELECT pg_advisory_unlock(1)"},
		{work.RejectAdvisoryLock, "SELECT pg_advisory_unlock_shared(1)"},
		{work.RejectAdvisoryLock, "SELECT pg_advisory_unlock_all()"},
		{work.RejectSetConfig, "SELECT set_config('work_mem', '4MB', 'true')"},
		{work.RejectSetConfig, "SELECT set_config($$work_mem$$, '4MB', true)"},
		{work.RejectSetConfig, "SELECT set_config('client_encoding', 'UTF8', true)"},
		{work.RejectSetConfig, "SELECT set_config('standard_conforming_strings', 'off', true)"},
		{work.RejectDiscard, "DISCARD SEQUENCES"},
		{work.RejectLoad, "LOAD 'auto_explain'"},
		{work.RejectCopy, "COPY kv FROM STDIN"},
		{work.RejectTwoPhase, "PREPARE TRANSACTION 't1'"},
		{work.RejectTwoPhase, "COMMIT PREPARED 't1'"},
		{work.RejectTwoPhase, "ROLLBACK PREPARED 't1'"},
	}
	for _, tc := range documented {
		u := pgwire.ClassifyQuery([]byte(tc.sql), false)
		if u.Class != work.ClassReject || u.Reason != tc.reason {
			t.Errorf("%q: got class=%s reason=%s, want reject %s", tc.sql, u.Class, u.Reason, tc.reason)
		}
	}

	// Every example in the "Allowed because they are transaction-scoped" table MUST be forwarded.
	allowed := parseDocsRejectTable(t, "## Allowed because they are transaction-scoped")
	if len(allowed) < 8 {
		t.Fatalf("parsed only %d allowed forms from docs/reject-list.md", len(allowed))
	}
	for _, r := range allowed {
		u := pgwire.ClassifyQuery([]byte(r[1]), true)
		if !u.Class.Forwards() {
			t.Errorf("documented allowed form %q was rejected (%s)", r[1], u.Reason)
		}
	}
}
