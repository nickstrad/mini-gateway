package pgwire

import (
	"bytes"
	"testing"
)

func challengeFor(t *testing.T, cfg ClientConfig, user string) string {
	t.Helper()
	s := NewClientSession(cfg)
	s.Receive(nil, startupBytes(user, testDatabase, nil))
	acts, _ := s.Receive(nil, AppendSASLInitialResponse(nil, ScramMechanism, []byte("n,,n=,r=fixednonce")))
	var out []byte
	for _, a := range acts {
		out = append(out, a.Data...)
	}
	i := bytes.Index(out, []byte(",s="))
	if i < 0 {
		t.Fatalf("no challenge for %q: %q", user, out)
	}
	salt, iters, _ := bytes.Cut(out[i+3:], []byte(",i="))
	return itoa(len(salt)) + ":" + string(iters)
}

// TestMockChallengeTakesTheTableShapeByDefault proves unknown-user SCRAM
// challenges inherit the tenant table's modal iterations and salt length unless
// MockIterations overrides them.
func TestMockChallengeTakesTheTableShapeByDefault(t *testing.T) {
	var tenants []Tenant
	for _, u := range []string{"alice", "bob", "dave"} {
		v, err := NewVerifier(passwordOf(u), []byte("twenty-byte-salt-"+u[:3]), 32)
		if err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, Tenant{User: u, Verifier: v})
	}
	table, err := NewTenantTable(tenants)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testClientConfig(t, ClientTLSOff, 1)
	cfg.Tenants, cfg.MockIterations = table, 0
	known, unknown := challengeFor(t, cfg, "alice"), challengeFor(t, cfg, "ghost")
	if known != unknown {
		t.Fatalf("known %s, unknown %s", known, unknown)
	}
	cfg.MockIterations = 77
	if known != "28:32" {
		t.Fatalf("known challenge shape %s", known)
	}
	if got := challengeFor(t, cfg, "ghost"); got != "28:77" {
		t.Fatalf("explicit MockIterations ignored: %s", got)
	}
	if got := challengeFor(t, testClientConfigNoTenants(t), "ghost"); got != "24:4096" {
		t.Fatalf("empty table: %s", got)
	}
}

func testClientConfigNoTenants(t *testing.T) ClientConfig {
	cfg := testClientConfig(t, ClientTLSOff, 1)
	cfg.Tenants, cfg.MockIterations = nil, 0
	return cfg
}
