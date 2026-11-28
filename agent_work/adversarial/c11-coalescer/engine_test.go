package adv_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	c "github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

func scratch(t *testing.T) string {
	t.Helper()
	d, e := os.MkdirTemp(".", "scratch-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	p, e := filepath.Abs(d)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func spec() e2e.Spec {
	s := e2e.Spec{Name: "adv-c11", Tenants: 3, Conns: 2, QueueCap: 64, Duration: 2 * time.Second, Rows: []fakepg.Row{{Key: "edge", Value: "answer"}}, Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 30 * time.Millisecond}}}, Coalesce: c.Config{Mode: c.ModeAuto, Join: c.JoinRelaxed}}
	for i := 0; i < 9; i++ {
		s.Clients = append(s.Clients, e2e.Script{Tenant: i % 3, Start: time.Duration(i) * time.Millisecond, Steps: []e2e.Step{{SQL: "SELECT v FROM kv WHERE k = 'edge'"}}})
	}
	return s
}
func simRun(t *testing.T, s e2e.Spec, o e2e.SimOptions) *e2e.SimRun {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./agent_work/adversarial/c11-coalescer", seed, t.Name())
	r, e := e2e.RunSim(s, seed, o)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.World.Proxy.Engine.Invariants(); e != nil {
		t.Fatal(e)
	}
	if e = r.World.Proxy.Engine.Quiesce(); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestAdv_11_QueueAndConnectionPeaks(t *testing.T) {
	s := spec()
	s.Conns = 1
	r := simRun(t, s, e2e.SimOptions{})
	if r.Engine.CoalesceFollowed < 5 {
		t.Fatalf("followers not exercised: %+v", r.Engine)
	}
	if r.Engine.DBLent.Peak > 1 || r.Engine.Queued.Peak > 1 {
		t.Fatalf("followers consumed resources: lent=%d queued=%d", r.Engine.DBLent.Peak, r.Engine.Queued.Peak)
	}
}
func TestAdv_11_TenantHitsAfterOwnerCancellation(t *testing.T) {
	s := spec()
	s.Pool = &pool.Config{RatePerSec: 1000, MaxConnecting: 2}
	s.Clients[0].Steps[0].Cancel = 15 * time.Millisecond
	r := simRun(t, s, e2e.SimOptions{})
	cs := r.World.Proxy.Engine.CoalesceSnapshot()
	var n int64
	for _, h := range cs.PerTenant {
		n += h.Hits
	}
	want(t, n, cs.Hits)
	if len(cs.PerTenant) != 3 || cs.Hits < 5 || cs.Handoffs == 0 {
		t.Fatalf("expected hits in three tenants and handoff: %+v", cs)
	}
	if e := r.Engine.Check(); e != nil {
		t.Fatal(e)
	}
}
func TestAdv_11_ShutdownDuringSharedRead(t *testing.T) {
	s := spec()
	s.Duration = 20 * time.Millisecond
	r := simRun(t, s, e2e.SimOptions{})
	if r.Engine.CoalesceFollowed == 0 {
		t.Fatal("no follower reached shutdown")
	}
	want(t, r.World.Proxy.Engine.CoalesceSnapshot().Waiting.Value, int64(0))
}
func equalReplies(t *testing.T, s e2e.Spec) {
	t.Helper()
	a := simRun(t, s, e2e.SimOptions{})
	b := simRun(t, s, e2e.SimOptions{Direct: true})
	if a.Engine.CoalesceFollowed == 0 {
		t.Fatal("no shared reply exercised")
	}
	for i := range a.Clients {
		if len(a.Clients[i].Steps) == 0 {
			t.Fatalf("client %d no reply", i)
		}
		want(t, a.Clients[i].Steps, b.Clients[i].Steps)
	}
}
func TestAdv_12_OneByteFragmentsMatchDirect(t *testing.T) {
	s := spec()
	s.Fragment = true
	equalReplies(t, s)
}
func TestAdv_12_EmptyResultMatchesDirect(t *testing.T) { s := spec(); s.Rows = nil; equalReplies(t, s) }
func TestAdv_12_StalledFollowerCannotDelayPeers(t *testing.T) {
	s := spec()
	s.Rows[0].Value = strings.Repeat("z", 32<<10)
	s.Clients[4].Steps[0].StallFor = 200 * time.Millisecond
	a := simRun(t, s, e2e.SimOptions{})
	s.Clients[4].Steps[0].StallFor = 0
	b := simRun(t, s, e2e.SimOptions{})
	if a.Engine.CoalesceFollowed == 0 {
		t.Fatal("no follower")
	}
	for i := range a.Clients {
		want(t, a.Clients[i].Steps, b.Clients[i].Steps)
		if i != 4 {
			want(t, a.Clients[i].Elapsed, b.Clients[i].Elapsed)
		}
	}
}

type poisoned struct {
	*proxy.CoreCoalescer
	fault int
}

func (p *poisoned) Check() error {
	if p.fault == 1 {
		return errors.New("adversarial core sentinel")
	}
	return p.CoreCoalescer.Check()
}
func (p *poisoned) Counters() c.Snapshot {
	s := p.CoreCoalescer.Counters()
	if p.fault == 2 {
		s.Waiting.Value++
	}
	return s
}
func poisonEngine(t *testing.T, k int) *proxy.Engine {
	t.Helper()
	r, e := e2e.BuildSim(spec(), seed, e2e.SimOptions{})
	if e != nil {
		t.Fatal(e)
	}
	conf := r.World.Proxy.Engine.Config()
	core, e := proxy.NewCoreCoalescer(c.Config{Mode: c.ModeAuto})
	if e != nil {
		t.Fatal(e)
	}
	if k == 3 {
		core.Admit(0, 999, unit("orphan"))
	}
	conf.Seams.Coalescer = &poisoned{CoreCoalescer: core, fault: k}
	eng, e := proxy.New(conf)
	if e != nil {
		t.Fatal(e)
	}
	return eng
}
func TestAdv_16_CoreCheckMustBeCalled(t *testing.T) {
	e := poisonEngine(t, 1).Invariants()
	if e == nil || !strings.Contains(e.Error(), "adversarial core sentinel") {
		t.Fatalf("core corruption invisible: %v", e)
	}
}
func TestAdv_16_WaiterGaugeMismatchMustBeCaught(t *testing.T) {
	if e := poisonEngine(t, 2).Invariants(); e == nil {
		t.Fatal("phantom waiting gauge invisible to engine")
	}
}
func TestAdv_16_OrphanFlightMustBeCaught(t *testing.T) {
	if e := poisonEngine(t, 3).Invariants(); e == nil {
		t.Fatal("live core flight without engine owner invisible")
	}
}
func TestAdv_17_FragmentedReplayAndFlush(t *testing.T) {
	s := spec()
	s.Fragment = true
	a := simRun(t, s, e2e.SimOptions{Dir: scratch(t)})
	b := simRun(t, s, e2e.SimOptions{Dir: scratch(t)})
	want(t, a.World.S.Digests(), b.World.S.Digests())
	want(t, a.Engine, b.Engine)
	for _, r := range []*e2e.SimRun{a, b} {
		if e := csvflush.RoundTrip(r.Flush.RunDir, r.Final); e != nil {
			t.Fatal(e)
		}
	}
	fa, _ := filepath.Glob(filepath.Join(a.Flush.RunDir, "*.csv"))
	for _, p := range fa {
		aa, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		bb, e := os.ReadFile(filepath.Join(b.Flush.RunDir, filepath.Base(p)))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(aa, bb) {
			t.Fatalf("flush differs: %s", filepath.Base(p))
		}
	}
}
func TestAdv_17_CancelAndCapFlush(t *testing.T) {
	s := spec()
	s.Coalesce.MaxReplyBytes = 16
	s.Clients[5].Steps[0].Cancel = time.Millisecond
	r := simRun(t, s, e2e.SimOptions{Dir: scratch(t)})
	if r.World.Proxy.Engine.CoalesceSnapshot().Detached == 0 {
		t.Fatal("reply overflow not exercised")
	}
	if e := csvflush.RoundTrip(r.Flush.RunDir, r.Final); e != nil {
		t.Fatal(e)
	}
}
func TestAdv_17_CrossProcessReplay(t *testing.T) {
	if os.Getenv("ADV_CHILD") == "1" {
		r := simRun(t, spec(), e2e.SimOptions{})
		b, e := json.Marshal(struct {
			Engine proxy.Snapshot
			Coal   c.Snapshot
			Digest string
		}{r.Engine, r.World.Proxy.Engine.CoalesceSnapshot(), r.World.S.Digests().String()})
		if e != nil {
			t.Fatal(e)
		}
		fmt.Printf("ADV-DIGEST:%s\n", b)
		return
	}
	var previous []byte
	for range 3 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAdv_17_CrossProcessReplay$")
		cmd.Env = append(os.Environ(), "ADV_CHILD=1")
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("child: %v %s", e, b)
		}
		var got []byte
		for _, line := range bytes.Split(b, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("ADV-DIGEST:")) {
				got = line
			}
		}
		if len(got) == 0 {
			t.Fatal("child returned no digest")
		}
		if previous != nil && !reflect.DeepEqual(previous, got) {
			t.Fatal("cross-process snapshots or digests differ")
		}
		previous = got
	}
}

