# CR-2 — one retry in `scripts/fuzz.sh` for Go's own deadline error: builder notes

Rebuilt by Neon on 2026-11-27 on branch `work/cr02n-fuzz-deadline-retry` (worktree
`/root/Software/mini-gateway-work/cr02n`, base `779203c`) from
`/root/briefs/zgateway-p2-cr02n-fuzz-deadline-retry.md` under Nick's ruling
(`/root/briefs/zgateway-cr02-neon-rebuild-ruling.md`), and repaired from
`/root/briefs/zgateway-p2-cr02n-repair.md`.

## What was built

| Deliverable | Where | Commit |
| --- | --- | --- |
| `fuzz_retries` and `fuzz_max_retries` in `result.json`; landing cap `<= 2`; `ci-fast.sh` env unset | `scripts/gate.sh`, `scripts/ci-fast.sh` | `8b91644` |
| Exact 5-condition deadline signature check, single retry per target, `FUZZ_MAX_RETRIES` cap (default 2, 0 disables), `fuzz: RETRY:` line, 6th `retried` TSV column, NOTE count, and `FUZZ_GO` tests | `scripts/fuzz.sh`, `scripts/fuzz_test.go` | `86116a1` |
| CR-1 TSV column-width expectation updated from 5 to 6 | `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go` | `a5082a8` |
| Gate canaries `g5-retry-reported` and `g5-crasher-not-retried`; `FUZZ_MAX_RETRIES` unset | `scripts/gate-canaries.sh` | `22a80ee` |
| G5 deadline-retry documentation | `docs/knowledge/foundation-gates.md` | `23b18eb` |
| Repair: NUL-safe corpus-tree snapshot through symlinks with SHA-256 content hashes (fail closed on snapshot error); isolated temp-dir corpus tests | `scripts/fuzz.sh`, `scripts/fuzz_test.go` | `e6e8b17` |
| Repair: fast real-`gate.sh` tests for the `FUZZ_MAX_RETRIES <= 2` landing cap and column-6 `fuzz_retries` counting | `scripts/fuzz_test.go` | `3027fab` |
| Repair: updated G5 corpus-snapshot and retry-cap wording in docs | `docs/knowledge/foundation-gates.md` | `cb74d4a` |

## Use of parked branch `work/cr02-fuzz-deadline-retry`

