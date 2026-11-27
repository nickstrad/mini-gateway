package e2e

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

func mustFail(t *testing.T, spec Spec, check func(*SimRun) error) string {
	t.Helper()
	r, err := RunSim(spec, 1, SimOptions{})
	if err != nil {
		return err.Error()
	}
	if err := check(r); err != nil {
		return err.Error()
	}
	t.Fatalf("%s passed with its mechanism removed: the scenario cannot fail", spec.Name)
	return ""
}

func mustPass(t *testing.T, spec Spec, check func(*SimRun) error) *SimRun {
	t.Helper()
	r, err := RunSim(spec, 1, SimOptions{})
	if err != nil {
		t.Fatalf("%s: %v", spec.Name, err)
	}
	if err := check(r); err != nil {
		t.Fatalf("%s: %v", spec.Name, err)
	}
	return r
}

func TestCanaryPoolCapPlantBreaksTheHardCap(t *testing.T) {
	t.Logf("caught: %s", mustFail(t, HardCapSpec(pool.PlantNoCap), checkHardCap))
}

func TestAblationColdStartWithoutTheCreationRateExceedsTheBound(t *testing.T) {
	t.Logf("caught: %s", mustFail(t, ColdStartSpec(pool.PlantNoRate), checkColdStart))
}

func TestCanaryPoolCheckDeadlinePlantHoldsTheQueuedRequest(t *testing.T) {
	t.Logf("caught: %s", mustFail(t, CheckHangsSpec(pool.PlantNoCheckDeadline), checkCheckHangs))
}

func TestCanaryPoolSuspectPlantCostsClientsAfterAMassDeath(t *testing.T) {
	t.Logf("caught: %s", mustFail(t, MassDeathSpec(PathBlackhole, pool.PlantNoSuspect), checkMassDeath))
}

func TestCanaryPoolOnSwitchIgnoringTheTagCrossesTenants(t *testing.T) {
	t.Logf("caught: %s", mustFail(t, PolicySpec(proxy.ResetOnSwitch, false, pool.PlantIgnoreTag), checkPolicy(proxy.ResetOnSwitch, false)))
}

func TestAblationRestartWithoutTheBreakerAnswersNo53M03(t *testing.T) {
	t.Logf("caught: %s", mustFail(t, RestartSpec(false), checkRestart))
}

func TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount(t *testing.T) {
	sp := AdmissionRestartSpec()
	sp.Plant |= proxy.PlantBreakerFlushPops
	got := mustFail(t, sp, checkAdmissionRestart)
	if !strings.Contains(got, "sim: invariant") || !strings.Contains(got, "admission:") {
		t.Fatalf("the plant was caught by something other than the admission accounting: %s", got)
	}
	t.Logf("caught: %s", got)
}

func TestAblationLifetimesWithoutJitterExpireTogether(t *testing.T) {
	if testing.Short() {
		t.Skip("long scenario")
	}
	t.Logf("caught: %s", mustFail(t, LifetimesSpec(true), checkLifetimes))
}

func TestAblationNightTrickleWithEveryRemedyOffHangsTheRamp(t *testing.T) {
	if testing.Short() {
		t.Skip("two virtual hours")
	}
	r := mustPass(t, NightTrickleSpec(Remedies{}), checkNight(false))
	t.Logf("all remedies off: %d client errors, %d no-reply deadlines, %s", anyClientErrors(r), poolSnap(r).NoReply, r.Path)
}

func TestNightTrickleEachRemedyAloneCarriesTheRamp(t *testing.T) {
	if testing.Short() {
		t.Skip("two virtual hours per arm")
	}
	for _, m := range []Remedies{{Keepalive: true}, {IdleTimeout: true}, {Check: true}} {
		r, err := RunSim(NightTrickleSpec(m), 1, SimOptions{})
		if err != nil {
			t.Fatalf("%s: %v", m.name(), err)
		}
		s := poolSnap(r)
		t.Logf("%s alone: %d client errors (%d 53M03, %d 53M01, %d 08006), %d no-reply deadlines, %d checks failed, breaker opened %d times, %d idle closes, %s",
			m.name(), anyClientErrors(r), clientErrors(r, "53M03"), clientErrors(r, "53M01"), clientErrors(r, "08006"), s.NoReply,
			s.Closes[pool.CloseCheckFailed], s.BreakerTransitions[pool.TransClosedOpen], s.Closes[pool.CloseIdle], r.Path)
		if m.Check {
			if s.NoReply != 0 || clientErrors(r, "08006")+clientErrors(r, "08007") != 0 {
				t.Errorf("check alone: %d statements hung on a dead connection", s.NoReply)
			}
			if n := anyClientErrors(r); n != 0 || s.BreakerTransitions[pool.TransClosedOpen] != 0 || s.Closes[pool.CloseCheckFailed] == 0 {
				t.Errorf("check alone: %d client errors, breaker opened %d times, %d checks failed; failed checks of idle connections must not open the breaker",
					n, s.BreakerTransitions[pool.TransClosedOpen], s.Closes[pool.CloseCheckFailed])
			}
			continue
		}
		if err := checkNight(true)(r); err != nil {
			t.Errorf("%s alone: %v", m.name(), err)
		}
	}
}

