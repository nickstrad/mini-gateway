package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	headlineClients = 500
	headlinePool    = 16
	stormMaxStartup = 64
)

func backends(t *testing.T, env *RealEnv) int {
	t.Helper()
	c, err := env.Super()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := c.Query("SELECT count(*) FROM pg_stat_activity WHERE usename = '" + RealBackendUser + "' AND datname = '" + RealDatabase + "'")
	for _, tk := range got {
		if v, ok := strings.CutPrefix(tk, "D:"); ok {
			n, err := strconv.Atoi(v)
			if err == nil {
				return n
			}
		}
	}
	t.Fatalf("backend count: %v", got)
	return 0
}

func poolMetric(final obs.InstanceSnapshot, metric string) int64 {
	return final.Value(obs.ComponentPool, metric, obs.Labels{})
}

func TestTierRPoolEmptyQueryCheckAndServerKeepalive(t *testing.T) {
	env := tierR(t)
	d, err := env.DialDirect(false)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Query(""); !slices.Equal(got, []string{"I", "ZI"}) {
		t.Fatalf("the empty-query check against Postgres 17 replied %v, want exactly EmptyQueryResponse and ReadyForQuery(I)", got)
	}
	d.Close()
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-pool-keepalive", Conns: 2})
	c, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"tcp_keepalives_idle": "30", "tcp_keepalives_interval": "10", "tcp_keepalives_count": "3"} {
		if got := c.Query("SHOW " + name); !slices.Contains(got, "D:"+want) {
			t.Fatalf("SHOW %s on a pooled connection: %v, want %s", name, got, want)
		}
	}
	port := env.PGAddr[strings.LastIndex(env.PGAddr, ":")+1:]
	out, err := exec.Command("ss", "-tnoi", "state", "established", "dst", "127.0.0.1:"+port).CombinedOutput()
	if err != nil {
		t.Fatalf("ss: %v: %s", err, out)
	}
	keepalive := strings.Count(string(out), "timer:(keepalive")
	if keepalive == 0 {
		t.Fatalf("no database socket shows a keepalive timer:\n%s", out)
	}
	c.Close()
	final := stopAndRead(t, env, g, "tierr-pool-keepalive")
	t.Logf("tier R: SHOW tcp_keepalives_* = 30/10/3 on a pooled connection; %d database sockets with a keepalive timer; pool dials %d", keepalive, poolMetric(final, "dials"))
}

type policyResult struct {
	crossings, sameTenant     int
	residueOther, residueSame bool
}

func randomAfter(t *testing.T, c *RealClient) string {
	t.Helper()
	for _, tk := range c.Query("SELECT random()") {
		if v, ok := strings.CutPrefix(tk, "D:"); ok {
			return v
		}
	}
	t.Fatal("no random() value")
	return ""
}

