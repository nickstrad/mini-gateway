package proxy

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type RetryEnd uint8

const (
	RetryEndCompleted RetryEnd = iota
	RetryEndFailed
	RetryEndShed
	RetryEndGone
	RetryEndBreaker
	NumRetryEnds
)

var retryEndNames = [NumRetryEnds]string{"completed", "failed", "shed", "gone", "breaker"}

func (r RetryEnd) String() string {
	if r < NumRetryEnds {
		return retryEndNames[r]
	}
	return "?"
}

type RetryCore struct{ p retry.Policy }

func NewRetryCore(p retry.Policy) *RetryCore { return &RetryCore{p: p} }

func (r *RetryCore) Retry(now Instant, f Failure) bool {
	return r.p.Decide(retry.Time(now), retry.Failure{Reached: f.Reached, InTx: f.InTx, Attempt: f.Attempt,
		BreakerOpen: f.BreakerOpen, Draining: f.Draining, Deadline: retry.Time(f.Deadline)}).Retry
}

func (r *RetryCore) Attempt(now Instant) { r.p.Attempt(retry.Time(now)) }

func (r *RetryCore) Snapshot() retry.Snapshot { return r.p.Snapshot() }

type retryAttempts interface{ Attempt(now Instant) }

type breakerState interface{ Open() bool }

func (b CoreBreaker) Open() bool { return b.cp.p.Breaker() != pool.BreakerClosed }

func (ClosedBreaker) Open() bool { return false }

func (e *Engine) initRetry() error {
	if e.seams.Retry == nil {
		p, err := retry.New(e.cfg.Retry)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrConfig, err)
		}
		e.seams.Retry = NewRetryCore(p)
	}
	return nil
}

func (e *Engine) RetrySnapshot() (retry.Snapshot, bool) {
	if rc, ok := e.seams.Retry.(*RetryCore); ok {
		return rc.Snapshot(), true
	}
	return retry.Snapshot{}, false
}

func (e *Engine) requestDeadline(req *request) Instant {
	return addDuration(req.enq, e.cfg.Timeouts.Statement)
}

func addDuration(at Instant, d time.Duration) Instant {
	if int64(at) > math.MaxInt64-int64(d) {
		return Instant(math.MaxInt64)
	}
	return at + Instant(d)
}

func (e *Engine) retryClamp(req *request, at Instant) Instant {
	if req == nil || req.attempt == 0 {
		return at
	}
	return min(at, e.requestDeadline(req))
}

func (e *Engine) attempted(req *request) {
	if a, ok := e.seams.Retry.(retryAttempts); ok && req.attempt == 0 {
		a.Attempt(e.now)
	}
}

func (e *Engine) breakerOpen() bool {
	if b, ok := e.seams.Breaker.(breakerState); ok {
		return b.Open()
	}
	return false
}

