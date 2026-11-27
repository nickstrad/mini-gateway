package proxy

import (
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pool"
)

// TimeoutRow describes one wait rendered into the generated docs/timeout-budget.md table.
type TimeoutRow struct {
	Wait  string
	Where string
	Key   string
	Value time.Duration
	Ends  string
}

// TimeoutRows returns the ordered timeout budget rows for documentation generation
// and contract verification.
func TimeoutRows(t Timeouts) []TimeoutRow {
	return append([]TimeoutRow{
		{"Client startup: the TLS request exchange, the TLS handshake and authentication, together", "engine, client session (armed at accept)", "timeouts.client_startup", t.ClientStartup, "Connection closed (FATAL 08P01 during authentication); a TLS handshake past it is closed with no message"},
		{"Client mid-message read (a partial message)", "engine, query phase (armed while a message is incomplete)", "timeouts.client_mid_message", t.ClientMidMessage, "FATAL 08P01 (protocol-violation), connection closed"},
		{"Client idle between transactions", "engine, client with no request in progress", "timeouts.client_idle", t.ClientIdle, "Connection closed"},
		{"Client idle inside a transaction", "engine, client holding a lease between statements (pool core)", "timeouts.idle_in_transaction", t.IdleInTx, "ROLLBACK on the database; FATAL 25P03 (idle-in-transaction), client closed; the connection is reset and reused"},
		{"Queue wait (waiting for a connection: no separate wait, D3)", "engine, queue stage", "timeouts.queue_wait", t.QueueWait, "ERROR 53M01 (shed), connection stays usable"},
		{"Database connect: dial, TLS handshake when configured, authentication", "engine, fixed set (the shell dials with the same deadline)", "timeouts.database_connect", t.DBConnect, "Connection closed, counted by class; the slot is dialled again after the redial delay"},
		{"Pause before dialling again after a failed connect", "engine, fixed set", "timeouts.redial_delay", t.RedialDelay, "The next dial (at most one at a time)"},
		{"Database statement (send to end of reply)", "engine, lease", "timeouts.statement", t.Statement, "ERROR 08006 or 08007 (FATAL 08006 inside a transaction unless the message holds COMMIT or END), or the client is closed if part of the reply was sent; with the pool core the connection goes to the dirty-connection table (cancel, then clean-up), with the fixed set it is closed"},
		{"Cancel and clean-up of a dirty connection (cancel on a second connection, the end of the reply, ROLLBACK)", "pool core, dirty-connection table", "timeouts.cleanup", t.Cleanup, "Database connection closed (cleanup_failed, or no_reply when the statement had no reply)"},
		{"Check before reuse (an empty query after pool.check_after_idle idle, or on a suspect connection)", "pool core, checkout (off the request path)", "pool.check_timeout", t.CheckTimeout, "Database connection closed (check_failed); no client error; counts toward the breaker"},
		{"Reset after a lease (DISCARD ALL until its ReadyForQuery)", "engine, release stage", "timeouts.reset", t.Reset, "Database connection closed (reset failed: deadline); a late reply is never read"},
		{"Client write stalled (reply bytes not taken by the client)", "engine, relay (the shell sets the same per-write socket deadline)", "timeouts.client_write", t.ClientWrite, "Client dropped; its lent database connection closed"},
		{"Socket read backstop", "shell, every reader", "timeouts.client_idle + timeouts.statement", t.ClientIdle + t.Statement, "Connection closed; the engine's own timers end every real wait first"},
		{"Idle database connection", "pool core, per connection (jitter ±pool.jitter_percent)", "pool.idle_timeout", pool.DefaultIdleTimeout, "Connection closed (idle); must be below backend.path_idle_limit"},
		{"Database connection lifetime", "pool core, at release and while idle, never mid-lease (jitter ±pool.jitter_percent)", "pool.max_lifetime", pool.DefaultMaxLifetime, "Connection closed (lifetime)"},
		{"Idle age that makes the next lend check first", "pool core, checkout", "pool.check_after_idle", pool.DefaultCheckAfterIdle, "The connection is checked before it is lent"},
		{"Breaker open before the half-open trial", "pool core, breaker", "breaker.open_for", pool.DefaultBreakerOpenFor, "Half-open: the next refused request starts one trial (a dial, or a check when the pool is at its cap)"},
		{"TCP keepalive on database sockets: idle before the first probe", "shell, database dialer", "backend.tcp_keepalive.idle", 30 * time.Second, "Probes every backend.tcp_keepalive.interval (10s), backend.tcp_keepalive.count (3) unanswered probes report the socket dead; validation: at most backend.path_idle_limit / 2"},
		{"Shortest idle timeout of anything between proxy and database (the operator's statement)", "config validation", "backend.path_idle_limit", 240 * time.Second, "Not a timer: pool.idle_timeout must be below it and backend.tcp_keepalive.idle at most half of it"},
	}, admissionTimeoutRows()...)
}

