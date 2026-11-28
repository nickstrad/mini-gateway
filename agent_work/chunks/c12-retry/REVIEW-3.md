VERDICT: NITS

Scoped review of the chunk 12 × chunk 9 join (`d5dc685..2d47715`, merge commit `1d15364` on top of `54385bc` and `d5dc685`).

## Findings

1. **MINOR — `test/e2e/c12_c09_join_test.go:20-73`: `TestScenarioRetryStormTwoTenantsOfUnequalWeight` does not assert that the 3:1 weights took effect.**
   - What is wrong: The test configures weights 3:1 (`tenant0` weight 3, `tenant1` weight 1) but asserts only `f.HeldShare > 0` and `served == 2`. Both hold even when weights are equal (1:1).
   - How I know: Mutating `test/e2e/c12_retry.go:121` from `Fairness: admission.FairConfig{Weights: sp.Weights}` to `Fairness: admission.FairConfig{}` in `/tmp/zgw-c12-retry-review3/mut` still passed `TestScenarioRetryStormTwoTenantsOfUnequalWeight` on all 3 seeds (`seed=1..3`, 15.9s). Direct probe on seed 1 shows that with 3:1 weights active `held_share=16`, `tenant0 at_share=3,140,532 ns`, `tenant1 at_share=388,343,723 ns` (>120× asymmetry); with `Weights: nil`, `held_share=2`, `tenant0 at_share=23,935,363 ns`, `tenant1 at_share=34,710,026 ns`.
   - What a fix must achieve: Assert weight asymmetry in `TestScenarioRetryStormTwoTenantsOfUnequalWeight` (e.g., verify the 1-weight tenant spends strictly more time at its share than the 3-weight tenant by a clear margin, or check `FairConfig().Weights` plus `AtShare` ordering) so dropping `sp.Weights` fails the test.

2. **MINOR — `test/e2e/c12_retry.go:107`: Pinned storm budget-exhaustion seeds (`Exhausts: []int64{1, 3, 5}`) remain schedule-fragile instead of forced by construction.**
   - What is wrong: The re-pinning from `{1, 2, 3}` to `{1, 3, 5}` is **honest** — probing seeds 1–10 directly confirms `Refused[RefusedBudget]` is `[3, 0, 4, 0, 2, 1, 2, 0, 1, 4]` (seeds 2, 4, and 8 never exhaust, matching `NOTES.md`). The cause is that `StormPattern` arms `FaultDoom` every 10th query (`n % 10 == 3`), right at the 10-first-attempts-per-token refill boundary, so any scheduler change (chunk 9 now; chunk 10 AIMD/GCRA or chunk 11 coalescing next) shifts which seeds happen to land two zero-byte refusals fewer than 10 first attempts apart.
   - How I know: Code inspection of `test/e2e/c12_retry.go:84-108` and `test/e2e/c12_relay.go:117-123, 258-326`, plus the 10-seed probe run in `/tmp/zgw-c12-retry-review3/mut`.
   - What a fix must achieve: Force exhaustion by construction so `Refused[RefusedBudget] > 0` is required on every seed without a pinned `Exhausts` list. That takes ~10 lines and 0 ms extra simulated duration: change `RelayPlan.DoomAllAt` to `[]time.Duration` and split `RetryStormSpec().Phases` around a second quiet gap right after the first doom burst — Phase 1 `0..450ms` (warms the 4 connections and fills the 1-token cap to 10 tenths), `DoomAll` at `600ms`, Phase 2a `700..720ms` (shorter than `retryClientGap = 25ms` so the 8 clients issue at most 1 op each = ≤ 8 first attempts; the first query spends the 1 token leaving `level = 0`, deposits bring `level` to at most 8 < 10 tenths while suspect siblings are checked/replaced), quiet gap `720..850ms` with a second `DoomAll` at `800ms`, then Phase 2b `850..3500ms` (the first query at `850ms` hits a doomed connection while `level ≤ 8 < 10` tenths and is refused `RefusedBudget` on every seed regardless of dispatcher order).

