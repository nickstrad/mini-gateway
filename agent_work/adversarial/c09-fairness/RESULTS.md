HELD: 37  BROKEN: 12

# Fairness independent checks

Base: `27970a5b8f69248ee4131e6d9bb7b2bc17376b0f`. External-package tests, standard-library imports plus this repository's exported APIs; no product file changed. There are 49 named checks, at least three for each of the 15 claims. `evidence.json` preserves each completed check's output and elapsed time.

**Interpretation:** the 12 failures challenge the builder brief's original wording. They are judged **over-broad claims**, not demonstrated violations of the narrowed dispatcher algorithm in NOTES.md. In particular, the original brief does not state its traffic-shape qualifications. These independent schedules vary that shape rather than rerunning the builder's scenarios. NOTES.md already documents several corresponding limits. No claim was wholly untested; coverage limits are stated below. “held” means this check did not find a counterexample, not a proof for all inputs.

| Claim | Attack (named test) | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_LimitShrinksBelowOutstanding` | held |
| 1 | `TestAdv_1_MaximumWeightedArithmetic` | held |
| 1 | `TestAdv_1_WeightedLastSlot` | held |
| 2 | `TestAdv_2_AccountAtLimitChange` | held |
| 2 | `TestAdv_2_EngineEndsMixedTransactions` | held |
| 2 | `TestAdv_2_ShuffledLeaseEnds` | held |
| 2 | `TestAdv_2_StatsOverflowConserves` | held |
| 3 | `TestAdv_3_CancelChangesEligibility` | held |
| 3 | `TestAdv_3_OnlyOverShareWaitingBelowLast` | held |
| 3 | `TestAdv_3_RoundedRemainderIdles` | BROKEN |
| 4 | `TestAdv_4_CancelHeadPreservesTurn` | held |
| 4 | `TestAdv_4_ContinuousBetterGroup` | BROKEN |
| 4 | `TestAdv_4_MoreTenantsThanSlots` | held |
| 5 | `TestAdv_5_QuietAtLeaseBoundary` | BROKEN |
| 5 | `TestAdv_5_QuietEarlyInFlood` | BROKEN |
| 5 | `TestAdv_5_ShortQuietDuringLongRead` | BROKEN |
| 6 | `TestAdv_6_OddPoolRounding` | BROKEN |
| 6 | `TestAdv_6_ShortQuietLongFlood` | BROKEN |
| 6 | `TestAdv_6_VeryLongFloodLeases` | BROKEN |
| 7 | `TestAdv_7_QuietArrivalAtSweep` | held |
| 7 | `TestAdv_7_SynchronizedQuietTenants` | held |
| 7 | `TestAdv_7_TotalQueuePressure` | BROKEN |
| 8 | `TestAdv_8_HeldPeerChangesShare` | BROKEN |
| 8 | `TestAdv_8_LimitReductionWithHeldLeases` | BROKEN |
| 8 | `TestAdv_8_NewTenantShrinksShare` | BROKEN |
| 9 | `TestAdv_9_SingleQueueBurst` | held |
| 9 | `TestAdv_9_SingleQueueHeldTransactions` | held |
| 9 | `TestAdv_9_SingleQueueLateQuiet` | held |
| 10 | `TestAdv_10_ChurnBeyondStatsCap` | held |
| 10 | `TestAdv_10_FullLimitRepeatedPicks` | held |
| 10 | `TestAdv_10_ReuseAfterClose` | held |
| 11 | `TestAdv_11_DuplicateEscapedNames` | held |
| 11 | `TestAdv_11_IntegerOverflowAtLoad` | held |
| 11 | `TestAdv_11_UnknownShareKey` | held |
| 12 | `TestAdv_12_BreakerRefusalWhileHeld` | held |
| 12 | `TestAdv_12_ExpiryWhileHeld` | held |
| 12 | `TestAdv_12_UnpopAfterHeldSelection` | held |
| 13 | `TestAdv_13_CorruptionAfterBalancedHistory` | held |
| 13 | `TestAdv_13_ExtraFairLeaseDetected` | held |
| 13 | `TestAdv_13_MultipleGhostTenantsDetected` | held |
| 13 | `TestAdv_13_RemovingInvariantMakesProbeFail` | held |
| 14 | `TestAdv_14_ByteFragmentReplay` | held |
| 14 | `TestAdv_14_CancellationReplay` | held |
| 14 | `TestAdv_14_FreshProcessesReplay` | held |
| 14 | `TestAdv_14_SeedCampaign` | held |
| 14 | `TestAdv_14_TiedArrivalsAndFlush` | held |
| 15 | `TestAdv_15_EveryByteAndTailLength` | held |
| 15 | `TestAdv_15_HugeSuffixCannotChangePrefix` | held |
| 15 | `TestAdv_15_IndependentSeedsFindTurnPlant` | held |

## Reproduction and observations

Run from `/root/Software/mini-gateway-work/c09-adv`. All deterministic tests use seed **909271**, except the two specified offsets below; the longer replay campaign uses **909271–909302**. Assertions and schedules are fixed, with no wall-clock pass/fail thresholds outside tier R. Commands below select exactly one failing check. Failures are intentionally retained.

### TestAdv_3_RoundedRemainderIdles

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_3_RoundedRemainderIdles$' ./agent_work/adversarial/c09-fairness
```

