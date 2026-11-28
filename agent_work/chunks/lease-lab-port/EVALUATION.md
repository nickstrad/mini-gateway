# Evaluation of `exp/lease-lab` against current main — keep / drop verdicts

Evaluator: a fresh Opus agent, 2026-11-27, on branch `work/lease-port` (cut from main `98cee87`).
Brief: `/root/briefs/zgateway-p2-lease-eval.md`. Binding ruling:
`/root/briefs/zgateway-lease-lab-port-ruling.md`. This is an evaluation only: no code was
ported. Every figure below comes from a command run during this evaluation. The commands are
named in each section.

The branch: `exp/lease-lab`, tip `5ffc7a3`, merge base with main `f7b2ec8`, 68 commits. It
only adds files: `git diff f7b2ec8 exp/lease-lab --shortstat` gives 833 files changed and
505,406 insertions, with no deletions. Main contains none of those 833 paths.

## Verdict table

| # | Piece | Verdict | One-line reason | Home on main (keeps) |
| --- | --- | --- | --- | --- |
| 1a | Isolation workload (E4): hostile kinds that main's tier R lacks, namely a held cursor, a role change and a custom setting carrying a secret | **KEEP IN PART** | A partial reset (no `CLOSE ALL`) passes main's tier R tests, and one held-cursor kind taken from E4 catches it (3 checks run, below) | `test/e2e/realenv.go` (setup functions) and `test/e2e/tierr_test.go` (`hostileCalls` and a read-back probe), run by G6 tier R |
| 1b | Isolation workload (E4): everything else (the 21-kind seeded deck, the lab engine's script source, the R5 "T/E/gone always closes" checks, the remake and warm-stock checks) | **DROP** | Built on the lab engine, not main's proxy. R5 contradicts main's decision D7, where a dirty connection is rolled back, reset and reused. The remake and warm-stock checks test designs main did not build | — |
| 2a | Oracle (E4): the rule "every catalog column must show dirt at least once with the reset off" | **KEEP** (as a new assertion) | Main's `test/oracles/dirty-check.sql` has seven columns, but only five ever see dirt (cursors and role never do). This rule would have flagged that gap | `test/e2e/tierr_test.go`, inside `TestTierRSessionAblationWithoutResetCrosses` (G6) |
| 2b | Oracle (E4): `test/analysis/lease/oracle/e4.sql` | **KEEP as a record** | Main already holds this SQL's input (24 `oracle.csv` and `lab.csv` files in `e7/isolation/per-run.tar.gz`) and its output (`e4-oracle-check.txt`), but not the SQL. With it, main's isolation result can be re-run from main alone | `agent_work/chunks/c05-classifier/lease-exp/e7/isolation/e4.sql` |
| 2c | Oracle (E4): the Go inspector `internal/lab/leaselab/oracle.go` | **DROP** | Coupled to the lab engine's `Inspector` seam and its superuser login. Main's tier R runs as a non-superuser and checks through its own gateway | — |
| 3a | `internal/lab/leasepool` (pool core, sizing rule R1, Poisson table generator, reference model U1–U4) | **DROP** | Main's `internal/pool` (with `model_test.go`) supersedes it. R1 was "recorded as tried" (REPORT.md, row 6) and is not built. The remake policies were rejected | — |
| 3b | `internal/lab/leaselab` (driver, seams, simulator shell, real-Postgres shell, scenarios S1–S12, U5, U6) | **DROP** | It measures the lab's own engine, not main's proxy, so it cannot catch a regression in shipped code. Its leaselab tests take 540.9 s on this host | — |
| 3c | `cmd/mglease` | **DROP** | Runner for 3a and 3b only | — |
| 3d | `cmd/mgcalib` and `scripts/lease-calib.sh` (calibration probe) | **DROP as code; KEEP the method as a summary** | Chunk 14 builds calibration on `cmd/loadgen` and `scripts/bench.sh`. The probe's method and pitfalls (connect phases, bridge path against the published port, CPU-bound connect ceiling) are worth a knowledge note | `docs/knowledge/lease-experiment-method.md` (new, short) |
| 4a | `scripts/lease-exp.sh`, container handling, campaign manifests (`manifests/`, `e4-campaign.txt`, `e4-reduced.txt`) | **DROP** | They drive `cmd/mglease`, which is dropped. Main already holds the four E7 manifests and the isolation manifest byte for byte (blob hashes compared) | — |
| 4b | `scripts/lease-report.sh` | **KEEP as a record** (with 5a) | It is the only way to run the report SQL that defines REPORT.md's verdict table | `agent_work/chunks/c05-classifier/lease-exp/report-sql/lease-report.sh` |
| 5a | Report SQL `test/analysis/lease/report/` (tables 0–10, views, params, `empty/` sentinels) and `checks/conservation.sql` | **KEEP as a record, not as a gate test** | It defines every threshold and the "inconclusive" rule behind main's REPORT.md. It is 1,112 lines with no run cost if it is not wired into the gate. It is prior art for chunk 14's `test/analysis/bench/` | `agent_work/chunks/c05-classifier/lease-exp/report-sql/` |
| 5b | U6, the report self-test (`internal/lab/leaselab/report_test.go`) | **DROP** | Its planted runs are produced by the lab simulator (3b). Chunk 14 has its own report tests in its plan | — |
| 6a | REPORT.md, CALIBRATION-NOTE.md, calibration.json, `e7/` tables and tarballs | **Already on main**; two pointers need fixing | Main holds 212 files (7,605,150 bytes) under `lease-exp/`. Two of its documents point at branch-only files | Edit `REPORT.md` (the S6 formula) and `CALIBRATION-NOTE.md` ("Where the rest is") |
| 6b | Raw calibration runs `lease-exp/runs/r0-*` (36 files, 30,627,241 bytes; `samples.csv` alone is 30,535,941) | **DROP** | `calibration.json` on main is the summary derived from them | — |
| 6c | `e4-evidence/` (628 files, 19,790,927 bytes, of which 13,393,920 are two `.duckdb` binaries) | **DROP; summary already on main** | It is the reduced rehearsal and test logs. Main's `e7/isolation/` holds the full 24-arm campaign that superseded it. NOTES-E4's matrix is kept with the notes (6f) | — |
| 6d | `e5-smoke/` (17 files, 28,910 bytes), `e5-rehearsal/` (26 files, 43,012 bytes) | **DROP** | Smoke and rehearsal runs. Their numbers are not evidence, and the E7 runs on main replaced them | — |
| 6e | Review records `REVIEW-1.md`, `REVIEW-2.md`, `REVIEW-E1-1` … `REVIEW-E6-2` (12 files, 83,850 bytes) | **KEEP as records** | They are the verification trail of the evidence that ruling 9 cites. They are small, and `agent_work/` is where records belong (PLAN.md 12.2, step 9) | `agent_work/chunks/c05-classifier/lease-exp/lab-records/` |
| 6f | Builder notes `NOTES.md` (E0) and `NOTES-E1` … `NOTES-E6` (7 files, 285,183 bytes) | **KEEP as records** | NOTES.md section 4 is the only description of the schema of main's `calibration.json` (`mgcalib-calibration/1`). REPORT.md cites NOTES-E3 for the corrected capacity formula | `agent_work/chunks/c05-classifier/lease-exp/lab-records/` (rename E0's `NOTES.md` to `NOTES-E0.md`) |
| 7 | Anything else | Nothing else exists | Every one of the 833 paths falls under one of the six prefixes judged above (checked with `grep -v`) | — |

