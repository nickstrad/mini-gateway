package proxy

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	checkQuery     = ""
	rollbackQuery  = "ROLLBACK"
	reseedTag      = "SELECT 1"
	maxCancelConns = pool.MaxCancelsLimit
	drainHeader    = headerLen + 1
	drainMidFrame  = 0
)

// CorePool adapts *pool.Pool to the engine's Pool seam, buffering pool actions
// emitted during Checkout until finish drains them in corePump.
type CorePool struct {
	p      *pool.Pool
	acts   []pool.Action
	demand func() (int, bool)
}

// NewCorePool constructs a CorePool wrapping a validated pool.Pool.
func NewCorePool(cfg pool.Config) (*CorePool, error) {
	p, err := pool.New(cfg)
	if err != nil {
		return nil, err
	}
	return &CorePool{p: p}, nil
}

// Core returns the underlying sans-I/O pool state machine.
func (cp *CorePool) Core() *pool.Pool { return cp.p }

// Snapshot returns the pool core's current observability snapshot.
func (cp *CorePool) Snapshot() pool.Snapshot { return cp.p.Snapshot() }

// Checkout asks the pool core for a lendable connection for tenant, buffering
// any off-path check or switch-reset actions for corePump.
func (cp *CorePool) Checkout(now Instant, tenant work.TenantID) (ConnID, bool) {
	// Refresh demand before Checkout when the fair limit sits below pool.connections
	// so the pool's own dial check does not open connections past the fair limit.
	if cp.demand != nil {
		if n, ok := cp.demand(); ok {
			cp.acts = cp.p.Demand(pool.Instant(now), n, cp.acts)
		}
	}
	id, ok, acts := cp.p.Checkout(pool.Instant(now), tenant, cp.acts)
	cp.acts = acts
	return ConnID(id), ok
}

// Checkin is a no-op because the engine drives releases directly through pool.Pool.Release.
func (cp *CorePool) Checkin(Instant, ConnID) {}

// Remove is a no-op because the engine reports losses directly through pool.Pool.Lost.
func (cp *CorePool) Remove(ConnID) {}

// Idle returns the number of connections currently lendable in the pool core.
func (cp *CorePool) Idle() int { return cp.p.Lendable() }

func (cp *CorePool) take() []pool.Action {
	a := cp.acts
	cp.acts = nil
	return a
}

// CoreBreaker adapts CorePool's database-down circuit breaker to the Breaker seam.
type CoreBreaker struct{ cp *CorePool }

// Allow reports whether the breaker permits requests to enter the queue at now.
func (b CoreBreaker) Allow(now Instant) bool { return b.cp.p.Allow(pool.Instant(now)) }

// Observe is a no-op because connection-class failures reach the breaker directly
// through the pool core's dial, loss, and trial paths.
func (CoreBreaker) Observe(Instant, work.Outcome) {}

// CorePolicy implements ReleasePolicy for the pool core's always and on-switch modes.
type CorePolicy struct{ Mode pool.ResetMode }

// AtRelease closes connections with unread bytes or non-idle status, lends as-is
// under ResetOnSwitch, and resets under ResetAlways.
func (p CorePolicy) AtRelease(_ Instant, in ReleaseInput) ReleaseAction {
	switch {
	case in.Status != pgwire.StatusIdle || in.Unread:
		return ReleaseClose
	case p.Mode == pool.ResetOnSwitch:
		return ReleaseLendAsIs
	}
	return ReleaseReset
}

func (c Config) poolConfig() pool.Config {
	pc := *c.Pool
	pc.Cap = c.Connections
	pc.ResetTimeout = c.Timeouts.Reset
	pc.Cleanup = c.Timeouts.Cleanup
	pc.CheckTimeout = c.Timeouts.CheckTimeout
	pc.Reset = pool.ResetAlways
	if c.Reset == ResetOnSwitch {
		pc.Reset = pool.ResetOnSwitch
	}
	pc.Reseed = c.Reseed
	return pc.WithDefaults()
}

