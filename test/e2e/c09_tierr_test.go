package e2e

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	tierRFairFloodClients = 40
	tierRFairFloodSQL     = "SELECT pg_sleep(0.005)"
	tierRFairQuietSQL     = "SELECT pg_sleep(0.02)"
	tierRFairQuietEvery   = 50 * time.Millisecond
	tierRFairQuietSteps   = 100
	tierRFairWarmUp       = 300 * time.Millisecond
	tierRFairScenario     = "tierr-fair-noisy"
)

type tierRFairResult struct {
	quietOK, quietShed, quietOther int
	floodOK, floodShed             int64
	latency                        []time.Duration
	final                          obs.InstanceSnapshot
}

func (r tierRFairResult) p99() time.Duration {
	s := slices.Clone(r.latency)
	slices.Sort(s)
	return s[(len(s)*99+99)/100-1]
}

func tierRFairRun(t *testing.T, env *RealEnv, scenario string, flood bool) tierRFairResult {
	t.Helper()
	g := gateway(t, env, GatewayOpts{Scenario: scenario, Conns: 2, QueueCap: 128, Tenants: 2, MaxClients: 256,
		Queue: map[string]any{"rule": "timeout", "tenant_capacity": 16}})
	var out tierRFairResult
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var floodOK, floodShed atomic.Int64
	if flood {
		for range tierRFairFloodClients {
			c, err := env.Dial(g, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				for ctx.Err() == nil {
					r := c.Query(tierRFairFloodSQL)
					switch {
					case HasToken(r, "C:SELECT 1"):
						floodOK.Add(1)
					case slices.Equal(r, errTokens("53M01")):
						floodShed.Add(1)
						time.Sleep(time.Millisecond)
					default:
						return
					}
				}
			}()
		}
		time.Sleep(tierRFairWarmUp)
	}
	q, err := env.Dial(g, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	next := time.Now()
	for range tierRFairQuietSteps {
		time.Sleep(time.Until(next))
		next = next.Add(tierRFairQuietEvery)
		start := time.Now()
		r := q.Query(tierRFairQuietSQL)
		switch {
		case HasToken(r, "C:SELECT 1"):
			out.quietOK++
			out.latency = append(out.latency, time.Since(start))
		case slices.Equal(r, errTokens("53M01")):
			out.quietShed++
		default:
			out.quietOther++
		}
	}
	q.Close()
	stop()
	wg.Wait()
	out.floodOK, out.floodShed = floodOK.Load(), floodShed.Load()
	out.final = stopAndRead(t, env, g, scenario)
	return out
}

func TestTierRFairNoisyNeighbour(t *testing.T) {
	env := tierR(t)
	alone := tierRFairRun(t, env, tierRFairScenario+"-alone", false)
	if alone.quietOK != tierRFairQuietSteps {
		t.Fatalf("the quiet tenant alone: %d ok of %d (%d shed, %d other)", alone.quietOK, tierRFairQuietSteps, alone.quietShed, alone.quietOther)
	}
	r := tierRFairRun(t, env, tierRFairScenario, true)
	shed := r.floodShed + int64(r.quietShed)
	t.Logf("quiet tenant: %d ok of %d, p99 %v (alone %v); flooder: %d ok, %d shed", r.quietOK, tierRFairQuietSteps, r.p99(), alone.p99(), r.floodOK, r.floodShed)
	if r.quietOK == 0 || 100*r.quietOK <= 99*tierRFairQuietSteps {
		t.Fatalf("the quiet tenant succeeded %d of %d times: want more than 99%%", r.quietOK, tierRFairQuietSteps)
	}
	if r.p99() > 2*alone.p99() {
		t.Fatalf("the quiet tenant's p99 %v is more than twice its p99 alone %v", r.p99(), alone.p99())
	}
	if shed == 0 || 100*r.floodShed <= 95*shed || r.floodOK == 0 {
		t.Fatalf("the flooder took %d of %d sheds (want more than 95%%) and was served %d times", r.floodShed, shed, r.floodOK)
	}
	if ev(r.final, "replies", obs.Labels{Reason: "shed"}) < shed {
		t.Fatalf("shed row %d, clients saw %d", ev(r.final, "replies", obs.Labels{Reason: "shed"}), shed)
	}
}
