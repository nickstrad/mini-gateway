# Chunk 5 (statement classifier) — canvas of how other projects handle the problem

Written by the Fable orchestrator on 2026-11-25, at Nick's request
(`/root/briefs/zgateway-c05-canvas-mission.md`, relayed by Twin; copy in
[canvas/briefs/00-mission.md](canvas/briefs/00-mission.md)). It sits beside
[SCOPE-ASSESSMENT.md](SCOPE-ASSESSMENT.md) and [EXPLAINER.md](EXPLAINER.md) and checks their
working conclusion against other projects' source code.

Research only. Chunk 5 is still parked on branch `work/c05-classifier` (tip `d39ecfb`);
its code, its branch and `PLAN.md` were not touched. This run added this file, the
`canvas/` folder beside it, rows in `agent_work/INDEX.md` and one pointer line in `STATE.md`.

**The question.** A Postgres proxy that lends connections per transaction must decide, from
SQL text alone, whether a statement changes session state, whether it is provably read-only,
and whether it is safe to share. Three review rounds found 11 holes in our classifier. Which
projects claim to solve this, and what do they actually do?

**The answer in five lines.**

1. No project decides session safety from SQL text and claims the decision is complete. The
   ones that read SQL for it say "best effort" in their own code or documentation.
2. Four proxies carry PostgreSQL's real parser (Pgpool-II, Odyssey, PgDog, Supavisor; the
   last two through the libpg_query library) and a fifth, ShardingSphere, a full grammar of
   its own. Reading their code turned up list holes of the same kind as ours in every one.
   The parser closes the lexical layer and nothing else.
3. Where a project is safe, the safety comes from a mechanism: reset on release, a pool that
   is not shared across identities, a standby that refuses writes, or a server that was
   modified to report its own state.
4. The canvas found mechanisms the assessment did not consider. The most useful: the server
   already tells the proxy what kind of statement ran (the `CommandComplete` tag), and
   `DISCARD ALL` on a clean session costs less than a `SELECT 1`.
5. The recommendation stands: option (b), staged with (d1). The canvas changes four details
   inside (b) and adds one question for Nick (section 6).

## 1. How the canvas was run, and how to read the evidence

Twelve fresh Opus agents (`claude-opus-5-5`), one per cluster, ran headless and five at a
time through `agent_work/orch/launch.sh` under a small conductor
([canvas/conductor.sh](canvas/conductor.sh); log in
[canvas/conductor-run.log](canvas/conductor-run.log)). All twelve exited 0 on the first try,
in 4 to 13 minutes each. Each wrote one findings file; the twelve files are in
[canvas/findings/](canvas/findings/) and hold the citations, code excerpts and probe tables
this document summarises. The briefs are in [canvas/briefs/](canvas/briefs/).

The mission listed ten clusters. I added two: managed and caching proxies (RDS Proxy,
Hyperdrive, ReadySet), because result caching is the nearest public precedent for our
sharing decision; and the PostgreSQL server itself, because option (b) rests on what
`DISCARD ALL` and read-only transactions really do.

Evidence labels used below:

- **[code]** — an agent read the cited lines in a local clone at the commit in the table.
- **[run]** — an agent executed it (PostgreSQL 17.11 in a throwaway container; Go benchmarks).
- **[docs]** — the project's documentation only. Everything about the two closed products
  (RDS Proxy, Hyperdrive) is of this kind.
- **[inferred]** — a consequence an agent worked out from code it read, without running it.
  Every hole reported in another project's code is of this kind unless marked [run]. None
  was reported upstream or reproduced.
- **[checked]** — I re-read the cited lines myself after the agents finished (about twenty
  citations, listed in section 7).

### Sources

| Project | Read from | Commit or version | Local source or web |
| --- | --- | --- | --- |
| PgBouncer | github.com/pgbouncer/pgbouncer | `23347f6`, 2026-11-25 (release 1.26.1) | local clone |
| PgCat | github.com/postgresml/pgcat | `5b03881`, 2025-02-27 (last commit; unmaintained) | local clone; sqlparser-rs 0.52 crate |
| Odyssey | github.com/yandex/odyssey | `21d01bd`, 2026-11-24 (release 1.5.3) | local clone |
| pgagroal | github.com/agroal/pgagroal | `0f53af7`, 2026-11-05 (release 2.1.0) | local clone |
| Pgpool-II | github.com/pgpool/pgpool2 (mirror) | `d30ad43`, 2026-11-24 (tag V4_7_3) | local clone; grammar history from the GitHub API |
| PgDog | github.com/pgdogdev/pgdog, pg_raw_parse, docs | `1d1d861`, 2026-11-25 (v0.1.66) | local clones |
| Supavisor | github.com/supabase/supavisor | `22efe25`, 2026-11-25 (v2.10.1); tag v2.0.0 for removed features | local clone; v2.0.0 files from the GitHub API |
| ShardingSphere | github.com/apache/shardingsphere | `03aa12d`, 2026-11-24 (release 5.5.3) | sparse local clone (24 MB) and GitHub API |
| libpg_query, pg_query_go, wasilibs/go-pgquery | github.com/pganalyze/…, github.com/wasilibs/go-pgquery | `86b593b` (PG 18.6); `7ac09d4` (v6.2.5, PG 17.7); `3939604` | local clones, built and benchmarked |
| CockroachDB | github.com/cockroachdb/cockroach | `d30c905`, 2026-09-23 | sparse local clone |
| YugabyteDB | github.com/yugabyte/yugabyte-db | `42689cf`, 2026-11-25 | sparse local clone |
| ProxySQL | github.com/sysown/proxysql | `e1a7dc6`, 2026-11-24 (v3.0.12) | sparse local clone and its documentation site |
| Vitess | github.com/vitessio/vitess | `84d5f2d`, 2026-11-24 (v24.0.5) | sparse local clone |
| Citus | github.com/citusdata/citus | `5290ba9`, 2026-11-25 (v14.3.0) | sparse local clone |
| Postgres-XL | git.postgresql.org (dormant since 2018) | GitHub mirror `b715468`, 2017-08-22 | web reads only |
| ReadySet | github.com/readysettech/readyset | `f3270de`, 2026-11-25 | sparse local clone |
| RDS Proxy | docs.aws.amazon.com (closed source) | pages fetched 2026-11-25 | web reads only |
| Cloudflare Hyperdrive | developers.cloudflare.com, blog (closed source) | pages fetched 2026-11-25 | web reads only |
| PostgreSQL | github.com/postgres/postgres | master `4483697` (20devel), 2026-11-25; experiments on 17.11 | local clone and a throwaway container |

Full URLs, release dates and licences are in each findings file's "Sources" table. The
clones were shallow and lived in scratch space (566 MB in all); they are not kept.

## 2. Comparison table

"Session state" means the decision our plan calls D3. "Miss" means what happens in that
system when its reading or its list is wrong.

