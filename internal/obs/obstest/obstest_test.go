package obstest

import (
	"slices"
	"testing"
	"time"
)

type inner struct{ A, B int64 }

type good struct {
	N    int64
	In   inner
	List []inner
	Name string
}

type bad struct {
	F   float64
	M   map[string]int
	P   *int
	At  time.Time
	Out good
}

// Proves PlainProblems catches floats, maps, pointers, and time.Time, and
// ZeroCounters/MergeMoved/NeverMoved accurately track unexercised counters.
func TestPlainAndCounters(t *testing.T) {
	RequirePlain(t, good{})
	if p := PlainProblems(bad{}); len(p) != 4 {
		t.Fatalf("problems %v", p)
	}
	if z := ZeroCounters(good{N: 1, In: inner{B: 2}}); !slices.Equal(z, []string{"In.A"}) {
		t.Fatalf("zero %v", z)
	}
	moved := map[string]bool{}
	MergeMoved(moved, good{N: 1})
	MergeMoved(moved, good{In: inner{A: 1}})
	if nm := NeverMoved(moved); !slices.Equal(nm, []string{"In.B"}) {
		t.Fatalf("never moved %v", nm)
	}
}
