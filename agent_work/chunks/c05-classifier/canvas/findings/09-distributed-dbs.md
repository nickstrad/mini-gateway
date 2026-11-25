# Wire-compatible distributed databases — CockroachDB and YugabyteDB (YSQL)

Canvas 09 for mini-gateway chunk 5. Research only. Line numbers refer to the sparse clones
listed under Sources, at the commits recorded there. Evidence tags: **VERIFIED IN CODE**,
**DOCS ONLY**, **COULD NOT FIND**. Nothing was built or run against either database.

The one-sentence answer: both systems "solve" the classifier problem because the component
that decides is the server itself, and where each has a proxy/pooler in front, that proxy
reads **no SQL** and instead receives a **server-produced signal** — CockroachDB's proxy asks
`SHOW TRANSFER STATE` and gets either a serialized session or an error string; YugabyteDB's
patched backend returns `ReadyForQuery` status `'i'` instead of `'I'` when the session holds
unpoolable state, and sends a private `'r'` ParameterStatus for every session-sourced GUC
change. Neither signal exists in vanilla PostgreSQL, but a weaker version of both can be
built from PostgreSQL's catalog views (see "Mechanisms not in the assessment").

## Sources

| Project | URL | Commit SHA | Commit date | Local / web |
| --- | --- | --- | --- | --- |
| CockroachDB | https://github.com/cockroachdb/cockroach | `d30c905fff79ef825adc96bcc647f1872a90f2ff` (master) | 2026-09-23 09:47 -04:00 | LOCAL SOURCE, sparse blobless depth-1 clone `repos/cockroach` (pkg/sql/parser, pkg/sql/scanner, pkg/sql/*.go, pkg/sql/sem/{tree/stmt.go,volatility,builtins}, pkg/sql/opt/{optbuilder,exec/execbuilder}, pkg/sqlproxy, two RFCs) |
| cockroachdb-parser | https://github.com/cockroachdb/cockroachdb-parser | `97d0c5a3e2190b6135858dac489886d2324e4ade` | 2026-04-30 | WEB READS (`gh api`: repo metadata, README, `version`, latest commit) |
| CockroachDB docs, PostgreSQL compatibility | https://www.cockroachlabs.com/docs/stable/postgresql-compatibility | — | fetched 2026-11-25 (page is v26.4) | WEB READ |
| YugabyteDB | https://github.com/yugabyte/yugabyte-db | `42689cf0e88b7b3999bf3adf73423dd3b4edfaf7` (master) | 2026-11-25 22:17 +05:30 | LOCAL SOURCE, sparse blobless depth-1 clone `repos/yugabyte-db` (src/odyssey/{sources,third_party}, src/postgres/src/backend/{tcop,utils/misc,parser,access/transam,commands,catalog,utils/cache,utils/adt/lockfuncs.c,utils/mmgr/portalmem.c}, src/postgres/src/include/utils) |
| YSQL Connection Manager docs | https://docs.yugabyte.com/stable/additional-features/connection-manager-ysql/ycm-setup/ | — | fetched 2026-11-25 | WEB READ |
| PostgreSQL (for the probe section) | `repos/postgres` (shared clone, read only) | `4483697be0e0d22b9b4fd847e8bf2d998c56a741` | 2026-11-25 17:31 +01:00 | LOCAL SOURCE |

Newest releases (from `git ls-remote --tags`, not from release pages): CockroachDB `v26.2.12`
is the highest non-prerelease `v2*` tag; YugabyteDB `2026.2.0.0-b39` is the highest `2026*`
tag. CockroachDB's own docs page is already labelled v26.4, so tags lag docs; I did not
reconcile this. YSQL's PostgreSQL fork is **PostgreSQL 15.12**
(`src/postgres/configure.ac`: `AC_INIT([PostgreSQL], [15.12], ...)`).

## Summary rows

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| **CockroachDB server** | Own Go LALR grammar `pkg/sql/parser/sql.y` (20,835 lines; header "Portions Copyright (c) 1996-2015, PostgreSQL Global Development Group", i.e. hand-forked from a ~9.5-era `gram.y` and maintained by hand since) + hand-written Go scanner `pkg/sql/scanner/scan.go` (1,296 lines), not a port of `scan.l`. | Session state is server objects (`varGen` in `pkg/sql/vars.go`, `sessiondata.SessionData`). No pooling across clients. | Server enforces read-only on the **optimizer plan tree** (any mutation operator, any locking, `nextval()`), not on statement text. Function volatility is a property of every builtin, tested against a PostgreSQL `pg_proc.provolatile` dump. | n/a (exact semantic knowledge) | n/a for a proxy: the server is the authority. | Closed: unknown syntax is a parse error; `standard_conforming_strings=off` is refused. |
| **CockroachDB sqlproxy** (`pkg/sqlproxy`) | **Nothing.** Message headers and type bytes only. | No cross-client sharing (one client ↔ one SQL session). Migration between pods: proxy waits for a protocol-level safe point, sends `SHOW TRANSFER STATE`, and the **server** either serializes the session or returns an error string (open txn, active portals, temp schemas). Then `crdb_internal.deserialize_session` on the new pod. | None. | Server's refusal list: 3 conditions (+ a size cap). | Migration skipped (best effort). Possible silent loss of holdable cursors on migration — see Could not verify. | Closed: any server error ⇒ no transfer. |
| **YugabyteDB YSQL backend** | The real PostgreSQL 15 parser (`src/postgres/src/backend/parser/gram.y`, `scan.l`), in the server. | Server keeps sticky flags set by the **code paths that create state** (heap.c for temp tables, prepare.c, async.c, lockfuncs.c, portalmem.c, guc.c) and reports them in the `ReadyForQuery` status byte (`'i'` = idle but sticky). Every session-sourced GUC change is pushed to the pooler as a private `'r'` message. | n/a (server). | n/a | n/a | n/a |
| **YSQL Connection Manager** (Odyssey fork, `src/odyssey`) | **Nothing** on the data path (query text only logged; `od_parser` is for config/console). | Transaction pooling + **track and replay** GUCs per logical client (from `'r'` messages, replayed as `SET` text / a `'G'` packet at `od_deploy`) + **pin** ("sticky") when the server says `'i'`. Default mode: transaction pooling. | None. | Sticky triggers are a server-side list in `YbIsStickyConnection()` (pg_yb_utils.c:9133). | A trigger missing from the server list ⇒ state leaks to the next client of the backend (documented example: protocol-level prepared statements "may be visible to other sessions"; `currval`/`lastval`). | Open where the server list is incomplete; closed for superusers by default (always sticky). |

