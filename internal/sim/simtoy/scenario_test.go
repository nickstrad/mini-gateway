package simtoy

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

var update = flag.Bool("update", false, "rewrite testdata/golden-digests.txt")

// Proves every named scenario is deterministic in process (matching digests
// and textual snapshots), meets its outcome, recovery, quiesce, and counter
// checks, and passes ablation where configured across all swept seeds.
func TestScenario(t *testing.T) {
	for _, sc := range Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			simtest.Sweep(t, func(t *testing.T, seed int64) {
				replay := simtest.ReplayCommand(t, seed)
				var runs []*Result
				var snaps []string
				simtest.RequireDeterministic(t, seed, func(seed int64) *sim.Sim {
					cfg := sc.Config(seed)
					cfg.Replay = replay
					res := Run(cfg)
					runs = append(runs, res)
					snaps = append(snaps, fmt.Sprintf("%+v %+v %+v", res.Net.Snapshot(), res.Sim.Snapshot(), res.Server))
					return res.Sim
				})
				res := runs[0]
				if err := sc.Check(res); err != nil {
					t.Fatalf("%v\n%s", err, replay)
				}
				if snaps[0] != snaps[1] {
					t.Fatalf("snapshots differ between two runs of one seed:\n%s\n%s\n%s", snaps[0], snaps[1], replay)
				}
				if sc.Ablate {
					checkAblation(t, sc, seed, res, replay)
				}
			})
		})
	}
}

func checkAblation(t *testing.T, sc Scenario, seed int64, res *Result, replay string) {
	t.Helper()
	control := Run(sc.Control(seed))
	if err := (Scenario{Name: "control", Expect: OutcomeCompleted, recoverAt: sc.recoverAt}).Check(control); err != nil {
		t.Fatalf("control run: %v\n%s", err, replay)
	}
	switch sc.Kind {
	case simnet.FaultStall:
		stallEnd := faultAt.Add(StallFor)
		if res.Finished[0] < stallEnd || control.Finished[0] >= stallEnd {
			t.Fatalf("stall had no effect: finished at %d, control at %d, stall ends %d\n%s", res.Finished[0], control.Finished[0], stallEnd, replay)
		}
	default:
		got, ctl := res.Net.Snapshot(), control.Net.Snapshot()
		if got.Up.Chunks+got.Down.Chunks <= ctl.Up.Chunks+ctl.Down.Chunks {
			t.Fatalf("chunking had no effect: chunks %d/%d vs control %d/%d\n%s",
				got.Up.Chunks, got.Down.Chunks, ctl.Up.Chunks, ctl.Down.Chunks, replay)
		}
	}
	ctl := sc.Control(seed)
	ctl.plantBug = true
	if err := Run(ctl).Err; err != nil {
		t.Fatalf("the planted bug fired without chunking: %v\n%s", err, replay)
	}
}

// Proves the planted server line-splitting bug causes echo-chunk-1 and
// echo-random-split to fail with ErrMismatch and a REPLAY line.
func TestPlantedBugCanary(t *testing.T) {
	for _, name := range []string{"echo-chunk-1", "echo-random-split"} {
		sc, _ := Lookup(name)
		canary(t, sc)
	}
}

func canary(t *testing.T, sc Scenario) {
	for seed := int64(1); seed <= 5; seed++ {
		cfg := sc.Config(seed)
		cfg.plantBug = true
		res := Run(cfg)
		if !errors.Is(sc.Check(res), ErrMismatch) {
			t.Fatalf("seed %d: the chunk scenario passed with the planted bug", seed)
		}
		if !strings.Contains(res.Err.Error(), "REPLAY: go run ./cmd/mgsim run "+sc.Name+" -seed") {
			t.Fatalf("no replay line: %v", res.Err)
		}
	}
}

// Proves every counter in simnet.Snapshot moves across the scenario suite,
// byte conservation holds at every step, and both snapshot types are plain.
func TestCountersMove(t *testing.T) {
	netMoved := map[string]bool{}
	for _, sc := range Scenarios() {
		cfg := sc.Config(1)
		var last *simnet.Net
		cfg.observe = func(n *simnet.Net) {
			last = n
			snap := n.Snapshot()
			obstest.MergeMoved(netMoved, snap)
			for _, d := range []simnet.DirSnapshot{snap.Up, snap.Down} {
				if d.Written != d.Delivered+d.Discarded+d.InFlight {
					t.Fatalf("%s: conservation broken in a snapshot: %+v", sc.Name, d)
				}
			}
		}
		res := Run(cfg)
		if last == nil || res.Err != nil {
			t.Fatalf("%s: %v", sc.Name, res.Err)
		}
	}
	if never := obstest.NeverMoved(netMoved); len(never) > 0 {
		t.Fatalf("simnet counters that never moved: %v", never)
	}
	obstest.RequirePlain(t, simnet.Snapshot{})
	obstest.RequirePlain(t, sim.Snapshot{})
}

