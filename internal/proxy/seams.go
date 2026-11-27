package proxy

import "github.com/nickstrad/mini-gateway/internal/work"

// Stage identifies one step in the fixed fail-fast request pipeline (PLAN.md 10.1).
type Stage uint8

const (
	// StageClassify classifies the SQL text into a protocol-free work.Unit.
	StageClassify Stage = iota
	// StageReject refuses courtesy reject-list statements and extended protocol messages.
	StageReject
	// StageDraining refuses new work from idle clients while draining is set.
	StageDraining
	// StageBreaker consults the circuit breaker before queueing.
	StageBreaker
	// StageRate consults the per-tenant rate gate before queueing.
	StageRate
	// StageCoalesce offers eligible reads to the in-flight coalescer.
	StageCoalesce
	// StageQueue admits the request into the bounded admission queue.
	StageQueue
	// StageLease acquires an idle database connection for the transaction.
	StageLease
	// StageForward writes the Query to the lent database connection.
	StageForward
	// StageRelease runs the release policy when ReadyForQuery reports status 'I'.
	StageRelease
	// NumStages is the number of pipeline stages for fixed-size metric arrays.
	NumStages
)

var stageNames = [NumStages]string{"classify", "reject", "draining", "breaker", "rate", "coalesce", "queue", "lease",
	"forward", "release"}

// String returns the canonical stage label name.
func (s Stage) String() string {
	if s < NumStages {
		return stageNames[s]
	}
	return "?"
}

// RequestID uniquely identifies one accepted client request inside the engine.
type RequestID int64

// Classifier turns raw query bytes (without trailing NUL) into a work.Unit.
type Classifier interface {
	Classify(tenant work.TenantID, text []byte, inTx bool) work.Unit
}

// Breaker gates new work when the database is unhealthy (pass-through in chunk 6; chunk 7).
type Breaker interface {
	Allow(now Instant) bool
	Observe(now Instant, o work.Outcome)
}

// RateGate enforces per-tenant admission rates (pass-through in chunk 6; chunk 10).
type RateGate interface {
	Allow(now Instant, tenant work.TenantID) bool
}

// Coalescer shares identical in-flight reads (pass-through in chunk 6; chunk 11).
// Returning true from Join means the request waits on a shared flight and takes no queue slot.
type Coalescer interface {
	Join(now Instant, id RequestID, u *work.Unit) bool
	Leave(now Instant, id RequestID)
}

// Queue holds accepted requests waiting for a database connection (FIFO in chunk 6; chunks 8–9).
type Queue interface {
	Push(now Instant, tenant work.TenantID, id RequestID) bool
	Pop(now Instant) (RequestID, bool)
	Remove(id RequestID) bool
	Len() int
	Cap() int
}

// Pool tracks idle database connection IDs eligible to be lent (FixedSet in chunk 6; chunk 7).
// Protocol and reset state stay owned by the engine.
type Pool interface {
	Checkout(now Instant, tenant work.TenantID) (ConnID, bool)
	Checkin(now Instant, id ConnID)
	Remove(id ConnID)
	Idle() int
}

// ReleaseAction is the decision returned by ReleasePolicy.AtRelease when a lease ends.
type ReleaseAction uint8

const (
	// ReleaseReset sends DISCARD ALL and returns the connection to idle only after its verified reply.
	ReleaseReset ReleaseAction = iota + 1
	// ReleaseLendAsIs returns the connection to idle without resetting (used by chunk 7 on-switch).
	ReleaseLendAsIs
	// ReleaseClose destroys the database connection immediately.
	ReleaseClose
)

var releaseNames = [...]string{"?", "reset", "lend-as-is", "close"}

// String returns the canonical name of the release action.
func (a ReleaseAction) String() string {
	if int(a) < len(releaseNames) {
		return releaseNames[a]
	}
	return "?"
}

// ReleaseInput carries the lease-end facts inspected by ReleasePolicy.
type ReleaseInput struct {
	Conn       ConnID
	Tenant     work.TenantID
	LastTenant work.TenantID
	Status     byte
	Unread     bool
}

// ReleasePolicy decides whether a released database connection is reset, lent
// as is, or closed (decision D8).
type ReleasePolicy interface {
	AtRelease(now Instant, in ReleaseInput) ReleaseAction
}

