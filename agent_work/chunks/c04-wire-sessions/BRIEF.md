# mini-gateway — chunk 4: Wire sessions — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c04-wire-sessions`.
Worktree: `/root/Software/mini-gateway-work/c04` — branch `work/c04-wire-sessions`.
This chunk is built concurrently with chunks 3 (`internal/obs` collector and adapters) and
5 (the SQL lexer and classifier in `internal/pgwire`, files `lex*.go`, `classify*.go`,
`errors*.go`, `reply*.go`, and `internal/work`). Stay inside your files so the three merge
cleanly: you own, in `internal/pgwire`, the files `frame*.go`, `prestartup*.go`,
`client*.go` (client-facing session), `backend*.go` (database-facing session), `scram*.go`,
`cycle*.go`, `params*.go`, `snapshot_wire*.go`, and their tests. Do not create
`internal/work`, `internal/proxy`, `internal/shell`. For the few proxy-made error replies
your machines must emit (FATAL at startup), build the bytes with a small local helper
`fatal*.go`; chunk 5 owns the general error table and will absorb it at chunk 6.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — the rules for every builder. They bind you.
2. `PLAN.md`: "Chunk 4 — Wire sessions" (your scope); section 3.1 in full (the TLS verdict:
   the exchange before startup, the GS2 rule, what v1 includes and leaves out); decisions
   D1, D2, D6, D8, D13; section 4.1 (the probe evidence, rows 1, 4, 5, 6, 9, 10 and the
   codec notes); section 9.1 (the error rows for startup and TLS), 9.2 (the startup and
   connect deadlines), 10.4 (the TLS and startup faults on both sides).
3. The probes — ground truth: `agent_work/phase1/probes/pgwire/main.go` (a working SCRAM
   client in ~50 lines, the 15-line framer, message decoding without `pgproto3.Frontend`)
   with `output-2026-11-24.txt`; `agent_work/phase1/probes/pgtls/main.go` with its output;
   `docs/knowledge/phase1-findings.md`, `docs/knowledge/tls-cost-assessment.md`.
4. What exists: the simulator (`internal/sim`, `simnet`, `fakepg`, `workload`, scenarios,
   `scripts/tier-r.sh`), notes in `agent_work/chunks/c01-sim-kernel/NOTES.md` and
   `c02-sim-world/NOTES.md`, `docs/knowledge/`. The simulator API is frozen (tag
   `freeze-sim`). This chunk has a planned, allowed extension to the simulated world: SCRAM
   in `fakepg` and in the workload client, the TLS-request answers and the simulated TLS
   handshake events (see below). Keep those changes additive, and list them under
   "Simulator changes" in NOTES.md.
5. `docs/knowledge/foundation-gates.md`.

## Purpose

Speaking Postgres on both sides: taking a client through the exchange before startup,
startup and login, and logging the proxy in to the database — as **pure, sans-I/O state
machines** that cope with bytes arriving in any fragments. Events in (bytes, a handshake
result, a deadline reached), actions out (write these bytes, start TLS, close with reason,
session ready). No goroutines, no sockets, no clock reads, no `crypto/tls` (real TLS is
chunk 6; here a TLS handshake is an event with an outcome). The package passes the
determinism lint.

## Deliverables — `internal/pgwire`

- **The incremental framer**: bytes in any chunking → complete messages; handles the
  untyped startup-phase packets (length + code) and typed messages; a maximum message size
  (named constant / config) — an oversized or impossible length is a protocol violation
  detected from the header alone, without buffering the body; bounded buffer; never reads
  past the bytes it needs (see "exactly 8 bytes" below).
