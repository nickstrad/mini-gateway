package proxy

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type FlightTable interface {
	Coalescer
	Admit(now Instant, id RequestID, u *work.Unit) coalesce.Joined
	Started(now Instant, f coalesce.FlightID)
	Stored(now Instant, f coalesce.FlightID, n int) bool
	Finish(now Instant, f coalesce.FlightID, r coalesce.Result)
	Depart(now Instant, id RequestID, why coalesce.LeaveWhy) coalesce.Left
	Followers(f coalesce.FlightID) int
	Sweep(now Instant)
	TakeNotice() (coalesce.Notice, bool)
	Next() Instant
	Shutdown(now Instant)
	Check() error
	Quiesce() error
	Owner(f coalesce.FlightID) (RequestID, bool)
	Member(id RequestID) (coalesce.FlightID, bool)
	Snapshot() coalesce.Snapshot
	Counters() coalesce.Snapshot
}

type CoreCoalescer struct{ t *coalesce.Table }

func NewCoreCoalescer(cfg coalesce.Config) (*CoreCoalescer, error) {
	t, err := coalesce.New(cfg)
	if err != nil {
		return nil, err
	}
	return &CoreCoalescer{t: t}, nil
}

func (c *CoreCoalescer) Table() *coalesce.Table { return c.t }

func (c *CoreCoalescer) Join(now Instant, id RequestID, u *work.Unit) bool {
	return c.Admit(now, id, u).Verdict.Waits()
}

func (c *CoreCoalescer) Leave(now Instant, id RequestID) { c.Depart(now, id, coalesce.LeaveGone) }

func (c *CoreCoalescer) Admit(now Instant, id RequestID, u *work.Unit) coalesce.Joined {
	return c.t.Join(coalesce.Instant(now), coalesce.RequestID(id), u)
}

func (c *CoreCoalescer) Started(now Instant, f coalesce.FlightID) {
	c.t.Started(coalesce.Instant(now), f)
}

func (c *CoreCoalescer) Stored(now Instant, f coalesce.FlightID, n int) bool {
	return c.t.Stored(coalesce.Instant(now), f, n)
}

func (c *CoreCoalescer) Finish(now Instant, f coalesce.FlightID, r coalesce.Result) {
	c.t.Finish(coalesce.Instant(now), f, r)
}

func (c *CoreCoalescer) Depart(now Instant, id RequestID, why coalesce.LeaveWhy) coalesce.Left {
	return c.t.Leave(coalesce.Instant(now), coalesce.RequestID(id), why)
}

func (c *CoreCoalescer) Followers(f coalesce.FlightID) int { return c.t.Followers(f) }

func (c *CoreCoalescer) Sweep(now Instant) { c.t.Sweep(coalesce.Instant(now)) }

func (c *CoreCoalescer) TakeNotice() (coalesce.Notice, bool) { return c.t.TakeNotice() }

func (c *CoreCoalescer) Next() Instant { return Instant(c.t.Next()) }

func (c *CoreCoalescer) Shutdown(now Instant) { c.t.Shutdown(coalesce.Instant(now)) }

func (c *CoreCoalescer) Check() error { return c.t.Check() }

func (c *CoreCoalescer) Quiesce() error { return c.t.Quiesce() }

func (c *CoreCoalescer) Owner(f coalesce.FlightID) (RequestID, bool) {
	id, ok := c.t.Owner(f)
	return RequestID(id), ok
}

func (c *CoreCoalescer) Member(id RequestID) (coalesce.FlightID, bool) {
	return c.t.Member(coalesce.RequestID(id))
}

func (c *CoreCoalescer) Snapshot() coalesce.Snapshot { return c.t.Snapshot() }

func (c *CoreCoalescer) Counters() coalesce.Snapshot { return c.t.Counters() }

type flightEnd struct {
	reply   []byte
	row     pgwire.ErrRowID
	reached work.Tri
}

func (c Config) coalesceDefaults() coalesce.Config {
	cc := c.Coalesce
	if cc.Deadline == 0 {
		cc.Deadline = coalesce.Instant(c.Timeouts.Statement)
	}
	if cc.MaxReplyBytes == 0 {
		cc.MaxReplyBytes = min(coalesce.DefaultMaxReplyBytes, int64(c.ReplyBufferCap)/2)
	}
	return cc.WithDefaults()
}

