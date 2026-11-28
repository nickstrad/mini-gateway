# Lease lab — review 1 of piece E3 (simulator scenarios S1–S12, sweeps)

Reviewer: Fable, 2026-11-26. Build: `exp/lease-lab-e3`, commits `1ec8831`, `e635cb6`,
`eebdc96`, `d1bea83`, `fb7de22` (all new files; no E1/E2 or frozen file changed — checked with
`git diff --name-status 4429d1a..HEAD`). Brief: `/root/briefs/zgateway-lease-e3-sim-scenarios.md`.

**Verdict: FAIL, narrow.** The scenario tests are sound and assert what plan §8 asks. One
defect is in the sweeps' labelling: the report (piece E6) cannot read a large part of the
campaign as labelled (finding 1). One repair round, a few lines plus a test.

## What I verified, and how

- `go test ./internal/lab/... ./cmd/mglease/... -count=1`: green (leaselab 476 s on a busy
  host, leasepool 9.5 s, mglease 5 s). `gofmt -l .` empty, `go vet` clean.
- Full-form S1 (`LEASE_E3=full go test -run TestS1Structural`): green, 200 seeds × 6 arms, 287 s.
- Read `isolation_`, `stock_`, `capacity_`, `faults_scenario_test.go` in full, and the diff
  of `d1bea83` (the check changed after the full-form failure).
- S1's `reset_between` is independent of the pool: `SimDB.tap` counts the fake's own
  `pg.done` trace records of kind DISCARD without an error, per fake backend
  (`simdb.go:104–112`); the pool core and the engine do not feed it. The canary (policy
  unwired) is caught under all five non-baseline arms.
- Ran `mglease sweep all seeds=1 scale=10 jobs=2` myself: 461 runs, 0 failed, 2 min 18 s,
  293 MB. Re-ran `s11` with `jobs=1`: every file and the summary byte-identical to the
  `jobs=2` run. Goroutines and `time.Now` are only in `cmd/mglease/sweep.go`.
- Ran E6's `scripts/lease-report.sh` (from the `exp/lease-lab` worktree) on that sweep
  directory with `baseline_arm="'none'"` — see finding 1.
- Arm set against the ruling: no R2, no dispersion, no EWMA sweep (the only EWMA change is
  S12's "frozen" ablation); `remake-always` only as `.cold` and the single `fixed4` run in
  S6; `remake-on-switch` always as `.pauto` with `.p0` as a labelled extra; `warm.prime`
  compared on remake-on-switch in S3 and S7. Matches.
- Headline numbers against first principles: baseline medium ceiling 3,867/s against
  cap/h0 = 20/5.19 ms = 3,850/s; reset-always medium 3,725/s = baseline × 0.963 (r ≈ 0.2 ms on
  5.2 ms); remake-always 173.4–173.8/s against the model's 173.9/s; fixed:0 misses 100%;
  W = 2 at m ≈ 0.5: 10.3% against Poisson 9.0–9.6%. All plausible.

## Findings

1. **Major — sweep labels collide; the report drops the colliding runs.**
   `internal/lab/leaselab/scenario_sweeps.go` (the `Sweeps` table, line 428 ff., and where
   `run.test` is set from the S-number): every run of a sweep carries `run.test=S<n>`, and
   cells that differ only in keys other than the load (profile, tenants, lease, affinity,
   `workload.burst_active`, `cm.avail_pm`, `connect.max_concurrent`) share
   campaign/test/arm/load/seed/rep. E6's table 0 flags them `broken / duplicate labels` and
   the other tables leave them out. On my reduced sweep: **85 of 461 runs** — S4 15, S5 12
   (all of `bursty`, which shares load 1.35 with `steady`: table 7 has no bursty row), S6 30
   (short against medium), S7 18, S8 10 (table 4 shows 43 of 73 S6 rows, 30 of 60 S8 rows).
   `lease-report.sh` exits 1. Some colliding cells differ in keys E6's workload string does
   not contain at all (affinity in S8; `cm.avail_pm`, K14 and `burst_active` in S4), so even
   a looser duplicate check in E6 would pool different experiments as repetitions.
   **Repair:** make `run.test` name the comparison cell — the S-number plus the cell name
   without its load part (for example `S8/t10-affon`, `S6/short`, `S4/cpu50`, `S5/bursty`) —
   so that (campaign, test, arm, load, seed, rep) is unique over the whole campaign and all
   arms of one comparison still share `run.test`. Add a test over every sweep's `Runs` that
   the six-label key is unique and that each (test, load, seed) group with a `none` arm has it
   under the same label. Prove it: merge `exp/lease-lab` (E6 is there), run
   `mglease sweep all seeds=1 scale=10` and `scripts/lease-report.sh … baseline_arm="'none'"`;
   table 0 must be empty, table 7 must show bursty rows, table 4 all S6 rows. Update the
   notes' sweep table.

2. **Minor — S5's "holds ε" is asymmetric.** `stock_scenario_test.go:199–210`: a fixed W
   holds ε on the point estimate (≤ 1%), but R1 fails only above ε + 3σ (about 1.67% at 2,000
   trials). The test's own claim (R1 never misses where hindsight holds) is therefore weaker
   than §10's line "miss rate at or under ε". No repair to the test; E7 evaluates §10 by the
   rule below, not by this test's verdict column.

3. **Minor — S3's asserted domain was drawn after seeing the grid.** `inModelDomain`
   (`stock_scenario_test.go:50`): the K14 clause is principled (the plan's limit 2, stated
   before any run); the "connect CPU ≤ 50%" line was placed between the last cell that held
   (46%) and the first that did not (62%) — NOTES-E3 decision 3 says so. It is a description
   of where the model stops, not a prediction. Out-of-domain cells in the full grid: 22, of
   which 4 in band; they show measured misses 2–4 pp above Poisson at m ≈ 2.2 (108
   switches/s) and far above at m ≈ 3.5 (W = 8: 41% against 2.9%). The report must give the
   out-of-domain cells beside the in-domain ones and call the 50% line empirical.

4. **Minor — what S6 still tests.** `none`, `reset-always`: an independent prediction (no
   measured input beyond the calibration costs), ±5% — a real test; it found the plan's
   S/(S+r) wrong for short leases and the correction (two round trips per lease) is right.
   `reset-on-switch`: uses the run's measured reset fraction, so it tests the cost per reset,
   not how many there are. `remake-*`: uses the run's measured connects per lease and connect
   time; it confirms the run sits at the connect ceiling and nothing more (the notes say
   this). The "below the knee" clause only checks goodput ≥ 97% of offered. The report must
   not present the remake rows of table 4's "predicted knee" as predictions.

5. **Minor — S8's starvation check is a FIFO bound on the longest wait** (1 s + 2 × the time
   to serve wait-list high-water + cap leases), not a direct "waits while connections sit
   idle for others". It would catch an unbounded wait; with `pool.wait_cap=200` it cannot see
   a wait that ends in a refusal. Acceptable; say so in the report.