func (e *Engine) initCore() error {
	if e.cfg.Pool == nil {
		return nil
	}
	pc := e.cfg.poolConfig()
	if e.cfg.Plant&PlantNoReset != 0 {
		pc.Reset = pool.ResetOnSwitch
		pc.Plant |= pool.PlantIgnoreTag
	}
	if cp, ok := e.seams.Pool.(*CorePool); ok {
		e.core = cp
	} else {
		cp, err := NewCorePool(pc)
		if err != nil {
			return err
		}
		e.core = cp
		e.seams.Pool = cp
	}
	e.core.demand = e.checkoutDemand
	if e.seams.Breaker == nil {
		e.seams.Breaker = CoreBreaker{cp: e.core}
	}
	if e.seams.Release == nil {
		e.seams.Release = CorePolicy{Mode: pc.Reset}
	}
	e.keys = make(map[pgwire.BackendKey]*client)
	return nil
}

func (e *Engine) pnow() pool.Instant { return pool.Instant(e.now) }

func (e *Engine) coreRun(acts []pool.Action) {
	for _, a := range acts {
		e.coreExec(a)
	}
}

func (e *Engine) corePump() {
	acts := e.core.take()
	// Report only releasable demand so held-back requests neither dial past the
	// fair limit nor trigger dial/idle-close cycles while waiting.
	acts = e.core.p.Demand(e.pnow(), e.releasable(), acts)
	e.coreRun(acts)
	next := Instant(e.core.p.Next())
	if next == 0 {
		e.t.cancel(&e.poolT)
		return
	}
	if e.poolT == nil || e.poolT.at != next {
		e.t.arm(&e.poolT, next, tPool, nil, nil)
	}
}

func (e *Engine) poolTimer() {
	e.poolT = nil
	e.coreRun(e.core.p.Tick(e.pnow(), nil))
	e.dispatch()
}

func (e *Engine) coreExec(a pool.Action) {
	id := ConnID(a.Conn)
	if a.Kind == pool.ActDial {
		e.coreDial(id)
		return
	}
	if a.Kind == pool.ActRefuseQueued {
		e.refuseQueued()
		return
	}
	d := e.dbs[id]
	if d == nil {
		return
	}
	switch a.Kind {
	case pool.ActCheck:
		e.coreSetPhase(d, dpChecking)
		d.judge = resetJudge{mode: judgeCheck}
		d.judge.fr.Max = MaxResetReply
		e.writeDB(d, pgwire.AppendQuery(nil, []byte(checkQuery)))
	case pool.ActRollback:
		e.coreSetPhase(d, dpCleaning)
		d.judge = resetJudge{mode: judgeRollback}
		d.judge.fr.Max = MaxResetReply
		e.writeDB(d, pgwire.AppendQuery(nil, []byte(rollbackQuery)))
	case pool.ActReset:
		if !d.releasing {
			e.snap.ResetsNotAtRelease++
		}
		e.coreSetPhase(d, dpResetting)
		e.startReset(d)
		if a.Reseed {
			d.judge.reseed = true
			e.snap.Reseeds++
			e.writeDB(d, pgwire.AppendQuery(nil, []byte(reseedSQL(a.Seed))))
		}
	case pool.ActCancel:
		e.sendCancel(d, ConnID(a.Cancel))
	case pool.ActClose:
		reason := e.closeReason(d, a.Reason)
		if d.phase == dpResetting && reason == CloseDBResetFailed && !d.failCounted {
			e.snap.ResetsFailed[ResetFailDeadline]++
		}
		e.closeDB(d, reason)
	}
}

func reseedSQL(seed uint32) string {
	frac := strconv.FormatUint(uint64(seed%1000000)+1000000, 10)[1:]
	return "SELECT setseed(0." + frac + ")"
}

func (e *Engine) closeReason(d *dbConn, r pool.CloseReason) CloseReason {
	switch r {
	case pool.CloseIdle:
		return CloseDBIdle
	case pool.CloseLifetime:
		return CloseDBLifetime
	case pool.CloseDirty:
		return CloseDBReleaseDirty
	case pool.CloseCheckFailed:
		return CloseDBCheckFailed
	case pool.ClosePeerClosedIdle:
		return CloseDBLost
	case pool.CloseIdleError:
		return CloseDBUnsolicited
	case pool.CloseNoReply:
		return CloseDBNoReply
	case pool.CloseResetFailed:
		return CloseDBResetFailed
	case pool.CloseCleanupFailed:
		return CloseDBCleanupFailed
	case pool.CloseCancelCap:
		return CloseDBCancelCap
	case pool.CloseShutdown:
		return CloseDBShutdown
	}
	if d.lostReason != CloseNone {
		return d.lostReason
	}
	return CloseDBLost
}

