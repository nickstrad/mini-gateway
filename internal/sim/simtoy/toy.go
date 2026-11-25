// Package simtoy implements a deterministic line-echo protocol over simnet
// used to exercise and meta-test the simulator kernel before any proxy or
// Postgres code exists. It ships 11 named scenarios covering every simnet
// fault kind plus test-only planted-bug switches for canary and shrinker checks.
package simtoy

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// Default toy echo parameters and server buffer cap.
const (
	// Address is the simnet listener name used by the echo server.
	Address = "echo"
	// DefaultClients is the number of concurrent client actors when Config.Clients <= 0.
	DefaultClients = 3
	// DefaultLines is the number of newline-terminated lines each client sends.
	DefaultLines = 20
	// DefaultMaxLine is the maximum payload length (excluding '\n') per generated line.
	DefaultMaxLine = 40
	// DefaultDeadline is each client's virtual-clock timeout before closing its end.
	DefaultDeadline = time.Second
	// MaxPending caps combined partial-line and queued-echo bytes per server connection.
	MaxPending = 1 << 20
	readBuf    = 4096
)

// Outcome classifies how a toy client finished its exchange.
type Outcome uint8

// Terminal outcomes recorded per client in Result.Outcomes and Result.Recovery.
const (
	// OutcomeNone indicates the client has not finished yet.
	OutcomeNone Outcome = iota
	// OutcomeCompleted indicates every line echoed back byte-for-byte followed by EOF.
	OutcomeCompleted
	// OutcomeReset indicates the connection was reset before completion.
	OutcomeReset
	// OutcomeRefused indicates the dial was refused.
	OutcomeRefused
	// OutcomeTimedOut indicates the client hit its virtual-clock deadline.
	OutcomeTimedOut
	// OutcomeEarlyEOF indicates the server sent EOF before echoing all expected bytes.
	OutcomeEarlyEOF
	// OutcomeMismatch indicates an echoed byte differed from what the client sent.
	OutcomeMismatch
)

var outcomeNames = [...]string{"none", "completed", "reset", "refused", "timed-out", "early-eof", "mismatch"}

// String returns the human-readable outcome name, or "?" for unknown values.
func (o Outcome) String() string {
	if int(o) < len(outcomeNames) {
		return outcomeNames[o]
	}
	return "?"
}

// ErrMismatch is returned in Result.Err when any client observes an echoed
// byte that does not match its sent payload.
var (
	ErrMismatch = errors.New("simtoy: echoed bytes differ from the bytes sent")
	kindOutcome = sim.K("toy.out")
	kindEcho    = sim.K("toy.echo")
)

// Config configures one toy echo simulation run. Unexported switches
// (plantBug, panicOnEOF, observe) are reachable only from tests in this package.
type Config struct {
	Seed       int64
	Scenario   string
	Replay     string
	TraceOff   bool
	MaxSteps   int64
	Clients    int
	Lines      int
	MaxLine    int
	StartAt    time.Duration
	Gap        time.Duration
	Options    simnet.Options
	Deadline   time.Duration
	Faults     []sim.Fault
	RecoverAt  time.Duration
	Tap        func(sim.Record)
	plantBug   bool
	panicOnEOF bool
	observe    func(n *simnet.Net)
}

func (c Config) withDefaults() Config {
	if c.Clients <= 0 {
		c.Clients = DefaultClients
	}
	if c.Lines <= 0 {
		c.Lines = DefaultLines
	}
	if c.MaxLine <= 0 {
		c.MaxLine = DefaultMaxLine
	}
	if c.Deadline <= 0 {
		c.Deadline = DefaultDeadline
	}
	if c.Gap <= 0 {
		c.Gap = 10 * time.Microsecond
	}
	if c.Options.Window == 0 {
		c.Options = simnet.DefaultOptions()
	}
	return c
}

// Result collects the finished simulator, network, client outcomes, server
// counters, and any kernel, mismatch, or quiesce error from Run.
type Result struct {
	Sim       *sim.Sim
	Net       *simnet.Net
	Outcomes  []Outcome
	Recovery  Outcome
	Received  [][]byte
	Finished  []sim.Time
	Server    ServerStats
	Err       error
	Quiesce   error
	Mismatch  bool
	recClient *client
}

// ServerStats counts server-side connection and echo activity during a run.
type ServerStats struct {
	Accepted int64
	Reads    int64
	Lines    int64
	Echoed   int64
	Resets   int64
	Closed   int64
}