## CockroachDB

### 1. Does it read SQL?

**Server: yes, with its own grammar.** VERIFIED IN CODE.

- `pkg/sql/parser/sql.y`: 20,835 lines (PostgreSQL master `gram.y` in the shared clone:
  20,166). Header lines 1–2: `// Portions Copyright (c) 1996-2015, PostgreSQL Global
  Development Group`. The grammar comments cite PostgreSQL 9.6/10 docs
  (`sql.y:8964`, `:9007`, `:12636`), and `sql.y:4745` points at PostgreSQL's `gram.y` for the
  COPY rules. So it began as a derivative of PostgreSQL's grammar and is kept in step **by hand**,
  feature by feature; there is no mechanical sync. COULD NOT FIND any sync tooling.
- `pkg/sql/scanner/scan.go`: 1,296 lines of hand-written Go (plus `plpgsql_scan.go`,
  `jsonpath_scan.go`). It is not generated from `scan.l`.
- `pkg/sql/sem/tree/stmt.go:1` says the AST "was derived from https://github.com/youtube/vitess".

**Is it a faithful parser for real PostgreSQL text? No.** VERIFIED IN CODE for each example:

| PostgreSQL text | PostgreSQL | CockroachDB parser |
| --- | --- | --- |
| `LISTEN c` | statement | no `LISTEN` production at all; only `UNLISTEN` (`sql.y:16371`) → syntax error |
| `CREATE FUNCTION f() ... SET search_path = x` | function with a SET clause | `sql.y:6079` `SET { return unimplemented(sqllex, "create function/procedure ... set") }` |
| `SET x FROM CURRENT` | statement | `sql.y:8972` unimplemented |
| `CREATE RULE`, `CREATE AGGREGATE`, `GROUPING SETS`, `SET CONSTRAINTS` | statements | `sql.y:6589`, `:6580`, `:15763`, `:8327` unimplemented |
| `b'1010'` | **bit string**, no escapes | **BYTES literal with backslash escapes** (`scan.go:209-216`, `scanString(..., true /* allowEscapes */ ...)`); upper-case `B'...'` is the bit string (`scan.go:239-247`). So `b'\''` is one complete literal in CockroachDB but not in PostgreSQL: the string boundaries differ. |
| `'it\'s'` with `standard_conforming_strings = off` | one string (backslash escapes the quote) | plain `'...'` is always scanned with `allowEscapes=false` (`scan.go:204`), and the setting cannot be turned off: `vars.go:1894` `makeCompatBoolVar("standard_conforming_strings", true, false /* anyAllowed */)` — setting `off` errors (`vars.go:5425-5435`). |
| `U&'d\0061t\+000061'`, `U&"..."` | Unicode-escape string/identifier | COULD NOT FIND any `U&` handling in `scan.go` (grep for `U&`, `UIDENT`, `UESCAPE`: no hits). It lexes as identifier `U`, operator `&`, string — same string boundaries, wrong meaning. |

The parse errors fail closed for a proxy (an unparseable statement can be refused). The
lexical divergences are the dangerous kind for mini-gateway: they are exactly review 2's
blocker class — the proxy's lexer and the server's lexer disagreeing on where a string ends
(a server or role with `standard_conforming_strings=off`; any `b'...\...'` literal).
I did not construct and run an end-to-end exploit; the divergence is read from code.

**As a standalone Go library.** `github.com/cockroachdb/cockroachdb-parser`, WEB READS via
`gh api`: description "Apache licensed CockroachDB parser and dependencies", license
**Apache-2.0**, **archived: true**, last commit 2026-04-30 "parser: v26.2.0 snapshot …
Sync parser and dependencies from CockroachDB v26.2.0 (34d38d7…)". README: "a snapshot of the
parser package and all its dependencies … Versioning is done by CockroachDB version, with a
`v0.` prepended … There are custom patches in `patches/`". Its `go.mod` lists 60 versioned
requirement lines (counted lines matching a version; not a careful dependency audit). The main
`cockroach` repo itself is under the "CockroachDB Software License" (`LICENSE`, first line),
not an OSI licence (`gh api` reports `NOASSERTION`). So: pure Go (no cgo), Apache-2.0, but
archived, a dialect parser, and a heavy module.

### 2. Session-state safety

CockroachDB has no cross-client pooling; the problem mini-gateway has (state crossing
tenants on a shared connection) does not occur. What it has instead is **session migration**
between SQL pods in Serverless, and that is where the interesting mechanism lives.

