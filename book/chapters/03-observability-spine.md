# 3. Numbers without a framework: snapshots, a collector, exporters

Chapters 1 and 2 built a repeatable simulated world—an event loop, simulated TCP byte streams, a fake Postgres 17 server, and multi-tenant clients—and proved its direct-to-database baselines by reading internal Go structs inside the test process.

Before any proxy code lands, `mini-gateway` builds its **observability spine**: the path from a component's internal counters and histograms to outputs a person, a scraper, or an automated test can read. Building it up front means every later proxy component (wire sessions, classifier, pool, admission queues, coalescer, retry policy) plugs into one fixed contract on arrival. It also builds the project's external testing path: flushing snapshots to CSV files on disk and checking invariants over those files with SQL in DuckDB, without running a Prometheus server or OpenTelemetry Collector.

This chapter covers Chunk 3:

- the collector, metric registry, instance snapshots, cross-instance merge, three-state health table, and `obs.detail` recording gate in [`internal/obs`](../../internal/obs),
- the snapshot mappings for the four simulator components in [`internal/obs/simsrc`](../../internal/obs/simsrc),
- the four output adapters in [`internal/obs/jsonstats`](../../internal/obs/jsonstats), [`internal/obs/csvflush`](../../internal/obs/csvflush), [`internal/obs/logline`](../../internal/obs/logline), and [`internal/obs/promtext`](../../internal/obs/promtext),
- the reusable adapter conformance suite in [`internal/obs/obstest`](../../internal/obs/obstest),
- the metric catalog and generated schema document ([`internal/obs/catalog`](../../internal/obs/catalog), [`docs/csv-schema.md`](../../docs/csv-schema.md), [`scripts/gen-docs.sh`](../../scripts/gen-docs.sh)),
- the flushed scenario harness ([`internal/obs/simrun`](../../internal/obs/simrun)), and
- the DuckDB analysis layer ([`test/analysis/views.sql`](../../test/analysis/views.sql), [`test/analysis/guard.sql`](../../test/analysis/guard.sql), [`scripts/csv-assert.sh`](../../scripts/csv-assert.sh), and the first five baseline SQL assertions).

---

## Why build a collector instead of importing a telemetry SDK

