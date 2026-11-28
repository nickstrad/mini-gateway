# mini-gateway — chunk 9: Fairness — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c09-fairness`.
Worktree: `/root/Software/mini-gateway-work/c09` — branch `work/c09-fairness`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — they bind you.
2. `PLAN.md`: "Chunk 9 — Fairness" (your scope — every deliverable, test and observability
   item there is required); decisions D1–D4 (D3: one waiting place; D4: the unit of
   admission is the transaction, so a held transaction is in flight) and D6 (the tenant is
   the startup user); section 2 row 4; 6.1, 6.2, 6.4 (the noisy-neighbour row), 6.5 (the
   chunk 9 row), 6.6; 7.2, 7.4, 7.5 (the Fairness row), 7.6; 8.1, 8.2 (the fair-share row);
   9.1, 9.3 (active-tenant states); 10.1 (Bulkheads), 10.3; 12.2 (step 9), 12.3; section 14
   (rows "Noisy-neighbour fairness", "Results through the proxy equal results direct",
   "Engine operation fuzzing"); and chunk 10 (your consumer: it sets the limit you enforce).
3. `docs/knowledge/admission-queues.md` and `agent_work/chunks/c08-admission/NOTES.md`
   (the core you extend; decisions 5, 6, 9–11, 15–17; stated limits 1, 5).
4. `docs/knowledge/pool-core.md` and `agent_work/chunks/c07-pool/NOTES.md`, sections "Join
   with chunk 8" (decisions (a)–(e)) and "Join repair round". If these files are not in your
   worktree, stop and say so in your summary.
5. `docs/knowledge/walking-skeleton.md`, `docs/extension-seams.md`,
   `docs/knowledge/foundation-gates.md`.

## Purpose

Keeping a quiet tenant fast while a noisy one floods: choosing whose request runs next.

## What you build on, as built

- `internal/admission` holds one fixed ring per tenant behind `Queues`. The tenant choice
  is already a seam: `admission.Dispatcher` with `Pick(now Time, active []TenantView) int`,
  installed by `Queues.SetDispatcher` (nil restores the default `OldestFirst`), reached
  from the engine through `proxy.AdmissionQueue.Core()`. `TenantView` carries `Index`,
  `Name`, `Depth`, `OldestSeq`, `OldestEnq`, `LIFO` for each non-empty tenant queue. A
  `Pick` outside the slice makes `Pop` return nothing. Inside the chosen tenant the
  ring pops newest or oldest by that tenant's own LIFO state; you do not change that.
- The engine's `dispatch` loops while the queue is non-empty and the pool has an idle
  connection: `Pop`, one `Checkout`, and `Unpop` when the pool refuses (frequent with the
  pool core: a check or a half-open trial may be pending). `Unpop` restores the ring
  position, enqueue time, empty time and rule state.
- The binary runs the pool core behind the timeout rule with adaptive LIFO. Tier S specs
  choose their seams (`Spec.Pool`, `Spec.Admission.Rule != 0`). Per-tenant rows and
  histograms are exported only with `obs.detail`.

### The join decisions (a)–(e) you must keep true

(a) A request waits in exactly one place, its tenant's admission queue. Open breaker: it
never reaches the queue (`53M03` at the gate, no push). Half-open, or closed with nothing
lendable: it is pushed and waits. Its exits, unchanged by this chunk:

| Exit | Answer | Counted (admission / engine) |
| --- | --- | --- |
| a connection is lent | the reply | `served` / `StagePassed[queue]` |
| the rule's allowed wait passed | `ERROR 53M01` | `shed{rule}` / `Shed` |
| tenant ring full, total full, tenant cap | `ERROR 53M01` | `shed{overflow,total,tenants}` / `Shed` |
| `timeouts.queue_wait` backstop | `ERROR 53M01` | `shed{deadline}` / `Shed` |
| the breaker opens or re-opens while it waits | `ERROR 53M03` | `shed{breaker}` / `Shed`, `BreakerFlushed` |
| client cancel | `ERROR 57014` | `removed` / `Failed`, `CancelsQueued` |
| client gone, or shutdown | none | `removed` / `Failed` without a reply |

