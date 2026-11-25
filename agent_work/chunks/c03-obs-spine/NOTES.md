# Chunk 3 — Observability spine: builder notes

Built by Opus on 2026-11-25 on branch `work/c03-obs-spine` (worktree
`/root/Software/mini-gateway-work/c03`) from `/root/briefs/zgateway-p2-c03-obs-spine.md`.
Durable facts for later builders: `docs/knowledge/obs-spine.md`. Schema and metric registry:
`docs/csv-schema.md` (generated).

## Simulator change requests

None. No file under `internal/sim/...` was changed; the harness wraps `world.SimDriver` from
outside (`internal/obs/simrun`), using only exported API.

## What was built

| Deliverable | Where |
| --- | --- |
| Metric declarations, label sets, component list, registry | `internal/obs/metric.go` |
| Instance snapshot (points, histogram points, sort, lookup, clone, equality) | `internal/obs/instance.go` |
| Emitter, sources (`NewSource`), collector (`Register`, `Attach`, `Collect`, `Publish`, `Last`), adapter interface | `internal/obs/collector.go` |
| The collector's own counters (component `obs`) | `internal/obs/self.go` |
| Merge across instances | `internal/obs/merge.go` |
| Three-state health, rule table, collector self-check, JSON body | `internal/obs/health.go` |
| `obs.detail` gate on the recording side (`DetailHistogram`), `Counter` | `internal/obs/detail.go` |
| Mappings of the existing snapshots (kernel `sim`, `simnet`, `fakepg`, `workload`) | `internal/obs/simsrc/simsrc.go` |
| Adapter: JSON stats | `internal/obs/jsonstats/` |
| Adapter: CSV flush, schema version 1, round-trip reader | `internal/obs/csvflush/` (`schema.go`, `writer.go`, `reader.go`) |
| Adapter: one-line log | `internal/obs/logline/` |
| Adapter: Prometheus text, bounded tenant label, re-bucketing, eviction; grammar checker and decoder | `internal/obs/promtext/` (`promtext.go`, `check.go`) |
| Adapter conformance test helper, shared fixture, golden helper (`-update`) | `internal/obs/obstest/conformance.go`, `golden.go` |
| Scenario harness: a world scenario with the collector publishing on the virtual clock and every adapter attached | `internal/obs/simrun/simrun.go` |
| Metric catalog and the generator of `docs/csv-schema.md` (through the package test: `-update` rewrites the document, `-out <path>` writes it elsewhere; no main package, so `internal/obs/...` holds no process code) | `internal/obs/catalog/` |
| DuckDB views | `test/analysis/views.sql` |
| First SQL assertions | `test/analysis/direct-500-connections/{peak-connections,sent-equals-intended}.sql`, `test/analysis/direct-1000-identical-reads/database-reads.sql`, `test/analysis/direct-open-loop-overload/sent-equals-intended.sql`, `test/analysis/direct-reconnect-burst/refusals.sql` |
| Assertion runner | `scripts/csv-assert.sh` (marker `scripts/.not-built/csv-assert` deleted) |
| Generated documents driver + this chunk's generator | `scripts/gen-docs.sh` (marker `scripts/.not-built/gen-docs` deleted), `scripts/gen-docs.d/c03-csv-schema.sh` |
| Make target | `make csv-assert RUNS=<dir> [SQL=<files>]` |
| Gate extra (G10) | `agent_work/chunks/c03-obs-spine/gate/extra.sh` |
| Rules files (appended lines only) | `det-packages.txt` (+`internal/obs/simsrc`, `internal/obs/catalog`), `race-packages.txt` (+`./internal/obs/csvflush/...`), `imports.txt` (two `deny` lines, below) |

Existing files edited (small, local): `Makefile` (one target and its help line),
`scripts/csv-assert.sh` and `scripts/gen-docs.sh` (skeletons replaced), the three rules files
(appended). `prose-paths.txt` already denied `docs/csv-schema.md`; `.gitignore` unchanged.

## Exact layout of run directories

