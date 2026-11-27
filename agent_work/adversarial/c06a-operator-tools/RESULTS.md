HELD: 36  BROKEN: 9

Tested builder tip `cd3513b83d99c6dbd933b5e4295131c27c50728f` on branch `work/c06a-adv`.
There are three named attacks for each of the 15 claims. A held attack means that input
passed its assertion, not that the entire claim has been proved. No product code changed.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_BigIntegerClockOracle` | held |
| 1 | `TestAdv_1_ClockBeyondDuration` | held |
| 1 | `TestAdv_1_ReplyPermutation` | held |
| 2 | `TestAdv_2_ErrorRepliesHaveLatency` | held |
| 2 | `TestAdv_2_QueueDelayIncluded` | held |
| 2 | `TestAdv_2_WraparoundQueueLatency` | held |
| 3 | `TestAdv_3_MixedDropsAndLateOracle` | held |
| 3 | `TestAdv_3_OverflowedLatencyFormatting` | BROKEN |
| 3 | `TestAdv_3_ZeroIntendedShortfall` | BROKEN |
| 4 | `TestAdv_4_ExtremeSeeds` | held |
| 4 | `TestAdv_4_ProcessReplay` | held |
| 4 | `TestAdv_4_ReverseRandomAccess` | held |
| 5 | `TestAdv_5_ConnectionReuseAndStaleCallbacks` | held |
| 5 | `TestAdv_5_EventCampaign` | held |
| 5 | `TestAdv_5_StopWithQueuedAndSent` | held |
| 6 | `TestAdv_6_ClassCountsInterleaved` | held |
| 6 | `TestAdv_6_LiveSQLStateClassification` | held |
| 6 | `TestAdv_6_ProfileManySeeds` | held |
| 7 | `TestAdv_7_AdminReadsNeverDial` | held |
| 7 | `TestAdv_7_ConfigDuplicateFieldsDifferential` | held |
| 7 | `TestAdv_7_ConfigGrammarDifferential` | held |
| 8 | `TestAdv_8_InvalidIterationDiagnostics` | held |
| 8 | `TestAdv_8_LongStdinPassword` | BROKEN |
| 8 | `TestAdv_8_UnicodeAndWhitespaceSecrets` | held |
| 9 | `TestAdv_9_FatalWithoutReady` | BROKEN |
| 9 | `TestAdv_9_MultipleResultSets` | held |
| 9 | `TestAdv_9_RowsBeforeLaterError` | held |
| 10 | `TestAdv_10_EmptySiblingRun` | BROKEN |
| 10 | `TestAdv_10_FailureInMiddleRun` | held |
| 10 | `TestAdv_10_WholeFinalFlushMissing` | BROKEN |
| 11 | `TestAdv_11_DirectoryEnumerationOrder` | held |
| 11 | `TestAdv_11_RepeatMalformedReports` | held |
| 11 | `TestAdv_11_RepeatValidReports` | held |
| 12 | `TestAdv_12_ChaosParentFailure` | BROKEN |
| 12 | `TestAdv_12_ChaosProcessFailureAfterPass` | BROKEN |
| 12 | `TestAdv_12_SweepVerboseAndConcise` | held |
| 13 | `TestAdv_13_DockerUnavailableFailsClosed` | BROKEN |
| 13 | `TestAdv_13_IdempotentCleanupAndLoopback` | held |
| 13 | `TestAdv_13_RemovalFailurePropagates` | held |
| 14 | `TestAdv_14_AllLedgerSymbolsExist` | held |
| 14 | `TestAdv_14_FastSetGatesExternalCampaigns` | held |
| 14 | `TestAdv_14_OriginalAssertionsRetained` | held |
| 15 | `TestAdv_15_ModuleRequirements` | held |
| 15 | `TestAdv_15_NoRequirementChangeFromParent` | held |
| 15 | `TestAdv_15_OperatorImportsUseExistingModuleOnly` | held |

## Reproduction

Run from `/root/Software/mini-gateway-work/c06a-adv`:

```sh
export PATH=/usr/local/go/bin:$PATH
go test -count=1 ./agent_work/adversarial/c06a-operator-tools/...
ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c06a-operator-tools/...
```

The default suite completed in 26.764 seconds, with 36 passing and nine failing attacks.
The failures below are deliberately retained. The fixed seed is **606013**, printed by
the tests; no seed environment variable is needed. The default event campaign uses 80
seeds starting at 606013. `ADV_LONG=1` uses 10,000 seeds (606013–616012), 200 events each,
and also executes the promoted short tests in their nine shipped packages. See STATE.md
for the final extended-run timing. `go vet` passed; gofmt reports no unformatted files.

The local JSON logs are `default-run.jsonl` and `long-run.jsonl` (ignored by git). They
include each test's seed, assertions and result. This report and the tests are committed.
`write_report.py` regenerates this report from a complete default JSON log.

## Broken attacks

### 3: Zero intended operations omit SHORTFALL

```sh
go test -count=1 -v -run '^TestAdv_3_ZeroIntendedShortfall$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: rate 37/s, duration 1 ns; this is an accepted plan and produces zero
operations. Expected: the stated rule “whenever achieved < target” emits SHORTFALL.
Observed: `achieved 0.0/s on schedule of target 37/s`, with no SHORTFALL line. The summary
predicate checks only late + could-not-send, while the achieved-rate calculation returns
zero for zero intended operations. Judgment: **product edge-case defect** against the
explicit summary rule; rejecting zero-operation plans or explicitly narrowing that rule
would also resolve the discrepancy.

