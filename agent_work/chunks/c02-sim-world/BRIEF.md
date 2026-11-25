# mini-gateway — chunk 2: Simulated world (Commit 0, part 2) — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c02-sim-world`.
Worktree: `/root/Software/mini-gateway-work/c02` — branch `work/c02-sim-world`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — the rules for every builder. They bind you.
2. `PLAN.md`: "Chunk 2 — Simulated world" (your scope), section 10.4 (the fault catalog —
   every database-side and client-side fault in it is yours), section 6.2 (testing rules),
   section 4.1 (what Postgres really does — your fake must match it), section 7.7 (the
   component names `fakepg`, `workload`, `simnet` that your snapshots will be exported
   under later), and chunks 3–7 and 11 (your consumers: read them so your API serves them;
   build none of them).
3. Chunk 1 as landed: `agent_work/chunks/c01-sim-kernel/NOTES.md`,
   `docs/knowledge/sim-kernel-*.md`, and the code of `internal/sim`, `internal/sim/simnet`,
   `internal/sim/simtest`, `internal/obs`. You build ON this API. If you need a change to
   it, make the smallest one, and list it under "Kernel changes" in your NOTES.md (after
   this chunk lands the simulator API is frozen: tag `freeze-sim`).
4. The Postgres probe and its recorded output — the ground truth for fidelity:
   `agent_work/phase1/probes/pgwire/main.go` and `output-2026-11-24.txt`; and
   `docs/knowledge/phase1-findings.md` (pgproto3 facts: use message `Decode`/`Encode` with
   your own framer, not `pgproto3.Frontend`/`Backend`).
5. `docs/knowledge/foundation-gates.md`.

## Purpose

The things the proxy will stand between: a fake Postgres that can misbehave in every way in
the fault catalog, and simulated clients that generate realistic and hostile load. Plus the
"no proxy" baseline every later comparison needs. No proxy code in this chunk.

## Deliverables

### `internal/sim/fakepg` — a fake Postgres on simnet

- Speaks the simple-query subset over simnet using `github.com/jackc/pgx/v5/pgproto3`
  message types and its own small incremental framer (it must cope with one-byte reads).
  **This is where the one external dependency enters `go.mod`** (pin the version the probe
  used, v5.13.0, unless a newer v5 is already in the module cache; `go mod tidy`; the import
  rules already allow `pgproto3` in `internal/sim/fakepg/...`).
- Startup: an `SSLRequest` is answered `N` (configurable to `S`-then-simulated-handshake
  outcome is NOT needed here; chunk 4 adds TLS events), a GSS request `N`; startup message →
  **trust authentication** → the parameter-status set real Postgres 17 sends (take the 14
  from the probe output) → `BackendKeyData` → `ReadyForQuery('I')`. SCRAM is added in chunk
  4. Wrong database / unknown user answers with the real FATAL codes when configured.
- A **`max_connections` limit** with Postgres's real refusal (`FATAL 53300`, "sorry, too
  many clients already") — the connection is accepted, refused in-protocol, and closed.
- **Transaction status tracking** exactly as the probe shows: `BEGIN`/`COMMIT`/`ROLLBACK`
  (and `START TRANSACTION`, `END`, `ABORT`), status bytes `I`/`T`/`E`, an error inside a
  transaction → `E` until rollback, multi-statement `Query` messages (implicit transaction
  semantics: an error aborts the rest of the message; `BEGIN; SELECT 1` leaves `T`), an
  empty query → `EmptyQueryResponse`.
- A **small keyed table** so reads and writes have checkable results: a minimal statement
  set recognised by a tiny hand-written matcher (not a SQL parser), documented exactly in
  NOTES.md — at least: `SELECT v FROM kv WHERE k = '<key>'`, `SELECT 1`, `SELECT
  pg_sleep(<seconds>)`-style service-time statements, `UPDATE kv SET v = '<v>' WHERE k =
  '<k>'`, `INSERT INTO kv ...`, `DELETE`, `SET <name> = <value>` / `RESET` (session state
  that persists on the connection and triggers the unsolicited `ParameterStatus` for
  reported settings, per probe B6), `DISCARD ALL`, a statement producing N rows of a given
  size (for the large-reply faults), and an unknown statement → a real-looking
  `ERROR 42601`. Writes inside a transaction apply at `COMMIT` and vanish at `ROLLBACK`;
  a read that started before a write committed returns the old value (statement snapshot at
  start, per probe B10) — this matters for chunk 11.
