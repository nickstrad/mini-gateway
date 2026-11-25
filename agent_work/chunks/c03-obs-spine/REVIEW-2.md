VERDICT: NITS

## Findings

All 10 findings from Review 1 (including all 3 sub-items of Finding 1) and all 10 product-defect adversarial breaks are fixed, each verified by reverting the fix in a scratch copy and confirming its regression test fails. The two Prometheus collision attacks (`TestAdv_8_CounterGaugeFamilyCollision`, `TestAdv_8_HistogramGaugeSeriesCollision`) are closed at registration (`Registry.Declare` refuses colliding series names, and `promtext` skips points whose kind disagrees with their declaration). Narrowed Claim 2 and narrowed Claim 12 are true for the code as it now stands. Three minor non-claim-falsifying issues remain:

1. **MINOR** — `internal/obs/metric.go:254-272`
   - **What is wrong:** `Registry.Declare(component string, descs ...MetricDesc)` inserts each expanded descriptor into `r.descs` inside the loop before validating or clash-checking subsequent descriptors in the same variadic call. If a direct caller passes `(validDesc, clashingDesc)`, `Declare` returns `ErrBadDesc` while leaving `validDesc` registered in `r.descs`. (`Collector.Register` at `internal/obs/collector.go:213-218` is unaffected because it clones `c.reg.descs` first.)
   - **How I know:** Direct code inspection of `internal/obs/metric.go:254-272` (line 268 inserts before the loop validates the remaining `descs`).
   - **What a fix must achieve:** Validate and insert into a temporary copy of `r.descs` (or roll back on error) so `Registry.Declare` is atomic on failure just like `Collector.Register`.

2. **MINOR** — `internal/obs/csvflush/writer.go:421-426`
   - **What is wrong:** On a zero-flush `Close()`, if `w.open()` returns an error on line 422, `w.stopped` is set and `err` is recorded (so `run.csv` is withheld and `Strict: true` returns the error), but `w.flushErrors` is not incremented — inconsistent with the header-cap check directly above (line 418) and the close-error branch directly below (line 430), both of which call `w.flushErrors.Add(1)`.
   - **How I know:** Direct code inspection of `internal/obs/csvflush/writer.go:416-433`.
   - **What a fix must achieve:** Call `w.flushErrors.Add(1)` in the `w.open()` failure branch of `Close()`.

3. **MINOR** — `internal/obs/self.go:83-97`
   - **What is wrong:** `Collector.selfView()` assigns `out := c.last` and rebuilds `out.Points` into a fresh slice, but leaves `out.Hists` aliasing `c.last.Hists`. `CollectorRule()` only reads `Points`, so nothing mutates `c.last` today, but any future custom `HealthTable` rule that mutated `s.Hists` (or called `s.Sort()` on unsorted buckets) would mutate `c.last` in place.
   - **How I know:** Direct code inspection of `internal/obs/self.go:87-96`.
   - **What a fix must achieve:** Clone `c.last.Hists` (or start from `c.last.Clone()`) in `selfView()` so `HealthTable.Evaluate` never shares slice backing storage with `c.last`.

## Mutation checks

All checks were run in a scratch copy under `/tmp/zgw-c03-obs-spine-review2/work` against commit `1f9b883c2d7c9c18e32a03437fe7000dab666639`, restoring each mutated/reverted file afterward:

### Repair-round revert-to-fail verification (reverted to pre-repair `f0d2e19` one fix at a time)

