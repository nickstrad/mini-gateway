# Chunk 9 — Fairness: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c09-fairness`, worktree
`/root/Software/mini-gateway-work/c09`. No helper agents. Mutations were applied only in a
scratch copy (`/tmp/zgw-c09/mut`, from `git archive HEAD`), never in this tree.

No SPLIT-REQUEST: every deliverable of the brief is built. Product Go added: about 830 lines
(`internal/admission/fair.go` 545, `internal/proxy/fairness.go` 151, `internal/obs/admsrc/fair.go`
54, `internal/config/fairness.go` 27, the rest one-line hooks); the non-test e2e world
`test/e2e/c09_fairness.go` 376; test Go about 2,050 lines. Inside PLAN.md's 900.

## Simulator change requests

None. The scenarios use the frozen fake's per-kind service times (`fakepg.Service.PerKind`:
reads 5 ms, `UPDATE` 10 ms, `DISCARD ALL` free) and the workload's existing tenants, transactions
and closed-loop clients.

## What was built

| Path | What it is |
| --- | --- |
| `internal/admission/fair.go` | `Fair`, the fair dispatcher (implements `admission.Dispatcher`): the limit (`SetLimit`, `Limit`), shares with optional weights, the order of choice (below-share first, round robin by turn), the last-lease rule, per-tenant in-flight accounting (`LeaseStart`, `LeaseEnd` on `Queues`), state only for tenants with a lease, time at share and saturated time, `FairConfig` with validation naming each key, `FairSnapshot`, checks for `Queues.Check`, `Snapshot.Check` and `Quiesce`, test-only `FairPlant` bits. Also `Queues.Depth`. |
| `internal/admission/queues.go`, `tenant.go`, `snapshot.go` (additive edits) | `TenantView.Turn`; a turn per tenant queue (`tenantQueue.turn`) and a round counter (`Queues.round`) set when a pop is served and restored by `Unpop`; time accounting before each internal deadline step; the dispatcher attached by `SetDispatcher`; per-tenant `InFlight`, `Share`, `AtShare` and `Snapshot.Fair`. |
| `internal/proxy/fairness.go` (new) and one-line hooks in `engine.go`, `lease.go`, `corepool.go`, `config.go`, `contracts.go` | The engine installs the fair dispatcher on every `AdmissionQueue` it builds (limit and lease cap `Config.Connections`); reports a lease start in `lend` and a lease end beside every `LeasesEnded++` (release on both pools, the dirty-connection table, a lost lease, shutdown through those paths); `fairRedispatch` at the end of every event while the dispatcher holds a request back; the `fairness:` block in `Engine.Invariants`; `Config.Fairness`; capacity rows; read-only probes `TenantLeases`, `TenantQueued`; test plant `PlantFairStatementInFlight` (bit 15, chosen high so wave C's other plants do not collide). |
| `internal/config/fairness.go` (+ one line each in `config.go`, `validate.go`) | Section `fairness`: `default_weight`, `weights` (`[{tenant, weight}]`). Zero or omitted is the default, negative refused, at most 1,000, at most 4,096 entries, duplicates refused; errors name the key. Validated through `proxy.Config.Validate`. |
| `internal/obs/admsrc/fair.go` (+ `admsrc.go` two hooks) | The new metrics on component `admission` (below); per-tenant rows with `obs.detail` through chunk 8's bounded tenant label. |
| `test/e2e/c09_fairness.go`, `c09_fairness_test.go`, `c09_tierr_test.go` | `RunFair` (open-loop and closed-loop workloads through the real engine, CSV flush, replay), the noisy-neighbour, ablation, many-tenants and held-transaction scenarios, their canaries, the section 14 re-run `fair-results`, and tier R `TestTierRFairNoisyNeighbour`. |
| `test/analysis/fair-*`, `tierr-fair-noisy` | SQL assertions over the flushed files (below). |
| `scripts/fuzz-plant.sh` (appended) | `FuzzFairnessOps` must find `FairPlantUnpopKeepsTurn`. |
| `agent_work/chunks/c09-fairness/gate/extra.sh` | G10: `fuzz-plant.sh` and the fairness canaries. |
| `docs/knowledge/fairness.md` | The short knowledge note, including where chunk 10 sets the limit. |

## The rule, as built

Let `limit` be the fair limit, `in flight` the leases held (a transaction held open keeps its
lease), and, for each **active** tenant (a request waiting in its tenant queue, or a lease in
flight), `share = max(1, floor(limit x weight / sum of the active tenants' weights))`.

1. Nothing is lent while `in flight >= limit` (counted `held{limit}`).
2. A waiting tenant is **below its share** when `its in flight + 1 <= its share`.
3. The lend that takes the last free lease (`in flight + 1 = limit`) goes only to a tenant below
   its share. If no such tenant waits, nothing is lent (`held{share}`).
4. At every other lend, a waiting tenant below its share is chosen before any tenant at or above
   it; a tenant at or above its share is lent only when no tenant below its share waits
   (work-conserving below the last lease).
5. Within each group the choice is round robin: the tenant given a turn least recently (`turn`),
   ties to the oldest head. Serving a pop gives that tenant the newest turn; a new tenant queue
   joins with the turn just before the newest, so it goes ahead of every waiting tenant except the
   one served last. `Unpop` restores the turn and the round count exactly.
6. While the dispatcher holds a request back, the engine tries once more at the end of every
   event (`fairRedispatch`): one selection, no checkout and no pop unless something became
   eligible. There is no fairness timer.

## Decisions the brief left open

1. **Static limit:** `pool.connections` (`proxy.Config.Connections`, either pool). No config key;
   `proxy.Config.Fairness.Limit` exists for tests and for chunk 10 to start from.
2. **"The limit is reached":** a lend reaches the limit when it takes the last free lease
   (`in flight + 1 = limit`, measured after time has passed to the event). Rule 3 binds there, so
   the share binds at the instant a connection frees (in flight below the pool size). Rule 4 was
   added after the held-transaction scenario showed (seed 2) that with only rule 3 a tenant holding
   transactions reached 3 of 4 leases through round robin at a non-last lease while the quiet
   tenant waited at 0; with rule 4 it cannot exceed its share while a below-share tenant waits.
   Equality tests: `TestTheShareBindsExactlyWhenALendWouldReachTheLimit` (3 of 4 binds, 2 of 4 does
   not), `TestALeaseEndingAtTheInstantOfAPickFreesItsShareFirst`,
   `TestTimeAtShareChangesAtTheInstantASweepEmptiesAQueue`,
   `TestTimeAtShareAndSaturationAreMeasuredToTheInstant`.
3. **Rounding and who is active:** integer floor of `limit x weight / active weight`, at least 1
   (with more active tenants than the limit every share is 1; the sum of shares may then exceed
   the limit, the total never does). Active = a request waiting or a lease in flight; an empty
   tenant queue in its quiet window is not active. Weights are integers 1 to 1,000, default 1.
4. **In flight ends at the lease's end,** where the engine counts `LeasesEnded`: before the
   reset after a clean release, at entry to the dirty-connection table, at a loss. The reset or
   clean-up still occupies the connection in the pool, so no connection becomes lendable early;
   the tenant's share frees at the instant its client's lease ends.
5. **Single shared queue ablation:** `proxy.FIFO` (`Config.Admission.Rule == 0`, chunk 6's one
   FIFO, arm `fifo`). `OldestFirst` over the rings is not used as the ablation.
6. **Many-tenants size:** one flooder and 40 quiet tenants (41 tenants): above the Prometheus
   label bound of 32, far below `queue.max_tenants` 1,024 and `MaxTenantStats` 4,096. No chunk 8
   constant changed.
7. **`Demand` keeps counting held-back requests** (unchanged call). With the limit equal to the
   pool size, a request held back by rule 3 can make the pool open its last connection, which
   then idles until a below-share tenant arrives or a lease ends: at most the pool size, no
   cycle (the selection happens before any checkout, so no check, dial or unpop repeats:
   `TestFairAHeldBackRequestCostsNoPopNoUnpopAndNoDial`). For chunk 10, where the limit can be
   below the pool size, `Demand` should be passed the requests the dispatcher could release.
8. **Config keys:** `fairness.default_weight`, `fairness.weights[i].tenant`,
   `fairness.weights[i].weight`. A weight for a tenant not in `tenants` is accepted (decision (e):
   no key of one chunk constrains another's).
9. **Health rule:** none added. The share binding is normal behaviour under saturation, not
   degradation; a request held back too long is shed by chunk 8's rule, whose `admission` rule
   already reports `degraded`. Saturation is visible as `saturated_time` and `held`.
10. **Tier R:** only the noisy neighbour (`TestTierRFairNoisyNeighbour`); many tenants and held
    transactions are tier S only. The p99-within-2x threshold is robust on a shared host by
    construction: the quiet tenant's statement (`pg_sleep(0.02)`) is four times the flooder's
    (`pg_sleep(0.005)`), and its extra wait is at most one flooder statement plus a reset;
    100 quiet requests, p99 of the 100. Measured 2026-11-27 at load average 11-14: quiet 100 of 100
    served, p99 71.2 ms against 54.5 ms alone (1.31x); flooder 899 served, 28,720 shed. It runs
    only with `MG_TIER_R=1`.

## Claims and the tests that prove them

| # | Claim (as built) | Tests |
| --- | --- | --- |
| 1 | The share binds under saturation: the last free lease goes only to a tenant below its share; at every lend a tenant at or above its share is lent only while no tenant below its share waits; in flight includes held transactions | core property `TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim` (checks `claim share` and `claim share order` at every pop, 400 runs x 3 rules x 6 variants, weights included); `TestTheShareBindsExactlyWhenALendWouldReachTheLimit`, `TestBelowTheLimitTheShareDoesNotBind`, `TestWeightsDivideTheLimit`; engine `TestFairTheLastFreeConnectionGoesToTheTenantBelowItsShare` (a held transaction at its share of 1: the freed lease goes to the other tenant), `TestFairAHeldTransactionCountsAsInFlightUntilItsLeaseEnds`; e2e `TestScenarioFairHeldTransactions`; canaries `TestCanaryNoShareBoundBreaksTheShareClaim`, `TestCanaryFairWithoutTheShareBoundLendsAHeldTenantPastItsShare`, `TestCanaryFairHeldTransactionsWithoutTheShareBound` |
| 2 | Accounting: total in flight = sum over tenants = the engine's leases outstanding, at every event, on every way a lease ends | `Engine.Invariants` block `fairnessCheck` (total = `LeasesStarted - LeasesEnded`; per tenant = connections the engine lends it; states = tenants lent to), run after every event in every simulator scenario with an admission queue; `TestFairEveryWayALeaseEndsReturnsItsInFlight` (release, vanished mid-statement, vanished in a transaction, idle in transaction, connection lost, statement deadline, shutdown), `TestFairTheFixedSetReportsEveryLeaseEndToo`; core: reference model per tenant after every operation, `Fair.check` and `FairSnapshot.check` (starts - ends = in flight = sum of rows, states <= in flight); SQL `fair-noisy-neighbour/fair-accounting.sql` (every flush), `fair-held-transactions/held.sql`, `fair-results/fair.sql`, `tierr-fair-noisy/tenants.sql` |
| 3 | Work-conserving: no lendable connection stays idle while an eligible request waits; below the last lease the share only orders the choice | core property `claim work-conserving` (a pop that returns nothing while a waiting tenant is eligible fails), `TestBelowTheLimitTheShareDoesNotBind` (a tenant above its share is lent below the last lease); engine `TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible` |
| 4 | No starvation: a waiting tenant is chosen before any tenant of the same or a lower group is chosen twice, i.e. within (active tenants - 1) choices of its group | core property `claim no starvation` (FIFO arm, exact model: X chosen twice while Y, waiting since before X's earlier choice and eligible, in the same or a better group, was not chosen fails); canary `TestCanaryNoRoundRobinStepIsCaught` |
| 5 | Noisy neighbour, tier S: on every seed the quiet tenant's outcomes equal its outcomes alone, its p99 is within 2x of the control, the flooder takes more than 95% of the shedding, the quiet tenant's successes are non-zero | `TestScenarioFairNoisyNeighbour` (`checkNoisy`: ok = alone ok = intended > 0, no error, p99 <= 2 x alone p99 on virtual time, flooder's 53M01 > 95% of all 53M01, recovery, `checkFairMoved`); SQL `quiet-against-alone.sql` (a join of the arms of one seed), `flooder-shedding.sql`. Why it cannot race: see below |
| 6 | Noisy neighbour, tier R: > 99% success, p99 within 2x, flooder > 95% of the shedding | `TestTierRFairNoisyNeighbour` (numbers in decision 10); SQL `tierr-fair-noisy/tenants.sql` |
| 7 | Many tenants, one flooder: every quiet tenant keeps its outcomes, shown per tenant from the flushed files | `TestScenarioFairManyTenants` (each of 40 quiet tenants: ok = alone, p99 <= 2x alone; flooder > 95% of the shedding; more than 32 tenants); SQL `fair-many-tenants/per-tenant.sql` (per tenant, a join of two arms, fails if 32 or fewer tenants are checked), `flooder-shedding.sql` |
| 8 | Held transactions: a tenant holding transactions open does not exceed its share | `TestScenarioFairHeldTransactions` (the leases the engine lends the transaction tenant, measured after every step by the engine's own count, never exceed its share 2 while the quiet tenant is active, and reach it; all transactions complete before the window closes); engine `TestFairAHeldTransactionCountsAsInFlightUntilItsLeaseEnds`; canaries `TestCanaryFairHeldTransactionsWithoutTheShareBound` (peak above the share), `TestCanaryFairHeldTransactionsNotCountedInFlight` (the scenario fails on `fairness:`) |
| 9 | The ablation fails: with a single shared queue the quiet tenant starves | `TestScenarioFairNoisyNeighbourAblation` (arm `fifo`: the quiet check fails and its p99 is above 10 x alone; seed 1: 2.58 s against 11.1 ms alone, 86 of 88 ok, 2 shed); SQL `quiet-against-alone.sql` (fifo arm, where present, must do worse) |
| 10 | Bounded: dispatcher state only for tenants with a lease, capped; bounded work per pop and per event | `TestFairStateExistsOnlyWhileATenantHasLeasesAndIsBounded` (400 tenants through a cap of 16), `Fair.check` (states <= in flight <= `MaxLeases`; refused starts and unmatched ends are errors), `TestLeaseAccountingRefusesWhatCannotBeTrue`, `TestFairAHeldBackRequestCostsNoPopNoUnpopAndNoDial` (50 events held back: no pop served, no unpop, no dial, no checkout, at most one selection per event), `TestFairFuzzExecutionCostIsBounded`; steady state in `runFair` (no fair state, nothing active or in flight, no tenant queue after quiesce) |
| 11 | Shares are checked at load: a bad weight or share fails closed naming the key | `TestFairnessKeysFailClosedNamingTheKey`, `TestFairnessZeroOrOmittedMeansTheDefault`, `TestFairnessAndQueueKeysAreValidatedTogether`, `TestConfigNeverSetsAFairPlant` (`internal/config`); `TestFairConfigFailsClosedNamingTheKey` (core); `TestFairTheBinaryLimitIsThePoolSize` (an engine with a bad weight is refused) |
| 12 | The join decisions hold: one waiting place, the exits and errors of table (a) unchanged; `Unpop` leaves fairness state untouched; after `Shutdown` conservation holds and `Quiesce` passes | a held-back request stays in its tenant queue (no new structure holds requests; `Queues.Check` and the engine's queue/waiting agreement hold after every event); the scenarios see no reply other than results and `53M01`; `TestUnpopLeavesTurnsAndCountersAsIfThePopNeverHappened` and the reference model (unpop operations); `TestFairEveryWayALeaseEndsReturnsItsInFlight/shutdown`; `Queues.Quiesce` requires no fair state; every chunk 7/8 join test passes with the fair dispatcher installed (`TestJoinShutdownWithQueuedRequestsAndConnectionsInEveryPoolState`, `TestJoinEveryCoreEngineTestHoldsWithTheAdmissionQueue`, ...) |
| 13 | `Engine.Invariants` covers fairness, and a test fails if that block is removed | `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions` (mutation M6 below) |
| 14 | Replay and flush: every scenario replays bit-identically per seed; flushed files agree with the snapshots | `runFair` and `runScenario`: two runs per seed, equal digests and byte-identical files, `Flush.Check` round trip; tier R `stopAndRead` round trip |
| 15 | The fuzz target: total bounded decoding, no seed from a hash of the input, finds the planted bug | `TestFairFuzzDecodingIsTotalBoundedAndLocal`, `TestFairFuzzExecutionCostIsBounded`, `TestPlantedUnpopTurnBugIsCaughtByTheFairnessModel` (also holds that no committed seed finds it alone); `scripts/fuzz-plant.sh`: found after 2 s of 20 s |

### Why the tier S scenarios hold on every seed by construction

- **Noisy neighbour** (2 connections; flooder 200 open-loop clients at 10x, reads 5 ms; quiet
  tenant 1 client every 25 ms, `UPDATE` 10 ms). The quiet tenant never has two requests in the
  proxy (one client, D5) and its latency is at most about 16 ms < 25 ms. When it arrives both
  leases may be the flooder's; the first to end frees the last lease, and only a tenant below its
  share may take it: the quiet tenant (0 + 1 <= 1), not the flooder (1 + 1 > 1). So its wait is at
  most one flooder lease plus a reset (about 5.7 ms) against its own 10 ms statement: p99 <= 2x
  alone. It is never shed: its own queue holds at most one request waiting at most about 6 ms
  (allowed wait 100 ms, its queue is never standing), the flooder overflows its own ring of 32
  before the total of 128 can fill, and `timeouts.queue_wait` is 3 s. So its ok count equals its
  intended count, alone or not, and every 53M01 is the flooder's. Seed 1: p99 14.9 ms against
  11.1 ms alone.
- **Many tenants** (40 quiet tenants, one request each per second, starts 25 ms apart): quiet
  arrivals are 25 ms apart and each finishes within about 16 ms, so at most one quiet request is in
  the proxy and the argument above applies to each.
- **Held transactions** (4 connections, share 2 each; quiet tenant 8 closed-loop clients x 200
  reads from t = 0; transaction tenant 8 closed-loop clients x 3 transactions from t = 100 ms):
  at most 4 quiet requests are in flight and a quiet client spends about 0.3 ms between a reply
  and its next request, so at most as many quiet clients can be between requests as leases end
  in 0.3 ms (at most 4, while a lease lasts 5 ms): at least 4 quiet requests are always in the
  proxy, so the quiet tenant is always active and waiting whenever it holds fewer than 4.
  Whenever the transaction tenant could take a third lease, the quiet tenant holds at most 1 and
  waits below its share: rule 4 (or rule 3 for the last lease) gives it the lease. The quiet
  tenant's 1,600 reads take at least 2.2 s at 4 leases, while the 24 transactions end within about
  1.6 s; the check window closes when the quiet tenant first has nothing in the proxy, and the test
  requires that every transaction had ended by then (`HeldOpen = 0`).
- **fair-results** runs the results scenario behind the relaxed rule (interval 5 s, target 1 s),
  as chunk 8 did for tier R: the binary's 5 ms target shed a `BEGIN` queued behind held
  transactions (seed 3), which is chunk 8's rule, not fairness.

## Canaries and ablations (test-only; `internal/config` never sets them: `TestConfigNeverSetsAFairPlant`)

| Switch | Mechanism | Must fail | Test |
| --- | --- | --- | --- |
| `admission.FairPlantNoShareBound` | the share bound (rules 3 and 4) | core `claim share`; held-transaction peak above the share | `TestCanaryNoShareBoundBreaksTheShareClaim`, `TestCanaryFairWithoutTheShareBoundLendsAHeldTenantPastItsShare`, `TestCanaryFairHeldTransactionsWithoutTheShareBound` |
| `admission.FairPlantNoRoundRobinStep` | the round-robin step | reference model order / starvation / rounds | `TestCanaryNoRoundRobinStepIsCaught` |
| `proxy.PlantFairStatementInFlight` | held transactions counted | `Engine.Invariants` (`fairness:`), in the engine and through the scenario's simulator invariant | `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions`, `TestCanaryFairHeldTransactionsNotCountedInFlight` |
| `admission.FairPlantUnpopKeepsTurn` | the `Unpop` restore | reference model (order, rounds); found by the fuzzer | `TestCanaryUnpopKeepingTheTurnIsCaught`, `TestPlantedUnpopTurnBugIsCaughtByTheFairnessModel`, `scripts/fuzz-plant.sh` |
| Ablation arm `fifo` (`Rule == 0`) | the fair dispatcher over tenant queues | quiet tenant starves | `TestScenarioFairNoisyNeighbourAblation` |

## Mutation proofs (scratch copy, each applied alone, the named test run)

| # | Mutation | Failing test and message |
| --- | --- | --- |
| M1 | rule 3 removed (last lease to an over-share tenant) | `TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim`; `TestFairTheLastFreeConnectionGoesToTheTenantBelowItsShare` |
| M2 | rule 4 removed (no below-share priority) | `TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim` |
| M3 | round-robin step removed (`turn` not advanced) | same: "pop gave 3 of td, the model gives 5 of ta" |
| M4 | `Unpop` does not restore turn and round | same: "rounds 1, model 0" |
| M5 | a held transaction ends its in flight at each statement | `TestFairAHeldTransactionCountsAsInFlightUntilItsLeaseEnds`: "fairness: 0 in flight across tenants, the engine has 1 leases outstanding" |
| M6 | the fairness block removed from `Engine.Invariants` | `TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions`: "passed Engine.Invariants: <nil>" |
| M7 | `fairRedispatch` removed from the end of an event | `TestFairAHeldBackRequestIsLentAtTheEventThatMakesItEligible`: wrote "" |
| M8 | the dirty-table lease end not reported | `TestFairEveryWayALeaseEndsReturnsItsInFlight/vanished-mid-statement` |
| M9 | the lost-lease end not reported | `TestFairEveryWayALeaseEndsReturnsItsInFlight/connection-lost` |
| M10 | time accounted after an internal deadline's step instead of before | first SURVIVED the property test (the exact model runs the FIFO arm, where no sweep changes who waits); `TestTimeAtShareChangesAtTheInstantASweepEmptiesAQueue` was added and fails: "a was at its share ... 0" |
| M11 | share without its floor of 1 | property test: "ta share 0, model 1" |
| M12 | lease start not reported | `TestFairAHeldTransactionCountsAsInFlightUntilItsLeaseEnds` |
| M13 | the limit not enforced | property test: "claim limit: tb popped with 1 in flight at limit 1" |
| M14 | removal of all-zero state skipped | `TestFairStateExistsOnlyWhileATenantHasLeasesAndIsBounded`: "fair state for "tba" kept with 0 in flight" |
| M15 | fairness keys not validated | `TestFairnessKeysFailClosedNamingTheKey` |

SQL files, each shown able to fail over real flushed runs (seeds 1-3): `quiet-against-alone.sql`
with the fair arm read as the fifo arm (3 violating rows); `flooder-shedding.sql` with the quiet
tenant as flooder (3); `fair-many-tenants/per-tenant.sql` over a copy of the runs with one quiet
tenant's final ok count lowered by one ("seed 1, tenant17: alone 2 ok of 2, with the flood 1 ok");
`held.sql` reading time at share of a tenant that does not exist (3); `fair-accounting.sql` with
"in flight below the limit" added as a violation (102); `fair-results/fair.sql` requiring 4
tenants (1 failure).

## Sweeps (200 distinct seeds)

SWEEP-RESULTS-PLACEHOLDER

## Observability (PLAN.md 7.6)

Component `admission` (always, totals): `limit`, `in_flight` (+`in_flight_peak`),
`active_tenants` (+peak), `fair_states` (+peak), `rounds`, `held{reason: limit, share}`,
`lease_starts`, `lease_ends`, `saturated_time` (ns). Per tenant with `obs.detail` (bounded tenant
label in Prometheus, every tenant up to 4,096 in CSV): `tenant_in_flight`, `tenant_share`,
`tenant_time_at_share` (ns), beside chunk 8's `tenant_served` and `tenant_shed{reason}` (shed
share = a tenant's sheds over all sheds, left to the reader). Conservation: per-tenant in flight
sums to the total, `lease_starts - lease_ends = in_flight`, and the total equals the engine's
leases outstanding at every event (`Engine.Invariants`) and in every flush (SQL). Counters move:
`checkFairMoved`, `TestFairCountersMoveAndConserveThroughTheMapping`.

**Coverage row (PLAN.md 7.5, Fairness):** per tenant in flight, share, served, shed (shed share),
time at share: all present; rate-gate refusals are chunk 10's. **Health:** no new rule (decision 9).

**Mechanism row:** where it sits: inside the admission core, at the choice of the next tenant,
between the queue stage and the lease. Numbers: the limit (`pool.connections`), the weights
(`fairness.weights`, `fairness.default_weight`), the cap on tenant state (leases in flight,
`pool.connections`). What the caller gets when it binds: nothing new, the request stays queued
and leaves by an exit of table (a). Signal: `tenant_time_at_share`, `saturated_time`, `rounds`,
`held`.

## Steady-state row

| What grows | What removes it | Gauge |
| --- | --- | --- |
| dispatcher state, one per tenant with a lease in flight (<= leases in flight <= `pool.connections`) | its last lease ending (removed at once; the slot is recycled) | `admission.fair_states` |
| turns (one integer in each tenant queue) | the tenant queue's removal (chunk 8) | `admission.tenants` |
| per-tenant time at share | never (cumulative, in chunk 8's 4,096-row table) | `admission.tenant_time_at_share` |

After quiesce: no dispatcher state, in flight 0, active tenants 0, no timer of this chunk
(there is none); `runFair` checks it.

## Fleet row (PLAN 8.2, 12.3 item 10)

| Guarantee | At N instances | Why | Measured in |
| --- | --- | --- | --- |
| Fair share between tenants | **Survives if tenants are spread evenly; weakens if a tenant's connections land on one instance** | Each instance computes shares among the tenants active on it, with its own limit (its `pool.connections`); a tenant pinned to one instance competes only there, and a flooder spread over all instances takes a share on each | Chunk 13 (a tenant pinned to one instance against tenants spread over three) |

## Shared files

Touched (all additive or one-line hooks): `internal/proxy/config.go` (field `Fairness`, one
validation line), `internal/proxy/engine.go` (dispatcher install, `fairRedispatch` at the top of
`finish`, the invariant block), `internal/proxy/lease.go` (lease start in `lend`, ends in `release`
and `leaseLost`, the plant hooks), `internal/proxy/corepool.go` (ends in `coreRelease` and
`coreDirty`), `internal/proxy/contracts.go` (`append` of the fairness capacity rows),
`internal/proxy/fairness.go` and `fairness_test.go` (new), `internal/proxy/join_test.go` (one test,
below); `internal/config/config.go` (section field), `internal/config/validate.go` (defaults line,
`Fairness` into `proxyCaps`), `internal/config/fairness.go` and `fairness_test.go` (new);
`internal/obs/admsrc/admsrc.go` (two hooks) and `fair.go`, `fair_test.go` (new);
`internal/obs/simrun/engine_test.go` (baseline and `-short` list); `test/e2e/realtier.go`
(`tierr-fair-noisy` in `TierRScenarios`); `scripts/fuzz-plant.sh` (one call and its header);
`agent_work/INDEX.md` (rows). Generated: `docs/capacity-table.md`, `docs/csv-schema.md`
(`scripts/gen-docs.sh`). Not touched: `internal/obs/catalog`, `internal/obs/enginesrc`,
`test/e2e/spec.go`, `test/e2e/simtier.go`, `scripts/chaos.sh`, `tools/lint-rules/*`
(`internal/admission` is already on the determinism and race lists).

## Changes to earlier chunks' tests

| Test | Change | Why |
| --- | --- | --- |
| `internal/proxy/join_test.go` `TestJoinHalfOpenTrialWithQueuedRequestsFromTwoTenants/trial-passes` | the trial connection now goes to tenant 1's `SELECT 12` and the next dial to tenant 0's `SELECT 11` (was the oldest request first) | round robin across tenants (PLAN chunk 9): tenant 0 had the last lease (client 10's `SELECT 1`), so tenant 1's queue, new, has the earlier turn. Both requests are still served, nothing shed, the breaker closes; only the order changed. Not weaker: the assertions on both requests, the counters and the breaker are unchanged |
| `internal/obs/simrun/engine_test.go` | `engineRuns` also runs `e2e.FairBaseline` (seed 1, outside `-short`); `shortSkipped` lists `e2e.FairScenarios()` | the CSV baseline test requires a run for every scenario directory with SQL files; under `-short` they are counted as skipped, like chunk 7's heavy and chunk 8's overload scenarios |

No other earlier test changed; every admission queue in every earlier scenario now runs the fair
dispatcher (with one tenant it chooses exactly as `OldestFirst`).

## Deviations

- Rule 4 (below-share tenants first at every lend) goes beyond PLAN's "share enforced only when
  the limit is reached". It never leaves a connection idle (only rule 3 can), so "work-conserving
  otherwise" still holds; without it the held-transaction claim failed (decision 2).
- Tests were written beside the code rather than strictly first; every mechanism has a test that
  fails with it removed (mutation table).
- The scenarios are capped at 10 seeds per call (ablation 3) because each seed costs 30-100 s on
  the loaded host; the 200-seed sweep was run seed by seed with `-seed=N` (section Sweeps).
- `fair-results` names a relaxed queue rule (interval 5 s, target 1 s) instead of the binary's
  default (see "Why the tier S scenarios hold").

## Stated limits

1. **The share bounds database concurrency, not database CPU**: it counts leases; a tenant whose
   statements are expensive uses more of the database per lease, and the dispatcher does not see it.
2. No preemption: leases taken while other tenants were inactive are kept until they end (a held
   transaction until it commits or until `timeouts.idle_in_transaction`).
3. Below the last lease a tenant at or above its share is lent when no tenant below its share
   waits, so a tenant can hold up to `limit - 1` leases while others are active only through leases
   in flight; their next request waits for the next lease to end and then goes first.
4. The last free lease is reserved for a tenant below its share: when only tenants at or above
   their share wait, one connection can idle until a lease ends or a below-share tenant arrives.
5. Round robin gives one turn per request whatever the weight; weights change the share only.
6. An over-share tenant can wait while below-share tenants keep arriving; it holds at least its
   share meanwhile. The starvation bound (claim 4) is within a group.
7. Time at share and saturated time are accounted at the core's operations and deadlines; a
   snapshot reports them up to the last operation (as chunk 8's ages).
8. Selection and time accounting cost O(live tenant queues + tenants with leases) per operation;
   the engine's fairness invariant is O(L^2) in lent connections per event (L <= pool size).
9. `Demand` counts held-back requests (decision 7).
10. Shares are per instance (fleet row).
11. The noisy-neighbour bound in tier S rests on the scenario's construction (quiet statements
    longer than one flooder lease); with quiet statements shorter than the flooder's, the quiet
    tenant's latency can more than double while it waits for one flooder lease.

## Fuzz targets

| Target | Package | Seed source | Decoding | Rate |
| --- | --- | --- | --- | --- |
| `FuzzFairnessOps` | `internal/admission` | none: the core takes no randomness; the input decodes directly (3 bytes per operation: kind, argument, time step) | at most 96 operations (push per tenant, pop and lend, pop and unpop, lease end, remove, limit change, time step up to 2.4 s, which reaches tenant queue removal and re-creation), trailing bytes ignored, a one-byte change alters one operation; each input runs 3 rules x 3 weight/limit variants, the FIFO arm against the exact model, all against the claims | 201/s unplanted (20 s, loaded host); does not import `internal/sim`, so the pinned simulator-target inventory (`agent_work/adversarial/cr01-shrink-fuzz-rules/`) is unchanged |

## Commands

```
go test ./internal/admission/... ./internal/proxy/... ./internal/config/... ./internal/obs/admsrc
go test -count=1 -run 'TestScenarioFair|TestCanaryFair' ./test/e2e          # tier S scenarios, seeds 1-3
RUN_DIR=/tmp/r go test -run '^TestScenarioFair' ./test/e2e                   # with flushed files
scripts/csv-assert.sh /tmp/r/fair-noisy-neighbour test/analysis/fair-noisy-neighbour/*.sql
go test ./test/e2e -run '^TestScenarioFairHeldTransactions$' -seed=42 -count=1   # one seed (REPLAY lines)
MG_TIER_R=1 go test -run '^TestTierRFairNoisyNeighbour$' -v ./test/e2e      # tier R (Docker)
go test ./internal/admission -run '^$' -fuzz '^FuzzFairnessOps$' -fuzztime 30s
scripts/fuzz-plant.sh
agent_work/chunks/c09-fairness/gate/extra.sh
scripts/gate.sh c09-fairness
```

## Index rows (added to `agent_work/INDEX.md`)

`scripts/fuzz-plant.sh` (changed), `chunks/c09-fairness/gate/extra.sh`, `docs/knowledge/fairness.md`.

## Gate result

GATE-RESULT-PLACEHOLDER