func (e *Engine) coreDial(id ConnID) {
	d := &dbConn{id: id, slot: -1, phase: dpDialing, tlsFirst: e.cfg.Backend.TLS != pgwire.BackendTLSDisable}
	d.coreIdx = len(e.coreConns)
	e.coreConns = append(e.coreConns, d)
	e.dbs[d.id] = d
	e.dialing++
	e.snap.DBDials++
	e.snap.DBConnecting.Add(1)
	e.emit(Action{Kind: ActDBOpen, Conn: d.id, TLSFirst: d.tlsFirst})
	e.t.arm(&d.connectT, e.after(e.cfg.Timeouts.DBConnect), tDBConnect, nil, d)
}

func (e *Engine) coreForget(d *dbConn) {
	i := d.coreIdx
	if i < 0 || i >= len(e.coreConns) || e.coreConns[i] != d {
		return
	}
	last := len(e.coreConns) - 1
	e.coreConns[i] = e.coreConns[last]
	e.coreConns[i].coreIdx = i
	e.coreConns[last] = nil
	e.coreConns = e.coreConns[:last]
	d.coreIdx = -1
}

func (e *Engine) coreSetPhase(d *dbConn, p dbPhase) {
	if d.phase == p {
		return
	}
	switch d.phase {
	case dpIdle:
		e.snap.DBIdle.Add(-1)
	case dpLent:
		e.snap.DBLent.Add(-1)
	case dpResetting:
		e.snap.Resetting.Add(-1)
	}
	switch p {
	case dpIdle:
		e.snap.DBIdle.Add(1)
	case dpLent:
		e.snap.DBLent.Add(1)
	case dpResetting:
		e.snap.Resetting.Add(1)
	}
	d.phase = p
}

func (e *Engine) coreSync(d *dbConn) {
	if e.dbs[d.id] != d || d.phase == dpClosed {
		return
	}
	switch e.core.p.State(pool.ConnID(d.id)) {
	case pool.StateIdle:
		if d.phase != dpIdle {
			e.coreSetPhase(d, dpIdle)
			d.cur = msgCursor{}
			d.hold = nil
			d.holding = false
			if d.renew {
				d.cycle = pgwire.NewCycleTracker(e.cfg.Backend.MaxMessage, &e.wire)
				d.renew = false
			}
		}
	case pool.StateCleaning:
		if d.phase != dpCleaning || d.judge.mode == judgeDone {
			e.coreSetPhase(d, dpCleaning)
			d.judge = resetJudge{mode: judgeSilent}
		}
	}
}

func (e *Engine) coreSyncAll() {
	for _, d := range e.coreConns {
		e.coreSync(d)
	}
}

func (e *Engine) coreOpened(d *dbConn) {
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
	d.phase = dpStartup
	e.coreRun(e.core.p.Opened(e.pnow(), pool.ConnID(d.id), nil))
	e.coreSync(d)
	e.dispatch()
}

func (e *Engine) coreLost(d *dbConn, reason CloseReason, k pool.LossKind) {
	d.lostReason = reason
	e.coreRun(e.core.p.Lost(e.pnow(), pool.ConnID(d.id), k, nil))
	if e.dbs[d.id] == d && d.phase != dpClosed {
		e.closeDB(d, reason)
	}
}

func lossFor(reason CloseReason) pool.LossKind {
	switch reason {
	case CloseDBViolation:
		return pool.LossViolation
	case CloseDBServerFatal:
		return pool.LossFatal
	case CloseDBUnsolicited:
		return pool.LossBytes
	}
	return pool.LossClosed
}

func (e *Engine) coreStepDone(d *dbConn, status byte) {
	d.judge.mode = judgeDone
	e.coreRun(e.core.p.StepDone(e.pnow(), pool.ConnID(d.id), status, nil))
	e.coreSync(d)
	e.dispatch()
}

func (e *Engine) coreStepFailed(d *dbConn, why ResetFailure) {
	if d.phase == dpResetting {
		e.snap.ResetsFailed[why]++
		d.failCounted = true
	}
	f := pool.FailUnexpected
	if why == ResetFailError {
		f = pool.FailError
	}
	d.lostReason = CloseDBResetFailed
	if d.phase != dpResetting {
		d.lostReason = CloseDBViolation
	}
	e.coreRun(e.core.p.StepFailed(e.pnow(), pool.ConnID(d.id), f, nil))
	if e.dbs[d.id] == d && d.phase != dpClosed {
		e.closeDB(d, d.lostReason)
	}
}

