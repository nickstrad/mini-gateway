package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/config/certtest"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

type fixedRandom struct{ n uint64 }

func (r *fixedRandom) Uint64() uint64 {
	r.n++
	return r.n * 0x9e3779b97f4a7c15
}

func exampleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("testdata", "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	certtest.WriteFile(t, filepath.Join(dir, "example.json"), b)
	ca := certtest.NewCA(t, "example CA")
	ca.Issue(t, "localhost", "127.0.0.1").WriteFiles(t, dir, "server.crt", "server.key")
	certtest.WriteFile(t, filepath.Join(dir, "ca.crt"), ca.PEM)
	return dir
}

func TestExampleConfigLoads(t *testing.T) {
	dir := exampleDir(t)
	f, err := config.Load(filepath.Join(dir, "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Listen != "127.0.0.1:6432" || f.AdminListen != "127.0.0.1:6433" || f.Instance != "gw-1" {
		t.Fatalf("addresses %q %q %q", f.Listen, f.AdminListen, f.Instance)
	}
	if len(f.Fingerprint()) != 16 {
		t.Fatalf("fingerprint %q, want 16 hex characters", f.Fingerprint())
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plant != 0 {
		t.Fatalf("Proxy() returned plant %d for the example config", cfg.Plant)
	}
	if cfg.Client.TLS != pgwire.ClientTLSRequire || cfg.Backend.TLS != pgwire.BackendTLSRequire {
		t.Fatalf("TLS modes client %d backend %d", cfg.Client.TLS, cfg.Backend.TLS)
	}
	if cfg.Client.Database != "app" || cfg.Backend.Database != "app" || cfg.Backend.User != "gateway" || cfg.Backend.Password != "gateway-secret" {
		t.Fatalf("database fields %+v / %+v", cfg.Client.Database, cfg.Backend)
	}
	if cfg.Client.Tenants.Len() != 2 || len(cfg.Client.MockSecret) != 32 || cfg.Client.Random == nil || cfg.Client.Keys == nil {
		t.Fatalf("client config %+v", cfg.Client)
	}
	if cfg.Connections != 4 || cfg.QueueCap != 256 || cfg.MaxClients != 1024 || cfg.MaxStartup != 128 || cfg.ReplyBufferCap != 262144 {
		t.Fatalf("caps %+v", cfg)
	}
	if cfg.Timeouts != proxy.DefaultTimeouts() || cfg.Reset != proxy.ResetAlways {
		t.Fatalf("timeouts %+v reset %v", cfg.Timeouts, cfg.Reset)
	}
	if _, err := proxy.New(cfg); err != nil {
		t.Fatalf("the engine refuses the example config: %v", err)
	}
	if c := f.ClientTLSConfig(); c == nil || len(c.Certificates) != 1 || c.MinVersion != 0x0303 {
		t.Fatalf("client TLS config %+v", c)
	}
	if c := f.BackendTLSConfig(); c == nil || !c.InsecureSkipVerify || c.MinVersion != 0x0303 {
		t.Fatalf("backend TLS config %+v", c)
	}
	if f.PublishInterval() != time.Second || !f.Obs.Detail {
		t.Fatalf("obs %+v", f.Obs)
	}
}

func TestExampleVerifiersMatchTheirPasswords(t *testing.T) {
	f, err := config.Load(filepath.Join(exampleDir(t), "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{"alice": {"alice-secret", "alice-salt-16byt"}, "bob": {"bob-secret", "bob-salt-16bytes"}}
	for _, tn := range f.Tenants {
		w := want[tn.User]
		v, err := config.DeriveVerifier(w[0], []byte(w[1]), pgwire.DefaultScramIterations)
		if err != nil {
			t.Fatal(err)
		}
		if v != tn.Verifier {
			t.Errorf("%s: verifier %q, derived %q", tn.User, tn.Verifier, v)
		}
	}
}

func TestDeriveVerifierRoundTrips(t *testing.T) {
	v, err := config.DeriveVerifier("pw", []byte("0123456789abcdef"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pgwire.ParseVerifier(v)
	if err != nil || p.Iterations != 4096 || string(p.Salt) != "0123456789abcdef" {
		t.Fatalf("parse %q: %+v %v", v, p, err)
	}
	for _, c := range []struct {
		pw    string
		salt  string
		iters int
	}{{"", "salt", 4096}, {"pw", "", 4096}, {"pw", "salt", 0}} {
		if _, err := config.DeriveVerifier(c.pw, []byte(c.salt), c.iters); err == nil {
			t.Errorf("DeriveVerifier(%q, %q, %d) accepted", c.pw, c.salt, c.iters)
		}
	}
}

const minimal = `{
  "database": {"address": "db.internal:5432", "name": "app", "user": "gateway", "password": "pw"},
  "tenants": [{"user": "alice", "verifier": "SCRAM-SHA-256$4096:YWxpY2Utc2FsdC0xNmJ5dA==$wFsWRjw4CdurXhNfUCJ5TYgWLpm7VaVe1BnlT3YMkHQ=:kigZ7dHs129bcEpWRXAE1OULr7tdMHWVvgKrM5fmcQU="}]
}`

func TestDefaults(t *testing.T) {
	f, err := config.Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if f.Listen != config.DefaultListen || f.AdminListen != config.DefaultAdminListen || f.Instance != config.DefaultInstance {
		t.Fatalf("addresses %q %q %q", f.Listen, f.AdminListen, f.Instance)
	}
	if f.ClientTLS.Mode != "off" || f.BackendTLS.Mode != "disable" || f.Lease.Reset != "always" {
		t.Fatalf("modes %q %q %q", f.ClientTLS.Mode, f.BackendTLS.Mode, f.Lease.Reset)
	}
	def := proxy.Config{}.WithDefaults()
	if f.Pool.Connections != def.Connections || f.Queue.Capacity != def.QueueCap || f.Limits.MaxClients != def.MaxClients ||
		f.Limits.MaxStartup != def.MaxStartup || f.Limits.ReplyBuffer != def.ReplyBufferCap {
		t.Fatalf("caps %+v %+v %+v, want the engine defaults", f.Pool, f.Queue, f.Limits)
	}
	if f.ProxyTimeouts() != proxy.DefaultTimeouts() {
		t.Fatalf("timeouts %+v", f.ProxyTimeouts())
	}
	if f.PublishInterval() != time.Second || f.Obs.Detail || f.Obs.CSV.Dir != "" {
		t.Fatalf("obs %+v", f.Obs)
	}
	if f.Fleet != (config.Fleet{Instances: 1, BackendConnections: def.Connections, ConnectRatePerS: 10, BackendMaxConnections: 100}) {
		t.Fatalf("fleet %+v", f.Fleet)
	}
	if f.ClientTLSConfig() != nil || f.BackendTLSConfig() != nil {
		t.Fatal("TLS configs for off/disable")
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Client.TLS != pgwire.ClientTLSOff || cfg.Backend.TLS != pgwire.BackendTLSDisable || cfg.Plant != 0 {
		t.Fatalf("config %+v", cfg)
	}
}

func TestFingerprint(t *testing.T) {
	fp := func(js string) string {
		t.Helper()
		f, err := config.Parse([]byte(js))
		if err != nil {
			t.Fatal(err)
		}
		return f.Fingerprint()
	}
	base := fp(minimal)
	if base != fp(minimal) {
		t.Fatal("fingerprint is not stable")
	}
	explicit := strings.Replace(minimal, `"database"`, `"pool": {"connections": 4}, "timeouts": {"client_startup": "5s"}, "database"`, 1)
	if fp(explicit) != base {
		t.Fatal("writing a default out changed the fingerprint of the effective config")
	}
	if fp(strings.Replace(minimal, `"password": "pw"`, `"password": "other"`, 1)) != base {
		t.Fatal("the password reached the fingerprint")
	}
	if fp(strings.Replace(minimal, `"database"`, `"pool": {"connections": 3}, "database"`, 1)) == base {
		t.Fatal("pool.connections did not change the fingerprint")
	}
}

func TestProxyNeedsAParsedFile(t *testing.T) {
	r := &fixedRandom{}
	if _, err := (&config.File{}).Proxy(r, pgwire.NewKeyIssuer(r)); !errors.Is(err, config.ErrNotValidated) {
		t.Fatalf("err %v", err)
	}
	f, err := config.Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Proxy(nil, nil); err == nil {
		t.Fatal("Proxy without randomness accepted")
	}
}

func TestPasswordFileIsReadRelativeToTheConfig(t *testing.T) {
	dir := t.TempDir()
	certtest.WriteFile(t, filepath.Join(dir, "pw.txt"), []byte("from-file\n"))
	js := strings.Replace(minimal, `"password": "pw"`, `"password_file": "pw.txt"`, 1)
	certtest.WriteFile(t, filepath.Join(dir, "c.json"), []byte(js))
	f, err := config.Load(filepath.Join(dir, "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil || cfg.Backend.Password != "from-file" {
		t.Fatalf("password %q err %v", cfg.Backend.Password, err)
	}
}

func TestVerifyFullUsesServerNameOrHost(t *testing.T) {
	dir := t.TempDir()
	ca := certtest.NewCA(t, "db CA")
	certtest.WriteFile(t, filepath.Join(dir, "ca.crt"), ca.PEM)
	for _, c := range []struct{ extra, want string }{{``, "db.internal"}, {`, "server_name": "pg.example"`, "pg.example"}} {
		js := strings.Replace(minimal, `"database"`, `"backend_tls": {"mode": "verify-full", "ca_file": "ca.crt"`+c.extra+`}, "database"`, 1)
		certtest.WriteFile(t, filepath.Join(dir, "c.json"), []byte(js))
		f, err := config.Load(filepath.Join(dir, "c.json"))
		if err != nil {
			t.Fatal(err)
		}
		tc := f.BackendTLSConfig()
		if tc == nil || tc.InsecureSkipVerify || tc.RootCAs == nil || tc.ServerName != c.want {
			t.Fatalf("backend TLS %+v, want server name %q", tc, c.want)
		}
	}
}

type tlsFiles struct {
	dir                              string
	cert, key, otherKey, garbage, ca string
}

func writeTLSFiles(t *testing.T) tlsFiles {
	dir := t.TempDir()
	ca := certtest.NewCA(t, "test CA")
	cert, key := ca.Issue(t, "localhost").WriteFiles(t, dir, "server.crt", "server.key")
	_, other := ca.Issue(t, "other").WriteFiles(t, dir, "other.crt", "other.key")
	garbage := filepath.Join(dir, "garbage.pem")
	certtest.WriteFile(t, garbage, []byte("not a pem file\n"))
	caPath := filepath.Join(dir, "ca.crt")
	certtest.WriteFile(t, caPath, ca.PEM)
	return tlsFiles{dir: dir, cert: cert, key: key, otherKey: other, garbage: garbage, ca: caPath}
}

func set(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	if v == nil {
		delete(m, parts[len(parts)-1])
		return
	}
	m[parts[len(parts)-1]] = v
}

const otherShapeIters = "SCRAM-SHA-256$8192:Ym9iLXNhbHQtMTZieXRlcw==$6LsavMiZUI+B727MKlgjdT/C6El3jPBup8NRgJxLzec=:V9non7svIjxX0rnRmMlvrZyEtUOZzxkPhfzjiMzQZZ8="
const otherShapeSalt = "SCRAM-SHA-256$4096:c2hvcnQ=$6LsavMiZUI+B727MKlgjdT/C6El3jPBup8NRgJxLzec=:V9non7svIjxX0rnRmMlvrZyEtUOZzxkPhfzjiMzQZZ8="
const sameShape = "SCRAM-SHA-256$4096:Ym9iLXNhbHQtMTZieXRlcw==$6LsavMiZUI+B727MKlgjdT/C6El3jPBup8NRgJxLzec=:V9non7svIjxX0rnRmMlvrZyEtUOZzxkPhfzjiMzQZZ8="

func TestValidationFailsClosedNamingTheKey(t *testing.T) {
	files := writeTLSFiles(t)
	alice := map[string]any{"user": "alice", "verifier": sameShape}
	cases := []struct {
		name string
		edit func(m map[string]any)
		raw  string
		key  string
	}{
		{name: "nesting startup above idle", edit: func(m map[string]any) { set(m, "timeouts.client_startup", "20m") }, key: "timeouts.client_startup"},
		{name: "nesting connect above queue wait", edit: func(m map[string]any) { set(m, "timeouts.database_connect", "6s") }, key: "timeouts.database_connect"},
		{name: "nesting statement above idle", edit: func(m map[string]any) { set(m, "timeouts.statement", "1h") }, key: "timeouts.statement"},
		{name: "zero duration", edit: func(m map[string]any) { set(m, "timeouts.reset", "0s") }, key: "timeouts.reset"},
		{name: "negative duration", edit: func(m map[string]any) { set(m, "timeouts.client_write", "-1s") }, key: "timeouts.client_write"},
		{name: "not a duration", edit: func(m map[string]any) { set(m, "timeouts.queue_wait", "five") }, key: "timeouts.queue_wait"},
		{name: "number for a duration", edit: func(m map[string]any) { set(m, "timeouts.redial_delay", 100) }, key: "timeouts.redial_delay"},
		{name: "timeout above a day", edit: func(m map[string]any) { set(m, "timeouts.client_idle", "48h") }, key: "timeouts.client_idle"},
		{name: "client tls without certificate", edit: func(m map[string]any) { set(m, "client_tls.mode", "require") }, key: "client_tls.cert_file"},
		{name: "client tls without key", edit: func(m map[string]any) {
			set(m, "client_tls.mode", "allow")
			set(m, "client_tls.cert_file", files.cert)
		}, key: "client_tls.key_file"},
		{name: "missing certificate file", edit: func(m map[string]any) {
			set(m, "client_tls.mode", "require")
			set(m, "client_tls.cert_file", filepath.Join(files.dir, "absent.crt"))
			set(m, "client_tls.key_file", files.key)
		}, key: "client_tls.cert_file"},
		{name: "certificate is not PEM", edit: func(m map[string]any) {
			set(m, "client_tls.mode", "require")
			set(m, "client_tls.cert_file", files.garbage)
			set(m, "client_tls.key_file", files.key)
		}, key: "client_tls.cert_file"},
		{name: "unreadable key", edit: func(m map[string]any) {
			set(m, "client_tls.mode", "require")
			set(m, "client_tls.cert_file", files.cert)
			set(m, "client_tls.key_file", files.garbage)
		}, key: "client_tls.key_file"},
		{name: "missing key file", edit: func(m map[string]any) {
			set(m, "client_tls.mode", "require")
			set(m, "client_tls.cert_file", files.cert)
			set(m, "client_tls.key_file", filepath.Join(files.dir, "absent.key"))
		}, key: "client_tls.key_file"},
		{name: "key of another certificate", edit: func(m map[string]any) {
			set(m, "client_tls.mode", "require")
			set(m, "client_tls.cert_file", files.cert)
			set(m, "client_tls.key_file", files.otherKey)
		}, key: "client_tls.key_file"},
		{name: "unknown client tls mode", edit: func(m map[string]any) { set(m, "client_tls.mode", "on") }, key: "client_tls.mode"},
		{name: "verify-full without ca", edit: func(m map[string]any) { set(m, "backend_tls.mode", "verify-full") }, key: "backend_tls.ca_file"},
		{name: "verify-full with missing ca", edit: func(m map[string]any) {
			set(m, "backend_tls.mode", "verify-full")
			set(m, "backend_tls.ca_file", filepath.Join(files.dir, "absent.crt"))
		}, key: "backend_tls.ca_file"},
		{name: "verify-full with a ca that is not PEM", edit: func(m map[string]any) {
			set(m, "backend_tls.mode", "verify-full")
			set(m, "backend_tls.ca_file", files.garbage)
		}, key: "backend_tls.ca_file"},
		{name: "unknown backend tls mode", edit: func(m map[string]any) { set(m, "backend_tls.mode", "prefer") }, key: "backend_tls.mode"},
		{name: "missing tenant table", edit: func(m map[string]any) { set(m, "tenants", nil) }, key: "tenants"},
		{name: "empty tenant table", edit: func(m map[string]any) { set(m, "tenants", []any{}) }, key: "tenants"},
		{name: "duplicate tenant", edit: func(m map[string]any) { set(m, "tenants", []any{alice, alice}) }, key: "tenants[1].user"},
		{name: "empty tenant user", edit: func(m map[string]any) {
			set(m, "tenants", []any{map[string]any{"user": "", "verifier": sameShape}})
		}, key: "tenants[0].user"},
		{name: "bad verifier", edit: func(m map[string]any) {
			set(m, "tenants", []any{map[string]any{"user": "x", "verifier": "md5abc"}})
		}, key: "tenants[0].verifier"},
		{name: "mixed iteration counts", edit: func(m map[string]any) {
			set(m, "tenants", []any{alice, map[string]any{"user": "bob", "verifier": otherShapeIters}})
		}, key: "tenants[1].verifier"},
		{name: "mixed salt lengths", edit: func(m map[string]any) {
			set(m, "tenants", []any{alice, map[string]any{"user": "bob", "verifier": otherShapeSalt}})
		}, key: "tenants[1].verifier"},
		{name: "unknown top-level key", edit: func(m map[string]any) { set(m, "listn", "x") }, key: "listn: unknown key"},
		{name: "unknown nested key", edit: func(m map[string]any) { set(m, "pool.conections", 4) }, key: "pool.conections: unknown key"},
		{name: "key in another case", edit: func(m map[string]any) { set(m, "Listen", "127.0.0.1:1") }, key: "Listen: unknown key"},
		{name: "unknown key in a tenant", edit: func(m map[string]any) {
			set(m, "tenants", []any{map[string]any{"user": "a", "verifier": sameShape, "password": "x"}})
		}, key: "tenants[0].password: unknown key"},
		{name: "key given twice", raw: `{"listen": "127.0.0.1:1", "listen": "127.0.0.1:2"}`, key: "listen: given twice"},
		{name: "data after the object", raw: minimal + ` {}`, key: "after the top-level object"},
		{name: "syntax error", raw: `{"listen": }`, key: "syntax error"},
		{name: "string for a number", edit: func(m map[string]any) { set(m, "pool.connections", "4") }, key: "pool.connections"},
		{name: "number for a string", edit: func(m map[string]any) { set(m, "listen", 6432) }, key: "listen"},
		{name: "bad lease reset", edit: func(m map[string]any) { set(m, "lease.reset", "never") }, key: "lease.reset"},
		{name: "negative publish interval", edit: func(m map[string]any) { set(m, "obs.publish_interval", "-1s") }, key: "obs.publish_interval"},
		{name: "negative csv cap", edit: func(m map[string]any) { set(m, "obs.csv.max_bytes", -1) }, key: "obs.csv.max_bytes"},
		{name: "negative pool", edit: func(m map[string]any) { set(m, "pool.connections", -1) }, key: "pool.connections"},
		{name: "queue above clients", edit: func(m map[string]any) {
			set(m, "queue.capacity", 20)
			set(m, "limits.max_clients", 10)
			set(m, "limits.max_startup", 5)
		}, key: "queue.capacity"},
		{name: "startup above clients", edit: func(m map[string]any) {
			set(m, "limits.max_clients", 300)
			set(m, "limits.max_startup", 400)
		}, key: "limits.max_startup"},
		{name: "tiny reply buffer", edit: func(m map[string]any) { set(m, "limits.reply_buffer", 10) }, key: "limits.reply_buffer"},
		{name: "negative fleet instances", edit: func(m map[string]any) { set(m, "fleet.instances", -1) }, key: "fleet.instances"},
		{name: "negative connect rate", edit: func(m map[string]any) { set(m, "fleet.connect_rate_per_s", -5) }, key: "fleet.connect_rate_per_s"},
		{name: "fleet remainder", edit: func(m map[string]any) {
			set(m, "fleet.instances", 3)
			set(m, "fleet.backend_connections", 13)
		}, key: "fleet.backend_connections"},
		{name: "fleet budget below one per instance", edit: func(m map[string]any) {
			set(m, "fleet.instances", 4)
			set(m, "fleet.backend_connections", 2)
		}, key: "fleet.backend_connections"},
		{name: "pool above the per-instance budget", edit: func(m map[string]any) {
			set(m, "pool.connections", 8)
			set(m, "fleet.backend_connections", 4)
		}, key: "pool.connections"},
		{name: "fleet above the database cap", edit: func(m map[string]any) {
			set(m, "fleet.backend_connections", 4)
			set(m, "fleet.backend_max_connections", 6)
		}, key: "fleet.backend_max_connections"},
		{name: "missing database address", edit: func(m map[string]any) { set(m, "database.address", nil) }, key: "database.address"},
		{name: "database address without port", edit: func(m map[string]any) { set(m, "database.address", "db") }, key: "database.address"},
		{name: "database address without host", edit: func(m map[string]any) { set(m, "database.address", ":5432") }, key: "database.address"},
		{name: "database port out of range", edit: func(m map[string]any) { set(m, "database.address", "db:70000") }, key: "database.address"},
		{name: "missing database name", edit: func(m map[string]any) { set(m, "database.name", nil) }, key: "database.name"},
		{name: "missing database user", edit: func(m map[string]any) { set(m, "database.user", "") }, key: "database.user"},
		{name: "password and password file", edit: func(m map[string]any) { set(m, "database.password_file", files.cert) }, key: "database.password"},
		{name: "no password", edit: func(m map[string]any) { set(m, "database.password", nil) }, key: "database.password"},
		{name: "missing password file", edit: func(m map[string]any) {
			set(m, "database.password", nil)
			set(m, "database.password_file", filepath.Join(files.dir, "absent"))
		}, key: "database.password_file"},
		{name: "bad listen", edit: func(m map[string]any) { set(m, "listen", "6432") }, key: "listen"},
		{name: "admin on the listen address", edit: func(m map[string]any) {
			set(m, "listen", "127.0.0.1:7000")
			set(m, "admin_listen", "127.0.0.1:7000")
		}, key: "admin_listen"},
		{name: "bad instance", edit: func(m map[string]any) { set(m, "instance", "a/b") }, key: "instance"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := []byte(c.raw)
			if c.edit != nil {
				var m map[string]any
				if err := json.Unmarshal([]byte(minimal), &m); err != nil {
					t.Fatal(err)
				}
				c.edit(m)
				var err error
				if b, err = json.Marshal(m); err != nil {
					t.Fatal(err)
				}
			}
			f, err := config.Parse(b)
			if err == nil {
				t.Fatalf("accepted:\n%s", b)
			}
			if f != nil {
				t.Fatal("a file was returned with the error")
			}
			if !errors.Is(err, config.ErrInvalid) && !errors.Is(err, proxy.ErrConfig) {
				t.Fatalf("error %v is neither config.ErrInvalid nor proxy.ErrConfig", err)
			}
			t.Log(err)
			if !strings.Contains(err.Error(), c.key) {
				t.Fatalf("error does not name %q:\n%v", c.key, err)
			}
		})
	}
}

func TestEmptyPasswordFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	certtest.WriteFile(t, filepath.Join(dir, "pw"), []byte("\n"))
	js := strings.Replace(minimal, `"password": "pw"`, `"password_file": "pw"`, 1)
	certtest.WriteFile(t, filepath.Join(dir, "c.json"), []byte(js))
	if _, err := config.Load(filepath.Join(dir, "c.json")); err == nil || !strings.Contains(err.Error(), "database.password_file") {
		t.Fatalf("err %v", err)
	}
}

func TestLoadRefusesAMissingOrHugeFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.Load(filepath.Join(dir, "absent.json")); !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
	huge := filepath.Join(dir, "huge.json")
	certtest.WriteFile(t, huge, []byte(strings.Repeat(" ", config.MaxConfigBytes+1)))
	if _, err := config.Load(huge); !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestNoConfigOrCommandFileMentionsThePlantSwitch(t *testing.T) {
	for _, root := range []string{".", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "Plant") {
				t.Errorf("%s mentions Plant: the planted-bug switch must stay unreachable from config", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
