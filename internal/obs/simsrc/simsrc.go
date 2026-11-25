// Package simsrc declares metrics and maps raw Snapshot() structs from the
// frozen simulator components (sim, simnet, fakepg, workload) into obs.Emitter
// without modifying internal/sim/... or introducing circular imports.
package simsrc

import (
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

var none = obs.Labels{}

const (
	lt = obs.LabelTenant
	lo = obs.LabelOutcome
	lr = obs.LabelReason
	lm = obs.LabelMode
)

// KernelMetrics returns the metric declarations for the simulator kernel (sim).
func KernelMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.CounterDesc("steps", 0, "Events the kernel has run.").AsHeadline(),
		obs.CounterDesc("scheduled", 0, "Events scheduled."),
		obs.CounterDesc("fired", 0, "Scheduled events that fired."),
		obs.CounterDesc("cancelled", 0, "Scheduled events cancelled before firing."),
		obs.GaugeDesc("heap_len", 0, "Events waiting in the event heap."),
		obs.HighWaterDesc("heap_peak", 0, "Most events ever waiting in the event heap."),
		obs.GaugeDesc("pending_timers", 0, "Waiting events that are not background work."),
		obs.GaugeDesc("background_timers", 0, "Waiting events marked as background work."),
		obs.CounterDesc("trace_records", 0, "Trace records written."),
		obs.GaugeDesc("trace_ring_len", 0, "Trace records held in the ring."),
		obs.CounterDesc("faults_applied", 0, "Faults applied by the kernel's fault schedule."),
		obs.CounterDesc("invariant_checks", 0, "Invariant check passes run."),
	}
}

// Kernel wraps s as an obs.Source under ComponentSim.
func Kernel(s *sim.Sim) obs.Source {
	return obs.NewSource(obs.ComponentSim, KernelMetrics(), s.Snapshot, MapKernel)
}

// MapKernel emits every field of a sim.Snapshot into e.
func MapKernel(k sim.Snapshot, e *obs.Emitter) {
	e.Counter("steps", none, k.Steps)
	e.Counter("scheduled", none, k.Scheduled)
	e.Counter("fired", none, k.Fired)
	e.Counter("cancelled", none, k.Cancelled)
	e.Gauge("heap_len", none, k.HeapLen)
	e.Gauge("heap_peak", none, k.HeapPeak)
	e.Gauge("pending_timers", none, k.PendingTimers)
	e.Gauge("background_timers", none, k.BackgroundTimers)
	e.Counter("trace_records", none, k.TraceRecords)
	e.Gauge("trace_ring_len", none, k.TraceRingLen)
	e.Counter("faults_applied", none, k.FaultsApplied)
	e.Counter("invariant_checks", none, k.InvariantChecks)
}

// Direction mode label values for simnet byte and chunk counters.
const (
	DirUp   = "up"
	DirDown = "down"
)

// NetMetrics returns the metric declarations for the simulated network (simnet).
func NetMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("listeners", 0, "Listeners open."),
		obs.CounterDesc("dials_attempted", 0, "Dials attempted."),
		obs.CounterDesc("dials_refused", 0, "Dials refused."),
		obs.CounterDesc("dials_stalled", 0, "Dials stalled by a fault."),
		obs.CounterDesc("conns_opened", 0, "Connections opened.").AsHeadline(),
		obs.GaugeDesc("conns_open", 0, "Connections open."),
		obs.GaugeDesc("conns_connecting", 0, "Connections still connecting."),
		obs.CounterDesc("conns_closed", lr, "Connections closed, by close reason."),
		obs.CounterDesc("bytes_written", lm, "Bytes written; mode is the direction, up (client to server) or down.").WithUnit(obs.UnitBytes),
		obs.CounterDesc("bytes_delivered", lm, "Bytes delivered to the reader, by direction.").WithUnit(obs.UnitBytes),
		obs.CounterDesc("bytes_discarded", lm, "Bytes discarded by a reset or close, by direction.").WithUnit(obs.UnitBytes),
		obs.GaugeDesc("bytes_in_flight", lm, "Bytes written and not yet delivered or discarded, by direction.").WithUnit(obs.UnitBytes),
		obs.CounterDesc("writes_refused", lm, "Writes refused by a full window, by direction."),
		obs.CounterDesc("chunks", lm, "Delivery chunks, by direction."),
		obs.CounterDesc("faults", lr, "Network faults applied to a live target, by fault."),
		obs.CounterDesc("fault_clears", 0, "Network fault clears."),
		obs.CounterDesc("faults_no_target", 0, "Network faults whose target did not exist."),
	}
}

