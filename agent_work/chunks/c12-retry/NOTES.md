# Chunk 12 — Retry policy: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c12-retry`, worktree
`/root/Software/mini-gateway-work/c12`, on top of `f93f0f2` (chunk 7 + 8 landed on main).

## Join repair round

Repair builder: Opus (`claude-opus-5-5`), 2026-11-28, branch `work/c12-retry`, the one repair
round after `REVIEW-3.md` (NITS, four minor; the orchestrator fixed all four). **Test code only:
none of the new tests found a defect, and no product code changed.**

| Finding | Commit | Test |
| --- | --- | --- |
| 1 weights not asserted | `5599506` | `TestScenarioRetryStormTwoTenantsOfUnequalWeight` now requires tenant 1 (weight 1) to spend more than `weightedAtShareRatio` (10) times as long at its share (`TenantSnapshot.AtShare`) as tenant 0 (weight 3). Measured with the weights on, seeds 1–10: tenant 1 at 252–382 ms, tenant 0 at 0–2.6 ms (at least 100 times longer). Without the weights (the reviewer's mutation M-w1, `Fairness: admission.FairConfig{}`): ratio 0.48–1.6. M-w1 now fails the test on seeds 1, 2 (ratio) and 3 (no request ever held at its share). |
| 2 exhaustion on pinned seeds | `2f91e85` | `retry-storm/on`: `checkBudget` requires `Refused[budget] > 0` on **every** seed (`RetrySpec.Exhausts` is now a bool, set by the storm and inherited by the weighted variant; the pinned seed list is gone). Construction below. |
| 3 four untested interactions | `bafef03` | (a) `TestJoinARetryOverflowingItsTenantQueueShedsTheEarlierWaitingRetry`, `TestJoinARetryRefusedAtTheTenantCapIsShedAndEndsItsRetry`; (b) `TestJoinARetryAboveItsShareTakesAFreeLeaseButNotTheLastOneReserved`; (c) `TestJoinARetryHeldByAFairLimitBelowTheConnectionsIsNoDemand`; (d) `TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseIsLostWhileARetryIsHeldBack`, all in `internal/proxy/join_retry_fair_test.go` |
| 4 package global reset without `defer` | `2be82a6` | `TestJoinEveryRetryEngineTestHoldsBehindTheFairDispatcher` resets `coreAdmission` with `defer` |

Knowledge: `docs/knowledge/retry-policy.md`, section "Forcing budget exhaustion in a tier S
scenario" (`3871406`).

### Finding 2: exhaustion by construction

Two facts from a trace of the policy (a throw-away wrapper around `retry.Policy` that logged every
`Attempt` and `Decide`, in a scratch copy): (1) the bucket gets a deposit only at a forward of a
first attempt (`Engine.forward` → `attempted`); (2) a write to a doomed connection is refused, and
the retry decided, in the same instant as its forward, before any other lend. A doom burst
therefore gives exactly one eligible zero-byte refusal: the pool marks the siblings suspect and
checks them before they are lent again. That is why the old storm exhausted only when the relay's
"doom every tenth query" pattern happened to put two eligible losses within ten first attempts.

The storm now runs `0–450 ms`, a doom burst at `600 ms`, a **spend phase** `700–720 ms`
(`stormSpendAt`, `stormSpendFor`), a second burst at `800 ms`, then `850–3500 ms`
(`stormExhaustAt`). The relay's fault window moved from `700–1700 ms` to `850–1850 ms` (same
length), so the spend phase has no fault except burst 1. In the spend phase each client sends one
operation (20 ms is shorter than `retryClientGap`, so its next one waits for `850 ms`). The first
lend lands on a doomed connection and its retry takes the one token (level 10 → 0). At most the other
7 clients' first attempts deposit after it (≤ 7 tenths), and nothing fails there that a client
would resend. At `850 ms` every connection has been idle for 50 ms and is doomed. The first
forward deposits one tenth, the level is at most 8 < 10, and the retry is refused for budget, on
every seed and whatever order the dispatcher serves in. Traced: level 8 at `850.1 ms` on seeds
1, 2, 4, 8. The margin is `10 - clients` tenths, so the construction holds for fewer than ten
clients. No simulated time was added (still 4 s). `RelayPlan.DoomAllAt` became a list. The sweep
scenario keeps one burst at `300 ms`.

Seeds 1 to 30, arm `on`, `Policy.Refused[budget]` (probe: `RunRetry(sp, ArmOn, seed, "", "")`
plus `CheckProduct`, and `CheckAblation` on the `everything` arm; the "no dispatcher" columns
come from a scratch copy whose `proxy.New` skips `installFair`). All 360 runs passed every check:
2 specs × 3 arms × 30 seeds × 2 builds.

