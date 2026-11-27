package pool

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	opBytes   = 3
	maxOps    = 64
	modelSeed = 7
)

type opKind uint8

const (
	opDemand opKind = iota
	opOpened
	opDialFailed
	opCheckout
	opReleaseClean
	opReleaseDirty
	opStepDone
	opStepFailed
	opLost
	opCancelDone
	opClientCancel
	opAdvance
	opDatabaseLost
	opSwitchTenant
	numOpKinds
)

var opKindNames = [numOpKinds]string{"demand", "opened", "dial-failed", "checkout", "release-clean", "release-dirty",
	"step-done", "step-failed", "lost", "cancel-done", "client-cancel", "advance", "database-lost", "switch-tenant"}

func (k opKind) String() string { return opKindNames[k%numOpKinds] }

type op struct {
	kind opKind
	a, b byte
}

func (o op) String() string { return fmt.Sprintf("%s(%d,%d)", o.kind, o.a, o.b) }

func decodeOps(b []byte) []op {
	n := min(len(b)/opBytes, maxOps)
	out := make([]op, 0, n)
	for i := 0; i < n; i++ {
		c := b[i*opBytes:]
		out = append(out, op{kind: opKind(c[0] % byte(numOpKinds)), a: c[1], b: c[2]})
	}
	return out
}

func encodeOps(ops []op) []byte {
	out := make([]byte, 0, len(ops)*opBytes)
	for _, o := range ops {
		out = append(out, byte(o.kind), o.a, o.b)
	}
	return out
}

var advanceSteps = [...]time.Duration{0, time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
	250 * time.Millisecond, 400 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second, 31 * time.Second,
	181 * time.Second, 31 * time.Minute}

func modelConfig() Config {
	return Config{Cap: 3, MaxConnecting: 2, RatePerSec: 20, IdleTimeout: 180 * time.Second, MaxLifetime: 30 * time.Minute,
		CheckAfterIdle: 30 * time.Second, CheckTimeout: 250 * time.Millisecond, Cleanup: time.Second, ResetTimeout: time.Second,
		BreakerFailures: 3, BreakerOpenFor: time.Second, MaxCancels: 2, Seed: modelSeed}
}

type mconn struct {
	id         ConnID
	open       bool
	suspect    bool
	clean      bool
	tag        work.TenantID
	lastUsed   Instant
	checkStart Instant
	lentTo     work.TenantID
	pending    step
}

type model struct {
	cfg       Config
	p         *Pool
	now       Instant
	conns     map[ConnID]*mconn
	order     []ConnID
	cancels   map[ConnID]ConnID
	tainted   map[ConnID]bool
	lastDial  Instant
	dialed    bool
	waiting   int
	tenant    int
	trace     []string
	violation string
	stopped   bool
	losing    ConnID
	record    bool
}

func newModel(cfg Config) (*model, error) {
	p, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &model{cfg: p.Config(), p: p, conns: make(map[ConnID]*mconn), cancels: make(map[ConnID]ConnID),
		tainted: make(map[ConnID]bool)}, nil
}

func (m *model) fail(format string, args ...any) {
	if m.violation == "" {
		m.violation = fmt.Sprintf(format, args...)
	}
}

func (m *model) tenantName() work.TenantID { return work.TenantID("t" + string(rune('0'+m.tenant%3))) }

func (m *model) live() []ConnID {
	out := make([]ConnID, 0, len(m.order))
	for _, id := range m.order {
		if c := m.conns[id]; c != nil && c.open {
			out = append(out, id)
		}
	}
	return out
}

func (m *model) inState(s State) []ConnID {
	var out []ConnID
	for _, id := range m.live() {
		if m.p.State(id) == s {
			out = append(out, id)
		}
	}
	return out
}

func pickID(ids []ConnID, a byte) (ConnID, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	return ids[int(a)%len(ids)], true
}

func (m *model) suspectAll(except ConnID) {
	for _, id := range m.live() {
		if id != except && m.p.State(id) != StateConnecting {
			m.conns[id].suspect = true
		}
	}
}

