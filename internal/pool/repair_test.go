package pool

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func count(acts []Action, k ActKind) int {
	n := 0
	for _, a := range acts {
		if a.Kind == k {
			n++
		}
	}
	return n
}

func dialsIn(acts []Action) []ConnID {
	var ids []ConnID
	for _, a := range acts {
		if a.Kind == ActDial {
			ids = append(ids, a.Conn)
		}
	}
	return ids
}

// TestEveryOperationExpiresWhatIsDueBeforeApplyingIt proves time passes before
// every event at exact equality for lifetime, idle, check, reset, and cleanup deadlines.
func TestEveryOperationExpiresWhatIsDueBeforeApplyingIt(t *testing.T) {
	base := Config{Cap: 1, RatePerSec: 100, NoJitter: true, CheckAfterIdle: time.Hour, IdleTimeout: 2 * time.Hour, MaxLifetime: 3 * time.Hour}
	t.Run("checkout at the lifetime", func(t *testing.T) {
		cfg := base
		cfg.MaxLifetime = time.Minute
		p, ids, _ := open(t, cfg, 1)
		if _, ok, acts := p.Checkout(Instant(time.Minute), tenantA, nil); ok || p.State(ids[0]) != 0 || p.Snapshot().Closes[CloseLifetime] != 1 {
			t.Fatalf("lent %v at its lifetime: state %s acts %v", ok, p.State(ids[0]), acts)
		}
	})
	t.Run("checkout at the idle timeout", func(t *testing.T) {
		cfg := base
		cfg.IdleTimeout = 10 * time.Second
		p, ids, _ := open(t, cfg, 1)
		if _, ok, _ := p.Checkout(Instant(10*time.Second), tenantA, nil); ok || p.Snapshot().Closes[CloseIdle] != 1 {
			t.Fatalf("lent %v at its idle timeout: state %s", ok, p.State(ids[0]))
		}
	})
	t.Run("check reply at its deadline", func(t *testing.T) {
		cfg := base
		cfg.CheckAfterIdle = time.Second
		p, ids, _ := open(t, cfg, 1)
		at := Instant(2 * time.Second)
		if _, ok, acts := p.Checkout(at, tenantA, nil); ok || count(acts, ActCheck) != 1 {
			t.Fatalf("no check: %v", acts)
		}
		p.StepDone(at+Instant(DefaultCheckTimeout), ids[0], 'I', nil)
		if s := p.Snapshot(); p.State(ids[0]) != 0 || s.ChecksByResult[CheckTimeout] != 1 || s.ChecksByResult[CheckOK] != 0 {
			t.Fatalf("check passed at its deadline: state %s results %v", p.State(ids[0]), s.ChecksByResult)
		}
	})
	t.Run("reset reply at its deadline", func(t *testing.T) {
		p, ids, now := open(t, base, 1)
		p.Checkout(now, tenantA, nil)
		if acts := p.Release(now, ids[0], End{Kind: EndClean, Tenant: tenantA}, nil); count(acts, ActReset) != 1 {
			t.Fatalf("no reset: %v", acts)
		}
		p.StepDone(now+Instant(DefaultResetTimeout), ids[0], 'I', nil)
		if s := p.Snapshot(); p.State(ids[0]) != 0 || s.Closes[CloseResetFailed] != 1 || s.ResetsOK[ResetAtRelease] != 0 {
			t.Fatalf("reset completed at its deadline: state %s", p.State(ids[0]))
		}
	})
	t.Run("drain reply at the cleanup deadline", func(t *testing.T) {
		p, ids, now := open(t, base, 1)
		p.Checkout(now, tenantA, nil)
		p.Release(now, ids[0], End{Kind: EndVanishedMidStatement, Mid: true}, nil)
		acts := p.StepDone(now+Instant(DefaultCleanup), ids[0], 'I', nil)
		if p.State(ids[0]) != 0 || count(acts, ActReset) != 0 || p.Snapshot().Closes[CloseCleanupFailed] != 1 {
			t.Fatalf("drain revived at the cleanup deadline: state %s acts %v", p.State(ids[0]), acts)
		}
	})
}

