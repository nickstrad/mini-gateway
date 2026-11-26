package proxy

import "github.com/nickstrad/mini-gateway/internal/work"

type Stage uint8

const (
	StageClassify Stage = iota
	StageReject
	StageDraining
	StageBreaker
	StageRate
	StageCoalesce
	StageQueue
	StageLease
	StageForward
	StageRelease
	NumStages
)

var stageNames = [NumStages]string{"classify", "reject", "draining", "breaker", "rate", "coalesce", "queue", "lease",
	"forward", "release"}

func (s Stage) String() string {
	if s < NumStages {
		return stageNames[s]
	}
	return "?"
}

type RequestID int64

type Classifier interface {
	Classify(tenant work.TenantID, text []byte, inTx bool) work.Unit
}

type Breaker interface {
	Allow(now Instant) bool
	Observe(now Instant, o work.Outcome)
}

type RateGate interface {
	Allow(now Instant, tenant work.TenantID) bool
}

type Coalescer interface {
	Join(now Instant, id RequestID, u *work.Unit) bool
	Leave(now Instant, id RequestID)
}

type Queue interface {
	Push(now Instant, tenant work.TenantID, id RequestID) bool
	Pop(now Instant) (RequestID, bool)
	Remove(id RequestID) bool
	Len() int
	Cap() int
}

type Pool interface {
	Checkout(now Instant, tenant work.TenantID) (ConnID, bool)
	Checkin(now Instant, id ConnID)
	Remove(id ConnID)
	Idle() int
}

type ReleaseAction uint8

const (
	ReleaseReset ReleaseAction = iota + 1
	ReleaseLendAsIs
	ReleaseClose
)

var releaseNames = [...]string{"?", "reset", "lend-as-is", "close"}

func (a ReleaseAction) String() string {
	if int(a) < len(releaseNames) {
		return releaseNames[a]
	}
	return "?"
}

type ReleaseInput struct {
	Conn       ConnID
	Tenant     work.TenantID
	LastTenant work.TenantID
	Status     byte
	Unread     bool
}

type ReleasePolicy interface {
	AtRelease(now Instant, in ReleaseInput) ReleaseAction
}

type Failure struct {
	Request RequestID
	Tenant  work.TenantID
	Reached work.Tri
	Row     uint8
	InTx    bool
}

type RetryPolicy interface {
	Retry(now Instant, f Failure) bool
}

type Seams struct {
	Classifier Classifier
	Breaker    Breaker
	Rate       RateGate
	Coalescer  Coalescer
	Queue      Queue
	Pool       Pool
	Release    ReleasePolicy
	Retry      RetryPolicy
}

type ClosedBreaker struct{}

func (ClosedBreaker) Allow(Instant) bool            { return true }
func (ClosedBreaker) Observe(Instant, work.Outcome) {}

type OpenRate struct{}

func (OpenRate) Allow(Instant, work.TenantID) bool { return true }

type NoCoalesce struct{}

func (NoCoalesce) Join(Instant, RequestID, *work.Unit) bool { return false }
func (NoCoalesce) Leave(Instant, RequestID)                 {}

type RetryOff struct{}

func (RetryOff) Retry(Instant, Failure) bool { return false }

type ResetAlwaysPolicy struct{}

func (ResetAlwaysPolicy) AtRelease(_ Instant, in ReleaseInput) ReleaseAction {
	if in.Status != 'I' || in.Unread {
		return ReleaseClose
	}
	return ReleaseReset
}

type queueEntry struct {
	id     RequestID
	tenant work.TenantID
}

type FIFO struct {
	items []queueEntry
	head  int
	n     int
}

func NewFIFO(capacity int) *FIFO { return &FIFO{items: make([]queueEntry, capacity)} }

func (q *FIFO) Len() int { return q.n }
func (q *FIFO) Cap() int { return len(q.items) }

func (q *FIFO) Push(_ Instant, tenant work.TenantID, id RequestID) bool {
	if q.n >= len(q.items) {
		return false
	}
	q.items[(q.head+q.n)%len(q.items)] = queueEntry{id: id, tenant: tenant}
	q.n++
	return true
}

func (q *FIFO) Pop(Instant) (RequestID, bool) {
	if q.n == 0 {
		return 0, false
	}
	e := q.items[q.head]
	q.items[q.head] = queueEntry{}
	q.head = (q.head + 1) % len(q.items)
	q.n--
	return e.id, true
}

func (q *FIFO) Remove(id RequestID) bool {
	for i := 0; i < q.n; i++ {
		if q.items[(q.head+i)%len(q.items)].id != id {
			continue
		}
		for j := i; j < q.n-1; j++ {
			q.items[(q.head+j)%len(q.items)] = q.items[(q.head+j+1)%len(q.items)]
		}
		q.items[(q.head+q.n-1)%len(q.items)] = queueEntry{}
		q.n--
		return true
	}
	return false
}

type FixedSet struct {
	idle []ConnID
	cap  int
}

func NewFixedSet(n int) *FixedSet { return &FixedSet{idle: make([]ConnID, 0, n), cap: n} }

func (p *FixedSet) Idle() int { return len(p.idle) }

func (p *FixedSet) Checkout(Instant, work.TenantID) (ConnID, bool) {
	if len(p.idle) == 0 {
		return 0, false
	}
	id := p.idle[0]
	copy(p.idle, p.idle[1:])
	p.idle = p.idle[:len(p.idle)-1]
	return id, true
}

func (p *FixedSet) Checkin(_ Instant, id ConnID) {
	if len(p.idle) < p.cap {
		p.idle = append(p.idle, id)
	}
}

func (p *FixedSet) Remove(id ConnID) {
	for i, c := range p.idle {
		if c == id {
			p.idle = append(p.idle[:i], p.idle[i+1:]...)
			return
		}
	}
}
