# Lease lab — piece E1 notes: pool core, release policies, sizing rule (U1–U4)

Builder: Opus, 2026-11-26. Brief `/root/briefs/zgateway-lease-e1-pool-core.md`, rules
`/root/briefs/zgateway-lease-lab-rules.md`. Branch `exp/lease-lab`; not pushed.

Note on inputs: `lease-exp/CALIBRATION-NOTE.md` is not in this worktree. It is on main
(commit `5e91519`); I read it from there with `git show` and changed nothing on main.

## 1. What is built

Package `internal/lab/leasepool` — pure, deterministic, no I/O, integers only:

| File | What |
| --- | --- |
| `types.go` | Config (K1–K15 keys the core uses), `Event`, `Action`, every enum and label, `Snapshot` |
| `pool.go` | The pool core: `New(cfg)`, `Step(now, ev) ([]Action, error)`, `Snapshot()` |
| `rule.go` | Sizing rule R1 as its own type `Rule` (usable without the pool), `WStar`, fixed-point helpers |
| `poisson_table.go` | Generated exact threshold table (do not edit) |
| `poissongen/` | The generator (math/big) and `poissongen/gen`, its command |

**API.** One entry point: `Step(now int64, ev Event) ([]Action, error)`. `now` is integer
nanoseconds and must not go backwards. The returned slice is reused by the next call. An error
means a driver bug (time backwards, duplicate lease id, release of a lease that is not lent,
release status other than `I`/`T`/`E`, unknown event kind) and the pool is unchanged. Results
for a connection that is gone or in another state (a reply that lost a race with a timeout or
with "database lost") are *stale*: ignored and counted (`StaleEvents`). Nothing in the core
reads a clock, so a simulator loop and a wall-clock loop drive it the same way.

**Connection states:** connecting, priming, warm, lent, idle (tagged), resetting (a reset or,
with K2, a reseed in flight), closing. Each connection is in exactly one; the core keeps
per-state counts and U1 checks them against a scan at every event.

**Events:** request (lease, tenant) · release (lease, status, client gone) · connect done (ok,
or failed with a reason) · prime done · reset done · reseed done · close done · tick · database
lost. Replies carry `Failed`, `Tag`, `Status`; the core decides with `ReplyOK`: no error, the
expected tag (`DISCARD ALL` for the reset, `SELECT 1` for prime and reseed), then status `I`.
Anything else is not ok.

**Actions:** open · prime (`select1`) · lend (lease, conn, tenant, outcome, switch mode,
request time, match time) · reset · reseed · close (reason) · refuse (lease).

## 2. Decisions the brief left open

1. **Timeouts are inside the core.** K3 (`ResetTimeoutNs`) bounds every prime, reset and
   reseed; it is checked on tick, so its resolution is the tick (100 ms). On expiry the
   connection is closed (`reset_timeout`, `reseed_timeout`, `prime_timeout`). The driver arms
   nothing. Connect timeouts are the driver's: it reports `connect done, failed, timeout`.
2. **Close is valid in every state but connecting and lent.** The driver must abort whatever is
   in flight on that connection, close it, and report `close done` exactly once. A late reply
   for it is stale. The core never closes a lent connection and never closes a connecting one.
3. **Database lost:** every priming, warm, idle and resetting connection is closed
   (`db_lost`); every lent one is marked doomed and closed at its release whatever the status;
   connecting ones are left alone — the driver reports their connect result (a connect started
   after the loss may well succeed after a restart). Leases waiting on a checkout reset go back
   to the wait list.
4. **Connect retry:** after a failed connect no new open starts for `ConnectRetryNs` (default
   100 ms, one tick). Without it a refusing database plus an instant failure report is a
   zero-time loop in the simulator.
5. **Wait list:** one FIFO, capacity `WaitCap`. Capacity counts waiting leases plus leases
   matched to a connection whose checkout reset is still running (`pending`). A request that
   arrives with the list at capacity is refused (action `refuse`, counter `LeasesRefused`) —
   even one that could have been served at once. `WaitCap ≥ 1`. No lease times out of the
   list; the driver has no cancel event (none was asked for).
