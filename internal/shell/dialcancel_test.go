package shell

import (
	"context"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// TestEngineCloseCancelsADialInProgress proves ActDBClose cancels an in-progress dial immediately instead of waiting for timeouts.database_connect.
func TestEngineCloseCancelsADialInProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStart = func(proxy.Instant) []proxy.Action { return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 1}} }
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind == proxy.EvClientAccepted {
				return []proxy.Action{act(proxy.ActDBClose, 1)}
			}
			return nil
		}
		cfg := testConfig(l)
		cfg.Dial = func(ctx context.Context, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		s := startShell(t, core, cfg)
		synctest.Wait()
		if s.Snapshot().DBReaders.Value != 1 {
			t.Fatal("no dial in progress")
		}
		start := time.Now()
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		synctest.Wait()
		if s.Snapshot().DBReaders.Value != 0 || time.Since(start) != 0 {
			t.Fatalf("the dial outlived the engine's close: %+v after %v", s.Snapshot(), time.Since(start))
		}
		if n := len(core.of(proxy.EvDBDialFailed)); n != 0 {
			t.Fatalf("%d dial failures posted for a connection the engine closed", n)
		}
		closeShell(t, s)
	})
}
