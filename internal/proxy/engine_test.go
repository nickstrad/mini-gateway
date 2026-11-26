package proxy_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func world(t *testing.T, cfg proxy.Config) *proxysim.World {
	t.Helper()
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 1, Scenario: t.Name(), Engine: cfg,
		Fake: fakepg.Config{Rows: []fakepg.Row{{Key: "a", Value: "v"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func ready(t *testing.T, w *proxysim.World, tenant int) *workload.Probe {
	t.Helper()
	p := workload.NewProbe(w.Net, w.Proxy.Addr(), proxysim.NetOptions())
	p.Startup(proxysim.TenantName(tenant), proxysim.Database)
	if err := w.S.RunFor(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p.Trace(), "ZI") {
		t.Fatalf("startup: %s", p.Trace())
	}
	return p
}

func ask(t *testing.T, w *proxysim.World, p *workload.Probe, q string) string {
	t.Helper()
	mark := len(p.Events())
	p.Query(q)
	if err := w.S.RunFor(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	return p.TraceFrom(mark)
}

func finish(t *testing.T, w *proxysim.World, probes ...*workload.Probe) proxy.Snapshot {
	t.Helper()
	for _, p := range probes {
		p.Terminate()
		p.Close()
	}
	if err := w.Proxy.Finish(); err != nil {
		t.Fatal(err)
	}
	s := w.Proxy.Engine.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDrainingRefusesNewWorkWith53M05AndClosesWhenIdle(t *testing.T) {
	w := world(t, proxy.Config{Connections: 1})
	p := ready(t, w, 0)
	w.Proxy.Post(proxy.Event{Kind: proxy.EvDraining, On: true})
	got := ask(t, w, p, "SELECT 1")
	if !strings.HasPrefix(got, "E:53M05 ZI") || !p.Closed() {
		t.Fatalf("draining: %q closed %v", got, p.Closed())
	}
	s := finish(t, w)
	if s.ByRow[pgwire.ErrRowDraining] != 1 || s.StageRefuse[proxy.StageDraining] != 1 || s.ClientCloses[proxy.CloseClientDraining] != 1 {
		t.Fatalf("draining counters: %+v", s.StageRefuse)
	}
}

func TestPlaintextStartupUnderRequireGets28000(t *testing.T) {
	cfg := proxy.Config{Connections: 1}
	cfg.Client.TLS = pgwire.ClientTLSRequire
	w := world(t, cfg)
	p := workload.NewProbe(w.Net, w.Proxy.Addr(), proxysim.NetOptions())
	p.Startup(proxysim.TenantName(0), proxysim.Database)
	if err := w.S.RunFor(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := p.Trace(); !strings.Contains(got, "E:28000") {
		t.Fatalf("plaintext under require: %q", got)
	}
	s := finish(t, w)
	if s.ClientCloses[proxy.CloseClientStartup] != 1 || w.Proxy.Engine.WireSnapshot().Client.PlaintextRefused != 1 {
		t.Fatalf("closes %v", s.ClientCloses)
	}
}

func TestClientCapRefusesWith53300(t *testing.T) {
	w := world(t, proxy.Config{Connections: 1, MaxClients: 1, MaxStartup: 1, QueueCap: 1})
	a := ready(t, w, 0)
	b := workload.NewProbe(w.Net, w.Proxy.Addr(), proxysim.NetOptions())
	b.Startup(proxysim.TenantName(1), proxysim.Database)
	if err := w.S.RunFor(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := b.Trace(); !strings.Contains(got, "E:53300") {
		t.Fatalf("over the cap: %q", got)
	}
	s := finish(t, w, a)
	if s.ClientsRefused != 1 || s.ByRow[pgwire.ErrRowTooManyClients] != 1 {
		t.Fatalf("refused %d", s.ClientsRefused)
	}
}

func TestStageCountersMoveAndConserve(t *testing.T) {
	w := world(t, proxy.Config{Connections: 1})
	p := ready(t, w, 0)
	for _, q := range []string{"SELECT v FROM kv WHERE k = 'a'", "SET search_path = x", "BEGIN", "SELECT 1", "COMMIT"} {
		ask(t, w, p, q)
	}
	s := finish(t, w, p)
	for st := proxy.StageClassify; st < proxy.NumStages; st++ {
		if s.StagePassed[st] == 0 {
			t.Errorf("stage %s never passed", st)
		}
	}
	if s.Received != 5 || s.Rejected != 1 || s.Completed != 4 || s.LeasesStarted != 2 || s.ResetsCompleted != 2 {
		t.Fatalf("received %d rejected %d completed %d leases %d resets %d", s.Received, s.Rejected, s.Completed, s.LeasesStarted, s.ResetsCompleted)
	}
	if s.StatementTime.Count != 4 || s.ResetTime.Count != 2 || s.QueueWait.Count != 2 {
		t.Fatalf("histograms: %d %d %d", s.StatementTime.Count, s.ResetTime.Count, s.QueueWait.Count)
	}
}

func TestSnapshotCheckCatchesBrokenConservation(t *testing.T) {
	good := proxy.Snapshot{Received: 3, Accepted: 2, Rejected: 1, Completed: 1, Failed: 1, FailedNoReply: 0, ReachedYes: 1, DBCap: 1, QueueCap: 1, RelayCap: 1}
	good.ByRow[pgwire.ErrRowLostAfterRead] = 1
	good.ByRow[pgwire.ErrRowUnsupportedFeature] = 1
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*proxy.Snapshot){
		"accepted":  func(s *proxy.Snapshot) { s.Accepted++ },
		"rows":      func(s *proxy.Snapshot) { s.ByRow[pgwire.ErrRowShed]++ },
		"reached":   func(s *proxy.Snapshot) { s.ReachedNo++ },
		"resets":    func(s *proxy.Snapshot) { s.ResetsStarted++ },
		"leases":    func(s *proxy.Snapshot) { s.LeasesEnded++ },
		"cap":       func(s *proxy.Snapshot) { s.DBOpen.Set(2) },
		"queue":     func(s *proxy.Snapshot) { s.Queued.Set(2) },
		"relay":     func(s *proxy.Snapshot) { s.RelayHighWater = 2 },
		"completed": func(s *proxy.Snapshot) { s.Completed-- },
	} {
		s := good
		mut(&s)
		if s.Check() == nil {
			t.Errorf("%s: a broken snapshot passed Check", name)
		}
	}
}

func TestFIFOAndFixedSet(t *testing.T) {
	q := proxy.NewFIFO(2)
	if !q.Push(0, "a", 1) || !q.Push(0, "b", 2) || q.Push(0, "c", 3) || q.Len() != 2 {
		t.Fatal("FIFO capacity")
	}
	if !q.Remove(1) || q.Remove(9) || q.Len() != 1 {
		t.Fatal("FIFO remove")
	}
	if id, ok := q.Pop(0); !ok || id != 2 {
		t.Fatal("FIFO order")
	}
	if _, ok := q.Pop(0); ok {
		t.Fatal("FIFO empty")
	}
	p := proxy.NewFixedSet(2)
	p.Checkin(0, 7)
	p.Checkin(0, 8)
	p.Checkin(0, 9)
	if p.Idle() != 2 {
		t.Fatal("fixed set over capacity")
	}
	p.Remove(7)
	if id, ok := p.Checkout(0, work.TenantID("t")); !ok || id != 8 {
		t.Fatal("fixed set order")
	}
	if _, ok := p.Checkout(0, ""); ok {
		t.Fatal("fixed set empty")
	}
	if (proxy.ResetAlwaysPolicy{}).AtRelease(0, proxy.ReleaseInput{Status: 'T'}) != proxy.ReleaseClose ||
		(proxy.ResetAlwaysPolicy{}).AtRelease(0, proxy.ReleaseInput{Status: 'I', Unread: true}) != proxy.ReleaseClose ||
		(proxy.ResetAlwaysPolicy{}).AtRelease(0, proxy.ReleaseInput{Status: 'I'}) != proxy.ReleaseReset {
		t.Fatal("release policy")
	}
}

func TestConfigValidationNamesTheKey(t *testing.T) {
	base := proxy.Config{}.WithDefaults()
	for key, mut := range map[string]func(*proxy.Config){
		"pool.connections":      func(c *proxy.Config) { c.Connections = -1 },
		"queue.capacity":        func(c *proxy.Config) { c.QueueCap = c.MaxClients + 1 },
		"limits.max_startup":    func(c *proxy.Config) { c.MaxStartup = c.MaxClients + 1 },
		"limits.reply_buffer":   func(c *proxy.Config) { c.ReplyBufferCap = 10 },
		"lease.reset":           func(c *proxy.Config) { c.Reset = 9 },
		"timeouts.reset":        func(c *proxy.Config) { c.Timeouts.Reset = c.Timeouts.QueueWait },
		"timeouts.statement":    func(c *proxy.Config) { c.Timeouts.Statement = 20 * time.Minute },
		"timeouts.client_write": func(c *proxy.Config) { c.Timeouts.ClientWrite = -1 },
	} {
		c := base
		mut(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v", key, err)
		}
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
}
