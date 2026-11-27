VERDICT: NITS

Review of CR-2 (`cr02-fuzz-deadline-retry`), Neon build after its one repair round, tip
`1c0171c58428f1985b7faaf1218b7e048e1084c8`, base `779203c7980a8181658e1a2c0fd6762f125ea338`.
Reviewer: Fable. Round 2 (fresh reviewer; I did not write review 1).

No real failure I could produce was retried, and every review 1 finding and BROKEN attack has
the outcome NOTES.md records for it (checked by mutation or by reading, listed below). The
four findings are MINOR; none makes a claim of the brief false.

## Findings

1. **MINOR — the corpus snapshot reports "absent" when it cannot look, in one case.**
   `scripts/fuzz.sh:115-118` (`if [ ! -e "$d" ] && [ ! -L "$d" ]; then printf 'ABSENT\0'`).
   - What is wrong: `[ -e ]` and `[ -L ]` are also false when a parent of
     `testdata/fuzz/<target>` cannot be searched (EACCES) or is not a directory. The snapshot
     then says `ABSENT` before and after, the two compare equal, and the retry goes ahead.
     The script header, `docs/knowledge/foundation-gates.md` and NOTES.md all say the
     snapshot "fails closed if a snapshot cannot be taken".
   - How I know: as uid 65534, a corpus `p/testdata/fuzz/FuzzT/s` with `chmod 000 p/testdata`
     before the run, stub `go` giving the signature once: exit 0, 2 calls, 1 RETRY line
     (cases `nb_parent_unsearchable_both` and `…_both_absent`). The neighbouring cases fail
     closed: unreadable file, unreadable subdirectory, file made unreadable during the run,
     parent made unsearchable during the run (all: exit 1, 1 call, no RETRY).
   - Why it is not a real failure retried: `go test` runs as the same user and cannot write a
     failing input under a directory that user cannot search; as root (how the gate runs here)
     the case does not arise. The log check is independent of the snapshot and still applies.
   - What a fix must achieve: `ABSENT` only when the path is known not to exist (every
     ancestor that exists is a searchable directory); any other reason for not seeing it is
     a failed snapshot. Or narrow the wording to what the code does.

2. **MINOR — the signature check depends on the awk in use; under mawk a NUL byte ends a
   line early.** `scripts/fuzz.sh:141-205` (the three `line ~ /…$/` tests and the
   `substr(...) ~` test).
   - How I know: `--- FAIL: FuzzX (20.83s)\n    context deadline exceeded\0: real\nFAIL\n`
     and `--- FAIL: FuzzX (20.83s)\0junk\n    context deadline exceeded\nFAIL\n` are
     rejected by gawk 5.2.1 and busybox awk and **accepted by mawk 1.3.4** (installed on this
     machine; `awk` resolves to `/usr/bin/gawk`, so the gate here is not affected). Review 1
     reported gawk and mawk identical on its 35 logs; they differ on these two.
   - Why it is not a real failure retried: no failing target I ran with real `go test` puts
     a NUL-terminated copy of the message directly under the header with `FAIL` on the next
     line; a crash prints a nested `--- FAIL` and `Failing input written to`, a `t.Fatal`
     prints `file:line:`. It is forgery only, the same class as review 1 finding 5.
   - What a fix must achieve: the same answer under any awk — for example refuse a log that
     holds a NUL byte before parsing — or state gawk as a requirement of `fuzz.sh`.

3. **MINOR — no test holds "a failed snapshot before the first run blocks the retry" by
   itself.** `scripts/fuzz.sh:228-231,237` (`snap_before_ok`).
   - How I know: mutation D4 (retry allowed when the before-snapshot failed and the
     after-snapshot succeeds) leaves `go test ./scripts/` and both attack suites green. Every
     fail-closed test plants a fault that is still there after the run, so the after-snapshot
     fails too and hides the mutation.
   - The code is right: a broken symlink, a FIFO, or a file in place of the corpus directory
     that is present before the run and removed during it → exit 1, 1 call, no RETRY (my cases
     `before_broken_then_fixed`, `before_fifo_then_removed`, `before_nondir_then_dir`).
   - What a fix must achieve: one test of that shape (a fault before, repaired by the stub's
     first call).

