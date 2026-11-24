# B3 - Industry systems the spec does not name

Research notes for Mini-ZGateway Phase 0. Run date 2026-11-24. Research only.

**How to read the labels.** `read directly` means I fetched the primary source in this run
and the numbers quoted come from it. Anything marked *(from memory)* inside an entry was not
confirmed this run. GitHub sources were read at the default branch head as of the run date,
so they describe current behaviour, which in places differs from older write-ups (notably
Envoy's Redis proxy now has RESP3 and MULTI support).

None of the sources in this bucket are the mid/late-2026 posts the spec cites, so there was
no "does this post exist" check to make here. One spec claim was checked: the "10% retry
budget" does come from the Google SRE book (entry 16).

---

## Part 1 - Postgres poolers

### 1. PgBouncer
- **Source:** https://github.com/pgbouncer/pgbouncer (`doc/config.md`, `doc/usage.md`, `src/objects.c`), https://www.pgbouncer.org/features.html
- **Verified?** read directly - `raw.githubusercontent.com/pgbouncer/pgbouncer/master/doc/config.md`, `.../doc/usage.md`, `.../src/objects.c` (`launch_new_connection`), `raw.githubusercontent.com/pgbouncer/pgbouncer.github.io/master/features.md`
- **Key idea:** A single-threaded event loop that lends a server connection to a client for a session, a transaction, or one statement. Clients beyond the pool size wait in a per-pool queue. Transaction mode gives the fan-in win but breaks session state "by design".
- **What Mini-ZGateway would concretely borrow:**
  - *pool:* the limit set and its defaults: `max_client_conn` 100, `default_pool_size` 20 per (user, database), `min_pool_size` 0, `reserve_pool_size` 0 with `reserve_pool_timeout` 5 s (extra connections allowed only once the oldest waiter has waited that long), `server_lifetime` 3600 s, `server_idle_timeout` 600 s, `server_connect_timeout` 15 s, `server_check_delay` 30 s (a connection idle longer than this gets a check query before reuse).
  - *pool, creation-rate cap:* `launch_new_connection` opens **one backend connection at a time per pool** (returns early if `new_server_list` is non-empty; the source comment says "Allow only a single connection attempt at a time"). After a failed login it refuses new attempts for `server_login_retry` (15 s) and during that window new clients get an error immediately instead of queueing.
  - *admission:* waiters are served oldest first (`maxwait` = age of the "first (oldest) client in the queue"); `query_wait_timeout` 120 s disconnects a waiter; 0 means "queued indefinitely". Idle servers are reused LIFO unless `server_round_robin` is set.
  - *observability:* the `SHOW POOLS` vocabulary: `cl_active`, `cl_waiting`, `sv_login`, `sv_used`, `sv_tested`, `maxwait`.
  - *protocol (Variant B):* the transaction-mode break list: SET/RESET, LISTEN, WITH HOLD cursors, SQL-level PREPARE/DEALLOCATE, PRESERVE/DELETE ROWS temp tables, LOAD, session advisory locks are "Never". Protocol-level prepared statements work only with `max_prepared_statements` > 0 (default 200): each unique query text gets an internal name `PGBOUNCER_{id}`, client names are rewritten, and the statement is transparently re-prepared on whichever server connection the client lands on; a per-server LRU bounds the count.
- **What NOT to copy:** the 120 s wait timeout and unbounded FIFO queue are exactly the bufferbloat the spec's CoDel queue exists to remove. One-connect-at-a-time is too slow a ramp for a cold start of 20-32 connections; use a small cap (2-4) instead. Skip the prepared-statement rewrite layer unless Variant B is chosen and the extended protocol is in scope.
- **Strength:** CORE (for Variant B), SECONDARY (for A)
- **Recommendation:** ADOPT WITH CHANGE - take the limit taxonomy, the single-flight connect idea and fail-fast-after-login-failure; replace the wait queue.

### 2. PgCat
- **Source:** https://github.com/postgresml/pgcat (`README.md`, `CONFIG.md`)
- **Verified?** read directly - `raw.githubusercontent.com/postgresml/pgcat/main/README.md`, `.../CONFIG.md`
- **Key idea:** PgBouncer's session/transaction pooling rebuilt on a multi-threaded Tokio runtime, plus replica load balancing, failover with a ban list, and sharding. Transaction mode has the same limits (no prepared statements, SET, advisory locks unless the cache is enabled).
- **What Mini-ZGateway would concretely borrow:** *pool:* a short default wait: `connect_timeout` 1000 ms is "how long the client waits to obtain a server connection", versus PgBouncer's 120 s. Health check before handing out a connection idle longer than `healthcheck_delay` 30 s, with `healthcheck_timeout` 1000 ms. *errors:* the ban idea in its smallest form: a backend that fails a check is refused for `ban_time` 60 s. Other defaults: `pool_size` 9 per user, `idle_timeout` 30 s, `server_lifetime` 24 h, `worker_threads` 5.
- **What NOT to copy:** sharding (`SET SHARDING KEY`), replica routing, mirroring, the SQL parser. With one backend a ban list degenerates into a circuit breaker with one host; PgBouncer's `server_login_retry` already covers that.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - only the 1 s wait default and check-before-reuse are worth taking; both are also in PgBouncer.

### 3. Odyssey (Yandex)
- **Source:** https://github.com/yandex/odyssey (`README.md`, `odyssey.conf`)
- **Verified?** read directly - `raw.githubusercontent.com/yandex/odyssey/master/README.md`, `.../odyssey.conf` (the commented reference config)
- **Key idea:** A multi-worker pooler where limits and behaviour are set per route (database + user), and where the pooler cleans up a server connection after an unclean client exit instead of discarding it.
- **What Mini-ZGateway would concretely borrow:** *pool:* the return-to-pool hygiene rules: `pool_rollback yes` (send ROLLBACK if the server was left in a transaction, else close), `pool_cancel yes` (send a Cancel on a side connection if a query is still running, else close), `pool_discard` (DISCARD ALL before reuse, off in the sample). *admission:* `client_max_routing` caps how many clients may be in the accept/startup/TLS phase at once (default 64 x workers), a second, separate bound from total clients. This maps to the spec's reconnection-storm test on the client side. `pool_timeout` (wait for a server, 0 = unlimited), `pool_ttl` 60 s idle.
- **What NOT to copy:** multi-worker threading model (Go's scheduler replaces it), per-route config language, auth passthrough.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - copy the "what do we do with a dirty connection" decision table and the in-handshake client cap.

### 4. Supavisor (Supabase)
- **Source:** https://github.com/supabase/supavisor (`README.md`)
- **Verified?** read directly - `raw.githubusercontent.com/supabase/supavisor/main/README.md`
- **Key idea:** A clustered, multi-tenant pooler on the Erlang VM. A tenant's pool starts when its first client connects and lives on one node; other nodes proxy to it so the per-tenant backend connection count stays bounded cluster-wide.
- **What Mini-ZGateway would concretely borrow:** nothing mechanical. One idea worth a sentence in the README: "tenant" here means a separate database, which is a different meaning from the spec's tenants sharing one backend.
- **What NOT to copy:** cluster membership, dynamic pool placement.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE - solves a multi-node problem the project does not have.

---

## Part 2 - Envoy

### 5. Envoy overload manager
- **Source:** Envoy docs, `docs/root/configuration/operations/overload_manager/overload_manager.rst`
- **Verified?** read directly - `raw.githubusercontent.com/envoyproxy/envoy/main/docs/root/configuration/operations/overload_manager/overload_manager.rst`
- **Key idea:** Resource monitors report a pressure in [0,1]. Triggers map pressure to an action state. Actions and named "load shed points" are checked at fixed places in the request path, cheapest rejection first.
- **What Mini-ZGateway would concretely borrow:** *admission:* (a) the scaled trigger: state = 0 below `scaling_threshold`, `(pressure - scaling)/(saturation - scaling)` in between, 1 above; the doc example uses 0.85 and 0.95. (b) the idea of named shed points in pipeline order: refuse at accept (`tcp_listener_accept`), refuse after parsing the request header, and refuse to open new upstream connections (`connection_pool_new_connection`). (c) `reduce_timeouts`: scale idle timeouts down linearly as pressure rises. *observability:* one counter per shed point.
- **What NOT to copy:** heap/cgroup/CPU monitors and the generic monitor-action registry. The proxy's own "pressure" is queue wait and in-flight count, which the CoDel queue and concurrency limit already measure.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - take the named shed-point list as the structure for the pre-forward rejection codes; skip the framework.

### 6. Envoy adaptive concurrency filter (gradient controller)
- **Source:** `docs/root/configuration/http/http_filters/adaptive_concurrency_filter.rst`, `api/envoy/extensions/filters/http/adaptive_concurrency/v3/adaptive_concurrency.proto`
- **Verified?** read directly - both files at `raw.githubusercontent.com/envoyproxy/envoy/main/...`
- **Key idea:** Periodically pin concurrency very low to measure the ideal latency (minRTT). Between measurements, compare a percentile of recent latencies to minRTT and scale the limit by the ratio, plus a square-root headroom so the limit keeps probing upward.
- **What Mini-ZGateway would concretely borrow:** *concurrency-limit:*
  - `gradient = (minRTT + B) / sampleRTT`, `B = minRTT * buffer_pct` (buffer default 25%).
  - `limit_new = gradient * limit_old + headroom`, headroom fixed at `sqrt(limit)`.
  - sampleRTT = `sample_aggregate_percentile` (default p50; the doc example uses p90) over each `concurrency_update_interval` (example 0.1 s).
  - minRTT window: every `interval`, set the limit to `min_concurrency` (default 3) until `request_count` (default 50) samples arrive; start delayed by `jitter` (default 15% of the interval). Also re-measured if the limit sits at the minimum for 5 consecutive windows.
  - `max_concurrency_limit` default 1000; newer `min_concurrency_limit` separates "floor during normal clamping" from "concurrency used while measuring".
- **What NOT to copy:** the minRTT window itself is harmful with one backend. The docs admit "a noticeable increase in request 503s" during it and tell you to retry on another host; there is no other host here. Jitter exists to desynchronise many Envoys, which is also not our case.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - use the formula and buffer, but get the baseline from a long-window average (Netflix Gradient2, entry 11) instead of a pinned-low measurement window.

### 7. Envoy circuit breakers and retry budget
- **Source:** `docs/root/intro/arch_overview/upstream/circuit_breaking.rst`, `api/envoy/config/cluster/v3/circuit_breaker.proto`
- **Verified?** read directly - both at `raw.githubusercontent.com/envoyproxy/envoy/main/...`
- **Key idea:** Four independent static caps per upstream cluster, each with its own overflow counter, so an operator can tell which bound was hit. Retries have their own cap, either a fixed number or a budget proportional to live traffic.
- **What Mini-ZGateway would concretely borrow:** *pool + admission + observability:* the four bounds and names: `max_connections` (1024), `max_pending_requests` (1024, requests waiting for a connection), `max_requests` (1024, outstanding), `max_retries` (3 concurrent). One overflow counter per bound (`upstream_cx_overflow`, `upstream_rq_pending_overflow`, `upstream_rq_active_overflow`, `upstream_rq_retry_overflow`). *errors+retries:* the concurrency-style retry budget: active retries <= `budget_percent` (default 20%) of (active + pending requests), with a floor `min_retry_concurrency` (default 3) and an optional `budget_interval` window. This is a third retry-budget formulation alongside Finagle's token bucket and Google's ratio.
- **What NOT to copy:** the defaults; 1024 is "effectively off" for a 20-32 connection pool. Also note Envoy's own caveat that active connections can exceed `max_connections`; the spec's hard-cap invariant forbids that.
- **Strength:** CORE
- **Recommendation:** ADOPT - the "one named counter per bound" rule and the budget-with-floor shape.

### 8. Envoy admission control filter
- **Source:** `docs/root/configuration/http/http_filters/admission_control_filter.rst`, `api/envoy/extensions/filters/http/admission_control/v3/admission_control.proto`
- **Verified?** read directly - both at `raw.githubusercontent.com/envoyproxy/envoy/main/...`
- **Key idea:** Probabilistic rejection driven by the recent success rate, taken from the Google SRE client-side throttling formula, with a knob for how early and how steeply shedding starts.
- **What Mini-ZGateway would concretely borrow:** *admission / errors+retries:* `P_reject = ((n_total - s) / (n_total + 1)) ^ (1/aggression)`, `s = n_success / threshold`. Defaults from the proto: sampling window 30 s, `sr_threshold` 95%, `rps_threshold` 0 (below it nothing is rejected), `max_reject_probability` 80%. Aggression 1.0 is linear. Two details worth keeping: a floor RPS below which the filter never rejects, and a cap so some probes always get through.
- **What NOT to copy:** as the main admission gate. It reacts to failures, so it only engages after the backend is already erroring; the spec's queue-delay signal fires earlier. It also needs a seeded PRNG in the decision path (fine for DST, but one more thing to make deterministic).
- **Strength:** SECONDARY
- **Recommendation:** WATCH ONLY - a candidate for the load generator's "well-behaved client" model, not for the proxy core.

### 9. Envoy outlier detection
- **Source:** `docs/root/intro/arch_overview/upstream/outlier.rst`, `api/envoy/config/cluster/v3/outlier_detection.proto`
- **Verified?** read directly (proto defaults; the .rst was fetched and skimmed) - `raw.githubusercontent.com/envoyproxy/envoy/main/...`
- **Key idea:** Passive health checking: eject a host after N consecutive failures or when its success rate is a statistical outlier against its peers, for a time that grows with each ejection.
- **What Mini-ZGateway would concretely borrow:** only the constants as a reference point: `consecutive_5xx` 5, `base_ejection_time` 30 s multiplied by the ejection count, `max_ejection_percent` 10%, success-rate stdev factor 1900 (= 1.9).
- **What NOT to copy:** everything. Outlier detection compares hosts with each other; with one backend there is no peer group and ejecting the only host is an outage. The 10% ejection cap exists for the same reason.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE for v1 - only relevant to Variant C replicas.

### 10. Envoy Redis proxy filter
- **Source:** `docs/root/intro/arch_overview/other_protocols/redis.rst`, `api/envoy/extensions/filters/network/redis_proxy/v3/redis_proxy.proto`, `source/extensions/filters/network/common/redis/supported_commands.h`, `.../common/redis/client_impl.h`, `.../redis_proxy/command_splitter_impl.cc`
- **Verified?** read directly - all five at `raw.githubusercontent.com/envoyproxy/envoy/main/...` (the two C++ files were grepped, not read end to end)
- **Key idea:** A RESP codec plus an allow-list. Every command must be hashable to a server or be answered locally; anything else gets `ERR unknown command`. Client pipelines are supported and commands from many clients can be batched onto one upstream write.
- **What Mini-ZGateway would concretely borrow:**
  - *protocol:* allow-list classification, with separate sets for simple, multi-key, eval, transaction (`multi exec discard watch unwatch`) and write commands. The header has **no** BLPOP/BRPOP/BLMOVE, SUBSCRIBE/PSUBSCRIBE, MONITOR or WAIT. PING, ECHO, QUIT, HELLO, AUTH, CLIENT SETNAME/SETINFO are answered by the proxy itself; upstream AUTH is issued by the proxy on connect.
  - *batching:* `max_buffer_size_before_flush` (recommended 1024 bytes; unset = flush on every write, no batching) and `buffer_flush_timeout` (default 3 ms once the size knob is set). The proto states there is "no benefit to using this with a single downstream process".
  - *protocol, RESP3:* one RESP version is pinned end to end per listener, never translated, because reply shapes differ (the docs cite `ZRANGE WITHSCORES`). Unsolicited RESP3 Push frames from upstream are **dropped**, because "forwarding one would desynchronize the reply FIFO".
  - *errors:* the fixed local error strings (`no upstream host`, `upstream failure`, `invalid request`) distinct from pass-through Redis errors.
  - Transactions use a client marked `is_transaction_client` in the source, i.e. not the shared pipelined connection.
- **What NOT to copy:** the 3 ms flush timeout is 3-15x the spec's 200 µs-1 ms window. Cluster topology, MOVED/ASK redirection, key-hash splitting of MGET are all multi-node concerns.
- **Strength:** CORE (Variant A)
- **Recommendation:** ADOPT - the allow-list approach, proxy-answered connection commands, drop-or-forbid rule for Push frames, and size-or-timer flush.

---

## Part 3 - Adaptive concurrency libraries

### 11. Netflix concurrency-limits: AIMD, Vegas, Gradient, Gradient2
- **Source:** https://github.com/Netflix/concurrency-limits, `concurrency-limits-core/src/main/java/com/netflix/concurrency/limits/limit/{AIMDLimit,VegasLimit,GradientLimit,Gradient2Limit,WindowedLimit}.java`
- **Verified?** read directly - all five files plus `README.md` at `raw.githubusercontent.com/Netflix/concurrency-limits/main/...`. Not read: the `Log10RootIntFunction` and `SquareRootFunction` helpers (so the exact rounding of `log10(limit)` is unverified).
- **Key idea:** Treat in-flight requests like a TCP congestion window. Each completed request (or each window of samples) reports RTT, in-flight count and whether it was dropped; a small pure function returns the new limit.
- **What Mini-ZGateway would concretely borrow:** *concurrency-limit:* the common signature `update(startTime, rtt, inflight, didDrop) -> newLimit`, which is trivially deterministic for the simulator. The four rules:
  - **AIMD** (initial 20, min 20, max 200, backoff 0.9 in [0.5,1), timeout 5 s): if `didDrop || rtt > timeout` then `limit = int(limit * backoff)`; else if `inflight * 2 >= limit` then `limit + 1`; clamp.
  - **Vegas** (initial 20, max 1000, smoothing 1.0, probeMultiplier 30): `queue = ceil(limit * (1 - rttNoLoad/rtt))`; `alpha = 3*log10(limit)`, `beta = 6*log10(limit)`, `threshold = log10(limit)`. didDrop: `limit - log10(limit)`. `inflight*2 < limit`: hold. `queue <= threshold`: `limit + beta`. `queue < alpha`: `limit + log10(limit)`. `queue > beta`: `limit - log10(limit)`. Otherwise hold. `rttNoLoad` is the minimum seen, reset to the current sample every `jitter(0.5..1) * probeMultiplier * limit` updates.
  - **Gradient** (initial 50, min 1, max 1000, smoothing 0.2, tolerance 2.0, backoff 0.9, probeInterval 1000, queueSize = sqrt-based): `gradient = max(0.5, min(1, tolerance * rttNoLoad / rtt))`; didDrop: `limit * backoff`; `inflight < limit/2`: hold; else `new = limit*gradient + queueSize`, smoothed. On each probe the limit is cut to `max(minLimit, queueSize)` and the minimum is reset.
  - **Gradient2** (initial 20, min 20, max 200, smoothing 0.2, queueSize 4, longWindow 600, tolerance 1.5): `longRtt` = exponential average over 600 samples (10 warm-up); `shortRtt` = the current sample. If `longRtt/shortRtt > 2` then `longRtt *= 0.95`. `inflight < limit/2`: hold. `gradient = max(0.5, min(1.0, tolerance*longRtt/shortRtt))`; `new = limit*gradient + queueSize`; `limit = limit*(1-smoothing) + new*smoothing`; clamp.
  - **WindowedLimit** wrapper: feed the inner rule once per window (1 s, needs >= 10 samples, ignores RTT < 100 µs) rather than per request.
  - The "only grow when `inflight >= limit/2`" guard appears in all four and is essential: without it the limit drifts upward while idle.
- **What NOT to copy:** the minimum-RTT baseline (Vegas, Gradient) with one backend and mixed commands: a single fast `PING` or cache-hit `GET` sets a baseline that makes every normal request look congested. Gradient2's long-average baseline avoids that and needs no probe phase. The defaults (min 20) are sized for a service, not for a 20-connection pool.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - AIMD first (it is what the spec's commit 5 names), Gradient2 as the documented upgrade; classify RTT samples per command class or the baseline is meaningless.

### 12. Netflix partitioned limiter (per-tenant guaranteed shares)
- **Source:** `concurrency-limits-core/.../limiter/AbstractPartitionedLimiter.java`, `README.md`
- **Verified?** read directly - `raw.githubusercontent.com/Netflix/concurrency-limits/main/concurrency-limits-core/src/main/java/com/netflix/concurrency/limits/limiter/AbstractPartitionedLimiter.java`
- **Key idea:** Split the (adaptive) global limit into named partitions by percentage. A partition's share is a guarantee, not a cap: it is only enforced once the global limit is reached, so idle capacity is borrowed freely.
- **What Mini-ZGateway would concretely borrow:** *fairness:* state per partition `{percent, busy, limit}`; on every global limit change `limit_p = max(1, ceil(total * percent))`. Decision rule on acquire:
  ```
  if globalInflight >= globalLimit:      # saturated
      admit iff partition.busy < partition.limit
  else:
      admit                              # may exceed own share
  ```
  Unmatched requests go to an `unknown` partition with percent 0, which therefore gets limit 1. The sum of percents must be <= 1.0. Because limits round up, the sum of partition limits can exceed the global limit (source comment says so). Optional per-partition `backoffMillis`: sleep before rejecting, bounded by `maxDelayedThreads`.
- **What NOT to copy:** static percent per named partition does not fit an open set of tenants. The saturated branch also means in-flight can exceed the global limit by up to the sum of unused partition shares, which conflicts with a hard `MaxPoolSize`-style invariant if the limiter is the only gate. The reject-delay sleep holds a goroutine; skip it.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - use "share enforced only under saturation" as the fairness rule, with share = 1/active_tenants (or weight-based) computed dynamically, and keep the pool cap as a separate hard bound.

### 13. Netflix tech blog - "Performance Under Load"
- **Source:** https://netflixtechblog.medium.com/performance-under-load-3e6fa9a60581 (Landau, Thurston, Bozarth)
- **Verified?** read directly - fetched with the local renderer (plain `curl` got HTTP 403)
- **Key idea:** A fixed concurrency limit goes stale; infer it instead from latency, the way TCP infers a congestion window. The post gives the original gradient rule and the reasoning for a square-root queue allowance.
- **What Mini-ZGateway would concretely borrow:** *concurrency-limit / docs:* the framing sentence for the README (Little's law, "limit = concurrency + queue allowed before latency degrades") and the formula as published: `gradient = RTTnoload / RTTactual`, `newLimit = currentLimit * gradient + queueSize`, queueSize = sqrt(currentLimit) because it is "large relative to the current limit for low numbers ... but reduces for larger numbers for better stability".
- **What NOT to copy:** the post's reassurance that "a single client retry is nearly 100% successful at reaching an instance with available capacity" assumes many instances behind a load balancer. With one backend, a shed request has nowhere else to go, so retry-on-shed must be budgeted.
- **Strength:** BACKGROUND
- **Recommendation:** ADOPT - as the cited rationale; the code in entry 11 is the real reference.

---

## Part 4 - RPC-library overload practice

### 14. Finagle RetryBudget (and Linkerd's retry budget)
- **Source:** `finagle-core/src/main/scala/com/twitter/finagle/service/RetryBudget.scala`, `doc/src/sphinx/Clients.rst`; Linkerd `reference/retries.md`, `reference/service-profiles.md`
- **Verified?** read directly - Finagle files at `raw.githubusercontent.com/twitter/finagle/develop/...`; Linkerd docs at `raw.githubusercontent.com/linkerd/website/main/linkerd.io/content/2-edge/reference/...`. Linkerd's field names (`retryRatio`, `minRetriesPerSecond`, `ttl`) were read; its default values were not on the pages read.
- **Key idea:** Every original request deposits credit; every retry withdraws a larger amount. Credit expires after a TTL. A small time-based reserve lets low-traffic clients retry at all.
- **What Mini-ZGateway would concretely borrow:** *errors+retries:* the exact integer mechanics. Defaults: `ttl` 10 s (allowed 1-60 s), `minRetriesPerSec` 10, `percentCanRetry` 0.2. With `ScaleFactor = 1000`: `deposit = 1000` per request; `withdraw = 1000 / percentCanRetry` (5000 at 20%, 10000 at the spec's 10%); `reserve = minRetriesPerSec * ttl_seconds * withdraw`, a constant floor on the bucket; the bucket is a "leaky" token bucket whose deposits expire after `ttl`. `tryWithdraw()` either takes the full amount or leaves the balance untouched. Integer-only arithmetic suits a deterministic simulator. Also the `NonRetryable` flag on failures: a rejection can say "do not retry me", which is the spec's pre-forward vs post-forward distinction carried in the error.
- **What NOT to copy:** the default `minRetriesPerSec` 10 is large against a test tenant doing 50 QPS (it alone permits 20% extra). Finagle also retries immediately with no backoff by default; a proxy in front of one backend should not.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - this is the retry-budget implementation to build, set to 10% and a floor of about 1/s, keyed per tenant.

### 15. Finagle / Linkerd load balancing and failure accrual
- **Source:** `finagle-core/.../loadbalancer/PeakEwma.scala`, `.../liveness/FailureAccrualFactory.scala`, `.../liveness/FailureAccrualPolicy.scala`; Linkerd `features/load-balancing.md`, `reference/circuit-breaking.md`
- **Verified?** read directly - the three Finagle files and two Linkerd docs (URLs as in entry 14's repos). P2C selection itself and Finagle deadline propagation were not read this run (from memory).
- **Key idea:** Pick two backends at random and send to the cheaper one, where cost is a latency average that jumps up instantly on a slow response and decays slowly. Separately, mark a backend dead after repeated failures and let one probe request through after a backoff.
- **What Mini-ZGateway would concretely borrow:**
  - *pool, optional:* the peak-EWMA cost for choosing among **pooled connections**: `cost` = time-decayed EWMA of RTT; if a new RTT exceeds the current cost, reset to it (the peak); `load = cost * (pending + 1)`; a connection with no history and pending work gets a large penalty.
  - *errors:* failure accrual as the single-backend breaker. Defaults: success rate below 0.8 over a 30 s window (min 5 requests), or 5 consecutive failures; mark dead for an exponential jittered backoff from 5 s to 300 s; then ProbeOpen admits one request, success revives, failure extends the backoff. Linkerd's consecutive-failure default is 7.
  - *errors:* deadline propagation - pass remaining time, not a fresh timeout, to the backend call (principle confirmed in the SRE chapter, entry 17).
- **What NOT to copy:** P2C across hosts; there is one host. A 5 s minimum dead time is long for a local Redis; scale it to the test timescale.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - failure accrual with one probe as the backend-down state machine; peak-EWMA only if per-connection latency turns out to vary.

### 16. Google SRE book - "Handling Overload" (ch. 21)
- **Source:** https://sre.google/sre-book/handling-overload/
- **Verified?** read directly - page text, plus the equation image (`/img/dynamic/sre-book/b0f3bc17...webp`) rendered and read
- **Key idea:** Rejecting is not free, so clients should throttle themselves from their own accept ratio. Requests carry a criticality so the least important are shed first. Retries are capped twice: per request and per client.
- **What Mini-ZGateway would concretely borrow:**
  - *errors+retries:* **this is the source of the spec's 10%.** "We implement a per-request retry budget of up to three attempts" and "a per-client retry budget ... A request will only be retried as long as this ratio is below 10%". The chapter gives the payoff: worst-case growth just below 3x with only the per-request cap, "just 1.1x" with the 10% ratio added.
  - *admission (client side / loadgen):* adaptive throttling. Keep `requests` and `accepts` for the last two minutes; reject locally with probability `max(0, (requests - K * accepts) / (requests + 1))`. K = 2 is preferred; K = 1.1 means about one backend rejection per ten accepts.
  - *fairness:* four criticality levels (CRITICAL_PLUS, CRITICAL, SHEDDABLE_PLUS, SHEDDABLE), shed lowest first.
  - *errors:* the "overloaded; don't retry" reply as distinct from a plain failure.
- **What NOT to copy:** the two-minute window is far too slow for scenarios that last seconds. Criticality propagation needs caller cooperation that a RESP or Postgres client cannot give; a per-tenant priority configured at the proxy is the most this project can do.
- **Strength:** CORE
- **Recommendation:** ADOPT - cite it for the 10% number; implement the ratio via entry 14's bucket; put adaptive throttling in the load generator's well-behaved-client profile.

### 17. Google SRE book - "Addressing Cascading Failures" (ch. 22)
- **Source:** https://sre.google/sre-book/addressing-cascading-failures/
- **Verified?** read directly - page text
- **Key idea:** Overload feeds itself: queues add latency, latency causes timeouts, timeouts cause retries. The fixes are short queues, early rejection, deadline propagation and bounded, jittered retries.
- **What Mini-ZGateway would concretely borrow:**
  - *admission:* queue sizing guidance: for steady traffic keep the queue small relative to the worker pool, "e.g., 50% or less". The chapter's worked example (queue 10x the threads, 100 ms service time, 1.1 s when full) is a ready-made README illustration. It also names LIFO and CoDel as the alternatives to FIFO, citing the same Facebook work the spec does.
  - *errors:* deadline propagation with the 30 s / 7 s / 23 s example; check the remaining deadline before starting each stage.
  - *errors+retries:* "randomized exponential backoff", a process-wide retry budget (their example: 60 retries per minute), and the warning about retries multiplying across layers, which is the spec's 3x3x3 argument.
  - *testing:* the non-linear recovery point: a service healthy at 10,000 QPS that tipped at 11,000 will not recover at 9,000. Worth a chaos assertion: after the overload ends, measure how long goodput takes to return.
- **What NOT to copy:** the capacity-planning and restart-orchestration half of the chapter.
- **Strength:** CORE
- **Recommendation:** ADOPT - queue depth <= about half the concurrency limit as the default `MaxQueueDepth` starting point.

---

## Part 5 - Cache proxies and Redis specifics

### 18. Twemproxy (nutcracker)
- **Source:** https://github.com/twitter/twemproxy (`README.md`, `notes/redis.md`, `notes/recommendation.md`)
- **Verified?** read directly - all three at `raw.githubusercontent.com/twitter/twemproxy/master/...`
- **Key idea:** "Built primarily to reduce the number of connections to the caching servers." Many client connections are multiplexed onto very few server connections using protocol pipelining; anything that needs connection state is simply unsupported.
- **What Mini-ZGateway would concretely borrow:**
  - *pool:* the proof that the design works with tiny pools: `server_connections` defaults to **1** per server.
  - *protocol:* its `notes/redis.md` support table is the most conservative command classification available. Marked "No": MULTI, EXEC, WATCH, BLPOP, BRPOP, BRPOPLPUSH, SUBSCRIBE, PUBLISH, SELECT, AUTH (client-sent), MONITOR, KEYS, SCAN, INFO. Supported: single-key commands, MGET/MSET/DEL (fragmented across shards), EVAL with a key, PING and QUIT.
  - *protocol:* connection state belongs to the proxy: `redis_auth` and `redis_db` are per-pool config and "Twemproxy will always present itself to clients as DB 0".
  - *errors:* `timeout` has no default ("we wait indefinitely") and the notes say to always set it; `auto_eject_hosts` after `server_failure_limit` (default 2) consecutive failures, retry after `server_retry_timeout` (30000 ms).
- **What NOT to copy:** ejecting the only server; the infinite default timeout; rejecting PUBLISH (it is stateless and safe to multiplex); memory-buffer (mbuf) tuning.
- **Strength:** CORE (Variant A)
- **Recommendation:** ADOPT - as the baseline deny-list and the precedent for proxy-owned AUTH/SELECT.

### 19. McRouter (Meta)
- **Source:** https://github.com/facebook/mcrouter (`README.md`, wiki `Features`, `Home`)
- **Verified?** read directly - `raw.githubusercontent.com/facebook/mcrouter/main/README.md`, `raw.githubusercontent.com/wiki/facebook/mcrouter/Features.md`, `.../Home.md`
- **Key idea:** A memcached-protocol router where each proxy thread holds exactly one persistent connection per destination and all requests share it. Most of its value is in routing policies.
- **What Mini-ZGateway would concretely borrow:** *pool:* "one TCP connection to a given destination per mcrouter thread", opened lazily on first use and closed after inactivity. *errors:* TKO: after "a certain number of timeouts in a row" mark the destination down and send cheap probes (`version`) until one succeeds, the same shape as Finagle's probe state. *admission:* the description of the proxy as "a series of queues", each with its own limit, is the right mental model for the pipeline doc.
- **What NOT to copy:** route handles, failover pools, the reliable delete stream (the proxy replies success to the client and replays the delete later; that is the opposite of the spec's honest post-forward error semantics), large-value splitting. Memcached's protocol has none of Redis's connection state, so it does not inform the classification table.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - a second precedent for lazy, tiny pools and probe-based recovery; nothing unique to take.

### 20. Redis protocol facts: pipelining, RESP2 vs RESP3, client tracking
- **Source:** Redis docs repo (`content/develop/using-commands/pipelining.md`, `content/develop/reference/protocol-spec.md`, `content/develop/reference/client-side-caching.md`); `redis-specifications/protocol/RESP3.md`
- **Verified?** read directly - `raw.githubusercontent.com/redis/docs/main/content/develop/...` (three files, grepped for the relevant sections) and `raw.githubusercontent.com/redis/redis-specifications/master/protocol/RESP3.md`. Per-command semantics in the table below that are not in those four files are marked *(memory)*.
- **Key idea:** A Redis connection is a strict FIFO: the Nth reply answers the Nth command. That is what makes multiplexing possible with nothing but a queue of waiters per backend connection. Anything that breaks the FIFO, or that leaves state on the connection, cannot share one.
- **What Mini-ZGateway would concretely borrow:**
  - *batching:* pipelining's gain is mostly fewer `read()`/`write()` syscalls and context switches, not just RTT. The server must buffer replies, so the docs advise bounded batches ("for instance 10k commands"); the proxy needs a max batch size as well as a timer.
  - *protocol:* three things break request-response: RESP2 Pub/Sub ("the protocol changes semantics and becomes a push protocol"), MONITOR ("ad-hoc push mode"), and RESP3 Push frames (first byte `>`), which "the server may push ... at any time", so a reader "should keep reading if it is reading the reply of a command".
  - *protocol:* client-side caching: `CLIENT TRACKING ON` makes the server remember, per client ID, which keys that connection read, and send invalidations either as RESP3 Push on the same connection or, in RESP2, to another connection via `REDIRECT <id>`. On a shared connection this state would be attributed to the wrong client.
  - **Command classification table for Variant A** (rows: who supports what)

    | Class | Commands | Why it cannot share | Twemproxy | Envoy | Predixy | Proposed v1 |
    |---|---|---|---|---|---|---|
    | Transaction | MULTI, EXEC, DISCARD, WATCH, UNWATCH | state spans commands; other clients' commands would land inside the transaction | No | Yes, dedicated transaction client | Yes, `Private` connection | Reject |
    | Blocking | BLPOP, BRPOP, BRPOPLPUSH, BLMOVE, BZPOP*, XREAD BLOCK, WAIT | holds the FIFO head for an unbounded time | No | not in allow-list | Yes, `Private` | Reject |
    | Pub/Sub subscribe | SUBSCRIBE, PSUBSCRIBE, SSUBSCRIBE | connection turns into push mode | No | not in allow-list | Yes, `Private` | Reject |
    | Publish | PUBLISH | none (stateless) | No | Yes | Yes | Forward as a write |
    | Push/stream | MONITOR, CLIENT TRACKING, RESP3 Push | unsolicited frames desynchronise the FIFO | No | absent; Push frames dropped | - | Reject; drop or fail on Push |
    | DB select | SELECT, SWAPDB *(memory)* | per-connection DB index | No (`redis_db` config) | cluster-scope only | Yes (proxy tracks) | Reject non-zero, answer `SELECT 0` locally |
    | Handshake/auth | AUTH, HELLO, QUIT, RESET *(memory)* | per-connection identity and protocol version | No (`redis_auth` config) | answered locally | proxy-handled | Answer locally; proxy owns backend AUTH/HELLO |
    | Client state | CLIENT SETNAME/GETNAME/ID/REPLY/KILL/PAUSE *(memory for all but SETNAME/SETINFO)* | names, IDs and reply mode belong to the shared connection | - | SETNAME/SETINFO answered locally | - | Answer the harmless ones locally, reject the rest |
    | Keyspace scans | KEYS, SCAN | safe on one backend (cursor is stateless *(memory)*) but slow | No | Yes (cluster-scope) | Yes | Forward, never coalesce |
    | Scripts | EVAL, EVALSHA | safe to share; may write | Yes (needs a key) | Yes | Yes | Forward as write |
    | Plain reads | GET, MGET, EXISTS, TTL, HGET, ... | none | Yes | Yes | Yes | Forward, coalesce, batch |
    | Plain writes | SET, DEL, INCR, HSET, ... | none | Yes | Yes | Yes | Forward, batch, never coalesce |

- **What NOT to copy:** RESP3 support in v1. It forces a decision on Push frames and doubles the reply shapes the coalescer must hand to waiters (a reply produced for a RESP3 client cannot be given to a RESP2 waiter). Envoy pins one version per listener for this reason.
- **Strength:** CORE (Variant A)
- **Recommendation:** ADOPT - the table above is the proposed `internal/protocol` classification; allow-list by default, unknown commands rejected.

### 21. Predixy (and why Dragonfly / KeyDB are not entries)
- **Source:** https://github.com/joyieldInc/predixy (`README.md`, `src/Command.h`, `src/Command.cpp`)
- **Verified?** read directly - three files at `raw.githubusercontent.com/joyieldInc/predixy/master/...`. Dragonfly and KeyDB: could not verify (from memory) - not fetched.
- **Key idea:** A multi-threaded Redis proxy that supports the stateful commands the others refuse, by flagging them `Private` ("require private connection") and giving that client a dedicated backend connection.
- **What Mini-ZGateway would concretely borrow:** *protocol + pool:* the flag-per-command table (`Read`, `Write`, `Admin`, `Private`, `NoKey`, `MultiKey`, `Inner` = proxy use only). The `Private` set is exactly: `watch multi blpop brpop brpoplpush psubscribe subscribe`. That is the definitive short list of "pin or reject". It also shows the cost: every pinned client removes one connection from the shared pool.
- **What NOT to copy:** implementing `Private` connections in v1. A pinned connection is the "session-level pinning" the spec says makes fan-in disappear, and blocking commands could pin the whole pool. Dragonfly and KeyDB are Redis-compatible servers, not proxies; from memory they have no multiplexing design to learn from here, so they are dropped.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - use its flag vocabulary; reject the `Private` set in v1 and document pinning (with a hard cap on pinned connections) as a possible v2.

---

## Part 6 - General-purpose proxies

### 22. HAProxy server queueing
- **Source:** `doc/configuration.txt` (server `maxconn`, `maxqueue`, `minconn`, `slowstart`; `timeout queue`; `set-priority-class`, `set-priority-offset`)
- **Verified?** read directly - `raw.githubusercontent.com/haproxy/haproxy/master/doc/configuration.txt`, the named keyword sections
- **Key idea:** A per-server concurrency cap with a queue in front of it. HAProxy's own manual calls `maxconn` "very important as it can save fragile servers from going down under extreme loads".
- **What Mini-ZGateway would concretely borrow:**
  - *admission:* the three knobs and their unsafe defaults, as a cautionary table: `maxconn` 0 = unlimited; `maxqueue` 0 = "the queue is unlimited"; `timeout queue` falls back to `timeout connect`; on expiry the request "is dropped and a 503 error is returned".
  - *fairness:* queue ordering by `(priority class, arrival time + offset)`: class is an integer in -2047..2047, lower served first; offset is in ms within +/-524287 and shifts a request's effective arrival time. An offset is a cheap way to give a tenant a bounded head start without strict priority (and its starvation).
  - *concurrency-limit / pool:* `slowstart`: after a server recovers, its limit grows linearly from 1 to 100% over the configured time, and it "never applies when HAProxy starts", only after a failure. This is a deterministic alternative to letting AIMD rediscover the limit after a backend restart.
  - `minconn`/`fullconn`: a limit that ramps with total load.
- **What NOT to copy:** redispatch to another server when `maxqueue` is hit; strict priority classes without an aging offset.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - take slow-start after backend recovery and the priority-offset idea; the base queue is replaced by CoDel/LIFO.

### 23. nginx upstream `queue` / `max_conns`
- **Source:** https://nginx.org/en/docs/http/ngx_http_upstream_module.html
- **Verified?** read directly - that page (`queue` and `max_conns` sections)
- **Key idea:** The minimal bounded wait queue: a maximum length and a maximum wait, reject on either.
- **What Mini-ZGateway would concretely borrow:** *admission:* the two-parameter contract `queue number [timeout=time]`: if the queue is full, or no server can be selected within the timeout (default 60 s), the client gets 502. `max_conns` defaults to 0 (no limit) and the page warns the total "may exceed the max_conns value" with keepalive and multiple workers, another example of a soft cap the spec's invariant must not have. The `$upstream_queue_time` variable is precedent for reporting queue wait separately from backend time.
- **What NOT to copy:** the 60 s default; the directive is commercial-only, so there is no source to study.
- **Strength:** BACKGROUND
- **Recommendation:** WATCH ONLY - confirms the (depth, wait) pair as the minimum queue interface; nothing else.

---

## Part 7 - Coalescing

### 24. Go `golang.org/x/sync/singleflight`
- **Source:** https://github.com/golang/sync/blob/master/singleflight/singleflight.go
- **Verified?** read directly - `raw.githubusercontent.com/golang/sync/master/singleflight/singleflight.go` (whole file)
- **Key idea:** A mutex-guarded `map[string]*call`. The first caller for a key runs the function; later callers find the entry, bump `dups`, and wait on a `WaitGroup` (`Do`) or a buffered channel (`DoChan`). The entry is removed when the function returns.
- **What Mini-ZGateway would concretely borrow:** *coalescer:* the skeleton only: one lock, one map, entry removed on completion **only if it is still the same call** (`if g.m[key] == c { delete }`), a `Shared` flag in the result (use it for the coalesced-hits metric), and the leader/follower split.
- **Known pitfalls, each confirmed in the source:**
  1. **No context anywhere.** `fn` is `func() (any, error)`. In `Do` the function runs on the first caller's goroutine, so whatever context that caller closed over governs the backend call. If the leader's context is cancelled, every waiter receives the leader's cancellation error.
  2. **Errors fan out.** `c.err` is returned to all waiters; one transient failure fails the whole group.
  3. **A slow or hung leader blocks everyone.** `Do` waiters sit in `c.wg.Wait()` with no timeout and no way to leave.
  4. **Panics fan out.** A panic in `fn` is wrapped and re-panicked in every `Do` waiter; if any waiter used `DoChan` it is raised with `go panic(e)`, which cannot be recovered and takes the process down. `runtime.Goexit` is propagated similarly.
  5. **`Forget` breaks the one-in-flight guarantee.** It deletes the map entry while the old call is still running, so the next caller starts a second concurrent backend call, and old waiters still get the old (possibly stale) result.
  6. **Value aliasing.** All waiters get the same `any`; if it is a `[]byte` reply buffer, one waiter mutating or recycling it corrupts the others.
  7. `DoChan` spawns a goroutine per flight and the waiter set (`chans`) is unbounded.
- **What our coalescer must do differently:** run the backend read on a context detached from every caller; refcount waiters and cancel the backend read only when the count reaches zero; let each waiter leave on its own context without affecting the rest; bound waiters per key (see entry 26); give every waiter an immutable or copied reply; decide explicitly whether errors are shared; never crash on a panic; be driven by the injected clock and have no hidden goroutines so the simulator stays deterministic.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - copy the map-and-leader shape, fix all seven points; it must be written from scratch anyway (zero external deps).

### 25. Singleflight variants: groupcache, Tailscale, CockroachDB
- **Source:** `golang/groupcache/singleflight/singleflight.go`; `tailscale/tailscale/util/singleflight/singleflight.go`; `cockroachdb/cockroach/pkg/util/syncutil/singleflight/singleflight.go`
- **Verified?** read directly - all three via `raw.githubusercontent.com` (groupcache whole file; Tailscale `DoChanContext`/`waitCtx`; CockroachDB the doc comments, `DoOpts` and the start of `doCall`). "bradfitz notes": could not verify - no specific document was located or fetched.
- **Key idea:** The original (groupcache) is about 30 lines with no panic handling, no `Forget`, no `Shared`. The two forks each add the missing cancellation isolation, in two different ways.
- **What Mini-ZGateway would concretely borrow:** *coalescer:*
  - **Tailscale `DoChanContext(ctx, key, fn(ctx))`** is the exact semantics the spec asks for. The call runs on `context.WithCancel(context.Background())`, "not derived from any of the input contexts". An atomic `ctxWaiters` count is incremented per caller; each caller's wait goroutine selects on its own `ctx.Done()` or the result; on exit it decrements and, **if it was the last waiter, cancels the call context and waits for the call to finish**. A caller whose context is done gets `ctx.Err()` even if a result arrived at the same moment (cancellation takes precedence, checked explicitly because `select` is random).
  - **CockroachDB `DoOpts.InheritCancelation`** (default false: "a canceled leader does not propagate an error to everybody else"), a `Future` the caller selects on alongside its own context, a returned `leader` bool, `NumCalls(key)` for tests, and the documented pattern of joining a flight while still holding the lock that guarded the cache check, so no flight starts on stale state.
- **What NOT to copy:** Tailscale's extra goroutine per waiter (`go g.waitCtx`); with 1,000 waiters on a hot key that is 1,000 goroutines and non-deterministic scheduling. In a proxy each client connection already has a goroutine that can do the waiting. CockroachDB's tracing and stopper integration. Note the Tailscale design never cancels the backend call while one waiter remains, so a hung backend still needs its own deadline.
- **Strength:** CORE
- **Recommendation:** ADOPT - Tailscale's refcount-and-detach rule is the reference algorithm for the "cancel 99 of 100" invariant.

### 26. Vitess vttablet query consolidator (and hot-row protection)
- **Source:** `vitessio/vitess` `go/sync2/consolidator.go`, `go/vt/vttablet/tabletserver/query_executor.go` (`execSelect`, `shouldConsolidate`), `go/vt/vttablet/tabletserver/txserializer/tx_serializer.go`
- **Verified?** read directly - the three files at `raw.githubusercontent.com/vitessio/vitess/main/...` (consolidator outline, the two functions, and the tx_serializer header comment). The transaction throttler was not read.
- **Key idea:** Production coalescing in a database proxy: identical SELECTs (same SQL text after bind variables, comments stripped) that arrive while one is running wait for it and share its result. Hot-row protection is the write-side cousin: it queues, but does not merge, transactions on the same row.
- **What Mini-ZGateway would concretely borrow:** *coalescer:*
  - the **waiter cap**, which x/sync lacks: if total waiters exceed `ConsolidatorQueryWaiterCap`, a new duplicate either is rejected with `RESOURCE_EXHAUSTED` ("consolidator waiter cap exceeded") or "falls back to independent query execution", by config. This is the "bounded everything" answer for the coalescer.
  - the **mode switch** `enable | disable | notOnPrimary`, also overridable per query. Coalescing is off or replica-only where read-your-writes on the primary matters; it is the spec's "compatible read-consistency" rule made concrete.
  - the key: full statement text plus bound values.
  - a small LRU counting how often each query was consolidated, for the hot-key metric.
  - *admission (hot row):* per-key FIFO with a local (per row range) and a global cap, waiters "unblocked if their context is done", results **not** shared.
- **What NOT to copy:** waiters block in `q.Wait()` with no context, and the leader's error is handed to every waiter (`q.Err()`), the same two flaws as x/sync. The fall-back-to-independent-execution option quietly breaks "at most 1 in-flight call per key"; if the spec's invariant is strict, reject instead.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE - take the waiter cap and the consistency-mode switch; this is also the strongest precedent for coalescing in Variant B.

---

## Part 8 - Remaining candidates worth an entry

### 27. AWS RDS Proxy pinning (with ProxySQL multiplexing)
- **Source:** https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-proxy-pinning.html ; https://proxysql.com/documentation/multiplexing/
- **Verified?** read directly - both pages (HTML converted to text)
- **Key idea:** Instead of forbidding session state, detect it and pin the client to one backend connection until the session ends. Pinning is correct but "reduces the effectiveness of connection reuse".
- **What Mini-ZGateway would concretely borrow:** *protocol (Variant B) + observability:* the list of Postgres triggers: any statement text over 16 KB; `SET` (but not `SET LOCAL` inside a transaction block); PREPARE/DISCARD/DEALLOCATE/EXECUTE; temporary sequences, tables or views; declaring cursors; LISTEN; loading a module; `nextval`/`setval`. And one metric: currently pinned connections (`DatabaseConnectionsCurrentlySessionPinned`). ProxySQL has the same shape with two tiers: multiplexing disabled until the transaction or table lock ends, versus "never enabled again" for the connection (GET_LOCK, temp tables, user variables).
- **What NOT to copy:** pinning itself in v1. Detecting these triggers needs SQL parsing beyond a "simple query subset", and the spec lists session pinning as the way pooling fails.
- **Strength:** SECONDARY (Variant B only)
- **Recommendation:** ADOPT WITH CHANGE - use the trigger list as Variant B's reject list ("unsupported in transaction mode") and export the pinned gauge if pinning is ever added.

### 28. Uber Cinnamon (and QALM)
- **Source:** https://www.uber.com/blog/cinnamon-using-century-old-tech-to-build-a-mean-load-shedder/ ; QALM: https://www.uber.com/blog/qalm-qos-load-management-framework/
- **Verified?** Cinnamon: read directly via the local renderer (`curl` got HTTP 406); this is part 1 of a three-part series and parts 2-3 (PID and auto-tuner detail) were not read. QALM: secondary only - known through the Cinnamon post's description; its own page returned 406 and was not retried.
- **Key idea:** A Go load-shedding middleware with two control loops: a PID controller that picks what fraction to reject so the queue drains, and a Vegas-style auto-tuner that sets the in-flight limit. Requests carry a priority so the same low-priority slice is shed everywhere.
- **What Mini-ZGateway would concretely borrow:** *admission:*
  - overload detection: "the last time the request queue was empty", overloaded if not emptied in about 10 s, credited to the Facebook article the spec already cites.
  - pipeline order: priority tag, rejector, priority queue, scheduler under an in-flight limit, queue timeout.
  - queue timeout "typically 33% of the normal request timeout".
  - priority = 6 tiers x 128 cohorts (cohort = hash of the user, after WeChat DAGOR) = 768 buckets, so a "priority queue" is just an array of FIFO buckets.
  - a Go-specific warning about QALM: "using channels for load shedding can be expensive, as they are points of synchronization".
  - QALM, per this post: CoDel on the request queue ("delayed by more than, say 5 ms, within a 100 ms window") with a manually configured concurrency limit, which is the spec's design; the post reports the manual limit as the adoption problem.
  - *benchmarking:* report goodput by priority tier, not just throughput.
- **What NOT to copy:** the PID controller. Tuning three gains is a project of its own and CoDel's drop rule already provides the "how much to shed" answer. Cross-service priority propagation.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE - take the pipeline order, the queue-timeout-as-a-fraction-of-deadline rule and the bucket-array priority queue; skip PID.

### 29. Amazon Builders' Library (four articles)
- **Source:** "Using load shedding to avoid overload", "Timeouts, retries and backoff with jitter", "Fairness in multi-tenant systems", "Workload isolation using shuffle sharding" at `aws.amazon.com/builders-library/...`
- **Verified?** read directly, partially - all four pages fetched and converted to text; I read the passages matched by keyword search, not each article end to end
- **Key idea:** Measure goodput, not throughput; make rejection cheap; bound retries locally; and treat fairness as layered admission control built from token buckets.
- **What Mini-ZGateway would concretely borrow:**
  - *benchmarking:* the goodput vs throughput definition ("goodput is the subset of the throughput that is" answered successfully) and the practice of load-testing "far beyond the point where goodput plateaus" to check that rejection stays cheap.
  - *errors:* check the client deadline when dequeuing and propagate "remaining time" downstream.
  - *errors+retries:* a local retry token bucket in preference to a circuit breaker, because breakers "introduce modal behavior ... difficult to test"; retry only idempotent operations; pick timeouts from a latency percentile at an accepted false-timeout rate (their example 0.1%).
  - *fairness:* token bucket with rate and burst, and the **composed pair**: one bucket with low rate and high burst checked together with one with high rate and low burst, giving a burst allowance that is itself rate-bounded.
  - *observability:* on every shed, record which client and which operation.
- **What NOT to copy:** shuffle sharding. It needs several workers so each tenant can get a different subset; one backend offers nothing to shuffle (the only place it could apply is admission queues, see entry 30). Capacity placement and quota-management APIs.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT - goodput as a headline metric, the dual token bucket, deadline check at dequeue.

### 30. Kubernetes API Priority and Fairness
- **Source:** https://kubernetes.io/docs/concepts/cluster-administration/flow-control/ (source: `kubernetes/website` `flow-control.md`); KEP-1040
- **Verified?** read directly, partially - `raw.githubusercontent.com/kubernetes/website/main/content/en/docs/concepts/cluster-administration/flow-control.md` (queuing and seats sections). The KEP was downloaded but not read. Default values for `queues`, `handSize`, `queueLengthLimit` are not quoted because I did not read them.
- **Key idea:** Per priority level, a concurrency limit in "seats" fronted by a fixed number of queues. Each flow (tenant) is hashed to a small "hand" of queues and joins the shortest; queues are drained by fair queuing. Unused concurrency is lent between levels.
- **What Mini-ZGateway would concretely borrow:** *fairness:* (a) bounded-memory fairness for an unbounded tenant set: a fixed number of queues, tenant mapped to `handSize` candidates, enqueue on the shortest, so a heavy tenant can fill only its hand and "the maximum number of queued requests possible for a single flow is `handSize * queueLengthLimit`" ; (b) request "width": an expensive request occupies more than one seat, the right way to charge `MGET` of 100 keys or a `KEYS` scan against a tenant; (c) lend/borrow between levels, the same idea as entry 12's soft partitions.
- **What NOT to copy:** the full machinery (priority levels, flow schemas, virtual-time fair queuing with seat-seconds). For a handful of test tenants, one queue per tenant with DRR is simpler and exactly fair.
- **Strength:** SECONDARY
- **Recommendation:** WATCH ONLY - note shuffle-sharded queues as the documented scale-out path if tenant count becomes unbounded; adopt only the request-width idea now.

### 31. Stripe - "Scaling your API with rate limiters"
- **Source:** https://stripe.com/blog/rate-limiters
- **Verified?** read directly - page text
- **Key idea:** Four limiters in production, in order of how often they fire: per-user request rate, per-user concurrent requests, a fleet-wide reservation for critical traffic, and a worker-utilisation shedder that drops traffic class by class.
- **What Mini-ZGateway would concretely borrow:** *fairness:* run a per-tenant **concurrency** limiter next to the per-tenant **rate** limiter; the post credits the concurrent limiter with ending contention on expensive endpoints, which is the case a rate limit misses when the backend slows down. *admission:* reservation: "If our reservation number is 20%, then any non-critical request over their 80% allocation would be rejected". The post names the token bucket as the rate algorithm.
- **What NOT to copy:** the central Redis-backed counters (the proxy is the single control point; counters live in process). **Correction to the brief:** this post does not describe GCRA. GCRA is not covered by any source in this bucket.
- **Strength:** BACKGROUND
- **Recommendation:** ADOPT - the "rate and concurrency, both per tenant" pairing; it matches the spec's wording.

---

## Part 9 - Examined and ranked, no full entry

Ranked from most to least useful.

1. **Shopify Toxiproxy** - read directly (`README.md`). Its toxic names are a ready vocabulary for the simulator's fault injector: `latency`, `down`, `bandwidth`, `slow_close`, `timeout`, `reset_peer`, `slicer` (splits data into small pieces, the spec's 1-byte fragmented reads), `limit_data`. WATCH ONLY: borrow the names for `internal/sim` so readers recognise them; the tool itself is real-socket and non-deterministic, so it does not replace the DST harness.
2. **WeChat DAGOR** (arXiv 1806.04075) - secondary only (abstract page read; mechanism known through Uber's post and memory). Queue-time-based overload detection and business-priority x user-hash admission levels. EXCLUDE as a separate influence: the usable part arrives via Cinnamon (entry 28).
3. **Shopify Semian** - read directly (README, skimmed). Bulkhead (a ticket count per resource) plus circuit breaker, as a client library. EXCLUDE: the bulkhead is the pool cap; the breaker is covered by failure accrual (entry 15).
4. **ProxySQL** - read directly (multiplexing page); folded into entry 27. EXCLUDE as its own entry: MySQL-specific.
5. **"The Tail at Scale"** (Dean & Barroso) - could not verify (from memory; only the publication landing page was fetched, which has no mechanism text). Hedged and tied requests. EXCLUDE: hedging a single backend adds load to the thing that is slow; relevant only to Variant C replicas.
6. **alibaba/sentinel-golang** - read directly (README only). Flow control, concurrency limiting, circuit breaking and a system-adaptive protector as a Go library. EXCLUDE: a framework, not a mechanism; nothing was read at the algorithm level and the spec forbids external core deps.
7. **FluxNinja Aperture** - read directly (README only). Weighted-fair-queuing scheduler and global rate/concurrency limiting driven by a policy controller. EXCLUDE: needs a control plane; no extractable small mechanism in what was read.
8. **Doorman** (YouTube) - read directly (README). Global, cooperative client-side rate limiting with a lease server on etcd. EXCLUDE: solves capacity sharing across many clients of many servers; the proxy is already the single control point.

---

## Conflicts / tensions noticed

1. **Baseline latency: minimum vs long average.** Envoy and Netflix Vegas/Gradient compare against the minimum RTT and must periodically pin concurrency low (Envoy: 3) to re-measure, which Envoy's own docs say raises 503s. Gradient2 compares against a long exponential average and needs no probe, but slowly "learns" a degraded backend as normal (it even decays the average by 5% when the short RTT recovers). Cost of minimum: self-inflicted shedding with no second host to retry on. Cost of average: weaker protection in a slow, sustained degradation. AIMD avoids the question but needs an explicit timeout or drop signal.
2. **Per-tenant share: hard cap vs guarantee-under-saturation.** Stripe-style rate and concurrency limits cap a tenant even when the backend is idle (simple, predictable, wastes capacity). Netflix's partition and Kubernetes' borrowing enforce the share only when the global limit is hit (work-conserving, but in-flight can overshoot the global limit and a bursting tenant takes capacity that a returning tenant must wait to get back). The spec's fairness invariant ("Tenant A absorbs >95% of shedding") is the guarantee-style formulation.
3. **Retry budget: three formulations.** Google: ratio of retries to requests below 10%, plus max 3 attempts. Finagle: token bucket, 20% plus 10/s reserve, 10 s TTL. Envoy: concurrent retries <= 20% of active + pending, floor 3. The ratio is easiest to explain; the token bucket handles low-QPS tenants (reserve) and is integer-only; the concurrency form needs no time window at all. They give different answers during a burst.
4. **Stateful commands: reject vs pin.** Twemproxy and Envoy's allow-list refuse; Predixy, PgBouncer session mode, RDS Proxy and ProxySQL pin. Rejecting keeps the fan-in math exact and the pool invariant simple, at the cost of compatibility (`redis-cli` works, apps using MULTI or BLPOP do not). Pinning keeps compatibility and lets a few clients drain the pool.
5. **Queue wait: long and patient vs short and honest.** PgBouncer waits 120 s, nginx 60 s, HAProxy defaults to an unlimited queue; PgCat waits 1 s; Cinnamon uses a third of the request timeout; the SRE book wants queues at most half the worker count. The poolers optimise for "never show the app an error"; the overload literature says that is how a standing queue forms. The spec sides with the second group; say so explicitly in the README because PgBouncer users will expect the first.
6. **Coalescing: share errors or not; bounded waiters or strict single-flight.** x/sync and Vitess hand the leader's error to everyone (fast failure, amplifies one transient error to N clients). Vitess's waiter cap either rejects (keeps one-in-flight) or runs the query independently (keeps availability, breaks the invariant).
7. **Batching window.** Envoy's default flush timer is 3 ms and its docs say batching has no benefit with a single downstream; the spec wants 200 µs-1 ms and immediate flush when idle. A timer that always waits adds its full length to idle p50.
8. **Connection creation rate.** PgBouncer: strictly one connect in flight per pool, plus a 15 s lockout after a login failure. Envoy: no creation-rate limit, only a total cap and an overload shed point. One-at-a-time protects the backend's accept queue perfectly and makes cold start slow (20 connections x connect time).

## Variant implications

- **Push toward Variant A (Redis RESP):** Twemproxy (18), Envoy Redis filter (10), Predixy (21) and the Redis protocol facts (20) together give a complete, source-backed command classification; the strict FIFO reply order makes true multiplexing (many clients per backend connection, `server_connections: 1`) possible, so coalescing, batching and the pool can all be exercised. Coalescing keys are trivial (command + key). All of the general overload material (11-17, 22, 28-31) applies unchanged.
- **Push toward Variant B (Postgres wire):** PgBouncer (1), PgCat (2), Odyssey (3), RDS Proxy/ProxySQL (27) and Vitess (26). They show Variant B is mostly a *connection-lending* problem (one client per backend connection at a time, released at transaction end), not multiplexing: no pipelining across clients, and micro-batching does not apply to the simple-query protocol. The work goes into transaction-boundary tracking, the break list, dirty-connection cleanup and (optionally) prepared-statement rewriting. Coalescing has good precedent only via Vitess, and only outside transactions.
- **Neutral (apply to either):** Envoy overload manager, adaptive concurrency, circuit breakers, admission control (5-8); Netflix (11-13); Finagle/Linkerd (14-15); SRE book (16-17); HAProxy/nginx (22-23); singleflight family (24-25); Cinnamon, AWS, APF, Stripe (28-31).
- **Only matter for Variant C:** Envoy outlier detection (9), P2C + peak-EWMA across hosts (15), PgCat ban list and replica routing (2), hedged requests (Tail at Scale), shuffle sharding of backends (29), Redis client-side caching/tracking as a cache-invalidation feed (20), Vitess's `notOnPrimary` mode (26).
- **Net:** this bucket supports the spec's recommendation of Variant A. A covers every mechanism in the spec; B drops batching and turns "multiplexed pool" into "transaction-mode lending".

## Steering questions this bucket creates for Nick

1. **Stateful Redis commands (MULTI/WATCH, blocking pops, SUBSCRIBE): reject or pin?**
   Options: (a) reject with a clear error, like Twemproxy; (b) pin a dedicated backend connection, like Predixy, with a hard cap on pinned connections; (c) reject in v1, design the pin path as a documented v2.
   Recommended: **(c)**. It keeps the pool invariant exact and the classification table small.
2. **RESP3 in v1?**
   Options: (a) RESP2 only, answer `HELLO 3` with an error or `-NOPROTO`; (b) pin one version per listener like Envoy and drop Push frames; (c) full RESP3.
   Recommended: **(a)**. `redis-cli` and `redis-benchmark` work on RESP2, and it removes Push frames and mixed-version coalescing from the problem.
3. **Which concurrency-limit algorithm ships first, and what is the baseline?**
   Options: (a) AIMD with a timeout/drop signal; (b) Gradient2 with a long-average baseline; (c) Envoy-style gradient with a minRTT probe window.
   Recommended: **(a) first, (b) as the upgrade**, both behind the same `update(rtt, inflight, didDrop)` function. Avoid (c): the probe window sheds load with no second backend to absorb it.
4. **Per-tenant fairness rule: hard caps or shares enforced only under saturation?**
   Options: (a) per-tenant token bucket and concurrency cap, always enforced; (b) Netflix-style share that only binds when the global limit is reached; (c) both: (b) for concurrency, plus a generous (a) rate cap as a backstop.
   Recommended: **(c)**. (b) is what makes the noisy-neighbor invariant pass without wasting an idle backend.
5. **Retry budget formulation and where it lives.**
   Options: (a) Finagle token bucket at 10% with a small per-second floor, per tenant; (b) Google ratio over a sliding window; (c) Envoy concurrent-retries percentage.
   Recommended: **(a)**. Also decide scope: the proxy can only budget retries *it* performs (reconnect-and-resend of idempotent reads). Client retries are controlled by the fast `OVERLOADED` reply and by the load generator's client model, where the SRE adaptive-throttling formula belongs.
6. **Coalescer policy on errors and on waiter overflow.**
   Options for errors: share the leader's error with all waiters, or let waiters re-drive a fresh read (bounded). Options for overflow: reject past a per-key waiter cap, or run independently (Vitess offers both).
   Recommended: **share errors, reject on overflow**. Both keep "at most 1 in-flight read per key" strictly true, which is the invariant the tests assert.
7. **Backend connection creation: how many at once?**
   Options: (a) one in flight, PgBouncer-style; (b) a small fixed cap such as 2-4; (c) a rate (N per second).
   Recommended: **(b)** plus PgBouncer's "fail waiters fast for a short lockout after a connect failure" and HAProxy-style slow-start of the concurrency limit after the backend comes back.
8. **Default queue wait bound.**
   Options: (a) a fixed small value; (b) a fraction of the client deadline when known (Cinnamon uses 33%); (c) CoDel target/interval only, no separate cap.
   Recommended: **(a) with CoDel on top**. RESP and Postgres clients do not send deadlines, so (b) has no input; state the contrast with PgBouncer's 120 s in the README.

## Verification log

`raw` = `https://raw.githubusercontent.com`. "OK" = HTTP 200 and content read (fully or the named sections).

| URL / path | Result |
|---|---|
| `/root/Software/mini-zgateway/docs/project-source.md` | OK (read in full) |
| raw `/pgbouncer/pgbouncer/master/doc/config.md` | OK |
| raw `/pgbouncer/pgbouncer/master/doc/usage.md` | OK |
| raw `/pgbouncer/pgbouncer/master/src/objects.c` | OK (`launch_new_connection`) |
| raw `/pgbouncer/pgbouncer/master/doc/features.md`, `doc/faq.md` | 404 (wrong path) |
| raw `/pgbouncer/pgbouncer.github.io/master/features.md` | OK |
| `https://www.pgbouncer.org/features.html`, `/faq.html` | 200, not needed (same content as above; FAQ not read) |
| raw `/postgresml/pgcat/main/README.md`, `CONFIG.md` | OK |
| raw `/yandex/odyssey/master/README.md`, `odyssey.conf` | OK |
| raw `/supabase/supavisor/main/README.md` | OK |
| raw `/envoyproxy/envoy/main/docs/root/configuration/operations/overload_manager/overload_manager.rst` | OK |
| raw `/envoyproxy/envoy/main/docs/root/configuration/http/http_filters/adaptive_concurrency_filter.rst` | OK |
| raw `/envoyproxy/envoy/main/api/envoy/extensions/filters/http/adaptive_concurrency/v3/adaptive_concurrency.proto` | OK |
| raw `/envoyproxy/envoy/main/docs/root/intro/arch_overview/upstream/circuit_breaking.rst` | OK |
| raw `/envoyproxy/envoy/main/api/envoy/config/cluster/v3/circuit_breaker.proto` | OK |
| raw `/envoyproxy/envoy/main/docs/root/configuration/http/http_filters/admission_control_filter.rst` | OK |
| raw `/envoyproxy/envoy/main/api/envoy/extensions/filters/http/admission_control/v3/admission_control.proto` | OK (default comments only) |
| raw `/envoyproxy/envoy/main/docs/root/intro/arch_overview/upstream/outlier.rst` | 200, skimmed only |
| raw `/envoyproxy/envoy/main/api/envoy/config/cluster/v3/outlier_detection.proto` | OK (defaults) |
| raw `/envoyproxy/envoy/main/docs/root/intro/arch_overview/other_protocols/redis.rst` | OK |
| raw `/envoyproxy/envoy/main/api/envoy/extensions/filters/network/redis_proxy/v3/redis_proxy.proto` | OK (ConnPoolSettings) |
| raw `/envoyproxy/envoy/main/source/extensions/filters/network/common/redis/supported_commands.h` | OK |
| raw `/envoyproxy/envoy/main/source/extensions/filters/network/common/redis/client_impl.h` | OK (grepped) |
| raw `/envoyproxy/envoy/main/source/extensions/filters/network/redis_proxy/command_splitter_impl.cc` | OK (grepped) |
| raw `/Netflix/concurrency-limits/main/README.md` | OK |
| raw `/Netflix/concurrency-limits/main/concurrency-limits-core/src/main/java/com/netflix/concurrency/limits/limit/{AIMDLimit,VegasLimit,GradientLimit,Gradient2Limit,WindowedLimit}.java` | OK |
| raw `/Netflix/concurrency-limits/main/concurrency-limits-core/src/main/java/com/netflix/concurrency/limits/limiter/AbstractPartitionedLimiter.java` | OK |
| raw `.../limiter/AbstractLimiter.java` | 200, not read |
| `https://netflixtechblog.medium.com/performance-under-load-3e6fa9a60581` | curl 403; OK via `/root/Software/web-tools/websearch fetch` |
| raw `/twitter/finagle/develop/finagle-core/src/main/scala/com/twitter/finagle/service/RetryBudget.scala` | OK |
| raw `/twitter/finagle/develop/doc/src/sphinx/Clients.rst` | OK (retry section) |
| raw `/twitter/finagle/develop/finagle-core/src/main/scala/com/twitter/finagle/loadbalancer/p2c/PeakEwma.scala` | 404 (wrong path) |
| raw `/twitter/finagle/develop/finagle-core/src/main/scala/com/twitter/finagle/loadbalancer/PeakEwma.scala` | OK |
| raw `/twitter/finagle/develop/finagle-core/src/main/scala/com/twitter/finagle/liveness/{FailureAccrualFactory,FailureAccrualPolicy}.scala` | OK (defaults and state names) |
| raw `/twitter/finagle/develop/doc/src/sphinx/ApertureLoadBalancers.rst` | 200, not read |
| raw `/linkerd/website/main/linkerd.io/content/2-edge/reference/{retries,circuit-breaking,service-profiles}.md`, `.../features/load-balancing.md` | OK |
| `https://sre.google/sre-book/handling-overload/` | OK (+ equation image read) |
| `https://sre.google/sre-book/addressing-cascading-failures/` | OK |
| raw `/twitter/twemproxy/master/{README.md,notes/redis.md,notes/recommendation.md}` | OK |
| raw `/facebook/mcrouter/main/README.md`; raw `/wiki/facebook/mcrouter/{Features,Home}.md` | OK |
| raw `/redis/docs/main/content/develop/using-commands/pipelining.md` | OK |
| raw `/redis/docs/main/content/develop/use/pipelining/index.md` | 404 (old path) |
| raw `/redis/docs/main/content/develop/reference/{protocol-spec,client-side-caching}.md` | OK (relevant sections) |
| raw `/redis/redis-specifications/master/protocol/RESP3.md` | OK (Push sections) |
| raw `/redis/redis-specifications/master/protocol/RESP2.md` | 200, not read |
| raw `/joyieldInc/predixy/master/{README.md,src/Command.h,src/Command.cpp}` | OK |
| raw `/haproxy/haproxy/master/doc/configuration.txt` | OK (named keyword sections) |
| `https://nginx.org/en/docs/http/ngx_http_upstream_module.html` | OK |
| raw `/golang/sync/master/singleflight/singleflight.go` | OK (whole file) |
| raw `/golang/groupcache/master/singleflight/singleflight.go` | OK |
| raw `/tailscale/tailscale/main/util/singleflight/singleflight.go` | OK |
| raw `/cockroachdb/cockroach/master/pkg/util/syncutil/singleflight/singleflight.go` | OK (doc comments, DoOpts) |
| raw `/vitessio/vitess/main/go/sync2/consolidator.go` | OK |
| raw `/vitessio/vitess/main/go/vt/vttablet/tabletserver/query_executor.go` | OK (two functions) |
| raw `/vitessio/vitess/main/go/vt/vttablet/tabletserver/txserializer/tx_serializer.go` | OK (header comment) |
| `https://vitess.io/docs/reference/features/vttablet-modes/` | 404 |
| `https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-proxy-pinning.html` | OK |
| `https://proxysql.com/documentation/multiplexing/` | OK |
| `https://www.uber.com/blog/cinnamon-using-century-old-tech-to-build-a-mean-load-shedder/` | curl 406; OK via `websearch fetch` |
| `https://www.uber.com/blog/qalm-qos-load-management-framework/` | curl 406; not retried |
| `https://stripe.com/blog/rate-limiters` | OK |
| `https://aws.amazon.com/builders-library/{using-load-shedding-to-avoid-overload,timeouts-retries-and-backoff-with-jitter,fairness-in-multi-tenant-systems,workload-isolation-using-shuffle-sharding}/` | OK (keyword-matched passages) |
| raw `/kubernetes/website/main/content/en/docs/concepts/cluster-administration/flow-control.md` | OK (queuing, seats sections) |
| raw `/kubernetes/enhancements/master/keps/sig-api-machinery/1040-priority-and-fairness/README.md` | 200, not read |
| `https://arxiv.org/abs/1806.04075` (DAGOR) | OK, abstract only |
| `https://research.google/pubs/the-tail-at-scale/` | 200, landing page only, no mechanism text |
| raw `/Shopify/toxiproxy/main/README.md` | OK (toxic list) |
| raw `/Shopify/semian/main/README.md` | OK (skimmed) |
| raw `/alibaba/sentinel-golang/master/README.md` | OK (README only) |
| raw `/fluxninja/aperture/main/README.md` | OK (README only) |
| raw `/youtube/doorman/master/README.md` | OK (README only) |
| `kb search` (VM knowledge store) | failed: no kb location configured from this working directory; not needed |
| Built-in WebSearch | not used; all targets were known URLs |
| Dragonfly, KeyDB, "bradfitz notes", Finagle deadline docs, Linkerd retry-budget defaults | not fetched; marked from memory or could not verify in the entries |
