package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const stubGo = `#!/bin/bash
dir= name=
while [ $# -gt 0 ]; do
  case "$1" in
    -fuzz) name=${2#^}; name=${name%$}; shift ;;
    ./*) dir=${1#./} ;;
  esac
  shift
done
key=$(tr / _ <<<"$dir")__$name
n=0
if [ -f "$STUB_COUNTS/$key.n" ]; then
  n=$(cat "$STUB_COUNTS/$key.n")
fi
n=$((n + 1))
printf '%s\n' "$n" > "$STUB_COUNTS/$key.n"
if [ -f "$STUB_RECORDINGS/$key.$n.out" ]; then
  cat "$STUB_RECORDINGS/$key.$n.out"
  exit $(cat "$STUB_RECORDINGS/$key.$n.rc" 2>/dev/null || echo 0)
fi
echo "fuzz: elapsed: 0s, gathering baseline coverage: 0/3 completed"
if [ -f "$STUB_RECORDINGS/$key.out" ]; then cat "$STUB_RECORDINGS/$key.out"; else echo "$STUB_DEFAULT"; fi
echo PASS
exit $(cat "$STUB_RECORDINGS/$key.rc" 2>/dev/null || echo 0)
`

const fastLine = "fuzz: elapsed: 1s, execs: 5000 (5000/sec), new interesting: 0 (total: 3)"

type fuzzRun struct {
	code    int
	stdout  string
	stderr  string
	rows    map[string][]string
	calls   map[string]int
	retries []string
	note    string
	last    string
}

func deadlineOut(target, rateLine string) string {
	return strings.Join([]string{
		"fuzz: elapsed: 0s, gathering baseline coverage: 0/3 completed",
		"fuzz: elapsed: 0s, gathering baseline coverage: 3/3 completed, now fuzzing with 4 workers",
		rateLine,
		"--- FAIL: " + target + " (20.83s)",
		"    context deadline exceeded",
		"FAIL",
		"exit status 1",
		"FAIL\tgithub.com/nickstrad/mini-gateway/pkg\t20.910s",
		"",
	}, "\n")
}

func passOut(rateLine string) string {
	return strings.Join([]string{
		"fuzz: elapsed: 0s, gathering baseline coverage: 0/3 completed",
		rateLine,
		"PASS",
		"",
	}, "\n")
}

func runFuzzSh(t *testing.T, recordings map[string]string) fuzzRun {
	t.Helper()
	return runFuzzShEnv(t, recordings, nil)
}

func runFuzzShEnv(t *testing.T, recordings map[string]string, extraEnv []string) fuzzRun {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "go-stub")
	rec := filepath.Join(tmp, "rec")
	counts := filepath.Join(tmp, "counts")
	report := filepath.Join(tmp, "report.tsv")
	if err := os.WriteFile(stub, []byte(stubGo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(counts, 0o755); err != nil {
		t.Fatal(err)
	}
	for key, body := range recordings {
		if err := os.WriteFile(filepath.Join(rec, key), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "fuzz.sh"))
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "FUZZ_MAX_RETRIES=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "PATH="+filepath.Join(runtime.GOROOT(), "bin")+":"+os.Getenv("PATH"),
		"FUZZ_GO="+stub, "FUZZ_SECONDS=1", "FUZZ_RATE_FLOOR=100", "FUZZ_REPORT="+report,
		"STUB_RECORDINGS="+rec, "STUB_COUNTS="+counts, "STUB_DEFAULT="+fastLine)
	env = append(env, extraEnv...)
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	run := fuzzRun{
		stdout: string(out),
		stderr: stderr.String(),
		rows:   map[string][]string{},
		calls:  map[string]int{},
	}
	if ee, ok := err.(*exec.ExitError); ok {
		run.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(run.stdout, "\n") {
		if strings.HasPrefix(l, "NOTE: ") {
			run.note = l
		}
		if strings.HasPrefix(l, "fuzz: RETRY: ") {
			run.retries = append(run.retries, l)
		}
		if l != "" {
			run.last = l
		}
	}
	entries, err := os.ReadDir(counts)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".n") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(counts, ent.Name()))
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		stem := strings.TrimSuffix(ent.Name(), ".n")
		parts := strings.SplitN(stem, "__", 2)
		if len(parts) == 2 {
			run.calls[strings.ReplaceAll(parts[0], "_", "/")+" "+parts[1]] = n
		}
	}
	if run.code == 2 {
		return run
	}
	tsv, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no report: %v\n%s\n%s", err, out, run.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(string(tsv), "\n"), "\n")
	if lines[0] != "package\ttarget\texecs\texecs_per_sec\tclass\tretried" {
		t.Fatalf("report header %q", lines[0])
	}
	for _, l := range lines[1:] {
		f := strings.Split(l, "\t")
		if len(f) != 6 {
			t.Fatalf("report row %q", l)
		}
		run.rows[f[0]+" "+f[1]] = f[2:]
	}
	return run
}

