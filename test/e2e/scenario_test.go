package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

func runDir(t *testing.T) string {
	if v := os.Getenv("RUN_DIR"); v != "" {
		return v
	}
	return t.TempDir()
}

func flushedFiles(t *testing.T, dir string) []byte {
	t.Helper()
	var out []byte
	for _, f := range []string{csvflush.MetricsFile, csvflush.HistogramsFile, csvflush.RunFile} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

func replayLine(name string, seed int64) string {
	return fmt.Sprintf("REPLAY: go test ./test/e2e -run '^TestScenarioE2E$/^%s$' -seed=%d -count=1", name, seed)
}

func runScenario(t *testing.T, sc Scenario, seed int64) *SimRun {
	t.Helper()
	replay := replayLine(sc.Spec.Name, seed)
	a, err := RunSim(sc.Spec, seed, SimOptions{Dir: runDir(t), Replay: replay, Tamper: sc.Tamper})
	if err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if err := sc.Check(a); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if err := a.Flush.Check(); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	b, err := RunSim(sc.Spec, seed, SimOptions{Dir: t.TempDir(), Replay: replay, Tamper: sc.Tamper})
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, replay)
	}
	if a.World.S.Digests() != b.World.S.Digests() {
		t.Fatalf("replay law: digests differ %s / %s\n%s", a.World.S.Digests(), b.World.S.Digests(), replay)
	}
	if !bytes.Equal(flushedFiles(t, a.Flush.RunDir), flushedFiles(t, b.Flush.RunDir)) {
		t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
	}
	return a
}

func TestScenarioE2E(t *testing.T) {
	for _, sc := range Scenarios() {
		t.Run(sc.Spec.Name, func(t *testing.T) {
			simtest.Sweep(t, func(t *testing.T, seed int64) { runScenario(t, sc, seed) })
		})
	}
}
