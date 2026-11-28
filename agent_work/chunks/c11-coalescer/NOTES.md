# Chunk 11 — Coalescer: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c11-coalescer`, worktree
`/root/Software/mini-gateway-work/c11`. No helper agents.

No SPLIT-REQUEST: every deliverable of the brief is built (core, wiring, config, obs, tier S,
tier R, headline 2). Go added since the brief commit `6cffb0f`: about 2,880 lines in non-test
files (core 900, proxy wiring 560, `coalsrc` 170, config 90, the tier S scenario file
`test/e2e/c11_coalesce.go` 660 which is test plumbing in a non-test file, small edits) and about
2,150 lines in `_test.go` files. Inside the brief's estimate plus the 2,000-line split margin.

## Join with chunks 9 and 12

Join builder: Opus (`claude-opus-5-5`), 2026-11-28, branch `work/c11-coalescer`, worktree
`/root/Software/mini-gateway-work/c11`. `git merge --no-ff main` (main at `2e725b1`: chunk 9
landed `d5dc685`, chunk 12 landed `d547256`, each with its own join, prose and C4 work) on top of
`078b7b3` (review 2 NITS): one conflict resolution in merge commit `2862074`, then one product
fix (`2ed49d6`) and tests, scenarios and records (`f438247` to `5e6a99f`). No helper agents.

The three chunks compose in D1's stage order (classify, gate, coalesce, queue, lease, forward,
release) with D3's one waiting place: the coalesce stage decides lead, follow, hold, pass or
refuse before the queue; only a flight's owner (or an unshared request) enters its tenant queue;
chunk 9 chooses inside the queue stage; chunk 12 re-enters a granted retry at the queue stage.
**One defect was found and fixed** (decision (a): the lease end of a flight handed off to a
waiter of another tenant was reported for the wrong tenant). **No behaviour of any chunk was
dropped or weakened, and no two behaviours conflict.**

### Conflicts and how each was resolved

| File | Resolution |
| --- | --- |
| `internal/config/config.go` | `File` carries `Coalesce`, `Retry` and `Fairness`. |
| `internal/config/validate.go` | `proxyCaps` passes `Coalesce`, `KeyScope`, `Fairness`, `Retry`; `validate` runs `validateCoalesce` and `validateRetry` (fairness and its defaults auto-merged). |
| `internal/proxy/config.go` | `Config` carries `Coalesce`, `Retry`, `Fairness`; `Validate` appends `coalesceErrors()` and the retry config's error. |
| `internal/proxy/contracts.go` | Timeout rows: earlier rows, admission rows, coalescer rows, then chunk 12's retry row. Capacity rows: earlier rows, `retryCapacityRow`, chunk 9's fairness rows, then the coalescer rows. |
| `internal/proxy/engine.go` | `Invariants`: snapshot conservation, lease pairing, queue and wire checks, `admissionCheck`, `fairnessCheck`, `retryCheck`, `coalCheck`, the pool core. `Quiesce` and `Shutdown` auto-merged with every block of the three chunks (decision (e)). |
| `internal/proxy/lease.go` | `forward`: chunk 12's clamped statement timer, then `coalSent`. `statementLost`: the retry offer first; granted → `coalRetried` (drops anything stored, keeps the flight) and `rerun`; declined → `coalFailed` (shared error), then the client failure (decision (c)). |
| `internal/proxy/corepool.go` | `refuseWaiting(c, end)`: chunk 12's signature with chunk 11's `coalRefused` first, so every breaker refusal of a flight's owner (pool flush, or chunk 12's re-check of a queued retry) answers its waiters `53M03`. |
| `internal/proxy/client.go` | `request` carries chunk 11's flight fields and chunk 12's `attempt`, `retryOver`. |
| `internal/proxy/snapshot.go` | Both counter groups. |
| `internal/obs/catalog/catalog.go`, `internal/obs/enginesrc/enginesrc.go` | Both components declared (`coalescer`, `retry`); both metric groups; both sources. |
| `internal/obs/simrun/engine_test.go` | The coalescer baseline jobs and chunk 12's retry baseline jobs (outside `-short`); `shortSkipped` auto-merged with all three chunks' names. |
| `scripts/fuzz-plant.sh` | All three plants (`FuzzCoalesceOps`, `FuzzRetryOps`, `FuzzFairnessOps`) and their header paragraphs. |
| `test/e2e/spec.go`, `simtier.go`, `realtier.go` | `Spec` carries `Coalesce`, `KeyScope`, `Retry`; `engineConfig` passes all; tier R `GatewayOpts` carries `Coalesce` and `Retry`, with chunk 11's `on-switch` → `coalesce.mode off` default kept. |
| `docs/capacity-table.md`, `docs/timeout-budget.md`, `docs/csv-schema.md`, `docs/fault-matrix.md`, `docs/safe-shape.md` (generated) | Regenerated with `scripts/gen-docs.sh`, never hand-merged. |
| `docs/c4/`, `book/`, `README.md` | Main's side entirely (`git checkout main -- docs/c4 book README.md`). |
| `agent_work/INDEX.md` | Both sides' rows (auto-merged). |

`internal/proxy/fairness.go`, `retry.go`, `coalesce.go` and the rest of `internal/proxy`
auto-merged. Directly after the merge commit, before any join change, the packages of the
three chunks passed unchanged (`go test -short` over `internal/proxy/...`, `internal/config/...`,
`internal/coalesce/...`, `internal/admission/...`, `internal/retry/...`, `internal/obs/...`,
`cmd/...`: 26 packages).

### Where the chunks assumed different things, and what holds now

