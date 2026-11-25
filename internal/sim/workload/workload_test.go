package workload_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

type world struct {
	s   *sim.Sim
	n   *simnet.Net
	srv *fakepg.Server
	wl  *workload.Workload
}

func build(t testing.TB, seed int64, pg fakepg.Config, wc workload.Config) *world {
	t.Helper()
	return buildOn(t, sim.New(sim.Config{Seed: seed, Scenario: t.Name()}), pg, wc)
}

func buildOn(t testing.TB, s *sim.Sim, pg fakepg.Config, wc workload.Config) *world {
	t.Helper()
	n := simnet.New(s)
	srv, err := fakepg.New(n, pg)
	if err != nil {
		t.Fatal(err)
	}
	wc.Addr = srv.Addr()
	wl, err := workload.New(n, wc)
	if err != nil {
		t.Fatal(err)
	}
	wl.Start()
	return &world{s: s, n: n, srv: srv, wl: wl}
}

func (w *world) finish(t testing.TB) {
	t.Helper()
	if err := w.s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
	if err := w.s.Verify(); err != nil {
		t.Fatal(err)
	}
}

// Proves a basic mixed read/write/transaction workload completes every request and quiesces cleanly.
func TestSmoke(t *testing.T) {
	w := build(t, 1, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "x"}}, Service: fakepg.Service{Fixed: time.Millisecond}},
		workload.Config{Tenants: []workload.Tenant{{Name: "a", Clients: 3, Requests: 10, Interval: 5 * time.Millisecond,
			Keys: workload.Keys{N: 4}, Mix: []workload.Weight{{Op: workload.OpRead, Weight: 3}, {Op: workload.OpWrite, Weight: 1}, {Op: workload.OpTx, Weight: 1}},
			TxStatements: 3, TxReads: 1}}})
	w.finish(t)
	s := w.wl.Snapshot()
	if s.Total.Intended != 30 || s.Total.OK != 30 {
		t.Fatalf("%+v", s.Total)
	}
}

func sprint(v any) string { return fmt.Sprintf("%+v", v) }
