# Lease lab — piece E5 notes: real-database shell, container script, campaign runner, R6–R9

Builder: Opus, 2026-11-26. Brief `/root/briefs/zgateway-lease-e5-real-shell.md`, rules
`/root/briefs/zgateway-lease-lab-rules.md`. Branch `exp/lease-lab-e5` (forked from the
reviewed E2 tip `4429d1a` of `exp/lease-lab`); not pushed, not merged.

**Build round:** no existing E1/E2 file was changed. **Repair round** (section 12): five E2 files
changed for REVIEW-E5-1 findings 3 and 5 (`config.go`, `engine.go`, `metrics.go`, `run.go`,
`simdb.go`; every function listed in section 12). Change request: none; nothing frozen was touched.

## 1. What is built

| Where | What |
| --- | --- |
| `internal/lab/leaselab/realdb.go` | `RealDB`: E2's `DB` seam over TCP + TLS (sslmode=require) + SCRAM-SHA-256 through `pgwire.BackendSession`; `Login`, `RealSession`/`OpenSession` (blocking side session), the canary switch |
| `internal/lab/leaselab/realrun.go` | `RunReal`: the wall-clock loop that pumps the engine, the validity gate, the run directory; the nanosleep `waker`; `WriteSummary` |
| `internal/lab/leaselab/realmon.go` | `Monitor` (pg_stat_activity sampler, R6; host `/proc` process count), `EnsureLabObjects` (`lab_kv`, `lab_fn` as calibration made them), CPU accounting (`ReadCPU`, `cpu.csv`, R9) |
| `internal/lab/leaselab/realsum.go` | `Summarize`: `summary.csv`, the same headline numbers for both tiers (deterministic, no clock) |
| `cmd/mglease/real.go` | `mglease real`: one run against a server |
| `cmd/mglease/realcampaign.go` | `mglease campaign <manifest>`: interleaved, resumable runs, ramp → capacity, sim / real / both tiers |
| `cmd/mglease/realreport.go` | `mglease summary <root>` (per arm × load medians, validity, A/A) and `mglease compare <root>` (R8 table and band) |
| `scripts/lease-exp.sh` | containers, the machine lock, `run` / `campaign` / `test` / `detach` |
| `lease-exp/manifests/` | full: `r6.txt`, `r7a.txt`, `r7b.txt`, `r8.txt`; as run in the smoke: `r6-smoke.txt`, `r8-smoke.txt`, `r7a-smoke.txt` |
| `lease-exp/e5-smoke/` | the smoke's summaries (section 8) |
| `internal/lab/leaselab/real_test.go`, `cmd/mglease/realcampaign_test.go` | tests (section 7) |

Size: 2,876 lines of Go and shell including comments (realdb 614, realrun 535, realmon 337,
realsum 99, `cmd/mglease` real 143 + realcampaign 644 + realreport 316, script 188) and 472 lines
of tests (13 top-level tests: 7 in `leaselab`, 6 in `cmd/mglease`), against the plan's 350 / 300.
The campaign runner, its report commands and the monitor/CPU accounting are most of the excess.

## 2. How the real shell works

**Threads.** One goroutine per connection logs in (blocking `Login`, deadline
`connect.timeout`) and then only reads: it turns backend messages into `Result`s (one per
ReadyForQuery; `D`, `C`, `A`, `E`, `Z` as the simulator shell does, notifications included in
the next `Result` as E2 §2.1 says) and posts them to **one channel** (capacity 4,096; a full
channel blocks the reader, nothing is dropped). The run loop is the only goroutine that calls the
engine, the pool core and every `Sink` method, and the only one that writes queries. A `Sink`
call caused by a `DB` call (a write error, a close of a dead socket, a close in the canary) is
queued and made after that call returns (`RealDB.later` / `runDeferred`), never inside it.
The monitor has its own goroutine and session; the arrival waker its own locked OS thread.

**Clock.** `Now()` is `time.Since(t0)` (monotonic), `t0` reset just before the first arrival so
setup is not counted as lateness.

**Open loop.** The same seeded generator as the simulator (`NewGenerator` over
`sim.NewStream(seed, label)`, exactly the streams `RunSim` uses — `TestRealSameScheduleAsSim`).
A lease is dispatched at its intended time or as soon after as the loop can; lateness
(dispatch − intended) goes to a histogram; nothing is stretched or skipped. The loop sleeps
until the next arrival, tick (100 ms) or flush. Go's runtime timers wake an idle program only to
the millisecond (the netpoller waits in whole ms), which showed as 0.5–1 ms median lateness; a
**waker** goroutine on a locked OS thread `nanosleep`s to the target and signals the loop, which
brought the median to about 0.1–0.17 ms on this host (section 10).

**Lease work.** E2's `plain` script unchanged: the short statement of `workload.stmt_class`
(default `pk_lookup` = `SELECT v FROM lab_kv WHERE id = 42`), then `SELECT pg_sleep(<drawn
time>)`. `lab_kv` (1,000 rows, analysed) and `lab_fn` are created at run start if missing
(`EnsureLabObjects`, the statements of `cmd/mgcalib`). `first_stmt_ns` is the engine's, by mode
(fresh / reused / after_reset), and in `summary.csv`. **pg_sleep is not exact**: Postgres sleeps
in whole milliseconds (`ceil(remaining × 1000)` per `WaitLatch`), so the server-side hold is the
drawn time plus up to about 1 ms (mean about +0.5 ms on the 5 ms mean) plus a wake-up delay.
The simulator's fake sleeps the drawn time exactly unless `db.hold_quantum` / `db.hold_extra` are
set (repair round, section 12 item 5): R8's manifest sets them from an independent probe.

**Close and "close done".** `Close` = Terminate on the socket (and, if a statement is in flight,
a CancelRequest on a fresh TCP connection first, so a running `pg_sleep` does not hold the
backend). Close done is reported when the reader sees the **server end the connection** (EOF /
TLS close_notify). How we know the backend is gone: Postgres closes the client socket in an
`on_proc_exit` callback (`socket_close`, registered by `pq_init`), and `proc_exit_prepare` runs
`shmem_exit` — which releases the PGPROC slot (`ProcKill`) and clears the `pg_stat_activity`
entry — before the `on_proc_exit` callbacks. So EOF arrives only after the slot is free and the
backend has left `pg_stat_activity`. **Cost: nothing extra** (no poll; stage 0 polled
`pg_stat_get_activity(pid)` at about 0.7 ms per poll). The process itself may live a few µs
after the EOF until the postmaster reaps it; R6's `procs` column (host `/proc`) shows that
(section 8). A close that does not see the EOF within 5 s (`CloseMax`) drops the socket, is
counted (`real.close_timeouts`) and makes the run invalid. R6 is the check that the bound holds
(`TestRealR6CapHolds`), and the canary is the check that R6 can fail.

**Lost.** The core has no per-connection loss event: any loss is "database lost" and recycles
the pool (E2 decision 5). A connection is reported lost (`Lost`, or `ExecDone{Err: lost}` with a
statement in flight) **only** when its socket ends while we were not closing it (a flag set
before Terminate is written, read by the reader when the socket ends) or a write to it fails.
Our own close, a reset or prime timeout (the core closes; we Terminate) and a close timeout are
never reported as lost. Every loss is counted: `real.lost_events` and the core's `db_lost`, both
in `lab.csv`; `real.write_errors`, `real.close_timeouts`, `real.cancels`, `real.stale_results`
(replies that arrived after a close) beside them. `TestRealEachPolicyCompletes` asserts zero
losses over hundreds of closes; `TestRealDeadSocketIsLost` kills one backend from a side session
and asserts exactly one loss and an invalid run.

