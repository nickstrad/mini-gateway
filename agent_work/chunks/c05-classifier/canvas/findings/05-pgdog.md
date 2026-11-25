# PgDog (Rust): the real PostgreSQL parser in a pooler

Cluster 05. Research only. All code citations are paths in the local clones listed under Sources,
at the SHAs recorded there. Line numbers are from those SHAs. Tags: **VERIFIED IN CODE**,
**DOCS ONLY**, **COULD NOT FIND**, and **INFERRED** (a consequence I worked out by reading code
paths together. I did not run it).

## Sources

| Project | URL | Commit SHA read | Commit date | Local/web |
| --- | --- | --- | --- | --- |
| PgDog | https://github.com/pgdogdev/pgdog | `1d1d861f3b1f69ec182580cb1a5bf1682eced4a0` | 2026-11-25 | LOCAL SOURCE `repos/pgdog` |
| pg_raw_parse (PgDog's parser crate) | https://github.com/pgdogdev/pg_raw_parse | `a4784fc603563d3db8e9f381347f039f39b1dd85` (the rev pinned in PgDog `Cargo.toml:25`) | 2026-10-12 | LOCAL SOURCE `repos/pg_raw_parse` |
| libpg_query (submodule of pg_raw_parse, branch `18-latest`) | https://github.com/pganalyze/libpg_query | `aa71ad962b654c1d608c687232c047e48dba6452` | 2026-09-30 | WEB READ (`gh api`: commit and `Makefile`) |
| PgDog docs (source of docs.pgdog.dev) | https://github.com/pgdogdev/docs | `cb571daa71fad8a86a32703d24acfda1eb971b64` | 2026-11-18 | LOCAL SOURCE `repos/pgdog-docs` |
| PostgreSQL `guc_tables.c`, REL_17_STABLE and REL_18_STABLE | https://github.com/postgres/postgres | branch heads on 2026-11-25 | n/a | WEB READ (`gh api`); master read locally in `repos/postgres` |

Release and activity facts (from `gh api`): latest release is **v0.1.66, published 2026-11-19**.
The changelog says "PgDog is released weekly, on Thursdays" (`CHANGELOG.md:3`). The repo was created
2024-12-27 and was last pushed 2026-11-25. It has about 5.7k stars. License: **AGPL-3.0**
(`LICENSE`). pg_raw_parse is Apache-2.0/MIT (`LICENSE-APACHE`, `LICENSE-MIT`).

**Correction to the brief's premise.** PgDog **no longer depends on pg_query.rs**. Neither its
`Cargo.toml` nor its `Cargo.lock` mentions `pg_query`. The parser is `pg_raw_parse`, a crate by the
same organisation that binds libpg_query's C structs directly and skips protobuf
(`pgdog/Cargo.toml:85`, `Cargo.toml:25`). The docs still say the default engine is
`pg_query_protobuf` (`docs/configuration/pgdog.toml/general.md:583-592`). The code's config enum
says the default is `PgQueryRaw` (`pgdog-config/src/sharding.rs` `enum QueryParserEngine`). The
config value is read (`backend/databases.rs:544`), but I found nothing that branches on it.
**The docs lag the code.**

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| PgDog | **The real PostgreSQL 18.4 parser** (libpg_query through pg_raw_parse). But in the default `query_parser = "auto"` mode, a deployment with one shard and no replicas parses **only** statements whose leading keyword matches a regex (`SET`, `RESET`, `BEGIN`, `LISTEN`, …). Everything else is forwarded unread. | **Track and replay** of parameters (client-side store, plus a per-server "believed" store, synced by diff at checkout). **Pin** the client to its server for advisory locks, temp tables and `SET pgdog.pin`. **Targeted reset** (`SET SESSION AUTHORIZATION DEFAULT; RESET ALL; pg_advisory_unlock_all(); DISCARD TEMP`) only when the server is marked dirty. **`DISCARD ALL`** only in session mode. **Emulate** LISTEN/NOTIFY on a dedicated connection. Uses **server signals** too: `ParameterStatus` and `CommandComplete` tags. | Read/write split on parse-tree node type. A `SELECT` is a read unless it has a non-SELECT CTE, a locking clause, `nextval`/`setval`, or an advisory-lock call. A transaction opened with `BEGIN` goes to the primary ("conservative" strategy). No result sharing or coalescing exists. | Deny-list for "this SELECT writes" (3 node kinds plus 2 function names plus the advisory family). Allow-list keyword regex for "parse this at all". | Read/write: a write sent to a replica. The replica raises an error; no wrong data. Session state: a **silent cross-client leak within the same (user, database) pool** whenever a change is not seen. | Parse error: **closed** (syntax error returned to the client, statement not forwarded). Unknown function in a SELECT: **open** (treated as a read). Unseen session change on a clean connection: **open** (no reset). |

## PgDog

### 1. Does it read SQL? With what?

**Parser: the real PostgreSQL grammar. VERIFIED IN CODE.**
- `pg_raw_parse` wraps libpg_query through bindgen. Its README says: "provides direct access to the
  PostgreSQL parser", and "upgrading major PostgreSQL versions only requires bumping up the
  `postgres` and `libpg_query` submodules" (`repos/pg_raw_parse/README.md`, sections "Why another
  crate" and "Design"). The submodule is `libpg_query` on branch `18-latest`
  (`repos/pg_raw_parse/.gitmodules`), pinned at `aa71ad96…`. That commit's `Makefile` has
  `PG_VERSION = 18.4` and `PG_VERSION_NUM = 180004` (WEB READ). So the grammar is
  **PostgreSQL 18.4**.
- Size: about 3.1k hand-written Rust lines in `pg_raw_parse/src` plus generated bindings, on top of
  libpg_query (C, which carries PostgreSQL's parser sources). PgDog's own SQL-handling code under
  `pgdog/src/frontend/router/parser/` is **about 15.8k lines**, mostly sharding and rewriting.
- How it keeps up with PostgreSQL releases: by bumping the libpg_query submodule (the README claim
  above). PgDog's own tracking of what the tree *means* (which node types are session state, which
  functions write) is a hand-kept list. See section 3.

**Where it is called. VERIFIED IN CODE.**
- `frontend/client/query_engine/rewrite.rs:31-46` `parse_request`. It first asks
  `cluster.use_query_parser(request)`, and only if that returns true does it call
  `Cache::get().query(...)`.
- `frontend/router/parser/cache/ast.rs:70-100` `Ast::parse_and_rewrite` → `mem.parse(query_without_comment)`.

**Is every query parsed? No. This is the decisive finding for mini-gateway's situation.**
`backend/pool/cluster.rs:637-655`:
```rust
pub(crate) fn router_needed(&self) -> bool {
    !(self.shards().len() == 1 && (self.read_only() || self.write_only()))
}
pub(crate) fn use_query_parser(&self, request: &ClientRequest) -> bool {
    match self.query_parser() {
        QueryParserLevel::Off => false,
        QueryParserLevel::On => true,
        QueryParserLevel::SessionControl | QueryParserLevel::SessionControlAndLocks => {
            self.regex_parser.use_parser(request) }
        QueryParserLevel::Auto => {
            self.multi_tenant().is_some() || self.router_needed() || self.dry_run()
                || self.prepared_statements() == &PreparedStatementsLevel::Full
                || self.regex_parser.use_parser(request) }
```
`write_only()` is true when no shard has replicas (`cluster.rs:607-615`). So with **one Postgres and
no replicas, which is exactly mini-gateway's deployment**, `router_needed()` is false. In that case
`auto` (the default, per `sharding.rs` `#[default] Auto`) parses a statement only when the regex
pre-filter says so. `frontend/regex_parser.rs:10-15, 37-50, 97-103`:
```rust
static COMMENT_PREFIX: &str = r"(?i)^\s*(?:(?:--[^\n]*\n|/\*[\s\S]*?\*/)\s*)*";
static CMD_BASE: &[&str] = &[
    "(RE)?SET", "BEGIN", "START", "COMMIT", "END", "ROLLBACK", "LISTEN", "UNLISTEN", "NOTIFY",
    "DISCARD", "ABORT",
];
static COMMON_QUERY_KEYWORDS: &[&[u8]] = &[b"SELECT", b"INSERT", b"UPDATE", b"DELETE", b"WITH"];
...
        if is_common_query(query) { return false; }
        CMD_RE.is_match(truncate_utf8(query, self.limit))
```
The advisory-lock function names are added to the regex only at level `session_control_and_locks`
(`regex_parser.rs:17-29, 93-95`). The docs confirm it: "If your PgDog deployment is unsharded and
isn't using the load balancer for read/write separation, this feature is **disabled** by default"
(`docs/features/connection-pooler/transaction-mode.md:122`). So in the single-database default,
PgDog's view of session state is a **leading-keyword allow-list over raw text**. That is the same
kind of mechanism as mini-gateway's option (b) "first keyword" check, except that PgDog does **not**
reset on release when the check misses (see section 2).

**Parse cache. VERIFIED IN CODE.** `cache/cache_impl.rs:129-132`: extended-protocol `Parse` text
(`BufferedQuery::Prepared`) goes through `parse()`, which looks in a global LRU keyed by **query text
with the routing comment stripped** (`:148-152`). Simple-protocol queries go through `simple()`:
"Parse and rewrite a statement but do not store it in the cache, because it may contain parameter
values" (`:192-209`). So **every simple Query that reaches the parser is parsed fresh**. The cache
key is text, not a fingerprint. The only fingerprint-like step is `normalize`, used in dry-run stats
(`:255-260`).

**Measured or claimed overhead.**
- DOCS ONLY: "When using `simple` protocol, PgDog can't cache statements (since they include
  parameters), so the overhead of the parser is noticeable. When using `extended` and `prepared`
  protocols, the cache is doing its job and the parser overhead is almost entirely eliminated"
  (`docs/architecture/benchmarks.md:92-94`). No number is given for PgDog itself.
