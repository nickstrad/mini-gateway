# mini-gateway — change request CR-1: value lowering in the shrinker, and the fuzz rules in the tooling — builder brief

Seat: Opus (product Go, tests and scripts; logic only). Unit directory name:
`cr01-shrink-fuzz-rules`. Worktree: `/root/Software/mini-gateway-work/cr01` — branch
`work/cr01-shrink-fuzz-rules`. Another builder is reworking chunk 5 in `internal/pgwire`
and `internal/work` at the same time: do not touch those packages, and do NOT edit
`agent_work/INDEX.md` (put the rows you would add in a section "Index rows" of your
NOTES.md).

Read first: `agent_work/orch/BUILDER-RULES.md` (binding; and never delete or edit an
existing comment line of landed code — you add no comments); the request
`agent_work/chunks/cr01-shrink-fuzz-rules/CHANGE-REQUEST.md`; `PLAN.md` sections 6.1, 6.6
and 19.1; `/root/briefs/zgateway-dst-fuzz-ruling.md`; `internal/sim/fault.go` and its
tests; `internal/sim/simtoy` (the planted-bug scenario and `FuzzToySchedule`);
`internal/sim/world/property_test.go` (`FuzzWorldSchedule`); `scripts/fuzz.sh`,
`scripts/gate.sh` (check G5 and the result file) and `docs/knowledge/foundation-gates.md`
(the gate canaries: a gate change needs its canary); `agent_work/chunks/c01-sim-kernel/NOTES.md`.

## The freeze you must keep

`internal/sim` is frozen at tag `freeze-sim`. This request ADDS identifiers only.
`sim.Shrink` keeps its signature and returns exactly what it returns today for every
input. No existing exported identifier changes. No trace or state digest of an existing
scenario moves: the golden-digest tests must pass untouched.

## Deliverables

### 1. Value lowering in `internal/sim`

- A pass that takes a failing schedule and a `stillFails` predicate and lowers each
  remaining fault's `At`, `Arg1` and `Arg2` toward the smallest values for which the
  schedule still fails. Lower toward zero by default; where a fault kind has a smaller
  legal bound than zero would give, the caller supplies the floor (design the smallest
  API that lets a caller state per-field floors; `Kind` and `Target` are never changed).
  Use a bounded search per value (halving, then stepping), and bound the total number of
  predicate calls by a stated function of the schedule length and value sizes; test the
  bound.
