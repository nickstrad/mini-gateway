# Odyssey (Yandex) and pgagroal

Canvas 03 for mini-gateway chunk 5. Research only. Every line number refers to the clones
listed under Sources, at the commits recorded there. Evidence tags: **VERIFIED IN CODE**
(read in the clone; nothing was built or run), **DOCS ONLY**, **COULD NOT FIND**.

## Sources

| Project | URL | Commit SHA | Commit date | Local / web |
| --- | --- | --- | --- | --- |
| Odyssey | https://github.com/yandex/odyssey | `21d01bd734aa24944d92a76bc57db041b4baa5c8` | 2026-11-24 15:08 +0500 | LOCAL SOURCE (`repos/odyssey`, depth-1 clone); history of `sources/sql/full/gram.y` and release list via `gh api` (WEB READ) |
| pgagroal | https://github.com/agroal/pgagroal | `0f53af7e1b3011967befa01ca7a3ff5587de4bf4` | 2026-11-05 16:58 -0500 | LOCAL SOURCE (`repos/pgagroal`, depth-1 clone); release list via `gh api` |
| PostgreSQL (to compare Odyssey's vendored grammar) | `repos/postgres` (shared clone) | `4483697be0e0d22b9b4fd847e8bf2d998c56a741` | 2026-11-25 17:31 +0100 | LOCAL SOURCE (read only) |

Odyssey: newest release **v1.5.3, 2026-11-24**; v1.6.0-rc1 2026-10-05; `VERSION` file at the
commit read says `1.7.0-rc1`. Repository created 2018-05-28. BSD-3-Clause. (gh api)

pgagroal: newest release **2.1.0, 2026-04-29**; 2.0.2 2026-02-25; first tag 0.1.0 (commit dated
2019-09-17). Repository created 2019-08-28. BSD-3-Clause. (gh api, `git ls-remote --tags`)

## Summary rows

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| Odyssey | A **vendored copy of PostgreSQL's `gram.y` + `scan.l`** (≈23k lines, `query_parsing_mode full`, used only by read-only routing) and a 530-line hand bison grammar for 8 statement kinds (`minimal`, the default). Output is a 13-bit flag word for the **first** statement only; multi-statement = parse error. | **Reset on every release**: `od_reset` after each transaction — default "smart discard" = `DISCARD ALL` minus `DEALLOCATE ALL` (or plain `DISCARD ALL`, or a custom string); drop the connection instead when unsynchronised, in COPY, in a pipeline, or on reset failure. Plus **track/replay** of 33 named parameters learned from startup and `ParameterStatus`. Plus **pin on server signal**: `CommandComplete` tag `LISTEN` (opt-in) and a `ttt.owns_session_objs` `ParameterStatus` from a server extension. Pools keyed by (user, database). | Read-only routing to a standby (`auto_route_ro_on_standby`, opt-in). No caching or coalescing. | **Allow-list** over the parse tree: `SELECT` (no INTO / top-level locking / non-SELECT CTE) whose function calls are all on a 150-name list (+ user list); `EXPLAIN` of that; `COPY … TO STDOUT`; `SHOW`. | Session: SQL `PREPARE` survives smart discard and is reachable by the next client of the route; `pool_discard no` disables the reset. Routing: a write sent to a standby **errors**; the server is the backstop. | Session reset: closed (failure → close connection). Routing: closed on parse error, multi-statement, unknown statement/function, too long; **open inside the walker** for unlisted node types and for subtrees the grammar shims drop (`makeJsonKeyValue`, `makeJsonIsPredicate` return `NULL`). |
| pgagroal | **Nothing.** Message kinds only (`'Q'`/`'E'` counted for metrics; `'P'` with a non-empty name sets a "deallocate" flag); the `ReadyForQuery` status byte ends a transaction. | Session/performance pipelines: `server_reset_query` (default `DISCARD ALL`) on return. **Transaction pipeline: no reset by default**; optional `DEALLOCATE ALL` if a named protocol `Parse` was seen (`track_prepared_statements`, default off); optional `server_reset_query_always` (default off). ROLLBACK if the client leaves inside a transaction. No parameter tracking ("assumed that all clients using the same user name and database pair share the same startup parameters"). Pools keyed by (user, database). | None. | None. | Every session feature (`SET`, temp tables, `LISTEN`, `WITH HOLD`, SQL `PREPARE`, advisory locks) leaks to the next client of the backend, by documented design. | Open by default in transaction mode (documented). With `server_reset_query_always`: closed (failure → kill connection) unless `…_on_failure = ignore`. |

## Odyssey

Language C (plus its own coroutine library `machinarium`); BSD-3-Clause; ~40k lines in
`sources/*.c`. Pools are keyed by **route = (user, database, physical_rep, logical_rep)**
(`sources/include/route_id.h:14-21`). VERIFIED IN CODE.

### 1. Does it read SQL?

**Yes, with a near-verbatim copy of PostgreSQL's own grammar and scanner, plus a small
hand-written bison grammar.** VERIFIED IN CODE. This is new: the full grammar was imported
on 2026-09-30 (commit `e9fd665479`, "Import full SQL grammar from PG sources (#1610)", message
"For future usage"; `gh api repos/yandex/odyssey/commits?path=sources/sql/full/gram.y` lists
only that commit and `7d3e7ca84d` 2026-11-03). Three modes, `query_parsing_mode
disabled|minimal|full`, **default `minimal`** (`docs/configuration/query-parsing.md`; dispatch
in `sources/query.c:985-1008`).

- **`full`**: `sources/sql/full/` = `gram.y` 20,147 lines, `scan.l` 1,405, plus `parser.c`
  (PG's `base_yylex` filter), `kwlookup.c`, `scansup.c`, small node/list/makefuncs shims;
  about 23,000 lines in all. Built with bison/flex as part of Odyssey (`sources/CMakeLists.txt:293-303`,
  `add_definitions(-DFRONTEND)` at line 2). Header of `scan.l`: "lexical scanner for PostgreSQL
  (adapted for Odyssey) … Portions Copyright (c) 1996-2026, PostgreSQL Global Development Group".
  I diffed the rules section (`%%`…`%%`) of Odyssey's `gram.y` against PostgreSQL master in
  `repos/postgres`: **22 differing lines**, all of them Odyssey additions that record the
  location of a prepared-statement name (`name_location`, `ColId` actions). So the grammar
  layer *is* PostgreSQL's, as of a 2026 master snapshot. `scan.l` differs in 170 lines,
  mostly includes and error plumbing. **Kept in step by manual re-import**; there is no
  script or submodule pinning it to a PG release (COULD NOT FIND one in `scripts/`, `cmake/`).
- **`minimal`**: `sources/sql/minimal/` — its own flex scanner (`scan.l`, 303 lines) and
  bison grammar (`parse.y`, 529 lines) that recognises only `PREPARE`, `EXECUTE`, `SET`,
  `SHOW`, `BEGIN`, `DEALLOCATE`, `DISCARD`, `UNLISTEN` (`parse.y:204-446`). Anything else is
  a parse failure and is "treated as read-write" (docs, query-parsing.md).
- The config/console lexers (`sources/cfg/scan.l`, `sources/console/scan.l`) are separate
  and never applied to client SQL (they are compiled as `cfg_parser`/console only;
  `sources/CMakeLists.txt:251-263`). VERIFIED IN CODE.

What the full parse is turned into — a 13-bit flag word, nothing more
(`sources/include/sql/minimal/ast.h:94-106`): `PARSE_ERROR, IS_SELECT, IS_SHOW, IS_SET,
IS_BEGIN, IS_DISCARD_ALL, IS_UNLISTEN_ALL, IS_DEALLOCATE_ALL, HAS_DEALLOCATE_NAME, TOO_LONG,
IS_EMPTY, IS_EXECUTE, HAS_RESERVED_NAME`. Only the first statement is looked at, and more
than one statement is an error:

```c
// sources/query.c:876-879 (parse_full)
	if (list_length(tree) > 1) {
		/* do not support multi statements now */
		goto error;
	}
```

**Who consumes the flags** (grep of all uses, VERIFIED IN CODE):
1. *Read-only routing* (`auto_route_ro_on_standby`, the only consumer of the full parser —
   `sources/query.c:25-43` downgrades every other client to `minimal`) — section 3.
2. *Virtual commands* answered by Odyssey itself: `SET/SHOW odyssey.*` pseudo-settings,
   empty query, a deferred `BEGIN`, SQL `EXECUTE`/`DEALLOCATE` of protocol-prepared
   statements (`sources/virtual.c:883-956`). A plain `SET search_path` is **not**
   virtual: `process_vset` returns `OD_OK` (forward) for every name it does not own
   (`virtual.c:439-520`).
3. *Bookkeeping after success*: `DISCARD ALL` clears Odyssey's prepared-statement maps,
   `UNLISTEN *` clears a LISTEN pin (`sources/xplan.c:1764-1801`, applied only if the server
   returned no error, `xplan.c:1935-1939`).
4. *Reserved names*: `PREPARE/EXECUTE/DEALLOCATE odyssey…` rejected with `42939`
   (`sources/analyze.c:99-112`).

**None of these is a session-state safety decision.** No flag means "this statement
changes session state"; session safety does not depend on the parser at all (next section).

### 2. Session-state safety

**Mechanism: reset on every release (transaction mode), plus track-and-replay of reported
parameters, plus pin-on-signal.** VERIFIED IN CODE.

**When.** In transaction (and statement) mode, after every reply the frontend checks
whether the server may be released; the test is the protocol's transaction status
(`ReadyForQuery` byte, `sources/backend.c:147-172` sets `is_transaction` from `'I'/'T'/'E'`)
and every release goes through `od_reset`:

```c
// sources/frontend.c:1739-1742, 2264-2292
static inline bool od_frontend_should_detach_transaction(od_server_t *server)
{
	return !server->is_transaction;
}
...
	if (!od_server_synchronized(server)) return OD_OK;
	if (client->backend_pin)                return OD_OK;
	if (!od_frontend_should_detach(route, server)) return OD_OK;
	/* cleanup server */
	int rc = od_reset(server);
```

The same `od_reset` runs when a client disconnects (`frontend.c:3284`), which is the only
release point in session mode.

**What `od_reset` does** (`sources/reset.c:25-260`), in order:
1. **Drop instead of reset** when the connection is in a state a reset cannot fix: OOM
   flag, a broken cached plan, left in COPY, a half-read message, left in an
   extended-protocol pipeline (cancel first), in a transaction with `pool_rollback no`,
   or not synchronised (requests sent ≠ replies received) after cancel + wait for
   ReadyForQuery (`reset.c:30-170`). "Drop" = close the server connection.
2. `ROLLBACK` if still in a transaction and `pool_rollback` (default yes) (`reset.c:176-188`).
3. `DISCARD ALL` if `pool_discard` (`reset.c:191-199`), **or** the "smart discard" string
   if `pool_smart_discard` and no custom query (`reset.c:201-211`), **or**
   `pool_discard_query` verbatim if set (`reset.c:212-220`). Each is sent with the comment
   prefix `/* odyssey reset */`, with `pool_reset_timeout_ms` (default 1000 ms,
   `pool.c:30`). Any failure → close (`reset.c:255-259`).
4. Evict over-limit reserved prepared statements with protocol `Close` (`reset.c:222-250`).
5. Close if any unread bytes remain (`reset.c:252-257`).

The smart discard string is exactly `DISCARD ALL` minus `DEALLOCATE ALL`:

```c
// sources/reset.c:203-205
		char query_discard[] = RESET_COMMENT
			"SET SESSION AUTHORIZATION DEFAULT;RESET ALL;CLOSE ALL;UNLISTEN *;SELECT pg_advisory_unlock_all();DISCARD PLANS;DISCARD SEQUENCES;DISCARD TEMP;";
```

**What makes it smart, and what it deliberately does not reset.** It keeps the server's
prepared statements so that Odyssey's own `odyssey_pstmt_N` statements (section "prepared
statements" below) survive across clients. Config validation enforces the pairing
(`sources/rules.c:1859-1904`): with `pool_reserve_prepared_statement` on, `pool_discard`
is silently replaced by smart discard; smart discard without reservation is an error; a
custom `pool_discard_query` containing `DEALLOCATE ALL` is an error. Reservation is forced
off in session mode (`rules.c:1859-1866`).

**Defaults** (`sources/pool.c:23-33`): `discard=1, smart_discard=0, cancel=1, rollback=1,
reserve_prepared_statement=1, reset_timeout_ms=1000`. Unset config keys keep these
(`COPY_BOOL` copies only `seen.is_set`, `sources/cfg/convert.c:61-66, 872-878`). So **a
transaction-mode route with no reset settings sends the smart-discard string after every
transaction**. (The docs table says `pool_reserve_prepared_statement … yes (0)`,
`docs/configuration/rules.md:64` — self-contradictory; the code default is 1.) Many shipped
examples set `pool_discard no` (e.g. `config-examples/odyssey-aq.conf:74`), which turns
the reset off and leaves only document-and-shrug.

The consequence that matters for mini-gateway: under smart discard, **a SQL-level
`PREPARE p` made by one client survives on the server connection and can be `EXECUTE`d by
the next client of the same route**. `process_vexec` only rewrites names that are in the
client's own protocol-statement map and forwards every other `EXECUTE` unchanged ("Keep
native SQL PREPARE/EXECUTE behavior for other names", `sources/virtual.c:661-665`). Routes
are per user+database, so this crosses clients of the same role, not roles.
VERIFIED IN CODE (by reading; not executed).

**Track and replay.** Each client and server carries `kiwi_vars`, a fixed table of 33
parameters (`sources/include/kiwi/var.h:16-51`: client_encoding, DateStyle, TimeZone,
standard_conforming_strings, application_name, search_path, statement_timeout, work_mem,
lock_timeout, default_transaction_*, role, … and a few fork-specific names). Values come
from the startup packet and from server `ParameterStatus` messages, which update the
client and the server copy together (`sources/backend.c:1166-1210`, `kiwi_vars_update_both`).
On attach, `od_deploy` compares client vs server and sends one `SET …;` string for the
differences, `search_path` last and via `pg_catalog.set_config('search_path', …)`
(`sources/deploy.c:36-120`, `sources/kiwi/var.c:173-223`); controlled by
`maintain_params`, default on (`rules.c:547`). Only parameters the server *reports* can be
learned after startup: on vanilla PostgreSQL a `SET work_mem` produces no
`ParameterStatus`, so it is not replayed — and the reset removes it. That is the safe
direction.

**Pin on signal (two protocol/server signals, no SQL text).**
- `pool_pin_on_listen` (default no, "Experimental"): when the server's `CommandComplete`
  tag is exactly `LISTEN`, the client is pinned to that server until an `UNLISTEN *`
  succeeds (`sources/stream.c:154-167, 300-310`; unpin `xplan.c:1783-1801`). A pinned
  client is never detached (`frontend.c:2281`).
- `ttt.owns_session_objs`: if the server sends a `ParameterStatus` with that name (from a
  server-side extension, "ttt"), Odyssey pins the client while the value is `on`:

```c
// sources/backend.c:1191-1200
	/* ttt reports temp-rel ownership via ParameterStatus */
	if (client != NULL &&
	    name_len == sizeof(TTT_SESSION_OWNS_TEMP_RELS_STR) &&
	    memcmp(name, TTT_SESSION_OWNS_TEMP_RELS_STR,
		   sizeof(TTT_SESSION_OWNS_TEMP_RELS_STR)) == 0) {
		client->backend_pin =
			value_len == 3 && memcmp(value, "on", 2) == 0;
```

  The extension itself is not in the repository; I could not find its source or docs
  (COULD NOT FIND). The mechanism on Odyssey's side is clear: the *server* tells the
  pooler, through a reported GUC, that the session now owns objects.
- `SET odyssey.pin_backend = on` lets a client pin itself (`virtual.c:490-495`).

**Prepared statements in transaction mode** (`pool_reserve_prepared_statement`): protocol
`Parse` is keyed by a hash of (query text, parameter types) in a global map
(`sources/pstmt.c:921-990`); the server sees generated names `odyssey_pstmt_<n>`
(`sources/include/pstmt.h:39`); a `Parse` is replayed on a server that lacks it before
`Bind`/`Execute` (`virtual.c:670-699` for SQL `EXECUTE`, the xplan path for protocol). The
query text is parsed once at `Parse` time and the flags cached (`pstmt.c:991`). This is
protocol-message tracking, not SQL reading, apart from SQL `EXECUTE name` which needs the
parser to find the name.

### 3. Read-vs-write and sharing decisions

**Only one: `auto_route_ro_on_standby`** (default off; requires `query_parsing_mode full`,
refused with session pooling: `rules.c:2159-2236`). No result cache, no coalescing.
VERIFIED IN CODE.

It is an **allow-list** over the parse tree. `IS_SELECT` is set only for
(`sources/query.c:802-852, 961-978`):
- `SelectStmt` with no `INTO`, no top-level locking clause, every CTE a `SELECT`, and no
  function call except names on a 150-entry built-in list (`query.c:223-386`) or the
  user's `query_parsing_standby_function_list`; a schema-qualified name counts only if the
  schema is `pg_catalog` (`query.c:411-427`);
- `EXPLAIN` of such a `SELECT` (with `ANALYZE` only if function-free);
- `COPY … TO STDOUT` with no file (`query.c:207-222`);
- `SHOW`.

Routing: no `IS_SELECT`, or `PARSE_ERROR`/`TOO_LONG` → primary (`ast.h:135-143`,
`frontend.c:938-965`). So **parse errors, multi-statement queries, unknown statements,
over-long queries and unknown functions fail closed (to the primary).**

What a miss costs **in Odyssey**: a statement wrongly marked read-only is sent to a
physical standby, which rejects writes; the client gets an error (`cannot execute … in a
read-only transaction` / during recovery). No wrong data, no cross-client leak. A
session change (`set_config`) on a standby is still removed by the reset.

Holes I found by reading (not executed; each costs an error on the standby, nothing worse):
- The function walker returns "no function" for any node type it does not list
  (`query.c:784-789`, `default: break; … return 0`) — fail open *inside* the allow-list.
- Some grammar helpers are stubs that **drop the subtree**: `makeJsonKeyValue` and
  `makeJsonIsPredicate` return `NULL` (`sources/sql/full/makefuncs.c:169-173`). So
  `SELECT json_object('k' : nextval('s'))` and `SELECT nextval('s') IS JSON` reach the
  walker with the call removed and are classified `IS_SELECT`.
- `COPY (SELECT nextval('s')) TO STDOUT` — `is_select_copy` does not look for functions.
- A locking clause in a subquery: `is_select_select` checks only the top level
  (`query.c:833`).
- `txid_current` is on the "safe" list (`query.c:379`) but assigns a transaction id, which
  a standby refuses.
- `SELECT * FROM v` where the view calls a volatile function: views are invisible to the
  text, as everywhere.

These show the same pattern as mini-gateway's eleven holes, **even with PostgreSQL's real
grammar**: the parse is exact, but the *list* on top of it (safe functions, node types to
walk, stubs) is hand-made and leaks. Odyssey can afford it because the server enforces
read-only on a standby: the list is a performance hint with a server-side backstop.

### 4. Documented limitations (own words)

- "transaction pooling does not preserve session state between transactions, so features
  like session variables or temporary tables are not supported." —
  `docs/features/pooling.md` ("Transaction pooling"). DOCS.
- "`minimal` — … All other statements, including plain `SELECT`, are not classified and
  are treated as read-write. `full` — … Queries which can not be parsed or can not be
  classified as read-only require the primary." — `docs/configuration/query-parsing.md`.
- "Multi-statement queries and other unparsed queries pass through." (for the reserved-name
  check and SQL `EXECUTE` rewrite) — `docs/configuration/rules.md:732`.
- `pool_pin_on_listen`: "*Experimental* … Enable pinning client connection to server,
  after LISTEN command execute." — `rules.md:745-753`.
- `pool_discard_query` doc: "a set of SQL statements that will be executed during a
  `DISCARD ALL` command, except for `DEALLOCATE ALL`" — `rules.md:502-518`.

### 5. Language, license, maturity, weight

C, BSD-3-Clause, public since 2018 (gh), very active (last commit the day before this read;
v1.5.3 released 2026-11-24). Reusing its parser from Go means cgo + bison/flex output of a
PostgreSQL-derived grammar plus Odyssey's shims — strictly heavier and less maintained than
`pg_query_go` (libpg_query), which does the same thing with an upstream-synced build. The
reset/replay design needs no code from Odyssey at all.

### Probe table (Odyssey, transaction pool, default reset = smart discard)

| Probe | What Odyssey does | Code path |
| --- | --- | --- |
| P1 `SET search_path = x` | Parsed as `IS_SET`, but not an `odyssey.*` name → forwarded. If the server reports `search_path` (master; not PG ≤ 17), the client's var is updated and replayed on its next attach; the smart discard's `RESET ALL` removes it before the connection is reused. | `virtual.c:439-520`; `backend.c:1203-1207`; `deploy.c:52-90`; `reset.c:201-211` |
| P2 `SELECT set_config('search_path','h',false)` | Not seen as a SET (function call → not `IS_SELECT`, no `IS_SET`). Forwarded. Same server-side outcome as P1: `ParameterStatus` updates client vars if reported; reset removes it. | same |
| P3 `SELECT f()` (f sets / creates temp) | Not seen. Reset removes GUCs (`RESET ALL`) and temp tables (`DISCARD TEMP`). With the "ttt" extension, temp ownership would pin instead. | `reset.c:203-205`; `backend.c:1191-1200` |
| P4 lexical tricks | For routing: the scanner is PostgreSQL's `scan.l`, so continuation/escapes/dollar quotes are lexed as the server does (on a server where `standard_conforming_strings` is on; master's scanner no longer supports off). For session safety: irrelevant, the reset does not read text. | `sources/sql/full/scan.l` |
| P5 `PREPARE`, `LISTEN`, temp table, `pg_advisory_lock`, `WITH HOLD` | All forwarded. Reset: `UNLISTEN *`, `DISCARD TEMP`, `pg_advisory_unlock_all()`, `CLOSE ALL`. **SQL `PREPARE` survives smart discard** (no `DEALLOCATE ALL`) and is reachable by the next client of the route. `LISTEN` pins only with `pool_pin_on_listen`. A `WITH HOLD` cursor declared outside a transaction ends the lease and is closed by `CLOSE ALL`. | `reset.c:201-211`; `virtual.c:661-665`; `stream.c:154-167` |
| P6 `SELECT 1; SET search_path = x` | Parser: more than one statement → `PARSE_ERROR` → not virtual, not read-only; forwarded to primary. Reset handles the SET. | `query.c:876-879` |
| P7 `nextval` | Not on safe list → primary. (Hidden in `json_object('k': nextval(..))` or `COPY (…) TO` → standby → error.) | `query.c:636-643`; `makefuncs.c:169-173`; `query.c:207-222` |
| P7 `WITH d AS (DELETE …) SELECT` | CTE not a `SELECT` → primary. | `query.c:838-850` |
| P7 `SELECT … FOR UPDATE` | Locking clause → primary (top level only). | `query.c:833` |
| P7 `SELECT now()` | `now` on safe list → standby. (No caching, so volatility is irrelevant here.) | `query.c:316` |
| P7 `SELECT * FROM v` (volatile view) | No function in text → standby; error if the view writes. | `query.c:462-463` |
| P7 `EXPLAIN ANALYZE DELETE` | Inner query not `SelectStmt` → primary. | `query.c:802-828` |

## pgagroal

Language C; BSD-3-Clause; ~48k lines in `src/libpgagroal`. One worker process per client;
backends live in a shared-memory slot array. A connection is requested with
`pgagroal_get_connection(username, database, …)` (`src/libpgagroal/pool.c:77`), so pools
are keyed by **(user, database)**. VERIFIED IN CODE.

### 1. Does it read SQL?

**No.** VERIFIED IN CODE. The transaction pipeline's client-message callback looks only at
the message kind, and at the statement *name* in a `Parse`:

```c
// src/libpgagroal/pipeline_transaction.c:208-228 (transaction_client_message)
   if (config->track_prepared_statements)
   {
      /* The P message tell us the prepared statement */
      if (kind == 'P')
      {
         char* ps = pgagroal_read_string(msg + 5);
         if (pgagroal_strcmp(ps, ""))
         {
            deallocate = true;
         }
      }
   }
   /* The Q and E message tell us the execute of the simple query and the prepared statement */
   if (kind == 'Q' || kind == 'E')
   {
      pgagroal_prometheus_query_count_add();
```

The session pipeline does the same counting (`pipeline_session.c:283`). `queries.c` holds
only pgagroal's own health-check queries (`pg_is_in_recovery`, replication lag;
`queries.c:36-60`). A grep for `'Q'` over `src/libpgagroal` finds only messages pgagroal
writes itself (`message.c:858, 925, 1069, 1540`; `server.c:890`; `security.c:5408`).

