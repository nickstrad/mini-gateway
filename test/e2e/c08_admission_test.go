package e2e

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

const admReplaySeeds = 1

func admReplay(name, arm string, seed int64) string {
	return fmt.Sprintf("REPLAY: go test ./test/e2e -run '^TestScenarioAdmission' -seed=%d -count=1 (scenario %s, arm %s)", seed, name, arm)
}

func admSeeds(t *testing.T) []int64 {
	s := simtest.Seeds(t)
	if len(s) > admReplaySeeds {
		s = s[:admReplaySeeds]
	}
	return s
}

func runAdm(t *testing.T, spec AdmSpec, seed int64) *AdmRun {
	t.Helper()
	replay := admReplay(spec.Scenario, spec.Arm.Name, seed)
	a, err := RunAdmission(spec, seed, runDir(t), replay)
	if err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if err := a.Flush.Check(); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	b, err := RunAdmission(spec, seed, t.TempDir(), replay)
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
	if fa := a.Final.Adm; fa.Tenants.Value != 0 || fa.Queued.Value != 0 || fa.LIFOTenants != 0 || fa.Shedding != 0 {
		t.Fatalf("steady state: %+v\n%s", fa, replay)
	}
	return a
}

func servedAtPhaseEnd(r *AdmRun) int64 { return r.Samples[0].Adm.Served }

func waitQuantile(h obs.HistogramSnapshot, pct int64) int64 {
	var seen int64
	for _, b := range h.Buckets {
		seen += b.Count
		if 100*seen >= pct*h.Count {
			return b.Hi
		}
	}
	return h.Max
}

func isProductRule(k admission.RuleKind) bool {
	return k == admission.RuleTimeout || k == admission.RuleControlLaw
}

func checkRuleSheds(s admission.Snapshot, k admission.RuleKind) error {
	if isProductRule(k) && s.Shed[admission.ReasonRule] == 0 {
		return failf("the %v rule shed nothing by the rule (sheds %v): overflow alone held the bound", k, s.Shed)
	}
	return nil
}

func checkModerateConvergence(r *AdmRun, fifo *AdmRun) error {
	if r.Spec.Arm.Rule != admission.RuleControlLaw || r.Spec.LoadPct > 150 {
		return nil
	}
	h, f := r.Samples[0].Adm.QueueWait, fifo.Samples[0].Adm.QueueWait
	if p50 := waitQuantile(h, 50); p50 > int64(admission.DefaultTarget) {
		return failf("claim 3: under moderate overload the control law's median served wait %d did not return to the target %d", p50, admission.DefaultTarget)
	}
	if p99, fp50 := waitQuantile(h, 99), waitQuantile(f, 50); p99 >= fp50 {
		return failf("claim 3: the control law's p99 served wait %d is not below the FIFO control's median %d", p99, fp50)
	}
	return nil
}

func checkHeadToHead(r *AdmRun, fifo *AdmRun) error {
	fifoServed := servedAtPhaseEnd(fifo)
	s := r.Samples[0].Adm
	if s.Pushes == 0 || s.Served == 0 {
		return failf("counters did not move: %+v", s)
	}
	if s.Queued.Peak > int64(r.Spec.TenantCap) || s.Tenants.Peak > 1 {
		return failf("claim 1: depth peak %d above capacity %d", s.Queued.Peak, r.Spec.TenantCap)
	}
	if r.Spec.Arm.Rule == admission.RuleTimeout && s.QueueWait.Max >= admission.DefaultInterval {
		return failf("claim 2: a request was served after %d, the interval is %d", s.QueueWait.Max, admission.DefaultInterval)
	}
	if r.Spec.LoadPct >= 200 && 100*s.Served < 95*fifoServed {
		return failf("claim 4: served %d, below 95%% of the never-idle FIFO arm's %d", s.Served, fifoServed)
	}
	if r.Spec.LoadPct >= 200 && r.Spec.Arm.Rule != admission.RuleFIFO && s.ShedTotal() == 0 {
		return failf("an overloaded arm shed nothing")
	}
	if err := checkRuleSheds(s, r.Spec.Arm.Rule); err != nil {
		return err
	}
	return checkModerateConvergence(r, fifo)
}

func TestScenarioAdmissionHeadToHead(t *testing.T) {
	if testing.Short() {
		t.Skip("overload scenarios run in the full suite and the sweep")
	}
	for _, load := range HeadToHeadLoads() {
		t.Run(fmt.Sprintf("load=%d", load), func(t *testing.T) {
			t.Parallel()
			for _, seed := range admSeeds(t) {
				fifo := runAdm(t, HeadToHeadSpec(ArmFIFO, load), seed)
				capacity := int64(AdmPhase) / int64(AdmLeaseTime)
				if load >= 200 && 100*servedAtPhaseEnd(fifo) < 95*capacity {
					t.Fatalf("the FIFO arm served %d in the phase, capacity is %d", servedAtPhaseEnd(fifo), capacity)
				}
				for _, arm := range AdmArms() {
					r := fifo
					if arm != ArmFIFO {
						r = runAdm(t, HeadToHeadSpec(arm, load), seed)
					}
					if err := checkHeadToHead(r, fifo); err != nil {
						t.Fatalf("%s: %v\n%s", arm.Name, err, admReplay(r.Spec.Scenario, arm.Name, seed))
					}
				}
			}
		})
	}
}

func phaseSamples(r *AdmRun) (first, last AdmSample) { return r.Samples[0], r.Samples[r.Spec.Phases-1] }

