# PgCat (Rust): sqlparser-rs for routing, protocol signals for session cleanup

Cluster 02. Research only. Every code citation is a path in a local clone at the SHA under
Sources, and line numbers come from that SHA. Tags: **VERIFIED IN CODE**, **DOCS ONLY**,
**COULD NOT FIND**, and **INFERRED**. INFERRED means a consequence I worked out by reading two or
more code paths together. I did not build or run anything, because `cargo` is not installed and
the brief says not to build.

## Sources

| Project | URL | Commit SHA read | Commit date | Local/web |
| --- | --- | --- | --- | --- |
| PgCat | https://github.com/postgresml/pgcat | `5b038813eb14f181434ab7b5509e74d9b1fe123b` (HEAD of `main`) | 2025-02-27 | LOCAL SOURCE `repos/pgcat` |
| sqlparser-rs 0.52.0 (the version PgCat pins: `Cargo.toml:22` `sqlparser = { version = "0.52", features = ["visitor"] }`, `Cargo.lock` → `0.52.0`) | https://static.crates.io/crates/sqlparser/sqlparser-0.52.0.crate | crate tarball 0.52.0 | n/a | LOCAL SOURCE `repos/sqlparser-0.52/sqlparser-0.52.0` |
| PostgreSQL GUC table (to check what `RESET ALL` skips and what is `GUC_REPORT`) | https://github.com/postgres/postgres | `4483697` (master) | 2026-11-25 | LOCAL SOURCE `repos/postgres` |
| PgCat issues #942, #620 | https://github.com/postgresml/pgcat/issues/942, …/620 | n/a | n/a | WEB READ (`gh api`) |

Activity facts, from `gh api repos/postgresml/pgcat`: the repo was created 2022-01-29 and last
pushed 2025-02-27. It is not archived and has about 4.0k stars and 199 open issues. License:
**MIT**. `Cargo.toml` version `1.3.0`. The newest git tag is `v1.2.0` (commit dated 2024-08-30).
The GitHub "releases" are Helm-chart releases; the newest is `pgcat-0.2.5`, 2024-11-11.

**Maintenance status: effectively unmaintained.** Issue #942 (2025-11-23) asks whether PgCat is
still maintained. The original author (`levkk`) replied on 2025-12-07: *"I'm not developing pgcat
anymore, I'm working full time on PgDog"*. On 2026-01-14 he added *"I don't have access to the
repo anymore, it looks like."* No commit has landed since 2025-02-27. PRs still arrive (#953
2026-05, #956 2026-07, #957 2026-07). PgDog, covered in `findings/05-pgdog.md`, is the same
author's successor.

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| PgCat | **sqlparser-rs 0.52** (`PostgreSqlDialect`), a hand-written recursive-descent parser for many dialects, not derived from PostgreSQL's grammar. It is **off by default** (`query_parser_enabled` defaults to false). It is used only for replica/primary routing, automatic sharding and plugins. Separately, **anchored regexes** recognise PgCat's own `SET SHARD …`/`SET SERVER ROLE …` commands, and optional user regexes find shard comments. | **Reads no SQL for session state.** It watches **protocol signals** from the server: the `CommandComplete` tag `SET` (only outside an explicit transaction) or `PREPARE`, and a named extended-protocol `Parse` when the cache is off. Any of these marks the connection dirty. At check-in a dirty connection gets `RESET ROLE; RESET ALL;` and/or `DEALLOCATE ALL;`. An open transaction is always rolled back. **Track and replay** applies to 5 `ParameterStatus` parameters (`client_encoding`, `DateStyle`, `TimeZone`, `standard_conforming_strings`, `application_name`), synced by diff at checkout. Prepared statements are **renamed** (`PGCAT_<n>`) and cached per server. The cleanup is on by default (`cleanup_server_connections = true`). | Read/write split on the parse-tree statement kind: a top-level `Query` is a read unless it has a locking clause or its *body* is `INSERT`/`UPDATE`. Any other statement kind, including `BEGIN`, goes to the primary. No result sharing or coalescing exists. | Deny-list. A `Query` is a read unless one of 2 checks fires; everything that is not a `Query` is a write. | Read/write: a write that reaches a replica gets an **error from the replica**, never wrong data. Session state: a **silent leak to the next client of the same (database, user) pool** for anything that is not a tracked tag (temp tables, `LISTEN`, advisory locks, `WITH HOLD` cursors, `set_config`, a `SET` inside `BEGIN … COMMIT`). | Parse error: **open**. A warning is logged, the statement is forwarded, and it goes to whatever role the router last held. Unknown function: **open** (read). Unseen session change: **open** (no reset). |