### 2. Session-state safety

**Session and performance pipelines: reset on release.** When a session ends, the
connection is returned with `transaction_mode = false` (`worker.c:229-236`), and
`pgagroal_return_connection` sends `server_reset_query` — default `DISCARD ALL`
(`configuration.c:205`) — and kills the connection if it fails, unless
`server_reset_query_behavior_on_failure = ignore` (`pool.c:571-596`). An empty string
disables the reset.

**Transaction pipeline: release on `ReadyForQuery` 'I', no reset by default.** The server
callback reads the status byte (`message.c:505-520`: `*(state->in_tx) = (tx_state != 'I')`).
When a transaction completes (`pipeline_transaction.c:423-426`), the worker:

```c
// src/libpgagroal/pipeline_transaction.c:435-475 (abridged)
            /* In transaction pooling the reset query runs only when
             * server_reset_query_always is enabled (PgBouncer parity). A
             * non-empty reset (e.g. DISCARD ALL) subsumes DEALLOCATE ALL. If it
             * fails the backend is discarded rather than returned dirty. */
            if (config->server_reset_query_always && config->server_reset_query[0] != '\0' && ...)
            {   if (pgagroal_write_reset_query(...)) { ... kill or ignore ... }
                else deallocate = false; }
            if (deallocate)
            {   pgagroal_write_deallocate_all(wi->server_ssl, wi->server_fd);
                deallocate = false; }
            pgagroal_return_connection(slot, wi->server_ssl, true)
```