Expected: No idle capacity below the limit while waiting work exists.

Observed: Limit 3, one lease held by each of two tenants, and both waiting: no request is selected. Floors give each share 1, leaving the last slot idle.

```text
core_test.go:139: inflight=2 limit=3, two waiting, one idle below limit (original claim)
```

Judgment: **over-broad claim**. The last-slot reservation in NOTES.md explicitly permits this; the original “below the limit the share does not bind” needs that qualification.

### TestAdv_4_ContinuousBetterGroup

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_4_ContinuousBetterGroup$' ./agent_work/adversarial/c09-fairness
```

Expected: Every waiting tenant is selected within a bounded number of rounds.

Observed: Tenant a holds two leases at limit 4. Tenant b repeatedly enters the below-share group; a remains waiting after twelve selections with just two active tenants.

```text
core_test.go:215: a still waiting after 12 choices with 2 active tenants (original unqualified claim)
```

Judgment: **over-broad claim**. NOTES.md bounds waiting only within a group and explicitly permits an above-share tenant to wait indefinitely. The original unqualified statement does not.

### TestAdv_5_QuietAtLeaseBoundary

Seed: `909272`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_5_QuietAtLeaseBoundary$' ./agent_work/adversarial/c09-fairness
```

Expected: Quiet outcomes match standalone and quiet p99 is at most twice standalone.

Observed: A short UPDATE arrives during a burst of 40 ms reads. It succeeds in both arms but exceeds the latency ratio; with one quiet sample the empirical p99 is that sample.

```text
scenarios_test.go:77: quiet latency=3.033408ms alone=1.484784ms exceeds 2x; quiet success retained
```

Judgment: **over-broad claim**. The latency protection requires a bound on the residual flooder lease relative to the quiet service time. NOTES.md states its designated scenario makes quiet statements longer than flooder statements; that restricted scenario is not refuted.

### TestAdv_5_QuietEarlyInFlood

Seed: `909273`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_5_QuietEarlyInFlood$' ./agent_work/adversarial/c09-fairness
```

Expected: Quiet outcomes match standalone and quiet p99 is at most twice standalone.

Observed: A short UPDATE arrives during a burst of 40 ms reads. It succeeds in both arms but exceeds the latency ratio; with one quiet sample the empirical p99 is that sample.

```text
scenarios_test.go:77: quiet latency=38.898293ms alone=1.549816ms exceeds 2x; quiet success retained
```

Judgment: **over-broad claim**. The latency protection requires a bound on the residual flooder lease relative to the quiet service time. NOTES.md states its designated scenario makes quiet statements longer than flooder statements; that restricted scenario is not refuted.

### TestAdv_5_ShortQuietDuringLongRead

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_5_ShortQuietDuringLongRead$' ./agent_work/adversarial/c09-fairness
```

Expected: Quiet outcomes match standalone and quiet p99 is at most twice standalone.

Observed: A short UPDATE arrives during a burst of 40 ms reads. It succeeds in both arms but exceeds the latency ratio; with one quiet sample the empirical p99 is that sample.

```text
scenarios_test.go:77: quiet latency=31.942934ms alone=1.490498ms exceeds 2x; quiet success retained
```

Judgment: **over-broad claim**. The latency protection requires a bound on the residual flooder lease relative to the quiet service time. NOTES.md states its designated scenario makes quiet statements longer than flooder statements; that restricted scenario is not refuted.

