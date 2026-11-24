VERDICT: FAIL

## Findings

1. **MAJOR** — `internal/sim/simnet/conn.go:197-203`, `internal/sim/simnet/conn.go:523-528` (`End.Close` / `pipe.discardAll`): Closing the receiving end discards all buffered/in-flight bytes without waking a writer blocked on a full send window.
   - **What is wrong:** `NOTES.md` (line 114) specifies that a short or zero `Write` "arms one `EvWritable`, sent when the receiver next consumes or discards bytes." When the receiver calls `Close()` while the sender is still open and blocked (`p.blocked == true`), `End.Close()` calls `e.in().discardAll()`, emptying `p.buf` (`Space()` returns to `Window()`) and counting those bytes as `Discarded`, but `discardAll()` never calls `p.unblock()`. The sender remains `stOpen` (`maybeFinish()` does not finish the connection) and never receives `EvWritable`. By contrast, bytes arriving *after* `Close()` go to `discardFront()` (`conn.go:514-521`), which does call `p.unblock()`, and mutation M9 showed that no existing test covers `unblock()` on discard at all.
   - **How I know:** Reproduced in a scratch test under `/tmp/zgw-c01-sim-kernel-review1/mut` (`TestReproCloseDoesNotWakeBlockedWriter`): client fills a 10-byte window and gets a 0-byte refused write (`p.blocked = true`), server calls `Close()` without reading, `Up.Discarded` becomes 10 and `Space()` becomes 10, yet after advancing 10 ms `EvWritable` count is 0.
   - **What a fix must achieve:** When `End.Close()` discards bytes from `e.in()` and the connection remains open (`!e.c.done`), wake the blocked peer via `e.in().unblock()`. Add tests asserting `EvWritable` is delivered both when receiver `Close()` discards buffered bytes (`discardAll`) and when subsequent arrivals are discarded (`discardFront`).

2. **MAJOR** — `internal/sim/simnet/conn.go:382-388` (`pipe.delay`), `internal/sim/simnet/net.go:139` (`Net.Dial`): Signed 64-bit integer overflow in latency arithmetic and panic on `Jitter == math.MaxInt64`, violating Claim 3 ("time never overflows").
   - **What is wrong:**
     1. `pipe.delay()` evaluates `p.rng.Int64N(int64(l.Jitter) + 1)`. When `l.Jitter == math.MaxInt64`, `int64(l.Jitter) + 1` wraps to `math.MinInt64` and `Int64N` panics (`invalid argument to Int64N`).
     2. `l.Base + time.Duration(p.rng.Int64N(...))` wraps negative when `Base + jitter > math.MaxInt64`, causing `Now().Add(negative)` to clamp to `Now()` so the event fires immediately instead of saturating at `sim.MaxTime`.
     3. `net.go:139` computes `c.pipes[up].delay() + c.pipes[down].delay()` without saturation; if the sum exceeds `math.MaxInt64`, `at` wraps negative and `EvRefused` fires at `Now()`.
   - **How I know:** Reproduced in `/tmp/zgw-c01-sim-kernel-review1/mut`: `TestReproMaxInt64JitterPanics` panicked with `invalid argument to Int64N`; `TestReproDelayOverflowAndMaxJitterPanic` with `Base = math.MaxInt64 - 10, Jitter = 100` opened the connection within 1,000 ns instead of saturating at `MaxTime`.
   - **What a fix must achieve:** Saturate `Base + jitter` and `up.delay() + down.delay()` (never wrap negative), handle `Jitter == math.MaxInt64` without panicking, and add unit tests for both cases.

3. **MAJOR** — `internal/sim/simnet/fault.go:61-67`, `internal/sim/simnet/fault.go:96-111`, `internal/sim/simnet/net.go:95-100`: Fault targeting contradicts `NOTES.md`'s `NoTarget` rule (Claim 5) for closed listeners and not-yet-accepted connections.
   - **What is wrong:** `NOTES.md` (lines 128–129) defines: "a fault whose target does not exist, is closed or is not yet accepted is counted `NoTarget` and does nothing."
     1. `applyKind()` (`fault.go:62`) and `clear()` (`fault.go:97`) look up listeners via `n.listener(f.Target)`, which returns closed listeners (`l.closed == true`). Injecting `FaultRefuse`, `FaultDialStall`, `FaultSilence`, or `FaultClear` against a closed listener mutates it and increments the fault/`Clear` counter instead of `NoTarget`.
     2. `clear()` (`fault.go:102`) checks `c != nil && !c.done` for `FaultStall`/`FaultChunk`, omitting `!c.accepted` (present in `applyKind()` at line 70). Clearing a fault on a connecting, not-yet-accepted connection increments `Faults.Clear` instead of `NoTarget`.
   - **How I know:** Reproduced in `/tmp/zgw-c01-sim-kernel-review1/mut`: `TestReproClosedListenerFaultNotCountedNoTarget` got `{Refuse:1 Clear:1 NoTarget:0}` instead of `NoTarget:2`; `TestReproClearOnUnacceptedConnNotCountedNoTarget` got `{Clear:1 NoTarget:0}` instead of `NoTarget:1`.
   - **What a fix must achieve:** Treat `l == nil || l.closed` as no-target in both `applyKind` and `clear`, require `c.accepted` in `clear` for connection faults, and test both cases.

