# C4 Level 3: Components: retry policy

**Built so far: chunks 0–3. Dashed parts are planned.** In this diagram 0 of 7 elements and 0 of 7 relationships are built. Model: diagram `component-retry` in [model.json](model.json). Source of this page: [gen.py](gen.py).

The default policy is Off. Failures after anything reached the database are never retried under any policy.

A solid box or arrow is built; a dashed one is planned. An arrow points to the supplier. An arrow marked "implements" means the source implements the target's interface; every other arrow means "uses".

```mermaid
flowchart TB
  N_ENGINE["Engine"]:::planned
  N_WORK["Work vocabulary"]:::planned
  subgraph SCOPE["Retry policy"]
    R_POLICY["Policy"]:::planned
    R_OFF["Off"]:::planned
    R_ONCE["PreForwardOnce"]:::planned
    R_BUDGET["Retry budget"]:::planned
    R_SNAP["Retry snapshot"]:::planned
  end
  N_ENGINE -.->|1| R_POLICY
  N_ENGINE -.->|2| R_BUDGET
  N_ENGINE -.->|3| R_SNAP
  R_OFF -.->|4 implements| R_POLICY
  R_ONCE -.->|5 implements| R_POLICY
  R_ONCE -.->|6| R_BUDGET
  R_POLICY -.->|7| N_WORK
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
| Policy | component | planned | The interface the engine asks: given this failure — did anything reach the database, is the breaker open, how much of the deadline is left — may it be retried. |
| Off | component | planned | The default. Never retries. |
| PreForwardOnce | component | planned | Retries at most once, only when nothing reached the database, only within budget, never while the breaker is open, never holding a queue slot or a connection while it waits. |
| Retry budget | component | planned | An integer bucket: each first attempt deposits a tenth of a token, each retry takes one; capped, so credit expires. Retries stay at or below 10% of requests. |
| Retry snapshot | component | planned | Snapshot(): attempts, retries, budget level, deposits, withdrawals, exhaustions, refusals by reason. |
| Engine | neighbouring container | planned | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code. |
| Work vocabulary | neighbouring container | planned | internal/work. Small plain types the cores are written against: tenant, unit of work and its class, share key, outcome. Carries no Postgres type. This is the extension seam for a second protocol. |

## Relationships

| # | From | To | Status | What | How |
| --- | --- | --- | --- | --- | --- |
| 1 | Engine | Policy | planned | May this failure be retried | Go |
| 2 | Engine | Retry budget | planned | Deposit on each first attempt | Go |
| 3 | Engine | Retry snapshot | planned | Snapshot() | Go |
| 4 | Off | Policy | planned | Implements | Go, interface implementation |
| 5 | PreForwardOnce | Policy | planned | Implements | Go, interface implementation |
| 6 | PreForwardOnce | Retry budget | planned | Withdraw one token per retry | Go |
| 7 | Policy | Work vocabulary | planned | Reads the outcome's reached-the-database fact | Go types |
