# Review 2 (confirm, final) of piece E3 — repair round

Reviewer: Fable (coordinator), 2026-11-26. Scope: commits `964696a`, `90e8b9d` against
`/root/briefs/zgateway-lease-e3-repair.md` and REVIEW-E3-1.md. **Verdict: NITS** — the
required repair is done; E3 can merge.

Verified:
- Diff touches only E3's own files and the notes (10 files); no E1/E2/E6 file. The rename of
  E3's test helpers (`logTable`, `bySize`) resolves a compile clash with E6's
  `report_test.go` after the merge — legitimate.
- gofmt and vet clean; `go test -short -run 'Label|S5|S3|S1'` passes (72 s).
- Finding 1 (labels): `labels_scenario_test.go` checks uniqueness of (campaign, test, arm,
  load, seed, rep), one workload per comparison group, and the `none` baseline in each group
  the report compares; a second test shows the old labels produce 199 duplicates, so the
  check can fail. The builder's reduced sweep gave an empty table 0 and populated tables.
  I read the test; I did not re-run the reduced sweep myself — E7 runs the full one and
  table 0 is checked there first.
- S5 now applies one rule to R1 and to every fixed W (steady-state window, pooled seeds, 95%
  binomial band) on the ladder {0,1,2,3,4,5,6,8}.

Notes for E7/E8 (no repair):
1. The finer ladder changed the headline: on `steady` hindsight-fixed is W=5 and R1 costs
   1.04–1.05× its warm-connection-seconds (inside 1.5×, but no saving); on `step` 0.65× of
   W=8, with R1's miss rate inconclusive at 5 seeds (upper bound 1.009% against 1%). Report
   as measured in E7, not from these notes.
2. `bursty` holds ε for no rule or size below 16 — the model's stated limit.
3. The patience-0 arm was dropped from S5 (identical counts at this tenant count); it stays
   in S6–S8.
4. Remaining minor items of review 1 stand (S6 remake rows are consistency checks; S3's
   domain line was drawn after the grid; default S1 is 20 seeds — E7 runs the full form).
