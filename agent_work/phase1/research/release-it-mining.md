# Release It! research mined for the mini-gateway build plan

Scope: read-only research, 2026-11-24. Nothing in any repo was modified.

Citation legend. `PLAN-v1` = `R/PLAN-v1-per-topic-projects.md`. `R/` = `/root/Software/book-notes/books/release-it/`. `Lnn:a-b` = `R/lessons/<nn>-*.md`, lines a-b. `S-hunt`, `S-suggest`, `S-tip`, `S-budget`, `S-cap`, `S-transp`, `S-release`, `S-chaos` = `R/skills/{hunt-stability-antipatterns, suggest-stability-patterns, test-integration-points, budget-timeouts-and-retries, review-capacity-headroom, review-operational-transparency, plan-safe-release, design-chaos-experiment}/SKILL.md`. `K/` = `/root/Software/book-notes/docs/knowledge/`. `INF` = `/root/Software/mini-gateway/INFLUENCES.md`.

Labels used below: **[notes]** = stated in the on-machine notes (cited). **[adaptation]** = my translation of a notes item to a Postgres proxy; not in the notes. **[from memory, not in notes]** = book knowledge from my own memory, unverified here.

---

## A. Source inventory

**What the material is.** Not book notes in the summary sense. It is a *teaching project derived from the book*: one deliberately flawed Go platform ("litefleet", a multitenant SQLite platform: HAProxy, 3 gateways, a Postgres-backed control plane, 2 stateful nodes, 2 partner fakes, Toxiproxy between tiers) with 18 planted flaws, fixed over 12 lesson commits, plus 8 reusable "skills" (review procedures). `R/README.md:1-33`, `R/WALKTHROUGH.md:7-28`.

| Piece | Path | What it holds | Use for us |
|---|---|---|---|
| Lesson notes 00-12 | `R/lessons/` (13 files, ~2,100 lines) | Per lesson: the book idea with chapter citations, the flaw at `file:line`, the patch, measured before/after, "Try changing", deviations from plan | Primary source. All read in full. |
| Skills | `R/skills/*/SKILL.md` (8 files) | Procedures with checklists, signals, output table formats, worked examples | Most directly reusable: they are already review checklists. All read in full. |
| Plan | `R/PLAN.md` (v3, 909 lines); `PLAN-v1-…`, `PLAN-v2-…` superseded | Book argument (§1), fakes (§2.4), flaw register (§3), coverage (§4.4), build rules (§6) | Read §1, §2.4, §4.4, §6, L11 section; v1's chapter table and harness topic. Not read end to end (the lesson notes supersede §4.3, per `R/WALKTHROUGH.md:33-34`). |
| Project code | `R/project/` (Go, lab scripts, results CSVs) | The built system | Spot-read only (`cmd/node/drain.go`). Results CSVs not read. |
| Review, state, walkthrough | `R/REVIEW.md`, `R/STATE.md`, `R/WALKTHROUGH.md` | Fable's final review (PASS with fixes), open decisions | Read (REVIEW: verdict, matrix, H1, M1, teaching quality, method). |
| Lab knowledge | `K/toxiproxy-fault-removal.md`, `K/verify-workarounds-hide-defects.md`, `K/lab-on-a-shared-vm.md`, `K/plan-size-estimates.md` | What building the lab taught | Read; several build-process lessons come from here. |

**Completeness.** Built and reviewed: 12 lesson tags, `v1-hardened`, 8 skills, final review complete (`R/STATE.md:8-10`). Every flaw reproduces on its parent tag and lessons 05-12 verify as improved (`R/REVIEW.md:38-44`).

