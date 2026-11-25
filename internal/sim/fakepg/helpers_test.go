package fakepg_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

type world struct {
	t    testing.TB
	soft bool
	err  error
	s    *sim.Sim
	n    *simnet.Net
	srv  *fakepg.Server
	ps   []*workload.Probe
}

func (w *world) failf(format string, args ...any) {
	w.t.Helper()
	if !w.soft {
		w.t.Fatalf(format, args...)
	}
	if w.err == nil {
		w.err = fmt.Errorf(format, args...)
	}
}

func (w *world) closeAll() {
	for _, p := range w.ps {
		p.Terminate()
		p.Close()
	}
}

func newWorld(t testing.TB, cfg fakepg.Config) *world {
	t.Helper()
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	n := simnet.New(s)
	srv, err := fakepg.New(n, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, s: s, n: n, srv: srv}
}

func (w *world) dial() *workload.Probe {
	p := workload.NewProbe(w.n, w.srv.Addr(), simnet.DefaultOptions())
	w.ps = append(w.ps, p)
	return p
}

func (w *world) client() *workload.Probe {
	p := w.dial()
	p.Startup("app", "app")
	w.run(time.Millisecond)
	if !strings.HasSuffix(p.Trace(), "ZI") {
		w.failf("startup did not finish: %s", p.Trace())
	}
	return p
}

func (w *world) run(d time.Duration) {
	w.t.Helper()
	if err := w.s.RunFor(d); err != nil {
		w.failf("%v", err)
	}
}

func (w *world) quiet() {
	w.t.Helper()
	if err := w.s.RunUntilQuiet(); err != nil {
		w.failf("%v", err)
	}
	if err := w.s.CheckQuiesce(); err != nil {
		w.failf("%v", err)
	}
}

func (w *world) exchange(p *workload.Probe, qs ...string) string {
	w.t.Helper()
	from := len(p.Events())
	p.Query(qs...)
	w.run(100 * time.Millisecond)
	return p.TraceFrom(from)
}

func (w *world) done(ps ...*workload.Probe) {
	w.t.Helper()
	for _, p := range ps {
		p.Terminate()
		p.Close()
	}
	w.quiet()
}

type testProbe struct{ *workload.Probe }
