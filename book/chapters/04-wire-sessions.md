# 4. Speaking Postgres: TLS or not, startup, login, fragments

Chapters 1 through 3 built the deterministic simulator, the fake Postgres 17 server, simulated multi-tenant clients, and the observability spine. None of that code is the proxy itself.

Chunk 4 builds the proxy's first production wire layer in [`internal/pgwire`](../../internal/pgwire). It handles the Postgres wire protocol on both sides of a proxy connection: the first 8 bytes from a client, optional TLS (Transport Layer Security) negotiation, SCRAM-SHA-256 (Salted Challenge Response Authentication Mechanism with SHA-256) login on each leg, and query-phase message framing. Everything in this package is written as **sans-I/O state machines**—state machines that take bytes and events as inputs and return actions as outputs, without touching sockets, threads, or the wall clock.

Alongside [`internal/pgwire`](../../internal/pgwire), this chunk adds several other pieces. It adds simulated TLS handshake tokens in [`internal/sim/simtls`](../../internal/sim/simtls), additive SCRAM and TLS extensions to [`internal/sim/fakepg`](../../internal/sim/fakepg) and [`internal/sim/workload`](../../internal/sim/workload), and a test-only startup proxy plus a Postgres 17 startup fidelity runner in [`internal/pgwire/pgwiretest`](../../internal/pgwire/pgwiretest) and [`internal/pgwire/pgwiretest/wireworld`](../../internal/pgwire/pgwiretest/wireworld). It also adds the `wire` metric mapping in [`internal/obs/wiresrc`](../../internal/obs/wiresrc), flushed wire scenario tests in [`internal/obs/simrun`](../../internal/obs/simrun), and 57 DuckDB SQL assertions under `test/analysis/wire-*/`. The SQL statement classifier (Chunk 5) is not part of this chapter and has not landed.

---

## What problem this solves, and the alternatives turned down