Defaults: `track_prepared_statements = false`, `server_reset_query_always = false`
(`configuration.c:204-207`). So **by default nothing is reset between transactions**. If
the client disconnects inside a transaction, `ROLLBACK;` is sent
(`pipeline_transaction.c:170-178`). Startup parameters are not tracked or replayed: the
docs say they are assumed identical per (user, database). Configuration load warns that
`ignore` + `server_reset_query_always` "is UNSAFE … Session state (GUCs, temp tables, etc.)
may leak between clients" (`configuration.c:885-890`).

Observation (by reading, not run): `pgagroal_write_reset_query` and
`pgagroal_write_deallocate_all` read one network message and check only whether its kind
is `'E'` (`message.c:876-897, 941-964`); they do not wait for `ReadyForQuery`. For a
multi-statement reset string an error in a later statement may not be seen. I did not test
this.

### 3. Read-vs-write and sharing decisions

**None.** No read/write split, no cache, no coalescing. (Failover to a standby is
connection-level, `FAILOVER.md`.) n/a.

### 4. Documented limitations (own words, `doc/PIPELINES.md`)

- "there are some session based features of PostgreSQL that can't be supported in this
  pipeline. * `SET` / `RESET` * `LISTEN` / `NOTIFY` * `WITH HOLD CURSOR` * `PREPARE` /
  `DEALLOCATE`" (lines 63-69).
