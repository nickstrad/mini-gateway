HELD: 31  BROKEN: 5

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_LengthAndFlagBoundary` | held |
| 1 | `TestAdv_1_QuoteStateCrossProducts` | held |
| 1 | `TestAdv_1_StopWordsAtTokenEdges` | held |
| 2 | `TestAdv_2_PlainTableCoercionAndAggregates` | held |
| 2 | `TestAdv_2_PlainTablePolicyChangesRepeatedRows` | BROKEN |
| 2 | `TestAdv_2_PlainTablePolicyChangesSession` | BROKEN |
| 3 | `TestAdv_3_GatesUseRawArgument` | held |
| 3 | `TestAdv_3_LexerHostilityCannotWiden` | held |
| 3 | `TestAdv_3_TransitiveRecognizerClosure` | held |
| 4 | `TestAdv_4_EveryHintByteMutation` | held |
| 4 | `TestAdv_4_HintOffsetAndTruncation` | held |
| 4 | `TestAdv_4_HintedOpaqueAndRejectionKeyIsolation` | held |
| 5 | `TestAdv_5_BinaryAndMemoryCampaign` | held |
| 5 | `TestAdv_5_OperatorSuffixRescanning` | BROKEN |
| 5 | `TestAdv_5_WholeSpanRescanning` | BROKEN |
| 6 | `TestAdv_6_OpaqueWithoutKnownFeature` | held |
| 6 | `TestAdv_6_UnknownFutureGrammar` | held |
| 6 | `TestAdv_6_UnreadableLocalArguments` | BROKEN |
| 7 | `TestAdv_7_CorpusRejectInMessageMiddle` | held |
| 7 | `TestAdv_7_DocumentedExamplesNestedPadding` | held |
| 7 | `TestAdv_7_KeywordCaseWithoutTouchingLiterals` | held |
| 8 | `TestAdv_8_BatchedAppendAndReadyIsolation` | held |
| 8 | `TestAdv_8_ErrorRowsRandomOrderAccounting` | held |
| 8 | `TestAdv_8_SyncDiscardTerminationAndReuse` | held |
| 9 | `TestAdv_9_ErrorDocumentAgreesWithWire` | held |
| 9 | `TestAdv_9_FirstBuildCorpusWasNotRetired` | held |
| 9 | `TestAdv_9_RejectDocumentTracksLiveRules` | held |
| 9 | `TestAdv_9_ShapeDocumentTablesAndLimits` | held |
| 9 | `TestAdv_9_TestLedgerHasLiveDestinations` | held |
| 10 | `TestAdv_10_AllProductionFilesProtocolFree` | held |
| 10 | `TestAdv_10_ImportGraphUsesGoStandardFlag` | held |
| 10 | `TestAdv_10_PublicTypeClosure` | held |
| 11 | `TestAdv_11_CrossProcessFlushReplay` | held |
| 11 | `TestAdv_11_FlushedTruthAndMissingMetricFailClosed` | held |
| 11 | `TestAdv_11_ManySeedWireRefusalConservation` | held |
| 11 | `TestAdv_11_PipelinedWriteSandwiches` | held |

## Verdict and reproduction

Two product defects break the linear-work guarantee (claim 5). Two attacks expose one missing safety limit: row-level security policies on ordinary tables (claim 2). One attack confirms that claim 6 is broader than the implementation and the exception already disclosed in NOTES.md. Counts are attacks, not distinct root causes. No courtesy gap contributes to these counts.

Base: `ac8ff7a9db530b082a5807dd8cc21514e8641391`, branch `work/c05r-adv`. Every test uses fixed seed **12605990** (`0xC05A26`); deterministic nonrandom attacks print the same seed for the common replay record. Seeded scenario sweeps derive additional seeds as recorded in the tests. No product code was changed.

From `/root/Software/mini-gateway-work/c05r-adv`:

```sh
export PATH=/usr/local/go/bin:$PATH
go test -count=1 ./agent_work/adversarial/c05-classifier/...
ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c05-classifier/...
```

Both commands intentionally exit 1 while the five BROKEN tests remain. Dependencies: Go, Docker with the local `postgres:17` image, and DuckDB 1.5.x for the flushed-file check. Tests use only standard-library and existing repository imports; no module requirement was added. Postgres tests create their own named throwaway container, use its local `psql`, verify major version 17, and remove it after the suite. They do not use existing databases. Missing tools fail the tests rather than silently skipping them.

Raw complete runs: [default.jsonl](default.jsonl), [long.jsonl](long.jsonl). The long mode raises hostile byte strings from 10,000 to 150,000 and scenario seeds from 12 to 1,000. Each byte string also traverses the streaming lexer with exact span-coverage checks. Timing attacks use the minimum of three two-call samples for each size; the input is deterministic, but elapsed-time ratios remain measurements, not a scheduling-independent proof. The source-level repeated-scan arguments below explain the observed growth.

Default package elapsed time: **18.835 seconds** (under the three-minute limit). `go vet ./agent_work/adversarial/c05-classifier/...` passed.
## BROKEN: claim 2 — ordinary table policy changes session state

Test: `TestAdv_2_PlainTablePolicyChangesSession`. Seed: **12605990**.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_2_PlainTablePolicyChangesSession$' ./agent_work/adversarial/c05-classifier
```

