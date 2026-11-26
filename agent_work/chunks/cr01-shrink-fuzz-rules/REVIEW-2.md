VERDICT: PASS

## Findings

None. All five findings from `REVIEW-1.md` and both adversarial breaks / the `FuzzWorldSchedule` slow-input flake from `RESULTS.md` are resolved at `bb306843903d1a45e0908deddbc918a9cb357770`:

- **Review 1 Finding 1 (MAJOR, floors not asserted for `LowerValues`/`Minimize`)**: Fixed in `6a26854` via `loweredFrom` and `checkLoweredFrom` (`internal/sim/lower_test.go:393-429`), called by `checkValuesMinimal` (`internal/sim/lower_test.go:365`). Re-running both Review 1 floor-violation mutations now fails `TestLowerValuesAgainstBruteForce` (673 failures) and `TestMinimizeAgainstBruteForce` (362 failures).
- **Review 1 Finding 2 (MINOR, canary env unset)**: Fixed in `206c548`; `scripts/gate-canaries.sh:41` unsets `FUZZ_RATE_FLOOR`, `FUZZ_REPORT`, and `FUZZ_GO`.
- **Review 1 Finding 3 (MINOR, `g5-seed-sweep-reported` 5 s budget)**: Fixed in `206c548`; `scripts/gate-canaries.sh:342` runs with `FUZZ_SECONDS=20`, and the slow `FuzzWorldSchedule` baseline input is bounded by `5fbf999`.
- **Review 1 Finding 4 (MINOR, `unclassified` count and 15-line tail)**: Fixed in `b8e9aca`; `scripts/fuzz.sh:105-147` tracks `unreadable` and `unclassified` separately in the `NOTE:` line and emits `fuzz: FAILED: <dir> <target>, ...` as the final line on failure (covered by `TestFuzzShNamesEveryFailedTargetOnItsLastLine` and `TestFuzzShPrintsNoFailedLineWhenGreen`).
- **Review 1 Finding 5 (MINOR, `intCallBound` signed wrap)**: Fixed in `6a26854` via `satAdd`/`satMul` (`internal/sim/lower_test.go:65-95,317-356`), covered by `TestCallBoundsSaturateInsteadOfWrapping`.
- **Adversarial Claim 5 (`TestAdv_5_UnboundedScheduleClaim`)**: Narrowed to schedules of at most `MaxScheduleLen` (1,024) faults and settled by the orchestrator in `bb30684` (`ADVERSARIAL.md`, `adv_test.go:290`).
- **Adversarial Claim 6 (`TestAdv_6_UnreadableAfterProgress`)**: Fixed in `b8e9aca`; `read_rate` (`scripts/fuzz.sh:71-78`) takes the last `^fuzz: elapsed: ` line and requires `execs: <N> (<M>/sec)`. Both `TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne` and `TestAdv_6_UnreadableAfterProgress` pass.
- **`FuzzWorldSchedule` slow input (`5fbf999`)**: `internal/sim/world/property_test.go:114-125` (`fuzzSchedule`, test-only `package world_test`) caps decoded `fakepg.FaultBloat` `Arg2` at `fuzzMaxBloatRowBytes` (256), keeps `7edb6a746e048b6e` as a committed seed (`internal/sim/world/testdata/fuzz/FuzzWorldSchedule/7edb6a746e048b6e`, replays in 0.43 s), and asserts the 100,000-step budget in `TestFuzzWorldScheduleCostIsBounded`. `freeze-sim` holds untouched (`git diff --name-status freeze-sim..bb306843 -- 'internal/sim/*.go'` lists only the new CR-1 files; `internal/pgwire` and `internal/work` are untouched by `8efa92e7..bb306843`).
- **PLAN.md 6.6 seed-derivation rule**: Audited all 12 fuzz targets across `internal/sim`, `internal/sim/simtoy`, `internal/sim/world`, `internal/sim/fakepg`, `internal/pgwire`, and `internal/testclient`. None derives a simulator seed from a hash of its input (simulator-running targets use fixed seed `1`).

## Mutation checks

Executed in isolated scratch copies under `/tmp/zgw-cr01-shrink-fuzz-rules-review2/mut/` (`GOFLAGS=-buildvcs=false`):