## PgCat

### 1. Does it read SQL? With what?

PgCat has three separate readers. None of them guards session state.

**(a) sqlparser-rs, for routing only. VERIFIED IN CODE.** `QueryRouter::parse`
(`src/query_router.rs:362-408`) takes the text of a simple `Query` (`'Q'`) or a `Parse` (`'P'`)
message and calls the generic multi-dialect parser:

```rust
// src/query_router.rs:401-407
match Parser::parse_sql(&PostgreSqlDialect {}, &query) {
    Ok(ast) => Ok(ast),
    Err(err) => {
        debug!("{}: {}", err, query);
        Err(Error::QueryRouterParserError(err.to_string()))
    }
}
```

It is called only when `query_parser_enabled()` is true (`src/client.rs:951`, `:993`, `:1235`,
`:1311`). The config default is false: `#[serde(default)] // False pub query_parser_enabled: bool`
(`src/config.rs:544-545`). `CONFIG.md:373-377` says `default: true`, so **the docs contradict the
code**. A separate `query_parser_read_write_splitting` switch (`src/config.rs:549-550`, default
false) gates role inference, and config validation rejects it without the parser
(`src/config.rs:725-727`). A length cap, `query_parser_max_length`, turns long statements into a
parse error (`src/query_router.rs:368-375`).

sqlparser-rs 0.52 is a hand-written recursive-descent parser shared by about 15 dialects. It is
neither derived from PostgreSQL's `gram.y` nor kept in step with PostgreSQL releases. Its size:
`src/parser/mod.rs` has 13,022 lines, `src/tokenizer.rs` has 2,978 and `src/dialect/postgresql.rs`
has 274 (all VERIFIED by `wc -l`). PgCat's own issue #620 (2023, still open) says so: *"sqlparser-rs
does not correctly parse all PostgreSQL queries … it's not going to be 100% PG compatible"*, and
proposes moving to `pg_query.rs`. That never happened. PgDog did it instead.

Lexical gaps in sqlparser 0.52 that matter here (VERIFIED IN CODE in the crate):
- **No string continuation across a newline.** There is no `supports_string_literal_concatenation`
  anywhere in the 0.52 dialects (COULD NOT FIND it, by grep). `'a'\n'b'` produces two string
  tokens. `parse_optional_alias` accepts a single-quoted string as an alias
  (`parser/mod.rs:8126` `Token::SingleQuotedString(s) => Ok(Some(Ident::with_quote('\'', s)))`).
  So the second literal is read as an alias or the parse fails. It is never joined to the first
  literal the way PostgreSQL joins it.
- Nested block comments are handled (`tokenizer.rs:1595-1610`, a `nested` counter). Dollar quotes
  are handled (`tokenizer.rs:1275` `tokenize_dollar_preceded_value`).
- **Data-modifying CTEs**: `parse_cte` calls `parse_query()` (`parser/mod.rs:9040`), which accepts
  `INSERT` and `UPDATE` as a body (`:8789`, `:8804`). `parse_query_body` accepts only `SELECT`,
  `(`, `VALUES` and `TABLE` (`:9088-9104`). `WITH d AS (DELETE …)` is therefore a **parse error**.
  `WITH d AS (INSERT …) SELECT …` parses.

**(b) Anchored regexes for PgCat's own commands. VERIFIED IN CODE.** There are seven patterns,
all case-insensitive and anchored at both ends:

