package mgctl_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/loadgen/loadgentest"
	"github.com/nickstrad/mini-gateway/internal/mgctl"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

type fakeAdmin struct {
	paths []string
	reply map[string][]byte
	codes map[string]int
	seq   int
	snaps []obs.InstanceSnapshot
}

func (a *fakeAdmin) Get(path string) (int, []byte, error) {
	a.paths = append(a.paths, path)
	if path == mgctl.PathStats && len(a.snaps) > 0 {
		s := a.snaps[min(a.seq, len(a.snaps)-1)]
		a.seq++
		b, err := jsonstats.Render(s)
		return 200, b, err
	}
	b, ok := a.reply[path]
	if !ok {
		return 0, nil, errors.New("connection refused")
	}
	code := 200
	if c, ok := a.codes[path]; ok {
		code = c
	}
	return code, b, nil
}

func env(a mgctl.Admin) (mgctl.Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return mgctl.Env{Admin: a, Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Pause: func() {},
		Salt: func(b []byte) {
			for i := range b {
				b[i] = byte(i + 1)
			}
		}, Nonce: func() string { return "fixed-client-nonce-0123" }}, &out, &errb
}

func snap(seq int64, completed int64) obs.InstanceSnapshot {
	s := obs.InstanceSnapshot{Instance: "gw-1", Build: "abc", ConfigFingerprint: "0123456789abcdef", Seq: seq,
		Points: []obs.Point{
			{Component: "engine", Metric: "database_capacity", Kind: obs.KindGauge, Value: 4},
			{Component: "engine", Metric: "queue_capacity", Kind: obs.KindGauge, Value: 256},
			{Component: "engine", Metric: "replies", Kind: obs.KindCounter, Labels: obs.Labels{Reason: "shed"}, Value: 2},
			{Component: "engine", Metric: "requests_completed", Kind: obs.KindCounter, Value: completed},
			{Component: "wire", Metric: "logins", Kind: obs.KindCounter, Value: 3},
		}}
	s.Sort()
	return s
}

func TestHealthPrintsEveryCheckAndExitsByState(t *testing.T) {
	for _, c := range []struct {
		state string
		code  int
		want  int
	}{{"ok", 200, mgctl.ExitOK}, {"degraded", 200, mgctl.ExitAnswered}, {"failed", 503, mgctl.ExitAnswered}} {
		h := obs.Health{State: map[string]obs.State{"ok": obs.StateOK, "degraded": obs.StateDegraded, "failed": obs.StateFailed}[c.state],
			Checks: []obs.CheckResult{{Name: "database", State: obs.StateOK, Reason: "a connection is usable"}, {Name: "listener", State: obs.StateFailed, Reason: "down"}}}
		a := &fakeAdmin{reply: map[string][]byte{mgctl.PathHealth: h.AppendJSON(nil)}, codes: map[string]int{mgctl.PathHealth: c.code}}
		e, out, _ := env(a)
		if got := mgctl.Health(e); got != c.want {
			t.Fatalf("%s: exit %d", c.state, got)
		}
		if !strings.HasPrefix(out.String(), "health: "+c.state+"\n") || !strings.Contains(out.String(), "database") || !strings.Contains(out.String(), "down") {
			t.Fatalf("%s: %q", c.state, out.String())
		}
	}
	e, _, errb := env(&fakeAdmin{})
	if mgctl.Health(e) != mgctl.ExitRuntime || !strings.Contains(errb.String(), "connection refused") {
		t.Fatal("unreachable admin")
	}
}

func TestStatsWatchPrintsEverySampleWithChanges(t *testing.T) {
	a := &fakeAdmin{snaps: []obs.InstanceSnapshot{snap(1, 10), snap(2, 15), snap(3, 15)}}
	e, out, _ := env(a)
	if mgctl.Stats(e, mgctl.StatsOptions{Watch: true, Count: 3, Match: "requests_completed"}) != mgctl.ExitOK {
		t.Fatal("stats")
	}
	s := out.String()
	if strings.Count(s, "instance gw-1") != 3 || !strings.Contains(s, "+5") || !strings.Contains(s, "seq 3") {
		t.Fatalf("watch output:\n%s", s)
	}
	if strings.Contains(s, "queue_capacity") {
		t.Fatal("filter ignored")
	}
	for _, p := range a.paths {
		if p != mgctl.PathStats {
			t.Fatalf("stats asked for %s", p)
		}
	}
}

