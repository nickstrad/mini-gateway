package outervar

import "slices"

func Last(m map[string]int) string {
	var last string
	var ks []string
	for last = range m {
		ks = append(ks, last)
	}
	slices.Sort(ks)
	return last
}

func LastEmptyBody(m map[string]int) string {
	var last string
	for last = range m {
	}
	return last
}

func LastValue(m map[string]int) int {
	var v int
	for _, v = range m {
	}
	return v
}

type holder struct{ k string }

func LastField(m map[string]int) string {
	var h holder
	for h.k = range m {
	}
	return h.k
}

func Safe(m map[string]int) int {
	n := 0
	for range m {
	}
	for _ = range m {
	}
	for _, _ = range m {
	}
	return n
}
