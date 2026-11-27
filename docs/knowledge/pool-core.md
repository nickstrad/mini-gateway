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
  (with a `pool.CloseReason`), `ActRefuseQueued` (the breaker opened with nothing usable).
- The core owns every pool deadline: check (`pool.check_timeout`), clean-up
  (`timeouts.cleanup`), reset (`timeouts.reset`), idle timeout and lifetime (jittered per
  connection at dial time), breaker `open_for`, the creation-rate slot. Deadlines fire at
  equality (`now >= deadline`), like the engine's.
- Dials happen only on demand: `waiting > idle + connecting + checking + resetting`, under
  the cap (open + connecting), `max_connecting` and the creation rate. The rate is a
  one-slot GCRA: consecutive dials are at least `1s / rate` apart, so in any window W
  there are at most `floor(W·rate/1s) + 1` dials. No prefill: an empty pool after a quiet
  night is a cold start.
- Lend order: most recently used idle connection; under `on-switch` first one tagged with
  the same tenant, then an untagged (reset) one; a connection tagged with another tenant
  is reset at checkout (`tenant_switches`) and the request stays queued (D3: no wait in the
  pool). A stale (`check_after_idle`) or suspect connection is checked first, off the
  request path, at most 4 checks per checkout.
- `suspect`: any connection-class failure (check failed, loss while idle except a polite
  close, loss while lent / resetting / cleaning, a zero-reply statement deadline) marks
  every idle connection suspect and every busy one suspect at its return. Only a passed
  check clears it.
- Breaker: consecutive connection-class failures (dial failures, check failures, losses;
  never SQL errors, never a zero-reply timeout alone) to `breaker.failures` open it. Open:
  `Allow` false, no dial. After `open_for`: half-open; exactly one trial — a dial, or a check
  of an idle connection when the pool is at its cap — and only once a request was refused
  or work is waiting (so an idle proxy does not dial a dead database every second).
- Dirty-connection table: `Release` with an `End` kind other than clean: mid-statement →
  cancel on a second connection and drain to the end of the reply; inside a transaction →
  `ROLLBACK`; then the reset; reuse only after all of it and after the cancel connection
  reported done. Any failure or the clean-up deadline closes the connection.
- Plants (tests only, `pool.Config.Plant`): `PlantNoCap`, `PlantNoRate`,
  `PlantNoCheckDeadline`, `PlantNoSuspect`, `PlantIgnoreTag`, `PlantResetClearsSuspect`
  (the fuzz target's planted ordering bug).

## The engine's core path (`internal/proxy/corepool.go`)

- Selected by `proxy.Config.Pool != nil` (`internal/config` always sets it; the binary uses
  the core). `nil` keeps chunk 6's `FixedSet`, which the chunk 6 e2e scenarios still use as
  the comparison arm. `CorePool` implements the `Pool` seam, `CoreBreaker` the `Breaker`
  seam, `CorePolicy` the `ReleasePolicy` seam.
- `finish()` calls `core.Demand(now, queue.Len())` after every step and arms one engine
  timer (`tPool`) at `core.Next()`.
- New transport action and event: `ActDBCancel{Conn: cancel id, Data: CancelRequest}`; the
  driver dials, writes, reads to EOF or `timeouts.cleanup`, and posts `EvDBCancelDone`.
  `proxysim` and `internal/shell` implement it. Cancel connections are not counted in the
  pool cap (they are the fleet reserve).
- New database phases `dpChecking` and `dpCleaning`; the judge (`resetJudge` in
  `lease.go`) gained modes for the check, the rollback, the re-seed and a silent wait. The
  drain after a cancel continues the relay's message cursor.
- Lease accounting on the core path: `LeasesEnded = reset + close + as-is + dirty`;
  `ResetsStarted = LeasesEndedReset + ResetsNotAtRelease`.
- Idle inside a transaction (`timeouts.idle_in_transaction`, 5 s) exists only on the core
  path: `FATAL 25P03`, `ROLLBACK`, reset, reuse.

## Tests to copy from

- Engine level: `internal/proxy/core_test.go` (a hand-driven rig; no simulator).
- Core level: `internal/pool/model_test.go` (the reference model) and `FuzzPoolOps`.
- Scenarios: `test/e2e/c07_*.go`; `TestScenarioPool` (swept; heavy scenarios run 3 seeds).
  The fault driver `pathRelay` (`c07_faultnet.go`) sits between the proxy and the fake:
  blackhole, peer_forgot, polite close, idle_drop with modelled keepalive, slots held,
  and the `setseed` rewrite that lets the fake answer the re-seed. It classifies cancel
  connections and does not count them as dials.
