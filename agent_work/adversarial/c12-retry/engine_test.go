package adv_test

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/retry/retrytest"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func TestAdv_3_AcceptedOffsetsAcrossMessage(t *testing.T) {
	for _, n := range []int{1, 4, 5, 17, 29} {
		r := newRig(t, nil)
		r.query(1001, "UPDATE marks SET n=n+1 WHERE id=7")
		a := r.take()
		r.lose(a, n, true)
		if r.retryCount() != 0 {
			t.Fatalf("accepted %d bytes but retried", n)
		}
		r.end()
	}
}
func TestAdv_3_RefusalAfterPartialReply(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	b := command("UPDATE 1", 'I')
	r.step(proxy.Event{Kind: proxy.EvDBData, Conn: a.Conn, Data: b[:len(b)-6]})
	r.lose(a, 0, true)
	if r.retryCount() != 0 {
		t.Fatalf("relayed command then retried %d", r.retryCount())
	}
	r.end()
}
func TestAdv_3_SilenceAndCloseWithoutReport(t *testing.T) {
	for _, silence := range []bool{false, true} {
		r := newRig(t, nil)
		r.query(1001, "UPDATE marks SET n=n+1")
		a := r.take()
		if silence {
			r.advance(r.e.Config().Timeouts.Statement)
		} else {
			r.lose(a, 0, false)
		}
		if r.retryCount() != 0 {
			t.Fatalf("silence=%v retried", silence)
		}
		r.end()
	}
}

// Our driver keeps its own application ledger; accepting a whole Query applies it.
// It cuts the connection at each byte of the response, independently of the builder's relay.
func cutSweep(t *testing.T, kind retry.Kind) {
	for cut := 0; cut <= len(command("UPDATE 1", 'I')); cut++ {
		r := newRig(t, func(c *proxy.Config) { c.Retry.Kind = kind })
		r.query(1001, "UPDATE marks SET n=n+1")
		a := r.take()
		applied := 1
		r.step(proxy.Event{Kind: proxy.EvDBWrote, Conn: a.Conn, N: len(a.Data)})
		if cut > 0 {
			r.step(proxy.Event{Kind: proxy.EvDBData, Conn: a.Conn, Data: command("UPDATE 1", 'I')[:cut]})
		}
		r.step(proxy.Event{Kind: proxy.EvDBClosed, Conn: a.Conn})
		r.connect()
		for _, q := range r.queries {
			if string(q.Data) == string(a.Data) {
				applied++
			}
		}
		if applied != 1 || r.retryCount() != 0 {
			t.Fatalf("cut=%d policy=%s applied=%d retries=%d", cut, kind, applied, r.retryCount())
		}
		r.end()
	}
}
func TestAdv_4_OffEveryResponseByteCut(t *testing.T) { cutSweep(t, retry.KindOff) }
func TestAdv_4_OnEveryResponseByteCut(t *testing.T)  { cutSweep(t, retry.KindPreForwardOnce) }
func TestAdv_4_TransactionRefusalAfterPriorWrite(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "BEGIN")
	r.success(r.take(), 'T')
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	r.lose(a, 0, true)
	if r.retryCount() != 0 {
		t.Fatalf("retried transaction with lost history")
	}
	r.end()
}

type gate struct {
	open   bool
	allows int
}

func (g *gate) Allow(proxy.Instant) bool            { g.allows++; return !g.open }
func (g *gate) Observe(proxy.Instant, work.Outcome) {}
func (g *gate) Open() bool                          { return g.open }
func TestAdv_6_BreakerChangesWhileStatementActive(t *testing.T) {
	g := &gate{}
	r := newRig(t, func(c *proxy.Config) { c.Seams.Breaker = g })
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	g.open = true
	r.lose(a, 0, true)
	if r.retryCount() != 0 || g.allows != 1 {
		t.Fatalf("open breaker retries=%d Allow calls=%d", r.retryCount(), g.allows)
	}
	r.end()
}
func TestAdv_6_BreakerReopensBeforeQueuedRetryRuns(t *testing.T) {
	g := &gate{}
	r := newRig(t, func(c *proxy.Config) { c.Seams.Breaker = g })
	r.query(1001, "UPDATE marks SET n=n+1")
	r.lose(r.take(), 0, true)
	g.open = true
	r.connect()
	if len(r.queries) > 0 {
		t.Fatalf("granted while closed, but %d retry Query emitted after breaker opened", len(r.queries))
	}
	r.end()
}
func TestAdv_6_ClosedOpenClosedBudgetPreserved(t *testing.T) {
	begin(t)
	p := policy(t, 1, 1)
	f := eligible()
	f.BreakerOpen = true
	for i := 0; i < 100; i++ {
		if p.Decide(0, f).Retry {
			t.Fatal("retry through open breaker")
		}
	}
	f.BreakerOpen = false
	if !p.Decide(0, f).Retry {
		t.Fatal("open-breaker refusals consumed the token")
	}
	must(t, p.Snapshot().Check())
}

