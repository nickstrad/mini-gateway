package shell

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config/certtest"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const engineAndAccept = 2

func resumeOnData(_ proxy.Instant, ev proxy.Event) []proxy.Action {
	switch ev.Kind {
	case proxy.EvClientData:
		return []proxy.Action{act(proxy.ActClientResume, ev.Conn)}
	case proxy.EvDBData:
		return []proxy.Action{act(proxy.ActDBResume, ev.Conn)}
	}
	return nil
}

func TestTeardownLeavesNoGoroutine(t *testing.T) {
	t.Run("client gone", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, core := newPipeListener(), newCore()
			core.onStep = resumeOnData
			s := startShell(t, core, testConfig(l))
			c, err := l.Dial()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Write(sslRequest()); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			_ = c.Close()
			synctest.Wait()
			if got := kinds(core.events()); len(got) != 3 || got[2] != "client-closed" {
				t.Fatalf("events %v", got)
			}
			wantGoroutines(t, s, engineAndAccept)
			closeShell(t, s)
		})
	})
	t.Run("database gone", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, dbl, core := newPipeListener(), newPipeListener(), newCore()
			core.onStart = func(proxy.Instant) []proxy.Action { return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 7}} }
			cfg := testConfig(l)
			cfg.Dial = dbl.DialContext
			s := startShell(t, core, cfg)
			db, err := dbl.Accept()
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if s.Snapshot().DBReaders.Value != 1 || s.Snapshot().DBWriters.Value != 1 {
				t.Fatalf("database goroutines %+v", s.Snapshot())
			}
			_ = db.Close()
			synctest.Wait()
			if got := kinds(core.events()); len(got) != 2 || got[0] != "db-opened" || got[1] != "db-closed" {
				t.Fatalf("events %v", got)
			}
			wantGoroutines(t, s, engineAndAccept)
			closeShell(t, s)
		})
	})
	t.Run("read deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, core := newPipeListener(), newCore()
			s := startShell(t, core, testConfig(l))
			c, err := l.Dial()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			backstop := proxy.DefaultClientIdle + proxy.DefaultStatement
			time.Sleep(backstop - time.Second)
			synctest.Wait()
			if len(core.of(proxy.EvClientClosed)) != 0 {
				t.Fatal("closed before the read backstop")
			}
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if len(core.of(proxy.EvClientClosed)) != 1 || s.Snapshot().ReadDeadlines != 1 {
				t.Fatalf("events %v, snapshot %+v", kinds(core.events()), s.Snapshot())
			}
			wantGoroutines(t, s, engineAndAccept)
			closeShell(t, s)
		})
	})
	t.Run("dial deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, core := newPipeListener(), newCore()
			core.onStart = func(proxy.Instant) []proxy.Action { return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 1}} }
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
			time.Sleep(proxy.DefaultDBConnect)
			synctest.Wait()
			failed := core.of(proxy.EvDBDialFailed)
			if len(failed) != 1 || time.Duration(failed[0].at) < proxy.DefaultDBConnect {
				t.Fatalf("dial failures %+v", failed)
			}
			wantGoroutines(t, s, engineAndAccept)
			closeShell(t, s)
		})
	})
	t.Run("dial refused", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, core := newPipeListener(), newCore()
			core.onStart = func(proxy.Instant) []proxy.Action { return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 1}} }
			cfg := testConfig(l)
			cfg.Dial = func(context.Context, string) (net.Conn, error) { return nil, errRefused }
			s := startShell(t, core, cfg)
			synctest.Wait()
			if len(core.of(proxy.EvDBDialFailed)) != 1 {
				t.Fatalf("events %v", kinds(core.events()))
			}
			wantGoroutines(t, s, engineAndAccept)
			closeShell(t, s)
		})
	})
	t.Run("engine closes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, core := newPipeListener(), newCore()
			core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
				if ev.Kind == proxy.EvClientData {
					return []proxy.Action{writeAct(proxy.ActClientWrite, ev.Conn, []byte("bye")), {Kind: proxy.ActClientClose, Conn: ev.Conn}}
				}
				return nil
			}
			s := startShell(t, core, testConfig(l))
			c, err := l.Dial()
			if err != nil {
				t.Fatal(err)
			}
			go c.Write(sslRequest())
			got, err := io.ReadAll(c)
			if err != nil || string(got) != "bye" {
				t.Fatalf("read %q %v: the queued write must be flushed before the close", got, err)
			}
			synctest.Wait()
			if len(core.of(proxy.EvClientClosed)) != 0 {
				t.Fatal("EvClientClosed posted after ActClientClose")
			}
			if len(core.of(proxy.EvClientWrote)) != 0 {
				t.Fatal("an event for a connection the engine closed reached the engine")
			}
			wantGoroutines(t, s, engineAndAccept)
			closeShell(t, s)
		})
	})
	t.Run("listener closed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, core := newPipeListener(), newCore()
			s := startShell(t, core, testConfig(l))
			_ = l.Close()
			synctest.Wait()
			select {
			case <-s.ListenerLost():
			default:
				t.Fatal("listener loss not reported")
			}
			if h := s.Health(); h.State != 2 {
				t.Fatalf("health %+v", h)
			}
			wantGoroutines(t, s, 1)
			closeShell(t, s)
		})
	})
	t.Run("shutdown with everything busy", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, dbl, core := newPipeListener(), newPipeListener(), newCore()
			core.onStart = func(proxy.Instant) []proxy.Action {
				return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 1}, {Kind: proxy.ActDBOpen, Conn: 2, TLSFirst: true}, {Kind: proxy.ActWake, At: proxy.Instant(time.Hour)}}
			}
			core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
				if ev.Kind == proxy.EvClientData {
					return []proxy.Action{act(proxy.ActClientResume, ev.Conn), writeAct(proxy.ActClientWrite, ev.Conn, make([]byte, 1<<20))}
				}
				return nil
			}
			cfg := testConfig(l)
			cfg.Dial = dbl.DialContext
			s := startShell(t, core, cfg)
			for range 2 {
				db, err := dbl.Accept()
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			for range 3 {
				c, err := l.Dial()
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if _, err := c.Write(sslRequest()); err != nil {
					t.Fatal(err)
				}
			}
			synctest.Wait()
			snap := s.Snapshot()
			if snap.ClientReaders.Value != 3 || snap.ClientWriters.Value != 3 || snap.DBReaders.Value != 2 || snap.DBWriters.Value != 2 || snap.Accepted != 3 {
				t.Fatalf("before shutdown %+v", snap)
			}
			closeShell(t, s)
		})
	})
}

func TestMailboxNeverExceedsItsCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const capacity, clients = 4, 24
		l, core := newPipeListener(), newCore()
		gate := make(chan struct{})
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			<-gate
			return nil
		}
		cfg := testConfig(l)
		cfg.MailboxCap = capacity
		s := startShell(t, core, cfg)
		conns := make(chan net.Conn, clients)
		for range clients {
			go func() {
				c, err := l.Dial()
				if err != nil {
					return
				}
				conns <- c
				_, _ = c.Write(sslRequest())
			}()
		}
		synctest.Wait()
		snap := s.Snapshot()
		if snap.MailboxCap != capacity || snap.MailboxDepth.Value != capacity || snap.MailboxFull == 0 {
			t.Fatalf("under the flood %+v", snap)
		}
		close(gate)
		synctest.Wait()
		snap = s.Snapshot()
		if snap.MailboxDepth.Peak > capacity || snap.MailboxDepth.Value != 0 {
			t.Fatalf("high-water %d above cap %d (%+v)", snap.MailboxDepth.Peak, capacity, snap)
		}
		if a, d := len(core.of(proxy.EvClientAccepted)), len(core.of(proxy.EvClientData)); a != clients || d != clients {
			t.Fatalf("%d accepted, %d data events, want %d each", a, d, clients)
		}
		closeShell(t, s)
		for range clients {
			(<-conns).Close()
		}
	})
}