Fairness adds no exit and no error row: a request held back because its tenant is at its
share stays in its tenant queue and leaves by one of these exits.
(b) `Engine.Shutdown(now)` order: sweep the admission core at `now` (due sheds answered
`53M01`); close every client (queued requests removed as client-gone, lent ones through the
release path); the pool core's shutdown; then the empty tenant queues are closed and
`tAdmission` cancelled. Your state must be empty after it and `Quiesce` must pass.
(c) `Engine.Invariants` runs chunk 6's checks, then `admissionCheck` (no unanswered shed,
core shed total = engine `Shed`, the timer armed at the core's `Next`, `Queues.Check`),
then `coreAgrees` and `pool.Invariants`. You add yours; you remove none.
(d) Defaults: the binary's queue rule is `timeout`; every tier R gateway gets
`RelaxedQueue` (interval 5 s, target 1 s) unless the test names its own queue, and a
tenant ring defaults to min(256, `queue.capacity`). A tier R fairness test that needs the
flooder shed must name its own queue config.
(e) One `File.validate` validates every chunk's keys and joins every error; no key of one
chunk constrains a key of another.

## Where the plan's wording meets the code

1. PLAN says chunk 8 ships "a single default dispatcher". As built it is `OldestFirst`
   over per-tenant rings, behind the `Dispatcher` interface above. You implement that
   interface; you do not replace `Queues.Pop`.
2. `TenantView` and `Pick` carry no in-flight numbers, and the core is never told when a
   lease starts or ends. The in-flight accounting needs additive inputs from the engine's
   lend and lease-end points (new methods beside the old; `OldestFirst` and `FIFO` keep
   compiling). A lease is not ended while the client's status is `T` or `E`, which is how
   held transactions count.
3. A pop can be undone. After `Unpop` your round-robin position and every counter must be
   as if that pop had not happened; a tenant must not lose its turn or gain in-flight count.
4. PLAN's "single shared queue" ablation arm: as built the one shared queue is `proxy.FIFO`
   (`Config.Admission.Rule == 0`); `OldestFirst` over the tenant rings is the other
   candidate. Use an existing arm; do not add a product config value for it.
5. PLAN 6.5 speaks of 10,000 tenants each with its own rows. As built per-tenant counters
   are kept for at most `MaxTenantStats` = 4,096 tenants (then `other`),
   `queue.max_tenants` defaults to 1,024, and the Prometheus label is bounded at 32. The
   many-tenants scenario must exceed 32; see the open points for its size.
6. PLAN's "removal of all-zero tenant state" applies to the state you add. Chunk 8's
   cumulative per-tenant counters persist by design (CSV counters never decrease).
7. The pool dials on `Demand(now, queue.Len())`, which counts requests you are holding back.

## Deliverables

### In `internal/admission` (deterministic, already on the determinism list)

As PLAN.md's chunk 9: the dispatcher — round robin across tenants with waiting work; a
tenant's share (limit ÷ active tenants, with optional weights) enforced only when the
limit is reached; work-conserving otherwise; per-tenant in-flight accounting that includes
held transactions; removal of all-zero tenant state (memory bounded by active tenants and a
named cap); the limit as one integer with a setter (chunk 10 will drive it). Integer
arithmetic only; fixed-size state; bounded work per `Pop` and per engine event — one
waiting request that cannot be released must not cause repeated pop/unpop or dial cycles.
Extend `Queues.Check`, `Snapshot`, `Quiesce` for the new state.

### Wiring (smallest possible edits outside `internal/admission`)

- `internal/proxy`: install the dispatcher when the admission queue is built; report lease
  start and lease end (every way a lease ends, including the dirty table, a lost
  connection and shutdown) to the core; extend `Engine.Invariants` with a block that holds
  your per-tenant in-flight total equal to the engine's own count of leases.
- `internal/config`: weights and any share keys in a section of their own, parse-time
  checks on shares, fail closed naming the key; zero or omitted means the default,
  negative is refused; rows in the generated capacity document for every cap you add.
- `internal/obs/admsrc`: the new metrics, catalog entries, the bounded tenant label (7.4).

## Mechanism row

Where it sits: inside the admission core, at the choice of the next tenant, between the
queue stage and the lease. Numbers: the limit, the weights, the cap on tenant state. What
the caller gets when it binds: nothing new — the request stays queued and takes an exit
of table (a). Signal: per-tenant time at share, dispatcher saturation time, rounds.

## Tests (write these first)

All of PLAN.md's chunk 9 tests:

- Property tests against a reference model: per-tenant in flight ≤ share under saturation;
  total = sum; work-conserving (no idle capacity while anyone eligible waits); no
  starvation; the same after `Unpop`, tenant churn and removal of all-zero state.
- A per-core operation fuzz target (PLAN 6.1, 6.6; suggested name `FuzzFairnessOps`): the
  input decodes to pushes per tenant, pops, unpops, lease starts and ends, held
  transactions, limit changes, tenant churn and time steps, against the reference model;
  bounded cost with a test; a planted ordering bug (`scripts/fuzz-plant.sh` gains the plant).
