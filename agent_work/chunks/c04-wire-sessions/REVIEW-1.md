VERDICT: FAIL

## Findings

1. **MAJOR** — `internal/pgwire/cycle_test.go:155-192` (`internal/pgwire/cycle.go:64-70`)
   - **What is wrong:** `TestCycleTrackerStreamsManyRowsWithoutBuffering` cannot detect a regression where `ReplyTracker` stops skipping `DataRow` bodies and buffers them instead; no test in `internal/pgwire` fails if `t.fr.SkipBodies(byte(c))` is removed from `NewReplyTracker`.
   - **How I know:** In `/tmp/zgw-c04-wire-sessions-review1/repo`, replacing `t.fr.SkipBodies(byte(c))` with `_ = c` in `internal/pgwire/cycle.go:68` and running `go test -count=1 ./internal/pgwire` exits `0` with 0 failures. `TestCycleTrackerStreamsManyRowsWithoutBuffering` feeds 90-byte `DataRow` payloads (101 bytes each) and only checks `tr.BufferCap() <= 1024`; because `Framer` resets `f.body = f.body[:0]` per message, buffering every `DataRow` only grows `f.body` to ~101 bytes (≤ 1024), contradicting `NOTES.md:125` ("passes with a body buffer of 0 bytes") and failing to test the brief's "without buffering them" property. `TestCycleTrackerSurprises` only tests that a `MaxRelayedMessage+1` `'D'` message is rejected, which also passes when `SkipBodies` is disabled because `DefaultMaxMessage < MaxRelayedMessage`.
   - **What a fix must achieve:** Assert `tr.BufferCap() == 0` while streaming `DataRow` / `RowDescription` / `CommandComplete` messages (before `ReadyForQuery`), and test a `DataRow` larger than `DefaultMaxMessage` (e.g. `DefaultMaxMessage + 1024` or with a small `maxMessage` like `128` and a `4096`-byte `DataRow`) so that disabling `SkipBodies` deterministically fails both the zero-buffer check and the oversized-skipped-body pass-through check.

2. **MAJOR** — `internal/pgwire/cycle.go:49`, `internal/pgwire/fuzz_test.go`, `internal/pgwire/export_test.go`
   - **What is wrong:** `internal/pgwire/cycle.go:49` exports `type ReplyTracker struct`, violating the builder brief's explicit concurrent-chunk namespace reservation (`zgateway-p2-c04-wire-sessions.md:255-260`): chunk 5 owns the exported prefix `Reply` in the shared `internal/pgwire` package and chunk 4 must use none of chunk 5's prefixes. Additionally, `internal/pgwire/fuzz_test.go` and `internal/pgwire/export_test.go` sit outside chunk 4's owned filename globs (`frame*.go`, `prestartup*.go`, `client*.go`, `backend*.go`, `scram*.go`, `cycle*.go`, `params*.go`, `snapshot_wire*.go`, `fatal*.go`) and risk merge collisions with chunk 5 (which also adds fuzzing and tests to `internal/pgwire`).
   - **How I know:** `grep -rnE '^(type|func|var|const) +(Lex|Token|Classif|Reject|ErrorRow|ErrRow|Reply|SyncDiscard|Allow|lex|cls|rej|erow|rpl)' internal/pgwire/` returns `internal/pgwire/cycle.go:49:type ReplyTracker struct`, and `ls internal/pgwire/` shows `export_test.go` and `fuzz_test.go`.
   - **What a fix must achieve:** Rename `ReplyTracker` / `NewReplyTracker` to a non-reserved name (e.g. `CycleTracker` / `NewCycleTracker`) and update all call sites and `NOTES.md`/`docs/knowledge/wire-sessions.md`; rename `fuzz_test.go` and `export_test.go` to chunk-4-prefixed filenames (e.g. `frame_fuzz_test.go` or `cycle_fuzz_test.go`, and `client_export_test.go`).