- DOCS ONLY (pg_raw_parse README, measured on an M1 Max): parsing a 10-node query takes 1.12 µs
  versus 20.4 µs with pg_query.rs, and a 100-node query takes 5.7 µs versus 107 µs. That speedup
  is the stated reason the crate exists.
- The `auto` regex fast path exists to avoid paying even this cost on unsharded deployments.

**Parse error → fail closed. VERIFIED IN CODE.** `frontend/client/query_engine/mod.rs:177-185`:
```rust
if let Err(e) = self.parse_request(context, client_request) {
    self.error_response(context, client_request, ErrorResponse::syntax(e.to_string())).await?;
    return Ok(QueryEngineResult::Done(context.transaction()));
}
```
The statement is not forwarded. INFERRED: syntax that is valid on a newer server but not in the
PostgreSQL 18.4 grammar gets rejected by the proxy. On the regex path, a statement the regex does
not select is never parsed, so it cannot fail to parse.

### 2. Session-state safety: the mechanisms

PgDog uses several mechanisms together. Each one below has its trigger, its code, and its default.

**(i) Track and replay of parameters, not reset. VERIFIED IN CODE.**
- `SET` / `RESET` / `SET … DEFAULT` becomes `Command::Set`, taken from the tree's `VariableSetStmt`
  (`router/parser/query/mod.rs:395`, `query/set.rs:12-33`). `RESET ALL` becomes `Command::ResetAll`.
  `SET TRANSACTION` (`VAR_SET_MULTI`) is forwarded as an ordinary query.
