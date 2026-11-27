// Package admission implements per-tenant bounded admission queues with
// selectable shedding rules, adaptive LIFO service under overload, and a
// minimum-delay load gauge. It is pure sans-I/O integer code: callers pass
// explicit virtual timestamps and drain decided sheds after each operation.
package admission

import (
	"errors"
	"fmt"
)

// Time is a virtual timestamp or duration in nanoseconds.
type Time = int64

// RuleKind identifies which shedding rule a Queues instance runs.
type RuleKind uint8

const (
	// RuleTimeout is the "Fail at Scale" timeout rule: allowed wait is
	// Interval unless a standing queue exists, in which case it drops to Target.
	RuleTimeout RuleKind = iota + 1
	// RuleControlLaw is the RFC 8289 CoDel control law driven at the queue head.
	RuleControlLaw
	// RuleFIFO is a simulator-only comparison arm that never sheds by a rule.
	RuleFIFO
	// RuleFolly is a simulator-only comparison arm reproducing folly's Codel detector.
	RuleFolly
	numRuleKinds
)

var ruleNames = [...]string{"?", "timeout", "control-law", "fifo", "folly"}

// String returns the canonical lowercase name of k, or "?" if unknown.
func (k RuleKind) String() string {
	if k < numRuleKinds {
		return ruleNames[k]
	}
	return "?"
}

// ComparisonArm reports whether k is a simulator-only baseline that cannot be
// selected from the proxy binary's configuration.
func (k RuleKind) ComparisonArm() bool { return k == RuleFIFO || k == RuleFolly }

// RuleKinds returns all four defined rules (both product rules and both comparison arms).
func RuleKinds() []RuleKind { return []RuleKind{RuleTimeout, RuleControlLaw, RuleFIFO, RuleFolly} }

// ParseProductRule parses a binary-config rule name, accepting only "timeout"
// and "control-law" so simulator comparison arms stay unreachable from config.
func ParseProductRule(s string) (RuleKind, bool) {
	switch s {
	case "timeout":
		return RuleTimeout, true
	case "control-law":
		return RuleControlLaw, true
	}
	return 0, false
}

// LIFOMode controls whether overloaded tenant queues switch from oldest-first
// to newest-first service.
type LIFOMode uint8

const (
	// LIFOAdaptive serves newest-first while the rule reports overload and
	// returns to oldest-first after LIFOHold without overload.
	LIFOAdaptive LIFOMode = iota + 1
	// LIFOOff keeps strict oldest-first (FIFO) service order at all times.
	LIFOOff
)

// String returns the canonical lowercase name of m, or "?" if unknown.
func (m LIFOMode) String() string {
	switch m {
	case LIFOAdaptive:
		return "adaptive"
	case LIFOOff:
		return "off"
	}
	return "?"
}

// ParseLIFO parses a queue.lifo config value ("adaptive" or "off").
func ParseLIFO(s string) (LIFOMode, bool) {
	switch s {
	case "adaptive":
		return LIFOAdaptive, true
	case "off":
		return LIFOOff, true
	}
	return 0, false
}

// Plant is a bitmask of test-only fault switches used by canaries and fuzzing.
// Production configuration always leaves Plant zero.
type Plant uint8

const (
	// PlantNoHeadSweep disables head sweeping so stale entries remain queued under LIFO.
	PlantNoHeadSweep Plant = 1 << iota
	// PlantFirstBurst reproduces the classic bug of updating LastEmpty only on dequeue.
	PlantFirstBurst
	// PlantNoLIFOHysteresis clears LIFO immediately instead of waiting for LIFOHold.
	PlantNoLIFOHysteresis
	// PlantRemoveSwapsTail breaks arrival order on mid-queue removal for fuzz testing.
	PlantRemoveSwapsTail
)

// Time units, configuration defaults, and fixed structural bounds.
const (
	Millisecond Time = 1_000_000
	Second      Time = 1_000 * Millisecond

	DefaultTarget         = 5 * Millisecond
	DefaultInterval       = 100 * Millisecond
	DefaultCapacity       = 256
	DefaultTenantCapacity = 256
	DefaultMaxTenants     = 1024
	DefaultLIFOHold       = Second

	MinTarget         = 1_000
	MaxInterval       = 60 * Second
	MaxCapacity       = 1 << 20
	MaxTenantsCap     = 1 << 16
	MaxLIFOHold       = 3600 * Second
	QuietIntervals    = 16
	MaxDropCount      = 1 << 16
	MaxTenantStats    = 4096
	OtherTenant       = "other"
	maxPendingShedPad = 2
)

