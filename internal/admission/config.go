package admission

import (
	"errors"
	"fmt"
)

type Time = int64

type RuleKind uint8

const (
	RuleTimeout RuleKind = iota + 1
	RuleControlLaw
	RuleFIFO
	RuleFolly
	numRuleKinds
)

var ruleNames = [...]string{"?", "timeout", "control-law", "fifo", "folly"}

func (k RuleKind) String() string {
	if k < numRuleKinds {
		return ruleNames[k]
	}
	return "?"
}

func (k RuleKind) ComparisonArm() bool { return k == RuleFIFO || k == RuleFolly }

func RuleKinds() []RuleKind { return []RuleKind{RuleTimeout, RuleControlLaw, RuleFIFO, RuleFolly} }

func ParseProductRule(s string) (RuleKind, bool) {
	switch s {
	case "timeout":
		return RuleTimeout, true
	case "control-law":
		return RuleControlLaw, true
	}
	return 0, false
}

type LIFOMode uint8

const (
	LIFOAdaptive LIFOMode = iota + 1
	LIFOOff
)

func (m LIFOMode) String() string {
	switch m {
	case LIFOAdaptive:
		return "adaptive"
	case LIFOOff:
		return "off"
	}
	return "?"
}

func ParseLIFO(s string) (LIFOMode, bool) {
	switch s {
	case "adaptive":
		return LIFOAdaptive, true
	case "off":
		return LIFOOff, true
	}
	return 0, false
}

type Plant uint8

const (
	PlantNoHeadSweep Plant = 1 << iota
	PlantFirstBurst
	PlantNoLIFOHysteresis
	PlantRemoveSwapsTail
)

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

var ErrConfig = errors.New("admission: bad configuration")

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

func (c Config) Quiet() Time { return QuietIntervals * c.Interval }
