# 7. The pool: a hard cap, a creation rate, and connections that go bad

Postgres spawns one operating-system process per client connection and caps how many it will accept (`max_connections`, 200 by default in our test container, with 3 slots reserved for superusers). When 500 application clients connect directly to Postgres at once, the first 196 or 197 get in and the rest fail with `53300` (`too_many_connections`).

Putting a transaction-pooling proxy in front of Postgres fixes that failure mode, as measured directly against Postgres 17 by `TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy` ([`test/e2e/c07_tierr_test.go`](../../test/e2e/c07_tierr_test.go)):

```sh
$ MG_TIER_R=1 go test -count=1 -v -run '^TestTierRPoolHeadline' ./test/e2e
=== RUN   TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy
    c07_tierr_test.go:333: HEADLINE 1: 500 clients direct: 196 connected, 304 refused, 196 database backends, 196 queries ok; through the proxy: 500 connected, 0 refused, 16 database backends (pool 16, peak open+connecting 16), 500 queries ok
--- PASS: TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy (5.91s)
```

Direct clients exhausted the database (196 connected while the prior setup connection finished closing, 304 refused; 197 at most on a 200-slot server with 3 superuser slots). Through `mini-gateway` with a 16-connection pool, all 500 clients connected, ran their queries, and succeeded on 16 Postgres backends.

Holding 16 connections open fixes the slot limit and introduces five harder problems. The proxy must keep `open + connecting` below the cap during storms, pace new dials on a cold start, reset shared session state between tenants without breaking performance, handle connections that die silently behind a firewall or failover, and fail fast when Postgres itself goes down.

Chunk 7 replaces the walking skeleton's `FixedSet`, `ClosedBreaker`, and `ResetAlwaysPolicy` with a real connection pool and circuit breaker across five areas:

- [`internal/pool`](../../internal/pool): a pure, **sans-I/O** (no clocks, sockets, goroutines, or floating point) pool core on `tools/lint-rules/det-packages.txt`. It manages backend connections by opaque `ConnID`, enforcing the hard cap, concurrent-connect cap, creation-rate pacing, jittered idle timeouts and lifetimes, off-path staleness and `suspect` checks, a single circuit breaker, the dirty-connection clean-up sequence, `always` and `on-switch` release policies with optional re-seeding, bounded cancels, and fleet budget division (`Share`).
- [`internal/proxy/corepool.go`](../../internal/proxy/corepool.go) and hooks across [`internal/proxy`](../../internal/proxy): `CorePool`, `CoreBreaker`, and `CorePolicy` wired into `proxy.Engine`, plus the new transport action `ActDBCancel` and event `EvDBCancelDone` implemented in both [`internal/proxy/proxysim`](../../internal/proxy/proxysim/proxysim.go) and [`internal/shell/cancel.go`](../../internal/shell/cancel.go).
- [`internal/shell`](../../internal/shell): configurable TCP keepalive on database sockets (`keepAliveDialer`), bounded short-lived cancel connections, and a 100 ms write deadline before socket close so a TLS close-notify cannot stall on a dead peer.
- [`internal/config/pool.go`](../../internal/config/pool.go): fail-closed configuration for `pool.*`, `breaker.*`, `backend.tcp_keepalive.*`, `backend.server_keepalive`, `backend.path_idle_limit`, `timeouts.idle_in_transaction`, `timeouts.cleanup`, `lease.reset`, and `lease.reseed`, plus `File.Warnings()` and updated rows in [`docs/timeout-budget.md`](../../docs/timeout-budget.md), [`docs/capacity-table.md`](../../docs/capacity-table.md), and [`docs/error-contract.md`](../../docs/error-contract.md).
- [`internal/obs/poolsrc`](../../internal/obs/poolsrc/poolsrc.go): component `pool` observability export (gauges, counters, conservation invariants, and `lease_time` / `idle_age_at_lend` histograms under `obs.detail`) and the `breaker` health rule (`degraded` while open or half-open).

---

## Hard cap, creation rate, jitter, and fleet budgets

### Accounting and on-demand dialing

Every backend connection in [`internal/pool/pool.go`](../../internal/pool/pool.go) sits in exactly one of six states: `connecting`, `idle`, `checking`, `lent`, `cleaning`, or `resetting`. Two accounting rules never bend:

1. **Hard cap:** `open + connecting ≤ pool.connections` at every step, where `open` is the sum of the five non-connecting states. A connection that is being checked, drained, rolled back, or reset still occupies a slot and is never lendable.
2. **Dial on demand only:** the pool does no prefill at startup and holds no minimum idle count. Supply is `idle + connecting + checking + resetting`; the pool dials only while `waiting > supply`, up to both the hard cap and `pool.max_connecting` (default 4).

