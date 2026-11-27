package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

func parseEdited(t *testing.T, edits map[string]any) (*config.File, error) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(minimal), &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range edits {
		set(m, k, v)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return config.Parse(b)
}

func TestPoolRulesFailClosedNamingTheKey(t *testing.T) {
	cases := []struct {
		name  string
		edits map[string]any
		key   string
		says  string
	}{
		{"keepalive idle above half the path limit", map[string]any{"backend.path_idle_limit": "50s", "backend.tcp_keepalive.idle": "30s",
			"pool.idle_timeout": "40s", "pool.check_after_idle": "20s"}, "backend.tcp_keepalive.idle", "more than half of backend.path_idle_limit"},
		{"idle timeout at the path limit", map[string]any{"pool.idle_timeout": "240s"}, "pool.idle_timeout", "below backend.path_idle_limit"},
		{"check after idle at the idle timeout", map[string]any{"pool.check_after_idle": "180s"}, "pool.check_after_idle", "below pool.idle_timeout"},
		{"check timeout at the statement deadline", map[string]any{"pool.check_timeout": "30s"}, "pool.check_timeout", "timeouts.statement"},
		{"sub-second idle with only the server keepalive on", map[string]any{"backend.tcp_keepalive.mode": "off", "backend.tcp_keepalive.idle": "500ms"},
			"backend.tcp_keepalive.idle", "whole number of seconds"},
		{"sub-second interval with only the server keepalive on", map[string]any{"backend.tcp_keepalive.mode": "off", "backend.tcp_keepalive.interval": "1500ms"},
			"backend.tcp_keepalive.interval", "whole number of seconds"},
		{"sub-second idle with only the socket keepalive on", map[string]any{"backend.server_keepalive": "off", "backend.tcp_keepalive.idle": "2500ms"},
			"backend.tcp_keepalive.idle", "whole number of seconds"},
		{"keepalive mode", map[string]any{"backend.tcp_keepalive.mode": "sometimes"}, "backend.tcp_keepalive.mode", "must be on or off"},
		{"server keepalive", map[string]any{"backend.server_keepalive": "yes"}, "backend.server_keepalive", "must be on or off"},
		{"keepalive count", map[string]any{"backend.tcp_keepalive.count": 101}, "backend.tcp_keepalive.count", "must be 1 to"},
		{"lease reset", map[string]any{"lease.reset": "never"}, "lease.reset", ""},
		{"lease reseed", map[string]any{"lease.reseed": "sometimes"}, "lease.reseed", ""},
		{"breaker failures", map[string]any{"breaker.failures": 1001}, "breaker.failures", "must be 1 to"},
		{"breaker open for", map[string]any{"breaker.open_for": "-1s"}, "breaker.open_for", ""},
		{"jitter percent", map[string]any{"pool.jitter_percent": 51}, "pool.jitter_percent", "must be 1 to"},
		{"max connecting", map[string]any{"pool.max_connecting": 257}, "pool.max_connecting", "must be 1 to"},
		{"max cancels", map[string]any{"pool.max_cancels": 1025}, "pool.max_cancels", "must be 1 to"},
		{"lifetime too short for its jitter", map[string]any{"pool.max_lifetime": "5ms"}, "pool.max_lifetime", "spreads by less than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := parseEdited(t, c.edits)
			if err == nil || f != nil {
				t.Fatalf("accepted %v", c.edits)
			}
			if !strings.Contains(err.Error(), c.key) || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("error does not name %q with %q:\n%v", c.key, c.says, err)
			}
		})
	}
}

func TestKeepaliveWholeSecondsAreAcceptedAndSentToTheServer(t *testing.T) {
	f, err := parseEdited(t, map[string]any{"backend.tcp_keepalive.mode": "off", "backend.tcp_keepalive.idle": "7s", "backend.tcp_keepalive.interval": "2s"})
	if err != nil {
		t.Fatal(err)
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range cfg.Backend.SessionSettings {
		got[p.Name] = p.Value
	}
	if got["tcp_keepalives_idle"] != "7" || got["tcp_keepalives_interval"] != "2" || got["tcp_keepalives_count"] != "3" {
		t.Fatalf("session settings %v", got)
	}
}

func TestFleetDivisionSetsEachInstancesConnections(t *testing.T) {
	r := &fixedRandom{}
	for _, c := range []struct {
		instances, total, rate, explicit, want int
		warn                                   string
	}{
		{3, 20, 25, 0, 6, "fleet.backend_connections 20 leaves a remainder of 2 over 3 instances; each takes 6"},
		{5, 10, 10, 0, 2, ""},
		{4, 4, 8, 0, 1, ""},
		{2, 40, 10, 0, 20, ""},
		{3, 20, 25, 4, 4, "each takes 6"},
	} {
		edits := map[string]any{"fleet.instances": c.instances, "fleet.backend_connections": c.total, "fleet.connect_rate_per_s": c.rate}
		if c.explicit != 0 {
			edits["pool.connections"] = c.explicit
		}
		f, err := parseEdited(t, edits)
		if err != nil {
			t.Fatalf("%v: %v", edits, err)
		}
		cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Connections != c.want || f.Pool.Connections != c.want {
			t.Fatalf("%v: %d connections per instance, want %d", edits, cfg.Connections, c.want)
		}
		if c.warn != "" && !strings.Contains(strings.Join(f.Warnings(), "\n"), c.warn) {
			t.Fatalf("%v: warnings %q, want %q", edits, f.Warnings(), c.warn)
		}
	}
	if _, err := parseEdited(t, map[string]any{"fleet.instances": 3, "fleet.backend_connections": 20, "pool.connections": 7}); err == nil ||
		!strings.Contains(err.Error(), "pool.connections") {
		t.Fatalf("an explicit pool.connections above the share: %v", err)
	}
}

func TestWarningsNameEveryRiskySetting(t *testing.T) {
	f, err := parseEdited(t, map[string]any{"backend.tcp_keepalive.mode": "off", "backend.server_keepalive": "off",
		"fleet.instances": 3, "fleet.backend_connections": 21, "fleet.connect_rate_per_s": 10})
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(f.Warnings(), "\n")
	for _, want := range []string{"backend.tcp_keepalive.mode is off", "backend.server_keepalive is off", "fleet.connect_rate_per_s 10 leaves a remainder of 1"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings %q lack %q", w, want)
		}
	}
	if strings.Contains(w, "fleet.backend_connections") {
		t.Errorf("an even division warned: %q", w)
	}
	quiet, err := config.Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if w := quiet.Warnings(); len(w) != 0 {
		t.Errorf("the defaults warn: %q", w)
	}
}
