# 12. Retries, off by default

When a database connection drops under load, retrying blindly creates two failures worse than the original drop. First, if the proxy re-sends a statement that Postgres already received, a non-idempotent write (`INSERT`, `UPDATE`, `COMMIT`) can be applied twice. Second, if a partial outage makes requests fail and both the proxy and its clients retry aggressively, the failing database receives a burst of amplified load right when it has the least capacity—a **retry storm**.

In the deterministic simulator's `retry-storm` scenario—30% faulted queries on 4 database connections with 8 clients retrying retryable errors up to 6 times—switching on a test-only policy that retries every failure duplicates **48** writes and sends **83** extra statements to the database beyond its first attempts on seed 1. Under `mini-gateway`'s two product policies (`off` and `pre-forward-once`), the same seed applies **0** duplicate writes, keeps statements reaching the database within first attempts plus budgeted retries, answers refusals quickly, and completes 100% of operations once the faults end.

This chapter adds an explicit, budgeted retry policy behind the engine's `RetryPolicy` seam, off by default, across five areas:

- [`internal/retry/retry.go`](../../internal/retry/retry.go) and [`internal/retry/config.go`](../../internal/retry/config.go) (plus the test-only ablation policy in [`internal/retry/retrytest/everything.go`](../../internal/retry/retrytest/everything.go)): a **sans-I/O** (no sockets, goroutines, wall-clock reads, or floating point) retry core providing `Off` (the default) and `PreForwardOnce`, an integer tenths-of-a-token budget, fleet floor division, and exact snapshot conservation.
- [`internal/proxy/retry.go`](../../internal/proxy/retry.go) and [`internal/proxy/matrix.go`](../../internal/proxy/matrix.go) (plus hooks in [`lease.go`](../../internal/proxy/lease.go), [`engine.go`](../../internal/proxy/engine.go), [`corepool.go`](../../internal/proxy/corepool.go), [`client.go`](../../internal/proxy/client.go), [`seams.go`](../../internal/proxy/seams.go), [`snapshot.go`](../../internal/proxy/snapshot.go), and [`contracts.go`](../../internal/proxy/contracts.go)): engine wiring that re-runs a granted retry from `statementLost`, clamps timers to the request's deadline, re-checks the circuit breaker before forwarding, accounts for every retry exit path inside `Engine.Invariants` and `Engine.Quiesce`, and generates the complete fault × path matrix ([`docs/fault-matrix.md`](../../docs/fault-matrix.md)).
- [`internal/pgwire/errors_retry.go`](../../internal/pgwire/errors_retry.go) and [`internal/config/retry.go`](../../internal/config/retry.go): completes the error contract ([`docs/error-contract.md`](../../docs/error-contract.md)) with a "Retried by the proxy?" column on every row, and adds fail-closed JSON configuration under `retry.*` plus rows in [`docs/capacity-table.md`](../../docs/capacity-table.md) and [`docs/timeout-budget.md`](../../docs/timeout-budget.md).
- [`internal/obs/retrysrc/retrysrc.go`](../../internal/obs/retrysrc/retrysrc.go) and [`internal/obs/enginesrc/enginesrc.go`](../../internal/obs/enginesrc/enginesrc.go): exports component `retry` budget counters and engine retry lifecycle metrics ([`docs/csv-schema.md`](../../docs/csv-schema.md)) without altering health rules.
- [`test/e2e/c12_retry.go`](../../test/e2e/c12_retry.go), [`test/e2e/c12_relay.go`](../../test/e2e/c12_relay.go), [`test/e2e/c12_client.go`](../../test/e2e/c12_client.go), [`test/e2e/c12_retry_test.go`](../../test/e2e/c12_retry_test.go), [`test/e2e/c12_c09_join_test.go`](../../test/e2e/c12_c09_join_test.go), [`test/e2e/c12_tierr_test.go`](../../test/e2e/c12_tierr_test.go), and eight SQL assertions under `test/analysis/`: tier S storm, no-double-apply kill sweep, weighted-fairness storm, planted-bug canaries, and tier R storm against live Postgres 17.

**What lives in its own chapter.** The adaptive concurrency limit and per-tenant rate gate, the sharing of identical in-flight reads, and three-instance fleet measurements are described in their own chapters.

---

## Design: one hard fact, an integer budget, and one waiting place

A proxy retry policy has to answer two questions: *which* failures are safe to re-run, and *how many* retries are safe to spend while the database is failing.

### The one fact a retry may rest on

