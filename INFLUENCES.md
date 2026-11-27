# INFLUENCES — Mini-ZGateway (Phase 0)

Operational document for Nick's steering. Written by the Fable orchestrator, 2026-11-24.
Nothing past this document is authorized: no PLAN.md, no C4 diagrams, no product code until
Nick steers on section 5.

**Addendum, 2026-11-24.** Three directions from Nick arrived while this document was being
written and are added here without reopening Phase 0: the component-snapshot metrics pattern
(F29), *Release It!* (F30), and horizontal scale of the proxy tier as a design constraint (I36),
with steering questions 13 and 14. The orchestrator wrote these three entries itself from sources
on this machine (the direction note, the text of *Backpressure in Microservices*, and Nick's
*Release It!* notes); no research agent ran and no new external source was fetched. Each entry is
marked *(addendum)*. The gate is unchanged.

**Verification labels.** `read directly` = the primary source was fetched and read in this
Phase 0 run (partial reads are said so). `secondary only` = only a summary or third-party
account was read. `could not verify` = not reached; any content is from model memory and is
marked. Numbers marked *(probe)* come from our own toy scripts, not a published result.

**What could not be verified — said plainly.**

- "Master Systems Engineering Projects Roadmap" and "Systems Engineering Deep-Dives — Master Reading List" (spec §6 companion docs) are Google Docs with no local copy. They were not read and are not used.
- The ZGateway post's formula and result-table images did not render as text, so the balls-into-bins formulas and per-host numbers are not quoted anywhere below.
- On Apollo's post, one reader saw the date "October 7, 2026" on the page while the other saw no date. The post itself exists and was read in full by both.
- Canva's "Worker Backpressure" Part 2 (the actual control law) has not been published.
- Live `queue.acm.org` and `blog.acolyer.org` return 403, so "Fail at Scale", the CoDel paper, and the Morning Paper write-up were read from Wayback Machine copies.
- Several items come from memory only, with no source read: ServiceRouter / Akkio / Shard Manager, ITU-T I.371 (the standard for GCRA, the generic cell rate algorithm; Wikipedia's quotation and one implementation were read), Chiu & Jain 1989 (a scan with no text layer), Gil Tene's talk (not watched), the WFQ papers, Dragonfly / KeyDB, Uber QALM's own post, and Linkerd's retry-budget default values.

**Three facts the spec gets wrong or oversells** (each is acted on below):

1. **The "19× connection reduction" is a model output, not a measurement.** Meta's post derives "roughly 19x" from a balls-into-bins model fed with round mock inputs. With one backend the model degenerates, so our number is simply clients ÷ pool size and must be measured.
2. **"CoDel" in the spec's sources is three different algorithms**, and the one in the cited paper does not fit a request queue (section 3, conflict C1).
3. **Projects 2 and 4 have no product code.** `analytics-edge-gateway` and `zeromq-go` both stopped at plan sign-off. What the spec calls a "reusable GCRA rate-limiter" and a "reusable sans-I/O framing and backpressure simulator" are unreviewed designs plus small probes. The closest *working* prior code to this spec is in `mansio`.

---

## 1. Top-line recommendations

These eight influences should shape the project most, listed in priority order.

1. **"Fail at Scale" request-queue CoDel + Adaptive LIFO — not the CoDel paper's control law** (S7, F2, F4). This is the admission queue. It keeps one timestamp of state per tenant queue. The paper's `interval/√count` drop schedule assumes TCP senders that slow down, and in our probe it let the queue grow without bound at 10× overload.
2. **Meta ZGateway's fairness structure + Netflix's "share binds only under saturation"** (S1, I12, F8). Requests enter per-tenant queues drained round-robin, and a tenant's share is enforced only when the global limit is reached. This mechanism—not a per-tenant rate limit—is what makes the noisy-neighbor invariant pass. GCRA drops to an optional outer gate.
3. **Nick's own mansio admission code and the object-store / zeromq-go simulator lessons** (P11, P13, P14, P8, P4, P9). These mechanisms are measured and have already been attacked, and they decide the simulator architecture: pure decision cores with thin concurrent wrappers, a generated retry contract with a "write may have applied" column, a generated capacity table, and scenario ownership per chunk.
4. **The singleflight lineage: x/sync's seven pitfalls → Tailscale's refcount-and-detach → Vitess's waiter cap** (I24, I25, I26), plus the Hyperdrive MySQL bug's rule "canonical form in the shared slot, encode per recipient" (S4). Together they supply the complete coalescer specification that the spec only sketches.
5. **Twemproxy + Envoy's Redis filter + the Redis protocol rules** (I18, I10, I20). Together they provide a source-backed command allow-list for Variant A (Redis RESP) and establish the precedent that stateful commands are refused rather than pinned. They are also the strongest argument for making Variant A the MVP.
6. **Google SRE book + Finagle retry budget + metastable-failure papers** (I16, I17, I14, F22). The spec's 10% cap comes from the SRE book, Finagle supplies the integer deposit/withdraw mechanics, and the metastability papers change the retry-storm pass criterion to "goodput recovers after the trigger is removed".
7. **Coordinated omission + Apollo's gates, scaled to one noisy VM** (F16, S10, P3). Use an open-loop generator measured from intended send time, treat simulator results as primary evidence, run A/A baselines, allow "inconclusive" as a verdict, and headline exact counts (connections, backend reads, and shed attribution) ahead of latency percentiles.
8. **Netflix AIMD with the in-flight guard, sized by Little's Law** (I11, F11, F14). The adaptive limit takes four lines and one integer. Little's Law shows that the spec's scenarios need a pool of 32 connections, not 20, if the quiet tenant is to have any margin during the 200 ms stall.

**Addendum, 2026-11-24 — three binding directions from Nick, added after the list above was written.** They do not reorder the eight items above; they apply across all of them.

- **Observability is first-class, like testing** (F29). Every component owns a metrics state object and exposes a snapshot. One collector interface reads those snapshots, while exporters (Prometheus, OpenTelemetry, CSV, JSON, and logs) stay separate and never touch components. Question 13.
- ***Release It!* is a checklist over the whole design** (F30). Most of its patterns are already here under other names. What it adds is steady state (every growing structure gets an automatic remover), one timeout-budget table, a health path walled off from the data path, and a four-part row for every protective mechanism.
- **Horizontal scale is a design input from the start** (I36). Every guarantee is stated for N instances—whether it multiplies, weakens, or survives—and the weakened ones are measured. Question 14.

---

## 2. The full list

Each entry lists Source, Verified, Key idea, Borrow (mechanism and spec part), Do not copy, Strength, and Recommendation. Recommendations assume Variant A (Redis RESP) unless stated otherwise; Variant B is Postgres wire, and Variant C holds caching, replicas, hedging and P2C. IDs use prefixes by bucket: S = spec-named, P = prior projects, I = industry systems, F = foundations.

### Bucket 1 — Spec-named influences (spec §6)

#### S1 · Meta — "ZGateway: Learnings from Putting a Proxy in Front of ZippyDB"
- **Source:** https://engineering.fb.com/2026/09/03/core-infra/zgateway-proxy-zippydb-meta/ (3 Sep 2026)
- **Verified:** read directly (full text; formula/table images did not render). Exists as the spec says.
- **Key idea:** Put a stateless proxy tier between >1M client hosts and the ZippyDB fleet. Connections do not vanish; they move to a tier built to hold them, so server fan-in no longer depends on client population. That tier also acts as a single control point for batching, coalescing, admission, and per-use-case metrics.
- **Borrow:** For fairness, borrow "Discriminant load shedding": each request maps to a per-tenant bucket, buckets drain round-robin, and a flooder fills only its own bucket. For batching, use three flush triggers—linger elapsed, payload size, and request count—plus two safety valves: idle eviction of batch-map entries and an in-flight cap on flushed batches. For the concurrency limit, run an AIMD loop that adjusts a shared admission rate. For observability, treat per-use-case metrics as the precondition for shared-tier admission. For benchmarking, note the result shape: ~1,350 tenant buckets under >90% CPU, 6 shedding, the rest 99.9% served, and goodput at 97–98%.
- **Do not copy:** Do not adopt the 19× figure (it comes from a model with mock inputs), CPU as the AIMD signal (ours is backend latency / in-flight count), write batching presented as coalescing, or cross-region routing, consistent-hash balancing, and CDC cache invalidation. The post gives no linger value, batch caps, or AIMD constants, and says nothing on CoDel, LIFO, retry budgets, error codes, or cancellation—those spec parts cannot cite it.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — take the structure, not the numbers.

#### S2 · Cloudflare Hyperdrive docs — how it works, connection pooling, lifecycle, limits
- **Source:** https://developers.cloudflare.com/hyperdrive/concepts/how-hyperdrive-works/ and sibling pages
- **Verified:** read directly (four pages)
- **Key idea:** Split the client-facing handshake from a warm pool near the database, and pool connections in transaction mode, resetting each connection on return.
- **Borrow:** For the pool and errors, expose named limits as explicit config (connect timeout 15 s, idle 10 min, max query 60 s) and return a distinct pool-exhaustion error ("Failed to acquire a connection from the pool") separate from "server connection attempt failed", giving a ready-made pre-forward error code. Keep the warning that a long transaction pins a connection. For Variant B only, borrow the transaction-mode rule table: `SET` is scoped to the transaction, session advisory locks are unsupported, and named prepared statements are driver-dependent.
- **Do not copy:** Do not copy `max_size` as a *soft* limit that may be exceeded, because the spec's invariant requires a hard cap. The edge/origin split saves cross-region round trips and gains nothing on one box. No creation-rate cap or wait-queue bound is documented.
- **Strength:** SECONDARY (CORE if Variant B) · **Recommendation:** ADOPT WITH CHANGE — take the limits and the distinct exhaustion error, and make the cap hard.

#### S3 · Cloudflare Hyperdrive docs — query caching
- **Source:** https://developers.cloudflare.com/hyperdrive/concepts/query-caching/
- **Verified:** read directly
- **Key idea:** Reads are cached by default (`max_age` 60 s, `stale_while_revalidate` 15 s), while writes are never cached. Because there is no invalidation on write, a cached read can stay stale until its TTL expires. Queries calling volatile functions are not cacheable.
- **Borrow:** Nothing in v1. For Variant B later, borrow the volatility test as the "is this safe to coalesce" classifier and the escape hatch of a second cache-disabled endpoint.
- **Do not copy:** Do not adopt TTL caching. Coalescing shares a result only among requests that overlapped in time, whereas a 60 s cache changes read-after-write semantics.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — Variant C scope; see conflict C2.

#### S4 · Cloudflare — "Inside Hyperdrive: How we solved a rare Hyperdrive MySQL caching bug"
- **Source:** https://blog.cloudflare.com/hyperdrive-mysql/ (real title has "Hyperdrive" twice)
- **Verified:** read directly (full post; date not on the fetched page)
- **Key idea:** About 0.001% of cached MySQL queries returned empty results or hung because the cache stored "exactly the bytes written back" to one client. Those bytes depended on per-client capability flags, so another client read them in the wrong encoding.
- **Borrow:** For the coalescer, protocol, and testing, make this an invariant: hold a shared result in one canonical form and encode it per recipient, never fanning out raw backend bytes to waiters whose negotiated protocol may differ. For the pool, re-apply negotiated session state after any connection reset; the Redis analogue is `SELECT`, `CLIENT SETNAME`, and `HELLO` on a pooled connection. Add a property test with mixed-protocol waiters sharing one coalesced read.
- **Do not copy:** Skip the MySQL specifics and the two-node shared-cache topology.
- **Strength:** CORE · **Recommendation:** ADOPT — as a coalescer invariant (it also argues for RESP2-only in v1).

#### S5 · Canva — "Session revocations at scale"
- **Source:** https://www.canva.dev/blog/engineering/session-revocations-at-scale/ (22 Jul 2026)
- **Verified:** read directly
- **Key idea:** Seeding an in-memory revocation cache from MySQL at deploy time caused a coordinated stampede. The fix moved the data into sorted chunks in S3 that gateways binary-search.
- **Borrow:** Borrow only the failure pattern—that deploy or startup is a correlated load event—as one line of motivation for the pool's creation-rate cap.
- **Do not copy:** Copy nothing. The spec's summary ("scaling gateway-side reads") oversells this post; it is about cache seeding via object storage, not proxying or admission.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no mechanism to borrow.

#### S6 · Canva — "Worker Backpressure (Part 1)"
- **Source:** https://www.canva.dev/blog/engineering/worker-backpressure-part-1-how-we-taught-our-queue-workers-to-slow-down/ (17 Sep 2026)
- **Verified:** read directly
- **Key idea:** Each queue worker runs a local feedback loop where dependency-call outcomes feed a pluggable controller. That controller holds one number—a backoff factor from 0.0 to 1.0—applied to the worker's concurrency permits, and does nothing below a set-point failure rate (5% in production).
- **Borrow:** For the concurrency limit, borrow the pluggable `Controller` interface shape and the set-point idea. Use its comparison of alternatives (fixed limits go stale, circuit breakers have no ramp, exponential backoff is per-message) as design rationale.
- **Do not copy:** Do not invent the algorithm and attribute it to Canva, since Part 1 withholds it for an unpublished Part 2. Do not use error rate as the only signal, because a stalled database produces latency rather than errors until timeouts fire. Also note the setting difference: a worker can leave work in a durable queue, whereas a proxy has a waiting client.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the interface shape only; get the control law from AIMD (F11).

#### S7 · Ben Maurer — "Fail at Scale" (ACM Queue, Oct 2015)
- **Source:** https://queue.acm.org/detail.cfm?id=2839461
- **Verified:** read directly via Wayback copy (live page 403)
- **Key idea:** Queues fill with requests nobody is waiting for anymore, turning a small failure into a large one. The article uses three defences: a CoDel-inspired queue *timeout*, adaptive LIFO, and client-side concurrency control.
- **Borrow:** For admission, borrow the timeout rule: if the queue has not been empty in the last N, a queued request may wait only M; otherwise it may wait N ("5 milliseconds for M and 100 ms for N tends to work well"). Also borrow adaptive LIFO: serve FIFO normally, and switch to LIFO "when a queue is starting to form". For errors, borrow a per-backend in-flight cap whose overflow is "immediately marked as an error"—a pre-forward rejection.
- **Do not copy:** Do not treat the constants as universal truths: M = 5 ms was tuned on fast handlers, whereas the spec's own scenarios inject 50–200 ms backend latency. The pseudo-code says "N seconds" where the prose says milliseconds; follow the prose. No LIFO threshold is given.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — take the admission rule with M and N as named config; the mechanism is detailed in F2 and F4.

#### S8 · The Morning Paper — "Fail at Scale & Controlling Queue Delay"
- **Source:** https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/
- **Verified:** read directly via Wayback copy (live site 403)
- **Key idea:** A single post summarising both articles back to back and reproducing the Facebook pseudo-code verbatim.
- **Borrow:** Borrow one framing sentence: the Facebook variant "performs the equivalent of dropping packets by controlling the timeout".
- **Do not copy:** The summary never states that the two algorithms differ in mechanism. Reading only this summary is how they get conflated, which is what happens in the spec's §2.3.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — a reading aid; cite the primary sources.

#### S9 · Nichols & Jacobson — "Controlling Queue Delay" (ACM Queue 2012) and RFC 8289
- **Source:** https://queue.acm.org/detail.cfm?id=2209336 ; https://www.rfc-editor.org/rfc/rfc8289.txt
- **Verified:** read directly (paper's algorithm sections via Wayback; RFC pseudo-code in full)
- **Key idea:** Distinguish a good queue (a burst that drains) from a bad one (a standing queue). Measure each packet's sojourn time at dequeue; if the minimum stays above a 5 ms target for a 100 ms interval, drop a packet and then drop at increasing frequency.
- **Borrow:** For admission and testing, borrow the measurement choices—sojourn time rather than queue length, timestamp at enqueue, decide at dequeue—along with the good-queue/bad-queue vocabulary. Carry across the RFC's tuning guidance: set the interval to roughly the client's reaction time, and the target to a small fraction of it.
- **Do not copy:** Do not copy the control law. Its `interval/√count` spacing assumes TCP throughput falls with the square root of drop probability, whereas a shed Redis request slows no sender down.
- **Strength:** CORE (concepts) · **Recommendation:** ADOPT WITH CHANGE — take the concepts, not the drop scheduler (F1).

#### S10 · Apollo GraphQL — "How we know a router performance result is real"
- **Source:** https://www.apollographql.com/blog/how-we-know-a-router-performance-result-is-real
- **Verified:** read directly (full post). Date "October 7, 2026" seen by one reader only.
- **Key idea:** A benchmark result is usable only if identical re-runs agree closely enough to separate real change from noise. Run three gates in order: is the run valid, do repeats agree, and did the metric change by more than a pre-declared margin? "We don't know" is a reportable verdict.
- **Borrow:** For benchmarking, borrow Gate 1: confirm the load generator delivered within 2% of the target rate and reached steady state, discarding failed runs rather than averaging them. Run repeats and report the coefficient of variation. Use a pre-declared equivalence zone, the four verdicts including "inconclusive", a seeded mock backend ("seeding alone cut run-to-run variation ... from 244% to about 11%"), and a negative control metric.
- **Do not copy:** Skip the confidence-interval method (the post names none), Wasserstein distances, 500-run matrices, and separate nodes for the generator and system (vm-1 is one VM). Apollo built this harness to detect effects of a few percent, while our headline effects are 10×–1000×.
- **Strength:** SECONDARY (CORE for README credibility) · **Recommendation:** ADOPT WITH CHANGE — keep the gates, scaled down (question 7).

#### S11 · Cloudflare — "Pools across the sea" (found while verifying)
- **Source:** https://blog.cloudflare.com/how-hyperdrive-speeds-up-database-access/
- **Verified:** read directly (pooling and architecture sections)
- **Key idea:** Explains session, transaction, and statement pooling and the core difficulty: keeping the driver's view of connection state aligned with the pooled connection's real state.
- **Borrow:** For the pool, borrow the checkout invariant: every connection is "idle and ready for a query" when handed out. For Variant B, borrow prepared-statement tracking and replay.
- **Do not copy:** Do not copy prepared-statement replay, which is a large feature outside a "simple query subset". Note that the two Cloudflare sources disagree on connection cost (5 vs 7 round trips), so cite the specific source if quoting either figure.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the checkout-state invariant only.

#### S12 · Meta — "ZippyDB: Facebook's key value store" (found while verifying)
- **Source:** https://engineering.fb.com/2021/08/06/core-infra/zippydb/
- **Verified:** read directly (skimmed for client/connection content)
- **Key idea:** Describes the store behind ZGateway: RocksDB with Multi-Paxos replication, micro-shards, and selectable read consistency.
- **Borrow:** Take one caution: because reads carry a consistency level, "identical read" must include that consistency level in the coalescing key.
- **Do not copy:** Skip sharding, replication, and placement.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — context for Variant C.

#### S13 · ServiceRouter / Akkio / Shard Manager
- **Source:** OSDI '23, OSDI '18, SOSP '21 papers (mentioned in the Meta posts)
- **Verified:** could not verify (from memory; not fetched)
- **Key idea:** Meta's service mesh, micro-shard migration, and shard placement systems.
- **Borrow:** Nothing.
- **Do not copy:** Skip all of it; fleet-scale routing and placement are out of scope.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no single-backend relevance.

#### S14 · Companion docs — "Master Systems Engineering Projects Roadmap", "Master Reading List"
- **Source:** Nick's Google Docs (no local copy on vm-1)
- **Verified:** could not verify — searched `/root/Software/*`, `/root/briefs`; only mentions of the titles exist. (The Project 2 and Project 4 companion docs do have local copies; they are covered by P1–P7.)
- **Key idea:** Unknown; not readable.
- **Borrow:** Nothing can be stated.
- **Do not copy:** n/a.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — until Nick exports them; they cannot be cited as read.

### Bucket 2 — Nick's own prior projects (repos on vm-1)

A standing rule, confirmed in three repos' own files, is that each project implements its own code. Every entry below is an idea, a convention, or a ruling—never code to copy. kb was searched first (the `kb` on PATH cannot open the VM store; the older binary run from `/root/Raw/knowledge` works).

#### P1 · analytics-edge-gateway (Project 2) — GCRA / token-bucket design
- **Source:** `/root/Software/analytics-edge-gateway/PLAN.md` §7.2, §2.2, Chunk 2
- **Verified:** read directly (plan sections). **No implementation exists** — the repo stopped at plan sign-off.
- **Key idea:** GCRA keeps one `int64` per tenant—its theoretical arrival time (TAT)—updated with a single compare-and-swap, using integer nanoseconds and a rational rate so the bound can be checked exactly. The limiter never reads a clock: `Allow(now, n) (ok, retryAfterNs)`.
- **Borrow:** For fairness and testing, adopt the `now`-as-argument, no-floats contract and evict idle tenants once TAT ≤ now so the tenant table stays bounded. Borrow its test list: exact window bound checked against a `math/big` oracle, TAT never moving backward, a token-bucket-equals-GCRA differential test, and zero allocations per call.
- **Do not copy:** There is no code to copy, and the design is unreviewed. A rate limiter alone does not satisfy the fairness invariant, which sheds "when backend saturation is detected"—that requires in-flight accounting.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the contract and oracle; prove them here for the first time.

#### P2 · analytics-edge-gateway — workload / DST harness conventions
- **Source:** same `PLAN.md` §2, §2.1; probe result in its `agent_work/INDEX.md`
- **Verified:** read directly (plan; probe result as recorded, not re-run)
- **Key idea:** Pass time as an `int64` argument and randomness as an explicit generator. Drive one operation decoder three ways—seeded property test, fuzz engine, and regression files—so a failure found by any one replays in the others, checked against a trivially correct reference model per structure.
- **Borrow:** For testing and benchmarking, borrow named forked random streams so adding a draw does not shift unrelated streams, and print the `REPLAY: SEED=… go test … -run '^Test…$'` line on every failure. Use an event loop keyed by `(timeNs, seq)` in which **a rejected client schedules its own retry as an event**, which makes the retry-storm scenario simulable. Reuse the `Zipf`, `Burst`, `RetryStorm`, and `Mix` generators to drive real processes, and adopt the meta-test table (replay identical, golden digests, fork isolation, and planted bug caught).
- **Do not copy:** Do not use `math/rand` v1 (use `math/rand/v2` PCG), and skip the high-cardinality generator and shrinker sized for that project.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — adopt the conventions as ideas, scoping the determinism claim to the same commit, toolchain, and linux/amd64.

#### P3 · analytics-edge-gateway — benchmark-rigor rulings for vm-1
- **Source:** same `PLAN.md` §2.4; kb entry `vm1-no-hardware-perf-counters.md`
- **Verified:** read directly
- **Key idea:** State the machine's limits before making any claim, commit raw output, and narrow any claim that does not come out in your favour.
- **Borrow:** For benchmarking, account for vm-1's limits: 4 shared vCPUs with no hardware performance counters. Run evidence benchmarks serialised under a machine-wide lock with builders idle, use `-count=10` with median and spread computed by a small in-repo stats script, enforce allocation gates as tests, and note that "the repo ships the benchmark scripts so a reader can re-run them on real hardware".
- **Do not copy:** Skip its cache-line and SWAR material. Note also that the plan has **no open-loop generator**, so it contributes nothing reusable for `cmd/loadgen`.
- **Strength:** CORE · **Recommendation:** ADOPT — these are constraints of the machine, not opinions.

#### P4 · zeromq-go (Project 4) — baton scheduler vs `testing/synctest`
- **Source:** `/root/Software/zeromq-go/PLAN.md` §2.1, §8.1, Appendix A; `agent_work/probes/phase1/`; kb `go-deterministic-goroutine-replay-baton-scheduler.md`
- **Verified:** read directly (plan and kb entry; probe code by declarations; probes not re-run). **Plan and probes only — no product code.**
- **Key idea:** `synctest` virtualises time but does not control the scheduling order of goroutines woken at the same instant (4 goroutines produced all 24 orders across 2,000 runs). A baton scheduler that runs one simulated goroutine at a time, chosen by a seeded generator, produced a single trace digest across 8 processes.
- **Borrow:** For testing (which shapes the pool, coalescer, and admission), bring forward the three-option architectural decision—synctest only, everything single-threaded, or a baton scheduler plus a seam. This project has to make that choice because the spec asks for a single-threaded seeded harness *and* tests for goroutine leaks and 99-of-100 cancellation. Also borrow the digest design: fold every decision, delivered byte range, and fault into SHA-256.
- **Do not copy:** Do not copy the 250-line probe. Its costs: no `go`, `select`, channels, `time`, or `net` below the seam, throughput of ~530,000 scheduler steps/s, and a conflict with idiomatic `context` cancellation, which is how the coalescer rule is written.
- **Strength:** CORE (as a decision) · **Recommendation:** ADOPT WITH CHANGE — bring the decision to Nick (question 8); the recommended answer is sans-I/O cores, not a baton.

#### P5 · zeromq-go — sans-I/O framing tests and the simulated connection
- **Source:** `PLAN.md` §2.1–2.3, Appendix A P7
- **Verified:** read directly
- **Key idea:** Test the codec against byte streams rather than sockets: feed every golden vector whole, one byte at a time, and at every split point, requiring identical output. Model the simulated connection as two independent directions with adversarial chunking, bounded buffers, latency, and faults.
- **Borrow:** For the protocol and testing (spec Commit 1 and §4.1), borrow chunk-invariance as both a table test and a native fuzz target, rejection vectors that return typed errors and never panic, and the rule that "the decoder never allocates ahead of the bytes it has received" (so a RESP bulk length of 2³¹ costs nothing until bytes arrive). Also adopt the fault vocabulary: FIN, RST, half-open silent drop, stall-then-resume, per-direction latency, and chunk profiles.
- **Do not copy:** Skip ZMTP specifics and the libzmq interop harness (here interop is free with `redis-cli`).
- **Strength:** CORE · **Recommendation:** ADOPT — the closest match in this bucket to spec Commit 1.

#### P6 · zeromq-go — high-water-mark bounds and their tests
- **Source:** `PLAN.md` §2.5, §8.2, §8.3
- **Verified:** read directly
- **Key idea:** Every asserted bound must name its "transit term"—the items in hand-off between stages—or an adversarial tester will break it. Prove that a stalled consumer is isolated by comparing healthy peers' virtual delivery times against a control run.
- **Borrow:** For admission, the pool, and testing, state every bound with its transit term. Assert "healthy tenant unaffected" exactly in virtual time against a control run without the noisy tenant, rather than with a tolerance. Enforce that zero never means unlimited, declare a fallback ahead of time (if the clever variant fails its one repair round, land the simple one and narrow the claim), and adopt the goroutine-leak oracle (simulator registry empty *and* `runtime.NumGoroutine()` back at baseline).
- **Do not copy:** Do not block forever at the high-water mark; this spec sheds with a distinct error instead.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the bound discipline and control-run comparison.

#### P7 · zeromq-go and analytics-edge-gateway — per-chunk gate design
- **Source:** zeromq-go `PLAN.md` §2.13–2.14, §7; analytics-edge-gateway `PLAN.md` §2.9, §6
- **Verified:** read directly
- **Key idea:** Run ordered landing gates, prove each gate script with a canary that fails on a planted violation, and run adversarial testing after review so its findings target claims.
- **Borrow:** (process) gate order — hygiene and determinism lint → unit and pinned-seed sims → property → short fuzz → race and real-mode stress → seed sweep and replay meta-test → adversarial → review → prose → acceptance; a broken claim is either repaired or narrowed in writing; plain testing vocabulary in briefs to Astra (an earlier project lost runs to a provider filter over attack wording); done-marker lines because a cut-off can exit 0 silently.
- **Do not copy:** Do not copy the budgets as written, because the two plans disagree and neither was run. Use the object store's measured budgets instead.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the gate list; take the numbers from P10.

#### P8 · object-store — simulator kernel (`internal/env`, `internal/sim`, `simtest`)
- **Source:** `/root/Software/object-store/internal/{env,sim}`; `docs/knowledge/chunk-0b-simulator.md`, `sim-kernel-extraction-note.md`; `/root/briefs/shared-sim-investigation.md`, `shared-sim-ruling-note.md`
- **Verified:** read directly (knowledge entries, investigation, ruling note; code by listing and declarations)
- **Key idea:** A single-threaded discrete-event simulator owns the world. Protocol code is a pure core with no goroutines, locks, or wall clock, enforced by a lint and cross-process replay tests. This is the only simulator among Nick's projects that was built *and* attacked.
- **Borrow:** For testing, borrow stream derivation from `(seed, label)` on `math/rand/v2` PCG, the seed variables `-seed`, `DST_SEED`, and `DST_SEEDS`, and the `RequireDeterministic` (run twice, compare digests) and `Sweep` helpers. Adopt its determinism rules—no float arithmetic in a decision (use parts-per-million) and no map iteration reaching an output—and the freeze rule (once the simulator lands, its API changes only by change request).
- **Do not copy:** Do not copy the code itself (its own extraction note lists the tangles) or its disk, node-incarnation, and clock-skew models. Its "no goroutines, no mutex" rule cannot hold for a shipped connection pool.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — keep the same conventions, re-implemented in-repo.
- **Ruling that carries over:** `/root/briefs/shared-sim-ruling-note.md` (Nick, 2026-11-21) asks for a shared kernel "if feasible ... its own repo that going forward projects will import and extend". No such repo exists today; the object store recorded "later, not now". Open — question 8.

#### P9 · object-store — "incremental e2e per chunk" rule
- **Source:** `/root/briefs/objstore-guide-e2e-wording-ask-2026-11-23.md`, `/root/briefs/flow-guide-e2e-wording-draft.md`, `docs/knowledge/chunk-07-e2e.md`
- **Verified:** read directly. The wording draft is marked DRAFT for Nick's approval; **no file records that approval**, though this mission lists the rule as standing.
- **Key idea:** "The final chunk is never the first time the system runs end to end." Every scenario has an owning chunk, and the harness lands with the first runnable system.
- **Borrow:** For testing and the chunk plan, build a scenario-to-chunk table: reconnection storm with the pool chunk, hot-key stampede with the coalescer chunk, noisy neighbor with fairness, and retry storm with errors/budget. Land the harness (the proxy running against the virtual backend and against a real Redis container) with basic forwarding, and require each scenario to ship a planted-fault canary proving it exercised its subject. Carry over the rule that "An operation that times out or loses its connection has an unknown result, not a failed one", which is the same distinction as pre- vs post-forward errors.
- **Do not copy:** Nothing structural needs to be dropped, though the cost is that middle chunks get larger.
- **Strength:** CORE · **Recommendation:** ADOPT — it overrides the spec's roadmap, which puts `make chaos` / `make bench` in Commit 6 only.

#### P10 · object-store — flow rulings and recorded failures
- **Source:** object-store `STATE.md`; `/root/briefs/objstore-ci-fast-ruling-2026-11-22.md`, `objstore-p2-builder-rules.md`; `docs/knowledge/chunk-06-integration.md`, `chunk-07-e2e.md`
- **Verified:** read directly (rulings by opening sections; pitfall sections of the knowledge entries)
- **Key idea:** The seat arrangement worked for five primitive chunks; then the two integration-shaped chunks needed arbitration, seat swaps and days of closing work — they cost more than the five primitives together.
- **Borrow:** (process) rulings: CI runs only quick tests ("i dont want consistent 30 min ci runs"), long sweeps on demand, the local full gate is the landing authority; builder rules (tests first, canary bug per chunk, coverage floor, frozen simulator API). Lessons: "a clean small sample is not evidence"; statistical checks false-fail single-seed replay — make checks exact per event; counters must be required non-zero; "accepting any allowed error without requiring a success hides a dead feature" (a noisy-neighbor test that tolerates `OVERLOADED` for the quiet tenant passes with it fully starved); gates must fail closed.
- **Do not copy:** the mid-flight seat swaps (owner interventions, not a pattern).
- **Strength:** CORE (process) · **Recommendation:** ADOPT — and size integration chunks honestly.

#### P11 · mansio — nested admission caps with a pure decision core
- **Source:** `/root/Software/mansio/server/internal/front/limiter.go`, `admission.go`
- **Verified:** read directly (`limiter.go` lines 1–200; `admission.go` signatures). **Working, measured code.**
- **Key idea:** A request runs only when its database, its tenant, and the whole process are all under their in-flight caps. Otherwise it queues if every queue has room; if not, it is shed and consumes no slot. The decision state has "no locks, no clock, no channels", and a thin wrapper adds them.
- **Borrow:** For fairness and admission, borrow the two-layer split—a pure decision core inside a concurrent wrapper—as the sans-I/O answer for the admission queue. Adopt its written invariants: per-tenant in-flight ≤ cap, global in-flight equals the sum, work-conserving dispatch, and deleting all-zero tenant entries so memory stays bounded by active tenants. Keep the reason for a global cap ("without the VM cap the process's in-flight count would be tenants × TenantInFlight"); here the hierarchy collapses to tenant ≤ global.
- **Do not copy:** Do not copy the code or rely on fixed caps as the entire policy; mansio's own note says fixed caps are "unrelated to the deadline". A flooding tenant under a static cap still takes slots, which gives isolation rather than fair share.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — take the core/wrapper shape and invariants, and add fair-share shedding.

#### P12 · mansio — shed at admission what would expire in the queue
- **Source:** `server/knowledge/backpressure/2026-11-10-admission-would-expire.md`
- **Verified:** read directly (knowledge entry; code signatures)
- **Key idea:** When a level is at its cap, estimate the new request's finish time from its queue position and the measured completion rate (Little's law). If that finish time is past the request's deadline, refuse it immediately with a retry hint.
- **Borrow:** For benchmarking, borrow the before/after table format. Mansio measured 52 served, 203 expired, and deepest queue 61 before, versus 55 served, 0 expired, 203 shed, and deepest queue 9 after.
- **Do not copy:** Skip the mechanism in v1. It requires client deadlines, which RESP does not carry, and it overlaps with CoDel's job.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — a known alternative to CoDel; promote only as a comparison arm (question 10).

#### P13 · mansio — one generated retry contract, and a token-bucket retry budget
- **Source:** `server/knowledge/backpressure/2026-11-10-retry-contract.md`; `docs/retry-contract.md`; `server/internal/retry/retry.go`
- **Verified:** read directly (knowledge entry in full; parts of the contract and `retry.go`)
- **Key idea:** Every answer the server can return appears as one row in a table generated from code: error code, retry hint, what the client does, whether the attempt is charged to the budget, and **whether a mutation may have applied**. Clients run the table directly as test vectors.
- **Borrow:** For errors and retries, adopt that table as this project's pre-forward vs post-forward contract: `shed` means "nothing ran" and retrying is safe, whereas `expired` means "a write may have committed" and the result is final. For the budget, deposit a ratio on each first attempt, withdraw one token per retry, and cap the bucket so credit expires and a quiet hour cannot bank retries for a storm.
- **Do not copy:** Avoid a float balance with a tolerance (use integers in any simulated decision path) and HTTP status codes (RESP needs its own error strings). Also heed the bug recorded there: charging the server's own pacing signals to the budget caused a gated test to fail.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — use a generated contract table enforced by tests and an integer budget.

#### P14 · mansio — budget callers below the pool; refuse before reading; capacity table
- **Source:** `server/knowledge/backpressure/2026-11-10-controld-pool-budget.md`, `…-edge-admission.md`, `…-capacity-table.md`; `server/internal/slots/slots.go`
- **Verified:** read directly (the rule and measurement sections of each entry; code signatures)
- **Key idea:** Budget callers *below* the backend pool size, or the excess waits invisibly inside the pool and times out—in mansio, "256 resolve slots + 16 admin slots sat in front of a 20-connection pool".
- **Borrow:** For the pool, admission, and observability, check at config-parse time that admission gates sum to less than the pool. Follow the rule that "callers that join a load already in flight take no slot", so coalesced waiters consume no admission or pool slot. Hold nothing while a slow client sends, use one counted slot type instead of ad-hoc channel semaphores, and ship a capacity document **generated from defaults by a test** with one row per queue: what it protects, capacity and config key, full-queue policy, deadline, and metric.
- **Do not copy:** Skip pgxpool, Prometheus rule files, and HTTP admin levers.
- **Strength:** CORE · **Recommendation:** ADOPT — a generated, test-checked table is the cheapest strong proof of "bounded everything".

#### P15 · mansio — clock-driven `context` deadlines
- **Source:** `server/internal/det/clock.go` (`WithTimeout(ctx, clock, d)`, `WithDeadlineCause`)
- **Verified:** read directly (signatures); mansio's own simulator is secondary only
- **Key idea:** Provide a small injectable clock seam whose context deadlines fire on the injected clock.
- **Borrow:** For testing, this lets coalescer and pool code keep idiomatic `context` cancellation while deadlines run on virtual time—the piece the baton plan lacks.
- **Do not copy:** Skip its `time.Time`-based clock (the other projects settled on `int64` nanoseconds) and its simulator.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the clock-driven context idea only.

#### P16 · sqlite-internals / walfleet
- **Source:** kb `sqlite-internals-ch10a-findings.md`, `…ch10b…`; `/root/Software/walfleet` (`sqlite-internals-work` is an empty directory)
- **Verified:** read directly (first corrections of each kb entry; `STATE.md` head)
- **Key idea:** Single-writer fencing and relocation for per-database SQLite files on object storage—concerned with file ownership rather than multiplexing clients onto a server.
- **Borrow:** Nothing for pooling or WAL. Carry over only two habits: count file descriptors per unit of capacity (which matters for the 500-client and 1,000-connect tests against `ulimit -n`), and measure cancellation latency instead of assuming it.
- **Do not copy:** Skip fencing, epochs, and WAL shipping.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — it does not bear on a storage proxy.

#### P17 · dst-research — technique ladder
- **Source:** `/root/Software/dst-research/findings/_technique-ladder.md`, `_taxonomy.md`
- **Verified:** read directly (ladder tiers A–E); TigerBeetle through it is secondary only
- **Key idea:** 45 deterministic-simulation techniques drawn from 21 codebases, ordered from simplest to hardest.
- **Borrow:** For testing, turn the rungs into a Commit 0 checklist: pass a seeded generator explicitly, build a sans-I/O state machine, support seed replay, use a `(time, seq)` queue, detect deadlock as no ready task and no timer, run-twice fingerprint checks, named random streams, golden hashes, testing the tester with planted bugs, **coverage marks that must be hit** (the answer to "checks passed over empty sets"), a fault budget, and a fault-free control run.
- **Do not copy:** Skip the storage-fault tiers, linearizability checking, and consensus liveness modes.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — as the Commit 0 checklist.

#### P18 · systems-trinkets — local service labs
- **Source:** `/root/Software/systems-trinkets` (`software/` compose files for redis, valkey, postgres, pgbouncer, toxiproxy; lessons `pipelining-work`, `cache-aside`)
- **Verified:** read directly for listings only; lesson code not read
- **Key idea:** Ready local containers plus Nick's own measured pipelining lesson.
- **Borrow:** For benchmarking, reuse the compose files for a real-backend leg, and possibly Nick's pipelining measurements.
- **Do not copy:** Do not use k6 as the load generator, because it is closed-loop per virtual user by default.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — convenience, not design input.

#### P19 · *Backpressure in Microservices* (Luca Sepe), as applied to mansio
- **Source:** `/root/Software/book-notes-work/backpressure-plan/books/backpressure-in-microservices/APPLICATION-PLAN.md`
- **Verified:** read directly for the opening and the eleven finding titles only; the book itself was not read
- **Key idea:** Audited mansio against the book's default control—reject new + bounded queue + bounded workers + deadline + metrics—producing the findings that P11–P14 implement.
- **Borrow:** For admission and observability, use the finding list as an audit checklist for our own pipeline: boundaries that cannot be seen, slow clients holding slots, reading before admission, callers over-admitted onto a pool, structures that grow outside any queue, and missing capacity tables.
- **Do not copy:** n/a — its mechanisms already reach this project through P11–P14.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — use as a review checklist at each landing.

#### P20 · Examined and excluded (one entry)
- **Source:** `/root/Software/distributed-transactional-kv`, `let-it-race-analysis`, `durable-actor`, `mini-durable-objects`, `research/graphduck`, `tigerbeetle` (clone)
- **Verified:** read directly for existence, `STATE.md` / `AGENTS.md` heads and listings
- **Key idea:** Respectively: an empty repo with no commits, a book analysis on observability vs control, an early-stage simulation course, a course at lesson 1, graph modelling chapters, and an upstream clone not read here.
- **Borrow:** Nothing. `durable-actor` offers strictly less than P8, and `let-it-race`'s one relevant point (per-tenant metrics as the control signal) is already in the spec.
- **Do not copy:** n/a.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no content bearing on a storage proxy.

### Bucket 3 — Industry systems the spec does not name

GitHub sources were read at the default-branch head on 2026-11-24. Defaults quoted below come from the files read.

#### I1 · PgBouncer
- **Source:** https://github.com/pgbouncer/pgbouncer (`doc/config.md`, `doc/usage.md`, `src/objects.c`); pgbouncer.org features page
- **Verified:** read directly
- **Key idea:** A single-threaded event loop lends a server connection to a client for a session, a transaction, or a statement. Transaction mode delivers the fan-in win and breaks session state by design. Clients beyond the pool size wait in a queue.
- **Borrow:** For the pool, borrow the limit taxonomy: client cap, pool size, reserve pool, server lifetime, idle timeout, connect timeout, and check-before-reuse after idle. For the creation-rate cap, note how `launch_new_connection` allows "only a single connection attempt at a time" per pool, and after a failed login refuses attempts for `server_login_retry` (15 s) while returning errors to new clients immediately instead of queueing them. For observability, take the `SHOW POOLS` vocabulary: active, waiting, logging-in, and max wait. For Variant B, borrow the transaction-mode break list and the prepared-statement rewrite.
- **Do not copy:** Do not adopt the 120 s `query_wait_timeout` or the unbounded FIFO queue, which create the exact standing queue CoDel exists to remove. Opening one connection at a time is also too slow a ramp for 20–32 connections.
- **Strength:** SECONDARY (CORE if Variant B) · **Recommendation:** ADOPT WITH CHANGE — keep the limit taxonomy, bounded concurrent connects, and fail-fast lockout, and replace the wait queue.

#### I2 · PgCat
- **Source:** https://github.com/postgresml/pgcat (`README.md`, `CONFIG.md`)
- **Verified:** read directly
- **Key idea:** PgBouncer-style pooling on a multi-threaded runtime, adding replica load balancing, a ban list, and sharding.
- **Borrow:** For the pool, borrow a short default wait for a connection (1,000 ms compared with PgBouncer's 120 s) and a health check before handing out a long-idle connection.
- **Do not copy:** Skip sharding, replica routing, and mirroring. With one backend, a ban list reduces to a one-host circuit breaker.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — its two useful ideas also appear in I1.

#### I3 · Odyssey (Yandex)
- **Source:** https://github.com/yandex/odyssey (`README.md`, reference `odyssey.conf`)
- **Verified:** read directly
- **Key idea:** A multi-worker pooler that cleans up a server connection after an unclean client exit instead of discarding it, and caps clients in the handshake phase separately from total clients.
- **Borrow:** For the pool, borrow the dirty-connection decision table specifying what to do with a backend connection whose client vanished mid-request: cancel or close it, and never hand it out mid-reply. For admission, borrow `client_max_routing`, a separate cap on clients in accept or handshake that maps to the client side of the reconnection-storm test.
- **Do not copy:** Skip the worker threading model, per-route config language, and auth passthrough.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the dirty-connection table and the in-handshake cap.

#### I4 · Supavisor (Supabase)
- **Source:** https://github.com/supabase/supavisor (`README.md`)
- **Verified:** read directly
- **Key idea:** A clustered multi-tenant pooler where each tenant's pool lives on one node and other nodes proxy traffic to it.
- **Borrow:** Nothing mechanical. Keep one note for the README: "tenant" in Supavisor means a separate database, not multiple tenants sharing one backend.
- **Do not copy:** Skip cluster membership and dynamic pool placement.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — solves a multi-node problem we do not have.

#### I5 · Envoy — overload manager
- **Source:** Envoy docs, `overload_manager.rst`
- **Verified:** read directly
- **Key idea:** Resource monitors report pressure in [0,1], triggers map that pressure to actions, and named "load shed points" are checked at fixed stages along the request path, running the cheapest rejection first.
- **Borrow:** For admission, errors, and observability, adopt named shed points in pipeline order—refuse at accept, refuse after parsing, refuse to open a new upstream connection—as the structure for pre-forward rejection codes, with one counter per shed point.
- **Do not copy:** Skip the heap and CPU monitors and the generic monitor-action framework; our pressure signals are queue wait and in-flight count.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the shed-point list, not the framework.

#### I6 · Envoy — adaptive concurrency filter
- **Source:** Envoy docs `adaptive_concurrency_filter.rst` and its proto
- **Verified:** read directly
- **Key idea:** Periodically pin concurrency very low (default 3) to measure ideal latency, then scale the limit by `(minRTT + buffer) / sampleRTT` plus a square-root headroom term.
- **Borrow:** Nothing beyond what I11 already provides, since the formula is Netflix's gradient.
- **Do not copy:** Do not adopt the minimum-RTT measurement window. Envoy's own docs admit "a noticeable increase in request 503s" during that window and advise retrying on another host, whereas we have no other host.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — a cautionary example for single-backend adaptive limits.

#### I7 · Envoy — circuit breakers and retry budget
- **Source:** Envoy docs `circuit_breaking.rst`, `circuit_breaker.proto`
- **Verified:** read directly
- **Key idea:** Four independent static caps per upstream—connections, pending requests, active requests, and retries—each with its own overflow counter so an operator can see which bound tripped.
- **Borrow:** For the pool, admission, and observability, adopt the rule of "one named counter per bound". For retries, note the concurrency-style budget: active retries stay at or below a percentage of active plus pending requests, with a small floor.
- **Do not copy:** Skip the defaults (1024 is effectively "off" for a 20–32 connection pool) and Envoy's caveat that live connections can exceed `max_connections`.
- **Strength:** CORE · **Recommendation:** ADOPT — take the per-bound counter rule.

#### I8 · Envoy — admission control filter
- **Source:** Envoy docs `admission_control_filter.rst` and its proto
- **Verified:** read directly
- **Key idea:** Rejects requests probabilistically based on recent success rate (using the SRE-book client-throttling formula) with an aggression knob.
- **Borrow:** For benchmarking, consider it as a candidate "well-behaved client" model in the load generator.
- **Do not copy:** Do not use it as the proxy's admission gate. Because it reacts to failures, it kicks in only after the backend is already erroring, whereas queue delay fires earlier. It also puts a random draw into the decision path.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — load-generator side at most.

#### I9 · Envoy — outlier detection
- **Source:** Envoy docs `outlier.rst`, `outlier_detection.proto`
- **Verified:** read directly (proto defaults; doc skimmed)
- **Key idea:** Ejects a host that fails repeatedly or stands out as a statistical outlier compared with its peers.
- **Borrow:** Nothing.
- **Do not copy:** Skip all of it: with one backend there is no peer group, and ejecting the only host causes an outage.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — Variant C only.

#### I10 · Envoy — Redis proxy filter
- **Source:** Envoy docs `other_protocols/redis.rst`, `redis_proxy.proto`, `supported_commands.h` and two C++ sources
- **Verified:** read directly (docs, proto and header; the two C++ files by keyword)
- **Key idea:** Pairs a RESP codec with a command allow-list: every command must be routable or answered locally, and anything else returns an error. Commands from many clients are batched onto one upstream write.
- **Borrow:** For the protocol, borrow allow-list classification with separate sets for simple, multi-key, eval, transaction, and write commands. Answer `PING`, `ECHO`, `QUIT`, `HELLO`, `AUTH`, and `CLIENT SETNAME` in the proxy itself, while the proxy issues backend `AUTH` on connect. Pin one RESP version per listener rather than translating, and drop unsolicited RESP3 Push frames because "forwarding one would desynchronize the reply FIFO". For batching, flush on buffer size or a timer. For errors, return fixed local error strings distinct from pass-through Redis errors.
- **Do not copy:** Skip the 3 ms flush timer (3–15× the spec's window), cluster topology, MOVED/ASK redirects, and key-hash splitting.
- **Strength:** CORE (Variant A) · **Recommendation:** ADOPT — take the allow-list, proxy-answered connection commands, and local error strings.

#### I11 · Netflix concurrency-limits — AIMD, Vegas, Gradient, Gradient2
- **Source:** https://github.com/Netflix/concurrency-limits (`limit/{AIMDLimit,VegasLimit,GradientLimit,Gradient2Limit,WindowedLimit}.java`)
- **Verified:** read directly (all five files and the README)
- **Key idea:** Treat in-flight requests like a TCP congestion window: each completed request reports its latency, in-flight count, and whether it was dropped, and a small pure function computes the new limit.
- **Borrow:** For the concurrency limit, adopt the deterministic signature `update(rtt, inflight, didDrop) → newLimit`. In AIMD, on a drop or `rtt > timeout`, set `limit = limit × 0.9`; otherwise, if `inflight × 2 ≥ limit`, increment to `limit + 1`, then clamp. Keep Gradient2 as the documented upgrade: compare the current sample against a long exponential average (600 samples), compute `gradient = clamp(1.5 × long/short, 0.5, 1.0)`, set `new = limit × gradient + queueSize`, and smooth the result. Keep the guard "only grow when in-flight ≥ limit/2" that appears in all four algorithms, because without it the limit drifts to maximum while idle.
- **Do not copy:** Avoid minimum-RTT baselines (Vegas and Gradient v1), where a single fast `PING` sets a baseline that makes every normal request look congested. Skip the defaults (minimum 20, timeout 5 s), which are sized for a service rather than a 20-connection pool, and avoid floats in the decision path.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — ship AIMD first and keep Gradient2 as a labelled stretch, both in fixed-point arithmetic.

#### I12 · Netflix concurrency-limits — partitioned limiter
- **Source:** same repo, `limiter/AbstractPartitionedLimiter.java`
- **Verified:** read directly
- **Key idea:** Split the global concurrency limit into named partitions by percentage. Each partition's share is a *guarantee, not a cap*: it is enforced only once the global limit is reached, so partitions borrow idle capacity freely.
- **Borrow:** For fairness, take the decision rule: when global in-flight ≥ global limit, admit a request only if its tenant's in-flight count is below its share; otherwise admit. This is the work-conserving form of "shed preferentially from tenants exceeding their fair share".
- **Do not copy:** Do not copy static percentages per named partition, since our tenant set is open—compute shares dynamically from active tenants or weights. Because rounded-up shares can let total in-flight overshoot the global limit, keep the pool cap as a separate hard bound. Skip the reject-delay sleep.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — use share-under-saturation with dynamic shares.

#### I13 · Netflix Tech Blog — "Performance Under Load"
- **Source:** https://netflixtechblog.medium.com/performance-under-load-3e6fa9a60581
- **Verified:** read directly (via the local renderer)
- **Key idea:** Fixed concurrency limits go stale; infer the limit from latency the way TCP infers a congestion window.
- **Borrow:** For the docs, borrow the rationale and the Little's-law framing: the limit equals concurrency plus the queue allowed before latency degrades.
- **Do not copy:** Do not copy its reassurance that a retry "is nearly 100% successful at reaching an instance with available capacity". That assumes many instances; with one backend, a shed request has nowhere else to go.
- **Strength:** BACKGROUND · **Recommendation:** ADOPT — as cited rationale; I11 supplies the mechanism.

#### I14 · Finagle `RetryBudget` (and Linkerd's retry budget)
- **Source:** `finagle-core/.../service/RetryBudget.scala`; Linkerd `reference/retries.md`
- **Verified:** read directly (Finagle source; Linkerd field names — its default values were not on the pages read)
- **Key idea:** Every original request deposits credit and every retry withdraws a larger amount. Credit expires after a TTL, while a small time-based reserve lets low-traffic clients retry at all.
- **Borrow:** For retries, adopt the integer mechanics: deposit 1,000 per request and withdraw `1,000 / ratio` per retry (10,000 at 10%), where a withdrawal either takes the full amount or leaves the balance untouched. Also borrow the `NonRetryable` flag carried on a failure, which encodes the pre- vs post-forward distinction inside the error.
- **Do not copy:** Skip the defaults of 20% and a floor of 10 retries/s—at 50 QPS, that floor alone consumes the whole budget—and do not retry immediately without backoff.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — use this bucket at 10% with a small floor, per tenant.

#### I15 · Finagle / Linkerd — failure accrual and peak-EWMA balancing
- **Source:** Finagle `PeakEwma.scala`, `FailureAccrualFactory.scala`, `FailureAccrualPolicy.scala`; Linkerd load-balancing and circuit-breaking docs
- **Verified:** read directly (those files); power-of-two selection and deadline propagation are from memory
- **Key idea:** Mark a backend dead after repeated failures and allow one probe through after a backoff. Separately, pick the cheaper of two random backends using a latency average that jumps on slow responses and decays slowly.
- **Borrow:** For errors and the pool, adopt failure accrual as the single-backend "down" state machine: after N consecutive failures, mark the backend dead for a jittered exponential backoff, send one probe, and revive on success. Pair this with I1's connect lockout.
- **Do not copy:** Skip cross-host load balancing (there is only one host) and the 5 s minimum dead time (scale it to the test timescale).
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the probe state machine only.

#### I16 · Google SRE book — "Handling Overload" (ch. 21)
- **Source:** https://sre.google/sre-book/handling-overload/
- **Verified:** read directly
- **Key idea:** Rejecting requests still costs work, so clients throttle themselves using their own accept ratio, tag requests with criticality, and cap retries both per request and per client.
- **Borrow:** For retries, this is **the source of the spec's 10%**: "a per-request retry budget of up to three attempts" combined with a per-client budget where "a request will only be retried as long as this ratio is below 10%"—keeping worst-case amplification just under 3× with the per-request cap alone and 1.1× once the ratio is added. For benchmarking, borrow client-side adaptive throttling, `max(0, (requests − K·accepts)/(requests + 1))` with K = 2, as the load generator's well-behaved-client profile. For errors, return an "overloaded; don't retry" reply distinct from a plain failure.
- **Do not copy:** Skip the two-minute window (our scenarios run for seconds) and criticality propagation (RESP clients cannot send it).
- **Strength:** CORE · **Recommendation:** ADOPT — cite this chapter for the 10% limit and implement it with I14's bucket.

#### I17 · Google SRE book — "Addressing Cascading Failures" (ch. 22)
- **Source:** https://sre.google/sre-book/addressing-cascading-failures/
- **Verified:** read directly
- **Key idea:** Overload feeds itself: queues add latency, latency causes timeouts, and timeouts trigger retries. Break the loop with short queues, early rejection, deadline propagation, and bounded jittered retries.
- **Borrow:** For admission, borrow the queue-sizing rule of thumb: keep the queue small relative to the worker pool, "e.g., 50% or less", as the starting point for `MaxQueueDepth`. For testing, borrow the non-linear recovery check: a service that tipped at 11,000 QPS will not recover at 9,000, so assert how long goodput takes to return after overload ends.
- **Do not copy:** Skip the capacity-planning and restart-orchestration sections.
- **Strength:** CORE · **Recommendation:** ADOPT — take the queue-depth default and the recovery assertion.

#### I18 · Twemproxy (nutcracker)
- **Source:** https://github.com/twitter/twemproxy (`README.md`, `notes/redis.md`, `notes/recommendation.md`)
- **Verified:** read directly
- **Key idea:** "Built primarily to reduce the number of connections to the caching servers." It multiplexes many client connections onto very few server connections via pipelining, and simply leaves commands that require connection state unsupported.
- **Borrow:** For the pool, note the proof that tiny pools work: `server_connections` defaults to 1. For the protocol, take its support table as the most conservative command classification—no `MULTI`/`EXEC`/`WATCH`, blocking pops, `SUBSCRIBE`, `SELECT`, client `AUTH`, or `MONITOR`—and keep connection state in the proxy, making auth and database index pool config.
- **Do not copy:** Do not eject the only server, use an infinite default timeout, or reject `PUBLISH` (which is stateless and safe to share).
- **Strength:** CORE (Variant A) · **Recommendation:** ADOPT — take the baseline deny-list and the proxy-owned-state precedent.

#### I19 · McRouter (Meta)
- **Source:** https://github.com/facebook/mcrouter (`README.md`, wiki Features and Home)
- **Verified:** read directly
- **Key idea:** A memcached router where each proxy thread holds exactly one persistent connection per destination, with most of its value in routing policy.
- **Borrow:** Take it as a second precedent for lazy, tiny pools and probe-based recovery, and borrow its framing of a proxy as "a series of queues" where each queue has its own limit.
- **Do not copy:** Skip route handles, failover pools, and especially the reliable delete stream—replying success now and replaying later contradicts honest post-forward error semantics.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — nothing unique to borrow.

#### I20 · Redis protocol rules — pipelining, RESP2 vs RESP3, client tracking
- **Source:** Redis docs (`pipelining.md`, `protocol-spec.md`, `client-side-caching.md`); `redis-specifications` `RESP3.md`
- **Verified:** read directly (relevant sections). A few per-command semantics in the research note's table are marked from memory there.
- **Key idea:** A Redis connection is a strict FIFO: the Nth reply answers the Nth command. That lets a proxy multiplex clients using nothing more than a queue of waiters per backend connection. Anything that breaks the FIFO or leaves state on the connection cannot share one.
- **Borrow:** For the protocol, pool, and batching, adopt the classification table for `internal/protocol` (full table in `agent_work/phase0/research/b3-industry-systems.md`, entry 20): plain reads are forwarded, coalesced, and batched; plain writes are forwarded and batched, never coalesced; transactions, blocking commands, subscribe, `MONITOR`, and client tracking are rejected; `SELECT`, `AUTH`, and `HELLO` are answered or owned by the proxy; and scans are forwarded but never coalesced. Because pipelining mainly saves `read`/`write` syscalls while the server buffers replies, cap pipeline depth per connection. Enforce the per-connection invariant that the i-th reply goes to the i-th pending waiter.
- **Do not copy:** Skip RESP3 in v1, because it forces a decision on Push frames and doubles the reply shapes a coalesced read must hand out.
- **Strength:** CORE (Variant A) · **Recommendation:** ADOPT — allow-list by default, rejecting unknown commands.

#### I21 · Predixy
- **Source:** https://github.com/joyieldInc/predixy (`README.md`, `src/Command.h`, `src/Command.cpp`)
- **Verified:** read directly
- **Key idea:** A Redis proxy that supports the stateful commands other proxies refuse by flagging them `Private` and giving that client a dedicated backend connection.
- **Borrow:** For the protocol, borrow the flag-per-command vocabulary (read, write, admin, private, no-key, multi-key). Its `Private` set—`watch multi blpop brpop brpoplpush psubscribe subscribe`—is the definitive short list of "pin or reject".
- **Do not copy:** Skip private connections in v1. A pinned connection is the "session-level pinning" the spec warns makes fan-in disappear, and blocking commands could pin the whole pool.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the flag vocabulary and reject the private set (question 9).

#### I22 · HAProxy — server queueing
- **Source:** HAProxy `doc/configuration.txt` (`maxconn`, `maxqueue`, `slowstart`, `timeout queue`, priority class/offset)
- **Verified:** read directly (the named keyword sections)
- **Key idea:** Places a per-server concurrency cap with a queue in front; the manual calls `maxconn` "very important as it can save fragile servers from going down under extreme loads".
- **Borrow:** For the concurrency limit and pool, borrow `slowstart`: after a server recovers, grow its limit linearly to 100% over a configured duration—a deterministic alternative to letting AIMD rediscover the limit after a backend restart. For fairness, note its priority *offset* in milliseconds, which shifts a request's effective arrival time to grant a bounded head start without strict priority.
- **Do not copy:** Avoid its unsafe defaults (unlimited `maxconn` and `maxqueue`) and redispatch to another server.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — add slow-start after backend recovery.

#### I23 · nginx — upstream `queue` / `max_conns`
- **Source:** https://nginx.org/en/docs/http/ngx_http_upstream_module.html
- **Verified:** read directly
- **Key idea:** The minimal bounded wait queue: enforce a maximum length and a maximum wait time, rejecting when either is exceeded.
- **Borrow:** Take this as confirmation that (depth, wait) is the minimal queue interface, and as precedent for reporting queue time separately from backend time.
- **Do not copy:** Skip the 60 s default and `max_conns` as another soft cap ("may exceed").
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — confirms an interface without adding a mechanism.

#### I24 · Go `golang.org/x/sync/singleflight`
- **Source:** https://github.com/golang/sync/blob/master/singleflight/singleflight.go
- **Verified:** read directly (whole file)
- **Key idea:** Guards a map of in-flight calls with a mutex: the first caller for a key runs the function, later callers wait for its result, and the entry is removed when the function returns.
- **Borrow:** For the coalescer, take the skeleton: one lock, one map, removing an entry on completion only if it still points to the same call, a `Shared` flag (our coalesced-hits metric), and the leader/follower split.
- **Do not copy:** Avoid its seven pitfalls, each confirmed in the source: (1) no context, so the leader's cancellation fails every waiter; (2) errors fan out to all waiters; (3) a hung leader blocks everyone with no way to leave; (4) panics fan out and can take the process down; (5) `Forget` breaks the one-in-flight guarantee; (6) all waiters alias one value; and (7) the waiter set is unbounded.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — keep the shape, fix all seven pitfalls, and write it from scratch regardless.

#### I25 · Singleflight variants — Tailscale, CockroachDB, groupcache
- **Source:** `tailscale/util/singleflight`, `cockroach/pkg/util/syncutil/singleflight`, `groupcache/singleflight`
- **Verified:** read directly (Tailscale's `DoChanContext`; CockroachDB's doc comments and options; groupcache whole file)
- **Key idea:** Two forks add cancellation isolation. Tailscale runs the shared call on a context "not derived from any of the input contexts", counts waiters, and cancels the upstream call only when the last waiter leaves. CockroachDB makes leader-cancellation inheritance an explicit option that defaults to off.
- **Borrow:** For the coalescer, Tailscale's refcount-and-detach rule is the reference algorithm for "cancel 99 of 100": run the upstream call on a detached context, let each waiter exit on its own context, cancel upstream only when waiters reach zero, and return a waiter's own context error if its context is already done even when a result arrives at the same instant. From CockroachDB, borrow a `NumCalls(key)` test hook and the pattern of joining a flight while holding the lock that guarded the check.
- **Do not copy:** Skip Tailscale's extra goroutine per waiter (1,000 waiters would spawn 1,000 goroutines and introduce non-deterministic scheduling; each client connection already has a goroutine that can wait). A hung backend still needs its own deadline.
- **Strength:** CORE · **Recommendation:** ADOPT — it directly specifies the semantics the spec asks for.

#### I26 · Vitess vttablet — query consolidator
- **Source:** `vitessio/vitess` `go/sync2/consolidator.go`, `query_executor.go`, `txserializer/tx_serializer.go`
- **Verified:** read directly (consolidator outline, two functions, header comment)
- **Key idea:** Production coalescing inside a database proxy: identical `SELECT`s arriving while one is running wait for it and share its result.
- **Borrow:** For the coalescer, adopt the **waiter cap** that x/sync lacks, rejecting duplicates past the cap with resource-exhausted (or, by config, running them independently). Borrow the mode switch `enable | disable | notOnPrimary` so coalescing stays off where read-your-writes matters, plus a small counter tracking how often each key was consolidated for the hot-key metric.
- **Do not copy:** Do not let waiters block without a context or inherit the leader's error, and do not enable the run-independently fallback, which quietly breaks "at most 1 in-flight call per key".
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — take the waiter cap (rejecting on overflow) and a per-command-class coalescing switch.

#### I27 · AWS RDS Proxy pinning (with ProxySQL multiplexing)
- **Source:** AWS docs `rds-proxy-pinning.html`; ProxySQL multiplexing docs
- **Verified:** read directly
- **Key idea:** Instead of forbidding session state, detect it and pin the client to a dedicated backend connection—correct, but it "reduces the effectiveness of connection reuse".
- **Borrow:** For Variant B only, borrow the trigger list as a reject list, and a "currently pinned connections" gauge if pinning is ever added.
- **Do not copy:** Skip pinning in v1, since the spec lists session pinning as the way pooling fails.
- **Strength:** SECONDARY (Variant B) · **Recommendation:** WATCH ONLY — becomes ADOPT WITH CHANGE if Variant B is chosen.

#### I28 · Uber — Cinnamon load shedder (and QALM through it)
- **Source:** https://www.uber.com/blog/cinnamon-using-century-old-tech-to-build-a-mean-load-shedder/
- **Verified:** read directly (part 1 of 3; parts 2–3 not read). QALM: secondary only.
- **Key idea:** A Go load-shedding middleware where a controller chooses what fraction of requests to reject so the queue drains, an auto-tuner sets the in-flight limit, and requests carry a priority.
- **Borrow:** For admission, adopt the pipeline order—tag, rejector, queue, scheduler under an in-flight limit, and queue timeout—and overload detection based on "the last time the request queue was empty", crediting the same Facebook article. Heed its Go-specific warning that channels used as the shedding mechanism become expensive synchronisation points. According to this post, QALM was CoDel plus a *manually configured* concurrency limit (the spec's design), and that manual limit was its adoption problem, which argues for the adaptive limit. For benchmarking, report goodput by priority tier.
- **Do not copy:** Skip the PID controller (tuning three gains is a project of its own) and cross-service priority propagation.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the pipeline order and the QALM lesson, and skip the PID controller.

#### I29 · Amazon Builders' Library — load shedding, retries, fairness, shuffle sharding
- **Source:** four articles at aws.amazon.com/builders-library
- **Verified:** read directly, partially (keyword-matched passages, not end to end)
- **Key idea:** Measure goodput instead of raw throughput, keep rejection cheap, bound retries locally, and treat fairness as layered admission control.
- **Borrow:** For benchmarking, make goodput a headline metric and load-test "far beyond the point where goodput plateaus" to verify that rejection stays cheap. For retries, prefer a local retry token bucket over a circuit breaker, because breakers "introduce modal behavior ... difficult to test". For observability, record which tenant and which operation were shed on every rejection. For fairness, note the composed pair of token buckets (low rate/high burst paired with high rate/low burst).
- **Do not copy:** Skip shuffle sharding—one backend offers nothing to shuffle.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — take goodput as a headline, the cheap-rejection test, and shed attribution.

#### I30 · Kubernetes API Priority and Fairness
- **Source:** Kubernetes flow-control docs; KEP-1040
- **Verified:** read directly, partially (queuing/seats sections; the KEP's shuffle-sharding section)
- **Key idea:** Guards a concurrency limit measured in "seats" with a fixed set of queues: each flow hashes to a small hand of queues and joins the shortest, and unused concurrency is lent between levels.
- **Borrow:** For fairness, borrow request "width"—letting an expensive request occupy more than one seat—as the way to charge a 100-key `MGET`. Keep shuffle-sharded queues as the documented scale-out path if the tenant set ever grows unbounded.
- **Do not copy:** Skip the rest of the machinery: for a handful of tenants, one queue per tenant with round-robin is simpler and exactly fair.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — keep the width idea in mind; nothing to build now.

#### I31 · Stripe — "Scaling your API with rate limiters"
- **Source:** https://stripe.com/blog/rate-limiters
- **Verified:** read directly
- **Key idea:** Runs four limiters in production: per-user rate, per-user concurrency, a fleet-wide reservation for critical traffic, and a utilisation-based shedder.
- **Borrow:** For fairness, pair a per-tenant *concurrency* limiter with the per-tenant *rate* limiter, because a rate limit misses the case where the backend slows down.
- **Do not copy:** Skip central Redis-backed counters. As a correction to a common assumption, this post describes a token bucket and does **not** describe GCRA.
- **Strength:** BACKGROUND · **Recommendation:** ADOPT — take the rate-plus-concurrency pairing, which matches the spec's wording.

#### I32 · Dropbox — Bandaid service proxy
- **Source:** https://dropbox.tech/infrastructure/meet-bandaid-the-dropbox-service-proxy
- **Verified:** read directly (queueing sections)
- **Key idea:** Bandaid "always processes requests in Last In, First Out order" (rather than switching adaptively), fails requests that sit past a timeout without processing them, caps upstream concurrency with a fixed worker pool, and maintains several priority queues per upstream.
- **Borrow:** For admission and fairness, note the matching architecture—per-class queues, one dequeuer, and a fixed concurrency limit—and its argument against dropping on queue *length*, since a full queue cannot distinguish overload from a short burst.
- **Do not copy:** Do not run always-LIFO: on a single RESP client connection, replies must return in request order, so within-connection ordering matters more than in an HTTP proxy.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — keep the architecture, with FIFO as the normal mode.

#### I33 · Shopify Toxiproxy
- **Source:** https://github.com/Shopify/toxiproxy (`README.md`)
- **Verified:** read directly
- **Key idea:** A real-socket fault-injection proxy controlled through named "toxics".
- **Borrow:** For testing, borrow the toxic names as recognisable vocabulary for the simulator's fault injector: `latency`, `down`, `bandwidth`, `slow_close`, `timeout`, `reset_peer`, `slicer`, and `limit_data`.
- **Do not copy:** Do not use Toxiproxy itself as the test harness, since real sockets are not deterministic.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — names only.

#### I34 · joshbohde/codel (Go)
- **Source:** https://github.com/joshbohde/codel
- **Verified:** read directly (README; code by keyword)
- **Key idea:** A Go acquire/release limiter that ports the *network* CoDel control law directly to requests.
- **Borrow:** For benchmarking, borrow its 2×-overload before/after table as a template (plain queue p50 963 ms vs 27.7 ms).
- **Do not copy:** Do not copy the code or the √count control law. The repo is evidence that Go implementations disagree with folly about what "CoDel for requests" means.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — evidence for conflict C1.

#### I35 · Examined and excluded (one entry)
- **Source:** WeChat DAGOR (arXiv 1806.04075), Shopify Semian, ProxySQL (as its own entry), alibaba/sentinel-golang, FluxNinja Aperture, YouTube Doorman, Dragonfly, KeyDB
- **Verified:** DAGOR abstract only; Semian, sentinel-golang, Aperture, Doorman READMEs read directly; Dragonfly and KeyDB could not verify (from memory)
- **Key idea:** Respectively: priority × user-hash admission; a bulkhead and circuit-breaker library; MySQL multiplexing; a flow-control framework; a policy-driven control plane; cooperative global rate limiting; and two Redis-compatible *servers*.
- **Borrow:** Nothing separately. DAGOR's usable part arrives through I28, while Semian's bulkhead is the pool cap and its breaker is I15.
- **Do not copy:** Skip frameworks and control planes, because the spec forbids external core dependencies and the proxy is already the single control point.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no extractable small mechanism not already covered.

#### I36 · Horizontal scale of the proxy tier — a design constraint, with the precedent already in this list *(addendum)*
- **Source:** Nick's direction of 2026-11-24 (`/root/briefs/zgateway-observability-scale-releaseit-note.md` §3); spec §2.1 fan-in arithmetic (backend connections = proxy instances × max pool size); S1 (ZGateway is a "stateless proxy tier"); I4 (Supavisor); I31 (Stripe); F11 (AIMD); *Backpressure in Microservices*, "Local Versus Distributed Backpressure" and "Kubernetes Trade-Offs" (`/root/briefs/zgateway-backpressure-reference.txt`)
- **Verified:** read directly — the note, and those two sections of the book text. The precedent rests on entries already verified above; no new external source was fetched. PgBouncer's habit of running several processes behind `so_reuseport`, each with its own pools, is from memory only and is not relied on.
- **Key idea:** Every guarantee in the spec is written for one proxy process, yet every production proxy it cites runs many. At N instances, each limit either multiplies by N, weakens by N, or survives unchanged—which outcome happens must be decided per mechanism, written down, and measured rather than discovered later. The book's rule is to keep local bounds in every process at all times and add distributed coordination only for a shared scarce dependency, because "a remote controller can fail; local bounds must still hold". State for each limit whether it is per instance or globally coordinated (the book's example: 32 workers per pod scaling from 10 → 50 pods pushes downstream concurrency from 320 → 1,600).
- **Borrow:** For the pool, admission, fairness, coalescing, retries, observability, and testing, make a per-instance-state table a plan deliverable with one row per mechanism. Here is the first cut, to be confirmed by measurement:
  - *Pool hard cap* — multiplies: backend connections stay ≤ N × pool. The fleet bound holds only if pool size is derived from a fleet budget (budget ÷ N). The creation-rate cap multiplies the same way, which matters in a reconnection storm.
  - *Coalescing flights* — weakens: a hot-key burst costs up to N backend reads instead of 1. That is still bounded and still far below one read per client, so accept and measure it.
  - *Queue-wait bound (CoDel state, LIFO switch)* — survives: each instance detects and bounds its own standing queue with no shared state.
  - *Fairness (share under saturation)* — survives only if tenants spread evenly. Because each instance computes shares from its own active tenants, a tenant whose connections land on one instance is judged against a different crowd than one spread across all N. The optional GCRA outer gate multiplies by N unless divided.
  - *Retry budget* — survives where it is a ratio (N instances each at ≤ 10% keep the fleet at ≤ 10%), while the bucket's minimum floor multiplies.
  - *Adaptive concurrency (AIMD)* — survives by design: N independent AIMD loops sharing one bottleneck is the exact setting Chiu & Jain analysed (F11), and each instance reads backend latency itself. What does not adapt is the static pool cap above it.
  - *Metrics* — per instance. Aggregation belongs on the collector side (F29): counters and gauges sum, histograms merge, and ratios are recomputed from merged counters rather than averaged.
  - *Loss of an instance* — surviving instances inherit its clients as a reconnection burst (*Release It!*'s chain reaction, F30).
  Also borrow this testing advantage: because the cores are sans-I/O (question 8), the simulator can run N proxy cores on one event loop at almost no extra cost, making multi-instance scenarios (a reconnection storm split across 3 instances, a hot key seen by 3 coalescers, or one instance killed) exact and replayable.
- **Do not copy:** Do not put central counters in a shared store on the request path (I31 adds a remote decision per request and a new failure mode), do not pin a tenant's pool to one node with others proxying to it (I4 brings in cluster membership), and do not add discovery, gossip, or leader election. Do not claim ZGateway's or Hyperdrive's fleet behaviour as ours, because neither source says how or whether instances coordinate limits.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — use per-instance enforcement with a static, instance-aware config, name and measure every degradation, and leave coordination as a seam. The stance itself is question 14.

### Bucket 4 — Foundational papers and algorithms, reduced to the mechanism we would build

Pseudo-code sketches for each are in `agent_work/phase0/research/b4-foundations.md`. *(probe)*
numbers come from `agent_work/phase0/probes/codelsim.py` and `calc.py` — toy, single-seed scripts
of our own (20 servers × 50 ms = 400/s capacity, Poisson arrivals, 30 s), re-run by the
orchestrator with identical output. They are a reason to test this in Commit 0, not a result.

#### F1 · Network CoDel as an algorithm (RFC 8289 control law)
- **Source:** RFC 8289 §5 pseudo-code
- **Verified:** read directly
- **Key idea:** Once sojourn time has stayed above the target for an interval, drop one packet, then drop the next after `interval/√count`, and keep shortening the gap until delay falls below target.
- **Borrow:** Nothing as the shipped rule. Optionally keep it as a simulator-only baseline so the README can show why it is the wrong fit.
- **Do not copy:** Do not adopt the control law. *(probe)* At 1.2× overload, served-wait p50 was 58 ms; at 2× it was 730 ms; and at 10× the queue never stopped growing (85,292 queued after 30 s). Reaching a 400/s drop rate requires `count = 1600`, roughly 8 s of ramp, while open-loop clients never back off.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — as the implemented algorithm (keep S9's concepts).

#### F2 · Request-queue CoDel — the "Fail at Scale" timeout rule
- **Source:** "Fail at Scale" (S7); constants confirmed there
- **Verified:** read directly (Wayback copy)
- **Key idea:** Instead of dropping on a schedule, set each request's allowed queue wait: if the queue has not been empty in the last N = 100 ms, allow M = 5 ms; otherwise allow N.
- **Borrow:** For admission, keep one `int64` `lastEmptyTime` per queue. On enqueue, if the queue is empty, set `lastEmptyTime = now`, then assign deadline = now + (M if `lastEmptyTime < now − N` else N). On dequeue, shed any request past its deadline with a pre-forward `OVERLOADED`, and if the queue is found empty, set `lastEmptyTime = now`. On the virtual clock, test four invariants: every served request waited ≤ N (exact); queue depth stays the same for a run of length T and a run of 4T; served count stays ≥ 0.95 × capacity so shedding never idles the backend; and a FIFO control on the same seed grows linearly. *(probe)* At 10× overload: p50 wait was 4.3 ms, max was 100.0 ms, depth stayed ≤ 580, and no capacity went idle.
- **Do not copy:** Do not hard-code 5 ms / 100 ms as universal truths—make M and N configurable and size them against backend service time. Avoid the classic bug of updating `lastEmptyTime` only at dequeue, which wrongly gives the first burst after an idle period the short timeout.
- **Strength:** CORE · **Recommendation:** ADOPT — one word of state, and its detector does not depend on dequeue order.

#### F3 · folly `Codel` — Facebook's shipped request-queue rule
- **Source:** `facebook/folly` `folly/executors/Codel.h`, `Codel.cpp`
- **Verified:** read directly (both files; header comment re-fetched by the orchestrator)
- **Key idea:** Track the minimum queueing delay per 100 ms interval. If it exceeded the 5 ms target, mark the queue overloaded and shed any dequeued request that waited more than 2 × target. The header notes this replaces the √n law because it "empirically works better for our services".
- **Borrow:** For observability, borrow the load gauge `min(100, 100 × minDelay / (2 × target))` as a free 0–100 overload metric per tenant, along with the rule that the first sample of an interval is never shed. For testing, keep it as a comparison arm in the simulator.
- **Do not copy:** Do not pair this detector with LIFO dequeue. Because it reads the delay of the *dequeued* request—which under LIFO is the freshest one—the minimum looks healthy. *(probe)* Pairing folly detection with LIFO at 10× overload shed zero requests and let the queue grow without bound. It also decides only at dequeue, so a fully stalled backend dequeues nothing and sheds nothing.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the gauge and comparison arm; use F2 as the detector. (The two research agents split on this; see C1.)

#### F4 · Adaptive LIFO — HHVM's job queue, plus a switch rule and head sweep of our own
- **Source:** "Fail at Scale" (S7); HHVM `hphp/util/job-queue.h` at the commit the article cites
- **Verified:** read directly (article; HHVM header comment and constructor)
- **Key idea:** Serve FIFO normally; when a queue builds up, serve the newest request first because the oldest request's caller has probably already given up. HHVM's actual implementation uses a queue-*length* threshold (off by default, with no recommended value).
- **Borrow:** For admission, use a fixed-capacity ring buffer per tenant, appended in arrival order. Use our own switch rule (no source gives a time-based one): run LIFO while `lastEmptyTime < now − N`—the same detector as F2, adding no extra knob. Before every pop, sweep expired requests off the *head* (always the oldest) and answer them with `OVERLOADED` at once. On overflow, shed the head rather than the new arrival.
- **Do not copy:** Never run LIFO without the head sweep. *(probe)* Without it, stale requests stayed buried: 5,450 remained queued at the end, peak depth hit 11,380, and each held a client waiting for what would become a shed anyway. Also do not apply LIFO *within* one client connection's pipeline, since RESP replies must return in request order.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — the switch rule and head sweep are our design and must be labelled as such.

#### F5 · folly `LifoSem`
- **Source:** `folly/synchronization/LifoSem.h`, `LifoSemMPMCQueue.h`
- **Verified:** read directly
- **Key idea:** A semaphore that wakes waiting *threads* in LIFO order for cache warmth, while the task queue beside it remains an ordinary bounded FIFO.
- **Borrow:** Nothing for request ordering. The same idea does apply to idle pool connections: reuse the most recently used connection first so the rest stay idle and can be reaped.
- **Do not copy:** Do not cite it as the Adaptive LIFO implementation—it is not.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — as an admission influence.

#### F6 · GCRA (generic cell rate algorithm, virtual-scheduling form)
- **Source:** ITU-T I.371 (via Wikipedia's quotation); `throttled/throttled` `rate.go`
- **Verified:** secondary only for the standard; read directly for the implementation
- **Key idea:** Track one number per tenant, its theoretical arrival time (TAT). A request conforms if it does not arrive earlier than the burst tolerance allows. No bucket needs refilling because the clock supplies the advance.
- **Borrow:** For fairness, store one `int64` TAT in nanoseconds with `T = 1e9/rate` and `τ = (burst − 1) × T`. On `allow(now)`, compute `base = max(TAT, now)` and `new = base + T`; reject if `new − now > τ + T` (leaving TAT unchanged and returning the excess as retry-after), and otherwise store `new`. Assert the invariant that over any window, admitted requests ≤ burst + rate × window, and reject configurations where integer `T` loses precision.
- **Do not copy:** Do not advance TAT on rejection (a flooder would lock itself out indefinitely) and do not write `τ = burst × T` (off by one). Also do not expect GCRA to provide fairness on its own: a static 250 QPS limit does nothing when a 200 ms stall drops backend capacity to 100–160 QPS.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — as the optional per-tenant outer rate gate, not the fairness mechanism (question 4).

#### F7 · Token bucket (lazy refill)
- **Source:** `golang.org/x/time/rate` `rate.go`
- **Verified:** read directly (`advance`, `reserveN`)
- **Key idea:** A bucket holding up to `b` tokens that fills at rate `r` per second. Nothing runs in the background; each call adds tokens from elapsed time.
- **Borrow:** For retries and the pool, use this where tokens are deposited by *events* rather than time: the retry budget (I14) and the connection-creation cap. For unit-cost requests, a token bucket makes the exact same decisions as GCRA, giving a clean differential test.
- **Do not copy:** Do not import `x/time/rate` itself—it uses `float64` tokens and is an external module.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the integer, event-deposit form only.

#### F8 · Deficit Round Robin (Shreedhar & Varghese 1995)
- **Source:** SIGCOMM '95 paper; Wikipedia pseudo-code
- **Verified:** read directly with a caveat — the paper is a garbled scan; mechanism confirmed against the pseudo-code
- **Key idea:** Keep one queue per flow and a list of non-empty queues. Each visit adds a quantum to the queue's deficit, letting it send while the head item's cost fits, in constant work per item.
- **Borrow:** For fairness, give each tenant a queue, `deficit`, and `weight`, plus a shared active list. Run the dispatcher whenever a concurrency slot frees and skip tenants at their in-flight cap. With unit cost and quantum 1, this reduces to plain round robin; keeping the deficit field lets an `MGET` of k keys cost k later. Enforce the invariant that while two tenants are both backlogged, their served counts (per weight) differ by at most quantum + max cost.
- **Do not copy:** Skip byte-sized quanta, and avoid two classic bugs: forgetting to zero a queue's deficit when it empties, and inserting a newly active tenant at the head of the active list.
- **Strength:** CORE · **Recommendation:** ADOPT — O(1), integer-only, and it is the exact mechanism behind ZGateway's "buckets drained round-robin".

#### F9 · WFQ / start-time fair queueing / stochastic fairness queueing
- **Source:** Demers, Keshav & Shenker 1989; Goyal et al. 1996; McKenney 1990
- **Verified:** could not verify (from memory)
- **Key idea:** Serve packets in order of virtual finish tags using a priority queue for tighter fairness, or hash flows into a fixed pool of queues.
- **Borrow:** Nothing in v1.
- **Do not copy:** Skip virtual-time bookkeeping and flow hashing: with a handful of explicit tenants and unit costs, the gain over round robin is invisible.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — more state for no measurable benefit here.

#### F10 · FQ-CoDel (RFC 8290)
- **Source:** RFC 8290
- **Verified:** read directly (keyword passages)
- **Key idea:** Runs Deficit Round Robin (DRR) over per-flow queues with an independent CoDel instance *per queue*, giving newly active queues priority.
- **Borrow:** For admission and fairness, keep F2's state per tenant queue. The fairness invariant then holds by construction: a 50 QPS tenant drains its queue continuously, never enters overload mode, and keeps the long timeout, whereas a flooder's queue never empties, flips to the short timeout and LIFO, and absorbs the shedding.
- **Do not copy:** Do not hash flows into queues—a map from tenant to queue avoids collisions. Cap the number of tenant queues and route overflow to a shared default queue.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take per-queue CoDel state, without hashing.

#### F11 · AIMD (Chiu & Jain 1989)
- **Source:** the paper; Netflix `AIMDLimit.java` (I11)
- **Verified:** could not verify the paper (scan without text); read directly for the implementation
- **Key idea:** Increase the limit by a constant on success, and multiply it by a factor below 1 on a congestion signal.
- **Borrow:** For the concurrency limit, maintain one integer `limit`. On a drop or latency over threshold, set `limit = ⌊limit × 0.9⌋`; otherwise, if `inflight × 2 ≥ limit`, bump to `limit + 1`, clamped to [min, pool cap]. Check two invariants: the bounds always hold, and after backend latency steps from 50 ms to 200 ms, the limit is non-increasing until recovery and then rises by at most 1 per completion.
- **Do not copy:** Skip Netflix's defaults (5 s timeout, minimum 20). Set the latency threshold in tens of milliseconds and the minimum to 1–2.
- **Strength:** CORE · **Recommendation:** ADOPT — the spec's named controller; ship it in v1 rather than as an optional extra (question 11).

#### F12 · TCP Vegas (Brakmo & Peterson 1994)
- **Source:** SIGCOMM '94 paper; Netflix `VegasLimit.java`
- **Verified:** read directly (congestion-avoidance section; implementation)
- **Key idea:** Compare expected throughput at the best-ever latency with actual throughput, using the gap to estimate queued work.
- **Borrow:** Background understanding only.
- **Do not copy:** Avoid the minimum-RTT baseline: our observed latency includes pipeline position, which biases the minimum low, and a stale minimum pins the limit at the floor.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — ship AIMD first, and consider Gradient2 if an upgrade is wanted.

#### F13 · Little's Law
- **Source:** Little 1961, `L = λ × W`
- **Verified:** secondary only (statement); the arithmetic is ours *(probe: `calc.py`)*
- **Key idea:** A concurrency limit L and service time W cap throughput at L/W.
- **Borrow:** For the pool, concurrency limit, and benchmarking, use this as both the sizing identity and a test oracle: simulated throughput must equal `min(offered, L/W)`. Applying it to the spec's numbers: 20 connections at 50 ms yield 400 QPS and 32 yield 640 QPS; during a 200 ms stall, those drop to 100 and 160 QPS. Tenant A at 5,000 QPS plus Tenant B at 50 QPS is 12.6× or 7.9× overload normally, rising to 50× or 32× during the stall. **B needs 10 in-flight slots during the stall—exactly half of a 20-connection pool with zero margin—whereas with 32 connections, B's fair share is 16.** At 500 clients, ÷ 32 gives 15.6× (matching the spec's "~15×"), and ÷ 20 gives 25×.
- **Do not copy:** Do not assume one in-flight request per connection: with RESP pipelining, the in-flight limit and pool size are separate knobs.
- **Strength:** CORE · **Recommendation:** ADOPT — and publish the headline scenarios at pool size 32 (question 7).

#### F14 · Balls-into-bins and the power of two choices
- **Source:** Mitzenmacher, Richa & Sitaraman survey (2001); Azar et al. 1994 (not fetched)
- **Verified:** read directly (survey introduction)
- **Key idea:** Placing n balls into n bins at random puts roughly `log n / log log n` in the fullest bin; picking the emptier of two random bins lowers that to roughly `log log n`.
- **Borrow:** Nothing at the backend level—with one bin the model degenerates and fan-in is just proxies × pool. Across 20–32 pool connections, an exact least-pending scan is cheap; the only rule is "do not assign by random or hash".
- **Do not copy:** Do not use the fan-out model to explain our numbers. Because Meta's formula images did not render, we could not verify whether it matches their exact formulas.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — relevant only in Variant C (choosing among replicas).

#### F15 · Coordinated omission (Gil Tene; wrk2)
- **Source:** wrk2 README; HdrHistogram's `RecordCorrectedValue`; Tene's talk (not watched)
- **Verified:** read directly (wrk2 README, the Go port's correction function)
- **Key idea:** A load generator that waits for each reply before sending the next request slows down whenever the server slows down, under-sampling the stall. Measure latency from when each request *should* have been sent.
- **Borrow:** For benchmarking, follow the open-loop rule: compute `intended[i] = t0 + i/rate` (or cumulative exponential draws for Poisson arrivals), send everything whose intended time has passed without waiting on replies, make `latency = now − intended[i]` the headline metric while also recording `now − actual_send[i]` labelled as uncorrected, and report achieved vs intended rate plus a "could not send" count. Enforce the invariant that recorded samples equal intended sends.
- **Do not copy:** Do not apply HdrHistogram's expected-interval back-fill on top of intended-time recording, which counts a stall twice.
- **Strength:** CORE · **Recommendation:** ADOPT — this defines `cmd/loadgen`.

#### F16 · Open versus closed systems (Schroeder, Wierman & Harchol-Balter, NSDI 2006)
- **Source:** "Open Versus Closed: A Cautionary Tale"
- **Verified:** read directly, partially (the numbered principles)
- **Key idea:** Closed-loop and open-loop load generators produce very different response times at the same nominal load.
- **Borrow:** For benchmarking, label every scenario explicitly: noisy neighbor, hot key, and retry storm are open-loop, while the reconnection storm is naturally closed. Use Poisson arrivals for tenant traffic and constant spacing for calibration runs. Note the consequence: `redis-benchmark` is closed-loop, so it can verify protocol compatibility but cannot produce the shedding results.
- **Do not copy:** n/a.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — as a labelling rule in every result table.

#### F17 · HDR Histogram (Gil Tene)
- **Source:** HdrHistogram README; `hdrhistogram-go` `hdr.go`
- **Verified:** read directly
- **Key idea:** Divides exponentially growing value ranges into equal sub-buckets, giving fixed relative error over a wide range in a fixed array of counters with zero allocations on record.
- **Borrow:** For observability and benchmarking, build a minimal integer-only version supporting record, merge, percentile, and reset. *(probe)* Over 1 µs–60 s, 2 significant digits take 2,560 counters (20.5 KB) and 3 digits take 17,408 counters (139 KB). Use 2 digits per tenant × outcome inside the proxy and 3 digits in the load generator. Enforce three invariants: relative error ≤ 10^−digits, total equals the sum of counts, and merging two histograms matches recording both streams into one.
- **Do not copy:** Skip auto-resizing, wire encoding, and the packed variant. Avoid two classic bugs: silently dropping values above the maximum (clamp and count them instead) and returning a bucket's low edge for percentiles.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — write a minimal version from scratch.

#### F18 · DDSketch, t-digest, Prometheus native histograms
- **Source:** DDSketch (VLDB 2019), t-digest, Prometheus spec
- **Verified:** secondary only (abstract, README, spec introduction)
- **Key idea:** Alternative quantile sketches and a histogram exposition format.
- **Borrow:** Nothing.
- **Do not copy:** Avoid t-digest in particular, because its output depends on insertion and merge order, which breaks bit-identical replay.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — one HDR-style histogram covers every need.

#### F19 · benchstat and the Mann-Whitney U test
- **Source:** `golang.org/x/perf/cmd/benchstat` documentation
- **Verified:** read directly
- **Key idea:** Report a median with a confidence interval for each benchmark and compare before/after runs with a rank test, following two rules: "each benchmark should be run at least 10 times" and "interleave before and after runs".
- **Borrow:** For benchmarking, run at least 10 interleaved runs per arm and emit results in Go benchmark text format so benchstat *can* read them.
- **Do not copy:** Do not treat p < 0.05 as "matters", and do not import benchstat into the build—the analytics-edge-gateway plan ruled an in-repo stats script unless Nick approves benchstat (P3).
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — keep the format and run count, use an in-repo median/spread report, and leave benchstat optional (question 7).

#### F20 · Benchmark method papers — Kalibera & Jones; Georges et al.; Mytkowicz et al.
- **Source:** ISMM 2013; OOPSLA 2007; ASPLOS 2009
- **Verified:** read directly, partially (abstracts, introductions, keyword passages)
- **Key idea:** Performance variation enters at several levels, so repeat at the level that varies most, report intervals instead of best-of numbers, and guard against innocuous setup details that can flip conclusions.
- **Borrow:** For benchmarking, make the unit of repetition the *run* (a fresh proxy process and fresh backend state) rather than the individual request, discard warm-up, randomise or interleave run order, and run an A/A baseline (the same binary as both arms) to publish the noise floor.
- **Do not copy:** Skip multi-level variance analysis and bootstrap intervals (no source read—from memory).
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — take the principles, not the heavy machinery.

#### F21 · Metastable failures (Bronson et al., HotOS 2021; Huang et al., OSDI 2022)
- **Source:** https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf ; the OSDI '22 paper
- **Verified:** read directly (HotOS §2.1, §2.4, §3; OSDI abstract and introduction)
- **Key idea:** A trigger pushes a loaded system into a bad state that persists after the trigger ends, held there by a sustaining loop—most often retries. The root cause is the sustaining loop, not the trigger.
- **Borrow:** For testing, retries, and the pool, add a recovery invariant to every chaos scenario: after the fault ends, goodput must return to ≥ 99% of its pre-fault value within a stated virtual-time bound, and queue depth must return to baseline. Use the paper's worked retry example (healthy at 280 QPS, a 10 s outage, then stuck at 560 QPS with zero goodput) as the retry-storm test. Heed its pool anecdote—an outage sustained by a connection pool's reuse order and fixed in one line—by recording pool pick order as an explicit design decision.
- **Do not copy:** Skip fleet-scale mitigations, and do not claim more than a small simulator can demonstrate.
- **Strength:** CORE · **Recommendation:** ADOPT — one more invariant, cheap in the simulator, and a stronger argument than the spec's 3×3×3.

#### F22 · "The Tail at Scale" — hedged requests (Dean & Barroso 2013)
- **Source:** https://cacm.acm.org/research/the-tail-at-scale/
- **Verified:** read directly (via the local renderer)
- **Key idea:** Send a second copy of a slow request to another replica after the 95th-percentile delay.
- **Borrow:** Nothing in Variants A or B.
- **Do not copy:** Do not hedge against a single backend: sending duplicate work to the server that is already slow is just retry amplification under another name.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — Variant C, reads only, if ever.

#### F23 · Zipf key generation (Go `math/rand/v2`; YCSB)
- **Source:** Go `zipf.go`; YCSB `ZipfianGenerator.java`
- **Verified:** read directly; confirmed locally on Go 1.26.8
- **Key idea:** Go's `NewZipf` requires exponent s > 1, so it cannot reproduce YCSB's default 0.99.
- **Borrow:** For benchmarking and testing, use the standard-library generator with a seeded source, and print both the exponent and the resulting top-1% share in every report.
- **Do not copy:** Do not treat the spec's phrase "99/1 Zipfian" as a parameter. *(probe)* Over 100,000 keys, the hottest 1% of keys receive 63% of traffic at s = 1.01, 85% at s = 1.2, and 98% at s = 1.5 (where one key alone takes 38%).
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — use s = 1.2 with stated shares, plus a separate single-hot-key scenario (question 7).

#### F24 · XFetch — probabilistic early expiration (Vattani et al., VLDB 2015)
- **Source:** "Optimal Probabilistic Cache Stampede Prevention"
- **Verified:** read directly (algorithm listing)
- **Key idea:** Recompute a cached value slightly before it expires, with a probability that rises as expiry approaches.
- **Borrow:** Nothing—in-flight coalescing already handles a stampede without a cache.
- **Do not copy:** Do not add a TTL cache in v1.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — relevant only if caching enters scope (Variant C).

#### F25 · Smart batching (Martin Thompson)
- **Source:** https://mechanical-sympathy.blogspot.com/2011/10/smart-batching.html
- **Verified:** read directly
- **Key idea:** Batch without a timer: one writer waits for the first item, then drains whatever else queued while the previous write was in progress. At low load a batch is one item with zero added delay; under load, batches grow automatically.
- **Borrow:** For batching, adopt that flush rule per backend-connection writer: block for one item, poll until `maxBatch` or `maxBytes`, then issue one `write`. This satisfies the spec's "flush immediately when full or under low load" by construction and adds no timer events to simulate. Enforce three invariants: batch size ≤ `maxBatch`, replies match in order, and with one request in the system, enqueue-to-write time is zero on the virtual clock.
- **Do not copy:** Avoid an unbounded feed queue, and keep a fixed 200 µs–1 ms linger window (the spec's example, and ZGateway's) only as a measured experiment.
- **Strength:** CORE · **Recommendation:** ADOPT — timer-free batching as the default (question 11).

#### F26 · FoundationDB simulation
- **Source:** https://apple.github.io/foundationdb/testing.html ; Will Wilson's Strange Loop talk (not watched)
- **Verified:** read directly (documentation)
- **Key idea:** Run the whole system inside one single-threaded process on simulated time, network, and disks so any run replays identically from its seed, then inject failures far harsher than production would produce.
- **Borrow:** For testing, adopt the single-threaded, single-seed, simulated-time model, plus the "swizzle-clogging" pattern (stop a random subset of connections one by one, then restore them in random order) as a fault schedule for the virtual backend.
- **Do not copy:** Skip building a custom language runtime; our equivalent is the discipline of keeping core logic as plain state machines.
- **Strength:** CORE · **Recommendation:** ADOPT — this is the model for `internal/sim`.

#### F27 · TigerBeetle VOPR, Antithesis, turmoil, madsim
- **Source:** TigerBeetle `docs/internals/vopr.md`; Antithesis docs; turmoil and madsim READMEs
- **Verified:** read directly (those pages)
- **Key idea:** Simulators deterministic by "seed + Git commit", achieved by closing the three sources of non-determinism: clocks, interleaving, and randomness.
- **Borrow:** For testing, print the reproduction recipe on every failure.
- **Do not copy:** Skip the tools themselves (Rust, Zig, or a commercial hypervisor), because runtime-swapping does not carry over to Go.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — lineage and vocabulary.

#### F28 · Go `testing/synctest`
- **Source:** https://pkg.go.dev/testing/synctest ; Go 1.25 release notes
- **Verified:** read directly; present locally (Go 1.26.8)
- **Key idea:** Runs a function in a "bubble" with a fake clock that advances only when every goroutine inside the bubble is durably blocked.
- **Borrow:** For testing, use it as the second test layer for the real goroutine shell—covering timers, context deadlines, write-deadline teardown, and leak checks—with real `time` calls and no real sleeps.
- **Do not copy:** Do not use it as the replay harness. Mutex waits and network I/O are not durable blocks (so a fake network is still needed), and it supplies fake time rather than a seeded scheduler—Nick's own probe (P4) saw 24 orderings in 2,000 runs.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — use it as a second layer under the custom simulator, not a replacement.

#### F29 · Component-snapshot metrics — "Metrics Without Framework Lock-In" (*Backpressure in Microservices*, Luca Sepe) *(addendum)*
- **Source:** `/root/briefs/zgateway-backpressure-reference.txt` (text of the book; PDF beside it) — sections "Production-Ready Implementation in Go", "Metrics Without Framework Lock-In", "Recommended Metrics", "Behavior Under Stress"; Nick's direction in `/root/briefs/zgateway-observability-scale-releaseit-note.md` §1. Bucket: **foundational** — it is a mechanism we would build, not a system we would study. P19 is the same book reaching us through mansio; this entry is the book's metrics pattern read at the source.
- **Verified:** read directly for those sections and the passages around them; the rest of the book was not read end to end. The companion repository (`github.com/lucasepe/backpressure-in-microservices`) was not fetched.
- **Key idea:** Each component keeps its own counts and exposes one call—`Snapshot()`—returning "monotonic counters plus current gauges" (`Accepted, Rejected, Dropped, Queued, InFlight`). The service, not the component, maps that snapshot to Prometheus, OpenTelemetry, StatsD, logs, or local diagnostics, because "the package should not own the organization's telemetry stack."
- **Borrow:** For observability across every component, build three layers with one direction of dependency:
  1. *State object per component* — pool, coalescer, admission queue, fairness dispatcher, concurrency limiter, retry budget, batcher, protocol layer, and proxy pipeline. Each holds plain integers and fixed-size histograms (F17) with no telemetry imports, populated only when config enables emission; the plan states the cost when off and when on.
  2. *One collector interface* that walks the components and returns their snapshots as plain values.
  3. *Exporters* that read only the collector: Prometheus text, OpenTelemetry, CSV (for the benchmark and chaos evidence tables), JSON stats endpoint, and logs. Adding an exporter touches no component.
  Take the book's metric vocabulary as the per-component floor: accepted, processed, rejected, dropped, and failed counters; depth, capacity, and in-flight gauges; and a queue-wait histogram (admission to pickup). Follow its label rule: use aggregate, low-cardinality labels (queue, policy, dependency), and never raw tenant ID, request path, job ID, or error text. For this project, Prometheus gets a bounded tenant label (configured tenants, or top-K plus `other`), while full per-tenant detail stays in the snapshot, the JSON endpoint, and CSV. Use its alert rule—"alert on broken protection, not merely active protection"—as README guidance. Add three project-specific rules on top:
  - *Control reads are not exports.* Admission and AIMD consume queue wait and in-flight counts on every request. Those values live in core state, stay always on, and are read in place. The snapshot is a copy for observers and never sits on a decision path.
  - *Snapshots are part of the replay law.* Because the spec demands bit-identical final metrics, a snapshot must contain no wall-clock time and no map-order dependence. Because the cores are single-threaded, the simulator can assert conservation exactly (accepted = processed + failed + dropped + queued + in-flight).
  - *A landing gate.* A component without its snapshot, its exporter mapping, and a test showing its counters move does not land—giving observability the same standing as testing.
- **Do not copy:** Do not copy the reference's single mutex over the whole queue as our hot path (our cores are single-threaded, and the goroutine shell chooses its own synchronisation), do not treat its five-field snapshot as sufficient (it lacks histograms, per-tenant views, and shed reasons), and do not expose raw tenant ID as a Prometheus label. The book says nothing about cost when disabled, snapshot consistency under concurrent writers, or merging across instances; we design those ourselves (I36).
- **Strength:** CORE · **Recommendation:** ADOPT — as the observability architecture. The exporter set and coverage depth for v1 are question 13.

#### F30 · *Release It!* 2nd ed. (Michael Nygard) — stability anti-patterns and patterns, via Nick's litefleet notes *(addendum)*
- **Source:** `/root/Software/book-notes/books/release-it/` — `PLAN.md` (§1 the book's argument, §3 the 18-flaw register, §4.4 coverage), `lessons/README.md` (measured before/after per lesson), lessons 05, 06 and 10, `skills/suggest-stability-patterns/SKILL.md`. Bucket: **foundational** (book).
- **Verified:** read directly for those files (the three lessons: opening sections only; lessons 01–04, 07–09, 11–12 through the index and register). **The book itself was not read** — the PDF is on Nick's Drive, which this run cannot reach; chapter attributions are as the notes give them. Two `kb` searches found no Release It! entry.
- **Key idea:** Systems fail at integration points, and a small fault spreads along whatever path the design leaves open. Ch. 4 names the ways cracks start, and Ch. 5 names the structures that stop them. The notes add a rule for applying them: every pattern is a four-part decision—where it sits, what numbers it needs, what the caller gets when it trips, and which signal shows it tripped. "A pattern missing any of the four is not done."
- **Borrow:** Adopt the four-part rule as the row format for every protective mechanism in the plan, along with the mapping below (litefleet numbers are Nick's own measurements, quoted for scale only):
  - **Timeouts — ADOPT.** Bound every wait with inner timeouts shorter than outer ones: client read and idle, queue wait, pool checkout, backend connect, backend reply, and client write. Ship one generated timeout-budget table beside the capacity table (litefleet: 2.9 s ⊃ 2.5 s ⊃ 2 s; requests in flight during a stall dropped 1,130 → 149). Work must not outlive every caller, so the coalescer's detached flight (I25) carries its own deadline.
  - **Fail fast — ADOPT.** Validate first, then check resources, then do work: reject a bad or disallowed command before it queues, and refuse immediately when the backend is marked down. This extends P14's "refuse before reading" into a rule for every stage.
  - **Circuit breaker — ADOPT WITH CHANGE.** Ship one breaker only: the backend-down state machine from I15 paired with I1's connect lockout. We accept the notes' warning—"a breaker without a fallback just fails faster"—deliberately: with no cache (question 5), a fast pre-forward error *is* the fallback and protects a recovering backend. Answer I29's objection (that breakers add a second mode that is hard to test) by testing both modes in the simulator. Keep per-tenant and per-command breakers on WATCH, since the fairness queues already contain a bad tenant.
  - **Bulkheads per tenant — ADOPT WITH CHANGE.** Litefleet's lesson was that partitioning had to be per tenant rather than per database (neighbours' p99 dropped 534 ms → 87 ms). Ours is a softer wall—a share that binds only under saturation (I12)—because a hard partition wastes idle capacity, while hard per-tenant caps remain option 4c. Add one hard bulkhead: health, stats, and metrics endpoints must never draw on the data path's pool, queues, or locks (in litefleet, a health check sharing the pool timed out under load and marked the service unreachable).
  - **Shed load, handshaking, back pressure — ADOPT (already the core of the spec).** The book adds placement and signalling: shed at the callee and tell the caller what happened. Because RESP has no `Retry-After`, the handshake is a distinct error prefix. Record a plan decision on when to stop reading a client's socket (TCP back pressure) versus read-and-shed (litefleet: goodput after losing one of three gateways rose 11% → 68.5%).
  - **Steady state — ADOPT. The one group nothing else in this document covers.** Give every accumulator an automatic remover and a size gauge: per-tenant queues and histograms for quiet tenants, the coalescer's flight map, batch-map entries (S1's idle eviction), idle backend connections, tenant label sets in exporters, and log volume while shedding. Test this with a long simulated run under tenant churn where every size gauge levels off.
  - **Governor — ADOPT.** Put bounds on how far and how fast any automatic loop can move: the retry budget (I14), plus floor, ceiling, and step limits on AIMD.
  - **Test harness — ADOPT (already Commit 0).** The fault-injecting backend is Nygard's harness; in litefleet, 5 of 12 chaos tests failed on code whose happy-path tests passed.
  - **Anti-patterns — ADOPT as the review checklist at each landing,** mapped to concrete places in this proxy: *integration points* (the backend connection and the client socket), *blocked threads* (goroutines held by slow clients or a stalled backend), *slow responses* (the 150–200 ms stall scenarios), *cascading failure* (slow backend → client timeouts → retries), *chain reaction* (one proxy instance dies and its clients land on the survivors—I36, question 14), *unbalanced capacities* (N × pool against the backend's connection limit, and clients against the proxy's accept limit), *dogpile* (reconnection storm and post-outage recovery), *force multiplier* (AIMD and retries acting on a wrong signal), and *unbounded result sets* (reply size and `MGET` width caps—a memory bound the spec states only in general terms).
  - **Graceful drain on shutdown — ADOPT (small).** From the versions lesson: stop accepting, finish in-flight work, then exit (litefleet: requests cut off by a restart dropped 5 → 0). This is what makes a multi-instance rollout safe.
  - **Let it crash, decoupling middleware, the rest of deployment and versioning — EXCLUDE.** Because the proxy holds no durable state, a crash is a reconnection storm, which is already a scenario. Accepting a write now and delivering it later contradicts honest post-forward errors (the same objection as I19's delete stream). Expand/contract migrations and tolerant readers have nothing to attach to here.
  - **Transparency — see F29.** The notes moved transparency to lesson 1 "because every later pattern is measured through it", and split health into three states (ok / degraded / failed), reporting degraded while shedding or while the breaker is open.
- **Do not copy:** Do not copy litefleet's parameters (seconds-scale HTTP timeouts, whereas ours are milliseconds), HTTP-specific handshaking (`503` + `Retry-After`, load-balancer queue limits), a breaker per dependency (we have one dependency), or hard per-tenant partitions as the default.
- **Strength:** CORE as a checklist and row format; most of its mechanisms already arrive through S7, I14–I17, I12, and P11–P14, while steady state, the timeout table, the health-path bulkhead, and graceful drain are what is new · **Recommendation:** ADOPT WITH CHANGE — per group as marked. PLAN.md must carry the adopted / rejected table with reasons (Nick's direction).

---

## 3. Conflicts and tensions between influences

Each entry states the two competing pulls, what each side costs, and the recommended resolution.

**C1 · Which "CoDel"? (S9/F1 vs S7/F2 vs F3)**
The spec cites the CoDel paper as the algorithm, yet its sources actually contain three different ones: the paper's drop schedule, the "Fail at Scale" queue timeout, and folly's minimum-delay rule. The paper's law produces an unbounded queue under heavy open-loop overload *(probe)*. Folly's rule is the best-specified in code, but it goes blind when paired with LIFO dequeue *(probe)*—and the spec requires LIFO. The timeout rule needs only one timestamp and does not depend on dequeue order, though it produces a saw-tooth (shed until empty, relax for N, repeat) instead of smooth convergence. The two research agents split (one recommended folly, one the timeout rule). **Resolution:** Ship the timeout rule (F2), and keep folly's detector and the RFC law only as simulator comparison arms. Reword the spec's invariant from "wait converges to the CoDel target" to "served wait ≤ N, queue depth bounded regardless of overload duration, backend never idled by shedding". Question 3.

**C2 · Hyperdrive-style query caching vs a no-cache proxy (S3 vs the spec's thesis)**
Caching would cut read QPS more than anything else on the list. Its cost is read-after-write semantics, an invalidation design, and a correctness surface—the Hyperdrive MySQL bug (S4) shows what that surface looks like in production. Coalescing shares a result only among requests that overlap in time, so it needs no staleness contract. **Resolution:** Ship no cache in v1, use coalescing only, and leave caching to Variant C. Question 5.

**C3 · Transaction-mode lending (PgBouncer, Hyperdrive) vs RESP multiplexing (Twemproxy, Envoy)**
Postgres poolers lend an entire connection to one client until the transaction ends, whereas a Redis proxy multiplexes commands from many clients onto one connection at once. They share the fan-in goal but almost no mechanism: Variant B has no cross-client pipelining and no micro-batching, and its hard part is tracking transaction boundaries, whereas Variant A exercises every mechanism in the spec. Carrying both models inside one pool abstraction would fit neither. **Resolution:** Build the multiplexing pool for Variant A, and borrow only pool-*management* ideas from the Postgres side (limit taxonomy, bounded concurrent connects, lockout after connect failure, and the dirty-connection table). Questions 1 and 6.

**C4 · Static CoDel targets vs adaptive concurrency replacing them (S7 vs I11 / I6 / I28)**
One camp argues fixed 5 ms / 100 ms targets need no tuning; Netflix and Envoy argue fixed limits go stale and should be inferred; Uber reports that CoDel plus a *manual* concurrency limit was QALM's adoption problem. Yet the two mechanisms control different things: the queue rule decides *which waiting requests to shed*, while the concurrency limit decides *how much work the backend receives*. Dropping the queue rule leaves a standing queue of stale requests; dropping the adaptive limit leaves a pool hammering a stalled backend at full width. The real hazard is letting two control loops read the same signal. **Resolution:** Keep both with separate signals—queue wait for CoDel, and backend round-trip time (measured per batch, excluding queue wait) for AIMD—under static caps as the hard outer bound, with M and N configurable because 5 ms does not fit a 50–200 ms backend.

**C5 · Variant C ideas (caching, replicas, hedging, P2C) vs v1 scope**
Several strong influences make sense only with multiple backends: outlier detection (I9), power-of-two choices (F14), hedged requests (F22), shuffle sharding (I29, I30), and XFetch (F24). Each adds a mechanism whose tests require a second backend. **Resolution:** Keep all of them WATCH or EXCLUDE for v1. The only concession is keeping the backend behind an interface so adding a second one later is not a rewrite.

**C6 · Adaptive LIFO vs per-tenant round robin vs RESP reply order (S7 vs S1 vs I20)**
"Fail at Scale" flips one global queue to LIFO, ZGateway drains per-tenant buckets round-robin, and RESP requires replies on one client connection to return in request order. Global LIFO lets a flooder's fresh requests jump ahead of a quiet tenant's; per-tenant FIFO loses "freshest first"; and LIFO inside one client's pipeline would require reorder buffers. No source describes how to combine all three. **Resolution (ours, labelled as such):** Drain round robin *across* tenants, run LIFO *within* a tenant's queue only while that queue is in overload mode, and allow at most one queued request per client connection at a time (leaving the rest unread in the socket, which also provides the backpressure the spec asks for).

**C7 · Per-tenant rate limit (GCRA, token bucket, Stripe) vs share-under-saturation (Netflix, ZGateway)**
The spec lists token/GCRA budgets first. A rate cap is simple and predictable, but it throttles a tenant even when the backend is idle and protects nobody during a backend stall. A share that binds only under saturation is work-conserving and matches what the fairness invariant literally states, though in-flight counts can overshoot and a returning tenant must wait for capacity to free up. **Resolution:** Make per-tenant queues, round robin, and share-under-saturation the fairness core, and keep GCRA as an optional, generous outer gate. Question 4.

**C8 · Hard caps vs soft caps (spec invariant vs Hyperdrive, Envoy, nginx, Netflix partitions)**
Four production systems document that their connection or in-flight cap "may be exceeded". Soft caps buy availability during failover, whereas a hard cap can shed traffic while connections are replaced. Still, the hard cap is the exact guarantee this project exists to demonstrate. **Resolution:** Enforce a hard cap counting connecting and active connections together, and contain partition overshoot using the pool cap as a separate bound.

**C9 · Single-threaded seeded simulation vs testing the goroutine code that ships (P8, F26 vs P4, F28)**
The spec calls for a single-threaded seed-driven harness *and* for goroutine-leak and 99-of-100-cancellation invariants. An event loop alone is exact and cheap, but never runs the shipped goroutine wiring. A baton scheduler runs shipped code and replays, but bans `go`, `select`, channels, and `context` idioms below a seam and exists only as a probe. `synctest` alone tests real wiring but cannot replay identically. **Resolution:** Put pure sans-I/O cores (queue, limiter, pool accounting, coalescer bookkeeping, retry budget, and batcher) on the event loop with the replay law stated for the cores, and test a thin goroutine shell under `synctest` and `-race`. The cost is a doubled test surface. Question 8.

**C10 · Reject stateful commands vs pin a connection (Twemproxy, Envoy vs Predixy, RDS Proxy)**
Rejecting stateful commands keeps the fan-in arithmetic exact and the pool invariant simple, at the cost of breaking applications that rely on `MULTI` or `BLPOP`. Pinning preserves compatibility but lets a handful of clients drain the pool. **Resolution:** Reject stateful commands in v1 with a clear error, and document connection pinning with a hard cap as a v2 option. Question 9.

**C11 · Linger-window batching (spec, ZGateway, Envoy) vs timer-free smart batching (F25)**
A linger window is easy to explain and tune, but it adds its full duration to idle latency unless guarded. It also makes "queue wait" ambiguous for CoDel and inflates the latency AIMD observes. Timer-free batching has no knob and adds no idle cost, though batch size becomes an emergent outcome rather than a setting. **Resolution:** Default to timer-free batching bounded by `maxBatch` and `maxBytes`, treat a linger window as a measured experiment only, and exclude batch wait from queue sojourn time.

**C12 · Coalescing: shared errors, waiter caps, and who pays (I24, I26 vs the spec's invariants)**
Sharing the leader's error fails N clients on one transient fault, yet letting waiters re-drive breaks "at most 1 in-flight per key". An uncapped waiter set leaks unbounded memory, yet Vitess's run-independently overflow breaks that same invariant. Finally, when one backend read serves several tenants, charging only the initiator lets others ride free, while charging every waiter over-counts backend load. **Resolution:** Share errors, reject duplicates past a per-key waiter cap, and charge coalesced followers no admission or pool slot (P14) while counting them per tenant as coalesced hits.

**C13 · Model vs measurement; statistical thresholds vs exact checks (S1 vs S10; spec §4.2 vs P6, P10)**
Meta headlines a modelled 19× figure, while Apollo rejects any result that fails its validation gates. Meanwhile, the spec's invariants are statistical ("> 99%", "within 2× baseline"), yet the object store learned that sweep-level statistics cause false failures during single-seed replay. **Resolution:** In simulation, make exact per-run comparisons against a control run. Reserve percentage thresholds for the real-backend benchmark, report measured connection counts in the README, and label any formula-derived number as a model.

**C14 · Patient queues vs honest queues (PgBouncer 120 s, nginx 60 s vs SRE, Cinnamon, CoDel)**
Connection poolers optimise to avoid ever showing an error to the application, whereas the overload literature warns that patience is how a standing queue forms. **Resolution:** Keep queues short and honest, and state that explicitly in the README because PgBouncer users will expect the opposite.

**C15 · Three retry-budget formulations (SRE ratio vs Finagle bucket vs Envoy concurrent share)**
The ratio is easiest to explain, the bucket handles low-QPS tenants and uses integer arithmetic, and the concurrency form needs no time window—and the three disagree during a burst. **Resolution:** Use Finagle's deposit/withdraw bucket configured to the SRE book's 10%, with a small floor.

**C16 · Shared simulator kernel repo vs in-repo `internal/sim` (Nick's 2026-11-21 preference vs spec)**
A shared repo gives one simulator discipline attacked once, but it introduces a first-party dependency on a repo that does not yet exist, clashing with "zero external core dependencies" and the spec's Commit 0. **Resolution:** Keep the kernel in-repo for now, with kernel files free of proxy types so extracting it later stays cheap. Question 8.

**C17 · Incremental e2e per chunk (P9) vs the spec's Commit 6 roadmap**
The spec puts `make chaos` and `make bench` last, whereas the standing rule says the final chunk is never the first end-to-end run. Following the rule makes middle chunks larger, but the object store paid days for deferring end-to-end integration. **Resolution:** Follow the standing rule: each chaos suite and headline benchmark is owned by the chunk that first makes it runnable.

**C18 · Minimum-latency baseline vs long average (Vegas, Envoy vs Gradient2)**
A minimum-latency baseline must be re-measured periodically by pinning concurrency low, causing self-inflicted shedding when there is no second host. A long average needs no probe, but slowly absorbs a degraded backend as the new normal. **Resolution:** Using AIMD with an explicit latency threshold sidesteps the choice in v1; if Gradient2 is added later, use the long average.

---

## 4. Variant implications

| Pushes toward | Influences |
| --- | --- |
| **Variant A (Redis RESP)** | ZGateway's mechanisms (S1: KV gets/puts, shard-keyed batching, hot-key coalescing); Twemproxy, Envoy Redis filter, Redis protocol rules, Predixy (I18, I10, I20, I21) — a complete source-backed command classification; smart batching and pipelining (F25, I20); the Hyperdrive MySQL lesson via RESP2/RESP3 (S4); zeromq-go framing tests (P5); open-loop generation (F15 — RESP is pipelinable); Zipf hot keys (F23); systems-trinkets' Redis labs (P18) |
| **Variant B (Postgres wire)** | Hyperdrive docs and "Pools across the sea" (S2, S11); PgBouncer, PgCat, Odyssey (I1–I3); RDS Proxy pinning (I27); Vitess consolidator as the only coalescing precedent (I26); mansio's pool-budget finding (P14); Little's Law is exact when pool size *is* the concurrency limit (F13) |
| **Variant C only** | Hyperdrive caching (S3), XFetch (F24), ZippyDB context (S12), outlier detection (I9), balls-into-bins / P2C (F14), hedging (F22), shuffle sharding (I29, I30), PgCat replica routing (I2) |
| **Variant-neutral** | All queueing and admission material (S7, F2–F4, F10, I32); fairness (I12, F8, P11); concurrency limits (I11, F11); retries and errors (I14, I16, I17, P13, F21); singleflight lineage (I24, I25); benchmarking (S10, F15–F20, P3); simulation (P2, P4, P8, P17, F26, F28) |

**Does the buffed list change the spec's recommendation of A as the MVP? No — it strengthens it.**

- Variant A exercises every mechanism in the spec. Variant B drops micro-batching and turns a "multiplexed pool" into "transaction-mode lending"; coalescing in Variant B also requires a SQL volatility classifier and must stay outside transactions.
- Three production proxies we can cite have already done the command-classification work for Variant A. Nothing in Nick's prior work includes a Postgres wire parser or transaction-boundary logic, so Variant B would start from zero on its hardest component.
- Evidence from prior projects shows the main cost sits in integration and end-to-end work rather than primitives, and Variant A lets us spend the budget there.
- What Variant B has that Variant A lacks is drama: the fan-in story is clearest against a process-per-connection database (5–7 round trips per connect), and "PgBouncer with CoDel and per-tenant fairness" is a sharper pitch to database-infrastructure teams. That is a real argument for **B as v2**, built on the same admission, fairness, coalescing, and simulator cores—which is why the plan should keep those cores protocol-free.

One scope note inside Variant A: the list argues for **RESP2-only, stateful commands rejected** in v1 (S4, I10, I18, I20). That is narrower than the spec's "RESP2/3" and is question 9.

---

## 5. Steering questions for Nick

Reply with numbers and letters (for example "1a 2a 3a … 10: promote P12"). **Bold** = recommended.

1. **Which variant?**
   **(a) Variant A — Redis RESP — as v1, with cores kept protocol-free so B can be a v2.**
   (b) Variant B — Postgres wire — as v1. (c) A now, with B committed as a planned second phase.

2. **Repo name `mini-zgateway` (Twin's choice).**
   **(a) Keep it.** (b) Rename — say to what. (The name leans on Meta's; the project is equally Hyperdrive- and PgBouncer-shaped, though the spec calls it Mini-ZGateway.)

3. **Which CoDel, and do we reword the spec's invariant?**
   **(a) Ship the "Fail at Scale" timeout rule (not empty for N → allow M), keep state per tenant queue, switch LIFO on the same signal, and sweep expired requests from the head. Reword the invariant to "served wait ≤ N, depth bounded, backend never idled", and keep folly's rule and the RFC law as simulator comparison arms only.**
   (b) Ship folly's minimum-delay rule as the detector. (c) Ship the CoDel paper's control law, as the spec literally cites.

4. **What is the fairness mechanism?**
   **(a) Drain per-tenant queues round-robin, enforce a tenant's share only when the global limit is reached, and keep GCRA as an optional, generous outer rate gate.**
   (b) Use per-tenant GCRA / token-bucket rate budgets as the primary mechanism, as the spec lists.
   (c) Use static per-tenant in-flight caps only (mansio's shape).

5. **Caching.**
   **(a) None in v1 — use in-flight coalescing only, and leave TTL caching to Variant C.**
   (b) Add a small opt-in TTL read cache in v1 with a stated staleness bound.

6. **If A is chosen, how much Postgres-specific influence do we carry?** (If B, read it mirrored.)
   **(a) Carry pool-management ideas only—limit taxonomy, bounded concurrent connects, lockout after connect failure, and the dirty-connection table—plus one README section contrasting our short honest queue with PgBouncer's 120 s wait. Ship no Postgres code and no PgBouncer benchmark leg.**
   (b) Also include a real PgBouncer-vs-direct comparison leg in `make bench` for context.
   (c) Carry none at all.

7. **Benchmark rigor — how far toward Apollo?**
   **(a) "Apollo-lite": treat simulator results as the primary evidence (bit-identical, requiring no statistics). For real-Redis runs, use an open-loop generator, an A/A run to publish the noise floor, ≥ 10 interleaved runs per arm, an achieved-rate-within-2% gate that discards failed runs, medians with spread, and "inconclusive" as an allowed verdict. Compute statistics with an in-repo script and emit Go benchmark format so benchstat stays optional. Make exact counts the headlines (connections, backend reads, and who absorbed the shedding) with latency percentiles as supporting evidence. Run published scenarios at pool 32, use Zipf s = 1.2 with the resulting shares printed plus a single-hot-key scenario, and label the 19× figure as a model.**
   (b) Lighter: one run per scenario, as the spec implies. (c) Heavier: Apollo's full three gates with distribution distances and equivalence zones.

8. **Simulator architecture, and where the kernel lives.**
   **(a) Run pure sans-I/O cores on a single-threaded seeded event loop with the replay law stated for the cores, and test a thin goroutine shell under `testing/synctest` and `-race`. Keep the kernel in this repo, free of proxy types. Carried rulings assumed: CI runs fast tests only, no test-only dependencies, and incremental e2e per chunk.**
   (b) Use a baton scheduler so the shipped goroutine code itself replays (zeromq-go's plan).
   (c) Create the shared simulator-kernel repo now, with this project as its first consumer (your 2026-11-21 preference). (d) Use `synctest` only, accepting non-identical traces.

9. **Redis compatibility scope in v1.**
   **(a) Support RESP2 only with a command allow-list. Reject `MULTI`/`WATCH`, blocking commands, `SUBSCRIBE`, `MONITOR`, and client tracking with a clear error, and document connection pinning as a possible v2.**
   (b) Also pin a capped number of dedicated connections for stateful commands. (c) Also support RESP3.

10. **Should any EXCLUDE or WATCH item be promoted?** Candidates and my answer to each:
    **promote only the RFC CoDel law and folly's detector as simulator comparison arms (already in 3a).** Keep out mansio's would-expire shedding as a second comparison arm (P12), Gradient2 as a v1 deliverable (I11 stretch), hedged requests (F22), the two unread companion docs (S14—export them if you want them considered), and Canva's session-revocation post (S5).

11. **Should any CORE item be demoted, or spec item reshaped?** My three reshapes — confirm or reverse each:
    **(a) Demote GCRA from the fairness mechanism to an optional outer gate (follows 4a).**
    **(b) Ship the AIMD concurrency limit in v1 rather than as an "optional upgrade", since a manual limit was QALM's adoption problem.**
    **(c) Make micro-batching timer-free by default, turning the spec's 200 µs–1 ms linger window into a measured experiment.**
    One possible demotion I do *not* recommend: dropping Adaptive LIFO to FIFO-only for simplicity.

12. **Does the proxy itself ever retry?**
    **(a) Retry only failures where no byte reached the backend, at most once, under a per-tenant 10% deposit/withdraw budget, and never retry a write after forwarding. Have the load generator model clients both with and without their own budgets.**
    (b) Never — the proxy only answers fast and honestly, leaving all retry control to error codes.

13. **Observability scope for v1 — exporter set, and how deep the per-component snapshots go.**
    *(addendum; F29. The architecture itself — state object per component, one collector interface, exporters separate — is Nick's binding direction and is not the question.)*
    **(a) Full coverage, five exporters, standard library only. Every component ships a snapshot held by a landing gate, covering throughput, queue depth / wait / age, CoDel and LIFO state, drops by policy, connections and creation rate, coalescing hits / waiters / cancellations, per-tenant in-flight and shed share, retry-budget use, errors by command and code, latency histograms per tenant and outcome, and goroutine and memory-bound checks. Ship five exporters: a Prometheus text endpoint, a JSON stats endpoint, CSV (the benchmark and chaos evidence tables), a periodic log line, and a hand-written OTLP/HTTP JSON push exporter landed last with no SDK, verified against a real OpenTelemetry Collector container in the long-budget leg. Give Prometheus a bounded tenant label (configured tenants or top-K plus `other`), while keeping full per-tenant detail in JSON and CSV. Leave lock-wait timing and per-key hotness as opt-in diagnostics, off by default, because they cost on the hot path.**
    (b) Same coverage with the first four exporters and no OTLP code. Reach OpenTelemetry by pointing a Collector's Prometheus receiver at our endpoint and state that in the README. This is the cheapest honest answer, though the push path of the collector interface goes unexercised.
    (c) Same as (a), but implement OTLP through the official OpenTelemetry Go SDK isolated in its own Go module so the core keeps zero dependencies. This is the most standard path, though it breaks "no external modules".
    (d) Narrow: ship only the spec's per-tenant list and its Prometheus/JSON endpoint, adding snapshots to other components only when needed.

14. **Horizontal-scale stance for v1.** *(addendum; I36.)*
    (a) Single instance in v1, with the seams designed in (budgets behind an interface, config shaped per fleet, metrics merged outside the components), a multi-instance C4 deployment view, and one multi-instance scenario in the simulator only.
    **(b) Per-instance enforcement with instance-aware config. Support running N instances as a v1 deployment: a static `instances` value divides the fleet budgets (pool size, creation-rate cap, optional rate gate, and retry floor), with no run-time coordination. Put the per-instance-state table (I36) in PLAN.md, stating for each guarantee whether it multiplies, weakens, or survives at N. Measure the accepted degradations—a 3-instance reconnection storm, a hot key across 3 coalescers, a tenant pinned to one instance, and one instance killed—exactly in the simulator and once against real Redis. Let the load generator spread connections, building no balancer, no discovery, and no membership, while leaving coordination as a documented seam.**
    (c) Coordinated budgets: share tenant budgets and the hot-key flight across instances through the backend (leases or counters in Redis). This restores single-instance guarantees but puts a remote decision on the request path and adds a failure mode the spec does not ask for.
    (d) Sticky tenant-to-instance routing so each tenant's state lives on one instance. This requires a routing layer in front and turns losing an instance into a per-tenant outage.

---

## 6. What this changes for the plan

If Nick replies "go with your recommendations", the Phase 1 PLAN.md and C4 set differ from a plan written straight from the spec in these ways:

- **Admission is one design, not three boxes.** The spec's Commits 4 and 5 (CoDel/LIFO, then fairness and AIMD) merge into one pipeline: per-tenant ring-buffer queues with the timeout rule and head sweep, feeding a round-robin dispatcher with share-under-saturation, bounded by an AIMD limit under a hard pool cap. In C4, `internal/admission` shows a pure decision core and a thin concurrent wrapper as separate components, while GCRA moves to an optional gate at the edge instead of the centre.
- **The coalescer chunk is specified by its failure list:** a detached call context, waiter refcount, per-key waiter cap, canonical reply form, shared errors, and followers taking no slot.
- **The protocol chunk ships a generated command-classification table** (allow-list, RESP2) and proxy-owned connection state, rather than "read vs write classification" alone.
- **Two generated, test-held documents become deliverables:** the retry/error contract table (with a "write may have applied" column) and the capacity table (one row per bound, paired with its counter).
- **Commit 0 grows a decision record:** sans-I/O cores + event loop + synctest shell, the replay law scoped to the cores, and a comparison-arm facility (FIFO, RFC CoDel, and folly) so the README's "why not the paper's CoDel" rests on evidence rather than assertion.
- **End-to-end is per chunk.** Each of the five chaos suites and three headline benchmarks gets an owning chunk, and the harness (virtual backend plus a real Redis container) lands with basic forwarding, shrinking the spec's Commit 6 to long-budget runs and README assembly. Every chaos suite also adds a recovery assertion verifying that goodput returns after the fault is removed.
- **Benchmark claims are restated:** headline measured clients → backend connections at pool 32, backend reads per hot-key burst, shed attribution by tenant, and goodput. Replace the ~15×, 99/1 Zipfian, and "converges to target" wordings with what the mechanisms actually do.
- **Integration chunks are sized larger than primitive chunks**, with arbitration budget, because that is where the last project's cost was.
- **Observability becomes a plan section at the level of the testing architecture** (F29, question 13): a metrics state object and snapshot per component, one collector interface, separate exporters, a stated cost model and label policy, per-chunk observability deliverables, and a landing-gate rule that no component lands without its snapshot, its exporter mapping, and a test proving its counters move. Control reads stay separate from, and cheaper than, the export path.
- **The plan carries two more tables and one more view** (F30, I36, question 14): the *Release It!* mapping (each pattern group adopted or rejected, with where it sits, its numbers, what the caller gets, and its signal) and the per-instance-state table (each guarantee at N instances). C4 adds a multi-instance deployment view, the chaos and benchmark suites add multi-instance scenarios, and a steady-state run (tenant churn where every size gauge levels off) joins the invariants.
- **Explicitly absent from the plan:** caching, replicas, hedging, RESP3, connection pinning, any Postgres wire code, a baton scheduler, a shared kernel repo, external modules of any kind, and any run-time coordination between proxy instances.

---

*Counts: 100 influences — 14 spec-named, 20 prior projects, 36 industry systems, 30 foundations
(97 from the original Phase 0 merge plus the three addendum entries I36, F29, F30); 14 steering
questions.
Research notes, briefs and probes: `agent_work/phase0/`, indexed in `agent_work/INDEX.md`.*
