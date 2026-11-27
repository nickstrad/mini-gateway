package e2e

import (
	"bytes"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

func coalReplay(name string, seed int64) string {
	return replayLine(name, seed) + " (TestScenarioCoalesce)"
}

func runCoalesce(t *testing.T, sc CoalesceScenario, seed int64) *SimRun {
	t.Helper()
	replay := coalReplay(sc.Spec.Name, seed)
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
	if sc.Ablate != nil {
		ab := sc.Ablate(sc.Spec)
		r, err := RunSim(ab, seed, SimOptions{Dir: runDir(t), Arm: "ablation", Replay: replay})
		if err == nil {
			err = sc.Check(r)
		}
		if err == nil {
			t.Fatalf("ablation (%s): the scenario's check still passes, so it proves nothing\n%s", sc.Ablated, replay)
		}
	}
	if sc.Direct {
		if _, err := RunSim(sc.Spec, seed, SimOptions{Direct: true, Dir: runDir(t), Arm: "direct", Replay: replay}); err != nil {
			t.Fatalf("direct arm: %v\n%s", err, replay)
		}
	}
	return a
}

func TestScenarioCoalesce(t *testing.T) {
	for _, sc := range CoalesceScenarios() {
		t.Run(sc.Spec.Name, func(t *testing.T) {
			t.Parallel()
			if sc.Heavy && testing.Short() {
				t.Skip("1,000-client scenario: runs without -short (sweep, gate)")
			}
			seeds := simtest.Seeds(t)
			if sc.Heavy && len(seeds) > heavySeeds {
				seeds = seeds[:heavySeeds]
			}
			if testing.Short() && len(seeds) > 1 {
				seeds = seeds[:1]
			}
			for _, seed := range seeds {
				t.Run("seed="+itoa(int(seed)), func(t *testing.T) { runCoalesce(t, sc, seed) })
			}
		})
	}
}
