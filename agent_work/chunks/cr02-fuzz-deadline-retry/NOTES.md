# CR-2 — one retry in `scripts/fuzz.sh` for Go's own deadline error: builder notes

Rebuilt by Neon on 2026-11-27 on branch `work/cr02n-fuzz-deadline-retry` (worktree
`/root/Software/mini-gateway-work/cr02n`) from
`/root/briefs/zgateway-p2-cr02n-fuzz-deadline-retry.md` under Nick's ruling
(`/root/briefs/zgateway-cr02-neon-rebuild-ruling.md`).

## What was built

| Deliverable | Where | Commit |
| --- | --- | --- |
| `fuzz_retries` and `fuzz_max_retries` in `result.json`; landing cap `<= 2`; `ci-fast.sh` env unset | `scripts/gate.sh`, `scripts/ci-fast.sh` | `f3c4090` |
| Exact 5-condition deadline signature check, single retry per target, `FUZZ_MAX_RETRIES` cap (default 2, 0 disables), `fuzz: RETRY:` line, 6th `retried` TSV column, NOTE count, and `FUZZ_GO` tests | `scripts/fuzz.sh`, `scripts/fuzz_test.go` | `4f498d4` |
| CR-1 TSV column-width expectation updated from 5 to 6 | `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go` | `97ef501` |
| Gate canaries `g5-retry-reported` and `g5-crasher-not-retried`; `FUZZ_MAX_RETRIES` unset | `scripts/gate-canaries.sh` | `984183b` |
| G5 deadline-retry documentation | `docs/knowledge/foundation-gates.md` | `664357a` |

## Use of parked branch `work/cr02-fuzz-deadline-retry`

The implementation, tests, canaries, and docs were built independently without reading any
code or diffs from `work/cr02-fuzz-deadline-retry`. After encountering the pre-existing
`scripts/gate-canaries.sh` clean-copy G3 failure (`d39ecfb` and `freeze-sim` absent in the
throwaway `git init` copy), I read only `git show 0e8af68 -- agent_work/chunks/cr02-fuzz-deadline-retry/NOTES.md`
to confirm that the precondition failure was already observed as out of scope. No code,
tests, or documentation were copied or adapted from the parked branch.

## Decisions the brief left open

1. **Strict log-whitelist parser for the deadline signature (`is_deadline_signature` in `scripts/fuzz.sh`).**
   Instead of blacklisting only specific strings, an `awk` state machine whitelists every
   line legal in a genuine Go fuzz deadline exit:
   - Before `--- FAIL: <target>`: empty lines, Go's `warning: starting with empty corpus` /
     coverage-instrumentation warning, `waiting for fuzzing process to terminate...`,
     `=== RUN   <target>`, and exact `fuzz: elapsed:` baseline/seed/execs progress lines
     (excluding `minimizing`).
   - Header: exact `--- FAIL: <target>` with optional duration ` (<dur>)` (no subtest suffix,
     no prefix collision).
   - Message: immediately followed by exactly one line matching `^[ \t]+context deadline exceeded$`
     (so `t.Fatal("context deadline exceeded")`, which carries `file_test.go:N:`, is rejected).
   - Terminator: immediately followed by `FAIL`, then only optional `exit status 1` (rejecting
     `exit status 2`), optional `FAIL <pkg> <dur>`, and empty lines.
   Any other text anywhere (panic, `Failing input written to`, `fuzz: minimizing`, second
   `--- FAIL:` block, extra message line, `\r` carriage return) fails closed.
2. **Corpus-change detection (`list_corpus` + `LC_ALL=C comm -13`).**
   Before attempt 1, `scripts/fuzz.sh` records sorted relative paths under
   `<dir>/testdata/fuzz/<target>/`. On non-zero exit it snapshots again and refuses to retry
   if `LC_ALL=C comm -13` reports any new entry (works whether the directory existed before
   or was created by attempt 1).
3. **`FUZZ_MAX_RETRIES` validation and landing cap.**
   Both `scripts/fuzz.sh` and `scripts/gate.sh` accept `0` or positive integers up to 9
   digits (`^(0|[1-9][0-9]{0,8})$`) and exit 2 otherwise (avoiding 64-bit arithmetic overflow
   on oversized numbers). `scripts/gate.sh` passes `FUZZ_MAX_RETRIES` to `scripts/fuzz.sh`,
   records `fuzz_max_retries` in `result.json`, and requires `[[ "$fuzz_max_retries" =~ ^[012]$ ]]`
   for `counts_for_landing` — matching the existing gate convention for `SWEEP_SEEDS < 200`,
   `FUZZ_SECONDS < 20`, and `FUZZ_RATE_FLOOR < 100`. `fuzz_retries` itself never alters
   `counts_for_landing`.
4. **Report and NOTE format.**
   The `retried` column (`0` or `1`) is appended as column 6 on every row, including
   `unreadable` and `unclassified` rows (when a retried target fails to produce a rate or
   cannot be classified on attempt 2, `retried` remains `1` so RETRY lines, column 6, NOTE,
   and `fuzz_retries` always agree). The NOTE line appends `, <N> retried`.