6. **Matching, per waiter in FIFO order, each step:**
   - affinity on: an idle connection last used by the same tenant (most recently released
     first); else a warm one (oldest ready first); else, under `none`, `reset-always`,
     `reset-on-switch`, an idle one of another tenant (least recently used first).
     Under `remake-on-switch` other tenants' idle connections are never taken.
   - affinity off: the oldest lendable connection by the time it became ready (warm or idle,
     any tag). Under `remake-on-switch` an idle one of another tenant is then closed
     (`switch`) and the lease takes the oldest warm one if any, else waits; each lease does
     this at most once, so one lease cannot close a chain of connections.
   - Ties go to the lower connection id. A later waiter may take a connection the head cannot
     use (a same-tenant idle one under `remake-on-switch`); the head is still covered by the
     provisioning below.
   - *(Repair round, §8.)* Under `remake-on-switch` a **patient** waiter (its tenant holds a
     lent or idle connection, and it has waited less than `remake.patience`) takes only an idle
     connection of its own tenant: no warm one, and with affinity off it closes nothing.
   - Under `reset-on-switch` a connection of another tenant is matched, reserved for the
     lease, reset (and reseeded with K2) and lent only when that finishes ok. If it fails the
     connection closes and the lease returns to its original place in the list.
7. **Provisioning (one rule for stock and for waiters).** After matching:
   `deficit = target + waiters − (warm + connecting + priming) − replacing`, where `waiters`
   leaves out patient waiters (repair round, §8), and `replacing`
   is the number of connections closing for a remake (`remake`, `switch`) when K12 is
   `close-then-open` and the warm mode is not `cold`. Open `min(deficit, free slots, K14 room)`
   (none during the connect retry). Under `remake-on-switch`, if the deficit is larger than
   the free slots plus the slots already being freed by closing connections, idle tagged
   connections are closed (`switch`), least recently used first, to make the room — that is
   the "head of the list is not starved" rule and the "closed when its slot is needed" rule in
   one. *(Repair round, §8:)* no more are closed than can open once confirmed: closes and
   connects in flight together stay within K14, and none while the connect retry is on. Under `dynamic` remake, warm connections above `target + waiters` are closed
   (`shrink`), oldest first.
8. **Warm target:** `cold` → 0. `none`, `reset-*` → W (`WarmSize`; W_min for `dynamic`)
   until the first time W connections are up (not connecting, not closing), then 0 for good:
   "prefill up to W at start", nothing more (no refill after a restart). `remake-*` →
   `fixed:W` W, `dynamic` the rule's target.
9. **`remake.order` (K12).** Every connection counts against the cap until `close done`, in
   both orders, so the cap holds as the brief demands. `close-then-open`: a connection closing
   for a remake is counted as the replacement under way, so its replacement opens only when
   its slot is confirmed free, even if another slot is spare. `open-then-close`: the
   replacement opens at once into a spare slot, and within one step every open is moved ahead
   of the step's first close. With `cold` there is no stock to replace, so K12 has no effect.
10. **Outcome label** (one of four, set at lend):

    | | connection never lent before | connection lent before |
    | --- | --- | --- |
    | matched in the step of its request | `warm_hit` | `reuse` |
    | waited (not matched in that step, or put back after a failed checkout reset) | `miss_connect` | `miss_slot` |

    So under `remake-always` every waiting lease is `miss_connect`, and `miss_slot` means
    "waited, then got a used connection". A `reuse` under `reset-on-switch` may still wait for
    its checkout reset; that wait shows in the switch mode `reset` and in `MatchedAt` vs lend.
