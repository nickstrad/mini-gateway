# CANVAS AGENT BRIEF — RDS Proxy, Cloudflare Hyperdrive, ReadySet, (and similar) — pinning and SQL-driven caching
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
RDS Proxy (closed source; AWS docs only), Cloudflare Hyperdrive (closed source;
docs and engineering blog only), ReadySet (https://github.com/readysettech/readyset;
Rust; source-available — LARGE: sparse/blobless clone of the relevant crates such
as readyset-adapter, readyset-psql, nom-sql/readyset-sql-parsing, or web reads).
If you meet another serious caching or managed proxy with public mechanism
detail (Heimdall Data, PgBouncer forks such as AWS pgbouncer-rr-patch, Neon's
proxy, Prisma Accelerate, Google AlloyDB managed pooling, Azure built-in
PgBouncer), add a short treatment. For closed-source products every claim is
DOCS ONLY — say so and cite the URL; quote their words.
Focus for RDS Proxy:
- The pinning model: the documented list of conditions that pin a PostgreSQL
  client to its connection
  (https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-proxy-pinning.html
  and rds-proxy-managing.html / rds-proxy.howitworks.html). What does the doc say
  about HOW it detects them (text of statements? protocol?), about statements it
  cannot see into (functions/procedures that SET), about multi-statement queries
  (pin), about statement size (>16 KB pins), about `SET` tracking for a known
  variable list (MySQL: session variables tracked; PostgreSQL: SET pins?), and
  about session pinning filters (`EXCLUDE_VARIABLE_SETS`) — "tell the proxy to
  trust me". Record the fail direction: unsure → pin.
- Does AWS claim completeness? Quote the caveats.
Focus for Hyperdrive:
- Its query cache: Cloudflare states which queries are cacheable — "non-mutating"
  queries, and NOT those calling volatile/stable functions; how do they say they
  decide (parsing the query; a list of functions such as `now()`, `random()`,
  `current_timestamp` ...)? Docs:
  https://developers.cloudflare.com/hyperdrive/configuration/query-caching/ and
  blog posts. Quote the exact rule and the stated limits (for example text-match
  on function names causing false positives). This is the closest public
  precedent to our D5 (share a read between callers).
- Its pooling mode (transaction mode) and what it documents about SET, prepared
  statements, advisory locks; any reset (`DISCARD ALL`/`RESET ALL`) on return.
Focus for ReadySet:
- A cache that answers SELECTs from its own dataflow and PROXIES everything else
  upstream. How does it decide a query is cacheable — it must be parsed by its
  own parser AND supported by its planner, and caches are created explicitly
  (`CREATE CACHE`) or after seeing the query; unsupported or unparsable →
  proxied to the upstream on a per-client upstream connection (fail closed for
  correctness). Find: the parser crate(s) (nom-sql; newer sqlparser-rs
  migration), the fallback path in readyset-adapter (`backend.rs`: "fallback",
  `QueryStatus`, `MigrationState::Unsupported`), handling of SET statements
  (allow-list of SETs it tolerates; `unsupported_set_mode` = error/proxy/allow —
  proxied-and-then-ALL-queries-go-upstream), functions (non-deterministic
  built-ins such as now() are refused for caching — find the list), and whether
  each client keeps its own upstream connection (so session state is never
  shared → D3 does not arise). This is an "allow-list by construction" design:
  only what the engine can itself execute is shared.
- Lesson for D5: positive recognition ("I can prove what this is because I can
  execute it myself") versus exclusion lists.

## Hard rules
- Research only. Do NOT write, commit, or change anything under
  /root/Software/mini-gateway or /root/Software/mini-gateway-work (reading the two
  files above is fine). Do not touch any git branch there. Commit nothing anywhere.
- Work only under /root/scratch/c05-canvas/. Clone into
  /root/scratch/c05-canvas/repos/<project> with `git clone --depth 1` (reuse a
  clone that already exists). Scratch programs go in
  /root/scratch/c05-canvas/work/11-managed-and-caching-proxies/.
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
One markdown file: /root/scratch/c05-canvas/findings/11-managed-and-caching-proxies.md, at most about
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
