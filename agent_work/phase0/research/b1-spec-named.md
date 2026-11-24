# B1 - Spec-named influences (spec section 6)

Research run: 2026-11-24. Every "read directly" below was fetched and read in this run.
Three things the orchestrator should know before reading the entries:

1. **All six spec-named posts exist** with the titles the spec gives (small title/date corrections noted per entry).
2. **The "19x connection reduction" is a model output, not a measurement.** Meta's post derives it from a balls-into-bins model with "round mock figures". The spec's wording ("19x connection reduction") reads as a result; it is not one.
3. **"CoDel" means three different algorithms** across the spec's sources (network CoDel, the "Fail at Scale" queue-timeout rule, and folly's `Codel` class). The project must pick one. See "CoDel: which one" after the entries.

---

## 1. Meta ZGateway

### Meta Engineering - "ZGateway: Learnings from Putting a Proxy in Front of ZippyDB"
- **Source:** https://engineering.fb.com/2026/09/03/core-infra/zgateway-proxy-zippydb-meta/ (3 Sep 2026; date from the URL, matches the spec's "Sep 2026").
- **Verified?** read directly (full text; the formula and result-table images did not render as text, so the exact formulas and per-host numbers are NOT quoted here). InfoQ coverage also read: https://www.infoq.com/news/2026/09/meta-zgateway-zippydb-proxy/ (secondary).
- **Key idea:** A stateless regional proxy tier between >1M client hosts and the ZippyDB server fleet. Clients keep a sticky pool to the gateway; servers see only the gateway fleet. The value is a changed scaling variable (server fan-in stops depending on client population) plus one control point for batching, coalescing, admission control and per-use-case metrics. Carries ~40% of ZippyDB traffic, >1B ops/s, ~6% compute overhead.
- **What Mini-ZGateway would concretely borrow:**
  - *Pool / thesis:* "The connections don't vanish; they move to the tier built to hold them." Fan-in becomes a number the operator sets.
  - *Batching:* one shared batcher keyed by (use case, physical shard). A batch flushes on any of three triggers: linger window elapsed, payload size limit, request-count cap. Oversized batches fall back to individual sends. Two safety valves: (a) idle eviction - batch-map entries idle past a TTL are erased on the next flush; (b) an in-flight cap on running flushed batches that rejects new executions when the backend slows.
  - *Coalescing:* same key at the same moment is fetched once and fanned out. On the cache tier this is a per-key fill lock.
  - *Fairness (admission):* "Discriminant Load Shedding" - every request maps to a per-tenant bucket (keyed by use case, split by priority); buckets drain round-robin; a flooding tenant fills only its own bucket and its excess is shed.
  - *Concurrency limit:* an AIMD loop on CPU adjusts how fast a shared token bucket admits work; a memory handler does the same for OOM.
  - *Observability:* per-use-case metrics, traces and quota usage; the post says this is what makes shared-tier admission control safe.
  - *Benchmark shape:* controlled overload at >90% CPU, ~1,350 tenant buckets, only 6 shedding, the other ~1,344 ran 99.9% of requests with zero rejections, goodput 97-98%, machinery cost ~8% CPU.
- **What NOT to copy / where it misleads:**
  - The 19x figure. It comes from mock inputs (20 regions, 500,000 DB hosts, 30,000 proxy hosts, 1,000,000 clients, 50,000 shards per client) and the post says "total persistent connections still drop by roughly 19x". With one backend the balls-into-bins model degenerates (one bin); Mini-ZGateway's reduction is simply clients / MaxPoolSize and must be measured.
  - The post gives NO linger-window value, NO batch caps, NO AIMD constants, and says nothing about CoDel, adaptive LIFO, retry budgets, pre- vs post-forward error codes, or cancellation isolation. Those spec parts cannot cite ZGateway.
  - ZGateway batches writes too ("batches and coalesces misses and writes"); that is batching, not coalescing - keep the spec's rule that writes are never coalesced.
  - CPU as the AIMD signal fits a proxy fleet; for a single backend the useful signal is backend latency or in-flight count.
  - Skip: cross-region routing, weighted consistent-hash balancer, CDC cache invalidation, transactions on the gateway.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - borrow the structure (per-tenant round-robin buckets, three-trigger batch flush, batch safety valves); do not borrow the 19x number or imply measured constants.

### Meta Engineering - "ZippyDB: Facebook's key value store" (found while verifying)
- **Source:** https://engineering.fb.com/2021/08/06/core-infra/zippydb/ (the 2026 post links it).
- **Verified?** read directly (skimmed for client/connection content).
- **Key idea:** Describes the store behind the gateway: RocksDB + Data Shuttle (Multi-Paxos), physical shards of 50-100 GB holding tens of thousands of micro-shards, ShardManager for placement, Akkio for micro-shard placement, selectable consistency (eventual, read-your-writes via client-cached sequence numbers, strong via primary).
- **What Mini-ZGateway would concretely borrow:** Nothing mechanical. One caution for the coalescer: reads carry a consistency level, so "identical read" must include the consistency/isolation mode in the coalescing key (spec 2.2 already says this).
- **What NOT to copy:** Everything about sharding, replication and placement; irrelevant to one vanilla backend.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - context for Variant C only.

