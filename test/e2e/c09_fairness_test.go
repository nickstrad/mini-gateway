package e2e

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

const (
	fairSweepSeeds    = 10
	fairAblationSeeds = 3
)

func fairReplay(name, arm string, seed int64) string {
	return fmt.Sprintf("REPLAY: go test ./test/e2e -run '^TestScenarioFair' -seed=%d -count=1 (scenario %s, arm %s)", seed, name, arm)
}

func fairSeeds(t *testing.T) []int64 {
	s := simtest.Seeds(t)
	if len(s) > fairSweepSeeds {
		s = s[:fairSweepSeeds]
	}
	return s
}

func runFair(t *testing.T, spec FairSpec, seed int64) *FairRun {
	t.Helper()
	replay := fairReplay(spec.Scenario, spec.Arm.Name, seed)
	a, err := RunFair(spec, seed, runDir(t), replay)
	if err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if err := a.Flush.Check(); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	b, err := RunFair(spec, seed, t.TempDir(), replay)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, replay)
	}
	if a.World.S.Digests() != b.World.S.Digests() {
		t.Fatalf("replay law: digests differ %s / %s\n%s", a.World.S.Digests(), b.World.S.Digests(), replay)
	}
	if !bytes.Equal(flushedFiles(t, a.Flush.RunDir), flushedFiles(t, b.Flush.RunDir)) {
		t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
	}
	if err := a.World.Proxy.Engine.Quiesce(); err != nil {
		t.Fatalf("steady state: %v\n%s", err, replay)
	}
	if f := a.Adm.Fair; f.InFlight.Value != 0 || f.States.Value != 0 || f.Active.Value != 0 || a.Adm.Tenants.Value != 0 {
		t.Fatalf("steady state: fair %+v, tenant queues %d\n%s", f, a.Adm.Tenants.Value, replay)
	}
	return a
}

func shedCode(t workload.TenantSnapshot) int64 {
	for _, c := range t.ErrorsByCode {
		if c.Code == "53M01" {
			return c.Count
		}
	}
	return 0
}

func floodShedShare(r *FairRun) (flood, all int64) {
	for _, t := range r.Final.Tenants {
		n := shedCode(t)
		all += n
		if t.Name == FloodTenant() {
			flood += n
		}
	}
	return flood, all
}

func checkQuiet(name string, got, alone workload.TenantSnapshot) error {
	if alone.OK == 0 || alone.OK != alone.Intended || alone.Errors+alone.Lost+alone.TimedOut+alone.Dropped != 0 {
		return failf("%s alone: %d ok of %d intended, %d errors: the control must succeed in full", name, alone.OK, alone.Intended, alone.Errors)
	}
	if got.OK != alone.OK || got.Intended != alone.Intended || got.Errors+got.Lost+got.TimedOut+got.Dropped != 0 {
		return failf("%s: %d ok of %d (errors %d, lost %d, timed out %d, dropped %d) against %d ok alone", name, got.OK, got.Intended,
			got.Errors, got.Lost, got.TimedOut, got.Dropped, alone.OK)
	}
	p, pa := waitQuantile(got.Latency, 99), waitQuantile(alone.Latency, 99)
	if p > 2*pa {
		return failf("%s: p99 latency %d ns, alone %d ns: more than twice", name, p, pa)
	}
	return nil
}

func checkFlooderTakesTheShedding(r *FairRun) error {
	flood, all := floodShedShare(r)
	if all == 0 || 100*flood <= 95*all {
		return failf("the flooder took %d of %d sheds (53M01): want more than 95%%", flood, all)
	}
	return nil
}

func checkRecovered(r *FairRun) error {
	before, after := TenantStats(r.Mid, FloodTenant()), TenantStats(r.Final, FloodTenant())
	intended, ok, errs := after.Intended-before.Intended, after.OK-before.OK, after.Errors-before.Errors
	if intended < FairRecoveryClients || ok < intended || errs != 0 || after.OK+after.Errors != after.Intended {
		return failf("recovery: the flooder's light load after the flood: %d ok of %d, %d errors (whole run %d ok + %d errors of %d)",
			ok, intended, errs, after.OK, after.Errors, after.Intended)
	}
	return nil
}

