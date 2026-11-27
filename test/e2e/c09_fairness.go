package e2e

import (
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	FairConns           = 2
	FairFloodService    = 5 * time.Millisecond
	FairQuietService    = 10 * time.Millisecond
	FairFloodLease      = 5500 * time.Microsecond
	FairFloodCapPerS    = int64(FairConns) * int64(time.Second/FairFloodLease)
	FairFloodLoadPct    = 1000
	FairFloodClients    = 200
	FairTenantCap       = 32
	FairTotalCap        = 128
	FairFlood           = 500 * time.Millisecond
	FairRecoverAfter    = AdmRecoverAfter
	FairRecovery        = 500 * time.Millisecond
	FairRecoveryLoad    = 30
	FairRecoveryClients = 20
	FairQuietEvery      = 25 * time.Millisecond
	FairManyQuiet       = 40
	FairManyEvery       = time.Second
	FairManyStagger     = FairFlood / FairManyQuiet
	FairSyncAt          = 200 * time.Millisecond
	FairFlush           = time.Second
	FairHeldConns       = 4
	FairHeldClients     = 8
	FairHeldTx          = 3
	FairHeldThink       = 20 * time.Millisecond
	FairHeldStart       = 100 * time.Millisecond
	FairQuietClients    = 8
	FairQuietRequests   = 200
	FairQuietGap        = time.Millisecond
)

type FairArm struct {
	Name       string
	Rule       admission.RuleKind
	Flood      bool
	Plant      admission.FairPlant
	ProxyPlant proxy.Plant
}

var (
	FairArmFair  = FairArm{Name: "fair", Rule: admission.RuleTimeout, Flood: true}
	FairArmAlone = FairArm{Name: "alone", Rule: admission.RuleTimeout}
	FairArmFIFO  = FairArm{Name: "fifo", Flood: true}
)

type FairKind uint8

const (
	FairNoisy FairKind = iota + 1
	FairMany
	FairHeld
	FairSync
)

type FairSpec struct {
	Scenario     string
	Kind         FairKind
	Arm          FairArm
	FloodService time.Duration
	QuietEvery   time.Duration
	SyncQuiet    int
}

type FairRun struct {
	Spec       FairSpec
	Seed       int64
	World      *proxysim.World
	Mid        workload.Snapshot
	Phase      workload.Snapshot
	Final      workload.Snapshot
	Adm        admission.Snapshot
	Engine     proxy.Snapshot
	Flush      *simrun.Run
	HeldPeak   int
	Watched    bool
	HeldOpen   int
	Overlapped []bool
}

func NoisySpec(arm FairArm) FairSpec {
	return FairSpec{Scenario: "fair-noisy-neighbour", Kind: FairNoisy, Arm: arm}
}

func ManySpec(arm FairArm) FairSpec {
	return FairSpec{Scenario: "fair-many-tenants", Kind: FairMany, Arm: arm}
}

func HeldSpec(arm FairArm) FairSpec {
	return FairSpec{Scenario: "fair-held-transactions", Kind: FairHeld, Arm: arm}
}

func LongLeaseSpec(arm FairArm, flood, quietEvery time.Duration) FairSpec {
	return FairSpec{Scenario: "fair-noisy-long-leases", Kind: FairNoisy, Arm: arm, FloodService: flood, QuietEvery: quietEvery}
}

func SyncSpec(arm FairArm, quiet int) FairSpec {
	return FairSpec{Scenario: "fair-synchronized-quiet", Kind: FairSync, Arm: arm, SyncQuiet: quiet}
}

func (s FairSpec) Fingerprint() string {
	f := fmt.Sprintf("%s-%s-r%d-f%v-p%d-x%d", s.Scenario, s.Arm.Name, s.Arm.Rule, s.Arm.Flood, s.Arm.Plant, s.Arm.ProxyPlant)
	if s.FloodService != 0 || s.QuietEvery != 0 || s.SyncQuiet != 0 {
		f += fmt.Sprintf("-s%d-e%d-q%d", s.FloodService, s.QuietEvery, s.SyncQuiet)
	}
	return f
}

