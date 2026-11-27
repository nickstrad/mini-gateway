HELD: 33  BROKEN: 3

Robustness tests of database proxy session reset, against builder tip `9f0378f`.
Seed: `271828` (fixed inputs). No product files changed. All 36 attacks ran; three per each of the twelve claims in the builder’s NOTES.md.

| Claim | Attack | Result |
| --- | --- | --- |
| C1 | `TestAdv_1_CommitMixed` | held |
| C1 | `TestAdv_1_RollbackMixed` | held |
| C1 | `TestAdv_1_SavepointMixed` | held |
| C2 | `TestAdv_2_AlwaysLargeSecret` | held |
| C2 | `TestAdv_2_OnSwitchImmediate` | held |
| C2 | `TestAdv_2_OnSwitchReseed` | held |
| C3 | `TestAdv_3_EmptyIsNotMovement` | BROKEN |
| C3 | `TestAdv_3_ErrorIsNotMovement` | BROKEN |
| C3 | `TestAdv_3_TruncatedIsNotMovement` | BROKEN |
| C4 | `TestAdv_4_InertCursor` | held |
| C4 | `TestAdv_4_InertCustom` | held |
| C4 | `TestAdv_4_InertRole` | held |
| C5 | `TestAdv_5_ResetReordered` | held |
| C5 | `TestAdv_5_ResetRepeated` | held |
| C5 | `TestAdv_5_ResetSequences` | held |
| C6 | `TestAdv_6_NamedResetRepeated` | held |
| C6 | `TestAdv_6_NamedSettingReset` | held |
| C6 | `TestAdv_6_TempPlansOnly` | held |
| C7 | `TestAdv_7_RemoveCursor` | held |
| C7 | `TestAdv_7_RemoveCustom` | held |
| C7 | `TestAdv_7_RemoveRole` | held |
| C8 | `TestAdv_8_RepeatAblation` | held |
| C8 | `TestAdv_8_RepeatIsolation` | held |
| C8 | `TestAdv_8_RepeatPolicies` | held |
| C9 | `TestAdv_9_IsolationOracleHash` | held |
| C9 | `TestAdv_9_NoteReviewHashes` | held |
| C9 | `TestAdv_9_SQLAndScriptHashes` | held |
| C10 | `TestAdv_10_CalibrationByteEquality` | held |
| C10 | `TestAdv_10_MissingInputsFailClosed` | held |
| C10 | `TestAdv_10_RelocatedQuotedWorkingDirectory` | held |
| C11 | `TestAdv_11_EmptyInputs` | held |
| C11 | `TestAdv_11_ExactOutput` | held |
| C11 | `TestAdv_11_UncheckedCatalog` | held |
| C12 | `TestAdv_12_CSVDiscoveryScope` | held |
| C12 | `TestAdv_12_SQLDiscoveryScope` | held |
| C12 | `TestAdv_12_SiteExcludesRecords` | held |

## Failures

### C3: TestAdv_3_EmptyIsNotMovement

Reproduce (seed `271828`):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/lease-lab-port/... -run '^TestAdv_3_EmptyIsNotMovement$'
```

Expected: C3 and deliverable 1c require each column to have been observed differing from its clean value; an unavailable column must not satisfy that positive control.
Observed: with an empty response, all seven counters incremented to 1. The `i >= len(row)` branch in `sessionResult.observe` counts missing evidence as movement.
Classification: defect in the new test oracle, not evidence of an actual proxy isolation failure. The three attacks expose one root cause. An unreadable catalog query can satisfy every catalog-column guard while a separate valid secret read-back satisfies the remaining guard. These direct helper probes demonstrate false accounting; they do not claim that an unmodified live run encountered this error.

### C3: TestAdv_3_ErrorIsNotMovement

Reproduce (seed `271828`):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/lease-lab-port/... -run '^TestAdv_3_ErrorIsNotMovement$'
```

Expected: C3 and deliverable 1c require each column to have been observed differing from its clean value; an unavailable column must not satisfy that positive control.
Observed: with an error response without a data row, all seven counters incremented to 1. The `i >= len(row)` branch in `sessionResult.observe` counts missing evidence as movement.
Classification: defect in the new test oracle, not evidence of an actual proxy isolation failure. The three attacks expose one root cause. An unreadable catalog query can satisfy every catalog-column guard while a separate valid secret read-back satisfies the remaining guard. These direct helper probes demonstrate false accounting; they do not claim that an unmodified live run encountered this error.

### C3: TestAdv_3_TruncatedIsNotMovement

Reproduce (seed `271828`):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v ./agent_work/adversarial/lease-lab-port/... -run '^TestAdv_3_TruncatedIsNotMovement$'
```

Expected: C3 and deliverable 1c require each column to have been observed differing from its clean value; an unavailable column must not satisfy that positive control.
Observed: with a three-field clean row, missing the last four fields, the four absent-column counters incremented to 1. The `i >= len(row)` branch in `sessionResult.observe` counts missing evidence as movement.
Classification: defect in the new test oracle, not evidence of an actual proxy isolation failure. The three attacks expose one root cause. An unreadable catalog query can satisfy every catalog-column guard while a separate valid secret read-back satisfies the remaining guard. These direct helper probes demonstrate false accounting; they do not claim that an unmodified live run encountered this error.

## Execution and limits

- `default.jsonl`: fail, 146.129 seconds.
- `long.jsonl`: fail, 1593.868 seconds.
- `retry.jsonl`: pass, 192.058 seconds.
- The original long campaign hit its eight-minute harness deadline during policy repetition 20, after 19 passes and no assertion failures. This was a harness budget error, not a demonstrated C8 failure. The long-only deadline was increased to 20 minutes (outer process: 21), and that one check was rerun once without changing its assertions or its 20-repetition requirement. The original transcript and superseded outcome are retained; final counts use retry.jsonl. An owned leftover database container and gateway process were cleaned after the interrupted run.
- Default: 21 executed checks and 15 explicitly deferred checks; the recorded package-test duration is below three minutes on this shared host. Go reports package execution time; initial compilation is not included. This is measured evidence, not a universal timing guarantee.
- Long campaign selects C4–C8; it includes 20 consecutive executions of each of the three builder scenarios, plus setup/removal/reset mutations. Each subprocess owns a database and has a deadline. No additional artificial CPU load was injected; other work shares this host.
- Reset mutations use Go overlays and intentionally relax the reset-tag judge to isolate reset effectiveness. They are experiments against the test suite, not deployable product changes. C5 and two C6 checks run the pre-port tests at `98cee87` as comparison controls.
- C9 validates all 44 recorded Git blob hashes. The report script is compared at its copy commit, before its documented relocation edit; C10 tests the relocated version.
- C11’s empty and corrupted input checks are robustness extensions beyond the literal reproduction claim; both results are reported separately and do not reinterpret the archived historical data.
- C12 checks the three named SQL discovery roots, the CSV assertion script, and an actual site build. It does not establish a universal rule for every future gate.
- No numbered claim was left unattacked. The builder brief has no separate numbered claims section; the twelve numbered claims in NOTES.md define the mapping.
- The approximate 15–25-check recommendation conflicts with at least three attacks for each of twelve claims; the latter requires 36 checks.
- Full gate, race suite, and unrelated packages were not run; this brief asks for an independent robustness harness. Failed tests remain failing.
- Canonical outcomes are in default.jsonl, long.jsonl, retry.jsonl and evidence.json. Per-child logs in evidence/ are append-only and may also include the inherited interrupted run.
- kb could not open its configured store (default and global lookup); no search ID was returned, so feedback was unavailable. No environment store or files outside this work area were changed.
