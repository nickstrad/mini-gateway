package world_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const c02Seed int64 = 81173

const c02Ms = time.Millisecond

type c02Rig struct {
	t  *testing.T
	d  *world.SimDriver
	ps []*workload.Probe
}

func c02NewRig(t *testing.T, c fakepg.Config) *c02Rig {
	t.Helper()
	return c02NewRigSeed(t, c, c02Seed)
}

func c02NewRigSeed(t *testing.T, c fakepg.Config, runSeed int64) *c02Rig {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./internal/sim/world", runSeed, t.Name())
	d := &world.SimDriver{Seed: runSeed, Scenario: t.Name()}
	if e := d.Start(world.Setup{Fake: c}); e != nil {
		t.Fatal(e)
	}
	r := &c02Rig{t: t, d: d}
	t.Cleanup(func() {
		for _, p := range r.ps {
			p.Reset()
		}
		if e := d.S.RunUntilQuiet(); e != nil {
			t.Error(e)
		}
		if e := d.S.CheckQuiesce(); e != nil {
			t.Error(e)
		}
	})
	return r
}

func (r *c02Rig) run(d time.Duration) {
	r.t.Helper()
	if e := r.d.Run(d); e != nil {
		r.t.Fatal(e)
	}
}

func (r *c02Rig) p() *workload.Probe {
	p := workload.NewProbe(r.d.Net, r.d.Addr(), simnet.Options{})
	r.ps = append(r.ps, p)
	p.Startup("postgres", "postgres")
	r.run(c02Ms)
	return p
}

func (r *c02Rig) q(p *workload.Probe, q string) []testclient.Msg {
	r.t.Helper()
	n := len(p.Messages())
	p.Query(q)
	r.run(100 * c02Ms)
	return p.Messages()[n:]
}

func c02Shape(mm []testclient.Msg) string {
	var b strings.Builder
	for _, m := range mm {
		b.WriteByte(m.Type)
		if m.Type == 'Z' {
			b.WriteByte(m.Status)
		}
	}
	return b.String()
}

func c02Status(t *testing.T, mm []testclient.Msg, w byte) {
	t.Helper()
	if len(mm) == 0 || mm[len(mm)-1].Type != 'Z' || mm[len(mm)-1].Status != w {
		t.Fatalf("wanted Ready %c, observed %s", w, c02Shape(mm))
	}
}

func c02Value(t *testing.T, mm []testclient.Msg, w string) {
	t.Helper()
	for _, m := range mm {
		if m.Type == 'D' && len(m.Values) > 0 && string(m.Values[0]) == w {
			return
		}
	}
	t.Fatalf("wanted row %q observed %+v", w, mm)
}

func c02HasCode(mm []testclient.Msg, c string) bool {
	for _, m := range mm {
		if m.Code == c {
			return true
		}
	}
	return false
}

func c02CheckShape(t *testing.T, q, w string, live bool) {
	t.Helper()
	r := c02NewRig(t, fakepg.Config{})
	p := r.p()
	got := c02Shape(r.q(p, q))
	if got != w {
		t.Errorf("query %q expected %s observed %s", q, w, got)
	}
	if live {
		c02LiveCompare(t, q, got)
	}
}

func TestAdv_1_EmptyInsideTransaction(t *testing.T) {
	c02CheckShape(t, "BEGIN; /*only*/ ; SELECT 'semicolon;inside'; COMMIT", "CTDCCZI", false)
}

func TestTierRAdvC02_1_EmptyInsideTransaction(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	c02CheckShape(t, "BEGIN; /*only*/ ; SELECT 'semicolon;inside'; COMMIT", "CTDCCZI", true)
}

func TestAdv_1_ErrorAfterCommitBoundary(t *testing.T) {
	c02CheckShape(t, "BEGIN; SELECT 81; COMMIT; SELECT 7 / 0", "CTDCCEZI", false)
}

func TestTierRAdvC02_1_ErrorAfterCommitBoundary(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	c02CheckShape(t, "BEGIN; SELECT 81; COMMIT; SELECT 7 / 0", "CTDCCEZI", true)
}

