# Managed and caching proxies: RDS Proxy, Cloudflare Hyperdrive, ReadySet (+ pgbouncer-rr, Prisma Accelerate)

Canvas 11 for mini-gateway chunk 5. Written 2026-11-25 by a fresh Opus research agent. I
changed nothing under `/root/Software/mini-gateway*`.

Evidence labels: **VERIFIED IN CODE** (file:line at the recorded commit), **DOCS ONLY**
(quoted from the URL given, fetched 2026-11-25), **COULD NOT FIND**. Two products are closed
source (RDS Proxy, Hyperdrive), so every claim about them is DOCS ONLY: a vendor's statement
about what it does, not proof of how it does it.

## Sources

| Project | URL | Commit SHA / doc version | Commit date | Newest release | Local / web |
| --- | --- | --- | --- | --- | --- |
| Amazon RDS Proxy | docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-proxy-pinning.html, rds-proxy.howitworks.html, rds-proxy-creating.html, rds-proxy-modifying-proxy.html, APIReference/API_ConnectionPoolConfiguration.html | closed source; live docs fetched 2026-11-25 | — | managed service | WEB READS (copies in `work/11-managed-and-caching-proxies/web/rds-*.md`) |
| Cloudflare Hyperdrive | developers.cloudflare.com/hyperdrive/concepts/query-caching/ (the brief's `/configuration/query-caching/` now redirects here), /concepts/connection-pooling/, /concepts/how-hyperdrive-works/, /reference/supported-databases-and-features/; blog.cloudflare.com/how-hyperdrive-speeds-up-database-access/, /postgres-named-prepared-statements-supported-hyperdrive/, /hyperdrive-making-regional-databases-feel-distributed/ | closed source; live docs fetched 2026-11-25 | — | managed service | WEB READS (`web/hd-*.md`) |
| ReadySet | https://github.com/readysettech/readyset (sparse, blobless clone in `repos/readyset`) | `f3270de3edf83d5ba37873e752f6f7b91207a9e7` | 2026-11-25T14:42:00Z | `stable-261124` (2026-11-24) | LOCAL SOURCE (crates: readyset-adapter, readyset-psql, readyset-sql-parsing, readyset-sql, readyset-sql-passes, readyset-shallow, dataflow-expression) |
| awslabs/pgbouncer-rr-patch | https://github.com/awslabs/pgbouncer-rr-patch | `56d51abe2212f1fefa0937ff7532ad8b50b0eec8` | 2025-09-03 | no releases | WEB READS through `gh api` (README.md, routing_rules.py) |
| Prisma Accelerate | prisma.io/docs/accelerate/caching, /accelerate/reference/api-reference | closed source | — | being retired 2026-12-01 (its docs say so) | WEB READS |
| PostgreSQL (cross-check only) | `repos/postgres` (already cloned by another canvas) | `4483697` | 2026-11-25 | — | LOCAL SOURCE |

## Summary rows

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| RDS Proxy | Something that recognises statements and function names. The docs don't say what (DOCS ONLY). Transaction ends are read from the protocol, not from text. | **Pin** the client to its server connection for the rest of the session. Opt-out filter `EXCLUDE_VARIABLE_SETS` exists, but "PostgreSQL doesn't support this setting". | None (no cache, no read routing on the default endpoint) | Deny-list of pinning triggers, plus a documented "can't be certain → pin" rule | Undetected change (for example inside a function) stays on a connection that other sessions reuse: a cross-session leak. AWS documents this for stored routines. | Documented as closed ("can't be certain → pin"). Functions are documented as open. |
| Hyperdrive | A parser of unstated kind ("parses the query", "uses database protocols") (DOCS ONLY) | **Reset on release** ("the connection is `RESET`"). Prepared statements: track and replay. Everything else: "not supported" (documented, not rejected). | Cache only non-mutating queries without STABLE/VOLATILE functions. TTL cache: no invalidation on write. | Rule stated as an allow-list ("Only functions designated as `IMMUTABLE` … are compatible"). Implementation unknown. | Stale or wrong cached result shared across one customer's clients. A leaked session lock blocks other clients (documented). | Unknown for parse failures (COULD NOT FIND) |
| ReadySet | A real SQL parser: Rust `sqlparser-rs` fork (prod preference) plus its own `nom-sql`. Neither is PostgreSQL's parser. | **One private upstream connection per client session**, so state never crosses clients. Plus a mirror of a few settings, used for cache keys. Unsupported `SET` → error (default), or proxy everything for the rest of the session. | Deep cache: only queries its own dataflow engine can plan and execute. Shallow (TTL) cache: explicit `CREATE CACHE`/hint, or in-request auto-creation gated by a default-deny pass. | **Allow-list by construction**: functions must be IMMUTABLE builtins, generated from `pg_proc.provolatile`. Writes go upstream. | Performance (query proxied upstream), or staleness inside a declared TTL. A misjudged cacheable query returns cached rows. | **Closed**: parse error, unsupported construct or unknown function → upstream on the client's own connection |
| pgbouncer-rr | User Python regex over the raw query text | Inherits PgBouncer (no SQL reading) | Routing only, by user regex | User-written | Wrong server for a query | Open (no match → current pool) |
| Prisma Accelerate | Nothing: the client marks each cacheable query | n/a (cache) | Client-declared per query (`cacheStrategy`); writes unsupported | Client hint | Staleness inside the declared TTL | n/a |

---

## Amazon RDS Proxy (closed source — DOCS ONLY throughout)

### 1. Does it read SQL?

It must read something. It pins on named statements (`SET`, `PREPARE`, `LISTEN`, …) and on
named functions inside statements (`nextval`, `pg_advisory_lock`, `set_config`), so it
inspects statement text below the first keyword. AWS never says how: no parser named, no
lexer rules, nothing on comments, quoting or dollar quotes for PostgreSQL. **COULD NOT FIND**
any description of the detector for PostgreSQL.

Indirect clues (DOCS ONLY, rds-proxy-pinning.html):
- A size limit suggests a bounded buffer or parse budget: "Any statement with a text size
  greater than 16 KB causes the proxy to pin the session." (all engines).
- On MySQL it admits a parser that does not descend into executable comments: "Queries with
  executable comments for MySQL (syntax /\*! ... \*/) … cause pinning. RDS Proxy cannot parse
  SQL embedded in these comments to track session state changes."
- Transaction ends come from the protocol, not text (rds-proxy.howitworks.html): "RDS Proxy
  detects when a transaction ends through the network protocol used by the database client
  application. Transaction detection doesn't rely on keywords such as `COMMIT` or `ROLLBACK`
  appearing in the text of the SQL statement." This is mini-gateway's decision D4/row 2:
  use the ReadyForQuery status byte.

How it keeps up with PostgreSQL releases: **COULD NOT FIND**.

### 2. Session-state safety

Mechanism: **pin**. Default on, triggered by detection, and also by uncertainty
(rds-proxy.howitworks.html):

> "In some cases, RDS Proxy might detect a database request that makes it impractical to move
> your session to a different connection. In these cases, it turns off multiplexing for that
> connection the remainder of your session. The same rule applies if RDS Proxy can't be
> certain that multiplexing is practical for the session. This operation is called *pinning*."

So the stated fail direction is **unsure → pin** (cost: efficiency). The explainer's "from
memory: errs towards pinning" is confirmed by this quote.

The documented PostgreSQL pinning list (rds-proxy-pinning.html, "Conditions that cause
pinning for RDS for PostgreSQL"), verbatim items:
- "Using `SET` commands. RDS Proxy does not pin connections when you use `SET LOCAL` inside an
  explicit transaction block (between `BEGIN` and `COMMIT`). This exemption does not apply to
  `set_config(name, value, true)`, which does cause pinning."
- "Using `PREPARE`, `DISCARD`, `DEALLOCATE`, or `EXECUTE` commands to manage prepared statements."
- "Creating temporary sequences, tables, or views."
- "Declaring cursors." (all cursors, not only `WITH HOLD`)
- "Discarding the session state."
- "Listening on a notification channel."
- "Loading a library module such as `auto_explain`."
- "Manipulating sequences using functions such as `nextval` and `setval`."
- "Interacting with locks using functions such as `pg_advisory_lock` and
  `pg_try_advisory_lock`." With the note: "RDS Proxy does not pin on transaction level
  advisory locks, specifically `pg_advisory_xact_lock`, …"
- "Setting a parameter, or resetting a parameter to its default. Specifically, using `SET`
  and `set_config` commands to assign default values to session variables."
- All engines: statements over 16 KB.

`SET` tracking: on MySQL and SQL Server, RDS Proxy *tracks* a named list of variables and
reuses a connection only for sessions with equal values ("RDS Proxy only reuses the
connection for other sessions that have the same values for those settings"). For
PostgreSQL there is no tracked list: "However, for PostgreSQL setting a variable leads to
session pinning." (Minimizing pinning section.)

"Trust me" filter: `SessionPinningFilters`, whose only value is `EXCLUDE_VARIABLE_SETS`.
API_ConnectionPoolConfiguration: "Including an item in the list exempts that class of SQL
operations from the pinning behavior. Default: no session pinning filters."
rds-proxy-creating.html: "Currently, PostgreSQL doesn't support this setting … Enabling it
might cause session variables from one connection to affect others, leading to errors or
correctness issues". So on PostgreSQL there is no opt-out.

Other documented mitigation: an **initialization query** run on every new server connection
("move the statements that set up the initial session state into the initialization query").
That is "same state on every connection" instead of "per-client state".

Reset on release: **COULD NOT FIND** any statement that RDS Proxy resets a connection
(`DISCARD ALL` or similar) when a multiplexed (unpinned) transaction ends. The docs even pin
when the *client* sends `DISCARD ALL`: "If you use connection pooling libraries with
`DISCARD ALL` query configured as a reset query, RDS Proxy pins your client connection on
release."

### 3. Read/write and shareability

None on the default endpoint (no cache, no coalescing). n/a.

### 4. Documented limitations (their words)

rds-proxy-pinning.html, PostgreSQL list:

> "Calling stored procedures and stored functions doesn't cause pinning. RDS Proxy doesn't
> detect any session state changes resulting from such calls. Make sure that your application
> doesn't change session state inside stored routines if you rely on that session state to
> persist across transactions. For example, RDS Proxy isn't currently compatible with a stored
> procedure that creates a temporary table that persists across all transactions."

This is the same limit as mini-gateway's plan §9.4 ("a function or view can hide session
changes"). AWS frames the harm as "your state may not persist". It does not mention the
other direction (the state stays on a connection another session borrows next), and it does
not say whether anything cleans it. Since no reset on release is documented, the leak
direction is unresolved from the docs: **COULD NOT FIND**.

### 5. Language, license, maturity, weight

Closed, AWS managed service, GA since 2020. Nothing to reuse in a Go proxy. Release dates
were not looked up.

### Probe table (all DOCS ONLY)

| Probe | RDS Proxy |
| --- | --- |
| P1 `SET search_path = x` | Pins ("Using `SET` commands"). `SET LOCAL` inside `BEGIN…COMMIT` does not pin. |
| P2 `SELECT set_config('search_path','h',false)` | Pins. The doc names `set_config` twice, and says even `set_config(...,true)` pins. |
| P3 `SELECT f()`, f does SET / temp table | **Not detected, no pin.** Documented limitation (quoted above). Reset on release not documented. |
| P4 lexical tricks | Unknown. The detector is not described. COULD NOT FIND. |
| P5 PREPARE / LISTEN / CREATE TEMP / pg_advisory_lock / DECLARE … WITH HOLD | All pin (each is on the list; cursors pin even without HOLD). `pg_advisory_xact_lock` does not pin. |
| P6 `SELECT 1; SET search_path = x` | No item about multi-statement queries for PostgreSQL in the current page: COULD NOT FIND. Presumably the SET inside pins, but that is not stated. |
| P7 nextval / CTE DELETE / FOR UPDATE / now() / view / EXPLAIN ANALYZE | n/a (no read/write or cache decision). Note: `nextval`/`setval` *pin*, because `currval`/`lastval` are session state. |

---

## Cloudflare Hyperdrive (closed source — DOCS ONLY throughout)

### 1. Does it read SQL?

It says it parses, without saying with what. how-hyperdrive-works: "When queries are sent via
Hyperdrive, Hyperdrive parses the query and determines whether the query is a mutating (write)
or non-mutating (read) query." concepts/query-caching: "Hyperdrive uses database protocols to
differentiate between a mutating query (a query that writes to the database) and a
non-mutating query (a read-only query)… Besides determining the difference between a `SELECT`
and an `INSERT`, Hyperdrive also parses the database wire-protocol and uses it to
differentiate between a mutating or non-mutating query."

Parser kind, size, and how it keeps up with PostgreSQL: **COULD NOT FIND**. The blog says it
is written in Rust ("a few built-in Rust data structures", named-prepared-statements post).
"Uses database protocols" might mean protocol signals (for example the server's
`CommandComplete` tag, or `BEGIN READ ONLY`-style enforcement) as well as text, but no page
says which. I could not find what they read.

### 2. Session-state safety

Mechanism: **reset on release**, plus **track and replay** for protocol prepared statements,
plus "unsupported" for the rest.

- concepts/connection-pooling: "The Hyperdrive connection pooler operates in transaction mode
  … Hyperdrive supports `SET` statements for the duration of a transaction or a query … a
  query that includes a `SET` command (`SET X; SELECT foo FROM bar;`) will also apply the
  `SET` command. When a connection is returned to the pool, the connection is `RESET` such
  that the `SET` commands will not take effect on subsequent queries."
  Whether "`RESET`" means `RESET ALL` (settings only) or `DISCARD ALL` (also temp tables,
  prepared statements, LISTEN, session advisory locks): **COULD NOT FIND**. The
  advisory-lock text below implies it is *not* `DISCARD ALL`, because a session lock
  "can remain held by a pooled connection".
- Prepared statements (blog, how-hyperdrive-speeds-up-database-access): "Hyperdrive
  implements this by keeping track of what statements have been prepared by a given client,
  as well as what statements have been prepared on each origin connection in the pool. When a
  query comes in expecting to re-use a particular prepared statement …, Hyperdrive checks if
  it's been prepared on the checked-out origin connection. If it hasn't, Hyperdrive will
  replay the wire-protocol message sequence to prepare it on the newly-checked-out origin
  connection". This covers protocol-level `Parse` only. SQL-level `PREPARE` is unsupported.
- reference/supported-databases-and-features, "Hyperdrive does not support the following
  PostgreSQL features": "SQL-level management of prepared statements, such as using `PREPARE`,
  `DISCARD`, `DEALLOCATE`, or `EXECUTE`"; "Session-level advisory locks … Hyperdrive does not
  reject these commands, but a session-level lock can remain held by a pooled connection after
  your client disconnects, blocking other clients"; "`LISTEN` and `NOTIFY`"; "Any
  modification to per-session state not explicitly documented as supported elsewhere."

So the default for everything except SET and protocol prepares is **document and shrug**.
The docs say outright that some unsupported statements are not rejected. Isolation boundary:
pools are per Hyperdrive configuration (one origin, one credential), so a leak stays inside
one customer's configuration. This is like PgBouncer's per-user pool, which mini-gateway
cannot copy.

### 3. Read/write and cacheability (closest precedent to D5)

The rule (concepts/query-caching):

> "Mutating queries (including `INSERT`, `UPSERT`, or `CREATE TABLE`) and queries that use
> functions designated as `volatile` or `stable` by PostgreSQL are not cached"
>
> "Only functions designated as `IMMUTABLE` by PostgreSQL (functions whose return value never
> changes for the same inputs) are compatible with Hyperdrive caching. If your query uses a
> `STABLE` or `VOLATILE` function, move the function call to your application code and pass
> the resulting value as a query parameter instead."

They list examples, not the full mechanism: `NOW()`, `CURRENT_TIMESTAMP`, `CURRENT_DATE`,
`CURRENT_TIME`, `LOCALTIME`, `LOCALTIMESTAMP`, `TIMEOFDAY()`, `RANDOM()`, `LASTVAL()`,
`TXID_CURRENT()`, headed "Common PostgreSQL functions that are **not cacheable** include".

As stated, the rule is an allow-list (IMMUTABLE only). Whether the code applies it to a
complete IMMUTABLE list, to a deny-list of the "common" names, or by asking the origin's
`pg_proc`: **COULD NOT FIND**. The brief expected a stated limit like "text-match on function
names causing false positives". **COULD NOT FIND** such a statement in the current docs or
the three blog posts I read.

What a miss costs: a shared wrong or stale result. Hyperdrive also accepts staleness by
design: "Hyperdrive does not purge or invalidate cached read query results when your
application writes to your database." (`max_age` 60 s by default, 1 h max;
`stale_while_revalidate` 15 s). The documented remedy is a second, cache-disabled
configuration for reads that must be fresh. The *application* decides by choosing a binding,
like a client hint.

Cache key: the buffered protocol messages ("We use the messages sent as a cache key",
named-prepared-statements post). Messages are buffered until `Sync`. Whether the key
includes session settings (`TimeZone`, `search_path`) that change a result: **COULD NOT
FIND**. Fail direction on unparsable text: **COULD NOT FIND**.

### 4. Documented limitations

Quoted under 2 (supported-databases-and-features; connection-pooling). Also: "It is not
recommended to wrap multiple database operations with a single transaction to maintain the
`SET` state."

### 5. Language, license, maturity, weight

Closed, Rust (blog). Open beta 2023-09, GA promised for early 2024 (blog
hyperdrive-making-regional-databases-feel-distributed). Nothing to reuse.

### Probe table (all DOCS ONLY)

| Probe | Hyperdrive |
| --- | --- |
| P1 `SET search_path = x` | Applies for the rest of the transaction/query, then the connection is "`RESET`" on return. Not cached (a mutating/non-SELECT). |
| P2 `set_config(...,false)` | Not documented. `set_config` is VOLATILE, so by the stated rule not cached. Whether the reset covers it depends on what "`RESET`" means (a GUC, so `RESET ALL` would cover it). |
| P3 `SELECT f()` | User functions default to VOLATILE, so by the rule not cached. A SET inside is reset (if `RESET ALL`). A temp table inside is "unsupported" and probably survives `RESET ALL`. COULD NOT FIND. |
| P4 lexical tricks | Unknown (parser not described). |
| P5 | `PREPARE`: unsupported. `LISTEN`: unsupported. temp table: unsupported (catch-all). `pg_advisory_lock`: "does not reject", lock can leak to other clients (documented). `DECLARE … WITH HOLD`: not mentioned, so the catch-all applies. |
| P6 `SELECT 1; SET search_path = x` | The SET applies (documented example `SET X; SELECT foo FROM bar;`). Whether such a batch is cached: COULD NOT FIND. |
| P7 | `nextval`: VOLATILE, not cached. `now()`: listed, not cached. CTE DELETE, `FOR UPDATE`, `EXPLAIN ANALYZE DELETE`: "mutating" detection not described, COULD NOT FIND. View over a volatile function: not addressed. If the decision is by function names in the text, the view would be cached. COULD NOT FIND. |

---

## ReadySet (LOCAL SOURCE, commit f3270de, BSL 1.1)

ReadySet is a cache that sits in front of Postgres/MySQL. It keeps a **deep** cache
(incrementally maintained dataflow, fed by the replication stream) and, since recently, a
**shallow** cache (a TTL result cache, crate `readyset-shallow`). Everything it does not
answer it proxies to the upstream database.

### 1. Does it read SQL?

Yes, with a full SQL parser that is **not** PostgreSQL's:
- `readyset-sql-parsing/src/lib.rs:115-141` (VERIFIED IN CODE). Two parsers, chosen by a
  preset. Production prefers the Rust `sqlparser-rs` (a fork pinned at
  `Cargo.toml:263`: `sqlparser = { git = "https://github.com/readysettech/datafusion-sqlparser-rs", rev = "3200b96…" }`)
  and still runs ReadySet's own `nom-sql`:
  ```rust
  /// Parse with both, prefer sqlparser, warn if nom-sql errors or differs
  BothPreferSqlparser,
  ...
  pub fn for_prod() -> Self { Self::BothPreferSqlparser }
  ```
- The shallow path parses with sqlparser only (`parse_shallow_query`, lib.rs:1783), accepts
  only SELECT / set operations (lib.rs:1790-1801), and requires the whole input to be consumed
  (lib.rs:2010-2015):
  ```rust
  // Strip trailing semicolons and make sure we consumed everything by checking for the virtual
  // EOF token
  if sqlparser_result.is_ok() {
      while parser.consume_token(&Token::SemiColon) {}
      parser.expect_token(&Token::EOF)?;
  }
  ```
  So a multi-statement string is a parse failure (fail closed, see 3).
- Size: `readyset-sql-parsing/src/lib.rs` alone is 2,757 lines. The AST crate `readyset-sql`
  and the sqlparser fork are much larger (fork not measured).
- Keeping up with PostgreSQL: by hand (fork of an independent grammar). No generation from
  `gram.y`. The function lists *are* generated from a real server, see 3.

### 2. Session-state safety

Mechanism: **a private upstream connection per client session**, so there is no shared
pool and D3 does not arise. Plus a mirror of the settings that matter to cache lookups.

- VERIFIED IN CODE, `readyset-adapter/src/backend.rs:919-935`, `struct BackendConnectors`:
  `/// Optional connector to the upstream DB. Used for fallback reads and all writes if it exists`
  `upstream: Option<DB>,`. It is filled once per client session by
  `upstream_servers::connect_picked::<DB>(…, username, password, …)` (backend.rs:2230-2240),
  with the *client's* credentials when authentication is required, under the comment "The
  server is picked once, when the session starts."
- Every statement that is not answered from a cache goes to that connection
  (`query_fallback(connectors.upstream.as_mut(), …)`, adhoc.rs, many sites). So `PREPARE`,
  `LISTEN`, temp tables, session locks and SETs live on the client's own server session,
  exactly as without a proxy.
- `reset()` sends `DISCARD ALL` (readyset-psql/src/upstream.rs:556-565). Its comment
  explains why ReadySet itself might sit behind a transaction pooler:
  ```rust
  // `DISCARD ALL` drops session-state on this upstream so that a
  // sticky `SET LOCAL` from a previous client cannot leak into
  // the next fill. Required for the RLS shallow cache to remain
  // correct under transaction-mode pooling (Supavisor, PgBouncer)
  ```
- **Why SETs are still parsed**: a cached answer must equal what the upstream would have
  returned *for this session*. So ReadySet keeps an allow-list of settings whose values it can
  model and refuses the rest. `readyset-adapter/src/backend/set_handler.rs:1-7`:
  "A `SET` the adapter accepts is forwarded upstream so the session there stays
  authoritative … A `SET` it does not support is rejected or ignored per
  `unsupported_set_mode`, and never reaches the upstream at all." Note: in `Proxy` mode the
  statement *does* go upstream. The comment's last clause is true for `Error` mode only, per
  the code below.
- The PostgreSQL allow-list, `readyset-psql/src/query_handler.rs`:
  `ALLOWED_PARAMETERS_ANY_VALUE` (line 71, about 250 names such as autovacuum_*, wal_*,
  checkpoint_*: settings that cannot change a query result), `ALLOWED_PARAMETERS_WITH_VALUE`
  (line 329: `client_encoding` ∈ {utf8,…}, `datestyle` = ISO, `extra_float_digits` ∈ {1,2,3},
  `bytea_output` = hex, `standard_conforming_strings` = on, …), special handling for
  `autocommit`, `timezone` (UTC-equivalent only), `role`, `search_path` (mirrored),
  `CACHE_KEYED_VARIABLES` (line 18: `default_text_search_config`, `intervalstyle`,
  `timezone_abbreviations`, which become part of the shallow cache key), and dotted custom
  GUCs. Everything else is unsupported (query_handler.rs:486-500):
  ```rust
  if let Some(allowed_value) = ALLOWED_PARAMETERS_WITH_VALUE.get(other) {
      behavior.unsupported(!allowed_value.set_value_is_allowed(value))
  } else if other.contains('.') {
      ...
      behavior.unsupported(false)
  } else {
      behavior.unsupported(true)
  }
  ```
- What "unsupported" does: `UnsupportedSetMode` (backend.rs:241-248). The default is `Error`
  (backend.rs:497):
  ```rust
  pub enum UnsupportedSetMode {
      /// Return an error to the client (the default)
      Error,
      /// Proxy all subsequent statements to the upstream
      Proxy,
      /// Allow all unsupported set statements
      Allow,
  }
  ```
  `handle_set` (set_handler.rs:239-277): `Error` returns `ReadySetError::SetDisallowed`;
  `Proxy` sets `state.proxy_state = ProxyState::ProxyAlways`. `ProxyAlways` is sticky
  (routing.rs:73-80: "Unconditionally proxy all statements upstream, and do not leave this
  state when leaving transactions"). This is ReadySet's version of **pinning**: the session
  stops being served from cache, which costs only performance.
- Read-back from the server: after a rollback or the end of a transaction, ReadySet re-reads
  changed settings from upstream instead of guessing
  (`TransactionSettings.reread`, set_handler.rs:104-111; `reread_settings` → 
  `upstream.current_settings(names)`, set_handler.rs:346-362).
- `SELECT set_config(...)` as a *session mutation* is recognised only in one shape: the
  PostgREST batch, and only when RLS is active (`session_mutation::recognize`,
  session_mutation.rs:72-80; used at adhoc.rs:561-575). That path runs it upstream and then
  updates the mirror.

### 3. Read/write and shareability (cacheability)

Positive recognition throughout. A query is answered from a cache only if all of these hold:
1. **It parses as a single SELECT** (shallow) or as a ReadySet `SqlQuery::Select` (deep).
   Parse failure goes upstream. adhoc.rs:583-592 (VERIFIED IN CODE):
   ```rust
   // Parse error, send to fallback
   Err(e) => {
       ...
       let fallback_res =
           Self::query_fallback(connectors.upstream.as_mut(), query, event, None).await;
   ```
   Parse failures inside a transaction also mark "a write may have happened"
   (adhoc.rs:545-554): "conservatively mark on parse failure inside any transaction (some
   writes -- e.g. `SELECT ... FOR UPDATE`, stored-proc `CALL`, CTE-embedded `INSERT` -- never
   reach an `SqlQuery` variant)". That is an explicit fail-closed rule for read-your-writes.
2. **A cache exists for it.** It is created by `CREATE CACHE`, a `/*rs+ CREATE SHALLOW CACHE */`
   hint, or automatically (`--query-caching=async|inrequestpath`). backend.rs:28-41 module
   docs. shallow.rs:57-108 `should_query_shallow`: no `Successful(Shallow)` migration means
   return `None`, so the query is proxied.
3. **Deep cache: the dataflow engine can execute it.** Expressions are lowered by
   `dataflow-expression/src/lower.rs`. Any function not implemented there is refused
   (lower.rs:1068-1069 `_ => { unsupported!("Function {name} does not exist") }`). There is no
   `now`/`random`/`set_config` arm in `from_name_and_args` (grep: no `"now"` in lower.rs).
   Keyword clock functions are refused outright (lower.rs:2025-2036, "No-parentheses functions
   are not supported in dataflow expressions"), and so are UDFs (lower.rs:2037-2042). An
   unsupported query gets `MigrationState::Unsupported` and goes upstream for good (backend.rs:51-54).
   Row locks are refused (`readyset-sql/src/ast/select.rs:771-773`, "Row-level locking (FOR
   UPDATE, FOR SHARE) not supported"). This is "I can prove what it is because I can execute
   it myself".
4. **Shallow cache, automatic creation: a default-deny eligibility pass**,
   `readyset-sql-passes/src/shallow/auto_cache_eligibility/mod.rs` (2,465 lines). Header,
   lines 11-20:
   ```
   //! Calls to user-defined functions are excluded too, since a UDF may have side
   //! effects or be non-deterministic.  For PostgreSQL the pass is default-deny: a
   //! call is eligible only if it is an IMMUTABLE builtin (identified by
   //! `pg_proc.provolatile` and generated into `pg_builtins`), so any session-,
   //! clock-, or side-effect-dependent builtin is treated as non-deterministic
   //! without a hand-curated list.
   ```
   The decision is `function_reason`, mod.rs:902-955:
   ```rust
   // A call qualified by a non-system, non-public schema is user-defined
   if self.is_user_schema_qualified(&func.name) { return (!self.allow_udf).then_some(REASON_UDF); }
   // A name that does not resolve to a plain identifier cannot be matched
   // against the builtin list; treat it as user-defined (fail-safe).
   ...
   Dialect::PostgreSQL => {
       if is_immutable_builtin(name, self.dialect) || is_keyword_function(name)
           || on(PG_ADDITIONAL_ALLOWED) { None }
       else if on(PG_SIDE_EFFECTING_FUNCTIONS) { Some(REASON_SIDE_EFFECT) }
       else if on(PG_SESSION_SPECIFIC_FUNCTIONS) { ... }
       else if on(PG_NONDETERMINISTIC_FUNCTIONS) || is_nondeterministic_special(name) { ... }
       else if is_builtin(name, self.dialect) { Some(REASON_BUILTIN_NOT_ALLOWED) }
       else { (!self.allow_udf).then_some(REASON_UDF) }
   ```
   The comment at mod.rs:926-933 is the design principle in one line: the curated lists "only
   refine the skip reason: a builtin absent from all of them is still denied by the is_builtin
   fallthrough, so a forgotten entry never opens a caching hole."
   - The allow-list is **generated from real servers**. `pg_builtins.rs` header: "Source:
     pg_catalog.pg_proc across PostgreSQL majors 13, 14, 15, 16, 17 (BUILTINS unioned,
     IMMUTABLE_BUILTINS intersected over the majors where each name exists)". The rule is
     "names whose every overload is provolatile='i' in every major". Generator:
     `gen_pg_builtins.py` (`HAVING bool_and(p.provolatile = 'i')`, line 64). Counts at this
     commit: `BUILTINS` 2,743 names, `IMMUTABLE_BUILTINS` 1,966 names.
   - Curated lists, which only refine the reason: `PG_NONDETERMINISTIC_FUNCTIONS` (mod.rs:405:
     now, transaction_timestamp, statement_timestamp, clock_timestamp, timeofday, random,
     random_normal, gen_random_uuid), `PG_SIDE_EFFECTING_FUNCTIONS` (mod.rs:426: nextval,
     setval, pg_sleep*, set_config, pg_reload_conf, …, pg_notify, pg_advisory_lock, …,
     lo_*, pg_read_file, setseed), `PG_SESSION_SPECIFIC_FUNCTIONS` (mod.rs:386:
     current_user, …, current_setting), bare identifiers `CURRENT_DATE/TIME/TIMESTAMP`,
     `LOCALTIME[STAMP]` (mod.rs:491).
   - A small, explicit escape: `PG_ADDITIONAL_ALLOWED` (mod.rs:466: `inet_server_addr`,
     `inet_server_port`, `length`, `row`): non-IMMUTABLE functions judged safe by hand.
   - Non-function guards: data-modifying CTE bodies are always refused (mod.rs:743-756,
     "caching one would re-run the write on every hit. Unconditional: no opt-in flag relaxes
     it"); system schemas and any `pg_`-prefixed name (mod.rs:62-70); `@` variables; unseeded
     `TABLESAMPLE`.
   - Operator escapes: flags `allow_nondeterministic`, `allow_udf`, `allow_all_functions`,
     `allow_system_schema`, `allow_session_specific` (all off by default, mod.rs:516-522),
     and a runtime function allow-list (`ALTER READYSET ... SHALLOW CACHE ALLOWED FUNCTION`).
   - Explicit hints bypass the pass (mod.rs:22-23, "explicit `/*rs+ CREATE SHALLOW CACHE */`
     hints are user-initiated and bypass it"). That is the client-hint model.
   - Before creating any shallow cache the adapter asks the upstream to *prepare* the query
     (`connectors.upstream_supports(shallow)`, shallow.rs:245; for Postgres `can_prepare`
     runs `self.client.prepare(query)`, upstream.rs:596-600). So the real server's parser
     must also accept it as one statement. Shallow `validate_support` refuses `FOR
     UPDATE/SHARE` and `SELECT … INTO` (`readyset-sql-passes/src/shallow/rewrites.rs:88-97`).
5. **Not inside a transaction that has written**, per the cache's `TrxCachePolicy`
   (routing.rs:477, `cache_skip_reason`). Inside an explicit transaction the default
   proxy state forwards everything (`ProxyState::InTransaction`, routing.rs:62-65).

Cost of a miss: a query wrongly judged ineligible costs performance (it is proxied upstream).
A query wrongly judged eligible returns cached rows: stale within the TTL (shallow), or
missing a side effect. Session state never leaks, because no connection is shared. ReadySet
fails **closed**: parse error, unsupported function, unknown function or unsupported SET all
go upstream (or, for SET in default mode, give an error).

Holes I can see by reading (PLAUSIBLE, not run):
- The eligibility pass inspects function calls only. `SELECT 'now'::timestamptz` has no
  function call. I found no rule for it in mod.rs (grep for `'now'` finds only function names),
  so in in-request auto mode it would be cached for the TTL. This is mini-gateway's
  round-1 hole (`'now'::timestamp`), and the default-deny design does not cover it, because
  the volatility lives in the type's input function, not in a function name.
- A view over a volatile function is a plain relation to the pass, so it is eligible. The pass
  has no catalog lookup on relations.
- An unqualified user function that shadows a builtin name (`public.lower` with a
  `search_path` placing `public` before `pg_catalog`) is matched by bare name
  (mod.rs:919-935), so it is treated as the builtin.
For the shallow cache these produce staleness bounded by the TTL, which the product accepts
anyway. The deep cache is not affected, because it executes expressions itself.

### 4. Documented limitations (their words, in code)

- set_handler.rs:1-7 (quoted above): unsupported SETs are rejected or the session is proxied.
- shallow eligibility module header, mod.rs:3-9: "every previously-unseen SELECT becomes a
  candidate for a new shallow cache. That is too aggressive: client/driver bootstrap traffic
  (`information_schema` introspection, `@@version_comment`, `SELECT NOW()`, etc.) is
  structurally a SELECT but should never be cached."
- `PG_NONDETERMINISTIC_FUNCTIONS` and `PG_SIDE_EFFECTING_FUNCTIONS` say "Not exhaustive; a
  missing entry only yields the generic reason, never a caching hole" (mod.rs:424-425). The
  project designed the lists so that incompleteness is harmless.
- Readyset docs site: not read (the sparse checkout has no `docs/`). COULD NOT FIND a doc page
  in this run.

### 5. Language, license, maturity, weight

Rust; Business Source License 1.1 (`LICENSE`), not open source. Repo created 2022-05-24.
Release `stable-261124` on 2026-11-24, last commit the morning of 2026-11-25: very active.
Reuse from Go: none practical. The parser is a Rust crate (sqlparser-rs fork) and the
eligibility logic walks its AST. What *can* be reused is the **idea and the generated
data**: `gen_pg_builtins.py` is a ~200-line script that produces an IMMUTABLE-name list from
`pg_proc` over several majors. A Go proxy could run the same query and embed the list, or run
it against the actual upstream at startup.

### Probe table

| Probe | ReadySet (VERIFIED IN CODE unless marked) |
| --- | --- |
| P1 `SET search_path = x` | Parsed. `search_path` is supported (query_handler.rs `"search_path" =>` arm), forwarded to the client's own upstream connection, and mirrored for cache name resolution (set_handler.rs `PendingSetState::apply`). No cross-client effect is possible. |
| P2 `SELECT set_config('search_path','h',false)` | Not cacheable: shallow pass gives `set_config` as side-effecting (mod.rs:426 list; also not IMMUTABLE). Deep lowering has no `set_config` function (falls to `unsupported!`), so it is proxied upstream on the client's own connection. The mirror is updated only for the PostgREST RLS shape (session_mutation.rs). Otherwise I found no code that updates the `search_path` mirror after a `set_config`. A later cached lookup could then resolve names with the stale path. PLAUSIBLE, not run. |
| P3 `SELECT f()` | `f` is not a builtin, so `REASON_UDF` (shallow); deep: "User-defined function call" `unsupported!` (lower.rs:2037-2042). Proxied upstream. The SET or temp table stays in the client's own session (harmless to others). Same mirror caveat as P2. |
| P4 lexical tricks | Text is tokenised by sqlparser-rs (PostgreSQL dialect). Whether its tokenizer matches PostgreSQL on `E''` string continuation across a newline: COULD NOT VERIFY (fork not checked out). Consequence of a mismatch: if parsing fails, the query is proxied (safe). If it succeeds with a different meaning, a cached answer could be wrong. No session-state risk either way. The upstream `prepare` check (3.4) runs only at shallow-cache creation. |
| P5 PREPARE / LISTEN / CREATE TEMP / pg_advisory_lock / DECLARE … WITH HOLD | All proxied to the client's own upstream connection (non-SELECT, adhoc.rs:752-760, `query_adhoc_non_select`). `SELECT pg_advisory_lock(1)` is side-effecting for shallow and unsupported for deep, so proxied. State stays private. `DEALLOCATE` is handled in the adapter (adhoc.rs:751). |
| P6 `SELECT 1; SET search_path = x` | Shallow parse fails (`expect_token(EOF)`, lib.rs:2014). Deep parse fails too (same inner function, assumed). Parse error, so it goes upstream via the "Parse error, send to fallback" path (adhoc.rs:583). The SET runs in the client's own session. The mirror is not updated (same caveat as P2; PLAUSIBLE). |
| P7 `nextval('s')` | Shallow: side-effecting, not cached. Deep: no such function, proxied. |
| P7 CTE `DELETE … RETURNING` | Shallow: `REASON_WRITE_IN_CTE`, unconditional (mod.rs:743-756). In a transaction a parse failure marks a write. Proxied. |
| P7 `SELECT … FOR UPDATE` | Refused by both caches (select.rs:771-773, shallow rewrites.rs:90-91). Proxied. |
| P7 `SELECT now()` | Shallow: non-deterministic (opt-in `allow_nondeterministic` or allow-list entry `now` makes it eligible). Deep: no `now` in lowering, so unsupported. Proxied. |
| P7 `SELECT * FROM v` (view over volatile) | Shallow auto mode: **eligible** (no catalog check on relations). Cached for the TTL. Deep: views are expanded from ReadySet's replicated schema, and an unsupported function inside makes the query unsupported (not traced in code). |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | ReadySet's parser intercepts `EXPLAIN` for its own extensions (lib.rs:1834 `parse_explain`). Whether it falls back to the upstream statement: not traced. In any case it is not a SELECT, so it is never cached. It runs upstream or errors. |

---

## Short treatments

### awslabs/pgbouncer-rr-patch (WEB READS via `gh api`, commit 56d51ab, 2025-09-03, Apache-2.0)

A PgBouncer patch that calls a user Python function per query, used to route or rewrite.
routing_rules.py (VERIFIED IN CODE): a table of `usernameRegex` / `queryRegex` → pool key,
"RETURNS FIRST MATCH FOUND". The example is `'queryRegex' : '.*tablea.*'`. README:
"Multi-statement transactions will work correctly only if statements are not rerouted by the
routing_rules function to a different server pool mid-transaction. Set `pool_mode =
transaction`". "Any query that does not match either rule results in None being returned, and
the server connection remains unchanged". So it fails open, the text reading is plain
substring/regex, and session state is PgBouncer's (no reading). Its value here: a clear
example of a *routing* decision taken by regex that its authors leave entirely to the user's
risk. Not a model for D3–D5.

### Prisma Accelerate (DOCS ONLY)

prisma.io/docs/accelerate/reference/api-reference: "you can use the `cacheStrategy` parameter
for model queries and use the `ttl` and `swr` parameters"; "The `cacheStrategy` parameter is
not supported on any write operations, such as `create()`."; `cacheStatus` `none` means "no
cache strategy was specified and the database query was executed by the request". So
cacheability is declared by the client per query, with no SQL reading at all. This is
mini-gateway's `hinted` mode as a shipped commercial design. Retirement on 2026-12-01 is
stated on the caching page.

### Not examined

Heimdall Data, Neon's proxy, Google AlloyDB managed pooling, Azure built-in PgBouncer:
not examined in this run (time budget). No claims are made about them.

---

## Claims of completeness

- **RDS Proxy** does not claim completeness. It documents a hole and a fallback: functions and
  procedures are not seen ("RDS Proxy doesn't detect any session state changes resulting from
  such calls"), and uncertainty pins ("The same rule applies if RDS Proxy can't be certain that
  multiplexing is practical"). The list is presented as the conditions under which it pins,
  not as a complete list of session-changing statements. The page does not say that.
- **Hyperdrive** states a precise rule ("Only functions designated as `IMMUTABLE` by
  PostgreSQL … are compatible with Hyperdrive caching") as if it were applied completely. The
  example list is titled "Common … include", and nothing explains how a view, a cast of
  `'now'`, or a user function is classified. Accepting staleness up to `max_age` and having no
  invalidation also make any misclassification cheaper. I can't test the claim against code
  (closed). For session state it explicitly does not claim completeness ("Any modification to
  per-session state not explicitly documented as supported" is unsupported).
- **ReadySet** makes no completeness claim about text. Its code comments say the opposite:
  the lists are "Not exhaustive", and safety comes from default-deny plus a private upstream
  connection. The claim the code does make ("a forgotten entry never opens a caching hole")
  holds for *function names*. It does not hold for volatility that arrives without a function
  call (`'now'::timestamptz`, views), as shown above.

None of the three decides session-state safety from SQL text with a promise of completeness.
RDS Proxy comes closest to reading text for D3, and it pairs the reading with a safe fallback
plus a documented blind spot.

## Mapping to options (a) (b) (c1) (c2)

| Project | Closest option | Why |
| --- | --- | --- |
| RDS Proxy | **Neither (a) nor (b): "pin"**, which the steering answer ("rejected, not pinned") ruled out | Deny-list over text like (a), but a miss is meant to cost efficiency (pin), not isolation. Its own blind spot (functions) is the same as mini-gateway's §9.4. |
| Hyperdrive | **(b)** for session state (reset on release, document the rest). An allow-list rule (IMMUTABLE only) for D5. | Transaction pooling plus reset, with the same "unsupported, not rejected" stance as PgBouncer. The cache rule is the allow-list form of D5, plus an application-level opt-out (separate binding) like `hinted`. |
| ReadySet | **None of them for D3.** No pool is shared (one upstream connection per client), so the problem does not arise. **(c1)-like for D5**: a real parser (not PostgreSQL's) plus positive recognition, refined by **catalog data** (provolatile), which (c1) alone would not give. Also a partial **(c2)** flavour: the upstream must `prepare` a query before a shallow cache is made. | Its safety rests on "only what I can execute or prove immutable is shared" and on "everything else goes to your own connection". mini-gateway cannot copy the second half, because it shares connections across tenants. |
| Prisma Accelerate | **(d2)/hinted** | Client declares cacheability per query. No SQL reading. |
| pgbouncer-rr | none | Regex routing with no safety promise. |

## Mechanisms not in the assessment

1. **Generate the "safe function" allow-list from `pg_proc.provolatile`, and fail closed on
   every name not on it** (ReadySet, VERIFIED IN CODE). The assessment's (b)2 recogniser has
   "every called name on the short allow-list", hand-written. ReadySet's version makes the
   list *data from the server*: `SELECT proname FROM pg_proc GROUP BY proname HAVING
   bool_and(provolatile='i')` over each supported major, intersected. In a Go proxy, either
   embed the generated list (as ReadySet does) or run the query against the actual upstream at
   startup. The second also catches user functions marked IMMUTABLE, and closes the
   "unqualified name shadows a builtin" gap by querying with the pool's real `search_path`. It
   does *not* fix volatility without a function call (`'now'::timestamptz`) or views. Those
   still need a type rule (refuse any cast of a string literal to a date/time or range type) or
   the next mechanism.
2. **Ask the server's parser before trusting your own** (ReadySet: `can_prepare` sends a
   protocol `Parse` to the upstream before creating a shallow cache). For mini-gateway: when
   a statement first qualifies as shareable, send it upstream once as an extended-protocol
   `Parse` (unnamed statement) + `Describe` + `Sync`, on a pool connection. PostgreSQL
   refuses multiple statements in a `Parse` ("cannot insert multiple commands into a prepared
   statement"), so a successful `Parse` proves the text is **exactly one statement as
   PostgreSQL lexes it**. That closes the whole lexical-layer class (P4, P6) without a lexer.
   `Describe` returning a `RowDescription` proves it returns rows. The verdict can be cached
   per statement text. Cost: one round trip per distinct statement text, plus the proxy
   speaking extended protocol *to the server*, even though it refuses it from clients.
   Combined with (c2) (`BEGIN READ ONLY`) it gives "one statement, and the server refused any
   write". Function volatility is still left to mechanism 1.
3. **Read session state back from the server instead of inferring it from text** (ReadySet
   `reread_settings`, set_handler.rs:346-362, uses `current_settings` after rollback/commit).
   A stronger form for mini-gateway, my proposal and not found in any of these projects:
   at lease end, run one *check* query instead of an unconditional `DISCARD ALL`, for example
   `SELECT (SELECT count(*) FROM pg_prepared_statements) + (SELECT count(*) FROM pg_cursors)
   + (SELECT count(*) FROM pg_listening_channels()) + (SELECT count(*) FROM pg_locks WHERE
   pid = pg_backend_pid() AND locktype = 'advisory') + (SELECT count(*) FROM pg_settings WHERE
   source = 'session') + (pg_my_temp_schema() <> 0)::int`. Zero means the connection is clean
   and goes back as is. Otherwise `DISCARD ALL` or close. This detects state created inside
   functions (P3) without reading any SQL, at the cost of one round trip. That is the same
   cost as the reset, but it keeps plan caches warm on clean connections. Caveats: a temp
   schema persists after its first use even when empty, so that term over-reports and causes
   extra resets, which is safe. Unverified against a server in this run.
4. **Use the server's ParameterStatus messages as a free change signal for some settings.**
   PostgreSQL sends `ParameterStatus` when any `GUC_REPORT` setting changes, however it was
   changed (SET, `set_config`, inside a function). In PostgreSQL master at `4483697`,
   `search_path` carries `GUC_REPORT` (`src/backend/utils/misc/guc_parameters.dat:2620-2622`,
   `flags => 'GUC_LIST_INPUT | GUC_LIST_QUOTE | GUC_EXPLAIN | GUC_REPORT'`), alongside
   `application_name, client_encoding, DateStyle, default_transaction_read_only,
   IntervalStyle, is_superuser, session_authorization, standard_conforming_strings,
   TimeZone` and a few read-only ones (VERIFIED IN CODE, grep of that file). A proxy that sees
   a `ParameterStatus` mid-lease knows the session is dirty, with no parsing. Which PostgreSQL
   release first reports `search_path`: COULD NOT VERIFY in this run (shallow clone). Against
   older servers this signal covers only the older list. It covers no other kind of state
   (temp tables, locks).
5. **Key the shared result on the session values that change it** (ReadySet shallow key,
   `readyset-adapter/src/shallow_key.rs:1-10`: "The key pairs the bound query parameters with
   the session-derived values the cached result depends on … Entries are additionally
   partitioned by session variables that affect the result"). For mini-gateway's coalescer,
   the share key could include the tenant's effective `search_path`, `TimeZone`, `DateStyle`
   and role, instead of requiring them equal by construction. That only matters if session
   SETs are ever allowed through. Under option (b) they are reset, so this is low value.
(Pinning, which RDS Proxy and ReadySet's `ProxyAlways` use as the fallback, and TTL
staleness by contract are left out: the explainer already covers the first, and the second
does not apply to coalescing in-flight reads.)

## Could not verify

- RDS Proxy: how the PostgreSQL detector reads text (parser, lexer, comments, dollar quotes);
  whether multi-statement queries pin on PostgreSQL (no such item on the current page; an
  older version of the page may have had one, but I can't confirm that); whether it resets a
  connection after an unpinned transaction; whether pools are per database user. All COULD
  NOT FIND.
- Hyperdrive: what parser it uses; what "uses database protocols" means; whether the
  IMMUTABLE rule is applied from a complete list, a deny-list, or the origin's catalog; whether
  "`RESET`" is `RESET ALL` or `DISCARD ALL`; whether the cache key includes session settings;
  behaviour on unparsable text; the brief's "text-match on function names causing false
  positives" limit (not found in the current docs or the three blog posts read).
- ReadySet: whether the sqlparser-rs fork's tokenizer matches PostgreSQL on `E''` string
  continuation and other scanner rules (fork not checked out); whether the deep parse path also
  rejects trailing statements (assumed, same inner helper); the `search_path` mirror after
  `set_config` or a parse-failed batch (PLAUSIBLE gap, not run); how views are handled for the
  deep cache; ReadySet's public docs pages.
- Prisma Accelerate: the per-query opt-in is DOCS ONLY. The pooling mode was not read.
- Heimdall Data, Neon proxy, AlloyDB managed pooling, Azure built-in PgBouncer: not examined.
- PostgreSQL: which release first marked `search_path` as `GUC_REPORT`.