3. **MINOR — `internal/proxy/join_retry_fair_test.go`: Four fair-dispatcher × retry interactions are verified in code but untested by join tests.**
   - What is wrong:
     1. **Retry pushed into a full tenant queue (`ReasonOverflow`) or full tenant table (`ReasonTenants`):** Code inspection of `internal/admission/queues.go:392-425`, `internal/proxy/lease.go:85-98`, and `internal/proxy/retry.go:153-167` confirms that an overflow sheds the oldest queued entry (calling `retryEnded(RetryEndShed)` if that entry is also a waiting retry) and a refused `Push` sheds the retry itself (`RetryEndShed`, `RetryPending` back to 0) and pumps the client — but neither path is tested in `join_retry_fair_test.go`.
     2. **Retry of a tenant strictly above its share (`inFlight > share`) while the last-slot rule holds a connection back:** `retryHeldAtShare` tests `inFlight == share` (2 of 2 at limit 4). Strictly above share (e.g., limit 6, equal shares 3, tenant 0 drops 5 → 4 while tenant 1 holds 1) also tests that when one of tenant 0's leases ends (4 → 3, still at share, 2 free slots), work-conservation lends slot 5 to the retry while still reserving slot 6 for tenant 1.
     3. **`FairConfig.Limit < Connections` (`checkoutDemand`) with a waiting retry:** Mutation `M-c2` (disabling `checkoutDemand`) survived all `TestJoin*` tests (caught by chunk 9's `TestFairNoDialPastTheFairLimitWhileConnectionsOpen`). No join test exercises a retry held by `HeldLimit` (`Limit < Connections`) or checks `Queues.Releasable()` directly across retry transitions.
     4. **Breaker re-check via `fairRedispatch()`:** Mutation `M-b3` (removing `e.fairRedispatch()` from `Engine.finish`) survived all `TestJoin*` tests (caught by chunk 9's `TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible`). `TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseEndsWhileARetryIsHeldBack` releases tenant 1's lease via `COMMIT` (`coreRelease`/`coreStepDone` → direct `dispatch()`), not via `coreDirty` or `leaseLost` with an idle connection already in the pool where `fairRedispatch()` is the sole trigger of `dispatch()`.
   - How I know: Line-by-line review of `internal/proxy/join_retry_fair_test.go` and mutations `M-b3` and `M-c2` below.
   - What a fix must achieve: Add compact unit tests in `internal/proxy/join_retry_fair_test.go` for (a) retry into a full tenant queue (overflow evicting an earlier waiting retry, and `MaxTenants` push refusal), (b) above-share retry with last-slot reservation and work-conserving release, (c) `FairConfig.Limit < Connections` retry demand, and (d) breaker re-check triggered through `fairRedispatch()` after `leaseLost`/`coreDirty`.

4. **MINOR — `internal/proxy/join_retry_fair_test.go:323-366`: `coreAdmission` is reset without `defer`.**
   - What is wrong: `TestJoinEveryRetryEngineTestHoldsBehindTheFairDispatcher` mutates package-global `coreAdmission = a` and clears it at line 365; a `t.Fatal` at line 329 would leave `coreAdmission` dirty for later tests in `internal/proxy`.
   - How I know: Inspection of `internal/proxy/join_retry_fair_test.go:323-366`.
   - What a fix must achieve: Add `defer func() { coreAdmission = admission.Config{} }()` at the start of the test.

## Merge verification (`1d15364`)

- Two-sided file check (`git merge-base 54385bc d5dc685`):
  - Exactly 13 files were modified on both branches: `agent_work/INDEX.md`, `docs/capacity-table.md`, `docs/csv-schema.md`, `internal/config/config.go`, `internal/config/validate.go`, `internal/obs/simrun/engine_test.go`, `internal/proxy/config.go`, `internal/proxy/contracts.go`, `internal/proxy/corepool.go`, `internal/proxy/engine.go`, `internal/proxy/lease.go`, `scripts/fuzz-plant.sh`, `test/e2e/realtier.go`.
  - Automated tree verification confirmed 0 single-side files were altered by the merge commit (`only_left` files byte-identical to `54385bc`, `only_right` files byte-identical to `d5dc685`, 0 extraneous files).
  - `docs/c4/`, `book/`, `README.md`, `STATE.md`, and `agent_work/adversarial/c09-fairness/` are byte-identical to `d5dc685`; `agent_work/adversarial/c12-retry/` is byte-identical to `54385bc`.
  - Line-by-line review of both `54385bc..1d15364` and `d5dc685..1d15364` across all 13 two-sided files confirms no behaviour, test, or assertion of chunk 9 or chunk 12 was dropped or weakened.
  - Generated documents: `scripts/gen-docs.sh --check` passes (`docs/csv-schema.md`, `docs/timeout-budget.md`, `docs/capacity-table.md`, `docs/fault-matrix.md` all current); `docs/fault-matrix.md`, `docs/error-contract.md`, and `docs/timeout-budget.md` are byte-identical to `54385bc`, confirming fairness adds no fault or error-contract row.

## Mutation checks

All mutations were applied to a clean `git archive HEAD` copy in `/tmp/zgw-c12-retry-review3/mut` and reverted after each run.

| Decision | Mutation | Result | Killed by |
| --- | --- | --- | --- |
| (a) | `M-a1`: Drop `e.fairEnded(d.lastTenant)` in `leaseLost` (`internal/proxy/lease.go:347`) | **KILLED** | All `TestJoinA…` / `TestJoinThe…` / `TestJoinShutdown…` tests |
| (a) | `M-a2`: Skip `e.fairStarted(c.tenant)` in `lend` when `c.req.attempt > 0` (`internal/proxy/lease.go:164`) | **KILLED** | `TestJoinARetriedTenantAtItsShareLetsTheTenantBelowItsShareGoFirst`, `TestJoinARetryHeldBackAtItsShareIsLentWhenItsOwnLeaseEnds`, `TestJoinTheBreakerReCheck…`, `TestJoinARetryTakesANewTurn…`, `TestJoinEveryRetryEngineTest…` |
| (a) | `M-a3`: Double `e.fairStarted(c.tenant)` in `lend` when `c.req.attempt > 0` (`internal/proxy/lease.go:164`) | **KILLED** | Same 5 join tests as `M-a2` |
| (b) | `M-b1`: Drop `retryClamp` on queue-wait timer in `enqueue` (`internal/proxy/lease.go:94`) | **KILLED** | `TestJoinARetryHeldBackByTheDispatcherEndsAtTheRequestDeadline`, `TestJoinEveryRetryEngineTest…` |
| (b) | `M-b2`: Remove `refuseRetriesBehindOpenBreaker` from `dispatch` (`internal/proxy/lease.go:128`) | **KILLED** | `TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseEndsWhileARetryIsHeldBack`, `TestJoinEveryRetryEngineTest…` |
| (b) | `M-b3`: Remove `e.fairRedispatch()` from `Engine.finish` (`internal/proxy/engine.go:246`) | **SURVIVED** `^TestJoin`; **KILLED** full `internal/proxy` | `TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible` (Finding 3.4) |
| (c) | `M-c1`: Pass `e.seams.Queue.Len()` instead of `e.releasable()` in `corePump` (`internal/proxy/corepool.go:156`) | **KILLED** | 6 join tests (`TestJoinARetryHeldBackAtItsShareOpensNoConnection`, etc.) |
| (c) | `M-c2`: Disable `checkoutDemand` (`return 0, false`, `internal/proxy/fairness.go:183`) | **SURVIVED** `^TestJoin`; **KILLED** full `internal/proxy` | `TestFairNoDialPastTheFairLimitWhileConnectionsOpen` (Finding 3.3) |
| (d) | `M-d1`: Remove `e.fairnessCheck()` from `Engine.Invariants` (`internal/proxy/engine.go:426`) | **KILLED** | `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions` |
| (d) | `M-d2`: Remove `e.retryCheck()` from `Engine.Invariants` (`internal/proxy/engine.go:429`) | **KILLED** | `TestInvariantsCatchAPendingRetryNoRequestHolds`, `…TheEngineAndThePolicyDisagreeing`, `…ABrokenBudget` |
| (d) | `M-d3`: Remove `RetryPending` check from `Engine.Quiesce` (`internal/proxy/engine.go:376`) | **KILLED** | `TestQuiesceRefusesAPendingRetry` |
| (d) | `M-d4`: Remove `q.fairQuiesce()` from `Queues.Quiesce` (`internal/admission/queues.go:659`) | **KILLED** | `TestQuiesceRefusesALeaseStillInFlightAndPassesOnceItEnds` |
| (e) | `M-e1`: Skip `validateRetry()` when `proxyCaps().Validate()` already errored (`internal/config/validate.go:109`) | **KILLED** | `TestRetryAndFairnessKeysAreValidatedTogether` |
| (e) | `M-e2`: Drop `Fairness: f.fairness()` from `proxyCaps` (`internal/config/validate.go:68`) | **KILLED** | `TestRetryAndFairnessKeysAreValidatedTogether` |
| (e) | `M-e3`: Change `DefaultRetryPolicy` to `"pre-forward-once"` (`internal/config/retry.go:10`) | **KILLED** | `TestBinaryDefaultRunsTheFairDispatcherWithTheRetryPolicyOff` |
| (f) | `M-f1`: Push to front of tenant ring instead of back (`internal/admission/queues.go:415`) | **KILLED** | `TestJoinARetryTakesANewTurnBehindItsTenantsQueueAndNoTenantGainsService` |
| (f) | `M-f2`: Do not advance `t.turn` in `takeTurn` (`internal/admission/fair.go:547`) | **KILLED** | `TestJoinARetryTakesANewTurnBehindItsTenantsQueueAndNoTenantGainsService` |
| (4) | `M-w1`: Drop `Weights: sp.Weights` in `RetrySpec.engineConfig` (`test/e2e/c12_retry.go:121`) | **SURVIVED** | Neither `TestScenarioRetryStormTwoTenantsOfUnequalWeight` nor any other test failed (Finding 1) |

Decisions (a)–(f) all hold in the product code. Every join test in `internal/proxy/join_retry_fair_test.go`, `internal/admission/fair_quiesce_test.go`, and `internal/config/join_test.go` proves what its name says.

## Release It! checklist

Scoped to the chunk 12 × chunk 9 join (no new product code beyond the conflict resolution):

- **Integration points:** Every held-back retry keeps its clamped virtual-clock `queueT` and `stmtT` timers (`retryClamp`), bounded by arrival + `timeouts.statement` (`TestJoinARetryHeldBackByTheDispatcherEndsAtTheRequestDeadline`).
- **Blocked threads:** Lost leases report `fairEnded` (`leaseLost`/`coreDirty`) before a retry waits; `Shutdown` clears queued retries (`RetryEndGone`) and all tenants' in-flight leases, passing `Invariants` and `Quiesce` (`TestJoinShutdownWithAHeldBackRetryAndLeasesOfTwoTenants`).
- **Slow responses:** Held-back retries are answered `53M01` at their exact request deadline and `53M03` if the breaker opens before dispatch; `RetryPending` returns to 0 on every exit path.
- **Cascading failures:** With the pool breaker open, `ActRefuseQueued` flushes held-back retries via `refuseAdmitted` (`53M03`) and `refuseRetriesBehindOpenBreaker` guards custom breaker seams at the top of `dispatch`; no dial is started for held-back retries (`Queues.Releasable`).
- **Chain reactions:** Not applicable beyond chunk 9 and chunk 12 individually; the join composes shedding (`53M01` on queue wait / deadline) with the retry budget without adding queuing.
- **Dogpile:** A held-back retry is excluded from pool demand (`e.releasable()`), preventing dial cycles while waiting at share (`TestJoinARetryHeldBackAtItsShareOpensNoConnection`); storm recovery still passes on seeds 1–5 (`TestScenarioRetry`).
- **Self-denial attacks:** A retry re-enters at the tail of its tenant's queue and spends a new round-robin turn (`TestJoinARetryTakesANewTurnBehindItsTenantsQueueAndNoTenantGainsService`); retries cannot steal other tenants' turns or bypass the 10% budget.
- **Force multiplier:** Not applicable here because neither the retry budget nor the fair dispatcher introduces a feedback control loop at the join boundary.
- **Unbalanced capacities:** A waiting retry occupies one slot in its tenant's bounded admission ring (`TenantCapacity`, `Capacity`, `MaxTenants`), with overflow/refusal handled by `Queues.Push` + `e.shed`.
- **Scaling effects:** Fleet scaling rules of both chunks are unchanged (`retry.floor / fleet.instances`, per-instance fair limit `pool.connections`, bounded `MaxTenantStats` label cardinality in `admsrc`).
- **Users:** Per-tenant queues, fair shares, and `fairTenant` states (capped at `MaxLeases` and deleted on zero in flight) bound per-tenant resources across retried requests (`TestJoinShutdown…`, `TestScenarioRetryStormTwoTenantsOfUnequalWeight`).
- **Unbounded result sets:** Not applicable to the join; retries only trigger on `Reached == No` (zero bytes accepted, zero bytes relayed), before any reply bytes exist.
- **Compounds:** Checked the key compound — a retry of a tenant at its share while a connection is free or the breaker opens: no lease double-count or leak (`fairnessCheck` + `retryCheck`), no dial loop (`Releasable`), and breaker re-check runs on every dispatch path.

## Observability

PLAN.md section 7.6 for the join:
- `Snapshot()` present and unchanged on `proxy.Engine`, `admission.Queues` (including `FairSnapshot`), and `retry.Policy`; both `admsrc` and `retrysrc` plus `enginesrc` compile and pass unit tests (`internal/obs/admsrc`, `internal/obs/enginesrc`, `internal/obs/retrysrc`).
- `docs/csv-schema.md` was regenerated with `scripts/gen-docs.sh` and carries all chunk 9 (`admission.*`) and chunk 12 (`engine.retry_*`, `retry.*`) metrics; `scripts/gen-docs.sh --check` passes.
- Counters move and conservation holds across the join: `fairnessCheck` (`LeaseStarts - LeaseEnds == InFlight == engine leases`), `retryCheck` (`RetryPending`, `RetryOffered`, `RetryAsked`), `AdmissionSnapshot().Check()`, and `Snapshot.Check()` (`retryConserved`) run in every join test and in `TestScenarioRetry` (with `Flush.Check()` CSV round-trip on seeds 1–5) and `TestScenarioRetryStormTwoTenantsOfUnequalWeight`.
- Fuzz targets (PLAN.md 6.6): `scripts/fuzz-plant.sh` keeps both `FuzzRetryOps` (`retry-withdraw-first`) and `FuzzFairnessOps` (`fairness-unpop-turn`); neither derives a simulator seed from a hash of its input.

## What I ran

All commands ran in `/root/Software/mini-gateway-work/c12-review` with `PATH=/usr/local/go/bin:$PATH` (no repo files modified; `git status --short` clean).

- `go build ./... && go vet ./...` — pass.
- `scripts/gen-docs.sh --check` — pass (`docs/csv-schema.md`, `docs/timeout-budget.md`, `docs/capacity-table.md`, `docs/fault-matrix.md` all current).
- `gofmt -l internal/proxy/join_retry_fair_test.go internal/admission/fair_quiesce_test.go internal/config/join_test.go test/e2e/c12_c09_join_test.go test/e2e/c12_retry.go` — empty (clean); 0 new comments in join Go files.
- `go test -count=1 -timeout 5m ./internal/proxy/... ./internal/admission/... ./internal/retry/... ./internal/config/... ./internal/obs/retrysrc/...` — all pass (`internal/proxy` 1.03s, `proxysim` 0.02s, `internal/admission` 10.60s, `internal/retry` 1.26s, `internal/config` 0.39s, `internal/obs/retrysrc` 0.02s).
- `go test -count=1 -timeout 2m ./internal/obs/admsrc/... ./internal/obs/enginesrc/... ./internal/obs/retrysrc/...` — all pass (`0.82s`, `0.03s`, `0.01s`).
- `go test -count=1 -timeout 3m ./agent_work/adversarial/c09-fairness/... ./agent_work/adversarial/c12-retry/...` — both pass unchanged (`6.56s`, `0.29s`).
- `DST_SEEDS=5 GOMAXPROCS=2 go test -v -count=1 -timeout 10m -run '^(TestScenarioRetry|TestCanaryRetry)' ./test/e2e/` — pass (`434.4s`, seeds 1–5 across both retry scenarios × 3 arms with replay & CSV round-trip, weighted storm seeds 1–3, both canaries, and section 14 re-runs).
- `GOMAXPROCS=2 go test -v -count=1 -timeout 10m -run '^(TestScenarioFair|TestCanaryFair)' ./test/e2e/` — pass (`430.4s`, all fairness scenarios & canaries).
- Scratch-copy mutations (`18` runs across decisions (a)–(f) and item 4 in `/tmp/zgw-c12-retry-review3/mut`) and 10-seed storm + weighted probe (`41.8s`).
