package adv_test

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/test/e2e"
)

// These tests deliberately invert the builder's service-time ratio: short quiet
// queries must wait behind long flooder queries. No wall-clock assertion is used
// in simulator tests; tier R alone uses the claim's numerical thresholds.
func realRatio(t *testing.T, conns int, sql string) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: real Postgres campaign")
	}
	t.Logf("seed=%d (launch order fixed; tier R uses wall-clock thresholds)", seed)
	d := scratch(t)
	t.Setenv("TMPDIR", d)
	setup, e := e2e.StartRealEnv(fmt.Sprintf("mg-adv-c09-%d", os.Getpid()), d)
	if setup != nil {
		t.Cleanup(func() {
			if e := setup.Close(); e != nil {
				t.Error(e)
			}
		})
	}
	if e != nil {
		t.Fatalf("tier R environment unavailable: %v", e)
	}
	env := setup.Env
	type result struct {
		ok, shed           int
		delay              []time.Duration
		floodShed, floodOK int64
	}
	measure := func(flood bool) result {
		g, e := env.StartGateway(e2e.GatewayOpts{Scenario: fmt.Sprintf("adv-%v", flood), Conns: conns, QueueCap: 128, Tenants: 2, MaxClients: 128, Queue: map[string]any{"rule": "timeout", "tenant_capacity": 4}})
		if e != nil {
			t.Fatal(e)
		}
		defer g.Stop()
		q, e := env.Dial(g, 1, false)
		if e != nil {
			t.Fatal(e)
		}
		defer q.Close()
		// Warm the quiet path before taking the control samples.
		for range 5 {
			q.Query("SELECT 1")
		}
		var stop atomic.Bool
		var shed, ok atomic.Int64
		var wg sync.WaitGroup
		if flood {
			clients := make([]*e2e.RealClient, 48)
			for i := range clients {
				clients[i], e = env.Dial(g, 0, false)
				if e != nil {
					t.Fatal(e)
				}
			}
			for _, c := range clients {
				wg.Add(1)
				go func(c *e2e.RealClient) {
					defer wg.Done()
					defer c.Close()
					for !stop.Load() {
						r := c.Query(sql)
						if e2e.HasToken(r, "E:ERROR:53M01") {
							shed.Add(1)
						} else if e2e.HasToken(r, "C:") {
							ok.Add(1)
						}
					}
				}(c)
			}
			time.Sleep(100 * time.Millisecond)
		}
		var r result
		for range 30 {
			start := time.Now()
			x := q.Query("SELECT 1")
			if e2e.HasToken(x, "C:") {
				r.ok++
				r.delay = append(r.delay, time.Since(start))
			} else if e2e.HasToken(x, "E:ERROR:53M01") {
				r.shed++
			}
			time.Sleep(7 * time.Millisecond)
		}
		stop.Store(true)
		wg.Wait()
		r.floodShed, r.floodOK = shed.Load(), ok.Load()
		return r
	}
	alone, mixed := measure(false), measure(true)
	if alone.ok != 30 {
		t.Fatalf("control successes=%d/30", alone.ok)
	}
	if mixed.ok != 30 {
		t.Errorf("quiet success=%d/30, need >99%%", mixed.ok)
	}
	if mixed.floodOK == 0 || mixed.floodShed == 0 {
		t.Errorf("overload not exercised: flood ok=%d shed=%d", mixed.floodOK, mixed.floodShed)
	}
	if mixed.floodShed*100 <= 95*(mixed.floodShed+int64(mixed.shed)) {
		t.Errorf("flood shed=%d quiet shed=%d, flood share <=95%%", mixed.floodShed, mixed.shed)
	}
	if len(mixed.delay) > 0 {
		slices.Sort(alone.delay)
		slices.Sort(mixed.delay)
		a, b := alone.delay[len(alone.delay)-1], mixed.delay[len(mixed.delay)-1]
		if b > 2*a {
			t.Errorf("quiet p99=%v alone=%v exceeds 2x", b, a)
		}
	}
}
func TestAdv_6_ShortQuietLongFlood(t *testing.T) { realRatio(t, 2, "SELECT pg_sleep(0.03)") }
func TestAdv_6_OddPoolRounding(t *testing.T)     { realRatio(t, 3, "SELECT pg_sleep(0.03)") }
func TestAdv_6_VeryLongFloodLeases(t *testing.T) { realRatio(t, 2, "SELECT pg_sleep(0.08)") }
