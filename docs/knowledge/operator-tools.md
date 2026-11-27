# Operator tools: the load tool, mgctl, the dev stack and the story report (chunk 6a, 2026-11-27)

Facts a later builder needs before touching `cmd/loadgen`, `cmd/mgctl`, `deploy/dev/` or
`scripts/report.sh`. Full record: `agent_work/chunks/c06a-operator-tools/NOTES.md`.

## The load tool (`cmd/loadgen`, `internal/loadgen`, `internal/loadgen/live`)

- `internal/loadgen` is the determinism-linted core. `Plan{Seed, Rate, DurationNs, Tenants,
  Keys, Profile}`: `Count()`, `At(i)` (i × 1 s / rate, integer, overflow-safe up to 1e6/s for
  24 h), `Due(now)` (an op whose time equals `now` is due), `Op(i)` (a pure function of seed
  and index: a splitmix64 stream per index, so the rate and the replies never change an op),
  `Digest(n)`. Statements use the fake Postgres dialect (`kv (k text, v text)`, keys `k<n>`),
  so the same ops run on `fakepg`, the dev stack and tier R.
- `Tracker` is the accounting, one owner, no clock: `Tick(now)` makes due ops intended and
  gives each to a free connection of its tenant (connection c belongs to tenant c mod T) or
  counts it could-not-send at once (open loop: it never waits for capacity); `Sent`,
  `Completed` (FIFO per connection), `ConnDown`, `Stop`, `Finish` (queued → could not send).
  Conservation: intended = sent + could not send + queued; sent = completed + in flight.
  Late = sent more than `LateAfterNs` (default 25 ms) after the intended time.
- The runner (`live.Run`) owns the goroutines: one loop goroutine owns the tracker and the
  collector; per connection a writer (posts "sent" before it writes, so a reply can never
  overtake its send) and a reader (one `Reply` per `ReadyForQuery`). Proxy refusals are told
  from database errors by `mgctl.ProxyMade` (code and message of an error-contract row).
- A server that stops reading shows as late sends; one that answers slowly fills
  `-max-outstanding` (64) and shows as could-not-send; a stall shows in latency from intended
  send. SHORTFALL = late + could not send > 0; exit status 3.
- CSV runs: `<csv-dir>/load-<profile>/<seed>/<start ns>/`, component `loadgen`, tier `R`.
- Test fake: `internal/loadgen/loadgentest` (real sockets, trust auth, knobs: delay, stall,
  no read). The canary is `TrackerConfig.Plant = PlantSlowSchedule` (closed loop).

## mgctl (`cmd/mgctl`, `internal/mgctl`)

- `internal/mgctl` imports no `flag` and no `net/http` (a test holds it); the admin surface is
  an interface `Admin.Get(path)`, the data path a `Dialer` returning an `io.ReadWriteCloser`.
  `mgctl.Login`/`Session` is the shared SCRAM client (also used by the load tool).
- The admin surface has no per-tenant numbers in chunk 6a and no effective-settings
  endpoint: `tenants` lists tenant-labelled points (none yet) and the configured users with
  `-config`; `config show` prints the fingerprint and the capacity gauges from `/stats`, and
  with `-config` compares that file's fingerprint with the running one.
- `config check` is `config.Load`, the gateway's own path; its output equals
  `mini-gateway -check` after the program-name prefix (binary differential test).
- The model test in tier S drives the logic against `proxysim.World` with a pull-style
  `io.ReadWriter` over simnet (`Read` steps the simulator until bytes arrive).

## The dev stack (`deploy/dev/`, `make run-up`, `make load`, `make run-down`)

- Compose project `mg-dev` (override `MG_DEV_PROJECT`, must start with `mg-`); Postgres
  `postgres:17.11@sha256:2d2b…` on 127.0.0.1:55432; Prometheus `prom/prometheus:v3.9.1` on the
  host network, listening on 127.0.0.1:59090, scraping the gateway's admin port 56433 every
  second. Data directories are tmpfs, so `down -v` leaves no volume. Gateway on
  127.0.0.1:56432, tenants t0..t3, password `mg-dev-load`. State under `MG_DEV_DIR`
  (`/tmp/mg-dev`); `run-down` keeps the CSV runs there.
- The gateway runs on the host (nohup, pid file); Prometheus reaches it only because it is on
  the host network.
- On vm-1 under other builds' load, the stack served about 150 statements per second through
  8 pooled connections (each lease also pays the `DISCARD ALL` round trip); a 200/s errors
  run showed SHORTFALL with about 230 late sends. That is the proxy's back pressure, not a
  load-tool limit.

## The story report (`scripts/report.sh`, `test/analysis/report/`)

- Its SQL is under `test/analysis/report/sections/` — depth 3 on purpose: `csv-assert.sh`
  root mode treats every `test/analysis/<dir>/*.sql` as a scenario's assertions.
- Loadgen checks are built into the report (`sections/check-loadgen-*.sql`, with
  `-- applies-to-scenarios: load-*`), not under `test/analysis/load-*/`, for the same reason
  (the gate's G7 would then require load runs in every root).
- A run's scenario is the `scenario` column of its run.csv. A SQL file whose `-- requires:`
  names more than one component is an "oracle" check (proxy against an independent record),
  otherwise an "assertion"; failures of both are printed in full (no row limit).
- All checks run in one DuckDB session (one process per file took ~50 s at load 17; batched
  ~7 s); on a DuckDB error each file is re-run alone so the error names its file.
- Recorded runs for the golden tests are `test/analysis/report/testdata/e2e-runs.tar.gz`
  (re-record command in the chunk notes); `MG_REPORT_UPDATE=1` rewrites the goldens.
  `REPORT_PLANT=ignore-failures` is the report's test-only canary.
- `sweep.sh`/`chaos.sh` have `SWEEP_GO`/`CHAOS_GO` seams for fast script tests with a stub `go`.

## The promotion pass

- Chunks 1–4's held attacks live beside their code as `*_promoted_test.go` (`TestAdv_*`, or
  `TestAdvC01_*`/`TestAdvC03_*` in `internal/obs`); the ones needing Docker or DuckDB are
  `TestTierRAdv*` so `scripts/tier-r.sh` runs them. The ledger is in the chunk notes.
