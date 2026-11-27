package pool

import (
	"errors"
	"fmt"
	"time"
)

// ResetMode selects when the pool resets a cleanly released connection (decision D8).
type ResetMode uint8

const (
	// ResetAlways sends a reset after every clean lease release.
	ResetAlways ResetMode = iota + 1
	// ResetOnSwitch tags a cleanly released connection with its last tenant and
	// resets it on checkout only when a different tenant needs it.
	ResetOnSwitch
)

// String returns the config string for m ("always" or "on-switch").
func (m ResetMode) String() string {
	switch m {
	case ResetAlways:
		return "always"
	case ResetOnSwitch:
		return "on-switch"
	}
	return "?"
}

// Default and hard-limit configuration constants for the pool core.
const (
	DefaultCap             = 4
	DefaultMaxConnecting   = 4
	DefaultRatePerSec      = 10
	DefaultIdleTimeout     = 180 * time.Second
	DefaultMaxLifetime     = 30 * time.Minute
	DefaultJitterPermille  = 100
	DefaultCheckAfterIdle  = 30 * time.Second
	DefaultCheckTimeout    = 250 * time.Millisecond
	DefaultCleanup         = time.Second
	DefaultResetTimeout    = time.Second
	DefaultBreakerFailures = 5
	DefaultBreakerOpenFor  = time.Second
	DefaultMaxCancels      = 16
	MaxCap                 = 4096
	MaxConnectingLimit     = 256
	MaxRatePerSec          = 100000
	MaxJitterPermille      = 500
	MaxCancelsLimit        = 1024
	MaxBreakerFailures     = 1000
	MinJitterSpread        = time.Millisecond
	maxChecksPerCheckout   = 4
	maxDuration            = 24 * time.Hour
)

// Config configures the pool core's caps, timeouts, breaker, release policy,
// deterministic jitter stream, and test-only fault plants.
type Config struct {
	Cap             int
	MaxConnecting   int
	RatePerSec      int
	IdleTimeout     time.Duration
	MaxLifetime     time.Duration
	JitterPermille  int
	CheckAfterIdle  time.Duration
	CheckTimeout    time.Duration
	Cleanup         time.Duration
	ResetTimeout    time.Duration
	BreakerFailures int
	BreakerOpenFor  time.Duration
	MaxCancels      int
	Reset           ResetMode
	Reseed          bool
	NoJitter        bool
	Seed            uint64
	Plant           Plant
}

// Plant is a bitmask of test-only fault switches for canaries, ablations, and
// fuzz verification. It is never exposed through internal/config.
type Plant uint8

const (
	// PlantNoCap disables the open+connecting hard cap (cap canary).
	PlantNoCap Plant = 1 << iota
	// PlantNoRate disables concurrent-connect and creation-rate limits (cold-start ablation).
	PlantNoRate
	// PlantNoCheckDeadline omits the check-before-reuse deadline (check-hang canary).
	PlantNoCheckDeadline
	// PlantNoSuspect suppresses marking sibling connections suspect after a failure (mass-death canary).
	PlantNoSuspect
	// PlantIgnoreTag treats every idle connection as untagged under ResetOnSwitch (tenant-isolation canary).
	PlantIgnoreTag
	// PlantResetClearsSuspect wrongly clears suspect on reset completion without a check (FuzzPoolOps plant).
	PlantResetClearsSuspect
)

// ErrConfig is wrapped by all pool configuration validation errors.
var ErrConfig = errors.New("pool: bad configuration")

