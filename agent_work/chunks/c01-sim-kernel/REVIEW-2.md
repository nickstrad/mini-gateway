VERDICT: PASS

## Findings

None. All 7 findings from `REVIEW-1.md` (4 MAJOR, 3 MINOR) and all 4 BROKEN attacks from `agent_work/adversarial/c01-sim-kernel/RESULTS.md` are fixed in `c2e7c1aeca069008206be0fe9cc1f16613dbbb18..e0440347628cb254c1f2dd96d9c9951740c1d8ab`, and no new defects were introduced.

### Repair-round verification (reverted individually in `/tmp/zgw-c01-sim-kernel-review2/mut`)

| Prior item | Fix commit | Revert check result |
| --- | --- | --- |
| Review 1 #1 (MAJOR) + `TestAdv_4_ClosedReaderFreesBlockedWriter` | `571e020` (`internal/sim/simnet/conn.go:202`) | Reverting `e.in().unblock()` fails `TestReceiverCloseWakesABlockedWriterWhenItDiscardsBufferedBytes` and `TestAdv_4_ClosedReaderFreesBlockedWriter/0,1`. |
| Review 1 #2 (MAJOR) + `TestAdv_4_MaximumJitter` | `4c9981f` (`internal/sim/simnet/conn.go:392-397`) | Disabling the `bound == math.MaxInt64` branch fails `TestMaximumJitterDrawsWithoutPanicking` and `TestAdv_4_MaximumJitter` (`panic: invalid argument to Int64N`). |
| Review 1 #2 (MAJOR) + `TestAdv_4_LatencySumSaturates` | `4c9981f` (`internal/sim/simnet/conn.go:389`, `internal/sim/time.go:40-42`) | Replacing `sim.AddDurations(l.Base, p.jitter(l.Jitter))` with `+` fails `TestBasePlusJitterSaturates` and `TestAdv_4_LatencySumSaturates`. |
| Review 1 #2 (MAJOR) + `TestAdv_3_RefusalRoundTripOverflow` | `4c9981f` (`internal/sim/simnet/net.go:139`) | Replacing `sim.AddDurations(c.pipes[up].delay(), c.pipes[down].delay())` with `+` fails `TestRefusalRoundTripSaturates` and `TestAdv_3_RefusalRoundTripOverflow`. |
| Review 1 #3 (MAJOR) — closed listener fault/clear | `32cbf44` (`internal/sim/simnet/fault.go:63,97`) | Reverting `l.closed` in either `applyKind` or `clear` fails `TestFaultsOnAClosedListenerHaveNoTarget`. |
| Review 1 #3 (MAJOR) — unaccepted conn clear | `32cbf44` (`internal/sim/simnet/fault.go:102`) | Reverting `c.accepted` in `clear` fails `TestClearOnAnUnacceptedConnectionHasNoTarget`. |
| Review 1 #4 (MAJOR) — toy `MaxPending` bound | `c264260` (`internal/sim/simtoy/toy.go:350-353`, `ends_test.go:134-156`) | Disabling the per-byte `len(sc.out)+len(sc.line) > MaxPending` check fails `TestServerPendingEchoIsBounded`. Independent scratch probe sending `MaxPending+100` bytes without `\n` resets the connection and passes `CheckQuiesce()`. |
| Review 1 #5 (MINOR) — negative `TraceTail`/`LogTail` | `c5b2f0b` (`internal/sim/trace.go:93-95`) | Disabling `if n <= 0 { return nil }` panics in `TestTraceTailOfZeroOrNegativeIsEmpty` (`makeslice: cap out of range`). |
| Review 1 #6 (MINOR) — `sweep.sh` 1-level `TestScenarioX/seed=N` | `673b304` (`scripts/sweep.sh:50`) | A temporary 1-level `TestScenarioOneLevel` using `simtest.Sweep` passes `scripts/sweep.sh -seeds 2 -run '^TestScenarioOneLevel$'` (`5 seed runs passed`) with the fix and fails (`0 seed runs passed`, exit 1) when the regex is reverted. |
| Review 1 #7 (MINOR) — `MaxSameInstant` at `t = 0` vs `t > 0` and after `RunUntil` | `a839c27` (`internal/sim/sim.go:214,246`) | Reverting `s.sameInstant = 1` fails `TestMaxSameInstantIsTheSameBoundAtZeroAndLater`; reverting `s.sameInstant = 0` in `RunUntil` fails `TestMaxSameInstantRestartsWhenRunUntilMovesTheClock`. Independent scratch probe at `MaxSameInstant == 1` matches at `t = 0` and `t = 5`. |

