package allowed

func Count(m map[string]int) int {
	n := 0
	for range m {
		n++
	}
	return n
}