func goldenLine(sc Scenario, seed int64) string {
	d := sc.Run(seed).Sim.Digests()
	return fmt.Sprintf("%s %d %s %s", sc.Name, seed, d.Trace, d.State)
}

// Proves every scenario on every pinned seed reproduces the committed
// testdata/golden-digests.txt hashes bit-for-bit.
func TestGoldenDigests(t *testing.T) {
	pinned, err := simtest.PinnedSeeds("testdata")
	if err != nil || len(pinned) == 0 {
		t.Fatalf("pinned seeds: %v %v", pinned, err)
	}
	var lines []string
	for _, sc := range Scenarios() {
		for _, seed := range pinned {
			lines = append(lines, goldenLine(sc, seed))
		}
	}
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "golden-digests.txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("digests differ from %s (review the change, then rerun with -update):\n%s", path, got)
	}
}

// Proves distinct seeds produce distinct trace digests on echo-clean.
func TestDifferentSeedsGiveDifferentTraces(t *testing.T) {
	seen := map[sim.Digest]int64{}
	sc, _ := Lookup("echo-clean")
	for seed := int64(1); seed <= 30; seed++ {
		d := sc.Run(seed).Sim.TraceDigest()
		if prev, ok := seen[d]; ok {
			t.Fatalf("seeds %d and %d gave one trace digest", prev, seed)
		}
		seen[d] = seed
	}
}

// Proves disabling decision tracing leaves StateDigest identical on every scenario.
func TestTraceOffKeepsStateDigest(t *testing.T) {
	for _, sc := range Scenarios() {
		on := sc.Run(3)
		cfg := sc.Config(3)
		cfg.TraceOff = true
		off := Run(cfg)
		if on.Sim.StateDigest() != off.Sim.StateDigest() || off.Sim.LogLen() != 0 || on.Sim.LogLen() == 0 {
			t.Fatalf("%s: trace switch changed the state digest", sc.Name)
		}
	}
}

func recorder(keep func(sim.Record) bool) (func(sim.Record), func() [32]byte) {
	h := sha256.New()
	var buf [56]byte
	tap := func(r sim.Record) {
		if !keep(r) {
			return
		}
		binary.BigEndian.PutUint64(buf[0:], uint64(r.At))
		copy(buf[8:16], r.Kind[:])
		for i, v := range []int64{r.A, r.B, r.C, r.D} {
			binary.BigEndian.PutUint64(buf[16+8*i:], uint64(v))
		}
		h.Write(buf[:48])
	}
	return tap, func() [32]byte { var d [32]byte; copy(d[:], h.Sum(nil)); return d }
}

func connTraffic(ids ...int64) func(sim.Record) bool {
	return func(r sim.Record) bool {
		k := r.Kind.String()
		if r.Kind == sim.KindFault || k == "net.nota" {
			return false
		}
		if k == "toy.out" {
			return slices.Contains(ids, r.A+1)
		}
		return slices.Contains(ids, r.A)
	}
}

// Proves every per-connection fault targeting connection 1 leaves trace
// records, received bytes, and finish times of connections 2 and 3 identical.
func TestFaultOnOneConnectionLeavesOthersIdentical(t *testing.T) {
	clean, _ := Lookup("echo-clean")
	for _, sc := range Scenarios() {
		if sc.AllTargeted || sc.Kind == 0 {
			continue
		}
		for seed := int64(1); seed <= 5; seed++ {
			tapA, sumA := recorder(connTraffic(2, 3))
			cfg := sc.Config(seed)
			cfg.Tap = tapA
			faulted := Run(cfg)
			tapB, sumB := recorder(connTraffic(2, 3))
			ccfg := clean.Config(seed)
			ccfg.Options = cfg.Options
			ccfg.Tap = tapB
			control := Run(ccfg)
			if sumA() != sumB() {
				t.Fatalf("%s seed %d: traffic on connections 2 and 3 changed", sc.Name, seed)
			}
			for i := 1; i < len(faulted.Received); i++ {
				if string(faulted.Received[i]) != string(control.Received[i]) || faulted.Finished[i] != control.Finished[i] {
					t.Fatalf("%s seed %d: client %d saw different bytes", sc.Name, seed, i)
				}
			}
		}
	}
}