func (e *Engine) refuseRetriesBehindOpenBreaker() bool {
	if !e.breakerOpen() {
		return false
	}
	ids := make([]RequestID, 0, len(e.waiting))
	for id := range e.waiting {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	refused := false
	for _, id := range ids {
		c := e.waiting[id]
		if c == nil || c.phase != cpQueued || c.req == nil || c.req.id != id || c.req.attempt == 0 || c.req.retryOver {
			continue
		}
		if !e.takeQueued(id) {
			continue
		}
		delete(e.waiting, id)
		e.refuseWaiting(c, RetryEndBreaker)
		refused = true
	}
	if refused {
		e.queueTouched()
	}
	return refused
}

func (e *Engine) takeQueued(id RequestID) bool {
	if e.shedQ != nil {
		return e.shedQ.Refuse(e.now, id)
	}
	return e.seams.Queue.Remove(id)
}

func (e *Engine) offerRetry(c *client, req *request, reached work.Tri, row pgwire.ErrRowID) bool {
	f := Failure{Request: req.id, Tenant: c.tenant, Reached: reached, Row: uint8(row), InTx: req.inTx, Attempt: req.attempt,
		BreakerOpen: e.breakerOpen(), Draining: e.draining, Deadline: e.requestDeadline(req)}
	e.snap.RetryOffered++
	if !e.seams.Retry.Retry(e.now, f) {
		return false
	}
	e.snap.RetryAsked++
	e.snap.RetryRows[row]++
	return true
}

func (e *Engine) rerun(c *client, req *request) {
	if req.attempt == 0 {
		e.snap.RetriedRequests++
		e.snap.RetryPending.Add(1)
	}
	req.attempt++
	req.relayed = 0
	c.req = req
	c.phase = cpIdle
	c.status = pgwire.StatusIdle
	e.enqueue(c)
	if c.phase == cpIdle {
		e.pump(c)
	}
}

func (e *Engine) retryEnded(req *request, how RetryEnd) {
	if req == nil || req.attempt == 0 || req.retryOver {
		return
	}
	req.retryOver = true
	e.snap.RetryPending.Add(-1)
	e.snap.RetryEnds[how]++
}

func (e *Engine) retryCheck() error {
	var errs []error
	pending := 0
	for _, c := range e.clientList {
		if c.req != nil && c.req.attempt > 0 && !c.req.retryOver {
			pending++
		}
	}
	if int64(pending) != e.snap.RetryPending.Value {
		errs = append(errs, fmt.Errorf("retry: %d retried requests alive, gauge says %d", pending, e.snap.RetryPending.Value))
	}
	if s, ok := e.RetrySnapshot(); ok {
		if err := s.Check(); err != nil {
			errs = append(errs, err)
		}
		if s.Offered != e.snap.RetryOffered || s.Retries != e.snap.RetryAsked {
			errs = append(errs, fmt.Errorf("retry: the policy saw %d offers and granted %d, the engine offered %d and was granted %d",
				s.Offered, s.Retries, e.snap.RetryOffered, e.snap.RetryAsked))
		}
	}
	return errors.Join(errs...)
}

func (s Snapshot) retryConserved() error {
	var ended int64
	for _, v := range s.RetryEnds {
		ended += v
	}
	if s.RetriedRequests != ended+s.RetryPending.Value {
		return fmt.Errorf("retry: %d requests retried, %d ended and %d pending", s.RetriedRequests, ended, s.RetryPending.Value)
	}
	var rows int64
	for _, v := range s.RetryRows {
		rows += v
	}
	if rows != s.RetryAsked {
		return fmt.Errorf("retry: %d retries by row for %d granted", rows, s.RetryAsked)
	}
	if s.RetriedRequests > s.RetryAsked || s.RetryAsked > s.RetryOffered {
		return fmt.Errorf("retry: %d requests retried, %d retries granted for %d failures offered", s.RetriedRequests, s.RetryAsked, s.RetryOffered)
	}
	return nil
}

func retryTimeoutRows(t Timeouts) []TimeoutRow {
	return []TimeoutRow{
		{"A request's deadline for a retry by the proxy (from the request's arrival)", "engine, retry at the loss of a statement",
			"timeouts.statement", t.Statement, "No retry at or after it; a retry's queue wait and statement deadline end at it (ERROR 53M01, or 08006/08007)"},
	}
}

func retryCapacityRow(c Config) CapacityRow {
	r := c.Retry.WithDefaults()
	return CapacityRow{"Retry budget (pre-forward-once): a tenth of a token per first attempt, one token per retry",
		"the database from retries the proxy makes itself", itoa(int(r.CapTokens)) + " tokens per instance (zero means the default): the largest burst of retries, " +
			"even after a quiet period (stored credit does not expire with time); starts at retry.floor (" + itoa(int(r.FloorTokens)) +
			") divided by fleet.instances", "retry.bucket_cap, retry.floor",
		"Credit past the cap is lost (expired); with less than one token the failure is answered with its row (exhaustion)", "-", "retry.budget_level"}
}
