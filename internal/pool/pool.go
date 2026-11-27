// Package pool implements the deterministic, sans-I/O connection pool core.
//
// It manages backend connection lifecycle by opaque ConnID without Postgres
// types, clocks, or goroutines: a hard cap on open+connecting connections,
// concurrent-connect and creation-rate limits, jittered idle timeouts and
// maximum lifetimes, most-recently-used checkout with off-path staleness and
// suspect checks, a single database-down circuit breaker, dirty-connection
// clean-up, release policies (always and on-switch with optional re-seeding),
// and bounded concurrent cancels.
//
// Every action-returning method follows the engine's deadline law: time passes
// before the event, expiring every deadline at or before now before applying
// the call.
package pool

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Instant is monotonic virtual or wall-clock time in nanoseconds.
type Instant int64

// ConnID uniquely identifies a pooled database connection or cancel connection
// allocated by the pool core.
type ConnID int64

// State is the lifecycle state of a connection tracked by Pool.
type State uint8

const (
	// StateConnecting means a dial or backend startup is in progress.
	StateConnecting State = iota + 1
	// StateIdle means the connection is open and eligible for checkout (subject to checks or switch resets).
	StateIdle
	// StateChecking means an off-path empty-query liveness check is in flight.
	StateChecking
	// StateLent means a client currently holds the connection for a transaction lease.
	StateLent
	// StateCleaning means the connection is in the dirty-connection table (cancel, drain, rollback, or awaiting cancel completion).
	StateCleaning
	// StateResetting means a session reset (and optional re-seed) is in flight.
	StateResetting
	numStates
)

var stateNames = [numStates]string{"?", "connecting", "idle", "checking", "lent", "cleaning", "resetting"}

// String returns the canonical lowercase name of s, or "?" if unknown.
func (s State) String() string {
	if s < numStates {
		return stateNames[s]
	}
	return "?"
}

type step uint8

const (
	stepNone step = iota
	stepCheck
	stepDrain
	stepRollback
	stepReset
	stepAwaitCancel
)

// ActKind identifies one side effect emitted by Pool for the engine to execute.
type ActKind uint8

const (
	// ActDial instructs the engine to open a new backend connection with Action.Conn.
	ActDial ActKind = iota + 1
	// ActCheck instructs the engine to send an empty-query check on Action.Conn.
	ActCheck
	// ActCancel instructs the engine to open cancel connection Action.Cancel targeting Action.Conn.
	ActCancel
	// ActRollback instructs the engine to send ROLLBACK on Action.Conn.
	ActRollback
	// ActReset instructs the engine to send DISCARD ALL (and setseed when Action.Reseed is true) on Action.Conn.
	ActReset
	// ActClose instructs the engine to close Action.Conn immediately with Action.Reason.
	ActClose
	// ActRefuseQueued instructs the engine to flush all queued requests with 53M03 because the breaker is open.
	ActRefuseQueued
	numActKinds
)

var actNames = [numActKinds]string{"?", "dial", "check", "cancel", "rollback", "reset", "close", "refuse-queued"}

// String returns the canonical hyphenated name of k, or "?" if unknown.
func (k ActKind) String() string {
	if k < numActKinds {
		return actNames[k]
	}
	return "?"
}

// Action is one side effect returned by Pool methods for the engine to execute in order.
type Action struct {
	Kind   ActKind
	Conn   ConnID
	Cancel ConnID
	Reason CloseReason
	Reseed bool
	Seed   uint32
}

// CloseReason classifies why Pool closed a connection or failed a dial.
type CloseReason uint8

const (
	// CloseIdle means an idle connection reached its jittered idle timeout.
	CloseIdle CloseReason = iota
	// CloseLifetime means a connection reached its jittered max lifetime while idle or at release.
	CloseLifetime
	// CloseDirty means a released connection had unread bytes or non-idle status.
	CloseDirty
	// CloseCheckFailed means a pre-lend check timed out, errored, or returned unexpected bytes.
	CloseCheckFailed
	// ClosePeerClosedIdle means the peer closed an idle connection without unsolicited bytes.
	ClosePeerClosedIdle
	// CloseIdleError means an idle connection received unsolicited bytes or a protocol error.
	CloseIdleError
	// CloseNoReply means a dirty clean-up after a zero-reply statement deadline failed or timed out.
	CloseNoReply
	// CloseError means a lent connection was lost or hit a wire/server error.
	CloseError
	// CloseResetFailed means a session reset or re-seed failed, timed out, or lost its connection.
	CloseResetFailed
	// CloseCleanupFailed means a dirty-connection clean-up failed, timed out, or had an expired cancel.
	CloseCleanupFailed
	// CloseCancelCap means a dirty connection needed a cancel while MaxCancels were already in flight.
	CloseCancelCap
	// CloseConnectFailed means a dial or backend startup failed or timed out.
	CloseConnectFailed
	// CloseShutdown means Pool.Shutdown closed the connection.
	CloseShutdown
	// NumCloseReasons is the number of defined CloseReason values.
	NumCloseReasons
)

