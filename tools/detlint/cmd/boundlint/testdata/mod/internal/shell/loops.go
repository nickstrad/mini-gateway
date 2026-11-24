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

var backlog []int

var index = map[int]int{}

func (b *Box) throughPointer() {
	q := &b.queue
	for {
		*q = append(*q, 1)
	}
}

func (b *Box) throughMapCopy() {
	seen := b.seen
	for {
		seen[1] = true
	}
}

func (b *Box) throughParameter(m map[int]bool, p *[]int) {
	for {
		m[1] = true
		*p = append(*p, 1)
		(*p) = append(*p, 2)
	}
}

func (b *Box) packageVariables() {
	for {
		backlog = append(backlog, 1)
		index[1]++
	}
}

func (b *Box) pointerChecked() {
	q := &b.queue
	seen := b.seen
	for {
		if len(*q) < maxQueue && len(seen) < maxQueue {
			*q = append(*q, 1)
			seen[1] = true
		}
	}
}

func (b *Box) push(v int) {
	b.queue = append(b.queue, v)
}

func (b *Box) pushTwice(v int) {
	b.push(v)
	b.push(v)
}

func (b *Box) pushChecked(v int) {
	if len(b.queue) < maxQueue {
		b.queue = append(b.queue, v)
	}
}

func grow(p *[]int) {
	*p = append(*p, 1)
}

func note[K comparable](m map[K]bool, k K) {
	m[k] = true
}

func (b *Box) callsHelper() {
	for {
		b.push(1)
	}
}

func (b *Box) callsHelperOfHelper() {
	for v := range b.in {
		b.pushTwice(v)
	}
}

func (b *Box) callsOtherHelpers() {
	for {
		grow(&b.queue)
		note(b.seen, 1)
		note[int](b.seen, 2)
		go b.push(3)
	}
}

func (b *Box) helperChecked() {
	for {
		b.pushChecked(1)
	}
}

func (b *Box) callerChecks(other *Box) {
	for {
		if len(other.queue) >= maxQueue {
			return
		}
		other.push(1)
	}
}

func (b *Box) recursive(n int) {
	if n > 0 {
		b.recursive(n - 1)
	}
	b.count[n]++
}

func (b *Box) callsRecursive() {
	for {
		b.recursive(3)
	}
}

func (b *Box) helperOutsideLoop() {
	b.push(1)
	for i := 0; i < maxQueue; i++ {
		b.push(i)
	}
}
