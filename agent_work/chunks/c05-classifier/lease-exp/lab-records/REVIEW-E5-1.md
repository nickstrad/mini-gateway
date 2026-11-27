# Review 1 of piece E5 — real-database shell, container script, campaign runner, R6–R9

Reviewer: Fable, 2026-11-26. Build: `exp/lease-lab-e5`, commits `a0c7adc..75480f4` (six, after
`4429d1a`). Against `/root/briefs/zgateway-lease-e5-real-shell.md` (with its carried-forward
sections), plan §8 R6–R9, and `REVIEW-E2-2.md`'s E5 lines.

**Verdict: FAIL, narrow.** The shell itself is sound: I found no defect in how it drives the
core, classifies closes and losses, or bounds the server. Four things around it would put wrong
or unreadable numbers into the tier R evidence and need the one repair round: two are joins with
E6's report SQL that no parameter absorbs (findings 1, 2), two are measurement-design gaps
(findings 3, 4). E4 is not blocked by any of them: its seams work as NOTES-E5 §6 says.

## What I verified, and how

- Read `realdb.go`, `realrun.go`, `realmon.go`, the ramp/round code of `realcampaign.go`,
  `scripts/lease-exp.sh`, the four full manifests, NOTES-E5 and `e5-smoke/`.
- `scripts/lease-exp.sh test` (fresh container, `go test -race -p 1 -run Real`): both packages
  pass, no race report (110 s + 9 s). `gofmt -l` empty, `go vet` clean. The diff adds 35 files and
  changes no existing one; nothing under `internal/sim`, `pgwire`, `obs`, `leasepool`.
- **One goroutine drives the core.** Every `eng.Request`, `eng.Tick`, `db.handle` and
  `runDeferred` is in `RunReal`'s loop (`realrun.go:191-255`). Reader goroutines only `post` to the
  channel; Sink calls caused inside a DB call go through `later` (`realdb.go:331,338,361,371`).
  Shared fields are atomics (`closing`, `EventHW`, `RowsDropped`) or handed over by the channel
  (`c.nc` before `revOpen`). `t0` is reset before `mon.Begin()`, so the monitor never reads it
  concurrently.
- **Close done = server EOF is sound.** Postgres runs `shmem_exit` (frees the PGPROC slot, clears
  the activity entry) before the `on_proc_exit` callbacks, of which `socket_close` is one, so EOF
  is after "slot free". R6 counts `application_name = 'mglease'`; the monitor is `mglease-mon` and
  not counted. A backend still authenticating is not counted by name, but the core counts that
  connection as connecting, so the server count cannot exceed the core's. The canary bites (44
  against cap 20 in the builder's run; the test passed in mine).
- **Lost against closed:** `revEnd` with `closing` set is a close (`realdb.go:452`); without it, a
  loss, counted once (`:456-465`). A reset timeout is the core's close: Terminate plus a
  CancelRequest for the statement in flight (`:374-377`), never "lost". The reset reply (tag then
  status) is checked by the E2 engine and the core from `Result.Tag`/`Status` as the reader
  filled them (`:275-293`).
- **Open loop:** lateness = dispatch − intended, recorded per lease (`realrun.go:200-202`); the
  engine measures the wait from the intended start. Invalid runs are written, labelled
  (`real.valid`, `real.invalid`) and never re-run (`runDone` skips them as finished).
- **Campaign:** arm and load order rotate per round; seed = seed + round for every arm; one fresh
  container per phase; one capacity value from `capacity.csv` used for every arm (`:564-587`).
- **R9:** the cgroup path is the container's own scope (`docker-<id>.scope/cpu.stat`), read at
  start (after setup), window end and end (before shutdown); per 1,000 = over leases released.
- **My own runs** (7 arms at cap:80 of a pinned capacity, 10 s each, not under the lock, host
  busy with the E3 builder — not timing evidence): every arm completed, server peak 20 or less,
  quiesce 0, `remake-always` 84 goodput/s with 16,474 refused; all files present.
