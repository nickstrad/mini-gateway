# mini-gateway — chunk 6: Walking skeleton — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c06-walking-skeleton`.
Worktree: `/root/Software/mini-gateway-work/c06` — branch `work/c06-walking-skeleton`.

This is the integration chunk: the first complete proxy. It is sized large on purpose. It
is built ALONE (no concurrent chunk), on top of everything landed so far.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — the rules for every builder. They bind you.
2. `PLAN.md`: "Chunk 6 — Walking skeleton" (your scope); decisions D1–D8, D10–D13 in
   section 3 — **D8 was rewritten by Nick's ruling 9 of 2026-11-26: the proxy resets every
   database connection with `DISCARD ALL` after every lease, and session safety never
   depends on reading SQL**; section 18 row 9; sections 6.1 and 6.6 (operation fuzzing and
   the three rules for simulator fuzz targets); section 3.1 (TLS: "What v1 includes", findings 5–7); section 4.2 (the three
   seams — you create the `internal/proxy` side of them and `docs/extension-seams.md`);
   section 6 in full (testing architecture; 6.2 rules; 6.5 CSV assertions, the chunk 6
   row); section 7 (7.5 coverage, 7.6 the landing gate item); section 8.3 (drain: you only
   reserve the `draining` input); section 9 in full (error contract, timeout budget,
   capacity table); section 10.2 items 2, 4, 6, 8, 9 and section 10.3; and the sections of
   chunks 7–13 (your consumers — each plugs into a seam you define; read what each needs).
3. What exists (all landed): the simulator and world (`internal/sim/...`), the
   observability spine (`internal/obs/...`, `scripts/csv-assert.sh`, `test/analysis/`), the
   wire sessions and the classifier (`internal/pgwire`), the vocabulary (`internal/work`),
   and change request CR-1 (value lowering and shrunk scenario files in `internal/sim`,
   `docs/knowledge/shrunk-scenarios.md`; `scripts/fuzz.sh` reporting executions per second).
   For the classifier's output read `docs/knowledge/classifier.md`: it reports a class
   (reject / start a lease / continue a lease), `SafeRead`, `Shareable`, `Hinted` and a
   share key; there is no transaction-effect flag and no separate read-only proof.
   Read every chunk's NOTES.md under `agent_work/chunks/` and everything in
   `docs/knowledge/`. The simulator API is frozen (tag `freeze-sim`); a needed change goes
   under "Simulator change requests" at the top of your NOTES.md, smallest possible.
