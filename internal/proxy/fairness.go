package proxy

import (
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// PlantFairStatementInFlight is a test-only fault bit that drops a tenant's
// in-flight lease count between statements of an open transaction.
const PlantFairStatementInFlight Plant = 1 << 15

func (c Config) fairConfig() admission.FairConfig {
	f := c.Fairness
	if f.Limit == 0 {
		f.Limit = c.Connections
	}
	f.MaxLeases = c.Connections
	return f.WithDefaults()
}

// FairConfig returns the effective admission.FairConfig derived from c with
// defaults applied, Limit defaulting to Connections, and MaxLeases bound to Connections.
func (c Config) FairConfig() admission.FairConfig { return c.WithDefaults().fairConfig() }

func (a *AdmissionQueue) installFair(cfg admission.FairConfig) error {
	f, err := admission.NewFair(cfg)
	if err != nil {
		return err
	}
	a.q.SetDispatcher(f)
	return nil
}

// LeaseStart records that tenant began holding one database lease at now.
func (a *AdmissionQueue) LeaseStart(now Instant, tenant work.TenantID) bool {
	return a.q.LeaseStart(a.at(now), string(tenant))
}

// LeaseEnd records that one database lease held by tenant ended at now.
func (a *AdmissionQueue) LeaseEnd(now Instant, tenant work.TenantID) bool {
	return a.q.LeaseEnd(a.at(now), string(tenant))
}

// SetLimit updates the fair dispatcher's concurrency limit at now (1..admission.MaxFairLimit).
func (a *AdmissionQueue) SetLimit(now Instant, n int) bool { return a.q.SetLimit(a.at(now), n) }

func (e *Engine) fairCore() *admission.Queues {
	if q := e.admissionCore(); q != nil && q.Fair() != nil {
		return q
	}
	return nil
}

func (e *Engine) fairStarted(t work.TenantID) {
	if q := e.fairCore(); q != nil {
		q.LeaseStart(admission.Time(e.now), string(t))
	}
}

func (e *Engine) fairEnded(t work.TenantID) {
	if q := e.fairCore(); q != nil {
		q.LeaseEnd(admission.Time(e.now), string(t))
	}
}

func (e *Engine) fairStatementDone(c *client, inTx bool) {
	if e.cfg.Plant&PlantFairStatementInFlight != 0 && inTx {
		e.fairEnded(c.tenant)
	}
}

func (e *Engine) fairStatementStart(c *client) {
	if e.cfg.Plant&PlantFairStatementInFlight != 0 {
		e.fairStarted(c.tenant)
	}
}

// fairRedispatch retries dispatch once per event while the dispatcher is
// holding a request back, so a lease end or limit change wakes waiting work
// without polling timers.
func (e *Engine) fairRedispatch() {
	q := e.fairCore()
	if q == nil || e.stopping || !q.Held() || q.Len() == 0 || e.seams.Pool.Idle() == 0 {
		return
	}
	e.dispatch()
}

// fairnessCheck verifies that the fair dispatcher's total and per-tenant
// in-flight lease counts match the engine's own lent database connections.
func (e *Engine) fairnessCheck() error {
	q := e.fairCore()
	if q == nil {
		return nil
	}
	leases := e.snap.LeasesStarted - e.snap.LeasesEnded
	if int64(q.InFlight()) != leases {
		return fmt.Errorf("fairness: %d in flight across tenants, the engine has %d leases outstanding", q.InFlight(), leases)
	}
	var lent []*dbConn
	for _, set := range [][]*dbConn{e.slots, e.coreConns} {
		for _, d := range set {
			if d != nil && d.phase == dpLent && d.client != nil {
				lent = append(lent, d)
			}
		}
	}
	tenants := 0
	// Deduplicate tenants deterministically without iterating a map.
	for i, d := range lent {
		n, first := 0, true
		for j, o := range lent {
			if o.lastTenant == d.lastTenant {
				n++
				first = first && j >= i
			}
		}
		if !first {
			continue
		}
		tenants++
		if got := q.TenantInFlight(string(d.lastTenant)); got != n {
			return fmt.Errorf("fairness: tenant %q has %d in flight, the engine lends it %d connections", d.lastTenant, got, n)
		}
	}
	if n := q.Fair().States(); n != tenants {
		return fmt.Errorf("fairness: %d tenants with leases, the engine lends to %d", n, tenants)
	}
	return nil
}

func fairnessCapacityRows(c Config) []CapacityRow {
	f := c.fairConfig()
	return []CapacityRow{
		{"Leases in flight across tenants (the fair limit; held transactions count)", "the database's concurrency; each tenant's share of it",
			itoa(f.Limit) + ", the value of pool.connections", "pool.connections",
			"The request waits in its tenant queue. A tenant below its share (limit x weight / active weight, at least 1) is lent before one at or above it, " +
				"and the last free lease is held back only while an active tenant is below its share. " +
				"The limit counts leases, not answers: with coalesce.key_scope all-tenants a follower of another tenant rides a flight on the owner's lease and spends none of its own share, " +
				"and when the owner leaves, a follower of another tenant takes the flight over and keeps the read (and a retry of it) running on the leaving tenant's share until the lease ends. " +
				"coalesce.key_scope tenant keeps every flight inside one tenant (strict isolation)", "queue.interval / queue.target", "admission.in_flight"},
		{"Tenants with leases in flight (fair dispatcher state)", "proxy memory", "at most the leases in flight; " + itoa(f.MaxLeases), "pool.connections",
			"Cannot fill: a state exists only while its tenant holds a lease and is removed when its last lease ends", "-", "admission.fair_states"},
		{"Tenant weights", "proxy memory", itoa(admission.MaxWeights) + " entries, each 0 to " + itoa(admission.MaxWeight), "fairness.weights",
			"Configuration refused at load, naming the key", "-", "-"},
	}
}

// TenantLeases returns the number of database connections currently lent to t.
func (e *Engine) TenantLeases(t work.TenantID) int {
	n := 0
	for _, set := range [][]*dbConn{e.slots, e.coreConns} {
		for _, d := range set {
			if d != nil && d.phase == dpLent && d.client != nil && d.lastTenant == t {
				n++
			}
		}
	}
	return n
}

// TenantQueued returns the number of requests currently waiting in t's admission queue.
func (e *Engine) TenantQueued(t work.TenantID) int {
	if q := e.admissionCore(); q != nil {
		return q.Depth(string(t))
	}
	return 0
}

func (e *Engine) releasable() int {
	if q := e.fairCore(); q != nil {
		return q.Releasable()
	}
	return e.seams.Queue.Len()
}

// checkoutDemand returns the post-checkout demand when the fair limit sits
// below pool.connections so Pool.Checkout does not dial past the fair limit.
// At Limit == Connections it returns false to leave pool checkout dialing unchanged.
func (e *Engine) checkoutDemand() (int, bool) {
	q := e.fairCore()
	if q == nil || q.Limit() >= e.cfg.Connections {
		return 0, false
	}
	return min(q.Releasable(), max(0, q.Limit()-q.InFlight()-1)), true
}