func (m *model) absorb(acts []Action) {
	for _, a := range acts {
		if m.record {
			m.trace = append(m.trace, fmt.Sprintf("%d %s %d %d %s %v %d", m.now, a.Kind, a.Conn, a.Cancel, a.Reason, a.Reseed, a.Seed))
		}
		switch a.Kind {
		case ActDial:
			if m.p.Breaker() == BreakerOpen {
				m.fail("dial %d while the breaker is open", a.Conn)
			}
			if m.dialed && m.now-m.lastDial < m.p.rate.interval {
				m.fail("creation rate exceeded: dial %d at %d, previous at %d", a.Conn, m.now, m.lastDial)
			}
			m.dialed, m.lastDial = true, m.now
			m.conns[a.Conn] = &mconn{id: a.Conn, open: true}
			m.order = append(m.order, a.Conn)
		case ActCheck:
			if c := m.conns[a.Conn]; c != nil {
				c.checkStart = m.now
				c.pending = stepCheck
			}
		case ActReset:
			if c := m.conns[a.Conn]; c != nil {
				c.pending = stepReset
				c.clean = false
				if a.Reseed != m.cfg.Reseed {
					m.fail("reset of %d with reseed %v, config %v", a.Conn, a.Reseed, m.cfg.Reseed)
				}
			}
		case ActRollback:
			if c := m.conns[a.Conn]; c != nil {
				c.pending = stepRollback
			}
		case ActCancel:
			m.cancels[a.Cancel] = a.Conn
			if len(m.cancels) > m.cfg.MaxCancels {
				m.fail("%d cancels in flight above the cap %d", len(m.cancels), m.cfg.MaxCancels)
			}
		case ActClose:
			c := m.conns[a.Conn]
			if c == nil || (!c.open && a.Conn != m.losing) {
				m.fail("close of unknown connection %d", a.Conn)
				continue
			}
			c.open = false
			switch a.Reason {
			case CloseCheckFailed, CloseIdleError, CloseError, CloseNoReply, ClosePeerClosedIdle:
				m.suspectAll(a.Conn)
			}
		}
	}
	m.checkAccounting()
}

func (m *model) checkAccounting() {
	open, connecting := 0, 0
	for _, id := range m.live() {
		if m.p.State(id) == StateConnecting {
			connecting++
		} else {
			open++
		}
	}
	if open+connecting > m.cfg.Cap {
		m.fail("cap: %d open + %d connecting above %d", open, connecting, m.cfg.Cap)
	}
	if connecting > m.cfg.MaxConnecting {
		m.fail("%d connecting above %d", connecting, m.cfg.MaxConnecting)
	}
	for _, id := range m.live() {
		c := m.conns[id]
		if m.p.State(id) == 0 {
			m.fail("connection %d open in the model, unknown to the pool", id)
		}
		if m.p.State(id) == StateChecking && c.pending == stepCheck && m.now > c.checkStart+Instant(m.cfg.CheckTimeout) {
			m.fail("connection %d still checking %d after its check started (deadline %s)", id, m.now-c.checkStart, m.cfg.CheckTimeout)
		}
	}
	if err := m.p.Invariants(); err != nil {
		m.fail("%v", err)
	}
	if m.p.Breaker() != BreakerClosed && m.p.Allow(m.now) {
		m.fail("breaker %s allows work", m.p.Breaker())
	}
}

func (m *model) checkSuspect() {
	for _, id := range m.live() {
		if m.conns[id].suspect && m.p.State(id) != StateConnecting && !m.p.Suspect(id) {
			m.fail("connection %d lost its suspect flag without a check (lent suspect next)", id)
		}
	}
}

func (m *model) lent(id ConnID, tenant work.TenantID) {
	c := m.conns[id]
	if c == nil || !c.open {
		m.fail("lent unknown connection %d", id)
		return
	}
	switch {
	case c.suspect:
		m.fail("lent suspect connection %d to %s", id, tenant)
	case m.now-c.lastUsed >= Instant(m.cfg.CheckAfterIdle):
		m.fail("lent connection %d idle for %d without a check", id, m.now-c.lastUsed)
	case !c.clean && !(m.cfg.Reset == ResetOnSwitch && c.tag == tenant):
		m.fail("lent connection %d to %s without a completed reset (tag %q)", id, tenant, c.tag)
	}
	for _, conn := range m.cancels {
		if conn == id {
			m.fail("lent connection %d with a cancel in flight", id)
		}
	}
	if m.tainted[id] {
		m.fail("lent connection %d whose cancel expired without reporting done", id)
	}
	c.lentTo = tenant
}

