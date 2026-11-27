# mini-gateway — chunk 8: Admission queues — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c08-admission`.
Worktree: `/root/Software/mini-gateway-work/c08` — branch `work/c08-admission`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — they bind you.
2. `PLAN.md`: "Chunk 8 — Admission queues" (your scope — every deliverable, test and
   observability item there is required); decision D3 (no wait inside the pool: all
   waiting is in these queues) and the decisions on the two CoDel modes and adaptive LIFO
   in sections 2 and 3; section 6.4 (the invariants restated — anti-bufferbloat per mode);
   6.1, 6.2, 6.5, 6.6; 7.4 (label policy: the tenant label is bounded), 7.5, 7.6; 9.1
   (the shed rows), 9.2, 9.3; 10.3; and chunks 9 and 10 (your consumers: fairness replaces
   the dispatcher, the adaptive limit sets what it enforces).
3. `INFLUENCES.md` entries F2–F4 and the sources they cite for the two rules ("Fail at
   Scale" for the timeout rule; the CoDel paper and RFC 8289 for the control law; folly's
   detector and minimum-delay gauge) — including the classic bug list.
4. `docs/extension-seams.md`, `docs/knowledge/walking-skeleton.md` and
   `agent_work/chunks/c06-walking-skeleton/NOTES.md`: the seam you replace is `Queue`
   (`FIFO` today; `Push(now, tenant, id)`, `Pop(now)`, `Remove(id)`, `Len()`, `Cap()`) in
   `internal/proxy/seams.go`, and the queue-wait timer in the engine.
5. `docs/knowledge/foundation-gates.md`.

## Purpose

Deciding which waiting requests are still worth serving: per-tenant bounded queues with
two selectable shedding rules, and newest-first service while overloaded.

## Deliverables

### `internal/admission` — the core (deterministic, sans-I/O, no Postgres types; add to the determinism list)

As PLAN.md's chunk 8: a fixed-capacity ring per tenant, created on first use and removed
when a tenant goes quiet (the number of tenant queues is bounded by a named cap; beyond it
the arrival is shed with its row); the `QueueRule` interface with two equal
implementations selected by config per instance — the **timeout rule** (one last-empty
timestamp; the allowed wait is the short target while a standing queue exists, else the
interval) and the **control law** (sojourn above target for an interval enters dropping;
next drop after interval/√count, integer arithmetic only — no floating point); in both,
age is measured on the oldest item and expired items are swept from the head and answered
at once; adaptive LIFO (serve newest while the rule reports overload, with hysteresis);
overflow sheds the head, not the arrival; the minimum-delay figure as a 0–100 load gauge.
Simulator-only comparison arms, reachable from tests and the simulator's config only,
never from the binary's config: plain FIFO, folly's detector.

### Wiring (smallest possible edits outside `internal/admission`)

