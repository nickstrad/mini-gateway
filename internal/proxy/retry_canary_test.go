package proxy_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
)

func plantedRig(t *testing.T, plant retry.Plant, edit func(*proxy.Config)) *coreRig {
	return newCoreRig(t, func(c *proxy.Config) {
		c.Retry = retry.Config{Kind: retry.KindPreForwardOnce, Plant: plant}
		if edit != nil {
			edit(c)
		}
	})
}

func retriedAfterAcceptedBytes(t *testing.T, plant retry.Plant) bool {
	r := plantedRig(t, plant, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.ev(proxy.EvDBWrote, id, nil, 1)
	r.refuseAndClose(id)
	return r.e.Snapshot().RetryAsked > 0
}

func retriedTwice(t *testing.T, plant retry.Plant) bool {
	r := plantedRig(t, plant, nil)
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	next := r.lastDial()
	r.run(r.ev(proxy.EvDBOpened, next, nil, 0))
	r.ev(proxy.EvDBData, next, []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0, 'Z', 0, 0, 0, 5, 'I'}, 0)
	r.refuseAndClose(next)
	return r.e.Snapshot().RetryAsked > 1
}

func retriedPastAnEmptyBudget(t *testing.T, plant retry.Plant) bool {
	r := plantedRig(t, plant, func(c *proxy.Config) { c.Retry.CapTokens, c.Retry.FloorTokens = 1, 1 })
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	next := r.lastDial()
	r.open(next)
	r.db(next, complete("INSERT 0 1", 'I'))
	r.finishReset(next)
	r.send(11, insertSQL)
	r.refuseAndClose(next)
	s := r.retrySnap()
	if plant == 0 && s.Refused[retry.RefusedBudget] != 1 {
		t.Fatalf("exhaustion not counted: %+v", s)
	}
	return s.Retries > 1
}

func retriedWithTheBreakerOpen(t *testing.T, plant retry.Plant) bool {
	r := plantedRig(t, plant, func(c *proxy.Config) { c.Pool.BreakerFailures = 1 })
	id := r.idleConn(11)
	r.send(11, insertSQL)
	r.refuseAndClose(id)
	return r.e.Snapshot().RetryAsked > 0
}

func TestCanaryEveryRetryMechanismHasAnEngineTestThatFailsWhenRemoved(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant retry.Plant
		run   func(*testing.T, retry.Plant) bool
	}{
		{"reached-no condition", retry.PlantIgnoreReached, retriedAfterAcceptedBytes},
		{"once-per-request bound", retry.PlantNoOnceBound, retriedTwice},
		{"breaker check", retry.PlantIgnoreBreaker, retriedWithTheBreakerOpen},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.run(t, 0) {
				t.Fatal("the product policy shows the planted behaviour")
			}
			if !c.run(t, c.plant) {
				t.Fatal("the planted bug went unnoticed")
			}
		})
	}
}

func TestRetryBudgetExhaustionIsVisibleAndCounted(t *testing.T) {
	if retriedPastAnEmptyBudget(t, 0) {
		t.Fatal("a retry ran past an empty budget")
	}
}

func TestCanaryBudgetWithdrawalRemovedBreaksTheEngineInvariants(t *testing.T) {
	p, _ := retry.New(retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: 1, FloorTokens: 1, Plant: retry.PlantNoWithdraw})
	rc := proxy.NewRetryCore(p)
	if !rc.Retry(0, proxy.Failure{Deadline: 1}) || !rc.Retry(0, proxy.Failure{Deadline: 1}) {
		t.Fatal("the planted policy refused")
	}
	if err := rc.Snapshot().Check(); err == nil {
		t.Fatal("retries with no withdrawal passed the snapshot check the engine invariants run")
	}
}

func TestCanaryBucketCapRemovedBreaksTheEngineInvariants(t *testing.T) {
	p, err := retry.New(retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: 1, FloorTokens: 1, Plant: retry.PlantNoCap})
	if err != nil {
		t.Fatal(err)
	}
	rc := proxy.NewRetryCore(p)
	for range 20 {
		rc.Attempt(0)
	}
	if err := rc.Snapshot().Check(); err == nil {
		t.Fatal("a level above the cap passed the snapshot check the engine invariants run")
	}
}
