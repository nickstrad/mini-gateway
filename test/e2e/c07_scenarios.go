package e2e

import (
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

const (
	poolRate       = 50
	poolInterval   = time.Second / poolRate
	poolStmt       = 500 * time.Millisecond
	poolCheckIdle  = 2 * time.Second
	poolIdle       = 20 * time.Second
	heavySeeds     = 3
	poolSweepSeeds = 20
)

type PoolScenario struct {
	Spec   Spec
	Heavy  bool
	Direct bool
	Check  func(r *SimRun) error
}

func corePool(edit func(*pool.Config)) *pool.Config {
	c := &pool.Config{RatePerSec: poolRate, MaxConnecting: 4, CheckAfterIdle: poolCheckIdle, IdleTimeout: poolIdle,
		MaxLifetime: 30 * time.Minute, BreakerFailures: 3, BreakerOpenFor: time.Second, Seed: 7}
	if edit != nil {
		edit(c)
	}
	return c
}

func withCore(sp Spec, name string, edit func(*pool.Config)) Spec {
	sp.Name = name
	sp.Pool = corePool(edit)
	if sp.Path == nil {
		sp.Path = &PathPlan{}
	}
	return sp
}

func poolTimeouts() proxy.Timeouts {
	return proxy.Timeouts{Statement: poolStmt, QueueWait: 3 * time.Second, DBConnect: time.Second, Cleanup: 300 * time.Millisecond,
		Reset: 300 * time.Millisecond, IdleInTx: time.Second}
}

func loopScript(tenant int, start, gap time.Duration, n int, sql func(i int) string) Script {
	st := make([]Step, n)
	for i := range st {
		st[i] = Step{SQL: sql(i), Delay: gap}
	}
	return Script{Tenant: tenant, Start: start, Steps: st}
}

func manyClients(name string, n, conns int, start func(i int) time.Duration, steps func(i int) []Step) Spec {
	sp := Spec{Name: name, Tenants: 4, Conns: conns, QueueCap: n, Rows: clientRows(4), Timeouts: poolTimeouts(),
		Duration: 20 * time.Second, Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 2 * time.Millisecond}}}}
	for i := 0; i < n; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: start(i), Steps: steps(i)})
	}
	sp.Timeouts.QueueWait = 15 * time.Second
	return sp
}

func HardCapSpec(plant pool.Plant) Spec {
	sp := manyClients("pool-hard-cap", 1000, 8, func(i int) time.Duration { return time.Duration(i%50) * time.Microsecond },
		func(i int) []Step { return []Step{{SQL: read(i%4, "a")}} })
	sp = withCore(sp, sp.Name, func(c *pool.Config) { c.RatePerSec = 1000; c.MaxConnecting = 8; c.Plant = plant })
	sp.FakeConns = 1000
	return sp
}

func StormSpec() Spec {
	steps := func(i int) []Step {
		return []Step{{SQL: read(i%4, "a")}, {SQL: read(i%4, "b"), Delay: 10 * time.Millisecond, Vanish: VanishAfterReply}}
	}
	sp := manyClients("pool-storm", 500, 8, func(i int) time.Duration { return time.Duration(i) * 100 * time.Microsecond }, steps)
	for i := 0; i < 500; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: 2*time.Second + time.Duration(i%50)*time.Millisecond,
			Steps: []Step{{SQL: read(i%4, "a")}, {SQL: "SELECT 1", Delay: 5 * time.Millisecond}}})
	}
	sp = withCore(sp, sp.Name, func(c *pool.Config) { c.RatePerSec = 200; c.MaxConnecting = 8 })
	sp.FakeConns = 1000
	return sp
}

func ColdStartSpec(plant pool.Plant) Spec {
	sp := manyClients("pool-cold-start", 200, 32, func(int) time.Duration { return 0 },
		func(i int) []Step { return steps(read(i%4, "a"), read(i%4, "b")) })
	sp.Service = fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 200 * time.Millisecond}}}
	return withCore(sp, sp.Name, func(c *pool.Config) { c.RatePerSec = 20; c.MaxConnecting = 32; c.Plant = plant })
}

