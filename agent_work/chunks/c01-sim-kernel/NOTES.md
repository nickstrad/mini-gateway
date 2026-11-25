# Chunk 1 — Simulator kernel: builder notes

Built by Opus on 2026-11-24 on branch `work/c01-sim-kernel` (worktree
`/root/Software/mini-gateway-work/c01`) from `/root/briefs/zgateway-p2-c01-sim-kernel.md`.
Durable facts for later builders: `docs/knowledge/sim-kernel.md`; copied-file record:
`docs/knowledge/sim-kernel-provenance.md`.

## What was built

| Deliverable | Where |
| --- | --- |
| Time, streams, heap | `internal/sim/time.go`, `stream.go`, `queue.go` |
| Opaque-event loop, timers, invariants, quiesce, kernel snapshot | `internal/sim/sim.go`, `errors.go` |
| Decision trace, ring, split digests | `internal/sim/trace.go` |
| Fault schedule, `ScheduleFromBytes`, `Shrink` | `internal/sim/fault.go` |
| Byte-stream connections | `internal/sim/simnet/` (`options.go`, `net.go`, `conn.go`, `fault.go`, `snapshot.go`) |
| Seeds and replay plumbing | `internal/sim/simtest/` (`seeds.go`, `determinism.go`) |
| Measurement half of obs | `internal/obs/histogram.go`, `snapshot.go`; test helpers `internal/obs/obstest/` |
| Toy line-echo protocol and its scenarios | `internal/sim/simtoy/` (`toy.go`, `scenarios.go`), pinned seeds and golden digests in `testdata/` |
| CLI | `cmd/mgsim/main.go` |
| Scripts | `scripts/sweep.sh`, `scripts/fuzz.sh` (markers `sweep`, `fuzz` deleted); `make test` runs the CI set (marker `make-test` deleted) |
| Gate extra (G10) | `agent_work/chunks/c01-sim-kernel/gate/extra.sh` |
| Rules files | `tools/lint-rules/det-packages.txt` (four packages); `race-packages.txt` unchanged (no concurrency in this chunk) |

Size (`find internal cmd -name '*.go'`), after the repair round: product 3,529 lines (of
which 380 are test plumbing: `simtest` 265, `obstest` 115), tests 3,698 lines, 127 test and
fuzz functions.

## Kernel API in one screen

- `sim.New(sim.Config{Seed, Scenario, Replay, MaxSteps, MaxSameInstant, MaxEvents, TraceOff, TraceRing})`.
- `At(t, fn)`, `After(d, fn)`, `AtHandler(t, h, code)`, `AfterHandler(d, h, code)` return a
  `Timer` value: `Cancel() bool`, `Pending()`, `When()`, `MarkBackground()`.
- `Step()`, `RunUntil(t)`, `RunFor(d)`, `RunUntilQuiet()`. Errors are sticky; the first wins.
- `RegisterInvariant`, `RegisterSampledInvariant(name, every, fn)`, `CheckInvariants`,
  `Verify`; `RegisterQuiesce`, `CheckQuiesce`; `RegisterState(name, func(sim.StateSink))`.
- `Trace(kind, a, b, c, d)`, `SetTraceTap`, `TraceTail`, `LogTail`, `LogLen`, `WriteLog`,
  `TraceDigest`, `StateDigest`, `Digests`.
- `RegisterFaultKind`, `InjectAt`, `InjectSchedule`, `ApplyFault`; package functions
  `ScheduleFromBytes(data, Space)` and `Shrink(schedule, stillFails)`.
- `Snapshot()`: steps, scheduled, fired, cancelled, heap length and peak, pending and
  background timers, trace records and ring length, faults applied, invariant checks.

## Decisions the brief left open

1. **Kernel loop.** A `Handler` is `OnEvent(code uint64)`; an event carries either a closure
   or a handler plus code. Events scheduled in the past run at `now`. `RunUntil(t)` runs
   every event at or before `t`, then sets the clock to `t`. `RunUntilQuiet` runs until only
   background timers remain.
