package admission

type entry struct {
	id  int64
	enq Time
	seq int64
}

type ring struct {
	items []entry
	head  int
	n     int
}

func newRing(capacity int) ring { return ring{items: make([]entry, capacity)} }

func (r *ring) len() int { return r.n }

func (r *ring) cap() int { return len(r.items) }

func (r *ring) full() bool { return r.n == len(r.items) }

func (r *ring) at(i int) *entry { return &r.items[(r.head+i)%len(r.items)] }

func (r *ring) front() entry { return *r.at(0) }

func (r *ring) back() entry { return *r.at(r.n - 1) }

func (r *ring) pushBack(e entry) bool {
	if r.full() {
		return false
	}
	*r.at(r.n) = e
	r.n++
	return true
}

func (r *ring) pushFront(e entry) bool {
	if r.full() {
		return false
	}
	r.head = (r.head - 1 + len(r.items)) % len(r.items)
	r.items[r.head] = e
	r.n++
	return true
}

func (r *ring) popFront() entry {
	e := r.front()
	*r.at(0) = entry{}
	r.head = (r.head + 1) % len(r.items)
	r.n--
	return e
}

func (r *ring) popBack() entry {
	e := r.back()
	*r.at(r.n - 1) = entry{}
	r.n--
	return e
}

func (r *ring) index(id int64) int {
	for i := 0; i < r.n; i++ {
		if r.at(i).id == id {
			return i
		}
	}
	return -1
}

func (r *ring) removeAt(i int) entry {
	e := *r.at(i)
	for j := i; j < r.n-1; j++ {
		*r.at(j) = *r.at(j + 1)
	}
	*r.at(r.n - 1) = entry{}
	r.n--
	return e
}

func (r *ring) removeSwapTail(i int) entry {
	e := *r.at(i)
	*r.at(i) = *r.at(r.n - 1)
	*r.at(r.n - 1) = entry{}
	r.n--
	return e
}

func (r *ring) reset() {
	clear(r.items)
	r.head, r.n = 0, 0
}
