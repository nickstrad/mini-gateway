package adv_test

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

func scratch(t *testing.T) string {
	t.Helper()
	if e := os.MkdirAll("_scratch", 0755); e != nil {
		t.Fatal(e)
	}
	d, e := os.MkdirTemp("_scratch", t.Name()+"-")
	if e != nil {
		t.Fatal(e)
	}
	a, e := filepath.Abs(d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(a) })
	return a
}
func scenario() e2e.Spec {
	c := cfg()
	c.Cap = 1
	c.IdleTimeout = 2 * time.Second
	c.MaxLifetime = 3 * time.Second
	return e2e.Spec{Name: "adv-custom", Tenants: 2, Conns: 1, QueueCap: 16, Pool: &c, Reset: proxy.ResetOnSwitch, Duration: 3 * time.Second, Flush: 100 * time.Millisecond, Clients: []e2e.Script{{Tenant: 0, Steps: []e2e.Step{{SQL: "SELECT 1"}}}, {Tenant: 1, Start: 400 * time.Millisecond, Steps: []e2e.Step{{SQL: "SELECT 2"}}}}}
}
func simulate(t *testing.T, s e2e.Spec) *e2e.SimRun {
	t.Helper()
	t.Logf("seed=%d", seed)
	r, e := e2e.RunSim(s, seed, e2e.SimOptions{Dir: scratch(t), Replay: fmt.Sprintf("go test -run '^%s$' ./agent_work/adversarial/c07-pool", t.Name())})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Flush.Check(); e != nil {
		t.Fatal(e)
	}
	return r
}
func death(t *testing.T, mode int) {
	s := scenario()
	s.Pool.BreakerFailures = 1
	if mode != 1 {
		s.Clients[1].Tenant = 0
	}
	s.Clients = append(s.Clients, e2e.Script{Tenant: 0, Start: 2 * time.Second, Steps: []e2e.Step{{SQL: "SELECT 3"}}})
	s.Timeouts.CheckTimeout = 30 * time.Millisecond
	s.Timeouts.Reset = 50 * time.Millisecond
	s.Timeouts.DBConnect = 50 * time.Millisecond
	s.Timeouts.Statement = 100 * time.Millisecond
	s.Timeouts.QueueWait = 500 * time.Millisecond
	s.Timeouts.Cleanup = 100 * time.Millisecond
	s.Pool.CheckTimeout = 30 * time.Millisecond
	s.Path = &e2e.PathPlan{Events: []e2e.PathEvent{{At: 200 * time.Millisecond, Fault: e2e.PathBlackhole}, {At: time.Second, Clear: true}}}
	if mode == 1 {
		s.Pool.CheckAfterIdle = 40 * time.Millisecond
	}
	if mode == 2 {
		s.Path.Events[0].Fault = e2e.PathPeerForgot
	}
	r := simulate(t, s)
	if !e2e.HasToken(r.Clients[0].Last(), "ZI") {
		t.Fatalf("control query did not succeed: %v", r.Clients[0])
	}
	if !e2e.HasToken(r.Clients[2].Last(), "D:3") {
		t.Errorf("recovery query failed: %v", r.Clients[2])
	}
	if r.Path.Stats().Blackholed+r.Path.Stats().Forgot == 0 {
		t.Fatal("fault not exercised")
	}
	for _, tok := range r.Clients[1].Last() {
		if len(tok) >= 5 && (tok[:5] == "error" || tok[:5] == "fatal") {
			t.Fatalf("zero connections lent at death, but next client saw %s; full reply %v", tok, r.Clients[1])
		}
	}
	if r.Engine.Failed+r.Engine.Shed > 0 {
		t.Fatalf("zero lent at death, client failures=%d sheds=%d reply=%v", r.Engine.Failed, r.Engine.Shed, r.Clients[1])
	}
}
func TestAdv_6_RecentIdleBlackhole(t *testing.T)  { death(t, 0) }
func TestAdv_6_OldIdleCheckRefusal(t *testing.T)  { death(t, 1) }
func TestAdv_6_RecentIdlePeerForgot(t *testing.T) { death(t, 2) }

