package pool

import (
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	propertyRuns = 3000
	propertySeed = 11
	plantEnv     = "MG_FUZZ_PLANT"
)

func randomOps(r *rand.Rand) []op {
	n := 8 + r.IntN(maxOps-8)
	ops := make([]op, n)
	for i := range ops {
		ops[i] = op{kind: opKind(r.IntN(int(numOpKinds))), a: byte(r.IntN(256)), b: byte(r.IntN(256))}
		switch r.IntN(6) {
		case 0, 1:
			ops[i].kind = opCheckout
		case 2:
			ops[i].kind = opAdvance
		}
	}
	return ops
}

func configs() map[string]Config {
	always := modelConfig()
	onSwitch := modelConfig()
	onSwitch.Reset = ResetOnSwitch
	reseed := modelConfig()
	reseed.Reseed = true
	both := onSwitch
	both.Reseed = true
	return map[string]Config{"always": always, "on-switch": onSwitch, "always-reseed": reseed, "on-switch-reseed": both}
}

func runs() int {
	if testing.Short() {
		return propertyRuns / 6
	}
	return propertyRuns
}

func firstViolation(cfg Config, runs int) (runResult, bool) {
	r := rand.New(rand.NewPCG(propertySeed, 1))
	for i := 0; i < runs; i++ {
		res := runOps(cfg, randomOps(r))
		if res.violation != "" {
			return res, true
		}
	}
	return runResult{}, false
}

func TestPoolMatchesTheReferenceModelOnRandomOperations(t *testing.T) {
	for _, name := range []string{"always", "on-switch", "always-reseed", "on-switch-reseed"} {
		cfg := configs()[name]
		t.Run(name, func(t *testing.T) {
			if res, bad := firstViolation(cfg, runs()); bad {
				t.Fatalf("violation: %s\nREPLAY: ops %s", res.violation, res)
			}
		})
	}
}

func TestOperationsExerciseEveryMechanism(t *testing.T) {
	r := rand.New(rand.NewPCG(propertySeed, 1))
	var total Snapshot
	for i := 0; i < runs(); i++ {
		m, err := newModel(modelConfig())
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range randomOps(r) {
			m.apply(o)
			m.advance(time.Millisecond)
		}
		m.finish()
		s := m.p.Snapshot()
		total.Dials += s.Dials
		total.CheckoutsLent += s.CheckoutsLent
		total.Checks += s.Checks
		total.ChecksByResult[CheckTimeout] += s.ChecksByResult[CheckTimeout]
		total.ChecksByResult[CheckOK] += s.ChecksByResult[CheckOK]
		total.Cancels += s.Cancels
		total.Rollbacks += s.Rollbacks
		total.BreakerTransitions[TransClosedOpen] += s.BreakerTransitions[TransClosedOpen]
		total.BreakerTransitions[TransHalfOpenClosed] += s.BreakerTransitions[TransHalfOpenClosed]
		total.RateLimited += s.RateLimited
		total.Exhausted += s.Exhausted
		total.NoReply += s.NoReply
		total.DirtyReused[EndVanishedMidStatement] += s.DirtyReused[EndVanishedMidStatement]
		total.Closes[CloseIdle] += s.Closes[CloseIdle]
		total.Closes[CloseCheckFailed] += s.Closes[CloseCheckFailed]
	}
	for name, v := range map[string]int64{"dials": total.Dials, "leases": total.CheckoutsLent, "checks": total.Checks,
		"check timeouts": total.ChecksByResult[CheckTimeout], "checks ok": total.ChecksByResult[CheckOK], "cancels": total.Cancels,
		"rollbacks": total.Rollbacks, "breaker opens": total.BreakerTransitions[TransClosedOpen],
		"breaker recoveries": total.BreakerTransitions[TransHalfOpenClosed], "rate limited": total.RateLimited,
		"exhausted": total.Exhausted, "no reply": total.NoReply, "dirty reused": total.DirtyReused[EndVanishedMidStatement],
		"idle closes": total.Closes[CloseIdle], "check_failed closes": total.Closes[CloseCheckFailed]} {
		if v == 0 {
			t.Errorf("the random operations never exercised %s", name)
		}
	}
}

