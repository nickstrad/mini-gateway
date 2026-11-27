package admission

import "github.com/nickstrad/mini-gateway/internal/obs"

type tenantStats struct {
	name    string
	pushes  int64
	served  int64
	removed int64
	shed    [NumReasons]int64
	lifoOn  int64
	lifoOff int64
	hist    *obs.Histogram
}

type loadGauge struct {
	target      Time
	interval    Time
	minDelay    Time
	intervalEnd Time
	reset       bool
}

func (g *loadGauge) observe(now, delay Time) {
	if now > g.intervalEnd && !g.reset {
		g.reset = true
		g.intervalEnd = now + g.interval
	}
	if g.reset {
		g.reset = false
		g.minDelay = delay
		return
	}
	g.minDelay = min(g.minDelay, delay)
}

func (g *loadGauge) load() int64 { return LoadFromDelay(g.minDelay, g.target) }

func LoadFromDelay(minDelay, target Time) int64 {
	if minDelay <= 0 {
		return 0
	}
	if minDelay >= 2*target {
		return 100
	}
	return 100 * minDelay / (2 * target)
}

type tenantQueue struct {
	name         string
	ring         ring
	rule         QueueRule
	lifo         bool
	lastOverload Time
	gauge        loadGauge
	emptyAt      Time
	shedAt       Time
	hasShed      bool
	stats        *tenantStats
}

func (t *tenantQueue) empty() bool { return t.ring.len() == 0 }

func (t *tenantQueue) overloaded(now Time) bool { return t.rule.Overloaded(now, t.ring.len()) }

func (t *tenantQueue) observeLIFO(now Time, cfg *Config) {
	if cfg.LIFO == LIFOOff {
		return
	}
	if t.overloaded(now) {
		t.lastOverload = now
		if !t.lifo {
			t.lifo = true
			t.stats.lifoOn++
		}
		return
	}
	if t.lifo && (cfg.Plant&PlantNoLIFOHysteresis != 0 || now-t.lastOverload >= cfg.LIFOHold) {
		t.lifo = false
		t.stats.lifoOff++
	}
}

func (t *tenantQueue) next(cfg *Config, last Time) Time {
	var at Time
	earliest := func(v Time) {
		if v != 0 && (at == 0 || v < at) {
			at = v
		}
	}
	if t.empty() {
		if !t.lifo {
			earliest(t.emptyAt + cfg.Quiet())
		}
	} else if cfg.Plant&PlantNoHeadSweep == 0 {
		earliest(t.rule.Next(t.ring.front().enq))
	}
	if t.lifo && cfg.LIFO == LIFOAdaptive && !t.overloaded(last) {
		earliest(t.lastOverload + cfg.LIFOHold)
	}
	if t.hasShed && last < t.shedAt+cfg.Interval {
		earliest(t.shedAt + cfg.Interval)
	}
	return at
}

func (t *tenantQueue) oldestAge(now Time) Time {
	if t.empty() {
		return 0
	}
	return now - t.ring.front().enq
}
