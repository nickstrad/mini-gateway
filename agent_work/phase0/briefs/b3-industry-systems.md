# Brief B3 - industry systems the spec does NOT name

Read `_common.md` rules below first (they are appended to this brief).
Output file: /tmp/zgateway-phase0/out/b3-industry-systems.md

Study each from official docs / source code. For each, reduce to the
exact mechanism (state, constants/defaults, decision rule) and judge
its fit for a single-backend learning-scope Go proxy.

1. PgBouncer - session vs transaction vs statement pooling; what breaks
   in transaction mode; max_client_conn / default_pool_size /
   reserve_pool / query_wait_timeout / server_login_retry; how waiting
   clients are queued; prepared-statement support (1.21+).
2. PgCat and Odyssey (and Supavisor if useful) - what they add over
   PgBouncer (multi-threading, sharding, per-pool limits, banlists).
3. Envoy - overload manager (resource monitors, actions, load-shed
   points), adaptive concurrency filter (gradient controller: minRTT
   window, sample aggregate percentile, jitter, buffer, the formula),
   circuit breakers (max_connections, max_pending_requests,
   max_requests, max_retries, retry budgets), admission control filter
   (success-rate based client-side throttling), outlier detection.
   Also Envoy's Redis proxy filter (how it pipelines/multiplexes RESP,
   what commands it refuses).
4. Netflix concurrency-limits (github.com/Netflix/concurrency-limits) -
   AIMD, Vegas, Gradient, Gradient2 limit algorithms: exact formulas,
   defaults, the partitioned limiter (per-tenant guaranteed shares) -
   this last one bears directly on per-tenant fairness. Plus the
   Netflix tech blog "Performance Under Load".
5. Finagle / Linkerd - P2C + peak-EWMA load balancing, failure accrual,
   retry budgets (the 20%-of-requests + min-retries/s token design,
   deposit/withdraw mechanics), deadlines propagation.
6. Google SRE book - "Handling Overload" (client-side adaptive
   throttling formula: requests vs accepts, K=2; criticality; per-
   request retry cap 3 and per-client retry budget 10%) and
   "Addressing Cascading Failures". This is likely where the spec's
   "10% retry budget" comes from - confirm.
7. McRouter (Meta) and Twemproxy (Twitter), plus Redis-specific:
   Redis pipelining docs, RESP2 vs RESP3 (push messages!), Redis client
   -side caching/tracking, which commands CANNOT be multiplexed on a
   shared connection (MULTI/EXEC, WATCH, blocking BLPOP/BRPOP,
   SUBSCRIBE/PubSub, SELECT, CLIENT commands, AUTH/HELLO state,
   MONITOR), how Twemproxy/Envoy/dragonfly/predixy/KeyDB handle each.
   This list determines Variant A's command-classification table.
8. HAProxy - maxconn per server, queueing (timeout queue, priority
   queues), `maxqueue`, slowstart; and nginx upstream `queue`.
9. Go singleflight (golang.org/x/sync/singleflight) - read the actual
   source: Do / DoChan / Forget semantics, shared error and PANIC
   propagation, the fact that the first caller's context governs the
   call, Result.Shared. List the known pitfalls (shared errors fan out
   to all waiters; a slow/hung leader blocks everyone; no cancellation
   isolation; stale-result window on Forget; returned value aliasing)
   and what our own from-scratch coalescer must do differently.
   Also look at groupcache's original singleflight and any refcounted /
   context-aware variants (e.g. tailscale's singleflight fork, CockroachDB's
   `singleflight` with context cancellation, bradfitz notes).
10. Others to consider and either add or drop with a reason: ProxySQL,
    Vitess vttablet (connection pools, query consolidation = coalescing
    of identical in-flight reads! and its hot-row protection / txn
    throttler), AWS RDS Proxy (pinning), Uber's Cinnamon load shedder
    (PID + CoDel) and QALM, Stripe rate-limiter posts (GCRA / load
    shedders), Amazon Builders' Library ("Using load shedding to avoid
    overload", "Timeouts, retries and backoff with jitter", "Fairness
    in multi-tenant systems", shuffle sharding), Shopify's Semian /
    toxiproxy (fault injection precedent), WeChat DAGOR, Google's
    "The Tail at Scale", Doorman, Aperture (FluxNinja), Kubernetes API
    Priority and Fairness (shuffle-sharded fair queuing), sentinel-golang.
    Rank them; do not write full entries for the weak ones - a
    one-line "examined, exclude because ..." list is fine.

Be explicit, per system, whether it pushes toward Variant A (Redis),
Variant B (Postgres), or neither.