- **The join with E6**: I ran `scripts/lease-report.sh` (from `exp/lease-lab`, E6 merged) on those
  run directories. Table 0 lists all seven runs as `broken` (finding 1).

## Findings

1. **Major — `server.csv` does not match the report's reader; every real run is "broken".**
   E5 writes `t_ns,backends,procs,query_ns` (`realmon.go:245`); E6 reads columns `time` and
   `count` (`test/analysis/lease/report/views.sql:89-92`, as the E5 brief worded the sidecar).
   Evidence: table 0 on my seven runs — `server.csv | 101 rows without an integer count column`
   for each. A broken run blocks every verdict row. **Repair:** write the header
   `time,count,procs,query_ns` (time in ns, count = the `pg_stat_activity` count), and update
   `mglease summary`, `e5-smoke/r6-server.csv` readers and the notes.
2. **Major — CPU and validity keys.** E6 reads `lab.csv` keys `cpu.pg_ns` and `cpu.lab_ns` in
   nanoseconds; E5 writes `real.pg_cpu_us` / `real.lab_cpu_us` in microseconds. Pointing the
   parameter at E5's key gives table 10 a figure 1,000 times too small under an `_ns` heading.
   `cpu.csv` cannot stand in either (E6 sums columns `pg_cpu_ns`, `lab_cpu_ns`; E5's file holds
   cumulative readings at three points). **Repair:** also write `cpu.pg_ns`, `cpu.lab_ns` (ns,
   run start to end) and `run.valid` (`1`/`0`) to `lab.csv`. Keep the `real.*` rows.
3. **Major — no warm-up exclusion: the cold start sits in the p99.** Arrivals begin at full rate
   against an empty pool that opens 20 connections four at a time. The builder's own reading
   (NOTES-E5 §8): in `base` at cap:30, 136 of 7,013 leases waited, and wait p99 was 20–54 ms
   against a p50 of 0.14 ms. In a 35 s run the transient is still about 0.7% of leases — at the
   edge of the 99th percentile, in every arm, and differently for arms with a warm stock. §10's
   p99 line would then compare start-up transients. **Repair:** a key `obs.warmup=<duration>`
   (default 0 = today's behaviour; both tiers): leases whose intended start is before it are run
   and counted as now but left out of `lease_wait_ns`, `switch_ready_ns`, `first_stmt_ns`,
   `lease_total_ns` and the window goodput (window = warmup → duration). Written to `lab.csv`.
   This touches E2's engine/metrics files; that is allowed for a real defect and must keep U5
   and conservation green. Manifests: `obs.warmup=5s`.
4. **Major — the capacity estimate is the maximum of single noisy steps.** `ramp()` takes the
   highest window goodput over all steps, invalid ones included (`realcampaign.go:526-530`). In
   the smoke that was 2,353/s where the other four saturated steps gave 1,996–2,201/s: 7–18% high.
   "cap:80" is then nearer 90% of what the pool sustains and "cap:95" is overload, which moves the
   §10 verdict load. **Repair:** capacity = the median window goodput of the saturated steps
   (refused > 0), valid steps only when at least three are valid; write the rule and every step
   to `capacity.csv`. With finding 3, measured after the warm-up.
5. **Minor (recommended in the same round) — R8 as declared will fail for a known, modelable
   reason.** Postgres's `pg_sleep` waits in whole milliseconds (`ceil` of the remainder), so the
   real hold is the drawn time rounded up plus a wake-up delay: the builder's probe gives 1.48 /
   4.79 / 5.86 / 6.82 ms for 0.001 / 3.5 / 5.0 / 5.1 ms asked. The simulator holds exactly the
   drawn time. Plan §10 makes a simulator-against-real disagreement an invalidating result, so
   this should not be left to chance. **Repair:** two simulator keys, default 0 (existing
   simulator results unchanged): `db.hold_quantum` (round the hold up to a multiple) and
   `db.hold_extra` (added after rounding). `r8.txt` runs the simulator twin both ways (plain,
   and with `db.hold_quantum=1ms db.hold_extra=<probe>`), the extra taken from an independent
   probe (`pg_sleep` round trips on the campaign's own container, before the runs — not fitted
   to the R8 runs). Also: R8's S6-like loads 60 and 90 are at or beyond the real pool's knee
   (real capacity was 59% of nominal); use loads below it (15, 30, 45).
6. **Minor — `remake-always` at load is always `late_p99`-invalid** (the lab's own TLS and SCRAM
   work starves the loop). E6 then prints "inconclusive" for the control. Not a defect; E8 reads
   the control from the all-runs median and says so.
7. **Minor — the rate gate** compares leases dispatched inside the window with leases generated,
   not with the intended rate; the schedule's own deviation is recorded beside it. Acceptable.
8. **Minor — R7 samples the server every 100 ms**; the R6 claim rests on `r6.txt` (2 ms, back to
   back). Say so in the report.
9. **Minor — E6's table 8 takes one percentage band; E5 declared a compound band** (hit rate
   within 10 points; waits within ×2 or 1 ms; goodput within 1 point). `mglease compare` is the
   R8 table of record; table 8 is shown beside it with `sim_real_band_pct=100` (the ×2).

