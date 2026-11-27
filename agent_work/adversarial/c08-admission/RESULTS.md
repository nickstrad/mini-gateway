HELD: 46  BROKEN: 6

Verified against build `78510fae022d2b96b53ffc0ffbe129572e98d5fe` on `work/c08-adv`.
Counts are distinct named cases, not distinct defects. The two folded-statistics
failures share one cause. All 15 claims have at least three cases. Existing tests
from the interrupted run were retained unchanged; no product files were edited.

Commands run from this worktree (both exit 1 because the six failing cases remain):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/c08-admission/...
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c08-admission/...
```

The observed runs used `-json` to capture evidence without printing full logs.
Default: 44 held, 6 broken, 2 long cases skipped; package elapsed 60.859 seconds,
under the three-minute limit. Long: 46 held, 6 broken; elapsed 49.104 seconds.
The subprocess helper is excluded from counts. Tests print seed 808271; the long
wire campaign additionally prints each scenario seed 0 through 31. The independent
planted-case generator is seeded 808271. Go's separate actual fuzz campaign is
stochastic; this run found the ordering plant and saved the replay input
`[]byte("20020 200")` (Go fuzz corpus v1, identifier `ed10a113a973752f`).
The deterministic planted-case test provides the repeatable campaign.

| Claim | Test | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_ChurnBeyondCounterCap` | BROKEN |
| 1 | `TestAdv_1_PartialShedDrainMemory` | BROKEN |
| 1 | `TestAdv_1_QuietRemovalAfterMixedExits` | held |
| 2 | `TestAdv_2_FIFOControlHasGrowingWait` | held |
| 2 | `TestAdv_2_ManyTenantTimeout` | held |
| 2 | `TestAdv_2_PhaseLockedTimeout` | held |
| 3 | `TestAdv_3_ControlLawCapacityOne` | held |
| 3 | `TestAdv_3_ControlLawLIFOOldTail` | held |
| 3 | `TestAdv_3_ControlLawMultipleTenants` | held |
| 4 | `TestAdv_4_BurstyTenfoldLoad` | BROKEN |
| 4 | `TestAdv_4_ControlLawOffset` | held |
| 4 | `TestAdv_4_LongServiceTimeout` | held |
| 4 | `TestAdv_4_TargetEqualsService` | held |
| 5 | `TestAdv_5_IdleAfterCancellation` | held |
| 5 | `TestAdv_5_IdleAfterDeadline` | held |
| 5 | `TestAdv_5_IdleAfterService` | held |
| 6 | `TestAdv_6_DeadlineVersusCancel` | held |
| 6 | `TestAdv_6_LargeTimeJumpSweepsAll` | held |
| 6 | `TestAdv_6_UnpopPreservesDeadline` | BROKEN |
| 7 | `TestAdv_7_GlobalHeadTimestampTie` | held |
| 7 | `TestAdv_7_OverflowWhileLIFO` | held |
| 7 | `TestAdv_7_WrappedRingRemoval` | held |
| 8 | `TestAdv_8_ConservationWithUnpopAndExpire` | held |
| 8 | `TestAdv_8_FoldedStatsConservation` | BROKEN |
| 8 | `TestAdv_8_FragmentedShedReplies` | held |
| 8 | `TestAdv_8_RefusedTenantCount` | held |
| 8 | `TestAdv_8_WireControlLawTenantCollision` | held |
| 8 | `TestAdv_8_WireTimeoutTenantCollision` | held |
| 9 | `TestAdv_9_HoldEquality` | held |
| 9 | `TestAdv_9_HoldMinusOne` | held |
| 9 | `TestAdv_9_ObservationFrequency` | held |
| 10 | `TestAdv_10_MaxIntervalAllCounts` | held |
| 10 | `TestAdv_10_SeededLargeIntegerIntervals` | held |
| 10 | `TestAdv_10_SquareBoundaryMonotonic` | held |
| 11 | `TestAdv_11_RecoveryByCancellation` | held |
| 11 | `TestAdv_11_RecoveryByDeadline` | held |
| 11 | `TestAdv_11_RecoveryTimeout` | held |
| 12 | `TestAdv_12_ComparisonParserHostileCorpus` | held |
| 12 | `TestAdv_12_DuplicateRuleLastWins` | held |
| 12 | `TestAdv_12_EncodedComparisonNames` | held |
| 13 | `TestAdv_13_CSVRowOrder` | held |
| 13 | `TestAdv_13_QuotedOutputDirectory` | BROKEN |
| 13 | `TestAdv_13_SeparateSQLProcesses` | held |
| 14 | `TestAdv_14_CrossProcessCoreReplay` | held |
| 14 | `TestAdv_14_FlushRoundTripAndTamper` | held |
| 14 | `TestAdv_14_FragmentedCSVReplay` | held |
| 14 | `TestAdv_14_LongSeedCampaign` | held |
| 14 | `TestAdv_14_MultiTenantWireReplay` | held |
| 15 | `TestAdv_15_DecoderLengthAndSuffix` | held |
| 15 | `TestAdv_15_IndependentCorpusFindsPlant` | held |
| 15 | `TestAdv_15_LongActualFuzzCampaign` | held |
| 15 | `TestAdv_15_SeedCannotDependOnInput` | held |