- A combined minimiser: delete faults (the existing `Shrink`), lower values, and repeat
  until a full round changes nothing. Result: no fault can be deleted and no single value
  lowered by one step without the failure going away (state the exact guarantee you can
  prove for non-monotone predicates and test it, as `Shrink`'s comment does).
- A general helper for lowering a slice of integers under a predicate, with per-element
  floors, for inputs that are not faults (workload sizes now; operation arguments in
  chunk 6). It must not import anything outside the standard library, never mutate the
  caller's slice, and be deterministic.
- Determinism lint clean; no floats; no map order.

### 2. Shrunk scenarios as readable files

- A text form of a fault schedule that a person can read and that parses back to the
  identical schedule (one fault per line: time, kind by name, target, arguments; state the
  grammar). Round-trip property test; a malformed file is an error, never a panic or a
  silently shorter schedule.
- Apply it once, to show the workflow end to end: the toy's planted bug
  (`internal/sim/simtoy`) is found, minimised with the new pass, written as a shrunk
  scenario file under `testdata/`, and a plain test replays that file and fails with the
  bug planted and passes without it. The test must also show that the value-lowered
  schedule is strictly "smaller" than what delete-only `Shrink` returns for the same
  failure (a smaller sum of times and arguments), so the new pass is shown to do work.
- A short operational note `docs/knowledge/shrunk-scenarios.md`: how to turn a failure
  into a scenario file (the exact calls), and the rule that a found bug is committed as a
  scenario file, not as a pinned seed alone.

### 3. The three rules in the fuzz tooling

- `scripts/fuzz.sh`: after each target, parse Go's own summary (`execs: N (M/sec)`) and
  print a table at the end: package, target, executions, executions per second, class.
  Class is `fuzz` or `seed-sweep`. A target is *simulation-level* when its test package
  depends on `internal/sim` (decide it with `go list -deps -test`, not with a hand-kept
  list); a simulation-level target below the floor (`FUZZ_RATE_FLOOR`, default 100 per
  second) is classed `seed-sweep`. A seed sweep is not a failure of `fuzz.sh`. Write the
  table as a TSV to `$FUZZ_REPORT` when that is set. A target whose rate cannot be parsed
  is a failure (fail closed).
- `scripts/gate.sh`, check G5: pass `FUZZ_REPORT` so the table lands beside the gate's
  logs, and record two numbers in `result.json` (`fuzz_targets`, `fuzz_seed_sweeps`). The
  gate does not fail on a seed sweep; it reports it. Add the gate canary this change needs
  (read how canaries are declared; at least: an unparsable rate fails G5).
- Audit the existing simulation-level targets (`FuzzScheduleFromBytes`, `FuzzToySchedule`,
  `FuzzWorldSchedule`, and any other the dependency rule catches) against rule 1 — the
  simulator seed must be fixed or come from a dedicated prefix of the input, never from a
  hash of the input. Fix any that break it (in test code only; keep existing corpus files
  valid or regenerate them and say so). Record in NOTES.md, per target: how its seed is
  chosen, its measured rate at the gate's 20 s budget on this machine, and its class.
- A test for `fuzz.sh`'s parsing and classification that does not need a 20 s run (feed
  it recorded output through a small seam, or a `FUZZ_SECONDS=1` run of one cheap target —
  decide and say).

## Tests (write first)

Property tests for the value pass and the integer helper against a brute-force reference
on small inputs (the reference tries every lower value); monotone and non-monotone
predicates; the caller's slices never mutated; the predicate-call bound; floors respected;
`Shrink` unchanged (a table of inputs with today's outputs, captured before you touch the
file). The planted-bug canary of this unit: a test-only switch that makes the value pass
accept a candidate without calling the predicate must make the brute-force property test
FAIL. Round trip and malformed-input tests for the text form. The toy workflow test. The
`fuzz.sh` test and the gate canary.

## Claims (an adversary will attack each)

1. `sim.Shrink` returns byte-for-byte what it returned at `freeze-sim` for any schedule and
   predicate, and no existing scenario digest changed.
2. The minimiser's result still fails, and no single fault can be deleted nor any single
   `At`/`Arg1`/`Arg2` lowered by one (within its floor) with the failure remaining — for
   monotone and non-monotone predicates.
3. The number of predicate calls is within the stated bound; the passes terminate on any
   input, including a predicate that always or never fails.
4. Inputs are never mutated; results are deterministic across runs and processes.
5. The text form round-trips every schedule exactly and rejects every malformed file.
6. `fuzz.sh` reports a rate for every target, classes a slow simulation-level target as a
   seed sweep, fails closed when it cannot read a rate, and the gate records the counts.
7. No simulation-level fuzz target derives its seed from a hash of its input.

## Size and finish

About 250 lines of Go and 60 of shell. Finish as the builder rules say: `gofmt -l .`
empty; build, vet, `go test -count=1 ./...` green; `scripts/gate.sh cr01-shrink-fuzz-rules`
green at the build stage on a clean committed tree (run it detached; it takes about 15
minutes; delete the generated `gate/result.json` if it shows as untracked). NOTES.md at
`agent_work/chunks/cr01-shrink-fuzz-rules/NOTES.md`: what you added (every new exported
identifier with its contract), each claim with its test, the per-target audit table,
deviations, commands. Clean tree, nothing pushed, a summary, and a final line containing
exactly AGENT-DONE.