```rust
// src/query_router.rs:30-38
const CUSTOM_SQL_REGEXES: [&str; 7] = [
    r"(?i)^ *SET SHARDING KEY TO '?([0-9]+)'? *;? *$",
    r"(?i)^ *SET SHARD TO '?([0-9]+|ANY)'? *;? *$",
    r"(?i)^ *SHOW SHARD *;? *$",
    r"(?i)^ *SET SERVER ROLE TO '(PRIMARY|REPLICA|ANY|AUTO|DEFAULT)' *;? *$",
    r"(?i)^ *SHOW SERVER ROLE *;? *$",
    r"(?i)^ *SET PRIMARY READS TO '?(on|off|default)'? *;? *$",
    r"(?i)^ *SHOW PRIMARY READS *;? *$",
];
```

`try_execute_command` runs a `RegexSet` over the whole simple-query text. It accepts a command
only if exactly one pattern matches (`src/query_router.rs:234-241`), and the command is answered
by PgCat and never forwarded. The only caller is `handle_custom_protocol` (`src/client.rs:1661`),
which runs in the outer loop before a server is checked out (`src/client.rs:938-944`). Inside a
transaction, or in session mode, these strings go to Postgres, which rejects them as syntax
errors. **INFERRED** from the loop structure. A code comment says as much:
`src/client.rs:1170` comment "If the client is in session mode, no more custom protocol
commands will be accepted."

**(c) User-configured comment regexes. VERIFIED IN CODE.** `shard_id_regex` and
`sharding_key_regex` (`src/config.rs:580-582`) are matched against the first
`regex_search_limit` bytes (default 1000, `src/config.rs:806`) of every `Q` or `P` message,
decoded with `from_utf8_lossy` and with no awareness of quoting (`src/query_router.rs:170-213`).
A string literal containing `/* shard_id: 3 */` routes the statement the same way a real comment
would. **INFERRED.**

### 2. Session-state safety

**Mechanism: reset on release, triggered by server protocol signals. Plus track and replay for
5 reported parameters. Plus prepared-statement renaming.** This path reads no SQL.

**Trigger 1, the `CommandComplete` tag. VERIFIED IN CODE.** While receiving server messages,
`Server::recv` inspects each `CommandComplete`:

```rust
// src/server.rs:1012-1030
// Non-exhaustive list of commands that are likely to change session variables/resources
// which can leak between clients. This is a best effort to block bad clients
// from poisoning a transaction-mode pool by setting inappropriate session variables
match command.as_str() {
    "SET" => {
        // We don't detect set statements in transactions
        // No great way to differentiate between set and set local
        // As a result, we will miss cases when set statements are used in transactions
        // This will reduce amount of reset statements sent
        if !self.in_transaction {
            self.cleanup_state.needs_cleanup_set = true;
        }
    }
    "PREPARE" => { self.cleanup_state.needs_cleanup_prepare = true; }
    _ => (),
```

`self.in_transaction` is updated only from the `ReadyForQuery` status byte (`src/server.rs:930-950`).
At the moment a tag is processed, the flag therefore still holds the state left by the *previous*
message. **INFERRED** consequences:
- `SET x` sent alone while idle is caught.
- `BEGIN; SET x; COMMIT` sent in **one** Query message is caught, because the flag is still `I`
  from the previous `ReadyForQuery`.
- `BEGIN` then `SET x` then `COMMIT` sent as **separate** messages is **missed**: the flag is
  `T` when the `SET` tag arrives. A plain `SET` inside a transaction persists after `COMMIT`, so
  this is a real leak. The code comment admits it.

The tag is `SET` for every form of `SET`, including `SET ROLE` and `SET SESSION AUTHORIZATION`.
The tag is `RESET` for `RESET`, which is not on the list, but `RESET` only removes state, so the
miss is harmless. **INFERRED.**

**Trigger 2, a named `Parse` with the statement cache off. VERIFIED IN CODE.** In
`src/client.rs:1403-1412`, a buffered `Parse` with a non-empty statement name, when PgCat's
cache is disabled, calls `server.mark_dirty()`. That sets both cleanup flags
(`src/server.rs:1382-1385`, `CleanupState::set_true` `:131-134`).

