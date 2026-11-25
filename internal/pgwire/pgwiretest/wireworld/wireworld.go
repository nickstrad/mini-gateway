package wireworld

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

const (
	Iterations    = 16
	PoolUser      = "pool"
	PoolPassword  = "pool-pw"
	Database      = "app"
	FaultAt       = 20 * time.Millisecond
	RecoverAt     = 8 * time.Second
	CheckMargin   = 50 * time.Millisecond
	GoodClients   = 4
	GoodRequests  = 3
	RequestGap    = 5 * time.Millisecond
	ClientPrefix  = "tenant"
	ScenarioLimit = 30 * time.Second
	goodTenants   = 2
	tenantCount   = 3
)

type World struct {
	Sim      *sim.Sim
	Net      *simnet.Net
	PG       *fakepg.Server
	Proxy    *pgwiretest.Proxy
	Workload *workload.Workload
	Scenario Scenario
}

type Expect struct {
	Client   map[pgwire.Reason]int64
	Database map[pgwire.Class]int64
	By       time.Duration
	OKFault  bool
}

type Scenario struct {
	Name            string
	ClientTLS       pgwire.ClientTLSMode
	BackendTLS      pgwire.BackendTLSMode
	PGTLS           fakepg.TLSConfig
	OneByte         bool
	Fault           *workload.Tenant
	Inject          func(w *World)
	Clear           func(w *World)
	BackendPassword string
	Want            Expect
	Client          func(c *pgwire.ClientConfig)
}

func Verifier(password, salt string) (pgwire.Verifier, error) {
	return pgwire.NewVerifier(password, []byte(salt), Iterations)
}

func TenantName(i int) string { return fmt.Sprintf("%s%d", ClientPrefix, i) }

func TenantPassword(name string) string { return "pw-" + name }

func goodTenant(name string, start time.Duration, tls workload.TLSRequest) workload.Tenant {
	return workload.Tenant{Name: name, Database: Database, Clients: GoodClients, Requests: GoodRequests, Interval: RequestGap,
		Start: start, Statement: "SELECT 1", Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k"}, Password: TenantPassword(name), TLS: tls}
}

func oneByteOptions() simnet.Options {
	o := simnet.DefaultOptions()
	o.Up = simnet.Direction{Chunking: simnet.ChunkFixed, ChunkSize: 1}
	o.Down = simnet.Direction{Chunking: simnet.ChunkFixed, ChunkSize: 1}
	return o
}

func clientTLSFor(m pgwire.ClientTLSMode) workload.TLSRequest {
	if m == pgwire.ClientTLSRequire {
		return workload.TLSRequire
	}
	return workload.TLSPrefer
}

func tenantTable() (*pgwire.TenantTable, error) {
	var tenants []pgwire.Tenant
	for i := 0; i < tenantCount; i++ {
		name := TenantName(i)
		v, err := Verifier(TenantPassword(name), "salt-"+name+"-012345")
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, pgwire.Tenant{User: name, Verifier: v})
	}
	return pgwire.NewTenantTable(tenants)
}

func (sc Scenario) faultTenant() workload.Tenant {
	f := *sc.Fault
	f.Database, f.Statement, f.Keys, f.Start = Database, "SELECT 1", workload.Keys{Kind: workload.KeysFixed, Fixed: "k"}, FaultAt
	if f.Name == "" {
		f.Name = TenantName(goodTenants)
	}
	if f.Password == "" {
		f.Password = TenantPassword(f.Name)
	}
	if f.Clients == 0 {
		f.Clients = 1
	}
	if f.Requests == 0 {
		f.Requests = 1
	}
	return f
}