func TestAdv_7_WaitingRetryConsumesQueueSlot(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 7 narrowed: a waiting retry holds no database connection; it does hold one place in its tenant's admission queue. See agent_work/chunks/c12-retry/ADVERSARIAL.md.")
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	r.lose(r.take(), 0, true)
	s := r.e.Snapshot()
	defer r.end()
	if s.RetryPending.Value != 1 {
		t.Fatal("did not exercise waiting retry")
	}
	if s.Queued.Value != 0 || s.DBLent.Value != 0 {
		t.Fatalf("waiting retry queued=%d lent=%d; literal claim requires neither", s.Queued.Value, s.DBLent.Value)
	}
}
func TestAdv_7_DeadlineTiedWithNewConnection(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	r.advance(r.e.Config().Timeouts.Statement - time.Nanosecond)
	r.lose(a, 0, true)
	if r.retryCount() != 1 {
		t.Fatal("retry not granted just before deadline")
	}
	r.advance(time.Nanosecond)
	r.connect()
	if len(r.queries) != 0 || r.replies[1001] != 1 || r.e.Snapshot().RetryPending.Value != 0 {
		t.Fatalf("late forward=%d replies=%d pending=%d", len(r.queries), r.replies[1001], r.e.Snapshot().RetryPending.Value)
	}
	r.end()
}
func TestAdv_7_ClientLeavesBeforeReplacementReady(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	r.lose(r.take(), 0, true)
	r.step(proxy.Event{Kind: proxy.EvClientClosed, Conn: 1001})
	r.connect()
	if len(r.queries) != 0 || r.e.Snapshot().DBLent.Value != 0 {
		t.Fatalf("abandoned retry still lent/forwarded")
	}
	r.end()
}

// Burst phases use a seeded failure schedule, then an error-free recovery phase.
// No clocks, sleeps or existing scenario checks are used.
func burst(t *testing.T, kind retry.Kind, everything bool) (forwards, done int) {
	r := newRig(t, func(c *proxy.Config) {
		c.Retry.Kind = kind
		if everything {
			c.Seams.Retry = proxy.NewRetryCore(retrytest.NewEverything())
		}
	})
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 90; i++ {
		r.query(1001, "UPDATE marks SET n=n+1")
		a := r.take()
		forwards++
		if i < 60 && rng.Intn(10) < 3 {
			r.lose(a, 0, false)
			r.connect()
			if len(r.queries) > 0 {
				a = r.take()
				forwards++
				r.success(a, 'I')
			}
		} else {
			r.success(a, 'I')
			if i >= 60 {
				done++
			}
		}
		if r.replies[1001] != i+1 {
			t.Fatalf("burst request %d ready replies=%d", i, r.replies[1001])
		}
	}
	if done != 30 {
		t.Fatalf("recovery goodput %d/30", done)
	}
	r.end()
	return
}
func TestAdv_8_OffBurstAgainstAblation(t *testing.T) {
	a, _ := burst(t, retry.KindOff, false)
	b, _ := burst(t, retry.KindOff, true)
	if a >= b {
		t.Fatalf("off forwards=%d ablation=%d", a, b)
	}
}
func TestAdv_8_OnBurstAgainstAblation(t *testing.T) {
	a, _ := burst(t, retry.KindPreForwardOnce, false)
	b, _ := burst(t, retry.KindOff, true)
	if a >= b {
		t.Fatalf("on forwards=%d ablation=%d", a, b)
	}
}
func TestAdv_8_ZeroByteOutageBudgetAndRecovery(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < 140; i++ {
		started := r.now
		r.query(1001, "UPDATE marks SET n=n+1")
		a := r.take()
		if i < 100 {
			r.lose(a, 0, true)
			r.connect()
			if len(r.queries) > 0 {
				r.lose(r.take(), 0, true)
				r.connect()
			}
		} else {
			r.success(a, 'I')
		}
		if r.now-started > proxy.Instant(2*time.Millisecond) {
			t.Fatalf("request %d took %d ns of virtual time", i, r.now-started)
		}
		if r.replies[1001] != i+1 {
			t.Fatalf("reply count at %d: %d", i, r.replies[1001])
		}
	}
	s, _ := r.e.RetrySnapshot()
	if s.Retries == 0 || s.Refused[retry.RefusedBudget] == 0 || 10*s.Retries > s.Attempts+s.Floor || r.e.Snapshot().Completed != 40 {
		t.Fatalf("storm=%+v completed=%d", s, r.e.Snapshot().Completed)
	}
	r.end()
}