func discoveredTargets(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("..", path)
		if d.IsDir() && (rel == "tools" || rel == "agent_work" || rel == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, l := range strings.Split(string(src), "\n") {
			if name, ok := strings.CutPrefix(l, "func Fuzz"); ok {
				if i := strings.IndexByte(name, '('); i > 0 {
					out = append(out, filepath.Dir(rel)+" Fuzz"+name[:i])
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func TestFuzzShReportsEveryTargetWithItsRate(t *testing.T) {
	run := runFuzzSh(t, nil)
	if run.code != 0 {
		t.Fatalf("exit %d: %s", run.code, run.stderr)
	}
	want := discoveredTargets(t)
	if len(want) < 4 || len(run.rows) != len(want) {
		t.Fatalf("report has %d rows, the tree has %d targets", len(run.rows), len(want))
	}
	for _, k := range want {
		if r := run.rows[k]; !slices.Equal(r, []string{"5000", "5000", "fuzz", "0"}) {
			t.Fatalf("%s: %v", k, r)
		}
	}
	if !strings.Contains(run.note, " 0 seed-sweep") {
		t.Fatalf("note %q", run.note)
	}
}

func TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_sim_world__FuzzWorldSchedule.out": "fuzz: elapsed: 3s, execs: 400 (150/sec)\nfuzz: elapsed: 21s, execs: 1995 (0/sec), new interesting: 1 (total: 9)\n",
		"internal_sim_simtoy__FuzzToySchedule.out":  "fuzz: elapsed: 1m0s, execs: 6000 (0/sec), new interesting: 0 (total: 3)\n",
		"internal_testclient__FuzzSession.out":      "fuzz: elapsed: 20s, execs: 20 (1/sec)\n",
		"internal_sim__FuzzScheduleFromBytes.out":   "fuzz: elapsed: 1h0m2s, execs: 359999 (0/sec)\n",
		"internal_sim_fakepg__FuzzServerInput.out":  "fuzz: elapsed: 0s, execs: 7 (0/sec)\n",
		"internal_sim_fakepg__FuzzServerInput.rc":   "0",
		"internal_sim_world__FuzzWorldSchedule.rc":  "0",
		"internal_sim_simtoy__FuzzToySchedule.rc":   "0",
		"internal_sim__FuzzScheduleFromBytes.rc":    "0",
		"internal_testclient__FuzzSession.rc":       "0",
	})
	if run.code != 0 {
		t.Fatalf("a seed sweep failed the run: exit %d: %s", run.code, run.stderr)
	}
	for k, want := range map[string][]string{
		"internal/sim/world FuzzWorldSchedule": {"1995", "95", "seed-sweep", "0"},
		"internal/sim/simtoy FuzzToySchedule":  {"6000", "100", "fuzz", "0"},
		"internal/testclient FuzzSession":      {"20", "1", "fuzz", "0"},
		"internal/sim FuzzScheduleFromBytes":   {"359999", "99", "seed-sweep", "0"},
		"internal/sim/fakepg FuzzServerInput":  {"7", "7", "seed-sweep", "0"},
		"internal/pgwire FuzzFramer":           {"5000", "5000", "fuzz", "0"},
	} {
		if got := run.rows[k]; !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", k, got, want)
		}
	}
	if !strings.Contains(run.note, " 3 seed-sweep") {
		t.Fatalf("note %q", run.note)
	}
}

func TestFuzzShFailsClosedWithoutARate(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_sim_simtoy__FuzzToySchedule.out": "fuzz: elapsed: 3s, gathering baseline coverage: 1/3 completed\n",
	})
	if run.code != 1 || !strings.Contains(run.stderr, "FuzzToySchedule in ./internal/sim/simtoy: no execution rate") {
		t.Fatalf("exit %d: %s", run.code, run.stderr)
	}
	if got := run.rows["internal/sim/simtoy FuzzToySchedule"]; !slices.Equal(got, []string{"-", "-", "unreadable", "0"}) {
		t.Fatalf("row %v", got)
	}
	if got := run.rows["internal/sim/world FuzzWorldSchedule"]; !slices.Equal(got, []string{"5000", "5000", "fuzz", "0"}) {
		t.Fatalf("the other targets were not reported: %v", got)
	}
}

func TestFuzzShFailsWhenATargetFails(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_sim_world__FuzzWorldSchedule.out": "--- FAIL: FuzzWorldSchedule\nfuzz: elapsed: 2s, execs: 300 (150/sec)\n",
		"internal_sim_world__FuzzWorldSchedule.rc":  "1",
	})
	if run.code != 1 || !strings.Contains(run.stderr, "FuzzWorldSchedule in ./internal/sim/world FAILED") {
		t.Fatalf("exit %d: %s", run.code, run.stderr)
	}
	if got := run.rows["internal/sim/world FuzzWorldSchedule"]; !slices.Equal(got, []string{"300", "150", "fuzz", "0"}) {
		t.Fatalf("row %v", got)
	}
}

func TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_sim_simtoy__FuzzToySchedule.out": "fuzz: elapsed: 1s, execs: 300 (300/sec)\nfuzz: elapsed: corrupt, execs: unavailable\n",
	})
	if run.code != 1 || !strings.Contains(run.stderr, "FuzzToySchedule in ./internal/sim/simtoy: no execution rate") {
		t.Fatalf("a stale rate was accepted: exit %d: %s", run.code, run.stderr)
	}
	if got := run.rows["internal/sim/simtoy FuzzToySchedule"]; !slices.Equal(got, []string{"-", "-", "unreadable", "0"}) {
		t.Fatalf("row %v", got)
	}
}

func TestFuzzShNamesEveryFailedTargetOnItsLastLine(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_sim_world__FuzzWorldSchedule.out": "fuzz: elapsed: 2s, execs: 300 (150/sec)\n--- FAIL: FuzzWorldSchedule\n",
		"internal_sim_world__FuzzWorldSchedule.rc":  "1",
		"internal_sim_simtoy__FuzzToySchedule.out":  "fuzz: elapsed: 3s, gathering baseline coverage: 1/3 completed\n",
	})
	if run.code != 1 {
		t.Fatalf("exit %d: %s", run.code, run.stderr)
	}
	if want := "fuzz: FAILED: internal/sim/simtoy FuzzToySchedule, internal/sim/world FuzzWorldSchedule"; run.last != want {
		t.Fatalf("last line %q, want %q", run.last, want)
	}
	if !strings.HasSuffix(run.note, " 1 without a rate, 0 unclassified, 0 retried") {
		t.Fatalf("note %q", run.note)
	}
}

func TestFuzzShPrintsNoFailedLineWhenGreen(t *testing.T) {
	run := runFuzzSh(t, nil)
	if run.code != 0 || strings.Contains(run.last, "FAILED") || !strings.HasSuffix(run.note, " 0 without a rate, 0 unclassified, 0 retried") {
		t.Fatalf("exit %d, last %q, note %q", run.code, run.last, run.note)
	}
}