- The query engine applies the parameter to the **client's** `ParameterStore`. If no server is
  attached, it **answers the client itself without sending anything to Postgres**
  (`query_engine/set.rs:64-83`):
  ```rust
  if self.backend.connected() {
      // The SETs are recorded on the server when it replies (see `record_sets`).
      self.execute(context, client_request, None).await?;
  } else {
      self.fake_command_response(context, &client_request.messages, &FakeResponse::new(commands)).await?;
  }
  ```
- Each `Server` keeps `client_params`, its belief about what the connection currently has. At
  checkout, `Server::link_client` (`backend/server.rs:752-805`) computes
  `self.client_params.sync(params)`. That is a diff: reset the names the server has and the client
  lacks, then `SET` the ones that differ (`parameters/store.rs:298-337`). It runs those statements and
  then trusts the result. Docs: "PgDog tracks connection parameters for each client and updates
  connection settings before giving a connection to each client … server parameters are updated
  only if they differ" (`transaction-mode.md:67-69`).
- `record_sets` (`query_engine/query.rs:450-456`) updates the server's belief when the server
  confirms a SET. A failed SET marks the server dirty (`query.rs:458-465`).
- **Default: on.** Parameters are not reset at checkin. The next client gets a diff instead.

**(ii) Server-reported signals. VERIFIED IN CODE.**
- `ParameterStatus` (`'S'`) from the server is collected in `server.changed_params`
  (`server.rs:682-685`). At the end of the transaction it is copied into the **client's** parameter
  store (`query.rs:502-514`). Session identity (`role`, `session_authorization`) is excluded.
- `CommandComplete` tags are inspected (`server.rs:734-749`). On `PREPARE`/`DEALLOCATE` it sets
  `sync_prepared`. On `DISCARD ALL` it clears the tracked prepared statements and the parameter
  belief. On `RESET` it clears the parameter belief except identity.
- `sync_prepared` makes recovery **ask the server**:
  `SELECT name FROM pg_prepared_statements` (`server.rs:1158-1171`, called from
  `pool/recovery.rs:168-176`). This **records** the names. It does not drop them.

**(iii) Pin on detect. VERIFIED IN CODE.** `query_engine/lock.rs:7-15`:
```rust
let locked = self.advisory_locks.locked() || !self.temp_tables.is_empty() || self.manual_lock;
self.backend.lock(locked);
```
`Connection::lock(true)` also marks the binding dirty (`backend/pool/connection/mod.rs:498-503`).
So when the pin is released, the connection goes through the dirty cleanup in (iv).
- Advisory locks are found by walking the tree for `FuncCall` (`router/parser/statement.rs:581-601`).
  The name table is `advisory_lock/mod.rs:104-125`: the session and xact families, `try_` variants,
  `unlock`, and `unlock_all`. For `pg_try_advisory_lock` it reads the **result DataRow** and checks
  `t`/`f`, so a failed try does not pin (`lock.rs:17-55`).