| Seed | `retry-storm`, fair dispatcher | `retry-storm`, no dispatcher | `retry-storm-weighted`, fair | `retry-storm-weighted`, no dispatcher |
| --- | --- | --- | --- | --- |
| 1 | 3 | 3 | 4 | 3 |
| 2 | 1 | 3 | 2 | 3 |
| 3 | 4 | 2 | 3 | 2 |
| 4 | 5 | 2 | 1 | 2 |
| 5 | 2 | 2 | 2 | 2 |
| 6 | 2 | 2 | 1 | 2 |
| 7 | 2 | 3 | 1 | 3 |
| 8 | 3 | 3 | 2 | 3 |
| 9 | 1 | 5 | 2 | 5 |
| 10 | 1 | 4 | 2 | 4 |
| 11 | 3 | 3 | 3 | 3 |
| 12 | 4 | 4 | 3 | 4 |
| 13 | 2 | 2 | 4 | 2 |
| 14 | 3 | 3 | 3 | 3 |
| 15 | 2 | 2 | 2 | 2 |
| 16 | 3 | 1 | 5 | 1 |
| 17 | 2 | 3 | 3 | 3 |
| 18 | 1 | 2 | 2 | 2 |
| 19 | 2 | 4 | 3 | 4 |
| 20 | 3 | 2 | 2 | 2 |
| 21 | 1 | 4 | 2 | 4 |
| 22 | 2 | 1 | 1 | 1 |
| 23 | 2 | 2 | 4 | 2 |
| 24 | 3 | 2 | 1 | 2 |
| 25 | 2 | 2 | 3 | 2 |
| 26 | 2 | 2 | 2 | 2 |
| 27 | 1 | 1 | 1 | 1 |
| 28 | 3 | 3 | 1 | 3 |
| 29 | 7 | 4 | 2 | 4 |
| 30 | 4 | 3 | 3 | 3 |

Without the dispatcher the two specs give the same numbers, as they should: without the fair
dispatcher the tenant count and the weights change nothing. Before the repair, behind the
dispatcher, seeds 2, 4 and 8 of 1–10 never exhausted (`[3, 0, 4, 0, 2, 1, 2, 0, 1, 4]`).

### What moved

- `retry-storm` (all arms) and `retry-storm-weighted`: a new schedule, so every digest and every
  flushed file of the storm changed. Nothing stores them: no golden digest or committed baseline
  holds a retry scenario (the `internal/obs/simrun` baseline reruns the storm at seed 1 and checks
  it; it passes, below). `scripts/gen-docs.sh --check` passes: no generated document moved.
- The storm's fault window starts at `850 ms` instead of `700 ms`, and its recovery point
  (`3200 ms`) is unchanged.
- Unchanged: `retry-no-double-apply` (same single burst), `CheckProduct` and `CheckAblation`,
  `budget.sql`, `conservation.sql`, `load.sql`, both canaries, the tier R storm.
- Superseded: "The one place the join moved a test" below (the pin to 1, 3, 5), Deviation 1 and
  stated limit 8 (both now say exhaustion is by construction).

### Finding 3: the four join tests

- (a) **Overflow:** tenant ring of 1 (`queue.tenant_capacity`), two writes of tenant 0 lent on two
  idle connections, both refused with zero bytes. The second retry's push sheds the first
  (`ReasonOverflow`), whose client gets `53M01` (retry end `shed`), and `RetryPending` stays 1.
  The surviving retry is lent to the replacement connection and completes: ends `shed` 1 and
  `completed` 1, pending 0, fair lease starts and ends equal to the engine's. **Tenant cap:**
  `queue.max_tenants` 2, one connection. Tenant 0's write is lent and tenants 1 and 2 queue, so
  tenant 0's empty queue is evicted for tenant 2. The refused write's retry finds no tenant slot
  (`ReasonTenants`) and is shed `53M01`: asked 1, pending 0, end `shed` 1, the other two still
  queued, tenant 0 in flight 0.
- (b) Limit 6, equal weights (share 3). Tenant 0 holds 4 transactions plus a write, and tenant 1
  holds 1 transaction. After the refusal tenant 0 is at 4 of its share 3: the retry is held, with
  `Releasable` 0 and no dial (the last lease is reserved for tenant 1). Tenant 0 commits one
  transaction (3, two leases free): the work-conserving rule lends the freed connection to the
  retry. During that reset the retry was releasable, so the pool dialled once; the test opens that
  dial so a lease is idle. A new tenant-0 request is held (`HeldShare` +1, `Releasable` 0, nothing
  written to the idle connection), and tenant 1's request gets the idle connection. Mutation:
  removing the last-slot reservation from `Fair.Pick` fails it.
