package admission

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

// Fairness configuration defaults and hard bounds validated at config load.
const (
	// DefaultWeight is the weight assigned to any tenant without an explicit weight.
	DefaultWeight = 1
	// MaxWeight is the largest allowed per-tenant or default weight.
	MaxWeight = 1000
	// MaxFairLimit is the largest allowed concurrent-lease limit on a Fair dispatcher.
	MaxFairLimit = 1 << 16
	// MaxWeights is the maximum number of explicit per-tenant weight entries.
	MaxWeights = 4096
)

// FairPlant is a bitmask of test-only fault injections used by canaries and fuzz plants.
type FairPlant uint8

const (
	// FairPlantNoShareBound disables both below-share priority and last-slot reservation.
	FairPlantNoShareBound FairPlant = 1 << iota
	// FairPlantNoRoundRobinStep stops advancing tenant turn counters on pop.
	FairPlantNoRoundRobinStep
	// FairPlantUnpopKeepsTurn skips restoring turn and round state when a pop is undone.
	FairPlantUnpopKeepsTurn
)

// TenantWeight pairs a tenant name with its configured relative share weight.
type TenantWeight struct {
	Tenant string
	Weight int
}

// FairConfig configures a Fair dispatcher's concurrency limit, lease-state cap,
// and per-tenant share weights.
type FairConfig struct {
	Limit         int
	MaxLeases     int
	DefaultWeight int
	Weights       []TenantWeight
	Plant         FairPlant
}

// WithDefaults returns a copy of c with zero Limit, MaxLeases, and DefaultWeight
// replaced by their defaults.
func (c FairConfig) WithDefaults() FairConfig {
	if c.Limit == 0 {
		c.Limit = MaxFairLimit
	}
	if c.MaxLeases == 0 {
		c.MaxLeases = MaxFairLimit
	}
	if c.DefaultWeight == 0 {
		c.DefaultWeight = DefaultWeight
	}
	return c
}

// Validate checks every field of c and returns a joined error wrapping ErrConfig
// that names each offending configuration key.
func (c FairConfig) Validate() error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	if c.Limit < 1 || c.Limit > MaxFairLimit {
		bad("pool.connections", "the fair limit must be 1 to %d, got %d", MaxFairLimit, c.Limit)
	}
	if c.MaxLeases < 1 || c.MaxLeases > MaxFairLimit {
		bad("pool.connections", "must be 1 to %d for the fair dispatcher, got %d", MaxFairLimit, c.MaxLeases)
	}
	if c.DefaultWeight < 1 || c.DefaultWeight > MaxWeight {
		bad("fairness.default_weight", "must be 1 to %d (0 means %d), got %d", MaxWeight, DefaultWeight, c.DefaultWeight)
	}
	if len(c.Weights) > MaxWeights {
		bad("fairness.weights", "at most %d entries, got %d", MaxWeights, len(c.Weights))
		return errors.Join(errs...)
	}
	seen := make(map[string]int, len(c.Weights))
	for i, w := range c.Weights {
		key := "fairness.weights[" + strconv.Itoa(i) + "]"
		if w.Tenant == "" {
			bad(key+".tenant", "required")
		} else if j, dup := seen[w.Tenant]; dup {
			bad(key+".tenant", "%q is also fairness.weights[%d].tenant", w.Tenant, j)
		} else {
			seen[w.Tenant] = i
		}
		if w.Weight < 0 || w.Weight > MaxWeight {
			bad(key+".weight", "must be 0 to %d (0 means fairness.default_weight), got %d", MaxWeight, w.Weight)
		}
	}
	return errors.Join(errs...)
}

type fairTenant struct {
	name     string
	inFlight int
	stats    *tenantStats
}

type fairSaved struct {
	ok    bool
	picks int64
	held  bool
}

type fairCounters struct {
	picks        int64
	heldLimit    int64
	heldShare    int64
	starts       int64
	ends         int64
	refused      int64
	unmatched    int64
	saturated    Time
	inFlightPeak int64
	statesPeak   int64
	activePeak   int64
}