5. **Canary implementation in `scripts/gate-canaries.sh`.**
   Because `scripts/gate.sh` unsets `FUZZ_GO`, `g5-retry-reported` plants a fast target
   `internal/canary FuzzCanaryRetry` and prepends a temporary `go` wrapper on `PATH` that
   emits the exact deadline signature once for `^FuzzCanaryRetry$` and delegates attempt 2
   and all other invocations to real `go`. It verifies G5 passes, `fuzz: RETRY:` appears for
   `internal/canary FuzzCanaryRetry`, its TSV row has `retried=1`, and `result.json`
   `.fuzz_retries` equals both the TSV count (`>= 1`) and the NOTE count.
   `g5-crasher-not-retried` plants `internal/canary FuzzCrasher` (passes seed `"ok"`, fails
   any other input with `t.Fatal("PLANTED-FUZZ-CRASHER")`), runs real `go` with no wrapper,
   and verifies G5 fails with `PLANTED-FUZZ-CRASHER` and `Failing input written to`, with no
   `fuzz: RETRY:` line and `retried=0` for `FuzzCrasher`.
6. **Environment hygiene.**
   Added `FUZZ_MAX_RETRIES` to the `unset` line in `scripts/gate-canaries.sh` and added
   `FUZZ_RATE_FLOOR` and `FUZZ_MAX_RETRIES` to `unset` in `scripts/ci-fast.sh`.

## Claims and their tests

| # | Claim | Proved by |
| --- | --- | --- |
| 1 | A real failure is never retried (failing input, panic, other failure text, or new corpus file fails at once) | `scripts/fuzz_test.go`: `TestFuzzShDoesNotRetryNearMisses` (11 subtests: `failing_input_written`, `deadline_plus_panic_after`, `deadline_plus_panic_before`, `different_message_under_fail`, `file_line_prefixed_deadline`, `extra_message_under_fail`, `wrong_target_name`, `subtest_fail_block`, `minimizing_before_deadline`, `exit_status_2_after_fail`, `passing_target_with_message_in_log`), `TestFuzzShDoesNotRetryWhenNewCorpusFileAppears` (`existing_corpus_dir`, `new_corpus_dir`); `scripts/gate-canaries.sh`: `g5-crasher-not-retried` |
| 2 | Retry happens at most once per target and at most two targets per run | `scripts/fuzz_test.go`: `TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate`, `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShCapsRetriesAtTwoByDefault`, `TestFuzzShMaxRetriesZeroDisablesRetry`, `TestFuzzShRejectsBadMaxRetries` |
| 3 | A second failure of a retried target fails the run, whatever its message | `scripts/fuzz_test.go`: `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShFailsWhenSecondRunFailsWithOtherMessage`, `TestFuzzShRetryUnreadableSecondRunFailsClosed` |
| 4 | Every retry is visible: RETRY line, report column, NOTE line, and `fuzz_retries` agree | `scripts/fuzz_test.go`: `TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate`, `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShCapsRetriesAtTwoByDefault`, `TestFuzzShRetryUnreadableSecondRunFailsClosed`, `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries`; `scripts/gate-canaries.sh`: `g5-retry-reported` |
| 5 | Nothing else changed (rates, floor, seed-sweep class, fail-closed on unreadable rate, first five columns) | `scripts/fuzz_test.go`: `TestFuzzShReportsEveryTargetWithItsRate`, `TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps`, `TestFuzzShFailsClosedWithoutARate`, `TestFuzzShFailsWhenATargetFails`, `TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne`, `TestFuzzShNamesEveryFailedTargetOnItsLastLine`, `TestFuzzShPrintsNoFailedLineWhenGreen`; `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`: all tests pass (including `TestAdv_6_GateSeedSweepCounts`, `TestAdv_6_UnreadableAfterProgress`, `TestAdv_6_FailedTargetWithGoodRate`, `TestAdv_6_NoProgressFailsClosed`, and updated `TestAdv_6_RealTSVColumns`) |
| 6 | Retry cannot be widened from the environment for a landing run | `scripts/gate.sh` (`env -u FUZZ_GO`, `[[ "$fuzz_max_retries" =~ ^[012]$ ]]` in `counts_for_landing`, exit 2 on malformed/overflow values); `scripts/fuzz_test.go`: `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries` |

## Changes to earlier attack tests — for the reviewer to ratify or reject

- **Commit:** `97ef501` (`adversarial: update CR-1 TSV column expectation from 5 to 6 for CR-2`)
- **File and test:** `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`, `TestAdv_6_RealTSVColumns`
- **Old expectation:** Every row of `FUZZ_REPORT` has `5` tab-separated columns (`len(strings.Split(row, "\t")) != 5`).
- **New expectation:** Every row of `FUZZ_REPORT` has `6` tab-separated columns (`len(strings.Split(row, "\t")) != 6`).
- **Reason:** CR-2 adds the `retried` column (`0` or `1`) at the end of `scripts/fuzz.sh`'s TSV report while keeping the first five columns unchanged. Nothing else under `agent_work/adversarial/` was modified.