- e2e: **noisy neighbour** — tenant A at 10× capacity, tenant B steady and small. Tier S:
  B's outcomes compared exactly with B running alone on the same seed, with the three
  measures of 6.4 as exact comparisons on virtual time; tier R: the spec's thresholds
  (> 99% success, p99 within 2×, A takes > 95% of the shedding). Both require B's
  successes, not the absence of failures. **Many tenants, one flooder.** **A tenant
  holding transactions open** does not exceed its share. Ablation: with a single shared
  queue the quiet tenant starves. Recovery after the flood ends; quiesce.
- Section 14 re-runs: "Results through the proxy equal results direct" with the fair
  dispatcher on.
- CSV-flush assertions (G7, SQL under `test/analysis/<scenario>/`): per-tenant totals —
  the quiet tenant's completed count equals its count in the run where it is alone (a join
  of two runs); the flooder's share of all shedding > 95%; the many-tenants scenario
  checked per tenant from the files, beyond the Prometheus label bound.
- Planted-bug canaries, one per mechanism a reviewer will revert: the share bound, the
  round-robin step, held transactions counted, the `Unpop` restore.
- `Engine.Invariants`: a test that fails when your block is removed.

## Observability obligations (landing gate — PLAN.md 7.6)

Per tenant (bounded label): in flight, current share, served, shed share, time at share.
Dispatcher: rounds, saturation time. Durations are integer nanoseconds of virtual time;
ratios are left to the reader of the counters (7.1). Conservation: per-tenant in flight
sums to the total, and the total equals the engine's leases outstanding, on every exit
path including `Shutdown`. Its coverage-table row (7.5, Fairness). Its health rule: see the
open points.

## Claims this chunk makes (an adversary will try to break each)

1. **The share binds under saturation**: once the limit is reached no tenant is lent a
   connection that puts it above its share; in flight includes held transactions.
2. **Accounting**: total in flight = sum over tenants = the engine's leases outstanding
   at every event, on every way a lease ends.
3. **Work-conserving**: no lendable connection stays idle while an eligible request
   waits; below the limit the share does not bind.
4. **No starvation**: every tenant with waiting work is chosen within a stated number of
   rounds.
5. **Noisy neighbour, tier S**: on every seed B's outcomes equal B alone, B's p99 is
   within 2× of the control, A takes > 95% of the shedding, and B's successes are non-zero.
6. **Noisy neighbour, tier R**: the spec's three thresholds.
7. **Many tenants, one flooder**: every quiet tenant keeps its outcomes; shown per tenant
   from the flushed files.
8. **Held transactions**: a tenant holding transactions open does not exceed its share.
9. **The ablation fails**: with a single shared queue the quiet tenant starves.
10. **Bounded**: dispatcher state exists only for tenants with non-zero state and is
    capped; work per pop and per event is bounded.
11. **Shares are checked at load**: a bad weight or share fails closed naming the key.
12. **The join decisions hold**: one waiting place, the exits and errors of table (a)
    unchanged; `Unpop` leaves fairness state untouched; after `Shutdown` conservation holds
    and `Quiesce` passes.
13. **`Engine.Invariants` covers fairness**, and a test fails if that block is removed.
14. **Replay and flush**: every scenario replays bit-identically per seed; flushed files
    agree with the snapshots.
15. **The fuzz target**: total bounded decoding, no seed from a hash of the input, finds
    the planted bug.

## Steady-state row

What grows: per-tenant dispatcher state. What removes it: all of its counts reaching zero.
Gauge: active tenants. After quiesce: no tenant state, in flight 0, no timer of yours.

## Fleet row (PLAN 8.2, 12.3 item 10)

Write in NOTES.md the fair-share row with its condition: it survives if tenants are spread
evenly and weakens if a tenant's connections land on one instance, because shares are
computed among the tenants active on that instance. Measurement is chunk 13's.

## Size

PLAN.md estimates about 900 product lines. The builder rules' split rule applies.

## Open points for the builder to decide and record

