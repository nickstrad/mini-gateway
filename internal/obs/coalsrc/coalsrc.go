// Package coalsrc maps coalesce.Snapshot into component "coalescer" metrics
// and registers the coalescer health rule.
package coalsrc

import (
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	lt = obs.LabelTenant
	lr = obs.LabelReason
	lo = obs.LabelOutcome
	lm = obs.LabelMode

	// MetricWaiting is the gauge name read by the coalescer health rule.
	MetricWaiting = "waiting"
	// CheckCoalescer is the health check name registered by HealthRules.
	CheckCoalescer = "coalescer"
)

var none = obs.Labels{}

// Metrics returns every metric descriptor declared for component "coalescer".
func Metrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("mode", lm, "1 for the coalesce mode this instance runs (mode: off, auto, hinted)."),
		obs.GaugeDesc("join_mode", lm, "1 for the join mode (mode: strict, relaxed)."),
		obs.GaugeDesc("max_flights", 0, "Cap on keys with a flight (coalesce.max_flights)."),
		obs.GaugeDesc("max_waiters", 0, "Cap on waiters per flight, the request that started it included (coalesce.max_waiters)."),
		obs.GaugeDesc("max_reply_bytes", 0, "Cap on the stored reply of one flight (coalesce.max_reply_bytes).").WithUnit(obs.UnitBytes),
		obs.GaugeDesc("no_share_max", 0, "Cap on keys on the no-share list (coalesce.no_share_max)."),
		obs.GaugeDesc("no_share_for", 0, "How long a key stays on the no-share list (coalesce.no_share_for).").WithUnit(obs.UnitNs),
		obs.CounterDesc("not_eligible", 0, "Requests the mode does not share (not the safe shape under auto, no mark under hinted, or off)."),
		obs.CounterDesc("offered", 0, "Shareable requests offered to the flight table; each ends started, hit, refused or passed.").AsHeadline(),
		obs.CounterDesc("flights_started", 0, "Flights started: the request that starts one waits in the queue for a connection, or for the running read of its key (strict)."),
		obs.CounterDesc("hits", 0, "Requests that joined a flight and take no queue slot and no connection.").AsHeadline(),
		obs.CounterDesc("refused", 0, "Requests refused 53M04 at the waiter cap."),
		obs.CounterDesc("passed", lr, "Shareable requests passed on unshared (reason: no-share, full, duplicate)."),
		obs.CounterDesc("held", 0, "Flights started behind a read already running on the database (strict)."),
		obs.CounterDesc("promoted", 0, "Held flights started when the running read of their key ended."),
		obs.CounterDesc("sent", 0, "Flights whose request was written to the database."),
		obs.CounterDesc("flight_ends", lo, "Flights ended (outcome: completed, failed, cancelled, expired)."),
		obs.CounterDesc("detached", 0, "Flights that stopped sharing (the reply passed its cap, or the reply left a transaction open)."),
		obs.CounterDesc("shared_errors", 0, "Flights whose waiters all got one error: an error reply from the database or a proxy error."),
		obs.CounterDesc("handoffs", 0, "Times the request owning a flight left and a waiting one took over the queue slot or the connection."),
		obs.CounterDesc("waiters_in", 0, "Requests that waited on a flight, the starting one included."),
		obs.CounterDesc("waiter_ends", lo, "Waiters that left a flight (outcome: reply, shared-error, own-error, gone, refused)."),
		obs.CounterDesc("followers_left", 0, "Waiters other than the owner that left before the flight ended."),
		obs.CounterDesc("left_unknown", 0, "Leaves for requests not on any flight."),
		obs.CounterDesc("overflows", lr, "Caps reached (reason: waiters, reply, flights, unshareable)."),
		obs.CounterDesc("no_share_added", 0, "Keys put on the no-share list."),
		obs.CounterDesc("no_share_ends", lr, "Keys taken off the no-share list (reason: expired, evicted, renewed)."),
		obs.GaugeDesc(MetricWaiting, 0, "Requests waiting on flights now."),
		obs.HighWaterDesc("waiting_peak", 0, "Most requests waiting on flights at once."),
		obs.GaugeDesc("flights", 0, "Flights now (queued, running or held)."),
		obs.HighWaterDesc("flights_peak", 0, "Most flights at once."),
		obs.GaugeDesc("running", 0, "Flights running on the database now."),
		obs.HighWaterDesc("running_peak", 0, "Most flights running at once."),
		obs.GaugeDesc("table_size", 0, "Keys with a flight now."),
		obs.HighWaterDesc("table_size_peak", 0, "Most keys with a flight at once."),
		obs.GaugeDesc("stored_bytes", 0, "Reply bytes stored for sharing now.").WithUnit(obs.UnitBytes),
		obs.HighWaterDesc("stored_bytes_peak", 0, "Most reply bytes stored at once.").WithUnit(obs.UnitBytes),
		obs.GaugeDesc("no_share", 0, "Keys on the no-share list now."),
		obs.HighWaterDesc("no_share_peak", 0, "Most keys on the no-share list at once."),
		obs.HistogramDesc("waiters_per_flight", 0, obs.UnitCount, "Waiters answered by one flight that completed or failed, the owner included."),
		obs.CounterDesc("tenant_hits", lt, "Requests of this tenant that joined a flight."),
		obs.CounterDesc("hot_key_hits", lm, "Offers of the most offered keys (mode: rank, 1 is the hottest; coalesce.hot_keys, off by default)."),
	}
}