- **Gap. INFERRED from `AdvisoryLocks::merge`, `query_engine/advisory_lock.rs:12-28`.** A lock is
  tracked only `if let Some(id) = lock.id && lock.scope == LockScope::Session`. The parser returns
  `id: None` for keys it cannot evaluate, for example `pg_advisory_lock((SELECT 42))`. The test at
  `advisory_lock/test.rs:125-130` confirms that `id` is `None` there. So **an advisory lock with a
  computed key is not pinned.** The connection returns to the pool still holding the lock. It is
  not dirty, so no `pg_advisory_unlock_all()` runs.
- Temp tables: `CREATE TEMP TABLE` and `CREATE TEMP SEQUENCE` are detected through
  `RangeVar.relpersistence == 't'` (`query/ddl.rs:35-65`). `CREATE TEMP TABLE … AS`, `CREATE TEMP VIEW`
  and `SELECT … INTO TEMP` go through arms (`ddl.rs:115-125`) that **set no `temp_table` change**.
  `CREATE TABLE pg_temp.t` carries `relpersistence = 'p'` at parse time (PostgreSQL resolves
  `pg_temp` later, in analysis), and PgDog never matches `pg_temp` in DDL (grep finds it only in
  `sequence.rs` and tests). VERIFIED that those arms set nothing. INFERRED that those forms do not
  pin.
- `SET pgdog.pin = true` pins by hand (`query_engine/set.rs:145-160`).

**(iv) Reset, but only for dirty or session-mode connections. VERIFIED IN CODE.**
`backend/pool/cleanup.rs:14-23, 65-74`:
```rust
static DIRTY: Lazy<Vec<Query>> = Lazy::new(|| vec![
    Query::new("SET SESSION AUTHORIZATION DEFAULT"),
    Query::new("RESET ALL"), // Reset all other parameters.
    Query::new("SELECT pg_advisory_unlock_all()"), // Remove all advisory locks.
    Query::new("DISCARD TEMP"), // Drop all temporary tables.
]);
...
let mut clean = if guard.reset { Self::all() }          // DISCARD ALL
    else if server.dirty() { Self::parameters() }       // DIRTY list above
    else if server.schema_changed() { Self::prepared_statements() } // DEALLOCATE ALL
    else { Self::none() };
```
`guard.reset` is set only in session mode (`pool/connection/cluster_connection.rs:184-186`). "Dirty"
is set by a pin, a failed SET, or a failed param sync (`server.rs:773`). **A clean connection in
transaction mode is checked in with no reset at all.** Recovery is triggered only by
`in_transaction || cleanup.needed() || sync_prepared || needs_drain` (`pool/recovery.rs:22-25`).
Note that the DIRTY list omits `DEALLOCATE ALL`, `UNLISTEN *` and `CLOSE ALL`. Those are covered
only by `DISCARD ALL` in session mode.