- **The client-facing session machine**:
  - *The exchange before startup* (PLAN.md 3.1): an `SSLRequest` is answered `S` or `N`
    according to `client_tls.mode` (`off` → `N`; `allow`, `require` → `S`); a GSS encryption
    request is answered `N`; at most one TLS upgrade; after answering `S` the machine emits
    a **"start TLS" action** and accepts NO bytes until a **handshake-result event**
    (completed with a TLS version / failed with a reason / the deadline) arrives — any byte
    delivered between an accepted `SSLRequest` and the handshake result is a protocol
    violation and closes the connection (the CVE-2021-23214 class); a second `SSLRequest`
    after the upgrade is a violation; the machine consumes exactly the 8 bytes of the
    request and reports how many bytes it consumed so the shell can prove nothing was
    buffered across the upgrade.
  - A plaintext startup message while the mode is `require` → `FATAL 28000` and close.
  - A `CancelRequest` packet is recognised (in plaintext or after an upgrade) and surfaced
    as an event (process id, secret key) — the connection then closes without a reply.
  - Startup message: protocol version check (3.0; anything else → the FATAL Postgres
    sends), parameters parsed (`user`, `database`, `application_name` accepted and ignored,
    other startup parameters compared with the pool's fixed session settings → a differing
    one is refused with `0A000` per PLAN.md 9.4 — make the allowed set a config value),
    **tenant lookup by user** in a static table → unknown user gets the same failure as a
    wrong password (no user enumeration; state the exact reply), wrong database → `3D000`.
  - **SCRAM-SHA-256, server side**: offers only `SCRAM-SHA-256` (never `-PLUS`); accepts
    the GS2 headers `n,,` and `y,,` and rejects `p=...`; verifies the matching `c=` value
    (`biws` for `n,,`, `eSws` for `y,,`); the table holds each tenant's verifier (salt,
    iteration count, stored key, server key) — never a password; nonce from an injected
    random source (a named seeded stream in the simulator; the shell injects `crypto/rand`
    later); constant-time proof comparison (`crypto/subtle`); wrong proof → `FATAL 28P01`.
  - **Trust** only as an explicitly named test setting, never a default.
  - On success: `AuthenticationOk`, the parameter-status set (the server parameters
    collected from the database side by the pool's first connection are passed in as
    input; a fixed default set until then), **proxy-issued `BackendKeyData`** (process id
    and secret from the injected random source, unique per client session — the mapping to
    a real backend key is chunk 7), `ReadyForQuery('I')`.
  - Termination (`Terminate` message, EOF) and the startup deadline (one deadline covers
    the pre-startup exchange, the handshake and authentication; the machine is told
    "deadline reached" as an event and closes with the right reason).
- **The database-facing session machine**: when `backend_tls.mode` asks for TLS the proxy
  sends its own `SSLRequest`; `S` → "start TLS" action then a handshake-result event; `N`
  when TLS is required → a connect failure of its own class; then the startup message
  (configured role and database, the pool's fixed session settings), `AuthenticationSASL` →
  **SCRAM-SHA-256 client side** (choose `SCRAM-SHA-256` even when the server also offers
  `-PLUS`; GS2 header `n,,` in plaintext and `y,,` over TLS; verify the server signature —
  a wrong server signature is an authentication failure), also `AuthenticationOk` directly
  (trust) and a clear failure for unsupported methods (md5, cleartext, GSS...); collect the
  server's `ParameterStatus` set and `BackendKeyData`; ready at `ReadyForQuery('I')`. Every
  failure has a class (refused TLS, TLS failed, auth failed, FATAL from server with its
  SQLSTATE — `53300` too-many-connections is its own class, protocol violation, deadline)
  because the pool's breaker counts by class.
- **SCRAM as pure functions** in one file set, shared with `fakepg` (server side: add SCRAM
  to the fake, selectable per scenario alongside trust) and with the workload client
  (client side). Password → verifier derivation uses `crypto/pbkdf2` (Go 1.24+ standard
  library), `crypto/hmac`, `crypto/sha256`; SASLprep is NOT implemented — state the limit
  (ASCII passwords; non-ASCII passed through as bytes).
- **The query-cycle tracker**: follows a database reply stream to its `ReadyForQuery` and
  reports the status byte (`I`/`T`/`E`), message boundaries (so the engine can relay
  complete messages or raw bytes), unsolicited messages during a client's turn
  (`ParameterStatus` — the D8 dirty signal, `NoticeResponse`, `NotificationResponse`), an
  `ErrorResponse` with its severity and SQLSTATE (a `FATAL` means the stream will end),
  end of stream without the end marker (probe B9), and protocol surprises. **Unknown but
  well-framed message types on the database side are passed through, not rejected.** On
  the client side during the query phase it recognises `Query`, `Terminate`, and reports
  any extended-protocol message type (`Parse`, `Bind`, `Execute`, `Describe`, `Close`,
  `Flush`, `Sync`, `CopyData`...) as "extended, to be refused" (chunk 5 builds the reply).
- **Simulator additions** (allowed for this chunk): `fakepg` — SCRAM server (using the
  shared functions), its answer to an `SSLRequest` configurable (`N`, or `S` followed by a
  simulated handshake outcome), the two database-side TLS faults of section 10.4 (answers
  `N` when TLS is required; "presents a certificate that fails verification" = handshake
  outcome failed-verification); the workload client — SCRAM client, an optional TLS request
  first (libpq's default behaviour), and the client-side TLS faults of 10.4 (sends a TLS
  request and then stalls; fails the handshake; sends plaintext bytes after the TLS
  request; sends a second TLS request after the upgrade). In the simulator a TLS handshake
  is an event with an outcome (completed, failed, stalled) and a virtual duration — not
  cryptography. Provide the small harness that wires a simulated client → your client
  machine → your backend machine → `fakepg` back-to-back over simnet (a "startup-only
  proxy" used by this chunk's e2e; it lives in a test or `internal/pgwire/pgwiretest`
  package, not in `internal/proxy`).

## Tests (write these first)

- **Golden bytes** for every message the machines produce (testdata).
- **Every-chunking tests**: each scripted conversation (client side and database side) is
  replayed split at EVERY byte boundary (and with one-byte delivery) and must give the same
  sequence of events and actions.
- **Fuzzing** of the framer and both machines (`FuzzFramer`, `FuzzClientSession`,
  `FuzzBackendSession`, `FuzzCycle`): no panic, bounded memory, and the "two chunkings
  agree" property. Seed corpora committed.
- **SCRAM**: the RFC 7677 test vector (client and server sides); cross-test of your client
  against your server; wrong password; tampered server signature; tampered nonce; `p=`
  header refused; each GS2 header accepted with its `c=` value and rejected with the other.
- Wrong password, unknown user, wrong database, bad protocol version, differing startup
  parameter → the right FATAL with the right SQLSTATE, and the unknown-user reply is
  byte-identical in shape to the wrong-password reply.
- **The exchange before startup, for every `client_tls.mode`**: a default-style client
  (TLS request first), a plaintext client, a GSS request then a TLS request, a second TLS
  request after the upgrade, bytes sent early (an `SSLRequest` and a startup message in one
  delivery → the machine reports exactly 8 bytes consumed and flags the surplus as a
  violation), a handshake that fails, one that never finishes (deadline), a cancel request
  before and after an upgrade.
- Database side: TLS required and answered `N`; handshake failed; each auth outcome; a
  FATAL during startup (`53300`, `28P01`, `3D000`) classified; garbage; silence (deadline).
- Cycle tracker: the probe's reply shapes (single result, two result sets in one message,
  error then ready, error inside a transaction → `E`, FATAL then end of stream with no
  marker, unsolicited `ParameterStatus`, 200,000 `DataRow`s without buffering them, an
  unknown message type passed through).
