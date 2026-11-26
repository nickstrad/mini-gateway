package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"testing"
)

const shrinkFreezeDigest = "8eda1df20d7bfd1bbd82e870ad45976cf8ef7101f65b14ffbfbc816e437360ec"

type shrinkRow struct {
	in   []Fault
	pred func([]Fault) bool
	want []Fault
}

func kindsPresent(s []Fault, kinds ...FaultKind) bool {
	for _, k := range kinds {
		if !slices.ContainsFunc(s, func(f Fault) bool { return f.Kind == k }) {
			return false
		}
	}
	return true
}

func shrinkRows() []shrinkRow {
	a := Fault{At: 10, Kind: 1, Target: 1, Arg1: 2, Arg2: 3}
	b := Fault{At: 20, Kind: 2, Target: 0, Arg1: 0, Arg2: 9}
	c := Fault{At: 30, Kind: 3, Target: 2, Arg1: 5, Arg2: 0}
	d := Fault{At: 40, Kind: 2, Target: 1, Arg1: 7, Arg2: 1}
	return []shrinkRow{
		{nil, func([]Fault) bool { return true }, []Fault{}},
		{[]Fault{a, b, c}, func([]Fault) bool { return true }, []Fault{}},
		{[]Fault{a, b, c}, func([]Fault) bool { return false }, []Fault{a, b, c}},
		{[]Fault{a, b, c, d}, func(s []Fault) bool { return kindsPresent(s, 1, 3) }, []Fault{a, c}},
		{[]Fault{a, b, c, d}, func(s []Fault) bool { return kindsPresent(s, 2) }, []Fault{d}},
		{[]Fault{a, b, c, d}, func(s []Fault) bool { return len(s)%2 == 0 && len(s) > 0 }, []Fault{a, b, c, d}},
		{[]Fault{d, c, b, a}, func(s []Fault) bool { return len(s) >= 2 }, []Fault{b, a}},
	}
}

// Proves sim.Shrink matches its freeze-sim outputs on fixed edge and
// tie-breaking cases without mutating inputs.
func TestShrinkFrozenTable(t *testing.T) {
	for i, row := range shrinkRows() {
		in := slices.Clone(row.in)
		got := Shrink(row.in, row.pred)
		if !slices.Equal(got, row.want) {
			t.Fatalf("row %d: got %+v, want %+v", i, got, row.want)
		}
		if !slices.Equal(in, row.in) {
			t.Fatalf("row %d: input mutated", i)
		}
	}
}

func hashFaults(h interface{ Write([]byte) (int, error) }, tag byte, s []Fault) {
	var buf [8]byte
	h.Write([]byte{tag})
	binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
	h.Write(buf[:])
	for _, f := range s {
		for _, v := range []int64{int64(f.At), int64(f.Kind), f.Target, f.Arg1, f.Arg2} {
			binary.BigEndian.PutUint64(buf[:], uint64(v))
			h.Write(buf[:])
		}
	}
}

func freezePredicate(family int, s []Fault) bool {
	var sumT, sumA int64
	for _, f := range s {
		sumT += f.Target
		sumA += f.Arg1 + f.Arg2
	}
	switch family {
	case 0:
		return kindsPresent(s, 1, 4)
	case 1:
		return kindsPresent(s, 2) && len(s) > 3
	case 2:
		return sumT%3 == 1
	case 3:
		return len(s) > 0 && s[0].Kind == 3 && sumA > 10
	default:
		return len(s)%2 == 1 && kindsPresent(s, 5)
	}
}

func shrinkFreezeSweep() string {
	h := sha256.New()
	for seed := int64(0); seed < 400; seed++ {
		r := NewStream(seed, "shrink-freeze")
		n := r.IntN(14)
		in := make([]Fault, 0, n)
		for i := 0; i < n; i++ {
			in = append(in, Fault{At: Time(r.IntN(1000)), Kind: FaultKind(r.IntN(6)), Target: int64(r.IntN(4)), Arg1: int64(r.IntN(9)), Arg2: int64(r.IntN(9))})
		}
		family := int(seed % 5)
		hashFaults(h, 'i', in)
		out := Shrink(in, func(s []Fault) bool {
			hashFaults(h, 'c', s)
			return freezePredicate(family, s)
		})
		hashFaults(h, 'o', out)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Proves sim.Shrink's inputs, predicate-call sequence, and outputs over 400
// seeded schedules match the SHA-256 digest captured at freeze-sim.
func TestShrinkFrozenSweepDigest(t *testing.T) {
	if got := shrinkFreezeSweep(); got != shrinkFreezeDigest {
		t.Fatalf("Shrink's inputs, predicate calls and outputs moved: digest %s, frozen %s", got, shrinkFreezeDigest)
	}
}
