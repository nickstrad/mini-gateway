# Review 2 (confirm, final) of piece E6 — report SQL repair

Reviewer: Fable, 2026-11-26. Scope: commits `ffb9b41`, `aaf8ef5` against
`/root/briefs/zgateway-lease-e6-repair.md` and [REVIEW-E6-1.md](REVIEW-E6-1.md).

**Verdict: NITS.** The four repair items are implemented as briefed and tested. No further
repair is needed; E6 can merge into `exp/lease-lab`.

## What I verified

- Read `git diff 1847335..HEAD`: only `05-waiting.sql`, `09-verdict.sql`, `report_test.go`
  and the notes changed.
- `go test ./internal/lab/... ./cmd/mglease/... -count=1` passes (leaselab 122 s), `gofmt -l`
  empty, `go vet` clean.
- **Rule 1 (real goodput):** floor ≥ 5% of baseline → inconclusive; else ratio < 0.95 or
  d > 2·floor → fail; else pass. The note prints ratio, d against 2·floor, the three medians
  and the valid/total reps. U6: identical arm passes (d = 0, floor = 0), d 2.1 against 2.0
  fails at ratio 0.979, 6% floor inconclusive.
- **Rule 2 (real p99):** |arm − 2·baseline| ≤ 2·floor → inconclusive; else pass/fail. U6 has
  one case of each.
- **Rule 3 (invalid reps):** `invalid` runs are dropped and counted; `broken` runs still
  block. U6: one invalid of ten still passes with "9 of 10" in the note; four valid of ten is
  inconclusive.
- **Rule 4 (clamp bucket):** baseline p99 in the lowest bucket → table 5 ratio empty, table 9
  compares the arm's p99 against `p99_abs_floor_ns` (1 ms): pass under, inconclusive over.
- **Single-run simulator comparisons are not forced to inconclusive.** The test is
  `valid < least(min_valid_reps, runs of that arm at the load)`, so one run of one needs one.
  Checked on my own campaign (skewed, 10 tenants, 80% load, seed 7, one rep each of `none`,
  `reset-always`, `remake-on-switch` fixed:4): table 0 empty, exit 0; `reset-always` goodput
  pass (1.0) and p99 pass (1.108); `remake-on-switch` p99 **fail** (2.59). No parameter is
  needed for simulator campaigns.

## Findings (minor, no repair)

1. Under rule 1 a floor ≥ 5% gives inconclusive even when the arm is plainly below 95%. That
   is the repair brief's wording, applied literally. E8: read the note's ratio in that case.
2. Simulator goodput is the smallest same-seed ratio, and open-loop runs below saturation
   release everything, so it reads 1.0 for any arm that keeps up. The goodput line only
   discriminates at or above the knee; the wait line carries the verdict below it. E8 should
   say so.
3. Isolation rows read "inconclusive: no oracle row checked" for campaigns run with
   `oracle.rate=0`. Expected; isolation verdicts come from the stage 2 campaign.
4. Review 1's finding 5 (a comment in table 6 that the lead time is the connect alone) was
   not done, by the repair brief's scope. E8 states it instead.
5. The ≤ 1 µs clamp test is the constant 1000 ns in both SQL files.

## Parameters for the coordinator

Unchanged from review 1, plus `min_valid_reps` (default 5) and `p99_abs_floor_ns`
(default 1 ms): `invalid_key`, `cpu_pg_key` / `cpu_lab_key`, `sim_real_band_pct`,
`baseline_arm` / `aa_arm`, `server_extra`.
