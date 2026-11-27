package config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func (f *File) applyDefaults() {
	setString(&f.Listen, DefaultListen)
	setString(&f.AdminListen, DefaultAdminListen)
	setString(&f.Instance, DefaultInstance)
	setString(&f.ClientTLS.Mode, DefaultClientTLS)
	setString(&f.BackendTLS.Mode, DefaultBackendTLS)
	setString(&f.Lease.Reset, DefaultLeaseReset)
	f.applyPoolDefaults()
	f.divideFleetConnections()
	p := f.proxyCaps().WithDefaults()
	f.Pool.Connections = p.Connections
	f.Queue.Capacity = p.QueueCap
	f.Limits.MaxClients = p.MaxClients
	f.Limits.MaxStartup = p.MaxStartup
	f.Limits.ReplyBuffer = p.ReplyBufferCap
	f.Timeouts = timeoutsFrom(p.Timeouts)
	f.Pool.CheckTimeout = Duration(p.Timeouts.CheckTimeout)
	f.applyQueueDefaults(p)
	f.applyRetryDefaults()
	if f.Obs.PublishInterval == 0 {
		f.Obs.PublishInterval = Duration(DefaultPublishInterval)
	}
	if f.Fleet.Instances == 0 {
		f.Fleet.Instances = DefaultFleetInstances
	}
	if f.Fleet.BackendConnections == 0 && f.Fleet.Instances > 0 {
		f.Fleet.BackendConnections = f.Pool.Connections * f.Fleet.Instances
	}
	if f.Fleet.ConnectRatePerS == 0 {
		f.Fleet.ConnectRatePerS = DefaultConnectRate
	}
	if f.Fleet.BackendMaxConnections == 0 {
		f.Fleet.BackendMaxConnections = DefaultBackendMax
	}
}

func setString(s *string, def string) {
	if *s == "" {
		*s = def
	}
}

func (f *File) proxyCaps() proxy.Config {
	return proxy.Config{Connections: f.Pool.Connections, QueueCap: f.Queue.Capacity, MaxClients: f.Limits.MaxClients,
		MaxStartup: f.Limits.MaxStartup, ReplyBufferCap: f.Limits.ReplyBuffer, Timeouts: f.poolTimeouts(),
		Reset: f.resetPolicy(), Reseed: f.Lease.Reseed == LeaseReseedOn, Pool: f.poolCore(), Admission: f.admission(), Retry: f.retryCore()}
}

// Proxy converts the JSON Timeouts block into proxy.Timeouts.
func (t Timeouts) Proxy() proxy.Timeouts {
	return proxy.Timeouts{ClientStartup: t.ClientStartup.D(), ClientMidMessage: t.ClientMidMessage.D(),
		ClientIdle: t.ClientIdle.D(), QueueWait: t.QueueWait.D(), DBConnect: t.DatabaseConnect.D(),
		Statement: t.Statement.D(), Reset: t.Reset.D(), ClientWrite: t.ClientWrite.D(), RedialDelay: t.RedialDelay.D(),
		IdleInTx: t.IdleInTx.D(), Cleanup: t.Cleanup.D()}
}

func timeoutsFrom(t proxy.Timeouts) Timeouts {
	return Timeouts{ClientStartup: Duration(t.ClientStartup), ClientMidMessage: Duration(t.ClientMidMessage),
		ClientIdle: Duration(t.ClientIdle), QueueWait: Duration(t.QueueWait), DatabaseConnect: Duration(t.DBConnect),
		Statement: Duration(t.Statement), Reset: Duration(t.Reset), ClientWrite: Duration(t.ClientWrite),
		RedialDelay: Duration(t.RedialDelay), IdleInTx: Duration(t.IdleInTx), Cleanup: Duration(t.Cleanup)}
}

func (f *File) validate(base string) error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	add(validAddress("listen", f.Listen))
	add(validAddress("admin_listen", f.AdminListen))
	if f.Listen == f.AdminListen && !strings.HasSuffix(f.Listen, ":0") {
		add(bad("admin_listen", "must differ from listen (%s)", f.Listen))
	}
	add(validInstance(f.Instance))
	add(f.validateDatabase(base))
	add(f.validateClientTLS(base))
	add(f.validateBackendTLS(base))
	add(f.validateTenants())
	add(f.proxyCaps().Validate())
	add(f.validateLease())
	add(f.validateQueue())
	add(f.validateObs())
	add(f.validateFleet())
	add(f.validatePool())
	add(f.validateRetry())
	return errors.Join(errs...)
}