**Why no prefill.** Opening connections at startup and refusing to serve if Postgres is unreachable turns a database blip into a crash-looping proxy fleet. Dialing on demand lets an instance boot cleanly, report `degraded` health when the breaker opens, and avoid burning backend slots for idle instances.

The pool also shares one fleet-wide budget across instances (`pool.Share`, [`internal/pool/config.go`](../../internal/pool/config.go)). Given `fleet.backend_connections` or `fleet.connect_rate_per_s` and `fleet.instances`, each instance takes `total / N` rounded down so the fleet's sum never exceeds `total`. Any total smaller than `N` is refused at config load naming the key; an uneven division succeeds and emits a warning through `File.Warnings()` (printed by `-check` and at startup).

### Pacing dials with a one-slot GCRA

An empty pool facing a morning ramp or a mass disconnect would hammer Postgres's fork-and-authentication path if it opened all connections at once. Alongside `pool.max_connecting`, the pool paces dials through a one-slot **GCRA** (Generic Cell Rate Algorithm—a leaky-bucket scheduler that tracks the earliest theoretical arrival time, `tat`, of the next permitted dial) in [`internal/pool/rate.go`](../../internal/pool/rate.go):

```go
// limiter is a one-slot GCRA pacing dial attempts to at most one per interval.
type limiter struct {
	interval Instant
	tat      Instant
}

func newLimiter(perSec int) limiter {
	// Round up so rates that do not divide 1s still hold floor(W*rate/1s)+1.
	return limiter{interval: (Instant(time.Second) + Instant(perSec) - 1) / Instant(perSec)}
}

func (l *limiter) ready(now Instant) bool { return now >= l.tat }

func (l *limiter) take(now Instant) {
	if l.tat < now {
		l.tat = now
	}
	l.tat += l.interval
}
```

Rounding `interval` up to the next nanosecond guarantees that consecutive dials are at least `1 / rate` apart even for rates that do not divide one second (such as 3, 7, or 999 dials/s), so in any window `W` the pool issues at most `floor(W · rate / 1s) + 1` dials. Half-open breaker trials and redials after a mass death pass through the same limiter.

### Jittered lifetimes and exact deadlines

Connections created together during a cold start would expire together if they shared one fixed lifetime, recreating a dial spike every 30 minutes. At dial time, `Pool.jitter` draws each connection's `idleLimit` (from `pool.idle_timeout`, default 180 s) and `lifeLimit` (from `pool.max_lifetime`, default 30 min) uniformly per nanosecond in `d ± d · permille / 1000` (default ±10%). Validation refuses any duration whose jitter amplitude falls below `MinJitterSpread` (1 ms) unless jitter is explicitly disabled, so short test lifetimes cannot silently truncate to zero spread. Lifetimes expire only while idle or at release—never mid-lease.

Like `proxy.Engine`, the pool core obeys one deadline law: **time passes before the event**. Every action-returning method (`Demand`, `Checkout`, `Release`, `StepDone`, `StepFailed`, `Lost`, `Tick`, `Shutdown`, and the dial/cancel callbacks) advances the breaker and expires every deadline due at or before `now` before applying the event. Equality counts as expired: a reset reply or check reply arriving at its exact deadline is late. All pool timers coalesce onto one engine timer (`tPool`) armed at `core.Next()`.

---

## Release policies and the random-seed residue

Chapter 6 established decision D8: session isolation never relies on parsing SQL. Every cleanly ended lease (`ReadyForQuery 'I'`, zero unread bytes) ran `DISCARD ALL`. Chunk 7 extends that same single code point (`startReset` and `resetJudge` in [`internal/proxy/lease.go`](../../internal/proxy/lease.go)) with two orthogonal config knobs:

- **`lease.reset: always` (default):** sends `DISCARD ALL` immediately on every clean release.
- **`lease.reset: on-switch`:** tags the cleanly released connection with the tenant that just used it and returns it to `idle` without resetting. At checkout, `Pool.pick` prefers (0) a free connection tagged for the same tenant (most recently used first), then (1) an untagged reset connection, then (2) another tenant's tagged connection. Picking class 2 does not lend it: the pool clears the tag, starts a switch reset (`ResetAtSwitch`), increments `tenant_switches`, and leaves the client waiting in its queue until the reset completes.
- **`lease.reseed: off | on` (default `off`):** `DISCARD ALL` clears session parameters, prepared statements, temporary tables, and locks, but Postgres's per-backend `random()` generator state survives `DISCARD ALL`. With `lease.reseed: on`, every `DISCARD ALL` is immediately followed in the same write by `SELECT setseed(0.xxxxxx)` drawn from the pool's seeded stream. `resetJudge` expects the `DISCARD ALL` tag and `ReadyForQuery 'I'`, then an optional `RowDescription`, one `DataRow`, `CommandComplete("SELECT 1")`, and `ReadyForQuery 'I'`, all in one round trip.

