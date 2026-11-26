# Shrunk scenarios: from a failing fault schedule to a file (CR-1, 2026-11-26)

**Rule.** A bug found by a fuzz target, a sweep or a property test is committed as a
shrunk scenario file plus a plain test that replays it. A pinned seed alone is not enough:
the seed is opaque, and it stops reproducing the bug as soon as any stream consumer changes.

## The calls (package `internal/sim`)

1. A predicate that is true while the bug shows, and that depends on its argument only
   (fixed seed, fixed workload):
   `fails := func(s []sim.Fault) bool { return run(s).Mismatch }`.
2. Minimise: `min := sim.Minimize(sched, fails, floor)`. It deletes faults (`sim.Shrink`)
   and lowers each remaining fault's `At`, `Arg1` and `Arg2`, round after round, until a
   full round changes nothing. `floor` gives the smallest legal value per field for a kind
   (`func(sim.FaultKind) sim.FaultFloor`); `nil` means zero for every field. `Kind`,
   `Target` and the order of the faults never change.
3. Write the file: `text, err := sim.FormatSchedule(min, names)`, where `names` is a
   `[]sim.KindName` (for simnet: one `{k, simnet.FaultName(k)}` per `simnet.FaultKinds()`).
   Save it under the package's `testdata/shrunk/<bug>.faults`.
4. Replay it in a plain test: `sched, err := sim.ParseSchedule(string(text), names)`, run
   with the bug present (must fail) and with it fixed (must pass).

Workload sizes and other integers: `sim.LowerInts(vals, floors, fails)` lowers each value
toward its floor the same way (a missing floor is 0). Worked example of all of this:
`internal/sim/simtoy/shrunk_test.go` and
`internal/sim/simtoy/testdata/shrunk/planted-bug-chunk.faults` (regenerate with
`go test ./internal/sim/simtoy -run PlantedBugMinimised -args -update-shrunk`).

## The file format

```
faults 1
at=1021422ns kind=net.chunk target=3 arg1=0 arg2=1
```

- Line 1: `faults <n>`, the number of fault lines that follow (0 to 1,024).
- Then exactly `n` lines `at=<int>ns kind=<kind> target=<int> arg1=<int> arg2=<int>`:
  single spaces, fields in this order, integers in canonical decimal (`-` allowed, no
  `+`, no leading zeros, no `-0`), `at` in nanoseconds.
- `<kind>` is the kind's name from the table, or `fault-<id>` for a kind the table does
  not name (a named kind written by number is an error).
- Every line ends in `\n`; no blank lines, comments, tabs or `\r`. Anything else, a
  truncated file included, is an error wrapping `sim.ErrScheduleText`, never a shorter
  schedule.
- Names: 1 to 64 of `a-z 0-9 . _ -`, starting with a letter or digit, not starting with
  `fault-`, unique, one per kind.

## Limits

- The predicate must be deterministic. `Minimize`'s guarantee (no single fault deletable,
  no single value lowerable by one within its floor) holds for any such predicate, monotone
  or not; the result still fails if the input did.
- Predicate calls: a value `v` above its floor `f` costs at most `1 + bits.Len64(v - f)`
  calls per pass; with `C` the sum of that over all values, `P` the sum of all `v - f` and
  `n` faults, `LowerInts` and `LowerValues` make at most `(1 + P) * C` calls (`2C` when the
  predicate is monotone in each value) and `Minimize` at most `(1 + P) * (n(n+1) + C)`.
  The `P` factor is a worst case for adversarial non-monotone predicates; real ones settle
  in two or three rounds.
