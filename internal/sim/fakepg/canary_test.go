package fakepg_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	simworld "github.com/nickstrad/mini-gateway/internal/sim/world"
)

func TestPlantedBugDoubleWriteIsCaughtByTheWriteLogCheck(t *testing.T) {
	sc := simworld.MixedTransactions()
	for _, seed := range []int64{1, 2, 3} {
		clean := simworld.Run(sc, seed)
		if err := sc.Verify(clean); err != nil {
			t.Fatalf("seed %d: the control run fails: %v", seed, err)
		}
		planted := simworld.Run(sc, seed, func(s *simworld.Setup) { fakepg.PlantDoubleApply(&s.Fake) })
		err := sc.Verify(planted)
		if err == nil || !strings.Contains(err.Error(), "applied twice") {
			t.Fatalf("seed %d: a fake that applies every write twice passed the write-log check: %v", seed, err)
		}
	}
}
