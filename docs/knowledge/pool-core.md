# The pool core and its wiring (chunk 7, 2026-11-27)

Facts a later builder (chunks 8–13) needs before touching `internal/pool`, the engine's
core path in `internal/proxy`, or the pool scenarios. Full record:
`agent_work/chunks/c07-pool/NOTES.md`.

## The core (`internal/pool`)

- Sans-I/O, deterministic, determinism-linted; no Postgres types (imports `internal/work`
  and `internal/obs` only). `pool.New(cfg)`; every call takes `now` and an `out []Action`
  and returns the actions: `Demand(now, waiting)`, `Opened`, `DialFailed`,
  `Checkout(now, tenant)`, `Release(now, id, End)`, `StepDone(now, id, status)`,
  `StepFailed`, `Lost(now, id, LossKind)`, `ClientCancel`, `CancelDone`, `Tick`, `Next`,
  `Allow` (the breaker), `Shutdown`, `Snapshot`, `Invariants`, `Quiesce`.
- Actions: `ActDial` (the core allocates connection ids and cancel ids from one
  sequence), `ActCheck` (empty query), `ActReset` (with `Reseed` and a seed in millionths),
  `ActRollback`, `ActCancel` (`Cancel` is the id of the second connection), `ActClose`
  (with a `pool.CloseReason`), `ActRefuseQueued` (the breaker opened, including a failed trial).
- The core owns every pool deadline: check (`pool.check_timeout`), clean-up
  (`timeouts.cleanup`), reset (`timeouts.reset`), idle timeout and lifetime (jittered per
  connection at dial time), breaker `open_for`, the creation-rate slot. Deadlines fire at
  equality (`now >= deadline`), like the engine's, and time passes before the event: every
  action-returning operation first expires what is due at or before its `now` (and ticks
  the breaker), then applies the operation. `Allow` advances only the breaker and its
  snapshot; direct callers must use an action-returning operation to expire connections. A reply that arrives at its deadline is late.
- Dials happen only on demand: `waiting > idle + connecting + checking + resetting`, under
  the cap (open + connecting), `max_connecting` and the creation rate. The rate is a
  one-slot GCRA: consecutive dials are at least `ceil(1s / rate)` apart (rounded up, so the
  bound holds for a rate that does not divide a second), so in any window W there are at
  most `floor(W·rate/1s) + 1` dials. No prefill: an empty pool after a quiet
  night is a cold start.
- Jitter: each connection's idle timeout and lifetime is drawn uniformly per nanosecond in
  `d ± d·permille/1000`. It spreads expiries; it does not make them unique. A duration whose
  spread would be under `pool.MinJitterSpread` (1 ms) is refused at validation naming
  `pool.idle_timeout` or `pool.max_lifetime` (turn jitter off to allow it).
- Lend order: most recently used idle connection; under `on-switch` first one tagged with
  the same tenant, then an untagged (reset) one; a connection tagged with another tenant
  is reset at checkout (`tenant_switches`) and the request stays queued (D3: no wait in the
  pool). A stale (`check_after_idle`) or suspect connection is checked first, off the
  request path, at most 4 checks per checkout.
- `suspect`: any connection-class failure (check failed, loss while idle except a polite
  close, loss while lent / resetting / cleaning, a zero-reply statement deadline) marks
  every idle connection suspect and every busy one suspect at its return. Only a passed
  check clears it. A no-reply timeout marks connections present at that event; its later
  cleanup close alone does not mark replacement connections opened since the timeout.
  The reference model must preserve this distinction (`internal/pool/arbitration_test.go`).
- Breaker: consecutive connection-class failures to `breaker.failures` open it: failed
  dials (including the connect deadline), bytes on an idle connection, loss of a lent /
  resetting / cleaning connection. Never SQL errors, never a zero-reply timeout alone, never
  a polite close, and never a failed check of an idle connection that is not the trial: a
  dead old flow (a middlebox drop, a failover) says nothing about new dials, so the check
  closes it (`check_failed`, siblings suspect) and the replacement dial decides. Open:
  `Allow` false, no dial. After `open_for`: half-open; exactly one trial — a dial, or a check
  of an idle connection when the pool is at its cap — and only once a request was refused
  or work is waiting (so an idle proxy does not dial a dead database every second). While
  half-open `Allow` admits requests (they queue behind the trial; nothing is lent until it
  passes), so the first query after a quiet outage is not refused. A failed trial re-opens
  it. Opening or re-opening the breaker emits `ActRefuseQueued` even if idle connections
  remain: checkout cannot lend them while the breaker is open. The engine answers the queue
  `53M03`. Losing a connection while already open also requests that flush. After `Shutdown`, `Next()` is 0.
