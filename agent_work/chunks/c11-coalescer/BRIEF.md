# mini-gateway — chunk 11: Coalescer — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c11-coalescer`.
Worktree: `/root/Software/mini-gateway-work/c11` — branch `work/c11-coalescer`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — they bind you.
2. `PLAN.md`: "Chunk 11 — Coalescer" (your scope — every deliverable, test and
   observability item there is required); decisions D2, D3, D4, D5, D7, D8 and **D10**;
   section 18 rulings 2 and 9; 6.1, 6.2, 6.4 (the coalescing row: "in-flight" counts reads
   running on the database), 6.5 (the chunk 11 row), 6.6; 7.3 (opt-in diagnostics), 7.5
   (Contention), 7.6; 8.2 (one read per hot key weakens to N); 9.1 (the `53M04` row), 9.2
   (the coalesced-flight row), 9.3 (flights, waiters per flight, shared-reply bytes); 10.1
   (Fail fast: the stage order), 10.3; 11 (headline 2); 12.2 (step 9), 12.3; section 14
   (the four chunk 11 rows, "Results through the proxy equal results direct", "Engine
   operation fuzzing", and the chunk 2 baseline row it is compared against).
3. `docs/knowledge/classifier.md`, `docs/safe-shape.md` and
   `agent_work/chunks/c05-classifier/NOTES.md` ("The work vocabulary", "The safe shape",
   "Stated limits").
4. `docs/knowledge/walking-skeleton.md`, `docs/extension-seams.md` and
   `agent_work/chunks/c06-walking-skeleton/NOTES.md` ("Seam contracts", "The reset",
   decisions 3–6, the deadline rule).
5. `docs/knowledge/pool-core.md`, `docs/knowledge/admission-queues.md` and
   `agent_work/chunks/c07-pool/NOTES.md` ("Join with chunk 8", decision (a); the dirty
   table; decisions 10–12 on cancels). If these are not in your worktree, stop and say so.
6. `docs/knowledge/foundation-gates.md`.

## Purpose

Turning a stampede of identical reads into one or two database reads — without ever
sharing a write, and without one caller's cancellation hurting another.

## What decides sharing (ruling 9 — binding)

Session state is reset after every lease, and safety never depends on reading SQL. For
this chunk that means: **the coalescer never looks at statement text to decide anything.**
`coalesce: auto` shares exactly the requests whose `work.Unit.Shareable` is true (the
lexer-free safe shape of chunk 5, outside a transaction, not rejected). `coalesce: hinted`
shares exactly the requests whose `Unit.Hinted` is true (the exact leading bytes
`/* mg:share */`), whatever their shape. `off` shares nothing. The key is `Unit.Key` as
the classifier produced it (`HasKey`; `Key.Text` is a copy of the exact text; `Key.Tenant`
is set only under tenant scope). Never derive "a read" from the lexer, from `Class`, or
from text of your own; never widen the safe shape. The flight's connection is reset after
its lease like any other.

## Where the plan's wording meets the code

1. The seam today is `Coalescer.Join(now, id, *work.Unit) bool` and `Leave(now, id)`
   (`NoCoalesce` is the pass-through; set through `proxy.Config.Seams`, nil keeps it). A
   two-valued `Join` cannot say "refused, `53M04`" or "you start the flight", and the
   engine simply returns after a true `Join`: nothing yet answers a waiter. Grow the seam
   additively, keep `NoCoalesce` compiling, and build the answer path in the coalesce stage.
2. The stage order is fixed: draining, breaker, rate gate, then the coalescer join, then
   the queue. A follower has passed the gates; it takes no queue slot.
3. The error row exists: `pgwire.ErrRowCoalesceCap` (`coalesce-cap`, `53M04`,
   `work.RefusalCoalesceCap`). Add no second row for the caps.
4. `proxy.Config.KeyScope` already feeds the classifier; `internal/config` has no key for
   it yet. Chunk 7 built `lease.reset: always | on-switch` in config, so the refusal of
   `on-switch` with an all-tenants key is a cross-check you add in `File.validate` (join
   decision (e): one validation, every error joined, each naming its key).
5. As built `Engine.Invariants` holds "each lent connection has exactly one client",
   "queue length = queued clients = waiting requests" and the conservation
   accepted = completed + failed + shed + queued + in flight. A flight's lease can outlive
   the client that started it, and a follower is neither queued nor in flight. Extend
   these checks for flights and waiters; delete none.
6. Chunk 6 relays reply bytes to the one client as they arrive (holding `ErrorResponse`
   and `ReadyForQuery` whole) inside `limits.reply_buffer`, pausing the database read when
   that client is slow. PLAN wants the reply stored once and a slow follower never
   delaying the others. How the two meet is an open point.
7. The dirty table exists only on the pool-core path (`Spec.Pool` in tier S; always in the
   binary): a lease ended mid-statement is cancelled on a second connection, drained and
   reset. The fixed set closes the connection. "All 100 cancel" runs on the core path.
8. One waiting place (join decision (a)): a flight that needs a connection waits in an
   admission queue and can leave by that table's exits — lent, `53M01` (rule, overflow,
   deadline), `53M03` (the breaker opens), cancel, gone.