Under `PreForwardOnce`, the proxy retries a failed statement **only** when nothing of that request reached the database for certain. In `proxy.Engine`, that fact is `Failure.Reached == work.No`, computed by `reachedFor` in [`internal/proxy/lease.go`](../../internal/proxy/lease.go). The engine sets `Reached` to `work.No` in one situation only: the socket driver explicitly reported a write refusal (`EvDBWriteRefused`) at an acknowledged byte offset at or before the request's first byte, and zero bytes of a reply were relayed to the client.

Every other loss is treated as "may have reached" (`work.Yes`) and is never retried under any product policy:

- a write refused after even 1 byte was accepted by the socket;
- a backend close with no write-refusal report;
- a statement timeout (silence from Postgres);
- any failure after a single reply byte has been relayed to the client.

Failures inside a transaction (`Failure.InTx == true`) are also never retried, even when `Reached == work.No`: the transaction's earlier statements were lost with the database connection, so re-running only the last statement on a fresh connection would run it outside its transaction. `PreForwardOnce` checks `InTx` before `Reached`.

**Why `off` by default, and why never trust the classifier or error row.** The policy reads `Failure.Reached`, never the error-contract row's general column and never the classifier's `SafeRead` verdict. As Chapter 5 documents, a SQL statement whose text matches the safe read shape can still hide a write behind a view, user-defined operator, or row-level security policy. Because the retry decision ignores `SafeRead` completely, a classifier misjudgment cannot cause a double-applied write. Likewise, refusals generated before a database connection is ever touched—queue shedding (`53M01`), circuit breaker (`53M03`), unsupported features (`0A000`), queued cancels (`57014`), or draining (`53M05`)—never reach `statementLost`; those belong to the client so backoff happens at the caller rather than inside the proxy.

### An integer token budget

Even a pre-forward retry consumes a database connection dial and another trip through the queue. To stop retries from amplifying load during a rolling connection failure, `PreForwardOnce` charges every retry to an integer token bucket measured in **tenths of a token** (avoiding floating point completely):

- **Start:** the bucket starts at `retry.floor / fleet.instances` whole tokens (default `10` fleet tokens, `100` tenths on one instance).
- **Deposit:** every first attempt forwarded to a database connection (`Engine.forward`, including statements inside transactions) deposits `1` tenth of a token (`DepositTenths = 1`).
- **Withdraw:** every granted retry withdraws `10` tenths (`WithdrawTenths = 10`, one full token), only when the bucket holds at least `10` tenths.
- **Cap:** any deposit that would push the level above `retry.bucket_cap` tokens (default `100` tokens = `1,000` tenths per instance) is discarded and counted in `Expired`.

The token bucket enforces two exact inequalities:

```text
From startup:     10 × retries <= attempts + floor_in_tenths
Over any window:  10 × Δretries <= Δattempts + credit_carried_in <= Δattempts + cap_in_tenths
```

Notice what the cap does and does not do. Deposits above `retry.bucket_cap` are thrown away immediately, so a long stretch of healthy traffic cannot accumulate unbounded credit. Stored credit itself does **not** expire with time: after a quiet period with a full bucket, the largest burst of retries the proxy can grant is `retry.bucket_cap` tokens, after which retries drop to at most 10% of new first attempts.

### One waiting place, one deadline, and the circuit breaker

When `PreForwardOnce` grants a retry, three constraints keep the retry from leaking resources or outrunning the caller:

1. **One waiting place, no held connection.** Before `statementLost` offers the failure to the retry policy, `leaseFailed` has already closed the failed connection (or moved it to the dirty-connection clean-up table). A granted retry re-enters `Engine.enqueue` in the same step without re-running classification, reject-list, or rate gates. While it waits for a replacement connection, it holds **no database connection** and holds **one place in its tenant's admission queue** (decision D3: a request waits in its tenant's queue and nowhere else). It takes the exact same queue exits as any other queued request: `53M01` if shed by the queue rule, overflow, tenant cap, or deadline; `53M03` if flushed by the breaker; `57014` if cancelled; or silent cleanup if the client disconnects.
2. **One request deadline.** Every request carries its arrival timestamp (`req.enq`). Its retry deadline is `req.enq + timeouts.statement`. Following the engine's time-passes-before-event rule, a failure at exact equality (`now >= deadline`) is refused (`RefusedDeadline`). For a granted retry, `retryClamp` clamps both its queue-wait timer and its forwarded statement timer to `req.enq + timeouts.statement`, so a retry can never outlive the original statement budget.
3. **Never when the circuit breaker is open.** A **circuit breaker** (Chapter 7) opens when consecutive database failures cross a threshold and refuses traffic immediately with `53M03` until a timed half-open trial succeeds. Calling `Breaker.Allow(now)` on the pool core has a side effect—it counts as demand and can start a half-open trial—so the retry wiring instead reads `CoreBreaker.Open()`, a pure check that returns `true` whenever the breaker is open *or* half-open. The connection loss is reported to the pool core (`coreLost`) before `offerRetry` runs, so a failure that trips the breaker is refused immediately (`RefusedBreaker`). If a breaker opens while a granted retry is already waiting in the queue, `Engine.dispatch` re-checks `breakerOpen()` before lending any idle connection and refuses waiting retries with `53M03` (`RetryEndBreaker`) without refunding the token withdrawn at grant. In the shipped pool wiring, opening the breaker flushes all queued requests synchronously via `ActRefuseQueued` (`RetryEndShed`), so the dispatch re-check acts as a defense for custom breaker seams.

### Composing retries with per-tenant fairness

Every admission queue runs Chapter 9's weighted fair dispatcher (`admission.Fair`), and retries compose with it without special cases:

- **Lease accounting stays exact.** `leaseLost` reports `fairEnded` before `statementLost` offers the retry, so the tenant's in-flight count drops by one before the retry enters its queue, and `Engine.lend` calls `fairStarted` once when the retry is lent again.
- **A tenant gains no service through retries.** Because `rerun` calls `enqueue`, a granted retry is pushed to the tail of its tenant's ring (`Queue.Push`) as a new entry and must spend a fresh round-robin turn. The turn spent by the failed first attempt is not refunded, and other tenants keep their turns. Each retried request therefore costs its tenant one extra turn (and increments the admission queue's `pushes` and `served` counters twice while keeping queue conservation exact).
- **Held-back retries trigger no extra dials.** Because a waiting retry is an ordinary entry in its tenant's queue, `Queues.Releasable()` excludes it from pool demand whenever its tenant sits at or above its share on a reserved last slot or at a fair limit below `pool.connections`. It still expires at `arrival + timeouts.statement`, and any event that makes it eligible (including another tenant losing a plain-statement lease via `fairRedispatch`) runs through `dispatch` and its breaker re-check.

---

## How it works: policy, engine hooks, and the completed fault matrix

### Policy decision order

The eligibility order and budget withdrawal live in `PreForwardOnce.Decide` and `eligible` in [`internal/retry/retry.go`](../../internal/retry/retry.go):

```go
func (p *PreForwardOnce) Decide(now Time, f Failure) Decision {
	p.s.Offered++
	if p.plant&PlantWithdrawFirst != 0 && p.s.Level.Value >= WithdrawTenths {
		p.withdraw()
	}
	r := p.eligible(now, f)
	if r == Granted && p.plant&PlantWithdrawFirst == 0 {
		r = p.charge()
	}
	if r != Granted {
		p.s.Refused[r]++
		return Decision{Reason: r}
	}
	p.s.Retries++
	return Decision{Retry: true, Reason: Granted}
}

func (p *PreForwardOnce) eligible(now Time, f Failure) Reason {
	// Check InTx before Reached: even a zero-byte refusal inside a transaction
	// cannot be retried because earlier statements went with the connection.
	// Equality (now >= Deadline) refuses under the time-passes-before-event rule.
	switch {
	case f.Attempt >= MaxAttempts && p.plant&PlantNoOnceBound == 0:
		return RefusedOnce
	case f.InTx:
		return RefusedInTx
	case f.Reached != work.No && p.plant&PlantIgnoreReached == 0:
		return RefusedReached
	case f.BreakerOpen && p.plant&PlantIgnoreBreaker == 0:
		return RefusedBreaker
	case f.Draining:
		return RefusedDraining
	case now >= f.Deadline:
		return RefusedDeadline
	}
	return Granted
}
```

Checking eligibility before charging guarantees that an ineligible failure (such as a post-forward loss or an open breaker) never drains the retry bucket. Every offered failure ends either in `Retries++` or in exactly one `Refused[r]++`, and `Snapshot.Check()` verifies both offer conservation (`Offered == Retries + RefusedTotal`) and token conservation (`Deposits == Withdrawals + Expired + Level`) on every engine invariant check.

### Engine call site and exit-path accounting