### TestAdv_6_OddPoolRounding

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -v -run '^TestAdv_6_OddPoolRounding$' ./agent_work/adversarial/c09-fairness
```

Expected: Quiet success exceeds 99%, quiet p99 is at most twice standalone, and the flooder accounts for more than 95% of sheds.

Observed: Thirty short quiet queries run with 48 flood connections, a tenant ring of four, and 30 or 80 ms flooder statements. The reported quiet success or latency threshold fails. Flooder successes and shedding were both nonzero and the shedding-share assertion passed.

```text
real_test.go:107: quiet success=29/30, need >99%
```

Judgment: **over-broad claim**. The real benchmark guarantee is conditional on its traffic and service times, not a guarantee for arbitrary tenant SQL. These checks intentionally change those times and, in one case, the pool size. Real OS scheduling cannot be bit-replayed; fixed inputs and launch-order seed identify the experiment, while the measured numbers vary by host load.

### TestAdv_6_ShortQuietLongFlood

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -v -run '^TestAdv_6_ShortQuietLongFlood$' ./agent_work/adversarial/c09-fairness
```

Expected: Quiet success exceeds 99%, quiet p99 is at most twice standalone, and the flooder accounts for more than 95% of sheds.

Observed: Thirty short quiet queries run with 48 flood connections, a tenant ring of four, and 30 or 80 ms flooder statements. The reported quiet success or latency threshold fails. Flooder successes and shedding were both nonzero and the shedding-share assertion passed.

```text
real_test.go:107: quiet success=29/30, need >99%
real_test.go:120: quiet p99=140.272966ms alone=41.873259ms exceeds 2x
```

Judgment: **over-broad claim**. The real benchmark guarantee is conditional on its traffic and service times, not a guarantee for arbitrary tenant SQL. These checks intentionally change those times and, in one case, the pool size. Real OS scheduling cannot be bit-replayed; fixed inputs and launch-order seed identify the experiment, while the measured numbers vary by host load.

### TestAdv_6_VeryLongFloodLeases

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -v -run '^TestAdv_6_VeryLongFloodLeases$' ./agent_work/adversarial/c09-fairness
```

Expected: Quiet success exceeds 99%, quiet p99 is at most twice standalone, and the flooder accounts for more than 95% of sheds.

Observed: Thirty short quiet queries run with 48 flood connections, a tenant ring of four, and 30 or 80 ms flooder statements. The reported quiet success or latency threshold fails. Flooder successes and shedding were both nonzero and the shedding-share assertion passed.

```text
real_test.go:107: quiet success=25/30, need >99%
```

Judgment: **over-broad claim**. The real benchmark guarantee is conditional on its traffic and service times, not a guarantee for arbitrary tenant SQL. These checks intentionally change those times and, in one case, the pool size. Real OS scheduling cannot be bit-replayed; fixed inputs and launch-order seed identify the experiment, while the measured numbers vary by host load.

### TestAdv_7_TotalQueuePressure

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -v -run '^TestAdv_7_TotalQueuePressure$' ./agent_work/adversarial/c09-fairness
```

Expected: Every quiet tenant keeps its standalone outcomes, observable per tenant in flushed files.

Observed: With 120 synchronized quiet tenants and one flooder, 27 quiet tenants succeed alone but receive 53M01 with the flooder. Both arms are flushed and read back; every quiet tenant has a tenant_served row agreeing with successful replies.

```text
scenarios_test.go:95: tenant=1 alone success, mixed=startup[] 0[E:ERROR:53M01 ZI]
scenarios_test.go:95: tenant=2 alone success, mixed=startup[] 0[E:ERROR:53M01 ZI]
... 27 affected quiet tenants; complete list in evidence.json.
```

Judgment: **over-broad claim**. The 40 staggered quiet tenants in NOTES.md avoid concurrent quiet demand. Extending that guarantee to synchronized demand requires a queue-wait/load qualification. This does not contradict the builder’s specifically staggered scenario.

### TestAdv_8_HeldPeerChangesShare

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_8_HeldPeerChangesShare$' ./agent_work/adversarial/c09-fairness
```

Expected: A tenant holding transactions open never exceeds its share.

Observed: Six leases are obtained through successful pops while the tenant is alone. A new active tenant or a reduced limit then reduces its current share without ending those leases.

```text
core_test.go:237: held leases=6 current share=4: existing transactions exceed stated bound
```

Judgment: **over-broad claim**. The implementation deliberately does not preempt held transactions. NOTES.md says so. The original bound must distinguish new lease admission from existing leases after the active set or limit changes.

### TestAdv_8_LimitReductionWithHeldLeases

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_8_LimitReductionWithHeldLeases$' ./agent_work/adversarial/c09-fairness
```