func (s FairSpec) quietCount() int {
	switch s.Kind {
	case FairMany:
		return FairManyQuiet
	case FairSync:
		return s.SyncQuiet
	}
	return 1
}

func (s FairSpec) tenants() int { return 1 + s.quietCount() }

func (s FairSpec) conns() int {
	if s.Kind == FairHeld {
		return FairHeldConns
	}
	return FairConns
}

func (s FairSpec) engine() proxy.Config {
	c := proxy.Config{Connections: s.conns(), QueueCap: FairTotalCap, MaxClients: 1024, MaxStartup: 512, Plant: s.Arm.ProxyPlant,
		Fairness: admission.FairConfig{Plant: s.Arm.Plant}}
	if s.Arm.Rule != 0 {
		c.Admission = admission.Config{Rule: s.Arm.Rule, TenantCapacity: FairTenantCap, Detail: true}
	}
	if s.Kind == FairHeld {
		c.Admission.Target, c.Admission.Interval = admission.Second, 5*admission.Second
	}
	return c
}

func FloodTenant() string { return proxysim.TenantName(0) }

func QuietTenant(i int) string { return proxysim.TenantName(1 + i) }

func floodInterval(clients int, loadPct int64) time.Duration {
	return time.Duration(int64(clients) * int64(time.Second) * 100 / (loadPct * FairFloodCapPerS))
}

func readMix() []workload.Weight { return []workload.Weight{{Op: workload.OpRead, Weight: 1}} }

func writeMix() []workload.Weight { return []workload.Weight{{Op: workload.OpWrite, Weight: 1}} }

func (s FairSpec) floodTenants() []workload.Tenant {
	iv := floodInterval(FairFloodClients, FairFloodLoadPct)
	riv := floodInterval(FairRecoveryClients, FairRecoveryLoad)
	base := workload.Tenant{Name: FloodTenant(), Database: proxysim.Database, Clients: FairFloodClients, Arrival: workload.ArrivalConstant,
		Spread: true, ConnectFirst: true, Keys: workload.Keys{N: 64}, Mix: readMix(), Backlog: workload.MaxBacklog}
	flood, rec := base, base
	flood.Interval, flood.Requests = iv, int(FairFlood/iv)
	rec.Clients, rec.Interval, rec.Requests, rec.Start = FairRecoveryClients, riv, int(FairRecovery/riv), FairFlood+FairRecoverAfter
	return []workload.Tenant{flood, rec}
}

func (s FairSpec) quietDuration() time.Duration { return FairFlood + FairRecoverAfter + FairRecovery }

func (s FairSpec) quietTenants() []workload.Tenant {
	if s.Kind == FairNoisy {
		every := FairQuietEvery
		if s.QuietEvery != 0 {
			every = s.QuietEvery
		}
		return []workload.Tenant{{Name: QuietTenant(0), Database: proxysim.Database, Clients: 1, Arrival: workload.ArrivalConstant,
			Interval: every, Requests: int(s.quietDuration() / every), ConnectFirst: true, Keys: workload.Keys{N: 64}, Mix: writeMix()}}
	}
	var out []workload.Tenant
	for i := range s.quietCount() {
		q := workload.Tenant{Name: QuietTenant(i), Database: proxysim.Database, Clients: 1, Arrival: workload.ArrivalConstant,
			Interval: FairManyEvery, Requests: int(s.quietDuration() / FairManyEvery), Start: time.Duration(i) * FairManyStagger,
			ConnectFirst: true, Keys: workload.Keys{N: 64}, Mix: writeMix()}
		if s.Kind == FairSync {
			q.Start, q.Requests = FairSyncAt, 1
		}
		out = append(out, q)
	}
	return out
}

func (s FairSpec) heldTenants() []workload.Tenant {
	quiet := workload.Tenant{Name: QuietTenant(0), Database: proxysim.Database, Clients: FairQuietClients, Arrival: workload.ArrivalClosed,
		Requests: FairQuietRequests, ConnectFirst: true, Keys: workload.Keys{N: 64}, Mix: readMix()}
	held := workload.Tenant{Name: FloodTenant(), Database: proxysim.Database, Clients: FairHeldClients, Arrival: workload.ArrivalClosed,
		Requests: FairHeldTx, Start: FairHeldStart, ConnectFirst: true, Keys: workload.Keys{N: 64}, TxStatements: 2, TxReads: 2,
		TxThink: FairHeldThink, Mix: []workload.Weight{{Op: workload.OpTx, Weight: 1}}}
	return []workload.Tenant{quiet, held}
}

