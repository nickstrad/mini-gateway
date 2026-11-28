# Lease lab — piece E6 notes: report SQL and its self-test (U6)

Builder: Opus, 2026-11-26. Brief `/root/briefs/zgateway-lease-e6-report-sql.md`, rules
`/root/briefs/zgateway-lease-lab-rules.md`. Branch `exp/lease-lab-e6` (forked from `4429d1a`);
not pushed. Built against the run-directory contract of NOTES-E2.md sections 3–4 only; E3 and E5
were not visible.

## 1. What is built

All new files. Nothing existing was changed.

| File | Prints |
| --- | --- |
| `test/analysis/lease/report/params.sql` | the shared parameters (section 2) |
| `test/analysis/lease/report/views.sql` | the shared readers and derived tables: `runs` (labels from `lab.csv`), `pm` (pool counters, last flush), `problems`, `ok_runs`, `base_arms`, `run_q` (per-run wait quantiles), `run_miss` (warm-stock model miss), `iso` (isolation rows), the calibration extracts, and macros `lr_hq` (histogram quantile), `lr_poisson_tail` |
| `00-validity.sql` | **Table 0**: conservation and validity, one row per run and failed check; any row = not readable |
| `01-calibration.sql` | **Table 1**: reset, connect, close, first statement, connect rates, CPU per connect — from `calibration.json` |
| `02-isolation.sql` | **Table 2**: policy × state kind → checked / found / same-tenant; S1 structural, R3 backend and connection identity, R4 residue apart |
| `03-cost.sql` | **Table 3**: `reset_ns`, `connect_ns`, `close_lag_ns`, `first_stmt_ns` by mode — p50/p90/p99 with each quantile's bucket resolution, clamped counts |
| `04-capacity.sql` | **Table 4**: offered/s, goodput/s, throughput/s (with drain), ratio to the baseline on the same seed, refusals, predicted capacity and knee beside the measured knee |
| `05-waiting.sql` | **Table 5**: `lease_wait_ns` and `switch_ready_ns` p50/p90/p99/p999 (median across runs, p99 min–max), baseline beside, p99 ratio, refusals beside |
| `06-warm-stock.sql` | **Table 6**: miss rate against W (cold = 0, fixed), measured beside P(Poisson(m) ≥ W), binomial band, side |
| `07-rule.sql` | **Table 7**: R1 (`dynamic`) and each fixed W per seed: miss rate, warm-connection-seconds, hindsight W, ratio |
| `07b-rule-step.sql` | **Table 7b** (table 7's second part): warm target against offered load per flush on `step` |
| `08-sim-vs-real.sql` | **Table 8**: hit rate, p50/p99 wait, goodput/s — sim, real, ratio, inside/outside the band |
| `09-verdict.sql` | **Table 9**: one row per arm and threshold (isolation, goodput, p99_wait, warm_miss, warm_conn_s): measured, threshold, pass / fail / inconclusive, note |
| `10-switch-budget.sql` | **Table 10**: tenant switches/s against the measured (~195/s) and model (~174/s) ceilings, CPU that rate costs, measured Postgres CPU per 1,000 leases (R9) |
| `test/analysis/lease/report/empty/*.csv` | header-only files; every glob list starts with one, so an empty campaign reads as zero rows |
| `scripts/lease-report.sh` | `lease-report.sh <campaign-dir> <out-dir> [name=value ...]`: builds `<out>/report.duckdb` once (params + views), runs every `[0-9]*.sql` against it (read-only), writes `<table>.csv`, `<table>.md` and `index.md`. Exit 0 = table 0 empty; 1 = table 0 has rows (all tables still written, index marked NOT READABLE); 2 = usage / DuckDB error |
| `internal/lab/leaselab/report_test.go` | U6 (section 5) |

Every table file's header comment states what it reads, the grouping keys and the formula.

## 2. Parameters the coordinator sets to run the report

Pass as `name=value` after the two directories (value is SQL: quote text). Defaults in
`params.sql` and at the top of the table files.

| Parameter | Default | When to set |
| --- | --- | --- |
| `<campaign-dir>` | — | the root holding the run directories, any depth (E3's sweep output, E5's campaign directory, or one directory holding both) |
| `invalid_key` | `'run.valid'` | **name not fixed yet (E5)**: the `lab.csv` key of the runner's validity gate. Absent = valid; a value not in `invalid_ok` = invalid |
| `invalid_ok` | `['ok','valid','1','true','yes']` | E5's "valid" spellings |
| `cpu_pg_key`, `cpu_lab_key` | `'cpu.pg_ns'`, `'cpu.lab_ns'` | **names not fixed yet (E5, R9)**: `lab.csv` keys of Postgres / lab CPU ns over the run. Otherwise `cpu.csv` with columns `pg_cpu_ns`, `lab_cpu_ns` (rows summed) |
| `server.csv` columns | `time`, `count` | fixed by the E5 brief; read by name, extra columns ignored; a file without an integer `count` is a table 0 row |
| `server_extra` | `0` | backends allowed above cap (+ K14 under `open-then-close`) — e.g. 1 if E5's monitor session counts itself |
| `baseline_arm`, `aa_arm` | `''` (auto) | auto = the first / second arm name (sorted) with `none` + `cold` in a comparison group. Set when E5's A/A labels do not sort baseline first (e.g. `baseline_arm="'base'" aa_arm="'base-aa'"`) |
| `verdict_load` | `80` | `workload.load` of the goodput and p99 lines (§10) |
| `goodput_min_ratio`, `p99_max_ratio`, `wcs_max_ratio` | 0.95, 2, 1.5 | §10 thresholds as ruled |
| `min_valid_reps` | `5` | table 9: valid (not runner-invalid) runs needed at `verdict_load` per side (arm, baseline, real A/A); with fewer runs of that arm in total, all of them |
| `p99_abs_floor_ns` | `1000000` (1 ms) | table 9: absolute p99 bound used when the baseline's p99 sits in the histogram's lowest bucket (≤ 1 µs) |
| `sim_real_band_pct` | `25` | **E5 declares the R8 band**; set it to E5's value |
| `ceiling_cond` | `'c=16'` | M7S cell giving the measured connect ceiling (195.35/s) |
| `band_z` | `1.96` | binomial band, standard errors |
| `knee_tol_pct` | `2` | table 4: a load is past the knee when goodput < 98% of requested |
| `step_profile` | `'step'` | table 7b |
| env `LEASE_REPORT_CALIBRATION` | lease-exp `calibration.json` | another calibration file |

Example: `scripts/lease-report.sh /root/runs/lease-e7 /root/runs/lease-e7-report sim_real_band_pct=30 aa_arm="'base-aa'"`.
Run it from the repository (the script finds its SQL from its own path).

**Labels the report groups on** (from `lab.csv`): `tier`, `run.campaign`, `run.test`, `run.arm`
(falls back to `<policy>+<warm.mode>` if empty), `run.rep`, `seed`, `workload.load`, and a
`workload` key = profile | tenants | skew | lease | stmt_class | script | cap | duration. A
comparison group = (tier, campaign, test, workload, load); same-seed pairs add (seed, rep). So
**E3/E5 must give every arm of one comparison the same `run.campaign`, `run.test`, workload keys
and duration, and distinct `run.arm` labels**; two runs with the same campaign/test/arm/load/
seed/rep are a table 0 row ("duplicate labels").

## 3. Decisions the brief left open

1. **Run identity = directory path**, not `run.csv`'s run id: E2's run id is the last path
   element, which two campaigns may share. The report reads files with DuckDB's `filename`.
2. **Not through `scripts/csv-assert.sh`**: it needs explicit run lists and fails on a duplicate
   run id or an empty input; the report must print an empty campaign as a row. It reads the same
   schema-1 columns with the same types; `checks/conservation.sql` stays E2's per-flush assertion.
   Table 0 repeats the four conservation laws (with `pending`) in every flush.
3. **Globs with a sentinel**: `<root>/**/<file>` (matches the root itself too) plus a header-only
   file per kind from `report/empty/`; DuckDB refuses an empty file list otherwise.
4. **One database build, then the tables**: `params.sql` + `views.sql` run once into
   `report.duckdb`; each table re-reads `params.sql` (variables do not persist) and runs read-only.
   The 37-run smoke campaign (45 MB) takes 13 s.
5. **Quantiles** are the upper edge of the bucket holding the ⌈q·n⌉-th value (as E2's C1 table),
   with the bucket's relative width `(hi − lo)/lo` in ppm beside them (about 0.4–0.8%). The
   clamped-low values sit in the lowest bucket `[1000, 1004)`: a quantile of 1004 reads "≤ 1 µs".
   Across runs: buckets summed (table 3), or per-run quantiles then median and min–max (tables
   5, 8, 9) — the repetitions are the unit for the real tier.
6. **Isolation "promised"**: a dirty column counts as found on every lease under
   `reset-always`/`remake-always` and on a change of tenant (or no previous tenant) otherwise;
   same-tenant hits are a separate column (reset-on-switch keeps state within a tenant by design;
   under `remake-on-switch` the marker of the same tenant is still there, 43,746 rows in the smoke).
   Residue probes (R4) are class `residue`, listed, never counted toward the verdict.
   Structural (S1): `reset_between = 0` where the policy needs a reset; under remake policies any
   sharing (cross-tenant / any). Identity (R3): per backend (pid, start) from `oracle.csv`, and per
   pool connection from `leases.csv` where written — remake policies only.
7. **Goodput/s = `leases_goodput` / `run.duration`** (the arrival window). With the same seed the
   ratio to the baseline is the goodput count ratio, exact. Table 4 also prints throughput/s =
   goodput / `run.end_ns` (window + drain), where a backlog shows. My first version used `run.end`
   for both, and a drain quantised to 100 ms ticks put the baseline "past the knee" at 60%; changed.
8. **Measured knee** = the lowest tested load with goodput < 98% of requested; the predicted knee
   = predicted capacity / (cap / mean lease time). Predicted capacity: none cap/S; reset-always
   cap/(S+r); reset-on-switch cap/(S+f·r); remake-always min(cap/(S+L+x), C); remake-on-switch
   min(cap/(S+f·(L+x)), C/f); S = the baseline's measured hold per lease (`lent_ns_total`/granted),
   f = tenant-switch fraction, r, L, x = calibrated (M2I 177 µs, M3 16.0 ms, M4 0.82 ms), C = 195/s
   (real) or the run's connect model (sim, 174/s).
9. **Tenant switches** = `leases_switch{warm, connect, reset, ready}` (REVIEW-E2-2 finding 1), never
   `switch_arrivals`. Table 6's miss uses finding 2's formula; `miss_slot` is not a miss.
10. **Table 6 m** = measured fresh-connection arrival rate × measured mean connect time
    (`connecting_ns_total` / `connects{started}`); band = p ± z·√(p(1−p)/n) around the
    prediction (normal approximation), clipped to [0, 1]; the measured fraction is below / inside
    / above it.
11. **Verdict rules** (as repaired in review round 1, section 9; the table 9 header has all): sim
    goodput = the smallest same-seed ratio (every seed must hold); sim p99 = the largest same-seed
    ratio, inconclusive when the arm's bucket straddles 2× the baseline's bucket. Real goodput
    (medians over the valid reps): ratio = arm / baseline, d = baseline − arm, floor = |A/A −
    baseline|; floor ≥ 5% of the baseline → inconclusive; ratio < 0.95 or d > 2·floor → fail; else
    pass; the note prints the ratio, d against 2·floor, the three medians and the reps. Real p99:
    floor = |A/A p99 − baseline p99|; |arm − 2·baseline| ≤ 2·floor → inconclusive; else arm ≤
    2·baseline → pass, else fail. Clamp bucket (both tiers): a baseline p99 in the lowest bucket
    (lo ≤ 1000 ns) is compared absolutely — arm p99 ≤ `p99_abs_floor_ns` → pass, else inconclusive,
    both values in the note. Warm lines for `dynamic` arms over all loads: miss ≤ ε with a band
    around the measured fraction (straddle → inconclusive); warm-conn-s ratio = the largest over
    (load, seed, rep), inconclusive when some comparison has no hindsight W. A run of the arm, the
    baseline or the A/A arm of severity `broken` in table 0, or no run at the load, makes the row
    inconclusive. Runs of severity `invalid` are dropped and counted (`valid runs arm 9 of 10, …`);
    goodput and p99 are inconclusive when a side has fewer valid runs than
    least(`min_valid_reps`, its runs at the load); isolation and warm rows drop them and say so.
    The baseline gets rows too (its isolation line must read `fail`); the SQL does not editorialise.
12. **Validity rows** (table 0): missing metrics / histograms / run / lab / oracle (oracle.csv is
    written by every E2 run), leases with `obs.leases_csv=on`, server.csv for real-tier remake
    runs; `sidecar.version` ≠ 1; missing tier / policy / warm mode / load / duration / cap; no
    lease; `requests.achieved` ≠ `leases_requested`; the four conservation laws per flush;
    `open_connecting_hw` > cap; `db_backends_hw` and server.csv max > cap (+K14 under
    open-then-close) + `server_extra`; duplicate labels; then severity `invalid`: `run.result` ≠
    ok, the runner's validity key, real-tier `db_lost` > 0 (REVIEW-E2-2 E5 item).
13. **Table 8 pairs** by (campaign, test, arm, load) across tiers, listing every key that has a
    real run (sim-only arms would flood it). Hit rate = 1 − table 6's miss (remake policies).
14. **Output**: CSV (asserted by the test) and markdown per table, plus `index.md`.

## 4. Claims and the tests that prove them

All in `internal/lab/leaselab/report_test.go`; planted runs are generated by `newPlant` /
`plantRun.write` from a few numbers (no E2/E3/E5 code; conserved by construction).

| Claim (brief) | Test |
| --- | --- |
| Healthy pair: table 0 empty, exit 0, the arm passes isolation, goodput, p99 | `TestU6ReportOnPlantedRuns/healthy` |
| Planted leak: one dirty oracle row (cursors) under reset-always → table 2 found 1, table 9 isolation fail; one backend pid serving two tenants under remake-on-switch (the pool's previous tenant hidden, so only identity can see it) → backend_identity found 1, fail | `…/leak` (`checkLeaks`) |
| Planted miss rate: n = 990 (1,000 arrivals − 10 with no slot), 50 misses at W = 4 and 583 at W = 2, m = 99/s × 20 ms = 1.98 → measured exactly 0.050505 / 0.588889, predicted, m and band equal to an independent Go computation (1e-6), side `below` / `inside` | `…/miss` |
| Goodput 94% → fail (measured 0.94), 96% → pass (0.96), same seed | `…/goodput` |
| p99 over 2× → fail (25.2 / 10.066 ms), 1.5× → pass, a bucket straddling 2× → inconclusive; table 5 prints p99, baseline p99 and refusals | `…/p99` |
| Real goodput, 3 reps: baseline 100/s, A/A 101/s → 99.5/s pass, 97.9/s fail (d 2.1 > 2·floor at ratio 0.979), 97/s fail; identical arm and A/A (d = 0, floor = 0) pass; A/A floor 6% → inconclusive | `…/aa` |
| Real p99, 3 reps: baseline 10.066 ms, A/A 11.066 ms (floor 1 ms) → 25.2 ms fail, 15.1 ms pass, 21.2 ms inconclusive (1.07 ms from 2×, inside 2·floor) | `…/p99-real` |
| Clamp bucket: baseline p99 ≤ 1 µs → arm p99 0.5 ms pass, 5 ms inconclusive with both values; table 5 ratio empty | `…/clamp` |
| Invalid reps (real, 10 each): one invalid of ten in the arm and the baseline → isolation, goodput, p99 pass, note "valid runs arm 9 of 10, baseline 9 of 10, A/A 10 of 10"; six invalid (four valid) → inconclusive | `TestU6InvalidReps` |
| Switch budget: 500 switches in 10 s → 50/s, fraction of 195.35/s, "not measured" CPU | `…/budget` |
| Conservation break → table 0 row, exit 1, no pass for that arm | `TestU6Unreadable/conservation` |
| Empty input → one table 0 row "no run directory", exit 1, no verdict rows | `TestU6Unreadable/empty` |
| Missing oracle.csv / lab.csv / server.csv (real remake) and a runner-flagged run → table 0 rows, exit 1, never a pass; the missing-oracle arm's isolation inconclusive | `TestU6Unreadable/missing` |
| **Canary**: a test-only copy of the SQL with the `cursors` line deleted from the isolation query → the leak check fails | `TestU6Canary` |
| Every file on real `mglease sim` runs (5 arms, oracle and leases.csv on): table 0 empty, tables 1–7, 9, 10 non-empty, baseline marker leak found (canary, settings, structural), every other arm finds 0 and passes isolation | `TestU6ReportOnSimRun` |

Run: `go test ./internal/lab/leaselab -run U6 -count=1 -v` (about 40 s; needs DuckDB 1.5,
skipped without it unless `MG_REQUIRE_DUCKDB=1`).

## 5. On real `mglease sim` run directories (the contract checked against actual files)

A 37-run campaign: `skewed`, 10 tenants, 4 s, loads 60 and 80, seeds 1 and 2, `oracle.rate=0.2`,
`marker` script, `leases.csv` on; arms `base` (none+cold), `reset-always`, `reset-on-switch`,
`remake-on-switch` `dynamic` and `fixed:0/1/2/4` (patience auto), `remake-always` cold; plus one
`step` dynamic run (load 10, 6 s, flushes every 500 ms). Script in section 8. Report: exit 0,
table 0 empty, 13 s. Excerpts (abridged columns):

Table 2 (states not checked in the simulator omitted):

| policy | kind | class | runs | checked | found | same_tenant |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| none | canary_read | canary | 4 | 9085 | 4779 | 0 |
| none | settings_changed | state | 4 | 9085 | 4779 | 4295 |
| none | structural_reset | structural | 4 | 9074 | 4779 | 4295 |
| remake-always | backend_identity | identity | 4 | 1375 | 0 | 0 |
| remake-always | connection_identity | identity | 4 | 6762 | 0 | 0 |
| remake-on-switch | backend_identity | identity | 20 | 6941 | 0 | 0 |
| remake-on-switch | canary_read | canary | 20 | 45425 | 0 | 0 |
| remake-on-switch | structural_reset | structural | 20 | 43746 | 0 | 43746 |
| reset-always | settings_changed | state | 4 | 9085 | 0 | 0 |
| reset-on-switch | settings_changed | state | 4 | 9085 | 0 | 4100 |

Table 6 (remake-on-switch, fixed):

| load | W | misses | n | measured | m | predicted | band | side |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- |
| 60 | 1 | 1433 | 1575 | 0.909841 | 3.96 | 0.98101 | 0.974–0.988 | below |
| 60 | 2 | 1550 | 1758 | 0.881684 | 4.44 | 0.935869 | 0.924–0.947 | below |
| 60 | 4 | 2259 | 2653 | 0.851489 | 6.93 | 0.914495 | 0.904–0.925 | below |
| 80 | 2 | 13785 | 13787 | 0.999855 | 34.74 | 1.0 | 1.0–1.0 | below |

Table 9 (load 80 unless `all`):

| arm | threshold | measured | verdict | note |
| --- | --- | ---: | --- | --- |
| base | isolation | 14337 | fail | checked 27244 |
| reset-always | isolation / goodput / p99_wait | 0 / 1.0 / 1.031 | pass / pass / pass | |
| reset-on-switch | isolation / goodput / p99_wait | 0 / 1.0 / 1.026 | pass / pass / pass | |
| remake-on-switch-dynamic | isolation / goodput / p99_wait | 0 / 1.0 / 1.932 | pass / pass / pass | |
| remake-on-switch-dynamic | warm_miss | 0.994298 | fail | misses 15170 of 15257 |
| remake-on-switch-dynamic | warm_conn_s | — | inconclusive | comparisons 4, with a hindsight W 0 |
| remake-always | goodput / p99_wait | 0.1325 / 125.8 | fail / fail | refused 22125 |

Table 10 (switches/s; fraction of 195.35/s and of the model's 173.91/s; Postgres CPUs for the
connects at 14.29 ms each): remake-on-switch 60–135/s (0.31–0.69 / 0.35–0.78; 0.86–1.95 CPUs);
remake-always 422.6/s of demand (served at the ceiling); none / reset policies 970–2,090
"switches"/s at 5 connects/s. Table 4: predicted knees 96% (none), 93% (reset-always), 94–95%
(reset-on-switch), 85–88% (remake-on-switch dynamic), 4.3% (remake-always); measured: no tested
load past the knee except remake-always (below 60%).

What this shows about the contract (not findings for the report): every file and key the SQL
reads exists with the types it expects; conservation holds in every flush; this workload (10
tenants, cap 20) is past the warm-stock model's assumptions at both loads (the C1 table's churn),
so table 6 is "below" everywhere and no fixed W holds ε — the planted runs are what show the
band logic works where the model applies.

## 6. Deviations

- **Table 7b** is a second file for table 7's "warm target against offered load over time" (one
  file prints one table).
- **Shared `params.sql` + `views.sql`** instead of fully standalone files: the views (readers,
  `problems`, `iso`, `run_miss`, quantile macro) would otherwise be repeated in every file. Each
  table's own parameters are at its top; the shared ones at the top of `params.sql`.
- The self-test is a Go test in `internal/lab/leaselab` (new file `report_test.go`), reusing
  `requireDuckDB`, `repoRoot`, `runIn` from E2's tests.
- Size (after the repair round): SQL 1,029 lines (views 427, tables 579, params 23) + script 87,
  against 400; tests 708 against 100. The verdict file (226) and the shared views carry most of it.
- Commit trailer: the rules file's `Co-Authored-By: Claude <noreply@anthropic.com>`.

**Change request: none.**

## 7. Limits

- E5's names (`run.valid`, `cpu.pg_ns`/`cpu.lab_ns`, cpu.csv columns) are guesses made
  parameters; table 10 says "not measured" and table 0 says nothing until they match. Check
  E5's notes and set them.
- The binomial band is the normal approximation; with p near 0 or 1 or small n it is too narrow
  (n·p < 5 should be read with care). Not an exact Clopper–Pearson interval.
- Quantiles carry the histogram's ±0.4–0.8% bucket error; sim ratios near a threshold can be
  inconclusive only through the p99 straddle rule — sim goodput has no such guard (counts are exact).
  The real tier's p99 floor rule uses bucket upper edges and does not add the bucket error.
- The clamp-bucket test is `lo ≤ 1000 ns`, E2's lowest bucket `[1000, 1004)`, written as a constant in
  `09-verdict.sql` and `05-waiting.sql`; a change to the histogram's lower clamp must change both.
- The verdict's real-tier floor is one A/A difference of medians, not a distribution; with few
  repetitions it is itself noisy.
- Predicted capacity uses calibrated p50 costs and the baseline's measured hold; it ignores the
  first-statement penalty on fresh connections and queueing at the connect CPUs.
- Table 8 cannot tell which runs E5 meant as R8 pairs other than by identical labels.
- `lr_hq` is O(buckets²) per group; fine for the hundreds of buckets a run has, slow if a group
  merged tens of thousands.

## 8. Commands

```sh
export PATH=$PATH:/usr/local/go/bin
cd /root/Software/mini-gateway-work/lease-e6
go test ./internal/lab/leaselab -run U6 -count=1 -v           # U6, about 35 s
scripts/lease-report.sh <campaign-dir> <out-dir> [name=value ...]
# the smoke campaign of section 5 (bash; 23 s on 4 vCPUs, 45 MB):
go build -o /tmp/mglease ./cmd/mglease
C=/tmp/e6camp; common="workload.profile=skewed workload.tenants=10 run.duration=4s obs.interval=4s oracle.rate=0.2 workload.script=marker obs.leases_csv=on run.campaign=e6smoke run.test=mix"
run(){ id=$1; shift; /tmp/mglease sim "$@" out=$C/$id >/dev/null; }
for load in 60 80; do for seed in 1 2; do
  run base-$load-s$seed release.policy=none warm.mode=cold run.arm=base $common workload.load=$load seed=$seed run.rep=$seed &
  run ra-$load-s$seed release.policy=reset-always warm.mode=cold run.arm=reset-always $common workload.load=$load seed=$seed run.rep=$seed &
  run ros-$load-s$seed release.policy=reset-on-switch warm.mode=cold run.arm=reset-on-switch $common workload.load=$load seed=$seed run.rep=$seed &
  run rms-dyn-$load-s$seed release.policy=remake-on-switch warm.mode=dynamic run.arm=remake-on-switch-dynamic $common workload.load=$load seed=$seed run.rep=$seed &
  for w in 0 1 2 4; do run rms-f$w-$load-s$seed release.policy=remake-on-switch warm.mode=fixed warm.size=$w run.arm=remake-on-switch-fixed$w $common workload.load=$load seed=$seed run.rep=$seed & done
  run rma-$load-s$seed release.policy=remake-always warm.mode=cold run.arm=remake-always $common workload.load=$load seed=$seed run.rep=$seed &
  wait
done; done
run step-dyn release.policy=remake-on-switch warm.mode=dynamic run.arm=step-dynamic workload.profile=step workload.load=10 run.duration=6s obs.interval=500ms run.campaign=e6smoke run.test=step
scripts/lease-report.sh $C /tmp/e6report
```

## 9. Repair round (review 1, `REVIEW-E6-1.md`)

The one repair round: findings 1–4, by the coordinator's repair brief
(`/root/briefs/zgateway-lease-e6-repair.md`, which wins where it differs from the review).
Changed: `09-verdict.sql`, `05-waiting.sql` (finding 4's ratio only), `report_test.go`. Sections
2, 3 item 11, 4 and 7 above are rewritten to match.

1. **Real goodput** (`v_gp`): ratio < 0.95 or d > 2·floor → fail, else pass; inconclusive only
   without an A/A arm, with too few valid reps, or when floor ≥ (1 − `goodput_min_ratio`) × the
   baseline (5% by default). U6's "near" case now asserts `pass` (it asserted `inconclusive`,
   the case the review named); new arms `over` (fail by d alone), `aa0/same` (pass at d = floor = 0),
   `aa6/same` (inconclusive).
2. **Real p99** (`v_p99`): inconclusive only when |arm − 2·baseline| ≤ 2·floor (floor = A/A
   difference of p99 medians); the note prints arm, 2·baseline, the difference against 2·floor and
   the A/A p99. New group `p99r` covers fail / pass / inconclusive.
3. **Invalid repetitions**: only severity `broken` blocks (`bad`); `invalid` runs were already absent
   from `ok_runs`, so they are out of every median and pair. New: totals per arm at the load (`lt`),
   the `valid runs … of …` note on goodput and p99 rows (both tiers), the `min_valid_reps` rule, and
   "invalid runs dropped k" on isolation and warm rows. `TestU6InvalidReps` (one of ten → verdict;
   six of ten → inconclusive). The existing `TestU6Unreadable/missing` still holds: its single
   flagged run leaves 0 of 1 valid, so its rows stay inconclusive.
4. **Clamp bucket**: per the repair brief (not the review's "always inconclusive"), the comparison
   becomes absolute: arm p99 ≤ `p99_abs_floor_ns` (1 ms) → pass, else inconclusive with both values
   in the note; `measured` is then the arm's p99 in ns and `threshold_value` says "absolute". Table 5
   prints `p99_ratio` empty (NULL) when the baseline's median p99 bucket starts at ≤ 1000 ns. U6
   group `clamp`.

Decisions taken in the round:

- floor ≥ 5% of the baseline makes goodput inconclusive even when d > 2·floor (the repair brief lists
  it as an inconclusive condition without exception).
- `min_valid_reps` applies to the simulator too, as least(`min_valid_reps`, runs): with one seed per
  arm a single invalid run still makes the row inconclusive, as before.
- Simulator p99 with some pairs clamped and some not: the clamped pairs use the absolute rule, the
  others the ratio rules; any clear ratio fail fails, any straddle or clamped arm p99 above the bound
  is inconclusive; `measured` stays the largest unclamped ratio, the note names the clamped pairs.
- Each new U6 case was checked to fail on the pre-repair SQL (aa, p99-real, clamp, InvalidReps).

Not done: finding 5 (a header comment in table 6 on the connect-only lead time). The review lists
it under required repairs, but the repair brief says to repair findings 1–4 and change nothing else;
the under-estimate stands as the review describes it. Findings 6–8 need no change here (8's
`baseline_arm` is already in section 2).
