# Pgpool-II — a parser derived from PostgreSQL's, in a proxy

Research by a fresh Opus agent, 2026-11-25, for mini-gateway chunk 5 (statement classifier).
All line numbers refer to the clone at the commit below. Tags used:
**[CODE]** = VERIFIED IN CODE (I read the code path); **[DOCS]** = DOCS ONLY;
**[NOT FOUND]** = COULD NOT FIND; **[INFERRED]** = follows from code I read, but I did not run it.
Nothing was built or run. Every probe answer comes from reading the code.

## Sources

| Project | URL | Commit SHA read | Commit date | Newest release | Local / web |
| --- | --- | --- | --- | --- | --- |
| Pgpool-II | https://github.com/pgpool/pgpool2 (mirror of git.postgresql.org pgpool2) | `d30ad43b87ca3573db04651d49e02315b23918cd` | 2026-11-24 | tag `V4_7_3`, 2026-09-25 (from `gh api .../commits/V4_7_3`) | LOCAL SOURCE (`repos/pgpool2`, `--depth 1`, 21 MB); commit history of `src/parser/gram.y` from `gh api` |

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| Pgpool-II | A copy of PostgreSQL's own `gram.y` + `scan.l` (20,155 + 1,439 lines; PG 19 grammar imported 2026-11-06), plus the catalog of the backend (`pg_proc.provolatile`, `pg_class`) queried through a cache | **Pin + reset.** Session pooling: one client owns its backend connections for the whole session; on disconnect `ABORT; DISCARD ALL` (default), connection dropped if the reset fails. Cached connections are reused only for the same user + database. | Routing: allow-list of statement *types* (SELECT, COPY TO, SHOW, EXPLAIN-in-some-modes may go to a standby; everything else to the primary), then per-SELECT checks with catalog lookups. Query cache: SELECT passes ~12 checks, invalidated by table OIDs of write statements seen by Pgpool. | Statement types: allow-list. Functions: **catalog volatility by default** (volatile = write); else `read_only_function_list` (allow-list) or `write_function_list` (deny-list). Cache: deny-list of features (non-immutable functions, timestamptz casts, views, ...). | Routing miss: write sent to a read-only standby → **server rejects it** (error, no harm to data). Cache miss: **stale or wrong data returned** to later sessions of the same user+db. | Parse error → treated as a write, sent to primary, not cached: **closed**. Multi-statement → primary only, not cached: **closed**. Cache rules on things it does not model (attribute-notation calls, `'now'::timestamp`, search_path): **open**. |

## Pgpool-II

### 1. Does it read SQL, with what, and how is it kept in step?

**The real PostgreSQL grammar, vendored.** [CODE] `src/parser/` holds `gram.y` (20,155 lines),
`gram_minimal.y` (20,135), `gram_template.y`, `scan.l` (1,439), `keywords.c`, node
copy/out functions. `src/parser/README` describes the sync procedure:

> To import the PostgreSQL parser, merge all the changes in gram_template.y file
> from PostgreSQL's gram.y, and then run 'make generate_parsers' to generate
> updated minimal and standard grammar files for Pgpool-II

So the sync is a **manual merge, once per PostgreSQL major release**. Commit history of
`src/parser/gram.y` (`gh api repos/pgpool/pgpool2/commits?path=src/parser/gram.y`):

| Date | Commit subject |
| --- | --- |
| 2026-11-06 | Feature: Import PostgreSQL 19 parser. |
| 2025-10-03 | Feature: Import PostgreSQL 18 new parser. |
| 2024-10-21 | Feature: Import PostgreSQL 17 RC1 new parser. |
| 2023-07-31 / 2023-10-30 | Import PostgreSQL 16 BETA1 parser / update to 16 RC1 |
| 2022-09-21, 2021-09-27, 2020-09-07, 2019-07-24, 2018-08-28, 2017-10-03 | PG 15 BETA4, 14 beta2, 13 beta3, 12 beta2, 11 beta3, 10 |
| 2016-09-07, 2015-10-13, 2014-07-07 | PG 9.6, 9.5, 9.4 raw parser |

That is one import per year, landing between beta and a few weeks after the PostgreSQL
release. The lag matters only for new syntax: unknown syntax is a parse error, which fails
closed (below).

There are **two grammars** [CODE]. For a query longer than 10 KB
(`LENGTHY_QUERY_STRING 1024*10`, `pool_proto_modules.c:223`) in streaming-replication mode,
Pgpool uses `gram_minimal.y`, which differs by `InsertStmtShort` / `UpdateStmtShort`
productions (it skips parsing the bodies of large INSERT/UPDATE statements for speed)
(`pool_proto_modules.c:304-331`; `diff gram.y gram_minimal.y`).

A **third lexer**: multi-statement detection for long queries uses a copy of psql's lexer
(`src/utils/psqlscan.l`, `multi_statement_query()`, `pool_proto_modules.c:5140`) and counts
`PSCAN_SEMICOLON`. For short queries the statement count is `list_length(parse_tree_list)`
from the real grammar (`pool_proto_modules.c:337-344`).

