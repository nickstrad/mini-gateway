# PgBouncer — the baseline

Canvas 01 for mini-gateway chunk 5. Research only. All line numbers refer to the clones
listed under Sources, at the commits recorded there. Evidence tags: **VERIFIED IN CODE**,
**DOCS ONLY**, **COULD NOT FIND**.

## Sources

| Project | URL | Commit SHA | Commit date | Local / web |
| --- | --- | --- | --- | --- |
| PgBouncer | https://github.com/pgbouncer/pgbouncer | `23347f670c2296837012fd9a04f1bb2ac6fdcd45` | 2026-11-25 07:56 UTC | LOCAL SOURCE (`repos/pgbouncer`, depth-1 clone) |
| PgBouncer features page | https://www.pgbouncer.org/features.html | — | fetched 2026-11-25 | WEB READ (`websearch fetch`) |
| PostgreSQL (for the server side of the signals PgBouncer relies on) | already cloned in `repos/postgres` | `4483697be0e0d22b9b4fd847e8bf2d998c56a741` | 2026-11-25 17:31 +0100 | LOCAL SOURCE (read only, not modified) |

Newest PgBouncer release: **1.26.1, 2026-10-14** (`NEWS.md:7`; tag `pgbouncer_1_26_1` per
`git ls-remote`). First public release: 1.0, 2007-03-13 (`NEWS.md`, last entry).

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| PgBouncer | **Nothing** on the data path. Query text is forwarded unread. (The admin console has three regexes, only for its own `pgbouncer` pseudo-database.) Reads protocol fields only: startup parameters, `ParameterStatus` and `ReadyForQuery` from the server, the CommandComplete *tag*, and the statement *name* in Parse/Bind/Describe/Close. | (1) Track and replay the 10 server-reported (GUC_REPORT) settings, learned from `ParameterStatus`, replayed as `SET k=v;` when a connection is lent. (2) `server_reset_query` (`DISCARD ALL`) on release — default **session mode only**; transaction mode only with `server_reset_query_always=1`. (3) Otherwise document-and-shrug (feature matrix says "Never"). Pools keyed by (database, server user). | None. No read/write split, no result sharing. Protocol-level prepared statements are shared across clients by **exact byte equality** of query text + parameter types. | Startup parameters: **allow-list** (tracked ∪ `ignore_startup_parameters`; anything else disconnects). SQL: no list. | Untracked session state (temp tables, `PREPARE`, `LISTEN`, advisory locks, holdable cursors, non-reported GUCs, and `search_path` on PG ≤ 17) silently persists on the server connection and is seen by the next client of the same pool. | Open for session state in transaction mode (by design; documented). Closed for startup parameters. Closed for "dirty" connections: a server that is not idle (`'I'`) at release is disconnected. |

## PgBouncer

### 1. Does it read SQL?

**No, not on the data path.** VERIFIED IN CODE.

`handle_client_work` (`src/client.c:1456`) switches on the message type byte only. For a
simple `Query` the only check is the `disable_pqexec` switch; the body is never opened:

```c
// src/client.c:1465-1472
case PqMsg_Query:
	if (cf_disable_pqexec) {
		slog_error(client, "client used \"Query\" packet type");
		disconnect_client(client, true, "PQexec disallowed");
		return false;
	}
	track_outstanding = true;
	break;
```

After acquiring a server (`find_server`, `src/client.c:1655`) the packet is forwarded whole
with `sbuf_prepare_send` (`src/client.c:1718`). No function in `src/` tokenises SQL.

What it **does** read, exactly (all VERIFIED IN CODE):

