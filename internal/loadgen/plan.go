package loadgen

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
)

const (
	NsPerSecond    = int64(1_000_000_000)
	MaxRate        = int64(1_000_000)
	MaxDurationNs  = 24 * 3600 * NsPerSecond
	MaxTenants     = 1024
	MaxConns       = 4096
	MaxKeys        = int64(1_000_000_000)
	DefaultKeys    = int64(1000)
	MinPayload     = 8
	MaxPayload     = 64
	maxTenantName  = 64
	hexDigits      = "0123456789abcdef"
	permille       = 1000
	missingTable   = "mg_load_missing"
	listenChannel  = "mg_load"
	duplicateKey   = 1
	divisorConst   = 1
	splitmixGolden = 0x9E3779B97F4A7C15
)

var ErrPlan = errors.New("loadgen: invalid plan")

type Profile uint8

const (
	ProfileSimple Profile = iota
	ProfileErrors
)

func (p Profile) String() string {
	if p == ProfileErrors {
		return "errors"
	}
	return "simple"
}

func ParseProfile(s string) (Profile, error) {
	switch s {
	case "simple", "":
		return ProfileSimple, nil
	case "errors":
		return ProfileErrors, nil
	}
	return 0, fmt.Errorf("%w: profile %q (want simple or errors)", ErrPlan, s)
}

type Kind uint8

const (
	KindRead Kind = iota
	KindWrite
	KindUnique
	KindMissing
	KindDivZero
	KindRefused
	kindCount
)

func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	case KindUnique:
		return "unique-violation"
	case KindMissing:
		return "missing-relation"
	case KindDivZero:
		return "division-by-zero"
	case KindRefused:
		return "proxy-refusal"
	}
	return "unknown"
}

func (k Kind) ExpectedClass() string {
	switch k {
	case KindUnique:
		return "23"
	case KindMissing:
		return "42"
	case KindDivZero:
		return "22"
	case KindRefused:
		return "0A"
	}
	return ""
}

type weight struct {
	kind Kind
	per  int64
}

var simpleMix = []weight{{KindRead, 800}, {KindWrite, 200}}

var errorsMix = []weight{{KindRead, 500}, {KindWrite, 200}, {KindUnique, 80}, {KindMissing, 80},
	{KindDivZero, 70}, {KindRefused, 70}}

func (p Profile) mix() []weight {
	if p == ProfileErrors {
		return errorsMix
	}
	return simpleMix
}

func (p Profile) Kinds() []Kind {
	var ks []Kind
	for _, w := range p.mix() {
		ks = append(ks, w.kind)
	}
	return ks
}

type Plan struct {
	Seed       int64
	Rate       int64
	DurationNs int64
	Tenants    int
	Keys       int64
	Profile    Profile
}

func (p Plan) Validate() error {
	switch {
	case p.Rate < 1 || p.Rate > MaxRate:
		return fmt.Errorf("%w: rate %d (want 1 to %d per second)", ErrPlan, p.Rate, MaxRate)
	case p.DurationNs < 1 || p.DurationNs > MaxDurationNs:
		return fmt.Errorf("%w: duration %dns (want above 0 and at most 24h)", ErrPlan, p.DurationNs)
	case p.Tenants < 1 || p.Tenants > MaxTenants:
		return fmt.Errorf("%w: tenants %d (want 1 to %d)", ErrPlan, p.Tenants, MaxTenants)
	case p.Keys < 1 || p.Keys > MaxKeys:
		return fmt.Errorf("%w: keys %d (want 1 to %d)", ErrPlan, p.Keys, MaxKeys)
	case p.Profile > ProfileErrors:
		return fmt.Errorf("%w: profile %d", ErrPlan, p.Profile)
	}
	return nil
}

func (p Plan) Count() int64 {
	whole, part := p.DurationNs/NsPerSecond, p.DurationNs%NsPerSecond
	return whole*p.Rate + part*p.Rate/NsPerSecond
}

