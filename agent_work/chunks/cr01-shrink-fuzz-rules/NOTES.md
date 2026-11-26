# CR-1 — value lowering in the shrinker, and the fuzz rules in the tooling: builder notes

Built by Opus on 2026-11-26 on branch `work/cr01-shrink-fuzz-rules` (worktree
`/root/Software/mini-gateway-work/cr01`) from
`/root/briefs/zgateway-p2-cr01-shrink-fuzz-rules.md`. Operational note for later builders:
`docs/knowledge/shrunk-scenarios.md`; the G5 rules are added to
`docs/knowledge/foundation-gates.md`.

## What was added

| Deliverable | Where |
| --- | --- |
| Value lowering, combined minimiser, integer helper | `internal/sim/lower.go` (new file) |
| Text form of a fault schedule | `internal/sim/schedtext.go` (new file) |
| Shrink freeze table and sweep digest (captured before anything else was written) | `internal/sim/shrink_freeze_test.go` (commit 4510a76) |
| Property tests, canary, bounds | `internal/sim/lower_test.go`, `internal/sim/schedtext_test.go` |
| Toy workflow and the scenario file | `internal/sim/simtoy/shrunk_test.go`, `internal/sim/simtoy/testdata/shrunk/planted-bug-chunk.faults` |
| Fuzz rates and classes | `scripts/fuzz.sh`; its test `scripts/fuzz_test.go` (package `scripts_test`, test-only) |
| Gate G5 counts | `scripts/gate.sh` (`g5_fuzz`, `fuzz_targets`, `fuzz_seed_sweeps`, `fuzz_rate_floor`, `FUZZ_RATE_FLOOR`); `.gitignore` ignores `gate/*.tsv` |
| Gate canaries | `scripts/gate-canaries.sh`: `g5-unreadable-rate`, `g5-seed-sweep-reported` |

Nothing in `internal/sim/fault.go` or any other existing file of `internal/sim` changed
(`git diff freeze-sim HEAD -- internal/sim/*.go` lists only new files). No file in
`internal/pgwire` or `internal/work` was touched.

### New exported identifiers (package `sim`) and their contracts

- `type FaultFloor struct { At Time; Arg1, Arg2 int64 }` — the smallest legal value of each
  lowerable field of one fault kind.
- `LowerInts(vals, floors []int64, stillFails func([]int64) bool) []int64` — returns a new
  slice; `floors[i]` is the floor of `vals[i]` (a missing floor is 0, extra floors are
  ignored). A value at or below its floor is never touched. Repeats passes until a pass
  changes nothing. Guarantee (any deterministic predicate, monotone or not): for every `i`
  with `out[i] > floor`, `stillFails(out with out[i]-1)` is false; if
  `stillFails(vals)`, then `stillFails(out)`. Never mutates `vals` or `floors`; each
  predicate call gets a fresh slice (a predicate that writes into it changes nothing).
  Standard library only (`slices`).
- `LowerValues(sched []Fault, stillFails func([]Fault) bool, floor func(FaultKind) FaultFloor) []Fault`
  — the same over each fault's `At`, `Arg1`, `Arg2` (in schedule order, fields in that
  order). `floor == nil` means zero for every field. Length, order, `Kind` and `Target`
  never change; the result is not re-sorted by time.
- `Minimize(sched, stillFails, floor) []Fault` — rounds of `Shrink` then one lowering pass,
  until a round's lowering pass changes nothing. Guarantee (any deterministic predicate):
  no single fault can be deleted and no single `At`/`Arg1`/`Arg2` above its floor can be
  lowered by one with `stillFails` still true; if the input failed, the result fails.
  Proof: the last round's `Shrink` deleted nothing, so every single deletion of the final
  schedule was tested false; its lowering pass changed nothing, so for every value the
  probe at the floor and the bisection's last lower probe (`value-1`) were tested false on
  that same final schedule. `Shrink` is called with a wrapper that hands the predicate a
  copy, so a predicate that mutates its argument cannot change what `Shrink` keeps.
