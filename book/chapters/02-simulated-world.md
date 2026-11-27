# 2. A fake Postgres and hostile clients

Chapter 1 built an empty stage: a seeded event loop, simulated byte-stream connections, and integer histograms. Nothing on that stage yet speaks Postgres or generates database traffic.

Before writing a line of proxy code, `mini-gateway` builds both sides the proxy will stand between: a fake Postgres server that can fail in every way real databases fail, and simulated clients that generate realistic and hostile load. Together with a scenario runner and a real-Postgres verification script, they also establish the **direct baseline**—what happens when clients talk straight to the database with no proxy in between. Every later protection (connection pooling, admission queues, shared reads) will be judged against those control numbers.

This chapter covers Chunk 2:

- the sans-I/O client state machine ([`internal/testclient`](../../internal/testclient)),
- the fake Postgres server ([`internal/sim/fakepg`](../../internal/sim/fakepg)) and simulated client actors ([`internal/sim/workload`](../../internal/sim/workload)),
- the isolated random-distribution helpers ([`internal/randdist`](../../internal/randdist)),
- the scenario driver, direct baselines, and fidelity runner ([`internal/sim/world`](../../internal/sim/world)),
- the extended `mgsim` runner ([`cmd/mgsim/world.go`](../../cmd/mgsim/world.go)), and
- the real-Postgres test runner ([`scripts/tier-r.sh`](../../scripts/tier-r.sh)).

After Chunk 2 lands, the simulator API is frozen.

---

## Why build a wire-level fake instead of a generic mock