4. **MAJOR** — `internal/sim/simtoy/ends_test.go:134-148`, `internal/sim/simtoy/toy.go:346-364` (`TestServerPendingEchoIsBounded` / `serverConn.take`): Test cannot fail when the `MaxPending` bound is removed, and `sc.line` is unbounded prior to `\n`.
   - **What is wrong:**
     1. `TestServerPendingEchoIsBounded` configures `Seed: 1, Lines: 1, MaxLine: MaxPending + 10`. Because line length is `1 + r.IntN(MaxPending + 10)`, seed 1 generates a line well below `MaxPending`, so `toy.go:359-363` (`if len(sc.out)+len(echo) > MaxPending`) has 0 executions in `go test -coverprofile`.
     2. Line 142 checks `if res.Server.Resets != 0 && res.Outcomes[0] != OutcomeReset`. `Server.Resets` is only incremented on incoming `EvReset` (`toy.go:318`), never when the server itself calls `sc.e.Reset()` (`toy.go:361`), so `res.Server.Resets` is always 0 and the reset check is dead code even if the cap were hit.
     3. `serverConn.take()` (`toy.go:349`) appends to `sc.line` without checking `MaxPending` until `\n` arrives; a peer sending bytes without `\n` can grow `sc.line` past `MaxPending`.
   - **How I know:** `go test -coverprofile=/tmp/zgw-c01-sim-kernel-review1/cov.out ./internal/sim/simtoy` reports `toy.go:359.41,363.4 3 0` (0 hits). Mutation M11 (disabling the `MaxPending` check in `toy.go:359`) passed all tests in `./internal/sim/simtoy`.
   - **What a fix must achieve:** Bound `len(sc.out) + len(sc.line)` against `MaxPending` as bytes arrive in `take()`, and make `TestServerPendingEchoIsBounded` deterministically exceed `MaxPending` and unconditionally assert `res.Outcomes[0] == OutcomeReset` and `res.Net.Snapshot().Closed.Reset == 1` (and verify the test fails when the cap is removed).

5. **MINOR** — `internal/sim/trace.go:92-102` (`trace.tail`): `TraceTail(n)` and `LogTail(n)` panic on negative `n`.
   - **What is wrong:** `tail(n int)` clamps `n > len(t.ring)` but does not guard `n <= 0`, so `make([]Record, 0, n)` panics with `runtime error: makeslice: cap out of range` for any `n < 0`.
   - **How I know:** Reproduced in `/tmp/zgw-c01-sim-kernel-review1/mut` (`TestReproNegativeTraceTailPanics`).
   - **What a fix must achieve:** Return `nil` when `n <= 0`, with a test for `n <= 0`.

6. **MINOR** — `scripts/sweep.sh:50`: Passed-run regex requires two `/` path components and misses 1-level `TestScenarioX/seed=N` tests.
   - **What is wrong:** Line 50 uses `grep -cE -- '--- PASS: TestScenario[^ /]*/[^ ]+/seed='`, which matches `TestScenario/echo-clean/seed=1` but fails to match `TestScenarioBaseline/seed=1`. Both `scripts/sweep.sh:7` and `docs/knowledge/sim-kernel.md:44` document that any test named `TestScenario...` using `simtest.Sweep` works with `sweep.sh`. A 1-level `TestScenarioFoo` run via `scripts/sweep.sh -run '^TestScenarioFoo$'` will report `passed=0` and exit 1.
   - **How I know:** `echo "--- PASS: TestScenarioBaseline/seed=1 (0.01s)" | grep -cE -- '--- PASS: TestScenario[^ /]*/[^ ]+/seed='` outputs `0`.
   - **What a fix must achieve:** Change the regex to `--- PASS: TestScenario[^ ]*/seed=` so both 1-level and nested `TestScenario*` subtests are counted.