- `type KindName struct { Kind FaultKind; Name string }`,
  `FormatSchedule(sched []Fault, names []KindName) (string, error)`,
  `ParseSchedule(text string, names []KindName) ([]Fault, error)`,
  `var ErrScheduleText` — the text form; grammar below. Every error from both wraps
  `ErrScheduleText`; `ParseSchedule` returns `nil` with every error.

### The search per value, and the call bound

`lowerOne(v, f)`: if `v <= f`, no call. Else probe `f` (accept it if it fails); else bisect
`[lo=f, hi=v]` with `mid = lo + (hi-lo)/2` in unsigned arithmetic (no overflow for any
`int64` pair), `hi = mid` on fail, `lo = mid` otherwise, until `hi - lo == 1`; return `hi`.
"Halving, then stepping" collapses into this: the bisection's last probe is `hi-1`, the
one-step probe, so a separate stepping phase would only repeat a call. Calls per value per
pass: at most `1 + bits.Len64(v - f)` (at most 65).

Stated bound, with `C` = the sum of `1 + bits.Len64(v - f)` over every value above its
floor in the input, `P` = the sum of `v - f` over those values, `n` = faults:

| Function | Predicate calls |
| --- | --- |
| `LowerInts`, `LowerValues` | at most `(1 + P) * C`; at most `2C` when the predicate is monotone in each value (upward closed) |
| `Minimize` | at most `(1 + P) * (n(n+1) + C)` |

Why: values only fall, so every pass costs at most `C`; every pass or round except the last
lowers `P` by at least 1 (deletions never raise it). For an upward-closed predicate a value
set by bisection is the exact minimum given the others, and lowering the others later can
only raise that minimum, so the second pass changes nothing. The `(1 + P)` factor is a true
worst case only for adversarial non-monotone predicates; it is astronomically loose for
nanosecond times, and the code has no round cap (a cap would break the guarantee).
Termination on every input follows from the same potential argument.

### Text form grammar

```
file   = header LF { fault LF }        ; exactly <count> fault lines
header = "faults" SP count             ; count: 0 .. 1024 (MaxScheduleLen)
fault  = "at=" int "ns" SP "kind=" kind SP "target=" int SP "arg1=" int SP "arg2=" int
int    = canonical decimal int64: optional "-", no "+", no leading zeros, no "-0"
kind   = a name from the table | "fault-" id   ; id: canonical 0..65535, only for unnamed kinds
name   = 1..64 of [a-z0-9._-], first a letter or digit, not starting "fault-"; unique; one per kind
```

No blank lines, comments, tabs, `\r`, or text after the last `LF`. A truncated file fails
the header count. `ParseSchedule` refuses text with more than 1,025 lines before splitting.
Accepted text is canonical: `FormatSchedule(ParseSchedule(t)) == t` (fuzzed).

## Decisions the brief left open

1. **Floors are per kind** (`func(FaultKind) FaultFloor`), not per fault: `Kind` never
   changes, so floors stay fixed during the search, which the termination proof needs.
2. **Lowered values are not re-sorted by time**; positions are kept, so the text file shows
   the minimiser's output exactly. `InjectSchedule` schedules by `At` anyway.
3. **No check that the input fails.** Like `Shrink`, the functions do not spend a call on
   it; the "still fails" guarantee is conditional on the input failing. The one-step
   guarantee holds regardless.
4. **Toy floors** (`netFloor` in `shrunk_test.go`): `net.chunk` `Arg2 >= 1` (piece size),
   `net.clear` `Arg1 >= 1` (a real kind); everything else 0.
5. **The toy workflow** finds the same planted-bug schedule as `TestPlantedBugCaughtAndShrunk`
   (40 faults). Measured: `Shrink` keeps 1 fault with value sum 2,213,137; `Minimize` keeps
   1 fault with value sum 1,021,423 (`net.chunk` on connection 3:
   `at` 2,213,134 ns -> 1,021,422 ns, `arg1` 1 -> 0, `arg2` 2 -> 1). The file is regenerated with
   `go test ./internal/sim/simtoy -run PlantedBugMinimised -args -update-shrunk` (its own
   flag, so the golden-digest `-update` is not involved). A third test lowers the toy's
   workload sizes with `LowerInts` (clients, lines, max line: `[3 20 40] -> [3 1 1]`; 3
   clients stay because the fault targets connection 3).
