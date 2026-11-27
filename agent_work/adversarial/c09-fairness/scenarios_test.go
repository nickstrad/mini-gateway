package adv_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	a "github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

func scratch(t *testing.T) string {
	t.Helper()
	d, e := os.MkdirTemp(".", ".scratch-")
	if e != nil {
		t.Fatal(e)
	}
	d, e = filepath.Abs(d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}
func spec(quiet int, offset time.Duration, burst int) e2e.Spec {
	s := e2e.Spec{Name: "adv-fair", Tenants: quiet + 1, Conns: 2, QueueCap: 128, Duration: 3 * time.Second, Flush: 19 * time.Millisecond,
		Admission: a.Config{Rule: a.RuleTimeout, TenantCapacity: 8, Detail: true}, Rows: []fakepg.Row{{Key: "x", Value: "v"}},
		Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 40 * time.Millisecond}, {Kind: fakepg.KindUpdate, Time: time.Millisecond}}}}
	for i := 0; i < burst; i++ {
		s.Clients = append(s.Clients, e2e.Script{Tenant: 0, Steps: []e2e.Step{{SQL: "SELECT v FROM kv WHERE k = 'x'"}}})
	}
	for i := 1; i <= quiet; i++ {
		s.Clients = append(s.Clients, e2e.Script{Tenant: i, Start: offset, Steps: []e2e.Step{{SQL: "UPDATE kv SET v = 'v' WHERE k = 'x'"}}})
	}
	return s
}
func run(t *testing.T, s e2e.Spec, n int64, dir string) *e2e.SimRun {
	t.Helper()
	t.Logf("seed=%d", n)
	r, e := e2e.RunSim(s, n, e2e.SimOptions{Dir: dir, Arm: "adv"})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.World.Proxy.Engine.Invariants(); e != nil {
		t.Fatal(e)
	}
	if dir != "" {
		if e = r.Flush.Check(); e != nil {
			t.Fatal(e)
		}
	}
	return r
}
func success(c e2e.ClientResult) bool {
	return len(c.Steps) == 1 && e2e.HasToken(c.Steps[0], "C:") && !e2e.HasToken(c.Steps[0], "E:")
}
func latency(t *testing.T, c e2e.ClientResult) time.Duration {
	t.Helper()
	if !success(c) || len(c.Elapsed) != 1 {
		t.Fatalf("quiet result=%v", c)
	}
	return c.Elapsed[0]
}
func noisy(t *testing.T, offset time.Duration, n int64) {
	s := spec(1, offset, 36)
	r := run(t, s, n, "")
	s.Clients = s.Clients[36:]
	alone := run(t, s, n, "")
	got, base := latency(t, r.Clients[36]), latency(t, alone.Clients[0])
	if got > 2*base {
		t.Fatalf("quiet latency=%v alone=%v exceeds 2x; quiet success retained", got, base)
	}
}
func TestAdv_5_ShortQuietDuringLongRead(t *testing.T) { noisy(t, 10*time.Millisecond, seed) }
func TestAdv_5_QuietAtLeaseBoundary(t *testing.T)     { noisy(t, 39*time.Millisecond, seed+1) }
func TestAdv_5_QuietEarlyInFlood(t *testing.T)        { noisy(t, 3*time.Millisecond, seed+2) }

func many(t *testing.T, n int, offset time.Duration) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: many-tenant CSV campaign")
	}
	s := spec(n, offset, 36)
	r := run(t, s, seed, scratch(t))
	s.Clients = s.Clients[36:]
	alone := run(t, s, seed, scratch(t))
	for i := 0; i < n; i++ {
		if success(alone.Clients[i]) && !success(r.Clients[36+i]) {
			t.Errorf("tenant=%d alone success, mixed=%v", i+1, r.Clients[36+i])
		}
	}
	if len(r.Flush.Final.Points) == 0 {
		t.Fatal("empty flushed snapshot")
	}
}
func TestAdv_7_SynchronizedQuietTenants(t *testing.T) { many(t, 41, 10*time.Millisecond) }
func TestAdv_7_TotalQueuePressure(t *testing.T)       { many(t, 120, 10*time.Millisecond) }
func TestAdv_7_QuietArrivalAtSweep(t *testing.T)      { many(t, 57, 100*time.Millisecond) }

