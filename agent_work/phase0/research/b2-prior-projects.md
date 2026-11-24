# B2 — Nick's own prior projects on this machine (Phase 0 research, Mini-ZGateway)

Research agent: Fable (claude-fable-5-2), 2026-11-24. All sources are local repos and brief
files, read-only. Nothing was modified, built or committed in any of them.

**Three facts that change how this bucket should be read:**

1. **`analytics-edge-gateway` (Project 2) and `zeromq-go` (Project 4) contain no product code.**
   Both stopped at "Phase 1 complete, awaiting Nick's sign-off" (their `STATE.md`). What exists
   is a `PLAN.md` each, C4 diagrams, and small feasibility probes under `agent_work/probes/`.
   The spec's phrase "reusable GCRA rate-limiter and workload-fuzzing patterns" / "reusable
   sans-I/O framing and backpressure simulator patterns" therefore means **reusable designs and
   probe results, not reusable or proven code**. Nothing in either plan has been through
   review, adversarial testing or a seed sweep.
2. **The only fully built and attacked simulator is the object store's** (`internal/sim`, landed
   `ceb5438`, tag `freeze-0b`), and it is an event-loop simulator with **no goroutines** in the
   simulated code. Mini-ZGateway's hard parts (pool, singleflight, cancellation, deadlines) are
   goroutine-shaped, which is the zeromq-go problem, solved there only as a 272-line probe.
3. **mansio holds the closest working code to this spec** — nested per-database / per-tenant /
   per-VM admission, deadline-aware shedding, a retry budget, a generated retry contract, a
   caller budget below a Postgres pool. The brief framed mansio as "isolation as the unit"; it
   is more than that, and it is measured.

Standing rule confirmed in files: each project implements its own code. zeromq-go `PLAN.md`
§8.1: "Those ideas carry over from the object store's harness; none of its code does."
mini-durable-objects `AGENTS.md`: "do not share its code or assume its conventions."
The spec itself: "Implement ... from scratch in Go."

---

## 1. analytics-edge-gateway (Project 2) — PLAN ONLY

### AEG-1 GCRA / Token Bucket design (integer, `now` passed in)
- **Source:** `/root/Software/analytics-edge-gateway/PLAN.md` §7.2, §2.2 "Rate limiter", §4 Chunk 2
- **Verified?** read directly — that file (§2, §2.2 rate-limiter table, §2.4–2.9, Chunk 2, §6, §7.1–7.4), `STATE.md`, `agent_work/INDEX.md`. No implementation exists to read.
- **Key idea:** GCRA keeps one `int64` per tenant, the Theoretical Arrival Time (TAT), updated with one CAS. Token Bucket keeps `(tokens, last)`. Both use integer nanoseconds and a rational rate `(limit, periodNs)`, so the admission bound can be checked exactly against a `math/big` oracle. The limiter never reads a clock: `limiter.Allow(now, n)` returns `(ok bool, retryAfterNs int64)`.
- **What Mini-ZGateway would concretely borrow (fairness, testing):** the API shape `Allow(now int64, n) (ok, retryAfterNs)`; no floats; non-monotonic `now` clamped to last seen; an idle tenant is evictable once `TAT <= now` (so the tenant table can be bounded); and the test list: "over any interval [t0,t1] admitted <= burst + rate*(t1-t0)" against a big-rational oracle, "TAT never moves backward", "full burst recovers after idle >= burst/rate", "Token Bucket == GCRA decisions for equal (rate, burst), unit cost" as a differential test, no overflow at rates from 1/hour to 1e9/s with `now` near `MaxInt64`, 0 allocs per `Allow`. Planned adversarial targets worth reusing: rounding accumulation over 1e12 ops, coprime rate/burst pairs, eviction resurrecting a full burst early, tenant-table growth under a high-cardinality tenant flood.
- **What NOT to copy:** the code — there is none, and the design is unreviewed. A rate limiter alone does not meet this spec's fairness invariant: the spec sheds "preferentially from tenants exceeding their fair share **when backend saturation is detected**", which needs in-flight accounting (see MAN-1), not only a rate. The 1M-tenant benchmark and lock-free CAS are that project's theme, not this one's.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE — take the integer/`now`-as-argument contract and the exact-bound oracle; treat it as a design to be proven here for the first time.

### AEG-2 Workload / DST harness conventions
- **Source:** same `PLAN.md` §2 and §2.1; probe `agent_work/probes/sim-determinism/`
- **Verified?** read directly — `PLAN.md` §2–2.1 and the probe's row in `agent_work/INDEX.md` (result as recorded; I did not re-run the probe)
- **Key idea:** three rules. Sans-IO primitives (time is an `int64` argument, randomness an explicit `*rand.Rand`). "One op decoder, three drivers": one function decodes bytes into an operation sequence, driven by the seeded PRNG (property test), the fuzz engine, and `testdata/` regressions, so a failure found by one replays in the others. A trivially correct reference model per structure, compared op by op.
- **What Mini-ZGateway would concretely borrow (testing, benchmarking):** named forked streams (`env.Fork("traffic")`, `env.Fork("faults")`) so a new draw does not shift other streams; pinned seeds by default with opt-in sweeps; the exact failure line `REPLAY: <SEED_VAR>=<seed> go test ./<pkg> -run '^<TestName>$' -count=1`; an event loop keyed `(timeNs, seq)` with FIFO tie-break where **a rejected client schedules its own retry as an event** — that is what makes the spec's retry-storm scenario simulable instead of a fixed trace; generators `Zipf`, `Burst`/`RetryStorm` (including synchronised jitter-free retries and N events in one virtual nanosecond), `Mix`; the meta-test table (`TestReplayIdenticalStreams`, `TestGoldenDigests`, `TestForkIsolation`, `TestDiffCatchesPlantedBug`, `TestReplayLinePrinted`, `TestZipfShape`). The same generators also drive real processes in E2E and benchmarks.
- **What NOT to copy:** `math/rand` v1 (`rand.NewSource`) — the shared-sim investigation recommends `math/rand/v2` PCG instead (see OBJ-1). `HighCardinality` generator and ddmin shrinker are sized for that project's data structures; a ddmin shrinker is optional here. The plan's "primitives need no event loop" stance fits a limiter or a CoDel state machine, not the pool or coalescer.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE — adopt conventions and generators as ideas; use rand/v2; keep "honest scope of the determinism claim" (same commit, toolchain, GOOS/GOARCH; golden digests pinned for linux/amd64 only because float maths such as Zipf may differ on arm64).