- (c) `pool.connections` 4, `fairness.limit` 3. The test installs the fair dispatcher through the
  `Queue` seam (`seamFair`: the same `admission.NewFair(c.FairConfig())` the engine installs) so
  it can read `Queues.Releasable` without new product code. Tenant 0 holds a transaction and a
  write, tenant 1 a transaction (3 = the limit), and tenant 2's request waits (`Releasable` 0, no
  dial). After the refusal: `Releasable` 1, one dial, and tenant 2 (below its share) gets it.
  Then the retry waits at the limit: `Releasable` 0, no dial over five 100 ms events, 3 open of 4.
  Tenant 1 commits and the retry gets that connection without a dial, then completes. **Mutation
  M-c2 (`checkoutDemand` returns `0, false`) now fails this join test.**
- (d) The premise of `retryHeldAtShare`, plus a tenant-1 `SELECT pg_sleep(1)` running on a new
  connection. Tenant 1's transaction commits, so one connection is idle while the retry stays held
  (last slot reserved for tenant 1). Then the running statement's connection is lost
  (`EvDBClosed`, not in a transaction, so the client stays open and nothing else dispatches). Breaker
  seam closed: the retry gets the idle connection in that event. Open: the retry is answered
  `53M03`, retry end `breaker`, nothing forwarded. **Mutation M-b3 (no `fairRedispatch` in
  `Engine.finish`) now fails this join test.** A first version lost tenant 1's *transaction*
  instead: closing that client dispatches on its own, so M-b3 survived. That is why the lost lease
  is a plain statement.

Mutations in a scratch copy (`/tmp/zgw-c12/mut`, deleted after), `go test -run '^TestJoin'
./internal/proxy/`:

| Mutation | Result |
| --- | --- |
| M-b3 `e.fairRedispatch()` removed from `Engine.finish` | killed: `TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseIsLostWhileARetryIsHeldBack` |
| M-c2 `checkoutDemand` returns `0, false` | killed: `TestJoinARetryHeldByAFairLimitBelowTheConnectionsIsNoDemand` |
| the last-slot reservation removed from `Fair.Pick` | killed: `TestJoinARetryAboveItsShareTakesAFreeLeaseButNotTheLastOneReserved` |
| M-w1 `Fairness: admission.FairConfig{}` in `RetrySpec.engineConfig` | killed: `TestScenarioRetryStormTwoTenantsOfUnequalWeight` (seeds 1–3) |
| the storm's second doom burst removed (`DoomAllAt` = 600 ms only) | killed: `checkBudget` fails `retry-storm/on` seed 7 ("the budget was never exhausted"; probe over seeds 1–10, the other nine still exhaust by chance) |

### Results