var closeNames = [NumCloseReasons]string{"idle", "lifetime", "dirty", "check_failed", "peer_closed_idle", "idle_error",
	"no_reply", "error", "reset_failed", "cleanup_failed", "cancel_cap", "connect_failed", "shutdown"}

// String returns the canonical snake_case metric label for r, or "?" if unknown.
func (r CloseReason) String() string {
	if r < NumCloseReasons {
		return closeNames[r]
	}
	return "?"
}

// EndKind classifies how a lent connection's lease ended when returned to Release.
type EndKind uint8

const (
	// EndClean means the lease finished with ReadyForQuery 'I' and no unread bytes.
	EndClean EndKind = iota
	// EndUnread means the release policy ordered an immediate close (unread bytes or non-'I' status).
	EndUnread
	// EndVanishedMidStatement means the client disconnected while a statement was in flight.
	EndVanishedMidStatement
	// EndVanishedInTx means the client disconnected between statements inside a transaction.
	EndVanishedInTx
	// EndIdleInTx means the client exceeded timeouts.idle_in_transaction between statements.
	EndIdleInTx
	// EndWriteStalled means the client exceeded timeouts.client_write while receiving a reply.
	EndWriteStalled
	// EndStatementDeadline means the database exceeded timeouts.statement.
	EndStatementDeadline
	// EndLost means the lent connection was lost via Lost rather than Release.
	EndLost
	// NumEndKinds is the number of defined EndKind values.
	NumEndKinds
)

var endNames = [NumEndKinds]string{"clean", "unread", "vanished-mid-statement", "vanished-in-transaction",
	"idle-in-transaction", "write-stalled", "statement-deadline", "lost"}

// String returns the canonical hyphenated metric label for k, or "?" if unknown.
func (k EndKind) String() string {
	if k < NumEndKinds {
		return endNames[k]
	}
	return "?"
}

// End describes the state of a lent connection at lease end.
type End struct {
	Kind      EndKind
	Tenant    work.TenantID
	Mid       bool
	InTx      bool
	ZeroReply bool
}

// LossKind classifies a connection loss reported to Lost.
type LossKind uint8

const (
	// LossClosed means the transport reached EOF or closed cleanly.
	LossClosed LossKind = iota
	// LossBytes means unsolicited bytes arrived on an idle connection.
	LossBytes
	// LossViolation means the database sent malformed or unexpected wire frames.
	LossViolation
	// LossFatal means the database sent a FATAL or PANIC ErrorResponse.
	LossFatal
)

// Fail classifies a non-transport failure of an internal check, rollback, or reset step.
type Fail uint8

const (
	// FailError means the database returned an ErrorResponse.
	FailError Fail = iota
	// FailUnexpected means the reply carried an unexpected frame, tag, status, or trailing bytes.
	FailUnexpected
)

// CheckResult classifies the outcome of a pre-lend empty-query check.
type CheckResult uint8

const (
	// CheckOK means the check received EmptyQueryResponse followed by ReadyForQuery 'I'.
	CheckOK CheckResult = iota
	// CheckTimeout means the check hit pool.check_timeout.
	CheckTimeout
	// CheckError means the check received an ErrorResponse or transport close.
	CheckError
	// CheckUnexpected means the check received any unexpected frame or trailing bytes.
	CheckUnexpected
	// NumCheckResults is the number of defined CheckResult values.
	NumCheckResults
)

var checkNames = [NumCheckResults]string{"ok", "timeout", "error", "unexpected"}

// String returns the canonical lowercase name of r, or "?" if unknown.
func (r CheckResult) String() string {
	if r < NumCheckResults {
		return checkNames[r]
	}
	return "?"
}

// ResetCause classifies why a session reset was started.
type ResetCause uint8

const (
	// ResetAtRelease means a reset started immediately on clean release under ResetAlways.
	ResetAtRelease ResetCause = iota
	// ResetAtSwitch means a reset started on checkout under ResetOnSwitch to switch tenants.
	ResetAtSwitch
	// ResetAfterCleanup means a reset started after dirty-connection drain or rollback.
	ResetAfterCleanup
	// NumResetCauses is the number of defined ResetCause values.
	NumResetCauses
)

var resetCauseNames = [NumResetCauses]string{"always", "on-switch", "cleanup"}

// String returns the canonical metric label for c, or "?" if unknown.
func (c ResetCause) String() string {
	if c < NumResetCauses {
		return resetCauseNames[c]
	}
	return "?"
}