7. **MINOR** — `internal/sim/sim.go:212-220` (`Sim.Step`): Off-by-one discrepancy in `MaxSameInstant` between `t = 0` and `t > 0`.
   - **What is wrong:** At `t > 0` (`e.at > s.now`), `s.sameInstant` is reset to `0` and not incremented for the first event at that instant, permitting `MaxSameInstant + 1` steps before `ErrTimeFrozen`. At `t = 0` (`s.now == 0`), the first event enters the `else` branch and increments `s.sameInstant` to `1`, permitting only `MaxSameInstant` steps.
   - **How I know:** Inspection of `sim.go:212-220`.
   - **What a fix must achieve:** Count the first step at each instant consistently (e.g. set `s.sameInstant = 1` on `e.at > s.now`) so `MaxSameInstant` enforces the same bound at `t = 0` and `t > 0`.

## Mutation checks

All mutations were applied in an isolated scratch copy under `/tmp/zgw-c01-sim-kernel-review1/mut` and reverted after each run.

| # | Property broken | Mutation | Caught? | Catching tests |
| --- | --- | --- | --- | --- |
| M1 | Same-instant sequence ordering | `internal/sim/queue.go:23`: `q[i].seq < q[j].seq` -> `>` | **CAUGHT** | `TestHeapMatchesSortedReference`, `TestTimersFireInDeadlineThenCreationOrder`, `TestSameInstantRunsInSequenceOrder`, `TestLoopAgainstReferenceModel` |
| M2 | Cancelled timer removed from heap | `internal/sim/sim.go:133`: skip `t.s.q.remove(t.ev)` | **CAUGHT** | `TestTimersFireInDeadlineThenCreationOrder`, `TestQuiesceCatchesLeakedTimerButNotBackground`, `TestThousandsOfTimers`, `TestKernelSnapshotCountersMove` |
| M3 | Stream isolation across labels | `internal/sim/stream.go:9`: `fnv64a([]byte(label))` -> `0` | **CAUGHT** | `TestStreamIsolation` |
| M4 | Shrinker 1-minimality | `internal/sim/fault.go:179`: return after first removal | **CAUGHT** | `TestShrinkReturnsOneMinimalFailingSchedule`, `TestShrinkEdgeCases`, `TestPlantedBugCaughtAndShrunk` |
| M5 | Histogram quantile error bound (<= 1/128) | `internal/obs/histogram.go:139`: `bucketBounds(i + h.base + 2)` | **CAUGHT** | `TestQuantileErrorBoundAgainstSortedReference`, `TestHistogramRangeAndSize`, `TestGenericRangesForBytesAndCounts`, `TestQuantileArgumentClamped` |
| M6 | Histogram merge preserves clamp counts | `internal/obs/histogram.go:113`: drop `h.clampedLow += o.clampedLow` | **CAUGHT** | `TestMergeEqualsRecordingBoth`, `TestMergeIntoEmptyAndFromEmpty` |
| M7 | Send window bounds in-flight bytes | `internal/sim/simnet/conn.go:415`: `n := len(b); _ = space` | **CAUGHT** | `TestRandomOperationsKeepEveryLaw`, `TestStallFillsWindowThenWriterResumes`, `TestHalfOpenOfTheClientEnd`, `TestSnapshotIsDeterministicAndConserves`, `TestBytesDeliveredInOrderForEveryChunking`, `TestStoppedReaderBlocksWriterAtWindow` |
| M8 | Reset delivers no bytes afterwards | `internal/sim/simnet/conn.go:448`: disable `code>>32 != p.epoch \|\| p.c.done` guard | **CAUGHT** | `TestRandomOperationsKeepEveryLaw`, `TestResetDeliversNothingAfterAndNotifiesBothEnds` |
| M9 | `EvWritable` sent when receiver discards bytes | `internal/sim/simnet/conn.go:520`: remove `p.unblock()` in `discardFront` | **SURVIVED** | None (see Finding 1) |
| M10 | Quiesce catches in-flight bytes | `internal/sim/simnet/net.go:158`: disable `InFlight != 0` check | **CAUGHT** | `TestQuiesceCatchesInFlightByte` |
| M11 | Toy server pending echo bounded by `MaxPending` | `internal/sim/simtoy/toy.go:359`: disable `len(sc.out)+len(echo) > MaxPending` check | **SURVIVED** | None (see Finding 4) |
| M12 | Pipe FIFO arrival monotonicity under jitter | `internal/sim/simnet/conn.go:392`: disable `if at < p.lastAt` clamp | **CAUGHT** | `TestBytesDeliveredInOrderForEveryChunking`, `TestEachFaultAsSeenByBothEnds`, `TestLinesLongerThanTheWindow`, `TestScenario` |
| M13 | Invariants checked after every step | `internal/sim/sim.go:229`: skip `s.checkInvariants(false)` | **CAUGHT** | `TestPanicBecomesTypedFailure`, `TestInvariantsCheckedAfterEveryEventAndSampled`, `TestKernelSnapshotCountersMove` |
| M14 | Zero allocations per `Histogram.Record` | `internal/obs/histogram.go:93`: heap-escaping `allocSink = make([]byte, 32)` | **CAUGHT** | `TestRecordAllocatesNothing` (`1 allocations per Record`) |

