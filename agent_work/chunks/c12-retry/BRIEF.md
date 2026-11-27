# mini-gateway — chunk 12: Retry policy — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c12-retry`.
Worktree: `/root/Software/mini-gateway-work/c12` — branch `work/c12-retry`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — they bind you.
2. `PLAN.md`: "Chunk 12 — Retry policy" (your scope — every deliverable, test and
   observability item there is required); decisions D2, D3, D7, D11; section 2 row 12
   (default OFF); 6.1, 6.2, 6.5 (the chunk 12 row), 6.6; 7.5 (Retries), 7.6; 8.1 (the
   retry budget's floor is divided by N; ratios are not), 8.2 (the retry-budget row);
   **9.1, including its last paragraph**, 9.2; 10.1 (Governor), 10.2 items 4 and 8, 10.3
   (Compounds: a retry on a path that can leak); 12.2 (step 9), 12.3; section 14 (rows
   "Retry-storm amplification", "No statement applied twice", "Database killed
   mid-statement; error-contract rows" — re-run here with the matrix complete — "Results
   through the proxy equal results direct", "Engine operation fuzzing").
3. `agent_work/chunks/c06-walking-skeleton/NOTES.md`: "The engine contract" (write
   reports, rule G8), "Seam contracts", decisions 6 and 7, "Fault × path matrix", stated
   limit 10, and the repair round's rows on "reached". Also `docs/knowledge/walking-skeleton.md`.
4. `docs/knowledge/pool-core.md`, `docs/knowledge/admission-queues.md` and
   `agent_work/chunks/c07-pool/NOTES.md` (decisions 5–7 on the breaker, "Fault × path
   matrix: rows added", "Join with chunk 8" decisions (a)–(e)). If these are not in your
   worktree, stop and say so.
5. `docs/knowledge/classifier.md` (the error table, how a row is added and the generated
   `docs/error-contract.md`), `docs/extension-seams.md`, `docs/knowledge/foundation-gates.md`.

## Purpose

Making sure the proxy never turns a partial outage into a retry storm — its own retries
are a deliberate, budgeted choice that is off unless switched on.

## The one fact a retry may rest on (binding)

The proxy may retry only when nothing of the request reached the database **for
certain**. As built that fact is `Failure.Reached == work.No`, and the engine sets it only
on the driver's explicit zero-byte refusal: `EvDBWriteRefused` at an acknowledged offset
at or before the request's first byte, with nothing of the reply relayed (`reachedFor` in
`internal/proxy/lease.go`). Once a write was handed to the driver, a close with no write
report, a deadline, or silence is "may have reached" and is never retried, under any
policy. Keep this true for every path you add.

## Where the plan's wording meets the code

1. The seam is `RetryPolicy.Retry(now, Failure) bool` with
   `Failure{Request, Tenant, Reached work.Tri, Row uint8, InTx bool}`, set through
   `proxy.Config.Seams.Retry` (nil keeps `RetryOff`). It is called at one place,
   `statementLost`, and today the engine only counts `RetryAsked` and then answers the
   error row: nothing is re-run. Running the retry is this chunk's wiring. There is no
   retry entry in the `Stage` list; PLAN's "lease and forward stages" are that call site.
2. PLAN 9.1 says the retry acts where the "reached the database" column is "No". As built
   a loss outside a transaction with nothing reached is answered with `lost-after-read`
   (`08006`), whose column says Yes for the general case (chunk 6, decision 7). The policy
   reads `Failure.Reached`, never the row's column.
3. Refusals by a gate or the queue (`53M01`–`53M05`, `0A000`, `57014`) never reach the
   seam, and PLAN does not ask for them to be retried. Do not add call sites for them.
4. "Never when the breaker is open": the breaker seam is `Breaker.Allow(now)`. On the
   pool-core path `Allow` is not a pure read — a refused arrival counts as demand and
   lets the next half-open period send its trial — and while half-open it admits.
5. One waiting place (join decision (a)): a request that needs a connection waits in its
   tenant's admission queue and nowhere else, and with the admission queue the engine
   never lends past the queue. A retried request that has to wait takes that table's
   exits and errors (`53M01`, `53M03`, `57014`, gone).
6. The pool core checks stale and suspect idle connections before lending, so a dead
   connection is usually absorbed without any client error. A not-reached failure arises
   only when a lent connection refuses the first write. Your scenarios must produce that
   by construction (in tier S `proxysim` reports the bytes a failing write accepted and
   refuses the unwritten rest at the close).
7. The fault × path matrix is not a generated document: its rows are tables in the notes
   of chunks 6 and 7. The error contract is generated from `ErrorRows` in
   `internal/pgwire` and held by a test.
8. `fleet.instances` exists in config; a remainder of a fleet division is reported by
   `File.Warnings()`, not refused (chunk 7, decision 14).

## Deliverables

### `internal/retry` — the core (deterministic, sans-I/O, no Postgres types; add to the determinism and race lists)

As PLAN.md's chunk 12: the `Policy` interface; **Off** (the default); **PreForwardOnce** —
at most one retry per request, only for failures the engine marks "nothing reached the
database", charged to a budget: each first attempt deposits a tenth of a token, each
retry withdraws one, the bucket is capped so credit expires, a small floor divided by N;
integers only (count in tenths). Never when the breaker is open; never holding a queue
slot or a connection while waiting; never past the request's deadline. A test-only
"retry everything" policy for the ablation, unreachable from the binary's config. It
imports `internal/work` and `internal/obs` only. Bounded work: one failure triggers at
most one decision and at most one retry.

### Wiring (smallest possible edits outside `internal/retry`)

- `internal/proxy`: a `RetryPolicy` implementation backed by the core; the re-run of a
  granted retry from `statementLost` (the failed connection is closed or cleaned as today;
  the request keeps its one client and its one reply); first attempts reported to the
  budget; `Engine.Invariants`, `Quiesce`, `Shutdown` and the snapshot conservation extended.
- `internal/pgwire` (chunk 5's generator): the error contract completed — every row
  states retryable-by-proxy or not; `docs/error-contract.md` regenerated. Say in NOTES.md
  that the generated document changed.
- The fault × path matrix completed: every row of chunks 6 and 7, and yours, states
  retryable-by-proxy or not, each with its test.
- `internal/config`: the policy key (default off), the bucket cap and the floor in a
  section of their own; the floor divided by `fleet.instances`, the ratio not; fail closed
  naming the key; zero or omitted means the default, negative is refused; rows in the
  generated capacity and timeout documents for what you add.
- `internal/obs`: a source mapping (`internal/obs/retrysrc`), catalog entries. Health is
  unchanged by retries.

## Mechanism row

Where it sits: at the loss of a statement, after the lease and forward stages. Numbers:
the deposit (one tenth per first attempt), the bucket cap, the floor ÷ N. What the caller
gets: the retry's outcome, or the original error row when the policy declines. Signal:
budget level, exhaustions, retries refused by reason.

## Tests (write these first)

All of PLAN.md's chunk 12 tests:

- Property tests of the budget against a reference model: retries ≤ 10% of first
  attempts plus the floor over any window; a quiet hour cannot bank a storm; a
  pathological signal (every request failing pre-forward) cannot push it past the bound
  (10.1, Governor). Equality cases: the bucket holding exactly one token; exactly at the cap.
- A per-core operation fuzz target (PLAN 6.1, 6.6; suggested name `FuzzRetryOps`): the
  input decodes to first attempts, failures with each `Reached` value and `InTx`, breaker
  states, deadlines and time steps, against the reference model; bounded cost with a
  test; a planted ordering bug (`scripts/fuzz-plant.sh` gains the plant).
- e2e: **retry storm** (spec suite 5; tiers S and R) — 30% database errors with
  aggressively retrying clients: with the policy off and on, the load reaching the
  database stays bounded, refusals are fast, and goodput returns after the errors stop.
  **Post-forward failures are never retried under any policy** (tier S) — the fake's
  write log shows no statement applied twice across a sweep with kills at every point of
  the conversation. A retry happens only on the matrix rows that allow it. Budget
  exhaustion is visible. Ablation: the test-only "retry everything" policy produces
  duplicate writes and amplification, proving the scenario can fail. Quiesce after each.
- Counters must move: the scenario with the policy on shows retries > 0, so it must
  contain zero-byte write refusals by construction.
- Section 14 re-runs: "Database killed mid-statement; error-contract rows" with the
  matrix complete; "Results through the proxy equal results direct" with the policy on.
- A config-defaults test: the default is off, and off never retries.
- CSV-flush assertions (G7): in every window of the run retries ≤ 10% of first attempts
  plus the floor, from differences between flushes; statements reaching the database
  bounded against the ablation arm; writes applied in the fake = distinct writes sent.
- Planted-bug canaries, one per mechanism a reviewer will revert: the `Reached == No`
  condition, the once-per-request bound, the budget withdrawal, the bucket cap, the
  breaker check.
- `Engine.Invariants`: a test that fails when your block is removed.

## Observability obligations (landing gate — PLAN.md 7.6)

Attempts, retries, budget level, deposits, withdrawals, exhaustions, retries refused by
reason. Conservation: every failure offered to the policy ends in exactly one of retried
or refused by one reason; the engine's `RetryAsked` agrees with the policy's own counts;
every retried request ends in exactly one client reply; deposits − withdrawals − credit
lost at the cap = level — on every exit path including `Shutdown`. Coverage-table row
(7.5, Retries). Health: unchanged by retries, held by a test.

## Claims this chunk makes (an adversary will try to break each)

1. **Off by default**: the default config selects Off, and Off never retries.
2. **At most one retry per request** under PreForwardOnce.
3. **Only when nothing reached the database for certain**: a retry happens only on
   `Reached == No` from an explicit zero-byte refusal; never on silence, a deadline, a
   partly accepted write or a relayed byte.
4. **No statement is applied twice** under any product policy, across kills at every
   point of the conversation.
5. **The budget bound**: over any window retries ≤ 10% of first attempts plus the floor;
   the cap makes credit expire; integer arithmetic only.
6. **Never when the breaker is open.**
7. **A waiting retry holds nothing**: no queue slot and no connection while it waits, and
   it never runs past the request's deadline (tested at equality).
8. **The storm stays bounded**: with the policy off and on, the load reaching the
   database is bounded against the ablation arm, refusals are fast, goodput returns.
9. **Exhaustion is visible and counted**; every decision is counted by reason;
   conservation between engine and policy holds on every exit path including `Shutdown`,
   and each request gets exactly one reply.
10. **The matrix is complete**: every error-contract row and every fault × path row
    states retryable-by-proxy or not, and a retry happens only on rows that allow it.
11. **The ablation fails**: "retry everything" produces duplicate writes and
    amplification, and the binary's config cannot select it.
12. **Fleet**: the floor is divided by N and the ratio is not; validation names the key.
13. **`Engine.Invariants` covers the retry accounting**, and a test fails if that block
    is removed; `Quiesce` passes after shutdown.
14. **Replay and flush**: bit-identical per seed; flushed files agree with the snapshots.
15. **The fuzz target**: total bounded decoding, no seed from a hash, finds the plant.

## Steady-state row

What grows: the budget level (capped); a request waiting to retry. What removes it: the
cap, the retry running, the deadline, the client leaving. Gauges: budget level, retries
pending. After quiesce: nothing pending, no timer of yours.

## Fleet row (PLAN 8.2, 12.3 item 10)

Write in NOTES.md: the ratio survives; the bucket floor multiplies and is divided by N.
Chunk 13 measures it at three instances.

## Size

PLAN.md estimates about 600 product lines. The builder rules' split rule applies.

## Open points for the builder to decide and record

1. What "the request's deadline" is as built (the engine has `timeouts.queue_wait` and
   `timeouts.statement`, no single per-request deadline).
2. Whether the retry runs at once or after a wait, and where it re-enters the stage
   order (which gates run again), consistent with point 5 above.
3. How "never when the breaker is open" is decided given `Allow`'s side effect.
4. Whether any in-transaction row is retryable-by-proxy (`Failure.InTx`; the
   transaction's earlier statements are gone with the connection).
5. Where the completed fault × path matrix lives (a generated document held by a test,
   or NOTES.md as in chunks 6 and 7).
6. Config key names; the default bucket cap and floor.
7. How the retried request's second failure is reported (which row the client gets).
8. Tier R: whether a zero-byte write refusal can be produced deterministically on real
   sockets. If not, tier R shows the storm bounded and no duplicate write, the retry
   counter's movement is shown in tier S, and Deviations says so.
9. How "refusals are fast" is asserted without the wall clock in tier S, and with what
   threshold in tier R.

## Wave C — you are one of three concurrent builds (binding)

Chunks 9 (fairness), 11 (coalescer) and 12 build side by side and land one at a time; the
later ones are merged onto the earlier. All three touch `internal/proxy` and
`internal/config`.

- Keep edits to shared engine files minimal and inside the seam you own (the retry call
  site). ADD (a new file, a new block at the end, a new key section); never reorder or
  reformat. Do not change another stage's defaults — not the queue's, the pool's, the
  breaker's or the coalesce seam.
- List every shared file you touch in NOTES.md under "Shared files": `internal/proxy/*`,
  `internal/config/*`, `internal/pgwire/errors*.go`, `internal/obs/catalog`,
  `internal/obs/enginesrc`, `internal/obs/simrun` tests,
  `test/e2e/{spec,simtier,realtier}.go`, `scripts/fuzz-plant.sh`, `scripts/chaos.sh`,
  `tools/lint-rules/*`, `agent_work/INDEX.md`. Generated documents are regenerated with
  `scripts/gen-docs.sh`, never hand-edited.
- List every change to an earlier chunk's test in NOTES.md under "Changes to earlier
  chunks' tests", each with its reason. Each is examined for weakening.
- New scenarios, SQL and gate extras go in files of your own (`test/e2e/c12_*.go`,
  `test/analysis/<scenario>/`, `agent_work/chunks/c12-retry/gate/extra.sh`).

## Lessons from wave B (binding)

1. **Time passes before the event.** Every operation first expires what is due at or
   before `now`, then applies the event. A retry due at the request's deadline does not
   run; a failure at the instant the breaker opens is not retried. Test at equality.
2. **Leftover bytes and silence prove nothing.** "Did not reach the database" is claimed
   only on `EvDBWriteRefused`; a false "No" is a duplicate write.
3. **Every listed test must be able to fail.** Prove the important ones by mutation in a
   scratch copy (`/tmp/zgw-c12/`) and record mutation and failing test in NOTES.md.
4. **No verdict depends on the wall clock.** Count retries, statements and write-log
   entries and compare virtual time (`docs/knowledge/foundation-gates.md`, G3 "counted").
   A scenario check must hold on every seed by construction: write in NOTES.md why it
   cannot race. Sweep each new scenario at 200 seeds before you report (`DST_SEEDS`,
   `DST_SEED_BASE`, then `scripts/csv-assert.sh`); confirm 200 distinct seeds ran —
   earlier runners cap seeds per call.
5. **The fast set stays under five minutes** (`scripts/ci-fast.sh`, `go test -short`):
   the storm and the kill sweep skip under `-short`, also where `internal/obs/simrun`
   runs them.
6. **Every mechanism has a fast regression test** (no Docker) that fails when it is removed.
7. **Fuzz**: the simulator seed is a constant or a dedicated prefix, never a hash of the
   input; an unplanted run passes for 30 s; the plant is found inside the gate's 20 s.
   A core target that does not import `internal/sim` leaves the pinned inventory in
   `agent_work/adversarial/cr01-shrink-fuzz-rules/` green; list every target with its seed
   source in NOTES.md. On Go's bare `context deadline exceeded` re-run once; never edit
   `fuzz.sh`.
8. **Scenario facts**: a breaker meant to act on a standing queue must open inside the
   queue rule's interval (100 ms by default) or the rule sheds first; the fake charges
   its service time to `DISCARD ALL` too unless the time is per kind; tier R shares one
   Postgres cluster.
9. **The simulator API is frozen** (`freeze-sim`): test-side wrappers first (chunk 7's
   `pathRelay`, chunk 6's `tamper.go`); an unavoidable change is additive and listed at
   the top of NOTES.md under "Simulator change requests".
10. **Nothing reader-facing mentions how the work was made**: metric help texts, the
    error contract's wording and generated documents describe the software only.
11. **The machine is shared (4 cores).** Run fast checks often
    (`go test ./internal/retry/... ./internal/proxy/...`, `GATE_ONLY=G0,G1,G2`) and the
    full `scripts/gate.sh c12-retry` ONCE at the end, detached, on a clean committed tree.

## Finish

As the builder rules say: `gofmt -l .` empty; build, vet and `go test -count=1 ./...`
green; your packages on the determinism and race lists; the gate at the build stage on a
clean committed tree (delete the generated `gate/result.json` if untracked); NOTES.md at
`agent_work/chunks/c12-retry/NOTES.md` complete — what you built, each open point decided,
EACH CLAIM WITH ITS TEST, mutation proofs, the 200-seed sweeps, the completed matrix,
shared files, changes to earlier chunks' tests, deviations, stated limits, commands, fuzz
targets, simulator change requests, index rows; a short knowledge note in
`docs/knowledge/`; the summary and a final line containing exactly AGENT-DONE.

## Orchestrator's decisions on two open points (binding)

- Open point 4: no failure inside a transaction is ever retried by the proxy, whatever
  `Failure.Reached` says: the transaction's earlier statements went with the connection.
  A test holds it for every in-transaction row of the matrix.
- Open point 5: the completed fault × path matrix is a generated document held by a test
  (the pattern of `docs/error-contract.md` and `scripts/gen-docs.sh`), not a table in
  NOTES.md; add its path to `tools/lint-rules/prose-paths.txt`'s deny list of generated
  documents.
