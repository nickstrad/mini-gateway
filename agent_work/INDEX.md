# agent_work index

Scripts and research artifacts for mini-zgateway. Every script any agent produces on this project is saved here and indexed: name, purpose, phase/chunk, author seat, what it proved / last result.

## Phase 0 — influences research (Fable orchestrator, 2026-11-24)

| Path | Purpose | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `phase0/briefs/_common.md` | Shared rules for every research brief: honesty labels (read directly / secondary only / could not verify), the per-influence entry format, no repo edits | Fable | Appended to each bucket brief at dispatch |
| `phase0/briefs/b1-spec-named.md` | Research brief: verify every reference in spec §6 from the real source (ZGateway, Hyperdrive, Canva, Fail at Scale, CoDel paper, Apollo) | Fable | See `phase0/research/b1-spec-named.md` |
| `phase0/briefs/b2-prior-projects.md` | Research brief: Nick's prior repos on vm-1 (analytics-edge-gateway, zeromq-go, object-store, mansio, sqlite-internals) — reusable ideas, non-reusable code, carried rulings | Fable | See `phase0/research/b2-prior-projects.md` |
| `phase0/briefs/b3-industry-systems.md` | Research brief: unnamed industry systems (PgBouncer, Envoy, Netflix concurrency-limits, Finagle, SRE book, McRouter/Twemproxy, HAProxy, Go singleflight, Vitess…) | Fable | See `phase0/research/b3-industry-systems.md` |
| `phase0/briefs/b4-foundations.md` | Research brief: foundational algorithms reduced to exact mechanisms (CoDel, Adaptive LIFO, GCRA, token bucket, DRR, AIMD/Vegas/gradient, Little's Law, balls-into-bins, coordinated omission, HDR, benchmark statistics, DST lineage) | Fable | See `phase0/research/b4-foundations.md` |
| `phase0/dispatch-research.sh` | Conductor: launches the bucket research agents headlessly (fresh `claude-fable-5-2` each, scratch cwd under `/tmp/zgateway-phase0/`, brief copies in `/root/briefs/zgateway-phase0-*.md`), waits with a deadline, writes `/root/runs/zgateway-phase0-<bucket>.exit`, fails closed if an output is missing | Fable | Result recorded below once the run finishes |