6. **Minor — the S9/S10 check change (`d1bea83`) is legitimate.** "Restart seen" became
   `db_lost > 0 OR refused connects > 0`; the storm bound, goodput-returns, cap and quiesce
   assertions are unchanged. One loosening rode along: S9 no longer requires `refused > 0`
   when `db_lost > 0`. Harmless (a pool with nothing to reconnect during the outage refuses
   nothing).

7. **Minor — default S1 runs 20 seeds; the plan's 200 need `LEASE_E3=full`.** E7 must run the
   full form (done once in this review: green).

8. **Note for E6/E8, not E3's defect.** (a) Table 6 computes m = λ × mean connect time; E3's
   tests use the pipeline occupancy (connecting + priming + closing, Little's law), which is
   the right lead time under close-then-open. Table 6's prediction is therefore a few percent
   low and its 1.96σ band has no floor, so cells E3 calls "in band" print "above" there
   (e.g. W = 1 at load 0.34: 24.8% against 21.7%). (b) Table 6 groups by policy, patience,
   warm mode and W — not by arm — so `.pauto` and `.pauto.prime` are pooled as two "runs",
   and so are the two `remake.order` arms of S11. (c) With W = 0 the trials exclude arrivals
   that found no free slot (the cap fills with tagged idle connections), so λ in a fixed:0
   row is lower than in its W ≥ 1 neighbours; the miss rate is 100% either way.

## Carry-forward for E7 and E8

- **Report parameters for simulator sweeps:** `scripts/lease-report.sh <sweep-out-dir> <out>
  baseline_arm="'none'"`. Nothing else is needed (`min_valid_reps` does not block single
  runs; `verdict_load=80` is the default and S6/S7/S8 have an 80% cell). Run the report on
  the simulator campaign separately from the real one.
- **S5 / the §10 warm-stock line — evaluation rule.** Use the campaign's 120 s runs, 3 seeds
  (more if time allows: `seeds=5`), `remake.patience=auto`. Report two miss rates for R1 and
  for every fixed W: (i) **whole run**, cold start included, and (ii) **steady state**,
  excluding the first 10 s (difference the flushes; the step cell flushes every 4 s, so use
  [12 s, end]; for steady and bursty re-run with a flush at 10 s or use `obs.warmup` once E5's
  repair is merged). The §10 verdict is taken on (ii), pooled over seeds, with a 95% binomial
  band: pass if the upper bound ≤ ε, fail if the lower bound > ε, otherwise inconclusive.
  (i) is printed beside it and the cold-start cost stated in misses (it is a fixed cost: about
  7 misses in the first 4 s and about 37 at a ×4 step edge on seed 1). Hindsight-fixed = the
  smallest W in {0, 1, 2, 4, 8} whose pooled miss rate on the same window and seeds is ≤ ε by
  the same rule; warm-connection-seconds ratio on the same window. State that the ladder is
  coarse (W = 4 misses 2.2–2.3%, so hindsight is 8), which flatters R1's 0.6× ratio: add
  fixed W = 5, 6 to the S5 arms in the repair if cheap, otherwise say so. `bursty`: nothing
  holds ε; report it as the model's stated limit 3, not as a verdict on R1.
- S3 in the report: E3's `-summary.tsv` (`pipeline_m`, `miss_rate`) is the table of record for
  model-against-measured; table 6 beside it with finding 8(a) stated.
- Run full-form S1 and S2 in E7 (`LEASE_E3=full`, about 10 min together) and quote the counts.
- State in the report: the simulator's connect ceiling is 174/s (11% under the measured
  195/s); lease work does not load the connect CPUs; S6's remake rows are consistency checks;
  the S3 domain line is empirical; `remake.order` and patience made no difference wherever
  no tenant holds a connection at its next arrival.
- E3's findings 5 and 10 (NOTES-E3 §7) are the simulator's answer on `remake-on-switch` and
  `reset-on-switch`: both depend entirely on how sticky tenants are; with 100 or more tenants
  on 20 connections `reset-on-switch` equals `reset-always` and `remake-on-switch` sits at the
  connect ceiling at every load.
