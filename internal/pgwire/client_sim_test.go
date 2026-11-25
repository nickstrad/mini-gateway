package pgwire_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

const (
	simIters      = 16
	poolUser      = "pool"
	poolPassword  = "pool-pw"
	simDatabase   = "app"
	faultAt       = 20 * time.Millisecond
	recoverAt     = 8 * time.Second
	checkMargin   = 50 * time.Millisecond
	goodClients   = 4
	goodRequests  = 3
	requestGap    = 5 * time.Millisecond
	clientPrefix  = "tenant"
	scenarioLimit = 30 * time.Second
)

type wireWorld struct {
	s     *sim.Sim
	n     *simnet.Net
	pg    *fakepg.Server
	proxy *pgwiretest.Proxy
	wl    *workload.Workload
	sc    wireScenario
}

type expect struct {
	client   map[pgwire.Reason]int64
	database map[pgwire.Class]int64
	by       time.Duration
	okFault  bool
}

type wireScenario struct {
	name       string
	clientTLS  pgwire.ClientTLSMode
	backendTLS pgwire.BackendTLSMode
	pgTLS      fakepg.TLSConfig
	oneByte    bool
	fault      *workload.Tenant
	faultCount int
	inject     func(w *wireWorld)
	clear      func(w *wireWorld)
	backendPw  string
	want       expect
	plant      bool
}

