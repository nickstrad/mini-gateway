package pgwire_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/wireworld"
)

func scenarioNamed(t *testing.T, name string) wireworld.Scenario {
	sc, ok := wireworld.Lookup(name)
	if !ok {
		t.Fatalf("no scenario %s", name)
	}
	return sc
}

// TestPlantedEarlyBytesBugFailsTheSmuggleScenario is the planted-bug canary:
// wire-client-smuggle must pass normally and fail its startup-failures check
// when PlantEarlyBytes disables the pre-handshake byte guard.
func TestPlantedEarlyBytesBugFailsTheSmuggleScenario(t *testing.T) {
	sc := scenarioNamed(t, "wire-client-smuggle")
	for seed := int64(1); seed <= 3; seed++ {
		if err := sc.Verify(wireworld.Run(seed, sc, "")); err != nil {
			t.Fatalf("seed %d: the scenario fails without the planted bug: %v", seed, err)
		}
		planted := sc
		planted.Client = pgwire.PlantEarlyBytes
		err := planted.Verify(wireworld.Run(seed, planted, ""))
		if err == nil {
			t.Fatalf("seed %d: the smuggle scenario passed with the early-bytes rule switched off", seed)
		}
		if !strings.Contains(err.Error(), "client startup failures") {
			t.Fatalf("seed %d: failed for another reason: %v", seed, err)
		}
	}
}

// TestEveryWireScenarioHasItsAnalysisFiles proves all 19 wire scenarios have
// non-empty startup-failures, tls-requests, and handshake-failures SQL files.
func TestEveryWireScenarioHasItsAnalysisFiles(t *testing.T) {
	for _, sc := range wireworld.Scenarios() {
		for _, f := range []string{"startup-failures.sql", "tls-requests.sql", "handshake-failures.sql"} {
			requireAnalysisFile(t, sc.Name, f)
		}
	}
}

// TestWireDigests writes seed-42 trace and state digests for all 19 scenarios
// so gate/extra.sh can assert cross-process determinism.
func TestWireDigests(t *testing.T) {
	path := os.Getenv("MG_WIRE_DIGESTS")
	if path == "" {
		t.Skip("set MG_WIRE_DIGESTS to a file (the gate extra does)")
	}
	var lines []string
	for _, sc := range wireworld.Scenarios() {
		r := wireworld.Run(42, sc, "")
		if err := sc.Verify(r); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, sc.Name+" "+r.World.Sim.Digests().String())
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
