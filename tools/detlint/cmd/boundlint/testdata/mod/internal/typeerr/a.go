package typeerr

func New() chan int {
	return make(chan int, undefinedName)
}
