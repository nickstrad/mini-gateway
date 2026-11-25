package fakepg_test

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

func activity(t *testing.T) *world {
	cfg := fakepg.Config{MaxConnections: 8, Users: []string{"app"}, Databases: []string{"app"}, Capacity: 1,
		Service:  fakepg.Service{Fixed: time.Millisecond},
		WriteLog: fakepg.LogConfig{Cap: 1}, StatementLog: fakepg.LogConfig{Mode: fakepg.LogCountOnly}}
	w := newWorld(t, cfg)
	a, b := w.client(), w.client()
	w.exchange(a, "INSERT INTO kv VALUES ('a', '1')", "UPDATE kv SET v = '2' WHERE k = 'a'", "SET application_name = 'x'")
	w.exchange(a, "BEGIN", "UPDATE kv SET v = '3' WHERE k = 'a'", "COMMIT", "BEGIN", "ROLLBACK", "SELECT 1/0")
	a.Query("SELECT pg_sleep(0.5)")
	b.Query("SELECT 1")
	w.run(10 * time.Millisecond)
	c := w.dial()
	c.Cancel(a.Session().ProcessID(), a.Session().Secret())
	d := w.dial()
	d.Cancel(1, []byte{1, 1, 1, 1})
	w.run(100 * time.Millisecond)
	e := w.dial()
	e.Cancel(a.Session().ProcessID(), a.Session().Secret())
	w.exchange(b, "BEGIN")
	refusals := [][]testclient.Param{{{Name: "user", Value: "nobody"}}, {{Name: "user", Value: "app"}, {Name: "database", Value: "x"}}, {{Name: "database", Value: "app"}}}
	for _, ps := range refusals {
		p := w.dial()
		p.Send(testclient.EncodeStartup(nil, ps))
	}
	bad := w.dial()
	bad.Send([]byte{0, 0, 0, 9, 0, 3, 0, 0, 'x'})
	proto := w.dial()
	pkt := binary.BigEndian.AppendUint32(nil, 9)
	pkt = binary.BigEndian.AppendUint32(pkt, 4<<16)
	proto.Send(append(pkt, 0))
	w.run(10 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultMaxConn, Arg1: 2})
	extra := w.dial()
	extra.Startup("app", "")
	w.run(10 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultReset, Target: 999})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultParamStatus, Target: 0})
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultRestart, Arg2: int64(50 * time.Millisecond)})
	late := w.dial()
	late.Startup("app", "")
	w.run(10 * time.Millisecond)
	w.clear(fakepg.FaultMaxConn, 0)
	w.run(time.Second)
	w.closeAll()
	w.quiet()
	return w
}

// Proves fakepg.Snapshot is a plain value struct, obeys all conservation laws, and every non-gauge counter moves across activity and faults.
func TestSnapshotCountersMoveAndConserve(t *testing.T) {
	moved := map[string]bool{}
	w := activity(t)
	snap := w.srv.Snapshot()
	obstest.RequirePlain(t, snap)
	if err := snap.Check(); err != nil {
		t.Fatal(err)
	}
	obstest.MergeMoved(moved, snap)
	for _, fc := range faultCases() {
		fw := newWorld(t, fakepg.Config{Service: fakepg.Service{Fixed: 10 * time.Millisecond}})
		fc.steps(fw, fw.inject(fc.kind))
		fw.run(time.Second)
		s := fw.srv.Snapshot()
		if err := s.Check(); err != nil {
			t.Fatalf("%s: %v", fc.name, err)
		}
		obstest.MergeMoved(moved, s)
	}
	allowed := map[string]bool{"InService.Value": true, "Waiting.Value": true, "TxOpen": true, "ConnsOpen.Value": true, "Sessions.Value": true}
	for _, name := range obstest.NeverMoved(moved) {
		if !allowed[name] {
			t.Errorf("counter %s never moved", name)
		}
	}
	if snap.Refused.TooMany == 0 || snap.Refused.StartingUp == 0 || snap.Refused.Protocol == 0 || snap.Refused.BadStartup == 0 {
		t.Fatalf("refusals %+v", snap.Refused)
	}
}

// Proves running the same seeded activity twice produces byte-identical snapshots and simulator digests.
func TestSnapshotsAreIdenticalForOneSeed(t *testing.T) {
	a, b := activity(t), activity(t)
	if a.srv.Snapshot() != b.srv.Snapshot() || a.s.Digests() != b.s.Digests() {
		t.Fatal("same seed, different snapshot or digests")
	}
}