All retry offers pass through one call site, `Engine.statementLost` in [`internal/proxy/lease.go`](../../internal/proxy/lease.go), backed by `offerRetry` and `rerun` in [`internal/proxy/retry.go`](../../internal/proxy/retry.go):

```go
func (e *Engine) statementLost(c *client, d *dbConn) {
	req := c.req
	c.req = nil
	e.snap.InFlight.Add(-1)
	reached := reachedFor(d, req)
	row := lostRow(req, reached)
	// Offer the failure before recording a client failure: a granted retry keeps
	// the request alive for its one client reply. The failed connection was
	// already closed or moved to the dirty table before reaching statementLost.
	if e.offerRetry(c, req, reached, row) {
		e.rerun(c, req)
		return
	}
	e.snap.Failed++
	e.reached(reached)
	e.retryEnded(req, RetryEndFailed)
	c.phase = cpIdle
	c.status = pgwire.StatusIdle
	e.reply(c, row)
	if c.phase != cpClosed {
		e.pump(c)
	}
}
```

When `offerRetry` returns `true`, `rerun` increments `req.attempt`, marks `RetriedRequests++` and `RetryPending.Add(1)` on the first retry, resets `req.relayed = 0`, and hands the request back to `enqueue`. Every path that later clears `c.req` calls `retryEnded` with one of five outcomes:

- `completed` (`replyDone`: the retried statement succeeded);
- `failed` (`statementLost` on a second failure, or `clientCancel` while queued);
- `shed` (`shed` on queue rule, overflow, tenant cap, or clamped deadline, or pool-core breaker flush via `refuseQueued`/`refuseAdmitted`);
- `gone` (`dequeueGone`, `activeGone`, or `Shutdown`);
- `breaker` (`refuseRetriesBehindOpenBreaker` at the top of `dispatch`).

`Engine.Invariants` checks `retryCheck()` after every simulator step—verifying `RetryPending` against live clients, matching the engine's `RetryOffered`/`RetryAsked` against the policy's `Offered`/`Retries`, and running `retry.Snapshot.Check()`. `Engine.Quiesce` refuses to pass if `RetryPending != 0`. Because a granted retry can also end while queued (shed, cancelled, or disconnected) before reaching `Engine.forward`, total statements forwarded obey:

```text
attempts <= forwards <= attempts + retries - retry_ends{outcome="breaker"}
```

with the upper bound met exactly when no granted retry ends in the queue before forwarding.

### Completed contracts, configuration, and observability

- **Error contract and fault matrix:** [`docs/error-contract.md`](../../docs/error-contract.md) now states for every row whether the proxy may retry it; only `lost-after-read` (`08006`) says yes, and only when the failure itself carries `Reached == No`. [`docs/fault-matrix.md`](../../docs/fault-matrix.md) is now generated from [`internal/proxy/matrix.go`](../../internal/proxy/matrix.go) (`scripts/gen-docs.sh`, held by a test) across all **32** fault-by-path rows; exactly one row (`First write of the request refused with zero bytes accepted (a lent connection found dead)`) has `Retried() == true`. Rows whose error row allows a retry in general but whose path never does (such as exact deadline equality or a loss that trips the breaker) carry an explicit `NotRetried` reason.
- **Configuration (`internal/config/retry.go`):** `retry.policy` (`"off"` default, or `"pre-forward-once"`), `retry.bucket_cap` (`1..1,000,000` tokens per instance, default `100`), and `retry.floor` (`1..1,000,000` fleet tokens, default `10`, clamped to `min(10, bucket_cap × fleet.instances)` when omitted). Zero or omitted selects the default; negatives and unknown policy names fail closed naming the exact key. When `retry.policy` is `"pre-forward-once"`, `retry.floor` is divided by `fleet.instances` using integer floor division (refusing any split below 1 token per instance and reporting any remainder through `File.Warnings()`), while the 10% refill ratio and per-instance `retry.bucket_cap` are not divided. The test-only `retrytest.Everything` policy has no config name and is never linked into `./cmd/...`.
- **Observability (`internal/obs/retrysrc`):** exports component `retry` metrics (`policy`, `attempts`, `offered`, `retries`, `refused{reason}`, `exhaustions`, `budget_level`, `budget_level_peak`, `budget_cap`, `budget_floor`, `deposits`, `withdrawals`, `expired`) alongside engine metrics (`retry_offered`, `retry_asked`, `retried_requests`, `retries_pending`, `retries_pending_peak`, `retry_rows{reason}`, `retry_ends{outcome}`). Retries add no health rule: an exhausted retry budget means the proxy is declining to amplify load, not that the proxy itself is unhealthy.

