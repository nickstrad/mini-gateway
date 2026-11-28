package config

import (
	"errors"
	"time"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	// DefaultCoalesceMode is the binary's default coalescing mode ("auto").
	DefaultCoalesceMode = "auto"
	// DefaultCoalesceScope is the default key scope ("all-tenants").
	DefaultCoalesceScope = "all-tenants"
	// DefaultCoalesceJoin is the default join mode ("strict").
	DefaultCoalesceJoin = "strict"
	// CoalesceScopeAll shares eligible reads across all tenants.
	CoalesceScopeAll = "all-tenants"
	// CoalesceScopeTenant isolates shared flights per tenant.
	CoalesceScopeTenant = "tenant"
)

// Coalesce holds the JSON "coalesce" configuration block.
type Coalesce struct {
	Mode          string   `json:"mode"`
	KeyScope      string   `json:"key_scope"`
	Join          string   `json:"join"`
	MaxFlights    int      `json:"max_flights"`
	MaxWaiters    int      `json:"max_waiters"`
	MaxReplyBytes int64    `json:"max_reply_bytes"`
	NoShareFor    Duration `json:"no_share_for"`
	NoShareMax    int      `json:"no_share_max"`
	HotKeys       int      `json:"hot_keys"`
}

func (f *File) applyCoalesceDefaults() {
	c := &f.Coalesce
	setString(&c.Mode, DefaultCoalesceMode)
	setString(&c.KeyScope, DefaultCoalesceScope)
	setString(&c.Join, DefaultCoalesceJoin)
	setInt(&c.MaxFlights, coalesce.DefaultMaxFlights)
	setInt(&c.MaxWaiters, coalesce.DefaultMaxWaiters)
	setDuration(&c.NoShareFor, time.Duration(coalesce.DefaultNoShareFor))
	setInt(&c.NoShareMax, coalesce.DefaultNoShareMax)
}

func (f *File) coalesceOn() bool { return f.Coalesce.Mode == "auto" || f.Coalesce.Mode == "hinted" }

func (f *File) coalesce() coalesce.Config {
	mode, _ := coalesce.ParseMode(f.Coalesce.Mode)
	join, _ := coalesce.ParseJoin(f.Coalesce.Join)
	return coalesce.Config{Mode: mode, Join: join, MaxFlights: f.Coalesce.MaxFlights, MaxWaiters: f.Coalesce.MaxWaiters,
		MaxReplyBytes: f.Coalesce.MaxReplyBytes, NoShareFor: coalesce.Instant(f.Coalesce.NoShareFor),
		NoShareMax: f.Coalesce.NoShareMax, HotKeys: f.Coalesce.HotKeys}
}

func (f *File) keyScope() work.KeyScope {
	if f.Coalesce.KeyScope == CoalesceScopeTenant {
		return work.ScopeTenant
	}
	return work.ScopeGlobal
}

func (f *File) validateCoalesce() error {
	c := f.Coalesce
	var errs []error
	if _, ok := coalesce.ParseMode(c.Mode); !ok {
		errs = append(errs, bad("coalesce.mode", "%q: must be off, auto or hinted", c.Mode))
	}
	if c.KeyScope != CoalesceScopeAll && c.KeyScope != CoalesceScopeTenant {
		errs = append(errs, bad("coalesce.key_scope", "%q: must be all-tenants or tenant", c.KeyScope))
	}
	if _, ok := coalesce.ParseJoin(c.Join); !ok {
		errs = append(errs, bad("coalesce.join", "%q: must be strict or relaxed", c.Join))
	}
	for _, v := range []struct {
		key string
		n   int64
	}{{"coalesce.max_flights", int64(c.MaxFlights)}, {"coalesce.max_waiters", int64(c.MaxWaiters)},
		{"coalesce.max_reply_bytes", c.MaxReplyBytes}, {"coalesce.no_share_max", int64(c.NoShareMax)}, {"coalesce.hot_keys", int64(c.HotKeys)}} {
		if v.n < 0 {
			errs = append(errs, bad(v.key, "must be 0 (the default) or more, got %d", v.n))
		}
	}
	// Validate numeric bounds even when mode is "off" so misconfigured caps fail
	// closed at load rather than silently passing until coalescing is enabled.
	if !f.coalesceOn() {
		errs = append(errs, f.coalesceBoundsWhenOff()...)
	}
	// Refuse on-switch + all-tenants only while coalescing is active: with
	// mode "off" no read is shared across tenants.
	if f.coalesceOn() && f.Lease.Reset == LeaseResetOnSwitch && c.KeyScope == CoalesceScopeAll {
		errs = append(errs, bad("coalesce.key_scope", "all-tenants cannot be used with lease.reset on-switch: a connection may still hold its last tenant's session state, and a shared read on it would hand that state to another tenant (use key_scope tenant, lease.reset always, or coalesce.mode off)"))
	}
	return errors.Join(errs...)
}

func (f *File) coalesceBoundsWhenOff() []error {
	cc := f.coalesce()
	cc.Mode = coalesce.ModeOff
	cc.Deadline = coalesce.DefaultDeadline
	cc = cc.WithDefaults()
	var errs []error
	if err := cc.Validate(); err != nil {
		errs = append(errs, err)
	}
	if half := int64(f.Limits.ReplyBuffer) / 2; f.Limits.ReplyBuffer > 0 && cc.MaxReplyBytes > half {
		errs = append(errs, bad("coalesce.max_reply_bytes", "%d is more than half of limits.reply_buffer (%d)", cc.MaxReplyBytes, f.Limits.ReplyBuffer))
	}
	return errs
}
