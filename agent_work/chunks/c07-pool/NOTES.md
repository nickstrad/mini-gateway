# Chunk 7 — Pool: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c07-pool`, worktree
`/root/Software/mini-gateway-work/c07`. One builder, no helper agents.

No SPLIT-REQUEST: every deliverable of the brief is built (see "Deviations" and "Stated
limits" for what is narrower than the brief's words, and "Size" for line counts).

## Simulator change requests

None. Nothing under `internal/sim/` changed. Every G1 fault mode is built test-side in
`test/e2e/c07_faultnet.go`: a relay (`pathRelay`) placed between the proxy and the frozen
fake Postgres, the same pattern as chunk 6's `tamper.go`.

| G1 mode | How it is built | Built from |
| --- | --- | --- |
| `blackhole` | the relay accepts and discards every byte in both directions; nothing is reported to either end | relay (simnet's `FaultHalfOpen` silences one end of one connection; the relay gives both directions on a chosen connection) |
| `peer_forgot` | the first byte the proxy writes is accepted, then one network delay (1 ms) later the proxy's side is reset | relay |
| `idle_drop(D)` | a per-connection timer: no byte in either direction for D turns the connection into `blackhole` (or `peer_forgot`); keepalive is modelled as "probes every K keep the entry alive", so with K < D the connection is never dropped | relay |
| close on a dead connection | the relay never answers the proxy's close on a blackholed connection; the scenarios assert the close holds up nothing | relay |
| slots held on the server | after `blackhole`, when the proxy closes its side the relay keeps its own connection to the fake open for `ReapAfter`, so the fake's slot is held and re-dials can be refused with `53300` | relay + the fake's `MaxConnections` (spec field `FakeConns`) |
| polite idle close | the relay closes both sides of a connection idle for at least `MinAge` (a FIN to the proxy) | relay |
| the re-seed in tier S | the relay rewrites `SELECT setseed(…)` to `SELECT 0`, which the fake answers with one `DataRow` and `SELECT 1` | relay (the fake has no `setseed`) |

The relay classifies the cancel connections the proxy opens (first bytes = a
`CancelRequest`) and does not count them as dials. Its self-test is
`TestPathFaultOnOneConnectionLeavesTheOtherUnchangedAndClearingRestores` (G9 test 8).

## What was built

| Package / file | What it is |
| --- | --- |
| `internal/pool` (new, determinism list) | The pool core: accounting (open + connecting ≤ cap), concurrent-connect cap, creation rate (one-slot GCRA), idle timeout and lifetime with per-connection jitter, most-recently-used lend order, the check before reuse with its `checking` state, the `suspect` rule, the single breaker with one trial, the dirty-connection table (cancel, drain, rollback, reset, wait for the cancel), the release policies `always` / `on-switch` and the re-seed, client cancels with a cap, `Share` (fleet division), `Snapshot` with conservation, `Invariants`, `Quiesce`, test-only plants. |
| `internal/pool/model_test.go`, `pool_test.go` | The reference model, property tests over 3,000 random operation lists per config, `FuzzPoolOps`, unit tests per mechanism, canaries. |
| `internal/proxy/corepool.go` (new) + hooks in `engine.go`, `dbconn.go`, `lease.go`, `client.go`, `config.go`, `snapshot.go`, `api.go`, `timers.go`, `contracts.go` | The engine's core path: `CorePool` (`Pool` seam), `CoreBreaker` (`Breaker` seam), `CorePolicy` (`ReleasePolicy` seam); dials on demand, the check, the dirty table, the idle-in-transaction deadline, client cancels mapped to the lent connection (and `57014` for a cancelled queued request), the breaker's flush of queued work, the re-seed after the reset (same code point: `startReset` / `resetJudge` extended), the G5 `FATAL 08006` row. New transport action `ActDBCancel` and event `EvDBCancelDone`. `FixedSet` stays (the `Pool == nil` arm). |
| `internal/proxy/proxysim` | `ActDBCancel` on simnet (dial, write, wait for the close, `EvDBCancelDone`; 1 s fallback). |
| `internal/shell` | TCP keepalive on database sockets from config (`keepAliveDialer`), cancel requests on a second connection (`cancel.go`), a 100 ms write deadline before every close (TLS close-notify cannot hold a close). |
| `internal/config` (`pool.go` new) | Keys `pool.max_connecting`, `.idle_timeout`, `.max_lifetime`, `.jitter_percent`, `.check_after_idle`, `.check_timeout`, `.max_cancels`; `timeouts.idle_in_transaction`, `.cleanup`; `lease.reset: always | on-switch`, `lease.reseed: off | on`; `backend.tcp_keepalive.{mode,idle,interval,count}`, `backend.server_keepalive`, `backend.path_idle_limit`; `breaker.failures`, `.open_for`; fleet division rounding down; `Warnings()`; the server keepalive startup parameters. |
| `internal/pgwire` | New error row `lost-mid-statement-in-transaction` (`FATAL 08006`) through chunk 5's generator; `docs/error-contract.md` changes (one row). |
| `internal/obs/poolsrc` (new, determinism list) | Component `pool`: every `pool.Snapshot` field, health rule `breaker` (degraded while open or half-open). `enginesrc` registers it and maps the new engine fields; `catalog` declares it (`docs/csv-schema.md` regenerated). |
| `cmd/mini-gateway` | Keepalive passed to the shell; config warnings printed at start and by `-check`. |
| `test/e2e/c07_*.go` | Pool scenarios (21, `TestScenarioPool`), the path fault driver, checks, canaries and ablations, tier R pool tests. |
| `test/analysis/pool-*/` | SQL assertions (one `pool.sql` per scenario plus the scenario claims). `test/analysis/tierr-*/resets.sql` updated for the core's lease accounting. |
| Docs | `docs/timeout-budget.md`, `docs/capacity-table.md`, `docs/csv-schema.md`, `docs/error-contract.md` regenerated; `docs/knowledge/pool-core.md`. |
| Scripts | `scripts/fuzz-plant.sh` gains `FuzzPoolOps`; `agent_work/chunks/c07-pool/gate/extra.sh`. |

## Decisions the brief left open

1. **Who owns the pool's deadlines.** The core: check, clean-up, reset (on the core path the
   engine arms no reset timer of its own), idle timeout, lifetime, breaker, creation rate.
   One engine timer (`tPool`) at `core.Next()`. The connect deadline stays in the engine.
2. **Dial on demand only**, no prefill and no minimum idle. Demand is the queue length; a dial
   is wanted while `waiting > idle + connecting + checking + resetting`.
3. **Creation rate**: a one-slot GCRA (`interval = ceil(1 s / rate)` in nanoseconds, burst
   1; rounded up in the repair round so the bound holds for a rate that does not divide a
   second). Bound: in any window W, dials ≤ `floor(W·rate/1s) + 1`; consecutive dials ≥
   `1/rate` apart. A half-open
   trial and the redials after a mass death go through the same limiter.
4. **`max_connecting` default 4**; `pool.max_cancels` default 16.
5. **Breaker** (rewritten in the repair round, orchestrator's ruling on review finding 8):
   consecutive connection-class failures, default 5, `open_for` 1 s. What counts: dial
   failure or connect deadline, loss of an idle connection by unexpected bytes, loss of a
   lent / resetting / cleaning connection, and a failed half-open trial (dial or check),
   which re-opens it. What does not: a failed check of an idle connection that is not the
   breaker's trial (it proves only that one old flow died — a middlebox drop, a failover —
   not that new dials fail; the check closes it `check_failed`, marks the siblings suspect,
   and the replacement dial decides), SQL errors (never seen by the pool; an `ErrorResponse`
   to the check is such a failed check), a zero-reply statement deadline alone, a polite
   close (FIN) of an idle connection (it marks the siblings suspect but does not count: a
   database whose `idle_session_timeout` closes idle connections is healthy). A dial, a
   passed check or a completed reset resets the count. Whenever the breaker is open and no
   usable connection is left (it just opened, a trial failed, or the last open connection
   went while open) queued work is answered `53M03`.
6. **Half-open trial only when wanted**: a trial is sent once a request arrived (refused
   while open, or admitted while half-open) or work is waiting, so an idle proxy against a
   dead database does not dial every `open_for`. When the pool is at its cap of (suspect)
   idle connections, the trial is a check of one of them. While half-open `Allow` admits
   requests: they queue behind the one trial and nothing is lent until it passes (repair
   round; before, the first request after a quiet outage was answered `53M03`).
7. **Breaker opening with queued work**: the queued requests are answered `53M03` at once
   when nothing is usable (no open connection); otherwise they stay queued.
8. **The check**: at checkout, MRU order; at most 4 checks started per checkout call. A
   passed check sets the connection's idle age to zero. A suspect connection is checked
   whatever its age.
9. **Polite idle close** (FIN, or any close the driver reports on an idle connection) →
   reason `peer_closed_idle`, siblings suspect, no breaker count. Bytes on an idle connection
   → `idle_error`, breaker count, siblings suspect. (The engine cannot tell a FIN from a
   reset; both are `peer_closed_idle`.)
10. **Statement deadline on the core path** goes to the dirty table (cancel, drain, then
    rollback or reset) instead of closing; zero reply bytes → `backend_no_reply`, every
    connection suspect, and a clean-up timeout then closes with reason `no_reply`.
11. **Cancel and the next lease**: a connection with a cancel in flight is not lent again
    until its cancel connection reported done (or the clean-up deadline closed it). A pending
    cancel that lands on the proxy's own `DISCARD ALL` fails the reset and closes the
    connection — safe.
12. **Client cancel while queued** answers `ERROR 57014` (`cancelled-queued`, a row chunk 5
    already had) and removes the request; a cancel for a client with nothing in flight is
    ignored and counted.
13. **G5's new row** `lost-mid-statement-in-transaction`: `FATAL 08006` when the message holds
    none of `COMMIT`, `END`, `PREPARE` (case-insensitive, anywhere in the text — a false match
    gives the safe `08007`); otherwise `08007`. Both paths (fixed set and core) use it.
14. **Fleet division** (section 8.1): per-instance connections and creation rate are
    `total / N` rounded down, never zero (`pool.Share`); a total below N is refused naming the
    key (`fleet.backend_connections`, `fleet.connect_rate_per_s`); a remainder is reported by
    `File.Warnings()` (printed by `-check` and at start), no longer refused. Chunk 6 refused a
    remainder; its config test case was replaced.
15. **Server keepalive**: sent as the startup parameters `tcp_keepalives_idle`,
    `_interval`, `_count` (whole seconds, so the keepalive durations must be whole seconds).
16. **Keepalive off** is allowed and reported by `Warnings()`.
17. **Cancel connections** are plaintext `CancelRequest`s, not counted in the pool cap (the
    fleet reserve of 3 covers them); the shell gives each `timeouts.cleanup`.
18. **The re-seed** is a separate `Query` `SELECT setseed(0.xxxxxx)` written right after
    `DISCARD ALL` (one round trip, two messages); the seed comes from the pool's seeded stream.
    Its reply is judged: optional `RowDescription`, one `DataRow`, `SELECT 1`, `ReadyForQuery`
    `I`.
19. **The binary always uses the core**; there is no config key for the fixed set. The chunk
    6 tier S scenarios keep the fixed set (`Spec.Pool == nil`); the pool scenarios re-run
    results, transaction integrity, slow reader and vanish with the core.

## Claims and the tests that prove them

| # | Claim | Tests |
| --- | --- | --- |
| 1 | The cap holds at every instant | core: `TestPoolMatchesTheReferenceModelOnRandomOperations` (model checks open+connecting ≤ cap after every action), `FuzzPoolOps`, `Invariants` (registered by the engine with the simulator, after every event; since the repair round `Engine.Invariants` also checks every connection's engine phase and the idle gauge against the core: `coreAgrees`); e2e `pool-hard-cap` (1,000 clients at once: fake session peak, relay pair peak and open+connecting peak ≤ 8), `pool-storm`; SQL `pool-*/pool.sql` (every flush, value and high-water) and `pool-hard-cap/fake-peak.sql`; canary `TestCanaryPoolCapPlantBreaksTheHardCap` (plant `PlantNoCap`: "8 open + 1 connecting above the cap 8"); tier R `TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy`, `TestTierRPoolReconnectionStormWithClientTLS` |
| 2 | Creation limits hold (concurrent connects, dials per interval), also after a trial and a mass death | core: model (consecutive dials ≥ 1/rate), `TestCreationRateSpacesDialsAndConcurrentConnectsAreCapped`; e2e `pool-cold-start` (spacing at the relay), `pool-mass-*` and `pool-slots-held` (dials per second ≤ rate + 1); SQL `pool-cold-start/dial-rate.sql`; ablation `TestAblationColdStartWithoutTheCreationRateExceedsTheBound` (`PlantNoRate`: dials 7 µs apart); `TestCreationRateBoundHoldsForRatesThatDoNotDivideASecond` (rates 3, 7, 9, 11, 13, 999, 7919: every pair of dials within the stated bound) |
| 3 | No synchronized expiry (jitter ±10%) | `TestJitterSpreadsTheExpiryOfConnectionsCreatedTogether` (200 connections created together: at most 6 lifetimes within any 1 s); e2e `pool-lifetimes` (at most 4 lifetime ends within 100 ms, no dial spike above 16/s); ablation `TestAblationLifetimesWithoutJitterExpireTogether` (no jitter: 16 ends within 100 ms); `TestJitterSpreadsSmallDurationsAndRefusesOnesTooSmallToSpread` (a spread under 1 ms refused naming the key; 1 permille of 1 s gives ≥ 60 distinct lifetimes of 64). Narrowed: see "Narrowed claims" |
| 4 | Nothing is lent in doubt | model (`lent` checks: suspect, idle ≥ check_after_idle without a check, no completed reset unless same-tenant tag under on-switch, a cancel in flight); `TestCheckStartsAtExactlyCheckAfterIdleAndEndsAtItsDeadline`, `TestOneFailureMakesEveryIdleSiblingSuspect`, `TestDirtyTableCancelsDrainsRollsBackAndResetsBeforeReuse`, `TestCancelsAreCappedAndAClientCancelOnlyReachesALentConnection`, `TestEveryOperationExpiresWhatIsDueBeforeApplyingIt` (lifetime, idle timeout, check, reset and drain at deadline equality without a prior `Tick`), `TestAnExpiredCancelNeverLetsItsConnectionBeLentAgain` (the model also fails on such a lend); engine `TestCoreChecksALongIdleConnectionOffTheRequestPath`, `TestCoreDrainDoesNotTakeTheTailOfASplitMessageForReadyForQuery`, `TestCoreCancelDoneMakesTheConnectionIdleInTheEngineToo`; bytes after `ReadyForQuery` still close (chunk 6 tests unchanged) |
| 5 | The check never costs a client | engine `TestCoreCheckThatHangsEndsAtItsDeadlineWithNoClientError` (deadline at equality); e2e `pool-check-hangs` (zero client errors, the queued request served within the check deadline + 100 ms); canary `TestCanaryPoolCheckDeadlinePlantHoldsTheQueuedRequest` (`PlantNoCheckDeadline`: quiesce finds the connection still checking) |
| 6 | One death, bounded damage | e2e `pool-mass-blackhole`, `pool-mass-peer-forgot` (client errors ≤ the 2 connections lent at the death, goodput back within statement deadline + clean-up + 1 s); canary `TestCanaryPoolSuspectPlantCostsClientsAfterAMassDeath` (`PlantNoSuspect`: 6 client errors for 2 lent); `TestNightTrickleEachRemedyAloneCarriesTheRamp` check-only arm (0 client errors, breaker never opened). Narrowed: see "Narrowed claims" |
| 7 | The breaker | core `TestBreakerOpensOnConnectionFailuresSendsOneTrialAndRecovers`, `TestHalfOpenSendsNoTrialWhileNoWorkWasRefused`, `TestZeroReplyDeadlineSetsSuspectButDoesNotCountTowardTheBreaker`, `TestBreakerOpeningRefusesQueuedWorkOnlyWhenNothingIsUsable`; engine `TestCoreBreakerRefusesAtOnceWithNoDial`; e2e `pool-db-restart` (opens and closes, 53M03 answered in < 10 ms, trials ≤ half-open periods, goodput after the restart); SQL `pool-db-restart/recovery.sql`; ablation `TestAblationRestartWithoutTheBreakerAnswersNo53M03`; poolsrc `TestHealthIsDegradedWhileTheBreakerIsOpen`; repair round: `TestAFailedCheckOfAnIdleConnectionDoesNotCountTowardTheBreaker` (error, unexpected reply, timeout, loss), `TestATrialThatFailsOrALossWhileOpenRefusesQueuedWork`, `TestShutdownLeavesNoTimerWhileTheBreakerIsOpenOrTheRateLimits`, engine `TestCoreHalfOpenBreakerLetsARequestWaitForTheTrial`; adversarial `TestAdv_7_SQLFailureViaCheck` now holds |
| 8 | Release policies | e2e `pool-session-{always,on-switch,always-reseed,on-switch-reseed}` (`TenantCrossings` from the fake's statement log: 0 between tenants; same-tenant carry-over > 0 only under on-switch; re-seeds sent iff `lease.reseed: on`); canary `TestCanaryPoolOnSwitchIgnoringTheTagCrossesTenants` (`PlantIgnoreTag`: 74 crossings); engine `TestCoreOnSwitchLendsTheSameTenantAsIsAndResetsOnASwitch`, `TestCoreReseedFollowsTheResetAndIsJudged`; core `TestOnSwitchPrefersTheSameTenantAndResetsWhenItMustSwitch`; tier R `TestTierRPoolSessionStateUnderEachReleasePolicy` (Postgres 17, `test/oracles/dirty-check.sql`, the random-seed residue) |
| 9 | The dirty table | engine `TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses`, `TestCoreClientVanishedInsideATransactionIsRolledBackAndReused`, `TestCoreClientVanishedMidStatementCancelsTheStatement`, `TestCoreIdleInTransactionIsRolledBackWith25P03`; core `TestDirtyTableCancelsDrainsRollsBackAndResetsBeforeReuse`, `TestCleanupDeadlineClosesAndNoReplyNamesTheReason`; engine `TestCoreLifetimeEndingDuringAResetCountsTheResetOnce`; e2e `pool-vanish`, `pool-idle-in-tx`, `pool-slow-reader` (write deadline); model (dirty releases of every kind, then lend only after a completed reset) |
| 10 | Cancel | engine `TestCoreClientCancelReachesOnlyTheLentConnection`, `TestCoreCancelWhileQueuedAnswers57014`; core cap test; e2e `pool-cancel` (the fake applied exactly 1 cancel, for client 0; client 1's sleep and client 2's read untouched); shell `TestCancelRequestGoesOutOnASecondConnectionAndReportsDone`, `TestCancelToASilentDatabaseEndsAtTheCleanupDeadline`; tier R `TestTierRPoolCancelReachesTheRightStatementOnly` |
| 11 | Startup cap during a TLS storm | tier R `TestTierRPoolReconnectionStormWithClientTLS` (`clients_in_startup_peak` ≤ `limits.max_startup` 64 from the CSV file); chunk 6's `TestClientCapRefusesWith53300` |
| 12 | Fleet division | `TestShareSumsToAtMostTheTotalAndIsNeverZero` (every N 1–64, total 0–300); config cases "fleet total below instances", "fleet rate below instances"; `TestFleetDivisionSetsEachInstancesConnections` (an omitted `pool.connections` takes the share: 20/3 → 6, 10/5 → 2; an explicit one is kept, above the share refused) |
| 13 | Closing never waits | the engine emits `ActDBClose` and forgets the connection in the same step (no wait state exists); `pool-mass-blackhole` / `pool-check-hangs` (closes on blackholed connections hold nothing up; quiesce clean); shell: 100 ms write deadline before `Close` (`conn.abort`) |
| 14 | Replay and flush | `runPool` (two runs per seed: trace and state digests equal, flushed files byte-identical, round trip `Flush.Check`); core `TestReplayGivesTheSameActionsForTheSameOperations`; SQL files per scenario |
| 15 | `FuzzPoolOps` | `TestOpDecodingIsTotalBoundedAndLocal`, `TestFuzzPoolOpsCostIsBounded` (worst case < 5 ms), constant config seed (`modelSeed`), `scripts/fuzz-plant.sh` (`PlantResetClearsSuspect`; 2–8 s in 5 of 5 runs at load 13), `TestShrunkResetClearsSuspectOrderingBugReplays`, `TestNearMissSeedsPassAndAreOneByteFromThePlantedBug` (no committed seed fails under the plant) |

Steady-state row: `quietPool` after every pool scenario (lent, checking, cleaning, cancels in
flight 0; open+connecting peak ≤ cap; snapshot conservation); the model's `finish`
(nothing lent, checking, suspect or in flight, no timer with nothing open); the engine's
`Quiesce` on the core path (every connection idle or connecting, `pool.Quiesce`, no cancel
connection). The pool's memory: at most `cap` connection records and `max_cancels` cancel
records.

## Fuzz targets

| Target | Package | Seed source | Rate |
| --- | --- | --- | --- |
| `FuzzPoolOps` | `internal/pool` | no simulator; the pool's jitter stream uses the constant `modelSeed` (7); the input is never hashed | not simulation-level (no `internal/sim` dependency); 200–1,900/s observed on a loaded host |

Decoding: 3 bytes per operation (kind, two arguments), at most 64 operations, trailing bytes
ignored; the high bit of the first byte selects `on-switch`. The seed corpus holds 8 random
lists and two near-miss lists of the planted bug that pass under the plant (repair round:
the three padded near-misses diluted mutation and missed the 20 s budget). The reference
model now fails as soon as a connection loses its suspect flag without a check, after every
operation and while it settles the run, so ordinary inputs reach the plant. The pinned inventory in
`agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go` does not include it:
`TestAdv_6_GateSeedSweepCounts` and `TestAdv_7_InventoryAndHiddenHashImports` are expected red
until the orchestrator re-pins.

## G9 results

Tier S unless noted; seed 1; full per-seed checks in the tests named.

| G9 test | Scenario / test | Result |
| --- | --- | --- |
| 1 night trickle, remedies on | `pool-night-keepalive-idle-check` (idle_drop 1 h, a read every 5 s for 2 h, then 8 clients × 20 reads) | 0 client errors, `backend_no_reply` 0 (also `test/analysis/pool-night-keepalive-idle-check/no-reply.sql`) |
| 1 ablation, every remedy off | `TestAblationNightTrickleWithEveryRemedyOffHangsTheRamp` | the middlebox dropped 9 idle connections; the ramp hit 4 of them: 4 client errors (`08006` after the statement deadline), 4 no-reply deadlines — the invariant fails as required |
| 1 each remedy alone | `TestNightTrickleEachRemedyAloneCarriesTheRamp` | **keepalive alone**: no drop, 0 errors. **idle timeout alone** (180 s): the idle connections are closed long before the middlebox's hour (10 idle closes), the ramp is a cold start, 0 errors. **check alone** (re-run in the repair round, seed 1): 9 idle connections dropped by the middlebox, 4 caught by failed checks (the others expired idle first), **0 client errors, 0 `53M03`**, breaker never opened, 0 no-reply deadlines. Before the repair the failed checks counted toward the breaker and the ramp saw 123 `53M03` refusals. Each remedy alone carries the ramp; the test now requires zero client errors for the check-only arm too. |
| 2 silent mass death | `pool-mass-blackhole` | client errors ≤ 2 (the connections lent at the death); idle ones caught by suspect checks with no client error; goodput back within 0.5 s + 0.3 s + 1 s; dials per second ≤ rate + 1; canary `PlantNoSuspect`: 6 errors |
| 3 failover | `pool-mass-peer-forgot` | same assertions; errors arrive one network delay after the write |
| 4 polite idle close | `pool-polite-close` | 0 client errors; closes counted `peer_closed_idle` |
| 5 the check hangs | `pool-check-hangs` | check ends at 250 ms (`check_failed`, result `timeout`), the queued request served by a new dial within the check deadline + 100 ms, no client error, quiesce clean; canary `PlantNoCheckDeadline` |
| 6 server slots held | `pool-slots-held` (fake `max_connections` 5, slots held 4 s) | re-dials refused with `53300` while the slots are held, dials ≤ 10/s (the configured rate) throughout, recovery after the reap. Repair round: the refused dials open the breaker (the database does refuse new connections), so while it is open with nothing usable the loopers get `53M03` at once (92 on seed 1); the check allows `53M03` only together with refused dials and a breaker opening, other errors ≤ the 2 lent, and a client that starts after the reap must succeed |
| 7 tier R | `TestTierRPoolEmptyQueryCheckAndServerKeepalive` | the empty query against Postgres 17 returns exactly `EmptyQueryResponse` + `ReadyForQuery(I)`; `SHOW tcp_keepalives_idle/interval/count` on a pooled connection = 30/10/3; `ss -tnoi` shows a keepalive timer on the gateway's database sockets. The packet-drop rule (optional, root) was not built. The socket options are also read back with `getsockopt` in the fast test `TestKeepAliveDialerSetsTheDatabaseSocketOptions` (SO_KEEPALIVE 1, TCP_KEEPIDLE 30, TCP_KEEPINTVL 10, TCP_KEEPCNT 3; off → SO_KEEPALIVE 0). |
| 8 harness self-test | `TestPathFaultOnOneConnectionLeavesTheOtherUnchangedAndClearingRestores` | a blackhole on one connection leaves the other's replies identical to a never-faulted run; clearing idle_drop (and a keepalive shorter than the drop) gives a run identical to a never-faulted one |

Tier R (Postgres 17, the binary, client TLS on):

| Test | Result |
| --- | --- |
| Headline benchmark 1 (`TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy`) | **500 clients direct: 197 connected, 303 refused (`53300`: max_connections 200 less the 3 superuser-reserved slots), 197 database backends. Through the proxy: 500 connected, 0 refused, 16 database backends (pool 16; open+connecting peak 16), 500 queries ok.** |
| Reconnection storm with client TLS (`TestTierRPoolReconnectionStormWithClientTLS`) | 500 TLS clients dropped (reset) and reconnected: database backends peak 12 (pool 16), clients in startup peak 64 = `limits.max_startup`, 0 refused, 500 queries ok |
| Release policies (`TestTierRPoolSessionStateUnderEachReleasePolicy`, `test/oracles/dirty-check.sql` after each hostile lease) | always: 0 crossings, 0 same-tenant carry-over, seed residue present (other tenant and same tenant). on-switch: 0 crossings, 4 same-tenant carry-overs, residue present, 14 tenant switches. always + re-seed: 0 crossings, no carry-over, **residue gone**, 24 re-seeds. on-switch + re-seed: 0 crossings, 4 same-tenant carry-overs, residue gone across tenants and present within the tenant (lent as is, no reset, no re-seed), 14 switches = 14 re-seeds |
| Cancel (`TestTierRPoolCancelReachesTheRightStatementOnly`) | the cancelled `pg_sleep(10)` ended with `57014` in under 1 s; the other client's concurrent `pg_sleep(1.5)` completed; one cancel forwarded |

## Fault × path matrix: rows added (G5)

| Fault | Where | Client gets | Budget | Then the connection | Test |
| --- | --- | --- | --- | --- | --- |
| `blackhole` | statement outside a transaction, a safe read | `08006`, usable | `timeouts.statement` | cancel on a second connection, drain until `timeouts.cleanup`, closed `no_reply`; every connection suspect | `pool-mass-blackhole`, engine `TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses` |
| `blackhole` | a statement that could write, or `COMMIT` | `08007` | `timeouts.statement` | same | `TestCoreLossMidStatementInsideATransactionIsFatal08006UnlessItMayCommit` (the row choice) |
| `blackhole` / loss | inside a transaction, mid-statement, no `COMMIT`/`END`/`PREPARE` in the message | `FATAL 08006` (`lost-mid-statement-in-transaction`), closed | `timeouts.statement` (loss: at once) | dirty table or closed | same test |
| `blackhole` / loss | inside a transaction, mid-statement, the message may end it | `08007` | same | same | same test |
| `peer_forgot` | any of the three above | the same rows, one network delay after the write | — | closed (`error`), siblings suspect | `pool-mass-peer-forgot` |
| any death | idle ≥ `check_after_idle`, or suspect | nothing; the check absorbs it | `pool.check_timeout` | closed `check_failed` | `pool-check-hangs`, `pool-mass-*` |
| polite close (FIN) | idle | nothing | — | closed `peer_closed_idle` | `pool-polite-close` |
| client gone | mid-statement | — | `timeouts.cleanup` | cancel, drain, rollback if in a transaction, reset, reuse | `pool-vanish`, `TestCoreClientVanishedMidStatementCancelsTheStatement` |
| client gone | between statements inside a transaction | — | `timeouts.cleanup` | `ROLLBACK`, reset, reuse | `TestCoreClientVanishedInsideATransactionIsRolledBackAndReused` |
| client idle inside a transaction | between statements | `FATAL 25P03`, closed | `timeouts.idle_in_transaction` | `ROLLBACK`, reset, reuse | `pool-idle-in-tx`, `TestCoreIdleInTransactionIsRolledBackWith25P03` |
| client cancel | its statement running | `57014` from Postgres | — | normal release after the reply; not lent again before the cancel connection finished | `pool-cancel`, tier R cancel |
| client cancel | its request queued | `57014` (`cancelled-queued`), usable | — | — | `TestCoreCancelWhileQueuedAnswers57014` |
| database down | gate | `53M03` at once, no dial | — | breaker open; queued work answered `53M03` when nothing is usable | `pool-db-restart`, `TestCoreBreakerRefusesAtOnceWithNoDial` |

For the prose seat: `docs/extension-seams.md` still says "chunk 7" for the pool, breaker and
release rows and "recognised and counted only" for cancels; the facts are in
`docs/knowledge/pool-core.md` (new transport action `ActDBCancel`, event `EvDBCancelDone`, the
`CorePool` / `CoreBreaker` / `CorePolicy` seam implementations).

## Narrowed claims

For the orchestrator to accept or refuse. In each the code is right and the claim's wording
was broader than the behaviour; the code was not bent.

1. **Claim 1, the cap** (attack `TestAdv_1_CloseOutstanding`): open + connecting ≤ cap holds in
   the pool core at every instant, where a connection counts until the core **issues** its
   close (`ActClose`), not until the driver reports the descriptor closed: the core frees the
   slot in the step that emits the close and may dial a replacement in the same step. The
   engine's close is synchronous (stated limit 8): the shell closes the descriptor without
   waiting for the peer (a 100 ms write deadline for a TLS close-notify), so the physical
   overlap is at most one close in progress per replacement dial.
2. **Claim 3, no synchronized expiry** (attack `TestAdv_3_JitterCollisions`): jitter spreads
   expiries; it does not make them unique. The bound: each connection's idle timeout and
   lifetime is uniform per nanosecond in `d ± d·permille/1000`; with the default ±10% of
   30 min, 200 connections created together have at most 6 lifetimes ending within any 1 s
   (`TestJitterSpreadsTheExpiryOfConnectionsCreatedTogether`); with 1 permille of 1 s, 64
   connections created together have at least 60 distinct lifetimes
   (`TestJitterSpreadsSmallDurationsAndRefusesOnesTooSmallToSpread`; seed 3); e2e
   `pool-lifetimes`: at most 4 lifetime ends within 100 ms and no dial spike above the rate.
   A duration whose spread would be under 1 ms is refused at validation (see the repair
   round, `TestAdv_3_SubMicrosecondJitter`). After the repair (jitter drawn per nanosecond
   instead of in permille steps) the attack's own 64-connection case no longer collides at
   seed 70927, but the claim is stated as above, not as uniqueness.
3. **Claim 6, one death, bounded damage** (attacks `TestAdv_6_RecentIdleBlackhole`,
   `TestAdv_6_RecentIdlePeerForgot`): after a silent mass death, client errors ≤ connections
   lent at that instant **plus connections idle for less than `pool.check_after_idle` and not
   suspect**. Such a connection is lent without a check (stated limit 3, G5's exposure), so
   after a silent death the first statement on each one can fail once (`08006`); that
   failure marks every sibling suspect, so each dead connection costs at most one client
   error. Idle connections at or past `check_after_idle`, or suspect, are caught by the check
   with no client error. The later `53M03` that `TestAdv_6_RecentIdlePeerForgot`'s recovery
   query got is not covered by this narrowing and was fixed (repair round: half-open
   admits); that query now succeeds and the attack fails only on the narrowed `08006`.
4. **Claim 8, release policies** (attack `TestAdv_8_ReseedSameTenant`): the re-seed
   accompanies a reset. Under `lease.reset: on-switch` a connection returned to and lent
   again to the same tenant runs neither a reset nor a re-seed, by the policy's definition,
   so with `lease.reseed: on` the random-seed residue is gone across tenants and on every
   reset connection, and stays within one tenant's consecutive leases of an on-switch
   connection (tier R `TestTierRPoolSessionStateUnderEachReleasePolicy`, on-switch + re-seed:
   "residue gone across tenants and present within the tenant").

## Adversarial tests that stay red, and why

- `TestAdv_1_CloseOutstanding`, `TestAdv_3_JitterCollisions` (now passes),
  `TestAdv_6_RecentIdleBlackhole`, `TestAdv_6_RecentIdlePeerForgot`,
  `TestAdv_8_ReseedSameTenant`: narrowed above. `TestAdv_6_RecentIdlePeerForgot` now fails
  only on the narrowed first-statement `08006`; its recovery query succeeds.
- `TestAdv_3_SubMicrosecondJitter`: fixed by refusal, which the orchestrator's ruling
  allows. Its configuration (`MaxLifetime` 999 ns with 1 permille of jitter) is now refused
  by `pool.New` with `pool.max_lifetime: 999ns with pool.jitter_percent at 1 permille spreads
  by less than 1ms`, so the attack fails at its setup (`fresh` calls `t.Fatal` on the
  error) instead of at its assertion. The attack is not wrong; its premise ("validation
  accepts this duration") no longer holds.
- No adversarial test asserts something no claim says. The 33 attacks that held still hold.

## Repair round

Review 1 (Neon) and Astra's adversarial results, against the orchestrator's decisions.
Each "fails before" was checked by running the new test on a copy of `3cf6c92`.

| Finding / attack | Outcome | Commit | Regression test (fails before the fix) |
| --- | --- | --- | --- |
| R1 BLOCKER cancel done leaves the engine `dpCleaning` | fixed: `coreSyncAll` after `CancelDone` (event and fallback); `Engine.Invariants` checks every phase and the idle gauge against the core | `bd47280` | `TestCoreCancelDoneMakesTheConnectionIdleInTheEngineToo` (quiesce, reuse, unsolicited bytes: the nil-pointer panic); removing the sync also fails the old `TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses` through the new invariant |
| R2 BLOCKER lifetime during a reset counted twice | fixed: the completed reset leaves `dpResetting` before the core judges it | `bd47280` | `TestCoreLifetimeEndingDuringAResetCountsTheResetOnce` |
| R3 MAJOR fleet division never sets `pool.connections` | fixed: an omitted `pool.connections` takes `Share(total, N)` | `dcf192b` | `TestFleetDivisionSetsEachInstancesConnections` |
| R4 MAJOR expiry after the event | fixed: every exported operation expires what is due, and ticks the breaker, before applying itself | `a94fd47` | `TestEveryOperationExpiresWhatIsDueBeforeApplyingIt` (5 cases); attacks `TestAdv_4_ResetDeadlineEquality`, `TestAdv_4_LifetimeEqualityCheckout`, `TestAdv_5_LateCheckSuccess`, `TestAdv_9_CleanupDeadlineDrain` pass unchanged |
| R5 MAJOR drain reads a split message's tail as `ReadyForQuery` | fixed: a drain that starts inside a message seeds its header buffer with a non-`Z` marker | `bd47280` | `TestCoreDrainDoesNotTakeTheTailOfASplitMessageForReadyForQuery` (6- and 5-byte tails) |
| R6 MAJOR `FuzzPoolOps` misses its plant in 20 s | fixed: the model fails on a lost suspect flag at once and while settling; near-miss seeds that pass under the plant | `04e72e1` | `scripts/fuzz-plant.sh` (5 of 5 runs in 2–8 s at load 13, was 7–20 s and 2 of 3 before); `TestNearMissSeedsPassAndAreOneByteFromThePlantedBug` |
| R7 MAJOR config rules untested; sub-second keepalive with only the server keepalive on | fixed: whole seconds ≥ 1 s whenever either keepalive is on; tests for every rule | `dcf192b` | `TestPoolRulesFailClosedNamingTheKey` (18 cases), `TestKeepaliveWholeSecondsAreAcceptedAndSentToTheServer`, `TestWarningsNameEveryRiskySetting`; mutation check: deleting each of the G2/G3/G4 rules, the check-timeout nesting, the server-keepalive condition or the fleet division fails a test |
| R8 MAJOR failed idle checks open the breaker (ruled) | fixed as ruled: a failed check of an idle connection that is not the trial does not count; a failed trial re-opens; dials count as before. Decision 5 and stated limit 4 rewritten; check-only night arm re-run: 0 client errors (was 123 `53M03`) | `a94fd47`, `41cc4bc` | `TestAFailedCheckOfAnIdleConnectionDoesNotCountTowardTheBreaker`; `TestNightTrickleEachRemedyAloneCarriesTheRamp` now requires 0 errors for the check-only arm; attacks `TestAdv_6_OldIdleCheckRefusal`, `TestAdv_7_SQLFailureViaCheck` pass unchanged. `pool-slots-held`'s check changed (G9 row 6): there the database refuses dials, so `53M03` is right |
| R9 MINOR `as_is` check dropped from 13 tier R SQL files | fixed: restored (no ablation arm shares those scenario names) | `d776629` | tier R only (G6/G7) |
| R10 MINOR no refusal on a failed trial or a loss while open; `Next` after `Shutdown` | fixed | `a94fd47` | `TestATrialThatFailsOrALossWhileOpenRefusesQueuedWork`, `TestShutdownLeavesNoTimerWhileTheBreakerIsOpenOrTheRateLimits` |
| R11 MINOR `internal/obs/simrun` serial (942 s in G3) | fixed: scenarios and per-scenario SQL on 4 workers, collected in order; heavy pool scenarios skipped under `-short` (their SQL left to G7's sweep) | `9164c1e` | measured: 588 s → 256 s under `-short` at load 12 |
| `TestAdv_2_NonDivisorRate` | fixed: the creation interval rounds up | `a94fd47` | `TestCreationRateBoundHoldsForRatesThatDoNotDivideASecond`; attack passes |
| `TestAdv_3_SubMicrosecondJitter` | fixed by refusal at validation naming the key; jitter now drawn per nanosecond | `a94fd47` | `TestJitterSpreadsSmallDurationsAndRefusesOnesTooSmallToSpread`; config case "lifetime too short for its jitter". Attack fails at setup (above) |
| `TestAdv_10_LateCancelAfterExpiry` | fixed: an expired cancel taints its connection; closed `cleanup_failed` at its next return, never lent | `a94fd47` | `TestAnExpiredCancelNeverLetsItsConnectionBeLentAgain`; the reference model fails on such a lend (it caught the old code in the property test); attack passes |
| `TestAdv_1_CloseOutstanding` | narrowed (Narrowed claims 1) | — | — |
| `TestAdv_3_JitterCollisions` | narrowed (Narrowed claims 2); passes after the jitter change | — | — |
| `TestAdv_6_RecentIdleBlackhole`, `TestAdv_6_RecentIdlePeerForgot` | narrowed (Narrowed claims 3); the recovery `53M03` was a defect and is fixed: a half-open breaker admits requests to wait for its one trial | `e81050b` | `TestCoreHalfOpenBreakerLetsARequestWaitForTheTrial`; the attack's recovery query succeeds |
| `TestAdv_8_ReseedSameTenant` | narrowed (Narrowed claims 4) | — | — |
| `TestAdv_4_*`, `TestAdv_5_LateCheckSuccess`, `TestAdv_9_CleanupDeadlineDrain` | fixed with R4 | `a94fd47` | see R4 |
| `TestAdv_6_OldIdleCheckRefusal`, `TestAdv_7_SQLFailureViaCheck` | fixed with R8 | `a94fd47` | see R8 |

`pool-slots-held`'s check and late client: `73b9539`. Docs and notes: the commit after these.

## Stated limits

1. **G10 `TCP_USER_TIMEOUT` declined for v1** (the brief's ruling): Linux only, set through the
   dialer's control hook, one more simulator knob. A write into a blackhole is bounded by the
   statement deadline (30 s), not 10 s; and like any error it would still mean "sent, delivery
   unknown" (G8).
2. **Keepalive in tier S is modelled, not simulated**: the relay treats "keepalive interval <
   idle_drop" as "the middlebox entry never expires". Real probes, their loss and the kernel's
   count are checked only in tier R by reading the options back.
3. **The exposure G5 names remains**: a connection that died less than `check_after_idle` ago
   (30 s) and is not suspect costs its first client one statement deadline; G6 bounds it to
   one error per dead connection (Narrowed claims 3).
4. **Check alone after a mass silent death** (G9 test 1, check-only arm; rewritten in the
   repair round): the failed checks close the dead connections and do not count toward the
   breaker, so the ramp is served by replacement dials with no `53M03` (numbers in the G9
   table). The limit that remains: the ramp waits for those dials (creation rate, connect
   time) and for one check per dead connection; no statement hangs. If the database is
   really down the replacement dials fail and the breaker opens on them.
5. **FIN and reset are not told apart** on an idle connection (the transport has one close
   event); both are `peer_closed_idle` and neither counts toward the breaker.
6. **Cancel connections are outside the cap**; at most `pool.max_cancels` (16) at once, which
   the fleet reserve must cover.
7. **Heavy pool scenarios** (`pool-hard-cap`, `pool-storm`, `pool-lifetimes`,
   `pool-night-keepalive-idle-check`) run 3 seeds, also in the 200-seed sweep.
8. **The engine's database close is synchronous**: the pool forgets a connection when it emits
   `ActDBClose`; the shell closes the descriptor without waiting (100 ms write deadline for a
   TLS close-notify), so "counts until closed" is "counts until the close is issued".
9. **The idle-in-transaction deadline exists only with the pool core**; the fixed set keeps
   chunk 6's `timeouts.client_idle`.
10. **The random-seed residue is shown only in tier R**; the fake has no `random()` state.

## Deviations

- **Chunk 6 tests changed for the intended new behaviour of the binary** (the binary now runs the
  pool core): `TestTierRClientVanishes` asserts the two vanished leases went through the
  dirty-connection table (rolled back, reset, reused) instead of "2 connections closed";
  `TestTierRQueueFull` warms the pool with one query first (the core opens connections on
  demand, so the first request otherwise queues too); `TestTierRDatabaseLegTLSModes` accepts
  `53M03` as well as `53M01` for the wrong CA (the breaker opens after five connect failures
  inside the queue wait); `realConservation` and the thirteen `test/analysis/tierr-*/resets.sql`
  use the core's lease accounting (dirty outcome, resets not at release). The config test case
  "fleet remainder" became "fleet total below instances" and "fleet rate below instances"
  (rounding down, brief section on fleet division). `proxy.PlantNoReset` on the core path maps to
  `on-switch` with the tag ignored (lend as is, never reset), so the tier R ablation still works.
- **The chunk 6 tier S scenarios still run the fixed set**; their pool re-runs are
  `pool-results`, `pool-tx-integrity`, `pool-slow-reader`, `pool-vanish`.
- **"Database restart" in tier R** is not built (stopping the shared container mid-run would
  break the other tier R packages); the breaker is shown in tier S (`pool-db-restart`) and at
  engine level.
- **Synchronized lifetimes, cold start and slow consumer in tier R** are not separate tier R
  tests: the cold start is part of every tier R test (the pool starts empty), the slow reader
  is chunk 6's `TestTierRSlowReader` (now on the core), lifetimes are tier S only.
- **`idle_age_at_lend` and `lease_time` histograms** are exported only with `obs.detail` (as
  chunk 6's histograms).
- **G9 test 7's packet-drop rule** (optional) is not built.
- **Sweep**: pool scenarios run at most 20 seeds in a sweep, heavy ones 3 (stated limit 7);
  in `-short` heavy ones are skipped and the others run one seed.
- **Repair round, `pool-slots-held`**: its check no longer borrows claim 6's silent-death bound
  (that bound is for a healthy database; here the database refuses new dials for 4 s, the
  breaker opens on them and `53M03` is the intended answer). It now bounds errors other than
  `53M03` by the 2 lent connections, allows `53M03` only with refused dials and a breaker
  opening, and has one more client after the reap (the loopers spend all their steps on fast
  refusals before recovery). Commit `73b9539`.
- **Repair round, `internal/obs/simrun` under `-short`**: the four heavy pool scenarios
  (`pool-hard-cap`, `pool-storm`, `pool-lifetimes`, `pool-night-keepalive-idle-check`) are not
  run and their SQL files are counted as left to the sweep (G4 + G7 run them over 3 seeds);
  without `-short` (G3) everything runs, concurrently.
- **Repair round, half-open admission** (not a review finding): found by the recovery query of
  `TestAdv_6_RecentIdlePeerForgot`, which the orchestrator required to succeed. Decision 6
  rewritten.
- **`TestAdv_6_GateSeedSweepCounts` / `TestAdv_7_InventoryAndHiddenHashImports`** stayed green:
  `FuzzPoolOps` is not simulation-level (no `internal/sim` dependency), so the pinned
  simulator-level inventory did not change.

## Join with chunk 8

Join builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c07-pool`, worktree
`/root/Software/mini-gateway-work/c07`. `git merge --no-ff work/c08-admission` (chunk 8 stacked on
chunk 6a), one conflict resolution in merge commit `d572ed3`, then the join fix `f69522b` and the
join tests `67b39b1`. The plan's stage order (D1: classify, gate, coalesce, queue, lease, forward,
release) holds: chunk 8 owns the queue stage, chunk 7 the gate's breaker and the lease stage.

### Conflicts and how each was resolved

| File | Resolution |
| --- | --- |
| `internal/proxy/config.go` | `Config` carries both `Pool`/`Reseed` (chunk 7) and `Admission` (chunk 8); `Validate` runs both validations. |
| `internal/proxy/timers.go` | Both timer kinds: `tPool`, `tClientIdleTx` and `tAdmission`. |
| `internal/proxy/engine.go` | `Invariants` runs chunk 8's `admissionCheck` and then chunk 7's `coreAgrees` and `pool.Invariants` (decision c). |
| `internal/proxy/lease.go` | `enqueue`: chunk 8's rule (no direct lend for a `ShedQueue`) and chunk 7's arbitration rule (a failed direct checkout is not followed by a second checkout in the same call) both kept: `attempted` is set only on the direct-lend path, which a `ShedQueue` never takes, so with the admission queue every request is pushed and `dispatch` makes the one checkout; then `queueTouched` and, unless the direct checkout was tried, `dispatch`. |
| `internal/proxy/contracts.go` | Timeout rows: chunk 7's pool rows, then chunk 8's admission rows. Capacity rows: chunk 7's pool rows replace the fixed-set rows chunk 8 still carried; chunk 7's one-FIFO queue row is replaced by chunk 8's four queue rows. |
| `internal/config/validate.go` | `applyDefaults` applies both (`Pool.CheckTimeout`, `applyQueueDefaults`); `proxyCaps` passes `poolTimeouts()`, the reset policy, `Reseed`, `Pool` and `Admission`; `validate` runs `validateQueue` and `validatePool`. |
| `internal/obs/catalog/catalog.go`, `enginesrc/enginesrc.go` | Both components declared (`pool`, `admission`); health rules: database, `breaker`, then chunk 8's `admission`. |
| `internal/obs/simrun/engine_test.go`, `csvassert_test.go` | Chunk 7's parallel runner and heavy-scenario skip, then chunk 8's `AdmissionBaseline`; under `-short` the SQL files of chunk 8's overload scenarios count as skipped with chunk 7's heavy ones (one counter). |
| `test/e2e/spec.go`, `simtier.go`, `realtier.go` | `Spec` has both sets of fields; `engineConfig` passes `Admission` on both arms; the fingerprint appends `admFingerprint` to both arms (empty for `Rule == 0`, so every existing run name is unchanged); `GatewayOpts` has both sets of fields. |
| `scripts/chaos.sh` | Chunk 8's (6a's) version with chunk 7's `TestScenario(E2E|Pool)` pattern, also in the per-scenario failure detail. |
| `scripts/fuzz-plant.sh` | Chunk 7's `plant` function, with chunk 8's `FuzzAdmissionOps` / `admission-remove-order` as a third call (pattern `grep -E`, corpus clean-up removes up to the package's `testdata`). |
| `docs/timeout-budget.md`, `capacity-table.md`, `csv-schema.md` | Regenerated with `scripts/gen-docs.sh`, never hand-merged. |
| `docs/c4/`, `book/`, `README.md` | Chunk 8's side (they auto-merged to it exactly). |
| `agent_work/INDEX.md` | Both sides' rows (auto-merged). |

### Where the chunks assumed different things, and what holds now

**The one place the naive merge was wrong.** Chunk 7's breaker flush (`refuseQueued`) took each
queued request with `Queue.Pop` and counted it in the engine as `Shed` / `BreakerFlushed`. Behind
chunk 8's admission queue a pop is a *served* request (and records a served-wait sample), while
chunk 8's `Engine.Invariants` requires the core's shed total to equal the engine's `Shed`: the
first breaker flush with a standing queue broke it (`admission: the core shed 0, the engine
answered 3`). Fixed in `f69522b`, additively in chunk 8's core: shed reason `breaker`
(`admission.ReasonBreaker`), `Queues.Refuse(now, id)` and `ShedQueue.Refuse`. The flush with an
admission queue first sweeps the core at the instant (due sheds are answered `53M01` as rule sheds,
"time passes before the event"), then refuses every still-queued request in request-id order
(arrival order) through `Refuse` and answers it `53M03`. A refusal is counted as a shed with reason
`breaker`; it does not mark the tenant queue as shedding (the queue did not overload; the pool
component's `breaker` health rule reports the outage). The FIFO path (chunk 6/7 scenarios) is
unchanged. Canary: `proxy.PlantBreakerFlushPops` (tests only) restores the pop path;
`TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount` shows the scenario's invariant
catching it.

**A second one, with chunk 6a (which chunk 8 carries).** Chunk 7's error row
`lost-mid-statement-in-transaction` (`FATAL 08006`) had exactly the message of
`lost-in-transaction` (`mini-gateway: database connection lost inside a transaction`), so chunk
6a's `mgctl` could not name it from severity, code and message
(`internal/mgctl` `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage`: "matched as
lost-in-transaction"). Fixed in `8791d9a`: the row's message is now `mini-gateway: database
connection lost mid-statement inside a transaction`; code, severity, retry and connection facts
are unchanged, `reply-golden.txt` and `docs/error-contract.md` regenerated. Nothing in chunk 7
asserted the old text.

A client cancel of a queued request (chunk 7) now removes it at the current instant
(`RemoveAt`, the same call chunk 8's client-gone path uses) and re-arms the admission timer
(`queueTouched`); chunk 7 called `Queue.Remove`, which behind the admission core used the last
timestamp seen.

**(a) Where a request waits, and how it leaves.** There is one waiting place: its tenant's
admission queue (D3, no separate wait for a connection). While the breaker is **open** a request
never reaches the queue: the gate refuses it with `53M03` at once (counted `Rejected`,
`StageRefuse[breaker]`, no push). While the breaker is **half-open**, or the breaker is closed but
no connection is lendable (all lent, connecting, checking, cleaning or resetting, or the stale idle
ones are being checked), the request is pushed into its tenant queue and waits there behind the
trial or the preparation. Its exits:

| Exit | Answer | Counted (admission / engine) |
| --- | --- | --- |
| a connection is lent (the trial passed, a check passed, a reset or dial completed) | the reply | `served` / `StagePassed[queue]` |
| the rule's allowed wait passed (interval with no standing queue, target while standing; control law drop) | `ERROR 53M01` | `shed{rule}` / `Shed` |
| tenant ring full, total full, tenant cap | `ERROR 53M01` | `shed{overflow,total,tenants}` / `Shed` |
| `timeouts.queue_wait` backstop | `ERROR 53M01` | `shed{deadline}` / `Shed` |
| the breaker opens or re-opens (a failed trial, a loss) while it waits | `ERROR 53M03` | `shed{breaker}` / `Shed`, `BreakerFlushed` |
| client cancel | `ERROR 57014` | `removed` / `Failed`, `CancelsQueued` |
| client gone, or shutdown | none | `removed` / `Failed` without a reply |

The trial is not tied to the waiting requests: if the trial (a dial, up to
`timeouts.database_connect`) takes longer than the allowed wait, the waiting requests are shed
`53M01` by the rule and the trial still decides the breaker. With the defaults (interval 100 ms,
target 5 ms) and a database a few milliseconds away this does not happen in the tests.

**(b) Shutdown.** `Engine.Shutdown(now)` runs, in order: chunk 8's sweep of the admission core at
`now` (due sheds answered `53M01`); every client closed (queued requests removed as client-gone;
lent ones end through chunk 7's dirty-table / release path); chunk 7's `coreShutdown` (the pool
core's `Shutdown`, every pool connection closed `shutdown`, every cancel connection closed, the
pool timer cancelled); the fixed set's slots (none on the core path) and the redial timer; then
chunk 8's `closeAdmission` (empty tenant queues removed, `tAdmission` cancelled). Both chunks'
rules hold unchanged; neither needed to move. Test:
`TestJoinShutdownWithQueuedRequestsAndConnectionsInEveryPoolState` (two tenants queued while the
pool has connections connecting, idle, checking, lent, cleaning with a cancel in flight, and
resetting; after `Shutdown`: no wake armed, `Quiesce` passes, `Invariants` pass, admission
`pushes = served + shed + removed` with 2 removed, engine shed = admission shed, pool open 0,
connecting 0, cancels 0).

**(c) `Engine.Invariants`.** Every check of both runs: the snapshot conservation, lease pairing,
queue/waiting agreement and wire checks (chunk 6), then chunk 8's `admissionCheck` (no unanswered
shed, core shed total = engine `Shed`, the admission timer armed at the core's `Next`,
`Queues.Check`), then chunk 7's `coreAgrees` (every connection's engine phase against the core,
the idle gauge) and `pool.Invariants`. The admission check returns first only when it fails; the
core checks always run after it. With the breaker reason the admission equality now also covers
breaker flushes.

**(d) The binary's default, and the tests that had to change.** The binary runs the pool core
(chunk 7, no key for the fixed set) behind the timeout rule with adaptive LIFO (chunk 8, `queue.rule`
default `timeout`): `TestBinaryDefaultRunsThePoolCoreBehindTheTimeoutRule`. Tier S specs choose
their seams, so no tier S test changed: chunk 6 scenarios run the fixed set on `FIFO`, chunk 7's
pool scenarios the core on `FIFO` (`Spec.Admission` zero), chunk 8's admission scenarios the fixed
set behind the admission core; `pool-admission-restart` is the one tier S run of both. Changed:

| Test | Change | Why |
| --- | --- | --- |
| chunk 7 tier R pool tests (`TestTierRPool*`) | none in code; they now start the gateway with chunk 8's `GatewayOpts.Queue` default `RelaxedQueue` (interval 5 s, target 1 s) | the binary now sheds by the timeout rule; chunk 8 gave every tier R gateway the relaxed rule because those tests queue longer than 100 ms on purpose (the headline's 500 clients through 16 connections) |
| chunk 8 `TestTierRAdmissionOverloadWithPgSleep` | none in code; it now runs on the pool core (on-demand dials, the core's reset accounting) instead of the fixed set | the binary's pool is chunk 7's |
| chunk 6 `TestTierRQueueFull` | both chunks' edits kept: chunk 7's warm-up query, chunk 8's explicit interval 5 s / target 1 s | each chunk's default needed one of them |
| chunk 6 `TestTierRDatabaseLegTLSModes` | chunk 7's edit kept (accepts `53M03` as well as `53M01` for the wrong CA) | unchanged by the join |
| `internal/admission` `TestPropertyRunsExerciseEveryShedReasonAndLIFO` | the new reason `breaker` is excluded from the random-operation coverage, like `undrained` | only the engine reaches `Refuse`; it is covered by `TestRefuseCountsABreakerShedTheCallerAnswersAndKeepsTheQueueHealthy` and the engine join tests |
| `internal/proxy` `newCoreRig` | applies the package variable `coreAdmission` (zero by default, so every chunk 7 engine test runs as before) | lets `TestJoinEveryCoreEngineTestHoldsWithTheAdmissionQueue` rerun chunk 7's engine tests behind each product rule |
| `internal/obs/simrun` baseline tests | merged as above | both chunks changed them |

Tier R was not part of this join's required checks (G6 runs at landing); see "Join results" for
what was run.

**(e) Config keys.** One `File.validate` validates both chunks' keys and joins every error:
`proxyCaps().Validate()` (the engine config with the pool core's config and the admission
config), `validateLease` (`lease.reset`, `lease.reseed`), `validateQueue` (`queue.rule`,
`queue.lifo`), `validateFleet` and `validatePool` (`pool.*`, `breaker.*`, `backend.tcp_keepalive.*`,
`backend.server_keepalive`, `backend.path_idle_limit`). `queue.capacity` is chunk 8's total across
the tenant queues (it was chunk 6's one FIFO). No key of one chunk constrains a key of the other;
no cross-rule was added. `TestPoolAndQueueKeysAreValidatedTogether` gives four bad keys (two of
each chunk) in one file and requires each to be named. Both chunks' existing rule tests
(`TestPoolRulesFailClosedNamingTheKey`, `TestQueueKeysFailClosedNamingTheKey`, …) pass unchanged.

**No behaviour of either chunk was dropped or weakened, and no two behaviours truly conflict.**
The only change to chunk 8's code is additive (a shed reason, `Refuse`, a `ShedQueue` method, the
`shed` metric's help text); chunk 7's FIFO-path flush is unchanged, and the only other change to
chunk 7 is the new error row's message text (same code and facts).

### Join tests

| Test | What it proves |
| --- | --- |
| `internal/proxy` `TestJoinOverloadWithAHealthyPoolShedsByTheRuleAndLeavesThePoolUntouched` | 4 requests from two tenants queued behind one lent connection, shed `53M01` by the rule after the interval; the pool's dials, failures, closes, breaker, checkouts unchanged; the next request after the overload is served |
| `TestJoinBreakerOpeningRefusesTheStandingQueueWith53M03AndCountersAgree` | a standing queue (3 requests, 2 tenants) refused `53M03` when the lent connection's loss opens the breaker; admission `shed{breaker}` 3 = engine `BreakerFlushed`, shed totals equal, nothing removed, not shedding; an arrival while open is refused at the gate without a push or a dial; a half-open arrival is pushed and lent on the trial connection |
| `TestJoinHalfOpenTrialWithQueuedRequestsFromTwoTenants` | two tenants admitted while half-open wait behind one trial; trial passes: oldest served on the trial connection, the other on the next dial, nothing shed; trial fails: both `53M03`, `shed{breaker}` 2, breaker open again |
| `TestJoinCancelWhileQueuedLeavesItsTenantQueueAsRemoved` | `57014`, admission `removed` 1, nothing shed |
| `TestJoinShutdownWithQueuedRequestsAndConnectionsInEveryPoolState` | decision (b) above |
| `TestJoinQuiesceWaitsForTheQuietTenantQueueThenPasses` | `Quiesce` fails while the tenant queue is in its 16-interval quiet window and passes after it, with the pool's connection idle |
| `TestJoinEveryCoreEngineTestHoldsWithTheAdmissionQueue` | 18 of chunk 7's 20 engine core tests rerun behind the timeout rule and the control law (interval 10 s, target 5 s, so the rule never sheds), every event checked by both chunks' invariants. Left out: `TestCoreCancelDoneMakesTheConnectionIdleInTheEngineToo` and `TestCoreQuiesceAndShutdownLeaveNothing`, which call `Quiesce` right after traffic, when chunk 8's tenant queue is still in its quiet window (by chunk 8's design; the previous test covers it) |
| `internal/admission` `TestRefuseCountsABreakerShedTheCallerAnswersAndKeepsTheQueueHealthy` | `Refuse` counts reason `breaker`, leaves nothing for `TakeShed`, refuses only queued ids, keeps `Check` and conservation, does not set shedding |
| `internal/config` `TestBinaryDefaultRunsThePoolCoreBehindTheTimeoutRule`, `TestPoolAndQueueKeysAreValidatedTogether` | decisions (d) and (e) |
| `test/e2e` `TestScenarioPool/pool-admission-restart` | chunk 7's database restart (`checkRestart` unchanged: breaker opens and closes, `53M03` fast, goodput back) behind the default timeout rule with adaptive LIFO; admission `shed{breaker}` > 0 and = engine `BreakerFlushed`, shed totals equal, `pushes = served + shed + removed`, nothing queued, no tenant queue at the end. Seed 1: 968 pushes, 960 served, 4 breaker, 4 rule sheds. Replay law through `runPool`. SQL `test/analysis/pool-admission-restart/admission-pool.sql`. Canary `TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount` |

The breaker join tests were run against the naive merge's flush (the pop path): both fail on
chunk 8's invariant (`admission: the core shed 0, the engine answered 3` / `… 2`).

### Stated limits of the join

1. **Pop and unpop while a connection is being prepared.** With the pool core a checkout can
   refuse while connections are idle (they need a check, or the breaker is half-open); the admission
   queue then `Unpop`s. Chunk 8's stated limit 5 applies more often than with the fixed set: the
   served-wait sample and the load-gauge sample of such a pop stay recorded (`admission.unpops`
   counts them). The sample is never above the allowed wait (the head sweep runs first).
2. **A trial or a cold-start dial longer than the allowed wait** sheds the waiting requests
   `53M01` (above, (a)).
3. **Breaker refusals are admission sheds** (reason `breaker`, answered `53M03`): the `shed` metric
   by reason and the engine's `requests_shed` include them; the admission health rule does not.

### For the prose seat

`admission.Reason`'s doc comment ("shed with 53M01") and `ShedQueue`'s no longer cover reason
`breaker` / `Refuse` (`53M03`); `Queues.Refuse`, `AdmissionQueue.Refuse`, `ReasonBreaker` and
`PlantBreakerFlushPops` have no comments (builder rule). `docs/timeout-budget.md`'s check row still
says a failed check "counts toward the breaker" (chunk 7's arbitrated rule: a failed check of an
idle connection that is not the trial does not count).

### Join results

All on 2026-11-27, host load 5–8 (other agents and a landing gate's tier R on the machine).

| Check | Tree | Result |
| --- | --- | --- |
| `go build ./... && go vet ./...` | `8791d9a` | pass |
| `go test -count=1 ./internal/... ./test/... ./scripts/ ./cmd/...` | `67b39b1` | every package passed (`internal/obs/simrun` 379 s, `test/e2e` 318 s, `scripts` 31 s) except `internal/mgctl` `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage`: the row collision fixed in `8791d9a`; after it `internal/mgctl`, `internal/pgwire`, `internal/proxy`, `cmd/...` pass |
| adversarial `./agent_work/adversarial/c07-pool/...`, `c08-admission/...`, `c06a-operator-tools/...` (unchanged) | `8791d9a` | pass, pass, pass |
| `scripts/gen-docs.sh --check` | `8791d9a` | pass |
| 5-seed sweep: `DST_SEEDS=5 RUN_DIR=… go test -run '^(TestScenarioPool|TestScenarioAdmission.*)$' ./test/e2e` | `8791d9a` | pass (127 s); then `scripts/csv-assert.sh` per scenario directory over its `test/analysis/<scenario>/*.sql`: 28 directories (22 pool incl. `pool-admission-restart`, 6 admission), every SQL file 0 failures. Heavy pool scenarios ran 3 seeds, chunk 8's overload scenarios 1 (both by design) |
| chunk 7 gate extra (`agent_work/chunks/c07-pool/gate/extra.sh`, now with `TestCanaryJoin` and `TestJoin`) | `8791d9a` | pass; `fuzz-plant.sh`: `FuzzEngineOps` 13 s, `FuzzPoolOps` 3 s, `FuzzAdmissionOps` 2 s (budget 20 s each) |
| chunk 8 gate extra (`agent_work/chunks/c08-admission/gate/extra.sh`) | `8791d9a` | pass; `fuzz-plant.sh` 2 s / 5 s / 1 s; `admission-head-to-head.sh --check`: the table reproduced exactly |

## Commands

```
go test ./internal/pool/                                  # reference model, property tests, unit tests (-short: 1/6 of the runs)
go test ./internal/proxy/ -run TestCore                   # engine core path
go test ./test/e2e -run 'TestScenarioPool'                # pool scenarios, tier S (pinned seeds)
DST_SEEDS=200 RUN_DIR=/tmp/s go test -run '^TestScenarioPool$' ./test/e2e
go test ./test/e2e -run 'TestCanaryPool|TestAblation|TestNightTrickle|TestPathFault'
go test ./internal/pool -run '^$' -fuzz '^FuzzPoolOps$' -fuzztime 30s
scripts/fuzz-plant.sh                                     # both planted ordering bugs
MG_TIER_R=1 go test -run '^TestTierRPool' ./test/e2e      # chunk 7 tier R alone (starts its own container)
RUN_DIR=/tmp/r scripts/tier-r.sh                          # every tier R test
scripts/csv-assert.sh /tmp/s                              # SQL over the sweep's files
scripts/gen-docs.sh --check
scripts/gate.sh c07-pool
```

## Index rows

Added to `agent_work/INDEX.md` under "Phase 2, chunk 7 — pool".

## Size

Product Go (non-test files outside `test/`): +3,051 / −74 lines (`internal/pool` 1,498; the
engine's core path `internal/proxy/corepool.go` 653 plus 330 lines of hooks; config 197; obs
186; shell 55; proxysim 28; cmd 9). E2E harness (non-test files under `test/e2e`): +1,243.
Tests: +2,563. Inside the brief's 2,500–3,000 estimate for product code; no split.

## Gate result

Repair round: `scripts/gate.sh c07-pool` (build stage) once, at `4773c03`, 2026-11-27, clean
tree, host load 10–15. **overall fail, for two reasons, neither a product test**:

| Check | Result |
| --- | --- |
| G0 tools, gofmt, build, vet, tools-vet, tools-test, tidy | pass |
| G1 detlint (24 package patterns), importlint, boundlint, paniccheck | pass |
| G2 comments | pass |
| G3 unit (338 s; was 1,031 s in review 1) | **fail**: (a) `agent_work/adversarial/c07-pool`: the five attacks listed under "Adversarial tests that stay red" (`TestAdv_1_CloseOutstanding`, `TestAdv_3_SubMicrosecondJitter`, `TestAdv_8_ReseedSameTenant`, `TestAdv_6_RecentIdleBlackhole`, `TestAdv_6_RecentIdlePeerForgot`) — four narrowed claims and one refused configuration, awaiting the orchestrator; (b) `internal/pgwire` `TestLexOperatorRunsGrowLinearly`, one of the three wall-clock growth tests CR-3 replaces (not this chunk's). Every other package passed, including every chunk 7 package and the other adversarial packages. |
| G3 race | pass (4 package patterns) |
| G4 markers, sweep | pass (200 seeds per scenario, pool scenarios capped at 20 / 3; 924 s) |
| G5 fuzz | pass: 17 targets, 15 fuzz, 2 seed-sweep |
| G6 tier R | pass (285 s), including the restored `as_is` check of the 13 chunk 6 `resets.sql` files |
| G7 CSV assertions | pass (4,632 run directories, 80 scenarios with SQL files; 2,215 s) |
| G8 generated documents | pass |
| G9 site | pass |
| G10 chunk extra | pass: `FuzzEngineOps` found its planted bug after 2 s; `FuzzPoolOps` found `PlantResetClearsSuspect` after 2 s (budget 20 s each) |

Also run before the gate: `go test -count=1 ./...` (no `-short`), red only for the same five
c07 adversarial attacks (`internal/obs/simrun` 430 s, `test/e2e` 300 s);
`DST_SEEDS=5 go test -run '^TestScenarioPool$' ./test/e2e` green; `go test -short
./internal/obs/simrun` 256 s. `result.json` is not committed.
