package proxy_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

func fairRig(t *testing.T, conns int, edit func(*proxy.Config)) *coreRig {
	t.Helper()
	return joinRig(t, quietRule, func(c *proxy.Config) {
		c.Connections = conns
		c.QueueCap = 16
		if edit != nil {
			edit(c)
		}
	})
}

func tenantInFlight(r *coreRig, tenant int) int64 {
	for _, row := range r.e.AdmissionSnapshot().PerTenant {
		if row.Tenant == proxysim.TenantName(tenant) {
			return row.InFlight
		}
	}
	return 0
}

func (r *coreRig) inFlight() int64 {
	r.t.Helper()
	_, as := r.agree()
	return as.Fair.InFlight.Value
}

func (r *coreRig) lease(client proxy.ConnID, tenant int, sql string) proxy.ConnID {
	r.t.Helper()
	r.login(client, tenant)
	before := len(r.dbs)
	a := r.q(client, sql)
	if len(r.dbs) == before && r.e.AdmissionSnapshot().Queued.Value > 0 {
		a = append(a, r.tick(1000*1000)...)
	}
	if len(r.dbs) > before {
		a = append(a, r.open(r.dbs[len(r.dbs)-1])...)
	}
	for _, id := range r.dbs {
		if bytes.HasSuffix(written(a, proxy.ActDBWrite, id), query(sql)) {
			return id
		}
	}
	r.t.Fatalf("%q was not forwarded", sql)
	return 0
}

func TestFairAHeldTransactionCountsAsInFlightUntilItsLeaseEnds(t *testing.T) {
	r := fairRig(t, 2, nil)
	id := r.lease(10, 0, "BEGIN")
	r.db(id, complete("BEGIN", 'T'))
	if r.inFlight() != 1 || tenantInFlight(r, 0) != 1 {
		t.Fatalf("between statements of a transaction: in flight %d, tenant %d", r.inFlight(), tenantInFlight(r, 0))
	}
	r.q(10, "SELECT 1")
	r.db(id, complete("SELECT 1", 'T'))
	if r.inFlight() != 1 {
		t.Fatalf("after a statement inside the transaction: in flight %d", r.inFlight())
	}
	r.q(10, "COMMIT")
	r.db(id, complete("COMMIT", 'I'))
	if r.inFlight() != 0 || r.e.AdmissionSnapshot().Fair.States.Value != 0 {
		t.Fatalf("after COMMIT: in flight %d, states %d", r.inFlight(), r.e.AdmissionSnapshot().Fair.States.Value)
	}
}

func shareBindingPremise(t *testing.T, plant admission.FairPlant) (*coreRig, proxy.ConnID) {
	r := fairRig(t, 2, func(c *proxy.Config) { c.Fairness.Plant = plant })
	held := r.lease(10, 0, "BEGIN")
	r.db(held, complete("BEGIN", 'T'))
	other := r.lease(11, 1, "SELECT 1")
	r.login(12, 0)
	r.q(12, "SELECT 2")
	r.login(13, 1)
	r.q(13, "SELECT 3")
	if _, as := r.agree(); as.Queued.Value != 2 || as.Fair.InFlight.Value != 2 {
		t.Fatalf("premise: queued %d, in flight %d", as.Queued.Value, as.Fair.InFlight.Value)
	}
	r.db(other, complete("SELECT 1", 'I'))
	return r, other
}

func TestFairTheLastFreeConnectionGoesToTheTenantBelowItsShare(t *testing.T) {
	r, conn := shareBindingPremise(t, 0)
	a := r.db(conn, complete("DISCARD ALL", 'I'))
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, conn), query("SELECT 3")) {
		t.Fatalf("the freed connection went to %q; tenant 0 holds a transaction at its share of 1", written(a, proxy.ActDBWrite, conn))
	}
	if _, as := r.agree(); as.Queued.Value != 1 || tenantInFlight(r, 0) != 1 || tenantInFlight(r, 1) != 1 {
		t.Fatalf("queued %d, in flight %d / %d", as.Queued.Value, tenantInFlight(r, 0), tenantInFlight(r, 1))
	}
}

