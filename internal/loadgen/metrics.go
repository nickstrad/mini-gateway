package loadgen

import "github.com/nickstrad/mini-gateway/internal/obs"

// Metrics declares every scalar and histogram exported by component "loadgen".
func Metrics() []obs.MetricDesc {
	lo, lr := obs.LabelOutcome, obs.LabelReason
	return []obs.MetricDesc{
		obs.GaugeDesc("planned", 0, "Requests in the load plan (rate times duration)."),
		obs.GaugeDesc("target_rate", 0, "Target rate of the load plan, requests per second."),
		obs.CounterDesc("intended", 0, "Requests whose intended send time has come.").AsHeadline(),
		obs.CounterDesc("sent", 0, "Requests written to a connection."),
		obs.CounterDesc("could_not_send", 0, "Intended requests that were never sent (no free connection, connection lost, run ended)."),
		obs.CounterDesc("late", 0, "Sent requests written later than the late threshold after their intended send time."),
		obs.GaugeDesc("queued", 0, "Requests given to a connection and not yet written."),
		obs.GaugeDesc("in_flight", 0, "Requests written and not yet answered."),
		obs.CounterDesc("completed", lo, "Sent requests by final outcome (ok, db-error, refused, lost)."),
		obs.CounterDesc("errors_by_class", lr, "Error replies by SQLSTATE class (reason, two characters).").WithOpenLabels(),
		obs.GaugeDesc("conns_open", 0, "Load connections open now."),
		obs.CounterDesc("connect_failures", 0, "Load connections that failed to connect or log in."),
		obs.HistogramDesc("latency", 0, obs.UnitNs, "Request latency from the intended send time to the end of the reply."),
		obs.HistogramDesc("latency_from_send", 0, obs.UnitNs, "Request latency from the actual send (coordinated-omission control)."),
	}
}

// Map emits every loadgen metric from Snapshot s into e, including zero rows for all four outcomes.
func Map(s Snapshot, e *obs.Emitter) {
	var none obs.Labels
	e.Gauge("planned", none, s.Planned)
	e.Gauge("target_rate", none, s.TargetRate)
	e.Counter("intended", none, s.Intended)
	e.Counter("sent", none, s.Sent)
	e.Counter("could_not_send", none, s.CouldNotSend)
	e.Counter("late", none, s.Late)
	e.Gauge("queued", none, s.Queued)
	e.Gauge("in_flight", none, s.InFlight)
	for _, o := range Outcomes() {
		e.Counter("completed", obs.Labels{Outcome: o.String()}, s.Completed[o])
	}
	for _, c := range s.ErrorsByClass {
		e.Counter("errors_by_class", obs.Labels{Reason: c.Class}, c.Count)
	}
	e.Gauge("conns_open", none, s.ConnsOpen)
	e.Counter("connect_failures", none, s.ConnectFailures)
	e.Histogram("latency", none, s.Latency)
	e.Histogram("latency_from_send", none, s.LatencyFromSend)
}

// Source wraps a snapshot callback as an obs.Source for component "loadgen".
func Source(snapshot func() Snapshot) obs.Source {
	return obs.NewSource(obs.ComponentLoadgen, Metrics(), snapshot, Map)
}
