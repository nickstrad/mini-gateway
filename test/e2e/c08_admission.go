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
)

const (
	AdmConns        = 1
	AdmService      = 5 * time.Millisecond
	AdmLeaseTime    = 5500 * time.Microsecond
	AdmCapacityPerS = int64(AdmConns) * int64(time.Second/AdmLeaseTime)
	AdmClients      = 100
	AdmTenantCap    = 32
	AdmTotalCap     = 128
	AdmPhase        = 2 * time.Second
	AdmFlush        = time.Second
	AdmRecoveryLoad = 30
	AdmRecoveryFor  = 3 * time.Second
	AdmRecoverAfter = time.Duration(admission.DefaultInterval) + time.Duration(admission.DefaultLIFOHold) + time.Duration(admission.DefaultInterval)
)

type AdmArm struct {
	Name string
	Rule admission.RuleKind
	LIFO admission.LIFOMode
}

var (
	ArmTimeout    = AdmArm{Name: "timeout", Rule: admission.RuleTimeout}
	ArmControlLaw = AdmArm{Name: "control-law", Rule: admission.RuleControlLaw}
	ArmFIFO       = AdmArm{Name: "fifo", Rule: admission.RuleFIFO, LIFO: admission.LIFOOff}
	ArmFolly      = AdmArm{Name: "folly", Rule: admission.RuleFolly}
)

func AdmArms() []AdmArm { return []AdmArm{ArmTimeout, ArmControlLaw, ArmFIFO, ArmFolly} }

type AdmSpec struct {
	Scenario  string
	Arm       AdmArm
	LoadPct   int64
	Phases    int
	Clients   int
	TenantCap int
	TotalCap  int
	Recovery  bool
	Plant     admission.Plant
}

type AdmSample struct {
	At      time.Duration
	Adm     admission.Snapshot
	Engine  proxy.Snapshot
	Work    workload.Snapshot
	Backlog int64
}

type AdmRun struct {
	Spec    AdmSpec
	Seed    int64
	World   *proxysim.World
	Samples []AdmSample
	Final   AdmSample
	Flush   *simrun.Run
}

func (s AdmSpec) interval() time.Duration {
	return time.Duration(int64(s.Clients) * int64(time.Second) * 100 / (s.LoadPct * AdmCapacityPerS))
}

func (s AdmSpec) duration() time.Duration { return time.Duration(s.Phases) * AdmPhase }

func (s AdmSpec) engine() proxy.Config {
	return proxy.Config{Connections: AdmConns, QueueCap: s.TotalCap, MaxClients: 2 * s.Clients, MaxStartup: 2 * s.Clients,
		Admission: admission.Config{Rule: s.Arm.Rule, LIFO: s.Arm.LIFO, TenantCapacity: s.TenantCap, Plant: s.Plant, Detail: true}}
}

func (s AdmSpec) workload(addr string) workload.Config {
	iv := s.interval()
	t := workload.Tenant{Name: proxysim.TenantName(0), Database: proxysim.Database, Clients: s.Clients, Arrival: workload.ArrivalConstant,
		Interval: iv, Requests: int(s.duration() / iv), Spread: true, ConnectFirst: true, Keys: workload.Keys{N: 64},
		Mix: []workload.Weight{{Op: workload.OpRead, Weight: 1}}, Backlog: workload.MaxBacklog}
	cfg := workload.Config{Addr: addr, Options: proxysim.NetOptions(), Tenants: []workload.Tenant{t}}
	if s.Recovery {
		r := s
		r.LoadPct = AdmRecoveryLoad
		riv := r.interval()
		cfg.Tenants = append(cfg.Tenants, workload.Tenant{Name: proxysim.TenantName(0), Database: proxysim.Database, Clients: s.Clients,
			Arrival: workload.ArrivalConstant, Interval: riv, Requests: int(AdmRecoveryFor / riv), Start: s.duration() + AdmRecoverAfter,
			Spread: true, ConnectFirst: true, Keys: workload.Keys{N: 64}, Mix: []workload.Weight{{Op: workload.OpRead, Weight: 1}}})
	}
	return cfg
}

func (s AdmSpec) Fingerprint() string {
	return fmt.Sprintf("%s-%s-l%d-p%d-c%d-t%d-q%d-r%v-x%d", s.Scenario, s.Arm.Name, s.LoadPct, s.Phases, s.Clients, s.TenantCap, s.TotalCap, s.Recovery, s.Plant)
}

func admRows() []fakepg.Row {
	rows := make([]fakepg.Row, 64)
	for i := range rows {
		rows[i] = fakepg.Row{Key: workload.DefaultKeyPrefix + itoa(i), Value: "v" + itoa(i)}
	}
	return rows
}

func (r *AdmRun) sample() AdmSample {
	w := r.World
	s := AdmSample{At: time.Duration(w.S.Now()), Adm: w.Proxy.Engine.AdmissionSnapshot(), Engine: w.Proxy.Engine.Snapshot()}
	return s
}