// Failure describes a post-forward statement failure passed to RetryPolicy.
type Failure struct {
	Request RequestID
	Tenant  work.TenantID
	Reached work.Tri
	Row     uint8
	InTx    bool
}

// RetryPolicy decides whether a failed request may be retried (pass-through in chunk 6; chunk 12).
type RetryPolicy interface {
	Retry(now Instant, f Failure) bool
}

// Seams holds the pluggable stage implementations used by Engine. Any nil field
// falls back to chunk 6's default or pass-through implementation.
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

// ClosedBreaker is the chunk 6 pass-through Breaker that always allows work.
type ClosedBreaker struct{}

// Allow always returns true.
func (ClosedBreaker) Allow(Instant) bool { return true }

// Observe is a no-op.
func (ClosedBreaker) Observe(Instant, work.Outcome) {}

// OpenRate is the chunk 6 pass-through RateGate that never throttles.
type OpenRate struct{}

// Allow always returns true.
func (OpenRate) Allow(Instant, work.TenantID) bool { return true }

// NoCoalesce is the chunk 6 pass-through Coalescer that never joins a flight.
type NoCoalesce struct{}

// Join always returns false.
func (NoCoalesce) Join(Instant, RequestID, *work.Unit) bool { return false }

// Leave is a no-op.
func (NoCoalesce) Leave(Instant, RequestID) {}

// RetryOff is the chunk 6 pass-through RetryPolicy that never retries.
type RetryOff struct{}

// Retry always returns false.
func (RetryOff) Retry(Instant, Failure) bool { return false }

// ResetAlwaysPolicy is the chunk 6 ReleasePolicy implementing decision D8:
// reset with DISCARD ALL when status is 'I' and nothing is unread, else close.
type ResetAlwaysPolicy struct{}

// AtRelease returns ReleaseReset only for a clean idle boundary, otherwise ReleaseClose.
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

// FIFO is a single bounded ring-buffer Queue used in chunk 6 before per-tenant queues land.
type FIFO struct {
	items []queueEntry
	head  int
	n     int
}

// NewFIFO allocates a bounded FIFO queue holding up to capacity requests.
func NewFIFO(capacity int) *FIFO { return &FIFO{items: make([]queueEntry, capacity)} }

// Len returns the current number of queued requests.
func (q *FIFO) Len() int { return q.n }

// Cap returns the maximum capacity of the queue.
func (q *FIFO) Cap() int { return len(q.items) }

// Push appends id to the tail of the queue, returning false if full.
func (q *FIFO) Push(_ Instant, tenant work.TenantID, id RequestID) bool {
	if q.n >= len(q.items) {
		return false
	}
	q.items[(q.head+q.n)%len(q.items)] = queueEntry{id: id, tenant: tenant}
	q.n++
	return true
}

// Pop removes and returns the oldest queued request ID.
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

// Remove deletes id from anywhere in the queue while preserving FIFO order.
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

// FixedSet is the chunk 6 Pool tracking up to cap idle database connection IDs in FIFO order.
type FixedSet struct {
	idle []ConnID
	cap  int
}

// NewFixedSet allocates a FixedSet with room for n idle connection IDs.
func NewFixedSet(n int) *FixedSet { return &FixedSet{idle: make([]ConnID, 0, n), cap: n} }

// Idle returns the number of currently idle connections.
func (p *FixedSet) Idle() int { return len(p.idle) }

// Checkout pops and returns the longest-idle connection ID.
func (p *FixedSet) Checkout(Instant, work.TenantID) (ConnID, bool) {
	if len(p.idle) == 0 {
		return 0, false
	}
	id := p.idle[0]
	copy(p.idle, p.idle[1:])
	p.idle = p.idle[:len(p.idle)-1]
	return id, true
}

// Checkin appends id to the idle list if below capacity.
func (p *FixedSet) Checkin(_ Instant, id ConnID) {
	if len(p.idle) < p.cap {
		p.idle = append(p.idle, id)
	}
}

// Remove deletes id from the idle list if present.
func (p *FixedSet) Remove(id ConnID) {
	for i, c := range p.idle {
		if c == id {
			p.idle = append(p.idle[:i], p.idle[i+1:]...)
			return
		}
	}
}
