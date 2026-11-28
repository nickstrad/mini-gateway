package coalesce

import (
	"errors"
	"fmt"
)

type Instant int64

type RequestID int64

type FlightID int64

const (
	Millisecond Instant = 1_000_000
	Second              = 1000 * Millisecond
)

type Mode uint8

const (
	ModeOff Mode = iota
	ModeAuto
	ModeHinted
	NumModes
)

var modeNames = [NumModes]string{"off", "auto", "hinted"}

func (m Mode) String() string {
	if m < NumModes {
		return modeNames[m]
	}
	return "?"
}

func ParseMode(s string) (Mode, bool) {
	for i, n := range modeNames {
		if n == s {
			return Mode(i), true
		}
	}
	return 0, false
}

type JoinMode uint8

const (
	JoinStrict JoinMode = iota
	JoinRelaxed
	NumJoinModes
)

var joinNames = [NumJoinModes]string{"strict", "relaxed"}

func (j JoinMode) String() string {
	if j < NumJoinModes {
		return joinNames[j]
	}
	return "?"
}

func ParseJoin(s string) (JoinMode, bool) {
	for i, n := range joinNames {
		if n == s {
			return JoinMode(i), true
		}
	}
	return 0, false
}

type Plant uint16

const (
	PlantStrictJoinsRunning Plant = 1 << iota
	PlantAnyLeaveCancels
	PlantLeaverGetsResult
	PlantNoWaiterCap
	PlantNoReplyCap
	PlantSafeReadDoor
)

const (
	DefaultMaxFlights    = 1024
	DefaultMaxWaiters    = 1024
	DefaultMaxReplyBytes = 64 << 10
	DefaultNoShareFor    = 10 * Second
	DefaultNoShareMax    = 256
	DefaultDeadline      = 30 * Second
	MaxFlightsLimit      = 1 << 16
	MaxWaitersLimit      = 1 << 16
	MaxReplyBytesLimit   = 32 << 20
	MaxNoShareLimit      = 1 << 16
	MaxNoShareFor        = 24 * 3600 * Second
	MaxHotKeys           = 64
	MaxTenantStats       = 4096
	OtherTenant          = "other"
)

type Config struct {
	Mode          Mode
	Join          JoinMode
	MaxFlights    int
	MaxWaiters    int
	MaxReplyBytes int64
	NoShareFor    Instant
	NoShareMax    int
	Deadline      Instant
	HotKeys       int
	Plant         Plant
}

func (c Config) WithDefaults() Config {
	if c.MaxFlights == 0 {
		c.MaxFlights = DefaultMaxFlights
	}
	if c.MaxWaiters == 0 {
		c.MaxWaiters = DefaultMaxWaiters
	}
	if c.MaxReplyBytes == 0 {
		c.MaxReplyBytes = DefaultMaxReplyBytes
	}
	if c.NoShareFor == 0 {
		c.NoShareFor = DefaultNoShareFor
	}
	if c.NoShareMax == 0 {
		c.NoShareMax = DefaultNoShareMax
	}
	if c.Deadline == 0 {
		c.Deadline = DefaultDeadline
	}
	return c
}

var ErrConfig = errors.New("coalesce: bad configuration")

func (c Config) Validate() error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	if c.Mode >= NumModes {
		bad("coalesce.mode", "must be off, auto or hinted")
	}
	if c.Join >= NumJoinModes {
		bad("coalesce.join", "must be strict or relaxed")
	}
	ints := []struct {
		key      string
		v, limit int64
	}{
		{"coalesce.max_flights", int64(c.MaxFlights), MaxFlightsLimit},
		{"coalesce.max_waiters", int64(c.MaxWaiters), MaxWaitersLimit},
		{"coalesce.max_reply_bytes", c.MaxReplyBytes, MaxReplyBytesLimit},
		{"coalesce.no_share_max", int64(c.NoShareMax), MaxNoShareLimit},
		{"coalesce.no_share_for", int64(c.NoShareFor), int64(MaxNoShareFor)},
		{"timeouts.statement", int64(c.Deadline), int64(MaxNoShareFor)},
	}
	for _, f := range ints {
		if f.v < 1 || f.v > f.limit {
			bad(f.key, "must be 1 to %d, got %d", f.limit, f.v)
		}
	}
	if c.MaxWaiters < 2 && c.MaxWaiters >= 1 {
		bad("coalesce.max_waiters", "must be at least 2 (the request that starts a flight and one follower), got %d", c.MaxWaiters)
	}
	if c.HotKeys < 0 || c.HotKeys > MaxHotKeys {
		bad("coalesce.hot_keys", "must be 0 (off) to %d, got %d", MaxHotKeys, c.HotKeys)
	}
	return errors.Join(errs...)
}

const maxInstant = Instant(1<<63 - 1)

func after(now, d Instant) Instant {
	if d > maxInstant-now {
		return maxInstant
	}
	return now + d
}
