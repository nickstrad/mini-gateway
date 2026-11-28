// Package retry implements the proxy's deterministic, sans-I/O retry policies
// and integer token-bucket budget. Policies are off by default; when switched
// on, PreForwardOnce allows at most one retry per request and only when nothing
// of the request reached the database for certain.
package retry

import (
	"errors"
	"fmt"
)

// Time is a virtual-clock timestamp in nanoseconds supplied by the caller so
// the retry core stays free of wall-clock reads.
type Time int64

// Kind selects a product retry policy. The zero value (KindOff) disables
// proxy-initiated retries.
type Kind uint8

const (
	// KindOff disables proxy-initiated retries while still counting attempts
	// and offered failures.
	KindOff Kind = iota
	// KindPreForwardOnce retries a statement at most once when nothing reached
	// the database, outside a transaction, with the breaker closed, before the
	// request deadline, and within the token budget.
	KindPreForwardOnce
	// NumKinds is the sentinel count of valid product policy kinds.
	NumKinds
)

var kindNames = [NumKinds]string{"off", "pre-forward-once"}

// String returns the configuration name of k, or "?" for an out-of-range value.
func (k Kind) String() string {
	if k < NumKinds {
		return kindNames[k]
	}
	return "?"
}

// ParseKind parses a configuration policy name into its Kind.
func ParseKind(s string) (Kind, bool) {
	for k := Kind(0); k < NumKinds; k++ {
		if kindNames[k] == s {
			return k, true
		}
	}
	return 0, false
}

const (
	// TenthsPerToken scales whole tokens to integer tenths so the 10% refill
	// ratio stays exact without floating-point arithmetic.
	TenthsPerToken = 10
	// DepositTenths is the credit deposited by each forwarded first attempt
	// (one tenth of a token).
	DepositTenths = 1
	// WithdrawTenths is the cost of one granted retry (one full token).
	WithdrawTenths = TenthsPerToken
	// DefaultCapTokens is the default per-instance bucket ceiling in tokens.
	DefaultCapTokens = 100
	// DefaultFloorTokens is the default starting budget in tokens.
	DefaultFloorTokens = 10
	// MaxTokens caps retry.bucket_cap and retry.floor to keep tenths arithmetic
	// far away from int64 overflow.
	MaxTokens = 1_000_000
	// MaxAttempts caps proxy-initiated retries at one per request under
	// PreForwardOnce.
	MaxAttempts = 1
)

// Plant is a bitmask of test-only fault injections used by canaries and fuzz
// plant checks. Production configuration never sets any bit.
type Plant uint8

const (
	// PlantIgnoreReached disables the Reached == work.No guard.
	PlantIgnoreReached Plant = 1 << iota
	// PlantNoOnceBound disables the once-per-request guard.
	PlantNoOnceBound
	// PlantNoWithdraw grants retries without deducting from the bucket.
	PlantNoWithdraw
	// PlantNoCap disables clamping the bucket level at CapTokens.
	PlantNoCap
	// PlantIgnoreBreaker disables the open-breaker refusal.
	PlantIgnoreBreaker
	// PlantWithdrawFirst withdraws a token before checking eligibility so the
	// fuzz target catches ordering bugs that leak credit on refused offers.
	PlantWithdrawFirst
)

// Config configures a retry Policy. Zero values select defaults via
// WithDefaults (KindOff, DefaultCapTokens, DefaultFloorTokens).
type Config struct {
	Kind        Kind
	CapTokens   int64
	FloorTokens int64
	Plant       Plant
}

// ErrConfig wraps all retry configuration validation errors.
var ErrConfig = errors.New("retry: bad configuration")

// WithDefaults returns a copy of c with zero CapTokens and FloorTokens filled
// in. An omitted floor follows a smaller positive cap so setting bucket_cap
// below DefaultFloorTokens remains valid without also specifying floor.
func (c Config) WithDefaults() Config {
	if c.CapTokens == 0 {
		c.CapTokens = DefaultCapTokens
	}
	if c.FloorTokens == 0 {
		c.FloorTokens = DefaultFloorTokens
		// Only clamp against a valid positive cap so a negative cap does not
		// turn an omitted floor into a spurious retry.floor validation error.
		if c.CapTokens > 0 {
			c.FloorTokens = min(DefaultFloorTokens, c.CapTokens)
		}
	}
	return c
}

// Validate checks every field of c and returns a joined error naming each
// offending config key.
func (c Config) Validate() error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrConfig, key, fmt.Sprintf(format, args...)))
	}
	if c.Kind >= NumKinds {
		bad("retry.policy", "must be off or pre-forward-once")
	}
	capOK := c.CapTokens >= 1 && c.CapTokens <= MaxTokens
	if !capOK {
		bad("retry.bucket_cap", "must be 1 to %d tokens, got %d", MaxTokens, c.CapTokens)
	}
	if c.FloorTokens < 1 || c.FloorTokens > MaxTokens {
		bad("retry.floor", "must be 1 to %d tokens, got %d", MaxTokens, c.FloorTokens)
	} else if capOK && c.FloorTokens > c.CapTokens {
		bad("retry.floor", "%d tokens is more than retry.bucket_cap (%d)", c.FloorTokens, c.CapTokens)
	}
	return errors.Join(errs...)
}

// Share divides a fleet-wide retry floor across instances using integer floor
// division and fails closed if each instance would receive less than one token.
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
