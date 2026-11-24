# PLAN — mini-gateway (Phase 1 proposal)

Operational document, written by the Fable orchestrator on 2026-11-24 for Nick's approval.
**Status: PROPOSAL. Nothing below is authorized to be built until Nick approves it in chat.**
The diagrams that go with it are in [docs/c4/](docs/c4/README.md); both are published for review at
https://nickstrad.github.io/mini-gateway/.

Inputs, in order of authority:

1. Nick's 14 steering answers of 2026-11-24 (`/root/briefs/zgateway-steering-answers.md`). They govern.
2. The observability / horizontal-scale / *Release It!* note (`/root/briefs/zgateway-observability-scale-releaseit-note.md`) and the seat ruling (`/root/briefs/zgateway-seat-ruling.md`).
3. The spec, [docs/project-source.md](docs/project-source.md).
4. [INFLUENCES.md](INFLUENCES.md) — 100 influences and 18 conflicts. Written before the steering, with Redis as its working assumption. Where it disagrees with the steering, the steering wins; every such override is listed in section 2.

Evidence gathered in this phase (all in the repository):

| What | Where | Used in |
| --- | --- | --- |
| Probe against a real Postgres 17: startup with SCRAM, transaction status bytes, pipelining, session state, kill mid-query, statement snapshots | `agent_work/phase1/probes/pgwire/` (`main.go`, `output-2026-11-24.txt`) | sections 4, 9, chunks 4–6, 11, 12 |
| Evaluation of the object-store simulator, from its code | `agent_work/phase1/research/q8-object-store-simulator-evaluation.md` | section 5 |
| *Release It!* notes mined for the build plan | `agent_work/phase1/research/release-it-mining.md` | section 10 and the landing gate |

Contents: 1 what we build · 2 steering and overrides · 3 design decisions · 4 backend-interface verdict (steering answer 1) · 5 simulator evaluation (steering answer 8) · 6 testing architecture · 7 observability architecture · 8 horizontal scale · 9 error, timeout and capacity contracts · 10 *Release It!* mapping · 11 benchmarking · 12 build flow and landing gate · 13 the chunks · 14 scenario-to-chunk table · 15 the book · 16 risks · 17 out of scope and follow-ups · 18 open questions for Nick.

---

## 1. What we are building

**mini-gateway** is a Go proxy that speaks the Postgres wire protocol to clients and to one ordinary Postgres server. Clients connect to it as if it were Postgres. It gives the database a bounded, controlled number of connections no matter how many clients arrive, and it decides — per tenant, under overload — which requests wait, which run, and which are refused quickly with an error that says whether a retry is safe.

In one process it has these parts:

- **Wire layer.** Reads and writes Postgres messages (simple-query subset only) using the `pgproto3` package for message encoding. Handles client startup and authentication, proxy-to-database startup and authentication, tracks whether each client is inside a transaction, and classifies each statement (allowed or rejected; read-only or not; can it share a result; can it be batched).
- **Pool.** A hard-capped set of database connections. A connection is lent to a client for one transaction and returns when the database reports the transaction has ended.
- **Admission.** One bounded queue per tenant, with two selectable rules for shedding stale work (the "Fail at Scale" queue-timeout rule and the CoDel paper's control law), newest-first service while overloaded, round-robin between tenants with a fair share that binds only when the database is saturated, an adaptive limit on concurrent database work (AIMD), and an optional per-tenant rate gate (GCRA) in front.
- **Coalescer.** Identical read-only statements that arrive together share one database round trip. Writes are never shared.
- **Batcher.** When a database connection frees up and several single-statement requests are already waiting, they can be written to it in one go. No timer.
- **Retry policy.** An interface with two policies: off (the default), and "retry once, only if nothing reached the database, within a 10% budget".
- **Observability.** Every part keeps counters and gauges and returns them from `Snapshot()`. One collector reads the snapshots; separate exporters turn them into Prometheus text, JSON, CSV, a log line and (last) an OTLP push.
- **Simulator.** A deterministic, single-threaded test harness with a virtual clock, simulated byte-stream connections, a fake Postgres and simulated clients. It is built first and everything else is proved in it before it is proved against a real Postgres.

Scope fixed by the steering: Postgres only in v1; simple-query protocol only; transaction pooling; session-level features rejected rather than pinned; no cache; no replicas; no run-time coordination between proxy instances; N instances supported through a static fleet configuration.

Not in v1 (details in section 17): the extended query protocol (Parse/Bind/Execute), TLS, `COPY`, replication, connection pinning, caching, read replicas, hedging, a second backend protocol, coordinated fleet state.

---

## 2. Steering applied, and where it overrides INFLUENCES.md

| # | Nick's answer | What the plan does | Overrides in INFLUENCES.md |
| --- | --- | --- | --- |
| 1 | Postgres (Variant B); backend behind an interface if that stays honest | Postgres-first; see section 4 for the evidence and the verdict on the interface | **Overrides** recommendation 5 ("Variant A is the right MVP"), section 4 ("the list strengthens A"), conflict C3's resolution ("build the multiplexing pool for A"), and section 6's "no Postgres wire code". Entries marked "CORE if Variant B" (S2, I1) are now CORE; I27 is ADOPT WITH CHANGE. Redis-only entries (I10, I18, I20, I21, P18) become background. |
| 2 | Name: mini-gateway | Applied to STATE.md, PLAN.md, C4, the site. Phase 0 documents keep the old name. | Overrides the "keep `mini-zgateway`" recommendation. |
| 3 | Both CoDel rules, equal and selectable | Chunk 8 builds one queue with two rule implementations behind one interface, compared head-to-head in the simulator | **Overrides** conflict C1's resolution and top-line recommendation 1 (which shipped only the timeout rule) and entry F1 (was EXCLUDE; now ADOPT as one of two modes). The spec invariant is restated per mode (section 6.4). |
| 4 | Per-tenant queues, round robin, share binds under saturation | Chunk 9 | none |
| 5 | No cache | Coalescing only | none |
| 6 | Superseded by 1 | PgBouncer and the Postgres pooling sources are core inputs | Overrides "pool-management ideas and a README contrast only". |
| 7 | Apollo-lite, plus a trade-offs write-up | Section 11; the write-up is a named book chapter | Adds a deliverable the recommendation did not have. |
| 8 | Evaluate the object-store simulator first | Section 5 | Replaces the recommendation's flat "re-implement in-repo" with an evidence-based decision (which ends in nearly the same place, with named files copied). |
| 9 | Simple-query subset, transaction pooling, reject list, `pgproto3` from pgx/v5 | Chunks 4–6; the one allowed external dependency | **Overrides** the recommendation (RESP2 allow-list) and section 6's "external modules of any kind" exclusion — for the codec only. |
| 10 | No promotions | Nothing promoted. folly's rule (F3) stays what it was: a gauge and a simulator-only comparison arm. P12 stays out. | none |
| 11 | GCRA as outer gate; AIMD in v1; timer-free batching | Chunks 10 and 12. **Note:** INFLUENCES conflict C3 said a Postgres variant "has no micro-batching". The steering keeps batching, so section 3 (decision D9) defines what it honestly means for Postgres; it is flagged in open question 3. | Overrides C3's "no micro-batching in B". |
| 12 | Retry policy behind an interface, default OFF | Chunk 13 | **Overrides** the recommendation (retry on by default). |
| 13 | Full snapshot observability, OTLP exporter last | Section 7; chunk 16 is the last build chunk | none |
| 14 | Per-instance enforcement, static fleet config, graceful drain | Section 8; chunk 14 | none |

Two entries in INFLUENCES.md were written in Redis terms and are translated here, not overridden: F30's "RESP has no Retry-After" becomes "a distinct SQLSTATE per refusal", and its "`MGET` width caps" becomes reply-size and batch-width caps.

---

## 3. Design decisions this plan makes

Each is a decision of this proposal, open to Nick's veto. "Ours" marks a design not taken from a source.

**D1 — One single-threaded engine per proxy instance.** All decisions (pool, queues, coalescer, limits) are made by one sans-I/O state machine: events in (bytes from a client, bytes from the database, a connection opened or closed, the clock reached a deadline), actions out (write these bytes, open a connection, close one, wake me at time T). In production a thin shell of goroutines does the socket reads and writes and posts events to the engine through bounded mailboxes. In the simulator the same engine is driven directly by the event loop. So the code that decides is the same code in both, and the replay law covers it. This is the shape PgBouncer uses (one event loop). Cost: one instance uses one core for decisions; more throughput means more instances, which is the deployment section 8 supports anyway.

**D2 — Cores are pure and protocol-free.** `internal/pool`, `internal/admission`, `internal/coalesce`, `internal/batch`, `internal/retry` take `now` as an argument, return actions, hold no goroutines, locks, channels, wall clock or floating point, and import nothing from the wire layer. A lint enforces it (section 6.3). Integer forms are used where sources use floats: the CoDel control law's `interval/√count` uses the integer reciprocal-square-root step the Linux implementation uses; AIMD's ×0.9 is `limit*9/10`; histograms are integer buckets.

**D3 — One queue, not two.** A request waits in exactly one place: its tenant's admission queue. The dispatcher releases a request only when a database connection can be lent to it, so there is no second, hidden wait inside the pool (mansio's finding P14: callers must be budgeted below the pool or the excess waits invisibly).

**D4 — The unit of admission is the transaction, not the statement.** A client outside a transaction is queued, shed and scheduled like any request. Once the database reports the client is inside a transaction (status byte `T` or `E`), its next statements go straight to the connection it holds: shedding a statement in the middle of a transaction would abort work already done. The price is that a transaction left idle pins a connection; an idle-in-transaction deadline bounds it (section 9.2).

**D5 — At most one request per client connection inside the proxy.** The shell does not read a client's next message until the reply to the previous one has been written. Unread requests stay in the socket, which is the back pressure. A reply streams through a bounded per-client buffer; when it fills, the proxy stops reading that database connection; if the client stays stuck past the write deadline the client is dropped and the database connection goes through the dirty-connection table (D7).

**D6 — Tenant = the `user` in the client's startup message, looked up in a static table.** The proxy authenticates clients itself (SCRAM-SHA-256, or trust for tests). All pooled connections log in to Postgres as one configured role on one database, which is what makes them interchangeable. Per-tenant database roles would need one pool per role (PgBouncer's model) and are a documented seam, not v1. Open question 4.