| # | Fix reverted | Test(s) run | Result |
| --- | --- | --- | --- |
| R1 | `scripts/csv-assert.sh` (Review 1 F1.1/1.2/1.3, `Adv_7_MissingMetricInOneOfTwoRuns`, `Adv_7_NullNumericValue`) | `TestCSVAssertChecksEveryRunAndEveryField`, `TestAdv_7_MissingMetricInOneOfTwoRuns`, `TestAdv_7_NullNumericValue` | **CAUGHT (FAIL)** |
| R2 | `internal/obs/merge.go` (Review 1 F2, `Adv_9_SumOverflow`, `Adv_12_Undeclared*`) | `TestMergeChecksEveryPointAndHistogramAgainstTheRegistry`, `TestMergeWithoutARegistrySumsAndDoesNotPanic`, `TestMergeRefusesOverflow`, `TestAdv_9_SumOverflow`, `TestAdv_12_Undeclared*` | **CAUGHT (FAIL)** |
| R3 | `internal/obs/csvflush/writer.go` (Review 1 F3, F4, F10, `Adv_6_FirstFlushHeaderCap`) | `TestByteCapCountsTheHeaders`, `TestAsyncWriterNeverBlocksTheCaller`, `TestRowsOfOneFlushAreOrderedBySeqThenInstance`, `TestAdv_6_FirstFlushHeaderCap` | **CAUGHT (FAIL)** |
| R4 | `internal/obs/collector.go:84` (Review 1 F5 / M5: removed `\|\| !e.detail`) | `TestDetailOffDropsHistogramsAtTheEmitter` | **CAUGHT (FAIL)** |
| R5 | `internal/obs/health.go` + `internal/obs/self.go` (Review 1 F6) | `TestCollectorHealthFollowsTheLatestPublishAndRecovers` | **CAUGHT (FAIL)** |
| R6 | `internal/obs/csvflush/reader.go` (Review 1 F7, `Adv_3_CRLFLabelFidelity`, `Adv_4_DuplicateScalarCorruption`, `Adv_4_PhantomTenantCorruption`) | `TestRoundTripRejectsDuplicateAndFabricatedRows`, `TestCarriageReturnsInLabelsRoundTrip`, `TestAdv_3_CRLFLabelFidelity`, `TestAdv_4_DuplicateScalarCorruption`, `TestAdv_4_PhantomTenantCorruption` | **CAUGHT (FAIL)** |
| R7 | `internal/obs/promtext/check.go` + `promtext.go` (Review 1 F8, `Adv_8` wrong-kind) | `TestCheckerRejectsBadExposition`, `TestPointsOfTheWrongKindAreNotExported` | **CAUGHT (FAIL)** |
| R8 | `internal/obs/metric.go` + `collector.go` (`Adv_8` exposure clash, `Adv_3_ZeroMetricWithNonTenantLabels`) | `TestRegistryRefusesExpositionNameClashes`, `TestEveryMetricWithoutATenantLabelIsEmittedAtEveryCollect`, `TestAdv_3_ZeroMetricWithNonTenantLabels` | **CAUGHT (FAIL)** |
| R9 | `internal/obs/obstest/conformance.go` (Review 1 F9) + M7a (`break` after 1st headline in `logline/logline.go:74`) | `TestHeadlineFidelityNeedsEveryHeadlineValue`, `logline/TestConformance` | **CAUGHT (FAIL)** |

### Property mutations

| # | Property mutated | Mutation | Result | Caught by |
| --- | --- | --- | --- | --- |
| M1 | High-water marks merge by max | `internal/obs/merge.go:126`: `return max(a, b), true` -> `return addChecked(a, b)` | **CAUGHT** | `internal/obs` merge tests |
| M2 | Prometheus tenant label bound | `internal/obs/promtext/promtext.go:148`: disabled `len(a.slots) >= a.cfg.TenantMax` break | **CAUGHT** | `internal/obs/promtext` tests |
| M3 | Zero-allocation recording gate | `internal/obs/detail.go:53`: heap-escaping `leakSink = make([]byte, 64)` in `Counter.Inc()` | **CAUGHT** | `TestRecordingAllocatesNothing` |
| M4 | DuckDB guard rejects NULL/empty numeric values | `test/analysis/guard.sql:19`: removed `OR value IS NULL` | **CAUGHT** | `TestCSVAssertChecksEveryRunAndEveryField/empty-value` |
| M5 | CSV persists quiet tenant rows | `internal/obs/csvflush/writer.go:383`: disabled appending absent tracked points | **CAUGHT** | `TestTenantRowsPersistAfterTheTenantGoesQuiet` |
| M6 | Collector health reflects latest publish without lag | `internal/obs/health.go:117`: `t.Evaluate(c.selfView())` -> `t.Evaluate(c.last)` | **CAUGHT** | `TestCollectorHealthFollowsTheLatestPublishAndRecovers` |
| M7 | CSV `RoundTrip` enforces strict row order / uniqueness | `internal/obs/csvflush/reader.go:194`: disabled `run.checkOrder` | **CAUGHT** | `TestRoundTripRejectsDuplicateAndFabricatedRows` |
| M8 | Generated `docs/csv-schema.md` stays in sync with catalog | `internal/obs/catalog/catalog.go:17`: dropped first metric from `obs.SelfMetrics()` | **CAUGHT** | `internal/obs/catalog` tests |