---

## How it is tested and what the tests proved

### Core reference model, boundary cases, canaries, and `FuzzRetryOps`

[`internal/retry/ref_test.go`](../../internal/retry/ref_test.go) runs an independent reference model (`refModel`) and a linear window-bound checker (`window.check`, verified against the pairwise quadratic checker on 20,000 random traces) across random sequences of attempts, failures, breaker states, and deadlines. Canaries verify that disabling any of the six core guards (`Reached == No`, once-per-request, budget withdrawal, bucket cap, breaker check, or check-before-withdraw ordering) breaks the property test:

```sh
$ go test -count=1 -v ./internal/retry ./internal/obs/retrysrc
=== RUN   TestCanaryEveryPlantedBudgetBugFailsTheProperty
--- PASS: TestCanaryEveryPlantedBudgetBugFailsTheProperty (0.01s)
=== RUN   TestPropertyBudgetAgreesWithTheReferenceModel
--- PASS: TestPropertyBudgetAgreesWithTheReferenceModel (1.36s)
=== RUN   TestPathologicalSignalEveryRequestFailingPreForwardStaysUnderTheBound
--- PASS: TestPathologicalSignalEveryRequestFailingPreForwardStaysUnderTheBound (0.00s)
=== RUN   TestAQuietHourCannotBankAStorm
--- PASS: TestAQuietHourCannotBankAStorm (0.01s)
=== RUN   TestBucketHoldingExactlyOneTokenGrantsOneRetry
--- PASS: TestBucketHoldingExactlyOneTokenGrantsOneRetry (0.00s)
=== RUN   TestBucketExactlyAtTheCap
--- PASS: TestBucketExactlyAtTheCap (0.00s)
=== RUN   TestEveryRefusalReasonIsReachedAndCounted
--- PASS: TestEveryRefusalReasonIsReachedAndCounted (0.00s)
=== RUN   TestDeadlineAtEqualityRefusesAndJustBeforeGrants
--- PASS: TestDeadlineAtEqualityRefusesAndJustBeforeGrants (0.00s)
=== RUN   TestOffNeverRetriesAndCountsEveryOffer
--- PASS: TestOffNeverRetriesAndCountsEveryOffer (0.00s)
=== RUN   TestConfigValidationNamesTheKey
--- PASS: TestConfigValidationNamesTheKey (0.00s)
=== RUN   TestFloorShareDividesByInstancesAndRefusesBelowOne
--- PASS: TestFloorShareDividesByInstancesAndRefusesBelowOne (0.00s)
=== RUN   TestSnapshotCheckCatchesBrokenConservation
--- PASS: TestSnapshotCheckCatchesBrokenConservation (0.00s)
=== RUN   TestFuzzDecodingIsTotalBoundedAndLocal
--- PASS: TestFuzzDecodingIsTotalBoundedAndLocal (0.00s)
=== RUN   TestFuzzPlantIsFoundByTheReferenceModel
--- PASS: TestFuzzPlantIsFoundByTheReferenceModel (0.00s)
=== RUN   TestWindowCheckAgreesWithThePairwiseCheck
--- PASS: TestWindowCheckAgreesWithThePairwiseCheck (0.05s)
=== RUN   TestStoredCreditDoesNotExpireWithTimeAndTheLargestBurstIsTheCap
--- PASS: TestStoredCreditDoesNotExpireWithTimeAndTheLargestBurstIsTheCap (0.00s)
=== RUN   TestAnOmittedFloorFollowsASmallCapAndABadCapNamesOnlyTheCap
--- PASS: TestAnOmittedFloorFollowsASmallCapAndABadCapNamesOnlyTheCap (0.00s)
=== RUN   FuzzRetryOps
--- PASS: FuzzRetryOps (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/retry	1.442s
=== RUN   TestMappingExportsEveryFieldOfTheRetrySnapshot
--- PASS: TestMappingExportsEveryFieldOfTheRetrySnapshot (0.00s)
=== RUN   TestRetryCountersMoveAndConserve
--- PASS: TestRetryCountersMoveAndConserve (0.00s)
=== RUN   TestHealthIsUnchangedByRetries
--- PASS: TestHealthIsUnchangedByRetries (0.00s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/obs/retrysrc	0.013s
```