func TestEveryPlantedBugIsCaughtByTheModel(t *testing.T) {
	cases := []struct {
		name  string
		plant Plant
		reset ResetMode
	}{{"cap", PlantNoCap, ResetAlways}, {"creation-rate", PlantNoRate, ResetAlways},
		{"check-deadline", PlantNoCheckDeadline, ResetAlways}, {"suspect", PlantNoSuspect, ResetAlways},
		{"on-switch-tag", PlantIgnoreTag, ResetOnSwitch}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := modelConfig()
			cfg.Plant = c.plant
			cfg.Reset = c.reset
			res, bad := firstViolation(cfg, runs())
			if !bad {
				t.Fatalf("planted %s bug not caught in %d runs", c.name, propertyRuns)
			}
			t.Logf("caught: %s", res.violation)
		})
	}
}

var resetClearsSuspectOps = []op{{opDemand, 2, 0}, {opOpened, 0, 0}, {opAdvance, 6, 0}, {opDemand, 2, 0}, {opOpened, 0, 0},
	{opCheckout, 0, 0}, {opCheckout, 0, 0}, {opLost, 1, 0}, {opReleaseClean, 0, 1}, {opStepDone, 0, 0}, {opCheckout, 0, 0}}

func nearResetClearsSuspect() [][]op {
	steps := slices.Index(resetClearsSuspectOps, op{opStepDone, 0, 0})
	before := slices.Clone(resetClearsSuspectOps[:steps])
	lost := slices.Index(before, op{opLost, 1, 0})
	noLoss := slices.Clone(before)
	noLoss[lost].kind = opStepFailed
	return [][]op{noLoss, append(slices.Clone(noLoss), op{opStepDone, 0, 0}, op{opCheckout, 0, 0})}
}

func TestNearMissSeedsPassAndAreOneByteFromThePlantedBug(t *testing.T) {
	cfg := modelConfig()
	cfg.Plant = PlantResetClearsSuspect
	for i, near := range nearResetClearsSuspect() {
		if res := runOps(cfg, near); res.violation != "" {
			t.Fatalf("seed %d already fails: %s", i, res.violation)
		}
	}
}

func TestShrunkResetClearsSuspectOrderingBugReplays(t *testing.T) {
	cfg := modelConfig()
	if res := runOps(cfg, resetClearsSuspectOps); res.violation != "" {
		t.Fatalf("without the plant: %s", res.violation)
	}
	cfg.Plant = PlantResetClearsSuspect
	res := runOps(cfg, resetClearsSuspectOps)
	if !contains(res.violation, "suspect") {
		t.Fatalf("with the plant the shrunk operations must lend a suspect connection, got %q\nREPLAY: ops %s", res.violation, res)
	}
}

func TestReplayGivesTheSameActionsForTheSameOperations(t *testing.T) {
	r := rand.New(rand.NewPCG(propertySeed, 2))
	for i := 0; i < 200; i++ {
		ops := randomOps(r)
		a, b := runOps(modelConfig(), ops), runOps(modelConfig(), ops)
		if !slices.Equal(a.trace, b.trace) {
			t.Fatalf("two runs of the same operations differ\nREPLAY: ops %s", a)
		}
	}
}