6. **The rate** is `execs / elapsed seconds` from the last line of the run that starts
   `fuzz: elapsed:`, which must have the form `fuzz: elapsed: <d>, execs: N (M/sec)`; if that
   last progress line cannot be read the target is `unreadable`, never rated from an earlier
   line (repair round). Go's `M` is not used: `M` is the rate over the last log interval only and was
   `0/sec` on the final line of three targets in the baseline run (FuzzClientSession,
   FuzzWorldSchedule, FuzzToySchedule).
7. **The fuzz.sh test seam** is `FUZZ_GO`: the command run instead of `go` for the fuzzing
   step only. Target discovery and `go list -deps -test` classification still run for real
   on the real tree, so the test checks the real classification. The gate runs fuzz.sh
   under `env -u FUZZ_GO`. The test is a Go test (`scripts/fuzz_test.go`) so G3 and CI run
   it; about 4 s.
8. **Report columns**: `package target execs execs_per_sec class`, header row first;
   classes `fuzz`, `seed-sweep`, and for failures `unreadable` (no rate) and `unclassified`
   (`go list` failed). The table is in the log and in `gate/G5-fuzz.tsv`.
9. **Gate**: `FUZZ_RATE_FLOOR` is passed through and recorded (`fuzz_rate_floor`); a floor
   under 100 does not count for landing. G5 is red if fuzz.sh exits 0 without writing the
   table. Counts are `null` when G5 did not run.
10. **New fuzz target** `FuzzParseSchedule` (internal/sim): parse never panics; accepted
    text re-formats to itself. It is simulation-level by the dependency rule and fast.

## Claims and their tests