**(v) Emulate the feature in the proxy. VERIFIED IN CODE and DOCS.**
- LISTEN / NOTIFY / UNLISTEN in transaction mode are intercepted (`query/mod.rs:432-471`,
  `query_engine/mod.rs:282-314`). PgDog listens on a dedicated connection and fans out with a
  broadcast channel ("PgDog will register itself with Postgres on the requested channel, on the
  client's behalf. It does that over a dedicated server connection", `docs/features/connection-pooler/pub_sub.md`).
  If pub/sub is not configured, LISTEN returns `PubSubDisabled` (`backend/pool/shard/mod.rs:114`), so
  it **fails closed**.
- Simple-protocol `PREPARE`/`EXECUTE` are renamed to global `__pgdog_N` names only when
  `prepared_statements = "full"`. The default is `extended`, in which "Statements are forwarded
  as-is" for simple `PREPARE` (`docs/configuration/pgdog.toml/general.md:436-443`). Extended-protocol
  `Parse` is renamed into a global cache (`docs/features/connection-pooler/prepared-statements.md:13-28`).

**(vi) Pool scope.** Pools are keyed by `User` (user and database): `backend/databases.rs:237`
(`databases: HashMap<User, Cluster>`). So a leak stays inside one login, as in PgBouncer.
Mini-gateway cannot borrow this protection, because its pool is shared across tenants.

### 3. Read/write and shareability decisions

**No result sharing, caching or coalescing exists.** A grep for `coalesc`, `result_cache` and
`query_cache` finds only unrelated hits in replication, stats and config. So D5 has no counterpart
in PgDog. **COULD NOT FIND** any such feature.

**The read/write rule. VERIFIED IN CODE.** The parser routes on the root node type
(`query/mod.rs:394-494`). `SelectStmt` goes to `select()`. `Insert`/`Update`/`Delete`/`Copy FROM`/DDL
are writes. `PREPARE` is routed by the type of its inner statement (`query/prepare.rs:19-36`).
`EXPLAIN` is routed by its inner statement (`query/explain.rs:20-35`). Inside a SELECT, the walk
sets `mutates` (`statement.rs:581-620`, `function.rs:3, 31-37`, `query/select.rs:37, 96-98`):
```rust
const WRITE_ONLY: &[&str] = &["nextval", "setval"];                       // function.rs:3
walk.mutates |= behavior.writes;                                           // any FuncCall named so
walk.mutates |= !matches!(cte.ctequery(), Node::SelectStmt(_));            // CommonTableExpr
Node::LockingClause(_) => { walk.mutates = true; ... }                     // FOR UPDATE/SHARE
mutates |= !advisory_locks.is_empty();                                     // select.rs:96
let writes = self.write_override || mutates;                               // select.rs:98
```
`write_override` (`router/parser/context.rs:99-114`) is true inside a read-write or implicit
transaction under the default "conservative" strategy, or when the role hint says primary. So:
- this is a **deny-list** of write signals. Function volatility is **not** consulted, and there is no
  catalog lookup. A user function `f()` that writes is classified as a read.
- Cost of a miss: the statement runs on a hot-standby replica and Postgres raises "cannot execute …
  in a read-only transaction". That is an error, not wrong data. Replicas enforce read-only, which
  is the (c2) idea applied through physical replicas. Docs list only FOR UPDATE and write CTEs as
  "edge cases" (`docs/features/load-balancer/index.md:100-125`).
- Multi-statement simple queries: "only the first one is used for routing" (`query/mod.rs:374-380`).
  `check_multi_query_statement` (`query/split.rs:11-73`) splits mixed SET-plus-other batches and
  forwards the rest unsplit when there is one shard. The code says so in its own TODO:
  `// We can still mis-route things here, e.g., SELECT 1; INSERT INTO [...]; will use the first
  statement for routing and send the whole thing to a replica, causing an error.` (`split.rs:33-36`).

### 4. Documented limitations, in the project's own words

- "PgDog automatically detects `SET` commands and uses the native PostgreSQL query parser to extract
  the GUC/session variable … For deployments that don't normally need the parser (i.e. unsharded,
  replica-only deployments, or a primary with no replicas), PgDog can selectively enable its parser
  for `SET` commands only." (`docs/features/connection-pooler/transaction-mode.md:73-75`,
  https://docs.pgdog.dev/features/connection-pooler/transaction-mode/)
- Advisory locks: "This feature requires the query parser to be enabled … If your PgDog deployment
  is unsharded and isn't using the load balancer for read/write separation, this feature is
  **disabled** by default." (same file, `:118-122`)
- Simple `PREPARE`: "Statements sent over the simple protocol are not checked against the global
  cache … Since this requires PgDog to parse _each_ incoming query, and that's computationally
  expensive, this feature is **disabled** by default." (`prepared-statements.md:108-109`)
- Comparison page: "Session evariables in transaction mode" is rated *Partial* for PgBouncer and
  PgCat and a full check mark for PgDog (`docs/architecture/comparison.md:24`). That is a claim, not
  a limitation.
- **COULD NOT FIND** any documented limitation on temp tables, `set_config()` inside functions,
  session changes hidden in functions, or unresolved advisory-lock keys. The docs do not mention
  temp tables in transaction mode at all.

### 5. Language, licence, maturity, dependency weight

Rust, AGPL-3.0 (parser crate Apache/MIT). Weekly releases since late 2024; v0.1.66 on 2026-11-19;
a commit landed on the day of reading. For a Go proxy, reuse means: (1) the parser is
**libpg_query** through cgo. The Go equivalent of pg_raw_parse is `pg_query_go`, assessment
option (c1). (2) The *mechanisms* (diff-sync, ParameterStatus capture, CommandComplete inspection,
pin on detect, targeted dirty cleanup) are a few hundred lines of logic with no dependency, and can
be re-implemented. Copying PgDog code into a non-AGPL project is not an option.

### Probe table

Two columns, because the behaviour depends on whether the parser runs.
**ON** = sharded, read/write split, or `query_parser = "on"`.
**AUTO-1** = default `auto` with one primary and no replicas. This is mini-gateway's shape.