- "It is assumed that all clients using the same user name and database pair share the same
  startup parameters towards PostgreSQL." (line 71)
- "pgagroal can issue a `DEALLOCATE ALL` statement before a connection is returned back to
  the pool if the `track_prepared_statements` setting is set to `on`." (lines 93-95)
- "`DISCARD ALL` is not issued when a connection is returned to the pool in transaction
  mode. Session state such as `SET`, temporary tables and cursors is not reset between
  clients sharing the same backend connection." (lines 120-123). Note: `doc/CONFIGURATION.md:96`
  documents the newer `server_reset_query_always` that makes this optional.
- Performance and session pipelines: "A `DISCARD ALL` query is run after each client
  session." (lines 34, 46)

### 5. Language, license, maturity, weight

C, BSD-3-Clause, first tag 0.1.0 (2019-09), latest release 2.1.0 (2026-04-29), last commit
2026-11-05. Nothing to reuse from Go: it is a design data point (reset string + failure
policy + `ReadyForQuery`-driven release), not a library.

### Probe table (pgagroal, transaction pipeline, defaults)

| Probe | What pgagroal does | Code path |
| --- | --- | --- |
| P1 `SET search_path = x` | Forwarded unread. Persists on the backend; the next client of that backend sees it. | `pipeline_transaction.c:204-228, 425-480` |
| P2 `set_config(..., false)` | Same as P1. | same |
| P3 `SELECT f()` | Same as P1. | same |
| P4 lexical tricks | n/a (no reading). | — |
| P5 `PREPARE`, `LISTEN`, temp table, advisory lock, `WITH HOLD` | All persist. SQL `PREPARE` is *not* covered by `track_prepared_statements` (it watches only protocol `Parse` with a name). With `server_reset_query_always = on`: `DISCARD ALL` clears all of them. | `pipeline_transaction.c:208-219, 435-470` |
| P6 `SELECT 1; SET search_path = x` | Forwarded; release when `ReadyForQuery` says `'I'`. SET persists. | `message.c:505-520` |
| P7 (all) | n/a — no read/write or cache decision. | — |