- **Tier R** (through `scripts/tier-r.sh`, real Postgres 17): your SCRAM client machine
  logs in to a real Postgres with `scram-sha-256` auth; your SCRAM server machine is
  accepted by a real `psql` (drive your client-facing machine from a tiny test-only TCP
  listener in the test, no TLS); **fidelity for startup**: the fake's startup conversation
  matches Postgres 17 in message types, including its answers to a TLS request with `ssl`
  on and off (record transcripts for CI as chunk 2 did).
- **End-to-end for this chunk, in the simulator**: a simulated client completes startup
  through the two machines wired back-to-back to the fake Postgres — under one-byte
  delivery, and under each startup-phase fault on each side (accept and silence, reset
  mid-handshake, garbage, connect stall, the TLS faults), each ending in the expected error
  class within its deadline on the virtual clock, with recovery (a fresh client then
  succeeds), quiesce, replay, and non-zero counters. Ablation: with the "no bytes between
  `S` and the handshake result" rule switched off by the test-only bug switch, the
  smuggled-bytes scenario must FAIL its assertion (this is also the planted-bug canary).
- **CSV-flush assertion** (if chunk 3's `scripts/csv-assert.sh` and collector are not on
  your branch — they are being built concurrently — write the SQL files under
  `test/analysis/<scenario>/` to the schema of PLAN.md 7.7 and the same assertions as exact
  Go checks on the snapshots; the orchestrator wires the flush at integration): startup
  failures by reason in the wire snapshot equal the startup faults injected by kind, and
  TLS requests by kind and answer equal the requests sent.

## Observability obligations (landing gate)

Wire `Snapshot()`: startups begun, completed and failed by reason; pre-startup requests by
kind and answer; TLS handshakes completed and failed by reason; plaintext refused;
authentication failures by cause; cancel requests seen; messages and bytes by direction;
protocol violations by kind; oversized messages. Per side (client, database). Conservation:
startups begun = completed + failed + in progress. Counters-move test. Health: nothing new.

## Claims this chunk makes (an adversary will try to break each)

1. Any fragmentation of the same byte stream produces the same events and actions, on both
   machines and the cycle tracker.
2. No input can make the framer or a machine panic, loop, or hold more than the stated
   bounded memory; an oversized length is refused from the header.
3. Nothing sent between an accepted `SSLRequest` and the handshake result is ever
   interpreted; the machine consumes exactly 8 bytes for the request; at most one upgrade.