**Session variables are server objects.** VERIFIED IN CODE. `pkg/sql/vars.go:205`
`var varGen = map[string]sessionVar{` — 271 entries, each with `Get`/`Set`/`GlobalDefault`.
`set_config` is a builtin that calls the same setter (`pkg/sql/sem/builtins/pg_builtins.go:2476-2494`,
`setSessionVar(ctx, evalCtx, varName, newValue, is_local)`, `Volatility: volatility.Volatile`).
So `SET`, `set_config` and anything a function does all land in one place: `SessionData`.

**The proxy reads no SQL.** VERIFIED IN CODE. No file in `pkg/sqlproxy` imports
`pkg/sql/parser` or `pkg/sql/sem/tree` (grep over non-test files: no hits). The RFC says the
proxy "is only expected to parse the message headers" (`docs/RFCS/20220129_sqlproxy_connection_migration.md:110`).

**Step 1 — a protocol-level safe point, decided by the proxy from message types only.**
VERIFIED IN CODE, `pkg/sqlproxy/conn_migration.go:297-326`:

```go
// Three conditions when evaluating a safe transfer point:
//   1. The last message sent to the SQL pod was a Sync(S) or SimpleQuery(Q),
//      and a ReadyForQuery(Z) has been received after.
//   2. ... CopyDone(c) ...   3. ... CopyFail(f) ...
if request.mu.lastMessageTransferredAt > response.mu.lastMessageTransferredAt {
	return false
}
switch pgwirebase.ClientMessageType(request.mu.lastMessageType) {
case pgwirebase.ClientMessageType(0), pgwirebase.ClientMsgSync,
	pgwirebase.ClientMsgSimpleQuery, pgwirebase.ClientMsgCopyDone, pgwirebase.ClientMsgCopyFail:
	serverMsg := pgwirebase.ServerMessageType(response.mu.lastMessageType)
	return serverMsg == pgwirebase.ServerMsgReady || serverMsg == pgwirebase.ServerMessageType(0)
```

Note it does **not** look at the ReadyForQuery status byte; transaction state is left to the server.

**Step 2 — ask the server.** VERIFIED IN CODE. The proxy writes
`SHOW TRANSFER STATE WITH '<uuid>'` (`conn_migration.go:338`). On the server this is an
*observer statement* (runs regardless of transaction state) handled by
`runShowTransferState` (`pkg/sql/conn_executor_exec.go:4165`), which returns columns
`error, session_state_base64, session_revival_token_base64, transfer_key`; on failure only
`error` is filled. The proxy treats an error string as "not now":

```go
// conn_migration.go:259-267
// This case may happen pretty frequently (e.g. open transactions, temporary
// tables, etc.).
if transferErr != "" {
	return nil, errors.Newf("%s", transferErr)
}
```

**What the server refuses** — `serializeSessionState`, `pkg/sql/session_state.go:55-110`:

```go
if inExplicitTxn {           // "cannot serialize a session which is inside a transaction"
if prepStmtsState.HasActivePortals() { // "... which has active portals"
if sd == nil {               // "no session is active"
if len(sd.DatabaseIDToTempSchemaID) > 0 { // "cannot serialize session with temporary schemas"
...
m.SessionData = sd.SessionData                 // all session variables
m.LocalOnlySessionData = sd.LocalOnlySessionData
m.PreparedStatements, err = prepStmtsState.MigratablePreparedStatements()
if maxSize > 0 && int64(len(b)) > maxSize {    // sql.session_transfer.max_session_size
```

So: session variables and SQL/protocol prepared statements are **carried** (serialized as SQL
text + parameter type hints, `conn_executor.go:2193-2215`, re-parsed on the new pod at
`session_state.go:140-150`); open transactions, active portals and temp schemas **block**.
The CCL test confirms two of the error strings
(`pkg/ccl/testccl/sqlccl/show_transfer_state_test.go:247,268-276`: `CREATE TEMP TABLE` →
"cannot serialize session with temporary schemas").

**Step 3 — revive on the new pod.** VERIFIED IN CODE. The new connection authenticates with
the session revival token (`connector.OpenTenantConnWithToken`, `conn_migration.go:271`;
token minted by `createSessionRevivalToken`, `pkg/sql/session_revival_token.go:49-70`, refused
for root), then runs `SELECT crdb_internal.deserialize_session(decode('<b64>','base64'))`
(`conn_migration.go:458`). `DeserializeSessionState` (`session_state.go:113-`) refuses inside a
multi-statement transaction and refuses unless the session user matches.

Why the other PostgreSQL session features are absent from the refusal list: CockroachDB does
not have them in session form. DOCS ONLY (compatibility page): "`pg_advisory_lock`,
`pg_advisory_lock_shared`, and `pg_try_advisory_lock_shared` are not defined. Calling them
returns an error", and `pg_try_advisory_lock` / `pg_advisory_unlock*` "silently succeed
without any effect". VERIFIED IN CODE that `pg_advisory_lock` appears in
`pkg/sql/sem/builtins` only inside the PostgreSQL CSV test fixture, and that the grammar has
no `LISTEN`. Holdable cursors **do** exist (`pkg/sql/sql_cursor.go:41`, `:101-105`, `:434-436`
"txnCommit: … persist holdable cursors"), and I found **no** cursor check in
`serializeSessionState` — see Could not verify.

**`DISCARD ALL`** (`pkg/sql/discard.go:35-65`): refuses inside a transaction; then
`SET SESSION AUTHORIZATION DEFAULT`, `RESET ALL`, `DEALLOCATE ALL`, reset sequence state,
delete temp tables. VERIFIED IN CODE. No cursor close in that function (grep `cursor|portal`
in `discard.go`: no hits).

