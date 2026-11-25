package workload

import (
	"errors"
	"io"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

type EventKind uint8

const (
	EventConnected EventKind = iota + 1
	EventRefused
	EventMessage
	EventReset
	EventEOF
	EventProtocolError
)

func (k EventKind) String() string {
	switch k {
	case EventConnected:
		return "connected"
	case EventRefused:
		return "refused"
	case EventMessage:
		return "message"
	case EventReset:
		return "reset"
	case EventEOF:
		return "eof"
	case EventProtocolError:
		return "protocol-error"
	}
	return "?"
}

const (
	MaxProbeEvents = 1 << 20
	readSize       = 4096
)

type ProbeEvent struct {
	At   sim.Time
	Kind EventKind
	Msg  testclient.Msg
	Err  string
}

type Probe struct {
	s           *sim.Sim
	end         *simnet.End
	sess        testclient.Session
	pending     []byte
	events      []ProbeEvent
	dropped     int64
	stopReading bool
	closed      bool
	connected   bool
	buf         [readSize]byte
}

func NewProbe(n *simnet.Net, addr string, opts simnet.Options) *Probe {
	p := &Probe{s: n.Sim()}
	p.end = n.Dial(addr, opts, p)
	return p
}

func (p *Probe) End() *simnet.End             { return p.end }
func (p *Probe) Session() *testclient.Session { return &p.sess }
func (p *Probe) Events() []ProbeEvent         { return p.events }
func (p *Probe) Closed() bool                 { return p.closed }
func (p *Probe) Connected() bool              { return p.connected }
func (p *Probe) Dropped() int64               { return p.dropped }
func (p *Probe) SetMaxMessage(n int)          { p.sess.MaxMessage = n }
func (p *Probe) StopReading(stop bool)        { p.stopReading = stop; p.read() }
func (p *Probe) Startup(user, db string, extra ...testclient.Param) {
	params := []testclient.Param{{Name: "user", Value: user}}
	if db != "" {
		params = append(params, testclient.Param{Name: "database", Value: db})
	}
	p.Send(p.sess.Startup(nil, append(params, extra...)))
}

func (p *Probe) PreStartup(ssl, gss bool) { p.Send(p.sess.PreStartup(nil, ssl, gss)) }

func (p *Probe) Query(qs ...string) {
	var out []byte
	for _, q := range qs {
		out = p.sess.Query(out, q)
	}
	p.Send(out)
}

func (p *Probe) Cancel(pid uint32, secret []byte) { p.Send(testclient.EncodeCancel(nil, pid, secret)) }

func (p *Probe) Terminate() { p.Send(testclient.EncodeTerminate(nil)) }

func (p *Probe) Send(b []byte) {
	if p.closed {
		return
	}
	p.pending = append(p.pending, b...)
	p.flush()
}

func (p *Probe) flush() {
	if !p.connected || len(p.pending) == 0 {
		return
	}
	n, err := p.end.Write(p.pending)
	if err != nil {
		p.pending = nil
		return
	}
	p.pending = p.pending[n:]
	if len(p.pending) == 0 {
		p.pending = nil
	}
}

func (p *Probe) Close() {
	if p.closed {
		return
	}
	p.closed = true
	p.pending = nil
	_ = p.end.Close()
}

func (p *Probe) Reset() {
	if p.closed {
		return
	}
	p.closed = true
	p.pending = nil
	_ = p.end.Reset()
}

func (p *Probe) record(ev ProbeEvent) {
	ev.At = p.s.Now()
	if len(p.events) >= MaxProbeEvents {
		p.dropped++
		return
	}
	p.events = append(p.events, ev)
}

func (p *Probe) OnEvent(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		p.connected = true
		p.record(ProbeEvent{Kind: EventConnected})
		p.flush()
	case simnet.EvRefused:
		p.closed = true
		p.record(ProbeEvent{Kind: EventRefused})
	case simnet.EvReset:
		p.closed = true
		p.record(ProbeEvent{Kind: EventReset})
	case simnet.EvWritable:
		p.flush()
	case simnet.EvReadable:
		p.read()
	}
}

func (p *Probe) read() {
	for !p.closed && !p.stopReading {
		n, err := p.end.Read(p.buf[:])
		if n > 0 {
			p.sess.Feed(p.buf[:n])
			if !p.drain() {
				return
			}
			continue
		}
		if err == io.EOF {
			p.record(ProbeEvent{Kind: EventEOF})
			p.Close()
		}
		return
	}
}

func (p *Probe) drain() bool {
	for {
		m, ok, err := p.sess.Next()
		if ok {
			p.record(ProbeEvent{Kind: EventMessage, Msg: m})
		}
		if err != nil && !ok {
			if !errors.Is(err, testclient.ErrClosed) {
				p.record(ProbeEvent{Kind: EventProtocolError, Err: err.Error()})
				p.Close()
				return false
			}
			return true
		}
		if !ok {
			return true
		}
	}
}

func (p *Probe) Messages() []testclient.Msg {
	var out []testclient.Msg
	for _, e := range p.events {
		if e.Kind == EventMessage {
			out = append(out, e.Msg)
		}
	}
	return out
}

func (p *Probe) Trace() string { return p.TraceFrom(0) }

func (p *Probe) TraceFrom(i int) string {
	var parts []string
	for _, e := range p.events[min(i, len(p.events)):] {
		switch e.Kind {
		case EventMessage:
			parts = append(parts, describe(e.Msg))
		default:
			parts = append(parts, e.Kind.String())
		}
	}
	return strings.Join(parts, " ")
}

func describe(m testclient.Msg) string {
	switch m.Type {
	case testclient.MsgReady:
		return "Z" + string(m.Status)
	case testclient.MsgError, testclient.MsgNotice:
		return string(m.Type) + ":" + m.Code
	case testclient.MsgCommand:
		return "C:" + m.Tag
	case testclient.MsgSSLAnswer:
		return "ssl:" + string(m.Status)
	case testclient.MsgParamStatus:
		return "S:" + m.Name
	}
	return string(m.Type)
}