// Net wraps n as an obs.Source under ComponentSimnet.
func Net(n *simnet.Net) obs.Source {
	return obs.NewSource(obs.ComponentSimnet, NetMetrics(), n.Snapshot, MapNet)
}

// MapNet emits every field of a simnet.Snapshot into e, including zero rows for
// every fixed close reason, direction, and network fault kind.
func MapNet(s simnet.Snapshot, e *obs.Emitter) {
	e.Gauge("listeners", none, s.Listeners)
	e.Counter("dials_attempted", none, s.DialsAttempted)
	e.Counter("dials_refused", none, s.DialsRefused)
	e.Counter("dials_stalled", none, s.DialsStalled)
	e.Counter("conns_opened", none, s.ConnsOpened)
	e.Gauge("conns_open", none, s.ConnsOpen)
	e.Gauge("conns_connecting", none, s.ConnsConnecting)
	closed := []struct {
		reason string
		n      int64
	}{{"graceful", s.Closed.Graceful}, {"reset", s.Closed.Reset}, {"half-open", s.Closed.HalfOpen},
		{"silent", s.Closed.Silent}, {"aborted", s.Closed.Aborted}}
	for _, c := range closed {
		e.Counter("conns_closed", obs.Labels{Reason: c.reason}, c.n)
	}
	for _, d := range []struct {
		mode string
		s    simnet.DirSnapshot
	}{{DirUp, s.Up}, {DirDown, s.Down}} {
		l := obs.Labels{Mode: d.mode}
		e.Counter("bytes_written", l, d.s.Written)
		e.Counter("bytes_delivered", l, d.s.Delivered)
		e.Counter("bytes_discarded", l, d.s.Discarded)
		e.Gauge("bytes_in_flight", l, d.s.InFlight)
		e.Counter("writes_refused", l, d.s.WritesRefused)
		e.Counter("chunks", l, d.s.Chunks)
	}
	f := s.Faults
	faults := []struct {
		reason string
		n      int64
	}{{"reset", f.Reset}, {"stall", f.Stall}, {"half-open", f.HalfOpen}, {"half-close", f.HalfClose}, {"chunk", f.Chunk},
		{"refuse", f.Refuse}, {"dial-stall", f.DialStall}, {"silence", f.Silence}}
	for _, x := range faults {
		e.Counter("faults", obs.Labels{Reason: x.reason}, x.n)
	}
	e.Counter("fault_clears", none, f.Clear)
	e.Counter("faults_no_target", none, f.NoTarget)
}

