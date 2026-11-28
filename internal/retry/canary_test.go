package retry

import (
	"math/rand/v2"
	"testing"
)

func propertyFails(cfg Config) bool {
	for seed := range 20 {
		r := rand.New(rand.NewPCG(uint64(seed), 7))
		if _, err := runOps(cfg, randomOps(r, propertyOps, 50)); err != nil {
			return true
		}
	}
	return false
}

// TestCanaryEveryPlantedBudgetBugFailsTheProperty proves that each planted
// retry bug (reached, once bound, withdrawal, cap, breaker, withdraw-first)
// fails the reference-model property test while the unplanted policy passes.
func TestCanaryEveryPlantedBudgetBugFailsTheProperty(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant Plant
	}{
		{"reached-no condition removed", PlantIgnoreReached},
		{"once-per-request bound removed", PlantNoOnceBound},
		{"budget withdrawal removed", PlantNoWithdraw},
		{"bucket cap removed", PlantNoCap},
		{"breaker check removed", PlantIgnoreBreaker},
		{"withdrawal before the checks", PlantWithdrawFirst},
	} {
		cfg := smallConfig(KindPreForwardOnce)
		cfg.Plant = c.plant
		if !propertyFails(cfg) {
			t.Errorf("%s: the property test still passes", c.name)
		}
	}
	if propertyFails(smallConfig(KindPreForwardOnce)) {
		t.Fatal("the unplanted policy fails the property")
	}
}
