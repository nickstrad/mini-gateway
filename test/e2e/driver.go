package e2e

import (
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

// ProxyDriver implements chunk 2's world.Driver interface with a proxysim.Harness
// placed between the simulated workload and fakepg.
type ProxyDriver struct {
	world.SimDriver
	Conns int
	Proxy *proxysim.Harness
}

// Start creates sim, simnet, fakepg, and the simulated proxy for scenario setup s.
func (d *ProxyDriver) Start(s world.Setup) error {
	d.S = sim.New(sim.Config{Seed: d.Seed, Scenario: d.Scenario, Replay: d.Replay})
	d.Net = simnet.New(d.S)
	fc := s.Fake
	fc.Users = append(fc.Users, proxysim.BackendUser)
	fc.Databases = append(fc.Databases, proxysim.Database)
	srv, err := fakepg.New(d.Net, fc)
	if err != nil {
		return err
	}
	d.Fake = srv
	var list []pgwire.Tenant
	for _, t := range s.Workload.Tenants {
		v, err := pgwire.NewVerifier("unused-"+t.Name, []byte("salt-verifier-16"), proxysim.Iterations)
		if err != nil {
			return err
		}
		list = append(list, pgwire.Tenant{User: t.Name, Verifier: v})
	}
	tt, err := pgwire.NewTenantTable(list)
	if err != nil {
		return err
	}
	ec := proxy.Config{Connections: d.Conns, MaxClients: workload.MaxClients, MaxStartup: workload.MaxClients, QueueCap: workload.MaxClients}
	ec.Client.Tenants, ec.Client.Database, ec.Client.Auth = tt, proxysim.Database, pgwire.AuthTrustForTestsOnly
	ec.Backend.User, ec.Backend.Database, ec.Backend.Password = proxysim.BackendUser, proxysim.Database, proxysim.BackendPass
	d.Proxy, err = proxysim.New(d.Net, proxysim.Config{DBAddr: srv.Addr(), Options: s.Workload.Options, Engine: ec})
	return err
}

// Addr returns the proxy's simnet listener address.
func (d *ProxyDriver) Addr() string {
	if d.Proxy == nil {
		return ""
	}
	return d.Proxy.Addr()
}

// Connect starts the simulated workload aimed at the proxy rather than fakepg.
func (d *ProxyDriver) Connect(c workload.Config) error {
	if d.Proxy == nil {
		return world.ErrNotStarted
	}
	c.Addr = d.Proxy.Addr()
	c.Tenants = append([]workload.Tenant(nil), c.Tenants...)
	for i := range c.Tenants {
		c.Tenants[i].Database = proxysim.Database
	}
	wl, err := workload.New(d.Net, c)
	if err != nil {
		return err
	}
	d.Workload = wl
	wl.Start()
	return nil
}

// Stop runs Proxy.Finish to verify quiescence and invariants after the scenario.
func (d *ProxyDriver) Stop() error {
	if d.Proxy == nil {
		return world.ErrNotStarted
	}
	if err := d.Proxy.Finish(); err != nil {
		return fmt.Errorf("quiesce: %w", err)
	}
	return nil
}

// Result assembles the world.Result snapshots for scenario assertions.
func (d *ProxyDriver) Result(name string, err error) *world.Result {
	r := &world.Result{Scenario: name, Seed: d.Seed, Err: err, Obs: d.Observe(), Sim: d.S, Server: d.Fake, Workload: d.Workload}
	if d.Fake != nil {
		r.Fake = d.Fake.Snapshot()
	}
	if d.Net != nil {
		r.Net = d.Net.Snapshot()
	}
	return r
}