func verifierFor(t testing.TB, password, salt string) pgwire.Verifier {
	v, err := pgwire.NewVerifier(password, []byte(salt), simIters)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tenantName(i int) string { return fmt.Sprintf("%s%d", clientPrefix, i) }

func tenantPassword(name string) string { return "pw-" + name }

func goodTenant(name string, start time.Duration, tls workload.TLSRequest) workload.Tenant {
	return workload.Tenant{Name: name, Database: simDatabase, Clients: goodClients, Requests: goodRequests, Interval: requestGap,
		Start: start, Statement: "SELECT 1", Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k"}, Password: tenantPassword(name), TLS: tls}
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

func buildWire(t testing.TB, seed int64, sc wireScenario, replay string) *wireWorld {
	s := sim.New(sim.Config{Seed: seed, Scenario: sc.name})
	s.SetReplay(replay)
	n := simnet.New(s)
	var tenants []pgwire.Tenant
	for i := 0; i < 3; i++ {
		name := tenantName(i)
		tenants = append(tenants, pgwire.Tenant{User: name, Verifier: verifierFor(t, tenantPassword(name), "salt-"+name+"-012345")})
	}
	table, err := pgwire.NewTenantTable(tenants)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := fakepg.New(n, fakepg.Config{Auth: fakepg.AuthSCRAM, Users: []string{poolUser}, Databases: []string{simDatabase},
		Verifiers: []fakepg.UserVerifier{{User: poolUser, Verifier: verifierFor(t, poolPassword, "pool-salt-012345")}}, TLS: sc.pgTLS,
		Rows: []fakepg.Row{{Key: "k", Value: "v"}}})
	if err != nil {
		t.Fatal(err)
	}
	opts := simnet.DefaultOptions()
	if sc.oneByte {
		opts = oneByteOptions()
	}
	pw := poolPassword
	if sc.backendPw != "" {
		pw = sc.backendPw
	}
	ccfg := pgwire.ClientConfig{TLS: sc.clientTLS, Tenants: table, Database: simDatabase, SessionSettings: pgwire.DefaultSessionSettings(),
		MockSecret: []byte("sim-mock"), MockIterations: simIters}
	if sc.plant {
		pgwire.PlantEarlyBytes(&ccfg)
	}
	proxy, err := pgwiretest.New(n, pgwiretest.Config{Database: pg.Addr(), Client: ccfg, Options: opts,
		Backend: pgwire.BackendConfig{TLS: sc.backendTLS, User: poolUser, Database: simDatabase, Password: pw, SessionSettings: pgwire.DefaultSessionSettings()}})
	if err != nil {
		t.Fatal(err)
	}
	ws := []workload.Tenant{goodTenant(tenantName(0), 0, clientTLSFor(sc.clientTLS)), goodTenant(tenantName(1), recoverAt, clientTLSFor(sc.clientTLS))}
	if sc.fault != nil {
		f := *sc.fault
		f.Database, f.Statement, f.Keys, f.Start = simDatabase, "SELECT 1", workload.Keys{Kind: workload.KeysFixed, Fixed: "k"}, faultAt
		if f.Name == "" {
			f.Name = tenantName(2)
		}
		if f.Password == "" {
			f.Password = tenantPassword(f.Name)
		}
		if f.Clients == 0 {
			f.Clients = 1
		}
		if f.Requests == 0 {
			f.Requests = 1
		}
		ws = append(ws, f)
	}
	wl, err := workload.New(n, workload.Config{Addr: proxy.Addr(), Options: opts, Tenants: ws})
	if err != nil {
		t.Fatal(err)
	}
	w := &wireWorld{s: s, n: n, pg: pg, proxy: proxy, wl: wl, sc: sc}
	if sc.inject != nil {
		s.At(sim.Time(faultAt), func() { sc.inject(w) })
	}
	if sc.clear != nil {
		s.At(sim.Time(recoverAt-time.Second), func() { sc.clear(w) })
	}
	wl.Start()
	return w
}

type wireResult struct {
	w        *wireWorld
	atCheck  pgwire.WireSnapshot
	final    pgwire.WireSnapshot
	wl       workload.Snapshot
	counters pgwiretest.Counters
	auth     fakepg.AuthSnapshot
	err      error
}

func runWire(t testing.TB, seed int64, sc wireScenario, replay string) wireResult {
	w := buildWire(t, seed, sc, replay)
	r := wireResult{w: w}
	by := sc.want.by
	if by == 0 {
		by = faultAt + checkMargin
	}
	if err := w.s.RunUntil(sim.Time(by)); err != nil {
		r.err = err
		return r
	}
	r.atCheck = w.proxy.Snapshot()
	if err := w.s.RunUntil(sim.Time(scenarioLimit)); err != nil {
		r.err = err
		return r
	}
	if err := w.s.RunUntilQuiet(); err != nil {
		r.err = err
		return r
	}
	r.err = w.s.CheckQuiesce()
	r.final, r.wl, r.counters, r.auth = w.proxy.Snapshot(), w.wl.Snapshot(), w.proxy.Counters(), w.pg.AuthSnapshot()
	return r
}

func (r wireResult) tenant(name string) workload.TenantSnapshot {
	for _, ts := range r.wl.Tenants {
		if ts.Name == name {
			return ts
		}
	}
	return workload.TenantSnapshot{}
}

func failuresOf(s pgwire.WireSnapshot) (map[pgwire.Reason]int64, map[pgwire.Class]int64) {
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
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (sc wireScenario) verify(r wireResult) error {
	if r.err != nil {
		return r.err
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if err := r.final.Check(); err != nil {
		fail("%v", err)
	}
	gotC, gotD := failuresOf(r.final)
	if !sameCounts(gotC, sc.want.client) {
		fail("client startup failures %v, want %v", gotC, sc.want.client)
	}
	if !sameCounts(gotD, sc.want.database) {
		fail("database startup failures %v, want %v", gotD, sc.want.database)
	}
	checkC, checkD := failuresOf(r.atCheck)
	if !sameCounts(checkC, sc.want.client) || !sameCounts(checkD, sc.want.database) {
		fail("not every failure had ended by the deadline check: client %v database %v", checkC, checkD)
	}
	for _, name := range []string{tenantName(0), tenantName(1)} {
		ts := r.tenant(name)
		if ts.OK != goodClients*goodRequests || ts.FailedBeforeSend != 0 {
			fail("%s: ok %d of %d, failed before send %d", name, ts.OK, goodClients*goodRequests, ts.FailedBeforeSend)
		}
	}
	if sc.fault != nil {
		ft := r.tenant(r.faultName())
		if sc.want.okFault != (ft.OK > 0) {
			fail("fault tenant ok %d", ft.OK)
		}
	}
	good := int64(2 * goodClients)
	if r.final.Client.StartupsCompleted < good || r.final.Database.StartupsCompleted < good || r.counters.Queries < good*goodRequests {
		fail("counters did not move: %+v", r.counters)
	}
	if r.final.Client.BytesIn == 0 || r.final.Database.BytesIn == 0 || r.final.Database.Replies == 0 {
		fail("no bytes moved")
	}
	st := r.wl.Total.Startup
	if got := r.final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerYes] + r.final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerNo]; got != st.TLSRequests {
		fail("TLS requests answered by the proxy %d, sent by the workload %d", got, st.TLSRequests)
	}
	if r.final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerYes] != st.TLSAnsweredYes {
		fail("TLS answered S: proxy %d, workload %d", r.final.Client.Requests[pgwire.RequestTLS][pgwire.AnswerYes], st.TLSAnsweredYes)
	}
	if r.final.Database.TLSAnswers[pgwire.AnswerYes] != r.auth.TLSAnsweredYes || r.final.Database.TLSAnswers[pgwire.AnswerNo] != r.auth.TLSAnsweredNo {
		fail("database TLS answers: proxy %v, fake S %d N %d", r.final.Database.TLSAnswers, r.auth.TLSAnsweredYes, r.auth.TLSAnsweredNo)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", sc.name, strings.Join(errs, "; "))
	}
	return nil
}

func (r wireResult) faultName() string {
	if r.w.sc.fault != nil && r.w.sc.fault.Name != "" {
		return r.w.sc.fault.Name
	}
	return tenantName(2)
}

func clientFault(f workload.TLSFault, tls workload.TLSRequest) *workload.Tenant {
	return &workload.Tenant{TLS: tls, TLSFault: f}
}

func pgFault(kind sim.FaultKind, a1, a2 int64) func(w *wireWorld) {
	return func(w *wireWorld) { w.s.ApplyFault(sim.Fault{Kind: kind, Arg1: a1, Arg2: a2}) }
}

func pgClear(kind sim.FaultKind) func(w *wireWorld) {
	return func(w *wireWorld) { w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Arg1: int64(kind)}) }
}

