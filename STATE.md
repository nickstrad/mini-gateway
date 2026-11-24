# STATE — mini-zgateway

Goal: build Mini-ZGateway (spec: docs/project-source.md) — a Go storage proxy in front of a single vanilla Redis (Variant A, recommended) or Postgres (Variant B) backend: bounded multiplexed connection pool, in-flight read coalescing (never writes), micro-batching, CoDel + Adaptive LIFO admission, per-tenant fairness / noisy-neighbor isolation, adaptive concurrency, retry budgets, distinct pre-forward vs post-forward error semantics — designed testing-first, deterministic simulation harness (Commit 0) before any proxy code.

Status: PHASE 0 (INFLUENCES) — Fable orchestrator mission briefed 2026-11-24. HARD GATES, in order: (1) INFLUENCES.md first — an expanded ("buffed") influences list with Fable's suggestions — and STOP for Nick's steering; (2) only after that steering, PLAN.md + initial C4 diagrams as the initial proposal, then STOP for Nick's approval; (3) no building until Nick approves the proposal. No C4, no PLAN.md, no product code in Phase 0.

Seats (this project only; ruling: /root/briefs/zgateway-seat-ruling.md): Fable orchestrates, gates and lands; Opus writes product Go (logic only), fresh agent and worktree per chunk, concurrently where the dependency graph allows; Gemini Neon reviews Opus and writes ALL reader-facing prose once, late, per chunk (via Antigravity `agy` by default per Nick's 2026-11-24 ruling; OpenCode/OpenRouter are fallback only); Astra (gpt-6-astra) writes adversarial harnesses per chunk. Mission: /root/briefs/zgateway-orchestrator-mission.md.

Done: scaffold commit (Twin) — repo created, spec copied, this STATE.md, agent_work/INDEX.md, docs/c4/ placeholder, Pages enabled (workflow build type).

In progress: Phase 0 influences (Fable).

Next steps: (1) Fable completes INFLUENCES.md and writes /root/runs/zgateway-influences-done.txt; (2) Twin presents the influences + steering questions to Nick; (3) Nick steers; Twin relays steering as a file; (4) Fable then produces PLAN.md + kickoff C4 + Pages review site and stops for proposal approval.

Open decisions / blockers:
- Repo name `mini-zgateway` was Twin's choice (spec names the project Mini-ZGateway but no repo) — Nick to confirm or rename.
- Variant choice (A Redis RESP / B Postgres wire / C as v2) is a steering question in INFLUENCES.md, not pre-decided.
- vm-1 Claude sign-in expiry: warns it ends sometime Nov 25–28, 2026 — renew before then; keep this file resumable at all times.

Resume instructions: read this file, then INFLUENCES.md (once it exists) and /root/briefs/zgateway-orchestrator-mission.md; check git log; check /root/runs/zgateway-orchestrator-progress.txt.
