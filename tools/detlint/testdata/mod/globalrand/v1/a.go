package v1

import "math/rand"

func Shuffle(xs []int) {
	rand.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
}