func (c Config) coalesceErrors() []error {
	if c.Coalesce.Mode == coalesce.ModeOff && c.Seams.Coalescer == nil {
		return nil
	}
	var errs []error
	cc := c.coalesceDefaults()
	if err := cc.Validate(); err != nil {
		errs = append(errs, err)
	}
	if cc.MaxReplyBytes > int64(c.ReplyBufferCap)/2 {
		errs = append(errs, fmt.Errorf("%w: coalesce.max_reply_bytes: %d is more than half of limits.reply_buffer (%d)", ErrConfig,
			cc.MaxReplyBytes, c.ReplyBufferCap))
	}
	return errs
}

func (e *Engine) initCoalesce() error {
	if e.seams.Coalescer == nil && e.cfg.Coalesce.Mode != coalesce.ModeOff {
		cc, err := NewCoreCoalescer(e.cfg.Coalesce)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrConfig, err)
		}
		e.seams.Coalescer = cc
	}
	if ft, ok := e.seams.Coalescer.(FlightTable); ok {
		e.coal = ft
		e.followers = make(map[RequestID]*client)
		e.members = make(map[RequestID]*client)
		e.ends = make(map[coalesce.FlightID]flightEnd)
	}
	return nil
}

func (e *Engine) CoalesceSnapshot() coalesce.Snapshot {
	if e.coal == nil {
		return coalesce.Snapshot{}
	}
	return e.coal.Snapshot()
}

func (e *Engine) coalJoin(c *client, req *request) {
	j := e.coal.Admit(e.now, req.id, &req.unit)
	if j.Verdict == coalesce.VerdictRefuse {
		e.snap.Rejected++
		e.snap.StageRefuse[StageCoalesce]++
		e.snap.CoalesceRefused++
		e.reply(c, pgwire.ErrRowCoalesceCap)
		e.coalNotices()
		return
	}
	e.snap.Accepted++
	c.req = req
	e.snap.StagePassed[StageCoalesce]++
	switch j.Verdict {
	case coalesce.VerdictLead:
		e.snap.CoalesceLed++
		e.coalMember(c, req.id, j.Flight)
		req.fl, req.store = j.Flight, true
		e.enqueue(c)
	case coalesce.VerdictFollow, coalesce.VerdictHold:
		if j.Verdict == coalesce.VerdictHold {
			e.snap.CoalesceLed++
		} else {
			e.snap.CoalesceFollowed++
		}
		e.coalMember(c, req.id, j.Flight)
		c.phase = cpCoalesced
		e.followers[req.id] = c
		e.snap.Coalesced.Add(1)
	default:
		e.snap.CoalescePassed++
		e.enqueue(c)
	}
	e.coalNotices()
}

func (e *Engine) coalMember(c *client, wid RequestID, f coalesce.FlightID) {
	c.fl, c.wid = f, wid
	e.members[wid] = c
}

func (e *Engine) coalUnfollow(c *client) {
	if e.followers[c.wid] == c {
		delete(e.followers, c.wid)
		e.snap.Coalesced.Add(-1)
	}
	if e.members[c.wid] == c {
		delete(e.members, c.wid)
	}
	c.fl, c.wid = 0, 0
}

func (e *Engine) coalNotices() {
	if e.coal == nil || e.noticeBusy {
		return
	}
	e.noticeBusy = true
	for {
		n, ok := e.coal.TakeNotice()
		if !ok {
			break
		}
		e.coalNotice(n)
	}
	e.noticeBusy = false
	for _, f := range e.endOrder {
		delete(e.ends, f)
	}
	e.endOrder = e.endOrder[:0]
	e.coalArm()
}

func (e *Engine) coalArm() {
	next := e.coal.Next()
	if next == 0 {
		e.t.cancel(&e.coalT)
		return
	}
	if e.coalT == nil || e.coalT.at != next {
		e.t.arm(&e.coalT, next, tCoalesce, nil, nil)
	}
}

func (e *Engine) coalTimer() {
	e.coalT = nil
	e.coal.Sweep(e.now)
	e.coalNotices()
}

func (e *Engine) coalEnd(f coalesce.FlightID, end flightEnd) {
	if _, ok := e.ends[f]; !ok {
		e.endOrder = append(e.endOrder, f)
	}
	e.ends[f] = end
}

func (e *Engine) coalNotice(n coalesce.Notice) {
	c := e.members[RequestID(n.Waiter)]
	if c == nil || c.phase == cpClosed {
		return
	}
	switch n.Kind {
	case coalesce.NoticeStart:
		e.coalStart(c, n.Flight)
	case coalesce.NoticeExpireOwner:
		e.coalExpireOwner(c, n.Flight)
	default:
		e.coalAnswer(c, n)
	}
}

