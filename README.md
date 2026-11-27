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

[Chapter 1](book/chapters/01-simulator-kernel.md), [Chapter 2](book/chapters/02-simulated-world.md), [Chapter 3](book/chapters/03-observability-spine.md), [Chapter 4](book/chapters/04-wire-sessions.md), [Chapter 5](book/chapters/05-classifier.md), and [Chapter 6](book/chapters/06-walking-skeleton.md) of the book are live. The dynamic pool and circuit breaker (Chunk 7), admission queues (Chunks 8–9), adaptive limit (Chunk 10), coalescer (Chunk 11), retries (Chunk 12), and fleet drain (Chunk 13) remain pass-through stage seams.

## Where to read

- Project site: https://nickstrad.github.io/mini-gateway/
- Book: [book/index.md](book/index.md) ([Chapter 1](book/chapters/01-simulator-kernel.md), [Chapter 2](book/chapters/02-simulated-world.md), [Chapter 3](book/chapters/03-observability-spine.md), [Chapter 4](book/chapters/04-wire-sessions.md), [Chapter 5](book/chapters/05-classifier.md), and [Chapter 6: The first working proxy, and lending connections by the transaction](book/chapters/06-walking-skeleton.md) are live; later chapters publish as chunks land)
- Plan: [PLAN.md](PLAN.md)
- Generated contracts and registry: [docs/error-contract.md](docs/error-contract.md) · [docs/reject-list.md](docs/reject-list.md) · [docs/safe-shape.md](docs/safe-shape.md) · [docs/timeout-budget.md](docs/timeout-budget.md) · [docs/capacity-table.md](docs/capacity-table.md) · [docs/csv-schema.md](docs/csv-schema.md)
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
