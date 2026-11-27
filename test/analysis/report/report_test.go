package report_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const envRequireDuckDB = "MG_REQUIRE_DUCKDB"

var update = flag.Bool("update", false, "rewrite the golden files under testdata/")

func updating() bool { return *update || os.Getenv("MG_REPORT_UPDATE") == "1" }

func repoRoot(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func duckdbPath() string {
	if p, err := exec.LookPath("duckdb"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "duckdb")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func heavy(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs DuckDB over recorded runs; run without -short")
	}
}

func requireDuckDB(t *testing.T) {
	t.Helper()
	if duckdbPath() != "" {
		return
	}
	if os.Getenv(envRequireDuckDB) == "1" {
		t.Fatalf("%s=1 and duckdb is not on PATH or in ~/.local/bin", envRequireDuckDB)
	}
	t.Skipf("duckdb not installed; set %s=1 to make this a failure", envRequireDuckDB)
}

type result struct {
	out  string
	code int
}

func cleanEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REPORT_PLANT=") || strings.HasPrefix(kv, "VERBOSE=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func runReport(t *testing.T, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot(t), "scripts", "report.sh")}, args...)...)
	cmd.Env = cleanEnv(env)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	r := result{out: buf.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func extract(t *testing.T, tarball, dest string) {
	t.Helper()
	f, err := os.Open(tarball)
	must(t, err)
	defer f.Close()
	zr, err := gzip.NewReader(f)
	must(t, err)
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		must(t, err)
		name := filepath.Clean(h.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			t.Fatalf("bad name %q in %s", h.Name, tarball)
		}
		p := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			must(t, os.MkdirAll(p, 0o755))
		case tar.TypeReg:
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
			b, err := io.ReadAll(tr)
			must(t, err)
			must(t, os.WriteFile(p, b, 0o644))
		}
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	must(t, filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	}))
}

func testdata(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	must(t, err)
	return p
}

const loadRun = "load-errors/42/1764201600000000000"

func e2eRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	extract(t, testdata(t, "e2e-runs.tar.gz"), root)
	return root
}

func cloneRun(t *testing.T, srcDir, dstDir, oldID, newID, scenario string, seed int64) {
	t.Helper()
	must(t, os.MkdirAll(dstDir, 0o755))
	for _, f := range []string{"metrics.csv", "histograms.csv"} {
		b, err := os.ReadFile(filepath.Join(srcDir, f))
		must(t, err)
		b = bytes.ReplaceAll(b, []byte(","+oldID+","), []byte(","+newID+","))
		must(t, os.WriteFile(filepath.Join(dstDir, f), b, 0o644))
	}
	b, err := os.ReadFile(filepath.Join(srcDir, "run.csv"))
	must(t, err)
	rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	must(t, err)
	if len(rows) != 2 || len(rows[1]) != 11 || rows[1][1] != oldID {
		t.Fatalf("%s/run.csv: unexpected rows %q", srcDir, rows)
	}
	rows[1][1], rows[1][2], rows[1][4] = newID, scenario, strconv.FormatInt(seed, 10)
	var out bytes.Buffer
	w := csv.NewWriter(&out)
	must(t, w.WriteAll(rows))
	must(t, os.WriteFile(filepath.Join(dstDir, "run.csv"), out.Bytes(), 0o644))
}

func recordedRoot(t *testing.T) string {
	t.Helper()
	root := e2eRoot(t)
	copyTree(t, testdata(t, "load-errors"), filepath.Join(root, "load-errors"))
	cloneRun(t, filepath.Join(root, "e2e-reset-error", "1", "base"), filepath.Join(root, "demo-no-sql", "1", "base"),
		"e2e-reset-error/1/base", "demo-no-sql/1/base", "demo-no-sql", 1)
	return root
}

func replaceLine(t *testing.T, file, old, new string) {
	t.Helper()
	b, err := os.ReadFile(file)
	must(t, err)
	if n := bytes.Count(b, []byte(old+"\n")); n != 1 {
		t.Fatalf("%s: %d lines %q, want 1", file, n, old)
	}
	must(t, os.WriteFile(file, bytes.Replace(b, []byte(old+"\n"), []byte(new+"\n"), 1), 0o644))
}

