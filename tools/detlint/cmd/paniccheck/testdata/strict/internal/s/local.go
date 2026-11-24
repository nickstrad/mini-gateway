package s

func Local() {
	panic := func(string) {}
	panic("local")
}
