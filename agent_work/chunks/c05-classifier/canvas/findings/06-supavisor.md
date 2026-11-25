# Supavisor (Supabase, Elixir + Rust NIF)

Research only, by reading source. Nothing was built or run. Line numbers are in the clone
`/root/scratch/c05-canvas/repos/supavisor` at the commit below unless a tag is named.
Older-tag files were fetched with `gh api .../contents/<path>?ref=<tag>` into
`/root/scratch/c05-canvas/work/06-supavisor/`.

## Sources

| Project | URL | Commit SHA | Commit date | Local / web |
| --- | --- | --- | --- | --- |
| Supavisor (HEAD, `VERSION` = 2.11.0-dev) | https://github.com/supabase/supavisor | `22efe25b18245e0b857d4b524ee5d9a9971643bc` | 2026-11-25 | LOCAL SOURCE |
| Supavisor at tag `v2.0.0` (historical read/write routing) | same | `43aeb60d8fc05d38d1e0022c9b5fcf18341d1664` | 2024-08-30 | WEB READS (`gh api`), saved as `work/06-supavisor/{ch,dh,srv}-v2.0.0.ex` |
| Supabase docs, "Connecting to Postgres" | https://supabase.com/docs/guides/database/connecting-to-postgres | — | fetched 2026-11-25 | WEB READ (saved as `work/06-supavisor/sb-connecting.md`) |

Newest release: `v2.10.1`, published 2026-11-14 (`gh release list`). License Apache-2.0
(`LICENSE`). Rust NIF dependency: `pg_query = "6.2"` (`native/pgparser/Cargo.toml`),
locked at 6.2.1 (`native/Cargo.lock`).

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| Supavisor HEAD | Real PostgreSQL parser (libpg_query via the Rust `pg_query` crate, as an Erlang NIF) — but only when a per-tenant check is switched on | Transaction mode: **no reset on release**. Optional per-tenant deny-list detector `txn_mode_leak_action` = `ignore` (default) / `log` / `error`. Session mode: `server_reset_query` (`DISCARD ALL`) on client disconnect. Named extended-protocol prepared statements: track-and-replay with content-hashed server names (feature flag, default off). Pools keyed per tenant+user+mode+db+search_path, so tenants never share a backend. | None at HEAD (no read routing, no result cache found) | Deny-list (AST node types + 7 function names) | Session state of one client reaches the next client of the same backend — same tenant and user only, because pools are not shared across tenants | **Open**: unparseable, >1 KB, non-UTF-8 or operator-heavy queries are forwarded unchecked; non-literal `is_local` is let through; functions are not looked into |
| Supavisor `v2.0.0` (historical) | Same parser, every query on a cluster pool | (as above, earlier form) plus broadcast of simple-protocol `PREPARE`/`DEALLOCATE` to every backend in the pool | Route to a read replica iff the whole query parses to exactly `["SelectStmt"]`; else primary. If the replica answers SQLSTATE `25006`, rerun the query on the primary | Allow-list of one (`SelectStmt`), with a server-error fallback | A write-ish `SELECT` errors on the replica and is retried on the primary (only performance); stale reads from replica lag | Closed: parse error → primary |

## Supavisor

### 1. Does it read SQL?

VERIFIED IN CODE. Yes, with the real PostgreSQL parser. `Supavisor.PgParser`
(`lib/supavisor/pg_parser.ex`) is a Rustler NIF over `native/pgparser/src/lib.rs`
(340 lines), which calls `pg_query::parse` (libpg_query: PostgreSQL's own scanner and
grammar extracted into a C library). It is kept in step with PostgreSQL by bumping the
`pg_query` crate version; pg_query 6.x bundles libpg_query for PostgreSQL 17 — that
mapping is from memory, not checked in this run.

Before parsing, three cheap guards (`lib.rs:64-72`, `message_handler_helpers.ex:14-38`):

```rust
fn parse_query(query: Binary) -> Result<pg_query::ParseResult, Atom> {
    let query = std::str::from_utf8(query.as_slice()).map_err(|_| atoms::invalid_utf8())?;
    if operator_rescan_cost(query.as_bytes()) > MAX_OPERATOR_RESCAN_COST {
        return Err(atoms::too_complex());
    }
    pg_query::parse(query).map_err(|_| atoms::syntax_error())
}
```
```elixir
  @max_normal_parse_size 512
  @max_parsed_query_size 1024
  ...
  defp parse(query) when byte_size(query) > @max_parsed_query_size, do: {:error, :too_large}
```

