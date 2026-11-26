# Lease experiment, stages 1–3 — report

Written by Fable on 2026-11-26. Plan: [../LEASE-EXPERIMENT-PLAN.md](../LEASE-EXPERIMENT-PLAN.md).
Stage 0: [CALIBRATION-NOTE.md](CALIBRATION-NOTE.md). Scope: the narrowed form Nick ruled on
2026-11-25 (`/root/briefs/zgateway-lease-stages13-ruling.md`). Chunk 5 stays parked; PLAN.md
and product code are unchanged. Lab code is on the local, unpushed branch `exp/lease-lab`
(`5ffc7a3`). Every number below comes from a table or log under [e7/](e7/); the section
headings name the file.

## Summary

- **Reset isolates; the baseline does not.** On real Postgres 17, with no release action,
  session state crossed tenants in 15,657 of 34,006 column checks and a secret was read back
  in 3,531 of 4,856 attempts. Under reset-always and reset-on-switch both counts are zero.
  What a reset leaves is exactly the two residue items OPTIONS.md lists; a re-seed closes
  the first.
- **Reset is cheap at 5 ms leases and not at 0.5 ms.** At 80% load reset-always and
  reset-on-switch keep the baseline's goodput in both tiers. In the simulator the p99 wait is
  1.07–1.09× the baseline. A reset costs about 3.5% of pool capacity at 5 ms leases and 21%
  at 0.5 ms, so the 95% goodput line fails for short leases.
- **Remake-on-switch isolates completely and fails on waiting.** No backend served two
  tenants and no residue crossed. Inside the calibrated switch budget it kept goodput, but
  the p99 wait was 5.4–9.7× the baseline on real Postgres and the median wait went from
  0.17 ms to 22–35 ms. With 100 or more tenants it sits at the connect ceiling at every load.
- **Remake-always is not usable**, as stage 0 predicted: 153 leases/s against 2,321/s, and a
  warm stock did not move it (154/s).
- **The sizing rule works where the model holds and buys little.** On steady load R1 holds
  the 1% miss target with 1.04× the idle stock of the best fixed size. On a load step it uses
  0.65× the stock, with a miss rate too close to 1% to call. On bursty arrivals nothing below
  16 warm connections holds the target.
- **The real tier cannot resolve the p99 line for the reset policies.** Two identical
  baseline arms differed by 109% in p99 wait at the verdict load, so those two rows are
  inconclusive on real Postgres and rest on the simulator.

## Verdict table (plan §10, thresholds as accepted)

"Sim" is exact, same seed as the baseline. "Real" is the median over valid repetitions at
80% of the measured baseline capacity (R7 mix A: 6 tenants, 5 ms leases, cap 20, ten
repetitions). Sources: `e7/sim/tables/09-verdict.md`, `e7/r7a/tables/09-verdict.md`,
`e7/isolation/tables/02-isolation.md`, `e7/sim/logs/test-s5-full.log`.

| Policy | Isolation (zero rows) | Goodput at 80% (≥ 95% of baseline) | p99 wait at 80% (≤ 2× baseline) | Warm stock (miss ≤ 1%, stock ≤ 1.5× hindsight) |
| --- | --- | --- | --- | --- |
| none (baseline) | **Fails, as required**: dirty in all seven columns, secrets read | — | — | — |
| reset-always | **Pass**: 0 of 34,006 checks per arm; residue items 1 and 2 only | Sim: **pass** at 5 ms and heavy-tailed (1.00); **fail** at 0.5 ms (0.79). Real: **pass** (1.000) | Sim: **pass** (1.08–1.33×). Real: **inconclusive** (1.25×, inside the A/A floor) | n/a |
| reset-on-switch | **Pass** across tenants; same-tenant carry-over present, as designed | Sim: **pass** at 5 ms and heavy-tailed; **fail** at 0.5 ms (0.79–0.81). Real: **pass** (1.000) | Sim: **pass** (1.07–1.32×). Real: **inconclusive** (2.38×, inside the A/A floor) | n/a |
| remake-on-switch | **Pass**: 0 across tenants, no backend with two tenants, no residue across tenants | Sim: **fail** in 13 of 14 workload cells (0.01–0.52); pass only at 10 tenants with affinity. Real: **pass** (0.9996–1.001) | Sim: **fail** in every cell (2.55–111×). Real: **fail** (5.4–9.7×) | Steady: **pass** (0.36%, 1.04×). Step: **inconclusive** (0.90%, band to 1.009%; 0.65×). Bursty: **fail** (68%) |
| remake-always (control) | Pass: no backend with two leases | **Fail** (sim 0.06, real 0.066) | **Fail** (real 260×) | Warm run: ceiling unchanged |

