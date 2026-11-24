# mini-zgateway

Mini-ZGateway — a high-performance, fault-tolerant database/storage proxy in Go, inspired by Meta's ZGateway and Cloudflare's Hyperdrive. It collapses N×M client-to-database connections into a bounded multiplexed pool, coalesces hot-key stampedes, and enforces per-tenant adaptive admission control (CoDel + Adaptive LIFO, per-tenant fairness, retry budgets) in front of a single vanilla Redis or Postgres backend.

Status: Phase 0 (influences). Before any plan or architecture diagrams, the orchestrator is producing an expanded influences list for the owner to steer on.

Spec: [docs/project-source.md](docs/project-source.md) · Site (once the foundation chunk lands): https://nickstrad.github.io/mini-zgateway/