func ablation(t *testing.T, burst int, offset time.Duration) {
	s := spec(1, offset, burst)
	fair := run(t, s, seed, "")
	s.Admission = a.Config{}
	fifo := run(t, s, seed, "")
	a, b := latency(t, fair.Clients[burst]), latency(t, fifo.Clients[burst])
	if b <= a {
		t.Fatalf("single queue quiet=%v fair=%v; no degradation", b, a)
	}
}
func TestAdv_9_SingleQueueBurst(t *testing.T)       { ablation(t, 32, 10*time.Millisecond) }
func TestAdv_9_SingleQueueLargerBurst(t *testing.T) { ablation(t, 64, 10*time.Millisecond) }
func TestAdv_9_SingleQueueLateQuiet(t *testing.T)   { ablation(t, 48, 60*time.Millisecond) }

func replay(t *testing.T, fragment bool, cancel bool) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: full CSV replay campaign")
	}
	s := spec(3, 11*time.Millisecond, 12)
	s.Fragment = fragment
	if cancel {
		s.Clients[0].Steps[0].Cancel = time.Millisecond
	}
	x := run(t, s, seed, scratch(t))
	y := run(t, s, seed, scratch(t))
	if !reflect.DeepEqual(x.Clients, y.Clients) || !reflect.DeepEqual(x.Engine, y.Engine) || !reflect.DeepEqual(x.Final, y.Final) {
		t.Fatal("same seed changed replies or snapshots")
	}
	ents, e := os.ReadDir(x.Flush.RunDir)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		p := ent.Name()
		xb, e := os.ReadFile(filepath.Join(x.Flush.RunDir, p))
		if e != nil {
			t.Fatal(e)
		}
		yb, e := os.ReadFile(filepath.Join(y.Flush.RunDir, p))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(xb, yb) {
			t.Errorf("replay changed %s", p)
		}
		count++
	}
	if count == 0 {
		t.Fatal("no files compared")
	}
}
func TestAdv_14_TiedArrivalsAndFlush(t *testing.T) { replay(t, false, false) }
func TestAdv_14_ByteFragmentReplay(t *testing.T)   { replay(t, true, false) }
func TestAdv_14_CancellationReplay(t *testing.T)   { replay(t, false, true) }

func TestAdv_2_EngineEndsMixedTransactions(t *testing.T) {
	s := spec(1, 0, 0)
	s.Clients = []e2e.Script{{Tenant: 0, Steps: []e2e.Step{{SQL: "BEGIN"}, {SQL: "SELECT 1", Vanish: e2e.VanishAfterReply}}}, {Tenant: 1, Start: time.Millisecond, Steps: []e2e.Step{{SQL: "SELECT 1", Cancel: time.Millisecond}}}}
	r := run(t, s, seed, "")
	if r.Engine.LeasesStarted == 0 || r.Engine.LeasesStarted != r.Engine.LeasesEnded {
		t.Fatalf("lease conservation %d/%d", r.Engine.LeasesStarted, r.Engine.LeasesEnded)
	}
}
func TestAdv_14_SeedCampaign(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: independent seed campaign")
	}
	for i := int64(0); i < 32; i++ {
		s := spec(2, time.Duration(1+i%37)*time.Millisecond, 4+int(i%13))
		s.Name = fmt.Sprint("adv-seed-", i)
		x := run(t, s, seed+i, "")
		y := run(t, s, seed+i, "")
		if !reflect.DeepEqual(x.Engine, y.Engine) || !reflect.DeepEqual(x.Clients, y.Clients) {
			t.Fatalf("seed %d changed replay", seed+i)
		}
	}
}
