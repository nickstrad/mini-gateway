package e2e

import (
	"bytes"
	"os"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

const retrySweepCap = poolSweepSeeds

func retryReplay(name string, arm RetryArm, seed int64) string {
	return "REPLAY: go test ./test/e2e -run '^TestScenarioRetry$/^" + name + "$/^" + arm.String() + "$' -seed=" + itoa(int(seed)) + " -count=1"
}

func runRetry(t *testing.T, sp RetrySpec, arm RetryArm, seed int64) *RetryRun {
	t.Helper()
	replay := retryReplay(sp.Name, arm, seed)
	a, err := RunRetry(sp, arm, seed, runDir(t), replay)
	if err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	if arm == ArmEverything {
		err = a.CheckAblation()
	} else {
		err = a.CheckProduct()
	}
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, a.Summary(), replay)
	}
	if err := a.Flush.Check(); err != nil {
		t.Fatalf("%v\n%s", err, replay)
	}
	b, err := RunRetry(sp, arm, seed, t.TempDir(), replay)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, replay)
	}
	if a.World.S.Digests() != b.World.S.Digests() {
		t.Fatalf("replay law: digests differ %s / %s\n%s", a.World.S.Digests(), b.World.S.Digests(), replay)
	}
	if !bytes.Equal(flushedFiles(t, a.Flush.RunDir), flushedFiles(t, b.Flush.RunDir)) {
		t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
	}
	return a
}

func TestScenarioRetry(t *testing.T) {
	for _, sp := range []RetrySpec{RetryStormSpec(), RetrySweepSpec()} {
		t.Run(sp.Name, func(t *testing.T) {
			if testing.Short() {
				t.Skip("retry scenarios: run without -short (sweep, gate)")
			}
			seeds := simtest.Seeds(t)
			if os.Getenv("DST_SEEDS") == "" && os.Getenv("DST_SEED") == "" {
				seeds = seeds[:1]
			}
			if len(seeds) > retrySweepCap {
				seeds = seeds[:retrySweepCap]
			}
			for arm := ArmOff; arm < NumRetryArms; arm++ {
				t.Run(arm.String(), func(t *testing.T) {
					t.Parallel()
					for _, seed := range seeds {
						t.Run("seed="+itoa(int(seed)), func(t *testing.T) { runRetry(t, sp, arm, seed) })
					}
				})
			}
		})
	}
}

func TestCanaryRetryStormCatchesAPolicyThatIgnoresReached(t *testing.T) {
	if testing.Short() {
		t.Skip("retry scenario canary: runs without -short")
	}
	sp := RetryStormSpec()
	sp.Plant = retry.PlantIgnoreReached
	r, err := RunRetry(sp, ArmOn, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CheckProduct(); err == nil {
		t.Fatalf("the storm passed with the reached-no condition removed\n%s", r.Summary())
	}
}

func TestCanaryRetrySweepCatchesAPolicyThatIgnoresReached(t *testing.T) {
	if testing.Short() {
		t.Skip("retry scenario canary: runs without -short")
	}
	sp := RetrySweepSpec()
	sp.Plant = retry.PlantIgnoreReached
	r, err := RunRetry(sp, ArmOn, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Duplicates() == 0 {
		t.Fatalf("no statement applied twice with the reached-no condition removed\n%s", r.Summary())
	}
}

func withRetryOn(sp Spec) Spec {
	sp.Retry = retry.Config{Kind: retry.KindPreForwardOnce}
	return sp
}

func TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn(t *testing.T) {
	names := map[string]bool{"e2e-results": true, "e2e-kill-before-read": true, "e2e-kill-before-write": true, "e2e-kill-during-reply": true,
		"e2e-kill-mid-transaction": true, "e2e-kill-after-reply": true, "e2e-silent-db-query": true}
	for _, sc := range Scenarios() {
		if !names[sc.Spec.Name] {
			continue
		}
		t.Run(sc.Spec.Name, func(t *testing.T) {
			r, err := RunSim(withRetryOn(sc.Spec), 1, SimOptions{Dir: t.TempDir(), Arm: "retry-on", Tamper: sc.Tamper})
			if err != nil {
				t.Fatal(err)
			}
			if err := sc.Check(r); err != nil {
				t.Fatal(err)
			}
			p, ok := r.World.Proxy.Engine.RetrySnapshot()
			if !ok || p.Kind != int64(retry.KindPreForwardOnce) || p.Retries != 0 || p.Attempts == 0 {
				t.Fatalf("policy %+v", p)
			}
		})
	}
	for _, sc := range PoolScenarios() {
		if sc.Spec.Name != "pool-results" {
			continue
		}
		r, err := RunSim(withRetryOn(sc.Spec), 1, SimOptions{Dir: t.TempDir(), Arm: "retry-on"})
		if err != nil {
			t.Fatal(err)
		}
		if err := sc.Check(r); err != nil {
			t.Fatal(err)
		}
		if _, err := RunSim(withRetryOn(sc.Spec), 1, SimOptions{Direct: true, Dir: t.TempDir(), Arm: "direct"}); err != nil {
			t.Fatal(err)
		}
	}
}
