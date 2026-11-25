VERDICT: NITS

## Findings

1. **MINOR** — `internal/pgwire/client_tenants.go:46` (`internal/pgwire/scram.go:29,158-160`)
   - **What is wrong:** `NewTenantTable` checks `len(tn.Verifier.Salt) == 0` with no upper bound on salt length, while `mockVerifier` caps `saltLen` at `maxMockSaltLen` (128 bytes) and silently falls back to `ScramSaltLen` (16 bytes) when `saltLen > maxMockSaltLen`. A uniform tenant table whose verifiers use a salt longer than 128 bytes produces a 16-byte mock salt for unknown users (differing `s=` length), and an arbitrarily long salt (e.g. > 1,500 bytes) can push `ScramServer.First`'s `serverFirst` output past `MaxScramMessage` (2,048 bytes).
   - **How I know:** Code inspection of `client_tenants.go:46-54`, `prestartup.go:126-132`, and `scram.go:29,154-168`. Standard SCRAM salts are 16 bytes (128 bits), so this does not break practical usage or the narrowed Claim 5 under normal verifiers.
   - **What a fix must achieve:** Enforce `len(tn.Verifier.Salt) <= maxMockSaltLen` in `NewTenantTable` (and `ParseVerifier`), or validate it in chunk 6's config loader alongside the mixed-shape check already noted in `ADVERSARIAL.md`.

2. **MINOR** — `internal/pgwire/snapshot_wire_test.go:152`
   - **What is wrong:** `databaseOnly["TLSFailed"][int(HandshakeViolation)] = true` is a stale skip entry. Repair commit `22effbd` added a database `HandshakeViolation` case to `busyStats` (`snapshot_wire_test.go:90`), so `Database.TLSFailed[HandshakeViolation]` now moves, but `TestWireCountersMoveAndConserve` still skips asserting it.
   - **How I know:** Code inspection of `snapshot_wire_test.go:90` and `152` (note `backend_events_test.go:23` already asserts `sumInt64(r.stats.Database.TLSFailed[:]) == 1` for `HandshakeViolation`).
   - **What a fix must achieve:** Remove `int(HandshakeViolation): true` from `databaseOnly["TLSFailed"]` in `snapshot_wire_test.go:152`.

## Mutation checks

All checks were run in a scratch copy (`/tmp/zgw-c04-wire-sessions-review2/mut`) with `go test -count=1`.

### Repair-round revert verifications (Review 1 findings & BROKEN attacks)

1. **Revert R1-F1 (`cycle.go:67`: `t.fr.SkipBodies(byte(c))` → `_ = c`)** — **CAUGHT** (`rc=1`, 2 failures: `TestCycleTrackerStreamsManyRowsWithoutBuffering`, `TestCycleTrackerPassesARowLargerThanItsMessageLimit`).
2. **Revert R1-F3 (`backend.go:129`: `if !s.inStartup()` → `if s.state == bsClosed`)** — **CAUGHT** (`rc=1`, 1 failure: `TestReadyBackendSessionIgnoresStartupEvents`).
3. **Revert R1-F4a (`client.go:189`: remove `s.scram = nil` in `close`)** — **CAUGHT** (`rc=1`, 1 failure: `TestClientSessionDropsSCRAMStateWhenItCloses`).
4. **Revert R1-F4b (`backend.go:138`: remove `s.scram = nil` in `fail`)** — **CAUGHT** (`rc=1`, 1 failure: `TestBackendSessionDropsSCRAMStateWhenItFails`).
5. **Revert R1-F5 / Attack 2 / Attack 7 on client (`client.go:207-216`: remove `normalized()` and deadline/violation switch)** — **CAUGHT** (`rc=1`, 4 failures: `TestHandshakeResultOutcomesAreClassifiedOnTheClientSide/{deadline,protocol-violation,unknown}`).
6. **Revert R1-F5 / Attack 2 / Attack 7 on database (`backend.go:203-212`: remove `normalized()` and deadline/violation switch)** — **CAUGHT** (`rc=1`, 4 failures: `TestHandshakeResultOutcomesAreClassifiedOnTheDatabaseSide/{deadline,protocol-violation,unknown}`).
7. **Revert R1-F6 (`pgwiretest/proxy.go:274`: drop `ss.afterHello = append(ss.afterHello, b[k:]...)`)** — **CAUGHT** (`rc=1`, 1 failure: `TestProxyKeepsBytesCoalescedWithTheClientHello`).
8. **Revert Attack 2 `ScramMessageBound` & `WireSCRAMBound` (`scram.go:240`: `scramTooLong` → `return false`)** — **CAUGHT** (`rc=1`, 2 failures: `TestClientRejectsAnOversizedSCRAMMessageInsideTheFrame`, `TestScramFunctionsRefuseMessagesAboveTheBound`).
9. **Revert Attack 5 `UnknownUserIterationOracle` (`prestartup.go:127`: `mockShape()` → `DefaultScramIterations, ScramSaltLen`)** — **CAUGHT** (`rc=1`, 1 failure: `TestMockChallengeTakesTheTableShapeByDefault`).
10. **Revert Attack 11 `RepeatedSSLAfterRefusal` (`fakepg/auth.go:108`: disable `if b.sslDone`)** — **CAUGHT** (`rc=1`, 3 failures: `TestFakeRefusesARepeatedEncryptionRequestAsPostgresDoes/{ssl-twice-off,ssl-after-upgrade}`).

