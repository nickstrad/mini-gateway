HELD: 29  BROKEN: 5

Attacked build `66e52dc27d8823a12481d67a7d7391818d766625`. Counts are named attacks, not claims, seeds, or repeated executions. All 11 claims have at least three independent named attacks. The default suite intentionally exits 1.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_EmptyInsideTransaction` | held |
| 1 | `TestAdv_1_ErrorAfterCommitBoundary` | held |
| 1 | `TestAdv_1_RollbackThenBegin` | held |
| 2 | `TestAdv_2_AbortedTransactionEmptyQuery` | held |
| 2 | `TestAdv_2_ImplicitRollbackOfDeleteInsert` | held |
| 2 | `TestAdv_2_SnapshotAcrossDeleteReinsert` | held |
| 2 | `TestAdv_2_StatementLimitErrorAbortsTransaction` | BROKEN |
| 3 | `TestAdv_3_DeleteReinsertCommitOrder` | held |
| 3 | `TestAdv_3_FullTableCommitMustNotLogPhantom` | BROKEN |
| 3 | `TestAdv_3_RingWrapPreservesSequence` | held |
| 4 | `TestAdv_4_CancelAtFullCapacity` | held |
| 4 | `TestAdv_4_DeadEndsDoNotStealReleasedSlot` | held |
| 4 | `TestAdv_4_StartupReservations` | held |
| 5 | `TestAdv_5_FreezeClearWithQueuedBackends` | held |
| 5 | `TestAdv_5_RefuseRestartClearInterleaving` | held |
| 5 | `TestAdv_5_StallHitsAlreadyServing` | BROKEN |
| 6 | `TestAdv_6_ClearArmedHalfMessage` | held |
| 6 | `TestAdv_6_ResetQueuedClientReleasesWaiter` | held |
| 6 | `TestAdv_6_StopReadThenCancelThenClear` | held |
| 7 | `TestAdv_7_BacklogCapCountsEveryArrival` | held |
| 7 | `TestAdv_7_LatencyIncludesStartupWait` | held |
| 7 | `TestAdv_7_SendWindowIndependentOfService` | BROKEN |
| 8 | `TestAdv_8_CancelQueuedBackendOnly` | held |
| 8 | `TestAdv_8_OldKeyAfterBackendReplacement` | held |
| 8 | `TestAdv_8_RepeatCancelDoesNotPoisonNextStatement` | held |
| 9 | `TestAdv_9_CrossProcess` | held |
| 9 | `TestAdv_9_ExtremeSeedBaselineReplay` | held |
| 9 | `TestAdv_9_RandomFaultRecovery` | held |
| 10 | `TestAdv_10_500ConnectionsUnpinnedSeeds` | held |
| 10 | `TestAdv_10_BurstUnpinnedSeeds` | held |
| 10 | `TestAdv_10_IdenticalReadsUnpinnedSeeds` | held |
| 11 | `TestAdv_11_AllGoImportsIncludingTests` | BROKEN |
| 11 | `TestAdv_11_ModuleRequirements` | held |
| 11 | `TestAdv_11_NoReplacementOrWorkspaceEscape` | held |

Reproduction commands below assume the worktree root and `export PATH=/usr/local/go/bin:$PATH`.
All five broken attacks use seed **81173**; the import inspection is seed-independent.

**Claim 2 — statement limit leaves a transaction usable (product defect).**

```sh
go test -count=1 -run '^TestAdv_2_StatementLimitErrorAbortsTransaction$' ./agent_work/adversarial/c02-sim-world/...
```

Begin a transaction, then send 65 supported SELECT statements in one Query. The fake deliberately rejects the message with `54000`. An ERROR in an explicit transaction must leave ReadyForQuery `E`; observed `E` (ErrorResponse), then `ZT` (ReadyForQuery in a still-usable transaction). `beginMessage` handles the statement limit before normal failure handling. This is not a demand that Postgres share the fake's artificial 64-statement cap: it is a demand that the fake's own error abort the transaction.

**Claim 3 — phantom applied write at the table cap (product defect).**

```sh
go test -count=1 -run '^TestAdv_3_FullTableCommitMustNotLogPhantom$' ./agent_work/adversarial/c02-sim-world/...
```

With `MaxRows=1`, explicitly commit two inserts into different keys. The claim promises an exact log of applied writes. Observed log sequence 2 claims `b=2` was applied, but the table contains only `a=1`. The commit path ignores `put`'s error and increments the applied-write counter and log anyway. This attack uses the default large log cap, so retention cannot explain the missing row.

**Claim 5 — injected stall misses existing service (product defect).**

```sh
go test -count=1 -run '^TestAdv_5_StallHitsAlreadyServing$' ./agent_work/adversarial/c02-sim-world/...
```

Start a 100 ms statement. At 20 ms into service, inject a 200 ms stall window with 150 ms extra delay. NOTES.md explicitly includes statements already in service in the window. At 120 ms after sending, the entire reply including ReadyForQuery has arrived; it should still be in service until approximately 250 ms. The application is counted and traced, but `addStall` only appends a window; the running statement's timer is not changed.

**Claim 7 — sent-in-window equality (over-broad claim).**

```sh
go test -count=1 -run '^TestAdv_7_SendWindowIndependentOfService$' ./agent_work/adversarial/c02-sim-world/...
```

One client, 17 arrivals 1 ms apart starting at 10 ms, 1 s database service. At 40 ms all 17 sends are intended. Observed `Intended=17`, `Sent=1`, `NotYetSent=16`. Claim 7 explicitly promises the number sent in a window equals the intended number regardless of speed. The implementation follows the deliverable's alternative design: busy clients queue requests and include that delay in latency. The latency and backlog accounting attacks hold. Narrow the claim to intended arrival generation plus eventual drain/conservation, or change the sending model; do not hide this failure by waiting for drain before asserting the window.

**Claim 11 — repository-wide import exclusivity (over-broad claim).**

```sh
go test -count=1 -run '^TestAdv_11_AllGoImportsIncludingTests$' ./agent_work/adversarial/c02-sim-world/...
```

The literal claim permits `pgproto3` only under `internal/sim/fakepg` and the test-client package. An AST scan of all repository Go files finds imports in `agent_work/phase1/probes/pgwire/main.go` and five files in `tools/detlint/cmd/importlint/testdata/mod/internal/` (`other/o.go`, `pgwire/w.go`, `pool/pool.go`, `sim/fakepg/f.go`, `testclient/c_test.go`). These are pre-existing probe and lint-fixture files, including nested modules; this is not evidence of a production dependency leak. Scope the claim to the production module/package set if that is intended. The separate test of this root go.mod's actual Require list holds: exactly one direct pgx requirement. Transitive module-graph membership is deliberately not confused with Require entries.

**Execution evidence and limits.**

- `default.jsonl`: final `go test -count=1 -json ./agent_work/adversarial/c02-sim-world/...`; 29 held, 5 broken. No Docker or new module requirement. Each failure remains in place.
- `long.jsonl`: `ADV_LONG=1 go test -count=1 -json -run '^TestAdv_9_RandomFaultRecovery$' ./agent_work/adversarial/c02-sim-world/...`; passed. 300 seeds (81173–81472), twice each, with 12 timed injections of boundary, freeze, and first-byte faults overlapping a 16-statement conversation. Every kind clears; work completes, a fresh exchange succeeds, connections close, quiesce holds, and both digests match. Default runs 12 seeds.
- Replay attacks also cover all five baselines at seeds -1, 0, and MaxInt64, comparing both digests and observations, plus a fresh-process comparison of the mixed-transaction baseline. Baseline-number attacks use 81173, -81173, and 1000003, not the builder's pinned seeds.
- `tier-r.sh` is the local script index entry: starts an isolated `postgres:17` container, binds only loopback on an ephemeral port, runs the three claim-1 attacks with `MG_TIER_R=1`, and removes the container on exit. `tier-r.log` records **Postgres 17.11**, all three passed. The TCP oracle uses independent raw frame reads; it does not call or copy the builder's fidelity script. No production transcript is overwritten. To replay: `agent_work/adversarial/c02-sim-world/tier-r.sh`.
- Live shapes: comment/semicolon transaction `CTDCCZI`; error after explicit commit `CTDCCEZI`; rollback followed by new BEGIN `CCCTDCZT`. These also exercise claim 2's transaction boundaries. `MG_TIER_R` absent means the simulator assertions still run, with no network calls.
- Claim 1 is narrowly worded around the ten existing probe exchanges. As instructed, these attacks are new supported-subset conversations rather than copies of those tests. Their success does not establish full Postgres equivalence or independently re-validate all ten original exchanges.
- No claim was wholly unattackable. The three claim-5 attacks examine selected faults and their interleavings, not exhaustive combinations of all 24 kinds. Client attacks cover clearing an armed partial send, disconnecting a queued backend, and cancel delivery while reads are stopped. Neither campaign proves universal correctness over all schedules.
- The initial `kb` executable and its `--global` option could not locate a store and returned no search IDs. The known VM-store CLI search 811 was judged useful (rank 8). No project findings were saved to the VM knowledge store.

Durable findings are the five reproductions above. The reusable artifact is the isolated tier-R runner; no new project-wide helper was needed. All changes, evidence, and this handoff remain inside this attack directory, as the brief requires. Root STATE.md and agent_work/INDEX.md were intentionally not edited.

Final default package elapsed time: **3.979 seconds**, below the three-minute budget on this machine.
