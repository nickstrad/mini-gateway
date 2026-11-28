# Lease lab — review 1 of piece E6 (report SQL, U6)

Reviewer: Fable, 2026-11-26. Build: `exp/lease-lab-e6`, commits `31b90e3..88cefa3`. Brief
`/root/briefs/zgateway-lease-e6-report-sql.md`; plan §9–§10; `REVIEW-E2-2.md` carry-forwards.

**Verdict: FAIL — narrow.** The tables, the conservation checks and the isolation matrix are
right, and every number I recomputed by hand matched. Three rules in table 9 (the verdict
table) would print a wrong or needlessly empty verdict for the real tier. They are small
changes to one file plus their U6 cases. Two of them trace back to loose wording in the brief
("inconclusive when the difference is inside the A/A floor", "a required run is … invalid");
they are still wrong against plan §10 as ruled, so they are repaired here.

## What I verified, and how

- `go test ./internal/lab/... ./cmd/mglease/...` green (leaselab 107 s), `gofmt -l` empty,
  `go vet` clean; the diff adds 25 new files and changes no existing one.
- Read every SQL file and `scripts/lease-report.sh`.
- Made 10 simulated runs myself (skewed, 10 tenants, cap 20, medium leases, seed 7, loads 60
  and 80, `oracle.rate=1`, `obs.interval=1s`; arms: baseline, reset-always, remake-on-switch
  fixed 2, fixed 4, dynamic), ran the report on them (table 0 empty, exit 0) and recomputed
  from the raw CSV with my own SQL / Python:
  - p99 lease wait at 80%: baseline 35,389,440 ns, reset-always 40,632,320, remake arms
    96,993,280 → ratios 1.148148 and 2.740741, as table 9 prints.
  - Table 6 at 60%: fixed W = 4 has misses 4,752 and n = 5,840 − (4,753 − 4,752) = 5,839
    in the raw counters, as printed; fixed W = 2 has 3,799 of 4,386 = 0.866165, m = 219.3/s
    × 20.26 ms = 4.4432, P(Poisson ≥ 2) = 0.935997 (mine) against 0.935999; W = 4: 0.855390
    against 0.855387. The series macro is correct.
  - Histograms: `csvflush` counts are cumulative since the start (`schema.go:70`); the sum of
    `lease_wait_ns` counts in the last flush equals granted (63,825). Reading the last flush
    only is right. Quantile = upper edge of the bucket holding the ⌈q·n⌉-th value, with the
    bucket's relative width beside it: correct and stated.
- Table 0: the four conservation laws per flush include `pending`; the seven state integrals
  against `conn_ns_total`; cap and server bound; missing files, bad labels, duplicate labels,
  empty campaign; runner-invalid and real-tier `db_lost` as severity `invalid`. Every other
  table reads `ok_runs` only. No silent pass found.
- Table 2: `-1` (not checked) never counts as checked or found; a kind nobody checked prints
  `not checked`, and an arm with nothing checked is `inconclusive` in table 9. Across-tenant
  and same-tenant hits are separate; residue is its own class and outside the verdict;
  identity is per (pid, start) for the remake policies (the simulator writes distinct pids,
  so the rule works there too). The baseline's structural row reads `found` (58,859).
- Table 7: hindsight W = the smallest fixed W whose miss rate ≤ the dynamic run's ε on the
  same seed, rep and workload; warm-connection-seconds from `warm_ns_total`. Correct.
- Table 10: switches from the lend labels (`leases_switch` warm, connect, reset, ready), not
  `switch_arrivals`; both ceilings; CPU from calibration and, where present, measured.
- U6: planted runs are generated in Go from a few numbers with no E3/E5 code; the canary
  (isolation query without `cursors`) makes the leak test fail; the planted conservation
  break, missing sidecar and empty campaign all land in table 0 with exit 1.

## Findings

1. **Major — real-tier goodput verdict is not plan §10** (`09-verdict.sql`, `v_gp`, the two
   `WHEN abs(r.base_gp - r.gp) …` lines). As built: `|baseline − arm| ≤ floor → inconclusive`.
   Consequences: (a) an arm identical to the baseline with an identical A/A pair (the normal
   case below capacity in an open-loop run: every lease completes, d = 0, floor = 0) reads
   **inconclusive**; (b) U6's own "near" case (arm 99.5/s, baseline 100/s, floor 1/s) is
   asserted `inconclusive`, though §10 passes it: at least 95% of the baseline and not below
   it by more than twice the floor. **Repair:** with d = baseline − arm, floor =
   |A/A − baseline|, ratio = arm / baseline (medians over valid reps):
   - `fail` when ratio < 0.95 or d > 2·floor;
   - otherwise `pass`;
   - `inconclusive` only when there is no A/A arm, too few valid reps (finding 3), or
     floor ≥ 5% of the baseline (the noise is as large as the threshold, so 95% cannot be
     resolved).
   The note keeps arm, baseline, d and floor so a fail by a hair is visible. U6: "near" must
   pass; add d = 0 with floor = 0 → pass; a floor of 6% → inconclusive.