type conn struct {
	id            ConnID
	state         State
	step          step
	deadline      Instant
	created       Instant
	lastUsed      Instant
	lentAt        Instant
	idleLimit     Instant
	lifeLimit     Instant
	tag           work.TenantID
	pendingTag    work.TenantID
	suspect       bool
	suspectLater  bool
	cancelPending bool
	cancelLost    bool
	entry         EndKind
	dirty         bool
	noReply       bool
	resetCause    ResetCause
	trial         bool
	index         int
}

type cancelEntry struct {
	id      ConnID
	conn    ConnID
	expires Instant
}

// Pool is the sans-I/O state machine that manages backend connections, dials,
// checks, clean-ups, resets, cancels, and the database-down circuit breaker.
type Pool struct {
	cfg       Config
	rng       *rand.Rand
	conns     map[ConnID]*conn
	list      []*conn
	cancels   []cancelEntry
	nextID    ConnID
	waiting   int
	now       Instant
	stopped   bool
	rate      limiter
	brk       breaker
	limited   bool
	atCap     bool
	refuse    bool
	snap      Snapshot
	leaseHist *obs.Histogram
	ageHist   *obs.Histogram
	out       []Action
}

// New constructs a validated Pool from cfg after applying defaults.
func New(cfg Config) (*Pool, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &Pool{cfg: cfg, rng: rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)), conns: make(map[ConnID]*conn, cfg.Cap),
		list: make([]*conn, 0, cfg.Cap), cancels: make([]cancelEntry, 0, cfg.MaxCancels), rate: newLimiter(cfg.RatePerSec),
		leaseHist: obs.NewDurationHistogram(), ageHist: obs.NewDurationHistogram()}
	p.brk = breaker{threshold: cfg.BreakerFailures, openFor: Instant(cfg.BreakerOpenFor)}
	p.snap.Cap = int64(cfg.Cap)
	p.snap.MaxConnecting = int64(cfg.MaxConnecting)
	p.snap.RatePerSec = int64(cfg.RatePerSec)
	p.snap.MaxCancels = int64(cfg.MaxCancels)
	return p, nil
}

// Config returns the pool's effective configuration after defaults.
func (p *Pool) Config() Config { return p.cfg }

func (p *Pool) begin(now Instant, out []Action) {
	if now > p.now {
		p.now = now
	}
	p.out = out
	// Time passes before the event: advance the breaker and expire due timers first.
	p.brk.tick(p.now)
	p.expire()
}

func (p *Pool) end() []Action {
	p.expire()
	p.maybeDial()
	p.refuseIfOpen()
	p.gauges()
	out := p.out
	p.out = nil
	return out
}

func (p *Pool) emit(a Action) { p.out = append(p.out, a) }

// Allow reports whether the gate should admit a request at now. It advances
// only the breaker and updates its snapshot fields; while open it returns false
// and records demand so the next half-open period starts a single trial. While
// half-open it returns true so requests queue behind that trial instead of
// failing immediately after a quiet outage.
func (p *Pool) Allow(now Instant) bool {
	if now > p.now {
		p.now = now
	}
	p.brk.tick(p.now)
	if !p.brk.allow() {
		p.brk.wanted = true
	}
	p.snap.Breaker = int64(p.brk.state)
	p.snap.BreakerTransitions = p.brk.trans
	return p.brk.state != BreakerOpen
}

// Breaker returns the current circuit-breaker state without advancing time.
func (p *Pool) Breaker() BreakerState { return p.brk.state }

// Demand records the current queue depth and emits any permitted dials or
// half-open trial needed to meet it.
func (p *Pool) Demand(now Instant, waiting int, out []Action) []Action {
	p.begin(now, out)
	p.waiting = max(waiting, 0)
	return p.end()
}

// Tick advances the pool clock to now, expiring deadlines and emitting any
// resulting closes, rate-limited dials, or half-open trial.
func (p *Pool) Tick(now Instant, out []Action) []Action {
	p.begin(now, out)
	return p.end()
}

func (p *Pool) supply() int {
	n := 0
	for _, c := range p.list {
		switch c.state {
		case StateConnecting, StateIdle, StateChecking, StateResetting:
			n++
		}
	}
	return n
}

func (p *Pool) counts() (open, connecting int) {
	for _, c := range p.list {
		if c.state == StateConnecting {
			connecting++
		} else {
			open++
		}
	}
	return open, connecting
}

func (p *Pool) maybeDial() {
	for !p.stopped {
		p.brk.tick(p.now)
		trial := false
		switch p.brk.state {
		case BreakerOpen:
			p.limited = false
			return
		case BreakerHalfOpen:
			if p.brk.trialSent || (!p.brk.wanted && p.waiting == 0) {
				return
			}
			trial = true
		default:
			if p.waiting <= p.supply() {
				p.limited = false
				p.atCap = false
				return
			}
		}
		open, connecting := p.counts()
		if open+connecting >= p.cfg.Cap && p.cfg.Plant&PlantNoCap == 0 {
			p.limited = false
			if trial {
				p.checkTrial()
			} else if !p.atCap {
				p.atCap = true
				p.snap.Exhausted++
			}
			return
		}
		p.atCap = false
		if connecting >= p.cfg.MaxConnecting && p.cfg.Plant&PlantNoRate == 0 {
			p.limited = false
			return
		}
		if !p.rate.ready(p.now) && p.cfg.Plant&PlantNoRate == 0 {
			if !p.limited {
				p.limited = true
				p.snap.RateLimited++
			}
			return
		}
		p.limited = false
		p.rate.take(p.now)
		p.dial(trial)
	}
}

