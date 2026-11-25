# mini-gateway — chunk 3: Observability spine — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c03-obs-spine`.
Worktree: `/root/Software/mini-gateway-work/c03` — branch `work/c03-obs-spine`.
This chunk is built concurrently with chunks 4 (`internal/pgwire` sessions) and 5
(classifier, `internal/work`). Stay inside your files (below) so the three merge cleanly.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — the rules for every builder. They bind you.
2. `PLAN.md`: "Chunk 3 — Observability spine" (your scope); ALL of section 7 (7.1 the hard
   rule, 7.3 cost model, 7.4 label policy, 7.5 coverage, 7.7 the CSV schema version 1 —
   implement it exactly, 7.8 DuckDB); section 6.5 (asserting on flushed files; the row for
   chunk 3); decision D12.
3. What exists: `internal/obs` (the integer histogram and conservation helper from chunk
   1), `internal/sim/...` including `fakepg` and `workload` from chunk 2 with their
   `Snapshot()` types, the scenario type and the baseline scenarios; the notes
   `agent_work/chunks/c01-sim-kernel/NOTES.md`, `agent_work/chunks/c02-sim-world/NOTES.md`,
   and `docs/knowledge/`. The simulator API is frozen (tag `freeze-sim`): you may not change
   exported API of `internal/sim/...`; if you truly need a change, do the smallest one and
   list it under "Simulator change requests" at the top of NOTES.md.
4. `docs/knowledge/phase1-findings.md` — the DuckDB facts (empty text field reads as NULL;
   `read_csv` with explicit columns; `SET VARIABLE`), and `docs/knowledge/foundation-gates.md`.

## Purpose

The path from a component's numbers to something a person, a test or a scraper can read,
built before the components so each one plugs in on arrival. It also builds the testing
path: flush to CSV, assert on the files with SQL.

The hard rule (PLAN.md 7.1): **one raw export (`Snapshot()`) → the collector → adapters.**
No component knows an adapter exists. Each adapter is its own package under `internal/obs/`.

## Files you may touch

`internal/obs/...` (new: collector, registry, adapters as sub-packages, health), `test/analysis/`,
`scripts/csv-assert.sh`, `scripts/gen-docs.sh`, `docs/csv-schema.md` (generated),
`tools/lint-rules/*`, `Makefile` (only to wire a target), your chunk folder under
`agent_work/chunks/`, `docs/knowledge/`, and — only to plug the existing test-side
snapshots into the collector and to turn the flush on in the baseline scenarios — the
scenario harness files of chunk 2. Do NOT create `internal/pgwire`, `internal/work`,
`internal/proxy`.

## Deliverables

### `internal/obs` — the collector

- **A component registers a source**: a name from the fixed component list of section 7.7
  (`pool`, `admission`, `coalescer`, `retry`, `wire`, `classifier`, `engine`, `obs`,
  `fakepg`, `workload`, `loadgen`, `simnet`, plus `sim` if the kernel snapshot is exported —
  if you add a name, add it to the generated registry and say so in NOTES.md) and a function
  that maps its `Snapshot()` struct to **metric points**: (metric name, kind counter|gauge,
  labels tenant/outcome/reason/mode, integer value) and **histogram points** (metric name,
  labels, unit, the bucket rows). The mapping function lives next to the collector API in a
  form each component chunk can write for its own snapshot without importing any adapter.
  Provide the mappings for the snapshots that exist: `simnet`, `fakepg`, `workload` (and the
  kernel). Design the registration so the mapping of a component is declared once and every
  adapter gets it.
- **The metric registry**: every metric is declared (component, name, kind, labels it uses,
  unit, help text as a plain string constant) before it can be emitted; emitting an
  undeclared metric is a test failure, declaring one that is never emitted in any test is a
  test failure ("counters must move").
- **Instance snapshot assembly**: `Collect()` walks the registered sources in a fixed order
  and produces one instance snapshot value (instance id, build, config fingerprint, publish
  sequence number, the points, sorted by (component, metric, labels)); no time inside.
- **Publish interval and last-copy reads** (decision D12): `Publish()` is called by the
  owner (the engine later; the scenario harness now) at its interval; readers call
  `Last()` and get the last published copy without touching any component. The copy is
  immutable once published. In the simulator publish is an event on the virtual clock.