2. **Stall errors.** One type, `*sim.StallError{Cause, Seed, Steps, Now, Replay}`, with
   causes `ErrBudgetExhausted` (more than `MaxSteps` steps, default 50,000,000),
   `ErrTimeFrozen` (more than `MaxSameInstant` consecutive steps at one instant, default
   1,000,000; the first step at an instant counts as one, at time zero as at any later
   instant, and `RunUntil` moving the clock to `t` restarts the count) and `ErrHeapFull` (more than `MaxEvents` queued, default 4,194,304).
3. **Panic guard.** `*sim.PanicError{Value, Seed, Steps, Now, Replay}`; no stack (needs
   `runtime/debug`, which the determinism lint does not allow). Invariant and quiesce
   functions run under the same guard.
4. **REPLAY line.** The kernel carries one: `Config.Replay`, else
   `REPLAY: go run ./cmd/mgsim run <scenario> -seed N`. Tests pass
   `simtest.ReplayCommand(t, seed)` so a failure names the `go test` command.
5. **Trace record.** 48 bytes hashed per record: time, an 8-byte kind tag, four `int64`.
   No sequence number in the record, so a fault on one connection does not shift another
   connection's records. Ring default 256, capped at 65,536. Trace off: nothing hashed,
   nothing kept, the tap not called; the trace digest is then the digest of nothing.
6. **State digest.** Registered writers only, in registration order (a name, then the
   writer's integers and strings). The kernel adds nothing of its own (steps and fault
   counts would make a cleared fault visible). simnet writes connection states, byte
   counters and per-connection pipe state, and NOT its fault counters.
7. **Fault schedule.** `Fault{At, Kind, Target, Arg1, Arg2}`. Kinds are registered per
   `Sim` (at most 64), not in a global registry, so the decoder's kind list never depends
   on which packages a binary links. simnet owns kinds 1–9.
8. **`ScheduleFromBytes(data, Space)`.** The brief wrote `ScheduleFromBytes([]byte)`; the
   decoder needs the kind list, target count and time window, so it takes a `Space`.
   8 bytes per fault: kind index, target, 16-bit time scaled into `[Start, Start+Window)`,
   two 16-bit arguments reduced modulo `MaxArg+1`; a short tail is zero-padded; the result
   is sorted by time (stable) and has at most `MaxFaults` (default 64, cap 1,024) entries.
   An empty kind list decodes to an empty schedule.
9. **Shrinker.** Repeated passes of delete-one-fault until a full pass removes nothing, so
   the result is 1-minimal for any predicate (monotone or not). At most n passes of n calls.
10. **Determinism list.** `internal/sim`, `internal/sim/simnet`, `internal/sim/simtoy`,
    `internal/obs` one by one, not `internal/sim/...`: `simtest` uses `os`, `flag`,
    `regexp`, `path/filepath` and `testing`, and allow-listing it would take dozens of
    lines. `detlint-allow.txt` stays empty.
11. **Histogram layout.** Values 0–255 exact; above that 128 sub-buckets per power of two
    (`bits.Len64`). A histogram covers `[lo, hi]` with `bucketIndex(hi) - bucketIndex(lo) + 1`
    counters. Duration histogram: 1 µs to 60 s in ns, 3,302 buckets (26.4 KB). Out-of-range
    values are recorded in the edge bucket and counted in `ClampedLow`/`ClampedHigh`.
    `Quantile(bp)` takes basis points (`Percentile(p)` = `Quantile(100p)`), uses rank
    `ceil(n·bp/10000)` (at least 1) and returns that bucket's upper bound clipped to `hi`.