func (p *Pool) checkTrial() {
	c := p.pick("")
	if c == nil {
		return
	}
	c.trial = true
	p.brk.trialSent = true
	p.brk.trial = c.id
	p.brk.trials++
	p.startCheck(c)
}

func (p *Pool) jitter(d time.Duration) Instant {
	if p.cfg.NoJitter {
		return Instant(d)
	}
	a := jitterAmplitude(d, p.cfg.JitterPermille)
	if a == 0 {
		return Instant(d)
	}
	return Instant(int64(d) + p.rng.Int64N(2*a+1) - a)
}

func (p *Pool) dial(trial bool) {
	p.nextID++
	c := &conn{id: p.nextID, state: StateConnecting, created: p.now, trial: trial, index: len(p.list)}
	c.idleLimit = p.jitter(p.cfg.IdleTimeout)
	c.lifeLimit = p.jitter(p.cfg.MaxLifetime)
	p.conns[c.id] = c
	p.list = append(p.list, c)
	p.snap.Dials++
	if trial {
		p.brk.trialSent = true
		p.brk.trial = c.id
		p.brk.trials++
	}
	p.emit(Action{Kind: ActDial, Conn: c.id})
}

// Opened marks a dialing connection ready for use, resets the breaker's failure
// streak (closing it if id was the half-open trial), and places id in StateIdle.
func (p *Pool) Opened(now Instant, id ConnID, out []Action) []Action {
	p.begin(now, out)
	if c := p.conns[id]; c != nil && c.state == StateConnecting {
		p.snap.DialsOK++
		c.created = p.now
		p.brk.success()
		if c.trial {
			p.brk.trialDone(id, true, p.now)
			c.trial = false
		}
		p.toIdle(c, "")
	}
	return p.end()
}

// DialFailed records that dialing or starting up id failed, counts a breaker
// failure (or re-opens on a failed trial), and frees the connecting slot.
func (p *Pool) DialFailed(now Instant, id ConnID, out []Action) []Action {
	p.begin(now, out)
	if c := p.conns[id]; c != nil && c.state == StateConnecting {
		p.snap.DialFailures++
		if c.trial {
			c.trial = false
			p.trialFailed(id)
		} else {
			p.connFailure()
		}
		p.remove(c, CloseConnectFailed)
	}
	return p.end()
}

func (p *Pool) connFailure() {
	if p.brk.fail(p.now) {
		p.refuse = true
	}
}

func (p *Pool) trialFailed(id ConnID) {
	p.brk.observed++
	p.brk.trialDone(id, false, p.now)
	if p.brk.state == BreakerOpen {
		p.refuse = true
	}
}

func (p *Pool) refuseIfOpen() {
	if !p.refuse {
		return
	}
	p.refuse = false
	if p.brk.state == BreakerOpen {
		p.emit(Action{Kind: ActRefuseQueued})
	}
}

func (p *Pool) markSuspect() {
	if p.cfg.Plant&PlantNoSuspect != 0 {
		return
	}
	for _, c := range p.list {
		switch c.state {
		case StateIdle:
			c.suspect = true
		case StateLent, StateCleaning, StateResetting, StateChecking:
			c.suspectLater = true
		}
	}
}

func (p *Pool) toIdle(c *conn, tag work.TenantID) {
	if c.cancelPending {
		c.state = StateCleaning
		c.step = stepAwaitCancel
		c.pendingTag = tag
		if c.deadline == 0 {
			c.deadline = p.now + Instant(p.cfg.Cleanup)
		}
		return
	}
	c.state = StateIdle
	if c.cancelLost {
		p.remove(c, CloseCleanupFailed)
		return
	}
	if p.now-c.created >= c.lifeLimit {
		p.remove(c, CloseLifetime)
		return
	}
	if c.suspectLater {
		c.suspect = true
		c.suspectLater = false
	}
	if c.dirty {
		p.snap.DirtyReused[c.entry]++
		c.dirty = false
	}
	c.state = StateIdle
	c.step = stepNone
	c.deadline = 0
	c.tag = tag
	c.noReply = false
	c.lastUsed = p.now
}

