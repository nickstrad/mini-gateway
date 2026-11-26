package e2e

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

var updateShrunk = flag.String("update-shrunk", "", "minimise this raw fuzz input (Go string syntax bytes) into testdata/shrunk/queued-vanish.faults")

const queuedVanishFile = "testdata/shrunk/queued-vanish.faults"

func failsWith(plant proxy.Plant) func([]sim.Fault) bool {
	return func(ops []sim.Fault) bool { return RunOps(ops, plant).Failed() }
}

func TestShrunkQueuedVanishScenarioReplays(t *testing.T) {
	if *updateShrunk != "" {
		ops := DecodeOps([]byte(*updateShrunk))
		if !failsWith(proxy.PlantQueuedVanishKeepsSlot)(ops) {
			t.Fatal("the input does not fail with the bug planted")
		}
		min := sim.Minimize(ops, failsWith(proxy.PlantQueuedVanishKeepsSlot), nil)
		text, err := sim.FormatSchedule(min, OpNames())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.FromSlash(queuedVanishFile), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.FromSlash(queuedVanishFile))
	if err != nil {
		t.Fatal(err)
	}
	ops, err := sim.ParseSchedule(string(b), OpNames())
	if err != nil {
		t.Fatal(err)
	}
	planted := RunOps(ops, proxy.PlantQueuedVanishKeepsSlot)
	if !planted.Failed() {
		t.Fatalf("the shrunk scenario passes with the bug planted:\n%s", b)
	}
	t.Logf("with the bug: %s", planted)
	if r := RunOps(ops, 0); r.Failed() {
		t.Fatalf("the shrunk scenario fails without the bug: %s", r)
	}
	for i := range ops {
		fewer := append(append([]sim.Fault(nil), ops[:i]...), ops[i+1:]...)
		if failsWith(proxy.PlantQueuedVanishKeepsSlot)(fewer) {
			t.Fatalf("operation %d can be deleted: the file is not minimal", i)
		}
	}
}
