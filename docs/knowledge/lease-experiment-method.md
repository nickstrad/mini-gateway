# The lease experiment: method facts for later measurements (lease-lab port, 2026-11-27)

What the lease experiment (chunk 5, stages 0 to 3, on the deleted branch `exp/lease-lab`)
learned about measuring connections and resets on this VM. Chunk 14's calibration and
benchmark work should start from these. All paths are under
`agent_work/chunks/c05-classifier/lease-exp/`.

## Measuring connects and round trips

- **Use the container's own address, not the published port.** The published `127.0.0.1`
  port goes through Docker's proxy and is about 12% slower on connects and twice as slow per
  round trip as the bridge address (`CALIBRATION-NOTE.md`, "Conditions"). NOTES-E0 puts
  docker-proxy's own CPU at 1.6–2.9 ms per connect in sustained cells
  (`lab-records/NOTES-E0.md`, section 5).
- **The connect ceiling is CPU, not serial backend start.** Postgres 17 here sustains about
  195 new connections per second (burst about 235/s), flat from 8 to 64 concurrent connects.
  One connect-and-close costs about 14 ms of Postgres CPU and 5.5 ms of client CPU, and the
  plateau is where the 4 vCPUs are full (`CALIBRATION-NOTE.md`, verdict on P2). So the
  ceiling moves with whatever else shares the CPUs.
- **A reset has no single cost.** It depends on how recently the backend was busy: 177 µs
  falls to about 104 µs over five consecutive resets, and the best case (clean session, tight
  loop) is 88 µs (`CALIBRATION-NOTE.md`, "Limits" and the calibration table). Report a range
  and the backend's history, not one number.
- **`pg_sleep` overshoots.** A 5 ms `pg_sleep` lease holds its connection for about 5.3 ms
  (`REPORT.md`, section 4).

## Reading results

- **The real tier has an A/A floor.** Two identical baseline arms differed by 109% in p99
  wait at the verdict load, so p99 differences of that size on real Postgres are noise
  (`REPORT.md`, summary). The verdict SQL marks such rows "inconclusive"
  (`report-sql/09-verdict.sql`).
- **Capacity of a reset policy.** A lease holds its connection for h0 = S + 2 round trips +
  21 µs (S is the lease's own work; the lab's lease was two statements), and a reset adds
  r = round trip + 89 µs. The ceiling is cap / (h0 + resets per lease × r + connects per lease
  × (close lag + connect + fresh-backend penalty)). The reset/baseline ratio h0/(h0 + r) gave
  79.7% for short leases against 79.1–79.4% measured; the plan's S/(S + r) gave 73.9%
  (`lab-records/NOTES-E3.md`, section 3 item 6 and section 7 item 4; `REPORT.md`).

## Where the method lives

- `lab-records/NOTES-E0.md`: the stage 0 conditions and method per measurement (sections 2
  and 3), the `calibration.json` schema (section 4), and limits and confounders (section 6).
- `report-sql/`: the report SQL (tables 00–10, `views.sql`, `params.sql`, `empty/`
  sentinels, `conservation.sql`) and `lease-report.sh`, which runs it with DuckDB 1.5.x. The
  run sets main holds lack `metrics.csv` and `histograms.csv`; over the r6 set only table 01
  and the runner rows of table 00 reproduce (`agent_work/chunks/lease-lab-port/NOTES.md`).
- `e7/isolation/e4.sql`: the isolation oracle check; it reproduces `e4-oracle-check.txt` from
  `e7/isolation/per-run.tar.gz`.
- The tier R test `TestTierRSessionAblationWithoutResetCrosses` carries the experiment's
  positive-control rule: with the reset off, every column of `test/oracles/dirty-check.sql`
  must show dirt and a custom setting's secret must be read back.