func TestAdv_1_RollbackThenBegin(t *testing.T) {
	c02CheckShape(t, "BEGIN; ROLLBACK; BEGIN; SELECT 19", "CCCTDCZT", false)
}

func TestTierRAdvC02_1_RollbackThenBegin(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	c02CheckShape(t, "BEGIN; ROLLBACK; BEGIN; SELECT 19", "CCCTDCZT", true)
}

func TestAdv_2_AbortedTransactionEmptyQuery(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{})
	p := r.p()
	c02Status(t, r.q(p, "BEGIN; SELECT 8 / 0"), 'E')
	c02Status(t, r.q(p, " ; /*empty*/ ;"), 'E')
	c02Status(t, r.q(p, "COMMIT; BEGIN"), 'T')
	c02Status(t, r.q(p, "ROLLBACK"), 'I')
}

func TestAdv_2_ImplicitRollbackOfDeleteInsert(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{Rows: []fakepg.Row{{Key: "x", Value: "old"}}})
	p := r.p()
	c02Status(t, r.q(p, "DELETE FROM kv WHERE k = 'x'; INSERT INTO kv VALUES ('x','new'); SELECT 91 / 0"), 'I')
	v, _ := r.d.Fake.Get("x")
	if v != "old" || r.d.Fake.Snapshot().WritesApplied != 0 {
		t.Fatalf("rollback leaked writes: value=%q stats=%+v", v, r.d.Fake.Snapshot())
	}
}

func TestAdv_2_SnapshotAcrossDeleteReinsert(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{Rows: []fakepg.Row{{Key: "x", Value: "old"}}})
	p, q := r.p(), r.p()
	n := len(p.Messages())
	p.Query("SELECT v, pg_sleep(0.4) IS NOT NULL FROM kv WHERE k = 'x'")
	r.run(c02Ms)
	r.q(q, "DELETE FROM kv WHERE k = 'x'; INSERT INTO kv VALUES ('x','new')")
	r.run(400 * c02Ms)
	c02Value(t, p.Messages()[n:], "old")
	c02Value(t, r.q(p, "SELECT v FROM kv WHERE k = 'x'"), "new")
}

func TestAdv_3_FullTableCommitMustNotLogPhantom(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{MaxRows: 1})
	p := r.p()
	r.q(p, "BEGIN; INSERT INTO kv VALUES ('a','1'); INSERT INTO kv VALUES ('b','2'); COMMIT")
	for _, w := range r.d.Fake.WriteLog() {
		v, ok := r.d.Fake.Get(w.Key)
		if !ok || v != w.Value {
			t.Fatalf("log claims applied write %+v but table=%+v", w, r.d.Fake.Rows())
		}
	}
}

func TestAdv_3_RingWrapPreservesSequence(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{WriteLog: fakepg.LogConfig{Cap: 3}})
	p := r.p()
	for i := 0; i < 11; i++ {
		r.q(p, fmt.Sprintf("INSERT INTO kv VALUES ('k%d','v')", i))
	}
	ll := r.d.Fake.WriteLog()
	if len(ll) != 3 || r.d.Fake.Snapshot().WriteLogDropped != 8 {
		t.Fatal(ll, r.d.Fake.Snapshot())
	}
	for i, w := range ll {
		if w.Seq != int64(i+9) || w.Key != fmt.Sprintf("k%d", i+8) {
			t.Fatalf("bad ring chronology: %+v", ll)
		}
	}
}

func TestAdv_3_DeleteReinsertCommitOrder(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{})
	p := r.p()
	r.q(p, "BEGIN; INSERT INTO kv VALUES ('a','1'); DELETE FROM kv WHERE k = 'a'; INSERT INTO kv VALUES ('a','2'); COMMIT")
	ll := r.d.Fake.WriteLog()
	if len(ll) != 3 {
		t.Fatalf("expected three applied writes: %+v", ll)
	}
	for i, w := range ll {
		if w.Seq != int64(i+1) || w.TxID != ll[0].TxID || w.StmtSeq <= 0 {
			t.Fatalf("bad exact log: %+v", ll)
		}
	}
	v, _ := r.d.Fake.Get("a")
	if v != "2" {
		t.Fatal(v)
	}
}

