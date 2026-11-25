package csvflush_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
)

// TestRoundTripCatchesAPlantedBugInABaselineRun proves Run.Check passes cleanly
// on direct-open-loop-overload and fails when either planted-bug canary is enabled.
func TestRoundTripCatchesAPlantedBugInABaselineRun(t *testing.T) {
	for name, plant := range map[string]func(*csvflush.Config){
		"none":             nil,
		"drop-last-bucket": csvflush.PlantDropLastBucket,
		"stale-value":      csvflush.PlantStaleValue,
	} {
		t.Run(name, func(t *testing.T) {
			sc, _ := simrun.Baseline("direct-open-loop-overload")
			r, err := simrun.Scenario(sc, 1, simrun.Options{Dir: t.TempDir(), CSV: plant})
			if err != nil {
				t.Fatal(err)
			}
			err = r.Check()
			if plant == nil && err != nil {
				t.Fatalf("clean run: %v", err)
			}
			if plant != nil && err == nil {
				t.Fatal("the round trip did not catch the planted bug")
			}
		})
	}
}