func TestStatsFiltersByComponentAndSaysWhenNothingMatches(t *testing.T) {
	e, out, _ := env(&fakeAdmin{snaps: []obs.InstanceSnapshot{snap(1, 1)}})
	mgctl.Stats(e, mgctl.StatsOptions{Component: "wire"})
	if !strings.Contains(out.String(), "logins") || strings.Contains(out.String(), "engine") {
		t.Fatalf("%s", out.String())
	}
	e, out, _ = env(&fakeAdmin{snaps: []obs.InstanceSnapshot{snap(1, 1)}})
	mgctl.Stats(e, mgctl.StatsOptions{Match: "nothing-like-this"})
	if !strings.Contains(out.String(), "(no metric matches)") {
		t.Fatal(out.String())
	}
	if c := mgctl.Stats(e, mgctl.StatsOptions{Count: -1}); c != mgctl.ExitUsage {
		t.Fatal(c)
	}
}

func TestTenantsSaysWhenTheStatsCarryNoTenantAndListsTheConfiguredOnes(t *testing.T) {
	e, out, _ := env(&fakeAdmin{snaps: []obs.InstanceSnapshot{snap(1, 1)}})
	path := writeConfig(t, exampleConfig(t))
	if mgctl.Tenants(e, path) != mgctl.ExitOK {
		t.Fatal("tenants")
	}
	s := out.String()
	if !strings.Contains(s, "no per-tenant numbers") || !strings.Contains(s, "alice") || !strings.Contains(s, "bob") {
		t.Fatal(s)
	}
	ts := snap(1, 1)
	ts.Points = append(ts.Points, obs.Point{Component: "admission", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "alice", Outcome: "shed"}, Value: 9})
	ts.Sort()
	e, out, _ = env(&fakeAdmin{snaps: []obs.InstanceSnapshot{ts}})
	mgctl.Tenants(e, path)
	if !strings.Contains(out.String(), "alice") || !strings.Contains(out.String(), "outcome=shed") || !strings.Contains(out.String(), "has numbers") {
		t.Fatal(out.String())
	}
}

func exampleConfig(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "config", "testdata", "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["client_tls"] = map[string]any{"mode": "off"}
	return m
}

func writeConfig(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "gw.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigShowComparesTheFileWithTheRunningFingerprint(t *testing.T) {
	path := writeConfig(t, exampleConfig(t))
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := snap(1, 1)
	for _, fp := range []string{f.Fingerprint(), "ffffffffffffffff"} {
		s.ConfigFingerprint = fp
		e, out, _ := env(&fakeAdmin{snaps: []obs.InstanceSnapshot{s}})
		if mgctl.ConfigShow(e, path) != mgctl.ExitOK {
			t.Fatal("config show")
		}
		want := "matches the running instance: yes"
		if fp != f.Fingerprint() {
			want = "matches the running instance: no"
		}
		if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "pool.connections") {
			t.Fatalf("%s", out.String())
		}
		if strings.Contains(out.String(), "gateway-secret") {
			t.Fatal("config show printed the database password")
		}
	}
}

func TestConfigCheckAcceptsExactlyWhatTheGatewayValidationAccepts(t *testing.T) {
	base := exampleConfig(t)
	r := rand.New(rand.NewPCG(6, 6))
	accepted, refused := 0, 0
	for i := 0; i < 300; i++ {
		m := mutate(r, base)
		path := writeConfig(t, m)
		e, out, errb := env(nil)
		code := mgctl.ConfigCheck(e, path)
		f, err := config.Load(path)
		switch {
		case err == nil && code == mgctl.ExitOK:
			accepted++
			if out.String() != "config "+path+" is valid; fingerprint "+f.Fingerprint()+"\n" {
				t.Fatalf("output %q", out.String())
			}
		case err != nil && code == mgctl.ExitUsage:
			refused++
			if errb.String() != "mgctl: "+path+": "+err.Error()+"\n" {
				t.Fatalf("stderr %q, want the gateway's error %q", errb.String(), err)
			}
		default:
			t.Fatalf("case %d: mgctl exit %d, gateway validation %v", i, code, err)
		}
	}
	if accepted == 0 || refused == 0 {
		t.Fatalf("generated configs: %d accepted, %d refused; both must occur", accepted, refused)
	}
}