**Trigger 3, a "cached plan must not change result type" error** with the cache on sets
`needs_cleanup_prepare` (`src/server.rs:989-1000`).

**The reset itself, at check-in. VERIFIED IN CODE.** `Server::checkin_cleanup`
(`src/server.rs:1324-1364`) is called when a transaction-mode lease ends (`src/client.rs:1615`),
on client `Terminate` (`:1301`) and on client disconnect mid-transaction (`:1191`):

```rust
// src/server.rs:1329-1357 (abridged)
if self.in_transaction() {
    self.query("ROLLBACK").await?;
}
if self.cleanup_state.needs_cleanup() && self.cleanup_connections {
    let mut reset_string = String::from("RESET ROLE;");
    if self.cleanup_state.needs_cleanup_set { reset_string.push_str("RESET ALL;"); };
    if self.cleanup_state.needs_cleanup_prepare {
        reset_string.push_str("DEALLOCATE ALL;");
        if let Some(cache) = &mut self.prepared_statement_cache { cache.clear(); }
    };
    self.query(&reset_string).await?;
    self.cleanup_state.reset();
}
```

It never sends `DISCARD ALL`, `DISCARD TEMP`, `UNLISTEN *`, `pg_advisory_unlock_all()`, `CLOSE ALL`
or `SET SESSION AUTHORIZATION DEFAULT`. The source has no mention of advisory locks, `LISTEN`,
temp tables, `WITH HOLD` or `set_config` (COULD NOT FIND, by `grep -rni` over `src/`). In
PostgreSQL, `session_authorization` carries `GUC_NO_RESET_ALL` (`repos/postgres`
`src/backend/utils/misc/guc_parameters.dat:2710-2712`), so `RESET ALL` does not undo
`SET SESSION AUTHORIZATION`. PgCat would leave it in place, although only a superuser can run it.
`role` also carries `GUC_NO_RESET_ALL` (`:2594-2596`), which is why PgCat sends `RESET ROLE`
explicitly. **VERIFIED IN CODE** (both sides).

Default: `cleanup_server_connections` defaults to **true** (`src/config.rs:591-592`, `:658-660`).
I could not find it in `CONFIG.md` (COULD NOT FIND). A query error during the reset propagates
with `?`. I did not trace whether the connection is then dropped (COULD NOT FIND a definitive
path in the time budget).

