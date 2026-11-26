package shell

import (
	"context"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

func engineForShell(t *testing.T) *proxy.Engine {
	t.Helper()
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := w.Proxy.Engine.Config()
	cfg.Connections = 1
	e, err := proxy.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCloseShutsTheEngineDownBeforeTheFinalFlush(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		engine := engineForShell(t)
		l := newPipeListener()
		cfg := testConfig(l)
		cfg.Obs.CSVDir = dir
		cfg.Dial = func(ctx context.Context, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		cfg.Sources = func(c *obs.Collector, sh func() proxy.ShellSnapshot) error { return enginesrc.Register(c, engine, sh) }
		s := startShell(t, engine, cfg)
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		synctest.Wait()
		time.Sleep(cfg.PublishInterval)
		synctest.Wait()
		closeShell(t, s)
		r, err := csvflush.Read(s.CSVRunDir())
		if err != nil {
			t.Fatal(err)
		}
		seqs := r.FlushSeqs(cfg.Obs.Instance)
		if len(seqs) < 2 {
			t.Fatalf("%d flushes", len(seqs))
		}
		value := func(seq int64, metric string) int64 {
			return r.Snapshot(cfg.Obs.Instance, seq).Value(obs.ComponentEngine, metric, obs.Labels{})
		}
		before, final := seqs[len(seqs)-2], seqs[len(seqs)-1]
		if value(before, "clients_connected") != 1 || value(before, "database_connecting") != 1 {
			t.Fatalf("before the stop: clients %d connecting %d", value(before, "clients_connected"), value(before, "database_connecting"))
		}
		for _, m := range []string{"clients_connected", "clients_in_startup", "database_connecting", "requests_in_flight", "connections_resetting", "timers_pending"} {
			if v := value(final, m); v != 0 {
				t.Errorf("final flush: %s = %d, want 0 (the engine was not shut down before it)", m, v)
			}
		}
	})
}