// Proves a first run matching the deadline-only signature is retried once,
// reported via a RETRY line, retried=1, and the NOTE count, and uses the second
// run's execution rate.
func TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_sim_world__FuzzWorldSchedule.1.out": deadlineOut("FuzzWorldSchedule", "fuzz: elapsed: 20s, execs: 200 (0/sec), new interesting: 0 (total: 3)"),
		"internal_sim_world__FuzzWorldSchedule.1.rc":  "1",
		"internal_sim_world__FuzzWorldSchedule.2.out": passOut("fuzz: elapsed: 20s, execs: 4000 (200/sec), new interesting: 1 (total: 4)"),
		"internal_sim_world__FuzzWorldSchedule.2.rc":  "0",
	})
	if run.code != 0 {
		t.Fatalf("exit %d: %s\n%s", run.code, run.stderr, run.stdout)
	}
	if got := run.calls["internal/sim/world FuzzWorldSchedule"]; got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	wantRetry := []string{"fuzz: RETRY: internal/sim/world FuzzWorldSchedule: go's own deadline, no failing input"}
	if !slices.Equal(run.retries, wantRetry) {
		t.Fatalf("retries = %v, want %v", run.retries, wantRetry)
	}
	if got := run.rows["internal/sim/world FuzzWorldSchedule"]; !slices.Equal(got, []string{"4000", "200", "fuzz", "1"}) {
		t.Fatalf("row = %v, want [4000 200 fuzz 1]", got)
	}
	if got := run.rows["internal/pgwire FuzzFramer"]; !slices.Equal(got, []string{"5000", "5000", "fuzz", "0"}) {
		t.Fatalf("other row = %v", got)
	}
	if !strings.HasSuffix(run.note, ", 1 retried") {
		t.Fatalf("note = %q", run.note)
	}
	if strings.Contains(run.last, "FAILED") {
		t.Fatalf("unexpected failed line %q", run.last)
	}
}

// Proves a target whose retry also exits with the deadline signature is not
// retried a second time, fails the run, and reports retried=1 with the second
// run's rate.
func TestFuzzShFailsWhenDeadlineSignatureRepeats(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_pgwire__FuzzFramer.1.out": deadlineOut("FuzzFramer", "fuzz: elapsed: 20s, execs: 1000 (0/sec)"),
		"internal_pgwire__FuzzFramer.1.rc":  "1",
		"internal_pgwire__FuzzFramer.2.out": deadlineOut("FuzzFramer", "fuzz: elapsed: 20s, execs: 2400 (0/sec)"),
		"internal_pgwire__FuzzFramer.2.rc":  "1",
		"internal_pgwire__FuzzFramer.3.out": passOut("fuzz: elapsed: 1s, execs: 5000 (5000/sec)"),
		"internal_pgwire__FuzzFramer.3.rc":  "0",
	})
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	if got := run.calls["internal/pgwire FuzzFramer"]; got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	if len(run.retries) != 1 {
		t.Fatalf("retries = %v, want 1", run.retries)
	}
	if got := run.rows["internal/pgwire FuzzFramer"]; !slices.Equal(got, []string{"2400", "120", "fuzz", "1"}) {
		t.Fatalf("row = %v, want [2400 120 fuzz 1]", got)
	}
	if !strings.HasSuffix(run.note, ", 1 retried") {
		t.Fatalf("note = %q", run.note)
	}
	if want := "fuzz: FAILED: internal/pgwire FuzzFramer"; run.last != want {
		t.Fatalf("last = %q, want %q", run.last, want)
	}
}

// Proves a retried target whose second run fails with any other error fails
// the run and reports the second run's rate with retried=1.
func TestFuzzShFailsWhenSecondRunFailsWithOtherMessage(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_pgwire__FuzzFramer.1.out": deadlineOut("FuzzFramer", "fuzz: elapsed: 20s, execs: 1000 (0/sec)"),
		"internal_pgwire__FuzzFramer.1.rc":  "1",
		"internal_pgwire__FuzzFramer.2.out": "fuzz: elapsed: 2s, execs: 400 (200/sec)\n--- FAIL: FuzzFramer (2.01s)\n    framer_test.go:10: boom\nFAIL\n",
		"internal_pgwire__FuzzFramer.2.rc":  "1",
	})
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	if got := run.calls["internal/pgwire FuzzFramer"]; got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	if got := run.rows["internal/pgwire FuzzFramer"]; !slices.Equal(got, []string{"400", "200", "fuzz", "1"}) {
		t.Fatalf("row = %v", got)
	}
	if want := "fuzz: FAILED: internal/pgwire FuzzFramer"; run.last != want {
		t.Fatalf("last = %q, want %q", run.last, want)
	}
}

