# Fairness: the fair dispatcher and where the limit is set (chunk 9, 2026-11-27)

Reference notes for chunks 10, 13, and 14 before changing the dispatcher, the concurrency limit, or the fairness scenarios. Full record: `agent_work/chunks/c09-fairness/NOTES.md`.

## Where chunk 10 sets the limit

- The limit is a single integer inside `admission.Fair` (`internal/admission/fair.go`). Update it with `Queues.SetLimit(now, n)` (`1` to `admission.MaxFairLimit`; out-of-range values return `false` and leave the limit unchanged) and read it back with `Queues.Limit()`. From the engine, call `AdmissionQueue.SetLimit(now, n)` (`internal/proxy/fairness.go`); the engine holds the queue in `e.seams.Queue.(*AdmissionQueue)`, and `Engine.admissionCore()` returns the underlying `*admission.Queues`.
- Until chunk 10 drives the limit dynamically, its static value comes from `proxy.Config.Fairness.Limit`, which defaults to `Config.Connections` (`pool.connections`) and has no JSON config key. `FairConfig.MaxLeases` always equals `Connections` and caps dispatcher state.
- `SetLimit` follows the deadline rule ("time passes before the event"): it accounts `tenant_time_at_share` and `saturated_time` up to `now` under the old limit before installing the new one. Lowering the limit below current in-flight leases preempts nothing; the dispatcher simply stops lending until in-flight leases drop below the new limit.
- Chunk 10's AIMD signal (statement round-trip time) is not read here; `Fair` enforces whatever integer limit it is given.

## The rule (as built)

- Nothing is lent while total leases in flight are at or above `limit` (`held{limit}`).
- Each active tenant's share is `max(1, floor(limit × weight / sum of active tenants' weights))`, computed with integer floor division. A tenant is active while it has a request queued or a lease in flight.
- A waiting tenant below its share (`in_flight + 1 <= share`) is always chosen ahead of any waiting tenant at or above its share. Within each group, selection is round robin by turn (least recently served first, ties broken by oldest head).
- The last free lease (`in_flight + 1 == limit`) is held back (`held{share}`) only when no below-share tenant is waiting and some active non-waiting tenant holds fewer leases than its share (`in_flight < share`)—for example, a client between requests. When no active tenant is below its share (such as floor-rounded shares summing below `limit`, e.g. limit 3 with two tenants of share 1), the last lease is lent by round robin like any other.
- While the dispatcher holds a request back (`held{share}`), the engine retries once at the end of every event (`Engine.fairRedispatch`): at most one selection per event, with no checkout and no unpop until work becomes eligible.
- The pool receives only releasable demand: `Engine.corePump` passes `Queues.Releasable()` to `pool.Demand`, so held-back requests neither trigger dial/idle-close cycles nor dial past the fair limit. When the limit sits below `pool.connections` (chunk 10), `CorePool.Checkout` also refreshes that demand just before checkout because `Pool.Checkout` dials against the demand it last saw.
- In-flight accounting tracks leases: started in `Engine.lend` and ended beside every `LeasesEnded` increment (clean release on both pools, entry to the dirty-connection table, lost lease, and shutdown). Open transactions keep their lease between statements and continue counting against their tenant.
- Round-robin turns live in `tenantQueue.turn` alongside `Queues.round`. A newly created tenant queue joins behind every already-waiting tenant and just ahead of the tenant served last. `Unpop` restores the turn, round counter, and held flag.
- Dispatcher state (`fairTenant`) exists only while a tenant holds at least one lease; `tenant_time_at_share` is stored as a cumulative per-tenant counter in chunk 8's table (4,096 tenants, then `other`).

## Observability

Component `admission` exports `limit`, `in_flight` (+peak), `active_tenants` (+peak), `fair_states` (+peak), `rounds`, `held{limit,share}`, `lease_starts`, `lease_ends`, and `saturated_time`, plus per-tenant `tenant_in_flight`, `tenant_share`, and `tenant_time_at_share` when `obs.detail` is enabled. `Engine.Invariants` checks that the dispatcher's per-tenant and total in-flight counts match the engine's outstanding leases (error prefix `fairness:`). No new health rule is added.

## Scenarios and their cost

- `test/e2e/c09_fairness.go` runs `RunFair` across `NoisySpec`, `ManySpec`, and `HeldSpec` (plus `LongLeaseSpec` and `SyncSpec`, which run without flushed CSV files) over arms `fair`, `alone`, and `fifo` (the `fifo` arm uses `Config.Admission.Rule == 0`, chunk 6's shared FIFO). Pool size is 2 connections (4 for held transactions), with 5 ms flooder reads and 10 ms quiet writes.
- Each seed takes about 30–100 s on a loaded vm-1 because the engine runs per-step invariant snapshots across the flood's events. Tests cap seeds per call at 10 (ablation at 3) and skip under `-short`; `internal/obs/simrun` runs them once (seed 1) outside `-short`.
- `Engine.TenantLeases` and `Engine.TenantQueued` provide read-only probes for scenario invariants.