// FakeMetrics returns the metric declarations for the fake Postgres server (fakepg).
func FakeMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.CounterDesc("conns_accepted", 0, "Connections accepted.").AsHeadline(),
		obs.CounterDesc("conns_closed", 0, "Connections closed."),
		obs.GaugeDesc("conns_open", 0, "Connections open, sessions and dead ends together."),
		obs.HighWaterDesc("conns_open_peak", 0, "Most connections open at once."),
		obs.GaugeDesc("sessions", 0, "Sessions counted against max_connections."),
		obs.HighWaterDesc("sessions_peak", 0, "Most sessions at once; compare with max_connections.").AsHeadline(),
		obs.CounterDesc("startups_completed", 0, "Startups that reached ReadyForQuery."),
		obs.CounterDesc("refused", lr, "Connections refused at startup, by reason."),
		obs.CounterDesc("statements_started", 0, "Statements started.").AsHeadline(),
		obs.CounterDesc("statements_finished", 0, "Statements finished."),
		obs.CounterDesc("statements_aborted", 0, "Statements aborted."),
		obs.CounterDesc("statement_errors", 0, "Statements that ended in an error."),
		obs.CounterDesc("statements", lm, "Statements started, by statement kind (mode)."),
		obs.GaugeDesc("in_service", 0, "Statements in service."),
		obs.HighWaterDesc("in_service_peak", 0, "Most statements in service at once."),
		obs.GaugeDesc("waiting", 0, "Statements waiting for a capacity slot."),
		obs.HighWaterDesc("waiting_peak", 0, "Most statements waiting for a capacity slot at once."),
		obs.CounterDesc("writes_applied", 0, "Writes applied to the table."),
		obs.CounterDesc("tx_begun", 0, "Transactions begun."),
		obs.CounterDesc("tx_committed", 0, "Transactions committed."),
		obs.CounterDesc("tx_rolled_back", 0, "Transactions rolled back."),
		obs.CounterDesc("tx_lost", 0, "Transactions lost with their connection."),
		obs.GaugeDesc("tx_open", 0, "Transactions open."),
		obs.CounterDesc("cancels_received", 0, "Cancel requests received."),
		obs.CounterDesc("cancels_applied", 0, "Cancel requests that cancelled a statement."),
		obs.CounterDesc("cancels_idle", 0, "Cancel requests for an idle backend."),
		obs.CounterDesc("cancels_no_match", 0, "Cancel requests that matched no backend."),
		obs.CounterDesc("bytes_in", 0, "Bytes read from clients.").WithUnit(obs.UnitBytes),
		obs.CounterDesc("bytes_out", 0, "Bytes written to clients.").WithUnit(obs.UnitBytes),
		obs.CounterDesc("param_reports", 0, "ParameterStatus messages sent after startup."),
		obs.GaugeDesc("table_rows", 0, "Rows in the table."),
		obs.GaugeDesc("write_log_len", 0, "Entries held in the write log."),
		obs.CounterDesc("write_log_dropped", 0, "Write log entries not kept."),
		obs.GaugeDesc("statement_log_len", 0, "Entries held in the statement log."),
		obs.CounterDesc("statement_log_dropped", 0, "Statement log entries not kept."),
		obs.GaugeDesc("tx_log_len", 0, "Entries held in the transaction log."),
		obs.CounterDesc("tx_log_dropped", 0, "Transaction log entries not kept."),
		obs.CounterDesc("faults", lr, "Database faults applied, by fault kind."),
		obs.CounterDesc("faults_cleared", 0, "Database faults cleared."),
		obs.CounterDesc("faults_no_target", 0, "Database faults whose target did not exist."),
	}
}

// Fake wraps srv as an obs.Source under ComponentFakepg.
func Fake(srv *fakepg.Server) obs.Source {
	return obs.Source{Component: obs.ComponentFakepg, Metrics: fakeAll(), Collect: func(e *obs.Emitter) {
		MapFake(srv.Snapshot(), e)
		MapFakeAuth(srv.AuthSnapshot(), e)
	}}
}

func fakeAll() []obs.MetricDesc { return append(FakeMetrics(), FakeAuthMetrics()...) }

func FakeAuthMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.CounterDesc("tls_answered", lo, "Answers to a TLS request (outcome: S, N)."),
		obs.CounterDesc("gss_refused", 0, "GSS encryption requests answered N."),
		obs.CounterDesc("handshakes_started", 0, "Simulated TLS handshakes started."),
		obs.CounterDesc("handshakes", lr, "Simulated TLS handshakes by configured result (reason)."),
		obs.CounterDesc("bad_hellos", 0, "Handshakes refused for a bad hello."),
		obs.CounterDesc("scram_started", 0, "SCRAM exchanges started."),
		obs.CounterDesc("scram_ok", 0, "SCRAM exchanges that authenticated."),
		obs.CounterDesc("scram_failed", 0, "SCRAM exchanges that failed."),
		obs.CounterDesc("channel_binding_refused", 0, "SCRAM exchanges refused for their channel-binding header."),
		obs.CounterDesc("tls_changes", 0, "TLS configuration changes."),
	}
}

var handshakeResults = [fakepg.NumTLSOutcomes]string{
	fakepg.TLSCompleted: "completed", fakepg.TLSFailed: "failed", fakepg.TLSStall: "stall", fakepg.TLSBadCertificate: "bad-certificate"}

func MapFakeAuth(a fakepg.AuthSnapshot, e *obs.Emitter) {
	e.Counter("tls_answered", obs.Labels{Outcome: "S"}, a.TLSAnsweredYes)
	e.Counter("tls_answered", obs.Labels{Outcome: "N"}, a.TLSAnsweredNo)
	e.Counter("gss_refused", none, a.GSSAnsweredNo)
	e.Counter("handshakes_started", none, a.HandshakesStarted)
	for i, v := range a.HandshakesByResult {
		e.Counter("handshakes", obs.Labels{Reason: handshakeResults[i]}, v)
	}
	e.Counter("bad_hellos", none, a.BadHellos)
	e.Counter("scram_started", none, a.SCRAMStarted)
	e.Counter("scram_ok", none, a.SCRAMOK)
	e.Counter("scram_failed", none, a.SCRAMFailed)
	e.Counter("channel_binding_refused", none, a.ChannelBindingRefs)
	e.Counter("tls_changes", none, a.TLSChanges)
}

