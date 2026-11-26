VERDICT: FAIL

## Findings

1. **MAJOR** — `internal/sim/lower_test.go:325-355` (`checkValuesMinimal`)
   - **What is wrong**: `checkValuesMinimal`, used by both `TestLowerValuesAgainstBruteForce` and `TestMinimizeAgainstBruteForce`, does not assert that per-kind floors are respected, that fields starting at or below their floor are left untouched, or that fields never increase above their input values. Line 338 (`if fv[j] <= fl[j] { continue }`) skips any field that was lowered below its floor (`fv[j] < fl[j]`). (By contrast, `checkLowerInts` at `internal/sim/lower_test.go:109-119` asserts all three properties for `LowerInts`.) This leaves the brief's required `"floors respected"` test property unasserted for `LowerValues` and `Minimize`.
   - **How I know**: In a scratch copy under `/tmp/zgw-cr01-shrink-fuzz-rules-review1/mut2`, mutating `LowerValues` to lower positive floors (`lows[i]--` when `vals[i]-lows[i] >= 2` and `d&(d+1) != 0` so call counts stay within `2*C`) and mutating `Minimize` to zero out all positive floors (`lows[i] = 0`) still passed `go test -count=1 -run 'TestLowerValuesAgainstBruteForce|TestMinimizeAgainstBruteForce|TestMinimizeNilFloorLowersToZero' ./internal/sim`. With only the `LowerValues` floor-violation mutation applied, the entire `go test -count=1 ./internal/sim` package passed (`ok github.com/nickstrad/mini-gateway/internal/sim 3.828s`).
   - **What a fix must achieve**: Assert floor compliance and monotonicity of lowering in `checkValuesMinimal` (or separate helpers for `LowerValues` and `Minimize`): for `LowerValues`, check every index `i` and field `j` against `in[i]` (`in_fv[j] <= fl[j] => fv[j] == in_fv[j]`, `in_fv[j] > fl[j] => fl[j] <= fv[j] <= in_fv[j]`); for `Minimize`, verify `out` matches an order-preserving subsequence of `in` with the same per-field bounds (`in_fv[j] <= fl[j] => fv[j] == in_fv[j]`, `in_fv[j] > fl[j] => fl[j] <= fv[j] <= in_fv[j]`). Both mutations above must cause `TestLowerValuesAgainstBruteForce` and `TestMinimizeAgainstBruteForce` to fail.

2. **MINOR** — `scripts/gate-canaries.sh:41`
   - **What is wrong**: The script clears gate-controlling environment variables (`unset GATE_ONLY GATE_STAGE GATE_BASE GATE_PREPROSE GATE_SHORT GATE_OUT SWEEP_SEEDS FUZZ_SECONDS`) before running the clean-copy check, but omits the new `FUZZ_RATE_FLOOR` (as well as `FUZZ_REPORT` and `FUZZ_GO`). If `FUZZ_RATE_FLOOR` is set below 100 in the caller's environment, the clean-copy gate run inherits it, produces `counts_for_landing: false` (`scripts/gate.sh:435`), and aborts the canary run.
   - **How I know**: Code inspection of `scripts/gate-canaries.sh:41,162-167` and `scripts/gate.sh:122,435`.
   - **What a fix must achieve**: Add `FUZZ_RATE_FLOOR`, `FUZZ_REPORT`, and `FUZZ_GO` to the `unset` statement in `scripts/gate-canaries.sh:41`.