func startup(t *testing.T, mode int) {
	t.Logf("seed=%d", seed)
	w, e := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed, Engine: proxy.Config{Connections: 1, MaxClients: 100, QueueCap: 50, MaxStartup: 2, Pool: &pool.Config{Cap: 1}, Client: pgwire.ClientConfig{TLS: pgwire.ClientTLSRequire}, Timeouts: proxy.Timeouts{ClientStartup: 10 * time.Millisecond}}})
	if e != nil {
		t.Fatal(e)
	}
	p := w.Proxy.Engine
	p.Start(0, nil)
	ssl := pgwire.AppendSSLRequest(nil)
	for i := 1; i <= 60; i++ {
		id := proxy.ConnID(i)
		p.Step(0, proxy.Event{Kind: proxy.EvClientAccepted, Conn: id}, nil)
		if mode != 0 {
			p.Step(0, proxy.Event{Kind: proxy.EvClientData, Conn: id, Data: ssl}, nil)
		}
		if mode == 2 && i%2 == 0 {
			p.Step(0, proxy.Event{Kind: proxy.EvClientClosed, Conn: id}, nil)
		}
		if p.Snapshot().ClientsInStartup.Value > 2 {
			t.Fatal("startup cap exceeded")
		}
	}
	if p.Snapshot().ClientsInStartup.Peak != 2 {
		t.Fatal("startup cap never filled")
	}
	p.Step(proxy.Instant(10*time.Millisecond), proxy.Event{Kind: proxy.EvTimer}, nil)
	if p.Snapshot().ClientsInStartup.Value != 0 {
		t.Fatal("startup deadline leaked capacity")
	}
	p.Shutdown(proxy.Instant(11*time.Millisecond), nil)
	if e := p.Invariants(); e != nil {
		t.Fatal(e)
	}
}
func TestAdv_11_SilentStartupStorm(t *testing.T) { startup(t, 0) }
func TestAdv_11_TLSHandshakeStall(t *testing.T)  { startup(t, 1) }
func TestAdv_11_TLSCloseStorm(t *testing.T)      { startup(t, 2) }

func TestAdv_14_ScenarioReplay(t *testing.T) {
	s := scenario()
	s.Fragment = true
	a, b := simulate(t, s), simulate(t, s)
	if a.World.S.Digests() != b.World.S.Digests() || !reflect.DeepEqual(a.Engine, b.Engine) || !reflect.DeepEqual(a.Clients, b.Clients) {
		t.Fatal("same seed changed trace, snapshot, or client replies")
	}
}
func TestAdv_14_FlushedCapacity(t *testing.T) {
	s := scenario()
	r := simulate(t, s)
	paths, e := filepath.Glob(filepath.Join(r.Flush.RunDir, "*.csv"))
	if e != nil {
		t.Fatal(e)
	}
	seen := 0
	for _, p := range paths {
		f, e := os.Open(p)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := csv.NewReader(f).ReadAll()
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) < 2 {
			continue
		}
		cols := map[string]int{}
		for i, k := range rows[0] {
			cols[k] = i
		}
		mi, mok := cols["metric"]
		vi, vok := cols["value"]
		ci, cok := cols["component"]
		if !mok || !vok || !cok {
			continue
		}
		for _, row := range rows[1:] {
			if row[ci] == "pool" && row[mi] == "open_plus_connecting" {
				v, e := strconv.ParseInt(row[vi], 10, 64)
				if e != nil || v < 0 || v > 1 {
					t.Fatalf("CSV cap row %v", row)
				}
				seen++
			}
		}
	}
	if seen == 0 {
		t.Fatal("no pool capacity rows read")
	}
}
