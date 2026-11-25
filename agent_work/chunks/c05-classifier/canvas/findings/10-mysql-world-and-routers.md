# ProxySQL, Vitess, Citus, Postgres-XL — MySQL-world analogues and in-server routers


Line numbers below refer to the commits in the Sources table. "VERIFIED IN CODE" means
read in the clone at that commit; "DOCS ONLY" means the project's own documentation was
read and the code behind it was not; "COULD NOT FIND" means looked for and not found.

## Sources

| Project | URL | Commit SHA | Commit date | Newest release | Local/web |
| --- | --- | --- | --- | --- | --- |
| ProxySQL | https://github.com/sysown/proxysql | e1a7dc6c222df89439dffb82e545aae4b845c5a4 | 2026-11-24 | v3.0.12 (GitHub "latest", 2026-11-05); tags up to v4.0.12 exist | LOCAL SOURCE (`repos/proxysql`, sparse: `lib include doc`) + docs pages |
| Vitess | https://github.com/vitessio/vitess | 84d5f2df80a90fa79481ead850a9748e4fd85be3 | 2026-11-24 | v24.0.5 (2026-10-13) | LOCAL SOURCE (`repos/vitess`, sparse: `go/vt/vtgate go/vt/sqlparser go/vt/vttablet/tabletserver go/pools go/vt/sysvars go/mysql`) |
| Citus | https://github.com/citusdata/citus | 5290ba9e3fb5d885fd6031fed866e1b74cb2c0fa | 2026-11-25 | v14.3.0 (2026-10-23) | LOCAL SOURCE (`repos/citus`, sparse: `src/backend/distributed src/include/distributed`) |
| Postgres-XL | https://git.postgresql.org/git/postgres-xl.git (code via mirror https://github.com/pharosnet/postgres-xl) | mirror b715468dded41b032b79645a4bc7d1d70c72aabd | mirror 2017-08-22; official last change 2018-10-12 | XL_10_R1_1 (~2018) | WEB READS (gitweb summary, `gh api` file reads) |

Docs read (saved copies in `work/10-mysql-world-and-routers/`):
https://proxysql.com/documentation/multiplexing/ ,
https://proxysql.com/documentation/proxysql-read-write-split-howto/ ,
https://proxysql.com/documentation/query-cache/ , and `doc/pgsql-set-config-tracking.md` in the ProxySQL clone.

## Summary rows

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| ProxySQL (PG side) | Hand tokenizer → digest; prefix/substring tests; RE2 for `SET`; optional hand parser (off) | Taint after execution → pin to client; reset (`DISCARD ALL` / `COM_CHANGE_USER`) or close at client disconnect; 19 tracked settings replayed per connection; unknown `SET` → pin; `LISTEN` pinned from CommandComplete tag | User regex rules for routing; opt-in TTL cache; no coalescing | Deny-list for taint; allow-list for replayable settings | Cross-client leak of session state (connections returning clean are not reset) | Open for untainted text (`set_config` by default, `SELECT f()`, `SELECT 1; SET ...`, digests off, >65000 bytes); closed for unknown `SET` |
| Vitess | Own full MySQL grammar (goyacc, 9.7k lines) | Emulate in vtgate; sysvar allow-list by class; per-statement `SET_VAR` hints; settings pool (connections tagged by interned settings, reset/apply on mismatch); reserved (pinned, closed at end) for temp tables / `GET_LOCK`; discard after `CALL` | Plan type from AST; consolidator on by default for SELECT/SHOW outside transactions, keyed by SQL text | Allow-list (unknown variable rejected) | Opaque code (stored function) leaks; consolidation shares volatile results | Closed (parse error, unknown variable) except partial DDL |
| Citus | PostgreSQL's parser + analyser + catalog (hooks) | Not needed: worker connections per client backend; only `SET LOCAL` propagated, opt-in | Query tree + `func_volatile()`; unsupported → error | Catalog lookups | Error before execution | Closed |
| Postgres-XL | PostgreSQL's parser (fork) | Track every GUC change in `set_config_option`, replay `SET`s on each new pooled datanode connection, `RESET ALL` on release, destroy if temp objects | Planner | — | — | — (dormant since 2018) |

## ProxySQL

C++, GPL-3.0, first released 2013–2015 (from memory; not re-checked), very active: the
clone's HEAD is from 2026-11-24 and the latest GitHub release is v3.0.12 (2026-11-05). It
has a MySQL side (`lib/MySQL_*.cpp`, `lib/mysql_connection.cpp`) and, since 3.x, a
PostgreSQL side (`lib/PgSQL_*.cpp`). Both share one design: **multiplexing (transaction
pooling) is ON by default, and a per-backend-connection bit set records "this connection
now carries session state"; while any bit is set the connection stays bound to its client
("multiplexing disabled"); when the client disconnects the connection is reset or closed.**
The bits are set by substring and prefix matches on a normalised query text.

### 1. Does it read SQL? With what?

- **A hand-written tokenizer that produces a "digest".** `lib/c_tokenizer.cpp` (2,812 lines,
  MySQL) and `lib/pgsql_tokenizer.cpp` (3,789 lines, PostgreSQL) strip comments, replace
  literals with `?`, collapse whitespace. VERIFIED IN CODE (`Query_Processor.cpp:3631`
  `qp_digest_legacy`, calling `mysql_query_digest_and_first_comment` / `pgsql_query_digest_and_first_comment`).
  The PG tokenizer knows dollar quotes (`pgsql_tokenizer.cpp:501–520`) and `E'` prefixes
  (`:604`). I searched it for handling of string continuation across a newline and COULD
  NOT FIND any.
