package config_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
)

// TestBinaryDefaultRunsThePoolCoreBehindTheTimeoutRule proves the default config
// wires the pool core behind the adaptive-LIFO timeout admission queue.
func TestBinaryDefaultRunsThePoolCoreBehindTheTimeoutRule(t *testing.T) {
	f, err := parseEdited(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool == nil || cfg.Admission.Rule != admission.RuleTimeout || cfg.Admission.LIFO != admission.LIFOAdaptive {
		t.Fatalf("default engine: pool core %v, admission rule %v lifo %v", cfg.Pool != nil, cfg.Admission.Rule, cfg.Admission.LIFO)
	}
	e, err := proxy.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.PoolSnapshot(); !ok {
		t.Fatal("the default engine has no pool core")
	}
	if e.AdmissionSnapshot().Capacity == 0 {
		t.Fatal("the default engine has no admission queue")
	}
}

// TestPoolAndQueueKeysAreValidatedTogether proves File.validate joins errors
// from both pool and admission-queue keys in one pass.
func TestPoolAndQueueKeysAreValidatedTogether(t *testing.T) {
	_, err := parseEdited(t, map[string]any{"queue.rule": "fifo", "pool.max_connecting": 257, "queue.interval": "2m", "breaker.failures": 1001})
	if err == nil {
		t.Fatal("accepted bad pool and queue keys")
	}
	for _, key := range []string{"queue.rule:", "queue.interval:", "pool.max_connecting", "breaker.failures"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the joined error does not name %s:\n%v", key, err)
		}
	}
}

// TestRetryAndFairnessKeysAreValidatedTogether proves File.Validate reports
// bad retry, fairness, and queue keys together in one joined error.
func TestRetryAndFairnessKeysAreValidatedTogether(t *testing.T) {
	_, err := parseEdited(t, map[string]any{"retry.policy": "everything", "retry.bucket_cap": -1, "fairness.default_weight": -2, "queue.rule": "fifo"})
	if err == nil {
		t.Fatal("accepted bad retry and fairness keys")
	}
	for _, key := range []string{"retry.policy:", "retry.bucket_cap:", "fairness.default_weight:", "queue.rule:"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the joined error does not name %s:\n%v", key, err)
		}
	}
}

// TestBinaryDefaultRunsTheFairDispatcherWithTheRetryPolicyOff proves the
// default binary config runs the fair dispatcher with retries off, and that
// switching retry.policy to pre-forward-once runs both together.
func TestBinaryDefaultRunsTheFairDispatcherWithTheRetryPolicyOff(t *testing.T) {
	f, err := parseEdited(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	e, err := proxy.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fs := e.AdmissionSnapshot().Fair; !fs.On || fs.Limit != int64(cfg.Connections) {
		t.Fatalf("default engine: fair dispatcher %v, limit %d for %d connections", fs.On, fs.Limit, cfg.Connections)
	}
	if s, ok := e.RetrySnapshot(); !ok || cfg.Retry.Kind != retry.KindOff || s.Retries != 0 {
		t.Fatalf("default engine: retry core %v, kind %v", ok, cfg.Retry.Kind)
	}
	on, err := parseEdited(t, map[string]any{"retry.policy": "pre-forward-once"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = on.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	if e, err = proxy.New(cfg); err != nil || !e.AdmissionSnapshot().Fair.On || cfg.Retry.Kind != retry.KindPreForwardOnce {
		t.Fatalf("retry on: %v, fair %v, kind %v", err, e != nil && e.AdmissionSnapshot().Fair.On, cfg.Retry.Kind)
	}
}
