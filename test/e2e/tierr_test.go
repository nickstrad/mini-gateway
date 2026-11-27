package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

var (
	realSetup *RealSetup
	realErr   error
)

// TestMain manages the shared Postgres 17 Docker container and compiled binary when MG_TIER_R=1.
func TestMain(m *testing.M) {
	code := 0
	if os.Getenv("MG_TIER_R") == "1" {
		prefix := os.Getenv("MG_TIER_R_PREFIX")
		if prefix == "" {
			prefix = fmt.Sprintf("mg-tier-r-%d", os.Getpid())
		}
		dir := os.Getenv("RUN_DIR")
		if dir == "" {
			dir, _ = os.MkdirTemp("", "mg-tier-r-runs-")
		}
		realSetup, realErr = StartRealEnv(prefix, dir)
		code = m.Run()
		if realSetup != nil {
			if err := realSetup.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "tier R cleanup:", err)
				code = 1
			}
		}
	} else {
		code = m.Run()
	}
	os.Exit(code)
}

func tierR(t *testing.T) *RealEnv {
	t.Helper()
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	if realErr != nil {
		t.Fatalf("tier R setup: %v", realErr)
	}
	return realSetup.Env
}

func gateway(t *testing.T, env *RealEnv, o GatewayOpts) *Gateway {
	t.Helper()
	g, err := env.StartGateway(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Stop() })
	return g
}

func stopAndRead(t *testing.T, env *RealEnv, g *Gateway, scenario string) obs.InstanceSnapshot {
	t.Helper()
	if err := g.Stop(); err != nil {
		t.Fatalf("gateway stop: %v", err)
	}
	runs, err := filepath.Glob(filepath.Join(env.RunDir, scenario, "*", "*"))
	if err != nil || len(runs) == 0 {
		t.Fatalf("no CSV run for %s: %v", scenario, err)
	}
	sort.Strings(runs)
	run := runs[len(runs)-1]
	r, err := csvflush.Read(run)
	if err != nil {
		t.Fatal(err)
	}
	seqs := r.FlushSeqs("gw-tier-r")
	if len(seqs) == 0 {
		t.Fatalf("%s: no flush", run)
	}
	final := r.Snapshot("gw-tier-r", seqs[len(seqs)-1])
	if err := csvflush.RoundTrip(run, final); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if err := realConservation(final); err != nil {
		t.Fatalf("%s: %v", run, err)
	}
	return final
}

func ev(s obs.InstanceSnapshot, metric string, l obs.Labels) int64 {
	return s.Value(obs.ComponentEngine, metric, l)
}

func realConservation(s obs.InstanceSnapshot) error {
	none := obs.Labels{}
	rec, acc, rej := ev(s, "requests_received", none), ev(s, "requests_accepted", none), ev(s, "requests_rejected", none)
	comp, fail, shed := ev(s, "requests_completed", none), ev(s, "requests_failed", none), ev(s, "requests_shed", none)
	if rec != acc+rej || acc != comp+fail+shed || ev(s, "requests_queued", none) != 0 || ev(s, "requests_in_flight", none) != 0 || rec == 0 {
		return fmt.Errorf("conservation from the file: received %d accepted %d rejected %d completed %d failed %d shed %d", rec, acc, rej, comp, fail, shed)
	}
	ended := ev(s, "leases_ended", none)
	if ended != ev(s, "leases_started", none) || ended != ev(s, "resets_completed", none)+s.Sum(obs.ComponentEngine, "resets_failed")+
		ev(s, "leases_ended_by", obs.Labels{Outcome: "close"})+ev(s, "leases_ended_by", obs.Labels{Outcome: "as-is"}) {
		return fmt.Errorf("lease accounting from the file: started %d ended %d", ev(s, "leases_started", none), ended)
	}
	return nil
}

func realSpec(spec Spec) Spec {
	sp := spec
	sp.Clients = slices.Clone(spec.Clients)
	return sp
}