3. **MAJOR** — `internal/pgwire/backend.go:128-143`, `213-222`
   - **What is wrong:** `BackendSession.Deadline` and `BackendSession.PeerClosed` do not guard `!s.inStartup()`. `fail()` only checks `if s.state == bsClosed`, so calling `Deadline` or `PeerClosed` on an already-ready `BackendSession` (`bsReady`) transitions `s.state` to `bsClosed`, overwrites `s.class` with `ClassDeadline` / `ClassLost`, and emits a spurious `ActClose` — making `Ready()` become `false` and `Closed()` become `true` after `ActReady` and `StartupsCompleted++` were already recorded. By contrast, `ClientSession.Deadline` and `ClientSession.PeerClosed` (`client.go:223`, `230`) guard `!s.inStartup()` and are verified by `TestReceiveAfterReadyConsumesNothing`.
   - **How I know:** Code inspection of `backend.go:128-143` and `213-222`, confirmed by a repro test in `/tmp/zgw-c04-wire-sessions-review1/repo` calling `Deadline` and `PeerClosed` after a successful SCRAM login:
     - `BackendSession.Deadline after Ready: len(acts)=1 Ready=false Closed=true Class=deadline`
     - `BackendSession.PeerClosed after Ready: len(acts)=1 Ready=false Closed=true Class=connection-lost`
   - **What a fix must achieve:** Guard `BackendSession.fail` (or `Deadline` and `PeerClosed`) with `if !s.inStartup() { return acts }` so a ready session is immutable to startup events, and add a unit test covering `Receive`, `Deadline`, `PeerClosed`, and `HandshakeDone` after `BackendSession.Ready()`.

4. **MINOR** — `internal/pgwire/client.go:180-192`, `internal/pgwire/backend.go:128-143`
   - **What is wrong:** SCRAM exchange state (`s.scram`) is cleared on authentication success (`client.go:259`, `backend.go:299`) but is never set to `nil` in `ClientSession.close()` or `BackendSession.fail()`. On the backend side, `ScramClient` retains the plaintext database password string after a failed handshake. This contradicts the brief's steady-state row ("SCRAM exchange state (bounded, freed at completion or failure)") and `NOTES.md:235` ("freed at success or close").
   - **How I know:** Code inspection and repro test in `/tmp/zgw-c04-wire-sessions-review1/repo`:
     - `ClientSession after wrong-password close: Closed=true scram!=nil:true`
     - `BackendSession after tampered-sig fail: Closed=true scram!=nil:true`
   - **What a fix must achieve:** Set `s.scram = nil` in `ClientSession.close()` and `BackendSession.fail()`, and assert `s.scram == nil` after failure in tests.

5. **MINOR** — `internal/pgwire/client.go:202-214`, `internal/pgwire/backend.go:200-211`
   - **What is wrong:** `HandshakeDone` maps every non-`HandshakeCompleted` outcome — including `HandshakeDeadline` and `HandshakeViolation` — to `ReasonTLSFailed` / `ClassTLSFailed` instead of `ReasonDeadline` / `ClassDeadline` and `ReasonViolation` / `ClassViolation`, even though the brief states the handshake-result event carries "completed with a TLS version / failed with a reason / the deadline" and `HandshakeDeadline` is an enum member of `HandshakeOutcome`. In addition, indexing `s.st.TLSFailed[r.Outcome]` and `s.st.TLSCompleted[r.Version]` without bounds checks panics if a caller passes an out-of-range enum value.
   - **How I know:** Code inspection and repro test in `/tmp/zgw-c04-wire-sessions-review1/repo`:
     - `ClientSession.HandshakeDone(HandshakeDeadline): Reason=tls-failed`
     - `BackendSession.HandshakeDone(HandshakeDeadline): Class=tls-failed`
   - **What a fix must achieve:** Classify `HandshakeDeadline` as `ReasonDeadline` / `ClassDeadline` and `HandshakeViolation` as `ReasonViolation` / `ClassViolation` in `HandshakeDone` (matching `Deadline()`), clamp or validate `r.Outcome` and `r.Version` before array indexing, and add unit tests.

