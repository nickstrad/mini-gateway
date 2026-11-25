VERDICT: FAIL

## Findings

1. **BLOCKER** — `internal/sim/fakepg/exec.go:442-460`, `internal/sim/fakepg/exec.go:489-507`: `rowsAfter()` only checks the final row count at `COMMIT`, not the peak row count during sequential `apply()`, causing a committed transaction to silently drop writes and break `WriteLog` / `world.CheckWrites`.
   - **What is wrong:** Repair commit `6fc0e83` added `b.srv.rowsAfter(tx.writes) > b.srv.cfg.MaxRows` in `commit()` to reject commits that exceed `MaxRows`, then marks the transaction `TxCommitted`, emits `C:COMMIT ZI`, and loops over `tx.writes` calling `b.srv.apply()`. Because `apply()` calls `srv.put()` one write at a time and `srv.put()` returns `ErrTableFull` whenever `len(srv.table) >= srv.cfg.MaxRows`, any transaction whose intermediate row count exceeds `MaxRows` before a later `DELETE` brings the final row count back to `<= MaxRows` passes `rowsAfter()`, commits, and then silently drops the intermediate `INSERT` (or re-creating `UPDATE`) without applying it or recording it in `WriteLog`, while still applying and logging the subsequent `DELETE`. In the `BEGIN; INSERT k2; DELETE k2; COMMIT` variant, `INSERT k2` is silently dropped while `DELETE k2` runs `srv.del("k2")` (returning `false`) and still records a `DELETE` in `WriteLog` and increments `WritesApplied`. This falsifies Claim 3 ("The write log is exact: every applied write once; none missing; none duplicated"), breaks `world.CheckWrites` / `CheckWriteLog`, and violates transactional commit atomicity ("Writes inside a transaction apply at `COMMIT`").
   - **How I know:** Reproduced in `/tmp/zgw-c02-sim-world-review2/src` (`go test -v -count=1 -run '^TestReproR2' ./internal/sim/fakepg`):
     - `TestReproR2PeakRowsExceedsMaxRowsDuringCommitDropsWrite`: with `MaxRows: 2` and initial `[{k1: v1}]`, session A ran `BEGIN; INSERT INTO kv VALUES ('k2', 'v2'); DELETE FROM kv WHERE k = 'k1'`, session B ran `INSERT INTO kv VALUES ('k3', 'v3')`, then session A ran `COMMIT`. Result: `C:COMMIT ZI`, `TxLog: [{TxID:1 Outcome:TxCommitted Writes:2}]`, `Rows: [{k3: v3}]` (`k2` silently lost), `WriteLog` contained only `INSERT k3` and `DELETE k1`, and `world.CheckWrites` failed with `statement 2 wrote a row and committed but is missing from the write log`.
     - `TestReproR2PeakRowsUpdateRecreatedThenDeleteDropsUpdate`: with `MaxRows: 2` and initial `[{k1: v1}, {k2: v2}]`, session A ran `BEGIN; UPDATE kv SET v = 'v1b' WHERE k = 'k1'; DELETE FROM kv WHERE k = 'k2'`, session B ran `DELETE FROM kv WHERE k = 'k1'; INSERT INTO kv VALUES ('k3', 'v3')`, then session A ran `COMMIT`. Result: `C:COMMIT ZI`, `Rows: [{k3: v3}]` (`k1` update silently dropped), `world.CheckWrites` failed with `statement 2 wrote a row and committed but is missing from the write log`.
     - `TestReproR2InsertThenDeleteSameKeyAtCommit`: with `MaxRows: 2` and initial `[{k1: v1}]`, session A ran `BEGIN; INSERT INTO kv VALUES ('k2', 'v2'); DELETE FROM kv WHERE k = 'k2'`, session B ran `INSERT INTO kv VALUES ('k3', 'v3')`, then session A ran `COMMIT`. Result: `C:COMMIT ZI`, `WriteLog` contained `INSERT k3` and `DELETE k2` (deleting a key never inserted) while omitting `INSERT k2`, and `world.CheckWrites` failed with `statement 2 wrote a row and committed but is missing from the write log`.
   - **What a fix must achieve:** Track and return the peak row count reached across `tx.writes` in `rowsAfter()` (e.g., `peak := n; ... if n > peak { peak = n }; return peak`) so any transaction whose sequential `apply()` would exceed `MaxRows` is rolled back with `ERROR 53100` before `TxCommitted` (or apply the transaction atomically without failing mid-loop), ensuring `srv.put()` inside `commit()` never silently drops a write and `world.CheckWrites` passes for all `INSERT`/`UPDATE`/`DELETE` orderings at `MaxRows`. Add regression tests covering `INSERT`+`DELETE` and `UPDATE`+`DELETE` commits whose peak exceeds `MaxRows`.

