package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const stubGoLog = `#!/bin/bash
[ "$1" = test ] || { echo "stub go: unexpected arguments $*" >&2; exit 9; }
if [ -n "${STUB_RUNS:-}" ] && [ -n "${RUN_DIR:-}" ]; then mkdir -p "$RUN_DIR" && cp -r "$STUB_RUNS"/. "$RUN_DIR"/; fi
cat "$STUB_LOG"
exit "${STUB_RC:-0}"
`

const mod = "github.com/nickstrad/mini-gateway"

func pkgLine(status, pkg, rest string) string {
	return status + "\t" + mod + "/" + pkg + "\t" + rest + "\n"
}

func e2eBlock(failSeed2 bool) string {
	var b strings.Builder
	b.WriteString("=== RUN   TestScenarioE2E\n")
	for _, sc := range []string{"e2e-results", "e2e-queue-full"} {
		b.WriteString("=== RUN   TestScenarioE2E/" + sc + "\n=== PAUSE TestScenarioE2E/" + sc + "\n")
	}
	for _, sc := range []string{"e2e-results", "e2e-queue-full"} {
		b.WriteString("=== CONT  TestScenarioE2E/" + sc + "\n")
		for _, s := range []string{"1", "2", "3"} {
			b.WriteString("=== RUN   TestScenarioE2E/" + sc + "/seed=" + s + "\n")
		}
	}
	if failSeed2 {
		b.WriteString("=== NAME  TestScenarioE2E/e2e-queue-full/seed=2\n" +
			"    scenario_test.go:41: e2e-queue-full: shed 2, want 3\n" +
			"    determinism.go:147: simulation failed\n" +
			"        seed: 2\n" +
			"        error: e2e: scenario check failed\n" +
			"        digests: trace=5e1f state=77aa\n" +
			"        steps: 120, virtual time: 3000215352\n" +
			"        trace tail (2 of 120):\n" +
			"          t=2999000000 engine shed client 7\n" +
			"          t=3000215352 flush 1\n" +
			"        PIN: echo '2 TestScenarioE2E/e2e-queue-full/seed=2' >> testdata/seeds.txt\n" +
			"        REPLAY: go test ./test/e2e -run '^TestScenarioE2E$/^e2e-queue-full$/^seed=2$' -seed=2 -count=1\n")
	}
	if failSeed2 {
		b.WriteString("--- FAIL: TestScenarioE2E (0.00s)\n")
	} else {
		b.WriteString("--- PASS: TestScenarioE2E (0.00s)\n")
	}
	for _, sc := range []string{"e2e-queue-full", "e2e-results"} {
		st := "PASS"
		if failSeed2 && sc == "e2e-queue-full" {
			st = "FAIL"
		}
		b.WriteString("    --- " + st + ": TestScenarioE2E/" + sc + " (0.47s)\n")
		for _, s := range []string{"1", "2", "3"} {
			st := "PASS"
			if failSeed2 && sc == "e2e-queue-full" && s == "2" {
				st = "FAIL"
			}
			b.WriteString("        --- " + st + ": TestScenarioE2E/" + sc + "/seed=" + s + " (0.15s)\n")
		}
	}
	if failSeed2 {
		b.WriteString("FAIL\n" + pkgLine("FAIL", "test/e2e", "4.682s"))
	} else {
		b.WriteString("PASS\n" + pkgLine("ok  ", "test/e2e", "4.682s"))
	}
	return b.String()
}

