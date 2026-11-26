HELD: 39  BROKEN: 3

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_ByteSplitsThroughRow` | held |
| 1 | `TestAdv_1_SeededIrregularSplits` | held |
| 1 | `TestAdv_1_HeaderBoundarySplits` | held |
| 2 | `TestAdv_2_FailedTransactionKeepsOwner` | held |
| 2 | `TestAdv_2_QueuedContenderCannotSteal` | held |
| 2 | `TestAdv_2_OwnerVanishClosesLease` | held |
| 3 | `TestAdv_3_DuplicateResetTag` | held |
| 3 | `TestAdv_3_ResetWithTrailingReply` | BROKEN |
| 3 | `TestAdv_3_ResetDeadlineTie` | BROKEN |
| 4 | `TestAdv_4_PartialCommandThenLoss` | held |
| 4 | `TestAdv_4_QueueShedHasSingleEnd` | held |
| 4 | `TestAdv_4_RejectInsideFailedTransaction` | held |
| 5 | `TestAdv_5_FirstByteAccepted` | held |
| 5 | `TestAdv_5_ResponseBeforeWriteAck` | held |
| 5 | `TestAdv_5_ZeroAcceptedBytes` | held |
| 6 | `TestAdv_6_QueueStorm` | held |
| 6 | `TestAdv_6_StartupCapBurst` | held |
| 6 | `TestAdv_6_RowLargerThanRelay` | held |
| 7 | `TestAdv_7_PartialStartupDeadline` | held |
| 7 | `TestAdv_7_PartialQueryDeadline` | held |
| 7 | `TestAdv_7_IdleTransactionDeadline` | held |
| 9 | `TestAdv_9_RequireAfterGSSRefusal` | held |
| 9 | `TestAdv_9_SSLWithTrailingStartup` | held |
| 9 | `TestAdv_9_VerifyFullRefusedTLS` | held |
| 10 | `TestAdv_10_CommentedMixedCaseListen` | held |
| 10 | `TestAdv_10_PrepareWithWhitespace` | held |
| 10 | `TestAdv_10_CopyAfterLineComment` | held |
| 11 | `TestAdv_11_RoundTripWithQueuedTransactions` | held |
| 11 | `TestAdv_11_ByteIdenticalIndependentFlushes` | held |
| 11 | `TestAdv_11_ConservationFromReloadedFiles` | held |
| 12 | `TestAdv_12_ConcurrentSchedulesReplay` | held |
| 12 | `TestAdv_12_FragmentedReplay` | held |
| 12 | `TestAdv_12_FreshProcessReplay` | held |
| 13 | `TestAdv_13_NegativeFleetBudget` | held |
| 13 | `TestAdv_13_ExplicitZeroCapacity` | BROKEN |
| 13 | `TestAdv_13_NestedTimeoutEquality` | held |
| 14 | `TestAdv_14_AllByteValuesAndLengths` | held |
| 14 | `TestAdv_14_OneByteCannotReseedWorld` | held |
| 14 | `TestAdv_14_SeededPlantCampaign` | held |
| 14 | `TestAdv_14_LongUnplantedCampaign` | pending (ADV_LONG) |
| 8 | `TestAdv_8_AdminWhileMailboxBackedUp` | held |
| 8 | `TestAdv_8_ConcurrentCloseDuringDial` | held |
| 8 | `TestAdv_8_HalfStartupDisconnectStorm` | held |
| 3b | `TestAdv_3b_AbortedLeaseSettings` | pending (ADV_LONG) |
| 3b | `TestAdv_3b_DynamicTempPreparedListener` | pending (ADV_LONG) |
| 3b | `TestAdv_3b_ReentrantAdvisoryLocks` | pending (ADV_LONG) |

Seed: `6062026` throughout (logged by the tests); deterministic boundary cases use no randomness.
Counts are attacks, not claims. All 15 claim identifiers (1–14 and 3b) have at least
three distinct named tests. No claim is wholly unattacked.

Checkpoint: the default run completed in 2.054 seconds: 39 held, 3 broken, 4 skipped.
The `ADV_LONG=1` invocation is in progress, including the 3,000-input campaign and the
three real-Postgres isolation attacks. Pending tests are not counted as held.

Commands, from this worktree:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/c06-walking-skeleton/...
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -v ./agent_work/adversarial/c06-walking-skeleton/...
```

