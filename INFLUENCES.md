# INFLUENCES — Mini-ZGateway (Phase 0)

Operational document for Nick's steering. Written by the Fable orchestrator, 2026-11-24.
Nothing past this document is authorized: no PLAN.md, no C4 diagrams, no product code until
Nick steers on section 5.

**How this was made.** Four fresh research agents (one per bucket) read the sources and wrote
notes; the notes are in `agent_work/phase0/research/` (b1–b4) with a verification log of every
URL and path tried. This document is the orchestrator's merge: duplicates removed, cross-bucket
disagreements resolved or turned into steering questions. The orchestrator re-fetched five
high-impact claims directly (the ZGateway post and its "roughly 19x" wording, folly's `Codel.h`
rule, Tailscale's `DoChanContext`, Twemproxy's `server_connections` default, the SRE book's 10%
retry ratio) and re-ran the two probe scripts in `agent_work/phase0/probes/`. Everything else
carries the research agent's label.

**Verification labels.** `read directly` = the primary source was fetched and read in this
Phase 0 run (partial reads are said so). `secondary only` = only a summary or third-party
account was read. `could not verify` = not reached; any content is from model memory and is
marked. Numbers marked *(probe)* come from our own toy scripts, not a published result.

**What could not be verified — said plainly.**

- "Master Systems Engineering Projects Roadmap" and "Systems Engineering Deep-Dives — Master
  Reading List" (spec §6 companion docs): Google Docs with no local copy. Not read. Not used.
- The ZGateway post's formula and result-table images did not render as text. The balls-into-bins
  formulas and per-host numbers are therefore not quoted anywhere below.
- Apollo's post: one reader saw the date "October 7, 2026" on the page, the other saw no date.
  The post itself exists and was read in full by both.
- Canva "Worker Backpressure" Part 2 (the actual control law) is not published.
- Live `queue.acm.org` and `blog.acolyer.org` return 403; "Fail at Scale", the CoDel paper and the
  Morning Paper write-up were read from Wayback Machine copies.
- From memory only (no source read): ServiceRouter / Akkio / Shard Manager, ITU-T I.371 (GCRA's
  standard; Wikipedia's quotation and one implementation were read), Chiu & Jain 1989 (scan with
  no text layer), Gil Tene's talk (not watched), WFQ papers, Dragonfly / KeyDB, Uber QALM's own
  post, Linkerd's retry-budget default values.

**Three facts the spec gets wrong or oversells** (each is acted on below):

1. **The "19× connection reduction" is a model output, not a measurement.** Meta's post derives
   "roughly 19x" from a balls-into-bins model with round mock inputs. With one backend the model
   degenerates; our number is simply clients ÷ pool size and must be measured.
2. **"CoDel" in the spec's sources is three different algorithms**, and the one in the cited paper
   is the wrong one for a request queue (section 3, conflict C1).
3. **Projects 2 and 4 have no product code.** `analytics-edge-gateway` and `zeromq-go` both stopped
   at plan sign-off. The spec's "reusable GCRA rate-limiter" and "reusable sans-I/O framing and
   backpressure simulator" are unreviewed designs plus small probes. The closest *working* prior
   code to this spec is in `mansio`.

---

## 1. Top-line recommendations

The eight influences that should most shape this project, in priority order.

1. **"Fail at Scale" request-queue CoDel + Adaptive LIFO — not the CoDel paper's control law**
   (S7, F2, F4). It is the admission queue. One timestamp of state per tenant queue; the paper's
   `interval/√count` drop schedule assumes TCP senders that slow down, and in our probe it let the
   queue grow without bound at 10× overload.
2. **Meta ZGateway's fairness structure + Netflix's "share binds only under saturation"** (S1, I12,
   F8). Per-tenant queues drained round-robin, with a tenant's share enforced only when the global
   limit is hit. This — not a per-tenant rate limit — is what makes the noisy-neighbor invariant
   pass. GCRA drops to an optional outer gate.
3. **Nick's own mansio admission code and the object-store / zeromq-go simulator lessons** (P11,
   P13, P14, P8, P4, P9). Pure decision cores with thin concurrent wrappers; a generated retry
   contract with a "write may have applied" column; a generated capacity table; scenario ownership
   per chunk. These are measured and already attacked — and they decide the simulator architecture.
4. **The singleflight lineage: x/sync's seven pitfalls → Tailscale's refcount-and-detach → Vitess's
   waiter cap** (I24, I25, I26), plus the Hyperdrive MySQL bug's rule "canonical form in the shared
   slot, encode per recipient" (S4). Together they are the complete coalescer specification the
   spec only sketches.
5. **Twemproxy + Envoy's Redis filter + the Redis protocol rules** (I18, I10, I20). They supply a
   source-backed command allow-list for Variant A and the precedent that stateful commands are
   refused, not pinned. They are also the strongest argument that A is the right MVP.
6. **Google SRE book + Finagle retry budget + metastable-failure papers** (I16, I17, I14, F22). The
   spec's 10% comes from the SRE book; Finagle gives integer deposit/withdraw mechanics; the
   metastability papers change the retry-storm pass criterion to "goodput recovers after the
   trigger is removed".
7. **Coordinated omission + Apollo's gates, scaled to one noisy VM** (F16, S10, P3). Open-loop
   generator measured from intended send time; simulator results as primary evidence; A/A runs;
   "inconclusive" as an allowed verdict; exact counts (connections, backend reads, shed
   attribution) as headlines ahead of latency percentiles.
8. **Netflix AIMD with the in-flight guard, sized by Little's Law** (I11, F11, F14). Four lines and
   one integer for the adaptive limit; Little's Law shows the spec's scenarios need a pool of 32,
   not 20, for the quiet tenant to have any margin during the 200 ms stall.

---

## 2. The full list

Entry fields: Source · Verified · Key idea · Borrow (mechanism + spec part) · Do not copy ·
Strength · Recommendation. Recommendations assume Variant A unless stated. IDs: S = spec-named,
P = prior projects, I = industry systems, F = foundations.

### Bucket 1 — Spec-named influences (spec §6)

#### S1 · Meta — "ZGateway: Learnings from Putting a Proxy in Front of ZippyDB"
- **Source:** https://engineering.fb.com/2026/09/03/core-infra/zgateway-proxy-zippydb-meta/ (3 Sep 2026)
- **Verified:** read directly (full text; formula/table images did not render). Exists as the spec says.
- **Key idea:** A stateless proxy tier between >1M client hosts and the ZippyDB fleet. Connections do not vanish, they move to a tier built to hold them, so server fan-in stops depending on client population. The tier is also one control point for batching, coalescing, admission and per-use-case metrics.
- **Borrow:** (fairness) "Discriminant load shedding": each request maps to a per-tenant bucket, buckets drain round-robin, a flooder fills only its own bucket. (batching) three flush triggers — linger elapsed, payload size, request count — plus two safety valves: idle eviction of batch-map entries and an in-flight cap on flushed batches. (concurrency limit) an AIMD loop adjusting a shared admission rate. (observability) per-use-case metrics as the precondition for shared-tier admission. (benchmarking) result shape: ~1,350 tenant buckets under >90% CPU, 6 shedding, the rest 99.9% served, goodput 97–98%.
- **Do not copy:** the 19× figure (a model with mock inputs); CPU as the AIMD signal (ours is backend latency / in-flight); write batching presented as coalescing; cross-region routing, consistent-hash balancing, CDC cache invalidation. The post gives no linger value, batch caps or AIMD constants, and says nothing on CoDel, LIFO, retry budgets, error codes or cancellation — those spec parts cannot cite it.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — take the structure, not the numbers.

#### S2 · Cloudflare Hyperdrive docs — how it works, connection pooling, lifecycle, limits
- **Source:** https://developers.cloudflare.com/hyperdrive/concepts/how-hyperdrive-works/ and sibling pages
- **Verified:** read directly (four pages)
- **Key idea:** Split the client-facing handshake from a warm pool near the database; pool in transaction mode, resetting the connection on return.
- **Borrow:** (pool, errors) named limits as explicit config (connect timeout 15 s, idle 10 min, max query 60 s); a distinct pool-exhaustion error ("Failed to acquire a connection from the pool") separate from "server connection attempt failed" — a ready-made pre-forward code; the warning that a long transaction pins a connection. (Variant B only) the transaction-mode rule table: `SET` scoped to the transaction, session advisory locks unsupported, named prepared statements driver-dependent.
- **Do not copy:** `max_size` is a *soft* limit that may be exceeded; the spec's invariant is a hard cap. The edge/origin split saves cross-region round trips and gains nothing on one box. No creation-rate cap or wait-queue bound is documented.
- **Strength:** SECONDARY (CORE if Variant B) · **Recommendation:** ADOPT WITH CHANGE — limits and the distinct exhaustion error; make the cap hard.

#### S3 · Cloudflare Hyperdrive docs — query caching
- **Source:** https://developers.cloudflare.com/hyperdrive/concepts/query-caching/
- **Verified:** read directly
- **Key idea:** Reads are cached by default (`max_age` 60 s, `stale_while_revalidate` 15 s), writes never; no invalidation on write, so a cached read can be stale until TTL. Queries calling volatile functions are not cacheable.
- **Borrow:** nothing in v1. For Variant B later: the volatility test as the "is this safe to coalesce" classifier, and the escape hatch of a second cache-disabled endpoint.
- **Do not copy:** TTL caching. Coalescing shares a result only among requests that overlapped in time; a 60 s cache changes read-after-write semantics.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — Variant C scope; see conflict C2.

#### S4 · Cloudflare — "Inside Hyperdrive: How we solved a rare Hyperdrive MySQL caching bug"
- **Source:** https://blog.cloudflare.com/hyperdrive-mysql/ (real title has "Hyperdrive" twice)
- **Verified:** read directly (full post; date not on the fetched page)
- **Key idea:** About 0.001% of cached MySQL queries returned empty results or hung, because the cache stored "exactly the bytes written back" to one client, and those bytes depend on per-client capability flags. Another client read them in the wrong encoding.
- **Borrow:** (coalescer, protocol, testing) an invariant: a shared result is held in one canonical form and encoded per recipient — never fan out raw backend bytes to waiters whose negotiated protocol may differ. (pool) after any connection reset, re-apply negotiated session state; the Redis analogue is `SELECT`, `CLIENT SETNAME`, `HELLO` on a pooled connection. A property test with mixed-protocol waiters on one coalesced read.
- **Do not copy:** MySQL specifics; the two-node shared-cache topology.
- **Strength:** CORE · **Recommendation:** ADOPT — as a coalescer invariant (it also argues for RESP2-only in v1).

#### S5 · Canva — "Session revocations at scale"
- **Source:** https://www.canva.dev/blog/engineering/session-revocations-at-scale/ (22 Jul 2026)
- **Verified:** read directly
- **Key idea:** Seeding an in-memory revocation cache from MySQL at deploy time was a coordinated stampede; the fix moved the data into sorted chunks in S3 that gateways binary-search.
- **Borrow:** only the failure pattern — deploy/startup is a correlated load event — as one line of motivation for the pool's creation-rate cap.
- **Do not copy:** anything. The spec's summary ("scaling gateway-side reads") oversells it; it is about cache seeding via object storage, not proxying or admission.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no mechanism to borrow.

#### S6 · Canva — "Worker Backpressure (Part 1)"
- **Source:** https://www.canva.dev/blog/engineering/worker-backpressure-part-1-how-we-taught-our-queue-workers-to-slow-down/ (17 Sep 2026)
- **Verified:** read directly
- **Key idea:** Each queue worker runs a local feedback loop: dependency-call outcomes feed a pluggable controller holding one number, a backoff factor from 0.0 to 1.0, applied to the worker's concurrency permits. Below a set-point failure rate (5% in production) it does nothing.
- **Borrow:** (concurrency limit) the pluggable `Controller` interface shape and the set-point idea; its comparison of alternatives (fixed limits go stale, circuit breakers have no ramp, exponential backoff is per-message) as design rationale.
- **Do not copy:** the algorithm — Part 1 withholds it for an unpublished Part 2; do not invent it and attribute it to Canva. Error rate as the only signal: a stalled database produces latency, not errors, until timeouts fire. A worker can leave work in a durable queue; a proxy has a waiting client.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the interface shape only; control law from AIMD (F11).

#### S7 · Ben Maurer — "Fail at Scale" (ACM Queue, Oct 2015)
- **Source:** https://queue.acm.org/detail.cfm?id=2839461
- **Verified:** read directly via Wayback copy (live page 403)
- **Key idea:** Queues fill with requests nobody is waiting for, turning a small failure into a large one. Three defences: a CoDel-inspired queue *timeout*, adaptive LIFO, and client-side concurrency control.
- **Borrow:** (admission) the timeout rule: if the queue has not been empty in the last N, a queued request may wait only M; otherwise N. "5 milliseconds for M and 100 ms for N tends to work well". (admission) adaptive LIFO: FIFO normally, LIFO "when a queue is starting to form". (errors) a per-backend in-flight cap whose overflow is "immediately marked as an error" — a pre-forward rejection.
- **Do not copy:** the constants as truths — M = 5 ms was tuned on fast handlers; the spec's own scenarios inject 50–200 ms backend latency. The pseudo-code says "N seconds" where the prose says milliseconds; use the prose. No LIFO threshold is given.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — the admission rule; M and N as named config, mechanism in F2/F4.

#### S8 · The Morning Paper — "Fail at Scale & Controlling Queue Delay"
- **Source:** https://blog.acolyer.org/2015/11/19/fail-at-scale-controlling-queue-delay/
- **Verified:** read directly via Wayback copy (live site 403)
- **Key idea:** One post summarising both articles back to back, reproducing the Facebook pseudo-code verbatim.
- **Borrow:** one framing sentence: the Facebook variant "performs the equivalent of dropping packets by controlling the timeout".
- **Do not copy:** it never says the two algorithms differ in mechanism; reading only this summary is how they get conflated — which is what the spec's §2.3 does.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — a reading aid; cite the primaries.