4. The probes: `agent_work/phase1/probes/pgwire/` and `pgtls/` (the pgtls relay is the
   reference for the shell's exact 8-byte read and the two TLS legs).
5. `docs/knowledge/foundation-gates.md`.
6. For the reset: `agent_work/chunks/c05-classifier/OPTIONS.md` (option A: the rules that
   make an unconditional reset hold, and its residue) and the lease experiment's report
   `agent_work/chunks/c05-classifier/lease-exp/REPORT.md`. The experiment's pool core is on
   the local branch `exp/lease-lab` (`git show exp/lease-lab:internal/lab/leasepool/pool.go`,
   and `NOTES-E1.md` beside the report on that branch): a reference for how reset replies
   were judged there (48 reply cases). Read it for the cases; do not copy lab code into
   the product.

## Purpose

A client connects, runs statements and transactions, and gets correct answers through the
proxy — in the simulator AND against a real Postgres 17 with the real binary. Deliberately
plain: a small FIXED set of database connections and ONE bounded first-come queue. But
with every seam the later chunks plug into, so that pool, admission, fairness, coalescer,
limit and retry each replace a pass-through without re-plumbing.

## Deliverables

### `internal/proxy` — the engine (decision D1: one single-threaded, sans-I/O state machine)

- **Events in**: client connection accepted / bytes from a client / client closed / client
  writable; database connection opened / dial failed / bytes from the database / database
  closed / database writable; TLS handshake result (either leg); a deadline reached; a
  tick for publishing; `draining` set. **Actions out**: write bytes to a client or database
  connection; open a database connection; close a connection (with reason); start TLS on a
  connection; arm/cancel a timer at time T; pause/resume reading a connection (back
  pressure). The engine takes `now` as an argument and never reads a clock, starts a
  goroutine or touches a socket. It passes the determinism lint. The same engine value is
  driven by the simulator's event loop and by the real shell.
- **The transport seam** (PLAN.md 4.2): a `Dialer`-shaped byte-stream boundary with NO
  Postgres knowledge, with two implementations: simnet (in the harness) and TCP/TLS (in
  the shell).
- **Stage seams**, each a small interface with a pass-through implementation in this
  chunk, called in the fixed fail-fast order of PLAN.md 10.1: parse and **classify** →
  reject list → **draining** → **breaker** (pass-through: always closed) → **rate gate**
  (pass-through) → **coalesce** join (pass-through: never joins) → **queue** (this chunk:
  one bounded FIFO, keyed by tenant in its API even though it is one queue now) → **lease**
  (this chunk: a fixed set of N database connections opened at start and re-opened on
  loss, at most one dial at a time) → **forward** → **release**. Each refusal has its own
  error-contract row and counter. Document each seam's interface and contract in NOTES.md —
  chunks 7–12 are written against it.
- **The transaction-lease lifecycle** (D3, D4): a client outside a transaction is queued;
  a connection is lent on the first statement; while the reply's status byte is `T` or `E`
  the client's next statements go straight to the held connection (continue a lease);
  release on `I`. The status byte decides; no classifier flag takes part.
- **The reset after every lease** (decision D8) — the release stage. When a lease ends
  with status `I`, the engine writes `DISCARD ALL` to that database connection as its own
  `Query` message and arms the reset deadline (1 s, timeout budget). The connection is
  free to be lent again ONLY after the engine has seen, in reply to that message, a
  `CommandComplete` with the tag `DISCARD ALL` and then `ReadyForQuery` with status `I`.
  An `ErrorResponse`, another tag, another status byte, a protocol surprise, the
  connection closing, or the deadline: the connection is closed (and re-opened by the
  fixed set's rule). `ParameterStatus` and `NoticeResponse` messages inside the reset's
  reply are consumed. Nothing from the reset's reply is ever written to a client. While
  resetting, the connection counts as in use: the queue does not dispatch to it. A late
  reply to a reset that already timed out is ignored (the connection is gone). The reset is
  unconditional — no statement, flag or client behaviour is consulted. Config key
  `lease.reset`, accepting only `always` in this chunk (chunk 7 adds `on-switch` and
  `lease.reseed` on this same code point: write the release stage so that "what to do at
  release" is one small policy decision with the actions *reset*, *lend as is* and
  *close*). There is NO config value that turns the reset off; a test-only switch
  (unreachable from config) provides the no-reset arm for the ablation run.
- **One request per client inside the proxy** (D5): the engine asks the shell to stop
  reading a client until its reply has been written; the reply streams through a bounded
  per-client buffer; when it fills the engine pauses reading that database connection;
  a client stuck past the write deadline is dropped and its database connection goes
  through the dirty rule.
- **Reply relay**: database bytes are relayed while still arriving, message-aligned via
  the cycle tracker, never buffering a whole reply; the proxy's own `BackendKeyData` and
  parameter set are what the client saw at startup; a `ParameterStatus` arriving during a
  client's turn is relayed with the rest of its reply and counted — it no longer marks
  anything (the reset ends every lease, D8).
- **The dirty-connection rule in its simplest form: close on doubt** (D7). A database
  connection is reused only when last status is `I`, no unread bytes, and the reset after
  its last lease completed. Client vanished mid-statement or mid-transaction → the database connection is
  closed (the full cancel/rollback/reuse table is chunk 7).
- **The "reached the database?" fact** recorded for every failure, following the mining
  note's rule G8 (`docs/knowledge/release-it-pooled-connections.md`): it is "No" ONLY when
  no write was attempted for this request, or the first write was refused with zero bytes
  accepted; everything else is "may have reached". The retry seam (pass-through policy:
  off) receives this fact.
- **Every error-contract row that can occur in this chunk** produced through chunk 5's
  reply builders with the correct end marker and status byte: reject list and
  extended-protocol messages (`0A000`, with the discard-until-Sync rule), queue full
  (`53M01`), draining (`53M05`, reserved input — a unit test only), database connection
  lost after a statement the classifier marked `SafeRead`, outside a transaction (`08006`)
  / after any other statement, including `COMMIT` (`08007`) / between statements inside a transaction (`FATAL 08006`) / reply
  already partly sent (client connection closed — never a short reply that looks complete),
  statement deadline, plaintext startup under `require` (`FATAL 28000`), TLS handshake
  failure (closed), idle-in-transaction is chunk 7. Absorb chunk 4's local `fatal*.go`
  helper into chunk 5's error table (one table).
- **Timers from the timeout budget** (PLAN.md 9.2) that apply here: client startup, client
  mid-message read, client idle between transactions, database connect (including TLS and
  authentication), database statement (send to end of reply), reset after a lease, client
  write stalled.
- **Engine `Snapshot()`** and registration with the collector; the engine publishes at the
  publish interval (D12).

### `internal/shell` — the goroutine shell (real sockets)

- Listeners; per-connection reader and writer goroutines; **bounded mailboxes** into the
  engine (named caps; the bounds lint must pass); deadlines on every socket operation;
  the single engine goroutine that owns the engine value and executes its actions.
- **TLS on both legs** (PLAN.md 3.1): the pre-startup request is read as **exactly 8 bytes
  with nothing buffered across the upgrade** (unbuffered read; prove it with the smuggled
  bytes test); `crypto/tls` server side for clients (TLS 1.2 minimum, the configured
  certificate), client side for the database per `backend_tls.mode` (`disable`, `require`
  = encrypt without verifying, `verify-full` = verify against `backend_tls.ca_file` and
  the server name); the handshake runs on the connection's goroutine inside the startup /
  connect deadline and its result is posted to the engine as an event.
- The **admin listener** on its own port, served from its own goroutines from the last
  published snapshot only (D12): `/healthz` (three-state body), `/stats` (JSON),
  `/metrics` (Prometheus text). It can never wait on the engine, a queue or the pool.
- The CSV flush adapter running in real-process mode when `obs.csv.dir` is set.
- Clean teardown: no goroutine left after any path (listener close, client gone, database
  gone, deadline, shutdown).

### `internal/config`, `cmd/mini-gateway`

- `internal/config`: the config file (a simple, documented format parsed with the standard
  library — decide and justify: JSON is acceptable), defaults, **validation that fails
  closed**: timeouts nest (PLAN.md 9.2: an inner wait ends before the outer one), caps are
  positive and consistent (the gates in front of the connections sum to something they can
  serve), the `client_tls` block (`mode`, `cert_file`, `key_file` — a mode other than `off`
  without a readable certificate and key is an error) and `backend_tls` block (`mode`,
  `ca_file`), the tenant table (user → SCRAM verifier; a helper command or flag to derive
  a verifier from a password for the example config), the backend role and password, the
  `obs` block, and the **fleet block** (parsed and validated; division is used from chunk
  7). A config fingerprint for the snapshot.
- `cmd/mini-gateway`: flags, load config, start shell, exit codes; SIGTERM handled as a
  plain stop for now (drain is chunk 13).
- `cmd/loadgen` is NOT in this chunk any more (it moved to chunk 6a). Tier R drives the
  real binary with real-socket clients built on `internal/testclient`.
- The `lease` block: `lease.reset` (only `always` is valid now; any other value fails
  validation with a message naming the key).

### `test/e2e` — the scenario harness for both tiers

- A scenario is written ONCE against the driver interface (from chunk 2) and runs in
  **tier S** (the engine on the simulator loop with fakepg, simulated clients, simnet) and
  **tier R** (the real `mini-gateway` binary + a real Postgres 17 container + real-socket
  clients). `make chaos` runs the named scenarios (tier S, then tier R when Docker is
  present) and is **concise by default**: one line per scenario (seeds run, pass or fail,
  the run directory); `VERBOSE=1` prints the raw test output. Delete its not-built marker. Tier S is exact; tier R uses thresholds. Extend `scripts/tier-r.sh` and the
  Docker setup (Postgres 17 with `ssl=on` and SCRAM; a test CA generated at test time with
  the standard library; container names prefixed `mg-`, always removed).
- **CSV flush in the harness**: from this chunk EVERY scenario in BOTH tiers flushes and
  runs the round-trip check (PLAN.md 6.5); in tier S two runs with one seed produce
  byte-identical files; the real binary runs with `obs.csv.dir` set and tier R numbers are
  read from the files with no metrics server. Run directories go under `$RUN_DIR` so the
  gate's G7 finds them.
- **Generated contract documents** (held by tests, wired into `scripts/gen-docs.sh`):
  `docs/timeout-budget.md` (every wait: where, initial value, config key, what ends it;
  with the nesting checked) and `docs/capacity-table.md` (one row per bounded thing: what it
  protects, capacity and config key, what happens when full, its deadline, its gauge —
  initial rows per PLAN.md 9.3 that exist now). `docs/extension-seams.md` (operational
  facts only: what a second protocol must supply, which engine code is Postgres-shaped —
  the prose seat will polish it; write it as a plain factual table).
- The **fault × path matrix**, first rows (`test/e2e` data + a generated or hand-kept table
  in NOTES.md): fault, where in the conversation, expected error-contract row, elapsed
  budget, "reached the database?".

## Tests (write these first)

End-to-end, **both tiers** unless noted; each with replay (tier S), quiesce, recovery,
non-zero counters, and the CSV round trip:

1. **Results through the proxy equal results direct** (same workload, same seed): every
   client-visible result equal to the chunk 2 direct baseline's.
2. **Transaction integrity** — the core safety property of transaction pooling: with many
   clients running interleaved multi-statement transactions over few connections, no
   statement of one client ever runs on a connection inside another client's transaction.
   Checked exactly from the fake's statement log in tier S; in tier R with a per-client
   marker written inside each transaction and verified from the table.
3. **Fragmentation**: one-byte delivery on both sides changes nothing (tier S: identical
   client-visible results; tier R: a test client that writes byte by byte).
4. **Reject list end-to-end** (a courtesy since ruling 9 — test what the list documents,
   do not hunt spellings): every rejected feature gets `0A000`, the fake's statement
   counter does not move, the client connection stays usable; an extended-protocol client
   gets `0A000` and is not hung (discard-until-Sync).
4b. **Session state never crosses clients** — the property the reset exists for. Many
   clients of several tenants over few connections; some change session state in every
   way the world can express, others observe. Tier S: session-changing statements must
   actually reach the fake, so run this scenario with the courtesy check bypassed by a
   test-only switch (the fake models `SET`, `RESET`, `SHOW` and `DISCARD ALL`); assert
   exactly, from the fake's own per-backend record, that every lease starts on a backend
   whose settings equal the defaults, and that no observer ever reads a value another
   client set. Tier R, with the courtesy check ON: the hostile clients change state through
   what the list cannot see — a function that hides a `SET`, one that creates a temporary
   table, one that takes a session advisory lock, one that runs `PREPARE`, one that
   `LISTEN`s (create them in the test schema) — and after every lease a checker connection
   path runs the catalog oracle `dirty-check.sql` (shipped under `test/` by chunk 5) on the
   same backend through the proxy's next lease; zero dirty rows across the run. Counters
   must move: resets completed equals leases ended clean. **Ablation**: with the test-only
   no-reset switch the same scenario must FAIL in both tiers (state crosses). Also here:
   reset replies — an error, a wrong tag, status `T`, no reply until the deadline, the
   connection dropping mid-reset, each injected by the fake — close the connection, never
   lend it, and a late reply after the deadline is ignored (tier S, one case each).
5. **Client vanishes mid-statement and mid-transaction**: the database connection is not
   reused dirty (the next client on any connection never sees the vanished client's
   transaction or session state), connections return to the fixed set, quiesce holds.
6. **Database killed mid-statement** (and reset before / during / after a reply, and
   mid-transaction): the client gets exactly the error-contract row the matrix predicts,
   within the budget; never a short reply that looks complete; recovery after restart.
7. **Slow reader**: a client that stops reading a large reply — bounded memory (relay
   buffer high-water ≤ its cap), the proxy stops reading the database, and at the write
   deadline the client is dropped and the connection closed.
8. **Queue full**: more concurrent clients than the fixed connections plus the queue →
   `53M01` with the connection usable; nothing reached the database for the refused ones.
9. **Accept-then-silence on the database side** at startup and after a query: the connect
   deadline and the statement deadline fire on the virtual clock; the right rows.

Planted-bug canary + ablation (tier S): a test-only switch "release the connection on
ReadyForQuery regardless of status byte" must make scenario 2 FAIL. A second canary: "lend
the connection as soon as `DISCARD ALL` is written, without waiting for its reply" must
make scenario 4b or the operation fuzz target FAIL.

**Engine operation fuzzing** (PLAN.md 6.1 and 6.6; Nick's DST fuzz ruling — binding):
`FuzzEngineOps` in tier S. The input decodes to a sequence of ordinary operations mixed
with faults: a client connects; sends a statement of each class (a safe read, a write,
`BEGIN`, `COMMIT`, `ROLLBACK`, a rejected one, an extended-protocol message); cancels;
disconnects at any point; stops reading; a deadline fires (advance virtual time); the
database replies in arbitrary fragments, stalls, resets, answers a reset wrongly. After
every operation every registered invariant holds, and after the sequence the world
quiesces. Rules: the simulator seed is a FIXED value or is read from a dedicated prefix
of the input — never a hash of the input; the decoding is total (every byte string is a
valid, bounded operation list) and local (a one-byte change alters one operation); the
target must run above the rate floor of 100 executions per second in `scripts/fuzz.sh`'s
table on vm-1 (keep worlds small: a few clients, two connections) — if it cannot, say so
in NOTES.md and it is reported as a seed sweep, not as fuzzing. Its seed corpus runs as
plain tests in CI. Show it works: a test-only switch plants an ordering bug (for example:
a cancel that arrives while the client's request is still queued leaves the queue slot
taken); the target finds it within the gate's 20 s budget, the failing input is minimised
with CR-1's helpers (delete operations, lower values) and committed as a readable shrunk
scenario file that replays as a plain test — failing with the bug planted, passing
without. Found real bugs are fixed and kept the same way: as a scenario file, not as a
pinned seed alone.

**Shell tests** under `testing/synctest` and `-race` (add `internal/shell` to
`tools/lint-rules/race-packages.txt` and `shell-packages.txt`): no goroutine left after
any teardown path; mailboxes never exceed their cap; a client that stalls mid-handshake is
closed at the startup deadline with no goroutine left; a stuck writer is bounded by its
deadline; the admin listener answers while the engine goroutine is blocked on purpose.

**Tier R smoke**: `psql` and `pgbench -S -M simple` through the real binary.

**TLS, tier R and shell tests** (real `crypto/tls`, test CA generated at test time):
`psql` with `sslmode=require` and `verify-full` through the proxy; `sslmode=disable`
refused with `28000` under `require`; the database leg under each `backend_tls.mode`
against a Postgres with `ssl=on`, checked in `pg_stat_ssl`, with a wrong CA giving a
connect failure of its own class; plaintext bytes smuggled in with the TLS request close
the connection; a client that stalls mid-handshake is closed at the startup deadline; a
cancel request after a TLS upgrade is accepted (recognised; mapping is chunk 7); **the
whole tier R suite of this chunk runs with client TLS on**.

**Config tests**: defaults; every validation failure (bad nesting, missing certificate,
bad fleet numbers) fails closed with a message naming the key.

**SQL assertions** under `test/analysis/<scenario>/` run by `scripts/csv-assert.sh` on
both tiers' files: conservation per instance from exported counters (accepted = completed
+ failed + shed, with queued and in flight 0 in the final flush); proxy completed =
workload completed (two components, one query); error-contract counts by row = faults
injected; resets completed + connections closed at release = leases ended, and for
scenario 4b the session-state crossings counted by the fake are 0 (and not 0 in the
ablation arm's files).

## Observability obligations (landing gate — PLAN.md 7.6)

Engine `Snapshot()`: clients connected, in startup, in transaction; requests by stage and
outcome (accepted, completed, failed by error-contract row, rejected, shed); the
statement-time histogram (database send to end of reply); queue depth and wait histogram
for the plain queue; relay buffer high-water; resets started, completed and failed by
reason (error, wrong tag, wrong status, deadline, connection lost), connections currently
resetting, and the reset-time histogram; `ParameterStatus` messages seen during a client's
turn; client connections by TLS version; database
connections open / connecting / lent; mailbox depth (shell). Mapped in every existing
adapter with rows in the CSV registry (`docs/csv-schema.md` regenerated). Conservation
asserted exactly in tier S: accepted = completed + failed + shed + queued + in flight.
**Health** (three-state): `ok`; `degraded` when the database is unreachable (no usable
connection and dials failing); `failed` only when the instance cannot work at all (config
unusable, listener down). The wire and classifier snapshots from chunks 4 and 5 are
registered too.

## Claims this chunk makes (an adversary will try to break each)

1. Through the proxy, every client-visible result equals the direct result, under any
   fragmentation on either side.
2. No statement of one client ever runs inside another client's transaction; a connection
   is lent to at most one client at a time and released only at status `I`.
3. A database connection is lent again only after a completed reset: `DISCARD ALL`
   written as its own message after status `I`, its tag and status `I` seen. A connection
   whose lease ended any other way (status not `I`, unread bytes, client vanished
   mid-statement or mid-transaction) or whose reset did not complete exactly so, is closed
   and never lent again. No client input can prevent or skip the reset.
3b. Session state never crosses clients: no client observes a setting, temporary table,
   prepared statement, listener or session advisory lock left by another client's lease —
   within the stated residue of `DISCARD ALL` (PLAN.md D8: the random seed, the existence
   of a custom setting name, extension and procedural-language memory, replication origin).
4. Every proxy-made refusal or failure is the documented error-contract row, with a
   correct end marker; a client never receives a truncated reply that looks complete.
5. "Reached the database: No" is claimed only when no byte of the request was accepted by
   a database connection.
6. Bounded memory: one stuck or slow client cannot make the proxy hold more than the
   per-client buffer cap; every mailbox, queue and buffer has a named cap that holds.
7. Every wait has a deadline and the documented nesting holds; nothing hangs: an
   accept-then-silence peer on either side at any point of the conversation ends at the
   documented deadline with the documented result.
8. No goroutine leaks on any teardown path; health and stats answer while the data path is
   blocked.
9. TLS: nothing buffered across the upgrade; `require` never serves a plaintext client;
   `verify-full` never connects to a database with an unverifiable certificate.
10. Every statement the reject list documents is refused end to end and never reaches the
    database; extended-protocol clients are refused cleanly. (The list is a courtesy: a
    session feature in a spelling the list does not document being forwarded is not a
    break of this claim — claim 3b covers what happens to it.)
14. The operation fuzz target takes no seed from a hash of its input, decodes every byte
    string to a bounded operation list, and finds the planted ordering bug within the
    gate's budget.
11. The numbers flushed to CSV equal the in-memory snapshot (round trip), tier S files are
    byte-identical per seed, and conservation holds from the files alone.
12. The engine is deterministic and sans-I/O (lint), and the replay law holds for every
    scenario.
13. Config validation fails closed on every invalid setting it documents.

## Steady-state row

What grows: per-client state, relay buffers, the queue, timers, connections in the
resetting state. What removes it: client close, reply completion, dequeue or shed, timer
fire or cancel, the reset's reply or its deadline. Gauges: clients connected,
buffer high-water, queue depth, pending timers. Quiesce: all zero after every scenario.

## Size and the split rule

About 2,350 lines of product code (about 250 of them TLS, config and certificates, about
100 the reset; the load generator's 250 moved to chunk 6a), and it may reach twice that. If you pass about 5,500 product lines and are not complete, apply
the SPLIT-REQUEST rule of the builder rules: finish a coherent green part — the engine
with the reset, the tier S harness and scenarios and the operation fuzz target FIRST;
then shell, config, binary, tier R — and say exactly what remains.

## Finish

As the builder rules say: gate green at the build stage (`scripts/gate.sh
c06-walking-skeleton`, including tier R and G7), NOTES.md complete at
`agent_work/chunks/c06-walking-skeleton/NOTES.md` — with the seam contracts, the fault ×
path matrix and the config reference — clean tree, summary, and a final line containing
exactly AGENT-DONE.

## Carried from chunk 3 (required fixes, each with a regression test)

Three minor items from chunk 3's second review (`agent_work/chunks/c03-obs-spine/REVIEW-2.md`)
were closed by carrying them here, because this chunk is the next to change the collector:
1. `Registry.Declare` must be all-or-nothing: a refused call leaves none of its descriptors
   registered (`internal/obs/metric.go`).
2. `csvflush` writer: a zero-flush `Close` whose `open` fails must count a flush error like
   the neighbouring error paths.
3. `Collector.selfView` must not share the histogram slice with the last published snapshot.
Also from chunk 3's adversarial record: add a test that every source the real binary
registers with the collector is listed in the catalog (`internal/obs/catalog`), so every
exported metric is documented in `docs/csv-schema.md`.

## Carried from chunk 4 (required fixes, each with a regression test)

From `agent_work/chunks/c04-wire-sessions/REVIEW-2.md` and `ADVERSARIAL.md`:
1. `NewTenantTable` must refuse a verifier whose salt is longer than the mock-challenge cap
   (`maxMockSaltLen`), so the unknown-user challenge always has the table's shape.
2. Remove the stale skip entry for `Database.TLSFailed[HandshakeViolation]` in
   `TestWireCountersMoveAndConserve` so the counter is asserted.
3. Config validation: a tenant table that mixes SCRAM iteration counts or salt lengths is
   refused (narrowed claim 5 of chunk 4 then holds for every valid config).
4. The key-data issuer in the shell is seeded from `crypto/rand`, one issuer per instance
   (narrowed claim 9).
5. From the scoped review of chunk 4's integration (`REVIEW-3.md`): make
   `TestOneRawExportImportRule` (`internal/obs/simrun/imports_test.go`) mirror both
   directions importlint enforces for `internal/pgwire` and `internal/obs/wiresrc`; and add
   every metric a `test/analysis/wire-*/*.sql` file queries to its `-- requires:` header
   (five are missing), so an absent metric fails the runner instead of comparing 0 with 0.

## Promotion (PLAN.md 12.2, step 9)

Check scripts and harness helpers you write that are worth keeping go under `scripts/` or
`test/`, not under `agent_work/`. `agent_work/chunks/c06-walking-skeleton/` holds notes
and records only.


## Addendum from the chunk 5 and CR-1 landings (2026-11-26) — binding

1. **Chunk 5's final names and limits.** Read `agent_work/chunks/c05-classifier/NOTES.md`
   ("Narrowed claims", "Stated limits") and `ADVERSARIAL.md` before using the classifier.
   Sharing is not built here (chunk 10), but `SafeRead` decides `08006` against `08007`
   in this chunk: the stated limits of the safe shape (tables with row-level security
   policies are outside it) are limits of that decision too — say so in
   `docs/error-contract.md`'s source text if you touch it, and in NOTES.md.
2. **Real-server tests share one Postgres cluster** and `scripts/tier-r.sh` runs the
   packages one at a time (`go test -p 1`). Server-wide views (`pg_stat_statements`,
   `pg_stat_activity`) see every database: a tier R test that counts through them must
   filter by its own database or connection, and must not assume it is alone.
3. **Bound the cost of one fuzz execution.** CR-1 found a fuzz input that passed but took
   13 seconds, which made the gate's fuzz step fail at random. `FuzzEngineOps` must cap
   whatever its input decodes into (operation count, sizes, virtual time) so that any
   input runs in well under a second, with a test that says so
   (see `TestFuzzWorldScheduleCostIsBounded`).
4. **The fuzz inventory is pinned** in `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`
   (14 targets). Adding `FuzzEngineOps` makes two tests there fail until the orchestrator
   reviews the new target and re-pins the number; you may not edit that directory. List
   every fuzz target you add in NOTES.md with its seed source, and expect those two tests
   (`TestAdv_6_GateSeedSweepCounts`, `TestAdv_7_InventoryAndHiddenHashImports`) to be the
   only red ones in the adversarial packages.
5. Go's fuzz tool sometimes ends a target with `context deadline exceeded` and no failing
   input (three times on 2026-11-26, three different targets). `fuzz.sh` fails closed on
   it. If your gate run is red only for that, re-run the gate once and say so in NOTES.md;
   do not change `fuzz.sh`.
