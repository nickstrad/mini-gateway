package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
echo "fuzz: elapsed: 0s, gathering baseline coverage: 0/3 completed"
if [ -f "$STUB_RECORDINGS/$key.out" ]; then cat "$STUB_RECORDINGS/$key.out"; else echo "$STUB_DEFAULT"; fi
echo PASS
exit $(cat "$STUB_RECORDINGS/$key.rc" 2>/dev/null || echo 0)
`

const fastLine = "fuzz: elapsed: 1s, execs: 5000 (5000/sec), new interesting: 0 (total: 3)"

type fuzzRun struct {
	code   int
	stderr string
	rows   map[string][]string
	note   string
	last   string
}

func runFuzzSh(t *testing.T, recordings map[string]string) fuzzRun {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "go-stub")
	rec := filepath.Join(tmp, "rec")
	report := filepath.Join(tmp, "report.tsv")
	if err := os.WriteFile(stub, []byte(stubGo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	for key, body := range recordings {
		if err := os.WriteFile(filepath.Join(rec, key), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "fuzz.sh"))
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(runtime.GOROOT(), "bin")+":"+os.Getenv("PATH"),
		"FUZZ_GO="+stub, "FUZZ_SECONDS=1", "FUZZ_RATE_FLOOR=100", "FUZZ_REPORT="+report,
		"STUB_RECORDINGS="+rec, "STUB_DEFAULT="+fastLine)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	run := fuzzRun{stderr: stderr.String(), rows: map[string][]string{}}
	if ee, ok := err.(*exec.ExitError); ok {
		run.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "NOTE: ") {
			run.note = l
		}
		if l != "" {
			run.last = l
		}
	}
	tsv, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no report: %v\n%s\n%s", err, out, run.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(string(tsv), "\n"), "\n")
	if lines[0] != "package\ttarget\texecs\texecs_per_sec\tclass" {
		t.Fatalf("report header %q", lines[0])
	}
	for _, l := range lines[1:] {
		f := strings.Split(l, "\t")
		if len(f) != 5 {
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
		if r := run.rows[k]; !slices.Equal(r, []string{"5000", "5000", "fuzz"}) {
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
		"internal/sim/world FuzzWorldSchedule": {"1995", "95", "seed-sweep"},
		"internal/sim/simtoy FuzzToySchedule":  {"6000", "100", "fuzz"},
		"internal/testclient FuzzSession":      {"20", "1", "fuzz"},
		"internal/sim FuzzScheduleFromBytes":   {"359999", "99", "seed-sweep"},
		"internal/sim/fakepg FuzzServerInput":  {"7", "7", "seed-sweep"},
		"internal/pgwire FuzzFramer":           {"5000", "5000", "fuzz"},
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
	if got := run.rows["internal/sim/simtoy FuzzToySchedule"]; !slices.Equal(got, []string{"-", "-", "unreadable"}) {
		t.Fatalf("row %v", got)
	}
	if got := run.rows["internal/sim/world FuzzWorldSchedule"]; !slices.Equal(got, []string{"5000", "5000", "fuzz"}) {
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
	if got := run.rows["internal/sim/world FuzzWorldSchedule"]; !slices.Equal(got, []string{"300", "150", "fuzz"}) {
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
	if got := run.rows["internal/sim/simtoy FuzzToySchedule"]; !slices.Equal(got, []string{"-", "-", "unreadable"}) {
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
	if !strings.HasSuffix(run.note, " 1 without a rate, 0 unclassified") {
		t.Fatalf("note %q", run.note)
	}
}

func TestFuzzShPrintsNoFailedLineWhenGreen(t *testing.T) {
	run := runFuzzSh(t, nil)
	if run.code != 0 || strings.Contains(run.last, "FAILED") || !strings.HasSuffix(run.note, " 0 without a rate, 0 unclassified") {
		t.Fatalf("exit %d, last %q, note %q", run.code, run.last, run.note)
	}
}
