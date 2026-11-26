HELD: 22  BROKEN: 2

**This report was assembled by the orchestrator (Fable), not written by the adversary.**
Astra wrote the harness in this directory (`adv_test.go`, 24 attacks over the seven claims,
at least three per claim; `long-fuzz.sh`) and ran its long campaign, but its run was stopped
twice by its provider's content filter before it wrote a report (runs `cr01-adv`, 2026-11-26;
the second was the one re-dispatch the protocol allows). Astra never declared the harness
finished. The counts below are from the orchestrator running Astra's tests unchanged at the
build tip `1179f92`: `go test -count=1 ./agent_work/adversarial/cr01-shrink-fuzz-rules/...`.
The judgements "defect or over-broad claim" are the orchestrator's.

| Claim | Attack | Result |
|---|---|---|
| 1 | `TestAdv_1_FrozenImplementation`, `TestAdv_1_DeletionTieOrder`, `TestAdv_1_ExistingGoldenContracts` | held |
| 2 | `TestAdv_2_CoupledParity`, `TestAdv_2_DeletionUnlocksLowering`, `TestAdv_2_NegativeFloorsAndIdentity`, `TestAdv_2_LongSeedCampaign` | held |
| 3 | `TestAdv_3_FullWidthCallBudget`, `TestAdv_3_ExhaustiveBooleanLandscape`, `TestAdv_3_EmptyAndBelowFloorNoCalls` | held |
| 4 | `TestAdv_4_MutatingPredicateIsolation`, `TestAdv_4_AliasedFloorsAndReturnedStorage`, `TestAdv_4_ProcessReplay` | held |
| 5 | `TestAdv_5_AllKindsRoundTrip`, `TestAdv_5_EveryByteCorruptionCanonical` | held |
| 5 | `TestAdv_5_UnboundedScheduleClaim` | BROKEN |
| 6 | `TestAdv_6_RealTSVColumns`, `TestAdv_6_GateSeedSweepCounts`, `TestAdv_6_FailedTargetWithGoodRate`, `TestAdv_6_NoProgressFailsClosed` | held |
| 6 | `TestAdv_6_UnreadableAfterProgress` | BROKEN |
| 7 | `TestAdv_7_WorldAndToySeedDataflow`, `TestAdv_7_WireAndFakeSeedDataflow`, `TestAdv_7_InventoryAndHiddenHashImports` | held |

## BROKEN: claim 5 — a schedule of 1,025 faults has no text form

`go test -count=1 -run TestAdv_5_UnboundedScheduleClaim ./agent_work/adversarial/cr01-shrink-fuzz-rules/`
The claim says the text form round-trips every schedule. `FormatSchedule` refuses a
schedule longer than `MaxScheduleLen` (1,024). Judgement: over-broad claim. The cap is a
stated limit in NOTES.md and bounded input is the house rule; the claim should say "every
schedule of at most `MaxScheduleLen` faults".

## BROKEN: claim 6 — an unreadable last line is accepted when an earlier line had a rate

`go test -count=1 -run TestAdv_6_UnreadableAfterProgress ./agent_work/adversarial/cr01-shrink-fuzz-rules/`
Go's output `fuzz: elapsed: 1s, execs: 300 (300/sec)` followed by a last progress line that
cannot be read makes `fuzz.sh` report 300 executions from the earlier line and exit 0.
Judgement: product defect against "fails closed when it cannot read a rate" — the rate is
taken from a stale line.

## The builder's open flake: `FuzzWorldSchedule` "hung or terminated unexpectedly"

Astra's long campaign (`ADV_LONG=1 ./long-fuzz.sh`, log `long-FuzzWorldSchedule.log`)
reproduced it in a 20-second run and saved the input
`reproduced-FuzzWorldSchedule/fuzz/FuzzWorldSchedule/7edb6a746e048b6e`. The orchestrator
replayed that input alone four times (`go test -count=1 -run
'FuzzWorldSchedule/7edb6a746e048b6e' ./internal/sim/world` with the file copied into the
package's `testdata`): it PASSES every time, taking 11 to 13 seconds for the one execution,
110 MB peak. So the input does not fail the property; it is slow, and a fuzz run whose
time ends while a worker is inside such an execution is reported by Go as a hung worker.
The rate in the log falls from 464/s to 47/s before the stop, which fits. Not a
nondeterminism. It is a defect of the target all the same: one input can cost 13 seconds,
which is why this target sits near the 100/s floor, and it makes G5 fail at random.

## Not attacked

Nothing was left unattacked by claim. The harness has no attack on the gate canaries.