// WithDefaults returns a copy of c with any zero capacity, timeout, jitter, or
// reset mode replaced by its default. Negative values are left untouched so
// Validate fails closed on them.
func (c Config) WithDefaults() Config {
	ints := []struct {
		v   *int
		def int
	}{{&c.Cap, DefaultCap}, {&c.MaxConnecting, DefaultMaxConnecting}, {&c.RatePerSec, DefaultRatePerSec},
		{&c.BreakerFailures, DefaultBreakerFailures}, {&c.MaxCancels, DefaultMaxCancels}}
	for _, p := range ints {
		if *p.v == 0 {
			*p.v = p.def
		}
	}
	durs := []struct {
		v   *time.Duration
		def time.Duration
	}{{&c.IdleTimeout, DefaultIdleTimeout}, {&c.MaxLifetime, DefaultMaxLifetime}, {&c.CheckAfterIdle, DefaultCheckAfterIdle},
		{&c.CheckTimeout, DefaultCheckTimeout}, {&c.Cleanup, DefaultCleanup}, {&c.ResetTimeout, DefaultResetTimeout},
		{&c.BreakerOpenFor, DefaultBreakerOpenFor}}
	for _, p := range durs {
		if *p.v == 0 {
			*p.v = p.def
		}
	}
	if c.JitterPermille == 0 && !c.NoJitter {
		c.JitterPermille = DefaultJitterPermille
	}
	if c.Reset == 0 {
		c.Reset = ResetAlways
	}
	return c
}

// Validate checks every pool cap, duration, jitter spread, and reset policy,
// returning joined errors that each name the offending config key.
func (c Config) Validate() error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	ranges := []struct {
		key    string
		v, max int
	}{{"pool.connections", c.Cap, MaxCap}, {"pool.max_connecting", c.MaxConnecting, MaxConnectingLimit},
		{"fleet.connect_rate_per_s", c.RatePerSec, MaxRatePerSec}, {"breaker.failures", c.BreakerFailures, MaxBreakerFailures},
		{"pool.max_cancels", c.MaxCancels, MaxCancelsLimit}}
	for _, r := range ranges {
		if r.v < 1 || r.v > r.max {
			bad(r.key, "must be 1 to %d, got %d", r.max, r.v)
		}
	}
	if c.JitterPermille < 0 || c.JitterPermille > MaxJitterPermille {
		bad("pool.jitter_percent", "must be 0 to %d permille, got %d", MaxJitterPermille, c.JitterPermille)
	}
	durs := []struct {
		key string
		v   time.Duration
	}{{"pool.idle_timeout", c.IdleTimeout}, {"pool.max_lifetime", c.MaxLifetime}, {"pool.check_after_idle", c.CheckAfterIdle},
		{"pool.check_timeout", c.CheckTimeout}, {"timeouts.cleanup", c.Cleanup}, {"timeouts.reset", c.ResetTimeout},
		{"breaker.open_for", c.BreakerOpenFor}}
	for _, d := range durs {
		if d.v <= 0 || d.v > maxDuration {
			bad(d.key, "must be above 0 and at most %s, got %s", maxDuration, d.v)
		}
	}
	if !c.NoJitter && c.JitterPermille > 0 {
		for _, d := range []struct {
			key string
			v   time.Duration
		}{{"pool.idle_timeout", c.IdleTimeout}, {"pool.max_lifetime", c.MaxLifetime}} {
			if d.v > 0 && jitterAmplitude(d.v, c.JitterPermille) < int64(MinJitterSpread) {
				bad(d.key, "%s with pool.jitter_percent at %d permille spreads by less than %s; raise it or turn jitter off", d.v, c.JitterPermille, MinJitterSpread)
			}
		}
	}
	if c.Reset != ResetAlways && c.Reset != ResetOnSwitch {
		bad("lease.reset", "must be always or on-switch")
	}
	return errors.Join(errs...)
}

// ErrShare is wrapped by Share errors when a fleet budget cannot give each
// instance at least one unit.
var ErrShare = errors.New("pool: fleet budget")

// Share divides a fleet-wide budget evenly across instances, rounding down so
// the instances' shares never exceed total. It fails when instances < 1 or
// total < instances so no instance ever receives zero.
func Share(total, instances int) (int, error) {
	if instances < 1 {
		return 0, fmt.Errorf("%w: %d instances", ErrShare, instances)
	}
	if total < instances {
		return 0, fmt.Errorf("%w: a total of %d is less than the %d instances", ErrShare, total, instances)
	}
	return total / instances, nil
}

func jitterAmplitude(d time.Duration, permille int) int64 {
	return int64(d) * int64(permille) / 1000
}