**What the parse tree is used for:** it is a *raw* parse tree (no name resolution). Function
names, table names and types are strings. Everything that needs meaning (is this function
volatile, is this table temporary/unlogged/a view, is this a catalog table) is looked up in
the backend's catalog — see "Catalog layer" below.

**Lexical version skew** [CODE + INFERRED]. Pgpool's `scan.l` at this commit starts every
`'...'` literal in the standard-conforming state unconditionally (`scan.l:546-551`
`{xqstart} ... BEGIN(xq);`), and nothing in `src/parser` reads `standard_conforming_strings`
(grep: no hits). Upstream PostgreSQL master's `scan.l` does the same (fetched via
`gh api repos/postgres/postgres/contents/src/backend/parser/scan.l`, `{xqstart}` → `BEGIN(xq)`),
so against PG 19 the two agree. Against an older server running with
`standard_conforming_strings = off` (which PG ≤ 18 still accepts — from memory, not
re-checked), Pgpool's lexer and the server's would disagree about where a string such as
`'a\'b'` ends. I did not find any code that handles this. This is the same class of hole as
mini-gateway's review-2 blocker: the proxy's lexer and the server's lexer must be the *same
version with the same settings*, and vendoring the parser makes the first true but not the
second.

### 2. Session-state safety

**Mechanism: pin the client to the connection for its whole session, then reset.**
Pgpool does not need mini-gateway's D3 decision, because it never shares a backend
connection between clients while either is connected.

- [CODE] Each Pgpool child process serves one client; its backend connections are cached
  per child and reused only for a later client with the **same user and database**:
  `pool_get_cp()` matches `sp->user` and `sp->database` (`pool_connection_pool.c:98-130`).
- [CODE] On client exit, `connection_cache` on → `pool_process_query(..., 1)` runs the reset
  queries (`child.c:546-560`); `reset_backend()` sends each element of `reset_query_list`
  through `SimpleQuery` (`pool_process_query.c:1003-1065`). ABORT is skipped if every backend
  is idle (`TSTATE == 'I'`).
- [CODE] Default `reset_query_list` is `"ABORT;DISCARD ALL"`
  (`src/config/pool_config_variables.c:51`).
- [CODE] **Fail closed on reset failure:** if the reset errors, or the frontend socket
  ended in EOF/error, or the database is `template0/template1/postgres/regression`, the
  connection is not cached (`child.c:571-584`, `reset_query_error`).
- [DOCS] There is no transaction-level pooling mode in Pgpool's documentation (grep of
  `doc/src/sgml` for "transaction pool"/"transaction mode": no hits outside release notes).

**SET with several backends** [CODE]: `send_to_where()` returns `POOL_BOTH` for every
`VariableSetStmt` (except `SET transaction_read_only = off`, `SET ... READ WRITE` and
serializable isolation, which go to the primary) and for `DiscardStmt`
(`pool_query_context.c:1055-1136`). `POOL_BOTH` → `pool_setall_node_to_be_sent()`
(`pool_query_context.c:2252-2266`). So a plain `SET` reaches primary and standbys, keeping
the session's settings the same on every node the session may be load-balanced to.

**But a session change through a function only reaches the primary** [INFERRED from
CODE]: `SELECT set_config('search_path','h',false)` is a `SelectStmt`; `set_config` is
volatile in `pg_proc`, so `pool_has_function_call()` is true and the statement is sent to
the primary only (`pool_query_context.c:2364-2373`). The standby connection of the same
session keeps the old `search_path`, and later load-balanced SELECTs run under it. I found
no code or documentation that addresses this. This is mini-gateway's hole pattern
("session state through a function") reappearing as *divergence between nodes* rather
than *leak between tenants*.

**Temp tables** [CODE]: `CREATE TEMP TABLE` is not a SELECT → primary. Later SELECTs that
mention a temp table go to the primary if `check_temp_table` (default `catalog`,
`pool_config_variables.c:2618-2626`) finds the name in `pg_class` in a `pg_temp_*`
namespace (`ISTEMPQUERY83`, `pool_select_walker.c:714`), or, with `check_temp_table = trace`,
in the session's own list of tables it saw created (`pool_temp_tables_find`,
`pool_select_walker.c:733-751`, maintained in `CommandComplete.c`). The catalog query
matches temp tables of *any* session with that name, which errs toward the primary.
Temp tables disappear at session end through `DISCARD ALL`.

**Prepared statements** [CODE]: `PREPARE` is routed as its inner query
(`send_to_where` recursive call, `pool_query_context.c:1146-1153`); `EXECUTE`/`DEALLOCATE`
inherit the destination recorded for the PREPARE (`where_to_send_deallocate`,
`pool_query_context.c:1201`). **LISTEN, DECLARE ... WITH HOLD, advisory locks**: no special
code; `LISTEN`/`DECLARE` fall through to "All other statements are sent to primary"
(`pool_query_context.c:1187-1193`); `pg_advisory_lock` is volatile → primary. All are wiped
at session end by `DISCARD ALL` (which in PostgreSQL also unlistens, closes cursors, releases
session advisory locks — PostgreSQL behaviour, from memory, not re-checked here).

### 3. Read-vs-write and cacheability decisions

#### 3a. Routing (load balancing), streaming-replication mode

