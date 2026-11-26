package config

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// ErrNotValidated is returned when Proxy is called on a File not produced by Parse or Load.
var ErrNotValidated = errors.New("config: the file was not built by Parse or Load")

// Proxy builds a validated proxy.Config from f using r for SCRAM nonces and the
// mock-challenge secret, and keys for proxy-issued BackendKeyData values.
func (f *File) Proxy(r pgwire.Random, keys *pgwire.KeyIssuer) (proxy.Config, error) {
	if !f.res.ok {
		return proxy.Config{}, ErrNotValidated
	}
	if r == nil || keys == nil {
		return proxy.Config{}, fmt.Errorf("%w: a random source and a key issuer are required", ErrInvalid)
	}
	secret := make([]byte, 0, 8*mockSecretWords)
	for range mockSecretWords {
		v := r.Uint64()
		for i := range 8 {
			secret = append(secret, byte(v>>(8*i)))
		}
	}
	cfg := f.proxyCaps()
	cfg.Client = pgwire.ClientConfig{TLS: f.clientMode(), Auth: pgwire.AuthSCRAM, Tenants: f.res.tenants,
		Database: f.Database.Name, Random: r, Keys: keys, MockSecret: secret}
	cfg.Backend = pgwire.BackendConfig{TLS: f.backendMode(), User: f.Database.User, Database: f.Database.Name,
		Password: f.res.password, Random: r}
	if err := errors.Join(cfg.Validate(), cfg.Client.Validate(), cfg.Backend.Validate()); err != nil {
		return proxy.Config{}, err
	}
	return cfg, nil
}

func (f *File) clientMode() pgwire.ClientTLSMode {
	switch f.ClientTLS.Mode {
	case ClientTLSAllow:
		return pgwire.ClientTLSAllow
	case ClientTLSRequire:
		return pgwire.ClientTLSRequire
	}
	return pgwire.ClientTLSOff
}

func (f *File) backendMode() pgwire.BackendTLSMode {
	switch f.BackendTLS.Mode {
	case BackendTLSRequire:
		return pgwire.BackendTLSRequire
	case BackendTLSVerifyFull:
		return pgwire.BackendTLSVerifyFull
	}
	return pgwire.BackendTLSDisable
}

// ClientTLSConfig returns the server-side TLS 1.2+ config for clients, or nil when off.
func (f *File) ClientTLSConfig() *tls.Config {
	if !f.res.ok || f.ClientTLS.Mode == ClientTLSOff {
		return nil
	}
	return &tls.Config{Certificates: []tls.Certificate{f.res.clientCert}, MinVersion: tls.VersionTLS12}
}

// BackendTLSConfig returns the client-side TLS 1.2+ config for Postgres, or nil when disabled.
func (f *File) BackendTLSConfig() *tls.Config {
	if !f.res.ok {
		return nil
	}
	switch f.BackendTLS.Mode {
	case BackendTLSRequire:
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	case BackendTLSVerifyFull:
		return &tls.Config{RootCAs: f.res.backendCAs, ServerName: f.res.serverName, MinVersion: tls.VersionTLS12}
	}
	return nil
}

// ProxyTimeouts returns the effective engine timeout budget.
func (f *File) ProxyTimeouts() proxy.Timeouts { return f.Timeouts.Proxy() }

// PublishInterval returns the configured observability publish interval.
func (f *File) PublishInterval() time.Duration { return f.Obs.PublishInterval.D() }

func (f *File) computeFingerprint() string {
	c := *f
	c.Database.Password = ""
	c.res = resolved{}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:fingerprintLen]
}

// DeriveVerifier derives a SCRAM-SHA-256 verifier string suitable for the tenants table.
func DeriveVerifier(password string, salt []byte, iterations int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("%w: empty password", pgwire.ErrVerifier)
	}
	if len(salt) == 0 {
		return "", fmt.Errorf("%w: empty salt", pgwire.ErrVerifier)
	}
	v, err := pgwire.NewVerifier(password, salt, iterations)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}