### AEG-3 Benchmark-rigor rulings
- **Source:** same `PLAN.md` §2.4; kb entry `vm1-no-hardware-perf-counters.md`
- **Verified?** read directly — both
- **Key idea:** state the machine's limits before claiming anything, commit raw output, and narrow a claim that does not come out in your favour. Allocation gates are tests (`testing.AllocsPerRun == 0`), not observations.
- **What Mini-ZGateway would concretely borrow (benchmarking):** `-count=10` with median and spread from a small in-repo stats script (no `benchstat` dependency unless Nick approves); evidence benchmarks run **serialised under a machine-wide lock with builders idle** (kb says e.g. `flock`); a fixed-bucket log-linear zero-alloc histogram (`internal/benchutil` there) since `go test -bench` reports no percentiles; latency sampled separately from throughput where timer overhead matters; alloc gates skipped under `-race` via a build-tagged constant; the sentence "the repo ships the benchmark scripts so a reader can re-run them on real hardware."
- **What NOT to copy:** the cache-line / SWAR / "CPU cache efficiency by proxy" material — irrelevant to a proxy whose cost is syscalls and queueing. Note the plan has **no open-loop load generator and no coordinated-omission treatment**; its E2E clients are closed-loop. Nothing reusable there for `cmd/loadgen`.
- **Strength:** CORE (as rules)
- **Recommendation:** ADOPT — these are constraints of vm-1, not opinions.

## 2. zeromq-go (Project 4) — PLAN + PROBES ONLY

### ZMQ-1 Baton scheduler behind an `env` seam (vs `testing/synctest`)
- **Source:** `/root/Software/zeromq-go/PLAN.md` §2.1, §8.1, Appendix A; `agent_work/probes/phase1/batonsim/sim.go`, `syncrace/race_test.go`; kb `go-deterministic-goroutine-replay-baton-scheduler.md`
- **Verified?** read directly — plan sections and the kb entry in full; `sim.go` type and function list only (`Sched`, `G`, `timerHeap`, `Sched.Go/park/ready/dispatch/After/Sleep/Run`, `WaitQ.Wait/WakeAll`, `Half` (one conn direction), `Queue`); probes not re-run
- **Key idea:** `testing/synctest` virtualises time but does not order goroutines woken at the same instant: 4 goroutines sending on one channel gave all 24 orders in 2,000 runs. A baton scheduler runs exactly one simulated goroutine at a time and picks the next with the seeded PRNG at every blocking point; that gave one trace digest across 8 processes (GOMAXPROCS 1/2/4/8, plain and `-race`). A planted wall-clock decision gave 6 digests in 6 runs, so the check can fail.
- **What Mini-ZGateway would concretely borrow (testing; shapes pool, coalescer, admission):** the decision table itself (options A synctest-only / B everything single-threaded / C baton + seam) — this project must make the same choice, because the spec asks for a "single-threaded, seed-driven" harness **and** tests goroutine leaks, cancellation of 99 of 100 waiters and write deadlines. The costs of C, verbatim from §8.1: code below the seam may not use `go`, blocking channel operations, `select`, `sync.Cond`, `time` or `net` directly (a lint enforces it); mutexes must not be held across an `env` blocking call; a simulated run is serialised, so races are hunted in separate real-mode stress tests under `-race`. The digest design: fold every scheduler decision, delivered byte range, fault and reported state transition into SHA-256.
- **What NOT to copy:** the probe code (a 250-line sketch, "not the product simulator"). Unverified on darwin/arm64. Cost: ~530,000 scheduler steps/s; 100,000 messages in 1–10 byte chunks = 21.0 M steps ≈ 40 s — a 5,000 QPS tenant scenario with 1-byte fragmentation will be slow. Known trap: the per-goroutine wake channel must be buffered (cap 1) or a timer readying the dispatching goroutine deadlocks. The seam also conflicts with idiomatic `context.Context` cancellation, which the spec's coalescer rule is written in terms of.
- **Strength:** CORE (as a decision to make), not as code
- **Recommendation:** ADOPT WITH CHANGE — bring the three-option decision and probe evidence to Nick; my recommended resolution is in "Conflicts" below (sans-IO state machines for limiter / CoDel / pool accounting / coalescer bookkeeping, with a thin goroutine shell tested in real mode).

### ZMQ-2 Sans-I/O framing tests and the simulated connection
- **Source:** `PLAN.md` §2.1 (simulated `net.Conn`), §2.2, §2.3, Appendix A P7
- **Verified?** read directly — §2.1, §2.2; P7 result as recorded
- **Key idea:** the codec is tested against bytes, never sockets: golden vectors from the RFC, and every vector fed whole, 1 byte at a time, and at every single split point with identical output. The simulated connection is two independent directions, each with adversarial chunking, bounded buffers, latency, and faults.
- **What Mini-ZGateway would concretely borrow (protocol, testing — spec Commit 1 and §4.1):** the chunk-invariance property as both a table test and a native fuzz target (`FuzzChunkInvariance` shape proven on go1.26.8); rejection vectors that return typed errors and never panic; "the decoder never allocates ahead of the bytes it has received" (a RESP bulk length of 2^31 costs nothing until bytes arrive) plus a max-message-size option; the fault vocabulary for the virtual conn — FIN, RST, **half-open silent drop** ("writes succeed until the buffer fills, reads never return, nobody is told"), stall-then-resume, per-direction latency, deadlines on the virtual clock, chunking profiles "always 1 byte" / "split at these offsets" / "random 1..N". These map one-to-one onto the spec's "1-byte fragmented reads, TCP half-open drops, connection resets, asymmetric latency".
- **What NOT to copy:** ZMTP specifics; the libzmq interop harness (the analogue here is free: real `redis-cli` / `redis-benchmark` / a Redis container). Listener bind/re-bind lifecycle is more than a proxy with one backend needs.
- **Strength:** CORE
- **Recommendation:** ADOPT — closest match in the whole bucket to spec Commit 1.