- `internal/proxy`: a `Queue` implementation backed by the core with a single default
  dispatcher (round robin over tenants arrives in chunk 9 — keep the dispatcher a small
  replaceable piece and say in NOTES.md where chunk 9 plugs in); shed requests are
  answered at once with their error-contract row (`53M01` for both a full queue and a shed by the rule, PLAN.md
  9.1; the reason is told apart in the counters, not in the SQLSTATE) and the correct end marker; the existing
  `FIFO` stays as the control arm. If the seam interface must grow (for the head sweep or
  the rule's verdict at pop), grow it additively and keep `FIFO` compiling.
- `internal/config`: `queue.rule: timeout | control-law`, targets, interval, capacity per
  tenant, the cap on tenant queues, LIFO hysteresis — validated, fail closed naming the
  key; rows in the generated timeout and capacity documents.
- `internal/obs`: an admission source mapping (`internal/obs/admsrc`), catalog entries,
  the bounded tenant label per 7.4, health `degraded` while any queue is shedding.

## Tests (write these first)

All of PLAN.md's chunk 8 tests:

- Core property tests for both rules against reference models; the classic bugs as named
  tests (the first burst after idle must not get the short timeout; LIFO without the head
  sweep buries stale requests; the control law's count and next-drop time across leaving
  and re-entering dropping).
- **`FuzzAdmissionOps`**: the input decodes to pushes, pops, removals, time advances and
  tenant churn, against the reference model, for both rules; bounded cost; a planted
  ordering bug found within 20 s (`scripts/fuzz-plant.sh` gains the plant).
- e2e: **anti-bufferbloat per mode** as section 6.4 restates it, against a FIFO control
  with the same seed that grows without bound; **10× overload** (depth ≤ capacity, memory
  flat, the backend never idled by shedding: served ≥ 95% of capacity); **the head-to-head
  table** — both modes and both comparison arms at 1.2×, 2× and 10× load: served-wait
  percentiles, depth, shed count, idle capacity (simulator, exact; produced by ONE DuckDB
  query over the flushed runs of every arm — this table goes in the book, so commit the
  query and its output under `test/analysis/admission-head-to-head/`); recovery after the
  overload ends. Ablation: with the rule off, the standing queue persists. Tier R:
  overload against real Postgres with `pg_sleep`-shaped service time.
- CSV-flush assertions (G7): queue-wait percentiles from the bucket rows per mode and
  load; depth equal at T and 4T; sheds by reason.
- Planted-bug canaries: the head sweep, the first-burst rule, the LIFO hysteresis.

## Observability obligations (landing gate — PLAN.md 7.6)

Per tenant (bounded label) and total: depth, capacity, oldest-item age, queue-wait
histogram, rule state, LIFO on/off and switches, sheds by reason, the load gauge.
Conservation: every push ends in exactly one of served, shed (by reason), removed (client
gone).

## Claims this chunk makes (an adversary will try to break each)

1. **Bounded**: depth ≤ capacity per tenant and tenant queues ≤ their cap at every
   instant; memory is fixed by the two caps; a quiet tenant's queue is removed.
2. **Anti-bufferbloat, timeout rule**: under sustained overload the served wait settles at
   or under the stated bound of that rule (section 6.4) while the FIFO control with the
   same seed grows without bound.
3. **Anti-bufferbloat, control law**: the same for the control law, with its own bound.
4. **No starvation of capacity**: shedding never idles the backend — served ≥ 95% of
   capacity at 10× load.
5. **The first burst after idle is not punished**: it gets the interval, not the short
   target, under both rules.
6. **Expired items never linger**: under LIFO and FIFO alike, an item past its allowed
   wait is answered at once from the head sweep; none is served late and none is buried.
7. **Overflow sheds the head, not the arrival.**
8. **Every shed is answered at once** with its error-contract row and a correct end
   marker, and counted by reason; conservation holds on every path.
9. **LIFO hysteresis**: the service order switches only at the stated thresholds and does
   not flap on a load that sits at the threshold.
10. **Integer arithmetic**: the control law uses no floating point and its next-drop time
    matches the reference model for every count up to the stated maximum.
11. **Recovery**: after the overload ends the rule leaves its overload state within the
    stated time and the wait returns to the uncongested level.
12. **The comparison arms cannot be selected by the binary's config.**
13. **The head-to-head table is reproducible**: the same seeds give the same table, from
    one query over flushed files.
14. **Replay and flush**: every scenario replays bit-identically per seed; flushed files
    agree with the snapshots.
15. **`FuzzAdmissionOps`**: total bounded decoding, constant seed, finds the planted bug.

## Steady-state row

After quiesce: every tenant queue empty and removed, no timer, the rule state back to
normal; memory fixed by the caps.

## Size

PLAN.md estimates about 1,300 product lines. The builder rules' split rule applies.

## Wave B — you are one of three concurrent builds (binding)

Chunks 6a (operator tools), 7 (pool) and 8 (admission queues) build at the same time in
separate worktrees and land one at a time. To keep the three apart:

- Touch only the files your brief names as yours, plus the smallest wiring edits it lists.
  Where you must edit a shared file (`internal/config`, `internal/proxy/seams.go` wiring,
  `cmd/mini-gateway`, `tools/lint-rules/*`, `scripts/gen-docs.d/`, `agent_work/INDEX.md`,
  `test/e2e` shared helpers), ADD (a new file, a new block at the end, a new key section)
  rather than reorder or reformat what is there, so the later landing merges cleanly.
- New e2e scenarios, SQL assertions and gate extras go in files of your own
  (`test/e2e/<chunk>_*.go`, `test/analysis/<scenario>/`, `agent_work/chunks/<chunk-dir>/gate/extra.sh`).
- The machine runs three builds: the full gate takes about 40 minutes alone and longer
  now. Run fast checks often (`go test ./<your packages>/...`, `GATE_ONLY=G0,G1,G2`), and
  the full `scripts/gate.sh <chunk-dir>` ONCE at the end, detached, on a clean committed
  tree.

## Lessons from chunks 5, 6 and CR-1 (binding)

1. **Time passes before the event.** In the engine, timers due at or before an event's
   time fire before the event is processed. Your core follows the same rule: a deadline
   that equals `now` has expired. Test at equality.
2. **Leftover bytes and silence prove nothing.** Bytes after a `ReadyForQuery` in the same
   read are unread bytes (close, never lend). "Did not reach the database" is claimed only
   on the driver's explicit zero-byte refusal (`EvDBWriteRefused`).
3. **Every fix and every mechanism has a FAST regression test** (plain `go test ./...`, no
   Docker) that fails when the mechanism is removed. A behaviour checked only in tier R is
   not covered. The reviewer reverts your mechanisms one by one.
4. **Real-server tests share one Postgres cluster** and `scripts/tier-r.sh` runs packages
   one at a time. Server-wide views see every database: filter by your own database or
   connection.
5. **Bound the cost of one fuzz execution** (operation count, sizes, virtual time), with a
   test; a simulation-level fuzz target takes its simulator seed from a constant, never
   from a hash of its input (PLAN.md 6.6), and must run above 100 executions per second or
   be reported as a seed sweep.
6. **The fuzz inventory is pinned** in `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`
   (15 simulator-level targets). Your new fuzz target makes two tests there fail until the
   orchestrator re-pins; you may not edit `agent_work/adversarial/`. List every fuzz
   target you add in NOTES.md with its seed source; those two tests
   (`TestAdv_6_GateSeedSweepCounts`, `TestAdv_7_InventoryAndHiddenHashImports`) are the
   only adversarial tests allowed to be red.
7. Go's fuzz tool sometimes ends a target with `context deadline exceeded` and no failing
   input. `fuzz.sh` fails closed on it. If your gate is red only for that, re-run the gate
   once and say so in NOTES.md; do not change `fuzz.sh`.
8. **A zero or omitted cap in config means its default; negative is refused naming the
   key.** Follow that for every key you add, and add each key to the generated timeout
   and capacity documents' source.
9. The simulator API is frozen (tag `freeze-sim`). If a scenario needs a fault the
   simulator lacks, first try a test-side wrapper (chunk 6 put a relay in front of the fake
   for bad reset replies). If a change to `internal/sim/...` is unavoidable it must be
   ADDITIVE (new fault kinds or options beside the old; every existing scenario's trace and
   digest unchanged, shown by a test), as small as possible, and listed at the top of
   NOTES.md under "Simulator change requests" with the reason — the reviewer and the
   adversary examine each one.

## Finish

As the builder rules say: `gofmt -l .` empty; build, vet and `go test -count=1 ./...`
green; your packages added to the determinism and race lists; the gate at the build stage
on a clean committed tree (delete the generated `gate/result.json` if untracked); NOTES.md
at `agent_work/chunks/<chunk-dir>/NOTES.md` complete — what you built, each open decision,
EACH CLAIM WITH ITS TEST, deviations, stated limits, commands, fuzz targets, simulator
change requests, index rows; a short knowledge note in `docs/knowledge/`; the summary and a
final line containing exactly AGENT-DONE.
