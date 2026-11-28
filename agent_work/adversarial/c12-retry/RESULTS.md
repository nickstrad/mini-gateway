HELD: 43  BROKEN: 5

Target: build `401f883f9b51534cf9cc3a69891cb99ddbce8b3a`, branch `work/c12-adv`.
All 15 claims have at least three distinct named attacks. Counts include the two
ADV_LONG attacks, both run successfully. The default suite has 41 held, five broken,
and two skipped long checks. BROKEN tests intentionally remain failing.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_BinaryDefaultAndExplicitZero` | held |
| 1 | `TestAdv_1_EngineDefaultAcrossRequests` | held |
| 1 | `TestAdv_1_ZeroCoreHostileOffers` | held |
| 2 | `TestAdv_2_LargeAttemptIndices` | held |
| 2 | `TestAdv_2_OnceStateDoesNotLeakToNextRequest` | held |
| 2 | `TestAdv_2_RepeatedRefusalAndStaleEvents` | held |
| 3 | `TestAdv_3_AcceptedOffsetsAcrossMessage` | held |
| 3 | `TestAdv_3_RefusalAfterPartialReply` | held |
| 3 | `TestAdv_3_SilenceAndCloseWithoutReport` | held |
| 4 | `TestAdv_4_LongSeededCuts` | held |
| 4 | `TestAdv_4_OffEveryResponseByteCut` | held |
| 4 | `TestAdv_4_OnEveryResponseByteCut` | held |
| 4 | `TestAdv_4_TransactionRefusalAfterPriorWrite` | held |
| 5 | `TestAdv_5_CapAtMaximumAndFractionalDeposits` | held |
| 5 | `TestAdv_5_CarriedCreditExceedsWindowFloor` | BROKEN |
| 5 | `TestAdv_5_QuietHourRetainsBurstCredit` | BROKEN |
| 6 | `TestAdv_6_BreakerChangesWhileStatementActive` | held |
| 6 | `TestAdv_6_BreakerReopensBeforeQueuedRetryRuns` | BROKEN |
| 6 | `TestAdv_6_ClosedOpenClosedBudgetPreserved` | held |
| 7 | `TestAdv_7_ClientLeavesBeforeReplacementReady` | held |
| 7 | `TestAdv_7_DeadlineTiedWithNewConnection` | held |
| 7 | `TestAdv_7_ReplacementEventExactlyAtDeadline` | held |
| 7 | `TestAdv_7_WaitingRetryConsumesQueueSlot` | BROKEN |
| 8 | `TestAdv_8_OffBurstAgainstAblation` | held |
| 8 | `TestAdv_8_OnBurstAgainstAblation` | held |
| 8 | `TestAdv_8_ZeroByteOutageBudgetAndRecovery` | held |
| 9 | `TestAdv_9_AllVetoCombinationsConserve` | held |
| 9 | `TestAdv_9_ExhaustionThenExactReplenishment` | held |
| 9 | `TestAdv_9_ShutdownAndLateDriverReports` | held |
| 10 | `TestAdv_10_ActualRetryRowsMatchContract` | held |
| 10 | `TestAdv_10_ErrorRowsRejectUnsafeCombinations` | held |
| 10 | `TestAdv_10_MatrixExplicitNeverPaths` | BROKEN |
| 11 | `TestAdv_11_AblationDuplicatesAnAcknowledgedWrite` | held |
| 11 | `TestAdv_11_BinaryRejectsPlantInjection` | held |
| 11 | `TestAdv_11_BinaryRejectsPolicyAliases` | held |
| 12 | `TestAdv_12_ConfigFloorDivisionAtBoundary` | held |
| 12 | `TestAdv_12_ExtremeInvalidKeys` | held |
| 12 | `TestAdv_12_RemainderFleetPreservesRatio` | held |
| 13 | `TestAdv_13_EngineRejectsBrokenWithdrawal` | held |
| 13 | `TestAdv_13_EngineRejectsOfferMismatch` | held |
| 13 | `TestAdv_13_ShutdownAtEachRetryPhase` | held |
| 14 | `TestAdv_14_CrossProcessReplay` | held |
| 14 | `TestAdv_14_FlushExhaustionAndFractionalLevels` | held |
| 14 | `TestAdv_14_ReplayActionsAndCSVBytes` | held |
| 15 | `TestAdv_15_DecoderHostilePrefixesAndHugeSuffix` | held |
| 15 | `TestAdv_15_FuzzInputCannotChooseAHashedSeed` | held |
| 15 | `TestAdv_15_LongActualFuzzTargetFindsPlant` | held |
| 15 | `TestAdv_15_WithdrawalPlantCaughtOnVetoSequences` | held |

## Reproductions and findings

Run each command from the worktree root. Every short attack uses seed **12012026**;
the deterministic boundary cases need no random choices. Seeds and replay commands
are logged by the tests. `default.log` contains the observed failure messages;
`long.log` records every extended-run verdict and the actual planted-fault output.

### Claim 5 — carried credit exceeds a window's floor

```
/usr/local/go/bin/go test -count=1 -run '^TestAdv_5_CarriedCreditExceedsWindowFloor$' ./agent_work/adversarial/c12-retry/...
```

Seed: 12012026. Expected: in ANY window, ten times retries is at most first
attempts plus floor in tenths. After 990 previous deposits with cap 100 and floor 1,
a new window has 20 first attempts and 20 retries: **200 > 20 + 10**.
Judgment: **over-broad claim**. The notes explicitly substitute carried level (up
to the cap) for the floor in arbitrary windows. This ordinary token bucket satisfies
the bound from startup, but not the bound promised for every window. This test
preserves the original claim's oracle instead of adopting the narrower notes.

### Claim 5 — quiet time retains burst credit

```
/usr/local/go/bin/go test -count=1 -run '^TestAdv_5_QuietHourRetainsBurstCredit$' ./agent_work/adversarial/c12-retry/...
```

Seed: 12012026. Expected: the quiet-hour claim and window bound prevent old credit
funding a burst beyond ten percent plus floor. After filling the bucket and advancing
one virtual hour without attempts, 15 new attempts receive 15 retries with floor 1:
**150 > 15 + 10**. Judgment: **over-broad claim**, related to the preceding finding.
The cap discards excess deposits; it does not expire stored credit with time. No
wall-clock delay is used. A bounded cap-sized burst is still possible after silence.

### Claim 6 — breaker opens between grant and dispatch

```
/usr/local/go/bin/go test -count=1 -run '^TestAdv_6_BreakerReopensBeforeQueuedRetryRuns$' ./agent_work/adversarial/c12-retry/...
```

Seed: 12012026. Expected: “never when the breaker is open” includes a retry waiting
for a replacement database connection. The breaker is closed at the zero-byte
refusal, then opens before the replacement handshake. Observed: **one retry Query
is emitted while Open() is true**. Judgment: **product defect at the exported breaker
seam**. The supplied breaker implements both Allow and the pure Open method; it is
not the documented unsupported case lacking Open. The test does not establish that
the pool-core breaker has the same behavior: its queue-flush integration may prevent
this schedule. The retry grant remains counted once; this failure concerns execution
after the breaker changes, not the initial grant decision.

### Claim 7 — retry occupies a queue slot

```
/usr/local/go/bin/go test -count=1 -run '^TestAdv_7_WaitingRetryConsumesQueueSlot$' ./agent_work/adversarial/c12-retry/...
```

Seed: 12012026. Expected from the literal claim: a waiting retry occupies neither a
queue slot nor a connection. Observed after zero-byte refusal: **pending=1, queued=1,
lent=0**. Judgment: **over-broad claim**. The binding one-waiting-place instruction
and builder notes deliberately put retries in the tenant admission queue. The
implementation follows that instruction; “no queue slot” cannot simultaneously hold.
The deadline, client departure, and no-held-connection checks pass separately.

### Claim 10 — row metadata contradicts explicit no-retry paths

```
/usr/local/go/bin/go test -count=1 -run '^TestAdv_10_MatrixExplicitNeverPaths$' ./agent_work/adversarial/c12-retry/...
```

Seed: 12012026. Expected: a row describing deadline equality or a loss that opens
the breaker is classified non-retried. Observed: both rows explicitly say “no retry”
in Client but return **Retried()=true**. Judgment: **product documentation/metadata
defect**, not evidence of an actual unsafe retry on these paths. Retried() only
checks error row, Reached, and transaction state. The rendered yes-cell includes
conditional boilerplate requiring a closed breaker and time before the deadline;
those conditions are impossible in these two specific rows. Readers treating that
cell as generic eligibility may find it conditional rather than contradictory, but
it does not give the specific path's no-retry answer claimed by the complete matrix.

## Validation and scope

- Default command: `/usr/local/go/bin/go test -count=1 ./agent_work/adversarial/c12-retry/...`.
  It finishes well under three minutes; see the final elapsed time in `default.log`.
  Exit 1 is expected for the five retained failures.
- Extended command is recorded in `long.log`: 48 executed, 43 held, five broken,
  no skips, package elapsed 2 seconds on this run. The 2,000-cut campaign uses engine
  seeds 12012026 through 12014025, with offsets selected by seeded PRNG 12012026.
- The actual FuzzRetryOps target detects `retry-withdraw-first` in corpus seed #2:
  “10 tenths withdrawn for 0 retries”. That test builds and executes the target;
  it does not merely inspect the plant source. No extended fuzz coverage claim is made.
- `go vet ./agent_work/adversarial/c12-retry/...` passed; `vet.log` is empty.
  All test files were formatted with gofmt.
- Tests use the exported engine/core/contract APIs and standard-library helpers;
  there is no new module requirement. A separate wire driver injects faults and
  tracks replies. The application ledger models full-query acceptance; these checks
  are not a real Postgres run or a replacement for the builder's end-to-end suite.
  The storm checks use independent seeded schedules and virtual-time bounds, including
  budget exhaustion and recovery, rather than importing the builder's scenarios.
- Three snapshot/flush failures in the interrupted run were harness errors: RoundTrip
  takes the final snapshot for each instance, not all historical snapshots. Fixed the
  call and added exact comparisons of EVERY flush's metric points against its snapshot.
  All three replay/flush tests now pass, including separate child processes.
- The decoder is extracted verbatim as the object under test, with an AST body equality
  guard. Boundary inputs and assertions are independent; builder test cases are not copied.
- No claim was left unattacked. Coverage is bounded, not a proof over all event schedules.
  Configuration exclusion checks exercise the binary's config parser, not a live binary.
  The invariant checks inject corrupt snapshots through the exported policy seam and
  require Engine.Invariants to reject them; no product file was edited for mutation.

## Handoff

Only this directory was changed. Product code and agent_work/INDEX.md are untouched.
No merge or push was performed. The original budget and queue wording requires an
owner decision; the tests intentionally retain those literal expectations. A local
STATE.md records completion because the brief forbids modifying the project-root state.
No additional reusable script or separate project-knowledge change is needed beyond
this harness and its findings, and the allowed edit scope excludes docs/knowledge.

The environment knowledge lookup was unavailable: local, --global, and explicit-root
kb searches all failed before creating a numbered search (missing .kb/kb.toml). No
search hit was acted on and there was no search ID to attach feedback to. No corpus
files were opened and no environment configuration was changed.