11. **Switch mode** (at match): `connect` (never-lent connection and the lease waited),
    `warm` (never-lent, ready at match), `reset` (another tenant's, checkout reset), `ready`
    (another tenant's, lent with no checkout action: `none`, or `reset-always` whose reset ran
    at release), `none` (same tenant: not a switch). The plan's three modes are the first
    three; `ready` and `none` are extra so every lease has one.
12. **Driver's per-lend data:** `RequestedAt`, `MatchedAt` (when a connection was chosen; for
    `reset` the checkout reset starts there), the lend action's own time is `now`. So
    `lease_wait_ns = now − RequestedAt` and `switch_ready_ns = now − MatchedAt` (or from
    `RequestedAt` for `connect`; the driver's choice).
13. **Snapshot extras:** `Priming` gauge and `PrimingNs` integral (priming is a state the
    brief's list of integrals did not name; it has to be somewhere for the sum to hold).
    Reseed time counts as resetting. `Pending` gauge (leases waiting on a checkout reset).
    Release outcomes: clean, in_tx, failed_tx, client_gone, db_lost. Release actions: idle,
    reset, close. Resets: started, ok, failed, timed out. Closes counted by reason at issue,
    `ClosesDone` at completion. Connect failures by reason.
14. **Rule inputs.** *(Changed in the repair round, §8.)* "Arrivals that needed a fresh
    connection" = each lease counted once: when it takes a never-lent connection, or when it
    first counts toward the deficit (a waiter that is not patient). A patient waiter is not
    demand. With patience 0 this equals the old count (at the end of the request's step:
    waiting, or matched to a never-lent connection). Connect times: every successful
    connect, `connect done − open`. The rule runs only for `dynamic` under the remake
    policies; elsewhere `lambda_hat_milli`, `lead_hat_ns` and the change counters are 0.

## 3. The rule R1 (`rule.go`)

- **Units:** λ̂ is held in micro-arrivals per second (exposed as `LambdaHatMilli`, milli per
  second); L̂ in ns; m in micro-units (m × 10⁶). 64×64→128-bit `mulDiv` from `math/bits`.
- **EWMA:** per tick, sample x = arrivals / dt; s += (x − s)·dt/τ (truncated toward s; dt ≥ τ
  replaces s). τ_fast = 1 s, τ_slow = 10 s by default (K8), configurable. λ̂ = max of the two.
  The first tick only sets the clock.
- **L̂:** mean, or nearest-rank p95, of the last 64 connect times (`LeadWindow`); before the
  first sample `InitialLeadNs` (default 21 ms, the calibrated connect at 4 concurrent).
- **Tail convention:** a lease misses when all W replacements are under way, so
  P(miss) = P(N ≥ W), N ~ Poisson(m), and **W\* = smallest W ≥ 0 with P(N ≥ W) ≤ ε**. Since
  P(N ≥ 0) = 1, W\* ≥ 1 always (m = 0 gives 1). This reproduces the plan's examples
  (m = 0.8 → 4, m = 8 → 16 at 1%), tested.
- **Table:** for each ε, `poissonThrX[W−1]` = the largest m in micro-units with
  P(N ≥ W) ≤ ε, found by integer bisection on the exact tail (math/big, 256 bits), up to the
  first W whose threshold reaches m = 32. Lookup: smallest W with m ≤ threshold. Exact at
  micro resolution. Above m = 32: `ceil(m + z_ε·isqrt(m)) + 2`, z in micro-units (1.644854,
  2.326348, 3.090232).
- **Clamp and pace:** hi = cap − (highest lent count over the last 100 ticks,
  `LentWindowTicks`, i.e. 10 s); lo = min(W_min, hi) — the cap wins. Target above hi or below
  lo is clamped at once (`clamp`); otherwise growth is immediate (`grow`); shrink is one step,
  only if ≥ `ShrinkIntervalNs` (1 s) has passed since the last change (`shrink`). The lent
  high-water is windowed because a since-start high-water would pin the target to 0 forever
  after one busy moment.

## 4. Claims and the tests that prove them

All in `internal/lab/leasepool`.

| Claim | Test |
| --- | --- |
| **U1** core = independent reference model, every policy × warm mode × both `remake.order` × affinity, seeded random sequences with injected faults (connect failed by 4 reasons, prime/reset/reseed replies with error, other tag, status T, status E; timeouts at 30 ms / 100 ms / 1 s; database lost; releases in T, E, client gone; stale late replies); at every event: actions equal, gauges equal, open+connecting ≤ cap, connects ≤ K14, waiting ≤ wait cap, per-state counts = scan, lent ⇒ its lease, nothing lent unready / unprimed / dirty / of a lost database, structural isolation per policy, the three conservation laws, Σ time-in-state = connection time = the world's own open-to-gone time; quiesce at the end | `u1_test.go` `TestU1CoreAgainstReference`; model in `ref_test.go` |
| U1 sequence count | **2,040 sequences** (60 configurations × 34 seeds) of 400 events plus quiesce; `-short`: 240 |
| Canary: lend without waiting for the checkout reset fails U1's own isolation check (reference off) | `TestU1CanaryLendBeforeReset` (caught in 50 of 50) |
| Canary: open ignores the cap fails U1's cap check | `TestU1CanaryIgnoreCap` (caught in 48 of 50) |
| Repair round B1–B5 | see §8 |
| **U2** W\* = exact inside the table; above: never below exact, at most 2 over | `rule_test.go` `TestU2RuleAgainstExactPoisson`: **1,667 grid points × 3 ε = 5,001 checks** (m 0–3 step 0.005, 3–200 step 0.25, plus every table threshold t and t+1 µ). Largest excess above the table: 2. Exact side computed in the test (e^m by halving and squaring, pmf recurrence, ε as a rational) — not the generator's code |
| Plan §4 examples | `TestU2PlanExamples` |
| Table = generator output | `TestPoissonTableRegenerates` |
| **U3** constant input settles, then never changes (after 300 ticks) | `TestU3ConstantSettles` |
| step up 50/s → 200/s grows on the first tick, reaches final within 60 ticks (measured 37) | `TestU3StepUpGrowsFast` |
| step down: one step at a time, ≥ 1 s apart, never grows, final within 600 ticks (measured 26.1 s) | `TestU3StepDownShrinksSlowly` |
| noisy Poisson(5/tick) input, 20 seeds × 10 min: target within [base, base+1]; ≤ 120 direction changes per 10 min | `TestU3NoisyInputBoundedFlips` |
| clamps: floor W_min, ceiling cap − lent high-water (counted as `clamp`), window expiry | `TestU3Clamps` |
| **U4** per policy: release in T, E, client gone (also with T), doomed by database lost → closed, never lent again; clean release → idle / reset / close per policy | `u4_test.go` `TestU4ReleaseEndings` (25 cases), `TestU4CleanRelease` |
| reset and reseed replies (ok, error, error with the right tag, other tag, empty tag, status T, status E, no reply in K3) under both reset policies, reseed off/on: only ok lends; otherwise closed, a late good reply is ignored, never lent again | `TestU4ResetReplies` (48 cases) |
| prime replies, every policy | `TestU4PrimeReplies` (40 cases) |
| failed checkout reset puts the lease back at the head | `TestU4CheckoutResetFailRequeues` |
| driver bugs are errors and change nothing; stale replies counted | `errors_test.go` `TestStepRejectsDriverBugs` |

Totals (after the repair round): 19 top-level tests, 137 with subtests.

**How I know U1 has teeth** (beyond the two canaries): I applied eleven one-line mutations
to `pool.go` by hand and ran U1, and three more against U4; every one failed (counts of
failing sequences): tie-break of same-tenant pick 159, oldest-ready tie-break 428, `waited` not set on
requeue 172, make-room ignoring closing slots 133, open-then-close reorder off 212, timeout
`<` → `≤` 2, doomed ignored 1,039, no connect retry 906, reseed off 324, prefill counting
closing 20, reset-on-switch not taking other tenants' idle 151; U4: status ignored in
`ReplyOK` 24 tests, client gone ignored 11, reset timeout not closing 5. Two of them (the
same-tenant tie-break and the timeout boundary) survived the first version of the driver; I then made half the events share an
instant and added a 100 ms timeout so ties and exact expiry happen. Line coverage of
`pool.go` from U1 alone: 91% (the misses are the error returns, covered by
`TestStepRejectsDriverBugs`).

## 5. Stated limits and findings

- **R1 flickers on noisy input.** With Poisson arrivals at m ≈ 1.05 (50/s, L = 21 ms, ε 1%)
  the target moves between 5 and 6 only, but changes direction 47–105 times in 10 minutes
  (one up-and-down every 10–25 s), about 7% of the time at 6. Cause: growth is immediate on
  the fast EWMA (τ = 1 s), whose noise at that rate is about ±10%. The stated bound (120) is
  set from that measurement, not from a target. Cost: each flicker opens one extra warm
  connection, which is then shrunk away. I tried "shrink only after want has stayed below the
  target for a whole interval": 105 → 89, not worth departing from the plan's wording, so
  reverted. This is a finding for S5, not a defect fixed here.
- The rule's step-down is slow by design: 26 s from 200/s to 50/s, mostly the slow EWMA.
- Timeouts have tick resolution (100 ms): a reset can run up to K3 + 100 ms before close.
- `WStar` above the table is at most 2 over exact for m ≤ 200 (U2); beyond 200 untested.
  `isqrt` loses precision above m ≈ 1.76·10⁷ (saturating branch) — far outside any pool.
- Bounds: connections ≤ `Cap` (≤ 4,096); waiting + pending ≤ `WaitCap` (≤ 2²⁰, refusal when
  full); lease table ≤ Cap + WaitCap; connect-time window 64; lent window 100 ticks; actions
  per step ≤ a few per connection and waiter. Work per step is O(waiters × connections).
- The reference model shares the public types and the `Rule` type with the core (the rule is
  checked on its own by U2/U3). Everything else in it — state, matching, provisioning,
  ordering, and since the repair round patience, L̂ for `auto` and the demand counters — is
  written separately, by scanning instead of counting.

## 6. Deviations from the brief

- Size: about 1,300 lines of code (pool 675, types 307, rule 200, generator 120) and 1,710 of
  tests, against the 500 / 550 estimate. The types file is mostly enums, labels and the
  snapshot; the tests carry a full reference model and an outside-world model.
- Two extra switch modes (`ready`, `none`) and an extra `Priming` gauge / `PrimingNs`
  integral — see decisions 11 and 13.
- Commit trailer: the rules file's `Co-Authored-By: Claude <noreply@anthropic.com>`.

No change request: nothing frozen was touched.

## 7. Commands

```sh
export PATH=$PATH:/usr/local/go/bin
cd /root/Software/mini-gateway-work/lease-lab
go test ./internal/lab/leasepool/ -count=1 -v          # all, about 6 s
go test -short ./internal/lab/leasepool/ -count=1      # U1 with 240 sequences, coarse U2 grid
go test ./internal/lab/leasepool/ -run 'U1' -v         # U1 and its canaries
go test ./internal/lab/leasepool/ -run 'U2|Poisson' -v # U2 and the table check
go test ./internal/lab/leasepool/ -run 'U3' -v
go test ./internal/lab/leasepool/ -run 'U4' -v
# regenerate the table (the test above fails if it was not):
go run ./internal/lab/leasepool/poissongen/gen internal/lab/leasepool/poisson_table.go
```

A failing U1 sequence prints its seed, policy, warm mode, order, affinity, tenant count and
the full config; `runU1(seed, cfg, tenants, bugNone, true)` replays it.

## 8. Repair round (REVIEW-E2-1 section C1; brief `zgateway-lease-e12-repair.md` part B)

Cause (the review's): under `remake-on-switch` every waiting lease counted in the deficit, and
the deficit closed idle connections of other tenants with no bound, so a queue of k waiters
destroyed k connections that each cost an 18 ms rebuild, whose tenants then queued in turn.
The coordinator's decisions, implemented as written (commit `dabb85f`; regression test
`99a70bb`):

| Item | What changed (`internal/lab/leasepool`) | Test |
| --- | --- | --- |
| **B1** room only for opens that can start now | `provision`: closes that make room are `min(deficit − free − other closing, K14 − connecting − closing)`, and none while the connect-retry gate is shut. Each such close's open starts when the close is confirmed, so closes in flight count against K14 room. | U1 world check `roomBounded` (after a step that closed for `switch` with affinity on: connecting + closing ≤ K14 and the retry gate open); canary `TestU1CanaryUnboundedRoom` (planted `bugUnboundedRoom`: caught in 50 of 50 sequences) |
| **B2** patience | `Config.PatienceNs` (`remake.patience`; `PatienceAuto` = −1, the default). A waiter is *patient* while its tenant holds a lent or idle connection and `now − requested < patience`. A patient waiter takes only its own tenant's idle connection (affinity on or off), is not in the deficit and causes no close. A tenant with nothing does not wait. `auto` = the current L̂: the rule's under `dynamic`; under `fixed`/`cold` the same estimator (`LeadEstimator`, now its own type in `rule.go`, used by `Rule` too) over the pool's own connect times, initial `warm.initial_lead` (21 ms). Free slots do not bypass patience. Only `remake-on-switch` uses it. | U1 (reference model), `TestC1PatienceStopsTheThrash` |
| **B3** fresh-connection demand | Counted once per lease (`lease.counted`): at grant to a never-lent connection, or in the step it first counts toward the deficit (`demand`). This is the rule's λ̂ input. | U1: core and reference counters equal at every event |
| **B4** counters | `Snapshot`: `SwitchArrivals`; `StockEmptyArrivals` (counted as a waiter with no warm connection); `StockEmptyFreeSlot` (of those, a slot free, or a connect/prime under way, or a close-then-open replacement closing — measured before that step's provisioning); `PatienceWaits` (once per lease, when seen waiting patiently after matching); `PatienceExpired` (once per such lease, when, its time up, it takes a connection not of its tenant or counts toward the deficit) | U1 compares all five with the reference at every event |
| **B5** reference model and regression | `ref_test.go` updated for the new rules from the rule text above (its own patience test by scanning connections, its own L̂ window, its own demand walk). U1 draws `remake.patience` per sequence from {auto, 0, 5 ms, 40 ms} (drawn last, so the other knobs of each seed are unchanged). U1 also checks the per-tenant tag map against a scan. | `TestU1CoreAgainstReference` 2,040 sequences green; all three canaries bite; `c1_test.go` `TestC1PatienceStopsTheThrash` |
| **B6** | config key, in E2 | NOTES-E2.md "Repair round" |

**Decisions this round left to me.**

- *Patience expiry is exact at event resolution*: a waiter is patient while
  `now − requested < patience`, evaluated at every step (every event, and every 100 ms tick).
  There is no timer of its own, so with no event in between a waiter can sit past its patience
  until the next event, at most one tick (100 ms) later. Under load events are far denser.
- *`auto` is evaluated every step*, so a waiter's deadline moves with L̂.
- *"Connecting for it"*: connections in flight belong to no tenant in this core, so a tenant
  "holds" only lent and idle connections; a waiter is never patient on the strength of a
  connect.
- *Patience is re-evaluated each step*: a waiter that becomes patient again (another lease of
  its tenant got a connection) leaves the deficit again; it is still counted as demand once.
- *The affinity-off close in matching* (a lease that picks another tenant's idle connection
  closes it) is not bounded by B1 — it is the switch itself, not room-making — but a patient
  waiter never does it. U1's room check therefore runs with affinity on only.
- The `pump` early exit stays: `patient` has no side effects, so skipping waiters when no
  warm or idle connection is left changes nothing.

**Mutations** (by hand, each against U1; failing sequences of 2,040): patience boundary
`<` → `≤` 20; no expiry count in `demand` 282; none at grant 128; no demand count at grant
983; room bound ignoring closing connections 74; patient waiter takes warm (affinity on) 98;
patience ignored with affinity off 110; stock-empty-with-free-slot ignoring priming 718; zero
tag entries kept 2,002; `auto` ignoring observed connects 73; closes ignoring the retry gate 46.
All caught.

**C1 in miniature** (`TestC1PatienceStopsTheThrash`): cap 4, K14 2, `fixed:1`, three tenants
each sending two leases 1 ms apart every 10 ms (5 ms leases, 18 ms connects, 1 ms close),
2 s. After a 200 ms warm-up: patience `auto` — 0 switch closes, 0 connects, 1,080 patient waits,
none expired, max wait 4 ms; patience `0` — 93 switch closes, 92 connects, max wait 59 ms. In
the warm-up (every tenant starts with nothing while the first connects take 18 ms) `auto` makes
3 switch closes and 22 waiters expire; the test logs both and asserts on the steady part only.

The simulator-level C1 table is in NOTES-E2.md "Repair round".

**Still true:** U1–U4 green (`go test ./internal/lab/leasepool/ -count=1`, about 8 s); U2/U3
unchanged (the rule's arithmetic is untouched; only the L̂ window moved into
`LeadEstimator`). Run the new tests: `go test ./internal/lab/leasepool/ -run 'U1|C1' -v`.

