package workload

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// ArrivalKind selects how a client schedules successive request arrivals.
type ArrivalKind uint8

const (
	// ArrivalConstant generates open-loop arrivals at fixed Interval steps.
	ArrivalConstant ArrivalKind = iota
	// ArrivalPoisson generates open-loop arrivals with exponential gaps of mean Interval.
	ArrivalPoisson
	// ArrivalClosed waits Think after each completed request before generating the next.
	ArrivalClosed
)

// KeyKind selects how a client picks keys from the kv table.
type KeyKind uint8

const (
	// KeysUniform chooses keys uniformly from [0, N).
	KeysUniform KeyKind = iota
	// KeysZipf chooses keys from a Zipf distribution with exponent Zipf/1000.
	KeysZipf
	// KeysFixed always returns Keys.Fixed.
	KeysFixed
)

// Op selects the kind of SQL conversation a generated request executes.
type Op uint8

const (
	// OpRead executes a single-key SELECT.
	OpRead Op = iota
	// OpWrite executes a single-key UPDATE.
	OpWrite
	// OpTx executes BEGIN, TxStatements reads/writes with TxThink pauses, and COMMIT.
	OpTx
	// OpFixed executes the literal Tenant.Statement string.
	OpFixed
)

// DisconnectPoint chooses when a configured or faulted client drops its connection.
type DisconnectPoint uint8

const (
	// DisconnectNow drops the connection immediately when the fault fires.
	DisconnectNow DisconnectPoint = iota
	// DisconnectAfterSend drops immediately after writing the request's first Query.
	DisconnectAfterSend
	// DisconnectInTx drops after receiving ReadyForQuery('T') inside an open transaction.
	DisconnectInTx
)

// Default limits and hard bounds for workload configuration.
const (
	DefaultBacklog   = 1024
	MaxBacklog       = 1 << 20
	MaxClients       = 1 << 16
	MaxRequests      = 1 << 24
	DefaultHalfHold  = time.Second
	MaxTxStatements  = 64
	DefaultUser      = "app"
	DefaultKeyPrefix = "k"
)

// Weight assigns a relative selection weight to one Op in a tenant's workload mix.
type Weight struct {
	Op     Op
	Weight int
}

// Keys configures key distribution for a tenant's generated reads and writes.
type Keys struct {
	Kind  KeyKind
	N     int
	Zipf  int64
	Fixed string
}

// Retry configures per-request exponential-backoff retries (off by default when Max == 0).
type Retry struct {
	Max       int
	Backoff   time.Duration
	Cap       time.Duration
	OnConnect bool
	OnLost    bool
	OnTimeout bool
	OnCodes   []string
}

// Disconnect schedules a deterministic client disconnect on a specific 0-based request index.
type Disconnect struct {
	Request int
	Point   DisconnectPoint
	Reset   bool
}

// Tenant defines one tenant's client population, arrival process, query mix,
// retry policy, and disconnect hooks. Name is sent as the Postgres startup user.
type Tenant struct {
	Name         string
	Database     string
	Clients      int
	Arrival      ArrivalKind
	Interval     time.Duration
	Think        time.Duration
	Requests     int
	Start        time.Duration
	Spread       bool
	Mix          []Weight
	Keys         Keys
	Statement    string
	TxStatements int
	TxReads      int
	TxThink      time.Duration
	Deadline     time.Duration
	Hold         time.Duration
	Retry        Retry
	Tag          bool
	Backlog      int
	Disconnect   []Disconnect
	ConnectFirst bool
}

// Config configures a simulated Workload targeting Addr over simnet.
type Config struct {
	Addr    string
	Options simnet.Options
	Tenants []Tenant
}

// ErrConfig wraps all workload configuration validation failures returned by New.
var ErrConfig = errors.New("workload: bad configuration")

func (c Config) validate() error {
	total := 0
	for i, t := range c.Tenants {
		if t.Clients < 0 || t.Requests < 0 || t.Requests > MaxRequests {
			return fmt.Errorf("%w: tenant %d: clients %d requests %d", ErrConfig, i, t.Clients, t.Requests)
		}
		total += t.Clients
		if t.Keys.Kind == KeysZipf && t.Keys.Zipf <= 1000 {
			return fmt.Errorf("%w: tenant %d: the Zipf exponent (in thousandths) must be above 1000, got %d", ErrConfig, i, t.Keys.Zipf)
		}
		if t.Keys.Kind != KeysFixed && t.Keys.N <= 0 && t.Requests > 0 && needsKeys(t) {
			return fmt.Errorf("%w: tenant %d: no keys", ErrConfig, i)
		}
		if t.Arrival != ArrivalClosed && t.Interval <= 0 && t.Requests > 1 {
			return fmt.Errorf("%w: tenant %d: open-loop arrivals need an interval", ErrConfig, i)
		}
		if t.TxStatements > MaxTxStatements {
			return fmt.Errorf("%w: tenant %d: more than %d statements in a transaction", ErrConfig, i, MaxTxStatements)
		}
	}
	if total > MaxClients {
		return fmt.Errorf("%w: %d clients, the limit is %d", ErrConfig, total, MaxClients)
	}
	return nil
}

func needsKeys(t Tenant) bool {
	if len(t.Mix) == 0 {
		return t.Statement == ""
	}
	for _, w := range t.Mix {
		if w.Op != OpFixed && w.Weight > 0 {
			return true
		}
	}
	return false
}

func (t Tenant) normalized() Tenant {
	if t.Name == "" {
		t.Name = DefaultUser
	}
	if t.Database == "" {
		t.Database = t.Name
	}
	if t.Backlog <= 0 {
		t.Backlog = DefaultBacklog
	}
	if t.Backlog > MaxBacklog {
		t.Backlog = MaxBacklog
	}
	if len(t.Mix) == 0 {
		op := OpRead
		if t.Statement != "" {
			op = OpFixed
		}
		t.Mix = []Weight{{Op: op, Weight: 1}}
	}
	return t
}