func (e *Engine) expireQueued(id RequestID) {
	if e.shedQ != nil {
		e.shedQ.Expire(e.now, id)
		return
	}
	e.seams.Queue.Remove(id)
}

func (e *Engine) coalStart(c *client, f coalesce.FlightID) {
	if c.phase != cpCoalesced || c.req == nil {
		return
	}
	delete(e.followers, c.wid)
	e.snap.Coalesced.Add(-1)
	e.snap.CoalescePromoted++
	c.phase = cpIdle
	c.req.fl, c.req.store = f, true
	c.req.enq = e.now
	e.enqueue(c)
	if c.phase == cpIdle {
		e.pump(c)
	}
}

func (e *Engine) coalExpireOwner(c *client, f coalesce.FlightID) {
	e.snap.CoalesceExpired++
	switch c.phase {
	case cpCoalesced:
		e.coalEnd(f, flightEnd{row: pgwire.ErrRowShed, reached: work.No})
		e.coalAnswer(c, coalesce.Notice{Kind: coalesce.NoticeExpired, Flight: f})
	case cpQueued:
		e.coalEnd(f, flightEnd{row: pgwire.ErrRowShed, reached: work.No})
		e.coalUnfollow(c)
		req := c.req
		req.fl, req.store, req.stored = 0, false, nil
		e.expireQueued(req.id)
		delete(e.waiting, req.id)
		e.t.cancel(&c.queueT)
		e.snap.Queued.Add(-1)
		e.snap.StageRefuse[StageQueue]++
		e.shed(c)
		e.queueTouched()
		if c.phase != cpClosed {
			e.pump(c)
		}
	case cpActive:
		d := c.db
		if d == nil {
			return
		}
		if e.core != nil {
			e.statementDeadline(d)
		} else {
			e.leaseFailed(d, CloseDBStatementDeadline)
		}
	}
}

func (e *Engine) coalAnswer(c *client, n coalesce.Notice) {
	if c.phase != cpCoalesced || c.req == nil {
		return
	}
	end, ok := e.ends[n.Flight]
	if !ok {
		end = flightEnd{row: pgwire.ErrRowShed, reached: work.No}
	}
	e.coalUnfollow(c)
	c.req = nil
	c.phase = cpIdle
	switch n.Kind {
	case coalesce.NoticeReply:
		e.snap.Completed++
		e.snap.CoalesceReplies++
		e.writeClient(c, end.reply)
		ready, _ := pgwire.ReplyReady(nil, pgwire.StatusIdle)
		e.writeClient(c, ready)
	case coalesce.NoticeRefused:
		e.snap.Failed++
		e.reached(work.No)
		e.snap.CoalesceOverflowed++
		e.reply(c, pgwire.ErrRowCoalesceCap)
	default:
		e.snap.Failed++
		e.reached(end.reached)
		e.snap.CoalesceErrors++
		e.reply(c, end.row)
	}
	if c.phase != cpClosed {
		e.pump(c)
	}
}

func (e *Engine) coalSent(req *request) {
	if e.coal == nil || req.fl == 0 {
		return
	}
	e.coal.Started(e.now, req.fl)
	e.coalNotices()
}

func (e *Engine) coalSawError(c *client) {
	if c != nil && c.req != nil && c.req.store {
		c.req.sawErr = true
	}
}

func (e *Engine) coalStoring(c *client, seg []byte) bool {
	req := c.req
	if e.coal == nil || req == nil || !req.store {
		return false
	}
	req.stored = append(req.stored, seg...)
	if e.coal.Stored(e.now, req.fl, len(seg)) {
		e.coalNotices()
		return true
	}
	req.store = false
	out := req.stored
	req.stored = nil
	e.coalNotices()
	e.relayed(c, out)
	return true
}

func (e *Engine) coalReady(d *dbConn, c *client, held []byte, status byte) bool {
	req := c.req
	if e.coal == nil || req == nil || req.fl == 0 {
		return false
	}
	f := req.fl
	if !req.store {
		req.fl = 0
		e.coalUnfollow(c)
		e.coal.Finish(e.now, f, coalesce.Result{ErrorReply: req.sawErr})
		e.coalNotices()
		return false
	}
	stored := req.stored
	req.fl, req.store, req.stored = 0, false, nil
	e.coalEnd(f, flightEnd{reply: stored})
	e.coalUnfollow(c)
	e.coal.Finish(e.now, f, coalesce.Result{ErrorReply: req.sawErr, Unshareable: status != pgwire.StatusIdle})
	req.relayed += int64(len(stored))
	e.writeClient(c, stored)
	e.relayed(c, held)
	e.replyDone(d, c, status)
	e.coalNotices()
	return true
}