3. **MINOR** — `scripts/gate-canaries.sh:342`
   - **What is wrong**: The `g5-seed-sweep-reported` green control canary sets `FUZZ_SECONDS=5`, which sits at the margin of `FuzzWorldSchedule`'s baseline-coverage duration on vm-1. Because Go's cached corpus under `$GOCACHE/fuzz/.../FuzzWorldSchedule` grows across runs (438 in `NOTES.md` -> 484 -> 492 -> 493 during this review), baseline coverage alone already takes 4–5 s on an unloaded host; under load or after modest cache growth, `FuzzWorldSchedule` will exit at 5 s without printing an `execs:` line, fail closed as `unreadable`, and falsely fail `g5-seed-sweep-reported`.
   - **How I know**: Observed `fuzz: elapsed: 5s, gathering baseline coverage: 484/484 completed` during `scripts/fuzz.sh` and `fuzz: elapsed: 4s, gathering baseline coverage: 492/492 completed` during `go test -run '^$' -fuzz '^FuzzWorldSchedule$' -fuzztime 5s ./internal/sim/world`.
   - **What a fix must achieve**: Give `g5-seed-sweep-reported` sufficient budget (or isolate its `GOCACHE` / fuzz cache so baseline coverage only evaluates the committed seed corpus) so it does not spuriously turn `unreadable` as `$GOCACHE/fuzz` grows.

4. **MINOR** — `scripts/fuzz.sh:125` and `scripts/gate.sh:190`
   - **What is wrong**: (a) `scripts/fuzz.sh:125` prints `$((count - fuzzed - sweeps)) without a rate`, which also counts `unclassified` targets (`scripts/fuzz.sh:111-115`, where a rate was parsed but `go list` failed) as "without a rate". (b) `scripts/fuzz.sh` prints a 15-line summary block (`fuzz: summary` + header + 12 target rows + `NOTE:` line), and `scripts/gate.sh:190` tails only the last 15 lines of `G5-fuzz.log` on failure, cutting off the `FAILED` and `--- FAIL:` lines completely.
   - **How I know**: Code inspection of `scripts/fuzz.sh:111-125` and `scripts/gate.sh:190`, verified against the 15-line summary emitted by `FUZZ_SECONDS=20 scripts/fuzz.sh`.
   - **What a fix must achieve**: Distinguish `unclassified` from `unreadable` in `scripts/fuzz.sh:125`, and emit a final line listing failed target names when `rc != 0` so `gate.sh:190`'s tail always identifies the failing target.

5. **MINOR** — `internal/sim/lower_test.go:74`
   - **What is wrong**: `intCallBound` computes `d := uint64(v) - uint64(f)` and adds `int64(d)` to `p`. For `v = math.MaxInt64, f = math.MinInt64` (`TestLowerIntsExtremesAndBounds` case 1), `d = 2^64 - 1` wraps to `int64(d) = -1`, making `(1 + p) * c == 0`. This is masked only because `TestLowerIntsExtremesAndBounds` currently calls `intCallBound` with `monotone = true` (returning `2 * c`).
   - **How I know**: Code inspection of `internal/sim/lower_test.go:72-79,185`.
   - **What a fix must achieve**: Compute `p` without signed wrap (e.g., saturating `uint64` arithmetic returning `math.MaxInt64` on overflow).

## Open flake, freeze-sim, and PLAN.md 6.6 audit

