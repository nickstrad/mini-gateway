package world_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

const (
	smallClients  = 8
	faultWindow   = sim.Time(300 * time.Millisecond)
	clearAt       = 400 * time.Millisecond
	scheduleBytes = 64

	fuzzInputBytes       = 128
	fuzzMaxBloatRowBytes = 256
	fuzzStepBudget       = 100_000
)

func smallWorld() world.Setup {
	t := func(name string) workload.Tenant {
		return workload.Tenant{Name: name, Clients: smallClients / 2, Requests: 10, Interval: 20 * time.Millisecond, Arrival: workload.ArrivalPoisson,
			Keys: workload.Keys{N: 5}, Mix: []workload.Weight{{Op: workload.OpRead, Weight: 2}, {Op: workload.OpWrite, Weight: 1}, {Op: workload.OpTx, Weight: 1}},
			TxStatements: 2, TxReads: 1, Deadline: 500 * time.Millisecond, Retry: workload.Retry{Max: 1, Backoff: 5 * time.Millisecond, OnConnect: true, OnLost: true}}
	}
	rows := []fakepg.Row{}
	for i := 0; i < 5; i++ {
		rows = append(rows, fakepg.Row{Key: workload.KeyName(i), Value: "v"})
	}
	return world.Setup{
		Fake:     fakepg.Config{MaxConnections: 20, Capacity: 4, Rows: rows, Service: fakepg.Service{Fixed: time.Millisecond, JitterMax: 2 * time.Millisecond}},
		Workload: workload.Config{Tenants: []workload.Tenant{t("a"), t("b")}},
	}
}

func faultSpace() sim.Space {
	kinds := append(fakepg.FaultKinds(), workload.FaultKinds()...)
	return sim.Space{Kinds: kinds, Targets: smallClients + 4, Start: 0, Window: faultWindow, MaxArg1: 4, MaxArg2: int64(200 * time.Millisecond), MaxFaults: 16}
}

func clearAll() []sim.Fault {
	at := sim.Time(clearAt)
	var out []sim.Fault
	for _, k := range fakepg.FaultKinds() {
		out = append(out, sim.Fault{At: at, Kind: fakepg.FaultClear, Arg1: int64(k)})
	}
	for id := int64(1); id <= smallClients; id++ {
		for _, k := range []sim.FaultKind{workload.FaultStopReading, workload.FaultHalfMessage, workload.FaultDisconnect} {
			out = append(out, sim.Fault{At: at, Kind: workload.FaultClear, Target: id, Arg1: int64(k)})
		}
	}
	return out
}

func runSchedule(seed int64, sched []sim.Fault, replay string) *world.Result {
	setup := smallWorld()
	setup.Faults = append(append([]sim.Fault(nil), sched...), clearAll()...)
	sc := world.Scenario{Name: "small-world-faults", Duration: 2 * time.Second, Setup: func(int64) world.Setup { return setup }}
	return world.RunWith(sc, seed, &world.SimDriver{Seed: seed, Scenario: sc.Name, Replay: replay})
}

func scheduleFor(seed int64) []sim.Fault {
	r := sim.NewStream(seed, "property/schedule")
	data := make([]byte, scheduleBytes)
	for i := range data {
		data[i] = byte(r.Uint32())
	}
	return sim.ScheduleFromBytes(data, faultSpace())
}

// Proves random fault schedules from ScheduleFromBytes never panic, always quiesce after clearing, obey conservation, and replay identically.
func TestScenarioRandomFaultSchedules(t *testing.T) {
	simtest.Sweep(t, func(t *testing.T, seed int64) {
		sched := scheduleFor(seed)
		var results []*world.Result
		simtest.RequireDeterministic(t, seed, func(seed int64) *sim.Sim {
			r := runSchedule(seed, sched, simtest.ReplayCommand(t, seed))
			results = append(results, r)
			return r.Sim
		})
		r := results[0]
		if r.Err != nil {
			t.Fatalf("schedule %v: %v", sched, r.Err)
		}
		if fmt.Sprintf("%+v", results[0].Fake) != fmt.Sprintf("%+v", results[1].Fake) {
			t.Fatal("fake snapshots differ between runs")
		}
		if err := r.Obs.Clients.Total.Check(); err != nil {
			t.Fatal(err)
		}
	})
}

// Proves arbitrary byte-decoded fault schedules run to clean quiesce without panics or invariant errors.
func FuzzWorldSchedule(f *testing.F) {
	f.Add([]byte{0, 1, 0, 10, 0, 0, 0, 100})
	f.Add([]byte{14, 0, 0, 50, 0, 0, 0, 0, 22, 3, 1, 0, 0, 2, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		sched := fuzzSchedule(data)
		r := runSchedule(1, sched, "")
		if r.Err != nil {
			t.Fatalf("schedule %v: %v", sched, r.Err)
		}
	})
}

func fuzzSchedule(data []byte) []sim.Fault {
	if len(data) > fuzzInputBytes {
		data = data[:fuzzInputBytes]
	}
	sched := sim.ScheduleFromBytes(data, faultSpace())
	for i := range sched {
		if sched[i].Kind == fakepg.FaultBloat {
			sched[i].Arg2 = min(sched[i].Arg2, fuzzMaxBloatRowBytes)
		}
	}
	return sched
}

func fuzzRecord(kind sim.FaultKind, target, at byte, arg1, arg2 uint16) []byte {
	k := byte(slices.Index(faultSpace().Kinds, kind))
	return []byte{k, target, 0, at, byte(arg1 >> 8), byte(arg1), byte(arg2 >> 8), byte(arg2)}
}

func TestFuzzWorldScheduleCostIsBounded(t *testing.T) {
	saved := []byte("\xfb\x01\x00\n\x00\x00\xff\xff\x06\x00\x00d")
	widest := slices.Concat(
		fuzzRecord(fakepg.FaultBloat, 0, 0, 4, 0xffff),
		fuzzRecord(fakepg.FaultFragment, 0, 0, 0, 1),
	)
	everyRecord := []byte{}
	for i := 0; i < fuzzInputBytes/8; i++ {
		if i%2 == 0 {
			everyRecord = append(everyRecord, fuzzRecord(fakepg.FaultBloat, byte(i), byte(i), 4, 0xffff)...)
		} else {
			everyRecord = append(everyRecord, fuzzRecord(fakepg.FaultFragment, byte(i%3), byte(i), 0, 0)...)
		}
	}
	for name, data := range map[string][]byte{"saved-7edb6a746e048b6e": saved, "bloat-and-one-byte-chunks-at-zero": widest, "every-record-bloat-or-fragment": everyRecord} {
		sched := fuzzSchedule(data)
		r := runSchedule(1, sched, "")
		if r.Err != nil {
			t.Fatalf("%s: %v", name, r.Err)
		}
		if steps := r.Sim.Snapshot().Steps; steps > fuzzStepBudget {
			t.Errorf("%s: one execution took %d simulator steps, budget %d (schedule %v)", name, steps, fuzzStepBudget, sched)
		}
	}
}