func (e *Engine) coreRelease(d *dbConn, c *client, status byte) {
	e.snap.StagePassed[StageRelease]++
	e.snap.LeasesEnded++
	e.fairEnded(c.tenant)
	d.client = nil
	c.db = nil
	in := ReleaseInput{Conn: d.id, Tenant: c.tenant, LastTenant: d.lastTenant, Status: status, Unread: len(d.hold) > 0 || d.cur.mid() || d.trailing}
	kind := pool.EndClean
	if e.seams.Release.AtRelease(e.now, in) == ReleaseClose {
		kind = pool.EndUnread
	}
	e.resumeDB(d)
	acts := e.core.p.Release(e.pnow(), pool.ConnID(d.id), pool.End{Kind: kind, Tenant: c.tenant}, nil)
	ended := false
	for _, a := range acts {
		if ConnID(a.Conn) != d.id || ended {
			continue
		}
		switch a.Kind {
		case pool.ActReset:
			e.snap.LeasesEndedReset++
			ended = true
		case pool.ActClose:
			e.snap.LeasesEndedClose++
			ended = true
		}
	}
	if !ended {
		e.snap.LeasesEndedAsIs++
	}
	d.releasing = true
	e.coreRun(acts)
	d.releasing = false
	e.coreSync(d)
	e.dispatch()
}

func (e *Engine) coreDirty(d *dbConn, kind pool.EndKind, mid, inTx bool) {
	if d.client != nil && d.client.db == d {
		d.client.db = nil
	}
	tenant := d.lastTenant
	d.client = nil
	e.snap.LeasesEnded++
	e.fairEnded(tenant)
	e.snap.LeasesEndedDirty++
	zero := kind == pool.EndStatementDeadline && d.got == 0
	if zero {
		e.snap.NoReply++
	}
	e.t.cancel(&d.stmtT)
	e.resumeDB(d)
	d.renew = true
	d.judge = resetJudge{mode: judgeDone}
	if mid {
		d.judge = resetJudge{mode: judgeDrain}
		switch {
		case d.holding:
			d.drained = append(d.drained[:0], d.hold[:min(len(d.hold), drainHeader)]...)
		case d.cur.mid():
			// Mark an already-open frame with a non-'Z' byte so its tail cannot
			// look like a ReadyForQuery header during drain.
			d.drained = append(d.drained[:0], drainMidFrame)
		default:
			d.drained = d.drained[:0]
		}
		d.hold = nil
		d.holding = false
	}
	e.coreSetPhase(d, dpCleaning)
	acts := e.core.p.Release(e.pnow(), pool.ConnID(d.id), pool.End{Kind: kind, Tenant: tenant, Mid: mid, InTx: inTx, ZeroReply: zero}, nil)
	e.coreRun(acts)
	e.coreSync(d)
}

func (e *Engine) coreDrain(d *dbConn, b []byte) []byte {
	for len(b) > 0 {
		if d.cur.atStart() {
			d.drained = d.drained[:0]
		}
		n, done := d.cur.next(b)
		if d.cur.bad {
			e.coreLost(d, CloseDBViolation, pool.LossViolation)
			return nil
		}
		if room := drainHeader - len(d.drained); room > 0 {
			d.drained = append(d.drained, b[:min(n, room)]...)
		}
		b = b[n:]
		if !done || len(d.drained) == 0 || d.drained[0] != pgwire.MsgReadyForQuery {
			continue
		}
		if len(d.drained) != drainHeader {
			e.coreLost(d, CloseDBViolation, pool.LossViolation)
			return nil
		}
		status := d.drained[headerLen]
		d.drained = d.drained[:0]
		if len(b) > 0 {
			e.coreStepFailed(d, ResetFailSurprise)
			return nil
		}
		e.coreStepDone(d, status)
		return nil
	}
	return nil
}

func (e *Engine) coreOwn(d *dbConn, b []byte) []byte {
	if d.judge.mode == judgeDrain {
		return e.coreDrain(d, b)
	}
	n, v, why := d.judge.take(b)
	switch v {
	case rvFail:
		e.coreStepFailed(d, why)
		return nil
	case rvDone:
		if n < len(b) {
			e.coreStepFailed(d, ResetFailSurprise)
			return nil
		}
		e.coreStepDone(d, pgwire.StatusIdle)
	}
	return nil
}