## Release It! checklist

- **Integration points:** Covered for chunk 1's toy and network: `simnet` provides accept-then-silence (`FaultSilence`), dial stall (`FaultDialStall`), stall (`FaultStall`), and half-open (`FaultHalfOpen`), and `simtoy` runs every client under a virtual-clock deadline (`DefaultDeadline = 1s`) with named scenarios (`echo-silence`, `echo-dial-stall`, `echo-stall-timeout`, `echo-half-open`) verified in `TestHalfOpenClientTimesOutOnItsOwnDeadline`.
- **Blocked threads:** No OS threads or goroutines in chunk 1; on every exit path of `simtoy` and `simnet` connections close/reset and `CheckQuiesce()` asserts zero open/connecting connections, zero bytes in flight, and zero non-background timers (see Finding 1 for a blocked writer missing `EvWritable` on peer `Close()`).
- **Slow responses:** Not applicable for database work (no proxy or database in chunk 1); in `simtoy`, client deadline closes the connection and cancels its timer, and `TestStallTimeoutServerSeesClientCloseOnlyAfterTheClear` verifies teardown timing.
- **Cascading failures:** Not applicable here because there is no proxy, database pool, circuit breaker, or health endpoint in chunk 1 (lands in chunks 6–7).
- **Chain reactions:** Not applicable here because there is no multi-instance proxy or load shedding in chunk 1 (lands in chunks 8 and 13).
- **Dogpile:** Not applicable for proxy/database dial capping (lands in chunk 7); every `simtoy` fault scenario asserts a post-clear recovery client completes (`Scenario.Check`).
- **Self-denial attacks:** Not applicable here because chunk 1 has no bulk reconnect, expiration, retry, or flush loop; `sim` bounds event storms via `MaxSteps`, `MaxSameInstant`, and `MaxEvents`.
- **Force multiplier:** Not applicable here because chunk 1 contains no automatic feedback/adaptive control loop (lands in chunks 7, 8, 10, 12).
- **Unbalanced capacities:** `simnet` bounds every pipe buffer to `Options.Window` (default 64 KiB, cap `MaxWindow` 16 MiB) with short-write refusal + `EvWritable` back pressure (except the `Close()` discard wake-up bug in Finding 1); `sim` bounds the event heap (`MaxEvents`), trace ring (`MaxTraceRing`), fault kinds (`MaxFaultKinds`), schedule length (`MaxScheduleLen`), and registrations (`MaxRegistrations`); `simnet` bounds listeners (`MaxListeners`) and connections (`MaxConns`); `simtoy` intends `MaxPending` (1 MiB) on server pending echo, but see Finding 4 (`sc.line` unbounded before `\n` and cap untested).
- **Scaling effects:** Not applicable for N-instance guarantees (no proxy in chunk 1); `simnet` and `sim` snapshots contain only fixed scalar struct fields with no unbounded labels.
- **Users:** Not applicable for per-tenant/per-client proxy config (no tenants or proxy in chunk 1); per-connection buffers are bounded by `Options.Window` and total connections by `MaxConns`.
- **Unbounded result sets:** Not applicable for SQL result forwarding (lands in chunk 6); in `simtoy`, `MaxPending` is meant to cap buffered echo memory per connection, but `sc.line` is not checked before `\n` and the cap is untested (Finding 4).
- **Compounds:** No retry/pool/coalescer interactions exist in chunk 1; the only compound defect found here is a writer blocked on back pressure meeting peer `Close()` (Finding 1), where `Close()` discards the pipe without waking the writer.

## Observability

