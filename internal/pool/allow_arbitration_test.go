package pool

import "testing"

func TestArbitrationAllowRefreshesBreakerSnapshot(t *testing.T) {
	cfg := modelConfig()
	cfg.BreakerFailures = 1
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ids := dialsIn(p.Demand(1, 1, nil))
	p.DialFailed(1, ids[0], nil)
	if !p.Allow(p.brk.openUntil) {
		t.Fatal("half-open admission refused")
	}
	s := p.Snapshot()
	if s.Breaker != int64(p.Breaker()) || s.BreakerTransitions[TransOpenHalfOpen] != 1 {
		t.Fatalf("breaker=%s snapshot=%d transitions=%v", p.Breaker(), s.Breaker, s.BreakerTransitions)
	}
}
