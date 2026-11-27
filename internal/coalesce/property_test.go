package coalesce

import (
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
)

const (
	propertySeeds = 400
	propertyOps   = 90
	plantSeeds    = 2000
)

func randomInput(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 0x636f616c))
	b := make([]byte, n*opBytes)
	for i := range b {
		b[i] = byte(r.IntN(256))
	}
	for i := 0; i < len(b); i += opBytes {
		switch r.IntN(10) {
		case 0, 1, 2, 3:
			b[i] = byte(opJoin)
		case 4:
			b[i] = byte(opAdvance)
			b[i+1] = byte(r.IntN(40))
		}
	}
	return b
}

func TestFlightTableAgreesWithReferenceModel(t *testing.T) {
	seeds := propertySeeds
	if testing.Short() {
		seeds = 100
	}
	moved := map[string]int64{}
	for seed := range seeds {
		ops := decodeOps(randomInput(uint64(seed), propertyOps))
		for _, cfg := range testConfigs(0) {
			w, err := runOps(cfg, ops)
			if err != nil {
				t.Fatalf("seed %d mode %v join %v: %v\n%s", seed, cfg.Mode, cfg.Join, err, trail(w))
			}
			s := w.t.Snapshot()
			moved["hits"] += s.Hits
			moved["held"] += s.Held
			moved["handoffs"] += s.Handoffs
			moved["refused"] += s.Refused
			moved["detached"] += s.Detached
			moved["expired"] += s.Expired
			moved["cancelled"] += s.Cancelled
			moved["promoted"] += s.Promoted
			moved["no-share"] += s.PassedNoShare
			moved["full"] += s.PassedFull
			moved["own-error"] += s.WaiterOwnError
			moved["gone"] += s.WaiterGone
		}
	}
	for k, v := range moved {
		if v == 0 {
			t.Errorf("counter %s never moved over %d seeds: the generator does not reach it", k, seeds)
		}
	}
}

func plantFound(plant Plant, seeds int) (int, error) {
	for seed := range seeds {
		ops := decodeOps(randomInput(uint64(seed), propertyOps))
		for _, cfg := range testConfigs(plant) {
			if _, err := runOps(cfg, ops); err != nil {
				return seed, err
			}
		}
	}
	return -1, nil
}

func TestPlantedBugsAreCaughtByThePropertyTest(t *testing.T) {
	for _, p := range []struct {
		name  string
		plant Plant
	}{
		{"strict-joins-running", PlantStrictJoinsRunning},
		{"any-leave-cancels", PlantAnyLeaveCancels},
		{"leaver-gets-result", PlantLeaverGetsResult},
		{"no-waiter-cap", PlantNoWaiterCap},
		{"no-reply-cap", PlantNoReplyCap},
		{"safe-read-door", PlantSafeReadDoor},
	} {
		t.Run(p.name, func(t *testing.T) {
			seed, err := plantFound(p.plant, plantSeeds)
			if err == nil {
				t.Fatalf("planted %s survived %d seeds of the property test", p.name, plantSeeds)
			}
			t.Logf("found at seed %d: %v", seed, err)
		})
	}
}

func TestReplayIsBitIdentical(t *testing.T) {
	for seed := range 50 {
		ops := decodeOps(randomInput(uint64(seed), propertyOps))
		for _, cfg := range testConfigs(0) {
			a, errA := runOps(cfg, ops)
			b, errB := runOps(cfg, ops)
			if errA != nil || errB != nil {
				t.Fatalf("seed %d: %v / %v", seed, errA, errB)
			}
			sa, sb := a.t.Snapshot(), b.t.Snapshot()
			if !snapshotsEqual(sa, sb) {
				t.Fatalf("seed %d: snapshots differ between two runs", seed)
			}
		}
	}
}

