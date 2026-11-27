package admission

import (
	"fmt"
	"slices"
)

const (
	fairTenants     = 4
	fairOpBytes     = 3
	fairMaxOps      = 96
	fairTimeUnit    = 8 * Millisecond
	fairMaxLimit    = 5
	fairMaxLeases   = 6
	fairInterval    = 100 * Millisecond
	fairTenantSlots = 16
)

type fairOpKind uint8

const (
	fopPush fairOpKind = iota
	fopLend
	fopUnpop
	fopEnd
	fopRemove
	fopLimit
	fopAdvance
	numFairOps
)

var fairOpNames = [numFairOps]string{"push", "lend", "unpop", "end", "remove", "limit", "advance"}

type fairOp struct {
	kind fairOpKind
	arg  int
	dt   Time
}

func (o fairOp) String() string { return fmt.Sprintf("%s(%d)+%d", fairOpNames[o.kind], o.arg, o.dt) }

func decodeFairOps(b []byte) []fairOp {
	n := min(len(b)/fairOpBytes, fairMaxOps)
	ops := make([]fairOp, 0, n)
	for i := range n {
		k, a, c := b[i*fairOpBytes], b[i*fairOpBytes+1], b[i*fairOpBytes+2]
		o := fairOp{arg: int(a), dt: Time(c%64) * fairTimeUnit}
		switch k % 12 {
		case 0, 1, 2, 3:
			o.kind = fopPush
		case 4, 5, 6:
			o.kind = fopLend
		case 7:
			o.kind = fopUnpop
		case 8, 9:
			o.kind = fopEnd
		case 10:
			o.kind = fopRemove
		default:
			o.kind = fopLimit
		}
		if c >= 192 {
			o.dt = []Time{0, 1, 1600 * Millisecond, 1600*Millisecond - 1, 2400 * Millisecond, 0, 3 * Millisecond, 0}[c%8]
		}
		if k >= 240 {
			o.kind = fopAdvance
		}
		ops = append(ops, o)
	}
	return ops
}

type fmItem struct {
	id  int64
	seq int64
}

type fmTenant struct {
	name     string
	weight   int64
	exists   bool
	items    []fmItem
	emptyAt  Time
	turn     int64
	inFlight int
	atShare  Time
}

type fmModel struct {
	limit     int
	maxLeases int
	quiet     Time
	tenants   []*fmTenant
	round     int64
	seq       int64
	last      Time
	saturated Time
	leases    []string
	prevTurn  int64
	prevSeq   int64
	prevEmpty Time
}

func newFairModel(limit, maxLeases int, weights []int64, quiet Time) *fmModel {
	m := &fmModel{limit: limit, maxLeases: maxLeases, quiet: quiet}
	for i := range fairTenants {
		m.tenants = append(m.tenants, &fmTenant{name: tenantName(i), weight: weights[i]})
	}
	return m
}

func (m *fmModel) total() int {
	n := 0
	for _, t := range m.tenants {
		n += t.inFlight
	}
	return n
}

func (m *fmModel) active(t *fmTenant) bool { return len(t.items) > 0 || t.inFlight > 0 }

func (m *fmModel) activeWeight() int64 {
	var w int64
	for _, t := range m.tenants {
		if m.active(t) {
			w += t.weight
		}
	}
	return w
}

func (m *fmModel) share(t *fmTenant) int {
	w := m.activeWeight()
	if !m.active(t) {
		w += t.weight
	}
	return int(max(1, int64(m.limit)*t.weight/w))
}

func (m *fmModel) advance(now Time) {
	if now > m.last {
		dt := now - m.last
		if m.total() >= m.limit && m.total() > 0 {
			m.saturated += dt
		}
		for _, t := range m.tenants {
			if t.inFlight > 0 && t.inFlight >= m.share(t) {
				t.atShare += dt
			}
		}
		m.last = now
	}
	for _, t := range m.tenants {
		if t.exists && len(t.items) == 0 && now >= t.emptyAt+m.quiet {
			t.exists = false
		}
	}
}

func (m *fmModel) push(now Time, t *fmTenant, id int64) {
	if !t.exists {
		t.exists = true
		t.turn = 2*m.round - 1
		t.emptyAt = now
	}
	m.seq++
	t.items = append(t.items, fmItem{id: id, seq: m.seq})
}

func (m *fmModel) under(t *fmTenant) bool { return t.inFlight+1 <= m.share(t) }

func (m *fmModel) eligible(t *fmTenant) bool {
	total := m.total()
	if total >= m.limit {
		return false
	}
	return total+1 < m.limit || m.under(t)
}

