package admsrc_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

func fairBusy(t *testing.T) *admission.Queues {
	q, err := admission.New(admission.Config{Rule: admission.RuleTimeout, Capacity: 8, TenantCapacity: 4, MaxTenants: 4, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	f, err := admission.NewFair(admission.FairConfig{Limit: 2, MaxLeases: 2, Weights: []admission.TenantWeight{{Tenant: "a", Weight: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	q.SetDispatcher(f)
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Push(0, "b", 3)
	q.Pop(1)
	q.LeaseStart(1, "a")
	q.Pop(1)
	q.LeaseStart(1, "b")
	q.LeaseEnd(admission.Millisecond, "b")
	q.Pop(admission.Millisecond)
	q.LeaseStart(admission.Millisecond, "a")
	q.Push(2*admission.Millisecond, "b", 4)
	q.Pop(2 * admission.Millisecond)
	q.Sweep(3 * admission.Millisecond)
	return q
}

func TestFairCountersMoveAndConserveThroughTheMapping(t *testing.T) {
	q := fairBusy(t)
	if err := q.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	s := collect(t, q, true)
	v := func(m string, l obs.Labels) int64 { return s.Value(obs.ComponentAdmission, m, l) }
	none := obs.Labels{}
	if v("limit", none) != 2 || v("in_flight", none) != 2 || v("in_flight_peak", none) != 2 || v("fair_states", none) != 1 {
		t.Fatalf("limit %d in flight %d peak %d states %d", v("limit", none), v("in_flight", none), v("in_flight_peak", none), v("fair_states", none))
	}
	if v("lease_starts", none)-v("lease_ends", none) != v("in_flight", none) || v("lease_ends", none) != 1 {
		t.Fatalf("lease starts %d ends %d", v("lease_starts", none), v("lease_ends", none))
	}
	if v("tenant_in_flight", obs.Labels{Tenant: "a"})+v("tenant_in_flight", obs.Labels{Tenant: "b"}) != v("in_flight", none) {
		t.Fatal("per-tenant in flight does not sum to the total")
	}
	if v("rounds", none) != 3 || v("held", obs.Labels{Reason: "limit"}) != 1 || v("active_tenants", none) != 2 {
		t.Fatalf("rounds %d held %d active %d", v("rounds", none), v("held", obs.Labels{Reason: "limit"}), v("active_tenants", none))
	}
	if v("tenant_share", obs.Labels{Tenant: "a"}) != 1 || v("tenant_share", obs.Labels{Tenant: "b"}) != 1 {
		t.Fatalf("shares a %d b %d", v("tenant_share", obs.Labels{Tenant: "a"}), v("tenant_share", obs.Labels{Tenant: "b"}))
	}
	if v("saturated_time", none) == 0 || v("tenant_time_at_share", obs.Labels{Tenant: "a"}) == 0 {
		t.Fatal("time counters did not move")
	}
	off := collect(t, q, false)
	if off.Value(obs.ComponentAdmission, "in_flight", none) != 2 || off.Value(obs.ComponentAdmission, "tenant_in_flight", obs.Labels{Tenant: "a"}) != 0 {
		t.Fatal("totals always, per-tenant rows only with detail")
	}
}