6. **MINOR** — `internal/pgwire/pgwiretest/proxy.go:253-268`
   - **What is wrong:** In the test harness's `takeHello`, if a simulated client sends the 16-byte `ClientHello` coalesced with subsequent bytes in one write, the first iteration schedules `ss.hsTimer` and returns `b[k:]`. `fromClient`'s loop immediately invokes `takeHello(b[k:])` while `ss.hsTimer.Pending()` is true, which returns `nil` and silently discards `b[k:]`.
   - **How I know:** Reasoning on `pgwiretest/proxy.go:235-268`: `takeHello` returns `b[k:]` on line 267 while `ss.tlsWait` remains `true`, so the loop re-enters `takeHello` and hits line 256 (`|| ss.hsTimer.Pending()`), returning `nil`.
   - **What a fix must achieve:** Retain trailing bytes arriving after `ClientHello` while `hsTimer` is pending (or stop reading until `handshakeDone`), and feed them once the handshake completes.

7. **MINOR** — `internal/pgwire/cycle.go:55`, `internal/pgwire/snapshot_wire.go:23`
   - **What is wrong:** Dead/unreachable definitions: `ReplyTracker.midMessage bool` (`cycle.go:55`) is never read or written; `ReasonTerminate` (`snapshot_wire.go:23`) is never emitted anywhere (`client.go:238` maps `MsgTerminate` during startup to `ReasonClientClosed`) and is forbidden by `WireSnapshot.Check()` (`snapshot_wire.go:264`).
   - **How I know:** Code inspection of `cycle.go`, `client.go`, and `snapshot_wire.go`.
   - **What a fix must achieve:** Remove `midMessage` and either remove `ReasonTerminate` or emit it consistently where intended.

## Mutation checks

All mutations were applied one at a time in `/tmp/zgw-c04-wire-sessions-review1/repo` and tested with `go test -count=1 ./internal/pgwire`:

1. **M1 — Framer oversized header check disabled** (`frame.go:148`: `if bodyLen > limit` → `if false && bodyLen > limit`): **CAUGHT** (`rc=1`, 12 failures including `TestFramerRefusesFromTheHeaderAlone`, `TestOversizedLengthIsRefusedFromTheHeader`, `TestCycleTrackerSurprises`, `TestBackendGarbageSilenceAndEOF`).
2. **M2 — Plaintext startup allowed under `ClientTLSRequire`** (`prestartup.go:78` disabled): **CAUGHT** (`rc=1`, 9 failures including `TestPlaintextStartupRefusedWhenRequired`, `TestWireCountersMoveAndConserve`, `TestScenarioWireStartup/wire-client-plaintext-refused`).
3. **M3 — Second `SSLRequest` allowed** (`prestartup.go:63` disabled): **CAUGHT** (`rc=1`, 9 failures including `TestSecondTLSRequestAfterTheUpgradeIsAViolation`, `TestWireCountersMoveAndConserve`, `TestScenarioWireStartup/wire-client-second-request`).
4. **M4 — SCRAM server `c=` channel-binding check skipped** (`scram_server.go:92` disabled): **CAUGHT** (`rc=1`, 5 failures: `TestGS2HeadersThroughTheMachine`, `TestScramClientAgainstServer/{n,y}-header-with-{y,n}-value`, `TestWireCountersMoveAndConserve`).
5. **M5 — SCRAM client server-signature check skipped** (`scram_client.go:79`: `!equalKeys` disabled): **CAUGHT** (`rc=1`, 5 failures: `TestBackendFailureClasses/tampered-server-signature`, `TestScramClientAgainstServer/tampered-server-signature`, `TestWireCountersMoveAndConserve`).
6. **M6 — Unknown user rejected immediately before mock SCRAM exchange** (`prestartup.go:118`): **CAUGHT** (`rc=1`, 1 failure: `TestWrongPasswordAndUnknownUserLookAlike`).
7. **M7 — Unknown backend message type rejected instead of passed through** (`cycle.go:118`): **CAUGHT** (`rc=1`, 3 failures: `TestCycleTrackerProbeShapes/unknown-type-passed-through`, `TestWireCountersMoveAndConserve`).
8. **M8 — Startup failure counter skipped on client close** (`client.go:185` removed): **CAUGHT** (`rc=1`, 71 failures across unit tests, conservation checks, and wire scenarios).
9. **M9 — Backend accepts `N` answer when TLS required** (`backend.go:192-194` calls `sendStartup` instead of `fail`): **CAUGHT** (`rc=1`, 2 failures: `TestBackendFailureClasses/tls-required-answered-N`, plus verify in `wire-db-tls-refused` when run).
10. **M10 — `ReplyTracker` buffers `DataRow` instead of skipping bodies** (`cycle.go:68`: `t.fr.SkipBodies(byte(c))` removed): **NOT CAUGHT** (`rc=0`, 0 failures — see Finding 1).