func LifetimesSpec(noJitter bool) Spec {
	sp := Spec{Name: "pool-lifetimes", Tenants: 4, Conns: 16, QueueCap: 256, Rows: clientRows(4), Timeouts: poolTimeouts(),
		Duration: 40 * time.Second, Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 20 * time.Millisecond}}}}
	for c := 0; c < 16; c++ {
		sp.Clients = append(sp.Clients, loopScript(c%4, time.Duration(c)*time.Millisecond, 50*time.Millisecond, 250,
			func(int) string { return read(c%4, "a") }))
	}
	return withCore(sp, sp.Name, func(c *pool.Config) {
		c.RatePerSec, c.MaxConnecting, c.MaxLifetime, c.NoJitter = 1000, 16, 10*time.Second, noJitter
		c.JitterPermille = 100
	})
}

func RestartSpec(breaker bool) Spec {
	sp := Spec{Name: "pool-db-restart", Tenants: 2, Conns: 4, QueueCap: 64, Rows: clientRows(4), Timeouts: poolTimeouts(),
		Duration: 12 * time.Second}
	sp.Faults = []sim.Fault{{At: sim.Time(2 * time.Second), Kind: fakepg.FaultRestart, Arg2: int64(3 * time.Second)}}
	for c := 0; c < 8; c++ {
		sp.Clients = append(sp.Clients, loopScript(c%2, time.Duration(c)*time.Millisecond, 50*time.Millisecond, 180,
			func(i int) string { return read(c%4, "a") }))
	}
	return withCore(sp, sp.Name, func(c *pool.Config) {
		c.RatePerSec = 20
		if !breaker {
			c.BreakerFailures = pool.MaxBreakerFailures
		}
	})
}

func AdmissionRestartSpec() Spec {
	sp := RestartSpec(true)
	sp.Name = "pool-admission-restart"
	sp.Admission = admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOAdaptive}
	return sp
}

func IdleInTxSpec() Spec {
	sp := Spec{Name: "pool-idle-in-tx", Tenants: 2, Conns: 1, QueueCap: 16, Rows: clientRows(2), Timeouts: poolTimeouts(), Duration: 5 * time.Second}
	sp.Clients = []Script{
		{Tenant: 0, Steps: []Step{{SQL: "BEGIN"}, {SQL: update(0, "a", "held")}, {SQL: "COMMIT", Delay: 3 * time.Second}}},
		{Tenant: 1, Start: 100 * time.Millisecond, Steps: steps(read(0, "a"), read(1, "a"))},
	}
	return withCore(sp, sp.Name, nil)
}

func CancelSpec() Spec {
	sp := Spec{Name: "pool-cancel", Tenants: 2, Conns: 2, QueueCap: 16, Rows: clientRows(2), Duration: 5 * time.Second,
		Timeouts: proxy.Timeouts{Statement: 5 * time.Second, Cleanup: 300 * time.Millisecond}}
	sp.Clients = []Script{
		{Tenant: 0, Steps: []Step{{SQL: "SELECT pg_sleep(2)", Cancel: 100 * time.Millisecond}, {SQL: "SELECT 7", Delay: 10 * time.Millisecond}}},
		{Tenant: 1, Start: 5 * time.Millisecond, Steps: []Step{{SQL: "SELECT pg_sleep(1)"}, {SQL: "SELECT 8"}}},
		{Tenant: 1, Start: 300 * time.Millisecond, Steps: []Step{{SQL: read(1, "a"), Cancel: 50 * time.Millisecond}, {SQL: "SELECT 9", Delay: 2 * time.Second}}},
	}
	return withCore(sp, sp.Name, nil)
}

func PolicySpec(reset proxy.ResetPolicy, reseed bool, plant pool.Plant) Spec {
	sp := sessionSpec(0)
	name := "pool-session-" + reset.String()
	if reseed {
		name += "-reseed"
	}
	sp = withCore(sp, name, func(c *pool.Config) { c.Plant = plant })
	sp.Reset, sp.Reseed = reset, reseed
	sp.Path = &PathPlan{RewriteReseed: reseed}
	return sp
}