func (e *Engine) coalFailed(c *client, req *request, row pgwire.ErrRowID, reached work.Tri) {
	if e.coal == nil || req == nil || req.fl == 0 {
		return
	}
	f := req.fl
	req.fl, req.store, req.stored = 0, false, nil
	e.coalEnd(f, flightEnd{row: row, reached: reached})
	if c != nil && c.fl == f {
		e.coalUnfollow(c)
	}
	e.coal.Finish(e.now, f, coalesce.Result{Failed: true})
	e.coalNotices()
}

func (e *Engine) coalRefused(c *client, row pgwire.ErrRowID) {
	if c.req != nil {
		e.coalFailed(c, c.req, row, work.No)
	}
}

func (e *Engine) coalClosing(c *client) {
	if e.coal == nil || c.fl == 0 {
		return
	}
	owner := c.phase == cpQueued || c.phase == cpActive
	reached := work.No
	if c.phase == cpActive {
		reached = work.Yes
	}
	if !e.coalLeave(c, coalesce.LeaveGone) && owner {
		return
	}
	e.snap.Failed++
	e.snap.FailedNoReply++
	e.reached(reached)
	c.req = nil
	c.phase = cpIdle
}

func (e *Engine) coalLeave(c *client, why coalesce.LeaveWhy) bool {
	wasFollower := c.phase == cpCoalesced
	l := e.coal.Depart(e.now, c.wid, why)
	e.coalUnfollow(c)
	if wasFollower {
		e.coalNotices()
		return true
	}
	req := c.req
	if l.Kind == coalesce.LeftHandoff {
		e.coalHandoff(c, e.members[RequestID(l.NewOwner)])
		e.coalNotices()
		return true
	}
	if req != nil {
		req.fl, req.store = 0, false
		out := req.stored
		req.stored = nil
		if why == coalesce.LeaveCancel && len(out) > 0 {
			e.relayed(c, out)
		}
	}
	e.coalNotices()
	return false
}

func (e *Engine) coalHandoff(old, nc *client) {
	req := old.req
	if nc == nil || req == nil {
		return
	}
	delete(e.followers, nc.wid)
	e.snap.Coalesced.Add(-1)
	e.snap.CoalesceHandoffs++
	nc.req = req
	old.req = nil
	switch old.phase {
	case cpQueued:
		nc.phase = cpQueued
		e.waiting[req.id] = nc
		if old.queueT != nil {
			e.t.arm(&nc.queueT, old.queueT.at, tQueueWait, nc, nil)
			e.t.cancel(&old.queueT)
		}
	case cpActive:
		d := old.db
		nc.phase = cpActive
		d.client = nc
		nc.db = d
		old.db = nil
	}
	old.phase = cpIdle
}

func (e *Engine) coalCancel(c *client) bool {
	if e.coal == nil || c == nil || c.phase == cpClosed || c.fl == 0 {
		return false
	}
	if !e.coalLeave(c, coalesce.LeaveCancel) {
		return false
	}
	e.snap.Failed++
	e.reached(work.No)
	e.snap.CancelsCoalesced++
	c.req = nil
	c.phase = cpIdle
	e.reply(c, pgwire.ErrRowCancelledQueued)
	if c.phase != cpClosed {
		e.pump(c)
	}
	return true
}

func (e *Engine) coalShutdown() {
	if e.coal == nil {
		return
	}
	e.coal.Shutdown(e.now)
	e.coalNotices()
	e.t.cancel(&e.coalT)
}

func (e *Engine) coalQuiet() error {
	if e.coal == nil {
		return nil
	}
	var errs []error
	if err := e.coal.Quiesce(); err != nil {
		errs = append(errs, err)
	}
	if len(e.followers) != 0 || len(e.members) != 0 || e.snap.Coalesced.Value != 0 {
		errs = append(errs, fmt.Errorf("%d followers, %d waiters, gauge %d", len(e.followers), len(e.members), e.snap.Coalesced.Value))
	}
	if e.coalT != nil {
		errs = append(errs, fmt.Errorf("coalescer timer armed at %d", e.coalT.at))
	}
	return errors.Join(errs...)
}

