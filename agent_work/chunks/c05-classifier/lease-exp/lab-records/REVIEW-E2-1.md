# Review 1 of piece E2 (lab driver) — Fable, 2026-11-26

Scope: commits `6f56fa0..ebf7160` on `exp/lease-lab` (`internal/lab/leaselab`, `cmd/mglease`,
`test/analysis/lease/checks/`, the `pump` early exit in `leasepool`), against
`/root/briefs/zgateway-lease-e2-lab-driver.md`, the lab rules and plan §5–§9.

**Verdict: FAIL — narrowly.** The instrument is sound: I found nothing that corrupts a number.
Two small gaps in the seams must be closed before E3/E5/E6 fork from this tip, because the
pieces that need them (E4, E5) are forbidden to edit E2's files (findings 1, 2). A third
finding (C1) is not E2's: it is a behaviour of the E1 pool core that this review's runs exposed,
it decides how `remake-on-switch` will look in every later table, and the coordinator must rule
on it before E3 runs.

## What I verified, and how

- `git diff 5887f20..HEAD --stat`: 24 files, all under `internal/lab/`, `cmd/mglease/`,
  `test/analysis/lease/checks/`, plus the notes; nothing frozen touched. `pool.go` +5 lines (the
  early exit). `gofmt -l .` empty; `go vet` clean; `go test ./internal/lab/... ./cmd/mglease/...`
  green (54 s + 8 s + 3 s); `internal/obs/simrun` green (128 s).
- Read in full: `engine.go`, `simdb.go`, `cpumodel.go` (model part), NOTES-E2.md; in part
  `run.go`, the conservation SQL, `u5_test.go`.
- Ran 30 simulated runs of my own with a built `mglease` (20 s each, cap 20) and summarised them
  with my own DuckDB queries. First-principles checks, medium leases (5 ms) unless marked:

  | Check | Expected | Observed |
  | --- | --- | --- |
  | Offered load at 80% | 0.8 × 20 / 5 ms = 3,200/s | 63,867 in 20 s = 3,193/s |
  | Baseline capacity (120% offered) | 20 / (5 ms + 0.088 ms round trip) = 3,931/s | 3,967/s incl. drain |
  | `reset-always` capacity factor | (S)/(S + r), r = 177 µs → 0.966 | 76,564 / 79,344 = 0.965 |
  | The same, short leases (0.5 ms) | 20 / (0.588 + 0.177 ms) = 26,140/s | 25,894/s (80% and 95% offered alike) |
  | `remake-always` cold ceiling | model plateau 174/s | 4,464 leases over 20 s + 5 s drain = 178/s |
  | Open loop under overload | arrivals unchanged, refusals grow | requested identical across policies per load; waits pin at wait cap / rate |
  | Cap | open + connecting ≤ 20, fake ≤ 20 | high-water 20 / 20 everywhere |
  | `faulty` | restart → one `db_lost`, refill, goodput returns | 1 loss at 6 s, 32 refused connects, 20 open again within 2 s, rate back to 3,200/s; quiesce clean |
  | 10,000 tenants | runs in seconds | 76,000 leases in 4 s wall; overload with full wait list 2 s |

- Determinism: no float, wall clock, goroutine or global random source in the non-test files
  (grep); the two engine maps and the two SimDB maps are lookup-only, the one iteration
  (`OpenConns`) is sorted. U5 compares every file of the directory byte for byte over 8
  configurations × 2 runs, plus a third seed that must differ. Real.
- Metrics: every plan §9 metric is in the flushed files with the stated labels (60 metric
  names under `pool`; histograms `lease_wait_ns`×outcome, `switch_ready_ns`×mode, `reset_ns`,
  `connect_ns`, `close_lag_ns`, `first_stmt_ns`×mode, `lease_total_ns`). Bucket width about
  0.7%. Periodic flush gives the time series (checked warm target against time on `step`).
  Conservation SQL includes `pending` and `closes_done`, runs on every flush.