| Probe | ON (parser runs) | AUTO-1 (regex pre-filter) |
| --- | --- | --- |
| P1 `SET search_path = x` | Parsed → `Command::Set`. Stored on the client. Faked if no server is attached, executed and `record_set` if one is. Replayed by diff at the next checkout (`query/set.rs:12-33`, `query_engine/set.rs:64-83`, `server.rs:752-805`). **Handled.** | Regex `(RE)?SET` matches → parsed → same as ON. **Handled.** |
| P2 `SELECT set_config('search_path','h',false)` | `extract_set_config` matches only if the target list is **exactly one** `FuncCall` named `set_config`/`pg_catalog.set_config` with **constant** arguments. Then it becomes a `Command::Set` and is tracked (`query/mod.rs:397, 741-761`, `set_config.rs:13-23`). Non-constant arguments → forwarded as a plain write and **not tracked**. | Starts with `SELECT` → `is_common_query` → **not parsed**, forwarded. On PG ≥ 18, `search_path` is `GUC_REPORT` (REL_18 `guc_tables.c` adds `GUC_REPORT`; REL_17 lacks it), so the server's ParameterStatus updates the *client's* store (`query.rs:502-514`). But the *server's* belief `client_params` is not updated from ParameterStatus (only `record_set`, `link_client`, and the `RESET`/`DISCARD ALL` tags write it: `server.rs:740-813`). **INFERRED: the next client in the same pool gets `search_path = h` silently.** For a GUC that is not `GUC_REPORT` (for example `work_mem`), no signal exists at all. |
| P3 `SELECT f()`, where f runs SET or creates a temp table | Parsed as a plain SELECT → **read**. Nothing tracked. A temp table is not pinned. A GUC_REPORT setting reaches the client via ParameterStatus, with the same stale-server-belief issue as P2. **INFERRED: leak.** | Not parsed. Same outcome. |
| P4 lexical tricks (`SELECT E'ok'\n'\' \|\| set_config(...) --'`) | The real PG 18.4 scanner handles the lexical layer exactly. The tree is a `SelectStmt` whose target is an operator expression, not a lone `FuncCall`, so P2's matcher misses it → read, untracked (as P2's non-constant branch). The **grammar** layer still misses. | Starts with `SELECT` → not parsed → untracked. Lexical tricks matter only for the leading-keyword regex. `COMMENT_PREFIX` skips leading `--` and `/* */` comments. Nested block comments are not modelled (`[\s\S]*?\*/` stops at the first `*/`). INFERRED: the worst case is a missed SET. |
| P5 `PREPARE p AS …` | Default `prepared_statements = "extended"`: forwarded as-is. The `CommandComplete PREPARE` tag sets `sync_prepared` → at checkin PgDog reads `pg_prepared_statements` and **records** `p`. The statement stays on the server, visible to the next client. With `"full"`: renamed to a global `__pgdog_N`. | Not in the regex → not parsed → same as ON with `extended`. |
| P5 `LISTEN c` | Intercepted. Served on a dedicated listener connection (pub/sub), or an error if pub/sub is not configured. **Never touches the pooled connection.** | `LISTEN` is in the regex → same. |
| P5 `CREATE TEMP TABLE t(...)` | `relpersistence == 't'` → `TempTableChange::Create` → **pin**. On unpin: dirty cleanup with `DISCARD TEMP`. | Not in the regex → **not parsed, not pinned**. The connection returns to the pool clean, with `t` on it. INFERRED: leak to the next client of the same pool. |
| P5 `SELECT pg_advisory_lock(1)` | Detected → **pin** until unlock or disconnect. Unpin → dirty cleanup `pg_advisory_unlock_all()`. A computed key (`pg_advisory_lock((SELECT 1))`) gives `id = None` → **not pinned** (INFERRED, `advisory_lock.rs:21-26`). | Default `auto`: **not parsed, not pinned** (documented). With `session_control_and_locks`: the regex finds the function name anywhere in the text → parsed → as ON. |
| P5 `DECLARE c CURSOR WITH HOLD …` | No arm for `DeclareCursorStmt` → falls to `ddl()` (`query/mod.rs:494`), routed as a write. **COULD NOT FIND** any tracking or pinning. The dirty list has no `CLOSE ALL`. | Not parsed. Same. |
| P6 `SELECT 1; SET search_path = x` | More than one statement → `try_multi_set` → `MultiStatementMixedSet` → split into separate queries via `deparse` (`split.rs:63-70, 76-88`). The SET is tracked. Other mixes either route by the first statement or return `MultiStatementSafety`. | Starts with `SELECT` → not parsed → forwarded whole. The SET runs on the server untracked (search_path partly rescued by ParameterStatus, as P2). |
| P7 `SELECT nextval('s')` | `WRITE_ONLY` → primary. | n/a (one server, no split) |
| P7 `WITH d AS (DELETE …) SELECT * FROM d` | Non-SELECT CTE → write. | n/a |
| P7 `SELECT * FROM t FOR UPDATE` | `LockingClause` → write. | n/a |
| P7 `SELECT now()` | Read. There is no cache, so volatility does not matter. | n/a |
| P7 `SELECT * FROM v` (view over a volatile or writing function) | Read. No catalog lookup. A write inside fails on the replica. | n/a |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | `explain()` dispatches on the inner `DeleteStmt` → write. | n/a |

## Claims of completeness