9. Queue defaults will shed a 1,000-request burst in the ablation arm: `queue.capacity`
   and `queue.tenant_capacity` default to 256, the timeout rule's interval to 100 ms. Size
   the queue for the arm that must show 1,000 database reads (chunk 7's tier R headline
   did the same).

## Deliverables

### `internal/coalesce` — the core (deterministic, sans-I/O, no Postgres types; add to the determinism and race lists)

As PLAN.md's chunk 11: the flight table keyed by the unit's key; leader and followers; the
two join modes of D10 — **strict** (default: a request never joins a read already running
on the database; it joins the next one, which starts when the running one finishes) and
**relaxed** (joins the running read); a flight deadline independent of every waiter;
waiter reference counting — the database work is cancelled only when the last waiter
leaves; a caller that has left gets its own error even if a result arrives at that
moment; a cap on waiters per flight (`53M04` past it) and on the shared reply's size
(past it, followers are refused and the key is not shared again for a while — a bounded,
expiring list); shared errors; followers counted per tenant; entries removed on
completion. It imports `internal/work` and `internal/obs` only — neither `internal/pgwire`
nor `pgproto3` (importlint holds this). Fixed-size tables, integer arithmetic, bounded
work per operation: one arriving request triggers a bounded number of internal actions.

### Wiring (smallest possible edits outside `internal/coalesce`)

- `internal/proxy`: a `Coalescer` implementation backed by the core in the coalesce
  stage; the reply stored once as the database's message bytes and each recipient given
  its own end marker; the flight's deadline as an engine timer; waiters leaving on client
  cancel, client gone and shutdown; `Engine.Invariants`, `Quiesce`, `Shutdown` and the
  snapshot conservation extended.
- `internal/config`: `coalesce: off | auto | hinted`, the key scope (all tenants or one
  tenant), the join mode (`strict` default, `relaxed`), the caps and the no-share expiry —
  validated, fail closed naming the key; zero or omitted means the default, negative is
  refused; `lease.reset: on-switch` with an all-tenants key refused; rows in the generated
  timeout and capacity documents.
- `internal/obs`: a source mapping (`internal/obs/coalsrc`), catalog entries, per-tenant
  coalesced hits under the bounded tenant label (7.4).

## Mechanism row

Where it sits: the coalesce stage, after the gates and before the queue. Numbers: waiters
per flight, shared-reply bytes, flights, the no-share expiry, the flight deadline (the
statement deadline). What the caller gets: the shared reply with its own end marker, the
shared error, or `53M04`. Signal: overflows by cap, flights, hits, waiters per flight.

## Tests (write these first)

All of PLAN.md's chunk 11 tests:

- Property tests on the flight table with synthetic units against a reference model,
  both join modes.
