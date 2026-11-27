package retry

import (
	"fmt"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/work"
)

type opKind uint8

const (
	opAttempt opKind = iota
	opFail
	numOpKinds
)

type op struct {
	kind opKind
	dt   Time
	f    Failure
	ttl  Time
}

func (o op) String() string {
	if o.kind == opAttempt {
		return fmt.Sprintf("+%d attempt", o.dt)
	}
	return fmt.Sprintf("+%d fail reached=%v intx=%v attempt=%d breaker=%v draining=%v ttl=%d", o.dt, o.f.Reached, o.f.InTx,
		o.f.Attempt, o.f.BreakerOpen, o.f.Draining, o.ttl)
}

type refModel struct {
	cfg      Config
	tenths   int64
	attempts int64
	retries  int64
	refused  map[Reason]int64
}

func newRef(cfg Config) *refModel {
	cfg = cfg.WithDefaults()
	r := &refModel{cfg: cfg, refused: map[Reason]int64{}}
	if cfg.Kind == KindPreForwardOnce {
		r.tenths = cfg.FloorTokens * 10
	}
	return r
}

func (r *refModel) attempt() {
	r.attempts++
	if r.cfg.Kind != KindPreForwardOnce {
		return
	}
	r.tenths++
	if r.tenths > r.cfg.CapTokens*10 {
		r.tenths = r.cfg.CapTokens * 10
	}
}

func (r *refModel) decide(now Time, f Failure) Decision {
	if r.cfg.Kind == KindOff {
		r.refused[RefusedOff]++
		return Decision{Reason: RefusedOff}
	}
	checks := []struct {
		refuse bool
		why    Reason
	}{
		{f.Attempt > 0, RefusedOnce},
		{f.InTx, RefusedInTx},
		{f.Reached == work.Yes || f.Reached == work.Unknown || f.Reached > work.Unknown, RefusedReached},
		{f.BreakerOpen, RefusedBreaker},
		{f.Draining, RefusedDraining},
		{!(now < f.Deadline), RefusedDeadline},
		{r.tenths < 10, RefusedBudget},
	}
	for _, c := range checks {
		if c.refuse {
			r.refused[c.why]++
			return Decision{Reason: c.why}
		}
	}
	r.tenths -= 10
	r.retries++
	return Decision{Retry: true}
}

type window struct {
	attempts []int64
	retries  []int64
	levels   []int64
}

func (w *window) record(attempts, retries, level int64) {
	w.attempts = append(w.attempts, attempts)
	w.retries = append(w.retries, retries)
	w.levels = append(w.levels, level)
}

func (w *window) check(cap, floor int64) error {
	for i := range w.attempts {
		for j := i; j < len(w.attempts); j++ {
			da, dr := w.attempts[j]-w.attempts[i], w.retries[j]-w.retries[i]
			if dr*10 > da+w.levels[i] {
				return fmt.Errorf("window %d..%d: %d retries for %d first attempts with %d tenths carried in", i, j, dr, da, w.levels[i])
			}
			if dr*10 > da+cap {
				return fmt.Errorf("window %d..%d: %d retries above a tenth of %d first attempts plus the cap %d", i, j, dr, da, cap)
			}
		}
		if w.retries[i]*10 > w.attempts[i]+floor {
			return fmt.Errorf("from the start to %d: %d retries for %d first attempts and the floor %d", i, w.retries[i], w.attempts[i], floor)
		}
	}
	return nil
}

func runOps(cfg Config, ops []op) ([]string, error) {
	p, err := New(cfg)
	if err != nil {
		return nil, err
	}
	ref := newRef(cfg)
	var trail []string
	var now Time
	var w window
	w.record(0, 0, ref.tenths)
	for i, o := range ops {
		now += o.dt
		trail = append(trail, o.String())
		switch o.kind {
		case opAttempt:
			p.Attempt(now)
			ref.attempt()
		case opFail:
			f := o.f
			f.Deadline = now + o.ttl
			got, want := p.Decide(now, f), ref.decide(now, f)
			if got != want {
				return trail, fmt.Errorf("op %d: decision %+v, reference %+v", i, got, want)
			}
		}
		s := p.Snapshot()
		if err := s.Check(); err != nil {
			return trail, fmt.Errorf("op %d: %w", i, err)
		}
		if err := agree(s, ref); err != nil {
			return trail, fmt.Errorf("op %d: %w", i, err)
		}
		w.record(s.Attempts, s.Retries, s.Level.Value)
	}
	cfg = cfg.WithDefaults()
	if cfg.Kind == KindPreForwardOnce {
		if err := w.check(cfg.CapTokens*10, cfg.FloorTokens*10); err != nil {
			return trail, err
		}
	}
	return trail, nil
}

func agree(s Snapshot, ref *refModel) error {
	if s.Level.Value != ref.tenths || s.Attempts != ref.attempts || s.Retries != ref.retries {
		return fmt.Errorf("level %d attempts %d retries %d, reference %d %d %d", s.Level.Value, s.Attempts, s.Retries,
			ref.tenths, ref.attempts, ref.retries)
	}
	for r := RefusedOff; r < NumReasons; r++ {
		if s.Refused[r] != ref.refused[r] {
			return fmt.Errorf("refused %s %d, reference %d", r, s.Refused[r], ref.refused[r])
		}
	}
	return nil
}

func trailText(t []string) string {
	if len(t) > 40 {
		t = append(t[:20:20], append([]string{"..."}, t[len(t)-20:]...)...)
	}
	return strings.Join(t, "\n")
}