func TestAdv_4_StartupReservations(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{MaxConnections: 1})
	a := workload.NewProbe(r.d.Net, r.d.Addr(), simnet.Options{})
	r.ps = append(r.ps, a)
	r.run(c02Ms)
	b := r.p()
	if !c02HasCode(b.Messages(), "53300") {
		t.Fatalf("half startup failed to reserve slot: %s", b.Trace())
	}
	a.Reset()
	r.run(c02Ms)
	c := r.p()
	c02Status(t, c.Messages(), 'I')
}

func TestAdv_4_CancelAtFullCapacity(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{MaxConnections: 1})
	p := r.p()
	p.Query("SELECT pg_sleep(1)")
	r.run(c02Ms)
	r.cancel(p)
	r.run(c02Ms)
	if !c02HasCode(p.Messages(), "57014") || r.d.Fake.Snapshot().Sessions.Peak != 1 {
		t.Fatalf("full cap blocked cancel: %s %+v", p.Trace(), r.d.Fake.Snapshot())
	}
}

func TestAdv_4_DeadEndsDoNotStealReleasedSlot(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{MaxConnections: 1})
	a := r.p()
	for i := 0; i < 13; i++ {
		p := r.p()
		if !c02HasCode(p.Messages(), "53300") {
			t.Fatal(p.Trace())
		}
	}
	a.Reset()
	r.run(c02Ms)
	b := r.p()
	c02Status(t, b.Messages(), 'I')
	if r.d.Fake.Snapshot().Sessions.Peak != 1 {
		t.Fatal(r.d.Fake.Snapshot())
	}
}

func (r *c02Rig) fault(k sim.FaultKind, target, a, b int64) {
	r.t.Helper()
	before := r.d.Fake.Snapshot()
	r.d.S.ApplyFault(sim.Fault{Kind: k, Target: target, Arg1: a, Arg2: b})
	traced := false
	for _, rec := range r.d.S.TraceTail(32) {
		if rec.Kind == sim.KindFault && rec.A == int64(k) && rec.B == target && rec.C == a && rec.D == b {
			traced = true
		}
	}
	if !traced {
		r.t.Fatalf("fault %d not in trace", k)
	}
	if k >= fakepg.FaultRefuse && k < fakepg.FaultClear && r.d.Fake.Snapshot().Fault(k) != before.Fault(k)+1 {
		r.t.Fatalf("fault %d not counted", k)
	}
	if e := r.d.S.Err(); e != nil {
		r.t.Fatal(e)
	}
}

func TestAdv_5_StallHitsAlreadyServing(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{Service: fakepg.Service{Fixed: 100 * c02Ms}})
	p := r.p()
	n := len(p.Messages())
	p.Query("SELECT 28")
	r.run(20 * c02Ms)
	r.fault(fakepg.FaultStallWindow, 0, int64(200*c02Ms), int64(150*c02Ms))
	r.run(100 * c02Ms)
	for _, m := range p.Messages()[n:] {
		if m.Type == 'Z' {
			t.Fatalf("in-service statement escaped 150ms stall: %s", p.Trace())
		}
	}
	r.fault(fakepg.FaultClear, 0, int64(fakepg.FaultStallWindow), 0)
	r.run(300 * c02Ms)
	c02Status(t, p.Messages(), 'I')
}

func TestAdv_5_FreezeClearWithQueuedBackends(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{Capacity: 1, Service: fakepg.Service{Fixed: 30 * c02Ms}})
	p, q := r.p(), r.p()
	p.Query("SELECT 31")
	q.Query("SELECT 32")
	r.run(c02Ms)
	r.fault(fakepg.FaultFreeze, 0, 0, 0)
	r.run(80 * c02Ms)
	if r.d.Fake.Snapshot().StatementsFinished != 0 {
		t.Fatal("frozen service completed")
	}
	r.fault(fakepg.FaultClear, 0, int64(fakepg.FaultFreeze), 0)
	r.run(100 * c02Ms)
	c02Status(t, p.Messages(), 'I')
	c02Status(t, q.Messages(), 'I')
	if r.d.Fake.Snapshot().StatementsFinished != 2 {
		t.Fatal(r.d.Fake.Snapshot())
	}
}