- A per-core operation fuzz target (PLAN 6.1, 6.6; suggested name `FuzzCoalesceOps`): the
  input decodes to joins by key and tenant, leaves, flight start, result, error, reply
  bytes past the cap, deadline and time steps, against the reference model; bounded cost
  with a test; a planted ordering bug of the kind 6.1 names — a cancel against a coalesced
  waiter (`scripts/fuzz-plant.sh` gains the plant).
- e2e (section 14: tiers S and R): **at most one in-flight read per key** (exact,
  simulator); **writes are never coalesced** — every write in the workload appears in the
  fake's write log exactly once; **cancel 99 of 100** — the 100th waiter gets a valid
  reply; **all 100 cancel** — the database work is cancelled and the connection handled
  by the dirty table; **viral hot key** — 1,000 concurrent identical reads with 50 ms
  database latency: 1–2 database reads, p99 near one service time in relaxed mode and near
  two in strict mode, both reported, against chunk 2's direct baseline;
  **read-your-own-write** — a client that writes then reads the same key never gets the
  old value in strict mode, and the same scenario shows the window in relaxed mode;
  statements inside transactions are never shared; a slow follower does not delay the
  others; shared error on a failed flight, with no re-drive stampede. Ablation: coalescer
  off gives 1,000 reads. Recovery and quiesce after each.
- Headline benchmark 2 (tier R): database reads and p99 under Zipf skew — state the
  exponent and what the generator actually produces — with and without coalescing, both
  join modes side by side. The numbers go in NOTES.md.
- Section 14 re-runs: "Results through the proxy equal results direct" with `auto` on.
- CSV-flush assertions (G7): database reads for the burst ≤ 2 against 1,000 in the
  ablation arm (a join of two arms); flights + hits = shareable reads; waiters-per-flight
  totals.
- Planted-bug canaries, one per mechanism a reviewer will revert: strict join, the waiter
  count that guards the cancel, the leaver's own error, the waiter cap, the reply cap,
  `Shareable` as the only door under `auto`.
- `Engine.Invariants`: a test that fails when your block is removed.

## Observability obligations (landing gate — PLAN.md 7.6)

Flights started, hits, waiters-per-flight histogram, cancellations (waiter and flight),
overflows by cap, shared errors, table size; per tenant: coalesced hits; opt-in per-key
hotness, off by default (7.3), bounded. Conservation: every shareable request ends in
exactly one of flight started, hit, refused by a cap, or passed on unshared; every waiter
ends in exactly one of reply, shared error, own error, gone — and the engine's own
conservation balances with waiters counted, on every exit path including `Shutdown`.
Coverage-table row (7.5, Contention). Its health rule: see the open points.

## Claims this chunk makes (an adversary will try to break each)

1. **At most one in-flight read per key**: per instance, at every instant, counting reads
   running on the database.
2. **Writes are never shared**: under `auto` only `Shareable` units join; nothing inside
   a transaction joins; `off` shares nothing; every write reaches the database once.
3. **Sharing never reads SQL**: the decision uses the classifier's flags and key only;
   `internal/coalesce` imports no wire package.
4. **Strict is never stale**: no request receives the result of a read that was already
   running on the database when it arrived; read-your-own-write holds. Relaxed shows the
   window, and both are reported.
5. **One caller's cancel hurts no other**: 99 of 100 leave and the 100th gets a valid
   reply; the database work is cancelled only when the last waiter leaves.
6. **A leaver gets its own error** even when the result arrives at that same instant.
7. **All leave**: the work is cancelled, the connection goes through the dirty table,
   nothing leaks.
8. **The flight deadline** is independent of every waiter, fires at equality, and ends
   in a shared error.
9. **Caps**: waiters per flight (`53M04` past it), shared-reply bytes (followers refused,
   the key on a bounded expiring list), flights; memory is fixed by the caps.
10. **Shared errors, no stampede**: a failed flight gives each waiter the error once and
    the database reads for the burst stay bounded.
11. **Followers take no queue slot and no connection**, are counted per tenant, and the
    engine's conservation balances on every exit path including `Shutdown`.
