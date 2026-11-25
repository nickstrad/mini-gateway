# C4 Level 3: Components: pool

**Built so far: chunks 0–4. Dashed parts are planned.** In this diagram 0 of 10 elements and 0 of 12 relationships are built. Model: diagram `component-pool` in [model.json](model.json). Source of this page: [gen.py](gen.py).

A request never waits inside the pool: the dispatcher releases it only when a lease is available. The pool's job is the cap, the creation rate, and never lending a connection that is not idle and clean.

A solid box or arrow is built; a dashed one is planned. An arrow points to the supplier.

```mermaid
flowchart TB
  N_ENGINE["Engine"]:::planned
  N_WORK["Work vocabulary"]:::planned
  subgraph SCOPE["Pool"]
    P_ACCT["Connection accounting"]:::planned
    P_CREATE["Creation limiter"]:::planned
    P_LEASE["Lease table"]:::planned
    P_BREAKER["Database-down breaker"]:::planned
    P_DIRTY["Dirty-connection table"]:::planned
    P_REAPER["Idle and lifetime reaper"]:::planned
    P_CANCEL["Cancel map"]:::planned
    P_SNAP["Pool snapshot"]:::planned
  end
  N_ENGINE -.->|1| P_LEASE
  N_ENGINE -.->|2| P_BREAKER
  N_ENGINE -.->|3| P_DIRTY
  N_ENGINE -.->|4| P_CANCEL
  N_ENGINE -.->|5| P_SNAP
  P_LEASE -.->|6| P_ACCT
  P_ACCT -.->|7| P_CREATE
  P_CREATE -.->|8| P_BREAKER
  P_DIRTY -.->|9| P_ACCT
  P_REAPER -.->|10| P_ACCT
  P_CANCEL -.->|11| P_LEASE
  P_LEASE -.->|12| N_WORK
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
| Connection accounting | component | planned | Every database connection is connecting, idle, lent or closing. Invariant: open + connecting never exceeds the pool size, which is the fleet budget divided by the instance count. |
| Creation limiter | component | planned | Caps concurrent connects and the rate of new connections, so a storm or a cold start cannot flood the database. |
| Lease table | component | planned | Lends a connection for one transaction: held while the status byte says in-transaction or failed, released on idle. Enforces the idle-in-transaction deadline. |
| Database-down breaker | component | planned | The one circuit breaker. Counts connection-class failures only, never SQL errors. Open: requests are refused at once and no dial is made. Half-open: one trial connect. |
| Dirty-connection table | component | planned | What to do with a connection whose client vanished or that surprised us: cancel, wait a bounded time, roll back, then reuse only if it is idle and clean; otherwise close. Destroy on doubt. |
| Idle and lifetime reaper | component | planned | Closes idle and old connections, with jitter so they do not all expire together; checks a long-idle connection before reuse. |
| Cancel map | component | planned | Maps proxy-issued key data to the connection a client currently holds, so a client's cancel request reaches its own statement and no one else's. Caps concurrent cancels. |
| Pool snapshot | component | planned | Snapshot(): open, idle, lent, connecting, creation rate, closes by reason, exhaustion events, breaker state and transitions, dirty-table outcomes, lease-time histogram. |
| Engine | neighbouring container | planned | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code. |
| Work vocabulary | neighbouring container | planned | internal/work. Small plain types the cores are written against: tenant, unit of work and its class, share key, outcome. Carries no Postgres type. This is the extension seam for a second protocol. |

## Relationships

| # | From | To | Status | What | How |
| --- | --- | --- | --- | --- | --- |
| 1 | Engine | Lease table | planned | Lend to a work unit; release on outcome | Go |
| 2 | Engine | Database-down breaker | planned | Reports connect and connection failures; asks whether to fail fast | Go |
| 3 | Engine | Dirty-connection table | planned | Client vanished, deadline passed, or protocol surprise | Go |
| 4 | Engine | Cancel map | planned | Client cancel request | Go |
| 5 | Engine | Pool snapshot | planned | Snapshot() | Go |
| 6 | Lease table | Connection accounting | planned | Idle to lent and back | Go |
| 7 | Connection accounting | Creation limiter | planned | May another connection be opened now | Go |
| 8 | Creation limiter | Database-down breaker | planned | Is the database marked down | Go |
| 9 | Dirty-connection table | Connection accounting | planned | Reuse or close | Go |
| 10 | Idle and lifetime reaper | Connection accounting | planned | Close idle and old connections | Go |
| 11 | Cancel map | Lease table | planned | Finds the lent connection | Go |
| 12 | Lease table | Work vocabulary | planned | Work units and outcomes (released clean, still held, dirty) | Go types |
