# C4 Deployment: Deployment: three instances, one database

**Target architecture (planned). Nothing in this diagram is built yet.** Model: diagram `deployment` in [model.json](model.json). Source of this page: [gen.py](gen.py).

Each instance enforces its own limits. The config declares the instance count and divides the fleet budgets. What multiplies, weakens or survives at N instances is the table in PLAN.md section 8.2; the weakened rows are measured in three-instance scenarios.

```mermaid
flowchart LR
  subgraph D_CLIENTS["Application hosts"]
    I_SPREAD["Client connections"]
  end
  subgraph D_TIER["Proxy tier, N = 3"]
    I_GW1["mini-gateway instance 1"]
    I_GW2["mini-gateway instance 2"]
    I_GW3["mini-gateway instance 3"]
  end
  subgraph D_DB["Database host"]
    I_PG[("Postgres 17")]
  end
  subgraph D_OPS["Operations"]
    I_TEL["Scraper or collector"]
  end
  I_SPREAD -->|1| I_GW1
  I_SPREAD -->|2| I_GW2
  I_SPREAD -->|3| I_GW3
  I_GW1 -->|4| I_PG
  I_GW2 -->|5| I_PG
  I_GW3 -->|6| I_PG
  I_TEL -->|7| I_GW1
  I_TEL -->|8| I_GW2
  I_TEL -->|9| I_GW3
```

## Elements

| Element | Kind | What it is |
| --- | --- | --- |
| Application hosts | deploymentNode | Clients of every tenant. They spread their connections over the instances themselves, or through any TCP balancer; we build none. |
| Client connections | infrastructureNode | 500 clients here; each connects to one instance. |
| Proxy tier, N = 3 | deploymentNode | Three independent mini-gateway processes with the same static config: fleet.instances = 3, so each takes a third of every fleet budget. No instance knows about the others. |
| mini-gateway instance 1 | instance | Own engine, pool (fleet budget / 3), queues, flights, limit, retry budget and snapshots. Drains on SIGTERM. |
| mini-gateway instance 2 | instance | Own engine, pool (fleet budget / 3), queues, flights, limit, retry budget and snapshots. Drains on SIGTERM. |
| mini-gateway instance 3 | instance | Own engine, pool (fleet budget / 3), queues, flights, limit, retry budget and snapshots. Drains on SIGTERM. |
| Database host | deploymentNode | One Postgres. Its connection limit must cover the fleet budget plus a reserve; config validation checks it. |
| Postgres 17 | infrastructureNode | Sees at most the fleet connection budget (for example 3 x 32 = 96), all as one role. |
| Operations | deploymentNode | Where metrics are scraped or pushed to, and merged across instances. |
| Scraper or collector | infrastructureNode | Reads each instance's exporters; sums counters, merges histograms, recomputes ratios. |

## Relationships

| # | From | To | What | How |
| --- | --- | --- | --- | --- |
| 1 | Client connections | mini-gateway instance 1 | About a third of the client connections | Postgres wire protocol, TCP |
| 2 | Client connections | mini-gateway instance 2 | About a third of the client connections | Postgres wire protocol, TCP |
| 3 | Client connections | mini-gateway instance 3 | About a third of the client connections | Postgres wire protocol, TCP |
| 4 | mini-gateway instance 1 | Postgres 17 | At most budget / 3 pooled connections; dials at most rate / 3 | Postgres wire protocol, TCP |
| 5 | mini-gateway instance 2 | Postgres 17 | At most budget / 3 pooled connections; dials at most rate / 3 | Postgres wire protocol, TCP |
| 6 | mini-gateway instance 3 | Postgres 17 | At most budget / 3 pooled connections; dials at most rate / 3 | Postgres wire protocol, TCP |
| 7 | Scraper or collector | mini-gateway instance 1 | Scrapes this instance's snapshot | HTTP admin port |
| 8 | Scraper or collector | mini-gateway instance 2 | Scrapes this instance's snapshot | HTTP admin port |
| 9 | Scraper or collector | mini-gateway instance 3 | Scrapes this instance's snapshot | HTTP admin port |
