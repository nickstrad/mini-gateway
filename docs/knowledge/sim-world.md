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
- The workload client never pipelines: arrivals (intended sends) follow the schedule
  whatever the server does, requests wait in the client's backlog, the wire send of the next
  request happens at the instant the previous reply's `ReadyForQuery` arrives, and latency is
  measured from the intended send time (`TenantSnapshot.Latency`; a retry keeps it);
  `LatencyFromSend` is the coordinated-omission control. "Sent in a window" therefore lags
  "intended" under a slow server; count arrivals (`Intended`), not `Sent`.
- `Retry.OnCodes` applies to query `ERROR`s as well as startup and mid-query `FATAL`s; a
  retried transaction re-runs its original statements.
- Exact write checks: `world.CheckWriteLog(res)` (or `world.CheckWrites` on raw logs) needs
  the write, statement and transaction logs complete (no dropped entries). It requires a
  bijection between write log records and committed write statements that changed a row
  (`StatementRecord.Rows > 0`, transaction outcome from `Server.TransactionLog()`), and that
  replaying the write log over `Config.Rows` gives the final table. Use it in any scenario
  whose claim is about writes.
- `MaxRows` holds inside transactions: an insert that would pass it gets `53100`, and a
  commit that no longer fits (other sessions inserted meanwhile) is refused with `53100`
  and rolled back.
- Every clearable fakepg fault kind has a case in `TestEachDatabaseFault` that checks the
  fault's own effect is gone after the clear; `TestEveryClearPathIsChecked` disables each
  clear path in turn (`fakepg.PlantSkipClear`, test-only) and fails if a case still passes.
  A new fault kind needs such a case (or a place in `oneShotKinds`).
- `pgproto3` may appear only under the directories of the importlint `only` rule, in every Go
  file outside `tools/`, tests and nested modules included
  (`internal/testclient/imports_test.go`). The phase-1 probe is kept as `main.go.txt` for
  that reason.
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
