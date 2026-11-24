# mini-gateway

A Go proxy that speaks the Postgres wire protocol in front of one ordinary Postgres. It collapses many client connections into a bounded pool lent per transaction, terminates client TLS, shares identical in-flight reads, and enforces per-tenant admission control under overload (two selectable CoDel modes, adaptive LIFO, fair shares, an adaptive concurrency limit). It is designed testing-first: a deterministic simulator is built before any proxy code.

Status: **plan approved by Nick on 2026-11-24, as amended by his rulings (PLAN.md section 18). Nothing is built yet; building starts on his go.**

- Plan site: https://nickstrad.github.io/mini-gateway/
- Plan: [PLAN.md](PLAN.md) · Diagrams: [docs/c4/](docs/c4/README.md) · Influences: [INFLUENCES.md](INFLUENCES.md) · Spec: [docs/project-source.md](docs/project-source.md)

The project was first named mini-zgateway; earlier documents keep that name.