Every error (`:syntax_error`, `:too_large`, `:too_complex`, `:invalid_utf8`) becomes
`:skip` (`message_handler_helpers.ex:18-31`), and the callers treat `:skip` as "pass the
message through" (`simple_query_handler.ex:53-55`: `_ -> :ok`;
`parse_message_handler.ex:23-24`). The module docs say so: "An unparseable query is passed
through for the backend to reject" (`simple_query_handler.ex:14-15`).

When does it parse? Only in transaction mode
(`client_handler/protocol_helpers.ex:127-153`: `process_client_packets(bin, :transaction, …)`;
other modes return the bytes unchanged), and only if a check is enabled:
`leak_action != :ignore` or the feature flag `check_simple_query_prepare`
(`simple_query_handler.ex:43-47`). Both default to off: `txn_mode_leak_action` defaults to
`:ignore` (`lib/supavisor/tenants/tenant.ex:39-42`), `CHECK_SIMPLE_QUERY_PREPARE_ENABLED`
defaults to false (`config/runtime.exs:293-294`). **So by default Supavisor HEAD reads no
SQL at all.** Parse messages (`P`) are also checked when the leak action is on
(`parse_message_handler.ex:31-41`).

What the parse is used for at HEAD: exactly two decisions — `session_leak/1` (D3) and
"is this `PREPARE`/`EXECUTE`/`DEALLOCATE`" (`simple_query_handler.ex:22-26, 58-66`).
No read/write or cache decision consults it (see 3).

Historical use (tag `v2.0.0`, WEB READ): `ClientHandler` parsed every query on a
read/write cluster pool to choose a pool (`ch-v2.0.0.ex:539-557`), and parsed every
simple query in transaction mode to find `PREPARE`/`DEALLOCATE` (`ch-v2.0.0.ex:1029-1033`).
Both are gone at HEAD (`grep PgParser lib` finds only the two HEAD call sites above).

### 2. Session-state safety

**Return point: protocol signal, not text.** VERIFIED IN CODE. `BackendConnection` is a
pure model of the backend that follows every tracked message and its replies
(`lib/supavisor/protocol/backend_connection.ex`, moduledoc lines 1-67). The state after a
`ReadyForQuery` comes from its status byte and the queue of unanswered requests
(`backend_connection.ex:553-561`):

```elixir
  defp ready_for_query(backend(requests: requests) = backend, status) do
    state =
      cond do
        not :queue.is_empty(requests) -> :busy
        status == ?I -> :idle
        true -> :in_transaction
      end
```

and `synced?` is `state == :idle and parked_write == nil and not client_in_copy`
(`:285-287`). Only when the backend becomes synced does the DbHandler tell the client
handler (`db_handler.ex:517-524`), and the ClientHandler then releases and checks the
backend in — but only if the write sequence number matches, i.e. nothing else was forwarded
in between (`client_handler.ex:651-686`, `db_handler.ex:491-510`; a mismatch closes the
server connection). So pipelined requests keep the lease. Same design as mini-gateway's D4.

**No reset on release in transaction mode.** VERIFIED IN CODE. The release path
(`client_handler.ex:678-682`, `db_handler.ex:493-499`) sends nothing to the server. The
only reset is `server_reset_query` (tenant field, default `"DISCARD ALL"`,
`tenant.ex:33`), sent by `DbHandler` `:cleanup` — which refuses in transaction mode
(`db_handler.ex:683-708`):

```elixir
      data.mode == :transaction ->
        Logger.error(
          "DbHandler: Cleanup called on transaction mode - only supported in session mode"
        )
        {:keep_state_and_data,
         {:reply, from, {:error, :cleanup_not_supported_in_transaction_mode}}}
```

It is called only from `maybe_cleanup_db_handler` when a **session-mode** client
disconnects while idle (`client_handler.ex:923-941`). The doc agrees:
"`server_reset_query` - the query run on a session mode connection before it is returned to
the pool" (`docs/configuration/tenants.md:47`).

**Detection (opt-in deny-list over the parse tree).** VERIFIED IN CODE.
`Supavisor.Protocol.SessionLeaks.check/2` (`session_leaks.ex:35-50`) calls the NIF
`session_leak`, which walks every top-level statement and returns the first match
(`lib.rs:176-249`). The list:

