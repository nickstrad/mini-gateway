# C4 diagrams — mini-gateway

**Target architecture of the approved plan.** These diagrams describe the system [PLAN.md](../../PLAN.md) sets out to build. Nick approved the plan on 2026-11-24 as amended by his rulings (PLAN.md section 18). The set reflects those rulings: the batcher is gone because batching was dropped from v1, and TLS is drawn on the shell and the two session machines.

Because the build has started, each diagram shows both what exists in the code (status `existing`, drawn solid) and what is still planned (status `planned`, drawn dashed). At every chunk landing the orchestrator marks what the chunk built — one edit to the AS BUILT block at the top of [gen.py](gen.py), described in [docs/knowledge/c4-as-built.md](../knowledge/c4-as-built.md) — and a diagram that no longer matches the code is treated as a defect.

The set covers three C4 levels plus a deployment view: system context, containers (two views), components (eight views), and the three-instance deployment.

## Diagrams

<!-- diagram-table:start -->
**Built so far: chunks 0–6. Dashed parts are planned.** The Built column counts the elements of each diagram that exist in the code.

| # | Level | File | Built | Shows |
| --- | --- | --- | --- | --- |
| 1 | System context | [01-system-context.md](01-system-context.md) | 5 of 7 | mini-gateway, its clients, its operator, the one Postgres behind it, the readers of its metrics, and its one external library |
| 2 | Containers | [02-container-request-path.md](02-container-request-path.md) | 4 of 13 | The Go packages inside one mini-gateway process and how a request passes through them |
| 3 | Containers | [03-container-drivers-and-seams.md](03-container-drivers-and-seams.md) | 5 of 13 | Who drives the engine (the shell or the simulator), where the Postgres codec boundary is, and the work vocabulary the cores are written against |
| 4 | Deployment | [04-deployment-fleet.md](04-deployment-fleet.md) | 0 of 10 | The supported multi-instance deployment: static fleet config, per-instance enforcement, no run-time coordination |
| 5 | Components | [05-component-engine.md](05-component-engine.md) | 12 of 21 | internal/proxy: the transport seam and its simulator driver, the two connection tables, the request pipeline and its stage seams, the lease end with its reset, the reply relay, the failure classifier, deadlines, the snapshot |
| 6 | Components | [06-component-wire-layer.md](06-component-wire-layer.md) | 20 of 23 | internal/pgwire: our framer and message coding, both session machines, SCRAM, the two trackers, the wire snapshot, the SQL lexer, the classifier with its courtesy reject list, the safe-shape recogniser, the error contract and reply builders, the test proxies and their scenarios; planned: the pgproto3 codec boundary |
| 7 | Components | [07-component-admission.md](07-component-admission.md) | 1 of 12 | internal/admission: the rate gate, per-tenant queues, both CoDel modes behind one interface, the dispatcher, the adaptive limit |
| 8 | Components | [08-component-pool.md](08-component-pool.md) | 1 of 10 | internal/pool: the hard cap, the creation limiter, leases per transaction, the breaker, the dirty-connection table |
| 9 | Components | [09-component-coalescer.md](09-component-coalescer.md) | 1 of 9 | internal/coalesce: the join rule, the flight table, waiters, the flight's own deadline, the shared reply |
| 10 | Components | [10-component-retry.md](10-component-retry.md) | 1 of 7 | internal/retry: the policy interface, its two policies, the budget |
| 11 | Components | [11-component-simulator.md](11-component-simulator.md) | 19 of 22 | internal/sim and its sub-packages, internal/testclient and internal/randdist: the loop, the clock, simulated byte streams, the simulated TLS handshake, the fake Postgres, simulated clients, the test client, the scenario world with its baselines and fidelity runner, faults and the shrinker, the trace, invariants and the quiesce check, replay, the toy protocol |
| 12 | Components | [12-component-observability.md](12-component-observability.md) | 24 of 32 | internal/obs: the one raw export (snapshots), the metric registry, the collector, the adapter interface and its five adapters, and where each output goes: the CSV flush read by tests and DuckDB, and OTLP to a Collector to Prometheus |
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

- [gen.py](gen.py) is the source for this directory. It defines elements, relationships, and diagrams in tables and generates every other file here when run. Edit `gen.py`, not the generated files. What is built lives in one place, the AS BUILT block at the top of the file: `LANDED` names the last landed chunk, `BUILT` maps each built element to its chunk, `PARTIAL` records what exists so far for a partly built element, and `REL_BUILT` lists exceptions to the rule that a relationship is built when both of its ends are.
- [model.json](model.json) holds the full C4 model — every element, relationship, and diagram — as a `c4-sdk` bundle (`schemaVersion: 1`). It uses the SDK's statuses: `existing` for built items, `planned` for the rest, and `mixed` for a diagram that contains both.
- Each diagram page contains a Mermaid diagram, an element table, and a numbered relationship table whose row numbers match the edge numbers in the diagram. Solid boxes and arrows represent built parts, dashed ones represent planned parts, and every diagram includes a key. Arrows point to the supplier. An arrow marked "implements" means the source implements the target's interface. Both tables have a Status column showing `built (chunk N)` or `planned`. A partly built element stays `planned`, and its row notes what exists so far.
- [diagrams/](diagrams/) holds the Mermaid sources, one per diagram.

## Model notes

1. **Containers are Go packages** inside one `mini-gateway` process, except the simulator harness and the end-to-end suite, which are test code in the same repository.
2. **Neighbour elements.** `c4-sdk` 0.1.0 allows a container in another container's component view only if that element is tagged `external`, and tags belong to the element rather than the view. Each container therefore has a twin with the id prefix `n-`, tagged `external` and `neighbour`, that appears only in component views. In the page tables, the twin displays the real container's description.
3. **Deployment instances** are instances of the container "Shell and binary", which stands for the whole process.
4. **Terminal (Unicode) renderings** of these diagrams are not included in this first set; see the open item in STATE.md.

## Validation

| Check | Tool | Result for this set |
| --- | --- | --- |
| `model.json` decodes | `decodeArchitecture` from `c4-sdk@0.1.0` (`c4-sdk/core`) | ok, 0 errors, 0 warnings |
| Every diagram builds a scene | `buildScene` from `c4-sdk/core` | 12 of 12 |
| Pictures and tables agree with the model | `gen.py` writes all three from the same tables and refuses both diagram edges whose ends are not in the diagram and relationships that no diagram uses | passes |
| Mermaid renders | Each built page was opened in a headless browser, and every diagram rendered | 12 of 12 |

To repeat these checks after a change:

```sh
python3 docs/c4/gen.py
# in a scratch folder outside /tmp, after `npm init -y && npm install c4-sdk@0.1.0`, with the script copied in
# (Node resolves the import from the script's own folder):
cp /root/Software/mini-gateway/agent_work/phase1/c4-check.mjs . && node c4-check.mjs /root/Software/mini-gateway/docs/c4/model.json
# the pictures: build the site, serve it locally, open every page in a headless browser
scripts/site.sh /tmp/c4-site && (cd /tmp/c4-site && python3 -m http.server 8765 --bind 127.0.0.1 >/dev/null 2>&1 &)
agent_work/phase1/check-mermaid.py http://127.0.0.1:8765
```