func checkNoisy(r, alone *FairRun) error {
	if err := checkQuiet(QuietTenant(0), TenantStats(r.Final, QuietTenant(0)), TenantStats(alone.Final, QuietTenant(0))); err != nil {
		return err
	}
	if err := checkFlooderTakesTheShedding(r); err != nil {
		return err
	}
	return checkRecovered(r)
}

func floodAtShare(r *FairRun) int64 {
	for _, row := range r.Adm.PerTenant {
		if row.Tenant == FloodTenant() {
			return row.AtShare
		}
	}
	return 0
}

func checkFairMoved(r *FairRun) error {
	f := r.Adm.Fair
	var quietAtShare int64
	for _, row := range r.Adm.PerTenant {
		if row.Tenant != FloodTenant() {
			quietAtShare += row.AtShare
		}
	}
	if !f.On || f.Rounds == 0 || f.Saturated == 0 || quietAtShare == 0 || floodAtShare(r) == 0 || f.LeaseStarts != r.Engine.LeasesStarted {
		return failf("the fair dispatcher was not exercised: %+v, quiet time at share %d, engine leases %d", f, quietAtShare, r.Engine.LeasesStarted)
	}
	return nil
}

func TestScenarioFairNoisyNeighbour(t *testing.T) {
	if testing.Short() {
		t.Skip("fairness scenarios run in the full suite and the sweep")
	}
	for _, seed := range fairSeeds(t) {
		fair := runFair(t, NoisySpec(FairArmFair), seed)
		alone := runFair(t, NoisySpec(FairArmAlone), seed)
		if err := checkNoisy(fair, alone); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(fair.Spec.Scenario, "fair", seed))
		}
		if err := checkFairMoved(fair); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(fair.Spec.Scenario, "fair", seed))
		}
		if testing.Verbose() {
			t.Logf("seed %d: quiet tenant p99 %d ns with the flood, %d ns alone", seed, waitQuantile(TenantStats(fair.Final, QuietTenant(0)).Latency, 99),
				waitQuantile(TenantStats(alone.Final, QuietTenant(0)).Latency, 99))
		}
	}
}

func TestScenarioFairNoisyNeighbourAblation(t *testing.T) {
	if testing.Short() {
		t.Skip("fairness scenarios run in the full suite and the sweep")
	}
	seeds := fairSeeds(t)
	if len(seeds) > fairAblationSeeds {
		seeds = seeds[:fairAblationSeeds]
	}
	for _, seed := range seeds {
		alone := runFair(t, NoisySpec(FairArmAlone), seed)
		fifo := runFair(t, NoisySpec(FairArmFIFO), seed)
		b, ba := TenantStats(fifo.Final, QuietTenant(0)), TenantStats(alone.Final, QuietTenant(0))
		if err := checkQuiet(QuietTenant(0), b, ba); err == nil {
			t.Fatalf("ablation: with one shared FIFO queue the quiet tenant kept its outcomes\n%s", fairReplay(fifo.Spec.Scenario, "fifo", seed))
		}
		if p, pa := waitQuantile(b.Latency, 99), waitQuantile(ba.Latency, 99); p <= 10*pa {
			t.Fatalf("ablation: the quiet tenant's p99 behind one shared queue is %d ns, alone %d ns: it must starve (above ten times)\n%s", p, pa,
				fairReplay(fifo.Spec.Scenario, "fifo", seed))
		}
		if testing.Verbose() {
			t.Logf("seed %d: quiet tenant p99 %d ns alone, %d ns with one shared queue; %d ok of %d, errors %v", seed, waitQuantile(ba.Latency, 99),
				waitQuantile(b.Latency, 99), b.OK, b.Intended, b.ErrorsByCode)
		}
	}
}