func runBoth(t *testing.T, env *RealEnv, spec Spec, scenario string, byteByByte bool) ([]ClientResult, []ClientResult, obs.InstanceSnapshot) {
	t.Helper()
	ctx := context.Background()
	if err := env.ResetRows(RowsSQL(spec)); err != nil {
		t.Fatal(err)
	}
	direct := RunReal(ctx, spec, func(int) (*RealClient, error) { return env.DialDirect(false) }, false)
	if err := env.ResetRows(RowsSQL(spec)); err != nil {
		t.Fatal(err)
	}
	g := gateway(t, env, GatewayOpts{Scenario: scenario, Conns: spec.Conns})
	proxied := RunReal(ctx, spec, func(tenant int) (*RealClient, error) { return env.Dial(g, tenant, byteByByte) }, byteByByte)
	return direct, proxied, stopAndRead(t, env, g, scenario)
}

// TestTierRResultsThroughTheProxyEqualDirect proves real-binary proxy results match direct Postgres 17 results over TLS.
func TestTierRResultsThroughTheProxyEqualDirect(t *testing.T) {
	env := tierR(t)
	direct, proxied, final := runBoth(t, env, realSpec(ResultsSpec("tierr-results", false)), "tierr-results", false)
	if err := ResultsEqual(direct, proxied); err != nil {
		t.Fatal(err)
	}
	if ev(final, "resets_completed", obs.Labels{}) == 0 {
		t.Fatal("no reset completed")
	}
	t.Logf("tier R: results equal direct for %d clients", len(proxied))
}

// TestTierRFragmentationChangesNothing proves byte-by-byte client writes produce identical results through the real binary.
func TestTierRFragmentationChangesNothing(t *testing.T) {
	env := tierR(t)
	direct, proxied, _ := runBoth(t, env, realSpec(ResultsSpec("tierr-fragment", true)), "tierr-fragment", true)
	if err := ResultsEqual(direct, proxied); err != nil {
		t.Fatal(err)
	}
}

// TestTierRTransactionIntegrity proves interleaved real transactions over 2 connections never mix statements across clients.
func TestTierRTransactionIntegrity(t *testing.T) {
	env := tierR(t)
	const clients, rounds = 12, 4
	spec := Spec{Name: "tierr-tx-integrity", Conns: 2, Rows: clientRows(clients)}
	for c := 0; c < clients; c++ {
		var st []Step
		for r := 0; r < rounds; r++ {
			mark := func(at string) string {
				return fmt.Sprintf("INSERT INTO marks VALUES (%d, %d, txid_current(), '%s')", c, r, at)
			}
			st = append(st, Step{SQL: "BEGIN"}, Step{SQL: mark("start")}, Step{SQL: update(c, "a", "r"), Delay: 2 * time.Millisecond},
				Step{SQL: mark("end"), Delay: time.Millisecond}, Step{SQL: "COMMIT"})
		}
		spec.Clients = append(spec.Clients, Script{Tenant: c % 4, Steps: st})
	}
	if err := env.ResetRows(append(RowsSQL(spec), "TRUNCATE marks")); err != nil {
		t.Fatal(err)
	}
	g := gateway(t, env, GatewayOpts{Scenario: spec.Name, Conns: 2})
	res := RunReal(context.Background(), spec, func(tenant int) (*RealClient, error) { return env.Dial(g, tenant, false) }, false)
	for c, r := range res {
		if len(r.Steps) != rounds*5 || r.Closed {
			t.Fatalf("client %d: %s", c, joinSteps(r))
		}
	}
	super, err := env.Super()
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	for _, q := range []string{
		"SELECT count(*) FROM (SELECT client, round FROM marks GROUP BY client, round HAVING count(DISTINCT txid) <> 1 OR count(*) <> 2) x",
		"SELECT count(*) FROM (SELECT txid FROM marks GROUP BY txid HAVING count(DISTINCT client) > 1) x",
	} {
		if got := super.Query(q); !slices.Contains(got, "D:0") {
			t.Fatalf("%s: %v", q, got)
		}
	}
	if got := super.Query("SELECT count(*) FROM marks"); !slices.Contains(got, "D:"+itoa(clients*rounds*2)) {
		t.Fatalf("marks: %v", got)
	}
	stopAndRead(t, env, g, spec.Name)
}