func TestOpDecodingIsTotalBoundedAndLocal(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 3))
	for i := 0; i < 500; i++ {
		b := make([]byte, r.IntN(400))
		for j := range b {
			b[j] = byte(r.IntN(256))
		}
		ops := decodeOps(b)
		if len(ops) > maxOps || len(ops) != min(len(b)/opBytes, maxOps) {
			t.Fatalf("%d bytes decoded to %d operations", len(b), len(ops))
		}
		if len(b) >= opBytes {
			c := slices.Clone(b)
			c[0]++
			if d := decodeOps(c); !slices.Equal(d[1:], ops[1:]) {
				t.Fatal("changing the first byte changed later operations")
			}
		}
	}
	ops := []op{{opDemand, 3, 0}, {opCheckout, 1, 2}}
	if got := decodeOps(encodeOps(ops)); !slices.Equal(got, ops) {
		t.Fatalf("round trip %v != %v", got, ops)
	}
}

func TestFuzzPoolOpsCostIsBounded(t *testing.T) {
	worst := make([]byte, 0, maxOps*opBytes*2)
	for i := 0; i < 2*maxOps; i++ {
		worst = append(worst, byte(i%int(numOpKinds)), 0xff, 0xff)
	}
	ops := decodeOps(worst)
	if len(ops) != maxOps {
		t.Fatalf("%d operations, want the bound %d", len(ops), maxOps)
	}
	start := time.Now()
	const reps = 200
	for i := 0; i < reps; i++ {
		runOps(modelConfig(), ops)
	}
	if per := time.Since(start) / reps; per > 5*time.Millisecond {
		t.Fatalf("one execution took %s, want under 5ms so the target runs far above 100 per second", per)
	}
}

func fuzzPlant() Plant {
	if os.Getenv(plantEnv) == "reset-clears-suspect" {
		return PlantResetClearsSuspect
	}
	return 0
}

func FuzzPoolOps(f *testing.F) {
	r := rand.New(rand.NewPCG(propertySeed, 4))
	for i := 0; i < 8; i++ {
		f.Add(encodeOps(randomOps(r)))
	}
	for _, near := range nearResetClearsSuspect() {
		f.Add(encodeOps(near))
	}
	plant := fuzzPlant()
	f.Fuzz(func(t *testing.T, b []byte) {
		cfg := modelConfig()
		cfg.Plant = plant
		if b2 := len(b); b2 > 0 && b[0]&0x80 != 0 {
			cfg.Reset = ResetOnSwitch
		}
		res := run(cfg, decodeOps(b), false)
		if res.violation != "" {
			t.Fatalf("pool invariant: %s\nREPLAY: ops %s", res.violation, res)
		}
	})
}

func TestShareSumsToAtMostTheTotalAndIsNeverZero(t *testing.T) {
	for n := 1; n <= 64; n++ {
		for total := 0; total <= 300; total++ {
			s, err := Share(total, n)
			if total < n {
				if err == nil {
					t.Fatalf("Share(%d, %d) accepted a total smaller than the instances", total, n)
				}
				continue
			}
			if err != nil || s < 1 || s*n > total || (s+1)*n <= total {
				t.Fatalf("Share(%d, %d) = %d, %v", total, n, s, err)
			}
		}
	}
	if _, err := Share(10, 0); err == nil {
		t.Fatal("zero instances accepted")
	}
}

