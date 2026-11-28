// Package coalesce implements the deterministic, sans-I/O request coalescing
// flight table. It groups identical eligible reads into shared flights, bounds
// waiters, stored reply bytes, active keys, and no-share entries, and reports
// state transitions for other waiters through a notice queue.
package coalesce

import (
	"errors"
	"fmt"
)

// Instant is virtual time in nanoseconds from the caller's clock.
type Instant int64

// RequestID identifies one client request waiting on or owning a flight.
type RequestID int64

// FlightID uniquely identifies one shared flight inside a Table.
type FlightID int64

const (
	// Millisecond is one millisecond expressed in Instant units.
	Millisecond Instant = 1_000_000
	// Second is one second expressed in Instant units.
	Second = 1000 * Millisecond
)

// Mode selects which incoming work units are eligible to share a flight.
type Mode uint8

const (
	// ModeOff disables coalescing; every request passes unshared.
	ModeOff Mode = iota
	// ModeAuto shares requests whose classifier safe-shape Shareable flag is set.
	ModeAuto
	// ModeHinted shares requests carrying the leading /* mg:share */ hint mark.
	ModeHinted
	// NumModes is the sentinel count of Mode values.
	NumModes
)

var modeNames = [NumModes]string{"off", "auto", "hinted"}

// String returns the canonical configuration name of m, or "?" if out of range.
func (m Mode) String() string {
	if m < NumModes {
		return modeNames[m]
	}
	return "?"
}

// ParseMode parses a canonical Mode name.
func ParseMode(s string) (Mode, bool) {
	for i, n := range modeNames {
		if n == s {
			return Mode(i), true
		}
	}
	return 0, false
}

// JoinMode selects whether an arriving request may join a read already running
// on the database.
type JoinMode uint8

const (
	// JoinStrict holds arrivals behind an already-running read so no caller can
	// observe data older than its own arrival.
	JoinStrict JoinMode = iota
	// JoinRelaxed lets arrivals join a read already running on the database.
	JoinRelaxed
	// NumJoinModes is the sentinel count of JoinMode values.
	NumJoinModes
)

var joinNames = [NumJoinModes]string{"strict", "relaxed"}

// String returns the canonical configuration name of j, or "?" if out of range.
func (j JoinMode) String() string {
	if j < NumJoinModes {
		return joinNames[j]
	}
	return "?"
}

// ParseJoin parses a canonical JoinMode name.
func ParseJoin(s string) (JoinMode, bool) {
	for i, n := range joinNames {
		if n == s {
			return JoinMode(i), true
		}
	}
	return 0, false
}

// Plant is a test-only bitmask of deliberately broken invariants used by
// canaries, ablations, and fuzzing.
type Plant uint16

const (
	// PlantStrictJoinsRunning breaks strict mode by letting arrivals join a running read.
	PlantStrictJoinsRunning Plant = 1 << iota
	// PlantAnyLeaveCancels cancels a flight on owner departure instead of handing off.
	PlantAnyLeaveCancels
	// PlantLeaverGetsResult leaves a departed follower on the flight's follower slice.
	PlantLeaverGetsResult
	// PlantNoWaiterCap disables the per-flight waiter cap.
	PlantNoWaiterCap
	// PlantNoReplyCap disables the stored reply byte cap.
	PlantNoReplyCap
	// PlantSafeReadDoor checks Unit.SafeRead instead of Unit.Shareable under ModeAuto.
	PlantSafeReadDoor
)

const (
	// DefaultMaxFlights is the default cap on distinct keys with live flights.
	DefaultMaxFlights = 1024
	// DefaultMaxWaiters is the default cap on waiters per flight, owner included.
	DefaultMaxWaiters = 1024
	// DefaultMaxReplyBytes is the default cap on stored shared-reply bytes per flight.
	DefaultMaxReplyBytes = 64 << 10
	// DefaultNoShareFor is how long an oversized or unshareable key stays unshared.
	DefaultNoShareFor = 10 * Second
	// DefaultNoShareMax is the default capacity of the bounded no-share ring.
	DefaultNoShareMax = 256
	// DefaultDeadline is the default per-flight deadline from flight creation.
	DefaultDeadline = 30 * Second
	// MaxFlightsLimit is the upper validation bound on Config.MaxFlights.
	MaxFlightsLimit = 1 << 16
	// MaxWaitersLimit is the upper validation bound on Config.MaxWaiters.
	MaxWaitersLimit = 1 << 16
	// MaxReplyBytesLimit is the upper validation bound on Config.MaxReplyBytes.
	MaxReplyBytesLimit = 32 << 20
	// MaxNoShareLimit is the upper validation bound on Config.NoShareMax.
	MaxNoShareLimit = 1 << 16
	// MaxNoShareFor is the upper validation bound on NoShareFor and Deadline.
	MaxNoShareFor = 24 * 3600 * Second
	// MaxHotKeys is the maximum number of opt-in hot-key slots.
	MaxHotKeys = 64
	// MaxTenantStats caps distinct per-tenant hit entries before folding into OtherTenant.
	MaxTenantStats = 4096
	// OtherTenant is the overflow tenant label past MaxTenantStats.
	OtherTenant = "other"
)

// Config holds the knobs and bounds for a coalescer Table. Zero values select
// defaults via WithDefaults; HotKeys defaults to 0 (off).
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

// WithDefaults returns a copy of c with zero-valued limits replaced by their defaults.
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

// ErrConfig is the sentinel error wrapped by Config.Validate failures.
var ErrConfig = errors.New("coalesce: bad configuration")

// Validate checks every field of c and returns all violations joined together,
// each naming its configuration key.
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

// after adds duration d to now and saturates at maxInstant so deadlines near
// the end of int64 never wrap into the past.
func after(now, d Instant) Instant {
	if d > maxInstant-now {
		return maxInstant
	}
	return now + d
}