### 3. Read-vs-write and shareability

The server knows exactly, from the plan, not from text. VERIFIED IN CODE:

- Mutations: `pkg/sql/opt/exec/execbuilder/relational.go:146-158` — for **any** mutation
  operator in the expression tree (so a data-modifying CTE counts), if `TxnReadOnly`, error
  `cannot execute %s in a read-only transaction` (only `DISCARD` is exempted).
- Row locks: `relational.go:3453-3457` — `SELECT … FOR UPDATE/SHARE` in a read-only
  transaction errors.
- Sequences: `pkg/sql/sequence.go:74-76` — `nextval()` in a read-only transaction errors
  (`readOnlyError("nextval()")`, `sequence.go:433-435`).
- Volatility is a per-overload attribute (`pkg/sql/sem/volatility/volatility.go`: Leakproof <
  Immutable < Stable < Volatile; "functions having side-effects, such as setval(), must be
  labeled volatile"). `TestOverloadsVolatilityMatchesPostgres`
  (`pkg/sql/sem/builtins/all_builtins_test.go:57`) checks every builtin against a dump of
  PostgreSQL's `pg_proc.provolatile` (`testdata/pg_proc_provolatile_dump.csv`).
- There is also a **syntactic** helper, `tree.CanWriteData` (`pkg/sql/sem/tree/stmt.go:190-213`),
  a type switch over AST node kinds (`*Insert, *Delete, *Update, *Truncate, *CopyFrom, …`). It
  is used only to classify opaque statements (`pkg/sql/opaque.go:525`). Even CockroachDB's own
  AST-level list would say a `WITH d AS (DELETE …) SELECT` is a `*Select`; the authority is
  the plan-time check above. That is the CockroachDB analogue of the assessment's lesson.

No result sharing/coalescing exists in the proxy. A miss cannot happen in the server sense.
Fails closed: parse error → error; unknown function → error.

### 4. Documented limitations on session features

- RFC, requirement 3 (`docs/RFCS/20220129_sqlproxy_connection_migration.md:71-73`):
  "Connection migrations are on a best-effort basis as it is known that not every connection
  can be transferred (e.g. sessions with temporary tables or active transactions)."
- RFC on the safe-point heuristic (`…:399-430`): "This approach comes with several drawbacks:
  1. The ReadyForQuery in condition (1) may not correlate with the Sync or SimpleQuery that
  was sent earlier since there could be in-flight queries in the pipelined case." The
  message-counting alternative was rejected: "All this could get complex, and can be
  difficult to get it right."
- Compatibility page (WEB READ): session-scoped advisory locks unsupported, quoted above.

### 5. Language, licence, maturity, weight

Go. Main repo: CockroachDB Software License (source-available, not OSI). Parser snapshot:
Apache-2.0, archived. Mature and very active (master commit 2026-09-23 in this clone; tags
to v26.2.12). For a Go proxy, reusing the parser means a ~60-requirement module that parses a
different dialect; reusing the *mechanism* (serialize/deserialize) is impossible without the
server.

### Probe table — CockroachDB (server + sqlproxy)

| Probe | What happens | Code |
| --- | --- | --- |
| P1 `SET search_path = x` | Server: session var set; proxy: no reading. Migration carries it (`m.SessionData`). | `vars.go:205`; `session_state.go:88-91` |
| P2 `SELECT set_config('search_path','h',false)` | Same setter as P1; carried by migration. | `pg_builtins.go:2476-2494` |
| P3 `SELECT f()` doing SET / temp table | SET inside a routine changes `SessionData` → carried; temp table → `DatabaseIDToTempSchemaID` non-empty → transfer refused. Text irrelevant. | `session_state.go:81-86` |
| P4 lexical tricks | Proxy: n/a (does not lex). Server: its own scanner decides; diverges from PostgreSQL on `b'…'` and `standard_conforming_strings`, but that only matters if CockroachDB's parser is reused **in front of PostgreSQL**. | `scan.go:204-216`; `vars.go:1894` |
| P5 PREPARE / LISTEN / TEMP / advisory / WITH HOLD | PREPARE: carried (re-parsed on new pod). LISTEN: syntax error. TEMP: blocks transfer. `pg_advisory_lock(1)`: undefined → error (DOCS ONLY). WITH HOLD: supported; no transfer check found. | `conn_executor.go:2193`; `sql.y:16371`; `session_state.go:81`; `sql_cursor.go:41` |
| P6 `SELECT 1; SET search_path = x` | Server executes both; the proxy only waits for the final ReadyForQuery before any transfer. | `conn_migration.go:297-326` |
| P7 nextval / DELETE-CTE / FOR UPDATE / now() / volatile view / EXPLAIN ANALYZE DELETE | Server, read-only txn: nextval error; CTE DELETE error (mutation op in plan); FOR UPDATE error; now() fine (Stable); view over volatile fn: allowed unless the fn writes (volatility marks it, enforcement is at the mutation/sequence call); EXPLAIN ANALYZE DELETE: plan contains a mutation → error. No proxy decision. | `relational.go:146-158`, `:3453-3457`; `sequence.go:74` |

## YugabyteDB (YSQL + YSQL Connection Manager)

### 1. Does it read SQL?

**Server: the real PostgreSQL 15.12 parser.** VERIFIED IN CODE:
`src/postgres/src/backend/parser/gram.y` (19,832 lines) and `scan.l` (1,438 lines, vs 1,421 in
PostgreSQL master) in the fork; version from `src/postgres/configure.ac`. Kept in step by
periodic rebases of the whole fork (DOCS ONLY / COULD NOT FIND the merge procedure; I did not
look at history in a depth-1 clone).

**Pooler: nothing.** VERIFIED IN CODE. In `src/odyssey/sources/frontend.c:1421-1433`, a client
`Query` message is only logged (`od_frontend_log_query`), queued for prepared-statement
bookkeeping, and counted for sync (`od_server_sync_request`). `od_parser_init` is used only
by `config_reader.c:427` and the admin console (`console.c:1992`).

### 2. Session-state safety — the server tells the pooler

This is the most useful mechanism in this cluster. Three parts.

**(a) The sticky bit rides in the ReadyForQuery status byte.** VERIFIED IN CODE.
Server side, `src/postgres/src/backend/access/transam/xact.c:5495-5505`:

```c
TransactionBlockStatusCode(void)
{
	switch (s->blockState)
	{
		case TBLOCK_DEFAULT:
		case TBLOCK_STARTED:
			return ((YbIsClientYsqlConnMgr() &&
					 YbIsStickyConnection(&(s->ybUncommittedStickyObjectCount)))
					? 'i' : 'I');	/* idle --- not in transaction */
```

Pooler side, `src/odyssey/sources/backend.c:643-680` (`od_backend_ready`):

```c
// We piggyback the stickiness bit 'i' from the response message
// to be absorbed by odyssey, which then in turn makes the connection
// sticky ...
if (status == 'I' || status == 'i') {
	if (status == 'i') {
		... route->server_pool.yb_count_sticky++;
		server->yb_sticky_connection = true;
		*kiwi_header_data((kiwi_header_t *)data) = 'I';   // rewritten before the client sees it
	} else {
		if (server->yb_sticky_connection) { ... yb_count_sticky--; }
		server->yb_sticky_connection = false;
	}
	server->is_transaction = 0;
```

and the detach decision, `frontend.c:1118-1127`: in transaction pooling, when not in a
transaction, `if (server->yb_sticky_connection) … "sticky connection established"; else
return OD_DETACH;`. So the pooler's whole "is this connection clean?" decision is one byte
the server computed. Note the code un-sticks on a later `'I'`, while the docs say "once
formed, sticky connections remain sticky until the end of the session" — for most triggers
the server flags are latched (below), so both are mostly true.

**What makes the server say `'i'`** — `YbIsStickyConnection`, `src/postgres/src/backend/utils/misc/pg_yb_utils.c:9133-9153`:

```c
return (ysql_conn_mgr_sticky_object_count > 0) ||      /* TEMP tables (and WITH HOLD per header) */
	YbIsConnectionMadeStickyUsingGUC() ||               /* certain GUCs; superuser ever */
	yb_ysql_conn_mgr_sticky_locks ||                    /* session advisory locks, latched */
	yb_ysql_conn_mgr_sticky_nontxn_object ||            /* e.g. LOAD, latched */
	yb_ysql_conn_mgr_sticky_replication_origin || YbHasReplicationOriginSetup() ||
	yb_ysql_conn_mgr_sticky_prepare || YbHasSqlPreparedStatement() ||
	yb_ysql_conn_mgr_sticky_listen ||
	/* Unsticking a listener makes it miss its notifications, and the next
	 * client on this backend receives them. */
	YbIsListening() ||
	yb_ysql_conn_mgr_sticky_cursor || YbHasHoldablePortal();
```

Where each flag is set — all at the point where the state is **created**, not where text is read:

| Trigger | Set in | Evidence |
| --- | --- | --- |
| temp table | `catalog/heap.c:1771-1776` `if (YbIsClientYsqlConnMgr() && new_rel_desc->rd_islocaltemp) increment_sticky_object_count();` (per-transaction count, committed at txn end, `xact.c:6995-7008`, `:5708`) | VERIFIED |
| session advisory lock | `utils/adt/lockfuncs.c:89-90`, `:105-106` `if (… && session_level) yb_ysql_conn_mgr_sticky_locks = true;` | VERIFIED |
| SQL `PREPARE` | `commands/prepare.c:838`; `YbHasSqlPreparedStatement` `prepare.c:887-892` | VERIFIED |
| `LISTEN` | `commands/async.c:1608`; `YbIsListening` `async.c:4277-4280` | VERIFIED |
| holdable cursor | `utils/mmgr/portalmem.c:1224-1253` (`cursorOptions & CURSOR_OPT_HOLD`) | VERIFIED |
| `LOAD` | `tcop/utility.c:909-919` "Making connection sticky for LOAD" | VERIFIED |
| `SET role` / `SET session_authorization` | `utils/misc/guc.c:11960-11971` (only when `source == PGC_S_SESSION`) | VERIFIED |
| SUSET GUC set while current user ≠ session user (e.g. inside SECURITY DEFINER) | `guc.c:13015-13029` "Conn Mgr replays the GUC as the session user, which can fail" | VERIFIED |
| GUCs flagged `GUC_YB_CONN_MGR_STICKY` (`default_table_access_method`, `default_tablespace`, `temp_tablespaces`, `default_text_search_config`, `yb_pg_batch_detection_mechanism`, every custom string GUC of an extension) | flag at `guc.c:7442,7460,7478,7979,8764,14353`; check at `guc.c:13031-13046` (session-level SET or startup value only; "SET LOCAL and function-level SET don't outlive the transaction") | VERIFIED |
| superuser ever in session (default on) | `pg_yb_utils.c:9108-9122`; gflag `ysql_conn_mgr_superuser_sticky` (docs default `true`) | VERIFIED + DOCS |

There are also `yb_conn_mgr_{prepare,listen,cursor,replication_origin}_permanent_sticky`
GUCs (`pg_yb_utils.c:9085-9091`, assigned at `guc.c:18651-18665`) that latch the flag instead
of re-evaluating it. I did not trace their defaults.

**(b) GUC changes are pushed, not inferred.** VERIFIED IN CODE. With a Connection Manager
client, `ReportGUCOption` (`guc.c:10998-11063`) sends a private message type **`'r'`**
instead of `'S'`, carrying name, value and a flags byte (`REPORT_ENABLED` if the GUC is a real
`GUC_REPORT`, `SESSION_VAL_RESET`, `DEFAULT_VAL_RESET`, `SOURCE_STARTUP`,
`USERSET_OR_SUSET_SOURCE_SESSION`). Which GUCs are reported, `yb_should_report_guc`
(`guc.c:10114-10128`):

```c
bool shouldReportGUC = record->flags & GUC_REPORT;
if (YbIsClientYsqlConnMgr()) {
	if (record->flags & GUC_YB_NO_CONN_MGR_REPORT) return false;
	shouldReportGUC = shouldReportGUC ||
		(record->status & (YB_GUC_VALUE_RESET | YB_GUC_DEFAULT_RESET)) ||
		(record->context >= PGC_SU_BACKEND &&
		 (record->source == PGC_S_CLIENT || record->source == PGC_S_SESSION));
```

i.e. **every** GUC whose value now comes from the session — whatever statement, function or
`UPDATE pg_settings` put it there — is reported. `'r'` is defined as
`YB_CONN_MGR_PARAMETER_STATUS = 'r'` (`src/odyssey/third_party/kiwi/kiwi/header.h:84`); the
pooler refuses a plain `'S'` from the backend outright (`backend.c:1005-1009`, `:1728-1733`:
"Unexpected ParameterStatus packet 'S' from postgres, refusing to parse") and stores `'r'`
values per client/server in `od_backend_update_parameter` (`backend.c:1620-1660`), forwarding
a normal `'S'` to the client only when the GUC is genuinely `GUC_REPORT`. `role` and
`session_authorization` are deliberately not tracked there (`backend.c:1652-1653`) — they make
the connection sticky instead.

**(c) Replay when a backend is lent.** VERIFIED IN CODE, `src/odyssey/sources/deploy.c:152-245`
(`od_deploy`): if the backend's session values differ from what this client needs
(`yb_check_reset_needed`), send a reset to the backend default; send a `'G'` packet with the
client's startup values (`yb_send_set_guc_defaults`); then build `SET k=v;…` text from the
difference between the client's and the server's tracked variables (`kiwi_vars_cas`) and send
it as one Query. The fallback reset (`reset.c:258-275`) is Odyssey's `DISCARD ALL` or the
"smart" sequence `SET SESSION AUTHORIZATION DEFAULT;RESET ALL;CLOSE ALL;UNLISTEN *;SELECT
pg_advisory_unlock_all();DISCARD PLANS;DISCARD SEQUENCES;DISCARD TEMP;`.

Default: transaction pooling with replay + stickiness; superuser connections sticky.

### 3. Read-vs-write and shareability

None in the pooler. The server is PostgreSQL. COULD NOT FIND any read/write routing or result
sharing in `src/odyssey/sources` (no SQL reading at all).

### 4. Documented limitations (ycm-setup page, WEB READ, quoted)

- "Currently, once formed, sticky connections remain sticky until the end of the session. At
  the end of the session, the backend process corresponding to a sticky connection is
  destroyed along with the connection, and the connection does not return to the pool."
- Sticky triggers as documented: "Creating TEMP tables. Declaring a CURSOR using the WITH HOLD
  attribute. Using a PREPARE query … Superuser connections … Using a SEQUENCE with
  `ysql_conn_mgr_sequence_support_mode` set to `session` … Replication connections. Setting
  the following configuration parameters during the session: `session_authorization`, `role`,
  `default_tablespace`, `temp_tablespaces`, Any string-type variables of extensions,
  `yb_read_after_commit_visibility`." (The code list above is longer: advisory locks, LISTEN,
  LOAD, SUSET-as-other-user are in code but not in this doc list.)