1. **Review 1 Finding 1A — `LowerValues` lowers positive floors without exceeding `2*C`** (`internal/sim/lower.go:24-29`, decrement positive `lows[i]` when `d >= 2 && d&(d+1) != 0`): **CAUGHT** by `TestLowerValuesAgainstBruteForce` (`673 failures, first: seed 1: always: fault 0 {0ns 4 0 0 -1} is not {12ns 4 0 3 -1} lowered within [floor, input]`).
2. **Review 1 Finding 1B — `Minimize` zeroes positive floors** (`internal/sim/lower.go:36-37`, set positive `lows[i] = 0`): **CAUGHT** by `TestMinimizeAgainstBruteForce` (`362 failures, first: seed 1: sum-mod-4: ... is not a subsequence ... with each value lowered within [floor, input]`).
3. **Bisection stops one step early** (`internal/sim/lower.go:68`: `> 1` -> `> 2`): **CAUGHT** by `TestLowerIntsAgainstBruteForce` (388 failures), `TestLowerValuesAgainstBruteForce` (243 failures), and `TestMinimizeAgainstBruteForce` (168 failures).
4. **`Minimize` stops after a single round** (`internal/sim/lower.go:40`: `return withValues(cur, vals)`): **CAUGHT** by `TestMinimizeAgainstBruteForce` (191 failures) and `TestMinimizeIsDeterministicAcrossProcesses` (digest `0x3222ad9b43b3832d` vs pinned `0xbbecbf44045a19ac`).
5. **`LowerInts` mutates caller's slice** (`internal/sim/lower.go:16`: `cur := vals`): **CAUGHT** by `TestLowerIntsAgainstBruteForce` (435 failures).
6. **`ParseSchedule` accepts truncated/extra-line schedules** (`internal/sim/schedtext.go:56`: disable `n != len(lines)-1` check): **CAUGHT** by `TestScheduleTextRejectsMalformed` (`truncated`, `extra line`).
7. **`FuzzWorldSchedule` bloat cap removed / raised to 1024** (`internal/sim/world/property_test.go:121`): **CAUGHT** by `TestFuzzWorldScheduleCostIsBounded` (timed out at 120 s when uncapped; failed step budget with `265980` and `265994` steps vs `100000` budget when raised to 1024).
8. **`fuzz.sh` reads stale rate from earlier progress line before an unreadable last line** (`scripts/fuzz.sh:73`: revert `read_rate` grep to `^fuzz: elapsed: [0-9hms]+, execs: [0-9]+ \([0-9]+/sec\)`): **CAUGHT** by `TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne` and `TestAdv_6_UnreadableAfterProgress`.
9. **`fuzz.sh` omits `fuzz: FAILED:` final line** (`scripts/fuzz.sh:146`): **CAUGHT** by `TestFuzzShNamesEveryFailedTargetOnItsLastLine`.
10. **`fuzz.sh` drops simulation-level dependency check** (`scripts/fuzz.sh:133`: remove `[ $lvl = 0 ] &&`): **CAUGHT** by `TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps` (`internal/testclient FuzzSession` wrongly classed `seed-sweep`).
11. **`intCallBound` wraps instead of saturating** (`internal/sim/lower_test.go:74`: `p += int64(d)`): **CAUGHT** by `TestCallBoundsSaturateInsteadOfWrapping` (`full-width non-monotone bound 0, want saturation at 9223372036854775807`).

## Release It! checklist

- **Integration points**: Not applicable here because CR-1 adds pure in-memory schedule-lowering and text-codec functions plus shell/test tooling, with no client or database connection I/O.
- **Blocked threads**: Not applicable here because CR-1 spawns no goroutines and holds no connections or locks; `simtoy` replay tests assert `res.Quiesce == nil`.
- **Slow responses**: Not applicable here because CR-1 adds no network deadline or request path; `lowerOne`, `LowerInts`, `LowerValues`, and `Minimize` terminate within stated finite bounds on any deterministic predicate, and `FuzzWorldSchedule` now bounds single-execution cost to `<= 100,000` simulator steps (`TestFuzzWorldScheduleCostIsBounded`).
- **Cascading failures**: Not applicable here because CR-1 does not sit on the request path or affect backend availability or health reporting.
- **Chain reactions**: Not applicable here because CR-1 adds no multi-instance proxy or queueing mechanism.
- **Dogpile**: Not applicable here because CR-1 performs no dialing, reconnection, or recovery traffic.
- **Self-denial attacks**: Not applicable here because CR-1 performs no burst dialing, expiry, flushing, or retries.
- **Force multiplier**: Checked — `lowerOne`, `LowerInts`, `LowerValues`, and `Minimize` enforce explicit floors (`FaultFloor` / `floors`), move values monotonically in `[floor, input]`, use overflow-free unsigned bisection, and are verified by `checkLowerInts`, `checkLoweredFrom`, and the brute-force sweeps.
- **Unbalanced capacities**: Checked — `FormatSchedule` and `ParseSchedule` enforce `MaxScheduleLen` (1,024) and 64-byte kind names (`ParseSchedule` checks newline count before splitting); line length is bounded by the caller's string (documented in `NOTES.md` Stated limits); `fuzzSchedule` bounds input bytes to 128 and `pg.bloat` rows to 256 bytes.
- **Scaling effects**: Not applicable here because CR-1 adds no multi-instance runtime guarantee or runtime metrics/labels.
- **Users**: Not applicable here because CR-1 introduces no per-client or per-tenant runtime resources.
- **Unbounded result sets**: Not applicable here because CR-1 does not stream or forward protocol replies.
- **Compounds**: Checked — `g5-seed-sweep-reported` now uses `FUZZ_SECONDS=20` and `FuzzWorldSchedule` bounds worst-case execution cost so corpus growth no longer compounds with the fail-closed rate check; no runtime code compounds exist.