func TestExactlyEightBytesAreReadBeforeTheDecision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStep = func(now proxy.Instant, ev proxy.Event) []proxy.Action {
			switch ev.Kind {
			case proxy.EvClientData:
				if len(core.of(proxy.EvClientData)) == 1 {
					return []proxy.Action{{Kind: proxy.ActWake, At: now + proxy.Instant(time.Second)}}
				}
			case proxy.EvTimer:
				return []proxy.Action{act(proxy.ActClientResume, 1)}
			}
			return nil
		}
		s := startShell(t, core, testConfig(l))
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		rest := startupMessage("alice", "app")
		go c.Write(append(sslRequest(), rest...))
		synctest.Wait()
		data := core.of(proxy.EvClientData)
		if len(data) != 1 || !bytes.Equal(data[0].ev.Data, sslRequest()) {
			t.Fatalf("before the decision: data events %+v", data)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		var after []byte
		for _, d := range core.of(proxy.EvClientData)[1:] {
			after = append(after, d.ev.Data...)
		}
		if !bytes.Equal(after, rest) {
			t.Fatalf("after the resume: %q, want %q", after, rest)
		}
		closeShell(t, s)
	})
}

func TestClientStalledMidHandshakeIsClosedAtTheStartupDeadline(t *testing.T) {
	ca := certtest.NewCA(t, "shell test CA")
	leaf := ca.Issue(t, "localhost")
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStep = tlsDecider
		cfg := testConfig(l)
		cfg.ClientTLS = &tls.Config{Certificates: []tls.Certificate{leaf.TLS}, MinVersion: tls.VersionTLS12}
		s := startShell(t, core, cfg)
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write(sslRequest()); err != nil {
			t.Fatal(err)
		}
		answer := make([]byte, 1)
		if _, err := io.ReadFull(c, answer); err != nil || answer[0] != 'S' {
			t.Fatalf("answer %q %v", answer, err)
		}
		time.Sleep(proxy.DefaultClientStartup - time.Millisecond)
		synctest.Wait()
		if len(core.of(proxy.EvClientTLS)) != 0 {
			t.Fatal("handshake ended before the startup deadline")
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		res := core.of(proxy.EvClientTLS)
		if len(res) != 1 || handshakeOf(res[0]).Outcome != pgwire.HandshakeDeadline {
			t.Fatalf("handshake events %+v", res)
		}
		if at := time.Duration(res[0].at); at < proxy.DefaultClientStartup || at > proxy.DefaultClientStartup+time.Millisecond {
			t.Fatalf("handshake ended at %s, want the startup deadline %s", at, proxy.DefaultClientStartup)
		}
		if n, err := c.Read(answer); n != 0 || err == nil {
			t.Fatalf("connection still open: %d %v", n, err)
		}
		if s.Snapshot().HandshakesFailed != 1 {
			t.Fatalf("snapshot %+v", s.Snapshot())
		}
		wantGoroutines(t, s, engineAndAccept)
		closeShell(t, s)
	})
}

func tlsDecider(_ proxy.Instant, ev proxy.Event) []proxy.Action {
	switch {
	case ev.Kind == proxy.EvClientData && len(ev.Data) == proxy.FirstClientRead && bytes.Equal(ev.Data, sslRequest()):
		return []proxy.Action{writeAct(proxy.ActClientWrite, ev.Conn, []byte{'S'}), act(proxy.ActClientStartTLS, ev.Conn)}
	case ev.Kind == proxy.EvClientTLS && ev.TLS.Outcome != pgwire.HandshakeCompleted:
		return []proxy.Action{{Kind: proxy.ActClientClose, Conn: ev.Conn, Reason: proxy.CloseClientStartup}}
	case ev.Kind == proxy.EvClientTLS:
		return []proxy.Action{act(proxy.ActClientResume, ev.Conn)}
	case ev.Kind == proxy.EvClientData:
		return []proxy.Action{writeAct(proxy.ActClientWrite, ev.Conn, append([]byte("echo:"), ev.Data...))}
	}
	return nil
}

func TestStuckWriterIsBoundedByTheWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind == proxy.EvClientData {
				return []proxy.Action{act(proxy.ActClientResume, ev.Conn), writeAct(proxy.ActClientWrite, ev.Conn, make([]byte, 1<<20))}
			}
			return nil
		}
		s := startShell(t, core, testConfig(l))
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write(sslRequest()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(proxy.DefaultClientWrite - time.Millisecond)
		synctest.Wait()
		if s.Snapshot().WriteDeadlines != 0 || len(core.of(proxy.EvClientClosed)) != 0 {
			t.Fatal("closed before the write deadline")
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		closed := core.of(proxy.EvClientClosed)
		if s.Snapshot().WriteDeadlines != 1 || len(closed) != 1 {
			t.Fatalf("after the write deadline: %+v, events %v", s.Snapshot(), kinds(core.events()))
		}
		wantGoroutines(t, s, engineAndAccept)
		closeShell(t, s)
	})
}

func TestWriteQueueOverflowClosesTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind != proxy.EvClientData {
				return nil
			}
			out := []proxy.Action{act(proxy.ActClientResume, ev.Conn)}
			for range maxQueueItems + 2 {
				out = append(out, writeAct(proxy.ActClientWrite, ev.Conn, []byte{'x'}))
			}
			return out
		}
		s := startShell(t, core, testConfig(l))
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write(sslRequest()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if s.Extra().QueueOverflows != 1 || len(core.of(proxy.EvClientClosed)) != 1 {
			t.Fatalf("extra %+v events %v", s.Extra(), kinds(core.events()))
		}
		wantGoroutines(t, s, engineAndAccept)
		closeShell(t, s)
	})
}

func TestWakeTimerIsArmedRearmedAndStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStart = func(now proxy.Instant) []proxy.Action {
			return []proxy.Action{{Kind: proxy.ActWake, At: now + proxy.Instant(5*time.Second)}, {Kind: proxy.ActWake, At: now + proxy.Instant(time.Second)}}
		}
		fired := 0
		core.onStep = func(now proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind != proxy.EvTimer {
				return nil
			}
			fired++
			if fired == 1 {
				return []proxy.Action{{Kind: proxy.ActWake, At: now + proxy.Instant(time.Second)}, {Kind: proxy.ActWake}}
			}
			return nil
		}
		cfg := testConfig(l)
		cfg.PublishInterval = time.Hour
		s := startShell(t, core, cfg)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		timers := core.of(proxy.EvTimer)
		if len(timers) != 1 || time.Duration(timers[0].at) != time.Second {
			t.Fatalf("timer events %+v", timers)
		}
		closeShell(t, s)
	})
}

func TestPublishTickerDrivesThePublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind == proxy.EvPublish {
				return []proxy.Action{{Kind: proxy.ActPublish}}
			}
			return nil
		}
		s := startShell(t, core, testConfig(l))
		synctest.Wait()
		first := s.pub.Load().seq
		time.Sleep(3*time.Second + time.Millisecond)
		synctest.Wait()
		if n := len(core.of(proxy.EvPublish)); n != 3 {
			t.Fatalf("%d publish events in 3 s, want 3", n)
		}
		if got := s.pub.Load(); got == nil || got.stats == nil || got.metrics == nil {
			t.Fatal("nothing published")
		}
		closeShell(t, s)
		if first != 1 || s.collector.Seq() != first+3+1 {
			t.Fatalf("publish sequence %d then %d, want 1 then 5 (start, three ticks, shutdown)", first, s.collector.Seq())
		}
	})
}
