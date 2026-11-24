# Brief B2 - Nick's own prior projects on this machine

Read `_common.md` rules below first (they are appended to this brief).
Output file: /tmp/zgateway-phase0/out/b2-prior-projects.md

All sources are LOCAL repos (read-only; do not modify, build artifacts
into, or commit in them). For VM knowledge, the kb store works only via
the older binary from its own directory:
`cd /root/Raw/knowledge && /root/Raw/knowledge/kb/.cache/kb search "<q>" --caller claude`
then `... kb show <entry>`; record feedback with `... kb feedback N <rank> --useful`
or `... kb feedback N --none --note "..."` for each search you act on. Never grep the
corpus directly. One known-relevant entry:
`data/go-deterministic-goroutine-replay-baton-scheduler.md`.

Projects to study (read STATE.md, PLAN.md, docs/knowledge/, AGENTS.md,
docs/project-source.md, then the code that matters):

1. /root/Software/analytics-edge-gateway - "Project 2: High-Throughput
   Analytics & Edge Gateway (Go)". GCRA / token-bucket rate limiter,
   workload-fuzzing / DST harness patterns, load generator, any
   benchmark-rigor rulings, per-tenant anything.
2. /root/Software/zeromq-go - "Project 4: ZeroMQ remake". Sans-I/O
   framing, the simulator (baton scheduler vs testing/synctest decision,
   PLAN.md section 8.1 + Appendix A, agent_work/probes/phase1),
   backpressure / HWM design, how incremental chunks were gated.
3. /root/Software/object-store (formerly decentralized-object-store;
   worktrees in /root/Software/object-store-work) - sim kernel, the
   "incremental e2e per chunk" rule, the multi-agent flow rulings
   (seats, adversarial testing, book chapters, C4), what went wrong.
4. /root/Software/mansio (+ mansio-work) - per-database isolation as the
   unit, noisy-neighbor framing, any admission/limits design.
5. sqlite-internals (/root/Software/sqlite-internals-work, and kb
   entries sqlite-internals-ch10a/b) - pooling / WAL / single-writer
   context ONLY if it bears on a storage proxy; say so if it does not.
6. Also skim for relevance and report honestly (include or exclude with
   a reason): /root/Software/dst-research, /root/Software/let-it-race-analysis,
   /root/Software/tigerbeetle (VOPR simulator precedent),
   /root/Software/distributed-transactional-kv, /root/Software/walfleet,
   /root/Software/durable-actor, /root/Software/mini-durable-objects,
   /root/Software/research, /root/Software/systems-trinkets.

For EACH project, in addition to the common entry fields, state:
- what is reusable here as an IDEA or pattern (name the file and the
  function/type so the planner can cite it),
- what is explicitly NOT reusable code (the standing rule is that each
  project implements its own code; say if a repo's rules confirm that),
- any RULING from that project (recorded in its STATE.md / AGENTS.md /
  docs/knowledge / rulings files / /root/briefs/*) that carries over to
  a new multi-agent Go project - quote the ruling's file path,
- lessons / failures recorded there that this project should not repeat
  (e.g. replay-determinism traps, flaky gates, benchmark claims that
  had to be narrowed).
Also list concrete measured facts worth carrying (e.g. simulator
steps/s, vm-1 has no hardware perf counters, go version on vm-1).