All on 2026-11-28 at the repair's code commits (`2be82a6`..`5599506`; NOTES only after them), load
average 5–15 on 4 cores (other sessions' runs beside these):

| Check | Result |
| --- | --- |
| `go build ./... && go vet ./...`, `gofmt -l` | pass, nothing listed |
| `go test -count=1 ./internal/proxy/... ./internal/admission/... ./internal/retry/... ./internal/config/...` | all pass |
| `go test -count=1 ./internal/obs/simrun/` (the baseline, the storm at seed 1 included) | pass (525 s) |
| `GOMAXPROCS=2 go test -count=1 -run '^(TestScenarioRetry\|TestCanaryRetry\|TestScenarioFair\|TestCanaryFair)' ./test/e2e/` (seed 1) | pass (161 s): every fairness scenario and canary, both retry scenarios × 3 arms, the weighted storm seeds 1–3, both retry canaries |
| `./agent_work/adversarial/c09-fairness/...`, `./agent_work/adversarial/c12-retry/...` | both pass unchanged (no file under `agent_work/adversarial/` edited) |
| `scripts/gen-docs.sh --check` | pass (all four generators current) |
| 10-seed sweep, `DST_SEEDS=10 RUN_DIR=… go test -run '^(TestScenarioRetry\|TestScenarioRetryStormTwoTenantsOfUnequalWeight)$' ./test/e2e/` | pass (190 s): `retry-storm` and `retry-no-double-apply`, 3 arms × seeds 1–10 each, replay law and flushed-file equality; the weighted storm seeds 1–3 |
| `scripts/csv-assert.sh <sweep>/<scenario> test/analysis/<scenario>/*.sql` | 0 failures: `retry-storm` (`budget.sql`, `conservation.sql`, `load.sql` over 30 runs), `retry-no-double-apply` (the same three over 30 runs). Given the sweep root alone the script also lists the 94 scenarios with SQL files and no run in that root; that is a usage artefact, not a failure of these runs |
| `GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh c12-retry` | at `ea59edd`, clean tree: **overall pass** (`counts_for_landing` false by `GATE_ONLY`): G0 all, G1 detlint (30 patterns), importlint, boundlint, paniccheck, G2 comments (baseline `d5dc685`), G3 unit (850 s, every package including both adversarial packages), G3 counted, G3 race (127 s). The rewritten evidence logs under `agent_work/adversarial/` were restored with `git checkout`; `gate/result.json` and the untracked `gate/` logs removed |

## Join with chunk 9

Join builder: Opus (`claude-opus-5-5`), 2026-11-28, branch `work/c12-retry`, worktree
`/root/Software/mini-gateway-work/c12`. `git merge --no-ff main` (main at `d5dc685`: chunk 9 landed,
with the lab-branch port `91fc9b3`) on top of `54385bc` (review 2 NITS, closed): one conflict
resolution in merge commit `1d15364`, then join tests only (`17f36af`, `5009cbf`, `8544f8b`,
`fec17ad`, `6f9a0bb`). **No product code changed beyond the conflict resolution**: every place the two
chunks meet already composed in the stage order of D1 (classify, gate, coalesce, queue, lease,
forward, release); chunk 9 owns the choice inside the queue stage, chunk 12 re-enters a granted
retry at the queue stage. No behaviour of either chunk was dropped or weakened, and no conflict
between them was found.

### Conflicts and how each was resolved

| File | Resolution |
| --- | --- |
| `internal/config/config.go` | `File` carries both sections, `Retry` and `Fairness`. |
| `internal/config/validate.go` | `applyDefaults` applies both (`applyRetryDefaults`, `applyFairnessDefaults`); `proxyCaps` passes both `Fairness: f.fairness()` and `Retry: f.retryCore()`. |
| `internal/proxy/config.go` | `Config` carries `Retry retry.Config` and `Fairness admission.FairConfig`; `Validate` (auto-merged) validates both. |
| `internal/proxy/contracts.go` | Capacity rows: every earlier row, then chunk 12's `retryCapacityRow`, then chunk 9's `fairnessCapacityRows`. |
| `internal/proxy/engine.go` | `Invariants` runs `admissionCheck`, then chunk 9's `fairnessCheck`, then chunk 12's `retryCheck`, then the pool core checks (decision (d)). `Quiesce` (auto-merged) has both blocks. |
| `internal/obs/simrun/engine_test.go` | Under `-short` both chunks' scenario names are skipped (`RetryScenarioNames()` and `FairScenarios()`); the baseline runs both (auto-merged). |
| `scripts/fuzz-plant.sh` | Both plants: `FuzzRetryOps` / `retry-withdraw-first` and `FuzzFairnessOps` / `fairness-unpop-turn`, with both header paragraphs. |
| `test/e2e/realtier.go` | `TierRScenarios` lists `tierr-fair-noisy` and `tierr-retry-storm`. |
| `docs/capacity-table.md`, `docs/csv-schema.md` (generated) | Regenerated with `scripts/gen-docs.sh`, never hand-merged. `docs/fault-matrix.md`, `docs/error-contract.md`, `docs/timeout-budget.md` came out byte-identical to chunk 12's. |
| `docs/c4/`, `book/`, `README.md` | Main's side entirely (`git checkout main -- docs/c4 book README.md`). |
| `agent_work/INDEX.md` | Both sides' rows (auto-merged). |

`internal/proxy/lease.go`, `corepool.go`, `client.go` auto-merged: chunk 9's lease start in
`lend`, lease ends beside every `LeasesEnded++` (`release`, `leaseLost`, `coreRelease`,
`coreDirty`), `fairRedispatch` at the top of `finish`, the releasable demand in `corePump` and
`CorePool.Checkout`; chunk 12's offer and re-run in `statementLost`, the clamps in `enqueue` and
`forward`, the breaker re-check at the top of every `dispatch` pass, `retryEnded` on every end path.

### Where the chunks assumed different things, and what holds now

**(a) A granted retry holds no lease while it waits; its lease starts once when it is lent
again.** Chunk 12 assumed "the failed connection was closed or sent to the dirty table before the
retry waits"; chunk 9 reports a lease end exactly there. The two orders compose: a zero-byte
refusal ends in `leaseFailed` → `leaseLost` (`LeasesEnded++`, `fairEnded(d.lastTenant)`) →
`coreLost` → `statementLost` → `offerRetry` → `rerun` → `enqueue` (push into the tenant queue). A
statement deadline ends in `coreDirty` (`LeasesEnded++`, `fairEnded`) before `statementLost`. So
the tenant's in flight falls by one before the retry is pushed. When it is lent again, `lend` is
the only place a lease starts (`LeasesStarted++`, `fairStarted`), once. No path counts a retried
request as two leases at once or as none: chunk 9's `fairnessCheck` (per tenant in flight = the
connections the engine lends it; total = `LeasesStarted - LeasesEnded`) runs after every event in
every simulator run with an admission queue, including the retry scenarios, which have always run
behind the admission queue (`RetrySpec.engineConfig`, rule `timeout`) and therefore now run behind
the fair dispatcher. Held by `TestJoinARetryHeldBackAtItsShareOpensNoConnection` and the shared
premise `retryHeldAtShare` (tenant 0 in flight 3 → 2 at the refusal, total 3, `LeaseEnds` +1, the
retry queued in tenant 0's queue, `Invariants` hold), `TestJoinARetryHeldBackAtItsShareIsLentWhenItsOwnLeaseEnds`
(`LeaseStarts` +1 exactly when it is lent), and
`TestJoinARetriedTenantAtItsShareLetsTheTenantBelowItsShareGoFirst` (fair lease starts and ends
equal the engine's at the end). Mutation: dropping `fairEnded` from `leaseLost` fails all eight
join tests.

**(b) A retry held back by the dispatcher still ends at the request's deadline, and the breaker
is re-read wherever a connection could be lent.** The deadline clamp is on the retry's queue timer
(`enqueue`, `retryClamp`) and on its statement timer (`forward`); neither depends on why the retry
waits, so a retry held at its tenant's share (or by the limit) is shed `53M01` (retry end `shed`)
at the request deadline exactly and never forwarded past it:
`TestJoinARetryHeldBackByTheDispatcherEndsAtTheRequestDeadline` (still pending one nanosecond
before, `53M01` at the deadline, not forwarded; the other tenants' leases are open transactions so
no other timer intervenes). Mutation: dropping the queue-wait clamp fails it. Every lend goes
through `dispatch` (the only `lend` callers are `dispatch` and chunk 6's direct lend in `enqueue`,
which the admission queue never takes), and `refuseRetriesBehindOpenBreaker` runs at the top of
every `dispatch` pass. Chunk 9 added two ways to reach `dispatch`: `fairRedispatch` at the end of
every event while the dispatcher holds a request back, and the dispatches that follow a lease end
of another tenant (reset done, as-is release); both reach the same loop. Held by
`TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseEndsWhileARetryIsHeldBack` (two arms: the
other tenant commits and its connection frees; with the breaker seam closed the freed connection
goes to the retry, with it open the retry is answered `53M03`, retry end `breaker`, nothing
forwarded) and `TestJoinThePoolBreakerOpeningFlushesARetryHeldBackByTheDispatcher` (the real pool
core: the replacement dial fails, the breaker opens and its flush answers the held-back retry and
the other tenant's queued request `53M03`; retry end `shed`, `BreakerFlushed` 2, fairness in flight
unchanged). Mutation: removing the re-check from `dispatch` fails the first.

**(c) The pool's demand.** Chunk 9 tells the pool only what the dispatcher would release
(`Queues.Releasable` in `corePump`, the checkout refresh below the pool size). A waiting retry is
an ordinary entry of its tenant queue, so it is demand exactly when the dispatcher would release
it; nothing in chunk 12 reports demand of its own. Held by
`TestJoinARetryHeldBackAtItsShareOpensNoConnection` (limit 4, weights equal: tenant 0 at its share
of 2, tenant 1 active below its share with nothing waiting, one lease free: no dial at the refusal
and none over five 100 ms events, the retry stays queued) and
`TestJoinARetriedTenantAtItsShareLetsTheTenantBelowItsShareGoFirst` (when tenant 1 sends a request
exactly one dial is made, for it). Mutation: passing `Queue.Len()` to `pool.Demand` again fails
four join tests.

**(d) `Invariants`, `Quiesce`, `Shutdown`.** Every check of both runs. `Invariants`: snapshot
conservation (`Snapshot.Check`, including chunk 12's `retryConserved`), lease pairing, queue and
wire checks, `admissionCheck`, chunk 9's `fairnessCheck`, chunk 12's `retryCheck`, the pool core.
Each block returns early only when it fails, so all run on a passing engine. `Quiesce`: chunk 12's
`RetryPending == 0` and chunk 8's `admissionQuiet`, which ends in chunk 9's `fairQuiesce` (no fair
state, nothing in flight). `Shutdown` is unchanged by both: queued requests (a waiting retry
included) end as client-gone (retry end `gone`), lent ones through the dirty-table / release paths
that report the lease end. Tests that fail when a block is removed (mutations in a scratch copy):
fairness block of `Invariants` — `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions`;
retry block of `Invariants` — `TestInvariantsCatchAPendingRetryNoRequestHolds`,
`...TheEngineAndThePolicyDisagreeing`, `...ABrokenBudget`; retry block of `Quiesce` —
`TestQuiesceRefusesAPendingRetry`; fairness block of `Quiesce` — **no test failed** when
`fairQuiesce` was removed (the engine's own lent-connection checks catch every reachable engine
state first), so the join adds `internal/admission` `TestQuiesceRefusesALeaseStillInFlightAndPassesOnceItEnds`,
which fails with it removed. Shutdown with both: `TestJoinShutdownWithAHeldBackRetryAndLeasesOfTwoTenants`
(retry queued, tenant 0 holding two transactions, tenant 1 a transaction and a statement; after
`Shutdown`: `Invariants` and `Quiesce` pass, admission `Check` passes, retry end `gone` 1, pending
0, fair in flight 0, no fair state, fair lease starts = ends = engine lease starts = ends).

**(e) Defaults, keys, tests that changed, generated documents.** The binary's default runs the
pool core behind the timeout rule with the fair dispatcher installed (limit `pool.connections`)
and the retry policy `off`; `retry.policy: pre-forward-once` runs both mechanisms:
`TestBinaryDefaultRunsTheFairDispatcherWithTheRetryPolicyOff`. Keys of both are validated in one
pass and the joined error names each: `TestRetryAndFairnessKeysAreValidatedTogether`
(`retry.policy`, `retry.bucket_cap`, `fairness.default_weight`, `queue.rule`). Neither chunk's key
constrains the other's. **One test datum of chunk 12 had to change, no test of chunks 6 to 9**:
the storm's pinned budget-exhaustion seeds (`RetryStormSpec().Exhausts`) moved from 1, 2, 3 to
1, 3, 5 (`6f9a0bb`; see "The one place the join moved a test" below). Every other package passed
unchanged after the merge at seed 1 (`internal/...`, `cmd/...`, `test/...`),
including chunk 12's engine tests, now also re-run behind the fair dispatcher under both product
rules by `TestJoinEveryRetryEngineTestHoldsBehindTheFairDispatcher` (26 tests × 2 rules; it first
asserts the rig runs the fair dispatcher), and chunk 12's scenarios `retry-storm` and
`retry-no-double-apply`, which now run behind the fair dispatcher with 4 and 2 tenants. The
generated documents: `docs/fault-matrix.md` and `docs/error-contract.md` are unchanged by the join
— **fairness adds and changes no row**: a request the dispatcher holds back stays in its tenant
queue and leaves by an exit chunk 8 and 12 already list (`53M01` rule, overflow or deadline,
`53M03` breaker, `57014`, gone); it makes no new error and no new way for a statement to be lost.
`docs/capacity-table.md` has both chunks' rows; `docs/csv-schema.md` both chunks' metrics;
`docs/timeout-budget.md` is chunk 12's (chunk 9 adds no timeout).

**(f) The round-robin turn of a retried tenant.** A turn is spent when a pop is served, and a
granted retry is pushed again (`enqueue` → `Queue.Push`), so it is a new entry at the tail of its
tenant's queue and needs a new turn: the turn its first attempt spent is not refunded and the
retry does not keep it. The tenant therefore **cannot gain** service through retries (one pop per
turn, whether a first attempt or a retry) and **pays one extra turn per retried request** (at most
one retry per request, within the budget); other tenants keep their turns. Within the tenant the
retry waits behind the tenant's requests already queued (with adaptive LIFO on, a standing tenant
queue serves its newest entry first, so the retry may go first within its own tenant; that does
not change the order across tenants). Chunk 9's per-tenant `served` and chunk 8's `pushes` count
a retried request twice (two pops), and conservation (`pushes = served + shed + removed`) still
holds. Held by `TestJoinARetryTakesANewTurnBehindItsTenantsQueueAndNoTenantGainsService` (one
connection, tenant 0's write lent, then tenant 1, tenant 0, tenant 1 queued: without the refusal
the order is `SELECT 11` (t1), `SELECT 12` (t0), `SELECT 13` (t1); with the zero-byte refusal it is
the same three and then the retried write).

### The one place the join moved a test

(Superseded by "Join repair round": the pin below is gone and the storm exhausts by construction.)

`retry-storm/on` requires a budget refusal on pinned seeds (Deviation 1: exhaustion was never by
construction). Behind the fair dispatcher the storm's four tenants are served round robin instead
of oldest first, which changes the schedule: on seed 2 the policy granted 12 retries and refused
none for budget (`Refused[budget] 0`, level back at the one-token cap), so the 5-seed sweep failed
`checkBudget` ("seed 2: the budget was never exhausted"). Cause shown in a scratch copy: with the
dispatcher not installed, seeds 1–8 all exhaust (seed 2: 1 refusal); with it, over seeds 1–30, 27
exhaust (1–4 refusals each) and seeds 2, 4 and 8 do not. Nothing about the budget changed (the
bound checks, `budget.sql` and the engine's exhaustion tests pass); only which seeds happen to
reach two eligible losses within ten first attempts. Decision: keep three required seeds and pin
them to 1, 3, 5 (all in the unit run's seed 1 and the gate's 10-seed sweep), rather than make the
claim weaker or rebuild the scenario for exhaustion by construction. Deviation 1 and stated limit
8 are updated. A later change to the schedule can move these seeds again; the probe is a
throw-away loop over `RunRetry(RetryStormSpec(), ArmOn, seed, "", "")` printing
`Policy.Refused[retry.RefusedBudget]`.

### Join tests

| Test | Package | What it holds |
| --- | --- | --- |
| `TestJoinARetryHeldBackAtItsShareOpensNoConnection` | `internal/proxy` | (a), (c) |
| `TestJoinARetriedTenantAtItsShareLetsTheTenantBelowItsShareGoFirst` | `internal/proxy` | brief item 4 first case: the other tenant (below its share) is served first, the retry then completes; counters of both agree |
| `TestJoinARetryHeldBackAtItsShareIsLentWhenItsOwnLeaseEnds` | `internal/proxy` | brief item 4 second case; lease started once |
| `TestJoinARetryHeldBackByTheDispatcherEndsAtTheRequestDeadline` | `internal/proxy` | (b) deadline |
| `TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseEndsWhileARetryIsHeldBack` | `internal/proxy` | (b) breaker, brief item 4 third case (breaker seam) |
| `TestJoinThePoolBreakerOpeningFlushesARetryHeldBackByTheDispatcher` | `internal/proxy` | (b) breaker, real pool core |
| `TestJoinShutdownWithAHeldBackRetryAndLeasesOfTwoTenants` | `internal/proxy` | (d), brief item 4 fourth case |
| `TestJoinARetryTakesANewTurnBehindItsTenantsQueueAndNoTenantGainsService` | `internal/proxy` | (f) |
| `TestJoinEveryRetryEngineTestHoldsBehindTheFairDispatcher` | `internal/proxy` | chunk 12's engine tests behind the fair dispatcher, rules `timeout` and `control-law` |
| `TestQuiesceRefusesALeaseStillInFlightAndPassesOnceItEnds` | `internal/admission` | (d) fairness block of `Quiesce` |
| `TestRetryAndFairnessKeysAreValidatedTogether`, `TestBinaryDefaultRunsTheFairDispatcherWithTheRetryPolicyOff` | `internal/config` | (e) |
| `TestScenarioRetryStormTwoTenantsOfUnequalWeight` | `test/e2e` | brief item 4 fifth case: the storm (`on` arm, one-token budget) with two tenants of weights 3 and 1; `CheckProduct` (no double apply, budget, fast refusals, recovery), `fairnessCheck` and `retryCheck` after every step, fair lease starts and ends equal the engine's, nothing in flight at the end, at least one completed retry, both tenants served, the share held a request back at least once (16–18 times on seeds 1–3), replay law. Seeds 1–3 (at most 3 per call), no flushed files (so G7 has no new scenario directory), about 5 s |

The join's mutations (scratch copy `/tmp/zgw-c12/mut`, from `git archive HEAD`, deleted after):

| Mutation | Killed by |
| --- | --- |
| J1 the fairness block removed from `Invariants` | `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions` |
| J2 the retry block removed from `Invariants` | `TestInvariantsCatchABrokenBudget`, `...APendingRetryNoRequestHolds`, `...TheEngineAndThePolicyDisagreeing` |
| J3 `leaseLost` does not report the lease end | all eight `TestJoinA…`/`TestJoinThe…`/`TestJoinShutdownWithAHeld…` tests |
| J4 the breaker re-check removed from `dispatch` | `TestJoinTheBreakerReCheckRunsWhenAnotherTenantsLeaseEndsWhileARetryIsHeldBack` |
| J5 the pool's demand is every queued request (`Queue.Len()`) | `TestJoinARetryHeldBackAtItsShareOpensNoConnection` and three more join tests |
| J6 the retry's queue-wait clamp removed | `TestJoinARetryHeldBackByTheDispatcherEndsAtTheRequestDeadline` |
| J7 the pending-retry block removed from `Quiesce` | `TestQuiesceRefusesAPendingRetry` |
| J8 `fairQuiesce` removed from `Queues.Quiesce` | `TestQuiesceRefusesALeaseStillInFlightAndPassesOnceItEnds` (nothing failed before it was added) |

### Results

All on 2026-11-28 at `6f9a0bb` (code as committed; NOTES only after it), load average 5–9 on 4
cores (other sessions' runs beside these):

| Check | Result |
| --- | --- |
| `go build ./... && go vet ./...`, `gofmt -l` | pass, nothing listed |
| `go test -count=1 -p 2 ./internal/... ./scripts/ ./cmd/...` | all packages pass (`internal/obs/simrun` 386 s) |
| `go test -count=1 ./test/...` (seed 1) | pass (`test/e2e` 364 s), before the weighted scenario and the re-pin; both run since |
| `./agent_work/adversarial/c09-fairness/...`, `./agent_work/adversarial/c12-retry/...` | both pass unchanged (no file under `agent_work/adversarial/` edited) |
| `scripts/gen-docs.sh --check` | pass (all four generators current) |
| `agent_work/chunks/c09-fairness/gate/extra.sh`, `agent_work/chunks/c12-retry/gate/extra.sh` | both pass (planted fuzz bugs found, canaries green) |
| 5-seed sweep, `DST_SEEDS=5 RUN_DIR=… go test -run '^(TestScenarioFair\|TestScenarioRetry)' ./test/e2e/` | every fairness scenario, `retry-no-double-apply` and the weighted storm pass; `retry-storm/on` seed 2 failed (above); after `6f9a0bb` `retry-storm` all arms seeds 1–5 pass (15 runs) |
| `scripts/csv-assert.sh` over the sweep's flushed files | 0 failures: `fair-held-transactions`, `fair-many-tenants`, `fair-noisy-neighbour`, `fair-results`, `retry-no-double-apply` (15 runs), `retry-storm` (15 runs, after the re-pin) |
| `GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh c12-retry` | at `20f4b47`, clean tree: **overall pass** (`counts_for_landing` false by `GATE_ONLY`): G0 all, G1 detlint (30 patterns), importlint, boundlint, paniccheck, G2 comments (baseline `d5dc685`), G3 unit (952 s, every package including both adversarial packages), G3 counted, G3 race (93 s). The rewritten evidence logs under `agent_work/adversarial/` were restored with `git checkout`; `gate/result.json` and the untracked `gate/` logs removed |

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
| 9 | Exhaustion visible; decisions counted by reason; conservation on every exit path incl. `Shutdown`; one reply per retried request | `TestRetryBudgetExhaustionIsVisibleAndCounted`, `TestEveryRefusalReasonIsReachedAndCounted`, `TestRetryShutdownWithAPendingRetryEndsItAndConserves`, `TestRetryClientGoneWhileTheRetryIsQueued`, `TestRetryQueuedAndCancelledByTheClientEndsFailed`, `TestRetryQueuedWhenTheBreakerFlushesEndsShed`, `TestRetryInFlightWhenTheClientCloses`; exhaustion in tier S: `retry-storm/on` (one-token budget) must refuse for budget on seeds 1, 3, 5 (`checkBudget`; seeds 1–3 before the join with chunk 9, see there), `conservation.sql` (`refused budget = exhaustions`, now non-zero); `Snapshot.Check` (`retryConserved`) run by every engine step's invariants, SQL `conservation.sql`; `internal/obs/retrysrc` `TestRetryCountersMoveAndConserve` |
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
- **A budget refusal in the storm's `on` arm** (since the join repair round): the second doom
  burst (800 ms) follows a 20 ms spend phase in which each client sends one operation, so at most
  seven deposits follow the grant that spends the one token, and the first lend at 850 ms is
  refused for budget at a level of at most 8 tenths ("Join repair round", Finding 2).
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

1. **Exhaustion in the scenarios was shown on pinned seeds, not by construction, until the join
   repair round; since then the storm exhausts by construction on every seed** (see "Join repair
   round", Finding 2; the history below is kept). Corrected
   reason (review minor 6): before the repair the scenarios retried 12–17 (storm) and 4–9 (sweep)
   times, but the `on` arm's floor of 4 tokens plus 450–835 first attempts (a tenth of a token
   each, 503–555 and 217–266 tenths lost at the 20-token cap) kept the bucket far above one
   token, so `retry.exhaustions` stayed 0. The doom pattern does produce several retries per run
   (every tenth faulted query dooms its connection); it is the budget that was too large, not the
   retries too few. Repair: the storm's `on` arm now runs a one-token budget (`Cap`/`Floor` 1,
   `stormCapTokens`), which costs no extra simulated work. With it the budget is refused 1–3 times
   on seeds 1–6 and on most of seeds 7–60, but on at least two of seeds 7–60 not at all (a probe,
   `CheckProduct` passed on all 54), so exhaustion is not guaranteed by construction. The run
   therefore requires a budget refusal only on pinned seeds (`RetrySpec.Exhausts`; 1, 2, 3 until the
   join with chunk 9, 1, 3, 5 since; see "Join with chunk 9"),
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
8. Budget exhaustion in tier S is required on every seed of the storm's `on` arm, by construction
   (two doom bursts around a one-operation spend phase; "Join repair round"). The construction
   needs fewer than ten clients (margin `10 - clients` tenths) and the relay's fault window kept
   out of the spend phase; `retry-no-double-apply` (20-token cap) does not exhaust and is not
   required to.
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