| AST node / function | Flagged as | Exception (not flagged) |
| --- | --- | --- |
| `VariableSetStmt` (SET, RESET, SET ROLE, …) | `:session_set` | `is_local`, or name `TRANSACTION` / `TRANSACTION SNAPSHOT` |
| `DiscardStmt` | `:discard` | — |
| `ListenStmt`, `UnlistenStmt` | `:listen` | — |
| `LoadStmt` | `:load` | — |
| `DeclareCursorStmt` with `CURSOR_OPT_HOLD` | `:hold_cursor` | without HOLD |
| `CreateStmt` with relpersistence `t` | `:temp_table` | `ON COMMIT DROP` |
| `CreateTableAsStmt` / `SelectStmt` with temp `INTO` | `:temp_table` | `ON COMMIT DROP` |
| any `FuncCall` anywhere in the tree named `set_config` (bare or `pg_catalog.`) whose 3rd arg is a **literal** false | `:set_config` | literal true, or a non-literal 3rd arg |
| any `FuncCall` named `pg_advisory_lock`, `pg_advisory_lock_shared`, `pg_try_advisory_lock`, `pg_try_advisory_lock_shared`, `pg_advisory_unlock`, `pg_advisory_unlock_all` | `:advisory_lock` | `_xact_` variants |

The non-literal case is a deliberate fail-open (`lib.rs:283-291`):

```rust
        // set_config(setting, value, is_local); only the session-scoped form
        // poisons the connection. A non-literal is_local (a parameter, say)
        // could be either, and flagging it would reject calls that are in fact
        // transaction-local, so it is let through
```

Not on the list (read from the `match` in `lib.rs:191-249`; nothing else matches):
`PREPARE` (handled by the separate flag, see below), `CREATE TEMP VIEW`, `CREATE TEMP
SEQUENCE`, `CREATE FUNCTION pg_temp.f()`, `UPDATE pg_settings SET …`, user functions that
do any of the above, `dblink_connect`, and schema-qualified calls other than `pg_catalog.`
(`qualified_func_name`, `lib.rs:304-319`, rejects them on purpose so that
`myschema.set_config` is not mistaken for the builtin).

Action on a hit: `:ignore` (default) does not even parse; `:log` logs and forwards;
`:error` returns `SessionLeakError` ("… is not allowed in transaction mode",
`lib/supavisor/errors/session_leak_error.ex:19`) to the client.

**Prepared statements.** VERIFIED IN CODE.
- Extended protocol, named statements (feature flag `named_prepared_statements`, default
  off: `config/runtime.exs:289-290`): **track and replay**. Each client's `Parse` is
  stored per client connection; the client name is replaced by a server-side name
  `"sv_" <> base64(sha256(Parse payload))` (`prepared_statements.ex:88-108, 207-213`).
  Each backend records which server-side names it has (`BackendConnection` field
  `statements`). On `Bind`/`Describe` for a statement the backend lacks, the stored `Parse`
  is sent first and its `ParseComplete` dropped (`backend_connection.ex:322-332`, action
  `:skip`); a `Parse` the backend already has is not sent and its `ParseComplete` is faked
  (`:309-313`, action `:fake`). Backends hold at most 200 such statements and evict
  (`prepared_statements.ex:45`, `@backend_limit 200`). Unnamed statements pass through
  (`prepared_statements.ex:90-92`). Errors `42P05` / `26000` correct the record
  (`backend_connection.ex` moduledoc lines 55-61).
- Simple protocol `PREPARE` / `EXECUTE` / `DEALLOCATE`: rejected with
  `SimpleQueryNotSupportedError` only when the `check_simple_query_prepare` flag is on
  (`simple_query_handler.ex:58-66`); otherwise forwarded and left on the backend.
- Historical (`v2.0.0`, WEB READ): a simple query that parsed to exactly `["PrepareStmt"]`
  or `["DeallocateStmt"]` was **broadcast** to every other DbHandler in the pool, which
  queued it to run on its own backend (`ch-v2.0.0.ex:1029-1052`; `dh-v2.0.0.ex:308-313`,
  `{:handle_ps, payload, bin}` → `anon_buffer`). A "make every connection identical"
  mechanism; removed by HEAD.