func (m *fmModel) choose() *fmTenant {
	var best *fmTenant
	for _, t := range m.tenants {
		if len(t.items) == 0 || !m.eligible(t) {
			continue
		}
		if best != nil && m.under(best) && !m.under(t) {
			continue
		}
		if best == nil || m.under(t) && !m.under(best) || t.turn < best.turn || t.turn == best.turn && t.items[0].seq < best.items[0].seq {
			best = t
		}
	}
	return best
}

func (m *fmModel) pop(now Time) (*fmTenant, int64) {
	t := m.choose()
	if t == nil {
		return nil, 0
	}
	id := t.items[0].id
	m.prevTurn, m.prevSeq, m.prevEmpty = t.turn, t.items[0].seq, t.emptyAt
	t.items = t.items[1:]
	m.round++
	t.turn = 2 * m.round
	if len(t.items) == 0 {
		t.emptyAt = now
	}
	return t, id
}

func (m *fmModel) find(name string) *fmTenant {
	for _, t := range m.tenants {
		if t.name == name {
			return t
		}
	}
	return nil
}

func (m *fmModel) remove(now Time, id int64) bool {
	for _, t := range m.tenants {
		for i, it := range t.items {
			if it.id == id {
				t.items = slices.Delete(t.items, i, i+1)
				if len(t.items) == 0 {
					t.emptyAt = now
				}
				return true
			}
		}
	}
	return false
}

type fairWorld struct {
	q       *Queues
	f       *Fair
	m       *fmModel
	weights []int64
	now     Time
	nextID  int64
	queued  []int64
	owner   map[int64]string
	log     []string
	claims  claimState
	exact   bool
}

type claimState struct {
	lastPick map[string]int64
	since    map[string]int64
	picks    int64
	leases   []string
}

func fairWeights(variant int) []int64 {
	if variant%2 == 1 {
		return []int64{3, 1, 2, 1}
	}
	return []int64{1, 1, 1, 1}
}

func fairQueueConfig(rule RuleKind) Config {
	c := Config{Rule: rule, Interval: fairInterval, Capacity: 64, TenantCapacity: fairTenantSlots, MaxTenants: 8, LIFO: LIFOOff}
	if rule != RuleFIFO {
		c.Target = 20 * Millisecond
		c.LIFO = LIFOAdaptive
		c.LIFOHold = 50 * Millisecond
	}
	return c
}

func newFairWorld(rule RuleKind, variant int, plant FairPlant) (*fairWorld, error) {
	q, err := New(fairQueueConfig(rule))
	if err != nil {
		return nil, err
	}
	weights := fairWeights(variant)
	var tw []TenantWeight
	for i, w := range weights {
		if w != DefaultWeight {
			tw = append(tw, TenantWeight{Tenant: tenantName(i), Weight: int(w)})
		}
	}
	limit := 2 + variant%3
	f, err := NewFair(FairConfig{Limit: limit, MaxLeases: fairMaxLeases, Weights: tw, Plant: plant})
	if err != nil {
		return nil, err
	}
	q.SetDispatcher(f)
	w := &fairWorld{q: q, f: f, weights: weights, now: 1, owner: map[int64]string{}, exact: rule == RuleFIFO,
		claims: claimState{lastPick: map[string]int64{}, since: map[string]int64{}}}
	w.m = newFairModel(limit, fairMaxLeases, weights, q.Config().Quiet())
	return w, nil
}

func (w *fairWorld) logf(format string, args ...any) {
	if len(w.log) < 4*fairMaxOps {
		w.log = append(w.log, fmt.Sprintf(format, args...))
	}
}

func (w *fairWorld) drain() {
	for {
		s, ok := w.q.TakeShed()
		if !ok {
			return
		}
		w.forget(s.ID)
	}
}

func (w *fairWorld) forget(id int64) {
	if i := slices.Index(w.queued, id); i >= 0 {
		w.queued = slices.Delete(w.queued, i, i+1)
	}
}

func (w *fairWorld) apply(o fairOp) error {
	w.now += o.dt
	if w.exact {
		w.m.advance(w.now)
	}
	w.logf("t=%d %v", w.now, o)
	if err := w.op(o); err != nil {
		return err
	}
	w.q.Sweep(w.now)
	w.drain()
	return nil
}

func (w *fairWorld) op(o fairOp) error {
	switch o.kind {
	case fopPush:
		return w.push(o.arg % fairTenants)
	case fopLend, fopUnpop:
		return w.pop(o.kind == fopLend)
	case fopEnd:
		return w.end(o.arg)
	case fopRemove:
		return w.remove(o.arg)
	case fopLimit:
		n := 1 + o.arg%fairMaxLimit
		if !w.q.SetLimit(w.now, n) {
			return fmt.Errorf("limit %d refused", n)
		}
		w.m.limit = n
	}
	return nil
}

