package seeded

import "math/rand/v2"

func New(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, 7))
}

func Pick(rng *rand.Rand, xs []int) int {
	rng.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
	return xs[rng.IntN(len(xs))]
}
