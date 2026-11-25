# Simulated world: fake Postgres, simulated clients, scenarios (chunk 2, 2026-11-25)

Facts a later builder needs before using `internal/sim/fakepg`, `internal/sim/workload`,
`internal/sim/world` and `internal/testclient`. The full behaviour tables are in
`agent_work/chunks/c02-sim-world/NOTES.md`.

## Standing up a world

- `sim.New` → `simnet.New(s)` → `fakepg.New(net, fakepg.Config{...})` (listens on
  `Config.Addr`, default `"pg"`) → `workload.New(net, workload.Config{Addr: srv.Addr(), ...})`
  → `wl.Start()` → `s.RunFor(d)` → `s.RunUntilQuiet()` → `s.CheckQuiesce()`.
- `world.SimDriver` does exactly that behind the `world.Driver` interface (Start, Addr,
  Connect, Inject, Run, Observe, Stop). A scenario is `world.Scenario{Name, Setup, Duration,
  Check}`; `world.Run(sc, seed, opts...)` runs it, `sc.Verify(res)` checks it.
  `world.Scenarios()` is the list `cmd/mgsim run <name> -seed N` searches after the toy list.
- For hand-driven tests use `workload.Probe` (dial, `Startup`, `Query`, `Send` raw bytes,
  `Cancel`, `Close`, `Reset`, `TraceFrom(i)` gives compact tokens such as `T D C:SELECT 1 ZI`).

## Fault kind numbers (per `Sim`, at most 64 kinds in total)

| Range | Owner |
| --- | --- |
| 1–9 | `simnet` |
| 32–55 | `fakepg` (`FaultRefuse` … `FaultClear`; `fakepg.FaultKinds()`) |
| 96–101 | `workload` (`FaultHalfMessage` … `FaultClear`; target = client id, 1-based) |

A fakepg fault target is the backend's connection id (the simnet connection id), or 0 for
"all backends / the server". Some fakepg faults apply simnet faults themselves (refuse,
connect stall, fragment, half-open), so they show in both snapshots and in the trace twice.

## Behaviour that is easy to get wrong

- The fake sends `RowDescription` when a SELECT-like statement **starts**, as Postgres 17
  does (found by the fidelity run: a terminated `pg_sleep` shows `T` before `FATAL 57P01`).
- A backend reads the next message only when its output queue is not blocked by the
  simnet window (back pressure); delayed output (first-byte, trickle, asymmetric faults)
  does not block reading, but at most `MaxDelayedSegments` (1,024) segments may wait.
- A client `Close` (FIN) is noticed by a busy backend only after its statement ends; a
  `Reset` is noticed at once and aborts the statement. A reset discards the bytes in
  flight, so a client that writes and resets at the same instant never reached the database.
- The workload client never pipelines: requests wait in the client's backlog, and latency is
  measured from the intended send time (`TenantSnapshot.Latency`); `LatencyFromSend` is the
  coordinated-omission control.
- Float code is not allowed anywhere under `internal/sim/` (an adversarial chunk 1 test scans
  it). The Poisson and Zipf generators live in `internal/randdist`, allow-listed in
  `tools/lint-rules/detlint-allow.txt`. The Zipf exponent is configured in thousandths.

## Tier R and the recorded transcript

- `scripts/tier-r.sh` starts two throwaway `postgres:17` containers (trust auth; `main` with
  `max_connections=600` and `pg_stat_statements`, `burst` with `max_connections=100`), runs
  `go test -run '^TestTierR' ./...` with `MG_TIER_R=1`, removes the containers on exit.
- `MG_TIER_R_UPDATE=1 scripts/tier-r.sh` rewrites
  `internal/sim/world/testdata/fidelity/postgres17.txt`, which CI replays against the fake
  (`TestFidelityAgainstRecordedPostgres17`).
- 500 real backends fit in the 7.7 GB VM with `shared_buffers=32MB`; 1,000 did not seem safe,
  so the tier R identical-read test uses 100 connections × 10 reads.
- Real Postgres forks backends one at a time: a burst of 500 connects takes over a second,
  so a burst test must hold accepted connections for seconds or early ones close before
  late ones arrive.
