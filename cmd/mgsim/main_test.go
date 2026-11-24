package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim/simtoy"
)

const childEnv = "MGSIM_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func runInProcess(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, simtoy.Run)
	return code, out.String(), errb.String()
}

func runChild(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("mgsim %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestListNamesEveryScenario(t *testing.T) {
	code, out, _ := runInProcess("list")
	if code != 0 || strings.Count(out, "\n") != len(simtoy.Scenarios()) || !strings.Contains(out, "echo-reset\n") {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestRunPrintsDigestsAndReplayLine(t *testing.T) {
	code, out, _ := runInProcess("run", "echo-stall", "-seed", "7")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	sc, _ := simtoy.Lookup("echo-stall")
	d := sc.Run(7).Sim.Digests()
	for _, want := range []string{"trace-digest: " + d.Trace.String(), "state-digest: " + d.State.String(), "REPLAY: go run ./cmd/mgsim run echo-stall -seed 7"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	code2, out2, _ := runInProcess("run", "-seed", "7", "echo-stall")
	if code2 != 0 || out2 != out {
		t.Fatal("flag order changed the result")
	}
}

func TestCrossProcessReplay(t *testing.T) {
	for _, sc := range simtoy.Scenarios() {
		a := runChild(t, "run", sc.Name, "-seed", "3")
		b := runChild(t, "run", sc.Name, "-seed", "3")
		if a != b {
			t.Fatalf("%s: two processes disagree:\n%s\n%s", sc.Name, a, b)
		}
		_, in, _ := runInProcess("run", sc.Name, "-seed", "3")
		if in != a {
			t.Fatalf("%s: subprocess and in-process disagree", sc.Name)
		}
	}
	if runChild(t, "run", "echo-clean", "-seed", "3") == runChild(t, "run", "echo-clean", "-seed", "4") {
		t.Fatal("different seeds gave one output")
	}
}

func TestTraceFlagPrintsRecords(t *testing.T) {
	_, out, _ := runInProcess("run", "echo-reset", "-trace")
	if !strings.Contains(out, " fault ") || !strings.Contains(out, " net.rst ") {
		t.Fatalf("trace output missing records:\n%s", out[:min(len(out), 400)])
	}
}

func TestFailureExitsOneWithReplay(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"run", "echo-clean", "-seed", "9"}, &out, &errb, func(cfg simtoy.Config) *simtoy.Result {
		cfg.MaxSteps = 10
		return simtoy.Run(cfg)
	})
	if code != 1 || !strings.Contains(errb.String(), "REPLAY: go run ./cmd/mgsim run echo-clean -seed 9") || !strings.Contains(out.String(), "status=fail") {
		t.Fatalf("code %d\n%s\n%s", code, out.String(), errb.String())
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"bogus"}, {"run", "nope"}, {"run", "echo-clean", "extra"}, {"run", "-seed", "x", "echo-clean"}} {
		if code, _, _ := runInProcess(args...); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
	if code, out, _ := runInProcess("help"); code != 0 || !strings.Contains(out, "mgsim run") {
		t.Fatal("help")
	}
}
