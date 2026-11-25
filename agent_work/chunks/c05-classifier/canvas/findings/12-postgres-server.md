# PostgreSQL itself — the server-side mechanisms a proxy can lean on

The question for this cluster: what does the PostgreSQL server itself offer, so that a proxy
in front of it does not have to read SQL to decide (D3) session state, (D4) read-only and
(D5) shareability? Short answer: the server will **reset** most session state for you
(`DISCARD ALL`, about one empty round trip) and will **tell you after the fact** which kind
of top-level command ran (the CommandComplete tag) and which reported settings changed
(ParameterStatus). It will **enforce** read-only only if the proxy, not the client, controls
where the transaction starts and ends. It will **refuse** some session features by privilege
(temp tables, `set_config`, advisory locks), but plain `SET`, `PREPARE`, `LISTEN` and
`DECLARE … WITH HOLD` have no privilege that can be revoked. It does not tell a client
whether a statement is volatile; that knowledge lives only in the planner.

Claim labels: **VERIFIED IN CODE** (cited file and line in the clone below), **VERIFIED BY
EXPERIMENT** (run against PostgreSQL 17.11; scripts and outputs in
`/root/scratch/c05-canvas/work/12-postgres-server/`), **DOCS ONLY**, **COULD NOT FIND**.
Line numbers refer to the master commit below. The experiments used 17.11. Where the two
could differ, this file says so.

## Sources

| Project | URL | Commit SHA | Commit date | Local / web |
| --- | --- | --- | --- | --- |
| PostgreSQL (master, `meson.build` version `20devel`) | https://github.com/postgres/postgres | `4483697be0e0d22b9b4fd847e8bf2d998c56a741` | 2026-11-25T17:31:36+01:00 | LOCAL SOURCE (depth-1 clone, `repos/postgres`) |
| PostgreSQL 17.11 server (Docker image `postgres:17`, throwaway container `c05canvas-pg`) | — | — | — | LOCAL EXPERIMENTS (`work/12-postgres-server/e*.sql`, `*.out`, `wire.py`) |
| Current docs | https://www.postgresql.org/docs/current/ (also `doc/src/sgml/` in the clone) | — | — | LOCAL (sgml in the clone) + WEB |
| pgsql-hackers / commitfest | links inline | — | — | WEB READS |

Newest release tag: not read (depth-1 clone has no tags). The tested server was 17.11.

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| PostgreSQL server (as a resource for a proxy) | The real parser (it is the parser). Exposes to a client only the **outcome**: CommandComplete tag per statement, ParameterStatus for 15 hard-wired GUCs, ReadyForQuery status | `DISCARD ALL` resets a fixed list (portals, session authorization/role, `RESET ALL`, prepared statements, `LISTEN`, session advisory locks, plan cache, temp relations, sequence caches); refused inside a transaction block, including the implicit block of a multi-statement Query | `BEGIN READ ONLY` / `default_transaction_read_only`: refuses writes to non-temp tables, DDL, `nextval` on non-temp sequences, large-object writes, `SELECT FOR UPDATE`. Allows `SET`, `set_config`, `PREPARE`, `LISTEN`, `NOTIFY`, advisory locks, writes to existing temp tables, `DECLARE WITH HOLD` | Hard-coded allow-list per node type (`ClassifyUtilityCommandAsReadOnly`) + executor permission check | Residue after `DISCARD ALL`: loaded libraries and their C state, the PRNG seed, custom-GUC placeholders, replication-origin session, PL interpreter globals. Read-only wrapper: a client `COMMIT` or `SET TRANSACTION READ WRITE` inside the same Query escapes it | Read-only check fails closed (unknown utility statement → `elog(ERROR)`; utility default is "not read-only"). `DISCARD ALL` is a fixed list: anything not on it survives, so it is open for extension state |

## PostgreSQL

### 1. Does it read SQL?

Yes: it is the reference parser (`src/backend/parser/gram.y`, `scan.l`). What matters for a
proxy is what the server *reports back* about the SQL it parsed. A client sees no parse tree.
It sees:

- **CommandComplete** for every statement of a simple Query, in order, even inside a
  multi-statement Query (VERIFIED BY EXPERIMENT, `wire.out`: `SELECT 1; SET search_path = x;
  SET LOCAL work_mem = '8MB'` → `SELECT 1`, `SET`, `SET`). The tag is built from the parse
  tree, not from the client's text (`src/backend/tcop/cmdtag.c` `BuildQueryCompletionString`,
  line 121). Spelling and lexical tricks therefore cannot change it. VERIFIED IN CODE.
- **ParameterStatus** for GUCs flagged `GUC_REPORT`, sent just before ReadyForQuery
  (`src/backend/tcop/postgres.c:4826` calls `ReportChangedGUCOptions()`; `guc.c:2503`).
- **ReadyForQuery** status `I`/`T`/`E` (VERIFIED BY EXPERIMENT: `BEGIN; SELECT 1` → `T`,
  `BEGIN; SELECT 1/0` → `E`).
- **NotificationResponse** (the backend sent one after `LISTEN c; NOTIFY c`; `wire.out`).

The in-core functions that answer "is this read-only / volatile" (`CommandIsReadOnly`,
`contain_volatile_functions`, `contain_mutable_functions`, `max_parallel_hazard`) are
internal C functions. No SQL function or protocol message exposes them (see "Volatility"
below). COULD NOT FIND any SQL-callable wrapper in the tree.