It is tempting to test a proxy against an in-memory `Do(request) -> response` stub. Phase 1 probed a real Postgres 17.11 server and showed why that abstraction misdescribes Postgres in ways that matter directly to a connection pool ([section 4.1 of the plan](../../PLAN.md#41-evidence)):

- **The reply decides when a connection is free.** Sending `BEGIN; SELECT 1` in one message ends with a `ReadyForQuery` status byte of `'T'` (in transaction). An error inside a transaction ends with `'E'` (failed transaction) until `ROLLBACK` returns `'I'` (idle). A pool cannot know whether a backend may be lent to another client without reading that byte.
- **Replies are streams, not values.** One 55-byte query can return 200,000 `DataRow` messages totaling 20 MB. A backend must stream rows under back pressure rather than buffering whole replies in memory.
- **Failure mid-query has no end marker.** Terminating a running backend emits `FATAL 57P01` and closes the stream with no `ReadyForQuery`.
- **Session state outlives transactions, and cancellation uses a second connection.** `SET statement_timeout` stays in effect on the next exchange, and `SET application_name` emits an unsolicited `ParameterStatus` message. `CancelRequest` travels on a fresh TCP connection carrying the `(ProcessID, SecretKey)` pair from `BackendKeyData`.

Four other design choices shaped Chunk 2:

1. **`pgproto3` message types, our own framers.** Chunk 2 introduces the project's single external dependency in `go.mod`: `github.com/jackc/pgx/v5 v5.13.0` (`go mod tidy` adds no indirect requirement). `pgproto3.Frontend` and `pgproto3.Backend` wrap a blocking `io.Reader` and do not fit a single-threaded event loop. Instead, [`internal/sim/fakepg`](../../internal/sim/fakepg) and [`internal/testclient`](../../internal/testclient) keep small incremental framers (`Framer.Feed` / `Framer.Next`) that handle arbitrary splits down to one byte. They call only `pgproto3` message `Decode`/`Encode` methods, confined to one file per package (`fakepg/proto.go` and `testclient/msg.go`). Startup encoding is the one exception: `pgproto3.StartupMessage.Encode` ranges over a Go map in non-deterministic order, so `testclient.EncodeStartup` writes startup parameters directly in slice order.
2. **A hand-written matcher, not a SQL parser.** A real SQL parser would add thousands of lines without testing proxy invariants. [`fakepg.Match`](../../internal/sim/fakepg/match.go) splits a query on `;` outside single-quoted strings and `/* */` comments (up to 64 statements per message) and matches 18 fixed statement shapes over one table, `kv(k text primary key, v text)`. Anything else returns `ERROR 42601`.
3. **Floats isolated outside `internal/sim/`.** Open-loop Poisson inter-arrival gaps and Zipf key skew rely on `math/rand/v2`'s floating-point routines, while an adversarial test from Chunk 1 forbids float syntax anywhere under `internal/sim/`. Those two helpers therefore live in a 23-line package, [`internal/randdist`](../../internal/randdist/randdist.go). It is listed in [`tools/lint-rules/det-packages.txt`](../../tools/lint-rules/det-packages.txt) with two allow-list entries in [`tools/lint-rules/detlint-allow.txt`](../../tools/lint-rules/detlint-allow.txt). Workload configs pass the Zipf exponent as integer thousandths (`Keys.Zipf = 1200` for `s = 1.2`) so no struct inside `internal/sim/` holds a float.
4. **Decoupled packages behind a small driver interface.** `fakepg` and `workload` do not import each other; they meet only over `simnet`. [`internal/sim/world`](../../internal/sim/world/driver.go) connects them behind `world.Driver` (`Start`, `Addr`, `Connect`, `Inject`, `Run`, `Observe`, `Stop`), keeping `Observation` limited to numbers a real Postgres server can also report in Chunk 6. [`internal/testclient`](../../internal/testclient) is sans-I/O, so simulated clients (`workload.Probe`, `workload.Workload`) and real-socket tier R tests share one protocol state machine.

---

## Inside the fake Postgres (`internal/sim/fakepg`)

[`fakepg.Server`](../../internal/sim/fakepg/server.go) listens on `simnet` and creates one `backend` actor per accepted connection. It speaks the Postgres 3.0 simple-query subset with trust authentication (SCRAM and TLS arrive in Chunk 4).

### Startup, `max_connections`, and protocol order

When a client connects, `Server.accept` checks the active session count against `srv.limit()` (either `Config.MaxConnections` or a tighter `FaultMaxConn` override). If the server is at its limit or inside a cold-restart window, it still accepts the connection as an uncounted "dead end" so it can send Postgres's real in-protocol refusal after the client's startup packet, then close.

`backend.startup` follows Postgres's refusal order:

1. `SSLRequest` or `GSSENCRequest`: reply with a single `'N'` byte and stay in startup mode.
2. `CancelRequest`: uncount the session slot, run `Server.cancel(pid, secret)`, and close without replying.
3. Unsupported protocol version: `FATAL 0A000`.
4. Restart window: `FATAL 57P03` ("the database system is starting up").
5. `max_connections` reached: `FATAL 53300` ("sorry, too many clients already").
6. Missing or unknown user (when `Config.Users` is set): `FATAL 28000`. Unknown database (when `Config.Databases` is set): `FATAL 3D000`.

Accepted sessions receive `AuthenticationOk`, the exact 14 `ParameterStatus` messages real Postgres 17 sends (`application_name` through `TimeZone`), `BackendKeyData` (a sequential PID starting above 10,000 plus a 4-byte secret from the seeded stream `fakepg/keys`), and `ReadyForQuery('I')`. A registered simulator invariant (`fakepg.conservation`) checks `sessions <= Config.MaxConnections` after every event.

### Transactions, statement snapshots, and the write log

The server holds one sorted slice of `(key, value)` rows capped at `Config.MaxRows` (default 1,048,576). Each backend tracks explicit transactions (`BEGIN`/`START TRANSACTION`, `COMMIT`/`END`, `ROLLBACK`/`ABORT`), implicit transactions (multi-statement `Query` messages outside an explicit transaction), transactional `SET`/`RESET` state, and status bytes `'I'`, `'T'`, and `'E'`.

Two details are essential for later proxy chunks—especially the read coalescer in Chunk 11 and the exact write checks in Chunk 6+:

- **Statement-start snapshots and early `RowDescription`.** When a statement obtains a capacity slot in [`beginService`](../../internal/sim/fakepg/exec.go#L124-L146), a read captures its value immediately—from its own transaction's pending writes first, else the committed table—before the service timer runs. A commit arriving from another session while the read is in flight cannot change that read's result. Immediately afterward, `describeAtStart` emits `RowDescription` for `SELECT`-like statements. This matches Postgres 17, as caught during the first live tier R run when a terminated `pg_sleep` emitted `T1` before `FATAL 57P01`:

```go
if r.st.Kind == KindRead {
	r.readVal, r.readFound = b.read(r.st.Key)
}
d := srv.serviceTime(b, r)
if srv.faults.frozen {
	r.remaining = d
} else {
	r.timer = srv.s.After(d, func() { b.serviceDone(r) })
}
if b.describeAtStart(r) && !srv.faults.frozen {
	b.pump()
}
```

- **Sequential peak check at `COMMIT` and exact audit logs.** Writes inside a transaction stay buffered in `b.tx.writes` (capped at `MaxTxWrites` = 65,536) and vanish on `ROLLBACK` or connection loss. At `COMMIT`, [`peakRowsAfter`](../../internal/sim/fakepg/exec.go#L453-L475) walks the pending writes in execution order against the live table and rolls the whole transaction back with `ERROR 53100` if any intermediate step would exceed `Config.MaxRows`:

```go
func (srv *Server) peakRowsAfter(writes []pendingWrite) int {
	n := len(srv.table)
	peak := n
	present := map[string]bool{}
	for _, w := range writes {
		was, known := present[w.key]
		if !known {
			_, was = srv.get(w.key)
		}
		now := w.op != KindDelete
		switch {
		case now && !was:
			n++
		case !now && was:
			n--
		}
		present[w.key] = now
		if n > peak {
			peak = n
		}
	}
	return peak
}
```

Once a commit passes `peakRowsAfter`, every write applies cleanly and is appended to `WriteLog`. The server also records `StatementLog` (including affected row count and optional `/*c<id>*/` client tag) and `TransactionLog` (outcome: committed, rolled back, or lost). All three logs are bounded rings (`LogRing`, default cap 1,048,576, or `LogCountOnly`) with explicit drop counters. [`world.CheckWrites`](../../internal/sim/world/check.go#L31-L39) refuses to run on any log that dropped entries. When the log is complete, it checks three properties: no transaction spanned two backends or two tagged clients, `WriteLog` records form a bijection with committed row-changing statements, and replaying `WriteLog` over the initial rows matches the final table.

### Service time, capacity, streaming, and the 24 database faults

Each backend runs one statement at a time. When `Config.Capacity = C > 0`, at most `C` statements run concurrently across all backends. Additional statements wait in a FIFO queue (`waitQ`), and their service time starts only when they win a slot.

A statement's service duration is the sum of six terms: its per-kind (or `Fixed`) base time, uniform jitter from `fakepg/service/<conn id>`, per-key extra, `pg_sleep` time, any per-connection `FaultSlowConn` extra, and any overlapping stall window `[From, To)` (including zero-duration statements starting at `From`). Injecting `pg.stall-window` also extends every statement already in service, or adds to its remaining time if frozen.

Large row sets (`SELECT repeat(...) FROM generate_series(...)` and `FaultBloat`) are generated incrementally in `outFill` (64 KiB) chunks inside `backend.pump()`. Streaming stops when `simnet`'s send window fills (**back pressure**), and replies larger than `Config.MaxReplyBytes` (default 64 MiB) are refused up front with `ERROR 54000`.

[`internal/sim/fakepg/fault.go`](../../internal/sim/fakepg/fault.go) registers 24 fault kinds (`32..55`, disjoint from `simnet`'s `1..9`) covering every non-TLS database-side fault in [section 10.4 of the plan](../../PLAN.md#104-fault-catalog-for-the-simulator-from-the-notes-22-named-fault-modes-plus-the-specs-list):

| Group | Fault kinds | Behavior |
| --- | --- | --- |
| Connection & startup | `pg.refuse`, `pg.connect-stall`, `pg.silent-startup`, `pg.max-conn` | Listener refusal or dial stall via `simnet`; accept-then-silence at startup; tighten `max_connections` (clamped to `[0, Config.MaxConnections]`). |
| Silence & latency | `pg.silent-query`, `pg.first-byte`, `pg.trickle`, `pg.asym`, `pg.boundary`, `pg.slow-conn`, `pg.stall-window` | Accept query and never reply; delay first byte; space rows by a gap; add downward byte delay; pin exact service time at a timeout boundary; slow one backend; inject a stall window covering running and new queries. |
| Wire corruption & cuts | `pg.fragment`, `pg.reset` (`now`/`before`/`during`/`after`/`mid-tx`), `pg.garbage` (random / short length / huge length), `pg.not-postgres`, `pg.terminate`, `pg.bloat` | Split down to 1-byte chunks; reset at five conversation points; emit invalid lengths, random bytes, or HTTP 400; finish in-progress bytes then send `FATAL 57P01` + EOF without `ReadyForQuery`; pad reads with extra rows (preserving column count). |
| State & lifecycle | `pg.dirty-state`, `pg.param-status`, `pg.kill`, `pg.freeze`, `pg.restart`, `pg.half-open`, `pg.clear` | Inject an open transaction or changed `search_path`; emit unsolicited `ParameterStatus`; reset all backends while keeping table; freeze timers (including delayed resets) until thaw; cold restart with `57P03` window; half-open drop via `simnet`; clear any clearable fault. |

---

## Simulated clients and open-loop arrivals (`internal/sim/workload`)

[`workload.Workload`](../../internal/sim/workload/workload.go) runs per-tenant client actors over `simnet`, where each tenant's `Name` is sent as the Postgres startup `user`. Each client owns isolated random streams (`arrivals`, `keys`, `mix`, `retry`) and drives a [`testclient.Session`](../../internal/testclient/session.go).

### Open-loop arrivals without coordinated omission

A classic benchmarking bug—**coordinated omission**—happens when a load generator waits for one reply before scheduling the next request, or starts its latency timer only when a request hits the wire. When the server stalls for 200 ms, a closed-loop or send-time measurement reports a single slow request and hides the backlog of requests that should have been served during the stall.

`workload` separates **intended arrival** from **wire send**:

- In open-loop modes (`ArrivalConstant` and `ArrivalPoisson`), `client.arrive()` schedules the next arrival from the configured interval regardless of whether the server has answered.
- Because one simulated client represents a single Postgres session and a request can be a multi-statement transaction, a client never pipelines a second request onto a busy session (which would execute inside the first transaction). Instead, arrivals wait in the client's bounded backlog (`Tenant.Backlog`, default 1,024), and overflow is dropped and counted.
- Latency (`TenantSnapshot.Latency`) is always recorded as `completion - intended`—including backlog wait and any retries—while `TenantSnapshot.LatencyFromSend` records `completion - lastSend` as the coordinated-omission control.

Running the generator tests shows the difference directly on a single client sending every 10 ms into a 200 ms stall:

```sh
$ go test -v -count=1 ./internal/sim/workload -run '^(TestLatencyIsMeasuredFromIntendedSendTime|TestZipfSharesMatchTheCalculation|TestPoissonInterArrivalMean)$'
=== RUN   TestLatencyIsMeasuredFromIntendedSendTime
    generator_test.go:83: requests slower than 50ms: 17 measured from the intended send time, 1 measured from the actual send
--- PASS: TestLatencyIsMeasuredFromIntendedSendTime (0.00s)
=== RUN   TestZipfSharesMatchTheCalculation
    generator_test.go:123: s=1.2, N=100000, 200000 draws: top 1% share 0.8526 (exact 0.8516), hottest key 0.1951 (exact 0.1964)
--- PASS: TestZipfSharesMatchTheCalculation (0.03s)
=== RUN   TestPoissonInterArrivalMean
    generator_test.go:161: Poisson: mean gap 9.811957ms (want 10ms), 3215 of 4999 gaps below the mean
--- PASS: TestPoissonInterArrivalMean (0.14s)
```

With retries enabled (`Tenant.Retry`, off by default), a failed request schedules an exponential backoff event (`Backoff * 2^(attempt-1)`, capped at `Cap`). It keeps its original `intended` timestamp and original statement slice (`r.orig`, even if a failed transaction ran `ROLLBACK`), and it counts toward `Tenant.Backlog` while waiting in backoff or still eligible to retry in flight:

```go
queued := len(c.backlog) + c.waiting
if c.cur != nil && c.cur.attempts < t.Retry.Max {
	queued++
}
if queued >= t.Backlog {
	st.dropped++
	c.w.s.Trace(kindDrop, int64(c.id), r.id, 0, 0)
}
```

### Client faults and conservation laws

[`internal/sim/workload/fault.go`](../../internal/sim/workload/fault.go) registers six client fault kinds (`96..101`, targeted by 1-based client ID):

- `wl.half-message` (96): writes half a `Query` frame, stalls for 1 s, then disconnects.
- `wl.stop-reading` (97): stops reading replies (for a duration or until cleared), filling the backend's send window.
- `wl.disconnect` (98): drops via FIN or RST immediately (`DisconnectNow`), right after sending (`DisconnectAfterSend`), or inside an open transaction (`DisconnectInTx`, also configurable via `Tenant.Disconnect` for Chunk 11's coalesced-waiter disconnect tests).
- `wl.burst` (99): drops `Arg1` clients starting at `Target` and reconnects each at a uniform time inside `Arg2`.
- `wl.cancel` (100): opens a second connection and sends `CancelRequest` (with the real secret, or a bit-flipped wrong secret when `Arg1 != 0`).
- `wl.clear` (101): clears armed client faults (`stop-reading`, `half-message`, `disconnect`).

Every tenant (and the merged total) enforces two registered conservation laws after every simulator step:

```text
intended = sent + not yet sent + dropped + failed before send
sent     = ok + error + lost + timed out + client left + in flight
```

`fakepg` enforces four more conservation laws alongside `sessions <= max_connections`: `accepted = open + closed`; `statements started = finished + in service + waiting + aborted`; `transactions begun = committed + rolled back + lost + open`; and `cancels received = applied + idle + no match`. Both `Snapshot()` types are plain value structs verified by `obstest.RequirePlain`.

---

## Direct baselines and running `mgsim`

[`internal/sim/world/baselines.go`](../../internal/sim/world/baselines.go) registers five direct-to-database scenarios alongside Chunk 1's 11 toy scenarios in `cmd/mgsim`:

```sh
$ go run ./cmd/mgsim list
echo-clean
...
echo-silence
direct-500-connections
direct-reconnect-burst
direct-1000-identical-reads
direct-open-loop-overload
direct-mixed-transactions
```

Running `direct-reconnect-burst` with seed 7 prints both SHA-256 digests, the counter and latency table, and the `REPLAY:` line:

```sh
$ go run ./cmd/mgsim run direct-reconnect-burst -seed 7
scenario=direct-reconnect-burst seed=7 steps=40108 status=pass
trace-digest: a21bf6d58dc11aef1c7e1c691ef2c27210488231c10cf6b0da58c374dc7adbef
state-digest: 564dded6e5cf6829bf5eb8b374dfa0d53844782697be65ac97551485620c51a1
db.connections.accepted     730
db.connections.peak         118
db.sessions.peak            100
db.refused.too_many         517
db.statements               2870
db.reads                    2870
db.writes                   0
clients.intended            3000
clients.sent                2870
clients.ok                  2870
clients.errors              0
clients.failed_before_send  130
clients.lost                0
clients.timed_out           0
clients.retries             0
clients.reconnects          100
latency.p50_ns              1269759
latency.p99_ns              1802239
latency.max_ns              1844886
REPLAY: go run ./cmd/mgsim run direct-reconnect-burst -seed 7
```

Here are the control-arm numbers observed across all five baselines on seed 1 (and seed 7 for the reconnect burst above):

| Scenario | Setup | Measured result (no proxy) |
| --- | --- | --- |
| `direct-500-connections` | 500 clients, 5 reads each, held 200 ms; `max_connections = 1000` | Seed 1: `db.sessions.peak = 500`, `db.connections.peak = 500`, `db.reads = 2,500`, `clients.ok = 2,500`; p50 `2.77 ms`, p99 `3.26 ms`. |
| `direct-reconnect-burst` | `max_connections = 100`; 100 steady + 400 idle clients drop and reconnect within 50 ms | Seed 7: `db.sessions.peak = 100`, `db.refused.too_many = 517`, `clients.failed_before_send = 130`, `clients.ok = 2,870` (517–598 refusals across pinned seeds 1, 2, 3, 7, 42; refusals exceed 400 because steady clients refused during the burst re-dial on later requests). |
| `direct-1000-identical-reads` | 1,000 clients send the same hot-key `SELECT` at `t = 20 ms`; 50 ms service | Seed 1: `db.reads = 1,000`, `clients.ok = 1,000`; p50 = p99 `50.86 ms`. |
| `direct-open-loop-overload` | Capacity 4 × 10 ms (400 req/s); 40 clients × 20 requests every 50 ms (800 req/s) | Seed 1: `intended = sent = ok = 800`; p50 `507.5 ms`, p99 `998.2 ms`, max `1,005.8 ms`; mean latency per completion quarter grows linearly: `132.9`, `382.9`, `632.9`, `882.9 ms`. |
| `direct-mixed-transactions` | 3 tenants × 10 clients × 10 Poisson requests (reads, writes, 3-stmt tx), Zipf `s = 1.2` over 50 keys, capacity 8 | Seed 1: `db.writes = 316`, `db.statements = 808`, `clients.ok = 300`; exact write log and transaction isolation checks pass. |

---

## Fidelity against real Postgres 17 and test coverage

Chunk 2 adds 6,347 lines of product code (excluding Chunk 1's `cmd/mgsim/main.go`) and 3,836 lines of tests across 106 test and fuzz functions in its five new packages. Testing spans five layers:

1. **Real-Postgres fidelity and tier R (`scripts/tier-r.sh`).** [`world.ProbeScript`](../../internal/sim/world/fidelity.go#L63-L89) replays the Phase 1 probe's ten simple-query exchanges (B0–B10, trust auth) against both `SimTarget` and a throwaway `postgres:17` Docker container over TCP, normalizing out values that legitimately differ (PIDs, secrets, version string, timings). On Postgres 17.11, both transcripts match line for line across all 19 output lines. That match runs from startup (`R:0 S*14 K Z:I` and all 14 sorted parameter names) through 200,000 rows (`T1 D*200000 C:SELECT_200000 Z:I`), statement-start snapshot (`B10: T2 D[1,t]` during a concurrent `UPDATE`), and backend termination (`B9: T1 E:FATAL:57P01 EOF`). The real transcript is committed at [`internal/sim/world/testdata/fidelity/postgres17.txt`](../../internal/sim/world/testdata/fidelity/postgres17.txt), so CI checks `TestFidelityAgainstRecordedPostgres17` without Docker.

   [`scripts/tier-r.sh`](../../scripts/tier-r.sh) starts two loopback-bound `postgres:17` containers: one with `max_connections=600` and `pg_stat_statements`, and one with `max_connections=100` and `superuser_reserved_connections=0`. It runs four live tests under `MG_TIER_R=1` and removes both containers on exit. In the gate run against Postgres 17.11:
   - `TestTierRFidelity`: live transcript identical to `fakepg`.
   - `TestTierRManyConnections`: 500 real-socket clients held 500 client backends in `pg_stat_activity`.
   - `TestTierRIdenticalReads`: 100 connections × 10 reads caused 1,000 executions in `pg_stat_statements`.
   - `TestTierRReconnectBurst`: 500 clients connecting within 50 ms against `max_connections=100` yielded 100 accepted and 400 refused with `FATAL 53300` ("sorry, too many clients already").

2. **Every fault kind, plus self-testing clear paths.** `TestEachDatabaseFault` runs 34 subtests covering every non-TLS database fault kind. Each subtest asserts the client-visible sequence, counter increment, trace record, post-clear absence of the fault's specific effect, and a clean `SELECT 1`. `TestEveryFaultKindHasACase` fails if a new kind is untested, and `TestEveryClearPathIsChecked` disables each of the 18 clearable kinds' clear paths in turn (via test-only `PlantSkipClear`) and fails if its test still passes.
3. **Planted-bug canaries.** Five test-only switches in `fakepg/export_test.go` verify that tests actually detect corruption. `PlantDoubleApply`, `PlantDropWrite`, and `PlantPhantomWritesFrom` must each fail `direct-mixed-transactions` via `CheckWriteLog`. `PlantIdleInTx` must fail the 18-case `TestTransactionStatusTable`, and `PlantSkipClear` must fail every clearable fault subtest.
4. **Property tests, fuzzers, and sweeps.** `TestScenarioRandomFaultSchedules` decodes random fault schedules via `sim.ScheduleFromBytes`, clears them at 400 ms, and proves no panics, clean quiesce, conservation, and exact replay. Five new fuzz targets (`FuzzFramer`, `FuzzMatch`, `FuzzServerInput`, `FuzzWorldSchedule`, `FuzzSession`) ran for 20 s each in the gate alongside Chunk 1's two targets. During initial gating, `FuzzServerInput` caught a real bug where a client requested a multi-terabyte `generate_series` and closed, which prompted the `Config.MaxReplyBytes` bound. The gate sweep ran 200 seeds per scenario (3,400 seed runs total across toy + world + property scenarios), and `extra.sh` confirmed identical cross-process digests across 10 scenario runs.
5. **Import boundary proof.** `TestPgproto3IsImportedOnlyWhereTheRulesAllowInEveryGoFileOutsideTools` parses every `.go` file outside `tools/` (tests, nested modules, and `testdata` included) and confirms `pgproto3` appears only in `fakepg/proto.go`, `testclient/msg.go`, and `testclient/session_test.go`.

---

## Stated limits

- **Protocol subset.** Simple-query protocol with trust authentication only; no extended protocol (`Parse`/`Bind`/`Execute`), no `COPY`, no SCRAM-SHA-256, and no TLS (SCRAM and TLS enter in Chunk 4).
- **Table and concurrency semantics.** One `kv(k text primary key, v text)` table with no row locks: concurrent writers to the same key both commit (last commit wins). An `UPDATE` committed after another session deleted its key re-creates the row and counts as an insert under `peakRowsAfter`.
- **Settings and error fields.** `SET` validates parameter names, not values; `RESET ALL` and `DISCARD ALL` drop custom dot-qualified settings. Error responses carry Postgres's real severity, SQLSTATE code, and message text for the supported cases, without secondary fields (detail, position, routine).
- **Float determinism scope.** `internal/randdist` uses `math/rand/v2` floating-point distributions for Poisson gaps and Zipf keys; bit-identical replay is scoped to the same commit, Go toolchain, and `linux/amd64`.
- **FIN detection under `simnet`.** `simnet` does not return `EPIPE`/`ECONNRESET` when a peer closes cleanly with FIN. A busy backend therefore notices a client FIN only after its current statement finishes—matching Postgres without `client_connection_check_interval`—and finishes streaming any in-progress reply up to `Config.MaxReplyBytes` (default 64 MiB).
- **Write-log replay visibility.** Replaying `WriteLog` compares only the final table, so a hypothetical phantom write overwritten later on the same key would be masked in table replay (though the statement-to-write bijection still catches missing or extra committed writes). Exact checks refuse logs that dropped entries.
- **Tier R scope.** In Chunk 2, `scripts/tier-r.sh` drives its three baseline tests directly with goroutines and `internal/testclient` rather than through a real-Postgres `world.Driver`, which waits for the proxy binary in Chunk 6. It also runs 100 connections × 10 identical reads rather than 1,000 connections to stay within the 7.7 GB VM's memory budget.
- **Code size.** Product code reached 6,347 lines (excluding Chunk 1's `cmd/mgsim/main.go`), above the plan's ~1,800 × 2 budget, driven by the 24-fault database catalog and the full client actor state machine.
