# mini-gateway — Phase 1 proposal for review

**Status: awaiting Nick's approval. Nothing has been built.** This site holds the proposal: the build plan, the target architecture diagrams, and a summary of the influences behind them. It is a review site, not the book; the book's chapters will appear here as chunks land.

**Amended 2026-11-24** with the observability amendment: metrics flushed to CSV files that tests assert on, DuckDB queries over those files, and the production path from the OTLP push through an OpenTelemetry Collector to Prometheus. See sections 6.5 and 7.7–7.9 of [the plan](../PLAN.md), chunks 3 and 16, and [the observability diagram](c4/13-component-observability.md).

mini-gateway is a Go proxy that speaks the Postgres wire protocol in front of one ordinary Postgres. It gives the database a bounded number of connections however many clients arrive, and under overload it decides per tenant which requests wait, which run, and which are refused quickly with an error that says whether a retry is safe.

## What to review

| Read | What it is |
| --- | --- |
| [The plan](../PLAN.md) | What will be built, in 17 chunks after a foundation step; the tests, observability duties and landing criteria of each; the open questions |
| [C4 diagrams](c4/README.md) | The target architecture: context, containers, the three-instance deployment, and nine component views |
| [Influences summary](influences-summary.md) | What Phase 0 found, what Nick steered, and where the steering overrode the recommendations |

## The two verdicts the steering asked for

**Can the object-store simulator be the starting point?** ([plan, section 5](../PLAN.md#5-steering-answer-8-can-the-object-store-simulator-be-the-starting-point)) No, not as a whole. It simulates a cluster exchanging whole messages; it has no byte-stream connections, which is the centre of a proxy's simulated world. About 8% of its code would survive unchanged. The plan writes a seeded event-loop kernel in this repository, copies in the pieces that are clean (named random streams, seed and replay plumbing, the event heap, the determinism lint), and keeps its conventions. A shared kernel for both projects is a follow-up after this one, outside v1.

**Does a generic backend interface stay honest for Postgres?** ([plan, section 4](../PLAN.md#4-steering-answer-1-does-a-generic-backend-interface-stay-honest-for-postgres)) No. A probe against a real Postgres 17 shows why: the reply, not the request, decides when a connection is free; replies are unbounded streams; errors carry transaction state; a failure after forwarding has no reply at all. The plan is Postgres-first with three seams that are tested inside v1 — a protocol-free transport under the engine, one package that holds everything Postgres, and a small vocabulary that is all the pool, queues and coalescer ever see.

## Decisions that need Nick

The plan ends with [eight open questions](../PLAN.md#18-open-questions-for-nick). The ones that change the design: who reviews Opus's code on this project; whether shared reads default to "never older than the request" (slower) or "join the read already running" (faster, with a brief stale-read gap); and what batching should mean for Postgres, where the plan proposes building it but leaving it off until measured.

## Evidence

- [Probe against Postgres 17 and its output](../agent_work/phase1/probes/pgwire/output-2026-11-24.txt)
- [Evaluation of the object-store simulator](../agent_work/phase1/research/q8-object-store-simulator-evaluation.md)
- [*Release It!* notes mined for the plan](../agent_work/phase1/research/release-it-mining.md)
- [The spec](project-source.md) and the [full influences list](../INFLUENCES.md)
