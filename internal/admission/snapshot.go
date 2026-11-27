package admission

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

// TenantSnapshot is an immutable point-in-time view of one tenant's queue
// state and cumulative counters (or the folded "other" bucket past MaxTenantStats).
type TenantSnapshot struct {
	Tenant    string
	Live      bool
	Depth     int64
	Capacity  int64
	OldestAge int64
	State     RuleState
	LIFO      bool
	LIFOOn    int64
	LIFOOff   int64
	Pushes    int64
	Served    int64
	Removed   int64
	Shed      [NumReasons]int64
	Load      int64
	Shedding  bool
	QueueWait obs.HistogramSnapshot
	InFlight  int64
	Share     int64
	AtShare   int64
}

// Snapshot is an immutable point-in-time view of all admission totals,
// served-wait histograms, and sorted per-tenant rows for observability export.
type Snapshot struct {
	Rule           RuleKind
	LIFOMode       LIFOMode
	Capacity       int64
	TenantCapacity int64
	MaxTenants     int64
	Queued         obs.Gauge
	Tenants        obs.Gauge
	TenantsCreated int64
	TenantsRemoved int64
	TenantsEvicted int64
	Pushes         int64
	Served         int64
	Removed        int64
	Unpops         int64
	Shed           [NumReasons]int64
	PendingSheds   int64
	OldestAge      int64
	LIFOTenants    int64
	LIFOOn         int64
	LIFOOff        int64
	RuleStates     [NumRuleStates]int64
	Load           int64
	Shedding       int64
	StepsCapped    int64
	QueueWait      obs.HistogramSnapshot
	PerTenant      []TenantSnapshot
	Fair           FairSnapshot
}

// ShedTotal returns the sum of sheds across all reasons.
func (s Snapshot) ShedTotal() int64 {
	n := int64(0)
	for _, v := range s.Shed {
		n += v
	}
	return n
}

// Check verifies request conservation, tenant-queue conservation, capacity bounds,
// step-budget headroom, and agreement between per-tenant sums and totals.
func (s Snapshot) Check() error {
	parts := []obs.Term{obs.T("served", s.Served), obs.T("removed", s.Removed), obs.T("queued", s.Queued.Value)}
	for r, v := range s.Shed {
		parts = append(parts, obs.T("shed_"+Reason(r).String(), v))
	}
	if err := obs.Conserve(obs.T("pushes", s.Pushes), parts...); err != nil {
		return err
	}
	if s.Queued.Value > s.Capacity || s.Queued.Peak > s.Capacity {
		return fmt.Errorf("admission: queued %d (peak %d) above capacity %d", s.Queued.Value, s.Queued.Peak, s.Capacity)
	}
	if s.Tenants.Value > s.MaxTenants || s.Tenants.Peak > s.MaxTenants {
		return fmt.Errorf("admission: tenants %d (peak %d) above the cap %d", s.Tenants.Value, s.Tenants.Peak, s.MaxTenants)
	}
	if err := obs.Conserve(obs.T("tenants_created", s.TenantsCreated), obs.T("tenants_removed", s.TenantsRemoved),
		obs.T("tenants_evicted", s.TenantsEvicted), obs.T("tenants", s.Tenants.Value)); err != nil {
		return err
	}
	if s.StepsCapped != 0 {
		return fmt.Errorf("admission: %d advances hit the step budget", s.StepsCapped)
	}
	var depth, pushes, served, removed int64
	var shed [NumReasons]int64
	for _, t := range s.PerTenant {
		if limit := s.depthLimit(t.Tenant); t.Depth > limit {
			return fmt.Errorf("admission: tenant %q depth %d above %d", t.Tenant, t.Depth, limit)
		}
		depth += t.Depth
		pushes += t.Pushes
		served += t.Served
		removed += t.Removed
		for r := range shed {
			shed[r] += t.Shed[r]
		}
	}
	if err := s.Fair.check(s.PerTenant); err != nil {
		return err
	}
	if depth != s.Queued.Value || pushes != s.Pushes || served != s.Served || removed != s.Removed || shed != s.Shed {
		return fmt.Errorf("admission: per-tenant sums (depth %d pushes %d served %d removed %d shed %v) differ from the totals (%d %d %d %d %v)",
			depth, pushes, served, removed, shed, s.Queued.Value, s.Pushes, s.Served, s.Removed, s.Shed)
	}
	return nil
}

// depthLimit bounds the folded "other" row by total capacity because it sums
// across multiple live queues; Queues.Check still enforces TenantCapacity on
// every live ring individually.
func (s Snapshot) depthLimit(tenant string) int64 {
	if tenant == OtherTenant {
		return s.Capacity
	}
	return s.TenantCapacity
}