func (p *Pool) remove(c *conn, reason CloseReason) {
	if p.conns[c.id] != c {
		return
	}
	if c.state == StateLent {
		p.leaseHist.Record(int64(p.now - c.lentAt))
		p.snap.Ends[EndLost]++
	}
	if c.state == StateChecking {
		p.snap.ChecksByResult[CheckError]++
	}
	if c.state == StateResetting {
		p.snap.ResetsFailed[c.resetCause]++
	}
	if c.dirty {
		p.snap.DirtyClosed[c.entry]++
	}
	p.snap.Closes[reason]++
	if p.brk.state == BreakerOpen {
		p.refuse = true
	}
	delete(p.conns, c.id)
	last := len(p.list) - 1
	p.list[c.index] = p.list[last]
	p.list[c.index].index = c.index
	p.list[last] = nil
	p.list = p.list[:last]
	c.state = 0
	if reason != CloseConnectFailed {
		p.emit(Action{Kind: ActClose, Conn: c.id, Reason: reason})
	}
}

func (p *Pool) needsCheck(c *conn) bool {
	return c.suspect || p.now-c.lastUsed >= Instant(p.cfg.CheckAfterIdle)
}

func (p *Pool) class(c *conn, tenant work.TenantID) int {
	switch {
	case p.cfg.Plant&PlantIgnoreTag != 0, c.tag == "":
		return 1
	case c.tag == tenant:
		return 0
	}
	return 2
}

func (p *Pool) pick(tenant work.TenantID) *conn {
	var best *conn
	bestClass := 3
	for _, c := range p.list {
		if c.state != StateIdle {
			continue
		}
		k := p.class(c, tenant)
		if best == nil || k < bestClass || (k == bestClass && (c.lastUsed > best.lastUsed || (c.lastUsed == best.lastUsed && c.id < best.id))) {
			best, bestClass = c, k
		}
	}
	return best
}

// Checkout attempts to lend an idle connection to tenant without waiting.
// Stale or suspect candidates are moved off-path into StateChecking (up to
// maxChecksPerCheckout per call); under ResetOnSwitch a connection tagged for
// another tenant starts a switch reset instead of lending.
func (p *Pool) Checkout(now Instant, tenant work.TenantID, out []Action) (ConnID, bool, []Action) {
	p.begin(now, out)
	p.snap.Checkouts++
	id, ok := p.checkout(tenant)
	if ok {
		p.snap.CheckoutsLent++
	} else {
		p.snap.CheckoutsRefused++
	}
	return id, ok, p.end()
}

func (p *Pool) checkout(tenant work.TenantID) (ConnID, bool) {
	p.brk.tick(p.now)
	if !p.brk.allow() {
		p.brk.wanted = true
		return 0, false
	}
	for checks := 0; ; {
		c := p.pick(tenant)
		if c == nil {
			return 0, false
		}
		if p.needsCheck(c) {
			if checks >= maxChecksPerCheckout {
				return 0, false
			}
			checks++
			p.startCheck(c)
			continue
		}
		if p.class(c, tenant) == 2 {
			p.snap.TenantSwitches++
			c.tag = ""
			p.startReset(c, ResetAtSwitch)
			return 0, false
		}
		if c.tag != "" {
			p.snap.LentAsIs++
		}
		p.lend(c)
		return c.id, true
	}
}

func (p *Pool) lend(c *conn) {
	p.ageHist.Record(int64(p.now - c.lastUsed))
	c.state = StateLent
	c.lentAt = p.now
	c.step = stepNone
	c.deadline = 0
}

func (p *Pool) startCheck(c *conn) {
	c.state = StateChecking
	c.step = stepCheck
	c.deadline = p.now + Instant(p.cfg.CheckTimeout)
	if p.cfg.Plant&PlantNoCheckDeadline != 0 {
		c.deadline = 0
	}
	p.snap.Checks++
	p.emit(Action{Kind: ActCheck, Conn: c.id})
}

func (p *Pool) startReset(c *conn, cause ResetCause) {
	c.state = StateResetting
	c.step = stepReset
	c.resetCause = cause
	c.deadline = p.now + Instant(p.cfg.ResetTimeout)
	p.snap.Resets[cause]++
	a := Action{Kind: ActReset, Conn: c.id}
	if p.cfg.Reseed {
		a.Reseed = true
		a.Seed = uint32(p.rng.Uint64N(1000000))
		p.snap.Reseeds++
	}
	p.emit(a)
}

