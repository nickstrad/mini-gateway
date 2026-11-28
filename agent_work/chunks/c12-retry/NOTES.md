# Chunk 12 — Retry policy: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c12-retry`, worktree
`/root/Software/mini-gateway-work/c12`, on top of `f93f0f2` (chunk 7 + 8 landed on main).

## Repair round

Repair builder: Opus (`claude-opus-5-5`), 2026-11-28, same branch and worktree, on `cad0787`
(review 1: FAIL, 2 major, 6 minor; adversarial: 43 held, 5 broken). Brief:
`/root/briefs/zgateway-p2-c12-repair.md` (orchestrator decisions a–h).

| Finding / attack | Outcome | Commit | Regression test(s) |
| --- | --- | --- | --- |
| Review 1 (MAJOR) ablation arm never required to write twice or amplify; dead condition | **fixed** (decision a). `CheckAblation` fails unless the arm applies a statement twice, forwards more than its first attempts and fails `CheckProduct`; dead branch removed; `load.sql` (both scenarios) fails it when `applied <= distinct_sent` | `8091b27` | `TestScenarioRetry/*/everything`, `load.sql`. Reproduced first: the reviewer's M12 as described (cut before forwarding at after-forward, after-applied, mid-reply) still leaves 6 (storm) and 20 (sweep) duplicates on seed 1, from the `after-reply` kill (the reply and the reset race), so the new checks rightly pass on it. M12 in full (all four kill points that reach the database cut before the forward) gives 0 duplicates: it now fails `TestScenarioRetry/{retry-storm,retry-no-double-apply}/everything` on seeds 1–3 ("applied no statement twice") and `load.sql` (3 rows per scenario, "no duplicate write") |
| Review 2 (MAJOR) + `TestAdv_10_MatrixExplicitNeverPaths` matrix rows say "yes, once" for never-retried paths; merged queued/forwarded row | **fixed** (decision b). `MatrixRow.NotRetried` names the reason (`at or past the request deadline`, `breaker open`) and `Retried()` is false with one; the deadline row is split into "still waiting" (`shed` 53M01, reached no) and "still running" (`lost-after-write` 08007, may have); a row for a retry refused at the forward is added; `docs/fault-matrix.md` regenerated (32 rows) | `b20f854` | `TestEveryFaultMatrixRowStatesRetryAndATestThatExists` (exactly one retried row; reported 3 before), `TestOnlyTheZeroByteRefusalOfALentConnectionIsRetried`; the adversary's test now passes |
| `TestAdv_6_BreakerReopensBeforeQueuedRetryRuns` retry sent after the breaker opened | **fixed** (decision c): decision 10 above. Token not returned. Shipped wiring: the pool core's queue flush already prevents it | `5d9328c` | `TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded` (failed before: "the retry was forwarded after the breaker opened"), `TestRetryStillForwardedWhenTheBreakerStaysClosed`, `TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward` (real pool core; passed before the fix too, which is the evidence that the core prevents the schedule); the adversary's test now passes |
| `TestAdv_5_CarriedCreditExceedsWindowFloor`, `TestAdv_5_QuietHourRetainsBurstCredit` | **narrowed** (decision d), see "Narrowed claims"; capacity row changed through `retryCapacityRow` | `b20f854`, `f100fec` | `TestStoredCreditDoesNotExpireWithTimeAndTheLargestBurstIsTheCap`, `TestPropertyBudgetAgreesWithTheReferenceModel`, `TestTheCapacityTableStatesTheCapAsTheLargestRetryBurst`. Both adversarial tests stay red (original wording) |
| `TestAdv_7_WaitingRetryConsumesQueueSlot` | **narrowed** (decision e), see "Narrowed claims"; "no queue slot" corrected in decision 2 and the knowledge page | `a460aa1` (knowledge) | existing claim 7 tests. The adversarial test stays red (original wording) |
| Review 3 (minor) `Quiesce` ignores pending retries | **fixed** | `5d9328c` | `TestQuiesceRefusesAPendingRetry` (failed before) |
| Review 4 (minor) three untested retry-end paths | **fixed** | `5d9328c` | `TestRetryQueuedAndCancelledByTheClientEndsFailed`, `TestRetryQueuedWhenTheBreakerFlushesEndsShed`, `TestRetryInFlightWhenTheClientCloses`; mutations M8, M9, M10 now each fail a test (mutation rows R2–R4) |
| Review 5 (minor) tier R nil close; no "something happened" assertion | **fixed**: the deferred close checks `c != nil`; the run fails unless the policy was offered at least one lost statement (`retry.offered > 0`, both arms) | `8fc67e5` | `TestTierRRetryStorm` (tier R, run by the gate's G6) |
| Review 6 (minor) no tier S exhaustion; Deviation 1 misstated | **fixed** (decision g): Deviation 1 corrected; the storm's `on` arm runs a one-token budget, no extra simulated work, and must refuse for budget on seeds 1–3; not by construction (Deviation 1) | `8091b27` | `TestScenarioRetry/retry-storm/on` (`checkBudget`), `conservation.sql` |
| Review 7 (minor) run time | **fixed** (decision h): `window.check` linear (running minima); the trail rendered only on failure; the two scenarios run in parallel and, since six parallel arms ran in two waves under `-test.parallel` 4, the seeds too; `retrySweepCap` 10 | `f100fec`, `96ca162`, `8091b27`, `c9aeaa3` | `TestWindowCheckAgreesWithThePairwiseCheck` (linear = pairwise on 20,000 random windows, both outcomes seen) |
| Review 8 (minor) default-floor mismatch; spurious `retry.floor` error; two typos | **fixed**: an omitted floor is `min(10, bucket_cap × fleet.instances)` in `internal/config` and `min(10, cap)` in `retry.Config.WithDefaults` (only for a positive cap); `Validate` compares the floor to the cap only when the cap is valid; `budget.sql` header names `retry-no-double-apply`; the `simrun` row names `e2e.RetryBaselineJobs` | `f100fec`, `8091b27` | `TestAnOmittedFloorFollowsASmallCapAndABadCapNamesOnlyTheCap`, `TestRetryOmittedFloorFollowsASmallBucketCap` (both failed before) |

Timings (decision h), this VM, the same commands before (`cad0787`) and after; the machine was
shared (load average 5–7 on 4 cores during both, another gate running), so the CPU share varied:

| What | Before | After |
| --- | --- | --- |
| `go test -race -count=1 ./internal/retry/` | 26.9 s (review, at load ~17: 45.5 s) | 5.4 s |
| `go test -count=1 ./internal/retry/` | 8.1 s (review) | 0.8 s |
| `TestScenarioRetry`, 6 seeds (`DST_SEEDS=3 DST_SEED_BASE=101`: 1–3, 101–103), all arms, compiled test binary | 117.5 s (192 s CPU) | 101.8 s (172 s CPU); 139.5 s with arm-level parallelism only (two waves of four) |

The sweep cost falls by more than the table shows: `retrySweepCap` 20 → 10 halves the seeds per
gate sweep call. All six SQL files passed over the after-run's 18 runs per scenario.

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
   dirty table (`statementDeadline`). It does hold one place in its tenant's admission queue (the
   one-waiting-place decision); there is no other waiting state. The coalescer is not consulted
   for a retry (see "Wave C"). When a connection could be lent to a waiting retry the breaker is
   read again (repair round, decision 10).