Evidence: `default-run.log` and `long-run.log` are this continuation's output;
`first-run.log` and `default.jsonl` are preserved first-dispatch evidence, not this run's verdicts.
Both commands are expected to exit 1 because broken attacks remain failing.

## Broken attacks

### Claim 3: TestAdv_3_ResetWithTrailingReply

Reproduce (seed `6062026`):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_3_ResetWithTrailingReply$' ./agent_work/adversarial/c06-walking-skeleton/...
```

Expected: claim 3 and the dirty-connection deliverable prohibit reuse with unread bytes.
A reset completion followed by an unsolicited complete reply in the same read must close
the connection before lending it to the queued client.
Observed: the engine writes `SELECT 82` and another `DISCARD ALL` after accepting the
trailing `SELECT 1` reply. The assertion reports “database lent with unread reset residue”.
Judgement: **product defect**. `internal/proxy/lease.go`'s `judgeReset` calls `makeIdle`
and `dispatch` before returning the unread remainder to `dbData`. This allows that
remainder to be processed after the next client has acquired the connection.

### Claim 3: TestAdv_3_ResetDeadlineTie

Reproduce (seed `6062026`):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_3_ResetDeadlineTie$' ./agent_work/adversarial/c06-walking-skeleton/...
```

Expected: the reset deadline is 1 second; PLAN.md's chunk 6 says to close “at the reset
deadline”. A completion delivered at the expired deadline must not dispatch queued work.
Observed: “reset at expired deadline dispatched queued query”.
Judgement: **product defect under the deadline wording**. `Engine.Step` processes data
before `finish` fires due timers; a successful reset cancels the due reset timer first.
This attack specifically covers data-before-timer ordering at equality; it does not claim
that an already-closed connection can be resurrected. If equality is intended to permit
completion, that exception needs an explicit contract ruling.

### Claim 13: TestAdv_13_ExplicitZeroCapacity

Reproduce (seed `6062026`):

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_13_ExplicitZeroCapacity$' ./agent_work/adversarial/c06-walking-skeleton/...
```

Expected: the brief's configuration deliverable says “caps are positive” and validation
fails closed. An explicitly supplied `queue.capacity: 0` should produce an error naming
that key; the otherwise-identical baseline is verified valid before the edit.
Observed: `config.Parse` succeeds; the attack reports “accepted invalid queue.capacity”.
Judgement: **over-broad claim / specification mismatch**, rather than an unbounded-memory
finding. NOTES.md decision 12 explicitly says “zero = default”, and `applyDefaults`
replaces zero through `proxy.Config.WithDefaults` before validation. The effective cap is
positive. The attack retains the literal brief expectation for reconciliation; either the
brief must allow explicit zero as default or parsing must distinguish omitted and zero.

## Scope and handoff

These are finite attacks, not proofs of the universal claims. Replay tests exercise
concurrent schedules, fragmentation and separate processes, but do not independently
re-run the repository's sans-I/O lint. Operation-decoder locality and `OpSeed` checks do
not by themselves prove every internal random source is independent of input hashes.
The real-server attacks cover transactional settings, hidden temp/prepared/listener state,
and repeated exclusive/shared advisory locks, within the stated DISCARD ALL residue.

This directory is the only permitted edit area, so this report serves as the checkpoint
instead of root STATE.md. Product code and agent_work/INDEX.md are untouched. The inherited
harness was committed first as `e4f9d44`. The two reset regressions are durable project
findings; their tests and this report are the retained artifacts for the builder/reviewer.
No VM knowledge entry or separate reusable script is needed.

The initial kb invocations failed before producing a search ID. The existing VM kb binary
then completed search 839; feedback was recorded as none useful because the brief already
specified Go's path and the hits did not describe this project's real-server fixture.