- **Open flake (`NOTES.md` lines 188–195)**: Does **not** point at a real nondeterminism in CR-1 or in any existing fuzz target. All 6 packages containing the 12 fuzz targets (`internal/sim`, `internal/sim/simtoy`, `internal/sim/world`, `internal/sim/fakepg`, `internal/pgwire`, `internal/testclient`) are in `tools/lint-rules/det-packages.txt` and pass `detlint` with 0 findings. Reading all 12 `Fuzz*` functions and their helpers (`runDecoded`, `runSchedule`, `newWorld`, `testClientConfig`, `testBackendConfig`) confirms none use wall-clock time, goroutines, map iteration order, or input-hashed seeds. The builder's speculation ("a wall-clock-free target hitting a step budget" only under load) is inconsistent because virtual step count does not vary with host CPU load; the failure is consistent with either a transient `go test -fuzz` worker termination/cache race under concurrent builder load on the shared `$GOCACHE/fuzz` directory (`internal/fuzz/worker.go:165-187`) or a rare randomly mutated input hitting a pre-existing target check whose `--- FAIL:` line was truncated by `gate.sh:190`'s 15-line tail (Finding 4) and deleted with `$work`. Neither a full 20 s `scripts/fuzz.sh` run nor a 5 s `gate.sh` G5 run reproduced any failure.
- **Freeze (`freeze-sim`)**: `git diff freeze-sim..HEAD -- internal/sim/fault.go` is empty; `git diff 8efa92e7..1179f92b` adds only new Go files in `internal/sim` (`lower.go`, `schedtext.go`, and tests) and touches no file in `internal/pgwire` or `internal/work`. `TestShrinkFrozenTable`, `TestShrinkFrozenSweepDigest`, and all golden-digest tests pass untouched.
- **PLAN.md 6.6 rules**:
  1. Seed derivation: audited all 12 targets (`FuzzScheduleFromBytes`, `FuzzParseSchedule`, `FuzzToySchedule`, `FuzzWorldSchedule`, `FuzzServerInput`, `FuzzFramer` x2, `FuzzMatch`, `FuzzClientSession`, `FuzzBackendSession`, `FuzzCycle`, `FuzzSession`). Every simulator-running target uses a fixed seed (`1`); none derives a seed from a hash of its input.
  2. Rate reporting: `scripts/fuzz.sh` reports executions, executions/sec (`execs / elapsed_seconds`), and class for every target, writes TSV to `FUZZ_REPORT`, and fails closed when a rate cannot be parsed.
  3. Rate floor: simulation-level targets are classified dynamically via `go list -deps -test` against `internal/sim`, classed `seed-sweep` below `FUZZ_RATE_FLOOR` (default 100) without failing the run, and counted in `result.json` (`fuzz_targets`, `fuzz_seed_sweeps`, `fuzz_rate_floor`); floors `< 100` set `counts_for_landing: false`.

## Mutation checks

Run in scratch copies under `/tmp/zgw-cr01-shrink-fuzz-rules-review1/` (with `GOFLAGS=-buildvcs=false` for `./scripts` because `/tmp/.git` exists on this host):

1. **Bisection stops one step early** (`internal/sim/lower.go:68`: `> 1` -> `> 2`): **CAUGHT** by `TestLowerIntsAgainstBruteForce`, `TestLowerIntsExtremesAndBounds`, and `TestLowerValuesAgainstBruteForce`.
2. **`Minimize` stops after a single round** (`internal/sim/lower.go:40`: `return withValues(cur, vals)`): **CAUGHT** by `TestMinimizeAgainstBruteForce` and `TestMinimizeIsDeterministicAcrossProcesses`.
3. **Caller's input slice mutated** (`internal/sim/lower.go:16`: `cur := vals` instead of `slices.Clone(vals)`): **CAUGHT** by `TestLowerIntsAgainstBruteForce` and `TestLowerIntsExtremesAndBounds`.
4. **Ignore all floors in `lowerOne`** (`internal/sim/lower.go:60`: `floor = 0`): **CAUGHT** by `TestLowerIntsAgainstBruteForce`, `TestLowerIntsExtremesAndBounds`, and `TestLowerValuesAgainstBruteForce` (caught on negative floor `-3` not reached).
5. **Violate positive floors in `LowerValues` and `Minimize` without exceeding call bound** (`internal/sim/lower.go:24-42`: `LowerValues` decrements positive `lows[i]` when `d >= 2 && d&(d+1) != 0`; `Minimize` zeroes positive `lows[i]`): **MISSED** by `TestLowerValuesAgainstBruteForce`, `TestMinimizeAgainstBruteForce`, and `TestMinimizeNilFloorLowersToZero` (and with only `LowerValues` mutated, **MISSED** by the entire `go test -count=1 ./internal/sim` suite) -> Finding 1.
6. **Alter `sim.Shrink` scan order** (`internal/sim/fault.go:204`: reverse deletion order): **CAUGHT** by `TestShrinkFrozenTable`, `TestShrinkFrozenSweepDigest`, and `TestMinimizeIsDeterministicAcrossProcesses`.
7. **Accept truncated schedule text** (`internal/sim/schedtext.go:56`: disable `if n != len(lines)-1`): **CAUGHT** by `TestScheduleTextRejectsMalformed` (`truncated`, `extra line`).
8. **Accept numeric `fault-<id>` for a named kind** (`internal/sim/schedtext.go:134`: disable `kindName` check): **CAUGHT** by `TestScheduleTextRejectsMalformed` (`named kind by number`).
9. **Remove planted bug in `simtoy`** (`internal/sim/simtoy/toy.go:395`: disable `plantBug` branch): **CAUGHT** by `TestShrunkScenarioPlantedBugReplays`.
10. **`fuzz.sh` fails open when rate is unreadable** (`scripts/fuzz.sh:106`: remove `rc=1`): **CAUGHT** by `TestFuzzShFailsClosedWithoutARate`.
11. **`fuzz.sh` drops simulation-level dependency check** (`scripts/fuzz.sh:118`: remove `[ $lvl = 0 ]`): **CAUGHT** by `TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps` (`internal/testclient FuzzSession` wrongly classed `seed-sweep`).

