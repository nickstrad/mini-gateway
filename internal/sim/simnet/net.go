// Package simnet simulates ordered, full-duplex byte-stream connections on top
// of internal/sim. Connections model per-direction latency and jitter,
// configurable delivery chunking down to one byte, a bounded send window with
// event-driven back pressure, half-close, reset, stalls, half-open failures,
// and listener faults.
package simnet

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

var (
	kindDial     = sim.K("net.dial")
	kindAccept   = sim.K("net.acc")
	kindRefuse   = sim.K("net.ref")
	kindConnect  = sim.K("net.conn")
	kindWrite    = sim.K("net.wr")
	kindDeliver  = sim.K("net.dlv")
	kindRead     = sim.K("net.rd")
	kindFin      = sim.K("net.fin")
	kindReset    = sim.K("net.rst")
	kindClose    = sim.K("net.cls")
	kindDiscard  = sim.K("net.disc")
	kindNoTarget = sim.K("net.nota")
)

// Listener represents a named endpoint accepting simulated connections. Its
// numeric Index is used as the target of listener-level faults.
type Listener struct {
	net       *Net
	name      string
	idx       int64
	accept    func(e *End)
	closed    bool
	refuse    bool
	dialStall bool
	silence   bool
}

// Name returns the endpoint address registered with Net.Listen.
func (l *Listener) Name() string { return l.name }

// Index returns the zero-based listener index used by Fault.Target.
func (l *Listener) Index() int64 { return l.idx }

// Close marks the listener closed so later dials to its name are refused and
// later faults targeting its index are counted as NoTarget.
func (l *Listener) Close() {
	l.closed = true
}

// Net owns all simulated listeners and connections attached to one *sim.Sim.
type Net struct {
	s         *sim.Sim
	listeners []*Listener
	conns     []*Conn
	stats     stats
}

type stats struct {
	dialsAttempted int64
	dialsRefused   int64
	dialsStalled   int64
	opened         int64
	open           int64
	connecting     int64
	closed         [ReasonAborted + 1]int64
	dir            [2]DirSnapshot
	faults         FaultSnapshot
}

// New attaches a simulated network to s, registering its byte-conservation
// invariant, quiesce check, state-digest writer, and fault kinds 1..9.
func New(s *sim.Sim) *Net {
	n := &Net{s: s}
	_ = s.RegisterQuiesce("simnet", n.quiesce)
	_ = s.RegisterInvariant("simnet.conservation", n.conservation)
	_ = s.RegisterState("simnet", n.writeState)
	n.registerFaults()
	return n
}

// Sim returns the underlying simulator kernel.
func (n *Net) Sim() *sim.Sim { return n.s }

// Listen registers an active listener at name and invokes accept with each new
// server End once the client's dial arrives after up latency.
func (n *Net) Listen(name string, accept func(e *End)) (*Listener, error) {
	if l := n.lookup(name); l != nil {
		return nil, fmt.Errorf("%w: %s", ErrAddrInUse, name)
	}
	if len(n.listeners) >= MaxListeners {
		return nil, ErrTooManyListen
	}
	l := &Listener{net: n, name: name, idx: int64(len(n.listeners)), accept: accept}
	n.listeners = append(n.listeners, l)
	return l, nil
}

func (n *Net) lookup(name string) *Listener {
	for _, l := range n.listeners {
		if l.name == name && !l.closed {
			return l
		}
	}
	return nil
}

func (n *Net) listener(idx int64) *Listener {
	if idx < 0 || idx >= int64(len(n.listeners)) {
		return nil
	}
	return n.listeners[idx]
}

func (n *Net) conn(id int64) *Conn {
	if id < 1 || id > int64(len(n.conns)) {
		return nil
	}
	return n.conns[id-1]
}

// Conn looks up a connection by its 1-based ID, returning nil if out of range.
func (n *Net) Conn(id int64) *Conn { return n.conn(id) }