func relayWorld(t *testing.T, plan PathPlan, scripts []Script) ([]ClientResult, *pathRelay) {
	t.Helper()
	relay := newPathRelay(plan)
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 3, Scenario: "path-self-test", Tenants: 2,
		Fake: fakepg.Config{Rows: clientRows(2)}, BeforeProxy: relay.listen,
		Engine: proxy.Config{Connections: 1, Timeouts: proxy.Timeouts{Statement: time.Second}}})
	if err != nil {
		t.Fatal(err)
	}
	res := make([]ClientResult, len(scripts))
	for i, sc := range scripts {
		newSimClient(w.S, w.Net, faultAddr, w.Cfg.Options, i, sc, &res[i]).start()
	}
	if err := w.S.RunFor(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	return res, relay
}

func TestPathFaultOnOneConnectionLeavesTheOtherUnchangedAndClearingRestores(t *testing.T) {
	a := Script{Tenant: 0, Steps: []Step{{SQL: read(0, "a")}, {SQL: read(0, "b"), Delay: 2 * time.Second}}}
	b := Script{Tenant: 1, Start: 10 * time.Millisecond, Steps: []Step{{SQL: read(1, "a")}, {SQL: read(1, "b"), Delay: 3 * time.Second}}}
	clean, _ := relayWorld(t, PathPlan{}, []Script{a, b})
	faulted, relay := relayWorld(t, PathPlan{Events: []PathEvent{{At: time.Second, Fault: PathBlackhole, Target: 2}}}, []Script{a, b})
	if relay.stats.Blackholed != 1 {
		t.Fatalf("blackholed %d", relay.stats.Blackholed)
	}
	if !slices.EqualFunc(clean[0].Steps, faulted[0].Steps, slices.Equal) {
		t.Fatalf("a blackhole on another connection changed this one: %v / %v", clean[0], faulted[0])
	}
	if slices.EqualFunc(clean[1].Steps, faulted[1].Steps, slices.Equal) {
		t.Fatalf("the blackholed connection behaved as if never faulted: %v", faulted[1])
	}
	idle := Script{Tenant: 0, Steps: []Step{{SQL: read(0, "a")}, {SQL: read(0, "b"), Delay: 3 * time.Second}}}
	dropped, r1 := relayWorld(t, PathPlan{IdleDrop: 2 * time.Second}, []Script{idle})
	cleared, r2 := relayWorld(t, PathPlan{IdleDrop: 2 * time.Second, Events: []PathEvent{{At: time.Second, Clear: true}}}, []Script{idle})
	never, _ := relayWorld(t, PathPlan{}, []Script{idle})
	if r1.stats.IdleDrops != 1 || slices.EqualFunc(dropped[0].Steps, never[0].Steps, slices.Equal) {
		t.Fatalf("idle_drop did not drop: %s %v", r1, dropped[0])
	}
	if r2.stats.IdleDrops != 0 || !slices.EqualFunc(cleared[0].Steps, never[0].Steps, slices.Equal) {
		t.Fatalf("clearing idle_drop did not restore a never-faulted network: %s %v / %v", r2, cleared[0], never[0])
	}
	kept, r3 := relayWorld(t, PathPlan{IdleDrop: 2 * time.Second, Keepalive: time.Second}, []Script{idle})
	if r3.stats.IdleDrops != 0 || !slices.EqualFunc(kept[0].Steps, never[0].Steps, slices.Equal) {
		t.Fatalf("keepalive shorter than the idle drop did not keep the flow: %s", r3)
	}
}
