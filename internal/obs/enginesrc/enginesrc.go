// Package enginesrc maps proxy.Snapshot and proxy.ShellSnapshot into component
// "engine" metrics for internal/obs, registers the wire and classifier sources
// beside it, and provides the "database" health check rule.
package enginesrc

import (
	"errors"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/admsrc"
	"github.com/nickstrad/mini-gateway/internal/obs/clssrc"
	"github.com/nickstrad/mini-gateway/internal/obs/wiresrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const (
	lo = obs.LabelOutcome
	lr = obs.LabelReason
	lm = obs.LabelMode

	// MetricDatabaseDown is the gauge set to 1 while no database connection is usable and dials fail.
	MetricDatabaseDown = "database_down"
	// CheckDatabase is the health rule name evaluating MetricDatabaseDown.
	CheckDatabase = "database"
)

var none = obs.Labels{}

var tlsModes = [proxy.NumTLSVersions]string{"plain", "1.2", "1.3"}

var leaseOutcomes = [...]string{"reset", "close", "as-is"}

// Metrics returns the metric descriptors exported from proxy.Snapshot.
func Metrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("clients_connected", 0, "Client connections open now."),
		obs.HighWaterDesc("clients_connected_peak", 0, "Most client connections open at once."),
		obs.GaugeDesc("clients_in_startup", 0, "Client connections in startup now."),
		obs.GaugeDesc("clients_in_transaction", 0, "Clients holding a lease inside a transaction now."),
		obs.HighWaterDesc("clients_in_startup_peak", 0, "Most client connections in startup at once."),
		obs.HighWaterDesc("clients_in_transaction_peak", 0, "Most clients inside a transaction at once."),
		obs.GaugeDesc("clients_by_tls", lm, "Ready client connections by TLS version (mode: plain, 1.2, 1.3)."),
		obs.CounterDesc("clients_accepted", 0, "Client connections accepted."),
		obs.CounterDesc("clients_refused", 0, "Client connections refused at the client or startup cap."),
		obs.CounterDesc("client_closes", lr, "Client connections closed, by reason."),
		obs.CounterDesc("cancel_requests", 0, "Cancel requests recognised (mapping arrives in chunk 7)."),
		obs.CounterDesc("requests_received", 0, "Query messages received after startup.").AsHeadline(),
		obs.CounterDesc("requests_accepted", 0, "Requests that passed the gates in front of the queue."),
		obs.CounterDesc("requests_completed", 0, "Requests whose reply reached its end marker."),
		obs.CounterDesc("requests_failed", 0, "Accepted requests that failed after acceptance."),
		obs.CounterDesc("requests_shed", 0, "Accepted requests shed by the queue (full or waited too long)."),
		obs.CounterDesc("requests_rejected", 0, "Requests refused before acceptance (reject list, extended protocol, gates)."),
		obs.GaugeDesc("requests_queued", 0, "Requests waiting in the queue now."),
		obs.HighWaterDesc("requests_queued_peak", 0, "Most requests waiting in the queue at once."),
		obs.GaugeDesc("requests_in_flight", 0, "Requests forwarded and not yet answered."),
		obs.HighWaterDesc("requests_in_flight_peak", 0, "Most requests in flight at once."),
		obs.CounterDesc("replies", lr, "Proxy-made replies by error-contract row (reason)."),
		obs.CounterDesc("stage_passed", lm, "Requests that passed each stage (mode)."),
		obs.CounterDesc("stage_refused", lm, "Requests refused at each stage (mode)."),
		obs.CounterDesc("failures_reached", lo, "Failures by whether the request may have reached the database (outcome: no, yes)."),
		obs.CounterDesc("failures_without_reply", 0, "Failures where the client was gone and no reply was possible."),
		obs.CounterDesc("non_request_replies", 0, "Proxy-made replies not tied to a request (startup caps, violations, a loss between statements)."),
		obs.CounterDesc("retry_asked", 0, "Failures the retry seam asked to retry (policy off: always 0)."),
		obs.HistogramDesc("statement_time", 0, obs.UnitNs, "Database statement time, from send to end of reply."),
		obs.HistogramDesc("queue_wait", 0, obs.UnitNs, "Time a dispatched request waited in the queue."),
		obs.GaugeDesc("queue_capacity", 0, "Capacity of the queue."),
		obs.HighWaterDesc("relay_buffer_high_water", 0, "Most reply bytes held for one client at once.").WithUnit(obs.UnitBytes),
		obs.GaugeDesc("relay_buffer_capacity", 0, "Per-client reply buffer capacity.").WithUnit(obs.UnitBytes),
		obs.CounterDesc("relay_pauses", 0, "Times reading a database connection was paused for a slow client."),
		obs.CounterDesc("clients_dropped", 0, "Clients dropped because their writes stalled past the deadline."),
		obs.CounterDesc("parameter_status_in_turn", 0, "ParameterStatus messages relayed during a client's turn."),
		obs.CounterDesc("leases_started", 0, "Database connections lent to a client."),
		obs.CounterDesc("leases_ended", 0, "Leases that ended."),
		obs.CounterDesc("leases_ended_by", lo, "Leases that ended, by what happened to the connection (outcome: reset, close, as-is)."),
		obs.CounterDesc("resets_started", 0, "DISCARD ALL resets sent after a lease."),
		obs.CounterDesc("resets_completed", 0, "Resets answered with the DISCARD ALL tag and status I."),
		obs.CounterDesc("resets_failed", lr, "Resets that failed, by reason; the connection was closed."),
		obs.GaugeDesc("connections_resetting", 0, "Database connections being reset now."),
		obs.HighWaterDesc("connections_resetting_peak", 0, "Most database connections being reset at once."),
		obs.HistogramDesc("reset_time", 0, obs.UnitNs, "Time from sending DISCARD ALL to its ReadyForQuery."),
		obs.CounterDesc("ignored_database_data", 0, "Bytes events from database connections already closed."),
		obs.GaugeDesc("database_open", 0, "Database connections open now (idle, lent or resetting)."),
		obs.HighWaterDesc("database_open_peak", 0, "Most database connections open at once."),
		obs.GaugeDesc("database_connecting", 0, "Database connections being opened now."),
		obs.GaugeDesc("database_lent", 0, "Database connections lent now."),
		obs.GaugeDesc("database_idle", 0, "Database connections idle now."),
		obs.HighWaterDesc("database_connecting_peak", 0, "Most database connections being opened at once."),
		obs.HighWaterDesc("database_lent_peak", 0, "Most database connections lent at once."),
		obs.HighWaterDesc("database_idle_peak", 0, "Most database connections idle at once."),
		obs.GaugeDesc("database_capacity", 0, "Size of the fixed set of database connections."),
		obs.GaugeDesc(MetricDatabaseDown, 0, "1 while no database connection is usable and the last dial failed."),
		obs.CounterDesc("database_dials", 0, "Database connections dialled."),
		obs.CounterDesc("database_dial_failures", 0, "Dials that failed before the connection opened."),
		obs.CounterDesc("database_ready", 0, "Database connections that finished startup."),
		obs.CounterDesc("database_closes", lr, "Database connections closed, by reason."),
		obs.CounterDesc("database_connect_failures", lr, "Database connects that failed, by class (reason)."),
		obs.GaugeDesc("timers_pending", 0, "Engine deadlines armed now."),
		obs.HighWaterDesc("timers_pending_peak", 0, "Most engine deadlines armed at once."),
		obs.CounterDesc("publishes", 0, "Publish ticks handled by the engine."),
		obs.GaugeDesc("draining", 0, "1 while the draining input is set."),
		obs.CounterDesc("events", 0, "Events handled by the engine."),
	}
}