2. **MINOR** — `internal/sim/workload/client.go:138-144`, `internal/sim/workload/client.go:580-594`: Retried requests re-entering `c.backlog` after backoff can push `len(c.backlog)` above `Tenant.Backlog`.
   - **What is wrong:** When a request fails and is scheduled for retry, `fail()` increments `c.waiting` and removes the request from `c.cur` without placing it in `c.backlog`; new arrivals check `len(c.backlog) >= t.Backlog` and can fill `c.backlog` to `t.Backlog`. When the backoff timer fires, `c.backlog = append([]*request{r}, c.backlog...)` prepends `r` without checking `t.Backlog`, so `len(c.backlog)` can exceed `t.Backlog` by up to the number of concurrently retrying requests (bounded by `t.Requests`). `NotYetSent` stays `<= t.Backlog` because retried requests remain counted in `InFlight`, so conservation holds.
   - **How I know:** Code inspection of `internal/sim/workload/client.go:138-144, 469-475, 580-594`.
   - **What a fix must achieve:** Either account for `c.waiting` in the backlog admission check (`len(c.backlog)+c.waiting >= t.Backlog`) or document that `c.backlog` may hold up to `Tenant.Backlog` unsent arrivals plus retrying in-flight requests.

## Mutation checks

All checks were applied one at a time in a clean scratch copy at `/tmp/zgw-c02-sim-world-review2/src` and reverted after each run. This table covers both core properties and revert-verification of every Review 1 finding and BROKEN adversarial attack.

### Core property mutations

| # | Property mutated | Mutation | Caught? | Failing tests |
| --- | --- | --- | --- | --- |
| M1 | Statement snapshot rule | `fakepg/exec.go`: re-read `b.read(r.st.Key)` inside `readReply()` instead of using the snapshot captured at `beginService()` | **Yes** | `TestReadInServiceDoesNotSeeLaterCommit`, `TestFidelityAgainstRecordedPostgres17` |
| M2 | `max_connections` hard bound | `fakepg/server.go`: change `>= srv.limit()` to `> srv.limit()` | **Yes** | `TestMaxConnectionsRefusesWithRealError` |
| M3 | Cancel secret validation | `fakepg/exec.go`: remove `string(t.secret) != string(secret)` check in `cancel()` | **Yes** | `TestCancelHitsOnlyTheTargetBackend`, `TestClientSendsACancel` |
| M4 | Open-loop intended-send-time latency (narrowed Claim 7) | `workload/client.go`: record `now - r.sentAt` into `c.t.latency` instead of `now - r.intended` | **Yes** | `TestLatencyIsMeasuredFromIntendedSendTime`, `TestOpenLoopArrivalsIgnoreABusyClientAndLatencyIsExact` |
| M5 | Transaction status table | `fakepg/backend.go`: return `'I'` instead of `'E'` for failed transaction in `status()` | **Yes** | `TestTransactionStatusTable` (failed-tx subtests) |
| M6 | Write-log completeness (`CheckWriteLog`) | `fakepg/exec.go`: silently drop every write where `w.stmtSeq % 5 == 0` in `apply()` | **Yes** | `TestScenario/direct-mixed-transactions` |
| M7 | Fault clear recovery (`FaultFragment`, `FaultTrickle`, `FaultBloat`) | `fakepg/fault.go`: make each `clearFault` branch a no-op | **Yes** | `TestEachDatabaseFault/fragment-to-one-byte`, `TestEachDatabaseFault/slow-trickle`, `TestEachDatabaseFault/very-large-and-many-rows` |