12. **Every recipient gets a complete reply**: the database's message bytes and its own
    end marker; a slow follower delays no one; results equal results direct.
13. **Hot key**: 1,000 identical reads cost 1–2 database reads in both modes; the
    ablation arm costs 1,000.
14. **Config**: `off | auto | hinted`, scope, `strict` default; `on-switch` with an
    all-tenants key is refused naming the key; `hinted` shares only marked statements.
15. **Entries are removed on completion**; after quiesce the table and the no-share list
    are empty and no timer is armed.
16. **`Engine.Invariants` covers flights and waiters**, and a test fails if that block
    is removed.
17. **Replay and flush**: bit-identical per seed; flushed files agree with the snapshots.
18. **The fuzz target**: total bounded decoding, no seed from a hash, finds the plant.

## Steady-state row

What grows: flights, waiters, the stored reply, the no-share list. What removes them:
completion, the deadline, the last leaver, expiry. Gauges: table size, waiters, stored
bytes. After quiesce: all zero, no timer.

## Fleet row (PLAN 8.2, 12.3 item 10)

Write in NOTES.md: one read per hot key weakens to at most N reads per burst, because
flights are per instance. Chunk 13 measures it; leave the scenario runnable with N engines.

## Size

PLAN.md estimates about 1,100 product lines, ×2. The split rule applies; if you must
split, priority is: the core with both join modes, caps and cancel; the engine wiring and
the tier S scenarios; tier R; headline 2 last.

## Open points for the builder to decide and record

1. The default of `coalesce` (PLAN names `strict` as the default join mode and no default
   among `off | auto | hinted`). Tier S specs choose their seams, so earlier scenarios
   need not change; say what the binary does.
2. How the seam grows (refusal, leader, the answer path).
3. Whether the leader streams while the reply is stored, and what a follower gets when
   the reply passes the cap mid-flight.
4. Which tenant a flight's queue slot and lease are charged to, in each key scope.
5. The error row for a waiter that cancels (`57014` exists for "cancelled while queued").
6. What every waiter gets when the flight's queued request is shed (`53M01`) or refused
   by the breaker (`53M03`): PLAN says errors are shared.
7. Whether a strict-mode waiter for the next flight has a wait bound of its own beyond
   the running flight's deadline.
8. Whether the `on-switch` refusal applies when `coalesce` is `off`.
9. Default values of the caps and of the no-share expiry.
10. The health rule: PLAN's chunk 11 names none and 7.6 requires a statement.
11. How tier R makes "cancel 99 of 100" exact enough for a count-based verdict.

## Wave C — you are one of three concurrent builds (binding)

Chunks 9 (fairness), 11 and 12 (retry policy) build side by side and land one at a time;
the later ones are merged onto the earlier. All three touch `internal/proxy` and
`internal/config`.

- Keep edits to shared engine files minimal and inside the stage you own (coalesce). ADD
  (a new file, a new block at the end, a new key section); never reorder or reformat. Do
  not change another stage's defaults — not the queue's, the pool's, or the retry seam.
- List every shared file you touch in NOTES.md under "Shared files": `internal/proxy/*`,
  `internal/config/*`, `internal/pgwire` (if any), `internal/obs/catalog`,
  `internal/obs/enginesrc`, `internal/obs/simrun` tests,
  `test/e2e/{spec,simtier,realtier}.go`, `scripts/fuzz-plant.sh`, `scripts/chaos.sh`,
  `tools/lint-rules/*`, `agent_work/INDEX.md`. Generated documents are regenerated with
  `scripts/gen-docs.sh`, never hand-edited.
- List every change to an earlier chunk's test in NOTES.md under "Changes to earlier
  chunks' tests", each with its reason. Each is examined for weakening.
- New scenarios, SQL and gate extras go in files of your own (`test/e2e/c11_*.go`,
  `test/analysis/<scenario>/`, `agent_work/chunks/c11-coalescer/gate/extra.sh`).

## Lessons from wave B (binding)

