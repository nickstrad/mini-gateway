package simrun

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/logline"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

const (
	DefaultInterval = 100 * time.Millisecond
	DefaultArm      = "base"
	Instance        = "sim-1"
	Build           = "sim"
	MaxPublishes    = 100_000
)

type Options struct {
	Interval  time.Duration
	FlushAt   []sim.Time
	Dir       string
	Arm       string
	DetailOff bool
	CSV       func(*csvflush.Config)
	Prom      promtext.Config
}

type Run struct {
	Result    *world.Result
	Collector *obs.Collector
	Final     obs.InstanceSnapshot
	RunDir    string
	RunID     string
	CSV       *csvflush.Writer
	JSON      *jsonstats.Adapter
	Prom      *promtext.Adapter
	Log       bytes.Buffer
	Publishes int64
	PublishAt []sim.Time
}

func RunID(scenario string, seed int64, arm string) string {
	return scenario + "/" + strconv.FormatInt(seed, 10) + "/" + arm
}

func ConfigFingerprint(sc world.Scenario, seed int64) string {
	return fmt.Sprintf("%s-d%d", sc.Name, int64(sc.Duration))
}

type driver struct {
	*world.SimDriver
	run  *Run
	opt  Options
	errs []error
}

func (d *driver) Start(s world.Setup) error {
	if err := d.SimDriver.Start(s); err != nil {
		return err
	}
	return simsrc.Register(d.run.Collector, simsrc.Kernel(d.S), simsrc.Net(d.Net), simsrc.Fake(d.Fake))
}

func (d *driver) Connect(c workload.Config) error {
	if err := d.SimDriver.Connect(c); err != nil {
		return err
	}
	if err := d.run.Collector.Register(simsrc.Workload(d.Workload)); err != nil {
		return err
	}
	d.publish()
	d.schedule(sim.Later(d.S.Now(), d.opt.Interval))
	for _, at := range d.opt.FlushAt {
		t := d.S.At(at, d.publish)
		t.MarkBackground()
	}
	return nil
}

func (d *driver) schedule(at sim.Time) {
	t := d.S.At(at, func() {
		d.publish()
		if d.run.Publishes < MaxPublishes {
			d.schedule(sim.Later(d.S.Now(), d.opt.Interval))
		}
	})
	t.MarkBackground()
}

func (d *driver) publish() {
	if _, err := d.run.Collector.Publish(); err != nil {
		d.errs = append(d.errs, err)
	}
	d.run.Publishes++
	d.run.PublishAt = append(d.run.PublishAt, d.S.Now())
}

func Scenario(sc world.Scenario, seed int64, opt Options) (*Run, error) {
	if opt.Interval <= 0 {
		opt.Interval = DefaultInterval
	}
	if opt.Arm == "" {
		opt.Arm = DefaultArm
	}
	if opt.Dir == "" {
		return nil, errors.New("simrun: no CSV directory")
	}
	run := &Run{RunID: RunID(sc.Name, seed, opt.Arm)}
	run.Collector = obs.NewCollector(obs.Config{Instance: Instance, Build: Build, ConfigFingerprint: ConfigFingerprint(sc, seed), Detail: !opt.DetailOff})
	d := &driver{SimDriver: &world.SimDriver{Seed: seed, Scenario: sc.Name}, run: run, opt: opt}
	cfg := csvflush.Config{Dir: opt.Dir, Strict: true, Clock: func() int64 {
		if d.S == nil {
			return 0
		}
		return int64(d.S.Now())
	}, Run: csvflush.RunInfo{RunID: run.RunID, Scenario: sc.Name, Tier: csvflush.TierSim, Seed: seed, Arm: opt.Arm,
		Instances: 1, Build: Build, ConfigFingerprint: ConfigFingerprint(sc, seed)}}
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
	setup := sc.Setup(seed)
	res := &world.Result{Scenario: sc.Name, Seed: seed}
	res.Err = world.Drive(d, setup, sc.Duration)
	res.Obs = d.Observe()
	res.Sim, res.Server, res.Workload = d.S, d.Fake, d.Workload
	if d.Fake != nil {
		res.Fake = d.Fake.Snapshot()
	}
	if d.Net != nil {
		res.Net = d.Net.Snapshot()
	}
	run.Result = res
	if d.S != nil && d.Workload != nil {
		d.publish()
	}
	run.Final, _ = run.Collector.Last()
	cerr := w.Close()
	return run, errors.Join(errors.Join(d.errs...), cerr)
}

func Baseline(name string) (world.Scenario, bool) { return world.Lookup(name) }

func (r *Run) Check() error {
	var errs []error
	if err := csvflush.RoundTrip(r.RunDir, r.Final); err != nil {
		errs = append(errs, fmt.Errorf("csv round trip: %w", err))
	}
	if err := promtext.Check(r.Prom.Last()); err != nil {
		errs = append(errs, err)
	}
	if got, err := jsonstats.Decode(r.JSON.Last()); err != nil || !obs.Equal(got, r.Final) {
		errs = append(errs, fmt.Errorf("json stats differ from the final snapshot (%v)", err))
	}
	if n := int64(bytes.Count(r.Log.Bytes(), []byte("\n"))); n != r.Publishes {
		errs = append(errs, fmt.Errorf("%d log lines for %d publishes", n, r.Publishes))
	}
	if r.Final.Seq != r.Publishes-1 {
		errs = append(errs, fmt.Errorf("final seq %d after %d publishes", r.Final.Seq, r.Publishes))
	}
	return errors.Join(errs...)
}
