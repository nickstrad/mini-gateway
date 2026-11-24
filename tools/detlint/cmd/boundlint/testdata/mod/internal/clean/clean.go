package clean

const size = 4

func New() chan int {
	return make(chan int, size)
}
