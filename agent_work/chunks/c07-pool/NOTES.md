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
3. **Creation rate**: a one-slot GCRA (`interval = 1 s / rate`, burst 1). Bound: in any
   window W, dials ≤ `floor(W·rate/1s) + 1`; consecutive dials ≥ `1/rate` apart. A half-open
   trial and the redials after a mass death go through the same limiter.
4. **`max_connecting` default 4**; `pool.max_cancels` default 16.
5. **Breaker**: consecutive connection-class failures, default 5, `open_for` 1 s. What
   counts: dial failure or connect deadline, check failure, loss of an idle connection by a
   reset or unexpected bytes, loss of a lent / resetting / cleaning connection. What does not:
   SQL errors (never seen by the pool), a zero-reply statement deadline alone, a polite close
   (FIN) of an idle connection (it marks the siblings suspect but does not count: a database
   whose `idle_session_timeout` closes idle connections is healthy). A dial, a passed check
   or a completed reset resets the count.
6. **Half-open trial only when wanted**: a trial is sent once a request was refused by the
   breaker or work is waiting, so an idle proxy against a dead database does not dial every
   `open_for`. When the pool is at its cap of (suspect) idle connections, the trial is a check
   of one of them.
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
| 1 | The cap holds at every instant | core: `TestPoolMatchesTheReferenceModelOnRandomOperations` (model checks open+connecting ≤ cap after every action), `FuzzPoolOps`, `Invariants` (registered by the engine with the simulator, after every event); e2e `pool-hard-cap` (1,000 clients at once: fake session peak, relay pair peak and open+connecting peak ≤ 8), `pool-storm`; SQL `pool-*/pool.sql` (every flush, value and high-water) and `pool-hard-cap/fake-peak.sql`; canary `TestCanaryPoolCapPlantBreaksTheHardCap` (plant `PlantNoCap`: "8 open + 1 connecting above the cap 8"); tier R `TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy`, `TestTierRPoolReconnectionStormWithClientTLS` |
| 2 | Creation limits hold (concurrent connects, dials per interval), also after a trial and a mass death | core: model (consecutive dials ≥ 1/rate), `TestCreationRateSpacesDialsAndConcurrentConnectsAreCapped`; e2e `pool-cold-start` (spacing at the relay), `pool-mass-*` and `pool-slots-held` (dials per second ≤ rate + 1); SQL `pool-cold-start/dial-rate.sql`; ablation `TestAblationColdStartWithoutTheCreationRateExceedsTheBound` (`PlantNoRate`: dials 7 µs apart) |
| 3 | No synchronized expiry (jitter ±10%) | `TestJitterSpreadsTheExpiryOfConnectionsCreatedTogether` (200 connections created together: at most 6 lifetimes within any 1 s); e2e `pool-lifetimes` (at most 4 lifetime ends within 100 ms, no dial spike above 16/s); ablation `TestAblationLifetimesWithoutJitterExpireTogether` (no jitter: 16 ends within 100 ms) |
| 4 | Nothing is lent in doubt | model (`lent` checks: suspect, idle ≥ check_after_idle without a check, no completed reset unless same-tenant tag under on-switch, a cancel in flight); `TestCheckStartsAtExactlyCheckAfterIdleAndEndsAtItsDeadline`, `TestOneFailureMakesEveryIdleSiblingSuspect`, `TestDirtyTableCancelsDrainsRollsBackAndResetsBeforeReuse`, `TestCancelsAreCappedAndAClientCancelOnlyReachesALentConnection`; engine `TestCoreChecksALongIdleConnectionOffTheRequestPath`; bytes after `ReadyForQuery` still close (chunk 6 tests unchanged) |
| 5 | The check never costs a client | engine `TestCoreCheckThatHangsEndsAtItsDeadlineWithNoClientError` (deadline at equality); e2e `pool-check-hangs` (zero client errors, the queued request served within the check deadline + 100 ms); canary `TestCanaryPoolCheckDeadlinePlantHoldsTheQueuedRequest` (`PlantNoCheckDeadline`: quiesce finds the connection still checking) |
| 6 | One death, bounded damage | e2e `pool-mass-blackhole`, `pool-mass-peer-forgot` (client errors ≤ the 2 connections lent at the death, goodput back within statement deadline + clean-up + 1 s); canary `TestCanaryPoolSuspectPlantCostsClientsAfterAMassDeath` (`PlantNoSuspect`: 6 client errors for 2 lent) |
| 7 | The breaker | core `TestBreakerOpensOnConnectionFailuresSendsOneTrialAndRecovers`, `TestHalfOpenSendsNoTrialWhileNoWorkWasRefused`, `TestZeroReplyDeadlineSetsSuspectButDoesNotCountTowardTheBreaker`, `TestBreakerOpeningRefusesQueuedWorkOnlyWhenNothingIsUsable`; engine `TestCoreBreakerRefusesAtOnceWithNoDial`; e2e `pool-db-restart` (opens and closes, 53M03 answered in < 10 ms, trials ≤ half-open periods, goodput after the restart); SQL `pool-db-restart/recovery.sql`; ablation `TestAblationRestartWithoutTheBreakerAnswersNo53M03`; poolsrc `TestHealthIsDegradedWhileTheBreakerIsOpen` |
| 8 | Release policies | e2e `pool-session-{always,on-switch,always-reseed,on-switch-reseed}` (`TenantCrossings` from the fake's statement log: 0 between tenants; same-tenant carry-over > 0 only under on-switch; re-seeds sent iff `lease.reseed: on`); canary `TestCanaryPoolOnSwitchIgnoringTheTagCrossesTenants` (`PlantIgnoreTag`: 74 crossings); engine `TestCoreOnSwitchLendsTheSameTenantAsIsAndResetsOnASwitch`, `TestCoreReseedFollowsTheResetAndIsJudged`; core `TestOnSwitchPrefersTheSameTenantAndResetsWhenItMustSwitch`; tier R `TestTierRPoolSessionStateUnderEachReleasePolicy` (Postgres 17, `test/oracles/dirty-check.sql`, the random-seed residue) |
| 9 | The dirty table | engine `TestCoreStatementDeadlineWithNoReplyCancelsDrainsAndReuses`, `TestCoreClientVanishedInsideATransactionIsRolledBackAndReused`, `TestCoreClientVanishedMidStatementCancelsTheStatement`, `TestCoreIdleInTransactionIsRolledBackWith25P03`; core `TestDirtyTableCancelsDrainsRollsBackAndResetsBeforeReuse`, `TestCleanupDeadlineClosesAndNoReplyNamesTheReason`; e2e `pool-vanish`, `pool-idle-in-tx`, `pool-slow-reader` (write deadline); model (dirty releases of every kind, then lend only after a completed reset) |
| 10 | Cancel | engine `TestCoreClientCancelReachesOnlyTheLentConnection`, `TestCoreCancelWhileQueuedAnswers57014`; core cap test; e2e `pool-cancel` (the fake applied exactly 1 cancel, for client 0; client 1's sleep and client 2's read untouched); shell `TestCancelRequestGoesOutOnASecondConnectionAndReportsDone`, `TestCancelToASilentDatabaseEndsAtTheCleanupDeadline`; tier R `TestTierRPoolCancelReachesTheRightStatementOnly` |
| 11 | Startup cap during a TLS storm | tier R `TestTierRPoolReconnectionStormWithClientTLS` (`clients_in_startup_peak` ≤ `limits.max_startup` 64 from the CSV file); chunk 6's `TestClientCapRefusesWith53300` |
| 12 | Fleet division | `TestShareSumsToAtMostTheTotalAndIsNeverZero` (every N 1–64, total 0–300); config cases "fleet total below instances", "fleet rate below instances" |
| 13 | Closing never waits | the engine emits `ActDBClose` and forgets the connection in the same step (no wait state exists); `pool-mass-blackhole` / `pool-check-hangs` (closes on blackholed connections hold nothing up; quiesce clean); shell: 100 ms write deadline before `Close` (`conn.abort`) |
| 14 | Replay and flush | `runPool` (two runs per seed: trace and state digests equal, flushed files byte-identical, round trip `Flush.Check`); core `TestReplayGivesTheSameActionsForTheSameOperations`; SQL files per scenario |
| 15 | `FuzzPoolOps` | `TestOpDecodingIsTotalBoundedAndLocal`, `TestFuzzPoolOpsCostIsBounded` (worst case < 5 ms), constant config seed (`modelSeed`), `scripts/fuzz-plant.sh` (`PlantResetClearsSuspect`), `TestShrunkResetClearsSuspectOrderingBugReplays` |

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
lists and three near-miss lists of the planted bug. The pinned inventory in
`agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go` does not include it:
`TestAdv_6_GateSeedSweepCounts` and `TestAdv_7_InventoryAndHiddenHashImports` are expected red
until the orchestrator re-pins.

## G9 results

GATE-RESULTS-PLACEHOLDER

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
   one error per dead connection.
4. **Check alone after a mass silent death opens the breaker** (G9 test 1, check-only arm): the
   failed checks are connection-class failures, so after enough of them the breaker refuses
   requests with `53M03` for `open_for`, although new dials would work. No statement hangs.
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

DEVIATIONS-PLACEHOLDER

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

SIZE-PLACEHOLDER

## Gate result

GATE-PLACEHOLDER