- README: "Unlike PgBouncer, PgDog can parse and handle `SET` statements and startup options,
  ensuring session state is set correctly when sharing server connections between clients with
  different parameters." (`README.md:114`). The comparison page gives PgDog a full check mark for
  "session variables in transaction mode" (`comparison.md:24`). Docs: "To avoid session-level
  state leaking between clients, PgDog tracks connection parameters …" (`transaction-mode.md:67`).
- **Does the claim survive the code?** For **visible `SET`/`RESET` statements**: yes. They are
  parsed with the real grammar, tracked per client, and replayed by diff. That is a real improvement
  over PgBouncer. Beyond that, no:
  - (1) In the default unsharded mode, parsing is gated by a leading-keyword regex. Any session
    change that does not start with `SET`/`RESET`/… is not seen: `SELECT set_config`, temp tables,
    session advisory locks, a SET after a SELECT in one batch.
  - (2) Even with the parser on, changes inside functions, `set_config` with non-constant
    arguments, `CREATE TEMP … AS`, `pg_temp.` DDL, computed advisory-lock keys and holdable
    cursors are not tracked.
  - (3) A clean connection is never reset in transaction mode, so nothing catches what tracking
    missed.
  - (4) ParameterStatus is applied to the client but not to the server's belief, so even a
    server-reported change can stay on the connection (INFERRED).
- PgDog does **not** claim to decide read/write completely. It names FOR UPDATE and write CTEs as
  edge cases it handles. Replicas enforce read-only, so a miss costs an error.

## Mapping to options (a) (b) (c1) (c2)

- **(c1)** is what PgDog does when its parser is on: the real PostgreSQL parser (libpg_query, PG
  18.4). It confirms the assessment's prediction exactly. The lexical layer is closed, the grammar
  layer is still a hand-kept list (`WRITE_ONLY = ["nextval","setval"]`, the advisory name table,
  `relpersistence == 't'`, `set_config` with constant arguments only), and the catalog layer
  (`SELECT f()`) is not attempted.
- **(a)-like** at the grammar level: deny-lists of write and session signals over the tree.
- **Not (b).** PgDog has the reset machinery (`cleanup.rs`) but runs it only on connections it
  *knows* are dirty. It **trusts a clean connection**, which is the opposite of (b)'s "reuse as-is
  only if known clean, otherwise `DISCARD ALL`". In the default unsharded mode, its gate is a
  keyword allow-list (as in (b)) with no reset behind it. That is (b)'s cheap half without its
  safety half.
- **(c2)-like** for reads only by accident of topology: writes that slip to a replica are refused
  by the hot standby. It never wraps statements in `BEGIN READ ONLY` itself.
- For mini-gateway's shared-across-tenants pool, PgDog's design would be unsafe in exactly the cases
  the assessment worries about. PgDog relies partly on pools being per (user, database).

## Mechanisms not in the assessment

1. **Track and replay by diff, with a per-connection "believed state".** Instead of resetting, keep
   for each server connection a map of the GUCs the proxy believes it has. At checkout, issue only
   the `RESET x` / `SET y` statements that turn the server's map into the client's map
   (`parameters/store.rs:298-337`). In a Go proxy this is a `map[string]string` per pooled connection
   plus a per-client map. It lets a tenant keep `SET` semantics across transactions (mini-gateway
   currently rejects SET) at the cost of zero round trips when the maps agree. Its soundness depends
   on the belief being right, which is the weak spot (see 2).
2. **Use the server's ParameterStatus messages as a free change detector.** Postgres pushes
   `ParameterStatus` whenever a `GUC_REPORT` setting changes, *including from inside functions and
   `set_config`*. That is a protocol signal, not text. The set is small: on master and REL_18 it is
   `application_name, client_encoding, DateStyle, default_transaction_read_only, in_hot_standby,
   integer_datetimes, IntervalStyle, is_superuser, scram_iterations, search_path, server_encoding,
   server_version, session_authorization, standard_conforming_strings, TimeZone` (from
   `repos/postgres/src/backend/utils/misc/guc_parameters.dat`). **`search_path` joined the set in
   PostgreSQL 18** (REL_18_STABLE `guc_tables.c` has `GUC_REPORT`; REL_17_STABLE does not). For
   mini-gateway: any ParameterStatus that the proxy did not cause, received during a lease, is proof
   the lease changed reported session state. Mark the connection not-clean and reset it. Cost: one
   switch case in the reply loop. It cannot see non-reported GUCs (`work_mem`, `statement_timeout`),
   temp tables or locks, so it complements (b) and cannot replace it. PgDog uses it only to update
   the client, and does not distrust the server connection. Mini-gateway should do the opposite.
