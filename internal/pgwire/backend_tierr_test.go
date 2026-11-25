package pgwire_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

func tierR(t *testing.T) (plainAddr, scramAddr, password string) {
	t.Helper()
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: run scripts/tier-r.sh")
	}
	return os.Getenv("MG_PG_ADDR"), os.Getenv("MG_PG_SCRAM_ADDR"), os.Getenv("MG_PG_SCRAM_PASSWORD")
}

func tlsVersionOf(v uint16) pgwire.TLSVersion {
	switch v {
	case tls.VersionTLS13:
		return pgwire.TLSVersion13
	case tls.VersionTLS12:
		return pgwire.TLSVersion12
	}
	return pgwire.TLSVersionOther
}

func handshakeOutcome(err error) pgwire.HandshakeOutcome {
	var verr *tls.CertificateVerificationError
	if errors.As(err, &verr) {
		return pgwire.HandshakeFailedVerification
	}
	return pgwire.HandshakeFailed
}

func driveBackend(t *testing.T, addr string, cfg pgwire.BackendConfig, tc *tls.Config) (*pgwire.BackendSession, net.Conn) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s := pgwire.NewBackendSession(cfg)
	acts := s.Start(nil)
	buf := make([]byte, 4096)
	deadline := time.Now().Add(pgwire.DefaultConnectTimeout + 3*time.Second)
	for {
		for len(acts) > 0 {
			a := acts[0]
			acts = acts[1:]
			switch a.Kind {
			case pgwire.ActWrite:
				if _, err := conn.Write(a.Data); err != nil {
					acts = append(acts, s.PeerClosed(nil)...)
				}
			case pgwire.ActStartTLS:
				c := tls.Client(conn, tc)
				_ = c.SetDeadline(deadline)
				if err := c.Handshake(); err != nil {
					acts = append(acts, s.HandshakeDone(nil, pgwire.HandshakeResult{Outcome: handshakeOutcome(err)})...)
					continue
				}
				_ = c.SetDeadline(time.Time{})
				conn = c
				acts = append(acts, s.HandshakeDone(nil, pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: tlsVersionOf(c.ConnectionState().Version)})...)
			case pgwire.ActReady, pgwire.ActClose:
				return s, conn
			}
		}
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		if n > 0 {
			var used int
			acts, used = s.Receive(nil, buf[:n])
			if used != n && !s.Ready() && !s.Closed() {
				t.Fatalf("consumed %d of %d", used, n)
			}
			continue
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				acts = s.Deadline(nil)
				continue
			}
			acts = s.PeerClosed(nil)
		}
	}
}

func realBackendConfig(mode pgwire.BackendTLSMode, password string) pgwire.BackendConfig {
	return pgwire.BackendConfig{TLS: mode, User: "postgres", Database: "postgres", Password: password,
		SessionSettings: pgwire.DefaultSessionSettings(), Random: rand.New(rand.NewPCG(1, 2)), Stats: &pgwire.WireStats{}}
}