const (
	pathLoopers     = 2
	pathLoopStart   = time.Second
	pathDeath       = 1500 * time.Millisecond
	slotsReap       = 4 * time.Second
	slotsLateClient = pathDeath + slotsReap + 2*time.Second
)

func pathSpec(name string, plan PathPlan, edit func(*pool.Config)) Spec {
	sp := Spec{Name: name, Tenants: 4, Conns: 6, QueueCap: 64, Rows: clientRows(6), Timeouts: poolTimeouts(), Duration: 15 * time.Second,
		Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 30 * time.Millisecond}}}}
	for c := 0; c < 6; c++ {
		sp.Clients = append(sp.Clients, Script{Tenant: c % 4, Start: time.Duration(c) * 3 * time.Millisecond,
			Steps: []Step{{SQL: "SELECT pg_sleep(0.3)", Delay: 100 * time.Millisecond}}})
	}
	for c := 0; c < pathLoopers; c++ {
		sp.Clients = append(sp.Clients, loopScript(c%4, pathLoopStart+time.Duration(c)*7*time.Millisecond, 40*time.Millisecond, 100,
			func(i int) string { return read(c, "a") }))
	}
	sp.Timeouts.QueueWait = 5 * time.Second
	p := plan
	sp.Path = &p
	return withCore(sp, name, edit)
}

func MassDeathSpec(f PathFault, plant pool.Plant) Spec {
	name := "pool-mass-" + f.String()
	ev := PathEvent{At: pathDeath, Fault: f}
	return pathSpec(name, PathPlan{Events: []PathEvent{ev}}, func(c *pool.Config) { c.Plant = plant; c.CheckAfterIdle = 5 * time.Second })
}

func PoliteCloseSpec() Spec {
	ev := PathEvent{At: 2 * time.Second, Fault: PathPoliteClose, Target: faultTargetIdleAge, MinAge: time.Second}
	return pathSpec("pool-polite-close", PathPlan{Events: []PathEvent{ev}}, nil)
}

func CheckHangsSpec(plant pool.Plant) Spec {
	sp := Spec{Name: "pool-check-hangs", Tenants: 2, Conns: 2, QueueCap: 16, Rows: clientRows(2), Timeouts: poolTimeouts(), Duration: 8 * time.Second}
	sp.Clients = []Script{
		{Tenant: 0, Steps: steps(read(0, "a"))},
		{Tenant: 1, Start: 4 * time.Second, Steps: steps(read(1, "a"), read(1, "b"))},
	}
	ev := PathEvent{At: 3 * time.Second, Fault: PathBlackhole, Target: faultTargetIdleAge, MinAge: poolCheckIdle}
	sp.Path = &PathPlan{Events: []PathEvent{ev}}
	return withCore(sp, sp.Name, func(c *pool.Config) { c.Plant = plant })
}

func SlotsHeldSpec() Spec {
	ev := PathEvent{At: pathDeath, Fault: PathBlackhole}
	sp := pathSpec("pool-slots-held", PathPlan{Events: []PathEvent{ev}, ReapAfter: slotsReap}, func(c *pool.Config) { c.RatePerSec = 10 })
	sp.FakeConns = 5
	sp.Duration = 20 * time.Second
	sp.Clients = append(sp.Clients, Script{Tenant: 0, Start: slotsLateClient, Steps: steps(read(0, "a"), read(0, "a"), read(0, "a"))})
	return sp
}

type Remedies struct{ Keepalive, IdleTimeout, Check bool }

func (m Remedies) name() string {
	var parts []string
	if m.Keepalive {
		parts = append(parts, "keepalive")
	}
	if m.IdleTimeout {
		parts = append(parts, "idle")
	}
	if m.Check {
		parts = append(parts, "check")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "-")
}

