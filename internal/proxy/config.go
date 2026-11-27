package proxy

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// CloseReason classifies why the engine closed a client or database connection.
type CloseReason uint8

const (
	// CloseNone is the zero value indicating no close reason.
	CloseNone CloseReason = iota
	// CloseClientGone records a peer-initiated close or reset on the client leg.
	CloseClientGone
	// CloseClientTerminate records a clean Postgres Terminate ('X') from the client.
	CloseClientTerminate
	// CloseClientStartup records a client closed during TLS negotiation or startup.
	CloseClientStartup
	// CloseClientViolation records a client closed for framing or buffer violation.
	CloseClientViolation
	// CloseClientIdle records a client closed after exceeding timeouts.client_idle.
	CloseClientIdle
	// CloseClientMidMessage records a client closed for an incomplete message deadline.
	CloseClientMidMessage
	// CloseClientWriteStalled records a slow client dropped at timeouts.client_write.
	CloseClientWriteStalled
	// CloseClientPartialReply records a client closed because the database failed after
	// part of the reply had already been relayed.
	CloseClientPartialReply
	// CloseClientLostInTx records a client closed with FATAL 08006 after losing its
	// held database connection inside a transaction.
	CloseClientLostInTx
	// CloseClientTooMany records a client refused at limits.max_clients or limits.max_startup.
	CloseClientTooMany
	// CloseClientDraining records a client closed after a draining refusal (53M05).
	CloseClientDraining
	// CloseClientCancelRequest records a client connection that carried a CancelRequest.
	CloseClientCancelRequest
	// CloseDBConnectFailed records a database dial or startup failure.
	CloseDBConnectFailed
	// CloseDBConnectDeadline records a database dial or startup exceeding timeouts.database_connect.
	CloseDBConnectDeadline
	// CloseDBLost records an unexpected close or reset from the database.
	CloseDBLost
	// CloseDBStatementDeadline records a database connection closed at timeouts.statement.
	CloseDBStatementDeadline
	// CloseDBClientVanished records a lent database connection destroyed because its client left.
	CloseDBClientVanished
	// CloseDBResetFailed records a database connection closed because DISCARD ALL failed.
	CloseDBResetFailed
	// CloseDBReleaseDirty records a database connection closed at release because status
	// was not 'I' or unread bytes remained after ReadyForQuery.
	CloseDBReleaseDirty
	// CloseDBUnsolicited records an idle database connection closed on unexpected bytes.
	CloseDBUnsolicited
	// CloseDBViolation records a database connection closed on a wire framing violation.
	CloseDBViolation
	// CloseDBServerFatal records a database connection closed on a FATAL/PANIC ErrorResponse.
	CloseDBServerFatal
	// CloseDBWriteStalled records a database connection closed on a write stall.
	CloseDBWriteStalled
	// CloseClientShutdown records a client closed by Engine.Shutdown.
	CloseClientShutdown
	// CloseDBShutdown records a database connection closed by Engine.Shutdown.
	CloseDBShutdown
	// CloseClientIdleInTx records a client closed for exceeding timeouts.idle_in_transaction.
	CloseClientIdleInTx
	// CloseDBIdle records a pooled database connection closed at its jittered idle timeout.
	CloseDBIdle
	// CloseDBLifetime records a pooled database connection closed at its jittered max lifetime.
	CloseDBLifetime
	// CloseDBCheckFailed records a database connection closed after failing a pre-lend check.
	CloseDBCheckFailed
	// CloseDBNoReply records a database connection closed after a zero-reply statement deadline.
	CloseDBNoReply
	// CloseDBCleanupFailed records a dirty database connection closed after clean-up failed or timed out.
	CloseDBCleanupFailed
	// CloseDBCancelCap records a dirty database connection closed because pool.max_cancels was reached.
	CloseDBCancelCap
	// NumCloseReasons is the cardinality of CloseReason for fixed-size metric arrays.
	NumCloseReasons
)

var closeNames = [NumCloseReasons]string{"none", "client-gone", "client-terminate", "client-startup",
	"client-protocol-violation", "client-idle", "client-mid-message", "client-write-stalled", "partial-reply",
	"lost-in-transaction", "too-many-clients", "draining", "cancel-request", "connect-failed", "connect-deadline",
	"connection-lost", "statement-deadline", "client-vanished", "reset-failed", "release-dirty", "unsolicited-bytes",
	"protocol-violation", "server-fatal", "database-write-stalled", "client-shutdown", "database-shutdown",
	"idle-in-transaction", "idle-timeout", "lifetime", "check-failed", "no-reply", "cleanup-failed", "cancel-cap"}