type world struct {
	cfg     Config
	s       *sim.Sim
	net     *simnet.Net
	clients []*client
	server  ServerStats
}

// Run executes one complete toy echo simulation with cfg until quiet and
// returns its Result, including the end-of-run CheckQuiesce result.
func Run(cfg Config) *Result {
	cfg = cfg.withDefaults()
	s := sim.New(sim.Config{Seed: cfg.Seed, Scenario: cfg.Scenario, Replay: cfg.Replay, TraceOff: cfg.TraceOff, MaxSteps: cfg.MaxSteps})
	if cfg.Tap != nil {
		s.SetTraceTap(cfg.Tap)
	}
	w := &world{cfg: cfg, s: s, net: simnet.New(s)}
	_ = s.RegisterState("simtoy", w.writeState)
	if cfg.observe != nil {
		_ = s.RegisterInvariant("simtoy.observe", func() error { cfg.observe(w.net); return nil })
	}
	if _, err := w.net.Listen(Address, w.accept); err != nil {
		s.Fail(err)
	}
	s.InjectSchedule(cfg.Faults)
	for i := 0; i < cfg.Clients; i++ {
		w.startClient(sim.Time(0).Add(cfg.StartAt + sim.Scaled(int64(i), cfg.Gap)))
	}
	var rec *client
	if cfg.RecoverAt > 0 {
		rec = w.startClient(sim.Time(0).Add(cfg.RecoverAt))
	}
	_ = s.RunUntilQuiet()
	res := &Result{Sim: s, Net: w.net, Server: w.server, recClient: rec}
	for _, c := range w.clients {
		if c == rec {
			res.Recovery = c.outcome
			continue
		}
		res.Outcomes = append(res.Outcomes, c.outcome)
		res.Received = append(res.Received, c.got)
		res.Finished = append(res.Finished, c.finishedAt)
	}
	for _, c := range w.clients {
		res.Mismatch = res.Mismatch || c.outcome == OutcomeMismatch
	}
	res.Quiesce = s.CheckQuiesce()
	res.Err = s.Err()
	if res.Err == nil && res.Mismatch {
		res.Err = fmt.Errorf("%w: seed %d\n%s", ErrMismatch, cfg.Seed, s.Replay())
	}
	return res
}

func (w *world) writeState(st sim.StateSink) {
	sv := w.server
	st.Int(sv.Accepted, sv.Reads, sv.Lines, sv.Echoed, sv.Resets, sv.Closed)
	for _, c := range w.clients {
		st.Int(int64(c.id), int64(c.outcome), int64(c.sent), int64(len(c.got)))
	}
}

func (w *world) startClient(at sim.Time) *client {
	c := &client{w: w, id: len(w.clients)}
	w.clients = append(w.clients, c)
	r := w.s.Stream("simtoy/client/" + strconv.Itoa(c.id))
	for i := 0; i < w.cfg.Lines; i++ {
		n := 1 + r.IntN(w.cfg.MaxLine)
		line := make([]byte, n+1)
		for j := 0; j < n; j++ {
			line[j] = byte('a' + r.IntN(26))
		}
		line[n] = '\n'
		c.lines = append(c.lines, line)
		c.expect = append(c.expect, line...)
	}
	w.s.At(at, c.start)
	return c
}

type client struct {
	w          *world
	id         int
	e          *simnet.End
	lines      [][]byte
	line       int
	off        int
	sent       int
	expect     []byte
	got        []byte
	outcome    Outcome
	deadline   sim.Timer
	finishedAt sim.Time
}

func (c *client) start() {
	c.e = c.w.net.Dial(Address, c.w.cfg.Options, c)
	c.deadline = c.w.s.After(c.w.cfg.Deadline, c.timeout)
}

func (c *client) OnEvent(e *simnet.End, ev simnet.Event) {
	if c.outcome != OutcomeNone {
		return
	}
	switch ev {
	case simnet.EvConnected, simnet.EvWritable:
		c.push()
	case simnet.EvReadable:
		c.pull()
	case simnet.EvRefused:
		c.finish(OutcomeRefused)
	case simnet.EvReset:
		c.finish(OutcomeReset)
	}
}

