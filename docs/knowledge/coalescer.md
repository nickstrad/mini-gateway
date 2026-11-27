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
  both with `HasKey`. The key is `Unit.Key` (tenant + exact text). Nothing reads SQL.
- Per key at most two flights: the current one (queued or running) and, in strict mode, one held
  behind a running one. Relaxed joins the running one. The flight deadline is `Config.Deadline`
  (the statement deadline) from creation; flights are kept in creation order, so the head of
  the list is the next deadline (O(1) `Next`).
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
- Errors: a flight that fails shares the owner's row (`08006`/`08007`, `53M01`, `53M03`); a
  flight past its deadline answers `53M01` if never sent, else the statement-deadline row; a
  waiter's own cancel is `57014` (`ErrRowCancelledQueued`); caps answer `53M04`.

## Scenarios

- `test/e2e/c11_coalesce.go`: `CoalesceScenarios()` (both join modes), each with replay, and an
  ablation that must fail the check (coalescer off, or a core plant). `TestScenarioCoalesce`
  caps seeds at 20 (3 for the 1,000-client hot key) unless `MG_C11_FULL_SWEEP=1`.
- Count database reads from `Fake.StatementLog()` (`KindRead`, exact `Text`); overlap of
  `[Start, End)` gives reads running at once. The fake reads a row's value when service starts,
  so the relaxed read-your-write window is visible in tier S.
- Tier R (`c11_tierr_test.go`) makes reads slow with views over `pg_sleep` (`c11slow`,
  `c11long`, `c11kv`): their query text stays in the safe shape. Count reads as
  `pg_stat_statements.calls` deltas for the exact normalised text (cancelled statements are not
  counted). The tier R harness sets `coalesce.mode: off` for gateways with `lease.reset:
  on-switch` (the all-tenants key is refused with it).
