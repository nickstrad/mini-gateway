package e2e

import (
	"bytes"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

func poolReplay(name string, seed int64) string {
	return replayLine(name, seed) + " (TestScenarioPool)"
}

func runPool(t *testing.T, sc PoolScenario, seed int64) *SimRun {
	t.Helper()
	replay := poolReplay(sc.Spec.Name, seed)
	a, err := RunSim(sc.Spec, seed, SimOptions{Dir: runDir(t), Replay: replay})
	if err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if err := sc.Check(a); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if err := a.Flush.Check(); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	b, err := RunSim(sc.Spec, seed, SimOptions{Dir: t.TempDir(), Replay: replay})
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, replay)
	}
	if a.World.S.Digests() != b.World.S.Digests() {
		t.Fatalf("replay law: digests differ %s / %s\n%s", a.World.S.Digests(), b.World.S.Digests(), replay)
	}
	if !bytes.Equal(flushedFiles(t, a.Flush.RunDir), flushedFiles(t, b.Flush.RunDir)) {
		t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
	}
	if sc.Direct {
		if _, err := RunSim(sc.Spec, seed, SimOptions{Direct: true, Dir: runDir(t), Arm: "direct", Replay: replay}); err != nil {
			t.Fatalf("direct arm: %v\n%s", err, replay)
		}
	}
	return a
}

func TestScenarioPool(t *testing.T) {
	for _, sc := range PoolScenarios() {
		t.Run(sc.Spec.Name, func(t *testing.T) {
			t.Parallel()
			seeds := simtest.Seeds(t)
			if sc.Heavy && len(seeds) > heavySeeds {
				seeds = seeds[:heavySeeds]
			}
			for _, seed := range seeds {
				t.Run("seed="+itoa(int(seed)), func(t *testing.T) { runPool(t, sc, seed) })
			}
		})
	}
}