## Release It! checklist

- **Integration points**: Not applicable here because CR-1 adds pure in-memory schedule-lowering and text-codec functions plus test/gate shell tooling, with no client or database socket I/O.
- **Blocked threads**: Not applicable here because CR-1 spawns no goroutines and holds no connections or locks; `simtoy` replay tests assert `res.Quiesce == nil`.
- **Slow responses**: Not applicable here because CR-1 introduces no network request/deadline path; `lowerOne`, `LowerInts`, `LowerValues`, and `Minimize` terminate deterministically within stated finite call bounds on any deterministic predicate.
- **Cascading failures**: Not applicable here because CR-1 does not sit on the request path or interact with backend availability or health reporting.
- **Chain reactions**: Not applicable here because CR-1 adds no multi-instance proxy or queueing mechanism.
- **Dogpile**: Not applicable here because CR-1 performs no dialing, reconnection, or fault recovery traffic.
- **Self-denial attacks**: Not applicable here because CR-1 performs no burst dialing, expiry, flushing, or retries.
- **Force multiplier**: Partially applicable to the minimiser's iterative lowering loop (`internal/sim/lower.go`): every value has an explicit floor (default 0 or caller-supplied `FaultFloor`), values move monotonically downward in `[floor, input]`, and `lowerOne` uses unsigned bisection with at most `1 + bits.Len64(v - f)` calls; however, floor enforcement on `LowerValues`/`Minimize` is not directly asserted by `checkValuesMinimal` (Finding 1).
- **Unbalanced capacities**: Checked — `FormatSchedule` and `ParseSchedule` cap schedules at `MaxScheduleLen` (1,024) and kind names at 64 bytes (`ParseSchedule` checks newline count before splitting); line length itself is bounded only by the caller's input string (documented in `NOTES.md` Stated limits).
- **Scaling effects**: Not applicable here because CR-1 adds no multi-instance runtime guarantee and emits no runtime metrics or labels.
- **Users**: Not applicable here because CR-1 introduces no per-client or per-tenant runtime resources.
- **Unbounded result sets**: Not applicable here because CR-1 does not stream or forward protocol replies.
- **Compounds**: Checked — `scripts/gate-canaries.sh` runs `fuzz.sh` against the shared `$GOCACHE/fuzz` corpus with `FUZZ_SECONDS=5`, compounding growing corpus size with the fail-closed rate check (Finding 3); no runtime code compounds exist.

## Observability

