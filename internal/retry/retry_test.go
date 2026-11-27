package retry

import (
	"errors"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	plantEnv          = "MG_FUZZ_PLANT"
	plantWithdraw     = "retry-withdraw-first"
	fuzzMaxOps        = 256
	fuzzOpBytes       = 3
	fuzzMaxStep       = 255 * Millisecond
	Millisecond       = Time(1_000_000)
	propertyRuns      = 200
	shortPropertyRuns = 30
	propertyOps       = 400
	smallCapTokens    = 3
)

func smallConfig(kind Kind) Config {
	return Config{Kind: kind, CapTokens: smallCapTokens, FloorTokens: 1}
}

func eligible(now Time) Failure {
	return Failure{Reached: work.No, Deadline: now + Millisecond}
}

func randomOps(r *rand.Rand, n int, failShare int) []op {
	ops := make([]op, n)
	for i := range ops {
		o := op{dt: Time(r.IntN(3)) * Millisecond}
		if r.IntN(100) < failShare {
			o.kind = opFail
			o.f = Failure{Reached: work.Tri(r.IntN(4)), InTx: r.IntN(8) == 0, Attempt: r.IntN(3) / 2,
				BreakerOpen: r.IntN(10) == 0, Draining: r.IntN(20) == 0}
			o.ttl = Time(r.IntN(3)) * Millisecond
		}
		ops[i] = o
	}
	return ops
}

func TestPropertyBudgetAgreesWithTheReferenceModel(t *testing.T) {
	runs := propertyRuns
	if testing.Short() {
		runs = shortPropertyRuns
	}
	for _, cfg := range []Config{smallConfig(KindPreForwardOnce), smallConfig(KindOff), {Kind: KindPreForwardOnce}} {
		for seed := range runs {
			for _, share := range []int{10, 50, 100} {
				r := rand.New(rand.NewPCG(uint64(seed), uint64(share)))
				if trail, err := runOps(cfg, randomOps(r, propertyOps, share)); err != nil {
					t.Fatalf("kind %s cap %d seed %d share %d: %v\n%s", cfg.Kind, cfg.CapTokens, seed, share, err, trailText(trail))
				}
			}
		}
	}
}

func allEligibleFailures(n int) []op {
	ops := make([]op, n)
	for i := range ops {
		ops[i] = op{kind: opFail, f: Failure{Reached: work.No}, ttl: Millisecond}
	}
	return ops
}

func TestPathologicalSignalEveryRequestFailingPreForwardStaysUnderTheBound(t *testing.T) {
	cfg := Config{Kind: KindPreForwardOnce, CapTokens: 50, FloorTokens: 5}
	p, _ := New(cfg)
	const attempts = 10_000
	for range attempts {
		p.Attempt(0)
		p.Decide(0, eligible(0))
	}
	s := p.Snapshot()
	if limit := int64(attempts/10 + 5); s.Retries > limit || s.Retries < limit-1 {
		t.Fatalf("%d retries for %d first attempts all failing pre-forward, want about %d", s.Retries, attempts, limit)
	}
	if s.Refused[RefusedBudget] == 0 {
		t.Fatal("no exhaustion counted")
	}
	if trail, err := runOps(cfg, append(attemptsThenFailures(attempts/10, 0), allEligibleFailures(2000)...)); err != nil {
		t.Fatalf("%v\n%s", err, trailText(trail))
	}
}

func attemptsThenFailures(attempts, failures int) []op {
	ops := make([]op, 0, attempts+failures)
	for range attempts {
		ops = append(ops, op{kind: opAttempt})
	}
	return append(ops, allEligibleFailures(failures)...)
}

func TestAQuietHourCannotBankAStorm(t *testing.T) {
	cfg := Config{Kind: KindPreForwardOnce, CapTokens: 20, FloorTokens: 2}
	p, _ := New(cfg)
	for range 1_000_000 {
		p.Attempt(0)
	}
	s := p.Snapshot()
	if s.Level.Value != 200 || s.Expired != 1_000_000-180 {
		t.Fatalf("after a quiet million attempts level %d expired %d, want 200 and %d", s.Level.Value, s.Expired, 1_000_000-180)
	}
	granted := 0
	for range 1000 {
		if p.Decide(0, eligible(0)).Retry {
			granted++
		}
	}
	if granted != 20 {
		t.Fatalf("a storm after a quiet period got %d retries, want the cap 20", granted)
	}
	if err := p.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
}

