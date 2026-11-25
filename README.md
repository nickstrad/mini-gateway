# mini-gateway

`mini-gateway` is a Go proxy that sits in front of one ordinary Postgres server. It speaks the Postgres wire protocol (the message format clients use to talk to Postgres), so applications connect to it as if it were Postgres. It collapses many client connections into a bounded pool lent one transaction at a time (transaction pooling), terminates client TLS (encrypted connections), and lets identical in-flight read-only statements share one database round trip. Under overload, it enforces per-tenant admission control: bounded per-tenant queues, two selectable CoDel (Controlled Delay) modes for dropping stale queued work, newest-first service while overloaded (adaptive LIFO, last-in first-out), fair shares between tenants, and an adaptive limit on concurrent database work. The proxy is built testing-first: a deterministic simulator (a single-threaded test harness with a virtual clock and seeded randomness) lands before any proxy code.

**Status:** Plan approved by Nick on 2026-11-24, as amended by his rulings ([PLAN.md](PLAN.md#18-nicks-rulings-the-decision-record)). Build in progress. Chunk 0 (foundation: Go module, local landing gate, lints, CI, scripts, book scaffold), Chunk 1 (simulator kernel, simulated byte-stream network, integer histogram and snapshot conventions, toy echo harness, and `mgsim` CLI), and Chunk 2 (fake Postgres 17 over `simnet`, simulated multi-tenant clients, sans-I/O test client, five direct-to-database baseline scenarios, and tier R Postgres 17 fidelity runner) have landed; [Chapter 1](book/chapters/01-simulator-kernel.md) and [Chapter 2](book/chapters/02-simulated-world.md) of the book are live. No proxy code exists yet.

## Where to read

- Project site: https://nickstrad.github.io/mini-gateway/
- Book: [book/index.md](book/index.md) ([Chapter 1](book/chapters/01-simulator-kernel.md) and [Chapter 2: A fake Postgres and hostile clients](book/chapters/02-simulated-world.md) are live; later chapters publish as chunks land)
- Plan: [PLAN.md](PLAN.md)
- Architecture diagrams (C4): [docs/c4/README.md](docs/c4/README.md)
- Influences: [INFLUENCES.md](INFLUENCES.md) · Summary: [docs/influences-summary.md](docs/influences-summary.md)
- Spec: [docs/project-source.md](docs/project-source.md)

## Running what exists today

Needs Go 1.26 (Python 3 to build the site; Docker with `postgres:17` for tier R).

- `make test` (same as `make ci`) — runs `scripts/ci-fast.sh`, the fast CI check set (formatting, build, vet, lints, and short tests, including replaying the fake against the recorded Postgres 17 transcript).
- `go test ./...` — runs the full unit, property, toy scenario, simulated-world baseline, and adversarial test suites (no Docker needed).
- `go run ./cmd/mgsim list` and `go run ./cmd/mgsim run <scenario> -seed N [-trace]` — list and run all 16 named scenarios (11 toy echo scenarios plus 5 direct-to-database baselines: `direct-500-connections`, `direct-reconnect-burst`, `direct-1000-identical-reads`, `direct-open-loop-overload`, `direct-mixed-transactions`), printing digests, result tables, and a `REPLAY:` line.
- `scripts/tier-r.sh` — starts throwaway `postgres:17` Docker containers on loopback, runs live Postgres 17 fidelity and direct-baseline tests (`MG_TIER_R=1`), and removes the containers on exit (`MG_TIER_R_UPDATE=1` refreshes the recorded transcript).
- `scripts/sweep.sh [-seeds N] [-run REGEXP]` — runs simulator scenario tests over `N` seeds (default 200) via `DST_SEEDS`.
- `scripts/fuzz.sh` — runs every fuzz target in the repository (7 targets) for `FUZZ_SECONDS` (default 20) each.
- `make site` — builds the static site and book into `_site/` using `scripts/site.sh`.
- `make chaos` and `make bench` — deliberately fail with `not built yet` until their build steps land and delete their markers under `scripts/.not-built/`.

The project was first named `mini-zgateway`; earlier documents keep that name.
