package sim

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/bits"
	"math/rand/v2"
	"slices"
	"testing"
)

type intPred struct {
	name     string
	monotone bool
	fails    func([]int64) bool
}

func hashBit(salt int64, vals []int64) bool {
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range append([]int64{salt}, vals...) {
		for i := range buf {
			buf[i] = byte(uint64(v) >> (8 * i))
		}
		h.Write(buf[:])
	}
	return h.Sum64()%3 == 0
}

func intPreds(r *rand.Rand, n int) []intPred {
	ks := make([]int64, n)
	for i := range ks {
		ks[i] = int64(r.IntN(9)) - 2
	}
	sumK := int64(r.IntN(20)) - 4
	mod := int64(r.IntN(3))
	salt := int64(r.Uint32())
	sum := func(v []int64) int64 {
		var s int64
		for _, x := range v {
			s += x
		}
		return s
	}
	return []intPred{
		{"always", true, func([]int64) bool { return true }},
		{"never", true, func([]int64) bool { return false }},
		{"each-at-least", true, func(v []int64) bool {
			for i, x := range v {
				if x < ks[i] {
					return false
				}
			}
			return true
		}},
		{"sum-at-least", true, func(v []int64) bool { return sum(v) >= sumK }},
		{"first-or-last", true, func(v []int64) bool { return len(v) > 0 && (v[0] >= ks[0] || v[len(v)-1] >= ks[len(v)-1]) }},
		{"sum-mod-3", false, func(v []int64) bool { return ((sum(v)%3)+3)%3 == mod }},
		{"hash", false, func(v []int64) bool { return hashBit(salt, v) }},
	}
}

func intCallBound(vals, floors []int64, monotone bool) int64 {
	var c, p int64
	for i, v := range vals {
		f := floorAt(floors, i)
		if v <= f {
			continue
		}
		d := uint64(v) - uint64(f)
		c += 1 + int64(bits.Len64(d))
		p = satAdd(p, d)
	}
	if monotone {
		return 2 * c
	}
	return satMul(satAdd(p, 1), c)
}

func satAdd(a int64, d uint64) int64 {
	if d > uint64(math.MaxInt64-a) {
		return math.MaxInt64
	}
	return a + int64(d)
}

func satMul(a, b int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi != 0 || lo > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(lo)
}

func floorAt(floors []int64, i int) int64 {
	if i < len(floors) {
		return floors[i]
	}
	return 0
}

func checkLowerInts(vals, floors []int64, p intPred) error {
	in, fl := slices.Clone(vals), slices.Clone(floors)
	calls := int64(0)
	out := LowerInts(vals, floors, func(v []int64) bool {
		calls++
		got := p.fails(v)
		for i := range v {
			v[i] = math.MinInt64
		}
		return got
	})
	if !slices.Equal(in, vals) || !slices.Equal(fl, floors) {
		return errors.New("the caller's slices were mutated")
	}
	if len(out) != len(vals) {
		return fmt.Errorf("length %d, want %d", len(out), len(vals))
	}
	if p.fails(vals) && !p.fails(out) {
		return fmt.Errorf("%v lowered to %v no longer fails", vals, out)
	}
	for i := range out {
		f := floorAt(floors, i)
		if vals[i] <= f {
			if out[i] != vals[i] {
				return fmt.Errorf("value %d at or below its floor moved: %v -> %v", i, vals, out)
			}
			continue
		}
		if out[i] < f || out[i] > vals[i] {
			return fmt.Errorf("value %d out of [floor, input]: %v -> %v", i, vals, out)
		}
		lowest := out[i] - 1
		if p.monotone {
			lowest = f
		}
		for w := lowest; w < out[i] && w >= f; w++ {
			cand := slices.Clone(out)
			cand[i] = w
			if p.fails(cand) {
				return fmt.Errorf("%s: %v -> %v, but value %d can still go to %d", p.name, vals, out, i, w)
			}
		}
	}
	if b := intCallBound(vals, floors, p.monotone); calls > b {
		return fmt.Errorf("%s: %d predicate calls, bound %d", p.name, calls, b)
	}
	if again := LowerInts(vals, floors, p.fails); !slices.Equal(again, out) {
		return fmt.Errorf("not deterministic: %v then %v", out, again)
	}
	return nil
}

