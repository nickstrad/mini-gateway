# Admission queues: the core, its seam and its scenarios (chunk 8, 2026-11-27)

Facts a later builder (chunks 9, 10, 13, 14) needs before touching `internal/admission`,
the engine's queue stage or the admission scenarios. Full record:
`agent_work/chunks/c08-admission/NOTES.md`.

## The core (`internal/admission`, determinism-linted)

- `admission.New(cfg)` → `*Queues`. Operations take `now` (int64 ns): `Push(now, tenant, id)`,
  `Pop(now)`, `Unpop()`, `Remove(now, id)` (client gone), `Expire(now, id)` (the engine's
  `timeouts.queue_wait` backstop, counted as shed reason `deadline`), `Sweep(now)`, `Next()`
  (the next instant the core must be woken; 0 = none), `TakeShed()` (the FIFO of sheds the
  caller must answer). Every operation first processes every internal deadline at or before
  `now`, each at its own instant ("time passes before the event"); a wait equal to the
  allowed wait has expired.
- One fixed-capacity ring per tenant, created on first push, removed after 16 intervals
  empty (and LIFO off); rings are recycled through a free list, so memory is
  `queue.max_tenants x queue.tenant_capacity` entries. `queue.capacity` is the total across
  tenants. Full tenant ring: its head is shed (`overflow`). Total full: the globally oldest
  request is shed (`total`). Tenant cap reached: an empty tenant queue is evicted; if none is
  empty the arrival is refused (`tenants`; `Push` returns false).
- Rules (`QueueRule`): `RuleTimeout` (one `LastEmpty`; allowed wait = target while
  `now - LastEmpty >= interval`, else interval; `LastEmpty` is set at a push into an empty
  queue and whenever the queue empties), `RuleControlLaw` (RFC 8289 state machine run at the
  head, timer driven; next drop `Law(t, interval, count)` = `t + floor(interval/sqrt(count))`
  by an exact 128-bit integer search; count saturates at 65,536), and the simulator arms
  `RuleFIFO` (never sheds) and `RuleFolly` (verdict at pop, no head sweep). `ParseProductRule`
  accepts only `timeout` and `control-law`; nothing in `internal/config` can produce an arm.
- Adaptive LIFO: on as soon as the rule reports overload; off only after `queue.lifo_hold`
  (default 1 s) without overload. The head sweep runs before every pop, so LIFO never buries.
- The default dispatcher is `OldestFirst` (the tenant whose head is oldest). **Chunk 9 plugs in
  here:** implement `admission.Dispatcher` (`Pick(now, []TenantView) int`) and call
  `Queues.SetDispatcher`. Selection costs O(active tenants) per operation today.
- Plants (tests only, `Config.Plant`): `PlantNoHeadSweep`, `PlantFirstBurst`,
  `PlantNoLIFOHysteresis`, `PlantRemoveSwapsTail` (the fuzz ordering bug).

## The engine seam

- `proxy.Config.Admission` with `Rule != 0` builds `proxy.AdmissionQueue`; `Rule == 0` keeps
  chunk 6's `FIFO`, so every chunk 6 tier S scenario still runs on `FIFO`. The binary always
  sets a rule (default `timeout`).
- The seam grew additively: `proxy.ShedQueue` (`RemoveAt`, `Expire`, `Sweep`, `Next`,
  `TakeShed`, `Unpop`, `Close`). `Engine.Shutdown` sweeps the core at its instant first
  (answering due sheds), then removes the rest as client-gone, then `Close`s the empty
  tenant queues and cancels `tAdmission`; `Invariants()` holds the core's shed total equal to
  the engine's `Shed`. `Unpop` restores the rule state a pop that emptied the queue reset. The engine drains sheds and re-arms one timer (`tAdmission`) after
  every queue operation (`Engine.queueTouched`); a shed is answered `53M01` with its
  `ReadyForQuery`, at once. With a `ShedQueue` the engine never lends past the queue (no
  fast path), so the rule sees every request.
- The engine's `Invariants()` checks the core cheaply (`Queues.Check`, no histogram copies);
  the full `Snapshot()` is for the collector only (it allocated 50% of run time when used as
  an invariant).

## Observability

- Component `admission` (`internal/obs/admsrc`, registered by `enginesrc.Sources`, so the
  binary and every e2e run export it). Totals always; `tenant_*` rows and histograms only with
  `obs.detail`; Prometheus folds tenants beyond 32 into `other` (promtext). Per-tenant
  counters outlive the tenant queue (up to 4,096 tenants, then `other`), so CSV counters
  never decrease.
- Health rule `admission`: `degraded` while any tenant queue shed within the last interval.

## Scenarios and their cost

- `test/e2e/c08_admission.go`: `RunAdmission` (open-loop workload through the engine, 1
  connection, 5 ms reads; one lease is about 5.5 ms with the reset, so capacity is 181/s).
  The fake charges its service time to `DISCARD ALL` too unless the time is per kind.
- Head-to-head, anti-bloat and the head-sweep canary skip under `-short`; together about
  3 minutes on a loaded vm-1. They use one seed whatever `DST_SEEDS` says; the first-burst
  scenario is swept.
- `scripts/admission-head-to-head.sh` prints the book table from one DuckDB query;
  `--check` reruns the scenarios and diffs against `test/analysis/admission-head-to-head/head-to-head.md`.
- Tier R runs the chunk 6 tests with a relaxed rule (`RelaxedQueue`: interval 5 s): the
  default 100 ms interval sheds their deliberately long queue waits.

## With chunk 7's pool core (the join, 2026-11-27)

- Shed reason `breaker` (`ReasonBreaker`): `Queues.Refuse(now, id)` takes a queued request out
  for the caller to answer itself (`53M03`); it is counted in `shed` and `ShedCount`, not left
  for `TakeShed`, and it does not mark the tenant queue as shedding. Only the engine reaches it
  (the breaker opening with requests queued), so the random-operation model does not.
- With the pool core a checkout can refuse while connections are idle (a check or a half-open
  trial is pending), so `Unpop` (and its kept served-wait sample) is more frequent than with
  the fixed set.
- Tier R gateways all get `RelaxedQueue` unless a test names its own queue; that now includes
  chunk 7's pool tests.
- Record: `agent_work/chunks/c07-pool/NOTES.md`, "Join with chunk 8".
