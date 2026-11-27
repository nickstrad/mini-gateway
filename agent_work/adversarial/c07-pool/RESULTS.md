HELD: 33  BROKEN: 14

# Chunk 7 independent property tests

Built on `70d8668086965c278290071e75f8de4c1587f5be`, branch `work/c07-adv`. Counts are named attacks, not unique defects. Existing tests from the interrupted run were retained; the recovery assertion now uses Errorf so its failure does not hide the client-error observation. No product files changed.

All ordinary cases use seed **70927** (fixed boundary cases need no randomness). The long campaign uses pool seeds **1..2000** and PRNG stream 70927. Fuzz probes use input-generator seed 70927 and the product model constant seed 7. The decoder probe enumerates every byte and the listed lengths. Each claim has at least three named attacks.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_CloseOutstanding` | BROKEN |
| 1 | `TestAdv_1_LateOpenAfterShutdown` | held |
| 1 | `TestAdv_1_LongCampaign` | held |
| 1 | `TestAdv_1_SeededChurn` | held |
| 2 | `TestAdv_2_ConcurrentPending` | held |
| 2 | `TestAdv_2_MassLossRate` | held |
| 2 | `TestAdv_2_NonDivisorRate` | BROKEN |
| 3 | `TestAdv_3_ExpiryRefillBound` | held |
| 3 | `TestAdv_3_JitterCollisions` | BROKEN |
| 3 | `TestAdv_3_SubMicrosecondJitter` | BROKEN |
| 4 | `TestAdv_4_LifetimeEqualityCheckout` | BROKEN |
| 4 | `TestAdv_4_ResetDeadlineEquality` | BROKEN |
| 4 | `TestAdv_4_UnreadAfterClean` | held |
| 5 | `TestAdv_5_CheckFailureNoLease` | held |
| 5 | `TestAdv_5_CheckTimeoutNoLeak` | held |
| 5 | `TestAdv_5_LateCheckSuccess` | BROKEN |
| 6 | `TestAdv_6_OldIdleCheckRefusal` | BROKEN |
| 6 | `TestAdv_6_RecentIdleBlackhole` | BROKEN |
| 6 | `TestAdv_6_RecentIdlePeerForgot` | BROKEN |
| 7 | `TestAdv_7_OpenRefusesAndOneTrial` | held |
| 7 | `TestAdv_7_SQLFailureViaCheck` | BROKEN |
| 7 | `TestAdv_7_ZeroReplyDoesNotTrip` | held |
| 8 | `TestAdv_8_AlwaysSameTenant` | held |
| 8 | `TestAdv_8_ReseedSameTenant` | BROKEN |
| 8 | `TestAdv_8_SwitchDuringReset` | held |
| 9 | `TestAdv_9_AllDirtyEnds` | held |
| 9 | `TestAdv_9_CancelDrainResetPermutations` | held |
| 9 | `TestAdv_9_CleanupDeadlineDrain` | BROKEN |
| 10 | `TestAdv_10_CancelStaleID` | held |
| 10 | `TestAdv_10_DuplicateCancelCap` | held |
| 10 | `TestAdv_10_LateCancelAfterExpiry` | BROKEN |
| 11 | `TestAdv_11_SilentStartupStorm` | held |
| 11 | `TestAdv_11_TLSCloseStorm` | held |
| 11 | `TestAdv_11_TLSHandshakeStall` | held |
| 12 | `TestAdv_12_InvalidSigns` | held |
| 12 | `TestAdv_12_MaxInteger` | held |
| 12 | `TestAdv_12_SeededRemainders` | held |
| 13 | `TestAdv_13_CloseDoesNotBlockSibling` | held |
| 13 | `TestAdv_13_CloseNoPeerReply` | held |
| 13 | `TestAdv_13_ShutdownInEveryState` | held |
| 14 | `TestAdv_14_CoreReplay` | held |
| 14 | `TestAdv_14_CrossProcessReplay` | held |
| 14 | `TestAdv_14_FlushedCapacity` | held |
| 14 | `TestAdv_14_ScenarioReplay` | held |
| 15 | `TestAdv_15_ConstantSeedReplay` | held |
| 15 | `TestAdv_15_DecoderAllBytesAndSizes` | held |
| 15 | `TestAdv_15_IndependentPlantWitness` | held |

## Broken cases

Run from the worktree root with `/usr/local/go/bin` on PATH. Each command below fixes the case and uses its compiled-in seed 70927. Failures are intentionally retained.

### TestAdv_1_CloseOutstanding

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_1_CloseOutstanding$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Closing connection must count until closed.

Observed: `adv_test.go:82: replacement dial issued before driver acknowledged closing the old descriptor: physical open+connecting=2, cap=1`

Over-broad claim: the core releases its slot when issuing ActClose; it has no close-acknowledgement API. This case models an outstanding driver close; it does not demonstrate excess physical connections in the shipped synchronous shell. NOTES limit 8 already narrows the contract.

### TestAdv_2_NonDivisorRate

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_2_NonDivisorRate$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: At rate 3, a 999,999,999 ns window permits at most three dials by the stated floor(W*rate/1s)+1 bound.

Observed: `adv_test.go:133: 4 dials in 999999999ns; floor(W*3/1s)+1=3`

Product defect: integer division rounds spacing down to 333,333,333 ns.

### TestAdv_3_JitterCollisions

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_3_JitterCollisions$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Connections opened together do not expire together when jitter is on.

Observed: `adv_test.go:187: jitter on: 21 co-created connections expired together at 1063000000`

Over-broad claim: jitter is sampled from discrete permille offsets, so ties are possible. This is not a violation of the narrower measured default-jitter bound in NOTES; the separate refill-rate case holds.

### TestAdv_3_SubMicrosecondJitter

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_3_SubMicrosecondJitter$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Enabled jitter separates simultaneous lifetimes even at small accepted durations.

Observed: `adv_test.go:187: jitter on: 64 co-created connections expired together at 64000999`

Product boundary defect / over-broad claim: duration/1000 truncates to zero, so all 64 lifetimes are identical. Validation accepts this duration; no minimum effective jitter is enforced.

### TestAdv_4_ResetDeadlineEquality

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_4_ResetDeadlineEquality$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: A reset completing at its deadline has expired and cannot return to idle.

Observed: `adv_test.go:225: reset completed at expired deadline 1003000000; state=idle`

Product defect in the core API: StepDone mutates state before end() expires timers. The engine may mask this by ticking first; this test directly exercises the exported core.

### TestAdv_4_LifetimeEqualityCheckout

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_4_LifetimeEqualityCheckout$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: A connection at its maximum lifetime cannot be lent.

Observed: `adv_test.go:235: expired connection 1 lent exactly at lifetime`

Product defect in the core API: checkout changes idle to lent before expiration, so the idle-only lifetime expiration misses it.

### TestAdv_5_LateCheckSuccess

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_5_LateCheckSuccess$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: A hanging check must end at its deadline, regardless of a late success.

Observed: `adv_test.go:263: hanging check survived deadline: state=idle`

Product defect in the core API: late StepDone clears the checking state before expiration. A prior engine timer may mask this ordering, but the core does not enforce the promised rule.

### TestAdv_6_RecentIdleBlackhole

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_6_RecentIdleBlackhole$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Zero connections lent at death implies zero client errors afterward.

Observed: `integration_test.go:92: zero lent at death, client failures=1 sheds=0 reply=startup[] 0[E:ERROR:08006 ZI]`

Over-broad claim: a recently idle, non-suspect connection is lent without a check. NOTES limit 3 explicitly acknowledges this first-client exposure.

### TestAdv_6_OldIdleCheckRefusal

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_6_OldIdleCheckRefusal$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Idle dead connections are caught without a client error.

Observed: `integration_test.go:81: recovery query failed: startup[] 0[E:ERROR:53M03 ZI]` `integration_test.go:92: zero lent at death, client failures=0 sheds=1 reply=startup[] 0[E:ERROR:53M03 ZI]`

Over-broad claim: the check fails, opens the breaker, and queued work can receive 53M03. NOTES limit 4 acknowledges check-induced refusals. The case also checks recovery after clearing the fault; its observed result is included below.

### TestAdv_6_RecentIdlePeerForgot

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_6_RecentIdlePeerForgot$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Zero connections lent at death implies zero client errors afterward.

Observed: `integration_test.go:81: recovery query failed: startup[] 0[E:ERROR:53M03 ZI]` `integration_test.go:92: zero lent at death, client failures=1 sheds=0 reply=startup[] 0[E:ERROR:08006 ZI]`

Over-broad claim: the first use of a recently idle connection discovers the loss; the resulting breaker can also refuse later work. No real Postgres run is claimed.

### TestAdv_7_SQLFailureViaCheck

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_7_SQLFailureViaCheck$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: SQL errors never open the breaker.

Observed: `adv_test.go:326: SQL ErrorResponse in a check (FailError) opened breaker; claim excludes SQL errors`

Over-broad claim: FailError during an internal check is counted as a connection failure. NOTES decision 5 treats all failed checks as connection failures; the absolute SQL-error exclusion needs that qualification.

### TestAdv_8_ReseedSameTenant

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_8_ReseedSameTenant$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: With reseeding enabled, random-seed residue is removed under every policy.

Observed: `adv_test.go:375: reseed enabled but same-tenant next lease gets no reseed: random seed residue remains`

Over-broad claim: on-switch same-tenant reuse emits neither reset nor reseed. NOTES tier R results explicitly acknowledge within-tenant residue. This test proves the missing reseed action, not real-server random() values.

### TestAdv_9_CleanupDeadlineDrain

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_9_CleanupDeadlineDrain$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: Cleanup completion at the deadline cannot revive an expired cleanup.

Observed: `adv_test.go:387: drain at cleanup deadline resurrected into resetting`

Product defect in the core API: StepDone replaces the expired drain deadline with a new reset deadline before expiration runs.

### TestAdv_10_LateCancelAfterExpiry

`PATH=/usr/local/go/bin:$PATH go test -count=1 -run ^TestAdv_10_LateCancelAfterExpiry$ ./agent_work/adversarial/c07-pool`

Seed: 70927. Expected: A connection with an uncompleted cancel cannot be lent to the next statement.

Observed: `adv_test.go:454: connection reused while cancel 2 has never reported completion; delayed cancel can hit next statement`

Product defect in the core contract: cancellation expiry clears cancelPending while the original lease remains lent, without closing its connection. Subsequent clean release allows reuse without CancelDone. This demonstrates unsafe eligibility if completion is delayed, not an observed misdirected cancel against Postgres; shell deadlines may reduce exposure.

## Verification and limits

- default-run: 32 held, 14 broken, 1 skipped; package elapsed 47.163 seconds.
- long-run: 33 held, 14 broken, 0 skipped; package elapsed 46.149 seconds.

Commands: `go test -json -count=1 ./agent_work/adversarial/c07-pool/...` and `ADV_LONG=1 go test -json -count=1 ./agent_work/adversarial/c07-pool/...`. Exit 1 is expected for the retained failing cases. The three claim-6 cases were rerun after the diagnostic-only assertion change. The default measured run is below the three-minute budget.

No claim was wholly untested. Coverage is bounded: claim 8 checks policy actions, not a new Postgres 17 oracle execution; claim 11 drives startup/TLS events rather than real cryptographic handshakes; claim 13 checks core progress rather than wall-clock shell TLS close latency. Claim 14 checks a custom fragmented scenario, cross-process core replay, Flush.Check and CSV capacity rows; it does not execute all scenario SQL files or establish replay for every possible scenario. Claim 15 uses a Go overlay to access the existing decoder/model without editing internal/pool, and supplies an independent planted-bug witness; it does not measure stochastic fuzz discovery time. Passing attacks establish only the tested cases.

## Handoff

Finished: 47 named attacks, report, default and long runs. The remaining work belongs to the builder/reviewer: resolve the listed product defects or reconcile the broad claims with the documented limits. No product repair, merge, or push was performed. Root STATE.md and INDEX.md were intentionally left unchanged because the brief permits edits only in this directory. Resume by reading this report and rerunning the individual commands; no background run is required. These findings are chunk-local, with no new VM knowledge or general-purpose scripts to preserve elsewhere.
