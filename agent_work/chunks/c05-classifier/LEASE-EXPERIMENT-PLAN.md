# Lease-design experiment — plan

Written by Fable on 2026-11-26 (mission `/root/briefs/zgateway-lease-exp-plan-mission.md`).
**Plan only.** Chunk 5 stays **parked** by Nick's ruling of 2026-11-25. This file dispatches
nothing, adds no product or test code, and changes neither PLAN.md nor `work/c05-classifier`.
Building starts only after Nick has reviewed this plan. Inputs: [OPTIONS.md](OPTIONS.md),
[CANVAS.md](CANVAS.md), PLAN.md, STATE.md.

Evidence labels: **[canvas]** a finding in CANVAS.md; **[repo]** a fact read in this
repository on 2026-11-26; **[mine]** my reasoning or estimate, not run.

Every numbered item (arms, knobs, tests, pieces, questions) is written so that Nick can
approve, amend or strike it on its own.

## 1. The question, and what I expect to find

**Question.** At the lease boundary, which release policy keeps session state from crossing
tenants at a cost the pool can carry — resetting the connection, or replacing it from a
stock of warm ones — and can the size of that stock be set by a rule instead of by hand?

**Predictions, declared before any run** (all [mine]; the experiment exists to confirm or
refute them):

- **P1.** Reset after every lease costs one round trip per lease and lowers the pool's
  capacity by the factor S/(S+r) (S = mean lease time, r = reset round trip). With r at
  0.044 ms on a clean session [canvas] this is a few percent for leases of 1 ms and up.
- **P2.** Remake after every lease has a hard throughput ceiling that reset does not have:
  the rate at which Postgres can start backends. Postgres starts them one at a time, and a
  burst of 500 connects took over a second on this VM [repo, `docs/knowledge/sim-world.md`],
  so the ceiling is in the hundreds of leases per second however large the pool is. A warm
  stock hides the connect time from the client; it cannot raise this ceiling.
- **P3.** Remake only on a tenant switch works wherever the *switch* rate stays under that
  ceiling, and is then the only policy with no residue at all.
- **P4.** While connects have free slots to run in, the number of warm connections needed
  for a miss probability ε is the (1−ε) quantile of a Poisson distribution with mean
  λ·L (arrival rate × connect time). A rule built on that tracks a changing load with fewer
  idle connections than the best fixed size chosen with hindsight.
- **P5.** "Warm" is not "hot": a fresh backend has empty catalog and plan caches, so its
  first statement is slower than on a reused connection. A reset also discards plans.
  Both penalties are unmeasured so far.

If P2 is wrong by an order of magnitude, the conclusion changes; that is why calibration
(stage 0) runs first and is a stopping point.

## 2. Arms — and a different cut than the mission's

The mission names two arms. **I recommend cutting them as two independent axes**, because
that is what the code is: OPTIONS.md already notes that A, B and C are one code point with
three policies, and remake-after-use is a fourth policy at the same point. Whether warm
connections are provisioned ahead of demand is a separate question, and only the remake
policies need it. One pool, two config keys; every arm is a pair of values.

**Axis 1 — release policy** (what happens to a connection between two leases):

| Key value | What it does | Mission / OPTIONS name |
| --- | --- | --- |
| `none` | Nothing. Reuse as is | **Baseline** (section 3) |
| `reset-always` | `DISCARD ALL` after every lease, at release | Arm 1; option A |
| `reset-on-switch` | `DISCARD ALL` only before a different tenant gets it, at checkout | Arm 1 variant; option B |
| `remake-always` | Close after every lease; a fresh connection takes the slot | Arm 2 as the mission states it |
| `remake-on-switch` | Keep for the same tenant; close before a different tenant would get it, who takes a warm one instead | Arm 2 variant; option C by reconnect |

`remake-on-switch` is in because Nick's wording ("warm connections ready to switch for new
users/tenants") reads either way, and by P2 and P3 it is the form most likely to survive.

**Axis 2 — warm provisioning** (connections opened before anyone asks, logged in, never
used, belonging to no tenant):