// ShellMetrics returns the metric descriptors exported from proxy.ShellSnapshot.
func ShellMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("shell_mailbox_depth", 0, "Events waiting in the engine mailbox now."),
		obs.HighWaterDesc("shell_mailbox_peak", 0, "Most events waiting in the engine mailbox at once."),
		obs.GaugeDesc("shell_mailbox_capacity", 0, "Capacity of the engine mailbox."),
		obs.CounterDesc("shell_mailbox_full", 0, "Posts that found the engine mailbox full and waited."),
		obs.GaugeDesc("shell_goroutines", 0, "Shell goroutines running now."),
		obs.HighWaterDesc("shell_goroutines_peak", 0, "Most shell goroutines at once."),
		obs.GaugeDesc("shell_readers", lm, "Socket reader goroutines now (mode: client, database)."),
		obs.HighWaterDesc("shell_readers_peak", lm, "Most socket reader goroutines at once (mode: client, database)."),
		obs.GaugeDesc("shell_writers", lm, "Socket writer goroutines now (mode: client, database)."),
		obs.HighWaterDesc("shell_writers_peak", lm, "Most socket writer goroutines at once (mode: client, database)."),
		obs.CounterDesc("shell_accepted", 0, "Client sockets accepted by the listener."),
		obs.CounterDesc("shell_admin_requests", 0, "Requests served by the admin listener."),
		obs.CounterDesc("shell_handshakes_failed", 0, "TLS handshakes that failed in the shell."),
		obs.CounterDesc("shell_deadlines", lm, "Socket deadlines that fired (mode: read, write)."),
		obs.CounterDesc("shell_accept_refused", 0, "Accepted sockets closed at once because the shell was full or closing."),
		obs.CounterDesc("shell_accept_errors", 0, "Errors returned by the client listener's Accept."),
		obs.CounterDesc("shell_write_queue_overflows", 0, "Connections closed because their write queue passed its cap."),
		obs.CounterDesc("shell_stale_events", 0, "Events dropped because their connection was already closed."),
	}
}