The implementation, tests, canaries, and docs were built independently without reading any
code or diffs from `work/cr02-fuzz-deadline-retry`. During the initial build, after
encountering the pre-existing `scripts/gate-canaries.sh` clean-copy G3 failure (`d39ecfb`
and `freeze-sim` absent in the throwaway `git init` copy), the initial builder read only
`NOTES.md` on that branch to confirm the precondition failure was already observed as out
of scope. No code, tests, or documentation were copied or adapted from any parked branch.
During the repair round, no parked branch was read at all.

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
2. **Corpus-change detection (`snapshot_corpus` + `cmp -s`).**
   Before attempt 1, `scripts/fuzz.sh` takes a NUL-delimited snapshot of
   `<dir>/testdata/fuzz/<target>/` (`ABSENT\0` if absent, otherwise `PRESENT\0` followed by
   `find -L . -mindepth 1 -print0 | LC_ALL=C sort -z`, emitting `D\0<path>\0` for
   directories and `F\0<path>\0<sha256>\0` for regular files using `sha256sum < file`).
   On non-zero exit it snapshots again and blocks retry unless both snapshots succeeded and
   `cmp -s` reports byte-for-byte equality. Any added, removed, or content-modified file
   (including behind symlinked directories or under filenames containing newlines) blocks
   retry, and any snapshot error (broken symlink, symlink loop, non-directory corpus root,
   special file, unreadable file, missing tool) fails closed with no retry.
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
| 1 | A real failure is never retried (failing input, panic, other failure text, or any change/error snapshotting the target's corpus tree fails at once) | `scripts/fuzz_test.go`: `TestFuzzShDoesNotRetryNearMisses` (11 subtests), `TestFuzzShDoesNotRetryWhenNewCorpusFileAppears` (`existing_corpus_dir`, `new_corpus_dir`), `TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors` (8 subtests: `existing_file_rewritten`, `existing_file_removed`, `new_file_behind_symlinked_dir`, `newline_directory_replacement`, `snapshot_fails_closed_on_broken_symlink`, `snapshot_fails_closed_on_symlink_loop`, `snapshot_fails_closed_on_non_dir_corpus_path`, `unchanged_corpus_with_symlink_still_retries`); `scripts/gate-canaries.sh`: `g5-crasher-not-retried` |
| 2 | Retry happens at most once per target and at most two targets per run (narrowed form under "Narrowed claims") | `scripts/fuzz_test.go`: `TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate`, `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShCapsRetriesAtTwoByDefault`, `TestFuzzShMaxRetriesZeroDisablesRetry`, `TestFuzzShRejectsBadMaxRetries`, `TestGateShLandingCapBlocksWidenedMaxRetriesAlone` |
| 3 | A second failure of a retried target fails the run, whatever its message | `scripts/fuzz_test.go`: `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShFailsWhenSecondRunFailsWithOtherMessage`, `TestFuzzShRetryUnreadableSecondRunFailsClosed` |
| 4 | Every retry is visible: RETRY line, report column, NOTE line, and `fuzz_retries` agree (and `fuzz_retries` is `null` when G5 did not run) | `scripts/fuzz_test.go`: `TestGateShCountsFuzzRetriesFromColumnSix` (proves agreement across `fuzz: RETRY:`, TSV column 6, NOTE, and `result.json` `.fuzz_retries` on real `gate.sh` + `fuzz.sh`, with distinct `fuzz_targets=4`, `fuzz_seed_sweeps=1`, `fuzz_retries=2`), `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries` (proves `fuzz_retries` is `null` when G5 is skipped), `TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate`, `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShCapsRetriesAtTwoByDefault`, `TestFuzzShRetryUnreadableSecondRunFailsClosed`; `scripts/gate-canaries.sh`: `g5-retry-reported` |
| 5 | Nothing else changed (rates, floor, seed-sweep class, fail-closed on unreadable rate, first five columns) | `scripts/fuzz_test.go`: `TestFuzzShReportsEveryTargetWithItsRate`, `TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps`, `TestFuzzShFailsClosedWithoutARate`, `TestFuzzShFailsWhenATargetFails`, `TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne`, `TestFuzzShNamesEveryFailedTargetOnItsLastLine`, `TestFuzzShPrintsNoFailedLineWhenGreen`; `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`: all tests pass (including `TestAdv_6_GateSeedSweepCounts`, `TestAdv_6_UnreadableAfterProgress`, `TestAdv_6_FailedTargetWithGoodRate`, `TestAdv_6_NoProgressFailsClosed`, and updated `TestAdv_6_RealTSVColumns`) |
| 6 | Retry cannot be widened from the environment for a landing run | `scripts/fuzz_test.go`: `TestGateShLandingCapBlocksWidenedMaxRetriesAlone` (drives real `scripts/gate.sh` with all other landing prerequisites true; passes for `FUZZ_MAX_RETRIES=0,1,2` with `counts_for_landing: true` and asserts `counts_for_landing: false` for `3` and `999999999`; verified red when `&& [[ "$fuzz_max_retries" =~ ^[012]$ ]]` is removed in a scratch copy), `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries` (exit 2 on malformed/overflow values); `scripts/gate.sh` (`env -u FUZZ_GO`, `[[ "$fuzz_max_retries" =~ ^[012]$ ]]`) |

## Narrowed claims

- **Claim 2** (attack `TestAdv_2_WidenedStandaloneBudget`): the retry happens at most once
  per target, and at most two targets per run by default and on any run that counts for
  landing (`counts_for_landing: true`). A standalone `scripts/fuzz.sh` (or non-landing
  `scripts/gate.sh`) run may set `FUZZ_MAX_RETRIES` above 2 and is then not eligible for
  landing (`counts_for_landing: false`); the two-target ceiling is a property of a run that
  counts. Code is unchanged; `TestAdv_2_WidenedStandaloneBudget` tests the unqualified
  standalone ceiling and stays red by design until settled by the orchestrator.

## Repair round

Review 1 (`REVIEW-1.md`) and adversarial results (`agent_work/adversarial/cr02-fuzz-deadline-retry/RESULTS.md`):

| Item | Outcome |
| --- | --- |
| Review 1 (MAJOR) claim 6 test cannot fail when landing cap is removed (M10) | **fixed** (`3027fab`), regression test `TestGateShLandingCapBlocksWidenedMaxRetriesAlone` in `scripts/fuzz_test.go` driving real `scripts/gate.sh` in a clean temp git repo with all other landing prerequisites true (`FUZZ_MAX_RETRIES=0,1,2` -> `counts_for_landing: true`; `3` and `999999999` -> `false`). Proven red in scratch copy with `&& [[ "$fuzz_max_retries" =~ ^[012]$ ]]` removed (`counts_for_landing = true, want false` on `3` and `999999999`). Named in Claim 6 row. |
| Review 2 (MINOR) NOTES.md names pre-rebase commits and stale target/baseline numbers | **fixed** (this NOTES.md commit): "What was built", "Changes to earlier attack tests" (`a5082a8`), and "Verification and commands" now name commits on `work/cr02n-fuzz-deadline-retry` and numbers from this tree (16 fuzz targets, baseline `779203c`). |
| Review 3 (MINOR) `fuzz_retries` in `gate.sh` held only by canary (M11) | **fixed** (`3027fab`), regression test `TestGateShCountsFuzzRetriesFromColumnSix` in `scripts/fuzz_test.go` driving real `scripts/gate.sh` + `scripts/fuzz.sh` with distinct `fuzz_targets=4`, `fuzz_seed_sweeps=1`, `fuzz_retries=2` (and verifying `fuzz: RETRY:`, column 6, NOTE, and `counts_for_landing: true`). Proven red in scratch copy when `$6 == "1"` is changed to `$5 == "1"` (`fuzz_retries = 0, want 2`). |
| Review 4 (MINOR) corpus test writes into tracked source directories | **fixed** (`e6e8b17`): `TestFuzzShDoesNotRetryWhenNewCorpusFileAppears` refactored onto `runIsolatedFuzzSh` (`t.TempDir()` outside the tracked tree); no test mutates tracked directories. |
| Review 5 (MINOR) signature does not check `--- FAIL` duration against `FUZZ_SECONDS` (`TestMain` forgery) | **declined**: deliberate output forgery by `TestMain` is not a real fuzz failure, and enforcing `duration >= FUZZ_SECONDS` would break the synthetic fixtures in `agent_work/adversarial/cr02-fuzz-deadline-retry/adv_test.go` (which emit `(2.01s)` under the default `FUZZ_SECONDS=20` and must not be modified). |
| Review 6 (MINOR) NOTES.md claim 4 row overstated what `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries` proves | **fixed** (this NOTES.md commit): Claim 4 row now cites `TestGateShCountsFuzzRetriesFromColumnSix` for agreement and accurately describes `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries` as proving `null` when G5 is skipped. |
| Attack `TestAdv_1_ExistingCorpusRewritten` (BROKEN) | **fixed** (`e6e8b17`), regression test `TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors/existing_file_rewritten` (failed before fix: exit 0, 2 calls; passes after). Adversarial test now passes. |
| Attack `TestAdv_1_SymlinkedCorpusChild` (BROKEN) | **fixed** (`e6e8b17`), regression test `TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors/new_file_behind_symlinked_dir` (failed before fix: exit 0, 2 calls; passes after). Adversarial test now passes. |
| Attack `TestAdv_1_NewlineDirectoryReplacement` (BROKEN) | **fixed** (`e6e8b17`), regression test `TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors/newline_directory_replacement` (failed before fix: exit 0, 2 calls; passes after). Also tested file deletion and fail-closed snapshot errors (broken symlink, symlink loop, non-dir corpus root). Adversarial test now passes. |
| Attack `TestAdv_2_WidenedStandaloneBudget` (BROKEN) | **narrowed** (Claim 2 under "Narrowed claims"; `docs/knowledge/foundation-gates.md` updated in `cb74d4a`). Code unchanged; adversarial test stays red by design until settled by the orchestrator. |

## Changes to earlier attack tests — for the reviewer to ratify or reject

- **Commit:** `a5082a8` (`adversarial: update CR-1 TSV column expectation from 5 to 6 for CR-2`) — ratified in `REVIEW-1.md`.
- **File and test:** `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`, `TestAdv_6_RealTSVColumns`
- **Old expectation:** Every row of `FUZZ_REPORT` has `5` tab-separated columns (`len(strings.Split(row, "\t")) != 5`).
- **New expectation:** Every row of `FUZZ_REPORT` has `6` tab-separated columns (`len(strings.Split(row, "\t")) != 6`).
- **Reason:** CR-2 adds the `retried` column (`0` or `1`) at the end of `scripts/fuzz.sh`'s TSV report while keeping the first five columns unchanged. Nothing else under `agent_work/adversarial/` was modified (including during the repair round).

## After review 2 (NITS; closed by the orchestrator)

- `TestAdv_2_WidenedStandaloneBudget` is no longer red: the orchestrator settled it with a
  skip at `1c0171c` (claim 2 narrowed; `ADVERSARIAL.md`). The attack suite is 20 pass,
  1 skip, and `G3 unit` passes. Lines in this file that say "stays red by design" or
  "20 of 21 PASS, 1 expected FAIL" describe the tree before that commit.
- Review 2 findings 1 and 2 are accepted as stated limits (below, and in the header of
  `scripts/fuzz.sh`); no code changed after review 2.
- Review 2 finding 3 is open: no test holds "a failed snapshot BEFORE the first run blocks
  the retry" by itself (the code is right; mutation D4 survives). Carried in STATE.md.

## Stated limits

- A corpus directory whose parent cannot be searched at all (before and after the run) is
  snapshotted as absent, not as a failed snapshot, so the retry is not blocked by the
  snapshot in that one case. `go test` runs as the same user and cannot write a failing
  input there; the log conditions still apply. (Review 2 finding 1.)
- The signature test is written for gawk (`awk` on the gate's machine). Under mawk a NUL
  byte ends a line early and two forged logs that gawk refuses are accepted. (Review 2
  finding 2.)

- `is_deadline_signature` whitelists the exact output lines Go 1.26.8's `testing` and
  `internal/fuzz` packages emit during a deadline-only exit. If a future Go version adds a
  new benign progress or warning line, `is_deadline_signature` will fail closed (refuse to
  retry) until updated — it will never silently retry a real failure.
- `snapshot_corpus` fails closed (refuses to retry) if `testdata/fuzz/<target>/` contains a
  broken symlink, symlink loop, special file, non-directory root, or unreadable entry, or if
  `sha256sum` / `find -L` / `sort -z` / `cmp` fails.
- `scripts/fuzz.sh` still fails closed if a target exits 0 with no readable `fuzz: elapsed: ..., execs:` line (e.g., a slow target failing to finish baseline coverage in 20 s under heavy host contention); that is not a deadline signature and is not retried.

## Deviations

1. **Full landing gate not run**, per the repair brief (a full `scripts/gate.sh` run takes
   about 40 minutes and other agents share this host; ran
   `GATE_ONLY=G0,G1,G2,G3,G5 scripts/gate.sh cr02-fuzz-deadline-retry` as directed, and did
   not commit `result.json`; the orchestrator runs the full gate at landing).
2. **`scripts/gate-canaries.sh` clean-copy precondition fails at G3 on `main` (pre-existing, out of scope).**
   Running `CANARY_ONLY=g5-retry-reported,g5-crasher-not-retried scripts/gate-canaries.sh`
   directly fails during its clean-copy prerequisite (`run_gate GATE_STAGE=build` on a fresh
   `git init` copy with no git history) because two earlier adversarial tests read git
   history that the throwaway copy lacks:
   - `agent_work/adversarial/c05-classifier` `TestAdv_9_FirstBuildCorpusWasNotRetired` (`d39ecfb`)
   - `agent_work/adversarial/cr01-shrink-fuzz-rules` `TestAdv_1_FrozenImplementation` (`freeze-sim`)
   Both new canaries were verified in the initial build via a scratch copy of
   `scripts/gate-canaries.sh` with the precondition narrowed to `G0,G1,G2` (2 of 2 CAUGHT),
   and in the repair round `fuzz_retries` counting and the landing cap are additionally held
   directly by fast real-`gate.sh` tests in `scripts/fuzz_test.go`.

## Verification and commands

Commands:

```bash
export PATH=/usr/local/go/bin:$PATH
gofmt -l .
go build ./... && go vet ./...
go test -count=1 ./scripts/
go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/
go test -count=1 ./agent_work/adversarial/cr02-fuzz-deadline-retry/
GATE_ONLY=G0,G1,G2,G3,G5 scripts/gate.sh cr02-fuzz-deadline-retry
```

Results (repair round, on clean commit `cb74d4a`, baseline `779203c`):

- `gofmt -l .`: empty; `go build ./... && go vet ./...`: pass; `bash -n` on `scripts/fuzz.sh`, `scripts/gate.sh`, `scripts/gate-canaries.sh`, `scripts/ci-fast.sh`: pass.
- `go test -count=1 -v ./scripts/`: **PASS** (20 top-level tests, 30 subtests, 66.1 s).
- `go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/`: **PASS** (14.7 s, all CR-1 attack tests green).
- `go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/`: **20 of 21 PASS**, 1 expected FAIL (`TestAdv_2_WidenedStandaloneBudget`, narrowed Claim 2; 9.7 s). All three formerly broken corpus attacks (`TestAdv_1_ExistingCorpusRewritten`, `TestAdv_1_SymlinkedCorpusChild`, `TestAdv_1_NewlineDirectoryReplacement`) now PASS.
- Scratch mutation proofs (under `/tmp/zgw-cr02n/`, deleted afterward):
  - M10 (delete `&& [[ "$fuzz_max_retries" =~ ^[012]$ ]]` from `scripts/gate.sh`): `TestGateShLandingCapBlocksWidenedMaxRetriesAlone` **FAIL** (`FUZZ_MAX_RETRIES=3` and `999999999`: `counts_for_landing = true, want false`, rc=1).
  - M11 (change `$6 == "1"` to `$5 == "1"` in `scripts/gate.sh`): `TestGateShCountsFuzzRetriesFromColumnSix` **FAIL** (`fuzz_retries = 0, want 2`, rc=1).
- `GATE_ONLY=G0,G1,G2,G3,G5 scripts/gate.sh cr02-fuzz-deadline-retry` (1,090 s total, `dirty_tree: false`, `stage: "build"`, `partial: true`, `comment_baseline: "779203c7980a8181658e1a2c0fd6762f125ea338"`):
  - `G0` (tools, gofmt, build, vet, tools-vet, tools-test, tidy): **PASS**
  - `G1` (detlint, importlint, boundlint, paniccheck): **PASS**
  - `G2` (comments against `779203c`): **PASS**
  - `G3 unit`: **FAIL** by design of the repair round (598 s): 41 packages `ok`, 5 packages `[no test files]`, 1 package `FAIL` (`agent_work/adversarial/cr02-fuzz-deadline-retry`), with the single failing test `TestAdv_2_WidenedStandaloneBudget` (narrowed Claim 2; `agent_work/adversarial/` must not be edited by the repairer).
  - `G3 race`: **PASS** (35 s, 3 package patterns).
  - `G5 fuzz`: **PASS** (429 s): `16 fuzz targets, 20s each: 14 fuzz, 2 seed-sweep (floor 100/s), 0 without a rate, 0 unclassified, 0 retried` (`fuzz_targets: 16`, `fuzz_seed_sweeps: 2`, `fuzz_retries: 0`, `fuzz_max_retries: 2`).
  - `result.json` was removed and not committed.
