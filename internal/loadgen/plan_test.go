package loadgen_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/loadgen"
)

func plan(rate, durationMs int64) loadgen.Plan {
	return loadgen.Plan{Seed: 42, Rate: rate, DurationNs: durationMs * 1_000_000, Tenants: 2, Keys: 100, Profile: loadgen.ProfileErrors}
}

func TestCountAndIntendedTimesAreExactAtTheConstantRate(t *testing.T) {
	cases := []struct {
		rate, ms, want int64
	}{{1, 1000, 1}, {3, 1000, 3}, {200, 10_000, 2000}, {7, 1500, 10}, {1_000_000, 1, 1000}, {3, 999, 2}}
	for _, c := range cases {
		p := plan(c.rate, c.ms)
		if got := p.Count(); got != c.want {
			t.Errorf("rate %d duration %dms: count %d, want %d", c.rate, c.ms, got, c.want)
		}
		for i := int64(0); i < p.Count(); i++ {
			want := i * loadgen.NsPerSecond / c.rate
			if p.At(i) != want {
				t.Fatalf("rate %d: At(%d) = %d, want %d", c.rate, i, p.At(i), want)
			}
			if p.At(i) >= p.DurationNs {
				t.Fatalf("rate %d: op %d at %d is past the duration %d", c.rate, i, p.At(i), p.DurationNs)
			}
		}
	}
}

func TestIntendedTimesDoNotOverflowAtTheLimits(t *testing.T) {
	p := loadgen.Plan{Seed: 1, Rate: loadgen.MaxRate, DurationNs: loadgen.MaxDurationNs, Tenants: 1, Keys: 1}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	n := p.Count()
	if n != loadgen.MaxRate*24*3600 {
		t.Fatalf("count %d", n)
	}
	last := p.At(n - 1)
	if last <= p.At(n-2) || last >= p.DurationNs {
		t.Fatalf("last op at %d, previous %d, duration %d", last, p.At(n-2), p.DurationNs)
	}
	if p.Due(p.DurationNs) != n || p.Due(last) != n || p.Due(last-1) != n-1 {
		t.Fatalf("due at the end: %d %d %d of %d", p.Due(p.DurationNs), p.Due(last), p.Due(last-1), n)
	}
}

func TestAnOpIsDueAtExactlyItsIntendedTime(t *testing.T) {
	p := plan(3, 2000)
	for i := int64(0); i < p.Count(); i++ {
		at := p.At(i)
		if got := p.Due(at); got != i+1 {
			t.Fatalf("Due(At(%d)=%d) = %d, want %d (an op whose time equals now is due)", i, at, got, i+1)
		}
		if at > 0 {
			if got := p.Due(at - 1); got != i {
				t.Fatalf("Due(At(%d)-1) = %d, want %d", i, got, i)
			}
		}
	}
	if p.Due(-1) != 0 {
		t.Fatal("nothing is due before the start")
	}
}

func TestTheSameSeedGivesTheSameOperationsKeysAndPayloads(t *testing.T) {
	a, b := plan(500, 4000), plan(500, 4000)
	for i := int64(0); i < a.Count(); i++ {
		if a.Op(i) != b.Op(i) {
			t.Fatalf("op %d differs: %+v vs %+v", i, a.Op(i), b.Op(i))
		}
	}
	if a.Digest(a.Count()) != b.Digest(b.Count()) {
		t.Fatal("digests differ")
	}
	c := a
	c.Seed = 43
	if c.Digest(c.Count()) == a.Digest(a.Count()) {
		t.Fatal("another seed gave the same sequence")
	}
	if a.Op(17) != a.Op(17) || a.Op(1999) != b.Op(1999) {
		t.Fatal("an op is not a pure function of its index")
	}
}

func TestOpsDoNotDependOnTheRateOrDuration(t *testing.T) {
	a, b := plan(100, 1000), plan(1000, 20_000)
	for i := int64(0); i < a.Count(); i++ {
		x, y := a.Op(i), b.Op(i)
		x.At, y.At = 0, 0
		if x != y {
			t.Fatalf("op %d depends on rate: %+v vs %+v", i, x, y)
		}
	}
}

func TestEveryProfileKindIsDrawnAndUsesItsStatement(t *testing.T) {
	for _, prof := range []loadgen.Profile{loadgen.ProfileSimple, loadgen.ProfileErrors} {
		p := plan(1000, 5000)
		p.Profile = prof
		seen := map[loadgen.Kind]int{}
		tenants := map[int]int{}
		for i := int64(0); i < p.Count(); i++ {
			op := p.Op(i)
			seen[op.Kind]++
			tenants[op.Tenant]++
			if op.Key < 1 || op.Key > p.Keys || len(op.Payload) < loadgen.MinPayload || len(op.Payload) > loadgen.MaxPayload {
				t.Fatalf("op %d out of range: %+v", i, op)
			}
			if strings.ContainsAny(op.Payload, "'\\") {
				t.Fatalf("payload %q needs quoting", op.Payload)
			}
		}
		if len(seen) != len(prof.Kinds()) {
			t.Fatalf("profile %s drew %v, want every one of %v", prof, seen, prof.Kinds())
		}
		if len(tenants) != p.Tenants {
			t.Fatalf("tenants drawn %v", tenants)
		}
	}
	want := map[loadgen.Kind]string{loadgen.KindRead: "SELECT v FROM kv WHERE k = 'k", loadgen.KindWrite: "UPDATE kv SET v = '",
		loadgen.KindUnique: "INSERT INTO kv VALUES ('k1', '", loadgen.KindMissing: "SELECT v FROM mg_load_missing WHERE k = 'k",
		loadgen.KindDivZero: "SELECT 1 / 0", loadgen.KindRefused: "LISTEN mg_load"}
	p := plan(1000, 5000)
	for i := int64(0); i < p.Count(); i++ {
		op := p.Op(i)
		if !strings.HasPrefix(op.SQL, want[op.Kind]) {
			t.Fatalf("op %+v: statement does not start with %q", op, want[op.Kind])
		}
	}
}

func TestValidateRefusesOutOfRangePlans(t *testing.T) {
	good := plan(10, 1000)
	bad := []func(*loadgen.Plan){
		func(p *loadgen.Plan) { p.Rate = 0 }, func(p *loadgen.Plan) { p.Rate = loadgen.MaxRate + 1 },
		func(p *loadgen.Plan) { p.DurationNs = 0 }, func(p *loadgen.Plan) { p.DurationNs = loadgen.MaxDurationNs + 1 },
		func(p *loadgen.Plan) { p.Tenants = 0 }, func(p *loadgen.Plan) { p.Tenants = loadgen.MaxTenants + 1 },
		func(p *loadgen.Plan) { p.Keys = 0 }, func(p *loadgen.Plan) { p.Profile = 7 },
	}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for i, f := range bad {
		p := good
		f(&p)
		if err := p.Validate(); !errors.Is(err, loadgen.ErrPlan) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if _, err := loadgen.ParseProfile("bogus"); err == nil {
		t.Fatal("bogus profile accepted")
	}
}
