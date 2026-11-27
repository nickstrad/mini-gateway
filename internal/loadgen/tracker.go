package loadgen

import (
	"errors"
	"fmt"
	"slices"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	MaxOutstandingCap     = 1024
	DefaultMaxOutstanding = 64
	DefaultLateAfterNs    = 25 * 1_000_000
	MaxClasses            = 32
	ClassOther            = "other"
	ClassLength           = 2
)

type Outcome uint8

const (
	OutcomeOK Outcome = iota
	OutcomeDBError
	OutcomeRefused
	OutcomeLost
	outcomeCount
)

func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeDBError:
		return "db-error"
	case OutcomeRefused:
		return "refused"
	}
	return "lost"
}

func Outcomes() []Outcome { return []Outcome{OutcomeOK, OutcomeDBError, OutcomeRefused, OutcomeLost} }

type Result struct {
	Outcome Outcome
	Code    string
}

type Dispatch struct {
	Conn int
	Op   Op
}

type TrackerConfig struct {
	Conns          int
	MaxOutstanding int
	LateAfterNs    int64
	Plant          Plant
}

type Plant uint8

const (
	PlantNone Plant = iota
	PlantSlowSchedule
)

type slot struct {
	index  int64
	dueAt  int64
	sentAt int64
}

type connState struct {
	up     bool
	tenant int
	ring   []slot
	head   int
	length int
	sent   int
}

func (c *connState) outstanding() int { return c.length }

func (c *connState) at(k int) *slot { return &c.ring[(c.head+k)%len(c.ring)] }

func (c *connState) push(i, dueAt int64) {
	*c.at(c.length) = slot{index: i, dueAt: dueAt}
	c.length++
}

func (c *connState) pop() slot {
	s := *c.at(0)
	c.head = (c.head + 1) % len(c.ring)
	c.length--
	c.sent--
	return s
}

type ClassCount struct {
	Class string
	Count int64
}

type Snapshot struct {
	Planned         int64
	TargetRate      int64
	Intended        int64
	Sent            int64
	CouldNotSend    int64
	Late            int64
	Queued          int64
	InFlight        int64
	Completed       [outcomeCount]int64
	ErrorsByClass   []ClassCount
	ConnsOpen       int64
	ConnectFailures int64
	Latency         obs.HistogramSnapshot
	LatencyFromSend obs.HistogramSnapshot
}

func (s Snapshot) CompletedTotal() int64 {
	var n int64
	for _, v := range s.Completed {
		n += v
	}
	return n
}

func (s Snapshot) OnTime() int64 { return s.Sent - s.Late }

func (s Snapshot) Shortfall() bool { return s.Late+s.CouldNotSend > 0 }

var ErrConservation = errors.New("loadgen: conservation broken")

func (s Snapshot) Check() error {
	var errs []error
	if s.Intended != s.Sent+s.CouldNotSend+s.Queued {
		errs = append(errs, fmt.Errorf("%w: intended %d != sent %d + could not send %d + queued %d", ErrConservation,
			s.Intended, s.Sent, s.CouldNotSend, s.Queued))
	}
	if s.Sent != s.CompletedTotal()+s.InFlight {
		errs = append(errs, fmt.Errorf("%w: sent %d != completed %d + in flight %d", ErrConservation,
			s.Sent, s.CompletedTotal(), s.InFlight))
	}
	if s.Late > s.Sent || s.Intended > s.Planned {
		errs = append(errs, fmt.Errorf("%w: late %d > sent %d or intended %d > planned %d", ErrConservation,
			s.Late, s.Sent, s.Intended, s.Planned))
	}
	return errors.Join(errs...)
}

type Tracker struct {
	plan     Plan
	cfg      TrackerConfig
	conns    []connState
	byTenant [][]int
	rr       []int
	next     int64
	stopped  bool
	snap     Snapshot
	classes  []ClassCount
	latency  *obs.Histogram
	fromSend *obs.Histogram
}

var ErrTracker = errors.New("loadgen: invalid tracker config")

func NewTracker(plan Plan, cfg TrackerConfig) (*Tracker, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if cfg.MaxOutstanding == 0 {
		cfg.MaxOutstanding = DefaultMaxOutstanding
	}
	if cfg.LateAfterNs == 0 {
		cfg.LateAfterNs = DefaultLateAfterNs
	}
	switch {
	case cfg.Conns < plan.Tenants || cfg.Conns > MaxConns:
		return nil, fmt.Errorf("%w: conns %d (want %d to %d: at least one per tenant)", ErrTracker, cfg.Conns, plan.Tenants, MaxConns)
	case cfg.MaxOutstanding < 1 || cfg.MaxOutstanding > MaxOutstandingCap:
		return nil, fmt.Errorf("%w: max outstanding %d (want 1 to %d)", ErrTracker, cfg.MaxOutstanding, MaxOutstandingCap)
	case cfg.LateAfterNs < 0:
		return nil, fmt.Errorf("%w: late threshold %d is negative", ErrTracker, cfg.LateAfterNs)
	}
	t := &Tracker{plan: plan, cfg: cfg, conns: make([]connState, cfg.Conns), byTenant: make([][]int, plan.Tenants),
		rr: make([]int, plan.Tenants), latency: obs.NewDurationHistogram(), fromSend: obs.NewDurationHistogram()}
	for c := range t.conns {
		t.conns[c] = connState{tenant: c % plan.Tenants, ring: make([]slot, cfg.MaxOutstanding)}
		t.byTenant[c%plan.Tenants] = append(t.byTenant[c%plan.Tenants], c)
	}
	t.snap.Planned = plan.Count()
	t.snap.TargetRate = plan.Rate
	return t, nil
}

