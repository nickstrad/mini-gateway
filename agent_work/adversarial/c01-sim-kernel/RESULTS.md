HELD: 30  BROKEN: 4

Adversarial review of builder tip `19edca0f05e46a5762b9abf8833ee16fc06252c0`, branch `work/c01-adv`. Attacks were derived from the ten claims and the stated fault semantics; builder test source was not read or reused. Only the adversarial directory was changed. Product code is untouched.

Counts are top-level named attacks, not subtests. Default: 29 held, four broken, one deliberately skipped long campaign; package execution 4.527 seconds. Extended run: 30 held, four broken, no skips; 4.751 seconds. Both commands exited 1 because broken tests remain enabled. Toolchain: Go 1.26.8, linux/amd64. Durations are this machine's measurements, not guarantees for another host; both are well below the three-minute default budget.

Commands, from the worktree root (Go must be on PATH):

```sh
export PATH=/usr/local/go/bin:$PATH
go test -count=1 ./agent_work/adversarial/c01-sim-kernel/...
ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c01-sim-kernel/...
```

Machine-readable evidence: `default.json`, `long.json` (`go test -json` outputs). All attacks print seed 731921 and a replay command under `-v`; seed-independent structural/arithmetic attacks retain the same identifier. Duplex default tests use seeds 731921 through 731936 in each of three chunking modes. The extended campaign uses 2,048 seeds `731921 + i*7919`, for `0 <= i < 2048`; failures print their exact seed.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_ProcessReplay` | held |
| 1 | `TestAdv_1_RingWrapReplay` | held |
| 1 | `TestAdv_1_TraceToggle` | held |
| 2 | `TestAdv_2_HostileLabels` | held |
| 2 | `TestAdv_2_InterleavedConsumption` | held |
| 2 | `TestAdv_2_RecreatedStreams` | held |
| 3 | `TestAdv_3_BackgroundHandleCopies` | held |
| 3 | `TestAdv_3_CancelInsideTie` | held |
| 3 | `TestAdv_3_ExtremeTimeArithmetic` | held |
| 3 | `TestAdv_3_RefusalRoundTripOverflow` | BROKEN |
| 4 | `TestAdv_4_ClosedReaderFreesBlockedWriter` | BROKEN |
| 4 | `TestAdv_4_DuplexTinyWindow` | held |
| 4 | `TestAdv_4_LatencySumSaturates` | BROKEN |
| 4 | `TestAdv_4_LongSeedCampaign` | held |
| 4 | `TestAdv_4_MaximumJitter` | BROKEN |
| 5 | `TestAdv_5_HalfOpenDuringHandshake` | held |
| 5 | `TestAdv_5_ListenerClearDoesNotReviveOldDial` | held |
| 5 | `TestAdv_5_ResetWhileStalled` | held |
| 5 | `TestAdv_5_StalledFINBothEnds` | held |
| 6 | `TestAdv_6_ChunkClearRestoresRandomSequence` | held |
| 6 | `TestAdv_6_ClearBeforeArrival` | held |
| 6 | `TestAdv_6_FaultedNeighborTraffic` | held |
| 7 | `TestAdv_7_BackgroundMasksNoForeground` | held |
| 7 | `TestAdv_7_ConnectingLeakWithoutTimers` | held |
| 7 | `TestAdv_7_HeldBytesWithoutTimers` | held |
| 8 | `TestAdv_8_AllocationAndHugeRank` | held |
| 8 | `TestAdv_8_MergeTreeAndClamps` | held |
| 8 | `TestAdv_8_PowerBoundaryError` | held |
| 9 | `TestAdv_9_EmptyAndMaximumSchedule` | held |
| 9 | `TestAdv_9_NonmonotonePredicate` | held |
| 9 | `TestAdv_9_OrderedDuplicateTriggers` | held |
| 10 | `TestAdv_10_AliasedEntropyAndClock` | held |
| 10 | `TestAdv_10_ConcurrencyAndFloatingSyntax` | held |
| 10 | `TestAdv_10_DeterminismLintNoAllowances` | held |

## Broken attacks

### Claim 3: refused-dial round-trip overflow

```sh
go test -count=1 -v ./agent_work/adversarial/c01-sim-kernel -run '^TestAdv_3_RefusalRoundTripOverflow$'
```

Seed: **731921** (no random draw needed). Up latency is `MaxInt64` nanoseconds; down latency is one nanosecond. The notes require refusal after one round trip and claim 3 says time never overflows. Expected: refusal at saturated `sim.MaxTime` (9223372036854775807). Observed: refusal at **0**. **Product defect**: `net.go:139` adds durations with ordinary signed arithmetic before calling the saturating scheduling API.

### Claim 4: maximum nonnegative jitter panics

```sh
go test -count=1 -v ./agent_work/adversarial/c01-sim-kernel -run '^TestAdv_4_MaximumJitter$'
```

Seed: **731921**. Up jitter is `time.Duration(math.MaxInt64)`, base zero, window one byte. Claim 4 covers every latency setting, and options normalization accepts nonnegative jitter. Expected: a valid sampled latency, eventual connection and one-byte delivery. Observed: **panic: invalid argument to Int64N** during Dial, outside the kernel panic guard. **Product defect**: `pipe.delay` adds one to maximum jitter before passing the bound to `Int64N`. The test catches that panic only to turn it into a descriptive failing assertion. There is no documented smaller jitter limit.

### Claim 4: base-plus-jitter overflow

```sh
go test -count=1 -v ./agent_work/adversarial/c01-sim-kernel -run '^TestAdv_4_LatencySumSaturates$'
```

Seed: **731921**. Up base latency is `MaxInt64-100`, jitter is 1000; down latency zero. Expected: connection time at least 9223372036854775707, saturating when the draw exceeds representable time. Observed: connection at **0**. **Product defect**: `pipe.delay` adds base and sampled jitter without saturation; scheduling then clamps the wrapped negative deadline to now. This is distinct from the maximum-jitter panic and the refusal round-trip addition.

### Claim 4: closing reader loses writer wakeup

```sh
go test -count=1 -v ./agent_work/adversarial/c01-sim-kernel -run '^TestAdv_4_ClosedReaderFreesBlockedWriter$'
```

Seed: **731921**. Reproduces in both directions (subtests `0`, `1`). A four-byte write fills a two-byte window and arms writable notification through the short write. After delivery, the reader closes, discarding the two unread bytes. Expected: space becomes two and one `EvWritable` arrives, because the notes explicitly promise notification when the receiver consumes **or discards** bytes. Observed: **space=2 wakes=0** in both directions. **Product defect**: `End.Close` calls `discardAll`, which frees the buffer without `unblock`; the ordinary discard-front path does unblock. An event-driven writer can remain asleep although its window is empty. Recovery assertions are retained after the failing assertion, ready to run when the defect is repaired.

## Attack scope and limits

All ten claims were attacked at least three times. No entire claim was left unattempted. Held means these attacks found no counterexample, not a proof of the universal wording.

- Replay: small trace ring repeatedly overwritten with random tied/cancelled events, four fresh processes of the same test binary, and trace-on/off state comparison. Tests print executable reproduction commands. This does not establish that every possible application failure's own REPLAY line is sufficient, or compare different toolchains/architectures.
- Streams: hostile/long/NUL labels, interleaved draw pressure, reconstruction after unrelated streams, and signed seed extrema.
- Scheduling: callback-time cancellation among ties, repeated copied background handles, an independent big-integer oracle for saturating time arithmetic, and the broken network round-trip integration case.
- Transport: simultaneous traffic in both directions, tiny windows, delayed small reads, all three chunking modes, asymmetric jitter, explicit wake counters, byte comparison, cleanup and quiesce. The long campaign is additional coverage, not required by the default run.
- Fault semantics: FIN held by a stall in each direction; reset while both directions are stalled with notification order and discard checks; a client disappearing mid-handshake; refuse/dial-stall/silence cleared between old and new dials. Each listener case checks the old dial remains affected and only a fresh dial is accepted.
- Isolation: neighboring random traffic under repeated faults, temporary stall cleared with bytes already travelling, and restored random chunking after an override. Destructive faults have no clear semantics; the tests follow NOTES.md rather than demanding resurrection after reset or half-open.
- Quiesce: foreground timer hidden among background timers; stalled connection with no events left; held bytes with no events left. Cleanup controls demonstrate that the checks can return success once the resources are removed.
- Histogram: power-of-two boundaries up to the maximum supported value with big-integer error checks, weighted merge trees including clamp counters, zero per-record allocations, and percentile rank arithmetic near maximum signed count. Counts beyond the representable int64 total were not assigned invented overflow semantics.
- Shrinking: a nonmonotone predicate, order-sensitive repeated fault targets, empty failing schedules and maximum-length input. Every result is independently checked for retained failure, one-deletion minimality and caller-input preservation.
- Determinism: alias-aware source checks for clock/global entropy references, independent AST checks for concurrency/floating syntax, and the repository's type-aware determinism lint run with **no allowance file**. The lint checked 16 files in four packages. `simtest` and `obstest` are excluded as explicitly authorized test plumbing in the builder brief; these source scans do not claim to audit arbitrary future dependencies.

## Handoff

The four failures require builder repair and independent review; this adversary made no product fixes. Re-run both commands above after a repair; do not remove, skip or weaken the four failing attacks. No publishing, merging or pushing was performed.

The global kb lookup and its `--global` retry both failed before creating a search record because no store configuration was found. There was no search number on which to record feedback. No VM knowledge store or project file outside the permitted directory was modified. Durable project findings are contained here because the brief forbids writes to `docs/knowledge/` or root `STATE.md`. The harness itself is the reusable executable artifact; no separate utility script was necessary.