func checkAntiBloat(r *AdmRun) error {
	first, last := phaseSamples(r)
	if r.Spec.Arm.Rule == admission.RuleFIFO {
		if last.Backlog < 3*first.Backlog || first.Backlog == 0 {
			return failf("FIFO control: client backlog %d at T and %d at 4T; it must grow without bound", first.Backlog, last.Backlog)
		}
		if last.Adm.QueueWait.Max < admission.DefaultInterval || last.Adm.Queued.Value < int64(r.Spec.Clients)/2 {
			return failf("ablation: with no rule the standing queue must persist (max wait %d, queued %d)", last.Adm.QueueWait.Max, last.Adm.Queued.Value)
		}
		return nil
	}
	if first.Adm.Queued.Peak != last.Adm.Queued.Peak || last.Backlog > first.Backlog+int64(r.Spec.Clients) {
		return failf("depth at T (peak %d, backlog %d) and at 4T (peak %d, backlog %d) differ", first.Adm.Queued.Peak, first.Backlog, last.Adm.Queued.Peak, last.Backlog)
	}
	if last.Adm.Queued.Peak > int64(r.Spec.TenantCap) {
		return failf("depth %d above capacity %d", last.Adm.Queued.Peak, r.Spec.TenantCap)
	}
	if r.Spec.Arm.Rule == admission.RuleTimeout && last.Adm.QueueWait.Max >= admission.DefaultInterval {
		return failf("claim 2: served after %d", last.Adm.QueueWait.Max)
	}
	if r.Spec.Arm.Rule == admission.RuleControlLaw && last.Adm.QueueWait.Max >= int64(r.Spec.TenantCap)*int64(AdmLeaseTime)+admission.DefaultInterval {
		return failf("claim 3: served after %d, the capacity bound is %d", last.Adm.QueueWait.Max, int64(r.Spec.TenantCap)*int64(AdmLeaseTime))
	}
	if last.Adm.ShedTotal() == 0 {
		return failf("nothing shed under 10x overload")
	}
	if err := checkRuleSheds(last.Adm, r.Spec.Arm.Rule); err != nil {
		return err
	}
	return checkRecovery(r)
}

func checkRecovery(r *AdmRun) error {
	after := r.Samples[r.Spec.Phases]
	if after.Adm.RuleStates[admission.StateNormal] != after.Adm.Tenants.Value || after.Adm.LIFOTenants != 0 {
		return failf("claim 11: %v after the overload ended the rule states are %v, LIFO %d", AdmRecoverAfter, after.Adm.RuleStates, after.Adm.LIFOTenants)
	}
	end := r.Samples[r.Spec.Phases+1]
	served := end.Adm.Served - after.Adm.Served
	shed := end.Adm.ShedTotal() - after.Adm.ShedTotal()
	if served == 0 || shed != 0 {
		return failf("claim 11: the light load after recovery: %d served, %d shed", served, shed)
	}
	rec := end.Work.Tenants[1]
	if rec.OK == 0 || rec.Latency.Max > int64(4*AdmLeaseTime) {
		return failf("claim 11: the wait did not return to the uncongested level: %d ok, max latency %d", rec.OK, rec.Latency.Max)
	}
	return nil
}

func TestScenarioAdmissionAntiBloat(t *testing.T) {
	if testing.Short() {
		t.Skip("overload scenarios run in the full suite and the sweep")
	}
	for _, arm := range AntiBloatArms() {
		t.Run(arm.Name, func(t *testing.T) {
			t.Parallel()
			for _, seed := range admSeeds(t) {
				r := runAdm(t, AntiBloatSpec(arm), seed)
				if err := checkAntiBloat(r); err != nil {
					t.Fatalf("%v\n%s", err, admReplay(r.Spec.Scenario, arm.Name, seed))
				}
			}
		})
	}
}

func TestCanaryAdmissionWithoutTheHeadSweepServesLate(t *testing.T) {
	if testing.Short() {
		t.Skip("overload scenarios run in the full suite and the sweep")
	}
	spec := HeadToHeadSpec(ArmTimeout, 1000)
	spec.Scenario = "admission-canary-head-sweep"
	spec.TenantCap = AdmTotalCap
	spec.Plant = admission.PlantNoHeadSweep
	r, err := RunAdmission(spec, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkHeadToHead(r, r); err == nil {
		t.Fatal("with the head sweep planted out the timeout rule still bounded the served wait")
	}
	spec.Plant = 0
	r, err = RunAdmission(spec, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkHeadToHead(r, r); err != nil {
		t.Fatal(err)
	}
}

func TestScenarioAdmissionFirstBurst(t *testing.T) {
	for _, rule := range []admission.RuleKind{admission.RuleTimeout, admission.RuleControlLaw} {
		t.Run(rule.String(), func(t *testing.T) {
			t.Parallel()
			simtest.Sweep(t, func(t *testing.T, seed int64) {
				runScenario(t, Scenario{Spec: FirstBurstSpec(rule, 0), Check: CheckFirstBurst}, seed)
			})
		})
	}
}

func TestCanaryAdmissionFirstBurstBugShedsTheBurst(t *testing.T) {
	r, err := RunSim(FirstBurstSpec(admission.RuleTimeout, admission.PlantFirstBurst), 1, SimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if CheckFirstBurst(r) == nil {
		t.Fatalf("with lastEmpty updated only at dequeue the first burst was not shed: %+v %v", r.World.Proxy.Engine.AdmissionSnapshot(), r.Clients)
	}
}