`FuzzRetryOps` decodes 3 bytes per operation (capped at 256 operations, no hash-derived seeds). With the planted ordering bug enabled (`MG_FUZZ_PLANT=retry-withdraw-first`, withdrawing a token before checking eligibility), `FuzzRetryOps` fails immediately on `seed#2`:

```sh
$ MG_FUZZ_PLANT=retry-withdraw-first go test -count=1 -run '^FuzzRetryOps$' ./internal/retry
--- FAIL: FuzzRetryOps (0.00s)
    --- FAIL: FuzzRetryOps/seed#2 (0.00s)
        retry_test.go:322: kind pre-forward-once: op 2: retry: 10 tenths withdrawn for 0 retries
            +0 attempt
            +0 attempt
            +0 fail reached=yes intx=false attempt=0 breaker=false draining=false ttl=500000
FAIL
FAIL	github.com/nickstrad/mini-gateway/internal/retry	0.008s
```

Engine unit tests in [`internal/proxy/retry_test.go`](../../internal/proxy/retry_test.go), [`retry_repair_test.go`](../../internal/proxy/retry_repair_test.go), [`retry_canary_test.go`](../../internal/proxy/retry_canary_test.go), [`retry_internal_test.go`](../../internal/proxy/retry_internal_test.go), and [`join_retry_fair_test.go`](../../internal/proxy/join_retry_fair_test.go) prove every eligibility guard, all seven `retryEnded` exit paths, `Engine.Invariants` and `Engine.Quiesce` checks, matrix completeness, and every retry × fairness interaction (queue overflow, tenant cap, above-share last-slot reservation, fair limit below `pool.connections`, round-robin turn order, breaker re-check on another tenant's lost lease, and shutdown). `TestJoinEveryRetryEngineTestHoldsBehindTheFairDispatcher` also replays all 26 engine retry tests behind the fair dispatcher under both `timeout` and `control-law` admission rules.

### Tier S scenarios, ablation, and DuckDB SQL assertions

In the simulator, the pool checks stale and suspect idle connections before lending, so dead connections are normally absorbed with no client error. To produce genuine zero-byte write refusals (`EvDBWriteRefused`) by construction without modifying the frozen simulator API, [`test/e2e/c12_relay.go`](../../test/e2e/c12_relay.go) sits between the proxy and fake Postgres and applies `simnet.FaultHalfOpen` to the proxy's end of idle connections during a quiet gap (`DoomAllAt`), plus mid-query cuts at all five conversation points (`before-forward`, `after-forward`, `after-applied`, `mid-reply`, `after-reply`).

To force budget exhaustion deterministically on every seed regardless of dispatcher order, `retry-storm` runs a one-token bucket (`Cap = 1`, `Floor = 1`) with two doom bursts around a 20 ms spend phase (shorter than `retryClientGap = 25 ms`, so each of the 8 clients sends at most one operation): the first lend after burst 1 spends the one token (`10 -> 0`), at most 7 other first attempts deposit afterward, and the first lend after burst 2 hits a doomed connection at level at most `8 < 10` tenths and is refused for budget (`RefusedBudget`).

Running all tier S retry scenarios, canaries, the 3:1 weighted-fairness storm, and the earlier kill and direct-equality re-runs on seed 1:

```sh
$ RUN_DIR=/tmp/opencode/c12-prose-run go test -count=1 -v \
    -run '^(TestScenarioRetry$|TestCanaryRetry|TestScenarioRetryStormTwoTenantsOfUnequalWeight$|TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn$)' \
    ./test/e2e -seed=1
--- PASS: TestScenarioRetryStormTwoTenantsOfUnequalWeight (0.00s)
    --- PASS: TestScenarioRetryStormTwoTenantsOfUnequalWeight/seed=1 (4.66s)
--- PASS: TestScenarioRetry (0.00s)
    --- PASS: TestScenarioRetry/retry-storm (0.00s)
        --- PASS: TestScenarioRetry/retry-storm/off/seed=1 (10.97s)
        --- PASS: TestScenarioRetry/retry-storm/everything/seed=1 (11.78s)
        --- PASS: TestScenarioRetry/retry-storm/on/seed=1 (11.71s)
    --- PASS: TestScenarioRetry/retry-no-double-apply (0.00s)
        --- PASS: TestScenarioRetry/retry-no-double-apply/off/seed=1 (7.39s)
        --- PASS: TestScenarioRetry/retry-no-double-apply/everything/seed=1 (9.31s)
        --- PASS: TestScenarioRetry/retry-no-double-apply/on/seed=1 (8.11s)
--- PASS: TestCanaryRetryStormCatchesAPolicyThatIgnoresReached (4.49s)
--- PASS: TestCanaryRetrySweepCatchesAPolicyThatIgnoresReached (1.92s)
--- PASS: TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn (0.80s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	42.700s
```

Querying the flushed CSV files from that seed-1 run with **DuckDB** (an embedded analytical SQL engine) shows the exact separation between the product arms (`off` and `on`) and the deliberately broken `everything` ablation arm:

| Scenario | Arm | First attempts | Offered | Retries | Exhaustions | Forwards | Writes applied | Distinct sent | Duplicates | Forbidden-row retries |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `retry-storm` | `off` | 815 | 66 | 0 | 0 | 815 | 790 | 801 | 0 | 0 |
| `retry-storm` | `on` | 806 | 69 | 14 | 3 | 820 | 792 | 803 | 0 | 0 |
| `retry-storm` | `everything` | 782 | 84 | 84 | 0 | 865 | 829 | 781 | 48 | 62 |
| `retry-no-double-apply` | `off` | 475 | 75 | 0 | 0 | 475 | 439 | 467 | 0 | 0 |
| `retry-no-double-apply` | `on` | 466 | 73 | 6 | 0 | 472 | 438 | 466 | 0 | 0 |
| `retry-no-double-apply` | `everything` | 458 | 109 | 109 | 0 | 567 | 516 | 458 | 58 | 100 |

Every seed runs twice to verify bit-identical simulator digests and byte-identical CSV bytes, and [`scripts/csv-assert.sh`](../../scripts/csv-assert.sh) validates all three SQL files per scenario—[`budget.sql`](../../test/analysis/retry-storm/budget.sql) (window and from-start bounds across every flush), [`conservation.sql`](../../test/analysis/retry-storm/conservation.sql) (engine/policy agreement, token conservation, zero pending retries, `refused{reason="budget"} == exhaustions`), and [`load.sql`](../../test/analysis/retry-storm/load.sql) (forward bounds, no extra applied writes on product arms, and mandatory amplification plus duplicate writes on `everything`):

```sh
$ scripts/csv-assert.sh /tmp/opencode/c12-prose-run/retry-storm test/analysis/retry-storm/*.sql
ok: test/analysis/retry-storm/budget.sql over 3 runs
ok: test/analysis/retry-storm/conservation.sql over 3 runs
ok: test/analysis/retry-storm/load.sql over 3 runs
csv-assert: 3 SQL files run, 0 failures; v1.5.5 (Variegata) d8cdaa33fd
```

`TestScenarioRetryStormTwoTenantsOfUnequalWeight` runs the `on` arm behind two tenants weighted 3:1 and asserts that all product retry checks hold, fair lease starts and ends equal the engine's, retries complete, and the weight-1 tenant spends more than 10× longer at its share than the weight-3 tenant. `TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn` re-runs the earlier kill scenarios and direct-equality workloads (`e2e-results`, `pool-results`) with `PreForwardOnce` enabled, proving replies still match direct execution and post-forward kills produce zero retries.

### Tier R against live Postgres 17

[`TestTierRRetryStorm`](../../test/e2e/c12_tierr_test.go) runs the compiled `mini-gateway` binary in front of a real Postgres 17 container with 4 backend connections and 12 client goroutines inserting unique rows into `marks`, while a superuser loop calls `pg_terminate_backend` on 30% of backend sessions every 100 ms for 3 seconds:

```sh
$ MG_TIER_R=1 RUN_DIR=/tmp/opencode/c12-prose-tierr go test -count=1 -v -run '^TestTierRRetryStorm$' ./test/e2e
=== RUN   TestTierRRetryStorm
=== RUN   TestTierRRetryStorm/off
    c12_tierr_test.go:184: off: 30 kills, 2330 operations, 2328 ok, codes map[08007:2]; after the errors 705 of 705 ok
=== RUN   TestTierRRetryStorm/on
    c12_tierr_test.go:184: on: 28 kills, 3673 operations, 3669 ok, codes map[08007:4]; after the errors 1046 of 1046 ok
--- PASS: TestTierRRetryStorm (17.64s)
    --- PASS: TestTierRRetryStorm/off (9.00s)
    --- PASS: TestTierRRetryStorm/on (8.62s)
PASS
ok  	github.com/nickstrad/mini-gateway/test/e2e	33.777s
```

Both arms leave **0** duplicate `(client, round)` rows in `marks`, pass [`test/analysis/tierr-retry-storm/budget.sql`](../../test/analysis/tierr-retry-storm/budget.sql) and [`conservation.sql`](../../test/analysis/tierr-retry-storm/conservation.sql), keep refusals within 500 ms, and achieve 100% goodput after the errors stop (`705/705` under `off`, `1,046/1,046` under `on`).

**Why the real-Postgres test produces zero retries.** In the CSV output of the run above, the `off` arm offered `2` lost statements and retried `0`, and the `on` arm offered `4` lost statements and retried `0` (`forwards == attempts`). That is expected on real TCP sockets: terminating an idle Postgres backend is absorbed before lending by the pool's check-before-reuse, and terminating an active backend races with the kernel socket write so bytes have already been accepted by TCP (`08007`, `Reached == Yes`) rather than returning an explicit zero-byte write refusal. Tier R therefore verifies no duplicate writes, fast refusals, conservation, and recovery on the real binary and Postgres 17, while actual proxy retry execution and budget exhaustion are proven deterministically in tier S.

---

## Stated limits

1. **Stored credit does not expire with time; the largest burst is the bucket cap.** Over any window, `10 × Δretries <= Δattempts + credit_carried_in`, where `credit_carried_in <= retry.bucket_cap × 10`. Deposits past `retry.bucket_cap` are discarded (`Expired`), but credit already in the bucket does not decay during idle time. After a quiet period with a full bucket, the proxy can grant a burst of up to `retry.bucket_cap` retries before dropping to the 10% refill rate.
2. **A waiting retry holds one place in its tenant's admission queue.** While waiting for a replacement connection, a retried request holds no database connection, but it occupies one slot in its tenant's bounded admission ring (following the one-waiting-place design) and can overflow or be shed like any other queued request.
3. **Real-Postgres testing does not exercise proxy retries directly.** Because a zero-byte write refusal cannot be forced deterministically on real TCP sockets against `pg_terminate_backend`, `TestTierRRetryStorm` records `0` retries on both arms; retry counter movement and budget exhaustion are proved in tier S.
4. **Retries cost a tenant an extra fair turn and never gain service.** A granted retry re-enters at the tail of its tenant's queue (`Queue.Push`) without refunding the first attempt's round-robin turn. Other tenants keep their turns, and the admission queue's `pushes` and `served` counters count a retried request twice. (With adaptive **LIFO**—last-in, first-out—active on a standing tenant queue, the retry may run before older entries of its *own* tenant without changing order across tenants.)
5. **At most one retry per request, never inside a transaction.** A second zero-byte refusal on the replacement connection is refused (`RefusedOnce`) and answered `lost-after-read` (`08006`). Any failure inside a transaction is refused (`RefusedInTx`) regardless of `Reached`.
6. **A token withdrawn at grant is not refunded if the breaker opens before forward.** If a custom breaker seam opens while a retry is queued, `dispatch` refuses the retry with `53M03` (`RetryEndBreaker`) and leaves the token spent so outages cannot mint credit.
7. **Breaker seams without `Open()` read as closed.** `CoreBreaker` and `ClosedBreaker` implement the pure `Open()` check; a custom `Breaker` seam that omits `Open()` is treated as closed for both the retry offer and the dispatch re-check.
8. **Fleet scaling of cap, floor, and ratio.** The 10% refill ratio holds across a fleet (each instance stays within 10% of its own first attempts), and `retry.floor` is divided by `fleet.instances` in config (with remainder warnings). `retry.bucket_cap` is per instance and is *not* divided by `fleet.instances`, so fleet-wide banked credit is `fleet.instances × retry.bucket_cap`. `retry.floor` is a starting level in tokens, not a recurring time-window reserve.
9. **No coordination with rate limiting or read sharing.** A retry re-enters directly at `enqueue`, bypassing the rate gate (its token bucket is its governor) and the read-sharing stage, which are described in their own chapters.
10. **Classifier errors cannot cause unsafe retries.** Because `PreForwardOnce` reads `Failure.Reached` from the driver's write report and never inspects `SafeRead`, a statement misclassified as a safe read is still never retried once any byte was handed to the database.
11. **Tier S exhaustion construction needs fewer than ten clients.** The storm's guaranteed budget exhaustion relies on `10 - clients` tenths of margin during the 20 ms spend phase (8 clients give a 2-tenth margin with the fault window kept out of the spend phase). `retry-no-double-apply` uses a 20-token cap and does not exhaust.
