package e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

// Credentials and database constants for the tier R Postgres 17 test cluster.
const (
	RealBackendUser = "gateway"
	RealBackendPass = "gateway-tier-r-pw"
	RealSuperPass   = "mg-c06-super-pw"
	RealDatabase    = proxysim.Database
	realIterations  = 4096
	gatewayStart    = 30 * time.Second
	gatewayStop     = 30 * time.Second
)

// RealEnv holds the addresses, certificates, binary path, and tenant verifiers
// shared across tier R subtests.
type RealEnv struct {
	PGAddr   string
	CertDir  string
	CAFile   string
	CertFile string
	KeyFile  string
	CA       *x509.CertPool
	Binary   string
	RunDir   string
	Verifier map[string]string
}

// ClientTLS returns a TLS 1.2+ client config trusting env's test CA for localhost.
func (env *RealEnv) ClientTLS() *tls.Config {
	return &tls.Config{RootCAs: env.CA, ServerName: "localhost", MinVersion: tls.VersionTLS12}
}

// Super opens a direct superuser connection to the tier R Postgres container.
func (env *RealEnv) Super() (*RealClient, error) {
	return DialReal(env.PGAddr, DialOptions{User: "postgres", Password: RealSuperPass, Database: RealDatabase})
}

// Exec executes one or more SQL statements directly as superuser, failing on any error.
func (env *RealEnv) Exec(sqls ...string) error {
	c, err := env.Super()
	if err != nil {
		return err
	}
	defer c.Close()
	for _, s := range sqls {
		if got := c.Query(s); HasToken(got, "E:") || HasToken(got, "closed:") {
			return fmt.Errorf("%s: %v", s, got)
		}
	}
	return nil
}

// GatewayOpts configures a mini-gateway subprocess launched by StartGateway.
type GatewayOpts struct {
	Scenario   string
	Conns      int
	QueueCap   int
	ReplyCap   int
	Timeouts   map[string]string
	ClientTLS  string
	BackendTLS string
	CAFile     string
	ServerName string
	DBAddr     string
	Tenants    int
	Queue      map[string]any
}

// Gateway represents a running mini-gateway process (or in-process shell for ablations).
type Gateway struct {
	Addr     string
	Admin    string
	RunDir   string
	cmd      *exec.Cmd
	stderr   *strings.Builder
	waitErr  chan error
	stopOnce sync.Once
	stopFn   func() error
}

var startLine = regexp.MustCompile(`listening on (\S+), admin on (\S+),`)

func (env *RealEnv) gatewayConfig(o GatewayOpts, dir string) map[string]any {
	if o.Conns == 0 {
		o.Conns = 2
	}
	if o.ClientTLS == "" {
		o.ClientTLS = "require"
	}
	if o.BackendTLS == "" {
		o.BackendTLS = "verify-full"
		o.CAFile = env.CAFile
	}
	if o.DBAddr == "" {
		o.DBAddr = env.PGAddr
	}
	if o.Tenants == 0 {
		o.Tenants = proxysim.DefaultTenants
	}
	var tenants []map[string]string
	for i := 0; i < o.Tenants; i++ {
		name := proxysim.TenantName(i)
		tenants = append(tenants, map[string]string{"user": name, "verifier": env.Verifier[name]})
	}
	timeouts := map[string]string{}
	for k, v := range o.Timeouts {
		timeouts[k] = v
	}
	cfg := map[string]any{
		"listen": "127.0.0.1:0", "admin_listen": "127.0.0.1:0", "instance": "gw-tier-r",
		"database":    map[string]any{"address": o.DBAddr, "name": RealDatabase, "user": RealBackendUser, "password": RealBackendPass},
		"client_tls":  map[string]any{"mode": o.ClientTLS, "cert_file": env.CertFile, "key_file": env.KeyFile},
		"backend_tls": map[string]any{"mode": o.BackendTLS, "ca_file": o.CAFile, "server_name": o.ServerName},
		"tenants":     tenants,
		"pool":        map[string]any{"connections": o.Conns},
		"timeouts":    timeouts,
		"lease":       map[string]any{"reset": "always"},
		"obs":         map[string]any{"publish_interval": "200ms", "detail": true, "csv": map[string]any{"dir": dir, "scenario": o.Scenario}},
		"fleet":       map[string]any{"instances": 1, "backend_connections": o.Conns, "connect_rate_per_s": 10, "backend_max_connections": 100},
	}
	if o.Queue == nil {
		o.Queue = RelaxedQueue
	}
	{
		q := map[string]any{}
		for k, v := range o.Queue {
			q[k] = v
		}
		if o.QueueCap > 0 {
			q["capacity"] = o.QueueCap
		}
		cfg["queue"] = q
	}
	if o.ReplyCap > 0 {
		cfg["limits"] = map[string]any{"reply_buffer": o.ReplyCap}
	}
	if o.ClientTLS == "off" {
		cfg["client_tls"] = map[string]any{"mode": "off"}
	}
	return cfg
}