func (e *Engine) sendCancel(d *dbConn, cancel ConnID) {
	if d.sess == nil || len(e.cancelConns) >= maxCancelConns {
		e.coreRun(e.core.p.CancelDone(e.pnow(), pool.ConnID(cancel), nil))
		e.coreSyncAll()
		return
	}
	e.cancelConns = append(e.cancelConns, cancel)
	e.snap.CancelConns.Set(int64(len(e.cancelConns)))
	e.snap.CancelsSent++
	e.emit(Action{Kind: ActDBCancel, Conn: cancel, Data: pgwire.AppendCancelRequest(nil, d.sess.Key())})
}

func (e *Engine) cancelDone(id ConnID) {
	i := slices.Index(e.cancelConns, id)
	if e.core == nil || i < 0 {
		return
	}
	e.cancelConns = slices.Delete(e.cancelConns, i, i+1)
	e.snap.CancelConns.Set(int64(len(e.cancelConns)))
	e.snap.CancelsDone++
	e.coreRun(e.core.p.CancelDone(e.pnow(), pool.ConnID(id), nil))
	e.coreSyncAll()
	e.dispatch()
}

func (e *Engine) clientCancel(key pgwire.BackendKey) {
	t := e.keys[key]
	if e.coalCancel(t) {
		return
	}
	switch {
	case t == nil || t.phase == cpClosed:
		e.snap.CancelsUnmatched++
	case t.phase == cpActive && t.db != nil:
		ok, acts := e.core.p.ClientCancel(e.pnow(), pool.ConnID(t.db.id), nil)
		e.coreRun(acts)
		if ok {
			e.snap.CancelsForwarded++
		} else {
			e.snap.CancelsRefused++
		}
	case t.phase == cpQueued && t.req != nil:
		e.unqueue(t.req.id)
		delete(e.waiting, t.req.id)
		e.t.cancel(&t.queueT)
		e.snap.Queued.Add(-1)
		e.snap.Failed++
		e.reached(work.No)
		e.snap.CancelsQueued++
		e.retryEnded(t.req, RetryEndFailed)
		t.req = nil
		t.phase = cpIdle
		e.reply(t, pgwire.ErrRowCancelledQueued)
		e.queueTouched()
		if t.phase != cpClosed {
			e.pump(t)
		}
	default:
		e.snap.CancelsIgnored++
	}
}

func (e *Engine) refuseQueued() {
	if e.shedQ != nil && e.cfg.Plant&PlantBreakerFlushPops == 0 {
		e.refuseAdmitted()
		return
	}
	for e.seams.Queue.Len() > 0 {
		id, ok := e.seams.Queue.Pop(e.now)
		if !ok {
			return
		}
		c := e.waiting[id]
		delete(e.waiting, id)
		if c == nil || c.phase != cpQueued || c.req == nil || c.req.id != id {
			continue
		}
		e.refuseWaiting(c, RetryEndShed)
	}
}

func (e *Engine) refuseAdmitted() {
	// Sweep first so any request already due at e.now leaves as a 53M01 rule shed
	// before remaining queued requests are refused with 53M03.
	e.sweepAdmission()
	ids := make([]RequestID, 0, len(e.waiting))
	for id := range e.waiting {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		c := e.waiting[id]
		if c == nil || c.phase != cpQueued || c.req == nil || c.req.id != id || !e.shedQ.Refuse(e.now, id) {
			continue
		}
		delete(e.waiting, id)
		e.refuseWaiting(c, RetryEndShed)
	}
	e.queueTouched()
}

func (e *Engine) refuseWaiting(c *client, end RetryEnd) {
	e.coalRefused(c, pgwire.ErrRowBreakerOpen)
	e.t.cancel(&c.queueT)
	e.snap.Queued.Add(-1)
	e.snap.Shed++
	e.snap.BreakerFlushed++
	e.retryEnded(c.req, end)
	c.req = nil
	c.phase = cpIdle
	e.reply(c, pgwire.ErrRowBreakerOpen)
	if c.phase != cpClosed {
		e.pump(c)
	}
}

func (e *Engine) statementDeadline(d *dbConn) {
	c := d.client
	if c == nil || c.phase != cpActive || c.req == nil {
		e.leaseFailed(d, CloseDBStatementDeadline)
		return
	}
	inTx := c.req.inTx || c.status != pgwire.StatusIdle
	e.setInTx(c, false)
	e.coreDirty(d, pool.EndStatementDeadline, true, inTx)
	e.statementLost(c, d)
}

