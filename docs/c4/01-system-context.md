# C4 Level 1: System context

**Built so far: chunks 0–2. Dashed parts are planned.** In this diagram 2 of 7 elements and 0 of 7 relationships are built. Model: diagram `context` in [model.json](model.json). Source of this page: [gen.py](gen.py).

Clients speak the Postgres wire protocol to mini-gateway as if it were the database. The proxy holds the only connections to Postgres. Metrics leave through exporters; nothing inside the proxy depends on who reads them.

A solid box or arrow is built; a dashed one is planned. An arrow points to the supplier.

```mermaid
flowchart TB
  APP(["Client application"]):::planned
  OPERATOR(["Operator"]):::planned
  DEVELOPER(["Developer or reader"]):::built
  GW["mini-gateway"]:::planned
  PG[("Postgres")]:::planned
  TELEMETRY[["Metrics and log readers"]]:::planned
  PGPROTO3[["pgproto3 (from pgx/v5)"]]:::built
  APP -.->|1| GW
  OPERATOR -.->|2| GW
  DEVELOPER -.->|3| GW
  GW -.->|4| PG
  TELEMETRY -.->|5| GW
  GW -.->|6| TELEMETRY
  GW -.->|7| PGPROTO3
  subgraph KEY["Key"]
    KEY_BUILT["Built"]:::built
    KEY_PLANNED["Planned"]:::planned
  end
  classDef built fill:#d9ecd9,stroke:#2e6b34,stroke-width:2px,color:#1c1e21
  classDef planned stroke-width:1px,stroke-dasharray:6 4
```

## Elements

| Element | Kind | Status | What it is |
| --- | --- | --- | --- |
| Client application | person | planned | Any program that speaks the Postgres wire protocol. Its startup user name selects its tenant. |
| Operator | person | planned | Runs proxy instances, writes the static fleet config, reads health and stats, drains an instance with SIGTERM. |
| Developer or reader | person | built (chunk 1) | Runs simulator scenarios from a seed, replays a failure from its printed REPLAY line, runs make test / chaos / bench. |
| mini-gateway | softwareSystem | planned | A proxy that speaks the Postgres wire protocol (simple-query subset) in front of one Postgres: a hard-capped connection pool lent per transaction, per-tenant admission with two CoDel modes, read coalescing, an off-by-default retry policy, snapshot observability. Terminates client TLS; logs clients in with SCRAM-SHA-256. Runs as N independent instances. |
| Postgres | softwareSystem, external | planned | One ordinary Postgres server. Sees at most the fleet's connection budget, all logged in as one role. |
| Metrics and log readers | softwareSystem, external | planned | Everything that reads what the adapters produce: a Prometheus fed through an OpenTelemetry Collector or by direct scrape, flushed CSV files read by tests and DuckDB, log readers. They merge across instances; no component knows about them. The observability component view shows them one by one. |
| pgproto3 (from pgx/v5) | softwareSystem, external | built (chunk 2) | Go package github.com/jackc/pgx/v5/pgproto3: Postgres wire message structs with Encode and Decode. The one external dependency, used for message encoding only. |

## Relationships

| # | From | To | Status | What | How |
| --- | --- | --- | --- | --- | --- |
| 1 | Client application | mini-gateway | planned | Connects, logs in, sends statements and transactions | Postgres wire protocol v3, simple query; TCP, TLS when the client asks for it |
| 2 | Operator | mini-gateway | planned | Configures the fleet; reads health and stats; drains | Config file, HTTP admin port, SIGTERM |
| 3 | Developer or reader | mini-gateway | planned | Runs simulator scenarios and the test, chaos and benchmark commands | go test, make, cmd/mgsim |
| 4 | mini-gateway | Postgres | planned | Lends pooled connections per transaction; at most the configured budget | Postgres wire protocol v3; TCP, TLS by config |
| 5 | Metrics and log readers | mini-gateway | planned | Scrapes metrics and stats from every instance and merges them | HTTP: Prometheus text, JSON |
| 6 | mini-gateway | Metrics and log readers | planned | Pushes metrics to a Collector that feeds Prometheus; flushes CSV files; writes log lines | OTLP/HTTP JSON, files |
| 7 | mini-gateway | pgproto3 (from pgx/v5) | planned | Encodes and decodes wire messages | Go import, wire layer only |