## Observability

- **7.6 applicability**: CR-1 adds no stateful runtime component (only stateless pure functions `LowerInts`, `LowerValues`, `Minimize`, `FormatSchedule`, `ParseSchedule`, plus test/gate tooling).
- **`Snapshot()`**: Existing `sim.Sim.Snapshot()` is unchanged (`freeze-sim` preserved); no new `Snapshot()` struct is required.
- **Counters move, conservation, CSV flush, registry, coverage, health**: No new runtime metrics, exporters, CSV schema rows, or health rules are required; all existing observability, CSV-flush, and conservation tests across `internal/obs/...`, `internal/sim/...`, and `internal/pgwire/...` pass in `go test -count=1 ./...`.

## What I ran

- `git diff --name-status 8efa92e7f4b19b66c12bc61d8a9911b20389a177..bb306843903d1a45e0908deddbc918a9cb357770`: 26 files changed (+2840 / -16); `git diff --name-status freeze-sim..bb306843 -- 'internal/sim/*.go'` lists only new CR-1 files; `git status -s` clean.
- `gofmt -l .`: empty; `go build ./... && go vet ./...`: exit 0.
- `GATE_ONLY=G0,G1,G2 GATE_OUT=/tmp/zgw-cr01-shrink-fuzz-rules-review2/gate-lint scripts/gate.sh cr01-shrink-fuzz-rules`: exit 0 (57 s), all 12 checks (`tools`, `gofmt`, `build`, `vet`, `tools-vet`, `tools-test`, `tidy`, `detlint` 16 package patterns, `importlint`, `boundlint`, `paniccheck`, `comments` vs `8efa92e7`) pass.
- `go test -count=1 ./...`: exit 0, all 29 test packages green (`internal/sim` 4.94 s, `internal/sim/simtoy` 3.06 s, `internal/sim/world` 69.21 s, `scripts` 26.36 s, `agent_work/adversarial/cr01-shrink-fuzz-rules` 13.19 s, `internal/obs/simrun` 356.27 s under concurrent load).
- `go test -count=1 -v ./internal/sim -run 'Lower|Minimize|Planted|Shrink|ScheduleText'`: exit 0, 16/16 pass (0.73 s).
- `go test -count=1 -v ./internal/sim/simtoy -run 'PlantedBug|Shrunk'`: exit 0, 5/5 pass (0.32 s; `Shrink` 1 fault sum `2213137` -> `Minimize` 1 fault sum `1021423`; workload `[3 20 40] -> [3 1 1]`).
- `go test -count=1 -v ./internal/sim/world -run 'TestFuzzWorldScheduleCostIsBounded|FuzzWorldSchedule'`: exit 0 (2.12 s; `TestFuzzWorldScheduleCostIsBounded` 1.60 s, `FuzzWorldSchedule/7edb6a746e048b6e` 0.43 s).
- `go test -count=1 -v ./scripts/`: exit 0, 7/7 pass (27.38 s).
- `go test -count=1 -v ./agent_work/adversarial/cr01-shrink-fuzz-rules/`: exit 0, 22 pass, 2 skip (`TestAdv_5_UnboundedScheduleClaim` settled by orchestrator, `TestAdv_2_LongSeedCampaign` requires `ADV_LONG=1`) (35.44 s).
- `DST_SEED=999` and `DST_SEED=424242` on `./internal/sim/simtoy/ ./internal/sim/world/`: exit 0 (3.13 s / 37.04 s and 4.68 s / 39.89 s).
- `scripts/sweep.sh -seeds 50`: exit 0, 1,206 seed runs passed, 0 failures.
- `FUZZ_SECONDS=20 FUZZ_REPORT=/tmp/zgw-cr01-shrink-fuzz-rules-review2/rates.tsv scripts/fuzz.sh`: exit 0, 12 targets, 12 `fuzz`, 0 `seed-sweep`, 0 without a rate, 0 unclassified (`FuzzBackendSession` 6,684/s, `FuzzClientSession` 1,531/s, `FuzzCycle` 4,198/s, `FuzzFramer` pgwire 5,668/s / fakepg 14,460/s, `FuzzParseSchedule` 15,260/s, `FuzzScheduleFromBytes` 21,102/s, `FuzzMatch` 11,311/s, `FuzzServerInput` 6,909/s, `FuzzToySchedule` 235/s, `FuzzWorldSchedule` 146/s, `FuzzSession` 26,165/s).
- 11 mutation checks in `/tmp/zgw-cr01-shrink-fuzz-rules-review2/mut/`: 11/11 caught.