func TestAdv_11_AblationDuplicatesAnAcknowledgedWrite(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.Seams.Retry = proxy.NewRetryCore(retrytest.NewEverything()) })
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	applied := 1
	r.lose(a, len(a.Data), false)
	r.connect()
	b := r.take()
	if string(a.Data) != string(b.Data) {
		t.Fatal("retry changed SQL")
	}
	applied++
	r.success(b, 'I')
	if applied != 2 || r.replies[1001] != 1 {
		t.Fatalf("applied=%d replies=%d", applied, r.replies[1001])
	}
	r.end()
}

func TestAdv_13_ShutdownAtEachRetryPhase(t *testing.T) {
	for phase := 0; phase < 4; phase++ {
		r := newRig(t, nil)
		r.query(1001, "UPDATE marks SET n=n+1")
		r.lose(r.take(), 0, true)
		if phase > 0 {
			r.connect()
			a := r.take()
			if phase == 2 {
				r.step(proxy.Event{Kind: proxy.EvDBWrote, Conn: a.Conn, N: 1})
			}
			if phase == 3 {
				r.success(a, 'I')
			}
		}
		r.end()
		r.end()
	}
}

type corruptPolicy struct {
	retry.Policy
	mode int
}

func (p *corruptPolicy) Snapshot() retry.Snapshot {
	s := p.Policy.Snapshot()
	switch p.mode {
	case 1:
		s.Offered++
		s.Refused[retry.RefusedOff]++
	case 2:
		s.Deposits++
		s.Level.Value++
		s.Cap++
	case 3:
		s.Withdrawals++
	}
	return s
}
func TestAdv_13_EngineRejectsOfferMismatch(t *testing.T) {
	p := &corruptPolicy{Policy: policy(t, 10, 1)}
	r := newRig(t, func(c *proxy.Config) { c.Seams.Retry = proxy.NewRetryCore(p) })
	p.mode = 1
	if e := r.e.Invariants(); e == nil {
		t.Fatal("engine accepted policy offers not seen by engine")
	}
	p.mode = 0
	r.end()
}
func TestAdv_13_EngineRejectsBrokenWithdrawal(t *testing.T) {
	p := &corruptPolicy{Policy: policy(t, 10, 1)}
	r := newRig(t, func(c *proxy.Config) { c.Seams.Retry = proxy.NewRetryCore(p) })
	p.mode = 3
	if e := r.e.Invariants(); e == nil {
		t.Fatal("engine accepted counterfeit withdrawal")
	}
	p.mode = 0
	r.end()
}

func TestAdv_4_LongSeededCuts(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1")
	}
	begin(t)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 2000; i++ {
		r := newRigSeed(t, nil, seed+int64(i))
		r.query(1001, "UPDATE marks SET n=n+1")
		a := r.take()
		n := 1 + rng.Intn(len(a.Data))
		r.lose(a, n, true)
		if r.retryCount() != 0 {
			t.Fatalf("iteration=%d seed=%d accepted=%d retries=%d", i, seed+int64(i), n, r.retryCount())
		}
		r.end()
	}
}

// Make the replacement-open event itself arrive at deadline equality.
func TestAdv_7_ReplacementEventExactlyAtDeadline(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	r.lose(r.take(), 0, true)
	r.advance(time.Millisecond)
	if len(r.opens) != 1 {
		t.Fatalf("replacement dials=%d", len(r.opens))
	}
	r.now = proxy.Instant(r.e.Config().Timeouts.Statement)
	r.connect()
	if len(r.queries) != 0 || r.replies[1001] != 1 {
		t.Fatalf("deadline equality forwarded=%d replies=%d", len(r.queries), r.replies[1001])
	}
	r.end()
}