**Pool keying.** VERIFIED IN CODE. The pool id is the record
`{type, tenant, user, mode, db, search_path, upstream_tls}` (`lib/supavisor.ex:25-33`), and
each pool is its own poolboy (`tenant_supervisor.ex:22-40`). Backends are never shared
across tenants or users, nor between transaction and session mode. Startup `options` are
filtered to a schema of six names (`startup_options.ex:83-93`: "Options outside the schema
are dropped, since a pooled backend is shared between clients"); a client's `search_path`
startup option becomes part of the pool key, so clients with different startup search paths
get different pools. **This is the key difference from mini-gateway**: Supavisor's worst
case is one client of a tenant seeing another client's state of the *same* tenant and role.

### 3. Read-vs-write and shareability decisions

HEAD: none found. COULD NOT FIND any read/write routing or result cache. Cluster pools
exist (`tenants/cluster_tenants.ex:17`, type `:write | :read`), but the Manager selects the
`:write` cluster tenant (`manager.ex:199-210`) and `get_local_pool/1` returns a map for a
multi-replica cluster (`supavisor.ex:380-396`), which `:poolboy.checkout` in
`client_handler.ex:1185-1186` cannot take. The README lists "Queries can be load balanced
across read-replicas" and "Query caching" under **Future Work** (`README.md:117-124`).

Historical `v2.0.0` (WEB READ, `ch-v2.0.0.ex:539-557`):

```elixir
        case statements do
          # naive check for read only queries
          ["SelectStmt"] -> :read
          _ -> :write
        end
      else
        other ->
          Logger.error("ClientHandler: Receive query error: #{inspect(other)}")
          :write
```

Allow-list of one node type, whole query, fail closed (parse error → primary). The safety
net was the server: the read-replica DbHandler scanned replies for an `ErrorResponse` with
SQLSTATE `25006` (`srv-v2.0.0.ex:458-463`, `%{payload: ["SERROR", "VERROR", "C25006" | _]}`),
answered `:read_sql_error` (`dh-v2.0.0.ex:245-261`), and the ClientHandler re-ran
`data.last_query` on the write pool (`ch-v2.0.0.ex:648-660`). Cost of a miss: a statement
that writes is refused by the hot standby and retried on the primary (latency), plus any
non-transactional side effect run twice; stale reads from replica lag are not addressed.

### 4. Documented limitations (quotes)

- Supavisor docs, `docs/migrating/pgbouncer.md:14-17`: "In transaction mode, any
  session-level `SET` stays on the server connection after the transaction ends, and every
  later client that gets that connection runs under it."
- Same file, `:37-39`: "Set the tenant's `txn_mode_leak_action` to `log` or `error` to find
  clients that send a session-level `SET` in transaction mode. Queries the check skips, such
  as those larger than 1 KB, are not caught."
- `docs/configuration/tenants.md:53-61`: "… This covers session-level `SET`,
  `set_config/3`, `DISCARD`, session-scoped advisory locks, `LISTEN`/`UNLISTEN`, `WITH HOLD`
  cursors, temp tables and `LOAD`. … Queries larger than 1 KB, or with long runs of operator
  characters such as `+++…`, are not checked; a warning is logged for each one instead."
- `docs/faq.md:15-19`: "It supports prepared statements in session mode. In transaction
  mode, named prepared statements are supported when the `named_prepared_statements`
  feature flag is enabled …"
- Supabase docs (https://supabase.com/docs/guides/database/connecting-to-postgres, DOCS
  ONLY): "The shared pooler doesn't support prepared statements in transaction mode."
  "**Session-level state** is lost between transactions. This covers `set` and `reset`,
  session-level advisory locks, `listen` and `notify`, and temporary tables."
  "**Query pipelining** isn't supported: transaction mode returns a connection to the pool
  as soon as it sees one reply finish, even if the client already queued more queries on it."
- Ports (same page): "Port `5432` reaches Postgres for a direct connection and the shared
  pooler for session mode. Port `6543` reaches the dedicated pooler or the shared pooler in
  transaction mode, depending on the host." The dedicated pooler on `db.<ref>:6543` is
  PgBouncer, not Supavisor.

Two of the Supabase sentences disagree with the code at HEAD. "Session-level state is lost
between transactions" — the code does not reset anything in transaction mode, so the state
is not lost, it stays on the backend for the next client (as Supavisor's own doc says). And
the pipelining sentence describes older behaviour: HEAD keeps the lease while any request is
unanswered (`backend_connection.ex:553-561`) and checks `write_seq` before releasing. The
Supabase page may describe the production deployment, not this commit; I could not tell which
version production runs.

### 5. Language, license, maturity, dependency weight

Elixir/OTP application (Apache-2.0) with one Rust NIF crate (`rustler 0.36.1`, `pg_query
6.2`). Active: HEAD commit dated 2026-11-25, release `v2.10.1` on 2026-11-14, tags since
v0.x/v1.x. For a Go proxy the reusable part is tiny: the 340-line `lib.rs` is a tree walk
over libpg_query's protobuf AST; the Go equivalent is `pg_query_go` (cgo, libpg_query
vendored) plus about 150 lines of the same `match`. Nothing in the Elixir runtime is
reusable. The operator-run guard (`lib.rs:74-126`) is a portable idea: it bounds the one
quadratic path in PostgreSQL's scanner before calling it.

### Probe table (HEAD, transaction mode, `txn_mode_leak_action = :error`; with the default `:ignore` every probe is forwarded unchecked)

| Probe | What Supavisor does | Code path |
| --- | --- | --- |
| P1 `SET search_path = x` | `VariableSetStmt`, not local → `:session_set` → rejected. (Default `:ignore`: forwarded, leaks to the next client of that backend.) | `lib.rs:195-200` |
| P2 `SELECT set_config('search_path','h',false)` | `FuncCall` `set_config`, literal false → `:set_config` → rejected. With `$1` or `(1=0)` as 3rd arg → let through. | `lib.rs:270-293, 323-338` |
| P3 `SELECT f()` (f runs SET / creates temp table) | Not detected; forwarded; state leaks. | `lib.rs:247` (`func_call_leak` matches names only) |
| P4 lexical tricks | Comments, dollar quotes, `E''`, continuation are handled exactly as PostgreSQL's scanner would, **if** the server has `standard_conforming_strings = on` (libpg_query's default, `repos/libpg_query/src/pg_query_parse.c:53-59`; Supavisor passes no options). Bypass by size: pad any leaking statement past 1024 bytes (e.g. a trailing comment) → `:too_large` → `:skip` → forwarded unchecked; also any `+++…` run over the cost bound, any non-UTF-8 byte, or a syntax error elsewhere in the same Query. | `message_handler_helpers.ex:14-38`, `simple_query_handler.ex:53-55` |
| P5 `PREPARE p AS …` | Not a leak; rejected only if `check_simple_query_prepare` flag is on, else forwarded and left on the backend | `simple_query_handler.ex:22-26, 58-66` |
| P5 `LISTEN c` | `:listen` → rejected | `lib.rs:208-209` |
| P5 `CREATE TEMP TABLE t(…)` | `:temp_table` → rejected (allowed with `ON COMMIT DROP`) | `lib.rs:221-229` |
| P5 `SELECT pg_advisory_lock(1)` | `:advisory_lock` → rejected | `lib.rs:165-172, 296-298` |
| P5 `DECLARE c CURSOR WITH HOLD …` | `:hold_cursor` → rejected | `lib.rs:215-217` |
| P6 `SELECT 1; SET search_path = x` | All top-level statements are walked; second one flagged → whole Query rejected | `lib.rs:177-186` |
| P7 (all) | n/a at HEAD (no read/write or cache decision). At `v2.0.0`: `nextval`, writable CTE, `FOR UPDATE`, `now()`, view → all top-level `SelectStmt` → replica; those that write get `25006` from the standby and are rerun on the primary; `EXPLAIN ANALYZE DELETE` is `ExplainStmt` → primary. | `ch-v2.0.0.ex:548-551`, `dh-v2.0.0.ex:245-261` |