Connecting to Postgres is not a single `Dial` call ([section 4.1 of the plan](../../PLAN.md#41-evidence)). Before a client can send its first `SELECT`, both sides must complete a multi-step conversation over a raw TCP byte stream that can split or coalesce bytes at any boundary:

1. **Pre-startup negotiation:** A stock `libpq` client such as `psql` defaults to `sslmode=prefer`, so it opens by sending an untyped 8-byte `SSLRequest` packet (length 8, magic code `80877103`) or an 8-byte `GSSENCRequest` (`80877104`). The server answers with one byte: `'S'` to upgrade to TLS or `'N'` to stay in plaintext. A `CancelRequest` (16 bytes, code `80877102`) arrives through the same pre-startup path, either in plaintext or after a TLS upgrade, and closes without a reply.
2. **Protocol version and startup parameters:** The client next sends an untyped `StartupMessage` carrying the protocol version (`3.0`), a `user`, an optional `database`, and session settings. Because the future pool lends one database connection across many clients, the proxy must refuse up front any client that asks for a session setting differing from the pool's fixed settings.
3. **Authentication on both legs:** Under [decision D6](../../PLAN.md#3-design-decisions-this-plan-makes) and [Ruling 5](../../PLAN.md#31-ruling-5-client-facing-tls-verdict-and-evidence), the proxy authenticates clients itself against a static table of tenant **verifiers**—the salted PBKDF2-derived keys Postgres stores in `pg_authid`, never plaintext passwords. On the database side, the proxy logs its own pooled connections in to Postgres as one shared database role.
4. **Query-phase boundaries without buffering rows:** Once logged in, the proxy must follow database replies to their `ReadyForQuery` end marker without holding 20 MB of `DataRow` messages in memory. That marker carries the transaction status (`'I'` idle, `'T'` in transaction, or `'E'` failed transaction), and along the way the proxy must also catch unsolicited `ParameterStatus` messages—the [D8](../../PLAN.md#3-design-decisions-this-plan-makes) dirty-connection signal.

Five design alternatives were considered and turned down:

- **Not passing Postgres's SCRAM exchange through.** Phase 1's TLS probe (`agent_work/phase1/probes/pgtls/`) showed why. When Postgres speaks TLS, it advertises `SCRAM-SHA-256-PLUS`, whose channel binding is tied to the server's TLS certificate. A client connected over TLS to a terminating proxy binds its SCRAM proof to the proxy's certificate rather than Postgres's, so authentication fails with "SCRAM channel binding check failed". Terminating TLS means the proxy must run SCRAM independently on each leg—which decision D6 already requires.
- **Not wrapping `pgproto3.Frontend`, `pgproto3.Backend`, or `crypto/tls`.** `pgproto3`'s session drivers block on an `io.Reader` and do not fit a single-threaded event loop. `crypto/tls` performs network I/O and wall-clock certificate checks that break deterministic simulation. `internal/pgwire` does not import `pgproto3` at all: every message the machines read or write is encoded and decoded by hand in [`frame_encode.go`](../../internal/pgwire/frame_encode.go) and [`fatal.go`](../../internal/pgwire/fatal.go). That keeps `pgproto3.StartupMessage`'s Go map iteration out of a determinism-checked package and leaves `go.mod` unchanged. Real `crypto/tls` runs on shell goroutines in Chunk 6 (and in tier R tests here). To the state machines, a TLS handshake is just an `ActStartTLS` action out and a `HandshakeDone(HandshakeResult)` event back in.
- **Not buffering across the TLS upgrade.** A buffered reader over a TCP socket can read an `SSLRequest` and trailing plaintext bytes in one `Read` call. If those buffered plaintext bytes reach the post-TLS parser after the handshake completes, a network attacker can inject unencrypted SQL or startup parameters into an encrypted session—the CVE-2021-23214 vulnerability class in Postgres itself. Instead, `ClientSession` consumes *exactly* the 8 bytes of `SSLRequest` and rejects any byte delivered before `HandshakeDone` as a protocol violation. `BackendSession` enforces the mirror rule after the server's `'S'` (CVE-2021-23222).
- **Not running real cryptography inside the simulator.** Simulated TLS handshakes in [`internal/sim/simtls`](../../internal/sim/simtls/simtls.go) exchange fixed 16-byte tokens (`SIMTLS-HELLO` and a 16-byte server reply carrying OK, bad certificate, or alert plus a TLS version byte) over a configurable virtual duration (default 2 ms). That models handshake outcomes, stalls, and bad certificates deterministically at simulator speed without paying real key-exchange CPU on every sweep seed.
- **Not inventing new `sim.Fault` IDs for database TLS faults.** Chunk 2's fault table (`32..55`) and its every-kind tests are frozen. Database-side TLS faults (`Answer: 'N'`, `TLSFailed`, `TLSBadCertificate`, `TLSStall`) are configured through `fakepg.Config.TLS` (changeable mid-run via `Server.SetTLS`), and client-side TLS faults through `workload.Tenant.TLSFault`. All Chunk 2 scenarios stay unchanged by default.

---

## Inside `internal/pgwire`

```text
bytes / HandshakeDone / Deadline / PeerClosed
        │
        ▼
  ClientSession  ──► ActWrite | ActStartTLS | ActCancel | ActReady | ActClose(Reason)
        │ (after ActReady)
        ▼
  FrontendTracker ──► FrontQuery | FrontTerminate | FrontExtended | FrontViolation

  BackendSession ──► ActWrite | ActStartTLS | ActReady(Params, Key) | ActClose(Class, Code)
        │ (after ActReady)
        ▼
  CycleTracker   ──► CycleMessage | CycleReady(I/T/E) | CycleError | CycleParameterStatus
                     | CycleNotice | CycleNotification | CycleEndOfStream | CycleViolation
```

### The incremental framer (`frame.go`)

[`Framer`](../../internal/pgwire/frame.go) handles two wire formats: untyped startup packets (4-byte big-endian length, minimum 8 bytes, capped at `MaxStartupPacket = 10,000`) and typed messages (1-byte type plus 4-byte length, minimum 4 bytes, default cap `DefaultMaxMessage = 1 MiB`).

Two properties hold on every call to `Take(b []byte) (consumed int, done bool, err error)`:

1. **Header-only refusal.** As soon as the 4- or 5-byte header arrives, `parseHeader()` checks both minimum length (`ErrBadLength`) and maximum length (`ErrTooLarge`) before allocating or copying any body byte. Once an error occurs, the framer latches `f.err` and refuses further calls.
2. **Exact boundary stop and zero-buffer skipping.** `Take` never consumes a byte past the current message. For any type registered via `SkipBodies(...)`, it counts remaining bytes down (`f.need -= c`) up to Postgres's 1 GiB − 1 limit (`MaxRelayedMessage`) without touching `f.body`. For inspected bodies, `parseHeader` pre-allocates at most `frameGrowAhead` (64 KiB), and `clear()` drops any buffer larger than 64 KiB after a large message so steady-state memory returns to baseline.

### Client-facing session machine and the early-bytes guard (`client.go`, `prestartup.go`, `client_tenants.go`)

[`NewClientSession`](../../internal/pgwire/client.go) starts in `csPre` with `Untyped: true` and immediately increments `StartupsBegun` and `InStartup` so conservation holds from connection accept.

When an `SSLRequest` arrives:
- Under `ClientTLSOff`, the machine writes `'N'` and stays in `csPre` waiting for a plaintext `StartupMessage`.
- Under `ClientTLSAllow` or `ClientTLSRequire`, it writes `'S'`, sets `state = csTLSWait`, and emits `ActStartTLS`.

In `Receive`, the guard against CVE-2021-23214 sits at the top of the loop ([`client.go`](../../internal/pgwire/client.go)):

```go
func (s *ClientSession) Receive(acts []Action, b []byte) ([]Action, int) {
	n := 0
	for n < len(b) && s.inStartup() {
		if s.state == csTLSWait {
			if !s.cfg.plantEarlyBytes {
				acts = s.violation(acts, ViolationBytesBeforeHandshake, false)
				break
			}
			s.state, s.upgraded = csPre, true
		}
		c, done, err := s.fr.Take(b[n:])
		n += c
		...
```

If a client coalesces an 8-byte `SSLRequest` with a plaintext `StartupMessage` in one write, the first iteration consumes exactly 8 bytes, emits `ActWrite{'S'}` and `ActStartTLS`, and switches to `csTLSWait`. The very next iteration sees `len(b) > 8`, leaves `n` at 8, and closes the session with `ViolationBytesBeforeHandshake` (`ReasonViolation`) without interpreting a single trailing byte. A second `SSLRequest` (after upgrade or after `'N'`) or a second `GSSENCRequest` also closes as a violation.

Once a `StartupMessage` is accepted:
- Plaintext under `ClientTLSRequire` fails with `FATAL 28000` ("TLS is required for this connection: SSL off").
- Protocol major versions `< 3` and `> 3` are refused; `3.x` (`x > 0`) and `_pq_.` options are negotiated down to `3.0` (details below).
- Missing `user` fails with `FATAL 28000`. `replication`, `options`, or any startup parameter outside `ClientConfig.SessionSettings` (default `client_encoding=UTF8`, matched case-insensitively) fails with `FATAL 0A000`. `application_name` is accepted and ignored, and an omitted `database` defaults to `user`.
- Authentication defaults to `AuthSCRAM` (the zero value of `AuthMode`); `AuthTrustForTestsOnly` must be named explicitly.
- For an **unknown user**, `ClientSession` still sends `AuthenticationSASL` offering `SCRAM-SHA-256` and runs the full two-round exchange against a deterministic mock verifier (`MockVerifier(MockSecret, user, iterations, saltLen)`). At the end, it returns the same `FATAL 28P01` ("password authentication failed for user \"\<user\>\"") that a wrong password gets. When `MockIterations` is left at 0, [`TenantTable`](../../internal/pgwire/client_tenants.go) precomputes the modal iteration count and salt length across its stored verifiers so the mock challenge matches a uniform table.
- Database existence is checked *after* authentication (matching Postgres), emitting `AuthenticationSASLFinal`, `AuthenticationOk`, and then `FATAL 3D000`.
- On success, the machine emits one `ActWrite` containing `AuthenticationOk`, the `ParameterStatus` set, a proxy-issued `BackendKeyData`, and `ReadyForQuery('I')`, followed by `ActReady`. Those status parameters come from the pool's first backend login (or Postgres 17's 14 defaults), with `session_authorization` rewritten to the tenant user and `is_superuser` forced to `off`. [`KeyIssuer`](../../internal/pgwire/client_tenants.go) generates the key data independently of any backend key (`pid = (base + issued) % (2^31 - 1) + 1`, plus 4 random secret bytes). After `ActReady`, `Receive` consumes 0 bytes so any pipelined query bytes pass straight to `FrontendTracker`.

### SCRAM-SHA-256 as pure functions (`scram.go`, `scram_server.go`, `scram_client.go`)

SCRAM-SHA-256 lives in pure functions shared by `ClientSession`, `BackendSession`, `fakepg`, and `workload`. They use Go's standard `crypto/pbkdf2`, `crypto/hmac`, `crypto/sha256`, and `crypto/subtle` (for constant-time proof and signature comparison). Nonces come from an injected `Random` interface (`Uint64() uint64`), backed by a named seeded stream in the simulator and `crypto/rand` in the real shell.

Every SCRAM entry point enforces `MaxScramMessage = 2048` bytes, at most 16 comma-separated attributes, iteration counts in `[1, 2^20]`, and single-use steps (`s.step = scramDone` before validating).

On the server side, `ScramServer` offers only `SCRAM-SHA-256` and refuses `p=` channel-binding headers (`FATAL 08P01`). It accepts GS2 headers `n,,` and `y,,`, and checks that `c=` in `client-final-message` equals `base64(gs2)` (`biws` for `n,,`, `eSws` for `y,,`). On the client side, `ScramClient` always picks `SCRAM-SHA-256` even when `-PLUS` is also offered. It verifies that the server nonce extends the client nonce and checks the server's `v=` signature in `AuthenticationSASLFinal`, rejecting a server that sends a bad signature or skips directly to `AuthenticationOk`.

### Database-facing session machine (`backend.go`)

[`BackendSession`](../../internal/pgwire/backend.go) mirrors the client machine to log a pooled connection in to Postgres under `BackendTLSDisable`, `BackendTLSRequire`, or `BackendTLSVerifyFull`. Every startup failure ends under exactly one [`Class`](../../internal/pgwire/snapshot_wire.go) so Chunk 7's circuit breaker can count failures by cause:

- `tls-refused` (`'N'` when TLS is required) and `tls-failed` (handshake or certificate verification failure)
- `auth-failed` (wrong server signature, skipped signature, or `FATAL 28xxx`) and `auth-unsupported` (md5, cleartext, GSS, SSPI, Kerberos, SCM, or no `SCRAM-SHA-256` offered)
- `too-many-connections` (`53300`) and `server-fatal` (any other server `FATAL`, retaining its SQLSTATE in `Action.Code`)
- `protocol-violation`, `deadline`, and `connection-lost`

Both `ClientSession.close` and `BackendSession.fail` clear `s.scram = nil` on every exit path so a failed login never retains the plaintext database password. Once ready, both sessions ignore startup events.

### Query-phase trackers (`cycle.go`)

After startup completes:
- [`CycleTracker`](../../internal/pgwire/cycle.go) follows the database reply stream. Its constructor calls `SkipBodies` on every message type except `Z` (`ReadyForQuery`), `E` (`ErrorResponse`), `S` (`ParameterStatus`), `N` (`NoticeResponse`), and `A` (`NotificationResponse`). `DataRow`, `RowDescription`, `CommandComplete`, and any **unknown well-framed message type** pass through with a 0-byte body buffer (`Known: false`, incrementing `UnknownPassThrough`). `CycleEvent.End` reports exact cumulative stream offsets so the future relay can slice raw buffers on message boundaries. If EOF arrives while a query is outstanding or mid-frame (Probe B9), `PeerClosed` flags `InCycle: true` and increments `EndWithoutMarker`.
- [`FrontendTracker`](../../internal/pgwire/cycle.go) follows client messages after login. It copies bounded simple `Query` (`'Q'`) payloads and recognises `Terminate` (`'X'`). For all 11 extended-protocol, COPY, and function-call types (`P B E D C H S F d c f`), it skips the message body and reports `FrontExtended` (Chunk 5 will build their discard-until-sync error replies). Any unknown type, including `'p'` after login, is rejected as a violation.

---

## Where real Postgres 17 overruled the brief

Running the live tier R tests and startup fidelity script against `postgres:17` (`17.11 (Debian 17.11-1.pgdg13+2)`) caught three places where the builder brief's wording disagreed with the real server—plus a fourth during the adversarial pass. In each case, the code and `fakepg` were updated to match Postgres 17, and the orchestrator recorded the deviations in [`ADVERSARIAL.md`](../../agent_work/chunks/c04-wire-sessions/ADVERSARIAL.md):

1. **GS2 header over TLS when `-PLUS` is offered.**
   - *What the brief assumed:* The client sends `n,,` in plaintext and `y,,` over TLS.
   - *What Postgres 17.11 actually did:* Over TLS, Postgres offers `SCRAM-SHA-256-PLUS` alongside `SCRAM-SHA-256`. When a client sent `y,,` ("I support channel binding, but you did not offer it") to a server that *had* offered `-PLUS`, Postgres refused login with `FATAL 28000` ("SCRAM channel binding negotiation error"), captured in fidelity step `y-header-over-tls-with-plus-offered`.
   - *What the code does:* [`GS2Header(overTLS, plusOffered)`](../../internal/pgwire/scram.go) sends `y,,` over TLS **only** when `-PLUS` was *not* offered, and `n,,` otherwise—matching RFC 5802 and `libpq`. On the server side, `ClientSession` never offers `-PLUS` and accepts both `n,,` and `y,,` (which `psql` 16 sends over TLS); `fakepg` offers `-PLUS` over TLS and enforces the same `FATAL 28000` check.
2. **Protocol 3.x (`x > 0`) is negotiated down, not refused.**
   - *What the brief assumed:* Check for protocol `3.0`; anything else gets the `FATAL` Postgres sends.
   - *What Postgres 17.11 actually did:* On protocol `3.2` with a `_pq_.mg_option` parameter, Postgres sent `NegotiateProtocolVersion` (`'v'`) advertising newest minor `0` and listing `_pq_.mg_option`, and then continued straight into SCRAM login. On protocol `2.0`, it sent an old-format unframed error with no length word (`EFATAL:  unsupported frontend protocol 2.0: server supports 3.0 to 3.0\n\x00`), and on `4.0`, a normal protocol-3.0 `FATAL 0A000`.
   - *What the code does:* Both `ClientSession` and `fakepg` negotiate `3.x` (`x > 0`) and `_pq_.` options via `NegotiateProtocolVersion` and emit Postgres 17's exact bytes for major `< 3` and `> 3`.
3. **GSS encryption request answer (`G` vs `N`).**
   - *What the brief assumed:* The fake's startup conversation matches Postgres 17.
   - *What Postgres 17.11 actually did:* The official `postgres:17` Debian image is compiled with GSSAPI enabled and answers `GSSENCRequest` with `'G'`.
   - *What the code does:* Both `ClientSession` and `fakepg` answer `'N'` (as a server without GSSAPI does). `pgwiretest.KnownDifferences` documents `gss-request` as the single accepted fidelity difference, and `TestKnownDifferencesAreTheRecordedOnes` fails if that list drifts.

A fourth real-server check surfaced during the adversarial pass (Attack 11): when `ssl=off`, sending a *second* `SSLRequest` after receiving `'N'` does not receive another `'N'` from Postgres 17.11—it receives `FATAL 0A000: unsupported frontend protocol 1234.5679: server supports 3.0 to 3.0` (`1234.5680` for a repeated GSS request), because Postgres tracks `ssl_done`/`gss_done` flags in `ProcessStartupPacket`. `fakepg` was repaired to match that rule too (`TestFakeRefusesARepeatedEncryptionRequestAsPostgresDoes`).

---

## Integration with the observability spine (Chunk 3)

After rebasing onto Chunk 3, wire counters were wired into the collector without adding any adapter import to `internal/pgwire` (preserving the [one-raw-export rule](../../tools/lint-rules/imports.txt)):

- [`internal/obs/wiresrc`](../../internal/obs/wiresrc/wiresrc.go) maps `pgwire.WireSnapshot` to **47 metrics** under component `wire`: 21 `client_*` metrics and 26 `database_*` metrics. Putting the side in the metric name respects the two legs' different failure vocabularies (`Reason` vs `Class`) and leaves `mode` free for request kind (`tls`/`gss`/`cancel`), TLS version (`1.2`/`1.3`/`other`), and ready status (`I`/`T`/`E`). Every enum slot is emitted explicitly, including zero rows.
- [`internal/obs/simsrc`](../../internal/obs/simsrc/simsrc.go) maps Chunk 4's simulator additions (`fakepg.AuthSnapshot` and `workload.TenantSnapshot.Startup`). [`internal/obs/catalog`](../../internal/obs/catalog/catalog.go) declares `wiresrc.Metrics()`, and that declaration regenerates [`docs/csv-schema.md`](../../docs/csv-schema.md).
- Because `importlint` checks test files and forbids `internal/pgwire/...` from importing `simrun` or adapters, the 19 wire scenarios moved into an importable harness package, [`internal/pgwire/pgwiretest/wireworld`](../../internal/pgwire/pgwiretest/wireworld/wireworld.go). [`simrun.Wire`](../../internal/obs/simrun/wire.go) drives each scenario through all four adapters (`csv`, `json`, `prom`, `log`). It publishes at `t = 0`, every `WireInterval` (5 s virtual—coarser than the 100 ms baseline cadence because wire runs span 30 s), at the scenario's deadline-check instant `CheckAt()`, and at the end.

Flushing all five direct baselines and all 19 wire scenarios for seed 1 and checking them with [`scripts/csv-assert.sh`](../../scripts/csv-assert.sh) validates all 62 SQL files in DuckDB `1.5.5`: 5 baseline files plus 19 × 3 wire files (`startup-failures.sql`, `tls-requests.sql`, and `handshake-failures.sql`):

```sh
$ RUN_DIR=/tmp/c04-wire-runs go test -count=1 -run '^(TestScenarioFlush|TestScenarioWireFlush)$' ./internal/obs/simrun -seed=1
ok  	github.com/nickstrad/mini-gateway/internal/obs/simrun	11.210s

$ scripts/csv-assert.sh /tmp/c04-wire-runs | tail -n 5
ok: test/analysis/wire-startup-plain/tls-requests.sql over 1 runs
ok: test/analysis/wire-startup-tls/handshake-failures.sql over 1 runs
ok: test/analysis/wire-startup-tls/startup-failures.sql over 1 runs
ok: test/analysis/wire-startup-tls/tls-requests.sql over 1 runs
csv-assert: 62 SQL files run, 0 failures; v1.5.5 (Variegata) d8cdaa33fd
```

Querying the flushed wire `metrics.csv` files directly through [`test/analysis/views.sql`](../../test/analysis/views.sql) summarizes all 19 scenarios on seed 1 (every scenario runs 4 healthy `tenant0` clients at `t = 0` and 4 recovery `tenant1` clients at `t = 8 s`, plus one client-side or database-side fault at `t = 20 ms`):

```text
┌───────────────────────────────┬───────┬────────────────────┬───────┬──────────────────────┬─────────┬──────────┐
│           scenario            │ c_ok  │       c_fail       │ db_ok │       db_fail        │ c_tls_s │ db_tls_s │
├───────────────────────────────┼───────┼────────────────────┼───────┼──────────────────────┼─────────┼──────────┤
│ wire-client-bad-hello         │     8 │ tls-failed         │     8 │ -                    │       9 │        0 │
│ wire-client-plaintext-refused │     8 │ plaintext-refused  │     8 │ -                    │       8 │        0 │
│ wire-client-second-request    │     8 │ protocol-violation │     8 │ -                    │       9 │        0 │
│ wire-client-smuggle           │     8 │ protocol-violation │     8 │ -                    │       9 │        0 │
│ wire-client-tls-stall         │     8 │ deadline           │     8 │ -                    │       9 │        0 │
│ wire-client-unknown-user      │     8 │ auth-failed        │     8 │ -                    │       0 │        0 │
│ wire-client-wrong-password    │     8 │ auth-failed        │     8 │ -                    │       0 │        0 │
│ wire-db-bad-certificate       │     9 │ -                  │     8 │ tls-failed           │       0 │        9 │
│ wire-db-connect-stall         │     9 │ -                  │     8 │ deadline             │       0 │        0 │
│ wire-db-garbage               │     9 │ -                  │     8 │ protocol-violation   │       0 │        0 │
│ wire-db-handshake-stall       │     9 │ -                  │     8 │ deadline             │       0 │        9 │
│ wire-db-max-connections       │     9 │ -                  │     8 │ too-many-connections │       0 │        0 │
│ wire-db-reset-mid-handshake   │     9 │ -                  │     8 │ connection-lost      │       0 │        0 │
│ wire-db-silent-startup        │     9 │ -                  │     8 │ deadline             │       0 │        0 │
│ wire-db-tls-refused           │     9 │ -                  │     8 │ tls-refused          │       0 │        8 │
│ wire-one-byte                 │     8 │ -                  │     8 │ -                    │       8 │        8 │
│ wire-one-byte-plain           │     8 │ -                  │     8 │ -                    │       0 │        0 │
│ wire-startup-plain            │     8 │ -                  │     8 │ -                    │       0 │        0 │
│ wire-startup-tls              │     8 │ -                  │     8 │ -                    │       8 │        8 │
└───────────────────────────────┴───────┴────────────────────┴───────┴──────────────────────┴─────────┴──────────┘
```

In every client-fault scenario, 8 healthy sessions complete on both legs and the fault client closes under its exact reason. In every database-fault scenario, all 9 clients authenticate to the proxy (`c_ok = 9`), the one database connection opened during the fault window fails under its exact class within its deadline (2 s virtual for stalls), and recovery succeeds once the fault clears at `t = 7 s`.

---

## Testing, fidelity, and the planted-bug canary

Before prose comments, Chunk 4 adds 2,613 lines of non-test Go in `internal/pgwire`, 1,010 in `pgwiretest`, 383 in `wireworld`, 73 in `simtls`, 159 in `wiresrc`, 90 in `simrun/wire.go`, and ~600 in simulator extensions—backed by 89 test and fuzz functions:

```sh
$ go test -v -count=1 ./internal/pgwire -run '^(TestBytesSentEarlyAfterAnAcceptedTLSRequest|TestWrongPasswordAndUnknownUserLookAlike|TestPlantedEarlyBytesBugFailsTheSmuggleScenario|TestCycleTrackerStreamsManyRowsWithoutBuffering|TestScramRFC7677ServerSide|TestScramRFC7677ClientSide)$'
=== RUN   TestBytesSentEarlyAfterAnAcceptedTLSRequest
--- PASS: TestBytesSentEarlyAfterAnAcceptedTLSRequest (0.02s)
=== RUN   TestWrongPasswordAndUnknownUserLookAlike
--- PASS: TestWrongPasswordAndUnknownUserLookAlike (0.07s)
=== RUN   TestCycleTrackerStreamsManyRowsWithoutBuffering
--- PASS: TestCycleTrackerStreamsManyRowsWithoutBuffering (0.02s)
=== RUN   TestScramRFC7677ClientSide
--- PASS: TestScramRFC7677ClientSide (0.01s)
=== RUN   TestScramRFC7677ServerSide
--- PASS: TestScramRFC7677ServerSide (0.00s)
=== RUN   TestPlantedEarlyBytesBugFailsTheSmuggleScenario
--- PASS: TestPlantedEarlyBytesBugFailsTheSmuggleScenario (0.04s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/pgwire	0.194s
```

The test suite spans six layers:

1. **Golden wire bytes and RFC 7677:** [`TestGoldenBytes`](../../internal/pgwire/frame_test.go) pins the exact hex bytes of all 26 produced message shapes in `internal/pgwire/testdata/golden.txt`. `TestScramRFC7677ClientSide`/`ServerSide` verify both sides against the RFC 7677 test vector.
2. **Every-chunking replays and fuzzing:** Nearly every client, backend, and tracker test runs through `requireEveryChunking` / `requireBackendEveryChunking` / `requireCycleChunkings`. Each helper replays a conversation whole, one byte at a time, and split at *every* byte boundary of every step, requiring identical action traces and consumed byte counts. Four new fuzz targets (`FuzzFramer`, `FuzzClientSession`, `FuzzBackendSession`, `FuzzCycle`, bringing the repository total to 11) assert no panics, bounded buffers, startup conservation, and agreement across whole, split, and one-byte chunkings.
3. **Zero-buffer row streaming:** `TestCycleTrackerStreamsManyRowsWithoutBuffering` feeds 200,000 `DataRow` messages and asserts `BufferCap() == 0` throughout. `TestCycleTrackerPassesARowLargerThanItsMessageLimit` streams a 4 KiB `DataRow` through a tracker with a 128-byte inspected-message cap.
4. **Planted-bug canary:** [`TestPlantedEarlyBytesBugFailsTheSmuggleScenario`](../../internal/pgwire/client_canary_test.go) runs `wire-client-smuggle` on seeds 1–3 normally (must pass) and again with `pgwire.PlantEarlyBytes` disabling the `csTLSWait` guard (must fail on `"client startup failures"`). Similarly, [`TestWireSQLCatchesPlantedNumbers`](../../internal/obs/simrun/wiresql_test.go) mutates the final flush of every scenario's `metrics.csv` and verifies that `scripts/csv-assert.sh` exits non-zero, naming all three SQL files with violating rows.
5. **Simulated end-to-end sweeps and cross-process replay:** `TestScenarioWireStartup` runs all 19 scenarios on pinned seeds (`1, 2, 3, 7, 42`) and a rotating stride-4 subset across the 200-seed sweep (4,469 total seed runs across the repo in Gate G4, 0 failures). [`gate/extra.sh`](../../agent_work/chunks/c04-wire-sessions/gate/extra.sh) (Gate G10) runs the DuckDB SQL assertions and the early-bytes canary, and checks that all 19 seed-42 trace and state digests match across two separate OS processes.
6. **Live Postgres 17 and `psql` (Tier R):** [`scripts/tier-r.sh`](../../scripts/tier-r.sh) starts a third container (`-scram`, running `scram-sha-256` with `ssl=on` and the image's snakeoil certificate). In Gate G6 against Postgres 17.11, all 7 tier R tests passed. `TestTierRScramClientLogsInToPostgres` covers plaintext, TLS with `-PLUS` offered, wrong password, `verify-full`, TLS refused, and unknown database; `TestTierRScramServerAcceptedByPsql` runs 9 `psql` 16 cases across all three TLS modes; and `TestTierRStartupFidelity` matches all 12 steps of [`startup-postgres17.txt`](../../internal/pgwire/pgwiretest/testdata/startup-postgres17.txt), which CI replays offline against `fakepg`.

---

## Reviews, adversarial attacks, and narrowed claims

Chunk 4 went through one review-and-repair round, one adversarial pass, a second review of the repair, and a third scoped review after integrating with Chunk 3.

### Review 1 and the adversarial pass

Against the initial build, [`REVIEW-1.md`](../../agent_work/chunks/c04-wire-sessions/REVIEW-1.md) returned **VERDICT: FAIL** (3 majors, 4 minors, and 1 surviving mutation out of 10: `M10` disabled `SkipBodies` in `CycleTracker` and no test failed because `TestCycleTrackerStreamsManyRowsWithoutBuffering` only checked `BufferCap() <= 1024`).

In parallel, the black-box adversarial suite (Astra seat, [`RESULTS.md`](../../agent_work/adversarial/c04-wire-sessions/RESULTS.md)) ran 35 independent named attacks across all 11 claims (plus a 50,000-seed / 600,000-step hostile-input campaign that held) and reported **HELD: 28, BROKEN: 7**:

1. **Invalid handshake event enums (`TestAdv_2_InvalidHandshakeEnums`, Review 1 Finding 5):** Passing an out-of-range `HandshakeOutcome` or `TLSVersion` indexed snapshot arrays out of bounds and panicked. Fixed by `HandshakeResult.normalized()` clamping invalid values to `HandshakeFailed` / `TLSVersionOther`.
2. **Unbounded SCRAM payloads (`TestAdv_2_ScramMessageBound`, `TestAdv_2_WireSCRAMBound`):** The pure SCRAM functions accepted messages larger than `MaxScramMessage` (2,048 bytes), and the outer frame cap (`maxAuthMessage = 2,112`) let a 2,061-byte SCRAM message through `ClientSession`. Fixed by checking `len(msg) > MaxScramMessage` in all four SCRAM server/client entry points.
3. **Handshake deadline classification (`TestAdv_7_HandshakeDeadlineClassification`, Review 1 Finding 5):** `HandshakeDone` classified `HandshakeDeadline` and `HandshakeViolation` as `tls-failed` instead of `deadline` and `protocol-violation`. Fixed on both legs.
4. **Repeated refused `SSLRequest` fidelity (`TestAdv_11_RepeatedSSLAfterRefusal`):** With `ssl=off`, `fakepg` answered a second `SSLRequest` with `'N'` where Postgres 17.11 returned `FATAL 0A000`. Fixed in `fakepg/auth.go`.
5. **Unknown-user iteration oracle (`TestAdv_5_UnknownUserIterationOracle`):** With `MockIterations` left at 0 and a tenant table using 32-iteration verifiers, known users got `i=32` while unknown users got `i=4096`. Fixed for uniform tables by `TenantTable.mockShape()` (and narrowed for mixed tables below).
6. **Independent key-issuer collision (`TestAdv_9_IndependentIssuerCollision`):** Two separate `KeyIssuer` instances fed the same constant random stream issued identical keys. Narrowed below.

The repair round also fixed Review 1's remaining findings: tightening the `CycleTracker` zero-buffer test (`M10`), renaming `ReplyTracker` to `CycleTracker` (avoiding Chunk 5's reserved `Reply` prefix; `NewReplyTracker` stays as a one-line compatibility alias for the frozen adversarial test), guarding `BackendSession.fail()` so a ready session ignores `Deadline`/`PeerClosed`, clearing `s.scram = nil` on failure, preserving client bytes coalesced with `ClientHello` in `pgwiretest`, and removing dead fields (`midMessage`, `ReasonTerminate`).

After the repair round ([`ADVERSARIAL.md`](../../agent_work/chunks/c04-wire-sessions/ADVERSARIAL.md)), **34 of the 35 attacks pass, 0 fail, and 1 is settled by the orchestrator** (`TestAdv_9_IndependentIssuerCollision`, skipped under the narrowed Claim 9).

### Narrowed claims (accepted by the orchestrator)

- **Claim 9 (proxy-issued key uniqueness):** Originally stated that proxy-issued key data is unique per session. As narrowed: **unique among the sessions of one `KeyIssuer`** (non-zero sequential process IDs unique for `2^31 - 1` issues, random 4-byte secret, never copied from a backend key). Two independent issuers share no state and produce identical keys if fed identical random streams; a real proxy instance keeps one issuer seeded from `crypto/rand` (as Postgres scopes cancel keys to one postmaster).
- **Claim 5 (unknown user vs wrong password indistinguishability):** Holds for every reply byte shape (message types, SQLSTATE `28P01`, message template) and for the SCRAM challenge (`s=` length and `i=`) **when every verifier in the tenant table uses the same iteration count and salt length** (with default `MockIterations = 0`). In a table that mixes verifier shapes, a user whose verifier differs from the modal shape can be distinguished from an unknown user by `i=` or `s=` length, matching Postgres's own mock behavior. Timing is not part of the claim.

### Reviews 2 and 3 (NITS carried to Chunk 6)

[`REVIEW-2.md`](../../agent_work/chunks/c04-wire-sessions/REVIEW-2.md) (17/17 mutation checks caught) and [`REVIEW-3.md`](../../agent_work/chunks/c04-wire-sessions/REVIEW-3.md) (scoped review of the Chunk 3 integration, 7/8 mutations caught by Go tests and 8/8 by `importlint` + tests) both closed with **VERDICT: NITS**, carrying four minor follow-up items into Chunk 6:

1. `NewTenantTable` does not yet cap verifier salt length against `maxMockSaltLen` (128 bytes), to be validated alongside mixed-shape checks in Chunk 6's config loader.
2. `snapshot_wire_test.go` retains a stale skip entry for `Database.TLSFailed[HandshakeViolation]` even though `busyStats` now exercises it.
3. `TestOneRawExportImportRule` in `internal/obs/simrun/imports_test.go` checks `wiresrc` → adapter but does not mirror `pgwire` → adapter or adapter → `wiresrc`/`pgwire` (both of which `importlint` already enforces).
4. Five metrics compared in `handshake-failures.sql` and `tls-requests.sql` are omitted from `-- requires:` headers (guarded in Go by `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented`, kept short for DuckDB startup time).

---

## Stated limits

- **No SASLprep:** Passwords are hashed as raw bytes. ASCII passwords match Postgres; non-ASCII passwords that SASLprep would normalize will not match a Postgres-generated verifier.
- **No channel binding (`-PLUS`), client certificates, direct TLS, GSS, md5, or cleartext:** A client configured with `channel_binding=require` refuses on its own because `ClientSession` never advertises `SCRAM-SHA-256-PLUS`; `channel_binding=prefer` (the default) works. Postgres 17's optional `sslnegotiation=direct` is out of v1.
- **Simulated TLS is not cryptography:** `simtls` models outcomes, TLS version reporting, and virtual duration with 16-byte tokens, not TLS record framing or handshake CPU cost. Real `crypto/tls` handshakes run in Chunk 6 (and in tier R tests here).
- **Sequential process IDs within one issuer:** `KeyIssuer` increments process IDs sequentially from a random base; unpredictability of the cancel credential rests on the 32-bit random secret (as in Postgres).
- **No SQL classification or pooling yet:** `pgwiretest.Proxy` opens one backend connection per client and forwards simple `Query` messages verbatim. Statement classification (Chunk 5) is paused while its scope is reviewed, and the real engine, socket shell, and connection pool arrive in Chunks 6 and 7.