func statementCalls(t *testing.T, env *RealEnv) int64 {
	t.Helper()
	c, err := env.Super()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := c.Query("SELECT coalesce(sum(calls), 0) FROM pg_stat_statements s JOIN pg_roles r ON r.oid = s.userid WHERE r.rolname = '" + RealBackendUser + "'")
	for _, tok := range got {
		if v, ok := strings.CutPrefix(tok, "D:"); ok {
			var n int64
			fmt.Sscan(v, &n)
			return n
		}
	}
	t.Fatalf("pg_stat_statements: %v", got)
	return 0
}

// TestTierRRejectListAndExtendedProtocol proves reject-list statements and extended protocol batches get 0A000 without reaching Postgres.
func TestTierRRejectListAndExtendedProtocol(t *testing.T) {
	env := tierR(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-reject-list", Conns: 1})
	c, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Query("SELECT 1"); !slices.Equal(got, okSelect1) {
		t.Fatalf("warm-up: %v", got)
	}
	before := statementCalls(t, env)
	refused := 0
	for _, r := range pgwire.RejectRules {
		if r.Example == "" || strings.HasPrefix(r.Example, "(") {
			continue
		}
		if got := c.Query(r.Example); !slices.Equal(got, rejected) {
			t.Fatalf("%q: %v", r.Example, got)
		}
		refused++
	}
	if got := c.Send(ExtendedMessages()); !slices.Equal(got, rejected) {
		t.Fatalf("extended protocol: %v", got)
	}
	if got := c.Query("SELECT 1"); !slices.Equal(got, okSelect1) {
		t.Fatalf("the connection is not usable after the refusals: %v", got)
	}
	c.Close()
	time.Sleep(200 * time.Millisecond)
	after := statementCalls(t, env)
	if d := after - before; d > 3 {
		t.Fatalf("the database counted %d statements of the gateway role for %d refusals and one SELECT", d, refused)
	}
	t.Logf("tier R: %d reject-list examples refused; database statement calls moved by %d (one SELECT and its reset)", refused, after-before)
	stopAndRead(t, env, g, "tierr-reject-list")
}

func dirtyCheck(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "oracles", "dirty-check.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var hostileCalls = []string{"SELECT public.hide_set()", "SELECT public.make_temp()", "SELECT public.take_lock()", "SELECT public.do_prepare()", "SELECT public.do_listen()"}

func sessionRun(t *testing.T, env *RealEnv, g *Gateway) int {
	t.Helper()
	check := dirtyCheck(t)
	checker, err := env.Dial(g, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	clean := checker.Query(check)
	if !HasToken(clean, "D:") {
		t.Fatalf("dirty-check: %v", clean)
	}
	dirty := 0
	for round := 0; round < 3; round++ {
		for i, call := range hostileCalls {
			h, err := env.Dial(g, 2+(i%2), false)
			if err != nil {
				t.Fatal(err)
			}
			if got := h.Query(call); HasToken(got, "E:") {
				dirty++
			}
			h.Close()
			if got := checker.Query(check); !slices.Equal(got, clean) {
				dirty++
			}
			if got := checker.Query("SHOW search_path"); !slices.Contains(got, `D:"$user", public`) {
				dirty++
			}
		}
	}
	return dirty
}

// TestTierRSessionStateNeverCrossesClients proves DISCARD ALL clears hidden function-driven SET, temp table, lock, PREPARE, and LISTEN state against dirty-check.sql.
func TestTierRSessionStateNeverCrossesClients(t *testing.T) {
	env := tierR(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-session-state", Conns: 1})
	if n := sessionRun(t, env, g); n != 0 {
		t.Fatalf("%d dirty observations through the proxy", n)
	}
	final := stopAndRead(t, env, g, "tierr-session-state")
	c, e := ev(final, "resets_completed", obs.Labels{}), ev(final, "leases_ended", obs.Labels{})
	cut := ev(final, "resets_failed", obs.Labels{Reason: "closed"})
	if c == 0 || c+cut != e || cut > 1 || final.Sum(obs.ComponentEngine, "resets_failed") != cut {
		t.Fatalf("resets completed %d (+%d cut by the stop), leases ended %d", c, cut, e)
	}
	t.Logf("tier R: 0 dirty observations over %d hostile leases", 3*len(hostileCalls))
}

// TestTierRSessionAblationWithoutResetCrosses proves disabling DISCARD ALL via PlantNoReset leaves dirty rows on real Postgres 17.
func TestTierRSessionAblationWithoutResetCrosses(t *testing.T) {
	env := tierR(t)
	g := startInProcess(t, env, GatewayOpts{Scenario: "tierr-session-ablation", Conns: 1}, true)
	if n := sessionRun(t, env, g); n == 0 {
		t.Fatal("no dirty observation with the reset switched off: the scenario cannot fail")
	} else {
		t.Logf("tier R ablation: %d dirty observations without the reset", n)
	}
}

// TestTierRClientVanishes proves an abruptly reset real client causes its database connection to be closed and never reused dirty.
func TestTierRClientVanishes(t *testing.T) {
	env := tierR(t)
	if err := env.ResetRows([]string{"INSERT INTO kv VALUES ('shared', 'clean'), ('x', 'clean')"}); err != nil {
		t.Fatal(err)
	}
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-client-vanish", Conns: 2})
	a, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"BEGIN", "UPDATE kv SET v = 'dirty' WHERE k = 'shared'", "SELECT hide_set()"} {
		if got := a.Query(q); HasToken(got, "E:") {
			t.Fatalf("%s: %v", q, got)
		}
	}
	a.Abort()
	b, err := env.Dial(g, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	b.Query("BEGIN")
	_ = b.write(b.sess.Query(nil, "UPDATE kv SET v = 'dirty' WHERE k = 'x' AND pg_sleep(2) IS NOT NULL"))
	time.Sleep(200 * time.Millisecond)
	b.Abort()
	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 4; i++ {
		c, err := env.Dial(g, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Query("SELECT v FROM kv WHERE k = 'shared'"); !slices.Contains(got, "D:clean") {
			t.Fatalf("a vanished client's transaction is visible: %v", got)
		}
		if got := c.Query("SELECT v FROM kv WHERE k = 'x'"); !slices.Contains(got, "D:clean") {
			t.Fatalf("a vanished client's statement is visible: %v", got)
		}
		if got := c.Query("SHOW search_path"); !slices.Contains(got, `D:"$user", public`) {
			t.Fatalf("a vanished client's session state is visible: %v", got)
		}
		c.Close()
	}
	final := stopAndRead(t, env, g, "tierr-client-vanish")
	if n := ev(final, "database_closes", obs.Labels{Reason: "client-vanished"}); n != 2 {
		t.Fatalf("%d connections closed for vanished clients, want 2", n)
	}
}

func backendPID(t *testing.T, env *RealEnv, like string) string {
	t.Helper()
	c, err := env.Super()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := 0; i < 100; i++ {
		got := c.Query("SELECT pid FROM pg_stat_activity WHERE usename = '" + RealBackendUser + "' AND " + like + " LIMIT 1")
		for _, tok := range got {
			if v, ok := strings.CutPrefix(tok, "D:"); ok {
				return v
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no backend matching %s", like)
	return ""
}

func kill(t *testing.T, env *RealEnv, pid string) {
	t.Helper()
	if err := env.Exec("SELECT pg_terminate_backend(" + pid + ")"); err != nil {
		t.Fatal(err)
	}
}

// TestTierRDatabaseKilled proves killing backend sessions mid-read, mid-write, mid-reply, and mid-transaction produces the documented error-contract rows.
func TestTierRDatabaseKilled(t *testing.T) {
	env := tierR(t)
	if err := env.ResetRows([]string{"INSERT INTO kv VALUES ('a', 'v')"}); err != nil {
		t.Fatal(err)
	}
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-db-killed", Conns: 1})
	c, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	expect := func(name string, got, want []string, start time.Time) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("%s: answered after %s", name, el)
		}
		if got := c.Query("SELECT 5"); !slices.Equal(got, selectN("5")) {
			t.Fatalf("%s: no recovery: %v", name, got)
		}
	}
	start := time.Now()
	go func() { kill(t, env, backendPID(t, env, "query LIKE 'UPDATE kv%pg_sleep%'")) }()
	expect("write killed", c.Query("UPDATE kv SET v = 'w' WHERE k = 'a' AND pg_sleep(3) IS NOT NULL"), errTokens("08007"), start)
	locker, err := env.Super()
	if err != nil {
		t.Fatal(err)
	}
	locker.Query("BEGIN")
	locker.Query("LOCK TABLE kv IN ACCESS EXCLUSIVE MODE")
	start = time.Now()
	go func() { kill(t, env, backendPID(t, env, "wait_event_type = 'Lock'")) }()
	got := c.Query("SELECT v FROM kv WHERE k = 'a'")
	locker.Query("ROLLBACK")
	locker.Close()
	expect("safe read killed", got, errTokens("08006"), start)
	start = time.Now()
	go func() { kill(t, env, backendPID(t, env, "query LIKE 'SELECT repeat%'")) }()
	got = c.Query("SELECT repeat('x', 100), pg_sleep(0.002) FROM generate_series(1, 3000)")
	if HasToken(got, "Z") || !HasToken(got, "closed:") || !HasToken(got, "T") {
		t.Fatalf("a reply cut mid-way must end in a closed connection without an end marker: %d tokens, last %v", len(got), got[max(0, len(got)-2):])
	}
	c, err = env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Query("BEGIN"); !slices.Equal(got, []string{"C:BEGIN", "ZT"}) {
		t.Fatalf("BEGIN: %v", got)
	}
	kill(t, env, backendPID(t, env, "state = 'idle in transaction'"))
	msg, err := c.Next()
	if err != nil || !slices.Equal(msg, []string{"E:FATAL:08006"}) {
		t.Fatalf("between statements: %v %v", msg, err)
	}
	if _, err := c.Next(); err == nil {
		t.Fatal("the client connection stayed open after FATAL 08006")
	}
	final := stopAndRead(t, env, g, "tierr-db-killed")
	for _, row := range []string{"lost-after-write", "lost-after-read", "partial-reply", "lost-in-transaction"} {
		if n := ev(final, "replies", obs.Labels{Reason: row}); n != 1 {
			t.Fatalf("row %s counted %d times", row, n)
		}
	}
}

// TestTierRSlowReader proves a stalled real reader keeps relay high-water within limits.reply_buffer and is dropped at timeouts.client_write.
func TestTierRSlowReader(t *testing.T) {
	env := tierR(t)
	const capacity = 65536
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-slow-reader", Conns: 2, ReplyCap: capacity,
		Timeouts: map[string]string{"client_write": "1s"}})
	slow, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := slow.Conn().(*tls.Conn); ok {
		if tcp, ok := tc.NetConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(4096)
		}
	}
	_ = slow.write(slow.sess.Query(nil, "SELECT repeat('x', 1000) FROM generate_series(1, 50000)"))
	time.Sleep(3 * time.Second)
	other, err := env.Dial(g, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := other.Query("SELECT 1"); !slices.Equal(got, okSelect1) {
		t.Fatalf("others are blocked: %v", got)
	}
	other.Close()
	slow.Abort()
	final := stopAndRead(t, env, g, "tierr-slow-reader")
	if hw := ev(final, "relay_buffer_high_water", obs.Labels{}); hw > capacity || hw == 0 {
		t.Fatalf("relay high-water %d, cap %d", hw, capacity)
	}
	if ev(final, "clients_dropped", obs.Labels{}) != 1 || ev(final, "relay_pauses", obs.Labels{}) == 0 {
		t.Fatalf("dropped %d pauses %d", ev(final, "clients_dropped", obs.Labels{}), ev(final, "relay_pauses", obs.Labels{}))
	}
}

// TestTierRQueueFull proves exceeding pool plus queue capacity returns 53M01 while keeping refused clients usable.
func TestTierRQueueFull(t *testing.T) {
	env := tierR(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-queue-full", Conns: 1, QueueCap: 2,
		Queue: map[string]any{"interval": "5s", "target": "1s"}})
	spec := Spec{}
	for c := 0; c < 5; c++ {
		spec.Clients = append(spec.Clients, Script{Tenant: c % 4, Steps: []Step{{SQL: "SELECT pg_sleep(0.5)"}, {SQL: "SELECT 9", Delay: 2 * time.Second}}})
	}
	var conns []*RealClient
	for c := 0; c < 5; c++ {
		rc, err := env.Dial(g, c%4, false)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, rc)
	}
	res := RunReal(context.Background(), spec, func(i int) (*RealClient, error) {
		rc := conns[0]
		conns = conns[1:]
		return rc, nil
	}, false)
	shed := 0
	for _, r := range res {
		if slices.Equal(r.Steps[0], errTokens("53M01")) {
			shed++
		}
		if !slices.Equal(r.Steps[1], selectN("9")) {
			t.Fatalf("a refused client's connection is not usable: %v", r.Steps[1])
		}
	}
	if shed != 2 {
		t.Fatalf("%d shed, want 2 (1 running, 2 queued)", shed)
	}
	final := stopAndRead(t, env, g, "tierr-queue-full")
	if ev(final, "replies", obs.Labels{Reason: "shed"}) != 2 {
		t.Fatalf("shed row %d", ev(final, "replies", obs.Labels{Reason: "shed"}))
	}
}

func silentListener(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	done := make(chan struct{})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				close(done)
				return
			}
			held = append(held, c)
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		<-done
		for _, c := range held {
			_ = c.Close()
		}
	})
	return l.Addr().String()
}

