// Package admsrc maps admission.Snapshot values onto the observability spine as
// component "admission" and provides the admission shedding health rule.
package admsrc

import (
	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	lt = obs.LabelTenant
	lr = obs.LabelReason
	lm = obs.LabelMode

	// MetricShedding is the gauge of tenant queues that shed within the last interval.
	MetricShedding = "shedding"
	// CheckShedding is the health check name registered for admission shedding.
	CheckShedding = "admission"
)

var none = obs.Labels{}

// Metrics returns the full metric descriptor catalog for component "admission".
func Metrics() []obs.MetricDesc {
	return append([]obs.MetricDesc{
		obs.GaugeDesc("rule", lm, "1 for the queue rule this instance runs (mode: timeout, control-law, fifo, folly; the last two are simulator arms)."),
		obs.GaugeDesc("lifo_adaptive", 0, "1 when adaptive LIFO is on (queue.lifo adaptive), 0 when off."),
		obs.GaugeDesc("capacity", 0, "Total capacity across the tenant queues (queue.capacity)."),
		obs.GaugeDesc("per_tenant_capacity", 0, "Capacity of one tenant queue (queue.tenant_capacity)."),
		obs.GaugeDesc("max_tenants", 0, "Cap on tenant queues (queue.max_tenants)."),
		obs.GaugeDesc("queued", 0, "Requests waiting in the tenant queues now.").AsHeadline(),
		obs.HighWaterDesc("queued_peak", 0, "Most requests waiting at once."),
		obs.GaugeDesc("tenants", 0, "Tenant queues that exist now."),
		obs.HighWaterDesc("tenants_peak", 0, "Most tenant queues at once."),
		obs.CounterDesc("tenants_created", 0, "Tenant queues created on first use."),
		obs.CounterDesc("tenants_removed", 0, "Tenant queues removed after 16 quiet intervals."),
		obs.CounterDesc("tenants_evicted", 0, "Empty tenant queues evicted at the tenant cap for a new tenant."),
		obs.CounterDesc("pushes", 0, "Requests offered to the queues."),
		obs.CounterDesc("served", 0, "Requests taken from a queue to a database connection."),
		obs.CounterDesc("removed", 0, "Queued requests removed because their client left."),
		obs.CounterDesc("unpops", 0, "Taken requests put back because no connection could be lent."),
		obs.CounterDesc("shed", lr, "Requests shed, by reason (rule, at-pop, overflow, total, tenants, deadline, undrained: 53M01; breaker: 53M03, refused while queued because the breaker opened)."),
		obs.GaugeDesc("pending_sheds", 0, "Sheds decided and not yet answered by the engine (0 between events)."),
		obs.GaugeDesc("oldest_age", 0, "Age of the oldest queued request at the last queue operation.").WithUnit(obs.UnitNs),
		obs.GaugeDesc("lifo_tenants", 0, "Tenant queues serving newest first now."),
		obs.CounterDesc("lifo_switches", lm, "Adaptive LIFO switches (mode: on, off)."),
		obs.GaugeDesc("rule_states", lm, "Tenant queues in each rule state (mode: normal, standing, dropping, overloaded)."),
		obs.GaugeDesc("load", 0, "Highest 0-100 load gauge over the tenant queues: 100 x min queue delay of the interval / (2 x target)."),
		obs.GaugeDesc(MetricShedding, 0, "Tenant queues that shed within the last interval."),
		obs.HistogramDesc("queue_wait", 0, obs.UnitNs, "Time a served request waited in its tenant queue."),
		obs.GaugeDesc("tenant_depth", lt, "Requests waiting in this tenant's queue now."),
		obs.GaugeDesc("tenant_capacity", lt, "Capacity of this tenant's queue."),
		obs.GaugeDesc("tenant_oldest_age", lt, "Age of the oldest request in this tenant's queue.").WithUnit(obs.UnitNs),
		obs.GaugeDesc("tenant_rule_state", lt|lm, "1 for this tenant queue's rule state (mode: normal, standing, dropping, overloaded)."),
		obs.GaugeDesc("tenant_lifo", lt, "1 while this tenant queue serves newest first."),
		obs.CounterDesc("tenant_lifo_switches", lt|lm, "Adaptive LIFO switches of this tenant (mode: on, off)."),
		obs.CounterDesc("tenant_pushes", lt, "Requests this tenant offered to its queue."),
		obs.CounterDesc("tenant_served", lt, "Requests of this tenant served from its queue."),
		obs.CounterDesc("tenant_removed", lt, "Requests of this tenant removed because the client left."),
		obs.CounterDesc("tenant_shed", lt|lr, "Requests of this tenant shed, by reason."),
		obs.GaugeDesc("tenant_load", lt, "This tenant queue's 0-100 load gauge."),
		obs.GaugeDesc("tenant_shedding", lt, "1 while this tenant queue shed within the last interval."),
		obs.HistogramDesc("tenant_queue_wait", lt, obs.UnitNs, "Time a served request of this tenant waited."),
	}, fairMetrics()...)
}

