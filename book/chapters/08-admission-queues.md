# 8. Two CoDels, measured against each other

A bounded first-in, first-out (FIFO) queue protects a proxy's memory, not its clients' latency. When requests arrive faster than Postgres can execute them, a FIFO queue fills to its cap and stays full. Every request that eventually reaches the database sits behind the full queue first—a **standing queue** that adds fixed waiting time to every reply (**bufferbloat**). Callers time out and retry, while the proxy spends its database connections serving requests whose callers already gave up.

Under **decision D3** ([PLAN.md](../../PLAN.md#3-design-decisions-this-plan-makes)), waiting happens in one place only: the admission queues. There is no second hidden queue inside the connection pool. Chunk 8 replaces the walking skeleton's single `FIFO` queue behind the `Queue` seam with per-tenant bounded admission queues, two selectable shedding rules, newest-first service during overload, and a reproducible benchmark that puts both rules beside two baselines under identical simulated traffic:

- [`internal/admission`](../../internal/admission): a pure, **sans-I/O** (no clocks, sockets, goroutines, or floating point) core with per-tenant fixed-capacity ring buffers, two product rules (`TimeoutRule` and `ControlLaw`) behind one `QueueRule` interface, two simulator-only comparison arms (`fifoRule` and `FollyRule`), adaptive **LIFO** (last-in, first-out) with a head sweep and hold **hysteresis** (delaying the switch back so load near the threshold does not flap), a `Dispatcher` seam, and a 0–100 minimum-delay load gauge.
- [`internal/proxy/admission.go`](../../internal/proxy/admission.go) and [`internal/proxy/seams.go`](../../internal/proxy/seams.go): `AdmissionQueue` and the additive `ShedQueue` interface wired into `proxy.Engine` with one coalesced admission timer (`tAdmission`), immediate `ERROR 53M01` replies on shed, and full engine-to-queue conservation.
- [`internal/config/queue.go`](../../internal/config/queue.go): fail-closed JSON configuration for `queue.rule`, `queue.target`, `queue.interval`, `queue.tenant_capacity`, `queue.max_tenants`, `queue.lifo`, and `queue.lifo_hold`, plus rows in [`docs/timeout-budget.md`](../../docs/timeout-budget.md) and [`docs/capacity-table.md`](../../docs/capacity-table.md).
- [`internal/obs/admsrc`](../../internal/obs/admsrc/admsrc.go): component `admission` observability export (totals always; per-tenant metrics and histograms under `obs.detail`) and the `admission` health rule (`degraded` while any queue shed within the last interval).
- [`test/e2e/c08_admission.go`](../../test/e2e/c08_admission.go), [`test/e2e/c08_admission_test.go`](../../test/e2e/c08_admission_test.go), [`test/e2e/c08_tierr_test.go`](../../test/e2e/c08_tierr_test.go), [`test/analysis/admission-head-to-head/head-to-head.query`](../../test/analysis/admission-head-to-head/head-to-head.query), and [`scripts/admission-head-to-head.sh`](../../scripts/admission-head-to-head.sh): tier S overload, anti-bufferbloat, recovery, first-burst, and canary scenarios; 15 DuckDB SQL assertions; tier R real-Postgres overload; and one DuckDB query that generates the head-to-head table from flushed CSV files.

**What is not built yet.** On this branch, the proxy still holds Chapter 6's fixed set of database connections (`FixedSet`); the dynamic pool and circuit breaker belong to Chapter 7. Each tenant already gets its own queue ring and rule state, but the default dispatcher (`OldestFirst`) still picks whichever tenant has the oldest waiting request—per-tenant fair sharing arrives in Chapter 9 on the `Dispatcher` seam, and the adaptive concurrency limit and rate gate arrive in Chapter 10.

---

## Design: why "CoDel" is three different algorithms

**CoDel** (Controlled Delay) is often cited as a single algorithm, but the literature behind request-queue admission describes three distinct mechanisms ([INFLUENCES.md](../../INFLUENCES.md), entries F1–F4):

1. **The timeout rule (`timeout`, default).** Described in Ben Maurer's "Fail at Scale", it stores one timestamp per queue: `LastEmpty`, updated whenever a request arrives at an empty queue and whenever the queue empties. If the queue has remained non-empty for at least `queue.interval` (default `100ms`), a standing queue exists and the allowed wait tightens to `queue.target` (default `5ms`); otherwise every waiting request is allowed `queue.interval`. Any request whose age reaches or passes the current allowed wait is shed at once.
2. **The RFC 8289 control law (`control-law`).** Designed for network routers, it watches the **sojourn time** (time spent waiting in the queue) of the oldest item. If sojourn stays at or above `queue.target` for a full `queue.interval`, the queue enters its dropping phase, drops the head, and schedules the next drop after `floor(interval / sqrt(count))`, incrementing `count` on each drop (capped at `MaxDropCount = 65,536`). Leaving and re-entering dropping within 16 intervals keeps `count - lastCount` so repeated congestion resumes near the prior drop rate.
3. **folly's dequeue detector (`folly`, simulator-only).** Facebook's `folly::Codel` tracks the minimum queueing delay observed at dequeue over each `interval`. If the previous interval's minimum exceeded `target`, subsequent dequeued items that waited longer than `2 × target` are shed (never shedding the first sample of an interval).

**Why ship both `timeout` and `control-law`, and keep `fifo` and `folly` out of the binary.** The timeout rule bounds worst-case wait under heavy **open-loop** overload (traffic whose arrival rate does not slow down when the server stalls), whereas the RFC 8289 control law assumes senders back off after drops and ramps its drop rate only as `√count`. Under moderate overload, however, the control law can converge around `target` with gentler shedding. Rather than guess between them, `internal/admission` puts both behind `QueueRule` as equal, config-selectable rules and measures them against each other—and against plain `fifo` and `folly`—in the same deterministic simulator. `ParseProductRule` ([`internal/admission/config.go`](../../internal/admission/config.go)) accepts only `"timeout"` and `"control-law"`, so binary configs fail closed on `"fifo"` and `"folly"`.

**Why exact 128-bit integer arithmetic instead of floating point or Newton steps.** `internal/admission` is on `tools/lint-rules/det-packages.txt` (decision D2: no floating point, clocks, or concurrency). Rather than port Linux's reciprocal-square-root Newton approximation—whose rounding depends on prior drop history—`IntervalOverSqrt` ([`internal/admission/rule.go`](../../internal/admission/rule.go)) binary-searches for the largest integer `d` such that `d² × count ≤ interval²` using `math/bits.Mul64` 128-bit products. That computes `floor(interval / sqrt(count))` exactly for every count up to `65,536` and every interval up to `MaxInterval` (`60s`), verified exhaustively against `math/big`.

**Why adaptive LIFO needs both a head sweep and a time hold.** When a queue stays overloaded, serving newest-first (LIFO) lets fresh requests succeed within milliseconds instead of serving stale ones whose callers may have already timed out. Turning on LIFO without sweeping the head is a classic trap: stale entries stay buried at the front of the ring while fresh entries enter and leave at the back. Pairing LIFO with folly's detector is a second trap: because folly reads the wait of *dequeued* items, LIFO feeds it fresh waits and blinds the detector completely. Here, both product rules inspect the *oldest* item at the ring head and run a head sweep before every operation and at every rule deadline, so LIFO never buries an expired entry. To keep load hovering near the threshold from flapping between FIFO and LIFO, `observeLIFO` switches LIFO on as soon as the rule reports overload and switches it back off only after `queue.lifo_hold` (default `1s`) with no overload—avoiding HHVM's queue-length threshold, which depends on service time.

---

## How it works: rings, exact deadlines, and engine wiring

### Per-tenant rings and fixed memory

Each tenant gets a fixed-capacity circular buffer (`ring`, [`internal/admission/ring.go`](../../internal/admission/ring.go)) of `queue.tenant_capacity` entries (default `min(256, queue.capacity)`), created on first push and returned to a free list after 16 quiet intervals empty (`1.6s` with default `100ms` interval) once LIFO is off. Total queued items across all tenants are capped by `queue.capacity` (default `256`), and live tenant rings are capped by `queue.max_tenants` (default `1,024`).

Every overflow sheds stale work rather than fresh work:

- **Tenant ring full (`overflow`):** the tenant's oldest entry at the ring head is popped and shed; the new arrival enters the tail.
- **Total capacity full (`total`):** the globally oldest entry across all live rings (lowest arrival sequence number) is popped and shed.
- **Tenant cap reached (`tenants`):** the oldest empty tenant queue is evicted and its ring reused; only if every live tenant queue is non-empty is the new arrival refused.

One subtle boundary matters for a 1-slot ring (`tenant_capacity = 1`) or when total-cap overflow sheds the arriving tenant's own sole item: `Push` records `wasEmpty := t.empty()` *before* shedding the overflowing head and never calls `emptied` on the arriving tenant during overflow, so a continuously flooded 1-slot ring still reaches its standing or dropping state instead of resetting on every arrival.

### Time passes before the event

Both product rules follow the engine's deadline law: at every `Push`, `Pop`, `Remove`, `Expire`, `Sweep`, or `Close` call with timestamp `now`, `Queues.advance(now)` steps every internal deadline due at or before `now` at its own exact instant before running the event. Equality counts as expired.

The entire timeout rule fits in a few lines of [`internal/admission/rule.go`](../../internal/admission/rule.go):

```go
func (r *TimeoutRule) standing(now Time) bool { return now-r.LastEmpty >= r.Interval }

func (r *TimeoutRule) Allowed(now Time) Time {
	if r.standing(now) {
		return r.Target
	}
	return r.Interval
}

func (r *TimeoutRule) SweepHead(now, oldest Time) bool { return now-oldest >= r.Allowed(now) }

func (r *TimeoutRule) Next(oldest Time) Time { return max(r.LastEmpty+r.Interval, oldest+r.Target) }
```

Because `Allowed(now)` is evaluated at the current instant on the tenant queue as a whole rather than frozen per request at enqueue, the head of the ring is always the most expired entry—sweeping from the head until `SweepHead` returns false guarantees no older or younger item lingers past its allowed wait. Updating `LastEmpty` on `Pushed(now, wasEmpty)` prevents the classic first-burst bug where an idle queue wakes up already appearing to have stood for an interval and immediately applies the 5 ms target.

For `ControlLaw`, `Next(oldest)` returns `DropNext` while dropping, `FirstAbove` while timing a sojourn above `Target`, or `oldest + Target` otherwise, so the RFC 8289 state machine runs on timer wakeups at the head even when no backend connection is freeing to pop. Its integer schedule computes `floor(interval / sqrt(count))` with 128-bit checks in `IntervalOverSqrt`:

```go
func IntervalOverSqrt(interval Time, count int64) Time {
	if count <= 1 {
		return interval
	}
	i := uint64(interval)
	c := uint64(count)
	ih, il := bits.Mul64(i, i)
	lo, hi := uint64(0), i
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if fitsUnder(mid, c, ih, il) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return Time(lo)
}
```

### Wiring into `proxy.Engine`, config, and observability

In [`internal/proxy/seams.go`](../../internal/proxy/seams.go), `ShedQueue` extends `Queue` additively (`RemoveAt`, `Expire`, `Sweep`, `Next`, `TakeShed`, `Unpop`, `Close`) so Chapter 6's `FIFO` stays untouched as the control arm when `Config.Admission.Rule == 0` (the binary always sets a product rule, defaulting to `timeout`). Four engine details hold the seams together:

- **Single coalesced timer and immediate replies.** After every queue operation, `Engine.queueTouched` ([`internal/proxy/admission.go`](../../internal/proxy/admission.go)) drains `TakeShed()`, cancels each shed client's outer `timeouts.queue_wait` timer, and immediately writes `ERROR 53M01` followed by `ReadyForQuery 'I'` (connection stays usable; zero bytes reached Postgres—[`docs/error-contract.md`](../../docs/error-contract.md)). It then re-arms the engine's single `tAdmission` timer to `Next()`, which coalesces head sweeps, LIFO hold expiry, shedding-gauge decay, and quiet-tenant removal.
- **No direct-lend bypass.** When `e.shedQ != nil`, `Engine.enqueue` skips the direct-lend shortcut so every request passes through `Push` and `Pop`, letting the rule and the 0–100 load gauge observe zero-wait requests too.
- **`Unpop` preserves deadlines.** If a pool refuses checkout after `Pop`, `Unpop` returns the entry to the exact end of its ring it left and restores any `emptyAt` and rule state cleared when that pop emptied the ring.
- **Ordered shutdown.** `Engine.Shutdown` calls `sweepAdmission()` at `now` first (answering and counting any sheds already due), closes clients (removing remaining queued requests as `removed`), and then calls `closeAdmission()` to drop empty tenant queues and cancel `tAdmission` so `Engine.Quiesce()` passes with no pending timer and `q.ShedCount() == e.snap.Shed`.

`internal/obs/admsrc` registers component `admission` on the observability spine ([`docs/csv-schema.md`](../../docs/csv-schema.md)): totals (`queued`, `tenants`, `pushes`, `served`, `removed`, `unpops`, `shed{reason}`, `oldest_age`, `lifo_tenants`, `lifo_switches{mode}`, `rule_states{mode}`, `load`, `shedding`) are always emitted; `queue_wait` histograms and `tenant_*` rows emit when `obs.detail` is on. Per-tenant counters outlive removed rings so CSV counters never decrease, up to `MaxTenantStats = 4,096` tenants (folding later tenants—and any real tenant named `"other"`—into one `"other"` row); Prometheus folds tenants past 32 into `"other"`. The `admission` health rule reports `degraded` (never `failed`) while any tenant queue shed within the last `queue.interval`, and the core arms a wake at `shedAt + interval` so health recovers to `ok` without requiring new traffic.

---

## The head-to-head table: two CoDels and two baselines

The centre of this chunk is a direct comparison of all four arms—`timeout`, `control-law`, `fifo` (plain bounded ring, LIFO off), and `folly` (with adaptive LIFO)—at **1.2×**, **2×**, and **10×** offered load in the deterministic simulator ([`test/e2e/c08_admission.go`](../../test/e2e/c08_admission.go)).

Each run puts 100 open-loop clients (`workload.ArrivalConstant`, evenly spread arrivals) in front of the real `proxy.Engine` with 1 backend connection, 5 ms read service time (`fakepg.Service.PerKind` on `KindRead` so `DISCARD ALL` is not also charged 5 ms), a 32-entry tenant ring (`tenant_capacity = 32`), and default admission parameters (`target = 5ms`, `interval = 100ms`, `lifo_hold = 1s`). Including the unconditional `DISCARD ALL` reset and simulated network hops, one lease takes roughly **5.5 ms**, giving a measured capacity of **~181 leases/s** (about 363 leases in a 2 s overload phase).

Every run flushes `metrics.csv`, `histograms.csv`, and `run.csv`. One DuckDB (**DuckDB** is an embedded analytical SQL engine run over the flushed CSV files) query—[`test/analysis/admission-head-to-head/head-to-head.query`](../../test/analysis/admission-head-to-head/head-to-head.query), invoked by [`scripts/admission-head-to-head.sh`](../../scripts/admission-head-to-head.sh)—builds the entire 12-row table from those flushed files in a single pass:

1. A `phase` CTE reads `served`, total `shed`, rule-only `shed_rule` (`reason = 'rule'`), and `queued_peak` from `metrics` at the exact end of the 2 s overload phase (`t_ns = 2000000000`).
2. A `pct` CTE computes cumulative counts over `final_hist` for `admission.queue_wait`, taking the smallest bucket upper bound reaching 50% (`wait_p50_ms`) and 99% (`wait_p99_ms`), plus `max(bucket_hi)` (`wait_max_ms`), converted from nanoseconds to milliseconds.
3. The final `SELECT` joins `phase` and `pct` per `(scenario, arm, seed)` and derives `idle_capacity_pct` as `greatest(0, 100 - 100.0 * served * 5500000 / 2000000000)`—the share of the 2 s phase during which the single database connection was not serving a 5.5 ms lease.

Running the check script reruns all 12 scenarios on seed 1, executes the query, and diffs against the committed [`test/analysis/admission-head-to-head/head-to-head.md`](../../test/analysis/admission-head-to-head/head-to-head.md):

```sh
$ scripts/admission-head-to-head.sh --check
NOTE: the head-to-head table is reproduced exactly from the flushed runs
```

Here is that exact committed table (`seed = 1`):

| load_x | arm | seed | wait_p50_ms | wait_p99_ms | wait_max_ms | depth_peak | served | shed | shed_rule | idle_capacity_pct |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1.2 | timeout | 1 | 2.44 | 19.92 | 55.05 | 5 | 336 | 64 | 64 | 7.6 |
| 1.2 | control-law | 1 | 2.46 | 25.43 | 121.63 | 14 | 338 | 62 | 62 | 7.0 |
| 1.2 | fifo | 1 | 143.65 | 170.92 | 174.06 | 32 | 363 | 35 | 0 | 0.2 |
| 1.2 | folly | 1 | 3.42 | 1149.24 | 1149.24 | 32 | 344 | 56 | 0 | 5.4 |
| 2.0 | timeout | 1 | 2.34 | 54.79 | 88.08 | 19 | 359 | 341 | 341 | 1.3 |
| 2.0 | control-law | 1 | 2.36 | 187.7 | 221.25 | 32 | 363 | 325 | 104 | 0.2 |
| 2.0 | fifo | 1 | 88.08 | 165.68 | 174.06 | 32 | 363 | 318 | 0 | 0.2 |
| 2.0 | folly | 1 | 2.44 | 310.38 | 343.93 | 32 | 363 | 318 | 0 | 0.2 |
| 10.0 | timeout | 1 | 0.22 | 17.69 | 17.69 | 32 | 361 | 3239 | 3108 | 0.7 |
| 10.0 | control-law | 1 | 0.24 | 104.33 | 122.68 | 32 | 363 | 3208 | 104 | 0.2 |
| 10.0 | fifo | 1 | 17.43 | 162.53 | 177.21 | 32 | 363 | 3207 | 0 | 0.2 |
| 10.0 | folly | 1 | 0.25 | 110.1 | 128.97 | 32 | 363 | 3207 | 0 | 0.2 |

What those numbers establish for each arm:

- **`timeout` is the only arm whose served wait stays below `interval` (100 ms) at every load.** Max wait is 55.05 ms at 1.2×, 88.08 ms at 2×, and 17.69 ms at 10× (with p50 down to 0.22 ms under LIFO). At 10× it sheds 3,108 of 3,239 shed requests by its own rule and serves 361 leases against FIFO's 363 (99.4%, 0.7% idle). Its real cost shows up at **1.2× moderate overload**: when the standing queue crosses 100 ms, the allowed wait snaps from 100 ms down to 5 ms and sweeps the queued backlog at once, creating a saw-tooth that sheds 64 requests (all by the rule) and leaves the backend idle **7.6%** of the phase (336 served vs FIFO's 363).
- **`control-law` converges under moderate overload, then yields to ring capacity under heavy open-loop overload.** At 1.2×, its median served wait is 2.46 ms (below the 5 ms target), its p99 is 25.43 ms (far below FIFO's 143.65 ms median), and it sheds 62 requests by its law (7.0% idle, 338 served). At 2× and 10×, open-loop clients never back off; `interval/√count` ramps slowly enough that it fires 104 rule drops in 2 s, leaving the remaining 221 (at 2×) and 3,104 (at 10×) excess requests to the 32-slot ring's overflow. Depth hits the 32-entry cap and max wait sits near `32 × 5.5 ms` (221.25 ms at 2×, 122.68 ms at 10×), with 0.2% idle capacity.
- **`fifo` holds a full standing queue at every overload level.** Peak depth hits 32 immediately, zero requests are shed by a rule, and median served wait stays bloated at 143.65 ms (1.2×) and 88.08 ms (2×).
- **`folly` paired with adaptive LIFO goes blind under sustained overload.** At 2× and 10×, its shed total matches plain FIFO to the request (318 and 3,207; zero `at-pop` sheds) because LIFO hands the detector only fresh arrivals. At 1.2×, where overflow barely trims the ring, requests buried at the head wait until they are eventually dequeued—pushing p99 and max served wait to **1,149.24 ms**, more than six times worse than plain FIFO.

---

## How it is tested and what the tests proved

The suite checks every claim at three layers: pure core properties and fuzzing, tier S simulated end-to-end scenarios with DuckDB SQL assertions, and tier R overload against real Postgres 17.

### Core reference model, classic bugs, canaries, and `FuzzAdmissionOps`

[`internal/admission/ref_test.go`](../../internal/admission/ref_test.go) implements an independent slice-based reference model for all four rules, checking every queued item's age (not just the head) on every operation. `TestCoreAgreesWithTheReferenceModelForEveryRule` ([`property_test.go`](../../internal/admission/property_test.go)) runs 300-operation random sequences across all four rules and four configurations, checking exact agreement on pop IDs, shed lists, `Next()`, per-tenant order, rule states, load gauge, and `Queues.Check()` after every step, plus `Quiesce()` after draining. `TestIntervalOverSqrtMatchesTheReferenceForEveryCountUpToTheMaximum` checks `Law` against `math/big` for every count `1..65,536` across five intervals up to `60s`.

Named tests in [`classic_test.go`](../../internal/admission/classic_test.go), [`repair_test.go`](../../internal/admission/repair_test.go), [`internal/proxy/admission_test.go`](../../internal/proxy/admission_test.go), [`admission_shutdown_test.go`](../../internal/proxy/admission_shutdown_test.go), [`internal/config/queue_test.go`](../../internal/config/queue_test.go), and [`internal/obs/admsrc/admsrc_test.go`](../../internal/obs/admsrc/admsrc_test.go) prove each invariant directly, including exact equality boundaries, fixed pending-shed storage under partial drains, folded `"other"` statistics past 4,096 tenants, capacity-1 overflow, `Unpop` deadline restoration, ordered shutdown, and three planted-bug canaries (`PlantFirstBurst`, `PlantNoHeadSweep`, `PlantNoLIFOHysteresis`):

```sh
$ go test -count=1 -v -run '^(TestFirstBurst|TestCanary|TestLIFO|TestFolly|TestControlLaw)' ./internal/admission
=== RUN   TestFirstBurstAfterIdleGetsTheIntervalNotTheShortTarget
--- PASS: TestFirstBurstAfterIdleGetsTheIntervalNotTheShortTarget (0.00s)
=== RUN   TestCanaryFirstBurstBugShedsTheFirstBurst
--- PASS: TestCanaryFirstBurstBugShedsTheFirstBurst (0.00s)
=== RUN   TestLIFOWithoutTheHeadSweepBuriesStaleRequests
--- PASS: TestLIFOWithoutTheHeadSweepBuriesStaleRequests (0.03s)
=== RUN   TestControlLawCountAndNextDropAcrossLeavingAndReentering
--- PASS: TestControlLawCountAndNextDropAcrossLeavingAndReentering (0.00s)
=== RUN   TestLIFOHysteresisDoesNotFlapAtTheThreshold
--- PASS: TestLIFOHysteresisDoesNotFlapAtTheThreshold (0.00s)
=== RUN   TestLIFOSwitchesOffOnlyAfterTheHold
--- PASS: TestLIFOSwitchesOffOnlyAfterTheHold (0.00s)
=== RUN   TestFollyDetectorWithLIFOIsBlind
--- PASS: TestFollyDetectorWithLIFOIsBlind (0.01s)
=== RUN   TestControlLawAtEqualityOfTargetAndFirstAbove
--- PASS: TestControlLawAtEqualityOfTargetAndFirstAbove (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/admission	0.053s
```

[`FuzzAdmissionOps`](../../internal/admission/fuzz_test.go) decodes raw fuzz bytes locally (3 bytes per operation, capped at 64 operations, no randomness) and drives all four rules against the reference model. `TestFuzzDecodingIsTotalBoundedAndLocal` and `TestFuzzExecutionCostIsBounded` bound its decode and execution cost, and [`scripts/fuzz-plant.sh`](../../scripts/fuzz-plant.sh) verifies that `FuzzAdmissionOps` catches `PlantRemoveSwapsTail` (swapping the ring tail on mid-queue removal, corrupting arrival order) within its 20 s budget.

### Tier S scenarios, DuckDB assertions, and Tier R overload

- **Anti-bufferbloat and recovery (`TestScenarioAdmissionAntiBloat`).** Runs 10× overload for four phases (`T = 2s` through `4T = 8s`). Under both `timeout` and `control-law`, peak queue depth at `4T` equals peak depth at `T`, rule sheds are non-zero, and client backlog stops growing; under the `fifo` ablation on the same seed, client backlog at `4T` grows to at least `3×` its value at `T` and max wait exceeds `interval`. After the overload stops, both product rules return to `StateNormal` with LIFO off within `1.2s` (`interval + lifo_hold + interval`), and a 3 s `0.3×` follow-up phase completes with zero sheds and latency within 4 leases.
- **First burst after idle (`TestScenarioAdmissionFirstBurst` + canary).** Swept across seeds for both `timeout` and `control-law`: 6 clients arrive after a 2 s idle period behind a 60 ms holder, wait past the 5 ms target, and all 7 complete with zero sheds. Enabling `PlantFirstBurst` sheds the burst and fails `CheckFirstBurst`.
- **Replay, CSV round-trip, and SQL assertions.** Every tier S scenario runs twice per seed to prove bit-identical simulator digests and byte-identical CSV files, checks the CSV-to-snapshot round trip, and passes 14 tier S DuckDB SQL files under `test/analysis/admission-*/` covering conservation (`pushes = served + removed + shed + queued`, `engine.requests_shed = admission.shed`), depth equality at `T` and `4T` ([`admission-depth.sql`](../../test/analysis/admission-anti-bloat/admission-depth.sql)), wait percentiles ([`admission-wait.sql`](../../test/analysis/admission-h2h-l120/admission-wait.sql)), and 53M01 counts.
- **Tier R real Postgres (`TestTierRAdmissionOverloadWithPgSleep`, [`test/e2e/c08_tierr_test.go`](../../test/e2e/c08_tierr_test.go)).** Drives 40 clients issuing 150 `SELECT pg_sleep(0.02)` statements each through the compiled `mini-gateway` binary against Postgres 17 (2 connections, `tenant_capacity = 16`). Both `timeout` and `control-law` shed `53M01` while keeping shed clients' connections usable for a trailing `SELECT 9`, sustain well above the 70% real-hardware throughput floor against an overflow-only baseline (81/s for `timeout` with 77.6 ms max served wait and 86/s for `control-law` with 335.5 ms max served wait against 85/s for the overflow-only run), keep `timeout` served waits below 100 ms, and pass SQL conservation over the binary's flushed CSV files.

---

## Stated limits

1. **Capacity utilization under bursts.** The guarantee that shedding serves at least 95% of backend capacity at 10× load holds for evenly spread arrivals whose inter-arrival gap is shorter than `queue.target` (in the benchmark, 100 spread clients send one request about every 0.55 ms against a 5 ms target and 5.5 ms lease, so the queue never runs dry when the connection frees). It does **not** hold for clustered bursts separated by idle gaps longer than the allowed wait: the rule sheds whatever the backend cannot finish within the allowed wait, and the backend then sits idle until the next burst arrives. Per burst, served requests are bounded by `allowed_wait / service_time + 1` (for example, bursts of 1,000 requests every 100 service slots with a 10-slot allowed wait serve 100 of 1,000 slots, or 10% utilization).
2. **Linear scan over live tenant queues and no fair sharing yet.** `OldestFirst` selection, `Next()`, and `Sweep()` cost `O(live tenant queues)` per operation. Per-tenant fair scheduling arrives in Chapter 9 on the `Dispatcher` seam.
3. **Load gauge blindness under LIFO.** `loadGauge` mirrors `folly::Codel` by taking the minimum over *served* queue waits per interval. Under LIFO, served waits come from fresh arrivals, so the gauge under-reports backlog; it is exported for observability only and never drives shedding or LIFO transitions.
4. **Per-tenant statistics folding past 4,096 tenants.** Cumulative per-tenant counters are capped at `MaxTenantStats = 4,096`; further tenants—and any real tenant named `"other"`—share one folded `"other"` row whose depth is bounded by `queue.capacity` rather than `queue.tenant_capacity` (each individual live ring is still checked against `queue.tenant_capacity`). Prometheus additionally folds tenants beyond 32 into `"other"`.
5. **`Unpop` retains its wait sample.** Returning a popped entry after a refused checkout restores its ring slot, enqueue timestamp, empty timestamp, and rule state, while leaving the served-wait sample already recorded in the histogram and load gauge.
6. **Fixed pending-shed buffer.** The pending-shed slice is preallocated once at `2 × queue.capacity + 2` entries and compacted in place; if a caller leaves `queue.capacity + 1` sheds undrained, `Push` refuses new arrivals with reason `undrained`.
7. **LIFO switch recording between operations.** If a queue crosses into standing state between operations and rule deadlines, the `lifo_switches` counter records the transition at the next operation (the pop order itself is always evaluated fresh at `Pop`).
8. **Scenario seed scope and tier R threshold.** The heavy tier S overload scenarios run exact checks on seed 1 (first-burst sweeps multiple seeds), and tier R uses a 70% relative throughput floor against an overflow-only run on the same host to accommodate real-machine scheduling variance.
