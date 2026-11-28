# 11. One read for a thousand callers

When a cache entry expires or a link goes viral, hundreds of clients ask Postgres for the same row at the same instant. Without request sharing, a connection pool protects Postgres from opening a thousand backend sessions, yet every one of those thousand requests still queues behind the pool, runs the same query, and waits for its own round trip.

In the deterministic simulator's viral hot-key scenario—1,000 clients sending the same 50 ms `SELECT` across 4 database connections—leaving sharing off forces **1,000** database reads and drives p99 latency to **12.65 s**. With the coalescer enabled on the same seed, those 1,000 clients trigger **2** database reads in strict mode (**101.7 ms** p99) and **1** database read in relaxed mode (**51.5 ms** p99, matching the **50.3 ms** direct baseline), while every client receives a complete Postgres reply.

Chapter 11 replaces the pass-through `Coalescer` seam with a full request-coalescing stage across five areas:

- [`internal/coalesce/table.go`](../../internal/coalesce/table.go), [`config.go`](../../internal/coalesce/config.go), [`lists.go`](../../internal/coalesce/lists.go), and [`snapshot.go`](../../internal/coalesce/snapshot.go): a pure **sans-I/O** (no clocks, sockets, goroutines, or floating point) flight table with strict and relaxed join modes, creation-ordered flight deadlines, waiter reference counting and handoff, fixed memory caps (`MaxFlights`, `MaxWaiters`, `MaxReplyBytes`, and a bounded FIFO no-share ring), per-tenant hit accounting, and opt-in Space-Saving hot-key tracking.
- [`internal/proxy/coalesce.go`](../../internal/proxy/coalesce.go) (plus one-line stage hooks across [`engine.go`](../../internal/proxy/engine.go), [`lease.go`](../../internal/proxy/lease.go), [`client.go`](../../internal/proxy/client.go), [`corepool.go`](../../internal/proxy/corepool.go), [`retry.go`](../../internal/proxy/retry.go), [`fairness.go`](../../internal/proxy/fairness.go), [`snapshot.go`](../../internal/proxy/snapshot.go), [`timers.go`](../../internal/proxy/timers.go), [`contracts.go`](../../internal/proxy/contracts.go), and [`matrix.go`](../../internal/proxy/matrix.go)): wires `CoreCoalescer` into `proxy.Engine` between the gates and the admission queue, stores each shared reply once as raw Postgres message bytes, hands off leases and queue slots when owners leave, composes flights with fairness (Chapter 9) and pre-forward retries (Chapter 12), and checks flight invariants on every step.
- [`internal/config/coalesce.go`](../../internal/config/coalesce.go) (plus hooks in [`config.go`](../../internal/config/config.go) and [`validate.go`](../../internal/config/validate.go)): fail-closed JSON configuration under `coalesce.*` (defaulting the binary to `mode: "auto"`, `join: "strict"`, `key_scope: "all-tenants"`), refusing `lease.reset: "on-switch"` with `key_scope: "all-tenants"` while coalescing is on, and updating [`docs/timeout-budget.md`](../../docs/timeout-budget.md), [`docs/capacity-table.md`](../../docs/capacity-table.md), [`docs/safe-shape.md`](../../docs/safe-shape.md), and [`docs/fault-matrix.md`](../../docs/fault-matrix.md).
- [`internal/obs/coalsrc/coalsrc.go`](../../internal/obs/coalsrc/coalsrc.go) (and [`enginesrc.go`](../../internal/obs/enginesrc/enginesrc.go), [`catalog.go`](../../internal/obs/catalog/catalog.go)): exports component `coalescer` metrics, engine coalescer counters, and the `coalescer` health check ([`docs/csv-schema.md`](../../docs/csv-schema.md)).
- [`test/e2e/c11_coalesce.go`](../../test/e2e/c11_coalesce.go), [`c11_join.go`](../../test/e2e/c11_join.go), [`c11_coalesce_test.go`](../../test/e2e/c11_coalesce_test.go), [`c11_tierr_test.go`](../../test/e2e/c11_tierr_test.go), and 27 DuckDB SQL assertion files under `test/analysis/`: 27 deterministic tier S scenarios (with replay checks and ablations) plus six live Postgres 17 tier R tests including the Zipf skew benchmark.

---

## Design: what may share, when to join, and how replies are bounded

A request coalescer in front of a database has to get four boundaries right: never sharing a write, never letting one caller observe stale data by default, never letting one caller's cancel abort another caller's read, and never letting a single hot key or huge result set exhaust proxy memory.

### Sharing never reads SQL

`internal/coalesce` imports `internal/work` and `internal/obs` only—neither `internal/pgwire` nor `pgproto3`. Eligibility rests strictly on the flags Chapter 5's classifier already placed on `work.Unit`:

- Under `coalesce.mode: "auto"` (the binary's default), a request joins the flight table only when `Unit.HasKey && Unit.Shareable` is true—meaning it arrived outside a transaction, passed the reject list, and matched the lexer-free safe shape ([`docs/safe-shape.md`](../../docs/safe-shape.md)). Writes and every statement inside a transaction never have `Shareable` set and pass straight through.
- Under `coalesce.mode: "hinted"`, a request joins only when `Unit.HasKey && Unit.Hinted` is true (its query begins with the exact leading bytes `/* mg:share */`), regardless of shape.
- Under `coalesce.mode: "off"`, nothing joins.

The flight key is `Unit.Key` (`work.ShareKey`), encoded by `keyOf` as an 8-byte big-endian tenant length, followed by the tenant bytes (empty under `key_scope: "all-tenants"`), followed by the exact SQL bytes. Prefixing the length guarantees that two different `(tenant, text)` pairs can never collide on one flight key even if either string contains NUL bytes (`0x00`).

### Strict versus relaxed joins

A **flight** is one shared database read owned by one request (the **owner** or **leader**) and waited on by zero or more **followers**. The coalesce stage sits directly after the draining, circuit-breaker, and rate gates and directly before the admission queue: followers have already cleared the gates and take neither a queue slot nor a database connection.

When a second eligible request arrives for a key that already has a flight, what happens depends on whether the first flight's query has been written to Postgres yet and on `coalesce.join`:

1. **Before the query is written (`stateQueued`):** in both modes, the arrival joins the queued flight (`VerdictFollow`).
2. **After the query is written (`stateRunning`):**
   - In **`strict` mode** (the default), the arrival never joins the read already running on the database. Instead, it starts—or follows—a single **held flight** (`stateHeld`, `VerdictHold`) waiting behind the running read. When the running read finishes, the coalescer promotes the held flight (`NoticeStart`) into the admission queue. Because the promoted read starts strictly after every write acknowledged before any waiter on that held flight arrived, **read-your-own-write** holds by construction: a client that updates a row and immediately reads it never sees the value read by an earlier in-flight query.
   - In **`relaxed` mode**, the arrival joins the running read (`VerdictFollow`). A burst that arrives while the read runs costs a single database read and roughly one service time, at the cost of a staleness window: a client can receive data from a read that started before its own acknowledged write.

**Why strict is the default, and why at most one held flight per key.** A single key entry holds at most two flights: `run` (queued or running) and `hold` (waiting behind `run`). A third arrival in strict mode follows `hold` rather than chaining a third flight. That bounds per-key state to two slots, guarantees at most two database reads for any burst shorter than one read time, and avoids surprising callers with stale reads unless the operator explicitly chooses `relaxed`.

### Reference counting, independent deadlines, and bounded memory

Four rules keep flights from leaking work or memory under cancels, slow clients, and oversized results:

- **Waiter reference counting and handoff.** If a follower cancels or disconnects, it leaves the flight quietly (`LeftFollower`); a cancel receives `ERROR 57014` (`cancelled-queued`, pre-forward for that caller) and nothing touches the database read. If the flight's *owner* leaves while followers remain, `Table.Leave` hands ownership in `O(1)` to the newest remaining follower (`LeftHandoff`) and the engine transfers the queue entry or active database lease to that client. Only when the *last* remaining waiter leaves (`LeftLast`) does the flight end and cancel the database work—routing a running connection through Chapter 7's dirty-connection table on the pool core.
- **One flight deadline, independent of every waiter.** Every flight's deadline is `now + timeouts.statement` from its creation (saturating at `maxInstant` so timestamps near `math.MaxInt64` never wrap). Because every flight uses the same duration, flights sit in a creation-ordered doubly linked list whose head is always the earliest expiry (`O(1)` `Next()` and expiry). At or past the deadline (obeying the engine's time-passes-before-event rule), every waiter receives one shared error: `53M01` if the flight was never sent, or the statement-deadline row (`08006` for a safe read, `08007` otherwise) if it was running.
- **Store once, cap bytes, and isolate slow followers.** Rather than streaming the database reply to the owner while trying to copy to followers, the engine buffers the reply's Postgres message bytes once on `req.stored` up to `coalesce.max_reply_bytes` (default `64 KiB`, validated to at most half of `limits.reply_buffer` so relay high-water bounds still hold). When `ReadyForQuery` arrives with idle status `'I'`, the owner receives the stored bytes plus the database's `ReadyForQuery`, and each follower receives a reference to that same stored slice plus a freshly built `ReadyForQuery 'I'`. Because nothing pauses the database read for a slow follower while storing, a stalled follower writes into its own bounded write queue and never delays its peers.
- **Four explicit memory caps.**
  1. `coalesce.max_waiters` (default `1,024`, owner included): arrivals past the cap are rejected immediately before acceptance with `ERROR 53M04` (`coalesce-cap`).
  2. `coalesce.max_reply_bytes` (default `64 KiB`): if a reply passes the cap mid-flight, the flight detaches—followers receive `ERROR 53M04` (nothing reached the database for them), the owner switches to normal back-pressured streaming of the buffered prefix and remainder, and the key enters the bounded FIFO **no-share list** (`coalesce.no_share_max`, default `256`, evicting oldest) for `coalesce.no_share_for` (default `10 s`). A hinted reply ending in a non-idle transaction status (such as a marked `BEGIN`) detaches the same way (`CapUnshareable`).
  3. `coalesce.max_flights` (default `1,024` distinct keys): when full, new keys pass unshared (`passed{reason="full"}`, `overflows{reason="flights"}`) rather than evicting live flights or rejecting traffic.
  4. `coalesce.hot_keys` (default `0`, off; max `64`): opt-in **Space-Saving** tracker (when full, a new key replaces the lowest-count slot and increments it), exported by rank only (`hot_key_hits{mode="<rank>"}`) so SQL text never enters metric labels. Per-tenant hit stats cap at `4,096` distinct tenants before folding into `other`.

