# mini-gateway — chunk 7: Pool — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c07-pool`.
Worktree: `/root/Software/mini-gateway-work/c07` — branch `work/c07-pool`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — they bind you.
2. `PLAN.md`: "Chunk 7 — Pool" (your scope — every deliverable, test and observability
   item there is required unless this brief says otherwise); decisions D3, D7, D8 (as
   rewritten by ruling 9: reset after every lease; `on-switch` and the re-seed are config
   policies on the same code point), D10; sections 6.1, 6.2, 6.5, 6.6; 7.5, 7.6; 8.2 (the
   rows for cap and creation rate); 9.1, 9.2, 9.3; 10.3, 10.4; and chunks 9–13 (your
   consumers).
3. **`docs/knowledge/release-it-pooled-connections.md` — the mining note. Its part 3
   (G1 to G10) is part of this brief**, with the rulings in "The mining note: what is
   adopted" below. Part 4 lists what is not adopted.
4. `docs/extension-seams.md`, `docs/knowledge/walking-skeleton.md` and
   `agent_work/chunks/c06-walking-skeleton/NOTES.md`: the seams you replace are
   `Pool` (`FixedSet` today), `Breaker` (`ClosedBreaker`), `ReleasePolicy`
   (`ResetAlwaysPolicy`) in `internal/proxy/seams.go`; the reset, its judge and deadline
   already exist in `internal/proxy/lease.go` — you extend that one code point, you do not
   build a second reset.
5. For the release policies: `agent_work/chunks/c05-classifier/OPTIONS.md` (options A and
   B and their residues) and `agent_work/chunks/c05-classifier/lease-exp/REPORT.md`
   (what crossed tenants under each policy on real Postgres 17, and the random-seed
   residue that the re-seed removes).
6. `docs/knowledge/foundation-gates.md`.

## Purpose

The connection pool that is the point of the project: a hard cap that holds under a
storm, a limit on how fast connections are opened, sane handling of connections that go
bad — including the ones that die without either end being told — and the release
policies.

## Deliverables

### `internal/pool` — the core (deterministic, sans-I/O, no Postgres types; add to the determinism list)

Everything PLAN.md's chunk 7 lists: accounting with open + connecting ≤ cap; a cap on
concurrent connects and a creation rate; idle timeout and maximum lifetime with jitter;
lend the most recently used idle connection (G3); the check before reuse (G4) with its
`checking` state off the request's path; the `suspect` rule (G6); the single breaker
(database-down detection from connection-class failures, connect lockout, one trial when
half-open, immediate `53M03` while open; a zero-reply timeout sets `suspect` but does not
by itself count toward the breaker); the full dirty-connection table (cancel, bounded
wait, rollback, then the reset of D8, reuse or close); the release policies
`lease.reset: always | on-switch` (a last-tenant tag per connection; under `on-switch`
prefer a free connection last held by the same tenant, and reset on the checkout path
when it must switch) and `lease.reseed: off | on`; the idle-in-transaction deadline;
client cancel requests mapped to the lent connection with a cap on concurrent cancels;
the separate cap on clients in startup; fleet division of the connection and
creation-rate budgets (section 8.1: the config gives the fleet total and N; each instance
takes its share, rounding down, never zero).

The core sees connections as ids and results as the work vocabulary (`internal/work`);
it imports neither `internal/pgwire` nor `pgproto3` (importlint holds this).

### Wiring (smallest possible edits outside `internal/pool`)

- `internal/proxy`: implementations of `Pool`, `Breaker` and `ReleasePolicy` backed by the
  core, selected by config; the check, the re-seed and the rollback are sent through the
  existing database-connection path (`dbconn.go`/`lease.go`), each with its deadline from
  the timeout budget; closing never waits for the peer (G5). `FixedSet` stays as the
  comparison arm for tests. If a seam interface must grow, grow it additively and keep
  the pass-through implementations compiling.
- `internal/shell`: TCP keepalive on database sockets from config (G2), the server-side
  keepalive startup parameters, cancel requests on a second connection.
- `internal/config`: new keys in their own section(s) with validation that fails closed
  naming the key (G2's `tcp_keepalive.idle ≤ path_idle_limit / 2`,
  `pool.idle_timeout < path_idle_limit`, `pool.check_timeout` < statement deadline), and
  rows in the generated timeout and capacity documents.
- `internal/obs`: a pool source mapping (`internal/obs/poolsrc`), catalog entries, health
  `degraded` while the breaker is open.

### The mining note: what is adopted

- **G1 (fault modes)** adopted: `blackhole`, `peer_forgot`, `idle_drop(D)`, close on a dead
  connection, server slots held. simnet already has `FaultHalfOpen` (a blackhole on one
  side), `FaultReset`, `FaultStall`; the fake has `FaultHalfOpen` and a connection limit.
  Build each mode from those where you can, in a test-side fault driver; `idle_drop` and
  the reaping of held server slots probably need additive simulator changes — follow
  lesson 9 below.
- **G2 (keepalive)** adopted, both directions, with the table's defaults and validation.
- **G3, G4, G5, G6, G7** adopted as written. G5's suggested row for "inside a transaction,
  mid-statement" is adopted: `FATAL 08006` when the message holds no `COMMIT` or `END`,
  `08007` when it does or the classifier cannot tell — add it to the error table through
  chunk 5's generator (the generated `docs/error-contract.md` changes; say so in NOTES.md).
- **G8** is already in the engine since chunk 6 (`EvDBWriteRefused`); keep it true for
  every new path (the check, the rollback, the re-seed, the cancel).