// Map emits every engine metric from s into e.
func Map(s proxy.Snapshot, e *obs.Emitter) {
	mapClients(s, e)
	mapRequests(s, e)
	mapLeases(s, e)
	mapDatabase(s, e)
	e.Gauge("timers_pending", none, s.TimersPending.Value)
	e.Gauge("timers_pending_peak", none, s.TimersPending.Peak)
	e.Counter("publishes", none, s.Publishes)
	e.Gauge("draining", none, boolInt(s.Draining))
	e.Counter("events", none, s.Events)
}

func mapClients(s proxy.Snapshot, e *obs.Emitter) {
	e.Gauge("clients_connected", none, s.ClientsConnected.Value)
	e.Gauge("clients_connected_peak", none, s.ClientsConnected.Peak)
	e.Gauge("clients_in_startup", none, s.ClientsInStartup.Value)
	e.Gauge("clients_in_transaction", none, s.ClientsInTx.Value)
	e.Gauge("clients_in_startup_peak", none, s.ClientsInStartup.Peak)
	e.Gauge("clients_in_transaction_peak", none, s.ClientsInTx.Peak)
	for i, v := range s.ClientsByTLS {
		e.Gauge("clients_by_tls", obs.Labels{Mode: tlsModes[i]}, v)
	}
	e.Counter("clients_accepted", none, s.ClientsAccepted)
	e.Counter("clients_refused", none, s.ClientsRefused)
	for i := proxy.CloseReason(1); i < proxy.NumCloseReasons; i++ {
		if i.ClientSide() {
			e.Counter("client_closes", obs.Labels{Reason: i.String()}, s.ClientCloses[i])
		}
	}
	e.Counter("cancel_requests", none, s.CancelRequests)
	e.Counter("clients_dropped", none, s.ClientsDropped)
}

