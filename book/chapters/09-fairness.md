# 9. Fairness that binds only when it must

Per-tenant queues stop a noisy tenant from overflowing a quiet tenant's buffer, but they do not stop it from taking every database connection. Under Chapter 8's default `OldestFirst` dispatcher, whenever a connection frees, the proxy serves whichever tenant has the oldest queued request. A flooding tenant always has older queued requests waiting than a quiet tenant that sends one query every 25 ms, and a tenant holding open transactions can absorb every freed connection unless its open leases count against it.

Under a single shared FIFO queue on 2 backend connections—one flooder at 10× capacity sending 5 ms reads beside one quiet tenant sending a 10 ms `UPDATE` every 25 ms—the quiet tenant's p99 latency jumps from **11.08 ms** alone to **2.58 s** (233×), with 2 of its 88 requests shed (`53M01`). With the fair dispatcher installed on the per-tenant rings, the same seed completes all 88 quiet requests with zero errors and a p99 of **14.88 ms** (1.34× standalone), while the flooder takes 100% of the shedding.

Chapter 9 installs per-tenant weighted fair dispatching behind Chapter 8's `Dispatcher` seam across five areas:

- [`internal/admission/fair.go`](../../internal/admission/fair.go) (plus hooks in [`queues.go`](../../internal/admission/queues.go), [`snapshot.go`](../../internal/admission/snapshot.go), and [`tenant.go`](../../internal/admission/tenant.go)): `Fair`, a pure **sans-I/O** (no clocks, sockets, goroutines, or floating point) dispatcher enforcing weighted shares over one settable concurrency limit (`SetLimit`), two-group round robin with exact `Unpop` restoration, per-tenant lease accounting that includes held transactions, recycled fixed-bound dispatcher state, and `Queues.Releasable()` for pool demand.
- [`internal/proxy/fairness.go`](../../internal/proxy/fairness.go) (plus one-line hooks in [`engine.go`](../../internal/proxy/engine.go), [`lease.go`](../../internal/proxy/lease.go), [`corepool.go`](../../internal/proxy/corepool.go), [`config.go`](../../internal/proxy/config.go), and [`contracts.go`](../../internal/proxy/contracts.go)): installs `Fair` on every engine-constructed `AdmissionQueue` (default limit `pool.connections`), reports every lease start and every lease-end path to the core, drives `pool.Demand` from `Queues.Releasable()`, retries held-back work once per event (`fairRedispatch`), and checks per-tenant lease conservation inside `Engine.Invariants`.
- [`internal/config/fairness.go`](../../internal/config/fairness.go): fail-closed JSON configuration for `fairness.default_weight` and `fairness.weights`, plus fairness rows in [`docs/capacity-table.md`](../../docs/capacity-table.md).
- [`internal/obs/admsrc/fair.go`](../../internal/obs/admsrc/fair.go): component `admission` fairness totals and per-tenant `obs.detail` metrics through the bounded tenant label ([`docs/csv-schema.md`](../../docs/csv-schema.md)).
- [`test/e2e/c09_fairness.go`](../../test/e2e/c09_fairness.go), [`test/e2e/c09_fairness_test.go`](../../test/e2e/c09_fairness_test.go), [`test/e2e/c09_tierr_test.go`](../../test/e2e/c09_tierr_test.go), and eight DuckDB SQL assertions under `test/analysis/`: tier S noisy-neighbour, single-FIFO ablation, 40-quiet-tenant, held-transaction, and results-equal-direct scenarios, plus tier R noisy-neighbour against live Postgres 17.

**What stays ahead.** The concurrency limit that `Fair` divides is one settable integer, fixed at `pool.connections` until the adaptive **AIMD** (additive-increase, multiplicative-decrease) limit drives `SetLimit` from database round-trip time (Chapter 10). Request coalescing, retries, and multi-instance fleet measurements remain separate stages.

---

## Design: work-conserving shares without stranding a connection

A fair scheduler in front of a transaction pool faces two opposing failure modes. Hard static per-tenant caps (`limit / configured_tenants`) isolate a quiet tenant cleanly, but they waste database connections whenever only one tenant is busy. Pure round robin across waiting queues stays **work-conserving** (never leaving a connection idle while work waits), yet it ignores how many connections each tenant already holds: a tenant whose clients hold open transactions (`BEGIN ...` with think time before `COMMIT`) still gets every alternate turn and climbs past its share.