**Backend identity.** The pid comes from BackendKeyData at every connect (free). With runner
argument `identity=query` the connect also runs `SELECT pg_backend_pid(), backend_start …`
(one round trip, inside `connect_ns`, before the connection is reported open) and `StartNs` in
`OpenResult` (→ `oracle.csv` `backend_start_ns`) is `backend_start` in Unix ns; the pid is
checked against the key's. Default `identity=key` (timing runs): that statement would warm the
catalog caches and so shrink the first-statement penalty P5 measures. Every connection's
identity goes to `backends.csv` (`conn, backend_pid, backend_start_unix_us` (-1 under `key`),
`connected_ns`). `pg_postmaster_start_time()`, server version and `max_connections` are in
`lab.csv` `real.postmaster`.

## 3. Decisions the brief left open

1. **Close done = server EOF** (section 2), not a `pg_stat_activity` poll: free, and ordered after
   the slot is released. Bound checked by R6 on every run with the monitor on.
2. **R6's count** is `count(*) FROM pg_stat_activity WHERE application_name = 'mglease'`: the
   pool's sessions set `application_name=mglease` in the StartupMessage; the monitor's own
   session is `mglease-mon` (side sessions choose their own). It counts a backend from the end of
   authentication (PG 17 reports it after login) until its shared-memory exit, i.e. "still
   exiting" means "Terminate received, not yet out of shared memory". The **bound is `pool.cap`
   for both remake orders**: E1's core counts a closing connection against the cap until close
   done (NOTES-E1 item 9), so open-then-close only moves opens ahead of closes inside a step; the
   stated overlap is 0. Beside it, `procs` counts the postmaster's client-backend processes from
   the host's `/proc/<postmaster>/task/<pid>/children` (title `postgres: <user> <db>
   <host>(<port>)`, the monitor excluded by its port): every process from fork (during TLS and
   SCRAM) to reaping. It is reported, not asserted (section 8).
3. **Monitor interval** is a runner argument (`monitor=<dur>`, 0 = off). 2 ms in R6 (a sample's
   round trip was 2.2–10.7 ms p50 in the contended smoke, so in practice samples run back to
   back, 2–11 ms apart: `server.csv` has every sample's time and round trip); 100 ms in the R7/R8 manifests (cheap, fills the
   `db_backends` gauges and `server.csv`, and R6 is still asserted on those runs at that
   resolution).
4. **Validity gate** (per run, in `lab.csv` and `summary.csv`): `real.valid` = 1 unless one of
   `rate` (leases dispatched by the end of the arrival window / leases the seeded schedule holds
   < 98%: the shell fell behind), `late_p99` (generator lateness p99 > 5 ms), `late_max` (any
   lease > 50 ms late), `db_lost` (core `db_lost` > 0 or a lost socket), `close_timeout`,
   `violated` (any invariant). Reasons in `real.invalid`. Invalid runs are kept, labelled,
   counted in `summary` and never re-run. Bounds are runner arguments (`late_p99=`,
   `late_max=`, `rate_min_ppm=`). The 2% rate gate compares with the seeded schedule, not with
   rate × duration: the schedule's own Poisson deviation from the nominal rate is identical for
   every arm of a round (same seed) and is recorded (`requests.intended_milli` vs
   `requests.generated`). Why 5 ms and not less: with the waker, the median lateness is about
   0.1–0.2 ms but the p99 on this shared 4-vCPU VM is 1–3 ms even at low load (the loop thread
   is descheduled under CPU contention); 5 ms separates "falling behind" from scheduling jitter.
   **Load from other work** is noted, not gated: `real.load1_milli_start/_end` and
   `real.other_cpus_milli` (host busy − Postgres − lab over the run) per run; the campaign's
   `env.txt` has load, a quiet-host CPU reading and the top processes at start, and the load
   per phase.
5. **Fresh container per round**, not per run: a container takes about 3–5 s to start and become
   ready, which per 10–40 s run would be 10–40% overhead; within a round every arm then sees the
   same server state, and drift between rounds shows as round-to-round variation instead of as
   an arm difference (the arm order is rotated per round). Nothing a run leaves on the server
   carries over in a way the policies differ on: every run opens its own backends and closes them
   all (checked: zero backends of ours after each run, else the run is violated). The ramp runs in
   its own container before round 0.
6. **Seeds**: seed = `seed` + round, the same for every arm and load of a round (paired
   comparisons, and the A/A pair runs the identical schedule).
7. **Capacity** (the ramp): `none` + `cold` at nominal loads (r7a: 50, 70, 85, 100, 115, 130%
   of cap / mean lease time); per step the window goodput per second
   (`real.window_goodput_milli_per_s`: leases completed with goodput between the end of the
   warm-up and the end of the arrivals, divided by that window). **Capacity = the median window
   goodput of the saturated steps** (repair round, section 12 item 4; the build round took the
   maximum). `cap:<pct>` loads become `workload.load = pct × capacity / (cap / mean lease time)`.
   Written to `<root>/capacity.csv` with every step and which were used.
8. **R7 workloads** (both `skewed`, cap 20, medium leases): **mix A** `workload.tenants=6
   workload.skew=3000` — on real Postgres `remake-on-switch` `fixed:4` made 41 and 36
   connects/s at cap:80 and cap:95 (inside the 20–50/s budget at the higher loads; section 8).
   The smoke ran the mix first chosen in the simulator, 4 tenants Zipf 5.0 (22 / 39 connects/s at
   nominal 70 / 80 there), which on real Postgres gave only 7 / 24 because the real capacity puts
   cap:80 at nominal 47; **mix B** `workload.tenants=10 workload.skew=1100` (E2's C1 configuration) —
   126–133 connects/s at nominal 60–80, far outside. (Simulator sweep over 2–50 tenants and skew
   1.1–6.0, `/tmp`, not kept; under `reset-on-switch` with affinity even 2 tenants switch 400–770
   times/s at nominal 70–80, because waiters take whichever connection frees.)
9. **R7 arms**: `base` (none+cold), `aa` (none+cold, the A/A pair), `ra` (reset-always),
   `ros` (reset-on-switch), `rmsf` (remake-on-switch fixed:4, patience auto) and `rmsf0`
   (patience 0, the labelled extra), `rmsd`/`rmsd0` (dynamic, W_min 1), `rmsdp`/`rmsdp0`
   (dynamic + `warm.prime=select1`), `rma` (remake-always+cold, control), `rmaw`
   (remake-always + fixed:4, one repetition per load). W = 4 is a manifest value.
10. **R8 scenarios** (both tiers, same config and seed): S3-like = `remake-on-switch` fixed W ∈
    {1, 2, 4}, steady, 100 uniform tenants, nominal load 1 (40 leases/s, nearly every lease a
    switch, λ·L ≈ 0.8), below the cap; S6-like = `none` and `reset-always`, steady, nominal loads
    30/60/90. **Agreement band, declared here before any comparison run**: warm hit rate
    (1 − REVIEW-E2-2 finding 2's miss rate) within 10 percentage points; wait p50, p90, p99 within
    a factor of 2 or within 1 ms; goodput as a fraction of requested within 1 point. Outside the
    band is a finding.
11. **R9**: `cpu.csv` per run — the container's cgroup `cpu.stat` (`usage_usec`, `user_usec`,
    `system_usec`), the lab process's `getrusage(RUSAGE_SELF)` (includes the monitor goroutine
    and the Go runtime), host `/proc/stat` and the load average — at `start` (before the first
    arrival), `window_end` and `end` (after the drain, before the shutdown). `lab.csv`/`summary.csv`
    `real.pg_cpu_us_per_1000` = Postgres CPU over the run / leases released × 1000. No separate
    R9 manifest: every real run of R7 carries it, so it is per policy and load.
12. **The summary sidecar** `summary.csv` (key,value) is written for both tiers by my code (for
    the simulator from `RunSim`'s engine), so `summary` and `compare` read the same numbers;
    `lab.csv` gets the `real.*` rows before `run.result`. `run.replay` in a real run's `lab.csv`
    reads `mglease real …` (runner arguments are not config keys and are recorded as `real.*`
    rows instead; the address changes per container).
13. **Runner arguments, not config keys** (no change to `config.go`): `addr`, `password`,
    `user`, `database`, `monitor`, `pgpid`, `cgroup`, `identity`, `late_p99`, `late_max`,
    `rate_min_ppm`, `setup`. The simulator-only keys (`cm.*`, `db.net_one_way`, `db.close_lag`,
    `fault.*`) still appear in a real run's `lab.csv` with their resolved values; they have no
    effect there.

## 4. Run directory (additions to E2 §3)

`tier` R in `run.csv`, build `lab-real`, instance `real-1`; sources: `pool` only (no simulator
sources). Files beside E2's: `summary.csv` (key,value), `cpu.csv` (`point, t_ns, pg_usage_us,
pg_user_us, pg_system_us, lab_user_us, lab_sys_us, host_busy_ticks, host_total_ticks,
load1_milli`; points `start`, `warmup` (when `obs.warmup` > 0), `window_end`, `end`), `server.csv`
(`time, count, procs, query_ns` — time in ns on the shell's clock, count = the
`pg_stat_activity` backends of ours; only with the monitor on), `backends.csv`. `lab.csv` real
rows (before `run.result`): first E6's `run.valid` (1/0), `run.invalid` (reasons), `cpu.pg_ns`,
`cpu.pg_ns_measured`, `cpu.lab_ns`, `cpu.lab_ns_measured`, `cpu.measured_from_ns`, then `real.addr, identity, postmaster, monitor_every_ns, valid,
invalid, achieved_ppm, rate_min_ppm, leases_in_window, window_goodput, window_requested,
window_goodput_milli_per_s, late_p50_ns, late_p99_ns, late_max_ns, late_p99_bound_ns,
late_max_bound_ns, lost_events, close_timeouts, write_errors, cancels, stale_results,
rows_dropped, connect_failed, identity_failed, event_hw, server_samples, server_dropped,
server_errors, server_peak, procs_peak, quiesce_backends, pg_cpu_us, lab_cpu_us,
leases_released, pg_cpu_us_per_1000, lab_cpu_us_per_1000, other_cpus_milli, load1_milli_start,
load1_milli_end`. A campaign root holds `manifest.txt`, `env.txt`, `index.csv` (every planned run:
state pending / half-written / finished, result, validity), `capacity.csv` and one directory per
run: `<test>-<arm>-l<load>-r<round>[-sim|-real]` (`cap:80` → `c80`).

## 5. Campaign manifest

Line directives, `#` comments: `campaign`, `test`, `tier real|sim|both`, `reps`, `seed`,
`common <key=value…>`, `real <runner args…>`, `loads <l…>` (`60` = `workload.load`, `cap:80` = %
of the measured capacity), `reps_at <load> <n>` (repetitions at one load; repair round),
`ramp <workload.load…>`, `ramp_arm <keys…>`, `capacity <leases/s>` (instead of a ramp),
`hold probe [samples]` or `hold <quantum> <extra>` and `plain_twin` (simulator hold keys; repair
round), `arm <name> [@reps=N] [@loads=a,b] <keys…>`. Repetitions of an arm at a load: `@reps`,
else `reps_at`, else `reps`. Order: per round, per load (rotated by the round), every arm
(rotated by the round), each tier (`sim`, `real`, then `simplain`). `mglease campaign <m>
phase=plan` prints the order; `phase=rounds` the round count; `phase=probe` / `ramp` /
`round:<r>` run one phase (the script runs each in a fresh container). Exit 1 when any run
violated a check (it is kept), 2 on setup errors.

