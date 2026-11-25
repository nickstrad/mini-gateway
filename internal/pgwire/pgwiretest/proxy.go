package pgwiretest

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtls"
)

const (
	DefaultAddr       = "proxy"
	DefaultMaxSession = 4096
	maxPending        = 64 << 10
	readChunk         = 4096
)

var (
	kindAccept  = sim.K("pw.acc")
	kindReady   = sim.K("pw.ready")
	kindDBReady = sim.K("pw.dbrdy")
	kindClose   = sim.K("pw.close")
	kindDBFail  = sim.K("pw.dbfail")
)

type Config struct {
	Addr           string
	Database       string
	Client         pgwire.ClientConfig
	Backend        pgwire.BackendConfig
	Handshake      time.Duration
	TLSVersion     pgwire.TLSVersion
	StartupTimeout time.Duration
	ConnectTimeout time.Duration
	Options        simnet.Options
	MaxSessions    int
}

type Counters struct {
	Accepted        int64
	Refused         int64
	Closed          int64
	Open            int64
	ClientReady     int64
	Relayed         int64
	Queries         int64
	Replies         int64
	CancelsSeen     int64
	ExtendedRefused int64
	BackendFailures [pgwire.NumClasses]int64
	ClientCloses    [pgwire.NumReasons]int64
	HandshakesBad   int64
}

type Proxy struct {
	s        *sim.Sim
	net      *simnet.Net
	cfg      Config
	l        *simnet.Listener
	stats    pgwire.WireStats
	rnd      *rand.Rand
	keys     *pgwire.KeyIssuer
	params   []pgwire.Param
	sessions []*session
	c        Counters
	buf      [readChunk]byte
}

func New(net *simnet.Net, cfg Config) (*Proxy, error) {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Handshake <= 0 {
		cfg.Handshake = simtls.DefaultHandshake
	}
	if cfg.TLSVersion == pgwire.TLSVersionOther {
		cfg.TLSVersion = pgwire.TLSVersion13
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = pgwire.DefaultClientStartupTimeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = pgwire.DefaultConnectTimeout
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = DefaultMaxSession
	}
	p := &Proxy{s: net.Sim(), net: net, cfg: cfg, rnd: net.Sim().Stream("pgwiretest/proxy")}
	p.keys = pgwire.NewKeyIssuer(p.rnd)
	p.cfg.Client.Random, p.cfg.Client.Keys, p.cfg.Client.Stats = p.rnd, p.keys, &p.stats
	p.cfg.Backend.Random, p.cfg.Backend.Stats = p.rnd, &p.stats
	if err := errors.Join(p.cfg.Client.Validate(), p.cfg.Backend.Validate()); err != nil {
		return nil, err
	}
	l, err := net.Listen(cfg.Addr, p.accept)
	if err != nil {
		return nil, err
	}
	p.l = l
	_ = p.s.RegisterQuiesce("pgwiretest", p.quiesce)
	_ = p.s.RegisterInvariant("pgwiretest.conservation", func() error { return p.stats.Snapshot().Check() })
	_ = p.s.RegisterState("pgwiretest", p.writeState)
	return p, nil
}

func (p *Proxy) Addr() string                        { return p.cfg.Addr }
func (p *Proxy) Snapshot() pgwire.WireSnapshot       { return p.stats.Snapshot() }
func (p *Proxy) Counters() Counters                  { return p.c }
func (p *Proxy) ServerParams() []pgwire.Param        { return slices.Clone(p.params) }
func (p *Proxy) SetClientTLS(m pgwire.ClientTLSMode) { p.cfg.Client.TLS = m }

func (p *Proxy) quiesce() error {
	if p.c.Open != 0 || len(p.sessions) != 0 {
		return fmt.Errorf("%d proxy sessions open", p.c.Open)
	}
	if s := p.stats.Snapshot(); s.Client.InStartup != 0 || s.Database.InStartup != 0 {
		return fmt.Errorf("sessions in startup: client %d database %d", s.Client.InStartup, s.Database.InStartup)
	}
	return nil
}

func (p *Proxy) writeState(w sim.StateSink) {
	s := p.stats.Snapshot()
	w.Int(s.Client.StartupsBegun, s.Client.StartupsCompleted, s.Client.BytesIn, s.Client.BytesOut, s.Client.MessagesIn, s.Client.MessagesOut)
	w.Int(s.Client.StartupsFailed[:]...)
	w.Int(s.Database.StartupsBegun, s.Database.StartupsCompleted, s.Database.BytesIn, s.Database.BytesOut, s.Database.MessagesIn, s.Database.Replies)
	w.Int(s.Database.StartupsFailed[:]...)
	w.Int(p.c.Accepted, p.c.Closed, p.c.Open, p.c.Relayed, p.c.Queries, int64(p.keys.Issued()))
	for _, ss := range p.sessions {
		w.Int(ss.id, int64(ss.phase))
	}
}

