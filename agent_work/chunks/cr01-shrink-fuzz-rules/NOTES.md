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
6. **The rate** is `execs / elapsed seconds` from Go's last `fuzz: elapsed: ..., execs: N
   (M/sec)` line, not Go's `M`: `M` is the rate over the last log interval only and was
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
| 2 | Minimiser result fails; 1-minimal by deletion and by one-step lowering, monotone and non-monotone | `internal/sim/lower_test.go`: `TestMinimizeAgainstBruteForce` (400 seeds × 6 predicates: always, never, two upward-closed, two non-monotone incl. a hash predicate; brute force tries every lower value for monotone predicates and the one-step value for all, and every single deletion); `TestLowerValuesAgainstBruteForce`; `TestLowerIntsAgainstBruteForce`; `TestMinimizeNilFloorLowersToZero` |
| 3 | Calls within the bound; termination incl. always/never | same sweeps check `calls <= bound` per case; `TestLowerIntsExtremesAndBounds` (MaxInt64/MinInt64 values and floors, always, never, threshold); `TestLowerIntsEmptyAndNil` |
| 4 | No mutation; deterministic across runs and processes | sweeps compare inputs and floors before/after, with predicates that overwrite their argument; each case is run twice; `TestMinimizeIsDeterministicAcrossProcesses` (pinned FNV digest of 120 seeds' outputs) |
| 5 | Text round-trips and rejects malformed files | `internal/sim/schedtext_test.go`: `TestScheduleTextRoundTrips` (500 schedules with int64 extremes, named and unnamed kinds, a 1,024-fault one), `TestScheduleTextReadsAsDocumented`, `TestScheduleTextRejectsMalformed` (33 cases incl. truncation, extra/blank lines, CRLF, non-canonical ints, unknown and number-for-named kinds), `TestScheduleTextRejectsBadNameTables`, `FuzzParseSchedule` |
| 6 | fuzz.sh rates, seed sweeps, fail closed; gate counts | `scripts/fuzz_test.go`: `TestFuzzShReportsEveryTargetWithItsRate`, `TestFuzzShClassesSlowSimulationTargetsAsSeedSweeps` (incl. a slow non-sim target staying `fuzz`, `1m0s`/`1h0m2s` elapsed, a final `0/sec`), `TestFuzzShFailsClosedWithoutARate`, `TestFuzzShFailsWhenATargetFails` (all four fail against the old fuzz.sh). Gate: canaries `g5-unreadable-rate`, `g5-seed-sweep-reported`; the gate run below records the counts. |
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

`FuzzWorldSchedule` is close to the floor (128/s here, 231/s in an earlier run on a quieter
machine); under load it may be classed a seed sweep, which the gate reports and does not
fail. The pgwire targets were only read and measured (that package belongs to the chunk 5
rework).

No target needed a fix; no corpus file changed.

## Deviations

- **`g5-built-with-marker` canary is stale (pre-existing).** Chunk 1 deleted the `fuzz`
  marker, so that canary (expects G5 red with "delete the marker") can no longer be
  caught. Left as is; noted in `docs/knowledge/foundation-gates.md`. The two new canaries
  were run with
  `CANARY_KEEP=1 CANARY_ONLY=g5-unreadable-rate,g5-seed-sweep-reported scripts/gate-canaries.sh`:
  clean copy green and counting, **2 of 2 caught** (1,023 s). An earlier identical run
  stopped before the canaries because the clean copy's G5 was red: one fuzz target failed
  (every target had a rate; `FuzzWorldSchedule` was a seed sweep at 67/s and
  `FuzzToySchedule` at 120/s, so the machine was heavily loaded). The harness deletes its
  copy, so the failing target and any crasher input were lost; the failure was not
  reproduced by a 20 s `fuzz.sh` run in the worktree nor by the second canary run. Treat it
  as an open flake for the reviewer: a fuzz target that fails only under load (a
  candidate is a wall-clock-free target hitting a step budget, but this is unconfirmed).
- **"Halving, then stepping"** is one bisection whose last probe is the step (above).
- The call bound for non-monotone predicates is exact but very loose (factor `1 + P`).

## Stated limits

- Predicates must be deterministic functions of their argument.
- A slow simulation-level target whose cached corpus (`$GOCACHE/fuzz/...`) grows large can
  fail to finish Go's baseline-coverage pass inside `FUZZ_SECONDS`; it then prints no rate
  and G5 is red (fail closed, by the brief). At 1 s, `FuzzWorldSchedule` does exactly this
  on vm-1 (438 cached inputs); at 20 s it does not.
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

`scripts/gate.sh cr01-shrink-fuzz-rules` at the build stage on the clean commit 3fb773d
(code complete; only this NOTES.md was committed after it): exit 0, `overall: pass`,
`counts_for_landing: true`, 981 s, `fuzz_targets: 12`, `fuzz_seed_sweeps: 0`,
`fuzz_rate_floor: 100`. Every check passed (G3 unit 176 s, G4 sweep 332 s, G5 256 s, G6
tier R 40 s, G7 131 s); G10 skip-none. The generated `result.json` was deleted, not committed.