const (
	plantedShedOld = "1,e2e-queue-full/2/base,1,3000215352,sim-1,engine,replies,counter,,,shed,,3"
	plantedShedNew = "1,e2e-queue-full/2/base,1,3000215352,sim-1,engine,replies,counter,,,shed,,2"
	plantedSentOld = "1," + loadRun + ",1,2000500000,loadgen-1,loadgen,sent,counter,,,,,198"
	plantedSentNew = "1," + loadRun + ",1,2000500000,loadgen-1,loadgen,sent,counter,,,,,197"
)

func plantedRoot(t *testing.T) string {
	t.Helper()
	root := recordedRoot(t)
	replaceLine(t, filepath.Join(root, "e2e-queue-full", "2", "base", "metrics.csv"), plantedShedOld, plantedShedNew)
	replaceLine(t, filepath.Join(root, loadRun, "metrics.csv"), plantedSentOld, plantedSentNew)
	return root
}

func golden(t *testing.T, name, got string, write bool) error {
	t.Helper()
	p := testdata(t, name)
	if write {
		must(t, os.WriteFile(p, []byte(got), 0o644))
		return nil
	}
	want, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("golden %s: %v (run with -update or MG_REPORT_UPDATE=1)", name, err)
	}
	if string(want) != got {
		return fmt.Errorf("output differs from %s (run with -update or MG_REPORT_UPDATE=1 after checking it):\n%s", name, firstDiff(string(want), got))
	}
	return nil
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\n want %q\n  got %q", i+1, a, b)
		}
	}
	return "(no line differs)"
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func section(out, from, to string) string {
	i := strings.Index(out, "\n"+from)
	if i < 0 {
		return ""
	}
	rest := out[i+1:]
	if j := strings.Index(rest, "\n"+to); j >= 0 {
		return rest[:j]
	}
	return rest
}

const (
	secAssert = "5. Failed assertions, in full"
	secOracle = "6. Oracle mismatches, in full"
	secVerdct = "7. Verdict"
)

var verdictLine = regexp.MustCompile(`^VERDICT: (PASS \(\d+ checks|FAIL \(\d+ of \d+ checks failed) over \d+ runs?(; \d+ scenarios? (has|have) no assertions)?\)$`)

func checkVerdict(r result) error {
	l := lastLine(r.out)
	if !verdictLine.MatchString(l) {
		return fmt.Errorf("last line %q is not a verdict line", l)
	}
	if n := strings.Count(r.out, "\nVERDICT: "); n != 1 {
		return fmt.Errorf("%d verdict lines", n)
	}
	if strings.HasPrefix(l, "VERDICT: PASS") != (r.code == 0) {
		return fmt.Errorf("verdict %q with exit status %d", l, r.code)
	}
	return nil
}

func plantedCheck(t *testing.T, env []string, write bool) error {
	t.Helper()
	r := runReport(t, env, plantedRoot(t))
	if r.code != 1 {
		return fmt.Errorf("exit status %d, want 1\n%s", r.code, r.out)
	}
	if err := checkVerdict(r); err != nil {
		return err
	}
	if l := lastLine(r.out); l != "VERDICT: FAIL (4 of 36 checks failed over 14 runs; 1 scenario has no assertions)" {
		return fmt.Errorf("verdict line %q", l)
	}
	assert, oracle := section(r.out, secAssert, secOracle), section(r.out, secOracle, secVerdct)
	for _, want := range []string{
		"- e2e-queue-full/faults.sql (test/analysis/e2e-queue-full/faults.sql) over 3 runs\n" +
			"  claim: e2e-queue-full: more clients than connections plus queue: exactly the three over the bound were shed (53M01).\n" +
			"  1 violating rows:\n" +
			"    e2e-queue-full/2/base: row 2, shed 3\n",
		"    " + loadRun + " loadgen-1: intended 200, sent 197, could_not_send 2, queued 0\n",
		"    " + loadRun + " loadgen-1: sent 197, completed 198, in_flight 0\n",
	} {
		if !strings.Contains(assert, want) {
			return fmt.Errorf("section 5 lacks %q:\n%s", want, assert)
		}
	}
	want := "    e2e-queue-full/2/base: shed 2, c53m01 3, unsupported 0, c0a000 0, after_read 0, c08006 0, after_write 0, c08007 0\n"
	if !strings.Contains(oracle, "- e2e-queue-full/rows.sql ") || !strings.Contains(oracle, want) {
		return fmt.Errorf("section 6 lacks the rows.sql mismatch %q:\n%s", want, oracle)
	}
	for _, row := range []string{"e2e-queue-full/faults.sql", "e2e-queue-full/rows.sql", "load-errors/report:loadgen-answered", "load-errors/report:loadgen-conservation"} {
		if !regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta(row) + ` .*\| FAIL +\|$`).MatchString(r.out) {
			return fmt.Errorf("verdict table has no FAIL row for %s", row)
		}
	}
	return golden(t, "planted.golden", r.out, write)
}