func resetMidHandshake(w *wireWorld) {
	ids := w.proxy.AuthenticatingBackends()
	if len(ids) == 0 {
		w.s.After(100*time.Microsecond, func() { resetMidHandshake(w) })
		return
	}
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultReset, Target: ids[0], Arg1: int64(fakepg.PointNow)})
}

func one[K comparable](k K) map[K]int64 { return map[K]int64{k: 1} }

func wireScenarios() []wireScenario {
	yes := fakepg.TLSConfig{Answer: 'S'}
	dbFaultTenant := &workload.Tenant{}
	return []wireScenario{
		{name: "wire-startup-plain"},
		{name: "wire-startup-tls", clientTLS: pgwire.ClientTLSRequire, backendTLS: pgwire.BackendTLSRequire, pgTLS: yes},
		{name: "wire-one-byte", clientTLS: pgwire.ClientTLSAllow, backendTLS: pgwire.BackendTLSRequire, pgTLS: yes, oneByte: true},
		{name: "wire-one-byte-plain", oneByte: true},
		{name: "wire-client-tls-stall", clientTLS: pgwire.ClientTLSRequire, fault: clientFault(workload.TLSFaultStall, workload.TLSRequire),
			want: expect{client: one(pgwire.ReasonDeadline), by: faultAt + pgwire.DefaultClientStartupTimeout + checkMargin}},
		{name: "wire-client-bad-hello", clientTLS: pgwire.ClientTLSAllow, fault: clientFault(workload.TLSFaultBadHello, workload.TLSRequire),
			want: expect{client: one(pgwire.ReasonTLSFailed)}},
		{name: "wire-client-smuggle", clientTLS: pgwire.ClientTLSRequire, fault: clientFault(workload.TLSFaultSmuggle, workload.TLSRequire),
			want: expect{client: one(pgwire.ReasonViolation)}},
		{name: "wire-client-second-request", clientTLS: pgwire.ClientTLSRequire, fault: clientFault(workload.TLSFaultSecondRequest, workload.TLSRequire),
			want: expect{client: one(pgwire.ReasonViolation)}},
		{name: "wire-client-plaintext-refused", clientTLS: pgwire.ClientTLSRequire, fault: clientFault(workload.TLSFaultNone, workload.TLSOff),
			want: expect{client: one(pgwire.ReasonPlaintextRefused)}},
		{name: "wire-client-wrong-password", fault: &workload.Tenant{Password: "wrong"}, want: expect{client: one(pgwire.ReasonAuthFailed)}},
		{name: "wire-client-unknown-user", fault: &workload.Tenant{Name: "stranger", Password: "x"}, want: expect{client: one(pgwire.ReasonAuthFailed)}},
		{name: "wire-db-silent-startup", fault: dbFaultTenant, inject: pgFault(fakepg.FaultSilentStartup, 0, 0), clear: pgClear(fakepg.FaultSilentStartup),
			want: expect{database: one(pgwire.ClassDeadline), by: faultAt + pgwire.DefaultConnectTimeout + checkMargin}},
		{name: "wire-db-connect-stall", fault: dbFaultTenant, inject: pgFault(fakepg.FaultConnectStall, 0, 0), clear: pgClear(fakepg.FaultConnectStall),
			want: expect{database: one(pgwire.ClassDeadline), by: faultAt + pgwire.DefaultConnectTimeout + checkMargin}},
		{name: "wire-db-garbage", fault: dbFaultTenant, inject: pgFault(fakepg.FaultNotPostgres, 0, 0), clear: pgClear(fakepg.FaultNotPostgres),
			want: expect{database: one(pgwire.ClassViolation)}},
		{name: "wire-db-reset-mid-handshake", fault: dbFaultTenant, inject: resetMidHandshake, want: expect{database: one(pgwire.ClassLost)}},
		{name: "wire-db-max-connections", fault: dbFaultTenant, inject: pgFault(fakepg.FaultMaxConn, 0, 0), clear: pgClear(fakepg.FaultMaxConn),
			want: expect{database: one(pgwire.ClassTooManyConnections)}},
		{name: "wire-db-tls-refused", backendTLS: pgwire.BackendTLSRequire, pgTLS: yes, fault: dbFaultTenant,
			inject: func(w *wireWorld) { w.pg.SetTLS(fakepg.TLSConfig{Answer: 'N'}) }, clear: func(w *wireWorld) { w.pg.SetTLS(yes) },
			want: expect{database: one(pgwire.ClassTLSRefused)}},
		{name: "wire-db-bad-certificate", backendTLS: pgwire.BackendTLSVerifyFull, pgTLS: yes, fault: dbFaultTenant,
			inject: func(w *wireWorld) { w.pg.SetTLS(fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSBadCertificate}) },
			clear:  func(w *wireWorld) { w.pg.SetTLS(yes) }, want: expect{database: one(pgwire.ClassTLSFailed)}},
		{name: "wire-db-handshake-stall", backendTLS: pgwire.BackendTLSRequire, pgTLS: yes, fault: dbFaultTenant,
			inject: func(w *wireWorld) { w.pg.SetTLS(fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSStall}) },
			clear:  func(w *wireWorld) { w.pg.SetTLS(yes) }, want: expect{database: one(pgwire.ClassDeadline), by: faultAt + pgwire.DefaultConnectTimeout + checkMargin}},
	}
}

func TestScenarioWireStartup(t *testing.T) {
	for _, sc := range wireScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			simtest.Sweep(t, func(t *testing.T, seed int64) {
				var results []wireResult
				run := func(seed int64) *sim.Sim {
					r := runWire(t, seed, sc, simtest.ReplayCommand(t, seed))
					results = append(results, r)
					return r.w.s
				}
				simtest.RequireDeterministic(t, seed, run)
				a, b := results[0], results[1]
				if fmt.Sprintf("%+v %+v", a.final, a.counters) != fmt.Sprintf("%+v %+v", b.final, b.counters) {
					t.Fatal("snapshots differ between two runs of one seed")
				}
				if err := sc.verify(a); err != nil {
					t.Fatalf("%v\n%s", err, a.w.s.Replay())
				}
			})
		})
	}
}