### Review 1 and adversarial repair revert-checks

| Item | Reverted change in scratch copy | Caught? | Failing tests |
| --- | --- | --- | --- |
| R1-F1 / Adv-3 (stmt check) | `fakepg/exec.go`: restore `b.tx == nil` guard on `MaxRows` check in `write()` | **Yes** | `TestInsertInsideATransactionRespectsMaxRows` |
| R1-F1 / Adv-3 (commit check) | `fakepg/exec.go`: disable `rowsAfter` check in `commit()` | **Yes** | `TestCommitThatNoLongerFitsIsRolledBack` |
| R1-F1 / Adv-3 (`apply()` guard) | `fakepg/exec.go`: ignore `srv.put` error in `apply()` (note: only bypassed when `rowsAfter` is defective, see Finding 1) | N/A (masked by stmt+commit checks on existing tests) | Caught by Finding 1 repros |
| Adv-2 | `fakepg/exec.go`: remove `b.failMessage()` in `beginMessage()` on `> MaxStatements` | **Yes** | `TestStatementLimitErrorFailsTheTransaction`, `TestAdv_2_StatementLimitErrorAbortsTransaction` |
| R1-F2 / Adv-5 (in-service) | `fakepg/fault.go`: remove `srv.stallInService(extra)` call in `addStall()` | **Yes** | `TestStallWindowExtendsStatementsAlreadyInService`, `TestStallWindowExtendsAFrozenStatement`, `TestAdv_5_StallHitsAlreadyServing` |
| R1-F2 (zero-duration at `From`) | `fakepg/exec.go`: revert `serviceTime()` overlap condition to `start < w.To && end > w.From` | **Yes** | `TestStallWindowCatchesAZeroTimeStatementAtItsStart` |
| R1-F3 | `fakepg/fault.go`: no-op `clearFault` for `FaultFragment`, `FaultTrickle`, `FaultBloat` | **Yes** | `TestEachDatabaseFault` (all 3 subtests) |
| R1-F4 | `fakepg/exec.go`: drop `w.stmtSeq % 5 == 0` in `apply()` | **Yes** | `TestScenario/direct-mixed-transactions` |
| R1-F5 | `fakepg/fault.go`: remove upper clamp `min(..., MaxConnections)` on `FaultMaxConn` | **Yes** | `TestMaxConnFaultCannotRaiseTheConfiguredLimit` |
| R1-F6 (fragment / garbage) | `fakepg/fault.go`: targeted `FaultFragment` resets `fs.fragment`; targeted `FaultGarbage` overwrites `fs.garbageMode` | **Yes** | `TestTargetedFragmentKeepsTheServerWideFragment`, `TestTargetedGarbageKeepsTheServerWideMode` |
| R1-F7 | `fakepg/match.go`: revert pre-multiply bound `n > MaxRowSize/unit` | **Yes** | `TestMatchRowsRefusesOverflowingSizes`, `FuzzMatch/seed#11` |
| R1-F8 (OnCodes / `r.orig`) | `workload/client.go`: call `complete(OutcomeError)` instead of `fail`; drop `r.stmts = r.orig` | **Yes** | `TestRetryOnCodesRetriesAQueryError`, `TestRetriedTransactionRunsItsOriginalStatements` |
| R1-F9 | `fakepg/fault.go`: return `true` when `b.phase != phaseReady` for ready-only faults | **Yes** | `TestReadyOnlyFaultsOnAStartingBackendCountAsNoTarget` |
| R1-F10 | `fakepg/exec.go`: omit `second = []byte("t")` on 2-column `FaultBloat` | **Yes** | `TestBloatOnATwoColumnReadKeepsTheColumnCount` |
| R1-F11 | `fakepg/fault.go`: remove `b.resetTimer` pause in `freeze()` | **Yes** | `TestFreezeHoldsADelayedReset` |
| R1-F12 | `workload/fault.go`: stop storing/cancelling `c.resumeT` on `FaultStopReading` | **Yes** | `TestStopReadingTimerOfAClearedFaultDoesNotResumeALaterOne` |
| R1-Compound1 | `fakepg/fault.go`: clear `b.outBuf = nil` in `terminate()` | **Yes** | `TestTerminateDuringABackPressuredReplyKeepsTheFraming` |