## Release It! checklist

- **Integration points:** Not applicable to client/database sockets here (this chunk adds no socket I/O); file I/O is isolated to `internal/obs/csvflush`, where size-cap checks (now including headers and `run.csv`) and write errors transition the writer to `stopped`, count errors/drops, withhold `run.csv`, and never block the caller in async mode (`writer.go:234-267,416-473`).
- **Blocked threads:** No database connections here; async `csvflush` runs one writer goroutine that exits cleanly on `Close()` (`close(w.queue); w.done.Wait()`, `writer.go:182-190,408-411`), and `simrun`'s publish timers are marked background (`MarkBackground`) so quiesce checks pass with zero pending non-background timers.
- **Slow responses:** Not applicable to client/DB requests here; in async `csvflush`, a slow or blocked filesystem never blocks the caller because `Flush()` uses a non-blocking `select` send with drop-and-count (`writer.go:212-219`), verified under `-race` with both a test gate (`TestAsyncWriterNeverBlocksTheCaller`) and a real FIFO (`TestAdv_6_FullQueueWithBlockedFilesystem`).
- **Cascading failures:** Adapter errors in `Collector.Publish()` are isolated per adapter, counted in `obs.export_errors{reason=<name>}`, and never propagate to the collector or other adapters (`collector.go:296-304`); `Collector.Health()` now immediately reports `StateDegraded` on adapter or emit failure and recovers to `StateOK` once the failure clears (`health.go:90-118`, `self.go:45-97`).
- **Chain reactions:** Not applicable here because this chunk builds observability plumbing and merge utilities, not load-bearing request shedding or multi-instance failover.
- **Dogpile:** Not applicable here because this chunk performs no database dials or reconnect recovery loops.
- **Self-denial attacks:** `simrun` publishes at a fixed virtual-clock interval (default 100 ms) capped at `MaxPublishes = 100,000` (`simrun.go:26,94`); `BenchmarkPublish` measures single-publish cost up to 1,000 tenants (3.95 ms detail-off / 29.37 ms detail-on at 500x) and `BenchmarkFlushFixture` measures sync vs async caller-side flush cost (0.62 ms / 0.19 ms at 500x).
- **Force multiplier:** Not applicable here because this chunk has no automatic control loop adjusting limits or rates.
- **Unbalanced capacities:** Producer/consumer boundary in async `csvflush` uses a bounded channel `QueueCap` (default 64, max `MaxQueueCap = 4,096`) with non-blocking drop-and-count (`writer.go:148-151,168,212-219`), plus `MaxTrackedRows` (1,048,576) and `MaxBytes` (default 1 GiB, enforced across headers, data, and `run.csv`).
- **Scaling effects:** Fleet merge (`internal/obs/merge.go`) sums counters/gauges, takes max for high-water marks, merges histograms by bucket, validates all points/histograms against the registry, and refuses int64 overflow (`ErrMergeOverflow`); `promtext` bounds tenant cardinality to `TenantMax + 1` (default 32 + `other`, max 1,024) and re-buckets histograms to 16 bounds; CSV/JSON retain full per-tenant rows bounded by `obs.csv.max_bytes`.
- **Users:** Per-tenant exporter state is bounded in `promtext` by `TenantMax`, `EvictAfter` quiet-publish eviction, and `MaxTenantLabelLen` (64), folding hostile/excess names into `other`; `logline` caps string values at 32 bytes and total line length at 1,024 bytes; `csvflush` caps tracked keys at `MaxTrackedRows` and bytes at `MaxBytes`.
- **Unbounded result sets:** Not applicable to SQL query replies here; collector/health structures are capped by `MaxSources` (32), `MaxAdapters` (16), `MaxHealthRules` (64), and `maxEmitErrors` (16 per source).
- **Compounds:** Review 1's two compound bugs (async drop-on-full-queue + `Close()` counter decrement; multi-run `csv-assert.sh` + `FROM final GROUP BY run_id` hiding empty runs) are resolved: `Close()` no longer decrements counters, and `guard.sql` + per-run `-- requires:` checks reject any empty, duplicate, missing-metric, or NULL-valued run before SQL assertions execute.