### Composing flights with fairness and retries

Flights compose cleanly with Chapter 9's fair dispatcher and Chapter 12's pre-forward retry policy under three explicit rules:

- **Fair shares count leases, not answers.** A flight's owner occupies one place in its tenant's admission ring and holds one database lease counted against that tenant; followers hold neither. To keep lease starts and ends balanced across cross-tenant handoffs under `key_scope: "all-tenants"`, each request records the tenant it was queued under (`request.tenant`), `lend` stores it in `dbConn.lastTenant`, and every lease-end path reports `d.lastTenant` rather than the current client's tenant. A handed-off flight therefore stays charged to the tenant whose queue it left until its lease ends.
- **One pre-forward retry for the whole flight.** When `retry.policy` is `"pre-forward-once"` and a flight's statement fails with zero bytes accepted (`Reached == work.No`), `statementLost` checks `!e.flightGone(req)` (never retrying a flight whose flight deadline already fired) and offers the failure once for the whole flight. If granted, the owner re-enters its tenant queue (`rerun`), the waiters stay attached and receive the retry's outcome once, and the retry budget sees one first attempt per flight rather than one per follower. While the retry waits, the coalescer keeps the flight in `stateRunning`, so in strict mode new arrivals wait in a held flight behind it. Every request stores its immutable `arrived` instant at `startRequest` (distinct from `enq`, which promotion updates for queue-wait measurement), and held-flight handoffs copy `arrived` to the successor; a retried flight therefore ends at the earlier of its flight deadline and the original leader's `arrived + timeouts.statement`.
- **Promotions respect an open circuit breaker.** When the circuit breaker opens—either via a pool-core flush or a custom breaker seam—ending a running or queued-retry flight may emit `NoticeStart` for a strict-mode held flight behind it. `coalStart` checks `e.breakerOpen()` (open or half-open) and refuses the promoted flight and all its waiters immediately with `ERROR 53M03` (counted in `engine.coalesce_promoted_refused` as failed, never queued and never forwarded), so a breaker flush leaves zero queued work. Similarly, `coalStart` returns early once `e.stopping` is set so `Shutdown` never writes a new statement to Postgres.

---

## How it works: flight table, notice loop, and engine wiring

### Core join target and notice-driven state machine

The join decision and strict-vs-relaxed split live in `Table.Join` and `Table.joinTarget` in [`internal/coalesce/table.go`](../../internal/coalesce/table.go):

```go
func (t *Table) Join(now Instant, id RequestID, u *work.Unit) Joined {
	t.advance(now)
	if !t.eligible(u) {
		t.snap.NotEligible++
		return Joined{}
	}
	t.snap.Offered++
	key := keyOf(u.Key)
	t.hot.touch(key)
	if t.noShare.has(key) {
		t.snap.PassedNoShare++
		return Joined{}
	}
	if _, dup := t.members[id]; dup {
		t.snap.PassedDuplicate++
		return Joined{}
	}
	ei, ok := t.byKey[key]
	if !ok {
		if len(t.byKey) >= t.cfg.MaxFlights {
			t.snap.Overflows[CapFlights]++
			t.snap.PassedFull++
			return Joined{}
		}
		ei = t.newEntry(key)
		fi := t.newFlight(ei, id, stateQueued)
		t.entries[ei].run = fi
		return Joined{Verdict: VerdictLead, Flight: t.flights[fi].id}
	}
	target := t.joinTarget(ei)
	if target == none {
		fi := t.newFlight(ei, id, stateHeld)
		t.entries[ei].hold = fi
		return Joined{Verdict: VerdictHold, Flight: t.flights[fi].id}
	}
	f := &t.flights[target]
	if 1+len(f.followers) >= t.cfg.MaxWaiters && t.cfg.Plant&PlantNoWaiterCap == 0 {
		t.snap.Overflows[CapWaiters]++
		t.snap.Refused++
		return Joined{Verdict: VerdictRefuse, Flight: f.id}
	}
	f.followers = append(f.followers, id)
	t.members[id] = target
	t.snap.Hits++
	t.snap.WaitersIn++
	t.snap.Waiting.Add(1)
	t.tenants.hit(u.Tenant)
	return Joined{Verdict: VerdictFollow, Flight: f.id}
}

func (t *Table) joinTarget(ei int) int {
	e := t.entries[ei]
	if e.run != none {
		f := t.flights[e.run]
		running := f.state == stateRunning && t.cfg.Plant&PlantStrictJoinsRunning == 0
		if !f.detached && (t.cfg.Join == JoinRelaxed || !running) {
			return e.run
		}
	}
	return e.hold
}
```

