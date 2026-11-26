package proxy

import (
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type dbPhase uint8

const (
	dpDialing dbPhase = iota
	dpStartup
	dpIdle
	dpLent
	dpResetting
	dpClosed
)

var dbPhaseNames = [...]string{"dialing", "startup", "idle", "lent", "resetting", "closed"}

func (p dbPhase) String() string {
	if int(p) < len(dbPhaseNames) {
		return dbPhaseNames[p]
	}
	return "?"
}

type dbConn struct {
	id         ConnID
	slot       int
	phase      dbPhase
	sess       *pgwire.BackendSession
	tlsFirst   bool
	decided    bool
	cycle      *pgwire.CycleTracker
	cur        msgCursor
	holding    bool
	hold       []byte
	client     *client
	lastTenant work.TenantID
	queued     int64
	acked      int64
	paused     bool
	resetAt    Instant
	judge      resetJudge
	connectT   *timer
	stmtT      *timer
	resetT     *timer
}

func (e *Engine) dialMore() {
	if e.dialing > 0 || e.stopping {
		return
	}
	slot := -1
	for i, d := range e.slots {
		if d == nil {
			slot = i
			break
		}
	}
	if slot < 0 {
		return
	}
	if e.now < e.dialAfter {
		if e.redialT == nil {
			e.t.arm(&e.redialT, e.dialAfter, tRedial, nil, nil)
		}
		return
	}
	e.nextDB++
	d := &dbConn{id: e.nextDB, slot: slot, phase: dpDialing, tlsFirst: e.cfg.Backend.TLS != pgwire.BackendTLSDisable}
	e.slots[slot] = d
	e.dbs[d.id] = d
	e.dialing++
	e.snap.DBDials++
	e.snap.DBConnecting.Add(1)
	e.emit(Action{Kind: ActDBOpen, Conn: d.id, TLSFirst: d.tlsFirst})
	e.t.arm(&d.connectT, e.after(e.cfg.Timeouts.DBConnect), tDBConnect, nil, d)
}

func (e *Engine) dbOpened(d *dbConn) {
	if d.phase != dpDialing {
		return
	}
	d.phase = dpStartup
	d.sess = pgwire.NewBackendSession(e.cfg.Backend)
	e.backendActs(d, d.sess.Start(nil))
}

func (e *Engine) dbTLS(d *dbConn, r pgwire.HandshakeResult) {
	if d.phase != dpStartup {
		return
	}
	e.backendActs(d, d.sess.HandshakeDone(nil, r))
	if d.phase == dpStartup && !d.sess.Closed() {
		e.emit(Action{Kind: ActDBResume, Conn: d.id})
	}
}

func (e *Engine) backendActs(d *dbConn, acts []pgwire.Action) {
	for _, a := range acts {
		if d.phase != dpStartup {
			return
		}
		switch a.Kind {
		case pgwire.ActWrite:
			e.writeDB(d, a.Data)
		case pgwire.ActStartTLS:
			d.decided = true
			e.emit(Action{Kind: ActDBStartTLS, Conn: d.id})
		case pgwire.ActReady:
			e.dbReady(d)
		case pgwire.ActClose:
			reason := CloseDBConnectFailed
			if a.Class == pgwire.ClassDeadline {
				reason = CloseDBConnectDeadline
			}
			e.dbConnectFailed(d, reason, a.Class)
		}
	}
}

func (e *Engine) writeDB(d *dbConn, data []byte) {
	if len(data) == 0 {
		return
	}
	d.queued += int64(len(data))
	e.emit(Action{Kind: ActDBWrite, Conn: d.id, Data: data})
}

func (e *Engine) dbReady(d *dbConn) {
	e.t.cancel(&d.connectT)
	e.dialing--
	e.snap.DBConnecting.Add(-1)
	e.snap.DBOpen.Add(1)
	e.snap.DBReady++
	e.lastFailed = false
	d.cycle = pgwire.NewCycleTracker(e.cfg.Backend.MaxMessage, &e.wire)
	if d.tlsFirst && !d.decided {
		d.decided = true
		e.emit(Action{Kind: ActDBResume, Conn: d.id})
	}
	e.makeIdle(d)
	e.dialMore()
	e.dispatch()
}

func (e *Engine) makeIdle(d *dbConn) {
	d.phase = dpIdle
	e.snap.DBIdle.Add(1)
	e.seams.Pool.Checkin(e.now, d.id)
}

func (e *Engine) dbConnectFailed(d *dbConn, reason CloseReason, class pgwire.Class) {
	if d.phase != dpDialing && d.phase != dpStartup {
		return
	}
	if class < pgwire.NumClasses {
		e.snap.DBConnectFails[class]++
	}
	if d.phase == dpDialing {
		e.snap.DBDialFailures++
	}
	e.lastFailed = true
	e.dialAfter = e.after(e.cfg.Timeouts.RedialDelay)
	e.closeDB(d, reason)
}

func (e *Engine) dbData(d *dbConn, b []byte) {
	for len(b) > 0 {
		switch d.phase {
		case dpStartup:
			acts, n := d.sess.Receive(nil, b)
			b = b[n:]
			e.backendActs(d, acts)
			if d.tlsFirst && !d.decided && d.phase == dpStartup && !d.sess.AwaitingTLS() {
				d.decided = true
				e.emit(Action{Kind: ActDBResume, Conn: d.id})
			}
			if n == 0 && d.phase == dpStartup {
				return
			}
		case dpLent:
			b = e.relay(d, b)
		case dpResetting:
			b = e.judgeReset(d, b)
		case dpIdle:
			e.closeDB(d, CloseDBUnsolicited)
			return
		default:
			return
		}
	}
}

func (e *Engine) dbClosed(d *dbConn) {
	switch d.phase {
	case dpDialing, dpStartup:
		if d.sess != nil {
			e.backendActs(d, d.sess.PeerClosed(nil))
		}
		e.dbConnectFailed(d, CloseDBConnectFailed, pgwire.ClassLost)
	case dpIdle:
		e.closeDB(d, CloseDBLost)
	case dpResetting:
		e.resetFailed(d, ResetFailConnLost)
	case dpLent:
		if d.cycle != nil {
			d.cycle.PeerClosed(nil)
		}
		e.leaseFailed(d, CloseDBLost)
	}
}

func (e *Engine) dbTimer(t *timer) {
	d := t.db
	switch t.kind {
	case tDBConnect:
		d.connectT = nil
		if d.phase == dpDialing {
			e.dbConnectFailed(d, CloseDBConnectDeadline, pgwire.ClassDeadline)
			return
		}
		if d.phase == dpStartup {
			e.backendActs(d, d.sess.Deadline(nil))
		}
	case tDBStatement:
		d.stmtT = nil
		if d.phase == dpLent {
			e.leaseFailed(d, CloseDBStatementDeadline)
		}
	case tDBReset:
		d.resetT = nil
		if d.phase == dpResetting {
			e.resetFailed(d, ResetFailDeadline)
		}
	}
}

func (e *Engine) closeDB(d *dbConn, reason CloseReason) {
	if d.phase == dpClosed {
		return
	}
	for _, slot := range []**timer{&d.connectT, &d.stmtT, &d.resetT} {
		e.t.cancel(slot)
	}
	switch d.phase {
	case dpDialing, dpStartup:
		if d.sess != nil && !d.sess.Closed() {
			d.sess.PeerClosed(nil)
		}
		e.dialing--
		e.snap.DBConnecting.Add(-1)
	case dpIdle:
		e.seams.Pool.Remove(d.id)
		e.snap.DBIdle.Add(-1)
		e.snap.DBOpen.Add(-1)
	case dpLent:
		e.snap.DBLent.Add(-1)
		e.snap.DBOpen.Add(-1)
	case dpResetting:
		e.snap.Resetting.Add(-1)
		e.snap.DBOpen.Add(-1)
	}
	d.phase = dpClosed
	e.snap.DBCloses[reason]++
	delete(e.dbs, d.id)
	if d.slot >= 0 && d.slot < len(e.slots) && e.slots[d.slot] == d {
		e.slots[d.slot] = nil
	}
	e.emit(Action{Kind: ActDBClose, Conn: d.id, Reason: reason})
	e.dialMore()
}
