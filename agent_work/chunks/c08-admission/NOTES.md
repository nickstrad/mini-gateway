# Chunk 8 — Admission queues: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c08-admission`, worktree
`/root/Software/mini-gateway-work/c08`. No helper agents.

No SPLIT-REQUEST: every deliverable of the brief is built. Product Go added: about 2,060
lines (core 1,180, `admsrc` 160, proxy wiring 190, config 80, the non-test e2e world 290, the
rest small edits); test Go added: about 2,100 lines. Inside the ×2 budget of PLAN.md's 1,300.

## Simulator change requests

None. The frozen fake has no per-statement-kind exemption for the reset, so the scenarios
give the service time to `KindRead` only (`fakepg.Service.PerKind`); with `Service.Fixed`
the fake also charges it to every `DISCARD ALL` and halves the capacity.

## What was built

| Path | What it is |
| --- | --- |
| `internal/admission` | The core, sans-I/O and determinism-linted: per-tenant fixed-capacity rings created on first use and removed after 16 quiet intervals (rings recycled through a free list); the tenant-queue cap; `QueueRule` with `TimeoutRule`, `ControlLaw` and the two simulator arms `fifoRule` and `FollyRule`; head sweep at every operation and at the core's own next deadline; adaptive LIFO with a hold; overflow sheds the head (per tenant) or the globally oldest (total); the folly minimum-delay load gauge 0–100; `Snapshot()` with per-tenant rows; `Check()`, `Quiesce()`; the `Dispatcher` seam with `OldestFirst`. |
| `internal/proxy/admission.go`, `seams.go` (appended) | `AdmissionQueue` (the `Queue` seam backed by the core) and the additive `ShedQueue` extension (`RemoveAt`, `Expire`, `Sweep`, `Next`, `TakeShed`, `Unpop`); `Engine.queueTouched` answers every shed with `53M01` and re-arms the engine's one admission timer (`tAdmission`); `Engine.AdmissionSnapshot()`; core checks in `Invariants()` and `Quiesce()`. `FIFO` is unchanged and stays the default when `Config.Admission.Rule == 0`. |
| `internal/proxy/lease.go`, `client.go`, `engine.go`, `timers.go`, `config.go`, `contracts.go` | Small edits: queue stage calls, the timer kind, `Config.Admission`, validation, rows of the timeout and capacity documents. |
| `internal/config/queue.go` (+ `config.go`, `validate.go` one line each) | `queue.rule`, `queue.target`, `queue.interval`, `queue.tenant_capacity`, `queue.max_tenants`, `queue.lifo`, `queue.lifo_hold`; defaults; fail closed naming the key. |
| `internal/obs/admsrc` | Component `admission` mapping (totals always; per-tenant rows and histograms with `obs.detail`), health rule `admission`. Registered through `enginesrc.Sources` and `catalog.Registry`. |
| `test/e2e/c08_admission.go`, `c08_admission_test.go`, `c08_tierr_test.go` | The admission world (`RunAdmission`: open-loop workload through the real engine on the simulator, CSV flush), the head-to-head, anti-bloat, recovery, first-burst and canary scenarios, tier R overload. |
| `test/analysis/admission-*`, `tierr-admission-overload` | SQL assertions per scenario; `admission-head-to-head/head-to-head.sql` and its committed output `head-to-head.md`. |
| `scripts/admission-head-to-head.sh` | Prints the table (one DuckDB query); `--check` reruns the scenarios and diffs with the committed table. |
| `scripts/fuzz-plant.sh` (appended) | Also requires `FuzzAdmissionOps` to find `PlantRemoveSwapsTail`. |
| `agent_work/chunks/c08-admission/gate/extra.sh` | G10: `fuzz-plant.sh` and `admission-head-to-head.sh --check`. |
| `docs/knowledge/admission-queues.md` | The short knowledge note. |

## Decisions the brief left open