// TestTierRScramClientLogsInToPostgres proves BackendSession authenticates
// against real Postgres 17 (plaintext, TLS with -PLUS offered, wrong password,
// verify-full failure, TLS refused, and unknown database).
func TestTierRScramClientLogsInToPostgres(t *testing.T) {
	plain, scram, pw := tierR(t)
	insecure := &tls.Config{InsecureSkipVerify: true}
	s, conn := driveBackend(t, scram, realBackendConfig(pgwire.BackendTLSDisable, pw), nil)
	conn.Close()
	v, _ := pgwire.LookupParam(s.Params(), "server_version")
	if !s.Ready() || !strings.HasPrefix(v, "17.") || s.Key().ProcessID == 0 || len(s.Params()) != 14 {
		t.Fatalf("plaintext SCRAM login: ready=%v class=%v params=%v", s.Ready(), s.Class(), s.Params())
	}
	s, conn = driveBackend(t, scram, realBackendConfig(pgwire.BackendTLSRequire, pw), insecure)
	if !s.Ready() || !s.OverTLS() {
		t.Fatalf("SCRAM over TLS with -PLUS offered: class=%v code=%s", s.Class(), s.ServerCode())
	}
	conn.Close()
	s, conn = driveBackend(t, scram, realBackendConfig(pgwire.BackendTLSDisable, "wrong"), nil)
	conn.Close()
	if s.Class() != pgwire.ClassAuthFailed || s.ServerCode() != pgwire.CodeInvalidPassword {
		t.Fatalf("wrong password: class=%v code=%s", s.Class(), s.ServerCode())
	}
	s, conn = driveBackend(t, scram, realBackendConfig(pgwire.BackendTLSVerifyFull, pw), &tls.Config{ServerName: "localhost"})
	conn.Close()
	if s.Class() != pgwire.ClassTLSFailed {
		t.Fatalf("verify-full against a self-signed certificate: class=%v", s.Class())
	}
	s, conn = driveBackend(t, plain, realBackendConfig(pgwire.BackendTLSRequire, ""), insecure)
	conn.Close()
	if s.Class() != pgwire.ClassTLSRefused {
		t.Fatalf("TLS required against ssl=off: class=%v", s.Class())
	}
	cfg := realBackendConfig(pgwire.BackendTLSDisable, pw)
	cfg.Database = "no_such_database"
	s, conn = driveBackend(t, scram, cfg, nil)
	conn.Close()
	if s.Class() != pgwire.ClassServerFatal || s.ServerCode() != pgwire.CodeUnknownDatabase {
		t.Fatalf("unknown database: class=%v code=%s", s.Class(), s.ServerCode())
	}
	t.Log("tier R: SCRAM client: plaintext, TLS with -PLUS offered, wrong password, verify-full, TLS refused, unknown database as expected")
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(cryptorand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type testServer struct {
	t       *testing.T
	ln      net.Listener
	cfg     pgwire.ClientConfig
	cert    tls.Certificate
	backend string
	pw      string
	mu      sync.Mutex
	last    *pgwire.ClientSession
	wg      sync.WaitGroup
}

func (ts *testServer) serve() {
	defer ts.wg.Done()
	for {
		c, err := ts.ln.Accept()
		if err != nil {
			return
		}
		ts.handle(c)
	}
}

func (ts *testServer) handle(conn net.Conn) {
	defer conn.Close()
	s := pgwire.NewClientSession(ts.cfg)
	ts.mu.Lock()
	ts.last = s
	ts.mu.Unlock()
	buf := make([]byte, 4096)
	var acts []pgwire.Action
	_ = conn.SetDeadline(time.Now().Add(pgwire.DefaultClientStartupTimeout))
	for {
		for len(acts) > 0 {
			a := acts[0]
			acts = acts[1:]
			switch a.Kind {
			case pgwire.ActWrite:
				_, _ = conn.Write(a.Data)
			case pgwire.ActStartTLS:
				c := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{ts.cert}, MinVersion: tls.VersionTLS12})
				if err := c.Handshake(); err != nil {
					acts = append(acts, s.HandshakeDone(nil, pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailed})...)
					continue
				}
				conn = c
				acts = append(acts, s.HandshakeDone(nil, pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: tlsVersionOf(c.ConnectionState().Version)})...)
			case pgwire.ActReady:
				_ = conn.SetDeadline(time.Time{})
				ts.relay(conn)
				return
			}
		}
		if s.Closed() {
			return
		}
		n, err := conn.Read(buf)
		if n > 0 {
			acts, _ = s.Receive(nil, buf[:n])
			continue
		}
		if err != nil {
			acts = s.PeerClosed(nil)
		}
	}
}

func (ts *testServer) relay(client net.Conn) {
	bs, db := driveBackend(ts.t, ts.backend, realBackendConfig(pgwire.BackendTLSDisable, ts.pw), nil)
	if !bs.Ready() {
		return
	}
	defer db.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(db, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, db); done <- struct{}{} }()
	<-done
}