```
<obs.csv.dir or $RUN_DIR>/
  <scenario>/<seed>/<arm>/        run_id = "<scenario>/<seed>/<arm>"; arm is "base" here
    metrics.csv                   header + rows appended per flush
    histograms.csv                header + one row per non-empty bucket per flush
    run.csv                       header + one row, written last at Close
```

`simrun.Scenario` writes to `Options.Dir`; the scenario test `TestScenarioFlush` passes
`simrun.Dir(t.TempDir())`, i.e. `$RUN_DIR` when set. The gate's G4 sweep sets `RUN_DIR=<runs>/sweep`,
so G7 finds `<runs>/sweep/direct-*/{1,42}/base/` (the pinned seeds 1 and 42 of
`internal/obs/simrun/testdata/seeds.txt`). Run ids allow `[A-Za-z0-9._=-]` and `/`, at most 8
parts and 200 bytes, no empty, `.` or `..` part. `csvflush.New` deletes the three files of an
existing run directory with the same id (a rerun replaces it).

## Decisions the brief left open

1. **Where the mappings live.** `internal/obs` cannot import `fakepg`/`workload` (they import
   `obs` for `Gauge` and `Histogram`), and the simulator API is frozen, so the mappings of the
   existing snapshots are in `internal/obs/simsrc`, one `MapX(snapshot, *obs.Emitter)` and
   one `XMetrics()` per component. A later component writes the same two functions in its own
   package (or next to it); it imports only `internal/obs`.
2. **The kernel is exported** as component `sim` (13th name, added to `obs.Components()` and
   to the generated registry).
3. **Label use in the mappings.** simnet byte counters by direction use `mode` (`up`, `down`);
   close reasons and fault kinds use `reason`; fakepg statement kinds use `mode` (`read`,
   `const`, …); refusals and fault kinds use `reason`; workload outcomes use `outcome` on
   `workload.requests`; SQLSTATEs use `reason` on `workload.errors_by_code`. Workload tenants
   with the same name get `name#<index>` for the later ones.
4. **Peaks** (`obs.Gauge.Peak`) are exported as separate gauges `<name>_peak` declared
   `MergeMax`. Stated limit: the max over instances is a lower bound of the fleet's peak of a
   summed level (the instances may peak at different instants); the sum would be an upper
   bound. Max is what the plan names.
5. **The workload `Total`** is not exported: it is the sum over tenants, which a reader gets
   by summing (or by `obs.detail` off, which folds tenants).