// Dial opens a connection from a new client End to the named listener using
// opts and h. Completion arrives asynchronously via EvConnected or EvRefused.
func (n *Net) Dial(to string, opts Options, h Handler) *End {
	opts = opts.normalized()
	id := int64(len(n.conns)) + 1
	c := &Conn{net: n, id: id, opts: opts}
	c.ends[client] = End{c: c, side: client, h: h, state: stConnecting}
	c.ends[server] = End{c: c, side: server, state: stConnecting}
	for d := range c.pipes {
		c.pipes[d].init(c, d)
	}
	for i := range c.ends {
		c.ends[i].note.e = &c.ends[i]
	}
	if len(n.conns) >= MaxConns {
		n.s.Fail(fmt.Errorf("%w: more than %d", ErrTooManyConns, MaxConns))
		c.ends[client].state = stRefused
		return &c.ends[client]
	}
	n.conns = append(n.conns, c)
	n.stats.dialsAttempted++
	n.stats.connecting++
	l := n.lookup(to)
	lidx := int64(-1)
	if l != nil {
		lidx = l.idx
	}
	n.s.Trace(kindDial, id, lidx, 0, 0)
	switch {
	case l == nil || l.refuse:
		// Saturate the round trip so extreme latencies refuse at MaxTime instead of wrapping.
		at := sim.AddDurations(c.pipes[up].delay(), c.pipes[down].delay())
		n.s.After(at, func() { c.refuse() })
	case l.dialStall:
		c.dialStalled = true
		n.stats.dialsStalled++
	default:
		c.listener = l
		c.silent = l.silence
		n.s.After(c.pipes[up].delay(), func() { c.accept() })
	}
	return &c.ends[client]
}

func (n *Net) quiesce() error {
	st := &n.stats
	var errs []error
	if st.open != 0 || st.connecting != 0 {
		errs = append(errs, fmt.Errorf("%d connections open, %d connecting", st.open, st.connecting))
	}
	if st.dir[up].InFlight != 0 || st.dir[down].InFlight != 0 {
		errs = append(errs, fmt.Errorf("bytes in flight: up %d, down %d", st.dir[up].InFlight, st.dir[down].InFlight))
	}
	return errors.Join(errs...)
}

func (n *Net) conservation() error {
	for d, ds := range n.stats.dir {
		if ds.Written != ds.Delivered+ds.Discarded+ds.InFlight {
			return fmt.Errorf("direction %s: written %d != delivered %d + discarded %d + in flight %d",
				dirName(d), ds.Written, ds.Delivered, ds.Discarded, ds.InFlight)
		}
		if ds.InFlight < 0 {
			return fmt.Errorf("direction %s: negative in flight %d", dirName(d), ds.InFlight)
		}
	}
	return nil
}

func dirName(d int) string {
	if d == up {
		return "up"
	}
	return "down"
}

func (n *Net) writeState(w sim.StateSink) {
	st := &n.stats
	w.Int(st.dialsAttempted, st.dialsRefused, st.dialsStalled, st.opened, st.open, st.connecting)
	w.Int(st.closed[:]...)
	// Fault counters are intentionally omitted so a fault injected and cleared
	// before traffic leaves StateDigest identical to a never-faulted run.
	for _, ds := range st.dir {
		w.Int(ds.Written, ds.Delivered, ds.Discarded, ds.InFlight)
	}
	for _, c := range n.conns {
		if c == nil || c.done {
			continue
		}
		w.Int(c.id, int64(c.ends[client].state), int64(c.ends[server].state), boolInt(c.silent), boolInt(c.dialStalled))
		for d := range c.pipes {
			p := &c.pipes[d]
			w.Int(p.written, p.consumed, p.discarded, int64(len(p.buf)), int64(p.ready), int64(p.held),
				boolInt(p.stalled), int64(p.override), boolInt(p.blackhole), boolInt(p.finSent), boolInt(p.finReady))
		}
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func label(id int64, d int) string {
	return "simnet/conn/" + strconv.FormatInt(id, 10) + "/" + dirName(d)
}
