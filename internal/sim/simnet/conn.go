package simnet

import (
	"io"
	"math"
	"math/rand/v2"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

const (
	client = 0
	server = 1
	up     = 0
	down   = 1

	// Delivery event codes pack a 32-bit epoch in the upper half and either
	// finBit or a byte length (<= MaxWindow < 2^31) in the lower half.
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

// Conn represents one simulated connection consisting of a client End, a
// server End, and two unidirectional pipes (up and down).
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

// ID returns the connection's 1-based identifier, also used as Fault.Target.
func (c *Conn) ID() int64 { return c.id }

// Client returns the dialing endpoint of the connection.
func (c *Conn) Client() *End { return &c.ends[client] }

// Server returns the accepted endpoint of the connection.
func (c *Conn) Server() *End { return &c.ends[server] }

// Done reports whether the connection has reached a terminal closed state.
func (c *Conn) Done() bool { return c.done }

// Reason returns why the connection closed, or ReasonNone if still active.
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

// End is one side (client or server) of a simulated Conn.
type End struct {
	c          *Conn
	side       int
	h          Handler
	state      endState
	readClosed bool
	note       notifier
}

// ID returns the owning connection's 1-based ID.
func (e *End) ID() int64 { return e.c.id }

// Conn returns the owning connection.
func (e *End) Conn() *Conn { return e.c }

// IsClient reports whether e is the dialing side of the connection.
func (e *End) IsClient() bool { return e.side == client }

// SetHandler installs or replaces the Handler that receives events for e.
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
	// Coalesce duplicate notifications of the same event kind already queued.
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

// Buffered returns the number of delivered incoming bytes currently ready to Read.
func (e *End) Buffered() int { return e.in().ready }

// Window returns the configured send window capacity in bytes for outgoing writes.
func (e *End) Window() int { return e.out().window }

// Space returns how many additional bytes Write can currently accept before
// filling the outgoing send window.
func (e *End) Space() int {
	p := e.out()
	return p.window - len(p.buf)
}

// WaitWritable arms a single EvWritable notification once outgoing window space
// is freed by the peer consuming or discarding bytes.
func (e *End) WaitWritable() {
	if e.state == stOpen {
		e.out().blocked = true
	}
}

// Read copies up to len(b) delivered bytes from the incoming pipe. It returns
// (0, nil) when no bytes are ready yet and (0, io.EOF) once the peer's FIN has
// arrived and all preceding bytes have been read.
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

// Write copies up to Space() bytes of b into the outgoing pipe and schedules
// their chunked arrival on the virtual clock. A short write (including 0)
// counts as a refused write and arms EvWritable.
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

// CloseWrite half-closes the outgoing pipe by queuing a FIN behind any pending
// bytes; e can continue reading until Close or Reset.
func (e *End) CloseWrite() error {
	if err := e.err(); err != nil {
		return err
	}
	e.out().sendFin()
	return nil
}

// Close sends a FIN if not yet sent, stops reading, discards any buffered or
// future incoming bytes, and finishes the connection once both sides are done.
// Calling Close on a connecting client end aborts the dial.
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
	// Discarding buffered bytes frees the peer's send window; wake a blocked writer
	// before advancing the epoch so it does not hang with an empty pipe.
	e.in().unblock()
	e.in().epoch++
	e.c.maybeFinish()
	return nil
}

// Reset tears down the connection immediately, discarding all bytes in flight
// in both directions and notifying only the peer with EvReset.
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
	// If the server wrote immediately on accept, queue EvReadable right after
	// EvConnected so the client never observes bytes before learning it connected.
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
	// Notify client before server so injected resets (by == -1) have a fixed,
	// deterministic notification order at the fault instant.
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
	case cl == stClosed && gone(sv) || sv == stClosed && gone(cl) || gone(cl) && gone(sv):
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
	c      *Conn
	dir    int
	opts   Direction
	window int
	rng    *rand.Rand
	// buf holds all in-flight bytes in FIFO order: buf[:ready] are delivered
	// and unread; the remainder are either held by a stall or still travelling.
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
	return sim.AddDurations(l.Base, p.jitter(l.Jitter))
}

func (p *pipe) jitter(bound time.Duration) time.Duration {
	// Int64N(bound+1) would overflow and panic when bound == math.MaxInt64.
	if bound == math.MaxInt64 {
		return time.Duration(p.rng.Int64())
	}
	return time.Duration(p.rng.Int64N(int64(bound) + 1))
}

func (p *pipe) arrival() sim.Time {
	// Clamp to lastAt so random jitter can delay a later piece but never
	// reorder it ahead of an earlier piece on the same byte stream.
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
	// Drop stale delivery events scheduled before a Close or Reset bumped epoch.
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
