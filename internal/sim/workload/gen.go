package workload

import (
	"math/rand/v2"
	"time"
)

func expGap(r *rand.Rand, mean time.Duration) time.Duration {
	g := time.Duration(r.ExpFloat64() * float64(mean))
	if g < 1 {
		return 1
	}
	if limit := 1000 * mean; g > limit {
		return limit
	}
	return g
}

func newZipf(r *rand.Rand, thousandths int64, n int) *rand.Zipf {
	return rand.NewZipf(r, float64(thousandths)/1000, 1, uint64(n-1))
}