func (m *model) apply(o op) {
	if m.stopped {
		return
	}
	p := m.p
	switch o.kind {
	case opDemand:
		m.waiting = int(o.a % 6)
		m.absorb(p.Demand(m.now, m.waiting, nil))
	case opOpened:
		if id, ok := pickID(m.inState(StateConnecting), o.a); ok {
			m.absorb(p.Opened(m.now, id, nil))
			if p.State(id) == StateIdle {
				c := m.conns[id]
				c.clean, c.lastUsed = true, m.now
			}
		}
	case opDialFailed:
		if id, ok := pickID(m.inState(StateConnecting), o.a); ok {
			m.conns[id].open = false
			m.absorb(p.DialFailed(m.now, id, nil))
		}
	case opCheckout, opSwitchTenant:
		if o.kind == opSwitchTenant {
			m.tenant++
		}
		t := m.tenantName()
		allowed := p.Allow(m.now)
		id, ok, acts := p.Checkout(m.now, t, nil)
		if !ok && m.waiting < 6 {
			m.waiting++
			acts = p.Demand(m.now, m.waiting, acts)
		}
		if ok && m.waiting > 0 {
			m.waiting--
		}
		if ok {
			if !allowed {
				m.fail("lent %d while the breaker refused work", id)
			}
			m.lent(id, t)
		}
		m.absorb(acts)
	case opReleaseClean:
		if id, ok := pickID(m.inState(StateLent), o.a); ok {
			c := m.conns[id]
			e := End{Kind: EndClean, Tenant: c.lentTo}
			if o.b%5 == 0 {
				e.Kind = EndUnread
			}
			c.tag = c.lentTo
			c.lastUsed = m.now
			c.clean = false
			m.absorb(p.Release(m.now, id, e, nil))
		}
	case opReleaseDirty:
		if id, ok := pickID(m.inState(StateLent), o.a); ok {
			c := m.conns[id]
			kinds := [...]EndKind{EndVanishedMidStatement, EndVanishedInTx, EndIdleInTx, EndWriteStalled, EndStatementDeadline}
			e := End{Kind: kinds[int(o.b)%len(kinds)], Tenant: c.lentTo, Mid: o.b&8 != 0, InTx: o.b&16 != 0}
			if e.Kind == EndStatementDeadline {
				e.Mid = true
				e.ZeroReply = o.b&32 != 0
			}
			if e.Kind == EndVanishedMidStatement {
				e.Mid = true
			}
			c.clean, c.lastUsed = false, m.now
			c.tag = ""
			if e.ZeroReply {
				m.suspectAll(0)
			}
			m.absorb(p.Release(m.now, id, e, nil))
		}
	case opStepDone:
		ids := append(append(m.inState(StateChecking), m.inState(StateCleaning)...), m.inState(StateResetting)...)
		slices.Sort(ids)
		if id, ok := pickID(ids, o.a); ok {
			c := m.conns[id]
			st := p.State(id)
			status := [...]byte{'I', 'T', 'E'}[int(o.b)%3]
			m.absorb(p.StepDone(m.now, id, status, nil))
			switch {
			case st == StateChecking && p.State(id) != StateChecking:
				c.suspect = false
				c.lastUsed = m.now
			case st == StateResetting && p.State(id) != StateResetting:
				c.clean, c.tag, c.lastUsed = true, "", m.now
			}
		}
	case opStepFailed:
		ids := append(append(m.inState(StateChecking), m.inState(StateCleaning)...), m.inState(StateResetting)...)
		slices.Sort(ids)
		if id, ok := pickID(ids, o.a); ok {
			checking := p.State(id) == StateChecking
			if checking {
				m.suspectAll(id)
			}
			m.absorb(p.StepFailed(m.now, id, Fail(o.b%2), nil))
		}
	case opLost:
		ids := m.live()
		if id, ok := pickID(ids, o.a); ok && p.State(id) != StateConnecting {
			m.conns[id].open = false
			m.suspectAll(id)
			m.losing = id
			m.absorb(p.Lost(m.now, id, LossKind(o.b%4), nil))
			m.losing = 0
		}
	case opDatabaseLost:
		for _, id := range m.live() {
			if p.State(id) == StateConnecting {
				m.conns[id].open = false
				m.absorb(p.DialFailed(m.now, id, nil))
				continue
			}
			if m.conns[id].open {
				m.conns[id].open = false
				m.suspectAll(id)
				m.losing = id
				m.absorb(p.Lost(m.now, id, LossClosed, nil))
				m.losing = 0
			}
		}
	case opCancelDone:
		ids := make([]ConnID, 0, len(m.cancels))
		for id := range m.cancels {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if id, ok := pickID(ids, o.a); ok {
			delete(m.cancels, id)
			m.absorb(p.CancelDone(m.now, id, nil))
		}
	case opClientCancel:
		if id, ok := pickID(m.inState(StateLent), o.a); ok {
			_, acts := p.ClientCancel(m.now, id, nil)
			m.absorb(acts)
		}
	case opAdvance:
		m.advance(advanceSteps[int(o.a)%len(advanceSteps)])
	}
}

func (m *model) advance(d time.Duration) {
	m.now += Instant(d)
	for id, until := range m.cancelExpiries() {
		if m.now >= until {
			if conn, ok := m.cancels[id]; ok && m.p.State(conn) != 0 {
				m.tainted[conn] = true
			}
			delete(m.cancels, id)
		}
	}
	m.absorb(m.p.Tick(m.now, nil))
}

func (m *model) cancelExpiries() map[ConnID]Instant {
	out := make(map[ConnID]Instant, len(m.p.cancels))
	for _, e := range m.p.cancels {
		out[e.id] = e.expires
	}
	for id := range m.cancels {
		if _, ok := out[id]; !ok {
			out[id] = m.now
		}
	}
	return out
}

func (m *model) finish() {
	for round := 0; round < 4*maxOps && m.violation == ""; round++ {
		progressed := false
		for _, id := range m.live() {
			switch m.p.State(id) {
			case StateLent:
				c := m.conns[id]
				c.tag, c.lastUsed, c.clean = c.lentTo, m.now, false
				m.absorb(m.p.Release(m.now, id, End{Kind: EndClean, Tenant: c.lentTo}, nil))
				progressed = true
			case StateChecking, StateCleaning, StateResetting:
				if m.p.conns[id].step == stepAwaitCancel {
					continue
				}
				st := m.p.State(id)
				m.absorb(m.p.StepDone(m.now, id, 'I', nil))
				if st == StateChecking && m.p.State(id) != StateChecking {
					m.conns[id].suspect, m.conns[id].lastUsed = false, m.now
				}
				if st == StateResetting && m.p.State(id) != StateResetting {
					c := m.conns[id]
					c.clean, c.tag, c.lastUsed = true, "", m.now
				}
				progressed = true
			}
		}
		ids := make([]ConnID, 0, len(m.cancels))
		for id := range m.cancels {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			delete(m.cancels, id)
			m.absorb(m.p.CancelDone(m.now, id, nil))
			progressed = true
		}
		m.checkSuspect()
		if !progressed {
			break
		}
	}
	m.waiting = 0
	m.absorb(m.p.Demand(m.now, 0, nil))
	m.advance(DefaultMaxLifetime + DefaultIdleTimeout)
	m.advance(DefaultMaxLifetime + DefaultIdleTimeout)
	if m.violation != "" {
		return
	}
	if err := m.p.Quiesce(); err != nil {
		m.fail("quiesce: %v", err)
	}
	s := m.p.Snapshot()
	if s.Lent.Value != 0 || s.Checking.Value != 0 || s.Suspect.Value != 0 || s.CancelsInFlight.Value != 0 {
		m.fail("steady state: lent %d checking %d suspect %d cancels %d", s.Lent.Value, s.Checking.Value, s.Suspect.Value, s.CancelsInFlight.Value)
	}
	if m.p.Next() != 0 && m.p.Breaker() == BreakerClosed && s.Open.Value == 0 {
		m.fail("steady state: a timer at %d with nothing open", m.p.Next())
	}
}

type runResult struct {
	violation string
	trace     []string
	ops       []op
}

func (r runResult) String() string {
	var b strings.Builder
	for _, o := range r.ops {
		b.WriteString(o.String())
		b.WriteString(" ")
	}
	return b.String()
}

func runOps(cfg Config, ops []op) runResult { return run(cfg, ops, true) }

func run(cfg Config, ops []op, record bool) runResult {
	m, err := newModel(cfg)
	if err != nil {
		return runResult{violation: err.Error()}
	}
	m.record = record
	for _, o := range ops {
		m.apply(o)
		m.checkSuspect()
		if m.violation != "" {
			break
		}
		if o.kind != opAdvance {
			m.advance(time.Millisecond)
		}
	}
	if m.violation == "" {
		m.finish()
	}
	return runResult{violation: m.violation, trace: m.trace, ops: ops}
}
