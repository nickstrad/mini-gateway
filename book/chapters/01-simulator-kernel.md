# 1. A world that repeats: the simulator kernel

Networked proxies fail in ways that ordinary unit tests rarely catch. A client query arrives split across two TCP packets, or a backend stalls for twenty milliseconds while the send buffer fills. One peer drops off the network without sending a reset, or a timeout fires at the same microsecond as a late reply. Run that scenario against real sockets and a wall clock, and a bug that shows up once in five hundred runs vanishes as soon as you attach a debugger.

Before `mini-gateway` has a single line of proxy or Postgres code, it builds a deterministic simulator. A deterministic simulator replaces the operating system's scheduler, wall clock, random-number generator, and TCP sockets with a single-threaded event loop driven by one 64-bit seed. Give it the same seed and the same binary, and every event, timer, random draw, byte split, and failure happens in the exact same order, down to the nanosecond.

This chapter covers Chunk 1:

- the simulator kernel ([`internal/sim`](../../internal/sim)) and simulated byte-stream network ([`internal/sim/simnet`](../../internal/sim/simnet)),
- the measurement half of observability ([`internal/obs`](../../internal/obs)),
- the test plumbing ([`internal/sim/simtest`](../../internal/sim/simtest) and [`internal/obs/obstest`](../../internal/obs/obstest)),
- a toy echo protocol that exercises the harness ([`internal/sim/simtoy`](../../internal/sim/simtoy)), and
- the standalone runner ([`cmd/mgsim`](../../cmd/mgsim)).

---

## Why build a new kernel instead of copying a whole simulator

