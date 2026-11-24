# C4 Level 3: Components: pool

**Target architecture (planned). Nothing in this diagram is built yet.** Model: diagram `component-pool` in [model.json](model.json). Source of this page: [gen.py](gen.py).

A request never waits inside the pool: the dispatcher releases it only when a lease is available. The pool's job is the cap, the creation rate, and never lending a connection that is not idle and clean.

```mermaid
flowchart TB
  N_ENGINE["Engine"]
  N_WORK["Work vocabulary"]
  subgraph SCOPE["Pool"]
    P_ACCT["Connection accounting"]
    P_CREATE["Creation limiter"]
    P_LEASE["Lease table"]
    P_BREAKER["Database-down breaker"]
    P_DIRTY["Dirty-connection table"]
    P_REAPER["Idle and lifetime reaper"]
    P_CANCEL["Cancel map"]
    P_SNAP["Pool snapshot"]
  end
  N_ENGINE -->|1| P_LEASE
  N_ENGINE -->|2| P_BREAKER
  N_ENGINE -->|3| P_DIRTY
  N_ENGINE -->|4| P_CANCEL
  N_ENGINE -->|5| P_SNAP
  P_LEASE -->|6| P_ACCT
  P_ACCT -->|7| P_CREATE
  P_CREATE -->|8| P_BREAKER
  P_DIRTY -->|9| P_ACCT
  P_REAPER -->|10| P_ACCT
  P_CANCEL -->|11| P_LEASE
  P_LEASE -->|12| N_WORK
```

## Elements

| Element | Kind | What it is |
| --- | --- | --- |
| Connection accounting | component | Every database connection is connecting, idle, lent or closing. Invariant: open + connecting never exceeds the pool size, which is the fleet budget divided by the instance count. |
| Creation limiter | component | Caps concurrent connects and the rate of new connections, so a storm or a cold start cannot flood the database. |
| Lease table | component | Lends a connection for one transaction: held while the status byte says in-transaction or failed, released on idle. Enforces the idle-in-transaction deadline. |
| Database-down breaker | component | The one circuit breaker. Counts connection-class failures only, never SQL errors. Open: requests are refused at once and no dial is made. Half-open: one trial connect. |
| Dirty-connection table | component | What to do with a connection whose client vanished or that surprised us: cancel, wait a bounded time, roll back, then reuse only if it is idle and clean; otherwise close. Destroy on doubt. |
| Idle and lifetime reaper | component | Closes idle and old connections, with jitter so they do not all expire together; checks a long-idle connection before reuse. |
| Cancel map | component | Maps proxy-issued key data to the connection a client currently holds, so a client's cancel request reaches its own statement and no one else's. Caps concurrent cancels. |
| Pool snapshot | component | Snapshot(): open, idle, lent, connecting, creation rate, closes by reason, exhaustion events, breaker state and transitions, dirty-table outcomes, lease-time histogram. |
| Engine | neighbouring container | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code. |
| Work vocabulary | neighbouring container | internal/work. Small plain types the cores are written against: tenant, unit of work and its class, share key, outcome. Carries no Postgres type. This is the extension seam for a second protocol. |

## Relationships

| # | From | To | What | How |
| --- | --- | --- | --- | --- |
| 1 | Engine | Lease table | Lend to a work unit; release on outcome | Go |
| 2 | Engine | Database-down breaker | Reports connect and connection failures; asks whether to fail fast | Go |
| 3 | Engine | Dirty-connection table | Client vanished, deadline passed, or protocol surprise | Go |
| 4 | Engine | Cancel map | Client cancel request | Go |
| 5 | Engine | Pool snapshot | Snapshot() | Go |
| 6 | Lease table | Connection accounting | Idle to lent and back | Go |
| 7 | Connection accounting | Creation limiter | May another connection be opened now | Go |
| 8 | Creation limiter | Database-down breaker | Is the database marked down | Go |
| 9 | Dirty-connection table | Connection accounting | Reuse or close | Go |
| 10 | Idle and lifetime reaper | Connection accounting | Close idle and old connections | Go |
| 11 | Cancel map | Lease table | Finds the lent connection | Go |
| 12 | Lease table | Work vocabulary | Work units and outcomes (released clean, still held, dirty) | Go types |