Expected: A tenant holding transactions open never exceeds its share.

Observed: Six leases are obtained through successful pops while the tenant is alone. A new active tenant or a reduced limit then reduces its current share without ending those leases.

```text
core_test.go:237: held leases=6 current share=2: existing transactions exceed stated bound
```

Judgment: **over-broad claim**. The implementation deliberately does not preempt held transactions. NOTES.md says so. The original bound must distinguish new lease admission from existing leases after the active set or limit changes.

### TestAdv_8_NewTenantShrinksShare

Seed: `909271`.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_8_NewTenantShrinksShare$' ./agent_work/adversarial/c09-fairness
```

Expected: A tenant holding transactions open never exceeds its share.

Observed: Six leases are obtained through successful pops while the tenant is alone. A new active tenant or a reduced limit then reduces its current share without ending those leases.

```text
core_test.go:237: held leases=6 current share=4: existing transactions exceed stated bound
```

Judgment: **over-broad claim**. The implementation deliberately does not preempt held transactions. NOTES.md says so. The original bound must distinguish new lease admission from existing leases after the active set or limit changes.

## Execution and coverage limits

- Default command: `PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/c09-fairness/...`. The final execution completed in 8.291 seconds; its eight failures are expected. The longer checks skip unless `ADV_LONG=1`.
- All longer checks were executed. Reproduce the full campaign with `PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -timeout=10m ./agent_work/adversarial/c09-fairness/...`. PostgreSQL tests require Docker; the harness builds its own gateway and starts/removes its own Postgres 17 container. Real input seed is fixed, but wall-clock observations are necessarily not deterministic, as allowed by PLAN 6.2's tier R thresholds.
- Longer runs were partitioned into real/mutation, CSV/replay, and final per-tenant CSV checks. The snapshot CSV campaign includes 41, 57 and 120 quiet tenants. The final per-tenant checks read actual CSV via `csvflush.Read`, not just the in-memory snapshot. All per-tenant CSV assertions held, including for the 27 correctly recorded sheds.
- Replay checks cover tied arrivals, byte fragmentation, cancellation, three fresh processes, and 32 distinct seeds run twice each. CSV replay compares every file in each run directory byte-for-byte; the 32-seed campaign compares replies and final engine snapshots. This is independent coverage, not an exhaustive rerun of every builder scenario.
- Claim 9 uses the as-built operational meaning of starvation: single-queue latency exceeds ten times standalone, and is worse than fair dispatch. The three schedules cover a read burst, held transactions, and a quiet arrival after a queue-sweep boundary. These do not assert permanent noncompletion.
- Claim 13's extra-lease probes create internally consistent queue accounting that disagrees with the engine; the error must specifically name fairness. An overlay removes only the engine's fairness invariant block and proves that the independent probe then fails. No product file is edited.
- Claim 15 checks the actual private decoder through Go overlays: every byte at lengths 0–8, a 1 MiB suffix against the 96-operation cap, and independently seeded operation sequences that find the turn-restoration plant while passing unplanted. It does not claim a fuzz engine's wall-clock discovery deadline is guaranteed.
- Boundedness checks measure state counts and selection counters rather than wall time. They cover 4,200 sequential tenants, 4,101 simultaneous lease identities beyond the statistics cap, 10,000 held selections, and repeated reuse after close. They do not prove a global CPU bound for every engine event or export adapter.
- Early exploratory runs exposed harness compilation and SQLSTATE-prefix mistakes, which were corrected before counting results. An overly frequent CSV flush made a superseded large-tenant run expensive; it was stopped. Final many-tenant tests publish once per virtual second and still check all tenant totals. Full replay tests publish every 137 ms. One superseded combined run was stopped after the real and invariant checks completed, avoiding duplicate expensive CSV work. Only completed checks from the final applicable executions are counted in `evidence.json`.
- Scratch files are created only inside this directory, in hidden directories excluded from Go's package discovery. Normal completion removes them. No root STATE.md, INDEX.md, production source, module requirement, or external project file was changed. The local STATE.md records this task's state because the brief forbids edits elsewhere.

## Durable findings assessment

No new VM/environment fact needs adding to kb. The durable project finding is the distinction between the original universal wording and the conditional guarantees in NOTES.md: last-slot reservation, group-scoped round robin, no preemption, and service-time/arrival-shape assumptions. This report and the failing tests preserve that evidence within the brief's permitted directory; no outside knowledge file was edited.
