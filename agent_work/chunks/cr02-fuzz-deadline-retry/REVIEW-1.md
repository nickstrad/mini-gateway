VERDICT: FAIL

Review of CR-2 (`cr02-fuzz-deadline-retry`), Neon build, tip `b4ede4cf1eada7a7d6a2de9b0153432c4d0012d9`,
base `779203c7980a8181658e1a2c0fd6762f125ea338`. Reviewer: Fable. Round 1.

The verdict rests on finding 1 alone: the only automated check of claim 6 cannot fail. I found
no defect in the product scripts: no real failure I could produce was retried (see "Signature
attacks"). Findings 2 to 6 are MINOR.

## Findings

1. **MAJOR — the test named for claim 6 cannot fail.**
   `scripts/fuzz_test.go`, `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries`
   (the `counts_for_landing` assertion near line 800); the code it is meant to hold is
   `scripts/gate.sh:442` (`&& [[ "$fuzz_max_retries" =~ ^[012]$ ]]`).
   - What is wrong: the test runs the gate with `GATE_ONLY=G10` and `GATE_OUT=<tmp>` and
     `FUZZ_MAX_RETRIES=3`, then asserts `counts_for_landing == false`. `GATE_ONLY` makes
     `partial=true` and `GATE_OUT` makes `custom_out=true`; each alone forces
     `counts_for_landing=false`, whatever `FUZZ_MAX_RETRIES` is. NOTES.md lists this test as the
     proof of claim 6 ("the retry cannot be widened from the environment for a landing run").
   - How I know: mutation M10 — in a scratch copy I deleted
     `&& [[ "$fuzz_max_retries" =~ ^[012]$ ]]` from `gate.sh`; `go test -count=1 ./scripts/`
     stayed green (rc=0, no failing test). No canary covers it either (no control in
     `scripts/gate-canaries.sh` runs with `FUZZ_MAX_RETRIES` above 2).
   - The code itself is right by reading: values 0, 1, 2 count; 3 and above never count;
     malformed values exit 2 (checked: `1000000000` and `" 2"` give exit 2; `3` runs and
     records `fuzz_max_retries: 3`).
   - What a fix must achieve: a check that goes red when the cap condition is removed from
     `gate.sh`. The repository already has the pattern: the "Controls on counts_for_landing"
     block of `scripts/gate-canaries.sh` (`dirty-tree-does-not-count`), a full green
     build-stage run in the copy that must say `pass … counts_for_landing false`. A control
     of the same shape with `FUZZ_MAX_RETRIES=3` (and nothing else that disqualifies the run)
     would do; so would any other test in which the cap is the only reason the run does not
     count. NOTES.md's claim-6 row must then name it.

2. **MINOR — NOTES.md names commits that are not on the branch, and its verification record
   is for a different tree.** `agent_work/chunks/cr02-fuzz-deadline-retry/NOTES.md`, the
   "What was built" table and "Results (on commit `664357a`, clean tree)".
   - `f3c4090`, `4f498d4`, `97ef501`, `984183b`, `664357a` exist as objects but none is an
     ancestor of the tip (`git merge-base --is-ancestor <sha> HEAD` → no for all five). The
     branch's commits are `8b91644`, `86116a1`, `a5082a8`, `22a80ee`, `23b18eb`, `b4ede4c`
     (the branch was rebased from base `65567de` to `779203c`).
   - The CR-2 files are byte-identical between `664357a` and the tip
     (`git diff --stat 664357a HEAD -- scripts/… docs/knowledge/foundation-gates.md` is
     empty), so the record is not wrong about the code, but its numbers are stale: it says
     15 fuzz targets and comment baseline `65567de`; the tip has 16 targets and baseline
     `779203c`. NOTES.md also says the ratify commit is `97ef501`; on the branch it is `a5082a8`.
   - Fix: the table and the results must name commits on the branch and numbers from this tree.

3. **MINOR — `fuzz_retries` in `gate.sh` is held only by a canary whose documented command
   does not complete.** `scripts/gate.sh:427`.
   - Mutation M11 (count column 5 instead of 6) leaves `go test ./scripts/` green; the only
     check that would notice is canary `g5-retry-reported` (`res_retries != tsv_retries`).
   - NOTES.md ("Deviations" 2) says `CANARY_ONLY=g5-retry-reported,g5-crasher-not-retried
     scripts/gate-canaries.sh` stops at its clean-copy precondition (a full build-stage gate
     in a `git init` copy, where two earlier attack tests need git history), and that the
     builder ran the two canaries through a scratch copy of the script with the precondition
     narrowed. I did not run the canary script (it needs a full gate run, which this review
     may not start), so I neither confirm nor refute that record. As it stands, the command
     the brief gives for deliverable 4 cannot produce a verdict on this tree.
   - I checked the same property another way (below, "stub gate"): `fuzz_retries`, the table,
     the RETRY lines and the NOTE agree for 1, 2 and 3 retried targets.
   - Fix (orchestrator's call, the precondition failure predates this change): either the
     canary script's precondition is repaired, or a fast test holds `fuzz_retries` against a
     report with retried rows.

4. **MINOR — a test writes into tracked source directories.**
   `scripts/fuzz_test.go`, `TestFuzzShDoesNotRetryWhenNewCorpusFileAppears`: the stub creates
   `internal/sim/world/testdata/fuzz/FuzzWorldSchedule/cr02-test-crasher` and
   `internal/sim/simtoy/testdata/fuzz/FuzzToySchedule/cr02-test-crasher` in the real tree and
   removes them in `t.Cleanup`.
   - While the file exists, a concurrent `go test` of `internal/sim/world` or
     `internal/sim/simtoy` (packages run in parallel under `go test ./...`) reads it as one
     more seed (`[]byte("boom")`; both targets take `[]byte`, so it runs and passes). A test
     process killed before cleanup leaves an untracked file: the tree is dirty for the next
     gate and the seed corpus has an entry nobody chose.
   - The `new_corpus_dir` subtest fails by design (`unexpected pre-existing`) on the day
     `internal/sim/simtoy/testdata/fuzz` is committed.
   - I saw no failure from this: `go test -count=1 ./...` passed and `git status` was clean after.
   - Fix: nothing required for landing; a later change could point the corpus test at a
     target directory that is not in the tracked tree.

5. **MINOR — the signature is text only, so output forged by the test binary matches it.**
   `scripts/fuzz.sh:117-181`.
   - A `TestMain` that prints `--- FAIL: FuzzX (1.00s)`, `    context deadline exceeded`,
     `FAIL` and exits 1 is accepted as the signature (my case `testmain`, real `go test`).
     This is deliberate forgery by code in the package under test, not a failure a target
     produces by failing, so I do not count it as a real failure retried; the file is, in the
     gate's own words, evidence for an honest operator.
   - The check does not compare the duration in the `--- FAIL` line with `FUZZ_SECONDS`. Go's
     own deadline error can only come at or after `-fuzztime`; requiring that would close the
     forgery above for free and cost nothing on the genuine case. Optional.

6. **MINOR — NOTES.md claim row 4 lists the gate test as proof that `fuzz_retries` agrees.**
   `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries` never runs G5; it
   proves `null` when G5 is skipped, nothing about agreement (see finding 3). The row should
   say so.

Checked and found in order (no finding):
- All five deliverables exist. Every test the brief lists exists: signature retried and
  passing; twice in a row fails; near misses (failing-input line, panic before and after,
  different message, message in a passing target's log, new corpus file in an existing and in
  a new directory, third target after two retries, `FUZZ_MAX_RETRIES=0`); rate from the run
  that stands; the stub's counter file (`STUB_COUNTS/<key>.n`).
- The `retried` column is last; the first five are unchanged (CR-1's tests pass with only the
  width edit).
- No comment was added to a Go file (G2 pass against `779203c`; `grep '^+.*//'` on the test
  diff is empty). No commit of the range by the builder carries a Co-Authored-By line, as the
  brief requires (the one that does is the orchestrator's filing commit `345932e`).
- `agent_work/adversarial/`: one file, four changed lines, in a commit of its own.
- `is_deadline_signature` gives the same answer under gawk and mawk on all 35 synthetic logs.

## Ratify or reject: the change to CR-1's attack test

**RATIFIED.** Commit `a5082a8` ("adversarial: update CR-1 TSV column expectation from 5 to 6
for CR-2"; NOTES.md calls it `97ef501`, see finding 2).

- The whole change under `agent_work/adversarial/` in the range is
  `cr01-shrink-fuzz-rules/adv_test.go`, `TestAdv_6_RealTSVColumns`: `n != 5` → `n != 6` and
  the message `want 5` → `want 6`. Two lines removed, two added; nothing else in the file or
  the directory differs (`git diff 779203c..b4ede4c --stat -- agent_work/adversarial`).
- It is the minimum: the test asserts only the width of every report row, the report now has
  six columns, and no smaller edit makes it pass.
- It weakens nothing else: the assertion stays an equality (not `>= 5`), still applies to
  every row, and the test's other checks (rows exist, the run's exit) are untouched. The rest
  of CR-1's attack suite passes unchanged
  (`go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/` → ok, 17.0 s).
- It is in a commit of its own with the `adversarial:` subject and is listed in NOTES.md under
  the required heading with old value, new value and reason.

## Signature attacks (brief item 7)

None of the real failures below was retried. "rejected" means `is_deadline_signature`
returned non-zero for the log; the function was extracted verbatim from `scripts/fuzz.sh`.

Real `go test -run '^$' -fuzz '^FuzzX$' -fuzztime 3s` (go1.26.8) on targets written for the purpose:

| Try | What the target does | go exit | New corpus file | Result |
| --- | --- | --- | --- | --- |
| mutfatal | `t.Fatal("context deadline exceeded")` on a mutated input | 1 | 1 | rejected (and blocked by the corpus check) |
| seedfatal | the same on the seed, so no file is written | 1 | 0 | rejected (`failure while testing seed corpus entry`, nested `--- FAIL`, `file:line:`) |
| seedfailnomsg | `t.Fail()` with no message on the seed | 1 | 0 | rejected |
| mutfailprint | prints `    context deadline exceeded` to stdout, then `t.Fail()` | 1 | 1 | rejected |
| seedpanic | `panic(context.DeadlineExceeded)` | 1 | 0 | rejected |
| mutexit | prints the text, then `os.Exit(1)` in the worker | 1 | 1 | rejected (`fuzzing process hung or terminated unexpectedly`) |
| setupfatal | `f.Fatal("context deadline exceeded")` before `f.Fuzz` | 1 | 0 | rejected (`x_test.go:6:` prefix) |
| setupfailprint | `f.Fail()` and the text printed from `f.Cleanup` | 1 | 0 | rejected (text comes before the header) |
| setupfailnl | `f.Error("\ncontext deadline exceeded")` after `f.Fuzz` — the closest real one: the message line is exactly `        context deadline exceeded` | 1 | 0 | rejected (the `x_test.go:8: ` line sits between header and message) |
| postfuzzfail | `f.Fail()` after `f.Fuzz`, no message | 1 | 0 | rejected |
| buildfail | does not compile | 1 | 0 | rejected |
| testmain | `TestMain` prints the three signature lines and exits 1 | 1 | 0 | **matches** — forgery, finding 5 |
| hang / spin / slow / seed hang | `time.Sleep(time.Hour)`, `for {}`, an 8 s sleep, `select {}` on the seed | 0 | 0 | not a failure to go at all: `PASS`, so the retry is never consulted. (Go 1.26.8 passes a target that hangs until `-fuzztime`; that blind spot is Go's and predates this change.) |

Whole `scripts/fuzz.sh` on a scratch module with real go (`FUZZ_SECONDS=3`): a real crasher
(`Failing input written to testdata/fuzz/FuzzCrash/582528ddfad69eb5`) → no RETRY, row
`retried=0`, run fails; a crasher already in `testdata/fuzz` from an earlier run (no new
file) → `failure while testing seed corpus entry`, no RETRY, run fails; a first run that
gives the signature through a `go` wrapper on PATH → one RETRY line, second run real, row
`retried=1`, NOTE `1 retried`.

Synthetic logs (35) fed to the function: CRLF on every line; CR on the message line only; a
real message overwritten on screen by `\r` and the signature text; two FAIL blocks (before
and after `FAIL`); the signature twice; `panic:` after `FAIL`; `Failing input written to`
after `FAIL`; a `-timeout` kill (`panic: test timed out`); `signal: killed`; `[build failed]`
and `[setup failed]` after the block; trailing space; non-breaking space indent; NUL byte;
ANSI escape; form feed; target that is a prefix of the failing one (`FuzzXY` for `FuzzX`);
subtest header; nested header; data-race warning; `fuzz: elapsed: …, minimizing`; junk after a
progress line; two `exit status 1`; two `FAIL pkg` lines; `ok pkg` after; no `FAIL` line;
empty log; `FAIL` only; target given as a regular expression (`Fuzz.`). All rejected. Three
matched, all genuine: the canonical output, the same without a final newline, and the
deadline during baseline coverage.

Truncation (a `tee` that dies): the `FAIL` line must follow the message line directly, and
`Failing input written to` comes between them in a real crash, so no prefix of a real
crasher's log matches.

## Mutation checks

Scratch copy of the tip under `/tmp/zgw-cr02n-review1/mut`; each mutation alone, then
`go test -count=1 ./scripts/`.

| # | Property broken | Caught by |
| --- | --- | --- |
| M1 | new corpus file no longer blocks the retry | `TestFuzzShDoesNotRetryWhenNewCorpusFileAppears` (both subtests) |
| M2 | no cap on retried targets | `TestFuzzShCapsRetriesAtTwoByDefault`, `TestFuzzShMaxRetriesZeroDisablesRetry` |
| M3 | message line matched anywhere on the line (`file:line: context deadline exceeded` accepted) | `…NearMisses/file_line_prefixed_deadline` |
| M4 | rate read from the first run, not the one that stands | 5 retry tests |
| M5 | a failed second run is not a failure | `TestFuzzShFailsWhenDeadlineSignatureRepeats`, `TestFuzzShFailsWhenSecondRunFailsWithOtherMessage` |
| M6 | any text allowed between the message and `FAIL` | `…/failing_input_written`, `…/deadline_plus_panic_after`, `…/extra_message_under_fail` |
| M7 | any text allowed before the header | `…/minimizing_before_deadline`, `…/deadline_plus_panic_before` |
| M8 | any text allowed after `FAIL` | `…/exit_status_2_after_fail` |
| M15 | subtest header accepted | `…/subtest_fail_block` |
| M12 | NOTE always says `0 retried` | 4 retry tests |
| M13 | no RETRY line printed | 3 retry tests |
| M10 | **landing cap removed from `gate.sh`** | **nothing — finding 1** |
| M11 | `gate.sh` counts `fuzz_retries` from the wrong column | no Go test; only canary `g5-retry-reported` would (finding 3) |

Not counted: M9 (my mutation also broke the genuine case, so it says nothing) and M14
(dropping `FUZZ_MAX_RETRIES=` from the `env` line of `g5_fuzz` is an equivalent mutant: the
variable is inherited anyway).

## Release It! checklist

The brief rules that PLAN.md section 10.3 does not apply to this change request (gate scripts,
no gateway code). One line each, for the record:

- Integration points — not applicable: no connection is read or written; the only external
  process is `go test`, bounded by `-fuzztime`.
- Blocked threads — not applicable: no pooled resource; the script's temp directory is removed by its EXIT trap.
- Slow responses — not applicable: no request path. The retry adds at most `FUZZ_MAX_RETRIES × FUZZ_SECONDS` to a run (40 s by default).
- Cascading failures — not applicable.
- Chain reactions — not applicable.
- Dogpile — not applicable.
- Self-denial attacks — not applicable: targets run one after another, retries included.
- Force multiplier — the one automatic loop here is the retry: once per target (no loop, a
  single second run), at most `FUZZ_MAX_RETRIES` targets per run (default 2), tested by
  `TestFuzzShCapsRetriesAtTwoByDefault` and `TestFuzzShFailsWhenDeadlineSignatureRepeats`;
  the ceiling for a landing run is in `gate.sh` but its test cannot fail (finding 1).
- Unbalanced capacities — not applicable.
- Scaling effects — not applicable.
- Users — not applicable.
- Unbounded result sets — not applicable: one log file per run of a target, in a temp directory.
- Compounds — retry on a path that can hide a failure is the pair that matters here; the
  signature attacks above are the answer.

## Observability

PLAN.md section 7.6 does not apply (no Go component, no `Snapshot()`). The equivalent for this
change, checked with an instant `go` stub on PATH driving the real `gate.sh` in the scratch
copy (`GATE_ONLY=G5`, no real fuzzing):

| Planted first-run signatures | `FUZZ_MAX_RETRIES` | gate exit | `fuzz_retries` | rows with `retried=1` | RETRY lines | NOTE |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | default | 0 | 1 | 1 | 1 | `1 retried` |
| 2 | default | 0 | 2 | 2 | 2 | `2 retried` |
| 3 | default | 1 | 2 | 2 | 2 | (G5 red; last line `fuzz: FAILED: internal/sim/fakepg FuzzMatch`) |
| 3 | 3 | 0 | 3 | 3 | 3 | `3 retried`; `fuzz_max_retries: 3` |
| 1 | 0 | 1 | 0 | 0 | 0 | (G5 red; `fuzz: FAILED: internal/pgwire FuzzLex`) |

The four places agree in every row. `fuzz_retries` is `null` when G5 does not run (the
builder's gate test). `counts_for_landing` was `false` in all of these because they are
partial runs with `GATE_OUT`; that is the same reason finding 1's test proves nothing.

Fuzz targets (PLAN.md section 6.6): the change adds or alters no fuzz target in the product
tree; the two canary targets take no seed from a hash of their input.

## What I ran

All with `PATH=/usr/local/go/bin:$PATH`; scratch under `/tmp/zgw-cr02n-review1/`; the
repository was not changed (`git status --short` empty after each run). Machine load was 10
to 25 on 4 cores throughout.

| Command | Result |
| --- | --- |
| `gofmt -l .`; `go build ./... && go vet ./...` | no output; pass |
| `go test -count=1 -v ./scripts/` | ok, 60.4 s; 17 tests and 17 subtests, none failing |
| `go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/` | ok, 17.0 s |
| `go test -count=1 ./...` | rc=0, 41 packages ok, 7 min 34 s |
| `GATE_ONLY=G0,G1,G2,G5 GATE_OUT=/tmp/zgw-cr02n-review1/gate scripts/gate.sh cr02-fuzz-deadline-retry` (once) | overall pass, 486 s; G5 448 s: `16 fuzz targets, 20s each: 14 fuzz, 2 seed-sweep (floor 100/s), 0 without a rate, 0 unclassified, 0 retried`; `fuzz_retries: 0`, `fuzz_max_retries: 2`, `counts_for_landing: false` (partial). No genuine deadline error occurred in this run, so the retry was not exercised against a live one |
| 13 informative mutations, `go test -count=1 ./scripts/` each | 11 caught, 2 not (M10, M11) |
| 12 real failing targets + 4 hanging ones, real `go test -fuzz`, 3 s and 5 s | table above |
| 35 synthetic logs through `is_deadline_signature`, gawk and mawk | table above; identical under both |
| `scripts/fuzz.sh` on a scratch module with real go | real crashers not retried; planted signature retried once |
| `gate.sh` `GATE_ONLY=G5` with an instant `go` stub, 7 configurations | table under Observability; `FUZZ_MAX_RETRIES=1000000000` and `" 2"` exit 2 |

Not run:
- `scripts/gate-canaries.sh` for the two new canaries: its precondition is a full build-stage
  gate, which this review may not start. I read both canaries; their logic matches what the
  brief asks (the retry canary compares the row, the table count, `fuzz_retries` and the NOTE;
  the crasher canary requires red, the crasher text, the failing-input line, no RETRY line and
  `retried=0`).
- The full gate.
- Extra seeds / `scripts/sweep.sh`: the change touches no simulator scenario.