func (c *client) push() {
	for c.line < len(c.lines) {
		l := c.lines[c.line]
		// Write whole lines whenever a line fits in the full window so that
		// under ChunkWhole no line straddles a delivery boundary and the
		// planted bug can fire only when chunking is active.
		if space := c.e.Space(); space > 0 && space < len(l)-c.off && space < c.e.Window() {
			c.e.WaitWritable()
			return
		}
		n, err := c.e.Write(l[c.off:])
		if err != nil {
			return
		}
		c.sent += n
		c.off += n
		if c.off < len(l) {
			return
		}
		c.line++
		c.off = 0
	}
	_ = c.e.CloseWrite()
}

func (c *client) pull() {
	var buf [readBuf]byte
	for {
		n, err := c.e.Read(buf[:])
		c.got = append(c.got, buf[:n]...)
		if len(c.got) > len(c.expect) || string(c.got) != string(c.expect[:len(c.got)]) {
			c.finish(OutcomeMismatch)
			_ = c.e.Reset()
			return
		}
		if err == io.EOF {
			if len(c.got) == len(c.expect) {
				c.finish(OutcomeCompleted)
			} else {
				c.finish(OutcomeEarlyEOF)
			}
			_ = c.e.Close()
			return
		}
		if err != nil || n == 0 {
			return
		}
	}
}

func (c *client) timeout() {
	if c.outcome != OutcomeNone {
		return
	}
	c.finish(OutcomeTimedOut)
	_ = c.e.Close()
}

func (c *client) finish(o Outcome) {
	c.outcome = o
	c.finishedAt = c.w.s.Now()
	c.deadline.Cancel()
	c.w.s.Trace(kindOutcome, int64(c.id), int64(o), int64(c.sent), int64(len(c.got)))
}

type serverConn struct {
	w      *world
	e      *simnet.End
	line   []byte
	pieces int
	out    []byte
	eof    bool
	closed bool
}

func (w *world) accept(e *simnet.End) {
	w.server.Accepted++
	e.SetHandler(&serverConn{w: w, e: e})
}

func (sc *serverConn) OnEvent(e *simnet.End, ev simnet.Event) {
	if sc.closed {
		return
	}
	switch ev {
	case simnet.EvReadable:
		sc.pull()
	case simnet.EvWritable:
		sc.flush()
	case simnet.EvReset:
		sc.w.server.Resets++
		sc.closed = true
	}
}

func (sc *serverConn) pull() {
	var buf [readBuf]byte
	for !sc.closed {
		n, err := sc.e.Read(buf[:])
		if n > 0 {
			sc.w.server.Reads++
			sc.take(buf[:n])
		}
		if err == io.EOF {
			if sc.w.cfg.panicOnEOF {
				var m map[int]int
				m[0]++
			}
			sc.eof = true
			sc.flush()
			return
		}
		if err != nil || n == 0 {
			return
		}
	}
}

func (sc *serverConn) take(b []byte) {
	sc.pieces++
	for _, ch := range b {
		sc.line = append(sc.line, ch)
		// Enforce MaxPending on every byte so a peer sending bytes without '\n'
		// cannot grow server memory unboundedly.
		if len(sc.out)+len(sc.line) > MaxPending {
			sc.overflow()
			return
		}
		if ch != '\n' {
			continue
		}
		echo := sc.line
		// Planted bug: drop the last payload byte of any line that arrived
		// across more than one Read call.
		if sc.w.cfg.plantBug && sc.pieces > 1 && len(echo) > 1 {
			echo = append(echo[:len(echo)-2:len(echo)-2], '\n')
		}
		sc.w.server.Lines++
		sc.w.s.Trace(kindEcho, sc.e.ID(), int64(len(echo)), 0, 0)
		sc.out = append(sc.out, echo...)
		sc.line = sc.line[:0]
		sc.pieces = 1
	}
	if len(sc.line) == 0 {
		sc.pieces = 0
	}
	sc.flush()
}

func (sc *serverConn) overflow() {
	sc.closed = true
	_ = sc.e.Reset()
}

func (sc *serverConn) flush() {
	if sc.closed {
		return
	}
	if len(sc.out) > 0 {
		n, err := sc.e.Write(sc.out)
		if err != nil {
			sc.closed = true
			sc.w.server.Closed++
			_ = sc.e.Close()
			return
		}
		sc.w.server.Echoed += int64(n)
		sc.out = sc.out[n:]
	}
	if len(sc.out) == 0 && sc.eof {
		sc.closed = true
		sc.w.server.Closed++
		_ = sc.e.Close()
	}
}