func b(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// Map emits all component "coalescer" metrics from s into e. Hot-key counts
// are labelled by rank only so SQL text never leaves the snapshot; histograms
// and per-tenant hits are emitted only when detail export is enabled.
func Map(s coalesce.Snapshot, e *obs.Emitter) {
	for m := coalesce.Mode(0); m < coalesce.NumModes; m++ {
		e.Gauge("mode", obs.Labels{Mode: m.String()}, b(s.Mode == m))
	}
	for j := coalesce.JoinMode(0); j < coalesce.NumJoinModes; j++ {
		e.Gauge("join_mode", obs.Labels{Mode: j.String()}, b(s.Join == j))
	}
	for _, g := range []struct {
		name string
		v    int64
	}{{"max_flights", s.MaxFlights}, {"max_waiters", s.MaxWaiters}, {"max_reply_bytes", s.MaxReplyBytes},
		{"no_share_max", s.NoShareMax}, {"no_share_for", s.NoShareFor},
		{MetricWaiting, s.Waiting.Value}, {"waiting_peak", s.Waiting.Peak}, {"flights", s.Flights.Value},
		{"flights_peak", s.Flights.Peak}, {"running", s.Running.Value}, {"running_peak", s.Running.Peak},
		{"table_size", s.Entries.Value}, {"table_size_peak", s.Entries.Peak}, {"stored_bytes", s.StoredBytes.Value},
		{"stored_bytes_peak", s.StoredBytes.Peak}, {"no_share", s.NoShare.Value}, {"no_share_peak", s.NoShare.Peak}} {
		e.Gauge(g.name, none, g.v)
	}
	for _, c := range []struct {
		name string
		v    int64
	}{{"not_eligible", s.NotEligible}, {"offered", s.Offered}, {"flights_started", s.Started}, {"hits", s.Hits},
		{"refused", s.Refused}, {"held", s.Held}, {"promoted", s.Promoted}, {"sent", s.Sent}, {"detached", s.Detached},
		{"shared_errors", s.SharedErrors}, {"handoffs", s.Handoffs}, {"waiters_in", s.WaitersIn},
		{"followers_left", s.FollowersLeft}, {"left_unknown", s.LeftUnknown}, {"no_share_added", s.NoShareAdded}} {
		e.Counter(c.name, none, c.v)
	}
	for _, p := range []struct {
		reason string
		v      int64
	}{{"no-share", s.PassedNoShare}, {"full", s.PassedFull}, {"duplicate", s.PassedDuplicate}} {
		e.Counter("passed", obs.Labels{Reason: p.reason}, p.v)
	}
	for _, f := range []struct {
		outcome string
		v       int64
	}{{"completed", s.Completed}, {"failed", s.Failed}, {"cancelled", s.Cancelled}, {"expired", s.Expired}} {
		e.Counter("flight_ends", obs.Labels{Outcome: f.outcome}, f.v)
	}
	for _, w := range []struct {
		outcome string
		v       int64
	}{{"reply", s.WaiterReply}, {"shared-error", s.WaiterSharedError}, {"own-error", s.WaiterOwnError},
		{"gone", s.WaiterGone}, {"refused", s.WaiterRefused}} {
		e.Counter("waiter_ends", obs.Labels{Outcome: w.outcome}, w.v)
	}
	for c, v := range s.Overflows {
		e.Counter("overflows", obs.Labels{Reason: coalesce.Cap(c).String()}, v)
	}
	for _, n := range []struct {
		reason string
		v      int64
	}{{"expired", s.NoShareExpired}, {"evicted", s.NoShareEvicted}, {"renewed", s.NoShareRenewed}} {
		e.Counter("no_share_ends", obs.Labels{Reason: n.reason}, n.v)
	}
	for i := range max(1, int(s.HotKeysMax)) {
		var v int64
		if i < len(s.HotKeys) {
			v = s.HotKeys[i].Count
		}
		e.Counter("hot_key_hits", obs.Labels{Mode: strconv.Itoa(i + 1)}, v)
	}
	if e.Detail() {
		e.Histogram("waiters_per_flight", none, s.WaitersPerFlight)
		for _, t := range s.PerTenant {
			e.Counter("tenant_hits", obs.Labels{Tenant: string(t.Tenant)}, t.Hits)
		}
	}
}

// Source wraps snapshot into an obs.Source for component "coalescer".
func Source(snapshot func() coalesce.Snapshot) obs.Source {
	return obs.NewSource(obs.ComponentCoalescer, Metrics(), snapshot, Map)
}

// HealthRules returns the coalescer health check, which always reports OK:
// cap refusals (53M04) and unshared pass-throughs are per-request outcomes
// that do not degrade the instance.
func HealthRules() []obs.Rule {
	return []obs.Rule{{Name: CheckCoalescer, Check: func(s obs.InstanceSnapshot) (obs.State, string) {
		n := s.Value(obs.ComponentCoalescer, MetricWaiting, none)
		return obs.StateOK, "coalescer: " + strconv.FormatInt(n, 10) + " requests waiting on flights; refusals are per request (53M04) and never degrade the instance"
	}}}
}
