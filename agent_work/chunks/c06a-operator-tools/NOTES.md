# Chunk 6a — Operator tools: builder notes

Builder: Opus (`claude-opus-5-5`), 2026-11-27, branch `work/c06a-operator-tools`. Two helper
agents worked inside this build under the builder's direction and are part of it: one wrote
the story report, its SQL, its golden tests and the concise/verbose switch of `sweep.sh` and
`chaos.sh`; one did the promotion pass for chunks 1–4. The builder wrote the load tool,
`mgctl`, the dev stack and their tests, integrated and committed everything.

No SPLIT-REQUEST: every deliverable is built (see "Size").

## Repair round

Repair builder: Opus (`claude-opus-5-5`), 2026-11-27, one round, on review 1 (Neon, FAIL) and
Astra's adversarial results (36 held / 9 broken). Every regression test below was seen to fail
before its fix: the fixed files were swapped back to their pre-fix versions (or the review's
mutation applied in a scratch copy under `/tmp/zgw-c06a-operator-tools/`) and the test run.

Review findings:

| # | Severity | Outcome | Commit | Regression test |
| --- | --- | --- | --- | --- |
| 1 | MAJOR | fixed: the longest matching row message wins, an exact message beats a prefix, a row of the reply's severity beats one of the other severity; the prefix excuse is gone from the test, which now requires `m.Row.ID == r.ID` for every row with a message | `f16632a` | `internal/mgctl` `TestALongerRowMessageWinsOverItsPrefixAndSeverityDecides`, `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage` (tightened) |
| 2 | MAJOR | fixed (test): sends 400 ms after their intended time, replies 1 ms after the send; latency from intended send must show the 400 ms, latency from send must not. Fails under the review's mutation M1 (`now-s.sentAt`) | `67703fb` | `internal/loadgen` `TestLatencyIsMeasuredFromTheIntendedSendTimeNotTheActualSend` |
| 3 | MAJOR | fixed (test): every send late, none dropped: `Shortfall()` true, achieved below target, the `SHORTFALL:` line; sends exactly at the threshold are no shortfall; `cmd/loadgen` exits 3 on a late-only shortfall. Fails under mutation M2 (`s.CouldNotSend > 0`), in both packages | `67703fb` | `internal/loadgen` `TestLateSendsAloneAreAShortfall`; `cmd/loadgen` `TestLateSendsAloneExitThreeWithTheShortfallLine` |
| 4 | MAJOR | fixed: `check-loadgen-labels.sql` accepts `reason = 'other'` and still refuses any other non-class label; its claim line says so | `ea337d6` | `test/analysis/report` `TestReportLoadLabelsAcceptOtherAndRefuseMalformedClasses` |
| 5 | MINOR | fixed: after each step the tier R model waits (polling every 20 ms, deadline 15 s) for a `/stats` snapshot with a `seq` above the one current when the command returned, instead of one 250 ms sleep. Ran green against the real binary and Postgres 17 (`MG_TIER_R=1 go test -run TestTierRMgctlSeededModelAgainstTheRealBinary ./test/e2e`, 9.9 s) | `81d17ea` | `test/e2e` `TestTierRMgctlSeededModelAgainstTheRealBinary` (changed) |
| 6 | MINOR | fixed: (a) `Plan.Validate` refuses a plan with no operation, naming `-rate` and `-duration` (so `NewTracker` and `cmd/loadgen` refuse it: exit 2); (b) `live.Result` carries the CSV writer's `Close` error (`FlushErr`) beside `FlushErrs`, and `cmd/loadgen` prints `loadgen: CSV flush failed …` to stderr and exits 1 on either, quiet or not (the message no longer goes to the status writer, which `-quiet` discards) | `b2c4ca1` | `internal/loadgen` `TestAPlanWithNoOperationIsRefusedNamingTheFlags`; `cmd/loadgen` `TestAPlanWithNoOperationAndAnOverLongPasswordAreUsageErrors`, `TestACSVFlushErrorIsReportedAndFailsTheRun` |
| 7 | MINOR | fixed: the deadlock guards of `TestAdv_6_FullQueueWithBlockedFilesystem` and the poll deadlines of it and `TestAdv_6_AsyncDiskErrorRecoveryBoundary` are one named 30 s constant (`c03GuardDeadline`); no assertion changed (`TestAdv_14_OriginalAssertionsRetained` still passes) | `81d17ea` | — (the guards only bound a hang) |

Broken attacks (`agent_work/adversarial/c06a-operator-tools/`, not edited):