#### S9 · Nichols & Jacobson — "Controlling Queue Delay" (ACM Queue 2012) and RFC 8289
- **Source:** https://queue.acm.org/detail.cfm?id=2209336 ; https://www.rfc-editor.org/rfc/rfc8289.txt
- **Verified:** read directly (paper's algorithm sections via Wayback; RFC pseudo-code in full)
- **Key idea:** Tell a good queue (a burst that drains) from a bad one (a standing queue). Measure each packet's sojourn time at dequeue; if the minimum stays above a 5 ms target for a 100 ms interval, drop, then drop at increasing frequency.
- **Borrow:** (admission, testing) the measurement choices — sojourn time not queue length, timestamp at enqueue, decide at dequeue; the good-queue/bad-queue vocabulary; the RFC's tuning guidance read across: interval ≈ the client's reaction time, target a small fraction of it.
- **Do not copy:** the control law. `interval/√count` spacing exists because TCP throughput falls with the square root of drop probability. A shed Redis request slows no sender down.
- **Strength:** CORE (concepts) · **Recommendation:** ADOPT WITH CHANGE — concepts yes, drop scheduler no (F1).

#### S10 · Apollo GraphQL — "How we know a router performance result is real"
- **Source:** https://www.apollographql.com/blog/how-we-know-a-router-performance-result-is-real
- **Verified:** read directly (full post). Date "October 7, 2026" seen by one reader only.
- **Key idea:** A result is usable only if identical re-runs agree closely enough to separate change from noise. Three gates in order: is the run valid, do repeats agree, did it change by more than a pre-declared margin. "We don't know" is a reportable verdict.
- **Borrow:** (benchmarking) Gate 1: load generator delivered within 2% of target rate, steady state reached; failed runs discarded, not averaged. Repeats with coefficient of variation reported. A pre-declared equivalence zone and the four verdicts including "inconclusive". A seeded mock backend ("seeding alone cut run-to-run variation ... from 244% to about 11%"). A negative control metric.
- **Do not copy:** the confidence-interval method (the post names none); Wasserstein distances; 500-run matrices; separate nodes for generator and system (vm-1 is one VM). It is a harness for effects of a few percent; our headline effects are 10×–1000×.
- **Strength:** SECONDARY (CORE for README credibility) · **Recommendation:** ADOPT WITH CHANGE — the gates, scaled down (question 7).

#### S11 · Cloudflare — "Pools across the sea" (found while verifying)
- **Source:** https://blog.cloudflare.com/how-hyperdrive-speeds-up-database-access/
- **Verified:** read directly (pooling and architecture sections)
- **Key idea:** Explains session / transaction / statement pooling and the core difficulty: keeping the driver's idea of connection state aligned with the pooled connection's real state.
- **Borrow:** (pool) the checkout invariant: every connection is "idle and ready for a query" when handed out. (Variant B) prepared-statement tracking and replay.
- **Do not copy:** prepared-statement replay — a large feature outside a "simple query subset". The two Cloudflare sources disagree on connection cost (5 vs 7 round trips); cite with source.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the checkout-state invariant only.

#### S12 · Meta — "ZippyDB: Facebook's key value store" (found while verifying)
- **Source:** https://engineering.fb.com/2021/08/06/core-infra/zippydb/
- **Verified:** read directly (skimmed for client/connection content)
- **Key idea:** The store behind the gateway: RocksDB with Multi-Paxos replication, micro-shards, selectable read consistency.
- **Borrow:** one caution: reads carry a consistency level, so "identical read" must include it in the coalescing key.
- **Do not copy:** sharding, replication, placement.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — context for Variant C.

#### S13 · ServiceRouter / Akkio / Shard Manager
- **Source:** OSDI '23, OSDI '18, SOSP '21 papers (mentioned in the Meta posts)
- **Verified:** could not verify (from memory; not fetched)
- **Key idea:** Meta's service mesh, micro-shard migration and shard placement.
- **Borrow:** nothing.
- **Do not copy:** all of it; fleet-scale routing and placement.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no single-backend relevance.

#### S14 · Companion docs — "Master Systems Engineering Projects Roadmap", "Master Reading List"
- **Source:** Nick's Google Docs (no local copy on vm-1)
- **Verified:** could not verify — searched `/root/Software/*`, `/root/briefs`; only mentions of the titles exist. (The Project 2 and Project 4 companion docs do have local copies; they are covered by P1–P7.)
- **Key idea:** unknown; not readable.
- **Borrow:** nothing can be stated.
- **Do not copy:** n/a.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — until Nick exports them; they cannot be cited as read.

### Bucket 2 — Nick's own prior projects (repos on vm-1)

Standing rule, confirmed in three repos' own files: each project implements its own code. Every
entry below is an idea, a convention or a ruling — never code to copy. kb was searched first
(the `kb` on PATH cannot open the VM store; the older binary run from `/root/Raw/knowledge` works).

#### P1 · analytics-edge-gateway (Project 2) — GCRA / token-bucket design
- **Source:** `/root/Software/analytics-edge-gateway/PLAN.md` §7.2, §2.2, Chunk 2
- **Verified:** read directly (plan sections). **No implementation exists** — the repo stopped at plan sign-off.
- **Key idea:** GCRA keeps one `int64` per tenant (theoretical arrival time) updated by one compare-and-swap; integer nanoseconds and a rational rate so the bound is checkable exactly. The limiter never reads a clock: `Allow(now, n) (ok, retryAfterNs)`.
- **Borrow:** (fairness, testing) the `now`-as-argument, no-floats contract; idle tenants evictable once TAT ≤ now, so the tenant table is bounded; the test list — exact window bound against a `math/big` oracle, TAT never moves backward, token-bucket-equals-GCRA differential test, zero allocations per call.
- **Do not copy:** the code (there is none; the design is unreviewed). A rate limiter alone does not meet the fairness invariant, which sheds "when backend saturation is detected" — that needs in-flight accounting.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the contract and oracle; proven here for the first time.

#### P2 · analytics-edge-gateway — workload / DST harness conventions
- **Source:** same `PLAN.md` §2, §2.1; probe result in its `agent_work/INDEX.md`
- **Verified:** read directly (plan; probe result as recorded, not re-run)
- **Key idea:** Time is an `int64` argument and randomness an explicit generator; one op-decoder driven three ways (seeded property test, fuzz engine, regression files) so a failure found by one replays in the others; a trivially correct reference model per structure.
- **Borrow:** (testing, benchmarking) named forked random streams so a new draw does not shift the others; the printed `REPLAY: SEED=… go test … -run '^Test…$'` line on every failure; an event loop keyed `(timeNs, seq)` in which **a rejected client schedules its own retry as an event** — which is what makes the retry-storm scenario simulable; generators `Zipf`, `Burst`, `RetryStorm`, `Mix`, reused to drive real processes; the meta-test table (replay identical, golden digests, fork isolation, planted bug caught).
- **Do not copy:** `math/rand` v1 (use `math/rand/v2` PCG); the high-cardinality generator and shrinker sized for that project.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — conventions as ideas; determinism claim scoped to same commit, toolchain, linux/amd64.

#### P3 · analytics-edge-gateway — benchmark-rigor rulings for vm-1
- **Source:** same `PLAN.md` §2.4; kb entry `vm1-no-hardware-perf-counters.md`
- **Verified:** read directly
- **Key idea:** State the machine's limits before claiming anything, commit raw output, narrow a claim that does not come out in your favour.
- **Borrow:** (benchmarking) vm-1 is 4 shared vCPUs with no hardware performance counters; evidence benchmarks run serialised under a machine-wide lock with builders idle; `-count=10` with median and spread from a small in-repo stats script; allocation gates as tests; "the repo ships the benchmark scripts so a reader can re-run them on real hardware".
- **Do not copy:** its cache-line / SWAR material. Note that plan has **no open-loop generator** — nothing reusable for `cmd/loadgen`.
- **Strength:** CORE · **Recommendation:** ADOPT — constraints of the machine, not opinions.

#### P4 · zeromq-go (Project 4) — baton scheduler vs `testing/synctest`
- **Source:** `/root/Software/zeromq-go/PLAN.md` §2.1, §8.1, Appendix A; `agent_work/probes/phase1/`; kb `go-deterministic-goroutine-replay-baton-scheduler.md`
- **Verified:** read directly (plan and kb entry; probe code by declarations; probes not re-run). **Plan and probes only — no product code.**
- **Key idea:** `synctest` virtualises time but does not order goroutines woken at the same instant (4 goroutines gave all 24 orders in 2,000 runs). A baton scheduler that runs one simulated goroutine at a time, chosen by a seeded generator, gave one trace digest across 8 processes.
- **Borrow:** (testing; shapes pool, coalescer, admission) the three-option decision — synctest only / everything single-threaded / baton plus seam — which this project must make, because the spec wants a single-threaded seeded harness *and* tests goroutine leaks and 99-of-100 cancellation. The digest design: fold every decision, delivered byte range and fault into SHA-256.
- **Do not copy:** the 250-line probe. Its costs: no `go`, `select`, channels, `time` or `net` below the seam; ~530,000 scheduler steps/s; it conflicts with idiomatic `context` cancellation, which the coalescer rule is written in.
- **Strength:** CORE (as a decision) · **Recommendation:** ADOPT WITH CHANGE — bring the decision to Nick (question 8); recommended answer is sans-I/O cores, not a baton.

#### P5 · zeromq-go — sans-I/O framing tests and the simulated connection
- **Source:** `PLAN.md` §2.1–2.3, Appendix A P7
- **Verified:** read directly
- **Key idea:** The codec is tested against bytes, never sockets: every golden vector fed whole, one byte at a time and at every split point, with identical output. The simulated connection is two independent directions with adversarial chunking, bounded buffers, latency and faults.
- **Borrow:** (protocol, testing — spec Commit 1 and §4.1) chunk-invariance as a table test and a native fuzz target; rejection vectors that return typed errors and never panic; "the decoder never allocates ahead of the bytes it has received" (a RESP bulk length of 2³¹ costs nothing until bytes arrive); the fault vocabulary — FIN, RST, half-open silent drop, stall-then-resume, per-direction latency, chunk profiles.
- **Do not copy:** ZMTP specifics; the libzmq interop harness (here interop is free with `redis-cli`).
- **Strength:** CORE · **Recommendation:** ADOPT — the closest match in this bucket to spec Commit 1.

#### P6 · zeromq-go — high-water-mark bounds and their tests
- **Source:** `PLAN.md` §2.5, §8.2, §8.3
- **Verified:** read directly
- **Key idea:** A bound that is asserted must name its "transit term" — the items in hand-off between stages — or an adversarial tester will break it. A stalled consumer is shown isolated by comparing healthy peers' virtual delivery times with a control run.
- **Borrow:** (admission, pool, testing) state every bound with its transit term; assert "healthy tenant unaffected" exactly in virtual time against a control run without the noisy tenant, instead of with a tolerance; zero never means unlimited; a pre-declared fallback (if the clever variant fails its one repair round, land the simple one and narrow the claim); the goroutine-leak oracle (simulator registry empty *and* `runtime.NumGoroutine()` at baseline).
- **Do not copy:** block-forever at the high-water mark — this spec sheds with a distinct error instead.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the bound discipline and control-run comparison.

#### P7 · zeromq-go and analytics-edge-gateway — per-chunk gate design
- **Source:** zeromq-go `PLAN.md` §2.13–2.14, §7; analytics-edge-gateway `PLAN.md` §2.9, §6
- **Verified:** read directly
- **Key idea:** Ordered landing gates, each gate script proven by a canary that fails on a planted violation; adversarial testing after review, so its findings are about claims.
- **Borrow:** (process) gate order — hygiene and determinism lint → unit and pinned-seed sims → property → short fuzz → race and real-mode stress → seed sweep and replay meta-test → adversarial → review → prose → acceptance; a broken claim is either repaired or narrowed in writing; plain testing vocabulary in briefs to Astra (an earlier project lost runs to a provider filter over attack wording); done-marker lines because a cut-off can exit 0 silently.
- **Do not copy:** the budgets as written — the two plans disagree and neither was run. Use the object store's measured budgets.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the gate list; numbers from P10.

#### P8 · object-store — simulator kernel (`internal/env`, `internal/sim`, `simtest`)
- **Source:** `/root/Software/object-store/internal/{env,sim}`; `docs/knowledge/chunk-0b-simulator.md`, `sim-kernel-extraction-note.md`; `/root/briefs/shared-sim-investigation.md`, `shared-sim-ruling-note.md`
- **Verified:** read directly (knowledge entries, investigation, ruling note; code by listing and declarations)
- **Key idea:** A single-threaded discrete-event simulator owns the world; protocol code is a pure core with no goroutines, locks or wall clock, enforced by a lint and cross-process replay tests. It is the only simulator among Nick's projects that was built *and* attacked.
- **Borrow:** (testing) stream derivation from `(seed, label)` on `math/rand/v2` PCG; seed variables `-seed`, `DST_SEED`, `DST_SEEDS`; `RequireDeterministic` (run twice, compare digests) and `Sweep`; determinism rules — no float arithmetic in a decision (use parts-per-million), no map iteration reaching an output; the freeze rule (after the simulator lands, its API changes only by change request).
- **Do not copy:** the code (its own extraction note lists the tangles); disk, node incarnations, clock skew. Its "no goroutines, no mutex" rule cannot hold for a shipped connection pool.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — same conventions, re-implemented in-repo.
- **Ruling that carries over:** `/root/briefs/shared-sim-ruling-note.md` (Nick, 2026-11-21): a shared kernel "if feasible ... its own repo that going forward projects will import and extend". No such repo exists today; the object store recorded "later, not now". Open — question 8.

#### P9 · object-store — "incremental e2e per chunk" rule
- **Source:** `/root/briefs/objstore-guide-e2e-wording-ask-2026-11-23.md`, `/root/briefs/flow-guide-e2e-wording-draft.md`, `docs/knowledge/chunk-07-e2e.md`
- **Verified:** read directly. The wording draft is marked DRAFT for Nick's approval; **no file records that approval**, though this mission lists the rule as standing.
- **Key idea:** "The final chunk is never the first time the system runs end to end." Every scenario has an owning chunk; the harness lands with the first runnable system.
- **Borrow:** (testing, chunk plan) a scenario-to-chunk table — reconnection storm with the pool chunk, hot-key stampede with the coalescer chunk, noisy neighbor with fairness, retry storm with errors/budget; the harness (proxy against the virtual backend and against a real Redis container) lands with basic forwarding; each scenario ships a planted-fault canary and proves it exercised its subject. "An operation that times out or loses its connection has an unknown result, not a failed one" — the same distinction as pre- vs post-forward errors.
- **Do not copy:** nothing structural. Cost: middle chunks get larger.
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
- **Key idea:** A request runs only when its database, its tenant and the whole process are all under their in-flight caps; otherwise it queues if all queues have room; otherwise it is shed and nothing is taken. The decision state has "no locks, no clock, no channels"; a thin wrapper adds them.
- **Borrow:** (fairness, admission) the two-layer split — pure decision core plus concurrent wrapper — which is the sans-I/O answer for the admission queue; the written invariants (per-tenant in-flight ≤ cap, global = sum, work-conserving, all-zero states deleted so memory is bounded by active tenants); the reason for a global cap ("without the VM cap the process's in-flight count would be tenants × TenantInFlight"). Here the levels collapse to tenant ≤ global.
- **Do not copy:** the code; fixed caps as the whole policy — mansio's own note says they are "unrelated to the deadline". A flooding tenant under a static cap still takes slots; that is isolation, not fair share.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — the core/wrapper shape and invariants, with fair-share shedding added.

#### P12 · mansio — shed at admission what would expire in the queue
- **Source:** `server/knowledge/backpressure/2026-11-10-admission-would-expire.md`
- **Verified:** read directly (knowledge entry; code signatures)
- **Key idea:** When a level is at its cap, estimate the new request's finish time from queue position and measured completion rate (Little's law); if that is past its deadline, refuse now with a retry hint.
- **Borrow:** (benchmarking) the before/after table format — measured there: 52 served / 203 expired / deepest queue 61 before; 55 served / 0 expired / 203 shed / deepest queue 9 after.
- **Do not copy:** the mechanism in v1. It needs client deadlines, which RESP does not carry, and it overlaps CoDel's job.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — a known alternative to CoDel; promote only as a comparison arm (question 10).

