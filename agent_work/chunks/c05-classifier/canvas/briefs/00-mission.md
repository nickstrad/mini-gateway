# CANVAS MISSION — how open-source projects solve the classifier problem
# (Nick, 2026-11-25, relayed by Twin. Research only: no changes to chunk 5
# code, the parked branch, or PLAN.md. Chunk 5 stays parked.)

## The problem being canvassed (mini-gateway chunk 5, statement classifier)
A Postgres wire-protocol proxy with transaction pooling must decide, from
SQL text alone: (1) does this statement change session state (must never
cross tenants on a shared connection), (2) is it provably read-only,
(3) is it safe to share/coalesce. Three review rounds found 11 holes; the
working conclusion is that a deny-list over SQL text cannot be completed,
and that safety should rest on mechanisms (reset on release, allow-lists,
server enforcement) instead of reading. Nick asks: what tools and projects
out there theoretically solve this, and what algorithms/approaches do they
actually use? This canvas checks those claims against the source code.

## How to run it
- You (Fable) coordinate. Dispatch FRESH Opus agents in parallel using the
  established headless runner pattern (brief file per agent under
  /root/briefs/, log + exit under /root/runs/, IS_SANDBOX=1 claude -p
  --dangerously-skip-permissions). One agent per project cluster below;
  run at least 3 concurrently. Each agent writes its findings as a markdown
  file; you synthesize.
- Scratch space: /root/scratch/c05-canvas/repos/ — clone there
  (git clone --depth 1). Small projects: full shallow clone. Huge ones
  (CockroachDB, YugabyteDB, ShardingSphere): partial/sparse clone or
  targeted file reads via GitHub — do not pull multi-GB trees. Record for
  each project whether findings come from local source or web reads.
- Evidence discipline: every claim about a project's approach must cite a
  file path in the clone (or a doc URL) — e.g. where its parser lives,
  where the session/reset/pin logic lives. "Their README says" is not
  evidence of mechanism; find the code or say it could not be found.

## Project clusters (extend if an agent finds a better candidate)
1. PgBouncer — the baseline. Does it parse SQL at all? reset query on
   return (server_reset_query / DISCARD), pool modes, documented session
   breakage.
2. PgCat (Rust) — parses queries (which parser?) for sharding/pooling;
   how does it treat session state in transaction mode?
3. Odyssey (Yandex) — pooling modes, any statement tracking, reset
   behavior.
4. Pgpool-II — ships a parser derived from PostgreSQL's; what does it
   classify (load balancing read/write split, query cache) and how does
   it handle session-state statements? This is the closest thing to
   option (a) in production — judge its completeness story.
5. PgDog — reportedly uses the real PostgreSQL parser; verify which
   parser and what decisions it drives (sharding, read/write split).
6. Supavisor (Supabase) — transaction mode design; prepared statements
   and session state handling; what it documents as unsupported.
7. Apache ShardingSphere (proxy) — full ANTLR PostgreSQL grammar; what
   classification does it do and what does maintaining that grammar cost
   (evidence: grammar size, update cadence)?
8. pg_query_go / libpg_query (and the wasilibs WASM variant) — the real
   parser as a library: what exactly it exposes (scanner, parse tree,
   fingerprint), integration cost in a Go proxy hot path.
9. Wire-compatible distributed databases — CockroachDB and YugabyteDB
   (YSQL): they "solve" the problem by being the server. State precisely
   what problem disappears, what new one appears, and why this is or is
   not an answer for a proxy in front of vanilla Postgres. (Targeted
   reads only; no full clones.)
10. Any other serious candidate an agent finds (pgagroal, ProxySQL as
    the MySQL-world analogue, Citus's router, Postgres-XL): same
    questions, shorter treatment.

## Questions every agent answers for its projects
1. Does it read SQL? With what — nothing, a hand lexer, a derived
   grammar, or the real PostgreSQL parser?
2. Session-state safety: mechanism (reset / pin / reject / document-and-
   shrug) and where in the code it lives.
3. Read-vs-write and shareability decisions, if any: allow-list or
   deny-list, and what a miss costs in that system.
4. Documented limitations (the project's own words) on session features.
5. Language, license, maturity/activity, dependency weight.

## Deliverable
/root/Software/mini-gateway/agent_work/chunks/c05-classifier/CANVAS.md,
committed and pushed on main, plus a one-line pointer in STATE.md:
- A comparison table across all projects (parses? mechanism for session
  safety? read/share decisions? miss cost?).
- Per-project notes, each with source-cited evidence.
- A closing section answering Nick directly: (i) does any project claim
  a complete solution to deciding safety from SQL text — and does the
  claim survive reading its code? (ii) which wild approaches map to
  options (a), (b), (c1) in SCOPE-ASSESSMENT.md; (iii) anything genuinely
  NEW the canvas found that the assessment and explainer did not
  consider — a mechanism, an algorithm, a trick — with your judgment on
  whether it changes the recommendation.
Print CANVAS_DONE and the commit SHA when finished.
