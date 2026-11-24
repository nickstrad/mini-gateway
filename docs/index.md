# mini-gateway — the approved plan

**Status: the plan was approved by Nick on 2026-11-24, as amended by his rulings. Nothing has been built yet; building starts on his go.** This site holds the plan, the target architecture diagrams, and a summary of the influences behind them. It is not the book yet; the book's chapters will appear here as chunks land.

mini-gateway is a Go proxy that speaks the Postgres wire protocol in front of one ordinary Postgres. It gives the database a bounded number of connections however many clients arrive, and under overload it decides per tenant which requests wait, which run, and which are refused quickly with an error that says whether a retry is safe.

## What to review

| Read | What it is |
| --- | --- |
| [The plan](../PLAN.md) | What will be built, in 16 chunks after a foundation step; the tests, observability duties and landing criteria of each; Nick's rulings |
| [C4 diagrams](c4/README.md) | The target architecture: context, containers, the three-instance deployment, and eight component views |
| [Influences summary](influences-summary.md) | What Phase 0 found, what Nick steered, and where the steering overrode the recommendations |

## The two verdicts the steering asked for

**Can the object-store simulator be the starting point?** ([plan, section 5](../PLAN.md#5-steering-answer-8-can-the-object-store-simulator-be-the-starting-point)) No, not as a whole. It simulates a cluster exchanging whole messages; it has no byte-stream connections, which is the centre of a proxy's simulated world. About 8% of its code would survive unchanged. The plan writes a seeded event-loop kernel in this repository, copies in the pieces that are clean (named random streams, seed and replay plumbing, the event heap, the determinism lint), and keeps its conventions. A shared kernel for both projects is a follow-up after this one, outside v1.

**Does a generic backend interface stay honest for Postgres?** ([plan, section 4](../PLAN.md#4-steering-answer-1-does-a-generic-backend-interface-stay-honest-for-postgres)) No. A probe against a real Postgres 17 shows why: the reply, not the request, decides when a connection is free; replies are unbounded streams; errors carry transaction state; a failure after forwarding has no reply at all. The plan is Postgres-first with three seams that are tested inside v1 — a protocol-free transport under the engine, one package that holds everything Postgres, and a small vocabulary that is all the pool, queues and coalescer ever see.

## What Nick decided

The proposal ended with eight questions. Nick ruled on all of them on 2026-11-24; the record is [section 18 of the plan](../PLAN.md#18-nicks-rulings-the-decision-record), and each ruling is written into the section it changes.

| # | Question | Ruling |
| --- | --- | --- |
| 1 | Who reviews Opus's code | Gemini Neon, on this project only; the global rule that only Fable reviews Opus is unchanged elsewhere |
| 2 | Shared reads | "Never older than the request" by default; "join the read already running" selectable; both measured |
| 3 | Batching | Dropped from v1. The plan is now 16 chunks after the foundation |
| 4 | Tenant identity | The client's startup user name, checked by the proxy, over one shared database role; per-tenant roles are a later seam |
| 5 | TLS and login | **TLS is included**: the proxy terminates client TLS, and the database connection can use TLS. SCRAM-SHA-256 is the login mechanism. The cost assessment and its evidence are [section 3.1 of the plan](../PLAN.md#31-ruling-5-client-facing-tls-verdict-and-evidence) |
| 6 | *Release It!* | The book itself is read for pooled connections that die silently, before the pool chunk is built |
| 7 | Size | Approved as amended |
| 8 | End-to-end tests with every chunk | Approved |

## Evidence

- [Probe against Postgres 17 and its output](../agent_work/phase1/probes/pgwire/output-2026-11-24.txt)
- [TLS cost probe and its output](../agent_work/phase1/probes/pgtls/output-2026-11-24.txt), and [the assessment](knowledge/tls-cost-assessment.md)
- [Evaluation of the object-store simulator](../agent_work/phase1/research/q8-object-store-simulator-evaluation.md)
- [*Release It!* notes mined for the plan](../agent_work/phase1/research/release-it-mining.md)
- [The spec](project-source.md) and the [full influences list](../INFLUENCES.md)
