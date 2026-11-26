package sim

import "slices"

// FaultFloor specifies the minimum legal values for the lowerable fields
// (At, Arg1, Arg2) of a single FaultKind. Kind and Target are never lowered.
type FaultFloor struct {
	At   Time
	Arg1 int64
	Arg2 int64
}

const valuesPerFault = 3

// Test-only canary switch: when true, lowerOne accepts floor without calling
// the predicate, so the brute-force property tests prove they catch unchecked
// lowering.
var plantAcceptUnchecked bool

// LowerInts lowers each element of a copy of vals toward its floor while
// stillFails holds, repeating passes until a full pass changes nothing.
// Element i uses floors[i] when present and 0 otherwise; elements already at
// or below their floor are left untouched. Neither vals nor floors is mutated,
// and every predicate call receives a fresh slice copy.
//
// For any deterministic predicate (monotone or not): if stillFails(vals) holds
// then stillFails(out) holds, and for every i with out[i] > floor, lowering
// out[i] by 1 makes stillFails false. With C = sum(1 + bits.Len64(v - f)) and
// P = sum(v - f) over elements above their floor, LowerInts makes at most
// (1 + P) * C calls (at most 2*C when stillFails is upward-closed in each
// element).
func LowerInts(vals, floors []int64, stillFails func([]int64) bool) []int64 {
	cur := slices.Clone(vals)
	lows := make([]int64, len(vals))
	copy(lows, floors)
	for lowerPass(cur, lows, stillFails) {
	}
	return cur
}

// LowerValues lowers each fault's At, Arg1, and Arg2 in schedule order toward
// the per-kind minimum returned by floor (zero for every field when floor is
// nil), repeating passes until a full pass changes nothing. Schedule length,
// element order (not re-sorted by At), Kind, and Target never change, and
// sched is never mutated. It provides the same one-step minimality and
// predicate-call bounds as LowerInts over the flattened fault fields.
func LowerValues(sched []Fault, stillFails func([]Fault) bool, floor func(FaultKind) FaultFloor) []Fault {
	vals, lows := faultValues(sched, floor)
	for lowerPass(vals, lows, faultPredicate(sched, stillFails)) {
	}
	return withValues(sched, vals)
}

// Minimize alternates whole-fault deletion (Shrink) with a value-lowering pass
// (At, Arg1, Arg2 toward floor, or zero when floor is nil) until a full round
// changes nothing. For any deterministic predicate (monotone or not): if
// stillFails(sched) holds then stillFails(out) holds, no single fault can be
// deleted, and no single At, Arg1, or Arg2 above its floor can be lowered by 1
// while stillFails remains true. sched is never mutated; with n = len(sched)
// and C, P as in LowerInts, Minimize makes at most (1 + P) * (n*(n+1) + C)
// predicate calls.
func Minimize(sched []Fault, stillFails func([]Fault) bool, floor func(FaultKind) FaultFloor) []Fault {
	// Wrap stillFails with a clone so a predicate that mutates its argument
	// cannot corrupt Shrink's working schedule.
	isolated := func(s []Fault) bool { return stillFails(slices.Clone(s)) }
	cur := slices.Clone(sched)
	for {
		cur = Shrink(cur, isolated)
		vals, lows := faultValues(cur, floor)
		// Stopping when lowerPass makes no change proves both guarantees on
		// cur: Shrink just tested every single deletion false, and lowerPass
		// just tested every floor and one-step-down probe false on this same
		// schedule.
		if !lowerPass(vals, lows, faultPredicate(cur, stillFails)) {
			return cur
		}
		cur = withValues(cur, vals)
	}
}

func lowerPass(cur, floors []int64, stillFails func([]int64) bool) bool {
	changed := false
	for i := range cur {
		probe := func(v int64) bool {
			cand := slices.Clone(cur)
			cand[i] = v
			return stillFails(cand)
		}
		if w := lowerOne(cur[i], floors[i], probe); w != cur[i] {
			cur[i] = w
			changed = true
		}
	}
	return changed
}

func lowerOne(v, floor int64, fails func(int64) bool) int64 {
	if v <= floor {
		return v
	}
	if plantAcceptUnchecked || fails(floor) {
		return floor
	}
	// Invariant: fails(lo) is false and hi is the lowest value accepted so far.
	// Unsigned subtraction avoids signed overflow across full-width int64
	// ranges. When hi - lo == 1, lo == hi - 1 was already probed false, so
	// bisection finishes with the one-step guarantee in at most
	// 1 + bits.Len64(uint64(v)-uint64(floor)) calls.
	lo, hi := floor, v
	for uint64(hi)-uint64(lo) > 1 {
		mid := lo + int64((uint64(hi)-uint64(lo))/2)
		if fails(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

func faultValues(sched []Fault, floor func(FaultKind) FaultFloor) (vals, floors []int64) {
	vals = make([]int64, 0, valuesPerFault*len(sched))
	floors = make([]int64, 0, valuesPerFault*len(sched))
	for _, f := range sched {
		var fl FaultFloor
		if floor != nil {
			fl = floor(f.Kind)
		}
		vals = append(vals, int64(f.At), f.Arg1, f.Arg2)
		floors = append(floors, int64(fl.At), fl.Arg1, fl.Arg2)
	}
	return vals, floors
}

func withValues(sched []Fault, vals []int64) []Fault {
	out := slices.Clone(sched)
	for i := range out {
		v := vals[valuesPerFault*i:]
		out[i].At, out[i].Arg1, out[i].Arg2 = Time(v[0]), v[1], v[2]
	}
	return out
}

func faultPredicate(sched []Fault, stillFails func([]Fault) bool) func([]int64) bool {
	return func(vals []int64) bool { return stillFails(withValues(sched, vals)) }
}
