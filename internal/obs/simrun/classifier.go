package simrun

import (
	"errors"

	"github.com/nickstrad/mini-gateway/internal/obs/clssrc"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/obs/wiresrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/clsworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

func ClassifierFingerprint(sc clsworld.Scenario) string { return sc.Name + "-classifier" }

func Classifier(sc clsworld.Scenario, seed int64, opt Options) (*Run, error) {
	if opt.Interval <= 0 {
		opt.Interval = DefaultInterval
	}
	if opt.Arm == "" {
		opt.Arm = DefaultArm
	}
	if opt.Dir == "" {
		return nil, errors.New("simrun: no CSV directory")
	}
	w, err := clsworld.Build(seed, sc, opt.Replay)
	if err != nil {
		return nil, err
	}
	d := &driver{SimDriver: &world.SimDriver{Seed: seed, Scenario: sc.Name, S: w.Sim}, opt: opt}
	run, err := newRun(sc.Name, seed, ClassifierFingerprint(sc), opt, d.now)
	if err != nil {
		return nil, err
	}
	d.run = run
	gw := w.Gateway
	snapshot := func() clssrc.Snapshot {
		return clssrc.Snapshot{Classifier: gw.Classifier.Snapshot(), Replies: gw.Replies.Snapshot(), Sync: gw.SyncSnapshot()}
	}
	if err := simsrc.Register(run.Collector, simsrc.Kernel(w.Sim), simsrc.Net(w.Net), simsrc.Fake(w.PG),
		clssrc.Source(snapshot), wiresrc.Source(gw.Wire.Snapshot)); err != nil {
		return nil, errors.Join(err, run.CSV.Close())
	}
	d.publish()
	d.schedule(sim.Later(w.Sim.Now(), opt.Interval))
	for _, at := range opt.FlushAt {
		t := w.Sim.At(at, d.publish)
		t.MarkBackground()
	}
	run.Classifier = w.Run()
	d.publish()
	run.Final, _ = run.Collector.Last()
	cerr := run.CSV.Close()
	return run, errors.Join(errors.Join(d.errs...), cerr)
}
