package pgwire_test

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/wireworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

const wireSweepStride = 4

func wireSeeds(t *testing.T, scenario int) []int64 {
	t.Helper()
	all := simtest.Seeds(t)
	_, single, err := simtest.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if single || os.Getenv("DST_SEEDS") == "" {
		return all
	}
	pinned, err := simtest.PinnedSeeds("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, seed := range all {
		if slices.Contains(pinned, seed) || ((seed+int64(scenario))%wireSweepStride+wireSweepStride)%wireSweepStride == 0 {
			out = append(out, seed)
		}
	}
	return out
}

func TestWireSweepKeepsTheFullMatrixOnPinnedSeeds(t *testing.T) {
	if _, single, _ := simtest.Seed(); single {
		t.Skip("one seed is pinned")
	}
	t.Setenv("DST_SEEDS", "40")
	t.Setenv("DST_SEED_BASE", "1")
	pinned, err := simtest.PinnedSeeds("testdata")
	if err != nil || len(pinned) == 0 {
		t.Fatalf("pinned seeds %v %v", pinned, err)
	}
	runs := map[int64]int{}
	for i := range wireworld.Scenarios() {
		seeds := wireSeeds(t, i)
		for _, p := range pinned {
			if !slices.Contains(seeds, p) {
				t.Fatalf("scenario %d skips pinned seed %d", i, p)
			}
		}
		if len(seeds) < 40/wireSweepStride {
			t.Fatalf("scenario %d runs %d seeds", i, len(seeds))
		}
		for _, s := range seeds {
			runs[s]++
		}
	}
	for s := int64(1); s <= 40; s++ {
		if runs[s] == 0 {
			t.Fatalf("seed %d runs no scenario", s)
		}
	}
	t.Setenv("DST_SEEDS", "")
	if got, all := len(wireSeeds(t, 1)), len(simtest.Seeds(t)); got != all {
		t.Fatalf("default run: %d of %d seeds", got, all)
	}
}

func TestScenarioWireStartup(t *testing.T) {
	for i, sc := range wireworld.Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			for _, seed := range wireSeeds(t, i) {
				t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { runWireScenario(t, sc, seed) })
			}
		})
	}
}

func runWireScenario(t *testing.T, sc wireworld.Scenario, seed int64) {
	var results []wireworld.Result
	run := func(seed int64) *sim.Sim {
		r := wireworld.Run(seed, sc, simtest.ReplayCommand(t, seed))
		if r.World == nil {
			t.Fatal(r.Err)
		}
		results = append(results, r)
		return r.World.Sim
	}
	simtest.RequireDeterministic(t, seed, run)
	a, b := results[0], results[1]
	if fmt.Sprintf("%+v %+v", a.Final, a.Counters) != fmt.Sprintf("%+v %+v", b.Final, b.Counters) {
		t.Fatal("snapshots differ between two runs of one seed")
	}
	if err := sc.Verify(a); err != nil {
		t.Fatalf("%v\n%s", err, a.World.Sim.Replay())
	}
}