## Claims of completeness

- **Odyssey** does not claim to decide session safety from SQL text. Its docs say the
  opposite for transaction pooling ("session variables or temporary tables are not
  supported", `docs/features/pooling.md`), and its code never uses the parser for session
  safety: the reset is unconditional per release. For read-only routing it claims only a
  conservative classification: "Queries which can not be parsed or can not be classified
  as read-only require the primary" (`docs/configuration/query-parsing.md`). The claim
  holds for parse failures and unknown statements; it does **not** fully hold inside the
  classifier (unlisted node types count as function-free; `json_object('k': f())` and
  `f() IS JSON` lose the call in the grammar shims; `COPY (SELECT f()) TO` is not
  checked; `txid_current` is wrongly "safe"). The server's read-only enforcement on a
  standby makes these errors, not wrong results.
- **pgagroal** makes no claim; it documents the gaps.

## Mapping to options (a) (b) (c1) (c2)

- **Odyssey session state → (b), in its strict form.** Reset after *every* transaction by
  default, no "known clean" shortcut, no reading involved; failure or unsynchronised state
  → close the connection. Its "smart discard" is a knowing carve-out of (b): it keeps
  prepared statements, which is safe for its own hash-named protocol statements and unsafe
  for client SQL `PREPARE`. Plus track-and-replay of reported parameters, which (b) does not
  need if mini-gateway forbids per-client settings.
- **Odyssey read-only routing → (c1) plus a hand allow-list, backstopped by (c2)-like
  server enforcement.** It *is* option (c1) — the real PostgreSQL grammar — and the
  experience matches the assessment's prediction: (c1) fixes the lexical layer (no
  string-continuation class of bugs) but not the grammar/catalog layer (function list,
  views, walker coverage). Safety comes from the standby refusing writes, which is the
  same principle as (c2) (`BEGIN READ ONLY`), applied by topology instead of by wrapping.