func TestReportRecordedRunsGolden(t *testing.T) {
	requireDuckDB(t)
	if testing.Short() {
		t.Skip("reads 14 recorded runs twice; run without -short")
	}
	t.Parallel()
	r := runReport(t, nil, recordedRoot(t))
	if r.code != 0 {
		t.Fatalf("exit status %d\n%s", r.code, r.out)
	}
	if err := checkVerdict(r); err != nil {
		t.Fatal(err)
	}
	if l := lastLine(r.out); l != "VERDICT: PASS (36 checks over 14 runs; 1 scenario has no assertions)" {
		t.Fatalf("verdict line %q", l)
	}
	for _, want := range []string{
		"1. What the runs prove\n", "2. How the data was collected\n", "3. Inputs\n",
		"4. Coverage by scenario, statement class and outcome\n", secAssert + "\n\nNone.\n", secOracle + "\n\nNone.\n", secVerdct + "\n",
		"| demo-no-sql ", "no assertions |",
		"| e2e-queue-full  | shed                 | 9     | 3 of 3 |",
		"| load-errors | achieved on time (sent - late)             | 195   | 1 of 1 |",
	} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("output lacks %q\n%s", want, r.out)
		}
	}
	if strings.Contains(r.out, os.TempDir()) || strings.Contains(r.out, repoRoot(t)) {
		t.Fatalf("output names an absolute path\n%s", r.out)
	}
	again := runReport(t, nil, recordedRoot(t))
	if again.out != r.out || again.code != r.code {
		t.Fatalf("a second report over a copy of the same runs differs:\n%s", firstDiff(r.out, again.out))
	}
	if err := golden(t, "recorded.golden", r.out, updating()); err != nil {
		t.Fatal(err)
	}
}

func TestReportPlantedFailureAppearsInFull(t *testing.T) {
	requireDuckDB(t)
	heavy(t)
	t.Parallel()
	if err := plantedCheck(t, nil, updating()); err != nil {
		t.Fatal(err)
	}
}

func TestReportPlantCanaryIsCaught(t *testing.T) {
	requireDuckDB(t)
	heavy(t)
	t.Parallel()
	err := plantedCheck(t, []string{"REPORT_PLANT=ignore-failures"}, false)
	if err == nil {
		t.Fatal("with REPORT_PLANT=ignore-failures the planted-failure check still passed: it cannot see a verdict that ignores failures")
	}
	t.Logf("the planted bug was caught: %v", strings.SplitN(err.Error(), "\n", 2)[0])
}

func TestReportEmptyDirectoryFails(t *testing.T) {
	requireDuckDB(t)
	t.Parallel()
	r := runReport(t, nil, t.TempDir())
	if r.code != 1 {
		t.Fatalf("exit status %d\n%s", r.code, r.out)
	}
	if err := checkVerdict(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out, "| structure: runs ") || !strings.Contains(r.out, "no metrics.csv, histograms.csv or run.csv at or below any argument") {
		t.Fatalf("no failed runs check\n%s", r.out)
	}
	if err := golden(t, "empty.golden", r.out, updating()); err != nil {
		t.Fatal(err)
	}
}

