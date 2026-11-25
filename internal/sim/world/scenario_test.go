package world_test

import (
	"fmt"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

func TestScenario(t *testing.T) {
	for _, sc := range world.Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			simtest.Sweep(t, func(t *testing.T, seed int64) {
				var results []*world.Result
				run := func(seed int64) *sim.Sim {
					d := &world.SimDriver{Seed: seed, Scenario: sc.Name, Replay: simtest.ReplayCommand(t, seed)}
					r := world.RunWith(sc, seed, d)
					results = append(results, r)
					return r.Sim
				}
				simtest.RequireDeterministic(t, seed, run)
				a, b := results[0], results[1]
				if sa, sb := fmt.Sprintf("%+v %+v", a.Fake, a.Obs), fmt.Sprintf("%+v %+v", b.Fake, b.Obs); sa != sb {
					t.Fatalf("snapshots differ between runs of one seed\n%s\n%s", sa, sb)
				}
				if err := sc.Verify(a); err != nil {
					t.Fatalf("%v\n%s", err, a.Sim.Replay())
				}
			})
		})
	}
}