- **The adapter interface** every output implements — take an instance snapshot, produce
  output — and an **adapter conformance test** (a reusable test helper in a `obstest`-style
  package) any future adapter must pass: every declared metric appears, values equal the
  snapshot, label policy respected, deterministic output for equal input, no retained
  reference to the snapshot's memory, an error from the adapter is counted and never
  propagates into the collector.
- **The merge function across instances**: counters and gauges sum (state which gauges are
  not summable — for example a high-water mark merges by max — and encode that in the
  metric declaration), histograms merge by bucket, ratios are never stored. Laws tested.
- **The `obs.detail` gate**: histograms and the per-tenant breakdown are config-gated;
  off = one branch per event and no memory.
- **The three-state health type** (`ok`, `degraded`, `failed`) with a rule table: named
  checks, each with a state and a reason; overall = worst; `failed` only for "this instance
  cannot work at all". No rules of real components yet beyond a self-check of the
  collector; the table is what later chunks add rows to. JSON-serialisable body naming each
  check and its reason.
- The collector's own counters (component `obs`): publishes, export errors by adapter, CSV
  flushes, flush errors, rows dropped.

### Adapters (each its own package under `internal/obs/`)

- **`internal/obs/jsonstats`**: the full snapshot as JSON, stable field order, all tenants.
- **`internal/obs/csvflush`**: the CSV flush adapter, **schema version 1 exactly as PLAN.md
  section 7.7**: one run directory `<dir>/<run_id>/` with `metrics.csv`, `histograms.csv`,
  `run.csv`; the exact columns in the exact order; rows appended per flush, sorted by
  (`flush_seq`, `instance`, `component`, `metric`, labels); a registered metric with no
  tenant label is written in every flush also when 0; a tenant's rows appear from its first
  activity and stay; integers only; `t_ns` stamped by the adapter from a clock function it
  is given (virtual time in the simulator, monotonic in a real process — the snapshot
  carries no time); the final flush on close and `run.csv` written last; a test harness can
  ask for a flush at a chosen instant; `obs.csv.max_bytes` cap — at the cap or on any write
  error a real process stops writing, counts `csv_flush_errors` and `csv_rows_dropped`, and
  carries on, while a test run can ask for those events to be reported as a failure. In the
  simulator the flush is synchronous at a virtual instant (the file system is the one thing
  the simulator harness may touch; keep file I/O in this adapter package and out of the
  determinism-linted packages). In a real process the adapter will write from its own
  goroutine and never block the caller: build that mode now, with a bounded queue,
  drop-and-count, and a shell-style test under `-race` (add the package to
  `tools/lint-rules/race-packages.txt`).
- **`internal/obs/logline`**: one structured line per publish (key=value, fixed order,
  bounded length, the headline counters only).
- **`internal/obs/promtext`**: Prometheus text exposition: low-cardinality labels only
  (component, outcome, reason, mode and the **bounded tenant label** — configured tenants up
  to `obs.prom_tenant_max`, default 32, the rest folded into `other`), histograms re-bucketed
  to the fixed short bound list `obs.export_buckets` (about 16 bounds from 100 µs to 60 s;
  cumulative `le` buckets, `_sum` omitted or integer — state which and why), counters with
  `_total`, label sets for tenants that went quiet evicted after a configurable number of
  publishes. A small in-repo checker of the exposition grammar used by the tests.
- The **round-trip helper** the scenario harness calls for every run: read the final flush
  back with `encoding/csv` and require it to equal the final in-memory snapshot, value for
  value (every metric row and every histogram bucket row).

### Analysis side (PLAN.md 6.5 and 7.8)

- `test/analysis/views.sql`: the views `metrics`, `hist`, `runs`, `final` (last flush of
  each run) over a directory of runs given by a variable, with column types written out,
  empty label fields turned back into `''`.
