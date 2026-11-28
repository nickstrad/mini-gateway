# mini-gateway

`mini-gateway` is a Go proxy that sits in front of one ordinary Postgres server. It speaks the Postgres wire protocol (the message format clients use to talk to Postgres), so applications connect to it as if it were Postgres. It collapses many client connections into a bounded pool lent one transaction at a time (transaction pooling), terminates client TLS (encrypted connections), and lets identical in-flight read-only statements share one database round trip.

Under overload, it enforces per-tenant admission control. That control has five parts: bounded per-tenant queues, two selectable CoDel (Controlled Delay) modes for dropping stale queued work, newest-first service while overloaded (adaptive LIFO, last-in first-out), fair shares between tenants, and an adaptive limit on concurrent database work. The proxy is built testing-first: a deterministic simulator (a single-threaded test harness with a virtual clock and seeded randomness) lands before any proxy code.

**Status:** Plan approved by Nick on 2026-11-24, as amended by his rulings ([PLAN.md](PLAN.md#18-nicks-rulings-the-decision-record)). Build in progress. Landed chunks:

- Chunk 0: foundation.
- Chunk 1: simulator kernel.
- Chunk 2: simulated world — fake Postgres 17, simulated clients, direct baselines, and tier R fidelity.
- Chunk 3: observability spine — collector, registry, adapters, CSV flush, and DuckDB SQL assertions.
- Chunk 4: wire sessions — framer, TLS/SCRAM state machines, cycle trackers, and component `wire` export.
- Chunk 5: work vocabulary, error contract, courtesy reject list, safe-shape recogniser, and component `classifier` export.
- Chunk 6: walking skeleton — sans-I/O `proxy.Engine` with transaction-lease lifecycle and unconditional `DISCARD ALL` reset, goroutine shell with TLS on both legs and isolated admin HTTP listener, fail-closed JSON config, `cmd/mini-gateway` binary, component `engine` export, generated timeout-budget and capacity tables, `FuzzEngineOps`, `make chaos`, and tier S + tier R e2e suites.
- Chunk 6a: operator tools — open-loop load generator (`cmd/loadgen`), read-only operator client (`cmd/mgctl`), one-command local stack (`deploy/dev/` with `make run-up`, `make load`, `make run-down`), DuckDB story report (`scripts/report.sh`, `make report`), concise `sweep.sh` and `make chaos` output, and shipped regression suites for Chunks 1–4.
- Chunk 7: connection pool and circuit breaker — sans-I/O pool core (`internal/pool`) with hard `open + connecting` cap, concurrent-connect cap and one-slot GCRA creation-rate pacing, jittered idle timeouts and max lifetimes, off-path empty-query checks and `suspect` propagation, single database-down circuit breaker, dirty-connection clean-up (`cancel`, drain, `ROLLBACK`, reset), `always`/`on-switch` release policies with optional `setseed` re-seeding, `timeouts.idle_in_transaction` (`25P03`), bounded cancel forwarding on a second connection, bidirectional TCP keepalives, fleet budget division, component `pool` observability and `breaker` health rule (`internal/obs/poolsrc`), `FuzzPoolOps`, and tier S/R pool suites.
- Chunk 8: admission queues — per-tenant bounded rings (`internal/admission`), selectable `timeout` and `control-law` shedding rules behind `QueueRule`, adaptive LIFO with head sweep and hold hysteresis, simulator-only `fifo` and `folly` comparison arms, `ShedQueue` engine wiring (`internal/proxy/admission.go`), fail-closed `queue.*` config, component `admission` export and shedding health rule (`internal/obs/admsrc`), `FuzzAdmissionOps`, tier S/R overload suites, and the reproducible DuckDB head-to-head table (`scripts/admission-head-to-head.sh`).
- Chunk 9: fairness — weighted per-tenant fair dispatcher (`admission.Fair`) with two-group round robin, lease-lifetime in-flight accounting including held transactions, last-slot reservation and releasable pool demand (`internal/proxy/fairness.go`), fail-closed `fairness.*` weights config (`internal/config/fairness.go`), component `admission` fairness metrics (`internal/obs/admsrc/fair.go`), `FuzzFairnessOps`, and tier S/R noisy-neighbour, many-tenant, and held-transaction suites with DuckDB SQL assertions.
- Chunk 12: retry policy — sans-I/O retry core (`internal/retry`) with `off` default and `pre-forward-once` integer tenths-of-a-token budget, engine wiring re-entering the tenant admission queue with request-deadline clamping and dispatch breaker re-check (`internal/proxy/retry.go`), completed `docs/error-contract.md` and generated `docs/fault-matrix.md` (`internal/proxy/matrix.go`), fail-closed `retry.*` config (`internal/config/retry.go`), component `retry` metrics (`internal/obs/retrysrc`), `FuzzRetryOps`, and tier S/R retry-storm, no-double-apply, and weighted-fairness suites with DuckDB SQL assertions.

[Chapter 1](book/chapters/01-simulator-kernel.md), [Chapter 2](book/chapters/02-simulated-world.md), [Chapter 3](book/chapters/03-observability-spine.md), [Chapter 4](book/chapters/04-wire-sessions.md), [Chapter 5](book/chapters/05-classifier.md), [Chapter 6](book/chapters/06-walking-skeleton.md), [Chapter 6a](book/chapters/06a-operator-tools.md), [Chapter 7](book/chapters/07-pool.md), [Chapter 8](book/chapters/08-admission-queues.md), [Chapter 9](book/chapters/09-fairness.md), and [Chapter 12](book/chapters/12-retry-policy.md) of the book are live. Adaptive limit (Chunk 10), coalescer (Chunk 11), and fleet drain (Chunk 13) remain pass-through stage seams.

## Where to read

- Project site: https://nickstrad.github.io/mini-gateway/
- Book: [book/index.md](book/index.md) ([Chapter 1](book/chapters/01-simulator-kernel.md), [Chapter 2](book/chapters/02-simulated-world.md), [Chapter 3](book/chapters/03-observability-spine.md), [Chapter 4](book/chapters/04-wire-sessions.md), [Chapter 5](book/chapters/05-classifier.md), [Chapter 6](book/chapters/06-walking-skeleton.md), [Chapter 6a: Watching it run: a load tool, a small client, and reports that say what a run proved](book/chapters/06a-operator-tools.md), [Chapter 7: The pool: a hard cap, a creation rate, and connections that go bad](book/chapters/07-pool.md), [Chapter 8: Two CoDels, measured against each other](book/chapters/08-admission-queues.md), [Chapter 9: Fairness that binds only when it must](book/chapters/09-fairness.md), and [Chapter 12: Retries, off by default](book/chapters/12-retry-policy.md) are live; later chapters publish as chunks land)
- Plan: [PLAN.md](PLAN.md)
- Generated contracts and registry: [docs/error-contract.md](docs/error-contract.md) · [docs/fault-matrix.md](docs/fault-matrix.md) · [docs/reject-list.md](docs/reject-list.md) · [docs/safe-shape.md](docs/safe-shape.md) · [docs/timeout-budget.md](docs/timeout-budget.md) · [docs/capacity-table.md](docs/capacity-table.md) · [docs/csv-schema.md](docs/csv-schema.md)
- Extension seams: [docs/extension-seams.md](docs/extension-seams.md)
- Architecture diagrams (C4): [docs/c4/README.md](docs/c4/README.md)
- Influences: [INFLUENCES.md](INFLUENCES.md) · Summary: [docs/influences-summary.md](docs/influences-summary.md)
- Spec: [docs/project-source.md](docs/project-source.md)

## Building and running the proxy binary

Needs Go 1.26. Three other tools are needed for specific tasks: Python 3 to build the site, Docker with `postgres:17` for tier R, and the DuckDB CLI `1.5.x` (on `PATH` or in `~/.local/bin`) for `scripts/csv-assert.sh` and the DuckDB Go tests.

- Build the binary:
  `go build -o /tmp/mini-gateway ./cmd/mini-gateway`
- Derive a SCRAM-SHA-256 verifier for the `tenants` table in your JSON config (pass `-password -` to read from stdin):
  `go run ./cmd/mini-gateway -verifier -password example-pw`
- Validate a config file and print its fingerprint. Start from [internal/config/testdata/example.json](internal/config/testdata/example.json). With `client_tls.mode` set to `"require"`, provide readable `cert_file` and `key_file`; for plaintext local testing, set `client_tls.mode` to `"off"`:
  `/tmp/mini-gateway -config <file> -check`
- Run the proxy until `SIGTERM` or `SIGINT` (the admin listener serves `/healthz`, `/stats`, and `/metrics` on `admin_listen`):
  `/tmp/mini-gateway -config <file>`

## Running the local stack and operator tools

Watch the gateway under constant open-loop load with Postgres 17 and Prometheus (all ports bound on `127.0.0.1`; settings in [deploy/dev/env.sh](deploy/dev/env.sh)):

- `make run-up` — builds `mini-gateway`, `mgctl`, and `loadgen`, starts Postgres 17 and Prometheus in Docker, starts the gateway on the host, waits until Prometheus reports the `/metrics` target as up, and prints the Prometheus UI, targets, and graph URLs.
- `make load RATE=100 DURATION=30s PROFILE=simple|errors SEED=<n>` — runs open-loop load via [cmd/loadgen](cmd/loadgen/main.go) against the dev gateway and flushes its snapshot to CSV under `/tmp/mg-dev/load/`. Exits `3` with a `SHORTFALL` line whenever achieved rate falls below target. Use `go run ./cmd/loadgen -plan 10 -seed 7 -profile errors` to inspect a plan offline.
- `make run-down` — stops the gateway and removes the stack's containers, network, and volumes while keeping flushed CSV runs under `/tmp/mg-dev/` for reporting.
- `make report RUNS=<dir...>` (or `make report SEEDS=3` to run tier S e2e scenarios and report over them) — runs [scripts/report.sh](scripts/report.sh) with DuckDB `1.5.x` to print claims, inputs, coverage tables, full assertion and oracle failures, and a one-line verdict.
- `go run ./cmd/mgctl -admin 127.0.0.1:56433 <subcommand>` — read-only CLI for a running gateway (`health`, `stats -watch`, `metrics`, `tenants [-config <file>]`, `config show [-config <file>]`), plus offline `config check <file>`, `verifier -password <pw|->`, and single-statement `query -user <u> -password <pw|-> "<sql>"` (with error-contract decoding). Set `MGCTL_TRANSCRIPT=<file>` to log commands and redacted output.

## Running tests and tools

- `make test` (same as `make ci`) — runs `scripts/ci-fast.sh`, the fast CI check set: formatting, build, vet, lints, and short tests, including replaying the fake against the recorded Postgres 17 transcript.
- `go test -count=1 ./...` — runs the full unit, property, simulator, e2e tier S, observability, and adversarial test suites without Docker. DuckDB-backed tests in `internal/obs/simrun` skip when `duckdb` is absent unless `MG_REQUIRE_DUCKDB=1` is set.
- `go test -race -count=1 ./internal/shell/... ./internal/config/...` — runs the goroutine shell (`testing/synctest` and loopback TCP/TLS) and config suites under the race detector.
- `CHAOS_SEEDS=3 CHAOS_TIER_R=0 scripts/chaos.sh` (or `make chaos` to run both tier S and tier R when Docker is present) — runs all 21 named tier S scenarios and their DuckDB SQL assertions, printing one summary line per scenario (`VERBOSE=1` prints raw output).
- `scripts/fuzz-plant.sh` — verifies `FuzzEngineOps` catches the planted queued-client vanish bug within the 20 s budget.
- `go run ./cmd/mgsim list` and `go run ./cmd/mgsim run <scenario> -seed N [-trace]` — list and run the 16 `mgsim` scenarios (11 toy echo scenarios plus 5 direct-to-database baselines).
- `scripts/gen-docs.sh --check` — regenerates [docs/error-contract.md](docs/error-contract.md), [docs/reject-list.md](docs/reject-list.md), [docs/safe-shape.md](docs/safe-shape.md), [docs/timeout-budget.md](docs/timeout-budget.md), [docs/capacity-table.md](docs/capacity-table.md), and [docs/csv-schema.md](docs/csv-schema.md) into a scratch directory and fails if any differs from the committed document.
- `scripts/tier-r.sh` — starts throwaway `postgres:17` Docker containers on loopback, runs all live Postgres 17 tests (including chunk 6's 14 tier R tests against the compiled binary with TLS on), and removes the containers on exit.
- `scripts/sweep.sh [-seeds N] [-run REGEXP]` — runs simulator scenario tests over `N` seeds (default 200) via `DST_SEEDS`.
- `scripts/fuzz.sh` — runs every fuzz target in the repository (16 targets) for `FUZZ_SECONDS` (default 20) each.
- `make site` — builds the static site and book into `_site/` using `scripts/site.sh`.
- `make bench` — deliberately fails with `not built yet` until its build step lands and deletes its marker under `scripts/.not-built/`.

The project was first named `mini-zgateway`; earlier documents keep that name.
