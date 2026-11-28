HELD: 51  BROKEN: 6

# Coalescer robustness checks — report assembled by the orchestrator

Target: build `dcf0c48`, branch `work/c11-adv`. Astra (gpt-6-astra) wrote this harness: 57
named attacks over the builder's claims, plus the long campaigns behind `ADV_LONG=1`. Both of
its runs (the first, and the one re-dispatch) were ended by its provider's content filter
after the harness was complete and run, and before this report was written; its checkpoint
is `STATE.md` beside this file ("57 named attacks implemented; default and ADV_LONG campaigns
completed. Six literal-claim failures preserved."). **This report was assembled by the
orchestrator (Fable), not by Astra**: the orchestrator ran the harness unchanged
(`ADV_LONG=1 go test -count=1 -v -timeout 8m ./agent_work/adversarial/c11-coalescer/...`,
seed 110028, logged by every test) and wrote the table and the six entries below from the
tests' own output. No test was edited. Astra's own extra validation (`extra-run.log`):
"clean fuzz passed; cancellation plant caught; all three invariant attacks catch removal".

| Attack | Result |
| --- | --- |
| `TestAdv_1_CancelAcknowledgementGap` | BROKEN |
| `TestAdv_1_FullTableDuplicateBypass` | BROKEN |
| `TestAdv_1_NoShareBypassOverlap` | BROKEN |
| `TestAdv_2_AutoIgnoresSafeReadOnly` | held |
| `TestAdv_2_NoKeyCannotShare` | held |
| `TestAdv_2_OffIgnoresBothFlags` | held |
| `TestAdv_2_WritesAndTransactionsReachDatabase` | held |
| `TestAdv_3_ExactBytesNotNormalized` | held |
| `TestAdv_3_OpaqueHostileText` | held |
| `TestAdv_3_TenantDelimiterCollision` | BROKEN |
| `TestAdv_4_CompletionThenArrivalSameTick` | held |
| `TestAdv_4_RelaxedWindowExplicit` | held |
| `TestAdv_4_StrictHeldCannotStartEarly` | held |
| `TestAdv_5_RepeatedCancelIsIdempotent` | held |
| `TestAdv_5_ShuffledDisconnects` | held |
| `TestAdv_5_ShuffledOwnerHandoffs` | held |
| `TestAdv_6_CancelBeforeResultTie` | held |
| `TestAdv_6_GoneBeforeResultTie` | held |
| `TestAdv_6_ResultBeforeCancelTie` | BROKEN |
| `TestAdv_7_AllCancelWithHeldSuccessor` | held |
| `TestAdv_7_AllGoneWithHeldSuccessor` | held |
| `TestAdv_7_DirtyPoolAndRecovery` | held |
| `TestAdv_7_LateReplyAfterLastDeparture` | held |
| `TestAdv_8_DeadlineOverflow` | BROKEN |
| `TestAdv_8_HandoffCannotExtendDeadline` | held |
| `TestAdv_8_JoinCannotExtendDeadline` | held |
| `TestAdv_9_NoShareEvictionChurn` | held |
| `TestAdv_9_RefusedWaiterCanRetryAfterDeparture` | held |
| `TestAdv_9_ReplyCapAtExactBoundary` | held |
| `TestAdv_10_FailedHeldSuccessorOnlyStartsOnce` | held |
| `TestAdv_10_SQLFailureOnlyOnce` | held |
| `TestAdv_10_TransportErrorOnlyOnce` | held |
| `TestAdv_11_QueueAndConnectionPeaks` | held |
| `TestAdv_11_ShutdownDuringSharedRead` | held |
| `TestAdv_11_TenantHitsAfterOwnerCancellation` | held |
| `TestAdv_12_EmptyResultMatchesDirect` | held |
| `TestAdv_12_OneByteFragmentsMatchDirect` | held |
| `TestAdv_12_StalledFollowerCannotDelayPeers` | held |
| `TestAdv_13_OffThousandPassThrough` | held |
| `TestAdv_13_RelaxedThousandAfterSend` | held |
| `TestAdv_13_StrictThousandAfterSend` | held |
| `TestAdv_14_DefaultStrict` | held |
| `TestAdv_14_HintedFlagOverridesShape` | held |
| `TestAdv_14_InvalidEnumsAndCaps` | held |
| `TestAdv_14_OnSwitchNamesScope` | held |
| `TestAdv_15_SeededReuseCampaign` | held |
| `TestAdv_15_ShutdownClearsHeldAndNoShare` | held |
| `TestAdv_15_StaleFlightIDCannotFinishReusedSlot` | held |
| `TestAdv_16_CoreCheckMustBeCalled` | held |
| `TestAdv_16_OrphanFlightMustBeCaught` | held |
| `TestAdv_16_WaiterGaugeMismatchMustBeCaught` | held |
| `TestAdv_17_CancelAndCapFlush` | held |
| `TestAdv_17_CrossProcessReplay` | held |
| `TestAdv_17_FragmentedReplayAndFlush` | held |
| `TestAdv_18_HostileDecoderBoundaries` | held |
| `TestAdv_18_LeaverPlantCaughtByTargetOracle` | held |
| `TestAdv_18_NoInputHashSeed` | held |

## The six failures

Reproduce each with `PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^<name>$' ./agent_work/adversarial/c11-coalescer`.

- `TestAdv_1_CancelAcknowledgementGap` — "old database read has no cancellation
  acknowledgement; new same-key read may start". After the last waiter of a flight leaves,
  a new request for the same key starts a second read while the first read's cancel is
  still on its way to the database.
- `TestAdv_1_NoShareBypassOverlap` — "oversized read still running; same-key request passed
  unshared, permitting a concurrent database read". A key on the no-share list passes
  straight through while an earlier read of it is still running.
- `TestAdv_1_FullTableDuplicateBypass` — "full table passes two identical reads to database
  without serializing them". With the flight table full, identical reads pass unshared.
- `TestAdv_3_TenantDelimiterCollision` — "observed 0x2; expected 0x1". Two different share
  keys (tenant `a` with text `b\x00c`; tenant `a\x00b` with text `c`) are treated as one
  flight: the table joins tenant and text with a single zero byte.
- `TestAdv_6_ResultBeforeCancelTie` — "leaver at same instant received reply
  (resultFirst=true)". A result and a waiter's leave at the same instant, the result
  applied first: the leaver is still sent the reply.
- `TestAdv_8_DeadlineOverflow` — "future deadline wrapped into past: now=9223372036854775707
  next=-9223372036854774909". A flight deadline computed near the largest instant wraps.

The judgment of each (defect, or a claim worded too broadly) is the orchestrator's and is
written in the repair brief and in `agent_work/chunks/c11-coalescer/ADVERSARIAL.md`.