func b(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// Map emits totals always, and emits queue_wait histograms and per-tenant rows
// only when obs.detail is enabled on the emitter.
func Map(s admission.Snapshot, e *obs.Emitter) {
	for _, k := range admission.RuleKinds() {
		e.Gauge("rule", obs.Labels{Mode: k.String()}, b(s.Rule == k))
	}
	e.Gauge("lifo_adaptive", none, b(s.LIFOMode == admission.LIFOAdaptive))
	e.Gauge("capacity", none, s.Capacity)
	e.Gauge("per_tenant_capacity", none, s.TenantCapacity)
	e.Gauge("max_tenants", none, s.MaxTenants)
	e.Gauge("queued", none, s.Queued.Value)
	e.Gauge("queued_peak", none, s.Queued.Peak)
	e.Gauge("tenants", none, s.Tenants.Value)
	e.Gauge("tenants_peak", none, s.Tenants.Peak)
	e.Counter("tenants_created", none, s.TenantsCreated)
	e.Counter("tenants_removed", none, s.TenantsRemoved)
	e.Counter("tenants_evicted", none, s.TenantsEvicted)
	e.Counter("pushes", none, s.Pushes)
	e.Counter("served", none, s.Served)
	e.Counter("removed", none, s.Removed)
	e.Counter("unpops", none, s.Unpops)
	for r, v := range s.Shed {
		e.Counter("shed", obs.Labels{Reason: admission.Reason(r).String()}, v)
	}
	e.Gauge("pending_sheds", none, s.PendingSheds)
	e.Gauge("oldest_age", none, s.OldestAge)
	e.Gauge("lifo_tenants", none, s.LIFOTenants)
	e.Counter("lifo_switches", obs.Labels{Mode: "on"}, s.LIFOOn)
	e.Counter("lifo_switches", obs.Labels{Mode: "off"}, s.LIFOOff)
	for st, v := range s.RuleStates {
		e.Gauge("rule_states", obs.Labels{Mode: admission.RuleState(st).String()}, v)
	}
	e.Gauge("load", none, s.Load)
	e.Gauge(MetricShedding, none, s.Shedding)
	mapFair(s.Fair, e)
	if e.Detail() {
		e.Histogram("queue_wait", none, s.QueueWait)
		for _, t := range s.PerTenant {
			mapTenant(t, e)
		}
	}
}

func mapTenant(t admission.TenantSnapshot, e *obs.Emitter) {
	l := obs.Labels{Tenant: t.Tenant}
	e.Gauge("tenant_depth", l, t.Depth)
	e.Gauge("tenant_capacity", l, t.Capacity)
	e.Gauge("tenant_oldest_age", l, t.OldestAge)
	for st := range admission.NumRuleStates {
		e.Gauge("tenant_rule_state", obs.Labels{Tenant: t.Tenant, Mode: st.String()}, b(t.Live && t.State == st))
	}
	e.Gauge("tenant_lifo", l, b(t.LIFO))
	e.Counter("tenant_lifo_switches", obs.Labels{Tenant: t.Tenant, Mode: "on"}, t.LIFOOn)
	e.Counter("tenant_lifo_switches", obs.Labels{Tenant: t.Tenant, Mode: "off"}, t.LIFOOff)
	e.Counter("tenant_pushes", l, t.Pushes)
	e.Counter("tenant_served", l, t.Served)
	e.Counter("tenant_removed", l, t.Removed)
	for r, v := range t.Shed {
		e.Counter("tenant_shed", obs.Labels{Tenant: t.Tenant, Reason: admission.Reason(r).String()}, v)
	}
	e.Gauge("tenant_load", l, t.Load)
	e.Gauge("tenant_shedding", l, b(t.Shedding))
	if t.QueueWait.Count > 0 {
		e.Histogram("tenant_queue_wait", l, t.QueueWait)
	}
	mapFairTenant(t, e)
}

// Source wraps a snapshot provider as an obs.Source for component "admission".
func Source(snapshot func() admission.Snapshot) obs.Source {
	return obs.NewSource(obs.ComponentAdmission, Metrics(), snapshot, Map)
}

// HealthRules returns the admission health rule, which reports StateDegraded
// (never StateFailed) while any tenant queue shed within the last interval.
func HealthRules() []obs.Rule {
	return []obs.Rule{{Name: CheckShedding, Check: func(s obs.InstanceSnapshot) (obs.State, string) {
		if n := s.Value(obs.ComponentAdmission, MetricShedding, none); n > 0 {
			return obs.StateDegraded, "admission: a tenant queue is shedding"
		}
		return obs.StateOK, "admission: no queue is shedding"
	}}}
}
