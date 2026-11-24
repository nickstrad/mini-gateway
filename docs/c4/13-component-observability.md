# C4 Level 3: Components: observability

**Target architecture (planned). Nothing in this diagram is built yet.** Model: diagram `component-observability` in [model.json](model.json). Source of this page: [gen.py](gen.py).

The hard rule: one common raw export, then the collector interface, then adapters. Components fill plain structs; the collector assembles them; every output form is an adapter that reads only what the collector publishes. No component knows about CSV, DuckDB, OTel or Prometheus, and adding an adapter touches no component. Two paths leave the process. The testing path: the CSV flush adapter writes files, and tests and DuckDB read them with nothing else running. The production path: the OTLP adapter pushes to an OpenTelemetry Collector, which Prometheus scrapes. DuckDB is an analysis tool over files, never a dependency of the proxy.

A dashed arrow means "implements"; a solid arrow means "uses" and points to the supplier.

```mermaid
flowchart TB
  N_ENGINE["Engine"]
  N_SHELL["Shell and binary"]
  N_E2E["End-to-end suite and load generator"]
  CSVFILES[["Flushed CSV files"]]
  DUCKDB[["DuckDB (analysis tool)"]]
  OTELCOL[["OpenTelemetry Collector"]]
  PROM[["Prometheus"]]
  subgraph SCOPE["Observability"]
    O_CONTRACT["Snapshot conventions"]
    O_HIST["Integer histogram"]
    O_COLLECTOR["Collector"]
    O_ADAPTER["Adapter interface"]
    O_MERGE["Fleet merge"]
    O_HEALTH["Health"]
    O_PROM["Prometheus text exporter"]
    O_JSON["JSON stats exporter"]
    O_CSV["CSV flush adapter"]
    O_LOG["Log-line exporter"]
    O_OTLP["OTLP/HTTP push exporter"]
  end
  N_ENGINE -->|1| O_COLLECTOR
  O_COLLECTOR -->|2| O_CONTRACT
  O_CONTRACT -->|3| O_HIST
  O_COLLECTOR -->|4| O_HEALTH
  O_ADAPTER -->|5| O_COLLECTOR
  O_PROM -.->|6| O_ADAPTER
  O_JSON -.->|7| O_ADAPTER
  O_CSV -.->|8| O_ADAPTER
  O_LOG -.->|9| O_ADAPTER
  O_OTLP -.->|10| O_ADAPTER
  N_SHELL -->|11| O_PROM
  N_SHELL -->|12| O_JSON
  N_SHELL -->|13| O_HEALTH
  O_CSV -->|14| CSVFILES
  N_E2E -->|15| O_CSV
  DUCKDB -->|16| CSVFILES
  N_E2E -->|17| DUCKDB
  O_OTLP -->|18| OTELCOL
  PROM -->|19| OTELCOL
  PROM -->|20| N_SHELL
  O_MERGE -->|21| O_CONTRACT
```

## Elements