func TestReportTruncatedRunFails(t *testing.T) {
	requireDuckDB(t)
	heavy(t)
	t.Parallel()
	root := t.TempDir()
	extract(t, testdata(t, "e2e-runs.tar.gz"), root)
	for _, sc := range []string{"e2e-reject-list", "e2e-reset-error", "e2e-results"} {
		must(t, os.RemoveAll(filepath.Join(root, sc)))
	}
	must(t, os.Remove(filepath.Join(root, "e2e-queue-full", "3", "base", "run.csv")))
	r := runReport(t, nil, root)
	if r.code != 1 {
		t.Fatalf("exit status %d\n%s", r.code, r.out)
	}
	if err := checkVerdict(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(section(r.out, secAssert, secOracle), "    e2e-queue-full/3/base: no run.csv (the run did not finish)\n") {
		t.Fatalf("the truncated run is not listed\n%s", r.out)
	}
	if err := golden(t, "truncated.golden", r.out, updating()); err != nil {
		t.Fatal(err)
	}
}

func TestReportFailsClosed(t *testing.T) {
	requireDuckDB(t)
	heavy(t)
	cases := []struct {
		name  string
		edit  func(t *testing.T, root string) []string
		check string
		want  string
	}{
		{"missing-histograms", func(t *testing.T, root string) []string {
			must(t, os.Remove(filepath.Join(root, "e2e-queue-full", "1", "base", "histograms.csv")))
			return []string{root}
		}, "structure: files", "e2e-queue-full/1/base: missing histograms.csv"},
		{"unknown-schema-header", func(t *testing.T, root string) []string {
			p := filepath.Join(root, "e2e-queue-full", "2", "base", "run.csv")
			b, err := os.ReadFile(p)
			must(t, err)
			must(t, os.WriteFile(p, bytes.Replace(b, []byte("duration_ns\n"), []byte("duration_ns,extra\n"), 1), 0o644))
			return []string{root}
		}, "structure: schema header", "e2e-queue-full/2/base/run.csv: the header is not schema version 1"},
		{"schema-version-2-rows", func(t *testing.T, root string) []string {
			p := filepath.Join(root, "e2e-queue-full", "1", "base", "metrics.csv")
			b, err := os.ReadFile(p)
			must(t, err)
			must(t, os.WriteFile(p, bytes.ReplaceAll(b, []byte("\n1,e2e-queue-full/"), []byte("\n2,e2e-queue-full/")), 0o644))
			return []string{root}
		}, "structure: schema guard", "unknown schema version 2 in metrics.csv of run e2e-queue-full/1/base"},
		{"required-metric-missing", func(t *testing.T, root string) []string {
			p := filepath.Join(root, "e2e-queue-full", "1", "base", "metrics.csv")
			b, err := os.ReadFile(p)
			must(t, err)
			var keep []string
			for _, l := range strings.Split(string(b), "\n") {
				if !strings.Contains(l, ",engine,requests_shed,") {
					keep = append(keep, l)
				}
			}
			must(t, os.WriteFile(p, []byte(strings.Join(keep, "\n")), 0o644))
			return []string{root}
		}, "e2e-queue-full: required metrics", "engine.requests_shed: no rows in the final flush of 1 of 3 runs"},
		{"argument-not-a-directory", func(t *testing.T, root string) []string {
			return []string{root, filepath.Join(root, "no-such-dir")}
		}, "structure: arguments", "argument 2 is not a directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			extract(t, testdata(t, "e2e-runs.tar.gz"), root)
			for _, sc := range []string{"e2e-reject-list", "e2e-reset-error", "e2e-results"} {
				must(t, os.RemoveAll(filepath.Join(root, sc)))
			}
			r := runReport(t, nil, c.edit(t, root)...)
			if r.code != 1 {
				t.Fatalf("exit status %d\n%s", r.code, r.out)
			}
			if err := checkVerdict(r); err != nil {
				t.Fatal(err)
			}
			if !regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta(c.check) + ` .*\| FAIL +\|$`).MatchString(r.out) {
				t.Fatalf("no FAIL row for %q\n%s", c.check, r.out)
			}
			if !strings.Contains(section(r.out, secAssert, secOracle), c.want) {
				t.Fatalf("section 5 lacks %q\n%s", c.want, r.out)
			}
		})
	}
}

