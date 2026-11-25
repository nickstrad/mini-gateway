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

func TestPlantedBugDroppedWriteIsCaughtByTheWriteLogCheck(t *testing.T) {
	sc := simworld.MixedTransactions()
	for _, seed := range []int64{1, 2, 3} {
		for _, nth := range []int64{1, 5, 40} {
			planted := simworld.Run(sc, seed, func(s *simworld.Setup) { fakepg.PlantDropWrite(&s.Fake, nth) })
			err := sc.Verify(planted)
			if err == nil || !strings.Contains(err.Error(), "missing from the write log") {
				t.Fatalf("seed %d: a fake that silently drops write %d passed the write-log check: %v", seed, nth, err)
			}
		}
	}
}

func TestPlantedBugPhantomWriteIsCaughtByTheWriteLogCheck(t *testing.T) {
	sc := simworld.MixedTransactions()
	for _, seed := range []int64{1, 2, 3} {
		planted := simworld.Run(sc, seed, func(s *simworld.Setup) { fakepg.PlantPhantomWritesFrom(&s.Fake, 7) })
		err := sc.Verify(planted)
		if err == nil || !strings.Contains(err.Error(), "replaying the write log") {
			t.Fatalf("seed %d: a fake that logs writes it did not apply passed the write-log check: %v", seed, err)
		}
	}
}