#### P13 · mansio — one generated retry contract, and a token-bucket retry budget
- **Source:** `server/knowledge/backpressure/2026-11-10-retry-contract.md`; `docs/retry-contract.md`; `server/internal/retry/retry.go`
- **Verified:** read directly (knowledge entry in full; parts of the contract and `retry.go`)
- **Key idea:** Every answer the server can give is one row in a table generated from code: code, retry hint, what the client does, whether it is charged to the budget, and **whether a mutation may have applied**. Clients run the table as test vectors.
- **Borrow:** (errors and retries) the table as this project's pre-forward vs post-forward contract: `shed` — "nothing ran", retry safe — versus `expired` — "a write may have committed", final. The budget: each first attempt deposits a ratio, each retry takes one token, the bucket is capped, so credit expires and a quiet hour cannot bank retries for a storm.
- **Do not copy:** float balance with a tolerance (use integers in a simulated decision path); HTTP status codes — RESP needs its own error strings. Recorded there: charging the server's own pacing signals to the budget made a gated test fail.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — a generated contract table held by tests; integer budget.

#### P14 · mansio — budget callers below the pool; refuse before reading; capacity table
- **Source:** `server/knowledge/backpressure/2026-11-10-controld-pool-budget.md`, `…-edge-admission.md`, `…-capacity-table.md`; `server/internal/slots/slots.go`
- **Verified:** read directly (the rule and measurement sections of each entry; code signatures)
- **Key idea:** Callers must be budgeted *below* the backend pool or the excess waits invisibly inside the pool and times out: "256 resolve slots + 16 admin slots sat in front of a 20-connection pool".
- **Borrow:** (pool, admission, observability) a config-parse-time check that gates sum to less than the pool; "callers that join a load already in flight take no slot" — coalesced waiters consume no admission or pool slot; hold nothing while a slow client sends; one counted slot type instead of ad-hoc channel semaphores; a capacity document **generated from defaults by a test**, one row per queue: what it protects, capacity and config key, full-queue policy, deadline, metric.
- **Do not copy:** pgxpool, Prometheus rule files, HTTP admin levers.
- **Strength:** CORE · **Recommendation:** ADOPT — "bounded everything" as a generated, test-checked table is the cheapest strong proof available.

#### P15 · mansio — clock-driven `context` deadlines
- **Source:** `server/internal/det/clock.go` (`WithTimeout(ctx, clock, d)`, `WithDeadlineCause`)
- **Verified:** read directly (signatures); mansio's own simulator is secondary only
- **Key idea:** A small injectable clock seam whose context deadlines fire on the injected clock.
- **Borrow:** (testing) lets coalescer and pool code keep idiomatic `context` cancellation while deadlines run on virtual time — the piece the baton plan lacks.
- **Do not copy:** its `time.Time`-based clock (the other projects ruled `int64` nanoseconds); its simulator.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the clock-driven context idea only.

#### P16 · sqlite-internals / walfleet
- **Source:** kb `sqlite-internals-ch10a-findings.md`, `…ch10b…`; `/root/Software/walfleet` (`sqlite-internals-work` is an empty directory)
- **Verified:** read directly (first corrections of each kb entry; `STATE.md` head)
- **Key idea:** Single-writer fencing and relocation for per-database SQLite files on object storage — ownership of a file, not multiplexing clients onto a server.
- **Borrow:** nothing for pooling or WAL. Two habits only: count file descriptors per unit of capacity (matters for the 500-client and 1,000-connect tests against `ulimit -n`); measure cancellation latency instead of assuming it.
- **Do not copy:** fencing, epochs, WAL shipping.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — it does not bear on a storage proxy.

#### P17 · dst-research — technique ladder
- **Source:** `/root/Software/dst-research/findings/_technique-ladder.md`, `_taxonomy.md`
- **Verified:** read directly (ladder tiers A–E); TigerBeetle through it is secondary only
- **Key idea:** 45 deterministic-simulation techniques from 21 codebases, ordered simplest to hardest.
- **Borrow:** (testing) a Commit 0 checklist by rung: seeded generator passed explicitly, sans-I/O state machine, seed replay, `(time, seq)` queue, deadlock = no ready task and no timer, run-twice fingerprint, named streams, golden hashes, testing the tester with planted bugs and **coverage marks that must be hit** (the answer to "checks passed over empty sets"), a fault budget, a fault-free control run.
- **Do not copy:** storage-fault tiers, linearizability checking, consensus liveness modes.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — as the Commit 0 checklist.

#### P18 · systems-trinkets — local service labs
- **Source:** `/root/Software/systems-trinkets` (`software/` compose files for redis, valkey, postgres, pgbouncer, toxiproxy; lessons `pipelining-work`, `cache-aside`)
- **Verified:** read directly for listings only; lesson code not read
- **Key idea:** Ready local containers and Nick's own measured pipelining lesson.
- **Borrow:** (benchmarking) the compose files for a real-backend leg; possibly Nick's pipelining measurements.
- **Do not copy:** k6 as the load generator — it is closed-loop per virtual user by default.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — convenience, not design input.

#### P19 · *Backpressure in Microservices* (Luca Sepe), as applied to mansio
- **Source:** `/root/Software/book-notes-work/backpressure-plan/books/backpressure-in-microservices/APPLICATION-PLAN.md`
- **Verified:** read directly for the opening and the eleven finding titles only; the book itself was not read
- **Key idea:** The book's default control — reject new + bounded queue + bounded workers + deadline + metrics — audited against mansio, producing the findings that P11–P14 implement.
- **Borrow:** (admission, observability) the finding list as an audit checklist for our own pipeline: boundaries that cannot be seen, slow clients holding slots, reading before admission, callers over-admitted onto a pool, things that grow outside any queue, no capacity table.
- **Do not copy:** n/a — it reaches this project through P11–P14.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — use as a review checklist at each landing.

#### P20 · Examined and excluded (one entry)
- **Source:** `/root/Software/distributed-transactional-kv`, `let-it-race-analysis`, `durable-actor`, `mini-durable-objects`, `research/graphduck`, `tigerbeetle` (clone)
- **Verified:** read directly for existence, `STATE.md` / `AGENTS.md` heads and listings
- **Key idea:** Respectively: an empty repo with no commits; a book analysis on observability vs control; an early-stage simulation course; a course at lesson 1; graph modelling chapters; an upstream clone not read here.
- **Borrow:** nothing. `durable-actor` is strictly less than P8; `let-it-race`'s one link (per-tenant metrics as the control signal) is already in the spec.
- **Do not copy:** n/a.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no content bearing on a storage proxy.

### Bucket 3 — Industry systems the spec does not name

GitHub sources were read at the default-branch head on 2026-11-24. Defaults quoted are from the
files read.

#### I1 · PgBouncer
- **Source:** https://github.com/pgbouncer/pgbouncer (`doc/config.md`, `doc/usage.md`, `src/objects.c`); pgbouncer.org features page
- **Verified:** read directly
- **Key idea:** A single-threaded event loop lends a server connection to a client for a session, a transaction or a statement. Transaction mode gives the fan-in win and breaks session state by design. Clients beyond the pool size wait in a queue.
- **Borrow:** (pool) the limit taxonomy — client cap, pool size, reserve pool, server lifetime, idle timeout, connect timeout, check-before-reuse after idle. (pool, creation-rate cap) `launch_new_connection` allows "only a single connection attempt at a time" per pool, and after a failed login refuses attempts for `server_login_retry` (15 s) while new clients get an error immediately instead of queueing. (observability) the `SHOW POOLS` vocabulary: active, waiting, logging-in, max wait. (Variant B) the transaction-mode break list and the prepared-statement rewrite.
- **Do not copy:** the 120 s `query_wait_timeout` and unbounded FIFO queue — exactly the standing queue CoDel exists to remove. One connect at a time is too slow a ramp for 20–32 connections.
- **Strength:** SECONDARY (CORE if Variant B) · **Recommendation:** ADOPT WITH CHANGE — limit taxonomy, bounded concurrent connects, fail-fast lockout; replace the wait queue.