| # | Claim | Tests |
| --- | --- | --- |
| 1 | `Shrink` unchanged; no digest moved | `internal/sim/shrink_freeze_test.go`: `TestShrinkFrozenTable` (7 rows, literal outputs), `TestShrinkFrozenSweepDigest` (400 seeded inputs × 5 predicate families; the digest covers inputs, every candidate passed to the predicate in order, and outputs; captured in commit 4510a76 before lower.go existed). Golden digests: `internal/sim/simtoy` `TestGoldenDigests` and the world/wire golden tests pass untouched; `fault.go` has no diff since `freeze-sim`. |
| 2 | Minimiser result fails; 1-minimal by deletion and by one-step lowering, monotone and non-monotone | `internal/sim/lower_test.go`: `TestMinimizeAgainstBruteForce` (400 seeds × 6 predicates: always, never, two upward-closed, two non-monotone incl. a hash predicate; brute force tries every lower value for monotone predicates and the one-step value for all, and every single deletion); `TestLowerValuesAgainstBruteForce`; `TestLowerIntsAgainstBruteForce`; `TestMinimizeNilFloorLowersToZero`. Floors and bounds: `checkLowerInts` (LowerInts) and `checkLoweredFrom` (LowerValues: same kind and target, a field at or below its floor unchanged, every other field in `[floor, input]`; Minimize: the output is an order-preserving subsequence of the input with the same per-field rule) |
| 3 | Calls within the bound; termination incl. always/never | same sweeps check `calls <= bound` per case (bounds computed with saturating arithmetic, `TestCallBoundsSaturateInsteadOfWrapping`); `TestLowerIntsExtremesAndBounds` (MaxInt64/MinInt64 values and floors, always, never, threshold); `TestLowerIntsEmptyAndNil` |
| 4 | No mutation; deterministic across runs and processes | sweeps compare inputs and floors before/after, with predicates that overwrite their argument; each case is run twice; `TestMinimizeIsDeterministicAcrossProcesses` (pinned FNV digest of 120 seeds' outputs) |
| 5 | Text round-trips every schedule of at most `MaxScheduleLen` (1,024) faults (narrowed, see below) and rejects malformed files | `internal/sim/schedtext_test.go`: `TestScheduleTextRoundTrips` (500 schedules with int64 extremes, named and unnamed kinds, a 1,024-fault one), `TestScheduleTextReadsAsDocumented`, `TestScheduleTextRejectsMalformed` (33 cases incl. truncation, extra/blank lines, CRLF, non-canonical ints, unknown and number-for-named kinds), `TestScheduleTextRejectsBadNameTables`, `FuzzParseSchedule` |
| 6 | fuzz.sh rates, seed sweeps, fail closed; gate counts | `scripts/fuzz_test.go`: `TestFuzzShReportsEveryTargetWithItsRate`, `TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps` (incl. a slow non-sim target staying `fuzz`, `1m0s`/`1h0m2s` elapsed, a final `0/sec`), `TestFuzzShFailsClosedWithoutARate`, `TestFuzzShFailsWhenATargetFails` (all four fail against the old fuzz.sh); repair round: `TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne`, `TestFuzzShNamesEveryFailedTargetOnItsLastLine`, `TestFuzzShPrintsNoFailedLineWhenGreen` (all three fail against the fuzz.sh of 1212e5e). Gate: canaries `g5-unreadable-rate`, `g5-seed-sweep-reported`; the gate run below records the counts. |
| 7 | No simulation-level target seeds from a hash of its input | audit table below (code reading; no test can prove a negative over future targets) |

Planted-bug canary of this unit: `plantAcceptUnchecked` (unexported package variable in
`lower.go`, set only by tests) makes `lowerOne` accept the floor without calling the
predicate. `TestPlantedAcceptUncheckedFailsTheBruteForceProperty` asserts that both the
`LowerInts` and the `Minimize` brute-force sweeps report failures with it set. Also checked
by hand (not committed): stopping the bisection one step early, and returning after a
single `Minimize` round, each make the brute-force tests fail.

## Audit of the simulation-level fuzz targets (rule 1) and their rates

Rates: from the gate run below (`gate/G5-fuzz.tsv`, 20 s per target, vm-1, the other
builder's work running at the same time). Simulation-level = the test package depends on
`internal/sim` (`go list -deps -test`).

| Target | Package | Sim-level | How the simulator seed is chosen | Execs in 20 s | Execs/s | Class |
| --- | --- | --- | --- | --- | --- | --- |
| `FuzzScheduleFromBytes` | internal/sim | yes (itself) | no simulator run (decoder only) | 623,668 | 31,183 | fuzz |
| `FuzzParseSchedule` (new) | internal/sim | yes (itself) | no simulator run (text parser) | 573,928 | 27,329 | fuzz |
| `FuzzToySchedule` | internal/sim/simtoy | yes | fixed: `Config{Seed: 1}` in `runDecoded` | 18,345 | 873 | fuzz |
| `FuzzWorldSchedule` | internal/sim/world | yes | fixed: `runSchedule(1, ...)` | 2,704 | 128 | fuzz |
| `FuzzServerInput` | internal/sim/fakepg | yes | fixed: `sim.Config{Seed: 1}` in `newWorld` | 275,709 | 13,785 | fuzz |
| `FuzzFramer` | internal/sim/fakepg | yes | no simulator run (framer only) | 570,357 | 28,517 | fuzz |
| `FuzzMatch` | internal/sim/fakepg | yes | no simulator run (matcher only) | 464,855 | 23,242 | fuzz |
| `FuzzFramer` | internal/pgwire | yes (test deps) | no simulator run | 256,732 | 12,836 | fuzz |
| `FuzzClientSession` | internal/pgwire | yes (test deps) | fixed: `rand.NewPCG(1, 99)` via `testClientConfig(..., 1)` | 60,733 | 2,892 | fuzz |
| `FuzzBackendSession` | internal/pgwire | yes (test deps) | fixed: `rand.NewPCG(1, 5)` via `testBackendConfig(..., 1)` | 190,187 | 9,509 | fuzz |
| `FuzzCycle` | internal/pgwire | yes (test deps) | no random source | 205,013 | 10,250 | fuzz |
| `FuzzSession` | internal/testclient | no | no random source | 555,076 | 27,753 | fuzz |

`FuzzWorldSchedule` was close to the floor (128/s here, 231/s in an earlier run on a quieter
machine) partly because one cached input cost 12 s per execution; the repair round bounds
one execution (see "The FuzzWorldSchedule flake") and measured 304/s after against 212/s
before on the same loaded machine. Under heavy load it may still be classed a seed sweep,
which the gate reports and does not fail. The pgwire targets were only read and measured (that package belongs to the chunk 5
rework).

No target needed a fix; no corpus file changed.

## Deviations

- **`g5-built-with-marker` canary is stale (pre-existing).** Chunk 1 deleted the `fuzz`
  marker, so that canary (expects G5 red with "delete the marker") can no longer be
  caught. Left as is; noted in `docs/knowledge/foundation-gates.md`. The two new canaries
  were run with
  `CANARY_KEEP=1 CANARY_ONLY=g5-unreadable-rate,g5-seed-sweep-reported scripts/gate-canaries.sh`:
  clean copy green and counting, **2 of 2 caught** (1,023 s). An earlier identical run
  stopped before the canaries because the clean copy's G5 was red; that was the slow
  `FuzzWorldSchedule` input explained under "The FuzzWorldSchedule flake" below, not an open
  flake.
- **"Halving, then stepping"** is one bisection whose last probe is the step (above).
- The call bound for non-monotone predicates is exact but very loose (factor `1 + P`).

## The FuzzWorldSchedule flake (explained and fixed in the repair round)

The builder's "open flake" was a slow input, not a nondeterminism. The adversary saved it:
`agent_work/adversarial/cr01-shrink-fuzz-rules/reproduced-FuzzWorldSchedule/fuzz/FuzzWorldSchedule/7edb6a746e048b6e`.
It decodes to `pg.bloat` (rows 0 -> 1, row size Arg2 = 65,535 bytes; the 16-bit decoded
value is under `MaxArg2`, which is sized for durations) and `pg.fragment` with chunk size
0 -> 1 byte. Every bloated reply then crosses the simulated network one byte per event.
Reproduced here: one execution PASSES but takes 13.96 s, 4,069,926 simulator steps,
108 MB peak (a clean run is 1,196 steps, 6 ms). With 4 rows of 65,535 bytes and 1-byte
chunks from time 0 an execution reaches 16,265,204 steps (about a minute). Go reports a
worker inside such an execution when `-fuzztime` ends as "hung or terminated unexpectedly",
and the input sat in `$GOCACHE/fuzz`, so every later run's baseline pass paid 11-14 s for it.

Fix (test code only, `internal/sim/world/property_test.go`, no simulator API change):
the target decodes with `fuzzSchedule`, which truncates to 128 bytes as before and caps
`pg.bloat` `Arg2` at `fuzzMaxBloatRowBytes` = 256. Cost is proportional to bloat bytes over
chunk size, and the chunk floor of 1 byte is the interesting case for the framers, so the
row size is the knob. `TestFuzzWorldScheduleCostIsBounded` runs the saved input, bloat with
1-byte chunks at time 0, and 16 alternating bloat/fragment records, and asserts each
execution stays within `fuzzStepBudget` = 100,000 simulator steps (deterministic; it fails
before the fix with 4.07 M and 16.27 M steps). Measured after: the worst constructed case
is 75,516 steps (about 0.2 s); a 600-input random search biased toward bloat and 1-2 byte
chunks peaked at 69,944 steps, 183 ms; all 530 cached corpus inputs replay in at most
0.12 s; the saved input takes 0.06 s. The saved input is committed as a seed:
`internal/sim/world/testdata/fuzz/FuzzWorldSchedule/7edb6a746e048b6e`.

Rate, 20 s, same machine (load average about 5.3), same cache: before 4,675 execs in 22 s
= 212/s; after 6,390 execs in 21 s = 304/s. The rate gain is modest because the slow input
mostly hurt the baseline pass and the tail; the fix is the worst case (13-60 s -> under
0.3 s), which is what made G5 fail at random.

`FuzzToySchedule` checked the same way: decoded args are at most 3 (`shrinkSpace`), at most
64 faults. All 212 cached inputs ran in under 5 ms each; 3,000 adversarial inputs up to
1 KB (half with every fault at the start of the window) peaked at 1,551 steps, 11 ms. No
change. Rate: 18,074 execs in 21 s = 860/s (873/s in the build run).

## Narrowed claims

- **Claim 5** (attack `TestAdv_5_UnboundedScheduleClaim`): the text form round-trips every
  schedule of at most `MaxScheduleLen` (1,024) faults. `FormatSchedule` refuses a longer
  schedule with an error wrapping `ErrScheduleText`, and `ParseSchedule` refuses a header
  count above 1,024; the cap is the house rule on bounded input. The code is unchanged; the
  adversarial test asserts the unbounded wording and stays red by design.

## Repair round

Review 1 (`REVIEW-1.md`) and the adversarial results (`agent_work/adversarial/cr01-shrink-fuzz-rules/RESULTS.md`):

| Item | Outcome |
| --- | --- |
| Review 1 (MAJOR) floors not asserted for LowerValues/Minimize | fixed, 6a26854: `checkLoweredFrom` in `internal/sim/lower_test.go`, used by `TestLowerValuesAgainstBruteForce` and `TestMinimizeAgainstBruteForce`. Reproduced first: both review mutations passed. After: the LowerValues mutation fails `TestLowerValuesAgainstBruteForce` (673 cases), the Minimize mutation fails `TestMinimizeAgainstBruteForce` (362 cases), and both together fail both (and `TestMinimizeIsDeterministicAcrossProcesses`). |
| Review 2 (MINOR) canaries do not clear `FUZZ_RATE_FLOOR`, `FUZZ_REPORT`, `FUZZ_GO` | fixed, 206c548 (added to the `unset` line of `scripts/gate-canaries.sh`). |
| Review 3 (MINOR) `g5-seed-sweep-reported` at 5 s can turn unreadable | fixed, 206c548: the control runs with `FUZZ_SECONDS=20`, the gate's own budget; and the cause of the slow baseline (the 12-second cached input) is removed by 5fbf999. |
| Review 4a (MINOR) NOTE counted `unclassified` as "without a rate" | fixed, b8e9aca: the NOTE says `N without a rate, M unclassified`; `TestFuzzShNamesEveryFailedTargetOnItsLastLine`, `TestFuzzShPrintsNoFailedLineWhenGreen`. |
| Review 4b (MINOR) gate's 15-line tail cut off the failing target | fixed, b8e9aca: a failed run ends with `fuzz: FAILED: <dir> <target>, ...`, so the tail always names it; `TestFuzzShNamesEveryFailedTargetOnItsLastLine`. |
| Review 5 (MINOR) `intCallBound` wraps for full-width ranges | fixed, 6a26854: saturating `satAdd`/`satMul` in `intCallBound` and `faultCallBound`; `TestCallBoundsSaturateInsteadOfWrapping` (fails before: bound 0). |
| Attack `TestAdv_5_UnboundedScheduleClaim` (BROKEN) | narrowed (claim 5, above); code unchanged, test stays red. |
| Attack `TestAdv_6_UnreadableAfterProgress` (BROKEN) | fixed, b8e9aca: `read_rate` takes the last `fuzz: elapsed:` line and requires the rate form; `TestFuzzShTakesNoRateFromALineBeforeAnUnreadableLastOne` (fails before: exit 0, rate 300). The adversarial test passes now. |
| Open flake (RESULTS.md) | explained and fixed, 5fbf999: see "The FuzzWorldSchedule flake"; `TestFuzzWorldScheduleCostIsBounded`, seed `7edb6a746e048b6e`. |

The full suite including `./agent_work/adversarial/...` therefore has exactly one expected
red test, `TestAdv_5_UnboundedScheduleClaim`, until the orchestrator rules on the narrowed
claim.

## Stated limits

- Predicates must be deterministic functions of their argument.
- A slow simulation-level target whose cached corpus (`$GOCACHE/fuzz/...`) grows large can
  fail to finish Go's baseline-coverage pass inside `FUZZ_SECONDS`; it then prints no rate
  and G5 is red (fail closed, by the brief). Before the repair round `FuzzWorldSchedule` did
  this at 1 s and nearly at 5 s, mostly because its cache held the 12-second input below;
  after the fix its 530 cached inputs replay in at most 0.12 s each.
- `FuzzWorldSchedule` caps the decoded `pg.bloat` row size at 256 bytes
  (`fuzzMaxBloatRowBytes`), so it never fuzzes bloat rows larger than that; the world
  scenario tests still use any size. `FuzzToySchedule` needs no cap.
- `ParseSchedule` bounds the schedule (1,024 faults) but not line length; the input string
  is the caller's.

## Index rows

Rows to add to `agent_work/INDEX.md` (not edited here, per the brief):

| Path | What |
| --- | --- |
| `scripts/fuzz.sh` | Timed fuzzing of every target; prints and (with `FUZZ_REPORT`) writes the rate table; classes `fuzz` / `seed-sweep` by `FUZZ_RATE_FLOOR`; fails closed without a rate. Test: `scripts/fuzz_test.go`. |
| `docs/knowledge/shrunk-scenarios.md` | How to turn a failing fault schedule into a committed scenario file. |

## Commands

```
export PATH=/usr/local/go/bin:$PATH
go test -count=1 ./internal/sim/ ./internal/sim/simtoy/ ./scripts/
go test -count=1 ./internal/sim -run 'Lower|Minimize|Planted|Shrink|ScheduleText'
go test ./internal/sim/simtoy -run PlantedBugMinimised -args -update-shrunk   # regenerate the scenario file
go test -run '^$' -fuzz '^FuzzParseSchedule$' -fuzztime 30s ./internal/sim
FUZZ_SECONDS=20 FUZZ_REPORT=/tmp/rates.tsv scripts/fuzz.sh
scripts/gate.sh cr01-shrink-fuzz-rules
CANARY_ONLY=g5-unreadable-rate,g5-seed-sweep-reported scripts/gate-canaries.sh
```

## Gate

Repair round, `scripts/gate.sh cr01-shrink-fuzz-rules` at the build stage on the clean
commit after the repair (only this NOTES.md changed after it): exit 1, `overall: fail`,
`counts_for_landing: false`. Every check passed except two:

- **G3 unit: fail, by design of this round.** The only failing test is
  `agent_work/adversarial/cr01-shrink-fuzz-rules TestAdv_5_UnboundedScheduleClaim`, the
  attack on the unbounded wording of claim 5 (see "Narrowed claims"). The builder may not
  edit `agent_work/adversarial/`, so G3 cannot be green until the orchestrator rules on the
  narrowed claim and settles that test. Every product package passes
  (`go test -count=1 ./...`: 28 packages ok, that one package FAIL).
- **G5 fuzz: fail, Go fuzz-engine deadline under load, not a target failure.**
  `FuzzScheduleFromBytes` (pure decoder, 29,862/s) ended `--- FAIL: ... context deadline
  exceeded` at the end of its 20 s with no failing input written. A G5-only rerun
  (`GATE_ONLY=G5`, load average 9.8) failed the same way on a different, untouched target,
  `internal/pgwire FuzzClientSession`; that one and `FuzzScheduleFromBytes` both PASS when
  run alone for 20 s right after. The new last line of fuzz.sh named the target each time
  (`fuzz: FAILED: internal/sim FuzzScheduleFromBytes`). This is the go tool's coordinator
  reporting its own `-fuzztime` context when workers are slow to stop on a heavily loaded
  host; it is not produced by any code in this chunk. fuzz.sh fails closed on it as on any
  nonzero exit (left as is: whether to tolerate that one message is a gate-policy decision
  for the orchestrator).

Other checks: G0, G1, G2, G3 race, G4 markers, G4 sweep (379 s, 200 seeds), G6 tier R
(41 s), G7 (87 s), G8, G9 pass; G10 skip-none. In the gate's G5 `FuzzWorldSchedule` was
95/s (seed-sweep) and 143/s in the rerun, `FuzzToySchedule` 1,823/s and 380/s: the host
was heavily loaded by other builders. No `result.json` committed.
