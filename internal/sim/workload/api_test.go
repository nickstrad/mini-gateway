package workload_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

// Proves workload.New rejects invalid client, request, key, interval, Zipf, and transaction lengths with ErrConfig.
func TestNewRefusesBadConfigurations(t *testing.T) {
	ok := workload.Tenant{Name: "a", Clients: 1, Requests: 2, Interval: time.Millisecond, Keys: workload.Keys{N: 1}}
	bad := map[string]func(t *workload.Tenant){
		"negative clients":       func(t *workload.Tenant) { t.Clients = -1 },
		"negative requests":      func(t *workload.Tenant) { t.Requests = -1 },
		"too many requests":      func(t *workload.Tenant) { t.Requests = workload.MaxRequests + 1 },
		"Zipf exponent at 1":     func(t *workload.Tenant) { t.Keys = workload.Keys{Kind: workload.KeysZipf, N: 10, Zipf: 1000} },
		"no keys":                func(t *workload.Tenant) { t.Keys = workload.Keys{} },
		"open loop, no interval": func(t *workload.Tenant) { t.Interval = 0 },
		"long transaction":       func(t *workload.Tenant) { t.TxStatements = workload.MaxTxStatements + 1 },
		"too many clients":       func(t *workload.Tenant) { t.Clients = workload.MaxClients + 1 },
	}
	for name, change := range bad {
		s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
		tn := ok
		change(&tn)
		if _, err := workload.New(simnet.New(s), workload.Config{Tenants: []workload.Tenant{tn}}); !errors.Is(err, workload.ErrConfig) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	closed := workload.Tenant{Name: "b", Clients: 1, Requests: 3, Arrival: workload.ArrivalClosed, Statement: "SELECT 1"}
	if _, err := workload.New(simnet.New(s), workload.Config{Tenants: []workload.Tenant{ok, closed}}); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

// Proves KeyName formatting and KeyChooser bounds for uniform and empty configs.
func TestKeyNamesAndChooser(t *testing.T) {
	if workload.KeyName(0) != workload.DefaultKeyPrefix+"0" || workload.KeyName(42) != workload.DefaultKeyPrefix+"42" {
		t.Fatal(workload.KeyName(42))
	}
	kc := workload.NewKeyChooser(sim.NewStream(1, "k"), workload.Keys{N: 5})
	for i := 0; i < 100; i++ {
		k := kc.Next()
		if !strings.HasPrefix(k, workload.DefaultKeyPrefix) {
			t.Fatal(k)
		}
	}
	if workload.NewKeyChooser(sim.NewStream(1, "k"), workload.Keys{}).Index() != 0 {
		t.Fatal("no keys: index 0")
	}
}

// Proves every workload fault kind, Outcome, and EventKind returns a unique canonical name.
func TestWorkloadFaultNamesAndOutcomeNames(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range workload.FaultKinds() {
		n := workload.FaultName(k)
		if n == "" || seen[n] || !strings.HasPrefix(n, "wl.") {
			t.Fatalf("kind %d named %q", k, n)
		}
		seen[n] = true
	}
	if len(seen) != 6 || workload.FaultName(workload.FaultHalfMessage-1) != "" {
		t.Fatalf("%v", seen)
	}
	want := []string{"ok", "error", "connection-lost", "timed-out", "client-left", "connect-failed", "dropped"}
	for i, w := range want {
		if got := workload.Outcome(i + 1).String(); got != w {
			t.Fatalf("outcome %d: %s, want %s", i+1, got, w)
		}
	}
	if workload.Outcome(99).String() != "?" || workload.EventKind(99).String() != "?" {
		t.Fatal("unknown names")
	}
}

// Proves Workload.Done stays false while requests remain and becomes true once all clients finish.
func TestWorkloadDoneOnlyWhenEveryClientFinished(t *testing.T) {
	w := build(t, 1, fakepg.Config{Service: fakepg.Service{Fixed: 10 * time.Millisecond}}, workload.Config{Tenants: []workload.Tenant{one("SELECT 1", 3)}})
	if w.wl.Done() {
		t.Fatal("done before it started")
	}
	if err := w.s.RunFor(60 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if w.wl.Done() {
		t.Fatal("done with requests left")
	}
	w.finish(t)
	if !w.wl.Done() {
		t.Fatal("not done at the end")
	}
}

type probeWorld struct {
	t   *testing.T
	s   *sim.Sim
	n   *simnet.Net
	srv *fakepg.Server
}

func newProbeWorld(t *testing.T) *probeWorld {
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return &probeWorld{t: t, s: s, n: n, srv: srv}
}

func (pw *probeWorld) run(d time.Duration) {
	if err := pw.s.RunFor(d); err != nil {
		pw.t.Fatal(err)
	}
}

// Proves Probe supports pre-startup, startup, stop/resume reading, pipelined queries, trace slicing, and clean termination.
func TestProbeConversation(t *testing.T) {
	pw := newProbeWorld(t)
	p := workload.NewProbe(pw.n, pw.srv.Addr(), simnet.DefaultOptions())
	if p.Connected() || p.Closed() || p.End() == nil {
		t.Fatal("a new probe")
	}
	p.PreStartup(true, false)
	pw.run(time.Millisecond)
	if !p.Connected() || p.Trace() != "connected ssl:N" {
		t.Fatalf("encryption request: %s", p.Trace())
	}
	p.Startup("app", "app", testclient.Param{Name: "application_name", Value: "probe"})
	pw.run(time.Millisecond)
	if p.Session().ProcessID() == 0 || len(p.Session().Secret()) != 4 {
		t.Fatal("no key data")
	}
	from := len(p.Events())
	p.StopReading(true)
	p.Query("SELECT 1", "SELECT 2")
	pw.run(10 * time.Millisecond)
	if got := p.TraceFrom(from); got != "" {
		t.Fatalf("read while stopped: %s", got)
	}
	p.StopReading(false)
	if got := p.TraceFrom(from); got != "T D C:SELECT 1 ZI T D C:SELECT 1 ZI" {
		t.Fatalf("after reading again: %s", got)
	}
	msgs := p.Messages()
	if last := msgs[len(msgs)-1]; last.Type != testclient.MsgReady || p.Events()[len(p.Events())-1].Kind != workload.EventMessage {
		t.Fatalf("%+v", last)
	}
	if p.TraceFrom(len(p.Events())+5) != "" || p.Dropped() != 0 {
		t.Fatal("trace past the end")
	}
	p.Terminate()
	p.Close()
	pw.run(time.Millisecond)
	if !p.Closed() || pw.srv.Snapshot().ConnsOpen.Value != 0 {
		t.Fatalf("after close: %s", p.Trace())
	}
}

// Proves Probe can send cancels, reset connections, and fail cleanly on backend messages exceeding SetMaxMessage.
func TestProbeCancelResetAndMessageLimit(t *testing.T) {
	pw := newProbeWorld(t)
	a := workload.NewProbe(pw.n, pw.srv.Addr(), simnet.DefaultOptions())
	a.Startup("app", "")
	pw.run(time.Millisecond)
	a.Query("SELECT pg_sleep(1)")
	pw.run(time.Millisecond)
	c := workload.NewProbe(pw.n, pw.srv.Addr(), simnet.DefaultOptions())
	c.Cancel(a.Session().ProcessID(), a.Session().Secret())
	pw.run(10 * time.Millisecond)
	if !strings.Contains(a.Trace(), "E:57014 ZI") || pw.srv.Snapshot().CancelsApplied != 1 || c.Trace() != "connected eof" {
		t.Fatalf("cancel: %s / %s", a.Trace(), c.Trace())
	}
	a.Reset()
	pw.run(time.Millisecond)
	if !a.Closed() || pw.srv.Snapshot().ConnsOpen.Value != 0 {
		t.Fatal("reset")
	}
	b := workload.NewProbe(pw.n, pw.srv.Addr(), simnet.DefaultOptions())
	b.SetMaxMessage(16)
	b.Startup("app", "")
	pw.run(time.Millisecond)
	if !strings.HasSuffix(b.Trace(), "protocol-error") || !b.Closed() {
		t.Fatalf("a parameter status above 16 bytes: %s", b.Trace())
	}
	b.Send([]byte("ignored"))
	if err := pw.s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if err := pw.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}