func admissionTimeoutRows() []TimeoutRow {
	a := admission.Config{}.WithDefaults()
	d := func(v admission.Time) time.Duration { return time.Duration(v) }
	return []TimeoutRow{
		{"Queue wait under the timeout rule, no standing queue (the tenant queue was empty within the last interval)", "admission, tenant queue (head sweep at the deadline)", "queue.interval", d(a.Interval), "ERROR 53M01 (shed, reason rule), answered at once; a request waiting exactly the allowed time has expired"},
		{"Queue wait under the timeout rule while a standing queue exists", "admission, tenant queue (head sweep at the deadline)", "queue.target", d(a.Target), "ERROR 53M01 (shed, reason rule), answered at once"},
		{"Control law: sojourn above target before dropping starts; then the next drop after interval/sqrt(count)", "admission, tenant queue (head sweep at the drop time)", "queue.target, queue.interval", d(a.Interval), "ERROR 53M01 (shed, reason rule) for the oldest request at each drop; no per-request bound (timeouts.queue_wait and the capacity bound it)"},
		{"Adaptive LIFO hold: newest-first service ends only after the rule reported no overload for this long", "admission, tenant queue", "queue.lifo_hold", d(a.LIFOHold), "Service returns to oldest first"},
		{"Quiet tenant queue removal (16 intervals empty)", "admission", "16 x queue.interval", d(a.Quiet()), "The tenant queue and its rule state are removed"},
	}
}

// CapacityRow describes one bounded structure rendered into the generated docs/capacity-table.md.
type CapacityRow struct {
	Thing    string
	Protects string
	Capacity string
	Key      string
	Full     string
	Deadline string
	Gauge    string
}

func itoa(n int) string { return strconv.Itoa(n) }

func configured(def string) string { return def + "; zero means the default" }

// CapacityRows returns the engine's bounded-resource rows for docs/capacity-table.md.
func CapacityRows(c Config) []CapacityRow {
	c = c.WithDefaults()
	a := c.admissionConfig()
	return []CapacityRow{
		{"Client connections", "proxy memory and file descriptors", configured(itoa(c.MaxClients)), "limits.max_clients", "FATAL 53300 (too-many-clients), connection closed", "timeouts.client_startup", "engine.clients_connected"},
		{"Clients in startup", "handshake and SCRAM CPU during a reconnection storm", configured(itoa(c.MaxStartup)), "limits.max_startup", "FATAL 53300 (too-many-clients), connection closed", "timeouts.client_startup", "engine.clients_in_startup"},
		{"Database connections, open plus connecting (pool core; the fleet share of fleet.backend_connections)", "the database's connection slots", configured(itoa(c.Connections)), "pool.connections", "Requests wait in the queue; a checkout at the cap counts as exhausted", "timeouts.database_connect", "pool.open_plus_connecting"},
		{"Database connections being opened at once", "the database's fork and authentication load", configured(itoa(pool.DefaultMaxConnecting)), "pool.max_connecting", "The next dial waits", "timeouts.database_connect", "pool.connecting"},
		{"Connection creation rate (dials per second, one per 1/rate; the fleet share of fleet.connect_rate_per_s)", "the database during a cold start, a mass death and after a breaker trial", configured(itoa(10)) + " per second", "fleet.connect_rate_per_s", "The next dial waits for its slot", "-", "pool.rate_limited"},
		{"Concurrent cancel requests", "the database's connection slots (each cancel is a second connection) and the reserve of fleet.backend_max_connections", configured(itoa(pool.DefaultMaxCancels)), "pool.max_cancels", "A client cancel is dropped (counted); a dirty connection that needs a cancel is closed (cancel_cap)", "timeouts.cleanup", "pool.cancels_in_flight"},
		{"Queue, total across the tenant queues (one FIFO in the test control arm)", "proxy memory; the wait bound", configured(itoa(c.QueueCap)), "queue.capacity", "The oldest queued request is shed: ERROR 53M01 (reason total), connection stays usable", "timeouts.queue_wait", "admission.queued"},
		{"Queue per tenant (a fixed-capacity ring)", "one tenant's share of the queue", configured(itoa(a.TenantCapacity)) + "; at most queue.capacity", "queue.tenant_capacity", "The tenant's oldest request is shed: ERROR 53M01 (reason overflow)", "queue.interval / queue.target", "admission.tenant_depth"},
		{"Tenant queues (active-tenant states)", "proxy memory: rings are tenant capacity x this cap", configured(itoa(a.MaxTenants)), "queue.max_tenants", "An empty tenant queue is evicted; with none empty the arrival is shed: ERROR 53M01 (reason tenants)", "16 x queue.interval (quiet removal)", "admission.tenants"},
		{"Per-tenant admission counters kept for export", "proxy memory", itoa(admission.MaxTenantStats), "(constant)", "Further tenants are counted under the tenant label other", "-", "admission.tenant_pushes"},
		{"Per-client reply buffer", "proxy memory under a slow client", configured(itoa(c.ReplyBufferCap) + " bytes"), "limits.reply_buffer", "Reading that database connection is paused", "timeouts.client_write", "engine.relay_buffer_high_water"},
		{"Unprocessed client input", "proxy memory under a pipelining client", itoa(maxClientInput) + " bytes", "(constant)", "FATAL 08P01, connection closed", "timeouts.client_mid_message", "-"},
		{"Held error or end-marker message of a reply", "proxy memory", "database max message + 5 bytes", "(constant)", "Database connection closed as a protocol violation", "timeouts.statement", "-"},
		{"Reset reply", "proxy memory", itoa(MaxResetReply) + " bytes", "(constant)", "Reset fails, connection closed", "timeouts.reset", "engine.resets_failed"},
	}
}