func seededValue(t *testing.T, env *RealEnv) string {
	t.Helper()
	d, err := env.DialDirect(false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Query("SELECT setseed(0.25)")
	return randomAfter(t, d)
}

func policyRun(t *testing.T, env *RealEnv, reset, reseed string) policyResult {
	t.Helper()
	scenario := "tierr-pool-session-" + reset
	if reseed == "on" {
		scenario += "-reseed"
	}
	g := gateway(t, env, GatewayOpts{Scenario: scenario, Conns: 1, Reset: reset, Reseed: reseed})
	check := dirtyCheck(t)
	var r policyResult
	clean := ""
	for round := 0; round < 2; round++ {
		for i, call := range hostileCalls {
			h, err := env.Dial(g, 2, false)
			if err != nil {
				t.Fatal(err)
			}
			h.Query(call)
			h.Close()
			tenant := 1
			if i%2 == 1 {
				tenant = 2
			}
			o, err := env.Dial(g, tenant, false)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(o.Query(check), " ")
			o.Close()
			if clean == "" {
				d, err := env.DialDirect(false)
				if err != nil {
					t.Fatal(err)
				}
				clean = strings.Join(d.Query(check), " ")
				d.Close()
			}
			if got != clean {
				if tenant == 2 {
					r.sameTenant++
				} else {
					r.crossings++
				}
			}
		}
	}
	want := seededValue(t, env)
	for _, tenant := range []int{1, 2} {
		s, err := env.Dial(g, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		s.Query("SELECT setseed(0.25)")
		s.Close()
		o, err := env.Dial(g, tenant, false)
		if err != nil {
			t.Fatal(err)
		}
		got := randomAfter(t, o)
		o.Close()
		if tenant == 1 {
			r.residueOther = got == want
		} else {
			r.residueSame = got == want
		}
	}
	final := stopAndRead(t, env, g, scenario)
	t.Logf("%s: crossings %d, same-tenant carry-over %d, seed residue other tenant %v same tenant %v; switches %d reseeds %d",
		scenario, r.crossings, r.sameTenant, r.residueOther, r.residueSame, poolMetric(final, "tenant_switches"), poolMetric(final, "reseeds"))
	return r
}

func TestTierRPoolSessionStateUnderEachReleasePolicy(t *testing.T) {
	env := tierR(t)
	for _, arm := range []struct{ reset, reseed string }{{"always", "off"}, {"on-switch", "off"}, {"always", "on"}, {"on-switch", "on"}} {
		r := policyRun(t, env, arm.reset, arm.reseed)
		if r.crossings != 0 {
			t.Errorf("%s/%s: %d session-state crossings between tenants", arm.reset, arm.reseed, r.crossings)
		}
		if (arm.reset == "on-switch") != (r.sameTenant > 0) {
			t.Errorf("%s/%s: same-tenant carry-over %d (present only under on-switch)", arm.reset, arm.reseed, r.sameTenant)
		}
		if (arm.reseed == "off") != r.residueOther {
			t.Errorf("%s/%s: random-seed residue across tenants %v (present only without the re-seed)", arm.reset, arm.reseed, r.residueOther)
		}
	}
}

func cancelThrough(g *Gateway, env *RealEnv, c *RealClient) error {
	conn, err := net.DialTimeout("tcp", g.Addr, realOpTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write(testclient.EncodeSSLRequest(nil)); err != nil {
		return err
	}
	var b [1]byte
	if _, err := conn.Read(b[:]); err != nil || b[0] != 'S' {
		return fmt.Errorf("TLS answer %q: %v", b[0], err)
	}
	tc := tls.Client(conn, env.ClientTLS())
	if err := tc.Handshake(); err != nil {
		return err
	}
	var k pgwire.BackendKey
	k.ProcessID = c.PID
	copy(k.Secret[:], c.Secret)
	_, err = tc.Write(pgwire.AppendCancelRequest(nil, k))
	_ = tc.Close()
	return err
}

func TestTierRPoolCancelReachesTheRightStatementOnly(t *testing.T) {
	env := tierR(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-pool-cancel", Conns: 2})
	a, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := env.Dial(g, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var bGot []string
	wg.Add(1)
	go func() { defer wg.Done(); bGot = b.Query("SELECT pg_sleep(1.5)") }()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	done := make(chan []string, 1)
	go func() { done <- a.Query("SELECT pg_sleep(10)") }()
	time.Sleep(300 * time.Millisecond)
	if err := cancelThrough(g, env, a); err != nil {
		t.Fatal(err)
	}
	aGot := <-done
	wg.Wait()
	if !HasToken(aGot, "E:ERROR:57014") || time.Since(start) > 5*time.Second {
		t.Fatalf("the cancelled client got %v after %s", aGot, time.Since(start))
	}
	if HasToken(bGot, "E:") {
		t.Fatalf("another client's statement was cancelled: %v", bGot)
	}
	a.Close()
	b.Close()
	final := stopAndRead(t, env, g, "tierr-pool-cancel")
	if ev(final, "client_cancels", obs.Labels{Outcome: "forwarded"}) != 1 || ev(final, "cancels_sent", obs.Labels{}) < 1 {
		t.Fatalf("cancels forwarded %d sent %d", ev(final, "client_cancels", obs.Labels{Outcome: "forwarded"}), ev(final, "cancels_sent", obs.Labels{}))
	}
}

func connectMany(env *RealEnv, n int, dial func() (*RealClient, error)) ([]*RealClient, int64) {
	var mu sync.Mutex
	var clients []*RealClient
	var refused atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := dial()
			if err != nil {
				refused.Add(1)
				return
			}
			mu.Lock()
			clients = append(clients, c)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return clients, refused.Load()
}

func queryAll(clients []*RealClient, sql string) int64 {
	var ok atomic.Int64
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := c.Query(sql); HasToken(got, "ZI") && !HasToken(got, "E:") {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	return ok.Load()
}

func TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy(t *testing.T) {
	env := tierR(t)
	base := backends(t, env)
	direct, refused := connectMany(env, headlineClients, func() (*RealClient, error) { return env.DialDirect(false) })
	directPeak := backends(t, env) - base
	directOK := queryAll(direct, "SELECT 1")
	for _, c := range direct {
		c.Close()
	}
	time.Sleep(500 * time.Millisecond)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-pool-headline", Conns: headlinePool, QueueCap: headlineClients,
		MaxClients: 2 * headlineClients, Timeouts: map[string]string{"queue_wait": "20s"}})
	proxied, prefused := connectMany(env, headlineClients, func() (*RealClient, error) { return env.Dial(g, 0, false) })
	proxyOK := queryAll(proxied, "SELECT pg_sleep(0.05)")
	proxyPeak := backends(t, env) - base
	for _, c := range proxied {
		c.Close()
	}
	final := stopAndRead(t, env, g, "tierr-pool-headline")
	t.Logf("HEADLINE 1: %d clients direct: %d connected, %d refused, %d database backends, %d queries ok; through the proxy: %d connected, %d refused, %d database backends (pool %d, peak open+connecting %d), %d queries ok",
		headlineClients, len(direct), refused, directPeak, directOK, len(proxied), prefused, proxyPeak, headlinePool,
		final.Value(obs.ComponentPool, "open_plus_connecting_peak", obs.Labels{}), proxyOK)
	if proxyPeak > headlinePool || final.Value(obs.ComponentPool, "open_plus_connecting_peak", obs.Labels{}) > headlinePool {
		t.Fatalf("the proxy opened %d database backends, more than its pool of %d", proxyPeak, headlinePool)
	}
	if int(proxyOK) != headlineClients || prefused != 0 {
		t.Fatalf("through the proxy %d of %d queries succeeded (%d refused)", proxyOK, headlineClients, prefused)
	}
	if directPeak <= headlinePool {
		t.Fatalf("direct connections opened only %d backends: the baseline did not load the database", directPeak)
	}
}

func TestTierRPoolReconnectionStormWithClientTLS(t *testing.T) {
	env := tierR(t)
	base := backends(t, env)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-pool-storm", Conns: headlinePool, QueueCap: headlineClients,
		MaxClients: 2 * headlineClients, MaxStartup: stormMaxStartup, Timeouts: map[string]string{"queue_wait": "20s"}})
	dial := func() (*RealClient, error) { return env.Dial(g, 1, false) }
	first, _ := connectMany(env, headlineClients, dial)
	queryAll(first, "SELECT 1")
	peak := backends(t, env) - base
	for _, c := range first {
		c.Abort()
	}
	var second []*RealClient
	var refused int64
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for attempt := 0; attempt < 5 && len(second) < headlineClients && ctx.Err() == nil; attempt++ {
		got, r := connectMany(env, headlineClients-len(second), dial)
		second = append(second, got...)
		refused += r
	}
	ok := queryAll(second, "SELECT 1")
	peak = max(peak, backends(t, env)-base)
	for _, c := range second {
		c.Close()
	}
	final := stopAndRead(t, env, g, "tierr-pool-storm")
	startupPeak := ev(final, "clients_in_startup_peak", obs.Labels{})
	t.Logf("tier R storm: %d clients dropped and reconnected over TLS; database backends peak %d (pool %d); clients in startup peak %d (cap %d); refused at the startup cap %d; queries ok %d",
		headlineClients, peak, headlinePool, startupPeak, stormMaxStartup, refused, ok)
	if peak > headlinePool || poolMetric(final, "open_plus_connecting_peak") > headlinePool {
		t.Fatalf("database backends peaked at %d, above the pool of %d", peak, headlinePool)
	}
	if startupPeak > stormMaxStartup {
		t.Fatalf("clients in startup peaked at %d above the cap %d", startupPeak, stormMaxStartup)
	}
	if int(ok) != len(second) || len(second) != headlineClients {
		t.Fatalf("after the storm %d of %d reconnected clients were served", ok, len(second))
	}
}