// Fair is a Dispatcher that enforces weighted per-tenant shares over a
// settable concurrency limit. It prefers tenants below their share at every
// lend, uses round-robin order within each group, holds back the last free
// lease only while an active non-waiting tenant is below its share, and
// allocates per-tenant state only for tenants holding an active lease.
type Fair struct {
	cfg     FairConfig
	weights map[string]int
	q       *Queues
	byName  map[string]*fairTenant
	list    []*fairTenant
	free    []*fairTenant
	total   int
	last    Time
	saved   fairSaved
	held    bool
	c       fairCounters
}

// NewFair validates cfg (after applying defaults) and constructs a Fair dispatcher.
func NewFair(cfg FairConfig) (*Fair, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	f := &Fair{cfg: cfg, weights: make(map[string]int, len(cfg.Weights)), byName: make(map[string]*fairTenant)}
	for _, w := range cfg.Weights {
		if w.Weight > 0 {
			f.weights[w.Tenant] = w.Weight
		}
	}
	return f, nil
}

// Config returns the validated FairConfig with defaults applied.
func (f *Fair) Config() FairConfig { return f.cfg }

// Limit returns the current fair concurrency limit.
func (f *Fair) Limit() int { return f.cfg.Limit }

// InFlight returns the total number of leases currently in flight across all tenants.
func (f *Fair) InFlight() int { return f.total }

// TenantInFlight returns the number of leases currently held by tenant name.
func (f *Fair) TenantInFlight(name string) int {
	if t := f.byName[name]; t != nil {
		return t.inFlight
	}
	return 0
}

// States returns the number of tenants currently holding at least one lease
// (and therefore a fairTenant entry).
func (f *Fair) States() int { return len(f.list) }

func (f *Fair) weight(name string) int64 {
	if w, ok := f.weights[name]; ok {
		return int64(w)
	}
	return int64(f.cfg.DefaultWeight)
}

func (f *Fair) waiting(name string) bool {
	if f.q == nil {
		return false
	}
	t := f.q.byName[name]
	return t != nil && !t.empty()
}

func (f *Fair) activeWeight() (int64, int) {
	var sum int64
	n := 0
	if f.q != nil {
		for _, t := range f.q.list {
			if !t.empty() {
				sum += f.weight(t.name)
				n++
			}
		}
	}
	for _, t := range f.list {
		if !f.waiting(t.name) {
			sum += f.weight(t.name)
			n++
		}
	}
	return sum, n
}

// ShareOf returns a tenant's integer share of limit given its weight and the
// total weight of active tenants, using integer floor with a minimum of 1.
func ShareOf(limit int, weight, active int64) int {
	if active <= 0 {
		return limit
	}
	return int(max(1, int64(limit)*weight/active))
}

func (f *Fair) share(name string, active int64) int {
	return ShareOf(f.cfg.Limit, f.weight(name), active)
}

// Share returns the current share of tenant name, treating name as active even
// if it currently has nothing queued and no leases in flight.
func (f *Fair) Share(name string) int {
	w, _ := f.activeWeight()
	if !f.waiting(name) && f.byName[name] == nil {
		w += f.weight(name)
	}
	return f.share(name, w)
}

func (f *Fair) lastSlot() bool { return f.total+1 >= f.cfg.Limit }

// reserved reports whether any active tenant with nothing waiting holds fewer
// leases than its share; only then is the last free lease held back from an
// over-share waiter so rounding remainders never strand capacity.
func (f *Fair) reserved(w int64) bool {
	for _, t := range f.list {
		if !f.waiting(t.name) && t.inFlight < f.share(t.name, w) {
			return true
		}
	}
	return false
}

// releasable returns how many waiting requests successive Pick+LeaseStart calls
// would release right now, without mutating state. Active weight and shares do
// not change as queued items become leases, so summing below-share deficits
// across waiting queues determines whether the last free slot would be held.
func (f *Fair) releasable() int {
	free := f.cfg.Limit - f.total
	n := min(f.q.total, max(free, 0))
	if n == 0 || n < free || !f.shareOn() {
		return n
	}
	w, _ := f.activeWeight()
	absorbed, reserved := 0, f.reserved(w)
	for _, t := range f.q.list {
		if t.empty() {
			continue
		}
		deficit := max(0, f.share(t.name, w)-f.TenantInFlight(t.name))
		absorbed += min(deficit, t.ring.len())
		// A tenant whose deficit exceeds its waiting depth will become an
		// active non-waiting below-share tenant once its queue drains.
		reserved = reserved || deficit > t.ring.len()
	}
	if absorbed >= free || !reserved {
		return free
	}
	return free - 1
}