## Release It! checklist

- **Integration points:** Covered — startup and connect deadlines are modelled as `Deadline` events on both machines (`DefaultClientStartupTimeout` 5 s, `DefaultConnectTimeout` 2 s) and exercised by accept-and-silence scenarios on both legs (`wire-client-tls-stall`, `wire-db-silent-startup`, `wire-db-connect-stall`, `wire-db-handshake-stall`) verified by virtual-clock deadline checks.
- **Blocked threads:** Partially covered — `pgwiretest` cleans up both ends and passes `CheckQuiesce` on every exit path, and `internal/pgwire` is pure sans-I/O with no goroutines; however `BackendSession.Deadline`/`PeerClosed` after `Ready` erroneously emits `ActClose` (Finding 3).
- **Slow responses:** Covered for startup — client departure during backend startup closes the backend leg (`connection-lost`) and every refusal writes a bounded FATAL reply (or closes silently) immediately; query cancellation on the DB is chunk 6/7.
- **Cascading failures:** Not applicable here because circuit breaker, pool, and health surface belong to chunks 6–7 (`BackendSession` supplies the failure `Class` the chunk 7 breaker consumes).
- **Chain reactions:** Not applicable here because multi-instance load shedding and admission queues belong to chunks 8 and 13.
- **Dogpile:** Not applicable here because connection pool creation-rate capping belongs to chunk 7; recovery after fault removal is asserted in all 19 wire scenarios via `tenant1` starting at 8 s.
- **Self-denial attacks:** Not applicable here because this chunk performs no background sweeps, expirations, retries, or batch dials; all transitions are reactive to per-connection wire events.
- **Force multiplier:** Not applicable here because this chunk has no automatic feedback loop or governor.
- **Unbalanced capacities:** Covered — `Framer` bounds inspected message bodies (`MaxStartupPacket` 10,000, `maxAuthMessage` 2,112, `DefaultMaxMessage` 1 MiB) and skips relayed bodies (`MaxRelayedMessage` 1 GiB − 1); `pgwiretest` bounds open sessions (`MaxSessions` 4,096) and pending write backpressure (`maxPending` 64 KiB), except `ss.early` in the test harness (`proxy.go:244`) is unbounded.
- **Scaling effects:** Covered — session state machines are per-connection with no shared mutable state beyond `KeyIssuer` and `WireStats`, so guarantees survive at N instances; `WireSnapshot` uses fixed-size arrays indexed by bounded enums with no unbounded labels.
- **Users:** Covered — startup parameters bounded by `MaxParams` (64), tenant table by `MaxTenants` (65,536), SCRAM message/attributes/iterations by `MaxScramMessage` (2,048), `maxScramAttrs` (16), and `MaxScramIterations` (2^20); exceeding any bound refuses the connection.
- **Unbounded result sets:** Code skips `DataRow` bodies (0 bytes buffered in `ReplyTracker`) and reports `CycleEndOfStream` with `InCycle: true` / `EndWithoutMarker++` when a reply terminates without `ReadyForQuery`; however the unit test fails to enforce zero buffering on `DataRow` (Finding 1).
- **Compounds:** No retry-plus-leak compound exists in this chunk; one minor state-retention compound is `BackendSession.fail()` not nil-ing `s.scram` (Finding 4).

## Observability