// MapFake emits every field of a fakepg.Snapshot into e, including zero rows
// for every refusal reason, statement kind, and database fault kind.
func MapFake(s fakepg.Snapshot, e *obs.Emitter) {
	e.Counter("conns_accepted", none, s.ConnsAccepted)
	e.Counter("conns_closed", none, s.ConnsClosed)
	e.Gauge("conns_open", none, s.ConnsOpen.Value)
	e.Gauge("conns_open_peak", none, s.ConnsOpen.Peak)
	e.Gauge("sessions", none, s.Sessions.Value)
	e.Gauge("sessions_peak", none, s.Sessions.Peak)
	e.Counter("startups_completed", none, s.StartupsCompleted)
	r := s.Refused
	refused := []struct {
		reason string
		n      int64
	}{{"too-many", r.TooMany}, {"unknown-user", r.UnknownUser}, {"unknown-database", r.UnknownDatabase},
		{"no-user", r.NoUser}, {"bad-startup", r.BadStartup}, {"starting-up", r.StartingUp}, {"protocol", r.Protocol}}
	for _, x := range refused {
		e.Counter("refused", obs.Labels{Reason: x.reason}, x.n)
	}
	e.Counter("statements_started", none, s.StatementsStarted)
	e.Counter("statements_finished", none, s.StatementsFinished)
	e.Counter("statements_aborted", none, s.StatementsAborted)
	e.Counter("statement_errors", none, s.StatementErrors)
	for k := fakepg.Kind(0); k < fakepg.NumKinds; k++ {
		e.Counter("statements", obs.Labels{Mode: k.String()}, s.StatementsByKind[k])
	}
	e.Gauge("in_service", none, s.InService.Value)
	e.Gauge("in_service_peak", none, s.InService.Peak)
	e.Gauge("waiting", none, s.Waiting.Value)
	e.Gauge("waiting_peak", none, s.Waiting.Peak)
	e.Counter("writes_applied", none, s.WritesApplied)
	e.Counter("tx_begun", none, s.TxBegun)
	e.Counter("tx_committed", none, s.TxCommitted)
	e.Counter("tx_rolled_back", none, s.TxRolledBack)
	e.Counter("tx_lost", none, s.TxLost)
	e.Gauge("tx_open", none, s.TxOpen)
	e.Counter("cancels_received", none, s.CancelsReceived)
	e.Counter("cancels_applied", none, s.CancelsApplied)
	e.Counter("cancels_idle", none, s.CancelsIdle)
	e.Counter("cancels_no_match", none, s.CancelsNoMatch)
	e.Counter("bytes_in", none, s.BytesIn)
	e.Counter("bytes_out", none, s.BytesOut)
	e.Counter("param_reports", none, s.ParamReports)
	e.Gauge("table_rows", none, s.TableRows)
	e.Gauge("write_log_len", none, s.WriteLogLen)
	e.Counter("write_log_dropped", none, s.WriteLogDropped)
	e.Gauge("statement_log_len", none, s.StatementLogLen)
	e.Counter("statement_log_dropped", none, s.StatementLogDropped)
	e.Gauge("tx_log_len", none, s.TxLogLen)
	e.Counter("tx_log_dropped", none, s.TxLogDropped)
	for _, k := range fakepg.FaultKinds() {
		e.Counter("faults", obs.Labels{Reason: fakepg.FaultName(k)}, s.Fault(k))
	}
	e.Counter("faults_cleared", none, s.Faults.Cleared)
	e.Counter("faults_no_target", none, s.Faults.NoTarget)
}