func TestAdv_5_RefuseRestartClearInterleaving(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{})
	r.fault(fakepg.FaultRefuse, 0, 0, 0)
	r.fault(fakepg.FaultRestart, 0, 0, 0)
	r.fault(fakepg.FaultClear, 0, int64(fakepg.FaultRefuse), 0)
	p := r.p()
	if !c02HasCode(p.Messages(), "57P03") {
		t.Fatal(p.Trace())
	}
	r.fault(fakepg.FaultClear, 0, int64(fakepg.FaultRestart), 0)
	q := r.p()
	c02Status(t, r.q(q, "SELECT 44"), 'I')
	for _, k := range []sim.FaultKind{fakepg.FaultRefuse, fakepg.FaultRestart} {
		if r.d.Fake.Snapshot().Fault(k) != 1 {
			t.Fatal(r.d.Fake.Snapshot())
		}
	}
}

func c02StartLoad(t *testing.T, c fakepg.Config, ten workload.Tenant) *c02Rig {
	r := c02NewRig(t, c)
	if e := r.d.Connect(workload.Config{Tenants: []workload.Tenant{ten}}); e != nil {
		t.Fatal(e)
	}
	return r
}

func c02Tenant() workload.Tenant {
	return workload.Tenant{Name: "a", Clients: 1, Requests: 3, Start: 10 * c02Ms, Interval: 10 * c02Ms, Statement: "SELECT 65", ConnectFirst: true}
}

func TestAdv_6_ClearArmedHalfMessage(t *testing.T) {
	r := c02StartLoad(t, fakepg.Config{}, c02Tenant())
	r.run(c02Ms)
	r.fault(workload.FaultHalfMessage, 1, 0, 0)
	r.fault(workload.FaultClear, 1, int64(workload.FaultHalfMessage), 0)
	r.run(time.Second)
	s := r.d.Observe().Clients.Total
	if s.OK != 3 || s.HalfMessages != 0 {
		t.Fatalf("clear failed: %+v", s)
	}
}

func TestAdv_6_ResetQueuedClientReleasesWaiter(t *testing.T) {
	tt := c02Tenant()
	tt.Clients = 3
	tt.Requests = 1
	r := c02StartLoad(t, fakepg.Config{Capacity: 1, Service: fakepg.Service{Fixed: 200 * c02Ms}}, tt)
	r.run(30 * c02Ms)
	r.fault(workload.FaultDisconnect, 2, int64(workload.DisconnectNow), 1)
	r.run(time.Second)
	s := r.d.Fake.Snapshot()
	if s.StatementsAborted != 1 || s.Waiting.Value != 0 || r.d.Observe().Clients.Total.OK != 2 {
		t.Fatalf("queued reset leaked: %+v clients=%+v", s, r.d.Observe().Clients.Total)
	}
}

func TestAdv_6_StopReadThenCancelThenClear(t *testing.T) {
	tt := c02Tenant()
	tt.Requests = 1
	tt.Statement = "SELECT pg_sleep(0.2)"
	r := c02StartLoad(t, fakepg.Config{}, tt)
	r.run(20 * c02Ms)
	r.fault(workload.FaultStopReading, 1, 0, 0)
	r.fault(workload.FaultCancel, 1, 0, 0)
	r.run(20 * c02Ms)
	if r.d.Observe().Clients.Total.Errors != 0 {
		t.Fatal("stopped reader consumed error")
	}
	r.fault(workload.FaultClear, 1, int64(workload.FaultStopReading), 0)
	r.run(time.Second)
	if r.d.Observe().Clients.Total.Code("57014") != 1 {
		t.Fatal(r.d.Observe().Clients.Total)
	}
}