### 3: Maximum positive latency formats as zero

```sh
go test -count=1 -v -run '^TestAdv_3_OverflowedLatencyFormatting$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: `FormatNs(math.MaxInt64)`. Expected: a positive large duration remains
visible in the operator summary. Observed: `0.0s`, because `ns * 10` overflows before the
division. Judgment: **over-broad claim at an exported-helper boundary**, with an actual
integer-formatting defect. This does not establish a failure in ordinary <=24-hour load
runs; the extreme duration is about 292 years. The attack remains to record that limit.

### 8: Long stdin password is silently truncated

```sh
go test -count=1 -v -run '^TestAdv_8_LongStdinPassword$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: `abcdefgh` repeated 129 times (1,032 bytes), followed by newline,
through verifier's stdin mode. Expected: explicit refusal, or a verifier authenticating
the supplied password. Observed: success output contains a verifier for the first 1,024
bytes; a real `pgwire.ScramServer` exchange using the supplied full password returns
`wrong-password`. Judgment: **product defect**. This case does not depend on randomness
or whether a password was printed; the other two claim-8 attacks check redaction and
valid verifier authentication separately.

### 9: Fatal transaction-loss error gets the retryable read row

```sh
go test -count=1 -v -run '^TestAdv_9_FatalWithoutReady$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: valid startup followed by FATAL 08006,
`mini-gateway: database connection lost inside a transaction`, with no ReadyForQuery.
Expected: `lost-in-transaction`, matching the gateway's error-contract row. Observed:
`lost-after-read`, with `retry: yes` and `client connection: stays usable`. The shorter
read-loss message wins the prefix match before severity can disambiguate. Judgment:
**product defect**; the diagnostic gives the wrong retry and connection advice. This
uses independently assembled wire frames and mgctl.Query, not just a lookup-function
assertion. The test also checks TLS-required and idle-in-transaction fatal replies.

### 10: An explicitly supplied empty run is ignored beside a good run

```sh
go test -count=1 -v -run '^TestAdv_10_EmptySiblingRun$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: two explicit report arguments, a complete three-operation load run
and an empty `aborted-run` directory. Expected: the empty run argument fails the report.
Observed: `VERDICT: PASS (13 checks over 1 run)` and exit 0. The global discovery count
finds the good run, hiding the empty argument. Judgment: **product defect** under “an
empty ... run directory is a failure”; this is not an arbitrary empty folder under a
root, since both directories are explicitly passed to report.sh.

### 10: An entire missing final flush passes

```sh
go test -count=1 -v -run '^TestAdv_10_WholeFinalFlushMissing$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: run.csv declares two flushes, but metrics.csv contains only flush 1.
Its earlier numbers are internally consistent. Expected: a truncated run fails.
Observed: `VERDICT: PASS (13 checks over 1 run)` and exit 0. The report treats the highest
remaining flush as final without reconciling the declared count. Judgment: **product
defect**. All three files and valid headers exist, so checking only file presence does
not detect this truncation.

### 12: Chaos ignores a failing test-process exit after a pass

```sh
go test -count=1 -v -run '^TestAdv_12_ChaosProcessFailureAfterPass$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Through the supported CHAOS_GO seam, the test process prints a passing seed
and scenario, then `panic: late background failure` and exits 1. Expected: nonzero chaos
exit and the failure visible in concise output. Observed: scenario `pass`, `chaos: all
green`, exit 0; panic absent. Judgment: **product defect**. The script does not retain
the Go command's exit status and the passing-seed check masks the later process failure.

