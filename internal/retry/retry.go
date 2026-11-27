package retry

import (
	"errors"
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type Failure struct {
	Reached     work.Tri
	InTx        bool
	Attempt     int
	BreakerOpen bool
	Draining    bool
	Deadline    Time
}

type Reason uint8

const (
	Granted Reason = iota
	RefusedOff
	RefusedOnce
	RefusedInTx
	RefusedReached
	RefusedBreaker
	RefusedDraining
	RefusedDeadline
	RefusedBudget
	NumReasons
)

var reasonNames = [NumReasons]string{"granted", "off", "once", "in-transaction", "reached", "breaker", "draining", "deadline", "budget"}

func (r Reason) String() string {
	if r < NumReasons {
		return reasonNames[r]
	}
	return "?"
}

type Decision struct {
	Retry  bool
	Reason Reason
}

type Policy interface {
	Attempt(now Time)
	Decide(now Time, f Failure) Decision
	Snapshot() Snapshot
}

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

func (s Snapshot) RefusedTotal() int64 {
	var n int64
	for r := RefusedOff; r < NumReasons; r++ {
		n += s.Refused[r]
	}
	return n
}

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

type Off struct{ s Snapshot }

func (o *Off) Attempt(Time) { o.s.Attempts++ }

func (o *Off) Decide(Time, Failure) Decision {
	o.s.Offered++
	o.s.Refused[RefusedOff]++
	return Decision{Reason: RefusedOff}
}

func (o *Off) Snapshot() Snapshot { return o.s }

type PreForwardOnce struct {
	plant Plant
	s     Snapshot
}

func newPreForwardOnce(cfg Config) *PreForwardOnce {
	p := &PreForwardOnce{plant: cfg.Plant}
	p.s.Kind = int64(KindPreForwardOnce)
	p.s.Cap = cfg.CapTokens * TenthsPerToken
	p.s.Floor = cfg.FloorTokens * TenthsPerToken
	p.s.Deposits = p.s.Floor
	p.s.Level.Set(p.s.Floor)
	return p
}

func (p *PreForwardOnce) Snapshot() Snapshot { return p.s }

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