type phase uint8

const (
	phaseStartup phase = iota
	phaseConnecting
	phaseRelay
	phaseClosed
)

type session struct {
	p       *Proxy
	id      int64
	phase   phase
	cend    *simnet.End
	client  *pgwire.ClientSession
	hello   []byte
	tlsWait bool
	hsTimer sim.Timer
	ddl     sim.Timer
	held    []byte
	early   []byte
	bend    *simnet.End
	backend *pgwire.BackendSession
	bTLS    bool
	bReply  []byte
	conn    sim.Timer
	front   *pgwire.FrontendTracker
	reply   *pgwire.ReplyTracker
	cout    []byte
	bout    []byte
	closing bool
}

func (p *Proxy) accept(e *simnet.End) {
	if len(p.sessions) >= p.cfg.MaxSessions {
		p.c.Refused++
		_ = e.Close()
		return
	}
	p.c.Accepted++
	p.c.Open++
	cfg := p.cfg.Client
	if p.params != nil {
		cfg.ServerParams = p.params
	}
	ss := &session{p: p, id: e.ID(), cend: e, client: pgwire.NewClientSession(cfg)}
	p.sessions = append(p.sessions, ss)
	p.s.Trace(kindAccept, ss.id, 0, 0, 0)
	ss.ddl = p.s.After(p.cfg.StartupTimeout, ss.startupDeadline)
	e.SetHandler(simnet.HandlerFunc(ss.onClient))
	ss.readClient()
}

func (ss *session) onClient(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvReadable:
		ss.readClient()
	case simnet.EvWritable:
		ss.flushClient()
		ss.readBackend()
	case simnet.EvReset:
		ss.clientGone()
	}
}

func (ss *session) onBackend(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		ss.backendActs(ss.backend.Start(nil))
		ss.readBackend()
	case simnet.EvRefused, simnet.EvReset:
		ss.backendGone()
	case simnet.EvReadable:
		ss.readBackend()
	case simnet.EvWritable:
		ss.flushBackend()
		ss.readClient()
	}
}

func (ss *session) readClient() {
	for ss.phase != phaseClosed && !ss.closing && len(ss.bout) < maxPending {
		n, err := ss.cend.Read(ss.p.buf[:])
		if n > 0 {
			ss.fromClient(ss.p.buf[:n])
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrClosed)) {
			ss.clientGone()
		}
		return
	}
}

func (ss *session) fromClient(b []byte) {
	for len(b) > 0 && ss.phase != phaseClosed && !ss.closing {
		switch {
		case ss.tlsWait:
			b = ss.takeHello(b)
		case ss.phase == phaseStartup && !ss.client.Ready():
			acts, n := ss.client.Receive(nil, b)
			b = b[n:]
			ss.clientActs(acts)
		case ss.phase == phaseConnecting:
			ss.early = append(ss.early, b...)
			return
		default:
			ss.relayFromClient(b)
			return
		}
	}
}

func (ss *session) takeHello(b []byte) []byte {
	k := min(simtls.HelloLen-len(ss.hello), len(b))
	ss.hello = append(ss.hello, b[:k]...)
	if len(ss.hello) < simtls.HelloLen || ss.hsTimer.Pending() {
		return nil
	}
	if !simtls.ValidClientHello(ss.hello) {
		ss.p.c.HandshakesBad++
		ss.tlsWait = false
		ss.writeClient(simtls.AppendServerReply(nil, simtls.ReplyAlert, 0))
		ss.clientActs(ss.client.HandshakeDone(nil, pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailed}))
		return nil
	}
	ss.hsTimer = ss.p.s.After(ss.p.cfg.Handshake, ss.handshakeDone)
	return b[k:]
}

func simVersion(v pgwire.TLSVersion) byte {
	if v == pgwire.TLSVersion12 {
		return simtls.Version12
	}
	return simtls.Version13
}

func (ss *session) handshakeDone() {
	if ss.phase == phaseClosed {
		return
	}
	ss.tlsWait = false
	ss.hello = nil
	ss.writeClient(simtls.AppendServerReply(nil, simtls.ReplyOK, simVersion(ss.p.cfg.TLSVersion)))
	ss.clientActs(ss.client.HandshakeDone(nil, pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: ss.p.cfg.TLSVersion}))
	ss.readClient()
}

