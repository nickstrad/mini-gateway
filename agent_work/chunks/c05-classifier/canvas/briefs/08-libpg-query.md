# CANVAS AGENT BRIEF — libpg_query / pg_query_go / wasilibs go-pgquery — the real parser as a library
# (mini-gateway chunk 5 canvas; mission /root/briefs/zgateway-c05-canvas-mission.md;
#  coordinator: Fable. You are a fresh Opus research agent. RESEARCH ONLY.)

## Why this canvas exists
mini-gateway is a Go proxy that speaks the Postgres wire protocol (simple-query
subset) in front of one ordinary Postgres, with a connection pool lent per
transaction and SHARED ACROSS TENANTS, and with coalescing of identical in-flight
reads. Its chunk 5 (statement classifier) must decide, from SQL text alone:
 (D3) does this statement change session state (settings, prepared statements,
      temp tables, LISTEN, session advisory locks, holdable cursors) — which must
      never cross tenants on a shared connection;
 (D4) is it provably read-only;
 (D5) is it safe to share/coalesce (one execution answers several callers).
Three review rounds found 11 holes. The working conclusion is that a deny-list
over SQL text cannot be completed and that safety should rest on mechanisms
(reset on release, allow-lists, server enforcement) rather than on reading.
The project owner (Nick) asks: which tools and projects out there claim to solve
this, and what algorithms/approaches do they ACTUALLY use? You check one cluster
of projects against their source code.

Read first (read-only, do not modify):
- /root/Software/mini-gateway/agent_work/chunks/c05-classifier/SCOPE-ASSESSMENT.md
  (options (a) continue the deny-list, (b) narrow + reset on release, (c1) real
  parser via cgo, (c2) let Postgres enforce read-only, (d) descope)
- /root/Software/mini-gateway/agent_work/chunks/c05-classifier/EXPLAINER.md
  (section 2: the lexical / grammar / catalog layers)