- "Prepared statements may be visible to other sessions in the same connection pool. #24652"
- "By default, `currval` and `lastval` functions do not work when YSQL Connection Manager is
  enabled." and, in `pooled_with_curval_lastval` mode, "A client can see a value that another
  client's `nextval` generated on that backend".
- "`idle_session_timeout` doesn't time out non-sticky client connections … the setting stays
  on the backend after the transaction ends."
- `ALTER ROLE/DATABASE … SET` needs its own adoption strategy flag
  (`fluctuating` / `gradual` / `connection_static`).

These limitations are the honest residue: each is state the server does **not** flag (sequence
session cache, protocol-level prepared statements, a GUC that acts on the backend itself).

### 5. Language, licence, maturity, weight

C (Odyssey fork) + C/C++ (PostgreSQL 15 fork + DocDB). `LICENSE.md`: core including
`src/postgres` under Apache-2.0; `src/odyssey` "has its own BSD-style license" (Yandex).
Active (master commit 2026-11-25). Reuse weight for a Go proxy: the pooler side is small in
concept (watch one status byte, store `'r'` messages, replay `SET`), but it depends on a
**patched server**; none of it works against vanilla PostgreSQL.

### Probe table — YugabyteDB (YSQL Connection Manager + YSQL backend)

| Probe | What happens | Code |
| --- | --- | --- |
| P1 `SET search_path = x` | Backend reports `'r' search_path` (source session); pooler records it for this client and replays `SET search_path` on whatever backend the client gets next; not sticky. | `guc.c:10114-10128`, `:11057`; `backend.c:1620-1660`; `deploy.c:199-226` |
| P2 `set_config('search_path','h',false)` | Identical to P1: same GUC machinery, source `PGC_S_SESSION`. | same |
| P3 `SELECT f()` doing SET / temp table | Function-body `SET`/`set_config` → reported as P1 (function `SET` clauses are reverted, `guc.c:13031-13036` comment). Temp table → `heap.c` count → `'i'` → sticky. Text irrelevant. | `heap.c:1771-1776`; `xact.c:5503` |
| P4 lexical tricks | n/a: neither side relies on text. | — |
| P5 PREPARE / LISTEN / TEMP / advisory / WITH HOLD | All five make the backend answer `'i'` → sticky. | table in §2 |
| P6 `SELECT 1; SET search_path = x` | Server executes both; reports `'r'` before the single ReadyForQuery; pooler applies it. | `backend.c:1734-1745` |
| P7 nextval / DELETE-CTE / FOR UPDATE / now() / volatile view / EXPLAIN ANALYZE DELETE | No pooler decision. `nextval` affects `currval`/`lastval` session state the pooler does not track (documented limitation; sticky only in `session` mode). | DOCS ONLY |