| Project | Reads client SQL with | What keeps session state from crossing clients | Read/write or sharing decisions | What a miss costs there |
| --- | --- | --- | --- | --- |
| **PgBouncer** | Nothing | Tracks the settings the server reports and sets them again per client. `DISCARD ALL` on release only in session mode, or always with one switch. Otherwise documents the breakage. Pools per database and user | None | State stays on the connection for the next client of the same pool |
| **PgCat** | sqlparser-rs (not PostgreSQL's grammar), off by default, for routing only | Marks a connection dirty from the server's `CommandComplete` tag (`SET`, `PREPARE`), then `RESET ROLE; RESET ALL; DEALLOCATE ALL` at check-in. Five reported settings tracked and replayed | Read/write split: deny-list over statement kinds | Session: temp tables, `LISTEN`, advisory locks, `set_config` leak within a pool. Routing: the replica refuses the write |
| **Odyssey** | Since 2026-09, a vendored copy of PostgreSQL's `gram.y` and `scan.l`, used only for read-only routing | **Reset after every transaction by default**, with no reading: `DISCARD ALL`, or the same list without `DEALLOCATE ALL`. Closes the connection if the reset fails. Tracks and replays reported settings | Routing to a standby: allow-list of 150 function names over the parse tree | Session: nothing, except SQL `PREPARE` under the reduced reset. Routing: the standby refuses the write |
| **pgagroal** | Nothing | Transaction mode: no reset by default, one switch for `DISCARD ALL` always. Documents the breakage | None | State stays on the connection |
| **Pgpool-II** | A vendored copy of PostgreSQL's grammar, re-imported once a year since 2014; plus catalog lookups (`pg_proc.provolatile`, `pg_class`) through a cache | Does not lend per transaction: one client owns its connections for the whole session, then `ABORT; DISCARD ALL` | Routing: statement-type allow-list plus function volatility from the catalog. Query cache: deny-list of about a dozen features | Routing: the standby refuses the write. **Cache: wrong or stale rows to another session** |
| **PgDog** | The real PostgreSQL 18.4 parser (libpg_query). With one database and no replicas, the default mode parses only statements whose first keyword matches a regex | Tracks `SET` per client and replays it; pins for advisory locks and temp tables; resets only connections it knows are dirty | Read/write split: deny-list of three node kinds and two function names | Session: whatever tracking missed stays on a connection that is never reset. Routing: the replica refuses the write |
| **Supavisor** | The real PostgreSQL parser (libpg_query), only when a per-tenant check is switched on; off by default | Pools are per tenant and user, so state cannot cross tenants. No reset in transaction mode. Optional deny-list detector that can log or refuse | None today. Version 2.0.0 routed a lone `SELECT` to a replica and re-ran it on the primary when the replica answered `25006` | Session: state crosses clients of the same tenant and user. The detector skips anything over 1 KB |
| **ShardingSphere-Proxy** | Its own ANTLR grammar for PostgreSQL: 8,042 grammar lines and a 5,343-line visitor | Intercepts `SET` and, for PostgreSQL, throws the value away while telling the client it worked. Everything else goes to a pool shared by all clients, with no reset | Read/write split: a `SELECT` with no lock clause and no writing CTE is a read; functions are not looked at | Session: `set_config`, temp tables, `PREPARE`, advisory locks reach the next client. Routing: the replica refuses the write |
| **ProxySQL** | A hand tokenizer; prefix and substring tests on the normalised text; a regular expression for `SET` | Marks a connection after the statement ran and pins it to its client; `DISCARD ALL` when that client leaves. Nineteen settings tracked and replayed. Unknown `SET` pins | Routing by regular expressions the user writes; opt-in cache with a time limit | Session: anything the substring tests miss travels to the next client; clean-looking connections are never reset |
| **Vitess** (MySQL) | Its own full MySQL grammar | Emulates state in the proxy; allow-list of system variables (unknown is an error); connections tagged with their settings; pins for temp tables and locks; discards a connection after `CALL` | **Coalesces identical in-flight reads by default**, keyed by text, for any `SELECT` outside a transaction, with no check for volatile functions | A stored function that changes state is not seen. Concurrent callers share one `NOW()` |
| **RDS Proxy** (closed) | Not stated | Pins the client to its connection; "can't be certain" also pins. No reset is documented | None | Documented: changes made inside stored functions are not detected |
| **Hyperdrive** (closed) | "Parses the query"; parser not stated | "The connection is `RESET`" on return; the rest is listed as unsupported, and some of it is not refused | Result cache for non-mutating queries without `STABLE` or `VOLATILE` functions; entries expire, no invalidation | Stale rows within the time limit; a session advisory lock can stay held |
| **ReadySet** | A fork of sqlparser-rs plus its own parser | **One upstream connection per client**: nothing is shared, so the problem does not arise | Caches only what its own engine can execute, or what passes a default-deny check whose function list is generated from `pg_proc.provolatile` | An unrecognised statement goes to the client's own connection: slower, never wrong |
| **CockroachDB proxy** | Nothing | Does not share connections. To move a session it asks the server (`SHOW TRANSFER STATE`); the server serialises the session or refuses | None; the server enforces read-only on the plan | A refused move is skipped |
| **YugabyteDB connection manager** | Nothing (an Odyssey fork) | **The patched server reports its own state**: status byte `i` instead of `I` when the session holds something unpoolable, and a private message for every setting change. The pooler pins or replays | None | State the server does not flag (sequence caches, protocol-level prepared statements) leaks; documented |
| **Citus, Postgres-XL** | PostgreSQL's parser, analyser and catalog: they run inside the server | Citus: worker connections belong to one client's backend. Postgres-XL: tracks every setting where it is set, replays it, resets on release | From the analysed query tree, with volatility from the catalog | An unsupported query is refused before it runs |
| **libpg_query family** | The real scanner and grammar as a library | None; a library | None; exposes tokens, statement boundaries, the raw tree, function names | Assumes `standard_conforming_strings = on`; the server may not |

Four patterns stand out.

- **Nobody both shares connections across identities and relies on reading.** PgBouncer,
  PgCat, PgDog, Odyssey and pgagroal key pools by database and user; Supavisor by tenant and
  user; ReadySet and Pgpool-II do not lend per transaction at all. Mini-gateway shares one
  pool across tenants over one database role (decision D6), so it is more exposed than any
  of them to a missed session change. ShardingSphere-Proxy is the one project found in the same
  position, and it has no protection: a `set_config` call reaches the next client [inferred].
- **Every read/write splitter has a server behind it that refuses writes.** A wrong "this is
  a read" lands on a hot standby and comes back as an error. That is option (c2)'s principle,
  delivered by topology. It is why those projects can live with short lists.
- **The one place a list stands alone, it has holes.** Pgpool-II's query cache has no server
  backstop, and that is where the agent found unclosed cases (section 3).
- **Projects that reset only known-dirty connections leak; projects that reset every time do
  not.** PgCat, PgDog and ProxySQL reset when a detector fires. Odyssey resets always.

## 3. Per-project notes

Each note gives the mechanism and the decisive citations. The findings file has the excerpts,
the probe table (how the project treats `SET`, `set_config`, a function that sets, lexical
tricks, `PREPARE`/`LISTEN`/temp tables/advisory locks, multi-statement messages, and
writing reads) and the list of what could not be verified.

### PgBouncer — the baseline ([findings](canvas/findings/01-pgbouncer.md))

- **Reads no SQL.** `handle_client_work` switches on the message type byte and forwards the
  packet (`src/client.c:1456-1472`, `:1718`) [code].
- **Settings: track and replay from a protocol signal.** It keeps the ten settings PostgreSQL
  reports and a client can change (`src/varcache.c:98-110`) [checked]. Every `ParameterStatus`
  updates the server's and the client's copy (`src/server.c:30-53`); when a connection is
  lent, the differences go out as one `SET …;` message before the client's statement
  (`src/varcache.c:253-281`, `src/objects.c:961-967`) [code]. The server sends the report
  however the change was made (`SET`, `set_config`, inside a function). `search_path` is
  reported only by PostgreSQL 18 and later; PgBouncer began tracking it in 1.26.0
  (2026-09-23), and its own release notes say bugs in that tracking are still open
  (`NEWS.md`, 1.26.1) [checked].
- **Reset.** `server_reset_query` defaults to `DISCARD ALL` but runs only in session mode
  unless `server_reset_query_always` is on (`src/objects.c:1231-1234`) [checked]. The
  documentation calls the switch a way to turn "non-deterministic breakage" into
  "deterministic breakage" (`doc/config.md:695-703`).
- **Everything else is documented as broken.** The feature table says "Never" for `SET`,
  `LISTEN`, holdable cursors, `PREPARE`, session temp tables, `LOAD` and session advisory
  locks in transaction pooling (pgbouncer.org/features.html) [docs].
- **One tag check.** It forgets its prepared-statement bookkeeping when the server's
  `CommandComplete` tag is `DEALLOCATE ALL` or `DISCARD ALL` — it reads the server's reply,
  not the client's text (`src/server.c:542-551`) [checked].
- The explainer's three "from memory" statements about PgBouncer are now verified.

### PgCat ([findings](canvas/findings/02-pgcat.md))

- **Unmaintained.** No commit since 2025-02-27; the author moved to PgDog (issue #942) [docs].
- **Parser for routing only**, sqlparser-rs 0.52, off by default (`src/config.rs:544-545`).
  On a parse error it logs and forwards the statement to whichever server role the previous
  statement chose (`src/client.rs:970-979`) [inferred]. Its own issue #620 says the parser
  "is not going to be 100% PG compatible".
- **Session state: server tags, not text.** `Server::recv` marks the connection when a
  `CommandComplete` tag is `SET` (only outside a transaction) or `PREPARE`
  (`src/server.rs:1012-1030`) [checked]. The comment above it reads "Non-exhaustive list …
  a best effort". At check-in a marked connection gets `RESET ROLE; RESET ALL;` and
  `DEALLOCATE ALL;` (`src/server.rs:1329-1357`) [code]. Never `DISCARD ALL`: temp tables,
  `LISTEN`, advisory locks and holdable cursors stay.

### Odyssey and pgagroal ([findings](canvas/findings/03-odyssey-pgagroal.md))

- **Odyssey resets after every transaction, by default, without reading anything.** The
  release path calls `od_reset` (`sources/frontend.c:2264-2292`), which sends `DISCARD ALL`
  (`sources/reset.c:191-199`), or, when Odyssey manages prepared statements itself, the
  string `SET SESSION AUTHORIZATION DEFAULT;RESET ALL;CLOSE ALL;UNLISTEN *;SELECT
  pg_advisory_unlock_all();DISCARD PLANS;DISCARD SEQUENCES;DISCARD TEMP;`
  (`sources/reset.c:203-205`) [checked]. Defaults: `discard = 1` (`sources/pool.c:23`)
  [checked]. A failed reset, an unfinished reply or a connection left in `COPY` closes the
  connection (`sources/reset.c:30-170`, `:255-259`) [code]. Many shipped example
  configurations turn the reset off.
- **The carve-out leaks.** The reduced string keeps prepared statements, so a client's SQL
  `PREPARE` survives for the next client of the same pool (`sources/virtual.c:661-665`)
  [inferred].
- **It now vendors PostgreSQL's grammar** (imported 2026-09-30; 22 lines differ from
  PostgreSQL master in the rules section), used only to route reads to a standby. The
  classification on top is a hand-made allow-list, and the agent found cases it gets wrong:
  two grammar helper stubs drop a subtree, so `SELECT json_object('k' : nextval('s'))` loses
  the call; `COPY (SELECT f()) TO STDOUT` is not checked for functions; `txid_current` is on
  the safe list (`sources/sql/full/makefuncs.c:169-173`, `sources/query.c:207-222`, `:379`)
  [inferred]. Each costs an error on the standby, nothing worse.
- **Pin on a server signal.** Optionally pins a client when the server's tag is `LISTEN`
  (`sources/stream.c:154-167`), and pins when a server-side extension reports
  `ttt.owns_session_objs` through `ParameterStatus` (`sources/backend.c:1191-1200`) [code].
  The extension is not public.
- **pgagroal** reads nothing, and in transaction mode resets nothing unless
  `server_reset_query_always` is set (`src/libpgagroal/pipeline_transaction.c:435-475`)
  [code]. Its documentation lists what breaks (`doc/PIPELINES.md:63-69`).

### Pgpool-II — the closest thing to option (a) in production ([findings](canvas/findings/04-pgpool2.md))

- **A copy of PostgreSQL's own grammar**: `src/parser/gram.y`, 20,155 lines, merged by hand
  from each PostgreSQL release — thirteen imports from 9.4 (2014) to 19 (2026-11-06)
  (`src/parser/README`; commit history) [code]. A parse error is treated as a write and
  sent to the primary (`src/protocol/pool_proto_modules.c:346-381`) [code].
- **It asks the catalog.** For each function call in a `SELECT` it runs `SELECT count(*) FROM
  pg_proc … WHERE proname = '%s' … AND provolatile = 'v'` on the client's connection and
  caches the answer (`src/utils/pool_select_walker.c:1212-1310`) [code]. This is the "catalog
  layer" the explainer said cannot be read from text; Pgpool reads it from the server. The
  lookup is by name only (every schema, every overload), the cache is never invalidated by
  default (`relcache_expire = 0`), and it sees only calls written as calls.
- **Session state is not its problem.** It never lends a connection per transaction. Its own
  safety for sessions is `ABORT; DISCARD ALL` when the client leaves
  (`src/config/pool_config_variables.c:51`) and dropping the connection if that fails
  (`src/protocol/child.c:571-584`) [code].
- **Routing is safe because the standby refuses writes**, not because the reading is complete.
- **The query cache has no such backstop, and it has holes** [inferred, none executed]:
  - a `MERGE` inside a CTE is judged cacheable and does not invalidate its table — the check
    lists `InsertStmt`, `DeleteStmt`, `UpdateStmt` only
    (`src/query_cache/pool_memqcache.c:1113-1130`) [checked that the list has three entries];
  - `'now'::timestamp` and range or array types over `now` are cached — only casts to
    `timestamptz` and `timetz` are refused (our holes 8 and 11);
  - attribute-notation calls (`t.f` for `f(t)`) are invisible to both walkers (our hole 5);
  - the cache key is user, text and database, so a changed `search_path` or role reuses
    another setting's result;
  - an immutable function name shadowed by a volatile one counts as immutable.
- **Verdict against our eleven holes:** the real scanner closes the four lexical ones;
  session pinning plus `DISCARD ALL` makes the four session ones harmless; the four that
  concern sharing stay open in its cache. Two decades of maintenance and the real grammar
  did not close the list layer.

### PgDog ([findings](canvas/findings/05-pgdog.md))

- **The real parser**, PostgreSQL 18.4, through its own binding `pg_raw_parse` (it dropped
  pg_query.rs to avoid the protobuf round trip; `Cargo.toml:25`) [code]. A parse error is
  returned to the client and the statement is not forwarded
  (`frontend/client/query_engine/mod.rs:177-185`) [code].
- **In our deployment shape it mostly does not parse.** With one database and no replicas,
  the default mode parses a statement only if a regular expression on its leading keyword
  matches (`SET`, `RESET`, `BEGIN`, `LISTEN`, …); anything starting with `SELECT`, `INSERT`,
  `UPDATE`, `DELETE` or `WITH` is forwarded unread (`backend/pool/cluster.rs:637-655`,
  `frontend/regex_parser.rs:10-50`) [code].
- **Track and replay.** Parsed `SET` is stored per client and replayed by difference at
  checkout (`backend/server.rs:752-805`) [code].
- **It trusts clean connections.** The cleanup — `SET SESSION AUTHORIZATION DEFAULT`,
  `RESET ALL`, `pg_advisory_unlock_all()`, `DISCARD TEMP` — runs only on a connection marked
  dirty; `DISCARD ALL` only in session mode (`backend/pool/cleanup.rs:14-23`, `:65-74`)
  [checked]. The hand-picked list omits `DEALLOCATE ALL`, `UNLISTEN *` and `CLOSE ALL`.
- **Holes found by reading** [inferred]: an advisory lock with a computed key is not pinned;
  `CREATE TEMP TABLE … AS`, `SELECT … INTO TEMP` and `CREATE TABLE pg_temp.t` do not pin;
  `set_config` is tracked only as the sole item of a `SELECT` with constant arguments; the
  write check is `["nextval", "setval"]` (`frontend/router/parser/function.rs:3`) [checked].
- **Its claim.** The README says PgDog "can parse and handle `SET` statements … ensuring
  session state is set correctly", and its comparison page gives itself a full mark where
  PgBouncer gets "Partial". That holds for a visible `SET`. It does not hold for the cases
  above. Licence: AGPL-3.0.

### Supavisor ([findings](canvas/findings/06-supavisor.md))

- **Isolation by pool key.** A pool is identified by tenant, user, mode, database and startup
  `search_path` (`lib/supavisor.ex:25-33`) [code]. Tenants never share a connection.
- **No reset in transaction mode**: the cleanup refuses to run there
  (`lib/supavisor/db_handler.ex:683-708`) [code].
- **An optional detector**, off by default, walks the real parse tree for a deny-list:
  `SET`, `DISCARD`, `LISTEN`, `LOAD`, holdable cursors, temp tables, `set_config` with a
  literal `false`, six advisory-lock names (`native/pgparser/src/lib.rs:176-249`) [code]. It
  fails open by design: anything over 1,024 bytes, unparseable, or not UTF-8 is forwarded
  unchecked (`lib/supavisor/protocol/message_handler_helpers.ex:14-38`) [checked], and its
  documentation says so. Its stated purpose is to find misbehaving clients.

### ShardingSphere-Proxy ([findings](canvas/findings/07-shardingsphere.md))

- **A full hand-kept grammar**: 8,042 lines of ANTLR and a 5,343-line visitor, with rule
  names that mirror `gram.y` but no recorded PostgreSQL version. Cost of keeping it: at
  least 32 commits to the grammar in two years, 69 issues labelled as PostgreSQL parse
  problems, and a list of 34 statements its own tests record as unparseable [code, GitHub
  API]. A statement it cannot parse is an error to the client. A string-escape bug in its
  lexer of the same class as our review-2 blocker was fixed in 2026 (issue #36416).
- **The grammar does not protect session state.** `SET` is intercepted and never sent; for
  PostgreSQL the value is logged and dropped, and the client is still told `SET` succeeded
  (`SessionVariableRecordExecutor.java:47-54`; the only replay provider is MySQL's) [code].
  Everything else is forwarded to a HikariCP pool shared by all sessions and returned after
  each command with no reset (`ProxyDatabaseConnectionManager`, `CommandExecutorTask.java:158-162`)
  [code]. So `SELECT set_config(…)`, a temp table or an advisory lock reaches the next
  client [inferred].
- This is the direct answer to "does a full grammar buy completeness": no, and here it does
  not even attempt the session decision.

### libpg_query, pg_query_go, go-pgquery — option (c1)'s engine ([findings](canvas/findings/08-libpg-query.md))

- **What it is.** PostgreSQL's scanner and grammar copied out of the server (PG 18.6 in
  libpg_query, 17.7 in the two Go bindings), with 15 patches that only widen the accepted
  language (`pg_query.h:165-201`) [code]. It returns tokens with offsets, statement
  boundaries, the raw tree, a fingerprint, a list of function names.
- **What it is not.** No name resolution and no catalog: it cannot tell a view from a table,
  what a function does, or that `'now'::timestamptz` is volatile [run].
- **Measured cost on this VM** (loaded, so treat as upper bounds; ratios are reliable) [run]:

  | Operation, short hot-key read | cgo (pg_query_go) | WASM (go-pgquery) |
  | --- | --- | --- |
  | Split into statements | 3.4 µs | not offered |
  | Scan to tokens | 5.6–12 µs | 20–31 µs |
  | Parse to a tree | 10–47 µs | 64–219 µs |
  | First call | — | 3.2 s and about 280 MB |
  | Build | 64 s cold; needs a C compiler per target | pure Go, 24 s |
  | Binary size added | 9.4 MB | 11.8 MB |

- **A residual lexical risk even with the real scanner.** The library always lexes as if
  `standard_conforming_strings` were on (`src/pg_query_parse.c:48-60`), and the Go bindings
  cannot change that [code]. With the setting off on the server, the text
  `SELECT 'a\' ; --' ; SET search_path = x` is one statement to the library and two to
  PostgreSQL 16 [run]. The server reports this setting in `ParameterStatus`, so a proxy can
  refuse to trust its reading when the value is not `on`.
- **The cheap splitter can fail open.** `SplitWithScanner("SELECT 1); SET search_path = x")`
  returns no statements and no error (`src/pg_query_split.c:19`, `:43`) [run] [checked].
- **A pure-Go port appeared this year**: sqlc-dev/oliphant, checked byte for byte against
  libpg_query on 308,561 cases by its own account, created 2026-08. Too young to rely
  on, but it removes the cgo objection to (c1) if it matures.

### CockroachDB and YugabyteDB ([findings](canvas/findings/09-distributed-dbs.md))

- **What disappears.** The component that decides is the server, with the parse tree, the
  plan and the catalog. CockroachDB enforces read-only on the optimiser's plan
  (`pkg/sql/opt/exec/execbuilder/relational.go:146-158`); YugabyteDB runs PostgreSQL 15's
  own parser.
- **What appears.** You must run that database. CockroachDB's grammar is a hand-maintained
  fork of a 2015 `gram.y` with a hand-written scanner; it has no `LISTEN`, reads `b'…'` with
  different string rules, and cannot turn `standard_conforming_strings` off
  (`pkg/sql/scanner/scan.go:204-216`, `pkg/sql/vars.go:1894`) [code]. As a parser in front
  of real PostgreSQL it would bring back the review-2 class of bug. Its standalone parser
  module is archived.
- **Why it matters to a proxy anyway.** Both have a proxy in front, both proxies read no SQL,
  and both get a signal from the server:
  - CockroachDB's proxy sends `SHOW TRANSFER STATE`; the server returns the serialised
    session or an error naming the obstacle (open transaction, active portals, temp schema)
    (`pkg/sqlproxy/conn_migration.go:259-338`, `pkg/sql/session_state.go:55-110`) [code].
  - YugabyteDB's server answers `ReadyForQuery` with `i` instead of `I` when the session
    holds a temp table, a SQL prepared statement, a `LISTEN`, a session advisory lock or a
    holdable cursor — flags set in the code that creates each of them, not from text
    (`src/postgres/src/backend/access/transam/xact.c:5495-5505`,
    `utils/misc/pg_yb_utils.c:9133-9153`) [checked the status byte]. It also pushes every
    setting change to the pooler in a private message.
- **The lesson.** Exact detection exists, and it took a modified server. Even then
  YugabyteDB documents leaks for state its server does not flag.

### ProxySQL, Vitess, Citus, Postgres-XL ([findings](canvas/findings/10-mysql-world-and-routers.md))

- **ProxySQL is the long-running precedent for "taint by substring".** On both its MySQL and
  PostgreSQL sides, session state is detected by `strncasecmp` and `strcasestr` on the
  normalised statement after it ran: `CREATE TEMP TABLE ` as a prefix,
  `SELECT pg_advisory_lock` as a prefix, `CURSOR WITH HOLD` as a substring
  (`lib/PgSQL_Connection.cpp:5814-5992`) [code]. A hit pins the connection to its client
  until the client leaves, then `DISCARD ALL`. A connection with no hit goes back unreset
  (`lib/PgSQL_Data_Stream.cpp:1665`). So `SELECT 1, pg_advisory_lock(1)`, `SELECT 1; SET …`
  and `set_config` (by default) get through [inferred]; its own document on `set_config`
  says so. The PostgreSQL side is growing case by case, each with an issue number in a
  comment — the path our classifier was on. One fix there is instructive: `LISTEN` is now
  detected from the server's `CommandComplete` tag because "the tag is there whatever the
  query looked like" (`lib/PgSQL_Connection.cpp:3757-3765`).
- **Vitess** has a full grammar and still added mechanisms for what parsing cannot see:
  an allow-list of system variables where an unknown name is an error
  (`go/vt/vtgate/planbuilder/system_variables.go:105-108`); a pool whose connections are
  tagged with the settings applied to them, reset and re-applied on mismatch
  (`go/pools/smartconnpool/pool.go:781-853`) [checked]; and discarding a connection after
  `CALL`, because "a procedure body can leave session state behind that the statement's plan
  cannot see" (`go/vt/vttablet/tabletserver/query_executor.go:1686-1715`) [code].
- **Vitess also coalesces in-flight reads** — the only project in the canvas that does what
  our chunk 11 does. It is on by default (`tabletenv/config.go:204`) [checked], keyed by the
  SQL text, for `SELECT` and `SHOW` plans outside a transaction, with no check for volatile
  functions (`query_executor.go:1340-1390`) [code].
- **Citus** runs inside PostgreSQL and takes volatility from the catalog on the resolved
  function (`utils/citus_clauses.c:427-459`). **Postgres-XL** (dormant since 2018) hooked the
  place where settings are set, replayed them on each pooled connection and reset on release.

### RDS Proxy, Hyperdrive, ReadySet ([findings](canvas/findings/11-managed-and-caching-proxies.md))

- **RDS Proxy** [docs]: pins on a documented list (`SET`, `PREPARE`, temp objects, any
  cursor, `LISTEN`, `LOAD`, `nextval`, advisory locks, `set_config`, any statement over
  16 KB) and "if RDS Proxy can't be certain". It states its blind spot: "Calling stored
  procedures and stored functions doesn't cause pinning. RDS Proxy doesn't detect any
  session state changes resulting from such calls." How it reads text is not published, and
  no reset on release is documented. The explainer's "from memory: errs towards pinning" is
  confirmed.
- **Hyperdrive** [docs]: transaction pooling where "the connection is `RESET`" on return;
  of session advisory locks it says it "does not reject these commands, but a session-level
  lock can remain held by a pooled connection". Its cache rule is the
  nearest public statement of our sharing decision: "Only functions designated as
  `IMMUTABLE` by PostgreSQL … are compatible with Hyperdrive caching." How the rule is
  applied is not published, so the claim cannot be tested.
- **ReadySet** [code]: gives each client its own upstream connection, so nothing leaks, and
  shares only by positive recognition. Its automatic cache check is default-deny: a function
  is acceptable only if it is on a list generated from `pg_proc` — every overload immutable,
  in every PostgreSQL major from 13 to 17 (`gen_pg_builtins.py:64`) [checked]. The curated
  lists of bad functions only choose the refusal message; the comment says a builtin missing
  from them "is still denied …, so a forgotten entry never opens a caching hole"
  (`auto_cache_eligibility/mod.rs:926-933`) [checked]. Before creating a cache it asks the
  real server to prepare the statement. Two cases still get through [inferred]:
  `'now'::timestamptz` (no function call to check) and a view over a volatile function.

## 4. What PostgreSQL itself offers a proxy

From [canvas/findings/12-postgres-server.md](canvas/findings/12-postgres-server.md);
experiment scripts and outputs are in
[canvas/experiments/postgres-server/](canvas/experiments/postgres-server/). This section
matters most for option (b), which rests on the server's behaviour.

**`DISCARD ALL` is a fixed list of nine actions** (`src/backend/commands/discard.c:57-79`)
[code]: close all cursors, reset session authorization, `RESET ALL`, drop prepared
statements, `UNLISTEN *`, release session advisory locks, drop cached plans, drop temp
tables, reset sequence caches. Experiments confirmed each [run].

**What it leaves behind** [run unless marked]:

| Left on the connection | Who can cause it | Effect on the next tenant |
| --- | --- | --- |
| The random seed. `setseed(0.5); DISCARD ALL; random()` returns the same number as without the reset; the `seed` setting is exempt from `RESET ALL` (`guc_parameters.dat:2629`) [checked] | any role | the previous tenant chooses the next tenant's `random()` sequence |
| The existence of a custom setting name (`SET myapp.x = …`); the value is cleared | any role | `current_setting('myapp.x', true)` is `''`, not null |
| Loaded libraries and whatever state extensions keep in memory; interpreter globals of PL/Perl and PL/Python [code: no hook in `DiscardAll`; not run] | roles allowed to use them | whatever that code stores |
| A replication-origin session | privileged roles | the next client's writes are tagged |

**It must be its own message.** `DISCARD ALL` is refused inside a transaction block, and a
multi-statement `Query` is an implicit block, so `ROLLBACK; DISCARD ALL` in one message
fails [run]. Sent as two messages it works.

**It is cheap on a clean session** [run: `pgbench`, one client, Unix socket, PostgreSQL
17.11, shared VM]:

| Script | Average latency |
| --- | --- |
| `SELECT 1` | 0.056 ms |
| `DISCARD ALL` on a clean session | 0.044 ms |
| `SELECT 1`, then `DISCARD ALL` | 0.099 ms |
| A query asking the catalog whether the session is dirty | 3.24 ms |

So a reset after every lease costs about one more empty round trip. Asking the server
whether a reset is needed costs seventy times the reset.

**The server reports what ran.** Each statement in a `Query` message gets its own
`CommandComplete` tag, built from the server's parse tree (`src/backend/tcop/cmdtag.c`)
[code]. `SELECT 1; SET search_path = x` comes back as `SELECT 1`, then `SET` [run]. The tags
for session statements are `SET`, `RESET`, `PREPARE`, `DEALLOCATE`, `LISTEN`, `UNLISTEN`,
`DECLARE CURSOR`, `CREATE TABLE`, `LOAD`, `DISCARD …`, `DO`, `CALL`. Two limits:

- A change made through an expression has the tag of its statement: `SELECT
  set_config(…)`, `SELECT pg_advisory_lock(1)` and `SELECT f()` are all `SELECT 1`. A
  plpgsql function called from a `SELECT` did `SET`, `LISTEN`, `PREPARE` and created a temp
  table, and all four persisted [run].
- `SELECT … INTO TEMP t` and `CREATE TEMP TABLE t AS SELECT …` are tagged `SELECT n`
  (`src/backend/commands/createas.c:347-349`) [checked] [run].

**The server reports some settings.** `ParameterStatus` follows any change to a reported
setting, however it was made [run with `set_config`]. `search_path` is on that list only
from PostgreSQL 18 (`doc/src/sgml/protocol.sgml:1549-1550`) [checked]. Our plan's second
line of defence (decision D8) watches this message; **on PostgreSQL 17, the version our
tests run against, it does not see `search_path`.**

**A read-only transaction does not protect session state.** Inside `BEGIN READ ONLY`, these
succeeded: `SET`, `set_config(…, false)`, `PREPARE`, `LISTEN`, `pg_advisory_lock`, `DECLARE …
WITH HOLD`, an insert into an existing temp table [run]. The server's comment: "These modify
only backend-local state, so they're OK to run in a read-only transaction"
(`src/backend/tcop/utility.c:259-279`).

**A read-only wrapper around client text can be escaped.** Both of these wrote a row [run]:

```
BEGIN READ ONLY; SELECT 1; COMMIT; INSERT INTO t VALUES (7); COMMIT
BEGIN READ ONLY; SET TRANSACTION READ WRITE; INSERT INTO t VALUES (7); COMMIT
```

The wrapper is sound only when the client's text is exactly one statement. The server can
enforce that: an extended-protocol `Parse` refuses a string with more than one command
(`src/backend/tcop/postgres.c:1503-1506`) [checked]. The full wrapper built that way was not
tested.

**Privileges can close some paths outright** [run, as an ordinary role]:

- `REVOKE TEMPORARY ON DATABASE … FROM PUBLIC` refuses temp tables by every route,
  including inside functions and `SELECT INTO TEMP`.
- `REVOKE EXECUTE` on `set_config` refuses the function and `UPDATE pg_settings` with it.
- Session advisory-lock functions can be revoked, one overload at a time.
- **No privilege exists** for plain `SET`, `PREPARE`, `LISTEN` or `DECLARE … WITH HOLD`.

**Volatility labels are not proof.** An `IMMUTABLE` plpgsql function called `set_config` and
`pg_advisory_lock`, and both took effect [run]. The server's own "does this query contain
volatile functions" code is not exposed to clients.

**History.** The built-in pooler proposed for PostgreSQL in 2019 marked a backend "tainted"
at the point where session state was created, inside the server, and stopped pooling it. It
was returned with feedback in 2021 [web]. Nobody in that discussion proposed classifying text.

## 5. Answers to Nick

### (i) Does any project claim a complete solution to deciding safety from SQL text, and does the claim survive its code?

**No project makes that claim for session state.** What each says about its own reading:

| Project | Its own words | After reading the code |
| --- | --- | --- |
| PgBouncer | transaction pooling "breaks client expectations of the server by design" | Accurate. It reads nothing |
| PgCat | "Non-exhaustive list … a best effort" (code comment) | Accurate |
| Supavisor | "Queries the check skips, such as those larger than 1 KB, are not caught" | Accurate |
| RDS Proxy | "doesn't detect any session state changes resulting from such calls" | Closed; the statement matches our own stated limit |
| ProxySQL | lists conditions; `set_config` is "forwarded as an ordinary query, untracked" by default | Accurate; more gaps than it lists |
| Pgpool-II | presents its lists as tunable heuristics | Accurate for routing. Its sentence "When the underlying table gets updated, corresponding cache entries are deleted" does not hold for writes it cannot see |
| **PgDog** | "ensuring session state is set correctly"; a full mark against PgBouncer's "Partial" | **Holds only for a visible `SET`.** Not for `set_config` in an expression, temp tables made by `CREATE … AS`, computed advisory-lock keys, or anything in the default single-database mode that does not start with a listed keyword [inferred] |
| **YugabyteDB** | sticky connections "to offer complete correctness/range of support" | With a modified server. Its own limitations page lists state that still leaks |
| **Hyperdrive** | "Only functions designated as `IMMUTABLE` … are compatible" | Closed source; stated as a rule, with only a list of "common" examples. Cannot be tested |

The three strongest claims are in bold. None survives as a claim of completeness, and two
of the three are not about text at all (YugabyteDB's signal comes from the server;
Hyperdrive's mechanism is unpublished).

The canvas also tested the assessment's prediction about option (c1) five times over.
Pgpool-II, Odyssey, PgDog and Supavisor use PostgreSQL's real grammar, and ShardingSphere
a full derived one. In each, the agent found by reading at least one statement its list on
top of the tree gets wrong (section 3). These are [inferred], not executed, and I would
not cite any single one as a confirmed bug. Together they are consistent: the tree is exact
and the list on top of it is hand-made.

### (ii) Which approaches in the wild map to options (a), (b) and (c1)?

**(a) A deny-list read from text or a tree, with safety resting on it.**
ProxySQL (substring and prefix tests, then pin); RDS Proxy (list, then pin);
PgDog in its default single-database mode (keyword regex, no reset behind it); Supavisor's
optional detector (a real parser doing a deny-list's job); ShardingSphere's interception of
`SET`; Pgpool-II's query cache. Every one of these either states that it is incomplete or
was found incomplete by reading. ProxySQL is the longest-running example and is still adding
cases. Nothing in the canvas argues for (a).

**(b) Safety from a mechanism; reading only as an optimisation.**

- *Reset on every release, no reading:* Odyssey by default; PgBouncer and pgagroal with one
  switch; Pgpool-II at session end; Hyperdrive by its documentation. This is (b)'s strict
  mode, and it is the only form with a clean record.
- *Reset when a detector fires:* PgCat (server tags), PgDog (parse tree), ProxySQL
  (substrings). All three reset connections they know are dirty and trust the rest. That is
  the opposite direction from (b), which resets everything it cannot show is clean. **No
  project implements (b)'s fail-closed conditional reset.** The idea is sound, but it has no
  production precedent; the unconditional form has.
- *Never share:* Supavisor (pool per tenant and user), ReadySet (connection per client),
  Pgpool-II (connection per session). Not available to us under decision D6.

**(c1) A real parser.** Pgpool-II and Odyssey vendor the grammar; PgDog and Supavisor link
libpg_query. In all four the parser serves routing or detection, and none relies on it for
session safety without a reset or an isolated pool behind it — except PgDog, which has the
gaps listed above. Derived grammars (ShardingSphere, PgCat's sqlparser-rs, CockroachDB's)
add a second risk: they disagree with PostgreSQL's scanner in places.

**(c2) Let the server enforce read-only.** Nobody wraps statements in `BEGIN READ ONLY`.
Every read/write splitter gets the same effect from a standby that refuses writes, and
Supavisor 2.0.0 reacted to the refusal (`25006`) by re-running on the primary. The principle
is well used; our particular form of it has the escape described in section 4.

**Hinted sharing, option (d2).** Prisma Accelerate (the client marks each cacheable query),
ReadySet's explicit `CREATE CACHE`, Pgpool-II's `/*FORCE QUERY CACHE*/` prefix, Hyperdrive's
second cache-disabled binding. Letting the client declare is common in shipped products.

**Pinning** (ruled out by steering answer 9) is the fallback of RDS Proxy, ProxySQL, PgDog,
Vitess and YugabyteDB. It is the most common answer in the wild to "session state seen".

### (iii) What is new, and does it change the recommendation?

**The recommendation stands: (b), staged with (d1).** Nothing found supports (a). Nothing
found makes (c1) necessary. The canvas changes details inside (b); items 1 to 4 belong in
the new chunk 5 brief, and item 1 carries a question for Nick.

**1. Reset after every lease can be the default, not a switch. — changes the default inside (b)**

The assessment made the reset conditional on "known clean" because it assumed the reset had
a real cost. Measured, `DISCARD ALL` on a clean session takes 0.044 ms on the server against
0.056 ms for `SELECT 1`. Odyssey ships reset-every-transaction as its default. And every
project that resets conditionally has leaks, while (b)'s own conditional form has no
precedent.

My judgment: make strict mode the default and keep "known clean" as an optimisation that
can be switched on once chunk 14 has measured both. Then no reading of any kind is in the
trust base for session state, and the function-hidden case — today's stated limit — is
closed by default.

What it costs: one extra round trip to the database per lease. It happens after the reply
has been relayed, so the client does not wait for it, but the connection is busy for that
long and pool throughput drops accordingly. My number is from a Unix socket with one
client; over a network the round trip dominates. Chunk 14 must measure it on the benchmark
link before the default is final.

This is a third change to decision D8 (from "optional switch", past the assessment's
"default when not known clean", to "default always"), so it needs Nick's word.

**2. Decide "known clean" from the server's reply, not from the client's text. — replaces part of (b)**

The assessment's test was: every statement's first keyword is on an allow-list. The
explainer noticed this needs statement boundaries, which is the lexer's job, and patched it
with a rule about semicolons. The server already gives the exact answer: one
`CommandComplete` tag per statement executed. PgCat, PgBouncer, PgDog, Odyssey and ProxySQL
each use a tag for some purpose; none uses the whole idea.

The rule for the brief: a lease is known clean only if every tag is on a short list
(`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `SHOW`, `EXPLAIN`, and the
transaction-control tags), no `ParameterStatus` arrived that the proxy did not cause, and
no error was reported. Any other tag, including `DO`, `CALL` and any tag not recognised,
means reset. This is a string comparison in the reply tracker that chunk 4 already has. It
cannot be fooled by quoting, comments or packing several statements into one message, and
it removes the last dependence on finding statement boundaries in text.

It does not see changes made inside an expression, and it does not see `SELECT … INTO TEMP`.
So the assessment's substring list (`set_config`, `advisory`, `temp`, …) stays beside it,
doing the job it was designed for. Under item 1 the whole test is an optimisation and a
miss in it matters only when the optimisation is on.

The courtesy refusal with `0A000` still reads the first keyword of the text, because a tag
arrives too late to refuse anything.

**3. `DISCARD ALL` has a residue and a rule for sending it. — adds to (b)**

- It must be sent as its own `Query` message after the lease ends with status `I`; never
  joined to a `ROLLBACK`.
- It does not reset the random seed. The brief should either follow the reset with a
  reseed or state the limit.
- It does not touch state kept by extensions or by PL/Perl and PL/Python. This is a
  deployment rule for the book: tenants of a shared pool must not have those languages or
  unvetted extensions.
- Use `DISCARD ALL` itself, not a hand-picked list. PgCat's, PgDog's and Odyssey's reduced
  lists each leave something out. PostgreSQL maintains the full one.
- The reset makes the server send `ParameterStatus` (our own probe shows it for
  `application_name`); the D8 watch must not count the proxy's own reset as a client's
  change.
- On PostgreSQL 17 the D8 watch does not cover `search_path`. The plan and the book
  should say so. It is one more reason for item 1.

**4. Option (c2) as written is unsound for text the proxy has not bounded. — corrects the assessment**

The assessment proposed sending a shareable read as `BEGIN READ ONLY; …; COMMIT` in one
message, as a later strengthening. A client whose text contains `; COMMIT; INSERT …` escapes
it. Inside (b)'s small recognised shape there is no semicolon, so the wrapper is sound
there. For hinted statements, which may be any text, it is not. If (c2) is ever built, the
client's text must go to the server as an extended-protocol `Parse`, which refuses more than
one statement. That is untested and is not needed for the ruling. Until then a hinted
statement is shared on the client's word, which is what `hinted` means.

Also: a read-only transaction permits nearly every session change, so (c2) never helped
decision 3.

**5. A test that does not depend on any text rule. — adds to chunks 6 and 7**

A single catalog query reports whether a session holds prepared statements, cursors,
listens, advisory locks, session settings, temp tables or a changed role
([canvas/experiments/postgres-server/dirty-check.sql](canvas/experiments/postgres-server/dirty-check.sql)).
At 3.2 ms it is useless on the request path. In tier R it is the right oracle: after every
lease in a hostile workload, ask the real server whether anything is left. That checks
"session state never crosses clients" directly, whatever rule produced the result.

**6. Deployment hardening that shrinks the function-hidden case. — optional, for the book**

`REVOKE TEMPORARY` on the database, `REVOKE EXECUTE` on `set_config`, the session
advisory-lock functions and `setseed`, and tenants without the right to create functions.
What remains reachable is then a top-level `SET`, `PREPARE`, `LISTEN` or `DECLARE`, which
the tag in item 2 sees exactly. The proxy could check these privileges at start-up and say
so in its health report. This matters only if strict mode is turned off.

**7. For the sharing decision: generate the function allow-list, and one shape to refuse. — for the chunk 5 and 11 briefs**

- The parked code has about 150 hand-listed safe functions. ReadySet generates its list from
  `pg_proc`: names whose every overload is immutable. That is a 200-line script against a
  real server and it turns "a list someone wrote" into data. It could also run against the
  actual database at start-up.
- A typed literal — `timestamp 'now'`, `date 'today'` — is volatile and contains no
  function call and no cast operator. It is made only of letters, a space and a quote, so it
  fits the small shape as the assessment describes it. Pgpool-II and ReadySet both miss this
  family. The new recogniser must refuse a string literal that directly follows a word. This
  is my own observation from their holes; it has not been tested against a build, because
  the narrowed recogniser does not exist yet.
- Vitess, the only coalescer found in production, shares any `SELECT` outside a transaction
  and accepts that concurrent callers get one `NOW()`. That is a looser contract than our
  strict default (ruling 2). I do not suggest loosening it; it is a data point for the
  `relaxed` mode's documentation.

**Considered and not adopted now.**

- *Track and replay settings* (PgBouncer, PgDog, Odyssey, ProxySQL), *connections tagged by
  settings* (Vitess), or a `SET LOCAL` prefix per transaction. These would let a tenant keep
  a few settings instead of being refused. That reverses steering answer 9 and is a feature,
  not a safety fix. Vitess's pool (`go/pools/smartconnpool`, Apache-2.0, about 1,700 lines of
  Go) is the design to study if it is ever wanted.
- *Reset only when the next lease is for a different tenant.* Tag each pooled connection
  with the tenant that last used it. Cross-tenant isolation is then unconditional, and the
  reset is paid only on a tenant switch; within a tenant, state could cross clients as in
  PgBouncer. A cheaper form of strict mode worth measuring in chunk 14 if item 1's cost
  turns out to matter. It touches the pool's fairness logic, so I would not put it in v1
  without that evidence.
- *A real parser.* Not needed under (b). If it is ever wanted for wider automatic sharing,
  the cost is now measured (section 3), and the reading must be distrusted whenever the
  server reports `standard_conforming_strings` other than `on`.
- *Asking the catalog about each function* (Pgpool-II). It answers volatility, not session
  effects, and the labels can lie.
- *A server-side extension that reports session state* (YugabyteDB's patch, Odyssey's
  private extension). The only exact detector, and outside "one ordinary Postgres".
- *Pinning.* Ruled out; nothing here reopens it.

## 6. What Nick is asked to rule, updated

The four questions at the end of the explainer stand. The canvas adds one and sharpens one.

1. Which option. Still (b), staged with (d1).
2. If (b): the changes to steering answer 9 and decisions D8 and D10, as before.
3. **New:** inside (b), whether the reset runs after every lease by default (my
   recommendation after the canvas), with "known clean" as an optimisation to be switched on
   after measurement — or conditionally by default, as the assessment proposed.
4. If (a): as before. The canvas found no project for which continuing a deny-list ended.
5. Whether 5a lands now. Still nothing against it.

## 7. Limits of this canvas

- **Holes in other projects were read, not run.** Every such statement is marked
  [inferred]. Agents were told not to build the projects. Any of them may be wrong in
  detail; the pattern across five projects is what the conclusion uses.
- **Closed products are documentation only.** How RDS Proxy and Hyperdrive read SQL is not
  published.
- **Experiments ran on PostgreSQL 17.11** (and 16 for one lexical probe), on a shared
  four-core VM. Line numbers for PostgreSQL are from master. The `search_path` report on
  PostgreSQL 18 is from the documentation and source, not from a running 18 server. The
  behaviour of PL/Perl, PL/Python and dblink across `DISCARD ALL` is inferred from the
  missing hook; they were not installed.
- **Timing numbers are single runs** on a loaded machine: one client over a Unix socket for
  `DISCARD ALL`; `-benchtime 300ms` for the parser.
- **Not examined:** Heimdall Data, Neon's proxy, AlloyDB and Azure managed pooling, upstream
  Odyssey's unpublished extension, the Postgres-XL official repository (read from a 2017
  mirror).
- **What I re-checked myself** after the agents finished, by opening the cited lines:
  PgBouncer `varcache.c:98`, `objects.c:1231`, `server.c:542` and the `NEWS.md` entries on
  `search_path`; PostgreSQL's lists of reported settings and of settings exempt from
  `RESET ALL` (including `seed`), `createas.c:349`, `postgres.c:1506`, `protocol.sgml:1549`;
  YugabyteDB `xact.c:5505`; PgCat `server.rs:1016-1024`; Odyssey `reset.c:193`, `:205` and
  `pool.c:23-24`; Vitess `pool.go:829` and `config.go:204`; libpg_query
  `pg_query_split.c:19`, `:43`; PgDog `function.rs:3` and `cleanup.rs:14-20`; Pgpool-II
  `pool_memqcache.c:1113-1130`; ReadySet `mod.rs:933` and `gen_pg_builtins.py:64`; Supavisor
  `message_handler_helpers.ex:15`, `:33`; the `DISCARD ALL` benchmark output. All matched.
  I did not re-run the experiments.
