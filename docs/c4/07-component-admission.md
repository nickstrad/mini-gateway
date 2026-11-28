# C4 Level 3: Components: admission

**Built so far: chunks 0–9 and chunk 12 (chunks 10 and 11 have not landed yet). Dashed parts are planned.** In this diagram 7 of 13 elements and 11 of 16 relationships are built. Model: diagram `component-admission` in [model.json](model.json). Source of this page: [gen.py](gen.py).

Decisions run in order: gate, then queue, then dispatch under the limit. The two queue rules are equal, selectable implementations of one interface. Two control loops read two different signals: the queue rule reads queue wait, and the adaptive limit reads database round-trip time. Chunk 8 built the queues, both rules, the comparison arms, and the seam the dispatcher plugs into. Chunk 9 built the fair dispatcher: shares by weight, a tenant below its share first, round robin, and leases counted per tenant. The limit it enforces is a settable integer, fixed at the pool size until the adaptive limit drives it. The gate and the adaptive limit are planned (chunk 10). The core takes a tenant as a string and a request as an integer id, so nothing in it imports the work vocabulary yet.

A solid box or arrow is built; a dashed one is planned. An arrow points to the supplier. An arrow marked "implements" means the source implements the target's interface; every other arrow means "uses".

```mermaid
flowchart TB
  N_ENGINE["Engine"]:::planned
  N_WORK["Work vocabulary"]:::built
  N_OBS["Observability"]:::planned
  subgraph SCOPE["Admission"]
    A_GATE["Rate gate (GCRA)"]:::planned
    A_TENANTS["Tenant table"]:::planned
    A_QUEUES["Per-tenant queues"]:::built
    A_RULE["QueueRule"]:::built
    A_RULE_TIMEOUT["Timeout rule"]:::built
    A_RULE_LAW["Control-law rule"]:::built
    A_ARMS["Comparison arms"]:::built
    A_DISPATCH["Dispatcher"]:::built
    A_LIMIT["Adaptive limit (AIMD)"]:::planned
    A_SNAP["Admission snapshot"]:::planned
  end
  N_ENGINE -.->|1| A_GATE
  N_ENGINE -->|2| A_QUEUES
  N_ENGINE -->|3| A_DISPATCH
  N_ENGINE -.->|4| A_LIMIT
  N_ENGINE -->|5| A_SNAP
  A_QUEUES -->|6| A_RULE
  A_RULE_TIMEOUT -->|7 implements| A_RULE
  A_RULE_LAW -->|8 implements| A_RULE
  A_ARMS -->|9 implements| A_RULE
  A_QUEUES -->|10| A_DISPATCH
  A_DISPATCH -.->|11| A_LIMIT
  A_DISPATCH -->|12| A_TENANTS
  A_GATE -.->|13| A_TENANTS
  A_QUEUES -->|14| A_TENANTS
  A_DISPATCH -.->|15| N_WORK
  A_SNAP -->|16| N_OBS
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
| Rate gate (GCRA) | component | planned | Optional outer gate, off by default: a per-tenant rate and burst in integer nanoseconds. Refuses before anything is queued. Rates are divided by the instance count. |
| Tenant table | component | planned | State for tenants that are active now: the queue with its round-robin turn, and gate state. Created on first use, removed when quiet, so memory follows active tenants. A tenant's counters are kept apart and outlive its entry, up to a cap. The in-flight count is not here: the dispatcher keeps it, for tenants with a lease only. **So far:** Built in chunk 8 (internal/admission: tenantFor, dropQuiet, evictEmpty and statsFor in queues.go; tenant.go): one entry per tenant with a queue (its ring, rule state, LIFO state and load gauge), created on first use and removed after 16 quiet intervals, its ring reused. At most queue.max_tenants entries: at the cap an empty queue is evicted for a new tenant, and if none is empty the arrival is shed. A tenant's counters outlive its entry, for at most 4,096 tenants; later ones are counted as other. Built in chunk 9 (the turn field in tenant.go, set in queues.go and fair.go): the entry's round-robin turn; a new entry joins behind every waiting tenant. Not built: gate state (chunk 10). |
| Per-tenant queues | component | built (chunk 8) | A fixed-capacity ring per tenant, under one total capacity. Age is measured on the oldest item; expired items are swept from the head, at every operation and at the core's own next deadline, and answered at once. A full ring sheds its head, not the arrival; a full total sheds the oldest request of all. Serves newest-first while the rule reports overload (adaptive LIFO), and oldest-first again once the rule has reported no overload for a hold time. Keeps folly's minimum-delay figure as a load gauge, 0 to 100. A queued request the caller refuses (the breaker opened) is counted as a shed with reason breaker and does not mark the queue as shedding. |
| QueueRule | component | built (chunk 8) | The interface both shedding rules implement; chosen by config (queue.rule: timeout or control-law), per instance. |
| Timeout rule | component | built (chunk 8) | Mode 1, from "Fail at Scale": one last-empty timestamp per queue; allowed wait is the short target while a standing queue exists, otherwise the interval. It is evaluated at the instant, on the oldest item, so the first burst after idle gets the interval. |
| Control-law rule | component | built (chunk 8) | Mode 2, from the CoDel paper and RFC 8289: sojourn above target for an interval starts dropping; next drop after interval / sqrt(count), in integer arithmetic (an exact floor). Its checks run at every queue operation and at the core's next deadline, not only at a pop; a drop sheds the oldest request. |
| Comparison arms | component, test code | built (chunk 8) | Plain FIFO and folly's minimum-delay detector, for the head-to-head table. Only tests and the simulator can select them: the config file's parser accepts neither. |
| Dispatcher | component | built (chunk 9) | The interface the queues ask at each pop, and the fair dispatcher the engine installs behind it. It enforces one limit on leases in flight, set from outside, and lends nothing at the limit. Each active tenant (a request waiting or a lease in flight) has a share: the limit times its weight over the active tenants' weights, rounded down, at least 1. A waiting tenant below its share is chosen before any tenant at or above it; within each group the choice is round robin. The last free lease is held back only while an active tenant with nothing waiting is below its share; otherwise no lease idles while a request waits. It counts leases in flight per tenant from the lease starts and ends reported to it, so a transaction held open counts; keeps state only for tenants with a lease; and never takes a lease back. It also says how many waiting requests it would release now. Weights are checked at load. A second implementation, oldest head first, is what a core without it uses (tests only). |
| Adaptive limit (AIMD) | component | planned | One integer limit on concurrent leases. Cut by a tenth on a slow statement, a timeout or a connection failure; raised by one when busy. Bounded by a floor, the pool size, a maximum step and a minimum interval. Reads database round-trip time, never queue wait. **So far:** Built in chunk 9 (internal/admission: SetLimit and Limit in fair.go): the limit as one integer the fair dispatcher enforces, settable at any instant; the engine sets it once, to pool.connections. Not built: everything that changes it — the cuts, the raises and their bounds (chunk 10). |
| Admission snapshot | component | planned | Snapshot(): per tenant and total — depth, capacity, oldest-item age, queue-wait histogram, rule state, LIFO switches, sheds by reason, the load gauge, in flight, share, time at share, the dispatcher's totals (limit, active tenants, rounds, selections held back, lease starts and ends, saturated time), the limit's changes, gate refusals. **So far:** Built in chunk 8 (internal/admission: snapshot.go): per tenant and total — depth, capacity, oldest-item age, queue-wait histogram, rule state, LIFO state and switches, sheds by reason, the load gauge, shedding — with a conservation check (pushes = served + shed + removed + queued). Built in chunk 9 (FairSnapshot and fairSnapshot in fair.go; three fields in snapshot.go): per tenant in flight, share and time at share; in total the limit, in flight, active tenants, tenants with a lease, rounds, selections held back by reason, lease starts and ends, and saturated time; with its check (lease starts - ends = in flight = the sum over tenants). Not built: the limit's changes and gate refusals (chunk 10). |
| Engine | neighbouring container | planned | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code; its test package proxysim is the simulator's driver. **So far:** Built in chunk 6 (internal/proxy, internal/proxy/proxysim): every component drawn here except two parts — the fan-out of a shared reply to its waiters (chunk 11) and the drain sequence (chunk 13). Chunk 7 put the pool core behind the pool, breaker and release seams (corepool.go) and chunk 8 the admission queues behind the queue seam (admission.go). Chunk 9 installed the fair dispatcher on those queues and reports every lease start and end to it (fairness.go). Chunk 12 put the retry policy behind the retry seam (retry.go); a granted retry re-enters at the queue stage. The stages that belong to cores not built yet run their stand-ins: a rate gate that always allows, no coalescing. |
| Work vocabulary | neighbouring container | built (chunk 5) | internal/work. Small plain types the cores are written against: tenant, unit of work with its class, reject reason and sharing flags, share key and its scope, outcome, refusal. Carries no Postgres type and imports only the standard library. This is the extension seam for a second protocol. |
| Observability | neighbouring container | planned | internal/obs and its sub-packages; test/analysis; scripts/csv-assert.sh; scripts/report.sh; scripts/admission-head-to-head.sh. Snapshot conventions (the one raw export), integer histogram, metric registry, collector, the adapter interface and its adapters (Prometheus text, JSON, CSV flush, log line, OTLP push), fleet merge, health, the SQL assertions over flushed files, and the story report and the admission head-to-head table over the same files. **So far:** Built in chunks 1, 3, 4, 5, 6, 7, 8, 9 and 12: everything except the OTLP push adapter (chunk 15) and the health rules for the limit's floor and draining. Since chunk 6 the shell creates a collector in the real process, with the engine, wire and classifier sources, and serves the outputs over HTTP. The binary attaches the JSON, Prometheus and CSV adapters; the log-line adapter is attached only by the scenario flush harness. Chunk 6a: the story report, and the load generator's metrics in the catalog. Chunk 7: the pool source with the health rule for the breaker. Chunk 8: the admission source with the health rule for shedding, and the head-to-head table. Chunk 9: the fairness metrics in the admission source, totals and per tenant, with no new health rule. Chunk 12: the retry source and the engine's retry metrics, with no new health rule. |

## Relationships

| # | From | To | Status | What | How |
| --- | --- | --- | --- | --- | --- |
| 1 | Engine | Rate gate (GCRA) | planned | May this request enter | Go |
| 2 | Engine | Per-tenant queues | built | Enqueue, pop, remove, refuse; sweep expired; take the shed requests and the next deadline | Go |
| 3 | Engine | Dispatcher | built | Installs it with the limit and the weights; reports every lease start and end; asks how many waiting requests it would release | Go |
| 4 | Engine | Adaptive limit (AIMD) | planned | Statement time and outcome of each completion | Go |
| 5 | Engine | Admission snapshot | built | Snapshot() | Go |
| 6 | Per-tenant queues | QueueRule | built | Allowed wait; overloaded or not | Go |
| 7 | Timeout rule | QueueRule | built | Implements | Go, interface implementation |
| 8 | Control-law rule | QueueRule | built | Implements | Go, interface implementation |
| 9 | Comparison arms | QueueRule | built | Implement (simulator only) | Go, interface implementation |
| 10 | Per-tenant queues | Dispatcher | built | At each pop: which tenant's queue is served next, or none; a pop put back is undone there too | Go |
| 11 | Dispatcher | Adaptive limit (AIMD) | planned | Reads the current limit | Go |
| 12 | Dispatcher | Tenant table | built | Which tenants wait, and each one's turn; adds time at share to a tenant's counters | Go |
| 13 | Rate gate (GCRA) | Tenant table | planned | Per-tenant gate state | Go |
| 14 | Per-tenant queues | Tenant table | built | Per-tenant queue | Go |
| 15 | Dispatcher | Work vocabulary | planned | Tenants as work-vocabulary types (so far a tenant is a string and the engine converts) | Go types |
| 16 | Admission snapshot | Observability | built | Uses the gauge, the histogram and the conservation check; the admission source mapping reads the snapshot | Go, plain structs |
