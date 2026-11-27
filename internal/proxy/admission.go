package proxy

import (
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// AdmissionQueue adapts admission.Queues to the engine's Queue and ShedQueue seams.
type AdmissionQueue struct {
	q    *admission.Queues
	last Instant
}

// NewAdmissionQueue constructs an AdmissionQueue backed by a validated admission.Queues core.
func NewAdmissionQueue(cfg admission.Config) (*AdmissionQueue, error) {
	q, err := admission.New(cfg)
	if err != nil {
		return nil, err
	}
	return &AdmissionQueue{q: q}, nil
}

// at keeps the adapter's clock monotonic so Remove (which carries no timestamp)
// never steps backward relative to the last timestamped call.
func (a *AdmissionQueue) at(now Instant) admission.Time {
	if now > a.last {
		a.last = now
	}
	return admission.Time(a.last)
}

// Core exposes the underlying admission.Queues so later chunks (e.g. chunk 9's
// fair dispatcher) can attach a Dispatcher without widening the Queue seam.
func (a *AdmissionQueue) Core() *admission.Queues { return a.q }

// Push enqueues request id for tenant at now.
func (a *AdmissionQueue) Push(now Instant, tenant work.TenantID, id RequestID) bool {
	return a.q.Push(a.at(now), string(tenant), int64(id))
}

// Pop dequeues the next request to serve at now.
func (a *AdmissionQueue) Pop(now Instant) (RequestID, bool) {
	id, ok := a.q.Pop(a.at(now))
	return RequestID(id), ok
}

// Remove deletes id at the last observed timestamp.
func (a *AdmissionQueue) Remove(id RequestID) bool {
	return a.q.Remove(admission.Time(a.last), int64(id))
}

// RemoveAt advances to now and deletes id as client-gone.
func (a *AdmissionQueue) RemoveAt(now Instant, id RequestID) bool {
	return a.q.Remove(a.at(now), int64(id))
}

// Expire advances to now and deletes id as a queue_wait deadline shed.
func (a *AdmissionQueue) Expire(now Instant, id RequestID) bool {
	return a.q.Expire(a.at(now), int64(id))
}

func (a *AdmissionQueue) Refuse(now Instant, id RequestID) bool {
	return a.q.Refuse(a.at(now), int64(id))
}

// Sweep advances internal admission deadlines up to now.
func (a *AdmissionQueue) Sweep(now Instant) { a.q.Sweep(a.at(now)) }

// Next returns the next virtual instant the engine must wake the admission core.
func (a *AdmissionQueue) Next() Instant { return Instant(a.q.Next()) }

// TakeShed drains one decided shed from the admission core.
func (a *AdmissionQueue) TakeShed() (RequestID, bool) {
	s, ok := a.q.TakeShed()
	return RequestID(s.ID), ok
}

// Unpop returns the last popped entry to its original ring end if checkout failed.
func (a *AdmissionQueue) Unpop() bool { return a.q.Unpop() }

// Close sweeps to now and drops empty tenant queues during engine shutdown.
func (a *AdmissionQueue) Close(now Instant) { a.q.Close(a.at(now)) }

// Len returns the number of requests currently queued.
func (a *AdmissionQueue) Len() int { return a.q.Len() }

// Cap returns the configured total queue capacity.
func (a *AdmissionQueue) Cap() int { return a.q.Cap() }

// Snapshot returns an immutable copy of the admission core's metrics and state.
func (a *AdmissionQueue) Snapshot() admission.Snapshot { return a.q.Snapshot() }

// AdmissionSnapshot returns the admission queue's snapshot, or a zero snapshot
// when the engine runs on the plain FIFO queue seam.
func (e *Engine) AdmissionSnapshot() admission.Snapshot {
	if a, ok := e.seams.Queue.(interface{ Snapshot() admission.Snapshot }); ok {
		return a.Snapshot()
	}
	return admission.Snapshot{}
}

// queueTouched drains every shed decided by the admission core, answers each
// shed client with 53M01 at once, and re-arms the engine's single tAdmission timer.
func (e *Engine) queueTouched() {
	if e.shedQ == nil {
		return
	}
	for {
		id, ok := e.shedQ.TakeShed()
		if !ok {
			break
		}
		c := e.waiting[id]
		delete(e.waiting, id)
		if c == nil || c.phase != cpQueued || c.req == nil || c.req.id != id {
			continue
		}
		e.t.cancel(&c.queueT)
		e.snap.Queued.Add(-1)
		e.snap.StageRefuse[StageQueue]++
		e.shed(c)
		e.pump(c)
	}
	if at := e.shedQ.Next(); at == 0 {
		e.t.cancel(&e.admT)
	} else if e.admT == nil || e.admT.at != at {
		e.t.arm(&e.admT, at, tAdmission, nil, nil)
	}
}

func (e *Engine) sweepAdmission() {
	if e.shedQ == nil {
		return
	}
	e.shedQ.Sweep(e.now)
	e.queueTouched()
}

func (e *Engine) closeAdmission() {
	if e.shedQ == nil {
		return
	}
	e.shedQ.Close(e.now)
	e.queueTouched()
}

func (e *Engine) admissionCore() *admission.Queues {
	if a, ok := e.seams.Queue.(*AdmissionQueue); ok {
		return a.q
	}
	return nil
}

func (e *Engine) admissionCheck() error {
	q := e.admissionCore()
	if q == nil {
		return nil
	}
	if q.Pending() != 0 {
		return fmt.Errorf("admission: %d sheds not answered", q.Pending())
	}
	if n := q.ShedCount(); n != e.snap.Shed {
		return fmt.Errorf("admission: the core shed %d, the engine answered %d", n, e.snap.Shed)
	}
	if at := q.Next(); at != 0 && (e.admT == nil || e.admT.at != Instant(at)) {
		return fmt.Errorf("admission: next deadline %d is not armed", at)
	}
	return q.Check()
}

func (e *Engine) admissionQuiet() error {
	q := e.admissionCore()
	if q == nil {
		return nil
	}
	if e.admT != nil {
		return fmt.Errorf("admission: timer armed at %d", e.admT.at)
	}
	return q.Quiesce()
}
