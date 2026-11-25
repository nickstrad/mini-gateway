package world

import (
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

// Option mutates a scenario Setup before execution (for example, to plant a canary bug).
type Option func(*Setup)

// Scenario defines one named, reproducible simulated-world test and its verifier.
type Scenario struct {
	Name     string
	Setup    func(seed int64) Setup
	Duration time.Duration
	Check    func(r *Result) error
}

// Result holds the final observation, snapshots, error, and simulator handles of a Scenario run.
type Result struct {
	Scenario string
	Seed     int64
	Obs      Observation
	Err      error
	Sim      *sim.Sim
	Fake     fakepg.Snapshot
	Net      simnet.Snapshot
	Server   *fakepg.Server
	Workload *workload.Workload
}

// Run executes sc on a fresh SimDriver with seed and optional Setup mutations.
func Run(sc Scenario, seed int64, opts ...Option) *Result {
	return RunWith(sc, seed, &SimDriver{Seed: seed, Scenario: sc.Name}, opts...)
}

// RunWith executes sc using caller-supplied SimDriver d (allowing custom Tap or Replay settings).
func RunWith(sc Scenario, seed int64, d *SimDriver, opts ...Option) *Result {
	setup := sc.Setup(seed)
	for _, o := range opts {
		o(&setup)
	}
	res := &Result{Scenario: sc.Name, Seed: seed}
	res.Err = Drive(d, setup, sc.Duration)
	res.Obs = d.Observe()
	res.Sim, res.Server, res.Workload = d.S, d.Fake, d.Workload
	if d.Fake != nil {
		res.Fake = d.Fake.Snapshot()
	}
	if d.Net != nil {
		res.Net = d.Net.Snapshot()
	}
	return res
}

// Drive executes the standard Start -> Inject -> Connect -> Run -> Stop sequence
// against any Driver implementation.
func Drive(d Driver, setup Setup, dur time.Duration) error {
	if err := d.Start(setup); err != nil {
		return err
	}
	for _, f := range setup.Faults {
		if err := d.Inject(f); err != nil {
			return err
		}
	}
	if err := d.Connect(setup.Workload); err != nil {
		return err
	}
	if err := d.Run(dur); err != nil {
		return err
	}
	return d.Stop()
}

// Verify checks driver/kernel health, workload conservation, non-zero activity
// ("counters must move"), and the scenario's own Check predicate.
func (sc Scenario) Verify(r *Result) error {
	if r.Err != nil {
		return r.Err
	}
	if r.Sim != nil {
		if err := r.Sim.Err(); err != nil {
			return err
		}
	}
	if err := r.Obs.Clients.Total.Check(); err != nil {
		return err
	}
	for _, t := range r.Obs.Clients.Tenants {
		if err := t.Check(); err != nil {
			return fmt.Errorf("tenant %s: %w", t.Name, err)
		}
	}
	if r.Obs.Clients.Total.Intended == 0 && r.Obs.Clients.Total.Connects == 0 {
		return errors.New("the workload did nothing")
	}
	if r.Obs.DBConnsAccepted == 0 {
		return errors.New("the database accepted no connection")
	}
	if sc.Check == nil {
		return nil
	}
	return sc.Check(r)
}

// Lookup finds a registered world scenario by name.
func Lookup(name string) (Scenario, bool) {
	for _, sc := range Scenarios() {
		if sc.Name == name {
			return sc, true
		}
	}
	return Scenario{}, false
}