func TestAdv_7_LatencyIncludesStartupWait(t *testing.T) {
	tt := c02Tenant()
	tt.Requests = 1
	r := c02StartLoad(t, fakepg.Config{}, tt)
	r.fault(fakepg.FaultFreeze, 0, 0, 0)
	r.run(500 * c02Ms)
	r.fault(fakepg.FaultClear, 0, int64(fakepg.FaultFreeze), 0)
	r.run(time.Second)
	s := r.d.Observe().Clients.Total
	if s.OK != 1 || s.Latency.Min < int64(490*c02Ms) {
		t.Fatalf("startup delay omitted: %+v", s)
	}
}

func TestAdv_7_BacklogCapCountsEveryArrival(t *testing.T) {
	tt := c02Tenant()
	tt.Requests = 19
	tt.Backlog = 1
	tt.Interval = c02Ms
	r := c02StartLoad(t, fakepg.Config{Service: fakepg.Service{Fixed: 100 * c02Ms}}, tt)
	r.run(30 * c02Ms)
	s := r.d.Observe().Clients.Total
	if s.Intended != 19 || s.Sent+s.NotYetSent.Value+s.Dropped != 19 || s.Dropped == 0 {
		t.Fatal(s)
	}
	r.run(time.Second)
	if e := r.d.Observe().Clients.Total.Check(); e != nil {
		t.Fatal(e)
	}
}

func (r *c02Rig) cancel(p *workload.Probe) {
	c := workload.NewProbe(r.d.Net, r.d.Addr(), simnet.Options{})
	r.ps = append(r.ps, c)
	c.Cancel(p.Session().ProcessID(), p.Session().Secret())
}

func TestAdv_8_RepeatCancelDoesNotPoisonNextStatement(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{})
	p := r.p()
	p.Query("SELECT pg_sleep(0.2)")
	r.run(c02Ms)
	r.cancel(p)
	r.cancel(p)
	r.run(c02Ms)
	if r.d.Fake.Snapshot().CancelsApplied != 1 || r.d.Fake.Snapshot().CancelsIdle != 1 {
		t.Fatal(r.d.Fake.Snapshot())
	}
	mm := r.q(p, "SELECT 77")
	if c02HasCode(mm, "57014") {
		t.Fatal(mm)
	}
	c02Status(t, mm, 'I')
}

func TestAdv_8_CancelQueuedBackendOnly(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{Capacity: 1})
	p, q := r.p(), r.p()
	p.Query("SELECT pg_sleep(0.2)")
	r.run(c02Ms)
	q.Query("SELECT 88")
	r.run(c02Ms)
	r.cancel(q)
	r.run(300 * c02Ms)
	if !c02HasCode(q.Messages(), "57014") || c02HasCode(p.Messages(), "57014") {
		t.Fatalf("cancel spillover p=%s q=%s", p.Trace(), q.Trace())
	}
	c02Status(t, p.Messages(), 'I')
}

func TestAdv_8_OldKeyAfterBackendReplacement(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{MaxConnections: 1})
	p := r.p()
	pid, secret := p.Session().ProcessID(), p.Session().Secret()
	p.Reset()
	r.run(c02Ms)
	q := r.p()
	q.Query("SELECT pg_sleep(0.2)")
	r.run(c02Ms)
	c := workload.NewProbe(r.d.Net, r.d.Addr(), simnet.Options{})
	r.ps = append(r.ps, c)
	c.Cancel(pid, secret)
	r.run(300 * c02Ms)
	if c02HasCode(q.Messages(), "57014") || r.d.Fake.Snapshot().CancelsNoMatch != 1 {
		t.Fatal(q.Trace(), r.d.Fake.Snapshot())
	}
}

func TestAdv_9_ExtremeSeedBaselineReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs without -short")
	}
	for _, s := range []int64{-1, 0, 9223372036854775807} {
		for _, sc := range world.Scenarios() {
			t.Logf("seed=%d scenario=%s", s, sc.Name)
			a, b := world.Run(sc, s), world.Run(sc, s)
			if e := sc.Verify(a); e != nil {
				t.Fatal(e)
			}
			if e := sc.Verify(b); e != nil {
				t.Fatal(e)
			}
			if a.Sim.Digests() != b.Sim.Digests() || !reflect.DeepEqual(a.Obs, b.Obs) {
				t.Fatal("replay mismatch", s, sc.Name)
			}
		}
	}
}