Narrowed claims checked:
- **Claim 7 (narrowed):** True. `arrive()` schedules independently of server speed; wire send waits for previous `ReadyForQuery`; latency is `(final ReadyForQuery) - intended` (preserving `intended` across retries), verified by `TestOpenLoopArrivalsIgnoreABusyClientAndLatencyIsExact` and `TestRetriedRequestKeepsItsIntendedTime`.
- **Claim 11 (narrowed):** True. Outside `tools/`, `find` + `grep` and `TestPgproto3IsImportedOnlyWhereTheRulesAllowInEveryGoFileOutsideTools` confirm only `internal/sim/fakepg/proto.go`, `internal/testclient/msg.go`, and `internal/testclient/session_test.go` import `pgproto3`; `go.mod` has exactly one requirement (`github.com/jackc/pgx/v5 v5.13.0`) and `go mod tidy -diff` is empty.

## Release It! checklist

- **Integration points:** `workload` applies `Tenant.Deadline` virtual-clock timers to both connect/startup (`c.connT`) and in-flight requests (`c.deadlineT`); `fakepg` implements `FaultSilentStartup`, `FaultSilentQuery`, and `FaultConnectStall`, exercised with deadlines in `world/property_test.go`; `workload.Probe` is bounded by caller `RunFor` / `CollectLimit` (`world/fidelity.go`).
- **Blocked threads:** No proxy pool yet; every backend exit path (`finalize`, `doAfter`, `EvReset`, `terminate`, `kill`, `half-open`) cancels timers (`pumpTimer`, `resetTimer`, `r.timer`), releases capacity slots, aborts `cur`, uncounts `sessions`, and removes the backend, and `workload` cancels all timers and closes `end` in `drop()`. Quiesce checks assert zero open connections, zero in-service/waiting statements, zero open transactions, and all clients finished.
- **Slow responses:** On client `Close` (FIN), `fakepg` finishes the running statement then closes; on `Reset`, `fakepg` aborts immediately (`StatementsAborted`) and releases the slot; on `Tenant.Deadline`, `workload` closes and counts `TimedOut`. Refusals (`53300`, `57P03`, `28000`, `3D000`) are answered immediately after startup packet decode and closed.
- **Cascading failures:** Not applicable here because this chunk contains only the simulated database (`fakepg`) and simulated clients (`workload`), not the proxy or health surface (chunks 3, 6–10).
- **Chain reactions:** Not applicable here because there are no proxy instances or shedding mechanisms in this chunk (chunks 8, 13).
- **Dogpile:** `direct-reconnect-burst` measures the unprotected direct-to-DB reconnection storm (peak sessions 100, 517–598 `53300` refusals across pinned seeds); `workload.Retry` uses capped exponential backoff (`Backoff * 2^(attempt-1)` capped at `Cap`, no jitter, off by default). Database protection against dogpiles belongs to chunks 7 and 10.
- **Self-denial attacks:** `workload` intentionally models burst reconnects (`FaultBurst`) and aggressive retries (`Tenant.Retry`, bounded by `Retry.Max`) to test the future proxy; `direct-reconnect-burst` measures the resulting peak sessions and `53300` refusals.
- **Force multiplier:** Not applicable here because this chunk has no adaptive control loop (chunk 10).
- **Unbalanced capacities:** Bounded buffers exist at each boundary: `fakepg` output uses simnet's bounded send window with back-pressure pausing reads, delayed segments capped at `MaxDelayedSegments` (1,024), capacity wait queue bounded by backend count, logs bounded by `LogConfig.Cap` (default 1 Mi, max 16 Mi) or `LogCountOnly`, `MaxReplyBytes` (default 64 MiB), `MaxTxWrites` (65,536); `workload` caps unsent arrivals at `Tenant.Backlog` (default 1,024, max 1 Mi; note Finding 2 regarding retrying requests re-entering `c.backlog`) and `Probe` events at `MaxProbeEvents` (1 Mi).
- **Scaling effects:** Not applicable to multi-instance proxy guarantees (no proxy yet, chunk 13). Metric labels in `workload.Snapshot()` are bounded: per-tenant `ErrorsByCode` is capped at `MaxCodes` (64) with overflow in `ErrorsOtherCodes`; tenant count is fixed by scenario config.
- **Users:** Per-client and per-tenant limits are configured and bounded (`MaxClients` 65,536, `MaxRequests` 16 Mi, `Tenant.Backlog` with drop counter, `MaxConnections` with `53300` refusal and `FaultMaxConn` clamped to `<= Config.MaxConnections`, `MaxSettings` 64, `MaxStatements` 64, `MaxTxWrites` 65,536, `MaxRows` enforced at statement and commit — with the peak-row bug in Finding 1).
- **Unbounded result sets:** `fakepg` streams `KindRows` and `FaultBloat` replies in `outFill` (64 KiB) chunks, guards `matchRows` against `int64` overflow, refuses `KindRows` replies exceeding `Config.MaxReplyBytes` (default 64 MiB) before emitting any row (`ERROR 54000`), and clamps `FaultBloat` to `MaxReplyBytes`. Mid-reply termination (`FaultTerminate`) finishes any already-framed bytes in `b.outBuf`, sends `FATAL 57P01`, and closes without `ReadyForQuery`.
- **Compounds:** Both compounds from Review 1 (`FaultTerminate` under back-pressure tearing framing, and failed-tx `ROLLBACK` mutating `r.stmts` on retry) are fixed; one new compound remains in Finding 1 (concurrent fill to `MaxRows` + transactional `INSERT`/`UPDATE` followed by `DELETE` passes final-count `rowsAfter()` and silently drops writes mid-`COMMIT`).

