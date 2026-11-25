package simrun

import (
	"errors"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/logline"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/obs/wiresrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/wireworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

func (d *driver) now() int64 {
	if d.S == nil {
		return 0
	}
	return int64(d.S.Now())
}

func newRun(scenario string, seed int64, fingerprint string, opt Options, clock func() int64) (*Run, error) {
	run := &Run{RunID: RunID(scenario, seed, opt.Arm)}
	run.Collector = obs.NewCollector(obs.Config{Instance: Instance, Build: Build, ConfigFingerprint: fingerprint, Detail: !opt.DetailOff})
	cfg := csvflush.Config{Dir: opt.Dir, Strict: true, Clock: clock, Run: csvflush.RunInfo{RunID: run.RunID, Scenario: scenario,
		Tier: csvflush.TierSim, Seed: seed, Arm: opt.Arm, Instances: 1, Build: Build, ConfigFingerprint: fingerprint}}
	if opt.CSV != nil {
		opt.CSV(&cfg)
	}
	w, err := csvflush.New(cfg)
	if err != nil {
		return nil, err
	}
	run.CSV, run.RunDir = w, w.RunDir()
	run.JSON = jsonstats.New(nil)
	run.Prom = promtext.New(run.Collector.Registry(), nil, opt.Prom)
	lines := logline.New(run.Collector.Registry(), &run.Log)
	for _, a := range []obs.Adapter{w, run.JSON, run.Prom, lines} {
		if err := run.Collector.Attach(a); err != nil {
			return nil, err
		}
	}
	return run, nil
}

// WireInterval is the default virtual publish cadence for wire scenarios
// (coarser than the 100 ms baseline default because wire runs span 30 s).
const WireInterval = 5 * time.Second

// WireFingerprint returns the deterministic config fingerprint for sc.
func WireFingerprint(sc wireworld.Scenario) string { return sc.Name + "-wire" }

// Wire builds and runs wire startup scenario sc on seed with sim, simnet,
// fakepg, workload, and wire sources attached to all four observability
// adapters, flushing at t=0, every WireInterval, at sc.CheckAt(), and at end.
func Wire(sc wireworld.Scenario, seed int64, opt Options) (*Run, error) {
	if opt.Interval <= 0 {
		opt.Interval = WireInterval
	}
	if opt.Arm == "" {
		opt.Arm = DefaultArm
	}
	if opt.Dir == "" {
		return nil, errors.New("simrun: no CSV directory")
	}
	opt.FlushAt = append(opt.FlushAt[:len(opt.FlushAt):len(opt.FlushAt)], sim.Time(sc.CheckAt()))
	w, err := wireworld.Build(seed, sc, opt.Replay)
	if err != nil {
		return nil, err
	}
	d := &driver{SimDriver: &world.SimDriver{Seed: seed, Scenario: sc.Name, S: w.Sim}, opt: opt}
	run, err := newRun(sc.Name, seed, WireFingerprint(sc), opt, d.now)
	if err != nil {
		return nil, err
	}
	d.run = run
	if err := simsrc.Register(run.Collector, simsrc.Kernel(w.Sim), simsrc.Net(w.Net), simsrc.Fake(w.PG), simsrc.Workload(w.Workload),
		wiresrc.Source(w.Proxy.Snapshot)); err != nil {
		return nil, errors.Join(err, run.CSV.Close())
	}
	d.publish()
	d.schedule(sim.Later(w.Sim.Now(), opt.Interval))
	for _, at := range opt.FlushAt {
		t := w.Sim.At(at, d.publish)
		t.MarkBackground()
	}
	run.Wire = w.Run()
	d.publish()
	run.Final, _ = run.Collector.Last()
	cerr := run.CSV.Close()
	return run, errors.Join(errors.Join(d.errs...), cerr)
}