Confidence: isolation is direct evidence on real Postgres, high. Goodput agrees in both
tiers, high. The p99 line for the reset policies is simulator evidence only, medium. The
warm-stock line is simulator evidence, with the real tier confirming hit rates at three
sizes, medium.

**What the evidence supports, by the plan's §10 table** (the decisions are Nick's):

- Row 2, with a change: reset-always as the default, reset-on-switch as a config policy.
  Reset-on-switch helps only where few tenants share the pool (it sent 470 resets/s against
  2,321/s in mix A). With 100 or more tenants it resets on nearly every lease and costs the
  same. Neither holds 95% of baseline goodput for 0.5 ms leases.
- Row 3 is not supported as written. Remake-on-switch does not "work well" even under the
  connect ceiling; it is a policy for a deployment that cannot accept the residue and can
  accept a connect time (about 30 ms here) on most tenant switches.
- Row 6: R1 does not beat a fixed size on steady load. If a warm stock ships, a fixed size
  is enough; the rule is recorded as tried.
- Row 7 (validity): the baseline fails the oracle, so the oracle has teeth. The simulator
  and the real tier agree on goodput and warm hit rate and disagree on wait tails (below).

## 1. Calibration

Unchanged from stage 0 (`e7/sim/tables/01-calibration.md`). Under campaign load the same
costs were higher than on an idle host (`e7/r7a/tables/03-cost.md`):

| Cost | Stage 0, idle | R7, pool under load |
| --- | --- | --- |
| Reset (`DISCARD ALL`), p50 / p99 | 88–177 µs / 0.26–2.1 ms | 305 µs / 2.6 ms (reset-always); 350 µs / 3.0 ms (reset-on-switch) |
| Connect, p50 / p99 | 16 ms / 37.5 ms | 30–33 ms / 55–70 ms |
| Close until the backend is gone, p50 | ≤ 0.8 ms | 0.70–0.72 ms |

## 2. Isolation (`e7/isolation/`)

Astra built the workload and the checker; Opus reviewed them. 24 arms on real Postgres
(SCRAM, TLS), 2,429 leases per arm, 2 connections, 12 tenants, every lease inspected before
its script ran. Each arm pair below pools affinity on and off.

| Policy | Re-seed | State columns: across tenants / checks | Secrets read across tenants / attempts | Same-tenant carry-over | Backend identity violations |
| --- | --- | --- | --- | --- | --- |
| none | off | 15,657 / 34,006 | 3,531 / 4,856 | 1,768 | — |
| reset-always | off, on | 0 / 34,006 each | 0 / 4,856 each | 0 | — |
| reset-on-switch | off, on | 0 / 34,006 each | 0 / 4,856 each | 775, 697 (allowed) | — |
| remake-on-switch, patience auto | off, on | 0 / 34,006 each | 0 / 4,854–4,855 | 722, 806 (allowed) | 0 / 8,924, 0 / 8,862 |
| remake-on-switch, patience 0 | off, on | 0 / 34,006 each | 0 / 4,854 | 597, 549 (allowed) | 0 / 9,006, 0 / 9,038 |
| remake-always | off, on | 0 / 34,006 each | 0 / 4,856 each | 0 | 0 / 9,716 each |

Residue across tenants (probes; found / probed):