Running `TestTierRPoolSessionStateUnderEachReleasePolicy` ([`test/e2e/c07_tierr_test.go`](../../test/e2e/c07_tierr_test.go)) against Postgres 17 executes eight hostile session-mutating functions (`SET`, temp tables, advisory locks, prepared statements, `LISTEN`, a held cursor, a role change, and a custom setting carrying a per-lease secret, plus `SELECT setseed(0.25)`) followed in one transaction on the next lease by a read-back of `mg_hostile.secret`, all seven columns of [`test/oracles/dirty-check.sql`](../../test/oracles/dirty-check.sql) (`prepared`, `cursors`, `listens`, `adv_locks`, `session_gucs`, `temp_rels`, `role_changed`), `SHOW search_path`, and then `SELECT random()` across alternating and same-tenant callers under all four combinations:

```sh
$ MG_TIER_R=1 go test -count=1 -v -run '^TestTierRPoolSessionState' ./test/e2e
=== RUN   TestTierRPoolSessionStateUnderEachReleasePolicy
    c07_tierr_test.go:187: tierr-pool-session-always: crossings 0, same-tenant carry-over 0, seed residue other tenant true same tenant true; switches 0 reseeds 0
    c07_tierr_test.go:187: tierr-pool-session-on-switch: crossings 0, same-tenant carry-over 4, seed residue other tenant true same tenant true; switches 14 reseeds 0
    c07_tierr_test.go:187: tierr-pool-session-always-reseed: crossings 0, same-tenant carry-over 0, seed residue other tenant false same tenant false; switches 0 reseeds 24
    c07_tierr_test.go:187: tierr-pool-session-on-switch-reseed: crossings 0, same-tenant carry-over 4, seed residue other tenant false same tenant true; switches 14 reseeds 14
--- PASS: TestTierRPoolSessionStateUnderEachReleasePolicy (1.52s)
```

Cross-tenant state crossings are **0** in all four arms. Same-tenant carry-over appears only under `on-switch` (4 of 4). Turning `lease.reseed` on eliminates the `random()` seed residue across tenants in both modes (and within a tenant under `always`, where every lease resets; under `on-switch`, same-tenant reuse runs neither reset nor re-seed).

---

## Connections that die silently: the 5 a.m. scenario

A TCP connection is just state in memory at two endpoints; it can sit idle for hours with zero packets on the wire. A stateful firewall, NAT gateway, or cloud load balancer in the middle keeps a connection table and silently discards entries that go idle past its limit (commonly 4 minutes to 1 hour, documented in [`docs/knowledge/release-it-pooled-connections.md`](../../docs/knowledge/release-it-pooled-connections.md)). Neither Postgres nor the proxy receives a FIN or RST.

Pair that silent drop with **most-recently-used (MRU)** lending—which pools use so unused connections stay cold and shrink under idle timeout—and you get the classic 5 a.m. outage:

1. Overnight, light trickle traffic rides one warm connection while the rest of the pool sits idle past the middlebox's timeout and gets silently dropped (`blackhole`).
2. At the morning ramp, requests grab those idle connections. Writing a SQL query into a blackholed socket succeeds locally—the kernel queues the bytes and starts retransmitting into the void.
3. No error returns until the statement deadline expires (30 s, counting `backend_no_reply`), or minutes later if no deadline exists. Worse, naive validity queries (`SELECT 1` without a timeout) or blocking closes (waiting for TLS close-notify on a dead socket) hang the same way.

The pool defends against silent death with four coordinated mechanisms:

