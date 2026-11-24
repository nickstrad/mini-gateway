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

func (l *Listener) Name() string { return l.name }
func (l *Listener) Index() int64 { return l.idx }

func (l *Listener) Close() {
	l.closed = true
}

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

func New(s *sim.Sim) *Net {
	n := &Net{s: s}
	_ = s.RegisterQuiesce("simnet", n.quiesce)
	_ = s.RegisterInvariant("simnet.conservation", n.conservation)
	_ = s.RegisterState("simnet", n.writeState)
	n.registerFaults()
	return n
}

func (n *Net) Sim() *sim.Sim { return n.s }

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

func (n *Net) Conn(id int64) *Conn { return n.conn(id) }

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