## Claims of completeness

- Neither project claims to decide session safety from SQL text; both proxies explicitly do
  not read SQL. VERIFIED (no parser import in `pkg/sqlproxy`; no SQL reading in Odyssey's
  data path).
- CockroachDB's RFC claims only best effort (quoted in §4) — the claim matches the code.
- YugabyteDB's docs say the manager "makes connections for some features sticky by default"
  "to offer complete correctness/range of support" (troubleshooting page). The code contradicts
  completeness on its own terms: the Limitations list names state that leaks (prepared
  statements visible across sessions, `currval`/`lastval`, `idle_session_timeout`). So "complete"
  does not survive; "complete for the triggers in `YbIsStickyConnection`" does.

## Mapping to options (a) (b) (c1) (c2)

- **CockroachDB server**: (c2) taken to its end — the server enforces read-only on the plan.
  CockroachDB's parser as a library would be a (c1) candidate in pure Go, but **wrong for
  PostgreSQL text** (dialect gaps; lexical divergences on `b'…'` and
  `standard_conforming_strings=off`), so it would re-introduce review 2's blocker class rather
  than remove it. Not recommended.
- **CockroachDB sqlproxy**: none of (a)–(c). It is "ask the server whether the session is
  clean, and let the server refuse" — closest in spirit to (b) (mechanism, not reading), but
  the cleanliness decision is the server's, not a keyword allow-list.
