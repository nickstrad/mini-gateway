package poolsrc

import (
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pool"
)

const (
	lo = obs.LabelOutcome
	lr = obs.LabelReason
	lm = obs.LabelMode

	MetricBreaker = "breaker_state"
	CheckBreaker  = "breaker"
)

var none = obs.Labels{}

func Metrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("capacity", 0, "The cap on open plus connecting database connections (pool.connections, the fleet share)."),
		obs.GaugeDesc("max_connecting", 0, "The cap on connections being opened at once."),
		obs.GaugeDesc("creation_rate_per_s", 0, "The creation-rate limit: dials per second, at most one per 1/rate."),
		obs.GaugeDesc("max_cancels", 0, "The cap on cancel requests in flight."),
		obs.GaugeDesc("open", 0, "Database connections open now (idle, checking, lent, cleaning, resetting)."),
		obs.HighWaterDesc("open_peak", 0, "Most database connections open at once."),
		obs.GaugeDesc("connecting", 0, "Database connections being opened now."),
		obs.HighWaterDesc("connecting_peak", 0, "Most connections being opened at once."),
		obs.GaugeDesc("open_plus_connecting", 0, "Open plus connecting now; never above capacity."),
		obs.HighWaterDesc("open_plus_connecting_peak", 0, "Most open plus connecting at once."),
		obs.GaugeDesc("idle", 0, "Idle connections now."),
		obs.GaugeDesc("lent", 0, "Lent connections now."),
		obs.HighWaterDesc("lent_peak", 0, "Most lent connections at once."),
		obs.GaugeDesc("checking", 0, "Connections being checked before reuse now."),
		obs.GaugeDesc("cleaning", 0, "Connections in the dirty-connection table now (cancel, drain, rollback, waiting for a cancel)."),
		obs.GaugeDesc("resetting", 0, "Connections being reset now."),
		obs.GaugeDesc("suspect", 0, "Connections that must pass a check before their next lend."),
		obs.HighWaterDesc("idle_peak", 0, "Most idle connections at once."),
		obs.HighWaterDesc("checking_peak", 0, "Most connections being checked at once."),
		obs.HighWaterDesc("cleaning_peak", 0, "Most connections in the dirty-connection table at once."),
		obs.HighWaterDesc("resetting_peak", 0, "Most connections being reset at once."),
		obs.HighWaterDesc("suspect_peak", 0, "Most suspect connections at once."),
		obs.CounterDesc("checkout_attempts", 0, "Checkouts asked for; each ends lent or refused."),
		obs.CounterDesc("dials", 0, "Dials started.").AsHeadline(),
		obs.CounterDesc("dials_opened", 0, "Dials that finished startup."),
		obs.CounterDesc("dial_failures", 0, "Dials that failed."),
		obs.CounterDesc("rate_limited", 0, "Times a wanted dial waited for the creation rate."),
		obs.CounterDesc("closes", lr, "Connections closed, by reason."),
		obs.CounterDesc("checkouts", lo, "Checkouts by outcome (lent, refused)."),
		obs.CounterDesc("exhausted", 0, "Checkouts refused with open plus connecting at the cap."),
		obs.CounterDesc("lent_as_is", 0, "Lends of a connection last held by the same tenant, with no reset (on-switch)."),
		obs.CounterDesc("tenant_switches", 0, "Resets started at checkout because the connection was last held by another tenant (on-switch)."),
		obs.CounterDesc("lease_ends", lr, "Leases that ended, by how (reason)."),
		obs.CounterDesc("dirty_reused", lr, "Dirty-table entries that ended in reuse after a completed clean-up, by how the lease ended."),
		obs.CounterDesc("dirty_closed", lr, "Dirty-table entries that ended in a close, by how the lease ended."),
		obs.CounterDesc("rollbacks", 0, "ROLLBACK messages sent by the dirty-connection table."),
		obs.CounterDesc("resets", lm, "Resets started, by policy (mode: always, on-switch, cleanup)."),
		obs.CounterDesc("resets_ok", lm, "Resets completed, by policy (mode)."),
		obs.CounterDesc("resets_failed", lm, "Resets that failed, by policy (mode)."),
		obs.CounterDesc("reseeds", 0, "Re-seeds sent with a reset (lease.reseed on)."),
		obs.CounterDesc("checks", 0, "Checks before reuse started."),
		obs.CounterDesc("checks_by_result", lo, "Checks by result (ok, timeout, error, unexpected)."),
		obs.CounterDesc("cancels", 0, "Cancel requests sent."),
		obs.CounterDesc("client_cancels", 0, "Cancel requests asked for by clients for their lent connection."),
		obs.CounterDesc("cancels_done", 0, "Cancel requests finished."),
		obs.CounterDesc("cancels_expired", 0, "Cancel requests dropped at their deadline or at shutdown."),
		obs.CounterDesc("cancels_refused", 0, "Cancel requests refused (cap reached, or no lent connection)."),
		obs.GaugeDesc("cancels_in_flight", 0, "Cancel requests in flight now."),
		obs.HighWaterDesc("cancels_in_flight_peak", 0, "Most cancel requests in flight at once."),
		obs.CounterDesc("backend_no_reply", 0, "Statements with zero reply bytes at the statement deadline."),
		obs.GaugeDesc(MetricBreaker, 0, "Breaker state: 0 closed, 1 open, 2 half-open."),
		obs.CounterDesc("breaker_transitions", lm, "Breaker transitions (mode)."),
		obs.CounterDesc("breaker_trials", 0, "Trials sent while half-open."),
		obs.CounterDesc("breaker_failures", 0, "Connection-class failures seen by the breaker."),
		obs.HistogramDesc("lease_time", 0, obs.UnitNs, "Time from lend to the end of the lease."),
		obs.HistogramDesc("idle_age_at_lend", 0, obs.UnitNs, "How long a connection had been idle when it was lent."),
	}
}