Entry point `pool_where_to_send()` (`pool_query_context.c:493`):

```c
else if (MAIN_REPLICA)
{
    if (query_context->is_multi_statement)
        ...
        pool_set_node_to_be_sent(query_context, PRIMARY_NODE_ID);
    else
        where_to_send_main_replica(query_context, query, node);
}
```

Step 1 — **statement type allow-list**, `send_to_where()` (`pool_query_context.c:938-1191`)
[CODE]. Only these can return `POOL_EITHER` (load-balanceable):
`SelectStmt` without `INTO`/`FOR UPDATE|SHARE` (`pool_has_insertinto_or_locking_clause`) and
whose every CTE is a `SelectStmt`; `COPY ... TO` with no query or a SELECT query;
`VariableShowStmt`. Everything else ends in

```c
	/*
	 * All other statements are sent to primary
	 */
	return POOL_PRIMARY;
```

There is **no `ExplainStmt` branch** in `send_to_where` (grep of `pool_query_context.c` for
"explain": no hits), so in streaming-replication mode every `EXPLAIN` goes to the primary
[CODE], although the docs list EXPLAIN as load-balanceable (`loadbalance.sgml`, "Writing
queries may affect Load Balancing"). `is_select_query()` does handle EXPLAIN
(`pool_process_query.c:1178-1215`) and is used for native-replication mode
(`pool_query_context.c:2475`). The discrepancy errs to the safe side.

Step 2 — **per-SELECT checks**, `where_to_send_main_replica()`
(`pool_query_context.c:2209-2452`) [CODE]. A SELECT that passed step 1 is sent to the primary
if any of these holds, in this order:

1. inside a transaction that has written, with `disable_load_balance_on_write = transaction`
   (default) or `always`; or an aborted/serializable transaction (`:2268-2302`);
2. `is_select_query()` false — with `allow_sql_comments = off` (default) this includes **any
   SELECT whose text does not start with `s S ( w W t T ; \0`** (`pool_process_query.c:1144-1150`),
   i.e. a leading comment routes to the primary (the documented `/*REPLICATION*/` trick);
3. `pool_has_system_catalog()` (catalog lookup, below);
4. `check_temp_table && pool_has_temp_table()`;
5. `check_unlogged_table && pool_has_unlogged_table()`;
6. `pattern_compare(query, WRITELIST, "primary_routing_query_pattern_list")` — a **regex over
   the raw query text**, user-supplied (`:2353-2360`);
7. `pool_has_function_call()` — a "writing" function appears (below);
8. `is_select_object_in_temp_write_list()` — `dml_adaptive` mode: objects written earlier in
   this transaction;
9. `dml_adaptive_global` mode: tables recently written by any session
   (`where_to_send_dml_adaptive_global`).

Otherwise it is sent to the session's load-balance node (chosen per session, or per
statement with `statement_level_load_balance`), unless replication delay is too high.

**Function classification**, `function_call_walker()` (`pool_select_walker.c:368-452`)
[CODE]. It walks the raw tree and looks only at `FuncCall` nodes:

```c
if (pool_config->num_read_only_function_list == 0 &&
    pool_config->num_write_function_list == 0)
{
    if (function_volatile_property(fname, FUNC_VOLATILE))
    {
        ctx->has_function_call = true;
        return true;
    }
    ...
}
/* read_only list set: anything NOT matching it is a writing function */
/* write list set: only functions matching it are writing functions */
```

- Default: both lists empty (`pool_config_variables.c:55-56`) → **ask the catalog**:
  volatile = writing. This is neither an allow-list nor a deny-list maintained by Pgpool; it
  delegates to the function author's volatility marking.
- `read_only_function_list` set → **allow-list** (unknown function = writing; fails closed).
- `write_function_list` set → **deny-list** (unknown function = read; fails open — a miss
  sends a write to a standby, which then errors). Docs: the two lists are "mutually
  exclusive" (`loadbalance.sgml`, write_function_list).
- Function names in the lists are regexes anchored with `^…$`; schema-qualified and
  unqualified spellings must both be listed (docs, same section).

#### 3b. The catalog layer: how Pgpool asks the backend

[CODE] `function_volatile_property()` (`pool_select_walker.c:1212-1310`) builds:

```c
#define VOLATILE_FUNCTION_QUERY "SELECT count(*) FROM pg_catalog.pg_proc AS p, pg_catalog.pg_namespace AS n WHERE p.proname = '%s' AND n.oid = p.pronamespace AND n.nspname %s '%s' AND p.provolatile = '%c'"
...
	if (list_length(names) == 2)   /* schema.func */
		snprintf(query, ..., llast(names), "=", linitial(names), prop_volatile);
	else
		snprintf(query, ..., llast(names), "~", ".*", prop_volatile);
```

