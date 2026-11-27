package pool

import (
	"errors"
	"fmt"
	"time"
)

type ResetMode uint8

const (
	ResetAlways ResetMode = iota + 1
	ResetOnSwitch
)

func (m ResetMode) String() string {
	switch m {
	case ResetAlways:
		return "always"
	case ResetOnSwitch:
		return "on-switch"
	}
	return "?"
}

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

type Plant uint8

const (
	PlantNoCap Plant = 1 << iota
	PlantNoRate
	PlantNoCheckDeadline
	PlantNoSuspect
	PlantIgnoreTag
	PlantResetClearsSuspect
)

var ErrConfig = errors.New("pool: bad configuration")

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

var ErrShare = errors.New("pool: fleet budget")

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
