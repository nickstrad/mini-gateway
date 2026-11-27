package adv_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const seed = 20261127

type fixture struct {
	t   *testing.T
	dir string
}
type result struct {
	out  string
	log  string
	code int
	rows [][]string
	meta map[string]any
}

func put(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0755); e != nil {
		t.Fatal(e)
	}
}
func fresh(t *testing.T, n int, body string) *fixture {
	t.Helper()
	t.Logf("seed=%d", seed)
	d, e := os.MkdirTemp(".", ".fixture-")
	if e != nil {
		t.Fatal(e)
	}
	d, _ = filepath.Abs(d)
	t.Cleanup(func() { os.RemoveAll(d) })
	f := &fixture{t, d}
	for _, s := range []string{"fuzz.sh", "gate.sh"} {
		b, e := os.ReadFile("../../../scripts/" + s)
		if e != nil {
			t.Fatal(e)
		}
		put(t, filepath.Join(d, "scripts", s), string(b))
	}
	os.MkdirAll(filepath.Join(d, "tools/detlint"), 0755)
	for i := 0; i < n; i++ {
		put(t, filepath.Join(d, "p", fmt.Sprintf("x%d_test.go", i)), fmt.Sprintf("package p\nfunc FuzzA%d(f *testing.F) {}\n", i))
	}
	stub := `#!/bin/bash
if [ "$1" = version ]; then echo 'go version fixture'; exit 0; fi
if [ "$1" = build ]; then exit 0; fi
if [ "$1" = list ]; then
 if [ "$2" = -m ]; then echo example.invalid/adv; exit 0; fi
 if [ "${ADV_DEPS:-}" = fail ]; then exit 1; fi
 echo example.invalid/adv/p
 [ "${ADV_SIM:-}" != 1 ] || echo example.invalid/adv/internal/sim
 exit 0
fi
[ "$1" = test ] || exit 90
name=${5#^}; name=${name%\$}
n=0; [ ! -f "$name.count" ] || n=$(cat "$name.count")
n=$((n+1)); echo "$n" > "$name.count"
good() { echo 'fuzz: elapsed: 2s, execs: 246 (0/sec), new interesting: 0 (total: 1)'; echo PASS; }
deadline() { echo 'fuzz: elapsed: 2s, execs: 800 (0/sec)'; echo "--- FAIL: $name (2.01s)"; echo '    context deadline exceeded'; echo FAIL; }
` + body + "\n"
	put(t, filepath.Join(d, "bin/go"), stub)
	return f
}
func (f *fixture) run(gate bool, extra ...string) result {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	script := "scripts/fuzz.sh"
	args := []string{script}
	if gate {
		args = []string{"scripts/gate.sh", "adv"}
	}
	c := exec.CommandContext(ctx, "bash", args...)
	c.Dir = f.dir
	for _, v := range os.Environ() {
		k := strings.SplitN(v, "=", 2)[0]
		if strings.HasPrefix(k, "FUZZ_") || strings.HasPrefix(k, "GATE_") || strings.HasPrefix(k, "ADV_") || k == "PATH" || k == "GOFLAGS" || k == "GOWORK" {
			continue
		}
		c.Env = append(c.Env, v)
	}
	c.Env = append(c.Env, "PATH="+f.dir+"/bin:/usr/local/go/bin:/usr/bin:/bin", "FUZZ_REPORT="+f.dir+"/report.tsv", "GATE_ONLY=G5")
	c.Env = append(c.Env, extra...)
	b, e := c.CombinedOutput()
	r := result{out: string(b), log: string(b)}
	if e != nil {
		r.code = 1
		if x, ok := e.(*exec.ExitError); ok {
			r.code = x.ExitCode()
		}
	}
	if ctx.Err() != nil {
		f.t.Fatalf("fixture timeout: %s", b)
	}
	report := filepath.Join(f.dir, "report.tsv")
	if gate {
		report = filepath.Join(f.dir, "agent_work/chunks/adv/gate/G5-fuzz.tsv")
		b, _ := os.ReadFile(filepath.Join(f.dir, "agent_work/chunks/adv/gate/result.json"))
		if len(b) > 0 {
			if e := json.Unmarshal(b, &r.meta); e != nil {
				f.t.Fatal(e)
			}
		}
		b, _ = os.ReadFile(filepath.Join(f.dir, "agent_work/chunks/adv/gate/G5-fuzz.log"))
		r.log = string(b)
		r.out += "\n" + string(b)
	}
	b, _ = os.ReadFile(report)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for _, line := range lines[1:] {
		r.rows = append(r.rows, strings.Split(line, "\t"))
	}
	return r
}
func (f *fixture) calls(i int) int {
	b, _ := os.ReadFile(filepath.Join(f.dir, fmt.Sprintf("FuzzA%d.count", i)))
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}
func reject(t *testing.T, f *fixture, r result, want int) {
	t.Helper()
	if r.code == 0 || f.calls(0) != want {
		t.Fatalf("expected failure and %d invocations; exit=%d calls=%d\n%s", want, r.code, f.calls(0), r.out)
	}
}