Precisely:
- **Lookup is by name only**, not by signature, not by `search_path`. An unqualified name
  matches functions of that name **in every schema and every overload**.
  - For routing ("is it volatile?") that is conservative: any volatile overload anywhere → primary.
  - For the query cache ("is it immutable?", `is_immutable_function`, `:1200`) it is
    **permissive**: if *any* overload in *any* schema is immutable, the call is treated as
    immutable [INFERRED]. E.g. `SELECT f(1)` with `f(int)` volatile and `f(text)` immutable,
    or a user `public.lower(text)` that is volatile, shadowing immutable `pg_catalog.lower`.
    The docs admit only the timestamptz-overload variant of this ("If multiple functions
    having the same name are registered and one of them returns TIMESTAMP WITH TIMEZONE..."
    `memcache.sgml`); I found nothing about volatility overloads.
- The name is interpolated **without escaping** (`'%s'`) [CODE]. I did not test what a
  function name containing `'` does to this catalog query.
- **Where it runs:** on the client's own session connection (`do_query(CONNECTION(backend,
  node_id), ...)`, `pool_relcache.c:262`), to the primary by default or the load-balance
  node with `relcache_query_target = load_balance_node` (`pool_relcache.c:139-158`).
- **Cache** (`pool_search_relcache`, `pool_relcache.c`): per child process, `relcache_size`
  entries (default 256), keyed by `(database name, query string)` (`:184`); LRU-ish
  replacement by refcount (`:315-345`). Session-local caches (temp-table check:
  `pool_create_relcache(..., true)`, `pool_select_walker.c:781`) are keyed also by session id.
  With `enable_shared_relcache = on` (default) non-session-local results are also stored in
  the shared query cache and read by every child (`pool_relcache.c:232-290`).
- **Invalidation:** only by time — `relcache_expire`, **default 0 = never**
  (`pgpool.conf.sample-stream:1000`), or child process exit; plus one special case:
  `CREATE TEMP TABLE` discards the temp-table relcache (`pool_proto_modules.c:4770-4781`).
  The docs state the staleness plainly (`misc-config.sgml`, enable_shared_relcache):
  > Cache invalidation is not happen even if the system catalog is modified. So it is
  > strongly recommend to set time out base cache invalidation by using relcache_expire
- **Staleness risks** [INFERRED]: `ALTER FUNCTION f VOLATILE` or `CREATE OR REPLACE` with a
  new volatility is not seen until expiry or restart. Routing consequence: a now-writing
  function is sent to a standby and **fails there** (standby is read-only). Cache
  consequence: a function that became volatile is still treated as immutable and its
  results are cached → stale results. A table converted to a view/unlogged, or a new
  temp table shadowing a name, has the same lag (the temp case is special-cased).

Other catalog checks, same machinery [CODE]: `is_system_catalog` (`pg_class` in
`pg_catalog`, `:618-627`), `is_temp_table` (`:700-796`), `is_unlogged_table`
(`relpersistence = 'u'`, `:822-832`), `is_view` (`relkind in ('v','m')`, `:910-914`),
row security (`relrowsecurity`, `:978`), table name → OID via `to_regclass` or
`pgpool_regclass` (`:1321-1323`, used for cache invalidation).

#### 3c. In-memory query cache (`src/query_cache/pool_memqcache.c`, 4,916 lines)

**Lookup happens before parsing** [CODE] (`pool_proto_modules.c:255-296`): if
`pool_is_likely_select(contents)` (skips leading comments, then text starts `SELECT` or
`WITH`, `pool_memqcache.c:897-975`) and the transaction has not written, the cache is
probed with key `md5(user \0 query text \0 database)` (`encode_key`, `pool_memqcache.c:567-605`).
A hit returns stored bytes without contacting PostgreSQL. Only text that was earlier judged
cacheable can be in the cache, so the safety decision is made at store time.

**Store decision** (`pool_proto_modules.c:592-614`): not multi-statement, parsed OK,
`IsA(node, SelectStmt)`, and `pool_is_allow_to_cache()` (`pool_memqcache.c:982-1135`)
[CODE]. Reject reasons, in order:

1. text begins `/*NO QUERY CACHE*/` (and `/*FORCE QUERY CACHE*/` → cache unconditionally);
2. a table matches `cache_unsafe_memqcache_table_list` (regex deny-list);
3. `SELECT INTO`, `FOR UPDATE/SHARE`;
4. `pool_has_non_immutable_function_call()`: a `FuncCall` not immutable per catalog, or
   returning `timestamptz`/`timetz`; a `TypeCast` to `timestamptz`/`timetz` (`isSystemType`,
   `src/rewrite/pool_timestamp.c:299-315`, exact type name match); any `SQLValueFunction`
   (`CURRENT_TIME`, `CURRENT_USER`, ...) (`pool_select_walker.c:1121-1196`);
5. temp table; system catalog; `TABLESAMPLE`; row-security table;
6. view or unlogged table, unless listed in `cache_safe_memqcache_table_list`;
7. a CTE that is `InsertStmt`, `DeleteStmt` or `UpdateStmt` (`:1113-1130`).

So the cache is a **deny-list of features over a parse tree plus the catalog**.

**Invalidation** [CODE]: on CommandComplete, `pool_extract_table_oids()`
(`pool_memqcache.c:1179-1375`) extracts target tables of INSERT/UPDATE/DELETE/MERGE,
ALTER TABLE, COPY FROM, DROP, TRUNCATE, and of INSERT/UPDATE/DELETE CTEs inside a SELECT
(`pool_extract_withclause_oids`, `:1379-1420`); their cache entries are deleted. `ALTER
DATABASE`, `ALTER ROLE`, `ALTER TABLE`, `REVOKE` clear the whole cache (docs,
`memcache.sgml`; REVOKE path at `CommandComplete.c:536-550`). `SET ROLE` / `SET SESSION
AUTHORIZATION` (only `VAR_SET_VALUE` form) disable the cache for the rest of the session
(`CommandComplete.c:526-535`).

**What a cache miss costs: wrong or stale data to another session**, never a protocol
error. Holes I can show from the code [INFERRED, not run]:

- **Writes Pgpool does not see** do not invalidate: writes inside functions
  (`SELECT do_insert()`), triggers, FK cascades, writes from clients not going through
  Pgpool. I found no handling [NOT FOUND]; the only remedy offered is `memqcache_expire`
  (default 0, "cache remains valid until the table is updated", `memcache.sgml`).
- **`WITH m AS (MERGE INTO t ... RETURNING *) SELECT * FROM m`**: the grammar accepts MERGE
  in a CTE (`gram.y:13384` `common_table_expr ... '(' PreparableStmt ')'`, and
  `PreparableStmt` includes `MergeStmt`, `gram.y:12454-12459`), but rule 7 above checks only
  Insert/Delete/Update, and `pool_extract_withclause_oids` extracts only those three. So the
  statement is judged cacheable and does not invalidate `t`; a repeat of the same text from
  the same user would be answered from the cache **without executing the MERGE**. (Routing is
  still correct: `send_to_where` sends any non-SELECT CTE to the primary.) If confirmed this
  is a real bug of exactly mini-gateway's kind — a list that is one statement type short.
- **`SELECT 'now'::timestamp`, `'today'::date`, `'[now,)'::tstzrange`**: only casts to the
  exact names `timestamptz`/`timetz` are rejected, so these are cached and frozen.
  (mini-gateway holes 8 and 11.)
- **Attribute-notation calls** `SELECT t.f FROM t` (= `f(t)`): the raw tree has a
  `ColumnRef`, not a `FuncCall`, and both walkers only inspect `FuncCall`, so a volatile `f`
  is neither routed to the primary nor kept out of the cache. (mini-gateway hole 5.) A
  standby would reject a write `f`; the cache would freeze a volatile one.
- **Session state not in the key**: the key is user + text + database. After
  `SET search_path = other` (or `set_config('search_path',...)`, or
  `set_config('role','x',false)`, or `RESET ROLE`), `SELECT * FROM t` is answered from an
  entry created under a different `search_path`/role. Only the literal `SET ROLE` /
  `SET SESSION AUTHORIZATION` statements are handled. Docs mention neither `search_path`
  nor `set_config` (grep of `doc/src/sgml`: no "search_path" hits).
- **Overload / shadowing** of an immutable function name (3b above).

#### 3d. Fail open or closed on text it does not understand

- **Parse error → closed** [CODE] (`pool_proto_modules.c:346-381`):

```c
/*
 * Unable to parse the query. Probably syntax error or the query
 * is too new and our parser cannot understand. Treat as if it
 * were an DELETE command. ...
 */