- **pgagroal → (b) only in session mode and as an opt-in in transaction mode; default
  transaction mode is "none of them" — document-and-shrug** (the PgBouncer position).

## Mechanisms not in the assessment

1. **Pin on a protocol signal instead of reading text (Odyssey `pool_pin_on_listen`).**
   Watch the server's `CommandComplete` tag; if it is exactly `LISTEN`, keep the client on
   that connection until `UNLISTEN *` succeeds (`stream.c:154-167`). The tag is the
   server's own statement of what ran, so comments, quoting and multi-statement tricks do
   not matter, and a `LISTEN` hidden in a function does not produce the tag (limit). In a
   Go proxy: a check on every `CommandComplete` (`C` message) for a small set of tags. For
   mini-gateway the useful variant is *taint, not pin*: a tag in {`SET`, `RESET`,
   `PREPARE`, `LISTEN`, `DECLARE CURSOR`, `CREATE TABLE`(temp unknown), `DISCARD`, …}
   marks the lease "not known clean" → reset at release. This replaces the
   "first keyword + taint words" text check of option (b) with a server-reported one for
   top-level statements, at the cost of one `strcmp` per reply. It cannot see
   `set_config()` inside a `SELECT` (tag `SELECT 1`) — but see 2.
2. **Learn session changes from `ParameterStatus` (Odyssey `kiwi_vars`).** The server
   sends `ParameterStatus` whenever a *reported* (`GUC_REPORT`) parameter changes, however
   it changed — `SET`, `set_config()`, a function, `RESET`. Odyssey uses it to replay; a
   proxy that forbids per-client settings can use it as a **taint signal**: any
   `ParameterStatus` during a lease → reset at release. On vanilla PostgreSQL this covers
   only reported parameters (`search_path` is `GUC_REPORT` on PostgreSQL master, `repos/postgres/src/backend/utils/misc/guc_parameters.dat:2622`, but not on PG ≤ 17 per canvas 01; `work_mem` is not reported), so
   it complements, not replaces, the reset.
