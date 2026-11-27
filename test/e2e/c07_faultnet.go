package e2e

import (
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// PathFault selects the middlebox or peer failure injected on a relayed backend connection.
type PathFault uint8

const (
	// PathNormal forwards bytes normally in both directions.
	PathNormal PathFault = iota
	// PathBlackhole accepts and silently discards bytes in both directions without closing.
	PathBlackhole
	// PathPeerForgot accepts the first write and resets the proxy leg after PeerDelay.
	PathPeerForgot
	// PathPoliteClose sends a clean FIN on both legs of matching idle connections.
	PathPoliteClose
)

var pathFaultNames = [...]string{"normal", "blackhole", "peer-forgot", "polite-close"}

// String returns the canonical hyphenated name of f, or "?" if unknown.
func (f PathFault) String() string {
	if int(f) < len(pathFaultNames) {
		return pathFaultNames[f]
	}
	return "?"
}

const (
	faultAddr          = "pg-path"
	defaultPeerDelay   = time.Millisecond
	reseedReplacement  = "SELECT 0"
	maxPathBuf         = 1 << 24
	reseedPrefix       = "SELECT setseed("
	faultTargetAll     = 0
	faultTargetIdleAge = -1
)

// PathEvent schedules one fault injection or clearing event on pathRelay.
type PathEvent struct {
	At     time.Duration
	Fault  PathFault
	Target int
	MinAge time.Duration
	Clear  bool
}

// PathPlan configures the path-fault relay placed between the proxy and fakepg.
type PathPlan struct {
	Events        []PathEvent
	IdleDrop      time.Duration
	IdleDropAs    PathFault
	Keepalive     time.Duration
	ReapAfter     time.Duration
	PeerDelay     time.Duration
	RewriteReseed bool
}

// PathStats records connections, injected faults, reaped slots, and re-seeds seen by pathRelay.
type PathStats struct {
	Pairs      int64
	Blackholed int64
	Forgot     int64
	Polite     int64
	IdleDrops  int64
	Reaped     int64
	Swallowed  int64
	Reseeds    int64
	Held       int64
	Cancels    int64
}

type pathRelay struct {
	plan    PathPlan
	s       *sim.Sim
	net     *simnet.Net
	fake    string
	opts    simnet.Options
	pairs   []*pathPair
	stats   PathStats
	cleared bool
	accepts []sim.Time
	gone    []sim.Time
	open    int64
	peak    int64
	buf     [16 << 10]byte
}

const maxPathTimes = 1 << 16

type pathPair struct {
	r       *pathRelay
	idx     int
	front   *simnet.End
	back    *simnet.End
	mode    PathFault
	last    sim.Time
	toBack  []byte
	toFront []byte
	closed  bool
	held    bool
	upFr    pgwire.Framer
	started bool
	idleT   sim.Timer
	forgotT sim.Timer
	at      sim.Time
	sorted  bool
	counted bool
	cancel  bool
}

func newPathRelay(plan PathPlan) *pathRelay {
	if plan.PeerDelay == 0 {
		plan.PeerDelay = defaultPeerDelay
	}
	if plan.IdleDropAs == PathNormal {
		plan.IdleDropAs = PathBlackhole
	}
	return &pathRelay{plan: plan}
}

func (r *pathRelay) listen(net *simnet.Net, srv *fakepg.Server) error {
	r.s, r.net, r.fake = net.Sim(), net, srv.Addr()
	r.opts = srv.Config().Options
	if _, err := net.Listen(faultAddr, r.accept); err != nil {
		return err
	}
	for _, ev := range r.plan.Events {
		ev := ev
		r.s.After(ev.At, func() { r.apply(ev) })
	}
	return nil
}

func (r *pathRelay) Stats() PathStats { return r.stats }

func (r *pathRelay) apply(ev PathEvent) {
	if ev.Clear {
		r.cleared = true
		r.plan.IdleDrop = 0
		return
	}
	now := r.s.Now()
	for _, p := range r.pairs {
		if p.closed || p.mode != PathNormal {
			continue
		}
		switch {
		case ev.Target > 0 && p.idx != ev.Target:
			continue
		case ev.Target == faultTargetIdleAge && now.Sub(p.last) < ev.MinAge:
			continue
		}
		p.fault(ev.Fault)
	}
}

func (r *pathRelay) accept(e *simnet.End) {
	p := &pathPair{r: r, idx: len(r.pairs) + 1, front: e, last: r.s.Now(), at: r.s.Now()}
	p.upFr.Untyped = true
	p.upFr.Max = 1 << 20
	r.pairs = append(r.pairs, p)
	r.stats.Pairs++
	e.SetHandler(simnet.HandlerFunc(p.onFront))
	p.back = r.net.Dial(r.fake, r.opts, simnet.HandlerFunc(p.onBack))
	p.armIdle()
}

func (p *pathPair) armIdle() {
	d := p.r.plan.IdleDrop
	if d <= 0 || p.closed || p.mode != PathNormal {
		return
	}
	if k := p.r.plan.Keepalive; k > 0 && k < d {
		return
	}
	p.idleT.Cancel()
	p.idleT = p.r.s.After(d, p.idleCheck)
}

func (p *pathPair) idleCheck() {
	if p.closed || p.mode != PathNormal || p.r.plan.IdleDrop <= 0 {
		return
	}
	if p.r.s.Now().Sub(p.last) >= p.r.plan.IdleDrop {
		p.r.stats.IdleDrops++
		p.fault(p.r.plan.IdleDropAs)
		return
	}
	p.idleT = p.r.s.After(p.r.plan.IdleDrop-p.r.s.Now().Sub(p.last), p.idleCheck)
}

func (p *pathPair) touch() {
	p.last = p.r.s.Now()
}

func (p *pathPair) fault(f PathFault) {
	p.idleT.Cancel()
	p.mode = f
	switch f {
	case PathBlackhole:
		p.r.stats.Blackholed++
		p.toFront, p.toBack = nil, nil
	case PathPeerForgot:
		p.r.stats.Forgot++
		p.toFront, p.toBack = nil, nil
	case PathPoliteClose:
		p.r.stats.Polite++
		p.shut()
	}
}

func (p *pathPair) onFront(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvReadable:
		p.pump(p.front, true)
	case simnet.EvWritable:
		p.flush()
	case simnet.EvReset:
		p.frontGone()
	}
}

