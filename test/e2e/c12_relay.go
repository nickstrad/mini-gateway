package e2e

import (
	"encoding/binary"
	"errors"
	"io"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

const (
	retryAddr          = "pg-retry"
	doomDelay          = 2 * time.Millisecond
	retryRelayFrameMax = 1 << 20
	maxRetryRelayBuf   = 1 << 24
	maxKilledTexts     = 1 << 16
)

type KillPoint uint8

const (
	KillBeforeForward KillPoint = iota
	KillAfterForward
	KillAfterApplied
	KillMidReply
	KillAfterReply
	NumKillPoints
	FaultDoom = NumKillPoints
)

var killNames = [...]string{"before-forward", "after-forward", "after-applied", "mid-reply", "after-reply", "doom"}

func (k KillPoint) String() string {
	if int(k) < len(killNames) {
		return killNames[k]
	}
	return "?"
}

type RelayPlan struct {
	From, Until time.Duration
	DoomAllAt   []time.Duration
	Pattern     func(n int64) (KillPoint, bool)
}

type RelayStats struct {
	Pairs        int64
	Queries      int64
	Faulted      int64
	Kills        [NumKillPoints]int64
	DoomsArmed   int64
	Dooms        int64
	DoomsSkipped int64
	Exempt       int64
}

type retryRelay struct {
	plan   RelayPlan
	s      *sim.Sim
	net    *simnet.Net
	fake   string
	opts   simnet.Options
	pairs  []*retryPair
	stats  RelayStats
	killed map[string]bool
	buf    [16 << 10]byte
}

type retryPair struct {
	r        *retryRelay
	front    *simnet.End
	back     *simnet.End
	upFr     pgwire.Framer
	downFr   pgwire.Framer
	toBack   []byte
	toFront  []byte
	closed   bool
	cancel   bool
	started  bool
	inQuery  bool
	internal bool
	discard  bool
	kill     KillPoint
	killing  bool
	doomNext bool
	upBytes  int64
	last     sim.Time
}

func newRetryRelay(plan RelayPlan) *retryRelay {
	return &retryRelay{plan: plan, killed: map[string]bool{}}
}

func (r *retryRelay) listen(net *simnet.Net, srv *fakepg.Server) error {
	r.s, r.net, r.fake = net.Sim(), net, srv.Addr()
	r.opts = srv.Config().Options
	if _, err := net.Listen(retryAddr, r.accept); err != nil {
		return err
	}
	for _, at := range r.plan.DoomAllAt {
		r.s.After(at, r.doomAll)
	}
	return nil
}

func (r *retryRelay) Stats() RelayStats { return r.stats }

func (r *retryRelay) inWindow() bool {
	now := time.Duration(r.s.Now())
	return r.plan.Pattern != nil && now >= r.plan.From && now < r.plan.Until
}

func (r *retryRelay) doomAll() {
	for _, p := range r.pairs {
		if !p.closed && !p.cancel && p.started && !p.inQuery && r.s.Now().Sub(p.last) >= doomDelay {
			p.doom()
		}
	}
}

func (r *retryRelay) accept(e *simnet.End) {
	p := &retryPair{r: r, front: e, last: r.s.Now()}
	p.upFr.Untyped, p.upFr.Max = true, retryRelayFrameMax
	p.downFr.Max = retryRelayFrameMax
	r.pairs = append(r.pairs, p)
	r.stats.Pairs++
	e.SetHandler(simnet.HandlerFunc(p.onFront))
	p.back = r.net.Dial(r.fake, r.opts, simnet.HandlerFunc(p.onBack))
}

func (p *retryPair) onFront(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvReadable:
		p.pump(p.front, true)
	case simnet.EvWritable:
		p.flush()
	case simnet.EvReset:
		p.shut()
	}
}

func (p *retryPair) onBack(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		p.flush()
		p.pump(p.back, false)
	case simnet.EvReadable:
		p.pump(p.back, false)
	case simnet.EvWritable:
		p.flush()
	case simnet.EvRefused, simnet.EvReset:
		p.shut()
	}
}

func (p *retryPair) pump(from *simnet.End, up bool) {
	for !p.closed {
		n, err := from.Read(p.r.buf[:])
		if n > 0 {
			p.take(p.r.buf[:n], up)
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrNotConnected)) {
			p.shut()
		}
		return
	}
}

func (p *retryPair) take(b []byte, up bool) {
	fr := &p.downFr
	if up {
		fr = &p.upFr
		p.upBytes += int64(len(b))
	}
	p.last = p.r.s.Now()
	for len(b) > 0 && !p.closed {
		n, done, err := fr.Take(b)
		b = b[n:]
		if err != nil {
			p.shut()
			return
		}
		if !done {
			continue
		}
		f := fr.Frame()
		raw := frameBytes(f, up && !p.started)
		if up {
			p.upFrame(f, raw)
		} else {
			p.downFrame(f, raw)
		}
	}
	p.flush()
}