| Input | Where | What is read | Purpose |
| --- | --- | --- | --- |
| StartupMessage key/value pairs | `decide_startup_pool`, `src/client.c:992-1062` | `database`, `user`, `options` (only `-c k=v` / `--k=v`, `set_startup_options`, `src/client.c:858-951`), `application_name`, `replication`, `_pq_.*` | Pool choice; seed the client's tracked-variable cache. Unknown keys → `disconnect_client(..., "unsupported startup parameter")` (`src/client.c:1057-1060`). |
| `ParameterStatus` from server | `load_parameter`, `src/server.c:30-67`; dispatched at `src/server.c:210` (login) and `src/server.c:457` (work) | key, value | Update server's and linked client's variable cache. |
| `ReadyForQuery` status byte | `src/server.c:433-455` | `'I'`, `'T'`, `'E'` | Decide when the server may be released; statement-mode enforcement. |
| `CommandComplete` tag | `src/server.c:542-555` | exact strings `DEALLOCATE ALL`, `DISCARD ALL` (only if prepared-statement tracking is on) | Forget its own prepared-statement bookkeeping. |
| Parse / Bind / Describe / Close | `inspect_parse_packet` etc., `src/messages.c:4-39`; `handle_parse_command`, `src/prepare.c:300` | statement **name**; for Parse also the query bytes + parameter type OIDs, as an opaque hash key | Protocol-level prepared statements in transaction/statement mode (1.21+). |
| Admin console `Query` | `admin_parse_query`, `src/admin.c:1447`; regexes `src/admin.c:54-63` | `CMD [arg]`, `SET k = v` | Only for the virtual `pgbouncer` admin database (`admin_handle_client`, `src/admin.c:1496`); not the proxy path. |

The prepared-statement code treats the query text as bytes. The global cache is a hash on
`query_and_parameters` (text plus parameter types), and each distinct byte string gets the
name `PGBOUNCER_<n>`:

```c
// src/prepare.c:122-126, 146-150
HASH_FIND(hh, prepared_statements, pkt->query_and_parameters,
	  pkt->query_and_parameters_len, ps);
...
ps->stmt_name_len = (uint8_t)snprintf(ps->stmt_name, sizeof ps->stmt_name,
	PREPARED_STMT_NAME_FORMAT, ps->query_id);   // "PGBOUNCER_%" PRIu64, include/prepare.h:4
```

Keeping in step with PostgreSQL releases: there is no grammar to keep in step. What has to
track Postgres is the **list of reported parameters** (below), which changed in 1.26.0
because PG 18 started reporting `search_path` (`NEWS.md`, 1.26.0 "Features").

### 2. Session-state safety

PgBouncer uses three mechanisms, none of which reads SQL.

#### 2a. Track and replay reported parameters (varcache). VERIFIED IN CODE.

**The tracked list** is fixed in `init_var_lookup`, plus anything in `track_extra_parameters`:

```c
// src/varcache.c:98-110
const char *names[] = {
	"DateStyle", "client_encoding", "TimeZone", "standard_conforming_strings",
	"application_name", "default_transaction_read_only", "IntervalStyle",
	"search_path", "scram_iterations", "session_authorization", NULL
};
```

**Why exactly these.** They are the PostgreSQL parameters flagged `GUC_REPORT` that a client
can change. In the Postgres clone, `GUC_REPORT` is set on 15 entries in
`src/backend/utils/misc/guc_parameters.dat`; the other five (`in_hot_standby`,
`integer_datetimes`, `is_superuser`, `server_encoding`, `server_version`) are `PGC_INTERNAL`,
so a client cannot change them. The PgBouncer docs say the same thing in words: "By default,
PgBouncer tracks all the parameters that Postgres reports to the client and that can be
changed by the client" (`doc/config.md:237-238`). The signal is the only reason for the list:
"PgBouncer only learns about changes made with `SET` for parameters that Postgres reports to
the client" (`doc/config.md:258-260`). VERIFIED IN CODE (both sides) + DOCS.

**Capture.** Every `ParameterStatus` from a server updates the server's cache and, if a client
is linked, that client's cache:

```c
// src/server.c:48-53  (load_parameter)
varcache_set(&server->vars, key, val);
if (client) {
	slog_debug(client, "setting client var: %s='%s'", key, val);
	varcache_set(&client->vars, key, val);
}
```

`varcache_set` ignores keys that are not in the lookup map (`src/varcache.c:142-145`).

**Why capture is text-independent (server side).** PostgreSQL sends `ParameterStatus` for any
changed `GUC_REPORT` variable just before it waits for the next query, regardless of how the
value changed:

```c
// postgres src/backend/utils/misc/guc.c:2490-2501 (comment on ReportChangedGUCOptions)
 * This is called just before we wait for a new client query.
 * ... a ParameterStatus message is sent at most once per variable per query,
 * even if the variable changed multiple times within the query.  That's quite
 * possible when using features such as function SET clauses.
```

It is called from the main loop at `src/backend/tcop/postgres.c:4826`, right before
`ReadyForQuery`. So `SET`, `set_config()`, a function body, a view, `RESET`, `SET LOCAL`
reverting at commit — all produce the same signal, if the value at the end of the statement
differs from the last one reported. VERIFIED IN CODE (Postgres side).

