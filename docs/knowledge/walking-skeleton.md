# Walking skeleton: the engine, its seams, the shell and the e2e harness (chunk 6, 2026-11-26)

Facts a later builder (chunks 6a–13) needs before touching `internal/proxy`,
`internal/shell`, `internal/config` or `test/e2e`. Full record: `agent_work/chunks/c06-walking-skeleton/NOTES.md`.

## The engine (`internal/proxy`)

- `proxy.New(cfg)`, `e.Start(now, out)`, `e.Step(now, ev, out)`, `e.Shutdown(now, out)`,
  `e.Snapshot()`. `now` is `proxy.Instant` (int64 ns). Event data is borrowed for the
  duration of `Step`; action data belongs to the caller. The engine is in the determinism
  list; it never blocks, reads a clock or touches a socket.
- One wake-up timer: the engine keeps its own heap and emits `ActWake{At}` whenever the
  earliest deadline changes (`At == 0`: none). The driver posts `EvTimer` when it fires.
- Read protocol the driver must follow: a new client is read for exactly 8 bytes, then
  nothing until `ActClientResume` or `ActClientStartTLS`; a database connection opened with
  `TLSFirst` is read for exactly 1 byte, then the same. After `ActClientPause`/`ActDBPause`
  at most the read already in progress may be posted, and **a reader must not post a second
  chunk until the engine has taken the first** (the shell's per-connection credit). Without
  that a whole mailbox of data can pass a pause (found by the tier R slow-reader test).
- A FIN from a paused client is seen only when the proxy next reads (after its reply), as
  Postgres itself sees it; a reset (RST) is seen at once. Write a "vanish" test with a reset.
- Stage seams (`seams.go`), called in the PLAN.md 10.1 order: `Classifier`, draining input,
  `Breaker`, `RateGate`, `Coalescer`, `Queue`, `Pool`, `ReleasePolicy`, `RetryPolicy`.
  Replace one by setting it in `proxy.Config.Seams`; nil means the chunk 6 pass-through.
  `docs/extension-seams.md` has the table.
- Release: `ReleasePolicy.AtRelease` returns reset / lend as is / close. Chunk 7's
  `on-switch` and re-seed plug in there; `ReleaseInput.LastTenant` is already filled.
- Test-only switches are `proxy.Config.Plant` bits (`PlantReleaseOnAnyReady`,
  `PlantLendBeforeResetReply`, `PlantNoReset`, `PlantQueuedVanishKeepsSlot`,
  `PlantSkipCourtesy`). `internal/config` never sets them; a test in config holds that.
- Invariants (`e.Invariants()`, registered by `proxysim`): snapshot conservation, each lent
  connection has exactly one client, queue length = queued clients = waiting requests,
  open + connecting ≤ the cap, relay high-water ≤ its cap.
- `e.Quiesce()` treats an idle fixed set as quiet; the simulator's network and fake are
  quiet only after `Harness.Finish()` (run until quiet, check the engine, shut down, run
  until quiet again, check every quiesce hook).

## The shell (`internal/shell`) and the binary

- `shell.New(core, cfg)` takes a `Core` (`Start`, `Step`; `Shutdown` is used when present).
  `cfg.Sources` registers the collector sources (`enginesrc.Register`), `cfg.HealthRules`
  the health rules. The admin listener serves `/healthz`, `/stats`, `/metrics` from the
  last published snapshot only.
- CSV runs of the real binary are `<obs.csv.dir>/<obs.csv.scenario>/<instance>/<start ns>/`,
  so `scripts/csv-assert.sh` root mode pairs them with `test/analysis/<scenario>/`.
- `mini-gateway -config <file>`, `-check`, `-verifier -password <pw|->`. Exit 0 / 1 / 2.

## Observability

- Component `engine`: one source for the engine and the shell (`enginesrc.Source(engine,
  shell)`; shell metrics are zero in the simulator). The collector refuses two sources of
  one component. Every field of `proxy.Snapshot` and `proxy.ShellSnapshot` is exported
  (`TestMappingMovesEveryMetricAndExportsEveryField`).
- Health rule `database`: degraded when no connection is usable and the last dial failed.

## The e2e harness (`test/e2e`)

- `Spec` (scripted clients, steps, faults) runs in tier S with `RunSim` (scripted sim
  clients, `proxysim.World`, flushed by `simrun.Custom` every 5 s of virtual time) and in
  tier R with `RunReal` (real-socket clients with TLS and SCRAM against the binary).
  Chunk 2's workload-shaped `world.Driver` is implemented by `ProxyDriver` (tier S only).
- `TestScenarioE2E` runs every scenario in parallel; each seed runs twice (digests and
  flushed files byte-identical), round trip, scenario check. SQL under
  `test/analysis/e2e-*/` (tier S) and `test/analysis/tierr-*/` (tier R).
- The frozen fake has no reset-reply faults; `test/e2e/tamper.go` is a relay in front of it
  that rewrites the reply to one `DISCARD ALL` (error, wrong tag, status T, silent, drop,
  late).
- Tier R starts its own container `<MG_TIER_R_PREFIX>-c06` (Postgres 17, ssl=on with a test
  CA made at test time, scram-sha-256, pg_stat_statements) and builds the binary; about 30 s.
- `FuzzEngineOps` (fixed seed; 4 bytes per operation; at most 48 operations; 3 clients over
  1 connection so queueing is common) runs at roughly 130–250 executions per second on vm-1.
  Go's fuzz cache changes how fast a planted bug is found; `fuzz-plant.sh` uses an empty one. Its planted bug is checked by
  `scripts/fuzz-plant.sh`; the minimised input is `test/e2e/testdata/shrunk/queued-vanish.faults`.