| Key value | What it does |
| --- | --- |
| `cold` | Open on demand only. The lease waits for the connect |
| `fixed:W` | Keep W warm connections; replace each one as it is taken |
| `dynamic` | As `fixed`, with W set by the rule of section 4 |

Under the reset policies connections persist, so the pool is its own warm stock after the
first seconds; there this axis reduces to "prefill at start or not" and is run only in the
cold-start and restart tests. That prunes the matrix to 11 arm pairs.

**Properties each arm must evidence:**

- Arm 1: no state in the nine `DISCARD ALL` categories crosses leases (`reset-always`) or
  tenants (`reset-on-switch`); the four-item residue of OPTIONS.md is shown as it is, not
  argued away; cost per lease and capacity loss are measured.
- Arm 2: a new tenant never waits on connection setup (a warm hit at rate ≥ 1−ε), and never
  inherits state (no backend process ever serves two tenants, or two leases).

## 3. Baseline

**Baseline: `none` + `cold`** — a plain fixed-size pool that opens connections on demand up
to the cap, reuses them without any reset, and provisions nothing ahead. This is PgBouncer's
transaction mode with no reset query [canvas].

Why this one:

- It is the simplest pool that can be written, and every arm is the same pool with one more
  action at release. Each arm's cost is then a difference against it, same seed, same load.
- It is the performance ceiling and the isolation floor. It **must fail** the isolation
  tests. That is the project's ablation rule (PLAN.md section 6.2): if the baseline passes
  the oracle, the hostile workload or the oracle has no teeth and the arms' passes prove
  nothing.

The mission's own example (fixed pool, `DISCARD ALL` per lease, cold on demand) is the pair
`reset-always` + `cold`, which is arm 1's plainest configuration; it is in the matrix under
that name. A second control, **`remake-always` + `cold`** (connect per lease, no pool in
effect), is the isolation ceiling and performance floor: the warm stock must beat it, or it
is doing nothing.

## 4. The sizing rule (the math element)

**Model.** A warm stock is an inventory problem, not a staffing problem. Each lease that
needs a fresh connection takes one from stock and causes one replacement to be ordered; the
replacement arrives after the connect time L. The stock rule is "keep warm + connecting
equal to a target W" (a base-stock rule).

- The number of replacements under way at a random instant has mean **m = λ·L**. That is
  Little's law applied to the connect pipeline.
