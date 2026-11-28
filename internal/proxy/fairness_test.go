package proxy_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

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

// TestFairAHeldTransactionCountsAsInFlightUntilItsLeaseEnds proves a tenant
// holding an open transaction keeps its in-flight count across statements until COMMIT.
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

// TestFairTheLastFreeConnectionGoesToTheTenantBelowItsShare proves a freed
// last connection goes to the below-share tenant rather than one holding a transaction at share.
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

// TestCanaryFairWithoutTheShareBoundLendsAHeldTenantPastItsShare proves
// disabling the share bound lets the transaction-holding tenant take the second connection.
func TestCanaryFairWithoutTheShareBoundLendsAHeldTenantPastItsShare(t *testing.T) {
	r, conn := shareBindingPremise(t, admission.FairPlantNoShareBound)
	a := r.db(conn, complete("DISCARD ALL", 'I'))
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, conn), query("SELECT 2")) || tenantInFlight(r, 0) != 2 {
		t.Fatalf("planted: freed connection went to %q, tenant 0 in flight %d", written(a, proxy.ActDBWrite, conn), tenantInFlight(r, 0))
	}
}

// TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions proves
// Engine.Invariants detects when dispatcher in-flight count drops a held transaction.
func TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions(t *testing.T) {
	r := fairRig(t, 2, func(c *proxy.Config) { c.Plant |= proxy.PlantFairStatementInFlight })
	id := r.lease(10, 0, "BEGIN")
	r.unchecked(proxy.EvDBData, id, complete("BEGIN", 'T'))
	err := r.e.Invariants()
	if err == nil || !strings.HasPrefix(err.Error(), "fairness:") {
		t.Fatalf("in flight that drops a held transaction passed Engine.Invariants: %v", err)
	}
}

// TestFairEveryWayALeaseEndsReturnsItsInFlight proves clean release, client
// vanish, idle-in-transaction, DB close, statement timeout, and shutdown all report lease end.
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

// TestFairTheFixedSetReportsEveryLeaseEndToo proves lease start and end
// reporting works identically on the FixedSet pool seam.
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

const fairIdleTimeout = 50 * time.Millisecond

func heldBackPremise(t *testing.T) (*coreRig, proxy.ConnID, proxy.ConnID) {
	r := fairRig(t, 4, func(c *proxy.Config) {
		c.Pool = &pool.Config{RatePerSec: 1000, NoJitter: true, IdleTimeout: fairIdleTimeout}
	})
	for _, client := range []proxy.ConnID{10, 11} {
		id := r.lease(client, 0, "BEGIN")
		r.db(id, complete("BEGIN", 'T'))
	}
	other := r.lease(12, 1, "SELECT pg_sleep(1)")
	spare := r.lease(13, 1, "SELECT 1")
	r.db(spare, complete("SELECT 1", 'I'))
	r.db(spare, complete("DISCARD ALL", 'I'))
	if p := r.pool(); p.Idle.Value != 1 || tenantInFlight(r, 0) != 2 || tenantInFlight(r, 1) != 1 {
		t.Fatalf("premise: idle %d, in flight %d / %d", p.Idle.Value, tenantInFlight(r, 0), tenantInFlight(r, 1))
	}
	r.login(14, 0)
	a := r.q(14, "SELECT 2")
	if bytes.Contains(written(a, proxy.ActDBWrite, spare), []byte("SELECT 2")) {
		t.Fatalf("tenant 0 at its share of 2 took the last lease while tenant 1 holds 1 of its 2: %q", written(a, proxy.ActDBWrite, spare))
	}
	if _, as := r.agree(); as.Queued.Value != 1 || as.Fair.HeldShare == 0 {
		t.Fatalf("premise: queued %d, held at a share %d", as.Queued.Value, as.Fair.HeldShare)
	}
	return r, other, spare
}

