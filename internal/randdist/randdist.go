// Package randdist isolates the two floating-point random-distribution helpers
// (exponential gaps and Zipf keys) outside internal/sim so the rest of the
// simulator stays strictly integer-only. Determinism is scoped to the same
// commit, Go toolchain, and linux/amd64.
package randdist

import (
	"math/rand/v2"
	"time"
)

// MaxGapFactor caps an exponential inter-arrival gap at 1000x the configured
// mean so extreme tail draws cannot stretch a scenario past its window.
const MaxGapFactor = 1000

// ExpGap draws a positive exponential inter-arrival gap with the given mean,
// clamped to [1ns, MaxGapFactor*mean].
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

// NewZipf constructs a Zipf generator over [0, n-1] with v=1 and exponent
// s = thousandths/1000. Callers pass the exponent as integer thousandths so
// configuration structs inside internal/sim carry no float fields.
func NewZipf(r *rand.Rand, thousandths int64, n int) *rand.Zipf {
	return rand.NewZipf(r, float64(thousandths)/1000, 1, uint64(n-1))
}
