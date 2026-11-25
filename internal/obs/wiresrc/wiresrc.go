package wiresrc

import (
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

const (
	lo = obs.LabelOutcome
	lr = obs.LabelReason
	lm = obs.LabelMode
)

var none = obs.Labels{}

var readyStatuses = [3]string{"I", "T", "E"}

func sideMetrics(side, peer string) []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.CounterDesc(side+"_messages_in", 0, "Messages read from the "+peer+"."),
		obs.CounterDesc(side+"_messages_out", 0, "Messages written to the "+peer+"."),
		obs.CounterDesc(side+"_bytes_in", 0, "Bytes read from the "+peer+".").WithUnit(obs.UnitBytes),
		obs.CounterDesc(side+"_bytes_out", 0, "Bytes written to the "+peer+".").WithUnit(obs.UnitBytes),
		obs.CounterDesc(side+"_violations", lr, "Protocol violations by the "+peer+", by violation (reason)."),
		obs.CounterDesc(side+"_oversized", 0, "Messages from the "+peer+" refused for their size."),
	}
}

func Metrics() []obs.MetricDesc {
	ms := []obs.MetricDesc{
		obs.CounterDesc("client_startups_begun", 0, "Client sessions that began startup.").AsHeadline(),
		obs.CounterDesc("client_startups_completed", 0, "Client sessions that reached ReadyForQuery."),
		obs.CounterDesc("client_startups_failed", lr, "Client sessions that ended before ReadyForQuery, by reason."),
		obs.GaugeDesc("client_in_startup", 0, "Client sessions in startup now."),
		obs.HighWaterDesc("client_in_startup_peak", 0, "Most client sessions in startup at once."),
		obs.CounterDesc("client_prestartup_requests", lo|lm, "Requests before startup by kind (mode: tls, gss, cancel) and answer (outcome: S, N, none)."),
		obs.CounterDesc("client_tls_completed", lm, "Client TLS handshakes completed, by TLS version (mode)."),
		obs.CounterDesc("client_tls_failed", lr, "Client TLS handshakes that did not complete, by handshake result (reason)."),
		obs.CounterDesc("client_plaintext_refused", 0, "Plaintext startups refused because TLS is required."),
		obs.CounterDesc("client_auth_failures", lr, "Client authentication failures, by cause (reason)."),
		obs.CounterDesc("client_cancel_requests", 0, "Cancel requests received from clients."),
		obs.CounterDesc("client_cancel_after_tls", 0, "Cancel requests received after a TLS upgrade."),
		obs.CounterDesc("client_terminated", 0, "Client sessions ended by Terminate."),
		obs.CounterDesc("client_queries_seen", 0, "Simple queries received from clients."),
		obs.CounterDesc("client_extended_refused", 0, "Extended-protocol messages refused."),
	}
	ms = append(ms, sideMetrics("client", "client")...)
	ms = append(ms,
		obs.CounterDesc("database_startups_begun", 0, "Database sessions that began startup.").AsHeadline(),
		obs.CounterDesc("database_startups_completed", 0, "Database sessions that reached ReadyForQuery."),
		obs.CounterDesc("database_startups_failed", lr, "Database sessions that failed to start, by error class (reason)."),
		obs.GaugeDesc("database_in_startup", 0, "Database sessions in startup now."),
		obs.HighWaterDesc("database_in_startup_peak", 0, "Most database sessions in startup at once."),
		obs.CounterDesc("database_tls_requests", 0, "TLS requests sent to the database."),
		obs.CounterDesc("database_tls_answers", lo, "Database answers to the TLS request (outcome: S, N, none)."),
		obs.CounterDesc("database_tls_completed", lm, "Database TLS handshakes completed, by TLS version (mode)."),
		obs.CounterDesc("database_tls_failed", lr, "Database TLS handshakes that did not complete, by handshake result (reason)."),
		obs.CounterDesc("database_auth_failures", lr, "Database authentication failures, by cause (reason)."),
		obs.CounterDesc("database_server_fatals", 0, "FATAL errors from the database during startup."),
		obs.CounterDesc("database_replies", 0, "Query replies followed to their end marker."),
		obs.CounterDesc("database_ready", lm, "ReadyForQuery messages by transaction status (mode: I, T, E)."),
		obs.CounterDesc("database_errors", 0, "ErrorResponse messages from the database."),
		obs.CounterDesc("database_fatal_errors", 0, "FATAL ErrorResponse messages from the database."),
		obs.CounterDesc("database_parameter_status", 0, "ParameterStatus messages from the database after startup."),
		obs.CounterDesc("database_notices", 0, "NoticeResponse messages from the database."),
		obs.CounterDesc("database_notifications", 0, "NotificationResponse messages from the database."),
		obs.CounterDesc("database_end_without_marker", 0, "Replies that ended without ReadyForQuery."),
		obs.CounterDesc("database_unknown_pass_through", 0, "Well-framed unknown messages passed through."),
	)
	return append(ms, sideMetrics("database", "database")...)
}

