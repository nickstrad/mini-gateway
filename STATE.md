# STATE — mini-gateway

RENAMED 2026-11-24 (Nick, steering answer 2): the project is **mini-gateway** (was mini-zgateway). Repository github.com/nickstrad/mini-gateway, checkout /root/Software/mini-gateway, site https://nickstrad.github.io/mini-gateway/. History was not rewritten; INFLUENCES.md, docs/project-source.md and the Phase 0 records under agent_work/ keep the old name and their Redis-first recommendations.

Goal: build mini-gateway (spec: [docs/project-source.md](docs/project-source.md), as changed by Nick's steering) — a Go proxy that speaks the Postgres wire protocol (simple-query subset) in front of one ordinary Postgres: a hard-capped connection pool lent per transaction, sharing of identical in-flight reads (never writes), timer-free batching, admission queues with two selectable CoDel modes and adaptive LIFO, per-tenant fairness, an adaptive concurrency limit, a retry policy that is off by default, distinct errors for "refused before anything reached the database" versus "failed after", snapshot-based observability in every component, and a static N-instance fleet configuration. Testing-first: the deterministic simulator lands before any proxy code.

Plan: [PLAN.md](PLAN.md) — a PROPOSAL, not yet approved. Diagrams: [docs/c4/](docs/c4/README.md) (target architecture).

Status: **PHASE 1 PROPOSAL AWAITING NICK'S APPROVAL (2026-11-24).** HARD GATE: no proxy code, no chunk builds, no builder dispatch until Nick approves the proposal in chat. Waiting is correct behaviour, not a stall.

Seats (this project only; ruling: /root/briefs/zgateway-seat-ruling.md): Fable orchestrates, gates, lands and owns the C4 diagrams; Opus writes product Go (logic only), fresh agent and worktree per chunk; Gemini Neon (via Antigravity `agy`) writes all reader-facing prose once, late, per chunk; Astra (gpt-6-astra) writes adversarial harnesses per chunk. Who reviews Opus's code is open question 1 below.

Done:
- Phase 0: scaffold `0005af5`; research `e1992eb`, `8fc77f5`; INFLUENCES.md `1c6eecf`, addendum `dbd79d0` (100 influences, 18 conflicts, 14 steering questions).
- Steering: Nick answered all 14 questions on 2026-11-24 (/root/briefs/zgateway-steering-answers.md). They govern; PLAN.md section 2 lists every place they override INFLUENCES.md.
- Phase 1 evidence, commit `43f8c33`: a probe against a real Postgres 17 (`agent_work/phase1/probes/pgwire/`), the evaluation of the object-store simulator and the *Release It!* mining report (`agent_work/phase1/research/`). Indexed in `agent_work/INDEX.md`.
- PLAN.md, commit `5c83fe6`: 17 chunks after a foundation step, each with purpose, deliverables, its end-to-end tests, observability duties, seats and landing criteria; the scenario-to-chunk table; testing and observability architecture; the fleet table; error, timeout and capacity contracts; the *Release It!* mapping; the benchmarking method and its trade-offs write-up.
- The two verdicts the steering asked for:
  - Simulator (steering answer 8): the object-store simulator does not fit as a whole (no byte-stream connections; about 8% of its code survives unchanged). The plan writes a seeded event-loop kernel in this repository and copies in the clean pieces (named random streams, seed and replay plumbing, event heap, determinism lint). A shared kernel for both projects is a follow-up after this project, outside v1.
  - Backend interface (steering answer 1): a generic backend interface would misdescribe Postgres. The plan is Postgres-first with three tested seams (a protocol-free byte transport under the engine, one package holding everything Postgres, a small work vocabulary that is all the cores see); the generic interface waits for a second protocol.
- C4, commit `eacd1f4`: 13 target-architecture diagrams generated from `docs/c4/gen.py` (context, two container views, three-instance deployment, nine component views); the c4-sdk model decodes with 0 errors and all 13 scenes build.
- Observability amendment (Nick, 2026-11-24; /root/briefs/zgateway-observability-csv-duckdb-amendment.md) folded into PLAN.md and C4 diagram 13 in the commit `plan: fold in the observability amendment`: the rule "one raw export → collector → adapters", the CSV flush with schema version 1 as the testing path, DuckDB over the flushed files (analysis only), and OTLP → Collector → Prometheus with a docker-compose dev setup in chunk 16, still the last build chunk. Chunk list, order and the 8 open questions are unchanged.
- Review site, commit `1e2e582`: workflow `.github/workflows/pages.yml`, builder `.github/scripts/build_site.py`. Verified live 2026-11-24 after the Pages run succeeded: all 18 pages and `c4/model.json` return HTTP 200 and all 13 diagram pictures draw in a headless browser (`agent_work/phase1/check-mermaid.py https://nickstrad.github.io/mini-gateway/`).

In progress: nothing. Waiting at the gate.

Next steps, in order: (1) Nick reviews the proposal on the site and answers the open questions; (2) the orchestrator applies his answers to PLAN.md and the C4 set; (3) only then: chunk 0 (foundation: module, gates, CI, scripts, book scaffold), then chunk 1 (simulator kernel) and chunk 2 (simulated world) — the simulator harness that must land before any proxy code.

Open questions for Nick (full text in PLAN.md section 18):
1. Who reviews Opus's code here — Neon (project seat ruling of 2026-11-24) or Fable only (global rule of 2026-11-11)?
2. Shared reads: default to "never older than the request" (slower under a stampede) or "join the read already running" (faster, brief stale-read gap)? Proposed: the first, with the second selectable.
3. Batching under Postgres means pipelining independent single statements onto one connection. Proposed: build it, off by default until measured. Or drop that chunk.
4. Tenant = the client's startup user name, checked by the proxy; all pooled connections use one database role. Acceptable for v1?
5. No TLS in v1; SCRAM-SHA-256 and trust only; client cancel requests supported. Agree?
6. The *Release It!* notes lack the book's material on pooled connections dying silently. Consult the book first? (The PDF is on Drive; the Drive connector was not authorized for this run.)
7. Size: 17 chunks, roughly 20,000 lines of product code. Go as proposed or apply the cuts listed in PLAN.md section 16?
8. Approval of the "end-to-end with every chunk" wording draft is still unrecorded; the plan follows the rule as standing.

Other open items:
- Terminal (Unicode) renderings of the C4 diagrams are not in this first set; the diagrams have a c4-sdk model and Mermaid pages. Owner: Fable, at chunk 0, unless Nick wants them before approval.
- README.md and the site's pages were written by the orchestrator as plain status pages; Neon's prose starts with the first chapter.
- vm-1 Claude sign-in expiry: warned to end sometime Nov 25–28, 2026 — renew before then (owner: Nick/Twin).
- Tooling: the `kb` on PATH cannot open the VM store from this repo; run `./kb/.cache/kb` from /root/Raw/knowledge (owner: Twin). The system Python on vm-1 has no pip; see `docs/knowledge/phase1-findings.md` for building the site locally.

Resume instructions: branch `main`, no worktrees, no background runs alive. Pushing: `origin` is an HTTPS URL and the gh token has no `workflow` scope, so push over SSH — `git push git@github.com:nickstrad/mini-gateway.git main` (the remote URL was left as it was). Read this file, then PLAN.md. Done marker: /root/runs/zgateway-phase1-done.txt; progress log: /root/runs/zgateway-phase1-progress.txt. If no approval from Nick is recorded (a brief under /root/briefs/ or an instruction in chat), do nothing — the gate is closed. After approval: apply his answers to PLAN.md and docs/c4 (edit `docs/c4/gen.py`, run it, re-run `agent_work/phase1/c4-check.mjs`), then write the chunk 0 brief.