func mutate(r *rand.Rand, base map[string]any) map[string]any {
	b, _ := json.Marshal(base)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	edits := r.IntN(3)
	for k := 0; k < edits; k++ {
		section := []string{"pool", "queue", "limits", "timeouts", "lease", "fleet", "obs", "database", "backend_tls", "top"}[r.IntN(10)]
		val := []any{-1, 0, 1, 7, 100000000, "5s", "-5s", "bogus", "always", true, nil}[r.IntN(11)]
		target := m
		if section != "top" {
			sub, ok := m[section].(map[string]any)
			if !ok {
				continue
			}
			target = sub
		}
		keys := make([]string, 0, len(target))
		for key := range target {
			keys = append(keys, key)
		}
		if len(keys) == 0 {
			continue
		}
		slicesSort(keys)
		key := keys[r.IntN(len(keys))]
		switch r.IntN(4) {
		case 0:
			delete(target, key)
		case 1:
			target[key+"_unknown"] = val
		default:
			target[key] = val
		}
	}
	return m
}

func slicesSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestVerifierIsAcceptedByTheGatewaysSCRAMAndNeverPrintsThePassword(t *testing.T) {
	const password = "correct horse battery staple"
	e, out, errb := env(nil)
	if mgctl.Verifier(e, password, pgwire.DefaultScramIterations) != mgctl.ExitOK {
		t.Fatal(errb.String())
	}
	line := strings.TrimSpace(out.String())
	if strings.Contains(out.String()+errb.String(), password) {
		t.Fatal("the password was printed")
	}
	v, err := pgwire.ParseVerifier(line)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pgwire.NewTenantTable([]pgwire.Tenant{{User: "alice", Verifier: v}}); err != nil {
		t.Fatalf("the tenant table refuses the verifier: %v", err)
	}
	for _, c := range []struct {
		pw   string
		pass bool
	}{{password, true}, {password + "x", false}} {
		srv := pgwire.NewScramServer(v, "server-nonce-0123456789", false)
		cl := pgwire.NewScramClient("", c.pw, "client-nonce-0123456789", pgwire.GS2Header(false, false))
		first, cause := srv.First(pgwire.ScramMechanism, cl.First())
		if cause != pgwire.AuthCauseNone {
			t.Fatal(cause)
		}
		final, cause := cl.Final(first)
		if cause != pgwire.AuthCauseNone {
			t.Fatal(cause)
		}
		sig, cause := srv.Final(final)
		if (cause == pgwire.AuthCauseNone) != c.pass {
			t.Fatalf("password %q: server cause %v", c.pw, cause)
		}
		if c.pass && cl.Verify(sig) != pgwire.AuthCauseNone {
			t.Fatal("server signature")
		}
	}
	e, out, errb = env(nil)
	e.Stdin = strings.NewReader(password + "\nignored\n")
	if mgctl.Verifier(e, "-", 4096) != mgctl.ExitOK || strings.Contains(out.String()+errb.String(), password) {
		t.Fatal("stdin password")
	}
	e, out, errb = env(nil)
	if mgctl.Verifier(e, password, -1) != mgctl.ExitUsage || strings.Contains(out.String()+errb.String(), password) {
		t.Fatalf("a refused iteration count: %q %q", out.String(), errb.String())
	}
}

func TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage(t *testing.T) {
	for _, r := range pgwire.ErrorRows {
		if !r.HasMessage() {
			continue
		}
		m := mgctl.ContractRow(r.Code, r.Message)
		if !m.Found {
			t.Fatalf("row %s not found", r.ID)
		}
		if m.Row.ID != r.ID {
			same := m.Row.Message == r.Message || strings.HasPrefix(r.Message, m.Row.Message)
			if !same {
				t.Fatalf("row %s matched as %s", r.ID, m.Row.ID)
			}
		}
		line := mgctl.ContractLine(&mgctl.ServerError{Severity: r.Severity(), Code: r.Code, Message: r.Message})
		if !strings.Contains(line, "error-contract row: "+m.Row.ID+" ") {
			t.Fatalf("%s: %s", r.ID, line)
		}
	}
	if mgctl.ContractRow("23505", "duplicate key").Found || !strings.Contains(mgctl.ContractLine(&mgctl.ServerError{Code: "23505"}), "pass-through") {
		t.Fatal("a Postgres error must be pass-through")
	}
	if !mgctl.ProxyMade("53M01", "mini-gateway: request shed by the admission queue") || mgctl.ProxyMade("57014", "canceling statement due to user request") {
		t.Fatal("ProxyMade")
	}
}