| Attack | Outcome | Commit | Regression test |
| --- | --- | --- | --- |
| `TestAdv_3_ZeroIntendedShortfall` | settled by finding 6: a plan with no operation is refused at validation, so no run can have zero intended operations. The attack now fails earlier, at its own `NewTracker` call (`-rate 37/s over -duration 1ns plans no operation`), because its premise "an accepted plan" no longer holds. **The orchestrator must settle this attack** (it stays red) | `b2c4ca1` | `TestAPlanWithNoOperationIsRefusedNamingTheFlags` |
| `TestAdv_3_OverflowedLatencyFormatting` | fixed: `FormatNs` (and the status line's elapsed time) divide before they scale (`ns/unit*10 + ns%unit*10/unit`), so `math.MaxInt64` formats as `9223372036.8s`; no narrowing needed | `b2c4ca1` | `internal/loadgen` `TestTheLargestDurationFormatsWithoutOverflow` |
| `TestAdv_8_LongStdinPassword` | fixed: one reader, `mgctl.ReadPasswordLine`, for `mgctl verifier -password -`, `mgctl query -password -` and `loadgen -password -`; a first line over 1,024 bytes (after a trailing `\r` is dropped) is refused with `the password line is longer than 1024 bytes` (exit 2) instead of being cut; exactly 1,024 bytes is accepted | `60385c4` | `internal/mgctl` `TestAPasswordLineLongerThanTheLimitIsRefusedNotCut`; `cmd/mgctl` `TestQueryRefusesAnOverLongStdinPassword`; `cmd/loadgen` `TestAPlanWithNoOperationAndAnOverLongPasswordAreUsageErrors` |
| `TestAdv_9_FatalWithoutReady` | fixed by finding 1 | `f16632a` | `TestALongerRowMessageWinsOverItsPrefixAndSeverityDecides` |
| `TestAdv_10_EmptySiblingRun` | fixed: the `structure: runs` check now requires a run directory at or below every directory argument; an empty one is named (`argument 2 (root2/): no metrics.csv, histograms.csv or run.csv at or below it`). An empty folder below a root argument is still not a run (unchanged; the attack does not claim it) | `ea337d6` | `TestReportEmptyArgumentBesideAGoodRunFails` |
| `TestAdv_10_WholeFinalFlushMissing` | fixed: `test/analysis/report/sections/flushes.sql` runs with the schema guard and fails a run whose metrics.csv holds fewer (or more) distinct `flush_seq` values than run.csv's `flushes` — the rule `csvflush.Reader` applies. A missing first or final flush both fail | `ea337d6` | `TestReportMissingWholeFlushFails` |
| `TestAdv_12_ChaosProcessFailureAfterPass` | fixed: `chaos.sh` keeps the exit status of each `go test` step; a non-zero status with no failed scenario is a failure, printed with the last 30 non-PASS lines of the log (the panic shows) | `f7747d6` | `scripts` `TestChaosAProcessFailureAfterPassingScenariosFails` |
| `TestAdv_12_ChaosParentFailure` | fixed: a scenario fails when its parent test (`--- FAIL: TestScenarioE2E/<sc> `) fails, not only a seed | `f7747d6` | `TestChaosAFailedParentWithPassingSeedsFailsTheScenario` |
| `TestAdv_13_DockerUnavailableFailsClosed` | fixed: `down.sh` fails (exit 1) when Docker is installed but a container, network or volume listing fails, naming the listing and Docker's message; a failed `compose down` is reported and the leftovers are still removed one by one and checked by the listings | `f7747d6` | `TestRunDownFailsWhenDockerCannotListTheStack` |

Not a finding, owed by this chunk (STATE.md open item): **the site's link check asks git** (`191ac9e`).
`.github/scripts/build_site.py` now refuses a relative link whose target exists but is not in
`git ls-files` (a folder passes when git tracks a file below it); it fails closed if `git
ls-files` cannot run. Fast test `scripts` `TestSiteLinksMustNameFilesGitTracks` (a throwaway
git repository with an ignored `.log`; a stub `markdown` module, so it needs no download);
gate canary `g9-ignored-link` in `scripts/gate-canaries.sh` (an ignored `docs/ignored-gate.out`
linked from `docs/index.md`; G9 must go red with `link to a file git does not track`). The canary
was not run through `scripts/gate-canaries.sh`: that script first runs the whole build-stage gate
on a clean copy, which the brief forbids on this shared host; its plant was reproduced by hand in
a scratch git copy instead (`scripts/site.sh` exit 1, `build_site: docs/index.md: link to a file
git does not track: ignored-gate.out`). The orchestrator's next canary run covers it. Size: 19
lines of script, 4 of canary, 35 of test — a little over the brief's "about 40"; kept because
the test is one table-like probe. Drop the commit if the orchestrator holds to 40.

Repair-round checks (host load 13–16, three other builds running): `go test -count=1 ./...`
including the adversarial packages: every package passes except
`agent_work/adversarial/c06a-operator-tools` (`TestAdv_3_ZeroIntendedShortfall`, above) and
`internal/pgwire` `TestLexOperatorRunsGrowLinearly` (a wall-clock growth test CR-3 replaces).
Build-stage gate, once, on the clean tree at `118a5d7`: G0, G1 and G2 green; G3 unit red only
for `TestAdv_3_ZeroIntendedShortfall` and the CR-3 growth tests
(`internal/pgwire` `TestLexOperatorRunsGrowLinearly`, `TestClassifierTimeGrowsLinearly`;
`agent_work/adversarial/c05-classifier` `TestAdv_5_WholeSpanRescanning`). Every other package
in G3 unit passed (`scripts`, `test/analysis/report`, `test/e2e` included). As the brief says,
the run was stopped there (during G3 race); G4–G10 were not reached in this round. No
`result.json` was written.

Adversarial tests still red after this round: `TestAdv_3_ZeroIntendedShortfall` only (see
above). Narrowed claims: none.

## Simulator change requests

None. The `mgctl` model test in tier S drives the frozen simulator through its public API
(`proxysim.BuildWorld`, `simnet.Net.Dial`, `sim.Sim.Step`); nothing under `internal/sim` changed
except added test files of the promotion pass (copies of chunk 1–2 attacks, see the ledger).

## Fuzz targets added

None. (The promotion pass copied no `Fuzz*` function as a fuzz target; see the ledger.) The
two pinned-inventory adversarial tests of CR-1 are therefore not affected by this chunk.

## What was built

| Path | What it is |
| --- | --- |
| `internal/loadgen` | The load tool's core, determinism-linted: `Plan` (seed, rate, duration, tenants, keys, profile → `Count`, `At`, `Due`, `Op`, `Digest`), the profiles `simple` and `errors`, `Tracker` (open-loop dispatch to connections, late / could-not-send / completed-by-outcome accounting, latency from intended send and from actual send), `Snapshot` with `Check()`, the `loadgen` metrics and mapping (`Metrics`, `Map`, `Source`), status and summary lines (integer arithmetic only). |
| `internal/loadgen/live` | The runner: dials and logs in every connection (SCRAM or trust, optional TLS), one loop goroutine owning the tracker and the collector, a writer and a reader goroutine per connection, the once-a-second status line and publish, the drain, the final flush (`csvflush`, async, tier `R`). |
| `internal/loadgen/loadgentest` | A real-socket fake Postgres for tests (trust auth; replies per statement like Postgres or the proxy would; knobs: per-reply delay, a stall window, never read). |
| `cmd/loadgen` | Flags (`-rate -duration -conns -tenants -keys -profile -seed -addr -database -user-prefix -password -tls -ca-file -server-name -max-outstanding -late-after -drain -csv-dir -quiet -plan N`), the seed line first, status lines, the summary with SHORTFALL. Exit 0 / 1 / 2 / 3 (SHORTFALL). |
| `internal/mgctl` | The client logic (no `flag`, no `net/http`): `Health`, `Stats` (watch with changes), `Metrics`, `Tenants`, `ConfigShow`, `ConfigCheck`, `Verifier`, `Query`, `PrintReply`, `ContractRow`/`ContractLine`/`ProxyMade` (error-contract lookup), `Login`/`Session` (SCRAM client over any `io.ReadWriter`), `ClientTLS`, `Transcript`. |
| `cmd/mgctl` | Standard-library `flag` with subcommands; HTTP `GET` admin client; TCP/TLS dialer; `MGCTL_TRANSCRIPT=<file>` (passwords in the arguments are written as `(password)`). Exit 0 / 1 / 2 / 3. |
| `deploy/dev/` | `docker-compose.yml` (Postgres 17.11 pinned by tag and digest, Prometheus v3.9.1; 127.0.0.1 only; `mg-` names; tmpfs data), `init.sql` (role `gateway`, table `kv`, rows k1..k1000), `env.sh`, `up.sh`, `load.sh`, `down.sh`. |
| `Makefile` | `run-up`, `load`, `run-down` (and `report`, see the report section) added as new blocks at the end. |
| `internal/obs/catalog` | Declares the `loadgen` metrics; `docs/csv-schema.md` regenerated (18 lines added). |
| `tools/lint-rules/` | `det-packages.txt` + `internal/loadgen`; `race-packages.txt` + `./internal/loadgen/...`; `imports.txt` + `deny internal/loadgen : internal/obs/... !internal/obs`. |
| `test/e2e/c06a_mgctl_test.go` | The seeded `mgctl` model test in process against the simulated gateway. |
| `test/e2e/c06a_tierr_test.go` | Tier R: the `mgctl` model against the real binary; the run targets end to end. |

## The story report, concise sweeps and chaos

Built by the report helper agent; integrated and committed by the builder.
#### What was built

- `scripts/report.sh <run directory or root>...` and `make report RUNS=<dir...>` / `make report SEEDS=N`
  (a block at the end of the Makefile). It prints seven sections over the flushed files of any set of
  runs:
  1. what the runs prove (every applicable `-- claim:` line, marked assertion or oracle, plus the
     report's own structural checks)
  2. how the data was collected (tiers, schema version, runs, flushes, rows, components)
  3. inputs (one row per scenario: tier, runs, seed range, arms, flushes, instances)
  4. coverage in nine tables (engine requests by outcome, engine `replies` by error-contract row,
     classifier `statements_by_class`, classifier `rejects`, workload `requests` and `errors_by_code`,
     loadgen totals, `completed` and `errors_by_class`)
  5. failed assertions in full
  6. oracle mismatches in full
  7. the verdict table (check, claim, observed, expected, pass)

  Every table carries its own legend. The output ends with exactly one line, `VERDICT: PASS (N checks
  over R runs[; k scenarios have no assertions])` or `VERDICT: FAIL (k of N checks failed over R runs...)`.
  Exit status: 0 pass, 1 any failure, 2 usage error.
- SQL under `test/analysis/report/sections/`, at depth 3 so that csv-assert.sh root mode never takes it
  for a scenario:
  - query files: `preamble.sql`, `load.sql`, `select.sql`, `scenarios.sql`, `collected.sql`,
    `inputs.sql`, `coverage.sql`
  - five built-in load-tool checks, `check-loadgen-{conservation,answered,drained,moved,labels}.sql`.
    Each has `-- requires:`, `-- claim:`, `-- applies-to: loadgen` and `-- applies-to-scenarios: load-*`
    lines and returns the violating rows, like a scenario's SQL file.
- `scripts/sweep.sh` is concise by default. It prints one line per scenario (package, test path without
  its `seed=N` element, seeds run, pass or FAIL, and `files <RUN_DIR>/<last element>` when that folder
  exists, otherwise the log). Under a FAIL line come every failing seed's `--- FAIL` line, its error
  lines (`file.go:N:`, `seed:`, `error:`) and its REPLAY line. After the unchanged summary line it lists
  other failures (non-seed tests, a package `FAIL` line), then the existing `first failure: REPLAY...`
  line. `VERBOSE=1` streams the raw output too (tee). `SWEEP_GO=<cmd>` is a seam for the go test step.
  The first line is still the `NOTE:` the gate records, and the exit codes are unchanged. go test now
  gets `RUN_DIR="$dir"` explicitly, so a manual sweep's CSV files land beside its log. The gate already
  passes RUN_DIR, so nothing changes for G4 and G7.
- `scripts/chaos.sh`: a FAIL scenario is followed by its `--- FAIL`, error and REPLAY lines. A
  `FAIL(sql)` scenario is followed by the `FAIL:` lines and violating rows of its `.sql.log` (tier S
  and tier R both). When no tier S scenario ran, the log tail goes to stderr. `CHAOS_GO=<cmd>` is a
  seam for both go test steps.
- Tests:
  - `test/analysis/report/report_test.go` (package `report_test`) with recorded data in
    `test/analysis/report/testdata/`. `e2e-runs.tar.gz` (123 KB) holds the real tier S flushes of
    `e2e-queue-full`, `e2e-reject-list`, `e2e-reset-error` and `e2e-results` at seeds 1-3, made by
    `DST_SEEDS=3 RUN_DIR=... go test -count=1 -run '^TestScenarioE2E$' ./test/e2e` on this branch's
    base. `load-errors/42/1764201600000000000/` is the hand-made loadgen run (schema v1, rows sorted,
    two flushes, a shortfall: 2 could not be sent and 3 were late).
  - Goldens: `recorded.golden`, `planted.golden`, `empty.golden`, `truncated.golden`.
  - `scripts/c06a_report_scripts_test.go` (package `scripts_test`): a stub `go` that prints a recorded
    log.

#### Decisions

- **Oracle and assertion.** A SQL file whose `-- requires:` line names metrics of more than one
  component is an oracle check: it compares the proxy's numbers with an independent record (workload,
  fakepg, loadgen). A file naming one component is an assertion. The built-in load-tool checks name
  only `loadgen`, so by this rule they are assertions and their failures go to section 5. That is a
  mechanical reading; the brief listed them beside section 7. Example: `e2e-queue-full/faults.sql`
  (engine only) is an assertion; `e2e-queue-full/rows.sql` (engine + workload) is an oracle.
- **Pairing.** A run's scenario is the `scenario` column of its run.csv, not the first path element.
  For runs laid out as `<root>/<scenario>/...` the two agree with csv-assert root mode. Reading run.csv
  also works when the argument is a scenario folder, a single run, or the parent of several roots, and
  it matches the `WHERE r.scenario = '<sc>'` filters inside the SQL files. Each scenario's SQL sees only
  that scenario's runs, as in csv-assert.
- **Same views.** All complete runs are read once through `test/analysis/views.sql` and copied into
  tables (`load.sql`). The views `metrics`, `hist` and `runs` are then redefined over those tables and
  narrowed to one scenario's runs (`select.sql`); `final` and `final_hist` of views.sql follow them by
  name. guard.sql runs over all complete runs as in csv-assert.
- **Speed.** All checks run in one DuckDB session. If that session errors, each file runs again in its
  own session, so a DuckDB error is reported against the file that caused it. The reason: one process
  per file took about 50 s on vm-1 at load 17; batched it takes about 7 s.
- **Fail closed.** Each of these is a failed verdict row, exit 1:
  - no run directory
  - an argument that is not a directory
  - a run directory without run.csv (truncated), or without metrics.csv or histograms.csv
  - a header that is not schema version 1
  - any guard.sql row (schema_version 2 in a row, duplicate run ids, and so on)
  - a required metric missing from a final flush (one `<scenario>: required metrics` row per scenario;
    the file it blocks shows "not run")
  - a DuckDB error
  - DuckDB missing or not 1.5.x

  When DuckDB is missing the report still prints every section ("not available"), the verdict table
  and the verdict line, and exits 1: never a pass. Usage errors (no arguments, an option) exit 2 with no
  verdict.
- **No assertions.** A scenario with runs and no SQL file or built-in check is shown in section 1 and as
  a `no assertions` row in the verdict table. It is neither a pass nor a failure, is left out of N, and
  is named in the verdict line's parenthesis.
- **Stability.**
  - Paths are relative to the argument (`rootN/` with several), and a final pass strips any absolute
    root or repo path from DuckDB messages.
  - Every row list is sorted (`LC_ALL=C`; violating rows are sorted too).
  - DuckDB prints only as "1.5.x"; there are no timings, dates or host names; `duckdb -no-init`.
  - Nothing is printed per run, so 3 and 300 seeds give the same lines.
- **Shortfall.** Shortfall is informative: coverage table 4.7 shows "achieved on time (sent - late)"
  against intended and counts shortfall runs (late + could_not_send > 0). It is not a check.
- **Canary.** `REPORT_PLANT=ignore-failures`, read only by report.sh and set only by a test, makes
  violating rows count as PASS.

#### Claims and the tests that prove them

- **Claim 10, the verdict is honest:**
  - `TestReportPlantedFailureAppearsInFull` in `test/analysis/report/report_test.go`. One recorded
    value is changed in `e2e-queue-full/2/base`: engine replies shed 3 becomes 2. The assertion row
    (faults.sql) appears in full in section 5 and the oracle row (rows.sql) in section 6. The loadgen
    final `sent` 198 becomes 197, and both load conservation rows appear in section 5 (197 + 2 + 0 =
    199, not 200; 6 + 0 + 188 + 4 = 198, not 197). The verdict reads `FAIL (4 of 36 ...)`, the exit
    status is 1, and the output equals `planted.golden`.
  - `TestReportPrintsEveryViolatingRow`: 25 failing runs give 25 rows; nothing is cut, unlike
    csv-assert's 20.
  - `TestReportEmptyDirectoryFails` and `TestReportTruncatedRunFails` (goldens).
  - `TestReportFailsClosed`: missing histograms.csv, a bad header, schema version 2 rows, a missing
    required metric, an argument that is not a directory.
  - `TestReportWithoutDuckDBFails`, `TestReportWrongDuckDBVersionFails` and `TestReportUsageErrors`
    (exit 2).
- **Claim 11, the report is stable:**
  - `TestReportRecordedRunsGolden` reports twice over two fresh copies at different temp paths, checks
    the two outputs are byte-identical, checks that no absolute path appears, and compares with
    `recorded.golden`.
  - `TestReportReadsTheSameAt3And300Seeds` compares 3 seeds (recorded seeds 1-3 of e2e-queue-full plus
    3 load-run copies) with 300 seeds: synthetic copies of the recorded seed-1 run and of the load run,
    with `run_id` and `seed` rewritten. Both give the same number of lines, the same section headings
    and the same verdict rows; the inputs row reads `300 | 1-300`. The 300-seed root is synthetic, as
    the brief allows.
- **Claim 12, concise by default, in `scripts/c06a_report_scripts_test.go`:**
  - `TestSweepConciseOneLinePerScenario`: recorded log across three packages, four scenarios; asserts
    exactly note + four lines + summary, with no raw lines.
  - `TestSweepVerbosePrintsRawOutput`.
  - `TestSweepConciseNeverHidesAFailure`: a planted failing seed shows its `--- FAIL`, error and REPLAY
    lines under its scenario line, and the run exits 1.
  - `TestSweepNoScenarioMatchedFails` (exit 1).
  - `TestChaosConciseAndVerbose`, `TestChaosFailedScenarioShowsItsReplay`, and
    `TestChaosFailedSQLShowsTheViolatingRows`, which uses a planted recorded run with real csv-assert
    and DuckDB and shows the violating rows.
- **Canary:** `TestReportPlantCanaryIsCaught` runs the same planted-failure check with
  `REPORT_PLANT=ignore-failures` and requires the check to fail. It does: "exit status 0, want 1".
- **make:** `TestMakeReport` (non-short) checks three things. `make report` alone exits 2;
  `make report RUNS=<dir>` gives byte-identical output to report.sh; `make report SEEDS=1` runs tier S
  e2e and ends in `VERDICT: PASS`.

#### Limits

- The DuckDB-backed report tests and the chaos SQL test skip under `-short` (and skip without DuckDB
  unless `MG_REQUIRE_DUCKDB=1`). `-short` runs the stub and no-DuckDB tests in about a second each.
  The scripts package still takes about 20 s under `-short` because of the existing fuzz.sh tests.
- sweep's `files <dir>` pointer is a folder named after the scenario's last path element that exists
  under RUN_DIR. For tests that do not flush (pgwire's TestScenarioWireStartup shares names with
  simrun's scenarios), it may point at another test's files.
- chaos SQL details come from csv-assert's `.sql.log`, which keeps at most 20 rows per file (said in
  chaos's header). The report has every row.
- A report over a full 200-seed gate sweep (about 4,000 runs) has not been timed. Each check reads
  tables, not CSV, so it should scale with DuckDB, not with bash.
- The load-tool checks assume the contract names, which `internal/loadgen/metrics.go` on this branch
  matches. The run id of real loadgen runs is assumed to be `load-<profile>/<seed>/<start-ns>`; the
  report does not depend on it.

#### Commands

```
scripts/report.sh <root or run dir>...            # or: make report RUNS="<dir> <dir>"
make report SEEDS=3                               # tier S e2e at 3 seeds into /tmp/report.*/runs, then the report
VERBOSE=1 scripts/sweep.sh -seeds 3               # raw output too; concise without VERBOSE
go test -count=1 ./scripts/ ./test/analysis/report/
MG_REQUIRE_DUCKDB=1 go test -count=1 ./scripts/ ./test/analysis/report/
go test -short -count=1 ./scripts/ ./test/analysis/report/
MG_REPORT_UPDATE=1 go test -count=1 ./test/analysis/report/   # or -args -update; rewrites the goldens
# re-record the tarball (from a fresh RUN_DIR of the tier S e2e run at 3 seeds):
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - e2e-queue-full e2e-reject-list \
  e2e-reset-error e2e-results | gzip -9n > test/analysis/report/testdata/e2e-runs.tar.gz
```

#### Verification (vm-1, 2026-11-27, load average about 13)

- `MG_REQUIRE_DUCKDB=1 go test -count=1 ./scripts/ ./test/analysis/report/`: both packages ok
  (`scripts` 72 s, `test/analysis/report` 153 s; the 300-seed test and `make report SEEDS=1` dominate).
- `go test -short`: the report package takes 0.7 s; my script tests take about 1.6 s.
- `gofmt -l` prints nothing; `go vet ./scripts/ ./test/analysis/report/` is clean; shellcheck on
  report.sh is clean. Three shellcheck notes in chaos.sh are on lines this change did not touch.
- `scripts/csv-assert.sh` root mode over a fresh 1-seed sweep root (115 runs): 166 SQL files run, all
  ok. The 13 failures are exactly the 13 `tierr-*` scenarios ("has SQL files but no run"), because
  that root has no tier R runs. That behaviour predates this change. `test/analysis/report` is not
  counted among the 59 scenario folders, so the SQL at depth 3 does not affect csv-assert.

## Decisions the brief left open

1. **Open loop with a finite connection set.** Requests are pipelined on a connection (the
   simple protocol allows it; the proxy answers in order). A connection holds at most
   `-max-outstanding` (64) requests, queued for its writer or in flight. An intended request
   with no connection of its tenant under that cap is counted could-not-send at its intended
   time; the schedule never waits for a reply or for capacity.
2. **Late** = written more than `-late-after` (25 ms) after the intended time. The writer
   stamps the send time just before the socket write (so a blocked write makes the next sends
   late). A send at exactly the threshold is on time (`TestLateIsCountedExactlyAgainstTheThreshold`).
3. **Achieved rate and SHORTFALL.** Achieved = target × (sent − late) / intended, i.e. the rate
   of requests sent on schedule. SHORTFALL is printed exactly when late + could-not-send > 0,
   so achieved < target ⟺ SHORTFALL. Exit status 3.
4. **The end of a run.** After the last intended time the tool waits up to `-drain` (5 s) for
   replies; then queued requests become could-not-send and unanswered ones stay "in flight at
   the end" (conservation sent = completed + in flight). An interrupt (SIGINT/SIGTERM) stops
   intending new requests and takes the same path; the summary says it was interrupted.
5. **Outcomes.** `ok`, `db-error` (an ErrorResponse that is not a proxy-made row), `refused`
   (code and message of an error-contract row whose message starts with `mini-gateway:`),
   `lost` (the connection closed with the request in flight). `errors_by_class` counts every
   error reply by the first two characters of its SQLSTATE (at most 32 classes, then `other`).
6. **Tenants and connections.** Connection c belongs to tenant c mod `-tenants`; `-conns` must
   be at least `-tenants`. Tenant users are `<user-prefix><i>`, one password for all.
7. **Statements in the fake's dialect.** `SELECT v FROM kv WHERE k = 'k<n>'`, `UPDATE kv SET v =
   '<hex payload>' WHERE k = 'k<n>'`; errors profile adds `INSERT INTO kv VALUES ('k1', …)`
   (23505), `SELECT v FROM mg_load_missing …` (42P01 on Postgres, 42601 on the fake: class 42
   both), `SELECT 1 / 0` (22012), `LISTEN mg_load` (refused by the proxy, 0A000). Mixes in
   per-mille: simple 800 read / 200 write; errors 500 / 200 / 80 / 80 / 70 / 70.
8. **Random seed.** Without `-seed` the tool draws one from `crypto/rand` and prints it on the
   first line with "repeat with -seed N". `-plan N` prints the first N operations and the
   digest without touching the network.
9. **No setup statements through the proxy.** The table and rows come from `deploy/dev/init.sql`
   (and the tier R setup), so the gateway's `requests_completed` during a load run equals the
   load tool's completed ok + db-error exactly.
10. **`mgctl` exit status 3** means "the gateway answered, but not well" (health not ok, an
    error reply to a query); 1 is "could not reach / failed", 2 usage or configuration.
11. **`mgctl tenants` and `config show`.** The stats JSON carries no tenant label in this chunk
    (no engine component exports one) and the admin surface has no effective-settings
    endpoint. Without editing the engine: `tenants` prints the tenant-labelled points it finds
    (none today, said in one line) and, with `-config`, the configured tenant users;
    `config show` prints the instance, build, fingerprint and the capacity gauges the stats do
    carry (`database_capacity`, `queue_capacity`, `relay_buffer_capacity`,
    `shell_mailbox_capacity`), and with `-config <file>` that file's effective settings (the
    database password shown as `(set)`) and whether its fingerprint matches the running one.
    No read-only field was added through `internal/obs` adapters (see "Deviations").
12. **`mgctl query`'s row lookup.** A reply is named by code and message against
    `pgwire.ErrorRows` (exact or prefix match). Rows whose text is Postgres's own
    (`cancelled-queued`, `auth-failed`, `too-many-clients`, `unknown-database`, `no-user`,
    `protocol-version`) are printed with "Postgres sends the same text for its own error of
    this code"; any other error is `pass-through`.
13. **Dev stack networking.** The gateway runs on the host; Prometheus uses the host network
    (its listen address set to 127.0.0.1 by flag) so it can scrape the gateway's 127.0.0.1
    admin port. Postgres is on the compose network with its port published on 127.0.0.1.
    Both data directories are tmpfs; `run-down` runs `down -v --remove-orphans` and then
    removes anything still named `<project>-…` / `<project>_…`.
14. **`run-down` keeps the CSV runs** (`$MG_DEV_DIR/csv`, `$MG_DEV_DIR/load`) for `make
    report`; it removes the binaries, configs, pid file and every container, network, volume
    and process.

## Claims and the tests that prove them

| # | Claim | Tests |
| --- | --- | --- |
| 1 | Open loop: intended send times depend only on rate, duration and seed; a slow or stalled server does not slow the schedule | `internal/loadgen`: `TestCountAndIntendedTimesAreExactAtTheConstantRate`, `TestIntendedTimesDoNotOverflowAtTheLimits`, `TestAnOpIsDueAtExactlyItsIntendedTime` (equality), `TestOpsDoNotDependOnTheRateOrDuration`, `TestAStalledServerShowsInLatencyFromIntendedSendNotInTheRate` (intended = sent = count under a 1 s stall); `internal/loadgen/live`: `TestAStalledFakeShowsInLatencyFromIntendedSend`; canary `TestPlantedClosedLoopHidesTheStallSoTheOpenLoopCheckCanFail` |
| 2 | Latency is measured from the intended send time | `TestLatencyIsMeasuredFromTheIntendedSendTimeNotTheActualSend` (late sends, quick replies: the two histograms differ), `TestAStalledServerShowsInLatencyFromIntendedSendNotInTheRate`, `TestAStalledFakeShowsInLatencyFromIntendedSend` (p99 ≥ 300 ms, max ≥ 500 ms for a 600 ms stall), the canary above (with the closed-loop plant the p99 drops under 100 ms, so the check can fail) |
| 3 | Never slows down silently: SHORTFALL whenever achieved < target; late and could-not-send exact | `TestLateSendsAloneAreAShortfall`, `cmd/loadgen` `TestLateSendsAloneExitThreeWithTheShortfallLine`, `TestAPlanWithNoOperationIsRefusedNamingTheFlags` (no run has zero intended operations), `TestTheLargestDurationFormatsWithoutOverflow`, `TestAThrottledServerGivesShortfallAndExactCouldNotSend`, `TestLateIsCountedExactlyAgainstTheThreshold`, `TestStatusAndSummaryLines`, `TestAchievedEqualsIntendedAtARateTheServerCanServe` (no SHORTFALL when served); live: `TestAThrottledFakeGivesShortfallAndCouldNotSend`, `TestAFakeThatStopsReadingMakesSendsLateOrUnsent`; `cmd/loadgen`: `TestAThrottledServerPrintsShortfallAndExitsThree` |
| 4 | Repeatable: same seed and flags → same operations, keys, payloads, in process and across processes | `TestTheSameSeedGivesTheSameOperationsKeysAndPayloads`; live `TestTheSameSeedSendsTheSameStatementsPerTenant` (two runs, per-tenant statement sequences equal to the plan); `cmd/loadgen` `TestTheSameSeedGivesTheSamePlanAcrossProcesses` (two processes print byte-identical plans whose digest equals the in-process one, and two process runs send identical per-tenant statements), `TestARandomSeedIsPrintedSoTheRunCanBeRepeated` |
| 5 | Conservation on every exit path including an interrupt | `TestConservationHoldsOnEveryExitPathUnderRandomEvents` (200 seeds, random ticks / sends / completions / connection losses / stops, `Check()` after every event and after `Finish`), `TestConnectionLossCountsLostAndCouldNotSendAndConserves`, `TestSnapshotCheckCatchesBrokenConservation`; live `TestConservationHoldsWhenTheRunIsInterrupted`, every live test through `run()` (checks `Snapshot.Check()`); `cmd/loadgen` `TestNoServerIsARuntimeFailureWithConservedNumbers` |
| 6 | `errors` profile: every promised outcome class appears and is counted under its SQLSTATE class | `TestEveryProfileKindIsDrawnAndUsesItsStatement`, `TestErrorsAreCountedUnderTheirSQLStateClass`, `TestErrorClassesAreBounded`; live `TestErrorsProfileShowsEveryOutcomeUnderItsSQLStateClass` (counts per class equal the plan's ops of that kind: 23, 42, 22, 0A; ok / db-error / refused equal the plan); seen on the real stack: `make load PROFILE=errors` gave `0A:72 22:65 23:85 42:76`, and Prometheus `requests_completed` = ok + db-error (890), `requests_rejected` = refused (72) |
| 7 | `mgctl` is read-only; `config check` accepts exactly what the gateway accepts | `internal/mgctl` `TestEveryAdminRequestIsARead`, `cmd/mgctl` `TestNoSubcommandSendsAnythingButAGet` (every request a GET to `/healthz`, `/stats`, `/metrics`); tier R `TestTierRMgctlSeededModelAgainstTheRealBinary` (`requests_received` moves only by the model's query count after every step); `internal/mgctl` `TestConfigCheckAcceptsExactlyWhatTheGatewayValidationAccepts` (300 generated configs, same verdict and error as `config.Load`), `cmd/mgctl` `TestConfigCheckAgreesWithTheGatewayBinaryOnGeneratedConfigs` (150 generated configs through both binaries: same exit status, same stdout, same error after the program-name prefix; both verdicts occur) |
| 8 | `mgctl verifier` produces a verifier the gateway's SCRAM login accepts, never prints the password | `TestAPasswordLineLongerThanTheLimitIsRefusedNotCut`, `TestVerifierIsAcceptedByTheGatewaysSCRAMAndNeverPrintsThePassword` (parsed, accepted by `NewTenantTable`, a SCRAM exchange with `pgwire.ScramServer` passes with the password and fails with another); tier R `TestTierRMgctlSeededModelAgainstTheRealBinary` (the real binary's tenant t0 has an `mgctl`-made verifier and `mgctl query` logs in with it); `cmd/mgctl` `TestTranscriptLogsEachCommandAndHidesThePassword` |
| 9 | `mgctl query` prints the rows, or the SQLSTATE with its error-contract row, for every row that can be produced through a running gateway | `TestALongerRowMessageWinsOverItsPrefixAndSeverityDecides`, `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage` (every row of `pgwire.ErrorRows` with a message is named from its code and text); `TestQueryPrintsRowsOrTheSQLStateWithItsContractRow`; tier S model `TestMgctlSeededModelAgainstTheSimulatedGateway` (rows, `unsupported-feature`, `pass-through`, `auth-failed` through the engine); tier R model (rows, `unsupported-feature`, `pass-through`, a wrong password). See "Stated limits" for the rows not produced end to end in this chunk. |
| 10 | The report's verdict is honest; empty or truncated runs fail | `TestReportEmptyArgumentBesideAGoodRunFails`, `TestReportMissingWholeFlushFails`, `TestReportLoadLabelsAcceptOtherAndRefuseMalformedClasses`, `test/analysis/report/report_test.go`: `TestReportPlantedFailureAppearsInFull` (one changed value: the assertion row and the oracle row appear in full, `VERDICT: FAIL`, exit 1, equals `planted.golden`), `TestReportPrintsEveryViolatingRow` (25 of 25 rows), `TestReportEmptyDirectoryFails`, `TestReportTruncatedRunFails`, `TestReportFailsClosed`, `TestReportWithoutDuckDBFails`, `TestReportWrongDuckDBVersionFails`, `TestReportUsageErrors`; canary `TestReportPlantCanaryIsCaught` |
| 11 | The report is stable | `TestReportRecordedRunsGolden` (two reports over copies at different paths are byte-identical, no absolute path, equal `recorded.golden`), `TestReportReadsTheSameAt3And300Seeds` (same lines, sections and verdict rows; the 300-seed root is synthetic copies with run id and seed rewritten) |
| 12 | Concise by default; `VERBOSE=1` raw; a failure never hidden | `scripts/c06a_chaos_down_test.go`: `TestChaosAProcessFailureAfterPassingScenariosFails`, `TestChaosAFailedParentWithPassingSeedsFailsTheScenario`; `scripts/c06a_report_scripts_test.go`: `TestSweepConciseOneLinePerScenario`, `TestSweepVerbosePrintsRawOutput`, `TestSweepConciseNeverHidesAFailure`, `TestSweepNoScenarioMatchedFails`, `TestChaosConciseAndVerbose`, `TestChaosFailedScenarioShowsItsReplay`, `TestChaosFailedSQLShowsTheViolatingRows`; `TestMakeReport` (non-short) |
| 13 | The run targets clean up; ports on 127.0.0.1 only | `scripts` `TestRunDownFailsWhenDockerCannotListTheStack` (fast: a Docker double); tier R `TestTierRRunTargetsUpLoadDown`: after `make run-up` every one of the four ports is listening on 127.0.0.1 only (`ss`), Postgres's published port is `127.0.0.1:…` (`docker port`); after `make run-down` no container or network named `<project>…`, no volume of the stack's containers, the gateway pid gone |
| 14 | The promotion ledger is true; CI's fast set stays under five minutes | The ledger below names every attack, its new file and name, and its gating; each promoted file was checked against a comment-free copy of its original (only skip lines differ); six reverted fixes made their regression tests fail (listed in the ledger); the promoted tests add about 2 s under `-short`; `scripts/ci-fast.sh` timing in "Size" |
| 15 | `go.mod` still has one requirement | gate G0 `tidy` and G1 `importlint` (`require` rule); `go.mod` unchanged in this chunk |

Smoke (also tier R, `TestTierRRunTargetsUpLoadDown`): `make run-up` → `make load RATE=40
DURATION=5s PROFILE=simple SEED=11` → Prometheus `mg_engine_requests_completed_total` moved by
exactly the load tool's completed ok + db-error from its own CSV flush (200), and non-zero.

## Canary

`loadgen.TrackerConfig.Plant = PlantSlowSchedule` (test-only; `TestNoConfigOrCommandSetsTheLoadToolsPlant`
holds that no command or runner file mentions it): the tracker waits for every reply before
making the next request due and measures latency from that moment — a closed loop. The
stalled-server check then loses the stall (`TestPlantedClosedLoopHidesTheStallSoTheOpenLoopCheckCanFail`
asserts the open-loop p99 ≥ 500 ms and the planted p99 < 100 ms). The report's canary is `REPORT_PLANT=ignore-failures` (read only by `scripts/report.sh`, set only by a test): `TestReportPlantCanaryIsCaught` requires the planted-failure check to fail with it.

## Observability

Component `loadgen` (`internal/loadgen/metrics.go`, declared in the catalog and
`docs/csv-schema.md`): `planned`, `target_rate`, `intended`, `sent`, `could_not_send`, `late`,
`queued`, `in_flight`, `completed{outcome=ok|db-error|refused|lost}` (all four always),
`errors_by_class{reason=<class>}` (open labels), `conns_open`, `connect_failures`, histograms
`latency` (from intended send) and `latency_from_send`. Tests: `TestSnapshotCountersMoveAndTheMappingExportsEveryMetric`
(every metric moves; the exported numbers conserve; a zero snapshot publishes),
`TestTheLoadToolFlushesItsOwnSnapshotToCSV` (the final flush equals the snapshot). No new proxy
metric.

## Stated limits

1. **Late depends on the host.** Go timers and goroutine scheduling on a loaded 4-vCPU VM can
   delay a send by milliseconds; the 25 ms default keeps that out of "late", and the tests use
   250 ms. A send delayed by the tool itself and one delayed by back pressure are both late.
2. **Latency from intended send includes the time a request waits behind earlier requests on
   its connection** (pipelining) — that is the open-loop measure, not the database time.
3. **Outcome `refused` relies on the proxy's message text.** A Postgres error whose code and
   text equal a row whose text is Postgres's own (`cancelled-queued`'s 57014 cancel) is
   counted `db-error`; only rows whose text starts with `mini-gateway:` count `refused`.
4. **`mgctl query` row coverage.** Produced end to end in this chunk: rows, `pass-through`,
   `unsupported-feature` (tier S and R), `auth-failed` (tier S and R). The other rows are named
   correctly from their code and text (unit test over every row) but their production through
   a running gateway is left to the scenarios that make them (`shed`, `lost-*`,
   `tls-required`, … are produced by chunk 6's e2e scenarios, not by `mgctl`).
5. **`mgctl tenants` shows nothing per tenant until a component exports a tenant label**
   (chunk 9's fairness); `config show` shows only the settings the stats carry.
6. **The dev stack's fixed defaults** (ports 55432, 59090, 56432, 56433; project `mg-dev`) can
   collide with another stack; every one is overridable (`deploy/dev/env.sh`) and the tier R
   smoke test uses free ports and its own project name.
7. **`config check` differential** compares the two binaries over configs mutated from
   `internal/config/testdata/example.json` (deleted keys, unknown keys, wrong types, negative,
   zero and huge caps, bad durations and modes); it is not an exhaustive grammar.

8. **Password lines read from stdin (`-password -`) are at most 1,024 bytes**; a longer first
   line is refused, not cut. A password given in the argument has no such limit.
9. **`flushes` in run.csv must equal the distinct `flush_seq` values in metrics.csv** for the
   report to pass (as `csvflush.Reader` requires); a run with a dropped flush fails.

## Deviations

- **No read-only field added to the admin surface.** Per-tenant numbers and the effective
  settings are not in the stats JSON and cannot come from `internal/obs` adapters alone (no
  snapshot field carries them). Recorded as a gap instead of an engine edit; `mgctl` works
  with what is there (decision 11).
- **Load-tool profile statements use the fake's table** (`kv`), so the dev stack's
  `init.sql` creates `kv` rather than a load-specific table.

## Promotion ledger (chunks 1–4, PLAN.md 12.2 step 9)

Done by the promotion helper agent; integrated and committed by the builder.

Done on branch `work/c06a-operator-tools` from base `a2f4ec9`, committed as `test: promote the held attacks of chunks 1-4 into the shipped suite`. The originals under `agent_work/adversarial/` are unchanged.

How the copies were made. A scratch AST tool (not kept) copied each declaration from the original files. It dropped every comment, renamed package-level helpers with a chunk prefix (`c01Must`, `c02Rig`, `c03Snapshot`, `c04Config`, ...) so they cannot collide with the existing external test packages, and rewrote only these string literals: REPLAY lines (`./agent_work/adversarial/<chunk>` to the new package path), repo-root relative paths (`../../..` to `../..` in `internal/sim` and `internal/obs`), the pgwire source glob (`../../../internal/pgwire/*.go` to `*.go`), the c04 oracle path (`oracle-<kind>.json` to `testdata/adv-c04-oracle-<kind>.json`), and the re-exec target `^TestReplayWorker$` to `^TestAdvC03ReplayWorker$`.

Equivalence check. Each promoted file was reverse-renamed and compared with a comment-free canonical copy of its originals. The only differences are the gate lines listed below, plus one-line `read` helpers that gofmt expanded onto several lines. Inputs, seeds and assertions are identical.

Gates added. Apart from these gates and the tier R renames and splits (next section), the code was not edited:
- `MG_TIER_R=1` skip, using the convention of `test/e2e/tierr_test.go`. These tests are named `TestTierR*`, so `scripts/tier-r.sh` (`go test -p 1 -run '^TestTierR' ./...`) runs them in the gate's tier R stage. They are listed in the next section.
- `testing.Short()` skip (slow): c02 `TestAdv_9_ExtremeSeedBaselineReplay` and the three `TestAdv_10_*` (via `c02Baseline`); c03 `TestAdv_5_*` (via `c03CrossProcess`, two child processes each).
- `ADV_LONG` campaigns: unchanged.

### Tier R stage (follow-up)

Renamed, because the whole test only runs under tier R. The body is unchanged and still starts with the `MG_TIER_R` skip:
- c01 `TestAdv_10_DeterminismLintNoAllowances` is now `TestTierRAdvC01_10_DeterminismLintNoAllowances` (`internal/sim`).
- c03 `TestAdv_7_{MissingMetricInOneOfTwoRuns,NullNumericValue,ReturnedNullViolationRow}` are now `TestTierRAdvC03_7_*` (`internal/obs/csvflush`).
- c03 `TestAdv_11_{ConnectionPeaksAtIrregularPublishes,IdenticalReadCountsAtTiedPublishes,OverloadCountsWithDetailAblation}` are now `TestTierRAdvC03_11_*` (`internal/obs/simrun`).

Split, because the original had an always-on half and a live half:
- c02 `TestAdv_1_{EmptyInsideTransaction,ErrorAfterCommitBoundary,RollbackThenBegin}`. `TestAdv_1_*` keeps the simulator assertion (`c02CheckShape(..., false)`). The new `TestTierRAdvC02_1_*` skips unless `MG_TIER_R=1`, then runs the same simulator assertion and the original live comparison (`c02CheckShape(..., true)` calls `c02LiveCompare`). `c02LiveCompare` lost its inner `MG_TIER_R` check, because the caller now does it. Address variable: `MG_PG_ADDR`, as in the original. In tier-r.sh that is the main container (trust, `max_connections=600`, `pg_stat_statements`), user and database `postgres`.
- c04 `TestAdv_11_{RepeatedSSLAfterRefusal,SSLRequestWithBody,StartupMissingTerminator}`. `TestAdv_11_*` keeps the recorded-oracle half (`c04FidelityAttack(t, kind, false)`, which reads `testdata/adv-c04-oracle-*.json`). The new `TestTierRAdvC04_11_*` skips unless `MG_TIER_R=1` and runs the live half (`c04FidelityAttack(t, kind, true)`). Before the split, setting `MG_TIER_R=1` turned the oracle half into the live half. Now the oracle half always runs.
  - **Variable mapped: `MG_PG_SCRAM_ADDR` to `MG_PG_ADDR`.** The original runner (`agent_work/adversarial/c04-wire-sessions/tier-r.sh`) used a SCRAM container with SSL off. tier-r.sh's SCRAM container has `ssl=on`, and the attacks need a server that refuses SSL: they compare against `TargetSSLOff` and the oracle `N E:FATAL:0A000`.
  - I checked this against a container of tier-r.sh's SCRAM shape. `repeat-ssl` got `Postgres17="S EOF"` and `ssl-with-body` got `"S"`, so both fail; `startup-missing-terminator` passes.
  - The three conversations end before authentication, so the auth method does not matter. The main container (trust, SSL off as in the image default) answers exactly as recorded.
  - With this mapping no live half needs a container shape that tier-r.sh lacks, so nothing is left gated outside tier R.

Verification. `/tmp/zgw-c06a/tier-r-promo.sh` is `scripts/tier-r.sh` byte for byte, except that `root` is fixed and `./...` is replaced by the nine touched packages. It uses the same three containers, the same environment variables, and `go test -p 1 -count=1 -timeout 30m -v -run '^TestTierR'`. Command: `RUN_DIR=/tmp/zgw-c06a/tr-promo /tmp/zgw-c06a/tier-r-promo.sh`. Result: exit 0, "tier-r: 21 passed, 0 failed, 0 skipped" (these packages' existing `TestTierR*` tests are included). All 13 promoted tier R tests ran and passed:
- `TestTierRAdvC01_10_DeterminismLintNoAllowances` (6.84 s)
- `TestTierRAdvC02_1_EmptyInsideTransaction` (0.02 s), `TestTierRAdvC02_1_ErrorAfterCommitBoundary` (0.02 s), `TestTierRAdvC02_1_RollbackThenBegin` (0.01 s)
- `TestTierRAdvC03_7_MissingMetricInOneOfTwoRuns` (1.30 s), `TestTierRAdvC03_7_NullNumericValue` (1.09 s), `TestTierRAdvC03_7_ReturnedNullViolationRow` (0.94 s)
- `TestTierRAdvC03_11_ConnectionPeaksAtIrregularPublishes` (2.03 s), `TestTierRAdvC03_11_IdenticalReadCountsAtTiedPublishes` (1.92 s), `TestTierRAdvC03_11_OverloadCountsWithDetailAblation` (2.10 s)
- `TestTierRAdvC04_11_RepeatedSSLAfterRefusal` (0.02 s), `TestTierRAdvC04_11_SSLRequestWithBody` (0.02 s), `TestTierRAdvC04_11_StartupMissingTerminator` (0.01 s)

Without `MG_TIER_R`, all 13 skip, and the always-on halves `TestAdv_1_*` (c02) and `TestAdv_11_*` (c04) pass. After the change, gofmt is clean, go vet is clean, and `GATE_ONLY=G1,G2` passes (detlint, importlint, boundlint, paniccheck, comments). The containers were removed on exit.

Names. When one package holds attacks from two chunks (`internal/obs`: c01 and c03), the test names get a chunk prefix (`TestAdvC01_...`, `TestAdvC03_...`). Everywhere else the original `TestAdv_<claim>_<Name>` names are kept. The c03 child-process helper `TestReplayWorker` (not an attack) became `TestAdvC03ReplayWorker`. No `Fuzz*` functions exist in these four harnesses.

Placement and lint rules. `internal/obs` may not import the adapters, so c03 attacks that use jsonstats/logline/promtext live in those adapter packages. Adapters may not import `internal/sim/...`, so the c03 scenario attacks (simrun + world) live in `internal/obs/simrun`. detlint runs without `-tests` (scripts/gate.sh `g1_detlint`), so test files in det-packages are not linted for goroutines or clocks. No file has a build tag. Nothing went under `test/`. No go.mod change.

### c01-sim-kernel

| Original (file, test) | Original verdict | Outcome | Promoted to (file, test) | Reason / gating |
| --- | --- | --- | --- | --- |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_1_ProcessReplay` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_1_ProcessReplay` | Held; passes unchanged. Gating: re-executes the test binary (no go build); about 0.1 s. |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_1_RingWrapReplay` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_1_RingWrapReplay` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_1_TraceToggle` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_1_TraceToggle` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_2_HostileLabels` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_2_HostileLabels` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_2_InterleavedConsumption` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_2_InterleavedConsumption` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_2_RecreatedStreams` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_2_RecreatedStreams` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_3_BackgroundHandleCopies` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_3_BackgroundHandleCopies` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_3_CancelInsideTie` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_3_CancelInsideTie` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_3_ExtremeTimeArithmetic` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_3_ExtremeTimeArithmetic` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_3_RefusalRoundTripOverflow` | BROKEN | promoted (regression) | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_3_RefusalRoundTripOverflow` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_4_ClosedReaderFreesBlockedWriter` | BROKEN | promoted (regression) | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_4_ClosedReaderFreesBlockedWriter` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_4_DuplexTinyWindow` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_4_DuplexTinyWindow` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_4_LatencySumSaturates` | BROKEN | promoted (regression) | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_4_LatencySumSaturates` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_4_LongSeedCampaign` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_4_LongSeedCampaign` | Held; passes unchanged. Gating: skips unless ADV_LONG=1 (unchanged: 2,048 extra duplex seeds). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_4_MaximumJitter` | BROKEN | promoted (regression) | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_4_MaximumJitter` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_5_HalfOpenDuringHandshake` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_5_HalfOpenDuringHandshake` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_5_ListenerClearDoesNotReviveOldDial` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_5_ListenerClearDoesNotReviveOldDial` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_5_ResetWhileStalled` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_5_ResetWhileStalled` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_5_StalledFINBothEnds` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_5_StalledFINBothEnds` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_6_ChunkClearRestoresRandomSequence` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_6_ChunkClearRestoresRandomSequence` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_6_ClearBeforeArrival` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_6_ClearBeforeArrival` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_6_FaultedNeighborTraffic` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_6_FaultedNeighborTraffic` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_7_BackgroundMasksNoForeground` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_7_BackgroundMasksNoForeground` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_7_ConnectingLeakWithoutTimers` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_7_ConnectingLeakWithoutTimers` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/network_test.go`, `TestAdv_7_HeldBytesWithoutTimers` | held | promoted | `internal/sim/simnet/adv_c01_promoted_test.go`, `TestAdv_7_HeldBytesWithoutTimers` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_8_AllocationAndHugeRank` | held | promoted | `internal/obs/adv_c01_promoted_test.go`, `TestAdvC01_8_AllocationAndHugeRank` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_8_MergeTreeAndClamps` | held | promoted | `internal/obs/adv_c01_promoted_test.go`, `TestAdvC01_8_MergeTreeAndClamps` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_8_PowerBoundaryError` | held | promoted | `internal/obs/adv_c01_promoted_test.go`, `TestAdvC01_8_PowerBoundaryError` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_9_EmptyAndMaximumSchedule` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_9_EmptyAndMaximumSchedule` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_9_NonmonotonePredicate` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_9_NonmonotonePredicate` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/kernel_test.go`, `TestAdv_9_OrderedDuplicateTriggers` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_9_OrderedDuplicateTriggers` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/determinism_test.go`, `TestAdv_10_AliasedEntropyAndClock` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_10_AliasedEntropyAndClock` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/determinism_test.go`, `TestAdv_10_ConcurrencyAndFloatingSyntax` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestAdv_10_ConcurrencyAndFloatingSyntax` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c01-sim-kernel/determinism_test.go`, `TestAdv_10_DeterminismLintNoAllowances` | held | promoted | `internal/sim/adv_c01_promoted_test.go`, `TestTierRAdvC01_10_DeterminismLintNoAllowances` | Held; passes unchanged. Gating: renamed TestTierRAdvC01_10_...; runs in the gate tier R stage (scripts/tier-r.sh), skips unless MG_TIER_R=1 (runs /usr/local/go/bin/go run in tools/detlint; about 7-16 s). |

Totals for c01-sim-kernel: 34 attacks in RESULTS.md; promoted 34 (held 30, regression 4); left behind 0.

### c02-sim-world

| Original (file, test) | Original verdict | Outcome | Promoted to (file, test) | Reason / gating |
| --- | --- | --- | --- | --- |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_1_EmptyInsideTransaction` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_1_EmptyInsideTransaction` + `internal/sim/world/adv_c02_promoted_test.go`, `TestTierRAdvC02_1_EmptyInsideTransaction` | Held; passes unchanged. Gating: split: TestAdv_1_ is the simulator half (always runs); TestTierRAdvC02_1_ is the same simulator assertion plus the live Postgres comparison, in the tier R stage (MG_TIER_R=1, MG_PG_ADDR = tier-r.sh main container, trust; same variable as the original). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_1_ErrorAfterCommitBoundary` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_1_ErrorAfterCommitBoundary` + `internal/sim/world/adv_c02_promoted_test.go`, `TestTierRAdvC02_1_ErrorAfterCommitBoundary` | Held; passes unchanged. Gating: split as above: live half is TestTierRAdvC02_1_..., tier R stage, MG_PG_ADDR. |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_1_RollbackThenBegin` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_1_RollbackThenBegin` + `internal/sim/world/adv_c02_promoted_test.go`, `TestTierRAdvC02_1_RollbackThenBegin` | Held; passes unchanged. Gating: split as above: live half is TestTierRAdvC02_1_..., tier R stage, MG_PG_ADDR. |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_2_AbortedTransactionEmptyQuery` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_2_AbortedTransactionEmptyQuery` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_2_ImplicitRollbackOfDeleteInsert` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_2_ImplicitRollbackOfDeleteInsert` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_2_SnapshotAcrossDeleteReinsert` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_2_SnapshotAcrossDeleteReinsert` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_2_StatementLimitErrorAbortsTransaction` | BROKEN | promoted (regression) | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_2_StatementLimitErrorAbortsTransaction` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_3_DeleteReinsertCommitOrder` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_3_DeleteReinsertCommitOrder` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_3_FullTableCommitMustNotLogPhantom` | BROKEN | promoted (regression) | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_3_FullTableCommitMustNotLogPhantom` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_3_RingWrapPreservesSequence` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_3_RingWrapPreservesSequence` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_4_CancelAtFullCapacity` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_4_CancelAtFullCapacity` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_4_DeadEndsDoNotStealReleasedSlot` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_4_DeadEndsDoNotStealReleasedSlot` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_4_StartupReservations` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_4_StartupReservations` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_5_FreezeClearWithQueuedBackends` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_5_FreezeClearWithQueuedBackends` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_5_RefuseRestartClearInterleaving` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_5_RefuseRestartClearInterleaving` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_5_StallHitsAlreadyServing` | BROKEN | promoted (regression) | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_5_StallHitsAlreadyServing` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_6_ClearArmedHalfMessage` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_6_ClearArmedHalfMessage` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_6_ResetQueuedClientReleasesWaiter` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_6_ResetQueuedClientReleasesWaiter` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_6_StopReadThenCancelThenClear` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_6_StopReadThenCancelThenClear` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_7_BacklogCapCountsEveryArrival` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_7_BacklogCapCountsEveryArrival` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_7_LatencyIncludesStartupWait` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_7_LatencyIncludesStartupWait` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_7_SendWindowIndependentOfService` | BROKEN | left behind | - | Skips today: claim 7 NARROWED by the orchestrator on 2026-11-25 (arrivals follow the schedule; latency measured from the intended time; the wire send waits for the previous reply). agent_work/chunks/c02-sim-world/ADVERSARIAL.md. |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_8_CancelQueuedBackendOnly` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_8_CancelQueuedBackendOnly` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_8_OldKeyAfterBackendReplacement` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_8_OldKeyAfterBackendReplacement` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_8_RepeatCancelDoesNotPoisonNextStatement` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_8_RepeatCancelDoesNotPoisonNextStatement` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_9_CrossProcess` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_9_CrossProcess` | Held; passes unchanged. Gating: re-executes the test binary (no go build); about 0.2 s, runs under -short. |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_9_ExtremeSeedBaselineReplay` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_9_ExtremeSeedBaselineReplay` | Held; passes unchanged. Gating: skipped under -short (about 12 s). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_9_RandomFaultRecovery` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_9_RandomFaultRecovery` | Held; passes unchanged. Gating: ADV_LONG=1 widens 12 to 300 seeds (unchanged). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_10_500ConnectionsUnpinnedSeeds` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_10_500ConnectionsUnpinnedSeeds` | Held; passes unchanged. Gating: skipped under -short (about 1.5 s). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_10_BurstUnpinnedSeeds` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_10_BurstUnpinnedSeeds` | Held; passes unchanged. Gating: skipped under -short (about 1.8 s). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_10_IdenticalReadsUnpinnedSeeds` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_10_IdenticalReadsUnpinnedSeeds` | Held; passes unchanged. Gating: skipped under -short (about 1.2 s). |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_11_AllGoImportsIncludingTests` | BROKEN | left behind | - | Skips today: claim 11 NARROWED on 2026-11-25 (holds for every Go file outside tools/; the import lint fixtures must import pgproto3). agent_work/chunks/c02-sim-world/ADVERSARIAL.md. |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_11_ModuleRequirements` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_11_ModuleRequirements` | Held; passes unchanged. Gating: runs `go mod edit -json` from PATH (go test puts GOROOT/bin first); about 0.03 s. |
| `agent_work/adversarial/c02-sim-world/adv_test.go`, `TestAdv_11_NoReplacementOrWorkspaceEscape` | held | promoted | `internal/sim/world/adv_c02_promoted_test.go`, `TestAdv_11_NoReplacementOrWorkspaceEscape` | Held; passes unchanged. Gating: runs `go env GOWORK` from PATH; about 0.02 s. |

Totals for c02-sim-world: 34 attacks in RESULTS.md; promoted 32 (held 29, regression 3); left behind 2.

### c03-obs-spine

| Original (file, test) | Original verdict | Outcome | Promoted to (file, test) | Reason / gating |
| --- | --- | --- | --- | --- |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_1_AdapterDependencyBoundary` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_1_AdapterDependencyBoundary` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_1_ComponentDependencyBoundary` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_1_ComponentDependencyBoundary` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_1_SnapshotHasNoLiveReferences` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_1_SnapshotHasNoLiveReferences` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_2_CollectorEmissionPermutation` | held | promoted | `internal/obs/jsonstats/adv_c03_promoted_test.go`, `TestAdv_2_CollectorEmissionPermutation` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_2_EqualInputsAfterHostileNoise` | held | promoted | `internal/obs/jsonstats/adv_c03_promoted_test.go`, `TestAdv_2_EqualInputsAfterHostileNoise` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_2_PrometheusHistoryIndependence` | BROKEN | left behind | - | Skips today: claim 2 narrowed (adapter output is a deterministic function of its whole input history; Prometheus tenant eviction depends on earlier publishes). agent_work/chunks/c03-obs-spine/ADVERSARIAL.md. |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_3_CRLFLabelFidelity` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_3_CRLFLabelFidelity` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_3_InterleavedInstancesAndQuietRows` | held | promoted | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_3_InterleavedInstancesAndQuietRows` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_3_ZeroMetricWithNonTenantLabels` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_3_ZeroMetricWithNonTenantLabels` | Regression. RESULTS judged it over-broad; ADVERSARIAL.md lists it among the 10 fixed defects ("zero rows for metrics with non-tenant labels"). Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_4_DuplicateScalarCorruption` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_4_DuplicateScalarCorruption` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_4_HistogramUnitCorruption` | held | promoted | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_4_HistogramUnitCorruption` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_4_PhantomTenantCorruption` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_4_PhantomTenantCorruption` | Regression. RESULTS judged it over-broad; ADVERSARIAL.md lists "fabricated CSV rows passing the round trip" as fixed. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/scenario_test.go`, `TestAdv_5_DetailOffCrossProcess` | held | promoted | `internal/obs/simrun/adv_c03_promoted_test.go`, `TestAdv_5_DetailOffCrossProcess` | Held; passes unchanged. Gating: skipped under -short (about 1 s); helper TestAdvC03ReplayWorker. |
| `agent_work/adversarial/c03-obs-spine/scenario_test.go`, `TestAdv_5_OverloadCrossProcess` | held | promoted | `internal/obs/simrun/adv_c03_promoted_test.go`, `TestAdv_5_OverloadCrossProcess` | Held; passes unchanged. Gating: skipped under -short (about 1 s); helper TestAdvC03ReplayWorker. |
| `agent_work/adversarial/c03-obs-spine/scenario_test.go`, `TestAdv_5_TiedPublishCrossProcess` | held | promoted | `internal/obs/simrun/adv_c03_promoted_test.go`, `TestAdv_5_TiedPublishCrossProcess` | Held; passes unchanged. Gating: skipped under -short (two child processes of the test binary, about 1 s); helper TestAdvC03ReplayWorker. |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_6_AsyncDiskErrorRecoveryBoundary` | held | promoted | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_6_AsyncDiskErrorRecoveryBoundary` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_6_FirstFlushHeaderCap` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_6_FirstFlushHeaderCap` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_6_FullQueueWithBlockedFilesystem` | held | promoted | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestAdv_6_FullQueueWithBlockedFilesystem` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_7_MissingMetricInOneOfTwoRuns` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestTierRAdvC03_7_MissingMetricInOneOfTwoRuns` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: renamed TestTierRAdvC03_7_...; tier R stage, skips unless MG_TIER_R=1 (scripts/csv-assert.sh with DuckDB). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_7_NullNumericValue` | BROKEN | promoted (regression) | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestTierRAdvC03_7_NullNumericValue` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: renamed TestTierRAdvC03_7_...; tier R stage (DuckDB). |
| `agent_work/adversarial/c03-obs-spine/csv_test.go`, `TestAdv_7_ReturnedNullViolationRow` | held | promoted | `internal/obs/csvflush/adv_c03_promoted_test.go`, `TestTierRAdvC03_7_ReturnedNullViolationRow` | Held; passes unchanged. Gating: renamed TestTierRAdvC03_7_...; tier R stage (DuckDB). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_8_CounterGaugeFamilyCollision` | BROKEN | left behind | - | Skips today: settled another way (the registry now refuses declarations whose Prometheus names collide, so the attack setup is rejected). agent_work/chunks/c03-obs-spine/ADVERSARIAL.md. |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_8_HistogramGaugeSeriesCollision` | BROKEN | left behind | - | Skips today: settled another way (the registry now refuses colliding Prometheus names, so the attack setup is rejected). agent_work/chunks/c03-obs-spine/ADVERSARIAL.md. |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_8_HostileTenantCampaign` | held | promoted | `internal/obs/promtext/adv_c03_promoted_test.go`, `TestAdv_8_HostileTenantCampaign` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_9_NegativeHighWaterAndEmptyIdentity` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_9_NegativeHighWaterAndEmptyIdentity` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_9_SparseBucketPartitions` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_9_SparseBucketPartitions` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_9_SumOverflow` | BROKEN | promoted (regression) | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_9_SumOverflow` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_10_CounterExtremeRecordingAllocations` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_10_CounterExtremeRecordingAllocations` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_10_DisabledConstructionAndSnapshots` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_10_DisabledConstructionAndSnapshots` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_10_HistogramClampAllocations` | held | promoted | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_10_HistogramClampAllocations` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/scenario_test.go`, `TestAdv_11_ConnectionPeaksAtIrregularPublishes` | held | promoted | `internal/obs/simrun/adv_c03_promoted_test.go`, `TestTierRAdvC03_11_ConnectionPeaksAtIrregularPublishes` | Held; passes unchanged. Gating: renamed TestTierRAdvC03_11_...; tier R stage (DuckDB; about 2-3 s); ADV_LONG=1 widens seeds (unchanged). |
| `agent_work/adversarial/c03-obs-spine/scenario_test.go`, `TestAdv_11_IdenticalReadCountsAtTiedPublishes` | held | promoted | `internal/obs/simrun/adv_c03_promoted_test.go`, `TestTierRAdvC03_11_IdenticalReadCountsAtTiedPublishes` | Held; passes unchanged. Gating: renamed TestTierRAdvC03_11_...; tier R stage (DuckDB; about 2-3 s). |
| `agent_work/adversarial/c03-obs-spine/scenario_test.go`, `TestAdv_11_OverloadCountsWithDetailAblation` | held | promoted | `internal/obs/simrun/adv_c03_promoted_test.go`, `TestTierRAdvC03_11_OverloadCountsWithDetailAblation` | Held; passes unchanged. Gating: renamed TestTierRAdvC03_11_...; tier R stage (DuckDB; about 2-3.5 s). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_12_RuntimeDeclarationDocumentationGap` | BROKEN | left behind | - | Skips today: claim 12 narrowed (documentation enforced for every source listed in the catalog; a run-time source outside it is declared but undocumented). agent_work/chunks/c03-obs-spine/ADVERSARIAL.md. |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_12_UndeclaredHistogramMerge` | BROKEN | promoted (regression) | `internal/obs/adv_c03_promoted_test.go`, `TestAdvC03_12_UndeclaredHistogramMerge` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c03-obs-spine/core_test.go`, `TestAdv_12_UndeclaredSingletonMerge` | BROKEN | promoted (regression) | `internal/obs/jsonstats/adv_c03_promoted_test.go`, `TestAdv_12_UndeclaredSingletonMerge` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |

Totals for c03-obs-spine: 36 attacks in RESULTS.md; promoted 32 (held 22, regression 10); left behind 4.

### c04-wire-sessions

| Original (file, test) | Original verdict | Outcome | Promoted to (file, test) | Reason / gating |
| --- | --- | --- | --- | --- |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_1_BackendParameterReplacementPartitions` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_1_BackendParameterReplacementPartitions` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_1_ClientFragmentedDuplicateParameters` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_1_ClientFragmentedDuplicateParameters` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_1_CycleRandomPartitions` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_1_CycleRandomPartitions` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_2_HeaderLengthExtremes` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_2_HeaderLengthExtremes` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_2_InvalidHandshakeEnums` | BROKEN | promoted (regression) | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_2_InvalidHandshakeEnums` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_2_ScramMessageBound` | BROKEN | promoted (regression) | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_2_ScramMessageBound` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_2_SeededHostileCampaign` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_2_SeededHostileCampaign` | Held; passes unchanged. Gating: ADV_LONG=1 widens 100 to 50,000 seeds (unchanged). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_2_WireSCRAMBound` | BROKEN | promoted (regression) | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_2_WireSCRAMBound` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_3_BackendCoalescedTLSBytes` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_3_BackendCoalescedTLSBytes` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_3_EarlyByteAtEveryRequestCut` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_3_EarlyByteAtEveryRequestCut` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_3_HandshakeReplayCannotUpgradeTwice` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_3_HandshakeReplayCannotUpgradeTwice` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_4_AllowIndependentModes` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_4_AllowIndependentModes` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_4_OffCoalescedGSSSSLStartup` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_4_OffCoalescedGSSSSLStartup` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_4_RequireCannotBeBypassedByGSSTrain` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_4_RequireCannotBeBypassedByGSSTrain` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_5_ChannelBindingSubstitution` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_5_ChannelBindingSubstitution` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_5_ProofReplayAcrossNonces` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_5_ProofReplayAcrossNonces` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_5_UnknownUserIterationOracle` | BROKEN | promoted (regression) | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_5_UnknownUserIterationOracle` | Regression. RESULTS judged it an over-broad claim; claim 5 was narrowed, but the product was also changed (the unknown-user mock challenge uses the table's most common iteration count), and the attack passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_6_AuthOKBeforeServerProof` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_6_AuthOKBeforeServerProof` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_6_MechanismOrderAndDuplicates` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_6_MechanismOrderAndDuplicates` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_6_SignatureBitFlips` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_6_SignatureBitFlips` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_7_BackendParameterFloodThenFaults` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_7_BackendParameterFloodThenFaults` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_7_ClientFaultOrderConservation` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_7_ClientFaultOrderConservation` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_7_HandshakeDeadlineClassification` | BROKEN | promoted (regression) | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_7_HandshakeDeadlineClassification` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_8_AllUnknownTypesAndStatuses` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_8_AllUnknownTypesAndStatuses` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_8_LargePartialUnknownNoBuffer` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_8_LargePartialUnknownNoBuffer` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_8_UnsolicitedBetweenReadyAndNextQuery` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_8_UnsolicitedBetweenReadyAndNextQuery` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_9_ClosedSessionsDoNotRecycleKeys` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_9_ClosedSessionsDoNotRecycleKeys` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_9_ConstantRandomSingleIssuer` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_9_ConstantRandomSingleIssuer` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_9_IndependentIssuerCollision` | BROKEN | left behind | - | Skips today: claim 9 narrowed (key data unique among the sessions of one issuer). agent_work/chunks/c04-wire-sessions/ADVERSARIAL.md. |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_10_NoGoroutinesOrClockReads` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_10_NoGoroutinesOrClockReads` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_10_NoIOImports` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_10_NoIOImports` | Held; passes unchanged. Gating: none (runs under -short). |
| `agent_work/adversarial/c04-wire-sessions/attacks_test.go`, `TestAdv_10_ProcessReplay` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_10_ProcessReplay` | Held; passes unchanged. Gating: re-executes the test binary (no go build); about 0.1 s. |
| `agent_work/adversarial/c04-wire-sessions/fidelity_test.go`, `TestAdv_11_RepeatedSSLAfterRefusal` | BROKEN | promoted (regression) | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_11_RepeatedSSLAfterRefusal` + `internal/pgwire/adv_c04_promoted_test.go`, `TestTierRAdvC04_11_RepeatedSSLAfterRefusal` | Regression: originally BROKEN, fixed in the repair round; passes unchanged. Gating: split: TestAdv_11_ compares the fake with the recorded Postgres 17.11 oracle internal/pgwire/testdata/adv-c04-oracle-repeat-ssl.json (always runs); TestTierRAdvC04_11_ is the live half in the tier R stage, address variable mapped MG_PG_SCRAM_ADDR to MG_PG_ADDR (see notes; ADV_RECORD=1 rewrites the oracle). |
| `agent_work/adversarial/c04-wire-sessions/fidelity_test.go`, `TestAdv_11_SSLRequestWithBody` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_11_SSLRequestWithBody` + `internal/pgwire/adv_c04_promoted_test.go`, `TestTierRAdvC04_11_SSLRequestWithBody` | Held; passes unchanged. Gating: split: oracle half TestAdv_11_ (testdata/adv-c04-oracle-ssl-with-body.json); live half TestTierRAdvC04_11_, tier R stage, MG_PG_ADDR (mapped from MG_PG_SCRAM_ADDR). |
| `agent_work/adversarial/c04-wire-sessions/fidelity_test.go`, `TestAdv_11_StartupMissingTerminator` | held | promoted | `internal/pgwire/adv_c04_promoted_test.go`, `TestAdv_11_StartupMissingTerminator` + `internal/pgwire/adv_c04_promoted_test.go`, `TestTierRAdvC04_11_StartupMissingTerminator` | Held; passes unchanged. Gating: split: oracle half TestAdv_11_ (testdata/adv-c04-oracle-startup-missing-terminator.json); live half TestTierRAdvC04_11_, tier R stage, MG_PG_ADDR (mapped from MG_PG_SCRAM_ADDR). |

Totals for c04-wire-sessions: 35 attacks in RESULTS.md; promoted 34 (held 28, regression 6); left behind 1.

### Totals

| Chunk | Attacks in RESULTS.md | Promoted (held) | Promoted (regression) | Left behind |
| --- | --- | --- | --- | --- |
| c01-sim-kernel | 34 | 30 | 4 | 0 |
| c02-sim-world | 34 | 29 | 3 | 2 |
| c03-obs-spine | 36 | 22 | 10 | 4 |
| c04-wire-sessions | 35 | 28 | 6 | 1 |
| **All** | **139** | **109** | **23** | **7** |

That is 132 promoted attacks plus one non-attack helper (`TestAdvC03ReplayWorker`). The six split attacks (c02 `TestAdv_1_*`, c04 `TestAdv_11_*`) each have two functions, so the new files hold 139 test functions: 126 `TestAdv*` and 13 `TestTierRAdv*`. In the original run, 131 attacks plus `TestReplayWorker` passed and 8 skipped. The skips were the 7 left behind and c01 `TestAdv_4_LongSeedCampaign`, which skips only for `ADV_LONG` and is promoted with that gate kept.

### Files created (all new; no existing file edited)

- `internal/sim/adv_c01_promoted_test.go` (package `sim_test`; 16 attacks, one of them `TestTierRAdvC01_10_*`)
- `internal/sim/simnet/adv_c01_promoted_test.go` (package `simnet_test`; 15 attacks)
- `internal/obs/adv_c01_promoted_test.go` (package `obs_test`; 3 attacks, `TestAdvC01_8_*`)
- `internal/sim/world/adv_c02_promoted_test.go` (package `world_test`; 32 attacks, 3 of them split into `TestAdv_1_*` + `TestTierRAdvC02_1_*`)
- `internal/obs/adv_c03_promoted_test.go` (package `obs_test`; 10 attacks, `TestAdvC03_*`)
- `internal/obs/jsonstats/adv_c03_promoted_test.go` (package `jsonstats_test`; 3 attacks)
- `internal/obs/promtext/adv_c03_promoted_test.go` (package `promtext_test`; 1 attack)
- `internal/obs/csvflush/adv_c03_promoted_test.go` (package `csvflush_test`; 12 attacks, 3 of them `TestTierRAdvC03_7_*`)
- `internal/obs/simrun/adv_c03_promoted_test.go` (package `simrun_test`; 6 attacks, 3 of them `TestTierRAdvC03_11_*`, + `TestAdvC03ReplayWorker`)
- `internal/pgwire/adv_c04_promoted_test.go` (package `pgwire_test`; 34 attacks, 3 of them split into `TestAdv_11_*` + `TestTierRAdvC04_11_*`)
- `internal/pgwire/testdata/adv-c04-oracle-repeat-ssl.json`, `adv-c04-oracle-ssl-with-body.json`, `adv-c04-oracle-startup-missing-terminator.json` (byte copies of the c04 Postgres 17.11 oracle records)

### Timings (this host, Go on /usr/local/go/bin, 2026-11-27; the builder was running tests at the same time, so times are inflated)

Promoted tests only (`-run TestAdv`):

| Package | full | -short |
| --- | --- | --- |
| internal/sim | 1.247 s | 0.682 s |
| internal/sim/simnet | 0.293 s | 0.087 s |
| internal/obs | 0.118 s | 0.109 s |
| internal/sim/world | 19.772 s | 0.534 s |
| internal/obs/jsonstats | 0.040 s | 0.092 s |
| internal/obs/promtext | 0.050 s | 0.035 s |
| internal/obs/csvflush | 0.056 s | 0.068 s |
| internal/obs/simrun | 4.470 s | 0.013 s |
| internal/pgwire | 0.743 s | 0.335 s |
| **sum** | **about 26.8 s** | **about 2.0 s** (package elapsed, binaries cached) |

First pass, before the tier R renames, with `MG_TIER_R=1` (no Docker needed): `TestAdv_10_DeterminismLintNoAllowances` took 15.7 s, csvflush `TestAdv_7_*` 3.0 s and simrun `TestAdv_11_*` 9.6 s, all passing. The live Postgres halves also passed then (a scratch runner, not kept). For the current `TestTierRAdv*` names and times, see "Tier R stage (follow-up)" above.

Whole packages (`go test -count=1`, existing tests included): sim 4.458 s, simnet 5.141 s, obs 3.323 s, world 39.549 s, jsonstats 0.058 s, promtext 3.429 s, csvflush 3.466 s, simrun 586.526 s, pgwire 23.217 s. All ok.

Whole packages `-short`: sim 1.317 s, simnet 0.843 s, obs 0.498 s, world 19.498 s, jsonstats 0.067 s, promtext 4.225 s, csvflush 3.100 s, pgwire 18.430 s, all ok. simrun failed then (502 s) in `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` on two loadgen metrics declared and not yet emitted by a baseline run; the builder fixed that (`internal/obs/simrun/loadgen_test.go`), and the 7 promoted simrun tests passed or skipped in that run. The earlier full (non-short) simrun run passed. Race: `go test -race -count=1 -run TestAdv ./internal/obs/csvflush` ok (1.1 s).

### Checks run

- `gofmt -l` on the new files: nothing. `go vet` on all nine packages: clean.
- `GATE_ONLY=G0,G1,G2` with the output outside the tree: detlint, importlint, boundlint, paniccheck and G2 comments all pass. G0 build, vet, tidy and tools pass. The one finding then was G0 gofmt on `internal/loadgen/tracker.go`, a builder file in progress (fixed). Because the output went to /tmp, no `agent_work/chunks/c06a-operator-tools/gate/result.json` was written.
- Mutation sanity: in a scratch copy of the tree, I reverted each fix temporarily and confirmed that the promoted regression test fails:
  - `simnet/net.go`: plain `+` instead of `sim.AddDurations` makes `TestAdv_3_RefusalRoundTripOverflow` fail ("refusal at 0, want 9223372036854775807").
  - `simnet/conn.go` `delay`: plain `+` makes `TestAdv_4_LatencySumSaturates` fail.
  - `simnet/conn.go` `jitter`: disabling the MaxInt64 guard makes `TestAdv_4_MaximumJitter` fail ("panicked: invalid argument to Int64N").
  - `pgwire/client.go`: removing the `HandshakeDeadline` case makes `TestAdv_7_HandshakeDeadlineClassification` fail ("counted as tls-failed").
  - `obs/merge.go` `addChecked`: disabling the overflow check makes `TestAdvC03_9_SumOverflow` fail ("silently wrapped").
  - `fakepg/exec.go`: disabling both table-cap checks and ignoring `put`'s error makes `TestAdv_3_FullTableCommitMustNotLogPhantom` fail ("log claims applied write ... b=2").
  - The real worktree was never mutated: `git diff --stat` lists only the builder's Makefile and scripts changes.

### Commands to run the promoted tests (from the repo root, `export PATH=/usr/local/go/bin:$PATH`)

```sh
P="./internal/sim ./internal/sim/simnet ./internal/obs ./internal/sim/world ./internal/obs/jsonstats ./internal/obs/promtext ./internal/obs/csvflush ./internal/obs/simrun ./internal/pgwire"
go test -count=1 -run 'TestAdv' $P                      # default set (slow ones included; tier R ones skip)
go test -short -count=1 -run 'TestAdv' $P               # CI set
scripts/tier-r.sh                                        # gate tier R stage: runs every TestTierR*, the 13 TestTierRAdv* included
ADV_LONG=1 go test -count=1 -run 'TestAdv_4_LongSeedCampaign|TestAdv_9_RandomFaultRecovery|TestAdv_2_SeededHostileCampaign' ./internal/sim/simnet ./internal/sim/world ./internal/pgwire
```

By hand, outside tier-r.sh, `TestTierRAdvC01_*` and `TestTierRAdvC03_*` need only `MG_TIER_R=1` (and DuckDB 1.5.x for c03). `TestTierRAdvC02_1_*` and `TestTierRAdvC04_11_*` also need `MG_PG_ADDR` pointing at a postgres:17 with trust auth and SSL off (tier-r.sh's main container). Without it they fail fatally ("requires MG_PG_ADDR" / "MG_PG_ADDR required").

## Images on vm-1

No pull was needed: `prom/prometheus:v3.9.1` was already on vm-1, and the Postgres reference
`postgres:17.11@sha256:2d2b8998…` resolves by digest to the local `postgres:17` image (17.11),
the one tier R uses. `make run-up` passes `--quiet-pull`; on a machine without the images the
first `run-up` pulls them.

## CI time and host load

- `scripts/ci-fast.sh` on vm-1 at load 14–18 (three builds and their gates running): 1,072 s
  (G3 unit 974 s). Most of it is pre-existing: `internal/obs/simrun` alone took 744 s under
  `-short` (its DuckDB tests, 515 s in `TestCSVAssertBaselineCountsFromFlushedFiles`, run when
  `duckdb` is installed; GitHub's runner has no DuckDB and skips them). The last CI runs on
  GitHub took 1 min to 1 min 40 s.
- This chunk's additions under `-short`, measured in that run: `internal/loadgen` 6.5 s,
  `internal/loadgen/live` 10.9 s (real sockets and real time), `cmd/loadgen` 3.6 s,
  `internal/mgctl` 2.3 s, `cmd/mgctl` 0.3 s, the mgctl tier S model about 3 s (2 seeds under
  `-short`), the script tests about 1.6 s, the promoted attacks about 2 s. The DuckDB report
  tests, the binary-building tests and the slow promoted attacks skip under `-short`.
- A full `go test -count=1 ./...` at load 14 failed twice in chunk 5's timing tests
  (`internal/pgwire` `TestLexOperatorRunsGrowLinearly`, `TestClassifierTimeGrowsLinearly`: a
  growth ratio over their bound); they pass when run alone. Host load, not this chunk.

## Gate runs

1. Full gate at `e6aefbf`: every stage green except G6, where `TestTierRRunTargetsUpLoadDown`
   failed: one send of the 200 was 31 ms late on the loaded host, which is a SHORTFALL, so
   `make load` exited 3. Fixed in `c193882` (`make load` forwards `LOAD_ARGS` and `CONNS`; the
   smoke test runs with `-late-after 2s`, because what it proves is Prometheus's count, not the
   host's timer latency). In that run: G3 498 s, G4 742 s (200 seeds, concise output), G5 405 s
   (16 targets, 14 fuzz, 2 seed sweeps — none added here), G6 37 of 38 passed, G7 1,823 s
   (4,270 run directories, 195 SQL files, 0 failures), G8, G9, G10 167 s green.
2. Full gate at `c193882` (clean tree): every stage green except G3 unit, where chunk 5's
   timing test `internal/pgwire` `TestClassifierSpanKeywordChecksGrowLinearly` measured a
   growth ratio of 108 against its bound of 96 (`"pg_settings, "`: 4 KiB in 110 µs, 64 KiB in
   12 ms) at host load 20+. G4 586 s, G5 361 s, G6 green (the smoke test included), G7 1,310 s
   (195 SQL files, 0 failures), G8–G10 green. This chunk changes nothing in `internal/pgwire`
   but adds test files (no `t.Parallel`).
3. `GATE_ONLY=G3` at the same commit `c193882`, load 12–14: G3 unit and race green (398 s).
   So every stage has passed on `c193882`; no single full run did, because of that load-
   sensitive pre-existing timing test. The orchestrator's landing gate will run it again.
   `gate/result.json` was deleted (untracked), as the rules ask.

## Commands

```
go test ./internal/loadgen/... ./cmd/loadgen ./internal/mgctl ./cmd/mgctl
go test -run 'Mgctl' ./test/e2e                                   # the mgctl model, tier S
go test -race ./internal/loadgen/...
MG_TIER_R=1 go test -run 'TestTierRMgctl|TestTierRRunTargets' ./test/e2e   # tier R (Docker)
make run-up; make load RATE=200 DURATION=60s PROFILE=errors SEED=1; make run-down
go run ./cmd/loadgen -plan 10 -seed 7 -profile errors
go run ./cmd/mgctl -admin 127.0.0.1:56433 stats -watch -match requests_
scripts/gate.sh c06a-operator-tools
```

## Size

Product Go (non-test files): `cmd/loadgen` 257, `cmd/mgctl` 324, `internal/loadgen` 831,
`internal/loadgen/live` 397, `internal/loadgen/loadgentest` 216 (a test fake, non-test file so
other packages' tests can import it), `internal/mgctl` 792 — 2,817 lines, against the brief's
~1,300 (within the +2,000 allowance; complete, so no split). Plus 3 lines in
`internal/obs/catalog`.

Test Go: those packages' tests 1,672; `test/e2e/c06a_*` , `internal/obs/simrun/loadgen_test.go`,
`test/analysis/report/report_test.go`, `scripts/c06a_report_scripts_test.go` 1,854; the promoted
attacks 3,577. Scripts and SQL: `scripts/report.sh`, `test/analysis/report/sections/*.sql` and
`deploy/dev/*` 948 lines; `sweep.sh`, `chaos.sh` and the Makefile +146 / −9.
