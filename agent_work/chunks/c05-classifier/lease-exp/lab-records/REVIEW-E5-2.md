# Review 2 (confirm, final) of piece E5 — the repair round

Reviewer: Fable, 2026-11-26. Scope: commits `3d713a7..1bf38cb` on `exp/lease-lab-e5` (after
merge `b16edcf`), against `/root/briefs/zgateway-lease-e5-repair.md` and `REVIEW-E5-1.md`.

**Verdict: NITS.** Every repair item is done and tested. No further repair. E5 can merge into
`exp/lease-lab`, E4 can plug in, and the tier-R campaign can be launched with the command in
section 4.

## 1. What I ran

- `gofmt -l .` empty; `go build ./...`, `go vet` clean.
- `go test ./internal/lab/... ./cmd/mglease/... -count=1`: all pass (leaselab 95 s).
- `scripts/lease-exp.sh test` (container tests, `-race`): all pass, no race report, no
  container left.
- **Simulator unchanged at defaults.** The same `mglease sim` configuration (remake-on-switch,
  dynamic, skewed, 10 tenants, load 60, seed 7) on this branch and on `exp/lease-lab`:
  `metrics.csv` (3,409 rows) and `histograms.csv` (27,616 rows) differ only in the `run_id`
  column (the output directory's name); `oracle.csv` is identical; `lab.csv` differs only by
  the six new rows (`obs.warmup`, `db.hold_quantum`, `db.hold_extra`, `warmup.*`) and the
  replay line naming the three new keys; `run.csv` by the run id and the config hash.
- Read the whole diff of the five E2 files, the capacity rule, the manifests, the script's
  `e7` and `detach` paths, and the rehearsal's kept tables. `mglease campaign … phase=plan` on
  `e7-3-r7a.txt` lists 201 runs, as the proposed campaign has it.

## 2. Items

| Item | Result |
| --- | --- |
| 1 `server.csv` | `time,count,procs,query_ns`; E6 reads it (`TestRealReportReadable`; rehearsal table 0 empty for R8, R7a, R7b; R6's one row is a run labelled `invalid` by the lateness gate on a busy host, which is the design) |
| 2 `lab.csv` keys | `run.valid`, `run.invalid`, `cpu.pg_ns`, `cpu.lab_ns` in ns; table 10 of the rehearsal prints measured Postgres CPU (about 0.50 s per 1,000 baseline leases) with an empty `cpu_note` |
| 3 `obs.warmup` | Both tiers. A lease is "measured" by its intended start. Four histograms exclude warm-up leases; counters stay whole; the warm-up phase is reported beside them. Tests compare the histograms with `leases.csv` in each tier; replays stay byte-identical |
| 4 capacity | Median window goodput of saturated steps (valid ones when at least three are valid). Sound: see finding 2 |
| 5 hold fidelity | `db.hold_quantum` / `db.hold_extra` act only in `simdb.go` (the simulator); the probe uses its own session and the workload's own drawn lease times, not R8's runs; the plain twin runs too and `compare` prints both |
| 6 campaign | `reps_at` works; four manifests match the proposed table and the narrowed ruling (section 3); `obs.warmup=5s` everywhere; one detached, resumable command |
| race fix `49fec9d` | Two counters read with `atomic.LoadInt64`; correct and minimal |

## 3. The manifests against the ruling

- `e7-1-r6`: remake-on-switch fixed:4 (skewed 10 tenants at load 60; 100 uniform tenants at
  load 4), each in both `remake.order` values; remake-always cold with K14 = 4 and K14 = 20.
  3 reps × 20 s, monitor every 2 ms. 18 runs.
- `e7-2-r8`: both tiers plus the plain simulator twin; S3-like W = 1, 2, 4 at load 1; S6-like
  `none` and `reset-always` at 15/30/45. 3 reps. Hold probe of 400 samples first.
- `e7-3-r7a` (mix A, 6 tenants, Zipf 3.0): `base`, `aa`, `ra`, `ros`, `rmsf`, `rmsf0`, `rmsd`,
  `rmsdp`, `rma` at cap:30/60/80/95; `rmsd0`, `rmsdp0`, `rmaw` at cap:80 only. 10 reps at
  cap:80, 3 elsewhere. 201 runs plus a 6-step ramp.
- `e7-4-r7b` (mix B, 10 tenants, Zipf 1.1): `base`, `aa`, `ra`, `ros`, `rmsf`, `rmsf0`,
  `rmsd`, `rma` at cap:80, 6 reps. 48 runs plus a ramp.

Four policies under test, remake-always as the cold control and one warm arm (`rmaw`; it gets
10 repetitions at cap:80, more than the "one run" asked for, which costs about 6 minutes and
harms nothing). Patience `0` is a labelled extra. Prime is compared on the dynamic arm. No R2,
no dispersion, no smoothing sweep.

## 4. Findings (all minor; none needs a repair)

1. **The goodput line includes warm-up leases.** It cannot give a wrong §10 verdict at 80%
   load: runs are open-loop and every arm of a round has the same seeded schedule, so below
   the knee each arm releases every lease it was sent, warm-up or not, and the ratio is 1.0
   with or without them. Above the knee (remake-always) the warm-up's 5 s of 35 s dilutes
   nothing in the arm's favour: the same leases are in the baseline's count. The consequence
   is the one REVIEW-E6-2 already states: below the knee the goodput line cannot separate
   arms, and the p99 wait line carries the verdict. E8 should say so beside table 9.
   `summary.csv` has `goodput_measured_milli_per_s` if a measured-phase figure is wanted.
2. **"Saturated" = a measured-phase lease was refused**, with `pool.wait_cap` = 1,000 (the
   default; the ramp does not change it). Sensible: a step whose offered rate exceeds
   capacity by 50/s overflows the list within 20 s, and its window goodput is then what the
   pool sustains. A step just above capacity may not overflow in 30 s and is then simply not
   used. With the ramp at 50–130% of nominal 4,000/s and real capacity near 2,200–2,600/s,
   five of six steps saturate. In the rehearsal's two-step ramp only one step was saturated,
   so its capacity is a single reading; the full ramp does not have that problem.
3. **`cpu.pg_ns` is the whole run**, divided by all leases requested. Consistent. The cold
   start's 20 connects cost about 0.3 s of Postgres CPU, which is under 1% of a 35 s
   baseline run at cap:80 and about the same in every arm; it is negligible beside the
   differences R9 is after (0.5 s against 18 s per 1,000 leases).
4. **The hold quantum is chosen from two candidates** (0 and 1 ms), the extra is the median
   residual. Declared, not fitted to R8. E8 must state the probed values and show the plain
   twin beside the held one.
5. **`rmaw` runs 10 times**, not once (section 3). Leave it.
6. **The ramp's runs appear in table 9 as an arm** (`ramp`) with inconclusive rows. Ignore
   them in E8, as the notes say.
7. **Table 10's "switches per second" for `none` and the reset policies** counts leases whose
   connection last served another tenant (1,100–1,200/s in mix B). That is the rate a remake
   policy would have to carry without affinity help, not a cost those policies pay. E8 should
   label the column so.

## 5. For the coordinator

**(a) Launch the tier-R campaign** (after merging E5, from the merged worktree; quiet host —
no builder or reviewer running; one hold of the machine lock for all four manifests):

```sh
cd /root/Software/mini-gateway-work/lease-lab        # after the merge
scripts/lease-exp.sh detach lease-e7 e7 /root/runs/lease-r
```

- Alive: `pgrep -f 'lease-exp.sh e7'`. Log: `/root/runs/lease-e7.log` (a start and an end
  line per manifest).
- Finished when `/root/runs/lease-e7.exit` exists: 0 = every run ok; 1 = some run violated a
  check (kept and labelled; read `index.csv`); 2 = setup error — run the same line again, it
  resumes (finished runs, an existing `hold.csv` and ramp are skipped).
- Outputs: `/root/runs/lease-r/{1-r6,2-r8,3-r7a,4-r7b}/`, each with `manifest.txt`,
  `env.txt`, `index.csv`, `capacity.csv` (R7), `hold.csv` (R8) and one directory per run.
- Expected: about 3 h 10 min (R6 7 min, R8 20 min, R7a 2 h 10 min, R7b 35 min).
- After it, check `real.other_cpus_milli` in the runs' `lab.csv` (under about 300 for a quiet
  host) and the count of invalid runs per arm in `mglease summary`.

**(b) Reports** (one per campaign root):

```sh
R=/root/runs/lease-r; P=/root/runs/lease-r-report
scripts/lease-report.sh $R/1-r6 $P/r6 server_extra=0
scripts/lease-report.sh $R/2-r8 $P/r8 server_extra=0 sim_real_band_pct=100
for m in 3-r7a 4-r7b; do
  vl=$(awk -F, '$4=="cap:80"{print $6; exit}' $R/$m/index.csv)
  scripts/lease-report.sh $R/$m $P/${m#*-} baseline_arm="'base'" aa_arm="'aa'" server_extra=0 \
    verdict_load=$vl min_valid_reps=5 sim_real_band_pct=100
done
go run ./cmd/mglease compare $R/2-r8          # the R8 table of record (held and plain twins)
go run ./cmd/mglease summary $R/3-r7a $R/4-r7b
```

`lease-report.sh` exits 1 when table 0 has rows; a row of severity `invalid` is a labelled
run and does not make the campaign unreadable, a row of severity `broken` does.

**(c) E4's hook into `mglease real`:** NOTES-E5.md section 6 ("For E4: how to plug in"):
register the script source and the inspector from `init` in E4's own files; `mglease real …
workload.script=<hostile> oracle.inspector=<name> oracle.rate=1 identity=query` writes
`oracle.csv` and `backends.csv`; a second session through `leaselab.OpenSession` with its own
`application_name`; the container through `scripts/lease-exp.sh run|campaign`. E4's campaign
is functional, not timed, so it does not need the lock, but it must not run while the tier-R
campaign holds it (it would disturb the timing).