func (w *fairWorld) push(i int) error {
	if len(w.queued) >= fairTenantSlots {
		return nil
	}
	name := tenantName(i)
	if w.exact && len(w.m.find(name).items) >= fairTenantSlots {
		return nil
	}
	w.nextID++
	if !w.q.Push(w.now, name, w.nextID) {
		return fmt.Errorf("push %d refused", w.nextID)
	}
	w.drain()
	w.queued = append(w.queued, w.nextID)
	w.owner[w.nextID] = name
	if w.exact {
		w.m.push(w.now, w.m.find(name), w.nextID)
	}
	return nil
}

func (w *fairWorld) pop(lend bool) error {
	w.q.Sweep(w.now)
	w.drain()
	before := w.claimView()
	id, ok := w.q.Pop(w.now)
	w.drain()
	if err := w.claimsAtPop(before, id, ok, lend); err != nil {
		return err
	}
	if w.exact {
		mt, mid := w.m.pop(w.now)
		switch {
		case mt == nil && ok:
			return fmt.Errorf("pop gave %d, the model gives nothing", id)
		case mt != nil && !ok:
			return fmt.Errorf("pop gave nothing, the model gives %d of %s", mid, mt.name)
		case ok && mid != id:
			return fmt.Errorf("pop gave %d of %s, the model gives %d of %s", id, w.owner[id], mid, mt.name)
		}
		if ok && !lend {
			w.m.unpop(mt, mid, w.now)
		}
	}
	if !ok {
		return nil
	}
	w.logf("  popped %d of %s", id, w.owner[id])
	if !lend {
		if !w.q.Unpop() {
			return fmt.Errorf("unpop of %d refused", id)
		}
		return nil
	}
	w.forget(id)
	name := w.owner[id]
	if !w.q.LeaseStart(w.now, name) {
		return fmt.Errorf("lease start for %s refused", name)
	}
	if w.exact {
		w.m.find(name).inFlight++
		w.m.leases = append(w.m.leases, name)
	} else {
		w.claims.leases = append(w.claims.leases, name)
	}
	return nil
}

func (m *fmModel) unpop(t *fmTenant, id int64, now Time) {
	m.round--
	t.turn = m.prevTurn
	t.items = append([]fmItem{{id: id, seq: m.prevSeq}}, t.items...)
	t.emptyAt = m.prevEmpty
	_ = now
}

func (w *fairWorld) leases() *[]string {
	if w.exact {
		return &w.m.leases
	}
	return &w.claims.leases
}

func (w *fairWorld) end(k int) error {
	ls := w.leases()
	if len(*ls) == 0 {
		return nil
	}
	i := k % len(*ls)
	name := (*ls)[i]
	*ls = slices.Delete(*ls, i, i+1)
	if !w.q.LeaseEnd(w.now, name) {
		return fmt.Errorf("lease end for %s refused", name)
	}
	if w.exact {
		w.m.find(name).inFlight--
	}
	return nil
}

func (w *fairWorld) remove(k int) error {
	w.q.Sweep(w.now)
	w.drain()
	if len(w.queued) == 0 {
		return nil
	}
	id := w.queued[k%len(w.queued)]
	if !w.q.Remove(w.now, id) {
		return fmt.Errorf("remove %d failed", id)
	}
	w.forget(id)
	if w.exact && !w.m.remove(w.now, id) {
		return fmt.Errorf("model lost %d", id)
	}
	return nil
}

func (w *fairWorld) compare() error {
	if err := w.q.Check(); err != nil {
		return err
	}
	s := w.q.Snapshot()
	if err := s.Check(); err != nil {
		return err
	}
	ls := w.leases()
	if int64(len(*ls)) != s.Fair.InFlight.Value || w.q.InFlight() != len(*ls) {
		return fmt.Errorf("in flight %d (snapshot %d), leases held %d", w.q.InFlight(), s.Fair.InFlight.Value, len(*ls))
	}
	for i := range fairTenants {
		name := tenantName(i)
		want := 0
		for _, l := range *ls {
			if l == name {
				want++
			}
		}
		if got := w.q.TenantInFlight(name); got != want {
			return fmt.Errorf("%s in flight %d, leases held %d", name, got, want)
		}
	}
	if s.Fair.States.Value > int64(len(*ls)) || s.Fair.States.Peak > fairMaxLeases {
		return fmt.Errorf("%d fair states for %d leases", s.Fair.States.Value, len(*ls))
	}
	if !w.exact {
		return nil
	}
	if s.Fair.Rounds != w.m.round {
		return fmt.Errorf("rounds %d, model %d", s.Fair.Rounds, w.m.round)
	}
	if s.Fair.Saturated != w.m.saturated {
		return fmt.Errorf("saturated %d, model %d", s.Fair.Saturated, w.m.saturated)
	}
	for _, row := range s.PerTenant {
		mt := w.m.find(row.Tenant)
		if mt == nil {
			return fmt.Errorf("row for unknown tenant %q", row.Tenant)
		}
		if row.AtShare != mt.atShare {
			return fmt.Errorf("%s time at share %d, model %d", row.Tenant, row.AtShare, mt.atShare)
		}
		if row.Depth != int64(len(mt.items)) || row.Live != mt.exists {
			return fmt.Errorf("%s depth %d live %v, model %d %v", row.Tenant, row.Depth, row.Live, len(mt.items), mt.exists)
		}
		if m := w.m.active(mt); m && row.Share != int64(w.m.share(mt)) || !m && row.Share != 0 {
			return fmt.Errorf("%s share %d, model %d (active %v)", row.Tenant, row.Share, w.m.share(mt), m)
		}
	}
	return nil
}

