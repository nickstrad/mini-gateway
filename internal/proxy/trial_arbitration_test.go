package proxy_test

import (
	"bytes"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"testing"
)

func TestCoreArbitrationTrialFailureRefusesWithIdleSibling(t *testing.T) {
	for _, n := range []int{1, 2} {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			r := arbitrationIdlePool(t, n, 2, proxy.ResetAlways, 1)
			r.db(r.dbs[n-1], []byte("X"))
			if r.pool().Breaker != int64(pool.BreakerOpen) {
				t.Fatal("breaker did not open")
			}
			r.tick(pool.DefaultBreakerOpenFor)
			r.login(11, 0)
			r.q(11, "SELECT 2")
			if r.e.Snapshot().Queued.Value != 1 {
				t.Fatal("not queued behind trial")
			}
			trial := r.dbs[len(r.dbs)-1]
			a := r.run(r.ev(proxy.EvDBDialFailed, trial, nil, 0))
			if !bytes.Contains(written(a, proxy.ActClientWrite, 11), []byte("53M03")) || r.e.Snapshot().Queued.Value != 0 {
				t.Fatalf("trial failed with %d idle siblings: queued=%d reply=%q", n-1, r.e.Snapshot().Queued.Value, written(a, proxy.ActClientWrite, 11))
			}
		})
	}
}