`Fair` resolves both tensions with five rules, using integer arithmetic only:

1. **Active set and dynamic share.** A tenant is **active** while it has at least one request waiting in its queue *or* at least one lease in flight (an empty tenant queue sitting in its 16-interval quiet window is not active). Each active tenant's share is:

   ```text
   share = max(1, floor(limit × weight / sum_of_active_weights))
   ```

   Weights default to `1` (`fairness.default_weight`, range `1..1,000`, at most `4,096` entries in `fairness.weights`). When a tenant is alone, its active weight equals the sum and its share is the full `limit`. When active tenants outnumber `limit`, every active tenant gets a floor share of `1` while the total in flight still obeys `limit`.
2. **Hard limit gate.** Nothing is lent while `in_flight >= limit` (counted as `held{reason="limit"}`).
3. **Below-share priority before round robin.** A waiting tenant is **below its share** when `in_flight + 1 <= share`. At every lend, `Fair.Pick` chooses a waiting tenant below its share ahead of any waiting tenant at or above its share. Within each group, selection is **round robin**: the tenant served least recently (smallest `Turn`) goes first, breaking ties by oldest queued request (`OldestSeq`).
4. **Last-slot reservation.** If the choice takes the last free lease (`in_flight + 1 == limit`) and the chosen tenant is at or above its share, `Fair.Pick` holds that lease back (`held{reason="share"}`) *only* when some active tenant with nothing waiting holds fewer leases than its share (`in_flight < share`)—for example, a **closed-loop** client (one that waits for each reply before sending the next request) between two queries. When no active tenant is below its share, the last lease is lent by round robin like any other.
5. **Lease-lifetime accounting, no preemption.** A tenant's `in_flight` increments when the engine lends a connection (`Engine.lend`) and decrements where the engine counts `LeasesEnded`—clean release (before `DISCARD ALL`), entry to the dirty-connection clean-up table, connection loss, or shutdown. Open transactions keep their lease between statements, so they remain charged to their tenant's share. Existing leases are never revoked when another tenant arrives or when `SetLimit` lowers the limit; shares constrain new lends only.

**Why guard the last-slot hold with `reserved(w)` instead of blocking every over-share waiter.** Integer floor division frequently sums below `limit`: with `limit = 3` and two equal-weight tenants, each gets `floor(3 × 1 / 2) = 1`. Once both tenants hold 1 lease, neither is below its share (`1 + 1 > 1`). An unconditional last-slot block would reject both tenants and leave the third connection permanently idle while both have work queued. Checking `reserved(w)`—whether any active non-waiting tenant actually sits below its share—keeps the reservation for a quiet client in the gap between requests without ever stranding rounding remainders.