- Connect model: derived by code from `calibration.json` (grid minimax over 13 cells), FIFO on
  3 cores, 13.75 ms work, 4.5 ms floor, 3.5 ms close work in the same queue. Honest: the notes
  print every cell and name the three it gets worst. **Decision:** acceptable as is. Its
  sustained ceiling is 174/s against the measured 195/s (−11%), and lease work does not
  compete with connects for CPU. Both must be stated in REPORT.md, and the switch-rate budget
  must be given against both numbers (measured 195/s; simulated 174/s).

## Findings

**1. (major, seam) The client-gone release cannot be reached.** `engine.go:370` always posts
`EvRelease{Lease, Status}`; nothing sets `ClientGone`, and `Script` has no way to ask for it.
Plan R5 ("with the client gone") and the E4 brief need it, and E4 may not edit the engine.
*Repair:* a field on `Script` (for example "abandon after statement n": the engine stops
sending, and releases with the status of the last reply and `ClientGone: true`), a test that
the core closes the connection (`closes{client_gone}` moves, the connection is never lent
again), and a line in NOTES-E2.md §2.3.

**2. (major for report table 3, small) `first_stmt_ns` times the sleep.** The default `plain`
script is the single statement `SELECT pg_sleep(t)`, so `first_stmt_ns` by mode
(`engine.go:451`) is the drawn lease time plus the penalty: the P5 effect (0.5–1.5 ms) is buried
under an exponential with mean 5 ms and no quantile of the three modes can be compared.
*Repair:* make the default script two statements — a short statement of class
`workload.stmt_class` first, then the sleep — so the first statement is the thing P5 is about,
in both tiers (E5 is told to use the same shape on real Postgres; R8 needs identical scripts).
Adjust the load definition or state the extra round trip in the notes. U5 and the fit stay.

**3. (minor, seam) `Result` drops asynchronous messages.** Only rows, the last tag, status and
SQLSTATE survive (`simdb.go:304–350`). A `NotificationResponse` reaching the next tenant is
one of the canaries E4 is asked to try. *Repair (cheap, do it with 1):* `Result.Notifications
[]string` (channel and payload), documented; the simulator leaves it empty.

**4. (minor) The DB contract "never calls the Sink from inside the call" is broken on a write
error.** `SimDB.Exec` → `write` → `died` → `sink.ExecDone` runs synchronously (`simdb.go:209`,
`236–256`). I traced the engine's four call sites; each tolerates it today. E5: do not copy
this; deliver through the loop. No repair required.

**5. (minor, carried) One dead connection = "database lost".** `engine.go:375`: the core then
closes every idle, warm and resetting connection. Right for a restart; on real Postgres one
killed backend empties the pool and causes up to `cap` reconnects. It needs no core change for
this experiment provided: E5 counts `db_lost` per run and marks a TIMING run with `db_lost > 0`
invalid; E4 does not use backend termination as a taint inside timing-sensitive tests and
states, where it kills a backend, that the whole pool was recycled.

**6. (minor) Refused leases are in no wait histogram.** Under overload `lease_wait_ns` describes
granted leases only (it pins at wait cap / rate). E6 must print refusals beside every wait
quantile and treat a run with refusals at ≤ 95% load as over capacity, not as "p99 = x".

**7. (minor) Output size.** Histograms are flushed cumulatively: 6 MB per 20 s run at the default
1 s interval. E3: set `obs.interval` to the run length for bulk sweeps and keep 1 s only where
a time series is tabulated (`step`).

**8. (note) What the simulator's numbers are.** The P5 penalty, the effect of `warm.prime`, the
reset cost and the close lag are calibration inputs replayed, constant, without tails. In the
simulator `warm.prime=select1` can only remove what was typed in (0.52 ms of 1.57 ms). The
evidence for priming is tier R (R7); the report must not present the simulator's prime
difference as a finding.