func lowerIntsSweep(seeds int64) []error {
	var errs []error
	for seed := int64(0); seed < seeds; seed++ {
		r := NewStream(seed, "lower-ints")
		n := r.IntN(5)
		vals := make([]int64, n)
		for i := range vals {
			vals[i] = int64(r.IntN(12)) - 2
		}
		var floors []int64
		for i := 0; i < r.IntN(n+2); i++ {
			floors = append(floors, int64(r.IntN(7))-2)
		}
		for _, p := range intPreds(r, n) {
			if err := checkLowerInts(vals, floors, p); err != nil {
				errs = append(errs, fmt.Errorf("seed %d: %w", seed, err))
			}
		}
	}
	return errs
}

func TestLowerIntsAgainstBruteForce(t *testing.T) {
	if errs := lowerIntsSweep(600); len(errs) > 0 {
		t.Fatalf("%d failures, first: %v", len(errs), errs[0])
	}
}

func TestPlantedAcceptUncheckedFailsTheBruteForceProperty(t *testing.T) {
	plantAcceptUnchecked = true
	defer func() { plantAcceptUnchecked = false }()
	if errs := lowerIntsSweep(200); len(errs) == 0 {
		t.Fatal("the property test passed with the value pass accepting candidates unchecked")
	}
	if errs := minimizeSweep(100); len(errs) == 0 {
		t.Fatal("the minimiser property test passed with the value pass accepting candidates unchecked")
	}
}

