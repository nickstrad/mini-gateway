package sim

// Copied from object-store internal/sim/stream.go at commit
// 25fb899317955eb7d87186f940702e55dc0e3cde.

import (
	"hash/fnv"
	"math/rand/v2"
)

// NewStream returns a deterministic PCG random stream derived from the root
// seed and a stream label. Distinct labels produce independent streams so
// drawing more values from one subsystem never shifts another.
func NewStream(seed int64, label string) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), fnv64a([]byte(label))))
}

func fnv64a(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}