## Observability

- **`Snapshot()` present and plain:** `fakepg.Server.Snapshot()` (`internal/sim/fakepg/snapshot.go`) and `workload.Workload.Snapshot()` (`internal/sim/workload/snapshot.go`) return plain value structs (checked by `obstest.RequirePlain`), with no wall-clock fields and no map iteration order. Both pass run-twice byte-equality checks (`TestSnapshotsAreIdenticalForOneSeed`, `TestWorkloadSnapshotsAreIdenticalForOneSeed`).
- **Counters move:** Verified in `fakepg/snapshot_test.go:TestSnapshotCountersMoveAndConserve` and `workload/snapshot_test.go:TestWorkloadSnapshotCountersMoveAndConserve`.
- **Conservation asserted:** `fakepg.Snapshot.Check()` (4 laws + `sessionBound`, registered as `fakepg.conservation`) and `workload.TenantSnapshot.Check()` (2 laws, registered as `workload.conservation`) pass across all tests and sweeps. Note: in Finding 1's `INSERT k2; DELETE k2` commit case, `WritesApplied` and `WriteLog` record a `DELETE` for a key never inserted while omitting the `INSERT`.
- **Exporters / CSV registry / health rule:** Not applicable in chunk 2 (exporters, CSV adapter, and health surface are introduced in chunk 3).

