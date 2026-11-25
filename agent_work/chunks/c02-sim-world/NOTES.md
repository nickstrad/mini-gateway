# Chunk 2 — Simulated world: builder notes

Built by Opus on 2026-11-25 on branch `work/c02-sim-world` (worktree
`/root/Software/mini-gateway-work/c02`) from `/root/briefs/zgateway-p2-c02-sim-world.md`.
Durable facts for later builders: `docs/knowledge/sim-world.md`.

## What was built

| Deliverable | Where |
| --- | --- |
| Fake Postgres on simnet | `internal/sim/fakepg/`: `config.go`, `server.go` (accept, table, invariants, state), `backend.go` (per-connection state machine and output pump), `exec.go` (statements, transactions, writes, cancel), `settings.go` (SET/RESET/SHOW/DISCARD, `ParameterStatus`), `match.go` (statement splitter and matcher), `frame.go` (server framer), `proto.go` (the one file importing `pgproto3`), `fault.go` (24 fault kinds), `log.go` (write, statement and transaction logs), `snapshot.go` |
| Test client (sans-I/O) | `internal/testclient/`: `frame.go` (client framer), `msg.go` (codec, the one file importing `pgproto3`), `session.go` (client state machine: phase, status byte, key data, parameters) |
| Simulated clients | `internal/sim/workload/`: `config.go`, `workload.go`, `client.go` (client actor), `keys.go` (uniform/Zipf/fixed key choice), `fault.go` (6 client fault kinds), `snapshot.go`, `probe.go` (a hand-driven client used by tests and by the fidelity runner) |
| Float generators, isolated | `internal/randdist/randdist.go` (Poisson gaps, Zipf); allow-listed in `tools/lint-rules/detlint-allow.txt` |
| Scenario type, driver interface, baselines, fidelity runner | `internal/sim/world/`: `driver.go` (`Driver`, `SimDriver`, `Observation`), `scenario.go` (`Scenario`, `Run`, `RunWith`, `Drive`, `Verify`, `Lookup`), `baselines.go` (five scenarios), `check.go` (write-log and transaction checks, `Quantile`), `fidelity.go` (script, tokens, transcript, `SimTarget`) |
| Recorded Postgres 17 transcript | `internal/sim/world/testdata/fidelity/postgres17.txt` (written by tier R) |
| Pinned seeds | `internal/sim/world/testdata/seeds.txt`: 1, 2, 3, 7, 42 |
| CLI | `cmd/mgsim/world.go` (`runWorld`, result table); two small edits in `main.go` and `main_test.go` (see Kernel changes) |
| Tier R | `scripts/tier-r.sh` (marker `scripts/.not-built/tier-r` deleted); tests `internal/sim/world/tierr_test.go` |
| Gate extra (G10) | `agent_work/chunks/c02-sim-world/gate/extra.sh`: every world scenario, seeds 1 and 42, in two processes, identical digests |
| Rules files | `det-packages.txt` (+ `internal/sim/fakepg`, `internal/sim/workload`, `internal/sim/world`, `internal/testclient`, `internal/randdist`); `detlint-allow.txt` (4 lines); `imports.txt` (the test client is `internal/testclient`, which is the placeholder path, so only its comment changed); `race-packages.txt` unchanged (no concurrency) |

`go.mod`: exactly one requirement, `github.com/jackc/pgx/v5 v5.13.0` (the version the probe
used; it was in the module cache). `go mod tidy` adds no indirect line.

## Kernel changes

The simulator API itself (`internal/sim`, `simnet`, `simtest`, `obs`) is unchanged.

| File | Change | Why |
| --- | --- | --- |
| `cmd/mgsim/main.go` | +5 lines: import `world`; `list` also prints the world scenarios; `run <name>` falls back to `world.Lookup` when the toy has no such scenario | the brief: `cmd/mgsim run <scenario> -seed N` runs any scenario |
| `cmd/mgsim/main_test.go` | `TestListNamesEveryScenario` counts toy + world scenarios | follows from the line above |

## Decisions the brief left open

1. **Package layout.** The test client is `internal/testclient` (sans-I/O, in the
   determinism list) so that chunk 6's load generator and real-socket tests can reuse it;
   the simnet actor around it is `workload.Probe`. The server and the client each have their
   own framer: the server's must also frame untyped startup packets (max 10,000 bytes, as
   Postgres), the client's must take the one-byte answer to an encryption request.
2. **The scenario world is its own package** `internal/sim/world` (scenario type, driver,
   baselines, fidelity runner); `fakepg` and `workload` do not import each other.
3. **Floats.** An adversarial chunk 1 test (`agent_work/adversarial/c01-sim-kernel`) forbids
   float syntax anywhere under `internal/sim/`. So the two float functions are in
   `internal/randdist` (outside `internal/sim`), listed in `det-packages.txt` with exactly two
   allow lines, one per function. The Zipf exponent is configured as integer thousandths
   (`Keys.Zipf = 1200` means s = 1.2); `v` is fixed at 1.
4. **`pgproto3` in a determinism-checked package.** detlint's import list does not include
   it; each of the two importing files has an allow line (`proto.go`, `msg.go`).
   `StartupMessage.Encode` ranges over a map, so the client encodes the startup packet itself
   (parameters in a fixed order); the server decodes with `pgproto3` and only reads keys.
5. **Startup refusal order** follows Postgres: an encryption request is answered `N` first;
   the startup packet is read; then, in order: protocol version (`0A000`, only 3.0 is
   accepted), the restart window (`57P03`), `max_connections` (`53300`), no user (`28000`),
   unknown user (`28000`, only when `Config.Users` is set), unknown database (`3D000`, only
   when `Config.Databases` is set; the database defaults to the user name). Every refusal is
   a `FATAL` `ErrorResponse` and then the server closes.
6. **`max_connections` model.** A connection is counted as a session at accept time if
   fewer than the limit are counted (sessions include those still in startup); otherwise it
   is a "dead end" that is answered `53300` after its startup packet. A cancel connection is
   uncounted when its packet is read. The registered invariant `sessions <= MaxConnections`
   is checked after every event. `Snapshot.Sessions.Peak` is the number to compare with the
   limit; `ConnsOpen.Peak` also counts dead ends.
7. **Concurrency model.** Each backend runs one statement at a time. With
   `Config.Capacity = C > 0` at most C statements are in service; the rest wait in one FIFO
   inside the database (bounded by the number of backends) and their service time starts when
   they get a slot. A statement is "in service" from the slot to its end (`InService`), or
   "waiting" (`Waiting`).
8. **Service time** of a statement = the per-kind time if `Service.PerKind` names its kind,
   else `Service.Fixed`; plus a uniform integer draw in `[0, JitterMax]` from the backend's
   own stream (`fakepg/service/<conn id>`); plus every `PerKey` entry for its key; plus the
   `pg_sleep` argument; plus the slow-connection fault's extra. Then for each stall window
   `[From, To)` that overlaps `[start, start + time]` the window's extra is added once
   (statements starting in or in service during the window; a zero-time statement starting
   in the window counts). A window injected with `pg.stall-window` also adds its extra to
   every statement already in service at that instant (to the remaining time if the server
   is frozen). The boundary fault replaces all of it with exactly its value, and an injected
   window does not extend statements while the boundary fault is on.