// Release returns a lent connection according to e: clean leases reset or
// return to idle tagged under ResetOnSwitch, unread releases close at once,
// and dirty releases enter the clean-up sequence (cancel, drain, rollback, reset).
func (p *Pool) Release(now Instant, id ConnID, e End, out []Action) []Action {
	p.begin(now, out)
	c := p.conns[id]
	if c == nil || c.state != StateLent || e.Kind >= NumEndKinds || e.Kind == EndLost {
		return p.end()
	}
	p.leaseHist.Record(int64(p.now - c.lentAt))
	p.snap.Ends[e.Kind]++
	c.state = StateCleaning
	if e.ZeroReply {
		p.snap.NoReply++
		c.noReply = true
		p.markSuspect()
	}
	switch e.Kind {
	case EndClean:
		p.cleanEnd(c, e.Tenant)
	case EndUnread:
		p.remove(c, CloseDirty)
	default:
		c.dirty = true
		c.entry = e.Kind
		c.state = StateCleaning
		c.deadline = p.now + Instant(p.cfg.Cleanup)
		c.pendingTag = ""
		switch {
		case e.Mid:
			p.cancel(c)
			if p.conns[c.id] == c {
				c.step = stepDrain
			}
		case e.InTx:
			p.rollback(c)
		default:
			p.startReset(c, ResetAfterCleanup)
		}
	}
	return p.end()
}

func (p *Pool) cleanEnd(c *conn, tenant work.TenantID) {
	if c.cancelLost {
		p.remove(c, CloseCleanupFailed)
		return
	}
	if p.now-c.created >= c.lifeLimit {
		p.remove(c, CloseLifetime)
		return
	}
	if p.cfg.Reset == ResetOnSwitch {
		p.toIdle(c, tenant)
		return
	}
	p.startReset(c, ResetAtRelease)
}

func (p *Pool) rollback(c *conn) {
	c.state = StateCleaning
	c.step = stepRollback
	p.snap.Rollbacks++
	p.emit(Action{Kind: ActRollback, Conn: c.id})
}

func (p *Pool) cancel(c *conn) bool {
	if c.cancelPending {
		return true
	}
	if len(p.cancels) >= p.cfg.MaxCancels {
		p.snap.CancelsRefused++
		if c.state != StateLent {
			p.remove(c, CloseCancelCap)
		}
		return false
	}
	p.nextID++
	p.cancels = append(p.cancels, cancelEntry{id: p.nextID, conn: c.id, expires: p.now + 2*Instant(p.cfg.Cleanup)})
	c.cancelPending = true
	p.snap.Cancels++
	p.emit(Action{Kind: ActCancel, Conn: c.id, Cancel: p.nextID})
	return true
}

// ClientCancel forwards a client's cancel request for lent connection id if
// MaxCancels permits, reporting whether a cancel action was emitted (or already pending).
func (p *Pool) ClientCancel(now Instant, id ConnID, out []Action) (bool, []Action) {
	p.begin(now, out)
	p.snap.ClientCancels++
	c := p.conns[id]
	ok := false
	if c != nil && c.state == StateLent {
		ok = p.cancel(c)
	} else {
		p.snap.CancelsRefused++
	}
	return ok, p.end()
}

// CancelDone records that cancel connection cancel finished, releasing any
// connection waiting in stepAwaitCancel back to idle.
func (p *Pool) CancelDone(now Instant, cancel ConnID, out []Action) []Action {
	p.begin(now, out)
	for i, e := range p.cancels {
		if e.id != cancel {
			continue
		}
		p.cancels = append(p.cancels[:i], p.cancels[i+1:]...)
		p.snap.CancelsDone++
		p.cancelCleared(e.conn)
		break
	}
	return p.end()
}

func (p *Pool) cancelCleared(id ConnID) {
	c := p.conns[id]
	if c == nil {
		return
	}
	c.cancelPending = false
	if c.state == StateCleaning && c.step == stepAwaitCancel {
		p.toIdle(c, c.pendingTag)
	}
}

// StepDone reports successful completion of an internal check, drain, rollback,
// or reset step on id, with status carrying the trailing ReadyForQuery byte.
func (p *Pool) StepDone(now Instant, id ConnID, status byte, out []Action) []Action {
	p.begin(now, out)
	c := p.conns[id]
	if c == nil {
		return p.end()
	}
	switch {
	case c.state == StateChecking && c.step == stepCheck:
		p.snap.ChecksByResult[CheckOK]++
		c.suspect = false
		p.brk.success()
		if c.trial {
			c.trial = false
			p.brk.trialDone(c.id, true, p.now)
		}
		p.toIdle(c, c.tag)
	case c.state == StateCleaning && c.step == stepDrain:
		if status == 'T' || status == 'E' {
			p.rollback(c)
		} else {
			p.startReset(c, ResetAfterCleanup)
		}
	case c.state == StateCleaning && c.step == stepRollback:
		p.startReset(c, ResetAfterCleanup)
	case c.state == StateResetting && c.step == stepReset:
		p.snap.ResetsOK[c.resetCause]++
		p.brk.success()
		if p.cfg.Plant&PlantResetClearsSuspect != 0 {
			c.suspect, c.suspectLater = false, false
		}
		c.deadline = 0
		p.toIdle(c, "")
	}
	return p.end()
}