func startTestServer(t *testing.T, mode pgwire.ClientTLSMode, backend, pw string) *testServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	v, err := pgwire.NewVerifier("alice-pw", []byte("alice-salt-01234"), pgwire.DefaultScramIterations)
	if err != nil {
		t.Fatal(err)
	}
	table, _ := pgwire.NewTenantTable([]pgwire.Tenant{{User: "alice", Verifier: v}})
	rnd := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1))
	cfg := pgwire.ClientConfig{TLS: mode, Tenants: table, Database: "app", SessionSettings: pgwire.DefaultSessionSettings(),
		Random: rnd, Keys: pgwire.NewKeyIssuer(rnd), MockSecret: []byte("tier-r-mock"), Stats: &pgwire.WireStats{}}
	ts := &testServer{t: t, ln: ln, cfg: cfg, cert: selfSigned(t), backend: backend, pw: pw}
	ts.wg.Add(1)
	go ts.serve()
	t.Cleanup(func() { ln.Close(); ts.wg.Wait() })
	return ts
}

func psql(t *testing.T, ts *testServer, user, password, db, sslmode string) (string, string, error) {
	t.Helper()
	port := ts.ln.Addr().(*net.TCPAddr).Port
	conn := "host=127.0.0.1 port=" + itoa(port) + " user=" + user + " dbname=" + db + " sslmode=" + sslmode + " connect_timeout=10"
	cmd := exec.Command("psql", conn, "-tAc", "SELECT 41 + 1")
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password, "PGCLIENTENCODING=UTF8")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return strings.TrimSpace(out.String()), errb.String(), err
}

func itoa(n int) string { return big.NewInt(int64(n)).String() }

// TestTierRScramServerAcceptedByPsql proves stock psql (libpq) authenticates
// against ClientSession across all 9 TLS-mode, SCRAM, wrong-password,
// unknown-user, and unknown-database cases.
func TestTierRScramServerAcceptedByPsql(t *testing.T) {
	_, scram, pw := tierR(t)
	if _, err := exec.LookPath("psql"); err != nil {
		t.Fatal("psql is not installed")
	}
	cases := []struct {
		mode     pgwire.ClientTLSMode
		sslmode  string
		user     string
		password string
		db       string
		wantOut  string
		wantErr  string
		upgraded bool
	}{
		{pgwire.ClientTLSOff, "disable", "alice", "alice-pw", "app", "42", "", false},
		{pgwire.ClientTLSOff, "prefer", "alice", "alice-pw", "app", "42", "", false},
		{pgwire.ClientTLSAllow, "disable", "alice", "alice-pw", "app", "42", "", false},
		{pgwire.ClientTLSAllow, "require", "alice", "alice-pw", "app", "42", "", true},
		{pgwire.ClientTLSRequire, "prefer", "alice", "alice-pw", "app", "42", "", true},
		{pgwire.ClientTLSRequire, "disable", "alice", "alice-pw", "app", "", "TLS is required for this connection", false},
		{pgwire.ClientTLSOff, "disable", "alice", "wrong", "app", "", `password authentication failed for user "alice"`, false},
		{pgwire.ClientTLSOff, "disable", "mallory", "wrong", "app", "", `password authentication failed for user "mallory"`, false},
		{pgwire.ClientTLSOff, "disable", "alice", "alice-pw", "nope", "", `database "nope" does not exist`, false},
	}
	for _, tc := range cases {
		ts := startTestServer(t, tc.mode, scram, pw)
		out, errText, err := psql(t, ts, tc.user, tc.password, tc.db, tc.sslmode)
		if out != tc.wantOut || (tc.wantErr == "" && err != nil) || !strings.Contains(errText, tc.wantErr) {
			t.Fatalf("mode %d sslmode %s user %s: out %q err %v stderr %q", tc.mode, tc.sslmode, tc.user, out, err, errText)
		}
		ts.mu.Lock()
		last := ts.last
		ts.mu.Unlock()
		if last == nil || last.Upgraded() != tc.upgraded {
			t.Fatalf("mode %d sslmode %s: upgraded %v, want %v", tc.mode, tc.sslmode, last != nil && last.Upgraded(), tc.upgraded)
		}
	}
	t.Logf("tier R: psql accepted by the SCRAM server machine in %d cases", len(cases))
}