func (e *Engine) coalCheck() error {
	if e.coal == nil {
		return nil
	}
	if err := e.coal.Check(); err != nil {
		return err
	}
	followers, members := 0, 0
	for _, c := range e.clientList {
		if c.phase == cpCoalesced {
			followers++
			if e.followers[c.wid] != c || c.req == nil {
				return fmt.Errorf("client %d waits on a flight without its follower entry", c.id)
			}
		}
		if c.fl == 0 {
			continue
		}
		members++
		if e.members[c.wid] != c {
			return fmt.Errorf("client %d is on flight %d without its waiter entry", c.id, c.fl)
		}
		if f, ok := e.coal.Member(c.wid); !ok || f != c.fl {
			return fmt.Errorf("client %d is on flight %d, the coalescer has it on %d", c.id, c.fl, f)
		}
		owner, _ := e.coal.Owner(c.fl)
		switch {
		case c.phase == cpCoalesced:
		case owner != c.wid:
			return fmt.Errorf("client %d is %d on flight %d but does not own it", c.id, c.phase, c.fl)
		case c.req == nil || c.req.fl != c.fl:
			return fmt.Errorf("client %d owns flight %d without carrying its request", c.id, c.fl)
		}
	}
	if followers != len(e.followers) || int64(followers) != e.snap.Coalesced.Value {
		return fmt.Errorf("%d clients wait on flights, %d follower entries, gauge %d", followers, len(e.followers), e.snap.Coalesced.Value)
	}
	if members != len(e.members) || int64(members) != e.coal.Counters().Waiting.Value {
		return fmt.Errorf("%d clients on flights, %d waiter entries, the coalescer counts %d", members, len(e.members),
			e.coal.Counters().Waiting.Value)
	}
	return nil
}

func coalesceTimeoutRows(t Timeouts) []TimeoutRow {
	c := coalesce.Config{}.WithDefaults()
	return []TimeoutRow{
		{"Coalesced flight, from the request that starts it (independent of every waiter)", "coalescer, flight table (swept at the deadline)", "timeouts.statement", t.Statement, "Every waiter gets one shared error: ERROR 53M01 if the flight never reached the database, else the statement's 08006 or 08007 (the connection goes to the dirty-connection table); a reply at the deadline is late"},
		{"No-share list: a key whose reply passed coalesce.max_reply_bytes is not shared again for this long", "coalescer, no-share list (swept at the expiry)", "coalesce.no_share_for", time.Duration(c.NoShareFor), "The key is shared again"},
	}
}

func coalesceCapacityRows(c Config) []CapacityRow {
	cc := c.coalesceDefaults()
	return []CapacityRow{
		{"Coalescer flights (keys with a flight; each key has at most a running flight and one waiting behind it)", "proxy memory", configured(itoa(cc.MaxFlights)), "coalesce.max_flights", "The request passes on unshared (overflow flights)", "timeouts.statement", "coalescer.table_size"},
		{"Waiters per flight, the request that started it included", "proxy memory; one flight's fan-out", configured(itoa(cc.MaxWaiters)), "coalesce.max_waiters", "ERROR 53M04 (coalesce-cap) at once; nothing reached the database for that request", "timeouts.statement", "coalescer.waiting"},
		{"Shared-reply bytes of one flight (stored once, written to every waiter)", "proxy memory under a large shared reply", configured(itoa(int(cc.MaxReplyBytes)) + " bytes, at most half of limits.reply_buffer"), "coalesce.max_reply_bytes", "The flight stops sharing: its waiters get ERROR 53M04, the request that started it gets the reply streamed, and the key goes on the no-share list", "timeouts.statement", "coalescer.stored_bytes"},
		{"No-share list", "proxy memory", configured(itoa(cc.NoShareMax)), "coalesce.no_share_max", "The oldest listed key is dropped (no_share_ends evicted)", "coalesce.no_share_for", "coalescer.no_share"},
		{"Per-key hotness slots (opt-in diagnostics)", "proxy memory and hot-path cost", "0 (off); at most " + itoa(coalesce.MaxHotKeys), "coalesce.hot_keys", "The least offered tracked key is replaced", "-", "coalescer.hot_key_hits"},
		{"Per-tenant coalescer counters kept for export", "proxy memory", itoa(coalesce.MaxTenantStats), "(constant)", "Further tenants are counted under the tenant label other", "-", "coalescer.tenant_hits"},
	}
}
