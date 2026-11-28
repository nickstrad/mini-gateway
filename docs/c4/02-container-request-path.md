# C4 Level 2: Containers: the request path

**Built so far: chunks 0–9. Dashed parts are planned.** In this diagram 5 of 13 elements and 12 of 16 relationships are built. Model: diagram `container` in [model.json](model.json). Source of this page: [gen.py](gen.py).

Containers are Go packages inside one process. The shell does all socket I/O and makes no decisions. The engine is one single-threaded state machine that calls the wire layer and the four pure cores. Observability hangs off the side: the engine publishes snapshots, and the admin listener serves them.

A solid box or arrow is built; a dashed one is planned. An arrow points to the supplier.

```mermaid
flowchart TB
  APP(["Client application"]):::built
  OPERATOR(["Operator"]):::built
  PG[("Postgres")]:::built
  TELEMETRY[["Metrics and log readers"]]:::planned
  subgraph SCOPE["mini-gateway process"]
    C_SHELL["Shell and binary<br/>cmd/mini-gateway"]:::built
    C_ENGINE["Engine<br/>internal/proxy"]:::planned
    C_PGWIRE["Wire layer<br/>internal/pgwire"]:::planned
    C_ADM["Admission<br/>internal/admission"]:::planned
    C_COAL["Coalescer<br/>internal/coalesce"]:::planned
    C_POOL["Pool<br/>internal/pool"]:::built
    C_RETRY["Retry policy<br/>internal/retry"]:::planned
    C_OBS["Observability<br/>internal/obs"]:::planned
    C_CONFIG["Configuration<br/>internal/config"]:::planned
  end
  APP -->|1| C_SHELL
  OPERATOR -->|2| C_SHELL
  C_SHELL -->|3| C_ENGINE
  C_ENGINE -->|4| C_PGWIRE
  C_ENGINE -.->|5| C_ADM
  C_ENGINE -.->|6| C_COAL
  C_ENGINE -->|7| C_POOL
  C_ENGINE -.->|8| C_RETRY
  C_SHELL -->|9| PG
  C_ENGINE -->|10| C_OBS
  C_SHELL -->|11| C_OBS
  TELEMETRY -->|12| C_SHELL
  C_OBS -.->|13| TELEMETRY
  C_SHELL -->|14| C_CONFIG
  C_ADM -->|15| C_OBS
  C_POOL -->|16| C_OBS
  subgraph KEY["Key"]
    KEY_BUILT["Built"]:::built
    KEY_PLANNED["Planned"]:::planned
  end
  classDef built fill:#d9ecd9,stroke:#2e6b34,stroke-width:2px,color:#1c1e21
  classDef planned stroke-width:1px,stroke-dasharray:6 4
  style SCOPE stroke-dasharray:6 4
```

## Elements