## 1. The isolation workload (piece E4)

**What it is.** The E4 workload lives in `internal/lab/leaselab/hostile.go` (110 lines, 21 kinds)
and `internal/lab/leaselab/oracle.go` (285 lines). Its tests are `oracle_tierr_test.go`
(467 lines, real Postgres) and `hostile_tierr_test.go` (52 lines). Each lease runs one hostile
kind with a per-lease secret. The next lease on that connection first runs a catalog query,
then tries to read the previous lease's secret by name.

**What main already has.** Scenario 4b in the simulator tier (`test/e2e/scenarios.go`
`sessionSpec`: five `SET` settings in three spellings, with an ablation and a lend-early plant)
and its per-policy form (`test/e2e/c07_scenarios.go` `PolicySpec`, checked by `checkPolicy`
in `c07_checks.go`). Tier R has `TestTierRSessionStateNeverCrossesClients` and
`TestTierRSessionAblationWithoutResetCrosses` in `test/e2e/tierr_test.go`, and
`TestTierRPoolSessionStateUnderEachReleasePolicy` (always and on-switch, each with re-seed on
and off, plus the `setseed` residue) in `test/e2e/c07_tierr_test.go`. Main's tier R uses five
hostile functions: `hide_set`, `make_temp`, `take_lock`, `do_prepare` and `do_listen`
(`test/e2e/realenv.go` `setupSQL`). It judges the result with `test/oracles/dirty-check.sql`,
which has seven columns: prepared, cursors, listens, advisory locks, session settings, temp
relations and role changed.