- **YSQL Connection Manager**: none of (a)–(c) as written. It is (b)'s *track-and-replay +
  pin* variant with the "known clean" bit computed **by the server**. The assessment's (b) uses
  a text taint list as a stand-in for that bit; YugabyteDB shows what the stand-in approximates.

## Mechanisms not in the assessment

**M1 — Server-computed cleanliness bit in ReadyForQuery (YugabyteDB).** The backend computes
"idle but holding unpoolable state" from the code paths that create state, and encodes it in
the existing status byte (`'i'`). Cost in a proxy: one byte check at lease end. **Not
available on vanilla PostgreSQL** — the status byte is only `I/T/E`
(PostgreSQL `TransactionBlockStatusCode`, `src/backend/access/transam/xact.c:5110,5121,5130` in the shared clone — VERIFIED IN CODE), and changing it needs a server patch.

**M2 — Push every session-sourced GUC change (YugabyteDB `'r'`).** Removes the need to know
*how* a setting was changed. On vanilla PostgreSQL only `GUC_REPORT` parameters are pushed as
`ParameterStatus`. In the shared PostgreSQL master clone these are: `application_name`,
`client_encoding`, `DateStyle`, `default_transaction_read_only`, `in_hot_standby`,
`integer_datetimes`, `IntervalStyle`, `is_superuser`, `scram_iterations`, `search_path`,
`server_encoding`, `server_version`, `session_authorization`, `standard_conforming_strings`,
`TimeZone` (`src/backend/utils/misc/guc_parameters.dat` lines 63…3106; `search_path` at 2622
carries `GUC_REPORT` in master — PgBouncer canvas 01 notes it is not reported on PG ≤ 17).
So a vanilla-PostgreSQL proxy **can** watch `ParameterStatus` for those 15 and treat any
change as "dirty" — a cheap, text-free signal for exactly the most dangerous setting
(`search_path`, on PostgreSQL 18+), including changes made by `set_config()` or inside a
function, because the server emits it at the end of the statement regardless of spelling.

**M3 — Ask the server before reuse (CockroachDB `SHOW TRANSFER STATE` → vanilla "dirtiness
probe").** CockroachDB needs a server statement; on vanilla PostgreSQL the closest is a
catalog query sent by the proxy at lease end, while the backend is idle (status `I`). Sketch
(not run; each part cites PostgreSQL docs):

```sql
SELECT
  (SELECT count(*) FROM pg_prepared_statements)                       AS prepared,  -- [1]
  (SELECT count(*) FROM pg_cursors)                                   AS cursors,   -- [2] at idle only WITH HOLD survive
  (SELECT count(*) FROM pg_listening_channels())                      AS listening, -- [3]
  (SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'
                                   AND pid = pg_backend_pid())        AS adv_locks, -- [4] at idle all are session-level
  (SELECT count(*) FROM pg_settings WHERE source = 'session')         AS gucs,      -- [5]
  (SELECT count(*) FROM pg_class
     WHERE relnamespace = pg_my_temp_schema())                        AS temp_rels, -- [6]
  current_user <> session_user                                        AS role_changed -- [7]
```

[1] https://www.postgresql.org/docs/current/view-pg-prepared-statements.html (includes
protocol-level statements; `from_sql` distinguishes) · [2]
https://www.postgresql.org/docs/current/view-pg-cursors.html (`is_holdable`) · [3]
https://www.postgresql.org/docs/current/functions-info.html (`pg_listening_channels()`) · [4]
https://www.postgresql.org/docs/current/view-pg-locks.html (`locktype = 'advisory'`) · [5]
https://www.postgresql.org/docs/current/view-pg-settings.html (`source = 'session'`) · [6]
https://www.postgresql.org/docs/current/functions-info.html (`pg_my_temp_schema()`, 0 if none) ·
[7] same page (`current_user`, `session_user`). These URLs are the standard PostgreSQL doc
locations; I did not fetch each page in this run.

