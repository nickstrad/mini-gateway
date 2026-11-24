Mini-ZGateway: Multiplexing Storage Proxy & Adaptive Admission Control
Document Type: Engineering Architecture & Agent Handoff SpecificationTarget Language: Go (pure standard library preferred; zero runtime AI tokens)Target Backend: Works out of the box on a single, unsharded vanilla database (Redis or Postgres), with an optional multi-backend/sharded extension.
1. Handoff Context & Mission
Build Mini-ZGateway, a high-performance, fault-tolerant database/storage proxy in Go inspired by Meta's ZGateway and Cloudflare's Hyperdrive.

Core Thesis: A storage proxy does not magically remove work; it changes the scaling variable and creates a single managed control point. By collapsing $N \times M$ client-to-database connections into a bounded multiplexed pool, coalescing hot-key stampedes, and enforcing per-tenant adaptive admission control, Mini-ZGateway keeps a vanilla backend alive and healthy tenants' p99 latency flat under reconnection storms, viral hot keys, and noisy-neighbor spikes.
Agent Execution Rules
Design & Test First: Before writing production proxy code, lock the chosen project variant (Section 3), write explicit invariants, and build Commit 0 (the deterministic simulation and virtual-clock harness).
Zero External Core Dependencies: Implement the connection pool, request coalescer, CoDel/LIFO admission queue, and token-bucket/AIMD controllers from scratch in Go.
Reproducible Proof: The finished repo must ship with one-command verification (make test, make chaos, make bench) producing reproducible before/after latency and connection-count evidence.
2. Main Architectural Considerations & Tradeoffs
2.1 Connection Ownership & Fan-In Math
Changing the Scaling Variable: Direct client access couples backend connection count to the caller fleet size. Under a reconnection storm, backend processes (especially process-per-connection databases like Postgres) collapse. A proxy moves persistent connections to a tier built to hold them, capping backend fan-in to $\text{proxy_instances} \times \text{max_pool_size}$—a number the platform team controls.
Bounded Pool Limits: Every pool must enforce hard upper bounds on: (1) open backend connections, (2) in-flight connection creation rate (to prevent backend accept-queue storms), and (3) waiting callers in the queue.
When Pooling Fails: If a proxy opens a fresh backend connection per forwarded request, or holds a backend connection pinned to an idle client session (session-level pinning without multiplexing), the fan-in reduction disappears.
2.2 Request Coalescing vs. Batching
These solve two distinct problems and must not be conflated:

In-Flight Request Coalescing (Singleflight): Collapses thousands of concurrent identical reads for a hot key into a single backend read. Eliminates hot-key stampedes against a single backend or replica.
Critical Safety Rule: Only coalesce idempotent, read-only operations with compatible read-consistency/isolation semantics. Never coalesce writes/mutations.
Cancellation Isolation: If Caller A initiates a coalesced read and cancels its context/disconnects after 5ms, Caller B (waiting on the same in-flight backend call) must NOT be aborted. Detach the backend context and only cancel upstream if all waiters cancel.
Micro-Batching / Pipelining: Groups multiple distinct keys arriving within a tiny batch window (e.g., 200µs–1ms or max $N$ items) into a single pipelined or multi-get (MGET) backend round trip to amortize syscall and network overhead.
Tradeoff: Batching trades slight p50 latency for massive throughput and CPU savings; the batch timer must flush immediately when full or under low load so idle latency doesn't degrade.
2.3 Adaptive Admission Control & Load Shedding
A single static rate limit cannot protect both a calm backend and a backend in shock. Implement layered overload protection:

CoDel (Controlled Delay) + Adaptive LIFO Queueing: Under normal load, serve queued requests in FIFO order. Once a standing queue persists longer than a target interval (e.g., queue never empties for $N$ ms), switch to Adaptive LIFO (serving the freshest requests most likely to still be within client deadlines) and drop requests whose queue wait exceeds threshold $M$ ms.
Per-Tenant Fairness & Noisy-Neighbor Isolation: Track per-tenant in-flight concurrency and token/GCRA budgets (or Deficit Round Robin / weighted fair queueing). When backend saturation is detected, shed load preferentially from tenants exceeding their fair share so low-volume healthy tenants maintain flat p99 latency.
Adaptive Concurrency Limits (Optional Upgrade): Dynamically adjust max in-flight backend concurrency using latency gradients (AIMD / Little's Law / Vegas-style controller) so the proxy automatically throttles when the database slows down (e.g., during a checkpoint, vacuum, or disk stall) without manual knob tuning.
2.4 Error Semantics & Retry Amplification Prevention
Pre-Forward Rejection vs. Post-Forward Ambiguity: Explicitly distinguish a fast local rejection (429 / OVERLOADED / SHED_LOAD before forwarding to the DB) from an upstream timeout after forwarding (where a write may have committed). Clients need distinct error codes to decide whether retrying is safe.
Retry Budgets: If the application, proxy, and DB driver all retry independently, a $3 \times 3 \times 3$ cascade creates a $27\times$ retry storm during partial outages. Enforce a strict retry budget (e.g., retries capped at 10% of active requests) and never retry non-idempotent writes.
2.5 Backpressure, Memory Safety & Observability
Bounded Everything: Zero unbounded channels, zero unbounded goroutine-per-request spawning without a semaphore/worker limit, and explicit read/write deadlines on all client and backend sockets.
Observability as a Control Signal: Aggregate average latency hides tenant starvation. Track per-tenant metrics separately: accepted requests, rejected/shed requests, coalesced hits, queue wait time (HDR histogram), backend execution time, and active vs. idle pool connections.
3. Project Variations & Scoping Options
Before coding, choose one primary variant (or plan Variant A as v1 and Variant B/C as v2). All variants work without requiring a sharded database out of the box.

Variant
Protocol & Backend
Primary Focus
Complexity
Best Hiring Signal
Variant A (Recommended MVP)
RESP2/3 (or custom gRPC KV) in front of 1 standard Redis container
90% focus on multiplexing, hot-key coalescing, CoDel/LIFO, and noisy-neighbor isolation
Medium (1–2 weeks)
Broad Platform, Core Infra, AI Serving (OpenAI, Meta, Perplexity, NVIDIA)
Variant B
Postgres v3 wire protocol (simple query subset) in front of 1 standard Postgres container
Connection fan-in collapse (500 clients -> 20 DB conns), transaction-mode pooling, per-tenant load shedding
High (2–3 weeks)
Database & Stateful Platform Infra (OpenAI DB, PlanetScale, Cloudflare, Neon)
Variant C
Adds read-replica routing, TTL/invalidation read caching, and shard-key routing on top of Variant A or B
Cross-replica load balancing, stale-read bounds, adaptive failover on tail-latency spikes
High (v2 expansion)
Distributed Storage & Multi-Region Infra (Meta ZippyDB, AWS, Uber)

Recommendation for Agent Handoff: Default to Variant A (Redis RESP2/3 proxy) so the proxy works natively with redis-cli and redis-benchmark while keeping wire parsing compact (~200–300 LOC). This lets the agent invest maximum rigor into the pooling state machine, coalescer, CoDel/LIFO queue, and deterministic chaos suite.
4. High-Leverage Testing & Verification Approaches
Testing is the core differentiator of this project. Treat verification as a first-class architectural deliverable across four tiers:
4.1 Tier 1: Deterministic Simulation Testing (DST) — Commit 0
Build a single-threaded, seed-driven simulation harness (internal/sim/) before implementing proxy logic:

Virtual Clock: Injectable nanosecond clock so CoDel intervals, token-bucket refills, batch flush timers, and timeouts execute deterministically in milliseconds with zero time.Sleep flakiness.
Fault-Injecting Virtual Network & Backend: Pluggable Transport / BackendConn interface capable of injecting slow backend stalls (e.g., 150ms GC/disk pauses), TCP half-open drops, 1-byte fragmented reads, connection resets, and asymmetric latency.
Deterministic Replay Law: Running any chaos scenario twice with the same int64 seed must produce bit-identical decision traces (accepted, coalesced, shed, timed-out) and final metrics.
4.2 Tier 2: Property-Based & Invariant Tests
Assert these non-negotiable system invariants under randomized traffic and fault schedules (with go test -race ./...):

Hard Connection Cap: Active + connecting backend connections never exceed MaxPoolSize (even under 1,000 simultaneous client connects).
Coalescing Safety & Cancellation Isolation:
Identical concurrent read keys trigger at most 1 in-flight backend call per key.
Write/mutation commands (SET, DEL, INCR, INSERT, UPDATE) are never coalesced.
Canceling 99 of 100 coalesced waiters still delivers a valid response to the 100th waiter.
Bounded Memory & Zero Goroutine Leaks: Under 10× overload for sustained duration, queue depth stays $\le$ MaxQueueDepth and goroutine count returns to baseline after traffic stops.
Noisy-Neighbor Isolation (Fairness Invariant): When Tenant A floods at 10× capacity while Tenant B sends steady low-QPS traffic, Tenant B's success rate remains >99% and p99 latency stays within 2× baseline, while Tenant A absorbs >95% of load shedding.
CoDel Anti-Bufferbloat Invariant: When backend service time exceeds arrival rate, standing queue wait time converges to the CoDel target rather than growing monotonically.
4.3 Tier 3: Adversarial Chaos Scenarios (make chaos)
Encode five named, reproducible chaos suites:

Reconnection Storm: Drop all 500 client connections simultaneously and reconnect in a 50ms burst; prove backend connections stay flat at MaxPoolSize (e.g., 20–32) with zero DB connection exhaustion.
Viral Hot-Key Stampede: Fire 1,000 concurrent reads for a single key while injecting a 50ms backend read latency; prove backend receives 1–2 reads instead of 1,000 and p99 stays near 50ms.
Noisy Neighbor + Backend Stall: Inject a 200ms latency spike on the backend while Tenant A blasts 5,000 QPS and Tenant B sends 50 QPS; prove CoDel + per-tenant admission control protects Tenant B.
Slow-Consumer / Half-Open TCP Drain: Simulate clients that stop reading responses; prove write deadlines tear down stalled clients without leaking backend pool connections.
Retry-Storm Amplification Test: Simulate 30% backend errors with aggressive retrying clients; prove the proxy's retry budget and fast OVERLOADED responses prevent backend collapse.
4.4 Tier 4: Rigorous Benchmarking (make bench)
Open-Loop Load Generator: Avoid coordinated omission (send requests on an independent arrival schedule rather than waiting for prior responses before scheduling the next).
HDR Histograms: Report p50, p95, p99, and p99.9 latency separately per tenant and per outcome (success vs. shed).
Headline Comparisons (Direct DB vs. Mini-ZGateway):
Total DB connections under 500-client fan-in (target: ~15× reduction).
Backend QPS and p99 under 99/1 Zipfian hot-key skew (with vs. without coalescing).
Healthy tenant p99 under noisy-neighbor overload (FIFO unprotected vs. CoDel + per-tenant admission control).
5. Suggested Go Repository Structure & Commit Roadmap
Repository Layout
Path
Responsibility
internal/sim/
Seeded PRNG, virtual nanosecond Clock, fault-injecting virtual Backend/Conn, discrete-event runner
internal/protocol/
Sans-I/O wire parser/encoder (RESP2/3 for Variant A, or Postgres v3 subset for Variant B) with read/write classification
internal/pool/
Multiplexed backend connection pool with bounded open/creating limits, health checks, and leak detection
internal/coalesce/
Singleflight in-flight read coalescer with context-cancellation isolation and batch-size metrics
internal/admission/
CoDel + Adaptive LIFO queue, per-tenant token bucket / GCRA fair-share limiter, and optional AIMD concurrency controller
internal/proxy/
Core request lifecycle pipeline tying protocol, admission, coalescing, pooling, and error semantics together
internal/metrics/
Zero-allocation atomic counters, per-tenant HDR histograms, and Prometheus/JSON stats endpoint
cmd/zgateway/
Main proxy server binary
cmd/loadgen/
Open-loop benchmark & chaos driver (reconnection storm, Zipfian skew, noisy-neighbor profiles)
Phased Commit Roadmap
Commit 0 — DST Harness (internal/sim): Virtual clock, seeded PRNG, fault-injecting backend/network, deterministic replay meta-test.
Commit 1 — Sans-I/O Protocol & Basic Forwarding (internal/protocol, internal/proxy): Streaming parser/encoder, read vs. write classification, golden-byte and arbitrary-chunking fuzz tests.
Commit 2 — Bounded Multiplexed Pool (internal/pool): Connection reuse, strict MaxPoolSize and creation-rate caps, reconnection storm chaos test.
Commit 3 — In-Flight Read Coalescer (internal/coalesce): Singleflight hot-key collapse, write bypass, cancellation isolation tests, Zipfian stampede benchmark.
Commit 4 — CoDel + Adaptive LIFO Queue (internal/admission): Standing-queue detection, FIFO-to-LIFO mode switching, fast local load shedding with distinct error codes.
Commit 5 — Per-Tenant Fairness & Adaptive Concurrency (internal/admission): Per-tenant budgets, noisy-neighbor isolation invariant tests, AIMD concurrency limit reaction to backend stalls.
Commit 6 — Open-Loop Benchmark Suite & README Proof (cmd/loadgen): make bench and make chaos targets generating reproducible before/after tables/charts.
6. Reference Blogs, Papers & Companion Docs
Primary Industry Blogs & Papers
Meta Engineering (Sep 2026): ZGateway: Learnings from Putting a Proxy in Front of ZippyDB — Fan-in/fan-out balls-into-bins model, 19× connection reduction, hot-key coalescing, adaptive load balancing, and per-use-case observability.
Cloudflare Docs & Engineering: How Hyperdrive Works and Inside Hyperdrive: How we solved a rare MySQL caching bug — Edge connection setup, connection pooling, transaction boundaries, and caching pitfalls.
Canva Engineering (Jul & Sep 2026): Session revocations at scale and Canva Engineering — Worker Backpressure — Scaling gateway-side reads and adaptive worker backpressure when downstream dependencies degrade.
Meta / ACM Queue (CoDel & Adaptive LIFO): Fail at Scale & Controlling Queue Delay (The Morning Paper) — Foundational explanation of CoDel queue-delay timeouts, Adaptive LIFO ordering, and concurrency control under overload.
Kathleen Nichols & Van Jacobson (ACM Queue, 2012): Controlling Queue Delay — Original CoDel algorithm paper for preventing standing queues and bufferbloat.
Apollo GraphQL (Oct 2026): How we know a router performance result is real — Statistical rigor for proxy/router benchmarking.
Companion Workspace Documents
Master Systems Engineering Projects Roadmap
Systems Engineering Deep-Dives — Master Reading List
Project 2: High-Throughput Analytics & Edge Gateway (Go) — Reusable GCRA rate-limiter and workload-fuzzing patterns.
Project 4: ZeroMQ Remake in Go (Pure-Go ZMTP 3.1 Messaging Engine) — Reusable sans-I/O framing and backpressure simulator patterns.

