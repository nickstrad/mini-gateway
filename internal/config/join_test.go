package config_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
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
