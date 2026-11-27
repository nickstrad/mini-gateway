package scripts_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChaosAProcessFailureAfterPassingScenariosFails(t *testing.T) {
	log := e2eBlock(false) + "panic: late background failure\nFAIL\t" + mod + "/test/e2e\t4.700s\n"
	r := runScript(t, "chaos.sh", log, 1)
	if r.code != 1 || !strings.Contains(r.out, "panic: late background failure") || strings.Contains(r.out, "all green") {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	if !strings.Contains(r.out, "chaos: the tier S go test process exited 1 though no scenario failed") {
		t.Fatalf("no process failure line:\n%s", r.out)
	}
}

func TestChaosAFailedParentWithPassingSeedsFailsTheScenario(t *testing.T) {
	log := strings.Replace(e2eBlock(false), "    --- PASS: TestScenarioE2E/e2e-results (", "    --- FAIL: TestScenarioE2E/e2e-results (", 1)
	r := runScript(t, "chaos.sh", log, 1)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	m := chaosLine.FindAllStringSubmatch(r.out, -1)
	verdicts := map[string]string{}
	for _, x := range m {
		verdicts[x[1]] = x[3]
	}
	if verdicts["e2e-results"] != "FAIL" || verdicts["e2e-queue-full"] != "pass" {
		t.Fatalf("verdicts %v\n%s", verdicts, r.out)
	}
}

func runDown(t *testing.T, dockerBody string) (string, int) {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/bash\n"+dockerBody), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "deploy", "dev", "down.sh"))
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "MG_DEV_DIR="+t.TempDir(), "MG_DEV_PROJECT=mg-down-test")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return buf.String(), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), 0
}

func TestRunDownFailsWhenDockerCannotListTheStack(t *testing.T) {
	out, code := runDown(t, "echo 'daemon unreachable' >&2\nexit 1\n")
	if code != 1 || strings.Contains(out, "the dev stack is down") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, kind := range []string{"containers", "networks", "volumes"} {
		if !strings.Contains(out, "run-down: cannot list the stack's "+kind+" (docker exit status 1: daemon unreachable)") {
			t.Fatalf("no listing failure for %s:\n%s", kind, out)
		}
	}
	out, code = runDown(t, "case \"$1 $2\" in \"volume ls\") exit 1;; esac\nexit 0\n")
	if code != 1 || !strings.Contains(out, "cannot list the stack's volumes") {
		t.Fatalf("one failed listing: exit %d\n%s", code, out)
	}
	out, code = runDown(t, "exit 0\n")
	if code != 0 || !strings.Contains(out, "the dev stack is down") {
		t.Fatalf("a reachable Docker with nothing left: exit %d\n%s", code, out)
	}
}