func (p *pathPair) onBack(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		p.flush()
		p.pump(p.back, false)
	case simnet.EvReadable:
		p.pump(p.back, false)
	case simnet.EvWritable:
		p.flush()
	case simnet.EvRefused, simnet.EvReset:
		p.backGone()
	}
}

func (p *pathPair) pump(from *simnet.End, up bool) {
	for !p.closed || (p.held && !up) {
		n, err := from.Read(p.r.buf[:])
		if n > 0 {
			if up {
				p.up(p.r.buf[:n])
			} else {
				p.down(p.r.buf[:n])
			}
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrNotConnected)) {
			if up {
				p.frontGone()
			} else {
				p.backGone()
			}
		}
		return
	}
}

func (p *pathPair) classify(b []byte) {
	if p.sorted {
		return
	}
	p.sorted = true
	if len(b) >= 8 && binary.BigEndian.Uint32(b[4:8]) == pgwire.CodeCancelRequest {
		p.cancel = true
		p.r.stats.Cancels++
		return
	}
	p.counted = true
	if len(p.r.accepts) < maxPathTimes {
		p.r.accepts = append(p.r.accepts, p.at)
		slices.Sort(p.r.accepts)
	}
	p.r.open++
	p.r.peak = max(p.r.peak, p.r.open)
}

func (p *pathPair) uncount() {
	if p.counted {
		p.counted = false
		p.r.open--
	}
}