### 12: Chaos ignores parent-scenario cleanup failure

```sh
go test -count=1 -v -run '^TestAdv_12_ChaosParentFailure$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: a passed seed beneath a failed parent scenario, with process exit 1,
as can happen in a parent's cleanup. Expected: a failed scenario and nonzero script exit.
Observed: the script counts only failing seed lines, prints the parent as `pass`, and
exits 0 with `all green`. Judgment: **product defect**, a distinct ordering from a test
process crash. Both failures are retained even though preserving the process exit code
would address part of both.

### 13: Cleanup claims success when Docker cannot be inspected

```sh
go test -count=1 -v -run '^TestAdv_13_DockerUnavailableFailsClosed$' ./agent_work/adversarial/c06a-operator-tools
```

Seed: 606013. Input: a Docker command double returning `daemon unreachable` and exit 1
for compose down and every resource-listing command. Expected: nonzero exit because
cleanup cannot verify or remove the stack's resources. Observed: `run-down: the dev
stack is down ...`, exit 0. Judgment: **product defect**. List-command failures collapse
to empty lists and the compose failure is ignored. No real Docker resources are touched.
A separate attack verifies that a listed container's removal failure *does* propagate.

## Coverage and limits

- All 15 claims were attacked; none was left entirely untested. Attacks were derived
  from the claims and public APIs. Builder test bodies were not used as test designs;
  the promotion-ledger checks necessarily inspect the older original and promoted tests.
- Claims 1–6: pure scheduling/accounting boundaries, ring reuse, delayed/error latency,
  reply independence, maximum clocks, repeatability across three child processes with
  different GOMAXPROCS, connection-loss callbacks, interrupt finalization, and the seeded
  event campaign. The live class-count test uses an independent TCP wire responder and
  requires exactly 18 database errors and six refusals, six per SQLSTATE class.
  It does not launch Postgres or prove every errors-profile SQL against a real database.
- Claim 5's interrupt attack drives Stop/Finish with both queued and sent operations;
  it is not an OS-signal lifecycle test. Conservation is checked after every event in
  the randomized campaign. Reopening a closed slot and stale callbacks are exercised.
- Claims 7–9: malformed/duplicate configuration inputs are compared with config.Load;
  admin reads are driven with a read-only spy and forbidden database dial; SCRAM uses
  the actual gateway authentication core. Query responses are independently encoded
  wire fixtures, including FATAL without ReadyForQuery and multiple result sets.
  A real gateway/Postgres end-to-end matrix is not claimed here.
- Claims 10–11: independent synthetic load CSV runs, a bad run between two good runs,
  missing whole flushes, malformed rows, repeated reports and changed filesystem creation
  order. The failure-in-middle test requires the entire violating assertion row in output.
  No new oracle SQL was injected outside the permitted directory, so exhaustive coverage
  of every existing scenario's oracle mismatch is not claimed.
- Claim 12: subprocess-result doubles exercise script control flow without a costly seed
  sweep; concise output must contain one scenario line, and verbose output must retain raw
  test detail. Claim 13 uses a Docker double and verifies loopback declarations, repeated
  cleanup, binary removal and CSV preservation. No live stack is started on the shared
  machine, and source binding checks do not prove runtime socket state or process cleanup.
- Claim 14: every promotion-ledger function is located by Go AST, non-split tests retain
  their original failure-call expressions (accounting for five helper/constant renames),
  and external-campaign gating is checked. The extended run executes the promoted short
  tests. This is not a full semantic equivalence proof, nor a measurement of the entire
  CI gate under five minutes. The nine retained broken attacks also make an all-packages
  invocation fail intentionally until the product/claims are resolved.
- Claim 15: actual parsed go.mod requirements, byte equality with the pre-chunk base,
  and every operator package's imports (including tests) are checked independently.
- Tooling limitation: kb lookup failed before issuing a search ID; details are in STATE.md.
  No outside file was edited, no product fix was made, and no push or merge was performed.