- **`Snapshot()` present:** `sim.Sim.Snapshot()` (`internal/sim/sim.go:358`), `simnet.Net.Snapshot()` (`internal/sim/simnet/snapshot.go:47`), and `obs.Histogram.Snapshot()` (`internal/obs/histogram.go:179`). Plain structs of integers (plus `[]Bucket` of integers on `HistogramSnapshot`), no wall-clock time, no maps. `obstest.RequirePlain` is asserted on `simnet.Snapshot{}` and `sim.Snapshot{}` in `internal/sim/simtoy/scenario_test.go:124-125`.
- **Counters move in a test:** `TestCountersMove` (`internal/sim/simtoy/scenario_test.go:101-126`) checks via `obstest.MergeMoved`/`NeverMoved` that every integer field of `simnet.Snapshot` is non-zero in at least one scenario; `TestKernelSnapshotCountersMove` (`internal/sim/sim_test.go:432-470`) checks every field of `sim.Snapshot`.
- **Conservation asserted:** `simnet` registers `simnet.conservation` (`Written == Delivered + Discarded + InFlight` and `InFlight >= 0`) checked after every step (`internal/sim/simnet/net.go:66, 164-175`), plus per-snapshot checks in `TestCountersMove` and `TestSnapshotIsDeterministicAndConserves`; `TestRandomOperationsKeepEveryLaw` asserts `Closed.* + DialsRefused == DialsAttempted` at quiesce; `TestKernelSnapshotCountersMove` asserts `Fired + Cancelled + HeapLen == Scheduled`; `obs.Conserve` and `obs.Counters.Check` are tested in `internal/obs/obs_test.go`.
- **Snapshot determinism:** `TestScenario` compares `fmt.Sprintf("%+v %+v %+v", res.Net.Snapshot(), res.Sim.Snapshot(), res.Server)` between two runs of every scenario and seed; `TestSnapshotIsDeterministicAndConserves` compares snapshots across two runs at every event.
- **Exporter mappings / CSV registry / CSV-flush assertions / coverage table / health rule:** Not applicable at chunk 1; collector, exporters, CSV adapter, and health rules land in chunks 3 and 6.

## What I ran

All commands run in `/root/Software/mini-gateway-work/c01-review` (commit `19edca0f05e46a5762b9abf8833ee16fc06252c0`) with `PATH=/usr/local/go/bin:$PATH` (Go 1.26.8):

- `go build ./... && go vet ./... && go test -count=1 ./...`: pass (7 packages: `cmd/mgsim` 0.58s, `internal/obs` 0.38s, `internal/obs/obstest` 0.02s, `internal/sim` 2.33s, `internal/sim/simnet` 2.80s, `internal/sim/simtest` 0.01s, `internal/sim/simtoy` 0.98s).
- `RUN_DIR=/tmp/zgw-c01-sim-kernel-review1/sweep scripts/sweep.sh -seeds 50`: pass (550 seed runs passed, 0 failures).
- `GATE_OUT=/tmp/zgw-c01-sim-kernel-review1/gate scripts/gate.sh c01-sim-kernel`: `overall=pass` (`G0` 11s, `G1` 2s, `G2` 0s, `G3` 6s, `G4` sweep 200 seeds 5s, `G5` fuzz 2 targets × 20s 52s, `G6–G8` `skip-not-built`, `G9` site 3s, `G10` extra 3s).
- `go test -coverprofile=/tmp/zgw-c01-sim-kernel-review1/cov.out ./internal/sim/simtoy ./internal/sim ./internal/sim/simnet ./internal/obs`: pass; coverage `simtoy` 92.2% (`toy.go:359-363` 0 hits), `sim` 95.9%, `simnet` 95.9%, `obs` 99.1% (separate `go test -cover ./...`: `cmd/mgsim` 98.3%, `obstest` 94.1%, `simtest` 85.9%).
- `go run ./cmd/mgsim list` (11 scenarios), `go run ./cmd/mgsim run echo-reset -seed 7` (`status=pass`, digests matched golden file), `DST_SEED=999 go test -count=1 ./internal/sim/simtoy -run '^TestScenario$'` (pass, 0.02s), `make test` (`scripts/ci-fast.sh`, pass).
- Provenance check against `/root/Software/object-store`: `git diff --stat 379e622 25fb899` and `25fb899 94d2b21` over copied paths returned empty (matches `docs/knowledge/sim-kernel-provenance.md`).
- Product / test line counts (`internal/`, `cmd/`): 3,508 product lines, 3,458 test lines (matches `NOTES.md`).
- Defect repros and 14 mutation checks in `/tmp/zgw-c01-sim-kernel-review1/mut`: 6/6 repros confirmed defects; 12/14 mutations caught, 2 survived (M9, M11).
