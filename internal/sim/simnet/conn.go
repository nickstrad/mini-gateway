package simnet

import (
	"io"
	"math/rand/v2"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

const (
	client = 0
	server = 1
	up     = 0
	down   = 1

	finBit   = 1 << 31
	sizeMask = finBit - 1
)

type endState uint8

const (
	stConnecting endState = iota
	stOpen
	stClosed
	stReset
	stGone
	stRefused
	stSilent
)

type Conn struct {
	net         *Net
	id          int64
	opts        Options
	ends        [2]End
	pipes       [2]pipe
	listener    *Listener
	silent      bool
	dialStalled bool
	accepted    bool
	done        bool
	reason      CloseReason
}

func (c *Conn) ID() int64           { return c.id }
func (c *Conn) Client() *End        { return &c.ends[client] }
func (c *Conn) Server() *End        { return &c.ends[server] }
func (c *Conn) Done() bool          { return c.done }
func (c *Conn) Reason() CloseReason { return c.reason }

type notifier struct {
	e        *End
	pending  [EvReset + 1]bool
	disabled bool
}

func (n *notifier) OnEvent(code uint64) {
	ev := Event(code)
	n.pending[ev] = false
	e := n.e
	if e.h == nil || !e.deliverable(ev) {
		return
	}
	e.h.OnEvent(e, ev)
}

type End struct {
	c          *Conn
	side       int
	h          Handler
	state      endState
	readClosed bool
	note       notifier
}

func (e *End) ID() int64            { return e.c.id }
func (e *End) Conn() *Conn          { return e.c }
func (e *End) IsClient() bool       { return e.side == client }
func (e *End) SetHandler(h Handler) { e.h = h }

func (e *End) in() *pipe {
	if e.side == client {
		return &e.c.pipes[down]
	}
	return &e.c.pipes[up]
}

func (e *End) out() *pipe {
	if e.side == client {
		return &e.c.pipes[up]
	}
	return &e.c.pipes[down]
}

func (e *End) peer() *End { return &e.c.ends[1-e.side] }

func (e *End) deliverable(ev Event) bool {
	switch e.state {
	case stOpen:
		return ev != EvRefused && ev != EvReset
	case stReset:
		return ev == EvReset
	case stRefused:
		return ev == EvRefused
	}
	return false
}

func (e *End) notify(ev Event) {
	if e.note.pending[ev] || e.state == stGone || e.state == stSilent {
		return
	}
	e.note.pending[ev] = true
	e.c.net.s.AtHandler(e.c.net.s.Now(), &e.note, uint64(ev))
}

func (e *End) err() error {
	switch e.state {
	case stConnecting:
		return ErrNotConnected
	case stReset:
		return ErrReset
	case stRefused:
		return ErrRefused
	case stGone, stSilent:
		return ErrGone
	case stClosed:
		return ErrClosed
	}
	return nil
}

func (e *End) Buffered() int { return e.in().ready }

func (e *End) Space() int {
	p := e.out()
	return p.window - len(p.buf)
}

func (e *End) WaitWritable() {
	if e.state == stOpen {
		e.out().blocked = true
	}
}

func (e *End) Read(b []byte) (int, error) {
	if err := e.err(); err != nil {
		return 0, err
	}
	if e.readClosed {
		return 0, ErrClosed
	}
	p := e.in()
	if p.ready == 0 {
		if p.finReady {
			return 0, io.EOF
		}
		return 0, nil
	}
	n := copy(b, p.buf[:p.ready])
	p.consume(n)
	return n, nil
}

func (e *End) Write(b []byte) (int, error) {
	if err := e.err(); err != nil {
		return 0, err
	}
	p := e.out()
	if p.finSent {
		return 0, ErrClosed
	}
	return p.write(b), nil
}

func (e *End) CloseWrite() error {
	if err := e.err(); err != nil {
		return err
	}
	e.out().sendFin()
	return nil
}

func (e *End) Close() error {
	switch e.state {
	case stConnecting:
		e.c.abort()
		return nil
	case stOpen:
	default:
		return e.err()
	}
	e.out().sendFin()
	e.readClosed = true
	e.state = stClosed
	e.in().discardAll()
	e.in().epoch++
	e.c.maybeFinish()
	return nil
}

func (e *End) Reset() error {
	switch e.state {
	case stConnecting:
		e.c.abort()
		return nil
	case stOpen:
	default:
		return e.err()
	}
	e.c.reset(e.side)
	return nil
}

func (c *Conn) refuse() {
	if c.done {
		return
	}
	n := c.net
	n.stats.dialsRefused++
	n.stats.connecting--
	c.done = true
	c.ends[client].state = stRefused
	c.ends[server].state = stClosed
	n.s.Trace(kindRefuse, c.id, 0, 0, 0)
	c.ends[client].notify(EvRefused)
}

func (c *Conn) accept() {
	if c.done {
		return
	}
	n := c.net
	c.accepted = true
	n.stats.opened++
	n.stats.open++
	n.s.Trace(kindAccept, c.id, c.listener.idx, boolInt(c.silent), 0)
	srv := &c.ends[server]
	if c.silent {
		srv.state = stSilent
	} else {
		srv.state = stOpen
		if c.listener.accept != nil {
			c.listener.accept(srv)
		}
	}
	n.s.After(c.pipes[down].delay(), func() { c.connected() })
}

func (c *Conn) connected() {
	cl := &c.ends[client]
	if c.done || cl.state != stConnecting {
		return
	}
	c.net.stats.connecting--
	cl.state = stOpen
	c.net.s.Trace(kindConnect, c.id, 0, 0, 0)
	cl.notify(EvConnected)
	p := cl.in()
	if p.ready > 0 || p.finReady {
		cl.notify(EvReadable)
	}
}

func (c *Conn) abort() {
	if c.done {
		return
	}
	n := c.net
	n.stats.connecting--
	if c.accepted {
		n.stats.open--
	}
	c.finish(ReasonAborted)
	c.ends[client].state = stClosed
	if c.ends[server].state == stOpen {
		c.ends[server].state = stReset
		c.ends[server].notify(EvReset)
	}
}

func (c *Conn) reset(by int) {
	if c.done {
		return
	}
	c.net.stats.open--
	if c.ends[client].state == stConnecting {
		c.net.stats.connecting--
		c.ends[client].state = stOpen
	}
	c.finish(ReasonReset)
	c.net.s.Trace(kindReset, c.id, int64(by), 0, 0)
	for side := client; side <= server; side++ {
		e := &c.ends[side]
		if e.state != stOpen {
			continue
		}
		e.state = stReset
		if side != by {
			e.notify(EvReset)
		}
	}
}

func (c *Conn) maybeFinish() {
	if c.done {
		return
	}
	cl, sv := c.ends[client].state, c.ends[server].state
	gone := func(s endState) bool { return s == stGone || s == stSilent }
	switch {
	case cl == stClosed && sv == stClosed:
		c.net.stats.open--
		c.finish(ReasonGraceful)
	case cl == stClosed && gone(sv) || sv == stClosed && gone(cl):
		reason := ReasonHalfOpen
		if c.silent {
			reason = ReasonSilent
		}
		c.net.stats.open--
		c.finish(reason)
	}
}

func (c *Conn) finish(reason CloseReason) {
	c.done = true
	c.reason = reason
	c.net.stats.closed[reason]++
	c.pipes[up].discardAll()
	c.pipes[down].discardAll()
	c.pipes[up].stop()
	c.pipes[down].stop()
	c.net.s.Trace(kindClose, c.id, int64(reason), 0, 0)
}

type pipe struct {
	c         *Conn
	dir       int
	opts      Direction
	window    int
	rng       *rand.Rand
	buf       []byte
	ready     int
	held      int
	lastAt    sim.Time
	epoch     uint64
	override  int
	stalled   bool
	stallT    sim.Timer
	blackhole bool
	blocked   bool
	finSent   bool
	finHeld   bool
	finReady  bool
	written   int64
	consumed  int64
	discarded int64
}

func (p *pipe) init(c *Conn, d int) {
	p.c = c
	p.dir = d
	p.window = c.opts.Window
	if d == up {
		p.opts = c.opts.Up
	} else {
		p.opts = c.opts.Down
	}
	p.rng = c.net.s.Stream(label(c.id, d))
}

func (p *pipe) stats() *DirSnapshot { return &p.c.net.stats.dir[p.dir] }

func (p *pipe) receiver() *End { return &p.c.ends[1-p.dir] }

func (p *pipe) sender() *End { return &p.c.ends[p.dir] }

func (p *pipe) delay() time.Duration {
	l := p.opts.Latency
	if l.Jitter <= 0 {
		return l.Base
	}
	return l.Base + time.Duration(p.rng.Int64N(int64(l.Jitter)+1))
}

func (p *pipe) arrival() sim.Time {
	at := p.c.net.s.Now().Add(p.delay())
	if at < p.lastAt {
		at = p.lastAt
	}
	p.lastAt = at
	return at
}

func (p *pipe) pieceSize(remaining int) int {
	if p.override > 0 {
		return min(p.override, remaining)
	}
	switch p.opts.Chunking {
	case ChunkFixed:
		return min(p.opts.ChunkSize, remaining)
	case ChunkRandom:
		lo := (remaining + MaxRandomPieces - 1) / MaxRandomPieces
		return lo + p.rng.IntN(remaining-lo+1)
	}
	return remaining
}

func (p *pipe) write(b []byte) int {
	space := p.window - len(p.buf)
	n := min(len(b), space)
	st := p.stats()
	if n < len(b) {
		p.blocked = true
		st.WritesRefused++
	}
	if n == 0 {
		return 0
	}
	p.buf = append(p.buf, b[:n]...)
	p.written += int64(n)
	st.Written += int64(n)
	st.InFlight += int64(n)
	s := p.c.net.s
	s.Trace(kindWrite, p.c.id, int64(p.dir), int64(n), p.written)
	for rem := n; rem > 0; {
		k := p.pieceSize(rem)
		rem -= k
		st.Chunks++
		s.AtHandler(p.arrival(), p, p.epoch<<32|uint64(k))
	}
	return n
}

func (p *pipe) sendFin() {
	if p.finSent {
		return
	}
	p.finSent = true
	p.c.net.s.AtHandler(p.arrival(), p, p.epoch<<32|finBit)
}

func (p *pipe) OnEvent(code uint64) {
	if code>>32 != p.epoch || p.c.done {
		return
	}
	n := int(code & sizeMask)
	fin := code&finBit != 0
	if p.stalled || p.blackhole {
		p.held += n
		p.finHeld = p.finHeld || fin
		return
	}
	p.arrive(n, fin)
}

func (p *pipe) arrive(n int, fin bool) {
	s := p.c.net.s
	r := p.receiver()
	if n > 0 {
		s.Trace(kindDeliver, p.c.id, int64(p.dir), int64(n), 0)
	}
	if fin {
		s.Trace(kindFin, p.c.id, int64(p.dir), 0, 0)
	}
	if r.readClosed || r.state == stClosed {
		p.discardFront(n)
		p.finReady = p.finReady || fin
		return
	}
	p.ready += n
	p.finReady = p.finReady || fin
	if r.state == stOpen {
		r.notify(EvReadable)
	}
}

func (p *pipe) release() {
	if p.blackhole || (p.held == 0 && !p.finHeld) {
		return
	}
	n, fin := p.held, p.finHeld
	p.held, p.finHeld = 0, false
	p.arrive(n, fin)
}

func (p *pipe) consume(n int) {
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	p.ready -= n
	p.consumed += int64(n)
	st := p.stats()
	st.Delivered += int64(n)
	st.InFlight -= int64(n)
	p.c.net.s.Trace(kindRead, p.c.id, int64(p.dir), int64(n), p.consumed)
	p.unblock()
}

func (p *pipe) unblock() {
	if p.blocked && len(p.buf) < p.window {
		p.blocked = false
		if w := p.sender(); w.state == stOpen && !p.finSent {
			w.notify(EvWritable)
		}
	}
}

func (p *pipe) discardFront(n int) {
	if n == 0 {
		return
	}
	p.buf = p.buf[n:]
	p.account(n)
	p.unblock()
}

func (p *pipe) discardAll() {
	n := len(p.buf)
	p.buf = nil
	p.ready, p.held = 0, 0
	p.account(n)
}

func (p *pipe) account(n int) {
	if n == 0 {
		return
	}
	p.discarded += int64(n)
	st := p.stats()
	st.Discarded += int64(n)
	st.InFlight -= int64(n)
	p.c.net.s.Trace(kindDiscard, p.c.id, int64(p.dir), int64(n), 0)
}

func (p *pipe) stop() {
	p.epoch++
	p.stalled = false
	p.stallT.Cancel()
	p.blocked = false
}