1. **Allowed wait is evaluated at the instant, on the oldest item** (not fixed per request at
   enqueue as in "Fail at Scale"'s pseudo-code). One allowed wait applies to the whole tenant
   queue at a given instant, so the head is always the most expired item and the head sweep is
   complete. The reference model checks this against every item, not just the head.
2. **Standing queue** = `now - LastEmpty >= interval` (equality counts, as for deadlines).
   `LastEmpty` is set at a push into an empty queue and whenever the queue becomes empty.
3. **The control law is timer driven.** The RFC's `dodequeue` checks run at every queue
   operation and at the core's next deadline (`head.enq + target`, `first_above_time`,
   `drop_next`), so its decisions do not depend on when the dispatcher pops. A "drop" sheds
   the oldest request. The RFC's "one MTU in the queue" exemption is not built (one request is
   not a byte count). Re-entry within 16 intervals keeps `count - lastcount` (Linux variant).
4. **Integer control law: exact floor, not the Linux Newton step.** `IntervalOverSqrt` finds
   the largest `d` with `d² · count ≤ interval²` using 128-bit products (`math/bits`), so it
   matches a `math/big` reference exactly for every count up to `MaxDropCount = 65,536`
   (the count saturates there). PLAN.md D2 names the Linux reciprocal-sqrt step; that step is
   an approximation whose result depends on history, and an exact floor is cheaper to prove.