## Stated limits

- `is_deadline_signature` whitelists the exact output lines Go 1.26.8's `testing` and
  `internal/fuzz` packages emit during a deadline-only exit. If a future Go version adds a
  new benign progress or warning line, `is_deadline_signature` will fail closed (refuse to
  retry) until updated — it will never silently retry a real failure.
- Corpus detection checks for newly created paths under `<dir>/testdata/fuzz/<target>/`
  (matching how Go writes content-hashed crasher files); it does not hash existing seed
  files for in-place modification.
- `scripts/fuzz.sh` still fails closed if a target exits 0 with no readable `fuzz: elapsed: ..., execs:` line (e.g., `FuzzWorldSchedule` failing to finish baseline coverage in 20 s under heavy host contention); that is not a deadline signature and is not retried.

## Deviations

1. **Full landing gate not run**, per the brief (chunk 6's landing gate was running on the
   host; ran `GATE_ONLY=G0,G1,G2,G5 scripts/gate.sh cr02-fuzz-deadline-retry` as directed,
   and did not commit `result.json`).
2. **`scripts/gate-canaries.sh` clean-copy precondition fails at G3 on `main` (pre-existing, out of scope).**
   Running `CANARY_ONLY=g5-retry-reported,g5-crasher-not-retried scripts/gate-canaries.sh`
   directly fails during its clean-copy prerequisite (`run_gate GATE_STAGE=build` on a fresh
   `git init` copy with no git history) because two earlier adversarial tests read git
   history that the throwaway copy lacks:
   - `agent_work/adversarial/c05-classifier` `TestAdv_9_FirstBuildCorpusWasNotRetired` (`d39ecfb`)
   - `agent_work/adversarial/cr01-shrink-fuzz-rules` `TestAdv_1_FrozenImplementation` (`freeze-sim`)
   Fixing the canary harness's copy mechanism is outside CR-2's scope. To verify both new
   canaries end-to-end against `scripts/gate.sh` and `scripts/fuzz.sh`, I ran a scratch copy
   of `scripts/gate-canaries.sh` under `/tmp/zgw-cr02n/` (deleted afterward) with only the
   clean-copy precondition narrowed to `GATE_ONLY=G0,G1,G2` (`overall == pass`). Both
   canaries were CAUGHT (see below).

## Verification and commands

Commands:

```bash
export PATH=/usr/local/go/bin:$PATH
gofmt -l .
go build ./... && go vet ./...
scripts/ci-fast.sh
go test -count=1 ./scripts/
go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/
GATE_ONLY=G0,G1,G2,G5 scripts/gate.sh cr02-fuzz-deadline-retry
CANARY_ONLY=g5-retry-reported,g5-crasher-not-retried scripts/gate-canaries.sh
```

Results (on commit `664357a`, clean tree):

- `gofmt -l .`: empty; `go build ./... && go vet ./...`: pass; `bash -n` on all edited scripts: pass.
- `scripts/ci-fast.sh`: **PASS** (G0, G1, G3 short unit 148 s, G3 race 18 s).
- `go test -count=1 ./scripts/`: **PASS** (17 top-level tests, 17 subtests, 26.9 s).
- `go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/`: **PASS** (27.6 s, all CR-1 attack tests green).
- `GATE_ONLY=G0,G1,G2,G5 scripts/gate.sh cr02-fuzz-deadline-retry`: **PASS** (362 s total;
  G0, G1, G2 comments against baseline `65567de`, G5 fuzz 352 s: `15 fuzz targets, 20s each:
  14 fuzz, 1 seed-sweep (floor 100/s), 0 without a rate, 0 unclassified, 0 retried`;
  `result.json`: `overall: "pass"`, `dirty_tree: false`, `partial: true`,
  `counts_for_landing: false`, `fuzz_targets: 15`, `fuzz_seed_sweeps: 1`,
  `fuzz_retries: 0`, `fuzz_max_retries: 2`). `result.json` was removed and not committed.
- New canaries (via scratch copy with precondition narrowed to `G0,G1,G2`, 655 s): **2 of 2 CAUGHT, 0 missed**:
  - `g5-retry-reported`: `CAUGHT` (`retried=1, fuzz_retries=2, note='16 fuzz targets, 20s each: 15 fuzz, 1 seed-sweep (floor 100/s), 0 without a rate, 0 unclassified, 2 retried'` — during the run a real repo target also hit Go's live deadline error under host load, was retried once and passed, exercising `is_deadline_signature` against a genuine Go 1.26.8 deadline failure as well as the planted one).
  - `g5-crasher-not-retried`: `CAUGHT` (`red without retry: Failing input written to testdata/fuzz/FuzzCrasher/582528ddfad69eb5`).