**Limits (important).**
- **Secondhand.** The notes are a project *inspired by* the book, with chapter/section attributions. They quote the book almost never. The reviewer checked citations "against the publisher's contents page ... subsection titles and case-study content from memory" (`R/REVIEW.md:314-317`). Two case-study summaries are flagged "Unconfirmed book summaries" for Nick to check against his copy (`R/STATE.md:92-94`); one was found wrong and reworded (`R/REVIEW.md:72-83`).
- **Uneven coverage of the book's lists.** Of the 12 anti-patterns, *Users*, *Scaling Effects* and *Self-Denial Attacks* get one sentence or one table cell each. Of the 12 patterns, *Let It Crash*, *Handshaking*, *Governor* and *Decoupling Middleware* are "merged inside lessons" rather than taught (`R/PLAN.md:743-744`). Ch. 7 Foundations, Ch. 11 Security, Ch. 16 Adaptation are deliberately omitted (`R/PLAN.md:745-747`).
- **HTTP/JSON and seconds-scale.** Every mechanism is demonstrated on HTTP clients with second-scale timeouts. Nothing is about a binary wire protocol, transaction pooling, or a deterministic simulator. Numbers are litefleet's own measurements on a shared VM (several "before" numbers needed a loaded host: `L04:89-93`, `L06:99-102`).
- **No cross-family review was run** on the notes; the review trail is all Claude (`R/STATE.md:74-77`).
- **The book itself is not on this machine** (INF:946 says the PDF is on Nick's Drive; the Drive connector is not authorized in this session either).

**kb store.** No Release It! entry. Ten searches run (793-802: "Release It", "stability patterns", "circuit breaker", "timeout", "bulkhead", "steady state", "fail fast", "decoupling middleware", "graceful shutdown drain", "backpressure"); hits were unrelated (reliable-distributed-programming book notes, "Let It Race", SQLite internals, rex, disk usage). All ten recorded as `--none` with a note. This matches INF:946.

**A variant mismatch to know about.** INF's F30 entry (INF:944-962) was written with Redis wording ("RESP has no `Retry-After`", "`MGET` width caps"). The brief for this task says Postgres wire protocol. The F30 conclusions carry over, but those two items need translating: the handshake becomes a distinct `ErrorResponse` SQLSTATE/message, and the unbounded-result bound becomes result-set bytes/rows per query and batch width. [adaptation]

---

## B. Stability anti-patterns

The notes' own review order and signals are in `S-hunt:39-52`. "Meaning" is as the notes state it.

| Anti-pattern | Meaning in the notes (cite) | How it shows up in a Postgres pool/admission proxy [adaptation] | Landing-checklist question |
|---|---|---|---|
| **Integration points** | "The number-one killer of systems"; the worst failure is not refusing but "accepting the connection and then never answering, or answering slowly" (`L02:9-15`). Signals: no timeouts, error bodies read whole, "no test where the partner is slow, resets, or sends garbage" (`S-hunt:41`). | Two integration points: backend socket and client socket. Backend accepts TCP and never sends `AuthenticationOk`/`ReadyForQuery`; backend stalls mid-`DataRow`; client sends half a message and stops. | Does every read and write on a backend or client connection in this chunk have a deadline on the virtual clock, and is there a simulator scenario for accept-then-silence on that exact path? |
| **Chain reactions** | Inside one tier of identical instances: one fails, its load moves to the rest, which may then fail too. "A horizontally scaled tier does not make you safe from overload; it decides how the overload spreads" (`L07:9-12`). Check N-1 (`S-hunt:47`, `S-cap:48-52`). | One proxy instance dies; its clients reconnect to the N-1 survivors as a burst; survivors' accept and creation-rate caps decide whether they shed or collapse. Also backend connection count: survivors must not exceed the fleet budget. | With N=3 and one instance killed at 80% load, does this chunk's mechanism shed (goodput near (N-1)/N) rather than queue? Is there a multi-instance sim scenario proving it? |
| **Cascading failures** | One layer's failure becomes the next layer's "because callers keep depending on a dependency that has stopped working"; timeouts make each call finite but do not stop the next doomed call (`L05:8-11`). Signal: a health check that reports the upstream's state as the caller's own `failed` (`S-hunt:46`). | Backend slow → queues fill → clients time out → clients retry → more load. Also: proxy health `failed` because Postgres is down, so a balancer removes every proxy and clients get connection refused instead of a typed pre-forward error. | When the backend is down, does a request cost O(1) and no backend dial (after the first detections)? Does proxy health say `degraded`, not `failed`, for a backend fault? |
| **Users** | Thin. Only as a hunt signal: "expensive pages or reports anyone can trigger repeatedly" (`S-hunt:49`). | One tenant issuing expensive queries or opening many client connections; client connections themselves as a per-user memory cost. | Is every per-client and per-tenant resource in this chunk (buffers, queue slots, map entries) bounded by config, with a refusal when the bound is hit? |
| **Blocked threads** | "Most outages are not crashes but threads stuck waiting, usually on a resource someone failed to release or a call with no limit." Defences: release on every path, bound every pool, "a timeout on waiting for a pool as well as on the network" (`L03:16-19`). The Ch. 2 story: one missed cleanup on an error path drained a pool (`L03:9-14`). | Pool checkout with no deadline; a backend connection not returned (or not destroyed) on an error, cancel, or mid-transaction client disconnect; a goroutine parked on a slow client's socket while holding a backend connection. | For every exit path of this chunk (success, error, timeout, client gone, drain), is the backend connection returned or destroyed, and does a sim invariant assert `in_use == 0` after quiesce? Does checkout have its own deadline inside the request's? |
| **Self-denial attacks** | Thin: "a system whose own behaviour takes it down" (`L04:12`). Practical list is in `S-cap:66-71` as "self-inflicted peaks": cache expiry at once, restarts, deploys, retries during an incident. | Pool warm-up dialing all connections at once; all idle connections hitting max-lifetime together; a config reload that resets every pool; drain of one instance dumping its clients on the rest. | What does this chunk do all at once (dial, expire, flush, retry)? Is it rate-capped or jittered, and is the peak measured in a scenario? |
| **Scaling effects** | Thin. Hunt signal only: "point-to-point fan-out that grows with instance count; per-user or per-tenant metric labels" (`S-hunt:49`). | N instances × pool size against Postgres `max_connections`; per-tenant metric labels; per-tenant state that is O(tenants ever seen). | Is each guarantee in this chunk stated for N instances (multiplies / weakens / survives)? Does any metric carry an unbounded label? |
| **Unbalanced capacities** | Front tier can offer more than the back can take, "so the node needs a limit of its own" (`L07:23-25`); also "a producer faster than its consumer with an unbounded buffer between them" (`S-hunt:48`). Pool math: Σ(pool × instances) ≤ downstream limit, with margin (`S-cap:41-46`). | Clients (thousands) vs pool (tens) vs backend `max_connections`; N × pool vs backend limit; coalescer/batcher producing faster than the backend drains. | Is there a bounded buffer and a full-buffer policy at every producer/consumer boundary in this chunk? Is the fleet pool sum checked against the backend limit in config validation? |
| **Dogpile** | "Many clients acting at the same moment, for example all retrying on the same schedule as a dependency comes back, so recovery meets a wall of traffic" (`L04:10-12`). Signals: fixed-sleep retries, caches expiring at once, synchronized cron (`S-hunt:45`). Remove jitter and "recovery after the fault shows a sawtooth" (`L04:132-133`). | Reconnection storm after backend restart; all queued requests released at once when the breaker closes; coalesced waiters all re-issuing after a failed flight; AIMD instances all probing upward together. | After the fault ends in this chunk's scenario, what is the peak backend dial rate and request rate, and what caps it? Is recovery time asserted, not just steady state? |
| **Force multiplier** | "Automation that turns a small fault into a large one because it acts fast and everywhere" (`L04:12-14`). Remedy is the Governor (`L04:20-23`, `S-cap:77-78`). | AIMD cutting the limit to the floor on a bad latency signal; retry policy amplifying; a health check that removes all instances; CoDel mode flapping. | Does every automatic control loop in this chunk have a floor, a ceiling, a maximum step and a minimum interval, and a test that a pathological signal cannot drive it past them? |
| **Slow responses** | "A slow answer is worse than a fast failure, because it ties up resources on both sides" (`L02:14-15`). Signal: "work with no deadline that can outlive its caller ... a server that keeps computing after the client hung up" (`S-hunt:43`). | Backend 150-200 ms stalls; a query still running on Postgres after its client left (connection stuck in use until done or cancelled); proxy itself answering sheds slowly. | When the client goes away or the deadline passes, what happens to the backend work: cancelled, connection destroyed, or left running, and is the choice counted in the snapshot? Is a shed answered in bounded time? |
| **Unbounded result sets** | Code that "reads all the rows" assumes the data is small; when wrong, "the result is not an error but memory exhaustion", on "the instance that serves everyone else too" (`L08:8-12`). Remedies: bound, stream, "a proxy that buffers a downstream body ... before forwarding" is the signal (`S-hunt:44`). Mid-stream failure: cut the connection "instead of sending a short answer that looks complete" (`L08:45-47`). | A `SELECT *` relayed through the proxy; coalescer buffering a full result to fan out to waiters; batcher accumulating; a slow client forcing the proxy to buffer `DataRow`s. | What is the maximum memory one request can hold in this chunk, and what enforces it? If a result fails after the first byte was forwarded, does the client get a clean post-forward error/close rather than a truncated-but-valid-looking reply? |

Two further rules from the hunt procedure worth adopting verbatim as review steps:
- **Compounds.** "Antipatterns multiply ... retries on top of a leak (each retry leaks again), timeouts that create new error paths into an unreleased resource" (`S-hunt:59-62`). Measured: leak + unbounded pool + retries hit the fd wall in 10-15 s and stayed down after load stopped (`L00:120-124`).
- **Severity bump.** "'Stays broken after the fault is removed' always raises severity one step" (`S-hunt:71`). Every chaos scenario should therefore assert recovery, not only behaviour during the fault.

---

## C. Stability patterns

The notes' governing rule: a pattern is four decisions: **where** it sits, **what numbers**, **what the caller gets when it trips**, **which signal** shows it tripped. "A pattern missing any of the four is not done" (`S-suggest:8-11`). INF:948 already adopts this as the row format. The verdicts below agree with INF:949-959 unless marked.

| Pattern | Meaning in the notes (cite) | Verdict for this proxy | Shapes |
|---|---|---|---|
| **Timeouts** | "Never wait without a limit" on every network call, every resource checkout, and work that can run away; "choose the limits together, so that an inner wait always ends before the outer one gives up" (`L02:17-21`). An inner limit *equal* to the outer races it (`L02:57-60`). "A sleep is also a wait" (`L02:62-63`). Split connect / first-byte / total (`S-budget:38-45`). Make cancellation real and "measure how late the work stops after its deadline" (`S-budget:51-54`). | **ADOPT.** One generated timeout-budget table, edge inward: client idle/read, queue wait (CoDel), pool checkout, backend connect, backend first byte, backend total, client write. Assert `inner < outer` in config validation. | Simulator harness (virtual-clock deadlines), protocol layer, pool, admission queues, coalescer (detached flight needs its own deadline), batching, graceful drain, fleet config |
| **Circuit breaker** | Closed / open / half-open with one trial call (`L05:13-18`). "A breaker without a fallback just fails faster" (`L05:18`). Only failures that say something about the dependency count (`L05:43-44`). Retries stop when open (`L05:47-48`). As built: 5 consecutive, or ≥50% of ≥20 calls in 10 s; 5 s cool-down (`L05:40-42`). | **ADAPT.** Exactly one breaker (backend-down state), with the fast typed pre-forward error as the deliberate "fallback". Reject per-tenant/per-query breakers. Take from the notes: client-fault errors (SQL errors) must not count toward tripping; half-open admits exactly one trial; health goes `degraded` while open. | Pool (connect lockout), retry policy (no retry when open), observability/health, simulator (both modes tested) |
| **Bulkheads** | "Partition [resources] so that one consumer, one path, or one failure can exhaust only its own share" (`L06:8-13`). Lesson: the partition unit matters: per-database lanes alone did not stop a tenant with four databases (`L06:151-156`, `S-suggest:124-126`). "Bulkheads bound concurrency, not CPU" (`L06:119-123`). Health and reports got their own pools (`L06:53-59`). | **ADAPT.** Soft per-tenant share that binds under saturation (the spec's choice) instead of hard partitions; state plainly that it bounds backend *concurrency*, not backend CPU/IO. **ADOPT hard** for one thing: health/stats/metrics must not draw on the data path's pool, queues or locks. | Admission queues, pool, observability/health, benchmarks (noisy-neighbour scenario reports ratio *and* absolute p99, per `L06:157-160`) |
| **Steady state** | "Every mechanism that accumulates something ... needs a matching mechanism that removes it, running automatically"; "anything that only grows is a slow-motion outage with a date already set" (`L10:8-14`). Review method: sample at three points under load and extrapolate time-to-wall (`S-cap:73-75`). Cache smaller than working set thrashes (`L10:118-120`). | **ADOPT.** Per-chunk "what grows here, what removes it, which gauge shows the level" row. Accumulators: per-tenant queues/histograms for quiet tenants, coalescer flight map, batch map, idle backend connections, GCRA per-tenant state, tenant label sets, log volume while shedding. | Admission queues, GCRA, coalescer, batching, pool (idle eviction), observability, simulator (tenant-churn soak) |
| **Fail fast** | "If a request is going to fail, fail it before it has used anything expensive. Validate input and check resources first, then do the work" (`L03:19-21`). Bounded pool wait then a *named* refusal (`L03:56-60`). | **ADOPT.** Order per request: parse/allow-list → breaker state → rate gate → queue admission → checkout. Each refusal has a distinct pre-forward error and counter. | Protocol layer, GCRA, admission, pool |
| **Let it crash** | Thin. "An instance that dies and restarts in a known good state is better than one that limps", survivable only with honest health, shedding upstream and clean recovery (`L08:21-25`). | **REJECT as a mechanism / ADAPT as a scenario.** The proxy holds no durable state, so there is nothing to design; a crash is a reconnection storm and a chain-reaction test. Per-connection "let it crash" is useful: on protocol desync or unknown backend state, destroy the connection rather than repair it. | Pool (destroy-on-doubt), simulator (instance kill), fleet scenarios |
| **Handshaking** | Thin. "Tell callers you are refusing, so they can back off or go elsewhere: a 503 with `Retry-After`, a health check that says 'degraded', a balancer that stops sending" (`L07:17-20`). Clients that honour it stop spinning (`L06:65-66`). | **ADAPT.** Postgres has no `Retry-After`; the handshake is a distinct SQLSTATE/message per refusal reason plus three-state health. Document which errors are safe to retry (pre-forward) in the error contract. | Protocol layer (error taxonomy), observability/health, graceful drain |
| **Test harnesses** | A stand-in "that can be made to misbehave on purpose, in every way the real one might"; network *and* application failure modes; each test asserts "the client failed *the right way*, with an error the caller can act on, inside its budget" (`L11:16-21`). 5 of 12 chaos tests failed on code whose happy-path tests passed (`L11:74-95`). | **ADOPT.** This is Commit 0. Take the assertion shape: class + elapsed + bounded resources (`S-tip:56-64`), the matrix as a living artifact, and the harness self-check (`S-tip:72-73`). Fault catalog in section D. | Simulator harness, protocol layer, pool, benchmarks/chaos |
| **Decoupling middleware** | Thin; a pointer only: "once work has to survive a restart, it belongs in something that persists" (`L09:20-22`). | **REJECT.** Accept-now-deliver-later contradicts honest post-forward error semantics for a synchronous SQL proxy (agrees with INF:959). | None |
| **Shed load** | "Refuse the excess early and cheaply ... so the work you do accept finishes in time" (`L07:15-17`). "The limit is the latency budget expressed as concurrency" (`L07:101-102`); size from Little's law (`S-cap:57-58`). Health and metrics are never shed (`L07:41`). Balancer limit a little *above* the instance's so the instance refuses first (`L07:121-123`). Measured 11.4% → 68.5% goodput after losing 1 of 3 (`L07:87-93`). | **ADOPT** (already the core: CoDel + LIFO + AIMD). Add from the notes: measure the *cost of a refusal* (`L07:95-97`), shed at the callee, low-priority paths share a fraction of the limit (`L07:38-41`). | Admission queues/CoDel, AIMD, GCRA, benchmarks |
| **Create back pressure** | "Bound every queue and decide what happens when it is full: make the producer wait, refuse new work, or put the work somewhere durable" (`L09:9-14`). As built: wait at most 20 ms, then overflow path (`L09:35-38`). | **ADOPT.** Explicit plan decision per boundary: stop reading the client socket (TCP back pressure) versus read-and-shed. Also proxy→client direction: a slow client must not make the proxy buffer unboundedly or pin a backend connection. | Protocol layer, admission queues, batching, coalescer fan-out |
| **Governor** | "Limits how fast automation may act" (`L04:20-21`); for automation that changes capacity: "bounded step size, bounded rate, a floor and a ceiling, so a bad signal cannot remove the fleet" (`S-cap:77-78`). Retry form: idempotent only, full jitter, ≈10% token-bucket budget, never past the caller's deadline (`L04:21-24`, `S-budget:59-73`). | **ADOPT.** AIMD floor/ceiling/step/interval; retry budget; connection creation-rate cap; CoDel mode-switch hysteresis. | AIMD, retry policy, pool, admission |

Retry specifics worth carrying into the retry-policy interface (all [notes]):
- "When a call times out, you do not know what happened on the other side" (`L04:16-18`). 308 duplicate rows measured from retried writes (`L04:82`). This is the notes' evidence for pre-forward-only retry.
- Do not retry when it cannot help: the callee's own deadline exceeded, bad payloads (`S-budget:63-64`, `L04:48-49`).
- Skip a retry if remaining time < backoff + minimum useful attempt (`L04:42-43`).
- A proxy-side retry that *holds a slot while waiting* can starve unrelated traffic: retrying `draining` once filled shed limits and requests to the *other* node were refused (`L12:263-267`). The proxy's optional retry must not hold a queue slot or pool permit during backoff.
- Deadlines had already closed most of the slow-dependency storm before any retry policy existed (`L04:122-126`): timeouts first, retry policy second.

---

## D. Specific topics

### D1. Transparency / observability

- **Three surfaces**: instance metrics ("above all the time spent waiting on each integration point"), logs "written for the people who read them during an incident", health checks that say "whether this instance can do its work right now" (`L01:14-21`).
- **Health has three states**: `ok`; `degraded` (keep sending traffic); `failed` (take me out), body names each check and reason (`L01:68-69`, `S-transp:30-31`).
- **`failed` only for what makes this instance unable to work**; a dependency the instance can work around is `degraded`. "A health check can cause the cascade it is meant to report" (`S-transp:32-37`, `L01:145-147`).
- **The health check must not starve**: own small pool/connection and short timeout (`S-transp:38-39`). Measured: health pinging through the shared pool failed 13 of 55 checks under report load: "a busy pool looked like a dead database" (`L06:31-35`, `L06:94`, `S-transp:138-140`). Under overload "a health check waits in the same queue" and the chain reaction completes (`L07:108-111`).
- **A health rule is part of each degraded mode** and must change when a mechanism is added (`L05:65-68`, `L05:121-123`, `S-transp:133-137`).
- **Physical facts, not just liveness**: fds vs limit, disk, log size (`S-transp:40`, `L01:74-76`).
- **Metrics every stability mechanism must expose** (`S-transp:48-51`): pool in use / idle / waiters / wait timeouts; queue depth **and oldest-item age**; breaker state and transitions; shed and admission rejections by reason; retry attempts and budget exhaustion; cancellations by reason; fds, goroutines, memory.
- **Cardinality**: no tenant label on metrics; per-tenant questions go to logs/usage data (`L01:64-66`, `S-transp:53-54`).
- **Logs**: structured, request id propagated on every hop, one line per request and per dependency call; very frequent calls logged only when slow or failed but always counted (`L01:83-90`, `S-transp:56-64`). Rotation "so logs cannot fill the disk" (`S-transp:64`).
- **Config and version exposure** per instance (build, wire version) so a mixed-version rollout is legible (`S-transp:66-68`).
- **Acceptance test for observability**: add latency below every timeout to one dependency and answer "which dependency is slow?" from the ops surface alone; then fail it and time how long until health says so (`S-transp:70-76`).
- **Observability must survive the failure it observes.** A request logger that ran *after* the handler was skipped by an abort path; the failing request was the one with no log line or count. "Code that records an outcome belongs in a `defer`" (`L01:167-189`, `K/verify-workarounds-hide-defects.md:13-15`). And: "when a verify can't find a signal, treat that as a finding, not as something to route around" (`L01:188-189`).

### D2. Graceful shutdown / drain / deployment

- **Drain sequence as built (node)**: stop admitting (refusal + `Connection: close`, health fails) → wait for in-flight work, bounded by the statement deadline → flush/close resources → exit (`L12:94-100`; code `R/project/cmd/node/drain.go:17-40`, deadline = statement deadline + 1 s, logs how many were still running at the deadline).
- **Stateless tier drain**: fail health, wait for the balancer to notice (2.5 s), then shut down gracefully (`L12:101-102`). Without it a rollout lost 3-5 requests per run (`L12:273`).
- **Health and metrics stay answerable during drain** (`drain.go:45`).
- **Rollout**: one instance at a time, gated on the instance's health before touching the next; stop on errors (`L12:105-106`, `S-release:52-57`). A deploy is N-1 capacity for its duration (`S-cap:68`).
- **Cold-start herd after restart**: a restarted node reopened ~120 databases under one lock and timed out its callers; fixed by opening outside the lock with singleflight (`L12:123-126`, `L12:283-284`).
- **Failure accounting for a restart** is three classes: refused fast (retryable), timed out, other (cut mid-answer); the target is zero in the third (`L12:146-152`).
- **SIGKILL still loses in-memory work**; drain needs a chance to run (`L09:106-108`).
- Not relevant to us: expand/contract migrations, tolerant JSON readers, backfill (`L12:16-33`). One transferable idea: tolerant reading (`L12:31-33`) → the protocol layer should pass through unknown-but-well-framed backend message types and startup parameters rather than reject them. [adaptation]

### D3. Configuration

Almost nothing. The notes cite Ch. 8 "Code; Configuration" only in a chapter table (`R/PLAN-v1-per-topic-projects.md:47`) and never teach it. What exists:
- Capacity numbers live in one generated file that every scenario reads (`L00:74-77`, `S-cap:31-34`).
- "Set the parameters from measurements, not taste ... Mark every number 'initial' and name the experiment that will tune it" (`S-suggest:62-68`). Parameters moved after measurement three times (`S-suggest:131-133`).
- Derive bounds from the real limit and enforce in code: handle budget = (nofile − reserved) ÷ fds per resource (`L03:50-53`, `S-cap:43-44`).
- Expose running config/version (`S-transp:66-68`).

### D4. Connection pools specifically

What the notes have:
- **The Ch. 2 story** (as summarized, unverified against the book): an exception during cleanup skipped returning a connection; the pool drained; every request thread blocked on checkout; "the servers were up, the database was fine" (`L03:9-14`).
- **Release on every path** using the language's cleanup idiom, not hand-written error branches (`L03:17-18`, `L03:44-49`). Regression test shape: N failing operations then one that must succeed (`L03:65-67`).
- **Bound the pool and bound the checkout wait** (250 ms inside a 2 s statement deadline), with a distinct `pool_timeout` error and counter (`L03:56-60`). Long checkout waits make requests "queue behind the noisy tenant for the full statement deadline instead of failing fast" (`L03:119-120`).
- **Timeouts create new error paths that leak** (`L02:140-143`): cancelled writes took an early return and leaked a connection *and a lock*.
- **A leaked connection can hold backend state** (there, a write lock), so "never returned" is worse than one lost slot (`L03:27-30`). [adaptation: for transaction pooling, a connection returned mid-transaction or with session state is the same class of bug.]
- **Sizing from measurement**: pool 10 → 20 (100% in use, 5 waiters, mean call 42 ms "almost all of it waiting for the pool") → 8 once load moved elsewhere (`L03:133-137`, `L06:166-168`). Pool smaller than arrival rate × hold time queues (`S-cap:45`).
- **Σ(pool × instances) ≤ downstream connection limit, with margin** (`S-cap:42`).
- **Too few idle connections is its own failure**: with 2 idle, each extra concurrent call opened a fresh Postgres backend and paid ~10 ms (`L00:149-152`).
- **Recycling**: max lifetime 30 min, idle time 1 min, stated without rationale (`L03:54`; `R/project/internal/registry/registry.go:77`).
- **Separate pools per path** (critical / reporting / health) (`L06:53-59`).
- **Pool metrics**: in use, idle, waiters, wait timeouts, labelled by pool (`L01:59`, `L06:57`, `S-transp:48`).
- **Lock or resource held across a slow network call** is a blocked-thread signal (`S-hunt:42`, `L06:36-39`).

What the notes do **not** have (searched for "firewall", "JDBC", "half-open", "keepalive", "dead conn", "stale conn", "test on borrow": no hits in lessons or skills): firewall idle-timeout silently dropping pooled connections, dead-connection detection / validation on checkout, TCP keepalive tuning, JDBC driver gotchas. v1 of the plan names "the 5 a.m. problem" only as a section title in a source line (`R/PLAN-v1-per-topic-projects.md:70`) and mentions "the listen queue is full and even the connect stalls" (`:74`). See section F.

### D5. Test harness: every fault mode the notes name

Principles first:
- Faults come "only from outside the code"; "never through hidden code paths in the service ... then you are testing the test hook" (`L00:87-88`, `R/PLAN.md:834-837`, `S-chaos:66`). [adaptation: in the simulator, faults belong in the fake backend / virtual network, never in `if testing` branches of proxy cores.]
- Network-layer and application-layer modes are both required (`L11:16-19`).
- A test asserts **class + elapsed ≤ budget + bounded resources**; "an unclassified error string fails the test" (`S-tip:56-64`).
- Inject → call through the public API → classify → assert → remove the fault in cleanup so a failed test cannot poison the next (`S-tip:66-70`).
- **Prove the harness tests what you think**: fault on A, call B, must pass fast (`S-tip:72-73`, `L11:123-124`).
- An experiment "is valid only if it reproduces the problem without the fix and passes with it" (`S-chaos:76-78`).
- "An injector has failure modes of its own. Check that removing the fault really removes it before trusting a recovery number" (`K/toxiproxy-fault-removal.md:14-15`).
- One fault at a time; fixed timeline (warm-up → baseline → fault on → fault off → recovery) on a shared clock; thresholds written before the run (`S-chaos:44-48`, `S-chaos:53`, `S-chaos:68-69`).
- Load is open-loop; "a closed-loop generator waits politely and hides overload" (`L00:80-82`, `S-cap:31-34`).

**Complete list of fault modes named anywhere in the notes**, with source, and a Postgres-backend translation [adaptation]:

| # | Fault mode (as named) | Where named | Fake Postgres backend equivalent [adaptation] |
|---|---|---|---|
| 1 | Connection refused / partner down | `S-tip:40`, `L11:59` (`down`), `PLAN-v1:297` | Dial fails immediately |
| 2 | Accepted, never answered (blackhole) | `S-tip:41`, `L02:89-91`, `L05:79-80` | TCP accept, no reply to StartupMessage; or reply to startup, then silence after Query |
| 3 | Connect stalls (listen queue full) | `PLAN-v1:74` | Dial hangs until connect timeout |
| 4 | Read-then-silence (reads request, never replies) | `PLAN-v1:303` | Same as 2 at the Query stage; distinguish in the catalog because "no byte reached backend" is false here (matters for pre-forward retry) |
| 5 | Slow answer: latency, with jitter | `S-tip:42`, `L04:70-72`, `S-chaos:57` | Delay before first response byte; the spec's 150-200 ms stall |
| 6 | Slow bytes / bandwidth limit (1 KB/s, 50 KB/s) | `S-tip:43`, `L11:59`, `L06:76-78` | DataRows trickle |
| 7 | One byte per second headers / slow headers / headers never come | `PLAN-v1:304`, `S-tip:49`, `L11:54-55` | RowDescription delayed or never sent after Query accepted |
| 8 | Fragmented answer (slicer, 64-byte chunks) | `S-tip:44`, `L11:111-112` | Messages split at arbitrary byte boundaries, including inside the 5-byte header |
| 9 | Reset mid-exchange (`reset_peer`; RST on first read) | `S-tip:45`, `L11:59`, `PLAN-v1:304-305` | RST before, during and after a result; mid-transaction |
| 10 | Garbage bytes | `PLAN-v1:304`, `S-hunt:41` | Invalid message type byte; impossible length field |
| 11 | Wrong status with a non-protocol body (5xx with HTML) | `S-tip:46`, `L11:54` | ErrorResponse where data was expected; non-Postgres bytes (e.g. an HTTP or TLS reply) on the socket |
| 12 | Success status with malformed / truncated body ("a 200 cut off") | `S-tip:47`, `L11:54` | Well-formed RowDescription then stream ends without CommandComplete/ReadyForQuery; DataRow with wrong column count |
| 13 | Huge body / oversized response (10 MB, 50 MB) | `S-tip:48`, `L11:54-55`, `PLAN-v1:304` | Very large single DataRow; very many rows; huge ErrorResponse/NoticeResponse |
| 14 | Stale state / version mismatch | `S-tip:50`, `L11:60` | Backend in unexpected transaction state at checkout; unexpected ParameterStatus / unknown message type |
| 15 | Instance dies / freezes / restarts (kill, pause, SIGTERM) | `S-chaos:60`, `L01:104-105` (pause), `L07:63` (kill), `L09:67-68` (restart) | Backend process killed (all connections reset at once); frozen (sockets open, nothing moves); restarted (refused, then accepting, cold) |
| 16 | Slow client: sends half a request and stalls | `L02:94-95` | Client sends partial message / stops reading results (client-side fault, needs a fake client too) |
| 17 | Caller hangs up while work runs | `L02:71-73` (`caller_gone`) | Client disconnects mid-query, mid-transaction, while queued, while a coalesced waiter |
| 18 | Data hazard created by SQL (big table, lock holder, constraint violations) | `S-chaos:62`, `L03:76-79`, `L12:215-219` | Query that errors mid-transaction; long lock wait on the backend |
| 19 | Overload as a fault (offered rate as % of measured capacity) | `S-chaos:63` | Open-loop arrival above pool throughput |
| 20 | Resource wall lowered for the run (CPU, memory, fds, disk) | `S-chaos:64`, `L10:65-66` | Backend `max_connections` set low so dials are refused with "too many clients" |
| 21 | Slow partner that makes a tight timeout misfire (answers in exactly ~1 s vs 1 s header timeout → every success counted as failure and resent) | `L09:54-56`, `S-budget:116-119` | Backend latency sitting right at a timeout boundary |
| 22 | Per-connection partial fault (toxicity 0.5) fades because slow connections get culled and healthy ones reused | `L04:150-152` | Some pooled connections slow, others fine: tests whether the pool evicts the slow ones or keeps reusing them |

Not named in the notes at all: **half-open sockets** (peer gone, no FIN/RST seen) and **asymmetric latency**. Both are in the mini-gateway spec's own Commit 0 list (`/root/Software/mini-gateway/docs/project-source.md`, §4.1), so the spec is ahead of the notes there. Mode 15's "pause" (`docker compose pause`) is the closest the notes come to half-open.

Result of running the notes' own matrix, for calibration of expectations: timeouts alone already handled latency, blackhole, slicer, bandwidth, slow headers; what failed were faults the client "noticed but could not name" (reset, down, 5xx-with-wrong-body, malformed) and the oversized error body read in full (`L11:109-117`).

---

## E. Insights that would change the order of build steps or add a step

Spec order for reference: C0 simulator → C1 protocol/forwarding → C2 pool → C3 coalescer → C4 CoDel/LIFO → C5 per-tenant fairness + AIMD → C6 benchmarks. The notes' dependency order for remedies is: "transparency → timeouts → resource release and bounded pools → retries with restraint → breakers with fallbacks → bulkheads → shed load → bounded results → back pressure → steady state → test harness → version handling" (`S-suggest:29-32`).

1. **Observability lands with C0, not after.** The notes moved transparency to lesson 1 "because every later lesson is measured through what it adds" (`L01:27-28`, `R/PLAN.md:740`). For us: the snapshot interface, the collector, and the decision trace are C0 deliverables, and the simulator's fake backend and virtual network get snapshots too (faults injected, bytes delivered), so a scenario can show the fault happened. Agrees with INF's landing gate; the notes add the reason and the "harness is observable too" corollary [adaptation].

2. **Add a timeout-budget table as a C1/C2 artifact, before admission.** Deadlines closed most of the retry storm before any retry policy existed (`L04:122-126`), and every later pattern assumes bounded waits. The table is generated and gains rows per chunk; config validation asserts `inner < outer` (`S-budget:33-36`). Include the "first-byte ≥ longest legitimate work" rule (`S-budget:41-43`), which bit the notes twice (`L02:166-169`, `L09:54-56`).

3. **Add a "release on every path" invariant to C0 that every later chunk inherits.** Global quiesce check after every scenario: pool in-use = 0, queue depth = 0, flight map empty, no goroutine/timer outstanding. Reason: "timeouts create new error paths, and lesson 3 is about what error paths leak" (`L02:140-143`); the compound leak + retry took a tier down in 10-15 s and did not recover (`L00:120-124`). This is cheap in a sans-I/O simulator and catches the Ch. 2 class of bug at the chunk that introduces it.

4. **Define the error taxonomy in C1, with the harness matrix as its test.** The notes' harness lesson came *second to last* and found 5 of 12 failures unclassified (`L11:109-117`). Doing it first means: C1 ships the pre-forward / post-forward classes and a matrix (fault × path → expected class, elapsed budget) that each later chunk extends with its own rows (`S-tip:77-81`). The matrix records "did any byte reach the backend", which the retry policy needs later.

5. **Every chunk's chaos scenario runs twice: mechanism off, then on.** "An experiment that passes both ways tests nothing" (`S-chaos:76-78`); the notes enforced this with `FLAW REPRODUCED` / `IMPROVED` verdict lines pasted into commit bodies (`R/PLAN.md:807-809`). Add an ablation arm to each chunk's acceptance: the scenario must fail the invariant with the new component disabled or replaced by a pass-through. This is a new gate item beside tests, snapshot and review.

6. **Add a harness self-test step to C0.** Fault on backend A / connection A, traffic on B, must be unaffected (`S-tip:72-73`); and "removing the fault really removes it" (`K/toxiproxy-fault-removal.md:14-15`). In the simulator: a meta-test that each injector, once cleared, leaves the fake backend bit-identical in behaviour to a never-faulted one.

7. **Health-path isolation lands with the first chunk that has a listener, and health mapping is updated by every protective chunk.** Health through the shared pool gave false "dead database" (`L06:31-35`); health in the request queue completed a chain reaction (`L07:108-111`). And each new degraded mode changed the health rule (`L05:65-68`, `S-transp:133-137`). So: per-chunk checklist line "what does health report while this mechanism is tripping?" rather than a single health chunk at the end.

8. **Design the per-tenant key into the queue structure when CoDel lands (C4), not as a C5 retrofit.** The notes built bulkheads (06) *before* shed load (07), and found the partition unit was the decision: per-database lanes failed, per-tenant worked (`L06:151-156`, `S-suggest:124-126`). The spec has CoDel in C4 and tenants in C5. Either swap emphasis (tenant-keyed queues first, CoDel per queue) or make C4's queue tenant-keyed with a single default tenant. Then "shed load inside each partition" (`S-suggest:44`) is the natural layering.

9. **Retry policy after timeouts and pool, before the breaker; and never holding a slot during backoff.** The notes moved retries ahead of the breaker "so the storm is demonstrable" (`R/PLAN.md:735`), and a retry that held a slot starved unrelated traffic (`L12:263-267`). For us the policy interface is default-off, but its *seam* (and the "no byte reached backend" fact from item 4) should exist by C2; the breaker then gets "stop retrying when open" (`L05:47-48`) for free.

10. **Reserve the drain state early; implement it late.** Drain touched every component in the notes and was pulled forward piecemeal (`L09:119-122` spill added out of plan; `L12:273` gateway drain added out of plan; lesson 12 ran 507 lines against 290 planned, `K/plan-size-estimates.md:11-12`). Give every component a `draining` input and a distinct refusal class from its first chunk; the drain chunk then only sequences them: stop admitting → finish in-flight bounded by the request deadline → close backend connections → exit (`L12:94-100`).

11. **Add a cold-start / post-restart scenario to the pool chunk (C2).** Restart produced a self-inflicted herd (`L12:123-126`); "self-inflicted peaks" is an explicit review step (`S-cap:66-71`). The creation-rate cap is the spec's answer; the scenario that proves it (empty pool + full client load at t=0; all connections reaching max lifetime together) belongs with the pool, not with benchmarks.

12. **Add a steady-state soak step once all accumulators exist, plus a per-chunk growth row.** Method from the notes: sample each size gauge at three or four points under churn and extrapolate time-to-wall; assert growth over the last window ≤ 0 (`L10:65-68`, `L10:80-82`, `S-cap:73-75`). INF already adopts steady state; the notes add the concrete test shape and the working-set warning (an eviction bound below the working set thrashes, `L10:118-120`).

13. **Calibration is a step that precedes the benchmark chunk, and its output is a file.** Measure capacity open-loop, write it to a file every scenario reads, express every scenario as a fraction of it (`L00:74-77`, `S-cap:31-34`). "Make the target the bottleneck" or the overload proves nothing (`K/lab-on-a-shared-vm.md:27-29`, `L07:116-120`). On this shared VM: median-of-windows p99 with worst window printed, record host load beside numbers (`K/lab-on-a-shared-vm.md:30-35`). The notes hit exactly this on vm-1 (`R/STATE.md:87-91`).

14. **Chunk sizing.** Chunks that add a new mechanism ran about 2× plan; single-rule fixes landed close; anything over ~400 lines was split (`K/plan-size-estimates.md:14-16`, `R/PLAN.md:821-824`). Expect observability, coalescer and drain chunks to be the 2× ones.

15. **Gate rule from a real miss:** a check that cannot find its signal is a finding; do not make the check fall back to another signal (`K/verify-workarounds-hide-defects.md:10-12`). Add to the reviewer brief for the snapshot gate.

---

## F. Gaps: what the notes do not cover that the book does

Stated as far as I can tell. Items marked **[from memory, not in notes]** are my recollection of the 2nd edition and are unverified in this run; the book is not on this machine.

- **Connection-pool and socket specifics.** The notes have no firewall idle-timeout story, no dead-connection detection, no validation-on-checkout, no keepalive discussion, no JDBC behaviour. [from memory, not in notes]: Ch. 4 "Integration Points" contains "the 5 a.m. problem", where a firewall silently dropped idle pooled database connections so that the first use each morning hung in a blocking socket read with no RST, plus a walk through the TCP handshake and how half-open/blackholed connections look to an application, and remarks on vendor client libraries blocking in ways you cannot control. This is the most relevant missing material for a pooled Postgres proxy and should be read from the book directly before the pool chunk is planned. The notes confirm only that a section by that name exists (`R/PLAN-v1-per-topic-projects.md:70`).
- **Users, Scaling Effects, Self-Denial Attacks** as full anti-patterns. The notes give one line each. [from memory, not in notes]: the Users section covers per-session memory cost, expensive transactions, unwanted users (bots/scrapers) and session floods; Scaling Effects covers point-to-point communication and shared resources that stop working at larger instance counts; Self-Denial Attacks covers marketing-driven traffic spikes and shared-nothing as mitigation.
- **Let It Crash, Handshaking, Governor, Decoupling Middleware** as patterns with their own conditions. The notes merge them into other lessons (`R/PLAN.md:743-744`). [from memory, not in notes]: Let It Crash lists prerequisites (limited granularity/crash boundary, fast replacement, supervision, reintegration); Governor discusses hysteresis and making automation's unsafe direction slow; Handshaking notes that TCP-level handshaking exists but HTTP-level does not, and suggests health-check queries as a substitute.
- **Circuit breaker details beyond the state machine**: the notes' thresholds are litefleet's own. What the book says about threshold choice, per-failure-type handling, or scope is not in the notes.
- **Steady State subsections**: the notes list "Data Purging, Log Files, Limit Caching" as titles (`L10:4`) and build three examples; the book's guidance on log rotation/retention and in-memory cache limits is not summarized.
- **Ch. 7 Foundations, Ch. 11 Security, Ch. 16 Adaptation**: deliberately omitted (`R/PLAN.md:745-747`). Foundations (networking, NICs, containers/VMs) may matter for a proxy's socket behaviour.
- **Ch. 8 Configuration and Code**: named in a chapter table only. No guidance on config handling, immutable deploys, or per-environment config is summarized.
- **Ch. 9 Interconnect** beyond Demand Control: DNS, load-balancer health-check behaviour, service discovery, migratory VIPs: not covered. The notes use HAProxy settings as the worked form of Demand Control (`L07:42-51`) without summarizing the chapter.
- **Ch. 10 Control Plane**: cited for "System-Wide Transparency" only (`L01:23-25`); the rest (configuration services, provisioning, command and control) is absent.
- **Ch. 13-14** are covered through schema migration and JSON readers only. Instance lifecycle beyond drain, deployment automation, and API version negotiation are thin. Protocol-level versioning for a binary wire protocol is not addressed.
- **Ch. 15 case study and Ch. 17 Chaos Engineering**: Ch. 17 is used as the method of the verify scripts and the `design-chaos-experiment` skill (`R/PLAN.md:746-747`); the chapter's own content is reduced to the skill's step headings (`S-chaos:109-110`).
- **Case studies** (Ch. 2, 6, 12, 15): one-paragraph summaries, two of them flagged unconfirmed (`R/STATE.md:92-94`).
- **Direct quotations and the book's own checklists** ("Remember this" boxes [from memory, not in notes]) are not reproduced anywhere in the notes; every checklist above is the notes' authors' synthesis, not Nygard's wording.
- **Nothing in the notes is about**: transaction or session pooling semantics, prepared statements across pooled connections, query cancellation in Postgres (`CancelRequest`), request coalescing, micro-batching, CoDel, AIMD, GCRA, or deterministic simulation. Those come from the project's other influences; Release It! (via these notes) contributes the review checklist, the four-part row, the fault catalog, steady state, the timeout table, health-path isolation and drain.
