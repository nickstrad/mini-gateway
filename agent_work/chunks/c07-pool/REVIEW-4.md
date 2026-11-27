VERDICT: NITS

## Review 3 findings — status

- **Finding 1 (BLOCKER, `pool-admission-restart` failing on ~1 in 5 seeds):** FIXED (`687cbaa`) — `AdmissionRestartSpec()` raises `RatePerSec` to `1000` (`admissionRestartDialRate`) on a copy of `*sp.Pool`, leaving `checkAdmissionRestart` and `test/analysis/pool-admission-restart/admission-pool.sql` untouched (`git diff aede6b0..b987964` empty on both); verified across `DST_SEEDS=200` (20-seed cap), all 200 distinct seeds `1..200` (`shed{breaker}` in `[3, 5]`, `shed{rule} == 0` on every seed), and seeds `42`, `99`, `1001` with 0 SQL failures. Holds by construction on every seed (minor inaccuracy in the written `NOTES.md` / `pool-core.md` proof recorded as Finding 1 below).
- **Finding 1 sibling (half-open trials firing immediately from `tPool` when gate refusals set `wanted`):** VERIFIED INTENDED AND BOUNDED (`c177aa7`) — `Allow` setting `wanted = true` on gate refusals matches chunk 7 decision 6; `trialSent` limits each half-open period to one trial, `breaker.open` resets `wanted = false` and `trialSent = false` and enforces `breaker.open_for` spacing, idle periods with no refusal send zero trials, and dial trials pass through `Cap`, `MaxConnecting`, and `RatePerSec`; held by `TestRefusedArrivalsStartOneTrialPerHalfOpenPeriod`.
- **Finding 2 (MAJOR, dropping either chunk's block from `Engine.Invariants()` not caught):** FIXED (`ccf14c9`) — `TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount` now requires both `"sim: invariant"` and `"admission:"`, and `internal/proxy/join_test.go` adds `TestJoinInvariantsRunTheAdmissionChecksOnAnEngineWithThePoolCore` and `TestJoinInvariantsRunThePoolCoreChecksOnAnEngineWithTheAdmissionQueue`; both M3a and M3b now fail.
- **Finding 3 (MINOR, deleting `e.sweepAdmission()` from `Engine.Shutdown()` not caught):** FIXED (`ccf14c9`) — `TestJoinShutdownSweepsTheLastQueuedClientAtItsDeadline` (both `LIFOOff` and `LIFOAdaptive`, pool core and admission queue active) fails under M2.
- **Review 3 M8 (MISSED, deleting `e.sweepAdmission()` from `Engine.refuseAdmitted()` not caught):** FIXED (`e742621`) — `TestJoinBreakerOpeningAtARuleDeadlineAnswersTheDueShedBeforeTheRefusals` fails under M8.

## Findings

1. **MINOR** — `agent_work/chunks/c07-pool/NOTES.md:586-605`, `docs/knowledge/pool-core.md:138`
   - **What is wrong:** Both the "Why `pool-admission-restart` now holds on every seed" proof in `NOTES.md` and the new bullet in `docs/knowledge/pool-core.md` state that `pool-admission-restart` uses the default `breaker.failures` of **five** ("the breaker opens at the fifth failure", "five dial failures, 50 ms apart", "The breaker needs five failures and at most four can come from connections that existed at the restart (cap 4), so at least one dial must fail", "At 1000 dials/s five refused dials take about 4 ms plus five round trips: the breaker opens at most 2.84 ms after P"). In code, `AdmissionRestartSpec()` inherits `corePool()` (`test/e2e/c07_scenarios.go:33`), which sets **`BreakerFailures: 3`**, not `5`. Step 2 of the proof (`5 > 4` connections) is therefore false for the actual config, and line 601 contradicts itself ("about 4 ms ... at most 2.84 ms").
   - **How I know:** Inspected `test/e2e/c07_scenarios.go:31-38,114-140` and verified `AdmissionRestartSpec().Pool.BreakerFailures == 3` in a scratch probe (`TestProbeReview4BreakerFailuresAndSeeds` in `/tmp/zgw-c07-pool-review4/repo`). The timing numbers confirm 3 failures, not 5: at 20 dials/s (50 ms GCRA spacing), 3 failed dials take 2 × 50 ms + 1 RTT ≈ 100.8 ms (5 dials would take 4 × 50 ms ≈ 200.8 ms); at 1000 dials/s (1 ms spacing), 3 failed dials take 2 × 1 ms + 1 RTT ≤ 2.84 ms (5 dials would take ≥ 4 ms). The scenario still holds on every seed by construction: with 8 clients on `Start = c*1ms`, `Delay = 50ms`, zero fake service time, and `NetOptions` latency `100µs ± 50µs` per leg, at `t = 2.000s` every client is in its 50 ms think time between step 38 (~1.98 s) and step 39 (~2.03 s), so 0 connections are lent at the restart (all 4 idle connections close politely as `peer_closed_idle`, adding 0 breaker failures) and all 3 failures come from dials in ≤ 3 ms against the 100 ms rule interval.
   - **What a fix must achieve:** Update `agent_work/chunks/c07-pool/NOTES.md:586-605` and `docs/knowledge/pool-core.md:138` to state `BreakerFailures: 3` (from `corePool()`), replace step 2's `5 > 4` reasoning with the actual reason 0 lent connections fail at `t = 2.000s` (clients are in think time so all 4 connections are idle and close as `peer_closed_idle`), and correct the "about 4 ms" / "five dial failures" arithmetic to 3 dials (2 intervals + RTT). (Alternatively, set `BreakerFailures = 5` in `AdmissionRestartSpec()` if the `5 > 4` bound itself is desired.)

## Mutation checks

All mutations were applied in a scratch copy at `/tmp/zgw-c07-pool-review4/repo` (repository `/root/Software/mini-gateway-work/c07-review` left untouched).

- **M3a — Drop `e.admissionCheck()` from `Engine.Invariants()`** (`internal/proxy/engine.go:415-417`): **CAUGHT** by `internal/proxy` `TestJoinInvariantsRunTheAdmissionChecksOnAnEngineWithThePoolCore` (`a breaker flush counted as served passed Engine.Invariants: <nil>`) and `test/e2e` `TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount` (`the plant was caught by something other than the admission accounting: e2e: check failed: the admission queue saw 964 pushes, served 964, refused 0 for the breaker`).
- **M3b — Drop `e.coreAgrees()` and `e.core.p.Invariants()` from `Engine.Invariants()`** (`internal/proxy/engine.go:418-423`): **CAUGHT** by `internal/proxy` `TestJoinInvariantsRunThePoolCoreChecksOnAnEngineWithTheAdmissionQueue` (`a pool above max_connecting passed Engine.Invariants: <nil>`). *(Note: dropping only `e.coreAgrees()` while keeping `e.core.p.Invariants()` passes `TestJoinInvariants...` because that test breaks `pool.Invariants` via `PlantNoRate`; `coreAgrees()` remains covered by chunk 7's engine tests.)*
- **M2 — Delete `e.sweepAdmission()` from `Engine.Shutdown()`** (`internal/proxy/engine.go:145`): **CAUGHT** by `internal/proxy` `TestJoinShutdownSweepsTheLastQueuedClientAtItsDeadline` (`off: the queued client at its deadline got "", want 53M01`).
- **M8 — Delete `e.sweepAdmission()` from `Engine.refuseAdmitted()`** (`internal/proxy/corepool.go:572`): **CAUGHT** by `internal/proxy` `TestJoinBreakerOpeningAtARuleDeadlineAnswersTheDueShedBeforeTheRefusals` (`at the deadline: client 11 53M01 at 2, client 13 53M03 at 1, want the due shed first`).
- **M9 — Drop `p.brk.trialSent ||` guard in `Pool.maybeDial()`** (`internal/pool/pool.go:341`): **CAUGHT** by `internal/pool` `TestRefusedArrivalsStartOneTrialPerHalfOpenPeriod` (`period 1: a second trial [3] while the first is in flight`).
- **M10 — Drop `b.wanted = false` in `breaker.open()`** (`internal/pool/breaker.go:77`): **CAUGHT** by `internal/pool` `TestRefusedArrivalsStartOneTrialPerHalfOpenPeriod` (`a half-open period with no refused arrival sent a trial [7], breaker half-open`).

## Release It! checklist

Scoped to the join repair round (`aede6b0..b987964`: test-only and scenario-config changes; no product code outside `test/e2e/c07_scenarios.go` changed):

- **Integration points:** Not applicable here because the repair round adds no socket reads, writes, or transport paths.
- **Blocked threads:** `TestJoinShutdownSweepsTheLastQueuedClientAtItsDeadline` verifies `Shutdown` at the exact admission deadline sweeps the last queued client, closes every pool connection (`Open == 0`, `Connecting == 0`), and passes `Invariants()` and `Quiesce()`.
- **Slow responses:** `TestJoinBreakerOpeningAtARuleDeadlineAnswersTheDueShedBeforeTheRefusals` verifies that when a connect deadline opens the breaker at the exact instant a rule shed is due, the due `53M01` shed is answered before the `53M03` breaker refusals and both counters conserve.
- **Cascading failures:** `TestRefusedArrivalsStartOneTrialPerHalfOpenPeriod` verifies gate refusals while open (`Allow == false`) emit zero dials during `open_for` and trigger at most one trial per half-open period, keeping outage load O(1).
- **Chain reactions:** Not applicable here because multi-instance failover is chunk 13.
- **Dogpile:** `TestRefusedArrivalsStartOneTrialPerHalfOpenPeriod` verifies 50 arrivals while a half-open trial is in flight start zero additional trials (`trialSent` guard) and a subsequent quiet period with no refusal starts zero trials (`wanted` cleared on re-open); all trials also pass through `Cap`, `MaxConnecting`, and `RatePerSec`.
- **Self-denial attacks:** `AdmissionRestartSpec()` raises `RatePerSec` to `1000` solely for the `pool-admission-restart` test scenario (isolated copy of `*sp.Pool`); `RestartSpec` keeps `20/s`, and `pool.Config` defaults are unchanged.
- **Force multiplier:** Not applicable here because the repair round adds no adaptive feedback loop.
- **Unbalanced capacities:** Not applicable here because the repair round adds no buffer or queue.
- **Scaling effects:** Not applicable here because the repair round adds no metric, label, or fleet calculation.
- **Users:** Not applicable here because the repair round adds no per-client or per-tenant state.
- **Unbounded result sets:** Not applicable here because the repair round touches no reply forwarding path.
- **Compounds:** Raising `RatePerSec` to `1000` in `AdmissionRestartSpec()` eliminates the timing race between dial pacing and the 100 ms admission timeout rule across all 200 seeds (`shed{rule} == 0`, `shed{breaker}` in `[3, 5]`); `Engine.Invariants()` composition of both chunks' invariant blocks is now held by both unit tests and the simulator canary.

## Observability

Scoped to the join repair round (PLAN.md section 7.6):

- **`Snapshot()` present:** No snapshot struct changed in the repair round; existing `pool.Snapshot`, `admission.Snapshot`, and `proxy.Snapshot` remain intact (`docs/csv-schema.md` verified current by `scripts/gen-docs.sh --check`).
- **Counters move and conservation holds:** Verified in `TestRefusedArrivalsStartOneTrialPerHalfOpenPeriod` (`BreakerTrials`, `BreakerTransitions[TransOpenHalfOpen]`, `Dials`), `TestJoinShutdownSweepsTheLastQueuedClientAtItsDeadline` (`Pushes == Served + ShedTotal + Removed`, `Shed[ReasonRule] == engine.Shed == 1`), and `TestJoinBreakerOpeningAtARuleDeadlineAnswersTheDueShedBeforeTheRefusals` (`Shed[ReasonRule] == 1`, `Shed[ReasonBreaker] == engine.BreakerFlushed == 1`).
- **CSV-flush assertions:** `test/analysis/pool-admission-restart/admission-pool.sql` is unchanged (`git diff aede6b0..b987964` empty) and passes with 0 failures over all 200 distinct seeds `1..200`, pinned seeds `42`, `99`, `1001`, and the 20-seed sweep; all 28 pool and admission scenario directories in the 20-seed sweep pass their SQL files with 0 failures.

## What I ran

Line-by-line review performed:
- Inspected all 7 commits in `aede6b0..b987964` (`687cbaa`, `ccf14c9`, `c177aa7`, `e742621`, `c3ba3ea`, `86537ce`, `b987964`) across all 6 touched files (`test/e2e/c07_scenarios.go`, `test/e2e/c07_canary_test.go`, `internal/proxy/join_test.go`, `internal/pool/pool_test.go`, `docs/knowledge/pool-core.md`, `agent_work/chunks/c07-pool/NOTES.md`).
- Verified `git diff aede6b0..b987964 -- test/e2e/c07_checks.go test/analysis/` is empty (neither `checkAdmissionRestart` nor any SQL file was weakened).
- Verified `internal/pool/pool.go` (`Allow`, `maybeDial`, `checkTrial`, `dial`, `DialFailed`, `Next`) and `internal/pool/breaker.go` for the half-open `wanted` flag behavior.
- Fuzz targets (PLAN.md section 6.6): no fuzz target added or modified in the repair round.

Commands and results:
- `gofmt -l .` — empty.
- `go build ./... && go vet ./...` — pass.
- `scripts/gen-docs.sh --check` — pass (`docs/csv-schema.md`, `docs/timeout-budget.md`, `docs/capacity-table.md` current).
- `go test -count=1 ./internal/pool ./internal/proxy ./internal/admission ./internal/config` — all pass (`pool` 2.96 s, `proxy` 0.29 s, `admission` 5.66 s, `config` 0.10 s).
- `go test -count=1 -v -run '^TestCanaryJoin' ./test/e2e` — pass (0.63 s; caught `sim: invariant proxy.engine: admission: the core shed 0, the engine answered 4`).
- `pool-admission-restart` extra seeds `42`, `99`, `1001` (`RUN_DIR=/tmp/zgw-c07-pool-review4/extra-seeds DST_SEED=<s>`) + `scripts/csv-assert.sh` — all 3 pass (5.30 s, 5.06 s, 5.10 s); SQL 0 failures over 3 runs.
- `pool-admission-restart` single `DST_SEEDS=200` call (capped at `poolSweepSeeds=20`) + `scripts/csv-assert.sh` — pass (151.37 s); SQL 0 failures over 20 runs.
- `pool-admission-restart` all 200 distinct seeds `1..200` (12 `DST_SEED_BASE` windows, 233 runs total) + `scripts/csv-assert.sh` per window — all 12 windows pass (67.58 s–150.11 s per window); SQL 0 failures in every window; across the 200 distinct runs, `shed{breaker}` ranged `[3, 5]` and `shed{rule}` was `0` on all 200 seeds.
- 20-seed sweep of every pool and admission scenario: `RUN_DIR=/tmp/zgw-c07-pool-review4/sweep-20 DST_SEEDS=20 go test -count=1 -timeout 30m -run '^(TestScenarioPool|TestScenarioAdmission.*)$' ./test/e2e` — pass (272.02 s); `scripts/csv-assert.sh` across all 28 scenario directories (22 pool, 6 admission) — 0 failures.
- `GATE_ONLY=G0,G1,G2,G3 GATE_BASE=3f38a1d6cf54a9dc90307b3758985b91147327cf GATE_OUT=/tmp/zgw-c07-pool-review4/gate scripts/gate.sh c07-pool` (baseline `d572ed3^2`, the merged `work/c08-admission` tip, because `work/c08-admission` on the host has moved to a non-ancestor commit) — `overall=pass` (502 s): G0 all 7 pass, G1 pass (`detlint` 27 patterns, `importlint`, `boundlint`, `paniccheck`), G2 comments pass against `3f38a1d`, G3 unit pass (436 s, all packages including all adversarial packages), G3 race pass (51 s, 6 patterns).
- `git status -s` — empty (repository untouched).

NITS CLOSED by the orchestrator (2026-11-27): finding 1 corrected in NOTES.md ("Correction after review 4"); the sentence in docs/knowledge/pool-core.md is corrected by the prose pass.
