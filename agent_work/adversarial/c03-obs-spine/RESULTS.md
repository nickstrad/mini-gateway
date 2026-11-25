HELD: 22  BROKEN: 14

Attacked build `085c617713da53c7f96e5ecac5d524ac82869cfc`; branch `work/c03-adv`.

36 named attacks, three per claim. Counts exclude `TestReplayWorker`, which only runs a scenario in a child process. Tests use package `adv_test`, standard-library test dependencies and existing repository APIs; no product code or module files changed.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_AdapterDependencyBoundary` | held |
| 1 | `TestAdv_1_ComponentDependencyBoundary` | held |
| 1 | `TestAdv_1_SnapshotHasNoLiveReferences` | held |
| 2 | `TestAdv_2_CollectorEmissionPermutation` | held |
| 2 | `TestAdv_2_EqualInputsAfterHostileNoise` | held |
| 2 | `TestAdv_2_PrometheusHistoryIndependence` | BROKEN |
| 3 | `TestAdv_3_CRLFLabelFidelity` | BROKEN |
| 3 | `TestAdv_3_InterleavedInstancesAndQuietRows` | held |
| 3 | `TestAdv_3_ZeroMetricWithNonTenantLabels` | BROKEN |
| 4 | `TestAdv_4_DuplicateScalarCorruption` | BROKEN |
| 4 | `TestAdv_4_HistogramUnitCorruption` | held |
| 4 | `TestAdv_4_PhantomTenantCorruption` | BROKEN |
| 5 | `TestAdv_5_DetailOffCrossProcess` | held |
| 5 | `TestAdv_5_OverloadCrossProcess` | held |
| 5 | `TestAdv_5_TiedPublishCrossProcess` | held |
| 6 | `TestAdv_6_AsyncDiskErrorRecoveryBoundary` | held |
| 6 | `TestAdv_6_FirstFlushHeaderCap` | BROKEN |
| 6 | `TestAdv_6_FullQueueWithBlockedFilesystem` | held |
| 7 | `TestAdv_7_MissingMetricInOneOfTwoRuns` | BROKEN |
| 7 | `TestAdv_7_NullNumericValue` | BROKEN |
| 7 | `TestAdv_7_ReturnedNullViolationRow` | held |
| 8 | `TestAdv_8_CounterGaugeFamilyCollision` | BROKEN |
| 8 | `TestAdv_8_HistogramGaugeSeriesCollision` | BROKEN |
| 8 | `TestAdv_8_HostileTenantCampaign` | held |
| 9 | `TestAdv_9_NegativeHighWaterAndEmptyIdentity` | held |
| 9 | `TestAdv_9_SparseBucketPartitions` | held |
| 9 | `TestAdv_9_SumOverflow` | BROKEN |
| 10 | `TestAdv_10_CounterExtremeRecordingAllocations` | held |
| 10 | `TestAdv_10_DisabledConstructionAndSnapshots` | held |
| 10 | `TestAdv_10_HistogramClampAllocations` | held |
| 11 | `TestAdv_11_ConnectionPeaksAtIrregularPublishes` | held |
| 11 | `TestAdv_11_IdenticalReadCountsAtTiedPublishes` | held |
| 11 | `TestAdv_11_OverloadCountsWithDetailAblation` | held |
| 12 | `TestAdv_12_RuntimeDeclarationDocumentationGap` | BROKEN |
| 12 | `TestAdv_12_UndeclaredHistogramMerge` | BROKEN |
| 12 | `TestAdv_12_UndeclaredSingletonMerge` | BROKEN |

## Reproduction and execution evidence

Run from `/root/Software/mini-gateway-work/c03-adv`:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/c03-obs-spine/...
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c03-obs-spine/...
PATH=/usr/local/go/bin:$PATH go test -race -count=1 -run '^TestAdv_6_' ./agent_work/adversarial/c03-obs-spine/...
```

Default: 16.894s package elapsed, well under three minutes. Long: 69.190s; the same 22 held / 14 broken. Race subset: 0.056s, two held / one broken (the byte cap); no race detector findings. All three commands intentionally exit 1 because failing attacks remain present. Machine-readable evidence: [default-run.jsonl](default-run.jsonl), [long-run.jsonl](long-run.jsonl), [race-run.jsonl](race-run.jsonl). Logs were captured with `-json`.