func snapshotsEqual(a, b Snapshot) bool {
	if len(a.PerTenant) != len(b.PerTenant) || len(a.HotKeys) != len(b.HotKeys) ||
		len(a.WaitersPerFlight.Buckets) != len(b.WaitersPerFlight.Buckets) {
		return false
	}
	for i := range a.PerTenant {
		if a.PerTenant[i] != b.PerTenant[i] {
			return false
		}
	}
	for i := range a.HotKeys {
		if a.HotKeys[i] != b.HotKeys[i] {
			return false
		}
	}
	for i := range a.WaitersPerFlight.Buckets {
		if a.WaitersPerFlight.Buckets[i] != b.WaitersPerFlight.Buckets[i] {
			return false
		}
	}
	return reflect.DeepEqual(a, b)
}

const (
	plantEnv           = "MG_FUZZ_PLANT"
	plantLeaverResult  = "coalesce-leaver-result"
	fuzzMaxVirtualTime = fuzzMaxOps * maxAdvanceMs * 4 * Millisecond
)

func fuzzPlant() Plant {
	if os.Getenv(plantEnv) == plantLeaverResult {
		return PlantLeaverGetsResult
	}
	return 0
}

func fuzzSeeds() [][]byte {
	return [][]byte{
		{},
		{0, 0, 0, 0, 0, 0, 2, 0, 0, 4, 0, 0},
		{0, 1, 0, 0, 1, 0, 2, 0, 0, 0, 1, 0, 1, 1, 0, 4, 0, 0},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 0, 200},
		{0, 0, 0, 5, 200, 0, 0, 0, 0, 6, 0, 0},
	}
}

func FuzzCoalesceOps(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	plant := fuzzPlant()
	f.Fuzz(func(t *testing.T, b []byte) {
		ops := decodeOps(b)
		for _, cfg := range testConfigs(plant) {
			if w, err := runOps(cfg, ops); err != nil {
				t.Fatalf("mode %v join %v: %v\n%s", cfg.Mode, cfg.Join, err, trail(w))
			}
		}
	})
}

func TestFuzzDecodingIsTotalBoundedAndLocal(t *testing.T) {
	long := make([]byte, 10_000)
	for i := range long {
		long[i] = byte(i * 7)
	}
	ops := decodeOps(long)
	if len(ops) != fuzzMaxOps {
		t.Fatalf("%d ops from a long input, cap %d", len(ops), fuzzMaxOps)
	}
	var virtual Instant
	for _, o := range ops {
		if o.kind >= numOpKinds {
			t.Fatalf("op out of range: %v", o)
		}
		if o.kind == opAdvance {
			virtual += Instant(o.a) * 4 * Millisecond
		}
	}
	if virtual > fuzzMaxVirtualTime {
		t.Fatalf("virtual time %d above the bound %d", virtual, fuzzMaxVirtualTime)
	}
	for _, n := range []int{0, 1, 2, 4, 5} {
		if got := decodeOps(long[:n]); len(got) != n/opBytes {
			t.Fatalf("%d bytes decoded to %d ops", n, len(got))
		}
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
		t.Fatalf("a one-byte change altered %d operations, want 1", diff)
	}
}

func TestFuzzExecutionCostIsBounded(t *testing.T) {
	worst := make([]byte, fuzzMaxOps*opBytes)
	for i := 0; i < len(worst); i += opBytes {
		worst[i] = byte(opJoin)
	}
	for _, cfg := range testConfigs(0) {
		w, err := runOps(cfg, decodeOps(worst))
		if err != nil {
			t.Fatal(err)
		}
		if len(w.trail) > fuzzMaxOps {
			t.Fatalf("%d operations ran, cap %d", len(w.trail), fuzzMaxOps)
		}
		if s := w.t.Snapshot(); s.Offered > fuzzMaxOps || s.Flights.Peak > int64(2*cfg.MaxFlights) {
			t.Fatalf("a bounded input grew the table past its caps: %+v", s)
		}
	}
}
