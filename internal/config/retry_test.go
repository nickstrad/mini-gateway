package config_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
)

func TestRetryDefaultIsOffAndOffNeverRetries(t *testing.T) {
	f, err := parseEdited(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Retry.Policy != "off" || f.Retry.BucketCap != retry.DefaultCapTokens || f.Retry.Floor != retry.DefaultFloorTokens {
		t.Fatalf("defaults %+v", f.Retry)
	}
	r := &fixedRandom{}
	cfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retry.Kind != retry.KindOff || cfg.Retry.Plant != 0 {
		t.Fatalf("binary retry config %+v", cfg.Retry)
	}
	e, err := proxy.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rc := proxy.Seams{}.Retry
	if rc != nil {
		t.Fatal("seams carry a retry policy")
	}
	p, ok := e.RetrySnapshot()
	if !ok || p.Kind != int64(retry.KindOff) {
		t.Fatalf("the default engine's policy is %+v", p)
	}
	pol, _ := retry.New(cfg.Retry)
	for i := range 1000 {
		pol.Attempt(retry.Time(i))
		if pol.Decide(retry.Time(i), retry.Failure{Deadline: retry.Time(i) + 1}).Retry {
			t.Fatal("the default policy retried an eligible failure")
		}
	}
}

func TestRetryKeysFailClosedNamingTheKey(t *testing.T) {
	for _, c := range []struct {
		edits map[string]any
		key   string
	}{
		{map[string]any{"retry.policy": "everything"}, "retry.policy"},
		{map[string]any{"retry.policy": "retry-everything"}, "retry.policy"},
		{map[string]any{"retry.policy": "On"}, "retry.policy"},
		{map[string]any{"retry.bucket_cap": -1}, "retry.bucket_cap"},
		{map[string]any{"retry.bucket_cap": retry.MaxTokens + 1}, "retry.bucket_cap"},
		{map[string]any{"retry.floor": -1}, "retry.floor"},
		{map[string]any{"retry.policy": "pre-forward-once", "retry.floor": 50, "retry.bucket_cap": 20}, "retry.floor"},
		{map[string]any{"retry.policy": "pre-forward-once", "retry.floor": 2, "fleet.instances": 3, "fleet.backend_connections": 12}, "retry.floor"},
	} {
		_, err := parseEdited(t, c.edits)
		if err == nil || !strings.Contains(err.Error(), c.key+":") {
			t.Errorf("%v: %v, want an error naming %s", c.edits, err, c.key)
		}
	}
}

func TestRetryZeroMeansTheDefault(t *testing.T) {
	f, err := parseEdited(t, map[string]any{"retry.bucket_cap": 0, "retry.floor": 0, "retry.policy": ""})
	if err != nil {
		t.Fatal(err)
	}
	if f.Retry.BucketCap != retry.DefaultCapTokens || f.Retry.Floor != retry.DefaultFloorTokens || f.Retry.Policy != "off" {
		t.Fatalf("%+v", f.Retry)
	}
}

func TestRetryFloorIsDividedByInstancesAndTheRatioAndCapAreNot(t *testing.T) {
	f, err := parseEdited(t, map[string]any{"retry.policy": "pre-forward-once", "retry.floor": 10, "retry.bucket_cap": 40,
		"fleet.instances": 3, "fleet.backend_connections": 12})
	if err != nil {
		t.Fatal(err)
	}
	c := f.ProxyCaps().Retry
	if c.Kind != retry.KindPreForwardOnce || c.FloorTokens != 3 || c.CapTokens != 40 {
		t.Fatalf("per-instance retry config %+v", c)
	}
	warned := false
	for _, w := range f.Warnings() {
		warned = warned || strings.Contains(w, "retry.floor 10 leaves a remainder of 1")
	}
	if !warned {
		t.Fatalf("no remainder warning: %v", f.Warnings())
	}
	p, _ := retry.New(c)
	if s := p.Snapshot(); s.Floor != 30 || s.Cap != 400 {
		t.Fatalf("policy floor %d cap %d tenths", s.Floor, s.Cap)
	}
}

func TestRetryEverythingIsUnreachableFromTheBinary(t *testing.T) {
	for k := retry.Kind(0); k < retry.NumKinds; k++ {
		if strings.Contains(k.String(), "everything") {
			t.Fatalf("policy kind %s", k)
		}
	}
	out, err := exec.Command("go", "list", "-deps", "../../cmd/...").CombinedOutput()
	if err != nil {
		t.Skipf("go list: %v %s", err, out)
	}
	if bytes.Contains(out, []byte("internal/retry/retrytest")) {
		t.Fatal("the binary links the test-only retry policies")
	}
	if !bytes.Contains(out, []byte("internal/retry\n")) {
		t.Fatalf("the binary does not link the retry core:\n%s", out)
	}
}

func TestConfigNeverSetsARetryPlant(t *testing.T) {
	for _, policy := range []string{"off", "pre-forward-once"} {
		f, err := parseEdited(t, map[string]any{"retry.policy": policy})
		if err != nil {
			t.Fatal(err)
		}
		if p := f.ProxyCaps().Retry.Plant; p != 0 {
			t.Fatalf("config set retry plant %d", p)
		}
	}
}
