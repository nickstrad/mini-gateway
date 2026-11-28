# Lease lab — piece E3 notes: simulator scenarios S1–S12 and the campaign sweeps

Builder: Opus, 2026-11-26. Brief `/root/briefs/zgateway-lease-e3-sim-scenarios.md`, rules
`/root/briefs/zgateway-lease-lab-rules.md`. Branch `exp/lease-lab-e3` (forked from `4429d1a`,
the reviewed E2 tip); not pushed. Inputs read: LEASE-EXPERIMENT-PLAN.md §4, §7–§10,
CALIBRATION-NOTE.md, the stages 1–3 ruling, NOTES-E1.md, NOTES-E2.md (§13 included),
REVIEW-E2-2.md (its carry-forward list).

## 1. What is built

All new files; **no E1 or E2 file was changed** (no defect needed fixing).

| File | Lines | What |
| --- | --- | --- |
| `internal/lab/leaselab/scenario.go` | 350 | `Summary`/`Summarize` (one run's numbers, final and arrival-window), `ReadFlushes` (metrics.csv by flush), `PoissonTailPPM`, `BinomialBandPPM`, `PredictCapacity` (the corrected S6 formula), formatting. Integers only |
| `internal/lab/leaselab/scenario_sweeps.go` | 448 | The arms, cells and the ten sweep grids (`Sweeps`, `FindSweep`, `Sweep.Runs(Size)`, `MergeArgs`) |
| `cmd/mglease/sweep.go` | 189 | `mglease sweep <name>\|all\|list out=<dir> [seeds=N] [scale=D] [jobs=J] [campaign=C] [calib=]` |
| `internal/lab/leaselab/helpers_scenario_test.go` | 136 | Sizes, `srun` (run + quiesce + summary), `quiet`, `replayLaw`, log tables |
| `internal/lab/leaselab/isolation_scenario_test.go` | 241 | S1, S2, the canary |
| `internal/lab/leaselab/stock_scenario_test.go` | 329 | S3, S4, S5, S12 |
| `internal/lab/leaselab/capacity_scenario_test.go` | 216 | S6, S7, S8 |
| `internal/lab/leaselab/faults_scenario_test.go` | 163 | S9, S10, S11 |
| `cmd/mglease/sweep_test.go` | 83 | Sweep labels, same bytes on 1 and 4 goroutines, usage |

Code 987 lines, tests 1,168 lines (estimate: 700 lines of tests).

## 2. How to run

```sh
export PATH=$PATH:/usr/local/go/bin
cd /root/Software/mini-gateway-work/lease-e3          # mglease and the tests find calibration.json upwards
go test -short ./internal/lab/leaselab -run 'TestS' -count=1 -v            # smallest: 62 s
go test ./internal/lab/leaselab -run 'TestS' -count=1 -v                   # default (reduced grids): ~250 s
LEASE_E3=full go test ./internal/lab/leaselab -run 'TestS' -count=1 -v -timeout 120m   # full form: 28 min
go test -race ./cmd/mglease -run Sweep -count=1
go run ./cmd/mglease sweep list
go run ./cmd/mglease sweep all out=/tmp/lease-sim jobs=4                   # the campaign (section 5)
go run ./cmd/mglease sweep all out=/tmp/lease-smoke seeds=1 scale=10 jobs=4  # the smoke (section 6)
```

The full form is reachable by the environment variable `LEASE_E3=full` (S1 with 200 seeds, S2
with 20, the whole S3 grid with W up to 8, every S6/S7/S8 cell, the sweeps' own seeds for
S9–S11). `-short` keeps a few cells of each test. Every test prints its table with `-v`.

Every S-test, in every size: each run goes through `RunSim`, whose own checks (cap, K14,
conservation, a lease unanswered at quiesce, the simulated world's quiesce) fail the test with
the run's `REPLAY:` line; `quiet` then asserts nothing lent, waiting, pending, connecting,
priming, resetting or closing, no lease in flight, no backend left in the fake after the
shutdown and no connect-model job; each assertion message ends with the run's `REPLAY:` line;
each test ends with `replayLaw` (one of its configurations run twice: `metrics.csv`,
`histograms.csv`, `run.csv`, `lab.csv`, `oracle.csv` byte-identical); each test asserts the
counters of what it exercises are non-zero.

## 3. Decisions the brief left open

1. **The warm-stock miss rate** is REVIEW-E2-2 finding 2's: `stock_empty_free_slot /
   (switch_arrivals − (stock_empty_arrivals − stock_empty_free_slot))` (`Summary.ModelMiss /
   ModelTrials`). The prediction uses **m measured by Little's law**: (connecting + priming +
   closing time) / arrival window — the mean number of replacements under way, closing ones
   included because under close-then-open a connection closing to make room is the replacement
   under way. λ and L are reported beside it (L = m/λ). The orders queued behind K14 (not yet
   connecting) are not in m; that is one of the stated domain limits.
2. **S3's band**: 3 binomial standard deviations of the predicted p over the run's trials,
   plus 0.2 percentage points (discreteness at small p and small n). Stated in
   `stock_scenario_test.go` (`zMilli`, `floorPP`). Trials per cell: 1,600–6,400 (default),
   3,300–9,600 (full; the low rates run up to 600 s).
3. **S3's asserted domain** (`inModelDomain`; the "connect CPU ≤ 50%" line was drawn AFTER
   seeing the grid — it is empirical; section 11.3): (a) W < K14, or P(Poisson(m) ≥ K14) ≤ 2%, and
   (b) connects use at most 50% of the modelled CPU (`connects × (work + close work) / (cores ×
   window)`). Cells outside are run and reported, not asserted: they are where the model
   breaks, which is S4's question asked inside S3's grid. Reason for (b): the full grid showed
   the model holding to m ≈ 1.6 (connect CPU 46%) and failing from m ≈ 2.2 (62%) — section 7.
4. **S3 runs at 10,000 uniform tenants**, so nearly every lease is a tenant switch and the
   fresh-connection rate is set by the load (about 40/s per 1% of load at cap 20, medium
   leases). m = λ·L runs 0.1–3.6; K14 = 4 caps it near 4.
5. **S5's rule — superseded in the repair round (section 11.2).** It was: point estimate ≤ 1%
   per seed for fixed W, ε + 3σ for R1 (asymmetric; REVIEW-E3-1 finding 2). Now: the §10 rule,
   the same for R1 and every fixed W — pooled over seeds, steady-state window [10 s, end], 95%
   Wilson band. W_min of the dynamic arm is 0 (`warm.size=0`): the rule alone sets the target.
6. **S6's corrected formula** (`PredictCapacity`): a lease holds its connection for
   h0 = S + 2 round trips + the short statement's 21 µs (the `plain` script is two statements);
   a reset adds r = round trip + 89 µs; ceiling = cap / (h0 + resets per lease · r + connects per
   lease · (close lag + connect + fresh-backend penalty)), and connects cannot exceed the
   connect model's CPU ceiling cores / (work + close work) = 173.9/s. The plan's S/(S+r) is
   printed beside it. The ceiling is read as window goodput at 120% offered load over
   [d/5, d] (cold start and drain excluded). Band: ±5% (none, reset-*), ±10% (remake-*).
   **For the remake policies the per-lease connect fraction is measured in the same window**,
   so at the CPU ceiling the agreement is close to circular: it shows the run sits at the
   connect ceiling, not an independent prediction of where it gets there.
7. **S8's starvation check**: no lease waits longer than 1 s + twice the time the pool takes
   to serve (wait-list high-water + cap) leases at its window goodput — the FIFO bound. A
   tenant skipped while others are served would exceed it. S8 cells use `pool.wait_cap=200` (so
   overload shows as refusals and a bounded queue, and the core's O(waiters × connections) step
   stays fast).
8. **S9's "no connect storm"**: connects refused while the database is down ≤ K14 × (restart
   length / connect retry + 2). Connects in flight never above K14 is checked by the engine at
   every step (a violation fails the run). "The restart was seen" = `db_lost` > 0 or a connect
   refused: under `remake-always` at 3% load no connection may be open at the restart instant
   (seed 2 of the full form), so `db_lost` stays 0 and only refusals show it.
9. **S10's "never hang"**: no lease waits longer than stall length + connect deadline + 2 s;
   goodput in the last 2 s ≥ 85% of the leases requested in them.
10. **S11's stated overlap is 0** for open-then-close: the core counts a closing connection
    against the cap until its close is confirmed (NOTES-E1 decision 9), and the fake drops the
    session before the close lag ends. The churn cells must reach cap − 1, so the bound is
    tested at its edge (all reach 20).
11. **S12**: W = 0's un-ablated twin is `fixed:8` (the hindsight W of S5 steady on the original
    ladder; on the repaired ladder hindsight is 5, and fixed:8 still holds ε, so the twin stands); the claim
    "a new tenant gets a warm connection at rate ≥ 1−ε" uses S5's band. "Rule frozen" = the
    dynamic arm with both EWMA time constants at 3,600 s (`ArmFrozen`): the rule runs but λ̂
    cannot follow; its claim = "holds ε and the target rises in the ×4 third". "Reset skipped" =
    `none` beside `reset-always` and `reset-on-switch` on S1's marker cell (the reset policies
    minus their reset is exactly `none`); claim = no cross-tenant pair without a reset and no
    marker read.
12. **Canary** (`TestS1CanaryPolicyUnwired`): the planted bug `e3-canary-policy-unwired` is an
    inspector factory, registered only from the test file, that sets the pool core's policy to
    `none` before the core is built — "the configured release policy never reaches the core".
    S1's check fails for all five non-baseline arms with it planted (728 violations under the
    reset policies, 20 under remake-on-switch, 7 under remake-always, seed 1). It shows S1
    reads the fake's records, not the configuration.
13. **`remake.patience`**: every remake-on-switch arm runs with `auto` (the arm under test,
    named `.pauto`) and, as a labelled extra arm, `0` (`.p0`); never mixed in one arm.
14. **`warm.prime`**: `none` and `select1` (arm suffix `.prime`) are compared on the
    remake-on-switch runs of S3 (W = 1, 2, 4) and S7.
15. **`remake-always`** appears only as `remake-always.cold` (S1, S2, S3's control, S6 incl.
    its knee grid, S9, S10) and as the one warm run `remake-always.fixed4` (S6, medium, 120%).
16. **Flushes**: one at the end of arrivals (interval = run length) for most cells; d/5 for
    S6–S8 (a warm-up boundary, so wait quantiles and rates can be taken over [d/5, d] by
    differencing the cumulative histograms); **10 s for every S5 run** (repair round: the
    steady-state window [10 s, end]); 4 s on S12's `step` (ten points per third); 1 s in S9;
    2 s in S10.

## 4. Claims and the tests that prove them

All in `internal/lab/leaselab` unless marked. Times: default size, the whole package run
(`go test ./internal/lab/... ./cmd/mglease/...`, 4 vCPUs, nothing else running), then the full
form (`LEASE_E3=full`, run alone).

| ID | Test | What it asserts | Result | Default / full |
| --- | --- | --- | --- | --- |
| S1 | `TestS1StructuralIsolationFromFakeLog` | Per backend (BackendKeyData pid), consecutive leases in grant order from `oracle.csv`; `reset_between` from the fake's own DISCARD ALL records. Reset policies: no cross-tenant pair without a completed reset. remake-on-switch: no backend serves two tenants. remake-always: none serves two leases. **`none` must fail on every seed** (it does). reset-on-switch must show same-tenant pairs with no reset (it does), reset-always none. 6 arms × 20 seeds (full: 200) | PASS | 39 s / 403 s |
| canary | `TestS1CanaryPolicyUnwired` | S1 fails for every non-baseline arm with the policy unwired | PASS (caught 5 of 5) | 1 s |
| S2 | `TestS2SettingMarkerOnTheWire` | `SET lab.marker` per lease, `SHOW` by the next: under reset and remake policies no marker of another tenant read and none present on a cross-tenant lease; under `none` it is read (5 and 50 tenants, affinity on/off); positive control: a tenant reads its own marker back under none, reset-on-switch, remake-on-switch; `canary_reads{leak}` = rows | PASS | 20 s / 156 s |
| S3 | `TestS3MissRateAgainstPoisson` | Measured miss inside P(Poisson(m) ≥ W) ± band in every in-domain cell (W 1, 2, 4; full also 8; patience 0 and prime arms); remake-always cold control: every switch misses | PASS (full: 26 in-domain cells, all in band; 22 out-of-domain cells reported, 4 of them in band anyway) | 21 s / 49 s |
| S4 | `TestS4WhereTheModelBreaks` | Bursty, high utilisation, connects at half CPU with 16 in flight: the gap table; sanity only | PASS | 15 s / 30 s |
| S5 | `TestS5RuleAgainstHindsightFixed` | Repaired (section 11.2): the §10 rule — pooled over seeds, steady-state window, 95% band; R1 must not miss where hindsight-fixed holds; bursty reported; whole-run and steady-state rates, ratio and the `step` series (10 s) printed | PASS (default 3 seeds auto; full 5 seeds, both patiences) | 76 s / see 11.6 |
| S6 | `TestS6CapacityKnee` | Window goodput at 120% within the band of `PredictCapacity`; below the predicted knee goodput ≥ 97% of offered; remake-always warm ceiling within 5% of cold | PASS | 25 s / 245 s |
| S7 | `TestS7CostMap` | The map (table); each policy exercised (resets, connects, switches) | PASS | 35 s / 320 s |
| S8 | `TestS8TenantScaleNoStarvation` | No wait above the FIFO bound; switch fraction, switch rate (and against 195/s), connects/s, reuse share, switch closes per policy | PASS | 9 s / 161 s |
| S9 | `TestS9ColdStartAndRestart` | Restart seen; no connect storm; goodput in the last 2 s ≥ 90% of requested; cap on both sides; prefill under none/reset is one-shot (no stock after the restart) | PASS (full: after the decision-8 fix) | 16 s / 57 s |
| S10 | `TestS10ConnectFaults` | Refused and timed-out connects both seen under remake; no wait above the bound; goodput returns; quiesce clean | PASS (full: after the same fix) | 17 s / 61 s |
| S11 | `TestS11ServerPeak` | Fake's peak backends ≤ cap (close-then-open) and ≤ cap + 0 (open-then-close), reaching cap − 1 | PASS | 16 s / 38 s |
| S12 | `TestS12Ablations` | W = 0, rule frozen, reset skipped: each ablated run fails its arm's claim, each un-ablated twin passes | PASS | 8 s / 11 s |
| labels | `TestSweepLabelsUniqueForTheReport`, `TestSweepLabelsCanaryOldLabels`, `TestWilsonBand` (`labels_scenario_test.go`, repair round) | (campaign, test, arm, load, seed, rep) unique over the campaign; one workload per comparison; the `none` baseline in every group the report compares with it; the old labels are caught | PASS | 0.1 s |
| sweeps | `cmd/mglease` `TestSweepLabelsAndDeterminism`, `TestSweepUsage` (also `-race`) | One directory per run with `run.campaign/test/arm/rep`, seed = rep for every arm, `run.result=ok`; files byte-identical between `jobs=1` and `jobs=4`; usage errors | PASS | 3 s |

Package totals: `leaselab` 310 s default (E2's tests were 62 s of it), `leasepool` 9 s,
`mglease` 5 s; `-short` S-tests 62 s; full-form S-tests 28 min. 14 new top-level tests.

**Full-form failure, recorded:** the first full run failed S9 and S10 on `remake-always.cold`
seed 2 only (`db_lost` 0: no connection was open at the restart instant). The check was wrong,
not the pool (decision 8); fixed in `d1bea83`; both tests re-run in full form and pass.

## 5. The sweeps (deliverable B)

`mglease sweep <name> out=<dir>` writes `<dir>/<name>/<run id>/` (the E2 run-directory
contract: `metrics.csv`, `histograms.csv`, `run.csv`, `lab.csv`, `oracle.csv`; no
`leases.csv`) and `<dir>/<name>-summary.tsv` (one line per run, `SummaryHeader`; outside the
run directories). Run id = `<s-number>-<arm>-<cell>-r<rep>`; `lab.csv` carries
`run.campaign` (default `e3-sim`), `run.test` (the S-number and the comparison cell, e.g.
`S8/t10-affon`, `S6/short`, `S5/bursty`, plain `S3` — repair round, section 11.1), `run.arm`,
`run.rep` and every config
key, so the report SQL needs nothing from this code. **Every arm of one comparison has the same
seed (seed = rep).** `jobs=J` runs J simulations on goroutines in one process; each run is its
own deterministic simulation (same bytes on 1 or 4 goroutines, tested). `scale=D` divides every
run's length by D (never below 1 s; runs already shorter keep their length). Exit 1 if any run
violated a check (its REPLAY line is printed).

Common: cap 20, K14 4, medium leases, `plain` script, steady unless said, ε 1%, seeds = rep.
"rmos" = remake-on-switch; `.pauto` / `.p0` = `remake.patience`.

| Sweep | Arms | Cells (loads, profile) | Seeds | Runs | Feeds |
| --- | --- | --- | --- | --- | --- |
| `s3` | rmos fixed 0/1/2/4/8 `.pauto`; fixed 1/2/4 `.p0`; fixed 1/2/4 `.pauto.prime`; `remake-always.cold` | steady, 10,000 tenants, load 0.14/0.34/0.68/1.35/2/2.7/3.4/4% (≈ 5.6–160 switches/s), 600/600/360/180/120/100/80/60 s | 2 | 192 | Table 6 (miss rate against W, Poisson beside it) |
| `s4` | rmos fixed 1/2/4/8 `.pauto`; fixed 2 `.p0` | bursty 10,000 tenants, 10 or 30 tenants per period, 1.35%, 120 s; steady 20 / 50 tenants at 80 / 95%, 20 s; steady 10,000 tenants at 0.68/1.35/2% with `cm.avail_pm=500`, K14 16, 120 s | 2 | 90 | Table 6 (the gap where the model breaks) |
| `s5` | rmos dynamic (W_min 0) and fixed 0/1/2/3/4/5/6/8 (bursty also 16), `.pauto` only (repair round) | steady 1.35%; step 0.68% ×4; bursty 10 tenants/period 1.35% — all 10,000 tenants, 120 s, flush 10 s | 3 | 84 | Table 7 (R1, hindsight-fixed, warm-connection-seconds; target against load on step); the §10 verdict by the rule of 11.2 |
| `s6` | none, reset-always, reset-on-switch, rmos fixed2 `.pauto`/`.p0`, `remake-always.cold`; plus `remake-always.cold` at 2–8% (medium) / 0.2–0.8% (short), 20 s; plus `remake-always.fixed4` medium 120% | steady 100 tenants, short (4 s) and medium (10 s) leases, 30/60/80/95/120%, wait cap 200, flush d/5 | 1 | 73 | Table 4 (capacity; knee beside the formula) |
| `s7` | none, reset-always, reset-on-switch, rmos fixed2 `.pauto`/`.p0`/`.pauto.prime` | skewed 100 tenants and steady 1,000 tenants × short (3 s) / medium / heavy (10 s) × 30/60/80%, wait cap 200, flush d/5 | 1 | 108 | Table 5 and the cost map (overhead over the baseline) |
| `s8` | none, reset-always, reset-on-switch, rmos fixed2 `.pauto`/`.p0` | skewed 10 / 100 / 10,000 tenants × affinity on/off × 60/80%, 10 s, wait cap 200, flush d/5 | 1 | 60 | Switch-rate budget (switches/s against 195/s and 174/s; connects/s; reuse share; switch closes) |
| `s9` | none, reset-always, reset-on-switch, each cold and `.fixed4` (prefill); rmos cold/fixed2/dynamic × `.pauto`/`.p0`; `remake-always.cold` | restart at 4 s for 1 s in 10 s, flush 1 s; steady 100 tenants 60% (none/reset), skewed 10 tenants 30% (rmos), steady 100 tenants 3% (remake-always) | 3 | 39 | Recovery rows of the verdict table |
| `s10` | rmos fixed2/dynamic × `.pauto`/`.p0`; `remake-always.cold`; none, reset-always | faulty (restart, stall, refuse, slow at 20/40/60/80% of 20 s), flush 2 s | 3 | 21 | Recovery rows |
| `s11` | rmos fixed4 `.pauto`/`.p0`, dynamic `.pauto`, each under both `remake.order` | skewed 10 tenants 60% (10 s); steady 10,000 tenants 2% (30 s) | 2 | 24 | Server-side peak (`db_backends_hw` against the cap) |
| `s12` | rmos fixed0 vs fixed8 (steady 1.35%), dynamic vs `.frozen` (step), each `.pauto`/`.p0`; none vs reset-always vs reset-on-switch (marker, `oracle.rate=1`, 5 tenants, 2 s) | as named | 3 | 33 | Ablation rows of the verdict table |

**Totals:** superseded — see section 11.5 (724 runs after the repair round).

## 6. Results

### 6.1 Campaign duration and size

`mglease sweep all out=/tmp/zgw-lease-e3/full jobs=4` with the final grids (commit `d1bea83`),
2026-11-26, 4 vCPUs shared with other agents' work: **748 runs, 0 failed, 11 min 12 s wall**
(29 min of CPU); **851 MB** on disk, no `leases.csv`.

| Sweep | Runs | Wall | Size |
| --- | --- | --- | --- |
| s3 | 192 | 146 s | 85 MB |
| s4 | 90 | 242 s | 58 MB |
| s5 | 108 | 60 s | 171 MB (36 step runs flushed every 4 s) — pre-repair grid; now 84 runs (11.5) |
| s6 | 73 | 63 s | 68 MB |
| s7 | 108 | 75 s | 134 MB |
| s8 | 60 | 37 s | 78 MB |
| s9 | 39 | 15 s | 106 MB (flushed every 1 s) |
| s10 | 21 | 15 s | 74 MB |
| s11 | 24 | 11 s | 15 MB |
| s12 | 33 | 9 s | 65 MB |

An earlier run of the same grids with 1 s / 500 ms flushes on step and S9 wrote 1.6 GB in
11 min 2 s; the flushes were coarsened for size (decision 16). Most of the bytes are
`histograms.csv` (cumulative, every non-empty bucket at every flush) and the simulator's own
sources in `metrics.csv`. The campaign is inside the brief's 15–30 minutes with room for
`seeds=` larger than the defaults (wall time scales linearly with seeds).

### 6.2 Smoke result (deliverable C): `sweep all seeds=1 scale=10 jobs=2`

461 runs, 0 failed, 2 min 15 s wall (other work was running), 293 MB. Observed numbers, not
verdicts. Runs at scale 10 are 1–60 s long, so the cold start weighs more than in the
campaign; the S3/S5 numbers below from the default and full test runs (same configurations at
full length) are given beside where they exist.

- **S3** (rmos `.pauto`, measured miss / trials-weighted; Poisson prediction from the measured
  m in the full-length test run in brackets): W = 2 at m 0.51: 10.2% (full: 10.3% against 9.6%);
  at m 1.03: 27.7% (28.3% against 28.0%); at m 2.2: 66.5% (66.5% against 64.5%, outside the
  band). W = 4 at m 1.03: 3.6% (2.3% against 2.2%); at m 3.45: 69.6% (76.3% against 47.7%).
  W = 8 at m 3.47: 29.9% (41.3% against 2.9%). Mean connect 18.5 ms at 27 switches/s, 19.6 ms
  at 108/s, 21.2 ms at 160/s. Cold control: 100% at every load.
- **S4**: bursty, 10 tenants arriving together each second, ≈ 11 switches/s, m 0.23: W = 2 misses
  82.7% against 2.2% predicted, W = 4 66.4% against 0.01%. High utilisation (20/50 tenants,
  80–95%): every model trial misses at every W, and most switch arrivals find the cap full
  (test, 20 tenants at 95%: 17,351 arrivals with the stock empty and no slot free, besides the
  17,056 trials). Half CPU and K14 16: connects take 33–70 ms instead of 18.5; the model still fits
  where the CPU is not saturated (W = 4 at m 2.0: 14.6% smoke, 17.6% against 17.1% in the test).
- **S5** (pre-repair grid and rule; superseded by section 11.2) (12 s smoke runs; the test's 120 s runs in brackets): R1 steady 3.4% (0.49–0.65% over 3
  seeds), step 2.1% (0.84–1.03%), bursty 72.5% (68.4–68.9%); hindsight-fixed W = 8 on steady
  and step (0.02–0.06% and 0.49–0.72%), none on bursty. Warm-connection-seconds R1 / hindsight:
  0.589–0.590 (steady), 0.626–0.630 (step).
- **S6** at 120% (window goodput/s; full-length test in brackets, against the formula): none
  short 27,073 (28,713 against 28,724), medium 3,643 (3,867 against 3,849); reset-always short
  21,315 (22,713 against 22,903), medium 3,529 (3,725 against 3,722); reset-on-switch the same
  as reset-always within 1% (100 uniform tenants: 92–100% of leases need a reset); rmos fixed2
  short 924 (1,002), medium 849 (930), 179 connects/s; remake-always cold 171–172 (173.4–173.8
  against 173.9); remake-always fixed4 171 = cold. Knee (smoke) of remake-always medium: 76.5/s
  at 2% offered (80/s), 149.5/s at 4% (160/s), 171/s at 8%.
- **S7** (skewed 100, 80%; goodput/s, p99 wait ms, refusals): short — none 26,972 / 20.7 /
  4,629; reset-always 21,344 / 27.7 / 10,249; reset-on-switch 21,618 / 27.1 / 9,976; rmos
  2,359 / 382 / 29,248 (prime 2,258). Medium — none 3,133 / 50.6 / 0; reset-always 3,131 / 51.9 /
  0; reset-on-switch 3,130 / 51.9 / 0; rmos 1,522 / 302 / 1,422 (prime 1,612 / 340 / 1,331).
  (The p99 in short runs includes the cold start; see limit 4.)
- **S8** (60%, 1 s smoke runs): tenant switches/s under none 1,012 (10 tenants, affinity on) to
  2,276 (10,000, off); reset policies the same ±5%. rmos `.pauto` at 10 tenants affinity on: 68
  switches/s, 70 connects/s, goodput 2,333/s of 2,340; `.p0` there: 175 switches/s, 179
  connects/s. Every other rmos cell (affinity off, or 100 / 10,000 tenants) sits at 171–175
  switches/s, 175–179 connects/s, goodput 184–1,523/s, p99 wait 0.3–1.1 s, 621–1,964 refusals.
- **S9**: every arm: the restart seen, goodput = requested over the run, p99 wait 31–40 ms,
  server peak ≤ cap. rmos fixed2 keeps its target of 2 after the restart; none/reset `.fixed4`
  end with target 0 (one-shot prefill; the test asserts stock and target 0).
- **S10**: remake arms: 12 connects failed per run (refused + timed out), max wait 2.2–2.3 s
  (bound 6 s), goodput 2,346–2,347 of 2,350 (the rest ended with the restart, not goodput);
  none/reset-always: 8 failed, max wait 0.33 s, 4,789 of 4,801.
- **S11**: `db_backends_hw` = 20 = cap in all 12 runs, both orders.
- **S12**: fixed0 100% misses, p50 wait 19.3 ms (fixed8 test: 0.03%); frozen rule 71.0% misses,
  target 1 (dynamic 2.1% in 12 s; 0.85% in 120 s); none: 728 cross-tenant pairs, all without a
  reset, 728 markers read; reset-always 804 / 0 / 0; reset-on-switch 749 / 0 / 0.

## 7. Findings (for E6/E8; observations, not verdicts)

1. **The warm-stock model holds inside its assumptions and departs where the plan said it
   would.** In the full S3 grid every in-domain cell (26 cells, m 0.1–1.6, connect CPU ≤ 46%,
   K14 rarely binding) is inside 3σ + 0.2 pp; the patience-0 and prime arms too. At m ≈ 2.2 (108 switches
   per second, connect CPU 62%) the measured miss is 1.9–4.3 pp above Poisson; at m ≈ 3.5 (160/s,
   92%) W = 8 misses 41% against 2.9%. Two causes, both the plan's limit 2: K14 caps
   replacements in flight at 4, and connects queue for the 3 modelled cores, so L grows with the
   load (18.4 ms at 13/s → 19.6 ms at 108/s → 21.4 ms at 160/s).
2. **Corrected in the repair round (section 11.2).** The original "R1 holds ε with about 0.6×
   the idle stock of hindsight-fixed" was an artefact of the coarse ladder {0, 1, 2, 4, 8}
   (hindsight 8 on steady). With W = 3, 5, 6 added and the §10 rule, hindsight on steady is
   W = 5 and R1's ratio is **1.047** (still inside 1.5×); on step hindsight stays 8, ratio 0.649. On bursty nothing holds ε (R1 68–69%, fixed:8 23%): ten tenants arrive at
   one instant every second, and no stock of ≤ 8 covers a batch of 10.
3. **R1's misses are a fixed cost at the cold start and at the step edge** (step seed 1, 55 misses in
   all: 7 of 102 trials in the first 4 s; 37 of 1,719 in the 16 s after the step to ×4; 11 in
   the other 100 s), so the ε claim depends on
   the run's length: the same step cell misses 1.64% over 60 s and 0.85% over 120 s. S12 and S5
   therefore use the campaign's 120 s in every size. The ×4 level (≈ 108 switches/s) is also
   where finding 1's departure starts.
4. **The plan's S/(S+r) is wrong for short leases; corrected in `PredictCapacity`.** The pool
   holds a connection for h0 = S + 2 round trips + 21 µs, not S (the `plain` lease is two
   statements, each a round trip). Corrected ratio reset/baseline = h0/(h0 + r): short leases
   79.7% (measured 79.1–79.4%; plan 73.9%), medium 96.7% (measured 96.3–96.6%; plan 96.6%).
   The baseline itself saturates at 71.8% of the nominal load for short leases (28,724/s) and
   96.2% for medium; reset-always at 57.3% and 93.1%. Every measured ceiling at 120% is within
   1% of the corrected formula.
5. **remake-on-switch with 100 uniform tenants is at the connect ceiling at every load in the
   grid** (≈ 930/s medium, ≈ 1,000/s short, 179 connects/s; knee ≈ 23% medium, 2.5% short), and
   with 10,000 tenants or affinity off it carries only 184–422 leases/s at 60%. With 10 skewed
   tenants and affinity on it carries 60% (`.pauto`: 70 connects/s; `.p0`: 179/s). The switch
   rate is the whole story: it stays under the ceiling only when tenants are few and sticky.
6. **`.pauto` and `.p0` give byte-identical counters in many cells**: wherever no tenant holds
   a connection when its next lease arrives (10,000 tenants, or affinity off), patience never
   applies. They differ with few, sticky tenants (S8 t10 affinity on: 68 against 175
   switches/s).
7. **`remake.order` made almost no difference**: in 9 of the 12 pairs of the full S11 run (4 of
   6 for seed 1, 5 of 6 for seed 2) the two orders give identical counters; in the rest connects
   differ by under 2% (dynamic on skewed 10 tenants: 690 against 678). Under remake-on-switch a
   connection is closed for a switch only when no slot is free, so open-then-close has no spare
   slot to open into. The order matters for remake-always with spare slots,
   which the narrowed arm set does not run warm.
8. **The one warm remake-always run does not move the ceiling**: fixed:4 at 120% gives the same
   173.4 leases/s and the same waits as cold; at overload every new connection goes to a waiter.
9. **`warm.prime=select1`** lengthens the replacement pipeline by about 0.6 ms (L 19.3 → 19.9
   ms at 13/s), raising the miss rate slightly (W = 2 at m 0.52: 10.8% against 10.3%), and lowers
   the first-statement cost the lease sees (E2's numbers). In S7 it changed rmos goodput by
   −4% to +6% (noise-level at these lengths).
10. **reset-on-switch saves almost nothing in these mixes**: 70–100% of leases are cross-tenant
    under 100 tenants (skewed or uniform) and 10,000 tenants; only S8's 10 tenants with affinity
    on bring it to 40%. Its cost then equals reset-always's within 1%.

## 8. Deviations

- **Size**: 987 lines of code and 1,168 of tests against "700 lines of tests". The sweep grids
  and the capacity formula are code because the command and the tests share them.
- **Default `go test` of `leaselab` now takes about 310 s** (62 s before). The S-tests are
  behind `-short` (62 s) as the brief allows; the full form needs `LEASE_E3=full` and
  `-timeout 120m`.
- **S3's assertion is restricted to a stated domain** (decision 3); cells outside are reported.
- **S6's remake formula uses the window's measured connect fraction** (decision 6), so for the
  remake policies it is a consistency check at the CPU ceiling, not an independent prediction.
- The S12 claim for "rule frozen" is evaluated over 120 s in every size (finding 3).
- No `scripts/lease-exp.sh sim-sweeps`: `mglease sweep` covers it (the brief's "or"), and the
  script file may be E5's.
- Commit trailer: the rules file's `Co-Authored-By: Claude <noreply@anthropic.com>`.

**Changes to E1/E2 files: none. Change request: none.** Nothing frozen was touched.

## 9. Limits

1. The simulator's connect ceiling is 174/s against 195/s measured (E2 §6.1), so every remake
   ceiling here is about 11% low; lease work does not load the connect CPUs in the model.
2. The binomial band treats trials as independent; misses cluster in time (a burst empties
   the stock), so the true spread is wider than the band. The +0.2 pp floor partly covers it;
   in-domain cells passed in both the default and the full grid, not because the band is
   generous by design.
3. m is measured, not predicted from λ and L separately; replacements waiting behind K14 are
   outside it (decision 1). The test validates P(Poisson(m) ≥ W) given m, which is what the
   rule uses (it estimates λ̂ and L̂, whose product is m).
4. Wait quantiles in the test tables and in `-summary.tsv` come from cumulative histograms and
   include the cold start (≈ 20 connects in the first 20–40 ms); in short runs that inflates
   p99 (e.g. none, medium, 60%: p99 16–37 ms while p50 is ≤ 1 µs). The campaign's S6–S8 runs
   flush at d/5 so the report can difference the histograms over [d/5, d].
5. Refusals are reported beside every wait quantile in the test tables and the summaries
   (refused leases are in no histogram).
6. The fixed ladder is {0, 1, 2, 4, 8} in S3/S4; S5 uses {0, 1, 2, 3, 4, 5, 6, 8} and 16 on
   bursty (repair round). Hindsight-fixed resolves to one step of that ladder (W = 7 is not run).
7. S9's database restart closes every connection (E2 §7.5); S10's faults are the faulty
   profile's defaults (windows of at most a tenth of the run).
8. Determinism holds for the same commit, toolchain and linux/amd64 (as E2).

## 10. Commits

On `exp/lease-lab-e3`, after `4429d1a`:

- `1ec8831` lease-lab: add the scenario summary, the Poisson and capacity predictions, and the S3-S12 sweep grids
- `e635cb6` lease-lab: add mglease sweep, running a campaign sweep's runs on parallel goroutines
- `eebdc96` lease-lab: add the simulator scenario tests S1-S12 and the policy-unwired canary
- `d1bea83` lease-lab: coarsen campaign flushes, align S12 with fixed:8, accept refused connects as a seen restart
- this notes file

Checks at the end: `gofmt -l .` empty; `go build ./... && go vet ./...` clean;
`go test ./internal/lab/... ./cmd/mglease/... -count=1` green (leaselab 289 s, leasepool 9 s,
mglease 5 s); `go test -race ./cmd/mglease/...` green; `go test ./internal/sim/...
./internal/obs/... ./internal/pgwire/...` green, untouched. No container used. Scratch
`/tmp/zgw-lease-e3/` deleted (the campaign and smoke directories were in it; nothing bulky is
committed).

## 11. Repair round

Brief `/root/briefs/zgateway-lease-e3-repair.md`; review `REVIEW-E3-1.md` (verdict FAIL, narrow).
Branch `exp/lease-lab-e3` with E6 merged (`e9b68ca`). Code commit `964696a`. Only the listed
items were changed; no E1/E2 file and no E6 SQL file was edited. Sweeps ran at `jobs=2`.

**Merge defect found and fixed on E3's side.** After the merge of E6 the `leaselab` test package
did not compile: E6's `report_test.go` and E3's helpers both declare `table` and `pick`. E3's
were renamed (`logTable`, `bySize`); E6's file is untouched.

### 11.1 Unique labels (finding 1)

- `run.test` = the S-number, `/`, and the comparison cell: the cell's name without its load
  part (`CellGroup`, `TestLabel` in `scenario_sweeps.go`). Examples: `S8/t10-affon`, `S6/short`,
  `S6/medium-knee` (remake-always's 20 s knee grid, its own comparison), `S4/cpu50`,
  `S4/high-t20`, `S4/bursty-a1`, `S5/steady|step|bursty`, `S7/skewed100-heavy`, `S9/steady100`,
  `S10/t100`, `S11/skewed10`, `S12/marker`; S3 has one cell family and keeps plain `S3`. Arms
  compared with one another share campaign, test, load and seed and differ in `run.arm`. Run ids
  are unchanged.
- Tests (`internal/lab/leaselab/labels_scenario_test.go`):
  `TestSweepLabelsUniqueForTheReport` — over every sweep's grid at the default seeds, at the
  smoke size and at `seeds=5`: (campaign, test, arm, load, seed, rep) unique; within one
  (campaign, test, load) every run holds the report's workload keys (profile, tenants, skew,
  lease, stmt class, script, cap, duration) fixed; every (campaign, test, load, seed, rep) group
  of S6 (except the knee grid), S7 and S8, and every group holding a reset policy anywhere, holds
  the baseline `none` (cold). `TestSweepLabelsCanaryOldLabels` — the same check on the old
  labels (`run.test` = the bare S-number) finds 199 duplicate keys at the smoke size, S5's
  bursty among them: the check can fail.
- **Proof** (smoke: `mglease sweep all out=/tmp/zgw-lease-e3r/smoke seeds=1 scale=10 jobs=2`,
  453 runs, 0 failed, 1 min 55 s wall on a shared host, 289 MB; then
  `scripts/lease-report.sh /tmp/zgw-lease-e3r/smoke /tmp/zgw-lease-e3r/report baseline_arm="'none'"`,
  exit 0, 13 s). Table 0, verbatim:

  ```
  severity,dir,check,detail

  (no rows)
  ```

  Row counts (CSV rows without the header): 00-validity 0, 01-calibration 45, 02-isolation 33,
  03-cost 630, **04-capacity 453, 05-waiting 906, 06-warm-stock 261, 07-rule 288,**
  07b-rule-step 23, 08-sim-vs-real 0 (no real tier), **09-verdict 813, 10-switch-budget 453.**
  Per S-test (rows in table 4 / 5 / 6 / 7 / 9 / 10): S3 96/192/72/88/252/96, S4 45/90/45/45/75/45,
  S5 28/56/25/28/90/28, S6 73/146/43/21/45/73, S7 108/216/36/54/108/108, S8 60/120/24/24/90/60,
  S9 13/26/5/4/43/13, S10 7/14/3/4/25/7, S11 12/24/4/12/44/12, S12 11/22/4/8/41/11. Table 4 now
  has all 73 S6 rows (was 43) and all 60 S8 rows (was 30), each with its ratio to the baseline;
  table 5 has the baseline p99 beside every S6/S7/S8 row; table 7 has the `S5/bursty` rows
  (R1, every W). Table 7b has 3 S5 rows at the smoke size (12 s runs, 10 s flushes) and 20 S12
  rows (4 s flushes); at 120 s S5's step gives 12 points.

### 11.2 S5 by the §10 warm-stock rule (finding 2, the carry-forward)

- Sweep `s5`: 120 s runs, 3 seeds (`seeds=5` for more), patience `auto` only (the rule's arm;
  at 10,000 tenants `.p0` gives identical counters, finding 6 — the labelled `.p0` extra is
  dropped here by the repair brief's "patience auto"), `obs.interval=10s` for S5 runs only
  (`S5IntervalNs`; S12's step cell keeps its 4 s via `S12StepCell`). Ladder
  {0, 1, 2, 3, 4, 5, 6, 8}; **16 on bursty only** (the one cell where a stock above 8 can
  matter — ten tenants arrive at once — and the 1.35% load leaves 16 of 20 slots free; on steady
  and step W = 8 already holds, so 16 would add nothing). 28 runs per seed, 84 in all.
- Test `TestS5RuleAgainstHindsightFixed`: per cell and arm, counts pooled over seeds; whole run
  (final counters, cold start included) and steady state [10 s, 120 s] (differenced from the
  flushes: `Flushes.Stock`). Band: 95% Wilson score interval (`WilsonPPM`, z = 1.96; integer
  arithmetic; chosen over p ± 1.96σ because it has width at zero misses). `EpsVerdict`: holds
  if upper ≤ ε, misses if lower > ε, else inconclusive. Hindsight-fixed = smallest W that
  holds. Fails only if R1 *misses* where hindsight holds; bursty printed, not asserted. Sizes:
  short = steady, 1 seed; default = 3 seeds, auto; full (`LEASE_E3=full`) = 5 seeds, auto and 0.
- Results (steady-state window, pooled; ratio = R1 / hindsight warm-connection-seconds on the
  same window):

  | Cell | Seeds | R1 ss miss (x/n) | 95% band | R1 verdict | Hindsight W (ss miss) | Ratio | R1 whole run |
  | --- | --- | --- | --- | --- | --- | --- | --- |
  | steady | 3 | 0.369% (66/17,871) | 0.290–0.469% | holds | 5 (0.464%) | 1.047 | 0.581% |
  | steady | 5 | 0.355% (105/29,528) | 0.293–0.430% | holds | 5 (0.440%) | 1.040 | 0.540% |
  | step | 3 | 0.831% (156/18,766) | 0.711–0.971% | holds | 8 (0.564%) | 0.649 | 0.904% |
  | step | 5 | 0.898% (279/31,065) | 0.799–1.009% | **inconclusive** | 8 (0.589%) | 0.646 | 0.940% |
  | bursty | 3 | 68.3% | 66.8–69.8% | misses (reported) | 16 (0/11,886) | 0.225 | 68.7% |

  Every fixed W at or below 4 (steady), 6 (step), 8 (bursty) misses by the rule. Patience 0
  (full form) gives the same counts within a few trials. The test passes in every size: R1
  never *misses* where hindsight holds. **Corrections to earlier text:** the 0.6× ratio on steady
  (finding 2) was the coarse ladder's; with W = 5 run it is 1.04–1.05×, still inside §10's
  1.5×. On step R1 sits just under ε: holds at 3 seeds, inconclusive at 5 (upper bound 1.009%);
  E7 should state this, not round it to a pass. The cold-start cost: R1's whole-run rate is
  0.2 pp (steady) and 0.04–0.07 pp (step) above its steady-state rate.

### 11.3 S3 domain line (finding 3)

`TestS3MissRateAgainstPoisson` now prints one table with every cell and a `domain` column: `in`,
or `OUT-OF-DOMAIN: K14 binds` / `connect CPU > 50% (empirical line)` (both when both); verdicts
of flagged cells read `… (not asserted)`, and the test logs the count. Full form: 26 cells
asserted, 22 flagged (4 of them in band anyway); default: 18 / 7. **The "connect CPU ≤ 50%"
line was drawn after seeing the grid** (between 46%, the last cell that held, and 62%, the first
that did not): it describes where the model stopped in this grid; it is not a prediction. The
K14 clause is the plan's limit 2, stated before any run.

### 11.4 What else changed in this file

Decisions 3, 5, 11 and 16, the S5 and labels rows of section 4, the `run.test` sentence and the
`s5` row of section 5, the totals, the S5 smoke line, finding 2 and limit 6 were corrected in
place (marked "repair round"). Everything else stands.

### 11.5 Final sweep list

Unchanged grids except `s5`. Durations at jobs=4 are section 6.1's measurements (shared host)
except `s5`, which is estimated from its jobs=2 run (labelled).

| Sweep | Runs (default seeds) | Wall at jobs=4 | Size |
| --- | --- | --- | --- |
| s3 | 192 | 146 s | 85 MB |
| s4 | 90 | 242 s | 58 MB |
| s5 | 84 | ≈ 30 s (estimate; measured 47 s at jobs=2) | 148 MB (measured) |
| s6 | 73 | 63 s | 68 MB |
| s7 | 108 | 75 s | 134 MB |
| s8 | 60 | 37 s | 78 MB |
| s9 | 39 | 15 s | 106 MB |
| s10 | 21 | 15 s | 74 MB |
| s11 | 24 | 11 s | 15 MB |
| s12 | 33 | 9 s | 65 MB |
| **all** | **724** | **≈ 10 min 45 s** (estimate: 11 min 12 s − 60 s + 30 s) | **≈ 828 MB** |

Labels changed nothing in the runs (same keys, one more value), so the other sweeps' times and
sizes carry over. `seeds=5` for `s5` alone: 140 runs, about 1 min at jobs=2, about 250 MB.

### 11.6 Commands for the E7 simulator campaign

```sh
export PATH=$PATH:/usr/local/go/bin:/root/.local/bin
cd <the repository tree>                       # mglease and the tests find calibration.json upwards
go build -o /tmp/mglease ./cmd/mglease
/tmp/mglease sweep all out=<camp> jobs=4                       # 724 runs, ≈ 11 min, ≈ 830 MB
/tmp/mglease sweep s5 out=<camp>-s5x5 seeds=5 jobs=4 campaign=e3-sim-s5x5   # optional: S5 at 5 seeds (own campaign label)
scripts/lease-report.sh <camp> <camp>-report baseline_arm="'none'"          # must exit 0, table 0 empty
# full-form S1 and S2 (REVIEW-E3-1 finding 7; about 10 min together) and the §10 S5 rule at 5 seeds:
LEASE_E3=full go test ./internal/lab/leaselab -run 'TestS1Structural|TestS2Setting' -count=1 -v -timeout 60m
LEASE_E3=full go test ./internal/lab/leaselab -run 'TestS5RuleAgainstHindsightFixed' -count=1 -v -timeout 60m   # ≈ 4 min
LEASE_E3=full go test ./internal/lab/leaselab -run 'TestS3MissRateAgainstPoisson' -count=1 -v                    # S3 table of record, ≈ 1 min
```

The §10 warm-stock verdict is S5's test table (steady-state, pooled, Wilson band); the report's
table 7 reads the last flush (whole run, per seed) and is printed beside it, not used for the
verdict. Times on this host (shared): full-form S3 64 s, full-form S5 243 s (5 seeds, both
patiences); default S3 23 s, S5 76 s, S12 10 s.

### 11.7 Checks at the end of the repair

`gofmt -l .` empty; `go build ./... && go vet ./...` clean; `go test ./internal/lab/...
./cmd/mglease/... -count=1` green (leaselab 366 s, leasepool 18 s, mglease 11 s; shared host);
`go test -race ./cmd/mglease/...` green; `go test ./internal/sim/... ./internal/obs/...
./internal/pgwire/...` green, untouched. Repair size: code +144 / −18 lines in `scenario.go` and
`scenario_sweeps.go`, tests +193 new (`labels_scenario_test.go`) and the S5/S3 rewrite in
`stock_scenario_test.go`; 9 files, +486 / −112 in all (incl. the renames). No container used.
Scratch `/tmp/zgw-lease-e3r/` deleted.