## Mutation checks

All mutations were run in an isolated scratch copy under `/tmp/zgw-c01-sim-kernel-review2/mut` and reverted after each run. Both mutations that survived in Review 1 (M9 and M11) are now caught.

| # | Property broken | Mutation | Caught? | Catching tests |
| --- | --- | --- | --- | --- |
| M1 | `EvWritable` sent when receiver discards late arrivals (Review 1 M9) | `internal/sim/simnet/conn.go:529`: remove `p.unblock()` in `discardFront` | **CAUGHT** | `TestReceiverCloseWakesABlockedWriterWhenLaterArrivalsAreDiscarded` |
| M2 | Toy server pending line + echo bounded by `MaxPending` (Review 1 M11) | `internal/sim/simtoy/toy.go:350`: disable `len(sc.out)+len(sc.line) > MaxPending` check | **CAUGHT** | `TestServerPendingEchoIsBounded` |
| M3 | Same-instant events run in sequence order | `internal/sim/queue.go:23`: `q[i].seq < q[j].seq` -> `>` | **CAUGHT** | `TestHeapMatchesSortedReference`, `TestTimersFireInDeadlineThenCreationOrder`, `TestSameInstantRunsInSequenceOrder`, `TestLoopAgainstReferenceModel` |
| M4 | Cancelled timer never fires and is removed from heap | `internal/sim/sim.go:133`: skip `t.s.q.remove(t.ev)` (set `t.ev.hi = -1` only) | **CAUGHT** | `TestTimersFireInDeadlineThenCreationOrder`, `TestQuiesceCatchesLeakedTimerButNotBackground`, `TestThousandsOfTimers`, `TestKernelSnapshotCountersMove` |
| M5 | Stream isolation across labels | `internal/sim/stream.go:9`: `fnv64a([]byte(label))` -> `0` | **CAUGHT** | `TestStreamIsolation` |
| M6 | Shrinker returns a 1-minimal failing schedule | `internal/sim/fault.go:178`: return after first successful deletion | **CAUGHT** | `TestShrinkReturnsOneMinimalFailingSchedule`, `TestShrinkEdgeCases`, `TestPlantedBugCaughtAndShrunk` |
| M7 | Histogram quantile within stated 1/128 error bound | `internal/obs/histogram.go:139`: `bucketBounds(i + h.base + 2)` | **CAUGHT** | `TestQuantileErrorBoundAgainstSortedReference`, `TestHistogramRangeAndSize`, `TestGenericRangesForBytesAndCounts`, `TestQuantileArgumentClamped` |
| M8 | Send window bounds in-flight bytes | `internal/sim/simnet/conn.go:424`: `n := len(b); _ = space` | **CAUGHT** | `TestReceiverCloseWakesABlockedWriterWhenItDiscardsBufferedBytes`, `TestReceiverCloseWakesABlockedWriterWhenLaterArrivalsAreDiscarded`, `TestRandomOperationsKeepEveryLaw`, `TestStallFillsWindowThenWriterResumes`, `TestHalfOpenOfTheClientEnd` |
| M9 | Reset discards in-flight bytes and delivers nothing afterwards | `internal/sim/simnet/conn.go:457`: disable `code>>32 != p.epoch \|\| p.c.done` guard | **CAUGHT** | `TestRandomOperationsKeepEveryLaw`, `TestResetDeliversNothingAfterAndNotifiesBothEnds` |
| M10 | Quiesce catches leaked in-flight bytes | `internal/sim/simnet/net.go:158`: disable `InFlight != 0` check | **CAUGHT** | `TestQuiesceCatchesInFlightByte` |

## Release It! checklist

