# mini-gateway

A Go proxy that speaks the Postgres wire protocol in front of one ordinary Postgres. It collapses many client connections into a bounded pool lent per transaction, shares identical in-flight reads, and enforces per-tenant admission control under overload (two selectable CoDel modes, adaptive LIFO, fair shares, an adaptive concurrency limit). It is designed testing-first: a deterministic simulator is built before any proxy code.

Status: **Phase 1 proposal, awaiting approval. Nothing is built yet.**

- Proposal site: https://nickstrad.github.io/mini-gateway/
- Plan: [PLAN.md](PLAN.md) · Diagrams: [docs/c4/](docs/c4/README.md) · Influences: [INFLUENCES.md](INFLUENCES.md) · Spec: [docs/project-source.md](docs/project-source.md)

The project was first named mini-zgateway; earlier documents keep that name.
