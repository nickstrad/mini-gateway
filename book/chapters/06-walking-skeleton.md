# 6. The first working proxy, and lending connections by the transaction

Chapters 1 through 5 built the simulator, the fake Postgres 17 server, the observability spine, the wire-protocol state machines, and the statement classifier. None of them forwarded a query from a client to a database or compiled into a proxy binary.

Chunk 6 integrates those pieces into the first runnable proxy—a **walking skeleton** that carries real traffic end to end through every architectural seam while leaving advanced policies as pass-throughs. A client connects over TCP or TLS (Transport Layer Security), authenticates with SCRAM-SHA-256 (Postgres's challenge-response password login), runs statements and multi-statement transactions, and receives answers identical to talking directly to Postgres 17.

What this chunk builds:

- [`internal/proxy`](../../internal/proxy): a single-threaded, **sans-I/O** state machine (**decision D1**)—an engine that takes explicit timestamps and byte or lifecycle events and returns actions, without reading clocks, starting goroutines, or touching sockets. It implements the transaction-lease lifecycle, unconditional `DISCARD ALL` reset after every lease, bounded streaming reply relay, close-on-doubt dirty handling, the error contract, and all stage seams.
- [`internal/proxy/proxysim`](../../internal/proxy/proxysim): the simulator driver that runs the exact same `proxy.Engine` over `simnet` and `simtls` against `fakepg` (tier S).
- [`internal/shell`](../../internal/shell): the real-socket goroutine shell — listeners, per-connection reader and writer goroutines, one bounded mailbox, TLS termination on both legs, socket deadlines, CSV flushing, and an isolated admin HTTP listener (`/healthz`, `/stats`, `/metrics`).
- [`internal/config`](../../internal/config) and [`cmd/mini-gateway`](../../cmd/mini-gateway): JSON configuration loading with fail-closed validation, and the `mini-gateway` binary (`-config`, `-check`, `-verifier`).
- [`internal/obs/enginesrc`](../../internal/obs/enginesrc): component `engine` observability export (84 engine and shell metrics) plus the `database` health rule. Four documents come with it: the code-generated [`docs/timeout-budget.md`](../../docs/timeout-budget.md) and [`docs/capacity-table.md`](../../docs/capacity-table.md), the regenerated [`docs/csv-schema.md`](../../docs/csv-schema.md), and [`docs/extension-seams.md`](../../docs/extension-seams.md).
- [`test/e2e`](../../test/e2e): 21 tier S scenarios, 14 tier R tests against the compiled binary and Postgres 17.11 in Docker (with client TLS on), DuckDB SQL assertions for both tiers, `make chaos`, and the engine operation fuzz target `FuzzEngineOps`.

**What is not built yet.** This proxy is deliberately plain: it holds a small fixed set of database connections (`FixedSet`, opened at start and re-opened one dial at a time on loss) behind one bounded first-in, first-out queue (`FIFO`). Seven policy stages are **not built** yet:

- **The pool** (dynamic sizing, creation rate, active cancel/rollback/reuse, `on-switch` reset, `lease.reseed`, the 5 s idle-in-transaction rule, and the circuit breaker — Chunk 7)
- **Admission control** (CoDel and adaptive LIFO — Chunk 8)
- **Fairness** (per-tenant queues — Chunk 9)
- **The adaptive concurrency limit and rate gate** (Chunk 10)
- **Shared reads / coalescing** (Chunk 11)
- **Retries** (Chunk 12)
- **Multi-instance fleet division and graceful drain** (Chunk 13)

Each sits behind an interface in [`internal/proxy/seams.go`](../../internal/proxy/seams.go) backed by a pass-through (`ClosedBreaker`, `OpenRate`, `NoCoalesce`, `RetryOff`, plus a reserved `EvDraining` input) so later chunks replace one stage without re-plumbing the engine.

---

## Architecture: one engine, two drivers, ten stage seams

The central design choice is **decision D1**: keep every decision about queues, leases, resets, back pressure, error codes, and deadlines inside a single-threaded state machine (`proxy.Engine`) that never performs I/O.

```text
  Tier S (simulator)                         Tier R (real binary)
  simnet + simtls                            TCP + crypto/tls + goroutines
  internal/proxy/proxysim                    internal/shell (bounded mailbox)
             \                               /
              \   Event + Instant           /
               ▼                           ▼
          ┌────────────────────────────────────┐
          │       proxy.Engine (sans-I/O)      │
          │  classify → reject → draining →    │
          │  breaker → rate → coalesce →       │
          │  queue → lease → forward → release │
          └────────────────────────────────────┘
               │                           │
               ▼   []Action                ▼
         simnet writes,              socket writes,
         sim.Sim wake timer          time.Timer, admin pub
```

**Alternatives turned down.** Putting locking and socket I/O directly inside the pool and request handlers (the usual Go server shape) would split the decision code from the simulator, breaking the replay law: a bug in concurrency ordering or deadline ties could no longer be replayed deterministically from a seed. Instead, `internal/proxy`, `internal/proxy/proxysim`, and `internal/obs/enginesrc` are on `tools/lint-rules/det-packages.txt`, so the determinism lint rejects goroutines, locks, wall-clock calls, and floating point at build time. The cost is documented in [PLAN.md](../../PLAN.md#3-design-decisions-this-plan-makes): one engine goroutine uses one CPU core for decisions; scaling past one core means running more instances (Chunk 13).

To keep the driver contract small, the engine manages all per-client and per-database deadlines on its own min-heap ([`internal/proxy/timers.go`](../../internal/proxy/timers.go)) and emits a single `ActWake{At}` action whenever the earliest deadline changes (`At == 0` cancels it). Every wait nests inside its enclosing wait, checked at config load ([`docs/timeout-budget.md`](../../docs/timeout-budget.md)): `database_connect` (2 s), `reset` (1 s), and `redial_delay` (100 ms) must end before `queue_wait` (5 s), and every active wait must end before `client_idle` (10 m).

Every client request outside a transaction traverses the fixed fail-fast pipeline of [PLAN.md section 10.1](../../PLAN.md#101-the-request-path) (seams documented in [`docs/extension-seams.md`](../../docs/extension-seams.md)):

| Stage | Seam in `internal/proxy/seams.go` | Chunk 6 behaviour | Refusal row |
| --- | --- | --- | --- |
| Classify | `Classifier` | `*pgwire.Classifier` (Chunk 5) | — |
| Reject list | `unit.Class == work.ClassReject` | Courtesy reject list + extended discard-until-`Sync` | `0A000` |
| Draining | `EvDraining{On}` input | Reserved, unit-tested; closes idle client after reply | `53M05` |
| Breaker | `Breaker` | `ClosedBreaker` (pass-through) | `53M03` |
| Rate gate | `RateGate` | `OpenRate` (pass-through) | `53M02` |
| Coalesce join | `Coalescer` | `NoCoalesce` (pass-through) | `53M04` |
| Queue | `Queue` | One bounded `FIFO` (tenant-keyed API), `queue_wait` 5 s | `53M01` |
| Lease | `Pool` | `FixedSet` of N database connections, 1 dial at a time | — |
| Forward | — | Writes `Query`, arms `timeouts.statement` (30 s) | `08006` / `08007` / close |
| Release | `ReleasePolicy` | `ResetAlwaysPolicy`: `DISCARD ALL` on `'I'`, else close | counted |
| Retry | `RetryPolicy` | `RetryOff` (pass-through; receives `Reached` fact) | — |

---

## Transaction leases, streaming back pressure, and the unconditional reset

### Lending by the transaction (decisions D3, D4, D5)

A client outside a transaction (`status == 'I'`) enters the queue only once; there is no second hidden queue inside the pool (**D3**). When a database connection is lent, the engine forwards the client's `Query` and relays Postgres's response frames as they arrive without buffering the whole reply.

Only two backend message types are held whole (bounded by `MaxMessage + 5` bytes): `ErrorResponse` and `ReadyForQuery`. Holding `ErrorResponse` lets the engine intercept a Postgres `FATAL` or `PANIC` and translate it into the error contract (`08006`/`08007` if nothing was relayed, or an immediate close if part of the reply was already sent). Holding `ReadyForQuery` lets the engine inspect Postgres's transaction-status byte before ending the turn:

- Status `'T'` (in transaction) or `'E'` (failed transaction): the client keeps its held database connection (`cpHeld`). Its next statements bypass draining, gates, and the queue and go straight to that connection (**D4**).
- Status `'I'` (idle): the lease ends and passes to `ReleasePolicy.AtRelease`. No classifier flag participates in this decision; Postgres's status byte alone decides.

To keep memory bounded (**D5**), the engine pauses a client (`ActClientPause`) as soon as a request starts and keeps it paused until its entire reply has been written and acknowledged (`c.out == 0`). Unprocessed client bytes already in memory are capped at `4 × MaxRead` (64 KiB), and exceeding that cap closes the client. On the reply side, when a client's unwritten bytes exceed `limits.reply_buffer − MaxRead`, the engine emits `ActDBPause` and resumes reading once the buffer drains to half. If the client stays stalled past `timeouts.client_write` (5 s), the engine drops the client and destroys its lent database connection.

### The reset after every lease (decision D8)

Following Nick's Ruling 9 (Chapter 5), session safety never depends on reading SQL. Whenever a lease ends with status `'I'` and zero unread bytes, `ResetAlwaysPolicy` returns `ReleaseReset` and `startReset` sends `DISCARD ALL` as the proxy's own `Query` message. The connection stays in phase `dpResetting` (counted in use, never lendable) under `timeouts.reset` (1 s).

A dedicated framer (`resetJudge`, capped at 4,096 bytes) in [`internal/proxy/lease.go`](../../internal/proxy/lease.go) verifies the reply:

```go
		f := j.fr.Frame()
		switch f.Type {
		case pgwire.MsgParameterStatus, pgwire.MsgNoticeResponse:
		case pgwire.MsgErrorResponse:
			return n, rvFail, ResetFailError
		case pgwire.MsgCommandComplete:
			if j.sawTag || string(f.Body) != discardAll+"\x00" {
				return n, rvFail, ResetFailWrongTag
			}
			j.sawTag = true
		case pgwire.MsgReadyForQuery:
			if !j.sawTag {
				return n, rvFail, ResetFailWrongTag
			}
			if len(f.Body) != 1 || f.Body[0] != pgwire.StatusIdle {
				return n, rvFail, ResetFailWrongStatus
			}
			return n, rvDone, 0
		default:
			return n, rvFail, ResetFailSurprise
		}
```

The connection returns to idle **only** after seeing exactly one `CommandComplete` carrying the tag `DISCARD ALL` followed by `ReadyForQuery 'I'`, with zero trailing bytes in the same read. Any error, wrong or duplicate tag, status other than `'I'`, unexpected message, connection drop, or deadline closes the connection (`resets_failed{reason}`) and never lends it. Nothing from a reset reply is ever written to a client, and a connection closed at its reset deadline is deleted from `e.dbs`, so a late reply is ignored.

In config, `lease.reset` accepts only `always` (Chunk 7 adds `on-switch` and `lease.reseed` on the same `ReleasePolicy` seam). There is no config setting to turn the reset off; `TestNoConfigOrCommandFileMentionsThePlantSwitch` enforces that the test-only `proxy.PlantNoReset` switch is unreachable from `internal/config` and `cmd/mini-gateway`.

### Ablation and canary evidence

Running the tier S canaries, the no-reset ablation, and the shrunk fuzz regression directly produces:

```sh
$ go test -count=1 -v -run '^(TestCanary|TestAblation|TestShrunk|TestResultsThroughTheProxyEqualDirectOnEveryPinnedSeed)' ./test/e2e
=== RUN   TestCanaryReleaseOnAnyReadyBreaksTransactionIntegrity
    canary_test.go:23: e2e-tx-integrity fails as it must: e2e: check failed: 132 statements ran inside another client's transaction
--- PASS: TestCanaryReleaseOnAnyReadyBreaksTransactionIntegrity (0.15s)
=== RUN   TestAblationWithoutResetSessionStateCrosses
    canary_test.go:31: e2e-session-state fails as it must: e2e: check failed: session state crossed clients 138 times
--- PASS: TestAblationWithoutResetSessionStateCrosses (0.03s)
=== RUN   TestCanaryLendBeforeResetReplyIsCaught
--- PASS: TestCanaryLendBeforeResetReplyIsCaught (0.06s)
=== RUN   TestResultsThroughTheProxyEqualDirectOnEveryPinnedSeed
--- PASS: TestResultsThroughTheProxyEqualDirectOnEveryPinnedSeed (0.24s)
=== RUN   TestShrunkQueuedVanishScenarioReplays
    shrunk_test.go:49: with the bug: sim: invariant proxy.engine: queue holds 1 entries and 1 waiting requests for 0 queued clients: seed 1, step 44, time 4000000
        REPLAY: go run ./cmd/mgsim run fuzz-engine-ops -seed 1
--- PASS: TestShrunkQueuedVanishScenarioReplays (0.01s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	0.500s
```

The same ablation holds on real Postgres 17.11 (tier R, recorded in [`NOTES.md`](../../agent_work/chunks/c06-walking-skeleton/NOTES.md) and the gate's tier R log (`G6-tier-r.log`, kept on the build machine, not in the repository)):

| Environment | Workload | With `DISCARD ALL` reset | Without reset (`PlantNoReset`) |
| --- | --- | --- | --- |
| Tier S (`e2e-session-state`, courtesy bypassed) | 6 hostile clients running `SET`/`RESET`, 6 observers running `SHOW`, 2 connections | **0 crossings** (`resets_completed == leases_ended`) | **138 crossings** |
| Tier R (`TestTierRSessionStateNeverCrossesClients`, real Postgres 17.11, courtesy on) | Eight PL/pgSQL functions hiding `SET`, `CREATE TEMP TABLE`, `pg_advisory_lock`, `PREPARE`, `LISTEN`, a held cursor, a role change, and a custom setting carrying a per-lease secret; checked via all seven columns of `test/oracles/dirty-check.sql` (`prepared`, `cursors`, `listens`, `adv_locks`, `session_gucs`, `temp_rels`, `role_changed`), `SHOW search_path`, and a read-back of `mg_hostile.secret` | **0 dirty observations** over 24 hostile leases | **69 dirty observations** (in-process shell against real Postgres 17; all seven columns and the secret read-back move) |

---

## The goroutine shell, TLS on both legs, and the two bugs only tier R caught

[`internal/shell`](../../internal/shell) bridges `proxy.Engine` to real TCP and `crypto/tls` sockets:

- **One engine goroutine** owns `Core`, reads from a single bounded mailbox (`mailboxCap = 4096`), drives a single `time.Timer` for `ActWake`, and ticks `EvPublish` at `obs.publish_interval`.
- **Two goroutines per live connection** (`readLoop` and `writeLoop`) perform blocking socket I/O with explicit deadlines and bounded write queues (64 MiB + 2 MiB slack, 65,536 items).
- **Exact pre-startup reads for TLS (decision D13).** On a newly accepted client, `readLoop` calls `io.ReadFull(nc, buf[:8])`—exactly `FirstClientRead` (8) bytes, unbuffered—and waits (`readWait`) until the engine answers `ActClientResume` or `ActClientStartTLS`. On a TLS-enabled database connection (`backend_tls.mode` of `require` or `verify-full`), it reads exactly 1 byte (`FirstDBTLSRead`) for Postgres's `'S'`/`'N'` answer. Nothing is ever buffered across the TLS upgrade. Plaintext bytes smuggled behind `SSLRequest` hit `crypto/tls` as a bad record header and close the connection (`TestSmuggledPlaintextAfterTheTLSRequestClosesTheConnection`).
- **Isolated admin listener (decision D12).** `/healthz`, `/stats` (JSON), and `/metrics` (Prometheus text) read exclusively from an `atomic.Pointer[published]` copy updated on publish ticks. The health endpoint reports three states: `ok`, `degraded` (when no database connection is usable and dials fail), and `failed` (only when the client listener stops). `TestAdminAnswersWhileTheEngineIsBlocked` proves all three endpoints answer while the engine goroutine is deliberately blocked.
- **Clean teardown.** `TestTeardownLeavesNoGoroutine` exercises 8 teardown paths under `testing/synctest` and `-race` with zero leaked goroutines.

### Two shell bugs that only real-server tests caught

During the initial build, two real defects passed every unit and tier S test and surfaced only against real Postgres 17 in tier R (fixed in commit `a5aa0e7`):

1. **Missing per-connection read credit (`conn.inflight`).** Without holding a one-chunk credit on each connection until `Engine.Step` finished processing the previous `EvDBData`, a fast Postgres reader goroutine could keep pumping 16 KiB chunks into the 4,096-entry mailbox before the engine's `ActDBPause` took effect. In `TestTierRSlowReader`, the per-client relay buffer climbed to **1.4 MB against a 64 KiB cap**. Adding `c.setInflight(true)` before posting data and `defer env.conn.setInflight(false)` in `deliver` bounds every connection to one unprocessed read at a time.
2. **Final CSV flush before `Engine.Shutdown`.** On `SIGTERM`, `engineExit` flushed the final CSV snapshot before calling `Engine.Shutdown`, leaving `requests_in_flight` or `connections_resetting` non-zero in the final flush and failing tier R SQL conservation checks. Calling `Shutdown` before the final `publish()` guarantees a clean post-stop snapshot.

Review 1's mutation checks (M1 and M2 in [`REVIEW-1.md`](../../agent_work/chunks/c06-walking-skeleton/REVIEW-1.md)) proved that reverting either fix still left `go test ./internal/shell/...` green because no fast non-Docker test exercised them. The repair round added `TestReaderPostsNoSecondReadBeforeTheEngineTakesTheFirst` and `TestCloseShutsTheEngineDownBeforeTheFinalFlush` (`internal/shell/credit_test.go`, `shutdown_test.go`), both verified to catch the reverted code in Review 2.

---

## Configuration, the binary, and building it

[`internal/config`](../../internal/config) reads JSON using the standard library with a strict token-walking pass (`decode.go`) that rejects unknown keys, wrong-case keys, and duplicate keys with full dotted paths. It then checks every cross-field bound and timeout nesting constraint, failing closed with an error naming the offending key (`TestValidationFailsClosedNamingTheKey` checks 62 invalid cases). All tenant SCRAM verifiers must share one iteration count and one salt length (closing Chunk 4's carried item so unknown-user mock challenges match the table's shape).

Building and checking [`cmd/mini-gateway`](../../cmd/mini-gateway/main.go):

```sh
$ go build -o /tmp/mini-gateway ./cmd/mini-gateway
$ /tmp/mini-gateway -help
mini-gateway is a PostgreSQL gateway: it holds a fixed set of database connections and
lends one to a client for each transaction.

Usage:
  mini-gateway -config <file>                 run until SIGTERM or SIGINT
  mini-gateway -config <file> -check          validate the config, print its fingerprint
  mini-gateway -verifier -password <pw|->     print a SCRAM-SHA-256 verifier for the tenant table
               [-iterations N]                ("-" reads the password from the first line of stdin)

Exit status: 0 clean stop, 1 runtime failure, 2 usage or configuration error.

$ go run ./cmd/mini-gateway -verifier -password example-pw
SCRAM-SHA-256$4096:kBVgjCXKmq0dahAkSpUsng==$KQiSGidlWeKbJc+5I4xQZEDv+cJsHhXuzRyOR2zQzoA=:9Ny0JVBKn12YEBNDhjTRHXszs5VkyTQpigzkKXN3K8E=
```

Running `-check` on [`internal/config/testdata/example.json`](../../internal/config/testdata/example.json) without its TLS files demonstrates fail-closed validation. Switching `client_tls.mode` to `"off"` (or supplying readable `server.crt` and `server.key`) passes validation and prints the 16-hex-character config fingerprint—the SHA-256 of the canonical effective config with `database.password` blanked:

```sh
$ go run ./cmd/mini-gateway -config internal/config/testdata/example.json -check
mini-gateway: internal/config/testdata/example.json: config: invalid: client_tls.cert_file: open internal/config/testdata/server.crt: no such file or directory
config: invalid: client_tls.key_file: open internal/config/testdata/server.key: no such file or directory
exit status 2

$ /tmp/mini-gateway -config /tmp/mg-example-off.json -check
config /tmp/mg-example-off.json is valid; fingerprint 4a827a9f43a1174e
```

---

## Testing, operation fuzzing, and `make chaos`

### Two tiers from one harness

[`test/e2e`](../../test/e2e) runs 21 named tier S scenarios: `e2e-results`, `e2e-fragment`, `e2e-tx-integrity`, `e2e-reject-list`, `e2e-session-state`, `e2e-client-vanish`, five `e2e-kill-*` database-loss points, `e2e-slow-reader`, `e2e-queue-full`, `e2e-silent-db-startup`, `e2e-silent-db-query`, and six `e2e-reset-*` reset-reply tamper scenarios (injected by [`tamper.go`](../../test/e2e/tamper.go) so the frozen `fakepg` stays untouched). Every seed runs twice to verify bit-identical trace/state digests and byte-identical CSV files, checks the CSV round trip, and runs DuckDB SQL conservation and fault-count assertions under `test/analysis/e2e-*/`.

`make chaos` (now live — its not-built marker was deleted in this chunk) runs `scripts/chaos.sh` concisely, one line per scenario:

```sh
$ CHAOS_SEEDS=3 CHAOS_TIER_R=0 RUN_DIR=/tmp/c06-prose-chaos scripts/chaos.sh
S  e2e-client-vanish            seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-client-vanish
S  e2e-fragment                 seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-fragment
S  e2e-kill-after-reply         seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-kill-after-reply
S  e2e-kill-before-read         seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-kill-before-read
S  e2e-kill-before-write        seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-kill-before-write
S  e2e-kill-during-reply        seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-kill-during-reply
S  e2e-kill-mid-transaction     seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-kill-mid-transaction
S  e2e-queue-full               seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-queue-full
S  e2e-reject-list              seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reject-list
S  e2e-reset-drop               seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reset-drop
S  e2e-reset-error              seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reset-error
S  e2e-reset-late               seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reset-late
S  e2e-reset-silent             seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reset-silent
S  e2e-reset-status-t           seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reset-status-t
S  e2e-reset-wrong-tag          seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-reset-wrong-tag
S  e2e-results                  seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-results
S  e2e-session-state            seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-session-state
S  e2e-silent-db-query          seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-silent-db-query
S  e2e-silent-db-startup        seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-silent-db-startup
S  e2e-slow-reader              seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-slow-reader
S  e2e-tx-integrity             seeds 3    pass      /tmp/c06-prose-chaos/sweep/e2e-tx-integrity
R  (skipped: no Docker)
chaos: all green; runs under /tmp/c06-prose-chaos
```

Tier R runs 14 tests in [`test/e2e/tierr_test.go`](../../test/e2e/tierr_test.go) against Postgres 17.11 with `ssl=on`, SCRAM-SHA-256, and an ephemeral test CA, keeping client TLS enabled throughout. The suite covers `psql` (`sslmode=require`, `verify-full`, and `disable` refused with `28000`), `pgbench -S -M simple` (800/800 transactions in the gate's tier R log (`G6-tier-r.log`, kept on the build machine, not in the repository)), `pg_stat_ssl` checks of `backend_tls.mode` (`disable`, `require`, `verify-full`, and wrong-CA failure), and DuckDB SQL assertions over the binary's flushed CSV files (`test/analysis/tierr-*/`).

### Engine operation fuzzing (`FuzzEngineOps`)

[`FuzzEngineOps`](../../test/e2e/fuzz_test.go) follows [PLAN.md section 6.6](../../PLAN.md#66-rules-for-simulation-level-fuzz-targets-nicks-dst-fuzz-ruling-2026-11-24). The simulator seed is the fixed constant `OpSeed = 1` (never a hash of the fuzz bytes), and decoding is total and local (4 bytes per operation, capped at `MaxOps = 48`). The simulated world stays small—3 clients, 1 database connection, queue capacity 2, and compressed deadlines—so queueing and contention happen frequently and execution stays above the 100/s rate floor (140/s in the gate's `G5-fuzz.tsv`, 2,947 executions in 20 s).

To prove the fuzzer finds real ordering bugs, `scripts/fuzz-plant.sh` enables `PlantQueuedVanishKeepsSlot` (a queued client closing leaves its queue slot occupied) and runs with an empty fuzz cache:

```sh
$ scripts/fuzz-plant.sh
NOTE: FuzzEngineOps found the planted queue-slot bug after 9s (budget 20s)
```

Minimised with CR-1's shrinking helpers and committed as [`test/e2e/testdata/shrunk/queued-vanish.faults`](../../test/e2e/testdata/shrunk/queued-vanish.faults), the 4-operation reproducer replays in 10 ms as a plain test:

```text
faults 4
at=0ns kind=slow-read target=0 arg1=0 arg2=0
at=0ns kind=disconnect target=0 arg1=0 arg2=0
at=0ns kind=slow-read target=0 arg1=0 arg2=0
at=0ns kind=vanish target=0 arg1=0 arg2=0
```

Unplanted fuzzing found one real harness bug (the fuzz client reported a legitimate proxy `FATAL 08006` close as an undecodable reply; fixed and retained in `test/e2e/testdata/fuzz/FuzzEngineOps/`) and zero product bugs.

---

## Narrowed claim and stated limits

### Narrowed Claim 13

> Config validation fails closed on every invalid setting it documents. A cap (`pool.connections`, `queue.capacity`, `limits.max_clients`, `limits.max_startup`, `limits.reply_buffer`) that is zero or omitted takes its default — zero is not an invalid setting; a negative cap, or one outside its documented range, is refused with an error naming the key.

Durations follow the same rule (zero or omitted takes the default; explicit `"0s"` or negative durations fail JSON decoding). [`docs/capacity-table.md`](../../docs/capacity-table.md) states "zero means the default" for each configurable cap, and `TestZeroCapMeansTheDefaultAndANegativeCapIsRefused` holds both halves per key.

### Stated limits

1. **`DISCARD ALL` residue (decision D8):** four things survive Postgres's `DISCARD ALL`: the PRNG random seed (Chunk 7's `lease.reseed` closes this), the *existence* of a custom placeholder setting name (its value is cleared), in-memory state of loaded extensions and procedural languages (PL/Perl, PL/Python), and privileged replication-origin sessions.
2. **Safe-shape limits carry into `08006` vs `08007`:** choosing `08006` (`lost-after-read`, retry safe) over `08007` (`lost-after-write`) rests on Chunk 5's `SafeRead`. A view, user-defined operator, shadowed function, or table with a row-level security policy can hide a write behind a statement text that fits the safe shape. Databases with such objects should treat `08006` like `08007` (stated in [`docs/error-contract.md`](../../docs/error-contract.md)).
3. **Graceful client `FIN` while paused:** a client that sends TCP `FIN` while its request is queued or running is noticed only when the proxy next reads that client after its reply (as Postgres itself notices it). The statement completes and the connection resets normally. An abrupt TCP `RST` is noticed immediately and closes the database connection.
4. **Close on doubt after `ReadyForQuery`:** an asynchronous backend message (`NoticeResponse`, `ParameterStatus`, `NotificationResponse`) arriving in the same read immediately after the `ReadyForQuery` that ends a lease or a reset closes the connection. Arriving later on an idle connection closes it as `unsolicited-bytes`.
5. **"Reached: No" requires driver reporting:** a driver that omits `EvDBWriteRefused` when dropping unwritten bytes makes the engine report `Reached: Yes` conservatively (never a false `No`). Both `internal/shell` and `internal/proxy/proxysim` report it.
6. **Deferred mechanisms (Chunks 7–13):** `CancelRequest` is recognised and counted only (mapping arrives in Chunk 7). Idle-in-transaction uses `timeouts.client_idle` (10 m) until Chunk 7 adds the 5 s rule. While the database is down, one dial runs at a time with `timeouts.redial_delay` and queued requests wait for `timeouts.queue_wait` until Chunk 7 adds the circuit breaker. Tier R checks CSV file consistency and conservation rather than cross-process in-memory snapshot equality (in-memory round trip is proved in every tier S run). Simulated TLS handshakes with an unknown version count in the TLS 1.3 gauge bucket.
