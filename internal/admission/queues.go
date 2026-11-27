package admission

import (
	"fmt"
	"slices"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

// Reason explains why a queued or arriving request was shed with 53M01.
type Reason uint8

const (
	// ReasonRule means the tenant queue's shedding rule dropped the head during a sweep.
	ReasonRule Reason = iota
	// ReasonAtPop means FollyRule shed the request at dequeue time.
	ReasonAtPop
	// ReasonOverflow means a full tenant ring shed its oldest entry for a new arrival.
	ReasonOverflow
	// ReasonTotal means total capacity was full and the globally oldest entry was shed.
	ReasonTotal
	// ReasonTenants means MaxTenants was reached with no empty tenant queue to evict.
	ReasonTenants
	// ReasonDeadline means the engine's outer timeouts.queue_wait backstop expired.
	ReasonDeadline
	// ReasonUndrained means Push refused an arrival because pending sheds reached the safety cap.
	ReasonUndrained
	// NumReasons is the number of defined Reason values.
	NumReasons
)

var reasonNames = [NumReasons]string{"rule", "at-pop", "overflow", "total", "tenants", "deadline", "undrained"}

// String returns the canonical lowercase name of r, or "?" if unknown.
func (r Reason) String() string {
	if r < NumReasons {
		return reasonNames[r]
	}
	return "?"
}

// Shed describes one shed decision waiting for the caller to answer on the wire.
type Shed struct {
	ID     int64
	Tenant string
	Reason Reason
	Wait   Time
}

// TenantView is the read-only summary of one non-empty tenant queue passed to Dispatcher.Pick.
type TenantView struct {
	Index     int
	Name      string
	Depth     int
	OldestSeq int64
	OldestEnq Time
	LIFO      bool
}

// Dispatcher chooses which non-empty tenant queue Pop serves next.
// Chunk 9 plugs in fair scheduling here without changing the core rings or rules.
type Dispatcher interface {
	Pick(now Time, active []TenantView) int
}

// OldestFirst is the default Dispatcher: it picks the tenant whose oldest queued
// request arrived earliest (lowest OldestSeq).
type OldestFirst struct{}

// Pick returns the index in active with the lowest OldestSeq, or -1 if active is empty.
func (OldestFirst) Pick(_ Time, active []TenantView) int {
	best := -1
	for i, v := range active {
		if best < 0 || v.OldestSeq < active[best].OldestSeq {
			best = i
		}
	}
	return best
}

type popped struct {
	t       *tenantQueue
	e       entry
	back    bool
	ok      bool
	emptied bool
	emptyAt Time
	rule    ruleState
}

// Queues manages per-tenant fixed-capacity admission rings, shedding rules,
// adaptive LIFO, and cumulative per-tenant counters.
type Queues struct {
	cfg        Config
	dispatcher Dispatcher
	byName     map[string]*tenantQueue
	list       []*tenantQueue
	free       []*tenantQueue
	where      map[int64]*tenantQueue
	stats      map[string]*tenantStats
	statsList  []*tenantStats
	other      *tenantStats
	views      []TenantView
	pending    []Shed
	pendHead   int
	total      int
	seq        int64
	last       Time
	hist       *obs.Histogram
	lastPop    popped
	c          counters
}

type counters struct {
	pushes, served, removed, unpops int64
	shed                            [NumReasons]int64
	queuedPeak                      int64
	tenantsPeak                     int64
	created, removedTenants         int64
	evicted                         int64
	capped                          int64
}

func (q *Queues) stepBudget() int { return 4*(q.cfg.Capacity+q.cfg.MaxTenants) + 16 }

// New constructs a validated Queues instance with defaults applied to zero fields.
func New(cfg Config) (*Queues, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Queues{cfg: cfg, dispatcher: OldestFirst{}, byName: make(map[string]*tenantQueue), where: make(map[int64]*tenantQueue),
		stats: make(map[string]*tenantStats), pending: make([]Shed, 0, pendingCap(cfg)),
		hist: obs.NewDurationHistogram()}, nil
}

// Config returns the effective configuration with defaults applied.
func (q *Queues) Config() Config { return q.cfg }

// SetDispatcher replaces the tenant selection policy; nil restores OldestFirst.
func (q *Queues) SetDispatcher(d Dispatcher) {
	if d == nil {
		d = OldestFirst{}
	}
	q.dispatcher = d
}

// Len returns the total number of requests currently queued across all tenants.
func (q *Queues) Len() int { return q.total }

// Cap returns the configured total queue capacity across all tenants.
func (q *Queues) Cap() int { return q.cfg.Capacity }

// Tenants returns the number of currently live tenant queues.
func (q *Queues) Tenants() int { return len(q.list) }

// Pending returns the number of decided sheds not yet drained via TakeShed.
func (q *Queues) Pending() int { return len(q.pending) - q.pendHead }

// TakeShed removes and returns the next decided shed in FIFO order.
func (q *Queues) TakeShed() (Shed, bool) {
	if q.pendHead >= len(q.pending) {
		return Shed{}, false
	}
	s := q.pending[q.pendHead]
	q.pending[q.pendHead] = Shed{}
	q.pendHead++
	if q.pendHead == len(q.pending) {
		q.pending = q.pending[:0]
		q.pendHead = 0
	}
	return s, true
}

// Next returns the earliest virtual instant at which the core must be swept
// (head expiry, LIFO hold end, shedding-gauge decay, or quiet-tenant removal),
// or 0 if no internal deadline is pending.
func (q *Queues) Next() Time {
	var at Time
	for _, t := range q.list {
		if v := t.next(&q.cfg, q.last); v != 0 && (at == 0 || v < at) {
			at = v
		}
	}
	return at
}

// Sweep advances internal deadlines up to and including now.
func (q *Queues) Sweep(now Time) { q.advance(now) }

// advance steps every internal deadline due at or before now at its own exact
// instant ("time passes before the event") so rule state transitions and drop
// timestamps match waking on each timer individually.
func (q *Queues) advance(now Time) {
	q.lastPop.ok = false
	for range q.stepBudget() {
		at := q.Next()
		if at == 0 || at > now {
			q.step(now)
			return
		}
		q.step(at)
	}
	q.c.capped++
	q.step(now)
}

func (q *Queues) step(now Time) {
	if now > q.last {
		q.last = now
	}
	for _, t := range q.list {
		t.observeLIFO(now, &q.cfg)
		q.sweep(t, now)
		t.observeLIFO(now, &q.cfg)
	}
	q.dropQuiet(now)
}

func (q *Queues) sweep(t *tenantQueue, now Time) {
	if q.cfg.Plant&PlantNoHeadSweep != 0 || t.empty() {
		return
	}
	for !t.empty() && t.rule.SweepHead(now, t.ring.front().enq) {
		e := t.ring.popFront()
		q.shedEntry(t, e, now, ReasonRule)
	}
	if t.empty() {
		q.emptied(t, now)
	}
}

func (q *Queues) emptied(t *tenantQueue, now Time) {
	t.emptyAt = now
	t.rule.Emptied(now)
}

func (q *Queues) dropQuiet(now Time) {
	for i := 0; i < len(q.list); {
		t := q.list[i]
		if t.empty() && now >= t.emptyAt+q.cfg.Quiet() && !t.lifo {
			q.removeTenant(i)
			q.c.removedTenants++
			continue
		}
		i++
	}
}

func (q *Queues) removeTenant(i int) {
	t := q.list[i]
	q.list = slices.Delete(q.list, i, i+1)
	delete(q.byName, t.name)
	t.ring.reset()
	t.rule = nil
	t.stats = nil
	t.lifo, t.hasShed = false, false
	q.free = append(q.free, t)
}

func (q *Queues) shedEntry(t *tenantQueue, e entry, now Time, r Reason) {
	delete(q.where, e.id)
	q.total--
	q.count(t.stats, r)
	t.shedAt, t.hasShed = now, true
	q.compactPending()
	q.pending = append(q.pending, Shed{ID: e.id, Tenant: t.name, Reason: r, Wait: now - e.enq})
}

// compactPending shifts live sheds to index 0 before the slice hits cap so a
// caller that drains only part of the backlog never triggers slice growth.
func (q *Queues) compactPending() {
	if q.pendHead == 0 || len(q.pending) < cap(q.pending) {
		return
	}
	n := copy(q.pending, q.pending[q.pendHead:])
	clear(q.pending[n:])
	q.pending = q.pending[:n]
	q.pendHead = 0
}

func (q *Queues) count(s *tenantStats, r Reason) {
	q.c.shed[r]++
	if s != nil {
		s.shed[r]++
	}
}

// statsFor returns cumulative counters for name, folding tenants past
// MaxTenantStats into a single "other" row (reusing any real tenant named
// "other" so Snapshot never emits duplicate "other" rows).
func (q *Queues) statsFor(name string) *tenantStats {
	if s, ok := q.stats[name]; ok {
		return s
	}
	if len(q.statsList) >= MaxTenantStats {
		if q.other == nil {
			q.other = q.stats[OtherTenant]
		}
		if q.other == nil {
			q.other = q.newStats(OtherTenant)
			q.stats[OtherTenant] = q.other
			q.statsList = append(q.statsList, q.other)
		}
		return q.other
	}
	s := q.newStats(name)
	q.stats[name] = s
	q.statsList = append(q.statsList, s)
	return s
}

func (q *Queues) newStats(name string) *tenantStats {
	s := &tenantStats{name: name}
	if q.cfg.Detail {
		s.hist = obs.NewDurationHistogram()
	}
	return s
}

func (q *Queues) tenantFor(now Time, name string) *tenantQueue {
	if t, ok := q.byName[name]; ok {
		return t
	}
	if len(q.list) >= q.cfg.MaxTenants && !q.evictEmpty() {
		return nil
	}
	var t *tenantQueue
	if n := len(q.free); n > 0 {
		t = q.free[n-1]
		q.free[n-1] = nil
		q.free = q.free[:n-1]
	} else {
		t = &tenantQueue{ring: newRing(q.cfg.TenantCapacity)}
	}
	t.name = name
	t.rule = newRule(q.cfg, now)
	t.emptyAt = now
	t.lastOverload = 0
	t.gauge = loadGauge{target: q.cfg.Target, interval: q.cfg.Interval, intervalEnd: now + q.cfg.Interval, reset: true}
	t.stats = q.statsFor(name)
	q.list = append(q.list, t)
	q.byName[name] = t
	q.c.created++
	q.c.tenantsPeak = max(q.c.tenantsPeak, int64(len(q.list)))
	return t
}

func (q *Queues) evictEmpty() bool {
	best := -1
	for i, t := range q.list {
		if t.empty() && (best < 0 || t.emptyAt < q.list[best].emptyAt) {
			best = i
		}
	}
	if best < 0 {
		return false
	}
	q.removeTenant(best)
	q.c.evicted++
	return true
}

func (q *Queues) refuse(name string, r Reason) bool {
	q.c.pushes++
	s := q.statsFor(name)
	s.pushes++
	q.count(s, r)
	return false
}

// Push enqueues request id for tenant at now. When the tenant ring or total
// capacity is full, Push sheds the oldest queued entry to make room and returns
// true; it returns false only when the arrival itself is refused (tenant cap
// reached with no empty tenant to evict, duplicate id, or undrained shed cap).
func (q *Queues) Push(now Time, tenant string, id int64) bool {
	q.advance(now)
	if q.Pending() >= pendingRefuseAt(q.cfg) {
		return q.refuse(tenant, ReasonUndrained)
	}
	if _, dup := q.where[id]; dup {
		return q.refuse(tenant, ReasonUndrained)
	}
	t := q.tenantFor(now, tenant)
	if t == nil {
		return q.refuse(tenant, ReasonTenants)
	}
	q.c.pushes++
	t.stats.pushes++
	// Record emptiness before overflow so a 1-slot ring shedding its own head
	// does not appear to have emptied and reset its rule state.
	wasEmpty := t.empty()
	if t.ring.full() {
		q.shedEntry(t, t.ring.popFront(), now, ReasonOverflow)
	} else if q.total >= q.cfg.Capacity {
		q.shedOldest(now, t)
	}
	q.seq++
	t.ring.pushBack(entry{id: id, enq: now, seq: q.seq})
	q.where[id] = t
	q.total++
	q.c.queuedPeak = max(q.c.queuedPeak, int64(q.total))
	t.rule.Pushed(now, wasEmpty)
	t.observeLIFO(now, &q.cfg)
	return true
}

func (q *Queues) shedOldest(now Time, arriving *tenantQueue) {
	var oldest *tenantQueue
	for _, t := range q.list {
		if !t.empty() && (oldest == nil || t.ring.front().seq < oldest.ring.front().seq) {
			oldest = t
		}
	}
	if oldest == nil {
		return
	}
	q.shedEntry(oldest, oldest.ring.popFront(), now, ReasonTotal)
	// Skip emptied when oldest is the arriving tenant, which is about to receive the push.
	if oldest.empty() && oldest != arriving {
		q.emptied(oldest, now)
	}
}

// Pop advances deadlines to now, selects a non-empty tenant via the Dispatcher,
// and pops either its newest entry (when LIFO is active) or its oldest.
// Entries rejected by AtPop (FollyRule) are recorded as ReasonAtPop and skipped.
func (q *Queues) Pop(now Time) (int64, bool) {
	q.advance(now)
	for {
		t := q.pick(now)
		if t == nil {
			return 0, false
		}
		back := t.lifo
		var e entry
		if back {
			e = t.ring.popBack()
		} else {
			e = t.ring.popFront()
		}
		wait := now - e.enq
		if t.rule.AtPop(now, wait) {
			q.shedEntry(t, e, now, ReasonAtPop)
			if t.empty() {
				q.emptied(t, now)
			}
			continue
		}
		delete(q.where, e.id)
		q.total--
		q.c.served++
		t.stats.served++
		q.hist.Record(wait)
		if t.stats.hist != nil {
			t.stats.hist.Record(wait)
		}
		t.gauge.observe(now, wait)
		q.lastPop = popped{t: t, e: e, back: back, ok: true}
		// Save pre-empty rule state so an immediate Unpop restores the original deadline.
		if t.empty() {
			q.lastPop.emptied, q.lastPop.emptyAt, q.lastPop.rule = true, t.emptyAt, saveRule(t.rule)
			q.emptied(t, now)
		}
		return e.id, true
	}
}

func (q *Queues) pick(now Time) *tenantQueue {
	q.views = q.views[:0]
	for i, t := range q.list {
		if t.empty() {
			continue
		}
		f := t.ring.front()
		q.views = append(q.views, TenantView{Index: i, Name: t.name, Depth: t.ring.len(), OldestSeq: f.seq, OldestEnq: f.enq, LIFO: t.lifo})
	}
	if len(q.views) == 0 {
		return nil
	}
	i := q.dispatcher.Pick(now, q.views)
	if i < 0 || i >= len(q.views) {
		return nil
	}
	return q.list[q.views[i].Index]
}

// Unpop returns the entry popped by the immediately preceding Pop back to its
// original end of its tenant ring and restores any rule state cleared when that
// pop emptied the ring. The served-wait histogram and load gauge samples remain.
func (q *Queues) Unpop() bool {
	p := q.lastPop
	q.lastPop.ok = false
	if !p.ok || p.t.stats == nil || p.t.ring.full() {
		return false
	}
	if p.back {
		p.t.ring.pushBack(p.e)
	} else {
		p.t.ring.pushFront(p.e)
	}
	if p.emptied {
		p.t.emptyAt = p.emptyAt
		restoreRule(p.t.rule, p.rule)
	}
	q.where[p.e.id] = p.t
	q.total++
	q.c.served--
	p.t.stats.served--
	q.c.unpops++
	return true
}

// Remove deletes a queued request because its client departed, counting it as removed.
func (q *Queues) Remove(now Time, id int64) bool { return q.take(now, id, false) }

// Expire deletes a queued request whose outer queue_wait timer fired, counting
// it as shed with ReasonDeadline.
func (q *Queues) Expire(now Time, id int64) bool { return q.take(now, id, true) }

func (q *Queues) take(now Time, id int64, expired bool) bool {
	q.advance(now)
	t, ok := q.where[id]
	if !ok {
		return false
	}
	i := t.ring.index(id)
	if i < 0 {
		return false
	}
	var e entry
	if q.cfg.Plant&PlantRemoveSwapsTail != 0 {
		e = t.ring.removeSwapTail(i)
	} else {
		e = t.ring.removeAt(i)
	}
	delete(q.where, e.id)
	q.total--
	if expired {
		q.count(t.stats, ReasonDeadline)
		t.shedAt, t.hasShed = now, true
	} else {
		q.c.removed++
		t.stats.removed++
	}
	if t.empty() {
		q.emptied(t, now)
	}
	return true
}

// Close sweeps to now and immediately removes all empty tenant queues (used on
// engine shutdown so no quiet-removal timer remains armed).
func (q *Queues) Close(now Time) {
	q.advance(now)
	for i := 0; i < len(q.list); {
		if !q.list[i].empty() {
			i++
			continue
		}
		q.removeTenant(i)
		q.c.removedTenants++
	}
}

// ShedCount returns the cumulative number of sheds across all reasons.
func (q *Queues) ShedCount() int64 {
	n := int64(0)
	for _, v := range q.c.shed {
		n += v
	}
	return n
}

// Contains reports whether request id is currently queued.
func (q *Queues) Contains(id int64) bool {
	_, ok := q.where[id]
	return ok
}

// Check verifies per-ring capacities, total and index consistency, tenant cap,
// and snapshot conservation without copying histograms.
func (q *Queues) Check() error {
	sum := 0
	for _, t := range q.list {
		if t.ring.len() > q.cfg.TenantCapacity {
			return fmt.Errorf("admission: tenant %q holds %d, capacity %d", t.name, t.ring.len(), q.cfg.TenantCapacity)
		}
		sum += t.ring.len()
	}
	if sum != q.total || len(q.where) != q.total {
		return fmt.Errorf("admission: rings hold %d, total %d, index %d", sum, q.total, len(q.where))
	}
	if q.total > q.cfg.Capacity {
		return fmt.Errorf("admission: %d queued, capacity %d", q.total, q.cfg.Capacity)
	}
	if len(q.list) > q.cfg.MaxTenants {
		return fmt.Errorf("admission: %d tenant queues, cap %d", len(q.list), q.cfg.MaxTenants)
	}
	return q.light().Check()
}

// Quiesce verifies that no requests, live tenant queues, undrained sheds, or
// pending internal deadlines remain.
func (q *Queues) Quiesce() error {
	if q.total != 0 || len(q.list) != 0 || q.Pending() != 0 {
		return fmt.Errorf("admission: %d queued, %d tenant queues, %d sheds not taken", q.total, len(q.list), q.Pending())
	}
	if at := q.Next(); at != 0 {
		return fmt.Errorf("admission: a deadline is still armed at %d", at)
	}
	return nil
}