6. **Histogram clamps** are exported as counters `<name>_clamped_low` / `<name>_clamped_high`
   that `HistogramDesc` declares automatically (PLAN.md 7.7: "Clamped out-of-range values
   appear as counters").
7. **`obs.detail` off** folds tenant labels into the empty tenant (sum, max for high-water
   marks) and drops histogram points in the emitter (one branch per point); on the recording
   side `DetailHistogram` holds no buckets. The collector config field is `obs.Config.Detail`.
8. **Unlabelled metrics must be emitted every collect** (an error otherwise); labelled ones
   are not checked, but every mapping emits every known label value. The CSV writer also
   persists every row key it has written once (generalising "a tenant's rows stay").
9. **Persisted rows keep their last value** (counters and gauges alike).
10. **The obs component's CSV and Prometheus numbers** come from adapters implementing
    `obs.FlushReporter` / `obs.TenantReporter`; the collector does not know which adapter is
    the CSV one. Snapshot N shows the adapters' state before publish N, so a run's final
    `csv_flushes` is one less than its flush count.
11. **Adapter errors**: `Export` returning an error is counted in `export_errors{reason=<name>}`
    and the adapter is marked failed for the health rule; `Publish` returns only emit errors.
    Panics in adapters are not recovered.
12. **`Last()` returns a deep copy** (the published value cannot be changed by a reader);
    adapters get the published value itself and must not change it (the conformance test
    checks that). `Publish` returns a copy too.
13. **Merge** (`obs.Merge(reg, instance, snaps...)`): points by declared rule, histograms by
    adding bucket counts with equal bounds, `Seq` = max, build/fingerprint `mixed` when they
    differ; refuses an undeclared metric (when the registry is non-empty) and kind or unit
    clashes. Ratios are never stored anywhere.
14. **Health**: `obs.HealthTable` with named `Rule{Name, Check(snapshot) (State, reason)}`,
    overall = worst; the only rule now is `obs.CollectorRule()` (`collector`: degraded before
    the first publish, after emit errors, or while an adapter has export errors; never
    `failed`). JSON: `{"state":…,"checks":[{"name":…,"state":…,"reason":…}]}` with a hand-written
    escaper (byte-equal to `encoding/json`, tested on random strings), because `obs` is in the
    determinism list and may not import `encoding/json`.
15. **CSV final flush**: the owner publishes a last time and then calls `Close`; `Close` also
    re-writes the last snapshot if that one was dropped from a full async queue. After the cap
    or a write error the writer stops for good, and `run.csv` is not written: the run stays
    visibly unfinished and every assertion on it fails (fail closed). `Close` closes the data
    files before writing `run.csv`.
16. **The CSV clock is always given** (`Config.Clock`, required): the simulator passes the
    virtual clock; a real process passes a monotonic clock from its own `main` (chunk 6). No
    clock code is in the adapter. **Async CSV mode** uses one goroutine, a `chan` of `Config.QueueCap` (default 64, max
    4,096) and atomics for the stats; `t_ns` is stamped by the caller at `Export`. `Strict`
    is the test mode.
17. **Prometheus**: the in-repo grammar checker accepts integer sample values only (this
    project exports integers only; a float value is a finding); `_sum` omitted (no exact sum in the snapshot; a midpoint estimate would be
    a made-up number); names `mg_<component>_<metric>[_total]` plus a `component` label;
    tenant slots first-come among tenants whose counters changed since the previous publish
    (or among the configured list); default `EvictAfter` 60 publishes; folded and evicted
    tenants are summed into `other`; hostile names (empty, `other`, > 64 bytes, invalid UTF-8)
    always fold. Bounds: `DefaultExportBuckets` (16, 100 µs … 60 s) for `ns`, `SizeBuckets` (16
    powers of 4) for `bytes`/`count`.
18. **Log line**: `obs.publish seq= instance= build= config= points= histograms=` then every
    `Headline` metric's untenanted points as `component.metric[.outcome][.reason][.mode]=value`;
    string values quoted when they hold spaces, quotes, `=` or non-ASCII, cut to 32 bytes;
    the line is at most 1,024 bytes and ends ` truncated=1` when cut.
19. **csv-assert.sh**: the SQL file header is `-- requires: comp.metric ...` (each must have rows
    in the final flush of the runs); a SQL file returns one text column, rows are violations;
    every SQL file starts with the schema-version guard over `runs`. The runner also checks each
    run directory's three headers and every `schema_version` field with `awk` before DuckDB reads
    anything. In root mode a root with no runs is a note, a missing root is a failure, a scenario
    with SQL files and no run is a failure, and no run at all is a failure. Pinned family `1.5.x`.
20. **Seeds of the flush test**: `TestScenarioFlush` runs the pinned seeds 1 and 42 (not the
    sweep's 200: the sweep already checks the replay law for every seed in `world`), or only
    `-seed N` when given; `-short` runs seed 1.
21. **Publish interval in the simulator**: 100 ms of virtual time by default, as a background
    timer (`MarkBackground`) so it neither keeps the world from quiescing nor counts as a leak.
    The first publish is at the instant the workload is connected (t = 0), the last after
    `Stop` at the final virtual time.

## Claims and the tests that prove them

| # | Claim | Tests (file: function) |
| --- | --- | --- |
| 1 | One raw export; no component or simulator package imports an adapter | `tools/lint-rules/imports.txt` chunk 3 `deny` lines (G1 importlint; checked by planting both directions of violation during the build: both reported); `simrun/imports_test.go: TestOneRawExportImportRule`; adapters take only `obs.InstanceSnapshot` (`obs.Adapter`) |
| 2 | Adapter output deterministic; snapshots carry no time and no map order | `obstest.Conformance` "deterministic" for every adapter; `simrun_test.go: TestScenarioFlush` (two runs per seed: CSV files, Prometheus, JSON and log lines byte-identical, trace digests equal); `obs/collector_test.go: TestSnapshotIsSortedAndHasNoTime` |
| 3 | CSV schema 1 exactly (columns, order, sorting, zero rows, tenant persistence, integers, `run.csv` last) | `csvflush_test.go: TestGoldenHeader`, `TestGoldenFilesAndByteIdentity` (golden `testdata/fixture-*.csv`), `TestRowsSortedZeroRowsPresentIntegersOnly`, `TestTenantRowsPersistAfterTheTenantGoesQuiet`, `TestAsyncWriterNeverBlocksTheCaller` (no `run.csv` before Close), `TestSizeCapIsCountedAndAFailureInTestMode` (no `run.csv` after stop); `catalog_test.go: TestCSVSchemaDocumentIsGeneratedFromTheCode` |
| 4 | Round trip exact for every metric and bucket | `csvflush.RoundTrip` called by `simrun.Run.Check` for every baseline scenario (`TestScenarioFlush`, both seeds); canaries `csvflush/csvflush_test.go: TestRoundTripCatchesPlantedBugs`, `csvflush/canary_test.go: TestRoundTripCatchesAPlantedBugInABaselineRun` (drop the last bucket row; stale values) |
| 5 | Tier S flush files byte-identical for one seed | `TestScenarioFlush` (same process); gate extra step 3 (two processes, `diff -r`, 15 files) |
| 6 | CSV never blocks the caller in real-process mode; drops and counts at the cap, on write errors, on a full queue | `TestAsyncWriterNeverBlocksTheCaller` (writer goroutine held; 5 publishes onto a full queue return at once, 5 drops counted; final flush lands on Close), `TestAsyncWriterUnderConcurrentStatsReads`, `TestSizeCapIsCountedAndAFailureInTestMode` (strict and real-process), `TestWriteErrorIsCountedAndAFailureInTestMode`; package in `race-packages.txt`; gate extra step 4 (`-race -count=10`) |
| 7 | The assertion runner fails closed and never skips | `simrun/csvassert_test.go: TestCSVAssertFailsClosed` (planted wrong number, no rows for the metric, no requires line, missing `run.csv`, unknown schema version, unknown header, bad SQL, scenario without runs, missing root, wrong DuckDB version — each must exit non-zero with the reason in the message), `TestCSVAssertFailsWithoutDuckDB`; gate extra step 1 (`MG_REQUIRE_DUCKDB=1`, a SKIP is a failure) |
| 8 | Prometheus output parses; at most `prom_tenant_max` + 1 tenant values, hostile names included | `promtext_test.go: TestConformance` (fixture with quotes, newline, backslash, 160-byte name, `other`, unicode; label policy checked), `TestTenantLabelBoundWithTenThousandHostileTenants` (10,000 tenants, max 0/1/32/100, three publishes, totals preserved), `TestQuietTenantsAreEvicted`, `TestCheckerRejectsBadExposition` (18 malformed texts), `TestRebucketIsCumulativeOnTheShortList`; `simrun.Run.Check` runs the checker on every baseline run |
| 9 | Merge laws | `obs/merge_test.go: TestMergeOfInstancesEqualsOneInstanceThatSawEverything` (300 seeds, 1–5 instances), `TestMergeIsAssociativeAndCommutative`, `TestHighWaterMergesByMaxAndCountersBySum`, `TestMergeRefusesUndeclaredAndKindClash`, `TestMergeBucketsAddsPerBucket` |
| 10 | Recording allocates nothing; detail off holds no histogram memory | `obs/cost_test.go: TestRecordingAllocatesNothing` (`testing.AllocsPerRun`, 5 paths); `obs/collector_test.go: TestDetailOffFoldsTenantsAndHoldsNoHistogram`; `simrun_test.go: TestDetailOffExportsNoHistogramAndNoTenant` |
| 11 | Baseline counts from the flushed files with DuckDB equal the simulator's | `simrun/csvassert_test.go: TestCSVAssertBaselineCountsFromFlushedFiles` (5 SQL files over the 5 baselines); gate G7 over the sweep's run directories; Go-side cross-check `simrun_test.go: checkBaselineCounts` (exported = `world.Observation`, 500/500, 1,000/1,000, sent and intended) |
| 12 | No metric exported without being declared and documented | `collector_test.go: TestUndeclaredMetricIsAnError`, `TestEmitterRefusesWrongKindLabelDuplicateNegativeAndMissing`; `catalog_test.go: TestCSVSchemaDocumentIsGeneratedFromTheCode`, `TestEveryDeclaredMetricIsInTheDocument`; `simrun/registry_test.go: TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented`; G8 `scripts/gen-docs.sh --check` |

The conformance test can fail: `obstest/conformance_test.go: TestConformanceCatchesBrokenAdapters`
runs every check against a correct toy adapter (all pass) and six broken ones (keeps a
reference, modifies its input, drops a declared metric, adds an undeclared label, is not
deterministic, hides its errors); each must fail exactly the checks named for it.

Also: `obstest.Conformance` passes for `jsonstats` (exact), `csvflush` (exact), `promtext`
(folded: untenanted points exact, totals over tenants exact; three configurations) and `logline`
(headline: every value present equals the snapshot). Its six parts: values, labels (only
declared labels, plus the subject's policy), deterministic, no retained reference (the snapshot
is scribbled after `Export` and the output must not change; the snapshot must not be modified
by `Export`), error counted and not propagated (a failing adapter attached to a collector:
`Publish` returns nil and `export_errors{reason=<name>}` = 1). Golden files per adapter:
`jsonstats/testdata/fixture.json`, `logline/testdata/fixture.log`,
`promtext/testdata/fixture.prom`, `csvflush/testdata/fixture-{metrics,histograms,run}.csv`;
rewrite with `go test <pkg> -update` (each adapter's test package declares its own `-update`
flag; `obstest.Golden` takes the value, so `obstest` registers no flag that could clash with a
package that imports it).

Counters must move: `simsrc_test.go: TestMappingsMoveEveryMetric` fills every field of each
snapshot with a distinct non-zero value by reflection and requires every declared metric to
move and every field to reach an exported value (it failed, as it should, when one mapping
line was deleted during the build). The baseline runs emit all 113 declared metrics; 51 of them
stay 0 in every baseline (fault and cancel counters, drops, dropped log entries, in-flight
gauges at the end), listed by `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented`
with `-v`. The collector's own counters move in `TestPublishSequenceLastCopyAndAdapterErrors`
(export errors), `TestUndeclaredMetricIsAnError` (emit errors),
`TestSizeCapIsCountedAndAFailureInTestMode` (flush errors, rows dropped, export errors).

## Planted-bug canaries

Switches in `internal/obs/csvflush/export_test.go` (test-only; `Config.plant` is unexported):
`PlantDropLastBucket` (the last histogram of each flush loses its last bucket row) and
`PlantStaleValue` (each metric row is written with the previous flush's value at the same
position). `TestRoundTripCatchesPlantedBugs` (fixture) and
`TestRoundTripCatchesAPlantedBugInABaselineRun` (`direct-open-loop-overload`, seed 1) require
the round trip to pass clean and fail with each plant. The DuckDB runner's planted wrong
number is in `TestCSVAssertFailsClosed/planted-wrong-number`.

## Cost model (PLAN.md section 7.3)

vm-1 (4 vCPU DigitalOcean "DO-Regular", shared; Go 1.26.8; numbers vary by about ±30% run to
run). Commands: `go test -run '^$' -bench 'Record|Publish' -benchtime 2000x ./internal/obs/`,
`go test -run '^$' -bench FlushFixture -benchtime 2000x ./internal/obs/csvflush/`.

| What | Cost |
| --- | --- |
| Counter increment (`obs.Counter.Inc`) | about 2 ns, 0 allocations (gate: `TestRecordingAllocatesNothing`) |
| Histogram record (`obs.Histogram.Record`, 3,302 buckets) | 20–35 ns, 0 allocations |
| Detail histogram, `obs.detail` off | about 3–5 ns (one nil check), 0 bytes held |
| Detail histogram, `obs.detail` on | about 20 ns, 26,416 bytes held per histogram |
| One publish, synthetic source, 1 tenant (3 counters + 1 histogram), detail off / on | 30–60 µs / 30–75 µs; 14 / 22 allocations |
| One publish, 32 tenants, detail off / on | 0.24–0.34 ms / 0.5–1.5 ms; 41 KB / 172 KB |
| One publish, 1,000 tenants, detail off / on | 3.6–8.7 ms / 15–30 ms; 1.3 MB / 4.5 MB |
| CSV flush of the fixture snapshot (78 rows + 13 histograms), sync / async (caller side) | about 0.33 ms / 0.03 ms |

With detail on, most of a publish is building each histogram's snapshot (3,302 buckets per
histogram); the per-tenant cost is linear in histograms × buckets. A baseline scenario run
with every adapter attached and a 100 ms publish interval takes 1–4 s of wall time (seed 1).

## Steady-state row

| What grows | What bounds it | Gauge |
| --- | --- | --- |
| Per-tenant label sets in Prometheus | `TenantMax` slots (default 32, max 1,024) + eviction after `EvictAfter` quiet publishes; the previous publish's tenant list (proportional to the snapshot) | `obs.prom_tenants_tracked` |
| CSV files | `MaxBytes` (`obs.csv.max_bytes`, default 1 GiB): stop, drop and count | `obs.csv_bytes_written`, `obs.csv_rows_dropped`, `obs.csv_flush_errors` |
| CSV persisted row keys | `MaxTrackedRows` 1,048,576; beyond it new keys are not persisted (still written while emitted) | — |
| Async CSV queue | `QueueCap` (default 64, max 4,096): drop and count | `obs.csv_queue_depth` |
| Collector sources, adapters, health rules | 32, 16, 64 | `obs.sources`, `obs.adapters` |
| Emit errors kept per collect | 16 per source | `obs.emit_errors` |
| Simulator publishes | `simrun.MaxPublishes` 100,000 per run | `obs.publishes` |
| Log line | 1,024 bytes | — |

## Limits

- `obs` has no locks: `Last()` is safe on the owner's goroutine only; the shell (chunk 6) hands
  the copy to the admin listener.
- A Prometheus re-bucketed bucket may sit one bound high when a full-resolution bucket
  straddles a bound (error < 0.79% of the value). `_sum` is not exported.
- When a tenant gets a Prometheus slot out of `other`, `other` decreases (a counter reset to
  Prometheus). The CSV and JSON carry every tenant and do not have this.
- Merging high-water marks by max gives a lower bound of the fleet peak of a summed level.
- With `obs.detail` off, duplicate untenanted points from a buggy mapping are summed instead of
  reported (the fold cannot tell them from tenants being folded).
- The CSV adapter's tenant persistence writes the last value of a row its source stopped
  emitting, gauges included.
- `RoundTrip` allows extra rows with a tenant label (persisted ones) and checks the last flush
  only; earlier flushes are covered by byte identity and the SQL files.
- `csv-assert.sh` takes the scenario of a run from its path (`<root>/<scenario>/…`), not from
  `run.csv`.
- No adapter recovers from a panic in another adapter.

## Deviations

- **Gate G3 is red on two chunk 1 adversarial tests, by construction of this brief.**
  `agent_work/adversarial/c01-sim-kernel/determinism_test.go` walks every non-test Go file
  under `internal/obs/...` (skipping only directories named `simtest` and `obstest`) and fails
  on any `os` import, wall-clock call, channel, goroutine or float. When it was written,
  `internal/obs` was only the measurement half. This brief requires adapters as packages under
  `internal/obs/`, with the CSV adapter doing the file I/O ("keep file I/O in this adapter
  package") and running its own goroutine with a bounded queue in real-process mode. Every
  avoidable hit was removed (the Prometheus checker is integer-only, the harness and the
  catalog have no `os`, the adapter takes its clock from the owner); what remains is exactly
  `internal/obs/csvflush/reader.go` and `writer.go` (`os`; `chan`, `go`, `select`, send):
  `TestAdv_10_AliasedEntropyAndClock`, `TestAdv_10_ConcurrencyAndFloatingSyntax`. I may not
  edit `agent_work/adversarial/`. Proposed resolution for the orchestrator: narrow those two
  attacks to the packages of `tools/lint-rules/det-packages.txt` (the scope the builder rules
  give the determinism rule; `csvflush` is deliberately not in it, and is in
  `race-packages.txt` instead), or add `csvflush` to their skip list. Every other check of
  G3, and every product package, passes.

- **Size.** Product Go (non-test) added by this chunk: 3,979 lines, against the brief's ~1,400
  (up to 2×). The excess is test machinery that lives in non-test files because other packages
  use it: `obstest` conformance and golden helpers (364), the Prometheus grammar checker and
  decoder (421), the scenario harness `simrun` (194), and the generated-document code (100).
  Without those, about 2,900. Test files: 2,297 lines (59 test and benchmark functions); with
  `obstest` and the checker counted as test code the two sides are about equal. Complete, so no
  split request.
- **The flush is turned on outside `internal/sim/world`**: the brief allowed editing the
  scenario harness files of chunk 2, but `world` may not import an adapter (claim 1), so the
  baseline scenarios are flushed by `internal/obs/simrun` (`TestScenarioFlush` over
  `world.Scenarios()`), not by `world.TestScenario`. Chunk 6 should attach the collector where
  the engine is created.
- **`gen-docs.sh`** has a `cd` to the repository root before the loop (the brief's shape plus
  one line), so it works from any directory.

## Index rows

For `agent_work/INDEX.md` (the orchestrator adds them):

| Path | What |
| --- | --- |
| `scripts/csv-assert.sh` | SQL assertions with DuckDB 1.5.x over flushed CSV runs; `<run-dir> <sql>...` or `<root>...` (every `test/analysis/<scenario>/*.sql`); fails closed |
| `scripts/gen-docs.sh` | Runs every `scripts/gen-docs.d/*.sh` (`--check` diffs against the committed documents) |
| `scripts/gen-docs.d/c03-csv-schema.sh` | Generates `docs/csv-schema.md` from `internal/obs/catalog` |
| `test/analysis/views.sql` | DuckDB views `metrics`, `hist`, `runs`, `final`, `final_hist` over lists of flushed files |
| `make csv-assert RUNS=<dir> [SQL=<files>]` | The runner through make |
| `agent_work/chunks/c03-obs-spine/gate/extra.sh` | G10: DuckDB e2e and runner self-tests (required), canaries, cross-process byte identity, async writer under `-race` |

## Commands

```
PATH=/usr/local/go/bin:$PATH
go test ./internal/obs/...                                              # everything (about 1 min)
go test -short ./internal/obs/...                                       # the CI subset
MG_REQUIRE_DUCKDB=1 go test -count=1 -run '^TestCSVAssert' -v ./internal/obs/simrun
go test -race -count=10 -run Async ./internal/obs/csvflush
go test ./internal/obs/simrun -run '^TestScenarioFlush$/^direct-500-connections$' -seed=42 -count=1
RUN_DIR=/tmp/runs go test -count=1 -run '^TestScenarioFlush$' ./internal/obs/simrun && scripts/csv-assert.sh /tmp/runs
scripts/csv-assert.sh /tmp/runs/direct-500-connections/1/base test/analysis/direct-500-connections/peak-connections.sql
go test ./internal/obs/jsonstats ./internal/obs/logline ./internal/obs/promtext ./internal/obs/csvflush -update   # rewrite goldens
scripts/gen-docs.sh; scripts/gen-docs.sh --check
go test -run '^$' -bench 'Record|Publish' -benchtime 2000x ./internal/obs/
scripts/gate.sh c03-obs-spine
```
