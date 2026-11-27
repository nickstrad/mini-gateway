// Package loadgen implements the deterministic, sans-I/O scheduling and
// accounting core of the open-loop load generator.
//
// A Plan maps a seed, rate, duration, and profile to exact intended send times
// and SQL operations without touching clocks or sockets. A Tracker advances
// along that schedule, tracks per-connection pipelined slots, measures latency
// from both intended and actual send times, and enforces strict conservation
// across every exit path.
package loadgen

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
)

// Plan bounds and statement-generation constants. Caps prevent runaway
// allocation or int64 overflow in intended-time arithmetic.
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

// ErrPlan is wrapped by validation errors for invalid load plans or profile names.
var ErrPlan = errors.New("loadgen: invalid plan")

// Profile selects the weighted mix of SQL operation kinds drawn by a Plan.
type Profile uint8

// Supported load profiles.
const (
	ProfileSimple Profile = iota
	ProfileErrors
)

// String returns the CLI name of the profile ("simple" or "errors").
func (p Profile) String() string {
	if p == ProfileErrors {
		return "errors"
	}
	return "simple"
}

// ParseProfile parses a profile flag value; the empty string defaults to ProfileSimple.
func ParseProfile(s string) (Profile, error) {
	switch s {
	case "simple", "":
		return ProfileSimple, nil
	case "errors":
		return ProfileErrors, nil
	}
	return 0, fmt.Errorf("%w: profile %q (want simple or errors)", ErrPlan, s)
}

// Kind identifies the category of SQL statement generated for an operation.
type Kind uint8

// Operation kinds across the simple and errors profiles.
const (
	KindRead Kind = iota
	KindWrite
	KindUnique
	KindMissing
	KindDivZero
	KindRefused
	kindCount
)

// String returns a human-readable label for the operation kind.
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

// ExpectedClass returns the two-character SQLSTATE class expected for error
// kinds, or the empty string for normal reads and writes.
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

// Kinds returns the operation kinds that can be drawn under p.
func (p Profile) Kinds() []Kind {
	var ks []Kind
	for _, w := range p.mix() {
		ks = append(ks, w.kind)
	}
	return ks
}

// Plan defines a deterministic open-loop sequence of requests at a constant rate.
type Plan struct {
	Seed       int64
	Rate       int64
	DurationNs int64
	Tenants    int
	Keys       int64
	Profile    Profile
}

// Validate checks every field bound and refuses plans that schedule zero operations.
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
	case p.Count() < 1:
		return fmt.Errorf("%w: -rate %d/s over -duration %dns plans no operation (raise -rate or -duration)", ErrPlan, p.Rate, p.DurationNs)
	}
	return nil
}

// Count returns the total number of requests scheduled over DurationNs at Rate.
// Splitting whole seconds from the remainder avoids int64 overflow up to MaxRate * MaxDurationNs.
func (p Plan) Count() int64 {
	whole, part := p.DurationNs/NsPerSecond, p.DurationNs%NsPerSecond
	return whole*p.Rate + part*p.Rate/NsPerSecond
}

// At returns the intended send time in nanoseconds from run start for operation index i.
// Whole seconds and remainder are computed separately so i * NsPerSecond cannot overflow.
func (p Plan) At(i int64) int64 {
	return (i/p.Rate)*NsPerSecond + (i%p.Rate)*NsPerSecond/p.Rate
}

// Due returns the number of operations whose intended send time is at or before now.
// An operation whose At(i) equals now is due immediately (time passes before the event).
func (p Plan) Due(now int64) int64 {
	if now < 0 {
		return 0
	}
	whole, part := now/NsPerSecond, now%NsPerSecond
	n := whole*p.Rate + part*p.Rate/NsPerSecond + 1
	// Correct for integer truncation in the estimate so At(n-1) <= now < At(n) holds exactly.
	for n > 0 && p.At(n-1) > now {
		n--
	}
	for p.At(n) <= now && n < p.Count() {
		n++
	}
	return min(n, p.Count())
}

// Op is a single deterministic request produced by Plan.Op.
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

// next advances a SplitMix64 generator, giving an independent deterministic stream per op index.
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

// Op returns operation i as a pure function of Seed, Tenants, Keys, Profile, and i.
// Rate and DurationNs affect only Op.At, never the chosen tenant, kind, key, or payload.
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

// KeyName formats numeric key k as "k<k>", matching the kv table used by fakepg and deploy/dev.
func KeyName(k int64) string { return "k" + strconv.FormatInt(k, 10) }

// statement builds SQL in the subset understood by both fakepg and real Postgres.
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

// Digest returns an FNV-64a digest over the first min(n, Count()) operations'
// intended times, tenants, and SQL strings for cross-process repeatability checks.
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

// Fingerprint returns a hex FNV-64a digest of the plan's parameters for CSV run metadata.
func (p Plan) Fingerprint() string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("seed=%d rate=%d duration=%d tenants=%d keys=%d profile=%s",
		p.Seed, p.Rate, p.DurationNs, p.Tenants, p.Keys, p.Profile)))
	return strconv.FormatUint(h.Sum64(), 16)
}

// TenantUser returns the startup user name for tenant index i under prefix.
func TenantUser(prefix string, i int) string { return prefix + strconv.Itoa(i) }

// ValidTenantPrefix rejects empty prefixes or prefixes that could exceed PostgreSQL's 63-byte identifier budget.
func ValidTenantPrefix(s string) error {
	if s == "" || len(s) > maxTenantName-4 {
		return fmt.Errorf("%w: tenant prefix %q", ErrPlan, s)
	}
	return nil
}