func TestLowerIntsExtremesAndBounds(t *testing.T) {
	cases := []struct {
		vals, floors []int64
		k            int64
	}{
		{[]int64{math.MaxInt64}, []int64{math.MinInt64}, 12345},
		{[]int64{math.MaxInt64, math.MaxInt64}, nil, math.MaxInt64 - 3},
		{[]int64{math.MinInt64, -5, 0}, []int64{0, -10, 5}, -7},
		{[]int64{1 << 40}, []int64{1 << 40}, 0},
	}
	for _, c := range cases {
		for _, p := range []intPred{
			{"always", true, func([]int64) bool { return true }},
			{"never", true, func([]int64) bool { return false }},
			{"each-at-least", true, func(v []int64) bool {
				for _, x := range v {
					if x < c.k {
						return false
					}
				}
				return true
			}},
		} {
			calls := int64(0)
			out := LowerInts(c.vals, c.floors, func(v []int64) bool { calls++; return p.fails(v) })
			if b := intCallBound(c.vals, c.floors, true); calls > b {
				t.Fatalf("%v %s: %d calls, bound %d", c.vals, p.name, calls, b)
			}
			if err := checkExtreme(c.vals, c.floors, c.k, p.name, out); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func checkExtreme(vals, floors []int64, k int64, name string, out []int64) error {
	inputFails := name == "always" || name == "each-at-least" && !slices.ContainsFunc(vals, func(x int64) bool { return x < k })
	for i, v := range vals {
		f := floorAt(floors, i)
		want := v
		switch {
		case v <= f:
		case name == "always":
			want = f
		case name == "each-at-least" && inputFails:
			want = max(k, f)
		}
		if out[i] != want {
			return fmt.Errorf("%v floors %v %s: got %v, want %d at %d", vals, floors, name, out, want, i)
		}
	}
	return nil
}

func TestLowerIntsEmptyAndNil(t *testing.T) {
	calls := 0
	if out := LowerInts(nil, nil, func([]int64) bool { calls++; return true }); len(out) != 0 || calls != 0 {
		t.Fatalf("nil input: %v after %d calls", out, calls)
	}
}

type faultPred struct {
	name     string
	monotone bool
	fails    func([]Fault) bool
}

func testFloor(k FaultKind) FaultFloor {
	return FaultFloor{At: Time(k % 3), Arg1: int64(k % 2), Arg2: -int64(k % 4)}
}

func faultPreds(r *rand.Rand) []faultPred {
	a, b := Time(r.IntN(10)), int64(r.IntN(5))
	mod := int64(r.IntN(4))
	salt := int64(r.Uint32())
	return []faultPred{
		{"always", true, func([]Fault) bool { return true }},
		{"never", true, func([]Fault) bool { return false }},
		{"late-kind1-and-kind2", true, func(s []Fault) bool {
			return slices.ContainsFunc(s, func(f Fault) bool { return f.Kind == 1 && f.At >= a && f.Arg1 >= b }) &&
				slices.ContainsFunc(s, func(f Fault) bool { return f.Kind == 2 })
		}},
		{"total-at-least", true, func(s []Fault) bool {
			var t int64
			for _, f := range s {
				t += int64(f.At) + f.Arg1 + f.Arg2
			}
			return t >= 9+int64(a)
		}},
		{"sum-mod-4", false, func(s []Fault) bool {
			var t int64
			for _, f := range s {
				t += int64(f.At) + f.Arg1
			}
			return len(s) > 0 && ((t%4)+4)%4 == mod
		}},
		{"hash", false, func(s []Fault) bool {
			var v []int64
			for _, f := range s {
				v = append(v, int64(f.At), int64(f.Kind), f.Target, f.Arg1, f.Arg2)
			}
			return hashBit(salt, v)
		}},
	}
}

func faultFields(f Fault) [3]int64 { return [3]int64{int64(f.At), f.Arg1, f.Arg2} }

func floorFields(fl FaultFloor) [3]int64 { return [3]int64{int64(fl.At), fl.Arg1, fl.Arg2} }

func withField(f Fault, j int, v int64) Fault {
	switch j {
	case 0:
		f.At = Time(v)
	case 1:
		f.Arg1 = v
	default:
		f.Arg2 = v
	}
	return f
}

func faultCallBound(s []Fault, deletions bool, monotone bool) int64 {
	var c, p int64
	for _, f := range s {
		fv, fl := faultFields(f), floorFields(testFloor(f.Kind))
		for j := range fv {
			if fv[j] <= fl[j] {
				continue
			}
			d := uint64(fv[j]) - uint64(fl[j])
			c += 1 + int64(bits.Len64(d))
			p = satAdd(p, d)
		}
	}
	if !deletions {
		if monotone {
			return 2 * c
		}
		return satMul(satAdd(p, 1), c)
	}
	n := int64(len(s))
	return satMul(satAdd(p, 1), n*(n+1)+c)
}

func TestCallBoundsSaturateInsteadOfWrapping(t *testing.T) {
	full := intCallBound([]int64{math.MaxInt64}, []int64{math.MinInt64}, false)
	if full != math.MaxInt64 {
		t.Fatalf("full-width non-monotone bound %d, want saturation at %d", full, int64(math.MaxInt64))
	}
	two := intCallBound([]int64{math.MaxInt64, math.MaxInt64}, []int64{0, 0}, false)
	if two != math.MaxInt64 {
		t.Fatalf("summed distance bound %d, want saturation", two)
	}
	if small := intCallBound([]int64{5, 2}, []int64{1, 0}, false); small != (1+4+2)*(1+3+1+2) {
		t.Fatalf("small bound %d", small)
	}
	wide := []Fault{{At: MaxTime, Kind: 1, Arg1: math.MaxInt64, Arg2: math.MaxInt64}}
	if b := faultCallBound(wide, true, false); b != math.MaxInt64 {
		t.Fatalf("fault bound %d, want saturation", b)
	}
}

func checkValuesMinimal(in, out []Fault, p faultPred, deletions bool) error {
	if !deletions && len(out) != len(in) {
		return fmt.Errorf("length changed %d -> %d", len(in), len(out))
	}
	if p.fails(in) && !p.fails(out) {
		return fmt.Errorf("%s: %v -> %v no longer fails", p.name, in, out)
	}
	if err := checkLoweredFrom(in, out, deletions); err != nil {
		return fmt.Errorf("%s: %w", p.name, err)
	}
	for i, f := range out {
		if deletions && p.fails(slices.Delete(slices.Clone(out), i, i+1)) {
			return fmt.Errorf("%s: fault %d of %v can still be deleted", p.name, i, out)
		}
		fv, fl := faultFields(f), floorFields(testFloor(f.Kind))
		for j := range fv {
			if fv[j] <= fl[j] {
				continue
			}
			lowest := fv[j] - 1
			if p.monotone {
				lowest = fl[j]
			}
			for w := lowest; w < fv[j]; w++ {
				cand := slices.Clone(out)
				cand[i] = withField(f, j, w)
				if p.fails(cand) {
					return fmt.Errorf("%s: %v -> %v, field %d of fault %d can still go to %d", p.name, in, out, j, i, w)
				}
			}
		}
	}
	return nil
}

func loweredFrom(src, dst Fault) bool {
	if src.Kind != dst.Kind || src.Target != dst.Target {
		return false
	}
	sv, dv, fl := faultFields(src), faultFields(dst), floorFields(testFloor(src.Kind))
	for j := range sv {
		if sv[j] <= fl[j] && dv[j] != sv[j] {
			return false
		}
		if sv[j] > fl[j] && (dv[j] < fl[j] || dv[j] > sv[j]) {
			return false
		}
	}
	return true
}

func checkLoweredFrom(in, out []Fault, deletions bool) error {
	if !deletions {
		for i := range out {
			if !loweredFrom(in[i], out[i]) {
				return fmt.Errorf("fault %d %v is not %v lowered within [floor, input]", i, out[i], in[i])
			}
		}
		return nil
	}
	k := 0
	for i := range out {
		for k < len(in) && !loweredFrom(in[k], out[i]) {
			k++
		}
		if k == len(in) {
			return fmt.Errorf("%v is not a subsequence of %v with each value lowered within [floor, input]", out, in)
		}
		k++
	}
	return nil
}

func randomFaults(r *rand.Rand, n int) []Fault {
	s := make([]Fault, 0, n)
	for i := 0; i < n; i++ {
		s = append(s, Fault{At: Time(r.IntN(14)) - 1, Kind: FaultKind(1 + r.IntN(4)), Target: int64(r.IntN(3)), Arg1: int64(r.IntN(7)) - 1, Arg2: int64(r.IntN(7)) - 1})
	}
	return s
}

func runFaultPass(pass func([]Fault, func([]Fault) bool, func(FaultKind) FaultFloor) []Fault, in []Fault, p faultPred) ([]Fault, int64, error) {
	keep := slices.Clone(in)
	calls := int64(0)
	out := pass(in, func(s []Fault) bool {
		calls++
		got := p.fails(s)
		for i := range s {
			s[i] = Fault{At: MinTime, Kind: 99}
		}
		return got
	}, testFloor)
	if !slices.Equal(keep, in) {
		return nil, 0, errors.New("the caller's schedule was mutated")
	}
	if again := pass(in, p.fails, testFloor); !slices.Equal(again, out) {
		return nil, 0, fmt.Errorf("not deterministic: %v then %v", out, again)
	}
	if !keepsKindsAndTargets(in, out) {
		return nil, 0, fmt.Errorf("%v is not a subsequence of %v by kind and target", out, in)
	}
	return out, calls, nil
}

func keepsKindsAndTargets(in, out []Fault) bool {
	j := 0
	for _, f := range in {
		if j < len(out) && out[j].Kind == f.Kind && out[j].Target == f.Target {
			j++
		}
	}
	return j == len(out)
}

func lowerValuesSweep(seeds int64) []error {
	var errs []error
	for seed := int64(0); seed < seeds; seed++ {
		r := NewStream(seed, "lower-values")
		in := randomFaults(r, r.IntN(4))
		for _, p := range faultPreds(r) {
			out, calls, err := runFaultPass(LowerValues, in, p)
			if err == nil {
				err = checkValuesMinimal(in, out, p, false)
			}
			if err == nil {
				for i := range out {
					if out[i].Kind != in[i].Kind || out[i].Target != in[i].Target {
						err = fmt.Errorf("kind or target changed at %d", i)
					}
				}
			}
			if b := faultCallBound(in, false, p.monotone); err == nil && calls > b {
				err = fmt.Errorf("%s: %d calls, bound %d", p.name, calls, b)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("seed %d: %w", seed, err))
			}
		}
	}
	return errs
}

func minimizeSweep(seeds int64) []error {
	var errs []error
	for seed := int64(0); seed < seeds; seed++ {
		r := NewStream(seed, "minimize")
		in := randomFaults(r, r.IntN(6))
		for _, p := range faultPreds(r) {
			out, calls, err := runFaultPass(Minimize, in, p)
			if err == nil {
				err = checkValuesMinimal(in, out, p, true)
			}
			if b := faultCallBound(in, true, p.monotone); err == nil && calls > b {
				err = fmt.Errorf("%s: %d calls, bound %d", p.name, calls, b)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("seed %d: %w", seed, err))
			}
		}
	}
	return errs
}

func TestLowerValuesAgainstBruteForce(t *testing.T) {
	if errs := lowerValuesSweep(500); len(errs) > 0 {
		t.Fatalf("%d failures, first: %v", len(errs), errs[0])
	}
}

func TestMinimizeAgainstBruteForce(t *testing.T) {
	if errs := minimizeSweep(400); len(errs) > 0 {
		t.Fatalf("%d failures, first: %v", len(errs), errs[0])
	}
}

func TestMinimizeNilFloorLowersToZero(t *testing.T) {
	in := []Fault{{At: 900, Kind: 7, Target: 3, Arg1: 40, Arg2: 1}, {At: 5, Kind: 8, Target: 1, Arg1: 2, Arg2: 2}}
	fails := func(s []Fault) bool {
		return slices.ContainsFunc(s, func(f Fault) bool { return f.Kind == 7 && f.Arg1 >= 10 })
	}
	out := Minimize(in, fails, nil)
	want := []Fault{{At: 0, Kind: 7, Target: 3, Arg1: 10, Arg2: 0}}
	if !slices.Equal(out, want) {
		t.Fatalf("got %+v, want %+v", out, want)
	}
	if got := LowerValues(in, fails, nil); !slices.Equal(got, []Fault{want[0], {Kind: 8, Target: 1}}) {
		t.Fatalf("LowerValues got %+v", got)
	}
}

func TestMinimizeIsDeterministicAcrossProcesses(t *testing.T) {
	h := fnv.New64a()
	for seed := int64(0); seed < 120; seed++ {
		r := NewStream(seed, "minimize-digest")
		in := randomFaults(r, r.IntN(6))
		for _, p := range faultPreds(r) {
			for _, f := range Minimize(in, p.fails, testFloor) {
				fmt.Fprintf(h, "%d %d %d %d %d;", f.At, f.Kind, f.Target, f.Arg1, f.Arg2)
			}
			fmt.Fprintf(h, "|")
		}
	}
	const pinned = uint64(0xbbecbf44045a19ac)
	if got := h.Sum64(); got != pinned {
		t.Fatalf("Minimize's results over the pinned sweep hash to %#x, pinned %#x", got, pinned)
	}
}