## 6. For E4: how to plug in

- Register as for the simulator, from `init` in your own files: `RegisterScriptSource("hostile",
  …)`, `RegisterScriptProfile("hostile", "steady", "hostile")`, `RegisterInspector("<name>",
  func(cfg *Config, db DB) (Inspector, error))`. `RunReal` uses the same registries as `RunSim`,
  so `mglease real … workload.profile=hostile oracle.inspector=<name> oracle.rate=1` writes
  `oracle.csv` through `OpenSidecars` exactly as `mglease sim` does.
- In the real tier `db` is a `*leaselab.RealDB`. A second session (the canary reading from
  another backend, a `pg_terminate_backend`) : `leaselab.OpenSession(db.(*RealDB).Options(),
  "mglease-e4", timeout)` → `(*RealSession).Query(sql) (Result, error)`, blocking. Give it its
  own `application_name` (not `mglease`, which R6 and the quiesce check count). A blocking call
  from `Statements`/`Row` stalls the run loop: fine for correctness runs (`oracle.rate=1`, not
  timing), and they will show as lateness.
- Run with `identity=query` so `OracleRow.StartNs` (→ `backend_start_ns`) is the backend's real
  start time (Unix ns) and (pid, start) identifies a backend across pid reuse (R3);
  `backends.csv` has every connection's identity.
- Inspector statements are single Query messages on the lease's connection, before the script
  (E2 §2.4); `Result.Notifications` is filled from `NotificationResponse` as in the simulator.
- Mark correctness runs invalid-agnostic: the validity gate is about timing; `real.valid` = 0 does
  not change `oracle.csv`.
- A killed backend is reported as `db_lost` and recycles the pool (expected; REVIEW-E2-2).
- Functional tests against a container: `scripts/lease-exp.sh test` runs every test whose name
  contains `Real` with `MGLEASE_PG_*` set; name yours `TestReal…` and use `realEnv(t)` in
  `real_test.go`.

## 7. Claims and the tests that prove them

All tests marked (C) need a container (`scripts/lease-exp.sh test`) and skip without one.

| Claim (brief) | Test |
| --- | --- |
| Each policy completes leases on real Postgres (none, reset-always, reset-on-switch cold; remake-always, remake-on-switch fixed:2): goodput = requested, nothing refused, zero lost, zero close timeouts, every file written, first statement and Postgres CPU measured | (C) `internal/lab/leaselab` `TestRealEachPolicyCompletes` |
| Reset really sends `DISCARD ALL`: marker script, oracle on every lease, reset-always → no tenant reads another's marker and resets ok > 0; none → reads happen (the check has teeth) | (C) `TestRealResetSendsDiscardAll` |
| Remake really produces new backends: remake-always, `identity=query`: every connection a distinct (pid, backend_start), as many as connects ok, and no backend serves two leases (`leases.csv` × `backends.csv`) | (C) `TestRealRemakeNewPids` |
| R6: cap holds on the server (20 s runs, monitor 2 ms): remake-always overloaded with K14 = 20, remake-on-switch close-then-open and open-then-close; ≥ 300 closes each | (C) `TestRealR6CapHolds` (and asserted inside every run with `monitor` on) |
| **Canary**: close reported before the backend is gone (close done at once, Terminate 300 ms later) → R6 fails | (C) `TestRealR6CanaryEarlyClose` (planted: `realCanaryEarlyClose`, test-only) |
| Quiesce leaves zero backends of ours on the server | every run: `real.quiesce_backends` = 0 or the run is violated; asserted in `TestRealEachPolicyCompletes` |
| A dead socket is lost, an ordinary close is not; every loss counted in `lab.csv` | (C) `TestRealDeadSocketIsLost`; zero losses in `TestRealEachPolicyCompletes` / R6 |
| Same seeded generator as the simulator | (C) `TestRealSameScheduleAsSim` |
| Campaign interleaving (rotation per round, per-arm limits) | `cmd/mglease` `TestCampaignPlanInterleaves` |
| Manifest errors refused | `TestCampaignManifestErrors` |
| Resumable: finished runs skipped (files untouched), half-written runs (no summary, or lab.csv without run.result) cleared and redone; labels and per-round seeds reach lab.csv; summary and A/A print | `TestCampaignResumes` |
| Ramp → capacity → `cap:` loads | `TestCampaignRampAndCapLoads` |
| Real tier needs an address | `TestCampaignRealNeedsAddr` |
| A two-tier campaign against a container resumes and `compare` prints the R8 table | (C) `TestRealCampaignBothTiers` |
| `-race` clean | `scripts/lease-exp.sh test` runs `go test -race -p 1`; last run on the final code: all 9 container tests and the campaign tests pass, no race reported |