func sweepLog(failSeed2 bool) string {
	var b strings.Builder
	b.WriteString("PASS\n" + pkgLine("ok  ", "cmd/mgsim", "0.007s [no tests to run]"))
	b.WriteString("=== RUN   TestScenarioClassifierRejectList\n")
	for _, s := range []string{"1", "2", "3", "7", "42"} {
		b.WriteString("=== RUN   TestScenarioClassifierRejectList/seed=" + s + "\n" +
			"    classify_e2e_test.go:24: digests TestScenarioClassifierRejectList/seed=" + s + " seed=" + s + " trace=7dc6 state=ec56\n")
	}
	b.WriteString("--- PASS: TestScenarioClassifierRejectList (0.20s)\n")
	for _, s := range []string{"1", "2", "3", "7", "42"} {
		b.WriteString("    --- PASS: TestScenarioClassifierRejectList/seed=" + s + " (0.03s)\n")
	}
	b.WriteString("PASS\n" + pkgLine("ok  ", "internal/pgwire", "10.789s"))
	b.WriteString("?   \t" + mod + "/internal/pgwire/pgwiretest/clsworld\t[no test files]\n")
	b.WriteString("=== RUN   TestScenarioRandomFaultSchedules\n")
	for _, s := range []string{"1", "2", "3", "7", "42"} {
		b.WriteString("=== RUN   TestScenarioRandomFaultSchedules/seed=" + s + "\n")
	}
	b.WriteString("--- PASS: TestScenarioRandomFaultSchedules (0.28s)\n")
	for _, s := range []string{"1", "2", "3", "7", "42"} {
		b.WriteString("    --- PASS: TestScenarioRandomFaultSchedules/seed=" + s + " (0.06s)\n")
	}
	b.WriteString("PASS\n" + pkgLine("ok  ", "internal/sim/world", "44.195s"))
	b.WriteString(e2eBlock(failSeed2))
	return b.String()
}

type scriptRun struct {
	out  string
	code int
}

