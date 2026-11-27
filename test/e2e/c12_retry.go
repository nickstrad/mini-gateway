package e2e

import (
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/retry/retrytest"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

type RetryArm uint8

const (
	ArmOff RetryArm = iota
	ArmOn
	ArmEverything
	NumRetryArms
)

var retryArmNames = [NumRetryArms]string{"off", "on", "everything"}

func (a RetryArm) String() string {
	if a < NumRetryArms {
		return retryArmNames[a]
	}
	return "?"
}

const (
	retryFlush      = 500 * time.Millisecond
	retryCapTokens  = 20
	retryFloor      = 4
	retryClientGap  = 25 * time.Millisecond
	retryWriteTime  = 2 * time.Millisecond
	fastBreakerBest = 5 * time.Millisecond
	fastShedBound   = 110 * time.Millisecond
	retrySlowBound  = 1100 * time.Millisecond
)

type RetrySpec struct {
	Name     string
	Clients  int
	Tenants  int
	Conns    int
	Phases   []Phase
	Duration time.Duration
	Relay    RelayPlan
	Breaker  int
	Recovery time.Duration
	Plant    retry.Plant
}

type RetryRun struct {
	Spec    RetrySpec
	Arm     RetryArm
	Seed    int64
	World   *proxysim.World
	Relay   *retryRelay
	Clients []RetryClientResult
	Engine  proxy.Snapshot
	Policy  retry.Snapshot
	Flush   *simrun.Run
	Final   obs.InstanceSnapshot
}

func StormPattern(n int64) (KillPoint, bool) {
	switch n % 10 {
	case 0:
		return KillPoint(n / 10 % int64(NumKillPoints)), true
	case 3:
		return FaultDoom, true
	case 6:
		return KillAfterApplied, true
	}
	return 0, false
}

func SweepPattern(n int64) (KillPoint, bool) {
	if n%2 != 0 {
		return 0, false
	}
	return KillPoint(n / 2 % int64(NumKillPoints+1)), true
}

func RetryStormSpec() RetrySpec {
	return RetrySpec{Name: "retry-storm", Clients: 8, Tenants: 4, Conns: 4, Duration: 4 * time.Second,
		Phases:  []Phase{{0, 450 * time.Millisecond}, {700 * time.Millisecond, 3500 * time.Millisecond}},
		Relay:   RelayPlan{From: 700 * time.Millisecond, Until: 1700 * time.Millisecond, DoomAllAt: 600 * time.Millisecond, Pattern: StormPattern},
		Breaker: 3, Recovery: 3200 * time.Millisecond}
}

func RetrySweepSpec() RetrySpec {
	return RetrySpec{Name: "retry-no-double-apply", Clients: 6, Tenants: 2, Conns: 4, Duration: 3 * time.Second,
		Phases:  []Phase{{0, 250 * time.Millisecond}, {400 * time.Millisecond, 2500 * time.Millisecond}},
		Relay:   RelayPlan{From: 400 * time.Millisecond, Until: 1200 * time.Millisecond, DoomAllAt: 300 * time.Millisecond, Pattern: SweepPattern},
		Breaker: pool.MaxBreakerFailures, Recovery: 1700 * time.Millisecond}
}

func (sp RetrySpec) engineConfig(arm RetryArm) proxy.Config {
	c := proxy.Config{Connections: sp.Conns, QueueCap: 256, Timeouts: poolTimeouts(), MaxClients: 1024, MaxStartup: 1024,
		Pool:      corePool(func(p *pool.Config) { p.BreakerFailures = sp.Breaker; p.RatePerSec = 500; p.MaxConnecting = sp.Conns }),
		Admission: admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOAdaptive}}
	switch arm {
	case ArmOn:
		c.Retry = retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: retryCapTokens, FloorTokens: retryFloor, Plant: sp.Plant}
	case ArmEverything:
		c.Seams.Retry = proxy.NewRetryCore(retrytest.NewEverything())
	}
	return c
}

func retryFingerprint(sp RetrySpec, arm RetryArm) string {
	return fmt.Sprintf("%s-%s-c%d-n%d-b%d-p%d", sp.Name, arm, sp.Conns, sp.Clients, sp.Breaker, sp.Plant)
}

