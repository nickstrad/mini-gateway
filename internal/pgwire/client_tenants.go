package pgwire

import (
	"errors"
	"fmt"
)

const (
	MaxTenants     = 1 << 16
	keySecretLen   = 4
	maxProcessID   = 1<<31 - 1
	processIDRange = maxProcessID
)

var ErrTenants = errors.New("pgwire: bad tenant table")

type BackendKey struct {
	ProcessID uint32
	Secret    [keySecretLen]byte
}

type Tenant struct {
	User     string
	Verifier Verifier
}

type TenantTable struct {
	byUser        map[string]int
	list          []Tenant
	commonIters   int
	commonSaltLen int
}

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
		t.byUser[tn.User] = len(t.list)
		t.list = append(t.list, tn)
	}
	t.commonIters = mostCommon(t.list, func(tn Tenant) int { return tn.Verifier.Iterations })
	t.commonSaltLen = mostCommon(t.list, func(tn Tenant) int { return len(tn.Verifier.Salt) })
	return t, nil
}

func mostCommon(list []Tenant, key func(Tenant) int) int {
	counts := make(map[int]int)
	best, bestCount := 0, 0
	for _, tn := range list {
		k := key(tn)
		counts[k]++
		if counts[k] > bestCount {
			best, bestCount = k, counts[k]
		}
	}
	return best
}

func (t *TenantTable) mockShape() (iterations, saltLen int) {
	if t == nil || len(t.list) == 0 {
		return DefaultScramIterations, ScramSaltLen
	}
	return t.commonIters, t.commonSaltLen
}

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

func (t *TenantTable) Len() int {
	if t == nil {
		return 0
	}
	return len(t.list)
}

type KeyIssuer struct {
	rnd    Random
	base   uint32
	issued uint32
}

func NewKeyIssuer(r Random) *KeyIssuer {
	return &KeyIssuer{rnd: r, base: uint32(r.Uint64() % processIDRange)}
}

func (k *KeyIssuer) Issued() uint32 { return k.issued }

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