func (p *pathPair) up(b []byte) {
	p.classify(b)
	switch p.mode {
	case PathBlackhole:
		p.r.stats.Swallowed += int64(len(b))
		return
	case PathPeerForgot:
		p.r.stats.Swallowed += int64(len(b))
		if !p.forgotT.Pending() {
			p.forgotT = p.r.s.After(p.r.plan.PeerDelay, p.forget)
		}
		return
	}
	p.touch()
	if !p.r.plan.RewriteReseed {
		p.toBack = append(p.toBack, b...)
		p.flush()
		p.armIdle()
		return
	}
	for len(b) > 0 && !p.closed {
		n, done, err := p.upFr.Take(b)
		chunk := b[:n]
		b = b[n:]
		if err != nil {
			p.shut()
			return
		}
		if !done {
			p.toBack = append(p.toBack, chunk...)
			continue
		}
		f := p.upFr.Frame()
		if !p.started {
			p.started = true
			p.upFr.Untyped = false
			p.toBack = append(p.toBack, chunk...)
			continue
		}
		p.toBack = append(p.toBack, p.rewrite(f, chunk)...)
	}
	p.flush()
	p.armIdle()
}

func (p *pathPair) rewrite(f pgwire.Frame, chunk []byte) []byte {
	if f.Type != pgwire.MsgQuery || len(f.Body) < len(reseedPrefix) || string(f.Body[:len(reseedPrefix)]) != reseedPrefix {
		return chunk
	}
	p.r.stats.Reseeds++
	return pgwire.AppendQuery(p.toBack[:0:0], []byte(reseedReplacement))
}

func (p *pathPair) down(b []byte) {
	if p.mode != PathNormal || p.closed {
		p.r.stats.Swallowed += int64(len(b))
		return
	}
	p.touch()
	p.toFront = append(p.toFront, b...)
	p.flush()
	p.armIdle()
}

func (p *pathPair) forget() {
	if p.closed {
		return
	}
	p.uncount()
	p.closed = true
	_ = p.front.Reset()
	_ = p.back.Close()
}

func (p *pathPair) frontGone() {
	if p.closed {
		return
	}
	if len(p.r.gone) < maxPathTimes && !p.cancel {
		p.r.gone = append(p.r.gone, p.r.s.Now())
	}
	if p.mode == PathBlackhole && p.r.plan.ReapAfter > 0 {
		p.uncount()
		p.closed, p.held = true, true
		p.r.stats.Held++
		_ = p.front.Close()
		p.r.s.After(p.r.plan.ReapAfter, p.reap)
		return
	}
	p.shut()
}

func (p *pathPair) reap() {
	p.held = false
	p.r.stats.Reaped++
	_ = p.back.Close()
}

func (p *pathPair) backGone() {
	if p.mode == PathBlackhole {
		return
	}
	p.shut()
}

func (p *pathPair) flush() {
	if p.closed {
		return
	}
	if len(p.toBack) > 0 {
		if n, err := p.back.Write(p.toBack); err == nil {
			p.toBack = p.toBack[n:]
		}
		if len(p.toBack) > 0 {
			p.back.WaitWritable()
		}
	}
	if len(p.toFront) > 0 {
		if n, err := p.front.Write(p.toFront); err == nil {
			p.toFront = p.toFront[n:]
		}
		if len(p.toFront) > 0 {
			p.front.WaitWritable()
		}
	}
	if len(p.toBack) > maxPathBuf || len(p.toFront) > maxPathBuf {
		p.shut()
	}
}

func (p *pathPair) shut() {
	if p.closed {
		return
	}
	p.closed = true
	p.uncount()
	p.idleT.Cancel()
	_ = p.front.Close()
	_ = p.back.Close()
}

func (r *pathRelay) String() string {
	s := r.stats
	return "pairs " + strconv.FormatInt(s.Pairs, 10) + " blackholed " + strconv.FormatInt(s.Blackholed, 10) +
		" forgot " + strconv.FormatInt(s.Forgot, 10) + " polite " + strconv.FormatInt(s.Polite, 10) + " idle drops " +
		strconv.FormatInt(s.IdleDrops, 10) + " held " + strconv.FormatInt(s.Held, 10) + " reaped " + strconv.FormatInt(s.Reaped, 10)
}