func (f *Fair) noteActive() {
	_, n := f.activeWeight()
	f.c.activePeak = max(f.c.activePeak, int64(n))
}

func (f *Fair) shareOn() bool { return f.cfg.Plant&FairPlantNoShareBound == 0 }

// Pick selects the next non-empty tenant queue to serve, or returns -1 when
// the fair limit is reached or the last free lease is reserved for an active
// non-waiting tenant below its share. Waiting tenants below their share are
// always chosen ahead of those at or above it; within each group selection is
// round robin by Turn with ties broken by OldestSeq.
func (f *Fair) Pick(_ Time, active []TenantView) int {
	// Save pick count and held state so an immediate Unpop restores them exactly.
	f.saved = fairSaved{ok: true, picks: f.c.picks, held: f.held}
	f.held = true
	if f.total >= f.cfg.Limit {
		f.c.heldLimit++
		return -1
	}
	w, _ := f.activeWeight()
	best, bestUnder := -1, false
	for i, v := range active {
		under := !f.shareOn() || f.TenantInFlight(v.Name)+1 <= f.share(v.Name, w)
		if best < 0 || under && !bestUnder || under == bestUnder && before(v, active[best]) {
			best, bestUnder = i, under
		}
	}
	if best >= 0 && !bestUnder && f.lastSlot() && f.reserved(w) {
		best = -1
	}
	if best < 0 {
		f.c.heldShare++
		return -1
	}
	f.c.picks++
	f.held = false
	return best
}

func before(a, b TenantView) bool {
	if a.Turn != b.Turn {
		return a.Turn < b.Turn
	}
	return a.OldestSeq < b.OldestSeq
}

func (f *Fair) unpick() {
	if f.saved.ok {
		f.c.picks, f.held = f.saved.picks, f.saved.held
	}
	f.saved.ok = false
}

func (f *Fair) account(now Time) {
	if now <= f.last {
		return
	}
	dt := now - f.last
	f.last = now
	if f.total == 0 {
		return
	}
	if f.total >= f.cfg.Limit {
		f.c.saturated += dt
	}
	w, _ := f.activeWeight()
	for _, t := range f.list {
		if t.inFlight >= f.share(t.name, w) {
			t.stats.atShare += dt
		}
	}
}

func (f *Fair) start(name string, stats func(string) *tenantStats) bool {
	if f.total >= f.cfg.MaxLeases {
		f.c.refused++
		return false
	}
	t := f.byName[name]
	if t == nil {
		t = f.newTenant(name, stats(name))
	}
	t.inFlight++
	f.total++
	f.c.starts++
	f.c.inFlightPeak = max(f.c.inFlightPeak, int64(f.total))
	f.noteActive()
	return true
}

func (f *Fair) newTenant(name string, stats *tenantStats) *fairTenant {
	var t *fairTenant
	if n := len(f.free); n > 0 {
		t = f.free[n-1]
		f.free[n-1] = nil
		f.free = f.free[:n-1]
	} else {
		t = &fairTenant{}
	}
	*t = fairTenant{name: name, stats: stats}
	f.byName[name] = t
	f.list = append(f.list, t)
	f.c.statesPeak = max(f.c.statesPeak, int64(len(f.list)))
	return t
}

func (f *Fair) end(name string) bool {
	t := f.byName[name]
	if t == nil {
		f.c.unmatched++
		return false
	}
	t.inFlight--
	f.total--
	f.c.ends++
	if t.inFlight == 0 {
		f.drop(t)
	}
	return true
}

func (f *Fair) drop(t *fairTenant) {
	delete(f.byName, t.name)
	if i := slices.Index(f.list, t); i >= 0 {
		f.list = slices.Delete(f.list, i, i+1)
	}
	*t = fairTenant{}
	f.free = append(f.free, t)
}