// TestFairAHeldBackRequestCostsNoPopNoUnpopAndNoDial proves holding a request
// back across events triggers no pop, unpop, checkout, or dial and at most one selection per event.
func TestFairAHeldBackRequestCostsNoPopNoUnpopAndNoDial(t *testing.T) {
	r, _, _ := heldBackPremise(t)
	_, before := r.agree()
	dials, checkouts := len(r.dbs), r.pool().Checkouts
	for range 4 {
		r.tick(10 * time.Millisecond)
	}
	_, after := r.agree()
	if after.Unpops != before.Unpops || after.Served != before.Served || len(r.dbs) != dials || r.pool().Checkouts != checkouts {
		t.Fatalf("held back over 4 events: unpops %d->%d, served %d->%d, dials %d->%d, checkouts %d->%d",
			before.Unpops, after.Unpops, before.Served, after.Served, dials, len(r.dbs), checkouts, r.pool().Checkouts)
	}
	if after.Fair.HeldShare-before.Fair.HeldShare > 4 {
		t.Fatalf("more than one selection per event: %d", after.Fair.HeldShare-before.Fair.HeldShare)
	}
}

// TestFairAHeldBackRequestCausesNoDialAfterTheIdleConnectionCloses proves an
// idle-closed spare connection is never redialled while only a held-back request waits.
func TestFairAHeldBackRequestCausesNoDialAfterTheIdleConnectionCloses(t *testing.T) {
	r, _, _ := heldBackPremise(t)
	dials := len(r.dbs)
	closed := r.pool().Closes[pool.CloseIdle]
	for range 40 {
		r.tick(fairIdleTimeout / 2)
	}
	p := r.pool()
	if p.Closes[pool.CloseIdle]-closed != 1 || p.Idle.Value != 0 {
		t.Fatalf("premise: the spare connection closes once at the idle timeout: closed idle %d, idle %d", p.Closes[pool.CloseIdle]-closed, p.Idle.Value)
	}
	if len(r.dbs) != dials {
		t.Fatalf("%d replacement dials over 20 idle timeouts while one request stayed held back", len(r.dbs)-dials)
	}
	if _, as := r.agree(); as.Queued.Value != 1 {
		t.Fatalf("the held-back request left the queue: queued %d", as.Queued.Value)
	}
}

// TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible proves
// fairRedispatch immediately lends a held-back request once the blocking tenant's lease ends.
func TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible(t *testing.T) {
	r, other, spare := heldBackPremise(t)
	a := r.run(r.ev(proxy.EvDBClosed, other, nil, 0))
	if bytes.Equal(written(a, proxy.ActDBWrite, spare), query("")) {
		a = r.db(spare, emptyReply())
	}
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, spare), query("SELECT 2")) {
		t.Fatalf("tenant 1's lease was lost: tenant 0 is alone and must get the idle connection at once, wrote %q", written(a, proxy.ActDBWrite, spare))
	}
	if tenantInFlight(r, 0) != 3 {
		t.Fatalf("tenant 0 in flight %d", tenantInFlight(r, 0))
	}
}

// TestFairAHeldBackRequestIsDialledForWhenItBecomesEligibleAfterTheIdleClose
// proves the pool dials a connection as soon as a previously held-back request becomes releasable.
func TestFairAHeldBackRequestIsDialledForWhenItBecomesEligibleAfterTheIdleClose(t *testing.T) {
	r, other, _ := heldBackPremise(t)
	for range 4 {
		r.tick(fairIdleTimeout)
	}
	if r.pool().Idle.Value != 0 {
		t.Fatal("premise: the spare connection closed")
	}
	dials := len(r.dbs)
	r.run(r.ev(proxy.EvDBClosed, other, nil, 0))
	r.tick(time.Millisecond)
	if len(r.dbs) != dials+1 {
		t.Fatalf("tenant 1's lease was lost: the request is releasable and one connection must be dialled, got %d dials", len(r.dbs)-dials)
	}
	fresh := r.dbs[len(r.dbs)-1]
	a := r.open(fresh)
	for range 10 {
		r.tick(10 * time.Millisecond)
	}
	if p := r.pool(); p.Open.Value+p.Connecting.Value > 4 {
		t.Fatalf("open %d, connecting %d: past pool.connections", p.Open.Value, p.Connecting.Value)
	}
	if !bytes.HasSuffix(written(a, proxy.ActDBWrite, fresh), query("SELECT 2")) {
		t.Fatalf("the dialled connection was not lent to the waiting request: %q (in flight %d / %d, queued %d)", written(a, proxy.ActDBWrite, fresh),
			tenantInFlight(r, 0), tenantInFlight(r, 1), r.e.AdmissionSnapshot().Queued.Value)
	}
}

