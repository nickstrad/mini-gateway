package s

func TS(x any) {
	switch panic := x.(type) {
	default:
		_ = panic
	}
}