2. **Major — real-tier p99 verdict, same shape** (`v_p99`,
   `WHEN abs(r.p99 - r.base_p99) <= abs(r.aa_p99 - r.base_p99) THEN 'inconclusive'`). An arm
   whose p99 is within the noise of the baseline's meets "within 2× the baseline's" unless
   the noise is itself about as large as the baseline. **Repair:** floor = |A/A p99 −
   baseline p99|; `inconclusive` only when the floor can flip the comparison, i.e.
   |arm − 2·baseline| ≤ 2·floor (or no A/A, or too few reps); otherwise `pass` when
   arm ≤ 2·baseline, else `fail`. U6 case for each branch.
3. **Major (practical) — one flagged repetition empties the arm's verdict** (`grp.n_bad`,
   `missing.why`). Any run of the arm, the baseline or the A/A arm at the verdict load that
   is in table 0 makes goodput and p99 `inconclusive`, including severity `invalid` (the
   runner's validity gate, `db_lost`). On this shared VM some of 10 repetitions will be
   flagged, so most real-tier rows would be empty. **Repair:** severity `broken` keeps the
   present rule. Severity `invalid` repetitions are dropped (the real tier's medians are
   unpaired, so nothing else is needed), counted in the note (`valid reps 8 of 10`), and the
   row is `inconclusive` only when the arm, the baseline or the A/A arm has fewer than
   `min_valid_reps` valid repetitions (new parameter, default 5; with fewer repetitions run
   in total, all of them). The same for the isolation and warm rows (`arm_bad`): only
   `broken` runs block. U6: a planted group with one invalid rep of ten still gives a verdict
   and says so; with six invalid it is inconclusive.
4. **Minor — a baseline p99 in the clamp bucket gives a meaningless ratio.** Simulator runs
   put waits under 1 µs in the lowest bucket `[1000, 1004)`. If the baseline's p99 at the
   verdict load is there, any arm with a p99 of a few µs "fails" by a ratio of thousands
   (table 5 already prints 56,397 for `switch_ready_ns`). In `v_p99` (sim and real) make the
   row `inconclusive` with the note `baseline p99 below the histogram floor (≤ 1 µs)`; in
   table 5 print the ratio as empty in that case. Same repair commit.
5. **Minor — table 6's lead time is the connect alone.** `L = connecting_ns_total /
   connects{started}`; a replacement under `close-then-open` also waits for the close of the
   evicted connection (0.8 ms) and, with `warm.prime=select1`, the prime. The predicted miss
   is therefore a slight under-estimate. No change needed; say so in the header comment.
6. **Minor — table 6 at overload means little.** At 80% in my runs every fresh-demand
   arrival found the stock empty (30,853 of 30,853) while only 2,457 connects ran: after
   patience expires a lease counts as fresh demand and is then mostly served by its own
   tenant's returning connection. m = λ·L = 31 there describes demand the pipeline never
   served. The row prints `inside` (1.0 against 1.0), which is true and uninformative. For
   E8: read table 6 only on rows where the stock is not permanently empty.
7. **Note — `workload.load` is in percent** (`80`, not `0.8`; `0.8` runs at 0.8% load and
   table 9 then finds no run at `verdict_load`). Not a defect; E3/E5 manifests must use it.
8. **Note — the comparison key leaves out arm-level knobs** (`affinity`, `warm.prime`,
   `reset.reseed`, `remake.patience`, `cm.avail_pm`, `connect.max_concurrent` are not in
   `workload`). Two `none`+`cold` arms that differ only in one of them (S8's affinity on and
   off) are read as baseline and A/A partner, in name order. Harmless in the simulator (the
   A/A arm is used only in the real tier) but the coordinator must set `baseline_arm` where
   the first name is not the intended baseline.

## Required repairs (one round)

Findings 1, 2, 3 and 4, in `09-verdict.sql` (and table 5 for the ratio in 4), each with its
U6 case; the header comment of table 9 and NOTES-E6.md §3 item 11 rewritten to the new
rules; finding 5 as a comment. Nothing else changes.

## Parameters the coordinator must set at report time

`invalid_key` / `invalid_ok` and `cpu_pg_key` / `cpu_lab_key` (or `cpu.csv` columns) to E5's
names; `sim_real_band_pct` to E5's declared band; `baseline_arm` / `aa_arm` when the A/A
labels do not sort baseline first; `server_extra` if E5's monitor session counts itself;
`min_valid_reps` (after the repair). Loads in percent. Run from inside the repository.