### ZMQ-3 HWM / backpressure design and its DST
- **Source:** `PLAN.md` §2.5, §8.2, §8.3
- **Verified?** read directly
- **Key idea:** bounded per-peer pipes with a high-water mark; the bound that is asserted is not the bare sum but `SNDHWM + RCVHWM + T`, where `T` is a computed "transit term" (one message being written, one being read, plus what fits in the simulated conn buffers). A stalled consumer is isolated: healthy peers see identical virtual delivery times to a control run.
- **What Mini-ZGateway would concretely borrow (admission, pool, testing):** (a) state every bound with its transit term — "queue depth <= MaxQueueDepth" needs the in-hand-off requests named, or an adversarial seat will break it; (b) "zero added latency is exact in virtual time": compare healthy tenant B's per-request virtual latency against a control run without tenant A, instead of a tolerance; (c) zero does not mean unlimited — every limit is at least 1; (d) a pre-declared fallback: build the lock-free variant, keep the mutex ring as model and baseline, and if a lost wake-up survives the one repair round, land the mutex version and narrow the claim from "lock-free" to "bounded"; (e) goroutine model: one reader that owns the lifecycle plus one writer that exists only while connected, "easy to count, so the leak oracle is exact"; (f) blocking send vs non-blocking `ErrAgain` as two explicit modes; (g) the simtest oracles: goroutine leak (simulator registry empty **and** `runtime.NumGoroutine()` at baseline), liveness (stall = nothing runnable, no timers, goroutines alive → dump where each is parked).
- **What NOT to copy:** ZeroMQ's block-forever semantics at HWM. This spec wants the opposite default under overload — shed with a distinct error — so HWM is the hard cap behind CoDel, not the policy. SPSC ring detail is over-engineering for a proxy bounded by backend round trips.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE — borrow the bound-with-transit-term discipline, the control-run comparison and the fallback rule; not the queueing policy.

### ZMQ-4 Per-chunk gating as designed in the two plans
- **Source:** zeromq-go `PLAN.md` §2.13, §2.14, §7; analytics-edge-gateway `PLAN.md` §2.9, §6
- **Verified?** read directly
- **Key idea:** ordered landing gates G0–G10, each gate script with a canary that proves it fails on a planted violation; adversarial testing after the review cycle, so its findings are about claims; a BROKEN result is either repaired and re-run or the claim is narrowed in writing.
- **What Mini-ZGateway would concretely borrow (process):** gate order (hygiene incl. dependency check and seam/determinism lint → unit + pinned-seed sims → property → fuzz-short → race + real-mode stress → DST seed sweep + replay meta-test → adversarial → review → prose + comment-only AST check → acceptance + live-URL check); the BROKEN classification "claim overreach → narrow; real defect in a stated invariant → counts as the second FAIL"; "Briefs to Astra use plain testing vocabulary (checks, malformed input, disprove); an earlier project lost runs to a provider filter over attack wording"; every Neon brief ends with a done-marker because a budget cut-off can exit 0 silently.
- **What NOT to copy:** budgets as written — the two plans disagree (fuzz 3 min vs 30 s per target; 200 vs 500 seeds) and neither was run. Use the object store's measured budgets (OBJ-3).
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE — take the gate list; take numbers from the project that actually ran them.

## 3. object-store (built, landed, published)

### OBJ-1 Simulator kernel: `internal/env`, `internal/sim`, `internal/sim/simtest`
- **Source:** `/root/Software/object-store/internal/{env,sim}`; `docs/knowledge/chunk-0b-simulator.md`, `docs/knowledge/sim-kernel-extraction-note.md`; `/root/briefs/shared-sim-investigation.md`, `/root/briefs/shared-sim-ruling-note.md`
- **Verified?** read directly — the two knowledge entries, the investigation and ruling note in full; file listing and sizes of `internal/sim` (about 3,300 non-test lines) and the type list of `env/env.go` (`Clock`, `Timer`, `Transport`, `Disk`, `Process`, `Env`, `Time int64`); `simtest/determinism.go` and `seeds.go` signatures (`RequireDeterministic`, `Seeds`, `Sweep`). I did not read the loop code line by line.
- **Key idea:** a single-threaded discrete-event simulator that owns the world; protocol code is a pure core with no goroutines, locks, channels or wall clock, enforced by an AST lint (`detlint`) and cross-process replay tests. The kernel-shaped part is small: named PRNG streams, virtual clock and timers, `(time, seq)` queue, event log + SHA-256 digest, seed plumbing.
- **What Mini-ZGateway would concretely borrow (testing):** stream derivation `NewPCG(uint64(seed), fnv64a(label))` on `math/rand/v2` (`stream.go`, `NewStream`); project-neutral seed variables `-seed`, `DST_SEED`, `DST_SEEDS`, `DST_SEED_BASE`; `simtest.RequireDeterministic(t, seed, run)` (run twice, compare digests) and `Sweep`; determinism rules from `/root/briefs/objstore-p2-builder-rules.md` (no float arithmetic in a decision — use parts-per-million; no map range reaching an output; no `%p`, `maphash`, env reads); the freeze rule (after the simulator lands its exported API is frozen, later chunks file a "simulator change request" instead of editing); a stated virtual-time horizon (`sim.Horizon`, 2^62 ns); "the event log hashes payload bytes as they crossed the wire, before any handler runs".
- **What NOT to copy:** the code. The extraction note lists the tangles (clock math on the domain `node`, domain `evKind` enums inside the loop, a log that cannot be switched off). Disk, nemesis, node incarnations, clock skew/drift are irrelevant to one proxy and one backend. The "no goroutines, no mutex, no atomics" rule cannot hold for a connection pool as shipped. Also `pgregory.net/rapid` was allowed there for tests; this spec says zero external core deps — test-only deps need Nick's ruling.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE — reuse the kernel *conventions* exactly (so three repos read the same way), re-implement in-repo, and keep kernel files free of conn/backend types from day one.
- **RULING that carries over:** `/root/briefs/shared-sim-ruling-note.md` (Nick, 2026-11-21): "If feasible, let's have that be its own repo that going forward projects will import and extend it first." The investigation's verdict was "share a small simulation kernel ... do not try to grow one simulator across the projects", extraction to happen when the next project's Chunk 0 is briefed. `STATE.md` records "simulator kernel extraction is later, not now". **No kernel repo exists in `/root/Software` today.** This is an open question for this project (see steering Q1).

