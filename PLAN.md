# PLAN — mini-gateway

Operational document, written by the Fable orchestrator on 2026-11-24.
**Status: APPROVED by Nick on 2026-11-24, as amended by his rulings on the eight questions the proposal asked (section 18). Building starts on Nick's go — the Phase 2 launch. Nothing is built before it.**

Five amendments are folded into the text below, so it reads as one settled plan:

- **Observability amendment (2026-11-24):** the CSV flush, DuckDB analysis and the OTLP → Collector → Prometheus chain (sections 6.5, 7.1, 7.4, 7.6–7.9 and 11).
- **Nick's rulings on section 18 (2026-11-24,** `/root/briefs/zgateway-section18-rulings.md`**).** Neon reviews Opus on this project; coalescing is strict by default with relaxed selectable; **the batching chunk is dropped**; tenant = the startup user name over one database role; **client-facing TLS is included** (verdict and evidence in section 3.1) with SCRAM-SHA-256 as the authentication mechanism; the *Release It!* book is mined before the pool chunk; the size is approved; the end-to-end wording is approved. **The plan is now 16 chunks after the foundation.** The proposal's chunk 12 (batching) is gone and its chunks 13–17 are now chunks 12–16; anything written before these rulings (the done marker's first part, commit messages) uses the old numbers.

- **Nick's ruling 9 (2026-11-26,** `/root/briefs/zgateway-c05-ruling-reset-always.md`**): reset after every lease.** A database connection gets `DISCARD ALL` after every lease; session safety never depends on reading SQL; the "known clean" conditional reset is not built. It changes decisions D7, D8 and D10, steering answer 9 (section 2), sections 9.1, 9.2 and 9.4, and chunks 5, 6, 7 and 11. The evidence is the lease experiment (`agent_work/chunks/c05-classifier/lease-exp/REPORT.md`); the record is section 18, row 9.

- **Three later binding inputs from Nick (2026-11-24, folded in on 2026-11-26; section 19):** engine-level operation fuzzing with three rules for simulation-level fuzz targets, and value lowering in the shrinker (sections 6.1 and 6.6, chunks 6 and 7, change request CR-1); a load tool for watching a running gateway (chunk 6a); six developer-experience patterns from object-store, answered item by item (section 19.3). **The plan is now 16 chunks plus chunk 6a after the foundation.**

- **The write-up seat rule (Nick, 2026-11-26,** `/root/briefs/zgateway-clarity-prose-pass-ruling.md`**):** Neon writes every reader-facing write-up with the Clarity skill; prose from any other seat is a draft for Neon to rewrite; published prose that reads densely gets a Neon clarity rewrite, checked against its source before it lands (sections 12.1, 12.2, 15 and 19.4).

The diagrams that go with it are in [docs/c4/](docs/c4/README.md); both are published for review at
https://nickstrad.github.io/mini-gateway/.

Inputs, in order of authority:

1. Nick's 14 steering answers of 2026-11-24 (`/root/briefs/zgateway-steering-answers.md`) and his 8 rulings on this plan of the same day (`/root/briefs/zgateway-section18-rulings.md`). They govern; where a ruling changes a steering answer (batching, answer 11), the ruling is the later word.
2. The observability / horizontal-scale / *Release It!* note (`/root/briefs/zgateway-observability-scale-releaseit-note.md`) and the seat ruling (`/root/briefs/zgateway-seat-ruling.md`). Also the observability amendment of 2026-11-24 (`/root/briefs/zgateway-observability-csv-duckdb-amendment.md`), which extends steering answer 13 and does not replace it.
3. The spec, [docs/project-source.md](docs/project-source.md).
4. [INFLUENCES.md](INFLUENCES.md) — 100 influences and 18 conflicts. Written before the steering, with Redis as its working assumption. Where it disagrees with the steering, the steering wins; every such override is listed in section 2.

Evidence gathered in this phase (all in the repository):

| What | Where | Used in |
| --- | --- | --- |
| Probe against a real Postgres 17: startup with SCRAM, transaction status bytes, pipelining, session state, kill mid-query, statement snapshots | `agent_work/phase1/probes/pgwire/` (`main.go.txt` — renamed in chunk 2 so that no Go file outside the wire packages imports `pgproto3`; copy it to a scratch folder as `main.go` to run it — and `output-2026-11-24.txt`) | sections 4, 9, chunks 4–6, 11 |
| Evaluation of the object-store simulator, from its code | `agent_work/phase1/research/q8-object-store-simulator-evaluation.md` | section 5 |
| *Release It!* notes mined for the build plan | `agent_work/phase1/research/release-it-mining.md` | section 10 and the landing gate |
| TLS cost probe: `psql` and `pgbench` through a standard-library TLS-terminating relay in front of Postgres 17, TLS on either leg, handshake cost | `agent_work/phase1/probes/pgtls/` (`main.go`, `output-2026-11-24.txt`) | section 3.1, chunks 4 and 6 |

Contents: 1 what we build · 2 steering and overrides · 3 design decisions (3.1: the TLS verdict) · 4 backend-interface verdict (steering answer 1) · 5 simulator evaluation (steering answer 8) · 6 testing architecture · 7 observability architecture · 8 horizontal scale · 9 error, timeout and capacity contracts · 10 *Release It!* mapping · 11 benchmarking · 12 build flow and landing gate · 13 the chunks · 14 scenario-to-chunk table · 15 the book · 16 risks · 17 out of scope and follow-ups · 18 Nick's rulings (the decision record).

---

## 1. What we are building

**mini-gateway** is a Go proxy that speaks the Postgres wire protocol to clients and to one ordinary Postgres server. Clients connect to it as if it were Postgres. It gives the database a bounded, controlled number of connections no matter how many clients arrive, and it decides — per tenant, under overload — which requests wait, which run, and which are refused quickly with an error that says whether a retry is safe.

In one process it has these parts:

- **Wire layer.** Reads and writes Postgres messages (simple-query subset only) using the `pgproto3` package for message encoding. Handles client startup and authentication, proxy-to-database startup and authentication, tracks whether each client is inside a transaction, and classifies each statement (refused as a courtesy or forwarded; recognisably a shareable read or not). Clients may connect over TLS, which the proxy terminates; the connection to the database can use TLS too. Login is SCRAM-SHA-256 on both sides.
- **Pool.** A hard-capped set of database connections. A connection is lent to a client for one transaction and returns when the database reports the transaction has ended. Before it is lent again the proxy resets it with `DISCARD ALL`, so one client's session state never reaches the next (decision D8).
- **Admission.** One bounded queue per tenant, with two selectable rules for shedding stale work (the "Fail at Scale" queue-timeout rule and the CoDel paper's control law), newest-first service while overloaded, round-robin between tenants with a fair share that binds only when the database is saturated, an adaptive limit on concurrent database work (AIMD), and an optional per-tenant rate gate (GCRA) in front.
- **Coalescer.** Identical read-only statements that arrive together share one database round trip. Writes are never shared.
- **Retry policy.** An interface with two policies: off (the default), and "retry once, only if nothing reached the database, within a 10% budget".
- **Observability.** Every part keeps counters and gauges and returns them from `Snapshot()`. That snapshot is the one raw export. One collector reads the snapshots, and adapters behind one interface turn its output into Prometheus text, JSON, CSV files flushed to disk, a log line and (last) an OTLP push that reaches Prometheus through an OpenTelemetry Collector. No part knows which adapters exist. Tests assert on the flushed CSV files, with DuckDB for the SQL, so no metrics server has to run during testing.
- **Simulator.** A deterministic, single-threaded test harness with a virtual clock, simulated byte-stream connections, a fake Postgres and simulated clients. It is built first and everything else is proved in it before it is proved against a real Postgres.

Scope fixed by the steering: Postgres only in v1; simple-query protocol only; transaction pooling; session-level features never pinned — the visible ones are refused as a courtesy and every lease ends with a reset (ruling 9); no cache; no replicas; no run-time coordination between proxy instances; N instances supported through a static fleet configuration.

Not in v1 (details in section 17): the extended query protocol (Parse/Bind/Execute), batching (several clients' statements pipelined onto one connection), TLS channel binding and client certificates, `COPY`, replication, connection pinning, caching, read replicas, hedging, a second backend protocol, coordinated fleet state.

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
| 9 | Simple-query subset, transaction pooling, reject list, `pgproto3` from pgx/v5. **Changed by ruling 9 (2026-11-26):** the reject list is a best-effort courtesy; what keeps session state from crossing clients is `DISCARD ALL` after every lease (decision D8). A session feature that slips past the courtesy check works until its transaction ends and is then gone | Chunks 4–6; the one allowed external dependency | **Overrides** the recommendation (RESP2 allow-list) and section 6's "external modules of any kind" exclusion — for the codec only. |
| 10 | No promotions | Nothing promoted. folly's rule (F3) stays what it was: a gauge and a simulator-only comparison arm. P12 stays out. | none |
| 11 | GCRA as outer gate; AIMD in v1; timer-free batching | GCRA and AIMD: chunk 10. **Batching: dropped from v1 by Nick's ruling 3 of 2026-11-24.** The answer was given when the backend was Redis; under Postgres it would have meant pipelining several clients' statements onto one connection. What the probe showed about pipelining stays in section 4.1 as evidence (decision D9). | None for batching now: conflict C3's "a Postgres variant has no micro-batching" stands again. |
| 12 | Retry policy behind an interface, default OFF | Chunk 12 | **Overrides** the recommendation (retry on by default). |
| 13 | Full snapshot observability, OTLP exporter last. Amended 2026-11-24: CSV flush as a testing path, DuckDB over the flushed files, OTLP → Collector → Prometheus as the production path | Section 7 (7.7–7.9 for the amendment); section 6.5; chunk 15 is the last build chunk | none |
| 14 | Per-instance enforcement, static fleet config, graceful drain | Section 8; chunk 13 | none |

Two entries in INFLUENCES.md were written in Redis terms and are translated here, not overridden: F30's "RESP has no Retry-After" becomes "a distinct SQLSTATE per refusal", and its "`MGET` width caps" becomes reply-size caps.

---

## 3. Design decisions this plan makes

Each is a decision of this plan. "Ours" marks a design not taken from a source. D6, D9, D10 and D13 were put to Nick as questions; they are written here as he ruled on 2026-11-24 (section 18).

**D1 — One single-threaded engine per proxy instance.** All decisions (pool, queues, coalescer, limits) are made by one sans-I/O state machine: events in (bytes from a client, bytes from the database, a connection opened or closed, the clock reached a deadline), actions out (write these bytes, open a connection, close one, wake me at time T). In production a thin shell of goroutines does the socket reads and writes and posts events to the engine through bounded mailboxes. In the simulator the same engine is driven directly by the event loop. So the code that decides is the same code in both, and the replay law covers it. This is the shape PgBouncer uses (one event loop). Cost: one instance uses one core for decisions; more throughput means more instances, which is the deployment section 8 supports anyway.

**D2 — Cores are pure and protocol-free.** `internal/pool`, `internal/admission`, `internal/coalesce`, `internal/retry` take `now` as an argument, return actions, hold no goroutines, locks, channels, wall clock or floating point, and import nothing from the wire layer. A lint enforces it (section 6.3). Integer forms are used where sources use floats: the CoDel control law's `interval/√count` uses the integer reciprocal-square-root step the Linux implementation uses; AIMD's ×0.9 is `limit*9/10`; histograms are integer buckets.

**D3 — One queue, not two.** A request waits in exactly one place: its tenant's admission queue. The dispatcher releases a request only when a database connection can be lent to it, so there is no second, hidden wait inside the pool (mansio's finding P14: callers must be budgeted below the pool or the excess waits invisibly).

**D4 — The unit of admission is the transaction, not the statement.** A client outside a transaction is queued, shed and scheduled like any request. Once the database reports the client is inside a transaction (status byte `T` or `E`), its next statements go straight to the connection it holds: shedding a statement in the middle of a transaction would abort work already done. The price is that a transaction left idle pins a connection; an idle-in-transaction deadline bounds it (section 9.2).

**D5 — At most one request per client connection inside the proxy.** The shell does not read a client's next message until the reply to the previous one has been written. Unread requests stay in the socket, which is the back pressure. A reply streams through a bounded per-client buffer; when it fills, the proxy stops reading that database connection; if the client stays stuck past the write deadline the client is dropped and the database connection goes through the dirty-connection table (D7).

**D6 — Tenant = the `user` in the client's startup message, looked up in a static table (decided, ruling 4).** The proxy authenticates clients itself with SCRAM-SHA-256; the table holds each tenant's SCRAM verifier (salt, iteration count, stored key, server key), never a password. All pooled connections log in to Postgres as one configured role on one database, which is what makes them interchangeable. The consequence is stated in the book: Postgres sees one role, so per-tenant privileges and row-level security by role do not exist behind this proxy; the tenant is an admission and accounting identity. Per-tenant database roles need one pool per role (PgBouncer's model) and are a recorded later seam (section 17, follow-up 5), not v1.

**D7 — Destroy on doubt.** A database connection is reused only when it is known idle and has been reset: last status byte `I`, no unread bytes, and the reset of decision D8 completed after its last lease. When a client vanishes mid-statement the proxy asks Postgres to cancel, waits a bounded time for the end of the reply, rolls back if a transaction was open, and reuses the connection only if that all succeeded; otherwise it closes it (Odyssey's dirty-connection table, I3). Any protocol surprise closes the connection.

**D8 — Session state is reset after every lease; safety never depends on reading SQL (decided, ruling 9 of 2026-11-26; it replaces "rejected, with a second line of defence").** When a lease ends with status `I`, the proxy sends `DISCARD ALL` on that connection as its own `Query` message, and the connection may be lent again only after the proxy has seen the `DISCARD ALL` command tag followed by `ReadyForQuery` with status `I` in reply to that message. An error, another tag, another status byte or the reset deadline (section 9.2) closes the connection. The reset is unconditional: no statement is examined to decide it, so nothing a client writes can switch it off. A lease that ends any other way (the client gone inside a transaction, a protocol surprise) goes through the dirty-connection table first (D7). While it is being reset a connection counts as in use, so the hard cap is unaffected.

- **One code point, two policies and one option, all in config.** `lease.reset: always` is the default. `lease.reset: on-switch` resets only before a connection is lent to a different tenant than the one that held it last; that is a comparison of two startup user names, still no SQL. Under `on-switch` session state can cross clients of the *same* tenant, as in PgBouncer's transaction mode, and the book says so. `lease.reseed: on` follows each reset with the proxy's own `setseed`, which closes the first residue item below; it is off by default. There is no "no reset" setting: the arm without a reset exists only as a test switch for the ablation run.
- **Not built:** the "known clean" conditional reset (reset only when the lease's statements look harmless). It would put reading SQL back into the trust base (`agent_work/chunks/c05-classifier/OPTIONS.md`, option D). Close-on-tenant-switch was measured and is not built (section 17).
- **What `DISCARD ALL` leaves, stated in the book as the limit of this design** (OPTIONS.md, option A): (1) the random seed — the previous client chooses the next one's `random()` sequence unless `lease.reseed` is on; (2) the *existence* of a custom setting name (`SET myapp.x`), whose value is cleared; (3) in-memory state of loaded libraries, extensions and procedural languages such as PL/Perl and PL/Python; (4) a replication-origin session, which needs a privileged role.
- **The reject list stays as a courtesy** (section 9.4): a client that sends a visible session feature is told `0A000` instead of watching the feature silently vanish at the end of its transaction. No guarantee rests on it. An unsolicited `ParameterStatus` during a client's turn is counted; it no longer decides anything.
- **Evidence** — the lease experiment, stages 0–3 (`agent_work/chunks/c05-classifier/lease-exp/REPORT.md`, tables under `lease-exp/e7/`). On real Postgres 17 with no release action, session state crossed tenants in 15,657 of 34,006 checks and a secret was read back in 3,531 of 4,856 attempts; under reset-always and reset-on-switch both counts were zero, and the residue was exactly items 1 and 2, with the re-seed closing item 1 (0 of 541 probes). A reset costs 0.09–0.18 ms on an idle host against 16 ms for a new connection. At 80% load both reset policies kept the baseline's goodput in both tiers for 5 ms leases; for 0.5 ms leases the simulator shows a capacity loss of 21%, which is the stated cost of this design. The p99 wait is 1.07–1.33× the baseline in the simulator and inconclusive on real Postgres (inside the A/A noise floor). Reset-on-switch sent 470 resets per second against 2,321 with six tenants, and resets on nearly every lease with 100 or more, so it is kept as the policy for pools with few tenants. Chunk 14 measures the reset's cost again on the finished proxy.

**D9 — No batching in v1 (decided, ruling 3).** The proposal defined batching for Postgres as pipelining: writing several clients' independent single-statement requests to one free connection in one write. Nick dropped it. In v1 a database connection carries one lease at a time and the proxy never writes a second client's request to it before the first one's reply has ended. Nothing else in the plan depended on batching. What the probe learned about pipelining is kept as evidence (section 4.1, row 7): Postgres answers pipelined `Query` messages in order and isolates their errors, but a statement that opens a transaction captures the ones behind it — which is why batching would have needed the classifier and a poison rule. The dropped design is in git history (commit `5c83fe6`: decision D9 and chunk 12) if a later measurement ever argues for it (section 17, follow-up 6).

**D10 — Coalescing shares only what is recognisably a read, and by default never returns data older than the request (ours; decided, ruling 2; the recogniser narrowed by ruling 9).** A statement is shared automatically (`coalesce: auto`) only when it arrives outside a transaction and fits **the safe shape**, which is deliberately small enough to need no lexer: the whole message is one statement that begins with `SELECT`; it is made only of ASCII letters, digits, spaces and the characters `_ , * = < > ( ) '` (so no backslash, dollar sign, double quote, comment, semicolon or line break can occur, and with them none of the string rules that defeat a hand-written lexer; the dot is left out too, because Postgres reads `t.f` as a call of `f(t)` when no such column exists); outside its quoted strings it contains no word from a short stop list (`INTO`, the locking words, the keywords whose value differs per call or per session such as `CURRENT_TIMESTAMP` and `CURRENT_USER`); every name directly followed by `(` is on a small allow-list of functions known to have no side effects and no per-call results; and no string is a time-dependent literal such as `'now'`. Everything else is shared only when the client marks it with the leading comment `/* mg:share */` and the proxy runs with `coalesce: hinted`; the mark is the client's own statement that this is a repeatable read. There is no second, wider "read-only" proof: a statement is treated as a read for the error contract (section 9.1) exactly when it fits the safe shape. A reading mistake outside the safe shape therefore costs a missed share or a more cautious error code, never a wrong result. The stated limit: the shape trusts the objects it names, so a view, a user-defined operator or a row-level security policy that hides a side effect or a per-call result is shared like any other read (the adversary showed the policy case on Postgres 17 in chunk 5's rework); `coalesce: off` and `hinted` exist for databases where that matters. The key is the exact statement text, and its scope is all tenants or one tenant by config. One combination is refused at config load: `lease.reset: on-switch` with a key shared across tenants, because under `on-switch` a connection can still carry its last tenant's session state (a temporary table shadowing a real one), and a shared read run on it would hand that state to another tenant's follower. Two join modes, selectable: **strict** (default) — a request never joins a read that is already running on the database; it joins the next one, which starts when the running one finishes, so its result is never older than its own arrival; **relaxed** — a request joins the running read. The probe shows why this matters: a read already running returned the old value after a concurrent write had been acknowledged, so relaxed mode can break "read your own write" for a moment. Strict costs up to one extra service time of latency; under a 1,000-request burst both modes send 1–2 reads to the database. Followers take no queue slot and no connection. Errors are shared. The waiter set and the buffered reply are capped. A flight runs under its own deadline and is cancelled only when every waiter has left. **Decided (ruling 2):** strict is the default, relaxed is selectable by config, and both are measured — every hot-key and read-your-own-write scenario of chunk 11 and headline benchmark 2 report both modes side by side.

**D11 — Errors are ordinary Postgres error replies with a contract.** Every refusal or failure the proxy itself produces is an `ErrorResponse` with a documented SQLSTATE, followed by the correct end-of-reply marker, and one generated table says for each: was anything sent to the database, and may a write have been applied (section 9.1).

**D12 — Health and stats never touch the data path.** The engine publishes a snapshot copy at a fixed interval; the admin listener (health, stats, metrics) serves that copy from its own goroutines and its own port. It cannot wait on the pool, a queue or the engine.

**D13 — The proxy terminates client TLS; SCRAM-SHA-256 is the authentication mechanism (decided, ruling 5).** A client may open with an `SSLRequest`; the proxy answers `S`, completes a TLS handshake with its own certificate, and then reads the startup message inside TLS. The connection from the proxy to Postgres uses TLS when configured. TLS is a property of the byte stream under the engine: the engine and the cores see the same bytes with or without it. Authentication is SCRAM-SHA-256 on both legs — Postgres's own default — with trust available only as an explicitly named test setting, never a default. Section 3.1 has the verdict, the evidence and the exact scope.

### 3.1 Ruling 5 — client-facing TLS: verdict and evidence

Nick's ruling: include TLS if it is not too hard; SCRAM is the auth default; state the verdict with its evidence.

**Verdict: INCLUDE in v1.** Client-facing TLS termination, and TLS on the database leg as a config choice, cost an estimated 350 lines of product code — under 2% of the build — with no new dependency and no new chunk. It is built as a named part of chunk 4 (the request exchange before startup, as a state machine) and a named part of chunk 6 (the handshake on real sockets, config, certificates, tier R). The evidence for "not hard" is below; nothing in it argues for deferring.

The evidence is a probe written for this ruling, `agent_work/phase1/probes/pgtls/` (output beside it): a TLS-terminating relay on the Go standard library in front of a throwaway Postgres 17 container, driven by the stock `psql` and `pgbench` 16.15 on vm-1, with Go 1.26.8. Together with the first probe (`probes/pgwire/`) and the design in sections 3 and 4:

| # | Finding | Evidence | What it means for the cost |
| --- | --- | --- | --- |
| 1 | **The exchange before startup has to be built anyway.** | libpq's default is `sslmode=prefer`: it sends an `SSLRequest` first. Probe run 3: a proxy answering `N` gets a plaintext retry from a default `psql`, and "server does not support SSL, but SSL was required" from `sslmode=require`. The proposal already listed "TLS and GSS requests answered no" in chunk 4. | TLS adds one branch to an existing state machine (answer `S`, then upgrade), not a new protocol phase. |
| 2 | **The TLS-specific code is small and all standard library.** | The probe's TLS-specific code is 59 lines: the client leg (read the 8-byte request, answer, `tls.Server`, handshake) and the database leg (send `SSLRequest`, read one byte, `tls.Client`). Only `crypto/tls` and `crypto/x509`. | `go.mod` keeps its single requirement. The product version adds config, certificate loading, counters and error rows: about 350 lines in all (breakdown below). |
| 3 | **Stock clients work through it.** | Run 1: `psql` with `sslmode=require`, with `sslmode=verify-full` against the probe's own CA, and with the default `prefer` all connect over TLS 1.3 (`TLS_AES_128_GCM_SHA256`); `sslmode=disable` still connects in plaintext on the same port; `pgbench -S -M simple`, 4 clients × 200 transactions over TLS: 800 of 800 processed, 0 failed. | No client-side work and no protocol surprises in the simple-query subset. |
| 4 | **The database leg is the same few lines.** | Run 2: with the relay speaking TLS to Postgres, `pg_stat_ssl` for the session shows `ssl = true`, `TLSv1.3`. | "Backend leg per config" is a mode switch, not a design. |
| 5 | **TLS sits under the engine.** | Design: the engine's database seam is already a byte transport with two implementations (section 4.2), and the shell owns the sockets (decision D1). In the probe, everything after the handshake is the same relay code for TLS and plaintext connections. | The engine, the cores, the replay law and the simulator's byte streams do not change. Handshakes run on shell goroutines, never on the engine's thread. In the simulator a handshake is an event with an outcome, not cryptography. |
| 6 | **A TLS-terminating proxy must do authentication itself — and this one already does.** | Run 2: when the relay passes Postgres's own SCRAM exchange through, a default `psql` fails with "SCRAM channel binding check failed": Postgres (itself on TLS) offered `SCRAM-SHA-256-PLUS`, and the client bound the exchange to the relay's certificate, not the server's. | No cost: decision D6 has the proxy run SCRAM itself on each leg, so there is nothing to pass through. One concrete requirement for chunk 4's SCRAM server, seen in run 1: a client on TLS that is offered only `SCRAM-SHA-256` sends the GS2 header `y,,` where a plaintext client sends `n,,`. The server must accept both, verify the matching `c=` value (`eSws` or `biws`), and must not advertise the `-PLUS` mechanism. |
| 7 | **Bytes smuggled in before the handshake are refused if the request is read exactly.** | Run 1, last case: an `SSLRequest` and a plaintext startup message sent in one write. The relay reads exactly 8 bytes from the socket with no buffer, answers `S`, and the handshake fails on the leftover bytes ("first record does not look like a TLS handshake"); the connection is closed. This is the class of bug behind CVE-2021-23214 in Postgres itself. | A rule for the shell, with a test: nothing is buffered across the upgrade. In the engine's state machine: any byte after an accepted `SSLRequest` and before the handshake result is a protocol violation. |
| 8 | **Handshake CPU is bounded and off the decision path.** | Probe run 4, 300 full TLS 1.3 handshakes over loopback, client and server halves in one process (so an upper bound for the proxy's half), with the host under load (load average 24): 3.3 ms of CPU each with an ECDSA P-256 certificate, 6.1 ms with RSA 2048. Against real `psql`, handshakes took 3–17 ms of wall time on the same loaded host. | A reconnection storm of 500 TLS clients costs at most about 1.6 CPU-seconds of handshakes with an ECDSA certificate, spread over shell goroutines and admitted through the existing cap on clients in startup (section 9.3). The proxy's SCRAM server side is a few HMACs; the costly key derivation is the client's. The per-statement cost of record encryption is not claimed here; chunk 14 measures it as a TLS-on against TLS-off arm. |

**What v1 includes.**

| Part | v1 |
| --- | --- |
| Client leg | `client_tls.mode`: `off` (answer `N`; the default when no certificate is configured), `allow` (answer `S`; plaintext clients are still accepted), `require` (a plaintext startup message is refused with `FATAL 28000`). `client_tls.cert_file`, `client_tls.key_file`. TLS 1.2 minimum. Config validation fails closed when a mode other than `off` has no readable certificate and key. The tier R setup and the example config run with `require` and a test CA generated at test time. |
| Database leg | `backend_tls.mode`: `disable` (the default), `require` (encrypt, do not verify), `verify-full` (verify against `backend_tls.ca_file` and the server name) — libpq's names for the same behaviours. A database that answers `N` when TLS is required, or fails verification, is a connect failure of its own class and counts toward the breaker. |
| Authentication | SCRAM-SHA-256 on both legs, with the GS2 rule of finding 6. Trust is an explicitly named test setting. No md5. |
| Cancel requests | A cancel arrives on a new connection through the same pre-startup exchange, so it is accepted in plaintext or after an upgrade, as Postgres accepts it. No extra work. |
| Deadlines and caps | One deadline (client startup, 5 s) covers the request exchange, the handshake and authentication; the database connect deadline (2 s) includes the handshake. Handshakes in progress count against the cap on clients in startup. |
| Observability | Wire snapshot: pre-startup requests by kind and answer, handshakes completed and failed by reason, client connections by TLS version, plaintext refused. Database-leg TLS failures appear in connect failures by class. |
| Fleet | Per connection, so it survives at N instances; every instance loads the same certificate. |

**What v1 leaves out, and what a client sees.** Channel binding (`SCRAM-SHA-256-PLUS`): a client set to `channel_binding=require` refuses to connect, by its own check (probe run 1); the default `prefer` works. Client certificates. Direct TLS negotiation without an `SSLRequest` (new in Postgres 17 clients as an option; not the default). Reloading a certificate without a restart — rotation is a rolling restart with drain (chunk 13). GSS encryption (answered `N`). TLS on the admin listener.

**Size and where it lands.** The pre-startup state machine, the GS2 handling and the simulator's handshake events, in chunk 4: about 100 lines. The shell's exact read and upgrade on both legs, about 110 lines; config, certificate loading and validation, about 90; counters, the error-contract and timeout rows, about 50 — in chunk 6: about 250 lines. Chunk 4 grows from ~1,400 to ~1,500 lines and chunk 6 from ~2,200 to ~2,450. It is not a chunk of its own because its two halves live in packages those chunks create, and because building it with them means the first real-socket run of the proxy (chunk 6, tier R) already speaks TLS, instead of plumbing it in later.

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
| 6 | **Session state lives on the connection and outlives the transaction.** | Probe B6: `SET statement_timeout` in one exchange was still in force in a later one; `SET application_name` also triggered an unsolicited `ParameterStatus` message. | That connections are interchangeable by construction. For Postgres they are interchangeable only because the proxy resets session state after every lease (decision D8). |
| 7 | **Sharing a connection between clients is legal only for some requests.** | Probe B4 (safe: three independent queries pipelined) against probe B5 (unsafe: the second query ran inside the first one's transaction). | That requests from different clients may be put on one connection freely. Here it would depend on what each statement is. v1 never does it (batching was dropped, decision D9); the row stays because a generic interface would still have to answer it. |
| 8 | **Sharing a result needs to know what the statement does and when its snapshot was taken.** | Probe B10: a read in flight returned `v=1` after a write of `v=2` had been acknowledged 12 ms after being sent. | That a "key" and a "read" flag come with the request. For SQL both are the output of a recogniser over the statement text (or the client's own mark), and "compatible consistency" is a timing rule. |
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
| **Work vocabulary** (`internal/work`) | Small plain types the cores are written against: tenant id, a unit of work with its class (reject / start a lease / continue a lease / answer locally), an optional share key, and an outcome (connection released clean / still held / dirty; reached the database or not; error class). | The cores are exercised in the simulator by a synthetic workload that produces these types directly, before any Postgres code exists (chunks 7–12 each have such a test). That is a second producer of the vocabulary inside v1. | Import lint: the core packages may not import `pgproto3` or `internal/pgwire`. |
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
| **Fuzz** | Framer and message decoding with arbitrary chunking; the classifier and the safe-shape recogniser; bytes-to-fault-schedule; **engine operations** — from chunk 6 a target whose input decodes to a sequence of ordinary client and engine operations (connect, each class of statement, begin and commit, cancel, disconnect at any point, a deadline firing, database replies in any fragments) mixed with faults, run through the real engine on the simulator; from chunk 7 the same per core, on synthetic work units | No panic, no unbounded allocation, agreement between chunkings; for the operation targets, every registered invariant and the quiesce rule after any operation order (the expected bugs are orderings of ordinary events: a cancel against a coalesced waiter, a commit racing a deadline) | Seed corpus as plain tests in CI; real fuzzing on demand, under the rules of section 6.6 |

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
- **End-to-end with every chunk.** The harness lands with the first runnable system (chunk 6) and every later chunk adds its scenarios in both tiers. The final chunks are never the first end-to-end run. Section 14 has the table. Nick approved this rule on 2026-11-24 (ruling 8: "yes I want e2e incrementally built throughout as a form of integration testing throughout"), and with it the flow-guide wording draft (`/root/briefs/flow-guide-e2e-wording-draft.md`), which Twin applies to the flow guide.

### 6.3 Gates and where they run

- **CI runs fast tests only** (target: under five minutes): build, vet, the lints, unit and property tests with a small fixed budget, the pinned simulator seeds, shell tests with `-race`, fuzz seed corpora as plain tests. No Docker, no sweeps.
- **On-demand scripts** (in `scripts/`, indexed in `agent_work/INDEX.md`): `sweep.sh` (hundreds of seeds per scenario), `fuzz.sh` (timed fuzzing per target), `race-long.sh`, `soak.sh` (long steady-state run), `tier-r.sh` (real Postgres scenarios), `bench.sh`, `csv-assert.sh` (SQL assertions over flushed CSV files with DuckDB, section 6.5), `obs-chain.sh` (the Collector and Prometheus chain check, chunk 15).
- **The local landing gate is the landing authority**, not CI: it runs the chunk's full list including the sweep and tier R, writes a machine-readable result, and fails closed.
- **Lints:** the determinism lint on core packages; an import lint (cores cannot import the wire layer or `pgproto3`; nothing outside the wire layer, the fake and the test clients imports `pgproto3`; `go.mod` has exactly one requirement); a bounded-channel lint for the shell (no unbuffered-forever or unbounded structures without a named cap).
- **Makefile:** `make test` (the CI set), `make chaos` (the named chaos scenarios, tier S, then tier R if Docker is present), `make bench` (section 11). Each exists from chunk 0 and fails with "not built yet" until its chunk lands, so a green target is never a stub. From chunk 6a the Makefile is also the way to run things by hand (section 19.3, item 3): `make run-up`, `make load`, `make report`, `make run-down`.
- **Concise by default.** A large test run prints one line per scenario (seeds run, pass or fail, where its files are) and leaves the detail in the flushed CSV files, which the story report reads (section 19.3, items 1 and 2). `VERBOSE=1` prints the raw test output.

### 6.4 The spec's invariants, restated

| Spec invariant | As tested here |
| --- | --- |
| Hard connection cap | Open + connecting database connections ≤ pool size at every event, in every scenario (a global invariant registered once). |
| At most one in-flight call per key; writes never coalesced; cancel 99 of 100 | Exactly as written; "in-flight" counts reads running on the database. |
| Bounded memory, no goroutine leaks | Every queue, map and buffer has a named cap (the generated capacity table); the quiesce rule; shell leak tests. |
| Noisy-neighbour fairness (> 99% success, p99 within 2×, flooder takes > 95% of shedding) | In simulation: compared exactly against a control run of the quiet tenant alone, same seed. Against real Postgres: the spec's thresholds. |
| CoDel "wait converges to target" | **Restated per mode** (steering answer 3). Timeout-rule mode: every served request waited ≤ the interval; depth is the same for a run of length T and 4T; the database is never idled by shedding. Control-law mode: under moderate overload the sojourn time returns to the target; under heavy open-loop overload it does not converge (the Phase 0 toy showed this) and depth is bounded by the queue's hard capacity instead. The head-to-head table states this plainly. |

### 6.5 Asserting on flushed numbers: CSV and DuckDB

Added by the observability amendment. The exact checks of section 6.2 read the engine's state inside the test process. This section adds the outside view: a run flushes its snapshots to CSV files (section 7.7), and assertions are made on those files, the way an operator would read telemetry. No OpenTelemetry Collector and no Prometheus runs for any of it.

Two levels:

- **Round trip, in Go, in CI.** The scenario harness turns the CSV flush on for every run, in both tiers. At the end of a run it reads the final flush back with `encoding/csv` and requires it to equal the final in-memory snapshot, value for value. One helper does this for every scenario, so every scenario proves on every run that the exported numbers are the real numbers. In tier S the flushed files of two runs with the same seed must be byte-identical; this extends the replay law to the files.
- **Scenario assertions, in SQL, with DuckDB.** Each scenario may ship SQL files under `test/analysis/<scenario>/`. `scripts/csv-assert.sh <run directory> <sql file>` runs them with the DuckDB command-line tool over the flushed files (`read_csv`). A query returns the rows that violate the claim, so zero rows is a pass. The runner fails closed: on any returned row, on a schema version it does not know, on a missing `run.csv` (the run did not finish), on an input with no rows for the metric under test (the "counters must move" rule), and when `duckdb` is not installed. It never skips.

Where each level runs: the round trip runs in CI with everything else. The SQL assertions run in the local landing gate (the landing authority), in `make chaos`, in `make bench` and by hand. CI keeps to the Go toolchain and does not install DuckDB.

DuckDB is a tool run over files after a run. It is not in `go.mod`, no Go package imports it, and the proxy and the simulator work without it. The import lint and the "one requirement" check already enforce this.

**Which flows flush and what they assert.** Every scenario flushes from chunk 6 on; the table names the SQL assertions each chunk adds. The last column gives the PromQL it replaces, where a live Prometheus would otherwise be needed.

| Chunk | Flow | Assertions on the flushed files | Otherwise needs |
| --- | --- | --- | --- |
| 3 | Chunk 2 baseline scenarios, tier S | The flush machinery itself: golden header for schema version 1; byte-identical files for one seed; the round trip. First SQL: the fake Postgres peaked at 500 connections for 500 clients; 1,000 identical reads were 1,000 database reads; workload sent = intended. Self-tests: a planted wrong number, an empty input and an unknown schema version each make the runner fail | — |
| 4, 5 | Wire-level scenarios, tier S | Startup failures by reason = startup faults injected by kind, including the TLS request exchange (requests by kind and answer = requests sent; handshake failures = handshake faults injected). Reject list (the courtesy): classifier rejects = statements sent, and the fake Postgres statement count is 0 | `sum by (reason)` over a scrape |
| 6 | Every walking-skeleton scenario, tiers S and R | Conservation per instance from exported counters: accepted = completed + failed + shed, with queued and in flight at 0 in the final flush. Proxy completed = workload completed (two components, one query). Error-contract counts by row = faults injected. Resets completed + connections closed at release = leases ended, and the session-state crossings counted by the fake are 0 (with the reset switched off by the test-only ablation they are not). First use against the real binary: the proxy runs with `obs.csv.dir` set and the same SQL files run on its files | `increase()` over the run per outcome |
| 7 | Hard cap, reconnection storm, cold start, database restart | Over every flush: pool open + connecting ≤ cap, and the fake's connection peak ≤ cap. Storm: database connections flat while the baseline run (joined by scenario and seed) shows refusals. Dials per interval ≤ the creation limit, from differences between flushes. Recovery: completed-per-interval returns to its pre-fault level after the restart. Headline 1 is the output of a query | `max_over_time`, `rate()`, and two separate runs kept in one Prometheus |
| 8 | Anti-bufferbloat per mode; 10× overload; head-to-head | Queue-wait percentiles computed from the bucket rows, per mode and load. Depth of the run of length T equals depth at 4T. Sheds by reason. **The head-to-head table in the book is one query's output** over the runs of every arm and load | `histogram_quantile` over `rate()` of buckets, per arm |
| 9 | Noisy neighbour; many tenants, one flooder; held transactions | Per-tenant totals: the quiet tenant's completed count equals its count in the run where it is alone (tier S, a join of two runs) or exceeds 99% (tier R). The flooder's share of all shedding > 95%. With 10,000 tenants every tenant has its own rows, which the bounded Prometheus label cannot show | `sum by (tenant)`, and a tenant label that is capped at 32 |
| 10 | Noisy neighbour + backend stall; limit against queue rule | The limit gauge over time: no increase between the stall's start and recovery; decreases by cause; number of direction changes under the stated bound. Rate-gate refusals per tenant = `53M02` count | `changes()`, `delta()` over a window |
| 11 | Hot key; cancel; read-your-own-write | Database reads for the burst: ≤ 2 with the coalescer, 1,000 in the ablation arm (a join of two arms). Flights + hits = shareable reads. Waiters-per-flight totals | A comparison across two runs |
| 12 | Retry storm; no statement applied twice | In every window of the run: retries ≤ 10% of first attempts plus the floor, from differences between flushes. Statements reaching the database bounded against the ablation arm. Writes applied in the fake = distinct writes sent | `increase()` ratios over sliding windows |
| 13 | Three-instance set; drain; steady state | Fleet totals as sums over the three instances' rows: connections ≤ fleet budget, hot-key reads ≤ 3. The SQL sum and bucket merge must equal the output of the `obs` merge function, which checks the merge laws with a second implementation. Size gauges at three points level off. Requests cut mid-answer during drain = 0 | `sum without (instance)`, `histogram_quantile` over merged buckets |
| 14 | `make bench`, `make chaos`, soak | Every benchmark run flushes at each publish interval. The validity gate (achieved rate within 2% of intended), per-arm medians and spread over the interleaved runs, the A/A noise floor, and the three headline tables are SQL over the per-run files (section 11). The "what is slow?" acceptance test is also answered by a query on the files | A Prometheus retaining every run, with a label per run and arm |
| 15 | The production chain, tier R on demand | The flushed files are the reference: what Prometheus holds after the OTLP → Collector → Prometheus chain must equal what DuckDB reads from the CSV files of the same run (section 7.9) | This is the one flow that does run a live Prometheus |

What the files cannot do: an assertion on flushed gauges sees the system at the flush instants only. Bounds that must hold at every event (the connection cap, for one) stay as simulator invariants; the components also export high-water marks so that a sampled view cannot miss a peak.

### 6.6 Rules for simulation-level fuzz targets (Nick's DST fuzz ruling, 2026-11-24)

A fuzz target that runs the simulator is only fuzzing if the fuzzer can explore: it must run fast, and a mutated input must stay close to the run it came from. The evidence is object-store's `FuzzClusterNemesis`, which derived its simulator seed from a hash of the input and ran at 0.26 executions per second, against 324 and 884 per second for that project's other targets (`/root/Software/yt-transcribe/transcripts/yMYBCxrRJyo/dst-analysis.md`). Three rules, binding on every simulation-level target from the first one on:

1. **The seed is never derived from a hash of the input.** A target uses a fixed seed, or reads the seed from a dedicated prefix of the input. Hashing makes every one-byte mutation a different world, which turns coverage guidance into a random seed sweep.
2. **`scripts/fuzz.sh` reports executions per second for every target**, not only pass or fail, and writes the table beside the gate result.
3. **A rate floor: 100 executions per second** (initial; measured by `fuzz.sh` over the target's own run on vm-1). A target below the floor is reported as a *seed sweep*. It still runs and can still fail the gate, but it does not count as fuzzing in any gate item or chapter claim. The floor sits between the two kinds of target object-store measured, with a wide margin on both sides.

Found bugs are kept as explicit shrunk scenarios — an operation list and a fault list a person can read — and not as a pinned seed alone. For that the shrinker lowers values as well as deleting faults (change request CR-1, section 19.1).

---

## 7. Observability architecture

First-class, like testing: it has its own landing-gate item and its own per-chunk obligations.

### 7.1 Three layers, one direction of dependency

**The hard rule (observability amendment, 2026-11-24): one common raw export → the collector interface → adapters.** A component exports its numbers in exactly one way, its `Snapshot()`. The collector is the only reader of snapshots. Every output form is an adapter that reads only what the collector publishes. No component, core or engine knows that CSV, DuckDB, OpenTelemetry or Prometheus exists.

```
components        Snapshot()  — plain structs: integer counters, gauges, fixed-size histograms
    │             the one raw export; nothing else leaves a component
    ▼
collector         assembles one instance snapshot per publish interval and keeps the last copy
    │             hands it to adapters through one interface
    ▼
adapters          Prometheus text │ JSON stats │ CSV flush to disk │ log line │ OTLP/HTTP push
                                                     │                              │
                                                     ▼                              ▼
                                        files  ←  DuckDB (tests, analysis)   OpenTelemetry Collector → Prometheus
```

1. **State in the component.** Each core keeps its counters and gauges as plain integers and fixed-size integer histograms. No telemetry imports. `Snapshot()` returns a copy as a plain struct. This is the common raw export.
2. **One collector.** `internal/obs` walks the components of an instance and assembles one instance snapshot (with the instance id, build and config fingerprint). The engine publishes it at a fixed interval; readers get the last published copy (decision D12). The test-side actors (the fake Postgres, the simulated workload, the load generator) hand their snapshots to a collector in the same way, so their numbers reach the same adapters.
3. **Adapters read only the collector's output.** Elsewhere this plan calls them exporters; the words mean the same thing. All implement one small interface (take an instance snapshot, produce output) and pass one conformance test. The set: Prometheus text; JSON stats; the **CSV flush** (section 7.7), which is also the testing path; a structured one-line log; and, last, the hand-written **OTLP/HTTP push**, the proxy's end of the production chain to Prometheus (section 7.9). Adding an adapter touches no component. All standard library.

The rule is enforced, not only stated: each adapter is its own package under `internal/obs/`, the import lint forbids any core and the engine from importing one, and no telemetry or database library can be imported anywhere (`go.mod` keeps its one requirement). Chunk 15's gate checks that adding the last adapter changed no component file.

Across instances, aggregation belongs to whoever reads the adapters' output: counters and gauges sum, histograms merge, ratios are recomputed from merged counters and never averaged. `internal/obs` ships the merge function, and the multi-instance scenarios use it.

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

Prometheus gets low-cardinality labels only: component, outcome, reason, mode, and a **bounded** tenant label — the configured tenants up to `obs.prom_tenant_max` (default 32), the rest folded into `other`. Never statement text, client address or error text. Full per-tenant detail is in the snapshot, the JSON endpoint and CSV. Exporter label sets for tenants that went quiet are evicted (steady state). The same discipline applies on the OTLP path, since it ends in Prometheus: the OTLP adapter sends the bounded tenant label, never the full tenant set. Histogram buckets are bounded too: the Prometheus text and OTLP adapters re-bucket each histogram to one fixed short list of bounds (`obs.export_buckets`, about 16, from 100 µs to 60 s); the full-resolution buckets are in the snapshot, JSON and CSV.

### 7.5 Coverage

| Area | In v1 | Out, and why |
| --- | --- | --- |
| Integration points | Per side (client, database): bytes, messages, connects, connect failures by class, TLS handshakes by outcome, time waiting on the database per statement | Per-statement-text metrics — unbounded cardinality |
| Throughput | Accepted, completed, failed, rejected, shed — per component and per tenant | |
| Back pressure and queues | Depth, capacity, **age of the oldest item**, queue-wait histogram, rule state (standing queue, dropping), LIFO on/off, sheds by reason | |
| Connections | Open, idle, lent, connecting, creation rate, exhaustion events, closes by reason (idle, lifetime, dirty, error), breaker state and transitions | |
| Contention | Coalescer: flights, hits, waiters per flight, cancellations, overflows. Shell mailbox depth | Lock-wait timing — the engine has no locks; mailbox wait is the opt-in equivalent |
| Fairness | Per tenant: in flight, share, shed share, rate-gate refusals | |
| Retries | Attempts, budget level, budget exhaustions | |
| Status and errors | Counts by error-contract row (SQLSTATE and class), latency histograms per tenant × outcome | |
| Adaptive limit | Current limit, floor, ceiling, increases, decreases by cause | |
| Process | Goroutines, heap in use, open file descriptors against the limit, build and config fingerprint | CPU profiling endpoints — `pprof` is a debug switch, not an exporter |
| Health | Three states: `ok`; `degraded` while shedding, while the breaker is open, or while draining; `failed` only when this instance cannot work at all. A database outage is `degraded`, not `failed`: the proxy is still answering, with fast typed errors. The body names each check and its reason. | |

Each chunk states what its mechanism does to the health mapping.

### 7.6 Landing gate item

A chunk that adds or changes a component does not land without: its `Snapshot()`; its mapping in every exporter that exists at that point, including its rows in the CSV metric registry (section 7.7); a test showing its counters move and conservation holds; a CSV-flush assertion for each of its end-to-end scenarios (section 6.5); its row in the coverage table; its health rule. Acceptance test of the whole (chunk 14): add latency to the database below every timeout and answer "what is slow?" from the stats surface alone; then stop the database and time how long until health says `degraded`.

### 7.7 The CSV flush adapter and its schema (version 1)

The CSV adapter flushes snapshots to files on disk. It has two purposes: evidence tables for scenarios and benchmarks, and a way to capture and check metrics with nothing else running. A test turns the flush on, runs, and asserts on the numbers in the files.

**When it flushes.** At every publish interval when `obs.csv.dir` is set (rows are appended), and once more at the end of a run or on shutdown (the final flush). A test harness can also ask for a flush at a chosen instant. In the simulator the flush is synchronous at a virtual instant. In the real binary the adapter writes from its own goroutine and never blocks the engine.

**One run, one directory, three files.** The directory is `<obs.csv.dir>/<run_id>/`.

`metrics.csv` — one row per scalar value per flush:

| Column | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | `1` |
| `run_id` | text | Set by whoever starts the run. Simulator: `<scenario>/<seed>/<arm>`. `bench.sh`: `<benchmark>/<run number>/<arm>`. A real proxy: the config value `obs.csv.run_id` |
| `flush_seq` | integer | Publish sequence number of the snapshot, from 0. In the simulator all instances publish at the same virtual instants, so equal values mean the same instant |
| `t_ns` | integer | Nanoseconds since the run began on the driver's clock: virtual time in tier S, the monotonic clock in tier R. The adapter stamps it; the snapshot itself still carries no time |
| `instance` | text | Instance id |
| `component` | text | `pool`, `admission`, `coalescer`, `retry`, `wire`, `classifier`, `engine`, `obs`; and from the test side `fakepg`, `workload`, `loadgen`, `simnet` |
| `metric` | text | A name from the metric registry (below) |
| `kind` | text | `counter` (cumulative since start, never decreases) or `gauge` (value at the snapshot) |
| `tenant`, `outcome`, `reason`, `mode` | text | The label columns of section 7.4, always present, empty when a metric does not use one. The tenant column is **not** bounded here: every tenant has its own rows |
| `value` | integer | Integers only. No ratios and no floating point; a reader recomputes ratios from counters |

`histograms.csv` — one row per non-empty bucket per flush: the same columns up to `mode` (without `kind`), then `unit` (`ns`, `bytes` or `count`), `bucket_lo` (inclusive), `bucket_hi` (exclusive) and `count` (cumulative since start). These are the full-resolution buckets of the integer histogram, so percentiles are computed by the reader, and histograms from several instances merge by adding counts per bucket. Clamped out-of-range values appear as counters in `metrics.csv`.

`run.csv` — one row, written last: `schema_version`, `run_id`, `scenario`, `tier` (`S`, `R` or `bench`), `seed`, `arm`, `instances`, `build`, `config_fingerprint`, `flushes`, `duration_ns`. A directory without `run.csv` is a run that did not finish, and assertions on it fail.

**Stability rules.**

- The same columns in the same order in every flush of every run. Rows are sorted by (`flush_seq`, `instance`, `component`, `metric`, labels), so a tier S run writes byte-identical files for the same seed.
- A registered metric that has no tenant label is written in every flush, also when its value is 0, so "nothing was shed" is a row that says 0 and not a missing row. A tenant's rows appear from the tenant's first activity and stay for the rest of the run.
- **Versioning.** `schema_version` starts at 1. Adding a metric adds rows, not columns, and does not change the version. Any change to a column (added, removed, renamed, retyped, or changed in meaning) and any rename or change in meaning of a registered metric raises the version. Every SQL file checks the version before it reads anything else.
- **The metric registry** is `docs/csv-schema.md`: the column tables above and every metric with its component, kind, labels and unit. It is generated from the code and has a test like the other generated contract documents, so a metric cannot be exported without being documented. A golden-header test pins the columns.
- **Bounded.** `obs.csv.max_bytes` caps a run directory. At the cap, or on any write error, a real proxy stops writing, counts it (`csv_flush_errors`, `csv_rows_dropped`) and carries on; the data path is never slowed. A test run treats the same events as a failure.

### 7.8 DuckDB over the flushed files

DuckDB reads the flushed files directly with `read_csv` so that tests and ad-hoc analysis can run real SQL over the metrics. It is an analysis-side tool and never a runtime dependency: the proxy does not import it, link it or start it (section 6.5 has the enforcement and says where the assertions run).

- **Shape.** `test/analysis/views.sql` defines the views `metrics`, `hist`, `runs` and `final` (the last flush of each run) over a directory of runs, with the column types written out instead of detected. DuckDB reads an empty field as NULL, so the views turn empty label columns back into the empty string. A glob over several run directories puts many runs, arms and seeds in one table, which is what makes comparisons between runs a join.
- **Version.** Nick named DuckDB 2.0. vm-1 has the DuckDB command-line tool 1.5.5 today. Chunk 3 pins the version the scripts require and the gate records `duckdb --version` beside its result. The SQL uses only `read_csv` with explicit columns, aggregates and window functions.
- **Checked on vm-1, 2026-11-24:** a sample `metrics.csv` and `histograms.csv` in this schema were read with DuckDB 1.5.5, and the two queries below returned the expected answers.

```sql
-- The flooder (tenant A) must take more than 95% of all shedding. Rows returned are violations.
SELECT run_id, sum(value) FILTER (WHERE tenant = 'A') AS shed_a, sum(value) AS shed_all
FROM final
WHERE component = 'admission' AND metric = 'requests' AND outcome = 'shed'
GROUP BY run_id
HAVING sum(value) = 0 OR 100 * sum(value) FILTER (WHERE tenant = 'A') <= 95 * sum(value);

-- Upper bound of the quiet tenant's p99 queue wait, from the bucket rows, merged over instances.
WITH b AS (SELECT run_id, bucket_hi, sum(count) AS n FROM hist
           WHERE metric = 'queue_wait' AND tenant = 'B' AND flush_seq = (SELECT max(flush_seq) FROM hist)
           GROUP BY ALL),
     c AS (SELECT *, sum(n) OVER (PARTITION BY run_id ORDER BY bucket_hi) AS cum,
                     sum(n) OVER (PARTITION BY run_id) AS total FROM b)
SELECT run_id, min(bucket_hi) AS p99_upper_ns FROM c WHERE 100 * cum >= 99 * total GROUP BY run_id;
```

**What this makes checkable with no Prometheus running** (the per-chunk list is the table in section 6.5):

| Assertion | With a live Prometheus it would be | From the files |
| --- | --- | --- |
| Shed counts by tenant and reason after a scenario | `sum by (tenant, reason) (increase(…[run]))`, limited to the 32 labelled tenants | A grouped sum over the final flush, for every tenant |
| Queue-wait and latency percentiles | `histogram_quantile` over about 16 buckets, interpolated | The same computation over the full-resolution bucket rows; exact bucket bounds |
| Per-tenant totals and conservation (accepted = completed + failed + shed + queued + in flight) | Several queries compared by hand | One query; a returned row is a violation |
| Fleet totals at N instances | `sum without (instance)` | A sum over instances, compared with the `obs` merge function's output |
| Behaviour over time: recovery after a fault, dial rate, limit not rising during a stall, retry ratio per window | `rate()`, `delta()`, `changes()`, `max_over_time` over a retained series | Window functions over `flush_seq` |
| Mechanism on against off; proxy against the direct baseline; one seed against another | Two runs retained side by side with labels to tell them apart | A join of two `run_id`s |
| Benchmark verdicts: medians, spread, A/A noise floor over 10 interleaved runs per arm | Not a natural fit | `median` and quantiles grouped by arm |

### 7.9 The production chain: OTLP → OpenTelemetry Collector → Prometheus

The final adapter is the production path, end to end:

```
component Snapshot() → collector → OTLP adapter ──OTLP/HTTP JSON push──► OpenTelemetry Collector ──► Prometheus
   (plain structs)    (internal/obs)  (internal/obs/otlp, stdlib)          otlp receiver (:4318)         scrapes the Collector's
                                       bounded queue, drop-and-count        batch processor               Prometheus endpoint
                                                                            prometheus exporter (:8889)   (:8889); UI on :9090
```

- **Proxy side.** The hand-written OTLP/HTTP push adapter of steering answer 13: standard library only, no OpenTelemetry SDK. It reads the collector's published snapshot like every other adapter and posts to `obs.otlp.endpoint` (for example `http://127.0.0.1:4318/v1/metrics`) every `obs.otlp.interval` (initial value 10 s). Counters go out as cumulative monotonic sums, which is what the snapshot already holds; gauges as gauges; histograms as explicit-bucket histograms on the short bound list of section 7.4. The instance id is the `service.instance.id` resource attribute. OTLP needs wall-clock timestamps; the adapter stamps them at push time, and the snapshot still carries none.
- **Collector.** Receives OTLP over HTTP, batches, and exposes the metrics on a Prometheus endpoint, with resource attributes turned into labels so that `instance` survives. The fleet view is then a sum over `instance` in Prometheus, the same merge rule as everywhere else.
- **Prometheus.** Scrapes the Collector. The Collector exposing an endpoint for Prometheus to scrape is chosen over the Collector writing to Prometheus by remote write because it needs no extra Prometheus flags; either satisfies the chain.
- **Cardinality.** Unchanged from section 7.4: Prometheus sees component, outcome, reason, mode and the bounded tenant label. Per-tenant detail stays in the snapshot, JSON and CSV.
- **Runnable, not only drawn.** Chunk 15 ships a docker-compose dev setup for the Collector and Prometheus leg (`deploy/obs-dev/`, started with `make obs-up`), and a check that runs a scenario through the whole chain and compares Prometheus with the CSV flush of the same run. Details are in chunk 15.
- **The Prometheus text endpoint stays** (`/metrics` on the admin port) for a direct scrape, for `curl`, and for setups with no Collector. The two routes carry the same metric names and labels, checked by a test, and one Prometheus should use one of them, not both.

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
| Coalescing safety, error contract, TLS | **Survive** | Local to a connection or an instance; every instance loads the same certificate | — |
| Metrics | Per instance; merged outside | Section 7.1 | Merge test |
| Loss of an instance | Survivors receive its clients as a reconnection burst | Chain reaction (*Release It!*) | One of three killed at 80% load: survivors shed, do not queue; the fleet connection bound holds |

The weakened and conditional rows are measured exactly in the simulator (three engines on one event loop cost almost nothing, because they are sans-I/O) and once against real Postgres with three processes.

### 8.3 Graceful drain

On SIGTERM an instance: reports `degraded (draining)` on health; stops accepting new client connections; answers new work from idle clients with the drain error (pre-forward, safe to retry elsewhere); lets transactions in progress finish, bounded by the statement deadline plus one second; closes idle database connections as they free up; exits. Health and stats stay answerable throughout. Every component has a `draining` input and its own refusal class from its first chunk; chunk 13 only sequences them. Failure accounting for a restart has three classes — refused fast, timed out, cut mid-answer — and the target for the third is zero.

---

## 9. Contracts: errors, timeouts, capacities, the reject list

Three of these are **generated from code by a test and held by that test**, so the document cannot drift from the behaviour: `docs/error-contract.md`, `docs/timeout-budget.md`, `docs/capacity-table.md`. They start in chunk 5 or 6 and gain rows with each chunk.

### 9.1 Error contract (initial table; codes are fixed in chunk 5)

"Pre-forward" means no byte of the request reached the database. Private SQLSTATE subclasses (`M..`) sit inside standard classes so drivers map them sensibly.

| Situation | Reply | Reached the database? | May a write have been applied? | Safe to retry? |
| --- | --- | --- | --- | --- |
| Shed by the queue rule, or queue full | `ERROR 53M01`, connection stays usable | No | No | Yes, after backing off |
| Refused by the rate gate | `ERROR 53M02` | No | No | Yes, later |
| Database marked down (breaker open) | `ERROR 53M03` | No | No | Yes, later |
| Coalescer waiter cap or shared-reply cap exceeded | `ERROR 53M04` | No (for this caller) | No | Yes |
| Instance draining | `ERROR 53M05`, then the connection is closed when idle | No | No | Yes, on another instance |
| Plaintext startup message while `client_tls.mode` is `require` | `FATAL 28000`, client connection closed | No | No | No — set the client's `sslmode` |
| TLS handshake failed or timed out | Client connection closed; no Postgres message can be sent | No | No | Caller's choice |
| Feature on the reject list, where the courtesy check sees it (section 9.4); extended-protocol message | `ERROR 0A000` (feature not supported) | No | No | No — change the client |
| Cancelled while queued | `ERROR 57014` | No | No | Caller's choice |
| Database connection lost or reply timed out after a statement that fits the safe shape of decision D10 was sent, outside a transaction | `ERROR 08006`, connection stays usable | Yes | No, as recognised | Yes |
| Same, for every other statement (it could write, or was `COMMIT`) | `ERROR 08007` (transaction resolution unknown) | Yes | **Yes — unknown** | Not without checking |
| Database connection lost while the client was inside a transaction, between statements | `FATAL 08006`, client connection closed | Yes | No — the transaction cannot have committed | Yes, the whole transaction |
| Transaction left idle past the deadline | Rolled back on the database; `FATAL 25P03`, client connection closed | Yes | No | Yes, the whole transaction |
| Reply already partly sent to the client when the database connection failed | Client connection closed — never a short reply that looks complete | Yes | Per the rows above | Per the rows above |
| An error from Postgres itself | Passed through unchanged | Yes | As Postgres says | As Postgres says |

A failed reset after a lease is not a row: the client already has its reply, and the database connection is closed and counted.

The proxy's own retry (chunk 12) may act only on a failure where the "reached the database" column is "No" for certain — for example a lent connection found dead before the first byte was written.

### 9.2 Timeout budget (initial values, each marked "initial" and tuned by a named experiment)

Every wait is bounded and an inner wait always ends before the outer one. Config validation asserts the nesting.

| Wait | Initial | Ends with |
| --- | --- | --- |
| Client startup: the TLS request exchange, the TLS handshake and authentication, together | 5 s | Connection closed |
| Client mid-message read (a partial message) | 5 s | Connection closed |
| Client idle between transactions | 10 min | Connection closed |
| Client idle inside a transaction | 5 s | Rollback, `25P03` |
| Queue wait | Set by the queue rule (interval 100 ms, target 5 ms; both configurable and to be reasoned against database service time) | `53M01` |
| Waiting for a connection | None separate — it is the queue wait (D3) | — |
| Database connect, including the TLS handshake when configured, and authentication | 2 s | Counts toward the breaker |
| Database statement (send to end of reply) | 30 s | Cancel, then the dirty-connection table; `08006`/`08007` |
| Coalesced flight | Same as the statement deadline; outlives any single waiter | Shared error |
| Cancel and clean-up of a dirty connection | 1 s | Connection closed |
| Reset after a lease (`DISCARD ALL`, and the re-seed when it is on) | 1 s | Connection closed |
| Client write stalled | 5 s | Client dropped; dirty-connection table |
| Drain | Statement deadline + 1 s | Exit |

### 9.3 Capacity table

One row per bounded thing: what it protects, its capacity and config key, what happens when it is full, its deadline, its gauge. Initial rows: client connections; clients in startup (a separate cap, Odyssey's idea — it is the client-side half of the reconnection storm); database connections; connections being opened; per-tenant queue; total queued; coalescer flights; waiters per flight; shared-reply bytes; per-client reply buffer; shell mailboxes; active-tenant states; concurrent cancels. A parse-time check asserts the gates in front of the pool sum to something the pool can serve.

### 9.4 Reject list — a best-effort courtesy (ruling 9; derived from RDS Proxy's pinning triggers, I27, and PgBouncer's and Hyperdrive's transaction-mode tables)

Refused with `0A000` before anything is sent, **where the classifier sees them**: session `SET` and `RESET` (but `SET LOCAL` inside a transaction is allowed); `PREPARE`, `EXECUTE`, `DEALLOCATE`; `LISTEN`, `UNLISTEN`; `DECLARE … WITH HOLD`; temporary tables; session advisory-lock functions; `DISCARD`; `LOAD`; `COPY`; two-phase commit. Refused always, because the decision needs no SQL: every extended-protocol message (its type byte) and startup parameters that differ from the pool's fixed session settings (other than `application_name`, which is accepted and ignored).

What the list is for: a client that uses a session feature gets an explicit error naming it, instead of a feature that works until the transaction ends and is then silently gone. What it is not: a safety mechanism. The classifier is a lexer over SQL text, not a parser, and three review rounds on the first build of chunk 5 found eleven ways to spell a session change that it missed (`agent_work/chunks/c05-classifier/SCOPE-ASSESSMENT.md`); a survey of 19 other projects found none that decides this completely from text (`CANVAS.md`). So no guarantee rests on the list. A session feature the list misses — another spelling, or a function or view that hides it — is forwarded, works for the rest of its transaction, and is removed by the reset that ends every lease (decision D8). A missed spelling is a gap in the courtesy, recorded and fixed when cheap; it is not a defect in session safety. When the classifier cannot tell, a statement is forwarded and never shared.

---

---

## 10. *Release It!* mapping

Source: Nick's notes at `/root/Software/book-notes/books/release-it/` — a teaching project ("litefleet": 18 planted flaws fixed over 12 lessons, plus 8 review procedures), read in full for the lessons and procedures. **Limits:** the notes are secondhand; the book itself was not read in Phase 1; the knowledge store has no *Release It!* entry (ten searches, all recorded as misses). The notes do not contain the book's material on pooled connections going dead silently (firewall idle timeouts, half-open sockets). **Decided (ruling 6):** the book is consulted for exactly that before the pool chunk builds. The PDF is on vm-1 at `/root/briefs/release-it-book.pdf`; the mining step, its output and its place in the order are in chunk 7. Full mining report with citations: `agent_work/phase1/research/release-it-mining.md`.

The notes' rule for a pattern: it is four decisions — where it sits, what numbers it needs, what the caller gets when it trips, which signal shows it tripped. Every protective mechanism in a chunk brief is written as that four-part row.

### 10.1 Patterns: adopted, adapted, rejected

| Pattern | Verdict | Where it lands |
| --- | --- | --- |
| Timeouts | **Adopt.** One generated budget table, inner before outer, checked at config load. "First-byte deadline ≥ longest legitimate work." Cancellation must be real: measure how late work stops after its deadline. | Section 9.2; starts in chunk 6, grows in 7, 8, 11, 13 |
| Fail fast | **Adopt.** Fixed order per request: parse and classify → reject list → draining → breaker → rate gate → coalescer join → queue → lease. Each refusal has its own error and counter. | Chunks 5, 6, 7, 8, 10 |
| Circuit breaker | **Adapt.** Exactly one: the database-down state on the pool, with a connect lockout. Only connection-class failures count; SQL errors never do. Half-open admits one trial. "A breaker without a fallback just fails faster" is accepted on purpose: with no cache, the fast pre-forward error is the fallback. No per-tenant or per-statement breakers. | Chunk 7 |
| Bulkheads | **Adapt.** Per tenant, soft: a share that binds under saturation. The partition unit is the tenant from the first queue chunk (the notes found the unit was the decisive choice). It bounds database concurrency, not database CPU, and the book says so. **Adopt hard** for one thing: health and stats never draw on the data path (D12). | Chunks 6 (health path), 8, 9 |
| Shed load | **Adopt.** Already the core. Added from the notes: measure the cost of a refusal; health and metrics are never shed. | Chunks 8–10, 14 |
| Handshaking | **Adapt.** Postgres has no `Retry-After`; the handshake is a distinct SQLSTATE per refusal plus three-state health. | Chunks 5, 6 |
| Create back pressure | **Adopt.** One request per client inside the proxy; unread requests stay in the socket; bounded reply buffers (D5). | Chunk 6 |
| Steady state | **Adopt.** Every chunk brief has a row: what grows here, what removes it, which gauge shows the level. A tenant-churn soak asserts every size gauge levels off (three-point growth method). | Every chunk; the soak in chunk 13 |
| Governor | **Adopt.** Floor, ceiling, maximum step and minimum interval on the adaptive limit; the retry budget; the creation-rate cap; hysteresis on the LIFO switch. A test drives each with a pathological signal. | Chunks 7, 8, 10, 12 |
| Test harness | **Adopt.** It is Commit 0. Taken from the notes: the assertion shape (error class + elapsed time within budget + bounded resources), the harness self-test, and the fault catalog below. | Chunks 1, 2 |
| Let it crash | **Reject as a mechanism; keep as a scenario and as "destroy on doubt".** The proxy holds no durable state. | Chunk 13 (instance kill); D7 |
| Decoupling middleware | **Reject.** Accept-now-deliver-later contradicts honest "may have been applied" errors. | — |
| Transparency | **Adopt** as section 7. | Chunk 3 and every chunk |
| Graceful drain | **Adopt.** Section 8.3. | Reserved from chunk 6; sequenced in chunk 13 |

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

Database side: refuses connections; answers `N` to a TLS request, or presents a certificate that fails verification, when the proxy requires TLS; accepts and never answers (at startup; after a query — the second is "a byte reached the database"); connect stalls; slow first byte (the spec's 150–200 ms stall); slow trickle of rows; fragmented delivery down to one byte, including inside a message header; reset before, during and after a reply and mid-transaction; garbage bytes and impossible lengths; a non-Postgres reply; a reply that ends without its end marker; very large rows and very many rows; unexpected state at checkout; unexpected `ParameterStatus`; killed (all connections reset at once), frozen (sockets open, nothing moves), restarted cold; `max_connections` reached; latency sitting exactly at a timeout boundary; some connections slow and others fine; half-open (peer gone, no reset seen); asymmetric latency. Client side: sends a TLS request and then stalls; fails the TLS handshake; sends plaintext bytes after a TLS request; sends a second TLS request after the upgrade; sends half a message and stops; stops reading replies; disconnects while queued, mid-statement, mid-transaction, while a coalesced waiter; reconnects in a burst; retries aggressively; sends a cancel. Load: open-loop arrival above capacity.

---

## 11. Benchmarking: "Apollo-lite"

Apollo's post describes three gates (is the run valid, do repeats agree, did the result move by more than a margin declared in advance) on dedicated hardware with hundreds of runs. This project runs on one shared 4-vCPU VM with no hardware performance counters. So:

**What we do.**

- The **simulator is the primary evidence** for every claim about counts and ordering: connections opened, reads reaching the database, who was shed. These are exact and replayable.
- Real-hardware runs use an **open-loop** load generator (`cmd/loadgen`, built in chunk 6a as an exploration tool and completed for benchmarking in chunk 14; one tool for both uses): each request has an intended send time; latency is measured from that time; the generator never waits for a reply before sending the next. Achieved rate against intended rate is reported and gates the run (within 2%, or the run is invalid and discarded, not averaged). The reconnection storm is closed-loop by nature and is labelled so.
- An **A/A run** (the same binary as both arms) publishes the noise floor for each benchmark.
- **At least 10 runs per arm, interleaved**; a fresh proxy process and fresh database state per run; warm-up discarded; medians with spread; percentiles as the median of windows with the worst window shown; host load recorded beside every number; runs serialised under the machine-wide lock with builders idle.
- Margins and verdicts are **declared before the run**; "**inconclusive**" is an allowed verdict.
- **Calibration** first: measure the real backend's capacity open-loop, write it to a file, and express every scenario as a fraction of it.
- **Headlines are exact counts**, with latency percentiles second: database connections under 500 clients (measured, stated as clients ÷ pool — the spec's "19×" and "~15×" are labelled as a model, not our result); database reads per hot-key burst with and without coalescing; shed attribution by tenant; goodput. Then the quiet tenant's p99 under a noisy neighbour, unprotected FIFO against the full admission stack, in both CoDel modes.
- Every run flushes its snapshots (proxy and load generator) to CSV at each publish interval (section 7.7). The report — the validity gate, medians and spread per arm, the A/A noise floor, the headline tables — is a set of SQL files run by DuckDB over the per-run files (sections 6.5 and 7.8). This replaces the in-repo median/spread script the first draft of this plan proposed. DuckDB is a tool the benchmark scripts need, like Docker; it is not a dependency of the Go module. Results are also emitted in Go benchmark text format so `benchstat` can read them.
- **TLS on against TLS off** is one arm pair of the connection benchmark and of the reconnection storm (section 3.1, finding 8): the cost per statement and per reconnect is reported, with the A/A floor beside it.
- The scripts ship in the repository so a reader can re-run them on real hardware.

**Deliverable Nick added (steering answer 7): the trade-offs write-up.** A book chapter, "How much to trust these numbers" (source `book/chapters/…-benchmark-trade-offs.md`, Neon's prose from evidence gathered in chunk 14; also linked from the README). It must cover:

| Part | Content |
| --- | --- |
| What Apollo-lite keeps | Run-validity gate; repeats with spread; pre-declared margins; "inconclusive"; seeded backend; A/A noise floor; open-loop generation |
| What it gives up | Dedicated and separate machines for generator and system; hundreds of runs; distribution-distance statistics; formal confidence intervals; hardware counters; isolation from a noisy host |
| What that costs | Effects of a few percent cannot be claimed at all; latency percentiles carry the VM's noise; generator and proxy compete for four cores |
| Confidence per headline | A table, one row per headline number: its kind (exact count from the simulator / exact count from real Postgres / latency ratio on a shared VM), what would have to be true for it to be wrong, and the confidence it deserves in words. Expected shape: connection counts and database-read counts — high (exact, replayable, order-of-magnitude effects); shed attribution — high in the simulator, medium on the VM; p99 ratios — medium at best, reported with the A/A floor beside them; absolute latencies and throughput — low, machine-specific, not headlines |
| What would raise confidence | Separate hosts, more runs, a pinned CPU set — listed as how a reader can do better |

---

## 12. Build flow and the landing gate

### 12.1 Seats (from the seat ruling, as confirmed by ruling 1)

| Seat | Role |
| --- | --- |
| **Fable** (orchestrator) | Writes each chunk's brief as a file, dispatches, runs the gates and the adversarial harnesses, final acceptance, lands (merge to main and push), keeps STATE.md, `agent_work/INDEX.md` and the C4 set current. Builds chunk 0 (scripts, CI, site — no product logic). |
| **Opus** | Writes the Go product code and its tests — logic only, no comments or prose. A fresh agent in its own worktree per chunk; chunks in the same wave run concurrently. |
| **Neon** (via Antigravity `agy`; OpenCode/OpenRouter only as a recorded fallback) | **Reviews Opus's work** — every chunk, verdict PASS / NITS / FAIL, by a fresh agent. Decided by ruling 1: on this project the seat ruling governs over the global rule that only Fable reviews Opus builds; it is a project-scoped exception and the global rule is unchanged elsewhere. Neon also writes **all** reader-facing prose — the book chapter, code comments, README, site copy, C4 explanatory text — once, late, per chunk, from the final verified evidence including every narrowed claim, **using the Clarity skill** (section 19.4). Prose from any other seat is a draft for Neon to rewrite with that skill, not a finished write-up. The review and the prose pass are separate fresh agents. |
| **Astra** (`gpt-6-astra`) | Writes adversarial harnesses that try to break each chunk's stated claims; Fable runs them and records held / broken counts. |
| Arbitration | A second FAIL, or a builder/reviewer standoff, goes to a top seat outside the build/review pair with the diff and both verdicts. |

Before each wave the orchestrator checks the usage gauges of every pool it is about to draw on, and does not dispatch into a pool at its limit.

### 12.2 Life of a chunk

1. Fable writes the brief (purpose, deliverables, claims to be attacked, tests, snapshot obligations, the four-part row per mechanism, the steady-state row, files it may touch).
2. A fresh Opus agent builds in its own worktree, tests first, with one planted-bug canary per chunk. Headless; exit file and log under `/root/runs/`.
3. Review by a fresh Neon agent through `agy` (verdict PASS / NITS / FAIL). In parallel, a fresh Astra agent writes the adversarial harness from the brief's claims, not from the code.
4. One repair round by a fresh Opus agent, then re-review by Neon. A second FAIL goes to arbitration.
5. Fable runs the local landing gate and the adversarial harness; claims that broke are fixed or narrowed in writing.
6. Neon's prose pass, once, late, with the final evidence, using the Clarity skill (section 19.4). Where another seat has already drafted reader-facing text for the chunk, Neon rewrites that draft in the same pass.
7. Fable checks the C4 set against the code, lands, verifies the chapter is live on Pages, updates STATE.md.
8. The agent that did the work records durable knowledge in `docs/knowledge/` and scripts in `agent_work/`, indexed.
9. **Promotion (section 19.3, item 4).** Test material built under `agent_work/` that is worth keeping does not stay there: in the repair round, or in a pass before landing, the adversary's tests that held and still describe the product move into the shipped suite (beside the package they test, or under `test/`), and check scripts move under `scripts/` or `test/`. `agent_work/` keeps the records: briefs, notes, results. Chunk 5's rework is the first to do this; chunk 6a does it once for chunks 1–4.

### 12.3 Landing gate — every chunk

A chunk lands only when all of these hold:

1. Its tests pass in the local gate: unit and property tests, its simulator scenarios on the pinned seeds and an on-demand sweep, shell tests with `-race` where it touches the shell, tier R where it has a real-Postgres scenario. The gate's result file says so; the gate fails closed.
2. **End-to-end:** its scenarios from section 14 exist in tier S (and tier R where listed), each with a planted-fault canary, the ablation run, the recovery assertion and the quiesce check.
3. **Observability:** section 7.6.
4. The replay law holds for every scenario it adds.
5. Lints pass (determinism, imports, bounds); `go.mod` still has one requirement.
6. Generated contract documents are regenerated and their tests pass.
7. Neon's review verdict PASS, or NITS closed.
8. Adversarial results recorded: held and broken counts; every break fixed or the claim narrowed in writing.
9. The *Release It!* checklist (section 10.3) answered by the reviewer (Neon); each mechanism has its four-part row and its steady-state row.
10. The multiplies / weakens / survives row for anything it adds (section 8.2).
11. C4 diagrams match the code (a stale diagram is a defect).
12. Neon's chapter is live on Pages and listed in the book's contents.
13. STATE.md and `agent_work/INDEX.md` are current.

---

## 13. The chunks

Sixteen chunks after the foundation (ruling 7: size approved as amended), plus chunk 6a added for the load-tool and developer-experience rulings (section 19), each sized for one fresh builder. Sizes are rough product-code estimates, excluding tests; chunks marked ×2 add a mechanism and are budgeted at up to twice the estimate (section 10.2, item 12).

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
   ├── Chunk 6a Operator tools             ┐
   ├── Chunk 7  Pool                       ├ wave B (6a touches no engine file)
   └── Chunk 8  Admission queues           ┘
   │
   ├── Chunk 9   Fairness                  ┐
   ├── Chunk 11  Coalescer                 ├ wave C
   └── Chunk 12  Retry policy              ┘
   │
Chunk 10  Adaptive limit and rate gate   (after chunk 9: it sets the limit the dispatcher enforces)
   │
Chunk 13  Fleet configuration and graceful drain
Chunk 14  Benchmarks and the trade-offs write-up
Chunk 15  OTLP push exporter   (last build chunk)
Chunk 16  Book finish          (assembly and polish only)
```

One step sits beside the chunks: the *Release It!* mining note (ruling 6) is written before chunk 7's brief, while wave A and chunk 6 build. It is described in chunk 7.

Chunks in a wave build concurrently in separate worktrees and land one at a time; the engine's stage seams (set in chunk 6) keep their edits to `internal/proxy` small and separate. The `internal/sim` API is frozen after chunk 2; later chunks change it only through a change request to the orchestrator.

For every chunk below, "Seats" is as in section 12.1 unless stated (build Opus; review Neon; adversarial Astra; prose Neon; gate, C4 and landing Fable), and "Landing" means the common gate of section 12.3 plus the chunk's own criteria.

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

**Purpose.** The path from a component's numbers to something a person, a test or a scraper can read, built before the components so each one plugs in on arrival. It also builds the testing path of the observability amendment: flush to CSV, assert on the files.

**Deliverables.** `internal/obs`: the collector (instance snapshot assembly, publish interval, last-copy reads); the adapter interface every output implements; the merge function across instances; adapters — JSON stats, the **CSV flush adapter** with schema version 1 (section 7.7: the three files, flush per interval and final flush, sorted rows, the size cap, drop-and-count), one-line structured log, Prometheus text with the bounded tenant label, the short export bucket list and label eviction; the three-state health type and its rule table; the `obs.detail` gate. The cost-model benchmark and the zero-allocation gate. An adapter conformance test any future adapter must pass. `docs/csv-schema.md`, generated, with its test. **The analysis side (section 7.8):** `test/analysis/views.sql`, `scripts/csv-assert.sh` with its fail-closed rules and the pinned DuckDB version, the Go round-trip helper the scenario harness will call for every run, and the first SQL assertion files, for the chunk 2 baseline.

**Tests.** Golden output per adapter; Prometheus text parses under the exposition grammar (a small in-repo checker); merge laws (sum, histogram merge, ratios recomputed); label bound holds with 10,000 tenants; snapshots contain no time and no map order (run-twice byte equality). CSV: golden header; the same seed gives byte-identical files; the final flush read back equals the snapshot; zero-valued metrics are present as rows; the size cap and a write error are counted and do not block. e2e for this chunk: the chunk 2 baseline scenarios exported through every adapter, and the baseline's counts asserted from the flushed files with DuckDB (500 clients, 500 database connections at peak; 1,000 identical reads, 1,000 database reads; sent = intended). Self-tests of the assertion runner: a planted wrong number, an input with no rows, a missing `run.csv` and an unknown schema version each fail it.

**Observability.** This chunk is the obligation's machinery; it adds the collector's own counters (publishes, export errors, CSV flushes, flush errors, rows dropped).

**Landing.** Cost numbers recorded for section 7.3; conformance test in place; schema version 1 and the metric registry published; the import lint shows no core or engine package importing an adapter. Size: ~1,400 lines.

---

### Chunk 4 — Wire sessions

**Purpose.** Speaking Postgres on both sides: taking a client through startup and login, and logging the proxy in to the database, as pure state machines that cope with bytes arriving in any fragments.

**Deliverables.** `internal/pgwire`: the incremental framer (with a maximum message size); the client-facing session machine — **the exchange before startup** (an `SSLRequest` answered `S` or `N` by `client_tls.mode`, a GSS encryption request answered `N`, at most one upgrade, any byte between an accepted `SSLRequest` and the handshake result a violation, a "start TLS" action out and a handshake-result event in; section 3.1), startup message, refusal of a plaintext startup when TLS is required, tenant lookup by user, SCRAM-SHA-256 server side accepting the GS2 headers `n,,` and `y,,` and never offering `-PLUS`, trust as a named test setting, the parameter-status set, proxy-issued key data, cancel-request recognition, termination; the database-facing session machine — the proxy's own `SSLRequest` when `backend_tls.mode` asks for one (an `N` answer is a connect failure), startup, SCRAM client side, collecting server parameters, key data, ready; SCRAM as pure functions shared with the simulator's fake and test clients; the query-cycle tracker that follows a reply to its end marker and reports the status byte, unsolicited messages and protocol surprises. Unknown but well-framed message types on the database side are passed through, not rejected.

**Tests.** Golden bytes for every message we produce; every-chunking tests (a conversation split at every byte boundary gives the same events); fuzzing of the framer and both machines (no panic, bounded memory); SCRAM against the RFC 7677 test vector and against real Postgres (tier R); wrong password, unknown user, wrong database give the right fatal errors. The exchange before startup, for every `client_tls.mode`: a default-style client (TLS request first), a plaintext client, a GSS request then a TLS request, a second TLS request after the upgrade, bytes sent early, a handshake that fails and one that never finishes; SCRAM with each GS2 header. e2e for this chunk, in the simulator: a simulated client completes startup through the two machines wired back-to-back to the fake Postgres, under one-byte delivery and under each startup-phase fault (accept and silence, reset mid-handshake, garbage), each ending in the expected error class within its deadline. In the simulator a TLS handshake is an event with an outcome (completed, failed, stalled), not cryptography; real `crypto/tls` runs first in chunk 6. CSV-flush assertion: startup failures by reason in the wire snapshot equal the startup faults injected by kind, and TLS requests by kind and answer equal the requests sent.

**Observability.** Wire snapshot: startups begun, completed and failed by reason; pre-startup requests by kind and answer; TLS handshakes completed and failed by reason; plaintext refused; authentication failures; messages and bytes by direction; protocol violations; oversized messages.

**Landing.** Fidelity against Postgres 17 for startup, including its answers to a TLS request with `ssl` on and off; determinism lint passes on the machines. Size: ~1,500 lines (about 100 of them the TLS exchange), ×2.

---

### Chunk 5 — Statement classifier and the contracts

Re-opened by ruling 9 (2026-11-26) after being parked on 2026-11-25. Its first build is on the branch `work/c05-classifier` (tip `d39ecfb`); the rework starts from that code and keeps its tests wherever the behaviour they test survives (Nick's test direction of 2026-11-26).

**Purpose.** Three jobs that need no trust in reading SQL, and two that read it in ways that cannot fail unsafe: the vocabulary the cores are written against; the generated, tested table of what the proxy tells a client when it refuses or fails; the builders for those replies; the reject list as a courtesy (section 9.4); and the recogniser of shareable reads (the safe shape of decision D10).

**Deliverables.** `internal/work`: the vocabulary types. In `internal/pgwire`: the error table as code, with the generator and test for `docs/error-contract.md`; builders for every proxy-made reply (error plus the correct end marker; the discard-until-sync rule for extended-protocol messages); the SQL lexer and the reject classifier kept as the courtesy check, with the generated `docs/reject-list.md`; the **safe-shape recogniser**, which does not use the lexer, with the function allow-list and the stop list as one table in code; the `/* mg:share */` mark reported as a flag; the share key (exact statement text, with the tenant scope hook for chunk 11). Removed from the first build: the lexer-based "read-only-provable" and "coalescable" proofs (replaced by the safe shape) and the "may leave a transaction open" flag (the status byte decides, decision D4). Joined to what landed since: the classifier's scenarios flush through the collector and their SQL assertions run in the gate.

**Tests.** The first build's tests are kept, hoisted or retired by one rule: a test stays when the behaviour it tests still exists; the adversary's tests that still hold move into the shipped suite; only tests of removed behaviour are retired, and the counts are recorded. The classifier corpus (several hundred statements, including hostile ones) keeps its reject expectations and restates its sharing expectations as "never unsafe": a statement marked shareable must fit the safe shape, and every statement the first build's corpus calls not coalescable must still be not shareable. Property: a statement outside the safe alphabet is never shareable, whatever else it contains. Differential test against real Postgres (tier R): every safe-shape statement of the corpus succeeds inside `BEGIN READ ONLY`, leaves a watched table unchanged, and returns the same rows twice. Fuzz: no panic; the recogniser and the lexer on arbitrary bytes. e2e for this chunk: each reject-list feature sent by a simulated client through the wire machines gets `0A000` and nothing reaches the fake Postgres (its statement counter stays zero). CSV-flush assertion: classifier rejects equal statements sent, and the fake Postgres statement count is 0.

**Observability.** Classifier counters: statements by class, rejects by reason, unclassifiable, safe-shape hits, hinted. Error-contract counters keyed by row.

**Landing.** Generated documents committed and held by tests; the kept / hoisted / retired test counts in the chunk notes; the stated limits of the courtesy list and of the safe shape written down for Neon's chapter. Size: about 1,900 lines, most of them carried over from the first build (the safe-shape recogniser is about 200 new lines).

---

### Chunk 6 — Walking skeleton

**Purpose.** The first complete proxy: a client connects, runs statements and transactions, and gets correct answers through the proxy, in the simulator and against a real Postgres. Deliberately plain — a small fixed set of database connections and a bounded first-come queue — but with every seam the later chunks plug into.

**Deliverables.** `internal/proxy`: the engine (decision D1) with its stage seams — classify, gate, coalesce, queue, lease, forward, release — each a small interface with a pass-through implementation; the transaction-lease lifecycle (lend on first statement, hold while status is `T` or `E`, release on `I`); reply relay with bounded buffers and back pressure both ways (D5); the dirty-connection rule in its simplest form (close on doubt); **the reset after every lease** (decision D8: `DISCARD ALL` as the proxy's own message at release, the connection lendable only after the expected tag and status `I`, closed on anything else or at the reset deadline; config key `lease.reset` with `always` only in this chunk; a test-only switch for the arm without a reset); the "reached the database?" fact recorded for every failure; the `draining` input reserved. `internal/shell`: listeners, per-connection reader and writer goroutines, bounded mailboxes, deadlines on every socket operation, **TLS on both legs** (section 3.1: the pre-startup request read as exactly 8 bytes with nothing buffered across the upgrade, `crypto/tls` server side for clients and client side for the database, the handshake inside the startup and connect deadlines, certificate loading), the admin listener on its own port serving health, JSON stats and Prometheus from the published snapshot. `internal/config`: the config file, validation, the `client_tls` and `backend_tls` blocks (fail closed on a missing or unreadable certificate), the fleet block (parsed; division used from chunk 7). `cmd/mini-gateway`. (`cmd/loadgen` moved to chunk 6a.) **The engine operation fuzz target** (sections 6.1 and 6.6) and `scripts/fuzz.sh` reporting executions per second per target with the rate floor. `make chaos`, concise by default. `test/e2e`: the scenario harness for tier S and tier R, with the Docker setup for Postgres 17. `docs/timeout-budget.md` and `docs/capacity-table.md` generators. `docs/extension-seams.md`. The fault × path matrix, first rows.

**Tests.** e2e, both tiers: *results through the proxy equal results direct* (same workload, same seed); *transaction integrity* — with many clients running interleaved multi-statement transactions over few connections, no statement of one client ever runs on a connection inside another client's transaction (checked from the fake's log, exactly; this is the core safety property of transaction pooling); *fragmentation* — one-byte delivery on both sides changes nothing; *reject list end-to-end* (the courtesy); ***session state never crosses clients*** — clients that change session state in every way the fake models (settings, including through statements the reject list does not see) interleave over few connections, and no client ever observes another's state: exact from the fake's own record in tier S, and in tier R checked on the real server by the catalog query of `agent_work/chunks/c05-classifier/canvas/experiments/postgres-server/dirty-check.sql` after each lease, with a function that hides a `SET` as the hostile case; the ablation arm with the reset off must fail it; *client vanishes mid-statement and mid-transaction* — the connection is not reused dirty; *database killed mid-statement* — the client gets the row of the error contract the matrix predicts; *slow reader* — bounded memory, proxy stops reading the database. **Operation fuzzing:** the target's seed corpus runs as plain tests in CI; on demand it must hold the rate floor of section 6.6 or be reported as a seed sweep; a planted ordering bug (a test-only switch) is found by it within the gate's fuzz budget and shrunk to a readable operation list. Shell tests under `synctest` and `-race`: no goroutine left after any teardown path; mailboxes never exceed their cap. `psql` and `pgbench -S`-style simple-protocol smoke runs against the real binary (tier R). **TLS, tier R and shell tests** (real `crypto/tls`, a test CA generated at test time with the standard library): `psql` with `sslmode=require` and `verify-full` through the proxy, and `sslmode=disable` refused with `28000` under `require`; the database leg under each `backend_tls.mode` against a Postgres with `ssl=on`, checked in `pg_stat_ssl`, with a wrong CA giving a connect failure of its class; plaintext bytes smuggled in with the TLS request close the connection; a client that stalls mid-handshake is closed at the startup deadline with no goroutine left; the whole tier R suite of this chunk runs with client TLS on. **CSV flush in the harness:** from this chunk every scenario in both tiers flushes and runs the round-trip check (section 6.5); the real binary runs with `obs.csv.dir` set, so tier R numbers are read from files with no metrics server. SQL assertions: conservation per instance; proxy completed = workload completed; error-contract counts by row = faults injected.

**Observability.** Engine snapshot: clients connected, in startup, in transaction; requests by stage and outcome; statement-time histogram (database send to end of reply); relay buffer high-water; resets started, completed and failed by reason, and the reset-time histogram; client connections by TLS version; the three-state health (only `ok` / `failed` rules so far, plus "database unreachable → degraded").

**Landing.** The first chapter that shows a working system. Timeout nesting validated at load. This is an integration-shaped chunk: sized large, with arbitration budget. Size: ~2,450 lines (about 250 of them TLS, config and certificates), ×2.

---

### Chunk 6a — Operator tools: the load tool, `mgctl`, run targets and the story report

Added on 2026-11-26 for the load-tool ruling and the developer-experience ruling (section 19). It builds beside wave B: it touches `cmd/`, `internal/mgctl`, `deploy/`, `scripts/`, `test/analysis/report/` and the Makefile, and no engine or core file.

**Purpose.** Being able to watch the proxy work. Start a gateway, a Postgres and a Prometheus with one command, put a constant load on it, watch the graphs move, change the load and run again; ask a running gateway how it is; and read a test run as a short report instead of raw output.

**Deliverables.**

- **`cmd/loadgen`** (Go; reuses `internal/testclient`, the `pgproto3` client path). Open-loop at a constant rate: every request has an intended send time and the tool never waits for a reply before the next send. Flags: `-rate`, `-duration`, `-conns`, `-tenants`, `-profile`, `-seed`, TLS and SCRAM settings. Keys, payloads and the operation mix are drawn from the seed, which is printed at the start, so a run can be repeated exactly. Profiles: `simple` (single-key reads and writes) and `errors` (the same plus statements that fail in the database — a unique violation, a missing relation, a division by zero — and statements the proxy refuses, so every class of outcome shows on the graphs). A live status line once a second (sent, completed, errors by SQLSTATE class, achieved against target rate, p50 and p99 from intended send time). An end-of-run summary that prints a **SHORTFALL** line when the achieved rate is below the target, and the count of requests it could not send; it never slows down silently. Its own snapshot goes through a collector and the CSV flush.
- **`cmd/mgctl`**, a small client for a running gateway (section 19.3, item 5), with its logic in `internal/mgctl` (no flag or HTTP-framework imports there): `health`, `stats` (with `--watch`), `metrics`, `tenants` (per-tenant numbers from the stats surface), `config show` (the running instance's fingerprint and effective settings) and `config check <file>` (validate a file offline with the gateway's own validation), `verifier` (derive a tenant's SCRAM verifier from a password), and `query` (run one statement through the proxy as a tenant and print the rows, or the SQLSTATE with its error-contract row). The admin surface is read-only in v1, so `mgctl` changes nothing in a running gateway. Standard-library `flag` with subcommands (section 19.3, item 6).
- **`deploy/dev/`** and the run targets: a docker-compose file with Postgres 17 and Prometheus (image versions pinned, ports on 127.0.0.1, names prefixed `mg-`), Prometheus scraping the gateway's own `/metrics` endpoint. `make run-up` builds and starts the stack and the gateway, waits until Prometheus reports the gateway target as up (its `/api/v1/targets` endpoint), and prints the URLs of the Prometheus UI, the targets page and three ready-made graphs. `make load RATE=… DURATION=… PROFILE=… SEED=…`, `make run-down`. Chunk 15 adds the OpenTelemetry Collector to this same stack.
- **The story report** (section 19.3, items 1 and 2): `scripts/report.sh <run directory or root>` and `make report RUNS=…`, with its SQL under `test/analysis/report/`. Over the flushed files of any set of runs it prints, in order: what the run proves; how the data was collected; the inputs (scenarios, seeds, arms, flushes); a legend for each table; coverage by scenario, statement class and outcome (error-contract row); every failed assertion and oracle mismatch in full; and a verdict table (claim, observed, expected, pass) that ends in one line and a non-zero exit on any failure. It reads the same at 3 seeds and at 300 (`SEEDS=`). `scripts/sweep.sh` and `make chaos` print one line per scenario by default and the raw output under `VERBOSE=1`.
- **The promotion pass for chunks 1–4** (section 12.2, step 9): the adversarial tests of those chunks that held and still describe the product move into the shipped suite; anything that needs vm-1 tools stays behind the tier R switch so CI stays fast.

**Tests.** The load tool: achieved = intended at a rate the fake can serve; the same seed gives the same operation sequence; a throttled server produces the SHORTFALL line and a non-zero "could not send" or late count; a stalled server shows in the latency measured from intended send time. `mgctl`: a seeded model test — a random sequence of subcommands whose expected output comes from the test's own model — in process against the simulator's world and, in tier R, against the real binary; `MGCTL_TRANSCRIPT=<file>` writes a readable step log. `make run-up` then `make load` then `make run-down` as a tier R smoke test: the Prometheus query for completed requests is non-zero and equals the load tool's own count from its CSV flush. The report: golden output over recorded run directories; a planted failing assertion appears in full and makes the exit status non-zero.

**Observability.** The load tool's snapshot (intended, sent, completed by outcome, late, could not send, latency histograms). No new proxy metrics.

**Landing.** None of this runs in CI beyond unit tests (Nick's fast-CI ruling): the run targets, the load tool and the report are on-demand. `go.mod` keeps its one requirement. Size: ~1,300 lines.

---

### Chunk 7 — Pool

**Purpose.** The connection pool that is the point of the project: a hard cap that holds under a storm, a limit on how fast connections are opened, and sane handling of connections that go bad.

**Step before the build: mine the book (ruling 6).** Before the brief for this chunk is written, a fresh agent dispatched by Fable reads *Release It!* itself — `/root/briefs/release-it-book.pdf` on vm-1 (SHA-256 `d4818e6417c5277123f11fd2d2adeff67505ae5b35999247023da26a2c8bd6cc`) — for the material Nick's notes lack: pooled connections that die without either end being told (firewall and NAT idle-timeout drops, half-open sockets), how the book says to detect them (keepalives, checks before reuse, dead-connection detection), and what it says about waits at checkout. The output is one short note, `docs/knowledge/release-it-pooled-connections.md`, with page references, in four parts: what the book says; each point mapped to the deliverable, test or fault-catalog entry of this chunk that already covers it; every gap, written as a concrete change to this chunk's brief (a fault mode, a timeout, a config default, a counter); and points not adopted, with the reason. Fable folds the gaps into the brief and into sections 9.2, 10.3 and 10.4 where they change, and indexes the note in `agent_work/INDEX.md`. The brief cites the note, and the builder is not dispatched without it. The step runs while wave A and chunk 6 build, so it delays nothing. What the note is checked against is this chunk's present cover of those failures, taken from the spec's list: the half-open and frozen-database faults, idle timeout and lifetime with jitter, and the check before reuse after long idle.

**Deliverables.** `internal/pool` core: accounting with open + connecting ≤ cap; a cap on concurrent connects and a creation rate (not PgBouncer's one-at-a-time); idle timeout and maximum lifetime with jitter (no synchronized expiry); check before reuse after long idle; the single breaker — database-down detection from connection-class failures, connect lockout, one trial when half-open, immediate `53M03` while open; the full dirty-connection table (cancel, bounded wait, rollback, then the reset of decision D8, reuse or close); **the release policies of decision D8 on the one code point chunk 6 built** — `lease.reset: always | on-switch` (a last-tenant tag per connection; under `on-switch` the pool prefers a free connection last held by the same tenant and resets on the checkout path when it must switch) and `lease.reseed: off | on`; the idle-in-transaction deadline; client-issued cancel requests mapped to the lent connection, with a cap on concurrent cancels; the separate cap on clients in startup; fleet division of the connection and creation-rate budgets. Wiring into the engine's lease stage.

**Tests.** Core property tests against a reference model with synthetic work units (no Postgres types). **A per-core operation fuzz target** (sections 6.1 and 6.6): the input decodes to pool operations and faults (checkout, release in each state, reset and connect results, deadlines, database lost), run against the reference model, at the rate floor; chunks 8–12 each add theirs the same way. e2e, both tiers unless noted: **hard connection cap** — 1,000 simultaneous client connects, open + connecting never exceeds the cap (exact, simulator); **reconnection storm** — 500 clients dropped and reconnecting within 50 ms, database connections stay flat at the pool size, against the chunk 2 baseline where the database refuses clients (tier R runs it with client TLS on, so the handshakes are part of the storm and the cap on clients in startup is shown to bound them); **slow consumer / half-open** — clients that stop reading are torn down at the write deadline and no connection leaks; **cold start** — empty pool, full load at time zero: dial rate ≤ the cap; **synchronized lifetimes** — no dial spike; **database restart** — breaker opens, requests cost O(1) with no dial, one trial, recovery without a dogpile, goodput returns; **idle-in-transaction** client is rolled back and its connection reused; **cancel** reaches the right statement and never another client's; **session state under each release policy** — chunk 6's isolation scenario re-run under `always`, `on-switch` and each with the re-seed: zero crossings between tenants in all of them, same-tenant carry-over present only under `on-switch`, the random-seed residue present without the re-seed and absent with it (tier R). Ablation: with the creation-rate cap off, the cold-start scenario exceeds the bound. Headline benchmark 1 (tier R): database connections under 500 clients, direct against proxy. CSV-flush assertions: open + connecting ≤ cap in every flush and the fake's connection peak ≤ cap; storm against the baseline run as a join; dials per interval ≤ the creation limit; completed-per-interval recovers after the restart.

**Observability.** Pool snapshot: open, idle, lent, connecting; creation rate; closes by reason; exhaustion events; breaker state and transitions; dirty-table outcomes; resets by policy and outcome, re-seeds, tenant switches; cancels; lease-time histogram. Health: `degraded` while the breaker is open.

**Landing.** The spec's pool invariants; section 8.2 rows for cap and creation rate; the mining note exists and every gap it names is closed in the code or declined in writing. Size: ~1,500 lines, ×2.

---

### Chunk 8 — Admission queues

**Purpose.** Deciding which waiting requests are still worth serving: per-tenant bounded queues with two selectable shedding rules, and newest-first service while overloaded.

**Deliverables.** `internal/admission`: a fixed-capacity ring per tenant, created on first use and removed when a tenant goes quiet; the `QueueRule` interface with two equal implementations — **timeout rule** ("Fail at Scale": one last-empty timestamp; allowed wait is the short target while a standing queue exists, else the interval) and **control law** (CoDel paper / RFC 8289: sojourn above target for an interval enters dropping; next drop after interval/√count, integer arithmetic) — selected by config, per instance; in both, age is measured on the oldest item and expired items are swept from the head and answered at once; adaptive LIFO (serve newest while the rule reports overload, with hysteresis); overflow sheds the head, not the arrival; folly's minimum-delay figure as a 0–100 load gauge. Simulator-only comparison arms: plain FIFO, folly's detector. Wiring into the engine's queue stage with a single default dispatcher (round robin arrives in chunk 9; the queues are tenant-keyed already).

**Tests.** Core property tests for both rules against reference models; the classic bug list from INFLUENCES F2–F4 as named tests (first burst after idle must not get the short timeout; LIFO without the head sweep buries stale requests). e2e: **anti-bufferbloat per mode** as restated in section 6.4, against a FIFO control with the same seed that grows without bound; **10× overload** — depth ≤ capacity, memory flat, backend never idled by shedding (served ≥ 95% of capacity); **the head-to-head table** — both modes and the comparison arms at 1.2×, 2× and 10× load: served-wait percentiles, depth, shed count, idle capacity (simulator, exact; this table goes in the book); recovery after overload ends. Ablation: with the rule off, the standing queue persists. Tier R: overload against real Postgres with `pg_sleep`-shaped service time. CSV-flush assertions: queue-wait percentiles from the bucket rows per mode and load; depth equal at T and 4T; sheds by reason; the head-to-head table is produced by one DuckDB query over the flushed runs of every arm.

**Observability.** Per tenant and total: depth, capacity, oldest-item age, queue-wait histogram, rule state, LIFO on/off and switches, sheds by reason, the load gauge. Health: `degraded` while any queue is shedding.

**Landing.** Both modes pass their own restated invariant; the difference between them is stated in numbers. Size: ~1,300 lines, ×2.

---

### Chunk 9 — Fairness

**Purpose.** Keeping a quiet tenant fast while a noisy one floods: choosing whose request runs next.

**Deliverables.** In `internal/admission`: the dispatcher — round robin across tenants with waiting work; a tenant's share (limit ÷ active tenants, with optional weights) enforced only when the limit is reached; work-conserving otherwise; per-tenant in-flight accounting that includes held transactions; removal of all-zero tenant state (memory bounded by active tenants). Parse-time checks on shares.

**Tests.** Property tests: per-tenant in flight ≤ share under saturation; total = sum; work-conserving (no idle capacity while anyone eligible waits); no starvation. e2e: **noisy neighbour** — tenant A at 10× capacity, tenant B steady and small: B's outcomes compared exactly with B running alone (simulator), and against the spec's thresholds (> 99% success, p99 within 2×, A takes > 95% of the shedding) in tier R; the test requires B's successes, not merely the absence of failures. **Many tenants, one flooder.** **A tenant holding transactions open** does not exceed its share. Ablation: with a single shared queue the quiet tenant starves. CSV-flush assertions: per-tenant totals — the quiet tenant against its run alone, the flooder's share of shedding > 95%; the many-tenants scenario is checked per tenant from the files, beyond the Prometheus label bound.

**Observability.** Per tenant: in flight, current share, served, shed share, time at share. Dispatcher: rounds, saturation time.

**Landing.** The fairness invariant in both tiers; section 8.2 row for fairness written with its condition. Size: ~900 lines.

---

### Chunk 10 — Adaptive limit and rate gate

**Purpose.** Giving the database less work when it slows down, without anyone turning a knob; and an optional per-tenant speed limit at the door.

**Deliverables.** In `internal/admission`: the AIMD limit on concurrent leases — one integer; multiplicative decrease on a statement time over the threshold, a timeout or a connection-class error (SQL errors never count); additive increase only when in flight ≥ half the limit; bounded by a floor, the pool size, a maximum step and a minimum interval between decreases; its signal is database round-trip time per statement, never queue wait (the queue rule's signal) — two loops, two signals. The GCRA gate — per tenant, integer nanoseconds, off by default, state evicted for quiet tenants, divided by N from the fleet config. Wiring: the gate stage and the dispatcher's limit.

**Tests.** Property tests: bounds always hold; after latency steps from 50 ms to 200 ms the limit never increases until recovery, then rises by at most one per completion; a pathological signal cannot push the limit past floor or ceiling or move it faster than the interval allows. GCRA against a reference model; a burst after a quiet hour is still bounded. e2e: **noisy neighbour + backend stall** (spec suite 3) — a 200 ms database stall while A sends 5,000/s and B 50/s: B protected, limit falls and recovers, goodput returns after the stall; **limit and queue rule do not fight** (no oscillation beyond a stated bound); rate gate refuses with `53M02` and never when off. Ablation: with a static limit the stall scenario fails B's bound. Headline benchmark 3 (tier R): quiet tenant's p99 under a noisy neighbour — unprotected FIFO against the full stack, in both CoDel modes. CSV-flush assertions: the limit gauge does not rise between stall and recovery; direction changes stay under the stated bound; gate refusals per tenant = `53M02` count.

**Observability.** Limit, floor, ceiling, increases, decreases by cause, time at floor; gate allows and refusals per tenant. Health: `degraded` while the limit is at its floor.

**Landing.** Spec suite 3 in both tiers; the stated limit of a single latency threshold for mixed SQL workloads written down. Size: ~900 lines, ×2.

---

### Chunk 11 — Coalescer

**Purpose.** Turning a stampede of identical reads into one or two database reads — without ever sharing a write, and without one caller's cancellation hurting another.

**Deliverables.** `internal/coalesce`: the flight table keyed by statement text; leader and followers; the two join modes of decision D10 (strict by default, relaxed selectable — ruling 2); a flight deadline independent of every waiter; waiter reference counting — the database work is cancelled only when the last waiter leaves; a caller that has left gets its own error even if a result arrives at that moment; a cap on waiters per flight (`53M04` past it) and on the shared reply's size (past it, followers are refused and the key is not shared again for a while — a bounded, expiring list); shared errors; followers take no queue slot and no connection but are counted per tenant; entries removed on completion. Wiring into the engine's coalesce stage; the reply is stored once as the database's message bytes and each recipient gets its own end marker. Config: `off | auto | hinted` and the scope of the key. `auto` shares exactly the statements chunk 5's recogniser marks as fitting the safe shape of decision D10; `hinted` shares only statements carrying the client's `/* mg:share */` mark, whatever their shape (the benchmark's hot-key read fits the safe shape, so headline 2 needs no hint). Config validation refuses `lease.reset: on-switch` together with a key scope of all tenants (decision D10).

**Tests.** Property tests on the flight table with synthetic units. e2e: **at most one in-flight read per key** (exact, simulator); **writes are never coalesced** — every write in the workload appears in the fake's write log exactly once; **cancel 99 of 100** — the 100th waiter gets a valid reply; **all 100 cancel** — the database work is cancelled and the connection handled by the dirty table; **viral hot key** (spec suite 2) — 1,000 concurrent identical reads with 50 ms database latency: 1–2 database reads, p99 near one service time in relaxed mode and near two in strict mode, both reported; **read-your-own-write** — a client that writes then reads the same key never gets the old value in strict mode, and the same scenario shows the window in relaxed mode; statements inside transactions are never shared; a slow follower does not delay the others; shared error on a failed flight, with no re-drive stampede. Ablation: coalescer off gives 1,000 reads. Headline benchmark 2 (tier R): database reads and p99 under Zipf skew (exponent stated; the spec's "99/1" restated as what the generator actually produces) with and without coalescing. CSV-flush assertions: database reads for the burst ≤ 2 against 1,000 in the ablation arm (a join of two arms); flights + hits = shareable reads.

**Observability.** Flights started, hits, waiters-per-flight histogram, cancellations (waiter and flight), overflows by cap, shared errors, table size; opt-in per-key hotness. Per tenant: coalesced hits.

**Landing.** The spec's three coalescing invariants; section 8.2 row "weakens to N" prepared for measurement in chunk 13. Size: ~1,100 lines, ×2.

---

### Chunk 12 — Retry policy

**Purpose.** Making sure the proxy never turns a partial outage into a retry storm — its own retries are a deliberate, budgeted choice that is off unless switched on.

**Deliverables.** `internal/retry`: the `Policy` interface; **Off** (the default); **PreForwardOnce** — at most one retry, only for failures the engine marks "nothing reached the database", charged to a budget (each first attempt deposits a tenth of a token, each retry withdraws one, the bucket is capped so credit expires, a small floor divided by N; integers only); never when the breaker is open; never holding a queue slot or a connection while waiting; never past the request's deadline. Completion of the error contract and the fault × path matrix (every row now states retryable-by-proxy or not). Wiring into the engine's lease and forward stages.

**Tests.** Property tests of the budget (retries ≤ 10% of first attempts plus the floor over any window; a quiet hour cannot bank a storm). e2e: **retry storm** (spec suite 5) — 30% database errors with aggressively retrying clients: with the policy off and on, the load reaching the database stays bounded, refusals are fast, and goodput returns after the errors stop; **post-forward failures are never retried under any policy** — the fake's write log shows no statement applied twice across a sweep with kills at every point of the conversation; a retry happens only on the matrix rows that allow it; budget exhaustion is visible. Ablation: a deliberately wrong policy ("retry everything", test-only) produces duplicate writes and amplification, proving the scenario can fail. CSV-flush assertions: in every window retries ≤ 10% of first attempts plus the floor; statements reaching the database bounded against the ablation arm; writes applied = distinct writes sent.

**Observability.** Attempts, retries, budget level, deposits, withdrawals, exhaustions, retries refused by reason. Health: unchanged by retries.

**Landing.** Spec suite 5; default verified off by a config-defaults test. Size: ~600 lines.

---

### Chunk 13 — Fleet configuration and graceful drain

**Purpose.** Running three proxies in front of one database honestly: measuring what gets weaker, proving what holds, and shutting an instance down without cutting anyone off.

**Deliverables.** `internal/config`: the full fleet block with division and validation (section 8.1). The drain sequence (section 8.3) across engine and shell. Multi-instance support in the simulator scenarios (N engines, one fake Postgres, a connection-spreading rule) and in the tier R harness (three processes). The steady-state soak. `docs/fleet-guarantees.md`: the table of section 8.2 with measured numbers, generated from scenario output.

**Tests.** e2e, three instances: **reconnection storm split across 3** — fleet connections ≤ the fleet budget; **hot key seen by 3 coalescers** — at most 3 database reads; **tenant pinned to one instance** against tenants spread over three — the fairness loss measured; **retry storm** — fleet ratio holds; **backend stall** — three adaptive limits converge, their sum bounded; **one instance killed at 80% load** — survivors shed rather than queue, the connection bound holds, goodput settles near two thirds and recovers when it returns; **rolling restart with drain** — zero requests cut mid-answer; refused-fast and timed-out counts reported; without drain (ablation) requests are cut. **Steady state** — a long simulated run with tenant churn (many configured tenants, few active at a time): every size gauge sampled at three points levels off. Config validation tests (bad divisions, budgets over the database limit, timeouts that do not nest). Tier R: the reconnection storm and the kill with three real processes. CSV-flush assertions: fleet totals as SQL sums over the three instances (connections ≤ fleet budget, hot-key reads ≤ 3); the SQL merge equals the `obs` merge function's output; size gauges level off at three points; requests cut mid-answer during drain = 0.

**Observability.** Instance id and fleet settings in the snapshot; drain state and progress; merged fleet view in the scenario reports. Health: `degraded (draining)`.

**Landing.** Every "weakens" and conditional row of section 8.2 has a measured number. Size: ~900 lines, ×2.

---

### Chunk 14 — Benchmarks and the trade-offs write-up

**Purpose.** The proof a reader can re-run: one command each for tests, chaos and benchmarks, and an honest account of how far the numbers can be trusted.

**Deliverables.** `cmd/loadgen` completed for benchmarking on top of chunk 6a's tool (further workload profiles: fan-in, Zipf, noisy neighbour, storm; calibration mode; 3-significant-digit histograms). `scripts/bench.sh` and `make bench`: calibration file, A/A run, interleaved runs, validity gate, the report in the story shape of chunk 6a, verdicts including "inconclusive", a CSV flush per run and Go-benchmark-format output; the report's SQL files under `test/analysis/bench/` (validity gate, medians and spread per arm, A/A noise floor, headline tables), run by DuckDB over the per-run files. `make chaos` running every named scenario. The three headline tables regenerated under the full method. The TLS-on against TLS-off arms (section 11). The observability acceptance test (section 7.6). The long-budget runs: sweeps, soak, timed fuzzing, `-race` long. Evidence pack for Neon: every number with its kind and its confidence row. README proof section. **The trade-offs chapter** (section 11).

**Tests.** The load generator's own: samples = intended sends; a stalled server shows in the corrected latency; achieved-rate gate rejects a throttled run. The A/A run reports "no difference". Bounded memory and no goroutine growth under a sustained 10× overload on the real binary. The report queries are tested on recorded run directories: a throttled run is rejected by the validity query; an A/A pair gives "no difference"; a planted difference above the margin is found. The "what is slow?" acceptance test is answered once from the stats surface and once by a query on the flushed files.

**Observability.** The load generator's snapshot goes through a collector and the same CSV flush adapter as the proxy's, so one query can compare what the generator sent with what the proxy counted.

**Landing.** `make test`, `make chaos`, `make bench` each run from a clean checkout; every headline carries its confidence statement; claims that did not come out in our favour are narrowed in writing. Size: ~1,200 lines.

---

### Chunk 15 — OTLP push exporter (last build chunk)

**Purpose.** The final adapter, and with it the production path: snapshots → collector → OTLP push → OpenTelemetry Collector → Prometheus (section 7.9). It also shows the snapshot design paying off: a new way to ship metrics is added without touching any component. It stays the last build chunk and the last observability chunk.

**Deliverables.** `internal/obs/otlp`: a hand-written OTLP/HTTP JSON metrics push adapter on the standard library — no OpenTelemetry SDK; cumulative sums, gauges and explicit-bucket histograms on the short bound list; the bounded tenant label; bounded queue, timeout, bounded retry with backoff, drop-and-count when the Collector is down (the adapter must never slow the proxy). Config (`obs.otlp.endpoint`, `obs.otlp.interval`). The adapter conformance test from chunk 3.

**The dev setup for the Collector and Prometheus leg**, `deploy/obs-dev/`, so the chain can be run and not only read about (it extends chunk 6a's `deploy/dev/` stack, so `make run-up OTLP=1` then gives the gateway, the Collector and Prometheus together, with the URLs printed):

- `docker-compose.yml` with two services, image versions pinned, every published port bound to 127.0.0.1, container names prefixed `mg-`:

```yaml
name: mg-obs-dev
services:
  otelcol:                       # receives the proxy's OTLP push, exposes a Prometheus endpoint
    image: otel/opentelemetry-collector-contrib:<pinned in this chunk>
    command: ["--config=/etc/otelcol/config.yaml"]
    volumes: ["./otelcol.yaml:/etc/otelcol/config.yaml:ro"]
    ports: ["127.0.0.1:4318:4318"]
  prometheus:                    # scrapes otelcol:8889
    image: prom/prometheus:<pinned in this chunk>
    volumes: ["./prometheus.yml:/etc/prometheus/prometheus.yml:ro"]
    ports: ["127.0.0.1:9090:9090"]
    depends_on: [otelcol]
```

- `otelcol.yaml`: an `otlp` receiver on HTTP port 4318; the `batch` processor; the `prometheus` exporter on port 8889 with resource attributes converted to labels; one metrics pipeline joining them.
- `prometheus.yml`: one scrape job for `otelcol:8889`, with a short scrape interval for development.
- `make obs-up` and `make obs-down`; the proxy runs on the host with `obs.otlp.endpoint: http://127.0.0.1:4318/v1/metrics`, and Prometheus is then at `http://127.0.0.1:9090`. The same steps are the walkthrough in this chunk's chapter.
- `scripts/obs-chain.sh`: the chain check described under Tests.

**Tests.** Golden payloads; a Collector that is down, slow, or answering errors never affects the data path (simulator and shell tests, using a fake receiver; no container); the diff of this chunk touches no file outside `internal/obs`, `internal/config`, `deploy/obs-dev`, the scripts and the shell's wiring — checked by the gate. Metric names and labels on the OTLP route equal those of the Prometheus text adapter. **The chain check, tier R, on demand** (`scripts/obs-chain.sh`, needs Docker): bring the compose setup up; run a short scenario against the real binary with both the CSV flush and the OTLP push on; after the final push and one scrape, query Prometheus over its HTTP API and compare with DuckDB's totals from the flushed files of the same run — counters equal for every labelled tenant, `other` equal to the sum of the remaining tenants, histogram counts equal after re-bucketing, and the three instances of a fleet run summing to the fleet total. Then stop the Collector mid-run: goodput is unchanged, the adapter's dropped-snapshot counter moves (read from the CSV flush, which does not depend on the Collector), and after the Collector returns Prometheus shows the correct cumulative totals. This is the only test in the plan that runs a live Prometheus.

**Observability.** Adapter counters: pushes, failures, dropped snapshots, queue depth.

**Landing.** "No component changed" verified mechanically; the chain check passes on vm-1 and its result file is recorded; the compose setup starts from a clean checkout with one command. Size: ~550 lines, plus the three config files.

---

### Chunk 16 — Book finish

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
| CSV flush: schema version 1, round trip, byte-identical files; baseline counts asserted with DuckDB; the assertion runner's self-tests | observability amendment | 3 | S | every scenario from chunk 6 on flushes and adds its SQL assertions (section 6.5) |
| Startup and authentication under fragmentation and faults | spec 4.1 | 4 | S, R | — |
| The TLS request exchange in every mode, with its faults (handshake as a simulated event) | ruling 5 | 4 | S | 6 |
| Real TLS on both legs: `psql` over TLS through the proxy, verified database leg, smuggled bytes, stalled handshake | ruling 5 | 6 | R, synctest | 7 (storm with TLS on), 14 (TLS on/off arms) |
| Reject list end-to-end (the courtesy) | steering 9, ruling 9 | 5 (wire-level), 6 (through the proxy) | S, R | — |
| Session state never crosses clients: reset after every lease, with its ablation | ruling 9 | 6 | S, R | 7 (each release policy, the re-seed) |
| Results through the proxy equal results direct | ours | 6 | S, R | every later chunk (with its feature on) |
| Transaction integrity (no cross-client interleaving) | ours | 6 | S, R | 7, 13 |
| Client vanishes mid-statement / mid-transaction | spec 4.1 | 6 | S, R | 7 (full dirty table) |
| Database killed mid-statement; error-contract rows | spec 2.4 | 6 | S, R | 12 (matrix complete) |
| Hard connection cap, 1,000 simultaneous connects | spec 4.2 | 7 | S, R | 13 (×3) |
| **Reconnection storm** | spec 4.3 (1) | 7 | S, R | 13 (split across 3) |
| **Slow consumer / half-open drain** | spec 4.3 (4) | 7 | S, R | — |
| Cold start; synchronized lifetimes; database restart and recovery | *Release It!* | 7 | S, R | 13 |
| Headline 1: database connections, direct vs proxy | spec 4.4 | 7 | R | 14 (full method) |
| CoDel anti-bufferbloat, per mode; head-to-head table | spec 4.2, steering 3 | 8 | S, R | 10 |
| Bounded memory under 10× overload | spec 4.2 | 8 | S | 14 (real binary, sustained) |
| Goroutine count returns to baseline | spec 4.2 | 6 (shell tests) | synctest, R | 14 |
| Engine operation fuzzing: any order of ordinary operations and faults keeps every invariant | DST fuzz ruling | 6 | S (fuzz) | 7–12 (per core) |
| Constant load on a running stack: Prometheus agrees with the load tool's own count; SHORTFALL reported | loadgen ruling | 6a | R (on demand) | 14 |
| `mgctl` seeded model run against a running gateway | DX ruling, item 5 | 6a | S, R | 13 (drain state) |
| Noisy-neighbour fairness | spec 4.2 | 9 | S, R | 10, 13 |
| **Noisy neighbour + backend stall** | spec 4.3 (3) | 10 | S, R | 13 |
| Headline 3: quiet tenant p99, FIFO vs full stack | spec 4.4 | 10 | R | 14 |
| At most one in-flight read per key; writes never coalesced; cancel 99 of 100 | spec 4.2 | 11 | S, R | 13 |
| **Viral hot-key stampede**, both join modes | spec 4.3 (2), ruling 2 | 11 | S, R | 13 (3 coalescers) |
| Read-your-own-write under coalescing, both join modes | ours, ruling 2 | 11 | S, R | — |
| Headline 2: database reads and p99 under skew, both join modes | spec 4.4 | 11 | R | 14 |
| **Retry-storm amplification** | spec 4.3 (5) | 12 | S, R | 13 |
| No statement applied twice | spec 2.4 | 12 | S | — |
| Three-instance set: storm, hot key, pinned tenant, stall, instance kill | steering 14 | 13 | S, R (storm and kill) | 14 |
| Rolling restart with drain | steering 14 | 13 | S, R | — |
| Steady state under tenant churn | *Release It!* | 13 | S | 14 (long) |
| Observability acceptance ("what is slow?") | *Release It!* | 14 | R | — |
| Collector down does not affect the data path | steering 13 | 15 | S, R | — |
| The chain check: Prometheus, fed through OTLP and the Collector, agrees with the CSV flush of the same run | observability amendment | 15 | R (compose setup) | — |

The five suites the spec names (bold) are each owned by the chunk that builds their subject; none waits for the benchmark chunk.

---

## 15. The book

The book is the learning artifact. Neon writes one chapter per chunk with the Clarity skill (section 19.4), once, late — after review, repair, adversarial results and the gate — from the final evidence, and it goes live on Pages at that chunk's landing, listed in the contents. A chunk is not landed until its chapter is live.

| Chapter | From chunk | Working title |
| --- | --- | --- |
| 1 | 1 | A world that repeats: the simulator kernel |
| 2 | 2 | A fake Postgres and hostile clients |
| 3 | 3 | Numbers without a framework: snapshots, a collector, exporters |
| 4 | 4 | Speaking Postgres: TLS or not, startup, login, fragments |
| 5 | 5 | What the proxy says and what it will not guess: the error contract, a courtesy list, and a safe shape for sharing |
| 6 | 6 | The first working proxy, and lending connections by the transaction |
| 6a | 6a | Watching it run: a load tool, a small client, and reports that say what a run proved |
| 7 | 7 | The pool: a hard cap, a creation rate, and connections that go bad |
| 8 | 8 | Two CoDels, measured against each other |
| 9 | 9 | Fairness that binds only when it must |
| 10 | 10 | Letting the database set the limit |
| 11 | 11 | One read for a thousand callers |
| 12 | 12 | Retries, off by default |
| 13 | 13 | Three proxies, one database: what multiplies, weakens and survives |
| 14 | 14 | How much to trust these numbers (the trade-offs write-up) |
| 15 | 15 | Adding an exporter without touching a component |
| — | 16 | Preface, topic map, assembly |

Chunk 0 has no chapter (the front page only). Titles are placeholders for Neon.

---

## 16. Risks

| Risk | Why it is real | What the plan does |
| --- | --- | --- |
| Integration chunks cost more than primitives | Object-store: two integration chunks cost more than five primitives together | Chunk 6 is sized large with arbitration budget; every later chunk integrates as it goes, so there is no big-bang integration chunk |
| Mechanism chunks run about 2× | *Release It!* notes | ×2 marks; split rule: a builder that passes ~2,000 lines of product code stops and asks the orchestrator to split |
| The classifier is wrong about a statement | It is a lexer without the catalog; the first build of chunk 5 lost three review rounds to it | Since ruling 9 a mistake cannot cross session state between clients (the reset of decision D8 does not read SQL) and cannot widen sharing (the safe shape of decision D10 does not use the lexer). What remains: a missed courtesy error, and the safe shape's own stated limit (a view or operator hiding a side effect), with `hinted` and `off` modes and the differential test against real Postgres |
| The fake Postgres drifts from the real one | A fake is a model | Fidelity script in both tiers; every scenario also runs in tier R |
| The single-threaded engine is a throughput ceiling | Decision D1 | Measured and stated in chunk 14; instances are the scaling unit; not hidden |
| TLS handshakes add CPU to a reconnection storm | Section 3.1, finding 8: up to about 3 ms of CPU per handshake on this VM | Handshakes run on shell goroutines and count against the cap on clients in startup; the tier R storm runs with TLS on (chunk 7); the cost is a measured arm in chunk 14 |
| Neon both reviews the code and writes the prose | Ruling 1 | Separate fresh agents for review and prose; Astra's adversarial harness is written from the brief, not from the code or the review; Fable runs the gate and holds final acceptance; arbitration goes outside the build/review pair |
| The control-law mode behaves badly under heavy open-loop overload | Phase 0 toy: queue grew without bound | It is bounded by the queue's hard capacity; the head-to-head table says plainly what each mode does |
| Wave merges conflict in `internal/proxy` | Several chunks wire into one engine | Stage seams fixed in chunk 6; chunks in a wave land one at a time and the later one rebases |
| Shared-VM noise | 4 shared vCPUs | Section 11; exact counts as headlines |
| The CSV schema drifts and SQL assertions silently read the wrong thing | Files outlive the code that wrote them | Schema version in every row and checked first by every query; golden header; generated metric registry with a test; the runner fails on empty input |
| DuckDB differs between machines | Nick named 2.0; vm-1 has 1.5.5 | Version pinned in the scripts and recorded by the gate; plain `read_csv` with explicit column types; DuckDB is absent from CI and from `go.mod`, so it cannot break the build |
| Neon or another pool runs out mid-wave | Subscription limits | Gauge check before each wave; recorded fallback route for Neon |
| vm-1 Claude sign-in expires Nov 25–28 | STATE.md | Owner: Nick/Twin; everything resumes from files |

**Size (decided, ruling 7: go as proposed, as amended).** Sixteen chunks after the foundation. The estimate is about 18,500–20,500 lines of product code before the ×2 allowances, and at least as much test code: the proposal's 19,000–21,000, less about 800 for the dropped batching chunk, plus about 350 for TLS. It is a larger build than the spec's "2–3 weeks" for Variant B, because the steering adds two CoDel modes, the full observability system and the fleet work to a Postgres proxy. No cuts are applied. The proposal's list of possible cuts is retired: its first candidate, batching, was dropped by ruling 3, and the other two (the GCRA half of chunk 10, tier R for the three-instance scenarios) stay in.

---

## 17. Out of scope for v1, and follow-ups

**Out of v1:** the extended query protocol; batching or pipelining several clients' statements onto one database connection (ruling 3); SCRAM channel binding (`SCRAM-SHA-256-PLUS`), client certificates, direct TLS negotiation, certificate reload without a restart and TLS on the admin listener (section 3.1); `COPY`; replication connections; `LISTEN`/`NOTIFY`; connection pinning; per-tenant database roles or several databases; caching; read replicas, hedging, outlier detection, shuffle sharding; a second backend protocol; run-time coordination between instances; a balancer, discovery or membership; the OpenTelemetry SDK; DuckDB as a library or anywhere in the proxy; Parquet or any second file format for the flush; dashboards and alert rules on top of Prometheus; a SQL parser; the "known clean" conditional reset (ruling 9); md5 password authentication.

**Follow-ups after the project, not part of v1:**

1. **Shared simulation kernel** (steering answer 8): assess extracting a shared kernel repository and refactor both object-store and mini-gateway onto it (section 5.4).
2. **A polymorphic protocol interface**, extracted against a real second protocol (Valkey/RESP is the candidate Nick named), using `docs/extension-seams.md` (section 4.2).
3. Pinning with a hard cap, as the alternative to resetting session state. Also a close-on-tenant-switch release policy: the lease experiment measured it (complete isolation including the residue of `DISCARD ALL`, but a p99 wait 5.4–9.7× the baseline on real Postgres), and ruling 9 did not adopt it.
4. Coordinated fleet budgets, as the alternative to static division.
5. **Per-tenant database roles** (the seam recorded by ruling 4): one pool per role, so that Postgres privileges can differ by tenant. It changes the pool's key, the fleet division and the fairness accounting, which is why it is not v1.
6. Batching by pipelining, only if a later measurement argues for it; the dropped design is in commit `5c83fe6` (decision D9).
7. Channel binding and client certificates on the client leg.

---

## 18. Nick's rulings (the decision record)

The proposal ended with eight open questions. Nick ruled on all of them on 2026-11-24 (`/root/briefs/zgateway-section18-rulings.md`), which approved the proposal as amended. Row 9 records his ruling of 2026-11-26 on chunk 5. Nothing here is open. Each ruling is written into the section named in the last column; this table is the record of what was asked and decided.

| # | Question | Status | Ruling (Nick, 2026-11-24) | Where it is applied |
| --- | --- | --- | --- | --- |
| 1 | Who reviews Opus's code on this project: Neon (project seat ruling) or Fable only (global rule of 2026-11-11)? | **DECIDED 2026-11-24** | Neon reviews Opus on this project, through Antigravity `agy`. The project seat ruling governs here; it is a project-scoped exception and the global Fable-only rule stands elsewhere. Astra's adversarial role stands. | Sections 12.1–12.3; every chunk's seats (section 13.1); `/root/briefs/zgateway-seat-ruling.md` |
| 2 | Coalescing: default to "a request never gets data older than itself" (strict) or "join the read already running" (relaxed)? | **DECIDED 2026-11-24** | The proposal is adopted: strict by default, relaxed selectable, both measured. | Decision D10; chunk 11; section 14 |
| 3 | Batching under Postgres (pipelining onto one connection): build it off by default, or drop the chunk? | **DECIDED 2026-11-24** | Drop the chunk. Timer-free batching and pipelining are out of v1. | Decision D9; section 2, row 11; the chunk list (16 chunks, renumbered); sections 14–17 |
| 4 | Tenant identity: the client's startup user name, checked by the proxy, over one shared database role? | **DECIDED 2026-11-24** | The proposal is adopted. Per-tenant roles are recorded as a later seam. | Decision D6; section 17, follow-up 5 |
| 5 | Client-facing extras: no TLS, SCRAM-SHA-256 and trust only, cancel requests supported? | **DECIDED 2026-11-24** | "I'd like to do TLS if not too hard but default to SCRAM if that's more common or normal." SCRAM-SHA-256 is the authentication mechanism. TLS: assess the real cost with evidence and include it if the cost is small. **Verdict of the assessment: INCLUDE** — about 350 lines, no new dependency, no new chunk. | Decision D13 and section 3.1 (verdict and evidence); chunks 4 and 6; sections 9.1, 9.2, 10.4, 11, 14 |
| 6 | Consult the *Release It!* book itself for pooled connections dying silently, before the pool chunk? | **DECIDED 2026-11-24** | Yes. The PDF is on vm-1 at `/root/briefs/release-it-book.pdf`. Consult it before the pool chunk builds and record what it changes: a step in the plan, not a someday note. | Chunk 7 (the step before the build); section 10; output `docs/knowledge/release-it-pooled-connections.md` |
| 7 | Size: 17 chunks and roughly 20,000 lines — go as proposed or cut? | **DECIDED 2026-11-24** | Go as proposed, minus the dropped batching chunk: 16 chunks after the foundation. | Section 13; section 16 (size) |
| 8 | Approval of the "end-to-end with every chunk" wording draft | **DECIDED 2026-11-24** | Approved. "Yes I want e2e incrementally built throughout as a form of integration testing throughout." The flow-guide draft wording (`/root/briefs/flow-guide-e2e-wording-draft.md`) is approved. | Section 6.2 (the rule); section 14 (the table) |

| 9 | Chunk 5 (statement classifier), parked on 2026-11-25: how is session state kept from crossing clients, given that reading SQL text cannot be made complete? (Asked by the scope assessment, the canvas of 19 projects, the options memo and the lease experiment, all under `agent_work/chunks/c05-classifier/`.) | **DECIDED 2026-11-26** (`/root/briefs/zgateway-c05-ruling-reset-always.md`) | Reset-always is the lease design: `DISCARD ALL` after every lease; reset-on-switch kept as a config policy on the same code point; the re-seed variant in config. The "known clean" conditional reset is not built. Session safety never depends on reading SQL. Chunk 5 is re-opened on that design: the reject list stays as a best-effort courtesy, and the classifier's real jobs are the work vocabulary, the error contract and the sharing recogniser inside its safe shape. The first build's tests are kept or hoisted wherever their behaviour survives. Evidence: the lease experiment's verdict table (`lease-exp/REPORT.md`). This changes steering answer 9 and decisions D7, D8 and D10. | Decisions D7, D8, D10; section 2, row 9; sections 9.1, 9.2, 9.4; chunks 5, 6, 7, 11; sections 14, 16, 17 |

Rows 1–8 were ruled before the build started; row 9 was ruled during it. The first step on Nick's go was chunk 0, then the simulator (chunks 1 and 2).

---

## 19. Later binding inputs (Nick, 2026-11-24; folded in on 2026-11-26)

Three inputs were filed while wave A was in flight, with the instruction not to disturb the chunks then building. They are folded in here, before chunk 6 is briefed.

### 19.1 DST fuzz ruling (`/root/briefs/zgateway-dst-fuzz-ruling.md`)

| Item | Where it is applied |
| --- | --- |
| 1. Engine-level operation fuzzing: the fuzz vocabulary includes client and engine operations, not only faults | Section 6.1 (the Fuzz row); chunk 6 (the engine target, with a planted ordering bug it must find); chunk 7 (per core), and each later core chunk the same way |
| 2. Three rules for simulation-level fuzz targets: no seed from a hash of the input; executions per second reported; a rate floor below which a target is a seed sweep | Section 6.6; `scripts/fuzz.sh` (change request CR-1, so the rules are in the tooling before the first new target lands) |
| 3. `sim.Shrink` lowers values (fault time and arguments, and workload values where the API allows) as well as deleting faults; found bugs are kept as shrunk scenarios | **Change request CR-1** against the frozen simulator API (`freeze-sim` passed with chunk 2): record `agent_work/chunks/cr01-shrink-fuzz-rules/`, executed as its own small build, review and attack while chunk 5 is reworked. The freeze rule is kept: `Shrink`'s existing signature and results do not change; value lowering is an added pass |

### 19.2 Load generator ruling (`/root/briefs/zgateway-loadgen-ruling.md`)

**Shape: a Go command, `cmd/loadgen`, the same tool the benchmark method of section 11 already needed. Placement: chunk 6a, right after the walking skeleton.** The reasons:

- A constant rate is the point and a shell loop cannot hold one (the ruling's own note). The plan already had an open-loop Go generator for the benchmarks; one tool with two uses is less code than two tools, and the exploration use arrives first, as Nick asked ("an interactive exploration tool first, not a CI artifact").
- It reuses `internal/testclient`, the `pgproto3`-based client the simulated clients and tier R already use, so it speaks exactly the protocol subset the proxy serves, with SCRAM and TLS.
- The proxy serves Prometheus text on its admin port from chunk 6. So "watch the metrics update in real time" does not have to wait for the OTLP chunk: chunk 6a's stack has Prometheus scrape the gateway directly, and chunk 15 adds the Collector to the same stack.
- Placed after chunk 6 and beside wave B, it lets every later mechanism (pool, queues, fairness, limit, coalescer, retry) be watched under load in the chunk that builds it.
- It is not CI: `make load` and `make run-up` are on-demand targets, consistent with the fast-CI ruling. If the same tool is used for performance numbers, that is chunk 14's `make bench`, also on demand.
- A note on the model: the ruling points at object-store's `cmd/objload`. That command is not in the object-store checkout on vm-1 (no branch or worktree has it; searched 2026-11-26), so the design follows the ruling's written notes: printed seed, a default and an error-path profile, a live status line, a shortfall summary, convenience targets that wait for Prometheus and print URLs.

### 19.3 Developer-experience patterns from object-store (`/root/briefs/zgateway-dx-patterns-ruling.md`)

Read in `/root/Software/object-store` at `d9dbac2` on 2026-11-26. "Found there" says what that checkout actually contains; some of what the ruling describes is from Nick's local session and is not on vm-1, and those items are designed here from the ruling's text.

| # | Pattern | Found in object-store | Verdict for mini-gateway | Where |
| --- | --- | --- | --- | --- |
| 1 | DuckDB reports that tell a story | `deploy/observability/queries.sql` (a narrated report: intro, how the data was collected, input summary, numbered sections each with question, method, legend, table), `test/observability/verdict.sql` (a `checks` view of claim, observed, expected, pass; one PASS line; non-zero exit on a failure), runner `query-csv.sh`. Not found there: coverage by command and outcome, an oracle-mismatch section, a `SEEDS=` knob on the report | **Adopted**, with the sections the ruling lists. Same CSV files and views as the assertions of section 6.5; the assertions stay the gate, the report is for people | Chunk 6a (`scripts/report.sh`, `test/analysis/report/`); chunk 14 uses the same shape for the benchmark report |
| 2 | Concise and verbose modes; CSV for large-test output | A transcript file for the CLI tests (`OBJCTL_TRANSCRIPT`), `-v` on the CLI. Not found there: structured CSV from tests, a concise/verbose switch | **Adopted.** Every scenario already flushes CSV (section 6.5). Large runs (`sweep.sh`, `make chaos`, tier R) print one line per scenario by default; `VERBOSE=1` gives the raw output; the transcript idea is used for `mgctl` | Section 6.3; chunk 6 (`make chaos`), chunk 6a (`sweep.sh`, the report, `MGCTL_TRANSCRIPT`) |
| 3 | The Makefile approach | A 68-line Makefile: build, cluster up and down with a health wait, test tiers, `query-csv`. Not found there: a target that leaves node, Collector and Prometheus running and prints URLs (that stack is only brought up inside a test, with a wait on Prometheus's targets endpoint) | **Adopted.** Targets grouped as build, test tiers (`test`, `chaos`, `sweep`, `fuzz`, `tier-r`), run (`run-up`, `load`, `report`, `run-down`), observability (`obs-up`, `obs-down`). The wait-for-Prometheus loop follows object-store's `check-otel.sh` | Section 6.3; chunk 6a; chunk 15 for the Collector |
| 4 | Lift agent-built tests out of `agent_work/` | Commit `4bd8d2b` moved three check scripts from `agent_work/` to `test/`. No written rule | **Adopted, as a written rule**: section 12.2, step 9 | Chunk 5's rework (first use); chunk 6a (once, for chunks 1–4); every chunk after |
| 5 | An exploratory CLI client | `cmd/objctl` on Cobra, logic in `internal/objctl` with no framework imports, a seeded model test that is also the oracle, run in process and against real nodes, with a transcript | **Adopted** at the surface this project has: a read-only admin surface (health, stats, metrics, per-tenant status, config), config validation, verifier derivation, and driving one proxied statement as a tenant. No mutating admin operation exists in v1, so there is none in the client | Chunk 6a (`cmd/mgctl`, `internal/mgctl`); chunk 13 adds the drain state |
| 6 | Dependency exemption for UX clients | `go.mod` requires Cobra for `objctl`; the gate states the exemption once | **Adopted as a permission and not used.** `mgctl` and `loadgen` have about ten subcommands and flags between them; the standard `flag` package covers that, and not using the exemption keeps the "exactly one requirement" check of `go.mod` (gate G0) and the import lint as they are. If a later tool needs Cobra, the exemption is written the way object-store's gate writes it: one `only` rule naming `cmd/mgctl` and `cmd/loadgen`, nothing in the core | Section 6.3 (lints unchanged); chunk 6a |

### 19.4 The write-up seat rule (`/root/briefs/zgateway-clarity-prose-pass-ruling.md`; Nick, 2026-11-26)

A later ruling than the three above, folded in on 2026-11-27. It mirrors the rule the project-skeleton template states in its `docs/build-flow.md`.

1. **Neon writes every reader-facing write-up, with the Clarity skill.** The skill is on vm-1 at `/root/.agents/skills/clarity`. The prose pass stays what section 12.2 step 6 says it is: one late stage of every chunk landing, written from the final verified evidence. The prose brief (`agent_work/orch/templates/prose.md`) tells the agent to read the skill and to follow it.
2. **Prose from any other seat is a draft.** A README paragraph by the orchestrator, a builder's note that reaches a published page, a C4 introduction written at the C4 step: each is input for Neon to rewrite with the Clarity skill in rewrite mode, not a finished write-up. No other seat hand-edits published prose for style.
3. **Published prose that reads densely gets a Neon clarity rewrite, not a hand edit.** A rewrite changes sentences, not substance. Headings, code blocks, inline code, file paths, links, numbers and claims are kept with the same force; nothing is invented or strengthened; the prose describes the software, never how it was made. Before the rewrite lands, the orchestrator checks it against the source — every number, link, heading and claim accounted for — and records the result in STATE.md. It lands by the normal rule: merge, push, and the published site checked for the new text.
4. **What a rewrite does not touch.** The generated or test-pinned contract pages (`docs/csv-schema.md`, `error-contract.md`, `capacity-table.md`, `safe-shape.md`, `reject-list.md`, `timeout-budget.md`, `extension-seams.md`) are never hand-edited; if one reads densely the fix belongs in its generator or its source, as a separate note. `docs/knowledge/`, PLAN.md and STATE.md are working documents, not published write-ups. The C4 pages are generated from `docs/c4/gen.py`: their introductions are rewritten there, and element and relationship tables are diagram definitions, not prose. The prose pass's gate does not admit `docs/c4/` (section 12.3, `tools/lint-rules/prose-paths.txt`), so a C4 rewrite is a pass of its own.
5. **First use.** The prose published up to chunk 6 — README, the book's front page and six chapters, the influences pages, the docs index and the C4 introductions — is rewritten once under this rule; STATE.md records the dispatch, the source check and the landing.
