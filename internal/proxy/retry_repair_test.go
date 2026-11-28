package proxy_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type switchBreaker struct{ open bool }

func (b *switchBreaker) Allow(proxy.Instant) bool            { return !b.open }
func (b *switchBreaker) Observe(proxy.Instant, work.Outcome) {}
func (b *switchBreaker) Open() bool                          { return b.open }

func queuedRetry(t *testing.T, edit func(*proxy.Config)) (*coreRig, proxy.ConnID) {
	t.Helper()
	r := newRetryRig(t, edit)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	if s := r.e.Snapshot(); s.RetryPending.Value != 1 || s.Queued.Value != 1 {
		t.Fatalf("no queued retry: pending %d queued %d", s.RetryPending.Value, s.Queued.Value)
	}
	return r, id
}

// TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded
// proves dispatch re-checks Breaker.Open before lending a queued retry, answers
// 53M03 under RetryEndBreaker, and keeps the withdrawn token spent.
func TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded(t *testing.T) {
	b := &switchBreaker{}
	r, _ := queuedRetry(t, func(c *proxy.Config) { c.Seams.Breaker = b })
	granted := r.retrySnap()
	b.open = true
	next := r.lastDial()
	a := r.open(next)
	if bytes.Contains(written(a, proxy.ActDBWrite, next), query(insertSQL)) {
		t.Fatal("the retry was forwarded after the breaker opened")
	}
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("53M03")) {
		t.Fatalf("client got %q, want the breaker's error", got)
	}
	s := r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndBreaker] != 1 || s.RetryPending.Value != 0 || s.Queued.Value != 0 {
		t.Fatalf("ends %v pending %d queued %d", s.RetryEnds, s.RetryPending.Value, s.Queued.Value)
	}
	p := r.retrySnap()
	if p.Retries != 1 || p.Withdrawals != retry.WithdrawTenths || p.Level.Value != granted.Level.Value || p.Offered != granted.Offered {
		t.Fatalf("budget after the refusal %+v, at the grant %+v", p, granted)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}

// TestRetryStillForwardedWhenTheBreakerStaysClosed proves the dispatch breaker
// re-check does not block a queued retry while the breaker remains closed.
func TestRetryStillForwardedWhenTheBreakerStaysClosed(t *testing.T) {
	b := &switchBreaker{}
	r, _ := queuedRetry(t, func(c *proxy.Config) { c.Seams.Breaker = b })
	next := r.lastDial()
	if a := r.open(next); !bytes.HasSuffix(written(a, proxy.ActDBWrite, next), query(insertSQL)) {
		t.Fatal("retry not forwarded")
	}
	if s := r.e.Snapshot(); s.RetryEnds[proxy.RetryEndBreaker] != 0 {
		t.Fatalf("%v", s.RetryEnds)
	}
}

// TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward proves
// the shipped pool core flushes a queued retry with 53M03 (RetryEndShed) the
// instant its breaker opens, before dispatch runs.
func TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward(t *testing.T) {
	r, _ := queuedRetry(t, func(c *proxy.Config) { c.Pool.BreakerFailures = 2 })
	if r.pool().Breaker != int64(pool.BreakerClosed) {
		t.Fatal("the breaker opened at the first loss")
	}
	dial := r.lastDial()
	a := r.run(r.ev(proxy.EvDBDialFailed, dial, nil, 0))
	if r.pool().Breaker != int64(pool.BreakerOpen) {
		t.Fatalf("the breaker did not open: %d", r.pool().Breaker)
	}
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("53M03")) {
		t.Fatalf("client got %q", got)
	}
	s := r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndShed] != 1 || s.RetryEnds[proxy.RetryEndBreaker] != 0 || s.RetryPending.Value != 0 || s.Queued.Value != 0 {
		t.Fatalf("ends %v pending %d queued %d", s.RetryEnds, s.RetryPending.Value, s.Queued.Value)
	}
	for _, x := range a {
		if x.Kind == proxy.ActDBWrite && bytes.Contains(x.Data, query(insertSQL)) {
			t.Fatal("the retry was forwarded")
		}
	}
}

// TestRetryQueuedAndCancelledByTheClientEndsFailed proves cancelling a queued
// retry answers 57014 and records RetryEndFailed with RetryPending back to 0.
func TestRetryQueuedAndCancelledByTheClientEndsFailed(t *testing.T) {
	r, _ := queuedRetry(t, nil)
	a := r.cancelFrom(90, r.clientKey(11))
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Contains(got, []byte("57014")) {
		t.Fatalf("client got %q", got)
	}
	s := r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndFailed] != 1 || s.RetryPending.Value != 0 {
		t.Fatalf("ends %v pending %d", s.RetryEnds, s.RetryPending.Value)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}

// TestRetryQueuedWhenTheBreakerFlushesEndsShed proves pool-core breaker flush
// records RetryEndShed and BreakerFlushed while conserving RetryPending.
func TestRetryQueuedWhenTheBreakerFlushesEndsShed(t *testing.T) {
	r, _ := queuedRetry(t, func(c *proxy.Config) { c.Pool.BreakerFailures = 2 })
	r.run(r.ev(proxy.EvDBDialFailed, r.lastDial(), nil, 0))
	s := r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndShed] != 1 || s.BreakerFlushed != 1 || s.RetryPending.Value != 0 {
		t.Fatalf("ends %v flushed %d pending %d", s.RetryEnds, s.BreakerFlushed, s.RetryPending.Value)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}

// TestRetryInFlightWhenTheClientCloses proves a client disconnect while a
// retry is forwarded records RetryEndGone and clears RetryPending.
func TestRetryInFlightWhenTheClientCloses(t *testing.T) {
	r, _ := queuedRetry(t, nil)
	next := r.lastDial()
	if a := r.open(next); !bytes.HasSuffix(written(a, proxy.ActDBWrite, next), query(insertSQL)) {
		t.Fatal("retry not forwarded")
	}
	r.run(r.ev(proxy.EvClientClosed, 11, nil, 0))
	s := r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndGone] != 1 || s.RetryPending.Value != 0 {
		t.Fatalf("ends %v pending %d", s.RetryEnds, s.RetryPending.Value)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}

// TestTheCapacityTableStatesTheCapAsTheLargestRetryBurst proves the generated
// capacity-table retry row states that stored credit does not expire with time
// and that the cap is the largest retry burst.
func TestTheCapacityTableStatesTheCapAsTheLargestRetryBurst(t *testing.T) {
	for _, row := range proxy.CapacityRows(proxy.Config{}) {
		if row.Key != "retry.bucket_cap, retry.floor" {
			continue
		}
		if !strings.Contains(row.Capacity, "the largest burst of retries") || !strings.Contains(row.Capacity, "does not expire with time") {
			t.Fatalf("retry row capacity %q", row.Capacity)
		}
		return
	}
	t.Fatal("no retry row in the capacity table")
}