### OBJ-2 "Incremental e2e per chunk" rule
- **Source:** `/root/briefs/objstore-guide-e2e-wording-ask-2026-11-23.md` (Nick's ask), `/root/briefs/flow-guide-e2e-wording-draft.md` (Fable's draft wording), `docs/knowledge/chunk-07-e2e.md`
- **Verified?** read directly — the ask and the draft in full; the Pitfalls section of the knowledge entry. **The draft is marked DRAFT "for Nick (approval)"; I found no file recording his approval or that the Drive guide was edited.** The zgateway mission does list "incremental e2e per chunk" among standing-flow items, so the intent is in force; the exact wording may not be.
- **Key idea:** "end-to-end testing is incremental, never end-only. The final chunk is never the first time the system runs end to end." Every scenario has an owning chunk; the harness lands with the first runnable system; the final chunk is thin.
- **What Mini-ZGateway would concretely borrow (testing, chunk plan):** a scenario-to-chunk table mapping each of the spec's five chaos suites and three headline benchmarks to the earliest chunk that can run it (reconnection storm → pool chunk; hot-key stampede → coalescer chunk; noisy neighbour → fairness chunk; retry storm → errors/budget chunk). The harness (start proxy against the virtual backend **and** against a real Redis container, one passing scenario) lands with Commit 1's basic forwarding. Each scenario ships with a planted-fault canary and must prove it exercised its subject (non-empty fixture, the fault fired, the operation ran). "An operation that times out or loses its connection has an unknown result, not a failed one" — the same distinction as the spec's pre-forward vs post-forward errors. A short mode so the per-landing run stays affordable.
- **What NOT to copy:** nothing structural. Cost to accept: middle chunks get larger and gates longer.
- **Strength:** CORE
- **Recommendation:** ADOPT — it directly contradicts the spec's own roadmap, which puts `make chaos` / `make bench` in Commit 6 only.

### OBJ-3 Flow rulings and what went wrong
- **Source:** `STATE.md`; `/root/briefs/objstore-ci-fast-ruling-2026-11-22.md`, `objstore-chunk7-seat-swap-ruling-2026-11-22.md`, `objstore-neon-second-eyes-ruling-2026-11-22.md`, `objstore-publish-book-now-ruling-2026-11-23.md`, `objstore-p2-builder-rules.md`; `docs/knowledge/chunk-06-integration.md`, `chunk-07-e2e.md`, `chunk-0a-foundation.md` (Pitfalls / Narrowed-claims sections); `/root/briefs/zgateway-seat-ruling.md`
- **Verified?** read directly — those files (rulings read in their opening sections; `objstore-book-ruling`, `objstore-c4-ruling` and `objstore-lww-ruling` were **not** opened — their content is taken from `STATE.md` and the zgateway seat ruling, which restate them)
- **Key idea:** the seat arrangement (Fable orchestrates and gates, Opus writes logic with no comments, Neon reviews and writes all prose once and late, Astra writes independent harnesses) worked for five protocol chunks, then integration and e2e chunks needed arbitration, owner-ordered seat swaps and days of closing work.
- **Rulings that carry over (quote path):**
  - CI: `/root/briefs/objstore-ci-fast-ruling-2026-11-22.md` — "at most have ci run quicker tests and nothing really long ... this is learning project and i dont want consistent 30 min ci runs"; long runs stay as on-demand scripts; the local full gate on vm-1 remains the landing authority.
  - Seats, book, C4 for this project: `/root/briefs/zgateway-seat-ruling.md` (chapters go live at each landing; stale diagram = defect; C4 only after Nick steers on influences; Neon default route is Antigravity `agy`).
  - Builder rules template: `/root/briefs/objstore-p2-builder-rules.md` (tests first, canary bug per chunk, 85% coverage floor on primitives, panic allowlist, no comments, frozen simulator API).
- **Lessons / failures not to repeat (from the knowledge entries):**
  - "A clean small sample is not evidence": the first harness run held 284 of 284 checks, then review found a blocker and eight majors.
  - "A statistical check at sweep level false-fails a single-seed replay" — make checks exact per event.
  - "A check that passes on the old code proves nothing" — mutation-test every repaired hole.
  - "Counters and loops must be required non-zero" — a dozen checks passed over empty sets.
  - "Accepting any allowed error without requiring a success hides a dead feature." Directly relevant: a noisy-neighbour test that tolerates `OVERLOADED` for tenant B would pass with B fully starved.
  - "Package-level state in test or mutation code leaks between the two runs of `RequireDeterministic`" and shows up as "same seed gave different digests".
  - "A check that pipes `go list` into `grep` passes when `go list` fails" — gates must fail closed.
  - A 30 s fuzz run timed out once under machine load with no failing input; rerun passed — record reruns.
  - The reviewer seat can stop early with exit 0; require a final marker line.
  - Running the adversarial harness rewrites its tracked evidence files; commit before any gate run.
  - Do not commit to main between a chunk's final gate and its landing.
  - Open and unexplained: the real-process step failed only when run straight after the hour-long sweep.
- **What NOT to copy:** the mid-flight seat swaps were owner interventions, not a pattern. Docker-as-the-only-environment was that project's ruling (zeromq left it as an open question); a Redis container is needed here anyway.
- **Strength:** CORE (process)
- **Recommendation:** ADOPT — and size integration honestly: the two integration-shaped chunks cost more than the five primitives together.

## 4. mansio (built; working code)

### MAN-1 Nested admission caps: database <= tenant <= VM
- **Source:** `/root/Software/mansio/server/internal/front/limiter.go` (`AdmissionLimits`, `limiter`, `acquire`, `release`, `grantRunnable`), `internal/front/admission.go` (`Admission.Acquire(ctx, tenant, db) (release func(), err error)`)
- **Verified?** read directly — `limiter.go` lines 1–200; `admission.go` signatures only
- **Key idea:** a request runs only when its database, its tenant and the whole process are all under their in-flight caps; otherwise it queues if all three queues have room; otherwise it is shed and nothing is taken. The pure state (`limiter`) has "no locks, no clock, no channels touched"; a concurrent wrapper (`Admission`) adds the mutex, channels and clock.
- **What Mini-ZGateway would concretely borrow (fairness, admission):** the two-layer split (pure decision core + thin concurrent wrapper) — this is the sans-IO answer for the admission queue. The written invariant block: per-tenant `inFlight <= cap`, `queued <= cap`, `v.inFlight = Σ t.inFlight`, **work-conserving** ("no waiter could run now"), and "states with all counts zero are deleted (memory is bounded by active tenants)". The reason for the global cap, quoted: "Without the VM cap the process's in-flight count would be tenants × TenantInFlight, which is unbounded." Release order: the releasing tenant's oldest waiter first, then other tenants in global arrival order only if the global cap was full. Defaults there: tenant 256 in flight / 1024 queued; db 64 / 256; VM `4 × GOMAXPROCS × DBInFlight`, queue `4 ×` that. For this project the "database" level collapses (one backend) to tenant <= global, with global = the pool/concurrency limit.
- **What NOT to copy:** the code; the fixed caps — mansio's own note says they are "fixed numbers unrelated to the deadline", which is the gap CoDel and the adaptive limit fill. Static per-tenant caps are isolation, not fair share: a flooding tenant under its cap still takes slots. mansio docs do not use the phrase "noisy neighbour" for this (a grep found the word only for measurement noise); the framing is "caps nest db <= tenant <= VM", credited in the code comment to Release It.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE — the pure-core shape and invariants, with fair-share shedding added on top.