### 2. Session-state safety: what the server offers

#### 2a. `DISCARD ALL`: exactly what it resets (VERIFIED IN CODE)

`src/backend/commands/discard.c:57-79`:

```c
static void
DiscardAll(bool isTopLevel)
{
	PreventInTransactionBlock(isTopLevel, "DISCARD ALL");
	/* Closing portals might run user-defined code, so do that first. */
	PortalHashTableDeleteAll();
	SetPGVariable("session_authorization", NIL, false);
	ResetAllOptions();
	DropAllPreparedStatements();
	Async_UnlistenAll();
	LockReleaseAll(USER_LOCKMETHOD, true);
	ResetPlanCache();
	ResetTempTableNamespace();
	ResetSequenceCaches();
}
```

That is the whole list. There is **no hook** for extensions or procedural languages in
`DiscardAll` (VERIFIED IN CODE: the function body above is complete). Details:

- **GUCs.** `ResetAllOptions` (`src/backend/utils/misc/guc.c:1910-1929`) skips three kinds
  of setting: those whose context is not `PGC_SUSET`/`PGC_USERSET`, those flagged
  `GUC_NO_RESET_ALL`, and those whose source is `<= PGC_S_OVERRIDE`. Settings that came from
  the startup packet, `ALTER ROLE/DATABASE … SET` or the config file are not "reset" either:
  they *are* the reset value, so `DISCARD ALL` returns to the login-time state, not to the
  server default.
  ```c
  if (gconf->context != PGC_SUSET && gconf->context != PGC_USERSET) continue;
  if (gconf->flags & GUC_NO_RESET_ALL) continue;
  if (gconf->source <= PGC_S_OVERRIDE) continue;
  ```
  `GUC_NO_RESET_ALL` settings (`src/backend/utils/misc/guc_parameters.dat`): `is_superuser`
  (l.1454), `role` (l.2594), `seed` (l.2629), `session_authorization` (l.2710),
  `transaction_deferrable`/`transaction_isolation`/`transaction_read_only` (l.3272-3291,
  transaction-scoped). `role` and `session_authorization` are reset anyway by the explicit
  `SetPGVariable("session_authorization", …)` (VERIFIED BY EXPERIMENT, `e1.out`/`e2.out`: after
  `SET ROLE tenant_b` or `SET SESSION AUTHORIZATION tenant_a`, `DISCARD ALL` restores
  `current_user = session_user =` the login role). **`seed` is not reset** (see residue).
- **Prepared statements**: SQL `PREPARE` and protocol-level named statements share one hash
  table, and `DropAllPreparedStatements` drops both. Experiment: count 0 afterwards.
- **Cursors/portals**: `PortalHashTableDeleteAll` closes holdable cursors too (experiment:
  `pg_cursors` empty).
- **LISTEN**: `Async_UnlistenAll`. **Session advisory locks**: `LockReleaseAll(USER_LOCKMETHOD)`
  (experiment: 0 advisory locks).
- **Temp tables**: `ResetTempTableNamespace` (`src/backend/catalog/namespace.c:4737-4741`)
  drops the *relations* but keeps the namespace. Experiment: `pg_my_temp_schema()` was
  `pg_temp_4` before and after. This is benign: the namespace is empty.
- **Sequences**: `ResetSequenceCaches`. Experiment: `currval`/`lastval` → "not yet defined in
  this session" afterwards.
- **Not in a transaction block**: `PreventInTransactionBlock` (`src/backend/access/transam/xact.c:3744-3783`)
  errors if `IsTransactionBlock()`. `IsTransactionBlock` (`xact.c:5070-5078`) is true for every
  block state except `TBLOCK_DEFAULT`/`TBLOCK_STARTED`. A multi-statement simple Query runs in
  an implicit block (`postgres.c:1114` `use_implicit_block = (list_length(parsetree_list) > 1)`,
  `postgres.c:1175` `BeginImplicitTransactionBlock()`). So **`ROLLBACK; DISCARD ALL` sent as ONE
  Query fails**. VERIFIED BY EXPERIMENT:
  `psql -c "ROLLBACK; DISCARD ALL"` → `ERROR: DISCARD ALL cannot run inside a transaction
  block`. The same pair sent as **two Query messages** succeeds. The two can still go out in one
  TCP write; each gets its own ReadyForQuery. It also cannot run from a function:
  `EXECUTE 'DISCARD ALL'` in plpgsql → "cannot be executed from a function" (`e8.out`).
  Consequence: a client can never "pre-empt" the proxy's reset from inside a statement.

#### 2b. Residue: what a tenant can leave behind that `DISCARD ALL` does not remove

