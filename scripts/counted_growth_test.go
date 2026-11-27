package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const countedStub = `#!/bin/bash
printf '%s\n' "$*" > "$STUB_ARGS"
cat "$STUB_OUT"
exit "$STUB_RC"
`

var countedRequired = []string{"TestCountedWorkGrowsLinearly", "TestCountedCheckRejectsWorkThatDoesNotGrowLinearly", "TestLexOperatorRunsGrowLinearly", "TestClassifierSpanKeywordChecksGrowLinearly", "TestClassifierWorkGrowsLinearly"}

type countedRun struct {
	code int
	args string
	out  string
}

func runCountedGrowth(t *testing.T, output string, rc string) countedRun {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "go-stub")
	args := filepath.Join(tmp, "args")
	canned := filepath.Join(tmp, "out")
	if err := os.WriteFile(stub, []byte(countedStub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canned, []byte(output), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "counted-growth.sh"))
	cmd.Env = append(os.Environ(), "COUNTED_GO="+stub, "STUB_ARGS="+args, "STUB_OUT="+canned, "STUB_RC="+rc)
	out, err := cmd.CombinedOutput()
	run := countedRun{out: string(out)}
	if ee, ok := err.(*exec.ExitError); ok {
		run.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatalf("the script did not run go: %v\n%s", err, out)
	}
	run.args = strings.TrimSpace(string(b))
	return run
}

func countedPassLines(names []string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("=== RUN   " + n + "\n--- PASS: " + n + " (0.01s)\n")
	}
	b.WriteString("PASS\nok  \tgithub.com/nickstrad/mini-gateway/internal/pgwire\t0.5s\n")
	return b.String()
}

func TestCountedGrowthRunsTheTaggedTests(t *testing.T) {
	run := runCountedGrowth(t, countedPassLines(countedRequired), "0")
	if run.code != 0 {
		t.Fatalf("exit %d on a green run:\n%s", run.code, run.out)
	}
	if want := "test -tags clscount -count=1 -run Linearly -v ./internal/pgwire/"; run.args != want {
		t.Fatalf("go was run as %q, want %q", run.args, want)
	}
}

func TestCountedGrowthFailsWhenTheRunFails(t *testing.T) {
	run := runCountedGrowth(t, countedPassLines(countedRequired)+"--- FAIL: TestCountedWorkGrowsLinearly (0.1s)\nFAIL\n", "1")
	if run.code != 1 || !strings.Contains(run.out, "the tagged run failed") {
		t.Fatalf("exit %d, want 1 with the reason:\n%s", run.code, run.out)
	}
}

func TestCountedGrowthFailsWhenNoTestRuns(t *testing.T) {
	run := runCountedGrowth(t, "testing: warning: no tests to run\nPASS\nok  \tgithub.com/nickstrad/mini-gateway/internal/pgwire\t0.01s [no tests to run]\n", "0")
	if run.code != 1 || !strings.Contains(run.out, "did not pass in the tagged run") {
		t.Fatalf("exit %d, want 1 when no test ran:\n%s", run.code, run.out)
	}
}

func TestCountedGrowthFailsWhenTheCountedTestIsMissing(t *testing.T) {
	for _, gone := range countedRequired {
		var rest []string
		for _, n := range countedRequired {
			if n != gone {
				rest = append(rest, n)
			}
		}
		run := runCountedGrowth(t, countedPassLines(rest), "0")
		if run.code != 1 || !strings.Contains(run.out, gone+" did not pass") {
			t.Errorf("%s missing: exit %d, want 1 naming it:\n%s", gone, run.code, run.out)
		}
	}
}

func TestGateAndCIRunCountedGrowthInG3(t *testing.T) {
	gate, err := os.ReadFile("gate.sh")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)\nif want G3; then\n(.*?)\nfi\n`).FindSubmatch(gate)
	if block == nil || !regexp.MustCompile(`(?m)^\s*check G3 counted g3_counted$`).Match(block[1]) {
		t.Fatal("gate.sh: the G3 block does not run `check G3 counted g3_counted`")
	}
	fn := regexp.MustCompile(`(?s)\ng3_counted\(\) \{\n(.*?)\n\}\n`).FindSubmatch(gate)
	if fn == nil || !strings.Contains(string(fn[1]), "scripts/counted-growth.sh") {
		t.Fatal("gate.sh: g3_counted does not run scripts/counted-growth.sh")
	}
	ci, err := os.ReadFile("ci-fast.sh")
	if err != nil {
		t.Fatal(err)
	}
	only := regexp.MustCompile(`GATE_ONLY=(\S+)`).FindSubmatch(ci)
	if only == nil || !strings.Contains(","+string(only[1])+",", ",G3,") {
		t.Fatal("ci-fast.sh: GATE_ONLY does not include G3")
	}
}