- **G9 tests 1 to 8** adopted; test 7's packet-drop rule is optional.
- **G10 (`TCP_USER_TIMEOUT`)** declined for v1: record it under stated limits with the
  note's reasoning.

## Tests (write these first)

All of PLAN.md's chunk 7 tests and the note's G9, in particular:

- Core property tests against a reference model with synthetic work units.
- **`FuzzPoolOps`**: the input decodes to pool operations and faults (checkout, release in
  each state, reset / check / connect results, deadlines, database lost, tenant switches),
  run against the reference model; bounded cost; constant seed; a planted ordering bug
  that it finds within the gate's 20 s (`scripts/fuzz-plant.sh` gains the plant).
- e2e in both tiers unless PLAN.md says simulator only: hard connection cap (1,000
  connects), reconnection storm against the chunk 2 baseline (tier R with client TLS on),
  slow consumer / half-open, cold start (dial rate ≤ cap; ablation: creation-rate cap off
  exceeds it), synchronized lifetimes, database restart (breaker), idle-in-transaction,
  cancel reaches the right statement and never another client's, and **session state
  under each release policy** — chunk 6's isolation scenario 4b re-run under `always`,
  `on-switch`, and each with the re-seed: zero crossings between tenants in all four,
  same-tenant carry-over present only under `on-switch`, the random-seed residue present
  without the re-seed and absent with it (tier R).
- G9: night trickle with each remedy alone and all off; silent mass death; failover;
  polite idle close; the check hangs; server slots still held; the tier R keepalive and
  empty-query checks; the harness self-test.
- Headline benchmark 1 (tier R): database connections under 500 clients, direct against
  proxy — the numbers go in NOTES.md.
- CSV-flush assertions (DuckDB SQL under `test/analysis/`, run in G7): open + connecting ≤
  cap in every flush and the fake's connection peak ≤ cap; the storm against the baseline
  run as a join; dials per interval ≤ the creation limit; completed-per-interval recovers
  after the restart; `backend_no_reply_total` = 0 in the night-trickle run with remedies on.
- One planted-bug canary per mechanism the reviewer will revert: the cap, the creation
  rate, the check deadline, the `suspect` rule, the tenant tag under `on-switch`.

## Observability obligations (landing gate — PLAN.md 7.6)

Pool snapshot as PLAN.md's chunk 7 and G7 list it (open, idle, lent, connecting, checking,
suspect; creation rate; closes by reason including `check_failed`, `peer_closed_idle`,
`idle_error`, `no_reply`; exhaustion events; breaker state and transitions; dirty-table
outcomes; resets by policy and outcome, re-seeds, tenant switches; cancels; checks by
result; lease-time and `idle_age_at_lend` histograms; `backend_no_reply_total`). No tenant
label on any of them. Counters move and conserve (every checkout ends in exactly one of:
lent, refused; every connection opened ends in exactly one close reason).

## Claims this chunk makes (an adversary will try to break each)

1. **The cap holds**: open + connecting ≤ cap at every instant, under any interleaving of
   dials, failures, closes and storms; a connection being closed counts until it is closed.
2. **The creation limits hold**: concurrent connects ≤ its cap and dials per interval ≤
   the creation rate, including after a breaker trial and a mass death.
3. **No synchronized expiry**: with jitter on, connections created together do not expire
   together (no dial spike above the creation rate; stated bound).
4. **Nothing is lent in doubt**: a connection is lent only when its last status was `I`,
   it has no unread bytes, its release action completed (reset, or tagged same-tenant
   under `on-switch`), and, if idle ≥ `check_after_idle` or `suspect`, it passed the check.
5. **The check never costs a client**: a failed or hanging check ends at its deadline,
   produces no client error, and never delays a request beyond what the queue allows.
6. **One death, bounded damage**: after a silent mass death, client errors ≤ connections
   lent at that instant; idle ones are caught without a client error.
7. **The breaker**: opens on connection-class failures only (never on SQL errors or a lone
   zero-reply timeout), answers `53M03` at once while open with no dial, sends exactly one
   trial, and recovery causes no dogpile.
8. **Release policies**: zero session-state crossings between tenants under every policy;
   same-tenant carry-over only under `on-switch`; the random-seed residue gone with the
   re-seed on. (Postgres 17, the oracle `test/oracles/dirty-check.sql`.)
9. **The dirty table**: every way a lease can end (client vanished mid-statement,
   mid-transaction, idle-in-transaction deadline, write deadline, cancel) ends in reuse
   after a completed clean-up or in a close — never a leak, never a lend in between.
10. **Cancel**: reaches the statement of the client that asked, or nothing; never another
    client's statement; concurrent cancels ≤ their cap.
11. **Startup cap**: clients in startup ≤ its cap during a storm with TLS handshakes.
12. **Fleet division**: for every N and total, the instances' shares sum to ≤ the total and
    none is zero; validation refuses a total smaller than N.
13. **Closing never waits** for the peer, and no close holds up a lease, a release or the
    dispatcher.
14. **Replay and flush**: every scenario replays bit-identically per seed; the flushed
    files agree with the snapshots and the SQL assertions hold.
15. **`FuzzPoolOps`**: total bounded decoding, constant seed, finds the planted bug.

## Steady-state row

After any scenario quiesces: no connection in `checking`, no `suspect` flag, no timer,
lent = 0, open ≤ cap; the pool's memory is fixed by the cap.

## Size and the split rule

PLAN.md estimates about 1,500 product lines; with the mining note expect 2,500 to 3,000.
The builder rules' split rule applies from 5,000. If you must split, the order of
priority is: the core with cap, creation limits, breaker, check and `suspect`; the release
policies; the dirty table and cancel; G1's `idle_drop` scenarios last.

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
