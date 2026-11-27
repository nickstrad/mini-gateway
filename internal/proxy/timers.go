package proxy

import "container/heap"

type timerKind uint8

const (
	tClientStartup timerKind = iota + 1
	tClientIdle
	tClientMid
	tClientWrite
	tQueueWait
	tDBConnect
	tDBStatement
	tDBReset
	tRedial
	tPool
	tClientIdleTx
	tAdmission
)

type timer struct {
	at     Instant
	seq    int64
	idx    int
	kind   timerKind
	client *client
	db     *dbConn
}

type timerHeap []*timer

func (h timerHeap) Len() int { return len(h) }

func (h timerHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}

func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (h *timerHeap) Push(x any) {
	t := x.(*timer)
	t.idx = len(*h)
	*h = append(*h, t)
}

func (h *timerHeap) Pop() any {
	old := *h
	t := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	t.idx = -1
	return t
}

type timers struct {
	h    timerHeap
	seq  int64
	woke Instant
}

func (ts *timers) arm(slot **timer, at Instant, kind timerKind, c *client, d *dbConn) {
	ts.cancel(slot)
	ts.seq++
	t := &timer{at: at, seq: ts.seq, kind: kind, client: c, db: d}
	heap.Push(&ts.h, t)
	*slot = t
}

func (ts *timers) cancel(slot **timer) {
	t := *slot
	if t == nil {
		return
	}
	if t.idx >= 0 && t.idx < len(ts.h) && ts.h[t.idx] == t {
		heap.Remove(&ts.h, t.idx)
	}
	*slot = nil
}

func (ts *timers) due(now Instant) *timer {
	if len(ts.h) == 0 || ts.h[0].at > now {
		return nil
	}
	return heap.Pop(&ts.h).(*timer)
}

func (ts *timers) next() Instant {
	if len(ts.h) == 0 {
		return 0
	}
	return ts.h[0].at
}

func (ts *timers) pending() int { return len(ts.h) }
