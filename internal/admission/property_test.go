package admission

import (
	"math/rand/v2"
	"strings"
	"testing"
)

const (
	fuzzMaxOps   = 64
	fuzzOpBytes  = 3
	fuzzTimeUnit = Millisecond
)

func smallConfig(rule RuleKind) Config {
	return Config{Rule: rule, Target: 5 * Millisecond, Interval: 20 * Millisecond, Capacity: 5, TenantCapacity: 3, MaxTenants: 3,
		LIFOHold: 30 * Millisecond}
}

func decodeOps(b []byte) []op {
	n := min(len(b)/fuzzOpBytes, fuzzMaxOps)
	ops := make([]op, 0, n)
	for i := range n {
		k, a, c := b[i*fuzzOpBytes], b[i*fuzzOpBytes+1], b[i*fuzzOpBytes+2]
		o := op{tenant: int(a % 4), pick: int(a >> 2)}
		switch k % 10 {
		case 0, 1, 2:
			o.kind = opPush
		case 3, 4:
			o.kind = opPop
		case 5:
			o.kind = opRemove
		case 6:
			o.kind = opExpire
		case 7:
			o.kind = opToNext
		case 8:
			o.kind = opUnpop
		default:
			o.kind = opAdvance
		}
		o.dt = Time(c%32) * fuzzTimeUnit
		if c >= 224 {
			o.dt = []Time{1, 5 * Millisecond, 20 * Millisecond, 30 * Millisecond, 320 * Millisecond, 400 * Millisecond, 0, 2}[c%8]
		}
		if o.kind != opAdvance && k >= 128 {
			o.dt = 0
		}
		ops = append(ops, o)
	}
	return ops
}

func runOps(cfg Config, ops []op) (*world, error) {
	w, err := newWorld(cfg)
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		if o.kind != opAdvance && o.dt > 0 {
			if err := w.apply(op{kind: opAdvance, dt: o.dt}); err != nil {
				return w, err
			}
		}
		if err := w.apply(o); err != nil {
			return w, err
		}
	}
	return w, w.finish()
}

func randomOps(r *rand.Rand, n int) []op {
	b := make([]byte, n*fuzzOpBytes)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return decodeOps(b)
}

func trail(w *world) string {
	if w == nil {
		return ""
	}
	start := max(0, len(w.log)-12)
	return strings.Join(w.log[start:], " ")
}

func TestCoreAgreesWithTheReferenceModelForEveryRule(t *testing.T) {
	configs := []func(RuleKind) Config{
		smallConfig,
		func(k RuleKind) Config {
			c := smallConfig(k)
			c.LIFO = LIFOOff
			return c
		},
		func(k RuleKind) Config {
			return Config{Rule: k, Target: 2 * Millisecond, Interval: 7 * Millisecond, Capacity: 8, TenantCapacity: 8, MaxTenants: 2, LIFOHold: 3 * Millisecond}
		},
		func(k RuleKind) Config {
			return Config{Rule: k, Target: 1 * Millisecond, Interval: 4 * Millisecond, Capacity: 12, TenantCapacity: 5, MaxTenants: 4, LIFOHold: 50 * Millisecond}
		},
	}
	seeds := 80
	if testing.Short() {
		seeds = 30
	}
	for _, kind := range RuleKinds() {
		for ci, mk := range configs {
			for seed := range seeds {
				r := rand.New(rand.NewPCG(uint64(seed), uint64(ci)<<8|uint64(kind)))
				w, err := runOps(mk(kind), randomOps(r, 300))
				if err != nil {
					t.Fatalf("rule %v config %d seed %d: %v\n%s", kind, ci, seed, err, trail(w))
				}
			}
		}
	}
}

func TestPropertyRunsExerciseEveryShedReasonAndLIFO(t *testing.T) {
	var shed [NumReasons]int64
	var lifoOn, served int64
	for _, kind := range RuleKinds() {
		for seed := range 40 {
			r := rand.New(rand.NewPCG(uint64(seed), uint64(kind)))
			w, err := runOps(smallConfig(kind), randomOps(r, 300))
			if err != nil {
				t.Fatal(err)
			}
			s := w.q.Snapshot()
			for i, v := range s.Shed {
				shed[i] += v
			}
			lifoOn += s.LIFOOn
			served += s.Served
		}
	}
	for r, v := range shed {
		if v == 0 && Reason(r) != ReasonUndrained {
			t.Errorf("no shed with reason %v", Reason(r))
		}
	}
	if lifoOn == 0 || served == 0 {
		t.Fatalf("lifo on %d, served %d", lifoOn, served)
	}
}