Expected: a text classified SafeRead/Shareable on Postgres 17 with plain tables cannot change session state. Observed on PostgreSQL **17.11**: `SELECT k FROM adv_policy` is SafeRead and Shareable, returns `17`, and changes `application_name` from `psql` to `adversary_changed`.

The fixture is an ordinary `CREATE TABLE` with row-level security enabled and a policy using only PostgreSQL built-ins:

```sql
CREATE POLICY visible ON adv_policy
USING (set_config('application_name','adversary_changed',false) = 'adversary_changed');
```

It runs as a non-owner role granted SELECT, so the policy is evaluated. There are no views, rules, user-defined functions or operators, foreign tables, or concurrent writers. Setup changes the role before measuring session state; the difference measured is solely the query's application-name change.

Judgment: **over-broad claim / missing stated limit**, with correctness consequences if ordinary RLS tables are intended to be supported by automatic sharing. The text recognizer cannot inspect the policy. “Plain tables” needs an explicit exclusion of policies that introduce effects; ordinary relations with RLS are not any of the listed exclusions. A reset after the lease does not make this statement safe to share during execution. This is not a courtesy-gap finding.

## BROKEN: claim 2 — ordinary table policy changes repeated rows

Test: `TestAdv_2_PlainTablePolicyChangesRepeatedRows`. Seed: **12605990**.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_2_PlainTablePolicyChangesRepeatedRows$' ./agent_work/adversarial/c05-classifier
```

Expected: identical rows on immediate rerun with nothing else running. Observed: `SELECT count(*) FROM adv_repeat` is SafeRead and Shareable but returns **1, then 0** on the same connection. A one-row ordinary table has this built-in-only policy:

```sql
CREATE POLICY visible ON adv_repeat USING (
  set_config('adv.counter', (current_setting('adv.counter')::int + 1)::text, false) = '1'
);
```

The fixture initializes `adv.counter` to `0` before the first query. The intervening marker SELECTs do not access the table or counter. The table is unchanged and no user-defined function, view, rule, operator or foreign table is involved.

Judgment: **over-broad claim / missing stated limit**, the same RLS omission as the preceding finding, independently breaking repeatability. Kept as its own failing attack because repeatability and session cleanliness are separately promised properties.

## BROKEN: claim 5 — lexer repeatedly scans an operator suffix

Test: `TestAdv_5_OperatorSuffixRescanning`. Seed: **12605990**.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_5_OperatorSuffixRescanning$' ./agent_work/adversarial/c05-classifier
```

Expected: linear work; NOTES.md states that 16 times the input takes no more than 96 times as long. Input is `SELECT ` followed by 512 or 8192 plus signs. The diagnostic in default.jsonl records the measured ratio; it exceeds 96 by a wide margin.

`internal/pgwire/lex.go:335` scans the entire remaining operator run, then `lexOperatorLen` reduces an all-plus run to one byte. The next `Next()` scans the remaining suffix again. The work is proportional to n + (n−1) + … + 1, and classification lexes twice. No valid-SQL assumption restricts claim 5: it explicitly covers any bytes. The smaller witness sizes keep this deliberately failing attack within the default runtime budget; an exploratory 32,768-sign run showed the same approximately quadratic growth.