9. **Snapshot reads.** A read takes its value when its service starts (after any capacity
   wait), from the transaction's own pending writes first, else the table. Writes apply at
   statement end outside a transaction, at `COMMIT` inside one. There are no row locks: two
   transactions writing one key both commit, last commit wins (a stated limit).
10. **Implicit transactions.** A `Query` with more than one statement and no open
    transaction runs as an implicit transaction (counted in `TxBegun`/`TxCommitted`): an error
    aborts the rest of the message and rolls back its earlier writes; a `BEGIN` inside it turns
    it into an explicit one (`BEGIN; SELECT 1` leaves `T`). A single statement outside a
    transaction is autocommit and has transaction id 0 in the logs.
11. **`SET` is transactional** (rolled back with its transaction, as in Postgres).
    `ParameterStatus` for a changed reported setting is sent just before `ReadyForQuery`.
12. **Output and back pressure.** Replies go to a per-backend queue of segments; a big row
    set is a generator encoded at most 64 KiB at a time. The backend reads its next message
    only when the queue is not blocked by the simnet window, so a client that stops reading
    stops the backend. Time-delayed segments (first-byte, trickle, asymmetric faults) do not
    block reading, up to 1,024 waiting segments.
13. **`RowDescription` is sent when a SELECT-like statement starts** (constant, `txid`, read,
    `pg_sleep`, rows, `SHOW` of a known setting), as Postgres 17 does. The first tier R run
    found this: a terminated `pg_sleep` shows `T1` before `FATAL 57P01`.
14. **Logs.** Write log, statement log and transaction log are rings with a cap
    (`LogConfig.Cap`, default 1,048,576, max 16,777,216) or count-only (`LogCountOnly`); the
    transaction log uses the statement log's configuration. Every entry pushed out or not
    kept is counted (`WriteLogDropped`, `StatementLogDropped`, `TxLogDropped`). A statement
    record carries the rows a write changed (`Rows`); a transaction record its outcome
    (committed, rolled back, lost) and number of pending writes. Exact checks
    (`world.CheckWriteLog`) refuse to run on a log that dropped entries.
15. **Client tagging.** With `Tenant.Tag` each statement starts with `/*c<client id>*/`; the
    fake records the comment as the statement's `Tag` in both logs. Off by default so
    identical reads stay byte-identical.
16. **"Sent" and "intended".** A request is *intended* when its arrival event fires (open
    loop: on the schedule, whatever the server does; closed loop: think time after the previous
    reply). It is *sent* when its first statement is written. The client never pipelines, so
    with a slow server requests wait in the client's backlog and the wait is part of the
    latency. Claim 7 is narrowed accordingly (see "Narrowed claims").
