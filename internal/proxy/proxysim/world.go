package proxysim

import (
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// Default database, credentials, and SCRAM parameters for simulated proxy worlds.
const (
	Database       = "app"
	BackendUser    = "gateway"
	BackendPass    = "gateway-pw"
	Iterations     = 16
	DefaultTenants = 4
)

// TenantName returns the deterministic tenant user name ("tenant0", "tenant1", ...).
func TenantName(i int) string { return "tenant" + strconv.Itoa(i) }

// TenantPassword returns the deterministic test password for tenant name.
func TenantPassword(name string) string { return "pw-" + name }

// TenantTable constructs a pgwire.TenantTable with n deterministic test tenants.
func TenantTable(n int) (*pgwire.TenantTable, error) {
	var list []pgwire.Tenant
	for i := 0; i < n; i++ {
		name := TenantName(i)
		v, err := pgwire.NewVerifier(TenantPassword(name), []byte(("salt-" + name + "-pad-0123456789")[:16]), Iterations)
		if err != nil {
			return nil, err
		}
		list = append(list, pgwire.Tenant{User: name, Verifier: v})
	}
	return pgwire.NewTenantTable(list)
}

// WorldConfig configures BuildWorld, assembling sim.Sim, simnet, fakepg, and Harness.
type WorldConfig struct {
	Seed        int64
	Scenario    string
	Replay      string
	Tenants     int
	SCRAM       bool
	Fake        fakepg.Config
	Engine      proxy.Config
	Options     simnet.Options
	DBOptions   simnet.Options
	TLSVersion  pgwire.TLSVersion
	DBAddr      string
	BeforeProxy func(*simnet.Net, *fakepg.Server) error
	Tap         func(sim.Record)
}

// World groups a simulator instance, network, fake Postgres server, and proxy Harness.
type World struct {
	S     *sim.Sim
	Net   *simnet.Net
	Fake  *fakepg.Server
	Proxy *Harness
	Cfg   WorldConfig
}

// NetOptions returns the default low-latency simnet options used by simulated proxy tests.
func NetOptions() simnet.Options {
	o := simnet.DefaultOptions()
	o.Up.Latency = simnet.Latency{Base: 100 * time.Microsecond, Jitter: 50 * time.Microsecond}
	o.Down.Latency = simnet.Latency{Base: 100 * time.Microsecond, Jitter: 50 * time.Microsecond}
	return o
}

// BuildWorld creates a deterministic sim.Sim, simnet.Net, fakepg.Server, and
// proxysim.Harness wired together with shared tenants and backend credentials.
func BuildWorld(cfg WorldConfig) (*World, error) {
	if cfg.Tenants <= 0 {
		cfg.Tenants = DefaultTenants
	}
	if cfg.Options.Window == 0 {
		cfg.Options = NetOptions()
	}
	if cfg.DBOptions.Window == 0 {
		cfg.DBOptions = cfg.Options
	}
	s := sim.New(sim.Config{Seed: cfg.Seed, Scenario: cfg.Scenario, Replay: cfg.Replay})
	if cfg.Tap != nil {
		s.SetTraceTap(cfg.Tap)
	}
	net := simnet.New(s)
	fc := cfg.Fake
	fc.Users = append(fc.Users, BackendUser)
	for i := 0; i < cfg.Tenants; i++ {
		fc.Users = append(fc.Users, TenantName(i))
	}
	fc.Databases = append(fc.Databases, Database)
	if cfg.SCRAM {
		v, err := pgwire.NewVerifier(BackendPass, []byte("salt-backend-0123"), Iterations)
		if err != nil {
			return nil, err
		}
		fc.Auth = fakepg.AuthSCRAM
		fc.Verifiers = append(fc.Verifiers, fakepg.UserVerifier{User: BackendUser, Verifier: v})
	}
	if fc.Options.Window == 0 {
		fc.Options = cfg.Options
	}
	srv, err := fakepg.New(net, fc)
	if err != nil {
		return nil, err
	}
	tt, err := TenantTable(cfg.Tenants)
	if err != nil {
		return nil, err
	}
	ec := cfg.Engine
	ec.Client.Tenants = tt
	ec.Client.Database = Database
	if !cfg.SCRAM {
		ec.Client.Auth = pgwire.AuthTrustForTestsOnly
	}
	ec.Backend.User = BackendUser
	ec.Backend.Database = Database
	ec.Backend.Password = BackendPass
	if cfg.BeforeProxy != nil {
		if err := cfg.BeforeProxy(net, srv); err != nil {
			return nil, err
		}
	}
	dbAddr := srv.Addr()
	if cfg.DBAddr != "" {
		dbAddr = cfg.DBAddr
	}
	h, err := New(net, Config{DBAddr: dbAddr, Options: cfg.DBOptions, Engine: ec, TLSVersion: cfg.TLSVersion})
	if err != nil {
		return nil, err
	}
	return &World{S: s, Net: net, Fake: srv, Proxy: h, Cfg: cfg}, nil
}
