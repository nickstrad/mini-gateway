package proxy

import (
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const PlantFairStatementInFlight Plant = 1 << 15

func (c Config) fairConfig() admission.FairConfig {
	f := c.Fairness
	if f.Limit == 0 {
		f.Limit = c.Connections
	}
	f.MaxLeases = c.Connections
	return f.WithDefaults()
}

func (c Config) FairConfig() admission.FairConfig { return c.WithDefaults().fairConfig() }

func (a *AdmissionQueue) installFair(cfg admission.FairConfig) error {
	f, err := admission.NewFair(cfg)
	if err != nil {
		return err
	}
	a.q.SetDispatcher(f)
	return nil
}

func (a *AdmissionQueue) LeaseStart(now Instant, tenant work.TenantID) bool {
	return a.q.LeaseStart(a.at(now), string(tenant))
}

func (a *AdmissionQueue) LeaseEnd(now Instant, tenant work.TenantID) bool {
	return a.q.LeaseEnd(a.at(now), string(tenant))
}

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

func (e *Engine) fairRedispatch() {
	q := e.fairCore()
	if q == nil || e.stopping || !q.Held() || q.Len() == 0 || e.seams.Pool.Idle() == 0 {
		return
	}
	e.dispatch()
}

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
			"The request waits in its tenant queue. A tenant below its share (limit x weight / active weight, at least 1) is lent before one at or above it, and the last free lease goes only to a tenant below its share", "queue.interval / queue.target", "admission.in_flight"},
		{"Tenants with leases in flight (fair dispatcher state)", "proxy memory", "at most the leases in flight; " + itoa(f.MaxLeases), "pool.connections",
			"Cannot fill: a state exists only while its tenant holds a lease and is removed when its last lease ends", "-", "admission.fair_states"},
		{"Tenant weights", "proxy memory", itoa(admission.MaxWeights) + " entries, each 0 to " + itoa(admission.MaxWeight), "fairness.weights",
			"Configuration refused at load, naming the key", "-", "-"},
	}
}

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

func (e *Engine) checkoutDemand() (int, bool) {
	q := e.fairCore()
	if q == nil || q.Limit() >= e.cfg.Connections {
		return 0, false
	}
	return min(q.Releasable(), max(0, q.Limit()-q.InFlight()-1)), true
}
