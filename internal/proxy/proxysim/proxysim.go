// Package proxysim drives proxy.Engine over simnet inside the deterministic
// simulator. It obeys the same byte-stream read protocol and write-report rules
// as internal/shell, using simtls for simulated TLS on both legs.
package proxysim

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtls"
)

const (
	// DefaultAddr is the default simnet listener address for the simulated proxy.
	DefaultAddr   = "proxy"
	maxEventQueue = 1 << 20
)

var (
	kindStep  = sim.K("px.step")
	kindWrite = sim.K("px.wr")
	kindClose = sim.K("px.cls")
)

// Config configures a simulated proxy Harness on simnet.
type Config struct {
	Addr       string
	DBAddr     string
	Options    simnet.Options
	Engine     proxy.Config
	Handshake  time.Duration
	TLSVersion pgwire.TLSVersion
	Trace      bool
}

// Harness adapts proxy.Engine to simnet listeners, dials, and sim.Sim timers.
type Harness struct {
	S       *sim.Sim
	Net     *simnet.Net
	Engine  *proxy.Engine
	cfg     Config
	l       *simnet.Listener
	clients map[proxy.ConnID]*conn
	dbs     map[proxy.ConnID]*conn
	queue   []proxy.Event
	readers []*conn
	running bool
	wake    sim.Timer
	acts    []proxy.Action
	buf     [proxy.MaxRead]byte
	steps   int64
	overrun int64
}

type side uint8

const (
	sideClient side = iota
	sideDB
	sideCancel
)

const cancelWait = time.Second

type conn struct {
	h         *Harness
	id        proxy.ConnID
	side      side
	end       *simnet.End
	limit     int
	permitted bool
	closed    bool
	out       []byte
	tls       bool
	hello     []byte
	hsTimer   sim.Timer
	connected bool
	reading   bool
}

// New constructs a proxy.Engine, binds a simnet listener, registers quiesce,
// invariant, and state-digest hooks with net.Sim(), and runs Engine.Start.
func New(net *simnet.Net, cfg Config) (*Harness, error) {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Handshake <= 0 {
		cfg.Handshake = simtls.DefaultHandshake
	}
	if cfg.TLSVersion == pgwire.TLSVersionOther {
		cfg.TLSVersion = pgwire.TLSVersion13
	}
	s := net.Sim()
	rnd := s.Stream("proxysim/engine")
	ec := cfg.Engine
	ec.Client.Random = rnd
	ec.Backend.Random = rnd
	if ec.Client.Keys == nil {
		ec.Client.Keys = pgwire.NewKeyIssuer(rnd)
	}
	if len(ec.Client.MockSecret) == 0 {
		ec.Client.MockSecret = []byte("proxysim-mock-secret")
	}
	eng, err := proxy.New(ec)
	if err != nil {
		return nil, err
	}
	h := &Harness{S: s, Net: net, Engine: eng, cfg: cfg, clients: make(map[proxy.ConnID]*conn),
		dbs: make(map[proxy.ConnID]*conn)}
	l, err := net.Listen(cfg.Addr, h.accept)
	if err != nil {
		return nil, err
	}
	h.l = l
	err = errors.Join(s.RegisterQuiesce("proxy", h.quiesce), s.RegisterInvariant("proxy.engine", eng.Invariants),
		s.RegisterState("proxy", h.writeState))
	if err != nil {
		return nil, err
	}
	h.acts = eng.Start(proxy.Instant(s.Now()), h.acts[:0])
	h.execute()
	return h, nil
}

// Addr returns the simnet address the harness listens on.
func (h *Harness) Addr() string { return h.cfg.Addr }

// Close stops accepting new simulated client connections.
func (h *Harness) Close() { h.l.Close() }

// Shutdown closes the listener and executes Engine.Shutdown.
func (h *Harness) Shutdown() {
	h.l.Close()
	h.acts = h.Engine.Shutdown(proxy.Instant(h.S.Now()), h.acts[:0])
	h.execute()
}

