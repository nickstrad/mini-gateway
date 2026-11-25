package randdist

import (
	"math/rand/v2"
	"time"
)

const MaxGapFactor = 1000

func ExpGap(r *rand.Rand, mean time.Duration) time.Duration {
	g := time.Duration(r.ExpFloat64() * float64(mean))
	if g < 1 {
		return 1
	}
	if limit := MaxGapFactor * mean; g > limit {
		return limit
	}
	return g
}

func NewZipf(r *rand.Rand, thousandths int64, n int) *rand.Zipf {
	return rand.NewZipf(r, float64(thousandths)/1000, 1, uint64(n-1))
}