- **`Snapshot()` present:** Yes — `WireStats.Snapshot() WireSnapshot` (`internal/pgwire/snapshot_wire.go:232`) returns a plain value copy of integer counters, gauges (`InStartup`, `InStartupPeak`), and fixed-size arrays for both `Client` and `Database` sides, with no time and no maps. Simulator additions expose `fakepg.Server.AuthSnapshot()` and `workload.TenantSnapshot.Startup`.
- **Exporter / CSV registry mapping:** Chunk 3 (`internal/obs` collector and `scripts/csv-assert.sh`) is built concurrently and absent from this branch. Proposed metric names are documented in `NOTES.md:140-145`, and 19 × 2 SQL files under `test/analysis/wire-*/` (`startup-failures.sql`, `tls-requests.sql`) match PLAN.md 7.7 schema v1.
- **Counters move & conservation:** `snapshot_wire_test.go: TestWireCountersMoveAndConserve` verifies every reachable scalar and array cell moves and `WireSnapshot.Check()` passes; `TestWireConservationCatchesAMissingOutcome` verifies conservation catches missing outcomes; `pgwiretest/proxy.go:107` registers `Check()` as a simulator invariant; `workload/startup_test.go: TestStartupCountersMove` exercises simulator startup counters. Conservation covers `Begun == Completed + sum(Failed) + InStartup` per side, non-negative `InStartup`, no unreasoned failures, and `TLSRequests - sum(TLSAnswers) ∈ [0, InStartup]`. Note Finding 5 (misclassification when `HandshakeDone` receives `HandshakeDeadline`/`HandshakeViolation`) and Finding 7 (unused `ReasonTerminate`).
- **CSV-flush assertions:** All 19 wire scenarios have `startup-failures.sql` and `tls-requests.sql` verified via DuckDB (including negative tests with planted wrong numbers) in `TestWireAnalysisSQL`, plus equivalent Go assertions in `client_sim_test.go:236-289`.
- **Health:** Nothing new required by the brief.

## What I ran

- `go build ./... && go vet ./...` — pass (0 errors).
- `go test -count=1 ./...` — pass across all 17 packages (`internal/pgwire` 6.455s, `internal/pgwire/pgwiretest` 0.263s, `internal/sim/fakepg` 1.843s, `internal/sim/workload` 0.486s, `internal/sim/simtls` 0.007s, `internal/sim/world` 11.303s).
- `agent_work/chunks/c04-wire-sessions/gate/extra.sh` — pass: SQL assertions with DuckDB on 19 wire scenarios (7.513s), early-bytes canary (0.038s), 19 cross-process digests identical.
- `scripts/ci-fast.sh` — pass (G0 tools/gofmt/build/vet/tools-vet/tools-test/tidy; G1 detlint on 12 package patterns / importlint / boundlint / paniccheck; G3 unit `-short` 19s); cleaned up generated `agent_work/ci/` afterward so repo remained unmodified.
- `DST_SEEDS=50 go test -count=1 -run '^TestScenarioWire' ./internal/pgwire` — pass (21.911s, 19 scenarios × 50 seeds × 2 replay runs).
- `RUN_DIR=/tmp/zgw-c04-wire-sessions-review1/tr scripts/tier-r.sh` — pass against real Postgres 17.11 (7 passed, 0 failed, 0 skipped: `TestTierRScramClientLogsInToPostgres`, `TestTierRScramServerAcceptedByPsql` 9 psql cases, `TestTierRStartupFidelity` 12 steps identical, plus 4 chunk 2 tier R tests).
- Fuzzing in scratch copy (`/tmp/zgw-c04-wire-sessions-review1/repo`, `-fuzztime 5s` each): `FuzzFramer` 27,999 execs (5.14s), `FuzzClientSession` 971 execs (6.14s), `FuzzBackendSession` 5,630 execs (5.31s), `FuzzCycle` 11,647 execs (5.47s) — all pass.
- 10 mutation checks + 3 repro tests in `/tmp/zgw-c04-wire-sessions-review1/repo` (results above).
- Comment audit across all 46 changed/added `.go` files — 0 Go comments added (6,683 product lines, 3,562 test lines across the diff's `.go` files).
- `git status --short && git diff --stat` — clean working tree.