func runScript(t *testing.T, script, log string, rc int, env ...string) scriptRun {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "go-stub")
	logf := filepath.Join(tmp, "recorded.log")
	if err := os.WriteFile(stub, []byte(stubGoLog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logf, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", script))
	var base []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "VERBOSE=") && !strings.HasPrefix(kv, "RUN_DIR=") {
			base = append(base, kv)
		}
	}
	cmd.Env = append(base, append([]string{"SWEEP_GO=" + stub, "CHAOS_GO=" + stub, "CHAOS_TIER_R=0",
		"STUB_LOG=" + logf, "STUB_RC=" + strconv.Itoa(rc), "RUN_DIR=" + filepath.Join(tmp, "runs")}, env...)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	r := scriptRun{out: buf.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

var sweepScenarioLine = regexp.MustCompile(`^(\./\S+) +(\S+) +seeds (\d+) +(pass|FAIL) +(files|log) \S+$`)

func scenarioLines(out string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if m := sweepScenarioLine.FindStringSubmatch(l); m != nil {
			got = append(got, m[1]+" "+m[2]+" "+m[3]+" "+m[4])
		}
	}
	return got
}

// TestSweepConciseOneLinePerScenario proves sweep.sh defaults to one line per
// scenario plus the gate note and summary without raw go test lines.
func TestSweepConciseOneLinePerScenario(t *testing.T) {
	r := runScript(t, "sweep.sh", sweepLog(false), 0, "SWEEP_SEEDS=3")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	lines := strings.Split(strings.TrimRight(r.out, "\n"), "\n")
	if lines[0] != "NOTE: 3 seeds per scenario from 1, tests matching ^TestScenario" {
		t.Fatalf("first line %q is not the gate note", lines[0])
	}
	want := []string{
		"./internal/pgwire TestScenarioClassifierRejectList 5 pass",
		"./internal/sim/world TestScenarioRandomFaultSchedules 5 pass",
		"./test/e2e TestScenarioE2E/e2e-queue-full 3 pass",
		"./test/e2e TestScenarioE2E/e2e-results 3 pass",
	}
	if got := scenarioLines(r.out); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("scenario lines:\n%s\nwant:\n%s\nfull output:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), r.out)
	}
	if len(lines) != 1+len(want)+1 || !strings.HasPrefix(lines[len(lines)-1], "sweep: 16 seed runs passed, 0 failures; log ") {
		t.Fatalf("want the note, one line per scenario and the summary, got:\n%s", r.out)
	}
	if strings.Contains(r.out, "=== RUN") || strings.Contains(r.out, "digests ") {
		t.Fatalf("concise output carries raw test output:\n%s", r.out)
	}
}

// TestSweepVerbosePrintsRawOutput proves VERBOSE=1 streams raw go test output
// alongside the gate note and scenario summary lines.
func TestSweepVerbosePrintsRawOutput(t *testing.T) {
	log := sweepLog(false)
	r := runScript(t, "sweep.sh", log, 0, "SWEEP_SEEDS=3", "VERBOSE=1")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	if !strings.Contains(r.out, log) {
		t.Fatalf("VERBOSE=1 does not print the raw go test output:\n%s", r.out)
	}
	if !strings.HasPrefix(r.out, "NOTE: ") || len(scenarioLines(r.out)) != 4 {
		t.Fatalf("VERBOSE=1 lost the note or the scenario lines:\n%s", r.out)
	}
}

// TestSweepConciseNeverHidesAFailure proves concise sweep output still prints
// the failing seed's FAIL, error, and REPLAY lines and exits 1.
func TestSweepConciseNeverHidesAFailure(t *testing.T) {
	r := runScript(t, "sweep.sh", sweepLog(true), 1, "SWEEP_SEEDS=3")
	if r.code != 1 {
		t.Fatalf("exit %d, want 1\n%s", r.code, r.out)
	}
	if got := scenarioLines(r.out); !strings.Contains(strings.Join(got, "\n"), "./test/e2e TestScenarioE2E/e2e-queue-full 3 FAIL") {
		t.Fatalf("no FAIL line for the scenario:\n%s", r.out)
	}
	replay := "REPLAY: go test ./test/e2e -run '^TestScenarioE2E$/^e2e-queue-full$/^seed=2$' -seed=2 -count=1"
	i := strings.Index(r.out, "TestScenarioE2E/e2e-queue-full ")
	if i < 0 {
		t.Fatalf("no scenario line\n%s", r.out)
	}
	after := r.out[i:]
	if j := strings.Index(after, "\n./"); j >= 0 {
		after = after[:j+1]
	}
	for _, want := range []string{
		"    --- FAIL: TestScenarioE2E/e2e-queue-full/seed=2 (0.15s)\n",
		"      scenario_test.go:41: e2e-queue-full: shed 2, want 3\n",
		"      error: e2e: scenario check failed\n",
		"      " + replay + "\n",
	} {
		if !strings.Contains(after, want) {
			t.Fatalf("the failed scenario's block lacks %q:\n%s\nfull output:\n%s", want, after, r.out)
		}
	}
	if !strings.Contains(r.out, "\nfirst failure: "+replay+"\n") {
		t.Fatalf("no first-failure REPLAY line:\n%s", r.out)
	}
	if !strings.Contains(r.out, "sweep: 15 seed runs passed, ") {
		t.Fatalf("summary line:\n%s", r.out)
	}
	if strings.Contains(r.out, "trace tail") || strings.Contains(r.out, "=== RUN") {
		t.Fatalf("concise output carries the raw trace:\n%s", r.out)
	}
}

// TestSweepNoScenarioMatchedFails proves sweep.sh exits 1 when no scenario matches.
func TestSweepNoScenarioMatchedFails(t *testing.T) {
	r := runScript(t, "sweep.sh", "PASS\n"+pkgLine("ok  ", "internal/config", "0.045s [no tests to run]"), 0, "SWEEP_SEEDS=3")
	if r.code != 1 || !strings.Contains(r.out, "no scenario test matched") || len(scenarioLines(r.out)) != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
}

var chaosLine = regexp.MustCompile(`(?m)^S  (\S+) +seeds (\d+) +(pass|FAIL|FAIL\(sql\)) +\S+$`)

// TestChaosConciseAndVerbose proves chaos.sh prints one line per scenario by
// default and includes raw go test output when VERBOSE=1.
func TestChaosConciseAndVerbose(t *testing.T) {
	log := e2eBlock(false)
	r := runScript(t, "chaos.sh", log, 0)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	m := chaosLine.FindAllStringSubmatch(r.out, -1)
	if len(m) != 2 || m[0][1] != "e2e-queue-full" || m[1][1] != "e2e-results" || m[0][3] != "pass" || m[1][3] != "pass" {
		t.Fatalf("scenario lines %q\n%s", m, r.out)
	}
	if strings.Contains(r.out, "=== RUN") {
		t.Fatalf("concise output carries raw test output:\n%s", r.out)
	}
	v := runScript(t, "chaos.sh", log, 0, "VERBOSE=1")
	if v.code != 0 || !strings.Contains(v.out, log) || len(chaosLine.FindAllString(v.out, -1)) != 2 {
		t.Fatalf("VERBOSE=1: exit %d\n%s", v.code, v.out)
	}
}

// TestChaosFailedScenarioShowsItsReplay proves concise chaos output prints the
// failing seed's error and REPLAY command under its FAIL scenario line.
func TestChaosFailedScenarioShowsItsReplay(t *testing.T) {
	r := runScript(t, "chaos.sh", e2eBlock(true), 1)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	for _, want := range []string{
		"    --- FAIL: TestScenarioE2E/e2e-queue-full/seed=2 (0.15s)\n",
		"      scenario_test.go:41: e2e-queue-full: shed 2, want 3\n",
		"      REPLAY: go test ./test/e2e -run '^TestScenarioE2E$/^e2e-queue-full$/^seed=2$' -seed=2 -count=1\n",
	} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("concise chaos output lacks %q:\n%s", want, r.out)
		}
	}
	if m := chaosLine.FindStringSubmatch(r.out); m == nil || m[1] != "e2e-queue-full" || m[3] != "FAIL" {
		t.Fatalf("no FAIL line for e2e-queue-full:\n%s", r.out)
	}
}

