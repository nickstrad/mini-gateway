HELD: 31  BROKEN: 0

Attacked build `65ddfff6dc289fa4444a206f670f8bdff8b4b97e`, seed **730327**.
Thirty default attacks plus one opt-in campaign. There are three separately named
attacks for each of the ten numbered claims in the builder notes; the extra campaign
extends claims 4, 6 and 10. No product files were changed.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_OperatorCommentSeams` | held |
| 1 | `TestAdv_1_KeepSignLate` | held |
| 1 | `TestAdv_1_SplitAfterNumeric` | held |
| 2 | `TestAdv_2_QuotedCaseAndStatements` | held |
| 2 | `TestAdv_2_SettingSuffixAndNestedCalls` | held |
| 2 | `TestAdv_2_UnicodeSettingNames` | held |
| 3 | `TestAdv_3_OperatorCounterNotDead` | held |
| 3 | `TestAdv_3_KeywordCounterNotDead` | held |
| 3 | `TestAdv_3_TokenCounterNotDead` | held |
| 4 | `TestAdv_4_IndependentProcesses` | held |
| 4 | `TestAdv_4_ConcurrentProcesses` | held |
| 4 | `TestAdv_4_HostileByteReplay` | held |
| 5 | `TestAdv_5_RescanOperators` | held |
| 5 | `TestAdv_5_RescanSettingWords` | held |
| 5 | `TestAdv_5_RescanInterleavedSettings` | held |
| 6 | `TestAdv_6_ContinuationFailures` | held |
| 6 | `TestAdv_6_DollarTagNearMatches` | held |
| 6 | `TestAdv_6_DeepUnbalancedAndResets` | held |
| 7 | `TestAdv_7_UnicodeLiterals` | held |
| 7 | `TestAdv_7_SetLocalArgumentBoundaries` | held |
| 7 | `TestAdv_7_MixedTokenFamilies` | held |
| 8 | `TestAdv_8_RescanOpenParens` | held |
| 8 | `TestAdv_8_RescanBalancedParens` | held |
| 8 | `TestAdv_8_RescanStatementBoundaries` | held |
| 9 | `TestAdv_9_NoClockImport` | held |
| 9 | `TestAdv_9_NoClockSelectors` | held |
| 9 | `TestAdv_9_NoTimingHelpers` | held |
| 10 | `TestAdv_10_AllCountersConcurrent` | held |
| 10 | `TestAdv_10_BoundaryOutputs` | held |
| 10 | `TestAdv_10_SeededOutputsAndAllocations` | held |
| 6 | `TestAdv_6_LongSeededCampaign` | held |

## Reproduction

From the worktree root, with `/usr/local/go/bin` on PATH:

```
go test -count=1 ./agent_work/adversarial/cr03-growth-tests/...
ADV_LONG=1 go test -count=1 ./agent_work/adversarial/cr03-growth-tests/...
```

Seed 730327 is fixed and printed with `-v`. Full final test output is in
`default-final.jsonl` and `long-final.jsonl`; only these final runs are retained.
The final default run passed 30 attacks (one opt-in skip) in 29.688 seconds;
the final long run passed all 31 in 50.246 seconds. These durations describe
runtime only and do not determine any verdict.
The long campaign compares 2,000 generated inputs and runs the actual three builder
growth tests 20 times. Its process-replay attacks increase from four to forty runs,
including concurrent child processes that supply CPU load themselves.

## What the attacks check

- **Claims 1, 2, 6, 7:** independently chosen SQL combinations: operator/comment
  boundaries, a late sign-preserving byte, numeric adjacency, quoted and escaped
  setting names, statement resets, failed string continuations, almost-matching
  dollar tags, malformed nesting, Unicode names, and long local-setting arguments.
  Inputs use 0, 1, 31, 256 and 4,096 repetitions. All five counters are checked at
  256 versus 4,096 repetitions, with a 17-times bound plus 32 fixed operations for
  boundary effects. The designated path must move. This is a nearby-input attack,
  not a reimplementation of the builder's 24 fixtures. Exact counts are in the logs.
- **Claim 3:** remove operator increments, keyword increments, or every token-visit
  increment in an overlay. The actual matching builder test must fail and report
  that its measured path did no work. A compilation failure does not count as success.
- **Claims 4 and 10:** compare all five counts, complete token streams, classification
  results, sharing decisions, snapshots and allocation counts across independent
  processes, both serially and concurrently. Alternating requests use transaction
  and non-transaction classification. Count collection itself uses non-transaction
  classification, matching the builder seam.
- **Claim 5:** disable the operator-run cache; separately insert repeated span keyword
  scans on two independently chosen setting-name shapes. The counted growth must
  exceed 17 times, while tokens and classifications remain unchanged.
- **Claim 8:** insert forward token scans at every opening parenthesis, then attack
  unclosed parentheses, balanced nesting and statement-separated parentheses. These
  scans use the real accessor and keep results unchanged. All three must exceed the
  counted growth bound. The mutation inputs use 64 versus 1,024 repetitions to avoid
  spending billions of operations on a deliberate defect. These mutations demonstrate
  counter sensitivity; they do not assert that every possible quadratic defect is caught.
- **Claim 9:** inspect the classifier test source for a time import, clock selectors,
  and the removed timing helpers. This uses the claim's demonstrated scope,
  `classify_test.go`, not every unrelated pgwire test. The adversarial verdicts likewise
  never compare elapsed times.
- **Unchanged-behavior/cost requirement:** compare against the actual pre-seam
  `lex.go` and `classify.go` from commit `4cf67c6`, not a hand-written oracle. Checks
  include malformed byte strings, empty inputs, share-length boundaries, continued
  literals, snapshots and per-call allocation counts for ClassifyQuery, LexAll and
  ShareFits. The fixed sample and the 2,000-input campaign found no differences.

## Harness and limits

`harness_test.go` is an external test package using only the standard library.
`bridge.txt` exposes the five private counters in a **Go build overlay**; `probe.txt`
provides the subprocess driver. Neither creates a source file in the product tree.
Overlays, binaries and logs used during tests live in a unique `.scratch-*` directory
under this adversarial directory and are removed on exit. The baseline and mutated
variants use the same mechanism. The harness requires the named baseline commit in
local git history and the Go compiler at the brief's stated location.

No numbered claim was left wholly unattacked. These attacks are evidence for the
specified shapes and paths, not a proof of whole-program complexity. In particular,
the builder explicitly excludes new uncounted byte loops, new direct-token loops
and ShareFits internals. No hidden superlinear path was established by this pass.
The allocation comparison measures allocation **counts**, not allocated bytes or CPU
cost; the stronger assertion of no measurable hot-path CPU cost is not independently
established here. No timing verdict was substituted for that missing proof.

No durable VM finding or reusable general script was established. The project-specific
findings and reusable harness remain here, within the brief's write boundary. Knowledge
store lookup was attempted before tooling work, but all three documented locations
failed before producing a search ID; feedback could not be recorded. Nothing in the
knowledge corpus was opened. Root STATE.md and the adversarial index were deliberately
left untouched as required by this brief.