func TestBucketHoldingExactlyOneTokenGrantsOneRetry(t *testing.T) {
	p, _ := New(Config{Kind: KindPreForwardOnce, CapTokens: 5, FloorTokens: 1})
	if s := p.Snapshot(); s.Level.Value != 10 {
		t.Fatalf("start level %d, want 10", s.Level.Value)
	}
	if d := p.Decide(0, eligible(0)); !d.Retry {
		t.Fatalf("one token held: %+v", d)
	}
	if d := p.Decide(0, eligible(0)); d.Retry || d.Reason != RefusedBudget {
		t.Fatalf("empty bucket: %+v", d)
	}
	for range 9 {
		p.Attempt(0)
	}
	if d := p.Decide(0, eligible(0)); d.Reason != RefusedBudget {
		t.Fatalf("nine tenths: %+v", d)
	}
	p.Attempt(0)
	if d := p.Decide(0, eligible(0)); !d.Retry {
		t.Fatalf("exactly ten tenths: %+v", d)
	}
	if s := p.Snapshot(); s.Level.Value != 0 || s.Refused[RefusedBudget] != 2 {
		t.Fatalf("level %d exhaustions %d", s.Level.Value, s.Refused[RefusedBudget])
	}
}

func TestBucketExactlyAtTheCap(t *testing.T) {
	p, _ := New(Config{Kind: KindPreForwardOnce, CapTokens: 2, FloorTokens: 1})
	for range 10 {
		p.Attempt(0)
	}
	if s := p.Snapshot(); s.Level.Value != 20 || s.Expired != 0 {
		t.Fatalf("level %d expired %d, want exactly the cap 20 and nothing lost", s.Level.Value, s.Expired)
	}
	p.Attempt(0)
	if s := p.Snapshot(); s.Level.Value != 20 || s.Expired != 1 || s.Deposits != 21 {
		t.Fatalf("one past the cap: level %d expired %d deposits %d", s.Level.Value, s.Expired, s.Deposits)
	}
}