Judgment: **product defect**, quadratic CPU work on an unauthenticated-to-the-classifier input path (the eventual proxy's authentication policy is outside this chunk). `ShareFits`' length cap does not protect `ClassifyQuery`, which lexes before checking that shape.

## BROKEN: claim 5 — classifier scans the whole span for every identifier

Test: `TestAdv_5_WholeSpanRescanning`. Seed: **12605990**.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_5_WholeSpanRescanning$' ./agent_work/adversarial/c05-classifier
```

Expected: the same linear-work bound. Input is `SELECT ` followed by 512 or 8192 repetitions of `pg_settings `, with no UPDATE token. The diagnostic records a ratio well above 96.

`internal/pgwire/classify.go`'s `tokenReject` calls `m.spanHas(s, "update")` for every name equal to `pg_settings`. `spanHas` (line 545) searches the entire statement each time; the absent target forces a full scan. There are n identifiers and n tokens to search per identifier: quadratic work independent of the operator-lexer issue.

Judgment: **product defect**, a second quadratic CPU path. The statement may be syntactically invalid; claim 5 still applies. These attacks do not claim that the measured memory ceiling was exceeded.

## BROKEN: claim 6 — unmodelled local arguments are refused

Test: `TestAdv_6_UnreadableLocalArguments`. Seed: **12605990**.

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_6_UnreadableLocalArguments$' ./agent_work/adversarial/c05-classifier
```

Expected from the brief's literal claim: an unmodelled construct is forwarded, never rejected. Observed:

- `SELECT set_config(lower('application_name'), 'x', true)` is rejected as `set-config`.
- `SELECT set_config('search_path', lower('public'), true)` is rejected as `temp-object`.

Both are tokenizable, transaction-local calls. The classifier cannot model their arguments and conservatively refuses them. `setConfigLocal` requires one plain string literal as the setting name; `setConfigPathMayNameTemp` refuses a nonliteral path value.

Judgment: **over-broad claim, already acknowledged in NOTES.md “Deviations”**, not a new safety defect. The implementation matches the narrower behavior stated there. The attack remains failing against the brief rather than silently adopting its exception. This is over-refusal, not a forwarded session-feature spelling; the courtesy-gap exclusion therefore does not apply.

## Ledger audit and retained tests

The two claim-9 ledger attacks supplement the three document attacks; they are not new numbered product claims. The live-destination check resolves the ledger's “same / as above” references and checks concrete nonempty test declarations. The corpus-history check reads the first-build commit `d39ecfb` and verifies that all **562 unique original inputs** survive, all original reject reasons remain exact, and every old non-coalescable input is still neither SafeRead nor Shareable.

Manual inspection also compared the ledger with all **46 first-build test/fuzz declarations** in the chunk's files: all 46 have rows. The current corpus has 599 records and 598 unique texts; no old text disappeared. Inspected restated negative assertions in `classify_repair_test.go`, `classify_attacks_test.go`, `classify_spellings_test.go`, and `lex_continue_test.go`: they still assert the relevant never-safe/never-shareable properties, including temporal literals and unlisted calls. The continuation tests assert the corrected Postgres interpretation and preserve rejection of an actual hidden SET. The three retired adversary tests concern the removed transaction-effect output; the ledger does not claim their removed properties still exist. The retained tier-R classifier test still invokes the session-state oracle; the safe-shape real-server oracle has its own test. No ledger defect was established. Declaration checks alone are not claimed to prove semantic equivalence of every retained test.

## Held coverage and limits

- Claim 1: quote-prefix and time-word cross products, independently spelled stop words and token boundaries, cap edges and poisoned bytes, and transaction/shareability gates. No counterexample to the stated byte-shape rules was found.
- Claim 2 control: ordinary tables without RLS, aggregates, typed constants, repeatability, a table checksum, and execution inside READ ONLY held. The two RLS attacks isolate what that control misses.
- Claim 3: recognizer call closure and imports, raw-input SafeRead assignment, and lexer-hostile inputs. The AST test distinguishes the unit's SafeRead assignment from counter updates.
- Claim 4: all single-byte hint mutations, prefix truncations and offsets, opaque/rejected hints, transaction gates, and owned tenant-scoped keys.
- Claim 5: random bytes, lexer progress and spans, and the allocation ceiling held. The two CPU paths did not.
- Claim 6: genuinely opaque text without a recognized reject feature and unknown syntax forward. Local arguments hit the disclosed exception.
- Claim 7: examples read from the generated document, nested comment/whitespace padding, case variants, and all corpus reject entries embedded between other statements are refused whole.
- Claim 8: independently decoded frames in shuffled error-row order, accounting across repeated batches, append boundaries, invalid Ready status, and discard/Terminate/reuse ordering.
- Claim 9: document bytes, wire/document SQLSTATE agreement, live-table generator sensitivity, length/table visibility, and ledger/history checks.
- Claim 10: the Go tool's standard-library dependency flags, reflected output-type closure, and every production work file's imports/identifiers.
- Claim 11: independently read CSV final counts, a deliberately missing database counter (runner fails closed), cross-process byte identity at a nondefault 37 ms flush interval, seeded scenario checks, and a custom pipeline of write/refusal/write messages across windows of 1, 7, 31 and 65,536 bytes. All three pipelined messages are refused, neither surrounding write reaches the fake, and a subsequent permitted query reaches it.

All eleven claims were attacked at least three ways. There is no unattacked claim. The harness does not prove the universal assertions; a “held” result means these attacks found no break. Cancellation and reuse-after-close do not apply to the pure classifier/recognizer APIs; reply-discard termination and subsequent reuse are exercised. It does not rerun the entire project's landing gate, mutate product code, or repair any finding.

## Courtesy gaps

No new courtesy spelling gaps were sought or counted. The known forwarding gaps in the builder's NOTES.md remain outside the verdict. RLS appears above only because a text is marked SafeRead/Shareable despite hidden effects; that is a safe-shape safety claim, not a demand to enlarge the courtesy reject list.

## Scope and handoff

Only `agent_work/adversarial/c05-classifier/` was edited. The root STATE.md and agent_work/INDEX.md were intentionally untouched because the brief explicitly forbids editing them. The scoped STATE.md records the handoff. Durable findings are preserved here; no VM knowledge entry or out-of-scope docs/knowledge/work-scripts change is warranted by this adversarial assignment. The initial knowledge-store attempts failed before creating any search IDs because no store configuration existed at the documented locations; no corpus was opened directly.