// Snapshot returns a full copy of the admission queues' counters, gauges,
// histograms, and sorted per-tenant rows.
func (q *Queues) Snapshot() Snapshot {
	s := Snapshot{Rule: q.cfg.Rule, LIFOMode: q.cfg.LIFO, Capacity: int64(q.cfg.Capacity), TenantCapacity: int64(q.cfg.TenantCapacity),
		MaxTenants: int64(q.cfg.MaxTenants), Queued: obs.Gauge{Value: int64(q.total), Peak: q.c.queuedPeak},
		Tenants: obs.Gauge{Value: int64(len(q.list)), Peak: q.c.tenantsPeak}, TenantsCreated: q.c.created,
		TenantsRemoved: q.c.removedTenants, TenantsEvicted: q.c.evicted, Pushes: q.c.pushes, Served: q.c.served,
		Removed: q.c.removed, Unpops: q.c.unpops, Shed: q.c.shed, PendingSheds: int64(q.Pending()), StepsCapped: q.c.capped,
		QueueWait: q.hist.Snapshot()}
	live := make(map[*tenantStats]*TenantSnapshot, len(q.list))
	for _, t := range q.list {
		a, ok := live[t.stats]
		if !ok {
			a = &TenantSnapshot{}
			live[t.stats] = a
		}
		a.Live = true
		a.Depth += int64(t.ring.len())
		a.OldestAge = max(a.OldestAge, t.oldestAge(q.last))
		a.State = max(a.State, t.rule.State(q.last, t.ring.len()))
		a.LIFO = a.LIFO || t.lifo
		a.Load = max(a.Load, t.gauge.load())
		a.Shedding = a.Shedding || q.shedding(t)
		s.OldestAge = max(s.OldestAge, t.oldestAge(q.last))
		s.RuleStates[t.rule.State(q.last, t.ring.len())]++
		if t.lifo {
			s.LIFOTenants++
		}
		s.Load = max(s.Load, t.gauge.load())
		if q.shedding(t) {
			s.Shedding++
		}
	}
	for _, st := range q.statsList {
		s.LIFOOn += st.lifoOn
		s.LIFOOff += st.lifoOff
		ts := TenantSnapshot{Tenant: st.name, LIFOOn: st.lifoOn, LIFOOff: st.lifoOff, Pushes: st.pushes, Served: st.served,
			Removed: st.removed, Shed: st.shed, Capacity: int64(q.cfg.TenantCapacity)}
		if st.hist != nil {
			ts.QueueWait = st.hist.Snapshot()
		}
		if a, ok := live[st]; ok {
			ts.Live, ts.Depth, ts.OldestAge, ts.State, ts.LIFO, ts.Load, ts.Shedding = true, a.Depth, a.OldestAge, a.State, a.LIFO, a.Load, a.Shedding
		}
		s.PerTenant = append(s.PerTenant, ts)
	}
	q.fairSnapshot(&s)
	slices.SortFunc(s.PerTenant, func(a, b TenantSnapshot) int { return strings.Compare(a.Tenant, b.Tenant) })
	return s
}

func (q *Queues) shedding(t *tenantQueue) bool { return t.hasShed && q.last-t.shedAt < q.cfg.Interval }

// light builds a minimal Snapshot without copying histograms or sorting rows so
// Engine.Invariants can run Queues.Check after every event cheaply.
func (q *Queues) light() Snapshot {
	s := Snapshot{Capacity: int64(q.cfg.Capacity), TenantCapacity: int64(q.cfg.TenantCapacity), MaxTenants: int64(q.cfg.MaxTenants),
		Queued: obs.Gauge{Value: int64(q.total), Peak: q.c.queuedPeak}, Tenants: obs.Gauge{Value: int64(len(q.list)), Peak: q.c.tenantsPeak},
		TenantsCreated: q.c.created, TenantsRemoved: q.c.removedTenants, TenantsEvicted: q.c.evicted, Pushes: q.c.pushes,
		Served: q.c.served, Removed: q.c.removed, Shed: q.c.shed, StepsCapped: q.c.capped}
	depth := map[*tenantStats]int64{}
	for _, t := range q.list {
		depth[t.stats] += int64(t.ring.len())
	}
	add := func(st *tenantStats) {
		s.PerTenant = append(s.PerTenant, TenantSnapshot{Tenant: st.name, Depth: depth[st], Pushes: st.pushes, Served: st.served,
			Removed: st.removed, Shed: st.shed})
	}
	for _, st := range q.statsList {
		add(st)
	}
	return s
}