#### I2 · PgCat
- **Source:** https://github.com/postgresml/pgcat (`README.md`, `CONFIG.md`)
- **Verified:** read directly
- **Key idea:** PgBouncer's pooling on a multi-threaded runtime, plus replica load balancing, a ban list and sharding.
- **Borrow:** (pool) a short default wait for a connection (1,000 ms against PgBouncer's 120 s); health check before handing out a long-idle connection.
- **Do not copy:** sharding, replica routing, mirroring. With one backend a ban list is a one-host circuit breaker.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — its two useful ideas are also in I1.

#### I3 · Odyssey (Yandex)
- **Source:** https://github.com/yandex/odyssey (`README.md`, reference `odyssey.conf`)
- **Verified:** read directly
- **Key idea:** A multi-worker pooler that cleans up a server connection after an unclean client exit instead of discarding it, and caps clients in the handshake phase separately from total clients.
- **Borrow:** (pool) the dirty-connection decision table — what to do with a backend connection whose client vanished mid-request: cancel or close, never hand it out mid-reply. (admission) `client_max_routing`: a separate bound on clients in accept/handshake, which maps to the reconnection-storm test on the client side.
- **Do not copy:** worker threading model, per-route config language, auth passthrough.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the dirty-connection table and the in-handshake cap.

#### I4 · Supavisor (Supabase)
- **Source:** https://github.com/supabase/supavisor (`README.md`)
- **Verified:** read directly
- **Key idea:** A clustered multi-tenant pooler where a tenant's pool lives on one node and other nodes proxy to it.
- **Borrow:** nothing mechanical. Note for the README: "tenant" there means a separate database, not tenants sharing one backend.
- **Do not copy:** cluster membership, dynamic pool placement.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — solves a multi-node problem we do not have.

#### I5 · Envoy — overload manager
- **Source:** Envoy docs, `overload_manager.rst`
- **Verified:** read directly
- **Key idea:** Resource monitors report pressure in [0,1]; triggers map pressure to actions; named "load shed points" are checked at fixed places in the request path, cheapest rejection first.
- **Borrow:** (admission, errors, observability) named shed points in pipeline order — refuse at accept, refuse after parsing, refuse to open a new upstream connection — as the structure for pre-forward rejection codes, with one counter per shed point.
- **Do not copy:** the heap/CPU monitors and the generic monitor-action framework; our pressure signals are queue wait and in-flight count.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the shed-point list, not the framework.

#### I6 · Envoy — adaptive concurrency filter
- **Source:** Envoy docs `adaptive_concurrency_filter.rst` and its proto
- **Verified:** read directly
- **Key idea:** Periodically pin concurrency very low (default 3) to measure ideal latency, then scale the limit by `(minRTT + buffer) / sampleRTT` plus a square-root headroom.
- **Borrow:** nothing beyond what I11 gives. The formula is Netflix's gradient.
- **Do not copy:** the minimum-RTT measurement window. Envoy's docs admit "a noticeable increase in request 503s" during it and tell you to retry on another host; there is no other host here.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — a cautionary example for single-backend adaptive limits.

#### I7 · Envoy — circuit breakers and retry budget
- **Source:** Envoy docs `circuit_breaking.rst`, `circuit_breaker.proto`
- **Verified:** read directly
- **Key idea:** Four independent static caps per upstream — connections, pending requests, active requests, retries — each with its own overflow counter, so an operator can tell which bound was hit.
- **Borrow:** (pool, admission, observability) "one named counter per bound". (retries) the concurrency-style budget: active retries ≤ a percentage of active + pending requests, with a small floor.
- **Do not copy:** the defaults (1024 is "off" for a 20–32 connection pool); Envoy's own caveat that connections can exceed `max_connections`.
- **Strength:** CORE · **Recommendation:** ADOPT — the per-bound counter rule.

#### I8 · Envoy — admission control filter
- **Source:** Envoy docs `admission_control_filter.rst` and its proto
- **Verified:** read directly
- **Key idea:** Probabilistic rejection driven by recent success rate (the SRE-book client-throttling formula) with an aggression knob.
- **Borrow:** (benchmarking) a candidate "well-behaved client" model for the load generator.
- **Do not copy:** as the proxy's admission gate. It reacts to failures, so it engages only after the backend is already erroring; queue delay fires earlier. It also puts a random draw in the decision path.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — load-generator side at most.

#### I9 · Envoy — outlier detection
- **Source:** Envoy docs `outlier.rst`, `outlier_detection.proto`
- **Verified:** read directly (proto defaults; doc skimmed)
- **Key idea:** Eject a host that fails repeatedly or is a statistical outlier against its peers.
- **Borrow:** nothing.
- **Do not copy:** all of it — there is no peer group with one backend, and ejecting the only host is an outage.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — Variant C only.

#### I10 · Envoy — Redis proxy filter
- **Source:** Envoy docs `other_protocols/redis.rst`, `redis_proxy.proto`, `supported_commands.h` and two C++ sources
- **Verified:** read directly (docs, proto and header; the two C++ files by keyword)
- **Key idea:** A RESP codec plus an allow-list. Every command must be routable or answered locally; anything else gets an error. Commands from many clients are batched onto one upstream write.
- **Borrow:** (protocol) allow-list classification with separate sets for simple, multi-key, eval, transaction and write commands; `PING`, `ECHO`, `QUIT`, `HELLO`, `AUTH`, `CLIENT SETNAME` answered by the proxy itself, backend `AUTH` issued by the proxy on connect; one RESP version pinned per listener, never translated; unsolicited RESP3 Push frames dropped because "forwarding one would desynchronize the reply FIFO". (batching) flush on buffer size or a timer. (errors) fixed local error strings distinct from pass-through Redis errors.
- **Do not copy:** the 3 ms flush timer (3–15× the spec's window); cluster topology, MOVED/ASK, key-hash splitting.
- **Strength:** CORE (Variant A) · **Recommendation:** ADOPT — allow-list, proxy-answered connection commands, local error strings.

#### I11 · Netflix concurrency-limits — AIMD, Vegas, Gradient, Gradient2
- **Source:** https://github.com/Netflix/concurrency-limits (`limit/{AIMDLimit,VegasLimit,GradientLimit,Gradient2Limit,WindowedLimit}.java`)
- **Verified:** read directly (all five files and the README)
- **Key idea:** Treat in-flight requests like a TCP congestion window. Each completed request reports latency, in-flight count and whether it was dropped; a small pure function returns the new limit.
- **Borrow:** (concurrency limit) the signature `update(rtt, inflight, didDrop) → newLimit` — trivially deterministic. AIMD: on drop or `rtt > timeout`, `limit = limit × 0.9`; else if `inflight × 2 ≥ limit`, `limit + 1`; clamp. Gradient2 as the documented upgrade: compare the current sample against a long exponential average (600 samples), `gradient = clamp(1.5 × long/short, 0.5, 1.0)`, `new = limit × gradient + queueSize`, smoothed. The guard "only grow when in-flight ≥ limit/2" appears in all four and is essential — without it the limit drifts to maximum while idle.
- **Do not copy:** minimum-RTT baselines (Vegas, Gradient v1): one fast `PING` sets a baseline that makes every normal request look congested. The defaults (minimum 20, timeout 5 s) are sized for a service, not a 20-connection pool. Floats in the decision path.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — AIMD first, Gradient2 as a labelled stretch, in fixed-point.

#### I12 · Netflix concurrency-limits — partitioned limiter
- **Source:** same repo, `limiter/AbstractPartitionedLimiter.java`
- **Verified:** read directly
- **Key idea:** Split the global limit into named partitions by percentage. A partition's share is a *guarantee, not a cap*: it is enforced only once the global limit is reached, so idle capacity is borrowed freely.
- **Borrow:** (fairness) the decision rule: if global in-flight ≥ global limit, admit only if the tenant's in-flight is under its share; otherwise admit. This is the work-conserving formulation of "shed preferentially from tenants exceeding their fair share".
- **Do not copy:** static percentages per named partition (our tenant set is open — compute share from active tenants or weights). Because shares round up, in-flight can overshoot the global limit; the pool cap must stay a separate hard bound. The reject-delay sleep.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — share-under-saturation with dynamic shares.

#### I13 · Netflix Tech Blog — "Performance Under Load"
- **Source:** https://netflixtechblog.medium.com/performance-under-load-3e6fa9a60581
- **Verified:** read directly (via the local renderer)
- **Key idea:** A fixed concurrency limit goes stale; infer it from latency the way TCP infers a congestion window.
- **Borrow:** (docs) the rationale and the Little's-law framing: limit = concurrency + the queue allowed before latency degrades.
- **Do not copy:** its reassurance that a retry "is nearly 100% successful at reaching an instance with available capacity" — that assumes many instances. With one backend a shed request has nowhere else to go.
- **Strength:** BACKGROUND · **Recommendation:** ADOPT — as cited rationale; I11 is the mechanism.

#### I14 · Finagle `RetryBudget` (and Linkerd's retry budget)
- **Source:** `finagle-core/.../service/RetryBudget.scala`; Linkerd `reference/retries.md`
- **Verified:** read directly (Finagle source; Linkerd field names — its default values were not on the pages read)
- **Key idea:** Every original request deposits credit, every retry withdraws a larger amount, credit expires after a TTL, and a small time-based reserve lets low-traffic clients retry at all.
- **Borrow:** (retries) the integer mechanics: deposit 1,000 per request; withdraw `1,000 / ratio` per retry (10,000 at 10%); a withdrawal either takes the full amount or leaves the balance untouched. Also the `NonRetryable` flag carried on a failure — the pre- vs post-forward distinction inside the error.
- **Do not copy:** the defaults: 20% and a floor of 10 retries/s — at 50 QPS the floor alone is the whole budget. Immediate retry with no backoff.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — this bucket at 10% with a small floor, per tenant.

#### I15 · Finagle / Linkerd — failure accrual and peak-EWMA balancing
- **Source:** Finagle `PeakEwma.scala`, `FailureAccrualFactory.scala`, `FailureAccrualPolicy.scala`; Linkerd load-balancing and circuit-breaking docs
- **Verified:** read directly (those files); power-of-two selection and deadline propagation are from memory
- **Key idea:** Mark a backend dead after repeated failures and let one probe through after a backoff; separately, pick the cheaper of two random backends by a latency average that jumps on a slow response and decays slowly.
- **Borrow:** (errors, pool) failure accrual as the single-backend "down" state machine: N consecutive failures → dead for a jittered exponential backoff → one probe → success revives. It pairs with I1's connect lockout.
- **Do not copy:** balancing across hosts (there is one); the 5 s minimum dead time (scale to the test timescale).
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the probe state machine only.

#### I16 · Google SRE book — "Handling Overload" (ch. 21)
- **Source:** https://sre.google/sre-book/handling-overload/
- **Verified:** read directly
- **Key idea:** Rejecting is not free, so clients throttle themselves from their own accept ratio; requests carry a criticality; retries are capped per request and per client.
- **Borrow:** (retries) **the source of the spec's 10%**: "a per-request retry budget of up to three attempts" and a per-client budget where "a request will only be retried as long as this ratio is below 10%" — worst-case growth just under 3× with the per-request cap alone, 1.1× with the ratio added. (benchmarking) client-side adaptive throttling, `max(0, (requests − K·accepts)/(requests + 1))` with K = 2, as the load generator's well-behaved-client profile. (errors) an "overloaded; don't retry" reply distinct from a plain failure.
- **Do not copy:** the two-minute window (our scenarios last seconds); criticality propagation (RESP clients cannot send it).
- **Strength:** CORE · **Recommendation:** ADOPT — cite for the 10%; implement via I14's bucket.

#### I17 · Google SRE book — "Addressing Cascading Failures" (ch. 22)
- **Source:** https://sre.google/sre-book/addressing-cascading-failures/
- **Verified:** read directly
- **Key idea:** Overload feeds itself — queues add latency, latency causes timeouts, timeouts cause retries. The fixes are short queues, early rejection, deadline propagation and bounded jittered retries.
- **Borrow:** (admission) queue sizing: keep the queue small relative to the worker pool, "e.g., 50% or less" — the starting point for `MaxQueueDepth`. (testing) non-linear recovery: a service that tipped at 11,000 QPS will not recover at 9,000 — assert how long goodput takes to return after overload ends.
- **Do not copy:** the capacity-planning and restart-orchestration half.
- **Strength:** CORE · **Recommendation:** ADOPT — queue-depth default and the recovery assertion.

#### I18 · Twemproxy (nutcracker)
- **Source:** https://github.com/twitter/twemproxy (`README.md`, `notes/redis.md`, `notes/recommendation.md`)
- **Verified:** read directly
- **Key idea:** "Built primarily to reduce the number of connections to the caching servers." Many client connections are multiplexed onto very few server connections by pipelining; anything needing connection state is simply unsupported.
- **Borrow:** (pool) proof that tiny pools work: `server_connections` defaults to 1. (protocol) its support table as the most conservative command classification — no `MULTI`/`EXEC`/`WATCH`, blocking pops, `SUBSCRIBE`, `SELECT`, client `AUTH`, `MONITOR`. (protocol) connection state belongs to the proxy: auth and database index are pool config.
- **Do not copy:** ejecting the only server; the infinite default timeout; rejecting `PUBLISH` (stateless, safe to share).
- **Strength:** CORE (Variant A) · **Recommendation:** ADOPT — the baseline deny-list and the proxy-owned-state precedent.

#### I19 · McRouter (Meta)
- **Source:** https://github.com/facebook/mcrouter (`README.md`, wiki Features and Home)
- **Verified:** read directly
- **Key idea:** A memcached router where each proxy thread holds exactly one persistent connection per destination; most of its value is routing policy.
- **Borrow:** a second precedent for lazy, tiny pools and probe-based recovery; the description of a proxy as "a series of queues", each with its own limit.
- **Do not copy:** route handles, failover pools, and especially the reliable delete stream — replying success and replaying later is the opposite of honest post-forward error semantics.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — nothing unique to take.

#### I20 · Redis protocol rules — pipelining, RESP2 vs RESP3, client tracking
- **Source:** Redis docs (`pipelining.md`, `protocol-spec.md`, `client-side-caching.md`); `redis-specifications` `RESP3.md`
- **Verified:** read directly (relevant sections). A few per-command semantics in the research note's table are marked from memory there.
- **Key idea:** A Redis connection is a strict FIFO — the Nth reply answers the Nth command. That is what makes multiplexing possible with nothing but a queue of waiters per backend connection. Anything that breaks the FIFO or leaves state on the connection cannot share one.
- **Borrow:** (protocol, pool, batching) the classification table for `internal/protocol` (full table in `agent_work/phase0/research/b3-industry-systems.md`, entry 20): plain reads — forward, coalesce, batch; plain writes — forward, batch, never coalesce; transactions, blocking commands, subscribe, `MONITOR`, client tracking — reject; `SELECT`/`AUTH`/`HELLO` — answered or owned by the proxy; scans — forward, never coalesce. Pipelining's gain is mostly fewer `read`/`write` syscalls; the server buffers replies, so cap pipeline depth per connection. Per-connection invariant: the i-th reply goes to the i-th pending waiter.
- **Do not copy:** RESP3 in v1 — it forces a decision on Push frames and doubles the reply shapes a coalesced read must hand out.
- **Strength:** CORE (Variant A) · **Recommendation:** ADOPT — allow-list by default, unknown commands rejected.

#### I21 · Predixy
- **Source:** https://github.com/joyieldInc/predixy (`README.md`, `src/Command.h`, `src/Command.cpp`)
- **Verified:** read directly
- **Key idea:** A Redis proxy that supports the stateful commands the others refuse by flagging them `Private` and giving that client a dedicated backend connection.
- **Borrow:** (protocol) the flag-per-command vocabulary (read, write, admin, private, no-key, multi-key). Its `Private` set — `watch multi blpop brpop brpoplpush psubscribe subscribe` — is the definitive short list of "pin or reject".
- **Do not copy:** private connections in v1. A pinned connection is the "session-level pinning" the spec says makes fan-in disappear, and blocking commands could pin the whole pool.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the flag vocabulary; reject the private set (question 9).

#### I22 · HAProxy — server queueing
- **Source:** HAProxy `doc/configuration.txt` (`maxconn`, `maxqueue`, `slowstart`, `timeout queue`, priority class/offset)
- **Verified:** read directly (the named keyword sections)
- **Key idea:** A per-server concurrency cap with a queue in front; the manual calls `maxconn` "very important as it can save fragile servers from going down under extreme loads".
- **Borrow:** (concurrency limit, pool) `slowstart`: after a server recovers, its limit grows linearly to 100% over a configured time — a deterministic alternative to letting AIMD rediscover the limit after a backend restart. (fairness) a priority *offset* in milliseconds that shifts a request's effective arrival time: a bounded head start without strict priority.
- **Do not copy:** its unsafe defaults (unlimited `maxconn` and `maxqueue`); redispatch to another server.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — slow-start after backend recovery.

#### I23 · nginx — upstream `queue` / `max_conns`
- **Source:** https://nginx.org/en/docs/http/ngx_http_upstream_module.html
- **Verified:** read directly
- **Key idea:** The minimal bounded wait queue: a maximum length and a maximum wait; reject on either.
- **Borrow:** confirmation that (depth, wait) is the minimum queue interface, and precedent for reporting queue time separately from backend time.
- **Do not copy:** the 60 s default; `max_conns` is another soft cap ("may exceed").
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — confirms an interface, adds no mechanism.

#### I24 · Go `golang.org/x/sync/singleflight`
- **Source:** https://github.com/golang/sync/blob/master/singleflight/singleflight.go
- **Verified:** read directly (whole file)
- **Key idea:** A mutex-guarded map of in-flight calls. The first caller for a key runs the function; later callers wait for its result; the entry is removed when the function returns.
- **Borrow:** (coalescer) the skeleton: one lock, one map, entry removed on completion only if it is still the same call, a `Shared` flag (our coalesced-hits metric), the leader/follower split.
- **Do not copy:** its seven pitfalls, each confirmed in the source — (1) no context: the leader's cancellation fails every waiter; (2) errors fan out to all waiters; (3) a hung leader blocks everyone with no way to leave; (4) panics fan out and can take the process down; (5) `Forget` breaks the one-in-flight guarantee; (6) all waiters alias one value; (7) the waiter set is unbounded.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — the shape, with all seven fixed; written from scratch regardless.

#### I25 · Singleflight variants — Tailscale, CockroachDB, groupcache
- **Source:** `tailscale/util/singleflight`, `cockroach/pkg/util/syncutil/singleflight`, `groupcache/singleflight`
- **Verified:** read directly (Tailscale's `DoChanContext`; CockroachDB's doc comments and options; groupcache whole file)
- **Key idea:** Two forks add the missing cancellation isolation. Tailscale runs the call on a context "not derived from any of the input contexts", counts waiters, and cancels the call only when the last waiter leaves. CockroachDB makes leader-cancellation inheritance an explicit option, off by default.
- **Borrow:** (coalescer) Tailscale's refcount-and-detach rule is the reference algorithm for "cancel 99 of 100": detached call context, per-waiter exit on its own context, cancel upstream at zero waiters, and a caller whose context is done gets its own error even if a result arrived at the same moment. From CockroachDB: a `NumCalls(key)` test hook and joining a flight while holding the lock that guarded the check.
- **Do not copy:** Tailscale's extra goroutine per waiter (1,000 waiters = 1,000 goroutines and non-deterministic scheduling; each client connection already has a goroutine that can wait). A hung backend still needs its own deadline.
- **Strength:** CORE · **Recommendation:** ADOPT — the semantics the spec asks for, already written down.

#### I26 · Vitess vttablet — query consolidator
- **Source:** `vitessio/vitess` `go/sync2/consolidator.go`, `query_executor.go`, `txserializer/tx_serializer.go`
- **Verified:** read directly (consolidator outline, two functions, header comment)
- **Key idea:** Production coalescing inside a database proxy: identical `SELECT`s arriving while one is running wait for it and share its result.
- **Borrow:** (coalescer) the **waiter cap** x/sync lacks — past the cap a duplicate is rejected with resource-exhausted (or, by config, runs independently); the mode switch `enable | disable | notOnPrimary`, i.e. coalescing is off where read-your-writes matters; a small counter of how often each key was consolidated, for the hot-key metric.
- **Do not copy:** waiters blocking with no context and inheriting the leader's error. The run-independently fallback quietly breaks "at most 1 in-flight call per key".
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — waiter cap (reject on overflow) and a per-command-class coalescing switch.

#### I27 · AWS RDS Proxy pinning (with ProxySQL multiplexing)
- **Source:** AWS docs `rds-proxy-pinning.html`; ProxySQL multiplexing docs
- **Verified:** read directly
- **Key idea:** Instead of forbidding session state, detect it and pin the client to one backend connection. Correct, but "reduces the effectiveness of connection reuse".
- **Borrow:** (Variant B only) the trigger list as a reject list; a "currently pinned connections" gauge if pinning is ever added.
- **Do not copy:** pinning in v1 — the spec lists session pinning as the way pooling fails.
- **Strength:** SECONDARY (Variant B) · **Recommendation:** WATCH ONLY — becomes ADOPT WITH CHANGE if Variant B is chosen.

#### I28 · Uber — Cinnamon load shedder (and QALM through it)
- **Source:** https://www.uber.com/blog/cinnamon-using-century-old-tech-to-build-a-mean-load-shedder/
- **Verified:** read directly (part 1 of 3; parts 2–3 not read). QALM: secondary only.
- **Key idea:** A Go load-shedding middleware: a controller picks what fraction to reject so the queue drains, an auto-tuner sets the in-flight limit, and requests carry a priority.
- **Borrow:** (admission) the pipeline order — tag, rejector, queue, scheduler under an in-flight limit, queue timeout; overload detection by "the last time the request queue was empty", crediting the same Facebook article; a Go-specific warning that channels as the shedding mechanism are expensive synchronisation points. Per this post, QALM is CoDel plus a *manually configured* concurrency limit — the spec's design — and the manual limit was its adoption problem, which argues for the adaptive limit. (benchmarking) report goodput by priority tier.
- **Do not copy:** the PID controller (tuning three gains is a project of its own); cross-service priority propagation.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — pipeline order and the QALM lesson; skip PID.

#### I29 · Amazon Builders' Library — load shedding, retries, fairness, shuffle sharding
- **Source:** four articles at aws.amazon.com/builders-library
- **Verified:** read directly, partially (keyword-matched passages, not end to end)
- **Key idea:** Measure goodput, not throughput; make rejection cheap; bound retries locally; treat fairness as layered admission control.
- **Borrow:** (benchmarking) goodput as a headline metric, and load-testing "far beyond the point where goodput plateaus" to check that rejection stays cheap. (retries) a local retry token bucket in preference to a circuit breaker, because breakers "introduce modal behavior ... difficult to test". (observability) on every shed, record which tenant and which operation. (fairness) a composed pair of token buckets — low rate/high burst with high rate/low burst.
- **Do not copy:** shuffle sharding — one backend offers nothing to shuffle.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — goodput as a headline, cheap-rejection test, shed attribution.

#### I30 · Kubernetes API Priority and Fairness
- **Source:** Kubernetes flow-control docs; KEP-1040
- **Verified:** read directly, partially (queuing/seats sections; the KEP's shuffle-sharding section)
- **Key idea:** A concurrency limit in "seats" fronted by a fixed number of queues; each flow is hashed to a small hand of queues and joins the shortest; unused concurrency is lent between levels.
- **Borrow:** (fairness) request "width" — an expensive request occupies more than one seat — as the way to charge an `MGET` of 100 keys. Shuffle-sharded queues as the documented scale-out path if the tenant set ever becomes unbounded.
- **Do not copy:** the machinery. For a handful of tenants, one queue per tenant with round-robin is simpler and exactly fair.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — keep the width idea in mind; nothing to build now.

#### I31 · Stripe — "Scaling your API with rate limiters"
- **Source:** https://stripe.com/blog/rate-limiters
- **Verified:** read directly
- **Key idea:** Four limiters in production: per-user rate, per-user concurrency, a fleet-wide reservation for critical traffic, and a utilisation-based shedder.
- **Borrow:** (fairness) run a per-tenant *concurrency* limiter beside the per-tenant *rate* limiter — the rate limit misses the case where the backend slows down.
- **Do not copy:** central Redis-backed counters. Correction to a common assumption: this post names the token bucket and does **not** describe GCRA.
- **Strength:** BACKGROUND · **Recommendation:** ADOPT — the rate-plus-concurrency pairing, which matches the spec's wording.

#### I32 · Dropbox — Bandaid service proxy
- **Source:** https://dropbox.tech/infrastructure/meet-bandaid-the-dropbox-service-proxy
- **Verified:** read directly (queueing sections)
- **Key idea:** Bandaid "always processes requests in Last In, First Out order" — not adaptive — fails requests that sit past a timeout without processing them, caps upstream concurrency with a fixed worker pool, and has several priority queues per upstream.
- **Borrow:** (admission, fairness) the same architecture as ours — per-class queues, one dequeuer, a fixed concurrency limit; its argument against dropping on queue *length* (a full queue cannot tell overload from a burst).
- **Do not copy:** always-LIFO — for RESP, replies on one client connection must come back in request order, so within-connection order matters more than for an HTTP proxy.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — same architecture; FIFO stays the normal mode.

#### I33 · Shopify Toxiproxy
- **Source:** https://github.com/Shopify/toxiproxy (`README.md`)
- **Verified:** read directly
- **Key idea:** A real-socket fault-injection proxy with named "toxics".
- **Borrow:** (testing) the toxic names as recognisable vocabulary for the simulator's fault injector: `latency`, `down`, `bandwidth`, `slow_close`, `timeout`, `reset_peer`, `slicer`, `limit_data`.
- **Do not copy:** the tool as the test harness — real sockets, not deterministic.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — names only.

#### I34 · joshbohde/codel (Go)
- **Source:** https://github.com/joshbohde/codel
- **Verified:** read directly (README; code by keyword)
- **Key idea:** A Go acquire/release limiter that ports the *network* CoDel control law to requests.
- **Borrow:** (benchmarking) its 2×-overload before/after table as a template (plain queue p50 963 ms vs 27.7 ms).
- **Do not copy:** the code; the √count control law. It shows Go implementations disagree with folly about what "CoDel for requests" means.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — evidence for conflict C1.

#### I35 · Examined and excluded (one entry)
- **Source:** WeChat DAGOR (arXiv 1806.04075), Shopify Semian, ProxySQL (as its own entry), alibaba/sentinel-golang, FluxNinja Aperture, YouTube Doorman, Dragonfly, KeyDB
- **Verified:** DAGOR abstract only; Semian, sentinel-golang, Aperture, Doorman READMEs read directly; Dragonfly and KeyDB could not verify (from memory)
- **Key idea:** Respectively: priority × user-hash admission; bulkhead + breaker library; MySQL multiplexing; a flow-control framework; a policy-driven control plane; cooperative global rate limiting; two Redis-compatible *servers*.
- **Borrow:** nothing separately. DAGOR's usable part arrives through I28; Semian's bulkhead is the pool cap and its breaker is I15.
- **Do not copy:** frameworks and control planes — the spec forbids external core dependencies and the proxy is already the single control point.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — no extractable small mechanism not already covered.

### Bucket 4 — Foundational papers and algorithms, reduced to the mechanism we would build

Pseudo-code sketches for each are in `agent_work/phase0/research/b4-foundations.md`. *(probe)*
numbers come from `agent_work/phase0/probes/codelsim.py` and `calc.py` — toy, single-seed scripts
of our own (20 servers × 50 ms = 400/s capacity, Poisson arrivals, 30 s), re-run by the
orchestrator with identical output. They are a reason to test this in Commit 0, not a result.

#### F1 · Network CoDel as an algorithm (RFC 8289 control law)
- **Source:** RFC 8289 §5 pseudo-code
- **Verified:** read directly
- **Key idea:** Once sojourn time has stayed above target for an interval, drop one packet, then the next after `interval/√count`, and so on, until delay falls below target.
- **Borrow:** nothing as the shipped rule. Optionally a simulator-only baseline, to show in the README why it is the wrong fit.
- **Do not copy:** the control law. *(probe)* at 1.2× overload served-wait p50 was 58 ms; at 2×, 730 ms; at 10× the queue never stopped growing (85,292 queued after 30 s) — reaching a 400/s drop rate needs `count = 1600`, about 8 s of ramp, and open-loop clients never back off.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — as the implemented algorithm (keep S9's concepts).

#### F2 · Request-queue CoDel — the "Fail at Scale" timeout rule
- **Source:** "Fail at Scale" (S7); constants confirmed there
- **Verified:** read directly (Wayback copy)
- **Key idea:** Do not drop by a schedule; set each request's allowed queue wait. Standing queue (not empty in the last N = 100 ms) → allow M = 5 ms. Otherwise allow N.
- **Borrow:** (admission) state: one `int64` `lastEmptyTime` per queue. On enqueue: if the queue is empty set `lastEmptyTime = now`; deadline = now + (M if `lastEmptyTime < now − N` else N). On dequeue: shed anything past its deadline with a pre-forward `OVERLOADED`; if the queue is found empty set `lastEmptyTime = now`. Testable invariants on the virtual clock: every served request waited ≤ N (exact); queue depth is the same for a run of length T and of 4T; served count ≥ 0.95 × capacity (shedding never idles the backend); a FIFO control with the same seed grows linearly. *(probe)* at 10×: p50 wait 4.3 ms, max 100.0 ms, depth ≤ 580, no idle capacity.
- **Do not copy:** 5 ms / 100 ms as truths — make M and N config and reason about them against backend service time. Classic bug: updating `lastEmptyTime` only at dequeue, so the first burst after idle gets the short timeout.
- **Strength:** CORE · **Recommendation:** ADOPT — one word of state, and its detector does not depend on dequeue order.

#### F3 · folly `Codel` — Facebook's shipped request-queue rule
- **Source:** `facebook/folly` `folly/executors/Codel.h`, `Codel.cpp`
- **Verified:** read directly (both files; header comment re-fetched by the orchestrator)
- **Key idea:** Track the minimum queueing delay per 100 ms interval; if it exceeded the 5 ms target, the queue is overloaded, and any dequeued request that waited more than 2 × target is shed. The header says this replaces the √n law because it "empirically works better for our services".
- **Borrow:** (observability) the load gauge `min(100, 100 × minDelay / (2 × target))` as a free 0–100 overload metric per tenant; the rule that the first sample of an interval is never shed. (testing) a comparison arm in the simulator.
- **Do not copy:** this detector together with LIFO dequeue. It reads the delay of the *dequeued* request; under LIFO that is the freshest one, so the minimum looks healthy. *(probe)* folly detection + LIFO at 10× shed zero requests and the queue grew without bound. It also decides only at dequeue: a fully stalled backend dequeues nothing and sheds nothing.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — gauge and comparison arm; F2 is the detector. (The two research agents split on this; see C1.)

#### F4 · Adaptive LIFO — HHVM's job queue, plus a switch rule and head sweep of our own
- **Source:** "Fail at Scale" (S7); HHVM `hphp/util/job-queue.h` at the commit the article cites
- **Verified:** read directly (article; HHVM header comment and constructor)
- **Key idea:** FIFO normally; when a queue forms, serve the newest request first, because the oldest request's caller has probably given up. HHVM's actual rule is a queue-*length* threshold (off by default, no recommended value).
- **Borrow:** (admission) a fixed-capacity ring buffer per tenant, appended in arrival order. Switch rule (ours — no source gives a time-based one): LIFO while `lastEmptyTime < now − N`, i.e. the same detector as F2, so there is no extra knob. Before every pop, sweep expired requests off the *head* (the head is always the oldest) and answer them `OVERLOADED` at once. On overflow, shed the head, not the arrival.
- **Do not copy:** LIFO without the head sweep. *(probe)* it buried stale requests: 5,450 still queued at the end, peak depth 11,380, each one a client waiting for an answer that will be a shed anyway. LIFO *within* one client connection's pipeline (RESP replies must return in request order).
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — the switch rule and head sweep are our design and must be labelled as such.

#### F5 · folly `LifoSem`
- **Source:** `folly/synchronization/LifoSem.h`, `LifoSemMPMCQueue.h`
- **Verified:** read directly
- **Key idea:** A semaphore that wakes waiting *threads* in LIFO order for cache warmth; the task queue beside it is an ordinary bounded FIFO.
- **Borrow:** nothing for request ordering. The same idea applies to idle pool connections: reuse the most recently used first so the rest go idle and can be reaped.
- **Do not copy:** citing it as the Adaptive LIFO implementation — it is not.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — as an admission influence.

#### F6 · GCRA (generic cell rate algorithm, virtual-scheduling form)
- **Source:** ITU-T I.371 (via Wikipedia's quotation); `throttled/throttled` `rate.go`
- **Verified:** secondary only for the standard; read directly for the implementation
- **Key idea:** One number per tenant, the theoretical arrival time. A request conforms if it is not more than the burst tolerance early. No bucket to refill; the clock does it.
- **Borrow:** (fairness) state: one `int64` TAT in nanoseconds. `T = 1e9/rate`, `τ = (burst − 1) × T`. `allow(now)`: `base = max(TAT, now)`; `new = base + T`; reject if `new − now > τ + T` (leaving TAT unchanged, retry-after = the excess); else store `new`. Invariant: over any window, admitted ≤ burst + rate × window. Reject configurations where integer `T` loses precision.
- **Do not copy:** advancing TAT on reject (a flooder locks itself out indefinitely); `τ = burst × T` (off by one). And the expectation that it provides fairness: a static 250 QPS limit does nothing when a 200 ms stall cuts capacity to 100–160 QPS.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — as the optional per-tenant outer rate gate, not the fairness mechanism (question 4).

#### F7 · Token bucket (lazy refill)
- **Source:** `golang.org/x/time/rate` `rate.go`
- **Verified:** read directly (`advance`, `reserveN`)
- **Key idea:** A bucket of up to `b` tokens gaining `r` per second; nothing runs in the background — tokens are added on each call from elapsed time.
- **Borrow:** (retries, pool) use it where tokens are deposited by *events* rather than time: the retry budget (I14) and the connection-creation cap. For unit-cost requests it makes the same decisions as GCRA — a differential test.
- **Do not copy:** `x/time/rate` itself: `float64` tokens, and an external module.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — integer, event-deposit form only.

#### F8 · Deficit Round Robin (Shreedhar & Varghese 1995)
- **Source:** SIGCOMM '95 paper; Wikipedia pseudo-code
- **Verified:** read directly with a caveat — the paper is a garbled scan; mechanism confirmed against the pseudo-code
- **Key idea:** One queue per flow and a list of non-empty queues. Each visit adds a quantum to the queue's deficit; it may send while the head's cost fits. Constant work per item.
- **Borrow:** (fairness) per-tenant queue, `deficit`, `weight`, and an active list; the dispatcher runs when a concurrency slot frees and skips tenants at their in-flight cap. With unit cost and quantum 1 this is plain round robin; keep the deficit field so `MGET` of k keys can cost k later. Invariant: while two tenants are both backlogged, their served counts (per weight) differ by at most quantum + max cost.
- **Do not copy:** byte-sized quanta. Classic bugs: not zeroing the deficit when a queue empties; inserting a newly active tenant at the head.
- **Strength:** CORE · **Recommendation:** ADOPT — O(1), integer, and it is what ZGateway's "buckets drained round-robin" is.

#### F9 · WFQ / start-time fair queueing / stochastic fairness queueing
- **Source:** Demers, Keshav & Shenker 1989; Goyal et al. 1996; McKenney 1990
- **Verified:** could not verify (from memory)
- **Key idea:** Virtual finish tags served smallest-first (tighter fairness, a priority queue); or hash flows into a fixed number of queues.
- **Borrow:** nothing in v1.
- **Do not copy:** virtual-time bookkeeping or hashing — with a handful of explicit tenants and unit costs the gain over round robin is invisible.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — more state for no measurable benefit here.

#### F10 · FQ-CoDel (RFC 8290)
- **Source:** RFC 8290
- **Verified:** read directly (keyword passages)
- **Key idea:** DRR over flow queues with a CoDel instance *per queue*, and priority for newly active queues.
- **Borrow:** (admission + fairness) keep F2's state per tenant queue. Then the fairness invariant holds by construction: a 50 QPS tenant drains its queue constantly, never enters overload mode and keeps the long timeout; a flooder's queue never empties, so it gets the short timeout and LIFO and absorbs the shedding.
- **Do not copy:** hashing flows to queues; a map from tenant to queue has no collisions. Cap the number of tenant queues and send overflow to a shared default.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — per-queue CoDel state yes, hashing no.

#### F11 · AIMD (Chiu & Jain 1989)
- **Source:** the paper; Netflix `AIMDLimit.java` (I11)
- **Verified:** could not verify the paper (scan without text); read directly for the implementation
- **Key idea:** Add a constant on success, multiply by a factor below 1 on a congestion signal.
- **Borrow:** (concurrency limit) one integer `limit`: on drop or latency over threshold, `limit = ⌊limit × 0.9⌋`; else if `inflight × 2 ≥ limit`, `limit + 1`; clamp to [min, pool cap]. Invariants: bounds always hold; after backend latency steps from 50 ms to 200 ms the limit is non-increasing until recovery, then rises by at most 1 per completion.
- **Do not copy:** Netflix's defaults (5 s timeout, minimum 20). Use a latency threshold in tens of milliseconds and a minimum of 1–2.
- **Strength:** CORE · **Recommendation:** ADOPT — the spec's named controller; in v1, not optional (question 11).

#### F12 · TCP Vegas (Brakmo & Peterson 1994)
- **Source:** SIGCOMM '94 paper; Netflix `VegasLimit.java`
- **Verified:** read directly (congestion-avoidance section; implementation)
- **Key idea:** Compare expected throughput at the best-ever latency with actual throughput; the difference estimates queued work.
- **Borrow:** understanding only.
- **Do not copy:** the minimum-RTT baseline — our latency includes pipeline position, so the minimum is biased low; a stale minimum pins the limit at the floor.
- **Strength:** SECONDARY · **Recommendation:** WATCH ONLY — AIMD first; Gradient2 if an upgrade is wanted.

#### F13 · Little's Law
- **Source:** Little 1961, `L = λ × W`
- **Verified:** secondary only (statement); the arithmetic is ours *(probe: `calc.py`)*
- **Key idea:** A concurrency limit L and service time W cap throughput at L/W.
- **Borrow:** (pool, concurrency limit, benchmarking) the sizing identity and test oracle — simulated throughput must equal `min(offered, L/W)`. Worked for the spec's numbers: 20 connections at 50 ms = 400 QPS; 32 = 640 QPS; during a 200 ms stall, 100 and 160 QPS. Tenant A 5,000 + B 50 QPS is 12.6× / 7.9× overload, 50× / 32× during the stall. **B needs 10 in-flight slots during the stall — exactly half of a 20-connection pool, zero margin; with 32, B's fair share is 16.** 500 clients ÷ 32 = 15.6× (the spec's "~15×"); ÷ 20 = 25×.
- **Do not copy:** the assumption of one in-flight request per connection. With RESP pipelining the in-flight limit and the pool size are different knobs.
- **Strength:** CORE · **Recommendation:** ADOPT — and publish the headline scenarios at pool 32 (question 7).

#### F14 · Balls-into-bins and the power of two choices
- **Source:** Mitzenmacher, Richa & Sitaraman survey (2001); Azar et al. 1994 (not fetched)
- **Verified:** read directly (survey introduction)
- **Key idea:** n balls into n bins at random puts about `log n / log log n` in the fullest; letting each ball pick the emptier of two random bins drops that to about `log log n`.
- **Borrow:** nothing at the backend level — with one bin the model is degenerate and the fan-in count is just proxies × pool. Among 20–32 pool connections an exact least-pending scan is cheap; the only rule is "do not assign by random or hash".
- **Do not copy:** the fan-out model as an explanation of our numbers. Whether it matches Meta's exact formulas could not be verified (their formula images did not render).
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — earns its place only in Variant C (choosing among replicas).

#### F15 · Coordinated omission (Gil Tene; wrk2)
- **Source:** wrk2 README; HdrHistogram's `RecordCorrectedValue`; Tene's talk (not watched)
- **Verified:** read directly (wrk2 README, the Go port's correction function)
- **Key idea:** A generator that waits for a response before sending the next request slows down exactly when the server is slow, so the slow period is under-sampled. Measure from when the request *should* have been sent.
- **Borrow:** (benchmarking) the open-loop rule: `intended[i] = t0 + i/rate` (or cumulative exponential draws for Poisson); send everything whose intended time has passed without waiting on replies; `latency = now − intended[i]` is the headline, `now − actual_send[i]` is also recorded and labelled uncorrected; report achieved vs intended rate and a "could not send" count. Invariant: samples = intended sends.
- **Do not copy:** HdrHistogram's expected-interval back-fill on top of intended-time recording — that counts a stall twice.
- **Strength:** CORE · **Recommendation:** ADOPT — the definition of `cmd/loadgen`.

#### F16 · Open versus closed systems (Schroeder, Wierman & Harchol-Balter, NSDI 2006)
- **Source:** "Open Versus Closed: A Cautionary Tale"
- **Verified:** read directly, partially (the numbered principles)
- **Key idea:** A closed generator and an open one give very different response times at the same load.
- **Borrow:** (benchmarking) label every scenario: noisy neighbor, hot key and retry storm are open-loop; the reconnection storm is naturally closed. Poisson arrivals for tenant traffic, constant spacing for calibration runs. Consequence: `redis-benchmark` is closed-loop, so it can show compatibility but not the shedding results.
- **Do not copy:** n/a.
- **Strength:** SECONDARY · **Recommendation:** ADOPT — as a labelling rule in every result table.

#### F17 · HDR Histogram (Gil Tene)
- **Source:** HdrHistogram README; `hdrhistogram-go` `hdr.go`
- **Verified:** read directly
- **Key idea:** Exponentially growing ranges each split into equal sub-buckets: fixed relative error over a huge range, a fixed array of counters, no allocation on record.
- **Borrow:** (observability, benchmarking) a minimal integer-only version — record, merge, percentile, reset. *(probe)* for 1 µs–60 s: 2 significant digits = 2,560 counters = 20.5 KB; 3 digits = 17,408 counters = 139 KB. Use 2 digits per tenant × outcome inside the proxy and 3 in the load generator. Invariants: relative error ≤ 10^−digits; total = sum of counts; merge equals recording both streams.
- **Do not copy:** auto-resizing, wire encoding, the packed variant. Classic bugs: values over the maximum silently dropped (clamp and count them); percentile returning the bucket's low edge.
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — written from scratch, minimal.

#### F18 · DDSketch, t-digest, Prometheus native histograms
- **Source:** DDSketch (VLDB 2019), t-digest, Prometheus spec
- **Verified:** secondary only (abstract, README, spec introduction)
- **Key idea:** Alternative quantile sketches and an exposition format.
- **Borrow:** nothing.
- **Do not copy:** t-digest in particular — its result depends on insertion and merge order, which fights bit-identical replay.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — one HDR-style histogram covers every need.

#### F19 · benchstat and the Mann-Whitney U test
- **Source:** `golang.org/x/perf/cmd/benchstat` documentation
- **Verified:** read directly
- **Key idea:** Report a median with a confidence interval per benchmark and compare before/after with a rank test; "each benchmark should be run at least 10 times"; "interleave before and after runs".
- **Borrow:** (benchmarking) at least 10 runs per arm, interleaved; emit results in Go benchmark text format so benchstat *can* read them.
- **Do not copy:** treating p < 0.05 as "matters"; importing it into the build. The analytics-edge-gateway plan ruled an in-repo stats script unless Nick approves benchstat (P3).
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the format and run count; in-repo median/spread report; benchstat optional (question 7).

#### F20 · Benchmark method papers — Kalibera & Jones; Georges et al.; Mytkowicz et al.
- **Source:** ISMM 2013; OOPSLA 2007; ASPLOS 2009
- **Verified:** read directly, partially (abstracts, introductions, keyword passages)
- **Key idea:** Variation lives at several levels, so repeat at the level that varies most; report intervals, not best-of; innocuous setup details can flip conclusions.
- **Borrow:** (benchmarking) the unit of repetition is the *run* (fresh proxy process, fresh backend state), not the request; discard warm-up; randomise or interleave run order; an A/A run (same binary as both arms) to publish the noise floor.
- **Do not copy:** multi-level variance analysis; bootstrap intervals (no source read — from memory).
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — the principles, not the machinery.

#### F21 · Metastable failures (Bronson et al., HotOS 2021; Huang et al., OSDI 2022)
- **Source:** https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf ; the OSDI '22 paper
- **Verified:** read directly (HotOS §2.1, §2.4, §3; OSDI abstract and introduction)
- **Key idea:** A trigger pushes a loaded system into a bad state that persists after the trigger is gone, held there by a sustaining loop — most often retries. The root cause is the loop, not the trigger.
- **Borrow:** (testing, retries, pool) a recovery invariant for every chaos scenario: after the fault is removed, goodput returns to ≥ 99% of its pre-fault value within a stated virtual-time bound and queue depth returns to baseline. The paper's worked retry example (healthy at 280 QPS, a 10 s outage, then stuck at 560 QPS with zero goodput) as the retry-storm test. Its pool anecdote — an outage sustained by a connection pool's reuse order, fixed in one line — makes pool pick order a recorded decision.
- **Do not copy:** fleet-scale mitigations; and do not claim more than a small simulator shows.
- **Strength:** CORE · **Recommendation:** ADOPT — one more invariant, cheap in the simulator, and a better argument than the spec's 3×3×3.

#### F22 · "The Tail at Scale" — hedged requests (Dean & Barroso 2013)
- **Source:** https://cacm.acm.org/research/the-tail-at-scale/
- **Verified:** read directly (via the local renderer)
- **Key idea:** Send a second copy of a slow request to another replica after the 95th-percentile delay.
- **Borrow:** nothing in Variants A or B.
- **Do not copy:** hedging against one backend sends duplicate work to the very server that is slow — retry amplification under another name.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — Variant C, reads only, if ever.

#### F23 · Zipf key generation (Go `math/rand/v2`; YCSB)
- **Source:** Go `zipf.go`; YCSB `ZipfianGenerator.java`
- **Verified:** read directly; confirmed locally on Go 1.26.8
- **Key idea:** Go's `NewZipf` requires exponent s > 1; YCSB's default 0.99 cannot be reproduced with it.
- **Borrow:** (benchmarking, testing) the standard-library generator with a seeded source, printing the exponent and the resulting top-1% share in every report.
- **Do not copy:** the spec's phrase "99/1 Zipfian" as if it were a parameter. *(probe)* over 100,000 keys the hottest 1% of keys take 63% of traffic at s = 1.01, 85% at s = 1.2, 98% at s = 1.5 (where one key alone takes 38%).
- **Strength:** CORE · **Recommendation:** ADOPT WITH CHANGE — s = 1.2 with stated shares, plus a separate single-hot-key scenario (question 7).

#### F24 · XFetch — probabilistic early expiration (Vattani et al., VLDB 2015)
- **Source:** "Optimal Probabilistic Cache Stampede Prevention"
- **Verified:** read directly (algorithm listing)
- **Key idea:** Recompute a cached value slightly before it expires, with a probability that rises as expiry nears.
- **Borrow:** nothing — in-flight coalescing already handles a stampede with no cache.
- **Do not copy:** any TTL cache in v1.
- **Strength:** BACKGROUND · **Recommendation:** EXCLUDE — relevant only if caching enters scope (Variant C).

#### F25 · Smart batching (Martin Thompson)
- **Source:** https://mechanical-sympathy.blogspot.com/2011/10/smart-batching.html
- **Verified:** read directly
- **Key idea:** No timer. One writer waits for the first item, then drains whatever else queued while the previous write was in progress. At low load a batch is one item with no added delay; under load batches grow on their own.
- **Borrow:** (batching) the flush rule per backend-connection writer: take one (blocking), then poll until `maxBatch` or `maxBytes`, then one `write`. It meets the spec's "flush immediately when full or under low load" by construction and adds no timer events to simulate. Invariants: batch ≤ `maxBatch`; replies matched in order; with one request in the system, enqueue-to-write time is zero on the virtual clock.
- **Do not copy:** an unbounded feed queue. A fixed 200 µs–1 ms linger (the spec's example, and ZGateway's) only as a measured experiment.
- **Strength:** CORE · **Recommendation:** ADOPT — timer-free batching as the default (question 11).

#### F26 · FoundationDB simulation
- **Source:** https://apple.github.io/foundationdb/testing.html ; Will Wilson's Strange Loop talk (not watched)
- **Verified:** read directly (documentation)
- **Key idea:** Run the whole system in one single-threaded process on simulated time, network and disks, so any run repeats exactly from its seed; then inject failures far harder than production would.
- **Borrow:** (testing) single thread, one seed, simulated time; the "swizzle-clogging" pattern (stop a random subset of connections one by one, restore in random order) as a ready fault schedule for the virtual backend.
- **Do not copy:** a custom language runtime; our equivalent is discipline — core logic as plain state machines.
- **Strength:** CORE · **Recommendation:** ADOPT — the model for `internal/sim`.

#### F27 · TigerBeetle VOPR, Antithesis, turmoil, madsim
- **Source:** TigerBeetle `docs/internals/vopr.md`; Antithesis docs; turmoil and madsim READMEs
- **Verified:** read directly (those pages)
- **Key idea:** Simulators deterministic by "seed + Git commit"; the three sources of non-determinism to close are clocks, interleaving and randomness.
- **Borrow:** (testing) the reproduction recipe printed on every failure.
- **Do not copy:** the tools — Rust, Zig, or a commercial hypervisor. Runtime-swapping does not carry over to Go.
- **Strength:** BACKGROUND · **Recommendation:** WATCH ONLY — lineage and vocabulary.

#### F28 · Go `testing/synctest`
- **Source:** https://pkg.go.dev/testing/synctest ; Go 1.25 release notes
- **Verified:** read directly; present locally (Go 1.26.8)
- **Key idea:** Runs a function in a "bubble" with a fake clock that advances only when every goroutine in it is durably blocked.
- **Borrow:** (testing) the second test layer: the real goroutine shell — timers, context deadlines, write-deadline teardown, leak checks — with real `time` calls and no sleeps.
- **Do not copy:** it as the replay harness. Mutex waits and network I/O are not durable blocks (so a fake network is required), and it gives fake time, not a seeded scheduler — Nick's own probe (P4) saw 24 orderings in 2,000 runs.
- **Strength:** SECONDARY · **Recommendation:** ADOPT WITH CHANGE — a second layer under the custom simulator, not a replacement.

---

## 3. Conflicts and tensions between influences

Each: the two pulls, what each side costs, the recommended resolution.

**C1 · Which "CoDel"? (S9/F1 vs S7/F2 vs F3)**
The spec cites the CoDel paper as the algorithm. Its sources actually contain three: the paper's
drop schedule, the "Fail at Scale" queue timeout, and folly's minimum-delay rule. The paper's
law costs an unbounded queue under heavy open-loop overload *(probe)*. Folly's rule is the
best-specified in code but goes blind when paired with LIFO dequeue *(probe)* — and the spec
requires LIFO. The timeout rule is one timestamp and does not depend on dequeue order, at the
cost of a saw-tooth (shed until empty, relax for N, repeat) instead of convergence. The two
research agents split (one recommended folly, one the timeout rule). **Resolution:** the timeout
rule (F2) ships; folly's detector and the RFC law run only as simulator comparison arms; the
spec's invariant "wait converges to the CoDel target" is reworded to "served wait ≤ N, queue
depth bounded regardless of overload duration, backend never idled by shedding". Question 3.

**C2 · Hyperdrive-style query caching vs a no-cache proxy (S3 vs the spec's thesis)**
Caching would give the largest read-QPS reduction of anything on the list. It costs
read-after-write semantics, an invalidation story, and a correctness surface — the Hyperdrive
MySQL bug (S4) is what that surface looks like in production. Coalescing shares a result only
among requests that overlapped in time, so it needs no staleness contract. **Resolution:** no
cache in v1; coalescing only; caching stays Variant C. Question 5.

**C3 · Transaction-mode lending (PgBouncer, Hyperdrive) vs RESP multiplexing (Twemproxy, Envoy)**
Postgres poolers lend a whole connection to one client until the transaction ends; a Redis proxy
puts many clients' commands on one connection at once. They share the fan-in idea and almost no
mechanism: Variant B has no cross-client pipelining and no micro-batching, and its hard part is
transaction-boundary tracking; Variant A exercises every mechanism in the spec. Carrying both
models in one pool abstraction would fit neither. **Resolution:** build the multiplexing pool for
A; borrow only pool-*management* ideas from the Postgres side (limit taxonomy, bounded concurrent
connects, lockout after connect failure, dirty-connection table). Questions 1 and 6.

**C4 · Static CoDel targets vs adaptive concurrency replacing them (S7 vs I11 / I6 / I28)**
One camp says fixed 5 ms / 100 ms needs no tuning; Netflix and Envoy say fixed limits go stale
and should be inferred; Uber reports that CoDel plus a *manual* concurrency limit was QALM's
adoption problem. But the two control different things: the queue rule decides *which waiting
requests to shed*; the concurrency limit decides *how much the backend is given*. Dropping the
queue rule leaves a standing queue of stale requests; dropping the adaptive limit leaves a pool
that keeps hammering a stalled backend at full width. The real risk is two loops reading the
same signal. **Resolution:** keep both, with separate signals — queue wait for CoDel, backend
round-trip time (measured per batch, excluding queue wait) for AIMD — static caps as the hard
outer bound, and M/N configurable because 5 ms is wrong for a 50–200 ms backend.

**C5 · Variant C ideas (caching, replicas, hedging, P2C) vs v1 scope**
Several strong influences only make sense with more than one backend: outlier detection (I9),
power-of-two choices (F14), hedged requests (F22), shuffle sharding (I29, I30), XFetch (F24).
Each adds a mechanism whose test needs a second backend. **Resolution:** all WATCH or EXCLUDE
for v1; the only concession is keeping the backend behind an interface so a second one is not a
rewrite.

**C6 · Adaptive LIFO vs per-tenant round robin vs RESP reply order (S7 vs S1 vs I20)**
"Fail at Scale" flips one global queue to LIFO; ZGateway drains per-tenant buckets round-robin;
RESP requires replies on one client connection in request order. Global LIFO lets a flooder's
fresh requests jump a quiet tenant's. Per-tenant FIFO loses "freshest first". LIFO inside one
client's pipeline needs reorder buffers. No source describes the combination. **Resolution
(ours, labelled as such):** round robin *across* tenants; LIFO *within* a tenant's queue only
while that queue is in overload mode; at most one queued request per client connection at a time
(the rest stay unread in the socket, which is also the backpressure the spec asks for).

**C7 · Per-tenant rate limit (GCRA, token bucket, Stripe) vs share-under-saturation (Netflix, ZGateway)**
The spec lists token/GCRA budgets first. A rate cap is simple and predictable but caps a tenant
when the backend is idle and protects no one during a stall. A share that binds only under
saturation is work-conserving and is what the fairness invariant literally says, but in-flight
can overshoot and a returning tenant waits for capacity to come back. **Resolution:** queues +
round robin + share-under-saturation are the fairness core; GCRA is an optional generous outer
gate. Question 4.

**C8 · Hard caps vs soft caps (spec invariant vs Hyperdrive, Envoy, nginx, Netflix partitions)**
Four production systems document that their connection or in-flight cap "may be exceeded". Soft
caps buy availability during failover; a hard cap can shed while connections are replaced. The
hard cap is the one guarantee this project exists to demonstrate. **Resolution:** hard, with
connecting + active counted together, and the partition overshoot contained by the pool cap as a
separate bound.

**C9 · Single-threaded seeded simulation vs testing the goroutine code that ships (P8, F26 vs P4, F28)**
The spec asks for a single-threaded seed-driven harness *and* for goroutine-leak and
99-of-100-cancellation invariants. Event loop only: exact and cheap, but the real goroutine
wiring never runs in it. Baton scheduler: runs shipped code and replays, but bans `go`, `select`,
channels and `context` idioms below a seam and exists only as a probe. `synctest` only: real
wiring, no replay. **Resolution:** pure sans-I/O cores (queue, limiter, pool accounting,
coalescer bookkeeping, retry budget, batcher) on the event loop with the replay law stated for
the cores; a thin goroutine shell tested under `synctest` and `-race`. Costs a doubled test
surface. Question 8.

**C10 · Reject stateful commands vs pin a connection (Twemproxy, Envoy vs Predixy, RDS Proxy)**
Rejecting keeps the fan-in arithmetic exact and the pool invariant simple; applications using
`MULTI` or `BLPOP` do not work. Pinning keeps compatibility and lets a few clients drain the
pool. **Resolution:** reject in v1 with a clear error; pinning with a hard cap is a documented
v2. Question 9.

**C11 · Linger-window batching (spec, ZGateway, Envoy) vs timer-free smart batching (F25)**
A window is easy to explain and tune and adds its full length to idle latency unless guarded; it
also makes "queue wait" ambiguous for CoDel and inflates the latency AIMD reads. Timer-free
batching has no knob and no idle cost, but batch size is then an outcome, not a setting.
**Resolution:** timer-free by default with `maxBatch` / `maxBytes`; a linger window only as a
measured experiment. Batch wait is not counted as queue sojourn.

**C12 · Coalescing: shared errors, waiter caps, and who pays (I24, I26 vs the spec's invariants)**
Sharing the leader's error fails N clients on one transient fault; letting waiters re-drive
breaks "at most 1 in-flight per key". An uncapped waiter set is unbounded memory; Vitess's
run-independently overflow breaks the invariant too. And one backend read may serve several
tenants: charge only the initiator and the others ride free; charge all and backend load is
over-counted. **Resolution:** share errors; reject past a per-key waiter cap; coalesced
followers take no admission or pool slot (P14) but are counted per tenant as coalesced hits.

**C13 · Model vs measurement; statistical thresholds vs exact checks (S1 vs S10; spec §4.2 vs P6, P10)**
Meta headlines a modelled 19×; Apollo refuses anything that does not survive its gates. The
spec's invariants are statistical ("> 99%", "within 2× baseline"); the object store learned that
sweep-level statistics false-fail single-seed replay. **Resolution:** in simulation, exact
per-run comparisons against a control run; percentage thresholds only for the real-backend
benchmark; the README reports measured connection counts and labels any formula-derived number
as a model.

**C14 · Patient queues vs honest queues (PgBouncer 120 s, nginx 60 s vs SRE, Cinnamon, CoDel)**
Poolers optimise for never showing the application an error; the overload literature says that
is how a standing queue forms. **Resolution:** short and honest, and say so in the README because
PgBouncer users will expect the opposite.

**C15 · Three retry-budget formulations (SRE ratio vs Finagle bucket vs Envoy concurrent share)**
The ratio is easiest to explain; the bucket handles low-QPS tenants and is integer-only; the
concurrency form needs no window. They disagree during a burst. **Resolution:** Finagle's
deposit/withdraw bucket set to the SRE book's 10%, with a small floor.

**C16 · Shared simulator kernel repo vs in-repo `internal/sim` (Nick's 2026-11-21 preference vs spec)**
Sharing gives one discipline attacked once; it adds a first-party dependency on a repo that does
not exist, against "zero external core dependencies" and the spec's Commit 0. **Resolution:**
in-repo now, kernel files kept free of proxy types so extraction stays cheap. Question 8.

**C17 · Incremental e2e per chunk (P9) vs the spec's Commit 6 roadmap**
The spec lands `make chaos` and `make bench` last; the standing rule says the final chunk is
never the first end-to-end run. The rule makes middle chunks bigger. The object store paid days
for the other choice. **Resolution:** the rule wins; each chaos suite and headline benchmark is
owned by the chunk that first makes it runnable.

**C18 · Minimum-latency baseline vs long average (Vegas, Envoy vs Gradient2)**
A minimum baseline must be re-measured by pinning concurrency low — self-inflicted shedding with
no second host. A long average needs no probe but slowly learns a degraded backend as normal.
**Resolution:** AIMD with an explicit latency threshold avoids the question in v1; if Gradient2
is added, the long average.

---

## 4. Variant implications

| Pushes toward | Influences |
| --- | --- |
| **Variant A (Redis RESP)** | ZGateway's mechanisms (S1: KV gets/puts, shard-keyed batching, hot-key coalescing); Twemproxy, Envoy Redis filter, Redis protocol rules, Predixy (I18, I10, I20, I21) — a complete source-backed command classification; smart batching and pipelining (F25, I20); the Hyperdrive MySQL lesson via RESP2/RESP3 (S4); zeromq-go framing tests (P5); open-loop generation (F15 — RESP is pipelinable); Zipf hot keys (F23); systems-trinkets' Redis labs (P18) |
| **Variant B (Postgres wire)** | Hyperdrive docs and "Pools across the sea" (S2, S11); PgBouncer, PgCat, Odyssey (I1–I3); RDS Proxy pinning (I27); Vitess consolidator as the only coalescing precedent (I26); mansio's pool-budget finding (P14); Little's Law is exact when pool size *is* the concurrency limit (F13) |
| **Variant C only** | Hyperdrive caching (S3), XFetch (F24), ZippyDB context (S12), outlier detection (I9), balls-into-bins / P2C (F14), hedging (F22), shuffle sharding (I29, I30), PgCat replica routing (I2) |
| **Variant-neutral** | All queueing and admission material (S7, F2–F4, F10, I32); fairness (I12, F8, P11); concurrency limits (I11, F11); retries and errors (I14, I16, I17, P13, F21); singleflight lineage (I24, I25); benchmarking (S10, F15–F20, P3); simulation (P2, P4, P8, P17, F26, F28) |

**Does the buffed list change the spec's recommendation of A as the MVP? No — it strengthens it.**

- A exercises every mechanism in the spec. B drops micro-batching and turns "multiplexed pool"
  into "transaction-mode lending"; coalescing in B needs a SQL volatility classifier and must
  stay outside transactions.
- The command-classification work for A is already done by three production proxies we can cite.
  Nothing in Nick's prior work has a Postgres wire parser or transaction-boundary logic; B would
  start from zero on its hardest part.
- Prior-project evidence says the cost is in integration and end-to-end, not primitives. A spends
  the budget there.
- What B has that A lacks is drama: the fan-in story is most vivid against a process-per-connection
  database (5–7 round trips per connect), and "PgBouncer with CoDel and per-tenant fairness" is a
  sharper pitch to database-infrastructure teams. That is a real argument for **B as v2**, on the
  same admission, fairness, coalescing and simulator cores — which is why the plan should keep
  those cores protocol-free.

One scope note inside A: the list argues for **RESP2-only, stateful commands rejected** in v1
(S4, I10, I18, I20). That is narrower than the spec's "RESP2/3" and is question 9.

---

## 5. Steering questions for Nick

Reply with numbers and letters (for example "1a 2a 3a … 10: promote P12"). **Bold** = recommended.

1. **Which variant?**
   **(a) Variant A — Redis RESP — as v1, with cores kept protocol-free so B can be a v2.**
   (b) Variant B — Postgres wire — as v1. (c) A now and B committed as a planned second phase.

2. **Repo name `mini-zgateway` (Twin's choice).**
   **(a) Keep it.** (b) Rename — say to what. (The name leans on Meta's; the project is equally
   Hyperdrive- and PgBouncer-shaped, but the spec calls it Mini-ZGateway.)

3. **Which CoDel, and do we reword the spec's invariant?**
   **(a) The "Fail at Scale" timeout rule (not empty for N → allow M), state kept per tenant
   queue, LIFO switched by the same signal, expired requests swept from the head; invariant
   reworded to "served wait ≤ N, depth bounded, backend never idled"; folly's rule and the RFC
   law as simulator comparison arms only.**
   (b) folly's minimum-delay rule as the shipped detector. (c) The CoDel paper's control law, as
   the spec literally cites.

4. **What is the fairness mechanism?**
   **(a) Per-tenant queues drained round-robin, with a tenant's share enforced only when the
   global limit is reached; GCRA as an optional, generous outer rate gate.**
   (b) Per-tenant GCRA / token-bucket rate budgets as the primary mechanism, as the spec lists.
   (c) Static per-tenant in-flight caps only (mansio's shape).

5. **Caching.**
   **(a) None in v1 — in-flight coalescing only; TTL caching stays Variant C.**
   (b) A small opt-in TTL read cache in v1 with a stated staleness bound.

6. **If A is chosen, how much Postgres-specific influence do we carry?** (If B, read it mirrored.)
   **(a) Pool-management ideas only — limit taxonomy, bounded concurrent connects, lockout after
   connect failure, dirty-connection table — and one README section contrasting our short honest
   queue with PgBouncer's 120 s wait. No Postgres code, no PgBouncer benchmark leg.**
   (b) Also a real PgBouncer-vs-direct comparison leg in `make bench` for context.
   (c) None at all.

7. **Benchmark rigor — how far toward Apollo?**
   **(a) "Apollo-lite": simulator results are the primary evidence (bit-identical, no statistics
   needed); for real-Redis runs — open-loop generator, an A/A run to publish the noise floor,
   ≥ 10 interleaved runs per arm, achieved-rate-within-2% gate with failed runs discarded,
   medians with spread, "inconclusive" allowed; an in-repo stats script, output in Go benchmark
   format so benchstat is optional. Headlines are exact counts (connections, backend reads, who
   absorbed the shedding); latency percentiles are supporting evidence. Published scenarios use
   pool 32, Zipf s = 1.2 with the resulting shares printed plus a single-hot-key scenario, and
   the 19× is called a model.**
   (b) Lighter: one run per scenario, as the spec implies. (c) Heavier: Apollo's full three
   gates with distribution distances and equivalence zones.

8. **Simulator architecture, and where the kernel lives.**
   **(a) Pure sans-I/O cores on a single-threaded seeded event loop, replay law stated for the
   cores; a thin goroutine shell tested under `testing/synctest` and `-race`; kernel in this
   repo, kept free of proxy types. Carried rulings assumed: CI runs fast tests only, no test-only
   dependencies, incremental e2e per chunk.**
   (b) A baton scheduler so the shipped goroutine code itself replays (zeromq-go's plan).
   (c) Create the shared simulator-kernel repo now, with this project as first consumer (your
   2026-11-21 preference). (d) `synctest` only, accepting non-identical traces.

9. **Redis compatibility scope in v1.**
   **(a) RESP2 only; allow-list of commands; `MULTI`/`WATCH`, blocking commands, `SUBSCRIBE`,
   `MONITOR`, client tracking rejected with a clear error; connection pinning documented as a
   possible v2.**
   (b) Also pin a capped number of dedicated connections for stateful commands. (c) Also RESP3.

10. **Should any EXCLUDE or WATCH item be promoted?** Candidates and my answer to each:
    **promote only the RFC CoDel law and folly's detector as simulator comparison arms (already
    in 3a).** Keep out: mansio's would-expire shedding as a second comparison arm (P12);
    Gradient2 as a v1 deliverable (I11 stretch); hedged requests (F22); the two unread companion
    docs (S14 — export them if you want them considered); Canva's session-revocation post (S5).

11. **Should any CORE item be demoted, or spec item reshaped?** My three reshapes — confirm or
    reverse each:
    **(a) GCRA demoted from the fairness mechanism to an optional outer gate (follows 4a).**
    **(b) AIMD concurrency limit is in v1, not an "optional upgrade" — a manual limit was QALM's
    adoption problem.**
    **(c) Micro-batching is timer-free by default; the spec's 200 µs–1 ms linger window becomes
    a measured experiment.**
    Possible demotion I do *not* recommend: dropping Adaptive LIFO to FIFO-only for simplicity.

12. **Does the proxy itself ever retry?**
    **(a) Only failures where no byte reached the backend, at most once, under a per-tenant 10%
    deposit/withdraw budget; never a write after forwarding; the load generator models clients
    with and without their own budgets.**
    (b) Never — the proxy only answers fast and honestly; all retry control is by error codes.

---

## 6. What this changes for the plan

If Nick replies "go with your recommendations", the Phase 1 PLAN.md and C4 set differ from a plan
written straight from the spec in these ways:

- **Admission is one design, not three boxes.** The spec's Commits 4 and 5 (CoDel/LIFO, then
  fairness and AIMD) become: per-tenant ring-buffer queues with the timeout rule and head sweep →
  round-robin dispatcher with share-under-saturation → AIMD limit under a hard pool cap. In C4,
  `internal/admission` shows a pure decision core and a thin concurrent wrapper as separate
  components, and GCRA appears as an optional gate at the edge, not the centre.
- **The coalescer chunk is specified by its failure list**: detached call context, waiter
  refcount, per-key waiter cap, canonical reply form, shared errors, followers take no slot.
- **The protocol chunk ships a generated command-classification table** (allow-list, RESP2) and
  proxy-owned connection state, instead of "read vs write classification" alone.
- **Two generated, test-held documents become deliverables**: the retry/error contract table
  (with a "write may have applied" column) and the capacity table (one row per bound, with its
  counter).
- **Commit 0 grows a decision record**: sans-I/O cores + event loop + synctest shell; the replay
  law scoped to the cores; a comparison-arm facility (FIFO, RFC CoDel, folly) so the README's
  "why not the paper's CoDel" is evidence, not assertion.
- **End-to-end is per chunk.** Each of the five chaos suites and three headline benchmarks gets an
  owning chunk; the harness (virtual backend and a real Redis container) lands with basic
  forwarding; the spec's Commit 6 shrinks to long-budget runs and README assembly. Every chaos
  suite gains a recovery assertion (goodput returns after the fault is removed).
- **Benchmark claims are restated**: measured clients → backend connections at pool 32; backend
  reads per hot-key burst; shed attribution by tenant; goodput. The ~15×, 99/1 Zipfian and
  "converges to target" wordings are replaced with what the mechanisms actually do.
- **Integration chunks are sized larger than primitive chunks**, with arbitration budget, because
  that is where the last project's cost was.
- **Explicitly absent from the plan**: caching, replicas, hedging, RESP3, connection pinning, any
  Postgres wire code, a baton scheduler, a shared kernel repo, external modules of any kind.

---

*Counts: 97 influences — 14 spec-named, 20 prior projects, 35 industry systems, 28 foundations.
Research notes, briefs and probes: `agent_work/phase0/`, indexed in `agent_work/INDEX.md`.*
