package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/config"
)

// TestQueueKeysFailClosedNamingTheKey proves invalid queue.* settings fail validation naming the offending key.
func TestQueueKeysFailClosedNamingTheKey(t *testing.T) {
	for _, c := range []struct {
		key string
		v   any
	}{
		{"queue.rule", "fifo"},
		{"queue.rule", "folly"},
		{"queue.rule", "codel"},
		{"queue.lifo", "lifo"},
		{"queue.target", "-1ms"},
		{"queue.target", "200ms"},
		{"queue.interval", "-5ms"},
		{"queue.interval", "2m"},
		{"queue.tenant_capacity", -1},
		{"queue.tenant_capacity", 1 << 21},
		{"queue.max_tenants", -1},
		{"queue.max_tenants", 1 << 20},
		{"queue.lifo_hold", "-1s"},
		{"queue.lifo_hold", "2h"},
	} {
		_, err := parseWith(t, c.key, c.v)
		if err == nil || !strings.Contains(err.Error(), c.key+":") {
			t.Errorf("%s = %v: %v, want an error naming the key", c.key, c.v, err)
		}
	}
}

// TestQueueKeysZeroMeansTheDefault proves omitted or zero queue.* settings resolve to documented defaults.
func TestQueueKeysZeroMeansTheDefault(t *testing.T) {
	for key, zero := range map[string]any{"queue.tenant_capacity": 0, "queue.max_tenants": 0, "queue.target": nil, "queue.interval": nil, "queue.lifo_hold": nil, "queue.rule": nil, "queue.lifo": nil} {
		f, err := parseWith(t, key, zero)
		if err != nil {
			t.Fatalf("%s: 0: %v", key, err)
		}
		got := f.Queue
		if got.TenantCapacity != admission.DefaultTenantCapacity || got.MaxTenants != admission.DefaultMaxTenants ||
			int64(got.Target) != admission.DefaultTarget || int64(got.Interval) != admission.DefaultInterval ||
			int64(got.LIFOHold) != admission.DefaultLIFOHold || got.Rule != "timeout" || got.LIFO != "adaptive" {
			t.Fatalf("%s: 0 gave %+v", key, got)
		}
	}
}

// TestQueueConfigReachesTheEngine proves parsed queue.* settings propagate into proxy.Config.AdmissionConfig.
func TestQueueConfigReachesTheEngine(t *testing.T) {
	f, err := parseWith(t, "queue.rule", "control-law")
	if err != nil {
		t.Fatal(err)
	}
	if a := f.ProxyCaps().AdmissionConfig(); a.Rule != admission.RuleControlLaw || a.Capacity != f.Queue.Capacity {
		t.Fatalf("%+v", a)
	}
	f, err = parseWith(t, "queue.tenant_capacity", 7)
	if err != nil || f.ProxyCaps().AdmissionConfig().TenantCapacity != 7 {
		t.Fatalf("%v", err)
	}
}

// TestBinaryConfigCannotSelectAComparisonArm proves simulator-only arms (fifo, folly) are refused by binary config.
func TestBinaryConfigCannotSelectAComparisonArm(t *testing.T) {
	for _, arm := range []admission.RuleKind{admission.RuleFIFO, admission.RuleFolly} {
		_, err := parseWith(t, "queue.rule", arm.String())
		if err == nil || !errors.Is(err, config.ErrInvalid) && !strings.Contains(err.Error(), "queue.rule") {
			t.Fatalf("%v accepted: %v", arm, err)
		}
	}
}

// TestConfigNeverSetsAnAdmissionPlant proves valid binary configs always leave admission Plant switches zero.
func TestConfigNeverSetsAnAdmissionPlant(t *testing.T) {
	for _, rule := range []string{"timeout", "control-law"} {
		f, err := parseWith(t, "queue.rule", rule)
		if err != nil {
			t.Fatal(err)
		}
		if p := f.ProxyCaps().Admission.Plant; p != 0 {
			t.Fatalf("config set admission plant %d", p)
		}
	}
}
