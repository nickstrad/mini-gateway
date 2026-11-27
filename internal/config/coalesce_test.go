package config_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func proxyOf(t *testing.T, f *config.File) func() (coalesce.Config, work.KeyScope) {
	t.Helper()
	return func() (coalesce.Config, work.KeyScope) {
		r := &fixedRandom{}
		cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.WithDefaults().Coalesce, cfg.KeyScope
	}
}

func TestCoalesceDefaultsToAutoStrictAcrossTenants(t *testing.T) {
	f, err := config.Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if f.Coalesce.Mode != "auto" || f.Coalesce.Join != "strict" || f.Coalesce.KeyScope != "all-tenants" {
		t.Fatalf("%+v", f.Coalesce)
	}
	c, scope := proxyOf(t, f)()
	if c.Mode != coalesce.ModeAuto || c.Join != coalesce.JoinStrict || scope != work.ScopeGlobal {
		t.Fatalf("%+v scope %v", c, scope)
	}
	if c.MaxFlights != coalesce.DefaultMaxFlights || c.MaxWaiters != coalesce.DefaultMaxWaiters || c.NoShareFor != coalesce.DefaultNoShareFor {
		t.Fatalf("%+v", c)
	}
	if c.Deadline != coalesce.Instant(f.Timeouts.Statement) {
		t.Fatalf("the flight deadline %d is not the statement deadline %d", c.Deadline, f.Timeouts.Statement)
	}
}

func TestCoalesceKeysParseAndReachTheEngine(t *testing.T) {
	f, err := parseEdited(t, map[string]any{"coalesce.mode": "hinted", "coalesce.join": "relaxed", "coalesce.key_scope": "tenant",
		"coalesce.max_waiters": 7, "coalesce.max_reply_bytes": 4096, "coalesce.no_share_for": "3s", "coalesce.hot_keys": 4})
	if err != nil {
		t.Fatal(err)
	}
	c, scope := proxyOf(t, f)()
	if c.Mode != coalesce.ModeHinted || c.Join != coalesce.JoinRelaxed || scope != work.ScopeTenant || c.MaxWaiters != 7 ||
		c.MaxReplyBytes != 4096 || c.NoShareFor != 3*coalesce.Second || c.HotKeys != 4 {
		t.Fatalf("%+v scope %v", c, scope)
	}
}

func TestCoalesceRulesFailClosedNamingTheKey(t *testing.T) {
	for _, c := range []struct {
		edits map[string]any
		key   string
	}{
		{map[string]any{"coalesce.mode": "always"}, "coalesce.mode"},
		{map[string]any{"coalesce.join": "loose"}, "coalesce.join"},
		{map[string]any{"coalesce.key_scope": "global"}, "coalesce.key_scope"},
		{map[string]any{"coalesce.max_flights": -1}, "coalesce.max_flights"},
		{map[string]any{"coalesce.max_waiters": -1}, "coalesce.max_waiters"},
		{map[string]any{"coalesce.max_waiters": 1}, "coalesce.max_waiters"},
		{map[string]any{"coalesce.max_reply_bytes": -5}, "coalesce.max_reply_bytes"},
		{map[string]any{"coalesce.no_share_max": -1}, "coalesce.no_share_max"},
		{map[string]any{"coalesce.hot_keys": -1}, "coalesce.hot_keys"},
		{map[string]any{"coalesce.hot_keys": 65}, "coalesce.hot_keys"},
		{map[string]any{"coalesce.no_share_for": "-1s"}, "coalesce.no_share_for"},
		{map[string]any{"coalesce.max_reply_bytes": 200000}, "coalesce.max_reply_bytes"},
		{map[string]any{"coalesce.max_flights": -1, "coalesce.mode": "off"}, "coalesce.max_flights"},
		{map[string]any{"lease.reset": "on-switch"}, "coalesce.key_scope"},
		{map[string]any{"lease.reset": "on-switch", "coalesce.mode": "hinted"}, "coalesce.key_scope"},
	} {
		_, err := parseEdited(t, c.edits)
		if err == nil || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%v: %v, want an error naming %s", c.edits, err, c.key)
		}
	}
}

func TestOnSwitchIsAllowedWithCoalescingOffOrScopedToATenant(t *testing.T) {
	for _, edits := range []map[string]any{
		{"lease.reset": "on-switch", "coalesce.mode": "off"},
		{"lease.reset": "on-switch", "coalesce.key_scope": "tenant"},
	} {
		if _, err := parseEdited(t, edits); err != nil {
			t.Errorf("%v: %v", edits, err)
		}
	}
}