func validAddress(key, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return bad(key, "%q is not host:port: %v", addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return bad(key, "%q has no numeric port from 0 to 65535", addr)
	}
	return nil
}

func validInstance(s string) error {
	if len(s) > maxInstanceLen {
		return bad("instance", "at most %d characters, got %d", maxInstanceLen, len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') || s == "." || s == ".." {
			return bad("instance", "%q: only letters, digits, '.', '_' and '-' are allowed", s)
		}
	}
	return nil
}

func (f *File) validateDatabase(base string) error {
	var errs []error
	d := f.Database
	host, port, err := net.SplitHostPort(d.Address)
	switch {
	case d.Address == "":
		errs = append(errs, bad("database.address", "required (host:port)"))
	case err != nil:
		errs = append(errs, bad("database.address", "%q is not host:port: %v", d.Address, err))
	case host == "":
		errs = append(errs, bad("database.address", "%q has no host", d.Address))
	default:
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			errs = append(errs, bad("database.address", "%q has no numeric port from 1 to 65535", d.Address))
		}
	}
	if d.Name == "" {
		errs = append(errs, bad("database.name", "required"))
	}
	if d.User == "" {
		errs = append(errs, bad("database.user", "required"))
	}
	switch {
	case d.Password != "" && d.PasswordFile != "":
		errs = append(errs, bad("database.password", "give database.password or database.password_file, not both"))
	case d.Password == "" && d.PasswordFile == "":
		errs = append(errs, bad("database.password", "required (or database.password_file)"))
	case d.Password != "":
		f.res.password = d.Password
	default:
		pw, err := readPasswordFile(resolvePath(base, d.PasswordFile))
		if err != nil {
			errs = append(errs, bad("database.password_file", "%v", err))
		}
		f.res.password = pw
	}
	return errors.Join(errs...)
}

func readPasswordFile(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, maxFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxFileBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", path, maxFileBytes)
	}
	pw := strings.TrimRight(string(b), "\r\n")
	if pw == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return pw, nil
}