4. **MINOR — NOTES.md is one commit behind on the settled attack test.**
   `agent_work/chunks/cr02-fuzz-deadline-retry/NOTES.md`, "Narrowed claims", the last row of
   "Repair round", "Deviations" 1 and the results list say `TestAdv_2_WidenedStandaloneBudget`
   "stays red by design", the attack suite is "20 of 21 PASS, 1 expected FAIL" and `G3 unit`
   fails. At the tip the orchestrator's commit `1c0171c` skips that test: the suite is 20
   pass, 1 skip, package `ok`. `ADVERSARIAL.md` records it; NOTES.md should point there.

Carried from review 1, not the builder's: `CANARY_ONLY=g5-retry-reported,g5-crasher-not-retried
scripts/gate-canaries.sh` still cannot reach a verdict on this tree (its clean-copy
precondition needs git history the copy lacks; NOTES.md "Deviations" 2). The repair took the
other route review 1 allowed (a fast test for `fuzz_retries`). I did not run the canary
script. The precondition is the orchestrator's to repair.

Checked and found in order (no finding):
- Deliverables 1 to 5 exist and do what the brief says; every listed test exists.
- `retried` is column 6; the first five columns and the header prefix are unchanged.
- The retry is a single second run, not a loop; `retried_total` is checked before the
  signature and counted once per target.
- No comment was added to a Go file (G2 pass against `779203c`; `grep '^+.*//'` over the
  test diff is empty). No builder commit carries a Co-Authored-By line.
- No test writes into the tracked tree any more; `git status --short` was empty after every run.
- `agent_work/adversarial/` in `db1507a..HEAD`: one line, the orchestrator's `t.Skip`.

## Review 1 findings and attacks — status

- Review 1 finding 1 (MAJOR, claim 6 test cannot fail) — **fixed, verified by mutation**: with `&& [[ "$fuzz_max_retries" =~ ^[012]$ ]]` deleted from `gate.sh`, `TestGateShLandingCapBlocksWidenedMaxRetriesAlone` goes red on `3` and `999999999` (and so does `TestAdv_6_LandingPredicateBudgetBoundary`). The test's run has `partial=false`, clean tree, no `GATE_OUT`, and `0,1,2` give `counts_for_landing: true`, so the cap is the only reason for `false`.
- Review 1 finding 2 (MINOR, NOTES names commits not on the branch) — **fixed**: all eight commits in "What was built" are ancestors of the tip; 16 targets and baseline `779203c` match my gate run. One new staleness: finding 4 above.
- Review 1 finding 3 (MINOR, `fuzz_retries` held only by a canary) — **fixed, verified by mutation**: `$6 == "1"` → `$5 == "1"` in `gate.sh` turns `TestGateShCountsFuzzRetriesFromColumnSix` red (and four attack tests). The canary command itself is unchanged (carried note above).
- Review 1 finding 4 (MINOR, a test wrote into tracked directories) — **fixed**: both corpus tests run `fuzz.sh` from a copy in `t.TempDir()` (`runIsolatedFuzzSh`); no path under the repository root is written.
- Review 1 finding 5 (MINOR, duration not compared with `FUZZ_SECONDS`; `TestMain` forgery) — **declined; I accept the decline.** The change request defines the signature in five conditions and a duration is not one of them; the case is output forged by code in the package under test, not a failure of a target; `gate.sh` already calls its result evidence for an honest operator. The second reason given (the attack fixtures print `(2.01s)`) is not a reason by itself, but the first is enough.
- Review 1 finding 6 (MINOR, NOTES claim 4 row overstated) — **fixed**: the row names `TestGateShCountsFuzzRetriesFromColumnSix` for agreement and the G10 test for `null` only.
- Attack `TestAdv_1_ExistingCorpusRewritten` (BROKEN) — **fixed, verified by mutation**: with the hash replaced by a constant, the attack and `…/existing_file_rewritten` go red; at the tip both pass.
- Attack `TestAdv_1_SymlinkedCorpusChild` (BROKEN) — **fixed, verified by mutation**: `find` without `-L` turns the attack and `…/new_file_behind_symlinked_dir` red.
- Attack `TestAdv_1_NewlineDirectoryReplacement` (BROKEN) — **fixed, verified by mutation**: with `scripts/fuzz.sh` put back to its pre-repair text (`db1507a`), this attack, the other two and seven of the eight builder subtests go red; at the tip all pass. (My finer mutation of the delimiter alone was badly made and is not counted.)
- Attack `TestAdv_2_WidenedStandaloneBudget` (BROKEN, skipped by the orchestrator) — **a narrowing, not a defect; I agree.** The brief's own deliverable 1 gives `FUZZ_MAX_RETRIES` a default of 2 and says only that "the gate does not let a caller raise it above 2 for a landing run"; claim 6 says the same. A standalone run with `FUZZ_MAX_RETRIES=3` retrying three targets is what the deliverable describes. The narrowed wording is exact and the code keeps it: `fuzz.sh:240` caps at the variable; `gate.sh:442` refuses to count any value outside `0,1,2`; `gate.sh:231` passes the validated value on. The wording in `foundation-gates.md` matches.
- The 17 HELD attacks — still pass at the tip (`go test -v` on the attack directory: 20 PASS, 1 SKIP).

## Ratify or reject: the change to CR-1's attack test

**RATIFIED** (my own reading; review 1 reached the same). Commit `a5082a8`,
`adversarial: update CR-1 TSV column expectation from 5 to 6 for CR-2`.

- `git diff 779203c..HEAD --stat -- agent_work/adversarial/cr01-shrink-fuzz-rules`: one file,
  2 insertions, 2 deletions, all in `TestAdv_6_RealTSVColumns`: `n != 5` → `n != 6` and the
  message `want 5` → `want 6`.
- Minimum: the test asserts only the width of each report row; the report has six columns;
  nothing smaller passes.
- Weakens nothing: still an equality, still every row, the rest of the test and of the file
  untouched. The CR-1 suite passes at the tip (35.7 s inside `go test ./...`), and went red
  only where expected under my mutations.
- Own commit, `adversarial:` subject, listed in NOTES.md under the required heading with
  old value, new value and reason. The repair round changed nothing under
  `agent_work/adversarial/`.

## Signature attacks (brief item 7)

No real failure was retried. All scratch work under `/tmp/zgw-cr02n-review2/`.

Real `go test -fuzz` (go1.26.8) through the real `scripts/fuzz.sh`, `FUZZ_SECONDS=3`,
`FUZZ_MAX_RETRIES=99` so the cap could not hide a retry; 9 targets, 0 RETRY lines, every row
`retried=0`, run exit 1:

| Try | What the target does | Result |
| --- | --- | --- |
| crash whose message is the text | `t.Fatal("context deadline exceeded")` on a mutated input | not retried (nested `--- FAIL`, `file:line:`, failing-input line; corpus changed) |
| the same on the seed | no file written | not retried (`failure while testing seed corpus entry`) |
| failing input written elsewhere | `TestMain` does `os.Chdir` to another directory, target fails; Go wrote `…/elsewhere/testdata/fuzz/FuzzC/06ba…`, outside the snapshot | not retried: the log has `fuzz: minimizing`, a nested `--- FAIL` and `Failing input written to` |
| panic | `panic(context.DeadlineExceeded)` | not retried |
| forged lines then exit | worker prints the three signature lines and `os.Exit(1)` | not retried (`fuzzing process hung or terminated unexpectedly`) |
| build failure | does not compile | not retried |
| output order | `f.Cleanup` prints the bare message and `f.Fail()` | not retried (text before the header, no message under it) |
| carriage return | `f.Error("\rcontext deadline exceeded")` after `f.Fuzz` | not retried (`x_test.go:6: ^Mcontext…`) |
| bare text then `FailNow` | worker writes the bare message to stdout, `t.FailNow()` | not retried |
| timeout kill | `GOFLAGS=-timeout=2s`, 6 s fuzz | Go ignores `-timeout` while fuzzing: PASS, retry not consulted |
| killed from outside | `go test` and its children sent SIGKILL at 3 s | not retried (rc 137, no FAIL block; row `unreadable`) |

Synthetic logs through `is_deadline_signature` (extracted verbatim), 57 files, each under
gawk, mawk and busybox awk: CRLF on all lines; CR on the message line; a real message
overwritten with `\r`; message with prefix, with suffix; failing-input line before the
header, between, and after `FAIL`; `FAIL` first; message before header; subtest header;
nested header; two blocks (adjacent, before, after `FAIL`); `[build failed]`, `[setup
failed]`; `panic: test timed out` before and after; `signal: killed`; `exit status 2`; two
`exit status 1`; `exit status 1` after the package line; two package lines; text after the
package duration; `FuzzXY` for `FuzzX`; data-race lines before and inside the block;
`fuzz: minimizing`; junk after a progress line; blank line inside the block; no indent;
trailing space on message and on `FAIL`; `ok` after; no `FAIL`; `=== RUN` of another
target; `PASS` first; a `t.Log` line; empty; non-breaking spaces; ANSI escape; form feed;
whitespace-only lines; NUL bytes in four positions; the target given as `Fuzz.` and as
`Fuzz`. Accepted, all three awks: the canonical output, the same with no final newline,
with no duration, with a tab indent, with `=== RUN   FuzzX` first, and the deadline during
baseline coverage — all genuine. Accepted by mawk only: two NUL cases (finding 2).

Whether real `go test … ./<dir>` ends with lines the whitelist rejects: checked with a
failing fuzz target; the tail is `FAIL`, `exit status 1`, `FAIL<TAB>pkg<TAB>2.068s` and
nothing after, which is what state 3 allows. No captured log of a genuine deadline error
exists on this machine outside the parked branch (which I did not read); the format in the
change request matches what `testing/fuzz.go` prints for a non-crash error.

## Corpus snapshot attacks (the repair's new code)

Through the real `fuzz.sh` with a stub `go` that changes the corpus during the first call
and prints the signature; "blocked" = exit 1, 1 call, no RETRY.

- Blocked (29): file added (existing directory, fresh directory, hidden name, name `-n`,
  name with a newline, three levels down, behind a symlinked subdirectory); empty directory created at the root; empty subdirectory added; content
  rewritten with the same size and mtime; `a` → `a\0`; rename; remove; two files swapping
  contents; names differing in a space, a backslash, a glob bracket; a directory replaced by
  a file of the same name (with and without content); corpus root a symlink with a new file in its
  target; `testdata/fuzz` a symlink with a new file; root symlink retargeted to different
  content; file symlink retargeted; file symlink's target rewritten; FIFO present; FIFO
  added; broken symlink added; root replaced by a file; root removed.
- Retried (7), each with no file added, removed or changed in content: unchanged corpus;
  no corpus; `chmod` only; `touch` only; root symlink retargeted to a tree with identical
  content; a file replaced by a symlink or hard link to identical content. The stated rule
  is content, so these are within it.
- Fail open, tools: `sha256sum`, `find`, `cmp`, `awk` each replaced by a program that exits
  127, and `sha256sum` that prints nothing and exits 0 → blocked every time (a broken
  `sort` stops `fuzz.sh` before any target, so it says nothing).
- Fail open, permissions (uid 65534): four cases blocked, two retried — finding 1.
- Fault present before the run and gone after: blocked (finding 3 is about the missing test).
- By reading: `return 1` inside the `{ … } > "$out"` group returns from the function (the
  group is not a subshell); `rm -f "$out"` at entry means a stale snapshot is never
  compared; the `D`/`F` records cannot be confused because NUL cannot occur in a path and
  every path starts `./`; `LC_ALL=C sort -z` makes the order independent of `find`.

## Mutation checks

Clone of the tip under `/tmp/zgw-cr02n-review2/mut`; each mutation alone; then
`go test -count=1 ./scripts/ ./agent_work/adversarial/cr02-fuzz-deadline-retry/ ./agent_work/adversarial/cr01-shrink-fuzz-rules/`.

| # | Property broken | Caught by (builder's tests; attack tests in brackets) |
| --- | --- | --- |
| M10 | landing cap removed from `gate.sh` | `TestGateShLandingCapBlocksWidenedMaxRetriesAlone` (`=3`, `=999999999`) [`TestAdv_6_LandingPredicateBudgetBoundary`] |
| M11 | `fuzz_retries` counted from column 5 | `TestGateShCountsFuzzRetriesFromColumnSix` [4 attack tests] |
| A | `fuzz.sh` as before the repair | 7 subtests of `TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors` [the 3 formerly BROKEN attacks] |
| B | content hash constant | `…/existing_file_rewritten` [`TestAdv_1_ExistingCorpusRewritten`] |
| C | `find` without `-L` | `…/new_file_behind_symlinked_dir`, `…/snapshot_fails_closed_on_symlink_loop` [`TestAdv_1_SymlinkedCorpusChild`] |
| D1 | an entry that is neither file nor directory is recorded, not refused | `…/snapshot_fails_closed_on_broken_symlink` |
| D2 | a `find` error is ignored | `…/snapshot_fails_closed_on_symlink_loop` |
| D3 | a corpus root that is not a directory counts as absent | `…/snapshot_fails_closed_on_non_dir_corpus_path` |
| D4 | **a failed before-snapshot no longer blocks when the after-snapshot succeeds** | **nothing — finding 3** |
| O | corpus condition dropped from the retry test | both `…NewCorpusFileAppears` subtests and 7 of the snapshot subtests [4 attack tests] |
| G | no cap on retried targets | `TestFuzzShCapsRetriesAtTwoByDefault`, `TestFuzzShMaxRetriesZeroDisablesRetry` [2] |
| H | a failed second run is not a failure | `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShFailsWhenSecondRunFailsWithOtherMessage` [2] |
| I | message matched anywhere on its line | `…NearMisses/file_line_prefixed_deadline` |
| K | any non-`FAIL` text allowed after `FAIL` | `…NearMisses/exit_status_2_after_fail` [2] |
| N | header of any `Fuzz…` target accepted | no near-miss test went red by itself; the mutation also broke the genuine case (8 tests red), so it says nothing about `wrong_target_name` — not counted |
| L | `retried` column always 0 | 6 tests incl. `TestGateShCountsFuzzRetriesFromColumnSix` [3] |
| J | rate read from the first run's log | 6 tests incl. `TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate` |

Counted: 16 informative mutations, 15 caught, 1 not (D4). Not counted: N (above), my first
form of J (an equivalent mutant: both runs wrote the same log file) and D5 (a delimiter
mutation that did not do what I meant).

## Release It! checklist

The brief rules that PLAN.md section 10.3 does not apply (gate scripts, no gateway code). One
line each, for the format:

- Integration points — not applicable: no connection; the one external process is `go test`, bounded by `-fuzztime`.
- Blocked threads — not applicable: no pool or lock; the temp directory is removed by the EXIT trap.
- Slow responses — not applicable: no request path. The change adds at most `FUZZ_MAX_RETRIES × FUZZ_SECONDS` (40 s by default) and two walks of a target's corpus tree per target (12 corpus files in the whole tree today).
- Cascading failures — not applicable.
- Chain reactions — not applicable.
- Dogpile — not applicable.
- Self-denial attacks — not applicable: targets and retries run one after another.
- Force multiplier — the retry is the one automatic repeat: one second run per target (no loop), at most `FUZZ_MAX_RETRIES` targets per run, ceiling of 2 for a run that counts; each bound has a test that fails under mutation (G, H, M10).
- Unbalanced capacities — not applicable.
- Scaling effects — not applicable.
- Users — not applicable.
- Unbounded result sets — not applicable: one log per run of a target and one snapshot pair, overwritten per target, in a temp directory. The snapshot grows with the corpus tree it lists; that tree is the repository's own.
- Compounds — a retry on a path that can hide a failure is the pair that matters; the signature and snapshot attacks above are the answer.

## Observability

PLAN.md section 7.6 does not apply (no Go component, no `Snapshot()`). The equivalent here
is claim 4. `TestGateShCountsFuzzRetriesFromColumnSix` drives the real `gate.sh` and
`fuzz.sh` and asserts RETRY lines = 2, rows with `retried=1` = 2, NOTE `, 2 retried`,
`fuzz_retries = 2`, with `fuzz_targets = 4` and `fuzz_seed_sweeps = 1` so a wrong column
cannot give the right number; mutations M11 and L turn it red. `fuzz_retries` is `null`
when G5 does not run (the G10 test). A retried target that then has no readable rate or
fails keeps `retried=1`, so the count still agrees on a red run
(`TestFuzzShRetryUnreadableSecondRunFailsClosed`, `TestFuzzShFailsWhenDeadlineSignatureRepeats`).

Fuzz targets (PLAN.md section 6.6): the change adds or alters no fuzz target in the product
tree; the two canary targets and the test fixtures run no simulator.

## What I ran

All with `PATH=/usr/local/go/bin:$PATH`; nothing in the repository was changed
(`git status --short` empty at the end). Load average was 12 to 18 on 4 cores throughout
(three other builds, plus my own runs side by side).

| Command | Result |
| --- | --- |
| `gofmt -l .`; `go build ./... && go vet ./...` | no output; pass |
| `go test -count=1 ./...` (while the gate and the mutations ran) | **rc=1**, 680 s: 40 packages ok, 2 failed, both untouched by this change (`git diff --stat 779203c..HEAD -- internal` is empty): `internal/obs/simrun` hit the 10-minute test timeout; `internal/pgwire` failed `TestLexOperatorRunsGrowLinearly` and `TestClassifierSpanKeywordChecksGrowLinearly` (timing tests). `scripts` ok 100.3 s; both attack suites ok |
| reruns of the two | `internal/obs/simrun` alone with `-timeout 30m`: ok, 592.5 s (under 10 minutes by 7 s at this load). `internal/pgwire`: failed `TestLexOperatorRunsGrowLinearly` once more beside that run, then ok (14.3 s); the two timing tests alone `-count=4`: ok. Load, not this change; the orchestrator should expect G3 to be sensitive to it |
| `go test -count=1 -v ./scripts/` | ok; 20 tests, 30 subtests, none failing |
| `go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/` | ok; 20 PASS, 1 SKIP (`TestAdv_2_WidenedStandaloneBudget`) |
| `GATE_ONLY=G0,G1,G2,G5 GATE_OUT=/tmp/zgw-cr02n-review2/gate scripts/gate.sh cr02-fuzz-deadline-retry` (once) | overall pass, 506 s; G5 453 s: `16 fuzz targets, 20s each: 14 fuzz, 2 seed-sweep (floor 100/s), 0 without a rate, 0 unclassified, 0 retried`; `fuzz_retries: 0`, `fuzz_max_retries: 2`, `counts_for_landing: false` (partial). No genuine deadline error occurred, so the retry was not exercised against a live one |
| 19 mutation runs in a clone, three test packages each | table above: 15 of 16 informative ones caught |
| 9 failing real targets, a timeout and a SIGKILL through the real `fuzz.sh` | table above; 0 retried |
| 57 synthetic logs × 3 awks | table above; 6 genuine forms accepted; 2 more under mawk only |
| 36 corpus cases, 5 broken-tool cases, 7 unprivileged cases (one a control), 3 fault-before cases with a stub `go` | lists above |

Not run:
- `scripts/gate-canaries.sh` (its precondition is a full build-stage gate; and the brief
  allows one partial gate run). I read both new canaries: `g5-retry-reported` requires gate
  exit 0, the planted first run to have fired, the RETRY line, the row `retried=1`,
  `fuzz_retries` equal to the table count and to the NOTE; `g5-crasher-not-retried`
  requires exit 1, `overall=fail`, the crasher text, `Failing input written to`, no RETRY
  line and `retried=0`. Neither can pass vacuously by reading.
- The full gate.
- Extra seeds / `scripts/sweep.sh`: the change touches no simulator scenario.
- The parked branch `work/cr02-fuzz-deadline-retry` was not read.