3. **A server-side extension that reports session-object ownership through a custom
   reported GUC (Odyssey `ttt.owns_session_objs`).** The server tells the pooler "this
   session now owns temp relations/objects" via `ParameterStatus`, and the pooler pins
   (`backend.c:1191-1200`). This is the only mechanism I met that would catch P3
   (temp table created inside a function) *without* resetting. It needs an extension
   installed in the server — not "vanilla Postgres" — and I could not find the extension's
   source (see Could not verify). A cheaper vanilla analogue for mini-gateway is to *ask the
   server* at release with one query, e.g. whether `pg_my_temp_schema()` is non-zero /
   has relations, whether `pg_prepared_statements`, `pg_cursors`, `pg_listening_channels()`
   or `pg_locks` (advisory, own pid) are non-empty — but that costs the same round trip as
   `DISCARD ALL`, so it only helps if one wants to *reject* rather than reset.
4. **"Reset minus `DEALLOCATE ALL`" plus proxy-owned, hash-named prepared statements
   (Odyssey smart discard + `pool_reserve_prepared_statement`).** Not needed by
   mini-gateway (simple protocol only), but it shows the trap: any carve-out from
   `DISCARD ALL` reopens a leak for the client-visible version of the thing carved out.
5. **Defer `BEGIN` virtually (Odyssey `process_vbegin`, `virtual.c:777-801`).** Odyssey
   answers `BEGIN` itself and sends it joined to the next statement, so an idle-in-
   transaction client does not hold a backend before its first real statement. A pooling
   optimisation, not a safety mechanism; noted because it changes when a lease starts.