Base seed is **730031**, fixed in source and printed by every attack. Structural and fault-ordering cases use no random input. Cross-process scenarios use 730031, 730032 and 730033, and vary GOMAXPROCS between 1 and 2. SQL baseline attacks use seed 730042 by default; ADV_LONG expands to 730042–730049 for each of three scenarios (24 runs), alternating detail on/off. Every SQL baseline comparison includes a deliberately wrong exact-count canary which must fail. DuckDB output records version v1.5.5.

The long and race logs precede the final addition of a documentation freshness control to the runtime-declaration attack. The final default run includes that control; all reported verdicts are unchanged.

## Broken attacks

Each command below reproduces one attack with seed **730031** (fixed, no environment seed required). Expected and observed behavior are separated from the severity judgment. Ten findings are judged product defects and four over-broad claims; all fourteen remain failing tests.

### TestAdv_2_PrometheusHistoryIndependence

Expected: Equal instance snapshots give byte-identical adapter output.

Observed: With TenantMax=1 and EvictAfter=1, exporting the identical snapshot twice changes tenant="tenant" to tenant="other".

Judgment: Over-broad claim: eviction is intentionally history-dependent; replay determinism should quantify equal initial adapter state and equal input history, not snapshot alone.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_2_PrometheusHistoryIndependence$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_3_CRLFLabelFidelity

Expected: Full tenant names survive CSV and the exact final round trip.

Observed: Tenant a\r\nb becomes a\nb in encoding/csv Read; the metric and histogram cannot be found under the original label.

Judgment: Product defect: no public label restriction forbids CRLF. The Go reader normalizes CRLF within quoted fields.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_3_CRLFLabelFidelity$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_3_ZeroMetricWithNonTenantLabels

Expected: A registered metric without a tenant label appears every flush, even at zero.

Observed: pool.refusals declares only reason, emits nothing, and Collect/Publish succeeds; the CSV contains no refusals row.

Judgment: Over-broad claim: NOTES explicitly narrows presence enforcement to entirely unlabelled descriptors. The registry has no enum of reason values from which to generate missing rows. Either strengthen enforcement or state the narrower guarantee.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_3_ZeroMetricWithNonTenantLabels$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_4_DuplicateScalarCorruption

Expected: Every final scalar row equals the snapshot exactly; corruption is rejected.

Observed: Duplicating the final tenant counter row is accepted by RoundTrip, although a SQL sum now counts 14 rather than 7.

Judgment: Product defect: key lookup compares a matching value but never enforces scalar row uniqueness.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_4_DuplicateScalarCorruption$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_4_PhantomTenantCorruption

Expected: The final read-back matches every metric, with only legitimately persisted tenant history allowed.

Observed: Adding tenant zz_never_observed with value 999 to the only flush is accepted by RoundTrip.

Judgment: Over-broad claim: NOTES allows arbitrary extra tenant rows as a persistence shortcut. This cannot prove exactness or distinguish persisted data from fabricated data.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_4_PhantomTenantCorruption$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_6_FirstFlushHeaderCap

Expected: The configured byte cap bounds the run directory; a flush exceeding it drops and counts.

Observed: MaxBytes=200, async mode: metrics.csv plus histograms.csv occupy 257 bytes; BytesWritten also reports 257.

Judgment: Product defect: the initial flush capacity check excludes the headers written by open().

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_6_FirstFlushHeaderCap$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_7_MissingMetricInOneOfTwoRuns

Expected: The assertion runner fails when a required metric is absent from a run.

Observed: Root containing complete runs a and b passes even though b has no pool.requests; the existence query aggregates all runs together. The positive single-run control passes first.

Judgment: Product defect: required metric presence must be checked per run, not just somewhere in the input root.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_7_MissingMetricInOneOfTwoRuns$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_7_NullNumericValue

Expected: Schema-v1 integer values and fail-closed assertions cannot silently lose a required value.