func dialer(addr string) mgctl.Dialer {
	return func() (io.ReadWriteCloser, error) { return net.Dial("tcp", addr) }
}

func TestQueryPrintsRowsOrTheSQLStateWithItsContractRow(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{})
	cases := []struct {
		sql  string
		code int
		want []string
	}{
		{"SELECT v FROM kv WHERE k = 'k1'", mgctl.ExitOK, []string{"v", "x", "(1 rows)", "SELECT 1"}},
		{"UPDATE kv SET v = 'a' WHERE k = 'k1'", mgctl.ExitOK, []string{"UPDATE 1"}},
		{"LISTEN mg_load", mgctl.ExitAnswered, []string{"ERROR 0A000", "error-contract row: unsupported-feature", "reached the database: no"}},
		{"SELECT 1 / 0", mgctl.ExitAnswered, []string{"ERROR 22012: division by zero", "error-contract row: pass-through"}},
	}
	for _, c := range cases {
		e, out, errb := env(nil)
		e.Dial = dialer(f.Addr())
		got := mgctl.Query(e, mgctl.Credentials{User: "t0", Password: "secret-pw", Database: "app"}, c.sql)
		if got != c.code {
			t.Fatalf("%s: exit %d\n%s%s", c.sql, got, out.String(), errb.String())
		}
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Fatalf("%s: output lacks %q:\n%s", c.sql, w, out.String())
			}
		}
		if strings.Contains(out.String()+errb.String(), "secret-pw") {
			t.Fatal("the password was printed")
		}
	}
	e, _, errb := env(nil)
	e.Dial = dialer("127.0.0.1:1")
	if mgctl.Query(e, mgctl.Credentials{User: "t0"}, "SELECT 1") != mgctl.ExitRuntime || errb.Len() == 0 {
		t.Fatal("no server")
	}
}

func TestEveryAdminRequestIsARead(t *testing.T) {
	var _ interface {
		Get(string) (int, []byte, error)
	} = mgctl.Admin(nil)
	a := &fakeAdmin{snaps: []obs.InstanceSnapshot{snap(1, 1)}, reply: map[string][]byte{mgctl.PathHealth: obs.Health{}.AppendJSON(nil),
		mgctl.PathMetrics: []byte("mg_engine_requests_completed_total 1\n")}}
	e, out, _ := env(a)
	mgctl.Health(e)
	mgctl.Stats(e, mgctl.StatsOptions{})
	mgctl.Metrics(e)
	mgctl.Tenants(e, "")
	mgctl.ConfigShow(e, "")
	for _, p := range a.paths {
		if p != mgctl.PathHealth && p != mgctl.PathStats && p != mgctl.PathMetrics {
			t.Fatalf("asked for %s", p)
		}
	}
	if !strings.Contains(out.String(), "mg_engine_requests_completed_total 1") {
		t.Fatal("metrics not printed as served")
	}
}

func TestTranscriptIsReadable(t *testing.T) {
	var b bytes.Buffer
	mgctl.Transcript(&b, []string{"health"}, "health: ok\n  listener ok\n", "", 0)
	if b.String() != "$ mgctl health\n  health: ok\n    listener ok\n[exit 0]\n\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestTheProxysOwnFatalTextsAreNamedByTheirRow(t *testing.T) {
	cases := map[string][2]string{
		"tls-required":       {"28000", "TLS is required for this connection: SSL off"},
		"no-user":            {"28000", "no PostgreSQL user name specified in startup packet"},
		"unknown-database":   {"3D000", `database "nope" does not exist`},
		"auth-failed":        {"28P01", `password authentication failed for user "alice"`},
		"startup-parameter":  {"0A000", `unsupported startup parameter "search_path"`},
		"protocol-version":   {"0A000", "unsupported frontend protocol 4.0: server supports 3.0 to 3.0"},
		"protocol-violation": {"08P01", "canceling authentication due to timeout"},
	}
	for id, c := range cases {
		line := mgctl.ContractLine(&mgctl.ServerError{Severity: "FATAL", Code: c[0], Message: c[1]})
		if !strings.Contains(line, "error-contract row: "+id+" ") {
			t.Errorf("%s: %s", id, line)
		}
	}
	if line := mgctl.ContractLine(&mgctl.ServerError{Severity: "ERROR", Code: "28000", Message: "x"}); !strings.Contains(line, "pass-through") {
		t.Fatal(line)
	}
}