func (s FairSpec) workload(addr string) workload.Config {
	cfg := workload.Config{Addr: addr, Options: proxysim.NetOptions()}
	switch {
	case s.Kind == FairHeld:
		cfg.Tenants = s.heldTenants()
	case s.Arm.Flood:
		cfg.Tenants = append(s.floodTenants(), s.quietTenants()...)
	default:
		cfg.Tenants = s.quietTenants()
	}
	return cfg
}

func (s FairSpec) duration() time.Duration {
	if s.Kind == FairHeld {
		return 3 * time.Second
	}
	return s.quietDuration()
}

func fairRows() []fakepg.Row {
	rows := make([]fakepg.Row, 64)
	for i := range rows {
		rows[i] = fakepg.Row{Key: workload.DefaultKeyPrefix + itoa(i), Value: "v" + itoa(i)}
	}
	return rows
}

func (s FairSpec) service() fakepg.Service {
	flood := FairFloodService
	if s.FloodService != 0 {
		flood = s.FloodService
	}
	return fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: flood}, {Kind: fakepg.KindUpdate, Time: FairQuietService}}}
}

func (r *FairRun) watchOverlap() error {
	e := r.World.Proxy.Engine
	flood := work.TenantID(FloodTenant())
	r.Overlapped = make([]bool, r.Spec.quietCount())
	return r.World.S.RegisterInvariant("fair-quiet-overlap", func() error {
		if time.Duration(r.World.S.Now()) >= FairFlood || e.TenantLeases(flood)+e.TenantQueued(flood) == 0 {
			return nil
		}
		for i := range r.Overlapped {
			q := work.TenantID(QuietTenant(i))
			if !r.Overlapped[i] && e.TenantLeases(q)+e.TenantQueued(q) > 0 {
				r.Overlapped[i] = true
			}
		}
		return nil
	})
}

func (r *FairRun) watchHeld() error {
	e := r.World.Proxy.Engine
	held, quiet := work.TenantID(FloodTenant()), work.TenantID(QuietTenant(0))
	active := false
	return r.World.S.RegisterInvariant("fair-held-share", func() error {
		q := e.TenantLeases(quiet) + e.TenantQueued(quiet)
		switch {
		case q > 0 && time.Duration(r.World.S.Now()) < FairHeldStart:
			active = true
		case q == 0 && active:
			active = false
			r.Watched = true
			r.HeldOpen = e.TenantLeases(held) + e.TenantQueued(held)
		}
		if active {
			r.HeldPeak = max(r.HeldPeak, e.TenantLeases(held))
		}
		return nil
	})
}