## Observability

- **`Snapshot()` / collector self-metrics present:** Yes — `obs.SelfMetrics()` (`internal/obs/self.go:19-35`) declares 13 metrics under component `obs` (`publishes`, `export_errors`, `emit_errors`, `adapter_failing`, `emit_failing`, `sources`, `adapters`, `csv_flushes`, `csv_flush_errors`, `csv_rows_dropped`, `csv_bytes_written`, `csv_queue_depth`, `prom_tenants_tracked`). Existing chunk 1/2 components (`sim`, `simnet`, `fakepg`, `workload`) are mapped in `internal/obs/simsrc/simsrc.go` without modifying `internal/sim/...`.
- **Every adapter & generated CSV registry:** All 115 declared metrics (`obs` + `simsrc`) appear in `internal/obs/catalog.Registry()` and `docs/csv-schema.md`; `scripts/gen-docs.sh --check`, `TestCSVSchemaDocumentIsGeneratedFromTheCode`, `TestEveryDeclaredMetricIsInTheDocument`, and `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` pass. All four adapters (`jsonstats`, `csvflush`, `logline`, `promtext`) pass `obstest.Conformance` (including the strengthened `Headline` check) and golden tests. One-raw-export import rules pass in `make lint` (`importlint`) and `TestOneRawExportImportRule`.
- **Counters move & conservation:** `TestMappingsMoveEveryMetric` proves every field of `sim`, `simnet`, `fakepg`, and `workload` snapshots reaches an exported metric; `obs` self-counters move in `TestPublishSequenceLastCopyAndAdapterErrors`, `TestUndeclaredMetricIsAnError`, `TestSizeCapIsCountedAndAFailureInTestMode`, `TestAsyncWriterNeverBlocksTheCaller` (now asserting counter monotonicity across `Close()`), and `TestCollectorHealthFollowsTheLatestPublishAndRecovers`. Baseline conservation is asserted in Go (`checkBaselineCounts`) and SQL (`sent-equals-intended.sql`, `database-reads.sql`, `refusals.sql`).
- **CSV-flush assertions:** All 5 SQL files under `test/analysis/<scenario>/` pass over pinned seeds, extra seeds, and the 50-seed sweep; `guard.sql` + `scripts/csv-assert.sh` pass all fail-closed self-tests (`TestCSVAssertFailsClosed`, `TestCSVAssertChecksEveryRunAndEveryField`, `TestCSVAssertFailsWithoutDuckDB`, plus CRLF-label and foreign-`run_id` probes). `csvflush.RoundTrip` passes on all 5 baselines, rejects duplicates/out-of-order/fabricated/mutated-persisted rows, preserves CRLF in labels, and catches both planted-bug canaries (`PlantDropLastBucket`, `PlantStaleValue`).
- **Health rule:** `obs.HealthTable` and `obs.CollectorRule()` evaluate the refreshed `obs` view (`Collector.Health`), degrade immediately on adapter or emit errors, recover on the next clean publish, and serialize to JSON byte-identical to `encoding/json` (verified including `\b` and `\f`).

## What I ran

All commands ran with `PATH=/usr/local/go/bin:/root/.local/bin:$PATH` on commit `1f9b883c2d7c9c18e32a03437fe7000dab666639` (clean repository verified before and after):

