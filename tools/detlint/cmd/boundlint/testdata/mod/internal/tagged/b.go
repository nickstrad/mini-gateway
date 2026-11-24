//go:build never

package tagged

func New() chan int {
	return make(chan int)
}
