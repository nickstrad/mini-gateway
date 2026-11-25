package fakepg

import (
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

const (
	DefaultAddr           = "pg"
	DefaultMaxConnections = 100
	DefaultLogCap         = 1 << 20
	MaxLogCap             = 1 << 24
	DefaultMaxRows        = 1 << 20
	DefaultAfterDelay     = 10 * time.Millisecond
	MaxStallWindows       = 64
	MaxDelayedSegments    = 1024
	DefaultMaxReplyBytes  = 64 << 20
	MaxReplyBytesLimit    = 1 << 32
	readChunk             = 4096
	outFill               = 64 << 10
	firstPID              = 10_000
	DefaultServerVersion  = "17.0 (fakepg)"
)

type LogMode uint8

const (
	LogRing LogMode = iota
	LogCountOnly
)

type LogConfig struct {
	Mode LogMode
	Cap  int
}

type Row struct {
	Key   string
	Value string
}

type KindTime struct {
	Kind Kind
	Time time.Duration
}

type KeyTime struct {
	Key  string
	Time time.Duration
}

type Stall struct {
	From  sim.Time
	To    sim.Time
	Extra time.Duration
}

type Service struct {
	Fixed     time.Duration
	JitterMax time.Duration
	PerKind   []KindTime
	PerKey    []KeyTime
	Stalls    []Stall
}

type Config struct {
	Addr           string
	MaxConnections int
	Users          []string
	Databases      []string
	Superusers     []string
	Rows           []Row
	MaxRows        int
	Service        Service
	Capacity       int
	MaxMessage     int
	MaxReplyBytes  int64
	WriteLog       LogConfig
	StatementLog   LogConfig
	ServerVersion  string
	AfterDelay     time.Duration
	Options        simnet.Options
	plant          plant
}

type plant struct {
	doubleApply bool
	idleInTx    bool
}

func (c Config) normalized() Config {
	if c.Addr == "" {
		c.Addr = DefaultAddr
	}
	if c.MaxConnections <= 0 {
		c.MaxConnections = DefaultMaxConnections
	}
	if c.MaxRows <= 0 {
		c.MaxRows = DefaultMaxRows
	}
	if c.MaxReplyBytes <= 0 {
		c.MaxReplyBytes = DefaultMaxReplyBytes
	}
	if c.MaxReplyBytes > MaxReplyBytesLimit {
		c.MaxReplyBytes = MaxReplyBytesLimit
	}
	if c.MaxMessage <= 0 {
		c.MaxMessage = DefaultMaxMessage
	}
	if c.ServerVersion == "" {
		c.ServerVersion = DefaultServerVersion
	}
	if c.AfterDelay <= 0 {
		c.AfterDelay = DefaultAfterDelay
	}
	c.WriteLog = c.WriteLog.normalized()
	c.StatementLog = c.StatementLog.normalized()
	return c
}

func (l LogConfig) normalized() LogConfig {
	if l.Cap <= 0 {
		l.Cap = DefaultLogCap
	}
	if l.Cap > MaxLogCap {
		l.Cap = MaxLogCap
	}
	return l
}