| Element | Kind | Status | What it is |
| --- | --- | --- | --- |
| Client application | person | built (chunk 6) | Any program that speaks the Postgres wire protocol. Its startup user name selects its tenant. |
| Operator | person | built (chunk 6) | Runs proxy instances, writes the config file with its static fleet block, reads health and stats (directly or with mgctl), stops an instance with SIGTERM (a plain stop so far; a graceful drain from chunk 13). |
| Shell and binary | container | built (chunk 6) | cmd/mini-gateway and internal/shell. The binary (-config, -check, -verifier) and the goroutine shell: the client listener, one reader and one writer goroutine per connection, one bounded mailbox into the engine's goroutine, a bounded write queue per connection, a deadline on every socket operation, TLS on both legs with crypto/tls (it reads exactly the 8 bytes of a client's first packet, upgrades the socket when the engine says so and buffers nothing across the upgrade), TCP keepalive on the database sockets by config, each cancel request sent on a database connection of its own, a short write deadline before every close so no close can wait, the admin listener on its own port, the collector with its JSON, Prometheus and CSV adapters, and signal handling: SIGTERM or SIGINT stops the process (a plain stop so far; the graceful drain is chunk 13). Makes no decisions: it posts events to the engine and performs the actions the engine returns. |
| Engine | container | planned | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code; its test package proxysim is the simulator's driver. **So far:** Built in chunk 6 (internal/proxy, internal/proxy/proxysim): every component drawn here except two parts — the fan-out of a shared reply to its waiters (chunk 11) and the drain sequence (chunk 13). Chunk 7 put the pool core behind the pool, breaker and release seams (corepool.go) and chunk 8 the admission queues behind the queue seam (admission.go). Chunk 9 installed the fair dispatcher on those queues and reports every lease start and end to it (fairness.go). The stages that belong to cores not built yet run their stand-ins: a rate gate that always allows, no coalescing, retry off. |
| Wire layer | container | planned | internal/pgwire. Everything that knows Postgres: framing, both session state machines, the query-cycle tracker, the SQL lexer, the statement classifier with its courtesy reject list, the safe-shape recogniser that decides sharing, the error contract, the wire snapshot. The only product package allowed to import pgproto3. Its test packages pgwiretest, wireworld and clsworld run this code in the simulator. **So far:** Built in chunks 4 and 5 (internal/pgwire): every component drawn here except the codec boundary. Chunk 4: the framer, the hand-written message coding, both session machines, the SCRAM-SHA-256 functions, the two trackers, the startup FATAL replies, the wire snapshot, and the test packages pgwiretest and wireworld. Chunk 5: the SQL lexer, the reject list, the statement classifier, the safe-shape recogniser, the error table with its reply builders, and the test package clsworld. Chunk 6: the engine drives this code, and the client session machine takes every FATAL from the error table (three startup rows added). Not built: the codec boundary (no file in the package imports pgproto3; the reply builders code their messages by hand). Nothing produces outcomes yet. |
| Admission | container | planned | internal/admission. Rate gate, per-tenant queues with two selectable queue rules, adaptive LIFO, a load gauge, a fair dispatcher (a share of the limit per tenant by weight, a tenant below its share served first, round robin), adaptive concurrency limit. **So far:** Built in chunk 8 (internal/admission): the per-tenant queues, the QueueRule interface, the timeout rule, the control-law rule and the two comparison arms, all of what is said of them here; the Dispatcher interface; and Snapshot() for the queues. Built in chunk 9 (fair.go): the fair dispatcher, all of what is said of it here — shares by weight, a tenant below its share first, round robin, the last-lease rule, leases counted per tenant — with the limit it enforces held as a settable integer, fixed at the pool size so far. The engine runs the queues behind its queue seam with the fair dispatcher installed, and the config file always selects one of the two rules. The core does not import internal/work so far: it takes a tenant as a string and a request as an integer id, and the engine converts. Chunk 7's join added Refuse and the shed reason breaker, for the queued requests an opening breaker refuses. Not built: the adaptive limit and the rate gate (chunk 10), with the tenant table's gate state and the snapshot's limit changes and gate refusals. |
| Coalescer | container | planned | internal/coalesce. Identical statements share one database read: in auto mode those the wire layer marks as fitting the safe shape, in hinted mode those carrying the client's mark. Never writes. |
| Pool | container | built (chunk 7) | internal/pool. Hard-capped database connections opened on demand, a creation limiter, leases with two release policies, a check before reuse, the one breaker, the dirty-connection table, cancels. A deterministic core with no Postgres types and no I/O: every call takes the time and returns actions (dial, check, cancel, roll back, reset, close, refuse the queued requests), and it owns every pool deadline. Its tests check it against a reference model and with the fuzz target FuzzPoolOps. |
| Retry policy | container | planned | internal/retry. A policy interface. Off by default; the other policy retries once, only when nothing reached the database, within a 10% budget. |
| Observability | container | planned | internal/obs and its sub-packages; test/analysis; scripts/csv-assert.sh; scripts/report.sh; scripts/admission-head-to-head.sh. Snapshot conventions (the one raw export), integer histogram, metric registry, collector, the adapter interface and its adapters (Prometheus text, JSON, CSV flush, log line, OTLP push), fleet merge, health, the SQL assertions over flushed files, and the story report and the admission head-to-head table over the same files. **So far:** Built in chunks 1, 3, 4, 5, 6, 7, 8 and 9: everything except the OTLP push adapter (chunk 15) and the health rules for the limit's floor and draining. Since chunk 6 the shell creates a collector in the real process, with the engine, wire and classifier sources, and serves the outputs over HTTP. The binary attaches the JSON, Prometheus and CSV adapters; the log-line adapter is attached only by the scenario flush harness. Chunk 6a: the story report, and the load generator's metrics in the catalog. Chunk 7: the pool source with the health rule for the breaker. Chunk 8: the admission source with the health rule for shedding, and the head-to-head table. Chunk 9: the fairness metrics in the admission source, totals and per tenant, with no new health rule. |
| Configuration | container | planned | internal/config. The config file (JSON), the client_tls and backend_tls blocks, the queue block (the rule and its times, the capacities, adaptive LIFO), the pool keys (connect cap, idle timeout, lifetime, jitter, the check, the cancel cap), the lease keys (reset: always or on-switch; reseed), the breaker keys, the backend keys (TCP keepalive, server keepalive, the path's idle limit), the fairness block (a default weight and a weight per tenant), the static fleet block (instance count N; fleet budgets divided by N), load-time validation that fails closed and names the key, warnings for risky settings, and the config fingerprint. **So far:** Built in chunk 6 (internal/config): the JSON config file with its defaults, validation that fails closed and names the key (the timeout nesting too), the client_tls and backend_tls blocks, the config fingerprint, and the fleet block parsed and checked (pool.connections must fit fleet.backend_connections / fleet.instances, and the fleet total must fit the database's limit with a reserve). Built in chunk 7 (pool.go): the pool, lease, breaker, backend and timeout keys, all of what is said of them here; an omitted pool.connections takes the fleet's connections divided by the instance count and the creation rate is the fleet's rate divided the same way, both rounded down and never zero (a total below the instance count is refused, a remainder is a warning). Built in chunk 8 (queue.go): the queue keys rule, target, interval, tenant_capacity, max_tenants, lifo and lifo_hold, with their defaults; queue.rule accepts only timeout and control-law. Built in chunk 9 (fairness.go): the fairness keys default_weight and weights (a tenant and a weight each): 1 to 1,000, zero or omitted meaning the default, at most 4,096 entries, a tenant named twice refused. Not built: the division of budgets whose cores are not built (the rate gate's rates, chunk 10) and the full fleet block (chunk 13). |
| Postgres | softwareSystem, external | built (chunk 6) | One ordinary Postgres server. Sees at most the fleet's connection budget, all logged in as one role. |
| Metrics and log readers | softwareSystem, external | planned | Everything that reads what the adapters produce: a Prometheus fed through an OpenTelemetry Collector or by direct scrape, flushed CSV files read by tests and DuckDB, log readers. They merge across instances; no component knows about them. The observability component view shows them one by one. |

## Relationships

| # | From | To | Status | What | How |
| --- | --- | --- | --- | --- | --- |
| 1 | Client application | Shell and binary | built | Connects, logs in, sends statements | Postgres wire protocol; TCP, TLS when the client asks for it |
| 2 | Operator | Shell and binary | built | Reads health, stats and metrics; sends SIGTERM to stop (a plain stop so far; a graceful drain from chunk 13) | HTTP admin port, signal |
| 3 | Shell and binary | Engine | built | Posts events (bytes arrived, bytes written, connection opened or closed, TLS result, clock) and performs the returned actions | Go, one bounded mailbox |
| 4 | Engine | Wire layer | built | Runs the session machines and trackers over the bytes; turns each Query into a work unit; builds the replies the proxy makes itself | Go, in-process |
| 5 | Engine | Admission | planned | Gate, enqueue, pick the next request, report completions | Go, in-process |
| 6 | Engine | Coalescer | planned | Join or start a shared read; fan the reply out | Go, in-process |
| 7 | Engine | Pool | built | Lend and release connections; performs the pool's open, check, clean-up, reset and close decisions; breaker state | Go, in-process |
| 8 | Engine | Retry policy | planned | Ask whether a failure may be retried | Go, in-process |
| 9 | Shell and binary | Postgres | built | Opens the pooled connections, with TCP keepalive, and moves their bytes; sends each cancel request on a connection of its own | Postgres wire protocol; TCP, TLS by config |
| 10 | Engine | Observability | built | Keeps its counters as one plain snapshot (histograms, gauges, a conservation check); answers each publish event with a publish action | Go, plain structs |
| 11 | Shell and binary | Observability | built | Creates the collector and publishes once per interval; the admin listener serves exporters and health from the last published snapshot | Go, in-process |
| 12 | Metrics and log readers | Shell and binary | built | Scrapes Prometheus text and JSON stats | HTTP admin port |
| 13 | Observability | Metrics and log readers | planned | Pushes OTLP metrics to a Collector that feeds Prometheus (last build chunk); flushes CSV files; writes log lines | OTLP/HTTP JSON, files |
| 14 | Shell and binary | Configuration | built | Loads and validates the config and the fleet block at start | Go, in-process |
| 15 | Admission | Observability | built | Keeps its counters as one plain snapshot (gauges with peaks, queue-wait histograms, a conservation check); the admission source mapping reads it | Go, plain structs |
| 16 | Pool | Observability | built | Keeps its counters as one plain snapshot (gauges with peaks, two histograms, a conservation check); the pool source mapping reads it | Go, plain structs |