3. **Breaker without `Allow`'s side effect**: the engine reads `breakerState.Open()` —
   `CoreBreaker.Open()` is `p.Breaker() != BreakerClosed` (half-open counts as open, so a retry is
   never the half-open trial's companion); `ClosedBreaker.Open()` is false; any other breaker seam
   without the method reads as closed (stated limit 3). The loss is reported to the pool core
   (`coreLost`) before the policy is asked, so a failure at the instant the breaker opens is not
   retried.
10. **A granted retry meets an open breaker before it is forwarded** (repair round): at the top of
   every `dispatch` pass, with the breaker seam's `Open()` true, every waiting retry is taken out
   of its queue (`Refuse` on the admission queue, `Remove` on the FIFO), answered `breaker-open`
   53M03, counted `Shed` and `BreakerFlushed`, and ends as retry end `breaker`
   (`refuseRetriesBehindOpenBreaker`). **The token is not returned**: the grant stays a retry in
   the policy (`Retries`, `Withdrawals = 10 × Retries` unchanged), so the budget bound and its
   proofs stand, nothing is counted twice, and the spent token is visible as
   `engine.retry_ends{outcome="breaker"}`. Refunding would need a deposit that is not a first
   attempt and would let an outage buy more retries. First attempts ≤ forwards ≤ first attempts + retries − ends
   `breaker` (`checkBudget`, `load.sql`, `TestTierRRetryStorm`); the upper bound is met exactly only
   when no granted retry ends in the queue (shed, cancelled or gone) before it is forwarded. Held by
   `TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded` (level, retries,
   withdrawals and offers unchanged from the grant; invariants hold).
   **In the shipped wiring this schedule does not happen**: the pool core emits `ActRefuseQueued`
   the moment its breaker opens, so the waiting retry is flushed with 53M03 (end `shed`) before any
   dispatch; it cannot be granted again while open or half-open (`CoreBreaker.Open()`). Shown
   through the real pool core by `TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward`
   (breaker failures 2: the zero-byte loss is failure 1 and grants, the replacement dial's failure
   opens it; end `shed` 1, end `breaker` 0, no write of the statement). The new check guards
   breaker seams other than `CoreBreaker` (the adversary's stub), and costs one pure read per
   `dispatch` pass.
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
| 5 | Budget bound (**narrowed**, see "Narrowed claims"): over any window `10·retries ≤ attempts + carried level ≤ attempts + cap`; from the start `≤ attempts + floor`; largest burst after a quiet period = the cap; integers | `TestPropertyBudgetAgreesWithTheReferenceModel` (the reference model's `window.check`, now linear, held against the pairwise check by `TestWindowCheckAgreesWithThePairwiseCheck`), `TestStoredCreditDoesNotExpireWithTimeAndTheLargestBurstIsTheCap`, `TestPathologicalSignalEveryRequestFailingPreForwardStaysUnderTheBound`, `TestAQuietHourCannotBankAStorm`, `TestBucketHoldingExactlyOneTokenGrantsOneRetry`, `TestBucketExactlyAtTheCap`; SQL `budget.sql` per flush window |
| 6 | Never when the breaker is open (at the decision and again before the forward) | `TestRetryNeverWhenTheLossOpensTheBreaker`, `TestRetryDecidedWithoutConsultingTheBreakersAllow` (no trial, no state change), `TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded`, `TestRetryStillForwardedWhenTheBreakerStaysClosed`, `TestRetryQueuedWhenThePoolCoreBreakerOpensIsFlushedBeforeAnyForward`; canary `.../breaker_check` |
| 7 | A waiting retry holds no connection and one place in its tenant's admission queue (**narrowed**); never past the deadline (equality) | `TestRetryAZeroByteRefusal...` (lent 0, queued 1 while waiting), `TestRetryWaitsInTheTenantQueueWithTheAdmissionRule`, `TestRetryRefusedAtTheRequestDeadlineExactly`, `TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt` (queued retry shed at the deadline exactly), `TestRetryForwardedStatementEndsAtTheRequestDeadline`, core `TestDeadlineAtEqualityRefusesAndJustBeforeGrants` |
| 8 | Storm bounded (off and on), refusals fast, goodput returns | `TestScenarioRetry/retry-storm/{off,on}` (`checkFast`, `checkRecovery`: every operation started after the recovery point succeeds on its first attempt, `checkBudget`: forwards = attempts + retries − retry ends `breaker`), SQL `load.sql`; tier R `TestTierRRetryStorm` |
| 9 | Exhaustion visible; decisions counted by reason; conservation on every exit path incl. `Shutdown`; one reply per retried request | `TestRetryBudgetExhaustionIsVisibleAndCounted`, `TestEveryRefusalReasonIsReachedAndCounted`, `TestRetryShutdownWithAPendingRetryEndsItAndConserves`, `TestRetryClientGoneWhileTheRetryIsQueued`, `TestRetryQueuedAndCancelledByTheClientEndsFailed`, `TestRetryQueuedWhenTheBreakerFlushesEndsShed`, `TestRetryInFlightWhenTheClientCloses`; exhaustion in tier S: `retry-storm/on` (one-token budget) must refuse for budget on seeds 1–3 (`checkBudget`), `conservation.sql` (`refused budget = exhaustions`, now non-zero); `Snapshot.Check` (`retryConserved`) run by every engine step's invariants, SQL `conservation.sql`; `internal/obs/retrysrc` `TestRetryCountersMoveAndConserve` |
| 10 | The matrix is complete; retries only on rows that allow them | `TestEveryErrorRowStatesWhetherTheProxyRetriesIt`, `TestEveryFaultMatrixRowStatesRetryAndATestThatExists` (each named test exists, each scenario has an analysis folder, retried rows are `lost-after-read`, reached no, outside a transaction, exactly one row retried), `TestOnlyTheZeroByteRefusalOfALentConnectionIsRetried` (only that row; a row naming a reason is never retried; a "no retry" client cell names its reason; the cell agrees with `Retried()`), `TestFaultMatrixDocumentIsGeneratedFromTheCode`; engine counter `RetryRows` checked by `CheckProduct` and `conservation.sql` |
| 11 | The ablation fails; the binary cannot select it | arm `everything`: `CheckAblation` (retries on forbidden rows > 0, a statement applied twice, forwards > first attempts, and the product checks fail), `load.sql` (forwards > attempts and applied > distinct sent); seed 1: 49 (storm) and 52 (sweep) statements applied twice; `TestRetryEverythingAblationRerunsAWriteThatMayHaveReached`; `TestRetryEverythingIsUnreachableFromTheBinary` (no kind name, `go list -deps ./cmd/...` has no `retrytest`), `TestConfigNeverSetsARetryPlant` |
| 12 | Fleet: floor ÷ N, ratio not; validation names the key | `TestRetryFloorIsDividedByInstancesAndTheRatioAndCapAreNot`, `TestRetryKeysFailClosedNamingTheKey`, `TestFloorShareDividesByInstancesAndRefusesBelowOne` |
| 13 | `Invariants` covers the retry accounting; a test fails if the block is removed; `Quiesce` refuses a pending retry and passes after shutdown | `TestQuiesceRefusesAPendingRetry`, `TestInvariantsCatchAPendingRetryNoRequestHolds`, `TestInvariantsCatchTheEngineAndThePolicyDisagreeing`, `TestInvariantsCatchABrokenBudget` (mutation M1); `TestRetryShutdownWithAPendingRetryEndsItAndConserves` |
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
  bytes accepted, and the policy grants the retry from the floor (4 tokens in the sweep, 1 in the
  storm's one-token budget: the bucket starts full at one token).
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

## Mutation proofs (scratch copy under `/tmp/zgw-c12/`, deleted after the run; each mutation was one string replacement and the listed tests)

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
| R1 (repair) drop the breaker re-check in `dispatch` | `TestRetryGrantedWhileClosedIsRefusedWhenTheBreakerOpensBeforeItIsForwarded` (and the adversary's `TestAdv_6_BreakerReopensBeforeQueuedRetryRuns`) |
| R2 (review M8) drop `retryEnded` in `clientCancel` | `TestRetryQueuedAndCancelledByTheClientEndsFailed` |
| R3 (review M9) drop `retryEnded` in `refuseWaiting` | `TestRetryQueuedWhenTheBreakerFlushesEndsShed`, `...PoolCoreBreakerOpensIsFlushedBeforeAnyForward`, `...GrantedWhileClosedIsRefused...` |
| R4 (review M10) drop `retryEnded` in `activeGone` | `TestRetryInFlightWhenTheClientCloses` |
| R5 `MatrixRow.Retried` ignores `NotRetried` | `TestEveryFaultMatrixRowStatesRetryAndATestThatExists` ("3 rows retried"), `TestOnlyTheZeroByteRefusalOfALentConnectionIsRetried` |
| R6 (review M12, full form) the relay cuts before forwarding at every kill point that reaches the database (`after-forward`, `after-applied`, `mid-reply`, `after-reply`) | `TestScenarioRetry/*/everything` seeds 1–3 ("applied no statement twice"), `load.sql` ("no duplicate write", 3 rows per scenario) |

## Sweeps (200 seeds)

Both tier S scenarios, all three arms (`off`, `on`, `everything`), **207 distinct seeds**
(1–207), every seed run twice for the replay law: 1,242 distinct (scenario, arm, seed) runs, all
passed, 0 failed. The runner takes at most 20 seeds per call (as chunk 7's) and the three pinned
seeds (1, 2, 3, `test/e2e/testdata/seeds.txt`) come first, so the sweep was twelve calls with
`DST_SEEDS=20` and `DST_SEED_BASE` 4, 21, 38, …, 191 (a scratch script, one `RUN_DIR`
per call; each call: 120 passed, 0 failed). Distinct seeds
counted from the `--- PASS` lines of all twelve logs.

`scripts/csv-assert.sh <run-dir>/<scenario> test/analysis/<scenario>/*.sql` over every call's
runs: all 24 (12 calls × 2 scenarios) green: "3 SQL files run, 0 failures"
each (`budget.sql`, `conservation.sql`, `load.sql`; DuckDB v1.5.5). The scratch logs were deleted after the counts were recorded here.

Without `DST_SEEDS` (the unit run, `go test ./...`) `TestScenarioRetry` runs seed 1 only, so the
package stays inside the gate's 30-minute `go test` limit; the gate's own sweep (G4) sets
`DST_SEEDS` and, since the repair round, runs at most 10 seeds per scenario and arm
(`retrySweepCap`), the two scenarios in parallel.

The 207-seed sweep above ran before the repair round. After it the storm's `on` arm runs a
one-token budget and the ablation arm has stronger checks; re-run since: seeds 1–3 and 101–103,
all arms, both scenarios, replay and flush checks and all six SQL files (see "Repair round"
timings), and a probe of `retry-storm/on` over seeds 7–60 (`CheckProduct` passed on all 54; no
replay or SQL in the probe).

## The completed matrix

`docs/fault-matrix.md` (generated; 32 rows: every row of chunks 6 and 7, and chunk 12's). Every
row states "Retried by the proxy?"; exactly one says yes: the first write of a request refused with
zero bytes on a lent connection, outside a transaction (answered `lost-after-read` when declined).
A row whose error row would allow a retry but whose path never does names its reason in
`MatrixRow.NotRetried` (`at or past the request deadline`, `breaker open`). `docs/error-contract.md` gained the same column for
every row (only `lost-after-read` says yes, with its conditions).

## Shared files

| File | Change |
| --- | --- |
| `internal/proxy/seams.go` | `Failure` gained `Attempt`, `BreakerOpen`, `Draining`, `Deadline` (appended) |
| `internal/proxy/lease.go` | `statementLost` offers the failure and re-runs a granted retry before counting it failed; `forward` reports first attempts and clamps a retry's statement deadline; `enqueue` clamps a retry's queue wait; `shed` and `replyDone` call `retryEnded`; `dispatch` first refuses waiting retries behind an open breaker (repair) |
| `internal/proxy/client.go` | `request` gained `attempt`, `retryOver`; `dequeueGone`, `activeGone` call `retryEnded` |
| `internal/proxy/corepool.go` | `clientCancel` (queued) and `refuseWaiting` call `retryEnded`; `refuseWaiting` takes the retry end (repair) |
| `internal/proxy/engine.go` | `New` calls `initRetry` (replaces the `RetryOff{}` default); `Invariants` runs `retryCheck` after `admissionCheck`; `Quiesce` refuses a pending retry (repair) |
| `internal/proxy/snapshot.go` | retry counters appended to `Snapshot`; `Check` runs `retryConserved` |
| `internal/proxy/config.go` | `Config.Retry` appended; `Validate` validates it |
| `internal/proxy/contracts.go` | one retry timeout row and one capacity row appended |
| `internal/config/config.go`, `validate.go`, `pool.go` | `File.Retry` (`retry` section), defaults, validation, `proxyCaps` passes it, `Warnings` appends the floor remainder |
| `internal/pgwire/errors_doc.go` | the "Retried by the proxy?" column and the closing paragraph |
| `internal/obs/catalog/catalog.go` | declares component `retry` |
| `internal/obs/enginesrc/enginesrc.go` | engine retry metrics; `retrysrc` source added to `Sources` |
| `internal/obs/simrun/engine_test.go` | baseline runs `e2e.RetryBaselineJobs` (not under `-short`); retry scenarios listed as short-skipped |
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

1. **Exhaustion in the scenarios is shown on pinned seeds, not by construction.** Corrected
   reason (review minor 6): before the repair the scenarios retried 12–17 (storm) and 4–9 (sweep)
   times, but the `on` arm's floor of 4 tokens plus 450–835 first attempts (a tenth of a token
   each, 503–555 and 217–266 tenths lost at the 20-token cap) kept the bucket far above one
   token, so `retry.exhaustions` stayed 0. The doom pattern does produce several retries per run
   (every tenth faulted query dooms its connection); it is the budget that was too large, not the
   retries too few. Repair: the storm's `on` arm now runs a one-token budget (`Cap`/`Floor` 1,
   `stormCapTokens`), which costs no extra simulated work. With it the budget is refused 1–3 times
   on seeds 1–6 and on most of seeds 7–60, but on at least two of seeds 7–60 not at all (a probe,
   `CheckProduct` passed on all 54), so exhaustion is not guaranteed by construction. The run
   therefore requires a budget refusal only on the pinned seeds 1, 2, 3 (`RetrySpec.Exhausts`,
   deterministic by the replay law); `conservation.sql`'s `refused_budget = exhaustions` now
   compares non-zero values. The engine-level proofs (`TestRetryBudgetExhaustionIsVisibleAndCounted`
   and the core tests) stand.
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

## Narrowed claims

- **Claim 5 (the budget bound), narrowed by the orchestrator (repair decision d).** The token
  bucket stands. As built and proven: *over any window, ten times the retries is at most the first
  attempts in the window plus the credit carried into it (in tenths), and that credit is at most
  the cap; from the start of the run, ten times the retries is at most the first attempts plus the
  floor. So the largest burst of retries after a quiet period is the cap (`retry.bucket_cap`
  tokens), and stored credit does not expire with time; the cap only discards deposits above it.*
  The brief's "≤ 10% of first attempts plus the floor over any window" holds only for windows
  starting at the start of the run. Held by `TestPropertyBudgetAgreesWithTheReferenceModel`
  (the reference model checks both window forms and the from-start form after every run; the
  window check is linear and held against the pairwise one by
  `TestWindowCheckAgreesWithThePairwiseCheck`), `TestStoredCreditDoesNotExpireWithTimeAndTheLargestBurstIsTheCap`
  (bucket full, one idle virtual hour, 100 eligible failures: exactly the cap granted, then budget
  refusals), `TestAQuietHourCannotBankAStorm` (a million deposits still give only the cap), and in
  the CSV by `budget.sql` (per flush window with the carried level). The capacity table's retry
  row states the cap as the largest burst (`retryCapacityRow`, held by
  `TestTheCapacityTableStatesTheCapAsTheLargestRetryBurst` and the generated-document check). The
  adversary's `TestAdv_5_CarriedCreditExceedsWindowFloor` and `TestAdv_5_QuietHourRetainsBurstCredit`
  assert the original wording and stay red until the orchestrator settles them.
- **Claim 7 (a waiting retry holds nothing), narrowed by the orchestrator (repair decision e).**
  As built: *a waiting retry holds no database connection; it holds one place in its tenant's
  admission queue (the one-waiting-place decision), and it never runs past the request's deadline
  (tested at equality).* Held by `TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply`
  (lent 0, queued 1), `TestRetryWaitsInTheTenantQueueWithTheAdmissionRule` and the deadline tests
  of claim 7. "No queue slot" is corrected in decision 2 and in `docs/knowledge/retry-policy.md`.
  The adversary's `TestAdv_7_WaitingRetryConsumesQueueSlot` asserts the original wording and stays
  red until settled.

The three adversarial tests above are not wrong: each asserts the brief's original wording
exactly, which the orchestrator has narrowed. No adversarial test asserts something no claim said.

## Stated limits

1. The retry is not coordinated with the rate gate (chunk 10) or the coalescer (chunk 11): a retry
   re-enters at the queue.
2. A failure is retried at most once whatever happens later; a retry's own zero-byte refusal is
   answered `lost-after-read`.
3. A `Breaker` seam other than `CoreBreaker`/`ClosedBreaker` without an `Open()` method reads as
   closed for the retry decision and for the re-check before the forward.
7. A retry refused at the forward because the breaker opened keeps its withdrawn token (decision
   10); the budget is not refunded.
8. Budget exhaustion in tier S is required only on the storm's pinned seeds 1–3 (Deviation 1).
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
`engine.retries_pending`. After quiesce: nothing pending (`Quiesce` and `retryCheck`), no timer of
the retry's own (it arms none; it uses the queue and statement timers).

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
Repair round (net against `cad0787`): +57 product lines under `internal/`, +16 in
`test/e2e/c12_*.go`, +304 test lines.

## Gate result

**Repair round:** `scripts/gate.sh c12-retry` (build stage) at `0153f43`, 2026-11-28 03:51–05:34,
clean tree (another change's gate was running beside it, load 5–8 on 4 cores): **overall fail,
counts_for_landing false, red only in G3 unit, and there only on the three narrowed-claim
adversarial tests** (`TestAdv_5_CarriedCreditExceedsWindowFloor`,
`TestAdv_5_QuietHourRetainsBurstCredit`, `TestAdv_7_WaitingRetryConsumesQueueSlot`; red by the
orchestrator's decisions d and e, code not bent, adversarial tests not edited). Every other
package passed G3 unit (971 s; `test/e2e` 684 s, which hit the 30-minute limit before the repair).
All other stages pass: G0, G1, G2, G3 counted, G3 race (50 s; `TestFuzzPoolOpsCostIsBounded`
passed this time), G4 markers, G4 sweep (1,119 s; the retry scenarios at 10 seeds per arm), G5
fuzz (19 targets), G6 tier R (310 s, `TestTierRRetryStorm` with the new "a statement was lost"
assertion and `TestTierRSlowReader` both passed), G7 csv (5,133 run directories, 91 scenarios),
G8 gen-docs, G9 site, G10 extra (all four plants found).

An earlier start of the gate on `d4625cc` was stopped by me after G1 detlint failed (a range over
`e.waiting` with a filter in the loop body; fixed in `0153f43` by collecting the ids first, the
form the lint accepts); the scratch folders its killed G3 run left under `agent_work/adversarial/`
were deleted. Before the gate, `go test -count=1 ./internal/... ./cmd/... ./agent_work/adversarial/c12-retry/...`
passed except the same three tests; the full `go test -count=1 ./...` is the gate's G3 unit run
above (run once, not repeated). `gate/result.json` is not committed (moved out of the tree).