func TestReportPrintsEveryViolatingRow(t *testing.T) {
	requireDuckDB(t)
	heavy(t)
	t.Parallel()
	src := e2eRoot(t)
	root := t.TempDir()
	const n = 25
	for seed := int64(1); seed <= n; seed++ {
		id := fmt.Sprintf("e2e-queue-full/%d/base", seed)
		dir := filepath.Join(root, "e2e-queue-full", strconv.FormatInt(seed, 10), "base")
		cloneRun(t, filepath.Join(src, "e2e-queue-full", "2", "base"), dir, "e2e-queue-full/2/base", id, "e2e-queue-full", seed)
		replaceLine(t, filepath.Join(dir, "metrics.csv"), strings.Replace(plantedShedOld, "/2/", fmt.Sprintf("/%d/", seed), 1), strings.Replace(plantedShedNew, "/2/", fmt.Sprintf("/%d/", seed), 1))
	}
	r := runReport(t, nil, root)
	if r.code != 1 {
		t.Fatalf("exit status %d\n%s", r.code, r.out)
	}
	assert := section(r.out, secAssert, secOracle)
	if !strings.Contains(assert, fmt.Sprintf("  %d violating rows:\n", n)) {
		t.Fatalf("section 5 does not count %d rows\n%s", n, assert)
	}
	for seed := 1; seed <= n; seed++ {
		if row := fmt.Sprintf("    e2e-queue-full/%d/base: row 2, shed 3\n", seed); !strings.Contains(assert, row) {
			t.Fatalf("row %q is missing: rows are cut\n%s", row, assert)
		}
	}
}

func syntheticRoot(t *testing.T, seeds int) string {
	t.Helper()
	src := e2eRoot(t)
	root := t.TempDir()
	if seeds == 3 {
		copyTree(t, filepath.Join(src, "e2e-queue-full"), filepath.Join(root, "e2e-queue-full"))
	} else {
		for seed := int64(1); seed <= int64(seeds); seed++ {
			cloneRun(t, filepath.Join(src, "e2e-queue-full", "1", "base"),
				filepath.Join(root, "e2e-queue-full", strconv.FormatInt(seed, 10), "base"),
				"e2e-queue-full/1/base", fmt.Sprintf("e2e-queue-full/%d/base", seed), "e2e-queue-full", seed)
		}
	}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		id := fmt.Sprintf("load-errors/%d/1764201600000000000", seed)
		cloneRun(t, testdata(t, loadRun), filepath.Join(root, filepath.FromSlash(id)), loadRun, id, "load-errors", seed)
	}
	return root
}

var heading = regexp.MustCompile(`^(# |\d+\. |\d+\.\d+ )`)

func shape(out string) (int, []string) {
	var keys []string
	for _, l := range strings.Split(out, "\n") {
		if heading.MatchString(l) {
			keys = append(keys, l)
		}
	}
	for _, l := range strings.Split(section(out, secVerdct, "VERDICT:"), "\n") {
		if f := strings.Split(l, "|"); strings.HasPrefix(l, "| ") && len(f) > 2 {
			keys = append(keys, "verdict row: "+strings.TrimSpace(f[1])+" "+strings.TrimSpace(f[len(f)-2]))
		}
	}
	return strings.Count(out, "\n"), keys
}

