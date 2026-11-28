# The lease experiment: method facts for later measurements (lease-lab port, 2026-11-27)

Measurement findings from the lease experiment (chunk 5, stages 0 to 3, originally on the
deleted branch `exp/lease-lab`) that govern connection and reset benchmarking on this VM.
Chunk 14's calibration and benchmark work starts here. Unless given a full repository path,
files below live under `agent_work/chunks/c05-classifier/lease-exp/`.

## Measuring connects and round trips

- Connect to the container's bridge address rather than the published `127.0.0.1` port.
  Traffic to the published port passes through Docker's userland proxy (`docker-proxy`),
  which makes connects about 12% slower and round trips twice as slow as the bridge address
  (`CALIBRATION-NOTE.md`, "Conditions") and burns 1.6–2.9 ms of `docker-proxy` CPU per
  connect in sustained cells (`lab-records/NOTES-E0.md`, section 5).
- Treat the connect ceiling as a CPU ceiling, not serial backend startup. Postgres 17 on
  this host sustains about 195 new connections per second (burst about 235/s), flat from 8
  to 64 concurrent connects. Each connect-and-close consumes roughly 14 ms of Postgres CPU
  and 5.5 ms of client CPU, so the plateau appears when all 4 vCPUs are full
  (`CALIBRATION-NOTE.md`, verdict on P2). Any concurrent work sharing those CPUs shifts the
  ceiling.
- Report reset latency as a range paired with backend history, not a single constant. How
  long `DISCARD ALL` takes depends on how recently the backend was busy: 177 µs drops to
  about 104 µs across five consecutive resets, while a clean session in a tight loop reaches
  88 µs (`CALIBRATION-NOTE.md`, "Limits" and the calibration table).
- Account for `pg_sleep` overshoot when sizing leases. A 5 ms `pg_sleep` lease holds its
  database connection for about 5.3 ms (`REPORT.md`, section 4).

## Reading results

- Measure against the real tier's A/A noise floor before calling a tail difference real. At
  the verdict load, two identical baseline arms differed by 109% in p99 wait on real
  Postgres, so p99 shifts of that scale are noise (`REPORT.md`, summary). The verdict SQL
  marks rows inside that floor `"inconclusive"` (`report-sql/09-verdict.sql`).
- Compute reset-policy capacity from full lease hold time rather than statement work `S`
  alone. A lease holds its connection for `h0 = S + 2 round trips + 21 µs` (for the lab's
  two-statement lease), and each reset adds `r = round trip + 89 µs`. Pool capacity is:

      cap / (h0 + resets per lease × r + connects per lease × (close lag + connect + fresh-backend penalty))

  For short leases, the ratio `h0 / (h0 + r)` gave 79.7% of baseline capacity against
  79.1–79.4% measured, whereas the plan's simpler `S / (S + r)` gave 73.9%
  (`lab-records/NOTES-E3.md`, section 3 item 6 and section 7 item 4; `REPORT.md`).

## Where the method lives

- `lab-records/NOTES-E0.md`: stage 0 conditions and measurement procedures (sections 2 and
  3), the `calibration.json` schema (section 4), and limits and confounders (section 6).
- `report-sql/`: the DuckDB 1.5.x report suite (tables 00–10, `views.sql`, `params.sql`,
  `empty/` sentinels, `conservation.sql`) and its runner `lease-report.sh`. Because the run
  sets kept on `main` lack `metrics.csv` and `histograms.csv`, running the script over the
  r6 set reproduces only table 01 and the runner rows of table 00
  (`agent_work/chunks/lease-lab-port/NOTES.md`).
- `e7/isolation/e4.sql`: the isolation oracle query, which reproduces `e4-oracle-check.txt`
  from `e7/isolation/per-run.tar.gz`.
- `TestTierRSessionAblationWithoutResetCrosses` (`test/e2e/tierr_test.go`): enforces the
  experiment's positive-control rule on real Postgres—with the reset switched off, every
  column of `test/oracles/dirty-check.sql` must show dirt and a custom setting's secret must
  be read back.
