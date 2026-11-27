package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/mgctl"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

const (
	mgctlModelSteps   = 60
	mgctlModelKeys    = 5
	mgctlModelConns   = 2
	mgctlModelTenants = 3
	mgctlSettle       = 50 * time.Millisecond
	maxSimSteps       = 1 << 20
)

var errSimStuck = errors.New("e2e: the simulation ran out of events")

type simWorld struct {
	w      *proxysim.World
	col    *obs.Collector
	health obs.HealthTable
	json   *jsonstats.Adapter
	prom   *promtext.Adapter
}

func newSimWorld(t *testing.T, seed int64) *simWorld {
	t.Helper()
	rows := make([]fakepg.Row, mgctlModelKeys)
	for i := range rows {
		rows[i] = fakepg.Row{Key: "k" + strconv.Itoa(i+1), Value: "v" + strconv.Itoa(i+1)}
	}
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: seed, Scenario: "mgctl-model", Tenants: mgctlModelTenants, SCRAM: true,
		Fake: fakepg.Config{Rows: rows, MaxConnections: 64}, Engine: proxy.Config{Connections: mgctlModelConns, QueueCap: 16, MaxClients: 64, MaxStartup: 64}})
	if err != nil {
		t.Fatal(err)
	}
	sw := &simWorld{w: w, col: obs.NewCollector(obs.Config{Instance: "gw-sim", Build: "sim", ConfigFingerprint: "sim-fingerprint", Detail: true})}
	if err := enginesrc.Register(sw.col, w.Proxy.Engine, nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range append(enginesrc.HealthRules(), obs.CollectorRule()) {
		if err := sw.health.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	sw.json = jsonstats.New(nil)
	sw.prom = promtext.New(sw.col.Registry(), nil, promtext.Config{})
	for _, a := range []obs.Adapter{sw.json, sw.prom} {
		if err := sw.col.Attach(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.S.RunFor(mgctlSettle); err != nil {
		t.Fatal(err)
	}
	return sw
}

func (sw *simWorld) Get(path string) (int, []byte, error) {
	if err := sw.w.S.RunFor(mgctlSettle); err != nil {
		return 0, nil, err
	}
	if _, err := sw.col.Publish(); err != nil {
		return 0, nil, err
	}
	switch path {
	case mgctl.PathHealth:
		h := sw.col.Health(&sw.health)
		code := 200
		if h.State == obs.StateFailed {
			code = 503
		}
		return code, h.AppendJSON(nil), nil
	case mgctl.PathStats:
		return 200, bytes.Clone(sw.json.Last()), nil
	case mgctl.PathMetrics:
		return 200, bytes.Clone(sw.prom.Last()), nil
	}
	return 404, []byte("404 page not found\n"), nil
}

type simConn struct {
	sw   *simWorld
	end  *simnet.End
	up   bool
	gone bool
}

func (c *simConn) OnEvent(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		c.up = true
	case simnet.EvRefused, simnet.EvReset:
		c.gone = true
	}
}

func (c *simConn) step() error {
	for i := 0; i < maxSimSteps; i++ {
		ok, err := c.sw.w.S.Step()
		if err != nil {
			return err
		}
		if !ok {
			return errSimStuck
		}
		return nil
	}
	return errSimStuck
}

func (sw *simWorld) dial() (io.ReadWriteCloser, error) {
	c := &simConn{sw: sw}
	c.end = sw.w.Net.Dial(sw.w.Proxy.Addr(), sw.w.Cfg.Options, c)
	for !c.up {
		if c.gone {
			return nil, errors.New("e2e: connection refused")
		}
		if err := c.step(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *simConn) Write(b []byte) (int, error) {
	done := 0
	for done < len(b) {
		n, err := c.end.Write(b[done:])
		done += n
		if err != nil {
			return done, err
		}
		if done < len(b) {
			if err := c.step(); err != nil {
				return done, err
			}
		}
	}
	return done, nil
}

func (c *simConn) Read(b []byte) (int, error) {
	for {
		n, err := c.end.Read(b)
		if n > 0 {
			return n, nil
		}
		if err != nil && !errors.Is(err, simnet.ErrNotConnected) {
			return 0, err
		}
		if c.gone {
			return 0, io.EOF
		}
		if err := c.step(); err != nil {
			return 0, err
		}
	}
}

func (c *simConn) Close() error {
	err := c.end.Close()
	for i := 0; i < 64; i++ {
		if c.step() != nil {
			break
		}
	}
	return err
}

type mgctlModel struct {
	values    map[string]string
	completed int64
	rejected  int64
	received  int64
}

type mgctlStep struct {
	args []string
	code int
	want []string
	run  func(mgctl.Env) int
}

func (m *mgctlModel) next(r *rand.Rand) mgctlStep {
	key := "k" + strconv.Itoa(1+r.IntN(mgctlModelKeys+1))
	tenant := proxysim.TenantName(r.IntN(mgctlModelTenants))
	creds := mgctl.Credentials{User: tenant, Password: proxysim.TenantPassword(tenant), Database: proxysim.Database}
	q := func(sql string, code int, want ...string) mgctlStep {
		return mgctlStep{args: []string{"query", "-user", tenant, sql}, code: code, want: want,
			run: func(e mgctl.Env) int { return mgctl.Query(e, creds, sql) }}
	}
	switch r.IntN(11) {
	case 9:
		other := mgctl.Credentials{User: tenant, Password: creds.Password, Database: "nope"}
		return mgctlStep{args: []string{"query", "-user", tenant, "-database", "nope", "SELECT 1"}, code: mgctl.ExitAnswered,
			want: []string{"FATAL 3D000", "error-contract row: unknown-database"}, run: func(e mgctl.Env) int { return mgctl.Query(e, other, "SELECT 1") }}
	case 0:
		return mgctlStep{args: []string{"health"}, want: []string{"health: ok", "database", "database reachable"}, run: mgctl.Health}
	case 1:
		return mgctlStep{args: []string{"stats", "-match", "requests_"}, want: []string{
			"engine requests_completed - counter " + strconv.FormatInt(m.completed, 10) + "\n",
			"engine requests_rejected - counter " + strconv.FormatInt(m.rejected, 10) + "\n",
			"engine requests_received - counter " + strconv.FormatInt(m.received, 10) + "\n"},
			run: func(e mgctl.Env) int { return mgctl.Stats(e, mgctl.StatsOptions{Match: "requests_"}) }}
	case 2:
		return mgctlStep{args: []string{"metrics"}, want: []string{`mg_engine_requests_completed_total{component="engine"} ` + strconv.FormatInt(m.completed, 10) + "\n"}, run: mgctl.Metrics}
	case 3:
		return mgctlStep{args: []string{"tenants"}, want: []string{"no per-tenant numbers"}, run: func(e mgctl.Env) int { return mgctl.Tenants(e, "") }}
	case 4:
		return mgctlStep{args: []string{"config", "show"}, want: []string{"config fingerprint sim-fingerprint", "pool.connections " + strconv.Itoa(mgctlModelConns)},
			run: func(e mgctl.Env) int { return mgctl.ConfigShow(e, "") }}
	case 5:
		v, ok := m.values[key]
		m.received++
		m.completed++
		if !ok {
			return q("SELECT v FROM kv WHERE k = '"+key+"'", mgctl.ExitOK, "(0 rows)")
		}
		return q("SELECT v FROM kv WHERE k = '"+key+"'", mgctl.ExitOK, "v\n"+v+"\n(1 rows)\nSELECT 1\n")
	case 6:
		v := "w" + strconv.Itoa(r.IntN(1000))
		m.received++
		m.completed++
		if _, ok := m.values[key]; !ok {
			return q("UPDATE kv SET v = '"+v+"' WHERE k = '"+key+"'", mgctl.ExitOK, "UPDATE 0")
		}
		m.values[key] = v
		return q("UPDATE kv SET v = '"+v+"' WHERE k = '"+key+"'", mgctl.ExitOK, "UPDATE 1")
	case 7:
		m.received++
		m.rejected++
		return q("LISTEN jobs", mgctl.ExitAnswered, "ERROR 0A000: mini-gateway: feature not supported", "error-contract row: unsupported-feature", "reached the database: no")
	case 8:
		m.received++
		m.completed++
		return q("SELECT 1 / 0", mgctl.ExitAnswered, "ERROR 22012: division by zero", "error-contract row: pass-through")
	}
	bad := mgctl.Credentials{User: tenant, Password: "wrong", Database: proxysim.Database}
	return mgctlStep{args: []string{"query", "-user", tenant, "-password", "(wrong)", "SELECT 1"}, code: mgctl.ExitAnswered,
		want: []string{"FATAL 28P01", "error-contract row: auth-failed"}, run: func(e mgctl.Env) int { return mgctl.Query(e, bad, "SELECT 1") }}
}

func squeeze(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.Join(lines, "\n")
}

func runMgctlModel(t *testing.T, seed int64) string {
	t.Helper()
	sw := newSimWorld(t, seed)
	m := &mgctlModel{values: map[string]string{}}
	for i := 1; i <= mgctlModelKeys; i++ {
		m.values["k"+strconv.Itoa(i)] = "v" + strconv.Itoa(i)
	}
	r := rand.New(rand.NewPCG(uint64(seed), 0x6a))
	var transcript bytes.Buffer
	n := 0
	for i := 0; i < mgctlModelSteps; i++ {
		st := m.next(r)
		var out, errb bytes.Buffer
		nonce := "nonce-" + strconv.Itoa(n)
		n++
		e := mgctl.Env{Admin: sw, Dial: sw.dial, Stdout: &out, Stderr: &errb, Nonce: func() string { return nonce }}
		code := st.run(e)
		mgctl.Transcript(&transcript, st.args, out.String(), errb.String(), code)
		if code != st.code {
			t.Fatalf("seed %d step %d %v: exit %d, model %d\n%s%s\nREPLAY: go test -count=1 -run '^TestMgctlSeededModelAgainstTheSimulatedGateway$' ./test/e2e  (seed %d)",
				seed, i, st.args, code, st.code, out.String(), errb.String(), seed)
		}
		for _, w := range st.want {
			if !strings.Contains(squeeze(out.String()), w) {
				t.Fatalf("seed %d step %d %v: output lacks %q (the model's expectation):\n%s", seed, i, st.args, w, out.String())
			}
		}
	}
	if err := sw.w.Proxy.Finish(); err != nil {
		t.Fatalf("seed %d: the world did not quiesce after mgctl: %v", seed, err)
	}
	return transcript.String()
}

// TestMgctlSeededModelAgainstTheSimulatedGateway drives a seeded sequence of
// mgctl admin and query commands against the simulated gateway, checking every
// reply against an independent model and verifying replayable transcripts.
func TestMgctlSeededModelAgainstTheSimulatedGateway(t *testing.T) {
	seeds := []int64{1, 2, 3, 4}
	if testing.Short() {
		seeds = seeds[:2]
	}
	for _, seed := range seeds {
		first := runMgctlModel(t, seed)
		if again := runMgctlModel(t, seed); again != first {
			t.Fatalf("seed %d: two runs gave different transcripts", seed)
		}
		if seed == 1 {
			if path := os.Getenv("MGCTL_TRANSCRIPT"); path != "" {
				if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "mgctl-transcript.txt")
			if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range []string{"$ mgctl health", "$ mgctl query", "[exit 3]"} {
				if !strings.Contains(first, cmd) {
					t.Fatalf("transcript lacks %q", cmd)
				}
			}
		}
	}
}

// TestMgctlQueryMovesTheSimulatedGatewaysStats proves a single proxied query
// through mgctl increments engine.requests_completed in the simulated stats.
func TestMgctlQueryMovesTheSimulatedGatewaysStats(t *testing.T) {
	sw := newSimWorld(t, 9)
	var out bytes.Buffer
	e := mgctl.Env{Admin: sw, Dial: sw.dial, Stdout: &out, Stderr: io.Discard, Nonce: func() string { return "n" }}
	creds := mgctl.Credentials{User: proxysim.TenantName(0), Password: proxysim.TenantPassword(proxysim.TenantName(0)), Database: proxysim.Database}
	if mgctl.Query(e, creds, "SELECT v FROM kv WHERE k = 'k1'") != mgctl.ExitOK || !strings.Contains(out.String(), "v1") {
		t.Fatalf("query through the simulated gateway: %s", out.String())
	}
	out.Reset()
	mgctl.Stats(e, mgctl.StatsOptions{Match: "requests_completed"})
	if !strings.Contains(squeeze(out.String()), fmt.Sprintf("requests_completed - counter %d\n", 1)) {
		t.Fatalf("the stats did not move with the query: %s", out.String())
	}
}
