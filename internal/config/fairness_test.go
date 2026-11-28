package config_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
)

func weights(pairs ...any) []any {
	var out []any
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, map[string]any{"tenant": pairs[i], "weight": pairs[i+1]})
	}
	return out
}

// TestFairnessKeysFailClosedNamingTheKey proves invalid fairness weights,
// duplicates, unknown keys, and oversized tables fail closed naming the key.
func TestFairnessKeysFailClosedNamingTheKey(t *testing.T) {
	for _, c := range []struct {
		key  string
		v    any
		name string
	}{
		{"fairness.default_weight", -1, "fairness.default_weight:"},
		{"fairness.default_weight", admission.MaxWeight + 1, "fairness.default_weight:"},
		{"fairness.weights", weights("alice", -3), "fairness.weights[0].weight:"},
		{"fairness.weights", weights("alice", 2, "bob", admission.MaxWeight+1), "fairness.weights[1].weight:"},
		{"fairness.weights", weights("", 2), "fairness.weights[0].tenant:"},
		{"fairness.weights", weights("alice", 2, "alice", 3), "fairness.weights[1].tenant:"},
		{"fairness.weights", weights("alice", "two"), "weight"},
		{"fairness.limit", 3, "fairness.limit"},
	} {
		_, err := parseWith(t, c.key, c.v)
		if err == nil || !strings.Contains(err.Error(), c.name) {
			t.Errorf("%s = %v: %v, want an error naming %s", c.key, c.v, err, c.name)
		}
	}
	many := make([]any, 0, admission.MaxWeights+1)
	for i := range admission.MaxWeights + 1 {
		many = append(many, map[string]any{"tenant": "t" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + itoaTest(i), "weight": 1})
	}
	if _, err := parseWith(t, "fairness.weights", many); err == nil || !strings.Contains(err.Error(), "fairness.weights:") {
		t.Errorf("%d weights: %v", len(many), err)
	}
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

// TestFairnessZeroOrOmittedMeansTheDefault proves omitted or zero fairness
// weights resolve to the default weight and bind the limit to pool.connections.
func TestFairnessZeroOrOmittedMeansTheDefault(t *testing.T) {
	for _, v := range []any{nil, 0} {
		f, err := parseWith(t, "fairness.default_weight", v)
		if err != nil {
			t.Fatal(err)
		}
		if f.Fairness.DefaultWeight != admission.DefaultWeight {
			t.Fatalf("%v gave default weight %d", v, f.Fairness.DefaultWeight)
		}
		fc := f.ProxyCaps().FairConfig()
		if fc.DefaultWeight != admission.DefaultWeight || fc.Limit != f.Pool.Connections || fc.MaxLeases != f.Pool.Connections {
			t.Fatalf("engine fair config %+v for %d connections", fc, f.Pool.Connections)
		}
	}
	f, err := parseWith(t, "fairness.weights", weights("alice", 0, "bob", 4))
	if err != nil {
		t.Fatal(err)
	}
	fc := f.ProxyCaps().FairConfig()
	if len(fc.Weights) != 2 || fc.Weights[0].Weight != 0 || fc.Weights[1].Weight != 4 {
		t.Fatalf("weights reached the engine as %+v", fc.Weights)
	}
	fd, err := admission.NewFair(fc)
	if err != nil || fd.Config().DefaultWeight != 1 {
		t.Fatalf("a zero weight is the default weight: %v", err)
	}
}

// TestConfigNeverSetsAFairPlant proves JSON config loading can never enable a test-only fair plant.
func TestConfigNeverSetsAFairPlant(t *testing.T) {
	f, err := parseWith(t, "fairness.weights", weights("alice", 3))
	if err != nil {
		t.Fatal(err)
	}
	if p := f.ProxyCaps().Fairness.Plant; p != 0 {
		t.Fatalf("config set fair plant %d", p)
	}
}

// TestFairnessAndQueueKeysAreValidatedTogether proves File.Validate joins
// fairness errors alongside queue and pool validation errors in one pass.
func TestFairnessAndQueueKeysAreValidatedTogether(t *testing.T) {
	_, err := parseEdited(t, map[string]any{"queue.rule": "fifo", "fairness.default_weight": -2, "pool.max_connecting": 257})
	if err == nil {
		t.Fatal("accepted bad keys")
	}
	for _, key := range []string{"queue.rule:", "fairness.default_weight:", "pool.max_connecting"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the joined error does not name %s:\n%v", key, err)
		}
	}
}
