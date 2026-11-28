package e2e

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

const weightedAtShareRatio = 10

// RetryStormWeightedSpec returns the retry-storm scenario with two tenants
// weighted 3:1 so fair shares bind alongside budgeted retries.
func RetryStormWeightedSpec() RetrySpec {
	sp := RetryStormSpec()
	sp.Name = "retry-storm-weighted"
	sp.Tenants = 2
	sp.Weights = []admission.TenantWeight{{Tenant: proxysim.TenantName(0), Weight: 3}, {Tenant: proxysim.TenantName(1), Weight: 1}}
	return sp
}

// TestScenarioRetryStormTwoTenantsOfUnequalWeight proves the retry storm holds
// all product retry invariants behind weighted fair dispatching, with the 3:1
// weights taking effect and exact replay.
func TestScenarioRetryStormTwoTenantsOfUnequalWeight(t *testing.T) {
	if testing.Short() {
		t.Skip("retry scenario: run without -short")
	}
	sp := RetryStormWeightedSpec()
	seeds := simtest.Seeds(t)
	if len(seeds) > 3 {
		seeds = seeds[:3]
	}
	for _, seed := range seeds {
		t.Run("seed="+itoa(int(seed)), func(t *testing.T) {
			t.Parallel()
			replay := "REPLAY: go test ./test/e2e -run '^TestScenarioRetryStormTwoTenantsOfUnequalWeight$' -seed=" + itoa(int(seed)) + " -count=1"
			a, err := RunRetry(sp, ArmOn, seed, "", replay)
			if err != nil {
				t.Fatalf("%v\n%s", err, replay)
			}
			if err := a.CheckProduct(); err != nil {
				t.Fatalf("%v\n%s\n%s", err, a.Summary(), replay)
			}
			adm := a.World.Proxy.Engine.AdmissionSnapshot()
			if err := adm.Check(); err != nil {
				t.Fatalf("%v\n%s", err, replay)
			}
			f := adm.Fair
			if !f.On || f.LeaseStarts == 0 || f.LeaseStarts != a.Engine.LeasesStarted || f.LeaseEnds != a.Engine.LeasesEnded || f.InFlight.Value != 0 || f.States.Value != 0 {
				t.Fatalf("fair on %v starts %d ends %d (engine %d %d) in flight %d states %d\n%s", f.On, f.LeaseStarts, f.LeaseEnds,
					a.Engine.LeasesStarted, a.Engine.LeasesEnded, f.InFlight.Value, f.States.Value, replay)
			}
			if a.Engine.RetryAsked == 0 || a.Engine.RetryEnds[0] == 0 {
				t.Fatalf("no retry completed behind the fair dispatcher: asked %d ends %v\n%s", a.Engine.RetryAsked, a.Engine.RetryEnds, replay)
			}
			if f.HeldShare == 0 {
				t.Fatalf("the weighted shares never held a request back during the storm\n%s", replay)
			}
			served := 0
			var atShare [2]int64
			for _, row := range adm.PerTenant {
				if row.Served > 0 {
					served++
				}
				for i := range atShare {
					if row.Tenant == proxysim.TenantName(i) {
						atShare[i] = row.AtShare
					}
				}
			}
			if atShare[1] == 0 || atShare[1] <= weightedAtShareRatio*atShare[0] {
				t.Fatalf("the weights did not take effect: tenant 1 (weight 1) at its share %d ns, tenant 0 (weight 3) %d ns, want more than %d times\n%s",
					atShare[1], atShare[0], weightedAtShareRatio, replay)
			}
			if served != 2 {
				t.Fatalf("%d tenants served\n%s", served, replay)
			}
			b, err := RunRetry(sp, ArmOn, seed, "", replay)
			if err != nil {
				t.Fatalf("second run: %v\n%s", err, replay)
			}
			if a.World.S.Digests() != b.World.S.Digests() {
				t.Fatalf("replay law: digests differ %s / %s\n%s", a.World.S.Digests(), b.World.S.Digests(), replay)
			}
		})
	}
}