6. **Close instead of reset when the protocol state is not clean (both).** Odyssey closes
   when requests ≠ replies after cancel+wait, in COPY, in an extended pipeline, or with
   unread bytes (`reset.c:30-170, 252-257`); pgagroal kills on reset failure. The
   assessment has "close if `DISCARD ALL` fails"; the request/reply counter as a
   precondition for reuse is the extra piece (mini-gateway gets the same from "status byte
   `'I'` and nothing outstanding").

No mechanism here decides session safety from SQL text completely; none tries.

## Could not verify

- The **"ttt" server extension** behind `ttt.owns_session_objs`: not in the Odyssey
  repository (`grep -ri ttt docs README.md modules` finds nothing). COULD NOT FIND its
  source, docs, or which objects it reports (the code comment says "temp-rel ownership";
  the GUC name says "session objs").
- How Odyssey's vendored grammar will be kept in step with PostgreSQL: only two commits
  touch `gram.y` (import 2026-09-30, one edit 2026-11-03); COULD NOT FIND a sync script,
  pinned PG version, or policy. Which PG version it was taken from: its rules match
  PostgreSQL master of 2026-11-25 to 22 lines, so a recent master/19-devel snapshot; COULD
  NOT FIND an explicit statement.
- The routing holes listed in Odyssey §3 (`json_object`/`IS JSON` dropping the call,
  `COPY (SELECT f()) TO`, nested `FOR UPDATE`, `txid_current`) are from reading the code;
  I did not build Odyssey or run them (the brief did not ask for builds).
- Whether `od_reset` after every transaction is what production deployments run: the code
  default is yes, but shipped examples often set `pool_discard no`; COULD NOT FIND
  Odyssey documentation stating a recommended setting for transaction pools.
- The Odyssey docs table value "yes (0)" for `pool_reserve_prepared_statement`
  (`rules.md:64`) contradicts itself; code default is 1 (`pool.c:28`). Not resolved beyond
  the code.
- pgagroal's single-message read in `pgagroal_write_reset_query` (§2 observation):
  behaviour with multi-statement reset strings not tested.
- A "statement" pooling mode exists in Odyssey's code (`OD_RULE_POOL_STATEMENT`, detach
  after every reply, `frontend.c:1754-1755`) but the `pool` docs list only session and
  transaction (`rules.md:405-417`; the parameter table at line 46 lists statement). Not
  investigated further.
