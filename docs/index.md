# mini-gateway

`mini-gateway` is a Go proxy that sits in front of one ordinary Postgres server and speaks the Postgres wire protocol (the message format Postgres clients already use). It gives the database a bounded pool of connections lent one transaction at a time (transaction pooling), terminates client TLS (encrypted connections), and lets identical in-flight read-only statements share one database round trip. Under overload, it applies per-tenant admission control: it decides which requests wait, which run, and which are refused quickly with an error stating whether a retry is safe. A deterministic simulator (a single-threaded test harness with a virtual clock and seeded randomness) is built first, before any proxy code.

**Status:** Plan approved by Nick on 2026-11-24, as amended by his rulings ([section 18 of the plan](../PLAN.md#18-nicks-rulings-the-decision-record)). Build in progress. Chunk 0 (foundation), Chunk 1 (simulator kernel), Chunk 2 (simulated world: fake Postgres 17, simulated clients, direct baselines, and tier R fidelity), Chunk 3 (observability spine: collector, adapters, CSV flush, generated schema, and DuckDB SQL assertions), Chunk 4 (wire sessions: sans-I/O framer, client and database startup/TLS/SCRAM-SHA-256 state machines, query-cycle trackers, simulated TLS, Postgres 17 startup fidelity, 19 wire scenarios, and component `wire` export), and Chunk 5 (statement classifier and contracts: protocol-free work vocabulary, 20-row error contract, wire reply builders and extended-protocol discard-until-`Sync`, SQL lexer and courtesy reject list, lexer-independent safe-shape recogniser, Postgres 17 differential oracles, two reject-list scenarios, and component `classifier` export) have landed, and [Chapter 1](../book/chapters/01-simulator-kernel.md), [Chapter 2: A fake Postgres and hostile clients](../book/chapters/02-simulated-world.md), [Chapter 3: Numbers without a framework: snapshots, a collector, exporters](../book/chapters/03-observability-spine.md), [Chapter 4: Speaking Postgres: TLS or not, startup, login, fragments](../book/chapters/04-wire-sessions.md), and [Chapter 5: What the proxy says and what it will not guess: the error contract, a courtesy list, and a safe shape for sharing](../book/chapters/05-classifier.md) are live.

## On this site

| Read | What it is |
| --- | --- |
| [The book](../book/index.md) | One chapter per build step, published as each chunk lands ([Chapter 1](../book/chapters/01-simulator-kernel.md), [Chapter 2](../book/chapters/02-simulated-world.md), [Chapter 3](../book/chapters/03-observability-spine.md), [Chapter 4](../book/chapters/04-wire-sessions.md), and [Chapter 5](../book/chapters/05-classifier.md) are live) |
| [The plan](../PLAN.md) | What is being built in 16 chunks after the foundation, with tests, observability duties, landing criteria, and Nick's rulings |
| [C4 diagrams](c4/README.md) | Architecture diagrams: system context, containers, the three-instance deployment, and eight component views |
| [Influences summary](influences-summary.md) | What Phase 0 found, what Nick steered, and where the steering overrode earlier recommendations |
| [Full influences list](../INFLUENCES.md) | The 100 Phase 0 influences and 18 conflicts, written before the steering |

## Two settled architecture verdicts

**Can the object-store simulator be the starting point?** ([plan, section 5](../PLAN.md#5-steering-answer-8-can-the-object-store-simulator-be-the-starting-point)) No, not as a whole. It simulates a cluster exchanging whole messages; it has no byte-stream connections, which sit at the centre of a proxy's simulated world. About 8% of its code would survive unchanged. The plan builds a seeded event-loop kernel in this repository, copies in the pieces that fit cleanly (named random streams, seed and replay plumbing, the event heap, the determinism lint), and keeps its conventions. A shared kernel for both projects is a follow-up outside v1.

**Does a generic backend interface stay honest for Postgres?** ([plan, section 4](../PLAN.md#4-steering-answer-1-does-a-generic-backend-interface-stay-honest-for-postgres)) No. A probe against a real Postgres 17 shows why: the reply, not the request, decides when a connection is free; replies are unbounded streams; errors carry transaction state; a failure after forwarding has no reply at all. The design is Postgres-first with three seams tested inside v1: a protocol-free transport under the engine, one package that holds everything Postgres, and a small vocabulary that is all the pool, queues, and coalescer (shared-read handler) see.

## What Nick decided (2026-11-24)

The plan ended with eight open questions. Nick ruled on all of them on 2026-11-24; the decision record is [section 18 of the plan](../PLAN.md#18-nicks-rulings-the-decision-record), and each ruling is folded into the section it changes.

| # | Question | Ruling |
| --- | --- | --- |
| 1 | Who reviews Opus's code | Gemini Neon, on this project only; the global rule that only Fable reviews Opus is unchanged elsewhere |
| 2 | Shared reads | "Never older than the request" by default; "join the read already running" selectable; both measured |
| 3 | Batching | Dropped from v1. The plan is 16 chunks after the foundation |
| 4 | Tenant identity | The client's startup user name, checked by the proxy, over one shared database role; per-tenant roles are a later seam |
| 5 | TLS and login | **TLS is included**: the proxy terminates client TLS, and the database connection can use TLS. SCRAM-SHA-256 (Postgres's challenge-response password login) is the authentication mechanism. The cost assessment and its evidence are in [section 3.1 of the plan](../PLAN.md#31-ruling-5-client-facing-tls-verdict-and-evidence) |
| 6 | *Release It!* | The book itself is read for pooled connections that die silently, before the pool chunk is built |
| 7 | Size | Approved as amended |
| 8 | End-to-end tests with every chunk | Approved |

## Phase 1 evidence

- [Probe against Postgres 17 and its output](../agent_work/phase1/probes/pgwire/output-2026-11-24.txt)
- [TLS cost probe and its output](../agent_work/phase1/probes/pgtls/output-2026-11-24.txt), and [the assessment](knowledge/tls-cost-assessment.md)
- [Evaluation of the object-store simulator](../agent_work/phase1/research/q8-object-store-simulator-evaluation.md)
- [*Release It!* notes mined for the plan](../agent_work/phase1/research/release-it-mining.md)
- [The spec](project-source.md) and the [full influences list](../INFLUENCES.md)