...
parse_tree_list = get_dummy_write_query_tree();
query_context->is_parse_error = true;
```

  The original text is still forwarded (to the primary), and `is_parse_error` blocks caching
  (`:605-607`). Empty query / `;` gets a dummy *read* tree instead (`:348-358`).
- **Unknown statement type → closed**: `send_to_where` default `POOL_PRIMARY`.
- **Unknown function** (not in `pg_proc`): count is 0 → "not volatile" → load-balanced; the
  standby then raises "function does not exist", same as the primary would. For the cache:
  not immutable → not cached. Closed.
- **Function called in a way the walker does not see** (attribute notation, inside a view,
  inside another function): **open** for both routing and cache; for routing the standby's
  read-only enforcement catches actual writes.

### 4. Documented limitations (the project's own words)

From `doc/src/sgml/restrictions.sgml` ("Restrictions"), markup stripped:

> **Load Balancing.** Multi-statement queries (multiple SQL commands on single line) are
> always sent to primary node (in streaming replication mode) or main node (in other
> modes). Usually Pgpool-II dispatch query to appropriate node, but it's not applied to
> multi-statement queries.

> **Temporary tables.** ... if the temporary table name is used as a literal in SELECT,
> there's no way to detect it, and the SELECT will be load balanced. That will trigger a
> "not found the table" error or will find another table having same name. To avoid the
> problem, use SQL comment.

> Tables created by CREATE TEMP TABLE will be deleted at the end of the session by
> specifying DISCARD ALL in reset_query_list ...

> **Functions, etc.** There is no guarantee that any data provided using a
> context-dependent mechanism (e.g. random number, transaction ID, OID, SERIAL, sequence
> etc,), will be replicated correctly on multiple backends. [native replication mode]

From `doc/src/sgml/loadbalance.sgml`:

> If this parameter and write_function_list is empty string, function's volatile property
> will be checked. If the property is volatile, the function is regarded as a writing
> function. This is convenient and recommended way.

> You could suppress load balancing by inserting arbitrary comments just in front of the
> SELECT query: `/*REPLICATION*/ SELECT ...`

From `doc/src/sgml/memcache.sgml`:

> `/*FORCE QUERY CACHE*/SELECT now();` will return the same timestamp until pgpool
> restarts, once the query is cached.

> If multiple functions having the same name are registered and one of them returns
> TIMESTAMP WITH TIMEZONE or TIME WITH TIMEZONE, Pgpool-II thinks that the query cannot be
> cached even if it uses a version of function which does not return [them].

From `doc/src/sgml/misc-config.sgml` (relcache): "If the table is modified using ALTER TABLE
or some other means, the relcache becomes inconsistent." and the shared-relcache sentence
quoted in 3b.

I found **no documented restriction about functions with side effects hidden inside views**
in Pgpool's docs [NOT FOUND]; the routing relies on the standby rejecting the write.

### 5. Language, license, maturity, dependency weight

- **Language:** C. ~25k lines in `src/protocol` alone; parser ~42k generated-from lines
  of grammar (two variants) plus node support code.
- **License:** permissive, ISC/PostgreSQL-style ("Permission to use, copy, modify, and
  distribute this software ... without fee", `COPYING`, "Copyright (c) 2003-2021 PgPool
  Global Development Group").
- **Maturity/activity:** first copyright year 2003 (`COPYING`); release notes back to 3.1 in
  `doc/src/sgml/release-*.sgml`; latest tag `V4_7_3` (2026-09-25); HEAD commit 2026-11-24;
  PG 19 parser imported 2026-11-06.
- **Reuse from Go:** not realistic. The parser is welded to Pgpool's memory contexts, error
  handling (`ereport`/`PG_TRY`) and node types; the routing logic depends on
  `POOL_CONNECTION_POOL` and its relcache. A Go proxy wanting "PostgreSQL's real grammar"
  would use `pg_query_go` (libpg_query, cgo) — option (c1) — rather than Pgpool's copy. What
  is reusable is the **design**, not the code.

### Probe table

Mode assumed: streaming replication (`backend_clustering_mode = streaming_replication`),
defaults (function lists empty, `check_temp_table = catalog`, `allow_sql_comments = off`,
`memory_cache_enabled` on where the cache column applies). "Session" = Pgpool's own session
pinning makes cross-client leakage n/a unless noted.

| Probe | Routing (where it is sent) | Query cache | Session-state handling | Citation |
| --- | --- | --- | --- | --- |
| P1 `SET search_path = x` | All nodes (`POOL_BOTH`) | Not a SELECT → not cached; but **later SELECTs keep using cache entries keyed without search_path** | Lives until session end, then `DISCARD ALL` | `pool_query_context.c:1055-1136, 2252-2266`; `pool_memqcache.c:567` |
| P2 `SELECT set_config('search_path','h',false)` | Primary only (volatile function) → **standby connection keeps old search_path** | Not cached (volatile) | Reset at session end | `pool_select_walker.c:368-452, 1212`; `pool_query_context.c:2364-2373` |
| P3 `SELECT f()`, f does SET / CREATE TEMP | Primary if f is volatile (default for plpgsql functions); if marked STABLE/IMMUTABLE wrongly → standby, where CREATE TEMP fails (read-only) and SET applies only there | Cached only if f is (or a same-named overload anywhere is) immutable | Reset at session end; standby/primary divergence possible | same as P2; `is_immutable_function` `:1200` |
| P4 lexical tricks (`E'ok'\n'\' ...`, comments, dollar quotes) | Real PostgreSQL scanner, so it sees what PG 19 sees; a leading comment sends to primary (`allow_sql_comments = off`) | Leading-comment skipper in `pool_is_likely_select` is hand-written, but only decides *whether to probe* the cache | n/a | `scan.l`; `pool_process_query.c:1144-1150`; `pool_memqcache.c:897` — caveat: version/`standard_conforming_strings` skew (§1) |
| P5 `PREPARE p AS ...` | As its inner query; EXECUTE/DEALLOCATE follow PREPARE | Inner SELECT may be cached on EXECUTE (not checked) | Reset at end | `pool_query_context.c:1146-1180, 1201` |
| P5 `LISTEN c`, `DECLARE c CURSOR WITH HOLD` | Primary (default branch) | n/a | Reset by `DISCARD ALL` | `pool_query_context.c:1187-1193` |
| P5 `CREATE TEMP TABLE t(...)` | Primary; temp relcache discarded; later SELECTs on t → primary | SELECTs on t not cached | Reset at end | `pool_proto_modules.c:4770-4781`; `pool_select_walker.c:700` |
| P5 `SELECT pg_advisory_lock(1)` | Primary (volatile) | Not cached | `DISCARD ALL` releases session locks | `function_call_walker` |
| P6 `SELECT 1; SET search_path = x` | **Whole message to primary only** → the SET never reaches the standby | Not cached (multi-statement) | Divergence between nodes; reset at end | `pool_query_context.c:510-529`; `pool_proto_modules.c:592`; restrictions.sgml |
| P7 `SELECT nextval('s')` | Primary (volatile) | Not cached | — | `function_call_walker` |
| P7 `WITH d AS (DELETE ... RETURNING *) SELECT * FROM d` | Primary (non-SELECT CTE) | Not cached; invalidates t | — | `pool_query_context.c:962-976`; `pool_memqcache.c:1113-1130` |
| P7 same with **MERGE** in the CTE | Primary | **Judged cacheable; does not invalidate t** [INFERRED] | — | `pool_memqcache.c:1113-1130, 1379-1420`; `gram.y:12454` |
| P7 `SELECT * FROM t FOR UPDATE` | Primary | Not cached | — | `pool_has_insertinto_or_locking_clause` |
| P7 `SELECT now()` | Load-balanced (now() is stable, not volatile) | Not cached (not immutable, returns timestamptz) | — | `pool_select_walker.c:1121-1196` |
| P7 `SELECT * FROM v`, v over a volatile function | Load-balanced (no FuncCall in text); if the function writes, **the standby rejects it** | Not cached (`pool_has_view`, default) | — | `pool_memqcache.c:1098-1110` |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | Primary (no ExplainStmt branch → default) | Not a SelectStmt → not cached | — | `pool_query_context.c:938-1191` |

