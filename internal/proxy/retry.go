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

// RetryEnd classifies how a request that the proxy ran again eventually ended.
// Every retried request records exactly one RetryEnd on its final exit path.
type RetryEnd uint8

const (
	// RetryEndCompleted means the retried request received a full reply.
	RetryEndCompleted RetryEnd = iota
	// RetryEndFailed means the retry failed with an error or was cancelled.
	RetryEndFailed
	// RetryEndShed means the retry was shed in the queue (rule, overflow,
	// tenant cap, request deadline, or pool-core breaker flush).
	RetryEndShed
	// RetryEndGone means the client disconnected or the engine shut down while
	// the retry was queued or in flight.
	RetryEndGone
	// RetryEndBreaker means the breaker opened after the grant and the retry
	// was refused at dispatch before any byte was forwarded.
	RetryEndBreaker
	// NumRetryEnds is the sentinel count of RetryEnd values.
	NumRetryEnds
)

var retryEndNames = [NumRetryEnds]string{"completed", "failed", "shed", "gone", "breaker"}

// String returns the label value of r, or "?" for an out-of-range RetryEnd.
func (r RetryEnd) String() string {
	if r < NumRetryEnds {
		return retryEndNames[r]
	}
	return "?"
}

// RetryCore adapts a retry.Policy core to the engine's RetryPolicy seam while
// also exposing first-attempt notifications and snapshots.
type RetryCore struct{ p retry.Policy }

// NewRetryCore wraps p as a RetryPolicy seam backed by the retry core.
func NewRetryCore(p retry.Policy) *RetryCore { return &RetryCore{p: p} }

// Retry forwards a lost-statement failure to the underlying retry.Policy.
func (r *RetryCore) Retry(now Instant, f Failure) bool {
	return r.p.Decide(retry.Time(now), retry.Failure{Reached: f.Reached, InTx: f.InTx, Attempt: f.Attempt,
		BreakerOpen: f.BreakerOpen, Draining: f.Draining, Deadline: retry.Time(f.Deadline)}).Retry
}

// Attempt records a forwarded first attempt with the underlying retry.Policy.
func (r *RetryCore) Attempt(now Instant) { r.p.Attempt(retry.Time(now)) }

// Snapshot returns the current retry.Snapshot from the underlying policy.
func (r *RetryCore) Snapshot() retry.Snapshot { return r.p.Snapshot() }

type retryAttempts interface{ Attempt(now Instant) }

type breakerState interface{ Open() bool }

// Open reports whether the pool breaker is open or half-open without calling
// Allow, so inspecting it for retries never counts as demand or starts a trial.
func (b CoreBreaker) Open() bool { return b.cp.p.Breaker() != pool.BreakerClosed }

// Open always reports false for ClosedBreaker.
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

// RetrySnapshot returns the retry core's snapshot and true when the engine's
// retry seam is a *RetryCore.
func (e *Engine) RetrySnapshot() (retry.Snapshot, bool) {
	if rc, ok := e.seams.Retry.(*RetryCore); ok {
		return rc.Snapshot(), true
	}
	return retry.Snapshot{}, false
}

// requestDeadline anchors retry expiry to the original arrival instant rather
// than req.enq, which strict-mode promotion updates for queue-wait histograms.
func (e *Engine) requestDeadline(req *request) Instant {
	return addDuration(req.arrived, e.cfg.Timeouts.Statement)
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
	// Collect and sort IDs before mutating e.waiting so refusal order is
	// deterministic and satisfies the determinism lint.
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
		// The token spent at grant is intentionally not refunded so an outage
		// cannot mint credit; the refusal is recorded under RetryEndBreaker.
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
	// Re-enter at enqueue so the retry waits only in its tenant queue (one
	// waiting place) without re-running classify, reject, or rate gates.
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
