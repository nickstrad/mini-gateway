# Observability spine: collector, adapters, CSV flush, DuckDB (chunk 3, 2026-11-25)

Facts a later builder needs before plugging a component into `internal/obs` or adding an
adapter or a SQL assertion. Full detail: `agent_work/chunks/c03-obs-spine/NOTES.md`; the
schema and every metric: `docs/csv-schema.md` (generated).

## Plugging a component in

- Declare the metrics once, next to the mapping, as a `[]obs.MetricDesc`
  (`obs.CounterDesc`, `obs.GaugeDesc`, `obs.HighWaterDesc` (merges by max), `obs.HistogramDesc`;
  `.WithUnit`, `.AsHeadline` for the log line). Help is one plain line, no `|` or `\`.
- Write `func MapX(s X.Snapshot, e *obs.Emitter)` and register
  `obs.NewSource(obs.ComponentX, metrics, x.Snapshot, MapX)` with the collector
  (`c.Register`). The component package itself does not change and imports nothing new.
  Examples: `internal/obs/simsrc` (sim kernel, simnet, fakepg, workload).
- The emitter refuses (and `Collect`/`Publish` return an error naming it): an undeclared
  metric, a wrong kind, a label the declaration does not have, a duplicate point, a negative
  counter, and a declared counter or gauge WITHOUT a tenant label that has no point at all
  (an unlabelled one must be emitted at every collect, also when 0; one labelled by
  outcome/reason/mode needs at least one point). Emit every known label value of a labelled
  metric (all fault kinds, all reasons), so the zero rows exist. Only when the label values
  are open-ended (one per attached adapter, say) declare it `.WithOpenLabels()`: it may then
  have no point.
- `Registry.Declare` refuses two metrics of one component that would export the same
  Prometheus series name: counter `x` (`x_total`) beside `x_total`; histogram `h` beside
  `h_bucket`, `h_count`, `h_sum`.
- Add the declarations to `internal/obs/catalog.Registry()` and run `scripts/gen-docs.sh`;
  `internal/obs/catalog` fails while `docs/csv-schema.md` is stale. `simrun`'s
  `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` fails for an exported metric
  missing from the catalog. Copy `simsrc_test.go: TestMappingsMoveEveryMetric` (reflection
  fills every snapshot field; every field must reach an exported value and every metric must
  move).
- Tenant: use `obs.Labels{Tenant: name}`. With `obs.Config.Detail` false the emitter folds
  every tenant into the empty tenant (sum, or max for high-water marks) and drops histograms;
  call `e.Histogram` only when `e.Detail()` so the snapshot of the histogram is not built.
  Recording side: `obs.NewDetailHistogram(detail, lo, hi)` holds no memory when off.
- Component names are fixed: `obs.Components()`. A new name is a change to `metric.go` and
  to PLAN.md section 7.7.

## Publishing

- `c.Publish()` assembles one `obs.InstanceSnapshot` (sorted by component, metric, labels;
  no time), hands it to every attached adapter, counts adapter errors in
  `obs.export_errors{reason=<adapter>}` and never returns them. `c.Last()` returns a copy of
  the last published snapshot without touching any component. `obs` is in the determinism
  list: no locks, so a reader on another goroutine (the admin listener, chunk 6) needs the
  shell to hand the copy over.
- The `obs` component's own counters are filled from adapters that implement
  `obs.FlushReporter` (CSV) and `obs.TenantReporter` (Prometheus); the values in snapshot N
  describe the adapters' state before publish N.
- Health: `c.Health(table)` evaluates the rules over the last snapshot with the `obs` points
  refreshed to now (`adapter_failing`, `emit_failing` of the latest publish), so it shows a
  failure of publish N right after it and recovers when the next publish succeeds. A rule
  applied to `c.Last()` itself sees the `obs` points one publish late.
- `obs.Merge(reg, instance, snaps...)` checks every point and histogram against `reg` when
  it is non-empty (undeclared, wrong kind or unit is an error), and returns
  `obs.ErrMergeOverflow` rather than wrapping an int64 sum. `reg` may be nil (all sums).
- In the simulator, `internal/obs/simrun.Scenario(sc, seed, opts)` runs a `world.Scenario`
  with every adapter attached and a background timer publishing every `Interval` (default
  100 ms) of virtual time, plus `FlushAt` instants, plus a final publish at the end; then
  closes the CSV writer. `run.Check()` is the round trip + adapter sanity check.

## CSV flush

- Layout: `<dir>/<run_id>/{metrics,histograms,run}.csv`; simulator run id
  `<scenario>/<seed>/<arm>` (arm `base`). Scenario tests write under `$RUN_DIR` when set
  (`simrun.Dir`), so the gate's sweep directory holds `<sweep>/<scenario>/<seed>/base/`.
- `run.csv` is written last, at `Close`, after the data files are closed. At the size cap or
  on a write error the writer stops for good, counts `csv_flush_errors`/`csv_rows_dropped`,
  and never writes `run.csv` (the run then fails every assertion). `Strict` (simulator, tests)
  makes `Close` return the failure; a real process keeps going.
- `Async` mode: one writer goroutine, queue `QueueCap` (default 64, max 4,096); a full queue
  drops the flush and counts it; `Close` drains and re-writes the last snapshot if it was the
  one dropped (the final flush always lands unless the writer stopped).
- A row key once written is written in every later flush, with its last value, even if the
  source stopped emitting it (tenant persistence). At most 1,048,576 keys are tracked.
- `csvflush.RoundTrip(runDir, finalSnapshots...)` reads the files back with `encoding/csv` and
  requires the last flush to equal the snapshot value for value, its rows strictly sorted (no
  duplicate); a row with a tenant that is only in the file passes only when the flush before
  has the same row with the same value (a persisted row). `encoding/csv` drops a `\r` before a
  `\n` even inside quotes, so the reader doubles `\r\n` first (the writer ends lines with `\n`).
- `MaxBytes` covers everything on disk, headers and `run.csv` included.
- Test-only plants (`export_test.go`): `PlantDropLastBucket`, `PlantStaleValue`,
  `SetCreate` (inject write errors), `SetGate` (block the writer goroutine).

## Prometheus text

- Family `mg_<component>_<metric>`, counters `_total`; labels `component` plus the
  non-empty ones of tenant, outcome, reason, mode. Histograms re-bucketed to 16 bounds
  (100 µs … 60 s for ns; powers of 4 for bytes/count), cumulative `le`, `+Inf`, `_count`, no
  `_sum` (the snapshot has no exact sum). A full-resolution bucket goes to the first bound at
  or above its upper end, so a value can show one bound high (< 0.79%).
- Tenant label: at most `TenantMax` (default 32) tenants hold a label, the rest are summed
  into `other`. A slot goes to a tenant whose counters changed since the last publish (in the
  configured list, if one is given); names that are empty, `other`, longer than 64 bytes or
  not UTF-8 are always folded. A slot holder quiet for `EvictAfter` publishes (default 60)
  loses its slot and its values move into `other`, so `other` can go down when a folded
  tenant gets a slot (a counter reset to Prometheus).
- `promtext.Check(text)` is the in-repo exposition grammar checker; `promtext.Decode` turns
  text back into an `obs.InstanceSnapshot` for tests.

## DuckDB assertions

- `scripts/csv-assert.sh <run-dir> <sql>...` or `scripts/csv-assert.sh <root>...` (every
  `test/analysis/<scenario>/*.sql` over `<root>/<scenario>/*/*`). Pinned family 1.5.x (vm-1:
  1.5.5 at `~/.local/bin/duckdb`). A SQL file starts with `-- requires: comp.metric ...` and
  returns one text column `violation`, zero rows = pass; start it with the schema-version
  guard `SELECT ... FROM runs WHERE schema_version <> 1 UNION ALL ...`.
- Before any SQL file the runner runs `test/analysis/guard.sql` over all the runs: any
  `schema_version` other than 1, an empty field in a required column (DuckDB reads an
  empty number as NULL, and `value <> 7` is then not true, so the row would pass), duplicate run ids, a run without metric rows. Each
  `-- requires:` metric must have rows (metric or histogram) in the final flush of EVERY
  run. Do not check rows with line tools: quoted fields may hold newlines.
- Views (`test/analysis/views.sql`): `metrics`, `hist`, `runs`, `final`, `final_hist`; the
  runner sets `metrics_files`, `histograms_files`, `run_files` as lists of paths with
  `SET VARIABLE`. Label columns are `''`, never NULL.
- Go tests that need DuckDB call `requireDuckDB(t)`: skip when it is missing, FAIL when
  `MG_REQUIRE_DUCKDB=1` (the chunk's gate extra sets it).

## Import rule

`tools/lint-rules/imports.txt` (chunk 3 lines): no core, simulator, collector or mapping
package imports an adapter, `simrun` or `catalog`; adapters import no component (tests may
import `simrun`). Mirrored by `simrun/imports_test.go: TestOneRawExportImportRule`.
