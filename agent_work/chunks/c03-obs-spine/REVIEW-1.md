VERDICT: FAIL

## Findings

1. **BLOCKER** — `scripts/csv-assert.sh:58,88-93` (and `test/analysis/*/*.sql`)
   - **What is wrong:** Three fail-closed / schema-v1 defects in `scripts/csv-assert.sh`:
     1. **Fails open on empty runs in multi-run mode.** Line 90 checks `SELECT count(*) FROM final WHERE component = ... AND metric = ...` summed across all runs together rather than per run. In addition, all five SQL files under `test/analysis/*/*.sql` query `FROM final GROUP BY run_id` instead of joining against `runs` (`LEFT JOIN`). When a root contains two runs for the same scenario and one run has a valid `run.csv` with a header-only (0-row) `metrics.csv`, the empty run vanishes from `final` and `scripts/csv-assert.sh` exits `0`. This falsifies brief claim 7 ("fails closed ... on an input with no rows for the metric under test").
     2. **Rejects valid schema-v1 CSVs containing quoted newlines (including this chunk's own golden fixture).** Line 58 runs `awk -F, 'NR > 1 && $1 != "1"'` on raw physical lines. RFC 4180 CSV written by `encoding/csv` puts literal newlines inside quoted fields (e.g., `"new\nline"` from `obstest.HostileTenants`). On the continuation line, `$1` is `line"`, so `csv-assert.sh` rejects valid `csvflush` output with `unknown schema version line"`.
     3. **`-- requires:` rejects histogram metrics.** Line 90 checks only `final` (scalar metrics from `metrics.csv`), never `final_hist` (histograms from `histograms.csv`). Listing a valid histogram metric such as `workload.latency` in `-- requires:` fails with `no rows for the metric under test workload.latency in the final flush (0)`.
   - **How I know:**
     - Copied two `direct-500-connections` runs into `/tmp/zgw-c03-obs-spine-review1/repro-multirun/direct-500-connections/{1,2}/base/`, truncated run 2's `metrics.csv` to header-only (`head -n1`) and updated run 2's `run_id` in `run.csv`; ran `scripts/csv-assert.sh /tmp/zgw-c03-obs-spine-review1/repro-multirun/direct-500-connections test/analysis/direct-500-connections/peak-connections.sql` -> exited `0` (`ok: test/analysis/direct-500-connections/peak-connections.sql over 2 runs`).
     - Copied `internal/obs/csvflush/testdata/fixture-{metrics,histograms,run}.csv` into a run directory and ran `scripts/csv-assert.sh` with `-- requires: admission.zero` -> exited `1` with `FAIL: .../metrics.csv: unknown schema version line"`.
     - Ran `scripts/csv-assert.sh` on a valid `direct-500-connections` run with `-- requires: workload.latency` -> exited `1` with `no rows for the metric under test workload.latency in the final flush (0)`.
   - **What a fix must achieve:**
     - Validate `schema_version` with a CSV-aware parser (DuckDB `read_csv` over `metrics`, `hist`, `runs`, or a proper RFC 4180 check) rather than line-oriented `awk`, so valid quoted newlines pass while any `schema_version <> 1` row still fails closed.
     - Check `-- requires:` **per run** (`runs LEFT JOIN` or checking every `run_id` in `runs` has at least one matching row in `final` or `final_hist`), and fail if any run in `runs` has 0 rows in `metrics`/`final` or is missing a required metric. Have the SQL assertion queries `LEFT JOIN` from `runs` (or enforce non-empty `final` per run in the runner) so an empty run can never silently pass. Add self-tests in `TestCSVAssertFailsClosed` for a multi-run directory where one run is empty, a fixture run with a quoted-newline tenant, and a histogram `-- requires:`.

2. **MAJOR** — `internal/obs/merge.go:45-81`
   - **What is wrong:**
     1. **Nil-pointer panic when `reg == nil`.** Line 57 calls `reg.Lookup(p.Component, p.Metric)` unconditionally before line 59 checks `else if reg != nil && reg.Len() > 0`. Passing `reg == nil` when two snapshots share any point dereferences `r.descs` in `Registry.Lookup` (`internal/obs/metric.go:264`) and panics.
     2. **Undeclared metrics silently accepted.** In `mergePoints` (lines 49–51), any point that does not collide with the previous point is appended and skipped via `continue` without consulting `reg`. An undeclared metric present in only one snapshot is never rejected (`TestMergeRefusesUndeclaredAndKindClash` only passed `(a, a)`, which masked the bug). In `mergeHists` (lines 67–81), `reg` is never consulted at all, so undeclared histograms are always accepted even when repeated across snapshots.
   - **How I know:** Ran three scratch tests against `internal/obs`:
     - `Merge(nil, "fleet", a, b)` -> `PANIC: runtime error: invalid memory address or nil pointer dereference`.
     - `Merge(reg, "fleet", a, b)` with undeclared point `pool.ghost` only in `a` -> returned `err == nil`.
     - `Merge(reg, "fleet", a, a)` with undeclared histogram `pool.ghost_hist` -> returned `err == nil`.
   - **What a fix must achieve:** Guard `reg != nil` before calling `reg.Lookup`; when `reg != nil && reg.Len() > 0`, validate every point and every histogram (even when it appears in only one snapshot) against `reg` and reject undeclared metrics/histograms and kind clashes. Cover `reg == nil`, single-snapshot undeclared points, and undeclared histograms in `merge_test.go`.

3. **MAJOR** — `internal/obs/csvflush/writer.go:233-253, 270-294, 405-410`
   - **What is wrong:** `obs.csv.max_bytes` is exceeded on the first flush (and on a 0-flush `Close()`). In `write()`, line 235 checks `w.bytes.Load()+size > w.cfg.MaxBytes` using only `len(mb)+len(hb)` without accounting for the header bytes (`len(hm)+len(hh)` = 222 bytes) that `w.open()` is about to write on line 239. `w.open()` writes both headers unconditionally without checking `MaxBytes`, and `write()` writes `mb` and `hb` without rechecking. On a 0-flush `Close()`, line 406 likewise calls `w.open()` without checking `MaxBytes`.
   - **How I know:** Ran scratch test `TestRepro_MaxBytesExceededOnFirstFlush` on the fixture snapshot (`bodyOnly=10617`, `hdrLen=222`) with `MaxBytes=10627`: `w.Export(s)` returned `nil` and wrote `10839` bytes (`BytesWritten=10839 > MaxBytes=10627`). `TestSizeCapIsCountedAndAFailureInTestMode` missed this because it used `MaxBytes=4000` (which only tripped on a later flush, or check failed to test a cap smaller than headers + flush 1).
   - **What a fix must achieve:** Include pending header bytes in the cap check before opening/writing files (both in `write()` and on 0-flush `Close()`), so total bytes written to disk and `FlushStats().BytesWritten` never exceed `cfg.MaxBytes`. Add a test with `MaxBytes` smaller than headers alone and with `MaxBytes` between `bodyOnly` and `bodyOnly+hdrLen`.

4. **MAJOR** — `internal/obs/csvflush/writer.go:399-403` (and `internal/obs/csvflush/csvflush_test.go:357,369`)
   - **What is wrong:** In async mode, when the last flush was dropped because the queue was full, `Close()` decrements `w.flushErrors` by `-1` and `w.rowsDropped` by `-w.last.rows` before re-writing `w.last`. Both `obs.csv_flush_errors` and `obs.csv_rows_dropped` are declared as `counter` ("cumulative, never decreases", PLAN.md section 7.7 and `docs/csv-schema.md:22,113,116`). Decreasing a counter violates schema v1 counter monotonicity (observed directly in `TestAsyncWriterNeverBlocksTheCaller`: `st.FlushErrors` drops from `5` to `4`) and leaves `obs.export_errors{reason=csv}` (5) permanently inconsistent with `obs.csv_flush_errors` (4).
   - **How I know:** Code inspection of `writer.go:399-403` and `csvflush_test.go:357` (`st.FlushErrors != 5`) vs `csvflush_test.go:369` (`st.FlushErrors != 4`).
   - **What a fix must achieve:** Never decrement `flushErrors` or `rowsDropped`. Either keep the drop counted (since that async `Export` call genuinely dropped from the queue and returned an error to the collector, and `Close()` performs a separate final flush) or avoid counting a flush as dropped until it is actually abandoned. Ensure `FlushStats()` counters are strictly non-decreasing across the writer's lifetime.

5. **MAJOR** — `internal/obs/collector.go:84` (`internal/obs/collector_test.go:214-255`)
   - **What is wrong:** The `obs.detail` off guard inside `Emitter.Histogram` (`if !ok || !e.detail { return }`) is never exercised by any test. Every source in the test suite (`synth.emit` at `internal/obs/synthetic_test.go:76` and `simsrc.MapWorkload` at `internal/obs/simsrc/simsrc.go:329`) wraps `e.Histogram` in `if e.Detail()`, so removing `|| !e.detail` in `collector.go:84` leaves all tests green.
   - **How I know:** Mutation check M5 (replaced `if !ok || !e.detail {` with `if !ok {` in `internal/obs/collector.go:84`) -> `go test -count=1 ./internal/obs` passed completely (`0` failures).
   - **What a fix must achieve:** Add a test in `internal/obs/collector_test.go` with a source that calls `e.Histogram(...)` unconditionally while `Config.Detail` is `false`, asserting that neither histogram points nor `*_clamped_low` / `*_clamped_high` points are emitted and that undeclared/bad-label histogram calls still fail even with `Detail: false`.

6. **MAJOR** — `internal/obs/health.go:90-109`, `internal/obs/collector.go:174, 299-301`
   - **What is wrong:**
     1. `adapterSlot.failed` is maintained in `Collector.Publish()` (`collector.go:299-301`: set `true` on export error, `false` on success) but is never read or exported anywhere (dead state).
     2. `CollectorRule()` (`health.go:99-105`) instead checks cumulative `obs.export_errors > 0`. Once any adapter has a single transient error and then succeeds on subsequent publishes, `Collector.Health()` stays `StateDegraded` permanently, contradicting `NOTES.md:96,106-107` ("the adapter is marked failed for the health rule"; "degraded ... while an adapter has export errors").
     3. Because `emitSelf` runs during `Collect()` before `c.emitErrors++` and before `slot.a.Export(snap)`, `c.Health(ht)` immediately after publish #1 with a failing adapter (or an emit error) returns `StateOK` instead of `StateDegraded` (lags by one publish).
   - **How I know:** Reproduced in scratch test `TestRepro_HealthDoesNotReflectPublish1FailureAndNeverRecovers`: immediately after publish #1 with a failing adapter, `c.Health(ht).State` was `ok`; after clearing the adapter error and publishing twice more, `c.Health(ht).State` remained `degraded (adapter export errors: flaky)`.
   - **What a fix must achieve:** Either expose active failing-adapter state (e.g., a gauge or a health check reading `slot.failed`, or document clearly if latched degradation is intended and remove the dead `slot.failed` field) and ensure recovery after transient adapter failure is tested; also decide and test whether `Collector.Health` reflects the latest publish's emit/export errors immediately or explicitly document the one-publish lag.

7. **MINOR** — `internal/obs/csvflush/reader.go:217-221, 239-243`
   - **What is wrong:** `comparePoints` and `compareHists` ignore every extra row in the final flush whose `Tenant != ""` without checking whether that tenant row actually appeared in an earlier flush of the same run. A fabricated tenant row injected only into the final flush passes `csvflush.RoundTrip`.
   - **How I know:** Scratch test `TestRepro_RoundTripIgnoresSpuriousTenantRow` appended `1,fixture/1/base,1,2000000,inst-1,zfake,bogus_metric,counter,ghost_tenant,,,,999999` to the final flush of `metrics.csv` and called `csvflush.RoundTrip(w.RunDir(), last)` -> returned `nil`.
   - **What a fix must achieve:** Since `RoundTrip` already reads all flushes in `run`, accept an extra tenant row in the final flush only if the exact same `(instance, component, metric, labels)` key appeared in an earlier flush with the same last value; reject fabricated rows that never appeared earlier.

8. **MINOR** — `internal/obs/promtext/check.go:340-353`
   - **What is wrong:** `checkHistogram` checks monotonicity (`h.counts[i] < h.counts[i-1]`), `h.counts[n-1] > h.inf`, and `h.inf == h.count`, but never checks for negative bucket counts (`h.counts[0] < 0` or `h.inf < 0`).
   - **How I know:** Scratch test `TestRepro_NegativeHistogramBucketAccepted` passed `# TYPE h histogram\nh_bucket{le="1"} -2\nh_bucket{le="2"} -1\nh_bucket{le="+Inf"} -1\nh_count -1\n` to `promtext.Check` -> returned `nil`.
   - **What a fix must achieve:** Reject negative bucket counts and negative `+Inf`/`_count` values in `checkHistogram`, and add a case to `TestCheckerRejectsBadExposition`.

9. **MINOR** — `internal/obs/obstest/conformance.go:295-305`
   - **What is wrong:** In `compare`, the `Headline` fidelity case checks only `len(got.Points) > 0` and that points present in `got` equal `want`, then returns `nil` early. A headline adapter that drops all headline metrics after the first one still passes `obstest.Conformance`.
   - **How I know:** Mutation check M7a added `break` after the first headline metric in `internal/obs/logline/logline.go:74`; `go test -count=1 -run '^TestConformance$' ./internal/obs/logline` passed (only `TestGolden` and `TestOneBoundedLineInFixedOrder` caught it).
   - **What a fix must achieve:** For `Headline` fidelity, verify that every declared `Headline` metric present in `want` (at least for non-truncated fixture inputs) appears in `got`.

10. **MINOR** — `internal/obs/csvflush/writer.go:101,206,316` and `agent_work/chunks/c03-obs-spine/NOTES.md:158,171`
    - **What is wrong:**
      1. `writer.go:316` (`plantStaleValue`) writes `v = w.prevVal[0].Value` instead of `w.prevVal[i].Value`, contradicting `NOTES.md:196` ("previous flush's value at the same position").
      2. `writer.go:101` declares `exported int64`, incremented on line 207 and never read.
      3. `writer.go:206` sorts `j.snaps` by `Instance` only instead of `(Seq, Instance)`, which would break `(flush_seq, instance, ...)` row order if multi-instance `Flush` were ever called with differing `Seq` values.
      4. `NOTES.md:158` says `TestTenantLabelBoundWithTenThousandHostileTenants` tests `max 0/1/32/100`, whereas `promtext.New` maps `TenantMax: 0` to `DefaultTenantMax` (32) (so `0` tests 32, not 0). `NOTES.md:171` says `obstest.Conformance` has "six parts" while `obstest.Checks()` has 5 checks.
    - **How I know:** Direct inspection of the referenced lines.
    - **What a fix must achieve:** Use `w.prevVal[i].Value`, drop unused `exported`, sort `j.snaps` by `(Seq, Instance)`, and align `NOTES.md` wording with the code.

## Mutation checks

All mutations were applied one at a time in a scratch copy under `/tmp/zgw-c03-obs-spine-review1/work` and reverted after each run:

| # | Property mutated | Mutation | Result | Caught by |
| --- | --- | --- | --- | --- |
| M1 | High-water marks merge by max | `internal/obs/collector.go:123`: `return max(a, b)` -> `return a + b` | **CAUGHT** | `TestMergeOfInstancesEqualsOneInstanceThatSawEverything`, `TestHighWaterMergesByMaxAndCountersBySum` |
| M2 | Prometheus tenant label bound (`prom_tenant_max`) | `internal/obs/promtext/promtext.go:148`: disabled `len(a.slots) >= a.cfg.TenantMax` break | **CAUGHT** | `TestConformance`, `TestGolden`, `TestTenantLabelBoundWithTenThousandHostileTenants`, `TestQuietTenantsAreEvicted` |
| M3 | CSV writes zero-valued unlabelled metrics | `internal/obs/csvflush/writer.go:314`: skip point when `p.Value == 0 && p.Labels.Tenant == ""` | **CAUGHT** | `TestConformance`, `TestGoldenFilesAndByteIdentity`, `TestRowsSortedZeroRowsPresentIntegersOnly`, `TestTenantRowsPersistAfterTheTenantGoesQuiet`, `TestAsyncWriterNeverBlocksTheCaller`, `TestRoundTripCatchesAPlantedBugInABaselineRun` |
| M4 | CSV persists quiet tenant rows | `internal/obs/csvflush/writer.go:370`: disabled re-appending absent tracked points | **CAUGHT** | `TestTenantRowsPersistAfterTheTenantGoesQuiet` |
| M5 | `obs.detail` off drops histograms in `Emitter.Histogram` | `internal/obs/collector.go:84`: `if !ok \|\| !e.detail` -> `if !ok` | **MISSED** | Passed all `./internal/obs` tests (Finding 5) |
| M6 | Zero allocations per recorded event | `internal/obs/detail.go:53`: allocate 64 bytes in `Counter.Inc()` | **CAUGHT** | `TestRecordingAllocatesNothing` |
| M7a | Headline conformance checks all headline metrics | `internal/obs/logline/logline.go:74`: `break` after first headline metric, run `TestConformance` only | **MISSED** | `TestConformance` passed (Finding 9); full package test (`M7b`) caught it in `TestGolden` and `TestOneBoundedLineInFixedOrder` |
| M8 | CSV `RoundTrip` rejects fabricated extra tenant rows | Appended fabricated `ghost_tenant` row to final flush of `metrics.csv`, called `csvflush.RoundTrip` | **MISSED** | `csvflush.RoundTrip` returned `nil` (Finding 7) |
| M9 | `csv-assert.sh` enforces `-- requires:` | `scripts/csv-assert.sh:92`: bypassed `[ "$out" -gt 0 ]` check | **CAUGHT** | `TestCSVAssertFailsClosed` |

## Release It! checklist

- **Integration points:** Not applicable to client/database sockets here (this chunk adds no socket I/O); file I/O is isolated in `internal/obs/csvflush`, where write errors and short writes transition the writer to `stopped` and count errors/drops without blocking the caller in async mode (`writer.go:228-268`), though the first-flush header size bypasses `MaxBytes` (Finding 3).
- **Blocked threads:** No database connections here; in `csvflush` async mode the single writer goroutine exits cleanly when `Close()` closes `w.queue` and waits on `w.done` (`writer.go:182-190,395-398`), and `simrun`'s publish timer is marked background (`MarkBackground`) so scenario quiesce checks pass with 0 pending non-background timers.
- **Slow responses:** Not applicable to client/DB requests here; in async `csvflush`, a slow disk never slows the caller because `Flush()` uses a non-blocking `select` send with `default` drop-and-count (`writer.go:213-220`), verified under `-race` with a gated writer goroutine.
- **Cascading failures:** Adapter failures in `Collector.Publish()` are isolated, counted in `obs.export_errors`, and never propagate to the collector or other adapters (`collector.go:295-303`); however, `CollectorRule` health never recovers from a transient adapter failure and lags by one publish (Finding 6).
- **Chain reactions:** Not applicable here because this chunk builds observability plumbing and single-instance / merge utilities, not load-bearing request shedding or multi-instance failover.
- **Dogpile:** Not applicable here because this chunk performs no database dials or reconnect recovery loops.
- **Self-denial attacks:** `simrun` publishes at a fixed 100 ms interval capped at `MaxPublishes = 100,000` (`simrun.go:26,94`); `BenchmarkPublish` measures single-publish cost up to 1,000 tenants (3.0 ms detail-off / 39.2 ms detail-on on this VM) and `BenchmarkFlushFixture` measures sync vs async caller-side flush cost (0.59 ms / 0.075 ms).
- **Force multiplier:** Not applicable here because this chunk has no automatic feedback or control loop adjusting limits or rates.
- **Unbalanced capacities:** Producer/consumer boundary in `csvflush` async mode has a bounded channel `QueueCap` (default 64, max `MaxQueueCap` 4,096) with explicit non-blocking drop-and-count when full (`writer.go:148-151,168,213-220`), plus `MaxTrackedRows` (1,048,576) and `MaxBytes` (1 GiB default, with Finding 3 on first-flush header accounting).
- **Scaling effects:** Fleet merge rules (`sum`, `max` for high-water marks, per-bucket histogram merge) are implemented in `internal/obs/merge.go` (with Finding 2 on nil registry and undeclared checks); Prometheus text bounds tenant cardinality to `TenantMax + 1` (`promtext.go:60-63,148`) and re-buckets histograms to 16 bounds; CSV/JSON intentionally retain full per-tenant rows bounded by `obs.csv.max_bytes`.
- **Users:** Per-tenant resources in `promtext` are bounded by `TenantMax` (default 32, max 1,024) + `EvictAfter` quiet-publish eviction and `MaxTenantLabelLen` (64), folding hostile/excess tenants into `other`; `logline` caps string values at 32 bytes and total line length at 1,024 bytes; `csvflush` bounds persisted row keys at `MaxTrackedRows` (1,048,576) and bytes at `MaxBytes`.
- **Unbounded result sets:** Not applicable to SQL query replies here; per-publish memory is bounded by active tenants × declared metrics and `MaxEmitErrors` (16 per source), `MaxSources` (32), `MaxAdapters` (16), `MaxHealthRules` (64).
- **Compounds:** Async `csvflush` drop-on-full-queue meets `Close()` re-writing `w.last`, which currently subtracts from `flushErrors` and `rowsDropped` counters and breaks counter monotonicity (Finding 4); multi-run `csv-assert.sh` meets `FROM final GROUP BY run_id`, allowing an empty run to pass silently alongside a non-empty run (Finding 1).

## Observability

- **`Snapshot()` / collector self-metrics present:** Yes — `obs.SelfMetrics()` (`internal/obs/self.go:17-31`) declares 11 metrics under component `obs` (`publishes`, `export_errors`, `emit_errors`, `sources`, `adapters`, `csv_flushes`, `csv_flush_errors`, `csv_rows_dropped`, `csv_bytes_written`, `csv_queue_depth`, `prom_tenants_tracked`), exported via `Collector.emitSelf`. Existing chunk 1/2 components (`sim`, `simnet`, `fakepg`, `workload`) are mapped in `internal/obs/simsrc/simsrc.go` without modifying `internal/sim/...`.
- **Every adapter & generated CSV registry:** All 113 declared metrics (`obs` + `simsrc`) are in `internal/obs/catalog.Registry()` and `docs/csv-schema.md`; `scripts/gen-docs.sh --check` and `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` pass. All four adapters (`jsonstats`, `csvflush`, `logline`, `promtext`) pass `obstest.Conformance` and golden tests. Import lint enforces one raw export (`tools/lint-rules/imports.txt` + `TestOneRawExportImportRule`).
- **Counters move & conservation:** `TestMappingsMoveEveryMetric` proves every field of `sim`, `simnet`, `fakepg`, and `workload` snapshots reaches an exported metric; `TestPublishSequenceLastCopyAndAdapterErrors`, `TestUndeclaredMetricIsAnError`, and `TestSizeCapIsCountedAndAFailureInTestMode` assert `obs` self-counters move. Conservation is checked on baseline runs in Go (`checkBaselineCounts`) and SQL (`sent-equals-intended.sql`, `database-reads.sql`, `refusals.sql`). However, async `csvflush.Close()` breaks counter monotonicity on `csv_flush_errors` / `csv_rows_dropped` (Finding 4).
- **CSV-flush assertions:** 5 SQL files under `test/analysis/<scenario>/` pass over pinned and extra seeds, and `csvflush.RoundTrip` passes on all 5 baseline scenarios with planted-bug canaries (`PlantDropLastBucket`, `PlantStaleValue`). Blocked by Finding 1 (`csv-assert.sh` fails open on multi-run empty `metrics.csv`, rejects RFC 4180 quoted newlines, and rejects histogram `-- requires:`) and Finding 7 (`RoundTrip` ignores spurious tenant rows).
- **Health rule:** `obs.HealthTable` and `obs.CollectorRule()` exist with JSON serialization matching `encoding/json`, but `CollectorRule` has dead `slot.failed` state, never recovers after a transient adapter error, and lags by one publish (Finding 6).

## What I ran

All commands ran with `PATH=/usr/local/go/bin:/root/.local/bin:$PATH` on commit `085c617713da53c7f96e5ecac5d524ac82869cfc` (clean tree verified before and after):

- `go build ./... && go vet ./... && go test -count=1 ./...` — **PASS** (22 packages `ok`; `internal/obs` 1.985s, `internal/obs/catalog` 0.012s, `internal/obs/csvflush` 5.623s, `internal/obs/jsonstats` 0.046s, `internal/obs/logline` 0.022s, `internal/obs/obstest` 0.186s, `internal/obs/promtext` 4.510s, `internal/obs/simrun` 33.041s, `internal/obs/simsrc` 0.021s, `agent_work/adversarial/c01-sim-kernel` 4.400s, `agent_work/adversarial/c02-sim-world` 11.432s).
- `gofmt -l .` — **PASS** (empty output).
- `make lint` — **PASS** (`detlint` 2s, `importlint` 0s, `boundlint` 0s, `paniccheck` 0s; removed untracked `agent_work/ci/` artifact afterward).
- `scripts/gen-docs.sh --check` — **PASS** (`NOTE: docs/csv-schema.md is current`).
- `agent_work/chunks/c03-obs-spine/gate/extra.sh` — **PASS** (DuckDB `v1.5.5 (Variegata) d8cdaa33fd`, e2e + runner self-tests with `MG_REQUIRE_DUCKDB=1`, planted-bug canaries, 15 CSV files byte-identical across 2 processes, async writer ×10 under `-race`).
- Extra seeds (`-seed=7`, `-seed=13`, `-seed=99` on `TestScenarioFlush`, ~4.4s each) + `scripts/csv-assert.sh /tmp/zgw-c03-obs-spine-review1/extra-seeds` — **PASS** (15 run directories, 5 SQL files run, 0 failures).
- `RUN_DIR=/tmp/zgw-c03-obs-spine-review1/sweep50 scripts/sweep.sh -seeds 50 && scripts/csv-assert.sh /tmp/zgw-c03-obs-spine-review1/sweep50` — **PASS** (860 seed runs passed, 0 failures; 10 run directories, 5 SQL files run, 0 failures).
- `go test -run '^$' -bench 'Record|Publish' -benchtime 2000x ./internal/obs/` (86.152s) — `RecordCounter` 2.094 ns/op, `RecordHistogram` 28.26 ns/op, `RecordDetail/false` 4.682 ns/op (0 held-bytes), `RecordDetail/true` 15.79 ns/op (26416 held-bytes), `Publish/tenants=1` 28.2 µs / 41.4 µs, `tenants=32` 166.7 µs / 619.2 µs, `tenants=1000` 2.97 ms / 39.19 ms.
- `go test -run '^$' -bench FlushFixture -benchtime 2000x ./internal/obs/csvflush/` (2.168s) — `sync` 587.3 µs/op (121 allocs/op), `async` 74.8 µs/op (27 allocs/op).
- Reproducers & 9 mutation checks in `/tmp/zgw-c03-obs-spine-review1/work` — reproduced Findings 1–9; 6 mutations caught, 3 missed (`M5`, `M7a`, `M8`).
