package simtoy

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

var updateShrunk = flag.Bool("update-shrunk", false, "rewrite testdata/shrunk/ from the minimiser")

var plantedBugScenario = filepath.Join("testdata", "shrunk", "planted-bug-chunk.faults")

func netKindNames() []sim.KindName {
	var out []sim.KindName
	for _, k := range simnet.FaultKinds() {
		out = append(out, sim.KindName{Kind: k, Name: simnet.FaultName(k)})
	}
	return out
}

func netFloor(k sim.FaultKind) sim.FaultFloor {
	switch k {
	case simnet.FaultChunk:
		return sim.FaultFloor{Arg2: 1}
	case simnet.FaultClear:
		return sim.FaultFloor{Arg1: int64(simnet.FaultReset)}
	}
	return sim.FaultFloor{}
}

func runPlanted(f []sim.Fault, planted bool) *Result {
	cfg := Config{Seed: 1, Options: BaseOptions(), Faults: f}
	cfg.plantBug = planted
	return Run(cfg)
}

func plantedFails(f []sim.Fault) bool { return runPlanted(f, true).Mismatch }

func firstPlantedBugSchedule(t *testing.T) []sim.Fault {
	for seed := int64(1); seed <= 40; seed++ {
		r := sim.NewStream(seed, "shrink-bytes")
		data := make([]byte, 8*40)
		for i := range data {
			data[i] = byte(r.Uint32())
		}
		sched := sim.ScheduleFromBytes(data, shrinkSpace())
		if len(sched) >= 30 && plantedFails(sched) {
			return sched
		}
	}
	t.Fatal("no failing schedule was generated")
	return nil
}

func valueSum(s []sim.Fault) int64 {
	var n int64
	for _, f := range s {
		n += int64(f.At) + f.Arg1 + f.Arg2
	}
	return n
}

func TestPlantedBugMinimisedBeyondShrink(t *testing.T) {
	sched := firstPlantedBugSchedule(t)
	shrunk := sim.Shrink(sched, plantedFails)
	minimal := sim.Minimize(sched, plantedFails, netFloor)
	if !plantedFails(minimal) {
		t.Fatalf("the minimised schedule passes: %+v", minimal)
	}
	if len(minimal) > len(shrunk) || valueSum(minimal) >= valueSum(shrunk) {
		t.Fatalf("value lowering did no work: Shrink %+v (sum %d), Minimize %+v (sum %d)", shrunk, valueSum(shrunk), minimal, valueSum(minimal))
	}
	text, err := sim.FormatSchedule(minimal, netKindNames())
	if err != nil {
		t.Fatal(err)
	}
	if *updateShrunk {
		if err := os.WriteFile(plantedBugScenario, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(plantedBugScenario)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != text {
		t.Fatalf("%s differs from the minimiser's output (review, then rerun with -update-shrunk):\n%s", plantedBugScenario, text)
	}
	t.Logf("%d faults; Shrink kept %d (value sum %d); Minimize kept %d (value sum %d)", len(sched), len(shrunk), valueSum(shrunk), len(minimal), valueSum(minimal))
}

func TestShrunkScenarioPlantedBugReplays(t *testing.T) {
	text, err := os.ReadFile(plantedBugScenario)
	if err != nil {
		t.Fatal(err)
	}
	sched, err := sim.ParseSchedule(string(text), netKindNames())
	if err != nil {
		t.Fatal(err)
	}
	if len(sched) == 0 {
		t.Fatal("the scenario file holds no fault")
	}
	if res := runPlanted(sched, true); !errors.Is(res.Err, ErrMismatch) {
		t.Fatalf("with the bug planted the scenario did not fail: %v", res.Err)
	}
	if res := runPlanted(sched, false); res.Err != nil || res.Mismatch || res.Quiesce != nil {
		t.Fatalf("without the bug the scenario failed: %v %v", res.Err, res.Quiesce)
	}
	if again := sim.Minimize(sched, plantedFails, netFloor); !slices.Equal(again, sched) {
		t.Fatalf("the scenario file is not minimal: %+v", again)
	}
}

func TestPlantedBugWorkloadSizesLowered(t *testing.T) {
	text, err := os.ReadFile(plantedBugScenario)
	if err != nil {
		t.Fatal(err)
	}
	sched, err := sim.ParseSchedule(string(text), netKindNames())
	if err != nil {
		t.Fatal(err)
	}
	fails := func(v []int64) bool {
		cfg := Config{Seed: 1, Options: BaseOptions(), Faults: sched, Clients: int(v[0]), Lines: int(v[1]), MaxLine: int(v[2])}
		cfg.plantBug = true
		return Run(cfg).Mismatch
	}
	in := []int64{DefaultClients, DefaultLines, DefaultMaxLine}
	out := sim.LowerInts(in, []int64{1, 1, 1}, fails)
	if !fails(out) || !slices.Equal(in, []int64{DefaultClients, DefaultLines, DefaultMaxLine}) {
		t.Fatalf("lowered sizes %v do not fail, or the input moved", out)
	}
	if out[0]+out[1]+out[2] >= in[0]+in[1]+in[2] {
		t.Fatalf("no workload size was lowered: %v", out)
	}
	t.Logf("clients, lines, max line: %v -> %v", in, out)
}
