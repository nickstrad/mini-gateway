package config_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func TestBinaryDefaultRunsTheCoalescerTheFairDispatcherAndTheRetryPolicyOff(t *testing.T) {
	f, err := parseEdited(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Coalesce.Mode != coalesce.ModeAuto || cfg.Coalesce.Join != coalesce.JoinStrict || cfg.KeyScope != work.ScopeGlobal {
		t.Fatalf("default coalescer: mode %v join %v scope %v", cfg.Coalesce.Mode, cfg.Coalesce.Join, cfg.KeyScope)
	}
	if cfg.Retry.Kind != retry.KindOff || cfg.Admission.Rule != admission.RuleTimeout || cfg.Pool == nil {
		t.Fatalf("default: retry %v rule %v pool core %v", cfg.Retry.Kind, cfg.Admission.Rule, cfg.Pool != nil)
	}
	e, err := proxy.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fs := e.AdmissionSnapshot().Fair; !fs.On || fs.Limit != int64(cfg.Connections) {
		t.Fatalf("default engine: fair dispatcher %v limit %d", fs.On, fs.Limit)
	}
	if _, ok := e.RetrySnapshot(); !ok {
		t.Fatal("default engine: no retry core")
	}
	if e.CoalesceSnapshot().Mode != coalesce.ModeAuto {
		t.Fatalf("default engine: coalescer mode %d", e.CoalesceSnapshot().Mode)
	}
	on, err := parseEdited(t, map[string]any{"retry.policy": "pre-forward-once", "coalesce.join": "relaxed"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err = on.Proxy(r, pgwire.NewKeyIssuer(r)); err != nil {
		t.Fatal(err)
	}
	if e, err = proxy.New(cfg); err != nil || !e.AdmissionSnapshot().Fair.On || cfg.Retry.Kind != retry.KindPreForwardOnce || cfg.Coalesce.Join != coalesce.JoinRelaxed {
		t.Fatalf("all three on: %v", err)
	}
}

func TestCoalesceRetryAndFairnessKeysAreValidatedTogether(t *testing.T) {
	_, err := parseEdited(t, map[string]any{"coalesce.mode": "always", "coalesce.max_waiters": 1, "retry.policy": "everything",
		"fairness.default_weight": -2, "queue.rule": "fifo"})
	if err == nil {
		t.Fatal("accepted bad coalesce, retry and fairness keys")
	}
	for _, key := range []string{"coalesce.mode", "coalesce.max_waiters", "retry.policy:", "fairness.default_weight:", "queue.rule:"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the joined error does not name %s:\n%v", key, err)
		}
	}
}

func TestTheOnSwitchRefusalAppliesOnlyWhenCoalescingIsOnWhateverTheRetryAndFairnessKeys(t *testing.T) {
	others := map[string]any{"retry.policy": "pre-forward-once", "fairness.default_weight": 3}
	with := func(edits map[string]any) map[string]any {
		out := map[string]any{"lease.reset": "on-switch"}
		for k, v := range others {
			out[k] = v
		}
		for k, v := range edits {
			out[k] = v
		}
		return out
	}
	for _, mode := range []string{"auto", "hinted"} {
		if _, err := parseEdited(t, with(map[string]any{"coalesce.mode": mode})); err == nil || !strings.Contains(err.Error(), "coalesce.key_scope") {
			t.Errorf("mode %s with on-switch: %v, want the key_scope refusal", mode, err)
		}
	}
	for _, edits := range []map[string]any{{"coalesce.mode": "off"}, {"coalesce.key_scope": "tenant"}} {
		if _, err := parseEdited(t, with(edits)); err != nil {
			t.Errorf("%v: %v", edits, err)
		}
	}
}