**Why fairness adds no queue and no error code.** Under decision D3 ([PLAN.md](../../PLAN.md#3-design-decisions-this-plan-makes)), a request waits in one place only: its tenant's admission ring. A request held back by `Fair` stays in its ring and leaves through the same exits as any other queued request—served when a connection frees, shed with `ERROR 53M01` by the tenant's **CoDel** (Controlled Delay) rule or `timeouts.queue_wait`, flushed with `ERROR 53M03` if the circuit breaker opens, or removed on cancel, client disconnect, or shutdown.

---

## How it works: selection, turn order, and releasable demand

### Selection and turns

The entire selection decision sits in `Fair.reserved` and `Fair.Pick` in [`internal/admission/fair.go`](../../internal/admission/fair.go):

```go
func (f *Fair) reserved(w int64) bool {
	for _, t := range f.list {
		if !f.waiting(t.name) && t.inFlight < f.share(t.name, w) {
			return true
		}
	}
	return false
}

func (f *Fair) Pick(_ Time, active []TenantView) int {
	// Save pick count and held state so an immediate Unpop restores them exactly.
	f.saved = fairSaved{ok: true, picks: f.c.picks, held: f.held}
	f.held = true
	if f.total >= f.cfg.Limit {
		f.c.heldLimit++
		return -1
	}
	w, _ := f.activeWeight()
	best, bestUnder := -1, false
	for i, v := range active {
		under := !f.shareOn() || f.TenantInFlight(v.Name)+1 <= f.share(v.Name, w)
		if best < 0 || under && !bestUnder || under == bestUnder && before(v, active[best]) {
			best, bestUnder = i, under
		}
	}
	if best >= 0 && !bestUnder && f.lastSlot() && f.reserved(w) {
		best = -1
	}
	if best < 0 {
		f.c.heldShare++
		return -1
	}
	f.c.picks++
	f.held = false
	return best
}
```

Round-robin turns live on each `tenantQueue` (`turn`) beside a monotonic `Queues.round` counter ([`internal/admission/queues.go`](../../internal/admission/queues.go), [`fair.go`](../../internal/admission/fair.go)):

- Serving a pop increments `q.round` and assigns the chosen tenant `turn = 2 * q.round`.
- Creating a new tenant queue assigns `turn = 2 * q.round - 1`. Because every previously waiting tenant has a turn `<= 2 * (q.round - 1)` and the tenant served last has `2 * q.round`, a newcomer lands behind every already-waiting tenant in its group and ahead only of the tenant served last.
- Undoing a pop via `Queues.Unpop()` (when the pool defers checkout for an off-path health check or switch reset) restores the popped tenant's previous `turn`, `q.round`, `f.c.picks`, and `f.held`, leaving dispatcher state identical to never having popped.

Per-tenant dispatcher state (`fairTenant`) exists *only* for tenants currently holding at least one lease. When a tenant's `inFlight` returns to zero in `Fair.end`, `Fair.drop` deletes it from `byName` and `list` and recycles the struct onto `free`. Dispatcher state is therefore bounded by `in_flight <= MaxLeases` (`pool.connections`), no matter how many thousands of tenants pass through the proxy. Time at share (`tenant_time_at_share`) and saturated time (`saturated_time`) follow the package's deadline law: `Queues.advance` calls `q.fairAccount(at)` *before* stepping any internal sweep or eviction at timestamp `at`, so durations reflect the exact virtual instant when an active tenant leaves or a lease ends.

### Releasable demand and engine wiring

Passing raw `Queue.Len()` to Chapter 7's `pool.Demand` breaks under fairness in two ways: a request held back by the last-slot reservation causes the pool to dial a replacement connection every time an idle spare closes at `pool.idle_timeout`, and requests held at a fair limit below `pool.connections` cause the pool to dial up to `pool.connections` instead of `limit`.

`Queues.Releasable()` ([`internal/admission/fair.go`](../../internal/admission/fair.go)) computes the exact number of waiting requests successive `Pick` + `LeaseStart` calls would lend right now, without mutating queue or dispatcher state:

```go
func (f *Fair) releasable() int {
	free := f.cfg.Limit - f.total
	n := min(f.q.total, max(free, 0))
	if n == 0 || n < free || !f.shareOn() {
		return n
	}
	w, _ := f.activeWeight()
	absorbed, reserved := 0, f.reserved(w)
	for _, t := range f.q.list {
		if t.empty() {
			continue
		}
		deficit := max(0, f.share(t.name, w)-f.TenantInFlight(t.name))
		absorbed += min(deficit, t.ring.len())
		// A tenant whose deficit exceeds its waiting depth will become an
		// active non-waiting below-share tenant once its queue drains.
		reserved = reserved || deficit > t.ring.len()
	}
	if absorbed >= free || !reserved {
		return free
	}
	return free - 1
}
```

Because moving a request from a tenant's queue into an active lease leaves that tenant in the active set, `activeWeight` and every tenant's share stay invariant across a burst of immediate lends. Summing each waiting tenant's below-share deficit tells the pool in one pass whether all `free` slots can be filled or whether the last slot will be held back.

Four small hooks in `internal/proxy` wire the dispatcher into `proxy.Engine`:

- **Installation and validation:** `proxy.New` installs `Fair` on every `AdmissionQueue` it builds (`internal/proxy/engine.go`), defaulting `Limit` and `MaxLeases` to `Connections`. `File.Validate` (`internal/config/validate.go`) checks `fairness.default_weight` and `fairness.weights` alongside every other section, refusing negatives, zero-name tenants, duplicates, weights above `1,000`, and tables above `4,096` entries with errors naming the exact key.
- **Lease reporting:** `Engine.lend` calls `fairStarted`; `release`, `coreRelease`, `coreDirty`, and `leaseLost` each call `fairEnded` beside `LeasesEnded++` (`internal/proxy/lease.go`, `corepool.go`).
- **Event-driven redispatch without timers:** `Engine.finish()` calls `fairRedispatch()` once per event when `q.Held()` is true, work is queued, and an idle connection exists (`internal/proxy/fairness.go`). Holding a request back adds no polling timer, no pop, no unpop, and no checkout until an event makes it eligible.
- **Pool demand and invariants:** `Engine.corePump()` passes `e.releasable()` to `pool.Demand`. When `q.Limit() < e.cfg.Connections`, `CorePool.Checkout` also refreshes demand via `Engine.checkoutDemand()` before `Pool.Checkout` checks whether to dial. `Engine.Invariants()` runs `fairnessCheck()` after every simulator step, verifying that `q.InFlight() == LeasesStarted - LeasesEnded`, every lent tenant's in-flight count equals the engine's lent connections for that tenant, and `Fair.States()` equals the number of distinct tenants holding leases.

`internal/obs/admsrc/fair.go` exports twelve component-level metrics (`limit`, `in_flight`, `in_flight_peak`, `active_tenants`, `active_tenants_peak`, `fair_states`, `fair_states_peak`, `rounds`, `held{reason="limit"|"share"}`, `lease_starts`, `lease_ends`, `saturated_time`) and three per-tenant metrics under `obs.detail` (`tenant_in_flight`, `tenant_share`, `tenant_time_at_share`) through Chapter 8's bounded tenant label (capped at 32 in Prometheus, 4,096 in CSV). No new health rule is added: share binding is expected behaviour under saturation, while sustained queueing that causes shedding already sets the `admission` health rule to `degraded`.

---

## How it is tested and what the tests proved

Fairness is verified across three layers: a pure core reference model and fuzzer, deterministic tier S scenarios with DuckDB SQL assertions over flushed CSV files, and tier R against live Postgres 17.

### Core reference model, exhaustive checks, canaries, and `FuzzFairnessOps`

[`internal/admission/fair_model_test.go`](../../internal/admission/fair_model_test.go) implements an independent slice-based reference model (`fmModel`) and checks five properties on every operation: hard limit, share bound at the last slot, below-share priority order, exact work-conservation (`idleAllowed` verified directly from snapshot state without calling `Fair`), and within-group non-starvation. `TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim` ([`internal/admission/fair_test.go`](../../internal/admission/fair_test.go)) runs 400 random 96-operation sequences across all three queue rules (`fifo`, `timeout`, `control-law`) and six weight/limit variants; `TestReleasableMatchesRepeatedPicksOnRandomStates` ([`fair_repair_test.go`](../../internal/admission/fair_repair_test.go)) checks `Queues.Releasable()` exhaustively across 6 limits × 64 held-lease states × 22 waiting states.

```sh
$ go test -count=1 -v -run '^(TestFairDispatcher|TestRoundingRemainders|TestTheLastLease|TestATenantAtItsShare|TestTheShareBoundsNewLends|TestCanaryNo|TestCanaryUnpop|TestPlantedUnpop)' ./internal/admission
=== RUN   TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait
=== RUN   TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait/limit-3-tenants-2
=== RUN   TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait/limit-4-tenants-3
=== RUN   TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait/limit-5-tenants-2
=== RUN   TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait/limit-7-tenants-3
=== RUN   TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait/limit-5-tenants-3
--- PASS: TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait (0.00s)
...
=== RUN   TestTheLastLeaseIsHeldOnlyForAnActiveTenantBelowItsShare
--- PASS: TestTheLastLeaseIsHeldOnlyForAnActiveTenantBelowItsShare (0.00s)
=== RUN   TestATenantAtItsShareWaitsWhileBelowShareTenantsArriveUntilItsOwnLeaseEnds
--- PASS: TestATenantAtItsShareWaitsWhileBelowShareTenantsArriveUntilItsOwnLeaseEnds (0.00s)
=== RUN   TestTheShareBoundsNewLendsAndPreemptsNothing
--- PASS: TestTheShareBoundsNewLendsAndPreemptsNothing (0.00s)
=== RUN   TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim
--- PASS: TestFairDispatcherAgreesWithTheReferenceModelAndHoldsEveryClaim (7.21s)
=== RUN   TestCanaryNoShareBoundBreaksTheShareClaim
--- PASS: TestCanaryNoShareBoundBreaksTheShareClaim (0.00s)
=== RUN   TestCanaryNoRoundRobinStepIsCaught
--- PASS: TestCanaryNoRoundRobinStepIsCaught (0.00s)
=== RUN   TestCanaryUnpopKeepingTheTurnIsCaught
--- PASS: TestCanaryUnpopKeepingTheTurnIsCaught (0.00s)
=== RUN   TestPlantedUnpopTurnBugIsCaughtByTheFairnessModel
--- PASS: TestPlantedUnpopTurnBugIsCaughtByTheFairnessModel (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/admission	7.230s
```

`FuzzFairnessOps` decodes 3 bytes per operation (up to 96 operations, ignoring trailing bytes, no input-hash seeding) and drives `Fair` against `fmModel` across all three queue rules and three variants. `TestFairFuzzDecodingIsTotalBoundedAndLocal` and `TestFairFuzzExecutionCostIsBounded` verify decode locality and state bounds, and [`scripts/fuzz-plant.sh`](../../scripts/fuzz-plant.sh) verifies that `FuzzFairnessOps` finds `FairPlantUnpopKeepsTurn` within its 20 s budget (no committed seed triggers it alone).

Engine-level tests in [`internal/proxy/fairness_test.go`](../../internal/proxy/fairness_test.go) prove every lease-end path (`release`, `vanished-mid-statement`, `vanished-in-transaction`, `idle-in-transaction`, `connection-lost`, `statement-deadline`, and `shutdown`) returns its in-flight count on both `CorePool` and `FixedSet`, that a held-back request triggers zero replacement dials across 20 idle timeouts and dials promptly once eligible, that `Limit < Connections` never dials past `Limit`, and that `Engine.Invariants` catches `PlantFairStatementInFlight`.

### Tier S scenarios, ablation, and DuckDB SQL assertions

Running the tier S fairness suite on seed 1 shows the protection, the single-FIFO ablation, and the exact latency and shedding boundaries directly:

```sh
$ go test -count=1 -v -run '^(TestScenarioFairNoisyNeighbour$|TestScenarioFairNoisyNeighbourAblation$|TestScenarioFairNarrowed|TestScenarioFairHeldTransactions$|TestScenarioFairManyTenants$|TestCanaryFair)' ./test/e2e -seed=1
=== RUN   TestScenarioFairNoisyNeighbour
    c09_fairness_test.go:164: seed 1: quiet tenant p99 14876671 ns with the flood, 11075583 ns alone
--- PASS: TestScenarioFairNoisyNeighbour (5.91s)
=== RUN   TestScenarioFairNoisyNeighbourAblation
    c09_fairness_test.go:192: seed 1: quiet tenant p99 11075583 ns alone, 2583691263 ns with one shared queue; 86 ok of 88, errors [{53M01 2}]
--- PASS: TestScenarioFairNoisyNeighbourAblation (8.76s)
=== RUN   TestScenarioFairManyTenants
--- PASS: TestScenarioFairManyTenants (16.89s)
=== RUN   TestScenarioFairNarrowedQuietWaitIsBoundedByOneFlooderLease
    c09_fairness_test.go:296: seed 1: flooder leases 40ms: quiet p99 32636927 ns, alone 11075583 ns (2.95x)
--- PASS: TestScenarioFairNarrowedQuietWaitIsBoundedByOneFlooderLease (1.25s)
=== RUN   TestScenarioFairNarrowedQuietIsShedOnceAFlooderLeaseReachesTheAllowedWait
    c09_fairness_test.go:324: seed 1: flooder leases 150ms: quiet 6 ok, 2 shed of 8
--- PASS: TestScenarioFairNarrowedQuietIsShedOnceAFlooderLeaseReachesTheAllowedWait (2.08s)
=== RUN   TestScenarioFairNarrowedSynchronizedQuietTenantsWithinCapacity
--- PASS: TestScenarioFairNarrowedSynchronizedQuietTenantsWithinCapacity (2.52s)
=== RUN   TestScenarioFairHeldTransactions
--- PASS: TestScenarioFairHeldTransactions (8.19s)
=== RUN   TestCanaryFairHeldTransactionsWithoutTheShareBound
--- PASS: TestCanaryFairHeldTransactionsWithoutTheShareBound (4.04s)
=== RUN   TestCanaryFairHeldTransactionsNotCountedInFlight
--- PASS: TestCanaryFairHeldTransactionsNotCountedInFlight (0.12s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	49.779s
```

Each scenario runs twice per seed to prove bit-identical simulator digests and byte-identical flushed CSV files, and verifies steady-state quiesce (`in_flight = 0`, `fair_states = 0`, `active_tenants = 0`, `tenants = 0`). The four main scenarios (`fair-noisy-neighbour`, `fair-many-tenants`, `fair-held-transactions`, and `fair-results`) also passed 200 distinct seeds (`1..200`) with zero failures across their DuckDB SQL assertions:

- **Noisy neighbour (`TestScenarioFairNoisyNeighbour` + ablation):** 2 connections, 200 **open-loop** flooder clients (arriving on a fixed schedule regardless of response time) at 10× capacity (5 ms reads, ~5.5 ms lease with reset), 1 quiet client sending a 10 ms `UPDATE` every 25 ms, followed by a 30% recovery phase. [`quiet-against-alone.sql`](../../test/analysis/fair-noisy-neighbour/quiet-against-alone.sql) (run via **DuckDB**, an embedded analytical SQL engine over the flushed CSV files) joins the `alone`, `fair`, and `fifo` arms of each seed to confirm exact quiet completion counts under `fair` and degradation under `fifo`; [`flooder-shedding.sql`](../../test/analysis/fair-noisy-neighbour/flooder-shedding.sql) verifies >95% of `53M01` sheds belong to the flooder; [`fair-accounting.sql`](../../test/analysis/fair-noisy-neighbour/fair-accounting.sql) checks lease conservation at every flush.
- **Many tenants, one flooder (`TestScenarioFairManyTenants`):** 1 flooder and 40 quiet tenants staggered 12.5 ms apart so every quiet tenant overlaps the 500 ms 10× flood (`checkOverlap` invariant watch). [`per-tenant.sql`](../../test/analysis/fair-many-tenants/per-tenant.sql) joins both arms per tenant directly from the flushed CSV files—checking all 40 quiet tenants past Prometheus's 32-tenant label cap—and verifies that every quiet tenant kept its standalone count and spent non-zero time at its share.
- **Held transactions (`TestScenarioFairHeldTransactions` + two canaries):** 4 connections (share 2 each); 8 closed-loop quiet clients run 200 reads while 8 closed-loop transaction clients hold 2-statement transactions open with 20 ms think time. A per-step invariant watch confirms the transaction tenant's lent connections reach its share of 2 and never exceed 2 while the quiet tenant stays active, and [`held.sql`](../../test/analysis/fair-held-transactions/held.sql) checks `in_flight >= clients_in_transaction` at every flush. Both `FairPlantNoShareBound` and `PlantFairStatementInFlight` fail the scenario.
- **Results equal direct (`TestScenarioFairResultsEqualDirect`):** re-runs the three-tenant direct-equality workload behind the fair dispatcher and a relaxed timeout rule (`interval = 5s`, `target = 1s`), proving proxied replies still match direct execution while every lease passes through `Fair` ([`fair.sql`](../../test/analysis/fair-results/fair.sql)).

### Tier R against live Postgres 17

`TestTierRFairNoisyNeighbour` ([`test/e2e/c09_tierr_test.go`](../../test/e2e/c09_tierr_test.go)) runs the compiled `mini-gateway` binary in front of Postgres 17 with 2 connections, `queue.rule = "timeout"`, `tenant_capacity = 16`, and total capacity 128: 40 closed-loop flooder clients run `SELECT pg_sleep(0.005)` while 1 quiet client sends `SELECT pg_sleep(0.02)` every 50 ms for 100 requests:

```sh
$ MG_TIER_R=1 go test -count=1 -v -run '^TestTierRFairNoisyNeighbour$' ./test/e2e
=== RUN   TestTierRFairNoisyNeighbour
    c09_tierr_test.go:110: quiet tenant: 100 ok of 100, p99 56.768264ms (alone 50.917713ms); flooder: 994 ok, 39064 shed
--- PASS: TestTierRFairNoisyNeighbour (12.14s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	20.856s
```

All 100 quiet requests succeeded (above the >99% threshold) with p99 56.77 ms against 50.92 ms alone (1.11×, within 2×), while the flooder absorbed all 39,064 sheds (100%, above the >95% threshold) and completed 994 statements. [`test/analysis/tierr-fair-noisy/tenants.sql`](../../test/analysis/tierr-fair-noisy/tenants.sql) verifies those totals and lease conservation from the binary's flushed CSV files.

---

## Stated limits

1. **Shares bound database concurrency, not database CPU.** The dispatcher counts open leases, not query CPU, memory, or I/O cost. A tenant running expensive statements consumes more database work per lease than one running trivial reads.
2. **No preemption.** Shares bound new lends only. Leases already held when a new tenant joins or when `SetLimit` lowers the limit continue until they end normally or hit `timeouts.idle_in_transaction` (`25P03`).
3. **When the last free connection may idle.** A connection below `limit` stays idle while a request waits in exactly one case: it is the last free lease (`in_flight + 1 == limit`), no waiting tenant is below its share, and some active tenant with nothing waiting holds fewer leases than its share. At most that one connection idles, until that tenant sends a request, its last lease ends, or the limit changes. When no active tenant is below its share (including when floor-rounded shares sum below `limit`), nothing idles.
4. **Round robin is bounded within a group.** A waiting tenant is chosen before any tenant in its own or a lower priority group is chosen twice. A tenant already at or above its share may wait indefinitely while below-share tenants keep arriving, until one of its own leases ends (moving it into the below-share group) or its queue rule sheds the request; it holds at least its share meanwhile.
5. **Noisy-neighbour wait bound and scenario shapes.** Because open leases are not preempted, a quiet tenant arriving while a flooder holds every connection waits at most one flooder lease (longest flooder statement—or longest open transaction including think time—plus the reset), not a fixed multiple of its own standalone latency. The headline figures—p99 within 2× standalone, exact standalone outcomes in tier S, and >99% served in tier R—hold for the stated scenario shapes where the flooder lease stays shorter than the quiet statement (for example, with 40 ms flooder reads and a 10 ms quiet `UPDATE`, quiet p99 reaches 32.64 ms vs 11.08 ms alone—2.95×, within `alone + 40.5 ms`—and all quiet requests still succeed). Once a flooder lease reaches the quiet queue's allowed wait (`queue.interval`, 100 ms when the quiet queue does not stand, or `queue.target`, 5 ms if quiet arrivals exceed service rate), the queue rule begins shedding quiet requests (0 shed at 90 ms flooder reads; 2 of 8 shed at 150 ms).
6. **Many-tenant outcomes require quiet demand within capacity.** Every quiet tenant keeps its standalone outcomes beside a flooder only while the quiet tenants' combined demand stays within queue-wait and total-queue capacity. With 2 connections, ~10.5 ms quiet leases, ~5.7 ms flooder leases, 100 ms allowed wait, total capacity 128, and a 32-slot flooder ring, at most 18 synchronized quiet requests fit (`TestScenarioFairNarrowedSynchronizedQuietTenantsWithinCapacity` verifies 16 arriving simultaneously); larger simultaneous bursts overflow wait budgets regardless of the flooder.
7. **Work-conserving below the last slot.** Below `limit - 1`, a tenant at or above its share is lent whenever no below-share tenant waits, so one tenant can climb to `limit - 1` leases while other active tenants have nothing queued; their next arrival waits for one lease to end and then takes priority.
8. **Weights change share, not turn cadence.** Every served request advances a tenant's round-robin turn by one step regardless of its weight.
9. **One extra checkout dial at `limit == pool.connections`.** `Engine.corePump` always reports `Queues.Releasable()` to `pool.Demand`, preventing dial/idle-close loops and dials past a lowered fair limit. When `limit < pool.connections`, `CorePool.Checkout` also refreshes demand before `Pool.Checkout` checks whether to dial. When `limit == pool.connections`, that checkout refresh is left off to preserve pool checkout dialing sequences; `Pool.Checkout` may therefore still dial one connection against the demand seen just before checkout, staying within `pool.connections` and closing once at `pool.idle_timeout` without replacement.
10. **Shares are per instance.** In a multi-instance fleet, each gateway computes shares over the tenants active on that instance against its own `pool.connections` limit. Fair sharing survives when tenants spread evenly across instances and weakens if a tenant's connections concentrate on one instance while a flooder spreads across all of them.
11. **Accounting granularity, CPU cost, and offline SQL memory.** `saturated_time` and `tenant_time_at_share` advance at queue operations and internal deadlines, so a snapshot reports them up to the last event. Selection and time accounting cost `O(live tenant queues + tenants with leases)` per operation, and `Engine.Invariants` checks per-tenant leases in `O(L²)` for `L <= pool.connections`. Offline multi-hundred-seed sweeps of `fair-many-tenants/per-tenant.sql` (41 tenants × 400 runs) exceed memory if queried in one DuckDB invocation on a small host and run in 20-seed windows.