func Map(s pool.Snapshot, e *obs.Emitter) {
	gauges := []struct {
		name string
		v    int64
	}{{"capacity", s.Cap}, {"max_connecting", s.MaxConnecting}, {"creation_rate_per_s", s.RatePerSec}, {"max_cancels", s.MaxCancels},
		{"open", s.Open.Value}, {"open_peak", s.Open.Peak}, {"connecting", s.Connecting.Value}, {"connecting_peak", s.Connecting.Peak},
		{"open_plus_connecting", s.OpenPlusConnecting.Value}, {"open_plus_connecting_peak", s.OpenPlusConnecting.Peak},
		{"idle", s.Idle.Value}, {"lent", s.Lent.Value}, {"lent_peak", s.Lent.Peak}, {"checking", s.Checking.Value},
		{"cleaning", s.Cleaning.Value}, {"resetting", s.Resetting.Value}, {"suspect", s.Suspect.Value},
		{"idle_peak", s.Idle.Peak}, {"checking_peak", s.Checking.Peak}, {"cleaning_peak", s.Cleaning.Peak},
		{"resetting_peak", s.Resetting.Peak}, {"suspect_peak", s.Suspect.Peak},
		{"cancels_in_flight", s.CancelsInFlight.Value}, {"cancels_in_flight_peak", s.CancelsInFlight.Peak}, {MetricBreaker, s.Breaker}}
	for _, g := range gauges {
		e.Gauge(g.name, none, g.v)
	}
	counters := []struct {
		name string
		v    int64
	}{{"checkout_attempts", s.Checkouts}, {"dials", s.Dials}, {"dials_opened", s.DialsOK}, {"dial_failures", s.DialFailures}, {"rate_limited", s.RateLimited},
		{"exhausted", s.Exhausted}, {"lent_as_is", s.LentAsIs}, {"tenant_switches", s.TenantSwitches}, {"rollbacks", s.Rollbacks},
		{"reseeds", s.Reseeds}, {"checks", s.Checks}, {"cancels", s.Cancels}, {"client_cancels", s.ClientCancels},
		{"cancels_done", s.CancelsDone}, {"cancels_expired", s.CancelsExpired}, {"cancels_refused", s.CancelsRefused},
		{"backend_no_reply", s.NoReply}, {"breaker_trials", s.BreakerTrials}, {"breaker_failures", s.BreakerFailures}}
	for _, c := range counters {
		e.Counter(c.name, none, c.v)
	}
	for i, v := range s.Closes {
		e.Counter("closes", obs.Labels{Reason: pool.CloseReason(i).String()}, v)
	}
	e.Counter("checkouts", obs.Labels{Outcome: "lent"}, s.CheckoutsLent)
	e.Counter("checkouts", obs.Labels{Outcome: "refused"}, s.CheckoutsRefused)
	for i := range s.Ends {
		k := pool.EndKind(i).String()
		e.Counter("lease_ends", obs.Labels{Reason: k}, s.Ends[i])
		e.Counter("dirty_reused", obs.Labels{Reason: k}, s.DirtyReused[i])
		e.Counter("dirty_closed", obs.Labels{Reason: k}, s.DirtyClosed[i])
	}
	for i := range s.Resets {
		m := obs.Labels{Mode: pool.ResetCause(i).String()}
		e.Counter("resets", m, s.Resets[i])
		e.Counter("resets_ok", m, s.ResetsOK[i])
		e.Counter("resets_failed", m, s.ResetsFailed[i])
	}
	for i, v := range s.ChecksByResult {
		e.Counter("checks_by_result", obs.Labels{Outcome: pool.CheckResult(i).String()}, v)
	}
	for i, v := range s.BreakerTransitions {
		e.Counter("breaker_transitions", obs.Labels{Mode: pool.Transition(i).String()}, v)
	}
	if e.Detail() {
		e.Histogram("lease_time", none, s.LeaseTime)
		e.Histogram("idle_age_at_lend", none, s.IdleAgeAtLend)
	}
}

func Source(snap func() pool.Snapshot) obs.Source {
	return obs.NewSource(obs.ComponentPool, Metrics(), snap, Map)
}

func HealthRules() []obs.Rule {
	return []obs.Rule{{Name: CheckBreaker, Check: func(s obs.InstanceSnapshot) (obs.State, string) {
		switch s.Value(obs.ComponentPool, MetricBreaker, none) {
		case int64(pool.BreakerOpen):
			return obs.StateDegraded, "breaker open: requests answered 53M03 with no dial"
		case int64(pool.BreakerHalfOpen):
			return obs.StateDegraded, "breaker half-open: one trial connection decides"
		}
		return obs.StateOK, "breaker closed"
	}}}
}