**D7 — Destroy on doubt.** A database connection is reused only when it is known idle and clean: last status byte `I`, no unread bytes, no session parameter changed. When a client vanishes mid-statement the proxy asks Postgres to cancel, waits a bounded time for the end of the reply, rolls back if a transaction was open, and reuses the connection only if that all succeeded; otherwise it closes it (Odyssey's dirty-connection table, I3). Any protocol surprise closes the connection.

**D8 — Session state is rejected, with a second line of defence.** The classifier refuses the session-level features on the reject list (section 9.4). Because a lexer cannot see everything (a function can change a setting), the wire layer also watches for a `ParameterStatus` message arriving during a client's turn — the probe shows Postgres sends one when a reported setting changes — and marks that connection dirty. An optional `DISCARD ALL` on every release is a config switch whose cost is measured, not a default.

**D9 — Batching means pipelining, and only where it is provably safe (ours).** The probe shows that several `Query` messages written to one Postgres connection in one write are answered in order, each with its own end-of-reply marker, and that an error in one does not affect the next. It also shows the hazard: if one of them opens a transaction, the next one runs inside it. So a request may be batched only if the classifier proves it is a single statement that is not transaction control, and its client is outside a transaction. When a connection frees up, the dispatcher takes what is already queued and eligible, up to `max_batch`, and writes it in one go; replies are split at the end-of-reply markers. If any marker comes back with a status other than idle, the connection is poisoned: closed, and the remaining members of the batch get the "may have been applied" error. No timer exists. Because batch members wait behind each other on one connection, batching trades latency for fewer round trips; it ships config-selectable and **off by default** until its own benchmark shows a gain (open question 3).

**D10 — Coalescing shares only what is provably a read, and by default never returns data older than the request (ours).** A statement is coalescable when the classifier proves: one statement, outside a transaction, begins with `SELECT`, no locking clause, no `INTO`, no data-changing keyword anywhere, and every function it calls is on a small allow-list of functions known not to have side effects or per-call results. The key is the exact statement text. Two join modes, selectable: **strict** (default) — a request never joins a read that is already running on the database; it joins the next one, which starts when the running one finishes, so its result is never older than its own arrival; **relaxed** — a request joins the running read. The probe shows why this matters: a read already running returned the old value after a concurrent write had been acknowledged, so relaxed mode can break "read your own write" for a moment. Strict costs up to one extra service time of latency; under a 1,000-request burst both modes send 1–2 reads to the database. Followers take no queue slot and no connection. Errors are shared. The waiter set and the buffered reply are capped. A flight runs under its own deadline and is cancelled only when every waiter has left. Open question 2.

**D11 — Errors are ordinary Postgres error replies with a contract.** Every refusal or failure the proxy itself produces is an `ErrorResponse` with a documented SQLSTATE, followed by the correct end-of-reply marker, and one generated table says for each: was anything sent to the database, and may a write have been applied (section 9.1).

**D12 — Health and stats never touch the data path.** The engine publishes a snapshot copy at a fixed interval; the admin listener (health, stats, metrics) serves that copy from its own goroutines and its own port. It cannot wait on the pool, a queue or the engine.

---

## 4. Steering answer 1 — does a generic backend interface stay honest for Postgres?

**Verdict: Postgres-first with documented, lint-enforced extension seams. The single generic backend interface is deferred until a second protocol exists.** A generic request/response interface would misdescribe Postgres in at least six ways, shown below. What v1 keeps generic is what can be proved generic inside v1: the cores (they never see a Postgres type) and the byte transport to the database (it has two implementations, real TCP and the simulator).

### 4.1 Evidence

All "probe" lines are from `agent_work/phase1/probes/pgwire/output-2026-11-24.txt`, run against Postgres 17.11.

The obvious generic interface is `Do(request) → response` on a pooled connection — what a Redis-style proxy needs. Postgres does not fit it:

| # | Postgres fact | Evidence | What a generic `Do(request) → response` would have to pretend |
| --- | --- | --- | --- |
| 1 | **When the connection becomes free is decided by the reply, not the request.** | Probe B1: the single message `BEGIN; SELECT 1` ends with status `T` — the connection is still owned by that client. Probe B2: after an error inside it, status `E`; only `ROLLBACK` returns `I`. | That a request has a known extent. Here the pool learns from a byte in the reply whether it may lend the connection to someone else. The lease is the unit, and its end is a wire event. |
| 2 | **The unit of admission is the transaction.** | Follows from 1 and decision D4: statements inside an open transaction cannot be queued or shed independently. | That every request passes the same admission path. Postgres needs two: "start a lease" and "continue a lease". |
| 3 | **A reply is a stream of many messages of unbounded size.** | Probe B8: one 55-byte query returned 200,000 `DataRow` messages, 20 MB. Probe B3: one message, two result sets. | That a response is a value. The proxy must relay a reply while it is still arriving, with back pressure in both directions, or one query can exhaust its memory. |
| 4 | **Errors are in-band and carry transaction state.** | Probe B4: an error reply is followed by a normal end-of-reply marker, and the next pipelined query runs fine. Probe A: a proxy-made error is an `ErrorResponse` plus a marker whose status byte must match what the client believes. | That an error is a Go `error`. Here the proxy must write a protocol-correct reply, and choose the status byte. |
| 5 | **Failure after forwarding has no reply at all.** | Probe B9: a backend killed mid-query sent `FATAL 57P01` and then the stream ended with no end-of-reply marker. | That a failed call returns an error value. The proxy must decide what the client is told when the answer is "unknown" (section 9.1). |
| 6 | **Session state lives on the connection and outlives the transaction.** | Probe B6: `SET statement_timeout` in one exchange was still in force in a later one; `SET application_name` also triggered an unsolicited `ParameterStatus` message. | That connections are interchangeable by construction. For Postgres they are interchangeable only because the proxy rejects or scrubs session state. |
| 7 | **Sharing a connection between clients is legal only for some requests.** | Probe B4 (safe: three independent queries pipelined) against probe B5 (unsafe: the second query ran inside the first one's transaction). | That batching is a property of the pool. Here it depends on what the statement is. |
| 8 | **Sharing a result needs to know what the statement does and when its snapshot was taken.** | Probe B10: a read in flight returned `v=1` after a write of `v=2` had been acknowledged 12 ms after being sent. | That a "key" and a "read" flag come with the request. For SQL both are the output of a classifier, and "compatible consistency" is a timing rule. |
| 9 | **Connecting is a multi-step conversation.** | Probe B0: startup → `AuthenticationSASL` → two SCRAM round trips → 14 `ParameterStatus` messages → key data → ready. | That `Dial` returns a usable connection. Here "connecting" is a state machine with its own timeouts and failure classes, and it is the thing the creation-rate cap limits. |
| 10 | **Cancelling uses a second connection.** | Protocol documentation; the key arrives in `BackendKeyData` (probe B0). | That cancellation is a context. |

A generic interface that expressed all ten would be the Postgres session model with the names removed. A Redis backend would then have to pretend to have leases, status bytes and streamed replies. That is the leaky interface the steering asks us not to ship. And with one implementation there is no test that could show the interface is right.

Evidence on the codec (steering answer 9), same probe:

- `pgproto3` compiles in with exactly two other packages, both inside pgx (`pgx/v5/internal/iobufpool`, `pgx/v5/internal/pgio`). Nothing else from pgx's module graph is built. `go.sum` gains hash lines for pgx and for the modules pgx's own `go.mod` names (16 lines in the probe), but none of their code is compiled in.
- Its message types can be used without its blocking reader: part A of the probe feeds 40 bytes one at a time into a 15-line framer of our own and decodes every message with the types' `Decode` method. So the codec fits a sans-I/O engine, and the framer (the part that has to cope with 1-byte reads) is ours.
- It gives message types only. The SCRAM exchange, the startup sequence, transaction tracking and error mapping are ours: the probe's SCRAM client is about 50 lines on the standard library (`crypto/pbkdf2`, `hmac`, `sha256`) and was accepted by the real server.

### 4.2 What v1 ships instead: three seams

| Seam | What it is | Why it is honest | How it is held |
| --- | --- | --- | --- |
| **Work vocabulary** (`internal/work`) | Small plain types the cores are written against: tenant id, a unit of work with its class (reject / start a lease / continue a lease / answer locally), an optional share key, a batchable flag, and an outcome (connection released clean / still held / dirty; reached the database or not; error class). | The cores are exercised in the simulator by a synthetic workload that produces these types directly, before any Postgres code exists (chunks 7–13 each have such a test). That is a second producer of the vocabulary inside v1. | Import lint: the core packages may not import `pgproto3` or `internal/pgwire`. |
| **Backend transport** (`internal/proxy`: `Dialer`, byte-stream connection events) | Open a byte stream to the database, bytes in, bytes out, closed. No Postgres knowledge. | Two real implementations in v1: TCP in the shell, the simulated network in the harness. | Both run the same e2e scenarios (section 6.2). |
| **Wire boundary** (`internal/pgwire`) | Everything that knows Postgres: framing, both session state machines, the classifier, the error table. It turns bytes into work units and outcomes into bytes. | It is concrete Postgres code, not an interface. A second protocol would be a sibling package producing the same work vocabulary. | Import lint: only `internal/pgwire`, the simulator's fake Postgres and the test clients import `pgproto3`. |

`docs/extension-seams.md` (a deliverable of chunk 6, kept current after) records what a second protocol would have to supply — the ten rows above, answered for that protocol — and which engine code is Postgres-shaped (the lease lifecycle in `internal/proxy`). Extracting a polymorphic protocol interface is listed as a post-v1 follow-up (section 17), to be done against a real second implementation.

---

## 5. Steering answer 8 — can the object-store simulator be the starting point?

**Decision: no, not as a whole — fall back to a seeded event-loop kernel written in this repository, seeded with the clean pieces of the object-store simulator copied in with their source commit recorded, and keeping its conventions.** About 8% of the object-store simulator's code would survive unchanged and about 26% with edits; the piece this project needs most (byte-stream connections) does not exist there.

Full evaluation, with file and line citations: `agent_work/phase1/research/q8-object-store-simulator-evaluation.md`. It was made by reading the code at `/root/Software/object-store` (commit `379e622`; the checkout was renamed from `decentralized-object-store` on 2026-11-23) and running its tests (`go test ./internal/sim/... ./internal/env/... ./cmd/objsim/...` — all pass). The orchestrator re-checked the three load-bearing claims in the code: the stream class delivers whole messages per node pair (`internal/sim/net.go:118-130`), the loop's dispatch switches on domain event kinds, and the project's own extraction note lists the same tangles.

### 5.1 What the object-store simulator is

2,966 lines in `internal/sim` plus 318 in `internal/env`, 328 in `simtest`, a 367-line toy protocol and a 127-line CLI: 4,106 lines of product code, 4,594 of tests. Standard library only in product code; its property tests use `pgregory.net/rapid`. It simulates a cluster of nodes that exchange datagrams and whole-message requests, with per-node clocks (skew and drift), node crashes and restarts with incarnations, a per-node disk with torn writes and bit rot, partitions, and a chaos driver. Its API was frozen at tag `freeze-0b` and has changed since only by the module rename. Its adversarial record: 58 attacks held, 2 broke (one narrowed in writing, one repaired).

### 5.2 Fit against this project's needs

| This project needs | Verdict | Evidence |
| --- | --- | --- |
| Seeded single-threaded event loop | Adapt (rewrite ~250 of 799 lines) | The loop dispatches on ten domain event kinds and indexes nodes and links (`sim.go:672-757`); its own extraction note: the loop "does NOT treat kind/node/peer as opaque integers". |
| Event heap ordered by (time, sequence) | **Reuse as-is** (~95 lines) | `queue.go:86-178`; real removal on cancel, which per-request deadline timers need. |
| Named random streams from (seed, label) | **Reuse as-is** (19 lines) | `stream.go`; standard library only. |
| Virtual clock with many cancellable timers | Adapt (~60 lines) | Timers are bound to a per-node incarnation handle and pass through per-node skew and drift (`clock.go`). The saturating time helpers copy unchanged. |
| Seed flags, pinned seeds, sweeps | **Reuse as-is** (156 lines) | `simtest/seeds.go` names nothing from the object store. |
| Run-twice determinism check and the replay line | Reuse with an import change (153 lines) | `simtest/determinism.go` needs nine methods of the simulator; the new kernel keeps those names. |
| Scenario CLI | Reuse with a rename (104 lines) | `cmd/objsim/main.go`. |
| Determinism lint | **Reuse as-is** (1,468 lines, own nested module, standard library only) | `agent_work/gates/detlint/`; bans wall clock, goroutines, map-order leaks and floats in checked packages, with a per-function allow-list. |
| **Byte-stream connections** (ordered, partial reads and writes, back pressure, half-close, reset) | **Missing** | The network is datagrams plus whole messages up to 16 MiB, ordered per node pair. No connection object. The object store's own plan says the simulator "does not model short reads, kernel buffers, connection resets". This is the centre of our simulated world. |
| Fake Postgres (latency distributions, connection limit, errors, stalls, kill mid-transaction) | Missing | Latency is a uniform range only; no connection-count concept. |
| Simulated clients (tenants, Zipf keys, open-loop arrivals, cancel mid-request) | Missing | `Workload` schedules uniform gaps only (19 lines). Modelling 1,000 clients as nodes costs a quadratic link matrix, and node ids are packed in 16 bits. |
| Fault injection | Shape transfers, content does not | All 18 fault kinds are node, partition or disk faults. None is "stall the backend 150 ms", "reset this connection", "deliver one byte at a time". |
| Decision trace (accepted, coalesced, shed, timed out) | Missing | Only the simulator itself can append to the log; a process cannot. The log cannot be switched off. |
| Latency histograms, metrics capture | Missing | No histogram or percentile code. |
| Shrinking a failing schedule | Missing under our module policy | Done by `rapid`, which we cannot import. The bytes-to-fault-schedule pattern works with the standard fuzzer and is copied as a pattern. |
| Cores as "events in, actions out" state machines | Does not transfer | Object-store cores are callback objects holding an environment handle. |
| Goroutine shell under `testing/synctest` | Does not transfer | Not part of that simulator. Its real-runtime package shows the "post everything to one loop" idea; taken as an idea only. |

About 76% of `internal/sim` is cluster machinery we would delete (node lifecycle, partitions, disk, chaos driver, incarnations).

### 5.3 What is reused, adapted, and left behind

- **Copied as-is, with the source commit recorded in each file's header and in `docs/knowledge/`:** named streams, seed plumbing, the heap, the saturating time helpers, the determinism lint (as a nested module under `tools/`).
- **Copied and adapted:** the loop skeleton (step, run-until, step budget, stall errors, panic guard, invariant registration — about 460 lines edited), the run-twice determinism check, the scenario CLI, and the meta-test structure (same-process replay, cross-process replay, golden digests, planted-bug canaries), rewritten against a small gateway toy.
- **Conventions kept:** `-seed`, `DST_SEED`, `DST_SEEDS`; a `REPLAY:` line printed on every failure; no floats and no map order in any decision; the API freeze after the simulator lands (changes only by change request).
- **Not transferred:** node registry, incarnations, regions, per-node clocks, disk, partitions, the chaos driver, the datagram and whole-message network, the fixed event log.
- **Built new here:** an opaque-event loop, byte-stream connections, the fake Postgres, client and tenant workload generators, the gateway fault catalog, a decision trace with split digests, integer histograms, and a small standard-library shrinker for fault schedules (delete-one-fault reduction; no `rapid`).

Two decisions the evaluation asked for: **floats** — cores use integer arithmetic throughout (decision D2), so the lint's float ban stays on for them; the workload generator (not a core) may use `math/rand/v2`'s Zipf and is allow-listed, with the determinism claim scoped to same commit, same toolchain, linux/amd64. **Core style** — cores take `now` and return actions (decision D1), so only the time type is copied from `internal/env`, not the clock and timer interfaces.

### 5.4 Follow-up after this project (explicitly OUT of v1 scope)

When mini-gateway is finished: assess extracting a shared simulation-kernel repository and refactoring **both** projects onto it. The evaluation names the common core: 64-bit time with saturation, named streams, an opaque-kind (time, sequence) loop with cancel, value-type timers, step budget with stall errors and a panic guard, invariant registration, a trace-sink interface with split digests, seed and replay plumbing against a small interface, and the determinism lint. Each project would keep its own world on top (cluster and disk there; byte streams, fake Postgres and load generators here). Because this plan keeps the copied files' names and semantics, that extraction should be a move rather than a redesign. Nothing in v1 depends on it and no v1 chunk does any of it.

---

## 6. Testing architecture

Testing is the main deliverable of this project, and the simulator lands before any proxy code.

### 6.1 Layers

| Layer | What runs | What it proves | Where it runs |
| --- | --- | --- | --- |
| **Unit and property tests** | Each core with seeded random inputs; a trivially correct reference model per structure where one exists | The core's invariants | CI and local |
| **Simulation, tier S** | The real engine(s) on the event loop, with the fake Postgres, simulated clients, simulated byte streams and injected faults | Whole-system invariants, exactly and replayably; the chaos scenarios; multi-instance scenarios | Short seed set in CI; sweeps on demand |
| **Shell tests** | The goroutine shell with in-memory pipes under `testing/synctest`, and `-race` | No goroutine leaks, deadlines fire, mailboxes are bounded, teardown is clean | CI |
| **Real, tier R** | The real `mini-gateway` binary against a real Postgres 17 container, driven by the test client and the load generator | The fake Postgres is faithful; the shell works on real sockets; the headline numbers | Local gate and on demand (needs Docker) |
| **Fuzz** | Framer and message decoding with arbitrary chunking; the classifier; bytes-to-fault-schedule | No panic, no unbounded allocation, agreement between chunkings | Seed corpus as plain tests in CI; real fuzzing on demand |

### 6.2 Rules

- **Replay law.** Running a scenario twice with the same seed gives a bit-identical decision trace and bit-identical final snapshots. It is stated for the engine and cores (which is all the decision logic, by D1). Scope: same commit, same toolchain, linux/amd64. Every failure prints a `REPLAY:` line that reproduces it.
- **Same scenario, two tiers.** A scenario is written once against a small driver interface and runs on the simulator and on the real binary with real Postgres. Tier S is exact; tier R uses thresholds.
- **Fidelity of the fake.** A scripted conversation is run against real Postgres and the fake; the message types and status bytes must match. The probe's ten exchanges are the first script.
- **Exact checks in simulation, thresholds only against real hardware** (INFLUENCES C13). The spec's statistical invariants ("> 99%", "within 2× baseline") are checked in the simulator as exact comparisons against a control run with the same seed.
- **Ablation.** Every scenario that shows a mechanism working is also run with the mechanism off or replaced by a pass-through, and must then fail its invariant. A scenario that passes both ways proves nothing (*Release It!* notes).
- **Recovery.** Every chaos scenario asserts that goodput returns after the fault is removed, not only that the system behaved during it (metastable-failure papers; *Release It!* notes: "stays broken after the fault is removed" raises severity).
- **Quiesce.** After every scenario: connections in use 0, every queue empty, no flight, no pending timer, no shell goroutine beyond the baseline. This catches the leaked-connection class of bug in the chunk that introduces it.
- **Faults come from outside.** Faults are injected in the fake Postgres, the simulated network and the simulated clients — never through a test-only branch inside a core.
- **The harness tests itself.** A fault on one connection leaves traffic on another unaffected; a cleared fault leaves behaviour identical to a never-faulted run; a planted bug in the toy is caught.
- **Counters must move.** A test that tolerates an error must also require a success; a scenario must show its subject was exercised (non-zero counters), so a dead feature cannot pass.
- **End-to-end with every chunk.** The harness lands with the first runnable system (chunk 6) and every later chunk adds its scenarios in both tiers. The final chunks are never the first end-to-end run. Section 14 has the table.

### 6.3 Gates and where they run

- **CI runs fast tests only** (target: under five minutes): build, vet, the lints, unit and property tests with a small fixed budget, the pinned simulator seeds, shell tests with `-race`, fuzz seed corpora as plain tests. No Docker, no sweeps.
- **On-demand scripts** (in `scripts/`, indexed in `agent_work/INDEX.md`): `sweep.sh` (hundreds of seeds per scenario), `fuzz.sh` (timed fuzzing per target), `race-long.sh`, `soak.sh` (long steady-state run), `tier-r.sh` (real Postgres scenarios), `bench.sh`.
- **The local landing gate is the landing authority**, not CI: it runs the chunk's full list including the sweep and tier R, writes a machine-readable result, and fails closed.
- **Lints:** the determinism lint on core packages; an import lint (cores cannot import the wire layer or `pgproto3`; nothing outside the wire layer, the fake and the test clients imports `pgproto3`; `go.mod` has exactly one requirement); a bounded-channel lint for the shell (no unbuffered-forever or unbounded structures without a named cap).
- **Makefile:** `make test` (the CI set), `make chaos` (the named chaos scenarios, tier S, then tier R if Docker is present), `make bench` (section 11). Each exists from chunk 0 and fails with "not built yet" until its chunk lands, so a green target is never a stub.

### 6.4 The spec's invariants, restated

| Spec invariant | As tested here |
| --- | --- |
| Hard connection cap | Open + connecting database connections ≤ pool size at every event, in every scenario (a global invariant registered once). |
| At most one in-flight call per key; writes never coalesced; cancel 99 of 100 | Exactly as written; "in-flight" counts reads running on the database. |
| Bounded memory, no goroutine leaks | Every queue, map and buffer has a named cap (the generated capacity table); the quiesce rule; shell leak tests. |
| Noisy-neighbour fairness (> 99% success, p99 within 2×, flooder takes > 95% of shedding) | In simulation: compared exactly against a control run of the quiet tenant alone, same seed. Against real Postgres: the spec's thresholds. |
| CoDel "wait converges to target" | **Restated per mode** (steering answer 3). Timeout-rule mode: every served request waited ≤ the interval; depth is the same for a run of length T and 4T; the database is never idled by shedding. Control-law mode: under moderate overload the sojourn time returns to the target; under heavy open-loop overload it does not converge (the Phase 0 toy showed this) and depth is bounded by the queue's hard capacity instead. The head-to-head table states this plainly. |

---

## 7. Observability architecture

First-class, like testing: it has its own landing-gate item and its own per-chunk obligations.

### 7.1 Three layers, one direction of dependency

1. **State in the component.** Each core keeps its counters and gauges as plain integers and fixed-size integer histograms. No telemetry imports. `Snapshot()` returns a copy as a plain struct.
2. **One collector.** `internal/obs` walks the components of an instance and assembles one instance snapshot (with the instance id, build and config fingerprint). The engine publishes it at a fixed interval; readers get the last published copy (decision D12).
3. **Exporters read only the collector's output.** Prometheus text, JSON stats, CSV writer (the evidence tables for scenarios and benchmarks), a structured one-line log, and — last — a hand-written OTLP/HTTP push exporter. Adding an exporter touches no component. All standard library.

Across instances, aggregation belongs to whoever reads the exporters: counters and gauges sum, histograms merge, ratios are recomputed from merged counters and never averaged. `internal/obs` ships the merge function, and the multi-instance scenarios use it.

### 7.2 Three rules this project adds

- **Control reads are not exports.** Admission and the adaptive limit read queue wait and in-flight counts on every request; those values are core state, always on, read in place. A snapshot is a copy for observers and is never on a decision path.
- **Snapshots obey the replay law.** No wall-clock time, no map order. In the simulator, conservation is asserted exactly: accepted = completed + failed + shed + queued + in flight.
- **Recording survives the failure it records.** Outcome counters are incremented on every exit path, including aborts and teardown; a check that cannot find its signal is a finding, not something to route around.

### 7.3 Cost model

- **Always on:** the integer counters and gauges that are also control state. In a single-threaded core these are plain increments; no atomics, no locks.
- **Config-gated (`obs.detail`):** histograms and the per-tenant breakdown. Off: one predictable branch per event, no memory. On: one array increment per recorded value; 20.5 KB per histogram at two significant digits over 1 µs–60 s (Phase 0 probe).
- **Publish:** one copy of the instance snapshot per interval (default 1 s), proportional to active tenants × histograms; done by the engine between events.
- **Opt-in diagnostics, off by default:** per-key hotness for the coalescer, mailbox wait timing in the shell. They cost on the hot path.
- Chunk 3 ships a benchmark that measures these (detail off vs on) and an allocation gate (zero allocations per recorded event), so the cost model is a tested claim.

### 7.4 Label policy

Prometheus gets low-cardinality labels only: component, outcome, reason, mode, and a **bounded** tenant label — the configured tenants up to `obs.prom_tenant_max` (default 32), the rest folded into `other`. Never statement text, client address or error text. Full per-tenant detail is in the snapshot, the JSON endpoint and CSV. Exporter label sets for tenants that went quiet are evicted (steady state).

### 7.5 Coverage

| Area | In v1 | Out, and why |
| --- | --- | --- |
| Integration points | Per side (client, database): bytes, messages, connects, connect failures by class, time waiting on the database per statement | Per-statement-text metrics — unbounded cardinality |
| Throughput | Accepted, completed, failed, rejected, shed — per component and per tenant | |
| Back pressure and queues | Depth, capacity, **age of the oldest item**, queue-wait histogram, rule state (standing queue, dropping), LIFO on/off, sheds by reason | |
| Connections | Open, idle, lent, connecting, creation rate, exhaustion events, closes by reason (idle, lifetime, dirty, error), breaker state and transitions | |
| Contention | Coalescer: flights, hits, waiters per flight, cancellations, overflows. Batcher: batch-size histogram, poison events. Shell mailbox depth | Lock-wait timing — the engine has no locks; mailbox wait is the opt-in equivalent |
| Fairness | Per tenant: in flight, share, shed share, rate-gate refusals | |
| Retries | Attempts, budget level, budget exhaustions | |
| Status and errors | Counts by error-contract row (SQLSTATE and class), latency histograms per tenant × outcome | |
| Adaptive limit | Current limit, floor, ceiling, increases, decreases by cause | |
| Process | Goroutines, heap in use, open file descriptors against the limit, build and config fingerprint | CPU profiling endpoints — `pprof` is a debug switch, not an exporter |
| Health | Three states: `ok`; `degraded` while shedding, while the breaker is open, or while draining; `failed` only when this instance cannot work at all. A database outage is `degraded`, not `failed`: the proxy is still answering, with fast typed errors. The body names each check and its reason. | |

Each chunk states what its mechanism does to the health mapping.

### 7.6 Landing gate item

A chunk that adds or changes a component does not land without: its `Snapshot()`; its mapping in every exporter that exists at that point; a test showing its counters move and conservation holds; its row in the coverage table; its health rule. Acceptance test of the whole (chunk 15): add latency to the database below every timeout and answer "what is slow?" from the stats surface alone; then stop the database and time how long until health says `degraded`.

---

## 8. Horizontal scale

Stance (steering answer 14): each instance enforces its own limits; a static configuration declares the instance count and divides fleet-level budgets; nothing is coordinated at run time; running N instances is a supported v1 deployment. We build no balancer, discovery or membership; the load generator and scenarios spread connections.

### 8.1 Fleet configuration

```
fleet:
  instances: 3                      # N
  backend_connections: 96           # fleet budget; per instance = 96 / 3 = 32
  connect_rate_per_s: 30            # fleet budget; per instance = 10
  backend_max_connections: 120      # what Postgres allows; validation checks budget + margin ≤ this
```

Validation at load, failing closed: every per-instance value is at least 1; remainders are reported, not silently dropped; fleet connection budget plus a reserve (admin, cancel) fits under the database's limit; timeouts nest (section 9.2). Tenant rate-gate rates and the retry budget's floor are divided by N; ratios are not.

### 8.2 Each guarantee at N instances

| Guarantee | At N instances | Why | Measured in |
| --- | --- | --- | --- |
| Hard cap on database connections | **Multiplies** (≤ N × per-instance pool); the fleet bound holds because the pool is set from the fleet budget | Each instance caps itself | 3-instance reconnection storm |
| Connection creation rate | **Multiplies**; divided by N in config | Same | Same scenario; cold start of all three |
| One read per hot key | **Weakens** to at most N reads per burst | Flights are per instance | Hot key seen by 3 coalescers: exact count of database reads |
| Queue-wait bound (both rules, LIFO) | **Survives** | Each instance bounds its own queues | Per-instance assertion in every 3-instance scenario |
| Fair share between tenants | **Survives if tenants are spread evenly; weakens if a tenant's connections land on one instance** | Shares are computed among the tenants active on that instance | A tenant pinned to one instance against tenants spread over three |
| Rate gate (GCRA) | **Multiplies**; divided by N in config; still weak if a tenant is unevenly spread | Per-instance state | Same scenario |
| Retry budget | Ratio **survives**; the bucket floor **multiplies** and is divided by N | N instances each ≤ 10% is a fleet ≤ 10% | Retry-storm scenario at 3 instances |
| Adaptive limit (AIMD) | **Survives** | N independent AIMD loops on one bottleneck is the case Chiu and Jain analysed; each reads database latency itself | Backend stall at 3 instances: sum of limits and convergence |
| Batching, coalescing safety, error contract | **Survive** | Local to a connection or an instance | — |
| Metrics | Per instance; merged outside | Section 7.1 | Merge test |
| Loss of an instance | Survivors receive its clients as a reconnection burst | Chain reaction (*Release It!*) | One of three killed at 80% load: survivors shed, do not queue; the fleet connection bound holds |

The weakened and conditional rows are measured exactly in the simulator (three engines on one event loop cost almost nothing, because they are sans-I/O) and once against real Postgres with three processes.

### 8.3 Graceful drain

On SIGTERM an instance: reports `degraded (draining)` on health; stops accepting new client connections; answers new work from idle clients with the drain error (pre-forward, safe to retry elsewhere); lets transactions in progress finish, bounded by the statement deadline plus one second; closes idle database connections as they free up; exits. Health and stats stay answerable throughout. Every component has a `draining` input and its own refusal class from its first chunk; chunk 14 only sequences them. Failure accounting for a restart has three classes — refused fast, timed out, cut mid-answer — and the target for the third is zero.

---

## 9. Contracts: errors, timeouts, capacities, the reject list

Three of these are **generated from code by a test and held by that test**, so the document cannot drift from the behaviour: `docs/error-contract.md`, `docs/timeout-budget.md`, `docs/capacity-table.md`. They start in chunk 5 or 6 and gain rows with each chunk.

### 9.1 Error contract (initial proposal; codes are fixed in chunk 5)

"Pre-forward" means no byte of the request reached the database. Private SQLSTATE subclasses (`M..`) sit inside standard classes so drivers map them sensibly.

| Situation | Reply | Reached the database? | May a write have been applied? | Safe to retry? |
| --- | --- | --- | --- | --- |
| Shed by the queue rule, or queue full | `ERROR 53M01`, connection stays usable | No | No | Yes, after backing off |
| Refused by the rate gate | `ERROR 53M02` | No | No | Yes, later |
| Database marked down (breaker open) | `ERROR 53M03` | No | No | Yes, later |
| Coalescer waiter cap or shared-reply cap exceeded | `ERROR 53M04` | No (for this caller) | No | Yes |
| Instance draining | `ERROR 53M05`, then the connection is closed when idle | No | No | Yes, on another instance |
| Feature on the reject list; extended-protocol message | `ERROR 0A000` (feature not supported) | No | No | No — change the client |
| Cancelled while queued | `ERROR 57014` | No | No | Caller's choice |
| Database connection lost or reply timed out after a statement classified read-only was sent, outside a transaction | `ERROR 08006`, connection stays usable | Yes | No, as classified | Yes |
| Same, but the statement could write, or was `COMMIT` | `ERROR 08007` (transaction resolution unknown) | Yes | **Yes — unknown** | Not without checking |
| Database connection lost while the client was inside a transaction, between statements | `FATAL 08006`, client connection closed | Yes | No — the transaction cannot have committed | Yes, the whole transaction |
| Transaction left idle past the deadline | Rolled back on the database; `FATAL 25P03`, client connection closed | Yes | No | Yes, the whole transaction |
| Reply already partly sent to the client when the database connection failed | Client connection closed — never a short reply that looks complete | Yes | Per the rows above | Per the rows above |
| An error from Postgres itself | Passed through unchanged | Yes | As Postgres says | As Postgres says |

The proxy's own retry (chunk 13) may act only on a failure where the "reached the database" column is "No" for certain — for example a lent connection found dead before the first byte was written.

### 9.2 Timeout budget (initial values, each marked "initial" and tuned by a named experiment)

Every wait is bounded and an inner wait always ends before the outer one. Config validation asserts the nesting.

| Wait | Initial | Ends with |
| --- | --- | --- |
| Client startup and authentication | 5 s | Connection closed |
| Client mid-message read (a partial message) | 5 s | Connection closed |
| Client idle between transactions | 10 min | Connection closed |
| Client idle inside a transaction | 5 s | Rollback, `25P03` |
| Queue wait | Set by the queue rule (interval 100 ms, target 5 ms; both configurable and to be reasoned against database service time) | `53M01` |
| Waiting for a connection | None separate — it is the queue wait (D3) | — |
| Database connect, including authentication | 2 s | Counts toward the breaker |
| Database statement (send to end of reply) | 30 s | Cancel, then the dirty-connection table; `08006`/`08007` |
| Coalesced flight | Same as the statement deadline; outlives any single waiter | Shared error |
| Cancel and clean-up of a dirty connection | 1 s | Connection closed |
| Client write stalled | 5 s | Client dropped; dirty-connection table |
| Drain | Statement deadline + 1 s | Exit |

### 9.3 Capacity table

One row per bounded thing: what it protects, its capacity and config key, what happens when it is full, its deadline, its gauge. Initial rows: client connections; clients in startup (a separate cap, Odyssey's idea — it is the client-side half of the reconnection storm); database connections; connections being opened; per-tenant queue; total queued; coalescer flights; waiters per flight; shared-reply bytes; batch size; per-client reply buffer; shell mailboxes; active-tenant states; concurrent cancels. A parse-time check asserts the gates in front of the pool sum to something the pool can serve.

### 9.4 Reject list (transaction pooling; derived from RDS Proxy's pinning triggers, I27, and PgBouncer's and Hyperdrive's transaction-mode tables)

Rejected with `0A000` before anything is sent: session `SET` and `RESET` (but `SET LOCAL` inside a transaction is allowed); `PREPARE`, `EXECUTE`, `DEALLOCATE`; `LISTEN`, `UNLISTEN`; `DECLARE … WITH HOLD`; temporary tables; session advisory-lock functions; `DISCARD`; `LOAD`; `COPY`; every extended-protocol message; startup parameters that differ from the pool's fixed session settings (other than `application_name`, which is accepted and ignored). The classifier is a lexer (strings, dollar quotes, comments), not a SQL parser, and is deliberately conservative: when it cannot tell, a statement is still forwarded but is never shared or batched. Limits are stated in the book: a function or view can hide session changes or side effects; D8's second line of defence catches reported settings only.

---

## 10. *Release It!* mapping

Source: Nick's notes at `/root/Software/book-notes/books/release-it/` — a teaching project ("litefleet": 18 planted flaws fixed over 12 lessons, plus 8 review procedures), read in full for the lessons and procedures. **Limits:** the notes are secondhand; the book itself was not read (the PDF is on Drive, which this run could not reach); the knowledge store has no *Release It!* entry (ten searches, all recorded as misses). The notes do not contain the book's material on pooled connections going dead silently (firewall idle timeouts, half-open sockets); that gap is open question 6. Full mining report with citations: `agent_work/phase1/research/release-it-mining.md`.

The notes' rule for a pattern: it is four decisions — where it sits, what numbers it needs, what the caller gets when it trips, which signal shows it tripped. Every protective mechanism in a chunk brief is written as that four-part row.

### 10.1 Patterns: adopted, adapted, rejected

| Pattern | Verdict | Where it lands |
| --- | --- | --- |
| Timeouts | **Adopt.** One generated budget table, inner before outer, checked at config load. "First-byte deadline ≥ longest legitimate work." Cancellation must be real: measure how late work stops after its deadline. | Section 9.2; starts in chunk 6, grows in 7, 8, 11, 14 |
| Fail fast | **Adopt.** Fixed order per request: parse and classify → reject list → draining → breaker → rate gate → coalescer join → queue → lease. Each refusal has its own error and counter. | Chunks 5, 6, 7, 8, 10 |
| Circuit breaker | **Adapt.** Exactly one: the database-down state on the pool, with a connect lockout. Only connection-class failures count; SQL errors never do. Half-open admits one trial. "A breaker without a fallback just fails faster" is accepted on purpose: with no cache, the fast pre-forward error is the fallback. No per-tenant or per-statement breakers. | Chunk 7 |
| Bulkheads | **Adapt.** Per tenant, soft: a share that binds under saturation. The partition unit is the tenant from the first queue chunk (the notes found the unit was the decisive choice). It bounds database concurrency, not database CPU, and the book says so. **Adopt hard** for one thing: health and stats never draw on the data path (D12). | Chunks 6 (health path), 8, 9 |
| Shed load | **Adopt.** Already the core. Added from the notes: measure the cost of a refusal; health and metrics are never shed. | Chunks 8–10, 15 |
| Handshaking | **Adapt.** Postgres has no `Retry-After`; the handshake is a distinct SQLSTATE per refusal plus three-state health. | Chunks 5, 6 |
| Create back pressure | **Adopt.** One request per client inside the proxy; unread requests stay in the socket; bounded reply buffers (D5). | Chunk 6 |
| Steady state | **Adopt.** Every chunk brief has a row: what grows here, what removes it, which gauge shows the level. A tenant-churn soak asserts every size gauge levels off (three-point growth method). | Every chunk; the soak in chunk 14 |
| Governor | **Adopt.** Floor, ceiling, maximum step and minimum interval on the adaptive limit; the retry budget; the creation-rate cap; hysteresis on the LIFO switch. A test drives each with a pathological signal. | Chunks 7, 8, 10, 13 |
| Test harness | **Adopt.** It is Commit 0. Taken from the notes: the assertion shape (error class + elapsed time within budget + bounded resources), the harness self-test, and the fault catalog below. | Chunks 1, 2 |
| Let it crash | **Reject as a mechanism; keep as a scenario and as "destroy on doubt".** The proxy holds no durable state. | Chunk 14 (instance kill); D7 |
| Decoupling middleware | **Reject.** Accept-now-deliver-later contradicts honest "may have been applied" errors. | — |
| Transparency | **Adopt** as section 7. | Chunk 3 and every chunk |
| Graceful drain | **Adopt.** Section 8.3. | Reserved from chunk 6; sequenced in chunk 14 |

### 10.2 Insights that changed the order or added steps

1. Snapshot types, the integer histogram and the decision trace land **with the simulator** (chunk 1), and the fake Postgres has a snapshot too, so a scenario can show its fault really happened.
2. The timeout table and the error contract start with the first forwarding chunk, before any admission code: deadlines closed most of the notes' retry storm before a retry policy existed.
3. The quiesce invariant and the harness self-test are Commit 0 deliverables that every later chunk inherits.
4. A fault × path matrix (fault, where in the conversation, expected error class, elapsed budget, did a byte reach the database) starts in chunk 6 and gains rows per chunk; the retry policy later reads its last column.
5. The ablation run (mechanism off must fail, on must pass) is a landing-gate item.
6. Health-path isolation lands with the first listener (chunk 6); each protective chunk updates the health mapping.
7. Queues are keyed by tenant from the first queue chunk, not retrofitted when fairness arrives.
8. The retry seam — the "nothing reached the database" fact — exists from chunk 6; the policy that uses it comes later.
9. A `draining` input is reserved in every component from its first chunk.
10. Cold start and synchronized connection lifetimes (self-inflicted peaks) are scenarios of the pool chunk, not of the benchmark chunk.
11. Calibration is a step with a file as output that every real-hardware scenario reads; on this shared VM, percentiles are medians of windows with the worst window printed and host load recorded.
12. Sizing: in the notes, chunks that added a mechanism ran about twice their planned size. Section 16 budgets for that.

### 10.3 Anti-patterns as the landing checklist

The reviewer of every chunk answers these, in writing, for the code in that chunk:

| Anti-pattern | Question |
| --- | --- |
| Integration points | Does every read and write on a client or database connection here have a deadline on the virtual clock, and is there a scenario where the other side accepts and then says nothing on this exact path? |
| Blocked threads | On every exit path (success, error, timeout, client gone, drain) is the database connection returned or destroyed? Does quiesce show zero in use? |
| Slow responses | When the client leaves or the deadline passes, what happens to the work on the database — cancelled, connection destroyed, or left running — and is that counted? Is a refusal answered in bounded time? |
| Cascading failures | With the database down, does a request cost O(1) and no dial? Is health `degraded`, not `failed`? |
| Chain reactions | With 3 instances and one killed at 80% load, does this mechanism shed rather than queue? |
| Dogpile | After the fault ends, what is the peak dial rate and request rate against the database, and what caps it? Is recovery time asserted? |
| Self-denial attacks | What does this chunk do all at once (dial, expire, flush, retry)? Is it rate-capped or jittered, and is the peak measured? |
| Force multiplier | Does every automatic loop here have a floor, ceiling, maximum step and minimum interval, and a test that a bad signal cannot push it past them? |
| Unbalanced capacities | Is there a bounded buffer and a full-buffer rule at every producer/consumer boundary here? |
| Scaling effects | Is each guarantee here stated for N instances? Does any metric carry an unbounded label? |
| Users | Is every per-client and per-tenant resource here bounded by config, with a refusal at the bound? |
| Unbounded result sets | What is the most memory one request can hold here, and what enforces it? If a reply fails after its first byte was forwarded, is the client cut off rather than given a short reply that looks complete? |
| Compounds | Does any pair of the above meet here (for example a retry on a path that can leak)? |

### 10.4 Fault catalog for the simulator (from the notes' 22 named fault modes plus the spec's list)

Database side: refuses connections; accepts and never answers (at startup; after a query — the second is "a byte reached the database"); connect stalls; slow first byte (the spec's 150–200 ms stall); slow trickle of rows; fragmented delivery down to one byte, including inside a message header; reset before, during and after a reply and mid-transaction; garbage bytes and impossible lengths; a non-Postgres reply; a reply that ends without its end marker; very large rows and very many rows; unexpected state at checkout; unexpected `ParameterStatus`; killed (all connections reset at once), frozen (sockets open, nothing moves), restarted cold; `max_connections` reached; latency sitting exactly at a timeout boundary; some connections slow and others fine; half-open (peer gone, no reset seen); asymmetric latency. Client side: sends half a message and stops; stops reading replies; disconnects while queued, mid-statement, mid-transaction, while a coalesced waiter; reconnects in a burst; retries aggressively; sends a cancel. Load: open-loop arrival above capacity.

---

## 11. Benchmarking: "Apollo-lite"

Apollo's post describes three gates (is the run valid, do repeats agree, did the result move by more than a margin declared in advance) on dedicated hardware with hundreds of runs. This project runs on one shared 4-vCPU VM with no hardware performance counters. So:

**What we do.**

- The **simulator is the primary evidence** for every claim about counts and ordering: connections opened, reads reaching the database, who was shed. These are exact and replayable.
- Real-hardware runs use an **open-loop** load generator (`cmd/loadgen`): each request has an intended send time; latency is measured from that time; the generator never waits for a reply before sending the next. Achieved rate against intended rate is reported and gates the run (within 2%, or the run is invalid and discarded, not averaged). The reconnection storm is closed-loop by nature and is labelled so.
- An **A/A run** (the same binary as both arms) publishes the noise floor for each benchmark.
- **At least 10 runs per arm, interleaved**; a fresh proxy process and fresh database state per run; warm-up discarded; medians with spread; percentiles as the median of windows with the worst window shown; host load recorded beside every number; runs serialised under the machine-wide lock with builders idle.
- Margins and verdicts are **declared before the run**; "**inconclusive**" is an allowed verdict.
- **Calibration** first: measure the real backend's capacity open-loop, write it to a file, and express every scenario as a fraction of it.
- **Headlines are exact counts**, with latency percentiles second: database connections under 500 clients (measured, stated as clients ÷ pool — the spec's "19×" and "~15×" are labelled as a model, not our result); database reads per hot-key burst with and without coalescing; shed attribution by tenant; goodput. Then the quiet tenant's p99 under a noisy neighbour, unprotected FIFO against the full admission stack, in both CoDel modes.
- Results are emitted in Go benchmark text format so `benchstat` can read them, but the report uses an in-repo median/spread script (no new dependency).
- The scripts ship in the repository so a reader can re-run them on real hardware.

**Deliverable Nick added (steering answer 7): the trade-offs write-up.** A book chapter, "How much to trust these numbers" (source `book/chapters/…-benchmark-trade-offs.md`, Neon's prose from evidence gathered in chunk 15; also linked from the README). It must cover:

| Part | Content |
| --- | --- |
| What Apollo-lite keeps | Run-validity gate; repeats with spread; pre-declared margins; "inconclusive"; seeded backend; A/A noise floor; open-loop generation |
| What it gives up | Dedicated and separate machines for generator and system; hundreds of runs; distribution-distance statistics; formal confidence intervals; hardware counters; isolation from a noisy host |
| What that costs | Effects of a few percent cannot be claimed at all; latency percentiles carry the VM's noise; generator and proxy compete for four cores |
| Confidence per headline | A table, one row per headline number: its kind (exact count from the simulator / exact count from real Postgres / latency ratio on a shared VM), what would have to be true for it to be wrong, and the confidence it deserves in words. Expected shape: connection counts and database-read counts — high (exact, replayable, order-of-magnitude effects); shed attribution — high in the simulator, medium on the VM; p99 ratios — medium at best, reported with the A/A floor beside them; absolute latencies and throughput — low, machine-specific, not headlines |
| What would raise confidence | Separate hosts, more runs, a pinned CPU set — listed as how a reader can do better |

---

## 12. Build flow and the landing gate

### 12.1 Seats (from the seat ruling)

| Seat | Role |
| --- | --- |
| **Fable** (orchestrator) | Writes each chunk's brief as a file, dispatches, runs the gates and the adversarial harnesses, final acceptance, lands (merge to main and push), keeps STATE.md, `agent_work/INDEX.md` and the C4 set current. Builds chunk 0 (scripts, CI, site — no product logic). |
| **Opus** | Writes the Go product code and its tests — logic only, no comments or prose. A fresh agent in its own worktree per chunk; chunks in the same wave run concurrently. |
| **Neon** (via Antigravity `agy`; OpenCode/OpenRouter only as a recorded fallback) | Reviews Opus's work (see open question 1), and writes **all** reader-facing prose — the book chapter, code comments, README, site copy, C4 explanatory text — once, late, per chunk, from the final verified evidence including every narrowed claim. |
| **Astra** (`gpt-6-astra`) | Writes adversarial harnesses that try to break each chunk's stated claims; Fable runs them and records held / broken counts. |
| Arbitration | A second FAIL, or a builder/reviewer standoff, goes to a top seat outside the build/review pair with the diff and both verdicts. |

Before each wave the orchestrator checks the usage gauges of every pool it is about to draw on, and does not dispatch into a pool at its limit.

### 12.2 Life of a chunk

1. Fable writes the brief (purpose, deliverables, claims to be attacked, tests, snapshot obligations, the four-part row per mechanism, the steady-state row, files it may touch).
2. A fresh Opus agent builds in its own worktree, tests first, with one planted-bug canary per chunk. Headless; exit file and log under `/root/runs/`.
3. Review (verdict PASS / NITS / FAIL). In parallel, a fresh Astra agent writes the adversarial harness from the brief's claims, not from the code.
4. One repair round by a fresh Opus agent, then re-review. A second FAIL goes to arbitration.
5. Fable runs the local landing gate and the adversarial harness; claims that broke are fixed or narrowed in writing.
6. Neon's prose pass, once, with the final evidence.
7. Fable checks the C4 set against the code, lands, verifies the chapter is live on Pages, updates STATE.md.
8. The agent that did the work records durable knowledge in `docs/knowledge/` and scripts in `agent_work/`, indexed.

### 12.3 Landing gate — every chunk

A chunk lands only when all of these hold:

1. Its tests pass in the local gate: unit and property tests, its simulator scenarios on the pinned seeds and an on-demand sweep, shell tests with `-race` where it touches the shell, tier R where it has a real-Postgres scenario. The gate's result file says so; the gate fails closed.
2. **End-to-end:** its scenarios from section 14 exist in tier S (and tier R where listed), each with a planted-fault canary, the ablation run, the recovery assertion and the quiesce check.
3. **Observability:** section 7.6.
4. The replay law holds for every scenario it adds.
5. Lints pass (determinism, imports, bounds); `go.mod` still has one requirement.
6. Generated contract documents are regenerated and their tests pass.
7. Review verdict PASS, or NITS closed.
8. Adversarial results recorded: held and broken counts; every break fixed or the claim narrowed in writing.
9. The *Release It!* checklist (section 10.3) answered by the reviewer; each mechanism has its four-part row and its steady-state row.
10. The multiplies / weakens / survives row for anything it adds (section 8.2).
11. C4 diagrams match the code (a stale diagram is a defect).
12. Neon's chapter is live on Pages and listed in the book's contents.
13. STATE.md and `agent_work/INDEX.md` are current.

---

## 13. The chunks

Seventeen chunks after the foundation, each sized for one fresh builder. Sizes are rough product-code estimates, excluding tests; chunks marked ×2 add a mechanism and are budgeted at up to twice the estimate (section 10.2, item 12).

### 13.1 Order and what can run together

```
Chunk 0  Foundation
   │
Chunk 1  Simulator kernel            ─┐  "Commit 0": the simulator harness.
Chunk 2  Simulated world             ─┘  No proxy code exists until both have landed.
   │
   ├── Chunk 3  Observability spine        ┐
   ├── Chunk 4  Wire sessions              ├ wave A (concurrent)
   └── Chunk 5  Classifier and contracts   ┘
   │
Chunk 6  Walking skeleton (first end-to-end system; the e2e harness lands here)
   │
   ├── Chunk 7  Pool                       ┐ wave B
   └── Chunk 8  Admission queues           ┘
   │
   ├── Chunk 9   Fairness                  ┐
   ├── Chunk 11  Coalescer                 ├ wave C
   └── Chunk 13  Retry policy              ┘
   │
   ├── Chunk 10  Adaptive limit and rate gate   ┐ wave D
   └── Chunk 12  Batching                       ┘
   │
Chunk 14  Fleet configuration and graceful drain
Chunk 15  Benchmarks and the trade-offs write-up
Chunk 16  OTLP push exporter   (last build chunk)
Chunk 17  Book finish          (assembly and polish only)
```

Chunks in a wave build concurrently in separate worktrees and land one at a time; the engine's stage seams (set in chunk 6) keep their edits to `internal/proxy` small and separate. The `internal/sim` API is frozen after chunk 2; later chunks change it only through a change request to the orchestrator.

For every chunk below, "Seats" is as in section 12.1 unless stated (build Opus; review per open question 1; adversarial Astra; prose Neon; gate, C4 and landing Fable), and "Landing" means the common gate of section 12.3 plus the chunk's own criteria.

---

### Chunk 0 — Foundation

**Purpose.** Everything a builder needs before writing the first line: the module, the gates, CI, the scripts, and a book site that can show chapters.

**Deliverables.** `go.mod` (`github.com/nickstrad/mini-gateway`, Go 1.26, no requirements yet). Makefile with `test`, `chaos`, `bench` that fail with "not built yet". The local gate script and its fail-closed result file. The determinism lint copied from object-store into `tools/detlint` (own module) with the source commit recorded; the import lint; the bounds lint. CI workflow (fast set only). On-demand script skeletons. Orchestration scripts (dispatch, launch check at 15 s, exit files, land). The book scaffold (`book/manifest.json`, front page) and the site build extended from the Phase 1 review site. `docs/knowledge/` with the first entries (Phase 1 findings). Usage-gauge check script.

**Tests.** Each gate has a canary that must make it fail (a planted wall-clock call for the lint, a forbidden import, a failing test). CI green on an empty module.

**Observability.** None (no component).

**Seats.** Built by Fable (no product logic). Astra checks that each gate can fail. No Neon chapter; the book's front page is Neon's.

**Landing.** Canaries fail closed; CI under five minutes; the site still serves.

---

### Chunk 1 — Simulator kernel (Commit 0, part 1)

**Purpose.** The deterministic core of the test harness: a virtual clock, one seeded event loop, simulated byte-stream connections, and the proof that a run repeats exactly.

**Deliverables.** `internal/sim`: 64-bit nanosecond time with saturating helpers; named random streams; the event heap; an opaque-event loop (step, run-until, step budget, stall errors, panic guard); cancellable timers; invariant registration with the global quiesce check; a decision trace that any actor can append to, with separate digests for the trace and for final state; a fault schedule (timed, recorded in the trace) and bytes-to-schedule decoding for the fuzzer; a delete-one-fault shrinker. `internal/sim/simnet`: connections as ordered byte streams with configurable delivery chunking (down to one byte), latency per direction, a bounded send window (back pressure), half-close, reset, stall, half-open. `internal/sim/simtest`: seed flags and variables, pinned seeds, sweep, run-twice check, the `REPLAY:` line. `internal/obs` (measurement half only): the snapshot conventions, the integer histogram (record, merge, percentile; out-of-range values clamped and counted), the conservation helper. A toy line-echo protocol with a planted-bug switch, used only by the meta-tests. `cmd/mgsim` skeleton. Copied object-store files carry their source commit.

**Tests.** Meta-tests: same-process and cross-process replay give identical digests; golden digests for pinned seeds; stream isolation (a new draw on one stream does not shift another); a planted bug in the toy is caught and shrunk; harness self-tests (a fault on connection A leaves B untouched; a cleared fault leaves a run identical to a never-faulted one). Property tests for the heap, timers, histogram (relative error, merge = recording both) and byte streams (bytes out = bytes in, in order, for every chunking). e2e for this chunk: the toy echo under every simnet fault, on the pinned seeds.

**Observability.** The simulated network has a snapshot (connections, bytes, faults injected by kind). Histogram and snapshot conventions land here so everything later measures through them.

**Landing.** The replay law demonstrated on the toy; the determinism lint passes on `internal/sim`; adversarial attacks on replay (the object-store harness's attack list is the starting brief). Size: ~1,500 lines, ×2.

---

### Chunk 2 — Simulated world (Commit 0, part 2)

**Purpose.** The things the proxy will stand between: a fake Postgres that can misbehave in every way in the fault catalog, and simulated clients that generate realistic and hostile load. Plus the "no proxy" baseline every later comparison needs.

**Deliverables.** `internal/sim/fakepg`: speaks the simple-query subset over simnet using `pgproto3` and its own small framer (**this is where the one external dependency enters `go.mod`**); trust authentication (SCRAM is added in chunk 4 using the wire layer's code); a `max_connections` limit with Postgres's real refusal; transaction status tracking; a small keyed table so reads and writes have checkable results and a write log for "was it applied?" assertions; service-time models (fixed, distribution, per-key, stall windows) on the virtual clock; every database-side fault in section 10.4; cancel handling; its own snapshot. `internal/sim/workload`: client actors with tenants, open-loop arrivals (constant and Poisson) with intended-send-time bookkeeping, Zipf key choice, transactions of several statements, think time, and every client-side fault in section 10.4 including aggressive retry (a refused client schedules its own retry as an event). A scenario type and `cmd/mgsim run <scenario> -seed N`. The fidelity script runner (the same scripted exchange against the fake and a real Postgres). The scenario driver interface that tier R will also implement.

**Tests.** Fidelity: the ten probe exchanges match between the fake and Postgres 17 (tier R, on demand; recorded transcripts replayed in CI). The fake under each fault behaves as specified. Workload generators: achieved arrivals = intended; Zipf shares match the Phase 0 calculation. e2e for this chunk — **the direct baseline**: clients straight at the fake Postgres with no proxy: 500 clients open 500 connections; a reconnection burst past `max_connections` is refused by the database; 1,000 identical reads cost 1,000 database reads. These are the control arms for chunks 7 and 11.

**Observability.** The fake Postgres snapshot: connections open and peak, statements by kind, writes applied, faults injected. Workload snapshot: intended, sent, completed by outcome, latency histograms from intended send time.

**Landing.** Replay law for every baseline scenario; the import lint allows `pgproto3` only here (and later in `internal/pgwire` and test clients); simulator API frozen at this commit (tag `freeze-sim`). Size: ~1,800 lines, ×2.

---

### Chunk 3 — Observability spine

**Purpose.** The path from a component's numbers to something a person or a scraper can read, built before the components so each one plugs in on arrival.

**Deliverables.** `internal/obs`: the collector (instance snapshot assembly, publish interval, last-copy reads); the merge function across instances; exporters — JSON stats, CSV writer, one-line structured log, Prometheus text with the bounded tenant label and label eviction; the three-state health type and its rule table; the `obs.detail` gate. The cost-model benchmark and the zero-allocation gate. An exporter conformance test any future exporter must pass.

**Tests.** Golden output per exporter; Prometheus text parses under the exposition grammar (a small in-repo checker); merge laws (sum, histogram merge, ratios recomputed); label bound holds with 10,000 tenants; snapshots contain no time and no map order (run-twice byte equality). e2e for this chunk: the chunk 2 baseline scenarios exported through every exporter, CSV checked against the scenario's own counts.

**Observability.** This chunk is the obligation's machinery; it adds the collector's own counters (publishes, export errors).

**Landing.** Cost numbers recorded for section 7.3; conformance test in place. Size: ~1,200 lines.

---

### Chunk 4 — Wire sessions

**Purpose.** Speaking Postgres on both sides: taking a client through startup and login, and logging the proxy in to the database, as pure state machines that cope with bytes arriving in any fragments.

**Deliverables.** `internal/pgwire`: the incremental framer (with a maximum message size); the client-facing session machine — TLS and GSS requests answered "no", startup message, tenant lookup by user, SCRAM-SHA-256 server side and trust, the parameter-status set, proxy-issued key data, cancel-request recognition, termination; the database-facing session machine — startup, SCRAM client side, collecting server parameters, key data, ready; SCRAM as pure functions shared with the simulator's fake and test clients; the query-cycle tracker that follows a reply to its end marker and reports the status byte, unsolicited messages and protocol surprises. Unknown but well-framed message types on the database side are passed through, not rejected.

**Tests.** Golden bytes for every message we produce; every-chunking tests (a conversation split at every byte boundary gives the same events); fuzzing of the framer and both machines (no panic, bounded memory); SCRAM against the RFC 7677 test vector and against real Postgres (tier R); wrong password, unknown user, wrong database give the right fatal errors. e2e for this chunk, in the simulator: a simulated client completes startup through the two machines wired back-to-back to the fake Postgres, under one-byte delivery and under each startup-phase fault (accept and silence, reset mid-handshake, garbage), each ending in the expected error class within its deadline.

**Observability.** Wire snapshot: startups begun, completed and failed by reason; authentication failures; messages and bytes by direction; protocol violations; oversized messages.

**Landing.** Fidelity against Postgres 17 for startup; determinism lint passes on the machines. Size: ~1,400 lines, ×2.

---

### Chunk 5 — Statement classifier and the contracts

**Purpose.** Deciding, from the text of a statement, what the proxy may do with it — and fixing in generated, tested tables what the proxy tells a client when it refuses or fails.

**Deliverables.** In `internal/pgwire`: the SQL lexer (strings, escape strings, dollar quotes, comments, identifiers, statement splitting); the classifier producing a work unit — reject (with reason), transaction control, read-only-provable, coalescable (with key), batchable; the reject list of section 9.4; the function allow-list. `internal/work`: the vocabulary types. The error table as code, with the generator and test for `docs/error-contract.md`; builders for every proxy-made reply (error plus the correct end marker; the discard-until-sync rule for extended-protocol messages). The reject-list document generated the same way.

**Tests.** A classifier corpus (several hundred statements with expected classes, including hostile ones: keywords inside strings and comments, dollar-quoted bodies, data-changing common table expressions, locking clauses, multi-statement messages). Property: classification is identical for every way of padding with whitespace and comments. Conservative-by-construction test: any statement the lexer cannot fully tokenise is never coalescable or batchable. Differential test against real Postgres (tier R): every statement classed "transaction control: no" leaves status `I`; every statement classed read-only-provable leaves the fake's and the real table's write log unchanged. Fuzz: no panic. e2e for this chunk: each reject-list feature sent by a simulated client through the wire machines gets `0A000` and nothing reaches the fake Postgres (its statement counter stays zero).

**Observability.** Classifier counters: statements by class, rejects by reason, unclassifiable. Error-contract counters keyed by row.

**Landing.** Generated documents committed and held by tests; the stated limits of the classifier written down for Neon's chapter. Size: ~1,300 lines.

---

### Chunk 6 — Walking skeleton

**Purpose.** The first complete proxy: a client connects, runs statements and transactions, and gets correct answers through the proxy, in the simulator and against a real Postgres. Deliberately plain — a small fixed set of database connections and a bounded first-come queue — but with every seam the later chunks plug into.

**Deliverables.** `internal/proxy`: the engine (decision D1) with its stage seams — classify, gate, coalesce, queue, lease, forward, release — each a small interface with a pass-through implementation; the transaction-lease lifecycle (lend on first statement, hold while status is `T` or `E`, release on `I`); reply relay with bounded buffers and back pressure both ways (D5); the dirty-connection rule in its simplest form (close on doubt); the "reached the database?" fact recorded for every failure; the `draining` input reserved. `internal/shell`: listeners, per-connection reader and writer goroutines, bounded mailboxes, deadlines on every socket operation, the admin listener on its own port serving health, JSON stats and Prometheus from the published snapshot. `internal/config`: the config file, validation, the fleet block (parsed; division used from chunk 7). `cmd/mini-gateway`. `cmd/loadgen` in its first form (open-loop, intended-send-time, one workload). `test/e2e`: the scenario harness for tier S and tier R, with the Docker setup for Postgres 17. `docs/timeout-budget.md` and `docs/capacity-table.md` generators. `docs/extension-seams.md`. The fault × path matrix, first rows.

**Tests.** e2e, both tiers: *results through the proxy equal results direct* (same workload, same seed); *transaction integrity* — with many clients running interleaved multi-statement transactions over few connections, no statement of one client ever runs on a connection inside another client's transaction (checked from the fake's log, exactly; this is the core safety property of transaction pooling); *fragmentation* — one-byte delivery on both sides changes nothing; *reject list end-to-end*; *client vanishes mid-statement and mid-transaction* — the connection is not reused dirty; *database killed mid-statement* — the client gets the row of the error contract the matrix predicts; *slow reader* — bounded memory, proxy stops reading the database. Shell tests under `synctest` and `-race`: no goroutine left after any teardown path; mailboxes never exceed their cap. `psql` and `pgbench -S`-style simple-protocol smoke runs against the real binary (tier R).

**Observability.** Engine snapshot: clients connected, in startup, in transaction; requests by stage and outcome; statement-time histogram (database send to end of reply); relay buffer high-water; the three-state health (only `ok` / `failed` rules so far, plus "database unreachable → degraded").

**Landing.** The first chapter that shows a working system. Timeout nesting validated at load. This is an integration-shaped chunk: sized large, with arbitration budget. Size: ~2,200 lines, ×2.

---

### Chunk 7 — Pool

**Purpose.** The connection pool that is the point of the project: a hard cap that holds under a storm, a limit on how fast connections are opened, and sane handling of connections that go bad.

**Deliverables.** `internal/pool` core: accounting with open + connecting ≤ cap; a cap on concurrent connects and a creation rate (not PgBouncer's one-at-a-time); idle timeout and maximum lifetime with jitter (no synchronized expiry); check before reuse after long idle; the single breaker — database-down detection from connection-class failures, connect lockout, one trial when half-open, immediate `53M03` while open; the full dirty-connection table (cancel, bounded wait, rollback, reuse or close); the idle-in-transaction deadline; client-issued cancel requests mapped to the lent connection, with a cap on concurrent cancels; the separate cap on clients in startup; fleet division of the connection and creation-rate budgets. Wiring into the engine's lease stage.

**Tests.** Core property tests against a reference model with synthetic work units (no Postgres types). e2e, both tiers unless noted: **hard connection cap** — 1,000 simultaneous client connects, open + connecting never exceeds the cap (exact, simulator); **reconnection storm** — 500 clients dropped and reconnecting within 50 ms, database connections stay flat at the pool size, against the chunk 2 baseline where the database refuses clients; **slow consumer / half-open** — clients that stop reading are torn down at the write deadline and no connection leaks; **cold start** — empty pool, full load at time zero: dial rate ≤ the cap; **synchronized lifetimes** — no dial spike; **database restart** — breaker opens, requests cost O(1) with no dial, one trial, recovery without a dogpile, goodput returns; **idle-in-transaction** client is rolled back and its connection reused; **cancel** reaches the right statement and never another client's. Ablation: with the creation-rate cap off, the cold-start scenario exceeds the bound. Headline benchmark 1 (tier R): database connections under 500 clients, direct against proxy.

**Observability.** Pool snapshot: open, idle, lent, connecting; creation rate; closes by reason; exhaustion events; breaker state and transitions; dirty-table outcomes; cancels; lease-time histogram. Health: `degraded` while the breaker is open.

**Landing.** The spec's pool invariants; section 8.2 rows for cap and creation rate. Size: ~1,500 lines, ×2.

---

### Chunk 8 — Admission queues

**Purpose.** Deciding which waiting requests are still worth serving: per-tenant bounded queues with two selectable shedding rules, and newest-first service while overloaded.

**Deliverables.** `internal/admission`: a fixed-capacity ring per tenant, created on first use and removed when a tenant goes quiet; the `QueueRule` interface with two equal implementations — **timeout rule** ("Fail at Scale": one last-empty timestamp; allowed wait is the short target while a standing queue exists, else the interval) and **control law** (CoDel paper / RFC 8289: sojourn above target for an interval enters dropping; next drop after interval/√count, integer arithmetic) — selected by config, per instance; in both, age is measured on the oldest item and expired items are swept from the head and answered at once; adaptive LIFO (serve newest while the rule reports overload, with hysteresis); overflow sheds the head, not the arrival; folly's minimum-delay figure as a 0–100 load gauge. Simulator-only comparison arms: plain FIFO, folly's detector. Wiring into the engine's queue stage with a single default dispatcher (round robin arrives in chunk 9; the queues are tenant-keyed already).

**Tests.** Core property tests for both rules against reference models; the classic bug list from INFLUENCES F2–F4 as named tests (first burst after idle must not get the short timeout; LIFO without the head sweep buries stale requests). e2e: **anti-bufferbloat per mode** as restated in section 6.4, against a FIFO control with the same seed that grows without bound; **10× overload** — depth ≤ capacity, memory flat, backend never idled by shedding (served ≥ 95% of capacity); **the head-to-head table** — both modes and the comparison arms at 1.2×, 2× and 10× load: served-wait percentiles, depth, shed count, idle capacity (simulator, exact; this table goes in the book); recovery after overload ends. Ablation: with the rule off, the standing queue persists. Tier R: overload against real Postgres with `pg_sleep`-shaped service time.

**Observability.** Per tenant and total: depth, capacity, oldest-item age, queue-wait histogram, rule state, LIFO on/off and switches, sheds by reason, the load gauge. Health: `degraded` while any queue is shedding.

**Landing.** Both modes pass their own restated invariant; the difference between them is stated in numbers. Size: ~1,300 lines, ×2.

---

### Chunk 9 — Fairness

**Purpose.** Keeping a quiet tenant fast while a noisy one floods: choosing whose request runs next.

**Deliverables.** In `internal/admission`: the dispatcher — round robin across tenants with waiting work; a tenant's share (limit ÷ active tenants, with optional weights) enforced only when the limit is reached; work-conserving otherwise; per-tenant in-flight accounting that includes held transactions; removal of all-zero tenant state (memory bounded by active tenants). Parse-time checks on shares.

**Tests.** Property tests: per-tenant in flight ≤ share under saturation; total = sum; work-conserving (no idle capacity while anyone eligible waits); no starvation. e2e: **noisy neighbour** — tenant A at 10× capacity, tenant B steady and small: B's outcomes compared exactly with B running alone (simulator), and against the spec's thresholds (> 99% success, p99 within 2×, A takes > 95% of the shedding) in tier R; the test requires B's successes, not merely the absence of failures. **Many tenants, one flooder.** **A tenant holding transactions open** does not exceed its share. Ablation: with a single shared queue the quiet tenant starves.

**Observability.** Per tenant: in flight, current share, served, shed share, time at share. Dispatcher: rounds, saturation time.

**Landing.** The fairness invariant in both tiers; section 8.2 row for fairness written with its condition. Size: ~900 lines.

---

### Chunk 10 — Adaptive limit and rate gate

**Purpose.** Giving the database less work when it slows down, without anyone turning a knob; and an optional per-tenant speed limit at the door.

**Deliverables.** In `internal/admission`: the AIMD limit on concurrent leases — one integer; multiplicative decrease on a statement time over the threshold, a timeout or a connection-class error (SQL errors never count); additive increase only when in flight ≥ half the limit; bounded by a floor, the pool size, a maximum step and a minimum interval between decreases; its signal is database round-trip time per statement, never queue wait (the queue rule's signal) — two loops, two signals. The GCRA gate — per tenant, integer nanoseconds, off by default, state evicted for quiet tenants, divided by N from the fleet config. Wiring: the gate stage and the dispatcher's limit.

**Tests.** Property tests: bounds always hold; after latency steps from 50 ms to 200 ms the limit never increases until recovery, then rises by at most one per completion; a pathological signal cannot push the limit past floor or ceiling or move it faster than the interval allows. GCRA against a reference model; a burst after a quiet hour is still bounded. e2e: **noisy neighbour + backend stall** (spec suite 3) — a 200 ms database stall while A sends 5,000/s and B 50/s: B protected, limit falls and recovers, goodput returns after the stall; **limit and queue rule do not fight** (no oscillation beyond a stated bound); rate gate refuses with `53M02` and never when off. Ablation: with a static limit the stall scenario fails B's bound. Headline benchmark 3 (tier R): quiet tenant's p99 under a noisy neighbour — unprotected FIFO against the full stack, in both CoDel modes.

**Observability.** Limit, floor, ceiling, increases, decreases by cause, time at floor; gate allows and refusals per tenant. Health: `degraded` while the limit is at its floor.

**Landing.** Spec suite 3 in both tiers; the stated limit of a single latency threshold for mixed SQL workloads written down. Size: ~900 lines, ×2.

---

### Chunk 11 — Coalescer

**Purpose.** Turning a stampede of identical reads into one or two database reads — without ever sharing a write, and without one caller's cancellation hurting another.

**Deliverables.** `internal/coalesce`: the flight table keyed by statement text; leader and followers; the two join modes of decision D10 (strict by default); a flight deadline independent of every waiter; waiter reference counting — the database work is cancelled only when the last waiter leaves; a caller that has left gets its own error even if a result arrives at that moment; a cap on waiters per flight (`53M04` past it) and on the shared reply's size (past it, followers are refused and the key is not shared again for a while — a bounded, expiring list); shared errors; followers take no queue slot and no connection but are counted per tenant; entries removed on completion. Wiring into the engine's coalesce stage; the reply is stored once as the database's message bytes and each recipient gets its own end marker. Config: `off | auto | hinted` (hinted = only statements carrying an opt-in comment) and the scope of the key.

**Tests.** Property tests on the flight table with synthetic units. e2e: **at most one in-flight read per key** (exact, simulator); **writes are never coalesced** — every write in the workload appears in the fake's write log exactly once; **cancel 99 of 100** — the 100th waiter gets a valid reply; **all 100 cancel** — the database work is cancelled and the connection handled by the dirty table; **viral hot key** (spec suite 2) — 1,000 concurrent identical reads with 50 ms database latency: 1–2 database reads, p99 near one service time in relaxed mode and near two in strict mode, both reported; **read-your-own-write** — a client that writes then reads the same key never gets the old value in strict mode, and the same scenario shows the window in relaxed mode; statements inside transactions are never shared; a slow follower does not delay the others; shared error on a failed flight, with no re-drive stampede. Ablation: coalescer off gives 1,000 reads. Headline benchmark 2 (tier R): database reads and p99 under Zipf skew (exponent stated; the spec's "99/1" restated as what the generator actually produces) with and without coalescing.

**Observability.** Flights started, hits, waiters-per-flight histogram, cancellations (waiter and flight), overflows by cap, shared errors, table size; opt-in per-key hotness. Per tenant: coalesced hits.

**Landing.** The spec's three coalescing invariants; section 8.2 row "weakens to N" prepared for measurement in chunk 14. Size: ~1,100 lines, ×2.

---

### Chunk 12 — Batching

**Purpose.** Saving round trips when work is already waiting: sending several independent single-statement requests to a free database connection in one write. No timer, and never at the risk of one client's statement running in another's transaction.

**Deliverables.** `internal/batch`: the take rule (when a connection frees up, take what is queued and eligible, up to `max_batch` and `max_bytes`; one item with nothing else waiting goes alone at once); eligibility from the classifier (decision D9); in-order reply splitting at end markers; the poison rule (any non-idle status ends the batch: connection closed, remaining members get `08007` or `08006` by class); accounting — each member counts toward its tenant's in-flight and the adaptive limit; time spent behind earlier members of a batch is recorded separately and is not queue wait. Config: `off` (default) / `on`. Wiring into the dispatcher and the forward stage.

**Tests.** Property tests: batch ≤ caps; replies matched to requesters in order; with one request in the system, enqueue-to-write is zero on the virtual clock. e2e: **batch safety** — across seeds and workloads mixing transactions and single statements, no statement ever runs inside another client's transaction (the chunk 6 integrity check, now with batching on); **the hazard, planted** — a fake-Postgres fault that returns a non-idle status mid-batch: the connection is closed and no later member's reply is delivered as if clean; an error in one member does not affect the others; a member's client vanishing mid-batch; fairness still holds with batching on. Ablation and measurement: round trips and latency with batching off against on, at low load (no added latency) and at saturation, in the simulator and tier R — the result decides the default, and "no gain on a local socket" is an acceptable, reported outcome.

**Observability.** Batch-size histogram, batched share of requests, in-batch wait histogram, poison events, ineligible reasons.

**Landing.** The safety property over a sweep; the measured trade stated in numbers. Size: ~800 lines, ×2.

---

### Chunk 13 — Retry policy

**Purpose.** Making sure the proxy never turns a partial outage into a retry storm — its own retries are a deliberate, budgeted choice that is off unless switched on.

**Deliverables.** `internal/retry`: the `Policy` interface; **Off** (the default); **PreForwardOnce** — at most one retry, only for failures the engine marks "nothing reached the database", charged to a budget (each first attempt deposits a tenth of a token, each retry withdraws one, the bucket is capped so credit expires, a small floor divided by N; integers only); never when the breaker is open; never holding a queue slot or a connection while waiting; never past the request's deadline. Completion of the error contract and the fault × path matrix (every row now states retryable-by-proxy or not). Wiring into the engine's lease and forward stages.

**Tests.** Property tests of the budget (retries ≤ 10% of first attempts plus the floor over any window; a quiet hour cannot bank a storm). e2e: **retry storm** (spec suite 5) — 30% database errors with aggressively retrying clients: with the policy off and on, the load reaching the database stays bounded, refusals are fast, and goodput returns after the errors stop; **post-forward failures are never retried under any policy** — the fake's write log shows no statement applied twice across a sweep with kills at every point of the conversation; a retry happens only on the matrix rows that allow it; budget exhaustion is visible. Ablation: a deliberately wrong policy ("retry everything", test-only) produces duplicate writes and amplification, proving the scenario can fail.

**Observability.** Attempts, retries, budget level, deposits, withdrawals, exhaustions, retries refused by reason. Health: unchanged by retries.

**Landing.** Spec suite 5; default verified off by a config-defaults test. Size: ~600 lines.

---

### Chunk 14 — Fleet configuration and graceful drain

**Purpose.** Running three proxies in front of one database honestly: measuring what gets weaker, proving what holds, and shutting an instance down without cutting anyone off.

**Deliverables.** `internal/config`: the full fleet block with division and validation (section 8.1). The drain sequence (section 8.3) across engine and shell. Multi-instance support in the simulator scenarios (N engines, one fake Postgres, a connection-spreading rule) and in the tier R harness (three processes). The steady-state soak. `docs/fleet-guarantees.md`: the table of section 8.2 with measured numbers, generated from scenario output.

**Tests.** e2e, three instances: **reconnection storm split across 3** — fleet connections ≤ the fleet budget; **hot key seen by 3 coalescers** — at most 3 database reads; **tenant pinned to one instance** against tenants spread over three — the fairness loss measured; **retry storm** — fleet ratio holds; **backend stall** — three adaptive limits converge, their sum bounded; **one instance killed at 80% load** — survivors shed rather than queue, the connection bound holds, goodput settles near two thirds and recovers when it returns; **rolling restart with drain** — zero requests cut mid-answer; refused-fast and timed-out counts reported; without drain (ablation) requests are cut. **Steady state** — a long simulated run with tenant churn (many configured tenants, few active at a time): every size gauge sampled at three points levels off. Config validation tests (bad divisions, budgets over the database limit, timeouts that do not nest). Tier R: the reconnection storm and the kill with three real processes.

**Observability.** Instance id and fleet settings in the snapshot; drain state and progress; merged fleet view in the scenario reports. Health: `degraded (draining)`.

**Landing.** Every "weakens" and conditional row of section 8.2 has a measured number. Size: ~900 lines, ×2.

---

### Chunk 15 — Benchmarks and the trade-offs write-up

**Purpose.** The proof a reader can re-run: one command each for tests, chaos and benchmarks, and an honest account of how far the numbers can be trusted.

**Deliverables.** `cmd/loadgen` complete (workload profiles: fan-in, Zipf, noisy neighbour, storm; calibration mode; 3-significant-digit histograms; "could not send" count). `scripts/bench.sh` and `make bench`: calibration file, A/A run, interleaved runs, validity gate, the in-repo stats report, verdicts including "inconclusive", CSV and Go-benchmark-format output. `make chaos` running every named scenario. The three headline tables regenerated under the full method. The observability acceptance test (section 7.6). The long-budget runs: sweeps, soak, timed fuzzing, `-race` long. Evidence pack for Neon: every number with its kind and its confidence row. README proof section. **The trade-offs chapter** (section 11).

**Tests.** The load generator's own: samples = intended sends; a stalled server shows in the corrected latency; achieved-rate gate rejects a throttled run. The A/A run reports "no difference". Bounded memory and no goroutine growth under a sustained 10× overload on the real binary.

**Observability.** The load generator's snapshot; benchmark CSV through the same exporter as everything else.

**Landing.** `make test`, `make chaos`, `make bench` each run from a clean checkout; every headline carries its confidence statement; claims that did not come out in our favour are narrowed in writing. Size: ~1,200 lines.

---

### Chunk 16 — OTLP push exporter (last build chunk)

**Purpose.** Showing the snapshot design pays off: a new way to ship metrics added without touching any component.

**Deliverables.** `internal/obs/otlp`: a hand-written OTLP/HTTP JSON metrics push exporter on the standard library — no OpenTelemetry SDK; bounded queue, timeout, bounded retry with backoff, drop-and-count when the collector is down (the exporter must never slow the proxy). Config. The exporter conformance test from chunk 3.

**Tests.** Golden payloads; a collector that is down, slow, or answering errors never affects the data path (simulator and shell tests); the diff of this chunk touches no file outside `internal/obs`, `internal/config` and the shell's wiring — checked by the gate. Tier R, on demand: a real OpenTelemetry Collector container receives and accepts the metrics.

**Observability.** Exporter counters: pushes, failures, dropped snapshots, queue depth.

**Landing.** "No component changed" verified mechanically. Size: ~500 lines.

---

### Chunk 17 — Book finish

**Purpose.** Assembly and polish only. The chapters already exist and are live; this chunk makes them one book.

**Deliverables.** Neon: preface, topic map, chapter order and cross-references, the final README and site front page. Fable: final C4 pass (as-built), final gate run, the link check of every site URL. Astra: an independent check that every command in the README and book runs and every stated number matches the evidence files.

**Landing.** All site pages return 200; unsupported claims closed; STATE.md says done.

---

## 14. Scenario-to-chunk table

Every scenario has one owning chunk — the first chunk in which it can run — and runs in every gate after that. S = simulator tier, R = real Postgres tier.

| Scenario | Source | Owner | Tiers | Re-run with more parts |
| --- | --- | --- | --- | --- |
| Toy echo under every network fault; replay law | spec 4.1 | 1 | S | — |
| Direct baseline: 500 clients, storm, 1,000 identical reads with no proxy | control arms | 2 | S, R | compared against in 7, 11 |
| Fake Postgres fidelity script | section 6.2 | 2 | S+R | extended by 4, 5 |
| Startup and authentication under fragmentation and faults | spec 4.1 | 4 | S, R | — |
| Reject list end-to-end | steering 9 | 5 (wire-level), 6 (through the proxy) | S, R | — |
| Results through the proxy equal results direct | ours | 6 | S, R | every later chunk (with its feature on) |
| Transaction integrity (no cross-client interleaving) | ours | 6 | S, R | 7, 12, 14 |
| Client vanishes mid-statement / mid-transaction | spec 4.1 | 6 | S, R | 7 (full dirty table) |
| Database killed mid-statement; error-contract rows | spec 2.4 | 6 | S, R | 13 (matrix complete) |
| Hard connection cap, 1,000 simultaneous connects | spec 4.2 | 7 | S, R | 14 (×3) |
| **Reconnection storm** | spec 4.3 (1) | 7 | S, R | 14 (split across 3) |
| **Slow consumer / half-open drain** | spec 4.3 (4) | 7 | S, R | — |
| Cold start; synchronized lifetimes; database restart and recovery | *Release It!* | 7 | S, R | 14 |
| Headline 1: database connections, direct vs proxy | spec 4.4 | 7 | R | 15 (full method) |
| CoDel anti-bufferbloat, per mode; head-to-head table | spec 4.2, steering 3 | 8 | S, R | 10 |
| Bounded memory under 10× overload | spec 4.2 | 8 | S | 15 (real binary, sustained) |
| Goroutine count returns to baseline | spec 4.2 | 6 (shell tests) | synctest, R | 15 |
| Noisy-neighbour fairness | spec 4.2 | 9 | S, R | 10, 14 |
| **Noisy neighbour + backend stall** | spec 4.3 (3) | 10 | S, R | 14 |
| Headline 3: quiet tenant p99, FIFO vs full stack | spec 4.4 | 10 | R | 15 |
| At most one in-flight read per key; writes never coalesced; cancel 99 of 100 | spec 4.2 | 11 | S, R | 14 |
| **Viral hot-key stampede** | spec 4.3 (2) | 11 | S, R | 14 (3 coalescers) |
| Read-your-own-write under coalescing, both join modes | ours | 11 | S, R | — |
| Headline 2: database reads and p99 under skew | spec 4.4 | 11 | R | 15 |
| Batch safety; planted poison | ours | 12 | S, R | 14 |
| **Retry-storm amplification** | spec 4.3 (5) | 13 | S, R | 14 |
| No statement applied twice | spec 2.4 | 13 | S | — |
| Three-instance set: storm, hot key, pinned tenant, stall, instance kill | steering 14 | 14 | S, R (storm and kill) | 15 |
| Rolling restart with drain | steering 14 | 14 | S, R | — |
| Steady state under tenant churn | *Release It!* | 14 | S | 15 (long) |
| Observability acceptance ("what is slow?") | *Release It!* | 15 | R | — |
| Collector down does not affect the data path | steering 13 | 16 | S, R | — |

The five suites the spec names (bold) are each owned by the chunk that builds their subject; none waits for the benchmark chunk.

---

## 15. The book

The book is the learning artifact. Neon writes one chapter per chunk, once, late — after review, repair, adversarial results and the gate — from the final evidence, and it goes live on Pages at that chunk's landing, listed in the contents. A chunk is not landed until its chapter is live.

| Chapter | From chunk | Working title |
| --- | --- | --- |
| 1 | 1 | A world that repeats: the simulator kernel |
| 2 | 2 | A fake Postgres and hostile clients |
| 3 | 3 | Numbers without a framework: snapshots, a collector, exporters |
| 4 | 4 | Speaking Postgres: startup, login, fragments |
| 5 | 5 | What a statement is allowed to do: classification and the error contract |
| 6 | 6 | The first working proxy, and lending connections by the transaction |
| 7 | 7 | The pool: a hard cap, a creation rate, and connections that go bad |
| 8 | 8 | Two CoDels, measured against each other |
| 9 | 9 | Fairness that binds only when it must |
| 10 | 10 | Letting the database set the limit |
| 11 | 11 | One read for a thousand callers |
| 12 | 12 | Batching without a timer |
| 13 | 13 | Retries, off by default |
| 14 | 14 | Three proxies, one database: what multiplies, weakens and survives |
| 15 | 15 | How much to trust these numbers (the trade-offs write-up) |
| 16 | 16 | Adding an exporter without touching a component |
| — | 17 | Preface, topic map, assembly |

Chunk 0 has no chapter (the front page only). Titles are placeholders for Neon.

---

## 16. Risks

| Risk | Why it is real | What the plan does |
| --- | --- | --- |
| Integration chunks cost more than primitives | Object-store: two integration chunks cost more than five primitives together | Chunk 6 is sized large with arbitration budget; every later chunk integrates as it goes, so there is no big-bang integration chunk |
| Mechanism chunks run about 2× | *Release It!* notes | ×2 marks; split rule: a builder that passes ~2,000 lines of product code stops and asks the orchestrator to split |
| The classifier is wrong about a statement | It is a lexer without the catalog | Conservative by construction; allow-list for functions; `hinted` mode; the differential test against real Postgres; limits stated in the book |
| The fake Postgres drifts from the real one | A fake is a model | Fidelity script in both tiers; every scenario also runs in tier R |
| The single-threaded engine is a throughput ceiling | Decision D1 | Measured and stated in chunk 15; instances are the scaling unit; not hidden |
| Batching shows no gain | Local sockets make round trips cheap | Off by default; "no gain" is a reportable result |
| The control-law mode behaves badly under heavy open-loop overload | Phase 0 toy: queue grew without bound | It is bounded by the queue's hard capacity; the head-to-head table says plainly what each mode does |
| Wave merges conflict in `internal/proxy` | Several chunks wire into one engine | Stage seams fixed in chunk 6; chunks in a wave land one at a time and the later one rebases |
| Shared-VM noise | 4 shared vCPUs | Section 11; exact counts as headlines |
| Neon or another pool runs out mid-wave | Subscription limits | Gauge check before each wave; recorded fallback route for Neon |
| vm-1 Claude sign-in expires Nov 25–28 | STATE.md | Owner: Nick/Twin; everything resumes from files |

Total estimate: about 19,000–21,000 lines of product code before the ×2 allowances, and at least as much test code. It is a larger build than the spec's "2–3 weeks" for Variant B, because the steering adds two CoDel modes, the full observability system, the fleet work and batching to a Postgres proxy. If that is too much, the cheapest cuts, in order, are: chunk 12 (batching), the GCRA half of chunk 10, tier R for the three-instance scenarios. None is cut in this proposal.

---

## 17. Out of scope for v1, and follow-ups

**Out of v1:** the extended query protocol; TLS on either side; `COPY`; replication connections; `LISTEN`/`NOTIFY`; connection pinning; per-tenant database roles or several databases; caching; read replicas, hedging, outlier detection, shuffle sharding; a second backend protocol; run-time coordination between instances; a balancer, discovery or membership; the OpenTelemetry SDK; a SQL parser; md5 password authentication.

**Follow-ups after the project, not part of v1:**

1. **Shared simulation kernel** (steering answer 8): assess extracting a shared kernel repository and refactor both object-store and mini-gateway onto it (section 5.4).
2. **A polymorphic protocol interface**, extracted against a real second protocol (Valkey/RESP is the candidate Nick named), using `docs/extension-seams.md` (section 4.2).
3. Pinning with a hard cap, as the alternative to rejecting session features.
4. Coordinated fleet budgets, as the alternative to static division.

---

## 18. Open questions for Nick

1. **Who reviews Opus's code on this project?** The project seat ruling (2026-11-24) says Neon reviews Opus. The standing global rule (2026-11-11) says Opus builds are reviewed only by Fable. This plan writes "review" without naming the seat until you say which applies here. (Either way Neon writes the prose and Fable does final acceptance.)
2. **Coalescing join mode default (D10).** Strict (never older than the request; hot-key p99 near two service times) or relaxed (p99 near one, the spec's "near 50 ms", with a brief read-your-own-write gap)? Proposed: strict by default, relaxed selectable, both measured.
3. **Batching under Postgres (D9).** Your answer 11 kept timer-free batching; it was written when the backend was Redis. For Postgres it means pipelining independent single-statement requests onto one connection. Proposed: build it (chunk 12), off by default until its benchmark shows a gain. Or drop chunk 12.
4. **Tenant identity (D6).** Tenant = the startup `user`, checked by the proxy; all pooled connections use one database role. Acceptable for v1, with per-tenant roles as a seam?
5. **Client-facing extras in v1:** no TLS; SCRAM-SHA-256 and trust only; client cancel requests supported. Agree?
6. ***Release It!* gap.** Your notes do not include the book's material on pooled connections dying silently (firewall idle timeouts, half-open sockets), which matters for chunk 7. The plan covers those faults from the spec's own list. If you want the book consulted first, the PDF is on Drive and the Drive connector is not authorized for this session — it needs authorizing in the claude.ai connector settings, or the PDF placed on vm-1.
7. **Size.** Seventeen chunks, roughly 20,000 lines of product code. Go as proposed, or apply cuts from section 16?
8. **Still unrecorded from Phase 0:** approval of the "end-to-end with every chunk" wording draft (`/root/briefs/flow-guide-e2e-wording-draft.md`). The plan follows the rule as standing.

On approval, the first step is chunk 0, then the simulator. Until then nothing is built.