4. With `client_tls.mode = require` no plaintext startup is ever accepted; with `off` an
   `SSLRequest` is answered `N` and plaintext startup works; `allow` accepts both.
5. SCRAM server: only a client knowing the password authenticates; `-PLUS` is never
   offered; `n,,` and `y,,` are accepted with the right `c=`; `p=` is refused; unknown user
   and wrong password are indistinguishable in the reply; the verifier table never holds a
   password.
6. SCRAM client: a server that does not know the verifier (wrong server signature) is
   rejected; `SCRAM-SHA-256` is chosen when `-PLUS` is also offered.
7. Every startup failure, on each side, ends in the documented error class and reply
   within the deadline, and is counted under exactly one reason.
8. The cycle tracker reports the correct status byte and end of reply for every reply
   shape in the probe, never buffers a whole reply, passes unknown well-framed message
   types through, and reports an unsolicited `ParameterStatus`.
9. Proxy-issued key data is unique per session and unrelated to the backend's.
10. `internal/pgwire` is deterministic and sans-I/O (determinism lint passes; no
    `crypto/tls`, `net`, `time.Now`, goroutines).
11. The fake's startup conversation, including its TLS-request answers, matches Postgres 17.

## Steady-state row

What grows: the framer buffer (bounded by the maximum message size), SCRAM exchange state
(bounded, freed at completion or failure), the parameter set. Gauges: sessions in startup.

## Size

About 1,500 lines of product code (about 100 of them the TLS exchange), may reach twice that.

## Finish

As the builder rules say: gate green at the build stage (`scripts/gate.sh
c04-wire-sessions`, including tier R), NOTES.md complete at
`agent_work/chunks/c04-wire-sessions/NOTES.md` (state every reply byte-shape decision, the
failure-class list, the simulator changes), clean tree, summary, and a final line containing
exactly AGENT-DONE.


## Notes from the orchestrator on your starting point (read before you start)

- Your branch starts from chunk 2 as repaired and attacked (`work/c02-sim-world`), which is
  in its second review and has not landed yet; chunk 2 gets a comments-only prose pass on
  another branch and your branch will be rebased onto it. Prefer NEW files; where you must
  edit an existing file keep the edit small and local, and list it in NOTES.md. The gate at
  the build stage compares comments with the merge base with `main`; chunk 2 files are
  comment-free in your tree and that passes.
- What chunk 2 actually built (names differ slightly from the wording above): the fake is
  `internal/sim/fakepg`; simulated clients are `internal/sim/workload`; the sans-I/O test
  client (framer, codec, session) is `internal/testclient`; scenarios, the driver interface
  and the fidelity runner are `internal/sim/world`; float-using generators are
  `internal/randdist`. Read `agent_work/chunks/c02-sim-world/NOTES.md` and
  `docs/knowledge/sim-world.md` first.
- Do NOT edit `agent_work/INDEX.md` (three chunks are building at once). Put the index rows
  you would add in a section "Index rows" at the end of your NOTES.md; the orchestrator adds
  them. The same goes for shared one-line lists: add your lines to
  `tools/lint-rules/det-packages.txt`, `race-packages.txt`, `imports.txt`,
  `prose-paths.txt` and `.gitignore` only by APPENDING lines at the end of the file.
- `scripts/gen-docs.sh` is shared by chunks 3 and 5: if it is still the skeleton, replace it
  with a small driver that runs every executable `scripts/gen-docs.d/*.sh` (passing
  `--check` through) and delete its marker; put YOUR generator in its own file there
  (`scripts/gen-docs.d/<your-chunk>-<name>.sh`). Both chunks will write the same driver;
  keep it to this exact shape so the merge is trivial:
  `for f in scripts/gen-docs.d/*.sh; do [ -x "$f" ] && "$f" "$@" || exit 1; done` after the
  usual header and `set -eu`.
- `internal/pgwire` is shared by chunks 4 and 5 (one Go package, two builders). To avoid
  name collisions: chunk 4 names its snapshot type `WireSnapshot` and chunk 5
  `ClassifierSnapshot`; chunk 5 exports only identifiers beginning with `Lex`, `Token`,
  `Classif`, `Reject`, `ErrorRow`, `ErrRow`, `Reply`, `SyncDiscard`, `Allow`; every
  unexported package-level identifier of chunk 5 begins with `lex`, `cls`, `rej`, `erow` or
  `rpl`; chunk 4 uses none of those prefixes. Test helper names follow the same rule.
  Neither chunk adds a `TestMain` or a `doc.go` to `internal/pgwire`.
