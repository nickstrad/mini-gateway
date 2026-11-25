// Package world wires fakepg and workload into reusable scenarios, a protocol-level
// Driver interface shared with future tier R drivers, the five direct-to-database
// baseline scenarios, exact write-log checkers, and the Postgres 17 fidelity runner.
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

// Setup bundles the fake database config, client workload config, and scheduled
// faults that define one scenario run.
type Setup struct {
	Fake     fakepg.Config
	Workload workload.Config
	Faults   []sim.Fault
}

// Observation holds the protocol-level database and client metrics that both
// the simulator driver and a future real-Postgres tier R driver can report.
type Observation struct {
	DBConnsAccepted  int64
	DBConnsPeak      int64
	DBSessionsPeak   int64
	DBRefusedTooMany int64
	DBReads          int64
	DBStatements     int64
	DBWrites         int64
	Clients          workload.Snapshot
}

// Driver is the minimal lifecycle interface a scenario needs from its test
// world (simulator today; real proxy + Postgres in Chunk 6).
type Driver interface {
	Start(s Setup) error
	Addr() string
	Connect(c workload.Config) error
	Inject(f sim.Fault) error
	Run(d time.Duration) error
	Observe() Observation
	Stop() error
}

// ErrNotStarted reports a SimDriver method called before Start.
var ErrNotStarted = errors.New("world: driver not started")

// SimDriver implements Driver in-process on sim.Sim, simnet, fakepg, and workload.
type SimDriver struct {
	Seed     int64
	Scenario string
	Replay   string
	Tap      func(sim.Record)
	S        *sim.Sim
	Net      *simnet.Net
	Fake     *fakepg.Server
	Workload *workload.Workload
}

// Start creates the simulator kernel, simnet, and fake Postgres server for s.
func (d *SimDriver) Start(s Setup) error {
	d.S = sim.New(sim.Config{Seed: d.Seed, Scenario: d.Scenario, Replay: d.Replay})
	if d.Tap != nil {
		d.S.SetTraceTap(d.Tap)
	}
	d.Net = simnet.New(d.S)
	srv, err := fakepg.New(d.Net, s.Fake)
	if err != nil {
		return err
	}
	d.Fake = srv
	return nil
}

// Addr returns the address clients connect to, or "" before Start.
func (d *SimDriver) Addr() string {
	if d.Fake == nil {
		return ""
	}
	return d.Fake.Addr()
}

// Connect creates and starts the simulated workload targeting the fake database.
func (d *SimDriver) Connect(c workload.Config) error {
	if d.Fake == nil {
		return ErrNotStarted
	}
	c.Addr = d.Fake.Addr()
	wl, err := workload.New(d.Net, c)
	if err != nil {
		return err
	}
	d.Workload = wl
	wl.Start()
	return nil
}

// Inject schedules fault f on the simulator loop.
func (d *SimDriver) Inject(f sim.Fault) error {
	if d.S == nil {
		return ErrNotStarted
	}
	d.S.InjectAt(f)
	return nil
}

// Run advances virtual time by dur.
func (d *SimDriver) Run(dur time.Duration) error {
	if d.S == nil {
		return ErrNotStarted
	}
	return d.S.RunFor(dur)
}

// Observe snapshots the database and client counters into a driver-neutral Observation.
func (d *SimDriver) Observe() Observation {
	var o Observation
	if d.Fake != nil {
		fs := d.Fake.Snapshot()
		o.DBConnsAccepted = fs.ConnsAccepted
		o.DBConnsPeak = fs.ConnsOpen.Peak
		o.DBSessionsPeak = fs.Sessions.Peak
		o.DBRefusedTooMany = fs.Refused.TooMany
		o.DBReads = fs.StatementsByKind[fakepg.KindRead]
		o.DBStatements = fs.StatementsStarted
		o.DBWrites = fs.WritesApplied
	}
	if d.Workload != nil {
		o.Clients = d.Workload.Snapshot()
	}
	return o
}

// Stop drains remaining foreground events, checks quiesce, and verifies kernel state.
func (d *SimDriver) Stop() error {
	if d.S == nil {
		return ErrNotStarted
	}
	if err := d.S.RunUntilQuiet(); err != nil {
		return err
	}
	if err := d.S.CheckQuiesce(); err != nil {
		return fmt.Errorf("quiesce: %w", err)
	}
	return d.S.Verify()
}
