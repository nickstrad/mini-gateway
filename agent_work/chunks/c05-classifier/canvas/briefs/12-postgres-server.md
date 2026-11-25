# CANVAS AGENT BRIEF — PostgreSQL itself — the server-side mechanisms a proxy can lean on
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
PostgreSQL (https://github.com/postgres/postgres; C). Shallow depth-1 clone of
master is acceptable (about 100–150 MB on disk); also consult the docs for the
current release (https://www.postgresql.org/docs/current/). Do not build it.
A PostgreSQL 17 server may be reachable through Docker on this machine (the
mini-gateway tier-R tests use one); you MAY run a throwaway container
(`docker run --rm -d --name c05canvas-pg -e POSTGRES_PASSWORD=x -p 127.0.0.1:55432:5432 postgres:17`
— only if the image is already present: check `docker images`; remove the
container when done) to confirm behaviours with psql inside the container
(`docker exec -i c05canvas-pg psql -U postgres`). Experiments are optional;
source citations are mandatory.
This cluster asks: what does the SERVER offer, so that a proxy need not read SQL?
Focus:
- `DISCARD ALL`: exactly what it resets (src/backend/commands/discard.c
  `DiscardAll`) and what it does NOT reset — walk the list of session state:
  GUCs (RESET ALL — which GUC contexts/sources survive? `SET SESSION
  AUTHORIZATION`, `SET ROLE`), prepared statements, cursors/portals, temp tables
  (and the temp NAMESPACE itself), LISTEN, advisory locks, sequences'
  currval/lastval caches, plan cache, loaded libraries (`LOAD`), per-backend
  caches, session-level `SECURITY LABEL`?, `pg_stat` state, large-object
  descriptors, replication origin session (`pg_replication_origin_session_setup`),
  logical decoding slots held, two-phase state, custom extension state in C
  globals/PL session variables (PL/Perl %_SHARED, PL/Python GD, plpgsql custom
  GUC placeholders), `SET LOCAL` leftovers, session_preload side effects,
  DISCARD ALL not allowed inside a transaction block
  (`PreventInTransactionBlock`) — and the consequence for a pipeline
  `ROLLBACK; DISCARD ALL`. State the residue precisely: what can a tenant leave
  on a connection that DISCARD ALL will not remove? Also what DISCARD ALL costs
  (it drops the plan cache and temp namespace work) — any published or measured
  numbers; measure a simple round trip if you run a container.
- Protocol signals about session state available to a proxy without parsing:
  ReadyForQuery status byte; `ParameterStatus` for GUC_REPORT variables (list
  them from src/backend/utils/misc/guc_tables.c; note PostgreSQL 18's ability
  for... check whether any release lets a client ask for extra reported GUCs —
  the `_pq_.report_parameters` proposal: landed or not? cite the commit or the
  mailing-list state); CommandComplete tags (e.g. `SET`, `PREPARE`, `LISTEN`,
  `DECLARE CURSOR`, `CREATE TABLE`, `DISCARD ALL` — the server tells the proxy
  WHAT KIND of command just ran, per statement, even inside a multi-statement
  Query: enumerate the tags relevant to session state from
  src/include/tcop/cmdtaglist.h and say what a "taint by CommandComplete tag"
  mechanism would catch (every top-level utility statement, regardless of
  spelling or lexical tricks) and miss (anything inside a function or a SELECT:
  set_config, pg_advisory_lock, temp table created in a function, `SELECT INTO
  TEMP`? — check its tag); NotificationResponse; NoticeResponse.
  This "read the server's command tag instead of the client's text" idea may be
  new to the assessment — examine it carefully and honestly, including the
  timing problem (the tag arrives after the statement ran: fine for deciding to
  reset on release, useless for rejecting up front).
- Server-side enforcement of read-only: `BEGIN READ ONLY` /
  `default_transaction_read_only` / `SET TRANSACTION READ ONLY`. What exactly is
  refused (src/backend/tcop/utility.c `ClassifyUtilityCommandAsReadOnly`,
  `PreventCommandIfReadOnly`, `COMMAND_OK_IN_READ_ONLY_TXN`; executor
  `ExecCheckXactReadOnly`; nextval/setval) and what is still ALLOWED in a
  read-only transaction that matters to us: writes to temp tables, `SET`,
  `set_config`, `PREPARE`, `LISTEN`/`NOTIFY`?, advisory locks, `nextval` on temp
  sequences, functions with side effects outside the database (dblink, COPY TO
  PROGRAM, pg_read_file), `pg_terminate_backend`, large objects... Can the
  transaction turn itself read-write again (`SET TRANSACTION READ WRITE` before
  the first query? after?) — pin down the exact rule, because it decides whether
  option (c2) is sound when the client controls the text inside the wrapper
  (e.g. `BEGIN READ ONLY; <client text containing "; COMMIT; DELETE ...">`).
- Restricting what a session CAN do, so the proxy need not detect it: role-level
  means — `ALTER ROLE ... SET`, revoking EXECUTE on set_config/advisory lock
  functions (possible? they are pg_catalog functions callable by PUBLIC — can
  EXECUTE be revoked from PUBLIC and does that survive upgrades?), revoking
  TEMP on the database (`REVOKE TEMPORARY ON DATABASE`), `SET ROLE`-per-tenant
  designs, GUCs that cannot be changed by non-superusers (`PGC_SUSET`), event
  triggers, `session_authorization`, hooks (ProcessUtility_hook in an extension
  that refuses session-state commands for a role — vanilla Postgres plus an
  extension; is there an existing one, e.g. `set_user`, `pg_tle` hooks,
  supautils `reserved_roles`/privileged settings?). Which session features have
  NO privilege that can be revoked (plain `SET` of user-settable GUCs, `PREPARE`,
  `LISTEN`, `DECLARE ... WITH HOLD`)?
- Asking the server whether a session is dirty: a single cheap query over
  pg_prepared_statements, pg_cursors, pg_listening_channels(), pg_locks
  (advisory, this pid), pg_settings (source = 'session'), temp namespace
  (`pg_my_temp_schema()` <> 0 and any pg_class rows in it), sequences touched.
  Write the query, say what it covers and misses, estimate/measure its cost
  versus `DISCARD ALL` itself. Is "just always DISCARD ALL" cheaper than
  asking?
- Function volatility in the catalog (`pg_proc.provolatile`, `proparallel`) as
  the server's own statement of "safe to share": how reliable are the labels
  (user functions default to VOLATILE; a function labelled IMMUTABLE may lie),
  and what in-core machinery already answers "does this query contain volatile
  functions" (`contain_volatile_functions`, `contain_mutable_functions`,
  `max_parallel_hazard`, `CommandIsReadOnly`, `pg_stat_statements` queryid /
  the planner's `PlannedStmt.hasModifyingCTE`, `rowMarks`) — none of it exposed
  to a client? Could `EXPLAIN (VERBOSE, FORMAT JSON)` or PREPARE + catalog
  inspection expose enough (cost: an extra round trip, cacheable by text)?
- History: in-core connection pooler proposals (Konstantin Knizhnik's built-in
  connection pooler patch, 2018–2020; "dedicated backends" when session state is
  detected) and protocol proposals for pooler-friendly session reset or state
  tracking — what did the hackers conclude about detecting session state? Cite
  mailing-list threads or the patch README if you can find them (web).

## Hard rules
- Research only. Do NOT write, commit, or change anything under
  /root/Software/mini-gateway or /root/Software/mini-gateway-work (reading the two
  files above is fine). Do not touch any git branch there. Commit nothing anywhere.
- Work only under /root/scratch/c05-canvas/. Clone into
  /root/scratch/c05-canvas/repos/<project> with `git clone --depth 1` (reuse a
  clone that already exists). Scratch programs go in
  /root/scratch/c05-canvas/work/12-postgres-server/.
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
One markdown file: /root/scratch/c05-canvas/findings/12-postgres-server.md, at most about
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