// String returns the canonical metric label name for the close reason.
func (r CloseReason) String() string {
	if r < NumCloseReasons {
		return closeNames[r]
	}
	return "?"
}

// ClientSide reports whether r classifies a client-leg close rather than a database-leg close.
func (r CloseReason) ClientSide() bool {
	return (r >= CloseClientGone && r <= CloseClientCancelRequest) || r == CloseClientShutdown || r == CloseClientIdleInTx
}

// ResetPolicy selects the lease-release reset behaviour (decision D8).
type ResetPolicy uint8

const (
	// ResetAlways sends DISCARD ALL after every cleanly ended lease.
	ResetAlways ResetPolicy = iota + 1
	// ResetOnSwitch tags a cleanly released connection with its last tenant and
	// resets it on checkout only when switching to another tenant.
	ResetOnSwitch
)

// String returns the config string for the reset policy.
func (p ResetPolicy) String() string {
	switch p {
	case ResetAlways:
		return "always"
	case ResetOnSwitch:
		return "on-switch"
	}
	return "?"
}

// Plant is a bitmask of test-only fault-injection switches used for canaries,
// ablations, and fuzz verification. It is never exposed through internal/config.
type Plant uint16

const (
	// PlantReleaseOnAnyReady releases a connection on any ReadyForQuery status byte
	// (canary for transaction integrity).
	PlantReleaseOnAnyReady Plant = 1 << iota
	// PlantLendBeforeResetReply returns a connection to idle immediately after writing
	// DISCARD ALL without waiting for its reply (canary for reset verification).
	PlantLendBeforeResetReply
	// PlantNoReset skips DISCARD ALL at release (ablation proving state crosses without reset).
	PlantNoReset
	// PlantQueuedVanishKeepsSlot leaks a queue slot when a queued client closes
	// (ordering bug that FuzzEngineOps must find within the gate budget).
	PlantQueuedVanishKeepsSlot
	// PlantSkipCourtesy bypasses the courtesy reject list so session-mutating statements
	// reach the fake database in tier S reset tests.
	PlantSkipCourtesy
	// PlantBreakerFlushPops flushes queued requests on breaker open via Queue.Pop
	// instead of ShedQueue.Refuse (canary proving admission/engine shed invariants catch it).
	PlantBreakerFlushPops
)

// Timeouts configures every deadline enforced by the engine (docs/timeout-budget.md).
type Timeouts struct {
	ClientStartup    time.Duration
	ClientMidMessage time.Duration
	ClientIdle       time.Duration
	QueueWait        time.Duration
	DBConnect        time.Duration
	Statement        time.Duration
	Reset            time.Duration
	ClientWrite      time.Duration
	RedialDelay      time.Duration
	IdleInTx         time.Duration
	Cleanup          time.Duration
	CheckTimeout     time.Duration
}

// Default timeout budget values (PLAN.md section 9.2).
const (
	DefaultClientStartup    = 5 * time.Second
	DefaultClientMidMessage = 5 * time.Second
	DefaultClientIdle       = 10 * time.Minute
	DefaultQueueWait        = 5 * time.Second
	DefaultDBConnect        = 2 * time.Second
	DefaultStatement        = 30 * time.Second
	DefaultReset            = time.Second
	DefaultClientWrite      = 5 * time.Second
	DefaultRedialDelay      = 100 * time.Millisecond
	DefaultIdleInTx         = 5 * time.Second
	DefaultCleanup          = time.Second
	DefaultCheckTimeout     = 250 * time.Millisecond
)

// DefaultTimeouts returns the initial timeout budget before tuning.
func DefaultTimeouts() Timeouts {
	return Timeouts{ClientStartup: DefaultClientStartup, ClientMidMessage: DefaultClientMidMessage,
		ClientIdle: DefaultClientIdle, QueueWait: DefaultQueueWait, DBConnect: DefaultDBConnect,
		Statement: DefaultStatement, Reset: DefaultReset, ClientWrite: DefaultClientWrite,
		RedialDelay: DefaultRedialDelay, IdleInTx: DefaultIdleInTx, Cleanup: DefaultCleanup, CheckTimeout: DefaultCheckTimeout}
}

// Default and hard-limit capacity constants (docs/capacity-table.md).
const (
	DefaultConnections    = 4
	DefaultQueueCap       = 256
	DefaultMaxClients     = 1024
	DefaultMaxStartup     = 128
	DefaultReplyBufferCap = 256 << 10
	MaxConnections        = 4096
	MaxQueueCap           = 1 << 20
	MaxClients            = 1 << 16
	MaxPipelined          = 64
	MaxResetReply         = 4096
	MinReplyBufferCap     = 2 * MaxRead
	MaxReplyBufferCap     = 64 << 20
)