func duckdbInstalled() bool {
	if _, err := exec.LookPath("duckdb"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".local", "bin", "duckdb"))
	return err == nil
}

func extractRun(t *testing.T, tarball, prefix, dest string) {
	t.Helper()
	f, err := os.Open(tarball)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg || !strings.HasPrefix(h.Name, prefix) {
			continue
		}
		p := filepath.Join(dest, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestChaosFailedSQLShowsTheViolatingRows proves a SQL assertion failure marks
// the scenario FAIL(sql) and prints the violating rows from csv-assert.sh.
func TestChaosFailedSQLShowsTheViolatingRows(t *testing.T) {
	if testing.Short() {
		t.Skip("runs csv-assert.sh with DuckDB; run without -short")
	}
	if !duckdbInstalled() {
		if os.Getenv("MG_REQUIRE_DUCKDB") == "1" {
			t.Fatal("MG_REQUIRE_DUCKDB=1 and duckdb is not on PATH or in ~/.local/bin")
		}
		t.Skip("duckdb not installed; set MG_REQUIRE_DUCKDB=1 to make this a failure")
	}
	runs := t.TempDir()
	extractRun(t, filepath.Join("..", "test", "analysis", "report", "testdata", "e2e-runs.tar.gz"), "e2e-queue-full/2/base/", runs)
	p := filepath.Join(runs, "e2e-queue-full", "2", "base", "metrics.csv")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	old := "1,e2e-queue-full/2/base,1,3000215352,sim-1,engine,replies,counter,,,shed,,3\n"
	if bytes.Count(b, []byte(old)) != 1 {
		t.Fatalf("%s has no line %q", p, old)
	}
	if err := os.WriteFile(p, bytes.Replace(b, []byte(old), []byte(strings.Replace(old, ",3\n", ",2\n", 1)), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runScript(t, "chaos.sh", e2eBlock(false), 0, "STUB_RUNS="+runs)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	if m := chaosLine.FindAllStringSubmatch(r.out, -1); len(m) != 2 || m[0][1] != "e2e-queue-full" || m[0][3] != "FAIL(sql)" {
		t.Fatalf("scenario lines %q\n%s", m, r.out)
	}
	for _, want := range []string{
		"test/analysis/e2e-queue-full/faults.sql: 1 violating rows over 1 runs:\n",
		"      \"e2e-queue-full/2/base: row 2, shed 3\"\n",
		"test/analysis/e2e-queue-full/rows.sql: 1 violating rows over 1 runs:\n",
	} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("concise chaos output lacks %q:\n%s", want, r.out)
		}
	}
}