// StepFailed reports an ErrorResponse or unexpected reply during a check,
// clean-up, or reset step on id, closing the connection.
func (p *Pool) StepFailed(now Instant, id ConnID, f Fail, out []Action) []Action {
	p.begin(now, out)
	c := p.conns[id]
	if c == nil {
		return p.end()
	}
	switch c.state {
	case StateChecking:
		r := CheckError
		if f == FailUnexpected {
			r = CheckUnexpected
		}
		p.checkFailed(c, r)
	case StateResetting:
		p.remove(c, CloseResetFailed)
	case StateCleaning:
		p.remove(c, p.cleanupReason(c))
	}
	return p.end()
}

func (p *Pool) cleanupReason(c *conn) CloseReason {
	if c.noReply {
		return CloseNoReply
	}
	return CloseCleanupFailed
}

func (p *Pool) checkFailed(c *conn, r CheckResult) {
	p.snap.ChecksByResult[r]++
	trial := c.trial
	// Leave StateChecking before remove so CheckError is not double-counted.
	c.state = StateIdle
	c.trial = false
	p.remove(c, CloseCheckFailed)
	// A non-trial idle check failure proves only an old flow died, not that new
	// dials fail; only a failed half-open trial counts toward the breaker.
	if trial {
		p.trialFailed(c.id)
	}
	p.markSuspect()
}

// Lost records an unexpected close, unsolicited bytes, wire violation, or
// server FATAL on open connection id, closing it and marking siblings suspect.
// A polite close (LossClosed) of an idle connection does not count toward the breaker.
func (p *Pool) Lost(now Instant, id ConnID, k LossKind, out []Action) []Action {
	p.begin(now, out)
	c := p.conns[id]
	if c == nil || c.state == StateConnecting {
		return p.end()
	}
	switch c.state {
	case StateIdle:
		if k == LossClosed {
			p.remove(c, ClosePeerClosedIdle)
			p.markSuspect()
			break
		}
		p.remove(c, CloseIdleError)
		p.connFailure()
		p.markSuspect()
	case StateChecking:
		r := CheckError
		if k != LossClosed && k != LossFatal {
			r = CheckUnexpected
		}
		p.checkFailed(c, r)
	default:
		reason := CloseError
		switch c.state {
		case StateResetting:
			reason = CloseResetFailed
		case StateCleaning:
			reason = p.cleanupReason(c)
		}
		p.remove(c, reason)
		p.connFailure()
		p.markSuspect()
	}
	return p.end()
}

func (p *Pool) expire() {
	if p.stopped {
		return
	}
	for i := 0; i < len(p.list); {
		c := p.list[i]
		if p.expireOne(c) {
			continue
		}
		i++
	}
	for i := 0; i < len(p.cancels); {
		if p.now >= p.cancels[i].expires {
			e := p.cancels[i]
			p.cancels = append(p.cancels[:i], p.cancels[i+1:]...)
			p.snap.CancelsExpired++
			if c := p.conns[e.conn]; c != nil {
				c.cancelPending = false
				c.cancelLost = true
				if c.state == StateCleaning && c.step == stepAwaitCancel {
					p.remove(c, CloseCleanupFailed)
				}
			}
			continue
		}
		i++
	}
}

func (p *Pool) expireOne(c *conn) bool {
	if c.deadline != 0 && p.now >= c.deadline {
		switch c.state {
		case StateChecking:
			p.checkFailed(c, CheckTimeout)
			return true
		case StateResetting:
			p.remove(c, CloseResetFailed)
			return true
		case StateCleaning:
			p.remove(c, p.cleanupReason(c))
			return true
		}
	}
	if c.state != StateIdle {
		return false
	}
	if p.now-c.created >= c.lifeLimit {
		p.remove(c, CloseLifetime)
		return true
	}
	if p.now-c.lastUsed >= c.idleLimit {
		p.remove(c, CloseIdle)
		return true
	}
	return false
}

// Next returns the earliest instant at which Tick must be called to expire a
// deadline, idle timeout, lifetime, cancel wait, breaker lockout, or dial
// rate-limit slot, or 0 if no timer is needed.
func (p *Pool) Next() Instant {
	if p.stopped {
		return 0
	}
	var next Instant
	take := func(t Instant) {
		if t > 0 && (next == 0 || t < next) {
			next = t
		}
	}
	for _, c := range p.list {
		if c.deadline != 0 {
			take(c.deadline)
		}
		if c.state == StateIdle {
			take(c.created + c.lifeLimit)
			take(c.lastUsed + c.idleLimit)
		}
	}
	for _, e := range p.cancels {
		take(e.expires)
	}
	if p.brk.state == BreakerOpen {
		take(p.brk.openUntil)
	}
	if p.limited {
		take(p.rate.next())
	}
	return next
}

