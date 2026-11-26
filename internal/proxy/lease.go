package proxy

import (
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const discardAll = "DISCARD ALL"

func (e *Engine) startRequest(c *client, body []byte) {
	e.snap.Received++
	e.t.cancel(&c.idleT)
	e.pause(c)
	text := body
	if n := len(text); n > 0 && text[n-1] == 0 {
		text = text[:n-1]
	}
	inTx := c.status != pgwire.StatusIdle
	u := e.seams.Classifier.Classify(c.tenant, text, inTx)
	e.snap.StagePassed[StageClassify]++
	if u.Class == work.ClassReject && e.cfg.Plant&PlantSkipCourtesy == 0 {
		e.snap.Received--
		e.reject(c, pgwire.ErrRowUnsupportedFeature, false)
		return
	}
	e.snap.StagePassed[StageReject]++
	e.nextReq++
	req := &request{id: e.nextReq, msg: pgwire.AppendQuery(nil, text), unit: u, inTx: inTx, enq: e.now}
	if inTx && c.db != nil {
		e.snap.Accepted++
		c.req = req
		e.forward(c)
		return
	}
	if !e.gates(c) {
		return
	}
	e.snap.Accepted++
	c.req = req
	if e.seams.Coalescer.Join(e.now, req.id, &req.unit) {
		e.snap.StagePassed[StageCoalesce]++
		return
	}
	e.snap.StagePassed[StageCoalesce]++
	e.enqueue(c)
}

func (e *Engine) gates(c *client) bool {
	refuse := func(stage Stage, row pgwire.ErrRowID) bool {
		e.snap.Rejected++
		e.snap.StageRefuse[stage]++
		e.reply(c, row)
		return false
	}
	if e.draining {
		return refuse(StageDraining, pgwire.ErrRowDraining)
	}
	e.snap.StagePassed[StageDraining]++
	if !e.seams.Breaker.Allow(e.now) {
		return refuse(StageBreaker, pgwire.ErrRowBreakerOpen)
	}
	e.snap.StagePassed[StageBreaker]++
	if !e.seams.Rate.Allow(e.now, c.tenant) {
		return refuse(StageRate, pgwire.ErrRowRateGate)
	}
	e.snap.StagePassed[StageRate]++
	return true
}

func (e *Engine) enqueue(c *client) {
	req := c.req
	if e.seams.Queue.Len() == 0 && e.seams.Pool.Idle() > 0 {
		if id, ok := e.seams.Pool.Checkout(e.now, c.tenant); ok {
			e.snap.StagePassed[StageQueue]++
			e.queueHist.Record(0)
			e.lend(c, e.dbs[id])
			return
		}
	}
	if !e.seams.Queue.Push(e.now, c.tenant, req.id) {
		e.snap.StageRefuse[StageQueue]++
		e.shed(c)
		return
	}
	c.phase = cpQueued
	e.waiting[req.id] = c
	e.snap.Queued.Add(1)
	e.t.arm(&c.queueT, e.after(e.cfg.Timeouts.QueueWait), tQueueWait, c, nil)
	e.dispatch()
}

func (e *Engine) shed(c *client) {
	e.snap.Shed++
	c.req = nil
	c.phase = cpIdle
	e.reply(c, pgwire.ErrRowShed)
}

func (e *Engine) queueExpired(c *client) {
	if c.phase != cpQueued || c.req == nil {
		return
	}
	e.seams.Queue.Remove(c.req.id)
	delete(e.waiting, c.req.id)
	e.snap.Queued.Add(-1)
	e.snap.StageRefuse[StageQueue]++
	e.shed(c)
	e.pump(c)
}

func (e *Engine) dispatch() {
	for e.seams.Queue.Len() > 0 && e.seams.Pool.Idle() > 0 {
		id, ok := e.seams.Queue.Pop(e.now)
		if !ok {
			return
		}
		c := e.waiting[id]
		delete(e.waiting, id)
		if c == nil || c.phase != cpQueued || c.req == nil || c.req.id != id {
			continue
		}
		dbID, ok := e.seams.Pool.Checkout(e.now, c.tenant)
		if !ok {
			e.seams.Queue.Push(e.now, c.tenant, id)
			e.waiting[id] = c
			return
		}
		e.t.cancel(&c.queueT)
		e.snap.Queued.Add(-1)
		e.snap.StagePassed[StageQueue]++
		e.queueHist.Record(int64(e.now - c.req.enq))
		e.lend(c, e.dbs[dbID])
	}
}

func (e *Engine) lend(c *client, d *dbConn) {
	e.snap.StagePassed[StageLease]++
	e.snap.LeasesStarted++
	e.snap.DBIdle.Add(-1)
	e.snap.DBLent.Add(1)
	d.phase = dpLent
	d.client = c
	d.lastTenant = c.tenant
	c.db = d
	e.forward(c)
}

func (e *Engine) forward(c *client) {
	d := c.db
	req := c.req
	e.snap.StagePassed[StageForward]++
	e.snap.InFlight.Add(1)
	c.phase = cpActive
	req.sent = e.now
	req.start = d.queued
	req.relayed = 0
	d.cycle.QuerySent()
	e.writeDB(d, req.msg)
	e.t.arm(&d.stmtT, e.after(e.cfg.Timeouts.Statement), tDBStatement, nil, d)
}

func (e *Engine) relay(d *dbConn, b []byte) []byte {
	for len(b) > 0 && d.phase == dpLent {
		if d.cur.atStart() {
			d.holding = b[0] == pgwire.MsgErrorResponse || b[0] == pgwire.MsgReadyForQuery
		}
		n, done := d.cur.next(b)
		seg := b[:n]
		b = b[n:]
		if d.cur.bad {
			evs, _ := d.cycle.Receive(nil, seg)
			_ = evs
			e.leaseFailed(d, CloseDBViolation)
			return nil
		}
		if d.holding {
			if len(d.hold)+len(seg) > e.holdCap() {
				e.leaseFailed(d, CloseDBViolation)
				return nil
			}
			d.hold = append(d.hold, seg...)
			if !done {
				continue
			}
			held := d.hold
			d.hold = nil
			evs, _ := d.cycle.Receive(nil, held)
			e.relayEvents(d, evs, held)
			continue
		}
		evs, _ := d.cycle.Receive(nil, seg)
		if c := d.client; c != nil {
			e.relayed(c, seg)
		}
		e.relayEvents(d, evs, nil)
	}
	return b
}

func (e *Engine) holdCap() int {
	m := e.cfg.Backend.MaxMessage
	if m <= 0 {
		m = pgwire.DefaultMaxMessage
	}
	return m + headerLen
}

func (e *Engine) relayed(c *client, seg []byte) {
	if c.req != nil {
		c.req.relayed += int64(len(seg))
	}
	e.writeClient(c, append([]byte(nil), seg...))
	if d := c.db; d != nil && !d.paused && c.out > int64(e.cfg.ReplyBufferCap-MaxRead) {
		d.paused = true
		e.snap.RelayPauses++
		e.emit(Action{Kind: ActDBPause, Conn: d.id})
	}
}

func (e *Engine) resumeDB(d *dbConn) {
	if d.paused {
		d.paused = false
		e.emit(Action{Kind: ActDBResume, Conn: d.id})
	}
}

func (e *Engine) relayEvents(d *dbConn, evs []pgwire.CycleEvent, held []byte) {
	for _, ev := range evs {
		if d.phase != dpLent {
			return
		}
		c := d.client
		switch ev.Kind {
		case pgwire.CycleViolation:
			e.leaseFailed(d, CloseDBViolation)
			return
		case pgwire.CycleError:
			if ev.Severity == pgwire.SeverityFatal || ev.Severity == pgwire.SeverityPanic {
				e.leaseFailed(d, CloseDBServerFatal)
				return
			}
			e.relayed(c, held)
		case pgwire.CycleParameterStatus:
			e.snap.ParamStatusTurn++
		case pgwire.CycleReady:
			if c.req == nil {
				e.leaseFailed(d, CloseDBViolation)
				return
			}
			e.relayed(c, held)
			e.replyDone(d, c, ev.Status)
		}
	}
}

func (e *Engine) replyDone(d *dbConn, c *client, status byte) {
	req := c.req
	c.req = nil
	e.t.cancel(&d.stmtT)
	e.snap.Completed++
	e.snap.InFlight.Add(-1)
	e.stmtHist.Record(int64(e.now - req.sent))
	c.status = status
	if status == pgwire.StatusIdle || e.cfg.Plant&PlantReleaseOnAnyReady != 0 {
		c.phase = cpIdle
		e.setInTx(c, false)
		e.release(d, c, status)
	} else {
		c.phase = cpHeld
		e.setInTx(c, true)
	}
	e.pump(c)
}

func (e *Engine) release(d *dbConn, c *client, status byte) {
	e.snap.StagePassed[StageRelease]++
	e.snap.LeasesEnded++
	d.client = nil
	c.db = nil
	in := ReleaseInput{Conn: d.id, Tenant: c.tenant, LastTenant: d.lastTenant, Status: status, Unread: len(d.hold) > 0 || d.cur.mid()}
	act := e.seams.Release.AtRelease(e.now, in)
	if e.cfg.Plant&(PlantNoReset|PlantReleaseOnAnyReady) != 0 {
		act = ReleaseLendAsIs
	}
	if act != ReleaseReset && act != ReleaseLendAsIs {
		e.snap.LeasesEndedClose++
		e.closeDB(d, CloseDBReleaseDirty)
		return
	}
	e.snap.DBLent.Add(-1)
	e.resumeDB(d)
	if act == ReleaseReset {
		e.snap.LeasesEndedReset++
		e.startReset(d)
		return
	}
	e.snap.LeasesEndedAsIs++
	e.makeIdle(d)
	e.dispatch()
}

func (e *Engine) leaseLost(d *dbConn) {
	if d.phase != dpLent {
		return
	}
	e.snap.LeasesEnded++
	e.snap.LeasesEndedClose++
	if c := d.client; c != nil && c.db == d {
		c.db = nil
	}
	d.client = nil
}

func (e *Engine) leaseFailed(d *dbConn, reason CloseReason) {
	c := d.client
	e.leaseLost(d)
	e.closeDB(d, reason)
	if c == nil || c.phase == cpClosed {
		return
	}
	e.setInTx(c, false)
	switch c.phase {
	case cpHeld:
		c.status = pgwire.StatusIdle
		e.snap.NonRequestReplies++
		e.reply(c, pgwire.ErrRowLostInTransaction)
	case cpActive:
		e.statementLost(c, d)
	}
}

func (e *Engine) statementLost(c *client, d *dbConn) {
	req := c.req
	c.req = nil
	e.snap.InFlight.Add(-1)
	e.snap.Failed++
	reached := work.No
	if d.acked > req.start || req.relayed > 0 {
		reached = work.Yes
	}
	e.reached(reached)
	row := lostRow(req, reached)
	if e.seams.Retry.Retry(e.now, Failure{Request: req.id, Tenant: c.tenant, Reached: reached, Row: uint8(row), InTx: req.inTx}) {
		e.snap.RetryAsked++
	}
	c.phase = cpIdle
	c.status = pgwire.StatusIdle
	e.reply(c, row)
	if c.phase != cpClosed {
		e.pump(c)
	}
}

func lostRow(req *request, reached work.Tri) pgwire.ErrRowID {
	switch {
	case req.relayed > 0:
		return pgwire.ErrRowPartialReply
	case req.inTx && reached == work.No:
		return pgwire.ErrRowLostInTransaction
	case req.inTx:
		return pgwire.ErrRowLostAfterWrite
	case reached == work.No || req.unit.SafeRead:
		return pgwire.ErrRowLostAfterRead
	}
	return pgwire.ErrRowLostAfterWrite
}

func (e *Engine) startReset(d *dbConn) {
	e.snap.ResetsStarted++
	d.resetAt = e.now
	d.judge = resetJudge{}
	d.judge.fr.Max = MaxResetReply
	e.writeDB(d, pgwire.AppendQuery(nil, []byte(discardAll)))
	if e.cfg.Plant&PlantLendBeforeResetReply != 0 {
		e.snap.ResetsCompleted++
		e.makeIdle(d)
		e.dispatch()
		return
	}
	d.phase = dpResetting
	e.snap.Resetting.Add(1)
	e.t.arm(&d.resetT, e.after(e.cfg.Timeouts.Reset), tDBReset, nil, d)
}

type resetJudge struct {
	fr     pgwire.Framer
	sawTag bool
}

type resetVerdict uint8

const (
	rvMore resetVerdict = iota
	rvDone
	rvFail
)

func (j *resetJudge) take(b []byte) (int, resetVerdict, ResetFailure) {
	n := 0
	for n < len(b) {
		k, done, err := j.fr.Take(b[n:])
		n += k
		if err != nil {
			return n, rvFail, ResetFailSurprise
		}
		if !done {
			continue
		}
		f := j.fr.Frame()
		switch f.Type {
		case pgwire.MsgParameterStatus, pgwire.MsgNoticeResponse:
		case pgwire.MsgErrorResponse:
			return n, rvFail, ResetFailError
		case pgwire.MsgCommandComplete:
			if j.sawTag || string(f.Body) != discardAll+"\x00" {
				return n, rvFail, ResetFailWrongTag
			}
			j.sawTag = true
		case pgwire.MsgReadyForQuery:
			if !j.sawTag {
				return n, rvFail, ResetFailWrongTag
			}
			if len(f.Body) != 1 || f.Body[0] != pgwire.StatusIdle {
				return n, rvFail, ResetFailWrongStatus
			}
			return n, rvDone, 0
		default:
			return n, rvFail, ResetFailSurprise
		}
	}
	return n, rvMore, 0
}

func (e *Engine) judgeReset(d *dbConn, b []byte) []byte {
	n, v, why := d.judge.take(b)
	switch v {
	case rvFail:
		e.resetFailed(d, why)
		return nil
	case rvDone:
		e.t.cancel(&d.resetT)
		e.snap.Resetting.Add(-1)
		e.snap.ResetsCompleted++
		e.resetHist.Record(int64(e.now - d.resetAt))
		e.makeIdle(d)
		e.dispatch()
	}
	return b[n:]
}

func (e *Engine) resetFailed(d *dbConn, why ResetFailure) {
	e.snap.ResetsFailed[why]++
	e.closeDB(d, CloseDBResetFailed)
}
