# Influences summary

Phase 0 produced [INFLUENCES.md](../INFLUENCES.md), a survey of 100 influences read from their sources where possible, recording what to borrow, what not to copy, and a recommendation for each. Nick then answered 14 steering questions to settle the design. This page is the short version. The full document was written before those answers, under the old project name and with Redis as the working assumption. Where it and the plan disagree, the plan follows the steering.

## What is in the list

| Bucket | Count | Examples |
| --- | --- | --- |
| Named by the spec | 14 | Meta's ZGateway post, Cloudflare Hyperdrive, "Fail at Scale", the CoDel paper, Apollo's benchmarking post |
| Nick's earlier projects | 20 | The object-store simulator, mansio's admission and retry code, the per-chunk end-to-end rule |
| Industry systems | 36 | PgBouncer, Odyssey, RDS Proxy, Envoy, Netflix concurrency-limits, Finagle's retry budget, the Google SRE book, Go's singleflight and its forks, Vitess |
| Foundations | 30 | CoDel's three variants, adaptive LIFO, GCRA, AIMD, Little's law, coordinated omission, HDR histograms, FoundationDB-style simulation, the snapshot metrics pattern, *Release It!* |

Before steering, the recommendations broke down into 28 adopt, 40 adopt with change, 18 watch only, and 14 exclude. The full document also records 18 conflicts between influences and how each was resolved.

## Three things the spec gets wrong or oversells

1. The "19× connection reduction" comes from a model with mock inputs, not a measurement. Our number will be measured and stated as clients ÷ pool size.
2. "CoDel" in the spec's sources refers to three different algorithms. In a toy simulation of ours, the one in the cited paper lets a request queue grow without bound under heavy open-loop overload.
3. Two of the earlier projects the spec calls reusable have no product code, only plans and probes.

## What most shapes the design

1. "Fail at Scale" request-queue CoDel and adaptive LIFO, which now form one of two equal queue modes.
2. ZGateway's per-tenant queues, where each tenant's share binds only under saturation.
3. Pure decision cores, a generated retry contract, and a generated capacity table from mansio, paired with the object-store simulator's conventions.
4. The singleflight lineage: detach the shared call from any single caller, count waiters, and cap how many can wait.
5. The Google SRE book's 10% retry budget implemented with Finagle's integer bucket, plus the rule that a scenario must show recovery after the fault ends.
6. Open-loop load generation and Apollo's validity gates, scaled to one shared VM.
7. AIMD with an in-flight guard, sized by Little's law.
8. PgBouncer, Odyssey, Hyperdrive, and RDS Proxy, promoted to core by the move to Postgres.

Three cross-cutting choices apply across all of them: the snapshot metrics pattern from *Backpressure in Microservices*, *Release It!* as a checklist, and horizontal scale as a design input.

## Nick's steering, 2026-11-24

| # | Question | Answer | Against the recommendation? |
| --- | --- | --- | --- |
| 1 | Redis or Postgres | **Postgres.** Keep the backend behind an interface only if that stays honest | Yes — the list recommended Redis |
| 2 | Name | mini-gateway | Yes |
| 3 | Which CoDel | **Both**, as equal selectable modes | Yes — the list shipped one and kept the paper's law only as a comparison |
| 4 | Fairness | Per-tenant queues drained round robin, with shares enforced under saturation | No |
| 5 | Caching | None in v1 | No |
| 6 | Postgres influence | Superseded by 1 | — |
| 7 | Benchmark rigor | "Apollo-lite", plus a write-up of its trade-offs | Added a deliverable |
| 8 | Simulator | Evaluate the object-store simulator first, with evidence | Changed the process |
| 9 | Protocol scope | Simple-query subset, transaction pooling, session features rejected, and `pgproto3` as the one external package | Reshaped by 1 |
| 10 | Promote parked items | None | No |
| 11 | Reshape core items | GCRA as an outer gate, AIMD in v1, and batching without a timer (batching was later dropped from v1 by Nick's ruling of 2026-11-24) | No |
| 12 | Proxy retries | A policy behind an interface, **off by default** | Yes — the list had it on |
| 13 | Observability | The full snapshot system, with the OTLP push exporter last | No |
| 14 | Horizontal scale | Per-instance enforcement with a static fleet config, measured at three instances, plus graceful drain | No |

See [section 2 of the plan](../PLAN.md#2-steering-applied-and-where-it-overrides-influencesmd) for how each answer is carried out and every place it overrides the list.