5. **Capacities.** `queue.capacity` (chunk 6's key, default 256) is now the total across the
   tenant queues; `queue.tenant_capacity` (default min(256, capacity)) is one ring;
   `queue.max_tenants` (default 1,024) caps the rings. Memory is fixed by tenant capacity × the
   tenant cap. Total full → the globally oldest request is shed (`total`); tenant ring full →
   its head is shed (`overflow`); tenant cap reached → an empty tenant queue is evicted, else
   the arrival is shed (`tenants`). All of them are `53M01`; the reason is in the counters.
6. **Quiet removal** after 16 intervals empty (the control law's re-entry memory), with
   adaptive LIFO off; no extra key.
7. **LIFO hysteresis** is a time hold (`queue.lifo_hold`, default 1 s): LIFO switches on at the
   first observation of overload and off only after the rule reported no overload for the
   hold. `queue.lifo: adaptive | off` selects it. The switch is evaluated at every operation
   and at the core's deadlines, and while overloaded no LIFO timer is armed.
8. **`timeouts.queue_wait` stays** as the outer backstop for every rule (the control law has
   no per-request bound). With the admission queue its expiry is `Expire` (shed reason
   `deadline`).
9. **The engine's direct lend is skipped for a `ShedQueue`:** every request is pushed and
   popped, so the rule and the gauge see zero-wait requests too.
10. **Default dispatcher `OldestFirst`**: the tenant whose head is oldest; that tenant then pops
    newest or oldest by its own LIFO state. **Chunk 9 plugs in** by implementing
    `admission.Dispatcher` and calling `Queues.SetDispatcher` (reach it through
    `AdmissionQueue.Core()`).
11. **`Unpop`**: if the pool refuses a checkout after a pop, the request goes back to its place
    (counted `unpops`; its served-wait sample stays in the histogram). `FixedSet` never refuses
    when `Idle() > 0`.
12. **Load gauge**: folly's `min(100, 100 × minDelay / (2 × target))`, the minimum taken over
    served waits per interval exactly as folly does it (so it shares folly's blindness under
    LIFO; it is a gauge, not a decision input).
13. **Comparison arms** live in the core and are reachable through `proxy.Config.Admission`
    (tests and the simulator); `admission.ParseProductRule` accepts only `timeout` and
    `control-law`, and `internal/config` uses only that parser.
14. **Health**: `degraded` while any tenant queue shed within the last interval (the core
    arms a deadline at `shed + interval` so the gauge falls without traffic).
15. **Per-tenant counters persist** after their queue is removed (CSV counters never decrease),
    in a table of at most `MaxTenantStats = 4,096`; later tenants are counted as `other`.
16. **Chunk 6 tier R tests run with a relaxed rule** (`RelaxedQueue`, interval 5 s, target 1 s),
    because the binary now defaults to the timeout rule and those tests queue for longer than
    100 ms on purpose (pgbench, results-equal-direct, silent database, …). `TestTierRQueueFull`
    names the same values explicitly.
17. **Scenario scale**: 1 connection, 5 ms reads (one lease ≈ 5.5 ms with the reset, so
    capacity ≈ 181/s), 100 open-loop clients, one tenant; T = 2 s, 4T = 8 s. The overload
    scenarios use one seed whatever `DST_SEEDS` says (each run is 1–4 s); the first-burst
    scenario is swept.

## Claims and the tests that prove them

| # | Claim | Tests |
| --- | --- | --- |
| 1 | Bounded: depth ≤ capacity per tenant, tenant queues ≤ cap, memory fixed, quiet queue removed | `internal/admission`: `TestCoreAgreesWithTheReferenceModelForEveryRule` (after every op `Queues.Check`: ring ≤ tenant capacity, total ≤ capacity, tenants ≤ cap), `TestTenantCapShedsTheArrivalOnlyWhenNoQueueIsEmpty`, `TestQuietTenantQueueIsRemovedAndItsRingReused` (200 tenants through a cap of 4, rings ≤ cap); `test/e2e`: `checkHeadToHead` (depth peak ≤ capacity), `runAdm` steady state (no tenant queue after quiesce); engine `Invariants()` |
| 2 | Anti-bufferbloat, timeout rule: served wait < interval, depth equal at T and 4T, FIFO control grows | core: `claims()` in every property run (no served wait ≥ interval); `TestLIFOWithoutTheHeadSweepBuriesStaleRequests`; e2e `TestScenarioAdmissionAntiBloat/timeout` + `/fifo` (FIFO control backlog at 4T ≥ 3× T); SQL `admission-anti-bloat/admission-depth.sql`, `admission-wait.sql`; tier R `TestTierRAdmissionOverloadWithPgSleep/timeout` |
| 3 | Anti-bufferbloat, control law: depth bounded by the hard capacity, its bound = capacity × lease | e2e `TestScenarioAdmissionAntiBloat/control-law` (peak equal at T and 4T, served wait < capacity × lease + interval); SQL `admission-depth.sql`; table: at 1.2× p50 2.5 ms (near target), at 10× the wait is bounded by the ring, not by the law |
| 4 | Shedding never idles the backend: served ≥ 95% of capacity at 10× | e2e `TestScenarioAdmissionHeadToHead` (`checkHeadToHead`: served ≥ 95% of the never-idle FIFO arm at 2× and 10×; the FIFO arm itself ≥ 95% of 2 s / 5.5 ms); tier R (≥ 70% of the run that sheds only on overflow, a real-hardware threshold) |
| 5 | The first burst after idle gets the interval, both rules | core `TestFirstBurstAfterIdleGetsTheIntervalNotTheShortTarget`; e2e `TestScenarioAdmissionFirstBurst/{timeout,control-law}` (swept); canaries below |
| 6 | Expired items never linger; none served late or buried, LIFO and FIFO alike | core `claims()` after every op (no queued item at or past its allowed wait), the reference model's all-items expiry check, `TestDeadlineAtEqualityHasExpired`, `TestLIFOWithoutTheHeadSweepBuriesStaleRequests`; engine `TestAdmissionShedByTheRuleIsAnsweredAtOnceWithItsRowAndEndMarker`, `TestAdmissionArmsItsNextDeadline` |
| 7 | Overflow sheds the head, not the arrival | core `TestOverflowShedsTheHeadNotTheArrival`; engine `TestAdmissionOverflowShedsTheQueuedHeadNotTheArrival` |
| 8 | Every shed answered at once with 53M01 and an end marker, counted by reason; conservation on every path | engine `TestAdmissionShedByTheRuleIsAnsweredAtOnceWithItsRowAndEndMarker`, `TestAdmissionConservesOnEveryExitPath` (served, removed, deadline); core `Snapshot.Check` (pushes = served + Σshed + removed + queued) in every property op; `admsrc` `TestCountersMoveAndConserveThroughTheMapping`; SQL `admission-conservation.sql` (all scenarios, incl. engine shed = admission shed) and `admission-h2h-l1000/admission-sheds.sql` (53M01 replies = sheds); tier R shed row ≥ client-seen 53M01 |
| 9 | LIFO hysteresis: switches only at the thresholds, no flapping at the threshold | core `TestLIFOHysteresisDoesNotFlapAtTheThreshold` (1 on, 0 off over 40 cycles at the threshold), `TestLIFOSwitchesOffOnlyAfterTheHold`; reference model agreement on every LIFO state |
| 10 | Integer control law matches the reference for every count up to the maximum | core `TestIntervalOverSqrtMatchesTheReferenceForEveryCountUpToTheMaximum` (every count 1..65,536 for five intervals, against `math/big`), `TestControlLawCountAndNextDropAcrossLeavingAndReentering`; detlint (no floating point) |
| 11 | Recovery: rule leaves overload within interval + hold + interval, wait back to uncongested | e2e `checkRecovery` in `TestScenarioAdmissionAntiBloat/{timeout,control-law}`: 1.2 s after the overload all tenant queues normal and LIFO off; the 0.3× load after it is never shed and its latency ≤ 4 leases |
| 12 | Comparison arms cannot be selected by the binary's config | core `TestComparisonArmsAreNotProductRules`; config `TestBinaryConfigCannotSelectAComparisonArm`, `TestQueueKeysFailClosedNamingTheKey` (`fifo`, `folly` refused) |
| 13 | The head-to-head table is reproducible from one query over flushed files | `scripts/admission-head-to-head.sh --check` (gate extra) reruns the scenarios and diffs the query output with `head-to-head.md`; `runAdm` (files byte-identical per seed) |
| 14 | Replay and flush | `runAdm` and `runScenario`: two runs per seed, identical digests and flushed files, `Flush.Check` round trip |
| 15 | `FuzzAdmissionOps`: bounded total decoding, no seed from input, finds the planted bug | `TestFuzzDecodingIsTotalBoundedAndLocal`, `TestFuzzExecutionCostIsBounded`, `TestPlantedRemoveOrderBugIsCaughtByTheOperationModel`; `scripts/fuzz-plant.sh` (found in 7 s of a 20 s budget, empty cache) |

## Canaries and ablations (test-only `admission.Plant` bits; `internal/config` never sets them: `TestConfigNeverSetsAnAdmissionPlant`)

| Switch | Must fail | Test |
| --- | --- | --- |
| `PlantNoHeadSweep` | served wait < interval; nothing lingers | core `TestLIFOWithoutTheHeadSweepBuriesStaleRequests`; e2e `TestCanaryAdmissionWithoutTheHeadSweepServesLate` |
| `PlantFirstBurst` (`LastEmpty` set only when the queue empties) | first burst gets the interval | core `TestCanaryFirstBurstBugShedsTheFirstBurst`; e2e `TestCanaryAdmissionFirstBurstBugShedsTheBurst` |
| `PlantNoLIFOHysteresis` | no flapping | core `TestLIFOHysteresisDoesNotFlapAtTheThreshold` (≥ 10 switches planted) |
| `PlantRemoveSwapsTail` (fuzz ordering bug) | reference-model order | `FuzzAdmissionOps` via `scripts/fuzz-plant.sh`; `TestPlantedRemoveOrderBugIsCaughtByTheOperationModel` |
| Ablation: rule off (`RuleFIFO`) | standing queue persists | e2e `TestScenarioAdmissionAntiBloat/fifo` (max wait ≥ interval, depth ≥ half the clients at 4T, client backlog grows) |

## Fuzz targets

| Target | Package | Seed source | Decoding | Rate |
| --- | --- | --- | --- | --- |
| `FuzzAdmissionOps` | `internal/admission` | none: the core takes no randomness; the input decodes directly (3 bytes per op: kind, tenant/pick, time step) | at most 64 ops, trailing bytes ignored, one-byte change alters one op; every op runs against the reference model for all four rules on a small config (tenant cap 3, total 5, 3 tenants) | about 137/s on vm-1 (not simulation-level) |

The pinned fuzz inventory in `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`
counts simulator-level targets; this target does not import `internal/sim`. Whether
`TestAdv_6_GateSeedSweepCounts` / `TestAdv_7_InventoryAndHiddenHashImports` go red is
recorded in "Gate result".

## The head-to-head table (`test/analysis/admission-head-to-head/head-to-head.md`, seed 1)

Load is offered load against the measured capacity (≈181 leases/s). Waits are bucket upper
bounds of the final served-wait histogram; served, shed and depth at the end of the 2 s
overload phase; idle = share of the phase the connection served nothing.

| load | arm | p50 ms | p99 ms | max ms | depth | served | shed | by rule | idle % |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1.2 | timeout | 2.44 | 19.92 | 55.05 | 5 | 336 | 64 | 64 | 7.6 |
| 1.2 | control-law | 2.46 | 25.43 | 121.63 | 14 | 338 | 62 | 62 | 7.0 |
| 1.2 | fifo | 143.65 | 170.92 | 174.06 | 32 | 363 | 35 | 0 | 0.2 |
| 1.2 | folly | 3.42 | 1149.24 | 1149.24 | 32 | 344 | 56 | 0 | 5.4 |
| 2 | timeout | 2.34 | 54.79 | 88.08 | 19 | 359 | 341 | 341 | 1.3 |
| 2 | control-law | 2.36 | 187.70 | 221.25 | 32 | 363 | 325 | 104 | 0.2 |
| 2 | fifo | 88.08 | 165.68 | 174.06 | 32 | 363 | 318 | 0 | 0.2 |
| 2 | folly | 2.44 | 310.38 | 343.93 | 32 | 363 | 318 | 0 | 0.2 |
| 10 | timeout | 0.22 | 17.69 | 17.69 | 32 | 361 | 3239 | 3108 | 0.7 |
| 10 | control-law | 0.24 | 104.33 | 122.68 | 32 | 363 | 3208 | 104 | 0.2 |
| 10 | fifo | 17.43 | 162.53 | 177.21 | 32 | 363 | 3207 | 0 | 0.2 |
| 10 | folly | 0.25 | 110.10 | 128.97 | 32 | 363 | 3207 | 0 | 0.2 |

What it says, in numbers: the timeout rule is the only arm whose served wait stays under the
interval at every load (max 88 ms at 2×); the control law drops 104 requests by its law at 2×
and 10× and leaves the rest to the ring's overflow (its 10× tail is the ring, 32 × 5.5 ms); the
plain FIFO ring holds a standing 32-deep queue (p50 88–144 ms at 1.2–2×); folly with adaptive
LIFO never sheds by its rule at 2× and 10× and starves the oldest (p99 1.15 s at 1.2×). The
cost of the timeout rule's saw-tooth at moderate load: at 1.2× it shed 64 requests while the
connection idled 7.6% of the phase (claim 4 is stated for 10×, where it idles 0.7%).

## Observability

Component `admission`: `rule{mode}`, `lifo_adaptive`, `capacity`, `per_tenant_capacity`,
`max_tenants`, `queued` (+peak), `tenants` (+peak), `tenants_created/removed/evicted`,
`pushes`, `served`, `removed`, `unpops`, `shed{reason}`, `pending_sheds`, `oldest_age`,
`lifo_tenants`, `lifo_switches{mode}`, `rule_states{mode}`, `load`, `shedding`, `queue_wait`
histogram; per tenant (with `obs.detail`): `tenant_depth`, `tenant_capacity`,
`tenant_oldest_age`, `tenant_rule_state{mode}`, `tenant_lifo`, `tenant_lifo_switches{mode}`,
`tenant_pushes/served/removed`, `tenant_shed{reason}`, `tenant_load`, `tenant_shedding`,
`tenant_queue_wait`. Prometheus tenant label bounded by promtext (32 + `other`). Health:
`admission` degraded while any queue shed in the last interval. Coverage row (PLAN.md 7.5,
back pressure and queues): depth, capacity, oldest age, queue-wait histogram, rule state,
LIFO on/off and switches, sheds by reason — all present.

## Steady-state row

| What grows | What removes it | Gauge |
| --- | --- | --- |
| tenant queues (≤ `queue.max_tenants`, rings recycled) | 16 quiet intervals, or eviction at the cap | `admission.tenants` |
| queued requests (≤ `queue.capacity`, ≤ tenant capacity each) | serve, shed, removal | `admission.queued` |
| per-tenant counters (≤ 4,096, then `other`) | never (cumulative by design) | `admission.tenant_pushes` rows |
| the admission timer | fires or is cancelled when `Next() == 0` | `engine.timers_pending` |

After quiesce every e2e run checks: no tenant queue, nothing queued, LIFO off, nothing shedding,
no admission timer (`Engine.Quiesce`).

## Stated limits

1. Tenant selection, `Next()` and the sweep cost O(live tenant queues) per operation.
2. LIFO switch-on is observed at operations and deadlines; when standing begins between
   them, the switch is recorded at the next operation (the pop decision is always current).
3. The load gauge shares folly's blindness under LIFO (it reads served waits).
4. A real tenant named `other` shares the fold-over counters once 4,096 tenants have been seen.
5. `Unpop` leaves the served-wait sample in the histogram.
6. The overload scenarios are exact on seed 1 only; the sweep runs them with one seed.
7. Tier R's idle-capacity check is a 70% threshold against a run without the rule on the same
   host; measured 73–74/s against 84/s while two other builds ran.

## Deviations

- Tests were written alongside the code rather than strictly first; every mechanism has a test
  that fails with it removed (the four plants and the reference model).
- The control law uses an exact integer floor instead of the Linux Newton step (decision 4).
- "Depth equal at T and 4T" is asserted on the peak queue depth (and the clients' backlog not
  growing): the instantaneous depth at a flush instant fluctuates by one or two requests.
- The FIFO control "grows without bound" in the clients' backlog (one request per client
  connection reaches the proxy, D5), shown as backlog at 4T ≥ 3× backlog at T.
- `make chaos` does not list the admission scenarios; they run in `go test`, the sweep and
  the gate extra.

## Commands

```
go test ./internal/admission/... ./internal/obs/admsrc ./internal/proxy/... ./internal/config/...
go test -run 'Admission' ./test/e2e                              # tier S admission scenarios (~3 min)
RUN_DIR=/tmp/r go test -run '^TestScenarioAdmission' ./test/e2e  # with flushed files
scripts/csv-assert.sh /tmp/r                                     # SQL assertions
scripts/admission-head-to-head.sh /tmp/r                         # the table; --check to reproduce
go test ./internal/admission -run '^$' -fuzz '^FuzzAdmissionOps$' -fuzztime 30s
scripts/fuzz-plant.sh
MG_TIER_R=1 go test -run '^TestTierR' ./test/e2e                 # tier R incl. admission overload
scripts/gate.sh c08-admission
```

## Index rows (added to `agent_work/INDEX.md`)

`scripts/admission-head-to-head.sh`, `scripts/fuzz-plant.sh` (changed),
`chunks/c08-admission/gate/extra.sh`, `docs/knowledge/admission-queues.md`.

## Gate result

(filled in after the run)
