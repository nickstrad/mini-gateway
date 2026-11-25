# Chunk 4 — Wire sessions: builder notes

Built by Opus on 2026-11-25 on branch `work/c04-wire-sessions` (worktree
`/root/Software/mini-gateway-work/c04`) from `/root/briefs/zgateway-p2-c04-wire-sessions.md`,
starting from `work/c02-sim-world` at `c80f08d`. Durable facts for later builders:
`docs/knowledge/wire-sessions.md`.

## What was built

| Deliverable | Where |
| --- | --- |
| Incremental framer (typed and untyped packets, header-only refusal, body skipping) | `internal/pgwire/frame.go`; encoders of every message the machines write: `frame_encode.go` |
| Proxy-made FATAL replies (and the old-protocol error, `NegotiateProtocolVersion`) | `internal/pgwire/fatal.go` (chunk 5 absorbs it at chunk 6) |
| Client-facing session machine | `client.go` (states, SCRAM exchange, success), `prestartup.go` (the exchange before startup, startup packet checks), `client_tenants.go` (tenant table, `KeyIssuer`), `params.go` (parameters, startup packet parser) |
| Database-facing session machine | `backend.go` |
| SCRAM-SHA-256 as pure functions | `scram.go` (verifier, derivation, nonce, GS2 rule), `scram_server.go`, `scram_client.go` |
| Query-cycle tracker (database replies) and the client query-phase tracker | `cycle.go` (`CycleTracker`, `FrontendTracker`) |
| Wire snapshot | `snapshot_wire.go` (`WireStats`, `WireSnapshot`, `Check`) |
| Startup-only proxy harness and the fidelity runner | `internal/pgwire/pgwiretest/` (`proxy.go`, `fidelity.go`, `fidelity_sim.go`) |
| Simulated TLS handshake | `internal/sim/simtls/` (new simulator package) |
| Fake Postgres: SCRAM, TLS answer, simulated handshake, protocol versions | `internal/sim/fakepg/auth.go` (new) + small edits (see "Simulator changes") |
| Workload client: SCRAM, TLS request, TLS faults | `internal/sim/workload/startup.go` (new) + small edits |
| End-to-end scenarios, canary, SQL checks | `internal/pgwire/client_sim_test.go`, `client_canary_test.go`, `client_analysis_test.go`; `test/analysis/wire-*/` (19 × 2 SQL files) |
| Tier R | `internal/pgwire/backend_tierr_test.go`, `internal/pgwire/pgwiretest/fidelity_test.go`; `scripts/tier-r.sh` (third container); recording `internal/pgwire/pgwiretest/testdata/startup-postgres17.txt` |
| Gate extra (G10) | `agent_work/chunks/c04-wire-sessions/gate/extra.sh` |
| Rules files (appended lines only) | `det-packages.txt`: `internal/pgwire`, `internal/sim/simtls`, `internal/pgwire/pgwiretest`; `detlint-allow.txt`: one line for `scram.go` (imports `crypto/hmac`, `crypto/pbkdf2`); `race-packages.txt`, `imports.txt`, `prose-paths.txt`, `.gitignore`: unchanged (no concurrency in product code, no `pgproto3` import, no generated contract document, nothing to ignore) |

`internal/pgwire` does not import `pgproto3`: every message the machines read or write is
coded by hand (a handful of formats), which keeps `StartupMessage`'s map ordering out of a
determinism-checked package. `go.mod` is unchanged.

## Interfaces (what chunk 6 calls)

- Events in: `Receive(acts, b) (acts, consumed)`, `HandshakeDone(acts, HandshakeResult)`,
  `Deadline(acts)`, `PeerClosed(acts)`; `BackendSession.Start(acts)`. Actions out (appended to
  `acts`): `ActWrite{Data}`, `ActStartTLS`, `ActReady{User, Key}` (client) /
  `ActReady{Params, Key}` (database), `ActCancel{Key}`, `ActClose{Reason | Class, Violation,
  Cause, Code}`.
- A machine consumes bytes only while it is in startup. `consumed < len(b)` means the machine
  is ready (the rest is query-phase input for the trackers) or closed (the rest is surplus,
  e.g. the smuggled bytes after an accepted `SSLRequest`). `TestReceiveAfterReadyConsumesNothing`.
- Trackers: `CycleTracker.QuerySent()` (`NewCycleTracker`; `NewReplyTracker` is a one-line alias kept only because the adversary's evidence tests call it) opens a cycle, `Receive` returns `CycleEvent`s (one
  per message: type, body length, stream offset of the end, `Known`, `InCycle`; semantic kind
  for `Z`, `E`, `S`, `N`, `A`), `PeerClosed` reports the end of stream (with or without the
  marker). `FrontendTracker.Receive` returns `FrontQuery{Body}`, `FrontTerminate`,
  `FrontExtended{Type}`, `FrontViolation`.
- Configs: `ClientConfig{TLS, Auth, Tenants, Database, SessionSettings, ServerParams, Random,
  Keys, MockSecret, MockIterations, MaxMessage, Stats}` with `Validate()`;
  `BackendConfig{TLS, User, Database, Password, SessionSettings, Random, MaxMessage, Stats}`.

## Decisions the brief left open