func Build(seed int64, sc Scenario, replay string) (*World, error) {
	s := sim.New(sim.Config{Seed: seed, Scenario: sc.Name})
	s.SetReplay(replay)
	n := simnet.New(s)
	table, err := tenantTable()
	if err != nil {
		return nil, err
	}
	poolVerifier, err := Verifier(PoolPassword, "pool-salt-012345")
	if err != nil {
		return nil, err
	}
	pg, err := fakepg.New(n, fakepg.Config{Auth: fakepg.AuthSCRAM, Users: []string{PoolUser}, Databases: []string{Database},
		Verifiers: []fakepg.UserVerifier{{User: PoolUser, Verifier: poolVerifier}}, TLS: sc.PGTLS,
		Rows: []fakepg.Row{{Key: "k", Value: "v"}}})
	if err != nil {
		return nil, err
	}
	opts := simnet.DefaultOptions()
	if sc.OneByte {
		opts = oneByteOptions()
	}
	pw := PoolPassword
	if sc.BackendPassword != "" {
		pw = sc.BackendPassword
	}
	ccfg := pgwire.ClientConfig{TLS: sc.ClientTLS, Tenants: table, Database: Database, SessionSettings: pgwire.DefaultSessionSettings(),
		MockSecret: []byte("sim-mock"), MockIterations: Iterations}
	if sc.Client != nil {
		sc.Client(&ccfg)
	}
	proxy, err := pgwiretest.New(n, pgwiretest.Config{Database: pg.Addr(), Client: ccfg, Options: opts,
		Backend: pgwire.BackendConfig{TLS: sc.BackendTLS, User: PoolUser, Database: Database, Password: pw, SessionSettings: pgwire.DefaultSessionSettings()}})
	if err != nil {
		return nil, err
	}
	ws := []workload.Tenant{goodTenant(TenantName(0), 0, clientTLSFor(sc.ClientTLS)), goodTenant(TenantName(1), RecoverAt, clientTLSFor(sc.ClientTLS))}
	if sc.Fault != nil {
		ws = append(ws, sc.faultTenant())
	}
	wl, err := workload.New(n, workload.Config{Addr: proxy.Addr(), Options: opts, Tenants: ws})
	if err != nil {
		return nil, err
	}
	w := &World{Sim: s, Net: n, PG: pg, Proxy: proxy, Workload: wl, Scenario: sc}
	if sc.Inject != nil {
		s.At(sim.Time(FaultAt), func() { sc.Inject(w) })
	}
	if sc.Clear != nil {
		s.At(sim.Time(RecoverAt-time.Second), func() { sc.Clear(w) })
	}
	wl.Start()
	return w, nil
}

type Result struct {
	World    *World
	AtCheck  pgwire.WireSnapshot
	Final    pgwire.WireSnapshot
	Workload workload.Snapshot
	Counters pgwiretest.Counters
	Auth     fakepg.AuthSnapshot
	Err      error
}

func (sc Scenario) CheckAt() time.Duration {
	if sc.Want.By == 0 {
		return FaultAt + CheckMargin
	}
	return sc.Want.By
}

func (w *World) Run() Result {
	r := Result{World: w}
	if err := w.Sim.RunUntil(sim.Time(w.Scenario.CheckAt())); err != nil {
		r.Err = err
		return r
	}
	r.AtCheck = w.Proxy.Snapshot()
	if err := w.Sim.RunUntil(sim.Time(ScenarioLimit)); err != nil {
		r.Err = err
		return r
	}
	if err := w.Sim.RunUntilQuiet(); err != nil {
		r.Err = err
		return r
	}
	r.Err = w.Sim.CheckQuiesce()
	r.Final, r.Workload, r.Counters, r.Auth = w.Proxy.Snapshot(), w.Workload.Snapshot(), w.Proxy.Counters(), w.PG.AuthSnapshot()
	return r
}

func Run(seed int64, sc Scenario, replay string) Result {
	w, err := Build(seed, sc, replay)
	if err != nil {
		return Result{Err: err}
	}
	return w.Run()
}

func (r Result) Tenant(name string) workload.TenantSnapshot {
	for _, ts := range r.Workload.Tenants {
		if ts.Name == name {
			return ts
		}
	}
	return workload.TenantSnapshot{}
}

func Failures(s pgwire.WireSnapshot) (map[pgwire.Reason]int64, map[pgwire.Class]int64) {
	c, d := map[pgwire.Reason]int64{}, map[pgwire.Class]int64{}
	for i, v := range s.Client.StartupsFailed {
		if v != 0 {
			c[pgwire.Reason(i)] = v
		}
	}
	for i, v := range s.Database.StartupsFailed {
		if v != 0 {
			d[pgwire.Class(i)] = v
		}
	}
	return c, d
}

func sameCounts[K comparable](a, b map[K]int64) bool {
	return maps.Equal(a, b)
}

