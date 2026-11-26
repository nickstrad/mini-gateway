package e2e

import (
	"errors"
	"io"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

type Tamper uint8

const (
	TamperNone Tamper = iota
	TamperError
	TamperWrongTag
	TamperStatusT
	TamperSilent
	TamperDrop
	TamperLate
	NumTampers
)

var tamperNames = [NumTampers]string{"none", "error", "wrong-tag", "status-t", "silent", "drop", "late"}

func (t Tamper) String() string {
	if t < NumTampers {
		return tamperNames[t]
	}
	return "?"
}

const (
	tamperAddr  = "pg-tamper"
	lateBy      = 3 * time.Second
	maxRelayBuf = 1 << 24
)

type tamperer struct {
	mode    Tamper
	s       *sim.Sim
	net     *simnet.Net
	fake    string
	opts    simnet.Options
	fired   int64
	backend int64
	done    bool
	pairs   []*relayPair
	buf     [16 << 10]byte
}

type relayPair struct {
	t        *tamperer
	front    *simnet.End
	back     *simnet.End
	upFr     pgwire.Framer
	started  bool
	downFr   pgwire.Framer
	capture  bool
	captured []byte
	toBack   []byte
	toFront  []byte
	closed   bool
}

func (t *tamperer) listen(net *simnet.Net, srv *fakepg.Server) error {
	t.s, t.net, t.fake = net.Sim(), net, srv.Addr()
	t.opts = srv.Config().Options
	_, err := net.Listen(tamperAddr, t.accept)
	return err
}

func (t *tamperer) Fired() int64 { return t.fired }

func (t *tamperer) accept(e *simnet.End) {
	p := &relayPair{t: t, front: e}
	p.upFr.Untyped = true
	p.upFr.Max = 1 << 20
	p.downFr.Max = 1 << 20
	t.pairs = append(t.pairs, p)
	e.SetHandler(simnet.HandlerFunc(p.onFront))
	p.back = t.net.Dial(t.fake, t.opts, simnet.HandlerFunc(p.onBack))
}

func (p *relayPair) onFront(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvReadable:
		p.pump(p.front, true)
	case simnet.EvWritable:
		p.flush()
	case simnet.EvReset:
		p.shut()
	}
}

func (p *relayPair) onBack(_ *simnet.End, ev simnet.Event) {
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

func (p *relayPair) pump(from *simnet.End, up bool) {
	for !p.closed {
		n, err := from.Read(p.t.buf[:])
		if n > 0 {
			if up {
				p.upBytes(p.t.buf[:n])
			} else {
				p.downBytes(p.t.buf[:n])
			}
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrNotConnected)) {
			p.shut()
		}
		return
	}
}

func (p *relayPair) upBytes(b []byte) {
	for len(b) > 0 && !p.closed {
		n, done, err := p.upFr.Take(b)
		p.toBack = append(p.toBack, b[:n]...)
		b = b[n:]
		if err != nil {
			p.shut()
			return
		}
		if !done {
			break
		}
		f := p.upFr.Frame()
		if !p.started {
			p.started = true
			p.upFr.Untyped = false
			continue
		}
		if f.Type == pgwire.MsgQuery && string(f.Body) == "DISCARD ALL\x00" && !p.t.done {
			p.t.done = true
			p.t.fired++
			p.t.backend = p.back.ID()
			p.capture = true
			if p.t.mode == TamperDrop {
				p.flush()
				p.reset()
				return
			}
		}
	}
	p.flush()
}

func (p *relayPair) downBytes(b []byte) {
	for len(b) > 0 && !p.closed {
		start := b
		n, done, err := p.downFr.Take(b)
		b = b[n:]
		if err != nil {
			p.shut()
			return
		}
		if !p.capture {
			p.toFront = append(p.toFront, start[:n]...)
			continue
		}
		p.captured = append(p.captured, start[:n]...)
		if done && p.downFr.Frame().Type == pgwire.MsgReadyForQuery {
			p.capture = false
			p.rewrite()
		}
	}
	p.flush()
}

func (p *relayPair) rewrite() {
	reply := p.captured
	p.captured = nil
	switch p.t.mode {
	case TamperError:
		reply = pgwire.AppendErrorResponse(nil, "ERROR", "25001", "tampered reset")
		reply = pgwire.AppendReadyForQuery(reply, pgwire.StatusIdle)
	case TamperWrongTag:
		reply = appendCommandComplete(nil, "DISCARD")
		reply = pgwire.AppendReadyForQuery(reply, pgwire.StatusIdle)
	case TamperStatusT:
		reply = appendCommandComplete(nil, "DISCARD ALL")
		reply = pgwire.AppendReadyForQuery(reply, pgwire.StatusInTransaction)
	case TamperSilent:
		reply = nil
	case TamperLate:
		late := reply
		reply = nil
		p.t.s.After(lateBy, func() {
			if p.closed {
				return
			}
			p.toFront = append(p.toFront, late...)
			p.flush()
		})
	}
	p.toFront = append(p.toFront, reply...)
}

func appendCommandComplete(dst []byte, tag string) []byte {
	dst = pgwire.AppendMessageHeader(dst, pgwire.MsgCommandComplete, len(tag)+1)
	dst = append(dst, tag...)
	return append(dst, 0)
}

func (p *relayPair) flush() {
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
	if len(p.toBack) > maxRelayBuf || len(p.toFront) > maxRelayBuf {
		p.shut()
	}
}

func (p *relayPair) reset() {
	p.closed = true
	_ = p.front.Reset()
	_ = p.back.Reset()
}

func (p *relayPair) shut() {
	if p.closed {
		return
	}
	p.closed = true
	_ = p.front.Close()
	_ = p.back.Close()
}
