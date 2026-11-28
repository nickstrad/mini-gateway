package e2e

import (
	"bytes"
	"os"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/coalesce"

	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

const (
	coalFullSweepEnv = "MG_C11_FULL_SWEEP"
	coalSweepSeeds   = 10
	coalHeavySeeds   = 3
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
		dir := t.TempDir()
		if ab.Coalesce.Mode == coalesce.ModeOff {
			dir = runDir(t)
		}
		r, err := RunSim(ab, seed, SimOptions{Dir: dir, Arm: "ablation", Replay: replay})
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
			if sc.Heavy && !CoalesceHeavyRuns(testing.Short()) {
				t.Skip("1,000-client scenario: runs in the sweep (RUN_DIR set) or with " + CoalesceHeavyEnv + "=1, never under -short")
			}
			seeds := simtest.Seeds(t)
			full := os.Getenv(coalFullSweepEnv) == "1"
			if sc.Heavy && len(seeds) > coalHeavySeeds && !full {
				seeds = seeds[:coalHeavySeeds]
			}
			if len(seeds) > coalSweepSeeds && !full {
				seeds = seeds[:coalSweepSeeds]
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

func TestCoalesceHotKeyNumbers(t *testing.T) {
	if testing.Short() || os.Getenv(CoalesceHeavyEnv) != "1" {
		t.Skip("1,000 clients per arm: runs with " + CoalesceHeavyEnv + "=1 (the gate's extra)")
	}
	report := func(name string, r *SimRun) {
		el := firstStepTimes(r)
		t.Logf("HEADLINE tier S hot key %s: %d clients, %d database reads, p50 %v, p99 %v, max %v", name, len(el),
			len(readsOf(r, hotRead())), Percentile(el, 500), Percentile(el, 990), Percentile(el, 1000))
	}
	for _, j := range []coalesce.JoinMode{coalesce.JoinStrict, coalesce.JoinRelaxed} {
		r, err := RunSim(HotKeySpec(j), 1, SimOptions{})
		if err != nil {
			t.Fatal(err)
		}
		report(j.String(), r)
	}
	off, err := RunSim(coalesceOff(HotKeySpec(coalesce.JoinStrict)), 1, SimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report("off", off)
	direct, err := RunSim(HotKeySpec(coalesce.JoinStrict), 1, SimOptions{Direct: true})
	if err != nil {
		t.Fatal(err)
	}
	report("direct", direct)
}