func TestReportReadsTheSameAt3And300Seeds(t *testing.T) {
	requireDuckDB(t)
	if testing.Short() {
		t.Skip("writes and reads 600 runs; run without -short")
	}
	t.Parallel()
	small := runReport(t, nil, syntheticRoot(t, 3))
	large := runReport(t, nil, syntheticRoot(t, 300))
	if small.code != 0 || large.code != 0 {
		t.Fatalf("exit status %d and %d\n%s\n%s", small.code, large.code, small.out, large.out)
	}
	ns, hs := shape(small.out)
	nl, hl := shape(large.out)
	if ns != nl {
		t.Fatalf("%d lines at 3 seeds, %d at 300", ns, nl)
	}
	if strings.Join(hs, "\n") != strings.Join(hl, "\n") {
		t.Fatalf("sections or verdict rows differ:\n%s\n---\n%s", strings.Join(hs, "\n"), strings.Join(hl, "\n"))
	}
	if lastLine(small.out) != "VERDICT: PASS (19 checks over 6 runs)" || lastLine(large.out) != "VERDICT: PASS (19 checks over 600 runs)" {
		t.Fatalf("verdicts %q and %q", lastLine(small.out), lastLine(large.out))
	}
	if !strings.Contains(large.out, "| e2e-queue-full | S    | 300  | 1-300 | base |") {
		t.Fatalf("the inputs row does not aggregate 300 runs\n%s", large.out)
	}
}

func TestReportWithoutDuckDBFails(t *testing.T) {
	t.Parallel()
	r := runReport(t, []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}, t.TempDir())
	if r.code != 1 || !strings.Contains(r.out, "| missing (not on PATH or in ~/.local/bin) |") {
		t.Fatalf("exit status %d\n%s", r.code, r.out)
	}
	if err := checkVerdict(r); err != nil {
		t.Fatal(err)
	}
}

func TestReportWrongDuckDBVersionFails(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	must(t, os.WriteFile(filepath.Join(bin, "duckdb"), []byte("#!/bin/sh\necho 'v2.0.0 (Future) 0000'\n"), 0o755))
	r := runReport(t, []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}, testdata(t, "load-errors"))
	if r.code != 1 || !strings.Contains(r.out, "version 'v2.0.0', not 1.5.x") {
		t.Fatalf("exit status %d\n%s", r.code, r.out)
	}
	if err := checkVerdict(r); err != nil {
		t.Fatal(err)
	}
}

func TestReportUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"-x"}, {t.TempDir(), "--bogus"}} {
		r := runReport(t, nil, args...)
		if r.code != 2 || regexp.MustCompile(`(?m)^VERDICT:`).MatchString(r.out) || !strings.Contains(r.out, "Usage: scripts/report.sh") {
			t.Fatalf("%q: exit status %d\n%s", args, r.code, r.out)
		}
	}
}

func TestMakeReport(t *testing.T) {
	requireDuckDB(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	if testing.Short() {
		t.Skip("runs the report and the tier S e2e scenarios; run without -short")
	}
	t.Parallel()
	mk := func(args ...string) result {
		cmd := exec.Command("make", append([]string{"-s", "-C", repoRoot(t), "report"}, args...)...)
		cmd.Env = cleanEnv(nil)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		r := result{out: buf.String()}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			r.code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := mk(); r.code != 2 || !strings.Contains(r.out, "usage: make report") {
		t.Fatalf("make report: exit status %d\n%s", r.code, r.out)
	}
	root := testdata(t, "load-errors")
	direct := runReport(t, nil, root)
	viaMake := mk("RUNS=" + root)
	if viaMake.code != 0 || viaMake.out != direct.out {
		t.Fatalf("make report RUNS=: exit status %d, output differs from scripts/report.sh:\n%s", viaMake.code, firstDiff(direct.out, viaMake.out))
	}
	seeded := mk("SEEDS=1")
	if seeded.code != 0 || !strings.HasPrefix(seeded.out, "report: tier S e2e scenarios at 1 seeds into /tmp/report.") {
		t.Fatalf("make report SEEDS=1: exit status %d\n%s", seeded.code, seeded.out)
	}
	if l := lastLine(seeded.out); !regexp.MustCompile(`^VERDICT: PASS \(\d+ checks over \d+ runs\)$`).MatchString(l) || !strings.Contains(seeded.out, "| e2e-results ") {
		t.Fatalf("make report SEEDS=1: %q\n%s", l, seeded.out)
	}
	if m := regexp.MustCompile(`into (/tmp/report\.[A-Za-z0-9]+)/runs`).FindStringSubmatch(seeded.out); m != nil {
		_ = os.RemoveAll(m[1])
	}
}