Most Go services import the Prometheus client library or an OpenTelemetry SDK directly into every package that wants a counter. `mini-gateway` turns that down for four reasons ([section 7.1 of the plan](../../PLAN.md#71-three-layers-one-direction-of-dependency)):

1. **One raw export, one direction of dependency.** Every component keeps its counters, gauges, and integer histograms as plain integers and returns a plain value struct from `Snapshot()`. No component knows that CSV, JSON, Prometheus, DuckDB, or OpenTelemetry exists. Adding a new exporter—such as the hand-written OTLP/HTTP push exporter planned for Chunk 15—touches zero component files.
2. **Zero telemetry dependencies in `go.mod`.** The repository allows only one third-party module (`github.com/jackc/pgx/v5`, restricted to `pgproto3` in the wire layer, fake Postgres, and test client). Everything in `internal/obs/...` is Go standard library.
3. **Deterministic simulation.** A Prometheus client registry holds locks, maps, and floating-point counters. Inside `mini-gateway`'s simulator, the collector package [`internal/obs`](../../internal/obs) stays in [`tools/lint-rules/det-packages.txt`](../../tools/lint-rules/det-packages.txt): no floats, no locks, no wall clock, no goroutines, and no map-order output.
4. **Control state stays separate from telemetry.** Admission control and adaptive concurrency limits read queue depth and in-flight counts directly in place. Snapshots are copies for observers, never inputs to a hot-path decision.

```text
components        Snapshot()  — plain structs: integer counters, gauges, fixed-size histograms
    │             the one raw export; nothing else leaves a component
    ▼
collector         internal/obs: validates against the registry, sorts, keeps last copy
    │             fans out through one small interface (obs.Adapter)
    ▼
adapters          jsonstats │ logline │ promtext │ csvflush  (OTLP/HTTP push in Chunk 15)
                                                      │
                                                      ▼
                                         files  ←  DuckDB (scripts/csv-assert.sh)
```

This boundary is enforced mechanically, not just by convention. Two `deny` rules in [`tools/lint-rules/imports.txt`](../../tools/lint-rules/imports.txt) forbid any core, simulator, mapping, or collector package from importing an adapter, `simrun`, or `catalog`, and forbid any adapter from importing a component package. `importlint` checks those rules on every gate and CI run, and [`TestOneRawExportImportRule`](../../internal/obs/simrun/imports_test.go) mirrors them.

Three other design choices follow from that boundary:

- **CSV files plus DuckDB instead of a live Prometheus in tests.** Running Prometheus inside scenario tests would add scrape-timing jitter, cap per-tenant labels at 32, interpolate latency quantiles over 16 coarse buckets, and make cross-run comparisons awkward. Flushing schema-version-1 CSV files instead gives exact integer counters, unbounded per-tenant rows, full-resolution histogram buckets, byte-identical replay on Tier S (the deterministic simulator), and real SQL joins across runs and arms. DuckDB is invoked only as an external CLI tool by [`scripts/csv-assert.sh`](../../scripts/csv-assert.sh); Go code never imports or links it.
- **Mappings outside `internal/sim/`.** The simulator API was frozen at the end of Chunk 2 (`freeze-sim`), and `fakepg` and `workload` already import `internal/obs` for `Gauge` and `Histogram`. Putting the four simulator mappings (`sim`, `simnet`, `fakepg`, `workload`) in [`internal/obs/simsrc`](../../internal/obs/simsrc/simsrc.go) avoids circular imports and leaves `internal/sim/...` untouched. Future proxy components will place their `MetricDesc` list and `MapX` function next to their own `Snapshot()`, importing only `internal/obs`.
- **Single-threaded collector, copy-on-read.** Following [decision D12](../../PLAN.md#3-design-decisions-this-plan-makes) ("health and stats never touch the data path"), `Collector.Publish()` is driven on its interval by the owner—the scenario harness now, and the proxy engine in Chunk 6. Readers call `Collector.Last()` to get a deep copy of the last published snapshot without calling into any component. Because `internal/obs` holds no locks, the shell in Chunk 6 will hand that copy to the admin HTTP listener.

---

## Inside the collector (`internal/obs`)

### Declarations, the registry, and the emitter

Before a source can emit a number, it registers a fixed component name (one of the 13 names in `obs.Components()`, adding `sim` for the kernel to the 12 listed in the plan) and a slice of `obs.MetricDesc` declarations ([`internal/obs/metric.go`](../../internal/obs/metric.go)):

- **Kinds:** `KindCounter` (cumulative non-negative integer), `KindGauge` (sampled level), and `KindHistogram` (full-resolution bucketed distribution with unit `ns`, `bytes`, or `count`).
- **Fixed label dimensions:** a bitmask over four labels—`tenant`, `outcome`, `reason`, and `mode`. No free-form label names exist.
- **Merge rule:** `MergeSum` for counters and additive gauges, `MergeMax` for high-water marks (`HighWaterDesc`), and bucket-by-bucket addition for histograms. Ratios are never stored.
- **Automatic clamp counters:** `HistogramDesc` expands automatically into three declarations—the histogram itself plus `<name>_clamped_low` and `<name>_clamped_high` counters—so out-of-range samples always appear in `metrics.csv`.
- **Exposition clash refusal:** `Registry.Declare` rejects any two declarations in the same component whose generated Prometheus names would collide. For example, it rejects counter `x` (exported as `x_total`) beside gauge `x_total`, or histogram `wait` beside gauge `wait_count`, `wait_bucket`, or `wait_sum`.

During `Collector.Collect()`, the collector walks registered sources in fixed component-rank order and passes each mapping an `*obs.Emitter` ([`internal/obs/collector.go`](../../internal/obs/collector.go)). Every `Counter`, `Gauge`, and `Histogram` call is checked against the registry. Undeclared names (`ErrUndeclared`), wrong kinds (`ErrWrongKind`), undeclared labels (`ErrBadLabel`), negative counters (`ErrNegative`), and duplicate keys (`ErrDuplicate`) are recorded as emit errors, capped at 16 per source.

After each source finishes, `requireUnlabelled` enforces the schema's zero-row rule: every declared counter or gauge without a `tenant` label must emit at least one point on every collect (`ErrMissing`). The only exception is metrics explicitly marked `WithOpenLabels()`—used solely for `obs.export_errors` and `obs.adapter_failing`, whose `reason` label takes attached adapter names. That rule guarantees "nothing was shed" appears as an explicit `0` row rather than an absent row.

### Publish isolation and collector self-metrics

`Collector.Publish()` captures a sorted, timeless `obs.InstanceSnapshot`, saves it for `Last()`, and calls `Export(snap)` on each attached `obs.Adapter`:

```go
func (c *Collector) Publish() (InstanceSnapshot, error) {
	snap, err := c.Collect()
	c.lastEmit = err
	if err != nil {
		c.emitErrors++
	}
	c.seq++
	c.last = snap
	c.published = true
	for i := range c.adapters {
		slot := &c.adapters[i]
		if xerr := slot.a.Export(snap); xerr != nil {
			slot.errors++
			slot.failed = true
		} else {
			slot.failed = false
		}
	}
	return snap.Clone(), err
}
```

Adapter errors never propagate out of `Publish()`. Instead, each failure increments `obs.export_errors{reason=<adapter>}` and sets the gauge `obs.adapter_failing{reason=<adapter>}` to `1` until that adapter succeeds again. The collector publishes 13 self-metrics under component `obs` ([`internal/obs/self.go`](../../internal/obs/self.go)). It pulls CSV and Prometheus internal stats through two narrow optional interfaces (`obs.FlushReporter` and `obs.TenantReporter`) so `internal/obs` never imports any adapter package. Because `emitSelf` runs during `Collect()` before publish `N`'s adapters run, snapshot `N` carries the adapters' stats from before publish `N`—for example, `obs.csv_flushes` in the final snapshot is `publishes - 1`.

### The `obs.detail` gate, fleet merge, and three-state health

- **`obs.detail` gate ([`internal/obs/detail.go`](../../internal/obs/detail.go)):** On the recording side, `obs.DetailHistogram` holds a `*Histogram` pointer. When `detail` is false, that pointer is `nil`, occupying 0 bytes and reducing `Record(v)` to a single nil check. On the collection side, `Emitter` drops histograms and their clamp counters when `Config.Detail` is false, while still running declaration checks so invalid calls fail. In that same detail-off case it also clears `Labels.Tenant` so `combinePoints` folds per-tenant scalars into the empty tenant using each metric's merge rule.
- **Cross-instance merge ([`internal/obs/merge.go`](../../internal/obs/merge.go)):** `obs.Merge(reg, instance, snaps...)` combines snapshots from multiple proxy instances. `Seq` takes the maximum, differing `Build` or `ConfigFingerprint` values become `"mixed"`, counters and additive gauges sum, high-water marks take `max`, and histograms merge bucket by bucket via `MergeBuckets`. When `reg` is non-empty, every point and histogram—including singletons—is checked against `reg` first (`ErrUndeclared`, `ErrMergeKind`). Signed 64-bit sums check for overflow and return `ErrMergeOverflow` rather than wrapping silently.
- **Three-state health ([`internal/obs/health.go`](../../internal/obs/health.go)):** `obs.HealthTable` evaluates named rules (`Rule{Name, Check(InstanceSnapshot) (State, reason)}`) and takes the worst verdict across `StateOK`, `StateDegraded`, and `StateFailed`. Only `obs.CollectorRule()` exists in Chunk 3. It reports `degraded` before the first publish, or while the latest publish had emit errors (`obs.emit_failing`) or failing adapters (`obs.adapter_failing`), and recovers to `ok` once they clear. It never reports `failed`, because telemetry trouble does not stop the proxy serving. `Collector.Health(table)` evaluates `selfView()`—the last snapshot with `obs` points refreshed from live collector state—so health reflects publish `N` immediately with no one-publish lag. `Health.AppendJSON` hand-escapes strings to match `encoding/json` byte-for-byte without importing `encoding/json` into the determinism-linted `internal/obs` package.

---

## The four adapters

Every exporter lives in its own package under `internal/obs/` and implements `obs.Adapter`:

| Package | Name | Fidelity | Purpose and bounds |
| --- | --- | --- | --- |
| [`internal/obs/jsonstats`](../../internal/obs/jsonstats) | `json` | Exact | Full indented JSON snapshot in fixed struct-field order, every tenant and every full-resolution bucket. |
| [`internal/obs/logline`](../../internal/obs/logline) | `log` | Headline | One `obs.publish key=value ...` line per publish with metadata and untenanted `Headline` metrics only. Values cap at 32 bytes; the total line caps at 1,024 bytes (`truncated=1`). |
| [`internal/obs/promtext`](../../internal/obs/promtext) | `prom` | Folded | Prometheus text exposition (`mg_<component>_<metric>[_total]`). Caps tenant labels at `TenantMax` (default 32, max 1,024), folds hostile or excess tenants into `other`, and evicts quiet tenants after `EvictAfter` publishes (default 60). Re-buckets histograms to 16 cumulative bounds (`100 µs`–`60 s` for `ns`, powers of 4 for `bytes`/`count`), omits `_sum`, and includes an in-repo grammar checker (`Check`) and `Decode`. |
| [`internal/obs/csvflush`](../../internal/obs/csvflush) | `csv` | Exact | Schema version 1 CSV files on disk (`metrics.csv`, `histograms.csv`, `run.csv`) with unbounded tenants and full-resolution buckets. Sync mode for the simulator; non-blocking async goroutine with bounded queue (`QueueCap`, default 64, max 4,096) for real processes. `obs.csv.max_bytes` cap (default 1 GiB) covers headers, data, and `run.csv`. |

Two details in `csvflush` and `promtext` are worth calling out:

1. **CSV durability and fail-closed shutdown (`csvflush/writer.go`, `csvflush/reader.go`).** Each run writes into `<dir>/<run_id>/`, and `New` removes any leftover CSV files from a prior run with the same ID. Once a row key has been written, `persist` keeps emitting it on later flushes with its last value (up to `MaxTrackedRows = 1,048,576`) so a tenant's rows stay visible after it goes quiet. If the writer hits `MaxBytes` (accounting for pending headers before opening files) or any write error, it stops for good, increments `csv_flush_errors` and `csv_rows_dropped`, and **withholds `run.csv`**. Because `run.csv` is written last at `Close()`, any interrupted or capped run is visibly incomplete and fails `csvflush.Read` and `scripts/csv-assert.sh`.

   In async mode, a full queue drops and counts immediately without blocking `Publish()`. `Close()` drains the queue and re-writes the last snapshot if it was dropped, without ever decrementing drop counters. On read-back, `csvflush.RoundTrip` doubles `\r\n` before handing bytes to Go's `encoding/csv` (which otherwise strips `\r` before `\n` inside quoted fields, while `Writer` always terminates records with `\n`). It also checks strict row ordering and uniqueness, and accepts an extra tenant row in the final flush only if the immediately preceding flush held the exact same row and value.

2. **Prometheus tenant slots and `_sum` (`promtext/promtext.go`, `promtext/check.go`).** Tenant names that are empty, equal to `"other"`, longer than 64 bytes, or invalid UTF-8 are always folded into `other`. Active tenants—those whose counter or histogram totals changed since the previous publish, restricted to `Config.Tenants` if configured—claim slots first-come up to `TenantMax`. A slot holder quiet for `EvictAfter` publishes loses its slot and folds back into `other`. Histograms re-bucket each full-resolution bucket to the first export bound `>= b.Hi` (at most one bound high, within the histogram's `< 0.79%` width). `_sum` is omitted deliberately because the snapshot carries no exact sum and a midpoint estimate would fabricate numbers. `promtext.Check` validates the exposition grammar, including integer-only sample values, required `# TYPE`, counter `_total` suffix and non-negativity, and histogram cumulative monotonicity and non-negativity.

---

## Real CSV output and DuckDB queries

[`internal/obs/simrun`](../../internal/obs/simrun/simrun.go) wraps `world.SimDriver` from outside `internal/sim/`, registers all four simulator sources, and attaches all four adapters. It publishes on the virtual clock every 100 ms using a `MarkBackground` timer (so quiesce checks stay clean), plus at `t = 0` and once more after `Stop`.

Running `TestScenarioFlush` with `RUN_DIR` set writes the five direct baselines for seed 1 and then runs [`scripts/csv-assert.sh`](../../scripts/csv-assert.sh) over them:

```sh
$ RUN_DIR=/tmp/c03-runs go test -count=1 -run '^TestScenarioFlush$' ./internal/obs/simrun -seed=1
ok  	github.com/nickstrad/mini-gateway/internal/obs/simrun	3.482s

$ scripts/csv-assert.sh /tmp/c03-runs
NOTE: v1.5.5 (Variegata) d8cdaa33fd; 5 run directories; 4 scenarios with SQL files
ok: test/analysis/direct-1000-identical-reads/database-reads.sql over 1 runs
ok: test/analysis/direct-500-connections/peak-connections.sql over 1 runs
ok: test/analysis/direct-500-connections/sent-equals-intended.sql over 1 runs
ok: test/analysis/direct-open-loop-overload/sent-equals-intended.sql over 1 runs
ok: test/analysis/direct-reconnect-burst/refusals.sql over 1 runs
csv-assert: 5 SQL files run, 0 failures; v1.5.5 (Variegata) d8cdaa33fd
```

Here are real excerpts from `/tmp/c03-runs/direct-500-connections/1/base/`:

```csv
# run.csv
schema_version,run_id,scenario,tier,seed,arm,instances,build,config_fingerprint,flushes,duration_ns
1,direct-500-connections/1/base,direct-500-connections,S,1,base,1,sim,direct-500-connections-d2000000000,22,2000000000

# metrics.csv (header, first flush zero rows, and selected rows from the final flush 21)
schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value
1,direct-500-connections/1/base,0,0,sim-1,fakepg,bytes_in,counter,,,,,0
1,direct-500-connections/1/base,0,0,sim-1,fakepg,bytes_out,counter,,,,,0
...
1,direct-500-connections/1/base,21,2000000000,sim-1,fakepg,statements_started,counter,,,,,2500
1,direct-500-connections/1/base,21,2000000000,sim-1,obs,csv_flushes,counter,,,,,21
1,direct-500-connections/1/base,21,2000000000,sim-1,obs,publishes,counter,,,,,22
1,direct-500-connections/1/base,21,2000000000,sim-1,workload,clients,gauge,,,,,500
1,direct-500-connections/1/base,21,2000000000,sim-1,workload,intended,counter,app,,,,2500
1,direct-500-connections/1/base,21,2000000000,sim-1,workload,requests,counter,app,ok,,,2500
1,direct-500-connections/1/base,21,2000000000,sim-1,workload,sent,counter,app,,,,2500

# histograms.csv (header and first five non-empty bucket rows)
schema_version,run_id,flush_seq,t_ns,instance,component,metric,tenant,outcome,reason,mode,unit,bucket_lo,bucket_hi,count
1,direct-500-connections/1/base,1,100000000,sim-1,workload,latency,app,,,,ns,2211840,2228224,1
1,direct-500-connections/1/base,1,100000000,sim-1,workload,latency,app,,,,ns,2228224,2244608,3
1,direct-500-connections/1/base,1,100000000,sim-1,workload,latency,app,,,,ns,2244608,2260992,8
1,direct-500-connections/1/base,1,100000000,sim-1,workload,latency,app,,,,ns,2260992,2277376,18
1,direct-500-connections/1/base,1,100000000,sim-1,workload,latency,app,,,,ns,2277376,2293760,30
```

Notice `obs,csv_flushes` is `21` beside `obs,publishes` `22` (the collector reads adapter stats before publish 22 writes flush 21), and `bucket_hi` is exclusive (`b.Hi + 1`).

[`test/analysis/views.sql`](../../test/analysis/views.sql) defines five views—`metrics`, `hist`, `runs`, `final`, and `final_hist`—over file lists supplied via `SET VARIABLE`. It pins explicit column types and uses `coalesce(..., '')` to turn DuckDB's NULL empty text fields back into `''`. Loading those views in DuckDB 1.5.5 over the four asserted baselines from that run lets one query summarize every baseline side by side:

```sql
SELECT r.scenario,
       r.flushes,
       max(f.value) FILTER (WHERE f.component = 'fakepg' AND f.metric = 'sessions_peak') AS sessions_peak,
       sum(f.value) FILTER (WHERE f.component = 'fakepg' AND f.metric = 'statements' AND f.mode = 'read') AS db_reads,
       sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'intended') AS intended,
       sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'requests' AND f.outcome = 'ok') AS ok
FROM runs r
JOIN final f ON f.run_id = r.run_id
GROUP BY r.scenario, r.flushes
ORDER BY r.scenario;
```

```text
┌─────────────────────────────┬─────────┬───────────────┬──────────┬──────────┬──────┐
│          scenario           │ flushes │ sessions_peak │ db_reads │ intended │  ok  │
├─────────────────────────────┼─────────┼───────────────┼──────────┼──────────┼──────┤
│ direct-1000-identical-reads │ 12      │ 1000          │ 1000     │ 1000     │ 1000 │
│ direct-500-connections      │ 22      │ 500           │ 2500     │ 2500     │ 2500 │
│ direct-open-loop-overload   │ 52      │ 40            │ 800      │ 800      │ 800  │
│ direct-reconnect-burst      │ 22      │ 100           │ 2830     │ 3000     │ 2830 │
└─────────────────────────────┴─────────┴───────────────┴──────────┴──────────┴──────┘
```

And querying `final_hist` over `direct-open-loop-overload/1/base` computes exact bucket-upper-bound p50 and p99 latencies directly from `histograms.csv`, exposing coordinated omission in SQL:

```sql
WITH c AS (
  SELECT metric, bucket_hi,
         sum(count) OVER (PARTITION BY metric ORDER BY bucket_hi) AS cum,
         sum(count) OVER (PARTITION BY metric) AS total
  FROM final_hist
  WHERE component = 'workload' AND metric IN ('latency', 'latency_from_send')
)
SELECT metric,
       min(bucket_hi) FILTER (WHERE 100 * cum >= 50 * total) AS p50_upper_ns,
       min(bucket_hi) FILTER (WHERE 100 * cum >= 99 * total) AS p99_upper_ns,
       max(total) AS samples
FROM c
GROUP BY metric
ORDER BY metric;
```

```text
┌───────────────────┬──────────────┬──────────────┬─────────┐
│      metric       │ p50_upper_ns │ p99_upper_ns │ samples │
├───────────────────┼──────────────┼──────────────┼─────────┤
│ latency           │ 507510784    │ 998244352    │ 800     │
│ latency_from_send │ 100139008    │ 100139008    │ 800     │
└───────────────────┴──────────────┴──────────────┴─────────┘
```

Measured from intended arrival time (`latency`), p50 is 507.5 ms and p99 is 998.2 ms; measured from wire send (`latency_from_send`), both appear to be 100.1 ms.

### Fail-closed SQL assertion runner

[`scripts/csv-assert.sh`](../../scripts/csv-assert.sh) pins DuckDB `1.5.x` (`v1.5.5` on vm-1), never skips, and fails closed on every integrity error:

1. Checks `run.csv`, `metrics.csv`, and `histograms.csv` exist in every run directory with exact schema-version-1 headers.
2. Runs [`test/analysis/guard.sql`](../../test/analysis/guard.sql) in DuckDB over all runs before any assertion query. The guard rejects any `schema_version` other than `1`, any empty or NULL required column (avoiding SQL three-valued logic where `NULL <> 7` would silently pass), duplicate `run_id`s across directories, runs with zero metric rows, and rows whose `run_id` has no `run.csv`. Because DuckDB parses RFC 4180 CSV properly, quoted newlines inside tenant names pass cleanly.
3. Requires every SQL file to declare `-- requires: component.metric ...` and checks that **every** run has rows for each required metric in `final` or `final_hist` ("counters must move").
4. Runs each SQL assertion file, where returned rows are violations and zero rows is a pass.

---

## Measured cost model (section 7.3)

All hot-path recording operations are gated by [`TestRecordingAllocatesNothing`](../../internal/obs/cost_test.go) using `testing.AllocsPerRun(1000, ...)`—requiring strictly 0 allocations for `Counter.Inc`, `Gauge.Add`, `Histogram.Record`, and `DetailHistogram.Record` (detail on and off).

Measured on vm-1 (4 vCPU shared DigitalOcean `DO-Regular`, Go 1.26.8; shared-host timings vary roughly ±30% run to run):

| Operation | Builder 2000× run (`NOTES.md`) | Prose 500× run | Memory / allocations |
| --- | --- | --- | --- |
| `obs.Counter.Inc` | ~2 ns | 2.36 ns | 0 allocs (hard gate) |
| `obs.Histogram.Record` (3,302 buckets) | 20–35 ns | 74.8 ns | 0 allocs (hard gate) |
| `obs.DetailHistogram.Record` (`detail=false`) | 3–5 ns | 4.02 ns | 0 bytes held, 0 allocs |
| `obs.DetailHistogram.Record` (`detail=true`) | ~20 ns | 74.0 ns | 26,416 bytes held, 0 allocs |
| `Collector.Publish`, 1 tenant (`detail=false` / `true`) | 30–60 µs / 30–75 µs | 45.5 µs / 32.6 µs | 10.7 KB (14 allocs) / 12.1 KB (22 allocs) |
| `Collector.Publish`, 32 tenants (`detail=false` / `true`) | 0.24–0.34 ms / 0.5–1.5 ms | 130.5 µs / 510.3 µs | 41.4 KB (18 allocs) / 171.9 KB (187 allocs) |
| `Collector.Publish`, 1,000 tenants (`detail=false` / `true`) | 3.6–8.7 ms / 15–30 ms | 2.96 ms / 17.15 ms | 1.33 MB (25 allocs) / 4.54 MB (5,040 allocs) |
| `csvflush` fixture (79 points + 13 histograms), sync / async caller | ~0.33 ms / ~0.03 ms | 287.7 µs / 16.6 µs | 49.5 KB (121 allocs) / 14.3 KB (20 allocs) |

With `obs.detail` enabled, publish time scales linearly with `tenants × histograms × buckets` because each histogram snapshots its 3,302-bucket array.

---

## Testing, generated documentation, and the gate

Chunk 3 adds 4,170 lines of non-test Go (before prose comments; about 3,100 excluding `obstest` conformance/golden helpers, the Prometheus grammar checker/decoder, `simrun`, and `catalog`) and 3,135 lines of test Go across 75 test and benchmark functions:

- **Reusable adapter conformance (`obstest.Conformance`):** Every adapter passes five checks—`values` (at `Exact`, `Folded`, or `Headline` fidelity), `labels`, `deterministic`, `no-retained-reference` (scribbling the input snapshot after `Export` must not alter output), and `error-counted-not-propagated`. The fixture uses 12 hostile tenant names: quotes, newlines, backslashes, commas, leading spaces, a 160-byte string, Unicode, and `"other"`. `TestConformanceCatchesBrokenAdapters` runs all five checks against one compliant toy adapter and six broken ones to prove each check catches its defect.
- **Golden files and grammar checks:** All four adapters pin golden outputs under `testdata/` (`-update` to refresh). `promtext` tests 10,000 hostile tenants across `TenantMax` 0 (default 32), 1, 32, and 100 over three publishes while preserving folded totals, tests quiet-tenant eviction, and rejects 20 malformed exposition inputs.
- **Merge laws:** 300 seeded runs over 1–5 partitioned instances prove `Merge` equals a single instance that saw every event, is associative and commutative, merges high-water marks by `max`, and refuses undeclared metrics, kind/unit mismatches, and `int64` overflow.
- **Counters must move and generated docs:** [`TestMappingsMoveEveryMetric`](../../internal/obs/simsrc/simsrc_test.go) fills every field of `sim`, `simnet`, `fakepg`, and `workload` snapshots with distinct non-zero values via reflection; it fails if any declared metric fails to move or any field is silently dropped. [`TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented`](../../internal/obs/simrun/registry_test.go) verifies that all 115 declared metrics are emitted across the five baseline runs and documented in [`docs/csv-schema.md`](../../docs/csv-schema.md) (53 stay 0 in the clean baselines: fault, cancel, drop, and in-flight gauges). [`scripts/gen-docs.sh --check`](../../scripts/gen-docs.sh) (Gate G8) regenerates `docs/csv-schema.md` through `internal/obs/catalog`'s package test—avoiding any `main` package inside `internal/obs/...`—and diffs it against the committed file.
- **Planted-bug canaries and fail-closed self-tests:** Test-only switches `PlantDropLastBucket` and `PlantStaleValue` prove `csvflush.RoundTrip` catches dropped bucket rows and stale metric values on both the fixture and `direct-open-loop-overload`. `TestCSVAssertFailsClosed` and `TestCSVAssertChecksEveryRunAndEveryField` prove `scripts/csv-assert.sh` fails closed on planted wrong numbers, missing metrics, empty runs in a multi-run root, duplicate `run_id`s, empty numeric cells, missing `run.csv`, unknown schema versions/headers, bad SQL, missing roots, and wrong or absent DuckDB binaries.
- **Gate checks:** The chunk's gate extra ([`agent_work/chunks/c03-obs-spine/gate/extra.sh`](../../agent_work/chunks/c03-obs-spine/gate/extra.sh)) runs DuckDB e2e and self-tests with `MG_REQUIRE_DUCKDB=1` (skip = failure) alongside both planted-bug canaries. It also runs all five baselines in two separate OS processes to confirm `diff -r` across all 15 CSV files is empty, and runs async `csvflush` tests ten times under `-race`. Across the full gate (`G0`–`G10`), all 22 packages, 3,410 sweep seed runs, 7 fuzz targets (20 s each), tier R against Postgres 17.11, G7 DuckDB assertions over 10 sweep run directories, G8 doc generation, and G9 site build passed.

---

## Stated limits

- **No locks inside `internal/obs`.** `Collector.Last()` and `Collector.Health()` are safe on the owner's goroutine only; the process shell in Chunk 6 hands copies across to the admin listener.
- **Prometheus re-bucketing and counter resets.** A full-resolution bucket that straddles a coarse export bound lands one bound high (within `< 0.79%`). `_sum` is omitted. When a previously folded tenant claims an evicted Prometheus slot out of `other`, `other` decreases (which Prometheus treats as a counter reset); JSON and CSV carry every tenant and do not fold.
- **Fleet high-water marks.** Merging `MergeMax` peaks across instances gives a lower bound on the fleet's simultaneous peak (instances may peak at different instants); summing them would give an upper bound.
- **Detail-off duplicate masking and overflow.** With `obs.detail` off, duplicate untenanted points from a buggy mapping are combined rather than flagged because the fold cannot distinguish them from collapsed tenants. That in-collector fold also adds without an overflow check, unlike `obs.Merge`, which returns `ErrMergeOverflow`.
- **CSV row persistence and round-trip scope.** Persisted rows repeat their last value—including gauges—after a source stops emitting them, capped at 1,048,576 tracked keys. `RoundTrip` checks the final flush (verifying persisted rows against the flush before it) and relies on byte-identity tests and SQL assertions for earlier flushes. Its CRLF preservation assumes `\n`-terminated records as written by `csvflush.Writer`.
- **Health evaluation on `Last()`.** `Collector.Health` evaluates a live `selfView()` with zero lag; a rule applied manually to `Collector.Last()` sees `obs` self-metrics from before that publish.
- **Scenario discovery and panics.** `scripts/csv-assert.sh` matches scenarios in root mode from the directory path (`<root>/<scenario>/...`), not from `run.csv`. Neither `Collector.Publish` nor any adapter recovers from a panic inside another adapter.
- **Not built yet.** Only the collector's own health rule exists so far (component rules arrive with each proxy component), and the OTLP/HTTP push exporter lands in Chunk 15.