### ServiceRouter / Akkio / Shard Manager posts and papers
- **Source:** ServiceRouter (atscaleconference.com link in the ZGateway post; OSDI '23 paper), Akkio (OSDI '18), Shard Manager (SOSP '21).
- **Verified?** could not verify (from memory) - not fetched this run; only their mentions inside the two Meta posts were read.
- **Key idea:** ServiceRouter is Meta's service mesh (discovery, weighted consistent-hash routing); Akkio migrates micro-shards near their readers; Shard Manager places shards.
- **What Mini-ZGateway would concretely borrow:** Nothing.
- **What NOT to copy:** All of it; fleet-scale routing and placement.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE - no single-backend relevance; do not spend Phase 0 time here.

---

## 2. Cloudflare Hyperdrive

### Hyperdrive docs - "How Hyperdrive works", "Connection pooling", "Connection lifecycle", "Limits"
- **Source:** https://developers.cloudflare.com/hyperdrive/concepts/how-hyperdrive-works/ , .../concepts/connection-pooling/ , .../concepts/connection-lifecycle/ , .../platform/limits/
- **Verified?** read directly (all four pages).
- **Key idea:** Split the client-facing connection setup from the database-facing pool. The driver handshakes with a Hyperdrive node next to the Worker; a pool near the database holds warm connections. Pooling is transaction mode: a connection is held for one transaction, then returned and `RESET`.
- **What Mini-ZGateway would concretely borrow (pool; Variant B mostly):**
  - Transaction-mode rules, usable as the Variant B compatibility table: `SET` only lasts for the transaction/query (connection is `RESET` on return); transaction-level advisory locks (`pg_advisory_xact_lock*`) work; session-level advisory locks (`pg_advisory_lock*`) are unsupported because the lock would stay on the pooled connection; named prepared statements supported only for specific drivers.
  - Limits to mirror as explicit config: initial connection timeout 15 s, idle connection timeout 10 min, max query duration 60 s, min pool 5, max origin connections ~20 (free) / ~100 (paid).
  - A distinct error for pool exhaustion: "Failed to acquire a connection from the pool." - a pre-forward rejection, separate from "Server connection attempt failed".
  - The warning that a long transaction pins a connection and starves the pool.
- **What NOT to copy:**
  - `max_size` is a SOFT limit ("may temporarily create additional connections"). The spec demands a HARD cap (invariant 4.2); do the opposite.
  - The edge/origin split exists to save cross-region round trips (docs say 7: TCP 1 + TLS 3 + auth 3). A one-box proxy gains nothing from splitting.
  - No documented creation-rate cap or wait-queue bound; the spec's bounds 2 and 3 have no Hyperdrive precedent.
- **Strength:** CORE for Variant B, SECONDARY for Variant A
- **Recommendation:** ADOPT WITH CHANGE - take the transaction-mode rule table and named limits; make the cap hard.

### Hyperdrive docs - "Query caching"
- **Source:** https://developers.cloudflare.com/hyperdrive/concepts/query-caching/
- **Verified?** read directly.
- **Key idea:** Read queries are cached by default; writes are not. Classification uses the wire protocol plus function volatility. No invalidation on write: a cached read can be stale until TTL.
- **What Mini-ZGateway would concretely borrow (coalescer read/write classification; Variant C cache):**
  - Defaults: `max_age` = 60 s, `stale_while_revalidate` = 15 s, max `max_age` 1 hour, responses over 50 MB not cached.
  - The classification rule: mutating statements are never cached, and queries calling `VOLATILE` or `STABLE` functions (`NOW()`, `RANDOM()`, `CURRENT_TIMESTAMP`, `LASTVAL()`, `TXID_CURRENT()`...) are not cacheable; only `IMMUTABLE` is safe. The same test applies to coalescing in Variant B: two textually identical `SELECT now()` are arguably coalescable within one instant, but a `SELECT nextval(...)` is not a read at all.
  - The escape hatch pattern: a second, cache-disabled endpoint for reads that must be fresh.
- **What NOT to copy:** TTL caching itself in v1. Coalescing only shares a result among requests that overlapped in time; a 60 s cache changes read-after-write semantics and is Variant C scope. (A February 2026 changelog made STABLE functions uncacheable - seen only in search results, not read.)
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - reuse the classifier idea for "is this safe to coalesce"; defer caching to Variant C.

### Cloudflare Blog - "Inside Hyperdrive: How we solved a rare Hyperdrive MySQL caching bug"
- **Source:** https://blog.cloudflare.com/hyperdrive-mysql/ . Real title has "Hyperdrive" twice (spec drops the second). Date: 14 Oct 2026 per a syndicated copy's URL seen in search results; the page text I fetched showed no date.
- **Verified?** read directly.
- **Key idea:** ~0.001% of queries on cache-enabled MySQL configs returned empty results or hung. Hyperdrive has two nodes (Client next to the Worker, Endpoint next to the DB) which share one cache when in the same data center. The Client cached "exactly the bytes written back to the Worker" - an assumption carried from Postgres. In MySQL those bytes are translated per client capability flags (e.g. `CLIENT_DEPRECATE_EOF`, which `mysql2` does not negotiate), so one node read bytes in the other's encoding.
- **What Mini-ZGateway would concretely borrow (coalescer + protocol + testing):**
  - Rule: a shared result must be stored in ONE canonical encoding and re-encoded per recipient. For Variant A this is concrete: RESP2 and RESP3 clients can coalesce on the same key, so the coalescer must hold a parsed/canonical reply and encode per waiter's negotiated protocol (`HELLO 3`), never fan out raw backend bytes.
  - Pool reset lesson: MySQL `COM_RESET_CONNECTION` reset charset to the server default, not the negotiated one. After any connection reset, re-apply negotiated session state. Redis analogue: `SELECT` db, `CLIENT SETNAME`, `HELLO` version on a pooled connection.
  - Test idea: a property test with mixed-protocol waiters on one coalesced read.
- **What NOT to copy:** MySQL specifics; the two-node shared-cache topology.
- **Strength:** CORE (as a lesson, not as code)
- **Recommendation:** ADOPT - "canonical form in the shared slot, translate at the edge" becomes a coalescer invariant.

### Cloudflare Blog - "Pools across the sea: how Hyperdrive speeds up access to databases..." (found while verifying)
- **Source:** https://blog.cloudflare.com/how-hyperdrive-speeds-up-database-access/ (2025; exact date not captured).
- **Verified?** read directly (pooling and architecture sections).
- **Key idea:** Explains the three pooler modes (session / transaction / statement), why Hyperdrive chose transaction mode, and the core difficulty: keeping the driver's idea of connection state aligned with the pooled connection's real state.
- **What Mini-ZGateway would concretely borrow (pool, Variant B):**
  - Invariant: every connection is "idle and ready for a query" when checked out.
  - Prepared statements: track which statements each client prepared and which each backend connection has; on a miss, replay the Parse sequence on the checked-out connection before the query.
  - Connection cost baseline: "a bare minimum of 5 round trips" to open a Postgres connection (this post; the docs page says 7 - the two Cloudflare sources disagree, cite whichever with its source).
- **What NOT to copy:** Prepared-statement replay is a large feature; the spec's Variant B is "simple query subset", so reject extended-protocol messages with a clear error instead.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - adopt the checkout-state invariant; explicitly exclude prepared-statement replay.

---

## 3. Canva Engineering

### Canva - "Session revocations at scale"
- **Source:** https://www.canva.dev/blog/engineering/session-revocations-at-scale/ (22 Jul 2026, Llew Vallis - date/author from search results).
- **Verified?** read directly.
- **Key idea:** Gateways keep 12 hours of session revocations in memory. Seeding that cache from MySQL at deploy time was "a coordinated stampede on our database" (hundreds of pods each pulling over a million rows). Fix: a worker writes revocations into sorted 16-byte-record chunks in S3 (30-minute segments, conditional PUT for optimistic concurrency); gateways download chunks and binary-search them in place. Memory down 87.5%; DB replicas cut to two.
- **What Mini-ZGateway would concretely borrow:** Only the failure pattern, as motivation for the reconnection-storm chaos test: startup/deploy is a correlated load event, and the fix is to bound the rate of the expensive warm-up operation. That maps to the pool's creation-rate cap (pool).
- **What NOT to copy / where it misleads:** The spec summarises this as "scaling gateway-side reads", which oversells its relevance. It is about cache seeding via object storage, not proxying, pooling, coalescing or admission. Nothing here is implementable in Mini-ZGateway.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE - cite at most as a one-line stampede anecdote; no mechanism to borrow.

### Canva - "Worker Backpressure (Part 1)"
- **Source:** https://www.canva.dev/blog/engineering/worker-backpressure-part-1-how-we-taught-our-queue-workers-to-slow-down/ (17 Sep 2026, Mikalai Barysau - date/author from search results).
- **Verified?** read directly.
- **Key idea:** A local feedback loop in queue workers. Each dependency call's outcome (success/failure) feeds a pluggable controller holding one number, the backoff factor, from 0.0 (full speed) to 1.0 (fully backed off). Below a set-point failure rate the controller does nothing; above it, it scales down the worker's concurrency permits. No coordinator, "two arithmetic operations" per event.
- **What Mini-ZGateway would concretely borrow (concurrency-limit, benchmarking):**
  - The controller shape: `Controller` interface; inputs = per-call outcomes; output = a factor applied to the max in-flight permits; a set point that defines "background noise" (their production value: 5% failure rate).
  - Their comparison of alternatives is a ready-made design-rationale paragraph: fixed rate limits go stale; circuit breakers have no ramp and release a flood on close; exponential backoff is per-message, not per-dependency.
  - Evidence style: incident tables with success/error counts and DLQ growth.
- **What NOT to copy:**
  - Part 1 withholds the algorithm: how the factor moves, how a fully backed-off worker probes for recovery, and how to pick the two knobs are all deferred to an unpublished Part 2. Do not invent them and attribute them to Canva.
  - Error-rate is the only signal. A stalled database (the spec's 200 ms stall scenario) produces latency, not errors, until timeouts fire; latency or in-flight count must be the primary signal.
  - A worker can leave work in a durable queue; a proxy has a waiting client, so backing off means shedding with an explicit error.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - take the pluggable-controller interface and set-point idea; use a latency signal; get the control law from AIMD/Vegas sources (other briefs).

---

## 4. "Fail at Scale" and its write-up

### Ben Maurer - "Fail at Scale: Reliability in the face of rapid change" (ACM Queue 13(8), 27 Oct 2015)
- **Source:** https://queue.acm.org/detail.cfm?id=2839461
- **Verified?** read directly, via the Wayback Machine copy (https://web.archive.org/web/2020id_/https://queue.acm.org/detail.cfm?id=2839461); the live queue.acm.org page returned a Cloudflare 403.
- **Key idea:** Latency-driven resource exhaustion turns a small failure into a site-wide one, because queues fill with requests nobody is waiting for. Three defences: a CoDel-inspired queue timeout, adaptive LIFO, and client-side concurrency control.
- **What Mini-ZGateway would concretely borrow (admission, errors+retries):**
  - Controlled delay, exactly as printed:
    ```
    onNewRequest(req, queue):
      if (queue.lastEmptyTime() < (now - N seconds)) {
         timeout = M ms
      } else {
         timeout = N seconds;
      }
      queue.enqueue(req, timeout)
    ```
    Constants: "a value of 5 milliseconds for M and 100 ms for N tends to work well". State: one timestamp, `lastEmptyTime`. NOTE the article is internally inconsistent: the pseudo-code says "N seconds", the prose says N milliseconds and gives N = 100 ms. Use milliseconds.
  - Adaptive LIFO: FIFO normally; "when a queue is starting to form, the server switches to LIFO mode". No threshold is given in the article.
  - Concurrency control: client tracks outstanding requests per service; above a configurable number, new requests are "immediately marked as an error". This is the proxy's per-backend in-flight cap with a pre-forward rejection.
  - Stated rationale worth quoting: a short timeout "ensures that the server always accepts just a little bit more work than it can actually handle so it never goes idle."
- **What NOT to copy:** M = 5 ms was tuned on services with fast handlers. In the spec's own chaos scenarios the backend takes 50-200 ms per call; with a full pool, every queued request waits longer than 5 ms and is shed. M and N must be configurable and reasoned against backend service time. The config-rollout, fire-drill, Cubism and DERP sections are out of scope.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - this is the admission queue's control rule; keep M/N as named config with the article's values as documented defaults, not as truths.

### The Morning Paper - "Fail at Scale & Controlling Queue Delay" (Adrian Colyer, 19 Nov 2015)
- **Source:** https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/
- **Verified?** read directly via Wayback (https://web.archive.org/web/2019id_/https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/); live site returned 403.
- **Key idea:** One post that summarises both articles: first the three Facebook techniques, then network CoDel in its original setting. It reproduces the Facebook pseudo-code verbatim (including the "N seconds" inconsistency) and quotes the CoDel paper's description of the control law.
- **What Mini-ZGateway would concretely borrow:** Nothing beyond the two primaries. Its one useful framing: "The Facebook variant performs the equivalent of dropping packets by controlling the timeout for messages placed into a queue."
- **What NOT to copy:** It presents the two algorithms back to back without spelling out that they differ in mechanism; reading only this summary is how the two get conflated. It adds no constants of its own.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - a reading aid; cite the primaries.

---

## 5. CoDel proper

### Nichols & Jacobson - "Controlling Queue Delay" (ACM Queue 10(5), May 2012)
- **Source:** https://queue.acm.org/detail.cfm?id=2209336
- **Verified?** read directly via Wayback (https://web.archive.org/web/2020id_/https://queue.acm.org/detail.cfm?id=2209336); live page 403. The pseudo-code appendix (queue.acm.org/appendices/codel.html) was not fetched; pseudo-code was read from RFC 8289 instead.
- **Key idea:** Distinguish a good queue (a burst that drains) from a bad queue (a standing queue that never drains). Measure each packet's sojourn time at dequeue; track the minimum over a sliding interval. If the minimum stays above `target` for a whole `interval`, drop a packet and then drop at an increasing rate until delay falls below target.
- **What Mini-ZGateway would concretely borrow (admission, testing):**
  - The measurement choices: sojourn time (not queue length), minimum (not average), a timestamp added at enqueue, all decisions at dequeue.
  - Constants and their meaning: target 5 ms = "acceptable standing queue delay"; interval 100 ms = "on the order of a worst-case RTT". Paper: below 5 ms utilisation suffers; 100 ms works for RTTs from 10 ms to 1 s.
  - The good-queue/bad-queue vocabulary for the README and for the anti-bufferbloat invariant.
- **What NOT to copy:** The control law. Dropping at `interval/sqrt(count)` spacing exists because TCP throughput falls with the square root of drop probability; a dropped packet makes a TCP sender slow down. A shed Redis/Postgres request makes no sender slow down (clients may retry faster). Also the "never drop when under one MTU of bytes" rule has no request-queue equivalent beyond "never shed when the queue is empty".
- **Strength:** CORE (concepts), not the control law
- **Recommendation:** ADOPT WITH CHANGE - adopt sojourn time + interval-minimum detection; do not implement the sqrt drop scheduler.

### RFC 8289 - "Controlled Delay Active Queue Management" (Experimental, January 2018)
- **Source:** https://www.rfc-editor.org/rfc/rfc8289.txt
- **Verified?** read directly (sections 3.2, 4.2-4.4, 5.2-5.6).
- **Key idea:** The normative write-up of network CoDel with full pseudo-code (the Linux variant).
- **What Mini-ZGateway would concretely borrow (admission, testing):** Exact reference behaviour, useful if a "CoDel proper" comparison mode is wanted in the simulator:
  - State: `first_above_time_`, `drop_next_`, `count_`, `lastcount_`, `dropping_`. Constants: `TARGET = 5 ms`, `INTERVAL = 100 ms`.
  - `dodequeue`: if `sojourn < TARGET` or queue holds <= one max packet, `first_above_time_ = 0`; else if `first_above_time_ == 0` set it to `now + INTERVAL`; else if `now >= first_above_time_`, `ok_to_drop`.
  - Enter drop state: drop one; `count_ = (delta > 1 && now - drop_next_ < 16*INTERVAL) ? delta : 1` where `delta = count_ - lastcount_`; `drop_next_ = control_law(now, count_)`.
  - In drop state: `while (now >= drop_next_ && dropping_) { drop; ++count_; ... drop_next_ = control_law(drop_next_, count_) }`; leave when sojourn < TARGET.
  - `control_law(t, count) = t + INTERVAL / sqrt(count)`.
  - Tuning guidance: TARGET should be 5-10% of INTERVAL; INTERVAL slightly larger than the RTT most flows see. Translated: interval ~ the client's reaction time (its timeout/retry period), target a small fraction of it.
- **What NOT to copy:** As the primary algorithm, for the reason above. ECN marking, MTU logic, fq_codel.
- **Strength:** SECONDARY
- **Recommendation:** WATCH ONLY - keep as the precise reference and as an optional sim baseline; not the production path.

### folly `Codel.h` / `Codel.cpp` (found while verifying)
- **Source:** https://raw.githubusercontent.com/facebook/folly/main/folly/executors/Codel.h and `Codel.cpp`. ("Fail at Scale" cites wangle's `wangle/concurrent/Codel.cpp`; that path now returns 404 - the class lives in folly.)
- **Verified?** read directly (both files).
- **Key idea:** Facebook's shipped request-queue CoDel. It is neither the paper's algorithm nor the article's pseudo-code. Header comment: "if every request has experienced queueing delay greater than the target (5ms) during the past interval (100ms), then we shed load", and "Instead of adapting the interval until the next drop, we slough off requests with queueing delay > 2*target_delay while in the overloaded regime. This empirically works better for our services".
- **What Mini-ZGateway would concretely borrow (admission):** A complete, tiny decision rule, called at dequeue with the request's queue delay:
  - State: `minDelay`, `intervalEnd`, `resetDelay` (bool), `overloaded` (bool). Defaults: interval 100 ms, target 5 ms (gflags `codel_interval`, `codel_target_delay`).
  - When `now > intervalEnd`: `overloaded = (minDelay > target)`; start a new interval; the first sample of the interval seeds `minDelay` and is never dropped.
  - Otherwise `minDelay = min(minDelay, delay)`.
  - Drop this request iff `overloaded && delay > 2 * target` (slough timeout = 10 ms by default).
  - `getLoad() = min(100, 100 * minDelay / (2*target))` - a free 0-100 overload gauge for metrics.
  - `setOptions` rejects `interval <= target`.
- **What NOT to copy:** The atomics/lock-free structure (the Go version can sit under the queue mutex, which also keeps the simulator deterministic). It only decides at dequeue: if the backend is fully stalled nothing dequeues and nothing is shed, so a separate deadline/expiry sweep and the MaxQueueDepth bound are still required.
- **Strength:** CORE
- **Recommendation:** ADOPT - the best-specified request-queue variant; pair with adaptive LIFO and a hard depth cap.

### HHVM `job-queue.h` - adaptive LIFO (found while verifying)
- **Source:** https://raw.githubusercontent.com/facebook/hhvm/43c20856239cedf842b2560fd768038f52b501db/hphp/util/job-queue.h (the exact commit "Fail at Scale" cites).
- **Verified?** read directly (header comment and constructor).
- **Key idea:** The concrete adaptive-LIFO rule the article only gestures at: a queue-LENGTH threshold. "If the job queue is configured to be in FIFO mode, and the current queue length exceeds lifoSwitchThreshold, then the workers will begin work on requests in LIFO order until the queue size is below the threshold in which case we resume in FIFO order."
- **What Mini-ZGateway would concretely borrow (admission):** Dequeue rule `if jobCount >= lifoSwitchThreshold { take newest } else { take oldest }`, on a deque. Also present: `maxJobQueuingMs` (expire jobs queued too long) and multiple priority queues. Default threshold is `INT_MAX` (off); no recommended value is given.
- **What NOT to copy:** A length threshold needs per-deployment tuning, which is what CoDel was meant to avoid. The spec instead ties the LIFO switch to the standing-queue detector (time-based). That is a reasonable design but note it is the spec's synthesis, not what HHVM does.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - use the deque + switch structure; drive the switch from the CoDel `overloaded` flag (see steering question 2).

### joshbohde/codel - Go implementation (found while verifying)
- **Source:** https://github.com/joshbohde/codel (`codel.go`, README).
- **Verified?** read directly (README; `codel.go` skimmed by grep, not line by line).
- **Key idea:** A Go lock/semaphore (`Acquire`/`Release`) with `MaxPending`, `MaxOutstanding`, `TargetLatency`. Unlike folly it ports the NETWORK control law to requests: `dropNext`, `droppedCount`, `interval / sqrt(droppedCount)`, with a hard-coded `interval = 10 * time.Millisecond`. README sim (1000 req/s in, 500 req/s out, 10 s): plain queue p50 963 ms vs codel p50 27.7 ms at equal throughput.
- **What Mini-ZGateway would concretely borrow (admission API, benchmarking):** The API shape (admission as an acquire/release limiter with pending and outstanding caps) and the 2x-overload before/after table as a benchmark template.
- **What NOT to copy:** The code (zero-dependency rule) and the sqrt control law. Shows that Go implementations disagree with folly about what "CoDel for requests" means.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - evidence for the fork in the road; not a model.

---

## 6. Benchmark rigor

### Apollo GraphQL - "How we know a router performance result is real" (Dan Anderson Hall)
- **Source:** https://www.apollographql.com/blog/how-we-know-a-router-performance-result-is-real ; part one: https://www.apollographql.com/blog/apollos-runtime-testing-framework
- **Verified?** read directly (full post; part one fetched, skimmed). Publication date was not visible in the fetched text, so the spec's "Oct 2026" is unconfirmed (it validates router v2.18.0 against v2.17.0).
- **Key idea:** A result is only usable if identical re-runs agree closely enough to separate change from noise. Results pass three gates in order: is the run valid, do repeats agree, has the version changed by more than a pre-declared margin. "We don't know" is a reportable verdict.
- **What Mini-ZGateway would concretely borrow (benchmarking):**
  - *Isolation:* the load generator never shares resources with the system under test; seeded mock backend with fixed artificial latency ("Seeding alone cut run-to-run variation in client latency from 244% to about 11%").
  - *Repeats:* 5 repeats per configuration (they say 5 was too few for p99 and memory).
  - *Gate 1 (valid run):* loadgen delivered within 2% of target rate; steady state reached; no metric still drifting >2% per 4 minutes; CPU throttled <5% of the time. Failed runs are discarded, not averaged in.
  - *Gate 2 (repeats agree):* coefficient of variation below 15% for single numbers; for latency distributions, Wasserstein distance between repeats compared with the distance between two halves of the same run.
  - *Gate 3 (changed?):* a pre-declared equivalence zone (+-10% per percentile, +-5% for single numbers); verdicts Equivalent / Regression / Improvement / Inconclusive depending on whether the confidence interval lies inside, beyond, or across the zone.
  - *Negative control:* a metric the change cannot affect (subgraph latency) moved <0.5%.
  - Whole distributions detect ~2-3% shifts versus ~6.5% for a single p99 figure.
  - Their v2.18.0 tally as an honesty model: 500 runs, 404 clean, 45 set aside; of 50 client-latency comparisons 16 equivalent, 27 inconclusive, 7 not comparable.
- **What NOT to copy:** The post does not say how the confidence interval is computed (no test named), so that must come from elsewhere. It is a regression harness for effects of a few percent; Mini-ZGateway's headline effects are 10x-1000x (connections, hot-key backend QPS), where five repeats and a min/max range suffice. Kubernetes namespaces, k6 and 500-run matrices are out of scope. No explicit A/A test is described beyond the negative control and split-half comparison.
- **Strength:** SECONDARY (CORE for the README's credibility)
- **Recommendation:** ADOPT WITH CHANGE - implement Gate 1 (achieved-rate within 2%, warm-up, steady state) and repeats with CV in `make bench`; report "inconclusive" honestly; skip Wasserstein and the matrix.

---

## 7. Related primary source

### Bronson, Aghayev, Charapko, Zhu - "Metastable Failures in Distributed Systems" (HotOS '21) (found while verifying)
- **Source:** https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf
- **Verified?** read directly (PDF converted to text; sections 2.1, 2.4 and 3 read, rest skimmed).
- **Key idea:** A trigger pushes a loaded system into a bad state that persists after the trigger is gone, because a sustaining feedback loop (most often retries) keeps load above capacity. The root cause is the sustaining loop, not the trigger.
- **What Mini-ZGateway would concretely borrow (errors+retries, pool, testing):**
  - A worked retry example to reproduce as the retry-storm chaos test: DB healthy under 300 QPS; app at 280 QPS with one retry after 1 s; a 10 s outage; afterwards load sits at 560 QPS and goodput stays at zero until load or retry policy changes. The pass criterion is therefore "goodput recovers after the trigger is removed", not "survives during the fault".
  - Overload detection described in section 3 matches folly: minimum queueing latency over a window; if it stays large, switch to a goodput-maximising policy "and add information about the overload to all responses" - support for a distinct OVERLOADED reply.
  - Give retried requests lower priority so they cannot sustain the loop.
  - Section 2.4: a connection pool's MRU reuse policy sustained an outage; "the fix was a single line that changed the connection pool's policy". Pool pick order (LIFO/MRU vs FIFO/LRU) is a design decision to record, not an accident.
- **What NOT to copy:** Production stress-testing (Kraken) and fleet-scale reasoning. The paper warns small-scale tests give limited confidence about large-scale feedback loops; do not claim more than the simulator shows.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT - shapes the retry-storm test's pass criterion and justifies retry budgets better than the spec's 3x3x3 arithmetic.

---

## 8. Companion documents (Nick's Google Docs)

### "Project 2: High-Throughput Analytics & Edge Gateway (Go)" and "Project 4: ZeroMQ Remake in Go"
- **Source:** local faithful copies at `/root/Software/analytics-edge-gateway/docs/project-source.md` and `/root/Software/zeromq-go/docs/project-source.md`. Each repo also has `README.md`, `PLAN.md`, `STATE.md`.
- **Verified?** read directly for existence and headers only (file headers say "Faithful copy of Nick's Google Doc (2026-11-20)" and "Source: Nick's Google Doc 'Project 4: ...'"). Content and code deliberately not analysed (brief B2 owns that).
- **Key idea:** Sibling projects the spec names as sources of a reusable GCRA rate limiter and workload fuzzing (Project 2) and sans-I/O framing plus a backpressure simulator (Project 4).
- **What Mini-ZGateway would concretely borrow:** See B2.
- **What NOT to copy:** See B2.
- **Strength:** SECONDARY
- **Recommendation:** WATCH ONLY here - defer to B2.

### "Master Systems Engineering Projects Roadmap" and "Systems Engineering Deep-Dives - Master Reading List"
- **Source:** Google Docs; no local copy found.
- **Verified?** could not verify. Searched `/root/Software/*/` (markdown grep for both titles), `/root/Software/research` (contains only `graphduck`), `/root/Software/portfolio`, `/root/briefs`, and filename search for `*roadmap*` / `*reading-list*` under `/root` (depth 3). The only hits are the titles being mentioned inside project-source copies and the briefs. Also found, unrelated to these titles: `/root/Software/object-store/docs/project-source.md`, `/root/briefs/gateway-*.md`, `/root/briefs/zeromq-*.md`, `/root/briefs/backpressure-phase1-brief.md`.
- **Key idea:** Unknown; not readable.
- **What Mini-ZGateway would concretely borrow:** Nothing can be stated.
- **What NOT to copy:** n/a.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE from INFLUENCES.md unless Nick exports them; they cannot be cited as read.

---

## CoDel: which one (the decision the brief asked for)

| | Network CoDel (paper, RFC 8289) | "Fail at Scale" pseudo-code | folly `Codel` | joshbohde/codel (Go) |
|---|---|---|---|---|
| Overload detector | min sojourn above target for a full interval | queue has not been empty for N | min queue delay over the last interval > target | sojourn above target for interval |
| Decision point | dequeue | enqueue (assigns a timeout) | dequeue | dequeue of waiters |
| What gets shed | one packet, then more at `interval/sqrt(count)` spacing | any request whose wait exceeds M (5 ms) while the queue is standing; N (100 ms) otherwise | any request whose wait exceeds 2 x target (10 ms) while overloaded | waiters, at `interval/sqrt(count)` spacing |
| State | 5 variables | 1 timestamp (`lastEmptyTime`) | min delay, interval end, 2 flags | similar to network CoDel |
| Relies on | TCP senders slowing down after a drop | nothing from the sender | nothing from the sender | nothing, but uses a law designed for TCP |
| Constants | target 5 ms, interval 100 ms | M 5 ms, N 100 ms | target 5 ms, interval 100 ms, slough 2 x target | interval hard-coded 10 ms, target configurable |

**Recommendation: the Facebook request-queue family, specifically folly's rule** (interval-minimum detector + shed anything waiting longer than 2 x target while overloaded), combined with adaptive LIFO and a hard queue-depth cap. Reasons:

- A database client does not reduce its send rate when one request is shed, so the sqrt drop schedule has no feedback loop to drive. Shedding by a wait-time threshold bounds latency directly.
- The folly rule is fully specified in code that was read; the article's pseudo-code is ambiguous (units) and assigns the timeout at enqueue, which is awkward once LIFO reorders the queue.
- Deciding at dequeue with one mutex-guarded struct is easy to drive from a virtual clock and gives bit-identical traces.
- The article's `lastEmptyTime` test is the simpler mental model and matches the spec's wording ("queue never empties for N ms"). The two detectors are close: a queue that emptied during the interval has a near-zero minimum delay. Document the choice either way.

Two additions the sources do not give and the project must design itself: (1) an expiry path for when nothing dequeues (total backend stall), and (2) target/interval chosen relative to backend service time rather than copied as 5 ms / 100 ms.

---

## Conflicts / tensions noticed

1. **Hard cap vs soft cap.** Hyperdrive's `max_size` is a soft limit that may be exceeded "to ensure high availability"; the spec's invariant is a hard cap. Hard cap costs availability during backend failover (requests shed while connections are replaced); soft cap costs the one guarantee the project exists to demonstrate. Take the hard cap.
2. **5 ms target vs slow-backend scenarios.** Every CoDel source says 5 ms/100 ms "needs no tuning". The spec's chaos tests inject 50-200 ms backend latency. With 5 ms the queue sheds almost everything that cannot get a connection immediately (protects the backend, hurts success rate); with a larger target, more requests succeed but tail latency grows. The spec's own invariant ("Tenant B p99 within 2x baseline") is the tie-breaker.
3. **Adaptive LIFO vs per-tenant round-robin.** "Fail at Scale" uses one queue and flips its order; ZGateway uses per-tenant buckets drained round-robin. Global LIFO lets a flooding tenant's fresh requests jump ahead of a quiet tenant's; per-tenant FIFO buckets give fairness but lose "freshest first". Combining them (round-robin across tenants, LIFO within a tenant when overloaded) is not described in any source read.
4. **Token bucket placement.** ZGateway: one shared token bucket whose rate is AIMD-controlled, in front of per-tenant buckets. Spec: per-tenant token/GCRA budgets. Per-tenant static budgets need configuration per tenant; ZGateway's structure isolates without per-tenant rates.
5. **Error-rate signal (Canva) vs latency signal (spec, CoDel).** Errors are unambiguous but late for a stalled database; latency is early but noisy. The spec's stall scenarios need latency.
6. **Batching vs CoDel accounting.** ZGateway parks requests for a linger window on purpose. If that wait counts as queue sojourn it pushes toward shedding; if it does not, the CoDel target no longer bounds total proxy delay. No source addresses this.
7. **Model vs measurement.** Meta headlines a modelled 19x; Apollo refuses to state anything not surviving three gates. The README should follow Apollo: report measured connection counts, and label any formula-derived number as a model.
8. **Cache vs coalesce.** Hyperdrive caches for 60 s with no invalidation; ZGateway's cache uses CDC invalidation with a staleness contract. Both are far past singleflight. Caching adds a correctness burden (the MySQL bug is the example) for a benefit the v1 benchmarks do not need.

## Variant implications

- **Variant A (Redis RESP):** ZGateway (KV gets/puts, shard-keyed batching maps to pipelining/MGET, hot-key coalescing), folly Codel, HHVM adaptive LIFO, "Fail at Scale", metastable failures, Canva backpressure, Apollo. The Hyperdrive MySQL bug applies directly through RESP2/RESP3 mixed clients.
- **Variant B (Postgres wire):** Hyperdrive docs and "Pools across the sea" become CORE: transaction-mode rules, `RESET` on return, session advisory locks unsupported, prepared-statement tracking, pool-exhaustion error, 5-7 round-trip connection cost (which is what makes the creation-rate cap and the reconnection-storm test most dramatic on Postgres). Coalescing is much harder: needs the volatility classifier and must stay outside transactions.
- **Variant C only:** Hyperdrive query caching (max_age/stale_while_revalidate), ZGateway's read cache with CDC invalidation, weighted load balancer and cross-region failover, ZippyDB/Akkio/ShardManager/ServiceRouter context.
- **Variant-neutral:** all CoDel material, Apollo, metastable failures, Canva backpressure.
- Net: the spec-named sources split evenly; ZGateway's mechanisms favour A, Hyperdrive's favour B. Nothing here argues against the spec's default of A first.

## Steering questions this bucket creates for Nick

1. **Which CoDel?** (a) folly rule: interval-min detector, shed waits > 2 x target; (b) "Fail at Scale" rule: `lastEmptyTime`, timeout M vs N; (c) network CoDel with sqrt control law. **Recommended: (a)**, with (c) optionally as a simulator baseline to show why it is the wrong fit.
2. **What triggers the FIFO to LIFO switch?** (a) the CoDel overloaded flag (time-based, spec's wording); (b) a queue-length threshold as in HHVM. **Recommended: (a)** - one detector, no extra knob.
3. **CoDel constants.** (a) fixed 5 ms / 100 ms as in every source; (b) configurable with 5/100 defaults; (c) derived from observed backend service time. **Recommended: (b)**, with the README showing one scenario where 5 ms is wrong for a 50 ms backend.
4. **Fairness structure.** (a) per-tenant buckets drained round-robin, ZGateway-style, with shedding from the fullest bucket; (b) per-tenant token bucket/GCRA rates as the spec lists. **Recommended: (a) as the core, with an optional per-tenant rate cap** - it isolates without per-tenant tuning and has the strongest cited evidence (6 of ~1,350 buckets shed).
5. **How to state the connection headline.** (a) "~15x" target as in the spec; (b) report measured clients -> backend connections and call ZGateway's 19x a model. **Recommended: (b).**
6. **Benchmark rigor level.** (a) Apollo-lite: 5 repeats, achieved-rate gate, warm-up, CV reported, "inconclusive" allowed; (b) single run per scenario; (c) full three-gate statistics. **Recommended: (a).**
7. **Coalesced reply representation.** (a) canonical parsed reply, re-encoded per waiter; (b) share raw backend bytes and restrict coalescing to waiters with identical protocol settings. **Recommended: (a)** if RESP3 is supported at all; (b) is acceptable if v1 is RESP2-only, but record it as an invariant.
8. **Do the two Canva posts stay in INFLUENCES.md?** **Recommended:** keep Worker Backpressure (secondary), drop Session revocations.

## Verification log

| URL / path | Result |
|---|---|
| `/root/Software/mini-zgateway/docs/project-source.md` | read |
| `kb search` (cwd, `--global`, `/root/Raw/knowledge`) | failed: "no kb location found"; no searches recorded, so no feedback to file |
| `websearch search "ZGateway ZippyDB proxy engineering.fb.com"` | useless (Bing returned unrelated results) |
| built-in WebSearch (4 queries: ZGateway, Canva x2, Hyperdrive MySQL) | worked; used only to locate URLs and dates |
| https://engineering.fb.com/?s=zgateway | fetched; "1 result" but listing not rendered |
| https://engineering.fb.com/2026/09/03/core-infra/zgateway-proxy-zippydb-meta/ | read in full; formula/table images missing |
| https://www.infoq.com/news/2026/09/meta-zgateway-zippydb-proxy/ | read |
| https://engineering.fb.com/2021/08/06/core-infra/zippydb/ | read (skim) |
| ServiceRouter / Akkio / Shard Manager sources | not attempted |
| https://developers.cloudflare.com/hyperdrive/concepts/how-hyperdrive-works/ | read |
| https://developers.cloudflare.com/hyperdrive/concepts/connection-pooling/ | read |
| https://developers.cloudflare.com/hyperdrive/concepts/query-caching/ | read |
| https://developers.cloudflare.com/hyperdrive/concepts/connection-lifecycle/ | read |
| https://developers.cloudflare.com/hyperdrive/platform/limits/ | read |
| https://blog.cloudflare.com/hyperdrive-mysql/ | read in full |
| https://blog.cloudflare.com/how-hyperdrive-speeds-up-database-access/ | read (pooling/architecture/caching sections) |
| https://www.canva.dev/blog/engineering/ (index) | fetched; cookie banner only |
| https://www.canva.dev/blog/engineering/session-revocations-at-scale/ | read in full |
| https://www.canva.dev/blog/engineering/worker-backpressure-part-1-how-we-taught-our-queue-workers-to-slow-down/ | read in full |
| Canva "Worker Backpressure Part 2" | not published per Part 1 ("coming soon"); not searched further |
| https://queue.acm.org/detail.cfm?id=2839461 (live) | 403 bot challenge (websearch and curl) |
| https://web.archive.org/web/2020id_/https://queue.acm.org/detail.cfm?id=2839461 | read in full |
| https://queue.acm.org/detail.cfm?id=2209336 (live) | 403 |
| https://web.archive.org/web/2020id_/https://queue.acm.org/detail.cfm?id=2209336 | read (algorithm sections; simulation results skimmed) |
| queue.acm.org/appendices/codel.html | not attempted |
| https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/ (live) | 403 |
| https://web.archive.org/web/2019id_/https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/ | read |
| https://www.rfc-editor.org/rfc/rfc8289.txt | read via curl (websearch fetch rejects text/plain) |
| https://datatracker.ietf.org/doc/html/rfc8289 | fetched; not used |
| https://raw.githubusercontent.com/facebook/folly/main/folly/executors/Codel.h , Codel.cpp | read in full |
| https://raw.githubusercontent.com/facebook/wangle/master/wangle/concurrent/Codel.cpp | 404 |
| https://raw.githubusercontent.com/facebook/hhvm/43c2085.../hphp/util/job-queue.h | read (header comment, constructor) |
| https://raw.githubusercontent.com/joshbohde/codel/master/README.md , codel.go | README read; code grepped |
| https://www.apollographql.com/blog/how-we-know-a-router-performance-result-is-real | read in full; no date visible |
| https://www.apollographql.com/blog/apollos-runtime-testing-framework | fetched, skimmed |
| https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf | read (pdftotext; sections 2.1, 2.4, 3) |
| `/root/Software/analytics-edge-gateway/docs/project-source.md` | exists (header read only) |
| `/root/Software/zeromq-go/docs/project-source.md` | exists (header read only) |
| Local copies of "Master ... Roadmap" / "Master Reading List" | not found |
