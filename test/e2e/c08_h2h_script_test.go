package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const h2hFixtureScenario = "admission-h2h-l370"

func h2hTable(t *testing.T, root string) []byte {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "admission-head-to-head.sh"), root)
	cmd.Env = append(os.Environ(), "PATH=/usr/local/go/bin:"+os.Getenv("HOME")+"/.local/bin:"+os.Getenv("PATH"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("head-to-head script over %q: %v\n%s", root, err, errb.String())
	}
	return out.Bytes()
}

func TestHeadToHeadScriptAcceptsAQuoteInTheRunDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the simulator and DuckDB")
	}
	if _, err := exec.LookPath("duckdb"); err != nil {
		if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "bin", "duckdb")); err != nil {
			t.Skip("duckdb is not installed")
		}
	}
	base := t.TempDir()
	plain, quoted := filepath.Join(base, "plain"), filepath.Join(base, "operator's runs")
	for _, dir := range []string{plain, quoted} {
		for _, arm := range AdmArms() {
			spec := AdmSpec{Scenario: h2hFixtureScenario, Arm: arm, LoadPct: 370, Phases: 1, Clients: 13, TenantCap: 9, TotalCap: 13}
			if _, err := RunAdmission(spec, 1, dir, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	a, b := h2hTable(t, plain), h2hTable(t, quoted)
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Fatalf("the table changed with the directory name:\n%s\n---\n%s", a, b)
	}
}