func TestAdv_2_WritesAndTransactionsReachDatabase(t *testing.T) {
	s := spec()
	for i := range s.Clients {
		s.Clients[i].Steps = []e2e.Step{{SQL: "BEGIN"}, {SQL: "SELECT v FROM kv WHERE k = 'edge'"}, {SQL: "UPDATE kv SET v = 'next' WHERE k = 'edge'"}, {SQL: "COMMIT"}}
	}
	r := simRun(t, s, e2e.SimOptions{})
	want(t, r.Engine.CoalesceFollowed, int64(0))
	want(t, len(r.World.Fake.WriteLog()), len(s.Clients))
	reads := 0
	for _, q := range r.World.Fake.StatementLog() {
		if q.Kind == fakepg.KindRead {
			reads++
		}
	}
	want(t, reads, len(s.Clients))
}
func TestAdv_7_DirtyPoolAndRecovery(t *testing.T) {
	s := spec()
	s.Pool = &pool.Config{RatePerSec: 1000, MaxConnecting: 2}
	for i := range s.Clients {
		s.Clients[i].Steps[0].Cancel = 12 * time.Millisecond
	}
	s.Clients = append(s.Clients, e2e.Script{Tenant: 0, Start: time.Second, Steps: []e2e.Step{{SQL: "SELECT v FROM kv WHERE k = 'edge'"}}})
	r := simRun(t, s, e2e.SimOptions{})
	p, ok := r.World.Proxy.Engine.PoolSnapshot()
	want(t, ok, true)
	if p.Cancels == 0 || p.CancelsInFlight.Value != 0 {
		t.Fatalf("dirty cleanup not completed: %+v", p)
	}
	cs := r.World.Proxy.Engine.CoalesceSnapshot()
	if cs.Cancelled == 0 {
		t.Fatalf("last departure did not cancel work: %+v", cs)
	}
	if !e2e.HasToken(r.Clients[len(r.Clients)-1].Last(), "D:") {
		t.Fatalf("recovery read failed: %s", r.Clients[len(r.Clients)-1])
	}
}
func TestAdv_14_OnSwitchNamesScope(t *testing.T) {
	for _, mode := range []string{"auto", "hinted"} {
		_, e := config.Parse([]byte(fmt.Sprintf(`{"lease":{"reset":"on-switch"},"coalesce":{"mode":%q,"key_scope":"all-tenants"}}`, mode)))
		if e == nil || !strings.Contains(e.Error(), "coalesce.key_scope") {
			t.Fatalf("unsafe scope not named: %v", e)
		}
	}
}