- Dirty-connection table: `Release` with an `End` kind other than clean: mid-statement →
  cancel on a second connection and drain to the end of the reply; inside a transaction →
  `ROLLBACK`; then the reset; reuse only after all of it and after the cancel connection
  reported done. Any failure or the clean-up deadline closes the connection. A cancel whose
  wait (`2 × timeouts.cleanup`) expires without reporting done taints its connection: it is
  closed (`cleanup_failed`) at its next return and never lent again.
- Plants (tests only, `pool.Config.Plant`): `PlantNoCap`, `PlantNoRate`,
  `PlantNoCheckDeadline`, `PlantNoSuspect`, `PlantIgnoreTag`, `PlantResetClearsSuspect`
  (the fuzz target's planted ordering bug).

## The engine's core path (`internal/proxy/corepool.go`)

- Selected by `proxy.Config.Pool != nil` (`internal/config` always sets it; the binary uses
  the core). `nil` keeps chunk 6's `FixedSet`, which the chunk 6 e2e scenarios still use as
  the comparison arm. `CorePool` implements the `Pool` seam, `CoreBreaker` the `Breaker`
  seam, `CorePolicy` the `ReleasePolicy` seam.
- `finish()` drains buffered checkout actions together with `core.Demand(now, queue.Len())`
  once after every step and arms one engine timer (`tPool`) at `core.Next()`. It does not
  redispatch merely because a check or reset started. An unsuccessful direct checkout
  queues without retrying checkout in that same enqueue call. Regression tests are in
  `internal/proxy/core_arbitration_test.go`.
- New transport action and event: `ActDBCancel{Conn: cancel id, Data: CancelRequest}`; the
  driver dials, writes, reads to EOF or `timeouts.cleanup`, and posts `EvDBCancelDone`.
  `proxysim` and `internal/shell` implement it. Cancel connections are not counted in the
  pool cap (they are the fleet reserve).
- The engine mirrors each core connection's state in `dbConn.phase`; `Engine.Invariants`
  checks after every event that each phase and the idle gauge agree with the core. The core
  can move a connection without an action (a cancel completing releases one waiting in
  `cleaning`), so the engine re-syncs every core connection after `CancelDone`
  (`coreSyncAll`). A completed reset leaves `dpResetting` before the core judges it, so a
  lifetime close at that moment does not also count a failed reset.
- New database phases `dpChecking` and `dpCleaning`; the judge (`resetJudge` in
  `lease.go`) gained modes for the check, the rollback, the re-seed and a silent wait. The
  drain after a cancel continues the relay's message cursor; when it starts inside a
  message it is marked so that message's tail is never read as a `ReadyForQuery`.
- Lease accounting on the core path: `LeasesEnded = reset + close + as-is + dirty`;
  `ResetsStarted = LeasesEndedReset + ResetsNotAtRelease`.
- Idle inside a transaction (`timeouts.idle_in_transaction`, 5 s) exists only on the core
  path: `FATAL 25P03`, `ROLLBACK`, reset, reuse.

## Config

- An omitted `pool.connections` with `fleet.backend_connections` set takes
  `pool.Share(total, fleet.instances)` (rounded down, never zero); an explicit one above the
  share is refused. Keepalive `idle` and `interval` must be whole seconds of at least 1 s
  whenever `backend.tcp_keepalive.mode` or `backend.server_keepalive` is on.

## Tests to copy from

- Engine level: `internal/proxy/core_test.go` (a hand-driven rig; no simulator).
- Core level: `internal/pool/model_test.go` (the reference model) and `FuzzPoolOps`.
- Scenarios: `test/e2e/c07_*.go`; `TestScenarioPool` (swept; heavy scenarios run 3 seeds).
  The fault driver `pathRelay` (`c07_faultnet.go`) sits between the proxy and the fake:
  blackhole, peer_forgot, polite close, idle_drop with modelled keepalive, slots held,
  and the `setseed` rewrite that lets the fake answer the re-seed. It classifies cancel
  connections and does not count them as dials.
