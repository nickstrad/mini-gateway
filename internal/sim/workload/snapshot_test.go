package workload_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

func busyWorld(t *testing.T) *world {
	rows := []fakepg.Row{{Key: "k0", Value: "v"}, {Key: "k1", Value: "v"}}
	tenants := []workload.Tenant{
		{Name: "mix", Clients: 3, Requests: 10, Interval: 5 * time.Millisecond, Keys: workload.Keys{N: 2},
			Mix:          []workload.Weight{{Op: workload.OpRead, Weight: 1}, {Op: workload.OpWrite, Weight: 1}, {Op: workload.OpTx, Weight: 1}},
			TxStatements: 2, TxReads: 1, Disconnect: []workload.Disconnect{{Request: 2, Point: workload.DisconnectAfterSend}}},
		{Name: "errors", Clients: 1, Requests: 3, Interval: 5 * time.Millisecond, Statement: "SELECT 1/0", Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k0"}},
		{Name: "slow", Clients: 2, Requests: 4, Interval: time.Millisecond, Statement: "SELECT pg_sleep(0.2)", Backlog: 2, Deadline: 100 * time.Millisecond,
			Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k0"}, Retry: workload.Retry{Max: 1, Backoff: time.Millisecond, OnTimeout: true}},
		{Name: "faulty", Clients: 4, Requests: 5, Interval: 20 * time.Millisecond, Keys: workload.Keys{N: 2}, ConnectFirst: true, Statement: "SELECT pg_sleep(0.01)"},
		{Name: "late", Clients: 2, Hold: 10 * time.Millisecond, Retry: workload.Retry{Max: 2, Backoff: time.Millisecond, OnConnect: true}},
		{Name: "refused", Clients: 1, Requests: 1, Start: 55 * time.Millisecond, Keys: workload.Keys{N: 2}},
	}
	s := sim.New(sim.Config{Seed: 5, Scenario: t.Name()})
	w := buildOn(t, s, fakepg.Config{Rows: rows, MaxConnections: 12}, workload.Config{Tenants: tenants})
	faulty := int64(3 + 1 + 2 + 1)
	fault(s, 15*time.Millisecond, workload.FaultHalfMessage, faulty, 0, 0)
	fault(s, 15*time.Millisecond, workload.FaultStopReading, faulty+1, 0, int64(30*time.Millisecond))
	fault(s, 30*time.Millisecond, workload.FaultCancel, faulty+2, 0, 0)
	fault(s, 45*time.Millisecond, workload.FaultDisconnect, faulty+3, int64(workload.DisconnectNow), 1)
	fault(s, 50*time.Millisecond, workload.FaultBurst, faulty+4, 2, int64(5*time.Millisecond))
	fault(s, 50*time.Millisecond, workload.FaultCancel, 999, 0, 0)
	s.InjectAt(sim.Fault{At: sim.Time(49 * time.Millisecond), Kind: fakepg.FaultMaxConn, Arg1: 0})
	s.InjectAt(sim.Fault{At: sim.Time(58 * time.Millisecond), Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultMaxConn)})

	s.InjectAt(sim.Fault{At: sim.Time(60 * time.Millisecond), Kind: fakepg.FaultReset, Arg1: int64(fakepg.PointBefore)})
	s.InjectAt(sim.Fault{At: sim.Time(70 * time.Millisecond), Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultReset)})
	w.finish(t)
	return w
}

// Proves workload.Snapshot is plain, obeys per-tenant and total conservation laws, and moves every non-gauge counter.
func TestWorkloadSnapshotCountersMoveAndConserve(t *testing.T) {
	w := busyWorld(t)
	snap := w.wl.Snapshot()
	obstest.RequirePlain(t, snap)
	for _, ts := range append(snap.Tenants, snap.Total) {
		if err := ts.Check(); err != nil {
			t.Fatalf("%s: %v", ts.Name, err)
		}
	}
	moved := map[string]bool{}
	obstest.MergeMoved(moved, snap.Total)
	obstest.MergeMoved(moved, snap.Faults)
	allowed := map[string]bool{"NotYetSent.Value": true, "InFlight.Value": true, "ConnsOpen.Value": true, "ErrorsOtherCodes": true,
		"Latency.ClampedLow": true, "Latency.ClampedHigh": true, "LatencyFromSend.ClampedLow": true, "LatencyFromSend.ClampedHigh": true}
	for _, name := range obstest.NeverMoved(moved) {
		if !allowed[name] && !strings.HasPrefix(name, "Startup.") {
			t.Errorf("counter %s never moved", name)
		}
	}
	for i, n := range snap.Faults.ByKind[:5] {
		if n == 0 {
			t.Errorf("fault %s never applied", workload.FaultName(workload.FaultKinds()[i]))
		}
	}
	if snap.Finished != snap.Clients {
		t.Fatalf("%d of %d clients finished", snap.Finished, snap.Clients)
	}
}

// Proves running the same seeded multi-tenant faulted workload twice produces identical snapshots and digests.
func TestWorkloadSnapshotsAreIdenticalForOneSeed(t *testing.T) {
	a, b := busyWorld(t), busyWorld(t)
	if sa, sb := sprint(a.wl.Snapshot()), sprint(b.wl.Snapshot()); sa != sb {
		t.Fatalf("same seed, different snapshots\n%s\n%s", sa, sb)
	}
	if a.s.Digests() != b.s.Digests() {
		t.Fatal("same seed, different digests")
	}
}
