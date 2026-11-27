// Package config parses, applies defaults to, and validates mini-gateway's
// JSON configuration file. Validation fails closed and names the offending key.
package config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

// Default configuration values and parser limits.
const (
	DefaultListen          = "127.0.0.1:6432"
	DefaultAdminListen     = "127.0.0.1:6433"
	DefaultInstance        = "gw-1"
	DefaultClientTLS       = "off"
	DefaultBackendTLS      = "disable"
	DefaultLeaseReset      = "always"
	DefaultPublishInterval = time.Second
	DefaultFleetInstances  = 1
	DefaultConnectRate     = 10
	DefaultBackendMax      = 100
	BackendReserve         = 3
	MaxConfigBytes         = 1 << 20
	maxInstanceLen         = 64
	fingerprintLen         = 16
	mockSecretWords        = 4
	maxFileBytes           = 1 << 20
)

// Supported client_tls.mode and backend_tls.mode values.
const (
	ClientTLSOff     = "off"
	ClientTLSAllow   = "allow"
	ClientTLSRequire = "require"

	BackendTLSDisable    = "disable"
	BackendTLSRequire    = "require"
	BackendTLSVerifyFull = "verify-full"
)

// ErrInvalid is wrapped by every Parse and Load configuration error.
var ErrInvalid = errors.New("config: invalid")

// Duration wraps time.Duration for JSON decoding of positive Go duration strings.
type Duration time.Duration

var durationType = reflect.TypeFor[Duration]()

// UnmarshalJSON parses a positive Go duration string such as "5s".
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "a non-string value", Type: durationType}
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: strconv.Quote(s) + " (not a Go duration such as \"5s\")", Type: durationType}
	}
	if v <= 0 {
		return &json.UnmarshalTypeError{Value: strconv.Quote(s) + " (a duration must be above 0)", Type: durationType}
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON encodes d using time.Duration.String.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// D returns the underlying time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// File is the validated, defaulted root configuration document returned by Parse or Load.
type File struct {
	Listen      string     `json:"listen"`
	AdminListen string     `json:"admin_listen"`
	Instance    string     `json:"instance"`
	Database    Database   `json:"database"`
	ClientTLS   ClientTLS  `json:"client_tls"`
	BackendTLS  BackendTLS `json:"backend_tls"`
	Tenants     []Tenant   `json:"tenants"`
	Pool        Pool       `json:"pool"`
	Queue       Queue      `json:"queue"`
	Limits      Limits     `json:"limits"`
	Timeouts    Timeouts   `json:"timeouts"`
	Lease       Lease      `json:"lease"`
	Obs         Obs        `json:"obs"`
	Fleet       Fleet      `json:"fleet"`

	res resolved
}

// Database configures the upstream Postgres endpoint and shared backend role (decision D6).
type Database struct {
	Address      string `json:"address"`
	Name         string `json:"name"`
	User         string `json:"user"`
	Password     string `json:"password"`
	PasswordFile string `json:"password_file"`
}

// ClientTLS configures client-facing TLS termination (decision D13).
type ClientTLS struct {
	Mode     string `json:"mode"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// BackendTLS configures TLS on connections from the proxy to Postgres.
type BackendTLS struct {
	Mode       string `json:"mode"`
	CAFile     string `json:"ca_file"`
	ServerName string `json:"server_name"`
}

// Tenant maps a client startup user name to its SCRAM-SHA-256 verifier string.
type Tenant struct {
	User     string `json:"user"`
	Verifier string `json:"verifier"`
}

// Pool configures the size of the backend connection set.
type Pool struct {
	Connections int `json:"connections"`
}

// Queue configures the bounded admission queue capacity.
type Queue struct {
	Capacity int `json:"capacity"`
}

// Limits configures per-instance client, startup, and reply-buffer bounds.
type Limits struct {
	MaxClients  int `json:"max_clients"`
	MaxStartup  int `json:"max_startup"`
	ReplyBuffer int `json:"reply_buffer"`
}

// Timeouts mirrors proxy.Timeouts as JSON duration strings.
type Timeouts struct {
	ClientStartup    Duration `json:"client_startup"`
	ClientMidMessage Duration `json:"client_mid_message"`
	ClientIdle       Duration `json:"client_idle"`
	QueueWait        Duration `json:"queue_wait"`
	DatabaseConnect  Duration `json:"database_connect"`
	Statement        Duration `json:"statement"`
	Reset            Duration `json:"reset"`
	ClientWrite      Duration `json:"client_write"`
	RedialDelay      Duration `json:"redial_delay"`
}

// Lease configures lease-release behaviour (only "always" is accepted in chunk 6).
type Lease struct {
	Reset string `json:"reset"`
}

// Obs configures snapshot publishing and optional CSV flushing.
type Obs struct {
	PublishInterval Duration `json:"publish_interval"`
	Detail          bool     `json:"detail"`
	CSV             CSV      `json:"csv"`
}

// CSV configures the real-process CSV flush adapter.
type CSV struct {
	Dir      string `json:"dir"`
	MaxBytes int64  `json:"max_bytes"`
	Scenario string `json:"scenario"`
}

// Fleet configures the static multi-instance database budget validated at load.
type Fleet struct {
	Instances             int `json:"instances"`
	BackendConnections    int `json:"backend_connections"`
	ConnectRatePerS       int `json:"connect_rate_per_s"`
	BackendMaxConnections int `json:"backend_max_connections"`
}

type resolved struct {
	ok          bool
	password    string
	clientCert  tls.Certificate
	backendCAs  *x509.CertPool
	serverName  string
	tenants     *pgwire.TenantTable
	fingerprint string
}

// Parse decodes, defaults, and validates JSON configuration bytes b without a
// base directory for relative file paths.
func Parse(b []byte) (*File, error) { return parse(b, "") }

// Load reads, decodes, defaults, and validates the JSON configuration file at
// path, resolving relative certificate, CA, and password file paths against path's directory.
func Load(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, MaxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return parse(b, filepath.Dir(path))
}

func parse(b []byte, base string) (*File, error) {
	if len(b) > MaxConfigBytes {
		return nil, fmt.Errorf("%w: the file is larger than %d bytes", ErrInvalid, MaxConfigBytes)
	}
	f, err := decode(b)
	if err != nil {
		return nil, err
	}
	f.applyDefaults()
	if err := f.validate(base); err != nil {
		return nil, err
	}
	f.res.fingerprint = f.computeFingerprint()
	f.res.ok = true
	return f, nil
}

// Fingerprint returns the 16-hex-character SHA-256 digest of the canonical
// effective config with database.password blanked.
func (f *File) Fingerprint() string { return f.res.fingerprint }

func bad(key, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, key, fmt.Sprintf(format, args...))
}