// WorkloadMetrics returns the metric declarations for the simulated clients (workload).
func WorkloadMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("clients", 0, "Simulated clients.").AsHeadline(),
		obs.GaugeDesc("finished", 0, "Simulated clients that finished."),
		obs.CounterDesc("faults", lr, "Client faults applied, by fault kind."),
		obs.CounterDesc("faults_no_target", 0, "Client faults whose target did not exist."),
		obs.CounterDesc("intended", lt, "Requests whose arrival event fired (intended sends)."),
		obs.CounterDesc("sent", lt, "Requests whose first statement was written."),
		obs.GaugeDesc("not_yet_sent", lt, "Requests waiting in client backlogs."),
		obs.HighWaterDesc("not_yet_sent_peak", lt, "Most requests waiting in one tenant's client backlogs at once."),
		obs.GaugeDesc("in_flight", lt, "Requests sent and not finished."),
		obs.HighWaterDesc("in_flight_peak", lt, "Most requests of one tenant in flight at once."),
		obs.CounterDesc("dropped", lt, "Requests dropped because a client backlog was full."),
		obs.CounterDesc("failed_before_send", lt, "Requests that failed before being sent (connect failed)."),
		obs.CounterDesc("requests", lt|lo, "Sent requests by final outcome."),
		obs.CounterDesc("errors_by_code", lt|lr, "Errors by SQLSTATE (reason), at most 64 codes per tenant."),
		obs.CounterDesc("errors_other_codes", lt, "Errors whose SQLSTATE did not fit in the 64 tracked codes."),
		obs.CounterDesc("retries", lt, "Request retries."),
		obs.CounterDesc("connects", lt, "Connects attempted."),
		obs.CounterDesc("connects_ok", lt, "Connects that reached ReadyForQuery."),
		obs.CounterDesc("connects_failed", lt, "Connects that failed."),
		obs.CounterDesc("reconnects", lt, "Reconnects after a lost connection."),
		obs.GaugeDesc("conns_open", lt, "Client connections open."),
		obs.HighWaterDesc("conns_open_peak", lt, "Most client connections of one tenant open at once."),
		obs.CounterDesc("half_messages", lt, "Half messages sent by the half-message fault."),
		obs.CounterDesc("stopped_reading", lt, "Stop-reading faults applied."),
		obs.CounterDesc("cancels_sent", lt, "Cancel requests sent."),
		obs.CounterDesc("disconnects", lt, "Disconnects by client fault."),
		obs.CounterDesc("burst_drops", lt, "Connections dropped by the reconnect burst."),
		obs.CounterDesc("startup_tls_requests", lt, "TLS requests sent before startup."),
		obs.CounterDesc("startup_tls_answered", lt|lo, "Answers to the TLS request (outcome: S, N)."),
		obs.CounterDesc("startup_handshakes", lt|lo, "Simulated TLS handshakes by result (outcome: ok, failed)."),
		obs.CounterDesc("startup_tls_faults", lt|lr, "Client TLS faults injected, by fault kind (reason)."),
		obs.CounterDesc("startup_scram_started", lt, "SCRAM exchanges started."),
		obs.CounterDesc("startup_scram", lt|lo, "SCRAM exchanges by result (outcome: ok, failed)."),
		obs.HistogramDesc("latency", lt, obs.UnitNs, "Request latency from the intended send time."),
		obs.HistogramDesc("latency_from_send", lt, obs.UnitNs, "Request latency from the last send (coordinated-omission control)."),
	}
}

// Workload wraps w as an obs.Source under ComponentWorkload.
func Workload(w *workload.Workload) obs.Source {
	return obs.NewSource(obs.ComponentWorkload, WorkloadMetrics(), w.Snapshot, MapWorkload)
}

var outcomes = []workload.Outcome{workload.OutcomeOK, workload.OutcomeError, workload.OutcomeLost, workload.OutcomeTimedOut, workload.OutcomeClientLeft}

// MapWorkload emits client counts, fault counts, and each tenant's metrics into e.
// Workload.Total is omitted because it is the sum over tenants, which readers
// compute or obtain automatically when obs.detail is off.
func MapWorkload(s workload.Snapshot, e *obs.Emitter) {
	e.Gauge("clients", none, s.Clients)
	e.Gauge("finished", none, s.Finished)
	for _, k := range workload.FaultKinds() {
		i := int(k - workload.FaultHalfMessage)
		if i < len(s.Faults.ByKind) {
			e.Counter("faults", obs.Labels{Reason: workload.FaultName(k)}, s.Faults.ByKind[i])
		}
	}
	e.Counter("faults_no_target", none, s.Faults.NoTarget)
	for i, t := range s.Tenants {
		t.Name = TenantLabel(s.Tenants, i)
		mapTenant(t, e)
	}
}

// TenantLabel disambiguates duplicate tenant names in ts by appending "#<index>"
// to later occurrences so the emitter never rejects them as duplicate points.
func TenantLabel(ts []workload.TenantSnapshot, i int) string {
	for j := 0; j < i; j++ {
		if ts[j].Name == ts[i].Name {
			return ts[i].Name + "#" + strconv.Itoa(i)
		}
	}
	return ts[i].Name
}