What it **covers**: every item on the assessment's session-state list, however it was
created (statement, `set_config`, function body, `UPDATE pg_settings`), because it reads the
resulting state, not the text.
What it **misses** (VERIFIED where noted, otherwise from reading the probe's semantics):
`role`/`session_authorization` are `GUC_NO_SHOW_ALL` and **absent from `pg_settings`**
(PostgreSQL `guc_parameters.dat:2594-2596`, `guc_funcs.c:921-923`) — hence item [7];
sequence session state (`currval`/`lastval`) has no view; `setseed()` random state; `LOAD`ed
libraries and C-extension static state; plan cache (`DISCARD PLANS`, harmless); a backend
whose GUC came from `ALTER ROLE … SET` shows source `user`, not `session` (correct to ignore).
It costs one round trip per lease — the same as an unconditional `DISCARD ALL`, which already
clears all of the above except `LOAD` and `setseed`. So the probe's value is **diagnostic and
for metrics** ("how often were leases dirty, with what"), and as a check *after* `DISCARD ALL`
in strict mode; as a replacement for the reset it is not cheaper.

**M4 — Fold the check into the client's own round trip.** A proxy can append the probe (or a
narrow version of it) to the client's simple Query as an extra statement and strip the extra
reply, like option (c2) strips `BEGIN READ ONLY`/`COMMIT`. This makes "known clean" an
observed fact per lease at no extra round trip. Problems: it runs inside the client's
implicit transaction (an error in the client's statement skips it — then treat as dirty), it
lengthens every reply, and it must not run if the client left a transaction open (status
`T`/`E` — then the lease is not ending anyway). Not in either project; my inference.

**M5 — Server-side GUC hooks via an extension.** YugabyteDB's mechanism is a patch to
`guc.c`/`xact.c`. On vanilla PostgreSQL an extension can run code at transaction end
(`RegisterXactCallback`) and could compute the same flags and expose them, e.g. through a
custom `GUC_REPORT` variable whose value changes when the session becomes dirty — making it
arrive as an ordinary `ParameterStatus`. I did not find such an extension; COULD NOT FIND.
It would need C, installation on the server, and superuser — against the project's "vanilla
Postgres" premise, but it is the only route to YugabyteDB-grade signals without forking.

**Takeaway for mini-gateway.** Both systems needed server cooperation for exactness. The
parts that survive on unmodified PostgreSQL are: the ReadyForQuery status byte (already used,
decision D4), `ParameterStatus` for the 15 `GUC_REPORT` settings (watch them; treat change
as dirty), and catalog probes (M3), which cover the assessment's whole session-state list
except role (needs [7]) and sequence state. Combined with `DISCARD ALL` on any lease not
proven clean, that is option (b) with a server-observed "known clean" instead of a text taint
list.

## Could not verify

- **CockroachDB, holdable cursors on migration.** `serializeSessionState`
  (`session_state.go:55-110`) checks transactions, active portals and temp schemas; I found no
  check of `sqlCursors` (grep `cursor` in `session_state.go`: no hits). Holdable cursors
  persist past commit (`sql_cursor.go:434-436`). Whether a session with a WITH HOLD cursor
  migrates and silently loses it, or is blocked elsewhere, I could not determine without
  running it.
- CockroachDB: no `CLOSE ALL` in `DISCARD ALL` (`discard.go`); not checked whether cursors are
  closed by another path.
- CockroachDB: how the grammar is kept in step with PostgreSQL releases — no tooling found;
  "by hand" is an inference from the 2015 copyright line and per-feature `unimplemented` stubs
  (119 occurrences of `unimplemented` in `sql.y`).
- CockroachDB `cockroachdb-parser`: whether "archived" means discontinued or just read-only
  between automated snapshots — the last snapshot commit is 2026-04-30 (v26.2.0); the README
  does not say.
- YugabyteDB: how the PostgreSQL fork is rebased onto new PostgreSQL releases (depth-1 clone,
  no history read).
- YugabyteDB: defaults of `yb_conn_mgr_*_permanent_sticky` and of
  `ysql_conn_mgr_optimized_session_parameters` in code (docs say the latter defaults `true`).
- YugabyteDB: the full `'G'` packet format (`yb_send_set_guc_defaults`) and
  `yb_check_reset_needed` were not read line by line.
- YugabyteDB docs: the troubleshooting page wording "To offer complete correctness/range of
  support" was read from a web fetch; I did not find a stronger completeness claim.
- The PostgreSQL doc URLs in M3 were cited from their standard locations, not fetched.
- Better candidate project: none met. (Odyssey upstream is covered by the YugabyteDB fork's
  base; I did not read upstream Odyssey separately.)
