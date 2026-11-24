package unsortedcollect

func Keys(m map[string]int) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