func (f *Fair) check() error {
	sum := 0
	for _, t := range f.list {
		if t.inFlight < 1 {
			return fmt.Errorf("admission: fair state for %q kept with %d in flight", t.name, t.inFlight)
		}
		if f.byName[t.name] != t {
			return fmt.Errorf("admission: fair state for %q is not indexed", t.name)
		}
		sum += t.inFlight
	}
	if sum != f.total || len(f.byName) != len(f.list) {
		return fmt.Errorf("admission: per-tenant in flight sums to %d over %d states, total %d over %d", sum, len(f.list), f.total, len(f.byName))
	}
	if f.total > f.cfg.MaxLeases {
		return fmt.Errorf("admission: %d in flight above the lease cap %d", f.total, f.cfg.MaxLeases)
	}
	if int64(f.total) != f.c.starts-f.c.ends {
		return fmt.Errorf("admission: %d in flight, %d lease starts and %d ends", f.total, f.c.starts, f.c.ends)
	}
	if f.c.refused != 0 || f.c.unmatched != 0 {
		return fmt.Errorf("admission: %d lease starts refused at the cap, %d lease ends without a start", f.c.refused, f.c.unmatched)
	}
	return nil
}

func (f *Fair) quiesce() error {
	if f.total != 0 || len(f.list) != 0 {
		return fmt.Errorf("admission: %d in flight over %d fair states", f.total, len(f.list))
	}
	return nil
}

// Fair returns the attached *Fair dispatcher, or nil if another Dispatcher is installed.
func (q *Queues) Fair() *Fair { return q.fair }

func (q *Queues) attach(d Dispatcher) {
	if q.fair != nil {
		q.fair.q = nil
	}
	q.fair = nil
	if f, ok := d.(*Fair); ok {
		q.fair = f
		f.q = q
		f.last = q.last
	}
}

func (q *Queues) fairAccount(now Time) {
	if q.fair != nil {
		q.fair.account(now)
	}
}

// LeaseStart advances time to now and records that tenant began holding one
// database lease. It returns false if no Fair dispatcher is attached or
// MaxLeases would be exceeded.
func (q *Queues) LeaseStart(now Time, tenant string) bool {
	q.advance(now)
	if q.fair == nil {
		return false
	}
	return q.fair.start(tenant, q.statsFor)
}

// LeaseEnd advances time to now and records that one lease held by tenant ended,
// removing tenant's dispatcher state when its in-flight count reaches zero.
func (q *Queues) LeaseEnd(now Time, tenant string) bool {
	q.advance(now)
	if q.fair == nil {
		return false
	}
	return q.fair.end(tenant)
}

// SetLimit advances time to now under the previous limit and updates the Fair
// dispatcher's concurrency limit to n (1..MaxFairLimit). Lowering the limit
// never preempts existing leases.
func (q *Queues) SetLimit(now Time, n int) bool {
	q.advance(now)
	if q.fair == nil || n < 1 || n > MaxFairLimit {
		return false
	}
	q.fair.cfg.Limit = n
	return true
}

// Limit returns the attached Fair dispatcher's concurrency limit, or 0 if none is attached.
func (q *Queues) Limit() int {
	if q.fair == nil {
		return 0
	}
	return q.fair.cfg.Limit
}

// Releasable returns how many queued requests the dispatcher would release
// immediately, used to report pool demand without dialing for held-back requests.
func (q *Queues) Releasable() int {
	if q.fair == nil {
		return q.total
	}
	return q.fair.releasable()
}

// Held reports whether the most recent Fair.Pick held a waiting request back.
func (q *Queues) Held() bool { return q.fair != nil && q.fair.held }

// InFlight returns the total leases currently tracked by the Fair dispatcher, or 0 if detached.
func (q *Queues) InFlight() int {
	if q.fair == nil {
		return 0
	}
	return q.fair.total
}

// TenantInFlight returns the leases currently held by tenant, or 0 if detached.
func (q *Queues) TenantInFlight(tenant string) int {
	if q.fair == nil {
		return 0
	}
	return q.fair.TenantInFlight(tenant)
}