Every `Table` method begins with `t.advance(now)`, expiring due flights and no-share entries at or before `now` before applying the event. Side effects affecting other requests—answering followers (`NoticeReply`, `NoticeError`, `NoticeRefused`, `NoticeExpired`), ending an expired owner (`NoticeExpireOwner`), or promoting a strict-mode held flight (`NoticeStart`)—are appended to `t.notices`.

### Engine notice drain, promotion, and invariants

In [`internal/proxy/coalesce.go`](../../internal/proxy/coalesce.go), `Engine.coalNotices` drains `Table.TakeNotice()` iteratively behind a `noticeBusy` re-entrancy guard (necessary because promoting a held flight into a full admission queue can shed another flight and enqueue more notices in the same step), then clears the flight payload cache `e.ends` in insertion order and arms a single engine timer `e.coalT` at `e.coal.Next()`.

When a queued flight hits its flight deadline (`coalExpireOwner`, `case cpQueued`), the engine removes the owner via `e.expireQueued(req.id)` (`admission.Queues.Expire`, counted as a deadline shed) rather than `RemoveAt`, keeping `admission.Queues.ShedCount() == Engine.Snapshot().Shed` exact. When a held owner leaves while still in `cpCoalesced`, `coalLeave` propagates its original `c.req.arrived` onto the new owner before draining notices:

```go
func (e *Engine) coalLeave(c *client, why coalesce.LeaveWhy) bool {
	wasFollower := c.phase == cpCoalesced
	l := e.coal.Depart(e.now, c.wid, why)
	e.coalUnfollow(c)
	if wasFollower {
		// A strict-mode held owner also has phase cpCoalesced and skips
		// coalHandoff; copy its original arrived timestamp to the successor so
		// retries after promotion remain bounded by the original leader's arrival.
		if l.Kind == coalesce.LeftHandoff {
			if nc := e.members[RequestID(l.NewOwner)]; nc != nil && nc.req != nil && c.req != nil {
				nc.req.arrived = c.req.arrived
			}
		}
		e.coalNotices()
		return true
	}
	req := c.req
	if l.Kind == coalesce.LeftHandoff {
		e.coalHandoff(c, e.members[RequestID(l.NewOwner)])
		e.coalNotices()
		return true
	}
```

Followers sit in client phase `cpCoalesced`, counted in `Engine.Snapshot().Coalesced` so engine conservation balances across every exit path including `Shutdown`:

```text
accepted = completed + failed + shed + queued + in_flight + coalesced
```

`Engine.Invariants` calls `coalCheck()` on every simulator step, checking `Table.Check()`, verifying that `e.coalT` is armed at `e.coal.Next()`, and matching every live client's phase, follower map entry, waiter entry, and ownership against the flight table. `Engine.Quiesce` requires zero flights, zero waiters, zero stored bytes, an empty no-share list, zero undrained notices, and no armed `tCoalesce` timer.

### Configuration and observability

In [`internal/config/coalesce.go`](../../internal/config/coalesce.go), the JSON `coalesce` block accepts `mode` (`"off"`, `"auto"`, `"hinted"`, default `"auto"`), `key_scope` (`"all-tenants"`, `"tenant"`, default `"all-tenants"`), `join` (`"strict"`, `"relaxed"`, default `"strict"`), `max_flights`, `max_waiters`, `max_reply_bytes`, `no_share_for`, `no_share_max`, and `hot_keys`. Zero or omitted selects the default; negative values and out-of-range bounds fail closed naming the exact key—even when `mode` is `"off"`—and `applyDefaults` writes the resolved `max_reply_bytes` back so omitted and explicit defaults produce the same config fingerprint. Combining `lease.reset: "on-switch"` with `coalesce.key_scope: "all-tenants"` is refused whenever coalescing is on (`"auto"` or `"hinted"`), because an unreset connection could otherwise hand one tenant's session state to a follower of another tenant; with `mode: "off"` or `key_scope: "tenant"` the combination is valid.

