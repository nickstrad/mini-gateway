package retry

import (
	"errors"
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Failure describes a lost statement offered to a Policy. Reached must come
// from the driver's explicit write report, never from the error-contract row.
type Failure struct {
	Reached     work.Tri
	InTx        bool
	Attempt     int
	BreakerOpen bool
	Draining    bool
	Deadline    Time
}

// Reason classifies the outcome of Policy.Decide. Every offered failure ends in
// Granted or exactly one refusal reason.
type Reason uint8

const (
	// Granted means the policy approved one retry and withdrew a token.
	Granted Reason = iota
	// RefusedOff means the policy is KindOff.
	RefusedOff
	// RefusedOnce means the request already used its retry (Attempt >= MaxAttempts).
	RefusedOnce
	// RefusedInTx means the statement ran inside a transaction whose prior work
	// was lost with the connection.
	RefusedInTx
	// RefusedReached means the engine could not prove zero bytes reached the
	// database (Reached != work.No).
	RefusedReached
	// RefusedBreaker means the database circuit breaker is open or half-open.
	RefusedBreaker
	// RefusedDraining means the proxy instance is draining.
	RefusedDraining
	// RefusedDeadline means now is at or past the request's deadline.
	RefusedDeadline
	// RefusedBudget means the bucket holds less than one full token.
	RefusedBudget
	// NumReasons is the sentinel count of Reason values.
	NumReasons
)

var reasonNames = [NumReasons]string{"granted", "off", "once", "in-transaction", "reached", "breaker", "draining", "deadline", "budget"}

// String returns the label value of r, or "?" for an out-of-range Reason.
func (r Reason) String() string {
	if r < NumReasons {
		return reasonNames[r]
	}
	return "?"
}

// Decision is the result of offering one Failure to a Policy.
type Decision struct {
	Retry  bool
	Reason Reason
}

// Policy governs whether the proxy retries a failed statement and tracks the
// retry budget in integer tenths of a token.
type Policy interface {
	Attempt(now Time)
	Decide(now Time, f Failure) Decision
	Snapshot() Snapshot
}

// Snapshot is the plain value export of a Policy's counters and budget state.
// Level, Cap, Floor, Deposits, Withdrawals, and Expired are measured in tenths
// of a token.
type Snapshot struct {
	Kind        int64
	Attempts    int64
	Offered     int64
	Retries     int64
	Refused     [NumReasons]int64
	Level       obs.Gauge
	Cap         int64
	Floor       int64
	Deposits    int64
	Withdrawals int64
	Expired     int64
}

// RefusedTotal returns the sum of all refusal reasons (excluding Granted).
func (s Snapshot) RefusedTotal() int64 {
	var n int64
	for r := RefusedOff; r < NumReasons; r++ {
		n += s.Refused[r]
	}
	return n
}

// Check enforces exact offer and token conservation invariants on s, plus the
// withdrawal and from-start budget bounds for KindPreForwardOnce.
func (s Snapshot) Check() error {
	var errs []error
	if s.Refused[Granted] != 0 {
		errs = append(errs, fmt.Errorf("retry: %d refusals counted as granted", s.Refused[Granted]))
	}
	if err := obs.Conserve(obs.T("offered", s.Offered), obs.T("retries", s.Retries), obs.T("refused", s.RefusedTotal())); err != nil {
		errs = append(errs, err)
	}
	if err := obs.Conserve(obs.T("deposits", s.Deposits), obs.T("withdrawals", s.Withdrawals), obs.T("expired", s.Expired),
		obs.T("level", s.Level.Value)); err != nil {
		errs = append(errs, err)
	}
	if s.Level.Value < 0 || s.Level.Value > s.Cap || s.Level.Peak > s.Cap {
		errs = append(errs, fmt.Errorf("retry: budget level %d (peak %d) outside 0..%d", s.Level.Value, s.Level.Peak, s.Cap))
	}
	// Non-product kinds (such as the test-only ablation policy) intentionally
	// bypass the token budget while still conserving offers and bucket counts.
	if s.Kind != int64(KindPreForwardOnce) {
		return errors.Join(errs...)
	}
	if s.Withdrawals != s.Retries*WithdrawTenths {
		errs = append(errs, fmt.Errorf("retry: %d tenths withdrawn for %d retries", s.Withdrawals, s.Retries))
	}
	if s.Retries*WithdrawTenths > s.Attempts*DepositTenths+s.Floor {
		errs = append(errs, fmt.Errorf("retry: %d retries above a tenth of %d first attempts plus the floor %d", s.Retries, s.Attempts, s.Floor))
	}
	return errors.Join(errs...)
}

// New validates cfg (after applying defaults) and constructs the requested
// Policy. The zero Config selects Off.
func New(cfg Config) (Policy, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Kind == KindOff {
		return &Off{}, nil
	}
	return newPreForwardOnce(cfg), nil
}

// Off never grants a retry while still recording first attempts and refused
// offers for observability.
type Off struct{ s Snapshot }

// Attempt records a forwarded first attempt.
func (o *Off) Attempt(Time) { o.s.Attempts++ }

// Decide always refuses with RefusedOff.
func (o *Off) Decide(Time, Failure) Decision {
	o.s.Offered++
	o.s.Refused[RefusedOff]++
	return Decision{Reason: RefusedOff}
}

// Snapshot returns a copy of o's counters.
func (o *Off) Snapshot() Snapshot { return o.s }

// PreForwardOnce grants at most one retry per request when nothing reached the
// database, outside a transaction, with the breaker closed, not draining,
// before the request deadline, and when the token bucket holds at least one
// full token. Stored credit does not decay with time; the cap discards excess
// deposits so the largest burst after a quiet period is CapTokens.
type PreForwardOnce struct {
	plant Plant
	s     Snapshot
}

func newPreForwardOnce(cfg Config) *PreForwardOnce {
	p := &PreForwardOnce{plant: cfg.Plant}
	p.s.Kind = int64(KindPreForwardOnce)
	p.s.Cap = cfg.CapTokens * TenthsPerToken
	p.s.Floor = cfg.FloorTokens * TenthsPerToken
	// Seed Deposits with the starting floor so Deposits = Withdrawals + Expired + Level holds from time zero.
	p.s.Deposits = p.s.Floor
	p.s.Level.Set(p.s.Floor)
	return p
}

// Snapshot returns a copy of p's counters and budget gauges.
func (p *PreForwardOnce) Snapshot() Snapshot { return p.s }

// Attempt records a forwarded first attempt and deposits one tenth of a token,
// counting any overflow past the cap as Expired.
func (p *PreForwardOnce) Attempt(Time) {
	p.s.Attempts++
	p.s.Deposits += DepositTenths
	level := p.s.Level.Value + DepositTenths
	if level > p.s.Cap && p.plant&PlantNoCap == 0 {
		p.s.Expired += level - p.s.Cap
		level = p.s.Cap
	}
	p.s.Level.Set(level)
}

// Decide evaluates eligibility in fixed order and withdraws one token only
// after every non-budget check passes.
func (p *PreForwardOnce) Decide(now Time, f Failure) Decision {
	p.s.Offered++
	if p.plant&PlantWithdrawFirst != 0 && p.s.Level.Value >= WithdrawTenths {
		p.withdraw()
	}
	r := p.eligible(now, f)
	if r == Granted && p.plant&PlantWithdrawFirst == 0 {
		r = p.charge()
	}
	if r != Granted {
		p.s.Refused[r]++
		return Decision{Reason: r}
	}
	p.s.Retries++
	return Decision{Retry: true, Reason: Granted}
}

func (p *PreForwardOnce) eligible(now Time, f Failure) Reason {
	// Check InTx before Reached: even a zero-byte refusal inside a transaction
	// cannot be retried because earlier statements went with the connection.
	// Equality (now >= Deadline) refuses under the time-passes-before-event rule.
	switch {
	case f.Attempt >= MaxAttempts && p.plant&PlantNoOnceBound == 0:
		return RefusedOnce
	case f.InTx:
		return RefusedInTx
	case f.Reached != work.No && p.plant&PlantIgnoreReached == 0:
		return RefusedReached
	case f.BreakerOpen && p.plant&PlantIgnoreBreaker == 0:
		return RefusedBreaker
	case f.Draining:
		return RefusedDraining
	case now >= f.Deadline:
		return RefusedDeadline
	}
	return Granted
}

func (p *PreForwardOnce) charge() Reason {
	if p.s.Level.Value < WithdrawTenths {
		return RefusedBudget
	}
	if p.plant&PlantNoWithdraw == 0 {
		p.withdraw()
	}
	return Granted
}

func (p *PreForwardOnce) withdraw() {
	p.s.Withdrawals += WithdrawTenths
	p.s.Level.Set(p.s.Level.Value - WithdrawTenths)
}
