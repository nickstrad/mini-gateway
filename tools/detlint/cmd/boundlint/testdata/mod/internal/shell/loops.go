package shell

const maxQueue = 4

type Box struct {
	queue []int
	seen  map[int]bool
	count map[int]int
	in    chan int
}

func (b *Box) forever() {
	for {
		b.queue = append(b.queue, 1)
		b.seen[1] = true
		b.count[1]++
	}
}

func (b *Box) overChannel() {
	for v := range b.in {
		b.queue = append(b.queue, v)
	}
}

func (b *Box) inClosure() {
	for {
		func() {
			b.queue = append(b.queue, 1)
		}()
	}
}

func (b *Box) checked() {
	for v := range b.in {
		if len(b.queue) >= maxQueue {
			continue
		}
		b.queue = append(b.queue, v)
		if len(b.seen) < maxQueue {
			b.seen[v] = true
		}
	}
}

func (b *Box) otherLength() {
	for {
		if len(b.seen) > maxQueue {
			return
		}
		b.queue = append(b.queue, 1)
	}
}

func (b *Box) bounded(vs []int) {
	for i := 0; i < len(vs); i++ {
		b.queue = append(b.queue, vs[i])
	}
	for _, v := range vs {
		b.seen[v] = true
	}
}

func (b *Box) locals() {
	var q []int
	m := map[int]int{}
	for {
		q = append(q, 1)
		m[1] = 2
		_ = b.seen[1]
		b.queue = b.queue[:0]
	}
}

func (b *Box) allowedGrowth() {
	for {
		b.queue = append(b.queue, 1)
	}
}