**The gap, checked.** No hostile function on main opens a cursor or changes the role, so two
of the seven columns can never show dirt. I ran three tier R checks in a scratch copy of main
(`/tmp/zgw-lease-eval/tree`, made with `git archive HEAD`, never committed). Each ran
`MG_TIER_R=1 go test -count=1 ./test/e2e/ -run 'TestTierRSessionStateNeverCrossesClients|TestTierRPoolSessionStateUnderEachReleasePolicy'`:

1. **The mutation.** In `internal/proxy/lease.go`, the reset sent was
   `RESET ALL; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD TEMP`
   instead of `DISCARD ALL`, and the reset judge accepted any command tags. This is the shape
   of a hand-written PgBouncer-style reset with `CLOSE ALL` and the session-authorization reset
   left out. Main's tests unchanged: **both PASS** ("0 dirty observations over 15 hostile
   leases"; 0 crossings in all four policy arms). The regression gets past main.
2. **The mutation plus one E4 kind.** I added `do_cursor()` (`DECLARE … CURSOR WITH HOLD`
   inside plpgsql, the way E4's `e4_f` does it) to `setupSQL` and `hostileCalls`. **Both
   FAIL**: "15 dirty observations through the proxy", and "3 session-state crossings between
   tenants" in each of always/off, on-switch/off, always/on and on-switch/on.
3. **Control: main's code unmutated, with `do_cursor()` added.** All PASS, including the
   ablation test, which reports 40 dirty observations without the reset. So the new kind is
   cleared by `DISCARD ALL`, and it is seen when the reset is off.

Each run took 17–23 s of package time on a host with a load average near 21.

**What else E4 covers that main does not.** A role change (`SET ROLE`; on main it would go
inside a function, as main's tier R already does to get past the courtesy reject list). A
custom setting carrying a secret: review E4-1, finding 1, showed that `pg_settings` does not
list placeholder settings, so only a read-back of the value catches it. Session authorization,
which needs a superuser login; main's backend role `RealBackendUser` is not a superuser, so
that kind cannot be ported as is. A temporary sequence, error-after-state, `SET LOCAL` followed
by a committed `SET`, multi-statement `Query`, and 32 `PREPARE`s. Each of those last kinds is
already caught by an existing main column (temp relations, prepared, settings) whenever the
reset is skipped, so they add little. I recommend porting the cursor, the role and the custom
setting read-back only.

**Why the rest is dropped.** The deck, the seeded script source and the inspector plug into
the lab engine (`leaselab.RegisterScriptSource`, `RegisterInspector`, the `DB` seam), which
is not main's proxy. R5 (`TestRealHostileR5`, `R5PatienceZero`) requires a lease that ended in
`T` or `E`, or whose client left, to be **closed** under every policy. Main's decision D7 and
`internal/pool/pool.go` instead roll it back, reset it and reuse it (`StepDone`: `stepRollback`
→ `startReset(ResetAfterCleanup)`). Ported as is, R5 would fail on correct main code. The
remake-policy identity checks and `TestRealHostileWarmStock` test designs that were not built.

**Cost of the keep.** About 10 lines of setup SQL in `realenv.go` and about 40–60 lines of
test Go (an estimate; nothing was written). The added hostile calls cost about one second of
G6 time (from the three runs above). There are no new dependencies.

## 2. The oracle (piece E4)

- **Main already has the catalog query.** `test/oracles/dirty-check.sql` has the same seven
  columns as E4's `hostileCatalog`. E4's version differs in two places. (a) It counts only
  settings whose value differs from `reset_val`. Main does not need that: it compares the whole
  output with a clean reference string instead of with zero. (b) It also counts
  `session_user <> 'postgres'`, which is only meaningful with a superuser login that main's
  tier R does not use. Neither difference should be ported.
- **Worth keeping: E4's positive-control rule.** `e4.sql` ends with "baseline exercises every
  catalog column": every column must be non-zero at least once with no reset. That rule is
  exactly what would have exposed main's two blind columns. Ported, it becomes one assertion in
  `TestTierRSessionAblationWithoutResetCrosses`: with `PlantNoReset`, each column of
  `dirty-check.sql` must differ from the clean value at least once.
- **`e4.sql` as a record.** Main's `e7/isolation/per-run.tar.gz` contains 24 `oracle.csv`,
  24 `lab.csv` and 24 `summary.csv` (listed with `tar -tzf`), and main's
  `e7/isolation/e4-oracle-check.txt` is the output of `e4.sql` over them. Only the 39-line SQL
  is missing. It needs no change beyond a comment naming the tarball.
- **`oracle.go` is dropped.** It needs the lab's `RealDB` and a superuser session for setup,
  and it reads the lab engine's lease metadata.

## 3. The experiment harnesses

Line counts are from `git show exp/lease-lab:<file> | wc -l`, split by `_test.go`:

| Package | Product Go | Test Go |
| --- | --- | --- |
| `internal/lab/leasepool` | 1,507 | 2,030 |
| `internal/lab/leaselab` | 6,469 | 4,226 |
| `cmd/mglease` | 1,867 | 569 |
| `cmd/mgcalib` | 1,612 | 0 |
| **Total** | **11,455** | **6,825** |

What I ran:
- `go test -count=1 -json -timeout 40m ./internal/lab/... ./cmd/mglease/...` in the lab
  worktree (read-only): exit 0. `leasepool` took 19.2 s with 19 tests passing. `cmd/mglease`
  took 22.1 s, with 13 passing and 2 skipped. `leaselab` took **540.9 s**, with 59 passing and
  10 skipped (the `TestReal*` tests that need E5's container variables). The load average
  during the run was 21–22 on 4 cores. A first attempt with the default 10-minute limit was
  stopped before `leaselab` finished.
- The lab code laid over current main (scratch copy, `git archive`) passes `go vet` for
  `./cmd/mgcalib/ ./cmd/mglease/ ./internal/lab/...`. So it still compiles against main's
  `pgwire`, `obs`, `sim`, `fakepg` and `simnet`. Porting it is not blocked by API drift, but
  it would still not test main.
- 66 of the lab's Go files carry 1,199 comment lines in total
  (`grep -rhc '^\s*//'`). The G2 build-stage rule (`nocomments -baseline`) rejects new
  comments, so every kept Go file would have to be stripped of them.

**3a `leasepool` — DROP.** It is a second pool core with five policies (none, reset-always,
reset-on-switch, remake-always, remake-on-switch), a warm stock and rule R1. Main's
`internal/pool` (3,391 lines with tests) is the shipped core. It has its own reference-model
test (`model_test.go`, `TestPoolMatchesTheReferenceModelOnRandomOperations`) and its own plants
(`PlantIgnoreTag` among them). I checked that main's own tests catch the obvious pool
regressions, so the lab core adds no protection:
- *No reset after a rollback* (`StepDone`, `stepRollback` → `toIdle` instead of `startReset`).
  Caught by `TestPoolMatchesTheReferenceModelOnRandomOperations` and
  `TestDirtyTableCancelsDrainsRollsBackAndResetsBeforeReuse`. The end-to-end
  `TestScenarioPool` and `TestScenarioE2E` still passed (194.7 s), so for this one main relies
  on the unit and model tests.
- *Release policy miswired*: the engine releases with an empty tenant tag
  (`internal/proxy/corepool.go`, `pool.End{Tenant: ""}`), so on-switch lends to anyone without
  a reset. Caught by three proxy tests (`TestCoreOnSwitchLendsTheSameTenantAsIsAndResetsOnASwitch`
  among them) and by six `TestScenarioPool` subtests ("session state crossed tenants 69–74
  times under on-switch").

Both mutations were reverted with `git checkout`; the tree is clean.

Rule R1 and the Poisson table serve a warm stock that main does not have (`internal/pool/config.go`
has no warm or minimum-idle field), and REPORT.md row 6 records R1 as "tried". The C1 test and
`remake.patience` exist only for remake-on-switch.

**3b `leaselab` — DROP.** It holds the event-driven lab engine, the simulator DB, the
real-Postgres shell (its own TLS and SCRAM DB seam, a monitor, CPU accounting), scenarios
S1–S12, the generator and the connect model. It runs leases through `leasepool`, not through
`internal/proxy`, so no mutation of main's code can make it fail. Where it overlaps shipped
behaviour, main already has the better test: S1 and S2 (isolation in the fake) correspond to
scenario 4b, and the reset reply rules correspond to the `e2e-reset-*` scenarios
(`test/analysis/e2e-reset-{drop,error,late,silent,status-t,wrong-tag}`). Kept, it would add
540.9 s to `go test ./...` on this host, which breaks `scripts/ci-fast.sh`'s five-minute
target. The corrected capacity formula (`PredictCapacity`) is kept as a sentence in REPORT.md
(section 6).

**3c `cmd/mglease` — DROP.** It is the runner for 3a and 3b (sim, real, campaign, sweep,
summary, compare).

**3d `cmd/mgcalib` and `scripts/lease-calib.sh` — DROP the code, keep the method.** Chunk 14
(PLAN.md, chunk 14 deliverables) builds a calibration mode into `cmd/loadgen` and a calibration
file into `scripts/bench.sh`. That calibration runs through the finished proxy, which
`mgcalib` never did. What chunk 14 should inherit is what the probe learned. It is all in E0's
`NOTES.md` sections 2, 3 and 6 (kept as a record, 6f). The port should also condense it into a
short `docs/knowledge/lease-experiment-method.md`, drawing only on CALIBRATION-NOTE.md and
E0's notes:
- the published `127.0.0.1` port through docker-proxy is about 12% slower on connects and twice
  as slow per round trip as the bridge address;
- the connect ceiling (about 195/s) is CPU-bound, not serial;
- the reset cost depends on how recently the backend was busy;
- `pg_sleep` holds a 5 ms lease for about 5.3 ms;
- two identical baseline arms differed by 109% in p99 wait (the A/A floor);
- the corrected capacity formula (NOTES-E3, item 6): a lease holds its connection for
  h0 = S + 2 round trips + 21 µs; a reset adds r = round trip + 89 µs; ceiling = cap /
  (h0 + resets per lease × r + connects per lease × (close lag + connect + fresh-backend
  penalty)). The plan's S/(S+r) predicted 73.9% for short leases against 79.1–79.4% measured;
  the corrected ratio h0/(h0+r) gives 79.7%.

## 4. The scripts and manifests

- `scripts/lease-exp.sh` (216 lines): run, campaign, e7, test and detach around `mglease` with
  throwaway `postgres:17` containers and the machine lock. **DROP**: its only target is dropped.
  Main's `scripts/tier-r.sh` and `test/e2e/realenv.go` (`StartRealEnv`) already own container
  handling for shipped tests.
- `scripts/lease-calib.sh` (140 lines): **DROP** with `mgcalib` (3d).
- `scripts/lease-report.sh` (87 lines): **KEEP as a record** beside the report SQL (5a). Moved
  under `agent_work/`, its `root` and default SQL directory (`LEASE_REPORT_SQL_DIR`) must point
  at the new folder. It needs DuckDB 1.5.x, the version main's G7 already pins.
- Manifests: `manifests/` (11 files, 11,889 bytes), `e4-campaign.txt` (3,231 bytes) and
  `e4-reduced.txt` (3,227 bytes). **DROP.** I compared blob hashes with `git rev-parse`: main's
  `e7/r6`, `r8`, `r7a` and `r7b` `manifest.txt` equal `manifests/e7-1-r6`, `e7-2-r8`,
  `e7-3-r7a` and `e7-4-r7b`, and `e7/isolation/manifest.txt` equals `e4-campaign.txt`. The
  smoke and rehearsal manifests describe runs that are not evidence.

## 5. The report SQL and its self-test (U6)

- `test/analysis/lease/` has 24 files and 1,112 lines: `report/00`–`10`, `07b`, `views.sql`
  (427 lines) and `params.sql`; eight header-only `empty/*.csv` sentinels; `checks/conservation.sql`;
  and `oracle/e4.sql` (judged in 2).
- It must **not** stay under `test/analysis/`. NOTES-E4 records that a SQL file placed directly
  under `test/analysis/lease/` was counted as scenario SQL by an existing suite and failed it.
  It also has no scenario of main's to run against. As a record under
  `agent_work/chunks/c05-classifier/lease-exp/report-sql/`, it runs nowhere, costs no gate time
  and keeps every threshold and the "inconclusive" rule (`09-verdict.sql`, 226 lines) behind the
  verdict table that ruling 9 rests on. Main's own report (`test/analysis/report/`,
  `test/analysis/views.sql`) has no histogram-quantile macro or A/A rule. The lab's `lr_hq` and
  the A/A floor in table 9 are prior art for chunk 14's `test/analysis/bench/`.
- U6 (`report_test.go`, 708 lines, with `duckdb_test.go`): **DROP.** Its planted and simulated
  runs come from `leaselab.RunSim`. Chunk 14's plan already lists the equivalent tests for its
  own report: a throttled run rejected, an A/A pair reported as "no difference", and a planted
  difference found.

## 6. The recorded results

**What main already holds** (`git ls-tree -r -l main agent_work/chunks/c05-classifier/lease-exp/`):
212 files, 7,605,150 bytes. These are REPORT.md (17,799 bytes), CALIBRATION-NOTE.md (8,989),
calibration.json (285,744), and `e7/` (209 files, 7,292,618 bytes). `e7/` holds the isolation
campaign with its per-run tarball (1,183,711 bytes), r6, r7a, r7b, r8, sim and sim-s5x5, each
with report tables 00–10, plus the manifests and logs.

**What the branch adds** (`git ls-tree -r -l exp/lease-lab`, minus main's paths): 739 files,
50,877,470 bytes.

| Part | Files | Bytes | Verdict |
| --- | --- | --- | --- |
| `runs/r0-*` (six calibration runs) | 36 | 30,627,241 | DROP. `samples.csv` is 30,535,941 of it. `calibration.json` on main is the summary |
| `e4-evidence/` | 628 | 19,790,927 | DROP. The rehearsal was superseded by main's `e7/isolation/`. Two DuckDB binaries are 13,393,920 bytes |
| `e5-smoke/` | 17 | 28,910 | DROP |
| `e5-rehearsal/` | 26 | 43,012 | DROP |
| `manifests/`, `e4-campaign.txt`, `e4-reduced.txt` | 13 | 18,347 | DROP (main holds the E7 and isolation manifests) |
| `NOTES.md`, `NOTES-E1` … `E6` | 7 | 285,183 | KEEP as records |
| `REVIEW-*.md` | 12 | 83,850 | KEEP as records |

**Two pointers on main that the deletion would break.** Both were found with `grep` over
REPORT.md and CALIBRATION-NOTE.md.
- `REPORT.md` line 144: "the corrected formula is in `NOTES-E3.md` on the lab branch". Fix: state
  the formula in one sentence (section 3d above, from NOTES-E3 item 6) and link the kept record.
- `CALIBRATION-NOTE.md` lines 120–124, "Where the rest is": it names the branch at `04cd947`,
  `cmd/mgcalib`, `scripts/lease-calib.sh` and the six run directories. Fix: say the probe and
  raw rows were not kept, and link `lab-records/NOTES-E0.md` for method and schema.
- `REPORT.md` line 6 names the branch `exp/lease-lab` (`5ffc7a3`). Fix: say it was deleted after
  the port, and name where its records now are.

## 7. Anything else

Nothing. Running `grep -v -E '^(agent_work/chunks/c05-classifier/lease-exp/|cmd/mgcalib/|cmd/mglease/|internal/lab/|scripts/lease-|test/analysis/lease/)'`
over the 833 paths printed nothing. Code, scripts and SQL account for 94 files (16 under
`cmd`, 51 under `internal/lab`, 3 scripts, 24 SQL and CSV); data and records account for 739.
The branch changes no existing file, including `go.mod`, `.gitignore` and the lint rules. The
branches `exp/lease-lab-e3`, `-e4`, `-e5` and `-e6` were merged into `exp/lease-lab`, but I did
not judge them separately. The deletion step should confirm they hold nothing beyond
`5ffc7a3` (`git log exp/lease-lab..exp/lease-lab-eN`) before removing them.

## Port plan

The build is one fresh Opus agent on a new branch from main, Neon review, Astra adversarial.
The steps are in order.

1. **Tier R hostile kinds** (test code only, no product code).
   - In `test/e2e/realenv.go` `setupSQL`: add `do_cursor()` (plpgsql,
     `EXECUTE 'DECLARE hostile_cursor CURSOR WITH HOLD FOR SELECT 1'`, the form verified in
     section 1); `do_role()` (create a role `mg_hostile_role`, grant it to `RealBackendUser`, and
     have the function run `set_config('role', 'mg_hostile_role', false)` or `EXECUTE 'SET ROLE …'`;
     the builder must check on Postgres 17 which form is allowed inside a non-SECURITY-DEFINER
     function); and `do_custom(secret text)` (`set_config('mg_hostile.secret', secret, false)`).
     Extend the `GRANT EXECUTE` line.
   - In `test/e2e/tierr_test.go`: add the three calls to `hostileCalls`. Add a read-back after
     each `do_custom` lease, as E4's custom-setting probe does:
     `SELECT current_setting('mg_hostile.secret', true)` on the next lease must not return the
     secret. Count it as dirty in `sessionRun` and as a crossing or same-tenant carry-over in
     `policyRun` (`c07_tierr_test.go`), which already iterates `hostileCalls`.
   - In `TestTierRSessionAblationWithoutResetCrosses`: assert that each of the seven
     `dirty-check.sql` columns differs from the clean value at least once with `PlantNoReset`
     (E4's "baseline exercises every catalog column").
   - Canary (BUILDER-RULES): a test-only proof that the new kinds have teeth. Recommended: keep
     the partial-reset reply in the test (as in section 1, run 2) behind a new test plant only
     if the orchestrator accepts a product plant (open point 1). Otherwise record the three runs
     of section 1 as the canary evidence in NOTES.md.
   - Gate stage: G6 (`scripts/tier-r.sh`, `-run '^TestTierR'`). Its run directories feed G7
     unchanged. The tests are in `test/e2e`, which is not on the determinism list; the fuzz
     inventory and `go.mod` are untouched. Estimate: 10 lines of SQL inside Go strings and 50–70
     lines of test Go, with no comments (Neon adds doc comments in the prose pass).
2. **Records.** Copy with `git show exp/lease-lab:<path>` into
   `agent_work/chunks/c05-classifier/lease-exp/lab-records/`: `NOTES.md` (renamed
   `NOTES-E0.md`), `NOTES-E1.md` … `NOTES-E6.md`, `REVIEW-1.md`, `REVIEW-2.md`,
   `REVIEW-E1-1.md`, `REVIEW-E2-1.md`, `REVIEW-E2-2.md`, `REVIEW-E3-1.md`, `REVIEW-E3-2.md`,
   `REVIEW-E4-1.md`, `REVIEW-E5-1.md`, `REVIEW-E5-2.md`, `REVIEW-E6-1.md` and `REVIEW-E6-2.md`.
   Add one header line to `lab-records/` (a short `README.md`) saying that the code and run
   directories these notes name were not kept and the branch was deleted. Copy
   `test/analysis/lease/oracle/e4.sql` to `lease-exp/e7/isolation/e4.sql`. Copy
   `test/analysis/lease/report/*` (with `empty/`) and `checks/conservation.sql` to
   `lease-exp/report-sql/`, and `scripts/lease-report.sh` to `lease-exp/report-sql/lease-report.sh`
   with its SQL directory default pointed at its own folder. About 420 KB of text and SQL, and
   no gate stage reads it. On main today, the tests that collect SQL files glob only
   `test/analysis/` (`internal/obs/simrun/csvassert_test.go`, `wiresql_test.go`,
   `classifier_test.go`), so SQL under `agent_work/` is not picked up. The builder re-checks
   this against the main it branches from.
3. **Pointer fixes on main** (orchestrator-owned documents; see open point 2): REPORT.md lines 6
   and 144, and CALIBRATION-NOTE.md lines 120–124, as listed in section 6.
4. **Knowledge note** `docs/knowledge/lease-experiment-method.md` (the bullets in section 3d,
   each sourced to CALIBRATION-NOTE.md, REPORT.md or `lab-records/NOTES-E0.md`), committed
   separately and indexed in `agent_work/INDEX.md`.
5. **STATE.md** at landing: the verdict table's keep and drop lines (ruling, step 1), then the
   branch deletion (ruling, step 3) after the confirmation in section 7.

### Open points for the orchestrator

1. **A product plant for the partial reset?** Adding `PlantPartialReset` to `internal/proxy`
   would make the canary permanent, but it is product code for a test-only purpose.
   **Recommendation: no.** The positive-control assertion of step 1 already guards the gap
   permanently, and the three recorded runs serve as the one-off canary.
2. **Who edits REPORT.md and CALIBRATION-NOTE.md?** Fable wrote both. **Recommendation:** the
   port builder makes the three factual pointer edits as drafts, and Neon's prose pass rewrites
   them if needed. They are not reader-facing book prose.
3. **Keep the notes and reviews (369,033 bytes) or summary only?** **Recommendation: keep.**
   They are the method behind the evidence ruling 9 cites, and NOTES-E0 is the only schema of
   `calibration.json`.
4. **An off-repository archive before deletion?** A `git bundle` of `exp/lease-lab` outside the
   repository (for example under `/root/runs/`) costs disk only and keeps the dropped code and
   data recoverable. **Recommendation:** Nick decides. The ruling says "delete", and nothing in
   this evaluation needs the archive.
5. **The real-tier A/A floor (109%).** It is not a port item, but chunk 14's brief should carry it,
   because its p99 verdicts will meet the same noise. Recommendation: name it in the chunk 14
   brief.

## Not ported

Every path on `exp/lease-lab` not listed under "Port plan" is dropped, by group:

- `internal/lab/leasepool/` — all 12 files (`pool.go`, `types.go`, `rule.go`,
  `poisson_table.go`, `poissongen/poissongen.go`, `poissongen/gen/main.go`, `c1_test.go`,
  `errors_test.go`, `ref_test.go`, `rule_test.go`, `u1_test.go`, `u4_test.go`).
- `internal/lab/leaselab/` — all 39 files, including `hostile.go`, `oracle.go`,
  `oracle_tierr_test.go` and `hostile_tierr_test.go`. The kinds are re-expressed in main's tier
  R (port step 1), not copied.
- `cmd/mglease/` — all 11 files.
- `cmd/mgcalib/` — all 5 files.
- `scripts/lease-exp.sh` and `scripts/lease-calib.sh`.
- `test/analysis/lease/report/`, `test/analysis/lease/checks/` and `test/analysis/lease/oracle/`
  at their current paths. They are re-homed as records (port step 2), not kept under
  `test/analysis/`.
- `agent_work/chunks/c05-classifier/lease-exp/runs/` — all six `r0-*` directories (36 files).
- `agent_work/chunks/c05-classifier/lease-exp/e4-evidence/` — all 628 files (`campaign/`,
  `campaign-mon/`, `report/`, `report-mon/`, `tests/`, and the top-level logs and
  `observed-matrix.csv`).
- `agent_work/chunks/c05-classifier/lease-exp/e5-smoke/` (17 files) and `e5-rehearsal/`
  (26 files).
- `agent_work/chunks/c05-classifier/lease-exp/manifests/` (11 files), `e4-campaign.txt` and
  `e4-reduced.txt`.

Kept (for the cross-check): the 19 notes and review files, `e4.sql`, the report SQL with its
sentinels and `conservation.sql`, and `lease-report.sh`, all as records under `agent_work/`;
the hostile cursor, role and custom-setting kinds and the positive-control rule, re-expressed
in `test/e2e`; the capacity formula and the method facts, as text.