func TestDefaultsAndValidationNameTheKey(t *testing.T) {
	cfg := Config{}.WithDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Cap != DefaultCap || cfg.CheckTimeout != DefaultCheckTimeout || cfg.JitterPermille != DefaultJitterPermille || cfg.Reset != ResetAlways {
		t.Fatalf("defaults %+v", cfg)
	}
	for _, c := range []struct {
		key string
		cfg Config
	}{{"pool.connections", Config{Cap: -1}}, {"pool.max_connecting", Config{MaxConnecting: -2}},
		{"fleet.connect_rate_per_s", Config{RatePerSec: -1}}, {"pool.check_timeout", Config{CheckTimeout: -1}},
		{"pool.jitter_percent", Config{JitterPermille: 900}}, {"breaker.failures", Config{BreakerFailures: -1}},
		{"lease.reset", Config{Reset: 9}}, {"pool.max_cancels", Config{MaxCancels: -1}}} {
		err := c.cfg.WithDefaults().Validate()
		if err == nil || !contains(err.Error(), c.key) {
			t.Errorf("%s: %v", c.key, err)
		}
	}
	if _, err := New(Config{Cap: -1}); err == nil {
		t.Fatal("New accepted a negative cap")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func open(t *testing.T, cfg Config, n int) (*Pool, []ConnID, Instant) {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := Instant(0)
	var ids []ConnID
	for len(ids) < n {
		for _, a := range p.Demand(now, n, nil) {
			if a.Kind == ActDial {
				ids = append(ids, a.Conn)
				p.Opened(now, a.Conn, nil)
			}
		}
		now += Instant(time.Second)
	}
	return p, ids, now
}

const tenantA, tenantB = work.TenantID("a"), work.TenantID("b")

func TestLendTheMostRecentlyUsedIdleConnection(t *testing.T) {
	p, ids, now := open(t, Config{Cap: 3, RatePerSec: 100}, 3)
	var lent []ConnID
	for range 3 {
		id, ok, _ := p.Checkout(now, tenantA, nil)
		if !ok {
			t.Fatal("no lend")
		}
		lent = append(lent, id)
	}
	for i, id := range lent {
		p.Release(now+Instant(i), id, End{Kind: EndClean, Tenant: tenantA}, nil)
		p.StepDone(now+Instant(i), id, 'I', nil)
	}
	id, ok, _ := p.Checkout(now+10, tenantA, nil)
	if !ok || id != lent[2] {
		t.Fatalf("lent %d, want the most recently used %d (ids %v)", id, lent[2], ids)
	}
}

func TestCheckStartsAtExactlyCheckAfterIdleAndEndsAtItsDeadline(t *testing.T) {
	p, ids, now := open(t, Config{Cap: 1, RatePerSec: 100}, 1)
	idle := p.conns[ids[0]].lastUsed
	if _, ok, acts := p.Checkout(idle+Instant(DefaultCheckAfterIdle)-1, tenantA, nil); !ok || len(acts) != 0 {
		t.Fatalf("one nanosecond before check_after_idle: lent %v actions %v", ok, acts)
	}
	_ = now
	p2, ids2, _ := open(t, Config{Cap: 1, RatePerSec: 100}, 1)
	at := p2.conns[ids2[0]].lastUsed + Instant(DefaultCheckAfterIdle)
	_, ok, acts := p2.Checkout(at, tenantA, nil)
	if ok || len(acts) != 1 || acts[0].Kind != ActCheck {
		t.Fatalf("at check_after_idle: lent %v actions %v, want a check and no lend", ok, acts)
	}
	if got := p2.Tick(at+Instant(DefaultCheckTimeout)-1, nil); len(got) != 0 || p2.State(ids2[0]) != StateChecking {
		t.Fatalf("before the check deadline: %v %s", got, p2.State(ids2[0]))
	}
	acts = p2.Tick(at+Instant(DefaultCheckTimeout), nil)
	if p2.State(ids2[0]) != 0 || len(acts) == 0 || acts[0].Kind != ActClose || acts[0].Reason != CloseCheckFailed {
		t.Fatalf("at the check deadline: %v %s", acts, p2.State(ids2[0]))
	}
	if s := p2.Snapshot(); s.ChecksByResult[CheckTimeout] != 1 || s.Checks != 1 {
		t.Fatalf("check counters %+v", s.ChecksByResult)
	}
}

func TestOneFailureMakesEveryIdleSiblingSuspect(t *testing.T) {
	p, ids, now := open(t, Config{Cap: 3, RatePerSec: 100}, 3)
	a, _, _ := p.Checkout(now, tenantA, nil)
	p.Lost(now, a, LossClosed, nil)
	for _, id := range ids {
		if id != a && !p.Suspect(id) {
			t.Fatalf("sibling %d not suspect", id)
		}
	}
	_, ok, acts := p.Checkout(now, tenantA, nil)
	if ok || len(acts) == 0 || acts[0].Kind != ActCheck {
		t.Fatalf("a suspect was lent without a check: %v %v", ok, acts)
	}
	p.StepDone(now, acts[0].Conn, 'I', nil)
	if p.Suspect(acts[0].Conn) {
		t.Fatal("a passed check did not clear suspect")
	}
	if id, ok, _ := p.Checkout(now, tenantA, nil); !ok || id != acts[0].Conn {
		t.Fatalf("checked connection not lent: %d %v", id, ok)
	}
}

func TestZeroReplyDeadlineSetsSuspectButDoesNotCountTowardTheBreaker(t *testing.T) {
	p, _, now := open(t, Config{Cap: 3, RatePerSec: 100, BreakerFailures: 1}, 3)
	a, _, _ := p.Checkout(now, tenantA, nil)
	p.Release(now, a, End{Kind: EndStatementDeadline, Mid: true, ZeroReply: true}, nil)
	if p.Breaker() != BreakerClosed {
		t.Fatal("a lone zero-reply timeout opened the breaker")
	}
	if s := p.Snapshot(); s.NoReply != 1 || s.Suspect.Value != 3 {
		t.Fatalf("no reply %d suspect %d", s.NoReply, s.Suspect.Value)
	}
}

func TestBreakerOpensOnConnectionFailuresSendsOneTrialAndRecovers(t *testing.T) {
	p, err := New(Config{Cap: 4, RatePerSec: 1000, BreakerFailures: 2, BreakerOpenFor: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := Instant(0)
	dials := func(acts []Action) (ids []ConnID) {
		for _, a := range acts {
			if a.Kind == ActDial {
				ids = append(ids, a.Conn)
			}
		}
		return
	}
	for range 2 {
		for _, id := range dials(p.Demand(now, 2, nil)) {
			p.DialFailed(now, id, nil)
		}
		now += Instant(time.Millisecond)
	}
	if p.Breaker() != BreakerOpen || p.Allow(now) {
		t.Fatalf("breaker %s after 2 failures", p.Breaker())
	}
	if d := dials(p.Demand(now+Instant(10*time.Millisecond), 4, nil)); len(d) != 0 {
		t.Fatalf("dialled %v while open", d)
	}
	now += Instant(time.Second)
	trial := dials(p.Tick(now, nil))
	if len(trial) != 1 || p.Breaker() != BreakerHalfOpen || p.Allow(now) {
		t.Fatalf("half-open: trial %v state %s", trial, p.Breaker())
	}
	if d := dials(p.Demand(now, 4, nil)); len(d) != 0 {
		t.Fatalf("a second trial %v", d)
	}
	p.Opened(now, trial[0], nil)
	if p.Breaker() != BreakerClosed || !p.Allow(now) {
		t.Fatalf("trial passed but breaker %s", p.Breaker())
	}
	s := p.Snapshot()
	if s.BreakerTrials != 1 || s.BreakerTransitions != [NumTransitions]int64{1, 1, 1, 0} {
		t.Fatalf("trials %d transitions %v", s.BreakerTrials, s.BreakerTransitions)
	}
}

func TestHalfOpenSendsNoTrialWhileNoWorkWasRefused(t *testing.T) {
	p, err := New(Config{Cap: 2, RatePerSec: 1000, BreakerFailures: 1})
	if err != nil {
		t.Fatal(err)
	}
	acts := p.Demand(0, 1, nil)
	p.DialFailed(0, acts[0].Conn, nil)
	p.Demand(0, 0, nil)
	if acts := p.Tick(Instant(5*time.Second), nil); len(acts) != 0 || p.Breaker() != BreakerHalfOpen {
		t.Fatalf("idle half-open breaker dialled: %v %s", acts, p.Breaker())
	}
	p.Allow(Instant(5 * time.Second))
	if acts := p.Tick(Instant(5*time.Second), nil); len(acts) != 1 || acts[0].Kind != ActDial {
		t.Fatalf("a refused request did not start the trial: %v", acts)
	}
}

func TestCreationRateSpacesDialsAndConcurrentConnectsAreCapped(t *testing.T) {
	p, err := New(Config{Cap: 50, MaxConnecting: 3, RatePerSec: 10})
	if err != nil {
		t.Fatal(err)
	}
	var times []Instant
	for now := Instant(0); now < Instant(10*time.Second); now += Instant(10 * time.Millisecond) {
		for _, a := range p.Demand(now, 50, nil) {
			if a.Kind == ActDial {
				times = append(times, now)
				if s := p.Snapshot(); s.Connecting.Value > 3 {
					t.Fatalf("%d connecting", s.Connecting.Value)
				}
				p.Opened(now, a.Conn, nil)
			}
		}
	}
	for i := 1; i < len(times); i++ {
		if times[i]-times[i-1] < Instant(100*time.Millisecond) {
			t.Fatalf("dials at %d and %d", times[i-1], times[i])
		}
	}
	if len(times) < 45 {
		t.Fatalf("only %d dials in 10 s at 10 per second", len(times))
	}
}

func TestJitterSpreadsTheExpiryOfConnectionsCreatedTogether(t *testing.T) {
	p, err := New(Config{Cap: 200, RatePerSec: MaxRatePerSec, MaxConnecting: 200, Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	fill(p, 200)
	var lives []Instant
	for _, c := range p.list {
		lives = append(lives, c.lifeLimit)
		lo, hi := Instant(DefaultMaxLifetime)*9/10, Instant(DefaultMaxLifetime)*11/10
		if c.lifeLimit < lo || c.lifeLimit > hi || c.idleLimit < Instant(DefaultIdleTimeout)*9/10 || c.idleLimit > Instant(DefaultIdleTimeout)*11/10 {
			t.Fatalf("limits %d %d outside ±10%%", c.lifeLimit, c.idleLimit)
		}
	}
	slices.Sort(lives)
	window := Instant(time.Second)
	most := 0
	for i := range lives {
		j := i
		for j < len(lives) && lives[j]-lives[i] < window {
			j++
		}
		most = max(most, j-i)
	}
	if most > 6 {
		t.Fatalf("%d of 200 connections expire within one second of each other", most)
	}
	nj, _ := New(Config{Cap: 200, RatePerSec: MaxRatePerSec, MaxConnecting: 200, NoJitter: true})
	fill(nj, 200)
	if nj.list[0].lifeLimit != nj.list[199].lifeLimit {
		t.Fatal("the no-jitter arm still jitters")
	}
}

func fill(p *Pool, n int) {
	for now := Instant(0); len(p.list) < n; now += Instant(10 * time.Microsecond) {
		for _, a := range p.Demand(now, n, nil) {
			if a.Kind == ActDial {
				p.Opened(now, a.Conn, nil)
			}
		}
	}
}

func TestOnSwitchPrefersTheSameTenantAndResetsWhenItMustSwitch(t *testing.T) {
	p, _, now := open(t, Config{Cap: 2, RatePerSec: 100, Reset: ResetOnSwitch}, 2)
	a, _, _ := p.Checkout(now, tenantA, nil)
	b, _, _ := p.Checkout(now, tenantB, nil)
	p.Release(now, a, End{Kind: EndClean, Tenant: tenantA}, nil)
	p.Release(now+1, b, End{Kind: EndClean, Tenant: tenantB}, nil)
	if id, ok, _ := p.Checkout(now+2, tenantA, nil); !ok || id != a {
		t.Fatalf("tenant a got %d, want its own %d", id, a)
	}
	_, ok, acts := p.Checkout(now+3, work.TenantID("c"), nil)
	if ok || len(acts) != 1 || acts[0].Kind != ActReset || acts[0].Conn != b {
		t.Fatalf("switch: lent %v actions %v", ok, acts)
	}
	p.StepDone(now+4, b, 'I', nil)
	if id, ok, _ := p.Checkout(now+5, work.TenantID("c"), nil); !ok || id != b {
		t.Fatal("reset connection not lent to the new tenant")
	}
	if s := p.Snapshot(); s.TenantSwitches != 1 || s.LentAsIs != 1 || s.Resets[ResetAtSwitch] != 1 {
		t.Fatalf("switches %d as-is %d resets %v", s.TenantSwitches, s.LentAsIs, s.Resets)
	}
}

func TestDirtyTableCancelsDrainsRollsBackAndResetsBeforeReuse(t *testing.T) {
	p, _, now := open(t, Config{Cap: 1, RatePerSec: 100}, 1)
	a, _, _ := p.Checkout(now, tenantA, nil)
	acts := p.Release(now, a, End{Kind: EndVanishedMidStatement, Mid: true, InTx: true}, nil)
	if len(acts) != 1 || acts[0].Kind != ActCancel {
		t.Fatalf("actions %v, want a cancel", acts)
	}
	cancel := acts[0].Cancel
	if _, ok, _ := p.Checkout(now, tenantA, nil); ok {
		t.Fatal("lent during clean-up")
	}
	if acts = p.StepDone(now+1, a, 'T', nil); len(acts) != 1 || acts[0].Kind != ActRollback {
		t.Fatalf("after the drain: %v", acts)
	}
	if acts = p.StepDone(now+2, a, 'I', nil); len(acts) != 1 || acts[0].Kind != ActReset {
		t.Fatalf("after the rollback: %v", acts)
	}
	p.StepDone(now+3, a, 'I', nil)
	if p.State(a) != StateCleaning {
		t.Fatalf("idle with the cancel in flight: %s", p.State(a))
	}
	p.CancelDone(now+4, cancel, nil)
	if id, ok, _ := p.Checkout(now+5, tenantA, nil); !ok || id != a {
		t.Fatal("cleaned connection not reused")
	}
	if s := p.Snapshot(); s.DirtyReused[EndVanishedMidStatement] != 1 || s.Rollbacks != 1 || s.CancelsDone != 1 {
		t.Fatalf("dirty %v rollbacks %d cancels %d", s.DirtyReused, s.Rollbacks, s.CancelsDone)
	}
}

func TestCleanupDeadlineClosesAndNoReplyNamesTheReason(t *testing.T) {
	p, _, now := open(t, Config{Cap: 1, RatePerSec: 100}, 1)
	a, _, _ := p.Checkout(now, tenantA, nil)
	p.Release(now, a, End{Kind: EndStatementDeadline, Mid: true, ZeroReply: true}, nil)
	acts := p.Tick(now+Instant(DefaultCleanup)-1, nil)
	if p.State(a) != StateCleaning || len(acts) != 0 {
		t.Fatal("closed before the clean-up deadline")
	}
	acts = p.Tick(now+Instant(DefaultCleanup), nil)
	if len(acts) == 0 || acts[0].Reason != CloseNoReply {
		t.Fatalf("at the deadline: %v", acts)
	}
}

func TestCancelsAreCappedAndAClientCancelOnlyReachesALentConnection(t *testing.T) {
	p, _, now := open(t, Config{Cap: 3, RatePerSec: 100, MaxCancels: 1}, 3)
	a, _, _ := p.Checkout(now, tenantA, nil)
	b, _, _ := p.Checkout(now, tenantB, nil)
	if ok, acts := p.ClientCancel(now, a, nil); !ok || len(acts) != 1 || acts[0].Kind != ActCancel || acts[0].Conn != a {
		t.Fatalf("cancel: %v %v", ok, acts)
	}
	if ok, _ := p.ClientCancel(now, b, nil); ok {
		t.Fatal("a second cancel passed the cap of 1")
	}
	idle := ConnID(0)
	for _, c := range p.list {
		if c.state == StateIdle {
			idle = c.id
		}
	}
	if ok, _ := p.ClientCancel(now, idle, nil); ok {
		t.Fatal("a cancel was sent for an idle connection")
	}
	p.Release(now, a, End{Kind: EndClean, Tenant: tenantA}, nil)
	p.StepDone(now, a, 'I', nil)
	if p.State(a) != StateCleaning {
		t.Fatal("a connection with a cancel in flight became lendable")
	}
}

func TestIdleTimeoutAndLifetimeCloseOnlyIdleConnections(t *testing.T) {
	p, _, now := open(t, Config{Cap: 2, RatePerSec: 100, NoJitter: true, MaxLifetime: time.Hour, IdleTimeout: time.Minute, CheckAfterIdle: 2 * time.Hour}, 2)
	a, _, _ := p.Checkout(now, tenantA, nil)
	later := now + Instant(2*time.Hour)
	p.Tick(later, nil)
	if p.State(a) != StateLent {
		t.Fatal("a lent connection was expired mid-lease")
	}
	if s := p.Snapshot(); s.Closes[CloseIdle]+s.Closes[CloseLifetime] != 1 {
		t.Fatalf("closes %v", s.Closes)
	}
	acts := p.Release(later, a, End{Kind: EndClean, Tenant: tenantA}, nil)
	if len(acts) != 1 || acts[0].Reason != CloseLifetime {
		t.Fatalf("release past the lifetime: %v", acts)
	}
}

func TestBreakerOpeningRefusesQueuedWorkOnlyWhenNothingIsUsable(t *testing.T) {
	p, err := New(Config{Cap: 2, RatePerSec: 1000, BreakerFailures: 1})
	if err != nil {
		t.Fatal(err)
	}
	acts := p.Demand(0, 1, nil)
	acts = p.DialFailed(0, acts[0].Conn, nil)
	found := false
	for _, a := range acts {
		found = found || a.Kind == ActRefuseQueued
	}
	if !found {
		t.Fatalf("actions %v, want queued work refused", acts)
	}
}

func TestSnapshotCheckCatchesBrokenConservation(t *testing.T) {
	p, _, now := open(t, Config{Cap: 2, RatePerSec: 100}, 2)
	p.Checkout(now, tenantA, nil)
	s := p.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	for name, broken := range map[string]func(*Snapshot){"cap": func(s *Snapshot) { s.Connecting.Value = 9 },
		"checkouts": func(s *Snapshot) { s.CheckoutsRefused++ }, "dials": func(s *Snapshot) { s.Dials++ },
		"closes": func(s *Snapshot) { s.Closes[CloseIdle]++ }, "checks": func(s *Snapshot) { s.Checks++ },
		"cancels": func(s *Snapshot) { s.Cancels++ }, "leases": func(s *Snapshot) { s.Ends[EndClean]++ }} {
		c := s
		broken(&c)
		if c.Check() == nil {
			t.Errorf("%s: broken conservation not caught", name)
		}
	}
}

func TestShutdownClosesEverythingAndLeavesNoTimer(t *testing.T) {
	p, _, now := open(t, Config{Cap: 3, RatePerSec: 100}, 3)
	a, _, _ := p.Checkout(now, tenantA, nil)
	p.Release(now, a, End{Kind: EndVanishedMidStatement, Mid: true}, nil)
	p.Demand(now, 5, nil)
	acts := p.Shutdown(now, nil)
	if len(acts) < 3 || p.Next() != 0 || len(p.list) != 0 {
		t.Fatalf("shutdown: %v next %d left %d", acts, p.Next(), len(p.list))
	}
	if err := p.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	if acts := p.Demand(now+1, 5, nil); len(acts) != 0 {
		t.Fatalf("dialled after shutdown: %v", acts)
	}
}
