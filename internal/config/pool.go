package config

import (
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// Default values, enum strings, and validation bounds for pool, keepalive,
// lease, and breaker settings.
const (
	LeaseResetAlways   = "always"
	LeaseResetOnSwitch = "on-switch"
	LeaseReseedOff     = "off"
	LeaseReseedOn      = "on"
	KeepaliveOn        = "on"
	KeepaliveOff       = "off"

	DefaultKeepaliveIdle     = 30 * time.Second
	DefaultKeepaliveInterval = 10 * time.Second
	DefaultKeepaliveCount    = 3
	DefaultPathIdleLimit     = 240 * time.Second
	MaxKeepaliveCount        = 100
	MaxJitterPercent         = pool.MaxJitterPermille / 10
	DefaultJitterPercent     = pool.DefaultJitterPermille / 10
)

// Backend configures TCP keepalives on proxy-to-Postgres sockets, server-side
// keepalive startup parameters, and the operator's stated path idle timeout.
type Backend struct {
	TCPKeepalive    Keepalive `json:"tcp_keepalive"`
	ServerKeepalive string    `json:"server_keepalive"`
	PathIdleLimit   Duration  `json:"path_idle_limit"`
}

// Keepalive configures proxy-side TCP keepalive probes on database sockets.
type Keepalive struct {
	Mode     string   `json:"mode"`
	Idle     Duration `json:"idle"`
	Interval Duration `json:"interval"`
	Count    int      `json:"count"`
}

// Breaker configures the pool's database-down circuit breaker.
type Breaker struct {
	Failures int      `json:"failures"`
	OpenFor  Duration `json:"open_for"`
}

func setDuration(d *Duration, def time.Duration) {
	if *d == 0 {
		*d = Duration(def)
	}
}

func setInt(v *int, def int) {
	if *v == 0 {
		*v = def
	}
}

func (f *File) applyPoolDefaults() {
	setString(&f.Lease.Reseed, LeaseReseedOff)
	setString(&f.Backend.TCPKeepalive.Mode, KeepaliveOn)
	setString(&f.Backend.ServerKeepalive, KeepaliveOn)
	setDuration(&f.Backend.TCPKeepalive.Idle, DefaultKeepaliveIdle)
	setDuration(&f.Backend.TCPKeepalive.Interval, DefaultKeepaliveInterval)
	setInt(&f.Backend.TCPKeepalive.Count, DefaultKeepaliveCount)
	setDuration(&f.Backend.PathIdleLimit, DefaultPathIdleLimit)
	setInt(&f.Pool.MaxConnecting, pool.DefaultMaxConnecting)
	setDuration(&f.Pool.IdleTimeout, pool.DefaultIdleTimeout)
	setDuration(&f.Pool.MaxLifetime, pool.DefaultMaxLifetime)
	setInt(&f.Pool.JitterPercent, DefaultJitterPercent)
	setDuration(&f.Pool.CheckAfterIdle, pool.DefaultCheckAfterIdle)
	setInt(&f.Pool.MaxCancels, pool.DefaultMaxCancels)
	setInt(&f.Breaker.Failures, pool.DefaultBreakerFailures)
	setDuration(&f.Breaker.OpenFor, pool.DefaultBreakerOpenFor)
}

func (f *File) resetPolicy() proxy.ResetPolicy {
	if f.Lease.Reset == LeaseResetOnSwitch {
		return proxy.ResetOnSwitch
	}
	return proxy.ResetAlways
}

func (f *File) poolTimeouts() proxy.Timeouts {
	t := f.Timeouts.Proxy()
	t.CheckTimeout = f.Pool.CheckTimeout.D()
	return t
}

func (f *File) ratePerInstance() int {
	if share, err := pool.Share(f.Fleet.ConnectRatePerS, max(f.Fleet.Instances, 1)); err == nil {
		return share
	}
	return 0
}

func (f *File) divideFleetConnections() {
	if f.Pool.Connections != 0 || f.Fleet.BackendConnections <= 0 {
		return
	}
	instances := f.Fleet.Instances
	if instances == 0 {
		instances = DefaultFleetInstances
	}
	if share, err := pool.Share(f.Fleet.BackendConnections, instances); err == nil {
		f.Pool.Connections = share
	}
}

func (f *File) poolCore() *pool.Config {
	return &pool.Config{MaxConnecting: f.Pool.MaxConnecting, RatePerSec: f.ratePerInstance(), IdleTimeout: f.Pool.IdleTimeout.D(),
		MaxLifetime: f.Pool.MaxLifetime.D(), JitterPermille: f.Pool.JitterPercent * 10, CheckAfterIdle: f.Pool.CheckAfterIdle.D(),
		BreakerFailures: f.Breaker.Failures, BreakerOpenFor: f.Breaker.OpenFor.D(), MaxCancels: f.Pool.MaxCancels}
}

func (f *File) validatePool() error {
	var errs []error
	for _, v := range []struct {
		key    string
		n, max int
	}{{"pool.max_connecting", f.Pool.MaxConnecting, pool.MaxConnectingLimit}, {"pool.jitter_percent", f.Pool.JitterPercent, MaxJitterPercent},
		{"pool.max_cancels", f.Pool.MaxCancels, pool.MaxCancelsLimit}, {"breaker.failures", f.Breaker.Failures, pool.MaxBreakerFailures},
		{"backend.tcp_keepalive.count", f.Backend.TCPKeepalive.Count, MaxKeepaliveCount}} {
		if v.n < 1 || v.n > v.max {
			errs = append(errs, bad(v.key, "must be 1 to %d (0 or omitted means the default), got %d", v.max, v.n))
		}
	}
	ka := f.Backend.TCPKeepalive
	if ka.Mode != KeepaliveOn && ka.Mode != KeepaliveOff {
		errs = append(errs, bad("backend.tcp_keepalive.mode", "%q: must be on or off", ka.Mode))
	}
	if f.Backend.ServerKeepalive != KeepaliveOn && f.Backend.ServerKeepalive != KeepaliveOff {
		errs = append(errs, bad("backend.server_keepalive", "%q: must be on or off", f.Backend.ServerKeepalive))
	}
	if ka.Mode == KeepaliveOn || f.Backend.ServerKeepalive == KeepaliveOn {
		for _, d := range []struct {
			key string
			v   time.Duration
		}{{"backend.tcp_keepalive.idle", ka.Idle.D()}, {"backend.tcp_keepalive.interval", ka.Interval.D()}} {
			if d.v < time.Second || d.v%time.Second != 0 {
				errs = append(errs, bad(d.key, "%s must be a whole number of seconds, at least 1s, while backend.tcp_keepalive.mode or backend.server_keepalive is on (Postgres takes tcp_keepalives_* in seconds)", d.v))
			}
		}
	}
	limit := f.Backend.PathIdleLimit.D()
	if ka.Mode == KeepaliveOn && ka.Idle.D() > limit/2 {
		errs = append(errs, bad("backend.tcp_keepalive.idle", "%s is more than half of backend.path_idle_limit (%s): a middlebox could drop an idle connection before the first probe",
			ka.Idle.D(), limit))
	}
	if f.Pool.IdleTimeout.D() >= limit {
		errs = append(errs, bad("pool.idle_timeout", "%s must be below backend.path_idle_limit (%s)", f.Pool.IdleTimeout.D(), limit))
	}
	if f.Pool.CheckAfterIdle.D() >= f.Pool.IdleTimeout.D() {
		errs = append(errs, bad("pool.check_after_idle", "%s must be below pool.idle_timeout (%s)", f.Pool.CheckAfterIdle.D(), f.Pool.IdleTimeout.D()))
	}
	return errors.Join(errs...)
}

// Warnings reports valid but risky or lossy settings (disabled keepalives or
// fleet budgets that leave a remainder when divided across instances).
func (f *File) Warnings() []string {
	var out []string
	if f.Backend.TCPKeepalive.Mode == KeepaliveOff {
		out = append(out, "backend.tcp_keepalive.mode is off: an idle connection dropped by a middlebox is found only by the check before reuse")
	}
	if f.Backend.ServerKeepalive == KeepaliveOff {
		out = append(out, "backend.server_keepalive is off: Postgres frees the slot of a vanished proxy only after the kernel default")
	}
	if n := f.Fleet.Instances; n > 0 {
		if rem := f.Fleet.BackendConnections % n; rem != 0 {
			out = append(out, "fleet.backend_connections "+strconv.Itoa(f.Fleet.BackendConnections)+" leaves a remainder of "+strconv.Itoa(rem)+" over "+strconv.Itoa(n)+" instances; each takes "+strconv.Itoa(f.Fleet.BackendConnections/n))
		}
		if rem := f.Fleet.ConnectRatePerS % n; rem != 0 {
			out = append(out, "fleet.connect_rate_per_s "+strconv.Itoa(f.Fleet.ConnectRatePerS)+" leaves a remainder of "+strconv.Itoa(rem)+" over "+strconv.Itoa(n)+" instances; each takes "+strconv.Itoa(f.Fleet.ConnectRatePerS/n))
		}
	}
	return out
}

// KeepAlive returns the net.KeepAliveConfig for database sockets dialed by the shell.
func (f *File) KeepAlive() net.KeepAliveConfig {
	ka := f.Backend.TCPKeepalive
	if ka.Mode == KeepaliveOff {
		return net.KeepAliveConfig{Enable: false, Idle: -1, Interval: -1, Count: -1}
	}
	return net.KeepAliveConfig{Enable: true, Idle: ka.Idle.D(), Interval: ka.Interval.D(), Count: ka.Count}
}

func seconds(d Duration) string { return strconv.FormatInt(int64(d.D()/time.Second), 10) }

func (f *File) serverKeepalive() []pgwire.Param {
	if f.Backend.ServerKeepalive != KeepaliveOn {
		return nil
	}
	ka := f.Backend.TCPKeepalive
	return []pgwire.Param{{Name: "tcp_keepalives_idle", Value: seconds(ka.Idle)}, {Name: "tcp_keepalives_interval", Value: seconds(ka.Interval)},
		{Name: "tcp_keepalives_count", Value: strconv.Itoa(ka.Count)}}
}
