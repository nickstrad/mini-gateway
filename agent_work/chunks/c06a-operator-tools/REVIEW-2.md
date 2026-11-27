VERDICT: PASS

## Findings

None.

## Review 1 findings and attacks — status

- **Review 1 #1 (MAJOR, `internal/mgctl/contract.go`) — FIXED:** `bestMessageRow`/`messageRank` prefers matching severity, exact message, and longest matching prefix (`f16632a`), and `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage` now requires `m.Row.ID == r.ID`; verified by mutation M3 (caught by `TestALongerRowMessageWinsOverItsPrefixAndSeverityDecides`, `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage`, and `TestAdv_9_FatalWithoutReady`).
- **Review 1 #2 (MAJOR, `internal/loadgen/tracker.go` / tests) — FIXED:** `TestLatencyIsMeasuredFromTheIntendedSendTimeNotTheActualSend` (`67703fb`) exercises 400 ms send lag with 1 ms reply time and asserts `Latency` reflects the lag while `LatencyFromSend` does not; verified by mutation M1 (caught).
- **Review 1 #3 (MAJOR, `internal/loadgen/tracker.go` / tests) — FIXED:** `TestLateSendsAloneAreAShortfall` and `cmd/loadgen` `TestLateSendsAloneExitThreeWithTheShortfallLine` (`67703fb`) assert late-only shortfall, achieved rate, `SHORTFALL:` line, and exit 3; verified by mutation M2 (caught in both packages).
- **Review 1 #4 (MAJOR, `test/analysis/report/sections/check-loadgen-labels.sql`) — FIXED:** `check-loadgen-labels.sql` now accepts `reason = 'other'` while rejecting malformed labels (`ea337d6`); verified by mutation M4 (caught by `TestReportLoadLabelsAcceptOtherAndRefuseMalformedClasses`).
- **Review 1 #5 (MINOR, `test/e2e/c06a_tierr_test.go`) — FIXED:** `freshEngineCounter` (`81d17ea`) polls `/stats` every 20 ms up to 15 s for `s.Seq > seen` instead of sleeping 250 ms; verified sound because `Shell.publish()` runs synchronously on the single-threaded engine goroutine (`internal/shell/engine.go:26`, `internal/shell/admin.go:83-87`).
- **Review 1 #6 (MINOR, `internal/loadgen/plan.go`, `internal/loadgen/live/live.go`, `cmd/loadgen/main.go`) — FIXED:** `Plan.Validate()` rejects `p.Count() < 1` naming `-rate` and `-duration`, and `live.Result.FlushErr`/`FlushErrs` cause `cmd/loadgen` to print to stderr and exit 1 even under `-quiet` (`b2c4ca1`); verified by mutations M5 and M6 (both caught).
- **Review 1 #7 (MINOR, `internal/obs/csvflush/adv_c03_promoted_test.go`) — FIXED:** deadlock guards and poll deadlines widened to `c03GuardDeadline = 30 * time.Second` (`81d17ea`) without altering assertions; `go test -count=1 -run 'TestAdv' ./internal/obs/csvflush` and `TestAdv_14_OriginalAssertionsRetained` both pass.
- **Attack `TestAdv_3_ZeroIntendedShortfall` — SETTLED:** `Plan.Validate()` now refuses zero-operation plans (`b2c4ca1`), so the attack's input cannot be constructed; skipped by the orchestrator (`c39839b`) with pointer to `agent_work/chunks/c06a-operator-tools/ADVERSARIAL.md`; refusal verified by mutation M5.
- **Attack `TestAdv_3_OverflowedLatencyFormatting` — FIXED:** `tenthsOf(ns, unit)` divides before multiplying (`b2c4ca1`) so `FormatNs(math.MaxInt64)` returns `"9223372036.8s"`; verified by mutation M7 (caught by `TestTheLargestDurationFormatsWithoutOverflow` and `TestAdv_3_OverflowedLatencyFormatting`).
- **Attack `TestAdv_8_LongStdinPassword` — FIXED:** shared `mgctl.ReadPasswordLine` (`60385c4`) used by `mgctl verifier`, `mgctl query`, and `loadgen` refuses lines over 1,024 bytes (after trailing `\r`) with exit 2 instead of truncating; verified by mutation M8 (caught by `internal/mgctl`, `cmd/mgctl`, `cmd/loadgen`, and `TestAdv_8_LongStdinPassword`).
- **Attack `TestAdv_9_FatalWithoutReady` — FIXED:** resolved by `f16632a` (same fix as Review 1 #1); verified by mutation M3 (caught).
- **Attack `TestAdv_10_EmptySiblingRun` — FIXED:** `scripts/report.sh` `structure: runs` now requires at least one run directory at or below every directory argument (`ea337d6`); verified by mutation M9 (caught by `TestReportEmptyArgumentBesideAGoodRunFails` and `TestAdv_10_EmptySiblingRun`).
- **Attack `TestAdv_10_WholeFinalFlushMissing` — FIXED:** `test/analysis/report/sections/flushes.sql` runs with `guard.sql` and checks `count(DISTINCT flush_seq) <> r.flushes` (`ea337d6`, matching `csvflush.Reader`); verified by mutation M10 (caught by `TestReportMissingWholeFlushFails` and `TestAdv_10_WholeFinalFlushMissing`).
- **Attack `TestAdv_12_ChaosProcessFailureAfterPass` — FIXED:** `scripts/chaos.sh` records `go test` exit status and fails via `process_failed` when non-zero with no failed scenario (`f7747d6`); verified by mutation M11 (caught by `TestChaosAProcessFailureAfterPassingScenariosFails` and `TestAdv_12_ChaosProcessFailureAfterPass`).
- **Attack `TestAdv_12_ChaosParentFailure` — FIXED:** `scripts/chaos.sh` fails a scenario when `--- FAIL: TestScenarioE2E/$sc` appears even if all seed subtests passed (`f7747d6`); verified by mutation M12 (caught by `TestChaosAFailedParentWithPassingSeedsFailsTheScenario` and `TestAdv_12_ChaosParentFailure`).
- **Attack `TestAdv_13_DockerUnavailableFailsClosed` — FIXED:** `deploy/dev/down.sh` `listed()` fails closed (`rc=1`) when any Docker resource listing fails (`f7747d6`); verified by mutation M13 (caught by `TestRunDownFailsWhenDockerCannotListTheStack` and `TestAdv_13_DockerUnavailableFailsClosed`).
- **Owed gate item (`191ac9e`, `.github/scripts/build_site.py`, `scripts/c06a_site_links_test.go`, `scripts/gate-canaries.sh`) — VERIFIED:** `tracked(target)` checks `git ls-files -z` and fails closed on `git ls-files` errors; fast test `TestSiteLinksMustNameFilesGitTracks` catches M14; manual plant of `g9-ignored-link` (`docs/ignored-gate.out` linked from `docs/index.md`) fails both `scripts/site.sh` and `GATE_ONLY=G9` with `build_site: docs/index.md: link to a file git does not track: ignored-gate.out`.

## Mutation checks

All mutations were applied in a clean scratch git copy under `/tmp/zgw-c06a-operator-tools-review2/repo` with `GOFLAGS=-buildvcs=false` and `MG_REQUIRE_DUCKDB=1`:

1. **`M1_latency_from_sentAt_instead_of_dueAt`** (`internal/loadgen/tracker.go:316`: `now-s.dueAt` → `now-s.sentAt`): **CAUGHT** by `TestLatencyIsMeasuredFromTheIntendedSendTimeNotTheActualSend` (`internal/loadgen`).
2. **`M2_shortfall_ignores_late`** (`internal/loadgen/tracker.go:132`: `s.Late+s.CouldNotSend > 0` → `s.CouldNotSend > 0`): **CAUGHT** by `TestLateSendsAloneAreAShortfall` (`internal/loadgen`) and `TestLateSendsAloneExitThreeWithTheShortfallLine` (`cmd/loadgen`).
3. **`M3_contract_first_prefix_wins`** (`internal/mgctl/contract.go:41-50`: return constant rank `0` in `messageRank`): **CAUGHT** by `TestEveryProxyMadeRowIsNamedFromItsCodeAndMessage`, `TestALongerRowMessageWinsOverItsPrefixAndSeverityDecides`, and `TestAdv_9_FatalWithoutReady`.
4. **`M4_check_loadgen_labels_rejects_other`** (`test/analysis/report/sections/check-loadgen-labels.sql:17`: remove `reason <> 'other'`): **CAUGHT** by `TestReportLoadLabelsAcceptOtherAndRefuseMalformedClasses`.
5. **`M5_plan_allows_zero_ops`** (`internal/loadgen/plan.go:146-147`: remove `p.Count() < 1` check): **CAUGHT** by `TestAPlanWithNoOperationIsRefusedNamingTheFlags` (`internal/loadgen`) and `TestAPlanWithNoOperationAndAnOverLongPasswordAreUsageErrors` (`cmd/loadgen`).
6. **`M6_cmd_loadgen_ignores_flush_errors`** (`cmd/loadgen/main.go:206-209`: remove `FlushErr`/`FlushErrs` failure branch): **CAUGHT** by `TestACSVFlushErrorIsReportedAndFailsTheRun` (`cmd/loadgen`).
7. **`M7_formatns_overflows_maxint64`** (`internal/loadgen/report.go:31`: `ns/unit*tenths + ns%unit*tenths/unit` → `ns * tenths / unit`): **CAUGHT** by `TestTheLargestDurationFormatsWithoutOverflow` and `TestAdv_3_OverflowedLatencyFormatting`.
8. **`M8_password_truncates_over_1024`** (`internal/mgctl/password.go:23-25`: break at `len(b) >= MaxPassword` instead of returning `ErrPasswordTooLong`): **CAUGHT** by `TestAPasswordLineLongerThanTheLimitIsRefusedNotCut`, `TestQueryRefusesAnOverLongStdinPassword`, `TestAPlanWithNoOperationAndAnOverLongPasswordAreUsageErrors`, and `TestAdv_8_LongStdinPassword`.
9. **`M9_report_ignores_empty_sibling_arg`** (`scripts/report.sh:180`: drop `[ "$with_runs" = "$roots_ok" ]`): **CAUGHT** by `TestReportEmptyArgumentBesideAGoodRunFails` and `TestAdv_10_EmptySiblingRun`.
10. **`M10_report_ignores_missing_whole_flush`** (`scripts/report.sh:202`: omit `"$sections/flushes.sql"`): **CAUGHT** by `TestReportMissingWholeFlushFails` and `TestAdv_10_WholeFinalFlushMissing`.
11. **`M11_chaos_ignores_process_failure`** (`scripts/chaos.sh:80`: disable `process_failed S`): **CAUGHT** by `TestChaosAProcessFailureAfterPassingScenariosFails` and `TestAdv_12_ChaosProcessFailureAfterPass`.
12. **`M12_chaos_ignores_parent_failure`** (`scripts/chaos.sh:63`: drop parent `--- FAIL: TestScenarioE2E/$sc` check): **CAUGHT** by `TestChaosAFailedParentWithPassingSeedsFailsTheScenario` and `TestAdv_12_ChaosParentFailure`.
13. **`M13_rundown_ignores_docker_list_failure`** (`deploy/dev/down.sh:26-29`: ignore non-zero docker exit in `listed()`): **CAUGHT** by `TestRunDownFailsWhenDockerCannotListTheStack` and `TestAdv_13_DockerUnavailableFailsClosed`.
14. **`M14_site_allows_untracked_link`** (`.github/scripts/build_site.py:267-268`: disable `if not tracked(target):`): **CAUGHT** by `TestSiteLinksMustNameFilesGitTracks` (`scripts`).
15. **Manual canary `g9-ignored-link`** (created gitignored `docs/ignored-gate.out`, appended `[the gate log](ignored-gate.out)` to `docs/index.md`, committed in scratch repo): **CAUGHT** by `scripts/site.sh` (exit 1: `build_site: docs/index.md: link to a file git does not track: ignored-gate.out`) and `GATE_ONLY=G9 scripts/gate.sh c06a-operator-tools` (exit 1, `G9 site fail`, matching canary regex `link to a file git does not track: ignored-gate\.out`).

## Release It! checklist

- **Integration points:** `cmd/mgctl` applies `-timeout` (default 10 s) to HTTP admin requests and TCP dial + socket `SetDeadline`; `internal/loadgen/live` applies `DialTimeout` (10 s) for connect/login and `-drain` (5 s) for shutdown, clears socket deadlines after login for open-loop pipelining bounded by `ctx`/`drain`, and tests silent/no-read peers in `TestAFakeThatStopsReadingMakesSendsLateOrUnsent` and `TestAStalledFakeShowsInLatencyFromIntendedSend`.
- **Blocked threads:** `live.Run` closes `r.quit` and every `net.Conn` in `finish()` before `r.wg.Wait()`, unblocking reader/writer goroutines on completion, drain timeout, and cancellation; `mgctl.Query` defers `conn.Close()`; `mgctl` tier S model asserts `Proxy.Finish()` quiesce.
- **Slow responses:** `loadgen` schedules strictly on intended time without waiting for replies; unanswered requests at drain end remain in `in_flight` and queued ones move to `could_not_send` (conserved); proxy refusals are classified immediately on receipt.
- **Cascading failures:** Not applicable to proxy engine/health paths (no engine code modified); `loadgen` fails fast on setup when `ConnsOpen == 0` and marks dead connections down (`ConnDown`), accounting further ops as `could_not_send` in O(1) with no redial loop.
- **Chain reactions:** Not applicable here because this chunk adds only external operator tools, dev stack scripts, report/site tooling, and promoted tests, with no proxy admission or multi-instance routing code.
- **Dogpile:** Not applicable to proxy pool recovery (no engine/pool code modified); `loadgen` dials once at startup (capped at `MaxConns = 4096`) and does not reconnect.
- **Self-denial attacks:** `loadgen` caps startup dials at `MaxConns = 4096` and sends strictly at constant `Rate` with no retries; `report.sh` batches DuckDB queries into single sessions; `mgctl stats -watch` enforces `interval > 0` and caps iterations at `MaxWatch`.
- **Force multiplier:** Not applicable here because this chunk adds no automatic control or retry loop.
- **Unbalanced capacities:** Every queue, channel, and map/slice has a named cap (`MaxOutstandingCap = 1024`, `c.jobs` cap `MaxOutstanding`, `r.events` cap `Conns * MaxOutstanding * 4`, `MaxClasses = 32` with `other` overflow; `cmd/mgctl` `maxBody = 64 MiB`; `mgctl.MaxPassword = 1024` returning `ErrPasswordTooLong`; `mgctl.MaxRows = 10_000` with `Truncated` count) with explicit full-buffer rules.
- **Scaling effects:** `loadgen` is a single-instance client (`Instances: 1`); every exported label is bounded (`completed` has 4 fixed outcomes, `errors_by_class` is capped at 32 values including `other`, and `check-loadgen-labels.sql` accepts `other`).
- **Users:** `Plan.Validate()` and `NewTracker()` enforce bounds on `Rate` (`MaxRate = 1e6`), `DurationNs` (`MaxDurationNs = 24h`), `Count() >= 1`, `Tenants` (`MaxTenants = 1024`), `Conns` (`Tenants..MaxConns = 4096`), `Keys` (`MaxKeys = 1e9`), and `MaxOutstanding` (`1..1024`) before allocating.
- **Unbounded result sets:** `mgctl.Session.Receive` caps retained rows at `MaxRows = 10_000` (tracking `Truncated`) and reads in 32 KiB chunks; `cmd/mgctl` limits HTTP admin response reads to `64 MiB`; `mgctl.ReadPasswordLine` aborts once past 1,024 bytes; no proxy forwarding path is modified.
- **Compounds:** No retry loop combines with a leaky path; `live.go:finish()` drains buffered events, calls `Tracker.Finish()`, closes `quit` and sockets, waits for `wg`, publishes, and closes/checks CSV flush errors (`FlushErr` / `FlushErrs`), preventing leaks and silent flush loss.

## Observability

- **`Snapshot()` present:** `loadgen.Tracker.Snapshot()` returns a plain struct copy (`internal/loadgen/tracker.go:105-120,391-397`) with cloned `ErrorsByClass` slice and `obs.HistogramSnapshot` values (`Latency`, `LatencyFromSend`); no wall clock or map iteration in core (`detlint` clean).
- **Exporters and catalog:** `internal/loadgen/metrics.go` defines `Metrics()`, `Map()`, and `Source()`; declared in `internal/obs/catalog/catalog.go:44-46`; `docs/csv-schema.md` updated (+18 rows, verified current by `scripts/gen-docs.sh --check`); `internal/obs/simrun/loadgen_test.go` verifies catalog emission; no new proxy metrics added.
- **Counters-move and conservation tests:** `TestSnapshotCountersMoveAndTheMappingExportsEveryMetric`, `TestConservationHoldsOnEveryExitPathUnderRandomEvents`, `TestSnapshotCheckCatchesBrokenConservation`, `TestConservationHoldsWhenTheRunIsInterrupted`, `TestTheLoadToolFlushesItsOwnSnapshotToCSV`, and `TestACSVFlushErrorIsReportedAndFailsTheRun` all pass.
- **CSV-flush assertions / report checks:** Five built-in checks in `test/analysis/report/sections/check-loadgen-{conservation,answered,drained,moved,labels}.sql` (with `labels` now accepting `other` and `flushes.sql` verifying flush count against `run.csv`) plus coverage tables 4.7–4.9; all report tests pass with DuckDB 1.5.5.
- **Health rule:** Not applicable for proxy health (no new proxy component); `loadgen` is an external client tool.
- **Fuzz targets (PLAN.md 6.6):** None added in this chunk (verified via `grep -rn '^func Fuzz'` across all chunk 6a and promoted files).

## What I ran

- `gofmt -l . && go build ./... && go vet ./...` — clean, exit 0.
- `MG_REQUIRE_DUCKDB=1 go test -count=1 ./internal/loadgen/... ./cmd/loadgen ./internal/mgctl ./cmd/mgctl ./scripts/ ./test/analysis/report/ ./agent_work/adversarial/c06a-operator-tools/...` — all 8 packages pass (`loadgen` 1.61s, `live` 10.69s, `cmd/loadgen` 7.69s, `mgctl` 0.45s, `cmd/mgctl` 12.10s, `scripts` 45.29s, `test/analysis/report` 130.80s, `agent_work/adversarial/c06a-operator-tools` 48.70s).
- `go test -count=1 -v ./agent_work/adversarial/c06a-operator-tools/...` — `PASS: 44`, `SKIP: 1` (`TestAdv_3_ZeroIntendedShortfall`, settled), `FAIL: 0`.
- `go test -count=1 -run 'Mgctl' ./test/e2e` — pass (6.30s).
- `go test -race -count=1 ./internal/loadgen/...` — pass (`loadgen` 30.21s, `live` 12.04s).
- `go test -count=1 -run 'TestAdv' ./internal/obs/csvflush` — pass (0.03s).
- `DST_SEED=77` and `DST_SEED=99` on `go test -count=1 -run '^TestScenarioE2E$/^(e2e-queue-full|e2e-results)$' ./test/e2e` — pass (0.52s, 0.58s).
- `RUN_DIR=/tmp/zgw-c06a-operator-tools-review2/sweep5 scripts/sweep.sh -seeds 5 -run '^TestScenarioE2E$' && scripts/report.sh /tmp/zgw-c06a-operator-tools-review2/sweep5` — 105 seed runs passed, 0 failures; `VERDICT: PASS (129 checks over 105 runs)`.
- `go run ./cmd/loadgen -plan 10 -seed 7 -profile errors` — printed 10 ops and digest `e67d95636460527a`.
- `scripts/gen-docs.sh --check` — pass (all generated docs current).
- `GATE_ONLY=G0,G1,G2,G3 GATE_OUT=/tmp/zgw-c06a-operator-tools-review2/gate scripts/gate.sh c06a-operator-tools` — overall pass (556s): G0 (`tools` 4s, `gofmt` 1s, `build` 4s, `vet` 2s, `tools-vet` 1s, `tools-test` 7s, `tidy` 0s), G1 (`detlint` 6s, `importlint` 0s, `boundlint` 8s, `paniccheck` 0s), G2 (`comments` 1s), G3 (`unit` 487s, `race` 34s).
- 14 mutation checks (M1–M14) + clean site build (24 pages) + manual `g9-ignored-link` canary plant (`scripts/site.sh` and `GATE_ONLY=G9`) in `/tmp/zgw-c06a-operator-tools-review2/repo` — 14/14 mutations caught, canary caught.