## Claims of completeness

No. Supavisor's docs explicitly disclaim completeness: the check is opt-in, off by default,
and "Queries the check skips, such as those larger than 1 KB, are not caught"
(`docs/migrating/pgbouncer.md:37-39`). The code comments make the same trade openly
(non-literal `is_local` "is let through", `lib.rs:283-291`; unparseable → "passed through for
the backend to reject"). Its stated purpose is to **find** misbehaving clients (`:log`),
not to guarantee isolation. Isolation between tenants comes from pool keying, not from
reading SQL. The README's "load balancing" and "query caching" remain Future Work. The
Supabase page's "session-level state is lost between transactions" is a user-facing
simplification the code does not support (see section 4).

## Mapping to options (a) (b) (c1) (c2)

- Session-state detector: **(c1)** — real parser via a native binding (Rust NIF, the
  analogue of cgo) — used as a **deny-list**, i.e. (c1) applied to option (a)'s job. Even
  with the real parser it fails open on size, encoding, functions and non-literals, which is
  the assessment's point that the grammar layer, not the lexer, is the open-ended part.
- Session safety overall: **none of them**. It neither resets on release nor rejects by
  default; it relies on per-tenant/per-user pool keying, so leaks stay inside one tenant
  and role, and documents the rest.
- Historical read routing (`v2.0.0`): a tiny **(c1)** allow-list (exactly one
  `SelectStmt`) backed by **(c2)** — the standby enforces read-only and the proxy reacts to
  SQLSTATE `25006`.

## Mechanisms not in the assessment

1. **Isolation by pool key instead of by reading.** Supavisor keys pools on tenant, user,
   db, mode and startup `search_path` (`supavisor.ex:25-33`). Session state can leak only
   between clients that already share an identity. For mini-gateway this would mean one
   pool (or one partition of the pool) per tenant: then (D3) mistakes cost intra-tenant
   surprise, not cross-tenant leakage. The cost is connection count (one idle floor per
   tenant). A middle form: keep the shared pool, but tag each backend with the tenant that
   last used it and run `DISCARD ALL` / reset only when the next lease is for a different
   tenant — reset cost paid only on tenant switches.
2. **React to the server's error instead of predicting it** (`v2.0.0`): send to a place
   where the server enforces the rule (a hot standby, or for mini-gateway a transaction
   opened `BEGIN READ ONLY`), and on SQLSTATE `25006` re-run on the writable path. For
   mini-gateway this is option (c2) with a twist: the retry turns a classifier miss into
   latency. Only safe when nothing reached the client yet and the statement had no
   side effect outside the transaction.
3. **Content-addressed server-side prepared statements with lazy re-prepare.** Server name
   = `sv_` + sha256 of the Parse payload; each backend's set of names is tracked; a Bind for
   a missing name is preceded by the stored Parse and the extra `ParseComplete` is
   swallowed; a duplicate Parse is not sent and its reply is faked; LRU at 200 per backend;
   self-correction on `42P05`/`26000` (`backend_connection.ex`, `prepared_statements.ex`).
   This makes named prepared statements safe without reset because the name encodes the
   text: a backend that has `sv_X` has exactly that statement, whoever prepared it. In Go:
   a per-backend `map[string]struct{}` and a per-client `map[name]parseBytes`, plus reply
   accounting — only relevant if mini-gateway ever accepts the extended protocol.
4. **Broadcast a session change to every backend** (`v2.0.0`, simple `PREPARE`): make all
   connections identical rather than reset them. Removed at HEAD; it does not scale to `SET`
   and races with in-flight leases.
5. **Bound the parser's input before parsing**: size limit plus an operator-run cost
   estimate (`lib.rs:74-103`) to cap libpg_query's one quadratic path. If mini-gateway takes
   (c1), it needs the same guard — and must then decide what to do with skipped input;
   Supavisor fails open, mini-gateway would have to fail closed (refuse or reset).
6. **Startup `options` allow-list.** Supavisor drops every startup `-c` option except six
   it understands (`startup_options.ex:83-93`), so session settings cannot ride in on the
   startup packet. mini-gateway should do the same for its shared pool.

## Could not verify

- COULD NOT FIND the commit that removed `v2.0.0`'s read/write routing and simple
  `PREPARE` broadcast; I only know both exist at `v2.0.0` and are absent at HEAD.
- COULD NOT FIND any code that resets a backend in transaction mode; searched for
  `server_reset_query`, `DISCARD`, `RESET ALL`, `DEALLOCATE ALL`, `attempt_cleanup`.
- COULD NOT FIND any result cache or coalescing.
- Did not verify that `pg_query`'s `nodes()` walker visits every subtree (CTEs, subqueries,
  `RETURNING`, default expressions). I believe it does, from memory; not checked in the
  crate source.
- Did not verify which libpg_query/PostgreSQL version `pg_query` 6.2.1 bundles (from
  memory: PostgreSQL 17).
- Which Supavisor version Supabase production runs, and therefore whether the Supabase
  docs' pipelining sentence describes current production: could not tell.
- Did not run any probe; all probe results are read from code.