func TestAdv_9_RandomFaultRecovery(t *testing.T) {
	n := 12
	if os.Getenv("ADV_LONG") == "1" {
		n = 300
	}
	for i := 0; i < n; i++ {
		s := c02Seed + int64(i)
		t.Logf("seed=%d", s)
		run := func() string {
			r := c02NewRigSeed(t, fakepg.Config{}, s)
			rr := rand.New(rand.NewPCG(uint64(s), 93))
			p := r.p()
			kinds := []sim.FaultKind{fakepg.FaultBoundary, fakepg.FaultFreeze, fakepg.FaultFirstByte}
			for j := 0; j < 12; j++ {
				k := kinds[rr.IntN(len(kinds))]
				at := r.d.S.Now() + sim.Time(rr.IntN(10)*int(c02Ms))
				r.d.S.InjectAt(sim.Fault{At: at, Kind: k, Arg2: int64((1 + rr.IntN(5)) * int(c02Ms))})
			}
			for _, k := range kinds {
				r.d.S.InjectAt(sim.Fault{At: r.d.S.Now() + sim.Time(20*c02Ms), Kind: fakepg.FaultClear, Arg1: int64(k)})
			}
			mm := r.q(p, strings.Repeat("SELECT pg_sleep(0.003);", 16))
			c02Status(t, mm, 'I')
			if r.d.Fake.Snapshot().StatementsFinished != 16 {
				t.Fatal("fault schedule lost work", r.d.Fake.Snapshot())
			}
			c02Status(t, r.q(p, "SELECT 129"), 'I')
			p.Reset()
			r.run(c02Ms)
			if e := r.d.S.CheckQuiesce(); e != nil {
				t.Fatal(e)
			}
			return r.d.S.Digests().String()
		}
		if a, b := run(), run(); a != b {
			t.Fatal("random fault replay diverged", s, a, b)
		}
	}
}

func TestAdv_9_CrossProcess(t *testing.T) {
	if os.Getenv("ADV_CHILD") == "1" {
		sc, _ := world.Lookup("direct-mixed-transactions")
		r := world.Run(sc, c02Seed)
		if e := sc.Verify(r); e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(r.Obs)
		fmt.Printf("DIGEST %s %s\n", r.Sim.Digests(), b)
		return
	}
	t.Logf("seed=%d", c02Seed)
	run := func() string {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAdv_9_CrossProcess$")
		cmd.Env = append(os.Environ(), "ADV_CHILD=1")
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal(e, string(b))
		}
		return string(b)
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("cross-process mismatch\n%s\n%s", a, b)
	}
}

func c02Baseline(t *testing.T, name string, check func(*world.Result) bool) {
	t.Helper()
	if testing.Short() {
		t.Skip("slow: runs without -short")
	}
	sc, ok := world.Lookup(name)
	if !ok {
		t.Fatal(name)
	}
	for _, s := range []int64{81173, -81173, 1000003} {
		t.Logf("seed=%d", s)
		r := world.Run(sc, s)
		if e := sc.Verify(r); e != nil {
			t.Fatal(e)
		}
		if !check(r) {
			t.Fatalf("seed=%d baseline violated: %+v", s, r.Obs)
		}
	}
}

func TestAdv_10_500ConnectionsUnpinnedSeeds(t *testing.T) {
	c02Baseline(t, "direct-500-connections", func(r *world.Result) bool { return r.Obs.DBSessionsPeak == 500 && r.Obs.DBConnsAccepted == 500 })
}

func TestAdv_10_BurstUnpinnedSeeds(t *testing.T) {
	c02Baseline(t, "direct-reconnect-burst", func(r *world.Result) bool {
		return r.Obs.DBRefusedTooMany > 0 && r.Obs.DBSessionsPeak == 100 && r.Obs.Clients.Total.Code("53300") > 0
	})
}

