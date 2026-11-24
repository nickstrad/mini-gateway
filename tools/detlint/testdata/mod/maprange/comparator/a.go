package comparator

import (
	"cmp"
	"maps"
	"slices"
	"sort"
)

type byLen []string

func (b byLen) Len() int           { return len(b) }
func (b byLen) Less(i, j int) bool { return len(b[i]) < len(b[j]) }
func (b byLen) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

func SortSort(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Sort(byLen(ks))
	return ks
}

func SortSlice(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return len(ks[i]) < len(ks[j]) })
	return ks
}

func SortFunc(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.SortFunc(ks, func(a, b string) int { return cmp.Compare(len(a), len(b)) })
	return ks
}

func SortedFunc(m map[string]bool) []string {
	return slices.SortedFunc(maps.Keys(m), func(a, b string) int { return cmp.Compare(len(a), len(b)) })
}