- **Every session-state decision is a `strncasecmp` (prefix) or `strcasestr` (substring) on
  that digest**, run in `ProcessQueryAndSetStatusFlags`. VERIFIED IN CODE. MySQL side,
  `lib/mysql_connection.cpp:3241–3318`:
  ```c
  if (!strncasecmp(query_digest_text,"PREPARE ", strlen("PREPARE "))) {
      set_status(true, STATUS_MYSQL_CONNECTION_PREPARED_STATEMENT);
  ...
  if (!strncasecmp(query_digest_text,"CREATE TEMPORARY TABLE ", strlen("CREATE TEMPORARY TABLE "))) {
  ...
  if (strcasestr(query_digest_text,"GET_LOCK(")) {
      set_status(true, STATUS_MYSQL_CONNECTION_GET_LOCK);
  ...
  if (strcasestr(query_digest_text,"SQL_CALC_FOUND_ROWS")) {
  ```
  User variables: `index(query_digest_text,'@')` → `STATUS_MYSQL_CONNECTION_USER_VARIABLE`
  unless the statement is `SELECT` of only names listed in `mysql-keep_multiplexing_variables`
  (default `"tx_isolation,transaction_isolation,version"`, `MySQL_Thread.cpp:1565`;
  `IsKeepMultiplexEnabledVariables`, `mysql_connection.cpp:2983`). That is **"taint by
  substring → pin"** exactly: a `@` anywhere outside a literal pins the connection.
  PostgreSQL side, `lib/PgSQL_Connection.cpp:5814–5992`, same shape:
  ```c
  if (!strncasecmp(query_digest_text, "CREATE TEMPORARY TABLE ", ...) ||
      !strncasecmp(query_digest_text, "CREATE TEMP TABLE ", ...)) {
      set_status(true, STATUS_PGSQL_CONNECTION_TEMPORARY_TABLE);
  ...
  if (!strncasecmp(query_digest_text, "SELECT pg_advisory_lock", sizeof("SELECT pg_advisory_lock")-1) ||
      !strncasecmp(query_digest_text, "SELECT pg_try_advisory_lock", ...)) {
      set_status(true, STATUS_PGSQL_CONNECTION_ADVISORY_LOCK);
  ```
  plus `PREPARE `, `LISTEN` (per statement, `update_listen_status`, `:5774`), `CURSOR WITH
  HOLD` (substring, `:5932`), `CREATE [TEMP|TEMPORARY] SEQUENCE`, `LOCK TABLE`/
  `pg_advisory_xact_lock`/`SAVEPOINT` inside a transaction. Flags are cleared by
  `DISCARD TEMP`, `SELECT pg_advisory_unlock_all`, `CLOSE ALL`, `UNLISTEN *`,
  `DISCARD SEQUENCES` (same function).
- **The PG `IsKeepMultiplexEnabledVariables` is a stub**: `PgSQL_Connection.cpp:6254`
  `return true; /* TODO: fix this */`, so on the PG side the "`.` in a non-`SET`
  statement" check never fires. VERIFIED IN CODE.
- **An RE2 regular expression parses `SET`.** `lib/PgSQL_Set_Stmt_Parser.cpp` (243 lines;
  one large pattern at `:56`); MySQL's `MySQL_Set_Stmt_Parser.cpp` is 917 lines. VERIFIED IN CODE.
- **An optional hand-written SQL parser, "ParserSQL"** (vendored under `deps/parsersql`,
  adapter `lib/Query_Processor_ParserSQL.cpp`), used when `*-query_processor_parser=1` or
  `*-set_parser_algorithm=3`. Defaults are `query_processor_parser=0`,
  `set_parser_algorithm=2` (`PgSQL_Thread.cpp:1166–1168`). VERIFIED IN CODE. It is not the
  PostgreSQL grammar; nothing ties it to PostgreSQL releases.
- **Where the detection runs, and when**: in `RequestEnd`, i.e. *after* the statement has
  executed and its reply has come back (`MySQL_Session.cpp:11073–11095`; PG:
  `PgSQL_Session.cpp:6981`). Detection does not gate execution; it decides only what happens
  to the connection afterwards. VERIFIED IN CODE.
- **Blind spots in the digest itself** (VERIFIED IN CODE): if `*-query_digests=false` there
  is no digest (`Query_Processor.cpp:3679`) and `ProcessQueryAndSetStatusFlags` returns at
  once on `NULL` (`mysql_connection.cpp:3243`, `PgSQL_Connection.cpp:5815`) — every flag is
  off; the tokenizer only reads the first `query_digests_max_query_length` bytes (default
  65000, `MySQL_Thread.cpp:1441`; `c_tokenizer.cpp:424 get_digest_max_len`), so a
  `GET_LOCK(` after byte 65000 is invisible. The PG side added one protocol-based backstop
  for `LISTEN` because of the first gap (see below).

### 2. Session-state safety: mechanism

**Taint → pin for the client's lifetime → reset (or close) when the client leaves.**
VERIFIED IN CODE.

- *Pin*: `MultiplexDisabled()` ORs the flags (`mysql_connection.cpp:2969`,
  `PgSQL_Connection.cpp:6229`). Every "return the connection to the pool at the end of a
  transaction" site checks it, e.g. `MySQL_Session.cpp:6915`:
  `if (mysql_thread___multiplexing && reusable && !IsActiveTransaction() && !MultiplexDisabled()) { ... return_MySQL_Connection_To_Pool(); }`.
  Otherwise the connection stays attached to the session.
- *Reset on client disconnect*: `MySQL_Backend::reset()` → `MySQL_Data_Stream::reset_connection()`
  (`mysql_data_stream.cpp:2202`): a clean connection goes back to the pool as is; a pinned
  one goes to `destroy_MyConn_from_pool`, which — if healthy and the reset queue has room
  (`connpoll_reset_queue_length`, default 50) — queues it for `COM_CHANGE_USER`
  (`MySQL_HostGroups_Manager.cpp:3058–3078`, `mysql_change_user_start` at `:279`) and
  otherwise closes it. On PostgreSQL, `PgSQL_Data_Stream::reset_connection()` (`:1746`) →
  `destroy_MySQL_Connection_From_Pool(true)` → `create_new_session_and_reset_connection`,
  which sends `ROLLBACK` if a transaction is open and then **`DISCARD ALL`**
  (`PgSQL_Connection.cpp:5283–5292`); `PgSQL_Connection::reset()` then zeroes the flags
  and tracked variables (`:6168`). A client that connected with untracked startup
  `options=` gets its connection closed, not reset (`PgSQL_Data_Stream.cpp:1768`).