Failing cases and exact replays (seed 808271 in every case):

**TestAdv_1_ChurnBeyondCounterCap**

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_1_ChurnBeyondCounterCap$' ./agent_work/adversarial/c08-admission/...
```

Expected: Bounded live queues remain valid after churn exceeds the statistics cap.

Observed: After 4,104 historical tenants, four live queues of capacity one produce `admission: tenant "other" depth 4 above 1`.

Judgment: Product defect in snapshot validation: the folded row aggregates several real queues but Check compares it with one tenant capacity. This does not demonstrate any real ring exceeding its cap.

**TestAdv_1_PartialShedDrainMemory**

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_1_PartialShedDrainMemory$' ./agent_work/adversarial/c08-admission/...
```

Expected: Memory remains fixed by the queue caps even when a consumer maintains a constant small shed backlog.

Observed: With capacity 3 and pending sheds fixed at 3, the pending slice backing capacity grows to 10,444.

Judgment: Product defect. TakeShed clears consumed entries but compacts only when completely drained; Push checks the pending count while appending after the consumed prefix. The production engine normally fully drains, which limits exposure but does not satisfy the unqualified core memory claim.

**TestAdv_4_BurstyTenfoldLoad**

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_4_BurstyTenfoldLoad$' ./agent_work/adversarial/c08-admission/...
```

Expected: The unqualified claim requires at least 95% service capacity at tenfold offered load.

Observed: Exactly 10,000 arrivals for 1,000 service slots, clustered into bursts, yield 100 services and 1,180 rule sheds (10% utilization).

Judgment: Over-broad claim. Long arrival gaps exceed the allowed wait, so expired work cannot fill subsequent service slots. The stated benchmark uses spread arrivals; the capacity guarantee needs an arrival-pattern condition. This case does not refute its particular benchmark result.

**TestAdv_6_UnpopPreservesDeadline**

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_6_UnpopPreservesDeadline$' ./agent_work/adversarial/c08-admission/...
```

Expected: An item returned after a failed checkout retains its original enqueue age, with a timely sweep at its interval deadline.

Observed: Enqueued at 1, popped at 9001, then immediately unpopped: original deadline 10001, but Next returns 19001.

Judgment: Product defect in deadline scheduling. Popping the sole item marks the rule empty; Unpop restores the ring entry without restoring rule state. The test fails at Next before its subsequent Sweep assertion, so it specifically proves the delayed timer, not a measured late wire reply.

**TestAdv_8_FoldedStatsConservation**

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_8_FoldedStatsConservation$' ./agent_work/adversarial/c08-admission/...
```

Expected: Valid queues remain acceptable to the published conservation/invariant checker after statistics fold into other.

Observed: Two live capacity-one queues beyond the statistics cap produce `admission: tenant "other" depth 2 above 1`.

Judgment: Product defect, same folded-row validation cause as claim 1. Arithmetic conservation itself is not shown to fail: Check rejects the aggregated depth before reaching the sum comparisons.

**TestAdv_13_QuotedOutputDirectory**

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_13_QuotedOutputDirectory$' ./agent_work/adversarial/c08-admission/...
```

Expected: Identical flushed files yield the same table after relocation to a valid output path.

Observed: Putting the fixture under `operator's runs` makes the table command exit 1 with a SQL parser error near `s`.

Judgment: Product defect in script SQL string quoting. File paths are interpolated into single-quoted SQL without escaping apostrophes. The separate-process and CSV-row-order cases pass on ordinary paths.

Coverage limits: no claim was wholly untested. These cases are finite evidence, not
proof of all inputs. The independent SQL fixture uses load 370 and seed 808271;
it exercises the production query but does not regenerate the builder's committed
seed-1 benchmark table. Wire cases run the simulator, not a real Postgres server.
Claim 10 exercises integer results against big-integer arithmetic; it is not a
separate static proof that every product path avoids floating point. The fuzz
access bridge copies the existing package into temporary directories beneath this
harness and supplies independently generated inputs; it does not modify product
sources. Temporary fixture directories are removed by test cleanup.

Handoff: leave all six failing tests in place for the owner to reconcile. No fix,
merge, push, or independent review of product changes was performed. Durable
findings are recorded here; there are no reusable scripts to extract beyond these
tests. The brief restricts edits to this directory, so root STATE.md, the index,
and docs/knowledge were not changed. Both initial kb searches failed before a
search ID was allocated (no configured location); no hits were used and no feedback
ID was available. No VM configuration was changed.
