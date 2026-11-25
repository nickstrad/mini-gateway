package simtoy

import (
	"fmt"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// Shared timing and window constants for the named toy scenarios.
const (
	faultAt       = sim.Time(2500 * time.Microsecond)
	baseLatency   = time.Millisecond
	baseJitter    = 200 * time.Microsecond
	baseWindow    = 256
	shortRecovery = 50 * time.Millisecond
	longClear     = sim.Time(1500 * time.Millisecond)
	longRecovery  = 2 * time.Second
	// StallFor is the duration of the transient stall in echo-stall.
	StallFor = 20 * time.Millisecond
)

// Scenario describes one named end-to-end toy echo scenario, its injected
// faults, expected client outcome class, and recovery timing.
type Scenario struct {
	Name        string
	Kind        sim.FaultKind
	Expect      Outcome
	AllTargeted bool
	Ablate      bool
	faults      []sim.Fault
	recoverAt   time.Duration
	options     func(o *simnet.Options)
}

// BaseOptions returns the standard toy network configuration: a 256-byte send
// window and 1 ms ± 200 µs latency in each direction.
func BaseOptions() simnet.Options {
	o := simnet.DefaultOptions()
	o.Window = baseWindow
	o.Up.Latency = simnet.Latency{Base: baseLatency, Jitter: baseJitter}
	o.Down.Latency = simnet.Latency{Base: baseLatency, Jitter: baseJitter}
	return o
}

func connFault(kind sim.FaultKind, a1, a2 int64) sim.Fault {
	return sim.Fault{At: faultAt, Kind: kind, Target: 1, Arg1: a1, Arg2: a2}
}

func clearFault(at sim.Time, target int64, kind sim.FaultKind) sim.Fault {
	return sim.Fault{At: at, Kind: simnet.FaultClear, Target: target, Arg1: int64(kind)}
}

func listenerFault(kind sim.FaultKind, clearAt sim.Time) []sim.Fault {
	return []sim.Fault{{At: 0, Kind: kind, Target: 0}, clearFault(clearAt, 0, kind)}
}

// Scenarios returns all 11 named toy scenarios covering clean operation,
// random splitting, and every simnet fault kind.
func Scenarios() []Scenario {
	return []Scenario{
		{Name: "echo-clean", Expect: OutcomeCompleted, recoverAt: shortRecovery},
		{Name: "echo-random-split", Expect: OutcomeCompleted, Ablate: true, recoverAt: shortRecovery,
			options: func(o *simnet.Options) { o.Up.Chunking, o.Down.Chunking = simnet.ChunkRandom, simnet.ChunkRandom }},
		{Name: "echo-chunk-1", Kind: simnet.FaultChunk, Expect: OutcomeCompleted, Ablate: true, recoverAt: shortRecovery,
			faults: []sim.Fault{connFault(simnet.FaultChunk, simnet.DirBoth, 1), clearFault(sim.Time(4*time.Millisecond), 1, simnet.FaultChunk)}},
		{Name: "echo-stall", Kind: simnet.FaultStall, Expect: OutcomeCompleted, Ablate: true, recoverAt: shortRecovery,
			faults: []sim.Fault{connFault(simnet.FaultStall, simnet.DirBoth, int64(StallFor))}},
		{Name: "echo-stall-timeout", Kind: simnet.FaultStall, Expect: OutcomeTimedOut, recoverAt: longRecovery,
			faults: []sim.Fault{connFault(simnet.FaultStall, simnet.DirBoth, 0), clearFault(longClear, 1, simnet.FaultStall)}},
		{Name: "echo-reset", Kind: simnet.FaultReset, Expect: OutcomeReset, recoverAt: shortRecovery,
			faults: []sim.Fault{connFault(simnet.FaultReset, 0, 0), {At: sim.Time(30 * time.Millisecond), Kind: simnet.FaultReset, Target: 1}}},
		{Name: "echo-half-open", Kind: simnet.FaultHalfOpen, Expect: OutcomeTimedOut, recoverAt: longRecovery,
			faults: []sim.Fault{connFault(simnet.FaultHalfOpen, 1, 0)}},
		{Name: "echo-half-close", Kind: simnet.FaultHalfClose, Expect: OutcomeEarlyEOF, recoverAt: shortRecovery,
			faults: []sim.Fault{connFault(simnet.FaultHalfClose, 1, 0)}},
		{Name: "echo-refuse", Kind: simnet.FaultRefuse, Expect: OutcomeRefused, AllTargeted: true, recoverAt: shortRecovery,
			faults: listenerFault(simnet.FaultRefuse, sim.Time(30*time.Millisecond))},
		{Name: "echo-dial-stall", Kind: simnet.FaultDialStall, Expect: OutcomeTimedOut, AllTargeted: true, recoverAt: longRecovery,
			faults: listenerFault(simnet.FaultDialStall, longClear)},
		{Name: "echo-silence", Kind: simnet.FaultSilence, Expect: OutcomeTimedOut, AllTargeted: true, recoverAt: longRecovery,
			faults: listenerFault(simnet.FaultSilence, longClear)},
	}
}

// Lookup finds a named scenario in Scenarios.
func Lookup(name string) (Scenario, bool) {
	for _, sc := range Scenarios() {
		if sc.Name == name {
			return sc, true
		}
	}
	return Scenario{}, false
}

// Config builds the toy Config for running sc with seed.
func (sc Scenario) Config(seed int64) Config {
	o := BaseOptions()
	if sc.options != nil {
		sc.options(&o)
	}
	return Config{
		Seed:      seed,
		Scenario:  sc.Name,
		Options:   o,
		Faults:    append([]sim.Fault(nil), sc.faults...),
		RecoverAt: sc.recoverAt,
	}
}

// Control builds the unfaulted control Config for sc using BaseOptions and no
// faults, used by ablation checks.
func (sc Scenario) Control(seed int64) Config {
	cfg := sc.Config(seed)
	cfg.Faults = nil
	cfg.Options = BaseOptions()
	return cfg
}

// Run executes sc with seed and returns its Result.
func (sc Scenario) Run(seed int64) *Result { return Run(sc.Config(seed)) }

// FaultCount returns the counter for kind from snap.Faults, or 0 if unknown.
func FaultCount(snap simnet.Snapshot, kind sim.FaultKind) int64 {
	f := snap.Faults
	switch kind {
	case simnet.FaultReset:
		return f.Reset
	case simnet.FaultStall:
		return f.Stall
	case simnet.FaultHalfOpen:
		return f.HalfOpen
	case simnet.FaultHalfClose:
		return f.HalfClose
	case simnet.FaultChunk:
		return f.Chunk
	case simnet.FaultRefuse:
		return f.Refuse
	case simnet.FaultDialStall:
		return f.DialStall
	case simnet.FaultSilence:
		return f.Silence
	case simnet.FaultClear:
		return f.Clear
	}
	return 0
}

// Check asserts all scenario obligations on res: no kernel or quiesce error,
// expected client and recovery outcomes, non-zero fault and clear counters,
// and non-zero traffic in both directions.
func (sc Scenario) Check(res *Result) error {
	if res.Err != nil {
		return res.Err
	}
	if res.Quiesce != nil {
		return fmt.Errorf("quiesce: %w", res.Quiesce)
	}
	for i, o := range res.Outcomes {
		want := OutcomeCompleted
		if sc.AllTargeted || i == 0 {
			want = sc.Expect
		}
		if o != want {
			return fmt.Errorf("client %d: outcome %s, want %s", i, o, want)
		}
	}
	if sc.recoverAt > 0 && res.Recovery != OutcomeCompleted {
		return fmt.Errorf("recovery client: outcome %s, want completed", res.Recovery)
	}
	snap := res.Net.Snapshot()
	if sc.Kind != 0 && FaultCount(snap, sc.Kind) == 0 {
		return fmt.Errorf("fault %s was never applied to a target", simnet.FaultName(sc.Kind))
	}
	for _, f := range sc.faults {
		if f.Kind == simnet.FaultClear && snap.Faults.Clear == 0 {
			return fmt.Errorf("the clear was never applied")
		}
	}
	if snap.Up.Delivered == 0 || snap.Down.Delivered == 0 || snap.ConnsOpened == 0 {
		return fmt.Errorf("no traffic moved: %+v", snap)
	}
	return nil
}