// Proves injecting and clearing all five clearable faults before traffic
// leaves traffic trace digests and StateDigest identical to a clean run.
func TestClearedFaultLeavesNoResidue(t *testing.T) {
	ms := func(us int64) sim.Time { return sim.Time(us * int64(time.Microsecond)) }
	faults := []sim.Fault{
		{At: ms(100), Kind: simnet.FaultRefuse, Target: 0},
		{At: ms(150), Kind: simnet.FaultDialStall, Target: 0},
		{At: ms(180), Kind: simnet.FaultSilence, Target: 0},
		clearFault(ms(200), 0, simnet.FaultRefuse),
		clearFault(ms(210), 0, simnet.FaultDialStall),
		clearFault(ms(220), 0, simnet.FaultSilence),
		{At: ms(1750), Kind: simnet.FaultStall, Target: 1, Arg1: simnet.DirBoth},
		{At: ms(1760), Kind: simnet.FaultChunk, Target: 1, Arg1: simnet.DirBoth, Arg2: 1},
		clearFault(ms(2400), 1, simnet.FaultStall),
		clearFault(ms(2410), 1, simnet.FaultChunk),
	}
	for seed := int64(1); seed <= 10; seed++ {
		run := func(f []sim.Fault) (*Result, [32]byte) {
			tap, sum := recorder(func(r sim.Record) bool { return r.Kind != sim.KindFault })
			cfg := Config{Seed: seed, Options: BaseOptions(), StartAt: 500 * time.Microsecond, Faults: f, Tap: tap, RecoverAt: shortRecovery}
			return Run(cfg), sum()
		}
		faulted, ft := run(faults)
		plain, pt := run(nil)
		if faulted.Err != nil || faulted.Net.Snapshot().Faults.Clear != 5 {
			t.Fatalf("seed %d: %v %+v", seed, faulted.Err, faulted.Net.Snapshot().Faults)
		}
		if ft != pt || faulted.Sim.StateDigest() != plain.Sim.StateDigest() {
			t.Fatalf("seed %d: a cleared fault left a residue", seed)
		}
	}
}

// Proves CheckQuiesce catches a leaked open connection, a half-closed
// connection, and a leaked foreground timer after a toy run.
func TestQuiesceCatchesLeaks(t *testing.T) {
	sc, _ := Lookup("echo-clean")
	res := sc.Run(1)
	if res.Quiesce != nil {
		t.Fatal(res.Quiesce)
	}
	s, n := res.Sim, res.Net
	e := n.Dial(Address, BaseOptions(), simnet.HandlerFunc(func(e *simnet.End, ev simnet.Event) {
		if ev == simnet.EvConnected {
			_, _ = e.Write([]byte{'x'})
		}
	}))
	_ = s.RunUntilQuiet()
	err := s.CheckQuiesce()
	if err == nil || !strings.Contains(err.Error(), "1 connections open") || !strings.Contains(err.Error(), "simnet") {
		t.Fatalf("leaked connection not caught: %v", err)
	}
	c := n.Conn(e.ID())
	_ = c.Server().Close()
	_ = s.RunUntilQuiet()
	if err := s.CheckQuiesce(); err == nil {
		t.Fatal("half-closed connection not caught")
	}
	_ = e.Close()
	_ = s.RunUntilQuiet()
	if err := s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
	s.After(time.Hour, func() {})
	if err := s.CheckQuiesce(); err == nil || !strings.Contains(err.Error(), "sim.timers") {
		t.Fatalf("leaked timer not caught: %v", err)
	}
}