**(a) Fairness and flights.** A flight's owner is an ordinary request: it waits in its own
tenant's queue (one entry) and, when lent, holds one lease counted for that tenant. Followers
take no queue place and no lease; they are not pushed, so a tenant whose only requests are
followers is not active for the dispatcher. With key scope `all-tenants`, followers of other
tenants therefore ride the owner's tenant's lease for free: they spend none of their tenant's
share, and the owner's tenant pays one lease for everyone (one database read is one unit of
database concurrency, which is what the share divides). Held by
`TestJoinFollowersOfOtherTenantsRideAFlightWithNoQueuePlaceAndNoLease` (both join modes: tenant 0
at its share, its flight queued with followers of tenants 1 and 2: queued 1/0/0, in flight
2/1/0, releasable 0, no dial over five events; after the release in flight 3/0/0; every waiter
gets the bytes; tenant 2 pushed nothing).
*Owner handoff across tenants.* Chunk 11 assumed the client holding a request is the tenant to
charge; chunk 9 charged `c.tenant` at the lease start (`lend`) and at the clean lease ends
(`release`, `coreRelease`), `d.lastTenant` at the others (`leaseLost`, `coreDirty`). After a
handoff to a waiter of another tenant these disagree: a running handoff reported the lease end
for the new tenant (the fair core: "1 lease ends without a start"), a queued handoff popped from
the first owner's tenant queue but started the lease for the new one. **Fixed (`2ed49d6`):** a
request carries the tenant it was queued under (`request.tenant`, set at acceptance); queue push,
checkout and the lease start use it, and `lend` records it as `d.lastTenant`; every lease end uses
`d.lastTenant`. So the queue place and the lease stay on the tenant whose queue the flight left
(the first owner's) before and after the handoff, until the lease ends; no path counts the lease
twice or not at all. For any request that is not a handed-off flight `request.tenant ==
c.tenant`, so chunk 9's behaviour is unchanged. Held by
`TestJoinAQueuedOwnerHandoffAcrossTenantsUnderABindingShareKeepsTheChargeOnTheQueuedTenant`
(binding share, the owner of tenant 0 leaves, the waiter of tenant 2 takes over; it stays in
tenant 0's queue and, when lent, tenant 0 holds 3 and tenant 2 none),
`TestJoinARunningOwnerHandoffAcrossTenantsEndsTheLeaseOnTheTenantItWasLentTo` (pool core) and
`TestJoinARunningOwnerHandoffAcrossTenantsOnTheFixedSetEndsTheLeaseOnTheTenantItWasLentTo`
(fixed set). The on-switch reset still sees the real user (`ReleaseInput.Tenant` is the client's
tenant), and on-switch is refused with `all-tenants` while coalescing is on anyway.

**(b) A flight held back by the dispatcher.** The flight deadline (from the flight's creation)
is independent of why the owner waits: at the deadline `coalExpireOwner` removes the queued owner
with `Expire` (a deadline shed, so the core's and the engine's shed counts agree) and every
waiter gets `53M01` once. The dispatcher's `Releasable` and the pool's demand count the flight as
the one queue entry it is, exactly when the dispatcher would release it; followers add nothing.
Held by `TestJoinAFlightHeldBackByTheDispatcherEndsAtItsDeadlineWithOneSharedError` (still
queued one nanosecond before, three `53M01` at the deadline, one deadline shed in the admission
queue, nothing sent, no dial, even after the share frees) and by the first test of (a)
(releasable 0 and no dial while held; one send when released).

**(c) Retries and flights — decision: a flight whose statement is lost with nothing reached is
retried once, for the whole flight.** The plan's words decide it. Chunk 12: a retry is "once per
request, only when nothing reached, never past the request's deadline" — the flight is one
request on the database (its owner's), so it gets at most one retry, under the same budget,
breaker, deadline and in-transaction rules; followers are not requests on the database and are
never offered to the policy. Chunk 11: "shared errors ... no re-drive stampede" — the stampede it
forbids is each waiter re-driving its own read; one retry by the owner is one read, not a
stampede, and when the policy declines (or the retry is lost again) the error is still shared:
every waiter gets the owner's row once. The other reading (each follower gets the error, the
owner retries alone) would answer the followers with an error the proxy itself was about to
repair, and gives them nothing in exchange. So: `statementLost` offers the loss to the policy
before the shared error; granted → the owner re-enters its tenant queue, the waiters stay on the
flight and get the retry's outcome once; declined → shared error at once. The budget counts the
flight's first attempt once (the owner is forwarded; followers never are). The fault matrix's
retried row says it (generated: "for the statement of a coalesced flight the one retry is the
flight's, and every waiter on it gets the retry's outcome once"), with a new non-retried row for
a flight not yet sent at its flight deadline. Held, in both join modes, by
`TestJoinALeaderRefusedWithNothingReachedIsRetriedOnceForTheWholeFlight` (zero-byte refusal,
nobody answered, offered 1 granted 1, two waiters still on the flight; the retry is sent once and
everyone gets its bytes; the core counts one flight sent; the policy's first attempts equal the
engine's forwards minus the one retry forward, and the COMMIT plus the flight added exactly 2),
`TestJoinARetriedFlightLostAgainIsAnsweredOnceWithOneSharedError` (the retry is refused again:
offered 2, granted 1, three identical replies with one end marker each, retry end `failed`),
`TestJoinWithTheRetryPolicyOffALeaderRefusedSharesItsErrorAtOnce`, and the tier S scenarios
`coalesce-hot-key-retry-strict` / `-relaxed` (below).

**(d) A granted retry of a flight's owner re-enters the queue.** It ends at the earlier of the
flight deadline and the request deadline: the flight deadline through `coalExpireOwner`
(`Expire`), the request deadline through chunk 12's clamp on the queue timer (`queueExpired` →
`shed` → `coalRefused`); each answers every waiter `53M01` once, retry end `shed`. The breaker
re-check at the top of every `dispatch` pass refuses it through `refuseWaiting` →
`coalRefused`: every waiter `53M03` once, retry end `breaker`, nothing sent. Followers are
answered exactly once on every exit (reply, shared error, deadline, request deadline, breaker,
shutdown). Held by `TestJoinARetriedFlightOwnerHeldInTheQueueEndsAtTheEarlierOfTheFlightAndRequestDeadlines`
(two arms: `coalesce.Config.Deadline` half the statement timeout, so the flight deadline comes
first, and twice it, so the request deadline comes first; still waiting one nanosecond before,
three `53M01` at it, one deadline shed, nothing sent after),
`TestJoinTheBreakerReCheckRunsForARetriedFlightOwnerAtTheLendThatWouldTakeIt` (closed: the retry
is lent the connection the other tenant frees and everyone gets the bytes; open: three `53M03`,
retry end `breaker`, nothing sent).

**(e) `Invariants`, `Quiesce`, `Shutdown`.** Every check of all three chunks runs: each block
returns early only when it fails, so on a passing engine all run. A test fails when any block is
removed (mutations in a scratch copy, table below). `Shutdown` still sends no statement to the
database (review 1 finding 2) with a retry pending and flights held:
`TestJoinShutdownWithARetryPendingAndFlightsHeldSendsNothingNewToTheDatabase` (a retried owner
waiting at its share with two waiters, a third read held behind it in strict mode or following
it in relaxed, open transactions of two tenants: the only database writes are the pool core's
`ROLLBACK`s of the open transactions; retry end `gone`, fair in flight 0 with starts = ends =
the engine's, `Invariants`, the admission `Check` and `Quiesce` pass). Chunk 11's
`TestShutdownSendsNothingNewToTheDatabase` still holds the `coalStart` guard. `coalShutdown`
had no test that failed without it (closing every client empties the table first), so the join
adds `TestJoinShutdownEmptiesTheFlightTableEvenOfAFlightNoClientHolds` (a flight stranded in the
table with no client, `StrandFlightForTest`: `Quiesce` fails before `Shutdown`, passes after).

**(f) Defaults, keys, tests that changed.** The binary's default: `coalesce.mode auto`,
`join strict`, `key_scope all-tenants`; the fair dispatcher on (limit `pool.connections`); the
retry policy `off`; the pool core behind the timeout rule. `retry.policy: pre-forward-once`
with `coalesce.join: relaxed` runs all three:
`TestBinaryDefaultRunsTheCoalescerTheFairDispatcherAndTheRetryPolicyOff`. Keys of all three
are validated in one pass and the joined error names each:
`TestCoalesceRetryAndFairnessKeysAreValidatedTogether` (`coalesce.mode`,
`coalesce.max_waiters`, `retry.policy`, `fairness.default_weight`, `queue.rule`). The `on-switch`
refusal applies only when coalescing is on (`auto`, `hinted`), with the retry and fairness keys
set or not: `TestTheOnSwitchRefusalAppliesOnlyWhenCoalescingIsOnWhateverTheRetryAndFairnessKeys`.
**No test of chunks 6 to 12 had to change because of another chunk**: every package passed
unchanged after the merge. Chunk 11's own scenario `coalesce-results-*` changed for review 2
finding 2 (below), not because of another chunk.

### Review 2's two findings

- **Finding 1 (no test under the binary's default admission rule) — closed by a test.**
  `TestJoinUnderTheDefaultAdmissionRuleRuleAndFullQueueShedsEndFlightsWithFollowers` (strict and
  relaxed; the pool core with one connection; `Admission{Rule: timeout, LIFO: adaptive}` and
  `QueueCap` default, checked equal to the defaults: target 5 ms, interval 100 ms, capacity 256,
  tenant capacity 256; the engine's `Invariants` after every event through the rig, the admission
  `Check` and engine sheds = admission sheds after every phase). Phases: a queued flight with
  followers of two other tenants waits past the interval → rule shed, all three `53M01`; the same
  after the owner leaves (handoff) → rule shed, both remaining `53M01`; a flight with a follower
  is the oldest entry when the queue fills → full-queue shed (`overflow` or `total`: with the
  default tenant capacity equal to the capacity both bounds are reached together), both
  `53M01`; strict only: a running read, a read held behind it with a follower, a second flight
  with a follower as the oldest entry of a full queue; the reply promotes the held flight, whose
  push sheds the second flight's owner inside the notice loop (`noticeBusy`): its waiters get
  `53M01`, the promoted flight's waiters are not answered; then drain and `Quiesce`. Rule sheds
  are 2 by construction. `TestJoinAFlightDeadlineAndAnAdmissionDeadlineAtTheSameInstantAnswerEachWaiterOnce`
  covers the reviewer's fourth probe (flight deadline = interval: one shed, each waiter answered
  once). The reviewer's 200-client probe is covered in tier S by the two join hot-key scenarios'
  burst of 200 readers (below), behind the timeout rule and the pool core.
- **Finding 2 (`coalesce-results-*` without the admission queues) — closed by wiring
  (`0a5b61b`).** `CoalesceResultsSpec` now takes `poolTimeouts()`, `QueueCap` 1,024,
  `coalAdmission()` and `withCore`, like `coalBase`; `coalAdmissionOK` therefore applies. Both
  scenarios pass on seeds 1–3 with replay. The sentence "every tier S coalescer scenario runs
  with the admission queues and the pool core" is now true for all 27.

### Join scenarios (tier S, `test/e2e/c11_join.go`, appended to `CoalesceScenarios()`)

`Spec` gains `Fairness` (tenant weights) and `Relay` (chunk 12's dooming relay in front of the
fake; `Path` must be nil since both use `BeforeProxy`). Each scenario runs in both join modes
with replay and an ablation that must fail its check; each has `coalescer.sql` under
`test/analysis/`.

| Scenario | What it does | Check (by construction) | Ablation |
| --- | --- | --- | --- |
| `coalesce-hot-key-weighted-{strict,relaxed}` | 2 tenants, weights 3 and 1; 16 writers (8 per tenant, 30 writes of 20 ms each) keep 4 connections busy so the shares bind; 200 readers of both tenants read the hot key at 100 ms, 3 µs apart | 1 or 2 database reads, never two at once; every reader gets the value; both tenants have shared hits; the fair dispatcher held a tenant at its share; fair lease starts = engine lease starts; coalescer and admission conservation | coalescer off (200 reads) |
| `coalesce-hot-key-retry-{strict,relaxed}` | retry policy on; 4 warm-up writes open 4 connections; the relay dooms every idle connection at 500 ms; 200 readers at 600 ms | the first write of the burst is the leader's and every connection is doomed, so it is refused with zero bytes: dooms > 0, a retry granted with row `lost-after-read` and ended `completed`; every reader gets the value, no shared error, 1 or 2 reads; the policy's first attempts = 4 warm-ups + flights sent (one per flight, not per follower) | retry policy off (every waiter gets the shared error) |

Seeds 1–10 of both, both modes: pass with replay and ablation (102 s).

### Join tests and the mutations that fail them

Scratch copy `/tmp/zgw-c11-join/mut` (deleted after), one source edit at a time,
`go test ./internal/proxy/ ./internal/admission/`:

| Mutation | Killed by |
| --- | --- |
| M1 `release` (fixed set) ends the lease on `c.tenant` | `TestJoinARunningOwnerHandoffAcrossTenantsOnTheFixedSetEndsTheLeaseOnTheTenantItWasLentTo` |
| M1b `coreRelease` ends the lease on `c.tenant` | `TestJoinAQueuedOwnerHandoff…`, `TestJoinARunningOwnerHandoffAcrossTenantsEndsTheLeaseOnTheTenantItWasLentTo` |
| M2 `lend` charges `c.tenant` | `TestJoinAQueuedOwnerHandoffAcrossTenantsUnderABindingShareKeepsTheChargeOnTheQueuedTenant` |
| M3 shared error before the retry offer (chunk 11's old order) | the five retried-flight tests of (c), (d), (e) |
| M4 a flight is never offered to the policy | the same five and `TestJoinWithTheRetryPolicyOffALeaderRefusedSharesItsErrorAtOnce` |
| M5 `refuseWaiting` without `coalRefused` | `TestJoinTheBreakerReCheckRunsForARetriedFlightOwnerAtTheLendThatWouldTakeIt` |
| M6 `shed` without `coalRefused` | both default-rule tests (finding 1), the retried-flight deadline test |
| M7 queued flight at its deadline removed as client-gone (review 1's M-R1) | `TestAQueuedFlightAtItsDeadlineIsShedThroughTheAdmissionQueue`, `TestJoinAFlightHeldBackByTheDispatcherEndsAtItsDeadlineWithOneSharedError`, the retried-flight deadline test |
| M8 `Invariants` without `coalCheck` | `TestInvariantsCoverFlightsAndWaiters` |
| M9 `Invariants` without `fairnessCheck` | `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions` |
| M10 `Invariants` without `retryCheck` | `TestInvariantsCatchABrokenBudget`, `…APendingRetryNoRequestHolds`, `…TheEngineAndThePolicyDisagreeing` |
| M11 `Invariants` without `admissionCheck` | `TestJoinInvariantsRunTheAdmissionChecksOnAnEngineWithThePoolCore` |
| M12 `Quiesce` without `coalQuiet` | `TestEngineQuiescesWithAnEmptyFlightTableAndNoTimer` |
| M13 `Quiesce` without the pending-retry check | `TestQuiesceRefusesAPendingRetry` |
| M14 `Quiesce` without `admissionQuiet` (chunk 9's `fairQuiesce` inside) | `TestJoinQuiesceWaitsForTheQuietTenantQueueThenPasses` |
| M15 `Shutdown` without `coalShutdown` | `TestJoinShutdownEmptiesTheFlightTableEvenOfAFlightNoClientHolds` (added: nothing failed before) |
| M16 `Shutdown` without `closeAdmission` | seven tests, among them `TestJoinShutdownWithARetryPendingAndFlightsHeldSendsNothingNewToTheDatabase` |
| M17 `coalStart` promotes during shutdown (review 1's M-R2) | `TestShutdownSendsNothingNewToTheDatabase` |
| M18 followers count as first attempts | `TestJoinALeaderRefusedWithNothingReachedIsRetriedOnceForTheWholeFlight` (added the forwards check: nothing failed before); tier S `coalesce-hot-key-retry-*` checks the same |

The first run left M1, M15 and M18 alive; the three tests named "added" were written for them
(`5e6a99f`) and the three re-run mutations were killed.

### Results

RESULTS-PENDING

### Stated limits added by the join

- A handed-off flight stays charged to the tenant whose queue it left until its lease ends; the
  new owner's tenant pays nothing for it. With key scope `all-tenants` a tenant can therefore be
  served through another tenant's lease (as every follower is); the fair dispatcher bounds leases,
  not answers.
- While a flight's retry waits, the coalescer still treats the flight as running, so in strict
  mode a read arriving then is held behind it (a second read later), never joined to the retry.
  Conservative: no waiter gets a read started before it arrived.

## Repair round (2026-11-28, Opus, after Neon review 1 and Astra's attacks)

| Item | Outcome |
| --- | --- |
| Review 1 (MAJOR): queued flight at its deadline shed as client-gone | Fixed `a54f609`: `coalExpireOwner` removes the queued owner with `Expire` (counted as a shed) like `queueExpired`. Regression test `internal/proxy` `TestAQueuedFlightAtItsDeadlineIsShedThroughTheAdmissionQueue` (failed before: `admission: the core shed 0, the engine answered 1`). Root cause addressed in `2c48541` (simrun: `d85d44c`): every tier S coalescer scenario but `coalesce-results-*` now runs with the admission queues (timeout rule) and the pool core beside the coalescer (those two since the join, review 2 finding 2), the engine's invariants checked at every step, plus `coalAdmissionOK` (pushes > 0, core sheds = engine sheds, queue empty). With the fix reverted in a scratch copy the scenarios fail: `coalesce-shared-error-strict` seed 2, `sim: invariant proxy.engine: admission: the core shed 0, the engine answered 1` |
| Review 1 (MAJOR): Shutdown promotes a held flight and writes to the database | Fixed `d889df4`: `coalStart` does nothing once shutdown has begun; the held owner stays a waiter and is closed like any other. Regression test `TestShutdownSendsNothingNewToTheDatabase` (no `ActDBWrite` during `Shutdown`; conservation, `Quiesce` and `Invariants` green; failed before with the write on the second connection) |
| Review 1 (MINOR 3): bounds unchecked under `off`; reply cap not written back | Fixed `60c82f1`: under `off` the same bounds are checked (naming the key) as under `auto`; `applyDefaults` writes the resolved `max_reply_bytes` back. Tests `TestCoalesceBoundsAreCheckedWhenModeIsOff`, `TestOmittedReplyCapResolvesToTheSameConfigAsTheDefault` (both failed before) |
| Review 1 (MINOR 4): the trust limit and the default not in the documents | Fixed `1d93564` (generator of `docs/safe-shape.md`: section "Limits"), `c9a68be` (`coalesce` section in `internal/config/testdata/example.json`), `1416c92` (`docs/knowledge/coalescer.md`) |
| Review 1 (MINOR 5): heavy seeds, `reads.sql` placement, label, timer check, det list, count, D5 limit | Fixed: `coalHeavySeeds` = 3 and `coalesce-hot-key-strict` with its ablation arm as a baseline run in `internal/obs/simrun` outside `-short` (its two-arm `reads.sql` now runs in G3 of every gate), label corrected (`2c48541` (simrun: `d85d44c`)); `coalCheck` checks the timer is armed at `Next()`, test case "timer" in `TestInvariantsCoverFlightsAndWaiters` (failed before) (`9bff846`); `internal/obs/coalsrc` on `det-packages.txt` (`e06603a`); scenario count 23 and stated limit 7 (vanished clients on real sockets) in this file |
| Attack `TestAdv_3_TenantDelimiterCollision` | Fixed `a5c70a4`: the flight key is an 8-byte tenant length, the tenant, then the text. Tests `TestTenantAndTextWithAZeroByteStartTwoFlights`, the property test `TestDifferentShareKeysNeverMapToOneFlightKey` (20,000 random pairs over bytes including 0x00 and 0xff; failed before on `("\x00\x00", "")` against `("", "\x00\x00")`) and the fuzz target `FuzzShareKeyIsUnambiguous` over arbitrary strings (also checks the hot-key decoder round-trips). The attack passes |
| Attack `TestAdv_8_DeadlineOverflow` | Fixed `fd2a3bc`: flight deadlines and no-share expiries saturate at the largest instant. Tests `TestADeadlineNearTheLargestInstantSaturates` (failed before: `next -9223372035854775909`), `TestSaturatingAdd`. The attack passes |
| Attacks `TestAdv_1_CancelAcknowledgementGap`, `TestAdv_1_NoShareBypassOverlap`, `TestAdv_1_FullTableDuplicateBypass` | Narrowed (N1 under "Narrowed claims"), tests `ef6bb8c` |
| Attack `TestAdv_6_ResultBeforeCancelTie` | Narrowed (N2 under "Narrowed claims"), test `ef6bb8c` |

What the admission arm showed besides finding 1: with the scenarios' queues on the binary's
default rule settings (timeout rule, target 5 ms, interval 100 ms; only the capacities raised) the
coalescer scenarios `coalesce-writes-*`, `coalesce-caps-*` and `coalesce-hinted` fail their checks
because the rule sheds (`53M01`) requests that their bursts keep queued for longer than the 5 ms
target on 4 connections; no engine invariant or conservation check failed in any scenario. That
is load shedding working as configured, not a coalescer defect, so the scenarios' base sizes the
rule for the arm that must show 1,000 database reads (target 15 s, interval 20 s, tenant capacity
1,024; brief note 9). Under that base all 23 scenarios pass on seeds 1–3 (the 1,000-client ones
with `MG_C11_HEAVY=1`, same numbers as before: strict 2 reads p99 101.7 ms, relaxed 1 read p99
51.5 ms, off 1,000 reads, direct p99 50.3 ms) and `reads.sql` passes over both hot-key
directories.

## Simulator change requests

None. Nothing under `internal/sim` changed. The scenarios use the fake's `Service.PerKind`
(time on `KindRead` and `KindUpdate` only, so `DISCARD ALL` stays free) and its statement and
write logs.

## What was built

| Path | What it is |
| --- | --- |
| `internal/coalesce` | The core, sans-I/O, determinism-linted, on the race list: the flight table (keys → at most a current flight and one held flight), leader/followers, strict and relaxed joins, a flight deadline per flight (creation order list, O(1) next wake-up), waiter reference counting with handoff, leaver removal, the waiter cap (`53M04`), the reply cap with a fixed-size expiring no-share list, the unshareable-end detach, shared errors, per-tenant hits (bounded, `other` past 4,096), opt-in per-key hotness, `Snapshot`/`Check`/`Quiesce`, six plants. Imports `internal/work` and `internal/obs` only. |
| `internal/proxy/coalesce.go` (new) | `FlightTable` (the grown seam: `Coalescer` plus `Admit`, `Started`, `Stored`, `Finish`, `Depart`, `TakeNotice`, …), `CoreCoalescer` (backed by the core), the coalesce stage (`coalJoin`), the answer path (stored reply + own end marker; shared error rows; 53M04; 57014 for a waiter's own cancel), handoff of a queue slot or a lease to a waiter, the flight-deadline timer (`tCoalesce`), `Invariants`/`Quiesce`/`Shutdown` blocks, contract-document rows. |
| `internal/proxy/{engine,client,lease,corepool,config,snapshot,timers,contracts}.go` | One-line hooks (listed under "Shared files"); `Snapshot` gains `Coalesced` and the `Coalesce*` counters; conservation gains the `coalesced` term. |
| `internal/config/coalesce.go` (+ one line each in `config.go`, `validate.go`) | `coalesce.mode` (`off`/`auto`/`hinted`, default `auto`), `coalesce.key_scope` (`all-tenants`/`tenant`), `coalesce.join` (`strict` default/`relaxed`), `max_flights`, `max_waiters`, `max_reply_bytes`, `no_share_for`, `no_share_max`, `hot_keys`; zero = default, negative refused naming the key; the `on-switch` × `all-tenants` refusal. |
| `internal/obs/coalsrc` | Component `coalescer`: every snapshot field, `tenant_hits` and the histogram with `obs.detail`, `hot_key_hits{mode=rank}`, health rule `coalescer` (always `ok`, states the waiting count). Registered in `enginesrc.Sources` and `catalog.Registry`; the engine's new fields mapped in `enginesrc`. |
| `test/e2e/c11_coalesce.go`, `c11_coalesce_test.go`, `c11_join.go` | 27 tier S scenarios (`CoalesceScenarios()`; 23 of the build, 21 light and 2 with 1,000 clients, plus the 4 join scenarios of `c11_join.go`), replay, ablation arms, the hot-key numbers test. Since the repair round every scenario runs with the admission queues (timeout rule) and the pool core beside the coalescer; `coalesce-results-*` since the join (review 2 finding 2). |
| `test/e2e/c11_tierr_test.go` | Tier R: hot key (strict, relaxed, off, direct), cancel 99 of 100, all 100 cancel, read-your-own-write, results equal direct with `auto`, headline 2 (Zipf). |
| `test/analysis/coalesce-*` | DuckDB assertions per tier S scenario (conservation per scenario; the hot-key two-arm join). |
| `scripts/fuzz-plant.sh` | Also requires `FuzzCoalesceOps` to find `PlantLeaverGetsResult`. |
| `agent_work/chunks/c11-coalescer/gate/extra.sh` | G10: fuzz plants, core tests, engine tests, the 1,000-client scenarios and the numbers test. |
| `docs/knowledge/coalescer.md` | The knowledge note. |
| Generated: `docs/timeout-budget.md`, `docs/capacity-table.md`, `docs/csv-schema.md` | Regenerated with `scripts/gen-docs.sh`. |

## Open points decided

1. **Default of `coalesce`** (orchestrator's ruling): the binary runs `coalesce.mode: auto`,
   `join: strict`, `key_scope: all-tenants`. In the engine a zero `proxy.Config.Coalesce` is
   `off` (`NoCoalesce`), so every earlier tier S spec is unchanged; tier S specs opt in with
   `Spec.Coalesce`.
2. **How the seam grows.** `Coalescer` stays (`NoCoalesce` compiles and is the zero default).
   `FlightTable` embeds it and adds the verdicts (`Pass`, `Lead`, `Follow`, `Hold`, `Refuse`),
   `Started`/`Stored`/`Finish`/`Depart` and a notice queue (`TakeNotice`) through which the core
   tells the engine to answer waiters, start a held flight or fail an expired one. The engine
   uses the rich path when `Seams.Coalescer` implements `FlightTable`
   (`CoreCoalescer` does; `Config.Coalesce.Mode != off` builds one).
3. **The leader does not stream.** The owner's reply is stored (`req.store`) up to
   `coalesce.max_reply_bytes`; at `ReadyForQuery` the owner gets the stored bytes and the
   database's end marker, every follower the same stored slice (one copy) and its own
   `ReadyForQuery 'I'`. Nothing pauses the database read for a slow client while storing, and the
   cap is at most half of `limits.reply_buffer` (validated), so the relay high-water bound holds.
   **Past the cap mid-flight**: the followers get `53M04` (nothing reached the database for
   them), the key goes on the no-share list, and the owner gets the stored bytes and then the rest
   streamed with ordinary back pressure. A reply ending with a status other than `I` (only
   possible under `hinted`, e.g. a marked `BEGIN`) is treated the same way (`unshareable`).
4. **Charging.** The flight's queue slot is pushed under the tenant of the request that started
   the flight; after a handoff the admission core keeps that tenant for the slot. The lease is
   checked out (and tagged) for whichever client owns the flight when it is lent. In tenant
   scope all waiters share the tenant, so both are that tenant.
5. **A waiter's own cancel** is answered `57014` with the existing row `cancelled-queued`
   (nothing of its own reached the database; it is pre-forward for that caller). If the owner
   cancels and others wait, the flight passes to a waiter and the owner gets `57014`. If the
   last waiter cancels, the cancel is forwarded to Postgres exactly as without coalescing.
6. **Shed or breaker refusal of a flight's queued request**: every waiter gets the same row
   (`53M01` / `53M03`), counted `failed` with reached `no` for followers (shared error).
7. **No separate wait bound for a held (strict next) flight**: its own flight deadline, from its
   creation, bounds it (it may expire before the running read ends; then all its waiters get
   `53M01`, never reached).
8. **The `on-switch` refusal applies only with coalescing on** (orchestrator's ruling):
   `mode: off` + `on-switch` + `all-tenants` is valid; `key_scope: tenant` + `on-switch` is valid.
9. **Defaults**: `max_flights` 1,024; `max_waiters` 1,024 (owner included); `max_reply_bytes`
   64 KiB (at most half of `limits.reply_buffer`); `no_share_for` 10 s; `no_share_max` 256;
   `hot_keys` 0 (off, at most 64). The flight deadline is `timeouts.statement`.
10. **Health rule**: `coalescer` is always `ok` and names how many requests wait; a cap refusal
    is per request (53M04) and passing unshared keeps serving, so the coalescer never degrades
    the instance.
11. **Tier R "cancel 99 of 100" made count-exact**: 100 clients are connected first, then their
    queries are released together; the read lasts 2 s (a view over `pg_sleep(2)`); the 99
    cancels go at +300 ms. `pg_stat_statements` counts only completed statements, so the verdict
    is "exactly one completed read" in both modes (strict: the first flight, if only cancelled
    waiters were on it, is cancelled at Postgres and not counted). Arrival spread (≪ 2 s) and
    cancel time (300 ms ≪ 2 s) make this hold by margin, not by timing luck.

## Further decisions

- **Followers are neither queued nor in flight**: a new phase `cpCoalesced` and the gauge
  `Snapshot.Coalesced`; conservation is accepted = completed + failed + shed + queued + in
  flight + coalesced.
- **A `53M04` at the waiter cap is a rejection before acceptance** (`requests_rejected`,
  `stage_refused{coalesce}`); a `53M04` mid-flight (reply cap) is a failure with reached `no`.
- **Flight deadline outcome**: if the flight never reached the database, every waiter gets
  `53M01` (reached no); if it is running, the engine runs the statement-deadline path for the
  owner (dirty-connection table on the core path) and every waiter gets the owner's row
  (`08006` for the safe shape, `08007` otherwise).
- **The flight-table cap passes unshared** (overflow `flights`) rather than refusing.
- **Handoff picks the newest remaining follower** (O(1) from the list's end; the reference model
  follows the same rule).
- **Hot keys** are exported as `hot_key_hits{mode=<rank>}` counts only; statement text never
  leaves the snapshot (label policy 7.4).

## Claims and the tests that prove them

| # | Claim | Tests |
| --- | --- | --- |
| 1 | At most one in-flight read per key | `internal/coalesce` `TestFlightTableAgreesWithReferenceModel` (after every op `Running(key) ≤ 1`); tier S `checkHotKey` (`MaxConcurrent` of the fake's statement log = 1); `Table.Check` (held flights never running). Narrowed: see "Narrowed claims" (N1) |
| 2 | Writes are never shared | tier S `coalesce-writes-{strict,relaxed}` (every write exactly once in the write log; every in-transaction read reached the database); `coalesce-marked-write-hinted` shows the write check fails when identical marked writes are shared under `hinted`; core `TestOnlyShareableUnitsJoinUnderAutoAndOnlyHintedUnderHinted` |
| 3 | Sharing never reads SQL | `Table.eligible` reads `Shareable`/`Hinted`/`HasKey` only; importlint (`internal/coalesce` → no `pgwire`, no `pgproto3`); plant `PlantSafeReadDoor` caught by the property test |
| 4 | Strict is never stale; relaxed shows the window | core `TestStrictJoinsTheNextFlightRelaxedJoinsTheRunningOne`, the driver's strict check; engine `TestStrictHoldsARequestThatArrivesWhileTheReadRuns`; tier S `coalesce-read-your-write-{strict,relaxed}` (strict ablation `PlantStrictJoinsRunning` fails it); tier R `TestTierRCoalesceReadYourWrite` |
| 5 | One caller's cancel hurts no other | core `TestNinetyNineLeaveAndTheLastGetsTheReply`; engine `TestTheOwnerLeavingHandsTheLeaseToAWaiter`; tier S `coalesce-cancel-99-*` (ablation `PlantAnyLeaveCancels` fails it); tier R `TestTierRCoalesceCancel99` |
| 6 | A leaver gets its own error even at the result's instant | core `TestLeaveAtTheInstantTheResultArrivesGetsOnlyTheLeaversOwnError`, the driver's `cancel-at-result` op; engine `TestALeaverAtTheInstantOfTheResultIsNotAnswered`; fuzz plant `PlantLeaverGetsResult`. Narrowed: see "Narrowed claims" (N2) |
| 7 | All leave: work cancelled, dirty table, nothing leaks | core `TestTheLastLeaverCancelsTheFlight`; tier S `coalesce-vanish-all-*` (read aborted `57014` at the fake, `lease_ends{vanished-mid-statement}`, cancels sent, recovery, quiesce) and `coalesce-cancel-all-*`; tier R `TestTierRCoalesceAllCancel` (0 completed reads) |
| 8 | Flight deadline independent, fires at equality, shared error | core `TestResultAtTheFlightDeadlineIsLate`; tier S `coalesce-shared-error-*` (every client exactly one shared error: `08006` from the read that ran, or `53M01` from a held flight that expired before it was sent) |
| 9 | Caps, fixed memory | core `TestWaiterCapRefuses`, `TestReplyCapRefusesFollowersAndListsTheKeyForAWhile`, `TestNoShareListIsBoundedAndEvictsTheOldest`, `TestFlightTableCapPassesUnshared`, `TestFuzzExecutionCostIsBounded`; tier S `coalesce-caps-*` (ablation without caps fails); generated capacity rows |
| 10 | Shared errors, no stampede | core `TestFailedFlightSharesItsErrorOnce`; tier S `coalesce-shared-error-*` (≤ 2 reads for 100 waiters; ablation off fails) |
| 11 | Followers take no slot or connection; per-tenant; conservation on every path incl. Shutdown | engine `TestFollowerGetsTheSameBytesAsTheRequestThatRanTheRead` (coalesced 1, queued 0, in flight 1); `Snapshot.Check` conservation with `coalesced` (every scenario, every event via `Invariants`); `TestEngineQuiescesWithAnEmptyFlightTableAndNoTimer` (Shutdown); `coalesce-tenant-scope` and `coalsrc` tests (per-tenant hits); SQL `coalescer.sql` |
| 12 | Complete replies; slow follower delays no one; results equal direct | engine `TestFollowerGetsTheSameBytes…`; tier S `coalesce-slow-follower-*`, `coalesce-results-*` (`ResultsEqual` against the direct arm); tier R `TestTierRCoalesceResultsEqualDirect` |
| 13 | Hot key: 1–2 reads; ablation 1,000 | tier S `coalesce-hot-key-*` (+ ablation, + SQL two-arm join `reads.sql`); tier R `TestTierRCoalesceHotKey` |
| 14 | Config | `internal/config/coalesce_test.go` (defaults, keys reach the engine, each refusal names its key, `on-switch` cases); tier S `coalesce-hinted` (marked shared, unmarked not; ablation `auto` fails) |
| 15 | Entries removed; after quiesce empty, no timer | `Table.Quiesce`, `Engine.Quiesce` (`coalQuiet`), every scenario's `Harness.Finish`; core `TestShutdownEmptiesTheTable`; SQL final-flush checks |
| 16 | `Invariants` covers flights and waiters, a test fails without the block | `internal/proxy` `TestInvariantsCoverFlightsAndWaiters` (mutation M7) |
| 17 | Replay and flush | `runCoalesce` (two runs per seed: digests and flushed files byte-identical); core `TestReplayIsBitIdentical`; SQL in `test/analysis/coalesce-*` |
| 18 | Fuzz target | `FuzzCoalesceOps` (fixed decoding, no seed from a hash, `TestFuzzDecodingIsTotalBoundedAndLocal`, `TestFuzzExecutionCostIsBounded`); `scripts/fuzz-plant.sh` finds `PlantLeaverGetsResult` in about 1 s; unplanted 30 s clean (about 850 execs/s) |

Planted-bug canaries (core property test, `TestPlantedBugsAreCaughtByThePropertyTest`): strict
join (`PlantStrictJoinsRunning`), the waiter count that guards the cancel
(`PlantAnyLeaveCancels`), the leaver's own error (`PlantLeaverGetsResult`), the waiter cap
(`PlantNoWaiterCap`), the reply cap (`PlantNoReplyCap`), `Shareable` as the only door
(`PlantSafeReadDoor`). Each is also an ablation of a tier S scenario where the engine can reach
it (strict, waiter count, caps).

## Narrowed claims

Written for the orchestrator's decision (repair round, decisions e and f). The behaviour stands;
the claim is narrower than the brief's wording.

**N1 — claim 1.** At most one read per key is in flight **among the flights the coalescer owns**
(per instance, at every instant, counting reads running on the database). Three cases are outside
it; each passes through unshared and each is counted:

| Case | What happens | Counter (snapshot field; exported metric) | Test |
| --- | --- | --- | --- |
| A read whose last waiter left and whose cancel has not yet reached the database | The flight ends at once (`LeftLast`); the engine sends the cancel; a new request for the key starts a new flight while the old read may still be finishing on the database | `Snapshot.Cancelled`; `coalescer.flight_ends{outcome=cancelled}` (the engine's `cancels_forwarded` / the dirty-connection table count the cancel itself) | `internal/coalesce` `TestACancelledReadNoLongerOwnedLetsTheNextReadStartAndIsCounted`; attack `TestAdv_1_CancelAcknowledgementGap` shows the case |
| A key on the no-share list | The request passes unshared while an earlier oversized read of the key may still run | `Snapshot.PassedNoShare`; `coalescer.passed{reason=no-share}` | `TestAKeyOnTheNoShareListPassesUnsharedAndIsCounted`; attack `TestAdv_1_NoShareBypassOverlap` |
| A request arriving while the flight table is full | It passes unshared; identical requests at a full table may run at once | `Snapshot.PassedFull` and `Snapshot.Overflows[CapFlights]`; `coalescer.passed{reason=full}`, `coalescer.overflows{reason=flights}` | `TestARequestArrivingAtAFullTablePassesUnsharedAndIsCounted`; attack `TestAdv_1_FullTableDuplicateBypass` |

Each test also asserts `Table.Running(key) ≤ 1` (the owned-flight count) in its case.

**N2 — claim 6.** At one instant the order of events decides: a result applied before a waiter's
leave is delivered to that waiter (the leave then finds nothing: `Snapshot.LeftUnknown`,
`coalescer.left_unknown`); a leave applied first gets nothing of the result, only the leaver's
own error (`57014` for a cancel) or nothing for a client that is gone. Tests:
`internal/coalesce` `TestAtOneInstantTheOrderOfEventsDecidesWhatALeaverGets` (both orders); result
first is also attack `TestAdv_6_ResultBeforeCancelTie`; leave first is
`TestLeaveAtTheInstantTheResultArrivesGetsOnlyTheLeaversOwnError`, engine
`TestALeaverAtTheInstantOfTheResultIsNotAnswered`, and attacks `TestAdv_6_CancelBeforeResultTie`,
`TestAdv_6_GoneBeforeResultTie` (held).

The four attacks above (`TestAdv_1_*` ×3, `TestAdv_6_ResultBeforeCancelTie`) are not wrong tests:
each asserts the brief's broad wording, which the narrowed claims replace. They still fail and
stay unedited (`agent_work/adversarial/` is evidence); settling them is the orchestrator's call.

## Mutation proofs (scratch copy `/tmp/zgw-c11/mut`, source edits, not plants)

| # | Mutation | Failing tests |
| --- | --- | --- |
| M1 | strict treats a running flight as joinable (`running := false && …`) | `TestFlightTableAgreesWithReferenceModel`, `TestStrictJoinsTheNextFlightRelaxedJoinsTheRunningOne`, tier S `coalesce-read-your-write-strict` |
| M2 | an owner leaving with followers ends the flight (no handoff) | `TestFlightTableAgreesWithReferenceModel`, `TestNinetyNineLeaveAndTheLastGetsTheReply`, tier S `coalesce-cancel-99-*` |
| M3 | a leaving follower stays on the list | `TestFlightTableAgreesWithReferenceModel`, `TestLeaveAtTheInstantTheResultArrivesGetsOnlyTheLeaversOwnError` |
| M4 | no waiter cap | `TestWaiterCapRefuses`, tier S `coalesce-caps-strict` |
| M5 | no reply cap | `TestReplyCapRefusesFollowersAndListsTheKeyForAWhile`, tier S `coalesce-caps-strict` |
| M6 | `auto` admits `SafeRead` instead of `Shareable` | `TestFlightTableAgreesWithReferenceModel`, `TestOnlyShareableUnitsJoinUnderAutoAndOnlyHintedUnderHinted` |
| M7 | `Engine.Invariants` block for flights disabled | `TestInvariantsCoverFlightsAndWaiters` |
| M8 | a follower's own end marker not written | `TestFollowerGetsTheSameBytesAsTheRequestThatRanTheRead`, tier S `coalesce-results-strict` |
| M9 | the engine ignores a handoff | `TestTheOwnerLeavingHandsTheLeaseToAWaiter`, tier S `coalesce-cancel-99-relaxed` |
| M10 | no flight deadline | `TestFlightTableAgreesWithReferenceModel`, `TestResultAtTheFlightDeadlineIsLate` |
| M11 | the deadline fires one tick late (`<` instead of `<=`) | `TestResultAtTheFlightDeadlineIsLate` |
| M12 (repair) | `coalExpireOwner` removes a queued owner with `unqueue` again (finding 1 reverted) | `TestAQueuedFlightAtItsDeadlineIsShedThroughTheAdmissionQueue`; tier S `coalesce-shared-error-strict` seed 2 (`invariant proxy.engine: admission: the core shed 0, the engine answered 1`) — the scenarios could not see it before they ran with admission queues |
| M13 (repair) | `coalStart` without the `stopping` guard (finding 2 reverted) | `TestShutdownSendsNothingNewToTheDatabase` (shutdown wrote the held read to the second connection) |
| M14 (repair) | the old key (tenant, `0x00`, text) | `TestDifferentShareKeysNeverMapToOneFlightKey`, `TestTenantAndTextWithAZeroByteStartTwoFlights` |
| M15 (repair) | plain `now + d` deadlines | `TestADeadlineNearTheLargestInstantSaturates` |
| M16 (repair) | `coalCheck` without the timer check | `TestInvariantsCoverFlightsAndWaiters` case "timer" |

M13–M16 are the code before the repair commits (each test was seen failing before its fix).

## Why the scenario checks cannot race (derivable bounds)

- **Hot key, 1–2 reads**: 1,000 clients start within 3 ms (3 µs apart) and send at once after a
  sub-millisecond startup, so every request arrives within W ≈ 5 ms of the first; the read takes
  S = 50 ms of virtual time. Strict: the requests before the first read is sent join it; every
  later one arrives before it ends (W < S) and joins the one held flight; that flight starts after
  the first ends, by which time no request is left: exactly ≤ 2 reads. Relaxed: every request
  arrives before the single read ends: 1 read. The p99 bounds (1.5 S relaxed, 2.5 S strict) follow
  from the same W < S. Virtual time; no wall clock.
- **Cancel 99**: cancels at +100 ms, read 300 ms: every cancel lands while the read runs.
- **Read-your-write**: the reader's read runs 0–100 ms; the write is acknowledged at about
  12 ms; the writer's read arrives at about 13 ms, strictly inside the running read.
- **Shared error**: read 2 s, flight deadline 500 ms from creation: every flight expires before
  any reply.
- Tier R uses margins instead (open point 11; a 500 ms hot read against an arrival spread of a
  few milliseconds after a barrier).

## 200-seed sweeps

Every new tier S scenario was run on 200 distinct seeds (1–200; the pinned seeds 1–3 are
among them), each seed run twice for the replay law (digests and flushed files byte-identical),
plus its ablation arm (which must fail its check) and, for the hot key, the direct arm:
`MG_C11_FULL_SWEEP=1 DST_SEEDS=200 DST_SEED_BASE=1 RUN_DIR=/tmp/zgw-c11/sweep go test -run
'^TestScenarioCoalesce$' ./test/e2e/` (the env var lifts the test's own caps of 20 seeds, and 3
for the hot key, that keep the gate's sweep short). Seed counts were confirmed from the run
directories (200 per scenario).

| Scenarios | Seeds | Result |
| --- | --- | --- |
| 19 light scenarios (writes ×2, cancel-99 ×2, cancel-all ×2, vanish-all ×2, read-your-write ×2, slow-follower ×2, caps ×2, results ×2, marked-write-hinted, hinted, tenant-scope) | 200 each | all pass |
| `coalesce-shared-error-{strict,relaxed}` | 200 each | first sweep: strict failed on 80 of 200 seeds; fixed (below); re-swept: 400 of 400 pass |
| `coalesce-hot-key-{strict,relaxed}` | 200 each | all pass (seeds 1–103 / 1–72 in the first run, the rest in four parallel ranges, seeds 1–3 re-run alone) |

The one finding: in `coalesce-shared-error-strict` a held (strict next) flight is promoted when
the first flight expires, then waits in the queue while the first flight's connection is in the
dirty-connection table, and expires there; its waiters correctly get the never-sent `53M01`
(open point 7), but the check demanded `08006` from every client. The check now requires
exactly one shared error per client, `08006` or `53M01`, and at least one `08006` (commit
`6f07dd5`). Not a product change.

A second artefact, not a finding: two of the parallel hot-key processes both ran the pinned
seed 2 into one run directory at the same time, so its flushed files did not round-trip; seeds 1–3
re-run alone passed.

DuckDB over the swept files (`scripts/csv-assert.sh <scenario dir> <sql>`, per scenario):
`coalesce-hot-key-{strict,relaxed}/reads.sql` over 600 runs each (base, ablation, direct; 200
seeds) — 0 failures, so every seed's base arm made 1–2 database reads against exactly 1,000 in
its ablation arm (the two-arm join), flights + hits = shareable reads, and the waiters-per-flight
histogram matched; `coalescer.sql` over each light scenario's 200 seeds — 0 failures in all 21 (the base arm of every seed balances, and the final flush shows nothing left).

## Headline 2 and the hot key (numbers)

Tier S (exact, virtual time; seed 1; `TestCoalesceHotKeyNumbers`), 1,000 identical reads,
read time 50 ms, 4 connections:

| Arm | Database reads | p50 | p99 |
| --- | --- | --- | --- |
| strict | 2 | 100.2 ms | 101.7 ms |
| relaxed | 1 | 50.0 ms | 51.5 ms |
| coalescer off (ablation) | 1,000 | 6.38 s | 12.65 s |
| direct to the fake (chunk 2's baseline shape, 1,000 connections) | 1,000 | 50.3 ms | 50.3 ms |

Tier R (Postgres 17, `TestTierRCoalesceHotKey`, one run, wall clock, 20 pool connections, the
read is a view over `pg_sleep(0.5)`; 1,000 clients connected first, then released together):

| Arm | Clients | Database reads (`pg_stat_statements` calls) | p50 | p99 |
| --- | --- | --- | --- | --- |
| strict | 1,000 | 1 | 569 ms | 594 ms |
| relaxed | 1,000 | 1 | 550 ms | 563 ms |
| coalescer off | 1,000 | 1,000 | 13.85 s | 26.12 s |
| direct (100 connections; the container allows 200) | 100 | 100 | 541 ms | 563 ms |

Strict gave 1 read in tier R because every request arrived before the cold pool's first
connection opened, so all joined the queued flight.

**Headline 2, Zipf skew** (tier R, `TestTierRCoalesceHeadlineZipf`): 100 clients, closed loop,
30 reads each, key drawn by `randdist.NewZipf` with exponent s = 1.100 over 200 keys (seed 11).
What the generator actually produced: 3,000 reads, the hottest key 656 times (21.9%), the top 10
keys 58.1%, 192 of 200 keys used. Each read is a view over `pg_sleep(0.01)`, 20 connections.

| Arm | Requests | Database reads | p50 | p99 |
| --- | --- | --- | --- | --- |
| off | 3,000 | 3,000 (100%) | 106 ms | 305 ms |
| strict | 3,000 | 1,235 (41.2%) | 67 ms | 196 ms |
| relaxed | 3,000 | 1,102 (36.7%) | 57 ms | 171 ms |

(A first run gave off 3,000 / p99 332 ms, strict 1,234 / 171 ms, relaxed 1,078 / 185 ms.)
The spec's "99/1" is restated as the measured shares above. One run, no repeats, no A/A floor:
the p99 values are indicative only; the read counts are the headline.

## Fast set

`scripts/ci-fast.sh` took 401 s on 2026-11-27 with the machine's load average at 10–14 on 4
cores (other builds running); it was not measurable alone. This chunk adds about 6 s of tier S
scenarios under `-short` (21 light scenarios, one seed; the 1,000-client ones skip), about 1 s of
core and `coalsrc` tests, and one baseline run in `internal/obs/simrun` (under a second). The 1,000-client hot-key scenarios run only where their files are kept (the sweep, with
`RUN_DIR` set, three seeds since the repair round) or with `MG_C11_HEAVY=1` (the gate's extra, three seeds; my sweeps),
never under `-short`; `TestCoalesceHotKeyNumbers` runs only with `MG_C11_HEAVY=1`. This was
tightened after the fourth gate run, whose G3 unit stage hit the 30-minute package timeout in
`test/e2e` at a load average of about 36 while those tests were running. Since the repair round
`internal/obs/simrun` also runs `coalesce-hot-key-strict` and its ablation arm once (seed 1)
outside `-short`, so its `reads.sql` runs in G3 of every gate.

## Fleet row (PLAN 8.2, 12.3 item 10)

One read per hot key **weakens** to at most N reads per burst at N instances: flights are per
instance, nothing is coordinated. Chunk 13 measures it; `CoalesceScenarios` specs are plain
`Spec`s, so a scenario with N engines can reuse `HotKeySpec` per engine and count reads from the
one fake's statement log.

## Steady-state row

What grows: flights, waiters, stored reply bytes, the no-share list. What removes them:
completion, the deadline, the last leaver, expiry (and Shutdown). Gauges: `coalescer.flights`,
`table_size`, `waiting`, `stored_bytes`, `no_share`; `engine.requests_coalesced`. After quiesce:
all zero, no `tCoalesce` timer (checked by `Engine.Quiesce` and the SQL final-flush checks).

## Mechanism row (Release It! four parts)

Where: the coalesce stage, after draining, breaker and rate gate, before the queue. Numbers:
waiters per flight (`max_waiters`), shared-reply bytes (`max_reply_bytes`), flights
(`max_flights`), no-share expiry (`no_share_for`), flight deadline (`timeouts.statement`). What the
caller gets: the shared reply with its own end marker, the shared error, `53M04`, or its own
`57014`. Signal: `coalescer.overflows{reason}`, `flights_started`, `hits`, `waiters_per_flight`,
`waiter_ends`, `flight_ends`.

## Observability (7.6)

Snapshot: `coalesce.Snapshot` (counters, gauges, histogram, per-tenant hits, hot keys) and the
engine's `Coalesce*` fields. Exported by `coalsrc` (CSV, Prometheus, JSON, log line through the
collector), documented in `docs/csv-schema.md`. Conservation: offered = started + hits + refused
+ passed; waiters in = reply + shared error + own error + gone + refused + waiting; started =
completed + failed + cancelled + expired + live; no-share added = expired + evicted + renewed +
listed; engine accepted = completed + failed + shed + queued + in flight + coalesced. Coverage-table
row (7.5, Contention): flights, hits, waiters per flight, cancellations (waiter `own-error`/`gone`,
flight `cancelled`), overflows by cap. Health rule: open point 10.

## Shared files touched

- `internal/proxy/engine.go` (fields; `initCoalesce` call; `tCoalesce` case; `coalShutdown`,
  `coalQuiet`, `coalCheck` lines), `client.go` (phase `cpCoalesced`; `fl`, `wid` on client;
  `fl`, `store`, `stored`, `sawErr` on request; `coalClosing` call), `lease.go` (`coalJoin`,
  `coalSent`, `coalStoring`, `coalSawError`, `coalReady`, `coalFailed`, `coalRefused` calls),
  `corepool.go` (`coalCancel`, `coalRefused` calls), `config.go` (`Coalesce` field, defaults,
  validation line), `snapshot.go` (fields appended, `coalesced` term), `timers.go`
  (`tCoalesce`), `contracts.go` (appends the coalescer rows).
- `internal/config/config.go` (field), `validate.go` (defaults call, `proxyCaps` fields,
  validation call).
- `internal/obs/catalog/catalog.go`, `internal/obs/enginesrc/enginesrc.go`,
  `internal/obs/simrun/engine_test.go` (coalescer scenarios as baseline runs; heavy ones skipped
  under `-short`).
- `test/e2e/spec.go` (`Coalesce`, `KeyScope`), `simtier.go` (engine config, fingerprint),
  `c06a_tierr_test.go` (`adminStats` waits for the first publish),
  `realtier.go` (`GatewayOpts.Coalesce`; `mode: off` for `on-switch` gateways).
- `scripts/fuzz-plant.sh`, `tools/lint-rules/det-packages.txt`, `race-packages.txt`,
  `agent_work/INDEX.md`.
- Repair round: `internal/pgwire/share_doc.go` (the safe-shape generator gains a "Limits"
  section; `docs/safe-shape.md` regenerated), `internal/config/testdata/example.json` (the
  `coalesce` section), `internal/config/validate.go` (one line: the resolved reply cap written
  back), `internal/obs/simrun/csvassert_test.go` (the count message), `internal/obs/simrun/engine_test.go`
  (the hot-key scenario as a baseline run outside `-short`), `tools/lint-rules/det-packages.txt`
  (`internal/obs/coalsrc`).

## Changes to earlier chunks' tests

- `internal/shell/contracts_test.go`: the "Not yet built" lines of the generated timeout and
  capacity documents no longer list the coalescer (it is built); the capacity line keeps chunk 9's
  active-tenant states. Text only.
- `test/e2e/realtier.go` (harness, used by chunks 6–8's tier R tests): a gateway with
  `lease.reset: on-switch` and no explicit coalesce setting now gets `coalesce.mode: off`. Reason:
  the binary's new default (`auto`, all tenants) is refused together with `on-switch` by design
  (D10); without this `TestTierRPoolSessionStateUnderEachReleasePolicy` could not start its
  on-switch gateways. Every other earlier tier R test runs with the new default (`auto`) and
  passed (run of 2026-11-27, `/tmp/zgw-c11/tr1.log`: only that test failed, before the change).
  Not a weakening: the on-switch test's subject is the reset policy, which coalescing does not
  touch.
- `internal/obs/simrun/engine_test.go`: adds one coalescer scenario (`coalesce-caps-strict`, which
  moves every coalescer metric, the per-tenant hits included) to the baseline runs and lists the
  other coalescer scenarios as left to the sweep, whose files G7 asserts. All of them were
  baseline runs until the sixth gate run, where `internal/obs/simrun` hit its 30-minute timeout
  at a load average of about 40.
- `test/e2e/c06a_tierr_test.go` (`adminStats`): waits, at most `freshStatsDeadline` (15 s), while
  `/stats` answers `503 nothing published yet`, then decodes. Reason: the first full gate run of
  this chunk failed G6 on `TestTierRMgctlSeededModelAgainstTheRealBinary` with `invalid character
  'o' in literal null` — the test read `/stats` before the gateway's first publish (machine load
  average about 13). It passed 3 of 3 alone before the change. Not a weakening: the decoded
  snapshot and every assertion on it are unchanged; a gateway that never publishes still fails
  (the 503 body does not decode).

- Repair round, `internal/obs/simrun/engine_test.go`: `coalesce-hot-key-strict` and its ablation
  arm (coalescer off) are baseline runs again when `-short` is not set, so its two-arm
  `reads.sql` runs in G3 of every later gate, not only in this chunk's `extra.sh`; the count
  message no longer calls the skipped files "(-short)". It strengthens: one more scenario runs.

## Deviations

- **Tier R "all 100 leave" uses cancel requests, not vanishing clients.** The real shell does
  not read a client socket while that client's request is in progress (D5), so a client that
  resets its connection mid-request is seen only when the reply is written; the first tier R
  attempt (100 clients aborting at +300 ms) got 100 replies and 1 completed read. The vanish
  case and the dirty-connection table are proved in tier S (`coalesce-vanish-all-*`, where the
  simulated network delivers the reset at once). Tier R proves the work is cancelled when all
  100 cancel (0 completed reads).
- **Tier R read time 500 ms, not 50 ms**, so that the arrival spread of 1,000 real clients
  (released by a barrier) is far below the read time; tier S keeps 50 ms and is exact.
- **Tier R direct baseline: 100 connections** (the tier R container allows 200; a 400-client
  direct arm failed with `53300` and left the next test's gateway with a tripped breaker). The
  1,000-connection direct baseline is tier S's.
- **No tier R SQL assertions** for the coalescer scenarios; their checks read the gateway's
  final flush through `stopAndRead` (conservation from the file) and the coalescer counters.
- **Headline 2 is one run per arm**, not the 10 interleaved runs of section 11 (chunk 14 owns the
  benchmark discipline). The database read counts are exact; the latencies are indicative.

## Stated limits

1. **Default**: the binary shares by default (`coalesce.mode: auto`, `strict`, all tenants). The
   safe shape trusts the objects it names: a view, a user-defined operator, a function that
   shadows an allow-listed name, or a **row-level security policy** with side effects or
   per-call results is shared like any read. `coalesce.mode: off` and `hinted` exist for
   databases where that matters (D10).
2. `hinted` shares whatever carries the mark, writes included (the mark is the client's
   statement; `coalesce-marked-write-hinted` shows it).
3. "At most one in-flight read per key" holds among the flights the coalescer owns; see
   "Narrowed claims" (N1) for the three counted cases outside it.
4. Relaxed mode can return data older than the request (the read-your-own-write window); strict
   costs up to one more read time.
5. Flights are per instance (fleet row).
6. Per-key hotness is opt-in and exported by rank only; the key texts are in the snapshot, not in
   metrics.
7. **Vanished clients on real sockets** (D5): the real shell does not read a client socket while
   that client's request is in progress, so a client that resets its connection mid-request is
   seen only when its reply is written. Such a client does not leave its flight early: it stays a
   waiter until the result (or the flight deadline), and only a `CancelRequest` makes it leave
   before then. In tier S the simulated network delivers the reset at once.
8. The flight key is the tenant's length (8 bytes, big-endian), the tenant, then the exact text,
   so no two different (tenant, text) pairs share a flight whatever bytes either holds.
9. A deadline or no-share expiry computed past the largest instant saturates at it.

## Gate runs (`GATE_BASE=6cffb0f scripts/gate.sh c11-coalescer`, build stage)

| Run | Commit | Result |
| --- | --- | --- |
| 1 | `89b655e` | G0–G5 pass (sweep 1,847 s; fuzz 19 targets); G6 failed on chunk 6a's `TestTierRMgctlSeededModelAgainstTheRealBinary` (`/stats` read before the first publish; fixed in the test, see "Changes to earlier chunks' tests") |
| 2 | `a97cfec` | G0–G3 pass; G4 sweep hit `sweep.sh`'s 60-minute package timeout in `test/e2e` (load average 13–19) |
| 3 | `6158b29` (coalescer seeds capped at 10 / 1 in the sweep) | G4 timeout again, in chunk 6's `TestScenarioE2E` (every coalescer scenario had passed); load 30–45 with another agent's gate running |
| 4, 5 | stopped / dirty tree | a stray fuzz input left by a killed `fuzz-plant.sh` made one start dirty; restarted |
| 6, 7 | `1eccd4c`, `6fb6d76` (1,000-client tests out of G3; one coalescer baseline in simrun) | G3 failed: `internal/obs/simrun` hit its 30-minute package timeout (in chunk 3/4 tests in run 7); also chunk 7's wall-clock `TestFuzzPoolOpsCostIsBounded` under the race detector (11.9 ms against 5 ms). Load average 25–40 with three other agents' full gates (c09, c12, lease-lab-port) running at the same time |
| 8 (final) | `d37bf98` | **pass, `counts_for_landing: true`**, clean tree, build stage: G3 unit 919 s, race 57 s; G4 sweep 1,466 s (200 seeds per scenario); G5 19 fuzz targets (two retried once on Go's own deadline error, CR-2); G6 tier R 420 s (Postgres 17.11); G7 5,359 run directories, 111 scenarios with SQL; G8, G9; G10 extra 56 s (all four fuzz plants found in 1–3 s; core, engine and hot-key tests green). `result.json` was deleted afterwards, not committed |
| 9 (repair round) | `5f41cae` | **overall fail, only in G3 unit**: the four narrowed attacks (`TestAdv_1_CancelAcknowledgementGap`, `TestAdv_1_NoShareBypassOverlap`, `TestAdv_1_FullTableDuplicateBypass`, `TestAdv_6_ResultBeforeCancelTie`) fail in `agent_work/adversarial/c11-coalescer` as expected until the orchestrator settles N1/N2; every other package of G3 passed (997 s). All other stages pass on a clean tree at the build stage: G0, G1 (29 det patterns), G2; G3 counted, race 49 s; G4 sweep 1,701 s (10,061 seed runs, 0 failures; hot key on 3 seeds); G5 20 fuzz targets, 0 retried; G6 tier R 387 s (Postgres 17.11); G7 3,596 s (5,371 run directories, 111 scenarios with SQL); G8, G9; G10 extra 69 s (all four plants found in 2–5 s). `result.json` deleted, not committed |

## Commands

```
go test ./internal/coalesce/ ./internal/proxy/ ./internal/obs/coalsrc/ ./internal/config/
go test -short -run TestScenarioCoalesce ./test/e2e/                       # 21 light scenarios, 1 seed
MG_C11_HEAVY=1 go test -run 'TestScenarioCoalesce/coalesce-hot-key|TestCoalesceHotKeyNumbers' ./test/e2e/
MG_C11_HEAVY=1 MG_C11_FULL_SWEEP=1 DST_SEEDS=200 RUN_DIR=<dir> go test -run '^TestScenarioCoalesce$' ./test/e2e/
scripts/csv-assert.sh <dir>                                               # SQL over the sweep
MG_TIER_R=1 go test -run '^TestTierRCoalesce' ./test/e2e/                 # needs Docker
go test -run '^$' -fuzz '^FuzzCoalesceOps$' -fuzztime 30s ./internal/coalesce/
go test -run '^$' -fuzz '^FuzzShareKeyIsUnambiguous$' -fuzztime 10s ./internal/coalesce/
go test -count=1 ./agent_work/adversarial/c11-coalescer/                     # 53 pass; the 4 narrowed attacks fail (N1, N2)
scripts/fuzz-plant.sh
scripts/gate.sh c11-coalescer   (GATE_BASE=6cffb0f on this branch, whose base carries chunk 7's prose comments)
```

## Fuzz targets

| Target | Package | Seed source | Rate (vm-1) |
| --- | --- | --- | --- |
| `FuzzCoalesceOps` | `internal/coalesce` | none: the input decodes to operations (3 bytes each, at most 96), no simulator, no randomness | about 850/s |
| `FuzzShareKeyIsUnambiguous` | `internal/coalesce` | none: four strings straight from the input (two tenants, two texts), no simulator | tens of thousands/s |

Neither imports `internal/sim`, so the pinned inventory in
`agent_work/adversarial/cr01-shrink-fuzz-rules/` is unchanged.

## Index rows

Added to `agent_work/INDEX.md` under "Phase 2, chunk 11".
