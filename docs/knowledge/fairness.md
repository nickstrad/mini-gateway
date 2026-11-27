# Fairness: the fair dispatcher and where the limit is set (chunk 9, 2026-11-27)

Facts a later builder (chunks 10, 13, 14) needs before touching the dispatcher, the limit or
the fairness scenarios. Full record: `agent_work/chunks/c09-fairness/NOTES.md`.

## Where chunk 10 sets the limit

- The limit is one integer inside the fair dispatcher, `admission.Fair` (`internal/admission/fair.go`).
  Set it with `Queues.SetLimit(now, n)` (1 to `admission.MaxFairLimit`; out of range returns
  false and changes nothing). From the engine: `AdmissionQueue.SetLimit(now, n)`
  (`internal/proxy/fairness.go`); the engine holds the queue as `e.seams.Queue.(*AdmissionQueue)`,
  and `Engine.admissionCore()` returns the core. Read it back with `Queues.Limit()`.
- Its static value until chunk 10: `proxy.Config.Fairness.Limit`, which defaults to
  `Config.Connections` (`pool.connections`); there is no config key for it.
  `FairConfig.MaxLeases` is always `Connections` (the cap on dispatcher state).
- `SetLimit` follows "time passes before the event": it accounts time at share and saturated
  time up to `now` with the old limit, then sets the new one. Lowering the limit below the
  leases in flight takes nothing back: nothing is lent until in flight drops below it.
- Chunk 10's AIMD signal (statement round-trip time) is not read here; the dispatcher only
  enforces whatever limit it is given.

## The rule (as built)

- Nothing is lent while leases in flight >= limit (`held{limit}`).
- Share of a tenant = max(1, limit x weight / sum of the weights of the active tenants);
  active = a request waiting in its tenant queue or a lease in flight. Integer floor.
- A waiting tenant below its share (in flight + 1 <= share) is chosen before any tenant at or
  above it; within each group, round robin by turn (least recently given a turn; ties go to
  the oldest head). The last free lease (in flight + 1 = limit) is held back only while an
  active tenant is below its share (in flight < share) and none of those waits: it is kept for
  a tenant between requests or holding fewer leases than its share. When no active tenant is
  below its share (for example floors summing below the limit: limit 3, two tenants of share
  1), the last lease goes by round robin like any other. While held (`held{share}`) the engine
  retries at the end of every event (`Engine.fairRedispatch`): one selection per event, no
  checkout, no unpop.
- The pool is told only the demand the dispatcher would release (`Queues.Releasable`, passed
  to `pool.Demand` in `Engine.corePump`): held-back requests open no connection, so there is no
  dial / idle-close cycle and no dial past the fair limit. With the limit below
  `pool.connections` (chunk 10), `CorePool.Checkout` also refreshes that demand before the
  checkout, because the pool's checkout dials against the demand it last saw.
- In flight counts leases: started at `Engine.lend`, ended where the engine counts
  `LeasesEnded` (release on both pools, the dirty-connection table, a lost lease, shutdown).
  A transaction held open keeps its lease, so it counts.
- Turns live in the tenant queue (`tenantQueue.turn`, `Queues.round`); a new tenant queue
  joins behind every waiting tenant and just ahead of the tenant served last. `Unpop` restores the turn and the round.
- Dispatcher state (`fairTenant`) exists only for tenants with a lease in flight; time at
  share is a cumulative per-tenant counter in chunk 8's table (4,096 tenants, then `other`).

## Observability

Component `admission`: `limit`, `in_flight` (+peak), `active_tenants` (+peak), `fair_states`
(+peak), `rounds`, `held{limit,share}`, `lease_starts`, `lease_ends`, `saturated_time`; per
tenant with `obs.detail`: `tenant_in_flight`, `tenant_share`, `tenant_time_at_share`.
`Engine.Invariants` holds the dispatcher's in flight equal to the engine's leases outstanding,
per tenant (error prefix `fairness:`). No new health rule.

## Scenarios and their cost

- `test/e2e/c09_fairness.go`: `RunFair` with `NoisySpec`, `ManySpec`, `HeldSpec` (and the
  narrowed-claim variants `LongLeaseSpec`, `SyncSpec`, run without flushed files) and arms
  `fair`, `alone`, `fifo` (the fifo arm is `Config.Admission.Rule == 0`, chunk 6's shared FIFO).
  Two connections (four for held transactions), flooder reads 5 ms, quiet writes 10 ms.
- About 30-100 s per seed on a loaded vm-1 (the cost is the engine's per-step invariant
  snapshots, scaled by the flood's events). The tests cap seeds per call at 10 (ablation 3)
  and skip under `-short`; `internal/obs/simrun` runs them once (seed 1) outside `-short`.
- `TenantLeases` and `TenantQueued` on the engine are read-only probes for scenarios.