- A **write log** for "was it applied?" assertions: every applied write with its
  connection id, client-visible statement text, virtual time and sequence, plus per
  statement: which connection ran it, inside which transaction — so later chunks can prove
  "no statement of one client ran inside another client's transaction" and "no write
  applied twice" from the log.
- **Service-time models** on the virtual clock: fixed; a seeded integer distribution; per
  key; per statement kind; and **stall windows** (all statements starting or in service
  during [t1, t2) take an extra d — the spec's 150–200 ms backend stall). A concurrency
  model: each connection serves one statement at a time; optional global capacity (at most
  C statements in service; beyond it they queue inside the database, which is how a real
  database slows under load) — document the model.
- **Every database-side fault in PLAN.md section 10.4**, each injectable through the
  kernel's fault schedule, clearable, recorded in the trace and counted: refuses
  connections; accepts and never answers (at startup; after a query); connect stalls; slow
  first byte; slow trickle of rows; fragmented delivery down to one byte including inside a
  message header (through simnet chunking); reset before, during and after a reply and
  mid-transaction; garbage bytes and impossible lengths; a non-Postgres reply; a reply that
  ends without its end marker (`FATAL 57P01` then end of stream — probe B9); very large
  rows and very many rows; unexpected state at checkout; unexpected `ParameterStatus`;
  killed (all connections reset at once); frozen (sockets open, nothing moves); restarted
  cold (all connections gone, new ones refused for a window, then accepted, table state
  kept); `max_connections` reached; latency sitting exactly at a timeout boundary
  (parameterised); some connections slow and others fine; half-open; asymmetric latency.
  (The two TLS faults of the catalog belong to chunk 4.)
- **Cancel handling**: a `CancelRequest` on a new connection with a matching key data
  cancels the statement in service on that backend (`ERROR 57014`, then
  `ReadyForQuery`), does nothing if idle, and is ignored with a wrong key — as Postgres does.
- `Snapshot()`: connections open and peak, accepted, refused by reason; statements by kind;
  statements in service and peak; writes applied; transactions begun/committed/rolled
  back; cancels received/applied; bytes; faults injected by kind.
- Quiesce registration: no connection open, no statement in service.

### `internal/sim/workload` — simulated clients

- Client actors that speak the client side of the simple-query protocol over simnet (their
  own small client state machine with `pgproto3` types — this is the "test client"; put its
  package path into `tools/lint-rules/imports.txt` in place of the placeholder).
- **Tenants**: each client belongs to a tenant (its startup `user`); per-tenant workload
  mixes.
- **Open-loop arrivals** — constant-rate and Poisson (integer arithmetic for the kernel
  side; the generator may use `math/rand/v2` distributions and floats only inside an
  allow-listed, clearly bounded generator file, per PLAN.md section 5.3) — with
  **intended-send-time bookkeeping**: every request has an intended send time; latency is
  measured from it; a client that is busy does not delay the next intended send (requests
  queue in the client and their wait counts as latency). Also a closed-loop mode with think
  time (needed for the reconnection storm).
- **Key choice**: uniform and Zipf with a stated exponent; the share of the hottest key is
  asserted against the calculation in Phase 0 (find it under `agent_work/phase0/`; if the
  expected share is not there, compute it exactly in the test from the definition).
- **Transactions** of several statements with think time between statements; read/write
  mixes; identical-read bursts (N clients sending the same statement at the same instant).
- **Every client-side fault in section 10.4** (except the TLS ones, chunk 4): sends half a
  message and stops; stops reading replies; disconnects while queued, mid-statement,
  mid-transaction (and, later, while a coalesced waiter — expose the hook: disconnect at a
  chosen point of the conversation); reconnects in a burst (N clients drop and reconnect
  within a window); retries aggressively (a refused or failed client schedules its own
  retry as an event, with a configurable policy and cap); sends a cancel. Load: open-loop
  arrival above capacity.
- Per-request outcome classification by SQLSTATE class (completed, error by code,
  connection lost, timed out by the client's own deadline).
- `Snapshot()`: intended, sent, completed by outcome, per tenant; latency histograms from
  intended send time (the `internal/obs` integer histogram); retries; reconnects;
  connections opened/failed.

### Scenarios, driver, CLI, fidelity

- A **scenario type**: name, seed, world setup (fake config, workload config, fault
  schedule), duration, assertions; registered in a list; `cmd/mgsim run <scenario> -seed N`
  runs any of them and prints digests, a short result table and the REPLAY line.
- The **scenario driver interface** that tier R will also implement later (chunk 6): the
  small set of operations a scenario needs from "the system under test and its world"
  (start, connect clients at an address, inject fault, read snapshots, stop). In this
  chunk it has one implementation (simulator, clients straight at the fake). Keep it small
  and protocol-level so a real binary plus real Postgres can implement it.
- The **fidelity script runner**: a scripted conversation (a list of client messages and
  expectations on message types and status bytes, ignoring values that legitimately differ
  such as process ids, timings, and server version text) run against the fake in the
  simulator and against a real Postgres 17 over TCP. First script: the probe's ten
  exchanges (B0–B10 as applicable to the simple-query subset with trust auth: B0 uses
  SCRAM against real Postgres — compare from `ReadyForQuery` onward and compare the
  parameter-status NAMES). `scripts/tier-r.sh`: build it (delete its marker): starts a
  throwaway `postgres:17` container bound to 127.0.0.1 on a free port (name prefix
  `mg-tier-r-`, always removed on exit, also on failure), runs the tier R tests (build tag
  or env gate `MG_TIER_R=1` so `go test ./...` in CI never needs Docker), prints a summary.
  **Recorded transcripts replayed in CI**: the real-Postgres side of the fidelity script is
  recorded to testdata by the tier R run (`-update`) and CI compares the fake against the
  recording.

## Tests (write these first)

- **Fidelity**: the ten probe exchanges match between the fake and Postgres 17 — message
  types and status bytes — live in tier R, and against recorded transcripts in CI.
- **The fake under each fault** behaves as specified in NOTES.md: one named test per fault
  kind, asserting what the client side observes, in what order, and that the fault counter
  moved; and for each, the cleared-fault recovery (a fresh exchange succeeds afterwards).
- Transaction semantics table-test: status byte after each of a list of message sequences
  (from the probe).
- Statement snapshot: a read in service does not see a write committed after it started.
- Write log: every applied write appears exactly once; rolled-back writes never appear.
- `max_connections`: connection N+1 gets `53300` and the open count never exceeds N.
- Cancel: right key cancels the running statement only; wrong key does nothing.
- **Workload generators**: achieved arrivals = intended (exact count over a window);
  latency is measured from intended send time (a stalled server shows the stall in the
  latency of requests that were due during it — the coordinated-omission test); Zipf
  shares match the calculation within a stated exact bound for a fixed seed; Poisson
  inter-arrival mean within a stated bound.
- Each client fault produces what NOTES.md says on the server side.
- Property test: for random fault schedules (`ScheduleFromBytes`) on a small world, the
  simulation never panics, always quiesces after faults clear and clients finish, and
  replays exactly. Fuzz target for the fake's framer and statement matcher (no panic,
  bounded memory) and for the workload client's reply parser.
- **End-to-end for this chunk — the direct baseline (no proxy)**, each a named scenario
  with replay check, quiesce check and non-zero counters, in tier S; and the first three
  also as tier R tests against real Postgres with a real-socket test client where
  practical (state exactly what tier R covers in NOTES.md):
  1. **500 clients open 500 database connections** (peak connections on the fake = 500).
  2. **A reconnection burst past `max_connections` is refused by the database**: with
     `max_connections` = 100 and 500 clients dropping and reconnecting within 50 ms, the
     fake refuses with `53300`; count refusals.
  3. **1,000 identical reads cost 1,000 database reads.**
  4. Open-loop overload straight at the fake: latency from intended send time grows with
     the backlog (the uncontrolled queue the proxy will later bound).
  These are the control arms for chunks 7, 8 and 11: record their exact numbers per pinned
  seed in NOTES.md.
- Planted-bug canary: a test-only switch (for example: the fake applies a write twice, or
  reports status `I` inside a transaction) that a named test must catch.

## Observability obligations (landing gate)

`fakepg.Snapshot()` and `workload.Snapshot()` as listed, each with a test that the counters
move and that conservation holds (workload: intended = sent + not-yet-sent; sent =
completed + failed + in flight. fakepg: accepted = open + closed; statements started =
finished + in service + aborted by fault). Byte-identical snapshots for one seed.

## Claims this chunk makes (an adversary will try to break each)

1. For the simple-query subset, the fake's message types and status bytes match Postgres 17
   on the fidelity script (the ten probe exchanges).
2. Transaction status and statement-snapshot semantics match the probe: status after every
   sequence; a read in service never sees a later commit; rolled-back writes are never
   applied.
3. The write log is exact: every applied write once; none missing; none duplicated.
4. `max_connections` is a hard bound on the fake and the refusal is the real one.
5. Every database-side fault of section 10.4 (non-TLS) is injectable, does what NOTES.md
   defines, is counted, is recorded in the trace, and clearing it restores normal service
   for new exchanges.
6. Every client-side fault of section 10.4 (non-TLS) does what NOTES.md defines.
7. Open-loop generation: the number of requests sent in a window equals the intended
   number regardless of server speed, and latency is measured from intended send time.
8. Cancel affects exactly the targeted backend's running statement.
9. The replay law holds for every baseline scenario and for random fault schedules; the
   world always quiesces after faults clear.
10. The baseline numbers: 500 clients → 500 connections; burst past the limit → refusals
    by the database; 1,000 identical reads → 1,000 database reads.
11. `pgproto3` is imported only in `internal/sim/fakepg/...` and the test-client package;
    `go.mod` has exactly one requirement.

## Rules files to update

`tools/lint-rules/det-packages.txt` (your packages; the float-using generator file is
allow-listed in `detlint-allow.txt` with a justification, or isolated so the rest stays
checked), `tools/lint-rules/imports.txt` (the test-client package path), markers for
`scripts/tier-r.sh`.

## Steady-state row

What grows here: the fake's connection table, the write log, the statement log, client
queues of not-yet-sent requests, per-tenant stats. What removes or bounds it: close;
named caps on the logs for long runs (a ring or a count-only mode, selectable) with a
counter of dropped log entries; a cap on a client's backlog with a "could not send"
counter. Which gauge shows the level: connections open, log lengths, backlog depth.

## Size

About 1,800 lines of product code, budgeted up to twice that. Tests at least as large.

## Finish

As the builder rules say: gate green at the build stage (`scripts/gate.sh c02-sim-world`,
which will now run tier R: make sure `scripts/tier-r.sh` passes on this machine), NOTES.md
complete at `agent_work/chunks/c02-sim-world/NOTES.md`, clean tree, summary, and a final
line containing exactly AGENT-DONE.

## Note from the orchestrator on your starting point

Your branch starts from chunk 1's reviewed and repaired code (`work/c01-sim-kernel` before
its prose pass). While you build, the prose seat is adding comments to chunk 1's Go files
on another branch, and your branch will be rebased onto that. To keep the rebase clean:
prefer NEW files in your own packages; when you must change a chunk 1 file (a "kernel
change"), keep the edit as small and local as possible and list it in NOTES.md.
`scripts/gate.sh` at the build stage compares comments with the merge base with `main`,
which does not contain chunk 1 yet, so chunk 1's (comment-free) files are part of your
diff: that is expected and passes.
