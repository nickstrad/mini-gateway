package pgwire

import (
	"errors"
	"fmt"
)

const (
	// MaxTenants bounds the static tenant verifier table.
	MaxTenants     = 1 << 16
	keySecretLen   = 4
	maxProcessID   = 1<<31 - 1
	processIDRange = maxProcessID
)

// ErrTenants is wrapped by NewTenantTable on invalid or duplicate entries.
var ErrTenants = errors.New("pgwire: bad tenant table")

// BackendKey is the (ProcessID, Secret) pair sent in BackendKeyData and echoed
// back in a CancelRequest.
type BackendKey struct {
	ProcessID uint32
	Secret    [keySecretLen]byte
}

// Tenant maps a Postgres startup user name to its SCRAM-SHA-256 verifier.
type Tenant struct {
	User     string
	Verifier Verifier
}

// TenantTable is an immutable lookup table of tenant SCRAM verifiers. It never
// stores plaintext passwords.
type TenantTable struct {
	byUser        map[string]int
	list          []Tenant
	commonIters   int
	commonSaltLen int
}

// NewTenantTable validates tenants and builds a lookup table, precomputing the
// iteration count and salt length shared by every tenant for default mock challenges.
func NewTenantTable(tenants []Tenant) (*TenantTable, error) {
	if len(tenants) > MaxTenants {
		return nil, fmt.Errorf("%w: %d tenants, the limit is %d", ErrTenants, len(tenants), MaxTenants)
	}
	t := &TenantTable{byUser: make(map[string]int, len(tenants))}
	for _, tn := range tenants {
		if tn.User == "" {
			return nil, fmt.Errorf("%w: empty user name", ErrTenants)
		}
		if _, dup := t.byUser[tn.User]; dup {
			return nil, fmt.Errorf("%w: user %q twice", ErrTenants, tn.User)
		}
		if tn.Verifier.Iterations < MinScramIterations || tn.Verifier.Iterations > MaxScramIterations || len(tn.Verifier.Salt) == 0 {
			return nil, fmt.Errorf("%w: user %q has no usable verifier", ErrTenants, tn.User)
		}
		if len(tn.Verifier.Salt) > maxMockSaltLen {
			return nil, fmt.Errorf("%w: user %q has a salt of %d bytes, the limit is %d", ErrTenants, tn.User, len(tn.Verifier.Salt), maxMockSaltLen)
		}
		if len(t.list) > 0 {
			first := t.list[0]
			if tn.Verifier.Iterations != first.Verifier.Iterations {
				return nil, fmt.Errorf("%w: user %q has %d SCRAM iterations but %q has %d; a table uses one iteration count", ErrTenants, tn.User, tn.Verifier.Iterations, first.User, first.Verifier.Iterations)
			}
			if len(tn.Verifier.Salt) != len(first.Verifier.Salt) {
				return nil, fmt.Errorf("%w: user %q has a salt of %d bytes but %q has %d; a table uses one salt length", ErrTenants, tn.User, len(tn.Verifier.Salt), first.User, len(first.Verifier.Salt))
			}
		}
		t.byUser[tn.User] = len(t.list)
		t.list = append(t.list, tn)
	}
	if len(t.list) > 0 {
		t.commonIters = t.list[0].Verifier.Iterations
		t.commonSaltLen = len(t.list[0].Verifier.Salt)
	}
	return t, nil
}

func (t *TenantTable) mockShape() (iterations, saltLen int) {
	if t == nil || len(t.list) == 0 {
		return DefaultScramIterations, ScramSaltLen
	}
	return t.commonIters, t.commonSaltLen
}

// Lookup returns the tenant entry for user, if present.
func (t *TenantTable) Lookup(user string) (Tenant, bool) {
	if t == nil {
		return Tenant{}, false
	}
	i, ok := t.byUser[user]
	if !ok {
		return Tenant{}, false
	}
	return t.list[i], true
}

// Len returns the number of tenants in the table.
func (t *TenantTable) Len() int {
	if t == nil {
		return 0
	}
	return len(t.list)
}

// KeyIssuer allocates proxy-issued BackendKey values for client sessions.
// Process IDs advance sequentially from a random base modulo 2^31 - 1 (shifted
// by 1 so 0 is never issued), guaranteeing uniqueness for 2^31 - 1 sessions of
// one issuer; secrets are drawn from the injected Random source.
type KeyIssuer struct {
	rnd    Random
	base   uint32
	issued uint32
}

// NewKeyIssuer creates a KeyIssuer seeded from r. One issuer should be shared
// by all client sessions of a proxy instance.
func NewKeyIssuer(r Random) *KeyIssuer {
	return &KeyIssuer{rnd: r, base: uint32(r.Uint64() % processIDRange)}
}

// Issued returns the number of keys allocated so far by k.
func (k *KeyIssuer) Issued() uint32 { return k.issued }

// Issue returns the next BackendKey with a non-zero ProcessID and fresh secret.
func (k *KeyIssuer) Issue() BackendKey {
	pid := (k.base+k.issued)%processIDRange + 1
	k.issued++
	var key BackendKey
	key.ProcessID = pid
	v := k.rnd.Uint64()
	for i := range key.Secret {
		key.Secret[i] = byte(v >> (8 * i))
	}
	return key
}
