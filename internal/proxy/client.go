package proxy

import (
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type clientPhase uint8

const (
	cpStartup clientPhase = iota
	cpIdle
	cpQueued
	cpActive
	cpHeld
	cpClosed
)

const maxClientInput = 4 * MaxRead

type request struct {
	id      RequestID
	msg     []byte
	unit    work.Unit
	inTx    bool
	enq     Instant
	sent    Instant
	start   int64
	relayed int64
}

type client struct {
	id       ConnID
	idx      int
	phase    clientPhase
	sess     *pgwire.ClientSession
	seen     int
	decided  bool
	tenant   work.TenantID
	tlsIdx   int
	counted  bool
	front    *pgwire.FrontendTracker
	cur      msgCursor
	sync     pgwire.SyncDiscard
	inbuf    []byte
	status   byte
	inTx     bool
	db       *dbConn
	req      *request
	out      int64
	paused   bool
	startupT *timer
	idleT    *timer
	midT     *timer
	writeT   *timer
	queueT   *timer
}

func (e *Engine) accept(id ConnID) {
	if e.clients[id] != nil {
		return
	}
	if len(e.clients) >= e.cfg.MaxClients || e.snap.ClientsInStartup.Value >= int64(e.cfg.MaxStartup) {
		e.snap.ClientsRefused++
		e.snap.NonRequestReplies++
		e.snap.ByRow[pgwire.ErrRowTooManyClients]++
		out, _, _ := e.replies.Build(nil, pgwire.ErrRowTooManyClients, pgwire.StatusIdle)
		e.emit(Action{Kind: ActClientWrite, Conn: id, Data: out})
		e.emit(Action{Kind: ActClientClose, Conn: id, Reason: CloseClientTooMany})
		e.snap.ClientCloses[CloseClientTooMany]++
		return
	}
	c := &client{id: id, idx: len(e.clientList), sess: pgwire.NewClientSession(e.cfg.Client), status: pgwire.StatusIdle}
	e.clients[id] = c
	e.clientList = append(e.clientList, c)
	e.snap.ClientsAccepted++
	e.snap.ClientsConnected.Add(1)
	e.snap.ClientsInStartup.Add(1)
	e.t.arm(&c.startupT, e.after(e.cfg.Timeouts.ClientStartup), tClientStartup, c, nil)
}

func (e *Engine) clientData(c *client, b []byte) {
	if c.phase == cpStartup {
		e.startupData(c, b)
		return
	}
	if len(c.inbuf)+len(b) > maxClientInput {
		e.clientViolation(c)
		return
	}
	c.inbuf = append(c.inbuf, b...)
	e.pump(c)
}

func (e *Engine) startupData(c *client, b []byte) {
	acts, n := c.sess.Receive(nil, b)
	c.seen += n
	e.startupActs(c, acts)
	if c.phase == cpClosed {
		return
	}
	if !c.decided && (c.seen >= FirstClientRead || c.phase != cpStartup) {
		c.decided = true
		if !c.sess.AwaitingTLS() {
			e.emit(Action{Kind: ActClientResume, Conn: c.id})
		}
	}
	if c.phase != cpStartup && n < len(b) {
		e.clientData(c, b[n:])
	}
}

func (e *Engine) startupActs(c *client, acts []pgwire.Action) {
	for _, a := range acts {
		if c.phase == cpClosed {
			return
		}
		switch a.Kind {
		case pgwire.ActWrite:
			e.writeClient(c, a.Data)
		case pgwire.ActStartTLS:
			c.decided = true
			e.emit(Action{Kind: ActClientStartTLS, Conn: c.id})
		case pgwire.ActCancel:
			e.snap.CancelRequests++
			if e.core != nil {
				e.clientCancel(a.Key)
			}
		case pgwire.ActReady:
			e.clientReady(c, a)
		case pgwire.ActClose:
			reason := CloseClientStartup
			if a.Reason == pgwire.ReasonCancelRequest {
				reason = CloseClientCancelRequest
			}
			e.closeClient(c, reason)
		}
	}
}

func (e *Engine) clientTLS(c *client, r pgwire.HandshakeResult) {
	if c.phase != cpStartup {
		return
	}
	if r.Outcome == pgwire.HandshakeCompleted {
		c.tlsIdx = tlsIndex(r.Version)
	}
	e.startupActs(c, c.sess.HandshakeDone(nil, r))
	if c.phase == cpStartup && !c.sess.Closed() {
		e.emit(Action{Kind: ActClientResume, Conn: c.id})
	}
}

func tlsIndex(v pgwire.TLSVersion) int {
	switch v {
	case pgwire.TLSVersion12:
		return 1
	case pgwire.TLSVersion13:
		return 2
	}
	return 2
}

func (e *Engine) clientReady(c *client, a pgwire.Action) {
	c.phase = cpIdle
	c.tenant = work.TenantID(a.User)
	e.t.cancel(&c.startupT)
	e.snap.ClientsInStartup.Add(-1)
	e.snap.ClientsByTLS[c.tlsIdx]++
	c.counted = true
	c.front = pgwire.NewFrontendTracker(e.cfg.Client.MaxMessage, &e.wire)
	e.t.arm(&c.idleT, e.after(e.cfg.Timeouts.ClientIdle), tClientIdle, c, nil)
	if e.keys != nil {
		e.keys[c.sess.Key()] = c
	}
}

func (e *Engine) setInTx(c *client, on bool) {
	if c.inTx == on {
		return
	}
	c.inTx = on
	if on {
		e.snap.ClientsInTx.Add(1)
	} else {
		e.snap.ClientsInTx.Add(-1)
	}
}

func (e *Engine) pause(c *client) {
	if !c.paused {
		c.paused = true
		e.emit(Action{Kind: ActClientPause, Conn: c.id})
	}
}

func (e *Engine) resume(c *client) {
	if c.paused {
		c.paused = false
		e.emit(Action{Kind: ActClientResume, Conn: c.id})
	}
}

func (e *Engine) busy(c *client) bool {
	return c.req != nil || c.phase == cpQueued || c.phase == cpActive
}

func (e *Engine) pump(c *client) {
	for c.phase == cpIdle || c.phase == cpHeld {
		if e.busy(c) || c.out > 0 || len(c.inbuf) == 0 {
			break
		}
		n, _ := c.cur.next(c.inbuf)
		evs, _ := c.front.Receive(nil, c.inbuf[:n])
		c.inbuf = append(c.inbuf[:0], c.inbuf[n:]...)
		for _, ev := range evs {
			if c.phase == cpClosed {
				return
			}
			e.front(c, ev)
		}
	}
	// Cancel the mid-message deadline as soon as the cursor returns to a
	// message boundary, even if startRequest moved the client to cpQueued or
	// cpActive; leaving it armed would kill a slow fragmented query mid-flight.
	if !c.cur.mid() {
		e.t.cancel(&c.midT)
	}
	if c.phase != cpIdle && c.phase != cpHeld {
		return
	}
	if c.cur.mid() && c.midT == nil {
		e.t.arm(&c.midT, e.after(e.cfg.Timeouts.ClientMidMessage), tClientMid, c, nil)
	}
	if !e.busy(c) && c.out == 0 && len(c.inbuf) == 0 {
		e.resume(c)
		if c.idleT == nil && e.core != nil && c.phase == cpHeld {
			e.t.arm(&c.idleT, e.after(e.cfg.Timeouts.IdleInTx), tClientIdleTx, c, nil)
		} else if c.idleT == nil {
			e.t.arm(&c.idleT, e.after(e.cfg.Timeouts.ClientIdle), tClientIdle, c, nil)
		}
	}
}

func (e *Engine) front(c *client, ev pgwire.FrontEvent) {
	if ev.Kind == pgwire.FrontViolation {
		e.clientViolation(c)
		return
	}
	switch c.sync.Step(ev.Type) {
	case pgwire.SyncDiscardDrop:
		e.syncSnap = addSync(e.syncSnap, 0, 1, 0)
		return
	case pgwire.SyncDiscardReady:
		e.syncSnap = addSync(e.syncSnap, 0, 0, 1)
		out, _ := pgwire.ReplyReady(nil, c.status)
		e.writeClient(c, out)
		return
	case pgwire.SyncDiscardRefuse:
		e.syncSnap = addSync(e.syncSnap, 1, 0, 0)
		e.reject(c, pgwire.ErrRowUnsupportedExtended, false)
		return
	case pgwire.SyncDiscardRefuseAndReady:
		e.syncSnap = addSync(e.syncSnap, 1, 0, 0)
		e.reject(c, pgwire.ErrRowUnsupportedExtended, true)
		return
	}
	switch ev.Kind {
	case pgwire.FrontTerminate:
		e.closeClient(c, CloseClientTerminate)
	case pgwire.FrontQuery:
		e.startRequest(c, ev.Body)
	}
}

func addSync(s pgwire.SyncDiscardSnapshot, refused, discarded, synced int64) pgwire.SyncDiscardSnapshot {
	s.Refused += refused
	s.Discarded += discarded
	s.Synced += synced
	return s
}

func (e *Engine) reject(c *client, row pgwire.ErrRowID, ready bool) {
	e.snap.Received++
	e.snap.Rejected++
	e.snap.StageRefuse[StageReject]++
	e.reply(c, row)
	if ready {
		out, _ := pgwire.ReplyReady(nil, c.status)
		e.writeClient(c, out)
	}
}

func (e *Engine) reply(c *client, row pgwire.ErrRowID) {
	e.snap.ByRow[row]++
	status := c.status
	if !pgwire.ReplyValidStatus(status) {
		status = pgwire.StatusIdle
	}
	out, act, err := e.replies.Build(nil, row, status)
	if err != nil {
		e.closeClient(c, CloseClientViolation)
		return
	}
	if len(out) > 0 {
		e.writeClient(c, out)
	}
	if act.Close {
		reason := CloseClientViolation
		switch row {
		case pgwire.ErrRowDraining:
			reason = CloseClientDraining
		case pgwire.ErrRowLostInTransaction:
			reason = CloseClientLostInTx
		case pgwire.ErrRowPartialReply:
			reason = CloseClientPartialReply
		case pgwire.ErrRowIdleInTransaction:
			reason = CloseClientIdleInTx
		case pgwire.ErrRowLostMidStatementInTransaction:
			reason = CloseClientLostInTx
		}
		e.closeClient(c, reason)
	}
}

func (e *Engine) clientViolation(c *client) {
	// Never splice FATAL 08P01 onto a stream that already relayed part of a reply.
	if c.req == nil || c.req.relayed == 0 {
		e.snap.NonRequestReplies++
		e.reply(c, pgwire.ErrRowProtocolViolation)
	}
	e.closeClient(c, CloseClientViolation)
}

func (e *Engine) clientWrote(c *client, n int) {
	if n <= 0 {
		return
	}
	c.out -= int64(n)
	if c.out < 0 {
		c.out = 0
	}
	if c.out == 0 {
		e.t.cancel(&c.writeT)
	} else {
		e.t.arm(&c.writeT, e.after(e.cfg.Timeouts.ClientWrite), tClientWrite, c, nil)
	}
	if d := c.db; d != nil && d.paused && c.out <= int64(e.cfg.ReplyBufferCap)/2 {
		e.resumeDB(d)
	}
	if c.out == 0 {
		e.pump(c)
	}
}

func (e *Engine) writeClient(c *client, data []byte) {
	if len(data) == 0 || c.phase == cpClosed {
		return
	}
	e.emit(Action{Kind: ActClientWrite, Conn: c.id, Data: data})
	if c.out == 0 {
		e.t.arm(&c.writeT, e.after(e.cfg.Timeouts.ClientWrite), tClientWrite, c, nil)
	}
	c.out += int64(len(data))
	if c.out > e.snap.RelayHighWater {
		e.snap.RelayHighWater = c.out
	}
}

func (e *Engine) clientTimer(t *timer) {
	c := t.client
	switch t.kind {
	case tClientStartup:
		c.startupT = nil
		if c.phase == cpStartup {
			e.startupActs(c, c.sess.Deadline(nil))
		}
	case tClientIdle:
		c.idleT = nil
		if c.phase == cpIdle || c.phase == cpHeld {
			e.closeClient(c, CloseClientIdle)
		}
	case tClientMid:
		c.midT = nil
		if c.cur.mid() {
			e.clientViolation(c)
		}
	case tClientWrite:
		c.writeT = nil
		e.snap.ClientsDropped++
		e.closeClient(c, CloseClientWriteStalled)
	case tQueueWait:
		c.queueT = nil
		e.queueExpired(c)
	case tClientIdleTx:
		c.idleT = nil
		if c.phase == cpHeld {
			e.idleInTx(c)
		}
	}
}

func (e *Engine) clientGone(c *client) {
	if c.phase == cpStartup {
		e.startupActs(c, c.sess.PeerClosed(nil))
		if c.phase != cpClosed {
			e.closeClient(c, CloseClientGone)
		}
		return
	}
	e.closeClient(c, CloseClientGone)
}

func (e *Engine) closeClient(c *client, reason CloseReason) {
	if c.phase == cpClosed {
		return
	}
	for _, slot := range []**timer{&c.startupT, &c.idleT, &c.midT, &c.writeT, &c.queueT} {
		e.t.cancel(slot)
	}
	switch c.phase {
	case cpStartup:
		if !c.sess.Closed() {
			c.sess.PeerClosed(nil)
		}
		e.snap.ClientsInStartup.Add(-1)
	case cpQueued:
		e.dequeueGone(c)
	case cpActive:
		e.activeGone(c)
	}
	if c.db != nil {
		d := c.db
		if d.client == c && e.core != nil {
			e.coreClientGone(c, d, reason)
		}
		c.db = nil
		if d.client == c {
			e.leaseLost(d)
			e.closeDB(d, CloseDBClientVanished)
		}
	}
	if e.keys != nil && c.sess != nil && e.keys[c.sess.Key()] == c {
		delete(e.keys, c.sess.Key())
	}
	e.setInTx(c, false)
	if c.counted {
		e.snap.ClientsByTLS[c.tlsIdx]--
	}
	c.phase = cpClosed
	e.snap.ClientsConnected.Add(-1)
	e.snap.ClientCloses[reason]++
	delete(e.clients, c.id)
	last := len(e.clientList) - 1
	e.clientList[c.idx] = e.clientList[last]
	e.clientList[c.idx].idx = c.idx
	e.clientList[last] = nil
	e.clientList = e.clientList[:last]
	e.emit(Action{Kind: ActClientClose, Conn: c.id, Reason: reason})
	e.dialMore()
	e.dispatch()
}

func (e *Engine) unqueue(id RequestID) {
	if e.shedQ != nil {
		e.shedQ.RemoveAt(e.now, id)
		return
	}
	e.seams.Queue.Remove(id)
}

func (e *Engine) dequeueGone(c *client) {
	if c.req == nil {
		return
	}
	if e.cfg.Plant&PlantQueuedVanishKeepsSlot == 0 {
		e.unqueue(c.req.id)
		delete(e.waiting, c.req.id)
		e.queueTouched()
	}
	e.snap.Queued.Add(-1)
	e.failNoReply(c.req, work.No)
	c.req = nil
}

func (e *Engine) activeGone(c *client) {
	d := c.db
	req := c.req
	if req == nil {
		return
	}
	e.snap.InFlight.Add(-1)
	e.failNoReply(req, reachedFor(d, req))
	c.req = nil
}

func (e *Engine) failNoReply(req *request, reached work.Tri) {
	e.snap.Failed++
	e.snap.FailedNoReply++
	e.reached(reached)
}

func (e *Engine) reached(r work.Tri) {
	if r == work.No {
		e.snap.ReachedNo++
	} else {
		e.snap.ReachedYes++
	}
}