- *A clean connection is NOT reset between clients*: `return_MySQL_Connection_To_Pool`
  (`PgSQL_Data_Stream.cpp:1665`) pushes it to the pool with no reset (a reset happens only
  for "too many prepared statements" or expiry). So any session change the text scan did
  not flag travels to the next client. VERIFIED IN CODE.
- *Track and replay for a known list of settings*: a `SET` whose digest matches the
  tracked-variables regex is parsed (`handler___status_WAITING_CLIENT_DATA___STATE_SLEEP___handle_SET_command`,
  `PgSQL_Session.cpp:5238`), the value is validated and stored as the *client's* value;
  before each query `PgSQL_Variables::verify_variable` (`PgSQL_Variables.cpp:272`) compares
  a per-variable hash on the client side with the hash on the chosen backend connection
  and, on a difference, sends `SET` to that backend (`update_server_variable`, `:284`).
  The tracked list is 19 settings (`include/proxysql_structs.h:2030–2054`:
  `client_encoding, DateStyle, IntervalStyle, standard_conforming_strings, TimeZone,
  search_path, allow_in_place_tablespaces, bytea_output, client_min_messages,
  enable_{bitmapscan,hashjoin,indexscan,nestloop,seqscan,sort}, escape_string_warning,
  extra_float_digits, maintenance_work_mem, synchronous_commit`). This is an
  **allow-list of replayable settings**; it is the same idea as Vitess's settings pool
  but the state lives on the client and is pushed to whichever connection it gets.