func (sc Scenario) Verify(r Result) error {
	if r.Err != nil {
		return r.Err
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if err := r.Final.Check(); err != nil {
		fail("%v", err)
	}
	gotC, gotD := Failures(r.Final)
	if !sameCounts(gotC, sc.Want.Client) {
		fail("client startup failures %v, want %v", gotC, sc.Want.Client)
	}
	if !sameCounts(gotD, sc.Want.Database) {
		fail("database startup failures %v, want %v", gotD, sc.Want.Database)
	}
	checkC, checkD := Failures(r.AtCheck)
	if !sameCounts(checkC, sc.Want.Client) || !sameCounts(checkD, sc.Want.Database) {
		fail("not every failure had ended by the deadline check: client %v database %v", checkC, checkD)
	}
	for _, name := range []string{TenantName(0), TenantName(1)} {
		ts := r.Tenant(name)
		if ts.OK != GoodClients*GoodRequests || ts.FailedBeforeSend != 0 {
			fail("%s: ok %d of %d, failed before send %d", name, ts.OK, GoodClients*GoodRequests, ts.FailedBeforeSend)
		}
	}
	if sc.Fault != nil {
		ft := r.Tenant(r.FaultName())
		if sc.Want.OKFault != (ft.OK > 0) {
			fail("fault tenant ok %d", ft.OK)
		}
	}
	good := int64(goodTenants * GoodClients)
	if r.Final.Client.StartupsCompleted < good || r.Final.Database.StartupsCompleted < good || r.Counters.Queries < good*GoodRequests {
		fail("counters did not move: %+v", r.Counters)
	}
	if r.Final.Client.BytesIn == 0 || r.Final.Database.BytesIn == 0 || r.Final.Database.Replies == 0 {
		fail("no bytes moved")
	}
	st := r.Workload.Total.Startup
	if got := r.Final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerYes] + r.Final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerNo]; got != st.TLSRequests {
		fail("TLS requests answered by the proxy %d, sent by the workload %d", got, st.TLSRequests)
	}
	if r.Final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerYes] != st.TLSAnsweredYes {
		fail("TLS answered S: proxy %d, workload %d", r.Final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerYes], st.TLSAnsweredYes)
	}
	if r.Final.Database.TLSAnswers[pgwire.AnswerYes] != r.Auth.TLSAnsweredYes || r.Final.Database.TLSAnswers[pgwire.AnswerNo] != r.Auth.TLSAnsweredNo {
		fail("database TLS answers: proxy %v, fake S %d N %d", r.Final.Database.TLSAnswers, r.Auth.TLSAnsweredYes, r.Auth.TLSAnsweredNo)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", sc.Name, strings.Join(errs, "; "))
	}
	return nil
}

func (r Result) FaultName() string {
	if r.World.Scenario.Fault != nil && r.World.Scenario.Fault.Name != "" {
		return r.World.Scenario.Fault.Name
	}
	return TenantName(goodTenants)
}

func clientFault(f workload.TLSFault, tls workload.TLSRequest) *workload.Tenant {
	return &workload.Tenant{TLS: tls, TLSFault: f}
}

func pgFault(kind sim.FaultKind, a1, a2 int64) func(w *World) {
	return func(w *World) { w.Sim.ApplyFault(sim.Fault{Kind: kind, Arg1: a1, Arg2: a2}) }
}

func pgClear(kind sim.FaultKind) func(w *World) {
	return func(w *World) { w.Sim.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Arg1: int64(kind)}) }
}

func resetMidHandshake(w *World) {
	ids := w.Proxy.AuthenticatingBackends()
	if len(ids) == 0 {
		w.Sim.After(100*time.Microsecond, func() { resetMidHandshake(w) })
		return
	}
	w.Sim.ApplyFault(sim.Fault{Kind: fakepg.FaultReset, Target: ids[0], Arg1: int64(fakepg.PointNow)})
}

func one[K comparable](k K) map[K]int64 { return map[K]int64{k: 1} }