// Finish drains the simulation until quiet, checks Engine.Quiesce, shuts the
// proxy down so the fixed set closes, drains again, and verifies all hooks.
func (h *Harness) Finish() error {
	if err := h.S.RunUntilQuiet(); err != nil {
		return err
	}
	if err := h.Engine.Quiesce(); err != nil {
		return err
	}
	h.Shutdown()
	if err := h.S.RunUntilQuiet(); err != nil {
		return err
	}
	if err := h.S.CheckQuiesce(); err != nil {
		return err
	}
	return h.S.Verify()
}

func (h *Harness) quiesce() error {
	var errs []error
	if err := h.Engine.Quiesce(); err != nil {
		errs = append(errs, err)
	}
	if n := len(h.clients); n != 0 {
		errs = append(errs, fmt.Errorf("%d client sockets open in the harness", n))
	}
	if n := len(h.dbs); n != 0 {
		errs = append(errs, fmt.Errorf("%d database sockets open in the harness", n))
	}
	if h.overrun != 0 {
		errs = append(errs, fmt.Errorf("event queue overran %d times", h.overrun))
	}
	return errors.Join(errs...)
}

func (h *Harness) writeState(w sim.StateSink) {
	s := h.Engine.Snapshot()
	w.Int(s.ClientsConnected.Value, s.ClientsAccepted, s.Received, s.Accepted, s.Completed, s.Failed, s.Shed, s.Rejected,
		s.Queued.Value, s.InFlight.Value, s.LeasesStarted, s.LeasesEnded, s.ResetsStarted, s.ResetsCompleted,
		s.DBOpen.Value, s.DBDials, s.RelayHighWater, s.Events, h.steps)
	w.Int(s.ByRow[:]...)
	w.Int(s.ResetsFailed[:]...)
	w.Int(s.DBCloses[:]...)
	w.Int(s.ClientCloses[:]...)
}

func (h *Harness) post(ev proxy.Event) {
	if len(h.queue) >= maxEventQueue {
		h.overrun++
		return
	}
	h.queue = append(h.queue, ev)
	h.drain()
}

// Post enqueues an external proxy.Event (such as EvDraining) and drains the loop.
func (h *Harness) Post(ev proxy.Event) { h.post(ev) }

// drain serialises all re-entrant events and reads so that one read chunk is
// fully processed by Engine.Step (and any resulting pause action applied) before
// the next chunk is read from simnet.
func (h *Harness) drain() {
	if h.running {
		return
	}
	h.running = true
	for {
		if len(h.queue) > 0 {
			next := h.queue[0]
			h.queue = h.queue[1:]
			h.steps++
			h.acts = h.Engine.Step(proxy.Instant(h.S.Now()), next, h.acts[:0])
			h.execute()
			continue
		}
		if len(h.readers) > 0 {
			c := h.readers[0]
			h.readers = h.readers[1:]
			c.reading = false
			if c.readChunk() {
				c.read()
			}
			continue
		}
		break
	}
	h.queue = nil
	h.readers = nil
	h.running = false
}

func (h *Harness) execute() {
	acts := h.acts
	h.acts = nil
	for _, a := range acts {
		h.do(a)
	}
}

func (h *Harness) do(a proxy.Action) {
	switch a.Kind {
	case proxy.ActWake:
		h.wake.Cancel()
		if a.At > 0 {
			h.wake = h.S.At(sim.Time(a.At), func() { h.post(proxy.Event{Kind: proxy.EvTimer}) })
		}
	case proxy.ActClientWrite:
		if c := h.clients[a.Conn]; c != nil {
			c.write(a.Data)
		}
	case proxy.ActDBWrite:
		if c := h.dbs[a.Conn]; c != nil {
			c.write(a.Data)
		}
	case proxy.ActClientClose:
		if c := h.clients[a.Conn]; c != nil {
			c.shut(int64(a.Reason))
		}
	case proxy.ActDBClose:
		if c := h.dbs[a.Conn]; c != nil {
			c.shut(int64(a.Reason))
		}
	case proxy.ActClientPause, proxy.ActDBPause:
		if c := h.lookup(a); c != nil {
			c.permitted = false
		}
	case proxy.ActClientResume, proxy.ActDBResume:
		if c := h.lookup(a); c != nil {
			c.permitted, c.limit = true, 0
			c.read()
		}
	case proxy.ActClientStartTLS:
		if c := h.clients[a.Conn]; c != nil {
			c.tls, c.permitted, c.limit = true, true, simtls.HelloLen
			c.read()
		}
	case proxy.ActDBStartTLS:
		if c := h.dbs[a.Conn]; c != nil {
			c.tls, c.permitted, c.limit = true, true, simtls.HelloLen
			c.write(simtls.AppendClientHello(nil))
			c.read()
		}
	case proxy.ActDBOpen:
		h.dial(a)
	case proxy.ActDBCancel:
		h.cancel(a)
	}
}