## 8. Smoke campaign (headline numbers, not verdicts)

Run on 2026-11-26, 07:04–07:20 UTC, under the machine lock, with
`e5-smoke/smoke.sh` (run from `/tmp`; waits for a 1-minute load average under 1.5, then
`scripts/lease-exp.sh campaign` for `r6-smoke.txt`, `r8-smoke.txt`, `r7a-smoke.txt`; exit 0, 16 min).
**The host was contended**: another piece's test suite (`leaselab.test`, about one CPU) ran the
whole time; the load average was 7–12 during the runs and `real.other_cpus_milli` 0.9–2.6 CPUs.
Many runs fail the 5 ms lateness gate for that reason (labelled, kept). Every number below is
from this contended smoke: 1–2 runs of 10–20 s per cell. **Headline numbers, not verdicts.**
Run directories (not committed): `/root/runs/lease-e5-smoke/{r6,r8,r7a-smoke,mixreal}`. Kept in
the repository: `lease-exp/e5-smoke/` (the `summary`/`compare` outputs, `capacity.csv`, every
campaign's `env.txt` and `index.csv`, the R6 sample table, the mix check).

**Measured baseline capacity** (ramp, `none`+`cold`, 4 tenants Zipf 5.0, medium leases, cap 20;
`e5-smoke/r7a-smoke-capacity.csv`): **2,353 leases/s** (the highest arrival-window goodput, at
nominal load 70%, i.e. 2,786/s offered). The other overloaded steps gave 1,996–2,201/s; at nominal
50% (2,001/s offered) goodput kept up. Nominal 100% is 4,000/s (cap / 5 ms): the real pool holds a
connection about 8.5 ms per lease, not 5 (see R8 below). `cap:30` = 706/s, `cap:80` = 1,882/s.

**A/A** (`base` against `aa`, identical schedule, medians over valid runs, `e5-smoke/r7a-smoke-summary.md`):

| load | goodput/s | wait p50 | wait p99 | PG CPU /1,000 leases |
| --- | --- | --- | --- | --- |
| cap:30 (17.64) | 0.0% | 33.8% | 48.3% | 6.2% |
| cap:80 (47.05) | 0.0% | 10.9% | 16.4% | 3.8% |

(At cap:30 `base` has one valid run of two.) The noise floor on waits is large at this size;
goodput (an open-loop count) has none below saturation.

**R7 smoke, cap:80 (1,872 goodput/s), medians** (full table: `e5-smoke/r7a-smoke-summary.md`):

| arm | wait p50 | wait p99 | refused | connects/s | PG CPU ms /1,000 | lab CPU ms /1,000 | first stmt fresh p50 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| base (none+cold) | 140 µs | 73 ms | 0 | 2.0 | 527 | 249 | 3.3 ms |
| ra (reset-always) | 187 µs | 87 ms | 0 | 2.0 | 649 | 300 | 3.7 ms |
| ros (reset-on-switch) | 152 µs | 77 ms | 0 | 2.0 | 526 | 245 | 4.0 ms |
| rmsf (remake-on-switch fixed:4) | 4.4 ms | 88 ms | 0 | 6.8 | 548 | 271 | 3.9 ms |
| rmsf0 (patience 0) | 6.5 ms | 82 ms | 0 | 61.0 | 1,016 | 408 | 4.4 ms |
| rmsd (dynamic) | 0.69 ms | 102 ms | 0 | 3.2 | 529 | 266 | 4.5 ms |
| rmsd0 | 5.7 ms | 82 ms | 47 | 44.7 | 945 | 400 | 4.1 ms |
| rmsdp (dynamic + prime) | 0.73 ms | 106 ms | 0 | 3.1 | 569 | 280 | 3.4 ms |
| rmsdp0 | 5.3 ms | 97 ms | 0 | 42.1 | 926 | 390 | 3.1 ms |
| rma (remake-always, control) | 8.9 s | 9.6 s | 33,417 | 203 | 17,818 | 7,677 | 4.3 ms |
| rmaw (remake-always + fixed:4) | 9.3 s | 10.9 s | 16,783 (1 run) | 205 | 19,132 | 8,250 | 4.8 ms |

- **Remake-always ceiling here: 103–116 goodput leases/s** at both loads (`rma` 102.6 / 115.8,
  `rmaw` 104.0 / 110.2): the warm stock did not move it (ruling item 1's "one warm run"). It costs
  Postgres 17.8 ms of CPU per lease (calibration: 13.7–15.6 ms per connect-and-close).
- **Switch rate of mix A on real Postgres was below the budget**: `rmsf` made 1.9 and 6.8
  connects/s at cap:30 / cap:80 (patience 0: 9.8 / 61). The mix was picked in the simulator at
  nominal loads 70–80, but the real capacity puts cap:80 at nominal 47. A check on real Postgres
  (`e5-smoke/mix-check-summary.md`, `remake-on-switch` fixed:4, one 10 s run each, capacity
  2,352/s): 4 tenants Zipf 5.0 → 7.0 / 24.2 connects/s at cap:80 / cap:95; Zipf 3.0 → 11 / 26;
  Zipf 2.0 → 29 / 21; **6 tenants Zipf 3.0 → 41 / 36**. `r7a.txt` now uses 6 tenants, Zipf 3.0;
  `r7b.txt` (10 tenants, Zipf 1.1) is unchanged and above the budget.
- The waits' p99 of tens of ms even at cap:30 is the cold start (my reading of the counts, not
  measured directly): in `base` at cap:30, 136 of 7,013 leases waited (16 for a connect, 120 for
  a slot), about the number that arrive (≈ 700/s) while the cold pool opens its 20 connections 4
  at a time at a connect p50 of 46 ms here (≈ 0.23 s → ≈ 160). In 35 s runs it is diluted, not
  removed; E7 may want it said in the report.
- First statement on a fresh backend: 3.1–4.8 ms p50 here (calibration: pk_lookup 1.75 ms); the
  prime (`rmsdp`) lowered it against `rmsd` (3.4 against 4.5 ms at cap:80; 2.4 against 5.9 ms at
  cap:30) — two runs each, contended.

**R6** (`e5-smoke/r6-summary.md`, `e5-smoke/r6-server.csv`; 20 s each, monitor 2 ms):

| run | samples | pg_stat_activity peak (cap 20) | procs peak | samples with procs > cap | closes | sample round trip p50 |
| --- | --- | --- | --- | --- | --- | --- |
| remake-always, K14 = 4, load 4 | 5,259 | 5 | 8 | 0 | 2,644 | 4.1 ms |
| remake-always, K14 = 20, load 4 | 1,898 | 13 | 20 | 0 | 3,154 | 10.7 ms |
| remake-on-switch fixed:4, 10 tenants Zipf 1.1, load 60, close-then-open | 4,094 | **20** | 21 | 2 | 250 | 2.6 ms |
| the same, open-then-close | 4,792 | **20** | 21 | 1 | 216 | 2.2 ms |
| remake-on-switch fixed:4, 100 uniform tenants, load 4, close-then-open | 3,180 | **20** | 22 | 20 | 1,889 | 4.2 ms |
| the same, open-then-close | 3,268 | **20** | 21 | 9 | 1,941 | 4.1 ms |

The assertion held in every run (peak = cap, never above, in both orders). The process count
(fork to reap, not asserted) went 1–2 above the cap in 1–20 samples of a few thousand: processes
that have left shared memory and closed the connection but are not yet reaped, or a backend still
starting after the pool gave up on its connect — section 10. Samples were 2–11 ms apart, not 2 ms
(each sample's round trip on the contended host). Five of six runs failed the lateness gate
(p99 5.0–12.8 ms); R6's assertion does not depend on it.

**R8** (`e5-smoke/r8-compare.md`; one 20 s run per cell, both tiers, same seed):

- **S3 (miss rate against fixed W)**: warm hit rate sim / real: W = 1 53.5% / 45.1%; W = 2 87.0% /
  77.9%; W = 4 98.4% / 95.8% — **inside the 10-point band**, real 3–9 points lower. Wait p99 real
  2–6 × the simulator's (outside the band).
- **S6 (wait and goodput against load)**: **outside the band**. `none` at nominal 30 / 60 / 90:
  real wait p99 31 ms / 115 ms / 419 ms against ≤ 1 µs / 1.4 ms / 40 ms; at 90 the real pool
  saturated (goodput 76% of requested, 17,172 refused) where the simulator served everything.
  `reset-always` saturated on real already at 60 (93.8%, 2,978 refused; 54.8% at 90). 21
  comparisons outside the band in total.
- **Why, as far as measured.** At nominal 30 the lease hold (intended start → release) p50 is
  6.4 ms real against 3.7 ms simulated. A probe on a fresh container (07:21 UTC, load 3,
  300 statements each, p50 round trip): `SELECT 1` 0.11 ms; `pg_sleep(0.000001)` 1.48 ms;
  `pg_sleep(0.0035)` 4.79 ms; `pg_sleep(0.005)` 5.86 ms; `pg_sleep(0.0051)` 6.82 ms. **Postgres's
  pg_sleep holds about 1.2–1.4 ms longer than asked** (it sleeps whole milliseconds, plus wake-up
  delay), about +25% on the 5 ms mean lease; the simulator's fake sleeps exactly. On top of that
  the contended host made every statement slower than calibration (first statement on a reused
  backend 0.58 ms against 0.11 ms simulated; reset p50 0.33 ms against 0.18 ms; connects 36–46 ms
  against the model's 18 ms). The rest of the gap is not decomposed (unsure: wake-up and
  scheduling delay of the backends and of the lab's loop on a busy host).
- **This is a finding, not tuned away.** Options for the coordinator (none taken): (a) keep
  loads relative to measured capacity in each tier (R7 does: `cap:` loads); (b) charge the
  measured `pg_sleep` overhead in the simulator's cost model (an E2/E3 change); (c) a real-tier
  script that asks pg_sleep for less than drawn (changes what R8 compares). The S3 warm-stock
  numbers agree; the capacity and wait numbers of the simulator are optimistic against this
  host.

**R9** (Postgres CPU per 1,000 leases, from the container's cgroup; table above and
`e5-smoke/r7a-smoke-summary.md`): at cap:80 `none` 527 ms, `reset-on-switch` 526,
`reset-always` 649 (+23%), `remake-on-switch` 529–569 with patience auto and 926–1,016 with
patience 0, `remake-always` 17,818 (17.8 ms per lease). The lab's own CPU per 1,000 leases is
beside it (249 for `none`, 7,677 for `remake-always`: TLS and SCRAM on the client).

**Time per run**: R7 smoke round 0 ran 24 runs in 281 s and round 1 22 runs in 243 s: **11.0–11.7 s
of wall time per 10 s run**, i.e. about 1–1.7 s of setup, drain, quiesce and files per run
(overloaded `remake-always` runs drain up to 1,000 waiters at about 105/s, up to 10 s more). A fresh
container takes 4–5 s per phase; the six 10 s ramp steps took 64 s. R6 (6 × 20 s): 140 s. R8 (9 real
× 20 s plus their 9 simulated twins): 204 s.

## 9. The full campaign (E7): commands and machine time

**Superseded by the repair round** (section 12 item 6): the E7 campaign is the sized-down one
of REVIEW-E5-1, run by one command; the build round's text follows for the record. The manifests
it names (`r6.txt`, `r8.txt`, `r7a.txt`, `r7b.txt`) are now `e7-1-r6.txt` … `e7-4-r7b.txt`, changed.

Each campaign takes the machine lock itself; run them detached, one after another (the
lock serialises them anyway). Run directories go under `/root/runs/lease-r/` (not the repository).

```sh
cd /root/Software/mini-gateway-work/lease-e5
M=agent_work/chunks/c05-classifier/lease-exp/manifests
scripts/lease-exp.sh detach lease-r6  campaign $M/r6.txt  out=/root/runs/lease-r/r6
scripts/lease-exp.sh detach lease-r8  campaign $M/r8.txt  out=/root/runs/lease-r/r8
scripts/lease-exp.sh detach lease-r7a campaign $M/r7a.txt out=/root/runs/lease-r/r7a
scripts/lease-exp.sh detach lease-r7b campaign $M/r7b.txt out=/root/runs/lease-r/r7b
# while running: tail -f /root/runs/lease-r7a.log; finished when /root/runs/lease-r7a.exit exists
# (0 = every run ok, 1 = some run violated a check: kept and labelled, 2 = setup error).
# Resume after any stop: the same command (finished runs are skipped, half-written ones redone).
go run ./cmd/mglease summary /root/runs/lease-r/r7a      # R7 + R9 per arm and load, A/A
go run ./cmd/mglease summary /root/runs/lease-r/r6       # R6 (also asserted in every run)
go run ./cmd/mglease compare /root/runs/lease-r/r8       # R8 against the band
```

Expected machine time, from the smoke's 1–1.7 s per run overhead (plus up to 10 s for each
overloaded `remake-always` run) and 4–5 s per container:

| Campaign | Runs | Expected |
| --- | --- | --- |
| R6 (`r6.txt`: 6 arms × 3 repetitions × 20 s) | 18 | about 7 min |
| R8 (`r8.txt`: 9 cells × 3 repetitions × 30 s, both tiers) | 54 (27 real) | about 16 min |
| R7 mix A (`r7a.txt`: ramp 6 × 35 s; 12 arms × 4 loads × 10 repetitions × 35 s, `rmaw` once per load) | 6 + 444 | about 4.7 h |
| R7 mix B (`r7b.txt`, the same) | 6 + 444 | about 4.7 h |
| Everything | | about 9.7 h |

R7 dominates. Each halving of repetitions or of the run length roughly halves it (5 repetitions:
2.4 h per mix); the four patience-0 arms and the two prime arms are a third of the arms. These
are the coordinator's choices (manifest lines `reps`, `common … run.duration=…`, `arm …`).
Before the full campaign: run on a quiet host (the smoke was contended; see section 8), and
consider whether R8's `pg_sleep` finding changes what is run.

## 10. Limits

- One shared 4-vCPU VM; Postgres, the lab and other agents' work share it. During this piece's
  development another piece's simulator sweep held 2.5–3.5 CPUs for long stretches; numbers taken
  then (section 8 says which) are contended. `real.other_cpus_milli` and `env.txt` record it.
- Lateness p99 1–3 ms even with the waker (descheduling); the gate's 5 ms bound is a choice.
- `pg_sleep` holds whole milliseconds (+0.5 ms mean on medium leases) plus a wake-up delay: real
  hold times are longer than the drawn ones; the simulator matches them only with
  `db.hold_quantum` / `db.hold_extra` set (R8 sets them from the probe).
- The lab's CPU (TLS, SCRAM key derivation 3.1 ms per connect, the loop) competes with Postgres
  for the same cores; the remake ceiling measured here is the ceiling *with* this client.
- R6 asserts on samples (monitor 2 ms, in practice back to back, 2–11 ms apart in the smoke): an excursion shorter
  than a sample can be missed. `procs` (process lifetime, fork to reap) is not asserted; it can
  exceed the cap by processes that have released their slot and ended the connection but are not
  yet reaped, and by backends still authenticating after a connect that the pool gave up on
  (connect timeout) — the shell closes its socket, the core frees the slot, and the backend exits
  on its own shortly after.
- A connect that fails after the server forked a backend is reported failed when the client gives
  up, before that backend has necessarily exited (the same case as above).
- Close timeouts (5 s without EOF) drop the socket and report close done: counted and invalidating,
  never silent.
- `identity=query` costs one round trip per connect and warms the catalog cache (off by default).
- The real tier does not run the `faulty` profile's fault schedule (restart, stall, refuse, slow):
  those are simulator faults; E4 kills backends itself.

## 11. Commands

```sh
export PATH=$PATH:/usr/local/go/bin
cd /root/Software/mini-gateway-work/lease-e5
go test ./internal/lab/... ./cmd/mglease/...                 # everything that needs no container
scripts/lease-exp.sh test                                     # the (C) tests: fresh container, -race, -p 1, no lock
scripts/lease-exp.sh test -v -run TestRealR6                  # one of them
scripts/lease-exp.sh run release.policy=reset-on-switch warm.mode=cold run.duration=10s monitor=100ms out=/root/runs/x/r1
go run ./cmd/mglease campaign agent_work/chunks/c05-classifier/lease-exp/manifests/r7a-smoke.txt phase=plan
scripts/lease-exp.sh campaign agent_work/chunks/c05-classifier/lease-exp/manifests/r6-smoke.txt out=/root/runs/lease-e5-smoke/r6
go run ./cmd/mglease summary /root/runs/lease-e5-smoke/r7a-smoke
go run ./cmd/mglease compare /root/runs/lease-e5-smoke/r8
```

The full-campaign commands are in section 9.

## 12. Repair round (REVIEW-E5-1, brief `/root/briefs/zgateway-lease-e5-repair.md`)

The one repair round: the brief's items 1–6 (review findings 1–5 and the campaign). Sections 1–5,
9 and 10 above are corrected where they had become untrue. Commits (after `b16edcf`):

| Commit | Change |
| --- | --- |
| `3d713a7` | item 1: `server.csv` header `time,count,procs,query_ns` |
| `6216316` | items 2, 3, 5 (shared files): `obs.warmup`, `db.hold_quantum` / `db.hold_extra`, E6's `lab.csv` keys, the warm-up window and CPU point. One commit because the three are interleaved in `config.go` and `realrun.go` |
| `49fec9d` | not an item: a data race the suite showed once under `-race` (`RunReal` read `real.event_hw` / `real.rows_dropped` plainly while socket readers store them atomically); needed for the round's `-race`-clean finish |
| `7ce3641` | container tests of items 1–3 |
| `462a4c3` | items 4, 5, 6 in the runner: capacity rule, `reps_at`, `hold probe` / `hold` / `plain_twin`, `phase=probe`, `mglease holdprobe`, `compare` for both simulator twins |
| `16016ba` | item 6: the E7 manifests and `lease-exp.sh e7` |
| (next) | the rehearsal's evidence (`e5-rehearsal/`, `manifests/rehearsal/`) and this section |

**E2 files and functions touched (for the merge with E3)** — every change is an insertion, no
existing line's meaning changed, and with the new keys at their defaults (all 0) the simulator
does exactly what it did:

- `config.go`: `Config` fields `WarmupNs`, `HoldQuantum`, `HoldExtra`; three `keys` entries —
  `obs.warmup` (after `obs.interval`), `db.hold_quantum`, `db.hold_extra` (after `db.close_lag`);
  `Validate`: two cases (warm-up in [0, run.duration), hold values ≥ 0). `lab.csv` therefore has
  three more config rows.
- `engine.go`: `Request` (counts warm-up requests), `act` / `ActRefuse` (warm-up refusals),
  `lend` (`lease_wait_ns`, `switch_ready_ns` only for measured leases), `release`
  (`lease_total_ns` only for measured leases; warm-up goodput), `ExecDone` / `opScript`
  (`first_stmt_ns` only for measured leases); new method `measured`.
- `metrics.go`: `Metrics` fields `WarmupRequested`, `WarmupRefused`, `WarmupGoodput` (no new
  metric declared: `metrics.csv` is unchanged).
- `run.go`: `RunFacts.Warmup`; `LabRows` writes `warmup.requested`, `warmup.refused`,
  `warmup.goodput` after `requests.achieved`.
- `simdb.go`: `Exec` (a `pg_sleep` statement is sent after `HeldNs(hold) − hold`); new functions
  `HeldNs`, `sleepNs`.

E5's own files: `realmon.go` (`ServerHeader`), `realrun.go`, `realsum.go`, `cmd/mglease/
realcampaign.go`, `realreport.go`, new `realprobe.go`, `scripts/lease-exp.sh`; tests
`e5repair_test.go`, `realrepair_test.go`, `cmd/mglease/realrepair_test.go`, and two edits to
`realcampaign_test.go` (the both-tiers test now also runs the probe and the plain twin).

### Item 1 — `server.csv` in E6's format

Header `time,count,procs,query_ns`: `time` = the shell's clock in ns at the sample, `count` = the
`pg_stat_activity` backends of ours (what E6 reads; the old `backends`), `procs` and `query_ns`
as before (E6 ignores extra columns, `union_by_name`). Nothing of mine read the old names
(`mglease summary` reads `real.server_peak` from `summary.csv`; `e5-smoke/r6-server.csv` is a
per-run summary table, not a `server.csv`, and is unchanged).
**Tests:** `TestServerCSVHeader`; (C) `TestRealReportReadable` — three real runs (`base`, `aa`,
and `rmsf` = remake-on-switch with the monitor on, so a real-tier remake run with `server.csv`)
through `scripts/lease-report.sh`: exit 0, table 0 empty. **Proof on the rehearsal** (below):
table 0 is empty for R8, R7a and R7b; R6's only row is `invalid | runner | run.valid 0` — a run
that failed the lateness gate on the busy host, labelled as designed, not a `broken` file.

### Item 2 — `cpu.pg_ns`, `cpu.lab_ns`, `run.valid`

`lab.csv` (and `summary.csv`) now start the real rows with `run.valid` (`1`/`0`), `run.invalid`
(the reasons, `;`-separated; empty when valid), `cpu.pg_ns` (container cgroup `usage_usec` × 1000,
run start → end; absent when the cgroup was not read) and `cpu.lab_ns` (the lab's rusage, same
span), plus `cpu.pg_ns_measured` / `cpu.lab_ns_measured` (from the warm-up reading to the end) and
`cpu.measured_from_ns`. **Decision:** `cpu.pg_ns` covers the whole run because E6's table 10
divides it by `leases_requested`, which is whole (item 3); the measured twins are for anyone who
divides by the measured leases. The `real.*` rows stay (`summary`, `compare`, `index.csv` read
`real.valid` / `real.invalid` / `real.pg_cpu_us_per_1000`). The report's defaults (`invalid_key`,
`cpu_pg_key`, `cpu_lab_key`) now match. **Tests:** (C) `TestRealReportReadable` (table 10: a
measured PG CPU per 1,000 leases and an empty `cpu_note` for every arm; table 9: no row blocked by
a broken run); (C) `TestRealWarmup` (measured ≤ whole, both > 0). **Proof:** rehearsal tables 0,
9 and 10 (below; `e5-rehearsal/*-report-10-switch-budget.md`: every arm measured, e.g. R7a
`base` 0.499 s and `rmsf` 0.690 s of Postgres CPU per 1,000 leases, R6 `rma` 20.1 s).

### Item 3 — `obs.warmup`

Key `obs.warmup=<duration>` (default `0s` = the old behaviour), both tiers, in `lab.csv` as a
config row. A lease whose **intended** start is before it runs normally and is counted in every
counter, but is left out of `lease_wait_ns`, `switch_ready_ns`, `first_stmt_ns` and
`lease_total_ns` (`reset_ns`, `connect_ns`, `close_lag_ns` are per-connection costs and stay
whole). **Decision — two phases, not exclusion from the counters:** E6's table 0 checks
`requests.achieved` = `leases_requested` and the conservation laws, and table 4 divides
`leases_goodput` by `requests.achieved` (the knee test); dropping the warm-up from the goodput or
request counter alone would make every load look past the knee, dropping it from both breaks table
0, and E6's SQL is not this piece's. So the counters stay whole and the warm-up phase is reported
beside them: `lab.csv` `warmup.requested`, `warmup.refused`, `warmup.goodput`; `summary.csv`
`warmup_ns`, `requested_measured`, `refused_measured`, `goodput_measured`,
`goodput_measured_milli_per_s` (measured = whole − warm-up). Consequence, stated for E8: E6's
goodput/s and table 9's goodput line include the warm-up's leases (an open-loop count: a cold
start delays leases but refuses none unless the wait list overflows, which `warmup.refused`
shows); every wait/switch/first-statement/total quantile excludes them. E5's own **window
goodput** (`real.window_goodput*`, the ramp's capacity) now counts completions between the end of
the warm-up and the end of the arrivals over that window. **CPU (R9):** `cpu.csv` gets a `warmup`
point when the loop first passes the warm-up; `cpu.*_measured` run from it to the end (they also
hold the few warm-up leases still in flight at that moment); `cpu.pg_ns` and
`real.pg_cpu_us_per_1000` stay whole-run (item 2). **Tests:** `TestWarmupSim` (same seed with and
without a 1 s warm-up: every counter equal; with it, the four histograms hold exactly the leases
of `leases.csv` with intended start ≥ 1 s; `warmup.*` rows match; two runs byte-identical — U5;
conservation is checked by every run); (C) `TestRealWarmup` (the same histogram identity against
`leases.csv` on real Postgres, the `cpu.csv` point, measured CPU). The existing U5 and
conservation tests pass unchanged. All E7 manifests set `obs.warmup=5s`.

### Item 4 — capacity from the ramp

**Rule** (`capacityFrom`, `cmd/mglease/realcampaign.go`): a ramp step is **saturated** when a
lease whose intended start is after the warm-up was refused (`refused_measured` > 0: the wait list,
`pool.wait_cap` = 1,000, overflowed in the measured phase, so the pool did not keep up and the
window goodput is what it sustains; refusals during the cold start do not count). Capacity = the
**median window goodput of the valid saturated steps when at least three are valid, else of every
saturated step**; an even count takes the mean of the middle two; no saturated step is an error
("extend the ramp or pin `capacity <n>`"). `capacity.csv` lists every step (`valid`,
`refused`, `refused_measured`, `saturated`, `used`) and the rule as applied. `capacity <n>` in a
manifest still pins it (no ramp run). In the simulator tier the step value is
`goodput_measured_milli_per_s`. Applied to the smoke's ramp (`e5-smoke/r7a-smoke-capacity.csv`,
no warm-up then): five saturated steps, three valid (70, 100, 130) → median 2,200.8/s instead of the
2,352.9/s maximum (6.5% lower). **Tests:** `TestCapacityRule` (valid median; fewer than three valid
→ all saturated; even count; warm-up-only refusals not saturated; none → error),
`TestCampaignRampAndCapLoads` (end to end in the simulator).

### Item 5 — hold fidelity for R8

Simulator keys `db.hold_quantum` and `db.hold_extra` (default `0s`, the old behaviour): a simulated
`pg_sleep(x)` holds `HeldNs(x) = ceil(x / q) × q + extra` (`q` = 0: `x + extra`); they have no
effect in the real tier (real `pg_sleep` is real). **Probe** (`mglease holdprobe`, `realprobe.go`;
independent of every lab run): one side session (`mglease-probe`), 50 warm-up `SELECT 1`, then
`n` pairs of `SELECT 1` and `SELECT pg_sleep(<drawn>)`, the drawn times being the first `n` lease
times of the workload's own generator (the manifest's `common` keys: `workload.lease`, seed). The
measured hold of a sample = its `pg_sleep` round trip − the median `SELECT 1` round trip (the
network and statement overhead the simulator models already). For each candidate quantum (0 and
1 ms — Postgres's `pg_sleep` waits `ceil` of the remaining milliseconds) the residual = measured −
`HeldNs(drawn, q, 0)`; the quantum is the candidate with the smaller interquartile range of
residuals, the extra their median (≥ 0, whole µs). Output: `hold.csv` (both values, the medians and
IQRs of both candidates, `SELECT 1` p50) and `holdprobe-samples.csv`. In a campaign, `hold probe
[n]` runs it as its own phase in a fresh container before anything else (resumable: an existing
`hold.csv` is kept), and every simulator run gets the two keys from it (`lab.csv` records them,
`compare` labels each row "sim hold <q> + <x>"); `plain_twin` runs every simulator twin again
without them under `run.test=<test>.plain` (its own comparison group in E6; `compare` labels it
"sim plain"). E6's table 8 pairs the real runs with the held twin. Nothing else was tuned.
**R8 loads** moved below the real knee: S6 at nominal 15, 30, 45 (was 30, 60, 90). **Tests:**
`TestHoldKeysSim` (`HeldNs` cases; `SleepSQL` round trip; same seed with `1ms`/`800us`: the mean
lend-to-release time grows by the expected `HeldNs − x`, measured 1.3218 ms against 1.3198 ms
expected over 1,566 leases), `TestFitHold` (synthetic whole-ms + 0.8 ms → `1ms`/`800us`; exact +
0.3 ms → `0`/`300us`), `TestCampaignHoldAndPlainTwin`, (C) `TestRealHoldProbe`, (C)
`TestRealCampaignBothTiers` (probe phase, held and plain twins, resume skips the probe,
`compare` prints both). **Values seen** (busy host, not evidence): the test probe gave quantum 1 ms,
extra 291 µs (IQR 0.17 ms against 0.65 ms for q = 0); the rehearsal's probe 1 ms / 269 µs
(IQR 0.29 against 0.61 ms; `SELECT 1` p50 0.30 ms). The smoke's figures (section 8) imply extras of
0.4–0.7 ms; E7's probe will measure its own.

### Item 6 — per-load repetitions, the E7 campaign, one command

Manifest line `reps_at <load> <n>` (an arm's `@reps` still wins). The campaign of REVIEW-E5-1's
"Proposed campaign", in `lease-exp/manifests/` (the old `r6.txt`, `r8.txt`, `r7a.txt`, `r7b.txt`
renamed and changed; `*-smoke.txt` stay as run):

| Manifest | Content | Runs (plan) | Expected |
| --- | --- | --- | --- |
| `e7-1-r6.txt` | R6 as before + `obs.warmup=5s`: 6 arms × 3 reps × 20 s | 18 | ≈ 7 min |
| `e7-2-r8.txt` | hold probe (400 samples), S3 W 1/2/4 at load 1, S6 `none`/`reset-always` at 15/30/45; 3 reps × (30 s + 5 s warm-up); both tiers + plain twin | 27 real + 54 sim | ≈ 20 min |
| `e7-3-r7a.txt` | mix A: ramp (6 × 35 s); cap:80 all 12 arms × 10 reps; cap:30/60/95 9 arms (`rmsd0`, `rmsdp0`, `rmaw` only at cap:80) × 3 reps; 30 s + 5 s warm-up | 6 + 201 | ≈ 2 h 10 min |
| `e7-4-r7b.txt` | mix B: ramp; cap:80 only; `base aa ra ros rmsf rmsf0 rmsd rma` × 6 reps; 30 s + 5 s | 6 + 48 | ≈ 35 min |

About 3 h 10 min in all (≈ 37 s of wall time per 35 s run from the smoke's overhead, 4–5 s per
container, up to 10 s of drain per overloaded `rma` run). **The command** (one hold of the machine
lock for everything; log and exit file; resumable — run the same line again after any stop:
finished runs and an existing `hold.csv` / ramp are skipped, half-written runs redone):

```sh
cd /root/Software/mini-gateway-work/lease-e5
scripts/lease-exp.sh detach lease-e7 e7 /root/runs/lease-r
# alive: pgrep -f 'lease-exp.sh e7'; log: /root/runs/lease-e7.log; done when /root/runs/lease-e7.exit
# exists: 0 every run ok, 1 some run violated a check (kept, labelled), 2 setup error (rerun the line)
# campaign roots: /root/runs/lease-r/{1-r6,2-r8,3-r7a,4-r7b}
```

Run it on a quiet host (REVIEW-E5-1: no builder running; `real.other_cpus_milli` under about 300).

**Report parameters** (one report per campaign root; `invalid_key`, `cpu_pg_key`, `cpu_lab_key`
at their defaults; `verdict_load` = the `workload.load` that cap:80 became):

```sh
R=/root/runs/lease-r; P=/root/runs/lease-r-report
scripts/lease-report.sh $R/1-r6 $P/r6 server_extra=0
scripts/lease-report.sh $R/2-r8 $P/r8 server_extra=0 sim_real_band_pct=100
for m in 3-r7a 4-r7b; do
  vl=$(awk -F, '$4=="cap:80"{print $6; exit}' $R/$m/index.csv)
  scripts/lease-report.sh $R/$m $P/${m#*-} baseline_arm="'base'" aa_arm="'aa'" server_extra=0 \
    verdict_load=$vl min_valid_reps=5 sim_real_band_pct=100
done
go run ./cmd/mglease compare $R/2-r8     # R8 table of record (both simulator twins, the compound band)
go run ./cmd/mglease summary $R/3-r7a $R/4-r7b
```

The ramp's runs carry `run.test=ramp`, `run.arm=ramp`; table 9 lists them as an arm with
"inconclusive" rows — ignore them.

**Rehearsal** (2026-11-26 07:55:41–07:58:43 UTC; `manifests/rehearsal/e7-*.txt`: 2 arms × 1 rep ×
10 s per phase, `obs.warmup=2s`, a 2-step ramp, a 100-sample probe). The lock was free, so it ran
through the real command under it: `scripts/lease-exp.sh detach lease-e5-rehearsal e7
/root/runs/lease-e5-rehearsal agent_work/chunks/c05-classifier/lease-exp/manifests/rehearsal` —
exit 0, 3 min. The host was busy (load average 4.4–4.8, other agents): **numbers are not evidence**.
Run directories `/root/runs/lease-e5-rehearsal/` (not committed); kept: `lease-exp/e5-rehearsal/`
(index, env, capacity, hold, compare and report tables 0, 9, 10 per campaign, table 8 for R8).
`lease-report.sh` exit codes: R6 1 (one `invalid` row), R8 0, R7a 0, R7b 0.

Table 0:

| campaign | rows |
| --- | --- |
| R6 | `invalid` · `r6-rmsf-l60-r0` · runner · `run.valid 0` (it failed `late_p99` on the busy host; labelled, kept) |
| R8, R7a, R7b | none |

Table 9, R7b (the A/A pair at cap:80 = `workload.load` 51.26; abridged):

| arm | threshold | measured | verdict | note |
| --- | --- | --- | --- | --- |
| aa | isolation | 0 | inconclusive | no oracle row checked |
| aa | goodput | 1.0 | pass | ratio 1.0; d 0.0/s against 2 × floor 0.0/s; 2,054.8/s each; valid runs 1 of 1 |
| aa | p99_wait | 5.59 | inconclusive | arm p99 29.0 ms, 2 × baseline 10.4 ms, \|difference\| 18.6 ms against 2 × floor 47.6 ms |
| base | goodput / p99_wait | 1.0 / 1.0 | pass / inconclusive | (as above) |

Table 9, R7a (`base`, `rmsf` at `workload.load` 51.92; no A/A arm in the rehearsal): every
goodput and p99 row `inconclusive` with "no A/A arm at this load" (`rmsf` p99 ratio 11.9); no row
says "broken". Ramp: R7a one saturated step of two (130: 20,565 measured refusals) → capacity
2,596/s; R7b 2,563/s. R8 compare: 10 comparisons outside the band over both twins (e.g. S6 `none`
at 15: wait p99 real 2.70 ms against ≤ 1 µs simulated; goodput 100% in both).

### Deviations and limits of the round

- Item 3 keeps the counters whole and reports the warm-up as a second phase (reason above); E6's
  goodput lines therefore include warm-up leases.
- `cpu.pg_ns` is the whole run (E6's divisor is whole); the measured-phase CPU is in
  `cpu.pg_ns_measured`.
- The quantum is chosen from two candidates (0, 1 ms), not fitted freely; the probe's `SELECT 1`
  subtraction removes the real statement overhead of `pg_sleep` too, where the simulator charges a
  `pg_sleep` statement only the network (tens of µs at most).
- One commit (`6216316`) carries items 2, 3 and 5's shared-file changes; `49fec9d` fixes a race
  outside the items.
- Tests: 10 new top-level tests (no container: `TestWarmupSim`, `TestHoldKeysSim`, `TestServerCSVHeader`,
  `TestCapacityRule`, `TestCampaignRepsPerLoad`, `TestCampaignHoldAndPlainTwin`, `TestFitHold`;
  container: `TestRealWarmup`, `TestRealReportReadable`, `TestRealHoldProbe`, and the extended
  `TestRealCampaignBothTiers`). Size of the round: Go code +615 / −53 lines (with the script),
  tests +471 / −4.
- Commands: `go test ./internal/lab/... ./cmd/mglease/...` (no container) and
  `scripts/lease-exp.sh test` (container, `-race`): both green on the final code; no planted-bug
  canary was added (the piece's canary, `TestRealR6CanaryEarlyClose`, still bites); each new
  assertion compares against an independent count (`leases.csv`, the generator, synthetic data).