const retryOK = `if [ "$n" = 1 ]; then deadline; exit 1; fi; good`

func TestAdv_1_ControlBytesAndExtraBlocks(t *testing.T) {
	for _, suffix := range []string{"\\r", "\\033[0m", "\\n--- FAIL: FuzzSibling (0.01s)\\n    invariant violated"} {
		t.Run(fmt.Sprintf("%q", suffix), func(t *testing.T) {
			f := fresh(t, 1, "if [ \"$n\" = 1 ]; then deadline; printf '"+suffix+"\\n'; exit 1; fi; good")
			reject(t, f, f.run(false), 1)
		})
	}
}
func TestAdv_1_ExistingCorpusRewritten(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then echo 'new failing input' > p/testdata/fuzz/FuzzA0/known; deadline; exit 1; fi; good`)
	put(t, filepath.Join(f.dir, "p/testdata/fuzz/FuzzA0/known"), "old seed")
	reject(t, f, f.run(false), 1)
}
func TestAdv_1_SymlinkedCorpusChild(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then echo crash > p/testdata/fuzz/FuzzA0/link/new; deadline; exit 1; fi; good`)
	os.MkdirAll(filepath.Join(f.dir, "p/testdata/fuzz/FuzzA0"), 0755)
	os.MkdirAll(filepath.Join(f.dir, "storage"), 0755)
	if e := os.Symlink(filepath.Join(f.dir, "storage"), filepath.Join(f.dir, "p/testdata/fuzz/FuzzA0/link")); e != nil {
		t.Fatal(e)
	}
	reject(t, f, f.run(false), 1)
}
func TestAdv_1_NewCorpusWithHostileFilename(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then mkdir -p p/testdata/fuzz/FuzzA0; printf crash > $'p/testdata/fuzz/FuzzA0/line\nbreak'; deadline; exit 1; fi; good`)
	reject(t, f, f.run(false), 1)
}
func TestAdv_2_BudgetAcrossSevenTargets(t *testing.T) {
	f := fresh(t, 7, retryOK)
	r := f.run(false)
	if r.code == 0 {
		t.Fatal("seven deadline targets passed default budget")
	}
	for i := 0; i < 7; i++ {
		want := 1
		if i < 2 {
			want = 2
		}
		if f.calls(i) != want {
			t.Fatalf("target %d calls=%d want=%d", i, f.calls(i), want)
		}
	}
}
func TestAdv_2_IndependentProcessesResetBudget(t *testing.T) {
	for i := 0; i < 3; i++ {
		f := fresh(t, 2, retryOK)
		r := f.run(false)
		if r.code != 0 || f.calls(0) != 2 || f.calls(1) != 2 {
			t.Fatalf("process %d did not get independent two-target budget: %s", i, r.out)
		}
	}
}
func TestAdv_2_WidenedStandaloneBudget(t *testing.T) {
	f := fresh(t, 4, retryOK)
	r := f.run(false, "FUZZ_MAX_RETRIES=3")
	total := 0
	for i := 0; i < 4; i++ {
		total += f.calls(i) - 1
	}
	if total > 2 {
		t.Fatalf("claim says at most two targets per run; observed %d retries with FUZZ_MAX_RETRIES=3, exit=%d", total, r.code)
	}
}
func TestAdv_3_SecondExitWithoutOutput(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then deadline; exit 1; fi; exit 17`)
	reject(t, f, f.run(false), 2)
}
func TestAdv_3_SecondSignalAfterGoodProgress(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then deadline; exit 1; fi; good; kill -TERM $$`)
	reject(t, f, f.run(false), 2)
}
func TestAdv_3_SecondExitAllStatuses(t *testing.T) {
	for _, code := range []int{2, 3, 64, 124, 137, 255} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := fresh(t, 1, fmt.Sprintf(`if [ "$n" = 1 ]; then deadline; exit 1; fi; good; exit %d`, code))
			reject(t, f, f.run(false), 2)
		})
	}
}
func visible(t *testing.T, r result, n int) {
	t.Helper()
	sum := 0
	for _, row := range r.rows {
		if len(row) != 6 {
			t.Fatalf("bad row %q", row)
		}
		if row[5] == "1" {
			sum++
		}
	}
	if sum != n || strings.Count(r.log, "fuzz: RETRY:") != n || !strings.Contains(r.log, fmt.Sprintf(", %d retried", n)) {
		t.Fatalf("retry visibility: sum=%d want=%d\n%s", sum, n, r.out)
	}
	if r.meta == nil || r.meta["fuzz_retries"] != float64(n) {
		t.Fatalf("gate retries=%v want=%d", r.meta["fuzz_retries"], n)
	}
}
func TestAdv_4_GateCountsWhenRetryClassificationFails(t *testing.T) {
	f := fresh(t, 2, retryOK)
	r := f.run(true, "ADV_DEPS=fail")
	visible(t, r, 2)
	if r.code == 0 {
		t.Fatal("dependency failure passed")
	}
	if strings.Count(r.out, "fuzz: RETRY:") < 2 {
		t.Fatal("retry lines missing")
	}
}
func TestAdv_4_MixedFailedAndRecoveredTargets(t *testing.T) {
	f := fresh(t, 3, `if [ "$n" = 1 ]; then deadline; exit 1; fi; if [ "$name" = FuzzA0 ]; then exit 7; fi; good`)
	r := f.run(true)
	visible(t, r, 2)
	if r.code == 0 || r.meta["overall"] != "fail" {
		t.Fatalf("mixed failures passed: %v", r.meta)
	}
}
func TestAdv_4_SkippedGateDiscardsStaleCounts(t *testing.T) {
	f := fresh(t, 1, retryOK)
	visible(t, f.run(true), 1)
	r := f.run(true, "GATE_ONLY=G10")
	if r.code != 0 || r.meta["fuzz_retries"] != nil {
		t.Fatalf("skipped G5 retained retries: exit=%d result=%v", r.code, r.meta)
	}
}
func TestAdv_5_RetryCrossesFloorBothDirections(t *testing.T) {
	for _, n := range []int{198, 200, 202} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			f := fresh(t, 1, fmt.Sprintf(`if [ "$n" = 1 ]; then deadline; exit 1; fi; echo 'fuzz: elapsed: 2s, execs: %d (999999/sec)'`, n))
			r := f.run(false, "ADV_SIM=1")
			want := "fuzz"
			if n < 200 {
				want = "seed-sweep"
			}
			if r.code != 0 || len(r.rows) != 1 || strings.Join(r.rows[0][:5], "|") != fmt.Sprintf("p|FuzzA0|%d|%d|%s", n, n/2, want) {
				t.Fatalf("wrong second-run rate/class: %v exit=%d", r.rows, r.code)
			}
		})
	}
}
func TestAdv_5_HourDurationAndZeroSeconds(t *testing.T) {
	for _, v := range []struct{ dur, execs, rate string }{{"1h2m3s", "372300", "100"}, {"0s", "99", "99"}, {"1m0s", "6000", "100"}} {
		f := fresh(t, 1, fmt.Sprintf("echo 'fuzz: elapsed: %s, execs: %s (0/sec)'", v.dur, v.execs))
		r := f.run(false)
		if r.code != 0 || len(r.rows) != 1 || r.rows[0][3] != v.rate {
			t.Fatalf("duration %s: %v exit=%d", v.dur, r.rows, r.code)
		}
	}
}
func TestAdv_5_LastProgressCannotBorrowFirstAttempt(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then deadline; exit 1; fi; echo 'fuzz: elapsed: 0s, gathering baseline coverage: 1/2 completed'; echo PASS`)
	r := f.run(false)
	reject(t, f, r, 2)
	if len(r.rows) != 1 || r.rows[0][4] != "unreadable" {
		t.Fatalf("borrowed first rate: %v", r.rows)
	}
}
func TestAdv_6_MaximumAndOverflowEnvironment(t *testing.T) {
	for _, v := range []string{"999999999", "1000000000", "18446744073709551618", "-1", "02", "2\n", "2+0"} {
		f := fresh(t, 1, retryOK)
		r := f.run(true, "GATE_ONLY=G10", "FUZZ_MAX_RETRIES="+v)
		if v == "999999999" {
			if r.meta["counts_for_landing"] != false || r.meta["fuzz_max_retries"] != float64(999999999) {
				t.Fatalf("wide budget result %v", r.meta)
			}
		} else if r.code != 2 || r.meta != nil {
			t.Fatalf("invalid budget %q exit=%d result=%v", v, r.code, r.meta)
		}
	}
}
func TestAdv_6_GateDropsInjectedFuzzCommand(t *testing.T) {
	f := fresh(t, 1, retryOK)
	put(t, filepath.Join(f.dir, "injected"), "#!/bin/sh\ntouch INJECTED\nexit 0\n")
	r := f.run(true, "FUZZ_GO="+f.dir+"/injected")
	if _, e := os.Stat(filepath.Join(f.dir, "INJECTED")); e == nil {
		t.Fatal("gate ran FUZZ_GO")
	}
	if r.code != 0 || f.calls(0) != 2 {
		t.Fatalf("real PATH tool not used: %s", r.out)
	}
	visible(t, r, 1)
}
func TestAdv_6_LandingPredicateBudgetBoundary(t *testing.T) {
	t.Logf("seed=%d", seed)
	b, e := os.ReadFile("../../../scripts/gate.sh")
	if e != nil {
		t.Fatal(e)
	}
	re := regexp.MustCompile(`(?s)if \[ "\$overall" = pass \].*?then counts=true; fi`)
	s := re.FindString(string(b))
	if s == "" {
		t.Fatal("landing predicate not found")
	}
	for _, n := range []string{"0", "1", "2", "3", "999999999"} {
		c := exec.Command("bash", "-c", `overall=pass; partial=false; dirty=false; sha=fixture; short=false; custom_out=false; sweep_seeds=200; fuzz_seconds=20; fuzz_floor=100; counts=false; fuzz_max_retries=$1; `+s+`; echo "$counts"`, "predicate", n)
		b, e := c.CombinedOutput()
		want := "false"
		if n == "0" || n == "1" || n == "2" {
			want = "true"
		}
		if e != nil || strings.TrimSpace(string(b)) != want {
			t.Fatalf("budget=%s landing=%q want=%s err=%v", n, b, want, e)
		}
	}
}
func TestAdv_1_SeededLogMutationCampaign(t *testing.T) {
	count := 12
	if os.Getenv("ADV_LONG") == "1" {
		count = 250
	}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < count; i++ {
		suffix := fmt.Sprintf("unexpected-diagnostic-%016x", rng.Uint64())
		f := fresh(t, 1, "if [ \"$n\" = 1 ]; then deadline; echo '"+suffix+"'; exit 1; fi; good")
		reject(t, f, f.run(false), 1)
	}
}

func TestAdv_1_NewlineDirectoryReplacement(t *testing.T) {
	f := fresh(t, 1, `if [ "$n" = 1 ]; then rm -rf p/testdata/fuzz/FuzzA0/*; mkdir -p p/testdata/fuzz/FuzzA0/a; echo crash > p/testdata/fuzz/FuzzA0/a/b; deadline; exit 1; fi; good`)
	put(t, filepath.Join(f.dir, "p/testdata/fuzz/FuzzA0/a\n.", "a", "b"), "old seed")
	reject(t, f, f.run(false), 1)
}