func (p Plan) At(i int64) int64 {
	return (i/p.Rate)*NsPerSecond + (i%p.Rate)*NsPerSecond/p.Rate
}

func (p Plan) Due(now int64) int64 {
	if now < 0 {
		return 0
	}
	whole, part := now/NsPerSecond, now%NsPerSecond
	n := whole*p.Rate + part*p.Rate/NsPerSecond + 1
	for n > 0 && p.At(n-1) > now {
		n--
	}
	for p.At(n) <= now && n < p.Count() {
		n++
	}
	return min(n, p.Count())
}

type Op struct {
	Index   int64
	At      int64
	Tenant  int
	Kind    Kind
	Key     int64
	Payload string
	SQL     string
}

type draws struct {
	state uint64
}

func (d *draws) next() uint64 {
	d.state += splitmixGolden
	z := d.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (d *draws) below(n int64) int64 {
	return int64(d.next() % uint64(n))
}

func (p Plan) stream(i int64) draws {
	d := draws{state: uint64(p.Seed)}
	d.state = d.next() ^ uint64(i)*splitmixGolden
	return d
}

func (p Plan) Op(i int64) Op {
	d := p.stream(i)
	op := Op{Index: i, At: p.At(i), Tenant: int(d.below(int64(p.Tenants))), Key: 1 + d.below(p.Keys)}
	op.Kind = pick(p.Profile.mix(), d.below(permille))
	size := MinPayload + int(d.below(MaxPayload-MinPayload+1))
	b := make([]byte, size)
	for j := range b {
		b[j] = hexDigits[d.below(int64(len(hexDigits)))]
	}
	op.Payload = string(b)
	op.SQL = statement(op)
	return op
}

func pick(mix []weight, roll int64) Kind {
	for _, w := range mix {
		if roll < w.per {
			return w.kind
		}
		roll -= w.per
	}
	return mix[len(mix)-1].kind
}

func KeyName(k int64) string { return "k" + strconv.FormatInt(k, 10) }

func statement(op Op) string {
	key := "'" + KeyName(op.Key) + "'"
	switch op.Kind {
	case KindWrite:
		return "UPDATE kv SET v = '" + op.Payload + "' WHERE k = " + key
	case KindUnique:
		return "INSERT INTO kv VALUES ('" + KeyName(duplicateKey) + "', '" + op.Payload + "')"
	case KindMissing:
		return "SELECT v FROM " + missingTable + " WHERE k = " + key
	case KindDivZero:
		return "SELECT " + strconv.Itoa(divisorConst) + " / 0"
	case KindRefused:
		return "LISTEN " + listenChannel
	}
	return "SELECT v FROM kv WHERE k = " + key
}

func (p Plan) Digest(n int64) uint64 {
	h := fnv.New64a()
	var buf []byte
	for i := int64(0); i < n && i < p.Count(); i++ {
		op := p.Op(i)
		buf = strconv.AppendInt(buf[:0], op.At, 10)
		buf = append(buf, ' ')
		buf = strconv.AppendInt(buf, int64(op.Tenant), 10)
		buf = append(buf, ' ')
		buf = append(buf, op.SQL...)
		buf = append(buf, '\n')
		_, _ = h.Write(buf)
	}
	return h.Sum64()
}

func (p Plan) Fingerprint() string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("seed=%d rate=%d duration=%d tenants=%d keys=%d profile=%s",
		p.Seed, p.Rate, p.DurationNs, p.Tenants, p.Keys, p.Profile)))
	return strconv.FormatUint(h.Sum64(), 16)
}

func TenantUser(prefix string, i int) string { return prefix + strconv.Itoa(i) }

func ValidTenantPrefix(s string) error {
	if s == "" || len(s) > maxTenantName-4 {
		return fmt.Errorf("%w: tenant prefix %q", ErrPlan, s)
	}
	return nil
}