[`internal/obs/coalsrc/coalsrc.go`](../../internal/obs/coalsrc/coalsrc.go) maps every `coalesce.Snapshot` field into component `coalescer` (`offered`, `flights_started`, `hits`, `refused`, `passed{reason}`, `held`, `promoted`, `sent`, `flight_ends{outcome}`, `waiter_ends{outcome}`, `overflows{reason}`, `shared_errors`, `handoffs`, `no_share*`, gauges and peaks, `hot_key_hits{mode="<rank>"}`, plus `waiters_per_flight` and `tenant_hits` under `obs.detail`). Its health rule always reports `ok` with the current waiting count: per-request `53M04` cap refusals and unshared pass-throughs do not degrade the instance.

---

## How it is tested and what the tests proved

### Core reference model, key injectivity, canaries, and fuzzing

[`internal/coalesce/ref_test.go`](../../internal/coalesce/ref_test.go) implements an independent slice-based reference model (`refModel`) checking every verdict, notice, conservation law, `Running(key) <= 1`, and `Quiesce` across `auto`/`hinted`/`off` and `strict`/`relaxed`. `TestFlightTableAgreesWithReferenceModel` drives 400 random 90-operation sequences and confirms every major counter moves; `TestPlantedBugsAreCaughtByThePropertyTest` verifies that each of the six planted bugs (`PlantStrictJoinsRunning`, `PlantAnyLeaveCancels`, `PlantLeaverGetsResult`, `PlantNoWaiterCap`, `PlantNoReplyCap`, `PlantSafeReadDoor`) fails the property test. `key_test.go` proves `keyOf` injectivity over 20,000 random byte pairs (including `0x00` and `0xff`) and via `FuzzShareKeyIsUnambiguous`, plus saturating deadline arithmetic near `maxInstant`.

```sh
$ go test -count=1 -v ./internal/coalesce ./internal/obs/coalsrc
=== RUN   TestDifferentShareKeysNeverMapToOneFlightKey
--- PASS: TestDifferentShareKeysNeverMapToOneFlightKey (0.04s)
=== RUN   TestTenantAndTextWithAZeroByteStartTwoFlights
--- PASS: TestTenantAndTextWithAZeroByteStartTwoFlights (0.01s)
=== RUN   TestADeadlineNearTheLargestInstantSaturates
--- PASS: TestADeadlineNearTheLargestInstantSaturates (0.00s)
...
=== RUN   TestFlightTableAgreesWithReferenceModel
--- PASS: TestFlightTableAgreesWithReferenceModel (1.05s)
=== RUN   TestPlantedBugsAreCaughtByThePropertyTest
--- PASS: TestPlantedBugsAreCaughtByThePropertyTest (0.02s)
=== RUN   TestReplayIsBitIdentical
--- PASS: TestReplayIsBitIdentical (0.21s)
...
=== RUN   TestStrictJoinsTheNextFlightRelaxedJoinsTheRunningOne
--- PASS: TestStrictJoinsTheNextFlightRelaxedJoinsTheRunningOne (0.00s)
=== RUN   TestOnlyShareableUnitsJoinUnderAutoAndOnlyHintedUnderHinted
--- PASS: TestOnlyShareableUnitsJoinUnderAutoAndOnlyHintedUnderHinted (0.00s)
=== RUN   TestResultAtTheFlightDeadlineIsLate
--- PASS: TestResultAtTheFlightDeadlineIsLate (0.00s)
=== RUN   TestLeaveAtTheInstantTheResultArrivesGetsOnlyTheLeaversOwnError
--- PASS: TestLeaveAtTheInstantTheResultArrivesGetsOnlyTheLeaversOwnError (0.00s)
=== RUN   TestNinetyNineLeaveAndTheLastGetsTheReply
--- PASS: TestNinetyNineLeaveAndTheLastGetsTheReply (0.00s)
=== RUN   TestTheLastLeaverCancelsTheFlight
--- PASS: TestTheLastLeaverCancelsTheFlight (0.00s)
=== RUN   TestWaiterCapRefuses
--- PASS: TestWaiterCapRefuses (0.00s)
=== RUN   TestReplyCapRefusesFollowersAndListsTheKeyForAWhile
--- PASS: TestReplyCapRefusesFollowersAndListsTheKeyForAWhile (0.00s)
...
PASS
ok  	github.com/nickstrad/mini-gateway/internal/coalesce	1.334s
=== RUN   TestMappingExportsEveryFieldOfTheCoalescerSnapshot
--- PASS: TestMappingExportsEveryFieldOfTheCoalescerSnapshot (0.00s)
=== RUN   TestCountersMoveAndConserveThroughTheMapping
--- PASS: TestCountersMoveAndConserveThroughTheMapping (0.00s)
=== RUN   TestDetailOffExportsNoTenantRows
--- PASS: TestDetailOffExportsNoTenantRows (0.00s)
=== RUN   TestHealthStatesTheCoalescerNeverDegradesTheInstance
--- PASS: TestHealthStatesTheCoalescerNeverDegradesTheInstance (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/obs/coalsrc	0.009s
```

`FuzzCoalesceOps` decodes raw bytes into bounded 3-byte operations (capped at 96 operations, no hash-derived seed); [`scripts/fuzz-plant.sh`](../../scripts/fuzz-plant.sh) verifies it catches `PlantLeaverGetsResult` within the 20 s gate budget while the unplanted target runs clean.

Engine-level tests in [`internal/proxy/coalesce_test.go`](../../internal/proxy/coalesce_test.go), [`coalesce_repair_test.go`](../../internal/proxy/coalesce_repair_test.go), [`join_coalesce_test.go`](../../internal/proxy/join_coalesce_test.go), [`join_coalesce_shed_test.go`](../../internal/proxy/join_coalesce_shed_test.go), [`join_repair_test.go`](../../internal/proxy/join_repair_test.go), and [`join_repair_ops_test.go`](../../internal/proxy/join_repair_ops_test.go) prove shared byte delivery, strict holding, owner handoffs on both `CorePool` and `FixedSet`, default-rule `RuleTimeout` and full-queue sheds with followers, cross-tenant lease-charge preservation, retried flights and arrival-bounded deadlines across single and repeated handoffs, open-breaker promotion refusals on both pool-core flush and custom breaker seams, `Shutdown` sending nothing new, and `Engine.Invariants` catching corrupted follower/owner/timer state. `TestRepairRandomFlightsRetriesDeadlinesAndBreakerTransitionsKeepTheInvariants` runs 400 randomized 160-operation engine sequences exercising flights, retries, handoffs, deadlines, and breaker transitions together, asserting `Engine.Invariants`, arrival-bounded retry delivery, per-event open-breaker promotion refusal, and `Quiesce` on every sequence.

### Tier S scenarios, hot-key numbers, and DuckDB SQL assertions

All 27 tier S coalescer scenarios (`CoalesceScenarios()`) run the full stack—coalescer, admission queues (timeout rule sized so the 1,000-read ablation arm does not shed), fair dispatcher, and pool core—with replay determinism checks (bit-identical simulator digests and byte-identical flushed CSVs) and ablations (disabling coalescing, enabling a core plant, or disabling retries) that must fail the scenario's check.

Running `TestCoalesceHotKeyNumbers` on seed 1 shows the 1,000-client hot-key latency and read-count split directly (50 ms read time, 4 connections, 1,000 clients arriving 3 µs apart):

```sh
$ MG_C11_HEAVY=1 go test -count=1 -v -run '^TestCoalesceHotKeyNumbers$' ./test/e2e
=== RUN   TestCoalesceHotKeyNumbers
    c11_coalesce_test.go:103: HEADLINE tier S hot key strict: 1000 clients, 2 database reads, p50 100.240003ms, p99 101.705708ms, max 101.756366ms
    c11_coalesce_test.go:103: HEADLINE tier S hot key relaxed: 1000 clients, 1 database reads, p50 49.997908ms, p99 51.47203ms, max 51.529475ms
    c11_coalesce_test.go:103: HEADLINE tier S hot key off: 1000 clients, 1000 database reads, p50 6.380152736s, p99 12.652920921s, max 12.756717031s
    c11_coalesce_test.go:103: HEADLINE tier S hot key direct: 1000 clients, 1000 database reads, p50 50.261668ms, p99 50.294794ms, max 50.298607ms
--- PASS: TestCoalesceHotKeyNumbers (10.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	10.015s
```

Running representative tier S scenarios on seed 1 and validating their flushed CSV files through **DuckDB** (an embedded analytical SQL engine):

```sh
$ RUN_DIR=/tmp/opencode/c11-prose-run MG_C11_HEAVY=1 DST_SEEDS=1 go test -count=1 -v \
    -run '^TestScenarioCoalesce/coalesce-(hot-key-strict|hot-key-relaxed|read-your-write-strict|read-your-write-relaxed|cancel-99-strict|vanish-all-strict|shared-error-strict|caps-strict|hot-key-weighted-strict|hot-key-retry-strict)$' \
    ./test/e2e -seed=1
--- PASS: TestScenarioCoalesce (0.02s)
    --- PASS: TestScenarioCoalesce/coalesce-caps-strict (7.53s)
    --- PASS: TestScenarioCoalesce/coalesce-hot-key-retry-strict (8.03s)
    --- PASS: TestScenarioCoalesce/coalesce-hot-key-weighted-strict (11.02s)
    --- PASS: TestScenarioCoalesce/coalesce-read-your-write-relaxed (4.44s)
    --- PASS: TestScenarioCoalesce/coalesce-read-your-write-strict (6.52s)
    --- PASS: TestScenarioCoalesce/coalesce-shared-error-strict (7.45s)
    --- PASS: TestScenarioCoalesce/coalesce-vanish-all-strict (7.35s)
    --- PASS: TestScenarioCoalesce/coalesce-hot-key-strict (25.61s)
    --- PASS: TestScenarioCoalesce/coalesce-cancel-99-strict (7.39s)
    --- PASS: TestScenarioCoalesce/coalesce-hot-key-relaxed (22.77s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	30.857s

$ scripts/csv-assert.sh /tmp/opencode/c11-prose-run/coalesce-hot-key-strict test/analysis/coalesce-hot-key-strict/*.sql
NOTE: v1.5.5 (Variegata) d8cdaa33fd; 3 run directories under /tmp/opencode/c11-prose-run/coalesce-hot-key-strict; 1 SQL files
ok: test/analysis/coalesce-hot-key-strict/reads.sql over 3 runs
csv-assert: 1 SQL files run, 0 failures; v1.5.5 (Variegata) d8cdaa33fd
```

