# Wire sessions: the startup machines, SCRAM and simulated TLS (chunk 4, 2026-11-25)

Facts a later builder (chunk 6's shell and engine, chunk 7's pool) needs before using
`internal/pgwire`'s session machines. The full decision list is in
`agent_work/chunks/c04-wire-sessions/NOTES.md`.

## Driving a machine

- `NewClientSession(cfg)` / `NewBackendSession(cfg)` count a startup as begun at once. Feed
  bytes with `Receive(acts, b) (acts, n)`; tell it `HandshakeDone`, `Deadline`, `PeerClosed`.
  `BackendSession.Start` writes the `SSLRequest` or the startup message. Actions:
  `ActWrite`, `ActStartTLS`, `ActReady`, `ActCancel`, `ActClose` (with `Reason`, or `Class`
  for the database side).
- `n < len(b)` only when the machine is done with startup (ready or closed). After
  `ActReady` the rest of the bytes belong to the query phase: `FrontendTracker` (client) and
  `ReplyTracker` (database). The machines never consume past their last startup message.
- After `ActStartTLS` the shell must read **exactly** the 8 bytes of the request (it is a
  separate `Receive` call in the tests) and hand the socket to TLS. Bytes the machine sees
  before `HandshakeDone` are a violation (`bytes-before-handshake`), not data.
- `ClientConfig.Keys` (one `KeyIssuer`) and `Random` must be shared by all sessions of an
  instance: keys are unique per issuer (sequential process ids from a random base, random
  secrets). `MockSecret` is required for SCRAM (salts of unknown users).
- Deadlines are events: the shell owns the timers (`DefaultClientStartupTimeout` 5 s,
  `DefaultConnectTimeout` 2 s).

## Facts found against real Postgres 17.11 (tier R)

- Over TLS Postgres offers `SCRAM-SHA-256-PLUS` first. A client that sends the GS2 header
  `y,,` while `-PLUS` was offered is refused with `FATAL 28000` ("SCRAM channel binding
  negotiation error"). So the rule is `GS2Header(overTLS, plusOffered)`: `y,,` only over TLS
  when `-PLUS` was **not** offered; `n,,` otherwise. libpq (psql 16) sends `y,,` to our
  client-facing machine over TLS, which never offers `-PLUS`.
- The `postgres:17` image answers a GSS encryption request with `G` (built with GSSAPI). The
  fake and the proxy answer `N`; the fidelity script lists this as its one known difference.
- Protocol 2.0 (or 1.0) gets an old-format error: `E` + `"FATAL:  unsupported frontend
  protocol 2.0: server supports 3.0 to 3.0\n"` + NUL, no length word. 4.0 gets a normal
  `FATAL 0A000`. 3.x with x > 0, or any `_pq_.` option, gets `NegotiateProtocolVersion`
  (newest 3.0, the unknown options listed) and the login continues.
- Unknown user under SCRAM: Postgres runs a mock exchange and fails at the end with the same
  `28P01` as a wrong password. Unknown database: `R12 R0` and then `FATAL 3D000`.
- `scripts/tier-r.sh` now starts a third container (`-scram`: scram-sha-256, password
  `mg-tier-r-scram`, `ssl=on` with the image's snakeoil certificate), exported as
  `MG_PG_SCRAM_ADDR` / `MG_PG_SCRAM_PASSWORD`. `MG_TIER_R_UPDATE=1` also rewrites
  `internal/pgwire/pgwiretest/testdata/startup-postgres17.txt`.

## Simulated TLS (`internal/sim/simtls`)

A handshake is 16-byte tokens over simnet, not cryptography: the client sends
`AppendClientHello`, the server answers `AppendServerReply` (ok, bad certificate, alert)
after a virtual duration. The fake's answer is `fakepg.Config.TLS` (`Answer` `N`/`S`,
`Outcome` completed / failed / stall / bad certificate) and can change during a run with
`Server.SetTLS`. A client in verify-full mode turns "bad certificate" into
`HandshakeFailedVerification`; require mode accepts it.

## The startup-only proxy (`internal/pgwire/pgwiretest`)

`pgwiretest.New(net, Config)` listens on simnet, runs a `ClientSession` per client, logs a
`BackendSession` in to the fake for each ready client (holding the client's final bytes until
then), and relays queries with the two trackers. A database login failure is sent to the
client as `FATAL 08006 could not connect to the database: <class>`. It is a test harness, not
the engine of chunk 6.