- For Poisson arrivals and *any* distribution of L, that number is Poisson with mean m
  (Palm's theorem; the pipeline is an M/G/∞ system).
- A lease misses when all W are under way, so **P(miss) = P(Poisson(m) ≥ W)**.

**Rule R1 (the one under test).** Every tick (100 ms):

```
λ̂  = max(EWMA_fast, EWMA_slow) of arrivals that need a fresh connection   (grow fast, shrink slowly)
L̂  = p95 of recent connect times                                          (knob: mean | p95)
m  = λ̂ · L̂
W* = smallest W with P(Poisson(m) ≥ W) ≤ ε     for m ≤ 32: exact, from a generated integer table
   = ceil( m + z_ε · sqrt(v · m) ) + 2          above the table, and whenever v > 1
     clamped to [W_min, cap − lent high-water]
```

- "Arrivals that need a fresh connection" is every lease under `remake-always` and only
  tenant switches under `remake-on-switch`.
- z_ε is the normal quantile for the target miss probability (2.326 for ε = 1%). Examples
  at ε = 1%: m = 0.8 needs W = 4; m = 8 needs W = 16. The table is used for small m because
  that is where pools of this size live and where the normal form is weakest: with `+1` it
  fell one short of the exact answer at many points below m = 3; with `+2` it was never
  short and at most 2 over, for m up to 200 and ε of 5%, 1% and 0.1% [mine, computed while
  writing this plan; test U2 repeats it against the built code].
- v is 1, or the measured variance-to-mean ratio of arrivals per tick when the knob
  `dispersion` is on. Real arrivals are burstier than Poisson; v widens the margin by what
  was observed instead of by a guess.
- Shrinking is at most one connection per idle interval; growing is immediate.
- Integer arithmetic only (fixed-point EWMA, the integer square root PLAN.md decision D2
  already names for CoDel), so the core stays inside the determinism lint.

**Why not Erlang-C.** Erlang-C sizes servers that customers queue for; it is the model for
the lent connections and belongs to the adaptive limit (chunk 10). The warm stock is not
queued for — it is consumed and reordered — which is the base-stock model.

**Where I expect the model to break, and the experiment must show it:**

1. *No free slots.* A replacement can start at checkout only while open + connecting is
   under the hard cap. At high utilisation it starts at release instead, and the stock
   cannot be held at W whatever the rule says. Sustainable load for `remake-always` is
   about (cap − W)/(S + x + L), x being the time a closed backend still holds its slot.
2. *Serial backend start* (P2). L grows with the number of connects in flight, so ordering
   more makes each slower. The cap on concurrent connects bounds it; the rule sees it
   through L̂.
3. *Bursts and tenants arriving together.* Not Poisson. Covered partly by v, partly not.

**Comparators for the rule:**

- **R0, fixed W** over a sweep of sizes. The best fixed W for a run, found afterwards by
  replaying the same seed at every size, is the yardstick ("hindsight fixed").
- **R2, reactive, no model:** one more on every miss, one fewer when a warm connection has
  sat unused for an interval. If R1 does not beat this, the math is not earning its place.

The rule is judged on two numbers together: miss rate against ε, and warm-connection-seconds
(what the idle stock costs in slots and backend memory).

## 5. Configuration surface

All per run, recorded in the run's files, and part of the replay line. One seed fixes the
workload and every random choice.

| # | Key | Values | Default |
| --- | --- | --- | --- |
| K1 | `release.policy` | `none`, `reset-always`, `reset-on-switch`, `remake-always`, `remake-on-switch` | — |
| K2 | `reset.reseed` | off, on (a `setseed` round trip after the reset; closes residue item 1) | off |
| K3 | `reset.timeout` | duration; on expiry the connection is closed | 1 s (PLAN.md 9.2 clean-up row) |
| K4 | `warm.mode` | `cold`, `fixed`, `dynamic` | — |
| K5 | `warm.size` | W for `fixed`; W_min for `dynamic` | swept: 0, 1, 2, 4, 8, 16 |
| K6 | `warm.rule` | `R1`, `R2` | R1 |
| K7 | `warm.epsilon` | target miss probability | 1% (also 0.1%, 5%) |
| K8 | `warm.ewma` | fast and slow time constants | 1 s, 10 s |
| K9 | `warm.lead` | `mean`, `p95` | p95 |
| K10 | `warm.dispersion` | off, on | off |
| K11 | `warm.prime` | `none`, `select1`, a script (run on a fresh connection before it counts as warm; tests P5) | none |
| K12 | `remake.order` | `close-then-open`, `open-then-close` (the second needs spare slots) | close-then-open |
| K13 | `pool.cap` | hard cap on open + connecting | 20 (also 5, 100) |
| K14 | `connect.max_concurrent` | connects in flight at once | 4 |
| K15 | `affinity` | prefer a connection last used by the same tenant: on, off | on |
| K16 | `workload.*` | profile (section 7), tenant count, tenant skew, lease-time distribution, load as a fraction of calibrated capacity, taint mix | per test |
| K17 | `db.*` | simulator or real; for real: trust or SCRAM, TLS off or on | per test |
| K18 | `oracle.rate` | fraction of leases followed by the dirty check | 1 in tier R correctness runs, 0 in timing runs |
| K19 | `seed` | integer | — |

## 6. Harness architecture

**The constraint.** There is no proxy yet: chunk 6 is not built, and `internal/work` sits in
the parked chunk 5. So the experiment is a **lease lab**: the pool and its release path
alone, driven by synthetic lease requests, talking real wire protocol to a database. No
client connections, no admission queue beyond a first-come wait list, no fairness. That is
enough for this question and it depends on nothing parked.

```
 lease-request generator ──► pool core ◄── sizing rule
 (seeded: tenant, time,       (pure: events in, actions out)
  script to run)                    │ actions: open, send reset, close, lend
                                    ▼
                         database-leg driver ──► simulator: simnet + fakepg + connect-cost model
                         (pgwire.BackendSession)  real:     TCP to Postgres 17 in Docker
                                    │
              obs collector ──► CSV flush (schema v1) ──► DuckDB SQL ──► REPORT.md
              inspector (tier R): dirty check + canary + residue probes between leases
```

**Pieces and what they reuse** [repo]:

- **Pool core** (new, pure): connection states (connecting, warm, lent, idle-tagged,
  resetting, closing), the release-policy switch, the warm stock, the sizing rule. Written
  to decision D2's rules — `now` as an argument, actions out, no goroutines, integers — so
  that if a policy is adopted the core can be promoted into chunk 7's `internal/pool`
  instead of rewritten. That promotion is a later decision, not part of this experiment.
- **Database leg:** `pgwire.BackendSession` (startup, SCRAM, TLS — landed in chunk 4) and
  the reply tracker. The reset is the proxy's own `Query` message, lendable only after the
  `DISCARD ALL` tag and status `I`, as OPTIONS.md specifies.
- **Simulator world:** `internal/sim`, `simnet`, `fakepg` as frozen at `freeze-sim`. Nothing
  in them changes. Two things the fake lacks are supplied by the lab, outside the frozen
  packages: a **connect-cost model** (the fake accepts connections for free; the lab's
  dialer delays each connect through a one-at-a-time queue with times drawn from the
  calibration file), and state kinds beyond settings (the fake models `SET`, `RESET`, `SHOW`
  and `DISCARD ALL` only; everything else is checked on real Postgres).
- **Observability:** `internal/obs` collector and `csvflush` unchanged, schema version 1.
  The lab registers its metrics under the existing component name `pool`, so no component
  is added and PLAN.md section 7.7 is untouched.
- **Real database:** the `scripts/tier-r.sh` pattern — throwaway `postgres:17` containers on
  127.0.0.1, trust and SCRAM variants.

**Where it lives.** Branch `exp/lease-lab`, own worktree; packages `internal/lab/leasepool`
(core), `internal/lab/leaselab` (driver, generator, inspector), `cmd/mglease` (runner),
`scripts/lease-exp.sh`, SQL under `test/analysis/lease/`. Only the evidence comes to main:
the report, the calibration file and per-arm summary tables under
`agent_work/chunks/c05-classifier/lease-exp/`. The code stays on its branch until Nick rules
on what the evidence says.

**Which tier answers what:**

| Question | Tier | Why |
| --- | --- | --- |
| What does a reset, a connect, a close, a first statement cost? | Real | The fake has no costs of its own; these numbers are the experiment's inputs |
| Does any session state survive a lease? | Real | Only a real server has the state, and its catalog is the oracle |
| Does the pool always take the required action between two tenants? | Simulator | Exact, from the fake's statement log, over many seeds |
| Miss rate against W; the rule against fixed sizes; capacity curves; 10,000 tenants; restarts and connect faults | Simulator | Scale, exact replay, and faults that cannot be staged on a real server |
| Do the simulator's curves match a real run? | Both | One small scenario in both tiers; this is what licenses the simulator's larger claims |
| Head-to-head latency and goodput | Real, small scale | PLAN.md section 11's method; shared 4-vCPU VM, so ratios with a noise floor, not absolutes |

## 7. Workloads

Seeded, open-loop (each lease has an intended start time; waits are measured from it).

| Name | Shape | Used for |
| --- | --- | --- |
| `steady` | Poisson arrivals, tenants uniform | The model's home ground |
| `skewed` | Zipf tenants: a few hot, a long tail (10, 100, 10,000 tenants) | Switch rate; `-on-switch` policies |
| `bursty` | Tenants switch on and off; many new tenants arrive together | Where Poisson fails |
| `step` | Load ×4 up, hold, back down | Tracking by the rule |
| `hostile` | Each lease runs a taint script, the next reads back (below) | Correctness |
| `faulty` | `steady` plus database restart, connect stall, connect refused, slow connects | Recovery, leaks |

Lease times: short (about 0.5 ms), medium (5 ms), heavy-tailed. Load levels: 30%, 60%, 80%,
95% and 120% of the baseline's calibrated capacity.

**Taint script kinds** (from the canvas experiments, including the function-hidden forms of
`e8-hidden.sql`): `SET`; `set_config` inside a function; `PREPARE`; `DECLARE … WITH HOLD`;
`LISTEN`; session advisory lock; temp table; `SET ROLE`; `setseed`; a custom setting name;
a lease that ends inside a transaction; one that ends in a failed transaction. Each writes a
per-tenant secret where the kind allows, so the next lease can try to read it.

## 8. Tests, and what each proves

"Fails if" is the observation that would refute the claim. Tier S checks are exact; tier R
latency checks use margins declared in section 10.

**Core and harness (no database)**

| ID | Test | Proves | Fails if |
| --- | --- | --- | --- |
| U1 | Pool core against a reference model, random event sequences, every policy | Open + connecting ≤ cap at every event; every connection in exactly one state; nothing lent that is not ready | Any divergence from the model |
| U2 | Sizing rule against exact Poisson quantiles over a grid of m and ε | The built rule equals the exact answer inside the table, and above it is never below exact and at most 2 over | A grid point below exact |
| U3 | Rule dynamics on constant, step and noisy input | No oscillation at constant input; shrink is rate-limited; clamps hold | Target changes direction more than a stated bound |
| U4 | Release state machine against scripted replies | Only the `DISCARD ALL` tag followed by status `I` makes a connection lendable; error, timeout, other status, or a lease ending in `T`/`E` closes it | Any other reply leads to a lend |
| U5 | Same seed twice | Byte-identical CSV files (the replay law extended to the lab) | Any differing byte |
| U6 | Report queries on planted run directories | The SQL finds a planted leak, a planted miss rate and an empty input | A planted error passes |

**Simulator (tier S)**

| ID | Test | Proves | Fails if |
| --- | --- | --- | --- |
| S1 | Structural isolation from the fake's log, 200 seeds, every policy | Between two leases of different tenants on one backend connection there is always a completed reset, or they are never on the same connection | One pair without it. **Baseline must fail this** |
| S2 | Setting marker: each lease sets a marker and the next reads it | The reset path works end to end on the wire, not only in the log | A marker read by another tenant |
| S3 | Miss rate against fixed W, `steady`, swept over λ·L | Whether P(miss) = P(Poisson(λL) ≥ W) describes the pool. This validates the model inside its assumptions | Measured misses outside the binomial band of the prediction |
| S4 | S3 on `bursty`, at high utilisation, and with serial connects | Where the model breaks, and by how much (the three limits of section 4) | Nothing — this test maps the limit; its output is the size of the gap |
| S5 | `dynamic` R1 against every fixed W and against R2, on `steady`, `step`, `bursty` | Whether the rule holds ε with fewer warm-connection-seconds than hindsight-fixed, and beats the reactive rule | R1 misses ε where hindsight-fixed holds it, or costs more idle stock than R2 for the same miss rate |
| S6 | Capacity curve per policy: goodput and wait against offered load | P1 and P2: the knee of each policy against its predicted ceiling | A knee more than a stated band from the formula — the formula is then wrong and is corrected |
| S7 | Cost map: overhead of each policy over the baseline across L/S and load | Where each policy is tolerable. **The headline picture of the experiment** | — (a map) |
| S8 | Tenant scale: 10, 100, 10,000 tenants, `skewed`, affinity on and off | How the switch fraction drives `reset-on-switch` and `remake-on-switch`; whether affinity starves anyone | A tenant whose wait is unbounded while connections sit idle for others |
| S9 | Cold start and database restart, each policy and warm mode | Refilling the stock obeys the connect cap; no connect storm; goodput returns; cap invariant holds | Connects in flight above K14, or no recovery |
| S10 | Connect stall, refusal and slow connects during remake | A failed replacement frees its slot; leases degrade to waiting, never hang; quiesce is clean | A leaked slot or a stuck lease |
| S11 | Server-side connection peak, both `remake.order` values | The fake never sees more than the cap (close-then-open), or cap + the stated overlap | Peak above the bound |
| S12 | Ablations: W = 0 under remake; rule frozen; reset skipped | The warm stock is what removes the wait; the rule is what tracks; the reset is what isolates | An ablated run that still passes its arm's claim |

**Real Postgres 17 (tier R)**

| ID | Test | Proves | Fails if |
| --- | --- | --- | --- |
| R0 | **Calibration.** Time: `DISCARD ALL` on a clean session and after each taint kind; connect (trust, SCRAM; TLS off, on; 1, 4, 16, 64 at once); close until the slot is free; first statement on a fresh, a reused and a just-reset backend; a plpgsql call after a reset | The cost inputs r, L, x and the P5 penalties, written to a calibration file the simulator reads. Tests P2 directly | — (measurement). **Stopping point**, see section 11 |
| R1 | **Dirty-check oracle.** `hostile` workload; after every lease the inspector runs the canvas's catalog query (`canvas/experiments/postgres-server/dirty-check.sql`) on that backend before it can be lent again | No prepared statement, cursor, listen, advisory lock, session setting, temp relation or changed role is left, under each arm, whatever SQL caused it | Any non-zero column under a reset or remake policy. **Baseline must show non-zero in every column** |
| R2 | Canary read-back: the next tenant tries to read each kind of state the previous one wrote | The oracle's "nothing exists" also means "nothing can be read" | Any secret read |
| R3 | Backend identity: `pg_backend_pid()` and backend start time per lease | Under `remake-always` no backend serves two leases; under `remake-on-switch` none serves two tenants. This is arm 2's "never inherits state", proved structurally | A backend seen with two leases or tenants |
| R4 | Residue probes: random sequence after the previous tenant's `setseed`; visibility of a custom setting name | The reset arms show exactly the residue OPTIONS.md lists (items 1 and 2), `reset.reseed` closes item 1, and the remake arms show none | Residue under remake; or a reset arm showing residue beyond the list |
| R5 | Lease ends in a transaction, in a failed transaction, with the client gone | The connection is closed (or rolled back, then reset) and never lent dirty; a refused reset closes it | Oracle non-zero afterwards |
| R6 | Server-side count from `pg_stat_activity`, sampled, during remake at load | The real server never holds more backends than the cap, including ones still exiting | Count above the bound |
| R7 | Head-to-head: five policies × four loads, 10 interleaved runs per arm, an A/A pair, validity gate (achieved rate within 2% of intended) | Lease wait, tenant-switch latency and goodput per policy against the baseline, with a noise floor | — (verdicts by section 10, "inconclusive" allowed) |
| R8 | One scenario from S3 and S6 at small scale, both tiers | The simulator with the calibrated costs predicts the real run | Hit rate or wait quantiles outside the declared band — the simulator's larger claims are then narrowed in the report |
| R9 | Postgres CPU time per 1,000 leases, per policy (container CPU accounting) | What connection churn costs the server, which the pool's own clock cannot see | — (measurement) |

Residue items 3 and 4 of OPTIONS.md (extension and procedural-language state; replication
origin) are not tested: the stock image has neither language installed, and item 4 needs a
privileged role. The report states them as untested.

## 9. Metrics and files

Standing pattern: components expose snapshots, a collector publishes them, the CSV adapter
flushes with schema version 1, analysis is SQL in DuckDB over the files. All under component
`pool`; labels only from the four the schema has.

| Evidence the mission asks for | Metric | Kind |
| --- | --- | --- |
| Lease wait time | `lease_wait_ns` by outcome (`reuse`, `warm_hit`, `miss_connect`, `miss_slot`) | histogram |
| Tenant-switch latency | `switch_ready_ns` by mode (`warm`, `reset`, `connect`): from grant to usable, for leases whose connection last served another tenant or none | histogram |
| Warm hit and miss | `leases_granted` by outcome; `warm_target`, `warm` | counter; gauges |
| Remake cost against reset cost | `reset_ns`, `connect_ns`, `close_lag_ns`, `first_stmt_ns` by mode (`fresh`, `reused`, `after_reset`) | histograms |
| Utilisation | Time spent per state as counters: `lent_ns_total`, `warm_ns_total`, `connecting_ns_total`, `resetting_ns_total`, `closing_ns_total`. Exact integrals, not sampled gauges; they sum to open-connection time, which is a conservation check | counters |
| Isolation checks | `oracle_checks` by outcome (`clean`, `dirty`); `canary_reads` by outcome (`clean`, `leak`) | counters |
| The rule's inputs | `lambda_hat_milli`, `lead_hat_ns`, `warm_target_changes` by reason | gauges; counter |
| The cap | `open`, `lent`, `connecting`, `waiters`; high-water marks of open + connecting and of waiters | gauges |
| Accounting | `leases_requested`, `leases_released` by outcome, `release_actions` by mode, `resets`, `connects`, `closes` by reason | counters |

Conservation, asserted in SQL on every run: requested = granted + still waiting; granted =
released + lent; connects ok = closes + open.

Two sidecar files beside the standard three, written by the lab and versioned on their own
(they do not touch schema version 1): `lab.csv` (every config key and the calibration id of
the run) and `oracle.csv` (one row per inspected lease: tenant, previous tenant, backend
pid, every dirty-check column, probe results). The sidecars are experiment-only.

**The report** (`lease-exp/REPORT.md`, every table the output of one SQL file, in this order):

1. Calibration: what a reset, a connect, a close and a first statement cost here.
2. Isolation matrix: policy × state kind → leaks found. Expected: baseline dirty everywhere,
   reset arms clean except the named residue, remake arms clean.
3. Cost per lease: remake against reset, quantiles.
4. Capacity: goodput against offered load per policy, predicted knee beside the measured one.
5. Waiting: lease wait and tenant-switch latency per policy and load, baseline beside each.
6. Warm stock: miss rate against W, measured beside the Poisson prediction.
7. The rule: R1, R2 and hindsight-fixed — miss rate and warm-connection-seconds; the target
   plotted against the load on `step`.
8. Simulator against real.
9. Verdict table against section 10, each row with its kind of evidence and confidence.

Tables first. Charts are optional and would use no new dependency.

## 10. What the evidence decides

**Thresholds, proposed for Nick to amend before any run.** A policy "works well" at a load
when all hold:

- Isolation: zero rows from R1, R2, R3 and S1 (residue rows from R4 reported separately).
- Goodput at 80% load: at least 95% of the baseline's, same seed (simulator, exact) and
  not below it by more than twice the A/A floor (real).
- p99 lease wait at 80% load: within 2× the baseline's.
- Warm stock: miss rate at or under ε, with warm-connection-seconds no more than 1.5× the
  hindsight-fixed size.

**Decisions the result supports** (all Nick's; none is taken here):

| If the evidence shows | It supports |
| --- | --- |
| `reset-always` works well across the load range | Option A as the chunk 5 ruling, as OPTIONS.md recommends; the classifier's session half is not needed for safety; no warm stock in chunk 7 |
| `reset-always` fails only at high load and `reset-on-switch` holds | A as default with B as a config policy; chunk 7 gains the last-tenant tag and affinity |
| `remake-on-switch` works well where switch rate is under the connect ceiling | It becomes a selectable policy for deployments that cannot accept the residue; chunk 7 gains a warm stock; the report states the ceiling as a documented limit |
| `remake-always` works well (P2 refuted) | The complete answer is affordable; the reset path becomes the fallback instead of the default |
| R1 holds ε with less idle stock than fixed and beats R2 | The rule ships with the warm stock |
| R1 does not beat R2 or a fixed size | Ship a fixed size; record the rule as tried |
| Baseline passes the oracle, or the simulator and real disagree (R8) | The experiment is not valid as run; fix the harness before reading anything else |

In every case the report feeds the brief for a re-opened chunk 5 and for chunk 7; it changes
decision D8 and steering answer 9 only through Nick's ruling.

## 11. Pieces, order and cost

Stages are stopping points: each ends with something readable, and Nick can halt after any.

| # | Piece | Stage | Size (lines: code / tests) | Builder | Cost |
| --- | --- | --- | --- | --- | --- |
| E0 | Calibration probe and R0 run; calibration file; two-page note with P2 checked | 0 | 250 / — | Opus | 1 run, about 2 h; 20 min machine time |
| E1 | Pool core, release policies, sizing rule; U1–U4 | 1 | 500 / 550 | Opus | 1 run |
| E2 | Lab driver: generator, simulator shell, connect-cost model, metrics mapping, sidecars, runner; U5 | 1 | 700 / 250 | Opus | 1 run |
| E3 | Simulator scenarios S1–S12 | 1 | — / 700 | Opus | 1 run; sweep about 15 min |
| E4 | Hostile workload, inspector, oracle, canary, residue probes (R1–R5) | 2 | 400 / 300 | Astra | 1 run |
| E5 | Real shell, container script, R6–R9 | 2 | 350 / 300 | Opus | 1 run |
| E6 | Report SQL and its self-test (U6) | 2 | 400 SQL / 100 | Opus | 1 run |
| E7 | The campaign: simulator sweeps, then tier R under the machine lock | 3 | — | Fable | about 4–5 h machine time (R7 alone: 5 policies × 4 loads × 10 runs × 40 s ≈ 2.2 h) |
| E8 | REPORT.md from the query outputs; verdict table | 3 | — | Fable | Half a day |
| — | Reviews: one per build piece, one repair round each | all | — | see Q3 | 6 reviews |

Total: about 2,600 lines of code and SQL and 2,200 of tests — a little more than chunk 7's
estimate — in 6 build runs, 6 reviews and one campaign. At this project's pace that is two
to three days, with E4 running beside E2/E3 and E5 beside E6. **Stage 0 alone costs one run
and settles P2**, which decides how much of arm 2 is worth the rest.

E4 goes to Astra on purpose: the builder of the pool should not also write the oracle that
judges it.

Cheaper cuts, if Nick wants them: drop `remake.order`, `warm.prime` and R9 (saves about 15%);
drop tier R head-to-head R7 and rest on the simulator with calibrated costs plus R8 (saves
half the machine time, loses the noise-floor comparison); run stage 0 only.

## 12. Limits and risks

- **A lab is not the proxy.** No client leg, no admission queue, no fairness. Waits here are
  pool waits only; how the policies interact with CoDel and per-tenant queues is chunk 8
  and 9's question. Ratios between policies should carry over; absolute numbers will not.
- **One shared VM.** Loopback, 4 vCPUs, generator and database on one host. Reset and
  connect costs over a real network are larger, and the round trip then dominates both.
  The simulator can sweep network latency; nothing here measures a real link.
- **The connect-cost model is mine.** A one-at-a-time queue fitted to R0. R8 is the check;
  if it fails, simulator claims about remake are narrowed to what tier R showed directly.
- **The oracle sees what the catalog shows.** Residue items 3 and 4 are outside it.
- **The frozen simulator may prove too plain** (free connects, settings only). The plan
  works around both without touching it. If a builder finds it cannot, that is a change
  request to the orchestrator, not an edit.

## 13. Questions for Nick

1. **The cut (section 2).** Two axes — five release policies × three warm modes — instead of
   two arms. Agreed? In particular: is `remake-on-switch` in scope, or did you mean strictly
   remake after every lease?
2. **Baseline (section 3).** `none` + `cold` as the baseline, with `remake-always` + `cold`
   as the second control. Or do you want the mission's example (`reset-always` + `cold`)
   treated as the baseline?
3. **Review seat.** The lab is experiment code, not product. The project ruling has Neon
   review Opus, and Neon's weekly pool was at 56% on 2026-11-25 with twelve chunks still to
   review. Recommended: Fable reviews the lab pieces (the global Opus rule), keeping Neon
   for product chunks. Your gate.
4. **Thresholds (section 10).** The four "works well" lines, before anything runs.
5. **Database leg in calibration.** Trust and SCRAM, TLS off and on (four variants), or only
   the variant you expect to deploy?
6. **Staging.** Approve all four stages, or stage 0 first and decide the rest on its note?