- *Unknown `SET` → lock on hostgroup (pin)*: `PgSQL_Session.cpp:5414–5420`
  (`if (unknown_var) { unable_to_parse_set_statement(lock_hostgroup); ...}`), also a
  multi-statement query that starts with `SET` (`is_multi_statement_command`, `:5150`), a
  `SET TIME ZONE INTERVAL ...` (`:5252`), and an unparseable `SET` (`:5302`). Then
  `ProcessQueryAndSetStatusFlags` turns `locked_on_hostgroup` into the
  `USER_VARIABLE` bit (algorithm 1, `mysql-/pgsql-set_query_lock_on_hostgroup=1`, the
  default: `PgSQL_Thread.cpp:1167`, `MySQL_Thread.cpp:1465`). **Exception that fails
  open**: a `SET` of a tracked variable that the parser cannot read is forwarded *without*
  a lock (`PgSQL_Session.cpp:5423–5440`, comment: "Forward to the backend without locking
  the hostgroup"). VERIFIED IN CODE.
- *`set_config()`*: `doc/pgsql-set-config-tracking.md`: "With the default settings
  (`pgsql-set_parser_algorithm=2`, `pgsql-query_processor_parser=0`) `set_config()` is
  forwarded as an ordinary query, untracked and without a lock, as in earlier versions."
  The code agrees: `set_config_tracking_on()` (`PgSQL_Session.cpp:72`) gates
  `handle_set_config` (`:5893`). When enabled, it scans the *raw text* for the words
  `set_config`/`pg_settings` and `DO` (`pgsql_text_find_words`, tokenizer bits
  `PGSQL_SCAN_*`, `include/c_tokenizer.h:93–97`), classifies the call with ParserSQL, and
  tracks it or locks; with `standard_conforming_strings=off` and a backslash in the query
  "any mention locks" (`:5906–5913`). VERIFIED IN CODE.
- *A protocol signal, not text*: a `LISTEN` pins from the backend's **CommandComplete tag**
  `LISTEN` (`PgSQL_Connection.cpp:3757–3765`, issue #6438 cited in the comment: "the tag is
  there whatever the query looked like, including when pgsql-query_digests is off"), and
  `NotificationResponse` messages arriving on a connection not flagged LISTEN are
  discarded rather than leaked to the current client (`:3741–3755`). VERIFIED IN CODE.
  ParameterStatus messages from the backend during a query are passed through to the
  client (`:3959–3961`) and are NOT used to update the tracked variables or to pin: COULD
  NOT FIND any such use.

### 3. Read/write and share decisions

- **Read/write split is not automatic.** Routing is by user-written query rules
  (`mysql_query_rules`/`pgsql_query_rules`) whose `match_digest`/`match_pattern` are
  regular expressions over the digest or the raw text. The docs' generic example is
  `'^SELECT.*FOR UPDATE'` → primary, `'^SELECT'` → replicas, with the warning: "Generic
  `SELECT` routing rules cause problems with transactions, session state, and consistency.
  Analyse your workload first and create targeted rules instead."
  (https://proxysql.com/documentation/proxysql-read-write-split-howto/). DOCS ONLY for the
  routing semantics; the rule engine itself (`Query_Processor.cpp`) was not read in depth.
  Cost of a miss: a write sent to a replica fails with the server's read-only error, or a
  read sees replica lag. List type: whatever the user writes.
- **Query cache is opt-in per rule with a TTL** (`cache_ttl` on a rule). "Writes do not
  invalidate stored results. A cached answer can remain visible until its TTL expires"
  (https://proxysql.com/documentation/query-cache/, Known Limitations). DOCS ONLY. Cost of a
  miss: stale or wrong data, by design bounded by the TTL. There is no in-flight
  coalescing of identical reads that I found (COULD NOT FIND).
- Multiplex control per rule: `multiplex` = 0 (disable), 1 (enable), 2 ("do not disable
  multiplex for this specific query containing `@`") — the escape hatch for false
  positives of the substring test (docs, multiplexing page; code: `mul` in
  `ProcessQueryAndSetStatusFlags`, `mysql_connection.cpp:3247–3258`).

### 4. Documented limitations (own words)

From https://proxysql.com/documentation/multiplexing/:
- "All queries that have `@` in their query_digest will disable multiplexing, and it will
  never be enabled again."
- "If `CREATE TEMPORARY TABLE` is executed, multiplexing is disabled and is never enabled
  again on the connection." and "disabling multiplexing doesn't disable routing, so it might
  happen that after a `CREATE TEMPORARY TABLE` is executed, a `SELECT` query on the same
  table returns a ... doesn't exist error".
- "If `GET_LOCK()` is executed, multiplexing is disabled and is never enabled again."
- "Session variables set on a frontend connection are not automatically issued to a backend
  connection. ProxySQL maintains a set of `mysql-default_XXX` global variables ... ProxySQL
  assumes that this is the default value set on all backends".
- `LAST_INSERT_ID()` needs `mysql-auto_increment_delay_multiplex` (default 5): "multiplexing
  is disabled on a connection whenever ProxySQL encounters an OK packet with last inserted
  identifier set" — another protocol-signal pin.

### 5. Language, licence, weight

C++ (GPL-3.0), single large daemon with SQLite admin, RE2, libpq/MariaDB client, its own
tokenizers. Nothing is reusable from Go except as design: the tokenizer and regexes are
interwoven with the session state machine.

### Probe table (PostgreSQL side, default configuration)

| Probe | What ProxySQL does | Where |
| --- | --- | --- |
| P1 `SET search_path = x` | Parsed by RE2; tracked variable → stored as the client's value, connection stays multiplexed; replayed by `SET` on whichever backend the client gets next | `PgSQL_Session.cpp:5238`, `PgSQL_Variables.cpp:272–295` |
| P2 `SELECT set_config('search_path','h',false)` | Default: forwarded untracked, no lock, no reset → the next client of that backend inherits `search_path`, and ProxySQL's own hash for that backend is now wrong. With `set_parser_algorithm=3`: tracked like `SET` | `doc/pgsql-set-config-tracking.md`; `PgSQL_Session.cpp:72, 5893` |
| P3 `SELECT f()` (f runs SET / creates temp table) | Not detected; connection returns to pool unreset → leak | no matching flag in `ProcessQueryAndSetStatusFlags`; `PgSQL_Data_Stream.cpp:1665` |
| P4 lexical tricks | Flags match the digest; if the tokenizer and PostgreSQL disagree on a string's end, flags miss. Continuation handling: COULD NOT FIND. Not run. | `pgsql_tokenizer.cpp` |
| P5 `PREPARE` / `LISTEN` / `CREATE TEMP TABLE` / `SELECT pg_advisory_lock(1)` / `DECLARE ... WITH HOLD` | All pin (prefix / substring / CommandComplete tag). But only when the digest *starts* with `SELECT pg_advisory_lock`: `SELECT 1, pg_advisory_lock(1)` does not pin; `CREATE TEMP VIEW`, `SELECT ... INTO TEMP` are not on the list | `PgSQL_Connection.cpp:5862–5945, 3757` |
| P6 `SELECT 1; SET search_path = x` | Digest starts with `SELECT` → SET handler not entered, `SET ` prefix test fails → not tracked, not pinned → leak (LISTEN and WITH HOLD are scanned per statement; the rest are not) | `PgSQL_Session.cpp:6155`, `PgSQL_Connection.cpp:5834` |
| P7 nextval / writable CTE / FOR UPDATE / now() / view / EXPLAIN ANALYZE DELETE | No automatic decision; only user regex rules and per-rule cache TTL. `SELECT nextval(...)` with a `^SELECT` rule goes to a replica and errors; with a `cache_ttl` rule it is served stale | docs |

## Vitess

Go, Apache-2.0, CNCF graduated (from memory; not re-checked); very active (HEAD 2026-11-24; v24.0.5 released
2026-10-13). Two server roles matter here: **vtgate** (the MySQL-protocol proxy clients talk
to; holds the client session) and **vttablet** (one per MySQL; owns the connection pools).

### 1. Does it read SQL? With what?

A **full MySQL grammar** of its own: `go/vt/sqlparser/sql.y` is 9,747 lines of goyacc,
generating `sql.go` (27,780 lines), with a hand-written tokenizer in the same package.
VERIFIED IN CODE (`wc -l`). It is not MySQL's parser; it is kept in step with MySQL by the
Vitess maintainers adding syntax as needed. Every query is parsed in vtgate
(`executor.go:1895` `parser.Parse2(query)`); a parse failure is returned to the client
as an error (`sqlparser/parser.go:76–92`, `vterrors.New(... INVALID_ARGUMENT, ...)`), with one
exception: a DDL that parses only partially is passed through as "not fully parsed"
(`:79–90`). Multi-statement text is split by `SplitStatementToPieces`
(`plugin_mysql_server.go:1237`) and each piece planned. VERIFIED IN CODE.

### 2. Session-state safety: six mechanisms, chosen by AST node type

1. **Emulate the state in vtgate, never send it.** User variables (`SET @x = ...`) become
   `engine.UserDefinedVariable` held in the vtgate session (`planbuilder/set.go`, `case
   sqlparser.VariableScope`) and are substituted as bind variables. Text-protocol
   `PREPARE/EXECUTE/DEALLOCATE` are planned by vtgate itself (`planbuilder/builder.go:246`).
   Docs: "when a user sets or evaluates a user defined variable, the `vtgate` will rewrite
   the query so that it does not actually do anything with user variables. Instead, it keeps
   the state in the Vitess layer." (https://vitess.io/docs/24.0/reference/query-serving/reserved-conn/).
   VERIFIED IN CODE for the plan types; the rewriting itself was not read line by line.
2. **An explicit allow-list of system variables, by class** (`go/vt/sysvars/sysvars.go`,
   352 lines). Classes: `VitessAware` (19 variables vtgate implements itself, e.g.
   `autocommit`, `sql_select_limit`, `transaction_read_only`, `workload`, `query_timeout`),
   `ReadOnly`, `IgnoreThese`, `NotSupported`, `UseReservedConn` (about 45: e.g.
   `foreign_key_checks`, `group_concat_max_len`, `optimizer_switch`, `sort_buffer_size`),
   `CheckAndIgnore` (about 35). Each entry also says whether MySQL accepts it in a
   per-statement `SET_VAR` hint (`SupportSetVar`). **An unknown name is rejected**:
   `planbuilder/system_variables.go:105–108`
   ```go
   pf, ok := pc.funcs[name]
   if !ok {
       return nil, vterrors.VT05006(sqlparser.String(expr))
   }
   ```
   and operators can deny more with `--denied-system-variables` (`executor.go:1764`).
   VERIFIED IN CODE. This is fail-closed.
3. **Carry settings per statement instead of per connection.** For a `UseReservedConn`
   variable vtgate first asks MySQL whether the value differs from the current one
   (`engine/set.go:382`: `select %s from dual where @@%s != %s`), stores the changed value
   in the vtgate session, and — if MySQL ≥ 8.0, the variable supports it, and the value
   cannot break out of a comment — sends it on every later statement as an optimizer hint
   `/*+ SET_VAR(name = value) */` (`executorcontext/vcursor_impl.go:269–311`
   `PrepareSetVarComment`; `sysvars.go` `IsSafeSetVarValue`: `!strings.Contains(value, "*/")`).
   Statements that cannot carry a hint mark the session as needing a reserved connection
   (`CheckForReservedConnection`, `vcursor_impl.go:1150–1173`). VERIFIED IN CODE.
4. **The settings pool: connections tagged by the settings applied to them.** Otherwise the
   session's settings travel to vttablet as a list of `SET` strings with each request
   (`settings []string` in `TabletServer.execute`, `tabletserver.go:999, 1037–1041`), and
   the tablet gets a connection *whose settings match*:
   - `QueryEngine.GetConnSetting` (`query_engine.go:559–581`) concatenates the strings,
     looks them up in a cache, and on a miss parses them with the Vitess parser and builds
     **one apply query and one reset query** (`planbuilder.BuildSettingQuery`,
     `plan.go:429`: each `SET x = v` gets a reset `SET x = DEFAULT`, or the explicit global
     value for `foreign_key_checks`/`unique_checks` because of a MySQL bug, `:466–474`). The
     result is an **interned** `*smartconnpool.Setting` (`pools/smartconnpool/settings.go`):
     ```go
     type Setting struct {
         queryApply string
         queryReset string
         bucket     uint32   // globalSettingsCounter.Add(1)
     }
     ```
     Identity is pointer identity: the same settings text always yields the same pointer.
   - The pool (`pools/smartconnpool/pool.go`) keeps one `clean` stack and eight `settings`
     stacks (`stackMask = 7`, `:141–149`). On return a connection is pushed onto `clean` if
     `Conn.Setting() == nil`, else onto `settings[setting.bucket & 7]` (`tryReturnConn`,
     `:515–546`).
   - `getWithSetting` (`:781–853`): pop from this setting's stack; else a clean
     connection; else open a new one; else steal from another setting stack; else wait.
     Then:
     ```go
     connSetting := conn.Conn.Setting()
     if connSetting != setting {
         if connSetting != nil {
             err = conn.Conn.ResetSetting(ctx)   // runs queryReset
             if err != nil { conn.Close(); err = pool.connReopen(...) ... }
         }
         if err := conn.Conn.ApplySetting(ctx, setting); err != nil { // runs queryApply
             conn.Close(); pool.closedConn(); return nil, err
         }
     }
     ```
     A plain `get` (no settings) prefers `clean` and resets a tagged connection it has to
     take (`:719–770`). `ApplySetting`/`ResetSetting` execute the two strings and update the
     tag (`connpool/dbconn.go:494–509`). Any failure closes the connection. VERIFIED IN CODE.
   - The flag `--queryserver-enable-settings-pool` that the brief names: I searched
     `tabletenv` and `tabletserver` for it and COULD NOT FIND it at this commit; the pool is
     used whenever `settings` is non-empty. The docs: "`SET` statements used to cause use of
     `reserved connections`. This is no longer the case with the new connection pool
     implementation used by vttablet. The connection pool now tracks connections with
     modified settings instead of pinning connections to specific client sessions." (same URL).
5. **Reserved connections (pinning) for what cannot be tagged.** The tablet plan marks
   `NeedsReservedConn` for `GET_LOCK` found by an AST walk (`planbuilder/plan.go:408–425`
   `lockFuncs`, matching `*sqlparser.LockingFunc` nodes) and for `CREATE TEMPORARY TABLE`
   (`builder.go:255`). Running such a plan on a pooled connection fails with "not allowed
   without reserved connection" (`query_engine.go:120–139`); `ReserveExecute` catches that
   exact error and retries on a newly reserved connection (`tabletserver.go:1826–1857`).
   A reserved connection is `Taint()`ed out of the pool (`smartconnpool/connection.go`:
   `Taint` sets `pool = nil`; `Recycle` then closes it) and is **closed, not reset**, when
   the vtgate session ends. Docs: "Note that removing the temporary table is not enough to
   reset the connection." VERIFIED IN CODE (taint/close) and DOCS (lifecycle).
6. **Discard after opaque code.** After a `CALL` the pooled connection is discarded
   (`query_executor.go:1686–1700` `discardPooledConnAfterCall`), and a reserved/transaction
   connection that ran a `CALL` is marked `MarkSessionDiverged` and discarded on release
   (`:1706–1715`, `stateful_connection.go:317–325`): "A procedure body can leave session
   state behind that the statement's plan cannot see (a SET SESSION, SET NAMES, a temporary
   table)". VERIFIED IN CODE. I COULD NOT FIND the same treatment for a stored *function*
   called from a `SELECT`.

### 3. Read/write and share decisions

- **Routing** is by target tablet type chosen by the client (`keyspace@replica` etc.), and
  by plan type from the AST (`PlanSelect` vs DML). Not a text classifier. DOCS/CODE only
  partially read; not pursued further.
- **Coalescing exists and is on by default: the query consolidator.** `--enable-consolidator`
  defaults to `true` (`tabletenv/config.go:204`). `execSelect` (`query_executor.go:1340–1390`)
  runs for plan types `PlanSelect, PlanSelectImpossible, PlanShow, PlanSelectLockFunc` on a
  *pooled* (not transaction/reserved) connection (`:209–220`), keyed by the final SQL text
  without margin comments:
  ```go
  if qre.shouldConsolidate() {
      q, original := qre.tsv.qe.consolidator.Create(sqlWithoutComments)
      if original { defer q.Broadcast(); conn, err := qre.getConn() ... q.SetResult(res) }
  ```
  The decision is **plan type (from the full parser) + "not in a transaction"**; no
  function-volatility check: I COULD NOT FIND any test for `NOW()`, `RAND()` or
  `LAST_INSERT_ID()` before consolidating, so concurrent identical `SELECT NOW()` share one
  result. I also COULD NOT FIND `qre.setting` (the settings-pool tag) in the consolidation
  key; `getConn` uses it (`:1452`) but `Create` takes only the SQL text. If that reading is
  right, two sessions with different pooled settings (e.g. `time_zone`) issuing the same
  text at the same moment would share one result. Not tested. A miss costs wrong data for
  the waiters; Vitess accepts the volatile-function case by design (the shared result is one
  a concurrent execution could have produced). Per-request override:
  `ExecuteOptions.Consolidator` (`:120–133`).
- No result cache (plan cache only).

### 4. Documented limitations (own words)

https://vitess.io/docs/24.0/reference/query-serving/reserved-conn/: "it is better to use
reserved connections sparingly, because they reduce the effectiveness of the `vttablet`
connection pooling"; "Temporary tables ... It will continue to use the reserved connection
until the user disconnects. Note that removing the temporary table is not enough to reset
the connection."; "it is possible for there to be significantly more `vttablet` <-> MySQL
connections than the limit you set by sizing the `vttablet` connection pools."

### 5. Language, licence, weight

Go, Apache-2.0. `pools/smartconnpool` is about 1,700 non-test lines with a small `Connection`
interface (`ApplySetting`, `ResetSetting`, `Setting`, `IsClosed`, `Close`) and depends on
little beyond the standard library — the one piece a Go proxy could study or adapt
directly. The parser is MySQL-only and useless for PostgreSQL text.

### Probe table (MySQL analogues; Vitess has no PostgreSQL front end)

| Probe | What Vitess does | Where |
| --- | --- | --- |
| P1 session `SET` of a known variable | Classified by allow-list: emulated in vtgate, or carried as `SET_VAR` hint, or applied via a settings-pool connection tagged with that setting, or (no hint possible) reserved connection. Unknown variable → error VT05006 | `sysvars.go`, `system_variables.go:105`, `engine/set.go:381–423`, `smartconnpool/pool.go:781` |
| P2 setting changed through a function | n/a in MySQL form; nearest: an expression whose evaluation needs connection state is evaluated on a reserved connection (`engine/set.go:291–303`) | — |
| P3 `SELECT f()` where f changes session state | Not seen; pooled connection returns to the pool with the change. `CALL p()` instead → connection discarded | `query_executor.go:1686–1715` |
| P4 lexical tricks | The full grammar decides statement boundaries and node types; a mismatch with MySQL's lexer would be a parser bug, not a list gap. Unparseable → error (fail closed), except partial DDL | `sqlparser/parser.go:76–92` |
| P5 PREPARE / temp table / advisory lock | `PREPARE` emulated in vtgate; `CREATE TEMPORARY TABLE` and `GET_LOCK` → reserved connection, closed at session end | `builder.go:246`, tablet `builder.go:255`, `plan.go:408` |
| P6 several statements in one message | Split and planned one by one | `plugin_mysql_server.go:1237` |
| P7 reads for sharing | Consolidated if plan is a SELECT/SHOW on a pooled connection (statements inside a transaction or reserved connection take the `txConnExec` path, `:185–206`, not `execSelect`); no volatility check, so `SELECT nextval`-like or `NOW()` reads are shared among concurrent callers. How `FOR UPDATE` outside a transaction is planned: not checked | `query_executor.go:120–133, 209–220, 1340` |

## Citus

C extension to PostgreSQL, AGPL-3.0, actively developed (HEAD 2026-11-25; v14.3.0 released
2026-10-23). Runs inside every coordinator and worker backend.

**1. Reads SQL with the real PostgreSQL parser and analyser, plus the catalog.** Citus
installs `planner_hook = distributed_planner`, `ExecutorStart_hook = CitusExecutorStart`
and `ProcessUtility_hook = citus_ProcessUtility` (`shared_library_init.c:505, 515, 606`).
VERIFIED IN CODE. It never sees text it must interpret itself: it receives PostgreSQL's
analysed `Query` tree, with every function resolved to an OID, and asks the catalog about
it. Volatility is read from `pg_proc`, not guessed from names — `utils/citus_clauses.c:427–459`:
```c
return (func_volatile(func_id) == PROVOLATILE_VOLATILE);
...
if (IsA(node, NextValueExpr)) { /* NextValueExpr is volatile */ return true; }
```
The router planner (`planner/multi_router_planner.c`, 4,633 lines) uses it to refuse what it
cannot distribute safely, e.g. `:1400–1412` "functions used in UPDATE queries on distributed
tables must not be VOLATILE", `:827` "Router planner doesn't support VOLATILE functions in
common table expressions", and `contain_mutable_functions` (PostgreSQL's own) on
`RETURNING` and `ON CONFLICT` (`:722, 1320`). Keeping in step with PostgreSQL is free for the
parser (it is PostgreSQL's) and costly for the planner (Citus releases per PG major).

**2. Session state.** Two facts, both VERIFIED IN CODE. (i) Worker connections live in a
per-backend cache, `ConnectionHash` (`connection/connection_management.c:53, 128`, "citus
connection cache (host,port,user,database)"); a PostgreSQL backend serves one client, so
worker connections are never shared across clients and the cross-tenant problem does not
arise. (ii) Session-level settings are **not** propagated to workers; only `SET LOCAL`,
`SET TRANSACTION`, `RESET` and `RESET ALL` inside a multi-statement transaction, and only when
`citus.propagate_set_commands = 'local'`; the default is `none`
(`shared_library_init.c:2616–2622`, `commands/utility_hook.c:94, 625–636`,
`commands/variableset.c:41–78`). `IsSettingSafeToPropagate` is a five-entry deny-list
(`application_name, citus.propagate_set_commands, client_encoding, exit_on_error,
max_stack_depth`; `connection/remote_commands.c:1388–1408`). The `SET` is recognised as a
`VariableSetStmt` node, so spelling tricks do not apply.

**3. Read/write.** Decided from `Query->commandType`, row marks and modifying CTEs in the
analysed tree, with volatility from the catalog (code above). A miss is an error raised
before execution ("not supported"), i.e. fails closed. No coalescing or result cache found
(COULD NOT FIND).

**What being in the server buys**: the three layers of the explainer collapse. Lexical: the
server's own scanner. Grammar: node types, not keyword lists. Catalog: `func_volatile()` on
the resolved OID answers "does `f()` have side effects / differ per call" — the question a
proxy cannot answer from text. What it does *not* buy: `provolatile` is declared by the
function's author (a `STABLE` function that calls `set_config` is possible), and
volatility says nothing about session-state changes; Citus sidesteps the latter by never
sharing a worker connection between clients.

| Probe | Citus |
| --- | --- |
| P1 `SET search_path` | Applies on the coordinator only; not sent to workers by default |
| P2 `set_config(...)` | Same as P1 (not propagated; I COULD NOT FIND special handling) |
| P3 `SELECT f()` | `f` is resolved; volatility from `pg_proc`; side effects on the coordinator stay in that client's backend |
| P4 lexical tricks | n/a (PostgreSQL's scanner) |
| P5 session features | Belong to the client's own backend; worker connections are per backend |
| P6 multi-statement | Each statement arrives separately through the hooks |
| P7 nextval / writable CTE / FOR UPDATE / now() / view | From the tree: `NextValueExpr` volatile; `hasModifyingCTE`; `hasForUpdate` (`:820`); views expanded by the rewriter before planning |

## Postgres-XL (short; WEB READS)

Fork of PostgreSQL (MPL-2.0 per its site, from memory; not re-checked). Official repository
`https://git.postgresql.org/git/postgres-xl.git`: gitweb summary shows "last change Fri, 12 Oct
2018" and newest tag `XL_10_R1_1` ("7 years ago"), branch `XL_10_STABLE` — **dormant**
(https://git.postgresql.org/gitweb/?p=postgres-xl.git;a=summary, read 2026-11-25). gitweb then
rate-limited me (HTTP 429), so the code below was read from the GitHub mirror
`pharosnet/postgres-xl` (master, commit `b715468dded41b032b79645a4bc7d1d70c72aabd`,
2017-08-22) via `gh api` — older than the last official change; treat line numbers as
belonging to that mirror. Copies in `work/10-mysql-world-and-routers/xl-*.c`.

- **Parses with the real parser** (it *is* PostgreSQL; coordinators plan and ship plans or
  SQL to datanodes).
- **Session parameter propagation is hooked into the GUC machinery, not into statement
  text.** In `set_config_option` (`src/backend/utils/misc/guc.c:6081`), every change whose
  source is `PGC_S_SESSION` sets `send_to_nodes` (`:6093–6097`) and the new value is recorded
  with `PGXCNodeSetParam(local, name, value)` (`:6847–6856`) — so `SET`, `set_config()`
  (`set_config_by_name`, `:7698`, calls `set_config_option`) and anything else that changes
  a GUC are captured identically. VERIFIED IN CODE (mirror). `AtEOXact_GUC` (`:5239–5504`)
  records values restored at transaction end.
- **Replay on every new datanode connection**: `pgxcnode.c:400–410`, "We got a new
  connection, set on the remote node the session parameters if defined":
  `init_str = PGXCNodeGetSessionParamStr(); pgxc_node_set_query(handle, init_str);`
  — the string is `SET global_session TO ...;` plus one `SET` per recorded parameter
  (`:2568–2593`). VERIFIED IN CODE (mirror).
- **Reset before return to the shared pooler**: `pgxc_node_remote_cleanup_all`
  (`execRemote.c:1907–1919`): "Execute DISCARD ALL command on all allocated nodes to remove
  all session specific stuff before releasing them to pool for reuse by other sessions",
  though the command actually sent is `"RESET ALL;RESET SESSION AUTHORIZATION;RESET
  transaction_isolation;"`. A connection with temporary objects is destroyed rather than
  pooled (`poolmgr.c:1529–1545`, comment "If connection has temporary objects on it,
  destroy connection slot"). VERIFIED IN CODE (mirror) for the calls; the detection of
  "temporary objects" was not traced.
- Mapping: a server-side **track (at the GUC layer) + replay + reset-on-release** design.

## Claims of completeness

- **ProxySQL** does not claim completeness; its docs list conditions ("Conditions where
  multiplexing is disabled") and give per-rule overrides for false positives and negatives
  (`multiplex` = 0/1/2). Its PG `set_config` doc states plainly that by default
  `set_config()` "is forwarded as an ordinary query, untracked and without a lock". The code
  matches the docs: the list is a deny-list over digest text, the PG
  `IsKeepMultiplexEnabledVariables` is a `TODO` stub, and several probe spellings (P2 by
  default, P3, P6, `SELECT 1, pg_advisory_lock(1)`) are not caught. Its PG side has been
  growing the same way mini-gateway's classifier did — `standard_conforming_strings`
  handling, `DO` bodies, shadowable `set_config` via `search_path`, `UNLISTEN*` without a
  space — each a code comment citing an issue.
- **Vitess** claims transparency for session constructs it knows ("Vitess removes all query
  constructs that would normally need to change the state in the MySQL connection", reserved-conn
  doc) and lists the exceptions. The claim holds for what the parser sees; the code itself
  admits the limit for opaque code (the `CALL` comment: "can leave session state behind that
  the statement's plan cannot see") and answers it with a mechanism (discard), not with
  more reading.
- **Citus / Postgres-XL**: no claim needed; they are the server.

## Mapping to options (a) (b) (c1) (c2)

- **ProxySQL ≈ (a), with a mechanism behind it — but pin-on-taint, not reset-on-not-clean.**
  It reads text with a deny-list (as (a)), and backs the list with a reset at client
  disconnect. The crucial difference from (b): ProxySQL resets only connections it *knows*
  are dirty (deny-list → fails open), while (b) resets every lease it cannot show is clean
  (allow-list → fails closed). ProxySQL is ten years of evidence that "known dirty" stays
  incomplete; it supports (b)'s direction, not (a)'s.
- **Vitess ≈ (c1) plus mechanisms.** A real grammar (for MySQL), but safety comes from an
  allow-list of known variables (unknown → error), tagged connections, pinning, and discard
  after opaque code. It shows that even with a full parser the project added mechanisms
  for what parsing cannot see.
- **Citus, Postgres-XL ≈ none of the options**: they run inside PostgreSQL. They show what
  the "catalog layer" costs: you must be the server (or an extension in it).
- **(c2)** (let PostgreSQL enforce read-only): no project in this cluster does it. Vitess's
  `select ... from dual where @@x != value` is a related idea — ask the server instead of
  reasoning about text.

## Mechanisms not in the assessment

1. **Tagged connections (Vitess settings pool).** Instead of rejecting `SET` or wiping it,
   accept a *narrow, allow-listed* `SET name = literal` (say `search_path`, `TimeZone`,
   `DateStyle`, `statement_timeout`, `application_name`), keep the client's settings as an
   interned canonical tuple, and tag each pooled connection with the tuple it carries.
   On lease: prefer a connection with the same tag, else a clean one, else reset the other
   tag and apply this one; any failure closes the connection
   (`smartconnpool/pool.go:781–853`). In a Go proxy for vanilla Postgres: an interned
   `*Setting{apply, reset string}` (apply = `SET a=..; SET b=..`, reset = `RESET ALL` or a
   per-name `RESET`), a few stacks keyed by tag, and the round trip done before forwarding
   the client's statement — about 150–250 lines; `smartconnpool` is Apache-2.0 Go and could
   be read or adapted. It still needs (b) underneath: only the narrow `SET` shape is
   understood; everything else either is rejected or makes the lease "not known clean". It
   turns a class of today's rejections into supported features without widening what must
   be read.
2. **Protocol signals instead of text.** The backend says what ran. ProxySQL pins
   `LISTEN` from the CommandComplete tag (`PgSQL_Connection.cpp:3757–3765`) and discards
   stray `NotificationResponse`s. For mini-gateway: a Query reply's CommandComplete tags
   name each executed statement (`SET`, `PREPARE`, `LISTEN`, `DECLARE CURSOR`,
   `CREATE TABLE`, `DISCARD ...`), whatever the spelling, comments, quoting or
   multi-statement packing — this closes P4 and P6 for those statement kinds with no lexer.
   It does not see `set_config()`, functions, or whether a table was temporary (tag is just
   `CREATE TABLE` — so any `CREATE TABLE` tag should taint). ParameterStatus messages are a
   second signal: PostgreSQL emits one when a *reported* setting changes by any path,
   including `set_config()` and function bodies. Which settings are reported depends on the
   server version (I remember `search_path` being added to the reported set in a recent
   release, from memory; not verified here). Both signals arrive *after* execution, so they
   can only taint the lease (→ reset), not prevent; that is exactly what (b) needs. Cost:
   the reply tracker already parses every reply message up to ReadyForQuery; recognising
   `C` and `S` messages is tens of lines.
3. **Ask the server at release.** Vitess asks MySQL whether a value differs before
   recording it. The PostgreSQL analogue for (b)'s "known clean" test is a single
   catalog probe at lease end — e.g. non-default `pg_settings` with `source = 'session'`,
   `pg_prepared_statements`, `pg_listening_channels()`, `pg_my_temp_schema()`, holdable rows in
   `pg_cursors`, advisory locks in `pg_locks` for `pg_backend_pid()` (names from memory; not
   verified in this canvas). It costs the same round trip as `DISCARD ALL`, so it only pays
   off if `DISCARD ALL`'s side effects (lost plan cache, re-preparation) matter, or as a
   *verifier* in tests and chunk 14's measurements: run it after every lease in the fuzz
   harness to prove "no state crosses" independently of any text rule.
4. **Hooking the GUC layer (Postgres-XL)** catches every setting change regardless of
   spelling, because it sits where the value is set. A proxy cannot do this against vanilla
   Postgres; it would take an extension (`GUC check/assign hooks` are per-variable; a
   general hook does not exist in vanilla, from memory). Out of scope for mini-gateway,
   listed to close the question.
5. **Discard after opaque code (Vitess `CALL`).** A statement whose effect the reader
   cannot see (`CALL`, `DO`, any function not on the allow-list) taints the lease. This is
   already (b)'s taint idea; Vitess is a production precedent for it.
6. **Per-statement carriage of settings (Vitess `SET_VAR`).** MySQL can carry settings in
   a statement hint, so no connection state exists. PostgreSQL has no such hint; the
   nearest analogue is prefixing each forwarded transaction with `SET LOCAL ...` for the
   client's allow-listed settings, which PostgreSQL undoes at commit/rollback by itself.
   That gives per-client settings with *no* reset step for them, at the cost of an extra
   statement per transaction (and an implicit `BEGIN`/`COMMIT` wrap for single statements,
   as in (c2)). Not in the assessment; worth a measurement against option 1.

No better candidate project than those listed was met in this cluster.

## Could not verify

- ProxySQL: string-continuation handling in `pgsql_tokenizer.cpp` (COULD NOT FIND); the
  exact digest ProxySQL produces for probe P4 (not run; building not allowed); how query
  rules route `SELECT ... FOR UPDATE` on the PG side (docs only); any in-flight coalescing
  (COULD NOT FIND); first release date (from memory only); the size and grammar coverage of
  `deps/parsersql` (not checked out).
- Vitess: the `--queryserver-enable-settings-pool` flag (COULD NOT FIND at this commit);
  whether the consolidation key includes the settings-pool tag (COULD NOT FIND it in the
  key; not tested); handling of a stored function that changes session state (COULD NOT
  FIND any); how vtgate rewrites `@@var` reads (not read in detail).
- Citus: special handling of `set_config()` (COULD NOT FIND); whether any coalescing or
  cache exists (COULD NOT FIND).
- Postgres-XL: official repository code (gitweb returned HTTP 429); code read from a 2017
  GitHub mirror; licence (from memory); how "temporary objects on a connection" is detected
  before `release_connection` destroys the slot (not traced).
- PostgreSQL facts used in "Mechanisms not in the assessment" (which settings are
  `GUC_REPORT`, the catalog views named in item 3) are from memory and were not verified in
  this canvas; another canvas (12-postgres-server) may cover them.