// Config holds the queue capacities, shedding rule parameters, LIFO hold, and
// test-only Plant switches for a Queues instance. Zero values mean defaults.
type Config struct {
	Rule           RuleKind
	Target         Time
	Interval       Time
	Capacity       int
	TenantCapacity int
	MaxTenants     int
	LIFO           LIFOMode
	LIFOHold       Time
	Detail         bool
	Plant          Plant
}

// ErrConfig is wrapped by every admission configuration validation error.
var ErrConfig = errors.New("admission: bad configuration")

// WithDefaults returns a copy of c with any zero field replaced by its default.
// Negative values are preserved so Validate fails closed naming the key.
func (c Config) WithDefaults() Config {
	if c.Rule == 0 {
		c.Rule = RuleTimeout
	}
	if c.Target == 0 {
		c.Target = DefaultTarget
	}
	if c.Interval == 0 {
		c.Interval = DefaultInterval
	}
	if c.Capacity == 0 {
		c.Capacity = DefaultCapacity
	}
	if c.TenantCapacity == 0 {
		c.TenantCapacity = min(DefaultTenantCapacity, c.Capacity)
	}
	if c.MaxTenants == 0 {
		c.MaxTenants = DefaultMaxTenants
	}
	if c.LIFO == 0 {
		c.LIFO = LIFOAdaptive
	}
	if c.LIFOHold == 0 {
		c.LIFOHold = DefaultLIFOHold
	}
	return c
}

// Validate checks every field and cross-field bound of c, returning a joined
// error naming each offending config key.
func (c Config) Validate() error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	if c.Rule == 0 || c.Rule >= numRuleKinds {
		bad("queue.rule", "must be timeout or control-law, got %d", c.Rule)
	}
	if c.Interval <= 0 || c.Interval > MaxInterval {
		bad("queue.interval", "must be above 0 and at most %dns, got %dns", MaxInterval, c.Interval)
	}
	if c.Target < MinTarget {
		bad("queue.target", "must be at least %dns, got %dns", MinTarget, c.Target)
	} else if c.Target >= c.Interval {
		bad("queue.target", "%dns must be below queue.interval (%dns)", c.Target, c.Interval)
	}
	if c.Capacity < 1 || c.Capacity > MaxCapacity {
		bad("queue.capacity", "must be 1 to %d, got %d", MaxCapacity, c.Capacity)
	}
	if c.TenantCapacity < 1 || c.TenantCapacity > c.Capacity {
		bad("queue.tenant_capacity", "must be 1 to queue.capacity (%d), got %d", c.Capacity, c.TenantCapacity)
	}
	if c.MaxTenants < 1 || c.MaxTenants > MaxTenantsCap {
		bad("queue.max_tenants", "must be 1 to %d, got %d", MaxTenantsCap, c.MaxTenants)
	}
	if c.LIFO != LIFOAdaptive && c.LIFO != LIFOOff {
		bad("queue.lifo", "must be adaptive or off, got %d", c.LIFO)
	}
	if c.LIFOHold <= 0 || c.LIFOHold > MaxLIFOHold {
		bad("queue.lifo_hold", "must be above 0 and at most %dns, got %dns", MaxLIFOHold, c.LIFOHold)
	}
	return errors.Join(errs...)
}

// Quiet returns how long an empty tenant queue is kept before removal (16 intervals,
// matching the control law's re-entry memory window).
func (c Config) Quiet() Time { return QuietIntervals * c.Interval }

// pendingRefuseAt caps undrained sheds before Push refuses arrivals (ReasonUndrained);
// pendingCap sizes the single preallocated buffer to absorb one full-capacity sweep
// on top of that threshold without ever reallocating.
func pendingRefuseAt(cfg Config) int { return cfg.Capacity + maxPendingShedPad - 1 }

func pendingCap(cfg Config) int { return pendingRefuseAt(cfg) + cfg.Capacity + 1 }