1. **Time passes before the event.** Every operation of the core first expires what is
   due at or before `now` (flight deadlines, the no-share list), then applies the event.
   Test at equality: a result at the flight's deadline is late; a join at the instant a
   flight completes; a leave at the instant the result arrives.
2. **Leftover bytes and silence prove nothing.** Bytes after a `ReadyForQuery` in the
   same read are unread bytes (close, never store or lend).
3. **Every listed test must be able to fail.** Prove the important ones by mutation in a
   scratch copy (`/tmp/zgw-c11/`) and record mutation and failing test in NOTES.md.
4. **No verdict depends on the wall clock.** Count database reads and compare virtual
   time (`docs/knowledge/foundation-gates.md`, G3 "counted"). A scenario check must hold
   on every seed by construction: write in NOTES.md why it cannot race ("1–2 reads" must
   be a bound you can derive). Sweep each new scenario at 200 seeds before you report
   (`DST_SEEDS`, `DST_SEED_BASE`, then `scripts/csv-assert.sh`); confirm 200 distinct
   seeds ran — earlier runners cap seeds per call.
5. **The fast set stays under five minutes** (`scripts/ci-fast.sh`, `go test -short`):
   the 1,000-client scenarios skip under `-short`, also where `internal/obs/simrun` runs
   them.
6. **Every mechanism has a fast regression test** (no Docker) that fails when it is removed.
7. **Fuzz**: the simulator seed is a constant or a dedicated prefix, never a hash of the
   input; an unplanted run passes for 30 s; the plant is found inside the gate's 20 s.
   A core target that does not import `internal/sim` leaves the pinned inventory in
   `agent_work/adversarial/cr01-shrink-fuzz-rules/` green; list every target with its seed
   source in NOTES.md. On Go's bare `context deadline exceeded` re-run once; never edit
   `fuzz.sh`.
8. **Scenario facts**: the fake charges its service time to `DISCARD ALL` too unless the
   time is per kind (`fakepg.Service.PerKind`); a "vanish" is a reset, a FIN from a paused
   client is seen only at the next read; tier R shares one Postgres cluster (filter
   server-wide views by your own database).
9. **The simulator API is frozen** (`freeze-sim`): test-side wrappers first; an unavoidable
   change is additive and listed at the top of NOTES.md under "Simulator change requests".
10. **Nothing reader-facing mentions how the work was made**: metric help texts, error
    messages and generated documents describe the software only.
11. **The machine is shared (4 cores).** Run fast checks often
    (`go test ./internal/coalesce/... ./internal/proxy/...`, `GATE_ONLY=G0,G1,G2`) and the
    full `scripts/gate.sh c11-coalescer` ONCE at the end, detached, on a clean committed tree.

## Finish

As the builder rules say: `gofmt -l .` empty; build, vet and `go test -count=1 ./...`
green; your packages on the determinism and race lists; the gate at the build stage on a
clean committed tree (delete the generated `gate/result.json` if untracked); NOTES.md at
`agent_work/chunks/c11-coalescer/NOTES.md` complete — what you built, each open point
decided, EACH CLAIM WITH ITS TEST, mutation proofs, the 200-seed sweeps, headline 2's
numbers, shared files, changes to earlier chunks' tests, deviations, stated limits
(including the safe shape's: it trusts the objects it names), commands, fuzz targets,
simulator change requests, index rows; a short knowledge note in `docs/knowledge/`; the
summary and a final line containing exactly AGENT-DONE.

## Orchestrator's decisions on two open points (binding)

- Open point 1: the binary's default is `coalesce: auto` with the `strict` join mode. The
  plan's section on sharing treats `auto` as the norm and says `off` and `hinted` "exist for
  databases where that matters" (row-level security policies with side effects). State the
  default, and that limit, in NOTES.md under "Stated limits".
- Open point 8: the refusal of `lease.reset: on-switch` together with an all-tenants key
  applies only when coalescing is on (`auto` or `hinted`); with `coalesce: off` the
  combination is valid.