## Verdict for us: the 11 hole classes

Holes from the scope assessment, section 3. "Routing" = would Pgpool's read/write split
handle it; "Cache" = would its query cache refuse to share it. Session-state holes (D3) are
mostly n/a because Pgpool pins sessions and resets with `DISCARD ALL`.

| # | Hole | Pgpool's machinery | Caught? |
| --- | --- | --- | --- |
| 1 | `SET local.x` (custom GUC) | Any `VariableSetStmt` → all nodes; session pinned, reset at end | Yes (by pin + reset, not by reading) |
| 2 | `UPDATE pg_settings` | `UpdateStmt` → primary; reset at end. Standby connection does not get the change [INFERRED] | Leak: n/a. Node divergence: no |
| 3 | `SET LOCAL standard_conforming_strings` | SET goes to all nodes, but Pgpool's own scanner does not track this setting (§1) | Session: yes. Its own lexing: no (same class of skew) |
| 4 | schema-qualified calls | Real parser gives `schema.func`; catalog lookup with `nspname =` | Yes |
| 5 | attribute-notation calls (`t.f`) | Walkers look only at `FuncCall` | **No** (routing backstopped by standby; cache open) |
| 6 | `search_path` naming `pg_temp` in a string | Session pinned + reset; but cache key ignores `search_path` | Leak: yes. Cache: **no** |
| 7 | quoted `EXPLAIN` option | Real parser normalises; EXPLAIN goes to primary anyway | Yes |
| 8 | `'now'::timestamp` | Only casts to `timestamptz`/`timetz` blocked | **No** (cache) |
| 9 | operator splitting | Real scanner | Yes |
| 10 | string continuation across a newline | Real scanner (`scan.l`, PG 19) | Yes, while proxy and server versions/settings match |
| 11 | temporal range / array types | Same exact-type-name check as 8 | **No** (cache) |

