package proxy

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type CloseReason uint8

const (
	CloseNone CloseReason = iota
	CloseClientGone
	CloseClientTerminate
	CloseClientStartup
	CloseClientViolation
	CloseClientIdle
	CloseClientMidMessage
	CloseClientWriteStalled
	CloseClientPartialReply
	CloseClientLostInTx
	CloseClientTooMany
	CloseClientDraining
	CloseClientCancelRequest
	CloseDBConnectFailed
	CloseDBConnectDeadline
	CloseDBLost
	CloseDBStatementDeadline
	CloseDBClientVanished
	CloseDBResetFailed
	CloseDBReleaseDirty
	CloseDBUnsolicited
	CloseDBViolation
	CloseDBServerFatal
	CloseDBWriteStalled
	CloseClientShutdown
	CloseDBShutdown
	NumCloseReasons
)

var closeNames = [NumCloseReasons]string{"none", "client-gone", "client-terminate", "client-startup",
	"client-protocol-violation", "client-idle", "client-mid-message", "client-write-stalled", "partial-reply",
	"lost-in-transaction", "too-many-clients", "draining", "cancel-request", "connect-failed", "connect-deadline",
	"connection-lost", "statement-deadline", "client-vanished", "reset-failed", "release-dirty", "unsolicited-bytes",
	"protocol-violation", "server-fatal", "database-write-stalled", "client-shutdown", "database-shutdown"}

func (r CloseReason) String() string {
	if r < NumCloseReasons {
		return closeNames[r]
	}
	return "?"
}

func (r CloseReason) ClientSide() bool {
	return (r >= CloseClientGone && r <= CloseClientCancelRequest) || r == CloseClientShutdown
}

type ResetPolicy uint8

const (
	ResetAlways ResetPolicy = iota + 1
)

func (p ResetPolicy) String() string {
	if p == ResetAlways {
		return "always"
	}
	return "?"
}

type Plant uint16

const (
	PlantReleaseOnAnyReady Plant = 1 << iota
	PlantLendBeforeResetReply
	PlantNoReset
	PlantQueuedVanishKeepsSlot
	PlantSkipCourtesy
)

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
}

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
)

func DefaultTimeouts() Timeouts {
	return Timeouts{ClientStartup: DefaultClientStartup, ClientMidMessage: DefaultClientMidMessage,
		ClientIdle: DefaultClientIdle, QueueWait: DefaultQueueWait, DBConnect: DefaultDBConnect,
		Statement: DefaultStatement, Reset: DefaultReset, ClientWrite: DefaultClientWrite,
		RedialDelay: DefaultRedialDelay}
}

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
}

var ErrConfig = errors.New("proxy: bad configuration")

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
		{&t.ClientWrite, d.ClientWrite}, {&t.RedialDelay, d.RedialDelay}} {
		if *p.v == 0 {
			*p.v = p.def
		}
	}
	return c
}

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
	if c.Reset != ResetAlways {
		bad("lease.reset", "only always is supported")
	}
	errs = append(errs, c.Timeouts.Validate()...)
	return errors.Join(errs...)
}

type TimeoutNest struct {
	Inner, Outer       string
	InnerKey, OuterKey string
	InnerValue         time.Duration
	OuterValue         time.Duration
}

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
	}
}

func (t Timeouts) Validate() []error {
	var errs []error
	for _, f := range []struct {
		key string
		v   time.Duration
	}{{"timeouts.client_startup", t.ClientStartup}, {"timeouts.client_mid_message", t.ClientMidMessage},
		{"timeouts.client_idle", t.ClientIdle}, {"timeouts.queue_wait", t.QueueWait},
		{"timeouts.database_connect", t.DBConnect}, {"timeouts.statement", t.Statement}, {"timeouts.reset", t.Reset},
		{"timeouts.client_write", t.ClientWrite}, {"timeouts.redial_delay", t.RedialDelay}} {
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
