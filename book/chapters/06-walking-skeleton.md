# 6. The first working proxy, and lending connections by the transaction

Chapters 1 through 5 built the simulator, the fake Postgres 17 server, the observability spine, the wire-protocol state machines, and the statement classifier. None of them forwarded a query from a client to a database or compiled into a proxy binary.

Chunk 6 integrates those pieces into the first runnable proxy — a **walking skeleton** (an end-to-end slice that carries real traffic through every architectural seam while leaving advanced policies as pass-throughs). A client connects over TCP or TLS (Transport Layer Security), authenticates with SCRAM-SHA-256 (Postgres's challenge-response password login), runs statements and multi-statement transactions, and receives answers identical to talking directly to Postgres 17.

What this chunk builds:

- [`internal/proxy`](../../internal/proxy): one single-threaded, **sans-I/O** state machine (**decision D1** — an engine that accepts explicit timestamps and byte/lifecycle events and returns actions, without reading clocks, starting goroutines, or touching sockets), implementing the transaction-lease lifecycle, unconditional `DISCARD ALL` reset after every lease, bounded streaming reply relay, close-on-doubt dirty handling, the error contract, and all stage seams.
- [`internal/proxy/proxysim`](../../internal/proxy/proxysim): the simulator driver that runs the exact same `proxy.Engine` over `simnet` and `simtls` against `fakepg` (tier S).
- [`internal/shell`](../../internal/shell): the real-socket goroutine shell — listeners, per-connection reader and writer goroutines, one bounded mailbox, TLS termination on both legs, socket deadlines, CSV flushing, and an isolated admin HTTP listener (`/healthz`, `/stats`, `/metrics`).
- [`internal/config`](../../internal/config) and [`cmd/mini-gateway`](../../cmd/mini-gateway): JSON configuration loading with fail-closed validation, and the `mini-gateway` binary (`-config`, `-check`, `-verifier`).
- [`internal/obs/enginesrc`](../../internal/obs/enginesrc): component `engine` observability export (84 engine and shell metrics) plus the `database` health rule, with code-generated [`docs/timeout-budget.md`](../../docs/timeout-budget.md) and [`docs/capacity-table.md`](../../docs/capacity-table.md), regenerated [`docs/csv-schema.md`](../../docs/csv-schema.md), and [`docs/extension-seams.md`](../../docs/extension-seams.md).
- [`test/e2e`](../../test/e2e): 21 tier S scenarios, 14 tier R tests against the compiled binary and Postgres 17.11 in Docker (with client TLS on), DuckDB SQL assertions for both tiers, `make chaos`, and the engine operation fuzz target `FuzzEngineOps`.

**What is not built yet.** The proxy here is deliberately plain: a small fixed set of database connections (`FixedSet`, opened at start and re-opened one dial at a time on loss) and one bounded first-in, first-out queue (`FIFO`). **The pool** (dynamic sizing, creation rate, active cancel/rollback/reuse, `on-switch` reset, `lease.reseed`, the 5 s idle-in-transaction rule, and the circuit breaker — Chunk 7), **admission control** (CoDel and adaptive LIFO — Chunk 8), **fairness** (per-tenant queues — Chunk 9), **the adaptive concurrency limit and rate gate** (Chunk 10), **shared reads / coalescing** (Chunk 11), **retries** (Chunk 12), and **multi-instance fleet division and graceful drain** (Chunk 13) are **not built**. Each sits behind an interface in [`internal/proxy/seams.go`](../../internal/proxy/seams.go) backed by a pass-through (`ClosedBreaker`, `OpenRate`, `NoCoalesce`, `RetryOff`, plus a reserved `EvDraining` input) so later chunks replace one stage without re-plumbing the engine.

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

To keep memory bounded (**D5**), the engine pauses a client (`ActClientPause`) the moment a request starts until its entire reply has been written and acknowledged (`c.out == 0`). Unprocessed client bytes already in memory are capped at `4 × MaxRead` (64 KiB; exceeding it closes the client). On the reply side, when a client's unwritten bytes exceed `limits.reply_buffer − MaxRead`, the engine emits `ActDBPause`, resuming at half the buffer; if the client stays stalled past `timeouts.client_write` (5 s), the client is dropped and its lent database connection is destroyed.

### The reset after every lease (decision D8)

Following Nick's Ruling 9 (Chapter 5), session safety never depends on reading SQL. Whenever a lease ends with status `'I'` and zero unread bytes, `ResetAlwaysPolicy` returns `ReleaseReset` and `startReset` sends `DISCARD ALL` as the proxy's own `Query` message, keeping the connection in phase `dpResetting` (counted in use, never lendable) under `timeouts.reset` (1 s).

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
| Tier R (`TestTierRSessionStateNeverCrossesClients`, real Postgres 17.11, courtesy on) | PL/pgSQL functions hiding `SET`, `CREATE TEMP TABLE`, `pg_advisory_lock`, `PREPARE`, `LISTEN`; checked via `test/oracles/dirty-check.sql` | **0 dirty observations** over 15 hostile leases | **32 dirty observations** (in-process shell against real Postgres 17) |

---

## The goroutine shell, TLS on both legs, and the two bugs only tier R caught

[`internal/shell`](../../internal/shell) bridges `proxy.Engine` to real TCP and `crypto/tls` sockets:

- **One engine goroutine** owns `Core`, reads from a single bounded mailbox (`mailboxCap = 4096`), drives a single `time.Timer` for `ActWake`, and ticks `EvPublish` at `obs.publish_interval`.
- **Two goroutines per live connection** (`readLoop` and `writeLoop`) perform blocking socket I/O with explicit deadlines and bounded write queues (64 MiB + 2 MiB slack, 65,536 items).
- **Exact pre-startup reads for TLS (decision D13).** On a newly accepted client, `readLoop` calls `io.ReadFull(nc, buf[:8])` — exactly `FirstClientRead` (8) bytes, unbuffered — and then waits (`readWait`) until the engine answers `ActClientResume` or `ActClientStartTLS`. On a TLS-enabled database connection (`backend_tls.mode` of `require` or `verify-full`), it reads exactly 1 byte (`FirstDBTLSRead`) for Postgres's `'S'`/`'N'` answer. Nothing is ever buffered across the TLS upgrade; plaintext bytes smuggled behind `SSLRequest` are seen by `crypto/tls` as a bad record header and close the connection (`TestSmuggledPlaintextAfterTheTLSRequestClosesTheConnection`).
- **Isolated admin listener (decision D12).** `/healthz` (three-state: `ok`, `degraded` when no database connection is usable and dials fail, `failed` only when the client listener stops), `/stats` (JSON), and `/metrics` (Prometheus text) read exclusively from an `atomic.Pointer[published]` copy updated on publish ticks. `TestAdminAnswersWhileTheEngineIsBlocked` proves all three answer while the engine goroutine is deliberately blocked.
- **Clean teardown.** `TestTeardownLeavesNoGoroutine` exercises 8 teardown paths under `testing/synctest` and `-race` with zero leaked goroutines.

### Two shell bugs that only real-server tests caught

During the initial build, two real defects passed every unit and tier S test and surfaced only against real Postgres 17 in tier R (fixed in commit `a5aa0e7`):

1. **Missing per-connection read credit (`conn.inflight`).** Without holding a one-chunk credit on each connection until `Engine.Step` finished processing the previous `EvDBData`, a fast Postgres reader goroutine could keep pumping 16 KiB chunks into the 4,096-entry mailbox before the engine's `ActDBPause` took effect. In `TestTierRSlowReader`, the per-client relay buffer climbed to **1.4 MB against a 64 KiB cap**. Adding `c.setInflight(true)` before posting data and `defer env.conn.setInflight(false)` in `deliver` bounds every connection to one unprocessed read at a time.
2. **Final CSV flush before `Engine.Shutdown`.** On `SIGTERM`, `engineExit` flushed the final CSV snapshot before calling `Engine.Shutdown`, leaving `requests_in_flight` or `connections_resetting` non-zero in the final flush and failing tier R SQL conservation checks. Calling `Shutdown` before the final `publish()` guarantees a clean post-stop snapshot.

Review 1's mutation checks (M1 and M2 in [`REVIEW-1.md`](../../agent_work/chunks/c06-walking-skeleton/REVIEW-1.md)) proved that reverting either fix still left `go test ./internal/shell/...` green because no fast non-Docker test exercised them. The repair round added `TestReaderPostsNoSecondReadBeforeTheEngineTakesTheFirst` and `TestCloseShutsTheEngineDownBeforeTheFinalFlush` (`internal/shell/credit_test.go`, `shutdown_test.go`), both verified to catch the reverted code in Review 2.

---

## Configuration, the binary, and building it

[`internal/config`](../../internal/config) reads JSON using the standard library with a strict token-walking pass (`decode.go`) that rejects unknown keys, wrong-case keys, and duplicate keys with full dotted paths, then validates every cross-field bound and timeout nesting constraint, failing closed with an error naming the offending key (`TestValidationFailsClosedNamingTheKey` checks 62 invalid cases). All tenant SCRAM verifiers must share one iteration count and one salt length (closing Chunk 4's carried item so unknown-user mock challenges match the table's shape).

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

Running `-check` on [`internal/config/testdata/example.json`](../../internal/config/testdata/example.json) without its TLS files demonstrates fail-closed validation; switching `client_tls.mode` to `"off"` (or supplying readable `server.crt` and `server.key`) validates and prints the 16-hex-character config fingerprint (SHA-256 of the canonical effective config with `database.password` blanked):

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

[`test/e2e`](../../test/e2e) runs 21 named tier S scenarios (`e2e-results`, `e2e-fragment`, `e2e-tx-integrity`, `e2e-reject-list`, `e2e-session-state`, `e2e-client-vanish`, five `e2e-kill-*` database-loss points, `e2e-slow-reader`, `e2e-queue-full`, `e2e-silent-db-startup`, `e2e-silent-db-query`, and six `e2e-reset-*` reset-reply tamper scenarios injected by [`tamper.go`](../../test/e2e/tamper.go) so the frozen `fakepg` stays untouched). Every seed runs twice to verify bit-identical trace/state digests and byte-identical CSV files, checks the CSV round trip, and runs DuckDB SQL conservation and fault-count assertions under `test/analysis/e2e-*/`.

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

In tier R (14 tests in [`test/e2e/tierr_test.go`](../../test/e2e/tierr_test.go) against Postgres 17.11 with `ssl=on`, SCRAM-SHA-256, and an ephemeral test CA), the entire suite runs with client TLS on, including `psql` (`sslmode=require`, `verify-full`, `disable` refused with `28000`), `pgbench -S -M simple` (800/800 transactions in the gate's tier R log (`G6-tier-r.log`, kept on the build machine, not in the repository)), `pg_stat_ssl` verification of `backend_tls.mode` (`disable`, `require`, `verify-full`, and wrong-CA failure), and DuckDB SQL checks over the binary's flushed CSV files (`test/analysis/tierr-*/`).

### Engine operation fuzzing (`FuzzEngineOps`)

[`FuzzEngineOps`](../../test/e2e/fuzz_test.go) follows [PLAN.md section 6.6](../../PLAN.md#66-rules-for-simulation-level-fuzz-targets-nicks-dst-fuzz-ruling-2026-11-24): the simulator seed is the fixed constant `OpSeed = 1` (never a hash of the fuzz bytes), decoding is total and local (4 bytes per operation, capped at `MaxOps = 48`), and the world is kept small (3 clients, 1 database connection, queue capacity 2, compressed deadlines) so queueing and contention happen frequently and execution stays above the 100/s rate floor (140/s in the gate's `G5-fuzz.tsv`, 2,947 executions in 20 s).

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

## Reviews, Astra's attack, and the two rules that came out of repair

Chunk 6 went through Review 1 (FAIL), Astra's adversarial pass, one repair round, and Review 2 (PASS). Every defect found was reproduced before fixing and locked down with a regression test.

### Review 1's two blockers ([`REVIEW-1.md`](../../agent_work/chunks/c06-walking-skeleton/REVIEW-1.md))

1. **Fragmented queries killed by a mid-message timer that was never cancelled (`internal/proxy/client.go`).** `c.midT` (`timeouts.client_mid_message`, 5 s) was armed whenever a client query arrived across multiple reads (`c.cur.mid()`), but once the final byte completed the message, `startRequest()` moved `c.phase` to `cpQueued` or `cpActive` and `pump()` returned early before reaching `e.t.cancel(&c.midT)`. Worse, `clientTimer()` fired `clientViolation(c)` without checking `c.cur.mid()`. Any fragmented query whose queue wait plus database execution exceeded 5 s (well inside `timeouts.statement` = 30 s) was killed mid-flight with `FATAL 08P01` spliced onto any already-relayed bytes, and its database connection was closed. `e2e-fragment` missed it because its fake statements ran in milliseconds. **Fix (`1f12441`):** cancel `c.midT` whenever `!c.cur.mid()` regardless of phase, and guard `tClientMid` on `c.cur.mid()`. Regression test: `TestFragmentedQueryOutlivesTheMidMessageDeadline`.
2. **Leftover bytes after `ReadyForQuery 'I'` letting a connection count as reset before `DISCARD ALL` ran (`internal/proxy/lease.go`).** When `relay()` consumed a lease-ending `ReadyForQuery 'I'`, any trailing bytes left in the same `EvDBData` slice stayed in local variable `b` and were not reported in `ReleaseInput.Unread`. `ResetAlwaysPolicy` therefore returned `ReleaseReset`, `startReset()` moved the connection to `dpResetting`, and `dbData()`'s `for len(b) > 0` loop immediately fed those pre-reset bytes into `judgeReset()`. Trailing bytes carrying `CommandComplete "DISCARD ALL"` + `ReadyForQuery 'I'` marked the connection `dpIdle` before `DISCARD ALL` had even reached Postgres. (Astra's `TestAdv_3_ResetWithTrailingReply` found the sister defect at the end of a reset reply: trailing bytes after the reset's `ReadyForQuery` were left for the next lease.) **Fix (`06ef106`):** pass `d.trailing = len(b) > 0` into `ReleaseInput.Unread` so leftover bytes after a lease close the connection dirty (`release-dirty`, no reset), and fail a reset as `protocol-surprise` if `n < len(b)` after the reset's `ReadyForQuery`. Regression tests: `TestBytesAfterReadyForQueryEndingALeaseCloseTheConnection` and `TestBytesAfterTheResetReplyCloseTheConnection`.

Review 1 also caught three major findings (unsynchronised write/close ordering in `internal/shell` — see Rule 2 below; the two missing shell unit tests; and a missing catalog check on `cmd/mini-gateway`'s actual collector wiring, now `TestEveryMetricTheBinaryExportsIsInTheCatalog`) and three minor findings (the reverse `importlint` rule for `enginesrc`, the safe-shape/RLS note in `docs/error-contract.md`'s source text, preventing `FATAL 08P01` splicing on pipelining overflow after partial reply, and cancelling an in-progress dial immediately on `ActDBClose`).

### Two rules established by the repair round

Two engine-wide invariants came directly out of Review 1 and Astra's attacks:

1. **Deadline rule — time passes before the event (`f7f59f3`).** `Engine.Step(now, ev, out)` fires every timer due at or before `now` *before* dispatching `ev`. Previously, `Step` handled data before `finish()` fired timers, so Astra's `TestAdv_3_ResetDeadlineTie` showed a reset reply arriving at the exact instant of its 1 s deadline still succeeded and dispatched queued work. Under the single rule — now documented in [`docs/timeout-budget.md`](../../docs/timeout-budget.md) and proved at equality across five tests in [`internal/proxy/deadline_test.go`](../../internal/proxy/deadline_test.go) — a reply, byte, or freed connection arriving at the exact instant of its deadline is late.
2. **"Did not reach the database" only on an explicit zero-byte refusal (rule G8, `8077048`).** In the first build, `statementLost()` defaulted `Reached` to `work.No` whenever `d.acked == req.start`. Review 1 Finding 3 showed that because `internal/shell` runs `readLoop` and `writeLoop` in separate goroutines, a database close/reset after executing an `INSERT` or `COMMIT` could post `EvDBClosed` before `writeLoop` posted `EvDBWrote`, causing the proxy to report `Reached: No` and `ERROR 08006` ("safe to retry") for a write — which would compound with Chunk 12's retries into duplicate writes. The repair fixed all three layers:
   - **Engine (`reachedFor` in `lease.go`):** once `ActDBWrite` is emitted, the request is `work.Yes` ("may have reached") unless the driver explicitly posted `EvDBWriteRefused` at or before `req.start` and `req.relayed == 0`.
   - **Shell (`io.go`):** on the database leg, `readerExit` calls `c.abort()`, waits on `<-c.writerDone` so every `EvDBWrote` precedes close, and posts `EvDBWriteRefused` before `EvDBClosed` whenever a write stopped short or queued writes were dropped.
   - **Simulator (`proxysim.go`):** `flush()` posts `EvDBWrote` for any partial `n > 0` before `gone()`, which posts `EvDBWriteRefused` if buffered unwritten bytes remain.

```go
func reachedFor(d *dbConn, req *request) work.Tri {
	if req.relayed > 0 || d == nil || !d.refused || d.refusedAt > req.start {
		return work.Yes
	}
	return work.No
}
```

### Astra's adversarial pass and Review 2 ([`ADVERSARIAL.md`](../../agent_work/chunks/c06-walking-skeleton/ADVERSARIAL.md), [`RESULTS.md`](../../agent_work/adversarial/c06-walking-skeleton/RESULTS.md))

Astra (`gpt-6-astra`) attacked all 15 claim identifiers (Claims 1–14 and 3b, at least three distinct attacks per claim, seed `6062026`). Its first dispatch was cut by its provider's content filter; the one re-dispatch finished the full harness (including the `ADV_LONG=1` 3,000-input fuzz campaign and three real Postgres 17 isolation attacks in 302.273 s) and recorded **43 held, 3 broken**:

- **Broken 1 & 2 (product defects, both fixed):** `TestAdv_3_ResetWithTrailingReply` and `TestAdv_3_ResetDeadlineTie`. Both now pass.
- **Broken 3 (specification mismatch, settled by narrowing Claim 13):** `TestAdv_13_ExplicitZeroCapacity` supplied `"capacity": 0` expecting validation to fail under "caps are positive", whereas config defaults replace zero with the documented positive default. The orchestrator kept zero-means-default and narrowed Claim 13 (below).
- **One previously held attack settled after repair:** `TestAdv_5_ZeroAcceptedBytes` posted `EvDBClosed` with no write report after `ActDBWrite` and expected `Reached: No`. That directly contradicted the rule G8 ruling from Review 1 Finding 3; the orchestrator settled it, and `TestWriteRefusedWithZeroBytesIsNotReached` tests the corrected contract.

Final adversarial tally after the repair round: **44 pass, 2 settled**. Review 2 ([`REVIEW-2.md`](../../agent_work/chunks/c06-walking-skeleton/REVIEW-2.md)) verified all 13 repair mutations (M1–M8b) were caught by their regression tests, ran all 38 test packages cleanly, and returned **VERDICT: PASS**.

### Gate record

There is no `gate/result.json` committed after the repair round. The last full build-stage gate run (commit `dbc896b`, 2026-11-26, 2,222 s, recorded in [`NOTES.md`](../../agent_work/chunks/c06-walking-skeleton/NOTES.md)) passed G0 (tools, gofmt, build, vet, tidy), G1 (detlint, importlint, boundlint, paniccheck), G2 (comments), G3 race, G4 (markers and 200-seed sweep: 9,073 seed runs in 304 s), G5 (16 fuzz targets, 0 seed sweeps; `FuzzEngineOps` 2,947 execs at 140/s), G6 (tier R: 23 tests on Postgres 17.11 in 203 s), G7 (DuckDB CSV assertions: 4,269 run directories, 59 scenarios, 195 SQL files, 0 failures), G8 (gen-docs), G9 (site), and G10 (`fuzz-plant.sh` found the planted bug in 2 s). Its only red check was G3 unit on the two unedited adversarial tests (`TestAdv_5_ZeroAcceptedBytes` and `TestAdv_13_ExplicitZeroCapacity`) before the orchestrator settled them in `9e3460f`. The final-stage gate runs after this prose pass.

---

## Narrowed claim and stated limits

### Narrowed Claim 13

> Config validation fails closed on every invalid setting it documents. A cap (`pool.connections`, `queue.capacity`, `limits.max_clients`, `limits.max_startup`, `limits.reply_buffer`) that is zero or omitted takes its default — zero is not an invalid setting; a negative cap, or one outside its documented range, is refused with an error naming the key.

Durations follow the same rule (zero or omitted takes the default; explicit `"0s"` or negative durations fail JSON decoding). [`docs/capacity-table.md`](../../docs/capacity-table.md) states "zero means the default" for each configurable cap, and `TestZeroCapMeansTheDefaultAndANegativeCapIsRefused` holds both halves per key.

### Stated limits

1. **`DISCARD ALL` residue (decision D8):** four things survive Postgres's `DISCARD ALL`: the PRNG random seed (Chunk 7's `lease.reseed` closes this), the *existence* of a custom placeholder setting name (its value is cleared), in-memory state of loaded extensions and procedural languages (PL/Perl, PL/Python), and privileged replication-origin sessions.
2. **Safe-shape limits carry into `08006` vs `08007`:** choosing `08006` (`lost-after-read`, retry safe) over `08007` (`lost-after-write`) rests on Chunk 5's `SafeRead`. A view, user-defined operator, shadowed function, or table with a row-level security policy can hide a write behind a statement text that fits the safe shape; databases with such objects should treat `08006` like `08007` (stated in [`docs/error-contract.md`](../../docs/error-contract.md)).
3. **Graceful client `FIN` while paused:** a client that sends TCP `FIN` while its request is queued or running is noticed only when the proxy next reads that client after its reply (as Postgres itself notices it); the statement completes and the connection resets normally. An abrupt TCP `RST` is noticed immediately and closes the database connection.
4. **Close on doubt after `ReadyForQuery`:** an asynchronous backend message (`NoticeResponse`, `ParameterStatus`, `NotificationResponse`) arriving in the same read immediately after the `ReadyForQuery` that ends a lease or a reset closes the connection; arriving later on an idle connection closes it as `unsolicited-bytes`.
5. **"Reached: No" requires driver reporting:** a driver that omits `EvDBWriteRefused` when dropping unwritten bytes makes the engine report `Reached: Yes` conservatively (never a false `No`). Both `internal/shell` and `internal/proxy/proxysim` report it.
6. **Deferred mechanisms (Chunks 7–13):** `CancelRequest` is recognised and counted only (mapping arrives in Chunk 7); idle-in-transaction uses `timeouts.client_idle` (10 m) until Chunk 7 adds the 5 s rule; with the database down, one dial runs at a time with `timeouts.redial_delay` and queued requests wait for `timeouts.queue_wait` until Chunk 7 adds the circuit breaker; tier R checks CSV file consistency and conservation rather than cross-process in-memory snapshot equality (in-memory round trip is proved in every tier S run); simulated TLS handshakes with an unknown version count in the TLS 1.3 gauge bucket.