## The lateness bound

Keep 5 ms at p99 and 50 ms maximum. On a quiet host the builder measured p99 of 1–3 ms; in my
runs with about two CPUs taken by other work, one of six non-control runs failed it (11.4 ms).
The bound is doing its job: it flags contended runs. The condition for E7 is a quiet host — no
builder running — checked from `real.other_cpus_milli` (expect under about 300). If more than a
fifth of a round's non-control runs are invalid, stop and wait rather than loosen the bound.

## Report parameters for E5's run directories (after the repair)

`scripts/lease-report.sh <root> <out> baseline_arm="'base'" aa_arm="'aa'" server_extra=0
verdict_load=<the workload.load that cap:80 became, from index.csv> min_valid_reps=5
sim_real_band_pct=100` — and `invalid_key`, `cpu_pg_key`, `cpu_lab_key` at their defaults once
finding 2 is repaired (before it: `invalid_key="'real.valid'"`). `verdict_load` must be given
per campaign, because `cap:` loads become a nominal `workload.load` such as `47.05`.

## Carry-forward

**E4:** the seams work on `mglease real` as on `mglease sim` (`oracle.csv` was written in my
runs). Use `identity=query` for (pid, start); open side sessions with
`OpenSession(db.(*RealDB).Options(), "mglease-e4", …)` under an application name other than
`mglease`; a blocking call from the inspector stalls the loop, which is fine at `oracle.rate=1`.
A killed backend recycles the whole pool. Name container tests `TestReal…`.

**E7 — a campaign of about 2.75 h** (the full manifests are 9.7 h). Per-load repetitions are not
a manifest feature, so use two manifests per mix sharing one capacity (`capacity <n>` in the
second, copied from the first's `capacity.csv`):

| Step | Content | Time |
| --- | --- | --- |
| R6 | `r6.txt` as is | 7 min |
| R8 | `r8.txt` with finding 5's changes | about 20 min |
| R7 mix A, verdict load | ramp; cap:80 only; all 12 arms; 10 repetitions × 30 s (+5 s warm-up) | about 70 min |
| R7 mix A, other loads | cap:30, cap:60, cap:95; 9 arms (drop `rmsd0`, `rmsdp0`, `rmaw`); 3 repetitions | about 50 min |
| R7 mix B | ramp; cap:80 only; 8 arms (`base`, `aa`, `ra`, `ros`, `rmsf`, `rmsf0`, `rmsd`, `rma`); 6 repetitions | about 30 min |

The §10 lines are read at cap:80 with ten repetitions on mix A; the other loads and mix B give
the shape with fewer. Run nothing else on the VM meanwhile.

**E8:** state the 100 ms sampling of R7 against R6's 2 ms; the control's invalid label
(finding 6); that real loads are fractions of a capacity measured on this host that day; the
`pg_sleep` granularity; and that the remake ceiling measured here includes the lab's own
client-side TLS and SCRAM work on the same four cores.