## Your cluster
libpg_query (https://github.com/pganalyze/libpg_query; C extracted from
PostgreSQL), pg_query_go (https://github.com/pganalyze/pg_query_go; Go + cgo),
wasilibs/go-pgquery (https://github.com/wasilibs/go-pgquery; the same library
compiled to WASM and run under wazero — no cgo). Clone all three (shallow).
Focus:
- What exactly the library exposes: scanner (pg_query_scan — tokens with
  positions, the real scan.l), raw parser (parse tree as JSON/protobuf),
  split (pg_query_split_with_scanner / _with_parser — statement boundaries),
  normalize, fingerprint, deparse, PL/pgSQL parsing. Cite the header and the Go API.
  Note what it does NOT do: no parse analysis, no catalog (cannot resolve
  functions, views, search_path, operator definitions, casts to volatile input
  functions).
- How it tracks PostgreSQL: which PostgreSQL version the current release embeds,
  release cadence relative to PostgreSQL majors (tags/dates), how the extraction
  works (scripts/extract_source.rb), patches carried.
- Does standard_conforming_strings or any other GUC change how the scanner reads
  text (so that the library and a live server can still disagree)? Look at how
  libpg_query sets `standard_conforming_strings`, `backslash_quote`,
  `escape_string_warning`. This is the residual lexical risk even with the real
  scanner — pin it down. Also encoding assumptions (client_encoding) and
  `quote_all_identifiers`-like switches.
- Integration cost in a Go proxy hot path — MEASURE IT (this cluster may build
  and run small Go programs; Go 1.26 is at /usr/local/go/bin, gcc should be
  present — check). In /root/scratch/c05-canvas/work/08-libpg-query/ write a
  small Go module with benchmarks (`go test -bench . -benchmem`, use
  `-cpu 1` and keep runs short, `GOFLAGS=-p=2` to be kind to the machine) for a
  short hot-key read (`SELECT v FROM kv WHERE k = 'a'`), a medium ORM-style
  query with quoted identifiers, and a 2 KB statement, measuring: Scan, Parse
  (JSON vs protobuf tree if both exist), Fingerprint, Split — for pg_query_go
  (cgo) and for wasilibs/go-pgquery (WASM). Record ns/op, B/op, allocs/op, the
  cold-start cost (first call / module instantiation for WASM), concurrency
  behaviour of the WASM build (one instance per goroutine? a pool? memory per
  instance), the build time and binary size delta versus a hello-world, and the
  cgo requirements (C compiler in CI, cross-compilation, static linking). If a
  build fails or is too slow (libpg_query cgo compile takes minutes — allow it
  up to about 8 minutes once), record that as a finding and fall back to the
  projects' own published numbers, citing them.
- For comparison, also note pure-Go alternatives if you meet them (for example
  the CockroachDB parser extracted as a library, `auxten/postgresql-parser`,
  `pganalyze`-independent scanners, pgx's `sanitize` lexer) — with one line each
  on fidelity to real PostgreSQL.
- Who uses it in a proxy hot path (PgDog via pg_query.rs; sqlc, pganalyze
  offline). Cite.
- Verdict for options (c1): what would using ONLY the scanner (exact lexical
  layer, statement splitting) buy versus the full parse tree, and what stays
  unsolved (the grammar-layer lists, the catalog layer).

## Hard rules
- Research only. Do NOT write, commit, or change anything under
  /root/Software/mini-gateway or /root/Software/mini-gateway-work (reading the two
  files above is fine). Do not touch any git branch there. Commit nothing anywhere.
- Work only under /root/scratch/c05-canvas/. Clone into
  /root/scratch/c05-canvas/repos/<project> with `git clone --depth 1` (reuse a
  clone that already exists). Scratch programs go in
  /root/scratch/c05-canvas/work/08-libpg-query/.
- Disk is tight (about 20 GB free for 12 agents): stay under 1.5 GB. For huge
  repositories never take a full tree: use
  `git clone --depth 1 --filter=blob:none --sparse <url>` followed by
  `git sparse-checkout set <dirs>`, or read single files with
  `gh api repos/<owner>/<repo>/contents/<path>` / raw.githubusercontent.com /
  `gh search code`. `gh` is logged in. Do not build the projects unless your
  cluster section says so.
- Web: use your built-in web search/fetch if you have them; otherwise
  `/root/Software/web-tools/websearch search "<query>"` and
  `/root/Software/web-tools/websearch fetch <url>`.
- The machine has 4 cores and 7 GB RAM and three other agents run beside you:
  no heavy builds, no parallel fan-out of subagents.
- Evidence discipline (this is the point of the canvas): EVERY claim about how a
  project works cites a file path and line (or function name) in the clone at the
  commit you record, or a doc URL. Quote the decisive code in short excerpts
  (at most about 15 lines each). Mark each claim as one of: VERIFIED IN CODE /
  DOCS ONLY / COULD NOT FIND. "Their README says" is not evidence of mechanism;
  find the code or say you could not. Do not answer from memory: if you remember
  something and cannot find it, say so in exactly those words.
- For each project record: clone URL, commit SHA read (`git rev-parse HEAD`), the
  commit's date, newest release tag/date if cheap to get, and whether findings
  come from LOCAL SOURCE or WEB READS.

## Questions to answer for every project in your cluster
1. Does it read SQL? With what — nothing, substring/regex matching, a hand lexer,
   a derived grammar, or the real PostgreSQL parser? Where does that code live,
   how big is it, and how is it kept in step with PostgreSQL releases?
2. Session-state safety: what is the mechanism (reset on release / pin the client
   to the connection / track and replay state / reject / document-and-shrug), where
   in the code does it live, what triggers it, and what is the default?
3. Read-vs-write and shareability/cacheability decisions, if any: allow-list or
   deny-list? What exactly is on the list? What does a miss cost IN THAT SYSTEM
   (wrong data, a write on a replica that errors, cross-client leak, only
   performance)? Does the system fail open or fail closed on text it does not
   understand (parse error, unknown statement, unknown function)?
4. Documented limitations on session features — quote the project's own words,
   with the URL or doc path.
5. Language, license, maturity/activity (first release, latest release, commit
   recency), dependency weight (what a Go proxy would have to take on to reuse it).

## Probe statements
By READING the code (run things only if it is cheap and your cluster section says
so), say what the project does with each probe that applies to it, and cite the
code path. Use "n/a" where the project has no such decision.
- P1  `SET search_path = x`                       (plain session SET)
- P2  `SELECT set_config('search_path','h',false)` (session change through a function)
- P3  `SELECT f()` where the user function f runs SET / creates a temp table
- P4  a session change hidden by lexical tricks: comments, dollar quotes,
      `E'..'` escapes, string continuation across a newline, e.g.
      `SELECT E'ok'\n'\' || set_config(...) --'`
- P5  `PREPARE p AS ...`, `LISTEN c`, `CREATE TEMP TABLE t(...)`,
      `SELECT pg_advisory_lock(1)`, `DECLARE c CURSOR WITH HOLD ...`
- P6  several statements in one simple Query: `SELECT 1; SET search_path = x`
- P7  for read/write or cache decisions: `SELECT nextval('s')`,
      `WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d`,
      `SELECT * FROM t FOR UPDATE`, `SELECT now()`, `SELECT * FROM v` (a view over
      a volatile function), `EXPLAIN ANALYZE DELETE FROM t`

## Also report
- Does the project CLAIM anywhere (docs, README, talks) to decide safety from SQL
  text completely? Does the claim survive the code you read?
- Which of the assessment's options — (a), (b), (c1), (c2) — the project's
  approach corresponds to, or "none of them" and why.
- MECHANISMS NOT IN THE ASSESSMENT: anything the assessment and explainer did not
  consider — a mechanism, an algorithm, a trick (for example: using a protocol
  signal instead of text, asking the server, tagging connections by state). Be
  concrete about how it works and what it would take in a Go proxy in front of
  vanilla Postgres. If you find none, say so.
- A better candidate project than those listed, if you meet one: give it the
  same questions in a shorter treatment.

## Deliverable
One markdown file: /root/scratch/c05-canvas/findings/08-libpg-query.md, at most about
600 lines, with this structure:
  # <cluster title>
  ## Sources            (table: project, URL, commit SHA, commit date, local/web)
  ## Summary row(s)     (table, one row per project: Project | Reads SQL with |
                         Session-state mechanism | Read/write and share decisions |
                         List type | Cost of a miss | Fails open or closed)
  ## <Project>          (one section per project: answers 1–5 with citations and
                         excerpts, then its probe table P1–P7)
  ## Claims of completeness
  ## Mapping to options (a) (b) (c1) (c2)
  ## Mechanisms not in the assessment
  ## Could not verify   (everything you looked for and did not find)
Write plain, precise prose. Prefer a short accurate file over a long padded one.
Write the file incrementally (create it early, extend it as you go) so that
partial work survives if you are interrupted. Budget: about 40 minutes.
When the file is complete, print a five-line summary and then a final line
containing exactly AGENT-DONE.
