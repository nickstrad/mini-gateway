package admsrc

import (
	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

// Reason labels emitted on the admission.held counter.
const (
	// HeldLimit marks selections that lent nothing because in-flight leases reached the fair limit.
	HeldLimit = "limit"
	// HeldShare marks selections that held back the last free lease for an active below-share tenant.
	HeldShare = "share"
)

func fairMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("limit", 0, "Most leases in flight at once across tenants (the fair limit; pool.connections)."),
		obs.GaugeDesc("in_flight", 0, "Leases in flight now across tenants; a transaction held open counts until its lease ends.").AsHeadline(),
		obs.HighWaterDesc("in_flight_peak", 0, "Most leases in flight at once."),
		obs.GaugeDesc("active_tenants", 0, "Tenants with a request waiting or a lease in flight now; the limit is divided among them."),
		obs.HighWaterDesc("active_tenants_peak", 0, "Most active tenants at once."),
		obs.GaugeDesc("fair_states", 0, "Tenants with a lease in flight, each holding one dispatcher state."),
		obs.HighWaterDesc("fair_states_peak", 0, "Most tenants with a lease in flight at once."),
		obs.CounterDesc("rounds", 0, "Turns given: requests chosen for a connection in round-robin order across tenants."),
		obs.CounterDesc("held", lr, "Selections that lent nothing, by reason (limit: the limit is reached; share: one lease short of the limit, every waiting tenant is at or above its share while an active tenant is below its share)."),
		obs.CounterDesc("lease_starts", 0, "Leases started, as reported to the dispatcher."),
		obs.CounterDesc("lease_ends", 0, "Leases ended on any path (release, dirty connection, lost connection, shutdown)."),
		obs.CounterDesc("saturated_time", 0, "Time with leases in flight at the limit.").WithUnit(obs.UnitNs),
		obs.GaugeDesc("tenant_in_flight", lt, "Leases this tenant holds now, held transactions included."),
		obs.GaugeDesc("tenant_share", lt, "This tenant's share of the limit now (limit x weight / active weight, at least 1); 0 while inactive."),
		obs.CounterDesc("tenant_time_at_share", lt, "Time this tenant held at least its share of the limit.").WithUnit(obs.UnitNs),
	}
}

func mapFair(f admission.FairSnapshot, e *obs.Emitter) {
	e.Gauge("limit", none, f.Limit)
	e.Gauge("in_flight", none, f.InFlight.Value)
	e.Gauge("in_flight_peak", none, f.InFlight.Peak)
	e.Gauge("active_tenants", none, f.Active.Value)
	e.Gauge("active_tenants_peak", none, f.Active.Peak)
	e.Gauge("fair_states", none, f.States.Value)
	e.Gauge("fair_states_peak", none, f.States.Peak)
	e.Counter("rounds", none, f.Rounds)
	e.Counter("held", obs.Labels{Reason: HeldLimit}, f.HeldLimit)
	e.Counter("held", obs.Labels{Reason: HeldShare}, f.HeldShare)
	e.Counter("lease_starts", none, f.LeaseStarts)
	e.Counter("lease_ends", none, f.LeaseEnds)
	e.Counter("saturated_time", none, f.Saturated)
}

func mapFairTenant(t admission.TenantSnapshot, e *obs.Emitter) {
	l := obs.Labels{Tenant: t.Tenant}
	e.Gauge("tenant_in_flight", l, t.InFlight)
	e.Gauge("tenant_share", l, t.Share)
	e.Counter("tenant_time_at_share", l, t.AtShare)
}
