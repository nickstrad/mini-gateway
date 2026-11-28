package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	pgImage        = "postgres:17"
	readyAttempts  = 120
	readyPause     = 500 * time.Millisecond
	certValidity   = 24 * time.Hour
	pgMaxConns     = "200"
	containerLabel = "c06"
	hostileRole    = "mg_hostile_role"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(name string) (*testCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(certValidity), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

func (ca *testCA) issue(names ...string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano() + 1), Subject: pkix.Name{CommonName: names[0]},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(certValidity), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, err
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), nil
}

// RealSetup owns a throwaway Postgres 17 Docker container (ssl=on, SCRAM),
// test CA certificates, and the compiled mini-gateway binary for tier R tests.
type RealSetup struct {
	Env       *RealEnv
	Container string
	WrongCA   string
	dir       string
}

func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	return filepath.Dir(strings.TrimSpace(string(out))), nil
}

func writeAll(files map[string][]byte) error {
	for p, b := range files {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// StartRealEnv generates ephemeral test certificates, builds cmd/mini-gateway,
// starts a Postgres 17 container named "<prefix>-c06", and installs the test schema.
func StartRealEnv(prefix, runDir string) (*RealSetup, error) {
	dir, err := os.MkdirTemp("", "mg-tier-r-c06-")
	if err != nil {
		return nil, err
	}
	s := &RealSetup{dir: dir, Container: prefix + "-" + containerLabel}
	ca, err := newTestCA("mini-gateway tier R test CA")
	if err != nil {
		return s, err
	}
	wrong, err := newTestCA("mini-gateway wrong CA")
	if err != nil {
		return s, err
	}
	cert, key, err := ca.issue("localhost", "127.0.0.1")
	if err != nil {
		return s, err
	}
	env := &RealEnv{CertDir: dir, CAFile: filepath.Join(dir, "ca.crt"), CertFile: filepath.Join(dir, "server.crt"),
		KeyFile: filepath.Join(dir, "server.key"), CA: x509.NewCertPool(), RunDir: runDir}
	env.CA.AddCert(ca.cert)
	s.WrongCA = filepath.Join(dir, "wrong-ca.crt")
	if err := writeAll(map[string][]byte{env.CAFile: ca.pem, env.CertFile: cert, env.KeyFile: key, s.WrongCA: wrong.pem}); err != nil {
		return s, err
	}
	if err := os.Chmod(env.KeyFile, 0o600); err != nil {
		return s, err
	}
	s.Env = env
	if env.Verifier, err = TenantVerifiers(8); err != nil {
		return s, err
	}
	root, err := moduleRoot()
	if err != nil {
		return s, err
	}
	env.Binary = filepath.Join(dir, "mini-gateway")
	build := exec.Command("go", "build", "-o", env.Binary, "./cmd/mini-gateway")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return s, fmt.Errorf("build: %v: %s", err, out)
	}
	addr, err := FreePort()
	if err != nil {
		return s, err
	}
	env.PGAddr = addr
	port := addr[strings.LastIndex(addr, ":")+1:]
	script := "cp /certs/server.crt /certs/server.key /tmp/ && chown postgres /tmp/server.crt /tmp/server.key && chmod 600 /tmp/server.key && " +
		"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key " +
		"-c shared_preload_libraries=pg_stat_statements -c max_connections=" + pgMaxConns + " -c shared_buffers=32MB"
	run := exec.Command("docker", "run", "-d", "--rm", "--name", s.Container, "-p", "127.0.0.1:"+port+":5432",
		"-e", "POSTGRES_PASSWORD="+RealSuperPass, "-e", "POSTGRES_HOST_AUTH_METHOD=scram-sha-256",
		"-e", "POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256", "-e", "POSTGRES_DB="+RealDatabase,
		"-v", dir+":/certs:ro", "--entrypoint", "bash", pgImage, "-c", script)
	if out, err := run.CombinedOutput(); err != nil {
		return s, fmt.Errorf("docker run: %v: %s", err, out)
	}
	if err := s.waitReady(); err != nil {
		return s, err
	}
	return s, s.Env.Exec(setupSQL()...)
}

func (s *RealSetup) waitReady() error {
	var last error
	for i := 0; i < readyAttempts; i++ {
		c, err := s.Env.Super()
		if err == nil {
			got := c.Query("SELECT 1")
			c.Close()
			if HasToken(got, "D:1") {
				return nil
			}
		}
		last = err
		time.Sleep(readyPause)
	}
	logs, _ := exec.Command("docker", "logs", "--tail", "20", s.Container).CombinedOutput()
	return fmt.Errorf("postgres did not become ready: %v\n%s", last, logs)
}

// Close removes the throwaway Docker container and deletes the temp directory.
func (s *RealSetup) Close() error {
	var errs []error
	if s.Container != "" {
		if out, err := exec.Command("docker", "rm", "-f", "-v", s.Container).CombinedOutput(); err != nil && !strings.Contains(string(out), "No such container") {
			errs = append(errs, fmt.Errorf("docker rm: %v: %s", err, out))
		}
	}
	errs = append(errs, os.RemoveAll(s.dir))
	return errors.Join(errs...)
}

func setupSQL() []string {
	return []string{
		"CREATE EXTENSION IF NOT EXISTS pg_stat_statements",
		"CREATE ROLE " + RealBackendUser + " LOGIN PASSWORD '" + RealBackendPass + "'",
		"CREATE TABLE kv (k text PRIMARY KEY, v text NOT NULL)",
		"CREATE TABLE marks (client int NOT NULL, round int NOT NULL, txid bigint NOT NULL, at text NOT NULL)",
		"GRANT ALL ON kv, marks TO " + RealBackendUser,
		"GRANT pg_read_all_stats TO " + RealBackendUser,
		`CREATE FUNCTION hide_set() RETURNS text LANGUAGE plpgsql AS $$ BEGIN PERFORM set_config('search_path', 'hostile', false); RETURN 'set'; END $$`,
		`CREATE FUNCTION make_temp() RETURNS text LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'CREATE TEMP TABLE IF NOT EXISTS hostile_temp (a int)'; RETURN 'temp'; END $$`,
		`CREATE FUNCTION take_lock() RETURNS text LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_lock(4242); RETURN 'lock'; END $$`,
		`CREATE FUNCTION do_prepare() RETURNS text LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'PREPARE hostile_plan AS SELECT 1'; RETURN 'prepare'; END $$`,
		`CREATE FUNCTION do_listen() RETURNS text LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'LISTEN hostile_channel'; RETURN 'listen'; END $$`,
		// Held cursors, role switches, and placeholder settings require CLOSE ALL,
		// RESET ROLE, and RESET ALL respectively. do_role uses set_config on a NOLOGIN
		// role so current_user stays different from session_user after the function returns;
		// do_custom writes a placeholder setting that pg_settings omits, so tests check it
		// with an explicit current_setting read-back.
		"CREATE ROLE " + hostileRole + " NOLOGIN",
		"GRANT " + hostileRole + " TO " + RealBackendUser,
		`CREATE FUNCTION do_cursor() RETURNS text LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'DECLARE hostile_cursor CURSOR WITH HOLD FOR SELECT 1'; RETURN 'cursor'; END $$`,
		`CREATE FUNCTION do_role() RETURNS text LANGUAGE plpgsql AS $$ BEGIN PERFORM set_config('role', '` + hostileRole + `', false); RETURN 'role'; END $$`,
		`CREATE FUNCTION do_custom(secret text) RETURNS text LANGUAGE plpgsql AS $$ BEGIN PERFORM set_config('mg_hostile.secret', secret, false); RETURN 'custom'; END $$`,
		"GRANT EXECUTE ON FUNCTION hide_set(), make_temp(), take_lock(), do_prepare(), do_listen(), do_cursor(), do_role(), do_custom(text) TO " + RealBackendUser,
		"GRANT CREATE, TEMPORARY ON DATABASE " + RealDatabase + " TO " + RealBackendUser,
	}
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }
