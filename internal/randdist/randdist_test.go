package randdist

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestExpGapIsBoundedAndPositive(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 100_000; i++ {
		g := ExpGap(r, time.Millisecond)
		if g < 1 || g > MaxGapFactor*time.Millisecond {
			t.Fatalf("gap %s", g)
		}
	}
}

func TestZipfStaysInRange(t *testing.T) {
	z := NewZipf(rand.New(rand.NewPCG(1, 2)), 1500, 10)
	for i := 0; i < 10_000; i++ {
		if k := z.Uint64(); k >= 10 {
			t.Fatalf("key %d", k)
		}
	}
	if NewZipf(rand.New(rand.NewPCG(1, 2)), 1000, 10) != nil {
		t.Fatal("an exponent of 1 must be refused by the generator")
	}
}