// Config holds the engine's capacities, timeouts, wire configurations, stage
// seams, and test-only Plant switches.
type Config struct {
	Client         pgwire.ClientConfig
	Backend        pgwire.BackendConfig
	Connections    int
	QueueCap       int
	MaxClients     int
	MaxStartup     int
	ReplyBufferCap int
	Timeouts       Timeouts
	Reset          ResetPolicy
	KeyScope       work.KeyScope
	Seams          Seams
	Plant          Plant
	Pool           *pool.Config
	Reseed         bool
	Admission      admission.Config
	Coalesce       coalesce.Config
}

func (c Config) admissionConfig() admission.Config {
	a := c.Admission
	a.Capacity = c.QueueCap
	return a.WithDefaults()
}

// AdmissionConfig returns the effective admission queue configuration derived
// from c with defaults applied and Capacity bound to QueueCap.
func (c Config) AdmissionConfig() admission.Config { return c.WithDefaults().admissionConfig() }

// ErrConfig is wrapped by all engine configuration validation errors.
var ErrConfig = errors.New("proxy: bad configuration")

// WithDefaults returns a copy of c with any zero capacity, timeout, or reset
// policy replaced by its documented default. Negative values are left untouched
// so Validate fails closed on them.
func (c Config) WithDefaults() Config {
	if c.Connections == 0 {
		c.Connections = DefaultConnections
	}
	if c.QueueCap == 0 {
		c.QueueCap = DefaultQueueCap
	}
	if c.MaxClients == 0 {
		c.MaxClients = DefaultMaxClients
	}
	if c.MaxStartup == 0 {
		c.MaxStartup = DefaultMaxStartup
	}
	if c.ReplyBufferCap == 0 {
		c.ReplyBufferCap = DefaultReplyBufferCap
	}
	if c.Reset == 0 {
		c.Reset = ResetAlways
	}
	d := DefaultTimeouts()
	t := &c.Timeouts
	for _, p := range []struct {
		v   *time.Duration
		def time.Duration
	}{{&t.ClientStartup, d.ClientStartup}, {&t.ClientMidMessage, d.ClientMidMessage}, {&t.ClientIdle, d.ClientIdle},
		{&t.QueueWait, d.QueueWait}, {&t.DBConnect, d.DBConnect}, {&t.Statement, d.Statement}, {&t.Reset, d.Reset},
		{&t.ClientWrite, d.ClientWrite}, {&t.RedialDelay, d.RedialDelay}, {&t.IdleInTx, d.IdleInTx}, {&t.Cleanup, d.Cleanup},
		{&t.CheckTimeout, d.CheckTimeout}} {
		if *p.v == 0 {
			*p.v = p.def
		}
	}
	c.Coalesce = c.coalesceDefaults()
	return c
}

