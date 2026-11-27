package pgwire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func verifierWith(t *testing.T, saltLen, iterations int) Verifier {
	t.Helper()
	v, err := NewVerifier("pw", bytes.Repeat([]byte("s"), saltLen), iterations)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestNewTenantTableRefusesAnOversizedSalt proves salts exceeding maxMockSaltLen are refused.
func TestNewTenantTableRefusesAnOversizedSalt(t *testing.T) {
	ok := Tenant{User: "alice", Verifier: verifierWith(t, maxMockSaltLen, 32)}
	if _, err := NewTenantTable([]Tenant{ok}); err != nil {
		t.Fatalf("a salt of exactly the cap must be accepted: %v", err)
	}
	big := Tenant{User: "alice", Verifier: verifierWith(t, maxMockSaltLen+1, 32)}
	if _, err := NewTenantTable([]Tenant{big}); !errors.Is(err, ErrTenants) || !strings.Contains(err.Error(), "salt") {
		t.Fatalf("an oversized salt must be refused with ErrTenants naming the salt: %v", err)
	}
}

// TestNewTenantTableRefusesMixedShapes proves a tenant table rejects mixed SCRAM iteration counts or salt lengths.
func TestNewTenantTableRefusesMixedShapes(t *testing.T) {
	a := Tenant{User: "alice", Verifier: verifierWith(t, 20, 32)}
	cases := []struct {
		name string
		b    Tenant
		want string
	}{
		{"iterations", Tenant{User: "bob", Verifier: verifierWith(t, 20, 64)}, "iteration"},
		{"salt length", Tenant{User: "bob", Verifier: verifierWith(t, 21, 32)}, "salt"},
	}
	for _, c := range cases {
		if _, err := NewTenantTable([]Tenant{a, c.b}); !errors.Is(err, ErrTenants) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: a mixed table must be refused with ErrTenants naming it: %v", c.name, err)
		}
	}
	same := Tenant{User: "bob", Verifier: verifierWith(t, 20, 32)}
	if _, err := NewTenantTable([]Tenant{a, same}); err != nil {
		t.Fatalf("a uniform table must be accepted: %v", err)
	}
}

// TestMockShapeOfAUniformTableIsTheTablesShape proves a valid table's mock challenge shape matches its uniform verifiers.
func TestMockShapeOfAUniformTableIsTheTablesShape(t *testing.T) {
	a := Tenant{User: "alice", Verifier: verifierWith(t, 20, 32)}
	b := Tenant{User: "bob", Verifier: verifierWith(t, 20, 32)}
	table, err := NewTenantTable([]Tenant{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if it, sl := table.mockShape(); it != 32 || sl != 20 {
		t.Fatalf("mock shape %d iterations, %d salt bytes", it, sl)
	}
}