Observed: Replacing the required counter value with an empty field passes. DuckDB reads NULL and value <> 7 returns no violating row. The unmodified positive control passes.

Judgment: Product defect: schema validation checks version/header but not nullability of required numeric cells. SQL three-valued logic turns corrupt evidence into a pass.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_7_NullNumericValue$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_8_CounterGaugeFamilyCollision

Expected: Every accepted metric declaration produces valid Prometheus exposition.

Observed: Counter x=2 and gauge x_total=-3 are both valid declarations. They collapse to counter mg_pool_x_total=-1; the exposition checker rejects the negative counter.

Judgment: Product defect: the registry/exporter does not reserve or disambiguate generated counter suffixes. This also silently loses separate values.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_8_CounterGaugeFamilyCollision$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_8_HistogramGaugeSeriesCollision

Expected: Prometheus output always parses, for accepted descriptors.

Observed: Histogram wait and gauge wait_count with the same labels generate duplicate mg_pool_wait_count series (7 and 91); the checker rejects the output.

Judgment: Product defect: generated histogram suffixes can collide with ordinary declared metric names.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_8_HistogramGaugeSeriesCollision$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_9_SumOverflow

Expected: Merging counters sums their nonnegative values.

Observed: Merging MaxInt64 and 1 succeeds with -9223372036854775808.

Judgment: Product defect: silent wrap makes a negative counter; an explicit overflow error would satisfy this attack. The current guarantee has no documented arithmetic-range precondition.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_9_SumOverflow$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_12_RuntimeDeclarationDocumentationGap

Expected: A metric cannot be exported without being declared and documented; generated-document validation detects omissions.

Observed: A newly registered engine.adversarial_unindexed counter publishes successfully while catalog.Markdown still equals the committed document and omits that metric.

Judgment: Over-broad claim: the catalog is a static assembly independent of runtime source registration. A source-to-catalog completeness check or a restriction to catalog-listed production sources is needed to justify the absolute wording.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_12_RuntimeDeclarationDocumentationGap$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_12_UndeclaredHistogramMerge

Expected: The declared-metric rule applies to histogram exports too.

Observed: Two not_declared histograms merge successfully with bucket counts 6 and 8 despite a nonempty registry.

Judgment: Product defect: mergeHists never consults the supplied registry. As above, this is the merge/export seam.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_12_UndeclaredHistogramMerge$' ./agent_work/adversarial/c03-obs-spine
```

### TestAdv_12_UndeclaredSingletonMerge

Expected: Registry-aware merging refuses undeclared metrics before export.

Observed: A single not_declared counter survives Merge with a nonempty registry and renders in JSON.

Judgment: Product defect: declaration lookup happens only when combining duplicate keys. This exercises the public merge/export seam, not collector emission.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -run '^TestAdv_12_UndeclaredSingletonMerge$' ./agent_work/adversarial/c03-obs-spine
```

## Coverage and limits

No claim was left unattacked. Attacks were derived from the brief and public contracts, not copied from builder tests. Structural checks inspect actual imports and snapshot field types; permutation tests randomize source emission; replay tests use separate test processes, tied/manual flushes and irregular 337 ms intervals; SQL checks compare independent simulator observations to flushed data with incorrect-count controls. CSV faults use actual filesystem behavior (/dev/full and a FIFO), not private product injection hooks. Corruption tests first require an unmodified round trip to pass.

Tier R was not run: these claims concern local export/validation machinery and deterministic Tier S evidence, not database protocol fidelity. No real-database attacks or unguarded database access were added. The nonblocking attacks cover write faults and a full queue; they do not establish a universal latency bound for arbitrarily large snapshots. Import checks establish present dependencies, not every possible future lint mutation. Synthetic merge snapshots deliberately exercise the public API boundary beyond the collector happy path.

Artifacts and handoff state are confined to this directory as required. STATE.md at repository root and agent_work/INDEX.md were deliberately left untouched; [STATE.md](STATE.md) carries this task’s handoff. Findings suitable for future project knowledge are the concrete reproducers above; no separate docs/knowledge or work-scripts edits were permitted by the brief.