func (h *Harness) lookup(a proxy.Action) *conn {
	if a.Kind == proxy.ActClientPause || a.Kind == proxy.ActClientResume {
		return h.clients[a.Conn]
	}
	return h.dbs[a.Conn]
}

func (h *Harness) accept(e *simnet.End) {
	c := &conn{h: h, id: proxy.ConnID(e.ID()), side: sideClient, end: e, permitted: true, limit: proxy.FirstClientRead,
		connected: true}
	h.clients[c.id] = c
	e.SetHandler(c)
	h.post(proxy.Event{Kind: proxy.EvClientAccepted, Conn: c.id})
	c.read()
}

func (h *Harness) dial(a proxy.Action) {
	c := &conn{h: h, id: a.Conn, side: sideDB, permitted: true}
	if a.TLSFirst {
		c.limit = proxy.FirstDBTLSRead
	}
	h.dbs[c.id] = c
	c.end = h.Net.Dial(h.cfg.DBAddr, h.cfg.Options, c)
}

func (h *Harness) cancel(a proxy.Action) {
	c := &conn{h: h, id: a.Conn, side: sideCancel, permitted: true, out: append([]byte(nil), a.Data...)}
	h.dbs[c.id] = c
	c.hsTimer = h.S.After(cancelWait, c.gone)
	c.end = h.Net.Dial(h.cfg.DBAddr, h.cfg.Options, c)
}

func (c *conn) OnEvent(_ *simnet.End, ev simnet.Event) {
	if c.closed {
		return
	}
	switch ev {
	case simnet.EvConnected:
		c.connected = true
		if c.side == sideCancel {
			c.flush()
			c.read()
			return
		}
		c.h.post(proxy.Event{Kind: proxy.EvDBOpened, Conn: c.id})
		c.read()
	case simnet.EvRefused:
		c.gone()
	case simnet.EvReset:
		c.gone()
	case simnet.EvReadable:
		c.read()
	case simnet.EvWritable:
		c.flush()
	}
}

func (c *conn) gone() {
	if c.closed {
		return
	}
	c.closed = true
	c.forget()
	if c.end != nil {
		_ = c.end.Close()
	}
	// Rule G8: report unwritten buffered bytes as EvDBWriteRefused before
	// EvDBClosed so the engine knows no byte at or past the acked offset went out.
	unwritten := len(c.out) > 0
	c.out = nil
	if c.side == sideCancel {
		unwritten = false
	}
	if unwritten && c.side == sideDB && c.connected {
		c.h.post(proxy.Event{Kind: proxy.EvDBWriteRefused, Conn: c.id})
	}
	switch {
	case c.side == sideCancel:
		c.h.post(proxy.Event{Kind: proxy.EvDBCancelDone, Conn: c.id})
	case c.side == sideClient:
		c.h.post(proxy.Event{Kind: proxy.EvClientClosed, Conn: c.id})
	case !c.connected:
		c.h.post(proxy.Event{Kind: proxy.EvDBDialFailed, Conn: c.id})
	default:
		c.h.post(proxy.Event{Kind: proxy.EvDBClosed, Conn: c.id})
	}
}

func (c *conn) forget() {
	if c.side == sideClient {
		delete(c.h.clients, c.id)
	} else {
		delete(c.h.dbs, c.id)
	}
	c.hsTimer.Cancel()
}

func (c *conn) read() {
	if c.closed || c.reading {
		return
	}
	c.reading = true
	c.h.readers = append(c.h.readers, c)
	c.h.drain()
}

