package a

type T struct{}

type G[K comparable] struct{ k K }

var initDone = func() bool {
	panic("package-level")
}()

func F(x int) int {
	if x < 0 {
		panic("negative")
	}
	return x
}

func (t *T) M() {
	func() {
		panic("in literal")
	}()
}

func (g G[K]) Get() K {
	panic("generic")
}