**Replay.** When a client is lent a server, `find_server` calls `varcache_apply` before
forwarding the client's packet (`src/objects.c:961-967`):

```c
// src/varcache.c:266-280  (varcache_apply)
HASH_ITER(hh, lookup_map, lk, tmp) {
	sval = get_value(&server->vars, lk);
	cval = get_value(&client->vars, lk);
	changes += apply_var(pkt, lk->name, cval, sval);
}
*changes_p = changes > 0;
if (!changes)
	return true;
...
return pktbuf_send_immediate(pkt, server);
```

`apply_var` emits `SET key=<quoted value>;` for each differing variable, or `RESET key;` when
the client has no value but the server does (`src/varcache.c:171-251`). All of them go out as
**one simple Query** built by PgBouncer itself. The client is paused (`src/objects.c:976-982`,
`server->setting_vars = true`) and the replies are swallowed (`src/server.c:613-615`). An
error during replay disconnects the server (`src/server.c:472-487`, "invalid server
parameter"). When the replay finishes, `varcache_set_canonical` copies the server's canonical
spelling back to the client cache so the next comparison does not see a false difference
(`src/server.c:866-885`, `src/varcache.c:283-346`).

**Gap for parameters the server does not report.** If a tracked parameter is not
`GUC_REPORT` on this server (e.g. `search_path` on PG ≤ 17, or anything added via
`track_extra_parameters` that Postgres does not report), PgBouncer only knows the startup
value; a later `SET` is invisible and leaks:

> "Changes made with `SET` after connecting are not tracked for such parameters, so they leak
> to other clients sharing the server connection, unless `server_reset_query_always` is used."
> (`doc/config.md:270-277`) — DOCS ONLY for the leak; the code that handles the unreported
> case is `varcache_set_canonical`, `src/varcache.c:293-327` (VERIFIED IN CODE).

The 1.26.1 notes also report "various bugs ... around tracking of `search_path`" still open
(`NEWS.md`, 1.26.1 "Fixes"). DOCS ONLY.

#### 2b. Reset on release (`server_reset_query`). VERIFIED IN CODE.

Defaults (`src/main.c:323-330`): `server_reset_query = "DISCARD ALL"`,
`server_reset_query_always = 0`, `server_check_query = "<empty>"` (meaning: send an empty
query). The release path:

```c
// src/objects.c:1231-1242  (release_server)
if (*cf_server_reset_query && (cf_server_reset_query_always ||
			       connection_pool_mode(server) == POOL_SESSION)) {
	/* notify reset is required */
	newstate = SV_TESTED;
} else if (cf_server_check_delay == 0 && *cf_server_check_query) {
	/* deprecated: ... */
	newstate = SV_USED;
}
```

`SV_TESTED` leads to `reset_on_release` (`src/objects.c:1317-1318` → `1173-1184`), which sends
the reset string as a simple Query; a send failure disconnects the server. So:

| Pool mode | Reset run on release by default? | With `server_reset_query_always = 1` |
| --- | --- | --- |
| session | yes, `DISCARD ALL`, when the client disconnects | yes |
| transaction | **no** | yes, after **every** transaction |
| statement | **no** | yes, after every statement |

`release_server` is reached from the server callback on `SBUF_EV_FLUSH` once the server is
`ready` (last `ReadyForQuery` said `'I'`) and no requests are outstanding
(`src/server.c:888-913`). `server_check_query` is a liveness probe, not a reset: it is run by
`launch_recheck` (`src/janitor.c:80-120`) on connections idle longer than
`server_check_delay` (default 30 s, `doc/config.md`), and has no session-state role.

What `DISCARD ALL` covers (Postgres side, `src/backend/commands/discard.c:58-79`, VERIFIED IN
CODE): close all portals (incl. holdable cursors), reset `session_authorization`,
`ResetAllOptions()` (all GUCs), drop all prepared statements, `UNLISTEN *`, release all
session advisory locks (`LockReleaseAll(USER_LOCKMETHOD, true)`), reset plan cache, drop temp
namespace, reset sequence caches. It refuses to run inside a transaction block
(`PreventInTransactionBlock`, line 67) — which is safe here because release only happens at
status `'I'`.

The docs describe the `always` switch frankly: "This setting is for working around broken
setups that run applications that use session features over a transaction-pooled PgBouncer.
It changes non-deterministic breakage to deterministic breakage: Clients always lose their
state after each transaction." (`doc/config.md:695-703`). DOCS.

#### 2c. Dirty-connection rule. VERIFIED IN CODE.

A server found not `ready` when it should be idle is closed, never reused:
`find_server` (`src/objects.c:940-941`, "idle server got dirty"), `launch_recheck`
(`src/janitor.c:92-94`), and the janitor (`src/janitor.c:433-436`, "SV_IDLE/SV_USED server got
dirty"). Note that `ErrorResponse`/`NoticeResponse` do not by themselves dirty a connection
any more; the comment at `src/server.c:462-471` explains the old behaviour.

#### 2d. Everything else: document and shrug.

Temp tables, SQL `PREPARE`, `LISTEN`, session advisory locks, `WITH HOLD` cursors and
non-reported GUCs are not detected, not refused, not reset in transaction mode by default.
They stay on the server connection. The feature matrix (section 4) marks them "Never".

#### 2e. Pool keying. VERIFIED IN CODE.

`get_pool` searches the **server user's** pool list for the database (`src/objects.c:776-791`).
The user is the login user unless the `[databases]` entry forces one:

```c
// src/client.c:344-349  (finish_set_pool)
if (client->db->forced_user_credentials)
	pool_user_credentials = client->db->forced_user_credentials;
else
	pool_user_credentials = client->login_user_credentials;
client->pool = get_pool(client->db, pool_user_credentials);
```

What this gives for free: in the default setup, leaked session state can only reach another
client **logged in as the same database role on the same database**. Role-level privileges,
`ALTER ROLE ... SET` defaults and row-level security keyed on `current_user` all stay
correct across the leak, because the server session's role never changes.

What it does **not** give: (i) any isolation between two clients of the same role (the usual
case for an application with one DB user); (ii) any isolation when the database entry forces a
user — then every client login shares one pool, which is exactly mini-gateway's
"one pool shared across tenants" situation. In that configuration PgBouncer's isolation is only
2a (10 parameters) plus 2b if `server_reset_query_always=1`. A Go proxy sharing one pool across
tenants cannot borrow the role boundary at all.

### 3. Read/write and shareability decisions

**None.** PgBouncer has no read/write routing, no result cache and no request coalescing.
COULD NOT FIND any code that classifies a statement as read or write. The only multi-host
feature is `load_balance_hosts` (round-robin over a host list, `doc/config.md:1407-1420`),
chosen per new server connection, not per statement. DOCS ONLY (not traced in code beyond the
doc entry).

The one sharing it does: protocol-level prepared statements with byte-identical text and
parameter types share one server-side name (`src/prepare.c:119-154`). This is sharing of a
*plan*, not of a *result*, so a miss costs nothing semantically. Inference, not verified: the
cache key does not include `search_path`, so a statement prepared on a connection under one
client's `search_path` and reused by a client with another could resolve names differently;
I did not find code that guards against this and did not test it.

Statement-mode enforcement uses the status byte, not text:

```c
// src/server.c:446-455
if (state == 'I') {
	ready = true;
} else if (connection_pool_mode(server) == POOL_STMT) {
	disconnect_server(server, true, "transaction blocks not allowed in statement pooling mode");
	return false;
} else if (state == 'T' || state == 'E') {
	idle_tx = true;
}
```

So in statement mode `BEGIN` is executed by Postgres, the reply `ReadyForQuery('T')` arrives,
and PgBouncer then kills the server connection (and with it the open transaction).
Fails closed, after the fact, with no text read.

### 4. Documented limitations (their words)

From https://www.pgbouncer.org/features.html (WEB READ, 2026-11-25):

> "Transaction pooling — A server connection is assigned to a client only during a
> transaction. When PgBouncer notices that the transaction is over, the server will be put back
> into the pool. This mode breaks a few session-based features of PostgreSQL. You can use it
> only when the application cooperates by not using features that break."

> "Note that “transaction” pooling breaks client expectations of the server *by design* and
> can be used only if the application cooperates by not using non-working features."

| Feature | Session pooling | Transaction pooling |
|---|---|---|
| Startup parameters [1] | Yes | Yes |
| SET/RESET | Yes | Never |
| LISTEN | Yes | Never |
| NOTIFY | Yes | Yes |
| WITHOUT HOLD CURSOR | Yes | Yes |
| WITH HOLD CURSOR | Yes | Never |
| Protocol-level prepared plans | Yes | Yes [2] |
| PREPARE / DEALLOCATE | Yes | Never |
| ON COMMIT DROP temp tables | Yes | Yes |
| PRESERVE/DELETE ROWS temp tables | Yes | Never |
| Cached plan reset | Yes | Yes |
| LOAD statement | Yes | Never |
| Session-level advisory locks | Yes | Never |

> "[1] Startup parameters are: `client_encoding`, `DateStyle`, `IntervalStyle`, `Timezone`,
> `standard_conforming_strings`, and `application_name`. PgBouncer detects their changes and so
> it can guarantee they remain consistent for the client."
> "[2] You need to change `max_prepared_statements` to a non-zero value to enable this support."

Note: the features page footnote lists 6 parameters, but the code tracks 10
(`src/varcache.c:98-110`) and `doc/config.md:237-249` lists 10. The web page lags the code.
"SET/RESET: Never" is also stricter than the code since 1.26.0, where `SET` of the 10 tracked
parameters (on servers that report them) is replayed correctly.

From the repo docs:

> `server_reset_query`: "When transaction pooling is used, the `server_reset_query` is not
> used, because in that mode, clients must not use any session-based features, since each
> transaction ends up in a different connection and thus gets a different session state."
> (`doc/config.md:686-689`)

> `max_prepared_statements`: "This tracking and rewriting of prepared statement commands does
> not work for SQL-level prepared statement commands, so `PREPARE`, `EXECUTE` and `DEALLOCATE`
> are forwarded straight to Postgres. The exception to this rule are the `DEALLOCATE ALL` and
> `DISCARD ALL` commands, these do work as expected and will clear the prepared statements that
> PgBouncer tracked for the client that sends this command." (`doc/config.md:394-399`)

The "exception" is implemented by reading the server's CommandComplete **tag**, not the
client's text (`src/server.c:542-555`, introduced in 1.22.0, `NEWS.md:406`):

```c
// src/server.c:542-551
if (is_prepared_statements_enabled(server)
    && (pkt->len == 1 + 4 + 15 || pkt->len == 1 + 4 + 12)) {	/* size of complete DEALLOCATE/DISCARD ALL */
	const char *tag;
	if (mbuf_get_string(&pkt->data, &tag)) {
		if (strcmp(tag, "DEALLOCATE ALL") == 0 ||
		    strcmp(tag, "DISCARD ALL") == 0) {
			free_server_prepared_statements(server);
			if (client)
				free_client_prepared_statements(client);
```

> `disable_pqexec`: "Simple Query allows multiple queries in one packet, which allows some
> classes of SQL-injection attacks." (`doc/config.md:328-331`)

### 5. Language, license, maturity, dependency weight

- C (C11 since 1.26.0, `NEWS.md`), about 21,000 lines in `src/*.c` (`wc -l`). ISC license
  (`COPYRIGHT:3`).
- First public release 1.0, 2007-03-13; latest 1.26.1, 2026-10-14; last commit on the day of
  this read (2026-11-25). Very active.
- Dependencies: libevent 2.0+, optional OpenSSL and c-ares (`README.md:24-28`), plus the
  vendored `libusual` (`lib/`).
- Reuse by a Go proxy: nothing to link — there is no SQL component to reuse. What transfers
  is ~400 lines of *idea*: `varcache.c` (402 lines) and the release state machine. The varcache
  algorithm would be about 150 lines of Go and needs no dependency.

### Probe table (transaction mode, defaults, PostgreSQL 18+ server unless noted)

| Probe | What PgBouncer does | Code path | Outcome |
| --- | --- | --- | --- |
| P1 `SET search_path = x` | Forwards unread. PG 18+ answers with `ParameterStatus search_path`; PgBouncer records it in server and client caches. When the connection is next lent to a client whose cached value differs, PgBouncer first sends `SET search_path=<client value>;`. | `src/client.c:1465`; `src/server.c:457-460` → `30-53`; `src/objects.c:961`; `src/varcache.c:253-281` | **Safe on PG 18+** (tracked & replayed). On PG ≤ 17: not reported → **leaks** to the next client of that pool (`doc/config.md:270-277`). |
| P2 `SELECT set_config('search_path','h',false)` | Same as P1: the signal is generated by the server, not read from the text. | as P1; Postgres `guc.c:2490-2530` | Same as P1. For a non-reported GUC (e.g. `work_mem`, `role`) via any route: **leaks**. |
| P3 `SELECT f()`, f runs `SET` / creates a temp table | `SET` of a reported GUC inside f → reported at statement end (unless f's `SET` clause reverts it) → tracked as P1. Temp table → nothing seen. | as P1 | Reported GUC: safe. Temp table: **persists on the connection**, visible to the next client of the pool ("Never" in matrix). |
| P4 lexical tricks (`E'ok'\n'\' \|\| set_config(...) --'`) | Irrelevant: text is never lexed. Outcome equals P2. | `src/client.c:1465-1472` | Same as P2 — the trick buys an attacker nothing and costs PgBouncer nothing. |
| P5 `PREPARE p AS …` | Forwarded; lives on the server connection. | — | **Leaks** (matrix: Never). `DEALLOCATE ALL`/`DISCARD ALL` reset PgBouncer's own protocol-level bookkeeping via CommandComplete tag (`src/server.c:542-555`). |
| P5 `LISTEN c` | Forwarded; registration stays on the server. Later `NotificationResponse` is passed to whichever client is linked, or dropped with a warning if none. | `src/server.c:563-568`, `673-679` | **Leaks**: notifications reach the wrong client or none (matrix: Never). |
| P5 `CREATE TEMP TABLE t(...)` | Forwarded. | — | **Persists** (matrix: Never, unless `ON COMMIT DROP`). |
| P5 `SELECT pg_advisory_lock(1)` | Forwarded. | — | **Lock stays held by the server connection** after the client's transaction (matrix: Never). |
| P5 `DECLARE c CURSOR WITH HOLD …` | Forwarded. | — | **Persists** (matrix: Never). |
| P5 with `server_reset_query_always = 1` | `DISCARD ALL` after every transaction. | `src/objects.c:1231-1234`, `1173-1184`; Postgres `discard.c:58-79` | All P5 state is wiped before the next client. |
| P6 `SELECT 1; SET search_path = x` | One Query, forwarded whole; the server returns one `ReadyForQuery`. The `SET` effect is tracked as P1. In statement mode it is **allowed** (ends in `'I'`). | `src/server.c:446-455` | As P1. |
| P7 (`nextval`, writable CTE, `FOR UPDATE`, `now()`, view over volatile function, `EXPLAIN ANALYZE DELETE`) | n/a — no read/write or cache decision exists. | — | n/a |

## Claims of completeness

PgBouncer makes **no** claim to decide anything from SQL text. Its documentation claims the
opposite: transaction pooling "breaks client expectations of the server *by design*"
(features page) and clients "must not use any session-based features" (`doc/config.md:686-689`).
The only guarantee claimed — that the tracked parameters "remain consistent for the client"
(features footnote 1) — rests on a server signal, and survives the code read, with two
documented exceptions: parameters the server does not report (`doc/config.md:270-277`) and the
open `search_path` bugs noted in 1.26.1. The features page under-states what the code tracks
(6 vs 10 parameters).

## Mapping to options (a) (b) (c1) (c2)

- **Not (a), (c1) or (c2).** It reads no SQL, has no parser and never asks the server to
  enforce read-only.
- **Closest to (b), but with the reading removed entirely.** The `server_reset_query_always`
  mode is exactly (b)'s "strict" switch — `DISCARD ALL` after every lease — with no "known
  clean" fast path; PgBouncer simply pays the reset every time or not at all. Its default
  transaction mode is the "document-and-shrug" position the explainer describes, *plus* a
  mechanism (varcache replay) that the assessment does not mention.
- (b)'s substring "known clean" filter has no counterpart in PgBouncer. PgBouncer's
  counterpart to "known clean" is a protocol fact: for reported GUCs, the server tells it.

## Mechanisms not in the assessment

1. **Track-and-replay driven by `ParameterStatus` (varcache).** The assessment treats session
   settings as something to *detect from text* or *wipe with a reset*. PgBouncer does neither
   for the 10 reported parameters: it lets the server announce every change and replays the
   client's view (`SET k=v; ...` in one Query) when lending the connection. Detection is
   complete for those parameters no matter how the change was made (SET, `set_config`, a
   function, a view, a lexical trick), because the server emits the message after the
   statement, from its own state (`guc.c` `ReportChangedGUCOptions`, called at
   `postgres.c:4826`). For mini-gateway on PG 18+ this covers `search_path`,
   `default_transaction_read_only`, `TimeZone`, `DateStyle`, `IntervalStyle`,
   `client_encoding`, `standard_conforming_strings`, `application_name`,
   `session_authorization`, `scram_iterations`.
   *Go cost:* ~150 lines: a per-tenant/per-client map and a per-connection map; update both on
   every `ParameterStatus` the wire layer already parses; on lease, diff and send one
   `SET`/`RESET` Query, swallow its replies, close the connection on error. The chunk-4 reply
   tracker already sees `ParameterStatus`. Limit: covers only `GUC_REPORT` parameters; on
   PG ≤ 17 `search_path` is not one.
2. **A cheaper "known clean" signal than text: "did any `ParameterStatus` arrive during the
   lease?"** (Our derivation from 1, not something PgBouncer does.) It is not a full
   session-state oracle — temp tables, `PREPARE`, `LISTEN`, advisory locks and non-reported
   GUCs emit nothing — so it can only *add* reasons to reset, never prove cleanliness.
3. **Server reply tags as a signal.** PgBouncer reacts to the CommandComplete tag
   (`DEALLOCATE ALL`, `DISCARD ALL`) instead of the client's text (`src/server.c:542-555`).
   Generalised for mini-gateway: every simple-query statement produces a CommandComplete tag
   naming what actually ran (`SET`, `PREPARE`, `LISTEN`, `DECLARE CURSOR`,
   `CREATE TABLE`, `SELECT n`, `DISCARD ALL`, ...). A proxy can mark a lease *dirty* whenever
   any tag in the reply is outside a short allow-list (`SELECT`, `INSERT`, `UPDATE`,
   `DELETE`, `MERGE`, `BEGIN`, `COMMIT`, `ROLLBACK`, ...). This is **after the fact** (the
   statement has already run, so it cannot refuse), but it is exact about top-level
   statements in a multi-statement Query, immune to lexical tricks, and costs no parser. It
   does **not** see effects inside functions (`SELECT f()` reports `SELECT 1`), so it cannot
   replace reset for P3; it can replace the text-based taint list of option (b) for the
   top-level forms (P1, P5, P6). *Go cost:* ~40 lines in the reply tracker. PgBouncer itself
   only uses two tags; the generalisation is ours.
4. **Startup parameters as an allow-list that fails closed.** Unknown startup keys disconnect
   the client (`src/client.c:1057-1060`) unless explicitly ignored. A proxy can do the same:
   accept only parameters it knows how to replay.
5. **Byte-identical keying without parsing** for prepared statements (`src/prepare.c:119-154`).
   Shows that sharing keyed on exact bytes needs no SQL reading; the coalescer already keys
   on exact text. Not a safety mechanism by itself.
6. **Status-byte enforcement after the fact** (statement mode, `src/server.c:446-455`): kill
   the connection when the server reports a state the mode forbids, instead of predicting it
   from text. mini-gateway already does the equivalent for transaction end (decision D4).

## Could not verify

- Any SQL lexing or parsing on the data path in PgBouncer: COULD NOT FIND (searched
  `src/client.c`, `src/server.c`, `src/objects.c`, `src/prepare.c`, `src/messages.c`,
  `src/varcache.c`; the only regexes are in `src/admin.c` for the admin console).
- Any reaction to `NoticeResponse` beyond pass-through: COULD NOT FIND (`src/server.c:560-561`
  is a bare `break`).
- Any read/write classification or result caching: COULD NOT FIND.
- That `load_balance_hosts` acts per server connection: DOCS ONLY (not traced in code).
- Whether the prepared-statement cache can misbind across clients with different
  `search_path`: inference only, not tested and no guard found.
- The specific open `search_path` tracking bugs referred to in 1.26.1: DOCS ONLY (`NEWS.md`);
  issue numbers not looked up.
- Whether PostgreSQL ever sends `ParameterStatus` for a GUC that changed and changed back
  within one statement: the comment at `guc.c:2496-2500` says redundant reports are avoided
  (`ReportGUCOption` compares with the last sent value); I did not read `ReportGUCOption`
  itself.
- The explainer's "from memory" claims about PgBouncer are now checked: it does not parse SQL
  (VERIFIED); it runs `DISCARD ALL` on release, off by default in transaction mode
  (VERIFIED, `src/objects.c:1231-1232`, `src/main.c:329-330`); pools are per database and user
  (VERIFIED, with the forced-user exception above).