// TestFairNoDialPastTheFairLimitWhileConnectionsOpen proves checkout demand
// refresh prevents opening a third connection when the fair limit is below pool.connections.
func TestFairNoDialPastTheFairLimitWhileConnectionsOpen(t *testing.T) {
	r := fairRig(t, 4, func(c *proxy.Config) { c.Fairness.Limit = 2 })
	for i := range proxy.ConnID(4) {
		r.login(10+i, int(i%2))
		r.q(10+i, "SELECT pg_sleep(1)")
	}
	r.tick(time.Millisecond)
	if len(r.dbs) != 2 {
		t.Fatalf("fair limit 2, four waiting: %d dials, want 2", len(r.dbs))
	}
	r.tick(2 * time.Millisecond)
	for _, id := range append([]proxy.ConnID(nil), r.dbs...) {
		r.open(id)
	}
	for range 20 {
		r.tick(10 * time.Millisecond)
	}
	if len(r.dbs) != 2 {
		t.Fatalf("fair limit 2: %d dials after both opened and were lent, want 2", len(r.dbs))
	}
	if _, as := r.agree(); as.Fair.InFlight.Value != 2 || as.Queued.Value != 2 {
		t.Fatalf("in flight %d, queued %d", as.Fair.InFlight.Value, as.Queued.Value)
	}
}

// TestFairNoDialPastTheFairLimit proves waiting requests held at limit < pool.connections
// never dial additional backend connections.
func TestFairNoDialPastTheFairLimit(t *testing.T) {
	r := fairRig(t, 4, func(c *proxy.Config) { c.Fairness.Limit = 1 })
	r.lease(10, 0, "SELECT pg_sleep(1)")
	for i := range proxy.ConnID(4) {
		r.login(11+i, int(i%2))
		r.q(11+i, "SELECT 2")
	}
	for range 20 {
		r.tick(10 * time.Millisecond)
	}
	if len(r.dbs) != 1 {
		t.Fatalf("fair limit 1 below pool.connections 4: %d dials, want 1", len(r.dbs))
	}
	if _, as := r.agree(); as.Fair.InFlight.Value != 1 || as.Queued.Value != 4 {
		t.Fatalf("in flight %d, queued %d", as.Fair.InFlight.Value, as.Queued.Value)
	}
}

// TestFairTheBinaryLimitIsThePoolSize proves FairConfig defaults Limit and
// MaxLeases to Connections and proxy.New refuses invalid fairness weights.
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

// TestFairOneTenantSeesNoChangeFromTheFairDispatcher proves a lone active
// tenant uses the full pool size without restriction.
func TestFairOneTenantSeesNoChangeFromTheFairDispatcher(t *testing.T) {
	r := fairRig(t, 2, func(c *proxy.Config) { c.Pool = &pool.Config{RatePerSec: 1000, NoJitter: true} })
	a := r.lease(10, 0, "SELECT 1")
	b := r.lease(11, 0, "SELECT 2")
	if a == b || tenantInFlight(r, 0) != 2 {
		t.Fatalf("a lone tenant must reach the pool size: in flight %d", tenantInFlight(r, 0))
	}
	_ = pgwire.StatusIdle
}
