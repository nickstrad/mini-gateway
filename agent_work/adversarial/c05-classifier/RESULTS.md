HELD: 29  BROKEN: 3

Attacks derived from the ten claims in the builder brief, against build tip `7b156bdd603b6c8b85a250cd03d9ad3a10d3fbf3`. All attacks print seed `5052026`; no builder corpus or builder tests were reused. Counts are named attacks, not inputs or repetitions.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_EscapeContinuation` | held |
| 1 | `TestAdv_1_UnicodeFunctionNames` | held |
| 1 | `TestAdv_1_WholeMessageRejection` | held |
| 2 | `TestAdv_2_NestedWriteAndLock` | held |
| 2 | `TestAdv_2_ShareKeyOwnershipAndScope` | held |
| 2 | `TestAdv_2_TimeDependentLiterals` | BROKEN |
| 2 | `TestAdv_2_UnknownCallsInGrammar` | held |
| 3 | `TestAdv_3_BadByteBetweenStatements` | held |
| 3 | `TestAdv_3_RejectBeforeLexError` | BROKEN |
| 3 | `TestAdv_3_TruncatedRead` | held |
| 4 | `TestAdv_4_FunctionPadding` | held |
| 4 | `TestAdv_4_LockPadding` | held |
| 4 | `TestAdv_4_PostgresUnaryOperatorBoundary` | BROKEN |
| 4 | `TestAdv_4_TransactionPadding` | held |
| 5 | `TestAdv_5_AllBytePairs` | held |
| 5 | `TestAdv_5_AllocationCeiling` | held |
| 5 | `TestAdv_5_SeededHostileBytes` | held |
| 6 | `TestAdv_6_ChainAndNoChain` | held |
| 6 | `TestAdv_6_FaultBeforeAndAfterBegin` | held |
| 6 | `TestAdv_6_UnicodeWhitespaceAndControl` | held |
| 7 | `TestAdv_7_AllRowsWithPrefixAndStatus` | held |
| 7 | `TestAdv_7_DiscardAllBytesAndRecover` | held |
| 7 | `TestAdv_7_InvalidInputsDoNotPoisonReuse` | held |
| 8 | `TestAdv_8_ErrorDocumentExact` | held |
| 8 | `TestAdv_8_GeneratorsTrackTableMutation` | held |
| 8 | `TestAdv_8_RejectDocumentExact` | held |
| 9 | `TestAdv_9_ImportClosureIsStandard` | held |
| 9 | `TestAdv_9_OutputTypeClosure` | held |
| 9 | `TestAdv_9_SourceHasNoProtocolImports` | held |
| 10 | `TestAdv_10_EscapeContinuationLeak` | held |
| 10 | `TestAdv_10_MultiStatementRejectAtomicity` | held |
| 10 | `TestAdv_10_NestedFunctionReject` | held |

## Broken attacks

All commands below run from `/root/Software/mini-gateway-work/c05-adv`. Seed is fixed at **5052026**. The failing tests remain enabled and failing.

### Claim 2 — time-sensitive input literals

Command:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_2_TimeDependentLiterals$' ./agent_work/adversarial/c05-classifier/...
```

Expected: D10 excludes expressions whose results differ per call. `SELECT 'now'::timestamp`, `SELECT 'now'::timestamptz`, and `SELECT CAST('today' AS date)` must not be coalescable.

Observed: all three return `ReadOnly:true Coalescable:true`. In the tier R run, PostgreSQL confirms the special timestamp literal equals the current transaction timestamp and the date literal equals the current date. These are built-in temporal expressions, with no user-created function, view, operator, or overload involved.

Judgment: **product defect against the stated D10 contract**. The broad notes limit concerning type-input functions could be read as excluding even built-in temporal literal parsing. Under that interpretation, this becomes an **over-broad claim**, and the D10 guarantee needs an explicit temporal-literal exclusion. The test deliberately retains the original D10 expectation.

### Claim 3 — reject prefix followed by lexical failure

Command:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_3_RejectBeforeLexError$' ./agent_work/adversarial/c05-classifier/...
```

Expected: every message the lexer cannot fully tokenise is forwarded, never shared and never read-only.

Observed: `DISCARD ALL; SELECT 'unfinished`, `LISTEN adv; /* unfinished`, and `RESET ALL; SELECT $tag$unfinished` all have `LexAll` clean=false but return class reject, in both transaction contexts. Read-only and coalescable remain false.

Judgment: **over-broad original claim**, already acknowledged by the builder's deviations. Rejection takes precedence over a lexical error. No database-execution safety defect is asserted; this attack preserves the requested claim rather than silently accepting the narrower implementation contract.

### Claim 4 — whitespace between ordinary and unary operators

Command:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_4_PostgresUnaryOperatorBoundary$' ./agent_work/adversarial/c05-classifier/...
```

Expected: adding whitespace between PostgreSQL tokens preserves classification.

Observed: `SELECT 71+-3` is neither read-only-provable nor coalescable, while `SELECT 71+ -3` is both. PostgreSQL 17 accepts both spellings. The lexer consumes `+-` as one maximal operator, which is outside the operator allow-list; PostgreSQL recognizes the ordinary `+` followed by unary `-` in this context.

Judgment: **product defect in claim 4's whitespace invariance**, a false-negative classification rather than an unsafe sharing false positive. The notes describe maximal operator scanning, but do not narrow claim 4 to exclude PostgreSQL's operator-boundary rule.

## Execution evidence

- `default.json`: default command, 26 held, three broken, three tier R attacks skipped; package execution 0.702 seconds. This is comfortably below the three-minute limit; compilation/cache startup is not included in Go's package timing.
- `tier-r.json`: three transaction-effect attacks held; the temporal-literal and unary-operator failures were reproduced with PostgreSQL oracles enabled. Each transaction query uses a fresh TCP session and reads the actual ReadyForQuery status, including syntax/error paths. No claim-6 attack was counted as held merely because it skipped in the default run.
- `long.json`: `ADV_LONG=1` increases the hostile-byte campaign from 3,000 to 100,000 deterministic inputs; 6.479 seconds. The separately launched default and long processes produced identical per-test verdicts. The 65,536 byte-pair sweep and allocation attacks also ran in both processes.
- The three fake-database attacks each run the classifier gate and a deliberately bypassed gate. The bypass must increase the fake's statement count; the real gate must keep it zero. A subsequent allowed read must execute and simulated connections must quiesce. This is an independent minimal frame/classify/forward harness, not the future production proxy.

Commands:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/c05-classifier/...
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c05-classifier/...
agent_work/adversarial/c05-classifier/run-tier-r.sh
```

All three commands currently exit nonzero because the broken attacks are retained. The tier R runner uses the same `postgres:17` image and trust/loopback setup as `scripts/tier-r.sh`, with its own uniquely named disposable container and cleanup trap. It sets the required `MG_TIER_R=1` gate and `MG_PG_ADDR`; it selects the adversarial tests because the repository's existing script selects only names beginning `TestTierR`. It saves its output to `tier-r.json`.

## Coverage limits

No claim was left without at least three distinct named attacks. These are finite attacks, not proofs over all inputs. Allocation measurement checks the published `80*n + 64KiB` bound for three hostile shapes; byte sweeps test panic/span behavior, not a universal resource bound. Claim 8 is attacked by exact document comparisons and table-mutation sensitivity, without modifying the committed documents outside the authorized directory. Claim 9 checks the compiled dependency closure, output type closure, and source imports. Claim 10 covers an independent minimal harness and fake statement counters; production proxy integration belongs to chunk 6.

No product files, shared state files, index files, or generator outputs were edited. Only this adversarial directory is committed.