func RunRetry(sp RetrySpec, arm RetryArm, seed int64, dir, replay string) (*RetryRun, error) {
	relay := newRetryRelay(sp.Relay)
	fake := fakepg.Config{Rows: clientRows(sp.Clients), MaxConnections: 256,
		Service:      fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindUpdate, Time: retryWriteTime}}},
		StatementLog: fakepg.LogConfig{Cap: logCap}, WriteLog: fakepg.LogConfig{Cap: logCap}}
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed, Scenario: sp.Name, Replay: replay, Tenants: sp.Tenants, Fake: fake,
		Engine: sp.engineConfig(arm), DBAddr: retryAddr, BeforeProxy: relay.listen})
	if err != nil {
		return nil, err
	}
	r := &RetryRun{Spec: sp, Arm: arm, Seed: seed, World: w, Relay: relay, Clients: make([]RetryClientResult, sp.Clients)}
	run := func() error {
		for i := range sp.Clients {
			c := &retryClient{s: w.S, net: w.Net, addr: w.Proxy.Addr(), opts: w.Cfg.Options, idx: i, tenant: i % sp.Tenants,
				phases: sp.Phases, gap: retryClientGap + time.Duration(i)*time.Millisecond, res: &r.Clients[i]}
			c.start()
		}
		if err := w.S.RunFor(sp.Duration); err != nil {
			return err
		}
		return w.Proxy.Finish()
	}
	if dir == "" {
		err = run()
	} else {
		srcs := append(enginesrc.Sources(w.Proxy.Engine, nil), simsrc.Kernel(w.S), simsrc.Net(w.Net), simsrc.Fake(w.Fake),
			obs.NewSource(obs.ComponentWorkload, simsrc.WorkloadMetrics(), r.WorkloadSnapshot, simsrc.MapWorkload))
		r.Flush, err = simrun.Custom(sp.Name, seed, retryFingerprint(sp, arm), simrun.Options{Dir: dir, Arm: arm.String(), Replay: replay,
			Interval: retryFlush}, w.S, srcs, run)
		if r.Flush != nil {
			r.Final = r.Flush.Final
		}
	}
	r.Engine = w.Proxy.Engine.Snapshot()
	r.Policy, _ = w.Proxy.Engine.RetrySnapshot()
	if err != nil {
		return r, fmt.Errorf("%s/%s seed %d: %w", sp.Name, arm, seed, err)
	}
	if err := w.S.Err(); err != nil {
		return r, err
	}
	return r, r.Engine.Check()
}

func (r *RetryRun) applied() map[string]int {
	out := map[string]int{}
	for _, rec := range r.World.Fake.WriteLog() {
		out[rec.Text]++
	}
	return out
}

func (r *RetryRun) Duplicates() int {
	n := 0
	for _, c := range r.applied() {
		if c > 1 {
			n++
		}
	}
	return n
}

func (r *RetryRun) forbiddenRetries() int64 {
	var n int64
	for id, v := range r.Engine.RetryRows {
		if v > 0 && pgwire.ErrRowID(id).ProxyRetry() == pgwire.ProxyRetryNever {
			n += v
		}
	}
	return n
}

func (r *RetryRun) CheckProduct() error {
	if len(r.World.Fake.WriteLog()) >= logCap {
		return failf("write log full: %d records", logCap)
	}
	applied := r.applied()
	for text, n := range applied {
		if n > 1 {
			return failf("statement applied %d times: %s", n, text)
		}
	}
	var ops, ok int64
	for _, c := range r.Clients {
		if c.Overflow > 0 {
			return failf("client error log overflowed")
		}
		for _, o := range c.Ops {
			ops++
			if o.OK {
				ok++
				if applied[o.Text] != 1 {
					return failf("acknowledged statement applied %d times: %s", applied[o.Text], o.Text)
				}
			}
		}
	}
	if ops == 0 || ok == 0 {
		return failf("%d operations, %d acknowledged", ops, ok)
	}
	if n := r.forbiddenRetries(); n > 0 {
		return failf("%d retries on error rows that never allow one: %v", n, r.Engine.RetryRows)
	}
	if err := r.checkFast(); err != nil {
		return err
	}
	if err := r.checkRecovery(); err != nil {
		return err
	}
	return r.checkBudget()
}

func (r *RetryRun) checkFast() error {
	for i, c := range r.Clients {
		for _, e := range c.Errors {
			bound := retrySlowBound
			switch e.Code {
			case "53M03":
				bound = fastBreakerBest
			case "53M01":
				bound = fastShedBound
			}
			if e.Elapsed > bound {
				return failf("client %d: %s answered after %s, bound %s", i, e.Code, e.Elapsed, bound)
			}
		}
	}
	return nil
}