func resolvePath(base, p string) string {
	if p == "" || base == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

func (f *File) validateClientTLS(base string) error {
	c := f.ClientTLS
	switch c.Mode {
	case ClientTLSOff:
		return nil
	case ClientTLSAllow, ClientTLSRequire:
	default:
		return bad("client_tls.mode", "%q: want off, allow or require", c.Mode)
	}
	certPEM, certErr := readTLSFile("client_tls.cert_file", c.CertFile, c.Mode, base)
	keyPEM, keyErr := readTLSFile("client_tls.key_file", c.KeyFile, c.Mode, base)
	if err := errors.Join(certErr, keyErr); err != nil {
		return err
	}
	if block, _ := pem.Decode(certPEM); block == nil || block.Type != "CERTIFICATE" {
		return bad("client_tls.cert_file", "%s holds no PEM certificate", c.CertFile)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return bad("client_tls.key_file", "%s does not load as the private key of client_tls.cert_file: %v", c.KeyFile, err)
	}
	f.res.clientCert = cert
	return nil
}

func readTLSFile(key, path, mode, base string) ([]byte, error) {
	if path == "" {
		return nil, bad(key, "required when client_tls.mode is %s", mode)
	}
	b, err := os.ReadFile(resolvePath(base, path))
	if err != nil {
		return nil, bad(key, "%v", err)
	}
	return b, nil
}

func (f *File) validateBackendTLS(base string) error {
	b := f.BackendTLS
	host, _, _ := net.SplitHostPort(f.Database.Address)
	f.res.serverName = b.ServerName
	if f.res.serverName == "" {
		f.res.serverName = host
	}
	switch b.Mode {
	case BackendTLSDisable, BackendTLSRequire:
		return nil
	case BackendTLSVerifyFull:
	default:
		return bad("backend_tls.mode", "%q: want disable, require or verify-full", b.Mode)
	}
	if b.CAFile == "" {
		return bad("backend_tls.ca_file", "required when backend_tls.mode is verify-full")
	}
	pem, err := os.ReadFile(resolvePath(base, b.CAFile))
	if err != nil {
		return bad("backend_tls.ca_file", "%v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return bad("backend_tls.ca_file", "%s holds no PEM certificate", b.CAFile)
	}
	if f.res.serverName == "" {
		return bad("backend_tls.server_name", "required: database.address has no host name to verify")
	}
	f.res.backendCAs = pool
	return nil
}

func (f *File) validateTenants() error {
	if len(f.Tenants) == 0 {
		return bad("tenants", "at least one tenant is required")
	}
	if len(f.Tenants) > pgwire.MaxTenants {
		return bad("tenants", "at most %d tenants, got %d", pgwire.MaxTenants, len(f.Tenants))
	}
	var errs []error
	list := make([]pgwire.Tenant, 0, len(f.Tenants))
	seen := make(map[string]int, len(f.Tenants))
	first := -1
	var shape pgwire.Verifier
	for i, t := range f.Tenants {
		key := "tenants[" + strconv.Itoa(i) + "]"
		if t.User == "" {
			errs = append(errs, bad(key+".user", "required"))
			continue
		}
		if j, dup := seen[t.User]; dup {
			errs = append(errs, bad(key+".user", "%q is also tenants[%d].user", t.User, j))
			continue
		}
		seen[t.User] = i
		v, err := pgwire.ParseVerifier(t.Verifier)
		if err != nil {
			errs = append(errs, bad(key+".verifier", "%v", err))
			continue
		}
		if first < 0 {
			first, shape = i, v
		} else if v.Iterations != shape.Iterations || len(v.Salt) != len(shape.Salt) {
			errs = append(errs, bad(key+".verifier", "%d iterations with a %d-byte salt, but tenants[%d] has %d iterations with a %d-byte salt: every verifier must have one shape so an unknown user's challenge looks the same",
				v.Iterations, len(v.Salt), first, shape.Iterations, len(shape.Salt)))
			continue
		}
		list = append(list, pgwire.Tenant{User: t.User, Verifier: v})
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	table, err := pgwire.NewTenantTable(list)
	if err != nil {
		return bad("tenants", "%v", err)
	}
	f.res.tenants = table
	return nil
}

func (f *File) validateLease() error {
	var errs []error
	if f.Lease.Reset != LeaseResetAlways && f.Lease.Reset != LeaseResetOnSwitch {
		errs = append(errs, bad("lease.reset", "%q: must be always or on-switch", f.Lease.Reset))
	}
	if f.Lease.Reseed != LeaseReseedOff && f.Lease.Reseed != LeaseReseedOn {
		errs = append(errs, bad("lease.reseed", "%q: must be off or on", f.Lease.Reseed))
	}
	return errors.Join(errs...)
}

func (f *File) validateObs() error {
	var errs []error
	if f.Obs.PublishInterval <= 0 || f.Obs.PublishInterval.D() > 24*time.Hour {
		errs = append(errs, bad("obs.publish_interval", "must be above 0 and at most 24h"))
	}
	if f.Obs.CSV.MaxBytes < 0 {
		errs = append(errs, bad("obs.csv.max_bytes", "must be 0 (the default cap) or more, got %d", f.Obs.CSV.MaxBytes))
	}
	return errors.Join(errs...)
}

func (f *File) validateFleet() error {
	fl := f.Fleet
	var errs []error
	for _, v := range []struct {
		key string
		n   int
	}{{"fleet.instances", fl.Instances}, {"fleet.backend_connections", fl.BackendConnections},
		{"fleet.connect_rate_per_s", fl.ConnectRatePerS}, {"fleet.backend_max_connections", fl.BackendMaxConnections}} {
		if v.n < 1 {
			errs = append(errs, bad(v.key, "must be 1 or more, got %d", v.n))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if fl.Instances > proxy.MaxConnections || fl.BackendConnections > proxy.MaxConnections*fl.Instances {
		errs = append(errs, bad("fleet.backend_connections", "%d over %d instances is more than %d per instance",
			fl.BackendConnections, fl.Instances, proxy.MaxConnections))
	}
	per := fl.BackendConnections / fl.Instances
	if fl.ConnectRatePerS < fl.Instances {
		errs = append(errs, bad("fleet.connect_rate_per_s", "%d over fleet.instances %d leaves less than 1 per instance",
			fl.ConnectRatePerS, fl.Instances))
	}
	if per < 1 {
		errs = append(errs, bad("fleet.backend_connections", "%d over fleet.instances %d leaves less than 1 per instance",
			fl.BackendConnections, fl.Instances))
	}
	if f.Pool.Connections > per {
		errs = append(errs, bad("pool.connections", "%d is more than the per-instance budget %d (fleet.backend_connections %d / fleet.instances %d)",
			f.Pool.Connections, per, fl.BackendConnections, fl.Instances))
	}
	if fl.BackendConnections+BackendReserve > fl.BackendMaxConnections {
		errs = append(errs, bad("fleet.backend_max_connections", "%d cannot hold fleet.backend_connections %d plus a reserve of %d (admin, cancel)",
			fl.BackendMaxConnections, fl.BackendConnections, BackendReserve))
	}
	return errors.Join(errs...)
}
