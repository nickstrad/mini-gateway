package sim

// Indexed min-heap copied from object-store internal/sim/queue.go at commit
// 25fb899317955eb7d87186f940702e55dc0e3cde, with domain event kinds removed.

// Handler receives scheduled events without allocating a closure per event.
// The code is opaque to the kernel and interpreted only by the handler.
type Handler interface {
	OnEvent(code uint64)
}

type event struct {
	at  Time
	seq uint64
	// hi tracks the event's slot in eventQueue so Timer.Cancel can remove it
	// in O(log n); -1 means the event is no longer queued.
	hi         int
	fn         func()
	h          Handler
	code       uint64
	background bool
}

// eventQueue is a binary min-heap ordered by (at, seq). Tie-breaking on the
// monotonic scheduling sequence guarantees deterministic FIFO order within an
// instant regardless of heap swaps.
type eventQueue []*event

func (q eventQueue) less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}

func (q eventQueue) swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].hi = i
	q[j].hi = j
}

func (q eventQueue) up(j int) {
	for j > 0 {
		i := (j - 1) / 2
		if !q.less(j, i) {
			break
		}
		q.swap(i, j)
		j = i
	}
}

func (q eventQueue) down(i0 int) bool {
	i := i0
	n := len(q)
	for {
		l := 2*i + 1
		if l >= n {
			break
		}
		j := l
		if r := l + 1; r < n && q.less(r, l) {
			j = r
		}
		if !q.less(j, i) {
			break
		}
		q.swap(i, j)
		i = j
	}
	return i > i0
}

func (q *eventQueue) push(e *event) {
	e.hi = len(*q)
	*q = append(*q, e)
	q.up(e.hi)
}

func (q *eventQueue) pop() *event {
	old := *q
	n := len(old) - 1
	old.swap(0, n)
	e := old[n]
	old[n] = nil
	*q = old[:n]
	if n > 0 {
		q.down(0)
	}
	e.hi = -1
	return e
}

func (q *eventQueue) remove(e *event) bool {
	i := e.hi
	if i < 0 || i >= len(*q) || (*q)[i] != e {
		return false
	}
	old := *q
	n := len(old) - 1
	if i != n {
		old.swap(i, n)
	}
	old[n] = nil
	*q = old[:n]
	// The element swapped in from the tail may belong either below or above i.
	if i != n {
		if !q.down(i) {
			q.up(i)
		}
	}
	e.hi = -1
	return true
}

func (q eventQueue) peek() *event {
	if len(q) == 0 {
		return nil
	}
	return q[0]
}