func (r *RetryRun) checkRecovery() error {
	from := r.Spec.Recovery
	for i, c := range r.Clients {
		late := 0
		for _, o := range c.Ops {
			if time.Duration(o.Start) < from {
				continue
			}
			late++
			if !o.OK || o.Attempts != 1 {
				return failf("client %d: operation at %s after the faults ended: ok %v attempts %d code %s", i, time.Duration(o.Start), o.OK, o.Attempts, o.Code)
			}
		}
		if late == 0 {
			return failf("client %d: no operation after %s", i, from)
		}
	}
	return nil
}

func (r *RetryRun) checkBudget() error {
	p, e := r.Policy, r.Engine
	if p.Offered != e.RetryOffered || p.Retries != e.RetryAsked {
		return failf("policy offered %d retried %d, engine %d %d", p.Offered, p.Retries, e.RetryOffered, e.RetryAsked)
	}
	if e.RetryPending.Value != 0 {
		return failf("%d retries pending after the run", e.RetryPending.Value)
	}
	if r.Arm != ArmOn {
		if p.Retries != 0 {
			return failf("%d retries with the policy off", p.Retries)
		}
		return nil
	}
	if p.Retries == 0 || e.RetriedRequests == 0 {
		return failf("no retry with the policy on: %+v", p)
	}
	if p.Retries*retry.WithdrawTenths > p.Attempts*retry.DepositTenths+p.Floor {
		return failf("%d retries for %d first attempts and the floor %d", p.Retries, p.Attempts, p.Floor)
	}
	if forwards := e.StagePassed[proxy.StageForward]; forwards != p.Attempts+p.Retries {
		return failf("%d statements forwarded for %d first attempts and %d retries", forwards, p.Attempts, p.Retries)
	}
	return nil
}

func (r *RetryRun) CheckAblation() error {
	if r.forbiddenRetries() == 0 {
		return failf("the retry-everything arm retried no failure the contract forbids")
	}
	if r.Policy.Retries <= r.Engine.RetriedRequests && r.Engine.RetriedRequests == 0 {
		return failf("the retry-everything arm retried nothing")
	}
	return nil
}

func (r *RetryRun) Summary() string {
	s := r.Relay.Stats()
	return fmt.Sprintf("%s/%s seed %d: queries %d faulted %d kills %v dooms %d/%d skipped %d; policy attempts %d retries %d refused %v level %d; engine retried %d ends %v rows-forbidden %d; duplicates %d",
		r.Spec.Name, r.Arm, r.Seed, s.Queries, s.Faulted, s.Kills, s.Dooms, s.DoomsArmed, s.DoomsSkipped, r.Policy.Attempts, r.Policy.Retries,
		r.Policy.Refused, r.Policy.Level.Value, r.Engine.RetriedRequests, r.Engine.RetryEnds, r.forbiddenRetries(), r.Duplicates())
}

func RetryScenarioNames() []string { return []string{RetryStormSpec().Name, RetrySweepSpec().Name} }

func RetryBaseline(dir string) ([]obs.InstanceSnapshot, error) {
	var out []obs.InstanceSnapshot
	for _, sp := range []RetrySpec{RetryStormSpec(), RetrySweepSpec()} {
		for arm := ArmOff; arm < NumRetryArms; arm++ {
			r, err := RunRetry(sp, arm, 1, dir, "")
			if err == nil && arm != ArmEverything {
				err = r.CheckProduct()
			}
			if err == nil && arm == ArmEverything {
				err = r.CheckAblation()
			}
			if err == nil {
				err = r.Flush.Check()
			}
			if err != nil {
				return nil, err
			}
			out = append(out, r.Final)
		}
	}
	return out, nil
}

func (r *RetryRun) WorkloadSnapshot() workload.Snapshot {
	tenants := make([]workload.TenantSnapshot, max(r.Spec.Tenants, 1))
	empty := obs.NewDurationHistogram().Snapshot()
	for i := range tenants {
		tenants[i].Name = proxysim.TenantName(i)
		tenants[i].Latency, tenants[i].LatencyFromSend = empty, empty
	}
	out := workload.Snapshot{Clients: int64(len(r.Clients))}
	for i, c := range r.Clients {
		t := &tenants[i%len(tenants)]
		t.Reconnects += c.Redials
		for _, o := range c.Ops {
			t.Intended++
			t.Sent++
			t.Retries += int64(o.Attempts - 1)
			switch {
			case o.OK:
				t.OK++
			case o.Code == "closed":
				t.Lost++
			default:
				t.Errors++
				addCode(t, o.Code)
			}
		}
	}
	out.Tenants = tenants
	return out
}