func mapTenant(t workload.TenantSnapshot, e *obs.Emitter) {
	l := obs.Labels{Tenant: t.Name}
	e.Counter("intended", l, t.Intended)
	e.Counter("sent", l, t.Sent)
	e.Gauge("not_yet_sent", l, t.NotYetSent.Value)
	e.Gauge("not_yet_sent_peak", l, t.NotYetSent.Peak)
	e.Gauge("in_flight", l, t.InFlight.Value)
	e.Gauge("in_flight_peak", l, t.InFlight.Peak)
	e.Counter("dropped", l, t.Dropped)
	e.Counter("failed_before_send", l, t.FailedBeforeSend)
	counts := []int64{t.OK, t.Errors, t.Lost, t.TimedOut, t.ClientLeft}
	for i, o := range outcomes {
		e.Counter("requests", obs.Labels{Tenant: t.Name, Outcome: o.String()}, counts[i])
	}
	for _, c := range t.ErrorsByCode {
		e.Counter("errors_by_code", obs.Labels{Tenant: t.Name, Reason: c.Code}, c.Count)
	}
	e.Counter("errors_other_codes", l, t.ErrorsOtherCodes)
	e.Counter("retries", l, t.Retries)
	e.Counter("connects", l, t.Connects)
	e.Counter("connects_ok", l, t.ConnectsOK)
	e.Counter("connects_failed", l, t.ConnectsFailed)
	e.Counter("reconnects", l, t.Reconnects)
	e.Gauge("conns_open", l, t.ConnsOpen.Value)
	e.Gauge("conns_open_peak", l, t.ConnsOpen.Peak)
	e.Counter("half_messages", l, t.HalfMessages)
	e.Counter("stopped_reading", l, t.StoppedReading)
	e.Counter("cancels_sent", l, t.CancelsSent)
	e.Counter("disconnects", l, t.Disconnects)
	e.Counter("burst_drops", l, t.BurstDrops)
	mapStartup(t.Name, t.Startup, e)
	if e.Detail() {
		e.Histogram("latency", l, t.Latency)
		e.Histogram("latency_from_send", l, t.LatencyFromSend)
	}
}

func mapStartup(tenant string, s workload.StartupCounters, e *obs.Emitter) {
	l := obs.Labels{Tenant: tenant}
	e.Counter("startup_tls_requests", l, s.TLSRequests)
	e.Counter("startup_tls_answered", obs.Labels{Tenant: tenant, Outcome: "S"}, s.TLSAnsweredYes)
	e.Counter("startup_tls_answered", obs.Labels{Tenant: tenant, Outcome: "N"}, s.TLSAnsweredNo)
	e.Counter("startup_handshakes", obs.Labels{Tenant: tenant, Outcome: "ok"}, s.HandshakesOK)
	e.Counter("startup_handshakes", obs.Labels{Tenant: tenant, Outcome: "failed"}, s.HandshakesFailed)
	for i, v := range s.TLSFaults {
		e.Counter("startup_tls_faults", obs.Labels{Tenant: tenant, Reason: workload.TLSFault(i).String()}, v)
	}
	e.Counter("startup_scram_started", l, s.SCRAMStarted)
	e.Counter("startup_scram", obs.Labels{Tenant: tenant, Outcome: "ok"}, s.SCRAMOK)
	e.Counter("startup_scram", obs.Labels{Tenant: tenant, Outcome: "failed"}, s.SCRAMFailed)
}

// Register registers every source in sources with c, returning the first error.
func Register(c *obs.Collector, sources ...obs.Source) error {
	for _, s := range sources {
		if err := c.Register(s); err != nil {
			return err
		}
	}
	return nil
}

// Declared pairs a component name with its full slice of MetricDesc declarations
// for assembly in internal/obs/catalog.
type Declared struct {
	Component string
	Metrics   []obs.MetricDesc
}

// All returns the declarations of all four simulator components (fakepg,
// workload, simnet, sim).
func All() []Declared {
	return []Declared{
		{obs.ComponentFakepg, fakeAll()},
		{obs.ComponentWorkload, WorkloadMetrics()},
		{obs.ComponentSimnet, NetMetrics()},
		{obs.ComponentSim, KernelMetrics()},
	}
}