func (ss *session) clientActs(acts []pgwire.Action) {
	hold := false
	for _, a := range acts {
		hold = hold || a.Kind == pgwire.ActReady
	}
	for _, a := range acts {
		switch a.Kind {
		case pgwire.ActWrite:
			if hold {
				ss.held = append(ss.held, a.Data...)
				continue
			}
			ss.writeClient(a.Data)
		case pgwire.ActStartTLS:
			ss.tlsWait = true
		case pgwire.ActCancel:
			ss.p.c.CancelsSeen++
		case pgwire.ActReady:
			ss.p.c.ClientReady++
			ss.p.s.Trace(kindReady, ss.id, int64(a.Key.ProcessID), 0, 0)
			ss.connect()
		case pgwire.ActClose:
			ss.p.c.ClientCloses[a.Reason]++
			ss.close(int64(a.Reason))
		}
	}
}

func (ss *session) startupDeadline() {
	if ss.phase != phaseStartup || ss.client.Ready() {
		return
	}
	ss.hsTimer.Cancel()
	ss.clientActs(ss.client.Deadline(nil))
}

func (ss *session) clientGone() {
	if ss.phase == phaseClosed {
		return
	}
	if ss.phase == phaseStartup && !ss.client.Ready() {
		ss.clientActs(ss.client.PeerClosed(nil))
		return
	}
	ss.close(-1)
}

func (ss *session) connect() {
	ss.phase = phaseConnecting
	ss.ddl.Cancel()
	ss.backend = pgwire.NewBackendSession(ss.p.cfg.Backend)
	ss.bend = ss.p.net.Dial(ss.p.cfg.Database, ss.p.cfg.Options, simnet.HandlerFunc(ss.onBackend))
	ss.conn = ss.p.s.After(ss.p.cfg.ConnectTimeout, ss.connectDeadline)
}

func (ss *session) connectDeadline() {
	if ss.phase != phaseConnecting {
		return
	}
	ss.backendActs(ss.backend.Deadline(nil))
}

func (ss *session) backendGone() {
	switch ss.phase {
	case phaseConnecting:
		ss.backendActs(ss.backend.PeerClosed(nil))
	case phaseRelay:
		ss.toClientEvents(ss.reply.PeerClosed(nil))
		ss.close(-2)
	}
}

func (ss *session) readBackend() {
	for ss.bend != nil && ss.phase != phaseClosed && len(ss.cout) < maxPending {
		n, err := ss.bend.Read(ss.p.buf[:])
		if n > 0 {
			ss.fromBackend(ss.p.buf[:n])
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrClosed)) {
			ss.backendGone()
		}
		return
	}
}

func (ss *session) fromBackend(b []byte) {
	for len(b) > 0 && ss.phase != phaseClosed {
		switch {
		case ss.bTLS:
			k := min(simtls.HelloLen-len(ss.bReply), len(b))
			ss.bReply = append(ss.bReply, b[:k]...)
			b = b[k:]
			if len(ss.bReply) == simtls.HelloLen {
				ss.bTLS = false
				ss.backendActs(ss.backend.HandshakeDone(nil, ss.backendHandshake(ss.bReply)))
			}
		case ss.phase == phaseConnecting:
			acts, n := ss.backend.Receive(nil, b)
			b = b[n:]
			ss.backendActs(acts)
		default:
			evs, n := ss.reply.Receive(nil, b)
			ss.writeClient(b[:n])
			b = b[n:]
			ss.toClientEvents(evs)
			if ss.reply.Closed() {
				return
			}
		}
	}
}

func (ss *session) backendHandshake(reply []byte) pgwire.HandshakeResult {
	r, v, ok := simtls.ParseServerReply(reply)
	switch {
	case !ok || r == simtls.ReplyAlert:
		return pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailed}
	case r == simtls.ReplyBadCertificate && ss.p.cfg.Backend.TLS == pgwire.BackendTLSVerifyFull:
		return pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailedVerification}
	}
	ver := pgwire.TLSVersion13
	if v == simtls.Version12 {
		ver = pgwire.TLSVersion12
	}
	return pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: ver}
}

func (ss *session) backendActs(acts []pgwire.Action) {
	for _, a := range acts {
		switch a.Kind {
		case pgwire.ActWrite:
			ss.writeBackend(a.Data)
		case pgwire.ActStartTLS:
			ss.bTLS = true
			ss.writeBackend(simtls.AppendClientHello(nil))
		case pgwire.ActReady:
			ss.backendReady(a)
		case pgwire.ActClose:
			ss.backendFailed(a)
		}
	}
}

