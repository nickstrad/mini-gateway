# Simulator kernel: what a later chunk builds on (chunk 1, 2026-11-24)

Short facts for the chunk 2 builder and later. The full record of the build, every claim
with its test, and the exact fault semantics are in
`agent_work/chunks/c01-sim-kernel/NOTES.md`. Provenance: `sim-kernel-provenance.md`.

## Packages

| Package | Determinism lint | What it is |
| --- | --- | --- |
| `internal/sim` | yes | `Time`, `NewStream`, the loop (`Sim`), `Timer`, trace and digests, fault schedule, `ScheduleFromBytes`, `Shrink` |
| `internal/sim/simnet` | yes | byte-stream connections on the loop, their faults, `Snapshot()` |
| `internal/sim/simtoy` | yes | the line-echo toy and its 11 named scenarios; used by meta-tests and `mgsim` only |
| `internal/sim/simtest` | no (test plumbing: flags, environment, files) | seeds, `Sweep`, `RequireDeterministic`, `Report`, `ReplayCommand` |
| `internal/obs` | yes | `Histogram` (integer, log-linear, 1/128 bound), `Conserve`, `Counters`, `Gauge`, `Snapshotter` |
| `internal/obs/obstest` | no (uses `reflect`) | `RequirePlain`, `ZeroCounters`, `MergeMoved`, `NeverMoved` for snapshot tests |
| `cmd/mgsim` | no | `mgsim run <scenario> -seed N [-trace]`, `mgsim list` |

`tools/lint-rules/det-packages.txt` lists the four lint-checked packages one by one, not
`internal/sim/...`, because `simtest` is not simulation code. **A new simulator package
(chunk 2: `internal/sim/fakepg`, `internal/sim/workload`) adds its own line there.**

## Conventions a world package follows

- Randomness: `s.Stream(label)` returns a fresh stream for `(seed, label)` each call; keep
  the returned stream. One label per independent thing (simnet uses
  `simnet/conn/<id>/up|down`) so a draw for one thing never shifts another.
- Events: `s.At`, `s.After` for closures; `s.AtHandler(t, h, code)` for an opaque handler
  value with a `uint64` code (no closure allocation). Events at one instant run in
  scheduling order. Nothing is ever called synchronously by the network: every
  notification is its own event.
- Time arithmetic saturates: `Time.Add`, `Time.Sub`, `sim.Later`, `sim.AddDurations` and
  `sim.Scaled`. Never add two `time.Duration` values from configuration with `+` (a wrapped
  negative delay is clamped to now and fires at once), and never pass `bound+1` to
  `Int64N` for a user-supplied bound (it panics at `math.MaxInt64`); simnet's `pipe.jitter`
  shows the pattern.
- Timers: `Timer` is a value; `Cancel()` removes it from the heap and reports whether it
  was pending. `MarkBackground()` exempts a timer from the quiesce check and from
  `RunUntilQuiet`.
- Register once per world: `RegisterState(name, fn)` (feeds the state digest, in
  registration order), `RegisterQuiesce(name, fn)`, `RegisterInvariant` or
  `RegisterSampledInvariant(name, every, fn)`. At most 256 of each.
- Trace: `s.Trace(sim.K("pkg.what"), a, b, c, d)`; kind names are at most 8 bytes. Put the
  connection or actor id in `a` so tests can filter one actor's traffic. Keep fault state
  and fault counters OUT of the state digest when a cleared fault must leave no residue.
- Faults: `RegisterFaultKind(kind, name, apply)`. simnet owns kinds 1 to 9 (`simnet.Fault*`).
  Chunk 2 picks a disjoint range (suggested: 32 and up for fakepg, 64 and up for the
  workload). A fault whose target does not exist is counted (`NoTarget`) and is not an error.
- Scenario tests are named `TestScenario...` and iterate seeds with `simtest.Sweep`:
  `scripts/sweep.sh` selects them with `-run '^TestScenario'` across `./...` and sets
  `DST_SEEDS`. Pinned seeds live in the package's `testdata/seeds.txt`.
- A failure prints a `REPLAY:` line: pass `simtest.ReplayCommand(t, seed)` as
  `sim.Config.Replay` for `go test` runs; the default is `go run ./cmd/mgsim run <scenario> -seed N`.
- Snapshots: plain structs of integers; check them with `obstest.RequirePlain`, prove
  "counters move" with `obstest.MergeMoved` and `NeverMoved`.

## Numbers

- Duration histogram: 1 µs to 60 s in nanoseconds, 3,302 buckets of `int64` (26.4 KB);
  `Quantile` returns a bucket's upper bound, never below the true value and less than
  value/128 above it.
- Gate cost at chunk 1: G4 sweep 5 s (200 seeds x 11 scenarios, each run twice), G5 fuzz
  47 s (2 targets x 20 s), whole gate about 70 s.