func RunAdmission(spec AdmSpec, seed int64, dir, replay string) (*AdmRun, error) {
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed, Scenario: spec.Scenario, Replay: replay, Tenants: 1,
		Fake:   fakepg.Config{Rows: admRows(), Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: AdmService}}}, MaxConnections: 64},
		Engine: spec.engine()})
	if err != nil {
		return nil, err
	}
	r := &AdmRun{Spec: spec, Seed: seed, World: w}
	var wl *workload.Workload
	record := func() {
		s := r.sample()
		s.Work = wl.Snapshot()
		s.Backlog = s.Work.Total.NotYetSent.Value
		r.Samples = append(r.Samples, s)
	}
	run := func() error {
		wl, err = workload.New(w.Net, spec.workload(w.Proxy.Addr()))
		if err != nil {
			return err
		}
		wl.Start()
		for range spec.Phases {
			if err := w.S.RunFor(AdmPhase); err != nil {
				return err
			}
			record()
		}
		if spec.Recovery {
			if err := w.S.RunFor(AdmRecoverAfter); err != nil {
				return err
			}
			record()
			if err := w.S.RunFor(AdmRecoveryFor); err != nil {
				return err
			}
			record()
		}
		if err := w.S.RunFor(DefaultSettle); err != nil {
			return err
		}
		if err := w.Proxy.Finish(); err != nil {
			return err
		}
		r.Final = r.sample()
		r.Final.Work = wl.Snapshot()
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
		r.Flush, err = simrun.Custom(spec.Scenario, seed, spec.Fingerprint(), simrun.Options{Dir: dir, Arm: spec.Arm.Name, Replay: replay, Interval: AdmFlush},
			w.S, srcs, run)
	}
	if err != nil {
		return r, fmt.Errorf("%s/%s seed %d: %w", spec.Scenario, spec.Arm.Name, seed, err)
	}
	if err := w.S.Err(); err != nil {
		return r, err
	}
	if err := r.Final.Engine.Check(); err != nil {
		return r, err
	}
	return r, r.Final.Adm.Check()
}

func HeadToHeadSpec(arm AdmArm, loadPct int64) AdmSpec {
	return AdmSpec{Scenario: fmt.Sprintf("admission-h2h-l%d", loadPct), Arm: arm, LoadPct: loadPct, Phases: 1, Clients: AdmClients,
		TenantCap: AdmTenantCap, TotalCap: AdmTotalCap}
}

func HeadToHeadLoads() []int64 { return []int64{120, 200, 1000} }

func AntiBloatSpec(arm AdmArm) AdmSpec {
	s := AdmSpec{Scenario: "admission-anti-bloat", Arm: arm, LoadPct: 1000, Phases: 4, Clients: AdmClients,
		TenantCap: AdmTotalCap, TotalCap: AdmTotalCap, Recovery: arm.Rule != admission.RuleFIFO}
	if arm.Rule == admission.RuleControlLaw {
		s.TenantCap = AdmTenantCap
	}
	return s
}

func AntiBloatArms() []AdmArm { return []AdmArm{ArmTimeout, ArmControlLaw, ArmFIFO} }

func admFingerprint(a admission.Config) string {
	if a.Rule == 0 {
		return ""
	}
	return fmt.Sprintf("-%s-x%d", a.Rule, a.Plant)
}

const (
	BurstClients = 6
	BurstService = 9 * time.Millisecond
	BurstAt      = 2 * time.Second
	BurstHold    = 60 * time.Millisecond
)

func FirstBurstSpec(rule admission.RuleKind, plant admission.Plant) Spec {
	name := "admission-first-burst"
	if plant != 0 {
		name += "-planted"
	}
	sp := Spec{Name: name, Tenants: 1, Conns: 1, QueueCap: 32, Rows: clientRows(BurstClients + 1), Duration: 4 * time.Second,
		Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: BurstService}},
			PerKey: []fakepg.KeyTime{{Key: key(BurstClients, "a"), Time: BurstHold}}},
		Admission: admission.Config{Rule: rule, LIFO: admission.LIFOOff, Plant: plant, Detail: true}}
	sp.Clients = append(sp.Clients, Script{Tenant: 0, Start: BurstAt - 50*time.Millisecond, Steps: steps(read(BurstClients, "a"))})
	for c := range BurstClients {
		sp.Clients = append(sp.Clients, Script{Tenant: 0, Start: BurstAt, Steps: steps(read(c, "a"))})
	}
	return sp
}

func CheckFirstBurst(r *SimRun) error {
	shed := 0
	for c := range r.Clients {
		if err := allSteps(r, c); err != nil {
			return err
		}
		if HasToken(step(r, c, 0), "E:ERROR:53M01") {
			shed++
		}
	}
	a := r.World.Proxy.Engine.AdmissionSnapshot()
	if shed != 0 || a.ShedTotal() != 0 || a.Served != BurstClients+1 {
		return failf("first burst after idle: %d shed (admission %v), %d served", shed, a.Shed, a.Served)
	}
	if a.QueueWait.Max <= int64(admission.DefaultTarget) {
		return failf("the burst never waited past the short target (max %d): the check proves nothing", a.QueueWait.Max)
	}
	return nil
}
