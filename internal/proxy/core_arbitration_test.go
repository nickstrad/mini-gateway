package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func arbitrationIdlePool(t *testing.T, n, cap int, mode proxy.ResetPolicy, failures int) *coreRig {
	t.Helper()
	r := newCoreRig(t, func(c *proxy.Config) {
		c.Connections = cap
		c.QueueCap = 64
		c.Reset = mode
		c.Pool.BreakerFailures = failures
	})
	for i := 0; i < n; i++ {
		client := proxy.ConnID(100 + i)
		r.login(client, 1)
		r.q(client, "SELECT 1")
		r.tick(time.Millisecond)
		if len(r.dbs) != i+1 {
			t.Fatalf("dials %d want %d", len(r.dbs), i+1)
		}
		r.open(r.dbs[i])
	}
	for _, id := range r.dbs {
		r.db(id, complete("SELECT 1", 'I'))
		if mode != proxy.ResetOnSwitch {
			r.finishReset(id)
		}
	}
	return r
}

func TestCoreArbitrationOneRequestBoundsPreparation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		n, cap, bound int
		mode          proxy.ResetPolicy
		sql           string
	}{
		{"switch", 11, 12, 1, proxy.ResetOnSwitch, "DISCARD ALL"},
		{"stale", 42, 45, 4, proxy.ResetAlways, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := arbitrationIdlePool(t, tc.n, tc.cap, tc.mode, 5)
			if tc.name == "stale" {
				r.tick(pool.DefaultCheckAfterIdle)
			}
			r.login(11, 0)
			a := r.q(11, "SELECT 2")
			writes := 0
			var prepared proxy.ConnID
			for _, x := range a {
				if x.Kind == proxy.ActDBWrite && bytes.Equal(x.Data, query(tc.sql)) {
					writes++
					prepared = x.Conn
				}
			}
			if writes != tc.bound {
				t.Fatalf("preparation writes %d want %d", writes, tc.bound)
			}
			if tc.name == "switch" {
				a = r.db(prepared, complete("DISCARD ALL", 'I'))
			} else {
				a = r.db(prepared, emptyReply())
			}
			if !bytes.Equal(written(a, proxy.ActDBWrite, prepared), query("SELECT 2")) {
				t.Fatalf("prepared connection did not serve request: %v", a)
			}
		})
	}
}