- **Integration points:** Covered for chunk 1's network and toy: `simnet` implements accept-then-silence (`FaultSilence`), dial stall (`FaultDialStall`), stall (`FaultStall`), and half-open (`FaultHalfOpen`); `simtoy` runs every client under a virtual-clock deadline (`DefaultDeadline = 1s`) with scenarios `echo-silence`, `echo-dial-stall`, `echo-stall-timeout`, and `echo-half-open` checked by `TestHalfOpenClientTimesOutOnItsOwnDeadline`.
- **Blocked threads:** No goroutines or OS threads exist in chunk 1; on every exit path in `simnet` and `simtoy`, connections close/reset, blocked writers are woken on peer `Close()` discards (`End.Close` + `discardFront`), and `CheckQuiesce()` asserts zero open/connecting connections, zero bytes in flight, and zero non-background timers.
- **Slow responses:** Not applicable for database work (no proxy or database in chunk 1); in `simtoy`, client timeout closes the connection and cancels its timer, and `TestStallTimeoutServerSeesClientCloseOnlyAfterTheClear` verifies teardown timing.
- **Cascading failures:** Not applicable here because there is no proxy, database pool, circuit breaker, or health surface in chunk 1 (lands in chunks 6–7).
- **Chain reactions:** Not applicable here because there is no multi-instance proxy or load shedding in chunk 1 (lands in chunks 8 and 13).
- **Dogpile:** Not applicable for proxy/database dial rate capping (lands in chunk 7); every `simtoy` fault scenario asserts post-clear recovery via a recovery client (`Scenario.Check`).
- **Self-denial attacks:** Not applicable here because chunk 1 has no bulk reconnect, expiration, retry, or flush loop; `sim` caps runaway event loops via `MaxSteps`, `MaxSameInstant`, and `MaxEvents`.
- **Force multiplier:** Not applicable here because chunk 1 has no adaptive/feedback control loop (lands in chunks 7, 8, 10, 12).
- **Unbalanced capacities:** Bounded at every producer/consumer boundary: `simnet` bounds each pipe buffer to `Options.Window` (default 64 KiB, cap `MaxWindow` 16 MiB) with short-write refusal + `EvWritable` back pressure, listeners to `MaxListeners` (1,024), and connections to `MaxConns` (1,048,576); `sim` bounds the event heap (`MaxEvents`), trace ring (`MaxTraceRing`), fault kinds (`MaxFaultKinds`), schedule length (`MaxScheduleLen`), and registrations (`MaxRegistrations`); `simtoy` bounds per-connection `len(sc.out)+len(sc.line)` to `MaxPending` (1 MiB) on every incoming byte and resets on overflow (`TestServerPendingEchoIsBounded`).
- **Scaling effects:** Not applicable for N-instance guarantees (no proxy in chunk 1); `sim.Snapshot`, `simnet.Snapshot`, and `obs.HistogramSnapshot` contain only fixed scalar fields (and bounded integer buckets) with no unbounded labels.
- **Users:** Not applicable for per-tenant/per-client proxy configuration (lands in chunks 8–9); per-connection pipe memory is bounded by `Options.Window` and total connections by `MaxConns`.
- **Unbounded result sets:** Not applicable for SQL result forwarding (lands in chunk 6); in `simtoy`, server buffering per connection is bounded at `MaxPending` bytes checked per byte in `serverConn.take()`, resetting the connection on overflow rather than forwarding a truncated line.
- **Compounds:** No retry/pool/coalescer interactions exist in chunk 1; back pressure meeting receiver `Close()` or `FaultStall` + receiver `Close()` was tested (`TestReceiverCloseWakesABlockedWriterWhenItDiscardsBufferedBytes`, `TestReceiverCloseWakesABlockedWriterWhenLaterArrivalsAreDiscarded`, `TestAdv_4_ClosedReaderFreesBlockedWriter`, plus a scratch probe combining `FaultStall`, blocked writer, travelling bytes, and receiver `Close()`) and conserves and quiesces cleanly.

## Observability