func mapRequests(s proxy.Snapshot, e *obs.Emitter) {
	e.Counter("requests_received", none, s.Received)
	e.Counter("requests_accepted", none, s.Accepted)
	e.Counter("requests_completed", none, s.Completed)
	e.Counter("requests_failed", none, s.Failed)
	e.Counter("requests_shed", none, s.Shed)
	e.Counter("requests_rejected", none, s.Rejected)
	e.Gauge("requests_queued", none, s.Queued.Value)
	e.Gauge("requests_queued_peak", none, s.Queued.Peak)
	e.Gauge("requests_in_flight", none, s.InFlight.Value)
	e.Gauge("requests_in_flight_peak", none, s.InFlight.Peak)
	for i, v := range s.ByRow {
		e.Counter("replies", obs.Labels{Reason: pgwire.ErrRowID(i).Row().ID}, v)
	}
	for i := proxy.Stage(0); i < proxy.NumStages; i++ {
		e.Counter("stage_passed", obs.Labels{Mode: i.String()}, s.StagePassed[i])
		e.Counter("stage_refused", obs.Labels{Mode: i.String()}, s.StageRefuse[i])
	}
	e.Counter("failures_reached", obs.Labels{Outcome: "no"}, s.ReachedNo)
	e.Counter("failures_reached", obs.Labels{Outcome: "yes"}, s.ReachedYes)
	e.Counter("failures_without_reply", none, s.FailedNoReply)
	e.Counter("non_request_replies", none, s.NonRequestReplies)
	e.Counter("retry_asked", none, s.RetryAsked)
	if e.Detail() {
		e.Histogram("statement_time", none, s.StatementTime)
		e.Histogram("queue_wait", none, s.QueueWait)
		e.Histogram("reset_time", none, s.ResetTime)
	}
	e.Gauge("queue_capacity", none, s.QueueCap)
	e.Gauge("relay_buffer_high_water", none, s.RelayHighWater)
	e.Gauge("relay_buffer_capacity", none, s.RelayCap)
	e.Counter("relay_pauses", none, s.RelayPauses)
	e.Counter("parameter_status_in_turn", none, s.ParamStatusTurn)
}

func mapLeases(s proxy.Snapshot, e *obs.Emitter) {
	e.Counter("leases_started", none, s.LeasesStarted)
	e.Counter("leases_ended", none, s.LeasesEnded)
	for i, v := range [...]int64{s.LeasesEndedReset, s.LeasesEndedClose, s.LeasesEndedAsIs} {
		e.Counter("leases_ended_by", obs.Labels{Outcome: leaseOutcomes[i]}, v)
	}
	e.Counter("resets_started", none, s.ResetsStarted)
	e.Counter("resets_completed", none, s.ResetsCompleted)
	for i, v := range s.ResetsFailed {
		e.Counter("resets_failed", obs.Labels{Reason: proxy.ResetFailure(i).String()}, v)
	}
	e.Gauge("connections_resetting", none, s.Resetting.Value)
	e.Gauge("connections_resetting_peak", none, s.Resetting.Peak)
	e.Counter("ignored_database_data", none, s.IgnoredDBData)
}

func mapDatabase(s proxy.Snapshot, e *obs.Emitter) {
	e.Gauge("database_open", none, s.DBOpen.Value)
	e.Gauge("database_open_peak", none, s.DBOpen.Peak)
	e.Gauge("database_connecting", none, s.DBConnecting.Value)
	e.Gauge("database_lent", none, s.DBLent.Value)
	e.Gauge("database_idle", none, s.DBIdle.Value)
	e.Gauge("database_connecting_peak", none, s.DBConnecting.Peak)
	e.Gauge("database_lent_peak", none, s.DBLent.Peak)
	e.Gauge("database_idle_peak", none, s.DBIdle.Peak)
	e.Gauge("database_capacity", none, s.DBCap)
	e.Gauge(MetricDatabaseDown, none, boolInt(s.DBDown))
	e.Counter("database_dials", none, s.DBDials)
	e.Counter("database_dial_failures", none, s.DBDialFailures)
	e.Counter("database_ready", none, s.DBReady)
	for i := proxy.CloseReason(1); i < proxy.NumCloseReasons; i++ {
		if !i.ClientSide() {
			e.Counter("database_closes", obs.Labels{Reason: i.String()}, s.DBCloses[i])
		}
	}
	for i, v := range s.DBConnectFails {
		if i > 0 {
			e.Counter("database_connect_failures", obs.Labels{Reason: pgwire.Class(i).String()}, v)
		}
	}
}