So the vendored parser closes the **lexical** holes (4, 7, 9, 10) — exactly as the
assessment predicts for (c1) — and session pinning plus `DISCARD ALL` makes the session
holes (1, 2, 3, 6) harmless *across clients*. The **grammar/meaning** holes for sharing
(5, 8, 11, 6-as-cache-key) remain open in Pgpool's query cache, and reading the code turned
up two more of the same kind that Pgpool itself has not closed: MERGE in a CTE (cache, no
invalidation) and overload/shadowing in the catalog lookup. Residual holes the project
itself admits: temp-table names as literals, writes not seen by Pgpool (only `memqcache_expire`
helps), stale relcache after catalog changes, multi-statement queries not analysed.

## Claims of completeness

- Pgpool **does not claim** that it decides safety completely from SQL text. Its docs
  present the function lists and volatility check as heuristics, and list the escape
  hatches (`/*REPLICATION*/`, `primary_routing_query_pattern_list`, `/*NO QUERY CACHE*/`,
  `cache_unsafe_memqcache_table_list`, `memqcache_expire`, `relcache_expire`). The closest to a
  claim is memcache.sgml's "When the underlying table gets updated, corresponding cache
  entries are deleted", which does not survive the code for writes Pgpool does not parse
  (functions, triggers, MERGE in a CTE, other clients) [INFERRED].
- The routing design is honest because **its safety does not depend on the reading**: a
  wrongly balanced write lands on a read-only standby, which refuses it. The query cache has
  no such backstop, and that is where the holes are.

## Mapping to options (a) (b) (c1) (c2)

- **Session state (our D3): option (b), in its strict form.** Pgpool pins the client to the
  connection for the whole session and runs `ABORT; DISCARD ALL` before the connection is
  reused, dropping the connection if that fails. It never tries to read session changes out
  of SQL for safety; the parse is used only to keep several nodes in step (SET to all), and
  that part has holes (P2, P6). Note it is session pooling, so it never faces mini-gateway's
  per-transaction lending; it does not demonstrate that per-lease reset is cheap.
