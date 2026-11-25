package workload_test

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

const scramPassword = "pw-app"

func scramFake(t testing.TB, tls fakepg.TLSConfig) fakepg.Config {
	v, err := pgwire.NewVerifier(scramPassword, []byte("fake-salt-012345"), 16)
	if err != nil {
		t.Fatal(err)
	}
	return fakepg.Config{Auth: fakepg.AuthSCRAM, Users: []string{"app"}, Databases: []string{"app"},
		Verifiers: []fakepg.UserVerifier{{User: "app", Verifier: v}}, TLS: tls}
}

func startupTenant(tls workload.TLSRequest, fault workload.TLSFault, password string) workload.Tenant {
	return workload.Tenant{Name: "app", Clients: 1, Requests: 1, Statement: "SELECT 1", Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: "k"},
		Password: password, TLS: tls, TLSFault: fault, Deadline: 500 * time.Millisecond}
}

type startupCase struct {
	name     string
	pg       fakepg.TLSConfig
	tenant   workload.Tenant
	ok       bool
	code     string
	check    func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot)
	database string
}

func runStartupCase(t *testing.T, seed int64, tc startupCase) (workload.Snapshot, fakepg.AuthSnapshot) {
	pg := scramFake(t, tc.pg)
	if tc.database != "" {
		tc.tenant.Database = tc.database
	}
	w := build(t, seed, pg, workload.Config{Tenants: []workload.Tenant{tc.tenant}})
	w.finish(t)
	return w.wl.Snapshot(), w.srv.AuthSnapshot()
}

func startupCases() []startupCase {
	yes := fakepg.TLSConfig{Answer: 'S'}
	return []startupCase{
		{name: "scram-plaintext", tenant: startupTenant(workload.TLSOff, workload.TLSFaultNone, scramPassword), ok: true,
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.SCRAMOK != 1 || a.SCRAMOK != 1 || c.TLSRequests != 0 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "prefer-answered-N", tenant: startupTenant(workload.TLSPrefer, workload.TLSFaultNone, scramPassword), ok: true,
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.TLSAnsweredNo != 1 || a.TLSAnsweredNo != 1 || c.SCRAMOK != 1 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "require-answered-N", tenant: startupTenant(workload.TLSRequire, workload.TLSFaultNone, scramPassword),
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.TLSAnsweredNo != 1 || c.SCRAMStarted != 0 {
					t.Fatalf("%+v", c)
				}
			}},
		{name: "require-over-tls-with-plus-offered", pg: yes, tenant: startupTenant(workload.TLSRequire, workload.TLSFaultNone, scramPassword), ok: true,
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.HandshakesOK != 1 || a.HandshakesByResult[fakepg.TLSCompleted] != 1 || c.SCRAMOK != 1 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "handshake-failed", pg: fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSFailed}, tenant: startupTenant(workload.TLSPrefer, workload.TLSFaultNone, scramPassword),
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.HandshakesFailed != 1 || a.HandshakesByResult[fakepg.TLSFailed] != 1 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "bad-certificate-not-verified-by-the-client", pg: fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSBadCertificate},
			tenant: startupTenant(workload.TLSRequire, workload.TLSFaultNone, scramPassword), ok: true},
		{name: "handshake-stalls", pg: fakepg.TLSConfig{Answer: 'S', Outcome: fakepg.TLSStall}, tenant: startupTenant(workload.TLSRequire, workload.TLSFaultNone, scramPassword),
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if a.HandshakesByResult[fakepg.TLSStall] != 1 || c.HandshakesOK != 0 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "client-bad-hello", pg: yes, tenant: startupTenant(workload.TLSRequire, workload.TLSFaultBadHello, scramPassword),
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if a.BadHellos != 1 || c.HandshakesFailed != 1 || c.TLSFaults[workload.TLSFaultBadHello] != 1 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "client-stalls-after-request", pg: yes, tenant: startupTenant(workload.TLSRequire, workload.TLSFaultStall, scramPassword),
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.TLSFaults[workload.TLSFaultStall] != 1 || a.HandshakesStarted != 0 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "client-smuggles-plaintext", pg: yes, tenant: startupTenant(workload.TLSRequire, workload.TLSFaultSmuggle, scramPassword),
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.TLSFaults[workload.TLSFaultSmuggle] != 1 || a.BadHellos != 1 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "client-second-request", pg: yes, tenant: startupTenant(workload.TLSRequire, workload.TLSFaultSecondRequest, scramPassword), ok: false,
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if c.TLSFaults[workload.TLSFaultSecondRequest] != 1 || a.TLSAnsweredNo != 1 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "wrong-password", tenant: startupTenant(workload.TLSOff, workload.TLSFaultNone, "nope"), code: "28P01",
			check: func(t *testing.T, c workload.StartupCounters, a fakepg.AuthSnapshot) {
				if a.SCRAMFailed != 1 || c.SCRAMOK != 0 {
					t.Fatalf("%+v %+v", c, a)
				}
			}},
		{name: "wrong-database", tenant: startupTenant(workload.TLSOff, workload.TLSFaultNone, scramPassword), database: "other", code: "3D000"},
	}
}

func TestStartupAgainstTheFake(t *testing.T) {
	for _, tc := range startupCases() {
		t.Run(tc.name, func(t *testing.T) {
			snap, auth := runStartupCase(t, 1, tc)
			tot := snap.Total
			if tc.ok != (tot.OK == 1) || tc.ok == (tot.FailedBeforeSend == 1) {
				t.Fatalf("ok %d failed-before-send %d: %+v", tot.OK, tot.FailedBeforeSend, tot)
			}
			if tc.code != "" && tot.Code(tc.code) != 1 {
				t.Fatalf("code %s: %+v", tc.code, tot.ErrorsByCode)
			}
			if tc.check != nil {
				tc.check(t, tot.Startup, auth)
			}
		})
	}
}

func TestStartupCountersMove(t *testing.T) {
	moved := map[string]bool{}
	for _, tc := range startupCases() {
		snap, auth := runStartupCase(t, 2, tc)
		obstest.MergeMoved(moved, snap.Total.Startup)
		obstest.MergeMoved(moved, auth)
	}
	for _, name := range obstest.NeverMoved(moved) {
		if name != "TLSFaults[0]" && name != "ChannelBindingRefs" && name != "TLSChanges" && name != "GSSAnsweredNo" {
			t.Errorf("%s never moved", name)
		}
	}
}

func TestFakeTLSAnswerCanChangeDuringARun(t *testing.T) {
	pg := scramFake(t, fakepg.TLSConfig{Answer: 'N'})
	tenant := startupTenant(workload.TLSRequire, workload.TLSFaultNone, scramPassword)
	tenant.Requests, tenant.Interval = 2, 100*time.Millisecond
	w := build(t, 3, pg, workload.Config{Tenants: []workload.Tenant{tenant}})
	w.s.At(sim.Time(50*time.Millisecond), func() { w.srv.SetTLS(fakepg.TLSConfig{Answer: 'S'}) })
	w.finish(t)
	tot := w.wl.Snapshot().Total
	if tot.FailedBeforeSend != 1 || tot.OK != 1 || tot.Startup.TLSAnsweredNo != 1 || tot.Startup.TLSAnsweredYes != 1 || w.srv.AuthSnapshot().TLSChanges != 1 {
		t.Fatalf("%+v %+v", tot, w.srv.AuthSnapshot())
	}
}
