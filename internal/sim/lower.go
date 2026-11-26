package sim

import "slices"

type FaultFloor struct {
	At   Time
	Arg1 int64
	Arg2 int64
}

const valuesPerFault = 3

var plantAcceptUnchecked bool

func LowerInts(vals, floors []int64, stillFails func([]int64) bool) []int64 {
	cur := slices.Clone(vals)
	lows := make([]int64, len(vals))
	copy(lows, floors)
	for lowerPass(cur, lows, stillFails) {
	}
	return cur
}

func LowerValues(sched []Fault, stillFails func([]Fault) bool, floor func(FaultKind) FaultFloor) []Fault {
	vals, lows := faultValues(sched, floor)
	for lowerPass(vals, lows, faultPredicate(sched, stillFails)) {
	}
	return withValues(sched, vals)
}

func Minimize(sched []Fault, stillFails func([]Fault) bool, floor func(FaultKind) FaultFloor) []Fault {
	isolated := func(s []Fault) bool { return stillFails(slices.Clone(s)) }
	cur := slices.Clone(sched)
	for {
		cur = Shrink(cur, isolated)
		vals, lows := faultValues(cur, floor)
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
