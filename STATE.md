# STATE — mini-zgateway

Goal: build Mini-ZGateway (spec: docs/project-source.md) — a Go storage proxy in front of a single vanilla Redis (Variant A, recommended) or Postgres (Variant B) backend: bounded multiplexed connection pool, in-flight read coalescing (never writes), micro-batching, CoDel + Adaptive LIFO admission, per-tenant fairness / noisy-neighbor isolation, adaptive concurrency, retry budgets, distinct pre-forward vs post-forward error semantics — designed testing-first, deterministic simulation harness (Commit 0) before any proxy code.

Status: PHASE 0 COMPLETE, ADDENDUM LANDED (2026-11-24) — awaiting Nick's steering on INFLUENCES.md. HARD GATE: nothing further is authorized. No PLAN.md, no C4 diagrams, no product code, no builder dispatch until Nick's steering arrives (Twin relays it as a file). Waiting is correct behaviour, not a stall. After steering: PLAN.md + initial C4 + Pages review site, then STOP again for Nick's approval of that proposal.

Seats (this project only; ruling: /root/briefs/zgateway-seat-ruling.md): Fable orchestrates, gates and lands; Opus writes product Go (logic only), fresh agent and worktree per chunk, concurrently where the dependency graph allows; Gemini Neon reviews Opus and writes ALL reader-facing prose once, late, per chunk (via Antigravity `agy` by default per Nick's 2026-11-24 ruling; OpenCode/OpenRouter are fallback only); Astra (gpt-6-astra) writes adversarial harnesses per chunk. Mission: /root/briefs/zgateway-orchestrator-mission.md.

Done:
- Scaffold commit `0005af5` (Twin) — repo, spec copy, STATE.md, agent_work index, docs/c4 placeholder, Pages enabled.
- Phase 0 research: four fresh Fable research agents (one per bucket), briefs and conductor in `agent_work/phase0/` (commit `e1992eb`); all exited 0; notes in `agent_work/phase0/research/`, probes in `agent_work/phase0/probes/`.
- INFLUENCES.md at the repo root (original merge, commit `1c6eecf`): 97 influences, 18 conflicts, variant implications, 12 steering questions, plan consequences.
- Phase 0 addendum (Nick's three directions, `/root/briefs/zgateway-observability-scale-releaseit-note.md`; amendment brief from Twin): three entries added — F29 component-snapshot metrics, F30 *Release It!*, I36 horizontal scale of the proxy tier — plus steering questions 13 and 14, an addendum note in sections 1 and 6. INFLUENCES.md now holds 100 influences (14 spec-named, 20 prior projects, 36 industry systems, 30 foundations; 28 ADOPT, 40 ADOPT WITH CHANGE, 18 WATCH ONLY, 14 EXCLUDE), 18 conflicts, 14 steering questions. Commit SHA is in /root/runs/zgateway-influences-amendment-done.txt and `git log -- INFLUENCES.md`. All three directions remain mandatory inputs to PLAN.md + C4 whatever Nick answers on 13 and 14.

In progress: nothing. Waiting at the gate.

Next steps: (1) Twin presents INFLUENCES.md sections 1 and 5 (now 14 questions) to Nick; (2) Nick steers by question number; Twin relays the steering as a file; (3) a fresh Fable orchestrator run reads this file, INFLUENCES.md and the steering file, then produces PLAN.md + kickoff C4 + Pages review site and stops for proposal approval.

Steering questions awaiting Nick (INFLUENCES.md section 5; Fable's recommendation in brackets):
1. Variant [A — Redis RESP — as v1, cores kept protocol-free so B can be v2].
2. Repo name `mini-zgateway` [keep].
3. Which CoDel, and reword the spec invariant ["Fail at Scale" timeout rule per tenant queue, LIFO on the same signal with a head sweep; invariant becomes "served wait ≤ N, depth bounded, backend never idled"; the paper's law and folly's rule only as simulator comparison arms].
4. Fairness mechanism [per-tenant queues drained round-robin, share enforced only under saturation; GCRA as an optional outer gate].
5. Caching [none in v1; coalescing only].
6. Postgres influence carried under A [pool-management ideas and a README contrast only; no Postgres code].
7. Benchmark rigor ["Apollo-lite": simulator evidence first, open-loop generator, A/A run, ≥10 interleaved runs, achieved-rate gate, exact counts as headlines; pool 32, Zipf s = 1.2, the 19× labelled a model].
8. Simulator architecture and kernel location [sans-I/O cores on a single-threaded seeded event loop + goroutine shell under synctest and -race; kernel in-repo; CI fast-only, no test-only deps, e2e per chunk].
9. Redis compatibility in v1 [RESP2 only, allow-list, stateful commands rejected].
10. Promote any EXCLUDE/WATCH item [only the comparison arms already in 3].
11. Demote/reshape any CORE or spec item [GCRA to outer gate; AIMD in v1, not optional; batching timer-free].
12. Does the proxy itself retry [only when no byte reached the backend, once, under a 10% budget].
13. Observability scope for v1 [every component ships a snapshot, held by a landing gate; exporters Prometheus text, JSON stats, CSV, log line and a hand-written OTLP/HTTP push exporter landed last, all standard library; bounded tenant label in Prometheus, full per-tenant detail in JSON/CSV; lock-wait timing and per-key hotness opt-in].
14. Horizontal-scale stance for v1 [per-instance enforcement with a static instance-aware config; N instances is a supported deployment; each guarantee stated as multiplies / weakens / survives at N and the weakened ones measured in 3-instance scenarios; no run-time coordination, which stays a documented seam].

Open decisions / blockers:
- All fourteen steering questions above — owner: Nick.
- vm-1 Claude sign-in expiry: warns it ends sometime Nov 25–28, 2026 — renew before then (owner: Nick/Twin). Phase 1 must start from files; nothing lives only in a session.
- Shared simulator-kernel repo (Nick's 2026-11-21 preference, `/root/briefs/shared-sim-ruling-note.md`): no such repo exists; folded into question 8.
- Incremental-e2e guide wording (`/root/briefs/flow-guide-e2e-wording-draft.md`) has no recorded approval, though the rule is treated as standing — owner: Nick.
- Addendum limits, stated in INFLUENCES.md: *Release It!* was read through Nick's on-machine notes (`/root/Software/book-notes/books/release-it/`), not the book (the PDF is on Drive, not reachable from this run); the kb store has no Release It! entry; the Backpressure book's companion repository was not fetched.
- Not verified, stated in INFLUENCES.md: the two "Master" companion Google Docs (no local copy); ZGateway's formula/table images; Apollo post date; Canva Part 2 (unpublished).
- Tooling note: the `kb` on PATH cannot open the VM store at /root/Raw/knowledge (no kb.toml there); `/root/Raw/knowledge/kb/.cache/kb` run from that directory works. Owner: Twin — fix or record in kb.

Resume instructions: branch `main`, no worktrees, no background runs alive. Read this file, then INFLUENCES.md, /root/briefs/zgateway-orchestrator-mission.md, /root/briefs/zgateway-observability-scale-releaseit-note.md, and the steering file once Twin writes it; check `git log` and /root/runs/zgateway-orchestrator-progress.txt. If no steering file exists, do nothing — the gate is still closed.