// MapShell emits every goroutine-shell metric from s into e.
func MapShell(s proxy.ShellSnapshot, e *obs.Emitter) {
	e.Gauge("shell_mailbox_depth", none, s.MailboxDepth.Value)
	e.Gauge("shell_mailbox_peak", none, s.MailboxDepth.Peak)
	e.Gauge("shell_mailbox_capacity", none, s.MailboxCap)
	e.Counter("shell_mailbox_full", none, s.MailboxFull)
	e.Gauge("shell_goroutines", none, s.Goroutines.Value)
	e.Gauge("shell_goroutines_peak", none, s.Goroutines.Peak)
	e.Gauge("shell_readers", obs.Labels{Mode: "client"}, s.ClientReaders.Value)
	e.Gauge("shell_readers", obs.Labels{Mode: "database"}, s.DBReaders.Value)
	e.Gauge("shell_readers_peak", obs.Labels{Mode: "client"}, s.ClientReaders.Peak)
	e.Gauge("shell_readers_peak", obs.Labels{Mode: "database"}, s.DBReaders.Peak)
	e.Gauge("shell_writers", obs.Labels{Mode: "client"}, s.ClientWriters.Value)
	e.Gauge("shell_writers", obs.Labels{Mode: "database"}, s.DBWriters.Value)
	e.Gauge("shell_writers_peak", obs.Labels{Mode: "client"}, s.ClientWriters.Peak)
	e.Gauge("shell_writers_peak", obs.Labels{Mode: "database"}, s.DBWriters.Peak)
	e.Counter("shell_accepted", none, s.Accepted)
	e.Counter("shell_admin_requests", none, s.AdminRequests)
	e.Counter("shell_handshakes_failed", none, s.HandshakesFailed)
	e.Counter("shell_deadlines", obs.Labels{Mode: "read"}, s.ReadDeadlines)
	e.Counter("shell_deadlines", obs.Labels{Mode: "write"}, s.WriteDeadlines)
	e.Counter("shell_accept_refused", none, s.AcceptRefused)
	e.Counter("shell_accept_errors", none, s.AcceptErrors)
	e.Counter("shell_write_queue_overflows", none, s.QueueOverflows)
	e.Counter("shell_stale_events", none, s.StaleEvents)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Snapshot bundles the engine and shell snapshots so one obs.Source exports
// both under component "engine".
type Snapshot struct {
	Engine proxy.Snapshot
	Shell  proxy.ShellSnapshot
}

// MapAll emits both Engine and Shell snapshots into e.
func MapAll(s Snapshot, e *obs.Emitter) {
	Map(s.Engine, e)
	MapShell(s.Shell, e)
}

// AllMetrics returns the combined engine and shell metric descriptors registered
// under component "engine".
func AllMetrics() []obs.MetricDesc { return append(Metrics(), ShellMetrics()...) }

// Source builds the obs.Source for component "engine". A nil shell callback
// leaves shell metrics at zero (used in tier S simulator runs).
func Source(engine func() proxy.Snapshot, shell func() proxy.ShellSnapshot) obs.Source {
	snap := func() Snapshot {
		s := Snapshot{Engine: engine()}
		if shell != nil {
			s.Shell = shell()
		}
		return s
	}
	return obs.NewSource(obs.ComponentEngine, AllMetrics(), snap, MapAll)
}

// Sources returns the engine, wire, and classifier obs.Sources for e.
func Sources(e *proxy.Engine, shell func() proxy.ShellSnapshot) []obs.Source {
	return []obs.Source{
		Source(e.Snapshot, shell),
		wiresrc.Source(e.WireSnapshot),
		clssrc.Source(func() clssrc.Snapshot {
			return clssrc.Snapshot{Classifier: e.ClassifierSnapshot(), Replies: e.ReplySnapshot(), Sync: e.SyncSnapshot()}
		}),
		admsrc.Source(e.AdmissionSnapshot),
	}
}

// Register attaches the engine, wire, and classifier sources of e to collector c.
func Register(c *obs.Collector, e *proxy.Engine, shell func() proxy.ShellSnapshot) error {
	var errs []error
	for _, s := range Sources(e, shell) {
		errs = append(errs, c.Register(s))
	}
	return errors.Join(errs...)
}

// HealthRules returns the database reachability rule, which reports StateDegraded
// (never StateFailed) while no database connection is usable and dials are failing.
func HealthRules() []obs.Rule {
	return append([]obs.Rule{{Name: CheckDatabase, Check: func(s obs.InstanceSnapshot) (obs.State, string) {
		if s.Value(obs.ComponentEngine, MetricDatabaseDown, none) > 0 {
			return obs.StateDegraded, "database unreachable: no usable connection and dials failing"
		}
		return obs.StateOK, "database reachable"
	}}}, admsrc.HealthRules()...)
}