// TestTierRSilentDatabase proves silent connect and silent query peers trip timeouts.database_connect/queue_wait/statement and mark /healthz degraded.
func TestTierRSilentDatabase(t *testing.T) {
	env := tierR(t)
	silent := silentListener(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-silent-db", Conns: 1, BackendTLS: "disable", DBAddr: silent,
		Timeouts: map[string]string{"database_connect": "300ms", "queue_wait": "1s", "reset": "500ms"}})
	c, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if got := c.Query("SELECT 1"); !slices.Equal(got, errTokens("53M01")) {
		t.Fatalf("accept-then-silence at startup: %v", got)
	}
	if el := time.Since(start); el < time.Second || el > 3*time.Second {
		t.Fatalf("refused after %s, budget 1s", el)
	}
	health := adminGet(t, g, "/healthz")
	if !strings.Contains(health, "degraded") {
		t.Fatalf("health while the database is silent: %s", health)
	}
	c.Close()
	final := stopAndRead(t, env, g, "tierr-silent-db")
	if ev(final, "database_connect_failures", obs.Labels{Reason: "deadline"}) == 0 {
		t.Fatal("no connect deadline counted")
	}
	g2 := gateway(t, env, GatewayOpts{Scenario: "tierr-silent-db", Conns: 1, Timeouts: map[string]string{"statement": "1s"}})
	c2, err := env.Dial(g2, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	if got := c2.Query("SELECT pg_sleep(5)"); !slices.Equal(got, errTokens("08007")) {
		t.Fatalf("statement deadline: %v", got)
	}
	if el := time.Since(start); el < time.Second || el > 3*time.Second {
		t.Fatalf("statement deadline after %s, budget 1s", el)
	}
	if got := c2.Query("SELECT 1"); !slices.Equal(got, okSelect1) {
		t.Fatalf("no recovery: %v", got)
	}
	c2.Close()
	stopAndRead(t, env, g2, "tierr-silent-db")
}

