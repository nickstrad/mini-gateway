# Simulator kernel: files copied from the object-store simulator (chunk 1, 2026-11-24)

Source repository: `/root/Software/object-store` (module `github.com/nickstrad/object-store`).
Source commit read for the copies: `25fb899317955eb7d87186f940702e55dc0e3cde`. The copied
source files are byte-identical at `379e622` (the commit PLAN.md section 5 evaluated) and at
`94d2b21` (that repository's HEAD later the same day); checked with
`git diff --stat 379e622 25fb899` and `git diff --stat 25fb899 94d2b21` over
`internal/sim/{stream,queue,clock,sim}.go`, `internal/sim/simtest/`, `internal/env/env.go`
and `cmd/objsim/`: no differences.

The Go files carry no comments (the builder rule), so this page is the provenance record.
"Comments removed" means the source's `//` lines were dropped and nothing else changed.

| File here | Source file | How it was copied |
| --- | --- | --- |
| `internal/sim/stream.go` | `internal/sim/stream.go` | As-is, comments removed. `NewStream(seed, label)` = PCG seeded with `(uint64(seed), fnv64a(label))`. |
| `internal/sim/queue.go` | `internal/sim/queue.go` | The indexed min-heap (`less`, `swap`, `up`, `down`, `push`, `pop`, `remove`, `peek`) as-is, comments removed. Dropped: `evKind`, `disposition` and their names. The `event` struct is new (time, sequence, heap index, callback or opaque `Handler` plus a code, background flag). `remove` now returns whether it removed. |
| `internal/sim/time.go` | `internal/sim/clock.go` (`later`, `scaled`) and `internal/env/env.go` (`Time`, `Add`, `Sub`, `String`) | `later` -> `Later`, `scaled` -> `Scaled` (exported; `Scaled` returns 0 for a negative factor, which the source left to its callers). `Time.Add` now saturates through `Later`; `Time.Sub` saturates. Not copied: per-node skew and drift, the `Clock` and `Timer` interfaces, the timer type. |
| `internal/sim/simtest/seeds.go` | `internal/sim/simtest/seeds.go` | As-is, comments removed: `-seed`, `DST_SEED`, `DST_SEEDS`, `DST_SEED_BASE`, `testdata/seeds.txt`, `Seed`, `PinnedSeeds`, `Seeds`, `Sweep`. |
| `internal/sim/simtest/determinism.go` | `internal/sim/simtest/determinism.go` | Adapted: import path; the panic stack section of `Report` removed (the kernel has no stack: `runtime/debug` is outside the determinism lint's import list); "event log" -> "trace"; artifact file suffix `.trace.txt`. Names kept: `CheckDeterministic`, `RequireDeterministic`, `Report`, `ReplayCommand`, `RunPattern`, `ErrNondeterministic`, `TailLines`. The methods it calls on `*sim.Sim` keep the source's names: `Digests`, `Verify`, `Err`, `Seed`, `Steps`, `Now`, `LogTail`, `LogLen`, `WriteLog`. |
| `cmd/mgsim/main.go` | `cmd/objsim/main.go` | Pattern only, rewritten: subcommands `run <scenario>` and `list` instead of `-scenario`/`-list`; prints both digests and the REPLAY line. |
| `internal/sim/sim.go` | `internal/sim/sim.go` (`Step`, `RunUntil`, `RunFor`, `RunUntilQuiet`, the step budget, `guard`, invariant checks) | Pattern only, rewritten as an opaque-event loop: no node registry, no event kinds, no per-node clocks. Kept names and conventions: `ErrBudgetExhausted`, `PanicError`, `DefaultMaxSteps`, sticky first error. |

Not copied (PLAN.md section 5.3): the node registry, incarnations, regions, per-node
clocks, the disk, partitions, the chaos driver, the datagram and whole-message network,
the fixed event log, `rapid`. The determinism lint itself was copied in chunk 0
(`tools/detlint/SOURCE.md`).