12. **Stated error bound.** For a value `v` in range the reported quantile `q` satisfies
    `v <= q` and `(q - v) * 128 <= v` (below 0.79%, inside the brief's 1%); values below 256
    are exact. Test: `TestQuantileErrorBoundAgainstSortedReference`.
13. **`obstest`.** Added beside `internal/obs` (not in the determinism list) because the
    "counters must move" and "plain snapshot" checks need `reflect`.
14. **Toy client writes whole lines only.** It waits (`WaitWritable`) when a line does not
    fit, unless the whole window is free; so without chunking no line ever straddles a
    delivery boundary and the planted bug cannot fire in a control run.
15. **Pinned seeds.** `internal/sim/simtoy/testdata/seeds.txt`: 1, 2, 3, 7, 42. Default
    `DST_SEEDS` is 3 (as copied), so `go test` runs pinned plus 1–3.

## Fault semantics (simnet) — who sees what, in what order

Words: the **client end** dialed, the **server end** was accepted. **Up** is client to
server, **down** is server to client. A **pipe** is one direction. **In flight** for a pipe
= bytes accepted by `Write` and not yet read by the receiver or discarded (bytes travelling,
bytes held by a stall, bytes delivered but unread). Events reach an actor only through its
`Handler`, each as its own loop event, never inside the call that caused it. A Readable,
Writable or Connected event already pending for an end is not queued twice.

**Normal operation.**
- Dial: at `now + up latency` the server end exists and the listener's accept function is
  called with it; at that time `+ down latency` the client gets `EvConnected`. Bytes the
  server writes before that are buffered for the client and announced (`EvReadable`) right
  after `EvConnected`.
- Write: accepts `min(len, window - in flight)` bytes; a short write (including 0) counts
  one `WritesRefused` and arms one `EvWritable`, sent when the receiver next consumes or
  discards bytes (including the bytes its own `Close()` discards, and bytes arriving after
  it closed), as long as the writer is still open and has not sent its FIN. `WaitWritable()` arms it without writing. Each accepted write is cut into
  pieces by the chunking mode (whole; fixed size, down to 1; random: between
  ceil(n/64) and n bytes per piece, from the pipe's own stream), and each piece arrives at
  `max(now + latency draw, previous arrival on this pipe)`, so order is kept under jitter.
- Read: returns up to `len(b)` delivered bytes; `(0, nil)` when nothing is ready;
  `(0, io.EOF)` once the peer's FIN arrived and every byte before it was read.
- `CloseWrite()` (**half-close**): a FIN queued behind the pending bytes on the outgoing
  pipe; later writes return `ErrClosed`; the end can still read.
- `Close()`: half-close, then stop reading: unread incoming bytes, and any that arrive later,
  are discarded (counted `Discarded`, never `Delivered`). The connection closes
  (`Graceful`) when both ends have closed; then anything still in flight is discarded.

**Faults** (each one registered on the loop, applied at its instant, recorded in the trace
as kind `fault` with `(kind, target, arg1, arg2)`; a fault whose target does not exist, is
closed or is not yet accepted is counted `NoTarget` and does nothing; this holds for
`clear` (9) too: a clear naming a closed listener, or a connection that is closed or not
yet accepted, is `NoTarget`, not `Clear`).

| Fault (kind) | Target, args | Client end sees | Server end sees | Clear |
| --- | --- | --- | --- | --- |
| reset (1) | connection | `EvReset` at the fault instant, first; then Read/Write/Close return `ErrReset` | `EvReset` at the same instant, after the client's; same errors | nothing to clear |
| `End.Reset()` by one end | — | if the server reset: `EvReset` now | if the client reset: `EvReset` now | — |
| (both resets) | | All bytes in flight in both directions are discarded at the instant; no byte is delivered or read after it. Closed reason `Reset`. | | |
| stall (2) | connection; arg1 direction mask (1 up, 2 down, 0 or 3 both); arg2 duration in ns, 0 = until cleared | nothing; writes still accepted up to the window | nothing | releases every held byte (and a held FIN) at the clear instant, in order, as one arrival |
| (stall) | | During the stall pieces and FINs that arrive are held; when it ends they are delivered at once. The window still counts them, so a writer blocks after `window` bytes. | | |
| half-open (3) | connection; arg1 = the end that is **gone** (0 client, 1 server) | if the server is gone: no event ever; writes accepted until the window is full, then 0; bytes already sent by the server still arrive; no FIN, no reset | if the client is gone: the same, mirrored | nothing to clear (a gone host does not come back) |
| (half-open) | | The gone end gets no event and its calls return `ErrGone`. The connection closes (reason `HalfOpen`) when the survivor calls `Close`, or with reason `Reset` when it calls `Reset`; held bytes are then discarded. If both ends are gone (half-open of a silenced connection) it closes at once. | | |
| half-close (4) | connection; arg1 = the end whose write side is shut | (target client) its writes return `ErrClosed`; it still reads | (target server) the same; the client then reads the server's pending bytes and `io.EOF` | nothing to clear |
| chunk (5) | connection; arg1 direction mask; arg2 piece size (0 means 1) | — | — | restores the connection's own chunking for later writes |
| (chunk) | | Writes made after the fault are cut into pieces of that size; bytes already accepted keep their pieces. | | |
| refuse (6) | listener index | every dial made while it is on gets `EvRefused` after one round trip (up + down latency); Write returns `ErrRefused` | never sees the dial | later dials are accepted |
| dial stall (7) | listener index | a dial made while it is on never completes: no event, Write returns `ErrNotConnected`; the client's `Close` or `Reset` aborts it (reason `Aborted`) | never sees the dial | later dials complete; already stalled dials stay stalled until aborted |
| accept-then-silence (8) | listener index | a dial made while it is on gets `EvConnected`; writes accepted up to the window; nothing ever comes back | the accept function is never called; the end is owned by the network and never reads or writes | later dials are accepted normally; silenced connections stay silent and close (reason `Silent`) when the client closes |
| clear (9) | connection or listener; arg1 = the kind to clear | — | — | — |

A dial to a name with no listener (or a closed one) is refused like refuse (6).
`Close()` on a dial that is still connecting aborts it; if the server end was already
accepted it gets `EvReset`.

## Claims and the tests that prove them

| # | Claim | Tests (file: function) |
| --- | --- | --- |
| 1 | Replay law in process | `internal/sim/simtoy/scenario_test.go`: `TestScenario` (every scenario, every seed, run twice through `simtest.RequireDeterministic`; snapshots compared as text), `TestDifferentSeedsGiveDifferentTraces`; `internal/sim/sim_test.go`: `TestTraceDigestsAndRing` |
| 1 | Replay law across processes | `cmd/mgsim/main_test.go`: `TestCrossProcessReplay` (re-executes the test binary as `mgsim`), `TestTraceOutputRepeatsAcrossProcesses`; gate `extra.sh` (built binary, two processes, every scenario) |
| 1 | Golden digests, reviewed update path | `scenario_test.go`: `TestGoldenDigests` (`testdata/golden-digests.txt`; rewrite with `go test ./internal/sim/simtoy -run TestGoldenDigests -update`) |
| 1 | REPLAY line reproduces a failure | `simtest_test.go`: `TestReplayCommand`, `TestReportPrintsTailPinAndReplay`; `scenario_test.go`: `TestPlantedBugCanary`, `TestStepBudgetAndPanicAreTypedFailures`; `main_test.go`: `TestFailureExitsOneWithReplay`; `sweep.sh` reprints the first REPLAY line |
| 2 | Stream isolation | `sim_test.go`: `TestStreamIsolation`; per-connection streams: `simnet/semantics_test.go`: `TestFaultOnOneConnectionLeavesAnotherByteIdentical` |
| 3 | Same-instant events in sequence order | `sim_test.go`: `TestSameInstantRunsInSequenceOrder`, `TestTimersFireInDeadlineThenCreationOrder`, `TestLoopAgainstReferenceModel` |
| 3 | A cancelled timer never fires, and leaves the heap | `sim_test.go`: `TestTimersFireInDeadlineThenCreationOrder`, `TestThousandsOfTimers`, `TestHeapMatchesSortedReference`, `TestTimerHandleSemantics` |
| 3 | Time never overflows | `sim_test.go`: `TestTimeSaturates`, `TestScheduleWindowAndArguments`, `TestAddDurationsSaturates`; `simnet/latency_test.go`: `TestBasePlusJitterSaturates`, `TestRefusalRoundTripSaturates`, `TestMaximumJitterDrawsWithoutPanicking` |
| 4 | Bytes out = bytes in, in order, every chunking and latency | `simnet/simnet_test.go`: `TestBytesDeliveredInOrderForEveryChunking`; `random_test.go`: `TestRandomOperationsKeepEveryLaw`; `semantics_test.go`: `TestAsymmetricLatencyAndJitterAreSeeded`, `TestRandomChunkingBoundsPieceCount` |
| 4 | Window bounds in-flight bytes; back pressure | `simnet_test.go`: `TestStoppedReaderBlocksWriterAtWindow` (and the window invariant in `newPair`); `random_test.go`; `semantics_test.go`: `TestWaitWritableWakesTheWriter`, `TestWritableNeverFiresForAWriterThatWasNotRefused`, `TestStallFillsWindowThenWriterResumes`; `closewake_test.go`: `TestReceiverCloseWakesABlockedWriterWhenItDiscardsBufferedBytes`, `TestReceiverCloseWakesABlockedWriterWhenLaterArrivalsAreDiscarded` |
| 5 | Each fault behaves as defined above, both ends | `simnet_test.go`: `TestResetDeliversNothingAfterAndNotifiesBothEnds`, `TestLocalResetNotifiesOnlyThePeer`, `TestHalfOpenSurvivorNeverLearns`, `TestStallHoldsBytesThenResumesInOrder`, `TestDialRefusedStalledAndSilenced`, `TestHalfCloseFaultSendsFinAfterPendingBytes`; `semantics_test.go`: `TestHalfOpenOfTheClientEnd`, `TestBytesAlreadyInFlightFromAGoneEndStillArrive`, `TestStallOneDirectionOnly`, `TestChunkFaultOverridesAndClears`, `TestAbortOfAcceptedButUnconnectedDialResetsServer`, `TestResetOfAnAcceptedConnectingDial`, `TestWriteAfterCloseWriteAndReadAfterClose`, `TestCloseDiscardsUnreadAndLaterArrivals`, `TestConnectedPrecedesReadableEvenWhenServerWritesFirst`, `TestHandshakeTakesOneRoundTripOnTheVirtualClock`; `target_test.go`: `TestFaultsOnAClosedListenerHaveNoTarget`, `TestClearOnAnUnacceptedConnectionHasNoTarget`; `simtoy/ends_test.go`: `TestEachFaultAsSeenByBothEnds`, `TestStallTimeoutServerSeesClientCloseOnlyAfterTheClear`, `TestHalfOpenClientTimesOutOnItsOwnDeadline` |
| 6 | A fault on one connection changes nothing on another | `simtoy/scenario_test.go`: `TestFaultOnOneConnectionLeavesOthersIdentical` (trace records and bytes of connections 2 and 3, every connection fault); `simnet/semantics_test.go`: `TestFaultOnOneConnectionLeavesAnotherByteIdentical` |
| 6 | A cleared fault leaves no residue | `simtoy/scenario_test.go`: `TestClearedFaultLeavesNoResidue` (stall, chunk, refuse, dial stall, silence injected and cleared before traffic: traffic trace digest and state digest equal to a never-faulted run); `simnet/semantics_test.go`: `TestStateDigestHasNoResidueAfterClear`; `TestClearedListenerFaultsLeaveNewDialsNormal` |
| 7 | Quiesce catches a leaked connection, byte or timer | `simtoy/scenario_test.go`: `TestQuiesceCatchesLeaks`, `TestQuiesceCatchesInFlightByte`; `simnet_test.go`: `TestQuiesceCatchesLeakedConnectionAndByte`; `sim_test.go`: `TestQuiesceCatchesLeakedTimerButNotBackground` |
| 8 | Histogram bound, merge law, clamps, no allocation | `internal/obs/obs_test.go`: `TestRecordedValueFallsInItsBucket`, `TestQuantileErrorBoundAgainstSortedReference`, `TestMergeEqualsRecordingBoth`, `TestClampsAreCounted`, `TestRecordAllocatesNothing`, `TestGenericRangesForBytesAndCounts` |
| 9 | Shrinker result still fails and is 1-minimal | `sim/fault_test.go`: `TestShrinkReturnsOneMinimalFailingSchedule`; `simtoy/scenario_test.go`: `TestPlantedBugCaughtAndShrunk` (40 decoded faults shrink to 1 chunk fault; bound asserted: at most 2) |
| 10 | No wall clock, goroutines, global random, floats or map order in `internal/sim`, `simnet`, `simtoy`, `internal/obs` | gate G1 detlint over `tools/lint-rules/det-packages.txt` with an empty allow file |
| — | Step budget turns a livelock into the typed stall error | `sim_test.go`: `TestStepBudgetTurnsLivelockIntoStall` (both causes); `simtoy`: `TestStepBudgetAndPanicAreTypedFailures` |
| — | Actor panic becomes a typed failure with a REPLAY line | `sim_test.go`: `TestPanicBecomesTypedFailure`; `simtoy`: `TestStepBudgetAndPanicAreTypedFailures` |
| — | Trace off vs on: same state digest | `sim_test.go`: `TestTraceDigestsAndRing`; `simtoy`: `TestTraceOffKeepsStateDigest` |
| — | `ScheduleFromBytes` total and bounded; decoded schedules never break the kernel | `sim/fault_test.go`: `FuzzScheduleFromBytes`, `TestScheduleFromBytesIsTotal`; `simtoy`: `FuzzToySchedule`, `TestDecodedSchedulesNeverBreakTheKernel` |

**Planted-bug canary.** The switch is the unexported `Config.plantBug` of `simtoy` (only
tests in that package can set it): the server drops the last byte of any line whose bytes
arrived in more than one read. `TestPlantedBugCanary` requires `echo-chunk-1` and
`echo-random-split` to FAIL with it on (seeds 1–5) with a REPLAY line; every ablation run in
`TestScenario` requires the same seed without chunking to PASS with the bug on. A second
switch, `panicOnEOF`, plants an actor panic.

## End-to-end scenarios (toy echo, every simnet fault)

Each runs 3 clients (connections 1–3; connection faults target connection 1) plus one
recovery client after the fault is cleared, on the pinned seeds plus `DST_SEEDS`, twice
per seed. `Scenario.Check` asserts: the expected outcome class for the targeted clients and
`completed` for the others; the recovery client `completed`; quiesce; no kernel error;
the scenario's fault counter in the simnet snapshot is non-zero (and the clear's, if any);
bytes moved both ways.

| Scenario | Fault | Expected outcome (targeted clients) | Ablation |
| --- | --- | --- | --- |
| echo-clean | none | completed | — |
| echo-random-split | random chunking (configuration) | completed | more pieces than the control; bug fires here, not in control |
| echo-chunk-1 | chunk to 1 byte at 2.5 ms, cleared at 4 ms | completed | same |
| echo-stall | stall both ways at 2.5 ms for 20 ms | completed, but finishing after the stall ends | control finishes before the stall would have ended |
| echo-stall-timeout | stall until cleared at 1.5 s | timed out (own 1 s deadline) | — |
| echo-reset | reset at 2.5 ms (and one at 30 ms with no target) | reset | — |
| echo-half-open | server end gone at 2.5 ms | timed out | — |
| echo-half-close | server write side shut at 2.5 ms | early EOF | — |
| echo-refuse | listener refuses from 0 to 30 ms | refused (all) | — |
| echo-dial-stall | listener stalls dials from 0 to 1.5 s | timed out (all) | — |
| echo-silence | accept-then-silence from 0 to 1.5 s | timed out (all) | — |

`TestEachFaultAsSeenByBothEnds` pins, per scenario, what the server saw (accepts, resets)
and the simnet close reasons. `TestCountersMove` requires every simnet snapshot counter to
be non-zero at some event of some scenario (`Up.WritesRefused` too) and checks
`written = delivered + discarded + in flight` at every event;
`TestKernelSnapshotCountersMove` does the same for the kernel snapshot.

## Observability

- `simnet.Snapshot()`: listeners; dials attempted, refused, stalled; connections opened,
  open, connecting; closed by reason (graceful, reset, half-open, silent, aborted); per
  direction written, delivered, discarded, in flight, writes refused, pieces; faults by kind
  plus clears and no-target. Conservation `written = delivered + discarded + in flight` is a
  registered invariant checked after every event.
- `sim.Snapshot()` (the steady-state row): heap length and peak, pending and background
  timers, trace records and ring length; `scheduled = fired + cancelled + queued`.
- `obs.Histogram`, `obs.Conserve`/`Counters.Check` (the error names each term and which side
  is higher), `obs.Gauge` (value and peak), `obs.Snapshotter`.
- No snapshot holds a time or a map.

## Steady-state row (PLAN.md section 10.1)

| What grows | What removes it | Gauge |
| --- | --- | --- |
| Event heap | event execution, `Timer.Cancel` (real removal) | `sim.Snapshot().HeapLen`, `PendingTimers`, `BackgroundTimers`, `HeapPeak` |
| Trace ring | ring overwrite (bounded, default 256) | `TraceRingLen` |
| Per-connection buffers | reads, `Close`/`Reset`/fault discards; bounded by the window | `simnet.Snapshot().Up/Down.InFlight`, `ConnsOpen` |
| Connection table | never shrinks within a run (ids index it); bounded by `MaxConns` = 1,048,576 | `DialsAttempted` |

## Limits

- One connection table entry per dial for the whole run (`MaxConns`); a dial past it fails
  the run. Listeners: 1,024.
- Stale delivery events of a closed connection stay in the heap until their time comes (they
  then do nothing); the heap is bounded by `MaxEvents`.
- Random chunking makes at most 64 pieces per write; fixed chunking of size 1 makes one
  event per byte (a 1 MB write is a million events).
- `Write` copies the bytes; per-pipe memory is bounded by the window (default 64 KiB, cap
  16 MiB).
- A latency draw is `Base + uniform[0, Jitter]` in ns, both inclusive, with any
  non-negative `Base` and `Jitter` up to `math.MaxInt64`; the sum saturates at
  `math.MaxInt64` ns, as does a refused dial's round trip (up + down). An arrival that
  saturates happens at `sim.MaxTime`. No other distribution yet.
- `Stream(label)` returns a new stream each call; callers keep it.
- Replay is claimed for same commit, same toolchain (Go 1.26.8), linux/amd64.
- The toy server's partial line plus pending echo is capped at `MaxPending` (1 MiB),
  checked at every byte as it arrives; one byte past it the server resets the connection
  (the line is not echoed and not counted in `Lines`).

## Deviations

- **Size.** Product Go is 3,529 lines against "about 1,500, budgeted up to twice that". It is
  complete; no SPLIT-REQUEST. 380 of those lines are test plumbing (`simtest`, `obstest`).
- **Tests vs product.** Tests are 3,698 lines, more than the 3,529 product lines.
- **`ScheduleFromBytes` signature** takes a `Space` (decision 8).
- **Determinism list** names four packages instead of `internal/sim/...` (decision 10).
- **No panic stack** in `PanicError` (decision 3).
- **`obstest`** is an extra package (decision 13).
- The object-store HEAD moved during the build; the copied files are identical at every
  commit involved (see the provenance page).

## Commands

```
export PATH=/usr/local/go/bin:$PATH
go test ./internal/... ./cmd/...                              # all tests (about 5 s)
go test -short ./...                                          # the CI budget
go test ./internal/sim/simtoy -run '^TestScenario$' -v        # the e2e scenarios on pinned seeds
DST_SEEDS=1000 go test ./internal/sim/simtoy -run '^TestScenario$'
go test ./internal/sim/simtoy -run TestGoldenDigests -update   # rewrite golden digests after review
go run ./cmd/mgsim list
go run ./cmd/mgsim run echo-reset -seed 7 [-trace]
scripts/sweep.sh [-seeds N] [-run REGEXP]                     # default 200 seeds, ^TestScenario
FUZZ_SECONDS=20 scripts/fuzz.sh
make test                                                     # scripts/ci-fast.sh
scripts/gate.sh c01-sim-kernel
```

Results on the committed tree (vm-1, Go 1.26.8): `scripts/gate.sh c01-sim-kernel` overall
pass, counts_for_landing true, 70 s (G4 sweep 5 s, G5 fuzz 47 s, G10 extra 2 s; G6–G8
skip-not-built). `scripts/sweep.sh -seeds 1000`: 11,000 seed runs passed in 17 s.
`TestRandomOperationsKeepEveryLaw` was also run once at 3,000 seeds: pass.

## Repair round

Opus, 2026-11-24, from `REVIEW-1.md` (FAIL: 4 major, 3 minor) and
`agent_work/adversarial/c01-sim-kernel/RESULTS.md` (4 broken). Each item was reproduced
first (the regression test failed on the pre-repair code; the adversary's test failed too);
then fixed. No claim was narrowed, no finding disputed, nothing declined.

| Item | Result |
| --- | --- |
| Review 1 (MAJOR): receiver `Close` discards without waking a blocked writer | fixed in `571e020` (`End.Close` calls `unblock` after `discardAll`); regression `simnet/closewake_test.go`: `TestReceiverCloseWakesABlockedWriterWhenItDiscardsBufferedBytes` (failed before: space 10, wakes 0); `TestReceiverCloseWakesABlockedWriterWhenLaterArrivalsAreDiscarded` covers the `discardFront` path (already correct; it kills mutation M9) |
| Review 2 (MAJOR): latency arithmetic overflows; `Jitter == MaxInt64` panics | fixed in `4c9981f` (`sim.AddDurations` saturates; `pipe.jitter` draws `Int64()` for the maximum bound; `Dial` saturates the refusal round trip); regressions `simnet/latency_test.go`: `TestMaximumJitterDrawsWithoutPanicking`, `TestBasePlusJitterSaturates`, `TestRefusalRoundTripSaturates`; `sim_test.go`: `TestAddDurationsSaturates` |
| Review 3 (MAJOR): faults on a closed listener and clears on an unaccepted connection not `NoTarget` | fixed in `32cbf44` (`applyKind` and `clear` treat a closed listener as no target; `clear` needs `c.accepted`); regressions `simnet/target_test.go`: `TestFaultsOnAClosedListenerHaveNoTarget`, `TestClearOnAnUnacceptedConnectionHasNoTarget`; NOTES fault paragraph now says `clear` follows the same rule |
| Review 4 (MAJOR): toy `MaxPending` cap untested and the partial line unbounded | fixed in `c264260` (`take` checks `len(out)+len(line) > MaxPending` at every byte, then resets; the later echo check is subsumed and removed); `simtoy/ends_test.go`: `TestServerPendingEchoIsBounded` rewritten: a precondition proves seed 1's first line exceeds `MaxPending`, then it asserts outcome `reset`, `Closed.Reset == 1`, nothing echoed and no line counted; it failed before the fix (`lines 1`) and fails with the cap removed (mutation M11 now caught) |
| Review 5 (MINOR): `TraceTail`/`LogTail` panic on negative n | fixed in `c5b2f0b` (`tail` returns nil for n <= 0); regression `sim_test.go`: `TestTraceTailOfZeroOrNegativeIsEmpty` |
| Review 6 (MINOR): `sweep.sh` misses one-level `TestScenarioX/seed=N` | fixed in `673b304` (regex `--- PASS: TestScenario[^ ]*/seed=`); checked against both line shapes; `sweep.sh -seeds 20` still counts 231 runs |
| Review 7 (MINOR): `MaxSameInstant` off by one between t = 0 and t > 0 | fixed in `a839c27` (the first step at a new instant counts as 1); while there, `RunUntil` moving the clock with no event left the count of the previous instant in place, so the first instant after it was short; that now restarts at 0. Regressions `sim_test.go`: `TestMaxSameInstantIsTheSameBoundAtZeroAndLater` (failed before: 6 ran at t = 3), `TestMaxSameInstantRestartsWhenRunUntilMovesTheClock` (failed before) |
| Attack `TestAdv_3_RefusalRoundTripOverflow` | fixed in `4c9981f` (same as review 2); passes |
| Attack `TestAdv_4_MaximumJitter` | fixed in `4c9981f`; passes |
| Attack `TestAdv_4_LatencySumSaturates` | fixed in `4c9981f`; passes |
| Attack `TestAdv_4_ClosedReaderFreesBlockedWriter` | fixed in `571e020` (same as review 1); passes in both directions |

Golden digests are unchanged: no scenario reaches any of the repaired paths.

### Narrowed claims

None.