1. **TCP keepalives in both directions.** On the proxy socket, `keepAliveDialer` ([`internal/shell/shell.go`](../../internal/shell/shell.go)) sets `SO_KEEPALIVE` with `backend.tcp_keepalive.idle = 30s`, `interval = 10s`, and `count = 3` (dead peer detected in 60 s). On the server side, `backend.server_keepalive = on` sends `tcp_keepalives_idle`, `tcp_keepalives_interval`, and `tcp_keepalives_count` as Postgres startup parameters so Postgres probes back and reaps slots if the proxy vanishes. Config validation fails closed unless `tcp_keepalive.idle ≤ backend.path_idle_limit / 2` (default `path_idle_limit = 240s`) and keepalive durations are whole seconds of at least 1 s.
2. **Idle timeout shorter than the path limit.** `pool.idle_timeout` (180 s) must be strictly below `backend.path_idle_limit` (240 s), closing cold connections before a middlebox drops them. The morning ramp becomes a paced cold start instead of a minefield of dead sockets.
3. **Off-path check before reuse, plus the `suspect` rule.** Before lending a connection that has been idle for at least `pool.check_after_idle` (30 s) or carries the `suspect` flag, `Checkout` moves it into `StateChecking` and sends an empty simple-protocol `Query("")` (PgBouncer's zero-SQL probe: Postgres replies `EmptyQueryResponse` + `ReadyForQuery 'I'`). The check runs **off the request path** (up to 4 checks per checkout) under `pool.check_timeout` (250 ms). The client stays in its queue and takes whichever connection becomes lendable first; a hanging or failing check closes the connection (`check_failed`) with **zero client error**. Any connection-class failure—check failure, idle loss, lent/cleaning/resetting loss, or a zero-reply statement timeout—marks every idle sibling `suspect` and every busy connection `suspect` on return, so a mass silent death or failover is cleaned up one check at a time instead of poisoning subsequent clients.
4. **Non-blocking close.** When the pool emits `ActClose`, the engine forgets the connection immediately with no wait state. In [`internal/shell/conn.go`](../../internal/shell/conn.go), `abort()` applies a 100 ms write deadline before `Close()` so a TLS close-notify cannot block on a blackholed socket.

To test these defenses without waiting two real hours, [`test/e2e/c07_faultnet.go`](../../test/e2e/c07_faultnet.go) puts a `pathRelay` between the proxy and `fakepg`. It implements `blackhole`, `peer_forgot` (accept the first write, then reset one network delay later), `idle_drop(D)` with modelled keepalive, `polite_close`, and held server slots (`ReapAfter`). The night-trickle scenario runs 8 pooled connections under `idle_drop = 1h`: one read every 5 s for 2 virtual hours, followed by an 8-client × 20-read morning ramp. Running it with all remedies off and then with each remedy alone shows what each layer fixes:

```sh
$ go test -count=1 -v -run '^(TestAblationNightTrickle|TestNightTrickleEachRemedy)' ./test/e2e
=== RUN   TestAblationNightTrickleWithEveryRemedyOffHangsTheRamp
    c07_canary_test.go:86: all remedies off: 4 client errors, 4 no-reply deadlines, pairs 13 blackholed 9 forgot 0 polite 0 idle drops 9 held 0 reaped 0
--- PASS: TestAblationNightTrickleWithEveryRemedyOffHangsTheRamp (1.44s)
=== RUN   TestNightTrickleEachRemedyAloneCarriesTheRamp
    c07_canary_test.go:99: keepalive alone: 0 client errors (0 53M03, 0 53M01, 0 08006), 0 no-reply deadlines, 0 checks failed, breaker opened 0 times, 2 idle closes, pairs 8 blackholed 0 forgot 0 polite 0 idle drops 0 held 0 reaped 0
    c07_canary_test.go:99: idle alone: 0 client errors (0 53M03, 0 53M01, 0 08006), 0 no-reply deadlines, 0 checks failed, breaker opened 0 times, 10 idle closes, pairs 10 blackholed 0 forgot 0 polite 0 idle drops 0 held 0 reaped 0
    c07_canary_test.go:99: check alone: 0 client errors (0 53M03, 0 53M01, 0 08006), 0 no-reply deadlines, 4 checks failed, breaker opened 0 times, 2 idle closes, pairs 9 blackholed 9 forgot 0 polite 0 idle drops 9 held 0 reaped 0
--- PASS: TestNightTrickleEachRemedyAloneCarriesTheRamp (4.22s)
```

With all remedies off, the middlebox silently drops 9 idle connections and the morning ramp hits 4 of them, hanging 4 client statements until the statement deadline (`08006` + `backend_no_reply`). Each remedy alone prevents every hang: **keepalive** keeps the middlebox table alive (0 drops); **idle timeout** closes cold connections after 180 s before the 1-hour drop (10 idle closes, 0 drops); and **check before reuse** absorbs the 9 dropped flows off-path (4 caught by failed checks at 250 ms, the rest expired idle, 0 client errors, 0 breaker openings).

---

## The circuit breaker

When Postgres is actually unreachable, dialing on every request wastes CPU and makes clients wait for `timeouts.database_connect` (2 s). The pool owns one database-down circuit breaker ([`internal/pool/breaker.go`](../../internal/pool/breaker.go)):

- **What counts toward `breaker.failures` (default 5 consecutive failures):** failed dials (including connect deadlines), unsolicited bytes on an idle connection (`idle_error`), loss of a lent, resetting, or cleaning connection, and a failed half-open trial. Any successful dial, passed check, or completed reset zeroes the consecutive streak.
- **What does not count:** SQL errors from Postgres never reach the pool, and a lone zero-reply statement deadline marks siblings `suspect` instead of tripping the breaker. A polite FIN on an idle connection (`peer_closed_idle`, such as Postgres's `idle_session_timeout`) also marks siblings `suspect` without counting. Most importantly, **a failed check of a non-trial idle connection does not count**.

That last rule is easy to get wrong. If a firewall drops an idle pool overnight, four consecutive pre-lend checks will fail in a row while Postgres is completely healthy. Opening the breaker on those failed checks would reject healthy traffic with `53M03` for a full `open_for` window. Instead, `checkFailed` ([`internal/pool/pool.go`](../../internal/pool/pool.go)) closes the bad flow, marks siblings suspect, and lets the replacement dial decide whether the database is actually down:

```go
func (p *Pool) checkFailed(c *conn, r CheckResult) {
	p.snap.ChecksByResult[r]++
	trial := c.trial
	// Leave StateChecking before remove so CheckError is not double-counted.
	c.state = StateIdle
	c.trial = false
	p.remove(c, CloseCheckFailed)
	// A non-trial idle check failure proves only an old flow died, not that new
	// dials fail; only a failed half-open trial counts toward the breaker.
	if trial {
		p.trialFailed(c.id)
	}
	p.markSuspect()
}
```

Once open, `Allow(now)` returns `false`, new requests are refused immediately at the gate with `ERROR 53M03` and zero dials, and any requests already waiting in the queue are flushed with `53M03` (`ActRefuseQueued`). Health reports `degraded` (not `failed`, so load balancers do not pull every proxy instance out of rotation at once).

After `breaker.open_for` (default 1 s), the breaker moves to **half-open**. To avoid hammering a dead database when no clients are calling, it starts its **single trial**—a dial, or a check of an idle connection if the pool is already at its cap—only once a request was refused while open or work is waiting. While half-open, `Allow(now)` returns `true` so arriving requests wait in their queue behind that one trial instead of failing immediately after a quiet outage. If the trial passes, the breaker closes and normal demand-driven dialing resumes under the creation-rate cap (no dogpile). If the trial fails, the breaker re-opens for another `open_for` and flushes queued requests with `53M03`.

---

## Dirty connections, idle transactions, and cancel forwarding

Not every lease ends cleanly on `ReadyForQuery 'I'`. When a client disconnects mid-statement or mid-transaction, stalls past `timeouts.client_write`, exceeds `timeouts.idle_in_transaction` (default 5 s), or hits `timeouts.statement` (default 30 s), the pool core runs the **dirty-connection table** instead of blindly closing the backend:

1. **Mid-statement (`Mid: true`):** the core emits `ActCancel` (sending a Postgres `CancelRequest` on a second short-lived plaintext connection, capped at `pool.max_cancels = 16` and bounded by `timeouts.cleanup = 1s`) and drains remaining reply frames until `ReadyForQuery`. If draining starts mid-frame, `coreDirty` seeds the drain header buffer with a non-`'Z'` marker (`drainMidFrame`) so payload bytes inside a split `DataRow` can never be mistaken for a `ReadyForQuery` header.
2. **Inside a transaction (`'T'` or `'E'`, or `InTx: true` when between statements):** emits `ActRollback` (`ROLLBACK`).
3. **Reset:** emits `ActReset` (`DISCARD ALL`, plus `SELECT setseed(...)` if enabled).
4. **Cancel completion gate:** a connection with an in-flight cancel enters `stepAwaitCancel` and is never lent again until `EvDBCancelDone` arrives. If a cancel's safety wait (`2 × timeouts.cleanup`) expires without reporting done, `cancelLost` taints the connection so it closes (`cleanup_failed`) on its next return—preventing a delayed cancel packet from ever hitting a later client's statement.

Any error, unexpected trailing byte, or `timeouts.cleanup` expiry closes the connection (`cleanup_failed`, or `no_reply` when the triggering statement received zero reply bytes). Two client-visible error rows accompany these paths ([`docs/error-contract.md`](../../docs/error-contract.md)):

- **Idle inside a transaction:** after `timeouts.idle_in_transaction` (5 s) between statements, the client receives `FATAL 25P03` and is closed while the backend is rolled back, reset, and reused.
- **Mid-statement loss inside a transaction:** if the backend drops or times out mid-statement inside a transaction, the client receives `FATAL 08006` (`lost-mid-statement-in-transaction`) unless the message contains `COMMIT`, `END`, or `PREPARE` (case-insensitive), in which case it receives `ERROR 08007` because the transaction may have committed.

Client-issued `CancelRequest` packets are routed by `BackendKey` (`e.keys` in [`internal/proxy/corepool.go`](../../internal/proxy/corepool.go)): if the matching client has an active lent connection, `Pool.ClientCancel` forwards the cancel on a second connection (within `pool.max_cancels`); if the client's request is sitting in the queue, `clientCancel` removes it immediately and answers `ERROR 57014` (`cancelled-queued`); idle or unknown keys are ignored and counted.

---

## How the pool and the admission queues meet

The compiled binary runs this pool core behind Chapter 8's per-tenant admission queues (default `timeout` rule with adaptive LIFO). Decision D3 holds across the boundary: **there is only one waiting place**—the tenant's admission ring. The pool never queues requests internally.

While the breaker is **open**, an incoming request outside a transaction never enters a queue: the gate refuses it at once with `ERROR 53M03` (`StageRefuse[breaker]`). While the breaker is **half-open**, or while no connection is immediately lendable (all connections are lent, dialing, checking, cleaning, or switching tenants), the request sits in its tenant admission queue. Every queued request leaves by exactly one exit:

| Exit | Client answer | Counted (admission / engine) |
| --- | --- | --- |
| A connection is lent (trial, check, reset, or dial completes) | Postgres reply | `served` / `StagePassed[queue]` |
| Admission rule's allowed wait expires (`interval`, `target`, or control-law drop) | `ERROR 53M01` | `shed{rule}` / `Shed` |
| Tenant ring full, total capacity full, or tenant cap reached | `ERROR 53M01` | `shed{overflow,total,tenants}` / `Shed` |
| `timeouts.queue_wait` backstop expires | `ERROR 53M01` | `shed{deadline}` / `Shed` |
| Breaker opens or re-opens while queued | `ERROR 53M03` | `shed{breaker}` / `Shed`, `BreakerFlushed` |
| Client sends `CancelRequest` while queued | `ERROR 57014` | `removed` / `Failed`, `CancelsQueued` |
| Client disconnects, or engine shuts down | none | `removed` / `Failed` |

When the breaker opens with requests standing in admission queues, `refuseAdmitted` ([`internal/proxy/corepool.go`](../../internal/proxy/corepool.go)) cannot simply call `Queue.Pop`—a pop means a served request and would violate the engine-to-admission shed conservation invariant (`admission.shed == engine.requests_shed`). Instead, it first sweeps any rule sheds already due at `e.now` (answering them `53M01`), then removes each remaining queued request in arrival order through `Queues.Refuse(now, id)` ([`internal/admission/queues.go`](../../internal/admission/queues.go)) and answers `53M03`. `Refuse` counts the removal under `ReasonBreaker` without placing an item in `TakeShed` and without marking the tenant queue as shedding (the pool component's `breaker` health rule reports the outage, not the admission rule).

Because a `ShedQueue` skips the direct-lend shortcut so every request passes through `Push` and `Pop`, a checkout that starts an off-path check or waits on a half-open trial calls `Unpop` to return the entry to its ring without issuing a second checkout in the same enqueue step. At `Engine.Shutdown`, the engine sweeps due admission sheds first, closes clients (routing active leases through the dirty table and queued requests through `RemoveAt`), shuts down the pool core and any open cancel connections, and finally closes empty tenant queues and cancels `tAdmission`.

---

## How it is tested and what the tests proved

Every pool invariant is checked at three layers: pure core property tests and fuzzing, 22 tier S simulator scenarios with DuckDB SQL assertions, and 5 tier R tests against the compiled binary and Postgres 17.

### Core reference model, canaries, and `FuzzPoolOps`

[`internal/pool/model_test.go`](../../internal/pool/model_test.go) implements an independent reference model tracking live connections, dial spacing, `suspect` propagation, reset tags, and cancel lifecycles. `TestPoolMatchesTheReferenceModelOnRandomOperations` runs 3,000 random operation lists per release-policy combination (`always`, `on-switch`, each with and without `reseed`), verifying accounting, `Invariants()`, and `Quiesce()` after every step.

[`FuzzPoolOps`](../../internal/pool/pool_test.go) decodes raw bytes locally (3 bytes per operation, capped at 64 operations, constant jitter seed `modelSeed = 7`) against the same model; [`scripts/fuzz-plant.sh`](../../scripts/fuzz-plant.sh) verifies it catches the planted ordering bug `PlantResetClearsSuspect` (clearing `suspect` when a reset completes instead of requiring a check) within its 20 s budget.

Both the core model and the tier S end-to-end scenarios carry planted-bug canaries and ablations proving each test fails when its mechanism is removed:

```sh
$ go test -count=1 -v -run '^(TestCanaryPool|TestCanaryJoin|TestAblationColdStart|TestAblationRestart|TestPathFault)' ./test/e2e
=== RUN   TestCanaryPoolCapPlantBreaksTheHardCap
    c07_canary_test.go:41: caught: pool-hard-cap seed 1: sim: invariant proxy.engine: database connections 8 open + 1 connecting above the cap 8: seed 1, step 11449, time 8545184
        REPLAY: go run ./cmd/mgsim run pool-hard-cap -seed 1
--- PASS: TestCanaryPoolCapPlantBreaksTheHardCap (0.23s)
=== RUN   TestAblationColdStartWithoutTheCreationRateExceedsTheBound
    c07_canary_test.go:45: caught: e2e: check failed: dials 1 and 2 only 6.9µs apart, the creation rate allows one per 50ms
--- PASS: TestAblationColdStartWithoutTheCreationRateExceedsTheBound (0.42s)
=== RUN   TestCanaryPoolCheckDeadlinePlantHoldsTheQueuedRequest
    c07_canary_test.go:49: caught: pool-check-hangs seed 1: database connection 1 is checking
        connection 1 is checking
--- PASS: TestCanaryPoolCheckDeadlinePlantHoldsTheQueuedRequest (0.00s)
=== RUN   TestCanaryPoolSuspectPlantCostsClientsAfterAMassDeath
    c07_canary_test.go:53: caught: e2e: check failed: 6 client errors after the mass death, at most the 2 connections that could be lent
--- PASS: TestCanaryPoolSuspectPlantCostsClientsAfterAMassDeath (0.09s)
=== RUN   TestCanaryPoolOnSwitchIgnoringTheTagCrossesTenants
    c07_canary_test.go:57: caught: e2e: check failed: session state crossed tenants 74 times under on-switch
--- PASS: TestCanaryPoolOnSwitchIgnoringTheTagCrossesTenants (0.06s)
=== RUN   TestAblationRestartWithoutTheBreakerAnswersNo53M03
    c07_canary_test.go:61: caught: e2e: check failed: breaker transitions [0 0 0 0]
--- PASS: TestAblationRestartWithoutTheBreakerAnswersNo53M03 (2.73s)
=== RUN   TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount
    c07_canary_test.go:71: caught: pool-admission-restart seed 1: sim: invariant proxy.engine: admission: the core shed 0, the engine answered 4: seed 1, step 11379, time 2034608794
        REPLAY: go run ./cmd/mgsim run pool-admission-restart -seed 1
--- PASS: TestCanaryJoinBreakerFlushThroughPopBreaksTheAdmissionCount (0.40s)
=== RUN   TestPathFaultOnOneConnectionLeavesTheOtherUnchangedAndClearingRestores
--- PASS: TestPathFaultOnOneConnectionLeavesTheOtherUnchangedAndClearingRestores (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	3.950s
```

### Tier S scenarios, DuckDB assertions, and Tier R Postgres 17

- **22 tier S pool scenarios (`TestScenarioPool`, [`test/e2e/c07_scenarios.go`](../../test/e2e/c07_scenarios.go)):** test the hard cap (1,000 simultaneous connects over 8 connections), 500-client reconnection storm joined against the direct baseline, cold start, jittered lifetimes, database restart (`pool-db-restart` and `pool-admission-restart` with the adaptive-LIFO timeout queue), idle-in-transaction, targeted cancel, and all four release-policy arms. They also cover silent mass death (`blackhole` and `peer_forgot`), polite idle close, hanging check, held server slots, the 2-hour night trickle, and core reruns of Chapter 6's results, transaction integrity, slow reader, and client-vanish scenarios. Every run checks bit-identical simulator digests and byte-identical CSV flushes across two runs per seed, plus DuckDB SQL assertions under `test/analysis/pool-*/` (conservation, cap peaks, dial rate, post-restart recovery, and `backend_no_reply = 0`).
- **Tier R real Postgres 17 ([`test/e2e/c07_tierr_test.go`](../../test/e2e/c07_tierr_test.go)):** alongside the headline 500-client benchmark and the four-arm session-isolation test quoted above, three further tests run against the compiled binary and Postgres 17. `TestTierRPoolEmptyQueryCheckAndServerKeepalive` confirms `Query("")` returns `EmptyQueryResponse` + `ReadyForQuery 'I'`, `SHOW tcp_keepalives_idle/interval/count` reads back `30/10/3`, and `ss -tnoi` shows a kernel keepalive timer on the gateway's database socket (`TestKeepAliveDialerSetsTheDatabaseSocketOptions` in [`internal/shell/keepalive_test.go`](../../internal/shell/keepalive_test.go) reads the options back with `getsockopt`). `TestTierRPoolCancelReachesTheRightStatementOnly` cancels a `pg_sleep(10)` with `57014` in under 1 s without disturbing a concurrent `pg_sleep(1.5)`. `TestTierRPoolReconnectionStormWithClientTLS` drops and reconnects 500 TLS clients while holding backends to a peak of 5 (cap 16) and clients in startup to the exact `limits.max_startup` cap of 64, serving all 500 queries.

---

## Stated limits

1. **Cap accounting tracks close issuance, not OS socket teardown.** `open + connecting ≤ cap` holds in the pool core at every instant where a connection counts until `ActClose` is emitted, not until the kernel finishes closing the file descriptor. Because the shell closes descriptors synchronously without waiting for the peer (bounded by a 100 ms write deadline for TLS close-notify), physical overlap is at most one closing descriptor per replacement dial.
2. **Jitter spreads expiries rather than making them unique.** Each connection's idle timeout and lifetime is drawn uniformly per nanosecond in `d ± d · permille / 1000` (minimum spread 1 ms enforced at validation). That bounds simultaneous expiries—at most 6 of 200 co-created connections within any 1 s window at the default ±10% of 30 min, and at most 4 within 100 ms in `pool-lifetimes`—without guaranteeing zero ties.
3. **Recently idle connections (`< pool.check_after_idle`) are lent without a check.** After a silent mass death, client errors are bounded by the connections lent at that instant **plus** any non-suspect connection idle for less than `pool.check_after_idle` (30 s). Each such dead connection can fail at most one statement (`08006` or `08007`) before its failure marks every sibling `suspect`; connections idle at or past `check_after_idle`, or already `suspect`, are caught by the check with zero client error.
4. **Re-seeding runs only when a reset runs.** Under `lease.reset: on-switch`, consecutive leases by the same tenant skip both `DISCARD ALL` and `SELECT setseed(...)` by definition. With `lease.reseed: on`, the `random()` seed residue is gone across tenants and on every reset connection, and stays present within one tenant's same-connection reuse.
5. **No `TCP_USER_TIMEOUT` in v1.** A statement written into a blackholed socket mid-lease is bounded by `timeouts.statement` (30 s), not a Linux-specific socket retransmission timeout; either way, a write error after bytes were handed to the kernel still means "sent, delivery unknown" (rule G8).
6. **Simulator keepalive and idle-close fidelity.** In tier S, `pathRelay` models keepalive as suppressing `idle_drop` whenever `keepalive < idle_drop` rather than simulating kernel probe packets and probe loss (real options and kernel timers are checked in tier R). Because the engine transport exposes one close event on an idle connection, an idle FIN and an idle RST both close as `peer_closed_idle` without counting toward the breaker.
7. **Cancel connections sit outside the pool cap.** Up to `pool.max_cancels` (default 16) short-lived plaintext cancel connections may open concurrently against Postgres's reserved connection slots.
8. **Pool-admission interaction limits.** A half-open trial or cold-start dial that takes longer than the admission rule's allowed wait causes waiting requests to be shed with `53M01` while the dial still decides the pool or breaker. When a popped request cannot be lent immediately (because idle connections need a check or the breaker is half-open), `AdmissionQueue.Unpop` restores the queue slot and rule state while leaving the pop's served-wait sample recorded.
9. **Tier R coverage scope.** Database restart and synchronized lifetimes are verified in tier S rather than tier R (restarting the shared Postgres container would disrupt other tier R tests); heavy tier S scenarios run 3 seeds in sweeps, and other pool scenarios cap sweeps at 20 seeds.
