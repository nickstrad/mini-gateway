# The coalescer: core, engine wiring and scenarios (chunk 11, 2026-11-27)

Facts a later builder (chunks 12–14) needs before touching `internal/coalesce`, the engine's
coalesce stage or the coalescer scenarios. Full record: `agent_work/chunks/c11-coalescer/NOTES.md`.

## The core (`internal/coalesce`, determinism-linted, race-listed)

- `coalesce.New(cfg)` → `*Table`. Every operation takes `now` and first expires what is due at
  or before it (flight deadlines, then the no-share list): `Join(now, id, *work.Unit)`,
  `Started(now, flight)`, `Stored(now, flight, n) bool`, `Finish(now, flight, Result)`,
  `Leave(now, id, LeaveCancel|LeaveGone)`, `Sweep(now)`, `Shutdown(now)`. Results that concern
  other requests come out as notices: drain `TakeNotice()` after every call.
- Verdicts: `Pass` (not eligible, key on the no-share list, table full), `Lead` (start a flight:
  queue yourself), `Follow` (wait; no queue slot, no connection), `Hold` (strict: start the flight
  that runs after the running one; wait like a follower until `NoticeStart`), `Refuse` (53M04).
- Eligibility reads only the unit's flags: `auto` = `Unit.Shareable`, `hinted` = `Unit.Hinted`,
  both with `HasKey`. The key is `Unit.Key`: inside the table an 8-byte tenant length, the
  tenant, then the exact text (`keyOf`; `splitKey` decodes it for the hot-key list), so no two
  different (tenant, text) pairs share a flight. Nothing reads SQL.
- Per key at most two flights: the current one (queued or running) and, in strict mode, one held
  behind a running one. Relaxed joins the running one. The flight deadline is `Config.Deadline`
  (the statement deadline) from creation; flights are kept in creation order, so the head of
  the list is the next deadline (O(1) `Next`). Deadlines and no-share expiries saturate at the
  largest instant (`after`).
- `Leave` of the owner with followers hands the flight to the newest follower (`LeftHandoff`);
  the last leaver ends it (`LeftLast`) and the engine cancels the work. A leaver is removed at
  once, so a result at the same instant never reaches it.
- Reply bytes past `MaxReplyBytes` detach the flight: followers get `NoticeRefused`, the key goes
  on a fixed-size FIFO no-share list for `NoShareFor`. A reply that ends with a status other
  than `I` (a hinted `BEGIN`) detaches the same way (`CapUnshareable`).
- Plants (tests only): `PlantStrictJoinsRunning`, `PlantAnyLeaveCancels`, `PlantLeaverGetsResult`
  (the fuzz plant), `PlantNoWaiterCap`, `PlantNoReplyCap`, `PlantSafeReadDoor`.

## The engine (`internal/proxy/coalesce.go`)

- Built when `proxy.Config.Coalesce.Mode != off` (or `Seams.Coalescer` is a `FlightTable`);
  `NoCoalesce` is still the default for a zero config, so every earlier tier S spec is unchanged.
  The binary defaults to `coalesce.mode: auto`, `join: strict`, `key_scope: all-tenants`.
  Limit of that default: the safe shape trusts the objects it names, so a view, a user-defined
  operator, a function shadowing an allow-listed name or a row-level security policy that hides
  a side effect or a per-call result is shared like any read unless the operator sets
  `coalesce.mode` to `off` or `hinted` (also stated in `docs/safe-shape.md`, "Limits").
- One claim is narrow: at most one read per key is in flight among the flights the table owns. A
  cancelled read still finishing on the database (`flight_ends{cancelled}`), a key on the no-share
  list (`passed{no-share}`) and a request at a full table (`passed{full}`) pass unshared.
- A flight's owner can sit in the admission queue (`cpQueued`). Remove it the way the queue's
  own exits do: at the flight deadline `Expire` (a shed), never `RemoveAt` (client gone), or the
  core's and the engine's shed counts diverge. `coalStart` does nothing once `stopping` is set,
  so `Shutdown` sends nothing new to the database.
- The owner of a flight is an ordinary request (queued, then lent). Its reply is stored, not
  streamed (`req.store`); at `ReadyForQuery` the owner gets the stored bytes and the database's
  end marker, each follower gets the same stored slice and its own `ReadyForQuery` 'I'. Nothing
  is paused for a slow client while storing, and the stored bytes are capped at half of
  `limits.reply_buffer` (validated), so the relay high-water bound still holds.
- Followers are clients in phase `cpCoalesced`, counted in `Snapshot.Coalesced`; the engine's
  conservation is accepted = completed + failed + shed + queued + in flight + coalesced.
- Hooks in shared files are one line each: `startRequest` (join), `forward` (`coalSent`),
  `relayed` (store), `relayEvents` (error seen, ready), `statementLost`, `shed`,
  `refuseWaiting`, `clientCancel`, `closeClient`, plus `Invariants`, `Quiesce`, `Shutdown`.
  `Invariants` also requires the coalescer timer armed at `Table.Next()`.
