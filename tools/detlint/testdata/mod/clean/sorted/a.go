package sorted

import (
	"maps"
	"slices"
	"sort"
)

func Keys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func Values(m map[string]int) []int {
	var vs []int
	for _, v := range m {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}

func Both(m map[int]int) []int {
	var xs []int
	for k, v := range m {
		xs = append(xs, k, v)
	}
	slices.Sort(xs)
	return xs
}

func Lit(m map[string]bool) func() []string {
	return func() []string {
		var ks []string
	outer:
		for k := range m {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		if len(ks) > 3 {
			goto outer
		}
		return ks
	}
}

func Empty(m map[string]int) {
	for range m {
	}
}

func Iter(m map[string]int) []string {
	return slices.Sorted(maps.Keys(m))
}
