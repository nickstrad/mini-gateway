package shell

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

type stamped struct {
	at proxy.Instant
	ev proxy.Event
}

type scriptCore struct {
	mu      sync.Mutex
	log     []stamped
	bell    chan struct{}
	onStart func(now proxy.Instant) []proxy.Action
	onStep  func(now proxy.Instant, ev proxy.Event) []proxy.Action
}

func newCore() *scriptCore { return &scriptCore{bell: make(chan struct{}, 1)} }

func (c *scriptCore) Start(now proxy.Instant, out []proxy.Action) []proxy.Action {
	if c.onStart != nil {
		out = append(out, c.onStart(now)...)
	}
	return out
}

func (c *scriptCore) Step(now proxy.Instant, ev proxy.Event, out []proxy.Action) []proxy.Action {
	ev.Data = slices.Clone(ev.Data)
	c.mu.Lock()
	c.log = append(c.log, stamped{at: now, ev: ev})
	c.mu.Unlock()
	select {
	case c.bell <- struct{}{}:
	default:
	}
	if c.onStep != nil {
		out = append(out, c.onStep(now, ev)...)
	}
	return out
}

func (c *scriptCore) events() []stamped {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.log)
}

func (c *scriptCore) of(kind proxy.EventKind) []stamped {
	var out []stamped
	for _, e := range c.events() {
		if e.ev.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (c *scriptCore) waitFor(t testing.TB, what string, timeout time.Duration, ok func(c *scriptCore) bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for !ok(c) {
		select {
		case <-c.bell:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s; events: %v", what, kinds(c.events()))
		}
	}
}

func (c *scriptCore) waitKind(t testing.TB, kind proxy.EventKind, n int, timeout time.Duration) []stamped {
	t.Helper()
	c.waitFor(t, kind.String(), timeout, func(c *scriptCore) bool { return len(c.of(kind)) >= n })
	return c.of(kind)
}

func kinds(evs []stamped) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ev.Kind.String()
	}
	return out
}

func act(kind proxy.ActionKind, id proxy.ConnID) proxy.Action {
	return proxy.Action{Kind: kind, Conn: id}
}

func writeAct(kind proxy.ActionKind, id proxy.ConnID, data []byte) proxy.Action {
	return proxy.Action{Kind: kind, Conn: id, Data: data}
}

func sslRequest() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], 8)
	binary.BigEndian.PutUint32(b[4:8], 80877103)
	return b
}

func startupMessage(user, database string) []byte {
	body := []byte{0, 3, 0, 0}
	for _, kv := range [][2]string{{"user", user}, {"database", database}} {
		body = append(body, kv[0]...)
		body = append(body, 0)
		body = append(body, kv[1]...)
		body = append(body, 0)
	}
	body = append(body, 0)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	return append(out, body...)
}

type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

func (l *pipeListener) Dial() (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.conns <- b:
		return a, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.conns <- b:
		return a, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

var errRefused = errors.New("connection refused")

func testConfig(l net.Listener) Config {
	return Config{Listener: l, PublishInterval: time.Second, Timeouts: proxy.DefaultTimeouts(), DBAddress: "db:5432",
		Obs: ObsConfig{Instance: "gw-test", Build: "test", ConfigFingerprint: "0123456789abcdef"}}
}

func startShell(t testing.TB, core Core, cfg Config) *Shell {
	t.Helper()
	s, err := New(core, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	return s
}

func closeShell(t testing.TB, s *Shell) {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	snap := s.Snapshot()
	if snap.Goroutines.Value != 0 || snap.ClientReaders.Value != 0 || snap.ClientWriters.Value != 0 ||
		snap.DBReaders.Value != 0 || snap.DBWriters.Value != 0 {
		t.Fatalf("goroutines left after Close: %+v", snap)
	}
}

func wantGoroutines(t testing.TB, s *Shell, n int64) {
	t.Helper()
	if g := s.Snapshot().Goroutines.Value; g != n {
		t.Fatalf("%d shell goroutines, want %d (%+v)", g, n, s.Snapshot())
	}
}

func handshakeOf(e stamped) pgwire.HandshakeResult { return e.ev.TLS }