- `scripts/csv-assert.sh <run-directory> <sql-file>...` (delete its not-built marker): runs
  each SQL file with the DuckDB CLI over the flushed files; a query returns the rows that
  VIOLATE the claim, so zero rows is a pass. Fails closed: on any returned row; on a schema
  version it does not know; on a missing `run.csv`; on an input with no rows for the metric
  under test (each SQL file declares the metric(s) it needs in a header line the runner
  checks — "counters must move"); when `duckdb` is not installed or is not the pinned
  version family (pin what vm-1 has: 1.5.x; record `duckdb --version` in the output). It
  never skips. With no SQL file argument it runs every `test/analysis/<scenario>/*.sql`
  against the matching run directories under a root (define the layout; the gate's G7 calls
  it this way over the run directories that the earlier gate stages produced — make the
  scenario tests write their run directories under `$RUN_DIR` when it is set).
- The first SQL assertion files, for the chunk 2 baseline, under
  `test/analysis/<scenario>/`: the fake Postgres peaked at 500 connections for 500 clients;
  1,000 identical reads were 1,000 database reads; workload sent = intended.
- `docs/csv-schema.md`: GENERATED from the code (column tables and every declared metric
  with component, kind, labels, unit) by `scripts/gen-docs.sh` (delete its marker;
  `--check` regenerates to a scratch folder and diffs), held by a test. A golden-header
  test pins the CSV columns.

## Tests (write these first)

- Golden output per adapter (testdata; `-update` path).
- Prometheus text parses under the exposition grammar checker; label bound holds with
  10,000 tenants (at most `prom_tenant_max` + 1 tenant label values); eviction works.
- Merge laws: merge is associative and commutative on counters and histograms; merge of N
  instance snapshots equals the snapshot of one instance that saw all events (property
  test with synthetic sources); high-water gauges merge by max.
- Snapshots contain no time and no map order: run-twice byte equality of every adapter's
  output for one seed.
- CSV: golden header; the same seed gives byte-identical files; the final flush read back
  equals the snapshot; zero-valued metrics are present as rows; a tenant's rows persist
  after it goes quiet; the size cap and an injected write error are counted and do not
  block (real-process mode) and are reported as a failure in test mode; a directory without
  `run.csv` is rejected by the assert runner.
- The cost-model benchmark (PLAN.md 7.3): record with detail off vs on; **zero allocations
  per recorded event** gate (`testing.AllocsPerRun`) for counter increment and histogram
  record; the cost of one publish for a stated number of tenants. Record the numbers in
  NOTES.md for the plan's section 7.3.
- Conformance test passes for every adapter.
- **End-to-end for this chunk**: the chunk 2 baseline scenarios (500 clients; burst past
  `max_connections`; 1,000 identical reads; open-loop overload) run with the collector
  publishing at an interval and every adapter attached; the CSV round trip holds for each;
  and the baseline's counts are asserted from the flushed files with DuckDB through
  `scripts/csv-assert.sh` (this part needs DuckDB, so it runs in the gate's G7 and in a
  test guarded by "duckdb on PATH" that FAILS, not skips, when `MG_REQUIRE_DUCKDB=1` — the
  gate sets it; CI does not install DuckDB).
