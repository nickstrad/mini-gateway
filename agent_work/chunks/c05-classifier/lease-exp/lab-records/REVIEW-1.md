# E0 review 1 — Fable, 2026-11-26 — FAIL (one major, one minor)

Reviewed at `06c0e6d`: `cmd/mgcalib/`, `scripts/lease-calib.sh`, NOTES.md, the three run directories.

Checked and accepted:
- `mgcalib summarize runs/r0-* | cmp - calibration.json`: identical.
- Recomputed from `samples.csv` and `cells.csv` in DuckDB, independently of the summarizer: M1,
  M3 (totals and phases), M4, M5, M6, PBKDF2 quantiles; M7 sustained rates per run; M7 burst
  rates; CPU per connect. All agree with calibration.json. No failed sample in 284,000 rows.
- Re-ran `ONLY=M1,M7B,M7S` myself (03:49–03:57 UTC, fresh containers): sustained bridge
  60 / 104 / 157 / 181 per second at c = 1 / 2 / 4 / 8 (Opus: 60 / 103 / 152 / 181); bursts
  202–236/s (B=100) and 199–255/s (B=500). Cells at c ≥ 16 in my run were hit by another
  agent's work (3.5 CPUs) and read lower, as the contended label predicts. M1 bridge p50
  113 µs on a busier host (Opus: 88 µs).
- Method of M1, M3, M4, M5, M6, M7: sound. The connect goes through `pgwire.BackendSession`;
  timing points are where NOTES.md says; CPU accounting windows match the cells.

Findings:
1. **Major — M2 per-kind costs are an ordering artefact.** Rotating the starting kind keeps
   each kind's predecessor fixed, and the p50s order by distance from the last temp-table
   drop (`set` 250 µs after `all`; `set_role` 255 after `temp_table`; … `listen` 118).
   `setseed`, which leaves DISCARD ALL nothing to do, reads 189 µs against M1's 88.
2. **Minor — `probe.out` is ignored by `.gitignore` (`*.out`)**, so the stdout deliverable is
   not committed.

Repair brief: /root/briefs/zgateway-lease-e0-repair.md.
