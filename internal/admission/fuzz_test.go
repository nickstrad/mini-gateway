package admission

import (
	"os"
	"testing"
)

const (
	plantEnv          = "MG_FUZZ_PLANT"
	plantRemoveOrder  = "admission-remove-order"
	fuzzMaxVirtual    = fuzzMaxOps * 2 * 400 * Millisecond
	fuzzMaxQueueBytes = 1 << 16
)

func fuzzPlant() Plant {
	if os.Getenv(plantEnv) == plantRemoveOrder {
		return PlantRemoveSwapsTail
	}
	return 0
}

func fuzzConfigs(plant Plant) []Config {
	var out []Config
	for _, k := range RuleKinds() {
		c := smallConfig(k)
		c.Plant = plant
		out = append(out, c)
	}
	return out
}

func fuzzSeeds() [][]byte {
	return [][]byte{
		{},
		{0, 0, 0, 3, 0, 5},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 0, 10, 7, 0, 0},
		{0, 1, 0, 0, 2, 0, 0, 3, 0, 0, 1, 0, 0, 1, 0, 3, 0, 200, 9, 0, 31, 7, 0, 0},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 5, 4, 0, 3, 0, 0, 3, 0, 0},
		{0, 0, 0, 0, 0, 0, 9, 0, 228, 3, 0, 0, 8, 0, 0, 6, 1, 0, 3, 0, 0},
	}
}

// FuzzAdmissionOps decodes bounded operation sequences (3 bytes per op, up to 64 ops) and checks all four rules against the reference model.
func FuzzAdmissionOps(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	plant := fuzzPlant()
	f.Fuzz(func(t *testing.T, b []byte) {
		ops := decodeOps(b)
		for _, cfg := range fuzzConfigs(plant) {
			if w, err := runOps(cfg, ops); err != nil {
				t.Fatalf("rule %v: %v\n%s", cfg.Rule, err, trail(w))
			}
		}
	})
}

// TestFuzzDecodingIsTotalBoundedAndLocal proves decodeOps caps op count and virtual time, ignores trailing bytes, and confines a one-byte edit to one op.
func TestFuzzDecodingIsTotalBoundedAndLocal(t *testing.T) {
	long := make([]byte, 10_000)
	for i := range long {
		long[i] = byte(i * 7)
	}
	ops := decodeOps(long)
	if len(ops) != fuzzMaxOps {
		t.Fatalf("%d ops from a long input, cap %d", len(ops), fuzzMaxOps)
	}
	var virtual Time
	for _, o := range ops {
		virtual += o.dt
		if o.kind >= numOpKinds || o.dt < 0 || o.dt > 400*Millisecond {
			t.Fatalf("op out of range: %v", o)
		}
	}
	if virtual > fuzzMaxVirtual {
		t.Fatalf("virtual time %d above the bound %d", virtual, fuzzMaxVirtual)
	}
	a := decodeOps(long[:30])
	mut := append([]byte(nil), long[:30]...)
	mut[4]++
	b := decodeOps(mut)
	diff := 0
	for i := range a {
		if a[i] != b[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Fatalf("a one-byte change altered %d operations", diff)
	}
	if len(decodeOps([]byte{1, 2})) != 0 {
		t.Fatal("a partial operation decoded")
	}
}

// TestFuzzExecutionCostIsBounded proves worst-case fuzz inputs stay within bounded step count and queue memory.
func TestFuzzExecutionCostIsBounded(t *testing.T) {
	worst := make([]byte, fuzzMaxOps*fuzzOpBytes)
	for i := 0; i < len(worst); i += fuzzOpBytes {
		worst[i], worst[i+1], worst[i+2] = 0, byte(i), 255
	}
	ops := decodeOps(worst)
	for _, cfg := range fuzzConfigs(0) {
		w, err := runOps(cfg, ops)
		if err != nil {
			t.Fatal(err)
		}
		if len(w.log) > 4*fuzzMaxOps+64*4 {
			t.Fatalf("rule %v: %d steps for %d ops", cfg.Rule, len(w.log), len(ops))
		}
		if cfg.Capacity*16 > fuzzMaxQueueBytes {
			t.Fatal("fuzz queue too large")
		}
	}
}

// TestPlantedRemoveOrderBugIsCaughtByTheOperationModel proves PlantRemoveSwapsTail fails the reference model while the unplanted core passes.
func TestPlantedRemoveOrderBugIsCaughtByTheOperationModel(t *testing.T) {
	ops := decodeOps([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 5, 0, 0})
	for _, cfg := range fuzzConfigs(PlantRemoveSwapsTail) {
		if cfg.Rule != RuleFIFO {
			continue
		}
		cfg.LIFO = LIFOOff
		if _, err := runOps(cfg, ops); err == nil {
			t.Fatal("the planted remove-order bug passed the reference model")
		}
	}
	for _, cfg := range fuzzConfigs(0) {
		if _, err := runOps(cfg, ops); err != nil {
			t.Fatal(err)
		}
	}
}