1. **Reply byte shapes (client side).** Every proxy-made reply is an `ErrorResponse` with
   exactly the fields `S` and `V` (severity), `C` (SQLSTATE), `M` (message), then the
   terminator; no detail, position or routine fields. Golden bytes: `testdata/golden.txt`.

   | Situation | Reply | Then |
   | --- | --- | --- |
   | `SSLRequest`, `client_tls.mode` off | `N` | waits for the next packet in plaintext |
   | `SSLRequest`, allow / require | `S`, `ActStartTLS` | no byte read until `HandshakeDone` |
   | GSS encryption request (first) | `N` | |
   | Plaintext startup, mode require | `FATAL 28000 TLS is required for this connection: SSL off` | close |
   | Protocol major < 3 | old-format `EFATAL:  unsupported frontend protocol 2.0: server supports 3.0 to 3.0\n\0` (Postgres 17's bytes) | close |
   | Protocol major > 3 | `FATAL 0A000 unsupported frontend protocol 4.0: server supports 3.0 to 3.0` | close |
   | Protocol 3.x, x > 0, or any `_pq_.` option | `NegotiateProtocolVersion` (newest 3.0, the `_pq_.` names) | login continues |
   | No `user` | `FATAL 28000 no PostgreSQL user name specified in startup packet` | close |
   | A startup parameter outside the allowed set | `FATAL 0A000 unsupported startup parameter "<name>": it differs from the pool's fixed session settings` | close |
   | Known or unknown user (SCRAM) | `AuthenticationSASL` offering `SCRAM-SHA-256` only | |
   | Wrong password **or unknown user** | `FATAL 28P01 password authentication failed for user "<user>"` after the full exchange | close |
   | `p=` GS2 header | `FATAL 08P01 SCRAM channel binding is not supported by this server` | close |
   | `c=` not matching the header | `FATAL 08P01 SCRAM channel binding check failed` | close |
   | Nonce changed in the final message | `FATAL 08P01 SCRAM nonce does not match` | close |
   | Another mechanism | `FATAL 08P01 client selected an invalid SASL authentication mechanism` | close |
   | Other malformed SCRAM | `FATAL 08P01 malformed SCRAM message` | close |
   | Wrong database (checked after authentication, as Postgres) | `R12 R0 FATAL 3D000 database "<db>" does not exist` | close |
   | Success | `R12 R0`, the parameter set, `BackendKeyData`, `Z I` in one write | `ActReady` |
   | Deadline in the SASL exchange | `FATAL 08P01 canceling authentication due to timeout` | close |
   | Deadline before or during the handshake, TLS failure, any violation before the startup packet was accepted, cancel request | nothing | close |
   | Violation after the startup packet (wrong message type, malformed SASL framing, oversized) | `FATAL 08P01 protocol violation: <kind>` | close |

   The unknown-user reply is the wrong-password reply with the other user name: same
   messages, same lengths (when the names have the same length), same SQLSTATE; the mock
   verifier's salt is derived from the user name and `MockSecret` (stable per name, like
   Postgres's mock salt). Its iteration count is `MockIterations` when set; left at 0 it is the
   tenant table's most common iteration count, and the mock salt has the table's most common
   salt length (`TenantTable.mockShape`; empty table: 4096 and 16 bytes). With a uniform table
   the challenge (`s=` length, `i=`) is the same for known and unknown users; see "Narrowed
   claims" for a mixed table.
2. **Allowed startup parameters** are `ClientConfig.SessionSettings` (default
   `client_encoding=UTF8`), compared by name case-insensitively and value case-insensitively.
   `user`, `database` and `application_name` are always accepted (`application_name` is
   ignored). `replication` and `options` are always refused. The database defaults to the
   user name, as in Postgres.
3. **Parameter-status set.** `ClientConfig.ServerParams` (the pool's collected set; the
   harness passes the first database connection's set) or `DefaultServerParams()` (Postgres
   17's 14 names). `session_authorization` is replaced by the tenant's user and
   `is_superuser` by `off`.
4. **Key data.** `KeyIssuer`: process id = (random base + n) mod (2^31 − 1) + 1, so unique for
   2^31 − 1 sessions of one issuer and never 0; secret = 4 random bytes. Never copied from a
   backend key.
5. **Startups counted.** A client startup is begun at `NewClientSession` (connection
   accepted). A cancel-request connection ends as reason `cancel-request` (it is not a
   failure of a real startup, but it must end under one reason for conservation).
6. **Failure classes (database side)** — each startup ends under exactly one:
   `tls-refused` (`N` when TLS is required), `tls-failed` (handshake failed or failed
   verification), `auth-failed` (wrong server signature, server skipped its signature,
   server `FATAL 28xxx`), `auth-unsupported` (md5, cleartext, GSS, SSPI, Kerberos, SCM, no
   `SCRAM-SHA-256` in the list), `too-many-connections` (`53300`), `server-fatal` (any other
   `ErrorResponse` during startup, SQLSTATE kept in `Action.Code`), `protocol-violation`,
   `deadline`, `connection-lost` (EOF or reset before ready).
   A handshake-result event ends startup by its outcome: failed, failed-verification or an
   out-of-range outcome → `tls-failed`; deadline → `deadline`; violation → `protocol-violation`
   (violation kind `bad-handshake`), on both sides, the same as the separate `Deadline` event.
   An out-of-range TLS version counts as `other`. A ready session ignores every startup event.
   **Client side reasons:** `client-closed`, `deadline`, `protocol-violation`, `tls-failed`,
   `plaintext-refused`, `protocol-version`, `parameter-refused`, `no-user`, `auth-failed`,
   `unknown-database`, `cancel-request`.
7. **GS2 header, client side.** `n,,` in plaintext; over TLS `y,,` only when the server did
   not offer `-PLUS`, `n,,` when it did. The brief says "`y,,` over TLS"; real Postgres 17
   refuses `y,,` with `FATAL 28000` when it offered `-PLUS` (tier R, fidelity step
   `y-header-over-tls-with-plus-offered`), so the rule follows RFC 5802 and libpq.
8. **GSS after the TLS upgrade** is answered `N` once; a second GSS request (or a second
   `SSLRequest`, before or after the upgrade) is a violation.
9. **What a violation in TLS wait does with the bytes.** The bytes are not consumed
   (`consumed` stays at 8 for an `SSLRequest` followed by anything) and the session closes
   with `bytes-before-handshake`. The same rule holds on the database side for bytes after the
   server's `S` (the CVE-2021-23222 class).
10. **Trust** is `AuthTrustForTestsOnly`; the zero `AuthMode` is SCRAM
    (`TestTrustIsANamedTestSettingOnly`). Unknown users get `28P01` in trust mode too.
11. **The cycle tracker never buffers row data.** Only `Z`, `E`, `S`, `N`, `A` bodies are
    buffered (bounded by `MaxMessage`); every other body is skipped as it arrives (limit
    1 GiB − 1, Postgres's), so a 20 MB reply of 200,000 rows passes with a body buffer of
    0 bytes. Unknown well-framed types are reported (`Known: false`) and passed through.
12. **Frontend query phase.** `Query` (body copied, bounded), `Terminate`, and every
    extended-protocol type (`P B E D C H S F d c f`, bodies skipped) reported as "extended, to
    be refused"; any other type (including `p` after login) is a violation.
13. **Database-side TLS faults and the simulated handshake** are fake configuration
    (`fakepg.Config.TLS`, changeable during a run by `Server.SetTLS`), not new `sim.Fault`
    kinds: adding fault kinds would renumber or extend chunk 2's fault table and its
    every-kind tests. The client-side TLS faults are `workload.Tenant.TLSFault`, per tenant.
14. **The startup-only proxy** (`pgwiretest`) dials the database only after the client is
    authenticated and holds the client's final bytes until the database is ready, so a
    database startup failure reaches the client as `FATAL 08006 could not connect to the
    database: <class>`. One database connection per client (no pool).
15. **SCRAM iteration counts in the simulator** are 16 (fake and tenant verifiers) to keep
    the sweep fast; unit tests use 64, the RFC vector and tier R use 4096.
16. **CSV metric names** (chunk 3 owns the registry; proposed here, used by the SQL files):
    component `wire`: `client_startups_begun`, `client_startups_failed{reason}`,
    `database_startups_failed{reason=<class>}`, `client_prestartup_requests{mode=tls|gss|cancel,
    outcome=S|N|none}`, `database_tls_answers{outcome}`; component `workload`:
    `startup_tls_requests`, `startup_tls_answered{outcome=S|N}`; component `fakepg`:
    `tls_answered{outcome=S|N}`. The SQL files read `metrics.csv` in the current directory.
17. **`gen-docs.sh`** is untouched: this chunk has no generated contract document.

## Claims and the tests that prove them

| # | Claim | Tests (file: function) |
| --- | --- | --- |
| 1 | Any fragmentation gives the same events and actions | `client_test.go`: every test via `requireEveryChunking` (whole, one byte, split at every boundary of every step, consumed counts equal); `backend_test.go` via `requireBackendEveryChunking`; `cycle_test.go: TestCycleTrackerProbeShapes`, `TestCycleTrackerSurprises`, `TestFrontendTrackerRecognisesQueriesTerminateAndExtended`; fuzz `FuzzFramer`, `FuzzClientSession`, `FuzzBackendSession`, `FuzzCycle` (whole vs fuzzer split vs one byte); scenarios `wire-one-byte`, `wire-one-byte-plain` |
| 2 | No panic, no loop, bounded memory; oversized refused from the header | `frame_test.go: TestFramerRefusesFromTheHeaderAlone`, `TestFramerTakesOnlyWhatItNeeds`; `client_test.go: TestOversizedLengthIsRefusedFromTheHeader`; `cycle_test.go: TestCycleTrackerStreamsManyRowsWithoutBuffering` (200,000 rows, body buffer exactly 0 while rows stream), `TestCycleTrackerPassesARowLargerThanItsMessageLimit` (4 KiB row through a 128-byte limit), `TestCycleTrackerSurprises`; `scram_bound_test.go: TestScramFunctionsRefuseMessagesAboveTheBound`, `TestClientRejectsAnOversizedSCRAMMessageInsideTheFrame`; `client_events_test.go`, `backend_events_test.go` (out-of-range handshake outcomes and versions); the four fuzz targets (buffer bounds, no progress-less loop, conservation) |
| 3 | Nothing between an accepted `SSLRequest` and the handshake result is interpreted; exactly 8 bytes; at most one upgrade | `client_test.go: TestBytesSentEarlyAfterAnAcceptedTLSRequest` (consumed = 8, every chunking, later delivery too), `TestSecondTLSRequestAfterTheUpgradeIsAViolation`; `backend_test.go: TestBackendGarbageSilenceAndEOF` (bytes after the server's `S`); scenarios `wire-client-smuggle`, `wire-client-second-request`; canary below |
| 4 | require: no plaintext startup; off: `N` and plaintext works; allow: both | `client_test.go: TestPlaintextStartupRefusedWhenRequired`, `TestTLSRequestAnsweredNWhenOff`, `TestPlaintextClientLogsInWithSCRAM` (off, allow), `TestDefaultStyleClientUpgradesWhenAllowedOrRequired`, `TestGSSThenTLSRequest`; scenario `wire-client-plaintext-refused`; tier R `TestTierRScramServerAcceptedByPsql` (9 psql cases over the three modes) |
| 5 | SCRAM server: only the password holder; no `-PLUS`; `n,,`/`y,,` with the right `c=`; `p=` refused; unknown user = wrong password; no password in the table | `scram_test.go: TestScramRFC7677ServerSide`, `TestScramClientAgainstServer` (15 cases), `TestVerifierHoldsNoPassword`, `TestScramServerRejectsAnotherMechanism`; `client_test.go: TestWrongPasswordAndUnknownUserLookAlike`, `TestUnknownUserWithTheRightProofForAnotherVerifierFails`, `TestGS2HeadersThroughTheMachine`, `TestOnlySCRAMSHA256IsOffered`; tier R psql cases (libpq's `y,,` over TLS accepted; wrong password and unknown user give the same text); `client_mock_test.go: TestMockChallengeTakesTheTableShapeByDefault` (uniform table, default mock: same `s=` length and `i=`) |
| 6 | SCRAM client: wrong server signature rejected; `SCRAM-SHA-256` chosen when `-PLUS` is offered | `scram_test.go: TestScramRFC7677ClientSide`, `TestScramClientAgainstServer` (tampered signature, nonce, `e=`); `backend_test.go: TestBackendChoosesSCRAMWhenPlusIsOfferedAndPicksTheGS2Header`, `TestBackendFailureClasses` (tampered signature, server skips its signature); tier R `TestTierRScramClientLogsInToPostgres` (over TLS with `-PLUS` offered) |
| 7 | Every startup failure ends in its class and reply within the deadline, counted under exactly one reason | `client_test.go` (`requireClose` checks the one reason and conservation in every failure test), `backend_test.go: TestBackendFailureClasses` (17 cases), `TestBackendGarbageSilenceAndEOF`; `snapshot_wire_test.go`; scenarios `wire-client-*`, `wire-db-*` (`verify`: the exact failure map, checked again at fault time + deadline + 50 ms on the virtual clock); `client_events_test.go: TestHandshakeResultOutcomesAreClassifiedOnTheClientSide`, `backend_events_test.go: TestHandshakeResultOutcomesAreClassifiedOnTheDatabaseSide`, `TestReadyBackendSessionIgnoresStartupEvents` |
| 8 | Cycle tracker: status byte and end of reply for every probe shape, no buffering of a reply, unknown types passed through, unsolicited `ParameterStatus` reported | `cycle_test.go: TestCycleTrackerProbeShapes` (single result, two result sets, error then ready, error in a transaction → `E`, `FATAL` then end, unsolicited `S`, unknown type, notice and notification), `TestCycleTrackerFatalEndsTheStreamWithoutAMarker`, `TestCycleTrackerStreamsManyRowsWithoutBuffering`, `TestCycleTrackerReportsUnsolicitedMessagesOutsideACycle`; the harness relays every scenario's queries through both trackers |
| 9 | Proxy key data unique per session of one `KeyIssuer` (see "Narrowed claims"), unrelated to the backend's | `client_test.go: TestProxyKeysAreUniqueAndFromTheIssuer` (2,000 issues, distinct per seed); `KeyIssuer` takes nothing from the backend; tier R backend key comes from Postgres separately |
| 10 | Deterministic and sans-I/O | G1 detlint on `internal/pgwire`, `internal/pgwire/pgwiretest`, `internal/sim/simtls` (no `crypto/tls`, `net`, `time.Now`, goroutines; one allow line for the SCRAM imports); `TestScenarioWireStartup` (two runs per seed, identical digests and snapshots); gate extra (two processes, identical digests) |
| 11 | The fake's startup conversation, TLS answers included, matches Postgres 17 | `pgwiretest/fidelity_test.go: TestTierRStartupFidelity` (12 steps live against Postgres 17.11, writes the recording), `TestStartupFidelityAgainstRecordedPostgres17` (CI), `TestKnownDifferencesAreTheRecordedOnes`; chunk 2's `TestTierRFidelity` still identical; `pgwiretest/fidelity_sim_test.go: TestFakeRefusesARepeatedEncryptionRequestAsPostgresDoes` (repeated `SSLRequest` / GSS request; live evidence: the adversary's `oracle-repeat-ssl.json`, Postgres 17.11) |

## Planted-bug canary

`ClientConfig.plantEarlyBytes` (unexported; set only by `client_export_test.go: PlantEarlyBytes`)
switches off the "no bytes between `S` and the handshake result" rule: the machine then
reads the surplus as if it had arrived over TLS (the CVE-2021-23214 behaviour).
`client_canary_test.go: TestPlantedEarlyBytesBugFailsTheSmuggleScenario` requires
`wire-client-smuggle` to pass without it and to FAIL its "client startup failures" assertion
with it (seeds 1–3); `client_test.go: TestPlantedEarlyBytesBugInterpretsTheSmuggledStartup`
shows the machine-level effect. The gate extra runs the canary.

## End-to-end scenarios (`TestScenarioWireStartup`, tier S)

Every scenario: tenant0 (4 clients × 3 queries from t = 0), tenant1 (the same from 8 s: the
recovery check), plus one fault client or one database fault at 20 ms (cleared at 7 s).
`verify` requires: exactly the expected startup failures on each side (and already counted at
fault time + deadline + 50 ms virtual), tenant0 and tenant1 all OK, quiesce, the wire
conservation, non-zero startups, queries, bytes and replies, TLS requests answered by the
proxy = sent by the workload (by answer), database TLS answers = the fake's answers. Each seed
runs twice (`simtest.RequireDeterministic`, REPLAY line) with identical snapshots.

**Seeds (repair round).** The pinned seeds (`internal/pgwire/testdata/seeds.txt`: 1, 2, 3, 7,
42), the default run (no `DST_SEEDS`: seeds 1–3) and a single `-seed`/`DST_SEED` run the full
matrix (every scenario on every seed). Under `DST_SEEDS` (the G4 sweep) each further seed runs
only the scenarios with index i where (seed + i) mod 4 = 0 (`wireSeeds`, `wireSweepStride`):
every scenario still gets a quarter of the sweep seeds (50 of 200) on top of the pinned ones,
and every seed runs four or five scenarios. Per-run setup was not the cost: a profile shows
about 70% in the run itself, a third of it chunk 2's per-step simulator invariants (fake and
workload conservation), which this chunk does not own. `client_sim_test.go:
TestWireSweepKeepsTheFullMatrixOnPinnedSeeds` holds the rule. At 20 seeds the wire scenarios
went from 11.0 s to about 6 s (pinned seeds dominate at that size); at 200 seeds the number
of wire runs drops from about 3,800 to about 1,050.

| Scenario | Fault | Expected |
| --- | --- | --- |
| `wire-startup-plain` | none, plaintext both legs | no failure |
| `wire-startup-tls` | none, require / require | no failure, TLS on both legs |
| `wire-one-byte` / `-plain` | every connection delivers one byte at a time (TLS allow + backend TLS / plaintext) | no failure |
| `wire-client-tls-stall` | client sends `SSLRequest`, then nothing | client `deadline` by 5 s |
| `wire-client-bad-hello` | client fails the handshake | client `tls-failed` |
| `wire-client-smuggle` | `SSLRequest` + plaintext startup in one write | client `protocol-violation` |
| `wire-client-second-request` | second `SSLRequest` after the upgrade | client `protocol-violation` |
| `wire-client-plaintext-refused` | plaintext client, mode require | client `plaintext-refused` |
| `wire-client-wrong-password` / `-unknown-user` | | client `auth-failed` |
| `wire-db-silent-startup` | fake accepts and never answers | database `deadline` by 2 s |
| `wire-db-connect-stall` | dial never completes | database `deadline` by 2 s |
| `wire-db-garbage` | fake answers with HTTP | database `protocol-violation` |
| `wire-db-reset-mid-handshake` | fake resets the connection during SCRAM | database `connection-lost` |
| `wire-db-max-connections` | `max_connections` 0 | database `too-many-connections` |
| `wire-db-tls-refused` | fake answers `N`, proxy requires TLS | database `tls-refused` |
| `wire-db-bad-certificate` | fake's certificate fails verify-full | database `tls-failed` |
| `wire-db-handshake-stall` | fake never finishes the handshake | database `deadline` by 2 s |

CSV-flush assertion: chunk 3's collector and `csv-assert.sh` are not on this branch, so the
SQL files are written to the 7.7 schema under `test/analysis/<scenario>/`
(`startup-failures.sql`, `tls-requests.sql`) and the same assertions are exact Go checks in
`verify`. `client_analysis_test.go: TestWireAnalysisSQL` (run by the gate extra with
`MG_DUCKDB=1`) writes each scenario's final numbers as `metrics.csv` with the proposed metric
names, runs both files with DuckDB (zero rows required) and plants a wrong number (rows
required). Superseded at integration: the scenarios now flush through chunk 3's collector and
the files run over the real flushed files (section "Integration with chunk 3");
`TestWireAnalysisSQL` is gone.

## Observability

`WireStats.Snapshot() WireSnapshot{Client, Database}` (plain struct, integers and fixed
arrays indexed by the enums). Client: startups begun, completed, failed by reason, in
startup (gauge, peak); pre-startup requests by kind (tls, gss, cancel) and answer (S, N,
none); TLS handshakes completed by version, failed by outcome (failed, failed-verification,
deadline, violation); plaintext refused; authentication failures by cause; cancel requests
(and after TLS); terminate, queries seen, extended refused; messages and bytes in and out;
violations by kind; oversized. Database: the same startup counters by class, TLS requests and
answers, handshakes, auth failures by cause, server FATALs, replies, ready by status, errors,
FATAL errors, `ParameterStatus`, notices, notifications, ends without marker, unknown types
passed through. `Check()`: begun = completed + failed + in startup per side, no failure
without a reason, TLS requests − answers ∈ [0, in startup]. Tests:
`snapshot_wire_test.go: TestWireCountersMoveAndConserve` (every counter and every reachable
array cell moves), `TestWireConservationCatchesAMissingOutcome`; the harness registers the
conservation as a sim invariant. Fake: `Server.AuthSnapshot()`; workload:
`TenantSnapshot.Startup`. Health: nothing new.

## Steady-state row

| What grows | Bound | Gauge |
| --- | --- | --- |
| Framer body buffer | `MaxMessage` for inspected bodies (×2 with append growth; dropped after a message above 64 KiB); `MaxStartupPacket` 10,000 before login; `MaxScramMessage` + 64 during SASL; skipped bodies are never buffered | — |
| SCRAM exchange state | one `ScramServer`/`ScramClient` per session, freed at success or close; messages ≤ 2,048 bytes, ≤ 16 attributes; iterations ≤ 2^20 | — |
| Parameter set (database side) | `MaxParams` 64, then a violation | — |
| Startup parameters (client side) | `MaxParams` 64, then a bad-packet violation | — |
| Tenant table | `MaxTenants` 65,536 | — |
| Sessions in startup | one per connection | `InStartup` (and peak) per side |
| Harness sessions | `pgwiretest.Config.MaxSessions` (4,096), then the connection is closed | `Counters.Open` |

## Limits

- SASLprep is not implemented: passwords are used as their bytes (ASCII works as in
  Postgres; a non-ASCII password that SASLprep would change does not match a Postgres-made
  verifier).
- No channel binding (`-PLUS`), no client certificates, no direct TLS negotiation, no GSS
  encryption (answered `N`), no md5 or cleartext on either leg.
- The proxy accepts protocol 3.x (x > 0) by negotiating down to 3.0, as Postgres 17 does.
- The fake answers a GSS request with `N`; the `postgres:17` image answers `G` (listed in
  `pgwiretest.KnownDifferences`). The fake refuses a client that picks `-PLUS` over the
  simulated TLS (it cannot bind to a certificate).
- The simulated handshake is 16-byte tokens; it models the outcome and the duration, not
  the byte cost or record framing of TLS.
- In the harness a client that leaves while its database login is in progress ends that
  login as `connection-lost` on the database side.
- Proxy-issued process ids are sequential from a random base (no randomness between
  consecutive sessions); the secret is random.
- The fake's repeated-request rule follows Postgres 17's `ProcessStartupPacket` (`ssl_done`,
  `gss_done`): a refused `SSLRequest` clears `gss_done` and a refused GSS request clears
  `ssl_done`, so alternating refused requests keep getting `N`. The repeated `SSLRequest` case
  is checked live (the adversary's oracle); the GSS cases are not, because the `postgres:17`
  image answers `G`.

## Deviations

- **GS2 rule over TLS** (decision 7): `n,,` when the server offers `-PLUS`, as Postgres 17
  requires; the brief's literal "`y,,` over TLS" would fail against a real server.
- **Protocol 3.x, x > 0, is negotiated, not refused**: the brief says "anything else → the
  FATAL Postgres sends"; Postgres 17 sends `NegotiateProtocolVersion` and continues (tier R
  shows it), so the machine does the same; major < 3 and > 3 get exactly Postgres's FATAL
  (old format for < 3). The fake was changed to match too.
- **Database TLS faults** are fake configuration, not `sim.Fault` kinds (decision 13).
- **Size.** `internal/pgwire` product code 2,527 lines (machines, framer, SCRAM, trackers,
  snapshot), the harness and fidelity runner 999, `simtls` 73, simulator additions about 600;
  tests about 3,400 lines. Within the brief's "may reach twice" for the package itself.

## Repair round

Review 1 (`REVIEW-1.md`) and the adversary's results (`agent_work/adversarial/c04-wire-sessions/RESULTS.md`).
Every item was reproduced first (the review's mutation M10 and repro calls, the seven failing
attacks); each regression test below was run against the code before its fix and failed.

| Item | Outcome |
| --- | --- |
| Review 1 (MAJOR): cycle tracker test blind to buffered rows | fixed `836dc4e`: `TestCycleTrackerStreamsManyRowsWithoutBuffering` now requires a body buffer of 0 while rows stream; new `TestCycleTrackerPassesARowLargerThanItsMessageLimit`. With `SkipBodies` removed (M10) both fail |
| Review 2 (MAJOR): `ReplyTracker` in chunk 5's prefix; `fuzz_test.go`, `export_test.go` outside chunk 4's globs | fixed `ebe532b`: `CycleTracker`/`NewCycleTracker`, `frame_fuzz_test.go`, `client_export_test.go`. `NewReplyTracker` stays as a one-line alias because the adversary's tests (not editable) call it; it does not begin with a reserved prefix. Checked by a scratch merge with `work/c05-classifier`: no identifier collisions (see "Integration notes") |
| Review 3 (MAJOR): `BackendSession.Deadline`/`PeerClosed` close a ready session | fixed `22effbd`: `fail` returns unless in startup; `backend_events_test.go: TestReadyBackendSessionIgnoresStartupEvents` (Receive, Deadline, PeerClosed, HandshakeDone after ready; counters unchanged) |
| Review 4 (MINOR): SCRAM state kept after failure | fixed `22effbd`: `close`/`fail` drop it; `TestClientSessionDropsSCRAMStateWhenItCloses`, `TestBackendSessionDropsSCRAMStateWhenItFails` |
| Review 5 (MINOR): handshake deadline/violation classified as tls-failed; unchecked enum indexing | fixed `22effbd` (see decision 6); `TestHandshakeResultOutcomesAreClassifiedOnTheClientSide`, `...OnTheDatabaseSide`, `TestHandshakeResultWithAnUnknownVersionCountsAsOther` |
| Review 6 (MINOR): harness drops bytes coalesced with the ClientHello | fixed `c673f16`: held (at most one read; the harness stops reading until the handshake completes) and fed after it; `pgwiretest/proxy_test.go: TestProxyKeepsBytesCoalescedWithTheClientHello` |
| Review 7 (MINOR): unused `midMessage`, `ReasonTerminate` | fixed `22effbd`: both removed |
| Review checklist remark: harness `ss.early` unbounded during the database connect | declined: test harness only (chunk 6 builds the real relay with its own bounds); the simulated clients send nothing before `ReadyForQuery` |
| Attack 2 `InvalidHandshakeEnums` | fixed `22effbd`: out-of-range outcome → failed, version → other; tests as review 5 (outcome 200, version 250) |
| Attack 2 `ScramMessageBound` | fixed `130a5a8`: `ScramServer.First`/`Final`, `ScramClient.Final`/`Verify` refuse input above `MaxScramMessage` (malformed); `scram_bound_test.go: TestScramFunctionsRefuseMessagesAboveTheBound` |
| Attack 2 `WireSCRAMBound` | fixed `130a5a8`: the session closes with `auth-failed` / `08P01 malformed SCRAM message`; `TestClientRejectsAnOversizedSCRAMMessageInsideTheFrame` |
| Attack 5 `UnknownUserIterationOracle` | fixed `bd5a768` for the default configuration (mock takes the table's most common iteration count and salt length); `client_mock_test.go: TestMockChallengeTakesTheTableShapeByDefault`. Narrowed for mixed tables (below) |
| Attack 7 `HandshakeDeadlineClassification` | fixed `22effbd`; tests as review 5 |
| Attack 9 `IndependentIssuerCollision` | narrowed (below); behaviour unchanged. The test is consistent with the claim as the brief worded it, so it is not disputed |
| Attack 11 `RepeatedSSLAfterRefusal` | fixed `21aef01` in the fake; `pgwiretest/fidelity_sim_test.go: TestFakeRefusesARepeatedEncryptionRequestAsPostgresDoes` |
| Brief item 6: sweep time | `058e2ad`: rotating scenario subset per sweep seed, full matrix on pinned seeds (see "Seeds" under the end-to-end scenarios) |

34 of the 35 attacks pass after the repair (`go test ./agent_work/adversarial/c04-wire-sessions/...`
prints one failure, attack 9, which stays failing by design: it asserts the unscoped wording).

## Narrowed claims

- **Claim 9** — Proxy-issued key data is unique per session **among the sessions of one
  `KeyIssuer`** (one per proxy instance): process ids do not repeat within 2^31 − 1 issues
  and are never 0; nothing is taken from the backend's key. Two independent issuers share no
  state: their keys differ only as far as their random sources differ (31-bit process-id
  base, 32-bit secret), and two issuers fed the same random stream issue the same keys.
  Postgres scopes cancel keys the same way (one postmaster). Chunk 7/13 must keep one issuer
  per proxy process and seed it from `crypto/rand`.
- **Claim 5, "unknown user and wrong password are indistinguishable in the reply"** — holds
  for every reply byte shape (message types, SQLSTATE `28P01`, the FATAL text with the user
  name, lengths when the names have equal length) and for the SCRAM challenge **when every
  verifier in the tenant table has the same iteration count and salt length** (the default
  mock then takes them; an explicit `MockIterations` overrides the count). In a table that
  mixes shapes, a user whose verifier differs from the table's most common shape can be told
  from an unknown user by `i=` or the `s=` length, as with Postgres's mock (which uses
  `scram_sha_256_iterations`). Timing is not part of the claim.

## Integration notes

- A scratch merge of this branch with `work/c05-classifier` (not committed) builds and vets
  cleanly except for one thing: chunk 5's `internal/pgwire/classify_e2e_test.go` and
  `classify_obs_test.go` are in-package tests (`package pgwire`) that import
  `internal/sim/fakepg`, and this chunk's `fakepg` imports `internal/pgwire` (the brief
  requires the fake to share the SCRAM functions), so `go test ./internal/pgwire` fails with
  "import cycle not allowed in test". The fix at integration is to make those two files
  external tests (`package pgwire_test`) or move them; `tools/lint-rules/det-packages.txt`
  also conflicts (both chunks appended lines; keep both).

## Integration with chunk 3

Done on 2026-11-25 after the rebase onto chunk 3 (brief
`/root/briefs/zgateway-p2-c04-integrate.md`). Wiring only; no product behaviour changed.

**What was added**

| Piece | Where |
| --- | --- |
| Mapping `WireSnapshot` -> metric points (component `wire`), `Metrics()`, `Source(func() WireSnapshot)` | `internal/obs/wiresrc/wiresrc.go`; test `wiresrc_test.go: TestMappingMovesEveryMetricAndExportsEveryField` (reflection fills every field: every field reaches an exported value, every metric moves), `TestZeroSnapshotEmitsEveryLabelValue` |
| Chunk 4's simulator counters, which chunk 3's mapping did not know: `fakepg.AuthSnapshot` and `workload.TenantSnapshot.Startup` | `internal/obs/simsrc/simsrc.go` (`FakeAuthMetrics`, `MapFakeAuth`, `mapStartup`; `Fake(srv)` now emits both fakepg snapshots); `simsrc_test.go: TestMappingsMoveEveryMetric` covers them (it failed on `Startup.*` after the rebase) |
| Catalog | `internal/obs/catalog/catalog.go` declares `wiresrc.Metrics()`; `docs/csv-schema.md` regenerated (`scripts/gen-docs.sh`) |
| Wire scenario world, importable | `internal/pgwire/pgwiretest/wireworld/` (moved out of `client_sim_test.go`: `Scenario`, `Expect`, `Build`, `World.Run`, `Run`, `Result`, `Scenario.Verify` - the exact Go assertions, unchanged - and `Scenarios()`). The planted-bug hook is now `Scenario.Client func(*pgwire.ClientConfig)`; the canary sets it to `pgwire.PlantEarlyBytes` |
| Flush harness | `internal/obs/simrun/wire.go: Wire(sc, seed, opts)`: builds the world, registers sim, simnet, fakepg, workload and wire sources, attaches all four adapters, publishes at t=0, every `WireInterval` (5 s virtual) and at the scenario's deadline-check instant (`Scenario.CheckAt`), then once at the end, and closes the CSV writer. Run id `<scenario>/<seed>/base`, the layout the SQL files expect. `simrun.Scenario` and `Wire` share `newRun` (adapter setup, unchanged behaviour) |
| Flush test, the baseline way | `internal/obs/simrun/wire_test.go: TestScenarioWireFlush`: every wire scenario on the pinned seeds (1, 42), run directory under `$RUN_DIR` when set; `Verify` (exact Go assertions), `Run.Check()` (CSV round trip, JSON, Prometheus grammar, log lines), a second run of the same seed with byte-identical files and adapter output and the same trace digest, and exported values equal to the snapshot |
| SQL over flushed files | `test/analysis/wire-*/{startup-failures,tls-requests,handshake-failures}.sql` (19 scenarios x 3) |
| SQL in `go test` and the gate | `simrun/csvassert_test.go`: the shared baseline root now also holds every wire scenario at seed 1, so `TestCSVAssertBaselineCountsFromFlushedFiles` runs all 62 SQL files (root mode) and `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` sees the wire metrics. `simrun/wiresql_test.go: TestWireSQLCatchesPlantedNumbers` (see below). G4's sweep runs `TestScenarioWireFlush` under `$RUN_DIR/sweep`, so G7 (`csv-assert.sh` root mode over the sweep) runs the wire files |
| Removed | `client_analysis_test.go: TestWireAnalysisSQL` and its hand-written `metrics.csv` (superseded by the real flush) |
| Lint rules | `tools/lint-rules/imports.txt`: `internal/obs/wiresrc` joins the mapping packages that may not import an adapter, `simrun` or `catalog`, and adapters may not import it; `simrun/imports_test.go` checks it too. `tools/lint-rules/det-packages.txt`: `internal/obs/wiresrc`, `internal/pgwire/pgwiretest/wireworld` |
| Gate extra | `gate/extra.sh` step 1 now runs `TestWireSQLCatchesPlantedNumbers` and `TestCSVAssertBaselineCountsFromFlushedFiles` with `MG_REQUIRE_DUCKDB=1` |

**Import rule.** `internal/pgwire` imports nothing new; its tests cannot import `simrun`
(importlint reads test files and `internal/pgwire/...` may not import `simrun`), which is why
the flush tests live in `internal/obs/simrun` and the scenario table moved to `wireworld`
(a harness package under `pgwiretest`, imports no adapter). `importlint` is green.

**Label decision.** The side is in the metric name (`client_...`, `database_...`), not in
`mode`: `mode` is already taken by the request kind and the TLS version, and the two sides
have different failure vocabularies (client reasons vs database error classes). Labels:
`reason` for every by-cause counter (startup failure reason or class, handshake result,
auth cause, violation), `outcome` for answers (`S`, `N`, `none`), `mode` for the request
kind (`tls`, `gss`, `cancel`), the TLS version (`1.2`, `1.3`, `other`) and the ready status
(`I`, `T`, `E`). Every known label value is emitted, zero included (the enum's `none` slot
too, which `Check` requires to stay 0). No tenant label: the wire snapshot is not per tenant.

**Metric names** (component `wire`, all counters unless marked):
client: `client_startups_begun`, `client_startups_completed`, `client_startups_failed{reason}`,
`client_in_startup` (gauge), `client_in_startup_peak` (high-water), `client_prestartup_requests{mode,outcome}`,
`client_tls_completed{mode}`, `client_tls_failed{reason}`, `client_plaintext_refused`,
`client_auth_failures{reason}`, `client_cancel_requests`, `client_cancel_after_tls`, `client_terminated`,
`client_queries_seen`, `client_extended_refused`, `client_messages_in`, `client_messages_out`,
`client_bytes_in`, `client_bytes_out`, `client_violations{reason}`, `client_oversized`;
database: `database_startups_begun`, `database_startups_completed`, `database_startups_failed{reason}`,
`database_in_startup` (gauge), `database_in_startup_peak` (high-water), `database_tls_requests`,
`database_tls_answers{outcome}`, `database_tls_completed{mode}`, `database_tls_failed{reason}`,
`database_auth_failures{reason}`, `database_server_fatals`, `database_replies`, `database_ready{mode}`,
`database_errors`, `database_fatal_errors`, `database_parameter_status`, `database_notices`,
`database_notifications`, `database_end_without_marker`, `database_unknown_pass_through`, and the
six side counters with the `database_` prefix.
New simulator metrics: `fakepg.tls_answered{outcome}`, `gss_refused`, `handshakes_started`,
`handshakes{reason: completed, failed, stall, bad-certificate}`, `bad_hellos`, `scram_started`,
`scram_ok`, `scram_failed`, `channel_binding_refused`, `tls_changes`; `workload.startup_tls_requests{tenant}`,
`startup_tls_answered{tenant,outcome}`, `startup_handshakes{tenant,outcome: ok, failed}`,
`startup_tls_faults{tenant,reason: fault kind}`, `startup_scram_started{tenant}`,
`startup_scram{tenant,outcome: ok, failed}`.

**The SQL files** (each starts with `-- requires:`, checks `schema_version`, groups by
`run_id` so it holds over many runs, returns zero rows on a correct run):

- `startup-failures.sql` - startup failures by reason in the wire snapshot = startup faults
  injected by kind. Each file lists its injection: the flushed fault counter (`fakepg.faults`
  `pg.<kind>`, `workload.startup_tls_faults` `<kind>`, `fakepg.handshakes` `<result>`, or
  `fakepg.tls_answered` `N` for the refused TLS request) and the `wire.client_startups_failed`
  / `database_startups_failed` reason it must produce. Every run: the injected count is at
  least 1 and equals the failures of that reason, no other reason is non-zero, and no fault of
  another kind was injected. Three client scenarios have no fault counter (the injection is the
  fault tenant's configuration: plaintext while TLS is required, wrong password, unknown user):
  their count is the planted 1, written in the file.
- `tls-requests.sql` - TLS requests by kind and answer = requests sent: the proxy's
  `client_prestartup_requests{mode=tls}` answered S or N = the workload's
  `startup_tls_requests` (at least 1), per answer = the workload's `startup_tls_answered`, no
  `gss` or `cancel` request counted; database side: `database_tls_answers` per answer =
  `fakepg.tls_answered`, and the sum = the fake's answers.
- `handshake-failures.sql` - handshake failures = handshake faults injected: `client_tls_failed`
  / `database_tls_failed` by result equal the injected handshake fault (`bad-hello` ->
  `failed`, `stall-after-request` -> client `deadline`, `bad-certificate` ->
  `failed-verification`, fake handshake `stall` -> database `deadline`), nothing else failed,
  no other handshake fault was injected; and completed handshakes match on both sides
  (`client_tls_completed` = workload `startup_handshakes{ok}`, `database_tls_completed` =
  `fakepg.handshakes{completed}`).

**Each can fail** - `wiresql_test.go: TestWireSQLCatchesPlantedNumbers`, per scenario on the
flushed seed-1 run: one copy with three wrong numbers planted in the final flush (+1
`client_startups_failed{deadline}`, +7 `startup_tls_requests` of `tenant0`, +1
`client_tls_failed{failed}`) must make `csv-assert.sh` exit non-zero naming all three files
with violating rows (and not for a missing metric or a DuckDB error); a second copy with the
expected startup failure set to 0 must make `startup-failures.sql` fail. The correct runs pass
in `TestCSVAssertBaselineCountsFromFlushedFiles`.

**Decisions.** (1) Flush on the pinned seeds, as chunk 3's `TestScenarioFlush` does; the
seed sweep `TestScenarioWireStartup` keeps running the world without the collector (same as
chunk 2's `TestScenario`). (2) `WireInterval` is 5 s of virtual time (the baseline default is
100 ms): a wire run lasts 30 s virtual and at 100 ms a run directory was 13 MB and made the
SQL tests take minutes; the extra flush at the deadline-check instant keeps the at-check
numbers in the files. (3) The SQL files are generated text but committed as data; the
generator was a scratch script and is not kept.

**Cost.** `go test ./internal/obs/simrun` takes about 2 minutes with DuckDB on vm-1 (it was
about 25 s): `TestCSVAssertBaselineCountsFromFlushedFiles` ~55 s (62 files, each runner call
starts DuckDB once per required metric), `TestWireSQLCatchesPlantedNumbers` parallel,
`TestScenarioWireFlush` ~12 s.

## Simulator changes (additive; the kernel API is untouched)

| File | Change |
| --- | --- |
| `internal/sim/simtls/` | new package: simulated handshake tokens |
| `internal/sim/fakepg/auth.go` | new: `AuthMode` (`AuthTrust` default, `AuthSCRAM`), `Verifiers`, `TLSConfig` (`Answer`, `Handshake`, `Outcome`, `Version`), `SetTLS`, `AuthSnapshot`, the simulated handshake, the SCRAM server (shared `pgwire` functions; offers `-PLUS` over TLS like Postgres; refuses `y,,` over TLS with `28000`), mock exchange for unknown users, the database check after authentication, protocol-version answers |
| `internal/sim/fakepg/auth.go`, `backend.go` (repair round) | `sslDone`/`gssDone` on the connection and `answerGSS`: a repeated encryption request gets `FATAL 0A000 unsupported frontend protocol 1234.5679` (`.5680` for GSS) as in Postgres 17 |
| `internal/sim/fakepg/backend.go` | +~30 lines: three fields, hooks in `readMessage` (hello, handshake wait), `dispatch` (SASL messages), `startup` (SSL answer, GSS counter, SCRAM branch, protocol answers), `finalize` (cancel the handshake timer) |
| `internal/sim/fakepg/config.go`, `server.go`, `frame.go`, `proto.go` | 3 config fields; auth state and its init; `Framer.TakeRaw`; `startupPacket.unknown/negotiate` and the 3.x decode |
| `internal/sim/fakepg/snapshot_test.go` | the protocol-refusal case now sends 4.0 (3.2 is negotiated now, as in Postgres) |
| `internal/sim/workload/startup.go` | new: `Tenant.Password`, `Tenant.TLS` (off, prefer, require), `Tenant.TLSFault` (stall after request, bad hello, plaintext after request, second request), the SCRAM client, `StartupCounters` |
| `internal/sim/workload/client.go`, `config.go`, `snapshot.go` | hooks: `beginStartup` on connect, `onTLSAnswer`/`onAuth` in `onMessage`, the hello read in `readAll`; 3 tenant fields; `TenantSnapshot.Startup` (summed into the total) |
| `internal/sim/workload/startup_test.go` (repair round) | `client-second-request` against the fake now expects the fake's refusal (no second `N`) |
| `internal/sim/workload/snapshot_test.go` | `Startup.*` exempt from chunk 2's busy-world counters check (its world has no TLS or SCRAM); `startup_test.go: TestStartupCountersMove` covers them |
| `internal/testclient/msg.go` | `Msg.Data`: the authentication message's payload |
| `scripts/tier-r.sh` | third container (`-scram`) and its two environment variables |

Defaults keep every chunk 2 scenario unchanged: trust auth, `N` answers, no TLS request
(`TestScenario` and `TestFidelityAgainstRecordedPostgres17` still pass; the chunk 2
recording was rewritten by `MG_TIER_R_UPDATE=1` with identical content).

## Commands

```
PATH=/usr/local/go/bin:$PATH
go test ./internal/pgwire/... ./internal/sim/... ./internal/testclient
DST_SEEDS=200 go test -run '^TestScenarioWire' ./internal/pgwire          # the sweep part (rotating subset per seed)
go test ./internal/pgwire -run '^TestScenarioWireStartup$/^wire-client-smuggle$/^seed=7$' -seed=7 -count=1
go test -run '^$' -fuzz '^FuzzClientSession$' -fuzztime 60s ./internal/pgwire   # also FuzzFramer, FuzzBackendSession, FuzzCycle
go test -run TestGoldenBytes -update ./internal/pgwire                     # after checking a reply change
MG_REQUIRE_DUCKDB=1 go test -run 'TestWireSQL|TestCSVAssertBaseline|TestScenarioWireFlush' ./internal/obs/simrun
RUN_DIR=/tmp/tr scripts/tier-r.sh                                          # MG_TIER_R_UPDATE=1 rewrites both recordings
agent_work/chunks/c04-wire-sessions/gate/extra.sh
scripts/gate.sh c04-wire-sessions
```

## Gate result

Repair round: `scripts/gate.sh c04-wire-sessions`, build stage, clean tree at `75501f9`
(only this file changed after it): `overall=fail`, `counts_for_landing=false`, and the only
red check is **G3 unit**, which fails on exactly one test, the adversary's
`TestAdv_9_IndependentIssuerCollision` (claim 9 narrowed, above). G3 runs `go test ./...`,
which includes `agent_work/adversarial/c04-wire-sessions`, and this round may not edit that
folder (chunk 2's repair round ended the same way). Every product package passes in G3.
G0, G1 (detlint on 12 package patterns), G2 comments, G3 race (no package listed), G4 markers,
G4 sweep (368 s for every `TestScenario*` at 200 seeds, down from 629 s; the machine is
shared, so treat the times as rough), G5 fuzz (11 targets × 20 s), G6 tier R (42 s, Postgres
17.11, three containers), G9 site and G10 extra (SQL assertions, canary, 19 cross-process
digests) pass; G7 and G8 are `skip-not-built`. `result.json` was not committed. Once the
orchestrator accepts the narrowing and skips attack 9, the gate should go green.

## Index rows

Rows the orchestrator may add to `agent_work/INDEX.md`:

| Path | What |
| --- | --- |
| `agent_work/chunks/c04-wire-sessions/gate/extra.sh` | chunk 4 G10: SQL assertions with DuckDB on the wire scenarios, the early-bytes canary, cross-process digests |
| `internal/pgwire/pgwiretest/testdata/startup-postgres17.txt` | recorded Postgres 17 startup conversation (`MG_TIER_R_UPDATE=1 scripts/tier-r.sh`) |
| `test/analysis/wire-*/` | SQL assertions per wire scenario (schema 7.7 v1) |
| `docs/knowledge/wire-sessions.md` | facts about the session machines, SCRAM and Postgres 17's startup answers |
