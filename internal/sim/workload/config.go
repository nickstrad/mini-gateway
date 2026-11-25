package workload

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

type ArrivalKind uint8

const (
	ArrivalConstant ArrivalKind = iota
	ArrivalPoisson
	ArrivalClosed
)

type KeyKind uint8

const (
	KeysUniform KeyKind = iota
	KeysZipf
	KeysFixed
)

type Op uint8

const (
	OpRead Op = iota
	OpWrite
	OpTx
	OpFixed
)

type DisconnectPoint uint8

const (
	DisconnectNow DisconnectPoint = iota
	DisconnectAfterSend
	DisconnectInTx
)

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

type Weight struct {
	Op     Op
	Weight int
}

type Keys struct {
	Kind  KeyKind
	N     int
	Zipf  int64
	Fixed string
}

type Retry struct {
	Max       int
	Backoff   time.Duration
	Cap       time.Duration
	OnConnect bool
	OnLost    bool
	OnTimeout bool
	OnCodes   []string
}

type Disconnect struct {
	Request int
	Point   DisconnectPoint
	Reset   bool
}

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

type Config struct {
	Addr    string
	Options simnet.Options
	Tenants []Tenant
}

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
		if t.Keys.Kind != KeysFixed && t.Keys.N <= 0 && needsKeys(t) {
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
