// Package simrun wraps world.SimDriver with an obs.Collector and all four
// adapters (csvflush, jsonstats, promtext, logline) so simulator scenarios
// publish on the virtual clock without internal/sim importing any adapter.
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
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/clsworld"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/wireworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

const (
	// DefaultInterval is the default virtual-clock publish period in simulated scenarios.
	DefaultInterval = 100 * time.Millisecond
	// DefaultArm is the arm name written into run_id when Options.Arm is empty.
	DefaultArm = "base"
	// Instance is the instance id stamped onto simulated snapshots.
	Instance = "sim-1"
	// Build is the build id stamped onto simulated snapshots and run.csv.
	Build = "sim"
	// MaxPublishes bounds periodic publishes per scenario run.
	MaxPublishes = 100_000
)

// Options configures a flushed scenario run. Dir is required.
type Options struct {
	Interval  time.Duration
	FlushAt   []sim.Time
	Dir       string
	Arm       string
	DetailOff bool
	CSV       func(*csvflush.Config)
	Prom      promtext.Config
	Replay    string
}

// Run captures the world result, collector, final snapshot, CSV directory, and
// outputs of all four attached adapters after Scenario completes.
type Run struct {
	Result     *world.Result
	Collector  *obs.Collector
	Final      obs.InstanceSnapshot
	RunDir     string
	RunID      string
	CSV        *csvflush.Writer
	JSON       *jsonstats.Adapter
	Prom       *promtext.Adapter
	Log        bytes.Buffer
	Publishes  int64
	PublishAt  []sim.Time
	Wire       wireworld.Result
	Classifier clsworld.Result
}

// RunID formats the schema v1 simulator run identifier "<scenario>/<seed>/<arm>".
func RunID(scenario string, seed int64, arm string) string {
	return scenario + "/" + strconv.FormatInt(seed, 10) + "/" + arm
}

// ConfigFingerprint builds a deterministic fingerprint for a scenario run.
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
	// Publish once at t=0 right after all sources are registered, then schedule
	// periodic and extra FlushAt publishes as background timers so they never
	// prevent quiesce or look like leaked timers.
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

// Scenario runs sc with seed while publishing to CSV, JSON, Prometheus, and
// logline adapters on the virtual clock, publishes a final snapshot after Stop,
// and closes the CSV writer to commit run.csv.
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
	d := &driver{SimDriver: &world.SimDriver{Seed: seed, Scenario: sc.Name}, opt: opt}
	run, err := newRun(sc.Name, seed, ConfigFingerprint(sc, seed), opt, d.now)
	if err != nil {
		return nil, err
	}
	d.run = run
	w := run.CSV
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

// Baseline looks up a named baseline scenario from world.
func Baseline(name string) (world.Scenario, bool) { return world.Lookup(name) }

// Check verifies every adapter's final output against Final: exact CSV round
// trip, valid Prometheus exposition grammar, exact JSON round trip, one log
// line per publish, and matching final publish sequence number.
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
