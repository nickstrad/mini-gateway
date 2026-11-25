package pgwire_test

import (
	"strings"
	"testing"
)

func scenarioNamed(t *testing.T, name string) wireScenario {
	for _, sc := range wireScenarios() {
		if sc.name == name {
			return sc
		}
	}
	t.Fatalf("no scenario %s", name)
	return wireScenario{}
}

func TestPlantedEarlyBytesBugFailsTheSmuggleScenario(t *testing.T) {
	sc := scenarioNamed(t, "wire-client-smuggle")
	for seed := int64(1); seed <= 3; seed++ {
		if err := sc.verify(runWire(t, seed, sc, "")); err != nil {
			t.Fatalf("seed %d: the scenario fails without the planted bug: %v", seed, err)
		}
		planted := sc
		planted.plant = true
		err := planted.verify(runWire(t, seed, planted, ""))
		if err == nil {
			t.Fatalf("seed %d: the smuggle scenario passed with the early-bytes rule switched off", seed)
		}
		if !strings.Contains(err.Error(), "client startup failures") {
			t.Fatalf("seed %d: failed for another reason: %v", seed, err)
		}
	}
}

func TestEveryWireScenarioHasItsAnalysisFiles(t *testing.T) {
	for _, sc := range wireScenarios() {
		for _, f := range []string{"startup-failures.sql", "tls-requests.sql"} {
			requireAnalysisFile(t, sc.name, f)
		}
	}
}