1. The static value of the limit until chunk 10 (PLAN's chunk 10 bounds it by the pool size).
2. What "the limit is reached" means exactly, so that the share can bind at the instant a
   connection frees (then total in flight is below the pool size). Test at equality.
3. Rounding of limit ÷ active tenants and of weights; who counts as active.
4. Whether in flight ends at the lease's end or after its reset completes.
5. Which existing arm is the "single shared queue" ablation.
6. The size of the many-tenants scenario against the 4,096 and 1,024 bounds. Changing
   either constant is a change to chunk 8's defaults and must be listed with its reason.
7. Whether `Demand` should count held-back requests.
8. Config key names for weights and shares.
9. The health rule: PLAN's chunk 9 names none and 7.6 requires a statement.
10. Tier R for "many tenants" and "held transactions" (PLAN names tier R only for the
    noisy neighbour), and how the tier R p99-within-2× threshold is made robust on a
    shared host; it never runs in the fast set.

## Wave C — you are one of three concurrent builds (binding)

Chunks 9, 11 (coalescer) and 12 (retry policy) build side by side and land one at a time;
the later ones are merged onto the earlier. All three touch `internal/proxy` and
`internal/config`.

- Keep edits to shared engine files minimal and inside the seam you own (the dispatcher
  behind the queue stage). ADD (a new file, a new block at the end, a new key section);
  never reorder or reformat. Do not change another stage's defaults.
- List every shared file you touch in NOTES.md under "Shared files": `internal/proxy/*`,
  `internal/config/*`, `internal/obs/catalog`, `internal/obs/enginesrc`,
  `internal/obs/simrun` tests, `test/e2e/{spec,simtier,realtier}.go`,
  `scripts/fuzz-plant.sh`, `scripts/chaos.sh`, `tools/lint-rules/*`, `agent_work/INDEX.md`.
  Generated documents are regenerated with `scripts/gen-docs.sh`, never hand-edited.
- List every change to an earlier chunk's test in NOTES.md under "Changes to earlier
  chunks' tests", each with its reason. Each is examined for weakening.
- New scenarios, SQL and gate extras go in files of your own (`test/e2e/c09_*.go`,
  `test/analysis/<scenario>/`, `agent_work/chunks/c09-fairness/gate/extra.sh`).

## Lessons from wave B (binding)

1. **Time passes before the event.** Every operation first expires and accounts what is
   due at or before `now`, then applies the event. Test the equality cases (a lease ending
   at the instant the limit is reached; time at share measured to the instant).
2. **Every listed test must be able to fail.** Prove the important ones by mutation in a
   scratch copy (`/tmp/zgw-c09/`) and record mutation and failing test in NOTES.md.
3. **No verdict depends on the wall clock.** Count work and compare virtual time
   (`docs/knowledge/foundation-gates.md`, G3 "counted"). A scenario check must hold on
   every seed by construction: write in NOTES.md why it cannot race. Sweep each new
   scenario at 200 seeds before you report (`DST_SEEDS`, `DST_SEED_BASE`, then
   `scripts/csv-assert.sh`); confirm 200 distinct seeds ran — earlier runners cap seeds
   per call.
4. **The fast set stays under five minutes** (`scripts/ci-fast.sh`, `go test -short`):
   heavy scenarios skip under `-short`, also where `internal/obs/simrun` runs them.
5. **Every mechanism has a fast regression test** (no Docker) that fails when it is removed.
6. **Fuzz**: the simulator seed is a constant or a dedicated prefix, never a hash of the
   input; an unplanted run passes for 30 s; the plant is found inside the gate's 20 s.
   A core target that does not import `internal/sim` leaves the pinned inventory in
   `agent_work/adversarial/cr01-shrink-fuzz-rules/` green; list every target with its seed
   source in NOTES.md. On Go's bare `context deadline exceeded` re-run once; never edit
   `fuzz.sh`.
7. **Scenario facts**: the fake charges its service time to `DISCARD ALL` too unless the
   time is per kind (`fakepg.Service.PerKind`); a `Quiesce` right after traffic fails
   inside the tenant queue's 16-interval quiet window; tier R shares one Postgres cluster.
8. **The simulator API is frozen** (`freeze-sim`): test-side wrappers first; an unavoidable
   change is additive and listed at the top of NOTES.md under "Simulator change requests".
9. **Nothing reader-facing mentions how the work was made**: metric help texts and
   generated documents describe the software only.
10. **The machine is shared (4 cores).** Run fast checks often
    (`go test ./internal/admission/... ./internal/proxy/...`, `GATE_ONLY=G0,G1,G2`) and the
    full `scripts/gate.sh c09-fairness` ONCE at the end, detached, on a clean committed tree.

## Finish

As the builder rules say: `gofmt -l .` empty; build, vet and `go test -count=1 ./...`
green; the gate at the build stage on a clean committed tree (delete the generated
`gate/result.json` if untracked); NOTES.md at `agent_work/chunks/c09-fairness/NOTES.md`
complete — what you built, each open point decided, EACH CLAIM WITH ITS TEST, mutation
proofs, the 200-seed sweeps, shared files, changes to earlier chunks' tests, deviations,
stated limits (including that the share bounds database concurrency, not database CPU),
commands, fuzz targets, simulator change requests, index rows; a short knowledge note in
`docs/knowledge/` saying where chunk 10 sets the limit; the summary and a final line
containing exactly AGENT-DONE.
