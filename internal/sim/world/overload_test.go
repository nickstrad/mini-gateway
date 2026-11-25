package world_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

// Proves direct-open-loop-overload mean latency from intended send time increases monotonically across completion quarters.
func TestOverloadLatencyGrowsWithTheBacklog(t *testing.T) {
	sc := world.OpenLoopOverload()
	type done struct{ at, latency int64 }
	var all []done
	kindDone := sim.K("wl.done")
	d := &world.SimDriver{Seed: 1, Scenario: sc.Name, Tap: func(r sim.Record) {
		if r.Kind == kindDone && r.C == int64(workload.OutcomeOK) {
			all = append(all, done{at: int64(r.At), latency: r.D})
		}
	}}
	r := world.RunWith(sc, 1, d)
	if err := sc.Verify(r); err != nil {
		t.Fatal(err)
	}
	q := len(all) / 4
	var means [4]int64
	for i := 0; i < 4; i++ {
		sum := int64(0)
		for _, x := range all[i*q : (i+1)*q] {
			sum += x.latency
		}
		means[i] = sum / int64(q)
	}
	for i := 1; i < 4; i++ {
		if means[i] <= means[i-1] {
			t.Fatalf("mean latency per quarter of completions %v does not grow", means)
		}
	}
	t.Logf("mean latency from the intended send time per quarter of completions (ns): %v", means)
}
