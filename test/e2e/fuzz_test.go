package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

const plantEnv = "MG_FUZZ_PLANT"

func fuzzPlant() proxy.Plant {
	if os.Getenv(plantEnv) == "queued-vanish" {
		return proxy.PlantQueuedVanishKeepsSlot
	}
	return 0
}

func op(k sim.FaultKind, target, arg int64, gapMs int) sim.Fault {
	return sim.Fault{Kind: k, Target: target, Arg1: arg, At: sim.Time(time.Duration(gapMs) * time.Millisecond)}
}

func seedCorpus() [][]sim.Fault {
	connect := []sim.Fault{op(OpConnect, 0, 0, 0), op(OpConnect, 1, 0, 0), op(OpConnect, 2, 0, 5)}
	with := func(ops ...sim.Fault) []sim.Fault { return append(append([]sim.Fault(nil), connect...), ops...) }
	return [][]sim.Fault{
		with(op(OpRead, 0, 0, 2), op(OpWrite, 1, 0, 2), op(OpRead, 2, 0, 2)),
		with(op(OpBegin, 0, 0, 2), op(OpWrite, 0, 0, 2), op(OpCommit, 0, 0, 2), op(OpRollback, 1, 0, 2)),
		with(op(OpReject, 0, 0, 2), op(OpExtended, 1, 0, 2), op(OpRead, 1, 0, 2)),
		with(op(OpBigRead, 0, 0, 1), op(OpStopReading, 0, 0, 0), op(OpAdvance, 0, 20, 0), op(OpResumeReading, 0, 0, 0)),
		with(op(OpSlowRead, 0, 0, 1), op(OpVanish, 0, 0, 1), op(OpRead, 1, 0, 2)),
		with(op(OpDBFragment, 0, 0, 0), op(OpRead, 0, 0, 2), op(OpDBReset, 0, 1, 0), op(OpRead, 1, 0, 2)),
		with(op(OpBadReset, 0, 2, 0), op(OpRead, 0, 0, 2), op(OpAdvance, 0, 10, 0), op(OpRead, 1, 0, 2)),
		with(op(OpCancel, 0, 0, 0), op(OpDBFirstByte, 0, 30, 0), op(OpRead, 2, 0, 1), op(OpDisconnect, 2, 0, 0)),
	}
}

func FuzzEngineOps(f *testing.F) {
	for _, ops := range seedCorpus() {
		f.Add(EncodeOps(ops))
	}
	plant := fuzzPlant()
	f.Fuzz(func(t *testing.T, b []byte) {
		r := RunOps(DecodeOps(b), plant)
		if r.Failed() {
			t.Fatalf("%s\nops: %v", r, DecodeOps(b))
		}
	})
}

func TestOpDecodingIsTotalBoundedAndLocal(t *testing.T) {
	for n := 0; n < 3*opBytes*MaxOps; n += 7 {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*31 + n)
		}
		ops := DecodeOps(b)
		if len(ops) > MaxOps || len(ops) != min(n/opBytes, MaxOps) {
			t.Fatalf("%d bytes decoded to %d ops", n, len(ops))
		}
		for i := range b {
			c := append([]byte(nil), b...)
			c[i]++
			other := DecodeOps(c)
			diff := 0
			for j := range ops {
				if ops[j] != other[j] {
					diff++
				}
			}
			if diff > 1 {
				t.Fatalf("a one-byte change altered %d operations", diff)
			}
		}
	}
}

func TestFuzzEngineOpsCostIsBounded(t *testing.T) {
	var worst []sim.Fault
	for i := 0; i < MaxOps; i++ {
		k := OpAdvance
		if i%3 == 0 {
			k = OpSlowRead
		}
		worst = append(worst, op(k, int64(i%OpClients), maxAdvanceMs-1, maxGapMs-1))
	}
	worst = append(seedCorpus()[0][:3:3], worst...)[:MaxOps]
	start := time.Now()
	r := RunOps(worst, 0)
	if el := time.Since(start); el > time.Second {
		t.Fatalf("one execution took %s (%d steps)", el, r.Steps)
	}
	if r.Failed() {
		t.Fatal(r)
	}
}

func TestSeedCorpusOpsPassAndMoveCounters(t *testing.T) {
	for i, ops := range seedCorpus() {
		r := RunOps(ops, 0)
		if r.Failed() {
			t.Fatalf("corpus %d: %s", i, r)
		}
		if r.Engine.Completed == 0 {
			t.Fatalf("corpus %d completed nothing", i)
		}
	}
}