// Shutdown stops further dials, closes every open and connecting connection,
// drops pending cancels, and clears all timers so Next returns 0.
func (p *Pool) Shutdown(now Instant, out []Action) []Action {
	p.begin(now, out)
	p.stopped = true
	p.limited = false
	for len(p.list) > 0 {
		c := p.list[len(p.list)-1]
		if c.state == StateConnecting {
			p.snap.DialFailures++
			p.remove(c, CloseConnectFailed)
			p.emit(Action{Kind: ActClose, Conn: c.id, Reason: CloseShutdown})
			continue
		}
		p.remove(c, CloseShutdown)
	}
	p.snap.CancelsExpired += int64(len(p.cancels))
	p.cancels = p.cancels[:0]
	p.gauges()
	o := p.out
	p.out = nil
	return o
}

// State returns the current lifecycle state of id, or 0 if id is not tracked.
func (p *Pool) State(id ConnID) State {
	if c := p.conns[id]; c != nil {
		return c.state
	}
	return 0
}

// Suspect reports whether id is currently suspect or marked suspect on return.
func (p *Pool) Suspect(id ConnID) bool {
	c := p.conns[id]
	return c != nil && (c.suspect || c.suspectLater)
}

// Tag returns the last-tenant tag currently held by id, or empty if untagged.
func (p *Pool) Tag(id ConnID) work.TenantID {
	if c := p.conns[id]; c != nil {
		return c.tag
	}
	return ""
}

// Lendable returns the count of connections currently in StateIdle.
func (p *Pool) Lendable() int {
	n := 0
	for _, c := range p.list {
		if c.state == StateIdle {
			n++
		}
	}
	return n
}

// Usable reports whether at least one open (non-connecting) connection exists.
func (p *Pool) Usable() bool {
	for _, c := range p.list {
		if c.state != StateConnecting {
			return true
		}
	}
	return false
}

func (p *Pool) gauges() {
	var n [numStates]int64
	var suspect int64
	for _, c := range p.list {
		n[c.state]++
		if c.suspect || c.suspectLater {
			suspect++
		}
	}
	open := n[StateIdle] + n[StateChecking] + n[StateLent] + n[StateCleaning] + n[StateResetting]
	p.snap.Open.Set(open)
	p.snap.Connecting.Set(n[StateConnecting])
	p.snap.Idle.Set(n[StateIdle])
	p.snap.Lent.Set(n[StateLent])
	p.snap.Checking.Set(n[StateChecking])
	p.snap.Cleaning.Set(n[StateCleaning])
	p.snap.Resetting.Set(n[StateResetting])
	p.snap.Suspect.Set(suspect)
	p.snap.CancelsInFlight.Set(int64(len(p.cancels)))
	p.snap.OpenPlusConnecting.Set(open + n[StateConnecting])
	p.snap.Breaker = int64(p.brk.state)
	p.snap.BreakerTransitions = p.brk.trans
	p.snap.BreakerTrials = p.brk.trials
	p.snap.BreakerFailures = p.brk.observed
}

// Snapshot returns an immutable copy of all pool metrics and duration histograms.
func (p *Pool) Snapshot() Snapshot {
	s := p.snap
	s.LeaseTime = p.leaseHist.Snapshot()
	s.IdleAgeAtLend = p.ageHist.Snapshot()
	return s
}

// Quiesce verifies that no connection is lent, checking, cleaning, resetting,
// or suspect and that no cancel connection remains in flight.
func (p *Pool) Quiesce() error {
	var errs []error
	for _, c := range p.list {
		switch {
		case c.state != StateIdle && c.state != StateConnecting:
			errs = append(errs, fmt.Errorf("connection %d is %s", c.id, c.state))
		case c.suspect || c.suspectLater:
			errs = append(errs, fmt.Errorf("connection %d is still suspect", c.id))
		}
	}
	if len(p.cancels) > 0 {
		errs = append(errs, fmt.Errorf("%d cancels in flight", len(p.cancels)))
	}
	return errors.Join(errs...)
}

// Invariants verifies hard caps, internal index consistency, and snapshot conservation.
func (p *Pool) Invariants() error {
	open, connecting := p.counts()
	if open+connecting > p.cfg.Cap {
		return fmt.Errorf("pool: %d open + %d connecting above the cap %d", open, connecting, p.cfg.Cap)
	}
	if connecting > p.cfg.MaxConnecting {
		return fmt.Errorf("pool: %d connecting above max_connecting %d", connecting, p.cfg.MaxConnecting)
	}
	if len(p.cancels) > p.cfg.MaxCancels {
		return fmt.Errorf("pool: %d cancels above the cap %d", len(p.cancels), p.cfg.MaxCancels)
	}
	if len(p.list) != len(p.conns) {
		return fmt.Errorf("pool: list %d and map %d disagree", len(p.list), len(p.conns))
	}
	for i, c := range p.list {
		if c.index != i || p.conns[c.id] != c {
			return fmt.Errorf("pool: connection %d misindexed", c.id)
		}
	}
	return p.snap.Check()
}
