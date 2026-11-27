package adv_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func root(t *testing.T) string {
	t.Helper()
	p, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root(t), p))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0755); e != nil {
		t.Fatal(e)
	}
}
func command(t *testing.T, env []string, name string, args ...string) (string, int) {
	t.Helper()
	limit := 45 * time.Second
	if os.Getenv("ADV_LONG") == "1" && strings.HasSuffix(name, "/go") {
		limit = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = root(t)
	c.Env = append(os.Environ(), env...)
	b, e := c.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("command timed out: %s %v", name, args)
	}
	if e == nil {
		return string(b), 0
	}
	if x, ok := e.(*exec.ExitError); ok {
		return string(b), x.ExitCode()
	}
	t.Fatal(e)
	return "", -1
}

const mh = "schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value\n"
const hh = "schema_version,run_id,flush_seq,t_ns,instance,component,metric,tenant,outcome,reason,mode,unit,bucket_lo,bucket_hi,count\n"
const rh = "schema_version,run_id,scenario,tier,seed,arm,instances,build,config_fingerprint,flushes,duration_ns\n"

func fixture(t *testing.T, dir, id string, flushes int, broken bool) {
	t.Helper()
	sent := 3
	if broken {
		sent = 2
	}
	var b strings.Builder
	b.WriteString(mh)
	for _, m := range []struct {
		n string
		v int
	}{{"intended", 3}, {"sent", sent}, {"could_not_send", 0}, {"queued", 0}, {"in_flight", 0}} {
		fmt.Fprintf(&b, "1,%s,1,1,loadgen,loadgen,%s,counter,,,,,%d\n", id, m.n, m.v)
	}
	for _, o := range []string{"ok", "db-error", "refused", "lost"} {
		v := 0
		if o == "ok" {
			v = sent
		}
		fmt.Fprintf(&b, "1,%s,1,1,loadgen,loadgen,completed,counter,,%s,,,%d\n", id, o, v)
	}
	write(t, filepath.Join(dir, "metrics.csv"), b.String())
	write(t, filepath.Join(dir, "histograms.csv"), hh)
	write(t, filepath.Join(dir, "run.csv"), rh+fmt.Sprintf("1,%s,load-adversarial,R,%d,adv,1,test,fp,%d,1\n", id, seed, flushes))
}
func report(t *testing.T, dirs ...string) (string, int) {
	t.Helper()
	t.Logf("seed=%d", seed)
	return command(t, []string{"REPORT_PLANT=", "LC_ALL=C"}, "bash", append([]string{"scripts/report.sh"}, dirs...)...)
}
func TestAdv_10_EmptySiblingRun(t *testing.T) {
	d := t.TempDir()
	fixture(t, filepath.Join(d, "good"), "good", 1, false)
	if e := os.Mkdir(filepath.Join(d, "aborted-run"), 0755); e != nil {
		t.Fatal(e)
	}
	out, rc := report(t, filepath.Join(d, "good"), filepath.Join(d, "aborted-run"))
	if rc == 0 {
		t.Fatalf("explicit empty sibling run silently ignored; %s", last(out))
	}
}
func TestAdv_10_WholeFinalFlushMissing(t *testing.T) {
	d := t.TempDir()
	fixture(t, d, "truncated", 2, false)
	out, rc := report(t, d)
	if rc == 0 {
		t.Fatalf("run declares 2 flushes but only flush 1 exists: %s", last(out))
	}
}
func TestAdv_10_FailureInMiddleRun(t *testing.T) {
	d := t.TempDir()
	for i := 0; i < 3; i++ {
		fixture(t, filepath.Join(d, fmt.Sprint(i)), fmt.Sprintf("case-%d", i), 1, i == 1)
	}
	out, rc := report(t, d)
	if rc == 0 || !strings.Contains(out, "case-1 loadgen: intended 3, sent 2, could_not_send 0, queued 0") {
		t.Fatalf("failed middle assertion not reported in full rc=%d output=%s", rc, out)
	}
}
func last(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
func TestAdv_11_RepeatValidReports(t *testing.T) {
	d := t.TempDir()
	fixture(t, d, "repeat", 1, false)
	a, rc := report(t, d)
	b, rc2 := report(t, d)
	if rc != 0 || rc2 != 0 || a != b {
		t.Fatalf("valid repeat differs or fails rc=%d/%d\n%s", rc, rc2, a)
	}
}
func TestAdv_11_RepeatMalformedReports(t *testing.T) {
	d := t.TempDir()
	fixture(t, d, "bad", 1, false)
	write(t, filepath.Join(d, "metrics.csv"), mh+"not,a,valid,row\n")
	a, rc := report(t, d)
	b, rc2 := report(t, d)
	if rc == 0 || rc2 == 0 {
		t.Fatal("malformed data passed")
	}
	if a != b {
		t.Fatalf("same malformed run yields unstable output\nfirst=%s\nsecond=%s", a, b)
	}
}
func TestAdv_11_DirectoryEnumerationOrder(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, i := range []int{2, 0, 1} {
		fixture(t, filepath.Join(a, fmt.Sprint(i)), fmt.Sprint(i), 1, false)
	}
	for _, i := range []int{1, 0, 2} {
		fixture(t, filepath.Join(b, fmt.Sprint(i)), fmt.Sprint(i), 1, false)
	}
	x, rc := report(t, a)
	y, rc2 := report(t, b)
	if rc != 0 || rc2 != 0 || x != y {
		t.Fatalf("same run contents creation-order dependent rc=%d/%d", rc, rc2)
	}
}
func fakeGo(t *testing.T, log string, rc int) string {
	p := filepath.Join(t.TempDir(), "go-stub")
	write(t, p, "#!/bin/bash\ncat <<'ADV_LOG'\n"+log+"\nADV_LOG\nexit "+fmt.Sprint(rc)+"\n")
	return p
}

const passing = "=== RUN   TestScenarioE2E/adv/seed=606013\n--- PASS: TestScenarioE2E/adv/seed=606013 (0.00s)\n--- PASS: TestScenarioE2E/adv (0.00s)\n--- PASS: TestScenarioE2E (0.00s)\nPASS\nok\tgithub.com/nickstrad/mini-gateway/test/e2e\t0.001s"

func TestAdv_12_ChaosProcessFailureAfterPass(t *testing.T) {
	t.Logf("seed=%d", seed)
	stub := fakeGo(t, passing+"\npanic: late background failure", 1)
	out, rc := command(t, []string{"CHAOS_GO=" + stub, "CHAOS_TIER_R=0", "RUN_DIR=" + t.TempDir(), "VERBOSE=0"}, "bash", "scripts/chaos.sh")
	if rc == 0 || !strings.Contains(out, "late background failure") {
		t.Fatalf("process failure after passed scenario hidden rc=%d out=%s", rc, out)
	}
}
func TestAdv_12_ChaosParentFailure(t *testing.T) {
	t.Logf("seed=%d", seed)
	log := strings.Replace(passing, "--- PASS: TestScenarioE2E/adv (", "--- FAIL: TestScenarioE2E/adv (", 1)
	stub := fakeGo(t, log, 1)
	out, rc := command(t, []string{"CHAOS_GO=" + stub, "CHAOS_TIER_R=0", "RUN_DIR=" + t.TempDir(), "VERBOSE=0"}, "bash", "scripts/chaos.sh")
	if rc == 0 {
		t.Fatalf("scenario cleanup failure declared green: %s", out)
	}
}
func TestAdv_12_SweepVerboseAndConcise(t *testing.T) {
	t.Logf("seed=%d", seed)
	log := strings.Replace(passing, "--- PASS: TestScenarioE2E/adv/seed=606013", "    adv.go:17: RAW-606013\n--- PASS: TestScenarioE2E/adv/seed=606013", 1)
	stub := fakeGo(t, log, 0)
	for _, v := range []string{"0", "1"} {
		out, rc := command(t, []string{"SWEEP_GO=" + stub, "SWEEP_SEEDS=1", "RUN_DIR=" + t.TempDir(), "VERBOSE=" + v}, "bash", "scripts/sweep.sh")
		if rc != 0 {
			t.Fatalf("sweep rc=%d %s", rc, out)
		}
		if strings.Contains(out, "RAW-606013") != (v == "1") {
			t.Fatalf("raw output mode %s: %s", v, out)
		}
		if v == "0" && strings.Count(out, "TestScenarioE2E/adv") != 1 {
			t.Fatalf("expected one scenario line: %s", out)
		}
	}
}
func dockerStub(t *testing.T, body string) ([]string, string) {
	d := t.TempDir()
	write(t, filepath.Join(d, "docker"), "#!/bin/bash\n"+body)
	state := t.TempDir()
	return []string{"PATH=" + d + ":" + os.Getenv("PATH"), "MG_DEV_DIR=" + state, "MG_DEV_PROJECT=mg-adv-606013"}, state
}
func TestAdv_13_DockerUnavailableFailsClosed(t *testing.T) {
	t.Logf("seed=%d", seed)
	env, _ := dockerStub(t, "echo 'daemon unreachable' >&2\nexit 1\n")
	out, rc := command(t, env, "bash", "deploy/dev/down.sh")
	if rc == 0 {
		t.Fatalf("cannot inspect or remove docker resources but cleanup reported success: %s", out)
	}
}
func TestAdv_13_RemovalFailurePropagates(t *testing.T) {
	t.Logf("seed=%d", seed)
	env, _ := dockerStub(t, "case \"$1\" in ps) echo stranded-container;exit 0;; rm) exit 1;; esac\nexit 0\n")
	out, rc := command(t, env, "bash", "deploy/dev/down.sh")
	if rc == 0 {
		t.Fatalf("container removal failure hidden: %s", out)
	}
}
func TestAdv_13_IdempotentCleanupAndLoopback(t *testing.T) {
	t.Logf("seed=%d", seed)
	env, d := dockerStub(t, "exit 0\n")
	write(t, filepath.Join(d, "csv", "keep.csv"), "evidence")
	write(t, filepath.Join(d, "bin", "mini-gateway"), "unused")
	for i := 0; i < 2; i++ {
		out, rc := command(t, env, "bash", "deploy/dev/down.sh")
		if rc != 0 {
			t.Fatalf("cleanup %d rc=%d %s", i, rc, out)
		}
	}
	if _, e := os.Stat(filepath.Join(d, "bin")); !os.IsNotExist(e) {
		t.Fatal("binary directory remains")
	}
	if b, e := os.ReadFile(filepath.Join(d, "csv", "keep.csv")); e != nil || !bytes.Equal(b, []byte("evidence")) {
		t.Fatal("CSV evidence deleted")
	}
	compose := read(t, "deploy/dev/docker-compose.yml")
	for _, s := range []string{"127.0.0.1:${MG_DEV_PG_PORT", "--web.listen-address=127.0.0.1:"} {
		if !strings.Contains(compose, s) {
			t.Fatalf("loopback binding absent: %s", s)
		}
	}
}
