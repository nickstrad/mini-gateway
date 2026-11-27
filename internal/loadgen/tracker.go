package loadgen

import (
	"errors"
	"fmt"
	"slices"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

// Tracker capacity caps, defaults, and SQLSTATE-class label bounds.
const (
	MaxOutstandingCap     = 1024
	DefaultMaxOutstanding = 64
	DefaultLateAfterNs    = 25 * 1_000_000
	MaxClasses            = 32
	ClassOther            = "other"
	ClassLength           = 2
)

// Outcome classifies the final disposition of a sent request.
type Outcome uint8

// Fixed request outcome values exported on the completed metric.
const (
	OutcomeOK Outcome = iota
	OutcomeDBError
	OutcomeRefused
	OutcomeLost
	outcomeCount
)

// String returns the CSV/metric label name for the outcome.
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

// Outcomes returns all four outcomes in fixed export order.
func Outcomes() []Outcome { return []Outcome{OutcomeOK, OutcomeDBError, OutcomeRefused, OutcomeLost} }

// Result carries the classified outcome and optional five-character SQLSTATE code of a reply.
type Result struct {
	Outcome Outcome
	Code    string
}

// Dispatch instructs the runner to enqueue Op onto connection Conn.
type Dispatch struct {
	Conn int
	Op   Op
}

// TrackerConfig holds connection concurrency, lateness threshold, and test-only plant settings.
type TrackerConfig struct {
	Conns          int
	MaxOutstanding int
	LateAfterNs    int64
	Plant          Plant
}

// Plant selects an optional test-only defect injected into Tracker to verify open-loop checks.
type Plant uint8

// Plant values; PlantSlowSchedule turns the tracker closed-loop for canary tests only.
const (
	PlantNone Plant = iota
	PlantSlowSchedule
)

type slot struct {
	index  int64
	dueAt  int64
	sentAt int64
}

// connState tracks one connection's FIFO ring of outstanding requests:
// indices [0, sent) are in flight on the wire; [sent, length) are queued to write.
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

// ClassCount records how many error replies fell into a two-character SQLSTATE class (or ClassOther).
type ClassCount struct {
	Class string
	Count int64
}

// Snapshot is an immutable point-in-time view of Tracker's counters and latency histograms.
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

// CompletedTotal returns the sum of completed requests across all four outcomes.
func (s Snapshot) CompletedTotal() int64 {
	var n int64
	for _, v := range s.Completed {
		n += v
	}
	return n
}

// OnTime returns the count of sent requests written within LateAfterNs of their intended time.
func (s Snapshot) OnTime() int64 { return s.Sent - s.Late }

// Shortfall reports whether any intended request was written late or could not be sent.
func (s Snapshot) Shortfall() bool { return s.Late+s.CouldNotSend > 0 }

// ErrConservation is wrapped when a Snapshot violates its request accounting equations.
var ErrConservation = errors.New("loadgen: conservation broken")

// Check verifies the conservation laws:
// Intended == Sent + CouldNotSend + Queued, Sent == CompletedTotal + InFlight,
// Late <= Sent, and Intended <= Planned.
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

// Tracker owns open-loop scheduling, connection slot rings, and request accounting.
// It is single-owner and reads no wall clock; callers supply monotonic timestamps in nanoseconds.
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

// ErrTracker is wrapped when TrackerConfig fails validation.
var ErrTracker = errors.New("loadgen: invalid tracker config")

// NewTracker validates plan and cfg, applies defaults for zero MaxOutstanding and
// LateAfterNs, and assigns connections round-robin across tenants (c mod Tenants).
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

// Plan returns the Plan driving this tracker.
func (t *Tracker) Plan() Plan { return t.plan }

// Config returns the effective TrackerConfig after defaults have been filled in.
func (t *Tracker) Config() TrackerConfig { return t.cfg }

// ConnTenant returns the tenant index assigned to connection c.
func (t *Tracker) ConnTenant(c int) int { return t.conns[c].tenant }

// ConnUp marks connection c ready to accept dispatches; duplicate calls are ignored.
func (t *Tracker) ConnUp(c int) {
	if c < 0 || c >= len(t.conns) || t.conns[c].up {
		return
	}
	t.conns[c].up = true
	t.snap.ConnsOpen++
}

// ConnectFailed records a failed connection or login attempt during setup.
func (t *Tracker) ConnectFailed(c int) {
	if c < 0 || c >= len(t.conns) {
		return
	}
	t.snap.ConnectFailures++
}

// NextAt returns the intended send time of the next scheduled operation, or
// (_, false) once stopped or all planned operations have been intended.
func (t *Tracker) NextAt() (int64, bool) {
	if t.stopped || t.next >= t.plan.Count() {
		return 0, false
	}
	return t.plan.At(t.next), true
}

// Done reports whether no further operations will be intended (stopped or plan exhausted).
func (t *Tracker) Done() bool { return t.stopped || t.next >= t.plan.Count() }

// Tick intends every operation due at or before now, assigning each to the
// least-loaded open connection of its tenant or counting it CouldNotSend at once.
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

// Sent records that the next queued request on connection c was written at now.
// Sends strictly past LateAfterNs after their intended time increment Late; equality is on time.
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

// Completed records the FIFO reply for the oldest in-flight request on connection c.
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
	// Record both intended-time latency (catches coordinated omission) and send-time latency (control).
	t.latency.Record(max(now-s.dueAt, 0))
	t.fromSend.Record(max(now-s.sentAt, 0))
}

func classOf(code string) string {
	if len(code) < ClassLength {
		return ClassOther
	}
	return code[:ClassLength]
}

// countClass maintains a sorted slice capped at MaxClasses, folding overflow into ClassOther.
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
	// Reserve the last slot for ClassOther so the exported label set stays bounded at MaxClasses.
	if len(t.classes) >= MaxClasses-1 && class != ClassOther {
		t.countClass(ClassOther)
		return
	}
	t.classes = slices.Insert(t.classes, i, ClassCount{Class: class, Count: 1})
}

// ConnDown marks connection c closed, accounting in-flight requests as OutcomeLost
// and unsent queued requests as CouldNotSend so conservation holds immediately.
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

// Stop halts intending any further operations while allowing in-flight requests to drain.
func (t *Tracker) Stop() { t.stopped = true }

// Finish ends the run, moving all remaining unsent queued requests to CouldNotSend
// while leaving unanswered sent requests counted in InFlight.
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

// Snapshot returns a detached value copy of all counters, error classes, and histograms.
func (t *Tracker) Snapshot() Snapshot {
	s := t.snap
	s.ErrorsByClass = slices.Clone(t.classes)
	s.Latency = t.latency.Snapshot()
	s.LatencyFromSend = t.fromSend.Snapshot()
	return s
}
