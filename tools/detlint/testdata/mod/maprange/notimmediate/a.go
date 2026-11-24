package notimmediate

import (
	"slices"
	"sort"
)

func Later(m map[string]int) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	n := len(ks)
	sort.Strings(ks)
	return ks[:n]
}

func TwoSlices(m map[string]int) ([]string, []int) {
	var ks []string
	var vs []int
	for k, v := range m {
		ks = append(ks, k)
		vs = append(vs, v)
	}
	slices.Sort(ks)
	slices.Sort(vs)
	return ks, vs
}

func OtherSlice(m map[string]int, other []string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(other)
	return ks
}

func InLiteral(m map[string]int) func() []string {
	return func() []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		return ks
	}
}