func TestCanaryFairWithoutTheShareBoundLendsAHeldTenantPastItsShare(t *testing.T) {
	r, conn := shareBindingPremise(t, admission.FairPlantNoShareBound)
	a := r.db(conn, complete("DISCARD ALL", 'I'))
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, conn), query("SELECT 2")) || tenantInFlight(r, 0) != 2 {
		t.Fatalf("planted: freed connection went to %q, tenant 0 in flight %d", written(a, proxy.ActDBWrite, conn), tenantInFlight(r, 0))
	}
}

func TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions(t *testing.T) {
	r := fairRig(t, 2, func(c *proxy.Config) { c.Plant |= proxy.PlantFairStatementInFlight })
	id := r.lease(10, 0, "BEGIN")
	r.unchecked(proxy.EvDBData, id, complete("BEGIN", 'T'))
	err := r.e.Invariants()
	if err == nil || !strings.HasPrefix(err.Error(), "fairness:") {
		t.Fatalf("in flight that drops a held transaction passed Engine.Invariants: %v", err)
	}
}

func TestFairEveryWayALeaseEndsReturnsItsInFlight(t *testing.T) {
	cases := map[string]func(r *coreRig, id proxy.ConnID){
		"release": func(r *coreRig, id proxy.ConnID) {
			r.db(id, complete("SELECT 1", 'I'))
		},
		"vanished-mid-statement": func(r *coreRig, id proxy.ConnID) {
			r.run(r.ev(proxy.EvClientClosed, 10, nil, 0))
		},
		"vanished-in-transaction": func(r *coreRig, id proxy.ConnID) {
			r.db(id, complete("SELECT 1", 'T'))
			r.run(r.ev(proxy.EvClientClosed, 10, nil, 0))
		},
		"idle-in-transaction": func(r *coreRig, id proxy.ConnID) {
			r.db(id, complete("SELECT 1", 'T'))
			r.tick(proxy.DefaultIdleInTx)
		},
		"connection-lost": func(r *coreRig, id proxy.ConnID) {
			r.run(r.ev(proxy.EvDBClosed, id, nil, 0))
		},
		"statement-deadline": func(r *coreRig, id proxy.ConnID) {
			r.tick(proxy.DefaultTimeouts().Statement)
		},
		"shutdown": func(r *coreRig, id proxy.ConnID) {
			r.run(r.e.Shutdown(r.now, nil))
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			r := fairRig(t, 2, nil)
			id := r.lease(10, 0, "SELECT 1")
			if r.inFlight() != 1 {
				t.Fatal("premise: one lease in flight")
			}
			end(r, id)
			s, as := r.agree()
			if as.Fair.InFlight.Value != 0 || as.Fair.States.Value != 0 || s.LeasesStarted != s.LeasesEnded {
				t.Fatalf("after %s: in flight %d, states %d, leases %d/%d", name, as.Fair.InFlight.Value, as.Fair.States.Value, s.LeasesStarted, s.LeasesEnded)
			}
			if as.Fair.LeaseStarts != 1 || as.Fair.LeaseEnds != 1 {
				t.Fatalf("after %s: lease starts %d ends %d", name, as.Fair.LeaseStarts, as.Fair.LeaseEnds)
			}
		})
	}
}