func adminGet(t *testing.T, g *Gateway, path string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", g.Admin, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET %s HTTP/1.0\r\nHost: x\r\n\r\n", path)
	b, _ := io.ReadAll(conn)
	return string(b)
}

func psql(env *RealEnv, g *Gateway, sslmode string, sql string) (string, error) {
	host, port, _ := net.SplitHostPort(g.Addr)
	cmd := exec.Command("psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql)
	cmd.Env = append(os.Environ(), "PGHOST=localhost", "PGHOSTADDR="+host, "PGPORT="+port, "PGUSER=tenant0",
		"PGPASSWORD=pw-tenant0", "PGDATABASE="+RealDatabase, "PGSSLMODE="+sslmode, "PGSSLROOTCERT="+env.CAFile,
		"PGCONNECT_TIMEOUT=10")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestTierRTLS proves real psql require/verify-full, plaintext refusal (28000), smuggled bytes, handshake stall, and post-TLS CancelRequest recognition.
func TestTierRTLS(t *testing.T) {
	env := tierR(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-tls", Conns: 1, Timeouts: map[string]string{"client_startup": "1s"}})
	for _, mode := range []string{"require", "verify-full"} {
		out, err := psql(env, g, mode, "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()")
		if err != nil || strings.TrimSpace(out) != "t" {
			t.Fatalf("psql sslmode=%s: %v %q", mode, err, out)
		}
	}
	out, err := psql(env, g, "disable", "SELECT 1")
	if err == nil || !strings.Contains(out, "TLS is required") {
		t.Fatalf("sslmode=disable under require: %v %q", err, out)
	}
	raw, err := net.Dial("tcp", g.Addr)
	if err != nil {
		t.Fatal(err)
	}
	smuggle := append(testclient.EncodeSSLRequest(nil), testclient.EncodeStartup(nil, []testclient.Param{{Name: "user", Value: "tenant0"}})...)
	_, _ = raw.Write(smuggle)
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	b, _ := io.ReadAll(raw)
	raw.Close()
	if len(b) > 1 && b[0] == 'S' && !strings.Contains(string(b[1:]), "\x15") {
		t.Fatalf("smuggled bytes: the server answered %q", b)
	}
	stall, err := net.Dial("tcp", g.Addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stall.Write(testclient.EncodeSSLRequest(nil))
	start := time.Now()
	_ = stall.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = io.ReadAll(stall)
	stall.Close()
	if el := time.Since(start); el < 900*time.Millisecond || el > 4*time.Second {
		t.Fatalf("a client stalled mid-handshake was closed after %s, budget 1s", el)
	}
	cancel, err := net.Dial("tcp", g.Addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = cancel.Write(testclient.EncodeSSLRequest(nil))
	var answer [1]byte
	_, _ = io.ReadFull(cancel, answer[:])
	tc := tls.Client(cancel, env.ClientTLS())
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	_, _ = tc.Write(testclient.EncodeCancel(nil, 1, []byte{1, 2, 3, 4}))
	_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.ReadAll(tc)
	tc.Close()
	final := stopAndRead(t, env, g, "tierr-tls")
	if ev(final, "cancel_requests", obs.Labels{}) != 1 {
		t.Fatalf("cancel after the TLS upgrade not recognised: %d", ev(final, "cancel_requests", obs.Labels{}))
	}
	if final.Value(obs.ComponentWire, "client_plaintext_refused", obs.Labels{}) == 0 {
		t.Fatal("plaintext refusal not counted")
	}
}

// TestTierRDatabaseLegTLSModes proves disable, require, verify-full, and wrong-CA backend TLS modes against Postgres 17 pg_stat_ssl.
func TestTierRDatabaseLegTLSModes(t *testing.T) {
	env := tierR(t)
	for _, m := range []struct{ mode, ca, want string }{{"disable", "", "f"}, {"require", "", "t"}, {"verify-full", env.CAFile, "t"}} {
		g := gateway(t, env, GatewayOpts{Scenario: "tierr-backend-tls", Conns: 1, BackendTLS: m.mode, CAFile: m.ca, ServerName: "localhost"})
		c, err := env.Dial(g, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		got := c.Query("SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()")
		if !slices.Contains(got, "D:"+m.want) {
			t.Fatalf("backend_tls.mode %s: pg_stat_ssl %v", m.mode, got)
		}
		c.Close()
		stopAndRead(t, env, g, "tierr-backend-tls")
	}
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-backend-tls", Conns: 1, BackendTLS: "verify-full", CAFile: realSetup.WrongCA,
		ServerName: "localhost", Timeouts: map[string]string{"queue_wait": "1s", "database_connect": "500ms", "reset": "500ms"}})
	c, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Query("SELECT 1"); !slices.Equal(got, errTokens("53M01")) {
		t.Fatalf("wrong CA: %v", got)
	}
	c.Close()
	final := stopAndRead(t, env, g, "tierr-backend-tls")
	if final.Value(obs.ComponentWire, "database_tls_failed", obs.Labels{Reason: "failed-verification"}) == 0 {
		t.Fatal("a wrong CA was not a verification failure")
	}
	if ev(final, "database_connect_failures", obs.Labels{Reason: "tls-failed"}) == 0 {
		t.Fatal("a wrong CA was not counted as a connect failure of its class")
	}
}

// TestTierRSmokePsqlAndPgbench proves stock psql and pgbench -S -M simple succeed through the real binary over TLS.
func TestTierRSmokePsqlAndPgbench(t *testing.T) {
	env := tierR(t)
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-smoke", Conns: 2})
	if out, err := psql(env, g, "verify-full", "SELECT 41 + 1"); err != nil || strings.TrimSpace(out) != "42" {
		t.Fatalf("psql: %v %q", err, out)
	}
	host, port, _ := net.SplitHostPort(env.PGAddr)
	init := exec.Command("pgbench", "-i", "-s", "1", "-q")
	init.Env = append(os.Environ(), "PGHOST="+host, "PGPORT="+port, "PGUSER=postgres", "PGPASSWORD="+RealSuperPass, "PGDATABASE="+RealDatabase)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("pgbench -i: %v %s", err, out)
	}
	if err := env.Exec("GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + RealBackendUser); err != nil {
		t.Fatal(err)
	}
	gh, gp, _ := net.SplitHostPort(g.Addr)
	bench := exec.Command("pgbench", "-S", "-M", "simple", "-n", "-c", "4", "-t", "200")
	bench.Env = append(os.Environ(), "PGHOST=localhost", "PGHOSTADDR="+gh, "PGPORT="+gp, "PGUSER=tenant0", "PGPASSWORD=pw-tenant0",
		"PGDATABASE="+RealDatabase, "PGSSLMODE=verify-full", "PGSSLROOTCERT="+env.CAFile)
	out, err := bench.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "number of transactions actually processed: 800/800") ||
		!strings.Contains(string(out), "number of failed transactions: 0") {
		t.Fatalf("pgbench -S -M simple: %v\n%s", err, out)
	}
	t.Logf("tier R: pgbench -S -M simple through the binary over TLS: 800/800")
	final := stopAndRead(t, env, g, "tierr-smoke")
	if ev(final, "requests_completed", obs.Labels{}) < 800 {
		t.Fatalf("completed %d", ev(final, "requests_completed", obs.Labels{}))
	}
}
