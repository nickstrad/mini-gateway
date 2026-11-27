package e2e

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

const (
	// FlushInterval is the virtual-time CSV publish period used by tier S runs.
	FlushInterval = 5 * time.Second
	logCap        = 1 << 16
	// DefaultSettle is the post-workload virtual-time settle horizon.
	DefaultSettle = 30 * time.Second
)

// SimOptions configures how RunSim and BuildSim execute a Spec in tier S.
type SimOptions struct {
	Direct bool
	Dir    string
	Arm    string
	Replay string
	Tamper Tamper
	Tap    bool
}

// SimRun holds the completed world, client replies, engine snapshot, and CSV flush
// record of one tier S scenario execution.
type SimRun struct {
	Spec    Spec
	Seed    int64
	World   *proxysim.World
	Clients []ClientResult
	Engine  proxy.Snapshot
	Flush   *simrun.Run
	Tamper  *tamperer
	Final   obs.InstanceSnapshot
	Path    *pathRelay
}

func fragmented(o simnet.Options) simnet.Options {
	o.Up.Chunking, o.Up.ChunkSize = simnet.ChunkFixed, 1
	o.Down.Chunking, o.Down.ChunkSize = simnet.ChunkFixed, 1
	return o
}

func (spec Spec) engineConfig() proxy.Config {
	c := proxy.Config{Connections: spec.Conns, QueueCap: spec.QueueCap, Timeouts: spec.Timeouts,
		ReplyBufferCap: spec.ReplyCap, Plant: spec.Plant, MaxClients: 1024, MaxStartup: 1024}
	if spec.Pool != nil {
		pc := *spec.Pool
		c.Pool = &pc
		c.Reset = spec.Reset
		c.Reseed = spec.Reseed
		c.MaxClients = proxy.MaxClients
		c.MaxStartup = proxy.MaxClients
	}
	return c
}

// BuildSim constructs a tier S world (with optional tamper relay and scheduled
// faults) without running the scripted clients yet.
func BuildSim(spec Spec, seed int64, o SimOptions) (*SimRun, error) {
	opts := proxysim.NetOptions()
	if spec.Fragment {
		opts = fragmented(opts)
	}
	maxConns := 256
	if spec.FakeConns > 0 {
		maxConns = spec.FakeConns
	}
	fake := fakepg.Config{Rows: spec.Rows, Service: spec.Service, MaxConnections: maxConns,
		StatementLog: fakepg.LogConfig{Cap: logCap}, WriteLog: fakepg.LogConfig{Cap: logCap}}
	cfg := proxysim.WorldConfig{Seed: seed, Scenario: spec.Name, Replay: o.Replay, Tenants: spec.Tenants, Fake: fake,
		Engine: spec.engineConfig(), Options: opts, DBOptions: opts}
	r := &SimRun{Spec: spec, Seed: seed}
	if spec.Path != nil && !o.Direct {
		r.Path = newPathRelay(*spec.Path)
		cfg.DBAddr = faultAddr
		cfg.BeforeProxy = r.Path.listen
	}
	if o.Tamper != TamperNone {
		r.Tamper = &tamperer{mode: o.Tamper}
		cfg.DBAddr = tamperAddr
		cfg.BeforeProxy = r.Tamper.listen
	}
	w, err := proxysim.BuildWorld(cfg)
	if err != nil {
		return nil, err
	}
	r.World = w
	for _, f := range spec.Faults {
		w.S.InjectAt(f)
	}
	return r, nil
}

// RunSim builds and executes spec in tier S with seed, optionally flushing CSV
// files under o.Dir, and verifies engine conservation and simulator invariants.
func RunSim(spec Spec, seed int64, o SimOptions) (*SimRun, error) {
	r, err := BuildSim(spec, seed, o)
	if err != nil {
		return nil, err
	}
	w := r.World
	addr := w.Proxy.Addr()
	if o.Direct {
		addr = w.Fake.Addr()
	}
	opts := w.Cfg.Options
	r.Clients = make([]ClientResult, len(spec.Clients))
	run := func() error {
		for i, sc := range spec.Clients {
			newSimClient(w.S, w.Net, addr, opts, i, sc, &r.Clients[i]).start()
		}
		if err := w.S.RunFor(spec.Duration); err != nil {
			return err
		}
		return w.Proxy.Finish()
	}
	if o.Dir == "" {
		err = run()
	} else {
		srcs := append(enginesrc.Sources(w.Proxy.Engine, nil), simsrc.Kernel(w.S), simsrc.Net(w.Net), simsrc.Fake(w.Fake), r.workloadSource())
		r.Flush, err = simrun.Custom(spec.Name, seed, fingerprint(spec), simrun.Options{Dir: o.Dir, Arm: o.Arm, Replay: o.Replay, Interval: FlushInterval},
			w.S, srcs, run)
		if r.Flush != nil {
			r.Final = r.Flush.Final
		}
	}
	r.Engine = w.Proxy.Engine.Snapshot()
	if err != nil {
		return r, fmt.Errorf("%s seed %d: %w", spec.Name, seed, err)
	}
	if err := w.S.Err(); err != nil {
		return r, err
	}
	return r, r.Engine.Check()
}

func fingerprint(spec Spec) string {
	if spec.Pool != nil {
		return fmt.Sprintf("%s-c%d-q%d-p%d-pool%d-%s-r%v", spec.Name, spec.Conns, spec.QueueCap, spec.Plant, spec.Pool.Plant, spec.Reset, spec.Reseed)
	}
	return fmt.Sprintf("%s-c%d-q%d-p%d", spec.Name, spec.Conns, spec.QueueCap, spec.Plant)
}

// ErrCheck is wrapped by scenario check failures.
var ErrCheck = errors.New("e2e: check failed")

func failf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCheck, fmt.Sprintf(format, args...))
}
