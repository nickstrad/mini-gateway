package adv_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdv_1_OperatorCommentSeams(t *testing.T) { growth(t, rep("+-/*x*/-+ --tail\n"), 0) }
func TestAdv_1_KeepSignLate(t *testing.T) {
	growth(t, func(n int) string { return "SELECT " + strings.Repeat("+", n) + "@" + strings.Repeat("-+", n) }, 0)
}
func TestAdv_1_SplitAfterNumeric(t *testing.T) { growth(t, rep("0xAB+-1e-2+-"), 0) }
func TestAdv_2_QuotedCaseAndStatements(t *testing.T) {
	growth(t, rep("PG_SETTINGS,\"client_encoding\"; select "), 2)
}
func TestAdv_2_SettingSuffixAndNestedCalls(t *testing.T) {
	growth(t, rep("f(pg_settings, standard_conforming_stringsX), "), 2)
}
func TestAdv_2_UnicodeSettingNames(t *testing.T) {
	growth(t, rep("U&\"pg_!0073ettings\" UESCAPE '!', "), 2)
}
func TestAdv_3_OperatorCounterNotDead(t *testing.T) {
	deadCounter(t, "lex.go", "l.opRunBytes += l.run.end - l.run.start", "TestLexOperatorRunsGrowLinearly")
}
func TestAdv_3_KeywordCounterNotDead(t *testing.T) {
	deadCounter(t, "classify.go", "m.counts.keywordChecks++", "TestClassifierSpanKeywordChecksGrowLinearly")
}
func TestAdv_3_TokenCounterNotDead(t *testing.T) {
	deadCounter(t, "classify.go", "m.counts.tokenVisits++", "TestClassifierWorkGrowsLinearly")
}
func TestAdv_4_IndependentProcesses(t *testing.T) {
	repeated(t, []string{"", "SELECT '+-'\n'x'", strings.Repeat("$a$?$a$;", 90)}, false)
}
func TestAdv_4_ConcurrentProcesses(t *testing.T) {
	repeated(t, []string{strings.Repeat("f((1),2);", 1000), "SELECT " + strings.Repeat("+", 4096)}, true)
}
func TestAdv_4_HostileByteReplay(t *testing.T)  { repeated(t, randomInputs(32), true) }
func TestAdv_5_RescanOperators(t *testing.T)    { mutation(t, "operator", rep("+-"), 0) }
func TestAdv_5_RescanSettingWords(t *testing.T) { mutation(t, "words", rep("PG_SETTINGS, x, "), 2) }
func TestAdv_5_RescanInterleavedSettings(t *testing.T) {
	mutation(t, "words", rep("client_encoding /*gap*/ standard_conforming_strings, "), 2)
}
func TestAdv_6_ContinuationFailures(t *testing.T) { growth(t, rep("'a' /* comment */\n x,"), 1) }
func TestAdv_6_DollarTagNearMatches(t *testing.T) {
	growth(t, func(n int) string { return "$" + strings.Repeat("a", n) + "$" + strings.Repeat("$aaaX", n) }, 1)
}
func TestAdv_6_DeepUnbalancedAndResets(t *testing.T) { growth(t, rep("((f(1,2);)("), 3) }
func TestAdv_7_UnicodeLiterals(t *testing.T)         { growth(t, rep("U&\"\\0061\\0062\", f('a'\n'b'), "), 4) }
func TestAdv_7_SetLocalArgumentBoundaries(t *testing.T) {
	growth(t, func(n int) string { return "SELECT set_config('app." + strings.Repeat("a", n) + "','x',true)" }, 4)
}
func TestAdv_7_MixedTokenFamilies(t *testing.T) {
	growth(t, rep("f($x$a$x$,U&\"a\",1e-9,?+-),'x'\n'y';"), 3)
}
func TestAdv_8_RescanOpenParens(t *testing.T)     { mutation(t, "parens", rep("(("), 3) }
func TestAdv_8_RescanBalancedParens(t *testing.T) { mutation(t, "parens", rep("f((1),2),"), 3) }
func TestAdv_8_RescanStatementBoundaries(t *testing.T) {
	mutation(t, "parens", rep("f(1);SELECT (2);"), 3)
}
func TestAdv_9_NoClockImport(t *testing.T)    { clockAudit(t, 0) }
func TestAdv_9_NoClockSelectors(t *testing.T) { clockAudit(t, 1) }
func TestAdv_9_NoTimingHelpers(t *testing.T)  { clockAudit(t, 2) }
func TestAdv_10_AllCountersConcurrent(t *testing.T) {
	repeated(t, []string{strings.Repeat("U&\"\\0061\",+-,'x'\n'y';", 300)}, true)
}
func TestAdv_10_BoundaryOutputs(t *testing.T) {
	equivalent(t, []string{"", "\x00", "\xff", "'", "$", "/*", "SELECT 1", strings.Repeat(" ", 4095) + "x", strings.Repeat(" ", 4096) + "x", "SELECT set_config('search_path','pg_temp',true)"})
}
func TestAdv_10_SeededOutputsAndAllocations(t *testing.T) { equivalent(t, randomInputs(160)) }

