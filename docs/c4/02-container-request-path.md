# C4 Level 2: Containers: the request path

**Target architecture (planned). Nothing in this diagram is built yet.** Model: diagram `container` in [model.json](model.json). Source of this page: [gen.py](gen.py).

Containers are Go packages inside one process. The shell does all socket I/O and makes no decisions. The engine is one single-threaded state machine that calls the wire layer and the five pure cores. Observability hangs off the side: the engine publishes snapshots, the admin listener serves them.

```mermaid
flowchart TB
  APP(["Client application"])
  OPERATOR(["Operator"])
  PG[("Postgres")]
  TELEMETRY[["Metrics and log readers"]]
  subgraph SCOPE["mini-gateway process"]
    C_SHELL["Shell and binary<br/>cmd/mini-gateway"]
    C_ENGINE["Engine<br/>internal/proxy"]
    C_PGWIRE["Wire layer<br/>internal/pgwire"]
    C_ADM["Admission<br/>internal/admission"]
    C_COAL["Coalescer<br/>internal/coalesce"]
    C_POOL["Pool<br/>internal/pool"]
    C_BATCH["Batcher<br/>internal/batch"]
    C_RETRY["Retry policy<br/>internal/retry"]
    C_OBS["Observability<br/>internal/obs"]
    C_CONFIG["Configuration<br/>internal/config"]
  end
  APP -->|1| C_SHELL
  OPERATOR -->|2| C_SHELL
  C_SHELL -->|3| C_ENGINE
  C_ENGINE -->|4| C_PGWIRE
  C_ENGINE -->|5| C_ADM
  C_ENGINE -->|6| C_COAL
  C_ENGINE -->|7| C_POOL
  C_ENGINE -->|8| C_BATCH
  C_ENGINE -->|9| C_RETRY
  C_SHELL -->|10| PG
  C_ENGINE -->|11| C_OBS
  C_SHELL -->|12| C_OBS
  TELEMETRY -->|13| C_SHELL
  C_OBS -->|14| TELEMETRY
  C_SHELL -->|15| C_CONFIG
```

## Elements

| Element | Kind | What it is |
| --- | --- | --- |
| Client application | person | Any program that speaks the Postgres wire protocol. Its startup user name selects its tenant. |
| Operator | person | Runs proxy instances, writes the static fleet config, reads health and stats, drains an instance with SIGTERM. |
| Shell and binary | container | cmd/mini-gateway and internal/shell. Listeners, one reader and one writer goroutine per connection, bounded mailboxes, a deadline on every socket operation, the admin listener on its own port, signal handling. Makes no decisions: it posts events to the engine and performs the actions the engine returns. |
| Engine | container | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code. |
| Wire layer | container | internal/pgwire. Everything that knows Postgres: framing, both session state machines, the query-cycle tracker, the statement classifier, the error contract. The only product package that imports pgproto3. |
| Admission | container | internal/admission. Rate gate, per-tenant queues with two selectable queue rules, adaptive LIFO, round-robin dispatcher with shares that bind under saturation, adaptive concurrency limit. |
| Coalescer | container | internal/coalesce. Identical provably-read-only statements share one database read. Never writes. |
| Pool | container | internal/pool. Hard-capped database connections, creation limiter, leases per transaction, the one breaker, the dirty-connection table. |
| Batcher | container | internal/batch. Timer-free: when a connection frees up, eligible queued single statements go out in one write. Off by default. |
| Retry policy | container | internal/retry. A policy interface. Off by default; the other policy retries once, only when nothing reached the database, within a 10% budget. |
| Observability | container | internal/obs. Snapshot conventions (the one raw export), integer histogram, collector, the adapter interface and its adapters (Prometheus text, JSON, CSV flush, log line, OTLP push), fleet merge, health. |
| Configuration | container | internal/config. The config file, the static fleet block (instance count N; fleet budgets divided by N), and load-time validation that fails closed. |
| Postgres | softwareSystem, external | One ordinary Postgres server. Sees at most the fleet's connection budget, all logged in as one role. |
| Metrics and log readers | softwareSystem, external | Everything that reads what the adapters produce: a Prometheus fed through an OpenTelemetry Collector or by direct scrape, flushed CSV files read by tests and DuckDB, log readers. They merge across instances; no component knows about them. The observability component view shows them one by one. |

## Relationships

| # | From | To | What | How |
| --- | --- | --- | --- | --- |
| 1 | Client application | Shell and binary | Connects, logs in, sends statements | Postgres wire protocol, TCP |
| 2 | Operator | Shell and binary | Reads health, stats and metrics; sends SIGTERM to drain | HTTP admin port, signal |
| 3 | Shell and binary | Engine | Posts events (bytes arrived, connection opened or closed, clock) and performs the returned actions | Go, bounded mailboxes |
| 4 | Engine | Wire layer | Turns bytes into work units and outcomes; builds replies the proxy makes itself | Go, in-process |
| 5 | Engine | Admission | Gate, enqueue, pick the next request, report completions | Go, in-process |
| 6 | Engine | Coalescer | Join or start a shared read; fan the reply out | Go, in-process |
| 7 | Engine | Pool | Lend and release connections; open and close decisions; breaker state | Go, in-process |
| 8 | Engine | Batcher | Take a batch for a free connection; split the replies | Go, in-process |
| 9 | Engine | Retry policy | Ask whether a failure may be retried | Go, in-process |
| 10 | Shell and binary | Postgres | Opens the pooled connections and moves their bytes | Postgres wire protocol, TCP |
| 11 | Engine | Observability | Publishes one instance snapshot per interval | Go, plain structs |
| 12 | Shell and binary | Observability | Admin listener serves exporters and health from the last published snapshot | Go, in-process |
| 13 | Metrics and log readers | Shell and binary | Scrapes Prometheus text and JSON stats | HTTP admin port |
| 14 | Observability | Metrics and log readers | Pushes OTLP metrics to a Collector that feeds Prometheus (last build chunk); flushes CSV files; writes log lines | OTLP/HTTP JSON, files |
| 15 | Shell and binary | Configuration | Loads and validates the config and the fleet block at start | Go, in-process |