func TestEveryRefusalReasonIsReachedAndCounted(t *testing.T) {
	p, _ := New(Config{Kind: KindPreForwardOnce, CapTokens: 1, FloorTokens: 1})
	cases := []struct {
		f    Failure
		want Reason
	}{
		{Failure{Reached: work.No, Attempt: 1, Deadline: 5}, RefusedOnce},
		{Failure{Reached: work.No, InTx: true, Deadline: 5}, RefusedInTx},
		{Failure{Reached: work.Yes, Deadline: 5}, RefusedReached},
		{Failure{Reached: work.Unknown, Deadline: 5}, RefusedReached},
		{Failure{Reached: work.No, BreakerOpen: true, Deadline: 5}, RefusedBreaker},
		{Failure{Reached: work.No, Draining: true, Deadline: 5}, RefusedDraining},
		{Failure{Reached: work.No, Deadline: 1}, RefusedDeadline},
		{Failure{Reached: work.No, Deadline: 5}, Granted},
		{Failure{Reached: work.No, Deadline: 5}, RefusedBudget},
	}
	for i, c := range cases {
		if d := p.Decide(1, c.f); d.Reason != c.want || d.Retry != (c.want == Granted) {
			t.Fatalf("case %d: %+v, want %s", i, d, c.want)
		}
	}
	s := p.Snapshot()
	for r := RefusedOnce; r < NumReasons; r++ {
		if s.Refused[r] == 0 {
			t.Fatalf("reason %s never counted", r)
		}
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestDeadlineAtEqualityRefusesAndJustBeforeGrants(t *testing.T) {
	p, _ := New(Config{Kind: KindPreForwardOnce})
	if d := p.Decide(100, Failure{Reached: work.No, Deadline: 100}); d.Reason != RefusedDeadline {
		t.Fatalf("at the deadline: %+v", d)
	}
	if d := p.Decide(99, Failure{Reached: work.No, Deadline: 100}); !d.Retry {
		t.Fatalf("one nanosecond before: %+v", d)
	}
}

func TestOffNeverRetriesAndCountsEveryOffer(t *testing.T) {
	p, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*Off); !ok {
		t.Fatalf("the zero config is %T, want Off", p)
	}
	for i := range 1000 {
		p.Attempt(Time(i))
		if d := p.Decide(Time(i), Failure{Reached: work.No, Deadline: Time(i) + 10}); d.Retry || d.Reason != RefusedOff {
			t.Fatalf("off answered %+v", d)
		}
	}
	s := p.Snapshot()
	if s.Retries != 0 || s.Refused[RefusedOff] != 1000 || s.Attempts != 1000 || s.Level.Value != 0 {
		t.Fatalf("%+v", s)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidationNamesTheKey(t *testing.T) {
	cases := []struct {
		c   Config
		key string
	}{
		{Config{Kind: NumKinds}, "retry.policy"},
		{Config{CapTokens: -1}, "retry.bucket_cap"},
		{Config{CapTokens: MaxTokens + 1}, "retry.bucket_cap"},
		{Config{FloorTokens: -1}, "retry.floor"},
		{Config{CapTokens: 5, FloorTokens: 6}, "retry.floor"},
	}
	for _, c := range cases {
		err := c.c.WithDefaults().Validate()
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), c.key) {
			t.Fatalf("%+v: %v, want an error naming %s", c.c, err, c.key)
		}
	}
	if err := (Config{}).WithDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
	if c := (Config{}).WithDefaults(); c.Kind != KindOff || c.CapTokens != DefaultCapTokens || c.FloorTokens != DefaultFloorTokens {
		t.Fatalf("defaults %+v", c)
	}
}

func TestFloorShareDividesByInstancesAndRefusesBelowOne(t *testing.T) {
	if n, err := Share(10, 3); err != nil || n != 3 {
		t.Fatalf("10 over 3: %d %v", n, err)
	}
	if _, err := Share(2, 3); err == nil || !strings.Contains(err.Error(), "retry.floor") {
		t.Fatalf("2 over 3: %v", err)
	}
	if _, err := Share(2, 0); err == nil {
		t.Fatal("zero instances accepted")
	}
}

func TestSnapshotCheckCatchesBrokenConservation(t *testing.T) {
	p, _ := New(Config{Kind: KindPreForwardOnce})
	p.Attempt(0)
	p.Decide(0, eligible(0))
	good := p.Snapshot()
	for name, edit := range map[string]func(*Snapshot){
		"offered":  func(s *Snapshot) { s.Offered++ },
		"deposits": func(s *Snapshot) { s.Deposits++ },
		"level":    func(s *Snapshot) { s.Level.Value = s.Cap + 1 },
		"withdraw": func(s *Snapshot) { s.Withdrawals += 10; s.Deposits += 10 },
		"granted":  func(s *Snapshot) { s.Refused[Granted] = 1; s.Offered++ },
	} {
		s := good
		edit(&s)
		if s.Check() == nil {
			t.Fatalf("%s: broken snapshot passed", name)
		}
	}
}

func decodeOps(b []byte) []op {
	n := min(len(b)/fuzzOpBytes, fuzzMaxOps)
	ops := make([]op, n)
	for i := range ops {
		x := b[i*fuzzOpBytes : (i+1)*fuzzOpBytes]
		o := op{dt: Time(x[1]) * Millisecond}
		if x[0]&1 == 1 {
			o.kind = opFail
			o.f = Failure{Reached: work.Tri(x[0] >> 1 & 3), InTx: x[0]&8 != 0, Attempt: int(x[0] >> 4 & 1),
				BreakerOpen: x[0]&32 != 0, Draining: x[0]&64 != 0}
			o.ttl = Time(x[2]) * Millisecond / 2
		}
		ops[i] = o
	}
	return ops
}

func fuzzPlant() Plant {
	if os.Getenv(plantEnv) == plantWithdraw {
		return PlantWithdrawFirst
	}
	return 0
}

func FuzzRetryOps(f *testing.F) {
	for _, s := range [][]byte{{}, {0, 1, 0, 1, 0, 2}, {0, 0, 0, 0, 0, 0, 3, 0, 1, 1, 0, 1}, {2, 0, 0, 0, 0, 0}} {
		f.Add(s)
	}
	plant := fuzzPlant()
	f.Fuzz(func(t *testing.T, b []byte) {
		ops := decodeOps(b)
		for _, cfg := range []Config{smallConfig(KindPreForwardOnce), smallConfig(KindOff)} {
			cfg.Plant = plant
			if trail, err := runOps(cfg, ops); err != nil {
				t.Fatalf("kind %s: %v\n%s", cfg.Kind, err, trailText(trail))
			}
		}
	})
}

func TestFuzzDecodingIsTotalBoundedAndLocal(t *testing.T) {
	long := make([]byte, 10_000)
	for i := range long {
		long[i] = byte(i*37 + 11)
	}
	ops := decodeOps(long)
	if len(ops) != fuzzMaxOps {
		t.Fatalf("%d ops from a long input, cap %d", len(ops), fuzzMaxOps)
	}
	for _, o := range ops {
		if o.kind >= numOpKinds || o.dt < 0 || o.dt > fuzzMaxStep || o.f.Reached > work.Unknown+1 || o.f.Attempt > 1 {
			t.Fatalf("op out of range: %v", o)
		}
	}
	for _, b := range [][]byte{nil, {1}, {1, 2}} {
		if len(decodeOps(b)) != 0 {
			t.Fatalf("%v decoded to operations", b)
		}
	}
	a, mut := decodeOps(long[:30]), append([]byte(nil), long[:30]...)
	mut[4]++
	b := decodeOps(mut)
	for i := range a {
		if i != 1 && a[i] != b[i] {
			t.Fatalf("changing byte 4 changed op %d", i)
		}
	}
	if _, err := runOps(smallConfig(KindPreForwardOnce), ops); err != nil {
		t.Fatal(err)
	}
}

func TestFuzzPlantIsFoundByTheReferenceModel(t *testing.T) {
	cfg := smallConfig(KindPreForwardOnce)
	cfg.Plant = PlantWithdrawFirst
	ops := []op{{kind: opFail, f: Failure{Reached: work.Yes}, ttl: Millisecond}}
	if _, err := runOps(cfg, ops); err == nil {
		t.Fatal("the planted ordering bug passed the reference model")
	}
}