const (
	nightDrop    = time.Hour
	nightTrickle = 2 * time.Hour
	nightGap     = 5 * time.Second
	remedyOff    = 23 * time.Hour
)

func NightTrickleSpec(m Remedies) Spec {
	sp := Spec{Name: "pool-night-" + m.name(), Tenants: 2, Conns: 8, QueueCap: 64, Rows: clientRows(8),
		Timeouts: poolTimeouts(), Duration: nightTrickle + 2*time.Minute,
		Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 40 * time.Millisecond}}}}
	sp.Timeouts.ClientIdle = 24 * time.Hour
	sp.Flush = time.Minute
	for c := 0; c < 8; c++ {
		sp.Clients = append(sp.Clients, Script{Tenant: c % 2, Start: time.Duration(c) * time.Millisecond, Steps: steps(read(c, "a"))})
	}
	sp.Clients = append(sp.Clients, loopScript(0, time.Second, nightGap, int(nightTrickle/nightGap), func(int) string { return read(0, "b") }))
	for c := 0; c < 8; c++ {
		sp.Clients = append(sp.Clients, loopScript(c%2, nightTrickle+time.Duration(c)*time.Millisecond, 50*time.Millisecond, 20,
			func(int) string { return read(c, "a") }))
	}
	plan := PathPlan{IdleDrop: nightDrop}
	if m.Keepalive {
		plan.Keepalive = 30 * time.Second
	}
	sp.Path = &plan
	return withCore(sp, sp.Name, func(c *pool.Config) {
		c.MaxLifetime = 24 * time.Hour
		c.IdleTimeout = remedyOff
		c.CheckAfterIdle = remedyOff
		if m.IdleTimeout {
			c.IdleTimeout = 180 * time.Second
		}
		if m.Check {
			c.CheckAfterIdle = 30 * time.Second
		}
	})
}

func CoreRerun(sp Spec, name string) Spec { return withCore(sp, name, nil) }

func PoolScenarios() []PoolScenario {
	out := []PoolScenario{
		{Spec: CoreRerun(ResultsSpec("", false), "pool-results"), Check: checkResults},
		{Spec: CoreRerun(TxIntegritySpec(), "pool-tx-integrity"), Check: checkTxIntegrity},
		{Spec: CoreRerun(slowReaderSpec(), "pool-slow-reader"), Check: checkPoolSlowReader},
		{Spec: CoreRerun(vanishSpec(), "pool-vanish"), Check: checkPoolVanish},
		{Spec: HardCapSpec(0), Heavy: true, Check: checkHardCap},
		{Spec: StormSpec(), Heavy: true, Direct: true, Check: checkStorm},
		{Spec: ColdStartSpec(0), Check: checkColdStart},
		{Spec: LifetimesSpec(false), Heavy: true, Check: checkLifetimes},
		{Spec: RestartSpec(true), Check: checkRestart},
		{Spec: AdmissionRestartSpec(), Check: checkAdmissionRestart},
		{Spec: IdleInTxSpec(), Check: checkIdleInTx},
		{Spec: CancelSpec(), Check: checkCancel},
		{Spec: MassDeathSpec(PathBlackhole, 0), Check: checkMassDeath},
		{Spec: MassDeathSpec(PathPeerForgot, 0), Check: checkMassDeath},
		{Spec: PoliteCloseSpec(), Check: checkPoliteClose},
		{Spec: CheckHangsSpec(0), Check: checkCheckHangs},
		{Spec: SlotsHeldSpec(), Check: checkSlotsHeld},
		{Spec: NightTrickleSpec(Remedies{true, true, true}), Heavy: true, Check: checkNight(true)},
	}
	for _, p := range []struct {
		reset  proxy.ResetPolicy
		reseed bool
	}{{proxy.ResetAlways, false}, {proxy.ResetOnSwitch, false}, {proxy.ResetAlways, true}, {proxy.ResetOnSwitch, true}} {
		out = append(out, PoolScenario{Spec: PolicySpec(p.reset, p.reseed, 0), Check: checkPolicy(p.reset, p.reseed)})
	}
	return out
}