## C1 — against the E1 core, for the coordinator (major; not E2's defect)

`remake-on-switch` as built thrashes at every load I tried, and the warm stock is never hit.
Skewed, 10 tenants, cap 20, `fixed:4`, medium leases, same seed as the baseline:

| Load | Connects/s | `reuse` | `warm_hit` | p50 wait | p99 wait | Baseline p99 wait |
| --- | --- | --- | --- | --- | --- | --- |
| 30% | 173 (the model's ceiling) | 221 of 23,963 | 6 | 24 ms | 61 ms | < 1 µs |
| 60% | 168 | 0 of 47,751 | 0 | 28 ms | 73 ms | 1.2 ms |
| 80% | 129 | 0 of 63,867 | 0 | 37 ms | 110 ms | 18 ms |

Cause, in `leasepool/pool.go:528–572` (`provision`): every waiting lease counts in the deficit,
and under `remake-on-switch` the deficit closes idle tagged connections with no bound
(`mk = deficit − free − closing`): not by `connect.max_concurrent`, and whether or not the
waiter's own tenant has connections lent that will come back within a lease time. A transient
queue of k waiters therefore destroys k idle connections of other tenants, each needing an
18 ms rebuild at ≤ 174/s; those tenants then queue in turn. The same waiting leases are counted
as "arrivals that needed a fresh connection", so under `dynamic` the rule's λ̂ is fed by the
thrash (on `step` at 10% base load: λ̂ about 400/s, target 13–14 of 20 slots, `warm` gauge 0
throughout, 10,423 connects for 47,750 leases).

Scratch check (not committed): bounding `mk` by the K14 room alone restores the 30% case
(reuse 16,048, warm hits 1,139, p50 wait < 1 µs, p99 17 ms) but not 60% or 80% (p50 12 and
29 ms), so the bound is necessary and not sufficient; the waiter rule is the rest.

Why it matters: E1's review and U1 hold the core to its stated rules, and it meets them — the
rules are the problem. If E3 and R7 run on this core, `remake-on-switch` fails §10's wait line
everywhere for a reason that is a provisioning choice, not the cost of a remake, and S3/S5
(miss rate against W, the rule against fixed sizes) measure a stock that waiters drain before
any switch can use it. Options for the coordinator: (a) repair the core now, with the same
repair run as findings 1–3 — make room only for what can be opened now (K14 room), let a
waiter trigger a remake only when its tenant has no connection lent, idle or on the way (or
after a stated patience), and count as fresh-connection demand only leases that took or were
given a never-lent connection; U1's reference model changes with it; (b) keep the core and
report the thrash as the policy's result. I recommend (a): (b) would test a strawman.

## Carry-forward

- **E3:** finding 7 (flush interval). S3 only at loads with free slots (E1 review). Whatever is
  ruled on C1, S8 must tabulate connects/s and `reuse` share per arm so thrash is visible.
- **E4:** client-gone via the new `Script` field (finding 1); notifications via finding 3; a
  second session for a check is not available through `DB` (its `Open` results go to the
  engine) — open your own side connection in the real tier, or use catalog views from the same
  session; finding 5.
- **E5:** same two-statement lease script as the simulator (finding 2); never call the Sink
  from inside a DB call (finding 4); `db_lost > 0` invalidates a timing run (finding 5);
  `lease_wait_ns` is measured from the intended start — keep it.
- **E6:** conservation includes `pending` and `closes_done` (copy `checks/conservation.sql`);
  finding 6; SQL must not sit directly in `test/analysis/lease/`; switch-rate budget against
  both 195/s (measured) and the run's own `cm.*` ceiling.
- **E8 (report):** the connect model's −11% ceiling and the fact that lease work does not load
  the connect CPUs; finding 8.

## Required for review 2

Findings 1 and 2 repaired (3 with them), tests green, U5 still byte-stable, NOTES-E2.md updated.
C1 by the coordinator's ruling.