func TestScenarioFairManyTenants(t *testing.T) {
	if testing.Short() {
		t.Skip("fairness scenarios run in the full suite and the sweep")
	}
	for _, seed := range fairSeeds(t) {
		fair := runFair(t, ManySpec(FairArmFair), seed)
		alone := runFair(t, ManySpec(FairArmAlone), seed)
		for i := range FairManyQuiet {
			name := QuietTenant(i)
			if err := checkQuiet(name, TenantStats(fair.Final, name), TenantStats(alone.Final, name)); err != nil {
				t.Fatalf("%v\n%s", err, fairReplay(fair.Spec.Scenario, "fair", seed))
			}
		}
		if err := checkFlooderTakesTheShedding(fair); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(fair.Spec.Scenario, "fair", seed))
		}
		if err := checkRecovered(fair); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(fair.Spec.Scenario, "fair", seed))
		}
		if err := checkFairMoved(fair); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(fair.Spec.Scenario, "fair", seed))
		}
		if fair.Adm.Fair.Active.Peak <= 32 && len(fair.Adm.PerTenant) <= 32 {
			t.Fatalf("the scenario must exceed the Prometheus tenant label bound: %d rows", len(fair.Adm.PerTenant))
		}
	}
}

func checkHeld(r *FairRun) error {
	q, h := TenantStats(r.Final, QuietTenant(0)), TenantStats(r.Final, FloodTenant())
	if !r.Watched {
		return failf("the quiet tenant never ended its continuous activity: the window was not closed")
	}
	share := FairHeldConns / 2
	if r.HeldPeak > share {
		return failf("the tenant holding transactions held %d connections while the quiet tenant was active: its share is %d", r.HeldPeak, share)
	}
	if r.HeldPeak != share || h.OK == 0 || h.OK != h.Intended || r.HeldOpen != 0 || q.OK != q.Intended || q.Errors != 0 || h.Errors != 0 {
		return failf("held %d (want exactly the share %d: it wanted more), transactions ok %d of %d errors %d, %d still open when the quiet tenant stopped, quiet ok %d of %d",
			r.HeldPeak, share, h.OK, h.Intended, h.Errors, r.HeldOpen, q.OK, q.Intended)
	}
	return nil
}

func TestScenarioFairHeldTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("fairness scenarios run in the full suite and the sweep")
	}
	for _, seed := range fairSeeds(t) {
		r := runFair(t, HeldSpec(FairArmFair), seed)
		if err := checkHeld(r); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(r.Spec.Scenario, "fair", seed))
		}
		if err := checkFairMoved(r); err != nil {
			t.Fatalf("%v\n%s", err, fairReplay(r.Spec.Scenario, "fair", seed))
		}
	}
}

func TestCanaryFairHeldTransactionsWithoutTheShareBound(t *testing.T) {
	arm := FairArmFair
	arm.Name, arm.Plant = "no-share-bound", admission.FairPlantNoShareBound
	r, err := RunFair(HeldSpec(arm), 1, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkHeld(r); err == nil || !strings.Contains(err.Error(), "its share is") {
		t.Fatalf("the missing share bound was not caught by the held-transaction scenario: %v", err)
	}
}

func TestCanaryFairHeldTransactionsNotCountedInFlight(t *testing.T) {
	arm := FairArmFair
	arm.Name, arm.ProxyPlant = "statement-in-flight", proxy.PlantFairStatementInFlight
	_, err := RunFair(HeldSpec(arm), 1, t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "fairness:") {
		t.Fatalf("in flight that ignores held transactions was not caught by the scenario's invariants: %v", err)
	}
}

func TestScenarioFairResultsEqualDirect(t *testing.T) {
	simtest.Sweep(t, func(t *testing.T, seed int64) {
		runScenario(t, Scenario{Spec: FairResultsSpec(), Check: CheckFairResults}, seed)
	})
}