// Validate checks capacities, cross-field bounds, reset policy, and timeout
// nesting, returning joined errors that each name the offending config key.
func (c Config) Validate() error {
	var errs []error
	bad := func(key string, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	if c.Connections < 1 || c.Connections > MaxConnections {
		bad("pool.connections", "must be 1 to %d, got %d", MaxConnections, c.Connections)
	}
	if c.QueueCap < 1 || c.QueueCap > MaxQueueCap {
		bad("queue.capacity", "must be 1 to %d, got %d", MaxQueueCap, c.QueueCap)
	}
	if c.MaxClients < 1 || c.MaxClients > MaxClients {
		bad("limits.max_clients", "must be 1 to %d, got %d", MaxClients, c.MaxClients)
	}
	if c.MaxStartup < 1 || c.MaxStartup > c.MaxClients {
		bad("limits.max_startup", "must be 1 to limits.max_clients (%d), got %d", c.MaxClients, c.MaxStartup)
	}
	if c.QueueCap > c.MaxClients {
		bad("queue.capacity", "%d is more than limits.max_clients (%d): the queue holds at most one request per client", c.QueueCap, c.MaxClients)
	}
	if c.ReplyBufferCap < MinReplyBufferCap || c.ReplyBufferCap > MaxReplyBufferCap {
		bad("limits.reply_buffer", "must be %d to %d bytes, got %d", MinReplyBufferCap, MaxReplyBufferCap, c.ReplyBufferCap)
	}
	switch {
	case c.Reset != ResetAlways && c.Reset != ResetOnSwitch:
		bad("lease.reset", "must be always or on-switch")
	case c.Pool == nil && (c.Reset != ResetAlways || c.Reseed):
		bad("lease.reset", "on-switch and lease.reseed need the pool core, not the fixed set")
	}
	if c.Pool != nil {
		if err := c.poolConfig().Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, c.Timeouts.Validate()...)
	if c.Admission.Rule != 0 {
		errs = append(errs, c.admissionConfig().Validate())
	}
	errs = append(errs, c.coalesceErrors()...)
	return errors.Join(errs...)
}

// TimeoutNest describes one required strict ordering (InnerValue < OuterValue)
// between an inner wait and the outer wait that encloses it.
type TimeoutNest struct {
	Inner, Outer       string
	InnerKey, OuterKey string
	InnerValue         time.Duration
	OuterValue         time.Duration
}

// Nesting returns all timeout nesting constraints checked at config load and
// rendered into docs/timeout-budget.md.
func (t Timeouts) Nesting() []TimeoutNest {
	n := func(inner, outer string, ik, ok string, iv, ov time.Duration) TimeoutNest {
		return TimeoutNest{Inner: inner, Outer: outer, InnerKey: ik, OuterKey: ok, InnerValue: iv, OuterValue: ov}
	}
	return []TimeoutNest{
		n("database connect", "queue wait", "timeouts.database_connect", "timeouts.queue_wait", t.DBConnect, t.QueueWait),
		n("reset after a lease", "queue wait", "timeouts.reset", "timeouts.queue_wait", t.Reset, t.QueueWait),
		n("queue wait", "client idle between transactions", "timeouts.queue_wait", "timeouts.client_idle", t.QueueWait, t.ClientIdle),
		n("database statement", "client idle between transactions", "timeouts.statement", "timeouts.client_idle", t.Statement, t.ClientIdle),
		n("client mid-message read", "client idle between transactions", "timeouts.client_mid_message", "timeouts.client_idle", t.ClientMidMessage, t.ClientIdle),
		n("client startup", "client idle between transactions", "timeouts.client_startup", "timeouts.client_idle", t.ClientStartup, t.ClientIdle),
		n("redial delay", "queue wait", "timeouts.redial_delay", "timeouts.queue_wait", t.RedialDelay, t.QueueWait),
		n("client write stalled", "client idle between transactions", "timeouts.client_write", "timeouts.client_idle", t.ClientWrite, t.ClientIdle),
		n("client idle inside a transaction", "client idle between transactions", "timeouts.idle_in_transaction", "timeouts.client_idle", t.IdleInTx, t.ClientIdle),
		n("cancel and clean-up of a dirty connection", "client idle between transactions", "timeouts.cleanup", "timeouts.client_idle", t.Cleanup, t.ClientIdle),
		n("check before reuse", "database statement", "pool.check_timeout", "timeouts.statement", t.CheckTimeout, t.Statement),
	}
}

// Validate checks that every timeout is in (0, 24h] and that every inner wait
// strictly precedes its outer wait.
func (t Timeouts) Validate() []error {
	var errs []error
	for _, f := range []struct {
		key string
		v   time.Duration
	}{{"timeouts.client_startup", t.ClientStartup}, {"timeouts.client_mid_message", t.ClientMidMessage},
		{"timeouts.client_idle", t.ClientIdle}, {"timeouts.queue_wait", t.QueueWait},
		{"timeouts.database_connect", t.DBConnect}, {"timeouts.statement", t.Statement}, {"timeouts.reset", t.Reset},
		{"timeouts.client_write", t.ClientWrite}, {"timeouts.redial_delay", t.RedialDelay},
		{"timeouts.idle_in_transaction", t.IdleInTx}, {"timeouts.cleanup", t.Cleanup}, {"pool.check_timeout", t.CheckTimeout}} {
		if f.v <= 0 || f.v > maxTimeout {
			errs = append(errs, fmt.Errorf("%w: %s: must be above 0 and at most %s, got %s", ErrConfig, f.key, maxTimeout, f.v))
		}
	}
	if len(errs) > 0 {
		return errs
	}
	for _, n := range t.Nesting() {
		if n.InnerValue >= n.OuterValue {
			errs = append(errs, fmt.Errorf("%w: %s: the %s wait (%s) must end before the %s wait (%s, %s)", ErrConfig,
				n.InnerKey, n.Inner, n.InnerValue, n.Outer, n.OuterValue, n.OuterKey))
		}
	}
	return errs
}

const maxTimeout = 24 * time.Hour