| Residue | Evidence | Who can create it | Cross-tenant effect |
| --- | --- | --- | --- |
| **PRNG state** (`setseed()`/`SET seed`) | `seed` is `GUC_NO_RESET_ALL` (guc_parameters.dat:2629). Experiment `e2.out`: `setseed(0.5); random()` = 0.98516…, and `setseed(0.5); DISCARD ALL; random()` = the **same** 0.98516… | Any role | The next tenant's `random()` sequence is chosen by the previous tenant (predictable). VERIFIED BY EXPERIMENT |
| **Custom GUC placeholders** (`SET myapp.tenant = 'A'`) | Experiment: fresh session `current_setting('myapp.tenant', true) IS NULL` = t. After SET + `DISCARD ALL` it is f, with value `''` | Any role | The value is reset. The *existence* of the name leaks one bit, and code that tests `IS NULL` behaves differently. VERIFIED BY EXPERIMENT |
| **Loaded libraries** (`LOAD`, or first use of an extension's C code) and all of their C globals and hooks | Experiment: superuser `LOAD 'auto_explain'`, `DISCARD ALL` → its 13 GUCs still exist (reset to defaults). A non-superuser `LOAD 'auto_explain'` → "access to library … is not allowed" (only `$libdir/plugins` is allowed) | Superuser; non-superuser only for `plugins/` libraries, or implicitly by calling extension functions | Whatever state the library keeps. For example, extension connection caches such as dblink named connections: inferred from the missing hook, NOT TESTED (dblink not installed) |
| **PL interpreter globals**: PL/Perl `%_SHARED`, PL/Python `GD`/`SD` | No hook in `DiscardAll` (VERIFIED IN CODE). Behaviour NOT TESTED: plperl/plpython are not in the image | Any role allowed to use that language | Full data leak between tenants through shared interpreter globals |
| **Replication-origin session** (`pg_replication_origin_session_setup`) | Experiment: still `pg_replication_origin_session_is_setup() = t` after `DISCARD ALL` | Superuser / roles granted the function | Next client's changes get tagged with the origin |
| Temp namespace OID | see above | any | Benign |
| Backend-local caches (relcache, catcache), memory high-water, `pg_stat` backend counters | Not in the list (VERIFIED IN CODE by absence) | any | Performance only |
| Prepared transactions (2PC) | Not session state: after `PREPARE TRANSACTION` they belong to no session. Needs `max_prepared_transactions > 0` (default 0, DOCS ONLY) | any, if enabled | Global locks held indefinitely, independent of the connection |
| Large-object descriptors | Closed at end of transaction (`src/backend/libpq/be-fsstubs.c:613` `AtEOXact_LargeObject`) | — | None after the lease ends |
| `SET LOCAL`, transaction advisory locks | End with the transaction | — | None |

For mini-gateway with ordinary (non-superuser) tenant roles and no PL/Perl or PL/Python, the
residue that matters is: **the PRNG seed, placeholder existence, and the C state of any
extension a tenant can call**. The seed has a cheap fix: send `SELECT setseed(<random>)`, or
include it in the reset message. The other two are not closable by `DISCARD ALL`. Restrict
the extension set instead.

#### 2c. Cost of `DISCARD ALL` (VERIFIED BY EXPERIMENT; `bench1.out`, `bench2.out`)

`pgbench -n -c 1 -T 8` over a Unix socket inside the container. Each script line is a
separate simple Query.

| Script | Average latency |
| --- | --- |
| `SELECT 1` | 0.056 ms |
| `DISCARD ALL` (clean session) | 0.044 ms |
| `SELECT 1` then `DISCARD ALL` (two queries) | 0.099 ms |
| `CREATE TEMP TABLE; PREPARE; DISCARD ALL` | 8.95 ms |
| `CREATE TEMP TABLE; PREPARE; DROP TABLE; DEALLOCATE` | 9.59 ms |
| dirty-check query (see 2e), with `pg_settings` | 3.24 ms |
| dirty-check without `pg_settings` | 0.71 ms |
| `pg_settings WHERE source='session'` alone | 2.43 ms |
| `pg_locks` advisory for this pid alone | 0.27 ms |

On a clean session `DISCARD ALL` costs **about one empty round trip**, less than `SELECT 1`.
When there is state to drop, the cost is the catalog work of dropping it, about the same as
dropping it by hand. Hidden cost: `ResetPlanCache` invalidates cached plans, including
plpgsql's saved SPI plans. Each function's next call re-plans. This was not measured. A
simple-protocol-only proxy has no client prepared plans to lose. COULD NOT FIND published
numbers from the project itself.

#### 2d. Protocol signals (VERIFIED IN CODE + EXPERIMENT)

**ParameterStatus.** The hard-wired `GUC_REPORT` list on master (flags in `guc_parameters.dat`;
documented in `doc/src/sgml/protocol.sgml:1526-1560`): `application_name`, `client_encoding`,
`DateStyle`, `default_transaction_read_only`, `in_hot_standby`, `integer_datetimes`,
`IntervalStyle`, `is_superuser`, `scram_iterations`, `search_path`, `server_encoding`,
`server_version`, `session_authorization`, `standard_conforming_strings`, `TimeZone`. The docs
say `search_path` "was not reported by releases before 18". The report comes from the
server's GUC machinery, so it fires **regardless of how the change was made**. Experiment
(`wire.py`): `SELECT set_config('application_name','viafunc',false)` → CommandComplete
`SELECT 1`, then `ParameterStatus application_name = viafunc`. A `SET` that was rolled back
produced no ParameterStatus. On PG ≥ 18, then, a `search_path` change through `SET`,
`set_config`, a function body, or `UPDATE pg_settings` is reported (DOCS ONLY for 18: the
test server was 17, where `search_path` is not reported).

**Configurable reporting (`_pq_.report_parameters`): NOT landed** as of this master commit.
VERIFIED IN CODE: `protocol.sgml:306-345`, the "Supported Protocol Extensions" table, reads
"(No supported protocol extensions are currently defined.)". The only `_pq_.` handling in the
backend is the reject path (`src/backend/tcop/backend_startup.c:834`). History (WEB READS):
Robert Haas's 2018 "let's make the list of reportable GUCs configurable"; Dave Cramer's 2019
startup option (Andres Freund objected to the approach,
https://www.postgresql.org/message-id/20191010160542.2gszdxhd56abzgvr%40alap3.anarazel.de);
commitfest https://commitfest.postgresql.org/46/4166; Jelte Fennema-Nio's 2024
protocol-parameter series with a `report_parameters` proof of concept
(https://www.postgresql.org/message-id/CAGECzQScQ3N-Ykv2j4NDyDtrPPc3FpRoa%3DLZ-2Uj2ocA4zr%3D4Q%40mail.gmail.com).
One design point from that thread (web snippet, not read in full): `RESET ALL`/`DISCARD ALL`
would *ignore* protocol parameters, so a proxy's own reporting request would survive a reset.

**CommandComplete tags** (`src/include/tcop/cmdtaglist.h`). The tags that mean session state:
`SET` (l.213; also `SET LOCAL`, `SET ROLE`, `SET SESSION AUTHORIZATION`, `SET TRANSACTION`),
`RESET` (l.200), `PREPARE` (l.193), `DEALLOCATE`/`DEALLOCATE ALL` (l.127-128), `LISTEN`
(l.186), `UNLISTEN` (l.218), `DECLARE CURSOR` (l.129, with or without `WITH HOLD`), `CLOSE
CURSOR*` (l.77-78), `CREATE TABLE` (l.115, temp or not), `CREATE SEQUENCE` (l.111),
`CREATE VIEW` (temp views), `LOAD` (l.187), `DISCARD*` (l.131-135), `DO` (l.136), `CALL`
(l.74). Experiments (`wire.out`) confirm `PREPARE`, `LISTEN`, `NOTIFY`, `DECLARE CURSOR`,
`CREATE TABLE`, `CREATE SEQUENCE`, `SET`/`RESET` for role and session authorization, and `DO`.

**The key exception, SELECT INTO / CREATE TABLE AS.** `cmdtaglist.h` has `SELECT INTO`
(l.212) and `CREATE TABLE AS` (l.116), but those tags are for event triggers. On the wire,
`ExecCreateTableAs` overwrites the tag:
`src/backend/commands/createas.c:347-349` `SetQueryCompletion(qc, CMDTAG_SELECT,
queryDesc->estate->es_processed);`. VERIFIED BY EXPERIMENT: `SELECT 1 INTO TEMP tt` → `SELECT 1`;
`CREATE TEMP TABLE tt2 AS SELECT 1` → `SELECT 1`. **A temp table created this way is
indistinguishable by tag from a plain SELECT.**

What "taint by tag" catches and misses:

- **Catches:** every top-level utility statement, whatever its spelling, comments, quoting,
  case or position in a multi-statement Query. Experiment: the explainer's review-2 trick
  `SELECT E'ok'\n'\'' , set_config('search_path','h',false) --'` came back as `SELECT 1`. The
  tag is honest about the statement *type*, and in this case the type is SELECT, which shows
  both the strength and the limit.
- **Misses:** everything reached through an expression, with tag `SELECT n`, `INSERT 0 n`,
  `UPDATE n`, and so on: `set_config()`, `pg_advisory_lock()`, `setseed()`, any function
  whose body does `SET`, `CREATE TEMP TABLE`, `EXECUTE 'LISTEN …'`, `EXECUTE 'PREPARE …'`,
  `EXECUTE 'DECLARE … WITH HOLD …'`. All of these ran inside plpgsql called by `SELECT` and
  **persisted** (VERIFIED BY EXPERIMENT, `e8.out`: afterwards `pg_listening_channels` = `hc`,
  `pg_prepared_statements` has `hq`, `work_mem = 77MB` with source `session`, and one temp
  relation). It also misses `SELECT INTO TEMP` / `CREATE TEMP TABLE AS` (above). `DO` and
  `CALL` tags are opaque: a tag-based rule must treat them as tainting.
- **Timing:** the tag arrives after execution, so it is useless for rejecting up front. It is
  enough for deciding *reset on release*, which is the decision (b) needs.

**NoticeResponse** carries no structured session information. It does show warnings such as
"there is no transaction in progress". **NotificationResponse** arrives only for channels this
backend is listening on, so seeing one proves a `LISTEN` happened.

#### 2e. Asking the server whether the session is dirty

`work/12-postgres-server/dirty-check.sql`:

```sql
SELECT (SELECT count(*) FROM pg_prepared_statements)                    AS prepared,
       (SELECT count(*) FROM pg_cursors)                                AS cursors,
       (SELECT count(*) FROM pg_listening_channels())                   AS listens,
       (SELECT count(*) FROM pg_locks WHERE pid = pg_backend_pid()
                                        AND locktype = 'advisory')      AS adv_locks,
       (SELECT count(*) FROM pg_settings WHERE source = 'session')      AS session_gucs,
       (SELECT count(*) FROM pg_class
         WHERE relnamespace = pg_my_temp_schema())                      AS temp_rels,
       current_user <> session_user                                     AS role_changed;
```

- **Covers:** prepared statements, open/holdable cursors, LISTEN, session advisory locks,
  session-sourced GUCs (including placeholders), temp relations, and `SET ROLE`.
- **Misses:** sequence `currval` caches (no view), the PRNG seed (`seed` is
  `GUC_NO_SHOW_ALL`; not tested in `pg_settings`), loaded libraries and their state, PL
  globals, and the replication-origin session (would need
  `pg_replication_origin_session_is_setup()`).
- **Cost:** 3.24 ms, against 0.044 ms for `DISCARD ALL`, which is **~70× more**. `pg_settings`
  alone costs 2.4 ms because it materialises every GUC. Without it, 0.71 ms is still 16×.
  **"Always DISCARD ALL" is cheaper than asking**, and it also covers more. The query is useful
  only as a test oracle (chunk 6/7 tests), never on the hot path.

### 3. Read-vs-write enforcement (option c2)

**What is refused** (VERIFIED IN CODE):
- Utility statements are classified by an explicit `switch` in
  `ClassifyUtilityCommandAsReadOnly` (`src/backend/tcop/utility.c:129`). DDL such as
  `T_CreateStmt` (l.188) is not read-only, **including temp tables**. `ProcessUtility` checks
  the result at `utility.c:576-586`. An unknown node type hits `elog(ERROR, "unrecognized node
  type")`, so the check **fails closed**.
- Executor: `ExecCheckXactReadOnly` (`src/backend/executor/execMain.c:813-836`) refuses any
  plan that requests a permission other than SELECT on a **non-temp** relation:
  ```c
  if ((perminfo->requiredPerms & (~ACL_SELECT)) == 0) continue;
  if (isTempNamespace(get_rel_namespace(perminfo->relid))) continue;
  PreventCommandIfReadOnly(CreateCommandName((Node *) plannedstmt));
  ```
- `nextval`/`setval` on non-temp sequences (`src/backend/commands/sequence.c:670-672`,
  `989`); large-object writes (`be-fsstubs.c:99,253,266,318,392,…`).

**What is allowed** in a read-only transaction (VERIFIED IN CODE, `utility.c:259-279`, with
the comment "These modify only backend-local state, so they're OK to run in a read-only
transaction"): `CLOSE`, `SET CONSTRAINTS`, `DEALLOCATE`, `DECLARE CURSOR`, `DISCARD`,
`EXECUTE`, `FETCH`, `LOAD`, `PREPARE`, `UNLISTEN`, `SET`/`RESET`. Also allowed: `LISTEN`
and `NOTIFY` (l.327-340); `CALL`/`DO` themselves (l.235-244: "will be checked separately");
`COPY FROM`, which is re-checked for non-temp targets (l.300-314); `ALTER SYSTEM` (!);
`CHECKPOINT`; `VACUUM`/`REINDEX`; `PREPARE TRANSACTION`; `COMMIT PREPARED`; and transaction
control, including `COMMIT`.

VERIFIED BY EXPERIMENT (`e3.out`, `e4.out`). Inside `BEGIN READ ONLY` these succeeded: `SET
search_path`, `set_config(…, false)`, `PREPARE`, `LISTEN`, `NOTIFY`, `pg_notify()`,
`pg_advisory_lock(42)`, `INSERT` into an existing temp table, `nextval` on a temp sequence,
`DECLARE … WITH HOLD`, and a plpgsql function doing `set_config`. These were refused: `CREATE
TEMP TABLE` ("cannot execute CREATE TABLE in a read-only transaction"), `SELECT … FOR UPDATE`,
`WITH d AS (DELETE …) SELECT`, `EXPLAIN ANALYZE DELETE`, `nextval('s')` on a non-temp
sequence, and `lo_create`. **Read-only enforcement therefore says nothing about D3**: almost
every session-state feature is explicitly permitted.

**Can the transaction become read-write again?** `check_transaction_read_only`
(`src/backend/commands/variable.c:546-574`) forbids the switch inside a subtransaction or
after the first snapshot:
```c
if (FirstSnapshotSet) {
    GUC_check_errmsg("transaction read-write mode must be set before any query");
    return false; }
```
`BEGIN` and `SET` take no snapshot. So **`BEGIN READ ONLY; SET TRANSACTION READ WRITE;
INSERT …; COMMIT` in one Query succeeds** (VERIFIED BY EXPERIMENT, RO-5: row inserted). After
a `SELECT`, the switch is refused (RO-3), including from a function (`f_rw` → same error).
**`BEGIN READ ONLY; SELECT 1; COMMIT; INSERT INTO t VALUES (7); COMMIT` succeeds** (RO-4: the
row was inserted), because `COMMIT` is `COMMAND_IS_STRICTLY_READ_ONLY` (`utility.c:358-372`)
and the next statement opens a fresh, read-write implicit transaction. `SET
default_transaction_read_only = on; SET default_transaction_read_only = off; INSERT` also
succeeds (RO-6).

**So (c2) is sound only if the proxy guarantees that the client text is exactly one
statement** and sits in a transaction the proxy opened, with no statement before it. With
raw text in a simple Query, guaranteeing "one statement" needs a lexer, which is the thing
(b) tries to avoid. **The server can enforce single-statement for you**: the extended
protocol's Parse rejects multiple commands (VERIFIED IN CODE: `src/backend/tcop/postgres.c:1503-1506`,
`exec_parse_message`: `if (list_length(parsetree_list) > 1) ereport(ERROR, … "cannot insert
multiple commands into a prepared statement")`). A proxy could send the proxy-owned `BEGIN READ ONLY` as a
simple Query and the client text as Parse/Bind/Execute of the unnamed statement. A lone
`SET TRANSACTION READ WRITE` or `COMMIT` would then be the entire statement, with nothing
after it to exploit. NOT TESTED; see "Mechanisms not in the assessment".

### 4. Restricting what a session can do (privileges, roles, hooks)

VERIFIED BY EXPERIMENT (`e5.out`, as non-superuser `tenant_a`):
- `REVOKE EXECUTE ON FUNCTION pg_catalog.set_config(text,text,boolean) FROM PUBLIC` →
  `set_config` gives "permission denied for function set_config", and **`UPDATE pg_settings`
  is refused as well**, because the view's rule calls `set_config`. Plain `SET search_path`
  **still works**: `SET` does not go through function ACLs.
- `REVOKE EXECUTE ON FUNCTION pg_advisory_lock(bigint) FROM PUBLIC` refuses that overload
  only. `pg_advisory_lock(1,2)` still succeeded. Each session-level overload
  (`pg_advisory_lock`, `_shared`, `pg_try_advisory_lock`, `_shared`, × `(bigint)`/`(int,int)`)
  must be revoked separately. The `_xact_` variants can stay.
- `REVOKE TEMPORARY ON DATABASE postgres FROM PUBLIC` → "permission denied to create
  temporary tables". This closes temp tables by **every** route, including inside functions
  and `SELECT INTO TEMP`.
- **No privilege exists** for plain `SET` of `PGC_USERSET` GUCs, `PREPARE`, `LISTEN`,
  `DECLARE … WITH HOLD`, or `setseed()`. `setseed` is revocable like any function (not
  tested). All four statements succeeded after the revokes.
- Do revokes survive dump/upgrade? `pg_dump` dumps the delta of catalog-object ACLs against
  `pg_init_privs` (comment at `src/bin/pg_dump/pg_dump.c:1941-1944`; DOCS:
  https://www.postgresql.org/docs/current/catalog-pg-init-privs.html). Mostly VERIFIED IN
  CODE; not tested across an upgrade.
- `PGC_SUSET` settings cannot be set by ordinary roles (`ResetAllOptions` resets both
  `PGC_SUSET` and `PGC_USERSET`). `ALTER ROLE … SET` defines the *reset value*, which
  `DISCARD ALL` returns to. That is useful for a per-tenant role, not for a shared pool.
- **Hook:** `ProcessUtility_hook` (`utility.c:71`, invoked at `utility.c:520`) sees every
  utility statement, **including ones run from inside functions via SPI**. An extension could
  refuse `VariableSetStmt`/`PrepareStmt`/`ListenStmt`/`DeclareCursorStmt` (with
  `CURSOR_OPT_HOLD`) for tenant roles, and that would cover the function-hidden cases a tag
  misses. It cannot see `set_config()`/`pg_advisory_lock()`, which are function calls, not
  utility statements; those are closed by revoke. Existing extensions: supautils uses
  `ProcessUtility_hook` for reserved roles and privileged-role elevation (WEB READS:
  https://supabase.com/blog/roles-postgres-hooks, https://github.com/supabase/supautils). COULD
  NOT FIND an existing extension that refuses session-state statements. Not checked: `set_user`
  or `pg_tle` hooks.
- **Event triggers** fire only for DDL (`event_trigger_ok` column in `cmdtaglist.h`: `SET`,
  `PREPARE`, `LISTEN` are `false`). They cannot police session state. VERIFIED IN CODE.

### 5. Volatility as the server's statement of "safe to share"

- Catalog: `pg_proc.provolatile`/`proparallel`. Experiment (`e6.out`): pg_catalog has 2,401
  `i`, 650 `s`, 268 `v`. `set_config` v/u; `pg_advisory_lock` v/r; `nextval` v/u; `random`
  v/r; `now` s/s; `txid_current` s/u. User functions default to VOLATILE (DOCS:
  https://www.postgresql.org/docs/current/sql-createfunction.html).
- **The label is partly enforced.** For non-volatile functions, SPI refuses any non-read-only
  statement (`src/backend/executor/spi.c:1758`, `2670`: "%s is not allowed in a non-volatile
  function", via `CommandIsReadOnly`, `utility.c:95-120`, which also treats every utility
  command as read/write). Experiment: an IMMUTABLE plpgsql function doing `INSERT` → refused;
  a STABLE one doing `SET` → refused. **But an IMMUTABLE function can lie through function
  calls**: `liar_set()` (IMMUTABLE) did `PERFORM set_config('search_path','liar',false)` and
  `pg_advisory_lock(77)`, and both took effect. An IMMUTABLE SQL function wrapping `random()`
  returned different values per call. Labels are therefore **not** a safety proof against a
  hostile function author.
- In-core machinery (`contain_volatile_functions` `src/backend/optimizer/util/clauses.c:616`,
  `contain_mutable_functions` l.404, `max_parallel_hazard` l.814, `PlannedStmt.hasModifyingCTE`
  / `rowMarks` `src/include/nodes/plannodes.h:81,138`) is internal and not exposed to clients
  (COULD NOT FIND an SQL wrapper). `EXPLAIN (VERBOSE, FORMAT JSON) SELECT * FROM v` for a view
  over `random()` shows `"Output": ["random()"]` (`e7.out`): function names, no volatility,
  and nothing about functions called *inside* other functions. A proxy would need a catalog
  lookup per name. One extra round trip per distinct text, cacheable by text until DDL, but
  only as good as the labels.

### Probe table (what the server does / signals)

| Probe | Tag on the wire | Other server signal | `DISCARD ALL` resets it? | `BEGIN READ ONLY` refuses? | Revocable? |
| --- | --- | --- | --- | --- | --- |
| P1 `SET search_path = x` | `SET` | ParameterStatus on PG ≥ 18 (DOCS ONLY) | yes | no | no |
| P2 `SELECT set_config('search_path','h',false)` | `SELECT 1` | ParameterStatus for reported GUCs (verified with `application_name`; `search_path` PG ≥ 18) | yes | no | yes (EXECUTE) |
| P3 `SELECT f()`, f does SET / temp table | `SELECT 1` | ParameterStatus only for reported GUCs; nothing for temp/LISTEN/PREPARE | SET, temp, LISTEN, PREPARE: yes | SET: no; temp table creation: yes (refused) | temp: yes (TEMPORARY); SET: no |
| P4 lexical tricks | Tag reflects what the server parsed (`SELECT 1` for the review-2 string) | same as P2 | yes | no | as P2 |
| P5 `PREPARE` / `LISTEN` / `CREATE TEMP TABLE` / `pg_advisory_lock(1)` / `DECLARE … WITH HOLD` | `PREPARE` / `LISTEN` / `CREATE TABLE` / `SELECT 1` / `DECLARE CURSOR` | NotificationResponse proves LISTEN | all yes | only `CREATE TEMP TABLE` refused | temp: yes; advisory: yes per overload; others: no |
| P6 `SELECT 1; SET search_path = x` | `SELECT 1`, `SET` (one per statement) | — | yes | no | no |
| P7 `nextval('s')` | `SELECT 1` | — | cache yes | refused (non-temp) | yes (sequence USAGE/UPDATE) |
| P7 `WITH d AS (DELETE…) SELECT` | `SELECT n` (wrapper is SELECT) | — | n/a | refused | table privileges |
| P7 `SELECT … FOR UPDATE` | `SELECT n` | — | n/a | refused | needs UPDATE privilege |
| P7 `SELECT now()` | `SELECT 1` | — | n/a | allowed (not a write; differs per transaction) | n/a |
| P7 `SELECT * FROM v` (view over volatile) | `SELECT n` | EXPLAIN VERBOSE shows `random()` | n/a | allowed | n/a |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | `EXPLAIN` | — | n/a | refused | table privileges |

## Claims of completeness

- The server claims a fixed meaning for `DISCARD ALL`. The docs say it is equivalent to the
  sequence `CLOSE ALL; SET SESSION AUTHORIZATION DEFAULT; RESET ALL; DEALLOCATE ALL; UNLISTEN *;
  SELECT pg_advisory_unlock_all(); DISCARD PLANS; DISCARD TEMP; DISCARD SEQUENCES;`
  (https://www.postgresql.org/docs/current/sql-discard.html; DOCS ONLY for the wording). The
  code matches that list exactly (`discard.c:69-78`). It does **not** claim to reset "the whole
  session". The residue in 2b is outside the documented list rather than a broken promise. The
  docs do not mention the `seed` exception or extension state; COULD NOT FIND a warning in the
  DISCARD page about them (not re-read in full).
- The read-only definition is explicitly *not* "no side effects". In-code comments define it
  as "changes nothing that affects the output of pg_dump" (`utility.c`, `ALTER SYSTEM` case),
  and backend-local state is deliberately allowed. The claim survives the code. It is just a
  different claim from D4/D5.

## Mapping to options (a) (b) (c1) (c2)

- **(b) reset on release** is exactly what `DISCARD ALL` provides. Measured cost on a clean
  session is ~0.04 ms, below a `SELECT 1`, so "strict mode, reset after every lease" costs
  about one extra pipelined round trip. Precondition: it must be its **own Query message**
  after any `ROLLBACK`, never `ROLLBACK; DISCARD ALL` in one message. Add `SELECT
  setseed(random())` or an equivalent to close the seed residue. Extension and PL state stays
  out of reach, so the tenant-visible extension set must be curated.
- **(c2) read-only wrapper** is sound only when the proxy controls statement boundaries. Raw
  client text inside `BEGIN READ ONLY; …; COMMIT` is defeated by a client `COMMIT;` or a
  leading `SET TRANSACTION READ WRITE` (both shown). And read-only says nothing about session
  state or `now()`/`random()`.
- **(c1)** is not served by the server. The parser is not exposed. The server's
  *classification of the result* (tags) is exposed, and that is a different thing (next
  section).
- **(a)** the server offers nothing that completes a deny-list. Its own classifier
  (`ClassifyUtilityCommandAsReadOnly`) is an exhaustive switch over parse-node types that fails
  closed. That is the shape a text deny-list cannot reach.

## Mechanisms not in the assessment

1. **Taint by CommandComplete tag (replaces the first-keyword allow-list of (b)).** After each
   lease, mark the connection dirty if any CommandComplete tag was outside a clean allow-list
   {`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `COPY`, `FETCH`, `MOVE`, `BEGIN`, `START
   TRANSACTION`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`, `RELEASE`, `SHOW`, `EXPLAIN`}. Any other
   tag, including `DO`, `CALL`, `SET`, `CREATE TABLE`, an unknown tag, or an ErrorResponse in
   the middle, means DISCARD ALL. In Go: the proxy already parses backend messages to find
   ReadyForQuery, so this is a string compare on the first word of each `C` message, about 30
   lines, with no lexer. It is exact for every top-level utility statement (spelling-proof,
   verified with the review-2 string). It misses function-hidden state and `SELECT INTO TEMP`
   / `CREATE TABLE AS`, which return tag `SELECT`. So it **replaces** (b)'s first-keyword check
   but not (b)'s substring list or strict mode.
2. **Taint by ParameterStatus.** Any ParameterStatus between leases means a reported GUC
   changed, by whatever route, including `set_config` inside functions. That covers
   `search_path` on PG ≥ 18, the most dangerous one for cross-tenant name resolution. It costs
   nothing and needs no SQL reading.
3. **Close the function-hidden paths by privilege, so tags become sufficient.** `REVOKE
   TEMPORARY ON DATABASE` (temp tables by any route), `REVOKE EXECUTE` on `set_config` (also
   kills `UPDATE pg_settings`) and on the session advisory-lock overloads and `setseed`. Keep
   tenants without `CREATE` on any schema, so they cannot create functions (PG ≥ 15 default for
   `public`, DOCS ONLY), and allow only vetted functions, extensions and languages. What then
   remains to reach session state is a top-level `SET`/`PREPARE`/`LISTEN`/`DECLARE` (visible
   by tag) or a vetted function. Experiment `e8` showed that a function *can* do
   `SET`/`LISTEN`/`PREPARE`/`DECLARE WITH HOLD` via `EXECUTE`, so "vetted" must mean "does none
   of these". This is a deployment rule, not code. In a Go proxy it is a documented setup step
   plus a startup self-check: query `has_function_privilege` and `has_database_privilege` for
   the tenant role and refuse to start if they are open.
4. **Server-enforced single statement for (c2): use extended-protocol Parse.** Parse rejects
   multi-statement strings server-side. Send the proxy-owned `BEGIN READ ONLY` as a Query
   message, then the client text as Parse(unnamed)/Bind/Execute/Sync, then `COMMIT`. A single
   statement cannot both escape and write. `COMMIT` alone ends the transaction with nothing
   after it. `SET TRANSACTION READ WRITE` alone does nothing more. Functions cannot flip after
   the snapshot (`variable.c:558`). `DO` in an explicit block is atomic and cannot `COMMIT`
   (from memory, not tested). Caveats: this assumes the backend accepts arbitrary text through
   Parse (simple-query-only features such as multi-statement `COPY` differ), and result
   framing changes slightly (RowDescription comes from Describe). The multi-command refusal is VERIFIED IN CODE (`postgres.c:1503-1506`); the end-to-end
   wrapper was NOT TESTED in this run.
5. **A `ProcessUtility_hook` extension** that refuses session-state utility statements for
   tenant roles, even inside functions. It is complete for the utility path, with revokes for
   the function path. But it is C code installed in the database, which is outside "vanilla
   Postgres", so it is a deployment option, not a proxy feature. No existing extension found.
6. **Reseed the PRNG on release** (`SELECT setseed(…)` as part of the reset), or revoke
   `setseed`. This is a residue the assessment's (b) would leave open.
7. **Do not ask the server whether a session is dirty on the hot path.** The full dirty-check
   costs 70× a `DISCARD ALL`. Use it as a test oracle in chunk 6/7 (the fake Postgres versus
   the real one).

History (WEB READS): Konstantin Knizhnik's built-in pooler (pgsql-hackers, January 2019,
https://www.postgresql.org/message-id/ac873432-31cf-d5e4-0b80-b5ac95cfe385%40postgrespro.ru)
quotes itself: "If client changes session context (creates temporary tables, set GUC values,
prepare statements,...) then its backend becomes 'tainted' and is not more participate in
pooling." The detection ran inside the backend, at the point of state change, not on text.
Tomas Vondra objected that it tainted too eagerly (`SET LOCAL`, functions with `SET`
clauses), and later versions exempted `ON COMMIT DROP` temp tables and added optional GUC
proxying (`proxying_gucs`)
(https://www.postgresql.org/message-id/20190730010240.tktomjoio2efgcx3%40development). The
commitfest entry ended "Returned with feedback" on 2021-10-01
(https://commitfest.postgresql.org/patch/3043). The lesson from the hackers' side: even with
access to the backend's internals, the design tracked state at the point of change and fell
back to dedicating the backend. Nobody proposed classifying text. COULD NOT FIND the patch
code for the taint points (gh code search for `is_tainted` returned nothing).

## Could not verify

- Behaviour of PL/Perl `%_SHARED` and PL/Python `GD` across `DISCARD ALL` (not installed in
  the image). The conclusion rests only on the missing hook in `DiscardAll`.
- dblink / postgres_fdw connection caches across `DISCARD ALL` (not installed).
- `search_path` ParameterStatus on PG 18+ (test server is 17). DOCS ONLY.
- Whether `seed` appears in `pg_settings` with source `session` (it is `GUC_NO_SHOW_ALL`).
- An end-to-end test of mechanism 4 (Parse-based single-statement read-only wrapper).
- Final state of the `_pq_.report_parameters` patch series beyond "not in master as of
  4483697". Web snippets only for the thread history.
- Knizhnik's patch source (taint points). Only the mailing-list description was read.
- An existing extension that blocks session-state utility statements per role.
- Published `DISCARD ALL` cost numbers from the PostgreSQL project. Only my own pgbench
  figures exist (Unix socket, one client, PG 17.11, 4-core VM shared with other agents).
