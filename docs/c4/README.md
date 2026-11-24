# C4 diagrams — mini-gateway

**Target architecture of the approved plan.** These diagrams describe what [PLAN.md](../../PLAN.md) sets out to build; Nick approved the plan on 2026-11-24 as amended by his rulings (PLAN.md section 18). The set was updated for those rulings: the batcher is gone (batching was dropped from v1), and TLS is drawn on the shell and the two session machines. Nothing in them exists yet; every element carries the status `planned`. As chunks land, the orchestrator updates the diagrams to the as-built architecture, and a diagram that no longer matches the code is treated as a defect.

Three C4 levels plus a deployment view: system context, containers (two views), components (eight views), and the three-instance deployment.

## Diagrams

<!-- diagram-table:start -->
| # | Level | File | Shows |
| --- | --- | --- | --- |
| 1 | System context | [01-system-context.md](01-system-context.md) | mini-gateway, its clients, its operator, the one Postgres behind it, the readers of its metrics, and its one external library |
| 2 | Containers | [02-container-request-path.md](02-container-request-path.md) | The Go packages inside one mini-gateway process and how a request passes through them |
| 3 | Containers | [03-container-drivers-and-seams.md](03-container-drivers-and-seams.md) | Who drives the engine (the shell or the simulator), where the Postgres codec boundary is, and the work vocabulary the cores are written against |
| 4 | Deployment | [04-deployment-fleet.md](04-deployment-fleet.md) | The supported multi-instance deployment: static fleet config, per-instance enforcement, no run-time coordination |
| 5 | Components | [05-component-engine.md](05-component-engine.md) | internal/proxy: the transport seam, the two connection tables, the request pipeline and its stage seams, the reply relay |
| 6 | Components | [06-component-wire-layer.md](06-component-wire-layer.md) | internal/pgwire: the pgproto3 codec boundary, our framer, both session machines, the classifier, the error contract |
| 7 | Components | [07-component-admission.md](07-component-admission.md) | internal/admission: the rate gate, per-tenant queues, both CoDel modes behind one interface, the dispatcher, the adaptive limit |
| 8 | Components | [08-component-pool.md](08-component-pool.md) | internal/pool: the hard cap, the creation limiter, leases per transaction, the breaker, the dirty-connection table |
| 9 | Components | [09-component-coalescer.md](09-component-coalescer.md) | internal/coalesce: the join rule, the flight table, waiters, the flight's own deadline, the shared reply |
| 10 | Components | [10-component-retry.md](10-component-retry.md) | internal/retry: the policy interface, its two policies, the budget |
| 11 | Components | [11-component-simulator.md](11-component-simulator.md) | internal/sim and its sub-packages: the loop, the clock, simulated byte streams, the fake Postgres, simulated clients, faults, the trace, invariants, replay |
| 12 | Components | [12-component-observability.md](12-component-observability.md) | internal/obs: the one raw export (snapshots), the collector, the adapter interface and its five adapters, and where each output goes: the CSV flush read by tests and DuckDB, and OTLP to a Collector to Prometheus |
<!-- diagram-table:end -->

## Where each required part is drawn

| Part of the plan | Diagrams |
| --- | --- |
| Client-facing protocol layer and the `pgproto3` codec boundary | 3, 6 |
| TLS: the request exchange (wire layer) and the upgrade on real sockets (shell) | 2, 6 |
| Admission: queues, fairness, both CoDel modes, rate gate (GCRA), adaptive limit (AIMD) | 2, 7 |
| Pool | 2, 8 |
| Coalescer | 2, 9 |
| Retry policy | 2, 10 |
| Backend interface verdict: Postgres-first with three seams (transport, wire layer, work vocabulary) | 3, 5 |
| Simulator harness | 3, 11 |
| Observability: snapshot → collector → exporters | 2, 12 |
| Horizontal scale: static fleet config, three instances | 4 |

## Format

- [gen.py](gen.py) is the source. Elements, relationships and diagrams are tables in that file; running it writes everything else here. Edit `gen.py`, not the generated files.
- [model.json](model.json) is the C4 model as a `c4-sdk` bundle (`schemaVersion: 1`): every element, relationship and diagram.
- Each diagram page has a Mermaid picture, an element table and a numbered relationship table. Edge numbers in the picture match the table rows. A dashed arrow means "implements"; a solid arrow means "uses" and points to the supplier.
- [diagrams/](diagrams/) holds the Mermaid sources, one per diagram.

## Model notes

1. **Containers are Go packages** inside one `mini-gateway` process, except the simulator harness and the end-to-end suite, which are test code in the same repository.
2. **Neighbour elements.** `c4-sdk` 0.1.0 accepts a container in another container's component view only when the element is tagged `external`, and the tag belongs to the element, not the view. So each container has a twin with the id prefix `n-`, tagged `external` and `neighbour`, used only in component views. The page tables show the real container's description for it.
3. **Deployment instances** are instances of the container "Shell and binary", which stands for the whole process.
4. **Terminal (Unicode) renderings** of these diagrams are not included in this first set; see the open item in STATE.md.

## Validation

| Check | Tool | Result for this set |
| --- | --- | --- |
| `model.json` decodes | `decodeArchitecture` from `c4-sdk@0.1.0` (`c4-sdk/core`) | ok, 0 errors, 0 warnings |
| Every diagram builds a scene | `buildScene` from `c4-sdk/core` | 12 of 12 |
| Pictures and tables agree with the model | All three are written by `gen.py` from the same tables; it refuses a diagram edge whose ends are not in the diagram and a relationship no diagram uses | passes |
| Mermaid renders | Each built page opened in a headless browser; every picture drew | 12 of 12 |

To repeat after a change:

```sh
python3 docs/c4/gen.py
# in a scratch folder outside /tmp, after `npm init -y && npm install c4-sdk@0.1.0`, with the script copied in
# (Node resolves the import from the script's own folder):
cp /root/Software/mini-gateway/agent_work/phase1/c4-check.mjs . && node c4-check.mjs /root/Software/mini-gateway/docs/c4/model.json
```
