package admsrc_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/admsrc"
)

func busy(t *testing.T) *admission.Queues {
	q, err := admission.New(admission.Config{Rule: admission.RuleTimeout, Capacity: 4, TenantCapacity: 2, MaxTenants: 2, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Push(0, "a", 3)
	q.Push(0, "b", 4)
	q.Push(0, "c", 5)
	q.Pop(admission.Millisecond)
	q.Push(admission.Millisecond, "b", 6)
	q.Remove(2*admission.Millisecond, 6)
	q.Sweep(admission.DefaultInterval)
	for {
		if _, ok := q.TakeShed(); !ok {
			break
		}
	}
	return q
}

func collect(t *testing.T, q *admission.Queues, detail bool) obs.InstanceSnapshot {
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: detail})
	if err := c.Register(admsrc.Source(q.Snapshot)); err != nil {
		t.Fatal(err)
	}
	s, err := c.Publish()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestCountersMoveAndConserveThroughTheMapping proves admission totals, per-tenant counters,
// histograms, and request conservation survive mapping through obs.Collector.
func TestCountersMoveAndConserveThroughTheMapping(t *testing.T) {
	q := busy(t)
	if err := q.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	s := collect(t, q, true)
	v := func(m string, l obs.Labels) int64 { return s.Value(obs.ComponentAdmission, m, l) }
	pushes, served, removed := v("pushes", obs.Labels{}), v("served", obs.Labels{}), v("removed", obs.Labels{})
	var shed int64
	for r := range admission.NumReasons {
		shed += v("shed", obs.Labels{Reason: r.String()})
	}
	if pushes == 0 || served == 0 || removed == 0 || shed == 0 || pushes != served+removed+shed+v("queued", obs.Labels{}) {
		t.Fatalf("pushes %d served %d removed %d shed %d", pushes, served, removed, shed)
	}
	if v("rule", obs.Labels{Mode: "timeout"}) != 1 || v("rule", obs.Labels{Mode: "folly"}) != 0 || v("capacity", obs.Labels{}) != 4 {
		t.Fatal("rule or capacity gauges")
	}
	if v("tenant_pushes", obs.Labels{Tenant: "a"}) != 3 || v("tenant_shed", obs.Labels{Tenant: "a", Reason: "overflow"}) != 1 {
		t.Fatal("per-tenant counters did not move")
	}
	if v("tenants_peak", obs.Labels{}) != 2 || v("shed", obs.Labels{Reason: "tenants"}) != 1 {
		t.Fatal("tenant cap not exported")
	}
	if v(admsrc.MetricShedding, obs.Labels{}) == 0 {
		t.Fatal("shedding gauge did not move")
	}
	found := false
	for _, h := range s.Hists {
		found = found || h.Metric == "tenant_queue_wait"
	}
	if !found {
		t.Fatal("no per-tenant histogram with detail on")
	}
}

// TestDetailOffExportsNoTenantRows proves no tenant_* metric or tenant label is emitted when obs.detail is false.
func TestDetailOffExportsNoTenantRows(t *testing.T) {
	s := collect(t, busy(t), false)
	for _, p := range s.Points {
		if p.Labels.Tenant != "" || len(p.Metric) > 7 && p.Metric[:7] == "tenant_" {
			t.Fatalf("tenant row with detail off: %+v", p)
		}
	}
}

// TestHealthIsDegradedWhileAQueueSheds proves the admission health rule reports degraded when shedding and ok when idle.
func TestHealthIsDegradedWhileAQueueSheds(t *testing.T) {
	rule := admsrc.HealthRules()[0]
	if st, _ := rule.Check(collect(t, busy(t), false)); st != obs.StateDegraded {
		t.Fatalf("shedding: %v", st)
	}
	q, _ := admission.New(admission.Config{})
	if st, _ := rule.Check(collect(t, q, false)); st != obs.StateOK {
		t.Fatalf("idle: %v", st)
	}
}