- **Read-only (our D4) / routing: (c1) + (c2).** A real vendored parser gives an allow-list
  of statement types, the catalog gives function volatility, and **the standby's
  server-enforced read-only mode is the real backstop** — the same idea as (c2).
- **Shareability (our D5) / query cache: (c1) plus a deny-list over the parse tree and the
  catalog — i.e. (a) with a better lexer.** This is where it carries the same classes of
  holes the assessment predicts for (a), despite the real parser.

## Mechanisms not in the assessment

1. **Asking the catalog, with a cache** (the "catalog layer"). Pgpool sends
   `SELECT count(*) FROM pg_proc ... WHERE proname = '<f>' AND provolatile = 'v'` on the
   client's own connection, caches the answer per `(database, query text)`, optionally shares
   it across processes, and never invalidates it except by time. For a Go proxy:
   one extra round trip per new function name, a map, and a TTL. Weaknesses seen in the code:
   lookup by name ignores overloads and `search_path`; stale after `ALTER FUNCTION`; and it only
   sees functions spelled as `FuncCall` in the text, not inside views or other functions. A
   stricter Go variant would ask about the *resolved* function (e.g. `'f(int)'::regprocedure`
   under the session's search_path) — but it still cannot see through views and nested calls,
   so it would remain an optimisation, not a guarantee.
2. **Reacting to what the server reports it did, not to what the text says.** Pgpool updates
   its state from the parse tree of the statement *at CommandComplete time*
   (`CommandComplete.c`: temp-table tracking, SET ROLE disables cache, REVOKE clears cache).
   A Go proxy can go further without parsing: CommandComplete **tags** (`SET`, `PREPARE`,
   `LISTEN`, `DECLARE CURSOR`, `CREATE TABLE`, `DISCARD ALL`, ...) arrive once per statement,
   including each statement of a multi-statement simple Query, and reflect what the server
   actually executed, so they cannot be fooled by lexical tricks. Any tag not on a short
   "known clean" list (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `BEGIN`, `COMMIT`,
   `ROLLBACK`, `SHOW`, ...) marks the lease dirty → reset. Plus **ParameterStatus** messages,
   which the server sends when a reported setting changes (which settings are reported depends
   on the server version — from memory, not re-checked). This does not catch session changes
   inside a function called by a SELECT (`set_config`, `pg_advisory_lock`), so it complements
   the substring taint check of option (b); it does not replace it.
3. **Server-enforced read-only on a different connection class.** Pgpool's routing is safe
   because standbys physically cannot write. mini-gateway has one ordinary Postgres, but the
   equivalent is a pool of connections opened with `default_transaction_read_only = on`
   (startup parameter, or `ALTER ROLE ... SET` for a dedicated reader role) used for shared /
   coalesced reads. This is (c2) without the `BEGIN READ ONLY; ...; COMMIT` wrapping and reply
   stripping. As the assessment says for (c2), it does not catch `now()`/`random()`.
   (Note `SET default_transaction_read_only = off` from the client would undo it; the
   connection class would need the reset/allow-list anyway.)
4. **Prefix-only text hints.** Pgpool's hints (`/*FORCE QUERY CACHE*/`, `/*NO QUERY CACHE*/`)
   are recognised only as an exact case-insensitive prefix of the message (`strncasecmp`,
   `pool_memqcache.c:986-1000`), which needs no lexer and cannot be confused by quoting —
   a concrete pattern for mini-gateway's `hinted` mode. Likewise `allow_sql_comments = off`
   makes *any* leading comment mean "not load-balanceable" — a text-shape allow-list on top
   of the parse, close to option (b)'s small recogniser.
5. **Invalidation by table OID from parsed writes** (query cache). Not needed for an
   in-flight coalescer (no stored results), but if mini-gateway ever keeps results longer
   than one execution, Pgpool shows the method and its blind spots (writes inside functions,
   triggers, MERGE CTEs, other clients).

**Better candidate project:** none met in this cluster. For the parser itself, the
assessment's (c1) candidate `pg_query_go`/libpg_query is the practical route for Go; Pgpool's
copy is not separable.

## Could not verify

- Any Pgpool code handling `standard_conforming_strings = off` on pre-PG-19 backends: not
  found in `src/parser` (§1). Whether this causes a real misparse against PG ≤ 18 was not tested.
- Any Pgpool handling of session changes made through functions (`set_config`) for the
  standby connections: not found.
- Any invalidation of the query cache for writes made by functions, triggers or cascades:
  not found; docs offer only `memqcache_expire`.
- Any mention of `search_path` in the docs or cache key: not found.
- The MERGE-in-CTE cache behaviour, the attribute-notation miss, the overload/shadowing
  miss, and `'now'::timestamp` being cached are read from code, **not executed**.
- What the unescaped function name in `VOLATILE_FUNCTION_QUERY` does with a name containing
  a quote: not tested.
- Whether EXPLAIN is load-balanced in streaming mode via some path I did not read: I found
  no `ExplainStmt` handling in `send_to_where` and concluded it goes to the primary.
- `DISCARD ALL` semantics (unlisten, close cursors, release advisory locks, drop temp tables,
  deallocate) are stated from memory of PostgreSQL docs, not re-checked in this run.