func (e *Engine) idleInTx(c *client) {
	d := c.db
	if d == nil || d.client != c {
		e.closeClient(c, CloseClientIdle)
		return
	}
	e.snap.IdleInTxRolledBack++
	e.coreDirty(d, pool.EndIdleInTx, false, true)
	e.setInTx(c, false)
	e.snap.NonRequestReplies++
	e.reply(c, pgwire.ErrRowIdleInTransaction)
	if c.phase != cpClosed {
		e.closeClient(c, CloseClientIdleInTx)
	}
}

func (e *Engine) coreClientGone(c *client, d *dbConn, reason CloseReason) {
	kind := pool.EndVanishedInTx
	mid := c.phase == cpActive
	if mid {
		kind = pool.EndVanishedMidStatement
	}
	if reason == CloseClientWriteStalled {
		kind = pool.EndWriteStalled
	}
	inTx := c.status != pgwire.StatusIdle || c.phase == cpHeld
	if !mid && !inTx {
		kind = pool.EndClean
	}
	if kind == pool.EndClean {
		e.coreRelease(d, c, pgwire.StatusIdle)
		return
	}
	e.coreDirty(d, kind, mid, inTx)
}

func (e *Engine) coreShutdown() {
	e.coreRun(e.core.p.Shutdown(e.pnow(), nil))
	for len(e.coreConns) > 0 {
		e.closeDB(e.coreConns[len(e.coreConns)-1], CloseDBShutdown)
	}
	for _, id := range e.cancelConns {
		e.emit(Action{Kind: ActDBClose, Conn: id, Reason: CloseDBShutdown})
	}
	e.snap.CancelsDone += int64(len(e.cancelConns))
	e.cancelConns = e.cancelConns[:0]
	e.snap.CancelConns.Set(0)
	e.t.cancel(&e.poolT)
}

func (e *Engine) coreUsable() bool {
	for _, d := range e.coreConns {
		if d.phase >= dpIdle && d.phase != dpClosed {
			return true
		}
	}
	return false
}

// PoolSnapshot returns the pool core's current snapshot and true, or the zero
// snapshot and false when the engine uses the fixed connection set.
func (e *Engine) PoolSnapshot() (pool.Snapshot, bool) {
	if e.core == nil {
		return pool.Snapshot{}, false
	}
	return e.core.Snapshot(), true
}

// ClientKey returns the Postgres BackendKey issued to authenticated client id.
func (e *Engine) ClientKey(id ConnID) (pgwire.BackendKey, bool) {
	c := e.clients[id]
	if c == nil || c.sess == nil || c.phase == cpStartup {
		return pgwire.BackendKey{}, false
	}
	return c.sess.Key(), true
}

func corePhase(s pool.State) (dbPhase, dbPhase) {
	switch s {
	case pool.StateConnecting:
		return dpDialing, dpStartup
	case pool.StateIdle:
		return dpIdle, dpIdle
	case pool.StateChecking:
		return dpChecking, dpChecking
	case pool.StateLent:
		return dpLent, dpLent
	case pool.StateCleaning:
		return dpCleaning, dpCleaning
	case pool.StateResetting:
		return dpResetting, dpResetting
	}
	return dpClosed, dpClosed
}

func (e *Engine) coreAgrees() error {
	idle := 0
	for _, d := range e.coreConns {
		if d.phase == dpIdle {
			idle++
		}
		st := e.core.p.State(pool.ConnID(d.id))
		if a, b := corePhase(st); d.phase != a && d.phase != b {
			return fmt.Errorf("database connection %d is in engine phase %d but the pool core has it %s", d.id, d.phase, st)
		}
	}
	if int64(idle) != e.snap.DBIdle.Value {
		return fmt.Errorf("%d core connections idle, gauge says %d", idle, e.snap.DBIdle.Value)
	}
	if s := e.core.p.Snapshot(); s.Idle.Value != int64(idle) || s.Open.Value+s.Connecting.Value != int64(len(e.coreConns)) {
		return fmt.Errorf("the pool core has %d idle and %d open or connecting, the engine %d idle of %d", s.Idle.Value,
			s.Open.Value+s.Connecting.Value, idle, len(e.coreConns))
	}
	return nil
}