func Source(snapshot func() pgwire.WireSnapshot) obs.Source {
	return obs.NewSource(obs.ComponentWire, Metrics(), snapshot, Map)
}

func Map(s pgwire.WireSnapshot, e *obs.Emitter) {
	mapClient(s.Client, e)
	mapDatabase(s.Database, e)
}

func mapClient(c pgwire.ClientWireSnapshot, e *obs.Emitter) {
	e.Counter("client_startups_begun", none, c.StartupsBegun)
	e.Counter("client_startups_completed", none, c.StartupsCompleted)
	for i, v := range c.StartupsFailed {
		e.Counter("client_startups_failed", obs.Labels{Reason: pgwire.Reason(i).String()}, v)
	}
	e.Gauge("client_in_startup", none, c.InStartup)
	e.Gauge("client_in_startup_peak", none, c.InStartupPeak)
	for k, answers := range c.Requests {
		for a, v := range answers {
			e.Counter("client_prestartup_requests", obs.Labels{Mode: pgwire.RequestKind(k).String(), Outcome: pgwire.Answer(a).String()}, v)
		}
	}
	tls(e, "client", c.TLSCompleted, c.TLSFailed)
	e.Counter("client_plaintext_refused", none, c.PlaintextRefused)
	auth(e, "client_auth_failures", c.AuthFailures)
	e.Counter("client_cancel_requests", none, c.CancelRequests)
	e.Counter("client_cancel_after_tls", none, c.CancelAfterTLS)
	e.Counter("client_terminated", none, c.Terminated)
	e.Counter("client_queries_seen", none, c.QueriesSeen)
	e.Counter("client_extended_refused", none, c.ExtendedRefused)
	side(e, "client", c.SideCounters)
}

func mapDatabase(d pgwire.DatabaseWireSnapshot, e *obs.Emitter) {
	e.Counter("database_startups_begun", none, d.StartupsBegun)
	e.Counter("database_startups_completed", none, d.StartupsCompleted)
	for i, v := range d.StartupsFailed {
		e.Counter("database_startups_failed", obs.Labels{Reason: pgwire.Class(i).String()}, v)
	}
	e.Gauge("database_in_startup", none, d.InStartup)
	e.Gauge("database_in_startup_peak", none, d.InStartupPeak)
	e.Counter("database_tls_requests", none, d.TLSRequests)
	for a, v := range d.TLSAnswers {
		e.Counter("database_tls_answers", obs.Labels{Outcome: pgwire.Answer(a).String()}, v)
	}
	tls(e, "database", d.TLSCompleted, d.TLSFailed)
	auth(e, "database_auth_failures", d.AuthFailures)
	e.Counter("database_server_fatals", none, d.ServerFatals)
	e.Counter("database_replies", none, d.Replies)
	for i, v := range d.ReadyByStatus {
		e.Counter("database_ready", obs.Labels{Mode: readyStatuses[i]}, v)
	}
	e.Counter("database_errors", none, d.Errors)
	e.Counter("database_fatal_errors", none, d.FatalErrors)
	e.Counter("database_parameter_status", none, d.ParameterStatus)
	e.Counter("database_notices", none, d.Notices)
	e.Counter("database_notifications", none, d.Notifications)
	e.Counter("database_end_without_marker", none, d.EndWithoutMarker)
	e.Counter("database_unknown_pass_through", none, d.UnknownPassThrough)
	side(e, "database", d.SideCounters)
}

func tls(e *obs.Emitter, side string, completed [pgwire.NumTLSVersions]int64, failed [pgwire.NumHandshakeOutcomes]int64) {
	for i, v := range completed {
		e.Counter(side+"_tls_completed", obs.Labels{Mode: pgwire.TLSVersion(i).String()}, v)
	}
	for i, v := range failed {
		e.Counter(side+"_tls_failed", obs.Labels{Reason: pgwire.HandshakeOutcome(i).String()}, v)
	}
}

func auth(e *obs.Emitter, name string, causes [pgwire.NumAuthCauses]int64) {
	for i, v := range causes {
		e.Counter(name, obs.Labels{Reason: pgwire.AuthCause(i).String()}, v)
	}
}

func side(e *obs.Emitter, side string, s pgwire.SideCounters) {
	e.Counter(side+"_messages_in", none, s.MessagesIn)
	e.Counter(side+"_messages_out", none, s.MessagesOut)
	e.Counter(side+"_bytes_in", none, s.BytesIn)
	e.Counter(side+"_bytes_out", none, s.BytesOut)
	for i, v := range s.Violations {
		e.Counter(side+"_violations", obs.Labels{Reason: pgwire.Violation(i).String()}, v)
	}
	e.Counter(side+"_oversized", none, s.Oversized)
}