// StartGateway writes a JSON config file, launches the compiled mini-gateway
// binary, and waits until it prints its bound client and admin addresses.
func (env *RealEnv) StartGateway(o GatewayOpts) (*Gateway, error) {
	dir := env.RunDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(env.gatewayConfig(o, dir), "", "  ")
	if err != nil {
		return nil, err
	}
	cfgDir, err := os.MkdirTemp("", "mg-gateway-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(cfgDir, "gateway.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	g := &Gateway{stderr: &strings.Builder{}, waitErr: make(chan error, 1)}
	g.cmd = exec.Command(env.Binary, "-config", path)
	g.cmd.Stderr = g.stderr
	out, err := g.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := g.cmd.Start(); err != nil {
		return nil, err
	}
	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			default:
			}
		}
	}()
	go func() { g.waitErr <- g.cmd.Wait() }()
	select {
	case l := <-lines:
		m := startLine.FindStringSubmatch(l)
		if m == nil {
			g.Stop()
			return nil, fmt.Errorf("unexpected start line %q", l)
		}
		g.Addr, g.Admin = m[1], m[2]
	case err := <-g.waitErr:
		return nil, fmt.Errorf("gateway exited: %v: %s", err, g.stderr.String())
	case <-time.After(gatewayStart):
		g.Stop()
		return nil, errors.New("gateway did not start")
	}
	return g, nil
}

// Stop sends SIGTERM (or stops the in-process shell) and waits for clean exit.
func (g *Gateway) Stop() error {
	var err error
	g.stopOnce.Do(func() {
		if g.stopFn != nil {
			err = g.stopFn()
			return
		}
		_ = g.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err = <-g.waitErr:
		case <-time.After(gatewayStop):
			_ = g.cmd.Process.Kill()
			err = errors.New("gateway did not stop on SIGTERM")
		}
		if err != nil && g.stderr.Len() > 0 {
			err = fmt.Errorf("%w: %s", err, g.stderr.String())
		}
	})
	return err
}

// Dial opens a TLS+SCRAM client connection for tenant through gateway g.
func (env *RealEnv) Dial(g *Gateway, tenant int, byteByByte bool) (*RealClient, error) {
	name := proxysim.TenantName(tenant)
	return DialReal(g.Addr, DialOptions{User: name, Password: proxysim.TenantPassword(name), Database: RealDatabase,
		TLS: env.ClientTLS(), ByteByByte: byteByByte})
}

// DialDirect opens a direct client connection to Postgres without the proxy.
func (env *RealEnv) DialDirect(byteByByte bool) (*RealClient, error) {
	return DialReal(env.PGAddr, DialOptions{User: RealBackendUser, Password: RealBackendPass, Database: RealDatabase,
		ByteByByte: byteByByte})
}

// ResetRows truncates kv and inserts the given SQL row statements.
func (env *RealEnv) ResetRows(rows []string) error {
	stmts := []string{"TRUNCATE kv"}
	return env.Exec(append(stmts, rows...)...)
}

// RowsSQL converts spec.Rows into INSERT statements for the tier R kv table.
func RowsSQL(spec Spec) []string {
	var out []string
	for _, r := range spec.Rows {
		out = append(out, "INSERT INTO kv VALUES ('"+r.Key+"', '"+r.Value+"')")
	}
	return out
}

// RunReal executes every client Script in spec concurrently over real sockets.
func RunReal(ctx context.Context, spec Spec, dial func(tenant int) (*RealClient, error), byteByByte bool) []ClientResult {
	res := make([]ClientResult, len(spec.Clients))
	var wg sync.WaitGroup
	start := time.Now()
	for i, sc := range spec.Clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sleepUntil(ctx, start.Add(sc.Start))
			runRealScript(ctx, sc, dial, &res[i])
		}()
	}
	wg.Wait()
	return res
}

func sleepUntil(ctx context.Context, t time.Time) {
	select {
	case <-ctx.Done():
	case <-time.After(time.Until(t)):
	}
}

func runRealScript(ctx context.Context, sc Script, dial func(int) (*RealClient, error), r *ClientResult) {
	c, err := dial(sc.Tenant)
	if err != nil {
		r.Startup = append(r.Startup, "closed:"+err.Error())
		r.Closed = true
		return
	}
	for _, st := range sc.Steps {
		sleepUntil(ctx, time.Now().Add(st.Delay))
		t0 := time.Now()
		var toks []string
		switch {
		case st.Vanish == VanishAfterSend:
			_ = c.write(c.sess.Query(nil, st.SQL))
			time.Sleep(vanishDelay)
			c.Abort()
			r.Steps = append(r.Steps, []string{"vanished"})
			r.Closed = true
			return
		case st.Raw != nil:
			toks = c.Send(st.Raw)
		default:
			toks = c.Query(st.SQL)
		}
		r.Steps = append(r.Steps, toks)
		r.Elapsed = append(r.Elapsed, time.Since(t0))
		if HasToken(toks, "closed:") {
			r.Closed = true
			return
		}
		if st.Vanish == VanishAfterReply {
			c.Abort()
			r.Closed = true
			return
		}
	}
	c.Close()
}

// FreePort binds and immediately releases an ephemeral loopback TCP port.
func FreePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

// TenantVerifiers derives SCRAM-SHA-256 verifier strings for tenants 0..n-1.
func TenantVerifiers(n int) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < n; i++ {
		name := proxysim.TenantName(i)
		salt := []byte(("salt-" + name + "-pad-0123456789")[:16])
		v, err := config.DeriveVerifier(proxysim.TenantPassword(name), salt, realIterations)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

// TierRPrefix identifies tier R SQL assertion directories under test/analysis/.
const TierRPrefix = "tierr-"

// TierRScenarios returns the ordered list of tier R CSV scenario names asserted by DuckDB.
func TierRScenarios() []string {
	return []string{"tierr-results", "tierr-fragment", "tierr-tx-integrity", "tierr-reject-list", "tierr-session-state",
		"tierr-client-vanish", "tierr-db-killed", "tierr-slow-reader", "tierr-queue-full", "tierr-silent-db", "tierr-tls",
		"tierr-backend-tls", "tierr-smoke", "tierr-admission-overload"}
}