func (t *Tracker) Plan() Plan { return t.plan }

func (t *Tracker) Config() TrackerConfig { return t.cfg }

func (t *Tracker) ConnTenant(c int) int { return t.conns[c].tenant }

func (t *Tracker) ConnUp(c int) {
	if c < 0 || c >= len(t.conns) || t.conns[c].up {
		return
	}
	t.conns[c].up = true
	t.snap.ConnsOpen++
}

func (t *Tracker) ConnectFailed(c int) {
	if c < 0 || c >= len(t.conns) {
		return
	}
	t.snap.ConnectFailures++
}

func (t *Tracker) NextAt() (int64, bool) {
	if t.stopped || t.next >= t.plan.Count() {
		return 0, false
	}
	return t.plan.At(t.next), true
}

func (t *Tracker) Done() bool { return t.stopped || t.next >= t.plan.Count() }

func (t *Tracker) Tick(now int64, out []Dispatch) []Dispatch {
	if t.stopped {
		return out
	}
	due := t.plan.Due(now)
	for ; t.next < due; t.next++ {
		if t.cfg.Plant == PlantSlowSchedule && t.snap.Queued+t.snap.InFlight > 0 {
			break
		}
		op := t.plan.Op(t.next)
		t.snap.Intended++
		c, ok := t.choose(op.Tenant)
		if !ok {
			t.snap.CouldNotSend++
			continue
		}
		t.conns[c].push(op.Index, t.intendedAt(op.Index, now))
		t.snap.Queued++
		out = append(out, Dispatch{Conn: c, Op: op})
	}
	return out
}

func (t *Tracker) choose(tenant int) (int, bool) {
	cands := t.byTenant[tenant]
	best, bestLoad := -1, 0
	for k := range cands {
		c := cands[(t.rr[tenant]+k)%len(cands)]
		cs := &t.conns[c]
		if !cs.up || cs.outstanding() >= t.cfg.MaxOutstanding {
			continue
		}
		if best < 0 || cs.outstanding() < bestLoad {
			best, bestLoad = c, cs.outstanding()
		}
	}
	if best < 0 {
		return 0, false
	}
	t.rr[tenant] = (t.rr[tenant] + 1) % len(cands)
	return best, true
}

func (t *Tracker) Sent(c int, now int64) bool {
	if c < 0 || c >= len(t.conns) {
		return false
	}
	cs := &t.conns[c]
	if cs.sent >= cs.length {
		return false
	}
	s := cs.at(cs.sent)
	s.sentAt = now
	cs.sent++
	t.snap.Queued--
	t.snap.Sent++
	t.snap.InFlight++
	if now-s.dueAt > t.cfg.LateAfterNs {
		t.snap.Late++
	}
	return true
}

func (t *Tracker) Completed(c int, now int64, r Result) bool {
	if c < 0 || c >= len(t.conns) {
		return false
	}
	cs := &t.conns[c]
	if cs.sent == 0 {
		return false
	}
	s := cs.pop()
	t.snap.InFlight--
	t.complete(s, now, r)
	return true
}

func (t *Tracker) complete(s slot, now int64, r Result) {
	if r.Outcome >= outcomeCount {
		r.Outcome = OutcomeLost
	}
	t.snap.Completed[r.Outcome]++
	if r.Code != "" {
		t.countClass(classOf(r.Code))
	}
	if r.Outcome == OutcomeLost {
		return
	}
	t.latency.Record(max(now-s.dueAt, 0))
	t.fromSend.Record(max(now-s.sentAt, 0))
}

func classOf(code string) string {
	if len(code) < ClassLength {
		return ClassOther
	}
	return code[:ClassLength]
}

func (t *Tracker) countClass(class string) {
	i, found := slices.BinarySearchFunc(t.classes, class, func(a ClassCount, b string) int {
		switch {
		case a.Class < b:
			return -1
		case a.Class > b:
			return 1
		}
		return 0
	})
	if found {
		t.classes[i].Count++
		return
	}
	if len(t.classes) >= MaxClasses-1 && class != ClassOther {
		t.countClass(ClassOther)
		return
	}
	t.classes = slices.Insert(t.classes, i, ClassCount{Class: class, Count: 1})
}

func (t *Tracker) ConnDown(c int) {
	if c < 0 || c >= len(t.conns) {
		return
	}
	cs := &t.conns[c]
	if cs.up {
		cs.up = false
		t.snap.ConnsOpen--
	}
	for cs.sent > 0 {
		s := cs.pop()
		t.snap.InFlight--
		t.complete(s, 0, Result{Outcome: OutcomeLost})
	}
	for cs.length > 0 {
		cs.head = (cs.head + 1) % len(cs.ring)
		cs.length--
		t.snap.Queued--
		t.snap.CouldNotSend++
	}
}

func (t *Tracker) intendedAt(i, now int64) int64 {
	if t.cfg.Plant == PlantSlowSchedule {
		return now
	}
	return t.plan.At(i)
}

func (t *Tracker) Stop() { t.stopped = true }

func (t *Tracker) Finish() {
	t.stopped = true
	for c := range t.conns {
		cs := &t.conns[c]
		for cs.length > cs.sent {
			cs.length--
			t.snap.Queued--
			t.snap.CouldNotSend++
		}
	}
}

func (t *Tracker) Snapshot() Snapshot {
	s := t.snap
	s.ErrorsByClass = slices.Clone(t.classes)
	s.Latency = t.latency.Snapshot()
	s.LatencyFromSend = t.fromSend.Snapshot()
	return s
}