func Scenarios() []Scenario {
	yes := fakepg.TLSConfig{Answer: 'S'}
	dbFaultTenant := &workload.Tenant{}
	return []Scenario{
		{Name: "wire-startup-plain"},
		{Name: "wire-startup-tls", ClientTLS: pgwire.ClientTLSRequire, BackendTLS: pgwire.BackendTLSRequire, PGTLS: yes},
		{Name: "wire-one-byte", ClientTLS: pgwire.ClientTLSAllow, BackendTLS: pgwire.BackendTLSRequire, PGTLS: yes, OneByte: true},
		{Name: "wire-one-byte-plain", OneByte: true},
		{Name: "wire-client-tls-stall", ClientTLS: pgwire.ClientTLSRequire, Fault: clientFault(workload.TLSFaultStall, workload.TLSRequire),
			Want: Expect{Client: one(pgwire.ReasonDeadline), By: FaultAt + pgwire.DefaultClientStartupTimeout + CheckMargin}},
		{Name: "wire-client-bad-hello", ClientTLS: pgwire.ClientTLSAllow, Fault: clientFault(workload.TLSFaultBadHello, workload.TLSRequire),
			Want: Expect{Client: one(pgwire.ReasonTLSFailed)}},
		{Name: "wire-client-smuggle", ClientTLS: pgwire.ClientTLSRequire, Fault: clientFault(workload.TLSFaultSmuggle, workload.TLSRequire),
			Want: Expect{Client: one(pgwire.ReasonViolation)}},
		{Name: "wire-client-second-request", ClientTLS: pgwire.ClientTLSRequire, Fault: clientFault(workload.TLSFaultSecondRequest, workload.TLSRequire),
			Want: Expect{Client: one(pgwire.ReasonViolation)}},
		{Name: "wire-client-plaintext-refused", ClientTLS: pgwire.ClientTLSRequire, Fault: clientFault(workload.TLSFaultNone, workload.TLSOff),
			Want: Expect{Client: one(pgwire.ReasonPlaintextRefused)}},
		{Name: "wire-client-wrong-password", Fault: &workload.Tenant{Password: "wrong"}, Want: Expect{Client: one(pgwire.ReasonAuthFailed)}},
		{Name: "wire-client-unknown-user", Fault: &workload.Tenant{Name: "stranger", Password: "x"}, Want: Expect{Client: one(pgwire.ReasonAuthFailed)}},
		{Name: "wire-db-silent-startup", Fault: dbFaultTenant, Inject: pgFault(fakepg.FaultSilentStartup, 0, 0), Clear: pgClear(fakepg.FaultSilentStartup),
			Want: Expect{Database: one(pgwire.ClassDeadline), By: FaultAt + pgwire.DefaultConnectTimeout + CheckMargin}},
		{Name: "wire-db-connect-stall", Fault: dbFaultTenant, Inject: pgFault(fakepg.FaultConnectStall, 0, 0), Clear: pgClear(fakepg.FaultConnectStall),
			Want: Expect{Database: one(pgwire.ClassDeadline), By: FaultAt + pgwire.DefaultConnectTimeout + CheckMargin}},
		{Name: "wire-db-garbage", Fault: dbFaultTenant, Inject: pgFault(fakepg.FaultNotPostgres, 0, 0), Clear: pgClear(fakepg.FaultNotPostgres),
			Want: Expect{Database: one(pgwire.ClassViolation)}},
		{Name: "wire-db-reset-mid-handshake", Fault: dbFaultTenant, Inject: resetMidHandshake, Want: Expect{Database: one(pgwire.ClassLost)}},
		{Name: "wire-db-max-connections", Fault: dbFaultTenant, Inject: pgFault(fakepg.FaultMaxConn, 0, 0), Clear: pgClear(fakepg.FaultMaxConn),
			Want: Expect{Database: one(pgwire.ClassTooManyConnections)}},
		{Name: "wire-db-tls-refused", BackendTLS: pgwire.BackendTLSRequire, PGTLS: yes, Fault: dbFaultTenant,
			Inject: func(w *World) { w.PG.SetTLS(fakepg.TLSConfig{Answer: 'N'}) }, Clear: func(w *World) { w.PG.SetTLS(yes) },
			Want: Expect{Database: one(pgwire.ClassTLSRefused)}},
		{Name: "wire-db-bad-certificate", BackendTLS: pgwire.BackendTLSVerifyFull, PGTLS: yes, Fault: dbFaultTenant,
			Inject: func(w *World) { w.PG.SetTLS(fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSBadCertificate}) },
			Clear:  func(w *World) { w.PG.SetTLS(yes) }, Want: Expect{Database: one(pgwire.ClassTLSFailed)}},
		{Name: "wire-db-handshake-stall", BackendTLS: pgwire.BackendTLSRequire, PGTLS: yes, Fault: dbFaultTenant,
			Inject: func(w *World) { w.PG.SetTLS(fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSStall}) },
			Clear:  func(w *World) { w.PG.SetTLS(yes) }, Want: Expect{Database: one(pgwire.ClassDeadline), By: FaultAt + pgwire.DefaultConnectTimeout + CheckMargin}},
	}
}

func Lookup(name string) (Scenario, bool) {
	for _, sc := range Scenarios() {
		if sc.Name == name {
			return sc, true
		}
	}
	return Scenario{}, false
}
