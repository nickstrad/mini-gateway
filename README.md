# mini-gateway

`mini-gateway` is a Go proxy that sits in front of one ordinary Postgres server. It speaks the Postgres wire protocol (the message format clients use to talk to Postgres), so applications connect to it as if it were Postgres. It collapses many client connections into a bounded pool lent one transaction at a time (transaction pooling), terminates client TLS (encrypted connections), and lets identical in-flight read-only statements share one database round trip. Under overload, it enforces per-tenant admission control: bounded per-tenant queues, two selectable CoDel (Controlled Delay) modes for dropping stale queued work, newest-first service while overloaded (adaptive LIFO, last-in first-out), fair shares between tenants, and an adaptive limit on concurrent database work. The proxy is built testing-first: a deterministic simulator (a single-threaded test harness with a virtual clock and seeded randomness) lands before any proxy code.

**Status:** Plan approved by Nick on 2026-11-24, as amended by his rulings ([PLAN.md](PLAN.md#18-nicks-rulings-the-decision-record)). Build in progress. Only the foundation exists so far: the Go module, the local landing gate and lints, continuous integration, scripts, and the book scaffold. No simulator or proxy code has been built yet.

## Where to read

- Project site: https://nickstrad.github.io/mini-gateway/
- Book: [book/index.md](book/index.md) (no chapters are live yet; chapters are published as build steps land)
- Plan: [PLAN.md](PLAN.md)
- Architecture diagrams (C4): [docs/c4/README.md](docs/c4/README.md)
- Influences: [INFLUENCES.md](INFLUENCES.md) · Summary: [docs/influences-summary.md](docs/influences-summary.md)
- Spec: [docs/project-source.md](docs/project-source.md)

## Running what exists today

Needs Go 1.26 (and Python 3 to build the site).

- `make ci` — runs `scripts/ci-fast.sh`, the fast CI check set (formatting, build, vet, lints, and short tests; passes on the empty module).
- `make site` — builds the static site and book into `_site/` using `scripts/site.sh`.
- `make test`, `make chaos`, and `make bench` — deliberately fail with `not built yet` until their build step lands and deletes their marker under `scripts/.not-built/`.

The project was first named `mini-zgateway`; earlier documents keep that name.
