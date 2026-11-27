HELD: 17  BROKEN: 4

Attacks against build `1a138cb943770bb8f72342d562d71291a1e7330c`, from the six claims in the rebuild brief. Counts are top-level named attacks; parameterized cases count once. Every attack uses seed `20261127` (fixed inputs except the seeded mutation campaign).

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_ControlBytesAndExtraBlocks` | held |
| 1 | `TestAdv_1_ExistingCorpusRewritten` | BROKEN |
| 1 | `TestAdv_1_SymlinkedCorpusChild` | BROKEN |
| 1 | `TestAdv_1_NewCorpusWithHostileFilename` | held |
| 2 | `TestAdv_2_BudgetAcrossSevenTargets` | held |
| 2 | `TestAdv_2_IndependentProcessesResetBudget` | held |
| 2 | `TestAdv_2_WidenedStandaloneBudget` | BROKEN |
| 3 | `TestAdv_3_SecondExitWithoutOutput` | held |
| 3 | `TestAdv_3_SecondSignalAfterGoodProgress` | held |
| 3 | `TestAdv_3_SecondExitAllStatuses` | held |
| 4 | `TestAdv_4_GateCountsWhenRetryClassificationFails` | held |
| 4 | `TestAdv_4_MixedFailedAndRecoveredTargets` | held |
| 4 | `TestAdv_4_SkippedGateDiscardsStaleCounts` | held |
| 5 | `TestAdv_5_RetryCrossesFloorBothDirections` | held |
| 5 | `TestAdv_5_HourDurationAndZeroSeconds` | held |
| 5 | `TestAdv_5_LastProgressCannotBorrowFirstAttempt` | held |
| 6 | `TestAdv_6_MaximumAndOverflowEnvironment` | held |
| 6 | `TestAdv_6_GateDropsInjectedFuzzCommand` | held |
| 6 | `TestAdv_6_LandingPredicateBudgetBoundary` | held |
| 1 | `TestAdv_1_SeededLogMutationCampaign` | held |
| 1 | `TestAdv_1_NewlineDirectoryReplacement` | BROKEN |

## Broken attacks and replay

Run from this worktree. Each command prints seed `20261127`; inputs are fixed and require no seed environment variable. These are ordinary shell-tooling fixtures with synthetic Go output, not demonstrations of a naturally occurring Go fuzz crash.

### Claim 1: existing corpus file rewritten

```
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/ -run '^TestAdv_1_ExistingCorpusRewritten$'
```

Expected: “any first run that wrote a failing input” fails without retry. The stub changes an existing `testdata/fuzz/FuzzA0/known` file to a failing-input payload, emits the otherwise accepted deadline signature, and exits 1. Observed: two invocations, one retry, exit 0 after the recovery output. Path-only snapshots cannot detect content replacement.

Judgment: **over-broad claim**. The notes explicitly exclude in-place modification, and ordinary Go content-hashed crasher writes do not normally replace a different input at the same path. The attack preserves the broader wording rather than silently narrowing its assertion.

### Claim 1: new corpus file behind an existing directory symlink

```
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/ -run '^TestAdv_1_SymlinkedCorpusChild$'
```

Expected: a new file at `p/testdata/fuzz/FuzzA0/link/new` prevents retry. Observed: two invocations, one retry, exit 0. `find` lists the existing symlink but does not traverse its destination, so both snapshots are identical.

Judgment: **product defect against the stated unrestricted new-file condition**, limited to symlinked corpus subdirectories. The destination is local to the fixture. Standard Go-generated crashers are direct children, so this does not show a normal Go crasher escaping detection. A narrower claim limited to ordinary, direct content-hashed corpus files would exclude this case.

### Claim 1: newline directory replacement hides a newly created file

```
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/ -run '^TestAdv_1_NewlineDirectoryReplacement$'
```

Expected: deleting the original corpus tree and creating a new directory `a` with new file `a/b` prevents retry. Initially the tree holds a directory whose name is `a` followed by a newline and `.`, containing `a/b`. Observed: two invocations, one retry, exit 0. Its newline-delimited `find` listing contains lines `./a` and `./a/b` before either new path exists. After replacement, `comm -13` sees only lines it has seen before; deletion of other lines does not block retry.

Judgment: **product defect in snapshot encoding under hostile valid filesystem names**. The paths are ordinary directories/files, with no symlink or external process race. NUL-safe path comparison would distinguish them. The input layout is unusual and not produced by Go's normal content-hash naming.

### Claim 2: standalone environment widens the two-target ceiling

```
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/ -run '^TestAdv_2_WidenedStandaloneBudget$'
```

Expected: “at most two targets per run.” Observed with `FUZZ_MAX_RETRIES=3`: three targets are retried; the fourth is refused and the run exits 1. The violation is the number of retries, not a falsely green final exit.

Judgment: **over-broad claim**. The deliverables and notes permit an enlarged standalone budget and restrict landing eligibility instead. Claim 6's environment checks held. Claim 2 needs the same scope qualification.

## Method and coverage limits

The harness copies the current `fuzz.sh` and `gate.sh` byte-for-byte into temporary fixtures beneath this attack directory. It supplies a deterministic PATH `go` stub for module/dependency enumeration, fuzz output, statuses, corpus mutations, and invocation counts. No target code or builder tests were copied. The source declarations are discovery fixtures; they are not compiled. Gate checks use G5/G10 subsets with the real gate, real fuzz script, shell utilities, and jq. No network, database, full gate, parked branch, or prior adversarial result is used.

Claim 4 is checked on mixed failures, unreadable second attempts, classification failure, and a subsequent skipped-G5 run. RETRY-line counts, all TSV retry bits, NOTE count, and gate JSON must agree. Claim 5 checks independent duration arithmetic, the 99/100/101 floor boundary, first-five-column values, and first-attempt rate isolation. Claim 6 includes malformed/overflow settings, removal of FUZZ_GO, and execution of the actual landing predicate with every other prerequisite true. Both allowed budgets and disallowed budgets are asserted, so that predicate test cannot pass merely because partial gates never count.

All six claims have at least three distinct named attacks. No entire claim was unattackable. Limitations: no full landing certification or real timed fuzzing was attempted; unchanged CR-1 canaries are not independently certified by this harness. Findings on nonstandard corpus layouts do not establish that stock Go's direct content-hashed crasher writes can be mistaken for a deadline. The parser campaign appends 250 independently seeded unexpected diagnostics with `ADV_LONG=1` (12 by default); all must prevent retry.

## Verification

```
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/...
ADV_LONG=1 PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/cr02-fuzz-deadline-retry/...
```

Both exit 1 intentionally because the four BROKEN attacks remain failing. The default run completed in 15.543 seconds while the long campaign was also running, below the three-minute budget. The long run completed in 54.897 seconds. Go vet passed. Full captured evidence is in `default-run.log` and `long-run.log`. Only files under this adversarial directory were edited; product fixes are left to the build/review chain.