func (ss *session) backendReady(a pgwire.Action) {
	ss.conn.Cancel()
	p := ss.p
	if p.params == nil {
		p.params = slices.Clone(a.Params)
	}
	p.c.Relayed++
	p.s.Trace(kindDBReady, ss.id, ss.bend.ID(), int64(a.Key.ProcessID), 0)
	ss.phase = phaseRelay
	ss.front = pgwire.NewFrontendTracker(p.cfg.Client.MaxMessage, &p.stats)
	ss.reply = pgwire.NewReplyTracker(p.cfg.Backend.MaxMessage, &p.stats)
	ss.writeClient(ss.held)
	ss.held = nil
	if len(ss.early) > 0 {
		early := ss.early
		ss.early = nil
		ss.relayFromClient(early)
	}
}

func (ss *session) backendFailed(a pgwire.Action) {
	ss.conn.Cancel()
	ss.p.c.BackendFailures[a.Class]++
	ss.p.s.Trace(kindDBFail, ss.id, int64(a.Class), 0, 0)
	ss.held = nil
	ss.writeClient(pgwire.AppendFatal(nil, pgwire.CodeConnectionFailure, "could not connect to the database: "+a.Class.String()))
	ss.close(100 + int64(a.Class))
}

func (ss *session) relayFromClient(b []byte) {
	evs, n := ss.front.Receive(nil, b)
	_ = n
	for _, ev := range evs {
		switch ev.Kind {
		case pgwire.FrontQuery:
			ss.p.c.Queries++
			ss.reply.QuerySent()
			ss.writeBackend(append(pgwire.AppendMessageHeader(nil, pgwire.MsgQuery, len(ev.Body)), ev.Body...))
		case pgwire.FrontTerminate:
			ss.writeBackend(pgwire.AppendTerminate(nil))
			ss.close(-3)
			return
		case pgwire.FrontExtended:
			ss.p.c.ExtendedRefused++
			ss.writeClient(pgwire.AppendFatal(nil, pgwire.CodeFeatureNotSupported, "extended query protocol is not supported"))
			ss.close(-4)
			return
		case pgwire.FrontViolation:
			ss.close(-5)
			return
		}
	}
}

func (ss *session) toClientEvents(evs []pgwire.CycleEvent) {
	for _, ev := range evs {
		switch ev.Kind {
		case pgwire.CycleReady:
			ss.p.c.Replies++
		case pgwire.CycleViolation:
			ss.close(-6)
			return
		case pgwire.CycleError:
			if ev.Severity == pgwire.SeverityFatal || ev.Severity == pgwire.SeverityPanic {
				ss.closing = true
			}
		}
	}
}

func (ss *session) writeClient(b []byte) {
	if len(b) == 0 || ss.phase == phaseClosed {
		return
	}
	ss.cout = append(ss.cout, b...)
	ss.flushClient()
}

func (ss *session) flushClient() {
	if len(ss.cout) == 0 {
		return
	}
	n, err := ss.cend.Write(ss.cout)
	if err != nil {
		ss.cout = nil
		return
	}
	ss.cout = ss.cout[n:]
	if len(ss.cout) > 0 {
		ss.cend.WaitWritable()
	}
}

func (ss *session) writeBackend(b []byte) {
	if len(b) == 0 || ss.bend == nil {
		return
	}
	ss.bout = append(ss.bout, b...)
	ss.flushBackend()
}

func (ss *session) flushBackend() {
	if len(ss.bout) == 0 || ss.bend == nil {
		return
	}
	n, err := ss.bend.Write(ss.bout)
	if err != nil {
		ss.bout = nil
		return
	}
	ss.bout = ss.bout[n:]
	if len(ss.bout) > 0 {
		ss.bend.WaitWritable()
	}
}

func (ss *session) close(why int64) {
	if ss.phase == phaseClosed {
		return
	}
	ss.flushClient()
	ss.flushBackend()
	if ss.phase == phaseConnecting && ss.backend != nil && !ss.backend.Closed() {
		ss.backend.PeerClosed(nil)
	}
	if ss.phase == phaseStartup && !ss.client.Ready() && !ss.client.Closed() {
		ss.client.PeerClosed(nil)
	}
	ss.phase = phaseClosed
	ss.ddl.Cancel()
	ss.hsTimer.Cancel()
	ss.conn.Cancel()
	_ = ss.cend.Close()
	if ss.bend != nil {
		_ = ss.bend.Close()
	}
	p := ss.p
	p.c.Closed++
	p.c.Open--
	p.s.Trace(kindClose, ss.id, why, 0, 0)
	if i := slices.Index(p.sessions, ss); i >= 0 {
		p.sessions = slices.Delete(p.sessions, i, i+1)
	}
}

func (p *Proxy) AuthenticatingBackends() []int64 {
	var out []int64
	for _, ss := range p.sessions {
		if ss.phase == phaseConnecting && ss.backend != nil && ss.backend.Authenticating() && ss.bend != nil {
			out = append(out, ss.bend.ID())
		}
	}
	return out
}
