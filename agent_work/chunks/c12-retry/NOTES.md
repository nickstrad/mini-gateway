# Chunk 12 — Retry policy: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c12-retry`, worktree
`/root/Software/mini-gateway-work/c12`, on top of `f93f0f2` (chunk 7 + 8 landed on main).

## Simulator change requests

None. The simulator API is untouched. Zero-byte write refusals are produced test-side with the
existing fault `simnet.FaultHalfOpen` applied through `sim.ApplyFault` to the proxy's own end of a
connection (`test/e2e/c12_relay.go`, a test-side relay in the manner of chunk 7's `pathRelay`).

## What was built

- **`internal/retry`** (deterministic, sans-I/O; imports `internal/work` and `internal/obs` only;
  on the determinism and race lists): `Policy` interface (`Attempt`, `Decide`, `Snapshot`),
  `Off` (the zero `Config`, the default) and `PreForwardOnce` with its integer budget in tenths
  of a token (start at the floor, +1 per first attempt, −10 per retry, capped; lost credit counted
  as `Expired`). `Config{Kind, CapTokens, FloorTokens, Plant}`, `Validate` naming
  `retry.policy`, `retry.bucket_cap`, `retry.floor`; `Share(fleetFloor, instances)`.
  Test-only: `retry.Plant` bits and `internal/retry/retrytest.Everything` (the ablation policy).
- **Engine wiring** (`internal/proxy/retry.go`, plus one-line hooks): `RetryCore` (the
  `RetryPolicy` seam backed by the core), the re-run of a granted retry from `statementLost`,
  first attempts reported to the budget in `forward`, the request deadline clamp, retry end
  accounting on every exit path, `Engine.RetrySnapshot()`, the `Invariants` block
  (`retryCheck`) and snapshot conservation (`retryConserved`), `CoreBreaker.Open()` /
  `ClosedBreaker.Open()` pure reads.
- **Config**: section `retry` (`internal/config/retry.go`): `policy`, `bucket_cap`, `floor`.
- **Observability**: `internal/obs/retrysrc` (component `retry`), catalog entries, engine metrics
  `retry_offered`, `retried_requests`, `retries_pending(_peak)`, `retry_ends{outcome}`,
  `retry_rows{reason}` (`retry_asked` kept; its help text now says "granted").
- **Contracts**: error contract column "Retried by the proxy?" (`internal/pgwire/errors_retry.go`,
  `errors_doc.go`) — **the generated `docs/error-contract.md` changed** (a new column on every row
  and a rewritten closing paragraph on the proxy's retry). New generated document
  `docs/fault-matrix.md` from `internal/proxy/matrix.go`. New rows in `docs/timeout-budget.md`
  (the request deadline for a retry) and `docs/capacity-table.md` (the retry budget);
  `docs/csv-schema.md` regenerated.
- **Scenarios** (tier S): `retry-storm` and `retry-no-double-apply`, each with arms `off`, `on`,
  `everything` (ablation); tier R: `TestTierRRetryStorm` (`tierr-retry-storm`, arms off/on).
  Section 14 re-runs: `TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn`.

## Decisions on the open points

1. **The request's deadline** = its arrival (`request.enq`, when the Query was read) +
   `timeouts.statement`. A retry is refused at or after it (`now >= deadline`, so equality
   refuses). A granted retry's queue wait and its statement deadline are clamped to it
   (`retryClamp` in `enqueue` and `forward`), so a retry never runs past it; first attempts keep
   their own timers unchanged.
2. **At once, no wait.** A granted retry re-enters at the queue stage (`enqueue`) in the same
   step: classify, reject list, draining, breaker and rate gate are not run again (breaker and
   draining are checked by the policy itself as pure reads; the rate gate does not see retries —
   the budget is their governor). With the admission queue it waits in its tenant queue and takes
   that table's exits (`53M01` rule/overflow/deadline, `53M03` breaker flush, `57014` cancel,
   gone). It holds no connection: the failed one was already closed (`leaseFailed`) or sent to the
   dirty table (`statementDeadline`), and no queue slot outside the queue: there is no other
   waiting state. The coalescer is not consulted for a retry (see "Wave C").
3. **Breaker without `Allow`'s side effect**: the engine reads `breakerState.Open()` —
   `CoreBreaker.Open()` is `p.Breaker() != BreakerClosed` (half-open counts as open, so a retry is
   never the half-open trial's companion); `ClosedBreaker.Open()` is false; any other breaker seam
   without the method reads as closed (stated limit 3). The loss is reported to the pool core
   (`coreLost`) before the policy is asked, so a failure at the instant the breaker opens is not
   retried.
4. **Orchestrator's ruling**: no failure inside a transaction is retried (`RefusedInTx`, checked
   before `Reached`).
5. **Orchestrator's ruling**: the matrix is generated, `docs/fault-matrix.md`, held by
   `TestFaultMatrixDocumentIsGeneratedFromTheCode`; `scripts/gen-docs.d/c12-fault-matrix.sh`; the
   path is on the `deny` line of `tools/lint-rules/prose-paths.txt`.
6. **Keys**: `retry.policy` (`off` | `pre-forward-once`, default `off`), `retry.bucket_cap`
   (tokens per instance, default 100, 1 to 1,000,000), `retry.floor` (fleet tokens, default 10,
   divided by `fleet.instances`). Zero or omitted = default; negative refused naming the key. With
   the policy on, a floor below one token per instance and a per-instance floor above the cap are
   refused naming `retry.floor`; a remainder is a `Warnings()` line. With the policy off the floor
   is not divided (nothing uses it).
7. **The retried request's second failure** is offered to the policy (counted, refused `once`)
   and answered with the row of that failure (`lostRow` of the second attempt).
8. **Tier R**: a zero-byte write refusal cannot be produced deterministically on real sockets
   (a terminated backend's RST races the next write). Tier R shows the storm bounded, no
   duplicate write and goodput returning with both policies; the retry counter's movement is shown
   in tier S (Deviation 2).
9. **"Refusals are fast"**: tier S (virtual time, no wall clock): every `53M03` within 5 ms of its
   send, every `53M01` within 110 ms (the rule's 100 ms interval), every other error within
   1.1 s (the statement deadline plus a retry) — `RetryRun.checkFast`. Tier R (wall clock):
   every `53M01`/`53M03` within 500 ms.

## Claims and the tests that prove them

| # | Claim | Tests |
| --- | --- | --- |
| 1 | Off by default; Off never retries | `internal/config` `TestRetryDefaultIsOffAndOffNeverRetries`, `TestRetryZeroMeansTheDefault`; `internal/retry` `TestOffNeverRetriesAndCountsEveryOffer`; `internal/proxy` `TestRetryOffByDefaultAnswersTheRow`; scenario arm `off` (`checkBudget`: 0 retries), SQL `budget.sql` |
| 2 | At most one retry per request | `TestRetryAtMostOncePerRequest`; property `TestPropertyBudgetAgreesWithTheReferenceModel` (reference refuses `Attempt > 0`); canary `TestCanaryEveryRetryMechanismHasAnEngineTestThatFailsWhenRemoved/once-per-request_bound` |
| 3 | Only on `Reached == No` from an explicit zero-byte refusal | `TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply`, `TestRetryNeverAfterBytesWereAccepted`, `TestRetryNeverOnACloseWithoutAWriteReport`, `TestRetryNeverAfterARelayedByte`, `TestRetryNeverOnStatementSilence`; scenario check "retries only on rows that allow one"; canaries (core, engine, `TestCanaryRetryStormCatchesAPolicyThatIgnoresReached`) |
| 4 | No statement applied twice under any product policy, kills at every point | `TestScenarioRetry/retry-no-double-apply/{off,on}` and `retry-storm/{off,on}` (`CheckProduct`: every write-log text at most once, every acknowledged write exactly once; kill points before-forward, after-forward, after-applied, mid-reply, after-reply, and dooms); tier R `TestTierRRetryStorm` (duplicate `marks` rows = 0); canary `TestCanaryRetrySweepCatchesAPolicyThatIgnoresReached` |
| 5 | Budget bound over any window, cap expires credit, integers | `TestPropertyBudgetAgreesWithTheReferenceModel` (every window `10·retries ≤ attempts + carried level ≤ attempts + cap`; from the start `≤ attempts + floor`), `TestPathologicalSignalEveryRequestFailingPreForwardStaysUnderTheBound`, `TestAQuietHourCannotBankAStorm`, `TestBucketHoldingExactlyOneTokenGrantsOneRetry`, `TestBucketExactlyAtTheCap`; SQL `budget.sql` per flush window |
| 6 | Never when the breaker is open | `TestRetryNeverWhenTheLossOpensTheBreaker`, `TestRetryDecidedWithoutConsultingTheBreakersAllow` (no trial, no state change); canary `.../breaker_check` |
| 7 | A waiting retry holds nothing; never past the deadline (equality) | `TestRetryAZeroByteRefusal...` (lent 0, queued 1 while waiting), `TestRetryWaitsInTheTenantQueueWithTheAdmissionRule`, `TestRetryRefusedAtTheRequestDeadlineExactly`, `TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt` (queued retry shed at the deadline exactly), `TestRetryForwardedStatementEndsAtTheRequestDeadline`, core `TestDeadlineAtEqualityRefusesAndJustBeforeGrants` |
| 8 | Storm bounded (off and on), refusals fast, goodput returns | `TestScenarioRetry/retry-storm/{off,on}` (`checkFast`, `checkRecovery`: every operation started after the recovery point succeeds on its first attempt, `checkBudget`: forwards = attempts + retries), SQL `load.sql`; tier R `TestTierRRetryStorm` |
| 9 | Exhaustion visible; decisions counted by reason; conservation on every exit path incl. `Shutdown`; one reply per retried request | `TestRetryBudgetExhaustionIsVisibleAndCounted`, `TestEveryRefusalReasonIsReachedAndCounted`, `TestRetryShutdownWithAPendingRetryEndsItAndConserves`, `TestRetryClientGoneWhileTheRetryIsQueued`, `Snapshot.Check` (`retryConserved`) run by every engine step's invariants, SQL `conservation.sql`; `internal/obs/retrysrc` `TestRetryCountersMoveAndConserve` |
| 10 | The matrix is complete; retries only on rows that allow them | `TestEveryErrorRowStatesWhetherTheProxyRetriesIt`, `TestEveryFaultMatrixRowStatesRetryAndATestThatExists` (each named test exists, each scenario has an analysis folder, retried rows are `lost-after-read`, reached no, outside a transaction), `TestFaultMatrixDocumentIsGeneratedFromTheCode`; engine counter `RetryRows` checked by `CheckProduct` and `conservation.sql` |
| 11 | The ablation fails; the binary cannot select it | arm `everything`: `CheckAblation` (retries on forbidden rows > 0), `load.sql` (forwards > attempts); seed 1: 49 (storm) and 52 (sweep) statements applied twice; `TestRetryEverythingAblationRerunsAWriteThatMayHaveReached`; `TestRetryEverythingIsUnreachableFromTheBinary` (no kind name, `go list -deps ./cmd/...` has no `retrytest`), `TestConfigNeverSetsARetryPlant` |
| 12 | Fleet: floor ÷ N, ratio not; validation names the key | `TestRetryFloorIsDividedByInstancesAndTheRatioAndCapAreNot`, `TestRetryKeysFailClosedNamingTheKey`, `TestFloorShareDividesByInstancesAndRefusesBelowOne` |
| 13 | `Invariants` covers the retry accounting; a test fails if the block is removed; `Quiesce` passes after shutdown | `TestInvariantsCatchAPendingRetryNoRequestHolds`, `TestInvariantsCatchTheEngineAndThePolicyDisagreeing`, `TestInvariantsCatchABrokenBudget` (mutation M1); `TestRetryShutdownWithAPendingRetryEndsItAndConserves` |
| 14 | Replay and flush: bit-identical per seed; flushed files agree with snapshots | `runRetry` runs every seed twice and compares trace/state digests and the flushed bytes; `a.Flush.Check()` (round trip of the in-memory snapshot) |
| 15 | Fuzz target: total bounded decoding, no seed from a hash, finds the plant | `FuzzRetryOps`, `TestFuzzDecodingIsTotalBoundedAndLocal`, `TestFuzzPlantIsFoundByTheReferenceModel`, `scripts/fuzz-plant.sh` (`retry-withdraw-first`) |

Health: unchanged by retries — `TestHealthIsUnchangedByRetries` (`internal/obs/retrysrc`: every
health rule gives the same state with and without exhausted retries) and
`TestRetryHealthIsUnchangedByRetries` (engine health inputs equal with the policy on and off).

## Why the scenario checks cannot race (they hold on every seed by construction)

- **Retries > 0 in the `on` arm.** Clients are scripted in phases with a quiet gap (storm
  450–700 ms, sweep 250–400 ms). At `DoomAllAt` (600 ms / 300 ms) every relay pair that has been
  quiet ≥ 2 ms and is not inside a query is doomed (`FaultHalfOpen` on the proxy's end). No
  client is active and no fault has happened yet, so no connection is suspect, the breaker is
  closed and the idle ages (≤ 250 ms) are below `pool.check_after_idle` (2 s): the first lend
  after the gap goes, without a check, to a doomed connection, its first write fails with zero
  bytes accepted, and the policy (floor 4 tokens) grants the retry.
- **Ablation duplicates (sweep).** A text the relay killed at `after-applied` (the fake applied
  it, the relay cut before the reply) is exempt from every later fault; the breaker is effectively
  off in the sweep (`pool.MaxBreakerFailures`), so `Everything`'s retry of it waits for a
  connection and is applied a second time. The first such kill happens at the 9th query in the
  window. The storm's ablation check is "retries on forbidden rows", which needs only one
  reached-yes failure with `Attempt < 3` — the window's first query is killed before forward.
- **Goodput returns.** Faults stop at `Until`; the recovery point leaves the breaker's 1 s
  `open_for` plus a flush interval; after it nothing fails and nothing is queued long.
- **No double apply in product arms** does not depend on timing: the proxy re-sends only
  `Reached == No` and clients retry only `53M01`, `53M03`, `08006` (never `08007`), and `08006`
  for a write means reached-no.

## Mutation proofs (scratch copy `/tmp/zgw-c12/mut`, script `/tmp/zgw-c12/mutate.sh`)

| Mutation | Killed by |
| --- | --- |
| M1 remove the `retryCheck` block from `Engine.Invariants` | `TestInvariantsCatchAPendingRetryNoRequestHolds`, `...TheEngineAndThePolicyDisagreeing`, `...ABrokenBudget` |
| M2 remove the `Reached != No` refusal | `TestRetryNeverAfterBytesWereAccepted`, `...OnACloseWithoutAWriteReport`, `...AfterARelayedByte`, `...OnStatementSilence`, the property test |
| M3 remove the once bound | `TestRetryAtMostOncePerRequest`, the property test |
| M4 remove the withdrawal | `TestRetryBudgetExhaustionIsVisibleAndCounted`, `TestBucketHoldingExactlyOneTokenGrantsOneRetry`, the property test |
| M5 remove the cap | `TestBucketExactlyAtTheCap`, `TestAQuietHourCannotBankAStorm`, the property test |
| M6 never read the breaker state | `TestRetryNeverWhenTheLossOpensTheBreaker` |
| M7 deadline `>=` → `>` | `TestDeadlineAtEqualityRefusesAndJustBeforeGrants`, `TestRetryRefusedAtTheRequestDeadlineExactly`, the property test |
| M8 drop the statement-deadline clamp | `TestRetryForwardedStatementEndsAtTheRequestDeadline` |
| M9 drop the queue-wait clamp | `TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt` |
| M10 drop `retryEnded` in `shed` | `TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt` (engine invariants) |
| M11 drop the first-attempt deposit | `TestRetryAZeroByteRefusal...`, `TestRetryOffByDefaultAnswersTheRow` |
| M12 drop the in-transaction refusal | `TestRetryNeverInsideATransactionWhateverReachedSays`, `...MidStatement` |
| M13 M2 against the sweep scenario | `TestScenarioRetry/retry-no-double-apply/on` (seeds 1 and 2) |
| CSV: +500 retries in flushes ≥ 3 of a storm `on` run | `budget.sql` (47 rows), `conservation.sql`, `load.sql` |

## Sweeps (200 seeds)

Both tier S scenarios, all three arms (`off`, `on`, `everything`), **207 distinct seeds**
(1–207), every seed run twice for the replay law: 1,242 distinct (scenario, arm, seed) runs, all
passed, 0 failed. The runner takes at most 20 seeds per call (as chunk 7's) and the three pinned
seeds (1, 2, 3, `test/e2e/testdata/seeds.txt`) come first, so the sweep was twelve calls with
`DST_SEEDS=20` and `DST_SEED_BASE` 4, 21, 38, …, 191 (`/tmp/zgw-c12/sweep.sh`, one `RUN_DIR`
per call, logs `/tmp/zgw-c12/sweep-<base>.log`; each call: 120 passed, 0 failed). Distinct seeds
counted from the `--- PASS` lines of all twelve logs.

`scripts/csv-assert.sh <run-dir>/<scenario> test/analysis/<scenario>/*.sql` over every call's
runs: all 24 (12 calls × 2 scenarios) green: "3 SQL files run, 0 failures"
each (`budget.sql`, `conservation.sql`, `load.sql`; DuckDB v1.5.5). Logs
`/tmp/zgw-c12/csv-sweep.out`, `/tmp/zgw-c12/csv-rest.out`.

Without `DST_SEEDS` (the unit run, `go test ./...`) `TestScenarioRetry` runs seed 1 only, so the
package stays inside the gate's 30-minute `go test` limit; the gate's own sweep (G4) sets
`DST_SEEDS` and runs 20 seeds per scenario and arm.

## The completed matrix

`docs/fault-matrix.md` (generated; 30 rows: every row of chunks 6 and 7, and chunk 12's). Every
row states "Retried by the proxy?"; only zero-byte refusals outside a transaction, answered
`lost-after-read` when declined, say yes. `docs/error-contract.md` gained the same column for
every row (only `lost-after-read` says yes, with its conditions).

## Shared files

| File | Change |
| --- | --- |
| `internal/proxy/seams.go` | `Failure` gained `Attempt`, `BreakerOpen`, `Draining`, `Deadline` (appended) |
| `internal/proxy/lease.go` | `statementLost` offers the failure and re-runs a granted retry before counting it failed; `forward` reports first attempts and clamps a retry's statement deadline; `enqueue` clamps a retry's queue wait; `shed` and `replyDone` call `retryEnded` |
| `internal/proxy/client.go` | `request` gained `attempt`, `retryOver`; `dequeueGone`, `activeGone` call `retryEnded` |
| `internal/proxy/corepool.go` | `clientCancel` (queued) and `refuseWaiting` call `retryEnded` |
| `internal/proxy/engine.go` | `New` calls `initRetry` (replaces the `RetryOff{}` default); `Invariants` runs `retryCheck` after `admissionCheck` |
| `internal/proxy/snapshot.go` | retry counters appended to `Snapshot`; `Check` runs `retryConserved` |
| `internal/proxy/config.go` | `Config.Retry` appended; `Validate` validates it |
| `internal/proxy/contracts.go` | one retry timeout row and one capacity row appended |
| `internal/config/config.go`, `validate.go`, `pool.go` | `File.Retry` (`retry` section), defaults, validation, `proxyCaps` passes it, `Warnings` appends the floor remainder |
| `internal/pgwire/errors_doc.go` | the "Retried by the proxy?" column and the closing paragraph |
| `internal/obs/catalog/catalog.go` | declares component `retry` |
| `internal/obs/enginesrc/enginesrc.go` | engine retry metrics; `retrysrc` source added to `Sources` |
| `internal/obs/simrun/engine_test.go` | baseline runs `e2e.RetryBaseline` (not under `-short`); retry scenarios listed as short-skipped |
| `test/e2e/spec.go`, `simtier.go` | `Spec.Retry` appended and passed to the engine config |
| `test/e2e/realtier.go` | `GatewayOpts.Retry` (the `retry` config section) appended; `tierr-retry-storm` in `TierRScenarios` |
| `scripts/fuzz-plant.sh` | `FuzzRetryOps` plant |
| `tools/lint-rules/det-packages.txt`, `race-packages.txt`, `prose-paths.txt` | `internal/retry`, `internal/retry/retrytest`, `internal/obs/retrysrc`; `./internal/retry/...`; deny `docs/fault-matrix.md` |
| `agent_work/INDEX.md` | chunk 12 rows |

## Changes to earlier chunks' tests

- `internal/obs/simrun/engine_test.go`: additive (retry baseline runs and short-skip names). No
  existing assertion was changed.
- No other earlier test was edited. `retry_asked`'s help text changed ("granted"), which only
  regenerates `docs/csv-schema.md`.

## Deviations

1. **Exhaustion in the scenarios is visible, not forced.** Budget exhaustion is proven by
   `TestRetryBudgetExhaustionIsVisibleAndCounted` (engine) and the core tests; the scenarios
   export `retry.exhaustions` and `conservation.sql` checks it equals the refusals by reason
   `budget`, but no scenario exhausts the budget by construction: after one zero-byte refusal the
   pool core marks every idle sibling suspect (chunk 7), so further dead connections are found by
   checks, and one quiet period yields at most one retry, far under the budget.
2. **Tier R shows no retry.** Zero-byte refusals cannot be produced deterministically on real
   sockets; tier R terminates 30% of the backends every 100 ms for 3 s; most are idle and are
   absorbed by the check before reuse, so the clients saw 4 errors (`08007`) per arm in the
   recorded run. It shows: no duplicate write, goodput back after the faults (all of ~500
   operations after the recovery point), forwards = attempts + retries within the budget, and the
   CSV conservation.
3. **"30% database errors"** in tier S is 30% of the client queries in the fault window being
   faulted by the relay (10% killed at one of five points, 10% killed after being applied, 10%
   followed by a doom of their connection); the clients retry `53M01`, `53M03`, `08006` up to six
   attempts, 1 ms apart.
4. **"Statements reaching the database bounded against the ablation arm"** is asserted as: product
   arms forward exactly first attempts plus budgeted retries; the ablation forwards more than its
   first attempts and retries on forbidden rows. A cross-arm comparison of totals is not used: the
   arms' client behaviour differs (the ablation answers fewer errors, so clients retry less), so
   it would not hold by construction.
5. **"Writes applied = distinct writes sent"** from the CSV is asserted as acknowledged ≤ applied
   ≤ distinct sent (each operation has its own text); the exact per-statement "applied at most
   once" is checked from the fake's write log in Go (`CheckProduct`), since the CSV holds no
   statement identity.

## Stated limits

1. The retry is not coordinated with the rate gate (chunk 10) or the coalescer (chunk 11): a retry
   re-enters at the queue.
2. A failure is retried at most once whatever happens later; a retry's own zero-byte refusal is
   answered `lost-after-read`.
3. A `Breaker` seam other than `CoreBreaker`/`ClosedBreaker` without an `Open()` method reads as
   closed for the retry decision.
4. The cap is per instance and is not divided by N: the fleet's banked credit is N × cap.
5. `retry.floor` is the bucket's starting level (in tokens), not a time-based reserve: a fresh
   instance can retry `floor` times before deposits; after that only the 10% ratio refills it.
6. With the policy on, a statement whose classifier verdict is wrong cannot be retried wrongly:
   the decision reads only the write report, never `SafeRead`.

## Wave C

Edits outside `internal/retry` are additive one-liners at the retry call site and the request end
paths. Chunk 11 (coalescer): a retry re-enters at `enqueue`, after the coalesce stage, so it does not
join a flight; how a coalesced flight's waiters follow their owner's loss is chunk 11's to decide
at the join. Chunk 9 (fairness): the retry re-enters through `Queue.Push` with the request's
tenant.

## Fleet row (PLAN 8.2)

The ratio survives (each instance ≤ 10% of its own first attempts, so the fleet ≤ 10%); the bucket
floor multiplies and is divided by N in config (`retry.floor` / `fleet.instances`); the cap is per
instance. Chunk 13 measures it at three instances.

## Steady state

What grows: the budget level (capped at `retry.bucket_cap`), a request waiting to retry (it waits
in the admission queue). What removes it: the cap, the retry running, the request deadline (queue
and statement clamps), the client leaving. Gauges: `retry.budget_level`,
`engine.retries_pending`. After quiesce: nothing pending (`retryCheck`), no timer of the retry's
own (it arms none; it uses the queue and statement timers).

## Mechanism row

Where it sits: at the loss of a statement, after the lease and forward stages (`statementLost`).
Numbers: deposit one tenth per first attempt, withdraw one token per retry, cap
`retry.bucket_cap` (100), floor `retry.floor` (10) ÷ `fleet.instances`, deadline arrival +
`timeouts.statement`. What the caller gets: the retry's outcome, or the original row when the
policy declines. Signal: `retry.budget_level`, `retry.exhaustions`, `retry.refused{reason}`,
`engine.retry_rows{reason}`, `engine.retry_ends{outcome}`.

## Fuzz targets

| Target | Package | Seed source |
| --- | --- | --- |
| `FuzzRetryOps` | `internal/retry` | no simulator; operations decoded from the input, 3 bytes each, at most 256; time steps from the input; no hash |

It does not import `internal/sim`, so the pinned inventory of
`agent_work/adversarial/cr01-shrink-fuzz-rules/` is unchanged. Unplanted: 10 s locally with no
failure (36,525 executions); planted (`retry-withdraw-first`): found by a committed seed at once.

## Commands

```
go test ./internal/retry/... ./internal/proxy/... ./internal/config/... ./internal/obs/retrysrc/
go test -run '^TestScenarioRetry' ./test/e2e/                      # 3 seeds (pinned), both scenarios, 3 arms
DST_SEEDS=20 DST_SEED_BASE=4 RUN_DIR=/tmp/r go test -run '^TestScenarioRetry$' ./test/e2e/
scripts/csv-assert.sh /tmp/r/retry-storm test/analysis/retry-storm/*.sql
go test -run '^$' -fuzz '^FuzzRetryOps$' -fuzztime 30s ./internal/retry
scripts/fuzz-plant.sh
MG_TIER_R=1 ... scripts/tier-r.sh                                    # TestTierRRetryStorm
scripts/gen-docs.sh --check
scripts/gate.sh c12-retry
```

## Index rows

Added to `agent_work/INDEX.md` under "Phase 2, chunk 12": `scripts/fuzz-plant.sh` (changed),
`scripts/gen-docs.d/c12-fault-matrix.sh`, `chunks/c12-retry/gate/extra.sh`,
`docs/knowledge/retry-policy.md`.

## Size

Product Go added: 927 lines under `internal/` (estimate ~600), plus 1,023 lines of test-side
scenario code in `test/e2e/c12_*.go` (relay, client, runner); test files 1,909 lines.

## Gate result

**Not green.** Three build-stage runs of `scripts/gate.sh c12-retry`, all on clean committed
trees; the last on `7a27166` (2026-11-27 23:50 to 2026-11-28 ~02:10), `overall=fail`. The machine's
load average was 15–40 on 4 cores for the whole period (other chunks' gates and builds running
beside it). The two earlier runs (`2342158`, `f6fc153`) overlapped for a while, which I caused by
starting the second before the first had stopped; both were then killed.

| Stage | Last run | Cause |
| --- | --- | --- |
| G0, G1, G2, G3 counted, G4 markers, G5 fuzz (19 targets), G8 gen-docs, G9 site | pass | — |
| G3 unit | fail | `internal/obs/simrun` and `test/e2e` hit the gate's 30-minute `go test -timeout`. At the timeout `test/e2e` had spent ~29.5 min on earlier chunks' tests; `TestScenarioRetry` had run 29 s. On the landed chunks the whole G3 unit run took 425 s (c07) and 847 s (c08); here G3 unit took 2,474 s. In gate run 1 `test/e2e` passed in 1,729 s (with three retry seeds; now one); `simrun` passed alone in 561 s. |
| G3 race | fail | chunk 7's wall-clock test `TestFuzzPoolOpsCostIsBounded` (one execution 6.7 ms / 9.6 ms / 12.2 ms in the three runs, limit 5 ms); `internal/retry` and the other race packages passed |
| G4 sweep | fail | `test/e2e` hit `sweep.sh`'s 60-minute limit with no scenario failure reported; every other package's scenarios passed. Landed chunks' whole sweep: 670 s (c07), 394 s (c08). The retry scenarios add 20 seeds × 6 arm-scenarios × 2 runs to this package (about 5.5 min per 20-seed call on the loaded machine). |
| G6 tier-r | fail | `TestTierRSlowReader` (chunk 6, wall-clock slow-client drop: "dropped 0 pauses 34"); it passed in my full `scripts/tier-r.sh` run earlier the same day. `TestTierRRetryStorm` passed in both runs. |
| G7 csv | fail | two sweep runs (`e2e-reset-late` seed 25, `e2e-reset-wrong-tag` seed 36) have no `run.csv`: they were cut by the G4 timeout |
| G10 extra | fail, **fixed after the run** | my defect: `scripts/fuzz-plant.sh` grepped `FuzzRetryOps`'s failure for `reference`, but the planted bug is reported by the snapshot check ("10 tenths withdrawn for 0 retries"). The pattern is now `reference\|withdrawn for`; `gate/extra.sh` re-run alone after the fix: exit 0 (all four plants found, `FuzzRetryOps` after 2 s; retry canaries and both ablation arms pass). |

Apart from the G10 pattern, no failure was in a chunk 12 test or caused by chunk 12 logic, as far
as the logs show. What chunk 12 adds to the two time budgets that ran out: about 40 s of
`test/e2e` unit time and about 1 min of `simrun` baseline time (six retry runs, now in its parallel
job pool), and the retry sweep in G4. **The gate has to be re-run on a quiet machine** (the
orchestrator's landing gate). If G4 still runs out of time there, lower `retrySweepCap` in
`test/e2e/c12_retry_test.go` (20 now, the same cap as the pool scenarios). The 200-seed evidence
above does not depend on it. `gate/result.json` was not committed.
