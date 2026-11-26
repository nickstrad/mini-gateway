package proxy

import (
	"strconv"
	"time"
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
	return []TimeoutRow{
		{"Client startup: the TLS request exchange, the TLS handshake and authentication, together", "engine, client session (armed at accept)", "timeouts.client_startup", t.ClientStartup, "Connection closed (FATAL 08P01 during authentication); a TLS handshake past it is closed with no message"},
		{"Client mid-message read (a partial message)", "engine, query phase (armed while a message is incomplete)", "timeouts.client_mid_message", t.ClientMidMessage, "FATAL 08P01 (protocol-violation), connection closed"},
		{"Client idle between transactions (and, until chunk 7, inside one)", "engine, client with no request in progress", "timeouts.client_idle", t.ClientIdle, "Connection closed; a lease it held is closed with its database connection"},
		{"Queue wait (waiting for a connection: no separate wait, D3)", "engine, queue stage", "timeouts.queue_wait", t.QueueWait, "ERROR 53M01 (shed), connection stays usable"},
		{"Database connect: dial, TLS handshake when configured, authentication", "engine, fixed set (the shell dials with the same deadline)", "timeouts.database_connect", t.DBConnect, "Connection closed, counted by class; the slot is dialled again after the redial delay"},
		{"Pause before dialling again after a failed connect", "engine, fixed set", "timeouts.redial_delay", t.RedialDelay, "The next dial (at most one at a time)"},
		{"Database statement (send to end of reply)", "engine, lease", "timeouts.statement", t.Statement, "Database connection closed; ERROR 08006 or 08007 (FATAL 08006 inside a transaction when nothing reached the database), or the client is closed if part of the reply was sent"},
		{"Reset after a lease (DISCARD ALL until its ReadyForQuery)", "engine, release stage", "timeouts.reset", t.Reset, "Database connection closed (reset failed: deadline); a late reply is never read"},
		{"Client write stalled (reply bytes not taken by the client)", "engine, relay (the shell sets the same per-write socket deadline)", "timeouts.client_write", t.ClientWrite, "Client dropped; its lent database connection closed"},
		{"Socket read backstop", "shell, every reader", "timeouts.client_idle + timeouts.statement", t.ClientIdle + t.Statement, "Connection closed; the engine's own timers end every real wait first"},
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
	return []CapacityRow{
		{"Client connections", "proxy memory and file descriptors", configured(itoa(c.MaxClients)), "limits.max_clients", "FATAL 53300 (too-many-clients), connection closed", "timeouts.client_startup", "engine.clients_connected"},
		{"Clients in startup", "handshake and SCRAM CPU during a reconnection storm", configured(itoa(c.MaxStartup)), "limits.max_startup", "FATAL 53300 (too-many-clients), connection closed", "timeouts.client_startup", "engine.clients_in_startup"},
		{"Database connections (the fixed set)", "the database's connection slots", configured(itoa(c.Connections)), "pool.connections", "Requests wait in the queue", "timeouts.database_connect", "engine.database_open"},
		{"Database connections being opened", "the database's fork rate", "1", "(fixed in chunk 6)", "The next dial waits", "timeouts.database_connect", "engine.database_connecting"},
		{"Queue (one FIFO, keyed by tenant in its API)", "proxy memory; the wait bound", configured(itoa(c.QueueCap)), "queue.capacity", "ERROR 53M01 (shed), connection stays usable", "timeouts.queue_wait", "engine.requests_queued"},
		{"Per-client reply buffer", "proxy memory under a slow client", configured(itoa(c.ReplyBufferCap) + " bytes"), "limits.reply_buffer", "Reading that database connection is paused", "timeouts.client_write", "engine.relay_buffer_high_water"},
		{"Unprocessed client input", "proxy memory under a pipelining client", itoa(maxClientInput) + " bytes", "(constant)", "FATAL 08P01, connection closed", "timeouts.client_mid_message", "-"},
		{"Held error or end-marker message of a reply", "proxy memory", "database max message + 5 bytes", "(constant)", "Database connection closed as a protocol violation", "timeouts.statement", "-"},
		{"Reset reply", "proxy memory", itoa(MaxResetReply) + " bytes", "(constant)", "Reset fails, connection closed", "timeouts.reset", "engine.resets_failed"},
	}
}
