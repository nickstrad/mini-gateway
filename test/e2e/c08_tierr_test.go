package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	tierRAdmClients   = 40
	tierRAdmSteps     = 150
	tierRAdmSleep     = 20 * time.Millisecond
	tierRAdmConns     = 2
	tierRAdmPace      = 10 * time.Millisecond
	tierRAdmBaseSteps = 30
	tierRAdmFloor     = 0.7
)

func admHistMax(s obs.InstanceSnapshot, metric string) (int64, int64) {
	var hi, n int64
	for _, h := range s.Hists {
		if h.Component != obs.ComponentAdmission || h.Metric != metric || h.Labels.Tenant != "" {
			continue
		}
		for _, b := range h.Buckets {
			if b.Count > 0 {
				hi = max(hi, b.Lo)
				n += b.Count
			}
		}
	}
	return hi, n
}

type tierRAdmResult struct {
	ok, shed int
	elapsed  time.Duration
	final    obs.InstanceSnapshot
}

func tierRAdmRun(t *testing.T, env *RealEnv, scenario string, steps int, queue map[string]any) tierRAdmResult {
	t.Helper()
	g := gateway(t, env, GatewayOpts{Scenario: scenario, Conns: tierRAdmConns, QueueCap: 64, Tenants: 1, Queue: queue})
	spec := Spec{}
	for range tierRAdmClients {
		var st []Step
		for range steps {
			st = append(st, Step{SQL: "SELECT pg_sleep(0.02)", Delay: tierRAdmPace})
		}
		st = append(st, Step{SQL: "SELECT 9"})
		spec.Clients = append(spec.Clients, Script{Tenant: 0, Steps: st})
	}
	start := time.Now()
	res := RunReal(context.Background(), spec, func(int) (*RealClient, error) { return env.Dial(g, 0, false) }, false)
	out := tierRAdmResult{elapsed: time.Since(start)}
	for _, r := range res {
		if len(r.Steps) != steps+1 {
			t.Fatalf("a client stopped early: %v", r)
		}
		for _, s := range r.Steps[:steps] {
			switch {
			case slices.Equal(s, errTokens("53M01")):
				out.shed++
			case HasToken(s, "C:SELECT 1"):
				out.ok++
			default:
				t.Fatalf("unexpected reply %v", s)
			}
		}
		if last := r.Steps[steps]; !slices.Equal(last, selectN("9")) && !slices.Equal(last, errTokens("53M01")) {
			t.Fatalf("a shed client's connection is not usable: %v", last)
		}
	}
	out.final = stopAndRead(t, env, g, scenario)
	return out
}

func TestTierRAdmissionOverloadWithPgSleep(t *testing.T) {
	env := tierR(t)
	base := tierRAdmRun(t, env, "tierr-admission-overload", tierRAdmBaseSteps, map[string]any{"rule": "timeout", "tenant_capacity": 64, "interval": "60s", "target": "30s"})
	baseRate := float64(base.ok) / base.elapsed.Seconds()
	for _, rule := range []string{"timeout", "control-law"} {
		t.Run(rule, func(t *testing.T) {
			r := tierRAdmRun(t, env, "tierr-admission-overload", tierRAdmSteps, map[string]any{"rule": rule, "tenant_capacity": 16, "interval": "100ms", "target": "5ms"})
			maxWait, served := admHistMax(r.final, "queue_wait")
			rate := float64(r.ok) / r.elapsed.Seconds()
			t.Logf("%s: %d ok, %d shed in %v (%.0f/s; the run that sheds only on overflow served %.0f/s), max served wait bucket %v",
				rule, r.ok, r.shed, r.elapsed, rate, baseRate, time.Duration(maxWait))
			if r.shed == 0 || r.ok == 0 || served == 0 {
				t.Fatalf("overload did not shed and serve: %d shed, %d ok", r.shed, r.ok)
			}
			if rate < tierRAdmFloor*baseRate {
				t.Fatalf("shedding idled the database: %.0f/s served against %.0f/s without the rule", rate, baseRate)
			}
			if rule == "timeout" && maxWait >= admission.DefaultInterval {
				t.Fatalf("a request was served after waiting %v, the interval is %v", time.Duration(maxWait), time.Duration(admission.DefaultInterval))
			}
			if ev(r.final, "replies", obs.Labels{Reason: "shed"}) < int64(r.shed) {
				t.Fatalf("shed row %d, clients saw %d", ev(r.final, "replies", obs.Labels{Reason: "shed"}), r.shed)
			}
		})
	}
}