// Proves CheckQuiesce catches a single unread byte left in flight on a pipe.
func TestQuiesceCatchesInFlightByte(t *testing.T) {
	cfg := Config{Seed: 1, Options: BaseOptions(), Clients: 1}
	res := Run(cfg)
	s, n := res.Sim, res.Net
	var srv *simnet.End
	l, _ := n.Listen("sink", func(e *simnet.End) { srv = e })
	_ = l
	e := n.Dial("sink", BaseOptions(), simnet.HandlerFunc(func(e *simnet.End, ev simnet.Event) {
		if ev == simnet.EvConnected {
			_, _ = e.Write([]byte{'x'})
		}
	}))
	_ = s.RunUntilQuiet()
	if err := s.CheckQuiesce(); err == nil || !strings.Contains(err.Error(), "bytes in flight: up 1") {
		t.Fatalf("in-flight byte not caught: %v", err)
	}
	_ = e.Close()
	_ = srv.Close()
	_ = s.RunUntilQuiet()
	if err := s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

// Proves exhausting MaxSteps yields a StallError and an actor panic yields a
// PanicError, each carrying the configured REPLAY command.
func TestStepBudgetAndPanicAreTypedFailures(t *testing.T) {
	sc, _ := Lookup("echo-clean")
	cfg := sc.Config(4)
	cfg.MaxSteps = 50
	res := Run(cfg)
	var se *sim.StallError
	if !errors.As(res.Err, &se) || !errors.Is(res.Err, sim.ErrBudgetExhausted) || se.Seed != 4 {
		t.Fatalf("got %v", res.Err)
	}
	if !strings.Contains(res.Err.Error(), "REPLAY: go run ./cmd/mgsim run echo-clean -seed 4") {
		t.Fatalf("no replay line: %v", res.Err)
	}
	cfg = sc.Config(5)
	cfg.panicOnEOF = true
	cfg.Replay = simtest.ReplayCommand(t, 5)
	res = Run(cfg)
	var pe *sim.PanicError
	if !errors.As(res.Err, &pe) || pe.Seed != 5 || !strings.Contains(res.Err.Error(), "REPLAY: go test ./internal/sim/simtoy") {
		t.Fatalf("got %v", res.Err)
	}
}

func shrinkSpace() sim.Space {
	return sim.Space{Kinds: simnet.FaultKinds(), Targets: 4, Start: 0, Window: sim.Time(40 * time.Millisecond), MaxArg1: 3, MaxArg2: 3, MaxFaults: 64}
}

// Proves sim.Shrink reduces a 40-fault decoded schedule that triggers the
// planted bug down to a 1-minimal failing schedule of at most 2 faults
// containing a FaultChunk.
func TestPlantedBugCaughtAndShrunk(t *testing.T) {
	fails := func(f []sim.Fault) bool {
		cfg := Config{Seed: 1, Options: BaseOptions(), Faults: f}
		cfg.plantBug = true
		return Run(cfg).Mismatch
	}
	found := 0
	for seed := int64(1); seed <= 40 && found < 3; seed++ {
		r := sim.NewStream(seed, "shrink-bytes")
		data := make([]byte, 8*40)
		for i := range data {
			data[i] = byte(r.Uint32())
		}
		sched := sim.ScheduleFromBytes(data, shrinkSpace())
		if len(sched) < 30 || !fails(sched) {
			continue
		}
		found++
		calls := 0
		out := sim.Shrink(sched, func(f []sim.Fault) bool { calls++; return fails(f) })
		if !fails(out) {
			t.Fatalf("seed %d: shrunk schedule passes", seed)
		}
		if len(out) > 2 {
			t.Fatalf("seed %d: %d faults left after shrinking %d: %+v", seed, len(out), len(sched), out)
		}
		for i := range out {
			if fails(slices.Delete(slices.Clone(out), i, i+1)) {
				t.Fatalf("seed %d: fault %d removable", seed, i)
			}
		}
		if !slices.ContainsFunc(out, func(f sim.Fault) bool { return f.Kind == simnet.FaultChunk }) {
			t.Fatalf("seed %d: minimal schedule %+v has no chunk fault", seed, out)
		}
		t.Logf("seed %d: %d faults shrunk to %d in %d runs: %+v", seed, len(sched), len(out), calls, out)
	}
	if found == 0 {
		t.Fatal("no failing schedule was generated")
	}
}

func runDecoded(t *testing.T, data []byte) {
	cfg := Config{Seed: 1, Options: BaseOptions(), Faults: sim.ScheduleFromBytes(data, shrinkSpace()), RecoverAt: 3 * time.Second}
	res := Run(cfg)
	var pe *sim.PanicError
	var se *sim.StallError
	if errors.As(res.Err, &pe) || errors.As(res.Err, &se) {
		t.Fatalf("kernel failure under a decoded schedule: %v", res.Err)
	}
	if res.Err != nil {
		t.Fatalf("unexpected failure: %v", res.Err)
	}
}

// Fuzzes arbitrary byte strings decoded into fault schedules against the toy
// protocol to prove no schedule panics or stalls the kernel.
func FuzzToySchedule(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{4, 1, 0, 9, 0, 3, 0, 1})
	f.Add([]byte{2, 1, 0, 9, 0, 3, 0, 0, 1, 1, 0, 10, 0, 0, 0, 0})
	f.Fuzz(runDecoded)
}

// Proves hundreds of random decoded fault schedules execute cleanly on the toy
// without kernel panics, stalls, or invariant failures.
func TestDecodedSchedulesNeverBreakTheKernel(t *testing.T) {
	n := 300
	if testing.Short() {
		n = 60
	}
	r := sim.NewStream(9, "decoded")
	for i := 0; i < n; i++ {
		data := make([]byte, r.IntN(200))
		for j := range data {
			data[j] = byte(r.Uint32())
		}
		runDecoded(t, data)
	}
}
