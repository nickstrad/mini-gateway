package proxy

import (
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type AdmissionQueue struct {
	q    *admission.Queues
	last Instant
}

func NewAdmissionQueue(cfg admission.Config) (*AdmissionQueue, error) {
	q, err := admission.New(cfg)
	if err != nil {
		return nil, err
	}
	return &AdmissionQueue{q: q}, nil
}

func (a *AdmissionQueue) at(now Instant) admission.Time {
	if now > a.last {
		a.last = now
	}
	return admission.Time(a.last)
}

func (a *AdmissionQueue) Core() *admission.Queues { return a.q }

func (a *AdmissionQueue) Push(now Instant, tenant work.TenantID, id RequestID) bool {
	return a.q.Push(a.at(now), string(tenant), int64(id))
}

func (a *AdmissionQueue) Pop(now Instant) (RequestID, bool) {
	id, ok := a.q.Pop(a.at(now))
	return RequestID(id), ok
}

func (a *AdmissionQueue) Remove(id RequestID) bool {
	return a.q.Remove(admission.Time(a.last), int64(id))
}

func (a *AdmissionQueue) RemoveAt(now Instant, id RequestID) bool {
	return a.q.Remove(a.at(now), int64(id))
}

func (a *AdmissionQueue) Expire(now Instant, id RequestID) bool {
	return a.q.Expire(a.at(now), int64(id))
}

func (a *AdmissionQueue) Sweep(now Instant) { a.q.Sweep(a.at(now)) }

func (a *AdmissionQueue) Next() Instant { return Instant(a.q.Next()) }

func (a *AdmissionQueue) TakeShed() (RequestID, bool) {
	s, ok := a.q.TakeShed()
	return RequestID(s.ID), ok
}

func (a *AdmissionQueue) Unpop() bool { return a.q.Unpop() }

func (a *AdmissionQueue) Close(now Instant) { a.q.Close(a.at(now)) }

func (a *AdmissionQueue) Len() int { return a.q.Len() }

func (a *AdmissionQueue) Cap() int { return a.q.Cap() }

func (a *AdmissionQueue) Snapshot() admission.Snapshot { return a.q.Snapshot() }

func (e *Engine) AdmissionSnapshot() admission.Snapshot {
	if a, ok := e.seams.Queue.(interface{ Snapshot() admission.Snapshot }); ok {
		return a.Snapshot()
	}
	return admission.Snapshot{}
}

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