An earlier sibling project (`object-store`) already had a working deterministic simulator in Go, with 4,106 lines of product code and 58 held adversarial attacks. The plan evaluated adopting it wholesale ([section 5 of the plan](../../PLAN.md#5-steering-answer-8-can-the-object-store-simulator-be-the-starting-point)) and turned that down.

The mismatch is structural. The object-store simulator models a cluster of nodes exchanging datagrams and whole messages up to 16 MiB, with per-node skewed clocks, node incarnations, disks with torn writes, and network partitions. It has no byte-stream connections: no partial reads, no send windows, no back pressure, no half-close, and no TCP resets. Roughly 76% of its `internal/sim` package is cluster machinery a Postgres proxy would delete, and its event loop dispatches on ten domain-specific event kinds rather than treating events as opaque callbacks. Only about 8% of its code would survive unchanged, and about 26% with edits.

Instead of inheriting a cluster model, Chunk 1 builds a small kernel in this repository and copies only the pieces that fit cleanly (recorded file by file in `docs/knowledge/sim-kernel-provenance.md` from object-store commit `25fb899`):

- **Copied as-is (comments stripped during build):** named random streams (`internal/sim/stream.go`), the indexed min-heap (`internal/sim/queue.go`), seed flags and sweeps (`internal/sim/simtest/seeds.go`), and the determinism linter (`tools/detlint`, landed in Chunk 0).
- **Copied and adapted:** saturating 64-bit time helpers (`internal/sim/time.go`), the run-twice determinism check and failure report (`internal/sim/simtest/determinism.go`), the loop skeleton (`internal/sim/sim.go`), and the CLI layout (`cmd/mgsim/main.go`).
- **Built new:** an opaque-event loop with no domain kinds, simulated full-duplex byte streams (`simnet`), a decision trace with two independent SHA-256 digests, a fixed-size integer histogram (`obs.Histogram`), and a standard-library fault decoder and shrinker.

Two other alternatives were rejected up front:

- **Property-testing libraries (`pgregory.net/rapid`).** The module policy restricts third-party dependencies to one Postgres codec (`pgproto3`, entering in Chunk 2). Rather than importing `rapid` to generate and shrink fault schedules, the kernel feeds Go's built-in fuzzer raw bytes decoded into bounded schedules (`ScheduleFromBytes`). Failing schedules are then shrunk by an 18-line standard-library loop (`Shrink`).
- **Floating-point arithmetic inside the simulation.** Floating-point rounding and library choices can vary across instructions or math routines, and `mini-gateway`'s core control laws (CoDel, AIMD, histograms) are designed in integer form anyway ([decision D2](../../PLAN.md#3-design-decisions-this-plan-makes)). Keeping floats out of all four simulation and measurement packages lets `detlint` enforce a zero-float, zero-goroutine, zero-wall-clock, zero-global-random rule with an empty allow-list (`tools/lint-rules/detlint-allow.txt`). The test plumbing packages (`simtest` and `obstest`) read flags, environment variables, files, or `reflect`, so they stay outside the lint list by explicit listing in [`tools/lint-rules/det-packages.txt`](../../tools/lint-rules/det-packages.txt).

---

## The kernel: virtual time, streams, and the event loop

Four mechanisms make a simulation repeat bit for bit.

### Saturating virtual time

[`sim.Time`](../../internal/sim/time.go) is a signed 64-bit integer counting nanoseconds since the start of a run (`0`). Normal signed addition wraps silently on overflow: adding two huge durations wraps negative, and scheduling an event at a negative offset clamps it to `Now()` so it fires immediately. Every time helper—`Time.Add`, `Time.Sub`, `Later`, `AddDurations`, and `Scaled`—saturates at `MinTime` or `MaxTime` instead of wrapping.

### Isolated named random streams

A single shared random generator is fragile. Adding one extra draw to connection 1 (say, because a fault triggered a retry) shifts every later draw for connection 2 and destroys fault isolation. [`sim.NewStream`](../../internal/sim/stream.go) avoids this by deriving a separate PCG generator from `(seed, fnv64a(label))`:

```go
func NewStream(seed int64, label string) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), fnv64a([]byte(label))))
}
```

Each pipe of each simulated connection draws from its own stream (`simnet/conn/<id>/up` or `down`), and each toy client draws from `simtoy/client/<id>`. Drawing a million extra values from one label leaves every other label's sequence unchanged. Callers keep the returned `*rand.Rand`, because calling `Stream(label)` twice with the same label starts from the beginning again.

### An opaque-event min-heap with real cancellation

The event queue in [`internal/sim/queue.go`](../../internal/sim/queue.go) is an indexed binary min-heap ordered by `(at, seq)`, where `seq` is a monotonically increasing scheduling counter. Because `seq` breaks ties, events scheduled for the exact same virtual nanosecond always execute in the order they were scheduled, independent of heap swaps.

Every event stores its own heap slot (`hi`), so `Timer.Cancel()` removes the entry from the heap in `O(log n)` time rather than leaving a tombstone behind. That matters once a proxy holds thousands of per-request deadline timers that are cancelled on every normal reply.

Events carry either a Go closure (`At`, `After`) or an opaque `Handler` interface plus a `uint64` code (`AtHandler`, `AfterHandler`), which avoids allocating a closure on hot paths like byte delivery. Timers marked with `MarkBackground()` keep firing while foreground work runs. Once only background timers remain, `RunUntilQuiet()` stops, and the end-of-run leak check ignores them.

### Livelock guards, panic guards, invariants, and quiesce

[`Sim.Step`](../../internal/sim/sim.go) enforces three safety bounds before popping an event:

- `MaxSteps` (default 50,000,000): stops infinite event chains with `ErrBudgetExhausted`.
- `MaxSameInstant` (default 1,000,000): stops zero-delay spin loops with `ErrTimeFrozen`. It counts the first step at any instant as `1`, so `t = 0` and `t > 0` enforce the exact same limit.
- `MaxEvents` (default 4,194,304): checked on schedule; stops runaway fan-out with `ErrHeapFull`.

Any panic inside an event callback, invariant, or quiesce check is caught by `recover()` and turned into a sticky `*sim.PanicError` carrying the seed, step count, virtual time, and `REPLAY:` command line. Errors are sticky: the first error wins and halts further steps so the original cause is never masked.

After every step, the loop runs registered **invariants** (`RegisterInvariant`, or `RegisterSampledInvariant` for expensive checks). At the end of a scenario, callers invoke `CheckQuiesce()`, which verifies that no non-background timers remain in the heap and that every registered subsystem **quiesce** check—an assertion that zero resources remain leaked or in flight—returns `nil`.

---

## Split digests, traces, and 1-minimal shrinking

To prove the **replay law**—running the same scenario twice with the same seed yields identical behaviour—the kernel maintains two independent SHA-256 hashes in [`internal/sim/trace.go`](../../internal/sim/trace.go):

1. **Trace digest (`TraceDigest`):** Hashes every 48-byte decision record (`At`, 8-byte `Kind` tag, and four `int64` fields) as actors call `s.Trace(...)`. Records omit the loop's sequence number so an unrelated fault on connection 1 does not shift the trace records of connection 2. A bounded ring buffer keeps the last `N` records (default 256, capped at 65,536) for failure reports. Tracing can be switched off with `Config.TraceOff`, which skips hashing and ring writes.
2. **State digest (`StateDigest`):** Calls every writer registered with `RegisterState(name, fn)` in registration order—never map iteration order—and hashes length-prefixed names, strings, and 64-bit integers. Neither the kernel's step and fault counters nor `simnet`'s fault counters feed into `StateDigest`. Two properties follow at once: switching tracing off leaves `StateDigest` identical, and injecting a fault then clearing it before traffic leaves `StateDigest` identical to a never-faulted run.

### Fault schedules and shrinking

Faults are plain structs: `Fault{At, Kind, Target, Arg1, Arg2}`. Fault kinds are registered per `Sim` instance (up to 64 kinds) rather than in a package-level global, so the set of kinds never depends on which packages a test binary happens to link. `simnet` owns kinds 1 through 9; later world packages will take disjoint ranges.

For fuzzing, `ScheduleFromBytes(data, Space)` consumes 8 bytes per fault and maps each field modulo the allowed kinds, target count, time window `[Start, Start+Window)`, and argument bounds. The window mapping uses 128-bit multiplication so `Window` can reach `MaxTime` without overflow. The decoder caps the schedule at `MaxFaults` (default 64, hard cap 1,024) and stably sorts it by time; every byte string decodes to a valid schedule without panicking.

When a fuzzed or long fault schedule triggers a failure, [`sim.Shrink`](../../internal/sim/fault.go) reduces it to a **1-minimal** failing schedule—one that still fails, and from which deleting any single fault makes the failure disappear:

```go
func Shrink(sched []Fault, stillFails func([]Fault) bool) []Fault {
	cur := slices.Clone(sched)
	for {
		removed := false
		for i := 0; i < len(cur); {
			cand := slices.Delete(slices.Clone(cur), i, i+1)
			if stillFails(cand) {
				cur = cand
				removed = true
				continue
			}
			i++
		}
		if !removed {
			return cur
		}
	}
}
```

Repeating full delete-one passes until a pass removes nothing guarantees 1-minimality even when `stillFails` is non-monotone (where deleting fault A makes a previously necessary fault B redundant).

---

## Simulated byte-stream connections (`simnet`)

[`internal/sim/simnet`](../../internal/sim/simnet) models TCP-like full-duplex connections on top of `sim.Sim`. Every connection has a client end (which called `Dial`), a server end (passed to `Listen`'s accept callback), and two independent unidirectional **pipes**: **up** (client to server) and **down** (server to client).

Nothing is ever called synchronously inside a network operation. Every notification—`EvConnected`, `EvRefused`, `EvReadable`, `EvWritable`, `EvReset`—is scheduled as its own event on the loop, and duplicate pending notifications of the same event kind on an end are coalesced.

### Latency, chunking, and back pressure

A handshake takes one round trip on the virtual clock. After up latency, the server's accept callback runs; after down latency, the client receives `EvConnected`. If the server writes immediately on accept, its bytes buffer and `EvReadable` is queued strictly after `EvConnected`.

Each pipe enforces a bounded **send window** (default 64 KiB, cap 16 MiB). **In flight** means every byte accepted by `Write` that has neither been read by the receiver nor discarded—whether still travelling, held by a stall, or sitting delivered in the receive buffer. When a writer tries to write more than `window - len(buf)`, `Write` accepts only what fits, increments `WritesRefused`, and sets `blocked = true`. When the receiver later reads bytes or discards them on `Close()`, `pipe.unblock()` delivers one `EvWritable` event (**back pressure**).

Accepted writes are split into delivery pieces according to the direction's chunking mode: `ChunkWhole` sends one piece, `ChunkFixed` splits down to 1 byte per event, and `ChunkRandom` uses seeded splits capped at 64 pieces per write. Each piece's arrival time is clamped so random jitter can delay a later piece but never reorder it ahead of an earlier one:

```go
func (p *pipe) arrival() sim.Time {
	// Clamp to lastAt so random jitter can delay a later piece but never
	// reorder it ahead of an earlier piece on the same byte stream.
	at := p.c.net.s.Now().Add(p.delay())
	if at < p.lastAt {
		at = p.lastAt
	}
	p.lastAt = at
	return at
}
```

Delivery events pack a 32-bit `epoch` and either `finBit` or the piece length into a single `uint64` `Handler` code. When a connection resets or a receiver closes, `epoch` increments so stale delivery events already sitting in the heap become no-ops when their timestamp arrives.

### The fault catalog

Every `simnet` fault is injectable through the kernel's fault schedule, traced as `fault`, and counted in `simnet.Snapshot().Faults`. Targeting a missing ID, a closed listener, a finished connection, or a not-yet-accepted connection counts as `NoTarget` and does nothing (including for `FaultClear`).

| Fault (kind) | Target | What both ends see | Clear behaviour |
| --- | --- | --- | --- |
| `net.reset` (1) | connection | All in-flight bytes are discarded immediately. Client gets `EvReset` first, then server at the same instant, and subsequent calls return `ErrReset`. | Not clearable. |
| `net.stall` (2) | connection | Arriving pieces and FINs on the masked direction(s) are held while still counting against the send window. | Releases all held bytes and FINs in order as one arrival (or auto-clears after `Arg2` ns). |
| `net.half-open` (3) | connection | The killed end gets no events and returns `ErrGone`. The surviving end never gets a FIN or reset, writes until the window fills, and closes (`ReasonHalfOpen`) only on its own `Close()` (or `ReasonReset` on `Reset()`). Bytes already in flight from the gone end still arrive. | Not clearable. |
| `net.half-close` (4) | connection | Queues a FIN behind pending bytes on the targeted side. Later writes on that side return `ErrClosed` while reads continue. | Not clearable. |
| `net.chunk` (5) | connection | Overrides piece size (minimum 1 byte) for subsequent writes on the masked direction(s). | Restores original chunking. |
| `net.refuse` (6) | listener | Subsequent dials receive `EvRefused` after one round trip; server never sees them. | Later dials accepted. |
| `net.dial-stall` (7) | listener | Subsequent dials never complete (`ErrNotConnected`). Client `Close`/`Reset` aborts them (`ReasonAborted`). | Later dials complete; already stalled dials stay stalled. |
| `net.silence` (8) | listener | **Accept-then-silence:** client receives `EvConnected` and fills its send window, while the server's accept callback is never called. Closes as `ReasonSilent` when the client closes. | Later dials normal; silenced connections stay silent. |
| `net.clear` (9) | conn / listener | Clears the fault kind named in `Arg1`. | — |

On every loop step, `simnet` checks a registered **conservation law** invariant on both directions:

```text
Written = Delivered + Discarded + InFlight   (with InFlight >= 0)
```

---

## Measurement without floats (`internal/obs`)

Later components will report metrics through a single plain-struct `Snapshot()` interface ([section 7.1 of the plan](../../PLAN.md#71-three-layers-one-direction-of-dependency)). Chunk 1 lands the measurement half in [`internal/obs`](../../internal/obs):

- **`obs.Gauge`** tracks a current `int64` level and its `Peak`.
- **`obs.Conserve` and `obs.Counters`** check conservation identities (such as `Accepted = Completed + Failed + Shed + Queued + InFlight`) and format a `*ConservationError` naming every term and which side is higher.
- **`obs.Histogram`** is a fixed-size, integer-only, log-linear histogram that records values with zero heap allocations.

Values `0..255` get exact single-integer buckets. Above `255`, each power-of-two octave is divided into 128 equal-width sub-buckets using `bits.Len64`:

```go
func bucketIndex(v int64) int {
	if v < exactBuckets {
		return int(v)
	}
	e := bits.Len64(uint64(v)) - 8
	m := v >> e
	return exactBuckets + (e-1)*subBuckets + int(m-subBuckets)
}
```

For the duration histogram (`NewDurationHistogram`, 1 µs to 60 s in nanoseconds), this layout uses 3,302 `int64` buckets (26.4 KB). Out-of-range values clamp into the edge bucket and increment `ClampedLow` or `ClampedHigh`. `Quantile(bp)` takes **basis points** (`0..10000`, so p99 is `9900`) and computes the rank `ceil(n * bp / 10000)` in 128-bit arithmetic (`bits.Mul64`/`bits.Div64`) so huge sample counts never overflow. It returns that bucket's upper bound clipped to `Hi`. For any in-range value `v`, the reported quantile `q` satisfies `v <= q` and `(q - v) * 128 <= v`—less than 0.79% relative error, inside the 1% target.

Beside `internal/obs`, [`internal/obs/obstest`](../../internal/obs/obstest) supplies reflection helpers (`RequirePlain`, `MergeMoved`, `NeverMoved`) so tests can reject maps, floats, pointers, or `time.Time` in snapshots and fail if any snapshot counter never moves.

---

## Running the toy echo protocol and `mgsim`

To prove the harness end to end before Postgres exists, [`internal/sim/simtoy`](../../internal/sim/simtoy) implements a line-echo server and client over `simnet`. It defines 11 named scenarios: one clean run, one random-split run, and nine fault scenarios covering every `simnet` fault. Each scenario runs three clients (with connection faults targeting connection 1) plus a fourth recovery client after the fault clears.

You can list and run scenarios directly with [`cmd/mgsim`](../../cmd/mgsim):

```sh
$ go run ./cmd/mgsim list
echo-clean
echo-random-split
echo-chunk-1
echo-stall
echo-stall-timeout
echo-reset
echo-half-open
echo-half-close
echo-refuse
echo-dial-stall
echo-silence

$ go run ./cmd/mgsim run echo-reset -seed 7
scenario=echo-reset seed=7 steps=153 status=pass
trace-digest: faced537d61b8cbd7ce7430488c33651cbab8cda14550f78503c43e45bfe26fa
state-digest: 91c2b2d389eab70c453454b29f4c7d32bd1fd81ba239ef710ca9271559a7f4fe
REPLAY: go run ./cmd/mgsim run echo-reset -seed 7
```

Passing `-trace` streams every 48-byte trace record as it happens. In `echo-reset` with seed 7, connection 1 is reset at 2.5 ms (`t=2500000`), so client 0 finishes with outcome `2` (`reset`) while clients 1 and 2 finish cleanly (`1` = `completed`). A second reset at 30 ms hits a closed connection (`NoTarget`), and recovery client 3 finishes cleanly at 55.6 ms:

```text
t=0 net.dial 1 0 0 0
t=10000 net.dial 2 0 0 0
t=20000 net.dial 3 0 0 0
t=1024910 net.acc 1 0 0 0
...
t=2500000 fault 1 1 0 0
t=2500000 net.rst 1 -1 0 0
t=2500000 toy.out 0 2 232 0
t=5674458 toy.out 1 1 444 444
t=5697289 toy.out 2 1 420 420
t=30000000 fault 1 1 0 0
t=55617311 toy.out 3 1 450 450
```

---

## How the harness proves itself

Tests in Chunk 1 total 3,698 lines across 127 test and fuzz functions (beside 3,529 lines of product code, of which 380 are `simtest` and `obstest` plumbing). They are organized into three layers:

1. **Meta-tests (testing the test harness).**
   - *Replay:* `TestScenario` runs every scenario twice per seed via `simtest.RequireDeterministic` and asserts identical trace digests, state digests, and formatted snapshots. `TestCrossProcessReplay` and the gate extra script ([`extra.sh`](../../agent_work/chunks/c01-sim-kernel/gate/extra.sh)) run `mgsim` in separate OS processes and require byte-identical output. `TestGoldenDigests` pins all 11 scenarios across 5 seeds (`1, 2, 3, 7, 42`) in [`testdata/golden-digests.txt`](../../internal/sim/simtoy/testdata/golden-digests.txt), updated only deliberately via `-update`.
   - *Isolation and residue:* `TestFaultOnOneConnectionLeavesOthersIdentical` proves that every per-connection fault on connection 1 leaves the trace records, received bytes, and finish times of connections 2 and 3 byte-identical to an unfaulted run. `TestClearedFaultLeavesNoResidue` injects and clears all five clearable faults before traffic and asserts that traffic trace digests and `StateDigest` match a clean run.
   - *Leak traps and planted bugs:* `TestQuiesceCatchesLeaks` and `TestQuiesceCatchesInFlightByte` deliberately leak a connection, an unread byte, and a foreground timer, requiring `CheckQuiesce()` to fail. For a canary, `simtoy` provides an unexported `plantBug` switch that drops the last payload byte of any line arriving across more than one `Read`. Because the toy client writes whole lines whenever they fit the window, the bug cannot fire under whole-write delivery (**ablation**: the control run passes even with the bug on). The `echo-chunk-1` and `echo-random-split` scenarios, however, fail with `ErrMismatch` and a `REPLAY:` line. Running the shrinker against 40-fault random schedules reduces each failing schedule to a single chunk fault:

```sh
$ go test -v ./internal/sim/simtoy -run '^TestPlantedBugCaughtAndShrunk$'
=== RUN   TestPlantedBugCaughtAndShrunk
    scenario_test.go:391: seed 1: 40 faults shrunk to 1 in 41 runs: [{At:2213134ns Kind:5 Target:3 Arg1:1 Arg2:2}]
    scenario_test.go:391: seed 4: 40 faults shrunk to 1 in 41 runs: [{At:3590698ns Kind:5 Target:2 Arg1:1 Arg2:1}]
    scenario_test.go:391: seed 12: 40 faults shrunk to 1 in 41 runs: [{At:3574218ns Kind:5 Target:2 Arg1:0 Arg2:0}]
--- PASS: TestPlantedBugCaughtAndShrunk (0.06s)
```

2. **Property tests and fuzzing.** Seeded property tests compare `eventQueue` and `Sim` against sorted-slice reference models, verify `Histogram` quantiles against sorted slices, check `testing.AllocsPerRun == 0` on `Record`, and drive random interleavings of dials, writes, closes, resets, and faults (`TestRandomOperationsKeepEveryLaw`, also run at 3,000 seeds during build verification). Two fuzz targets—`FuzzScheduleFromBytes` and `FuzzToySchedule`—run under [`scripts/fuzz.sh`](../../scripts/fuzz.sh) for 20 s each in the gate.
3. **End-to-end scenarios and sweeps.** [`scripts/sweep.sh`](../../scripts/sweep.sh) runs all `TestScenario*` tests across `SWEEP_SEEDS` seeds. The default is 200 seeds (2,200 seed runs in the gate); it was also run at 1,000 seeds (11,000 seed runs in 17 s). `TestCountersMove` verifies that every integer counter in `simnet.Snapshot` is non-zero in at least one scenario, and `TestKernelSnapshotCountersMove` does the same for `sim.Snapshot`.

---

## Stated limits

The simulator kernel is intentionally small and has clear boundaries:

- **Replay scope.** Bit-identical replay is claimed for the same commit, same Go toolchain (`go1.26.8`), and `linux/amd64`.
- **Fixed tables per run.** `simnet`'s connection slice grows by one slot per `Dial` and never shrinks within a run. It is capped at `MaxConns` (1,048,576), and listeners are capped at `MaxListeners` (1,024).
- **Stale delivery events.** Closing or resetting a connection invalidates queued delivery events via `epoch`. Those events stay in the heap until their virtual timestamp arrives; the heap's size is bounded by `MaxEvents` = 4,194,304.
- **One-byte chunking cost.** `ChunkFixed` with `ChunkSize: 1` schedules one loop event per byte (a 1 MiB write creates ~1,048,576 events). `ChunkRandom` caps splits at 64 pieces per write.
- **Latency model.** `simnet` supports only `Base + uniform[0, Jitter]` in nanoseconds, saturating at `MaxTime`. Skewed or bimodal service-time distributions belong to the fake Postgres in Chapter 2, not the byte transport.
- **No stack trace in `PanicError`.** Capturing a stack requires `runtime/debug`, which `detlint` forbids in simulation packages. `PanicError` records the panic value, seed, step, virtual time, and `REPLAY:` line instead.
- **No proxy or Postgres code yet.** Everything built so far is the deterministic clock, network, measurement primitives, and toy echo harness. Chapter 2 builds the simulated world on top of this kernel: a fake Postgres server and simulated clients.

---

## Change request CR-1: value lowering, scenario files, and fuzz rate rules

After Chunk 2 froze `internal/sim` at tag `freeze-sim`, Change Request CR-1 ([`CHANGE-REQUEST.md`](../../agent_work/chunks/cr01-shrink-fuzz-rules/CHANGE-REQUEST.md)) extended shrinking and the fuzzing tooling to carry out Nick's DST fuzz ruling ([sections 6.1](../../PLAN.md#61-layers) and [6.6](../../PLAN.md#66-rules-for-simulation-level-fuzz-targets-nicks-dst-fuzz-ruling-2026-11-24) of the plan). The ruling grew out of `object-store`'s `FuzzClusterNemesis`, which hashed its fuzz input into a simulator seed and ran at 0.26 executions per second (against 324/s and 884/s for that project's other targets). Hashing turns every one-byte mutation into a different world, degrading coverage-guided fuzzing into a slow random seed sweep, and opaque pinned seeds make fragile regression tests. CR-1 is strictly additive: `sim.Shrink`, every existing exported identifier, and every existing scenario digest remain unchanged, as pinned by [`shrink_freeze_test.go`](../../internal/sim/shrink_freeze_test.go).

[`internal/sim/lower.go`](../../internal/sim/lower.go) adds three shrinking helpers. `sim.LowerValues` uses overflow-free unsigned bisection to lower each remaining fault's `At`, `Arg1`, and `Arg2` toward per-kind floors (`FaultFloor`, defaulting to zero). `sim.Minimize` alternates whole-fault deletion (`sim.Shrink`) with value lowering until a full round changes nothing, proving both deletion and one-step value minimality even for non-monotone predicates. `sim.LowerInts` lowers plain `int64` slices for non-fault inputs such as workload sizes. Beside them, `sim.FormatSchedule` and `sim.ParseSchedule` in [`internal/sim/schedtext.go`](../../internal/sim/schedtext.go) define a canonical line-oriented text format bounded at `MaxScheduleLen` (1,024 faults), so found bugs are committed as readable scenario files and replayed as plain tests (`docs/knowledge/shrunk-scenarios.md`).

Applied end to end to `simtoy`'s planted bug in [`internal/sim/simtoy/shrunk_test.go`](../../internal/sim/simtoy/shrunk_test.go), `Minimize` keeps the same single `net.chunk` fault as `Shrink` while cutting its value sum from 2,213,137 to 1,021,423. That shrunk schedule is stored in [`planted-bug-chunk.faults`](../../internal/sim/simtoy/testdata/shrunk/planted-bug-chunk.faults) (`faults 1` / `at=1021422ns kind=net.chunk target=3 arg1=0 arg2=1`), and `LowerInts` reduces the toy workload `[clients, lines, max line]` from `[3 20 40]` to `[3 1 1]`:

```sh
$ go test -count=1 -v ./internal/sim/simtoy -run 'PlantedBugMinimised|ShrunkScenario|WorkloadSizes'
=== RUN   TestPlantedBugMinimisedBeyondShrink
    shrunk_test.go:97: 40 faults; Shrink kept 1 (value sum 2213137); Minimize kept 1 (value sum 1021423)
--- PASS: TestPlantedBugMinimisedBeyondShrink (0.06s)
=== RUN   TestShrunkScenarioPlantedBugReplays
--- PASS: TestShrunkScenarioPlantedBugReplays (0.01s)
=== RUN   TestPlantedBugWorkloadSizesLowered
    shrunk_test.go:150: clients, lines, max line: [3 20 40] -> [3 1 1]
--- PASS: TestPlantedBugWorkloadSizesLowered (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/sim/simtoy	0.075s
```

In [`scripts/fuzz.sh`](../../scripts/fuzz.sh) and [`scripts/gate.sh`](../../scripts/gate.sh), CR-1 enforced all three rules of section 6.6. After each target finishes, `fuzz.sh` parses Go's final `fuzz: elapsed:` progress line and computes executions per second (`execs / elapsed seconds`, avoiding Go's per-interval rate, which can print `0/sec` on the last line). If that rate cannot be read, the script fails closed. Any target whose test package depends on `internal/sim` (`go list -deps -test`) and runs below `FUZZ_RATE_FLOOR` (default 100/s) is classed as a `seed-sweep`. It is reported in the gate's `result.json` (`fuzz_targets`, `fuzz_seed_sweeps`, `fuzz_rate_floor`) without failing the run, and it does not count as fuzzing in any claim. Auditing all 12 fuzz targets across the repository confirmed that none hashes its input into a simulator seed: every simulator-running target uses fixed seed `1`.

---

## Change request CR-2: one retry for Go's own fuzz deadline

Go's fuzz coordinator (`go1.26.8`) occasionally ends a target at `-fuzztime` with bare `context deadline exceeded` under `--- FAIL: <target>` and no failing input—its own timer expiring, not a target failure. That happened four times on unchanged targets over two days; each target passed on rerun, yet failing closed discarded the gate.

Change Request CR-2 ([`CHANGE-REQUEST.md`](../../agent_work/chunks/cr02-fuzz-deadline-retry/CHANGE-REQUEST.md)) lets [`scripts/fuzz.sh`](../../scripts/fuzz.sh) retry a target once when all five conditions hold: (1) non-zero exit, (2) a `--- FAIL: <target>` block whose only message is bare `context deadline exceeded`, (3) no `Failing input written to` line, (4) no panic or other failure text in the output, and (5) an unchanged `testdata/fuzz/<target>/` tree (NUL-safe symlink walk with SHA-256 hashes; snapshot errors fail closed). The second run's outcome and rate stand, even if it fails. Each retry prints `fuzz: RETRY: <dir> <target>: go's own deadline, no failing input`, sets a new sixth `retried` column to `1` (first five unchanged), counts it on `NOTE:`, and records `fuzz_retries` (`null` when G5 does not run) in [`scripts/gate.sh`](../../scripts/gate.sh)'s `result.json`.

Real failures stay fatal because the check whitelists only Go's exact progress, warning, and deadline-block lines. Crashes, panics, `fuzz: minimizing`, subtest failures, `Failing input written to`, and `t.Fatal("context deadline exceeded")` (which carries `file:line:`) all fall outside the whitelist and fail at once; independently, any added, removed, or rewritten corpus file blocks the retry.

Four limits apply. `FUZZ_MAX_RETRIES` defaults to `2` (`0` disables retries); standalone runs may raise it, so the two-target cap holds by default and on any run that counts for landing (`counts_for_landing` turns `false` above `2`). If a parent of `testdata/fuzz/<target>/` is unsearchable both before and after, the snapshot marks it absent rather than failing closed (`go test`, as the same user, cannot write there either, and the log check still applies). The parser assumes `gawk`; under `mawk`, a NUL byte ends a line early and can admit forged logs. Finally, unfamiliar output in a later Go version fails closed until whitelisted, and an unreadable rate never retries.