### MAN-2 Shed at admission what would expire in the queue
- **Source:** `server/knowledge/backpressure/2026-11-10-admission-would-expire.md`; code `internal/front/admission.go` (`drainRate`, `queueDrain`)
- **Verified?** read directly — the knowledge entry in full; code signatures only
- **Key idea:** when a level is at its cap, estimate when the new request would finish; if that is past its deadline, refuse immediately with a retry hint instead of letting it queue and time out.
- **What Mini-ZGateway would concretely borrow (admission, errors):** the rule, as written: `wait = (queued ahead + 1) / completions per second`; `finish = wait + in flight / completions per second` (Little's law); completions counted over the last whole second, per level, slowest level decides; `finish > deadline` → shed now with `wait` as Retry-After, counted under its own reason `would_expire`. Fallback to plain queueing when there is no deadline or no measured rate. Measured there (2 slots, 50 ms/request, offered 200/s, 300 ms deadlines): before 52 served / 203 expired after the full deadline / deepest queue 61; after 55 served / 0 expired / 203 shed (slowest refusal 71 µs) / deepest queue 9. This is a before/after table of exactly the kind the spec's README proof wants.
- **What NOT to copy:** it needs client deadlines; RESP carries none, so Variant A would need a configured per-request deadline. Stated weakness: "The estimate is noisy when the rate is small or changing"; a burst from idle is not judged for a second. It overlaps CoDel's job; carrying both needs a reason.
- **Strength:** SECONDARY
- **Recommendation:** WATCH ONLY (promote if Nick wants a deadline-aware variant to compare with CoDel) — keep the measurement format either way.

### MAN-3 One generated retry contract, and a token-bucket retry budget
- **Source:** `server/knowledge/backpressure/2026-11-10-retry-contract.md`; `docs/retry-contract.md` (generated); `server/internal/retry/retry.go` (`Budget`, `First`, `TryRetry`, `BudgetAllowance`)
- **Verified?** read directly — knowledge entry in full, first 30 lines of the contract, `retry.go` lines 176–232
- **Key idea:** every answer the server can give is one row in a table: code, status, Retry-After yes/no, what the client does (done / retry / reroute / fail), whether it is charged to the budget, and **"a mutation may have applied" yes/no**. The table is generated from code and both clients run it as test vectors.
- **What Mini-ZGateway would concretely borrow (errors+retries):** the table shape as this project's pre-forward vs post-forward contract. mansio's rows map directly: `shed` 503 + Retry-After "nothing ran" (pre-forward, retry safe, charged) vs `expired` 504 "ambiguous=true: a write may have committed", **final for the client library, not retried**. The budget mechanism: "each first attempt adds Ratio tokens, each retry takes one, and the bucket holds at most Burst" so "over any window retries <= Burst + Ratio × first attempts in that window"; "Credit therefore expires: a quiet hour banks at most Burst retries for the storm that follows"; the zero bucket starts full; `BudgetAllowance = 10`. That is a precise form of the spec's "retries capped at 10% of active requests" (Ratio 0.1), with the window problem solved.
- **What NOT to copy:** the code; float `spent` with a `1e-9` tolerance — in a simulated decision path use integers (ppm). HTTP status codes and request-id idempotency do not exist in RESP; Variant A needs its own error strings. Recorded deviation worth knowing: charging a server-paced reroute (`moving`) to the budget made a gated test fail (4 of about 15,600 requests) — pacing signals from the proxy itself should not spend the budget.
- **Strength:** CORE
- **Recommendation:** ADOPT WITH CHANGE — contract table generated from code and held by tests; integer budget.

### MAN-4 Bounds in front of a real pool; refuse before reading; one slot type; capacity table
- **Source:** `server/knowledge/backpressure/2026-11-10-controld-pool-budget.md`, `...-edge-admission.md`, `...-ops-pre-admission.md`, `...-capacity-table.md`; `server/internal/slots/slots.go` (`Pool.TryAcquire/Release/SetLimit/InUse/Shed/Stat`)
- **Verified?** read directly — the four entries (first ~1,200 characters of each, which covers the rule and the measurement); `slots.go` signatures
- **Key idea:** callers must be budgeted **below** the backend pool, or the excess waits invisibly inside the pool and times out. Check everything cheap before doing anything expensive, and hold nothing while a slow client sends.
- **What Mini-ZGateway would concretely borrow (pool, admission, observability):** the failure it fixed — "256 resolve slots + 16 admin slots sat in front of a 20-connection pool ... the callers beyond 20 waited inside pgxpool for up to the call timeout"; the fix — a narrow non-waiting gate sized under the pool, "Callers that join a load already in flight take no slot" (coalesced waiters must not consume admission or pool slots); a config-parse-time budget check (sum of gates < pool size); the fleet rule "N × (max_conns + 1) under Postgres's max_connections" (this spec's fan-in formula); "The check only looks; it holds nothing while the client sends" — a slot held across a client read lets a slow sender pin it (the spec's slow-consumer suite); one counted, resizable, non-queuing slot type instead of ad-hoc channel semaphores; a capacity document **generated from defaults by a test** with one row per queue: what it protects, capacity and config key, full-queue policy, deadline, metric, runtime lever; per-queue metric families `queue_depth`, `queue_capacity`, `queue_rejected_total{reason}`, `queue_wait_seconds`. Memory ceiling stated as `max_in_flight × max_body`.
- **What NOT to copy:** pgxpool, Prometheus rule files, admin levers over HTTP — out of scope for a stdlib-only learning proxy. mansio is HTTP; RESP pipelining changes what "before reading the body" means.
- **Strength:** CORE
- **Recommendation:** ADOPT — "bounded everything" as a generated, test-checked table is the cheapest strong proof in this bucket.

### MAN-5 mansio determinism ports and simulator
- **Source:** `server/internal/det/clock.go`, `rand.go`; described in `/root/briefs/shared-sim-investigation.md` §2
- **Verified?** read directly for `det` signatures (`Clock{Now, NewTimer, NewTicker, AfterFunc, WithDeadlineCause}`, `det.WithTimeout(ctx, clock, d)`, `NewRand(seed)` over PCG, `IDGen`); **secondary only** for `server/internal/sim` (6,191 lines, "doors for goroutine ordering") — the investigation's account, I did not open it
- **Key idea:** a small injectable clock/rand/id seam that production code takes as a value, including **context deadlines driven by the injected clock**.
- **What Mini-ZGateway would concretely borrow (testing):** `WithTimeout(ctx, clock, d)` / `WithDeadlineCause` — the piece zeromq's baton plan lacks. It lets coalescer and pool code keep idiomatic `context` cancellation while deadlines run on virtual time.
- **What NOT to copy:** `time.Time`-based clock (the other projects ruled `int64` ns); the mansio simulator is "mansio-specific throughout".
- **Strength:** SECONDARY
- **Recommendation:** ADOPT WITH CHANGE — the clock-driven context idea only.

## 5. sqlite-internals

### SQL-1 sqlite-internals / walfleet (ch10a, ch10b and neighbours)
- **Source:** kb `sqlite-internals-ch10a-findings.md`, `sqlite-internals-ch10b-findings.md`; repo is `/root/Software/walfleet` (its `STATE.md` is titled sqlite-internals). `/root/Software/sqlite-internals-work` is an **empty directory**.
- **Verified?** read directly — the first three corrections of ch10a and the first of ch10b via `kb show`; walfleet `STATE.md` head. kb search hits for ch06/ch08 and `go-sqlite-drivers-cancellation-and-threads` read as summaries only.
- **Key idea:** single-writer fencing for per-database SQLite files on an S3 store, and a seven-step relocation. It is about ownership of a file, not about multiplexing clients onto a server.
- **What Mini-ZGateway would concretely borrow:** **nothing for the pool, WAL or single-writer path — it does not bear on a storage proxy in front of Redis or Postgres.** Two side facts transfer as habits only: (1) count file descriptors per unit of capacity honestly (3 fds per idle WAL connection; 7 per open database in the service) — the proxy analogue is fds per client and per backend connection against `ulimit -n` in the 500-client and 1,000-connect tests; (2) cancellation latency is a property to measure, not assume (kb summary: 0.1–0.2 ms vs 1–17 ms across drivers) — relevant to "cancel after 5 ms".
- **What NOT to copy:** fencing, epochs, WAL shipping.
- **Strength:** BACKGROUND
- **Recommendation:** EXCLUDE — say so plainly; the spec's roadmap did not name it either.

## 6. Skimmed for relevance

### DST-1 dst-research technique ladder (and TigerBeetle VOPR through it)
- **Source:** `/root/Software/dst-research/findings/_technique-ladder.md`, `_taxonomy.md`, `findings/tigerbeetle.md`; `/root/Software/tigerbeetle` (clone at `0450c32b2`, 2026-10-14)
- **Verified?** read directly for the ladder tiers A–E (rungs 1–24) and the head of `findings/tigerbeetle.md` (§1–2); **secondary only** for TigerBeetle itself — I confirmed `src/vopr.zig`, `src/testing/packet_simulator.zig`, `storage.zig`, `marks.zig` exist in the clone but did not read them. Note the findings were written against commit `a2a3c70`, not the local clone's head.
- **Key idea:** 45 DST techniques from 21 codebases ordered simplest to hardest, each with the best teaching example. The VOPR runs the real production code single-threaded with network, disk and clock swapped for seeded models.
- **What Mini-ZGateway would concretely borrow (testing):** a checklist for Commit 0, by rung: 1 seeded PRNG passed explicitly, 2 sans-IO state machine, 5 seed replay, 7 `(time, seq)` queue with jump-to-next-event, 11 "no ready task and no pending timer means deadlock" plus a step budget, 12 run-twice fingerprint, 13 closing entropy leaks, 14 named streams, 15 golden hashes, 16 testing the tester (planted bugs, **coverage marks that must be hit** — answers the object store's "passed over empty sets" failure), 19 a fault budget (inject only what the system claims to tolerate), 20 reference model / fault-free control run, 23 environment interface, 24 deterministic executor. From the taxonomy: `faultline` is the closest Go analogue and reaches goroutine determinism through `-toolexec` rewriting plus `testing/synctest` plus lint — a fourth option beside ZMQ-1's three.
- **What NOT to copy:** tiers D storage faults (torn writes, bit-rot), linearizability checking, liveness modes for consensus — no disk and no cluster here. TigerBeetle's seed-as-git-commit CI and swarm options are overkill under the CI-fast ruling.
- **Strength:** SECONDARY
- **Recommendation:** ADOPT (as checklist) — cite the ladder; WATCH ONLY for TigerBeetle specifics, which bucket B4 should verify from source if it wants to cite them.

### Examined and excluded, or leads not followed

| Path | What it is (as read) | Verdict |
|---|---|---|
| `/root/Software/distributed-transactional-kv` | Git repo with **no commits** and no files | EXCLUDE — empty |
| `/root/Software/let-it-race-analysis` | Transcription/analysis of a book on observability vs control; `STATE.md` says "NO book project" | EXCLUDE — its patterns (append-only event store, derived views) are about data reconciliation, not proxy admission. One thin link: per-tenant metrics as the control signal, already in the spec |
| `/root/Software/walfleet` | The sqlite-internals course repo under its published name | EXCLUDE — see SQL-1 |
| `/root/Software/durable-actor` | 12-day DST course; days 1–2 done (`ManualClock`, virtual-time `Scheduler`) | EXCLUDE — strictly less than OBJ-1; its day plan is a nice ordering (virtual time → scheduler → seeded commands → simulated network → retries/deadlines) but adds nothing new |
| `/root/Software/mini-durable-objects` | 10-lesson course, lesson 1 in progress | EXCLUDE — no relevant content; its `AGENTS.md` restates "do not share its code" |
| `/root/Software/research` | `graphduck` (graph modelling chapters) | EXCLUDE — unrelated |
| `/root/Software/systems-trinkets` | Go lessons with local services; `software/` has compose files for `redis`, `valkey`, `postgres`, **`pgbouncer`**, **`toxiproxy`**; lessons `pipelining-work`, `cache-aside`, each with `measurements.csv` and a k6 `perf/` | WATCH ONLY — **listing and `knowledge/performance-labs.md` head only; lesson code not read.** Useful as a ready local Redis/PgBouncer/toxiproxy for a real-backend comparison leg, and `pipelining-work` may hold Nick's own measured pipelining numbers. k6 is closed-loop per VU by default; do not reuse it as the open-loop generator without checking. Its `AGENTS.md` rule "explain, don't fix" is for that repo only |
| `/root/Software/book-notes-work/backpressure-plan/books/backpressure-in-microservices/APPLICATION-PLAN.md` | The findings B1–B11 that MAN-1..4 implement, with book chapter references | LEAD, could not verify — file exists, **not read this run**. Bucket B4 or the orchestrator should read it: it is the map from a backpressure book to exactly the mechanisms above |

---

## Concrete measured facts worth carrying

| Fact | Source |
|---|---|
| Go on vm-1: `go1.26.8 linux/amd64` (ran `go version` this run) | this run |
| vm-1: 4 shared vCPUs ("DO-Regular"), no PMU — `perf stat -e cache-misses,cycles` reports `<not supported>`; software events work | kb `vm1-no-hardware-perf-counters.md` |
| vm-1: "4 cores and 7 GB of RAM"; builder limits used: no test over about 1 GB, `go test ./...` of a chunk under about 3 min, 500-seed sweep under about 10 min per package | `/root/briefs/objstore-p2-builder-rules.md` |
| Baton scheduler: about 530,000 steps/s; 21.0 M steps for 100k messages in 1–10 byte chunks ≈ 40 s | zeromq-go `PLAN.md` App. A P4 |
| `testing/synctest`: 24 arrival orders in 2,000 runs for 4 goroutines woken together | same, P6 |
| Object-store event-loop sim: about 230,000 events/s at 100 nodes; 500-seed sweep of the sim package about 100 s | `docs/knowledge/chunk-0b-simulator.md` |
| Object-store final gate at project end: race 50–54 min, 500-seed sweep 69–73 min; CI fast lane about 1.5 min | object-store `STATE.md` |
| `math/rand` v1 `rand.NewZipf` returns nil for s = 1.0 (needs s > 1); `math/rand/v2` has `NewZipf` on 1.26.8 | AEG `agent_work/INDEX.md`; shared-sim investigation |
| Would-expire shedding before/after table (52/203/0/61 → 55/0/203/9) | mansio knowledge, MAN-2 |
| No pip/venv on the host; Docker is the practical way to get a foreign peer. `pkill -f <pattern>` in an agent shell kills the calling shell — kill by PID | kb baton entry |
| Run `opencode` and `codex` headless with stdin closed (`</dev/null`); Neon via opencode cannot read files outside its worktree | object-store `STATE.md` |
| Claude sign-in on vm-1 ends between 2026-11-25 and 2026-11-28 | three `STATE.md` files, zgateway mission |

## Conflicts / tensions noticed

1. **"Single-threaded, seed-driven" simulation (spec §4.1, OBJ-1) vs testing the goroutine code that ships (spec §4.2 leak/cancellation invariants, ZMQ-1).** Event-loop only: cheap, exact, but the pool's and coalescer's real goroutine/`context` code never runs in the simulator. Baton + seam: runs shipped code and replays exactly, but bans `go`/`select`/channels/`time`/`net` below the seam, costs a scheduler step at every blocking point (21 M steps for 100k finely chunked messages; the two simulators' rates — 530k steps/s and 230k events/s — count different things and are not comparable), and exists only as a probe. *Recommended:* pure sans-IO cores (as in MAN-1's `limiter`) for limiter, CoDel/LIFO queue, pool accounting, coalescer bookkeeping and retry budget, driven by an event loop; a thin goroutine shell tested in real mode under `-race` with a clock-driven `context` (MAN-5); state the replay claim only for the cores. Decide the baton question explicitly rather than inherit it.
2. **Shared kernel repo (Nick's 2026-11-21 preference) vs "zero external core dependencies" and the "Commit 0 builds the simulator" story (spec).** Sharing gives one discipline attacked once; it adds a first-party `require` and a repo that does not exist yet. In-repo costs roughly 600–900 duplicated lines (the investigation's estimate, not a measurement).
3. **Static nested caps (MAN-1) vs CoDel + adaptive concurrency (spec).** Caps are provable invariants and trivially deterministic; adaptive limits need floats or careful integer maths and are harder to assert exactly. Caps should stay as the hard outer bound either way.
4. **Deadline-aware shedding (MAN-2) vs CoDel.** Both stop a standing queue from serving dead requests. MAN-2 needs deadlines and a rate estimate; CoDel needs only sojourn time. Carrying both is duplicate mechanism unless it is a deliberate comparison.
5. **Incremental e2e per chunk (OBJ-2) vs the spec's Commit 6 roadmap.** The rule makes pool/coalescer/admission chunks bigger; the spec's roadmap makes the last chunk the riskiest. The object store paid for the second choice with two days.
6. **Exact-in-virtual-time assertions (ZMQ-3) vs the spec's statistical thresholds (">99%", "within 2× baseline").** Object-store lesson: sweep-level statistics false-fail single-seed replay. Prefer exact per-run comparisons against a control run in simulation, and keep the percentage thresholds for the real-backend benchmark only.
7. **Benchmark honesty (AEG-3) vs the spec's headline targets ("~15× reduction", "p99 near 50 ms").** On 4 shared vCPUs with the load generator, proxy and Redis co-located, p99.9 claims will be noisy. Connection-count and backend-read-count claims are exact and machine-independent; lead with those.

## Variant implications

- **Toward Variant A (Redis RESP):** ZMQ-2 (compact streaming codec, chunk-invariance, golden vectors — and interop is free with `redis-cli`); AEG-2 generators (Zipf hot keys are a KV workload); MAN-1/MAN-3 cores are protocol-independent; systems-trinkets already has redis/valkey compose and a pipelining lesson.
- **Toward Variant B (Postgres wire):** MAN-4's pool-budget finding is literally a Postgres pool story ("N × (max_conns + 1) under max_connections"), and systems-trinkets has a `pgbouncer` compose file for a baseline. But nothing in this bucket has a Postgres wire parser or transaction-boundary logic; B would start from zero on the part that is hardest.
- **Only matter for C:** nothing in this bucket. The object store's routing/membership work is cluster-shaped, not replica-routing-shaped; do not import it.
- **Net:** this bucket supports the spec's recommendation of A. The prior work that transfers (sans-IO framing tests, admission cores, retry contract, DST conventions) is all protocol-light; A spends the budget where the prior lessons say the cost really is — integration and e2e.

## Steering questions this bucket creates for Nick

1. **Simulator kernel: own repo, or in-repo?** Options: (a) build `internal/sim` in this repo, kernel files kept separable (what zeromq-go planned); (b) create the shared kernel repo now with this project as first consumer, per the 2026-11-21 preference; (c) copy the kernel at a recorded SHA once another project has one. **Recommended: (a)** — no proven kernel exists to import except the object store's tangled one, and the spec forbids external core deps; revisit after Commit 0 is attacked.
2. **How do goroutines replay?** Options: (a) sans-IO cores + event loop, goroutine shell tested in real mode only; (b) baton scheduler + `env` seam for everything; (c) `testing/synctest` only, accepting non-identical traces. **Recommended: (a)**, with the replay law stated for the cores; (c) is ruled out by the probe.
3. **Scope of the determinism claim.** Options: same commit + toolchain + linux/amd64 golden digests, other platforms run-twice only (AEG wording) / cross-platform identical. **Recommended: the first**, with integer-only decisions so floats never reach a digest.
4. **Is a test-only dependency allowed?** (`pgregory.net/rapid` was ruled in for the object store; zeromq recommended none.) **Recommended: none** — stdlib `testing/quick`-style generators plus native fuzzing; the "one op decoder, three drivers" pattern covers it.
5. **E2E placement.** Options: per-chunk scenario ownership with the harness landing at basic forwarding / spec's Commit 6. **Recommended: per chunk**; Commit 6 becomes long-budget runs and assembly only.
6. **Fairness mechanism.** Options: (a) nested in-flight caps only; (b) GCRA rate budget only; (c) caps as hard bound + fair-share shedding when saturated (+ optional GCRA). **Recommended: (c).**
7. **Deadline-aware "would expire" shedding** — include as a comparison arm against CoDel, or leave out? **Recommended: leave out of v1**; record it as a known alternative with mansio's numbers.
8. **Benchmark claims on vm-1.** Options: full p99.9 headline tables / exact counts (connections, backend reads, shed attribution) as headlines with latency as supporting evidence plus rerun scripts. **Recommended: exact counts first.**
9. **CI.** Confirm the object-store CI-fast ruling applies here (fast lane only on push; long sweeps on demand; local full gate is the landing authority). **Recommended: yes.**
10. **Approve the incremental-e2e guide wording?** The draft at `/root/briefs/flow-guide-e2e-wording-draft.md` has no recorded approval. **Recommended: approve as drafted** so the Phase 1 plan can cite it.

## Verification log

| Path / command | Result |
|---|---|
| `/root/Software/mini-zgateway/docs/project-source.md` | read in full |
| `kb search` ×3 (ids 786, 787, 788) from `/root/Raw/knowledge` with the older binary | worked; feedback recorded `--useful` rank 1 for each |
| `kb show go-deterministic-goroutine-replay-baton-scheduler.md`, `vm1-no-hardware-perf-counters.md` | read in full |
| `kb show sqlite-internals-ch10a-findings.md`, `...ch10b-findings.md` | read in part (first corrections) |
| `analytics-edge-gateway/STATE.md`, `agent_work/INDEX.md` | read in full |
| `analytics-edge-gateway/PLAN.md` | §2, §2.1, part of §2.2, §2.4–2.9, Chunk 2, §6, §7.1–7.4 read; §3, other chunks, §5, §7.5+, §8, §9 not read |
| `analytics-edge-gateway/agent_work/probes/*` | not opened; results taken from INDEX.md |
| `zeromq-go/STATE.md`, `agent_work/INDEX.md` | read in full |
| `zeromq-go/PLAN.md` | §2.1, §2.2, §2.5, §2.8–2.10, §2.13–2.15, §7, §8.1–8.3, §10, App. A read; §3–6, §8.4+, §9, App. B not read |
| `zeromq-go/agent_work/probes/phase1/batonsim/sim.go` | declarations only (grep); probes not re-run |
| `object-store/STATE.md` | read in full |
| `object-store/docs/knowledge/chunk-0b-simulator.md`, `sim-kernel-extraction-note.md` | read in full |
| `object-store/docs/knowledge/chunk-06-integration.md`, `chunk-07-e2e.md`, `chunk-0a-foundation.md` | Pitfalls / Narrowed-claims sections only |
| `object-store/internal/sim`, `internal/env`, `simtest` | listing, line counts, declarations; bodies not read |
| `object-store/PLAN.md`, `AGENTS.md`, `book/` | not read |
| `/root/Software/object-store-work`, `/root/Software/sqlite-internals-work` | both empty directories |
| `/root/briefs/zgateway-orchestrator-mission.md`, `zgateway-seat-ruling.md` | read in full |
| `/root/briefs/shared-sim-investigation.md`, `shared-sim-ruling-note.md`, `objstore-p2-builder-rules.md`, `flow-guide-e2e-wording-draft.md`, `objstore-guide-e2e-wording-ask-2026-11-23.md` | read in full |
| `/root/briefs/objstore-ci-fast-ruling-2026-11-22.md`, `objstore-chunk7-seat-swap-ruling-...`, `objstore-neon-second-eyes-ruling-...`, `objstore-publish-book-now-ruling-...` | opening sections read |
| `/root/briefs/objstore-book-ruling-*`, `objstore-c4-ruling-*`, `gateway-*-ruling*`, `zeromq-seat-ruling.md`, `objstore-phase2-mission.md` | not opened (content known only as restated in STATE files and the zgateway seat ruling) |
| `mansio/STATE.md` | first 80 lines (latest section only) |
| `mansio/server/internal/front/limiter.go` | lines 1–200 read |
| `mansio/server/internal/retry/retry.go` | lines 176–232 read |
| `mansio/server/internal/{front/admission.go,slots,det}` | declarations only |
| `mansio/server/knowledge/backpressure/*.md` | admission-would-expire and retry-contract in full; capacity-table, controld-pool-budget, edge-admission, ops-pre-admission, durability-promise in part; eventlog-retention, queue-metrics, shutdown, running-the-gated-suites not read |
| `mansio/docs/retry-contract.md` | first 30 lines |
| `mansio/AGENTS.md`, `docs/knowledge/`, `server/internal/sim`, `internal/ratelimit`, `internal/breaker`, `mansio-work/*` | not read (`ratelimit` and `breaker` directories exist; a grep for their declarations returned nothing I recorded) |
| `dst-research/STATE.md` (head), `findings/_technique-ladder.md` (tiers A–E), `_taxonomy.md` (grep hits), `findings/tigerbeetle.md` (§1–2) | read as stated |
| `/root/Software/tigerbeetle` | file existence and head commit only |
| `let-it-race-analysis/STATE.md`, `walfleet/STATE.md` (head), `durable-actor/AGENTS.md` (head), `mini-durable-objects/AGENTS.md` (head), `systems-trinkets/AGENTS.md` (head) + listings + `knowledge/performance-labs.md` (head) | read as stated |
| `distributed-transactional-kv` | `git log` → "does not have any commits yet" |
| `research/graphduck` | listing only |
| `book-notes-work/.../backpressure-in-microservices/APPLICATION-PLAN.md` | exists; not read |
| Web | none used; this bucket is local-only |