- **7.6 applicability**: CR-1 adds no stateful runtime component (only stateless pure functions `LowerInts`, `LowerValues`, `Minimize`, `FormatSchedule`, `ParseSchedule`, plus shell tooling in `scripts/`).
- **`Snapshot()`**: Existing `sim.Sim.Snapshot()` is unchanged (`freeze-sim` preserved); no new `Snapshot()` type is required.
- **Counters move & conservation / CSV flush / registry / coverage / health**: No new runtime metrics, exporters, CSV schema rows, or health rules are required; all existing observability, CSV-flush, and conservation tests in `internal/obs/...`, `internal/sim/...`, and `internal/pgwire/...` pass (`go test -count=1 ./...`).

## What I ran

- `git diff --stat 8efa92e7f4b19b66c12bc61d8a9911b20389a177..1179f92b6e94a429fef1411c516416bbed20de56`: 15 files changed, +1842 / -10; `git diff freeze-sim..HEAD -- internal/sim/fault.go` empty.
- `gofmt -l .`: empty.
- `go build ./... && go vet ./...`: exit 0.
- Project lints (`detlint` 75 files / 16 packages, `importlint` 182 files, `paniccheck`, `nocomments` against `8efa92e7`): all exit 0.
- `go test -count=1 ./...`: exit 0, all 25 test packages green (`internal/obs/simrun` 141.6s, `internal/sim/world` 13.8s, `internal/sim` 2.3s, `internal/sim/simtoy` 1.1s, `scripts` 5.6s, all 4 adversarial packages green).
- `go test -count=1 -v ./internal/sim -run 'Lower|Minimize|Planted|Shrink|ScheduleText'`: exit 0, 16/16 tests passed (0.48s).
- `go test -count=1 -v ./internal/sim/simtoy -run 'PlantedBug|Shrunk'`: exit 0, 5/5 tests passed (0.28s; confirmed `Shrink` 1 fault sum 2,213,137 -> `Minimize` 1 fault sum 1,021,423; workload `[3 20 40] -> [3 1 1]`).
- `go test -count=1 -v ./scripts/`: exit 0, 4/4 tests passed (6.81s).
- `DST_SEED=999 go test -count=1 ./internal/sim/simtoy/ ./internal/sim/world/` and `DST_SEED=424242 go test -count=1 ./internal/sim/simtoy/`: exit 0 (0.83s, 11.17s, 1.12s).
- `scripts/sweep.sh -seeds 50`: exit 0, 1,206 seed runs passed, 0 failures.
- `FUZZ_SECONDS=20 FUZZ_REPORT=/tmp/zgw-cr01-shrink-fuzz-rules-review1/rates.tsv scripts/fuzz.sh`: exit 0, 12 targets, 12 `fuzz`, 0 `seed-sweep`, 0 without a rate (`FuzzParseSchedule` 15,662/s, `FuzzScheduleFromBytes` 21,334/s, `FuzzToySchedule` 836/s, `FuzzWorldSchedule` 121/s, `FuzzServerInput` 11,789/s, `FuzzClientSession` 3,109/s, `FuzzBackendSession` 12,184/s, `FuzzCycle` 6,909/s, `FuzzFramer` pgwire 6,161/s / fakepg 16,384/s, `FuzzMatch` 18,163/s, `FuzzSession` 25,566/s).
- `GATE_ONLY=G5 GATE_OUT=/tmp/zgw-cr01-shrink-fuzz-rules-review1/gate-g5 FUZZ_SECONDS=5 FUZZ_RATE_FLOOR=1000000000 scripts/gate.sh cr01-shrink-fuzz-rules`: exit 0 (77s), `overall: pass`, `fuzz_targets: 12`, `fuzz_seed_sweeps: 11`, `fuzz_rate_floor: 1000000000`, `counts_for_landing: false`.
- 11 mutation checks in `/tmp/zgw-cr01-shrink-fuzz-rules-review1/`: 10 caught, 1 missed (Finding 1).
