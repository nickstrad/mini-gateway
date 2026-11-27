package shell

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func TestCancelRequestGoesOutOnASecondConnectionAndReportsDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		msg := pgwire.AppendCancelRequest(nil, pgwire.BackendKey{ProcessID: 42})
		core.onStart = func(proxy.Instant) []proxy.Action {
			return []proxy.Action{{Kind: proxy.ActDBCancel, Conn: 9, Data: msg}}
		}
		cfg := testConfig(l)
		got := make(chan []byte, 1)
		cfg.Dial = func(ctx context.Context, _ string) (net.Conn, error) {
			a, b := net.Pipe()
			go func() {
				buf := make([]byte, len(msg))
				_, _ = io.ReadFull(b, buf)
				got <- buf
				_ = b.Close()
			}()
			return a, nil
		}
		s := startShell(t, core, cfg)
		synctest.Wait()
		if b := <-got; !bytes.Equal(b, msg) {
			t.Fatalf("the database received %x, want the cancel request %x", b, msg)
		}
		synctest.Wait()
		done := core.of(proxy.EvDBCancelDone)
		if len(done) != 1 || done[0].ev.Conn != 9 {
			t.Fatalf("cancel done events %v", done)
		}
		closeShell(t, s)
	})
}

func TestCancelToASilentDatabaseEndsAtTheCleanupDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStart = func(proxy.Instant) []proxy.Action {
			return []proxy.Action{{Kind: proxy.ActDBCancel, Conn: 9, Data: pgwire.AppendCancelRequest(nil, pgwire.BackendKey{})}}
		}
		cfg := testConfig(l)
		cfg.Dial = func(ctx context.Context, _ string) (net.Conn, error) {
			a, b := net.Pipe()
			go func() {
				_, _ = io.Copy(io.Discard, b)
			}()
			return a, nil
		}
		s := startShell(t, core, cfg)
		start := time.Now()
		synctest.Wait()
		core.waitKind(t, proxy.EvDBCancelDone, 1, time.Minute)
		if d := time.Since(start); d > cfg.Timeouts.Cleanup+time.Millisecond {
			t.Fatalf("the cancel to a silent database took %s, want at most timeouts.cleanup %s", d, cfg.Timeouts.Cleanup)
		}
		closeShell(t, s)
	})
}