func (q *Queues) takeTurn(t *tenantQueue) {
	q.lastPop.turn, q.lastPop.round = t.turn, q.round
	if q.fair != nil && q.fair.cfg.Plant&FairPlantNoRoundRobinStep != 0 {
		return
	}
	q.round++
	// Even turns mark served tenants; a newly created queue joins at 2*round-1
	// so it falls behind older waiting tenants and ahead of the tenant served last.
	t.turn = 2 * q.round
}

func (q *Queues) untakeTurn(p popped) {
	if q.fair != nil && q.fair.cfg.Plant&FairPlantUnpopKeepsTurn != 0 {
		return
	}
	p.t.turn, q.round = p.turn, p.round
	if q.fair != nil {
		q.fair.unpick()
	}
}

func (q *Queues) fairCheck() error {
	if q.fair == nil {
		return nil
	}
	return q.fair.check()
}

func (q *Queues) fairQuiesce() error {
	if q.fair == nil {
		return nil
	}
	return q.fair.quiesce()
}

// FairSnapshot is an immutable point-in-time view of the Fair dispatcher's
// limit, in-flight leases, state gauges, selection counters, and saturated time.
type FairSnapshot struct {
	On            bool
	Limit         int64
	MaxLeases     int64
	InFlight      obs.Gauge
	States        obs.Gauge
	Active        obs.Gauge
	Rounds        int64
	Picks         int64
	HeldLimit     int64
	HeldShare     int64
	LeaseStarts   int64
	LeaseEnds     int64
	Saturated     int64
	DefaultWeight int64
}

func (s FairSnapshot) check(rows []TenantSnapshot) error {
	if !s.On {
		return nil
	}
	if s.LeaseStarts-s.LeaseEnds != s.InFlight.Value {
		return fmt.Errorf("admission: %d in flight, %d lease starts and %d ends", s.InFlight.Value, s.LeaseStarts, s.LeaseEnds)
	}
	var sum int64
	for _, t := range rows {
		sum += t.InFlight
	}
	if sum != s.InFlight.Value {
		return fmt.Errorf("admission: per-tenant in flight sums to %d, total %d", sum, s.InFlight.Value)
	}
	if s.States.Value > s.InFlight.Value || s.InFlight.Peak > s.MaxLeases || s.States.Peak > s.MaxLeases {
		return fmt.Errorf("admission: %d fair states (peak %d) for %d in flight (peak %d), lease cap %d",
			s.States.Value, s.States.Peak, s.InFlight.Value, s.InFlight.Peak, s.MaxLeases)
	}
	return nil
}

func (q *Queues) fairSnapshot(s *Snapshot) {
	f := q.fair
	if f == nil {
		return
	}
	w, n := f.activeWeight()
	s.Fair = FairSnapshot{On: true, Limit: int64(f.cfg.Limit), MaxLeases: int64(f.cfg.MaxLeases),
		InFlight: obs.Gauge{Value: int64(f.total), Peak: f.c.inFlightPeak}, States: obs.Gauge{Value: int64(len(f.list)), Peak: f.c.statesPeak},
		Active: obs.Gauge{Value: int64(n), Peak: f.c.activePeak}, Rounds: q.round, Picks: f.c.picks,
		HeldLimit: f.c.heldLimit, HeldShare: f.c.heldShare, LeaseStarts: f.c.starts, LeaseEnds: f.c.ends, Saturated: f.c.saturated,
		DefaultWeight: int64(f.cfg.DefaultWeight)}
	rows := make(map[*tenantStats]int, len(s.PerTenant))
	for i, st := range q.statsList {
		rows[st] = i
	}
	for i := range s.PerTenant {
		s.PerTenant[i].AtShare = q.statsList[i].atShare
	}
	for _, t := range f.list {
		if i, ok := rows[t.stats]; ok {
			s.PerTenant[i].InFlight += int64(t.inFlight)
		}
	}
	for i, st := range q.statsList {
		if st == q.other {
			continue
		}
		if s.PerTenant[i].InFlight > 0 || f.waiting(st.name) {
			s.PerTenant[i].Share = int64(f.share(st.name, w))
		}
	}
}

// Depth returns the number of requests currently queued for tenant.
func (q *Queues) Depth(tenant string) int {
	if t := q.byName[tenant]; t != nil {
		return t.ring.len()
	}
	return 0
}