- **`Snapshot()` present:** `sim.Sim.Snapshot()` (`internal/sim/sim.go:359`), `simnet.Net.Snapshot()` (`internal/sim/simnet/snapshot.go:47`), and `obs.Histogram.Snapshot()` (`internal/obs/histogram.go:179`). Plain structs of integers (plus `[]Bucket` of integers on `HistogramSnapshot`), no wall-clock time, no maps; `obstest.RequirePlain` is asserted on `simnet.Snapshot{}` and `sim.Snapshot{}` in `internal/sim/simtoy/scenario_test.go:124-125`.
- **Counters move in a test:** `TestCountersMove` (`internal/sim/simtoy/scenario_test.go:101-126`) verifies via `obstest.MergeMoved`/`NeverMoved` that every integer field of `simnet.Snapshot` moves in at least one scenario; `TestKernelSnapshotCountersMove` (`internal/sim/sim_test.go:432-470`) verifies every field of `sim.Snapshot`.
- **Conservation asserted:** `simnet` registers `simnet.conservation` (`Written == Delivered + Discarded + InFlight` and `InFlight >= 0`) checked after every step (`internal/sim/simnet/net.go:66,164-175`), plus per-snapshot assertions in `TestCountersMove` and `TestSnapshotIsDeterministicAndConserves`; `TestRandomOperationsKeepEveryLaw` asserts `Closed.* + DialsRefused == DialsAttempted` at quiesce; `TestKernelSnapshotCountersMove` asserts `Fired + Cancelled + HeapLen == Scheduled`; `obs.Conserve` and `obs.Counters.Check` are tested in `internal/obs/obs_test.go`.
- **Snapshot determinism:** `TestScenario` compares `fmt.Sprintf("%+v %+v %+v", res.Net.Snapshot(), res.Sim.Snapshot(), res.Server)` across two runs per seed; `TestSnapshotIsDeterministicAndConserves` compares snapshots across two runs at every event.
- **Exporter mappings / CSV metric registry / CSV-flush assertions / coverage table / health rule:** Not applicable at chunk 1; collector, exporters, CSV adapter, and health rules land in chunks 3 and 6.

## What I ran

All commands run in `/root/Software/mini-gateway-work/c01-review` at commit `e0440347628cb254c1f2dd96d9c9951740c1d8ab` with `PATH=/usr/local/go/bin:$PATH` (Go 1.26.8, linux/amd64):

- `go build ./... && go vet ./...`: pass.
- `go test -count=1 ./...`: pass across all 8 packages (`agent_work/adversarial/c01-sim-kernel` 3.27s, `cmd/mgsim` 0.34s, `internal/obs` 0.24s, `internal/obs/obstest` 0.01s, `internal/sim` 1.92s, `internal/sim/simnet` 2.57s, `internal/sim/simtest` 0.01s, `internal/sim/simtoy` 0.85s).
- `go test -count=1 ./agent_work/adversarial/c01-sim-kernel/...` (1.42s) and `ADV_LONG=1 go test -count=1 -v ./agent_work/adversarial/c01-sim-kernel/...` (3.12s): 34/34 `TestAdv_*` PASS (0 FAIL, 0 SKIP; all 4 previously BROKEN attacks now HELD).
- `DST_SEED=999 go test -count=1 ./internal/sim/simtoy -run '^TestScenario$'`: pass (0.02s).
- `go run ./cmd/mgsim list` (11 scenarios) and `go run ./cmd/mgsim run echo-reset -seed 7` (`steps=153 status=pass`, digests match `testdata/golden-digests.txt`).
- `RUN_DIR=/tmp/zgw-c01-sim-kernel-review2/sweep scripts/sweep.sh -seeds 50`: pass (`550 seed runs passed, 0 failures`).
- `make test` (`scripts/ci-fast.sh`): pass (`G0`, `G1`, `G3` short).
- `GATE_OUT=/tmp/zgw-c01-sim-kernel-review2/gate scripts/gate.sh c01-sim-kernel`: `overall=pass`, 61s (`G0` 5s, `G1` 1s with empty `detlint-allow.txt`, `G2` 0s, `G3` 4s, `G4` markers 0s + sweep 200 seeds 3s, `G5` fuzz 2 targets × 20s 45s, `G6–G8` `skip-not-built`, `G9` site 2s, `G10` extra 1s).
- `go test -coverprofile=/tmp/zgw-c01-sim-kernel-review2/cov.out ./internal/sim ./internal/sim/simnet ./internal/sim/simtoy ./internal/obs ./internal/obs/obstest ./internal/sim/simtest ./cmd/mgsim`: total statement coverage 95.0%; 100.0% statement coverage on every repaired function (`Close`, `delay`, `jitter`, `applyKind`, `clear`, `take`, `AddDurations`, `tail`).
- Line/test counts (`internal/`, `cmd/`): 3,529 product lines, 3,698 test lines, 127 test/fuzz functions (matches `NOTES.md`).
- Provenance check against `/root/Software/object-store` (`379e622..25fb899` and `25fb899..94d2b21`), forbidden-file diff check, `gofmt -l .`, and `git status -s`: all clean.
- Scratch verification in `/tmp/zgw-c01-sim-kernel-review2/mut`: 12 individual revert checks covering all 7 Review 1 findings and all 4 BROKEN attacks (12/12 caught), 10 mutation checks (10/10 caught), and 3 edge-case probes (3/3 pass).