- Errors: a flight that fails shares the owner's row (`08006`/`08007`, `53M01`, `53M03`); a
  flight past its deadline answers `53M01` if never sent, else the statement-deadline row; a
  waiter's own cancel is `57014` (`ErrRowCancelledQueued`); caps answer `53M04`.

## Joined with the fair dispatcher (chunk 9) and the retry policy (chunk 12)

- A flight's owner is one request in one tenant queue and holds one lease; followers take neither,
  so followers of other tenants (key scope `all-tenants`) ride without spending their tenant's
  share and without making their tenant active.
- A request carries the tenant it was queued under (`request.tenant`); push, checkout and the
  lease start use it, and every lease end uses `dbConn.lastTenant` (the tenant the connection was
  lent to), never the client's tenant. An owner handoff to a waiter of another tenant therefore
  keeps the queue place and the lease on the first owner's tenant until they end. Using
  `c.tenant` at a lease end reports an end without a start to the fair dispatcher.
- A lost flight statement is offered to the retry policy before the shared error. A granted
  retry keeps the flight: the owner re-enters its tenant queue (`rerun`), the waiters stay on the
  flight and get the retry's outcome once. The table keeps the flight `running`, so in strict mode
  a read arriving meanwhile is held behind it. Refused (budget, breaker, deadline, second loss,
  policy off): every waiter gets the owner's row once. The budget sees one first attempt per
  flight (followers are never forwarded).
- A retried owner waiting in the queue ends at the earlier of the flight deadline and the
  request deadline (`retryClamp` on the queue timer); both paths answer the waiters through
  `shed` → `coalRefused`. The breaker re-check at every `dispatch` refuses it through
  `refuseWaiting` → `coalRefused` (`53M03` to every waiter).

## Scenarios

- `test/e2e/c11_coalesce.go`: `CoalesceScenarios()` (both join modes; the join scenarios of
  `c11_join.go` appended), each with replay, and an ablation that must fail the check (coalescer
  off, a core plant, or the retry policy off). Every scenario, `coalesce-results-*` included,
  runs with the admission queues (timeout rule sized for the 1,000-read ablation arm: target 15 s,
  interval 20 s; the binary's 5 ms target sheds these bursts) and the pool core, as the binary
  does; `coalAdmissionOK` holds core sheds = engine sheds. `TestScenarioCoalesce` caps seeds at
  10 (3 for the 1,000-client hot key) unless `MG_C11_FULL_SWEEP=1`; the 1,000-client hot key
  runs only with `RUN_DIR` set (the sweep) or `MG_C11_HEAVY=1`, and `internal/obs/simrun` runs
  `coalesce-hot-key-strict` plus its ablation arm once outside `-short` (its `reads.sql` in G3).
- Count database reads from `Fake.StatementLog()` (`KindRead`, exact `Text`); overlap of
  `[Start, End)` gives reads running at once. The fake reads a row's value when service starts,
  so the relaxed read-your-write window is visible in tier S.
- Tier R (`c11_tierr_test.go`) makes reads slow with views over `pg_sleep` (`c11slow`,
  `c11long`, `c11kv`): their query text stays in the safe shape. Count reads as
  `pg_stat_statements.calls` deltas for the exact normalised text (cancelled statements are not
  counted). The tier R harness sets `coalesce.mode: off` for gateways with `lease.reset:
  on-switch` (the all-tenants key is refused with it).
- `Spec.Fairness` sets tenant weights and `Spec.Relay` puts chunk 12's dooming relay in front of
  the fake (set `Path` to nil: both use `BeforeProxy`). Doom every idle connection (`DoomAllAt`)
  a little before a burst and the first write of the burst is refused with zero bytes by
  construction (connections idle under `pool.check_after_idle` are lent without a check).

## Arrival and ownership across strict holds

A held flight's owner has phase `cpCoalesced`, the same phase as its followers.
Its departure therefore does not use `coalHandoff`. When `Depart` returns
`LeftHandoff`, `coalLeave` must copy the old owner's `request.arrived` to the
successor before processing notices. Repeating the handoff must preserve that
same original instant. Promotion changes `enq` for queue-wait measurement but
must not change `arrived`, which bounds retries. Otherwise, with a flight deadline
longer than the statement timeout, selecting the newest follower can permit a
retry after an earlier follower's deadline.

A held flight has no queued-under tenant or prior attempt: promotion enters the
surviving owner's tenant queue for the first time. Queued and active handoffs
instead transfer the entire request, preserving its original arrival, charged
tenant and retry attempt. The connection's `lastTenant` remains the charge used
at lease end. See `TestRepairHeldHandoffsPreserveTheOriginalArrival` and
`TestRepairRepeatedRetryHandoffsKeepTheAttemptAndTenant` for repeated disconnect
and cancel regressions, and the chunk's `ARBITRATION-1.md` for the ownership audit.

In the randomized engine harness, assert that every promotion while the custom
breaker is open is refused in the same event. A coverage count alone does not
establish this property, and a blanket assertion against all writes wrongly
includes ordinary first attempts admitted before that custom breaker opened.