// Proves near-miss failure logs (failing-input lines, panics, other messages,
// file:line prefixes, extra lines, wrong target or subtest headers, minimizing
// output, exit status 2) fail without retry, and a passing target logging the
// deadline text is not retried.
func TestFuzzShDoesNotRetryNearMisses(t *testing.T) {
	cases := []struct {
		name     string
		out      string
		rc       string
		wantCode int
	}{
		{
			name: "failing_input_written",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"    Failing input written to testdata/fuzz/FuzzFramer/deadbeef",
				"    To re-run:",
				"    go test -run=FuzzFramer/deadbeef",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "deadline_plus_panic_after",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"panic: boom",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "deadline_plus_panic_before",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"panic: boom",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "different_message_under_fail",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer (1.02s)",
				"    fuzzing process hung or terminated unexpectedly: exit status 2",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "file_line_prefixed_deadline",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer (1.02s)",
				"    frame_fuzz_test.go:19: context deadline exceeded",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "extra_message_under_fail",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"    context deadline exceeded",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "wrong_target_name",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzOther (1.02s)",
				"    context deadline exceeded",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "subtest_fail_block",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer/seed0 (0.01s)",
				"    context deadline exceeded",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "minimizing_before_deadline",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"fuzz: minimizing 12-byte failing input file",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"FAIL",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "exit_status_2_after_fail",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"FAIL",
				"exit status 2",
				"",
			}, "\n"),
			rc:       "1",
			wantCode: 1,
		},
		{
			name: "passing_target_with_message_in_log",
			out: strings.Join([]string{
				"fuzz: elapsed: 1s, execs: 1000 (1000/sec)",
				"    frame_fuzz_test.go:19: context deadline exceeded",
				"--- FAIL: FuzzFramer (1.02s)",
				"    context deadline exceeded",
				"PASS",
				"",
			}, "\n"),
			rc:       "0",
			wantCode: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := runFuzzSh(t, map[string]string{
				"internal_pgwire__FuzzFramer.1.out": tc.out,
				"internal_pgwire__FuzzFramer.1.rc":  tc.rc,
				"internal_pgwire__FuzzFramer.2.out": passOut("fuzz: elapsed: 1s, execs: 9000 (9000/sec)"),
				"internal_pgwire__FuzzFramer.2.rc":  "0",
			})
			if run.code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstderr: %s", run.code, tc.wantCode, run.stderr)
			}
			if got := run.calls["internal/pgwire FuzzFramer"]; got != 1 {
				t.Fatalf("calls = %d, want 1", got)
			}
			if len(run.retries) != 0 {
				t.Fatalf("unexpected retries: %v", run.retries)
			}
			if got := run.rows["internal/pgwire FuzzFramer"]; got[3] != "0" {
				t.Fatalf("retried col = %q, want 0 (row %v)", got[3], got)
			}
			if !strings.HasSuffix(run.note, ", 0 retried") {
				t.Fatalf("note = %q", run.note)
			}
		})
	}
}

func runIsolatedFuzzSh(t *testing.T, targetDir, targetName string, setup func(workRoot string), stubBody string, extraEnv []string) fuzzRun {
	t.Helper()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	fuzzSrc, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "fuzz.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "scripts", "fuzz.sh"), fuzzSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(tmp, targetDir)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fuzz_test.go"), []byte("package p\n\nimport \"testing\"\n\nfunc "+targetName+"(f *testing.F) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(tmp)
	}
	stubBin := filepath.Join(t.TempDir(), "bin")
	countsDir := filepath.Join(t.TempDir(), "counts")
	if err := os.MkdirAll(stubBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(countsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goStub := `#!/bin/bash
if [ "$1" = list ]; then
  if [ "$2" = -m ]; then
    echo example.invalid/isolated
    exit 0
  fi
  echo example.invalid/isolated/p
  exit 0
fi
dir= name=
while [ $# -gt 0 ]; do
  case "$1" in
    -fuzz) name=${2#^}; name=${name%$}; shift ;;
    ./*) dir=${1#./} ;;
  esac
  shift
done
key=$(tr / _ <<<"$dir")__$name
n=0
if [ -f "$STUB_COUNTS/$key.n" ]; then
  n=$(cat "$STUB_COUNTS/$key.n")
fi
n=$((n + 1))
printf '%s\n' "$n" > "$STUB_COUNTS/$key.n"
` + stubBody + "\n"
	if err := os.WriteFile(filepath.Join(stubBin, "go"), []byte(goStub), 0o755); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.tsv")
	cmd := exec.Command("bash", filepath.Join(tmp, "scripts", "fuzz.sh"))
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "FUZZ_") || strings.HasPrefix(e, "PATH=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"PATH="+stubBin+":"+filepath.Join(runtime.GOROOT(), "bin")+":"+os.Getenv("PATH"),
		"FUZZ_SECONDS=1",
		"FUZZ_RATE_FLOOR=100",
		"FUZZ_REPORT="+report,
		"STUB_COUNTS="+countsDir,
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	run := fuzzRun{
		stdout: string(out),
		stderr: stderr.String(),
		rows:   map[string][]string{},
		calls:  map[string]int{},
	}
	if ee, ok := err.(*exec.ExitError); ok {
		run.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(run.stdout, "\n") {
		if strings.HasPrefix(l, "NOTE: ") {
			run.note = l
		}
		if strings.HasPrefix(l, "fuzz: RETRY: ") {
			run.retries = append(run.retries, l)
		}
		if l != "" {
			run.last = l
		}
	}
	entries, err := os.ReadDir(countsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".n") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(countsDir, ent.Name()))
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		stem := strings.TrimSuffix(ent.Name(), ".n")
		parts := strings.SplitN(stem, "__", 2)
		if len(parts) == 2 {
			run.calls[strings.ReplaceAll(parts[0], "_", "/")+" "+parts[1]] = n
		}
	}
	if run.code == 2 {
		return run
	}
	tsv, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no report: %v\n%s\n%s", err, out, run.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(string(tsv), "\n"), "\n")
	if lines[0] != "package\ttarget\texecs\texecs_per_sec\tclass\tretried" {
		t.Fatalf("report header %q", lines[0])
	}
	for _, l := range lines[1:] {
		f := strings.Split(l, "\t")
		if len(f) != 6 {
			t.Fatalf("report row %q", l)
		}
		run.rows[f[0]+" "+f[1]] = f[2:]
	}
	return run
}

func isolatedDeadlineScript(target, firstAction string) string {
	return `if [ "$n" = 1 ]; then
` + firstAction + `
cat <<'EOF'
` + deadlineOut(target, "fuzz: elapsed: 2s, execs: 400 (200/sec)") + `EOF
exit 1
fi
cat <<'EOF'
` + passOut("fuzz: elapsed: 2s, execs: 4000 (2000/sec)") + `EOF
exit 0`
}