- `go build ./... && go vet ./... && go test -count=1 ./...` — **PASS** (22 packages `ok`; `internal/obs` 3.076s, `internal/obs/catalog` 0.015s, `internal/obs/csvflush` 5.743s, `internal/obs/jsonstats` 0.040s, `internal/obs/logline` 0.034s, `internal/obs/obstest` 0.183s, `internal/obs/promtext` 4.863s, `internal/obs/simrun` 37.553s, `internal/obs/simsrc` 0.017s, `agent_work/adversarial/c01-sim-kernel` 6.250s, `agent_work/adversarial/c02-sim-world` 12.925s, `agent_work/adversarial/c03-obs-spine` 17.777s).
- `go test -v -count=1 ./agent_work/adversarial/c03-obs-spine/...` — **PASS** (32 attacks PASS + `TestReplayWorker` PASS, 4 settled attacks SKIP, 0 FAIL).
- `go test -v -count=1 -run '^TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented$' ./internal/obs/simrun` — **PASS** (6.601s; 115 declared metrics, 53 zero in baselines).
- `gofmt -l .` — **PASS** (empty output).
- `scripts/gen-docs.sh --check` — **PASS** (`NOTE: docs/csv-schema.md is current`).
- `make lint` — **PASS** (`detlint` 3s / 11 patterns, `importlint` 0s, `boundlint` 0s, `paniccheck` 0s; removed untracked `agent_work/ci/` artifact afterward).
- `agent_work/chunks/c03-obs-spine/gate/extra.sh` — **PASS** (`DuckDB v1.5.5 (Variegata) d8cdaa33fd`, e2e + runner self-tests with `MG_REQUIRE_DUCKDB=1`, planted-bug canaries, 15 files byte-identical across 2 processes, async writer ×10 under `-race`).
- `go test -race -count=1 ./internal/obs/csvflush/...` — **PASS** (14.050s).
- Extra seeds (`-seed=7`, `-seed=13`, `-seed=99` on `TestScenarioFlush`: 5.458s, 10.641s, 9.270s) + `scripts/csv-assert.sh /tmp/zgw-c03-obs-spine-review2/extra-seeds` — **PASS** (15 run directories, 5 SQL files run, 0 failures).
- `RUN_DIR=/tmp/zgw-c03-obs-spine-review2/sweep50 scripts/sweep.sh -seeds 50 && scripts/csv-assert.sh /tmp/zgw-c03-obs-spine-review2/sweep50` — **PASS** (860 seed runs passed, 0 failures; 10 run directories, 5 SQL files run, 0 failures).
- `go test -run '^$' -bench 'Record|Publish' -benchtime 500x ./internal/obs/` (17.455s) and `go test -run '^$' -bench FlushFixture -benchtime 500x ./internal/obs/csvflush/` (0.920s) — `RecordCounter` 3.04 ns/op, `RecordHistogram` 54.5 ns/op, `RecordDetail/false` 4.43 ns/op (0 held-bytes), `RecordDetail/true` 116.9 ns/op (26,416 held-bytes), `Publish/tenants=1` 40.5 µs / 35.4 µs, `tenants=32` 177.9 µs / 845.2 µs, `tenants=1000` 3.95 ms / 29.37 ms; `FlushFixture` sync 619.9 µs/op / async 186.4 µs/op.
- Revert-to-fail checks (20 sub-runs across all 9 repair groups), 8 property mutations, and 6 edge-case probes (`\b`/`\f` JSON escaping, histogram-clamped-counter vs gauge clash, exact `run.csv` byte cap, persisted histogram bucket mutation, DuckDB with CRLF label and foreign `run_id`) in `/tmp/zgw-c03-obs-spine-review2/work` — all expected failures caught, all edge-case probes passed.


NITS CLOSED by the orchestrator, 2026-11-25: the three MINOR items (Registry.Declare leaving the earlier descriptors of a refused call registered; a missed flush-error count on a zero-flush Close whose open fails; selfView sharing the histogram slice with the last snapshot) falsify no claim, as the reviewer says. The one repair round is used, so they are carried forward as required fixes in the chunk 6 brief (section "Carried from chunk 3"), where the collector is next changed.