func randomInputs(count int) []string {
	r := rand.New(rand.NewSource(seed))
	atoms := []string{"pg_settings", "set_config(", "'a'\n'b'", "U&\"!0061\" UESCAPE '!'", "/*", "--\n", "$x$abc$x$", "+-", "?", "(,);", "\x00\xff", " SELECT ", "\\", "'", "1e-5"}
	out := []string{}
	for i := 0; i < count; i++ {
		var b strings.Builder
		for j := r.Intn(60); j > 0; j-- {
			b.WriteString(atoms[r.Intn(len(atoms))])
		}
		out = append(out, b.String())
	}
	return out
}
func clockAudit(t *testing.T, mode int) {
	t.Logf("seed=%d", seed)
	src, err := os.ReadFile(filepath.Join(root, "internal/pgwire/classify_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "classify_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case 0:
		for _, i := range f.Imports {
			if i.Path.Value == `"time"` {
				t.Error("classifier tests import wall-clock package")
			}
		}
	case 1:
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok && (s.Sel.Name == "Now" || s.Sel.Name == "Since" || s.Sel.Name == "UnixNano") {
				t.Errorf("clock selector %s", s.Sel.Name)
			}
			return true
		})
	case 2:
		for _, name := range []string{"clsFastest", "clsTimeGrowthLimit", "time.Now", "time.Since"} {
			if bytes.Contains(src, []byte(name)) {
				t.Errorf("timing helper remains: %s", name)
			}
		}
	}
}
func mutation(t *testing.T, kind string, shape func(int) string, counter int) {
	t.Helper()
	t.Logf("seed=%d mutation=%s", seed, kind)
	file := "classify.go"
	if kind == "operator" {
		file = "lex.go"
	}
	src, err := os.ReadFile(filepath.Join(root, "internal/pgwire", file))
	if err != nil {
		t.Fatal(err)
	}
	before, after := "", ""
	switch kind {
	case "operator":
		before = "if l.pos < l.run.start || l.pos >= l.run.end {"
		after = "if true {"
	case "words":
		before = "func (m *clsMsg) spanWords(s clsSpan) clsSpanWords {"
		after = before + "\n for i:=s.lo;i<s.hi;i++ {for j:=s.lo;j<s.hi;j++ { _ = m.is(j, \"never_a_keyword\") }}\n"
	case "parens":
		before = "func (m *clsMsg) matchParens() {"
		after = before + "\n for i:=range m.toks {if m.isPunct(i,'(') {for j:=i;j<len(m.toks);j++ {_ = m.isPunct(j,')')}}}\n"
	}
	if strings.Count(string(src), before) != 1 {
		t.Fatal("mutation anchor changed")
	}
	bridge, err := os.ReadFile(filepath.Join(home, "bridge.txt"))
	if err != nil {
		t.Fatal(err)
	}
	bin := build(t.Name(), bridge, map[string][]byte{file: []byte(strings.Replace(string(src), before, after, 1))})
	inputs := []string{shape(64), shape(1024)}
	a := run(t, bin, inputs)
	t.Logf("mutated counts 64->1024: %v -> %v", a[0].Counts, a[1].Counts)
	b := run(t, current, inputs)
	if a[1].Counts[counter] <= 17*a[0].Counts[counter] {
		t.Fatalf("quadratic mutation escaped counter %d: %v -> %v", counter, a[0].Counts, a[1].Counts)
	}
	for i := range a {
		a[i].Counts = [5]int{}
		b[i].Counts = [5]int{}
		if !bytes.Equal(a[i].Unit, b[i].Unit) || !bytes.Equal(a[i].Tokens, b[i].Tokens) {
			t.Fatal("mutation changed output; not a work-only canary")
		}
	}
}

func TestAdv_6_LongSeededCampaign(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1 for 2000 seeded inputs")
	}
	equivalent(t, randomInputs(2000))
	// Run the actual three builder verdicts repeatedly; output is retained on failure.
	cmd := exec.Command("/usr/local/go/bin/go", "test", "-count=20", "-run", "Linearly", "./internal/pgwire")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("seed=%d repeated builder verdict failed: %v\n%s", seed, err, out)
	}
}

func deadCounter(t *testing.T, file, increment, test string) {
	t.Helper()
	t.Logf("seed=%d removed=%s", seed, increment)
	src, err := os.ReadFile(filepath.Join(root, "internal/pgwire", file))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte(increment)) {
		t.Fatal("counter anchor changed")
	}
	dir := filepath.Join(scratch, t.Name())
	must(os.MkdirAll(dir, 0700))
	dst := filepath.Join(dir, file)
	write(dst, bytes.ReplaceAll(src, []byte(increment), nil))
	b, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(root, "internal/pgwire", file): dst}})
	must(err)
	overlay := filepath.Join(dir, "overlay.json")
	write(overlay, b)
	cmd := exec.Command("/usr/local/go/bin/go", "test", "-overlay", overlay, "-count=1", "-run", "^"+test+"$", "./internal/pgwire")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("--- FAIL: "+test)) || !bytes.Contains(out, []byte("measured path did no work")) {
		t.Fatalf("dead counter was not rejected by matching verdict: err=%v output=%s", err, out)
	}
}