// TestCreationRateBoundHoldsForRatesThatDoNotDivideASecond proves ceiling GCRA
// spacing holds floor(W*rate/1s)+1 across non-divisor rates.
func TestCreationRateBoundHoldsForRatesThatDoNotDivideASecond(t *testing.T) {
	for _, rate := range []int{3, 7, 9, 11, 13, 999, 7919} {
		p, err := New(Config{Cap: MaxCap, MaxConnecting: MaxConnectingLimit, RatePerSec: rate, NoJitter: true, CheckAfterIdle: time.Hour, IdleTimeout: 24 * time.Hour, MaxLifetime: 24 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		var at []Instant
		now := Instant(0)
		for len(at) < 3*min(rate, 100)+2 {
			for _, id := range dialsIn(p.Demand(now, MaxCap, nil)) {
				at = append(at, now)
				p.Opened(now, id, nil)
			}
			next := p.Next()
			if next <= now {
				t.Fatalf("rate %d: no timer after %d dials", rate, len(at))
			}
			now = next
		}
		for i := range at {
			for j := i + 1; j < len(at); j++ {
				if int64(j-i)*int64(time.Second) > int64(at[j]-at[i])*int64(rate) {
					t.Fatalf("rate %d: %d dials within %d ns, bound floor(W*rate/1s)+1 = %d", rate, j-i+1, at[j]-at[i], int64(at[j]-at[i])*int64(rate)/int64(time.Second)+1)
				}
			}
		}
	}
}

// TestJitterSpreadsSmallDurationsAndRefusesOnesTooSmallToSpread proves sub-ms
// jitter spreads fail closed naming the key and nanosecond jitter separates small durations.
func TestJitterSpreadsSmallDurationsAndRefusesOnesTooSmallToSpread(t *testing.T) {
	for _, c := range []struct {
		cfg Config
		key string
	}{{Config{MaxLifetime: 999 * time.Nanosecond, JitterPermille: 1}, "pool.max_lifetime"},
		{Config{IdleTimeout: 5 * time.Millisecond}, "pool.idle_timeout"},
		{Config{MaxLifetime: 9 * time.Millisecond, IdleTimeout: time.Second}, "pool.max_lifetime"}} {
		if _, err := New(c.cfg); err == nil || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%+v: error %v, want one naming %s", c.cfg, err, c.key)
		}
	}
	if _, err := New(Config{MaxLifetime: 999 * time.Nanosecond, IdleTimeout: time.Nanosecond, NoJitter: true}); err != nil {
		t.Errorf("no jitter: %v", err)
	}
	p, err := New(Config{Cap: 64, MaxConnecting: 64, RatePerSec: MaxRatePerSec, JitterPermille: 1, MaxLifetime: time.Second, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	fill(p, 64)
	var lives []Instant
	for _, c := range p.list {
		lives = append(lives, c.lifeLimit)
	}
	slices.Sort(lives)
	if n := len(slices.Compact(lives)); n < 60 {
		t.Fatalf("1 permille of 1s: only %d distinct lifetimes among 64 connections created together", n)
	}
}

// TestAnExpiredCancelNeverLetsItsConnectionBeLentAgain proves a connection whose
// cancel timed out is tainted and closed on its next return instead of reused.
func TestAnExpiredCancelNeverLetsItsConnectionBeLentAgain(t *testing.T) {
	for _, mode := range []ResetMode{ResetAlways, ResetOnSwitch} {
		p, ids, now := open(t, Config{Cap: 1, RatePerSec: 100, Reset: mode, CheckAfterIdle: time.Hour}, 1)
		p.Checkout(now, tenantA, nil)
		ok, acts := p.ClientCancel(now, ids[0], nil)
		if !ok || count(acts, ActCancel) != 1 {
			t.Fatalf("%s: cancel not sent: %v", mode, acts)
		}
		now += 2 * Instant(DefaultCleanup)
		p.Tick(now, nil)
		if s := p.Snapshot(); s.CancelsExpired != 1 || p.State(ids[0]) != StateLent {
			t.Fatalf("%s: cancel expired %d, state %s", mode, s.CancelsExpired, p.State(ids[0]))
		}
		p.Release(now, ids[0], End{Kind: EndClean, Tenant: tenantA}, nil)
		p.StepDone(now, ids[0], 'I', nil)
		if id, ok, _ := p.Checkout(now, tenantA, nil); ok {
			t.Fatalf("%s: connection %d lent again although its cancel never reported done", mode, id)
		}
		if s := p.Snapshot(); p.State(ids[0]) != 0 || s.Closes[CloseCleanupFailed] != 1 {
			t.Fatalf("%s: state %s closes %v", mode, p.State(ids[0]), s.Closes)
		}
		if err := p.Invariants(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAFailedCheckOfAnIdleConnectionDoesNotCountTowardTheBreaker proves non-trial
// check failures close the dead connection without opening the breaker so dials decide.
func TestAFailedCheckOfAnIdleConnectionDoesNotCountTowardTheBreaker(t *testing.T) {
	for _, fail := range []string{"error", "unexpected", "timeout", "lost"} {
		p, ids, now := open(t, Config{Cap: 3, RatePerSec: 100, BreakerFailures: 1, CheckAfterIdle: time.Second}, 3)
		now += Instant(time.Second)
		if _, ok, acts := p.Checkout(now, tenantA, nil); ok || count(acts, ActCheck) != 3 {
			t.Fatalf("%s: checks %v", fail, acts)
		}
		var after []Action
		for _, id := range ids {
			switch fail {
			case "error":
				after = p.StepFailed(now, id, FailError, after)
			case "unexpected":
				after = p.StepFailed(now, id, FailUnexpected, after)
			case "lost":
				after = p.Lost(now, id, LossFatal, after)
			}
		}
		if fail == "timeout" {
			now += Instant(DefaultCheckTimeout)
			after = p.Tick(now, after)
		}
		s := p.Snapshot()
		if p.Breaker() != BreakerClosed || !p.Allow(now) || s.Closes[CloseCheckFailed] != 3 {
			t.Fatalf("%s: three failed checks left the breaker %s (closes %v)", fail, p.Breaker(), s.Closes)
		}
		if after = p.Demand(now, 1, after); len(dialsIn(after)) == 0 {
			t.Fatalf("%s: no replacement dial: %v", fail, after)
		}
	}
}

// TestATrialThatFailsOrALossWhileOpenRefusesQueuedWork proves re-opening on a
// failed trial or losing a connection while open flushes queued work.
func TestATrialThatFailsOrALossWhileOpenRefusesQueuedWork(t *testing.T) {
	p, err := New(Config{Cap: 2, RatePerSec: 1000, BreakerFailures: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.DialFailed(0, dialsIn(p.Demand(0, 1, nil))[0], nil)
	now := Instant(DefaultBreakerOpenFor)
	p.Allow(now)
	trial := dialsIn(p.Tick(now, nil))
	if len(trial) != 1 || p.Breaker() != BreakerHalfOpen {
		t.Fatalf("trial %v breaker %s", trial, p.Breaker())
	}
	if acts := p.DialFailed(now, trial[0], nil); p.Breaker() != BreakerOpen || count(acts, ActRefuseQueued) != 1 {
		t.Fatalf("failed trial: breaker %s acts %v, want open and queued work refused", p.Breaker(), acts)
	}

	q, ids, now := open(t, Config{Cap: 3, RatePerSec: 100, BreakerFailures: 1}, 2)
	if acts := q.Lost(now, ids[0], LossBytes, nil); q.Breaker() != BreakerOpen || count(acts, ActRefuseQueued) != 1 {
		t.Fatalf("first loss: breaker %s acts %v, want queued work refused despite the idle connection left", q.Breaker(), acts)
	}
	if acts := q.Lost(now, ids[1], LossClosed, nil); count(acts, ActRefuseQueued) != 1 {
		t.Fatalf("last usable connection lost while open: acts %v, want queued work refused", acts)
	}
}

// TestShutdownLeavesNoTimerWhileTheBreakerIsOpenOrTheRateLimits proves Next()
// returns 0 after Shutdown even when breaker or rate-limit timers were armed.
func TestShutdownLeavesNoTimerWhileTheBreakerIsOpenOrTheRateLimits(t *testing.T) {
	p, err := New(Config{Cap: 2, RatePerSec: 1000, BreakerFailures: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.DialFailed(0, dialsIn(p.Demand(0, 1, nil))[0], nil)
	if p.Breaker() != BreakerOpen || p.Next() == 0 {
		t.Fatalf("breaker %s next %d", p.Breaker(), p.Next())
	}
	p.Shutdown(1, nil)
	if p.Next() != 0 {
		t.Fatalf("open breaker: next %d after shutdown", p.Next())
	}
	q, err := New(Config{Cap: 4, RatePerSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	q.Demand(0, 3, nil)
	if q.Next() == 0 {
		t.Fatal("rate limit not armed")
	}
	q.Shutdown(1, nil)
	if q.Next() != 0 {
		t.Fatalf("rate limited: next %d after shutdown", q.Next())
	}
}