func TestFairTheFixedSetReportsEveryLeaseEndToo(t *testing.T) {
	r := admissionRig(t, 4, admission.Config{Rule: admission.RuleTimeout, Interval: 10 * admission.Second, Target: admission.Second, LIFO: admission.LIFOOff})
	r.client(10)
	r.query(10, "SELECT 1")
	r.ev(proxy.EvDBData, 1, complete("SELECT 1", 'I'), 0)
	r.ev(proxy.EvDBData, 1, complete("DISCARD ALL", 'I'), 0)
	r.client(11)
	r.query(11, "SELECT 2")
	r.ev(proxy.EvDBClosed, 1, nil, 0)
	s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
	if as.Fair.LeaseStarts != s.LeasesStarted || as.Fair.LeaseEnds != s.LeasesEnded || as.Fair.InFlight.Value != 0 || s.LeasesStarted < 2 {
		t.Fatalf("fixed set: fair %d/%d, engine %d/%d", as.Fair.LeaseStarts, as.Fair.LeaseEnds, s.LeasesStarted, s.LeasesEnded)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}

func heldBackPremise(t *testing.T) (*coreRig, proxy.ConnID) {
	r := fairRig(t, 3, nil)
	held := r.lease(10, 0, "BEGIN")
	r.db(held, complete("BEGIN", 'T'))
	other := r.lease(11, 1, "SELECT pg_sleep(1)")
	r.login(12, 0)
	before := len(r.dbs)
	r.q(12, "SELECT 2")
	if len(r.dbs) == before {
		r.tick(1000 * 1000)
	}
	if len(r.dbs) != before+1 {
		t.Fatal("premise: a dial for the waiting request")
	}
	idle := r.dbs[len(r.dbs)-1]
	a := r.open(idle)
	if bytes.Contains(written(a, proxy.ActDBWrite, idle), []byte("SELECT 2")) {
		t.Fatalf("tenant 0 at its share was lent the third connection: %q", written(a, proxy.ActDBWrite, idle))
	}
	if p := r.pool(); p.Idle.Value != 1 {
		t.Fatalf("premise: idle %d", p.Idle.Value)
	}
	return r, other
}

func TestFairAHeldBackRequestCostsNoPopNoUnpopAndNoDial(t *testing.T) {
	r, _ := heldBackPremise(t)
	_, before := r.agree()
	dials, checkouts := len(r.dbs), r.pool().Checkouts
	for range 50 {
		r.tick(10 * 1000 * 1000)
	}
	_, after := r.agree()
	if after.Unpops != before.Unpops || after.Served != before.Served || len(r.dbs) != dials || r.pool().Checkouts != checkouts {
		t.Fatalf("held back over 50 events: unpops %d->%d, served %d->%d, dials %d->%d, checkouts %d->%d",
			before.Unpops, after.Unpops, before.Served, after.Served, dials, len(r.dbs), checkouts, r.pool().Checkouts)
	}
	if after.Fair.HeldShare-before.Fair.HeldShare > 50 {
		t.Fatalf("more than one selection per event: %d", after.Fair.HeldShare-before.Fair.HeldShare)
	}
}

func TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible(t *testing.T) {
	r, other := heldBackPremise(t)
	idle := r.dbs[len(r.dbs)-1]
	a := r.run(r.ev(proxy.EvDBClosed, other, nil, 0))
	if bytes.Equal(written(a, proxy.ActDBWrite, idle), query("")) {
		a = r.db(idle, emptyReply())
	}
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, idle), query("SELECT 2")) {
		t.Fatalf("tenant 1's lease was lost: tenant 0 is alone and must get the idle connection at once, wrote %q", written(a, proxy.ActDBWrite, idle))
	}
	if tenantInFlight(r, 0) != 2 {
		t.Fatalf("tenant 0 in flight %d", tenantInFlight(r, 0))
	}
}

func TestFairTheBinaryLimitIsThePoolSize(t *testing.T) {
	c := proxy.Config{Connections: 7}
	if f := c.FairConfig(); f.Limit != 7 || f.MaxLeases != 7 || f.DefaultWeight != admission.DefaultWeight {
		t.Fatalf("fair config %+v", f)
	}
	c.Fairness.Limit = 3
	if f := c.FairConfig(); f.Limit != 3 || f.MaxLeases != 7 {
		t.Fatalf("fair config %+v", f)
	}
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 1, Tenants: 2})
	if err != nil {
		t.Fatal(err)
	}
	c = w.Proxy.Engine.Config()
	c.Admission = admission.Config{Rule: admission.RuleTimeout}
	c.Fairness = admission.FairConfig{DefaultWeight: -1}
	if _, err := proxy.New(c); err == nil || !strings.Contains(err.Error(), "fairness.default_weight") {
		t.Fatalf("a bad weight built an engine: %v", err)
	}
}

func TestFairOneTenantSeesNoChangeFromTheFairDispatcher(t *testing.T) {
	r := fairRig(t, 2, func(c *proxy.Config) { c.Pool = &pool.Config{RatePerSec: 1000, NoJitter: true} })
	a := r.lease(10, 0, "SELECT 1")
	b := r.lease(11, 0, "SELECT 2")
	if a == b || tenantInFlight(r, 0) != 2 {
		t.Fatalf("a lone tenant must reach the pool size: in flight %d", tenantInFlight(r, 0))
	}
	_ = pgwire.StatusIdle
}