**Track and replay of 5 parameters. VERIFIED IN CODE.** `TRACKED_PARAMETERS` is `client_encoding`,
`DateStyle`, `TimeZone`, `standard_conforming_strings` and `application_name`
(`src/server.rs:152-160`). Every `ParameterStatus` (`'S'`) the server sends is stored both in
the attached client's `ServerParameters` and in the server's own copy
(`src/server.rs:1041-1052`; the client's copy is passed in at `src/client.rs:2057`). At every
checkout, `sync_parameters` diffs the server's copy against the client's and sends
`SET key TO 'value';` for each difference (`src/server.rs:1261-1279`, called at
`src/client.rs:1157`). The signal comes from the server, not from SQL text. A change to one of
these five parameters is therefore caught however it was made: `SET`, `set_config()`, or a
function that runs `SET` inside it. **INFERRED** from the code above. `set_param` drops every
key not in the set (`src/server.rs:194-207`), so `search_path` is not tracked, even though
PostgreSQL master now marks it `GUC_REPORT` (`guc_parameters.dat:2620-2622`).

Note `self.cleanup_state.reset()` at the end of `sync_parameters` (`src/server.rs:1276`). The
`SET`s PgCat sends itself would otherwise mark the connection dirty.

**Prepared statements. VERIFIED IN CODE.** The cache is active only in transaction mode with
`prepared_statements_cache_size > 0` (`src/client.rs:720-721`; default `0`,
`src/config.rs:662-664`). When it is on:
- Each client `Parse` is hashed over the text, the parameter count and the parameter types
  (`src/messages.rs:917-935`).
- The hash is looked up in a pool-wide LRU (`PreparedStatementCache::get_or_insert`,
  `src/pool.rs:86-103`) and renamed to `PGCAT_<global counter>` (`Parse::rewrite`,
  `src/messages.rs:900-906`).
- Each server connection keeps its own LRU of names. A missing statement is re-`Parse`d on
  demand, and an evicted one is `Close`d (`Server::register_prepared_statement`,
  `src/server.rs:1167-1210`).
- The client-to-pgcat name map is per client (`src/client.rs:112`). Simple-query `PREPARE` is
  not rewritten. It relies on the `PREPARE` tag and `DEALLOCATE ALL` (trigger 1).

**Pool keying. VERIFIED IN CODE.** Pools are keyed by `PoolIdentifier { db, user }`
(`src/pool.rs:114-131`). Inside a pool, shards and roles are separate address lists, selected
by `pool.get(shard, role, …)` (`src/client.rs:1072-1073`). A leak can therefore cross clients
only within one (database, user) pool, never across users.

### 3. Read-vs-write and shareability decisions

**Deny-list over statement kinds. VERIFIED IN CODE.** `QueryRouter::infer`
(`src/query_router.rs:458-570`) walks the statement list:

```rust
// src/query_router.rs:490-541 (abridged)
for q in ast {
    match q {
        StartTransaction { .. } => { self.active_role = Some(Role::Primary); break; }
        Query(query) => {
            let has_locks = !query.locks.is_empty();
            let has_mutation = Self::is_mutation_query(query);
            if has_locks || has_mutation { self.active_role = Some(Role::Primary); }
            else if !visited_write_statement {
                self.active_role = match self.primary_reads_enabled() {
                    false => Some(Role::Replica), true => None, } }
        }
        _ => { visited_write_statement = true; self.active_role = Some(Role::Primary); }
```

```rust
// src/query_router.rs:411-420
fn is_mutation_query(q: &sqlparser::ast::Query) -> bool {
    match q.body.as_ref() {
        SetExpr::Insert(_) => true, SetExpr::Update(_) => true,
        SetExpr::Query(q) => Self::is_mutation_query(q), _ => false, } }
```

The rule: a statement is a read if it is a `Query` whose top-level body is not `INSERT`/`UPDATE`
and which has no `FOR UPDATE/SHARE` clause. The check does **not** look inside `q.with` (the
CTE list), at function calls, or at `SELECT INTO`. An optional extra
(`db_activity_based_routing`, experimental, `CONFIG.md:332-338`) sends reads of recently
written tables to the primary for a TTL (`src/query_router.rs:500-511`, `:605-618`).

**Sticky role on failure. INFERRED.** `active_role` is set to the pool default once per client
(`src/client.rs:886`) and is then changed only by `infer` or the custom commands. Both callers
discard `infer`'s result (`let _ = query_router.infer(&ast);`, `src/client.rs:970`, `:1000`). On
a parse error the code only logs (`src/client.rs:974-979`). An unparseable statement therefore
goes to whichever role the *previous* parsed statement chose, which may be a replica.

**Cost of a miss, in PgCat.** A write routed to a replica fails there with a read-only-transaction
error. The client sees an error, not wrong data, and nothing is retried. There is no caching or
coalescing, so there is no shareability decision.

**Fail open or closed.** Open on every axis. A parse error forwards the statement with the
last role. An unknown function is a read. An unseen session change is not reset.

### 4. Documented limitations (the project's own words)

- README.md:144 (DOCS ONLY): *"In transaction mode, a client talks to one server for the duration
  of a single transaction; once it's over, the server is returned to the pool. Prepared
  statements, `SET`, and advisory locks are not supported; alternatives are to use `SET LOCAL`
  and `pg_advisory_xact_lock` which are scoped to the transaction."* The code partly contradicts
  "not supported": `SET`, `PREPARE` and protocol prepared statements are tracked and reset (see 2),
  and the prepared-statement cache exists to support them.
- README.md:151 (DOCS ONLY): *"The query parser, implemented with the `sqlparser` crate, will
  interpret the query and route all `SELECT` queries to a replica, while all other queries
  including explicit transactions will be routed to the primary."*
- README.md:154: *"The query parser will do its best to determine where the query should go, but
  sometimes that's not possible. In that case, the client can select which server it wants using
  this custom SQL syntax"* (`SET SERVER ROLE TO …`).
- README.md feature table: transaction pooling is *"Identical to PgBouncer with notable
  improvements for handling bad clients and abandoned transactions."* "Automatic sharding" and
  "Sharding using comments parsing/Regex" are both marked **Experimental**.
- The code comment at `src/server.rs:1012-1014` calls the tag list *"Non-exhaustive … a best
  effort to block bad clients"*.
- `CONFIG.md:334-338` (`db_activity_based_routing`): *"This feature is experimental and may not work
  as expected."*

### 5. Language, licence, maturity, dependency weight

Rust (Tokio). MIT. Started January 2022. The last commit is 2025-02-27 and the author has moved to
PgDog (see Sources). There are about 14k lines in `src/`, and `query_router.rs` alone has 2,289.
A Go proxy has nothing to reuse here as a library. The *ideas* cost little, though: the
`CommandComplete` tag watch and the `ParameterStatus` diff need only the wire-protocol decoding
that mini-gateway already does, and no SQL parser at all. Taking the parser idea would mean a Go
analogue of sqlparser-rs (for example `vitess`'s or `cockroachdb`'s grammar). PgCat's own
issue #620 argues against that class of parser for Postgres.

### Probe table (behaviour with parser and read/write splitting on; cleanup at its default, on)

| Probe | What PgCat does | Code path | Tag |
| --- | --- | --- | --- |
| P1 `SET search_path = x` | Parsed as `SetVariable`, so not a `Query`: routed to the primary. On the server the `CommandComplete` tag is `SET`. Sent while idle → connection marked, `RESET ROLE; RESET ALL;` at check-in → no leak. Sent inside an explicit multi-message transaction → not marked → **leaks** to the next client of the pool. | `query_router.rs:543-568`; `server.rs:1016-1025`, `:1339-1357` | VERIFIED IN CODE (the in-transaction miss is stated in the code comment) |
| P2 `SELECT set_config('search_path','h',false)` | A `Query` → replica (or any server with `primary_reads_enabled`). The tag is `SELECT 1`, so nothing is marked and `search_path` is not in the tracked five → **leaks**. With `TimeZone`/`DateStyle`/`application_name` instead of `search_path`, `ParameterStatus` makes `sync_parameters` repair it at the next checkout. | `query_router.rs:528-537`; `server.rs:152-160`, `:1041-1052`, `:1261-1279` | INFERRED |
| P3 `SELECT f()` with `SET`/temp table inside | Same as P2: a read, the tag is `SELECT 1`, and no marking. Temp table and `search_path` **leak**. A tracked GUC changed inside `f` is repaired through `ParameterStatus`. | as P2 | INFERRED |
| P4 lexical tricks (`SELECT E'ok'\n'\' \|\| set_config(...) --'`) | Session safety: n/a. No text is read for it, so a trick cannot hide anything from the tag check, but the tag check does not see `set_config` either (as P2). Routing: sqlparser 0.52 has no string continuation, so it reads `'\'` as an alias or fails to parse. On failure the statement is forwarded with the sticky role. | `parser/mod.rs:8126`; `client.rs:974-979` | INFERRED |
| P5 `PREPARE p AS …` | Routed to the primary (not a `Query`). Tag `PREPARE` → `DEALLOCATE ALL` at check-in. | `server.rs:1027-1030`, `:1347-1353` | VERIFIED IN CODE |
| P5 `LISTEN c` | Primary. Tag `LISTEN` is not on the list → **subscription leaks**. | `server.rs:1015-1032` | VERIFIED IN CODE (absence) |
| P5 `CREATE TEMP TABLE t(...)` | Primary. Tag `CREATE TABLE` is not on the list → **temp table leaks**. | same | VERIFIED IN CODE (absence) |
| P5 `SELECT pg_advisory_lock(1)` | A `Query` → **replica**, where advisory locks do work. The tag is `SELECT 1` → **the lock stays held on the pooled connection**. The README calls it "not supported". | `query_router.rs:528-537` | INFERRED |
| P5 `DECLARE c CURSOR WITH HOLD …` | Primary. Tag `DECLARE CURSOR` is not on the list → **cursor leaks**. | `server.rs:1015-1032` | VERIFIED IN CODE (absence) |
| P6 `SELECT 1; SET search_path = x` (one Query) | Two statements. `SELECT` first → replica, then `SET` → `visited_write_statement`, primary. Final role: primary. The second `CommandComplete` is `SET` while `in_transaction` still shows the earlier `I` → marked → reset. | `query_router.rs:490-568`; `server.rs:1021` | INFERRED |
| P7 `SELECT nextval('s')` | **Replica**, which errors with "cannot execute nextval() in a read-only transaction". No function inspection. | `query_router.rs:528-537` | INFERRED |
| P7 `WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d` | **Parse error** in sqlparser 0.52 (a CTE body cannot be `DELETE`) → forwarded with the sticky role: the primary if the last statement went there, otherwise a replica, which errors. `WITH d AS (INSERT …) SELECT …` parses, but `is_mutation_query` ignores `with`, so it goes to the **replica**, which errors. | `parser/mod.rs:9040`, `:9088-9104`; `query_router.rs:411-420` | VERIFIED IN CODE (parser), INFERRED (route) |
| P7 `SELECT * FROM t FOR UPDATE` | `locks` non-empty → primary. | `query_router.rs:528-532`; test `test_select_for_update` `:1364` | VERIFIED IN CODE |
| P7 `SELECT now()` | Replica. Correct, since there is no caching. | `query_router.rs:533-537` | INFERRED |
| P7 `SELECT * FROM v` (view over a volatile function) | Replica. If the function writes, the replica errors. | same | INFERRED |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | `Statement::Explain` is not a `Query` → primary. Correct. | `query_router.rs:543-568` | INFERRED |

## Claims of completeness

PgCat makes **no** claim to decide safety from SQL text completely. It concedes the opposite in
three places:
- the parser "will do its best … sometimes that's not possible" (README.md:154);
- the session tag list is "Non-exhaustive … best effort" (`src/server.rs:1012-1014`);
- the in-transaction `SET` miss is acknowledged (`src/server.rs:1017-1020`).

One claim overstates the code: README.md:15 says transaction pooling is "Identical to PgBouncer
with notable improvements". Its session protection covers `SET` outside transactions, `PREPARE`,
protocol prepared statements, `ROLE` and five reported GUCs. It does not cover temp tables,
`LISTEN`, advisory locks, holdable cursors or `SET` inside a transaction. Those gaps are
consistent with the README's "not supported" line (README.md:144), so this is a
document-and-shrug limit, not a false guarantee.

## Mapping to options (a) (b) (c1) (c2)

- **Session state (D3): closest to (b), with a different trigger.** It is reset on release when
  the connection is "not known clean". But PgCat decides "dirty" from **server protocol signals**
  (the `CommandComplete` tag, `ParameterStatus`, named `Parse`), not from a keyword allow-list or
  a substring taint list over the text. Its reset is narrower than (b)'s `DISCARD ALL`: it is
  `RESET ROLE; RESET ALL; DEALLOCATE ALL`, and it leaves temp tables, `LISTEN`, advisory locks and
  cursors in place. It has no "strict, reset every lease" switch.
- **Read/write (D4-like routing):** a deny-list over a real-ish parse tree. That is (c1) with a
  non-PostgreSQL parser, and it fails open. Its misses are cheap only because a replica refuses
  writes, which makes it (c2) by accident: **the replica's read-only state is what enforces
  correctness**, not the classifier.
- **Sharing (D5):** none of them. PgCat does not coalesce or cache.

## Mechanisms not in the assessment

1. **Using `CommandComplete` command tags as the session-change detector.** VERIFIED IN CODE
   (`src/server.rs:1005-1037`). Postgres reports what *each* executed statement was, after
   parsing it with its own grammar: `SET`, `PREPARE`, `LISTEN`, `CREATE TABLE`, `DECLARE CURSOR`,
   `DISCARD ALL`, and so on. This is immune to lexical tricks (P4) and to multi-statement
   packing (P6), because it counts executed statements, not text. Limits: a session change made
   inside a function or `set_config` returns `SELECT n` (P2/P3), and `CREATE TABLE` is the same
   tag for temporary and permanent tables. The tag cannot tell `SET` from `SET LOCAL`, which is
   PgCat's reason for skipping it inside a transaction.
   **For a Go proxy:** mini-gateway already relays every backend message. A taint flag set when a
   `CommandComplete` tag is in {`SET`, `PREPARE`, `LISTEN`, `DECLARE CURSOR`, `CREATE TABLE`,
   `SELECT INTO`/`CREATE TABLE AS`, `DO`, `CALL`, …} would cost about 30 lines. Unlike PgCat, it
   should also mark during transactions and accept the over-reporting. It complements (b)'s text
   taint check. It does not replace it, because of the function gap. Note: a `CREATE TABLE` tag
   does not say "temp", so treat any `CREATE TABLE` as dirty.
2. **Diff-and-replay of `ParameterStatus` parameters per client.** VERIFIED IN CODE
   (`src/server.rs:152-279`, `:1041-1052`, `:1261-1279`). The server pushes the new value of
   every `GUC_REPORT` setting whenever it changes, however it changed, including inside
   functions. The proxy records the last value per client and per server connection, and at
   checkout sends `SET k TO v` for the differences. This is "track and replay" with zero SQL
   reading, but it covers only the reported GUCs. On PostgreSQL master, `search_path` is now
   `GUC_REPORT` (`repos/postgres src/backend/utils/misc/guc_parameters.dat:2620-2622`), which
   would close the P2/P3 `search_path` hole on servers that report it. **For mini-gateway**, the
   cheaper use is as a *taint signal*: any `ParameterStatus` arriving during a lease that was not
   requested by the proxy marks the connection dirty. That is about 10 lines. (Which PostgreSQL
   major first reports `search_path` I did not check. See `findings/12-postgres-server.md`.)
3. **Renaming protocol-level prepared statements to proxy-owned names** with a per-connection LRU
   and lazy re-`Parse` (`src/messages.rs:900-935`, `src/pool.rs:66-110`,
   `src/server.rs:1119-1210`). Not relevant while mini-gateway refuses the extended protocol, but
   it is the known design if that ever changes.
4. **ROLLBACK instead of close** for a connection returned in a transaction (`src/server.rs:1329-1332`),
   with the transaction state read from the `ReadyForQuery` status byte. The assessment already
   uses the status byte for decision 2. The ROLLBACK-and-reuse choice is the extra detail.

None of these requires reading SQL. Mechanisms 1 and 2 are the useful ones for decision 3.

## Could not verify

- Whether a failure of the check-in reset query (`checkin_cleanup` returns `Err`) leads to the
  server connection being discarded or returned. I did not trace the error path through the
  `bb8` pool wrapper. COULD NOT FIND.
- `cleanup_server_connections` is absent from `CONFIG.md`. COULD NOT FIND any documentation of it.
- I did not run sqlparser 0.52 (no `cargo` on the machine). The P4 and P7 parse outcomes are read
  from `parser/mod.rs` and `tokenizer.rs`, not observed.
- Whether any deployment guide recommends `query_parser_enabled = true` as the default. The
  shipped `pgcat.toml` enables it (`pgcat.toml:161`, `:165`, `:333`), while the code default is
  false and `CONFIG.md` says true. The docs and the code disagree, and I could not determine
  which is intended.
- The PostgreSQL major version in which `search_path` became `GUC_REPORT`: I checked only
  master. COULD NOT FIND a version number in this run.
- A better candidate project than PgCat in this space: its successor PgDog is covered in
  `findings/05-pgdog.md`. I met no other.