The 27 scenarios cover every core property and its ablation:

- **Hot key (`coalesce-hot-key-{strict,relaxed}`):** 1–2 reads with `MaxConcurrent = 1` vs 1,000 reads off; [`reads.sql`](../../test/analysis/coalesce-hot-key-strict/reads.sql) joins the base and ablation arms across flushed CSVs and checks `flights_started + hits = offered` and the `waiters_per_flight` histogram totals.
- **Writes and transactions (`coalesce-writes-*`, `coalesce-marked-write-hinted`, `coalesce-hinted`, `coalesce-tenant-scope`):** every write appears in the fake's write log exactly once and every in-transaction read reaches Postgres under `auto`; `marked-write-hinted` proves the write check catches shared marked writes under `hinted`; `hinted` shares only marked queries; `tenant-scope` isolates 4 tenants into 4–8 reads.
- **Cancels and vanishes (`coalesce-cancel-99-*`, `coalesce-cancel-all-*`, `coalesce-vanish-all-*`):** cancelling 99 of 100 delivers `57014` to the 99 and a valid reply to the 100th; cancelling or vanishing all 100 aborts the read (`57014`) on Postgres and cleans up the connection via the dirty-connection table (`lease_ends{vanished-mid-statement}`), followed by recovery and quiesce.
- **Read-your-own-write (`coalesce-read-your-write-{strict,relaxed}`):** strict returns `'new'` after the client's acknowledged `UPDATE`; relaxed shows the stale `'v1'` window; enabling `PlantStrictJoinsRunning` fails the strict scenario.
- **Slow followers, shared errors, caps, and direct equality (`coalesce-slow-follower-*`, `coalesce-shared-error-*`, `coalesce-caps-*`, `coalesce-results-*`):** 3 stalled followers behind a 40 KiB read do not delay 17 fast peers; a 100-client timeout shares one error (`08006` or `53M01`) per client in `<= 2` reads; `MaxWaiters` and `MaxReplyBytes` fire `53M04` and expire the no-share list; proxied replies equal direct execution.
- **Fairness and retry composition (`coalesce-hot-key-weighted-*`, `coalesce-hot-key-retry-*`):** 200 readers across two 3:1 weighted tenants saturated by 16 writers complete in 1–2 reads while fairness holds shares binding; 200 readers arriving after every idle connection is doomed trigger one budgeted retry for the whole flight (`attempts = 4 warm-ups + flights sent`), answering every waiter with the value and zero shared errors.

### Tier R against live Postgres 17 and the Zipf benchmark

[`test/e2e/c11_tierr_test.go`](../../test/e2e/c11_tierr_test.go) runs the compiled `mini-gateway` binary against Postgres 17 using views over `pg_sleep` (whose query text fits the safe shape) and counts completed database reads via `pg_stat_statements.calls`:

- **Hot key (`TestTierRCoalesceHotKey`):** 1,000 clients connected before a barrier, 20 pool connections, 500 ms read time:

| Arm | Clients | Database reads (`pg_stat_statements`) | p50 | p99 |
| --- | ---: | ---: | ---: | ---: |
| `strict` | 1,000 | 1 | 569 ms | 594 ms |
| `relaxed` | 1,000 | 1 | 550 ms | 563 ms |
| `off` | 1,000 | 1,000 | 13.85 s | 26.12 s |
| `direct` (container limit 200) | 100 | 100 | 541 ms | 563 ms |

  Strict produced 1 read in tier R because all 1,000 arrivals queued before the cold pool's first connection opened and joined that queued flight.
- **Cancel 99 of 100 / all 100 cancel / read-your-own-write / results equal direct:** `TestTierRCoalesceCancel99` completes 1 read for the surviving waiter while 99 receive `57014`; `TestTierRCoalesceAllCancel` forwards 1 cancel after 99 coalesced cancels so 0 reads complete; `TestTierRCoalesceReadYourWrite` returns `'new'` in strict and demonstrates the `'old'` window in relaxed; `TestTierRCoalesceResultsEqualDirect` matches direct Postgres 17 output with `auto` enabled.
- **Headline benchmark 2 — Zipf skew (`TestTierRCoalesceHeadlineZipf`):** 100 closed-loop clients running 30 reads each (3,000 requests total) over 200 keys drawn from `randdist.NewZipf` with exponent `s = 1.100` (seed 11; 20 pool connections, 10 ms read view). The generator actually produced 656 reads on the hottest key (21.9%), 58.1% across the top 10 keys, and 192 of 200 keys used:

