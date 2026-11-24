package clean

type T struct{}

func (T) Panic() {}

func Panicf(string) {}

func F(t T) {
	t.Panic()
	Panicf("x")
}