func runFairOps(rule RuleKind, variant int, plant FairPlant, ops []fairOp) (*fairWorld, error) {
	w, err := newFairWorld(rule, variant, plant)
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		if err := w.apply(o); err != nil {
			return w, err
		}
		if err := w.compare(); err != nil {
			return w, err
		}
		w.track()
	}
	return w, nil
}

type claimView struct {
	total    int
	limit    int
	inFlight map[string]int
	waiting  map[string]bool
	weight   int64
}

func (w *fairWorld) claimView() claimView {
	v := claimView{total: w.q.InFlight(), limit: w.q.Limit(), inFlight: map[string]int{}, waiting: map[string]bool{}}
	for i := range fairTenants {
		name := tenantName(i)
		v.inFlight[name] = w.q.TenantInFlight(name)
	}
	for _, row := range w.q.Snapshot().PerTenant {
		if row.Depth > 0 {
			v.waiting[row.Tenant] = true
		}
	}
	for i := range fairTenants {
		name := tenantName(i)
		if v.waiting[name] || v.inFlight[name] > 0 {
			v.weight += w.weights[i]
		}
	}
	return v
}

func (v claimView) share(w *fairWorld, name string) int {
	i := int(name[1] - 'a')
	return ShareOf(v.limit, w.weights[i], v.weight)
}

func (v claimView) under(w *fairWorld, name string) bool {
	return v.inFlight[name]+1 <= v.share(w, name)
}

func (v claimView) eligible(w *fairWorld, name string) bool {
	if v.total >= v.limit {
		return false
	}
	return v.total+1 < v.limit || v.under(w, name)
}

func (w *fairWorld) claimsAtPop(v claimView, id int64, ok, lend bool) error {
	if !ok {
		for name := range v.waiting {
			if v.eligible(w, name) {
				return fmt.Errorf("claim work-conserving: nothing popped while %s waits eligible (in flight %d of %d, own %d, share %d)",
					name, v.total, v.limit, v.inFlight[name], v.share(w, name))
			}
		}
		return nil
	}
	x := w.owner[id]
	if v.total >= v.limit {
		return fmt.Errorf("claim limit: %s popped with %d in flight at limit %d", x, v.total, v.limit)
	}
	if v.total+1 >= v.limit && v.inFlight[x]+1 > v.share(w, x) {
		return fmt.Errorf("claim share: %s lent to %d in flight with share %d at the limit (%d of %d)", x, v.inFlight[x]+1, v.share(w, x), v.total+1, v.limit)
	}
	if !v.under(w, x) {
		for y := range v.waiting {
			if v.under(w, y) {
				return fmt.Errorf("claim share order: %s lent at or above its share (%d, share %d) while %s waits below its share", x, v.inFlight[x], v.share(w, x), y)
			}
		}
	}
	if !lend {
		return nil
	}
	c := &w.claims
	c.picks++
	if w.exact {
		if last, seen := c.lastPick[x]; seen {
			for i := range fairTenants {
				y := tenantName(i)
				since, waiting := c.since[y]
				if y == x || !waiting || !v.waiting[y] || !v.eligible(w, y) || since >= last || v.under(w, x) && !v.under(w, y) {
					continue
				}
				if c.lastPick[y] < last {
					return fmt.Errorf("claim no starvation: %s chosen twice while %s waited eligible since pick %d", x, y, since)
				}
			}
		}
	}
	c.lastPick[x] = c.picks
	return nil
}

func (w *fairWorld) track() {
	depth := map[string]int64{}
	for _, row := range w.q.Snapshot().PerTenant {
		depth[row.Tenant] = row.Depth
	}
	for i := range fairTenants {
		name := tenantName(i)
		_, waiting := w.claims.since[name]
		switch {
		case depth[name] == 0:
			delete(w.claims.since, name)
		case !waiting:
			w.claims.since[name] = w.claims.picks
		}
	}
}