func frameBytes(f pgwire.Frame, untyped bool) []byte {
	if untyped {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(f.Body)+4))
		return append(out, f.Body...)
	}
	out := append([]byte{f.Type}, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(out[1:], uint32(len(f.Body)+4))
	return append(out, f.Body...)
}

func (p *retryPair) upFrame(f pgwire.Frame, raw []byte) {
	if !p.started {
		p.started = true
		p.upFr.Untyped = false
		if len(f.Body) >= 4 && binary.BigEndian.Uint32(f.Body[:4]) == pgwire.CodeCancelRequest {
			p.cancel = true
		}
		p.toBack = append(p.toBack, raw...)
		return
	}
	if f.Type != pgwire.MsgQuery || p.cancel {
		p.toBack = append(p.toBack, raw...)
		return
	}
	text := string(f.Body)
	if n := len(text); n > 0 && text[n-1] == 0 {
		text = text[:n-1]
	}
	p.inQuery = true
	p.internal = text == "" || text == "DISCARD ALL" || text == "ROLLBACK" || len(text) > 14 && text[:15] == "SELECT setseed("
	p.discard = text == "DISCARD ALL"
	if p.internal {
		p.toBack = append(p.toBack, raw...)
		return
	}
	p.clientQuery(text, raw)
}

func (p *retryPair) clientQuery(text string, raw []byte) {
	r := p.r
	if !r.inWindow() {
		p.toBack = append(p.toBack, raw...)
		return
	}
	r.stats.Queries++
	if r.killed[text] {
		r.stats.Exempt++
		p.toBack = append(p.toBack, raw...)
		return
	}
	k, ok := r.plan.Pattern(r.stats.Queries - 1)
	if !ok {
		p.toBack = append(p.toBack, raw...)
		return
	}
	r.stats.Faulted++
	if k == FaultDoom {
		r.stats.DoomsArmed++
		p.doomNext = true
		p.toBack = append(p.toBack, raw...)
		return
	}
	r.stats.Kills[k]++
	if k == KillAfterApplied && len(r.killed) < maxKilledTexts {
		r.killed[text] = true
	}
	switch k {
	case KillBeforeForward:
		p.cut()
	case KillAfterForward:
		p.toBack = append(p.toBack, raw...)
		p.flush()
		p.closed = true
		_ = p.front.Reset()
		_ = p.back.Reset()
	default:
		p.kill, p.killing = k, true
		p.toBack = append(p.toBack, raw...)
	}
}

func (p *retryPair) downFrame(f pgwire.Frame, raw []byte) {
	if p.killing {
		switch {
		case p.kill == KillAfterApplied:
			p.cut()
			return
		case p.kill == KillMidReply && f.Type == pgwire.MsgReadyForQuery:
			p.cut()
			return
		case p.kill == KillAfterReply && f.Type == pgwire.MsgReadyForQuery:
			p.toFront = append(p.toFront, raw...)
			p.flush()
			p.cut()
			return
		}
	}
	p.toFront = append(p.toFront, raw...)
	if f.Type != pgwire.MsgReadyForQuery {
		return
	}
	p.inQuery = false
	if p.discard && p.doomNext {
		p.doomNext = false
		mark := p.upBytes
		p.r.s.After(doomDelay, func() { p.doomIfQuiet(mark) })
	}
}

func (p *retryPair) doomIfQuiet(mark int64) {
	if p.closed || p.upBytes != mark || p.inQuery {
		p.r.stats.DoomsSkipped++
		return
	}
	p.doom()
}

func (p *retryPair) doom() {
	p.r.stats.Dooms++
	p.r.s.ApplyFault(sim.Fault{Kind: simnet.FaultHalfOpen, Target: p.front.Conn().ID(), Arg1: 0})
	p.closed = true
	p.toBack, p.toFront = nil, nil
	_ = p.front.Close()
	_ = p.back.Close()
}

func (p *retryPair) cut() {
	if p.closed {
		return
	}
	p.closed = true
	p.toBack, p.toFront = nil, nil
	_ = p.front.Reset()
	_ = p.back.Close()
}

func (p *retryPair) flush() {
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
	if len(p.toBack) > maxRetryRelayBuf || len(p.toFront) > maxRetryRelayBuf {
		p.shut()
	}
}

func (p *retryPair) shut() {
	if p.closed {
		return
	}
	p.closed = true
	_ = p.front.Close()
	_ = p.back.Close()
}
