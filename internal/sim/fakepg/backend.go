package fakepg

import (
	"io"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

type phase uint8

const (
	phaseStartup phase = iota
	phaseReady
	phaseClosed
)

type afterAction uint8

const (
	afterNone afterAction = iota
	afterClose
	afterReset
	afterDelayedReset
)

type runState uint8

const (
	runWaiting runState = iota + 1
	runServing
	runMuted
	runHeld
	runDone
)

type run struct {
	st        Stmt
	seq       int64
	txID      int64
	start     sim.Time
	state     runState
	timer     sim.Timer
	remaining time.Duration
	slot      bool
	cancelled bool
	readVal   string
	readFound bool
	described bool
	tooLarge  bool
	rows      int64
}

type segment struct {
	data    []byte
	rows    int64
	size    int64
	unit    string
	second  []byte
	readyAt sim.Time
	trickle time.Duration
}

type backend struct {
	srv         *Server
	end         *simnet.End
	id          int64
	pid         uint32
	secret      []byte
	phase       phase
	counted     bool
	refuse      bool
	refuseAs    Refusal
	muteStartup bool
	muteQueries bool
	slowExtra   time.Duration
	user        string
	appName     string
	db          string
	fr          Framer
	eof         bool
	settings    []setting
	reported    []setting
	tx          *txn
	msg         []Stmt
	msgIdx      int
	msgImplicit bool
	firstReply  bool
	cur         *run
	out         []segment
	outBuf      []byte
	rowBuf      []byte
	lastReady   sim.Time
	written     int64
	cutAt       int64
	after       afterAction
	pumpTimer   sim.Timer
	resetTimer  sim.Timer
	arm         [numPoints]bool
	garbage     bool
	garbageMode int64
	resetHeld   bool
	resetLeft   time.Duration
	dying       bool
	stream      *rand.Rand
	step        startStep
	overTLS     bool
	tlsTimer    sim.Timer
	scram       *pgwire.ScramServer
}

func (b *backend) OnEvent(_ *simnet.End, ev simnet.Event) {
	if ev == simnet.EvReset {
		b.finalize()
		return
	}
	b.advance()
}

func (b *backend) status() byte {
	switch {
	case b.tx == nil || !b.tx.explicit:
		return 'I'
	case b.tx.failed:
		return 'E'
	}
	return 'T'
}

func (b *backend) advance() {
	for b.phase != phaseClosed {
		if b.srv.faults.frozen || b.dying {
			return
		}
		if b.cur != nil && b.cur.state == runDone {
			b.finishStatement()
			continue
		}
		// pump returns false when simnet's send window is full (back pressure) or
		// delayed output exceeds MaxDelayedSegments; pause reading until writable.
		if !b.pump() {
			return
		}
		if b.after != afterNone && (len(b.out) > 0 || len(b.outBuf) > 0) {
			return
		}
		if b.after != afterNone && b.after != afterDelayedReset {
			b.doAfter()
			return
		}
		if b.after == afterDelayedReset {
			b.after = afterNone
			b.scheduleReset()
		}
		if b.cur != nil {
			if b.cur.state == runMuted {
				b.drainMuted()
				return
			}
			return
		}
		if b.msg != nil {
			b.startNext()
			continue
		}
		if !b.readMessage() {
			return
		}
	}
}

func (b *backend) readMore() (bool, bool) {
	n, err := b.end.Read(b.srv.readBuf[:])
	if n > 0 {
		b.srv.stats.bytesIn += int64(n)
		return true, false
	}
	switch err {
	case nil:
		return false, false
	case io.EOF:
		b.eof = true
		return false, true
	}
	b.finalize()
	return false, true
}

func (b *backend) drainMuted() {
	for b.phase != phaseClosed {
		got, ended := b.readMore()
		if ended {
			if b.phase != phaseClosed {
				b.after = afterClose
				b.doAfter()
			}
			return
		}
		if !got {
			return
		}
	}
}

func (b *backend) readMessage() bool {
	for {
		if b.phase == phaseStartup && b.muteStartup {
			b.drainMuted()
			return false
		}
		if b.phase == phaseStartup && b.step == stepHello {
			return b.readHello()
		}
		if b.phase == phaseStartup && b.step == stepHandshake {
			return b.watchHandshake()
		}
		typ, body, ok, err := b.fr.Next()
		if err != nil {
			b.protocolFatal(err.Error())
			return true
		}
		if ok {
			b.dispatch(typ, body)
			return true
		}
		if b.eof {
			b.after = afterClose
			return true
		}
		n, rerr := b.end.Read(b.srv.readBuf[:])
		if n > 0 {
			b.srv.stats.bytesIn += int64(n)
			b.fr.Feed(b.srv.readBuf[:n])
			continue
		}
		if rerr == io.EOF {
			b.eof = true
			continue
		}
		if rerr != nil {
			b.finalize()
			return true
		}
		return false
	}
}

func (b *backend) dispatch(typ byte, body []byte) {
	if b.phase == phaseStartup {
		if b.step == stepSASLFirst || b.step == stepSASLFinal {
			b.authMessage(typ, body)
			return
		}
		b.startup(body)
		return
	}
	switch typ {
	case 'Q':
		q, err := decodeQuery(body)
		if err != nil {
			b.protocolFatal("invalid query message")
			return
		}
		b.beginMessage(q)
	case 'X':
		b.after = afterClose
	default:
		b.protocolFatal("invalid frontend message type " + strconv.Itoa(int(typ)))
	}
}

func (b *backend) protocolFatal(msg string) {
	b.emit(encError(nil, errorf("FATAL", "08P01", "%s", msg)))
	b.after = afterClose
}

func (b *backend) refuseWith(r Refusal, e pgError) {
	b.srv.stats.refused[r]++
	b.srv.s.Trace(kindRefuse, b.id, int64(r), CodeValue(e.code), 0)
	b.emit(encError(nil, e))
	b.after = afterClose
}

func (b *backend) startup(body []byte) {
	pkt, err := decodeStartup(body)
	if err != nil {
		b.refuseWith(RefuseBadStartup, errorf("FATAL", "08P01", "invalid startup packet layout"))
		return
	}
	// Follow Postgres's startup refusal order: encryption negotiation first, then
	// cancel request (uncounting its slot), protocol version, restart window,
	// max_connections, missing/unknown user, and unknown database.
	switch pkt.kind {
	case startupSSL:
		b.answerSSL()
		return
	case startupGSS:
		b.srv.auth.stats.GSSAnsweredNo++
		b.emit([]byte{'N'})
		return
	case startupCancel:
		b.uncount()
		b.srv.cancel(pkt.pid, pkt.secret)
		b.after = afterClose
		return
	}
	if pkt.version != protocol30 {
		b.refuseProtocol(pkt.version)
		return
	}
	if pkt.negotiate || len(pkt.unknown) > 0 {
		b.emit(pgwire.AppendNegotiateProtocolVersion(nil, protocol30, pkt.unknown))
	}
	if b.refuse {
		if b.refuseAs == RefuseStartingUp {
			b.refuseWith(RefuseStartingUp, errorf("FATAL", "57P03", "the database system is starting up"))
			return
		}
		b.refuseWith(RefuseTooMany, errorf("FATAL", "53300", "sorry, too many clients already"))
		return
	}
	if pkt.user == "" {
		b.refuseWith(RefuseNoUser, errorf("FATAL", "28000", "no PostgreSQL user name specified in startup packet"))
		return
	}
	if pkt.db == "" {
		pkt.db = pkt.user
	}
	if b.srv.cfg.Auth == AuthSCRAM {
		b.user, b.db, b.appName = pkt.user, pkt.db, pkt.appName
		b.beginAuth()
		return
	}
	if !allowed(b.srv.cfg.Users, pkt.user) {
		b.refuseWith(RefuseUnknownUser, errorf("FATAL", "28000", "role %q does not exist", pkt.user))
		return
	}
	if !allowed(b.srv.cfg.Databases, pkt.db) {
		b.refuseWith(RefuseUnknownDatabase, errorf("FATAL", "3D000", "database %q does not exist", pkt.db))
		return
	}
	b.user, b.db, b.appName = pkt.user, pkt.db, pkt.appName
	b.ready(pkt.appName)
}

func allowed(list []string, name string) bool {
	if len(list) == 0 {
		return true
	}
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func (b *backend) ready(appName string) {
	b.phase = phaseReady
	b.fr.Startup = false
	b.srv.stats.startups++
	b.settings = b.srv.defaultSettings(b.user, appName)
	b.reported = reportedOf(b.settings)
	b.srv.s.Trace(kindReady, b.id, int64(b.pid), 0, 0)
	if b.garbled() {
		return
	}
	out := encAuthOK(nil)
	for _, s := range b.reported {
		out = encParam(out, s.name, s.value)
	}
	out = encKey(out, b.pid, b.secret)
	out = encReady(out, 'I')
	b.emit(out)
}

func (b *backend) uncount() {
	if b.counted {
		b.counted = false
		b.srv.stats.sessions.Add(-1)
	}
}

func (b *backend) emit(data []byte) {
	at := b.srv.s.Now()
	if d := b.srv.faults.asym; d > 0 {
		at = sim.Later(at, d)
	}
	if b.firstReply {
		b.firstReply = false
		if d := b.srv.faults.firstByte; d > 0 {
			at = sim.Later(at, d)
		}
	}
	if at < b.lastReady {
		at = b.lastReady
	}
	b.lastReady = at
	b.out = append(b.out, segment{data: data, readyAt: at})
}

func (b *backend) emitRows(unit string, size, rows int64, second []byte) {
	if rows <= 0 {
		return
	}
	b.emit(nil)
	seg := &b.out[len(b.out)-1]
	seg.unit, seg.size, seg.rows, seg.second, seg.trickle = unit, size, rows, second, b.srv.faults.trickle
}

func (b *backend) pump() bool {
	now := b.srv.s.Now()
	for {
		if b.cutAt > 0 && b.written >= b.cutAt {
			b.cutAt = 0
			b.dying = true
			b.out, b.outBuf = nil, nil
			b.scheduleReset()
			return false
		}
		if len(b.outBuf) > 0 {
			limit := len(b.outBuf)
			if b.cutAt > 0 && int64(limit) > b.cutAt-b.written {
				limit = int(b.cutAt - b.written)
			}
			n, err := b.end.Write(b.outBuf[:limit])
			b.written += int64(n)
			b.srv.stats.bytesOut += int64(n)
			b.outBuf = b.outBuf[n:]
			if err != nil {
				b.finalize()
				return false
			}
			if n < limit {
				return false
			}
			continue
		}
		if len(b.out) == 0 {
			b.outBuf = nil
			return true
		}
		seg := &b.out[0]
		if now < seg.readyAt {
			if !b.pumpTimer.Pending() {
				b.pumpTimer = b.srv.s.At(seg.readyAt, b.advance)
			}
			return len(b.out) < MaxDelayedSegments
		}
		if seg.rows == 0 {
			b.outBuf = seg.data
			b.out = b.out[1:]
			continue
		}
		// Stream large row sets in outFill-sized batches (or one row at a time
		// under FaultTrickle) so a 200,000-row reply stays bounded in memory.
		b.rowBuf = b.rowBuf[:0]
		for seg.rows > 0 && (len(b.rowBuf) == 0 || (seg.trickle == 0 && len(b.rowBuf) < outFill)) {
			b.rowBuf = appendRepeatRow(b.rowBuf, seg.unit, seg.size, seg.second)
			seg.rows--
		}
		b.outBuf = b.rowBuf
		if seg.trickle > 0 {
			seg.readyAt = sim.Later(now, seg.trickle)
		}
		if seg.rows == 0 {
			b.out = b.out[1:]
		}
	}
}

func (b *backend) doAfter() {
	switch b.after {
	case afterClose:
		_ = b.end.Close()
	case afterReset:
		_ = b.end.Reset()
	}
	b.finalize()
}

func (b *backend) scheduleReset() {
	if b.resetTimer.Pending() || b.resetHeld {
		return
	}
	if b.srv.faults.frozen {
		b.resetHeld, b.resetLeft = true, b.srv.cfg.AfterDelay
		return
	}
	b.resetTimer = b.srv.s.After(b.srv.cfg.AfterDelay, b.delayedReset)
}

func (b *backend) delayedReset() {
	if b.phase == phaseClosed {
		return
	}
	_ = b.end.Reset()
	b.finalize()
}

func (b *backend) finalize() {
	if b.phase == phaseClosed {
		return
	}
	srv := b.srv
	b.phase = phaseClosed
	b.pumpTimer.Cancel()
	b.resetTimer.Cancel()
	b.tlsTimer.Cancel()
	if r := b.cur; r != nil {
		b.leaveService(r)
		b.cur = nil
		srv.stats.aborted++
		srv.logStatement(b, r, "", true)
	}
	if b.tx != nil {
		srv.logTx(b, b.tx, TxLost)
		srv.stats.txLost++
		srv.stats.txOpen--
		b.tx = nil
	}
	b.uncount()
	srv.stats.open.Add(-1)
	srv.stats.closed++
	srv.remove(b)
	srv.s.Trace(kindClose, b.id, int64(b.pid), b.written, 0)
	b.out, b.outBuf, b.msg = nil, nil, nil
}