| Arm | Requests | Database reads | Share of off | p50 | p99 |
| --- | ---: | ---: | ---: | ---: | ---: |
| `off` | 3,000 | 3,000 | 100.0% | 106 ms | 305 ms |
| `strict` | 3,000 | 1,235 | 41.2% | 67 ms | 196 ms |
| `relaxed` | 3,000 | 1,102 | 36.7% | 57 ms | 171 ms |

  Because this is a single unrepeated wall-clock run per arm without an A/A noise floor, the read counts are exact while the p50/p99 latencies are indicative only.

---

## Stated limits

1. **"At most one in-flight read per key" holds among flights the coalescer owns.** Three cases sit outside owned flights, and each is counted:
   - a read whose last waiter left (`LeftLast`) and whose asynchronous `CancelRequest` has not yet finished on Postgres (`coalescer.flight_ends{outcome="cancelled"}`; a fresh arrival can start a new flight meanwhile);
   - a key on the no-share list (`coalescer.passed{reason="no-share"}`), while an earlier oversized read of the key may still be streaming;
   - a request arriving while the flight table is at `coalesce.max_flights` (`coalescer.passed{reason="full"}`, `coalescer.overflows{reason="flights"}`), which passes unshared.
2. **At one instant, event order decides what a leaving waiter gets.** If a flight's result is applied before a waiter's leave at the same virtual instant, that waiter receives the reply and the late leave increments `coalescer.left_unknown`. If the leave is applied first, the waiter receives none of the result—only `57014` for a cancel or nothing for a gone client.
3. **Default sharing trusts the database objects named in a safe-shape query.** Because `coalesce.mode` defaults to `auto` (`strict`, `all-tenants`), a view, a user-defined operator, a function shadowing an allow-listed name, or a row-level security policy that hides a side effect or a per-call result is shared like any other read unless the operator sets `coalesce.mode` to `off` or `hinted`. Under `hinted`, any statement carrying `/* mg:share */` is shared, writes included.
4. **Cross-tenant riding and handoff keep-alive under `all-tenants`.** The fair dispatcher bounds database leases, not answers. With `coalesce.key_scope: "all-tenants"`, followers of other tenants ride a flight without taking a queue slot or spending their tenant's share. If the owner of a shared read leaves (cancel or disconnect) while a follower of another tenant remains, handoff (`LeftHandoff`) keeps that read—and any zero-byte-loss retry of it—alive on the leaving tenant's share until the lease ends, whereas without that follower the proxy would have cancelled the read (`LeftLast`) and freed the share. Set `coalesce.key_scope: "tenant"` for strict per-tenant isolation.
5. **One retry per flight, bounded by the original leader's arrival.** When retries are enabled, a shared read lost with zero bytes accepted before reaching Postgres is retried once for all its waiters, bounded by the original leader's arrival instant (`arrived + timeouts.statement`, carried across strict-mode promotions and repeated handoffs) and the flight deadline. A flight whose flight deadline has already fired is never offered a retry. While a retry waits in the queue, the flight stays `stateRunning`, so strict-mode arrivals wait in a held flight behind it rather than joining the retry.
6. **Held flights promoted at an open or half-open breaker are refused.** If a strict-mode held flight is promoted while the circuit breaker is open or half-open, it is refused immediately with `53M03` for its owner and every waiter (counted in `engine.coalesce_promoted_refused` as failed rather than shed), even when a half-open breaker would have let a fresh arrival start a trial.
7. **Vanished clients on real TCP sockets are noticed only at reply time.** The real goroutine shell pauses reading a client socket while a request is in progress (decision D5), so a client that resets its TCP connection mid-flight stays a waiter until the reply or flight deadline; only a `CancelRequest` makes a client leave a flight early on real sockets. (Tier S delivers socket resets immediately and exercises the vanish-all dirty-table path.)
8. **Strict latency cost versus relaxed staleness.** Relaxed mode can return data from a read started before the caller's own acknowledged write; strict mode eliminates that window at the cost of up to one extra read time.
9. **Flights are per instance.** Flights are not coordinated across a fleet of `N` proxy instances, so one read per hot key weakens to at most `N` reads per burst (measured in the fleet chapter).
10. **Opt-in hot-key metrics export rank counts only.** Enabling `coalesce.hot_keys` (up to `64`) records statement text in the in-process snapshot but exports only `hot_key_hits{mode="<rank>"}` so arbitrary SQL text never enters Prometheus or CSV labels.