- **Self-tests of the assertion runner** (shell-level tests invoked from a Go test or from
  the chunk's `gate/extra.sh`): a planted wrong number, an input with no rows for the
  metric, a missing `run.csv`, and an unknown schema version each make the runner exit
  non-zero with a message naming the reason.
- Planted-bug canary: a test-only switch in the CSV adapter (for example: drop the last
  histogram bucket row, or write a stale value) that the round-trip helper must catch.

## Observability obligations (landing gate)

This chunk is the machinery. It adds the collector's own counters, exported through every
adapter, with tests that they move (a failing adapter increments export errors; a capped
flush increments rows dropped).

## Claims this chunk makes (an adversary will try to break each)

1. One raw export: no adapter reads a component directly; no component or simulator
   package imports an adapter package (import lint rule added and passing).
2. Every adapter output for a given instance snapshot is deterministic, and equal inputs
   give byte-identical outputs; snapshots carry no time and no map order.
3. CSV schema version 1 is implemented exactly as PLAN.md section 7.7: columns, order,
   sorting, zero rows, tenant-row persistence, integers only, `run.csv` last.
4. The round trip is exact: the final flush read back equals the final snapshot for every
   metric and every histogram bucket.
5. Tier S flush files are byte-identical for the same seed.
6. The CSV adapter never blocks or slows the caller in real-process mode: at the size cap,
   on write errors, on a full queue it drops and counts.
7. The assertion runner fails closed in every listed case and never skips.
8. Prometheus output always parses, and never carries more than `prom_tenant_max` + 1
   tenant label values, whatever the tenants are named (hostile names included: quotes,
   newlines, backslashes, very long names, names equal to `other`).
9. Merge laws hold: sums, histogram merge, max for high-water marks.
10. Recording a counter or a histogram value allocates nothing; with `obs.detail` off no
    histogram memory is held.
11. The baseline counts read from the flushed files with DuckDB equal the simulator's own
    exact counts (500 / 500; 1,000 / 1,000; sent = intended).
12. A metric cannot be exported without being declared and documented in
    `docs/csv-schema.md` (the generated document's test fails otherwise).

## Steady-state row

What grows: per-tenant label sets and rows; the CSV files; the real-mode flush queue.
What bounds it: tenant-state eviction in promtext and the documented rule for CSV (rows
persist for the run — bounded by `obs.csv.max_bytes`); the queue cap. Gauges: tenants
tracked, CSV bytes written, queue depth, rows dropped.

## Size

About 1,400 lines of product code (may reach twice that). Tests at least as large.

## Finish

As the builder rules say: gate green at the build stage (`scripts/gate.sh c03-obs-spine`),
NOTES.md complete at `agent_work/chunks/c03-obs-spine/NOTES.md` (include the cost numbers
and the exact layout of run directories), clean tree, summary, and a final line containing
exactly AGENT-DONE.


## Notes from the orchestrator on your starting point (read before you start)

- Your branch starts from chunk 2 as repaired and attacked (`work/c02-sim-world`), which is
  in its second review and has not landed yet; chunk 2 gets a comments-only prose pass on
  another branch and your branch will be rebased onto it. Prefer NEW files; where you must
  edit an existing file keep the edit small and local, and list it in NOTES.md. The gate at
  the build stage compares comments with the merge base with `main`; chunk 2 files are
  comment-free in your tree and that passes.
- What chunk 2 actually built (names differ slightly from the wording above): the fake is
  `internal/sim/fakepg`; simulated clients are `internal/sim/workload`; the sans-I/O test
  client (framer, codec, session) is `internal/testclient`; scenarios, the driver interface
  and the fidelity runner are `internal/sim/world`; float-using generators are
  `internal/randdist`. Read `agent_work/chunks/c02-sim-world/NOTES.md` and
  `docs/knowledge/sim-world.md` first.
- Do NOT edit `agent_work/INDEX.md` (three chunks are building at once). Put the index rows
  you would add in a section "Index rows" at the end of your NOTES.md; the orchestrator adds
  them. The same goes for shared one-line lists: add your lines to
  `tools/lint-rules/det-packages.txt`, `race-packages.txt`, `imports.txt`,
  `prose-paths.txt` and `.gitignore` only by APPENDING lines at the end of the file.
- `scripts/gen-docs.sh` is shared by chunks 3 and 5: if it is still the skeleton, replace it
  with a small driver that runs every executable `scripts/gen-docs.d/*.sh` (passing
  `--check` through) and delete its marker; put YOUR generator in its own file there
  (`scripts/gen-docs.d/<your-chunk>-<name>.sh`). Both chunks will write the same driver;
  keep it to this exact shape so the merge is trivial:
  `for f in scripts/gen-docs.d/*.sh; do [ -x "$f" ] && "$f" "$@" || exit 1; done` after the
  usual header and `set -eu`.
- `internal/pgwire` is shared by chunks 4 and 5 (one Go package, two builders). To avoid
  name collisions: chunk 4 names its snapshot type `WireSnapshot` and chunk 5
  `ClassifierSnapshot`; chunk 5 exports only identifiers beginning with `Lex`, `Token`,
  `Classif`, `Reject`, `ErrorRow`, `ErrRow`, `Reply`, `SyncDiscard`, `Allow`; every
  unexported package-level identifier of chunk 5 begins with `lex`, `cls`, `rej`, `erow` or
  `rpl`; chunk 4 uses none of those prefixes. Test helper names follow the same rule.
  Neither chunk adds a `TestMain` or a `doc.go` to `internal/pgwire`.