| Element | Kind | What it is |
| --- | --- | --- |
| Snapshot conventions | component | The one common raw export. What every component's Snapshot() returns: a plain struct of monotonic counters and current gauges. No wall-clock time, no map order, no telemetry imports. Nothing else leaves a component. |
| Integer histogram | component | Fixed array, fixed relative error, no allocation on record; record, merge, percentile. Out-of-range values are clamped and counted. |
| Collector | component | Assembles one instance snapshot from the component snapshots, with instance id, build and config fingerprint. Keeps the last published copy; readers never wait on the engine. The only reader of component snapshots. |
| Adapter interface | component | The one interface every output form implements: take the published instance snapshot, produce output. One conformance test for all adapters. Components cannot see past it, so none knows about CSV, DuckDB, OTel or Prometheus. |
| Fleet merge | component | Merges instance snapshots: counters and gauges sum, histograms merge, ratios are recomputed, never averaged. |
| Health | component | Three states: ok; degraded while shedding, while the breaker is open, at the limit's floor, or draining; failed only when this instance cannot work. A database outage is degraded. |
| Prometheus text exporter | component | Low-cardinality labels and a bounded tenant label (configured tenants up to a cap, the rest as other). |
| JSON stats exporter | component | The full snapshot, including every active tenant. |
| CSV flush adapter | component | Flushes snapshots to disk at each publish and at the end of a run, in a stable versioned schema with unbounded per-tenant rows. The testing path: tests assert on the files with no Collector or Prometheus running. Bounded size; drop-and-count on a write error. |
| Log-line exporter | component | One structured line per interval. |
| OTLP/HTTP push exporter | component | The final adapter and the proxy's end of the production path. Hand-written on the standard library, no SDK. Cumulative sums, gauges, short-list histogram buckets, bounded tenant label. Bounded queue, timeout, drop-and-count when the Collector is down. Lands in the last build chunk and touches no component. |
| Engine | neighbouring container | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code. |
| Shell and binary | neighbouring container | cmd/mini-gateway and internal/shell. Listeners, one reader and one writer goroutine per connection, bounded mailboxes, a deadline on every socket operation, the admin listener on its own port, signal handling. Makes no decisions: it posts events to the engine and performs the actions the engine returns. |
| End-to-end suite and load generator | neighbouring container | test/e2e and cmd/loadgen. Each scenario is written once and runs on the simulator (tier S) and on the real binary against a real Postgres (tier R). The load generator is open-loop. |
| Flushed CSV files | softwareSystem, external | One directory per run on local disk: run.csv, metrics.csv, histograms.csv, schema version 1 (PLAN.md section 7.7). Same columns in every flush; every tenant has its own rows. Nothing has to be running to read them. |
| DuckDB (analysis tool) | softwareSystem, external | The DuckDB command-line tool, run after a run by test and benchmark scripts and by hand. Reads the flushed files with read_csv and runs SQL assertions and report queries. Never imported, linked or started by the proxy. |
| OpenTelemetry Collector | softwareSystem, external | Receives the proxy's OTLP/HTTP push, batches it and exposes the metrics on a Prometheus endpoint. Dev setup: docker-compose in deploy/obs-dev (chunk 16). |
| Prometheus | softwareSystem, external | End of the production path. Scrapes the Collector; sees low-cardinality labels and the bounded tenant label only. Fleet totals are sums over the instance label. |

## Relationships

| # | From | To | What | How |
| --- | --- | --- | --- | --- |
| 1 | Engine | Collector | Hands over every component's snapshot at each publish | Go, plain structs |
| 2 | Collector | Snapshot conventions | Assembles the instance snapshot | Go |
| 3 | Snapshot conventions | Integer histogram | Histograms inside snapshots | Go |
| 4 | Collector | Health | Derives the health state and its reasons | Go |
| 5 | Adapter interface | Collector | Receives the last published snapshot; all an adapter can read | Go |
| 6 | Prometheus text exporter | Adapter interface | Implements the adapter interface | Go, interface implementation |
| 7 | JSON stats exporter | Adapter interface | Implements the adapter interface | Go, interface implementation |
| 8 | CSV flush adapter | Adapter interface | Implements the adapter interface | Go, interface implementation |
| 9 | Log-line exporter | Adapter interface | Implements the adapter interface | Go, interface implementation |
| 10 | OTLP/HTTP push exporter | Adapter interface | Implements the adapter interface | Go, interface implementation |
| 11 | Shell and binary | Prometheus text exporter | Admin listener serves /metrics | HTTP |
| 12 | Shell and binary | JSON stats exporter | Admin listener serves /stats | HTTP |
| 13 | Shell and binary | Health | Admin listener serves /health | HTTP |
| 14 | CSV flush adapter | Flushed CSV files | Flushes rows at each publish and at the end of a run | Files, schema version 1 |
| 15 | End-to-end suite and load generator | CSV flush adapter | Turns the flush on for every run; checks the final flush equals the snapshot | Go test |
| 16 | DuckDB (analysis tool) | Flushed CSV files | Reads the files directly | read_csv, SQL |
| 17 | End-to-end suite and load generator | DuckDB (analysis tool) | Runs SQL assertions and report queries after a run | scripts/csv-assert.sh |
| 18 | OTLP/HTTP push exporter | OpenTelemetry Collector | Pushes metrics every interval | OTLP/HTTP JSON |
| 19 | Prometheus | OpenTelemetry Collector | Scrapes the Collector's Prometheus endpoint | HTTP |
| 20 | Prometheus | Shell and binary | Can scrape /metrics directly instead (no Collector) | HTTP admin port |
| 21 | Fleet merge | Snapshot conventions | Merges snapshots from several instances | Go |
