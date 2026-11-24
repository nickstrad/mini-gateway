package sub

func New() chan int {
	return make(chan int, 3)
}