func TestAdv_10_IdenticalReadsUnpinnedSeeds(t *testing.T) {
	c02Baseline(t, "direct-1000-identical-reads", func(r *world.Result) bool { return r.Obs.DBReads == 1000 && r.Obs.Clients.Total.OK == 1000 })
}

func c02Root(t *testing.T) string {
	t.Helper()
	p, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestAdv_11_ModuleRequirements(t *testing.T) {
	t.Logf("seed=%d (static)", c02Seed)
	c := exec.Command("go", "mod", "edit", "-json")
	c.Dir = c02Root(t)
	b, e := c.Output()
	if e != nil {
		t.Fatal(e)
	}
	var mod struct {
		Require []struct {
			Path, Version string
			Indirect      bool
		}
	}
	if e := json.Unmarshal(b, &mod); e != nil {
		t.Fatal(e)
	}
	if len(mod.Require) != 1 || mod.Require[0].Path != "github.com/jackc/pgx/v5" || mod.Require[0].Indirect {
		t.Fatalf("expected exactly one direct pgx requirement: %+v", mod.Require)
	}
}

func TestAdv_11_NoReplacementOrWorkspaceEscape(t *testing.T) {
	t.Logf("seed=%d (static)", c02Seed)
	b, e := os.ReadFile(filepath.Join(c02Root(t), "go.mod"))
	if e != nil {
		t.Fatal(e)
	}
	for _, word := range []string{"replace ", "exclude "} {
		if strings.Contains(string(b), word) {
			t.Fatalf("dependency escape via %s", word)
		}
	}
	c := exec.Command("go", "env", "GOWORK")
	c.Dir = c02Root(t)
	b, e = c.Output()
	if e != nil {
		t.Fatal(e)
	}
	if strings.TrimSpace(string(b)) != "" && strings.TrimSpace(string(b)) != "off" {
		t.Fatalf("workspace overrides module graph: %s", b)
	}
}

func TestAdv_2_StatementLimitErrorAbortsTransaction(t *testing.T) {
	r := c02NewRig(t, fakepg.Config{})
	p := r.p()
	c02Status(t, r.q(p, "BEGIN"), 'T')
	mm := r.q(p, strings.Repeat("SELECT 123;", fakepg.MaxStatements+1))
	if !c02HasCode(mm, "54000") {
		t.Fatalf("limit did not reject: %s", c02Shape(mm))
	}
	c02Status(t, mm, 'E')
}

func c02LiveCompare(t *testing.T, q, want string) {
	t.Helper()
	addr := os.Getenv("MG_PG_ADDR")
	if addr == "" {
		t.Fatal("MG_TIER_R requires MG_PG_ADDR")
	}
	c, e := net.DialTimeout("tcp", addr, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var sess testclient.Session
	write := func(b []byte) {
		for len(b) > 0 {
			n, e := c.Write(b)
			if e != nil {
				t.Fatal(e)
			}
			b = b[n:]
		}
	}
	read := func() []testclient.Msg {
		var out []testclient.Msg
		for {
			var h [5]byte
			if _, e := io.ReadFull(c, h[:]); e != nil {
				t.Fatal(e)
			}
			n := int(binary.BigEndian.Uint32(h[1:]))
			if n < 4 || n > 1<<20 {
				t.Fatal("bad server frame", n)
			}
			b := make([]byte, n-4)
			if _, e := io.ReadFull(c, b); e != nil {
				t.Fatal(e)
			}
			m, e := testclient.Decode(h[0], b)
			if e != nil {
				t.Fatal(e)
			}
			out = append(out, m)
			if m.Type == 'Z' {
				return out
			}
		}
	}
	write(sess.Startup(nil, []testclient.Param{{Name: "user", Value: "postgres"}, {Name: "database", Value: "postgres"}}))
	read()
	write(sess.Query(nil, q))
	got := c02Shape(read())
	t.Logf("tier R query=%q postgres=%s fake=%s", q, got, want)
	if got != want {
		t.Errorf("Postgres17=%s fake=%s for %q", got, want, q)
	}
	write(testclient.EncodeTerminate(nil))
}
