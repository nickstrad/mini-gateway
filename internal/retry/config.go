package retry

import (
	"errors"
	"fmt"
)

type Time int64

type Kind uint8

const (
	KindOff Kind = iota
	KindPreForwardOnce
	NumKinds
)

var kindNames = [NumKinds]string{"off", "pre-forward-once"}

func (k Kind) String() string {
	if k < NumKinds {
		return kindNames[k]
	}
	return "?"
}

func ParseKind(s string) (Kind, bool) {
	for k := Kind(0); k < NumKinds; k++ {
		if kindNames[k] == s {
			return k, true
		}
	}
	return 0, false
}

const (
	TenthsPerToken     = 10
	DepositTenths      = 1
	WithdrawTenths     = TenthsPerToken
	DefaultCapTokens   = 100
	DefaultFloorTokens = 10
	MaxTokens          = 1_000_000
	MaxAttempts        = 1
)

type Plant uint8

const (
	PlantIgnoreReached Plant = 1 << iota
	PlantNoOnceBound
	PlantNoWithdraw
	PlantNoCap
	PlantIgnoreBreaker
	PlantWithdrawFirst
)

type Config struct {
	Kind        Kind
	CapTokens   int64
	FloorTokens int64
	Plant       Plant
}

var ErrConfig = errors.New("retry: bad configuration")

func (c Config) WithDefaults() Config {
	if c.CapTokens == 0 {
		c.CapTokens = DefaultCapTokens
	}
	if c.FloorTokens == 0 {
		c.FloorTokens = min(DefaultFloorTokens, c.CapTokens)
	}
	return c
}

func (c Config) Validate() error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	if c.Kind >= NumKinds {
		bad("retry.policy", "must be off or pre-forward-once")
	}
	if c.CapTokens < 1 || c.CapTokens > MaxTokens {
		bad("retry.bucket_cap", "must be 1 to %d tokens, got %d", MaxTokens, c.CapTokens)
	}
	if c.FloorTokens < 1 || c.FloorTokens > MaxTokens {
		bad("retry.floor", "must be 1 to %d tokens, got %d", MaxTokens, c.FloorTokens)
	} else if c.FloorTokens > c.CapTokens {
		bad("retry.floor", "%d tokens is more than retry.bucket_cap (%d)", c.FloorTokens, c.CapTokens)
	}
	return errors.Join(errs...)
}

func Share(fleetFloor int64, instances int) (int64, error) {
	if instances < 1 {
		return 0, fmt.Errorf("%w: fleet.instances: must be 1 or more, got %d", ErrConfig, instances)
	}
	per := fleetFloor / int64(instances)
	if per < 1 {
		return 0, fmt.Errorf("%w: retry.floor: %d over fleet.instances %d leaves less than 1 per instance", ErrConfig, fleetFloor, instances)
	}
	return per, nil
}