## What I ran

All commands run with `PATH=/usr/local/go/bin:$PATH` in `/root/Software/mini-gateway-work/c02-review` (tip `c80f08d9d6bcac6934dcb487d465a41537778bd4`); scratch repros and mutations run in `/tmp/zgw-c02-sim-world-review2/src`.

- `gofmt -l .` — clean (0 files).
- `go mod tidy -diff` — clean (0 diff).
- `go build ./... && go vet ./... && go test -count=1 -cover ./...` — **PASS**
  - `agent_work/adversarial/c01-sim-kernel`: 2.826s
  - `agent_work/adversarial/c02-sim-world`: 8.650s (32 held, 0 broken, 2 narrowed/skipped)
  - `cmd/mgsim`: 0.292s, coverage **63.3%**
  - `internal/randdist`: 0.011s, coverage **71.4%**
  - `internal/sim/fakepg`: 1.767s, coverage **94.1%**
  - `internal/sim/workload`: 0.583s, coverage **88.2%**
  - `internal/sim/world`: 10.632s, coverage **83.3%**
  - `internal/testclient`: 0.015s, coverage **75.7%**
  - (Chunk 1 packages: `obs` 99.1%, `obs/obstest` 94.1%, `sim` 96.0%, `sim/simnet` 96.2%, `sim/simtest` 85.9%, `sim/simtoy` 93.5%)
- Line counts (`find internal/sim/fakepg internal/sim/workload internal/sim/world internal/testclient internal/randdist cmd/mgsim -name '*.go'`): 6,457 non-test lines, 3,863 test lines.
- `GATE_OUT=/tmp/zgw-c02-sim-world-review2/gate GATE_ONLY=G0,G1,G2,G9,G10 scripts/gate.sh c02-sim-world` — **PASS** (G0 tools/gofmt/build/vet/tools-vet/tools-test/tidy; G1 detlint over 9 package patterns, importlint, boundlint, paniccheck; G2 comments vs `42018a93`; G9 site; G10 extra: 10 cross-process scenario replays with identical digests).
- `go run ./cmd/mgsim list` and `go run ./cmd/mgsim run direct-reconnect-burst -seed 7` — **PASS** (16 scenarios listed; seed 7: `steps=40108`, `db.sessions.peak=100`, `db.refused.too_many=517`, `db.connections.accepted=730`, `clients.failed_before_send=130`, `clients.ok=2870`).
- `DST_SEEDS=50 go test -count=1 -run '^TestScenario' ./internal/sim/world` — **PASS** in 57.799s.
- `RUN_DIR=/tmp/zgw-c02-sim-world-review2/tier-r scripts/tier-r.sh` — **PASS** against Postgres 17.11 (4 passed, 0 failed, 0 skipped: fidelity identical, 500 clients held 500 connections, 1,000 identical reads = 1,000 executions, 500-client burst against `max_connections=100`: 100 accepted, 400 refused with `53300`).
- `find . -name '*.go' -not -path './.git/*' -exec grep -l 'pgproto3' {} + | sort` — outside `tools/`, only `internal/sim/fakepg/proto.go`, `internal/testclient/msg.go`, and `internal/testclient/session_test.go` import `pgproto3` (`adv_test.go` and `imports_test.go` only hold the path string).
- Scratch verification script `/tmp/zgw-c02-sim-world-review2/verify.py` + `TestReproR2InsertThenDeleteSameKeyAtCommit` in `/tmp/zgw-c02-sim-world-review2/src` — reproduced Finding 1 across 3 test cases; verified all 7 core mutations and all Review 1 / adversarial repair revert-checks.
- `git status --porcelain` in `/root/Software/mini-gateway-work/c02-review` — clean (0 modified or untracked files).