| Policy | Random seed inherited, re-seed off | Re-seed on | Custom setting name visible |
| --- | --- | --- | --- |
| none | 426 / 533 | 424 / 544 | 424 / 529 |
| reset-always | 462 / 551 | **0 / 541** | 460 / 545 |
| reset-on-switch | 425 / 540 | **0 / 545** | 420 / 540 |
| remake-on-switch, remake-always | 0 | 0 | 0 |

- The baseline went non-zero in all seven dirty-check columns
  (`e7/isolation/e4-oracle-check.txt` ends "baseline exercises every secret-bearing kind").
- Leases that ended in a transaction, in a failed transaction or with the client gone were
  never reused, under any policy (R5; the checker's own SQL over the full campaign reports
  "isolation expectations held", and Opus's review confirmed no reuse after those endings).
- In the simulator, S1 (200 seeds, read from the fake server's own log) and S2 pass for
  every policy and fail for `none` on every seed (`e7/sim/logs/test-s1s2-full.log`).
- Not tested, as the plan states: residue items 3 and 4 of OPTIONS.md (extension and
  procedural-language state; replication origin).
- A checker limit the reviewer found: a custom setting is invisible to the catalog query and
  is detected only by reading back the previous lease's secret.

## 3. Cost per lease (`e7/r7a/tables/03-cost.md`, `e7/r7-summary.md`)

| Policy (real, 80% load) | Release action | Postgres CPU per 1,000 leases | Against baseline |
| --- | --- | --- | --- |
| none | — | 550 ms | 1.00× |
| reset-on-switch | 470 resets/s | 570 ms | 1.04× |
| reset-always | 2,321 resets/s | 636 ms | 1.16× |
| remake-on-switch, dynamic / fixed:4 | 24 / 44 connects/s | 711 / 860 ms | 1.29× / 1.56× |
| remake-always | 153 connects/s | 19,106 ms | 34.8× |

First statement on a fresh backend: 3.4–3.5 ms p50 against 0.51 ms on a reused one. Priming
with `SELECT 1` brought it to 2.6 ms. That confirms P5 and the prime's effect, but priming
changed no wait verdict (p99 9.7× with it, 7.7× without). After a reset the first statement
cost the same as on a reused backend (0.47–0.48 ms).

## 4. Capacity (`e7/sim/logs/test-rest-full.log`, S6)

Simulator, cap 20, goodput at 120% offered load:

| Policy | 5 ms leases | 0.5 ms leases |
| --- | --- | --- |
| none | 3,867/s | 28,713/s |
| reset-always | 3,722/s predicted, within 1% | 22,713/s (79%) |
| reset-on-switch | as reset-always | 22,722/s (79%) |
| remake-on-switch, fixed:2, 100 tenants | connect-bound | 1,002/s (3.5%) |
| remake-always | 174/s | 174/s |

- The plan's formula S/(S+r) was wrong for short leases (predicted 74%, measured 79%). A
  lease holds its connection for two round trips beyond S; the corrected formula is in
  `NOTES-E3.md` on the lab branch and fits within 1%.
- The remake rows are consistency checks, not independent predictions: they confirm the run
  sits at the connect ceiling.
- On real Postgres the baseline's capacity was 2,902 leases/s (median of four saturated ramp
  steps, `e7/r7a/capacity.csv`), against 3,849/s in the simulator for the same settings.
  `pg_sleep` holds a 5 ms lease for about 5.3 ms and the lab shares the CPUs.

## 5. Waiting (`e7/r7-summary.md`, `e7/sim/tables/05-waiting.md`)

Real Postgres, mix A, at 80% of capacity (2,321 leases/s):

| Arm | Valid runs | Wait p50 | Wait p99 | Connects/s |
| --- | --- | --- | --- | --- |
| none | 8 of 10 | 0.17 ms | 27 ms | 0.6 |
| none (A/A twin) | 10 of 10 | 0.18 ms | 57 ms | 0.6 |
| reset-always | 8 of 10 | 0.27 ms | 29 ms | 0.6 |
| reset-on-switch | 7 of 10 | 0.31 ms | 87 ms | 0.6 |
| remake-on-switch, dynamic | 9 of 10 | 22 ms | 281 ms | 24 |
| remake-on-switch, dynamic, primed | 9 of 10 | 24 ms | 354 ms | 19 |
| remake-on-switch, fixed:4 | 9 of 10 | 35 ms | 230 ms | 44 |
| remake-on-switch, dynamic, patience 0 | 8 of 10 | 25 ms | 126 ms | 36 |
| remake-always | 5 of 10 | 7.9 s | 9.5 s | 153 |

- The A/A twin's p99 is 2.1× the baseline's. That is the noise floor, and it is why the
  reset rows are inconclusive on this line.
- Near saturation (95% of capacity, three repetitions, two or three valid) reset-always's
  median wait was 57 ms against 1.7 ms for the baseline and reset-on-switch's 16 ms. That
  is the capacity loss showing up as queueing. Too few runs for a verdict.
- At 30% load remake-on-switch with patience kept the median wait at the baseline's
  (0.13–0.14 ms) with a p99 of 8–11 ms against 2.7 ms.
- Mix B (same tenants, more switching; 6 repetitions, 4–5 valid, so every row is
  inconclusive by count): remake-on-switch waited 45–50 ms at the median and 154–197 ms at
  p99 against 0.12 ms and 5.2 ms.

## 6. Warm stock against the Poisson model (S3, S4; `e7/sim/logs/test-rest-full.log`)

- Inside the model's assumptions the prediction holds: all 26 asserted cells are within the
  binomial band (for example m = 0.53, W = 2: 10.2% measured, 9.8% predicted).
- It breaks from about 108 switches/s (m ≈ 2.2). Connect time grows with load and the limit
  of 4 connects in flight binds: at m = 3.6 and W = 8 the model predicts 2.9% and the pool
  measured 41%. The 22 cells outside the asserted domain are printed beside the others. The
  "connect CPU ≤ 50%" boundary was drawn after seeing the grid.
- On real Postgres (R8, valid runs) the warm hit rate was 0.94–1.00 of the simulator's at
  W = 1, 2 and 4 (for example 49.6% against 52.8% at W = 1 and 82.8% against 86.4% at W = 2).
- At load on real Postgres the stock is empty when needed: with 20 connections in use there
  is no free slot to hold it in. This is the plan's predicted limit 1.

## 7. The rule R1 (`e7/sim/logs/test-s5-full.log`; 5 seeds, 120 s, first 10 s excluded)

| Workload | R1 miss rate (95% band) | Smallest fixed W that holds 1% | R1 idle stock against it |
| --- | --- | --- | --- |
| steady | 0.36% (0.29–0.43%) — holds | 5 (0.44%) | 1.04× |
| step ×4 | 0.90% (0.80–1.009%) — inconclusive | 8 (0.59%) | 0.65× |
| bursty | 68% — misses | 16 | — |

- R1's misses cluster at the cold start and the step edge; over the whole run the steady
  rate is 0.54%.
- No reactive comparator was built (ruling). The dispersion term was dropped, and bursty is
  where it would have applied.
- The builder also found that R1's target flickers between two adjacent sizes under noisy
  input (47–105 direction changes in 10 minutes).

## 8. Simulator against real (`e7/r8/compare.md`)

54 runs, 6 real runs invalid. The simulator was run twice: plain, and with the hold measured
by an independent probe (1 ms quantum plus 0.317 ms, `e7/r8/hold.csv`).

| Metric | Agreement |
| --- | --- |
| Goodput / requested | Equal in every valid run |
| Warm hit rate | Real 0.94–1.00 of simulated |
| Wait p50, p90 | Real carries a floor of 0.1–0.4 ms the simulator does not have |
| Wait p99, remake cells | Real 1.4–1.8× simulated at W = 1 and 2; outside the band at W = 4, where the simulator says near zero |
| Wait p99, baseline and reset cells | Outside the band in all 15 valid runs: the simulator says under 0.3 ms, real shows 1–15 ms |

So the simulator's goodput, capacity ratios and hit rates stand. Its wait quantiles are
lower bounds, and its "1.08× baseline" for reset is not confirmed on real Postgres, where
the scheduling tail of this VM is larger than the effect. Two more limits of the model: its
connect ceiling is 11% low (174/s against 195/s measured), and lease work does not load the
CPUs that connects use.

## 9. Switch-rate budget

Ceiling: 195 connects/s sustained on an idle host (stage 0). Under campaign load
remake-always reached 153–159/s. One core of Postgres is used by about 70 connects/s.

| Workload | Tenant switches/s under the baseline | As remakes: share of ceiling | Remakes actually made (affinity + patience) | Postgres CPU per 1,000 leases |
| --- | --- | --- | --- | --- |
| Real mix A, 80% | 470 | 2.4× the ceiling | 18–44/s (9–23%) | 681–860 ms against 550 |
| Real mix B | 950–1,100 | 5× the ceiling | 69–78/s (35–40%) | 1,133–1,170 ms against 554 |
| Sim, 100 tenants, 80% | most leases | far above | at the ceiling; goodput 0.52 | — |
| Sim, 10 tenants, affinity on | — | — | below the ceiling; goodput 1.00, p99 2.55× | — |

- Affinity and patience are what keep remake-on-switch inside the budget; neither removes
  the wait when a switch does happen.
- Patience matters most at low load: at 30% it cut remakes from 43/s to 0.5/s and Postgres
  CPU from 1,488 to 609 ms per 1,000 leases, raising p99 wait from 4.2 to 8.7 ms.
- R6: the server never held more than the cap of 20 backends in any of 18 runs, in both
  remake orders (`e7/r6/`).

## How it was run, and what departs from the plan

- **Pieces and reviews.** E1, E2, E3, E5, E6 by Opus, reviewed by Fable; E4 by Astra,
  reviewed by Opus. E2, E3, E5 and E6 each failed review 1 narrowly and passed after one
  repair. Review files are on the lab branch.
- **One knob added beyond the plan: `remake.patience`.** The E2 review found that
  remake-on-switch as first built closed other tenants' idle connections for every waiter and
  reconnected at 130–170/s at every load. The repair bounds those closes and lets a lease
  wait for its own tenant's connection for up to one connect time. `0` is the plan's literal
  behaviour and was run beside `auto` throughout. This was my decision as coordinator.
- **E4's first run was stopped by OpenAI's content filter** part-way. I re-dispatched it
  once with the brief reworded to describe the task plainly; that run finished.
- **Tier R was sized down** from 9.7 h to 3 h: ten repetitions only at the verdict load,
  three at 30/60/95%, six for mix B. "80%" is 80% of the measured capacity (2,321/s), which
  is 58% of the nominal cap ÷ lease time.
- **79 of 360 tier-R runs are labelled invalid** by the 5 ms lateness gate, spread evenly
  across arms. They are kept and excluded from medians.
- **Warm-up.** The first 5 s of each real run are excluded from wait histograms; counters
  are whole, so the real-tier miss counters include the cold start and are not used.
- **S5's evaluation rule** (steady-state window, 95% band, same rule for R1 and fixed sizes)
  was set after the first S5 results showed the verdict depended on run length.
- **One shared 4-vCPU VM, loopback**, as in stage 0. Ratios, not absolutes.

## Files

- `e7/sim/`, `e7/sim-s5x5/` — simulator campaign: 864 runs, report tables, full-form
  S1–S12 logs, per-run `lab.csv` archive.
- `e7/isolation/` — the 24-arm isolation campaign: table 2, the checker's own SQL result,
  per-run `oracle.csv` archive.
- `e7/r6/`, `e7/r8/`, `e7/r7a/`, `e7/r7b/` — tier R: manifests, `env.txt`, capacity ramp,
  report tables; `e7/r7-summary.md`; `e7/tier-r-per-run.tar.gz` (per-run summaries).
- Raw run directories stay on vm-1 under `/root/runs/lease-s`, `lease-iso`, `lease-r`
  (1.4 GB, not in git).
