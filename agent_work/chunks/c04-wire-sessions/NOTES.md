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
| Query-cycle tracker (database replies) and the client query-phase tracker | `cycle.go` (`ReplyTracker`, `FrontendTracker`) |
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
- Trackers: `ReplyTracker.QuerySent()` opens a cycle, `Receive` returns `CycleEvent`s (one
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
   Postgres's mock salt) and its iteration count is `MockIterations` (set it to the count the
   real verifiers use, or an attacker can tell them apart by `i=`).
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
| 2 | No panic, no loop, bounded memory; oversized refused from the header | `frame_test.go: TestFramerRefusesFromTheHeaderAlone`, `TestFramerTakesOnlyWhatItNeeds`; `client_test.go: TestOversizedLengthIsRefusedFromTheHeader`; `cycle_test.go: TestCycleTrackerStreamsManyRowsWithoutBuffering` (200,000 rows, buffer ≤ 1 KiB), `TestCycleTrackerSurprises`; the four fuzz targets (buffer bounds, no progress-less loop, conservation) |
| 3 | Nothing between an accepted `SSLRequest` and the handshake result is interpreted; exactly 8 bytes; at most one upgrade | `client_test.go: TestBytesSentEarlyAfterAnAcceptedTLSRequest` (consumed = 8, every chunking, later delivery too), `TestSecondTLSRequestAfterTheUpgradeIsAViolation`; `backend_test.go: TestBackendGarbageSilenceAndEOF` (bytes after the server's `S`); scenarios `wire-client-smuggle`, `wire-client-second-request`; canary below |
| 4 | require: no plaintext startup; off: `N` and plaintext works; allow: both | `client_test.go: TestPlaintextStartupRefusedWhenRequired`, `TestTLSRequestAnsweredNWhenOff`, `TestPlaintextClientLogsInWithSCRAM` (off, allow), `TestDefaultStyleClientUpgradesWhenAllowedOrRequired`, `TestGSSThenTLSRequest`; scenario `wire-client-plaintext-refused`; tier R `TestTierRScramServerAcceptedByPsql` (9 psql cases over the three modes) |
| 5 | SCRAM server: only the password holder; no `-PLUS`; `n,,`/`y,,` with the right `c=`; `p=` refused; unknown user = wrong password; no password in the table | `scram_test.go: TestScramRFC7677ServerSide`, `TestScramClientAgainstServer` (15 cases), `TestVerifierHoldsNoPassword`, `TestScramServerRejectsAnotherMechanism`; `client_test.go: TestWrongPasswordAndUnknownUserLookAlike`, `TestUnknownUserWithTheRightProofForAnotherVerifierFails`, `TestGS2HeadersThroughTheMachine`, `TestOnlySCRAMSHA256IsOffered`; tier R psql cases (libpq's `y,,` over TLS accepted; wrong password and unknown user give the same text) |
| 6 | SCRAM client: wrong server signature rejected; `SCRAM-SHA-256` chosen when `-PLUS` is offered | `scram_test.go: TestScramRFC7677ClientSide`, `TestScramClientAgainstServer` (tampered signature, nonce, `e=`); `backend_test.go: TestBackendChoosesSCRAMWhenPlusIsOfferedAndPicksTheGS2Header`, `TestBackendFailureClasses` (tampered signature, server skips its signature); tier R `TestTierRScramClientLogsInToPostgres` (over TLS with `-PLUS` offered) |
| 7 | Every startup failure ends in its class and reply within the deadline, counted under exactly one reason | `client_test.go` (`requireClose` checks the one reason and conservation in every failure test), `backend_test.go: TestBackendFailureClasses` (17 cases), `TestBackendGarbageSilenceAndEOF`; `snapshot_wire_test.go`; scenarios `wire-client-*`, `wire-db-*` (`verify`: the exact failure map, checked again at fault time + deadline + 50 ms on the virtual clock) |
| 8 | Cycle tracker: status byte and end of reply for every probe shape, no buffering of a reply, unknown types passed through, unsolicited `ParameterStatus` reported | `cycle_test.go: TestCycleTrackerProbeShapes` (single result, two result sets, error then ready, error in a transaction → `E`, `FATAL` then end, unsolicited `S`, unknown type, notice and notification), `TestCycleTrackerFatalEndsTheStreamWithoutAMarker`, `TestCycleTrackerStreamsManyRowsWithoutBuffering`, `TestCycleTrackerReportsUnsolicitedMessagesOutsideACycle`; the harness relays every scenario's queries through both trackers |
| 9 | Proxy key data unique per session, unrelated to the backend's | `client_test.go: TestProxyKeysAreUniqueAndFromTheIssuer` (2,000 issues, distinct per seed); `KeyIssuer` takes nothing from the backend; tier R backend key comes from Postgres separately |
| 10 | Deterministic and sans-I/O | G1 detlint on `internal/pgwire`, `internal/pgwire/pgwiretest`, `internal/sim/simtls` (no `crypto/tls`, `net`, `time.Now`, goroutines; one allow line for the SCRAM imports); `TestScenarioWireStartup` (two runs per seed, identical digests and snapshots); gate extra (two processes, identical digests) |
| 11 | The fake's startup conversation, TLS answers included, matches Postgres 17 | `pgwiretest/fidelity_test.go: TestTierRStartupFidelity` (12 steps live against Postgres 17.11, writes the recording), `TestStartupFidelityAgainstRecordedPostgres17` (CI), `TestKnownDifferencesAreTheRecordedOnes`; chunk 2's `TestTierRFidelity` still identical |

## Planted-bug canary

`ClientConfig.plantEarlyBytes` (unexported; set only by `export_test.go: PlantEarlyBytes`)
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
required). The orchestrator wires the real flush at integration.

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

## Simulator changes (additive; the kernel API is untouched)

| File | Change |
| --- | --- |
| `internal/sim/simtls/` | new package: simulated handshake tokens |
| `internal/sim/fakepg/auth.go` | new: `AuthMode` (`AuthTrust` default, `AuthSCRAM`), `Verifiers`, `TLSConfig` (`Answer`, `Handshake`, `Outcome`, `Version`), `SetTLS`, `AuthSnapshot`, the simulated handshake, the SCRAM server (shared `pgwire` functions; offers `-PLUS` over TLS like Postgres; refuses `y,,` over TLS with `28000`), mock exchange for unknown users, the database check after authentication, protocol-version answers |
| `internal/sim/fakepg/backend.go` | +~30 lines: three fields, hooks in `readMessage` (hello, handshake wait), `dispatch` (SASL messages), `startup` (SSL answer, GSS counter, SCRAM branch, protocol answers), `finalize` (cancel the handshake timer) |
| `internal/sim/fakepg/config.go`, `server.go`, `frame.go`, `proto.go` | 3 config fields; auth state and its init; `Framer.TakeRaw`; `startupPacket.unknown/negotiate` and the 3.x decode |
| `internal/sim/fakepg/snapshot_test.go` | the protocol-refusal case now sends 4.0 (3.2 is negotiated now, as in Postgres) |
| `internal/sim/workload/startup.go` | new: `Tenant.Password`, `Tenant.TLS` (off, prefer, require), `Tenant.TLSFault` (stall after request, bad hello, plaintext after request, second request), the SCRAM client, `StartupCounters` |
| `internal/sim/workload/client.go`, `config.go`, `snapshot.go` | hooks: `beginStartup` on connect, `onTLSAnswer`/`onAuth` in `onMessage`, the hello read in `readAll`; 3 tenant fields; `TenantSnapshot.Startup` (summed into the total) |
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
DST_SEEDS=200 go test -run '^TestScenarioWire' ./internal/pgwire          # the sweep part (about 3 min)
go test ./internal/pgwire -run '^TestScenarioWireStartup$/^wire-client-smuggle$/^seed=7$' -seed=7 -count=1
go test -run '^$' -fuzz '^FuzzClientSession$' -fuzztime 60s ./internal/pgwire   # also FuzzFramer, FuzzBackendSession, FuzzCycle
go test -run TestGoldenBytes -update ./internal/pgwire                     # after checking a reply change
MG_DUCKDB=1 go test -run TestWireAnalysisSQL ./internal/pgwire
RUN_DIR=/tmp/tr scripts/tier-r.sh                                          # MG_TIER_R_UPDATE=1 rewrites both recordings
agent_work/chunks/c04-wire-sessions/gate/extra.sh
scripts/gate.sh c04-wire-sessions
```

## Gate result

`scripts/gate.sh c04-wire-sessions`, build stage, clean tree at `f5427d4` (the last code
commit; only this file and `docs/knowledge/wire-sessions.md` changed after it):
`overall=pass`, `counts_for_landing=true`. G0, G1 (detlint on 12 package patterns), G2, G3
unit (50 s), G4 markers, G4 sweep (629 s for every `TestScenario*` at 200 seeds; the wire
scenarios are about 165 s of it), G5 fuzz (11 targets × 20 s, the four new ones included),
G6 tier R (37 s, Postgres 17.11, three containers), G9 site, G10 extra (10 s) pass; G7 and
G8 are `skip-not-built` (chunk 3 and 5 scripts). G3 race lists no package (none added: no
concurrency in product code). `result.json` was not committed (moved out of the tree; the
orchestrator reruns the gate).

## Index rows

Rows the orchestrator may add to `agent_work/INDEX.md`:

| Path | What |
| --- | --- |
| `agent_work/chunks/c04-wire-sessions/gate/extra.sh` | chunk 4 G10: SQL assertions with DuckDB on the wire scenarios, the early-bytes canary, cross-process digests |
| `internal/pgwire/pgwiretest/testdata/startup-postgres17.txt` | recorded Postgres 17 startup conversation (`MG_TIER_R_UPDATE=1 scripts/tier-r.sh`) |
| `test/analysis/wire-*/` | SQL assertions per wire scenario (schema 7.7 v1) |
| `docs/knowledge/wire-sessions.md` | facts about the session machines, SCRAM and Postgres 17's startup answers |