func (c *conn) readChunk() bool {
	if c.closed || !c.permitted || !c.connected {
		return false
	}
	want := len(c.h.buf)
	if c.limit > 0 {
		want = c.limit
	}
	n, err := c.end.Read(c.h.buf[:want])
	if n > 0 {
		if c.limit > 0 {
			c.limit -= n
			if c.limit == 0 {
				c.permitted = false
			}
		}
		if c.tls {
			c.takeHello(c.h.buf[:n])
		} else {
			c.deliver(c.h.buf[:n])
		}
		return true
	}
	if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrClosed) && !errors.Is(err, simnet.ErrNotConnected)) {
		c.gone()
	}
	return false
}

func (c *conn) deliver(b []byte) {
	if c.side == sideCancel {
		return
	}
	kind := proxy.EvClientData
	if c.side == sideDB {
		kind = proxy.EvDBData
	}
	c.h.post(proxy.Event{Kind: kind, Conn: c.id, Data: append([]byte(nil), b...)})
}

func (c *conn) takeHello(b []byte) {
	c.hello = append(c.hello, b...)
	if len(c.hello) < simtls.HelloLen {
		c.permitted = true
		c.limit = simtls.HelloLen - len(c.hello)
		return
	}
	c.tls = false
	c.permitted = false
	if c.side == sideClient {
		c.clientHandshake()
		return
	}
	c.dbHandshake()
}

func (c *conn) clientHandshake() {
	if !simtls.ValidClientHello(c.hello) {
		c.write(simtls.AppendServerReply(nil, simtls.ReplyAlert, 0))
		c.h.post(proxy.Event{Kind: proxy.EvClientTLS, Conn: c.id, TLS: pgwire.HandshakeResult{Outcome: pgwire.HandshakeViolation}})
		return
	}
	c.hsTimer = c.h.S.After(c.h.cfg.Handshake, func() {
		if c.closed {
			return
		}
		v := byte(simtls.Version13)
		if c.h.cfg.TLSVersion == pgwire.TLSVersion12 {
			v = simtls.Version12
		}
		c.write(simtls.AppendServerReply(nil, simtls.ReplyOK, v))
		c.h.post(proxy.Event{Kind: proxy.EvClientTLS, Conn: c.id, TLS: pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: c.h.cfg.TLSVersion}})
	})
}

func (c *conn) dbHandshake() {
	r, v, ok := simtls.ParseServerReply(c.hello)
	res := pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: pgwire.TLSVersion13}
	switch {
	case !ok || r == simtls.ReplyAlert:
		res = pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailed}
	case r == simtls.ReplyBadCertificate && c.h.cfg.Engine.Backend.TLS == pgwire.BackendTLSVerifyFull:
		res = pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailedVerification}
	case v == simtls.Version12:
		res.Version = pgwire.TLSVersion12
	}
	c.h.post(proxy.Event{Kind: proxy.EvDBTLS, Conn: c.id, TLS: res})
}

func (c *conn) write(b []byte) {
	if c.closed || len(b) == 0 {
		return
	}
	c.out = append(c.out, b...)
	c.flush()
}

func (c *conn) flush() {
	if c.closed || len(c.out) == 0 || !c.connected {
		return
	}
	n, err := c.end.Write(c.out)
	if n > 0 && c.side == sideCancel {
		c.out = c.out[n:]
	} else if n > 0 {
		c.out = c.out[n:]
		kind := proxy.EvClientWrote
		if c.side == sideDB {
			kind = proxy.EvDBWrote
		}
		c.h.post(proxy.Event{Kind: kind, Conn: c.id, N: n})
	}
	if err != nil {
		c.gone()
		return
	}
	if len(c.out) > 0 {
		c.end.WaitWritable()
	}
}

func (c *conn) shut(why int64) {
	if c.closed {
		return
	}
	c.flush()
	c.closed = true
	c.forget()
	c.h.S.Trace(kindClose, int64(c.id), int64(c.side), why, int64(len(c.out)))
	c.out = nil
	_ = c.end.Close()
}