// Proves a first run that writes a new file under testdata/fuzz/<target>/ (in
// an existing or newly created corpus directory) fails without retry.
func TestFuzzShDoesNotRetryWhenNewCorpusFileAppears(t *testing.T) {
	t.Run("existing_corpus_dir", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzCorpusNew", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzCorpusNew")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(corpusDir, "seed0"), []byte("go test fuzz v1\n[]byte(\"ok\")\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzCorpusNew", `printf 'go test fuzz v1\n[]byte("boom")\n' > p/testdata/fuzz/FuzzCorpusNew/cr02-test-crasher`), nil)
		if run.code != 1 {
			t.Fatalf("exit %d, want 1", run.code)
		}
		if got := run.calls["p FuzzCorpusNew"]; got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
		if len(run.retries) != 0 {
			t.Fatalf("unexpected retries: %v", run.retries)
		}
		if got := run.rows["p FuzzCorpusNew"]; !slices.Equal(got, []string{"400", "200", "fuzz", "0"}) {
			t.Fatalf("row = %v", got)
		}
		if !strings.HasSuffix(run.note, ", 0 retried") {
			t.Fatalf("note = %q", run.note)
		}
	})

	t.Run("new_corpus_dir", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzCorpusFresh", nil, isolatedDeadlineScript("FuzzCorpusFresh", `mkdir -p p/testdata/fuzz/FuzzCorpusFresh
printf 'go test fuzz v1\n[]byte("boom")\n' > p/testdata/fuzz/FuzzCorpusFresh/cr02-test-crasher`), nil)
		if run.code != 1 {
			t.Fatalf("exit %d, want 1", run.code)
		}
		if got := run.calls["p FuzzCorpusFresh"]; got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
		if len(run.retries) != 0 {
			t.Fatalf("unexpected retries: %v", run.retries)
		}
		if got := run.rows["p FuzzCorpusFresh"]; !slices.Equal(got, []string{"400", "200", "fuzz", "0"}) {
			t.Fatalf("row = %v", got)
		}
	})
}

// Proves corpus rewrites, removals, files behind symlinked directories,
// newline-name replacements, and snapshot errors (broken symlink, symlink loop,
// non-directory corpus path) block retry, while an unchanged corpus containing
// a valid symlink still retries.
func TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors(t *testing.T) {
	t.Run("existing_file_rewritten", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(corpusDir, "known"), []byte("old seed"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", `printf 'new fail' > p/testdata/fuzz/FuzzTarget/known`), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("existing_file_removed", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(corpusDir, "known"), []byte("old seed"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", `rm p/testdata/fuzz/FuzzTarget/known`), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("new_file_behind_symlinked_dir", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget")
			storageDir := filepath.Join(workRoot, "storage")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(storageDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(storageDir, filepath.Join(corpusDir, "link")); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", `printf 'crash\n' > p/testdata/fuzz/FuzzTarget/link/new`), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("newline_directory_replacement", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			weird := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget", "a\n.", "a")
			if err := os.MkdirAll(weird, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(weird, "b"), []byte("old seed"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", `rm -rf p/testdata/fuzz/FuzzTarget/*
mkdir -p p/testdata/fuzz/FuzzTarget/a
printf 'crash\n' > p/testdata/fuzz/FuzzTarget/a/b`), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("snapshot_fails_closed_on_broken_symlink", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(workRoot, "does-not-exist"), filepath.Join(corpusDir, "dangling")); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", ""), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("snapshot_fails_closed_on_symlink_loop", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(".", filepath.Join(corpusDir, "loop")); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", ""), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("snapshot_fails_closed_on_non_dir_corpus_path", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			parent := filepath.Join(workRoot, "p", "testdata", "fuzz")
			if err := os.MkdirAll(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(parent, "FuzzTarget"), []byte("not a dir"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", ""), nil)
		if run.code != 1 || run.calls["p FuzzTarget"] != 1 || len(run.retries) != 0 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
	})

	t.Run("unchanged_corpus_with_symlink_still_retries", func(t *testing.T) {
		run := runIsolatedFuzzSh(t, "p", "FuzzTarget", func(workRoot string) {
			corpusDir := filepath.Join(workRoot, "p", "testdata", "fuzz", "FuzzTarget")
			storageDir := filepath.Join(workRoot, "storage")
			if err := os.MkdirAll(corpusDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(storageDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(corpusDir, "seed0"), []byte("seed-zero"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(storageDir, "seed1"), []byte("seed-one"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(storageDir, filepath.Join(corpusDir, "link")); err != nil {
				t.Fatal(err)
			}
		}, isolatedDeadlineScript("FuzzTarget", ""), nil)
		if run.code != 0 || run.calls["p FuzzTarget"] != 2 || len(run.retries) != 1 {
			t.Fatalf("exit=%d calls=%d retries=%v\n%s", run.code, run.calls["p FuzzTarget"], run.retries, run.stdout)
		}
		if got := run.rows["p FuzzTarget"]; !slices.Equal(got, []string{"4000", "2000", "fuzz", "1"}) {
			t.Fatalf("row = %v", got)
		}
	})
}

// Proves fuzz.sh retries at most two targets by default and fails without
// retrying a third target that hits the deadline signature.
func TestFuzzShCapsRetriesAtTwoByDefault(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_pgwire__FuzzBackendSession.1.out": deadlineOut("FuzzBackendSession", "fuzz: elapsed: 2s, execs: 200 (100/sec)"),
		"internal_pgwire__FuzzBackendSession.1.rc":  "1",
		"internal_pgwire__FuzzBackendSession.2.out": passOut("fuzz: elapsed: 2s, execs: 2000 (1000/sec)"),
		"internal_pgwire__FuzzBackendSession.2.rc":  "0",
		"internal_pgwire__FuzzClientSession.1.out":  deadlineOut("FuzzClientSession", "fuzz: elapsed: 2s, execs: 200 (100/sec)"),
		"internal_pgwire__FuzzClientSession.1.rc":   "1",
		"internal_pgwire__FuzzClientSession.2.out":  passOut("fuzz: elapsed: 2s, execs: 3000 (1500/sec)"),
		"internal_pgwire__FuzzClientSession.2.rc":   "0",
		"internal_pgwire__FuzzCycle.1.out":          deadlineOut("FuzzCycle", "fuzz: elapsed: 2s, execs: 200 (100/sec)"),
		"internal_pgwire__FuzzCycle.1.rc":           "1",
		"internal_pgwire__FuzzCycle.2.out":          passOut("fuzz: elapsed: 2s, execs: 4000 (2000/sec)"),
		"internal_pgwire__FuzzCycle.2.rc":           "0",
	})
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	if got := run.calls["internal/pgwire FuzzBackendSession"]; got != 2 {
		t.Fatalf("first target calls = %d, want 2", got)
	}
	if got := run.calls["internal/pgwire FuzzClientSession"]; got != 2 {
		t.Fatalf("second target calls = %d, want 2", got)
	}
	if got := run.calls["internal/pgwire FuzzCycle"]; got != 1 {
		t.Fatalf("third target calls = %d, want 1", got)
	}
	if len(run.retries) != 2 {
		t.Fatalf("retries = %v, want 2", run.retries)
	}
	if got := run.rows["internal/pgwire FuzzBackendSession"]; !slices.Equal(got, []string{"2000", "1000", "fuzz", "1"}) {
		t.Fatalf("first row = %v", got)
	}
	if got := run.rows["internal/pgwire FuzzClientSession"]; !slices.Equal(got, []string{"3000", "1500", "fuzz", "1"}) {
		t.Fatalf("second row = %v", got)
	}
	if got := run.rows["internal/pgwire FuzzCycle"]; !slices.Equal(got, []string{"200", "100", "fuzz", "0"}) {
		t.Fatalf("third row = %v", got)
	}
	if !strings.HasSuffix(run.note, ", 2 retried") {
		t.Fatalf("note = %q", run.note)
	}
	if want := "fuzz: FAILED: internal/pgwire FuzzCycle"; run.last != want {
		t.Fatalf("last = %q, want %q", run.last, want)
	}
}

// Proves FUZZ_MAX_RETRIES=0 disables the deadline retry so the first matching
// target fails at once.
func TestFuzzShMaxRetriesZeroDisablesRetry(t *testing.T) {
	run := runFuzzShEnv(t, map[string]string{
		"internal_pgwire__FuzzFramer.1.out": deadlineOut("FuzzFramer", "fuzz: elapsed: 2s, execs: 200 (100/sec)"),
		"internal_pgwire__FuzzFramer.1.rc":  "1",
		"internal_pgwire__FuzzFramer.2.out": passOut("fuzz: elapsed: 2s, execs: 4000 (2000/sec)"),
		"internal_pgwire__FuzzFramer.2.rc":  "0",
	}, []string{"FUZZ_MAX_RETRIES=0"})
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	if got := run.calls["internal/pgwire FuzzFramer"]; got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	if len(run.retries) != 0 {
		t.Fatalf("retries = %v, want none", run.retries)
	}
	if got := run.rows["internal/pgwire FuzzFramer"]; !slices.Equal(got, []string{"200", "100", "fuzz", "0"}) {
		t.Fatalf("row = %v", got)
	}
	if !strings.HasSuffix(run.note, ", 0 retried") {
		t.Fatalf("note = %q", run.note)
	}
}

// Proves fuzz.sh exits 2 when FUZZ_MAX_RETRIES is negative, non-numeric,
// leading-zero, or non-integer.
func TestFuzzShRejectsBadMaxRetries(t *testing.T) {
	for _, v := range []string{"-1", "abc", "01", "2.5"} {
		t.Run(v, func(t *testing.T) {
			run := runFuzzShEnv(t, nil, []string{"FUZZ_MAX_RETRIES=" + v})
			if run.code != 2 {
				t.Fatalf("FUZZ_MAX_RETRIES=%s: exit %d, want 2", v, run.code)
			}
		})
	}
}

// Proves a retried target whose second run exits 0 without a readable rate
// still fails closed as unreadable with retried=1.
func TestFuzzShRetryUnreadableSecondRunFailsClosed(t *testing.T) {
	run := runFuzzSh(t, map[string]string{
		"internal_pgwire__FuzzFramer.1.out": deadlineOut("FuzzFramer", "fuzz: elapsed: 2s, execs: 4000 (2000/sec)"),
		"internal_pgwire__FuzzFramer.1.rc":  "1",
		"internal_pgwire__FuzzFramer.2.out": "fuzz: elapsed: 3s, gathering baseline coverage: 1/3 completed\nPASS\n",
		"internal_pgwire__FuzzFramer.2.rc":  "0",
	})
	if run.code != 1 {
		t.Fatalf("exit %d, want 1", run.code)
	}
	if got := run.calls["internal/pgwire FuzzFramer"]; got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	if got := run.rows["internal/pgwire FuzzFramer"]; !slices.Equal(got, []string{"-", "-", "unreadable", "1"}) {
		t.Fatalf("row = %v, want [- - unreadable 1]", got)
	}
	if !strings.HasSuffix(run.note, " 1 without a rate, 0 unclassified, 1 retried") {
		t.Fatalf("note = %q", run.note)
	}
}

// Proves gate.sh records fuzz_retries as null when G5 does not run and exits 2
// on negative or overflowing FUZZ_MAX_RETRIES values.
func TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "gate.sh"), "cr02-fuzz-deadline-retry")
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(runtime.GOROOT(), "bin")+":"+os.Getenv("PATH"),
		"GATE_ONLY=G10",
		"GATE_OUT="+outDir,
		"FUZZ_MAX_RETRIES=3",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gate.sh failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	val, exists := res["fuzz_retries"]
	if !exists || val != nil {
		t.Fatalf("fuzz_retries = %v (exists=%v), want null", val, exists)
	}
	if res["fuzz_max_retries"] != float64(3) {
		t.Fatalf("fuzz_max_retries = %v, want 3", res["fuzz_max_retries"])
	}
	if res["counts_for_landing"] != false {
		t.Fatalf("counts_for_landing = %v, want false", res["counts_for_landing"])
	}

	for _, badVal := range []string{"-1", "18446744073709551616"} {
		bad := exec.Command("bash", filepath.Join(root, "scripts", "gate.sh"), "cr02-fuzz-deadline-retry")
		bad.Env = append(os.Environ(),
			"PATH="+filepath.Join(runtime.GOROOT(), "bin")+":"+os.Getenv("PATH"),
			"GATE_ONLY=G10",
			"GATE_OUT="+outDir,
			"FUZZ_MAX_RETRIES="+badVal,
		)
		if err := bad.Run(); err == nil {
			t.Fatalf("expected exit 2 for FUZZ_MAX_RETRIES=%s", badVal)
		} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
			t.Fatalf("FUZZ_MAX_RETRIES=%s: exit = %v, want 2", badVal, err)
		}
	}
}

type isolatedGateRepo struct {
	root    string
	stubBin string
	counts  string
}

func newIsolatedGateRepo(t *testing.T, targets map[string][]string, stubFuzzBody string) isolatedGateRepo {
	t.Helper()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stubBin := filepath.Join(t.TempDir(), "bin")
	countsDir := filepath.Join(t.TempDir(), "counts")
	if err := os.MkdirAll(stubBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(countsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile := func(rel, content string, mode os.FileMode) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	gitCmd := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	writeFile(".gitignore", "agent_work/chunks/*/gate/*.log\nagent_work/chunks/*/gate/*.tsv\n", 0o644)
	writeFile("go.mod", "module example.invalid/gatetest\n\ngo 1.26\n", 0o644)
	writeFile("tools/detlint/go.mod", "module example.invalid/gatetest/tools/detlint\n\ngo 1.26\n", 0o644)
	writeFile("tools/detlint/main.go", "package main\n\nfunc main() {}\n", 0o644)
	for _, rule := range []string{
		"det-packages.txt",
		"detlint-allow.txt",
		"imports.txt",
		"boundlint-allow.txt",
		"panic-allow.txt",
		"race-packages.txt",
	} {
		writeFile(filepath.Join("tools", "lint-rules", rule), "", 0o644)
	}
	writeFile("tools/lint-rules/shell-packages.txt", "p\n", 0o644)

	gitCmd("init")
	gitCmd("checkout", "-b", "main")
	gitCmd("add", ".")
	gitCmd("-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-m", "base")
	gitCmd("checkout", "-b", "work/test-chunk")

	for _, name := range []string{"gate.sh", "fuzz.sh"} {
		src, err := os.ReadFile(filepath.Join(repoRoot, "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(filepath.Join("scripts", name), string(src), 0o755)
	}
	for _, name := range []string{"sweep.sh", "tier-r.sh", "csv-assert.sh", "gen-docs.sh", "counted-growth.sh"} {
		writeFile(filepath.Join("scripts", name), "#!/bin/bash\nexit 0\n", 0o755)
	}
	writeFile("scripts/site.sh", "#!/bin/bash\nmkdir -p \"$1/book\"\necho ok > \"$1/book/index.html\"\necho book/index.html > \"$1/pages.txt\"\n", 0o755)

	for pkg, funcs := range targets {
		var sb strings.Builder
		sb.WriteString("package " + filepath.Base(pkg) + "\n\nimport \"testing\"\n")
		for _, fn := range funcs {
			sb.WriteString("\nfunc " + fn + "(f *testing.F) {}\n")
		}
		writeFile(filepath.Join(pkg, "fuzz_test.go"), sb.String(), 0o644)
	}

	gitCmd("add", ".")
	gitCmd("-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-m", "chunk")

	goStub := `#!/bin/bash
case "$1" in
  version)
    echo 'go version go1.26.8 linux/amd64'
    exit 0
    ;;
  build)
    if [ "$2" = -o ]; then
      out=$3
      mkdir -p "$out"
      for tool in detlint importlint boundlint paniccheck nocomments astdiff; do
        printf '#!/bin/bash\nexit 0\n' > "$out/$tool"
        chmod +x "$out/$tool"
      done
    fi
    exit 0
    ;;
  vet|mod)
    exit 0
    ;;
  list)
    if [ "$2" = -m ]; then
      echo example.invalid/gatetest
      exit 0
    fi
    if [ "$2" = -deps ]; then
      pkg=${4#./}
      echo "example.invalid/gatetest/$pkg"
      if [ "$pkg" = simpkg ]; then
        echo example.invalid/gatetest/internal/sim
      fi
      exit 0
    fi
    echo example.invalid/gatetest/p
    exit 0
    ;;
  test)
    fuzz=0
    dir= name=
    while [ $# -gt 0 ]; do
      case "$1" in
        -fuzz) fuzz=1; name=${2#^}; name=${name%$}; shift ;;
        ./*) dir=${1#./} ;;
      esac
      shift
    done
    if [ "$fuzz" = 0 ]; then
      exit 0
    fi
    key=$(tr / _ <<<"$dir")__$name
    n=0
    if [ -f "$STUB_COUNTS/$key.n" ]; then
      n=$(cat "$STUB_COUNTS/$key.n")
    fi
    n=$((n + 1))
    printf '%s\n' "$n" > "$STUB_COUNTS/$key.n"
` + stubFuzzBody + `
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(stubBin, "go"), []byte(goStub), 0o755); err != nil {
		t.Fatal(err)
	}
	return isolatedGateRepo{root: dir, stubBin: stubBin, counts: countsDir}
}

func (g isolatedGateRepo) runGate(t *testing.T, extraEnv ...string) (int, string, map[string]any) {
	t.Helper()
	entries, err := os.ReadDir(g.counts)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if err := os.Remove(filepath.Join(g.counts, ent.Name())); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(g.root, "scripts", "gate.sh"), "test-chunk")
	cmd.Dir = g.root
	env := []string{}
	for _, e := range os.Environ() {
		k := strings.SplitN(e, "=", 2)[0]
		if strings.HasPrefix(k, "GATE_") || strings.HasPrefix(k, "FUZZ_") || strings.HasPrefix(k, "SWEEP_") || k == "PATH" || k == "GOFLAGS" || k == "GOWORK" {
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"PATH="+g.stubBin+":"+filepath.Join(runtime.GOROOT(), "bin")+":"+os.Getenv("PATH"),
		"STUB_COUNTS="+g.counts,
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(g.root, "agent_work", "chunks", "test-chunk", "gate", "result.json"))
	if err != nil {
		t.Fatalf("missing result.json (exit=%d): %v\n%s", code, err, out)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return code, string(out), res
}

// Proves gate.sh with all other landing prerequisites satisfied sets
// counts_for_landing to true for FUZZ_MAX_RETRIES 0..2 and false for 3 or
// higher.
func TestGateShLandingCapBlocksWidenedMaxRetriesAlone(t *testing.T) {
	repo := newIsolatedGateRepo(t, map[string][]string{
		"p": {"FuzzAlpha"},
	}, `echo "fuzz: elapsed: 20s, execs: 40000 (2000/sec), new interesting: 0 (total: 1)"
echo PASS
exit 0`)

	cases := []struct {
		maxRetries string
		wantVal    float64
		wantCounts bool
	}{
		{maxRetries: "0", wantVal: 0, wantCounts: true},
		{maxRetries: "1", wantVal: 1, wantCounts: true},
		{maxRetries: "2", wantVal: 2, wantCounts: true},
		{maxRetries: "3", wantVal: 3, wantCounts: false},
		{maxRetries: "999999999", wantVal: 999999999, wantCounts: false},
	}

	for _, tc := range cases {
		t.Run("FUZZ_MAX_RETRIES="+tc.maxRetries, func(t *testing.T) {
			code, out, res := repo.runGate(t, "FUZZ_MAX_RETRIES="+tc.maxRetries)
			if code != 0 {
				t.Fatalf("gate.sh exit %d:\n%s", code, out)
			}
			if res["overall"] != "pass" || res["partial"] != false || res["dirty_tree"] != false || res["short"] != false {
				t.Fatalf("unexpected prerequisites in result.json: %v\n%s", res, out)
			}
			if res["fuzz_max_retries"] != tc.wantVal {
				t.Fatalf("fuzz_max_retries = %v, want %v", res["fuzz_max_retries"], tc.wantVal)
			}
			if res["counts_for_landing"] != tc.wantCounts {
				t.Fatalf("FUZZ_MAX_RETRIES=%s: counts_for_landing = %v, want %v\n%s", tc.maxRetries, res["counts_for_landing"], tc.wantCounts, out)
			}
		})
	}
}

// Proves gate.sh counts fuzz_retries from column 6 of G5-fuzz.tsv so RETRY
// lines, TSV retried flags, NOTE count, and result.json agree across distinct
// target, seed-sweep, and retry totals.
func TestGateShCountsFuzzRetriesFromColumnSix(t *testing.T) {
	repo := newIsolatedGateRepo(t, map[string][]string{
		"p":      {"FuzzAlpha", "FuzzBeta", "FuzzGamma"},
		"simpkg": {"FuzzSimSlow"},
	}, `if [ "$name" = FuzzAlpha ] || [ "$name" = FuzzBeta ]; then
  if [ "$n" = 1 ]; then
    echo "fuzz: elapsed: 20s, execs: 1000 (0/sec)"
    echo "--- FAIL: $name (20.50s)"
    echo "    context deadline exceeded"
    echo "FAIL"
    exit 1
  fi
  echo "fuzz: elapsed: 20s, execs: 4000 (200/sec)"
  echo PASS
  exit 0
fi
if [ "$name" = FuzzSimSlow ]; then
  echo "fuzz: elapsed: 20s, execs: 1000 (50/sec)"
  echo PASS
  exit 0
fi
echo "fuzz: elapsed: 20s, execs: 6000 (300/sec)"
echo PASS
exit 0`)

	code, out, res := repo.runGate(t)
	if code != 0 {
		t.Fatalf("gate.sh exit %d:\n%s", code, out)
	}
	if res["overall"] != "pass" || res["counts_for_landing"] != true {
		t.Fatalf("overall=%v counts_for_landing=%v\n%s", res["overall"], res["counts_for_landing"], out)
	}
	if res["fuzz_targets"] != float64(4) {
		t.Fatalf("fuzz_targets = %v, want 4", res["fuzz_targets"])
	}
	if res["fuzz_seed_sweeps"] != float64(1) {
		t.Fatalf("fuzz_seed_sweeps = %v, want 1", res["fuzz_seed_sweeps"])
	}
	if res["fuzz_retries"] != float64(2) {
		t.Fatalf("fuzz_retries = %v, want 2", res["fuzz_retries"])
	}

	gateDir := filepath.Join(repo.root, "agent_work", "chunks", "test-chunk", "gate")
	tsvRaw, err := os.ReadFile(filepath.Join(gateDir, "G5-fuzz.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(tsvRaw), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("tsv lines = %d, want 5:\n%s", len(lines), tsvRaw)
	}
	tsvRetries := 0
	for _, l := range lines[1:] {
		cols := strings.Split(l, "\t")
		if len(cols) != 6 {
			t.Fatalf("row %q has %d cols, want 6", l, len(cols))
		}
		if cols[5] == "1" {
			tsvRetries++
		}
	}
	if tsvRetries != 2 {
		t.Fatalf("tsv retried count = %d, want 2\n%s", tsvRetries, tsvRaw)
	}

	logRaw, err := os.ReadFile(filepath.Join(gateDir, "G5-fuzz.log"))
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logRaw)
	if got := strings.Count(logStr, "fuzz: RETRY:"); got != 2 {
		t.Fatalf("RETRY line count = %d, want 2\n%s", got, logStr)
	}
	if !strings.Contains(logStr, ", 2 retried") {
		t.Fatalf("NOTE line missing ', 2 retried':\n%s", logStr)
	}
}
