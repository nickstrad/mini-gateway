package channel

func F() int {
	c := make(chan int, 1)
	c <- 1
	v := <-c
	close(c)
	return v
}