func RunFair(spec FairSpec, seed int64, dir, replay string) (*FairRun, error) {
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed, Scenario: spec.Scenario, Replay: replay, Tenants: spec.tenants(),
		Fake: fakepg.Config{Rows: fairRows(), Service: spec.service(), MaxConnections: 64}, Engine: spec.engine()})
	if err != nil {
		return nil, err
	}
	r := &FairRun{Spec: spec, Seed: seed, World: w}
	watch := r.watchOverlap
	if spec.Kind == FairHeld {
		watch = r.watchHeld
	}
	if err := watch(); err != nil {
		return nil, err
	}
	var wl *workload.Workload
	run := func() error {
		wl, err = workload.New(w.Net, spec.workload(w.Proxy.Addr()))
		if err != nil {
			return err
		}
		wl.Start()
		mid := min(FairFlood+FairRecoverAfter, spec.duration())
		if err := w.S.RunFor(mid); err != nil {
			return err
		}
		r.Mid = wl.Snapshot()
		if err := w.S.RunFor(spec.duration() - mid); err != nil {
			return err
		}
		r.Phase = wl.Snapshot()
		if err := w.S.RunFor(DefaultSettle); err != nil {
			return err
		}
		if err := w.Proxy.Finish(); err != nil {
			return err
		}
		r.Final = wl.Snapshot()
		r.Adm = w.Proxy.Engine.AdmissionSnapshot()
		r.Engine = w.Proxy.Engine.Snapshot()
		return nil
	}
	if dir == "" {
		err = run()
	} else {
		srcs := append(enginesrc.Sources(w.Proxy.Engine, nil), simsrc.Kernel(w.S), simsrc.Net(w.Net), simsrc.Fake(w.Fake),
			obs.NewSource(obs.ComponentWorkload, simsrc.WorkloadMetrics(), func() workload.Snapshot {
				if wl == nil {
					return workload.Snapshot{}
				}
				return wl.Snapshot()
			}, simsrc.MapWorkload))
		r.Flush, err = simrun.Custom(spec.Scenario, seed, spec.Fingerprint(), simrun.Options{Dir: dir, Arm: spec.Arm.Name, Replay: replay, Interval: FairFlush},
			w.S, srcs, run)
	}
	if err != nil {
		return r, fmt.Errorf("%s/%s seed %d: %w", spec.Scenario, spec.Arm.Name, seed, err)
	}
	if err := w.S.Err(); err != nil {
		return r, err
	}
	if err := r.Engine.Check(); err != nil {
		return r, err
	}
	return r, r.Adm.Check()
}

func TenantStats(s workload.Snapshot, name string) workload.TenantSnapshot {
	var out workload.TenantSnapshot
	for _, t := range s.Tenants {
		if t.Name != name {
			continue
		}
		out.Name = name
		out.Intended += t.Intended
		out.Sent += t.Sent
		out.OK += t.OK
		out.Errors += t.Errors
		out.Lost += t.Lost
		out.TimedOut += t.TimedOut
		out.Dropped += t.Dropped
		out.Latency = t.Latency
		out.ErrorsByCode = append(out.ErrorsByCode, t.ErrorsByCode...)
	}
	return out
}

func FairScenarios() []string {
	return []string{NoisySpec(FairArmFair).Scenario, ManySpec(FairArmFair).Scenario, HeldSpec(FairArmFair).Scenario, FairResultsSpec().Name}
}

func fairBaselineSpecs() []FairSpec {
	return []FairSpec{NoisySpec(FairArmFair), NoisySpec(FairArmAlone), NoisySpec(FairArmFIFO), ManySpec(FairArmFair), ManySpec(FairArmAlone),
		HeldSpec(FairArmFair)}
}

func FairBaseline(dir string, run bool) ([]obs.InstanceSnapshot, error) {
	var out []obs.InstanceSnapshot
	if !run {
		return out, nil
	}
	for _, sp := range fairBaselineSpecs() {
		r, err := RunFair(sp, 1, dir, "")
		if err == nil {
			err = r.Flush.Check()
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r.Flush.Final)
	}
	r, err := RunSim(FairResultsSpec(), 1, SimOptions{Dir: dir})
	if err == nil {
		err = CheckFairResults(r)
	}
	if err == nil {
		err = r.Flush.Check()
	}
	if err != nil {
		return nil, err
	}
	return append(out, r.Final), nil
}

func FairResultsSpec() Spec {
	sp := CoreRerun(ResultsSpec("", false), "fair-results")
	sp.Admission = admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOAdaptive, Target: admission.Second, Interval: 5 * admission.Second, Detail: true}
	return sp
}

func CheckFairResults(r *SimRun) error {
	if err := checkResults(r); err != nil {
		return err
	}
	a := r.World.Proxy.Engine.AdmissionSnapshot()
	tenants := 0
	for _, row := range a.PerTenant {
		if row.Served > 0 {
			tenants++
		}
	}
	if !a.Fair.On || a.Fair.Rounds == 0 || a.Fair.LeaseStarts != r.Engine.LeasesStarted || a.Fair.InFlight.Value != 0 || tenants != r.Spec.Tenants {
		return failf("the fair dispatcher did not carry the results run: %+v, %d tenants served, engine leases %d", a.Fair, tenants, r.Engine.LeasesStarted)
	}
	return nil
}