17. **Retries** are per request, scheduled as events: `Retry{Max, Backoff, Cap, OnConnect,
    OnLost, OnTimeout, OnCodes}`, backoff `Backoff * 2^(attempt-1)` capped at `Cap`, no jitter.
    `OnCodes` applies to a query `ERROR` (since the repair round) as well as to startup and
    mid-query `FATAL`s. A retried request keeps its intended time and re-runs its original
    statements (a failed transaction's `ROLLBACK` does not replace them). Off by default
    (`Max = 0`).
18. **The direct baselines' ablation.** Section 6.2 asks that "mechanism off must fail the
    invariant". The baselines have no mechanism: they are the control arms. What stands in:
    the planted-bug canaries (below), the coordinated-omission control
    (`LatencyFromSend`), and `Verify`'s "counters must move" rules.

## The statement set (`fakepg.Match`)

Splitting (`fakepg.Split`): on `;` outside single-quoted strings and `/* */` comments;
pieces that are empty or only comments are dropped; more than 64 statements in one message
gives `ERROR 54000` (inside an explicit transaction it fails the transaction: `E`). Matching: tokens (words, numbers with `.`, single-quoted strings with
`''`, punctuation, comments skipped; a leading comment becomes the tag); keywords are
case-insensitive; at most 64 tokens. Exactly these shapes, everything else is
`ERROR 42601 syntax error at or near "<first token>"`:

| Statement | Kind | Reply |
| --- | --- | --- |
| `SELECT <number>` / `SELECT '<text>'` | const | `T` `D` `C SELECT 1` |
| `SELECT <number> / 0` | div-zero | `ERROR 22012` (no `T`) |
| `SELECT txid_current() IS NOT NULL` | txid | `T` `D t` `C SELECT 1` |
| `SELECT v FROM kv WHERE k = '<k>'` | read | `T` (0 or 1) `D` `C SELECT n` |
| `SELECT v, pg_sleep(<s>) IS NOT NULL FROM kv WHERE k = '<k>'` | read (+ sleep) | `T` (2 columns) `D [v,t]` `C SELECT n` |
| `SELECT pg_sleep(<seconds, decimal>)` | sleep (max 1 h) | `T` `D ''` `C SELECT 1` |
| `SELECT repeat('<s>', <n>) FROM generate_series(<lo>, <hi>)` | rows (row size ≤ 1 MiB, ≤ 10,000,000 rows) | `T` `D`×count `C SELECT count` |
| `UPDATE kv SET v = '<v>' WHERE k = '<k>'` | update | `C UPDATE 0/1` |
| `INSERT INTO kv VALUES ('<k>', '<v>')`, also `INSERT INTO kv (k, v) VALUES (…)` | insert | `C INSERT 0 1`, or `ERROR 23505` if the key exists, or `ERROR 53100` if the table (with the transaction's own pending inserts and deletes) would pass `MaxRows` |
| `DELETE FROM kv WHERE k = '<k>'` | delete | `C DELETE 0/1` |
| `SET [SESSION] <name> = / TO <value>` | set | `C SET`; unknown name without a dot `42704`; read-only `55P02` |
| `RESET <name>` / `RESET ALL` | reset | `C RESET` |
| `SHOW <name>` | show | `T` `D` `C SHOW`, unknown `42704` |
| `DISCARD ALL` | discard | `C DISCARD ALL`; inside a transaction `ERROR 25001` |
| `BEGIN`, `BEGIN WORK/TRANSACTION`, `START TRANSACTION` | begin | `C BEGIN`; again inside one: `WARNING 25001` notice first |
| `COMMIT`, `END` (+ `WORK/TRANSACTION`) | commit | `C COMMIT`; in a failed transaction `C ROLLBACK`; outside one `WARNING 25P01` first; `ERROR 53100` and rolled back if its inserts no longer fit in `MaxRows` (an implicit transaction's commit reports the same error before `ReadyForQuery`) |
| `ROLLBACK`, `ABORT` (+ `WORK/TRANSACTION`) | rollback | `C ROLLBACK`; outside one `WARNING 25P01` first |
| empty string, only `;` | — | `EmptyQueryResponse` |

Inside a failed transaction every statement but commit/rollback gets `ERROR 25P02`.
Settings: the 14 reported ones (below) plus `statement_timeout`, `lock_timeout`,
`idle_in_transaction_session_timeout` (integers shown with `ms`), `search_path`,
`work_mem`, and any custom name with a dot. Frontend messages other than `Q` and `X` get
`FATAL 08P01` and a close (no extended protocol).

Startup parameters, in this order: `application_name`, `client_encoding`, `DateStyle`,
`default_transaction_read_only`, `in_hot_standby`, `integer_datetimes`, `IntervalStyle`,
`is_superuser`, `scram_iterations`, `server_encoding`, `server_version` (`17.0 (fakepg)`),
`session_authorization`, `standard_conforming_strings`, `TimeZone` — the same 14 names as
Postgres 17.11 (tier R checks the sorted list).

## Database-side faults (section 10.4, non-TLS)

Injected with `s.InjectAt(sim.Fault{...})` or `s.ApplyFault`. Target = backend connection id,
or 0 = all backends / the server. Clear = `FaultClear` (55) with `Arg1` = the kind. Every
application is in the trace (kernel `fault` record; armed faults add `pg.fire` when they
fire) and counted in `Snapshot.Faults.ByKind` (clears in `Cleared`, missing targets in
`NoTarget`). One named subtest each in `fakepg/fault_test.go: TestEachDatabaseFault`; each
asserts the observation, the counter, the trace record, that after the clear the fault's
own effect is gone (an `after` check per clearable case: rows arrive together again, whole
pieces on the old and a new connection, one row for a read, a fast reply on the formerly
slow connection, two sessions accepted, a transaction that commits, …), and that after the
clear (or after the fault, for one-shot kinds) a fresh client completes `SELECT 1`.
`TestEveryFaultKindHasACase` fails if a kind has no case; `TestEveryClearPathIsChecked`
disables each kind's clear path in turn (test-only `PlantSkipClear`) and fails if a clear
case still passes or a clearable kind has no clear case. One-shot kinds (no clear path):
terminate, dirty-state, param-status, kill, half-open; targeted resets and targeted garbage
are one-shot per backend.

| Catalog entry | Kind (no.) | Target, args | What the client observes, in order (subtest) | Clear |
| --- | --- | --- | --- | --- |
| refuses connections | `pg.refuse` (32) | — (applies simnet refuse to the listener) | `refused` (refuse) | new dials accepted |
| accepts and never answers, at startup | `pg.silent-startup` (33) | — | `connected`, nothing else; server keeps the socket open until the client closes (silent-startup) | new connections answered; silent ones stay silent |
| accepts and never answers, after a query | `pg.silent-query` (34) | backend or 0 | startup completes; the query gets nothing; the statement counts as started and in service; when the client closes it is aborted (silent-after-query, silent-after-query-one-target: the other backend answers) | later queries answered |
| connect stalls | `pg.connect-stall` (35) | — (simnet dial stall) | nothing, not even `connected` (connect-stall) | new dials complete |
| slow first byte | `pg.first-byte` (36) | Arg2 = delay | every reply's first byte waits the delay (slow-first-byte: ≥ 150 ms) | off |
| slow trickle of rows | `pg.trickle` (37) | Arg2 = gap | row messages one at a time, exactly the gap apart (slow-trickle: 10 ms) | off |
| fragmented delivery, down to one byte, inside headers | `pg.fragment` (38) | backend or 0; Arg2 = piece size | same messages; simnet delivers one byte per piece (fragment-to-one-byte); target 0 also applies to new connections, and a later targeted fault does not change that | simnet chunk cleared on all |
| reset now / before / during / after a reply / mid-transaction | `pg.reset` (39) | Arg1 point 0–4 (`PointNow` needs a target); target 0 arms all backends until cleared, a target arms one shot | now: `reset`; before: query reaches the database, `reset`, no reply byte; during: `T D reset` (cut one byte into the second message); after: full reply then `reset` 10 ms later (reset-after-reply, and one shot: reset-after-reply-one-target); mid-transaction: `C:BEGIN ZT` then `reset`, transaction counted lost; a delayed reset waits while the server is frozen | disarms |
| garbage bytes / impossible lengths | `pg.garbage` (40) | Arg1 0 random 64 bytes, 1 length 2, 2 length 0x7ffffff0; next reply then close; a targeted fault keeps its own mode and does not change the server-wide one | `T protocol-error` (lengths), closed without `ReadyForQuery` (random) | off |
| a non-Postgres reply | `pg.not-postgres` (41) | next reply (startup too) is an HTTP 400 then close | `connected protocol-error` | off |
| reply without its end marker (probe B9) | `pg.terminate` (42) | backend or 0 | in-flight statement aborted: `E:57P01` (FATAL) then `eof`, no `ReadyForQuery` | one-shot |
| very large rows, very many rows | `pg.bloat` (43) | Arg1 rows, Arg2 bytes per row, added to every read reply | `rows 1001 bytes 1000001` for one real row plus 1,000 × 1,000 B (the rows statement covers this without a fault); on the two-column read the bloat rows have two columns (`t` second) | off |
| unexpected state at checkout | `pg.dirty-state` (44) | backend; Arg1 0 open transaction, 1 changed `search_path`; a backend still in startup is not a target (counted `NoTarget`) | `SELECT 1` ends `ZT`; or `SHOW search_path` = `fakepg_dirty` | one-shot |
| unexpected `ParameterStatus` | `pg.param-status` (45) | backend or 0; backends still in startup are skipped (none ready: `NoTarget`) | unsolicited `S:application_name` = `fakepg-surprise-N` while idle | one-shot |
| killed | `pg.kill` (46) | — | every connection `reset` at once; table kept | one-shot |
| frozen | `pg.freeze` (47) | Arg2 duration (0 = until clear) | nothing moves: replies and new startups wait; service timers and delayed resets pause; after the clear the reply arrives | thaws |
| restarted cold | `pg.restart` (48) | Arg2 window (0 = until clear) | all `reset`; new connections `E:57P03 eof` during the window; afterwards accepted; table kept (restarted-cold: 100 ms window; restarted-until-clear) | ends the window |
| `max_connections` reached | `pg.max-conn` (49) | Arg1 = limit override, clamped to `[0, Config.MaxConnections]` (the configured limit stays a hard ceiling) | `E:53300 eof` | limit restored |
| latency exactly at a timeout boundary | `pg.boundary` (50) | Arg2 = exact service time | reply at exactly the value plus the round trip (250.1 ms) | off |
| some connections slow, others fine | `pg.slow-conn` (51) | backend; Arg2 extra per statement | target slow, other fast | per target |
| half-open | `pg.half-open` (52) | backend (simnet half-open, server gone) | nothing ever; the fake has dropped the backend; the client's close ends it as simnet `HalfOpen` | one-shot |
| asymmetric latency | `pg.asym` (53) | Arg2 extra on every byte sent down | reply 30 ms later than service (40.1 ms) | off |
| backend stall window (150–200 ms spec stall) | `pg.stall-window` (54) | Arg1 window length, Arg2 extra | statements starting or in service in the window take the extra (160.1 ms), including those already in service when it is injected | restores the configured windows |

Not here, by the brief: answers `N` to a TLS request when the proxy requires TLS, and a
certificate that fails verification (chunk 4).

## Client-side faults (section 10.4, non-TLS) and what the server sees

| Catalog entry | How | What the fake sees (test) |
| --- | --- | --- |
| sends half a message and stops | `wl.half-message` (96) on a client: its next statement is written half, the client goes silent for 1 s then leaves | the half message stays in the framer, no statement starts, the connection stays open; after the client closes: closed, still no statement (`TestClientFaultHalfMessage`) |
| stops reading replies | `wl.stop-reading` (97), Arg2 = how long (0 = until cleared) | the backend writes until the simnet window is full and stops (`WritesRefused > 0`, ≤ 2 windows out); finishes after the client reads again (`TestClientFaultStopsReading`) |
| disconnects while queued | `wl.disconnect` (98) Arg1 `DisconnectNow`, Arg2 1 = reset, while its statement waits for a capacity slot | the waiting statement is aborted at once (`TestClientFaultDisconnectWhileQueuedInTheDatabase`) |
| disconnects mid-statement | `wl.disconnect` now, with close or reset | close: the statement finishes, the reply is discarded, then the backend reads EOF and closes; reset: the statement is aborted at once (`TestClientFaultDisconnectMidStatement`). Right after sending: close → the query arrives and runs; reset → the bytes in flight are discarded and the database never sees it (`TestClientFaultDisconnectRightAfterSending`) |
| disconnects mid-transaction | config hook `Tenant.Disconnect{Request, Point: DisconnectInTx}` or `wl.disconnect` Arg1 2 | the transaction is lost (`TxLost`), its writes never apply; the client reconnects for its next request (`TestClientFaultDisconnectMidTransaction`) |
| …while a coalesced waiter | the hook: `Disconnect{Request, Point}` chooses the request and the point of the conversation; chunk 11 uses it | — |
| reconnects in a burst | `wl.burst` (99): Target = first client id, Arg1 = N clients, Arg2 = window; each drops its connection and reconnects at a uniform time in the window | baseline 2: refusals `53300` beyond `max_connections` |
| retries aggressively | `Tenant.Retry` (a refused or failed client schedules its own retry event) | with max connections 0: every attempt refused, `Refused.TooMany = clients × (1 + Max)`; after the clear the retries succeed (`TestClientRetriesAggressively`) |
| sends a cancel | `wl.cancel` (100), Arg1 1 = wrong key: a new connection with `CancelRequest`, closed by the server (or after 1 s) | right key: `CancelsApplied`, the client gets `57014`; wrong key: `CancelsNoMatch`, statement completes (`TestClientSendsACancel`) |
| load: open-loop arrival above capacity | baseline 4 | the fake queues inside (waiting peak > 0) |

Also: `TestClientTimesOutOnItsOwnDeadline` (`Tenant.Deadline`), `TestBacklogCapCountsCouldNotSend`.

## Cancel

A `CancelRequest` on a new connection names a backend by process id and 4-byte secret
(`firstPID` 10,000 + accept order; secret from the stream `fakepg/keys`). Match and a
statement waiting or in service: that statement ends with `ERROR 57014 canceling statement
due to user request`, the rest of its message is skipped, an explicit transaction becomes
failed (`E`). Match and idle: nothing (`CancelsIdle`). No match or wrong secret: nothing
(`CancelsNoMatch`). The server never answers the cancel connection; it closes it.

## Outcomes and conservation (workload)

Per request, final outcome: `ok`, `error` (an `ErrorResponse` code, also a `FATAL` mid-request
such as `57P01`), `connection-lost` (reset/EOF/protocol error with no code), `timed-out`
(client deadline), `client-left` (client fault), `connect-failed` (the request was never sent:
dial refused or a startup refusal; its code, such as `53300`, is counted in `ErrorsByCode`),
and `dropped` (the backlog was full: "could not send"). Conservation, per tenant and total,
registered as an invariant after every event and checked by `TenantSnapshot.Check()`:

- `intended = sent + not yet sent + dropped + failed before send`
- `sent = ok + error + lost + timed out + client left + in flight`

(A sent request that is lost and retried stays in flight until its final outcome.)

fakepg, registered after every event and `Snapshot.Check()`: `accepted = open + closed`;
`statements started = finished + in service + waiting + aborted`; `transactions begun =
committed + rolled back + lost + open`; `cancels received = applied + idle + no match`; and
`sessions ≤ max_connections`.

## Scenarios, driver, CLI

`world.Driver` (implemented by `SimDriver`; tier R will add one in chunk 6):
`Start(Setup)`, `Addr()`, `Connect(workload.Config)`, `Inject(sim.Fault)`, `Run(d)`,
`Observe() Observation`, `Stop()`. `Observation` holds database numbers that a real Postgres
can also give (connections accepted and peak, sessions peak, `53300` refusals, reads,
statements, writes) and the workload snapshot. `world.Drive(d, setup, duration)` is the
whole scenario run against any driver.

`Scenario.Verify` always requires: no driver or kernel error, quiesce (in `Stop`),
conservation per tenant, the workload did something, the database accepted a connection;
then the scenario's own check. `TestScenario` runs every scenario on the pinned seeds plus
`DST_SEEDS`, twice per seed (`simtest.RequireDeterministic`, REPLAY line on failure) and
requires byte-identical snapshots between the two runs. `cmd/mgsim run <name> -seed N`
prints `scenario= … status=`, both digests, the result table and the `REPLAY:` line.

### The direct baseline — numbers per pinned seed (commit of this file's chunk)

| Scenario | Setup | Seed 1 | 2 | 3 | 7 | 42 |
| --- | --- | --- | --- | --- | --- | --- |
| `direct-500-connections` | 500 clients connect first, 5 reads each 20 ms apart (spread), hold 200 ms; `max_connections` 1,000 | sessions peak **500**, accepted 500, reads 2,500, ok 2,500; p50 2.77 ms p99 3.26 ms | 500 / 2,500 | 500 / 2,500 | 500 / 2,500 | 500 / 2,500 |
| `direct-reconnect-burst` | `max_connections` 100; 100 steady clients (closed loop, 30 reads, think 10 ms) + 400 idle; at 100 ms all 500 drop and reconnect within 50 ms; no retry | refused `53300` **553**, sessions peak 100, accepted 770, requests failed before send 170, ok 2,830 | 598 / 820 / 220 | 562 / 780 / 180 | 517 / 730 / 130 | 589 / 810 / 210 |
| `direct-1000-identical-reads` | 1,000 clients connected first send `SELECT v FROM kv WHERE k = 'hot'` at the same instant (20 ms); service 50 ms | database reads **1,000**, ok 1,000, p50 = p99 50.86 ms | 1,000 | 1,000 | 1,000 | 1,000 |
| `direct-open-loop-overload` | capacity 4 × 10 ms (400/s); 40 clients × 20 requests, 50 ms apart each (800/s) | intended = sent = ok **800**; p50 507.5 ms, p99 998.2 ms, max 1,005.8 ms; waiting peak > 0 | same p50/p99 | same | same | same |
| `direct-mixed-transactions` | 3 tenants × 10 clients × 10 Poisson requests (reads, writes, 3-statement transactions with think), Zipf s=1.2 over 50 keys, capacity 8, tagged | writes 316, statements 808, ok 300; write log exact, no transaction spans two backends or two clients | 324 / 848 | 292 / 764 | 311 / 808 | 292 / 752 |

The refusals in the burst exceed 400 because steady clients whose reconnect was refused open
a new connection for each later request, and some of those are refused too.
`TestOverloadLatencyGrowsWithTheBacklog` (seed 1): mean latency from the intended send time
per quarter of completions 132.9, 382.9, 632.9, 882.9 ms.

## Fidelity: the fake against Postgres 17

`world.ProbeScript()` — the probe's exchanges for the simple-query subset with trust
authentication, in this order: B0 startup; B1; B2 (error, rollback); B3; B4 (three
pipelined); B5 (+ cleanup); B6 (session state; `DISCARD ALL`); B7; B8 (200,000 rows, 20 MB);
B10 (a second connection writes while the first's `SELECT v, pg_sleep(1) IS NOT NULL …` runs;
the in-flight read must return the old value, and a later read the new one; values compared);
B9 (`pg_sleep(30)`, the backend terminated: the real side via `pg_terminate_backend` on a
third connection, the fake via `pg.terminate`). B0 uses trust on both sides, so the whole
startup is compared (`R:0 S*14 K Z:I`) plus the sorted parameter-status names; values that
legitimately differ (process id, key, server version, timings) are not in the tokens.

Tokens per message: `R:<auth>`, `S*<n>` (startup run) / `S:<name>`, `K`, `Z:<status>`,
`T<columns>`, `D` / `D*<n>` / `D[values]` (values only where the step asks), `C:<tag>`,
`E:<severity>:<code>`, `N:<severity>:<code>`, `I`, and `EOF` when the stream ended.

Result: identical transcripts. Recorded in `testdata/fidelity/postgres17.txt` (Postgres 17.11,
2026-11-25). CI: `TestFidelityAgainstRecordedPostgres17` replays the fake against it.

### What tier R covers (`scripts/tier-r.sh`, about 30 s)

| Test | Real side | Result (2026-11-25, 17.11) |
| --- | --- | --- |
| `TestTierRFidelity` | the script over TCP with the test client, compared with the fake live | identical (after the `RowDescription` fix) |
| `TestTierRManyConnections` | 500 real-socket clients, 5 reads each, all held; `pg_stat_activity` counted while held | 500 database connections |
| `TestTierRReconnectBurst` | 500 clients connect at random times within 50 ms against `max_connections=100` (`superuser_reserved_connections=0`), accepted ones held 5 s | 100 accepted, 400 refused with `53300` "sorry, too many clients already" |
| `TestTierRIdenticalReads` | 100 connections × 10 identical reads, counted with `pg_stat_statements` | 1,000 executions |

Tier R does not run the simulator scenarios through a real-Postgres `Driver` (chunk 6 adds
one); its three baseline tests are written directly with goroutines and the test client.
Thresholds there are exact counts except the burst (accepted ≤ 100, refused ≥ 400).

## Claims and the tests that prove them

| # | Claim | Tests (file: function) |
| --- | --- | --- |
| 1 | Message types and status bytes match Postgres 17 on the probe script | `world/fidelity_test.go: TestFidelityAgainstRecordedPostgres17` (CI); `world/tierr_test.go: TestTierRFidelity` (live); `fakepg/fakepg_test.go: TestStartupSequenceMatchesPostgres17`, `TestEncryptionRequestsAnsweredN` |
| 2 | Status after every sequence; a read in service never sees a later commit; rolled-back writes never apply | `fakepg/fakepg_test.go: TestTransactionStatusTable` (18 sequences, B1–B7 among them), `TestSessionStateAndParameterStatus`; `fakepg/semantics_test.go: TestReadInServiceDoesNotSeeLaterCommit`, `TestTransactionWritesApplyAtCommitAndVanishAtRollback`; fidelity step B10 |
| 3 | The write log is exact: every applied write once, none missing, none duplicated | `fakepg/semantics_test.go: TestWriteLogHasEveryAppliedWriteOnce`, `TestTransactionWritesApplyAtCommitAndVanishAtRollback`; `fakepg/repair_test.go: TestInsertInsideATransactionRespectsMaxRows`, `TestCommitThatNoLongerFitsIsRolledBack` (write log replayed against the table); `world/check.go: CheckWriteLog` (bijection with committed row-changing writes + replay) in scenario `direct-mixed-transactions` (all seeds); `world/check_test.go: TestCheckWritesFindsEveryKindOfMismatch`; canaries `fakepg/canary_test.go: TestPlantedBugDoubleWriteIsCaughtByTheWriteLogCheck`, `TestPlantedBugDroppedWriteIsCaughtByTheWriteLogCheck`, `TestPlantedBugPhantomWriteIsCaughtByTheWriteLogCheck` |
| 4 | `max_connections` is a hard bound; the refusal is the real one | `fakepg/semantics_test.go: TestMaxConnectionsRefusesWithRealError`; `fakepg/repair_test.go: TestMaxConnFaultCannotRaiseTheConfiguredLimit`; registered invariant `fakepg.conservation` (sessions ≤ limit after every event); scenario `direct-reconnect-burst`; tier R `TestTierRReconnectBurst` (the same code and text from real Postgres) |
| 5 | Every database fault: injectable, does what is defined above, counted, traced, cleared → its effect is gone and a fresh exchange succeeds | `fakepg/fault_test.go: TestEachDatabaseFault` (34 subtests), `TestEveryFaultKindHasACase`, `TestEveryClearPathIsChecked` (each clear path disabled must fail its case); `fakepg/repair_test.go`: the stall-window, targeted-fragment, targeted-garbage, ready-only, bloat and freeze tests; `world/property_test.go: TestScenarioRandomFaultSchedules` |
| 6 | Every client fault does what is defined above | `workload/clientfault_test.go`: all tests; `workload/snapshot_test.go: TestWorkloadSnapshotCountersMoveAndConserve`; scenario `direct-reconnect-burst` |
| 7 | NARROWED (see "Narrowed claims"): arrivals (intended sends) in a window = the schedule, whatever the server speed; each request's latency is measured exactly from its intended time; the wire send waits for the previous reply | `workload/generator_test.go: TestOpenLoopArrivalsIgnoreABusyClientAndLatencyIsExact` (the adversary's 17-arrival case: at 40 ms intended 17, sent 1, backlog 16; every latency = completion − intended time, each send at the instant the previous reply ended, histogram min/max = the trace), `TestRetriedRequestKeepsItsIntendedTime`, `TestOpenLoopArrivalsEqualIntendedWhateverTheServerSpeed` (exactly 500 in the window at service 0, 1 ms and 20 ms with capacity 1; times exactly on schedule), `TestLatencyIsMeasuredFromIntendedSendTime` (a 200 ms stall: 17 requests above 50 ms from the intended time, exactly 1 from the send time), `TestPoissonInterArrivalMean`, `TestZipfSharesMatchTheCalculation`, `TestClosedLoopThinkTime`; `world/overload_test.go: TestOverloadLatencyGrowsWithTheBacklog` |
| 8 | Cancel affects exactly the targeted backend's running statement | `fakepg/semantics_test.go: TestCancelHitsOnlyTheTargetBackend` (wrong key nothing, right key only that backend, idle nothing); `workload/clientfault_test.go: TestClientSendsACancel` |
| 9 | Replay law for every baseline and random fault schedules; the world always quiesces after faults clear | `world/scenario_test.go: TestScenario`; `world/property_test.go: TestScenarioRandomFaultSchedules` (500 seeds green during the build; 200 in the gate sweep), `FuzzWorldSchedule`; `fakepg/snapshot_test.go: TestSnapshotsAreIdenticalForOneSeed`; `workload/snapshot_test.go: TestWorkloadSnapshotsAreIdenticalForOneSeed`; gate extra (two processes) |
| 10 | 500 clients → 500 connections; burst → refusals by the database; 1,000 identical reads → 1,000 database reads | scenarios `direct-500-connections`, `direct-reconnect-burst`, `direct-1000-identical-reads` (`TestScenario`); tier R `TestTierRManyConnections`, `TestTierRReconnectBurst`, `TestTierRIdenticalReads` |
| 11 | `pgproto3` imported only in `fakepg` and the test client — in every Go file outside `tools/` (tests, nested modules, testdata included); `go.mod` has exactly one requirement | `testclient/imports_test.go: TestPgproto3IsImportedOnlyWhereTheRulesAllowInEveryGoFileOutsideTools` (today: `fakepg/proto.go`, `testclient/msg.go`, `testclient/session_test.go`); gate G1 importlint (`only … pgproto3 : internal/pgwire/... internal/sim/fakepg/... internal/testclient/...`, `require github.com/jackc/pgx/v5`); G0 `go mod tidy -diff` |

The property test found two real bugs while it was being written (both fixed): a request
that was sent, lost, retried and then refused at connect was counted "failed before send"
(conservation broke), and a negative shift in the retry backoff. The first gate run's timed
fuzzing found a third: `FuzzServerInput` sent a rows statement of up to 10 TB and closed;
simnet discards bytes written to a closed peer without a reset, so the backend streamed
forever. Fixed with `Config.MaxReplyBytes` (`TestReplyLargerThanTheLimitIsRefused`).

## Planted-bug canaries

Switches are the unexported `fakepg.Config.plant` fields, reachable only through
`fakepg/export_test.go` (`PlantDoubleApply`, `PlantIdleInTx`, `PlantDropWrite`,
`PlantPhantomWritesFrom`, `PlantSkipClear`):

- **double apply**: every write is applied and logged twice. `TestPlantedBugDoubleWriteIsCaughtByTheWriteLogCheck`
  requires `direct-mixed-transactions` to PASS without it and FAIL ("applied twice") with it, seeds 1–3.
- **idle in transaction**: `ReadyForQuery` says `I` inside a transaction.
  `TestPlantedBugIdleInTransactionIsCaughtByTheStatusTable` requires the status table to fail.
- **dropped write**: the nth commit-time apply silently does nothing (no table change, no log,
  no counter). `TestPlantedBugDroppedWriteIsCaughtByTheWriteLogCheck` (n = 1, 5, 40; seeds 1–3)
  requires "missing from the write log".
- **phantom writes**: from the nth apply on, writes are logged and counted but not applied.
  `TestPlantedBugPhantomWriteIsCaughtByTheWriteLogCheck` requires "replaying the write log".
- **skip clear**: one kind's clear is counted but does nothing. `TestEveryClearPathIsChecked`.

## Observability

- `fakepg.Snapshot()`: connections accepted, closed, open (value, peak), sessions (value,
  peak), startups; refusals by reason (too many, unknown user, unknown database, no user,
  bad startup, starting up, protocol); statements started, finished, aborted, errors, by
  kind; in service and waiting (value, peak); writes applied; transactions begun, committed,
  rolled back, lost, open; cancels received, applied, idle, no match; bytes in and out;
  parameter reports; table rows; log lengths and dropped entries (write, statement, transaction); faults by kind, cleared,
  no target. `Snapshot.Check()` checks the four conservation laws.
- `workload.Snapshot()`: clients, finished; faults by kind and no target; per tenant and
  total: intended, sent, not yet sent and in flight (value, peak), dropped, failed before
  send, ok, error, lost, timed out, client left, errors by code (sorted, at most 64 codes, the
  rest in `ErrorsOtherCodes`), retries, connects attempted/ok/failed, reconnects, open
  connections, half messages, stopped reading, cancels sent, disconnects, burst drops,
  latency histogram from the intended send time and from the actual send (`internal/obs`
  duration histogram). The total's peaks are sums of the tenants' peaks.
- Tests: `TestSnapshotCountersMoveAndConserve` (fakepg: every counter but the "current
  value" gauges moves somewhere across a busy run and the fault cases; all plain types),
  `TestWorkloadSnapshotCountersMoveAndConserve`, and the two byte-identical-snapshot tests.
- Trace kinds: `pg.acc`, `pg.start`, `pg.ref`, `pg.stmt`, `pg.done`, `pg.write`, `pg.close`,
  `pg.cancel`, `pg.fire`; `wl.arr`, `wl.send`, `wl.done` (D = latency from intended), `wl.conn`,
  `wl.retry`, `wl.drop`. State digest writers `fakepg` (counters, table, backends) and
  `workload` (counters per tenant, client states).
- Snapshots are later exported under the component names `fakepg` and `workload`
  (PLAN.md section 7.7); nothing here exports them yet (chunk 3).

## Steady-state row

| What grows | What removes or bounds it | Gauge |
| --- | --- | --- |
| The fake's backend table | removed on close | `ConnsOpen`, `Sessions` |
| Write log, statement log, transaction log | ring cap (`LogConfig.Cap`; the transaction log uses the statement log's) or count-only; dropped entries counted | `WriteLogLen`/`WriteLogDropped`, `StatementLogLen`/`StatementLogDropped`, `TxLogLen`/`TxLogDropped` |
| The table | `Config.MaxRows` (default 1,048,576): an insert beyond it is `ERROR 53100`, inside a transaction too (counting its pending inserts and deletes), and a commit that no longer fits is refused with `53100` and rolled back | `TableRows` |
| Per-backend output queue | simnet window (back pressure); delayed segments ≤ 1,024 | `BytesOut` |
| Bytes of one reply | `Config.MaxReplyBytes` (default 64 MiB, at most 4 GiB): a rows statement above it is `ERROR 54000` before any row; the bloat fault is clamped to it | — |
| Capacity wait queue | at most one entry per backend | `Waiting` |
| Pending writes of a transaction | 65,536, then `ERROR 54000` | — |
| Client backlog of not-yet-sent requests | `Tenant.Backlog` (default 1,024, max 1,048,576); beyond it the request is dropped and counted | `NotYetSent` (value, peak), `Dropped` |
| Per-tenant error codes | 64 distinct codes, the rest counted | `ErrorsOtherCodes` |
| Cancel connections | 65,536 per workload; each closed by the server or after 1 s | — |
| Probe event list | 1,048,576 events, the rest counted | `Probe.Dropped()` |

## Limits

- Simple-query subset only; no extended protocol, no `COPY`, no SCRAM (chunk 4), no TLS.
- The table is one `kv(k text primary key, v text)`; values cannot hold a `'` other than as
  `''`. No row locks: concurrent writers both commit, last commit wins.
- `SET` validates names, not values. `RESET ALL`/`DISCARD ALL` drop custom settings.
- A closed (FIN) client is noticed by a busy backend only after its statement, as in
  Postgres without `client_connection_check_interval`.
- Real Postgres's message texts are copied for the codes used here; other fields (detail,
  position, routine) are not sent.
- Determinism of the Poisson and Zipf generators is scoped to same commit, same toolchain,
  linux/amd64 (floats).
- One connection table entry per dial for the run (simnet `MaxConns`); 1,000 clients fit
  easily.
- The write-log replay check compares only the final table: a phantom write that a later
  write to the same key overwrites is invisible to it (the bijection check still needs a
  committed row-changing statement for every record).
- An `UPDATE` committed after another session deleted its row re-creates the row (no row
  locks; last commit wins), and the `MaxRows` commit check counts it as an insert.
- simnet gives a writer no reset when its peer has closed (Postgres would get `EPIPE` or
  `ECONNRESET`), so a backend keeps writing a reply to a closed client until the reply ends;
  `MaxReplyBytes` bounds that.

## Deviations

- **Size.** Product code is about 6,450 lines after the repair round (testclient 413, fakepg
  3,159, workload 1,729, world 931, randdist 23, cmd 203 including chunk 1's `main.go`), above
  the brief's ~1,800 × 2. The chunk is complete, so no split request; the bulk is the fault
  catalog (24 database kinds) and the client state machine. Tests are about 3,850 lines (98
  test and fuzz functions, 34 fault subtests, 24 clear-path canary subtests), still smaller
  than the product code: they are table-driven.
- **Claim 7** is narrowed (see "Narrowed claims"); **claim 11** holds for every Go file
  outside `tools/` (the scope the repair brief set).
- **Tier R** runs its three baselines directly (goroutines + test client), not through a
  real-Postgres `Driver` (that driver needs the real binary, chunk 6). The identical-read
  test uses 100 connections × 10 reads instead of 1,000 connections (VM memory).
- `scripts/tier-r.sh` was marked for chunk 6; the brief moved it here, so its header and
  marker changed in this chunk.

## Narrowed claims

For the orchestrator to accept or refuse. The code was not bent to fit the original wording.

**Claim 7 (open-loop generation).** Original: "the number of requests sent in a window equals
the intended number regardless of server speed, and latency is measured from intended send
time". The brief's own deliverable text says how a busy client behaves: "a client that is
busy does not delay the next intended send (requests queue in the client and their wait
counts as latency)". That is the built design: requests queue in the client. The Postgres
protocol does not strictly forbid pipelining simple `Query` messages (fidelity step B4 sends
three and gets three replies), but a workload request is a conversation on one session
(transactions of several statements with think time between them); a later request's
`Query` written while an earlier request's transaction is open would run inside that
transaction, and after an error would get `25P02`. So a client runs one request at a time
per connection, and "sent" (first statement on the wire) lags "intended" when the server is
slow. The precise claim, proved by `TestOpenLoopArrivalsIgnoreABusyClientAndLatencyIsExact`,
`TestRetriedRequestKeepsItsIntendedTime`, `TestOpenLoopArrivalsEqualIntendedWhateverTheServerSpeed`
and `TestLatencyIsMeasuredFromIntendedSendTime`:

- Arrivals (intended sends) are independent of the server: request k of an open-loop client
  is intended at its scheduled time (start + spread offset + the constant or Poisson gaps),
  so the number intended in any window is the scheduled number at any server speed.
- The wire send of a request happens when it reaches the head of the client's backlog and
  the connection is idle: at the very instant the previous request's last `ReadyForQuery`
  arrives (or when the connection becomes ready). Sent ≤ Intended at every instant, Sent =
  Intended once the backlog has drained, and Intended = Sent + NotYetSent + Dropped +
  FailedBeforeSend after every event (registered invariant).
- Each request's latency is exactly (arrival of its final `ReadyForQuery`) − (its intended
  time), retries included (a retry keeps the first intended time), and is recorded once, for
  outcomes `ok` and `error`; lost, timed-out, client-left, connect-failed and dropped requests
  are counted by outcome and not in the latency histogram. `LatencyFromSend` measures from the
  last attempt's send.

The adversary's `TestAdv_7_SendWindowIndependentOfService` asserts the original wording
(sent = intended at 40 ms) and still fails; under the narrowed claim that is expected.

**Claim 11 (pgproto3 imports).** Holds for every Go file in the repository outside `tools/`,
tests, nested modules and testdata included (`TestPgproto3IsImportedOnlyWhereTheRulesAllowInEveryGoFileOutsideTools`).
The adversary's `TestAdv_11_AllGoImportsIncludingTests` still fails, now only on five files
under `tools/detlint/cmd/importlint/testdata/mod/internal/`: importlint's own test inputs in a
nested fixture module, which must import `pgproto3` to test the rule (some of them are the
violations importlint must find). The repair brief scopes the claim to outside `tools/`.
The adversary's test is not wrong for the original wording; it is outside the narrowed one.

## Repair round

Repair builder (Opus), 2026-11-25, on `work/c02-sim-world` from `ddb6126`. Every finding was
reproduced first (the regression tests below failed before their fix).

| Item | Result |
| --- | --- |
| Review 1 (BLOCKER) transactional `INSERT` bypasses `MaxRows`, phantom write logged | fixed `6fc0e83`: check at the statement (pending net rows) and at commit (`53100`, rolled back); `apply` never logs a write it did not apply. Tests `fakepg/repair_test.go: TestInsertInsideATransactionRespectsMaxRows`, `TestCommitThatNoLongerFitsIsRolledBack` |
| Review 2 (BLOCKER) stall window misses statements in service and a zero-time statement at `From` | fixed `6fc0e83`: an injected window extends in-service timers (or the frozen remainder); overlap uses `[start, end]`. Tests `TestStallWindowExtendsStatementsAlreadyInService`, `TestStallWindowExtendsAFrozenStatement`, `TestStallWindowCatchesAZeroTimeStatementAtItsStart` |
| Review 3 (MAJOR) clear-and-recover cannot fail for fragment, trickle, bloat | fixed `42ac9a6`: an `after` check per clearable case; `TestEveryClearPathIsChecked` disables each kind's clear (all 18 clearable kinds, 24 cases) and requires failure. Also checked in a scratch copy by editing `clearFault` itself: the reviewer's M7b, M7c, M7d and three more (slow-conn, targeted silent query, max-conn) each fail `TestEachDatabaseFault` |
| Review 4 (MAJOR) `CheckWriteLog` misses dropped writes | fixed `c29a164`: transaction log + `StatementRecord.Rows`; bijection between committed row-changing writes and the write log, plus replay against the table. Tests `world/check_test.go: TestCheckWritesFindsEveryKindOfMismatch` (13 cases), canaries `TestPlantedBugDroppedWriteIsCaughtByTheWriteLogCheck`, `TestPlantedBugPhantomWriteIsCaughtByTheWriteLogCheck`. The reviewer's M9 (drop every write with `stmtSeq % 5 == 0`) now fails `TestScenario/direct-mixed-transactions` ("missing from the write log") |
| Review 5 (MAJOR) `FaultMaxConn` above `MaxConnections` breaks the invariant | fixed `6fc0e83`: the override is clamped to `[0, MaxConnections]`. Test `TestMaxConnFaultCannotRaiseTheConfiguredLimit` |
| Review 6 (MAJOR) targeted fragment / garbage clobber the server-wide settings | fixed `6fc0e83`: the server-wide fragment is set only for target 0; garbage mode is per backend for targeted faults. Tests `TestTargetedFragmentKeepsTheServerWideFragment`, `TestTargetedGarbageKeepsTheServerWideMode` |
| Review 7 (MAJOR) `int64` overflow in `matchRows` | fixed `6fc0e83`: `n` bounded before the multiply; the series length is computed without overflow. Test `TestMatchRowsRefusesOverflowingSizes`; `FuzzMatch` now asserts `Size ≥ 0`, `Rows ≥ 0` and seeds the overflow inputs |
| Review 8 (MAJOR) `Retry.OnCodes` ignored for query errors; failed transaction mutates its statements | fixed `b8be7ad`: the error exit goes through the retry policy; the original statements are kept (`orig`), the `ROLLBACK` is appended to a clipped copy. Tests `workload/repair_test.go: TestRetryOnCodesRetriesAQueryError`, `TestRetryOnCodesIgnoresOtherCodes`, `TestRetriedTransactionRunsItsOriginalStatements` |
| Review 9 (MINOR) ready-only faults counted on a starting backend | fixed `6fc0e83`: `applyTo` reports whether it applied. Test `TestReadyOnlyFaultsOnAStartingBackendCountAsNoTarget` |
| Review 10 (MINOR) bloat rows have one column on a two-column read | fixed `6fc0e83`: bloat rows carry the second column. Test `TestBloatOnATwoColumnReadKeepsTheColumnCount` |
| Review 11 (MINOR) freeze does not hold a delayed reset | fixed `6fc0e83`: the reset timer's remainder is held and re-armed at the thaw. Test `TestFreezeHoldsADelayedReset` |
| Review 12 (MINOR) stale stop-reading timer; wrong-key secret all zeros | fixed `b8be7ad`: the resume timer is stored and cancelled (test `TestStopReadingTimerOfAClearedFaultDoesNotResumeALaterOne`); the wrong key flips one bit of the real secret (no test can force a zero random secret; `TestClientSendsACancel` still covers the wrong-key path) |
| Review checklist "compounds" (1): terminate under back pressure tears the framing | fixed `7335d64`: the already-encoded bytes go out before the `FATAL`. Test `TestTerminateDuringABackPressuredReplyKeepsTheFraming`. (Compound 2 is review 8.) |
| Adversarial claim 2 `TestAdv_2_StatementLimitErrorAbortsTransaction` | fixed `6fc0e83`: the 64-statement error fails an explicit transaction. Test `TestStatementLimitErrorFailsTheTransaction`; the attack now holds |
| Adversarial claim 3 `TestAdv_3_FullTableCommitMustNotLogPhantom` | fixed `6fc0e83` (review 1); the attack now holds |
| Adversarial claim 5 `TestAdv_5_StallHitsAlreadyServing` | fixed `6fc0e83` (review 2); the attack now holds |
| Adversarial claim 7 `TestAdv_7_SendWindowIndependentOfService` | narrowed (above); latency accounting made exact and tested (`2f008a0`) |
| Adversarial claim 11 `TestAdv_11_AllGoImportsIncludingTests` | fixed outside `tools/` (`9bd436b`): the phase-1 probe (`agent_work/phase1/probes/pgwire/main.go`, its own module) is kept as `main.go.txt`, runnable from a scratch copy (command in `agent_work/INDEX.md`); repository-wide test added. Still fails on the `tools/` lint fixtures (narrowed, above). `PLAN.md` line 25 still names `main.go`; the orchestrator owns that file |
| Brief item 5: test gaps | `CheckWriteLog` detects missing writes (above); direct tests for exported fakepg and workload behaviour that had none (`3a22f13`: `fakepg/api_test.go`, `workload/api_test.go`: `Kind.IsWrite`, `CodeValue`, config defaults and limits, `New`'s refusals, fault and outcome names, transaction log, log ring, server framer, workload configuration errors, key names, `Done`, the `Probe` API). The remaining exported functions without a direct test are the simnet handler callbacks (`OnEvent`), exercised by every test |

Gate (`scripts/gate.sh c02-sim-world`, build stage, clean tree at `73c9ea5`): every check
passes or is a legitimate not-built skip (G7, G8) except **G3 unit**, which fails only on the
two adversarial attacks above (`TestAdv_7_SendWindowIndependentOfService`,
`TestAdv_11_AllGoImportsIncludingTests`), because G3 runs `go test ./...` including
`agent_work/adversarial/c02-sim-world`, which this round may not edit. Every product package
passes in G3; G4 sweep (200 seeds per scenario), G5 fuzz (7 targets × 20 s), G6 tier R
(Postgres 17.11) and G10 (cross-process replay) pass. So `overall=fail` until the
orchestrator accepts the two narrowed claims and decides what to do with those attacks.

Commit granularity: the first commit (`6fc0e83`) holds several fakepg defect fixes that touch
the same functions; each has its own regression test named above.

## Commands

```
PATH=/usr/local/go/bin:$PATH
go test ./internal/sim/fakepg ./internal/sim/workload ./internal/sim/world ./internal/testclient ./internal/randdist ./cmd/mgsim
DST_SEEDS=200 go test -run '^TestScenario' ./internal/sim/world        # the sweep, as G4 runs it
go test ./internal/sim/world -run '^TestScenario$/^direct-reconnect-burst$/^seed=7$' -seed=7 -count=1
go run ./cmd/mgsim list; go run ./cmd/mgsim run direct-reconnect-burst -seed 7
RUN_DIR=/tmp/tr scripts/tier-r.sh                     # tier R (Docker); MG_TIER_R_UPDATE=1 rewrites the recording
go test -run '^$' -fuzz '^FuzzServerInput$' -fuzztime 60s ./internal/sim/fakepg
scripts/gate.sh c02-sim-world
go test -count=1 ./agent_work/adversarial/...      # claims 7 and 11 attacks fail as described under "Narrowed claims"
```