3. **Inspect CommandComplete tags as server-confirmed facts.** The tag is what the server actually
   executed, independent of how the text was spelled: `PREPARE`, `DEALLOCATE`, `DISCARD ALL`, `RESET`,
   and also `SET`, `LISTEN`, `CREATE TABLE`, `DECLARE CURSOR`, `SELECT n`. PgDog uses these tags to
   invalidate or resync its tracking (`server.rs:734-749`). For mini-gateway, a tag allow-list is a
   cheap, unfoolable second gate: a lease is "known clean" only if every CommandComplete tag in it
   is in {`SELECT n`, `INSERT 0 n`, `UPDATE n`, `DELETE n`, `MERGE n`, `BEGIN`, `COMMIT`, `ROLLBACK`,
   `SHOW`, `EXPLAIN`…}. A `SET`, `PREPARE`, `LISTEN`, `CREATE TABLE` (temp or not), `DECLARE CURSOR`
   or `DO` tag forces a reset. This catches every top-level spelling trick, multi-statement batches,
   and lexer bugs, because it reads the server's account. It still misses changes *inside*
   functions, whose tag is just `SELECT 1`. That residue is the same as (b)'s, and (b)'s substring
   taint list or strict mode covers it.
4. **Ask the server at checkin.** PgDog queries `pg_prepared_statements` when a tag says the
   prepared set changed. Generalised: a single catalog probe at release can verify the
   high-risk kinds of state directly. For example: `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE
   locktype='advisory' AND pid=pg_backend_pid()), EXISTS(SELECT 1 FROM pg_namespace WHERE
   oid=pg_my_temp_schema()), EXISTS(SELECT 1 FROM pg_prepared_statements), EXISTS(SELECT 1 FROM
   pg_cursors), EXISTS(SELECT 1 FROM pg_listening_channels())`. Settings can be checked with
   `SELECT count(*) FROM pg_settings WHERE source='session'`. This is one round trip that answers
   "is this connection clean?" from the catalog, including changes made inside functions. It is
   about the price of `DISCARD ALL`, so it is worth doing only if a dirty connection is expensive to
   reset. More importantly, it **tests** the cheap gate: run it in CI or sampled in production to
   detect when the keyword and tag gates wrongly called a connection clean. I did not find this
   generalised probe in PgDog; PgDog does only the prepared-statement probe.
5. **Answer session commands in the proxy and move the feature off the pooled connection.** PgDog
   answers `SET` without contacting Postgres when no server is attached (`query_engine/set.rs:76-82`).
   It serves `LISTEN`/`NOTIFY` from one dedicated listener connection with an in-proxy fan-out. Both
   remove the state from pooled connections entirely, instead of rejecting it or resetting it. This
   is relevant if mini-gateway later wants to *support* rather than refuse these features.
6. **Read the result row to decide a pin.** For `pg_try_advisory_lock`, PgDog inspects the DataRow
   value (`t`/`f`) before pinning (`lock.rs:17-55`). This is a result-value signal and could be
   reused for any function whose return value says whether state was taken.
7. **Targeted cleanup instead of `DISCARD ALL`:** `SET SESSION AUTHORIZATION DEFAULT; RESET ALL;
   SELECT pg_advisory_unlock_all(); DISCARD TEMP` (`cleanup.rs:14-23`). It keeps prepared statements
   (which PgDog shares globally) while clearing the rest. This is a **cautionary example** for
   mini-gateway: the list omits `DEALLOCATE ALL`, `CLOSE ALL` and `UNLISTEN *`. A hand-picked cleanup
   list is itself a deny-list. `DISCARD ALL` is PostgreSQL's maintained list and is the safer choice.

## Could not verify

- **No runtime confirmation.** The leak scenarios marked INFERRED were not executed: P2/P3 server
  belief going stale after ParameterStatus, an unresolved advisory key not pinning, temp tables in
  AUTO-1, `CREATE TEMP … AS` and `pg_temp.` DDL not pinning. Building PgDog was out of scope.
- **COULD NOT FIND** any code that updates `Server::client_params` from ParameterStatus. Searched
  every `client_params` use in `backend/server.rs`.
- **COULD NOT FIND** handling of `DECLARE … WITH HOLD`, `CLOSE`, `UNLISTEN *` or `DEALLOCATE` in the
  transaction-mode dirty cleanup. The only matching handling is `DISCARD ALL` in session mode.
- **COULD NOT FIND** a measured PgDog-level parser overhead number. There is only the qualitative
  docs statement and pg_raw_parse's microbenchmarks.
- **COULD NOT FIND** any branch in PgDog's code on the `query_parser_engine` setting. The docs
  describe two engines; the lockfile contains only pg_raw_parse.
- **COULD NOT FIND** PgDog's first release date cheaply. The repo was created 2024-12-27, and tags run
  from `v0.1.x` to v0.1.66.
- The brief's belief that PgDog uses "pg_query.rs" is outdated. It uses pg_raw_parse.
  Its belief that PgDog "resets servers on checkin" is true only for dirty or pinned connections
  and for session mode (see section 2(iv)).