Review 1 findings verified directly without code mutation:
- **R1-F2 (namespace & filenames):** `ReplyTracker` renamed to `CycleTracker`; `fuzz_test.go` → `frame_fuzz_test.go`; `export_test.go` → `client_export_test.go`; `grep` for all chunk 5 reserved prefixes in `internal/pgwire/` returns 0 matches (`NewReplyTracker` starts with `New`, outside chunk 5's prefixes).
- **R1-F7 (dead fields/enums):** `midMessage` and `ReasonTerminate` removed.
- **Narrowed Claim 9 (Attack 9):** `KeyIssuer.Issue()` computes `(k.base+k.issued)%processIDRange + 1` with `k.base < 2^31 - 1`, guaranteeing no uint32 overflow and no duplicate or zero PID within `2^31 - 1` issues of a single issuer.
- **Narrowed Claim 5 (Attack 5):** `TenantTable.mockShape()` computes the most common iteration count and salt length deterministically over `t.list` (slice order, no map iteration), matching every verifier in a uniform table (subject to Finding 1 for salts > 128 bytes).

### Core property mutations

1. **M1 — Framer oversized header check disabled (`frame.go:148`: `if bodyLen > limit` → `if false && bodyLen > limit`)** — **CAUGHT** (`rc=1`, 4 failures: `TestBackendGarbageSilenceAndEOF`, `TestOversizedLengthIsRefusedFromTheHeader`, `TestCycleTrackerSurprises`, `TestFramerRefusesFromTheHeaderAlone`).
2. **M2 — Plaintext startup allowed under `ClientTLSRequire` (`prestartup.go:78` disabled)** — **CAUGHT** (`rc=1`, 1 failure: `TestPlaintextStartupRefusedWhenRequired`).
3. **M3 — Bytes before handshake not rejected on database side (`backend.go:156-158`: `bsTLSWait` transitions to `bsAuth`)** — **CAUGHT** (`rc=1`, 1 failure: `TestBackendGarbageSilenceAndEOF`).
4. **M4 — SCRAM server skips `c=` channel-binding check (`scram_server.go:98` disabled)** — **CAUGHT** (`rc=1`, 4 failures: `TestGS2HeadersThroughTheMachine`, `TestScramClientAgainstServer/{n,y}-header-with-{y,n}-value`).
5. **M5 — SCRAM client skips server signature verification (`scram_client.go:85`: `!equalKeys` removed)** — **CAUGHT** (`rc=1`, 4 failures: `TestBackendFailureClasses/tampered-server-signature`, `TestScramClientAgainstServer/tampered-server-signature`).
6. **M6 — Unknown user rejected immediately before mock SCRAM (`prestartup.go:118-120` calls `authFailed`)** — **CAUGHT** (`rc=1`, 1 failure: `TestWrongPasswordAndUnknownUserLookAlike`).
7. **M7 — Client startup failure counter skipped on close (`client.go:185` removed)** — **CAUGHT** (`rc=1`, 2 failures: `TestPlaintextStartupRefusedWhenRequired`, `TestWireCountersMoveAndConserve`).

## Release It! checklist

- **Integration points:** Covered — both session machines model deadlines explicitly via `Deadline` events (`DefaultClientStartupTimeout` 5 s, `DefaultConnectTimeout` 2 s) and `HandshakeDeadline` outcomes, exercised by accept-and-silence scenarios on both legs (`wire-client-tls-stall`, `wire-db-silent-startup`, `wire-db-connect-stall`, `wire-db-handshake-stall`) with virtual-clock deadline assertions.
- **Blocked threads:** Covered — `internal/pgwire` is pure sans-I/O with no goroutines; `BackendSession` and `ClientSession` are immutable to startup events once ready; `pgwiretest` tears down both ends and passes `CheckQuiesce` on every exit path.
- **Slow responses:** Covered for startup — client departure during backend connect closes the backend session (`connection-lost`) and every refusal writes a bounded FATAL reply (or closes silently) immediately; in-flight query cancellation belongs to chunks 6–7.
- **Cascading failures:** Not applicable here because circuit breaker, pool, and health surface belong to chunks 6–7 (`BackendSession` supplies the failure `Class` consumed by chunk 7's breaker).
- **Chain reactions:** Not applicable here because multi-instance load shedding and admission queues belong to chunks 8 and 13.
- **Dogpile:** Not applicable here because pool connection-creation rate caps belong to chunk 7; post-fault recovery is verified in all 19 wire scenarios via `tenant1` starting at 8 s.
- **Self-denial attacks:** Not applicable here because this chunk runs no background sweeps, expirations, retries, or batch dials; all transitions are reactive to per-connection wire events.
- **Force multiplier:** Not applicable here because this chunk has no automatic feedback loop or adaptive controller.
- **Unbalanced capacities:** Covered — `Framer` bounds inspected message bodies (`MaxStartupPacket` 10,000, `maxAuthMessage` 2,112, `DefaultMaxMessage` 1 MiB) and skips relayed bodies (`MaxRelayedMessage` 1 GiB − 1); `pgwiretest` bounds open sessions (`MaxSessions` 4,096), pending write buffers (`maxPending` 64 KiB), and post-hello read buffering (at most one `readChunk` = 4 KiB via `helloHeld()`). `ss.early` in the test-only harness (`proxy.go:245`) is unbounded by design as noted in `NOTES.md:312`.
- **Scaling effects:** Covered — session machines hold per-connection state only (shared state is limited to `KeyIssuer` and `WireStats`), and `WireSnapshot` uses fixed-size arrays indexed by bounded enums with no unbounded labels.
- **Users:** Covered — startup parameters bounded by `MaxParams` (64), tenant table by `MaxTenants` (65,536), SCRAM messages by `MaxScramMessage` (2,048), attributes by `maxScramAttrs` (16), and iterations by `MaxScramIterations` (2^20); see Finding 1 for an uncapped salt length in `NewTenantTable`.
- **Unbounded result sets:** Covered — `CycleTracker` skips `DataRow` and other relayed bodies with a 0-byte body buffer (enforced by `TestCycleTrackerStreamsManyRowsWithoutBuffering` and `TestCycleTrackerPassesARowLargerThanItsMessageLimit`) and reports `CycleEndOfStream` with `InCycle: true` and `EndWithoutMarker++` when a reply terminates without `ReadyForQuery`.
- **Compounds:** Covered — SCRAM state (including the backend plaintext password) is now cleared on every failure/close path in both `ClientSession` and `BackendSession` (`s.scram = nil`), and no retry-plus-leak path exists here.

## Observability

- **`Snapshot()` present:** Yes — `WireStats.Snapshot() WireSnapshot` (`internal/pgwire/snapshot_wire.go:242`) returns a plain value copy of integer counters, gauges (`InStartup`, `InStartupPeak`), and fixed-size arrays for both `Client` and `Database` sides, with no wall clock and no maps. Simulator additions expose `fakepg.Server.AuthSnapshot()` and `workload.TenantSnapshot.Startup`.
- **Exporter / CSV registry mapping:** Chunk 3 (`internal/obs` collector and `scripts/csv-assert.sh`) is concurrent and absent from this branch; proposed metric names are documented in `NOTES.md:147-152`, and all 19 × 2 SQL files under `test/analysis/wire-*/` (`startup-failures.sql`, `tls-requests.sql`) target PLAN.md 7.7 schema version 1.
- **Counters move & conservation:** `snapshot_wire_test.go: TestWireCountersMoveAndConserve` checks every reachable scalar and array cell moves (see Finding 2 for one stale skip entry on `Database.TLSFailed[HandshakeViolation]`) and `WireSnapshot.Check()` passes; `TestWireConservationCatchesAMissingOutcome` verifies conservation catches missing outcomes; `pgwiretest/proxy.go:107` registers `Check()` as a simulator invariant; `workload/startup_test.go: TestStartupCountersMove` exercises simulator startup counters. Conservation asserts `Begun == Completed + sum(Failed) + InStartup` per side, `InStartup >= 0`, zero unreasoned failures, and `TLSRequests - sum(TLSAnswers) ∈ [0, InStartup]`. `HandshakeDone` now classifies `HandshakeDeadline` and `HandshakeViolation` accurately and clamps out-of-range enum values before indexing.
- **CSV-flush assertions:** All 19 wire scenarios have `startup-failures.sql` and `tls-requests.sql`, exercised via DuckDB (including negative checks with planted wrong numbers) in `TestWireAnalysisSQL` and mirrored by exact Go checks in `client_sim_test.go:238-291`.
- **Health:** Nothing new required by the brief.

## What I ran

- `go build ./... && go vet ./... && go test -count=1 ./...` — pass across all 18 packages (`internal/pgwire` 7.344s, `internal/pgwire/pgwiretest` 0.418s, `internal/sim/fakepg` 2.320s, `internal/sim/workload` 0.753s, `internal/sim/simtls` 0.006s, `internal/sim/world` 12.089s, `agent_work/adversarial/c04-wire-sessions` 0.207s: 34 passed, 0 failed, 1 skipped/settled).
- `agent_work/chunks/c04-wire-sessions/gate/extra.sh` — pass: SQL assertions with DuckDB on 19 wire scenarios (8.410s), early-bytes canary (0.046s), 19 cross-process digests identical.
- `DST_SEEDS=50 go test -count=1 -run '^TestScenarioWire' ./internal/pgwire` — pass (10.023s, full matrix on pinned seeds + rotating subset across 50 seeds, 2 replay runs each).
- `RUN_DIR=/tmp/zgw-c04-wire-sessions-review2/tr scripts/tier-r.sh` — pass against real Postgres 17.11 (7 passed, 0 failed, 0 skipped: `TestTierRScramClientLogsInToPostgres`, `TestTierRScramServerAcceptedByPsql` 9 psql cases, `TestTierRStartupFidelity` 12 steps identical, plus 4 chunk 2 tier R tests).
- In scratch copy `/tmp/zgw-c04-wire-sessions-review2/repo`:
  - `scripts/ci-fast.sh` — pass (G0 tools/gofmt/build/vet/tools-vet/tools-test/tidy; G1 detlint on 12 package patterns / importlint / boundlint / paniccheck; G3 unit `-short` 32s / race).
  - `GATE_ONLY=G2 scripts/gate.sh c04-wire-sessions` — pass (`comments compared with baseline a9205f3532e4461f10a47830e4d63e6e10fec2bd`, 0s).
  - Timed fuzzing (`-fuzztime 5s` each): `FuzzFramer` 39,909 execs (6.041s), `FuzzClientSession` 3,118 execs (6.206s), `FuzzBackendSession` 27,871 execs (5.360s), `FuzzCycle` 9,568 execs (5.460s) — all pass.
- In scratch copy `/tmp/zgw-c04-wire-sessions-review2/mut`: 10 repair-revert checks + 7 core property mutation checks — all 17 caught.
- Namespace prefix grep on `internal/pgwire/` — 0 matches for chunk 5 reserved prefixes; `go.mod` has 1 requirement (`github.com/jackc/pgx/v5 v5.13.0`).
- `git status --short && git diff --stat` in `/root/Software/mini-gateway-work/c04-review` — clean working tree (0 repository files modified).


NITS CLOSED by the orchestrator, 2026-11-25: the two MINOR items (no upper bound on a verifier salt length against the mock challenge cap of 128 bytes; one stale skip entry in the counters-move test) falsify no claim. The one repair round is used, so they are carried forward as required fixes in the chunk 6 brief (section "Carried from chunk 4"), where the tenant table gets its config validation.
