# Chunk 5 (reworked) — Statement classifier and the contracts: builder notes

Built by Opus on 2026-11-26 on branch `work/c05r-classifier` (worktree
`/root/Software/mini-gateway-work/c05r`) from `/root/briefs/zgateway-p2-c05r-classifier.md`,
under Nick's ruling of 2026-11-26 (`/root/briefs/zgateway-c05-ruling-reset-always.md`): the
proxy resets every database connection after every lease, so session safety no longer
depends on reading SQL. This is a rework of the first build (`work/c05-classifier`,
`d39ecfb`), whose records are under `parked/`. Durable facts for later builders:
`docs/knowledge/classifier.md`.

## What was built

| Deliverable | Where |
| --- | --- |
| Work vocabulary: `TxEffect`, `MayLeaveOpen` and `Unit.Tx` removed; `ReadOnly`/`Coalescable` replaced by `SafeRead`/`Shareable` | `internal/work/work.go`; `work_test.go` |
| Safe-shape recogniser (new, 196 lines, no imports, nothing shared with the lexer or classifier) | `internal/pgwire/share.go`; tests `share_test.go`, `share_tierr_test.go` |
| Generated `docs/safe-shape.md` | `internal/pgwire/share_doc.go`; `scripts/gen-docs.d/c05-contracts.sh` |
| Classifier: reject list kept as the courtesy check; the lexer-based read-only and coalescable proofs, the transaction-effect logic and the wide allow-list removed; `SafeRead` = `ShareFits`; hint = exact prefix | `internal/pgwire/classify.go`, `rejectlist.go`, `rejectlist_doc.go`, `snapshot_classifier.go` |
| Lexer: string literals continued across a newline are joined as Postgres does (review 2, finding 1) | `internal/pgwire/lex.go` (`lexContinuation`); `lex_continue_test.go` |
| Replies encoded with chunk 4's encoder (no `pgproto3`: `internal/pgwire` is in the determinism list since chunk 4); bytes unchanged | `internal/pgwire/reply.go` |
| Reject-list scenario world on chunk 4's framer, importable | `internal/pgwire/pgwiretest/clsworld/clsworld.go` |
| Collector source, component `classifier` | `internal/obs/clssrc/clssrc.go`; catalog `internal/obs/catalog/catalog.go`; `docs/csv-schema.md` regenerated |
| Flush harness for the two scenarios | `internal/obs/simrun/classifier.go` (`simrun.Classifier`); tests `internal/obs/simrun/classifier_test.go` |
| SQL over flushed files (G7) | `test/analysis/classifier-reject-only/{rejects-equal-sent,fakepg-statements-zero}.sql`, `test/analysis/classifier-reject-list/forwarded-equal-started.sql` |
| Shipped session oracle | `test/oracles/dirty-check.sql` (copy of the canvas experiment's `dirty-check.sql`) |
| Corpus restated | `internal/pgwire/testdata/classifier-corpus.txt` (599 records) |
| Gate extra (G10) | `agent_work/chunks/c05-classifier/gate/extra.sh` |

Lint rules: `tools/lint-rules/det-packages.txt` gains `internal/obs/clssrc` and
`internal/pgwire/pgwiretest/clsworld`; `tools/lint-rules/imports.txt` adds `internal/obs/clssrc`
to the mapping packages (no adapter, `simrun` or `catalog` import; adapters may not import
it), mirrored in `internal/obs/simrun/imports_test.go`; `tools/lint-rules/prose-paths.txt`
denies `docs/safe-shape.md` to the prose pass. No race-list entry (no concurrency).

## The work vocabulary (`internal/work`), field by field

| Type | Values / fields | Meaning |
| --- | --- | --- |
| `TenantID` | string | The tenant (D6: the startup `user`). |
| `Class` | `ClassNone` (never produced), `ClassReject`, `ClassStartLease`, `ClassContinueLease`, `ClassAnswerLocally` (reserved, never produced in v1), `NumClasses` | What the engine does with the unit. `Forwards()` is true for the two lease classes. |
| `Tri` | `No`, `Yes`, `Unknown` | Three-valued fact; used by `Outcome`. |
| `RejectReason` | `RejectNone` and 16 reasons (`session-set` … `extended-protocol`), `NumRejectReasons` | Why a unit is rejected; `String()` is the counter label. `extended-protocol` is for the wire layer, never produced by the classifier. |
| `KeyScope` | `ScopeGlobal`, `ScopeTenant` | Share-key scope; chunk 11 configures it (`ClassifierConfig.KeyScope`). |
| `ShareKey` | `Tenant`, `Text []byte`; `Equal` | `Text` is the exact `Query` text (a copy); `Tenant` empty in the global scope. |
| `Unit` | `Tenant`, `Class`, `Reason`, `SafeRead`, `Shareable`, `Hinted`, `Opaque`, `Statements`, `Key`, `HasKey` | One classified `Query` message (below). |
| `Release`, `ErrorClass`, `Outcome`, `Refusal` | unchanged | Outcome of a unit; refusal classes of the error contract. |

`Unit` fields:

- `Class`, `Reason` — reject (with the reason) when the courtesy check recognises a
  reject-list feature anywhere in the message; else start a lease (`inTx` false) or continue
  one (`inTx` true).
- `SafeRead` — `ShareFits(text)`: the text fits the safe shape. This is what the error
  contract means by "a statement that fits the safe shape" (`08006` against `08007`). It is
  reported for every message, rejected or not, in a transaction or not.
- `Shareable` — `SafeRead` and not `inTx` and not rejected. What `coalesce: auto` shares.
- `Hinted` — the text begins with the exact bytes `/* mg:share */`. Never sets `SafeRead` or
  `Shareable`.
- `Key`, `HasKey` — present when not `inTx`, not rejected, and `Shareable` or `Hinted`.
- `Opaque` — the lexer could not tokenise the message (it saw a bad token).
- `Statements` — non-empty statements after splitting on `;` (counter input).

Removed: `TxEffect` (`TxNone` … `TxUnknown`), `MayLeaveOpen` on `TxEffect` and `Unit`,
`Unit.Tx`, `ReadOnly`, `Coalescable`. In `internal/pgwire`: `AllowEntry`, `AllowList`,
`AllowLookup`, `AllowLookupBytes`, `ClassifShareHint`, `ClassifierMetric`,
`ClassifierMetrics`, `ReplyMetrics`, `ClassifComponent`, `ReplyComponent`, and the tables
`rejTemporalTypes`, `rejRelativeTimeWords`, `rejPerCallKeywords`, `rejWriteKeywords`,
`rejLockStrength`, `rejNonCallWords`, `rejOperators`, `rejModeledLeads` (kept as test data
in `classify_firstbuild_test.go` under `clsFirst…` names, so the "never" tests keep their
inputs).

## The safe shape (`internal/pgwire/share.go`)

`ShareFits(text []byte) bool`, a pure function over the raw bytes, no allocation, no import.
A text fits if and only if:

1. its length is 1 to `ShareMaxLen` = **4,096** bytes;
2. every byte is an ASCII letter, an ASCII digit, the space (0x20) or one of
   `_ , * = < > ( ) '`;
3. toggling at every `'`, the text ends outside a string;
4. the byte before every opening quote is not a letter, digit or `_`;
5. no string's content, lower-cased, contains `now`, `today`, `tomorrow`, `yesterday`
   (substring; the content of `'n''ow'` is `n'ow`, which does not match — the words contain
   no quote, so searching the raw span is the same as searching the unescaped content);
6. outside strings: nothing but spaces before the first word, and the first word is
   `select` (any case); no word on the stop list; every word followed (after optional spaces)
   by `(` is on the parenthesis-keyword list or the share allow-list.

Tables (generated into `docs/safe-shape.md`):

- **Stop list** (`ShareStopWords`): `into for with tablesample`, the statement-leading
  keywords of the brief (`insert` … `table`), and the per-call/per-session keywords of
  PostgreSQL's grammar, **ported from `src/backend/parser/gram.y`, production
  `func_expr_common_subexpr`** (the SQL-value functions written without parentheses):
  `current_date current_time current_timestamp localtime localtimestamp current_role
  current_user session_user system_user user current_catalog current_schema`.
- **Parenthesis keywords** (`ShareParenKeywords`): `select from where and or not in on exists
  any all some when then else having union intersect except as limit offset distinct between
  case`. Decision: the brief's list **minus `by`, `join`, `like`, `is`**, because Postgres 17
  lists those as unreserved (`by`) or type/function-name (`join`, `like`, `is`) keywords
  (`pg_get_keywords()` category `U`/`T`), which an unqualified function call may use as its
  name; every word kept is category `R` or `C`, which no unqualified call can use
  (`shrTierRKeywordCategories` checks this on Postgres 17). `over` and `filter` (category `U`)
  are not added, so window functions and `FILTER` do not fit.
- **Share allow-list** (`ShareAllowList`, 66 names): aggregates (`avg bit_and bit_or
  bool_and bool_or count every max min string_agg sum`), plain string functions, math
  functions, `coalesce nullif greatest least`. It is a subset of the first build's
  coalescable list (135 names). Removed as doubtful: window functions (they need `OVER (`),
  array and JSON functions (they need `[`, `::` or produce output shaped by settings),
  every date/time function (`date_trunc`, `date_part`, `extract`, `make_*`, `to_char`,
  `to_date`, `to_timestamp`, `to_number`: time zone, locale and coercion of `'now'`-like
  literals), `encode`/`decode` (`bytea_output`), `format` and `quote_*`, `regexp_*`, the
  SQL-syntax functions with keyword arguments (`substring`, `trim`, `overlay`, `position`),
  set-returning functions (`generate_series`, `unnest`), `current_database`, `version`,
  `width_bucket`, `cardinality`, `grouping`, `row_to_json`, `to_json(b)`.

The hint (`ShareHint = "/* mg:share */"`) contains `/` and `*`, so a hinted text never fits.

## The classifier and the courtesy check

`Classifier.Classify(tenant, text, inTx)` and `ClassifyQuery(text, inTx)`. Order: lex
(significant tokens; `Opaque` if a bad token); split on `;`; reject when any statement
matches a reject rule (first reason wins; runs over every token the lexer produced, also
around a bad token); otherwise forward. `SafeRead = ShareFits(text)`, `Hinted =
HasPrefix(text, ShareHint)` are computed from the bytes, independently of the lexer.

The reject rules are the first build's, unchanged (see `parked/NOTES.md`, "Which statements
are rejected", and `docs/reject-list.md`), with the transaction-effect logic and the proofs
removed. Two changes:

- **String continuation (review 2, finding 1, fixed).** After the closing quote of any
  `'…'`-family string (`'…'`, `E'…'`, `N'…'`, `B'…'`, `X'…'`, `U&'…'`), the lexer looks for
  whitespace and `--` comments containing at least one `\n` or `\r`, then `'`; if found, the
  string continues in the same escape mode. Block comments do not count. Each rule was
  checked on Postgres 17.11 over the wire (a raw `Query` message): `'a'\n'b'`, `'a' -- c\n'b'`,
  `'a'\v\n'b'`, `'a'\r'b'` join; `'a' /*\n*/ 'b'`, `'a'\nE'b'`, `$$a$$\n'b'`, `'a' 'b'` do not
  (syntax errors); `E'ok'\n'\' ; SELECT ' ; SELECT 2 ; --'` is one escape string followed by
  a real `SELECT 2`. Regression: `lex_continue_test.go`, corpus records, tier R canary. A
  continued string used as a `SET LOCAL search_path` / `set_config('search_path', …)` value or
  as a `set_config` setting name is treated as unreadable (`clsContinued`), so
  `'pg_'\n'temp'` is rejected as temp-object and `set_config('search_'\n'path', …)` as
  set-config.
- **Review 2, finding 2 (temporal range, multirange and array types in the coalescable
  proof)** disappears with the proof: none of `('[' || 'n' || 'ow,)')::tstzrange` and friends
  fits the safe shape (`|`, `:`, `[` are outside the alphabet); they are kept as never-shared
  regressions in `lex_continue_test.go`.

## Decisions the brief left open

1. `ShareMaxLen` = 4,096 bytes (the benchmark's hot-key read is 30 bytes; a few kilobytes as
   asked).
2. The parenthesis-keyword list as above (`by`, `join`, `like`, `is` dropped; reason given).
3. The share allow-list as above.
4. Rule 6, stricter reading: "the first word is `select`" is read as "the text starts with
   optional spaces and then the word `select`", so `(SELECT 1)` and `'a' SELECT` do not fit.
5. `Opaque` means exactly "the lexer saw a bad token" and is counted also when the message is
   rejected; the first build's separate `Unclassifiable` counter is merged into it.
6. `Hinted` is the exact prefix only; the first build's trimmed-comment reading
   (`/*mg:share*/`, leading spaces) is gone, and two corpus records lost their `hint` flag.
7. A hinted message gets a key (not `inTx`, not rejected) even when `Opaque`; it is the
   client's own statement (D10, `hinted` mode).
8. `SafeRead` is reported for rejected and in-transaction messages too; only `Shareable`
   depends on `inTx` and the class.
9. The classifier snapshot gains `Keys` (share keys produced); `Check` holds shareable ≤
   safe-read and keys ≤ shareable + hinted.
10. Reply and discard-until-Sync counters are exported under component `classifier` (one
    source with the classifier's), as `replies{reason=row id}`, `replies_built`,
    `reply_failures`, `extended_refused`, `extended_discarded`, `extended_synced`.
    `replies_total` would collide with the Prometheus name of counter `replies`.
11. "Statements sent" in the flushed files is `wire.client_queries_seen` of chunk 4's
    `FrontendTracker` in the scenario gateway (the framer counts every `Query` the clients
    sent; in the simulator nothing is lost between them). `clsworld.Result.Verify` also checks
    it equals the probes' own count.
12. The SQL files move from `test/analysis/c05-reject-list/` to the scenario-named folders
    `test/analysis/classifier-reject-only/` (both first-build files, rewritten to the real
    schema) and `test/analysis/classifier-reject-list/` (new), because `csv-assert.sh` root
    mode pairs `test/analysis/<scenario>/` with `<root>/<scenario>/`.
13. The `lost-after-read` error row's situation text now says "a statement that fits the
    safe shape (SafeRead)" instead of "classified read-only" (vocabulary only; SQLSTATE,
    message and contract facts unchanged).
14. `reply.go` encodes with `AppendReadyForQuery` and chunk 4's `beginMessage` /
    `appendCString` / `finishMessage` instead of `pgproto3` (detlint refuses the import now
    that `internal/pgwire` is in the determinism list); `TestReplyGoldenBytes` and
    `TestReplyBytesMatchAHandEncoding` show the bytes are unchanged.
15. The planted-bug switch stays the unexported `ClassifierConfig.plantSetForwardable`; the
    external tests reach it through `classify_export_test.go` (`PlantSetForwardable`) and
    `clsworld.Scenario.Classifier`. The safe-shape canary is a parameter of the unexported
    `shareFits(text, skipAlphabet)`; `ShareFits` always passes `false`.

## The join with what landed since

1. **Import cycle.** `classify_e2e_test.go` is now `package pgwire_test` and drives
   `clsworld`; the fake-dependent half of `classify_obs_test.go` moved to
   `classify_obs_ext_test.go` (external). `classify_export_test.go` exports only the plant.
2. **Chunk 4's framer.** The gateway frames the startup packet with `pgwire.Framer{Untyped:
   true}`, client messages with `pgwire.FrontendTracker` (with a `WireStats`), and database
   messages with `pgwire.Framer` to follow `ReadyForQuery`; forwarded messages are encoded
   with `AppendQuery`/`AppendTerminate`. Chunk 4's session machines are **not** used: the
   startup is relayed to the fake, which runs trust authentication (a small change it is
   not: it would need tenant tables, SCRAM and a pool login; chunk 6 replaces the harness).
3. **Collector source.** `clssrc.Metrics()` declared in `catalog.Registry()`;
   `docs/csv-schema.md` regenerated; the declared/emitted tests of chunk 3 pass
   (`TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` sees the classifier runs
   in the shared baseline root).
4. **Flushed scenarios.** `simrun.Classifier(sc, seed, opts)` registers sim, simnet, fakepg,
   classifier and wire sources, publishes at t=0, every 100 ms of virtual time and at the
   end; run id `<scenario>/<seed>/base` under `$RUN_DIR` when set.
   `TestScenarioClassifierFlush`: both scenarios on the pinned seeds, `Verify`, `Run.Check`
   (CSV round trip, JSON, Prometheus grammar, log lines), byte-identical files and adapter
   output on a second run, equal trace digests, exported values equal to the snapshots. G4
   runs it under the sweep's `$RUN_DIR`, so G7 runs the SQL files over it.
5. **SQL files** (each starts with `-- requires:`, checks `schema_version`, groups by run,
   returns zero rows when correct, one row when no run of its scenario exists):
   `rejects-equal-sent.sql` (classifier rejects = statements sent ≥ 1 = classified),
   `fakepg-statements-zero.sql` (fake statements started = 0, fake startups ≥ 1, rejects ≥
   1), `forwarded-equal-started.sql` (classified = sent; rejects + forwarded = classified;
   fake statements started = forwarded ≥ 1; rejects ≥ 1). Each fails on a planted wrong
   number: `TestClassifierSQLCatchesPlantedNumbers` (eight plants, each makes the runner
   exit non-zero with "violating rows" and for no other reason).
6. **Gate extra** updated (see "Commands").

## Observability

- `Classifier.Snapshot()` → `ClassifierSnapshot{Classified, ByClass, ByReason, Opaque,
  SafeRead, Shareable, Hinted, Keys, Statements}`; `Check()`: classified = sum over classes;
  rejects = sum over reasons; shareable ≤ safe-read ≤ classified; opaque, hinted ≤
  classified; keys ≤ shareable + hinted. `ReplyBuilder.Snapshot()` (`ByRow`, `Total`,
  `Failed`) and `SyncDiscard.Snapshot()` unchanged.
- Exported through `clssrc` (component `classifier`; every label value emitted, zero
  included): `statements` (headline), `statements_by_class{outcome}`, `rejects{reason}`,
  `opaque`, `safe_read`, `shareable`, `hinted`, `share_keys`, `sql_statements`,
  `replies{reason}`, `replies_built`, `reply_failures`, `extended_refused`,
  `extended_discarded`, `extended_synced`. All counters.
- Tests: `classify_obs_test.go: TestClassifierSnapshotCountersMoveAndConserve` (every class
  and reason the classifier produces and every counter move over the corpus; `Check` catches
  three planted inconsistencies); `clssrc_test.go: TestMappingMovesEveryMetricAndExportsEveryField`,
  `TestZeroSnapshotEmitsEveryLabelValue`, `TestReplyMetricsCoverEveryRow`,
  `TestClassifierCountsReachTheExport`; `simrun/classifier_test.go: checkClassifierCounts`.

## Steady-state row and memory per classification

Nothing grows at run time: counters are fixed-size arrays; the classifier, the recogniser and
the builders keep no per-message state.

| What | Bound | Test |
| --- | --- | --- |
| Memory per classification | at most `ClassifMemoryFactor` (80) bytes per input byte + `ClassifMemoryConstant` (64 KiB); measured worst 52 B/byte at 1 MiB | `TestClassifierMemoryIsBounded`, `TestClassifierAllocationStaysUnderTheCeiling` |
| Safe-shape recogniser | allocates nothing; reads at most `ShareMaxLen` bytes past the length check | `TestSafeShapeAllocatesNothing` |
| Time | linear: 16 times the input takes at most 96 times as long (measured 13–29) over nine adversarial shapes, and over eight operator-run and six repeated-name shapes (measured 9–29 after the repair round; 117–252 before it) | `TestClassifierTimeGrowsLinearly`, `TestLexOperatorRunsGrowLinearly`, `TestClassifierSpanKeywordChecksGrowLinearly` |
| Scenario gateway buffers | `clsworld.MaxBuffer` (1 MiB) per direction, then the connection is closed and the run fails | `clsworld` |

## Claims and the tests that prove them

| # | Claim | Tests (file: function) |
| --- | --- | --- |
| 1 | Safe shape is exact | `share_test.go`: `TestSafeShapeTable` (each rule violated alone), `TestSafeShapePropertyFitsAgreesWithTheReferenceChecker` (200,000 seeded texts, 15,817 fit: `ShareFits` equals an independent reference checker in both directions), `TestSafeShapePropertyAnyByteOutsideTheAlphabetBreaksAFit`, `TestSafeShapePropertyGrammarTextsFit` (5,000 texts from a grammar over the allow-list), `TestSafeShapeEveryStopWordAndUnlistedCallBreaksAFit`, `FuzzSafeShape`; `classify_test.go`: `clsCheckInvariants` in `TestClassifierInvariantsOverMutants` and `FuzzClassify` (`SafeRead` = `ShareFits`; `Shareable` = fits ∧ ¬inTx ∧ ¬rejected; key only by the rule), `TestClassifierCorpus` |
| 2 | Safe shape is safe on a real server (narrowed form under "Narrowed claims") | `share_tierr_test.go: TestTierRSafeShapeDifferential` — Postgres 17, plain tables without row-level security policies: 81 corpus/table texts, 400 generated texts and 6 texts on a 200,000-row table `big` (5 of 6 get a `Parallel Seq Scan` plan, checked by `shrTierRParallelPlan`, which fails if none does) that fit give the same outcome inside `BEGIN READ ONLY` (never `25006`), leave the tables' checksum unchanged, leave the session clean by `test/oracles/dirty-check.sql`, and return the same multiset of rows twice (`shrRowMultiset`) (37 end in the same Postgres error each time, such as `to_hex(numeric)`). Canaries: each oracle catches its hazard (`SET`, `LISTEN`, advisory lock → dirty check; `PREPARE`, temp table, holdable cursor, `INSERT`, `random()`, `clock_timestamp()`, `'now'` → re-run; `DELETE`, `UPDATE` → read-only; an `UPDATE` moves the checksum), and none fits; Postgres resolves `'now'`/`'today'` to the current time; every parenthesis keyword is category `R` or `C` on Postgres 17 |
| 3 | The recogniser does not depend on the lexer | `share_test.go: TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier` (go/ast: `share.go` imports nothing, references no identifier declared in `lex.go`, `classify.go`, `rejectlist.go`, nor any package-level identifier it does not declare). A lexer bug cannot widen: `SafeRead` is `ShareFits(text)` and `Shareable` requires it; the lexer can only add a reject, which removes `Shareable` (`clsCheckInvariants`); the property "fits ⇒ the lexer tokenises it as one statement" holds (`shrCheckLexerAgrees` in the property, grammar and fuzz tests) |
| 4 | The hint never widens | `classify_test.go: TestClassifierShareKeyAndScope` (exact prefix only; hinted `SELECT now()` has a key but is neither safe-read nor shareable; no key inside a transaction or when rejected), `clsCheckInvariants` (`Hinted` = prefix), `share_test.go: TestSafeShapeTable` ("2 alphabet: hint"), `classify_attacks_test.go: TestClassifierShareKeyIsACopyAndScoped` |
| 5 | No panic, bounded work | `FuzzLex`, `FuzzClassify`, `FuzzSafeShape` (gate G5, 20 s each), `classify_attacks_test.go: TestClassifierEveryBytePairNeverPanicsAndCoversTheInput`, `TestClassifierSeededHostileBytesNeverPanic`, `TestClassifierAllocationStaysUnderTheCeiling`; `classify_test.go: TestClassifierMemoryIsBounded`, `TestClassifierTimeGrowsLinearly`, `TestLexOperatorRunsGrowLinearly` (eight operator-run shapes), `TestClassifierSpanKeywordChecksGrowLinearly` (six repeated-name shapes), `TestClassifierGeneratedStatements`; `share_test.go: TestSafeShapeAllocatesNothing` |
| 6 | Nothing is refused for being unknown (precise form under "Narrowed claims") | `classify_repair_test.go: TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade` (`temp`/`temporary` used as names are forwarded; every temporary-object form is refused); `classify_test.go: TestClassifierConservativeWhenUntokenisable` (mutants the lexer cannot tokenise: opaque, forwarded unless a reject-list feature was read, never safe-read or shareable), `classify_attacks_test.go: TestClassifierForwardsTruncatedMessagesAsOpaque`, `TestClassifierForwardsBadBytesBetweenStatementsAsOpaque`, `TestClassifierRejectWinsOverUntokenisable`; corpus `bad` records |
| 7 | The courtesy list refuses what it documents | `rejectlist_test.go: TestRejectRulesExamplesClassifyAsDocumented`; `classify_test.go: TestClassifierCorpus` (190 reject records, exact reason), `TestClassifierPaddingAndCaseProperty` (6 seeds × every record: class, reason, opaque and statement count invariant under whitespace, comments and keyword case); `classify_spellings_test.go: TestClassifierSpellingsAlwaysRejectTheRejectList`; `classify_attacks_test.go: TestClassifierRejectsUnicodeAndQuotedAdvisoryLockNames`, `TestClassifierRejectsTheWholeMessageForOneRejectedStatement`, `TestClassifierPaddingInvariant*`; `lex_continue_test.go: TestClassifierRejectsWhatHidesBehindAStringContinuation` |
| 8 | Every proxy-made reply is well formed | `errors_test.go`: `TestErrorRowsAreWellFormed`, `TestReplyGoldenBytes`, `TestReplyBytesMatchAHandEncoding`, `TestReplyEveryRowIsWellFormedWithTheRightMarker`, `TestReplyRefusesBadInput` (a refused build counts in `Failed` only, never in `Total` or `ByRow`), `TestSyncDiscardRule`, `TestSyncDiscardRepliesEndWithReadyAfterSync`; `classify_attacks_test.go: TestReplyEveryRowWithPrefixAndStatusIsWellFormed`, `TestReplyInvalidInputsDoNotPoisonReuse`, `TestSyncDiscardDropsEveryByteUntilSyncAndRecovers` |
| 9 | Generated documents cannot drift | `rejectlist_test.go: TestClassifierContractDocsMatchTheCode` (`error-contract.md`, `reject-list.md`, `safe-shape.md`), gate G8 `scripts/gen-docs.sh --check`, `classify_attacks_test.go: TestContractGeneratorsTrackTableMutation` |
| 10 | `internal/work` imports only the standard library; output in its types | `work_test.go: TestWorkImportsOnlyTheStandardLibrary`, `TestWorkImportClosureIsTheStandardLibrary`; `classify_attacks_test.go: TestClassifierOutputIsExpressedInWorkTypesOnly`; gate G1 importlint and detlint |
| 11 | Refused statements never reach the database, and the flushed files say so | `classify_e2e_test.go: TestScenarioClassifierRejectList`, `TestScenarioClassifierRejectOnly` (sweep, replay), `TestClassifierPlantedSetForwardableIsCaught` (canary: with `SET` forwardable the scenario fails with "a rejected statement reached the database" and "session state changed"), `TestClassifierRejectOnlyReachesNothing`; `classify_attacks_test.go: TestClassifier*NeverReachesTheFake` (with ablation); `simrun/classifier_test.go: TestScenarioClassifierFlush`, `TestClassifierSQLCatchesPlantedNumbers`; the three SQL files in G7 |

## Narrowed claims

Three claims of the brief were broader than the code and than what any reader of query text
can promise. The code is right in each; the claim is narrowed here (repair round, rows R3,
R4, R5 of `RECONCILIATION.md`). The orchestrator decides whether to accept each.

- **Claim 2, narrowed for row-level security (R3).** On Postgres 17, for a schema of plain
  tables *without row-level security policies* (no `CREATE POLICY` on a table the text
  reads, or row-level security not enabled on it, or the reading role bypasses it), no text
  that fits the safe shape writes, changes session state, or returns a different multiset
  of rows on an immediate re-run. A policy expression runs on every read and may call
  `set_config` or anything else a policy may call; it is not visible in the query text, so
  no recogniser of text can exclude it. A database whose tables carry policies should run
  `coalesce: off` or `hinted`. (PLAN.md states this limit since `3881f72`.)
- **Claim 2, narrowed for valid re-runs (R4).** What repeats is the result *as a multiset
  of rows*, and only for queries whose value SQL defines. Two valid runs of the same
  safe-shape text may differ in: row order where the text has no `ORDER BY` (on a table
  past `min_parallel_table_scan_size`, 8 MB by default, Postgres 17 plans `Gather` over a
  parallel scan and returns blocks in worker order); the value of an aggregate that depends
  on input order (`string_agg`, `array_agg` without `ORDER BY` inside the call); and the
  value of a floating-point aggregate (`sum`, `avg` over `real`/`double precision`: IEEE-754
  addition is not associative across worker completion order). Each run is a valid
  evaluation of the query, so handing one valid result to all identical reads in flight is
  correct; the safe shape is not changed. The tier R oracle now holds a 200,000-row table
  and compares re-runs as multisets of rows, on texts with exact types only.
- **Claim 6, precise form (R5).** A message is never refused because it is untokenisable,
  unmodelled or unknown, with three exceptions where a reject-list feature *is* recognised
  but part of it cannot be read: (a) a reject-list feature in the readable part of an
  untokenisable message is refused (`DISCARD ALL; SELECT 'unfinished`); (b) a
  `set_config(name, value, is_local)` call whose setting name is not one plain `'…'` string
  literal (an expression, a parameter, an `E'…'`/`U&'…'`/dollar string, or a string
  continued across a newline) is refused as `set-config`, because it might name a lexing
  setting (`SELECT set_config(lower('application_name'), 'x', true)`); (c) a
  transaction-local `search_path` value that the classifier cannot read is refused as
  `temp-object`, because it might name `pg_temp`: `SET LOCAL search_path`/`SET LOCAL
  SCHEMA` with an `E'…'` value holding a backslash, a `U&'…'` value with an escape or
  `UESCAPE`, or a continued string; and `set_config('search_path', value, true)` whose value
  is not one plain `'…'` string literal (`SELECT set_config('search_path', lower('public'),
  true)`). These are deliberate (kept from the first build) and listed under "Deviations".

## Deviations

- **Claim 6, precise form** (stated in full under "Narrowed claims"). "Nothing is refused for being unknown" holds in this form: a
  message is never refused because it is untokenisable or unmodelled. Three first-build
  behaviours, kept as the brief asks ("keep the first build's behaviour"), refuse a message
  in which a reject-list feature *is* recognised but some part cannot be read: (a) a
  reject-list feature in the readable part of an untokenisable message is refused (`DISCARD
  ALL; SELECT 'unfinished`; Postgres would run nothing of it anyway); (b) `set_config` whose
  setting name is not one plain `'…'` literal (it might name a lexing setting); (c) a
  transaction-local `search_path` value that cannot be read (it might name `pg_temp`). Under
  ruling 9 (b) and (c) protect nothing that the reset does not; they remain courtesies.
- **SQL files moved** to scenario-named folders (decision 12).
- **Chunk 4's session machines are not used** in the scenario harness (join item 2): framer
  and trackers only.
- **The scenario harness is product-shaped Go** (`clsworld`, 571 lines) where the first
  build had about 350 lines of test-only harness: `simrun` must import it, and test files
  cannot be imported.
- **The padding property is split**: the courtesy fields (class, reason, opaque, statement
  count) are invariant under any whitespace, comment and case padding; `SafeRead` depends on
  the bytes by design (a tab or a comment breaks a fit), so for it the property is "padding
  with single spaces and keyword case never breaks a fit".
- **`TestAdv_1_EscapeContinuation` and `TestAdv_10_EscapeContinuationLeak` are restated.**
  Their inputs (`SELECT E'ok'\n'\'; SET application_name = 'adv_escape'; --'`) relied on the
  first build's wrong lexing: on Postgres 17 the first is a syntax error and the second
  selects one string; neither changes the session (checked over the wire and in
  `shrTierRContinuation`). With the fidelity fix the classifier agrees with Postgres and
  forwards them. The hoisted tests assert that, and assert that the form which really hides a
  `SET` behind a continuation (`SELECT E'ok'\n'\' ; SELECT ' ; SET … ; --'`) is rejected.

## Stated limits

**The safe shape** (for the prose seat and the book):

1. It trusts the objects it names. A view (system views such as `pg_stat_activity`,
   `pg_locks` included: their rows change between calls), a user-defined operator behind
   `=`, `<`, `>`, `<=`, `>=`, `<>`, `*`, a user function that shadows an allow-listed name by
   argument type in the search path (`public.lower(integer)`), a foreign table, or a table
   with rules can hide a side effect or a per-call result. Claim 2 is over plain tables only.
   `coalesce: off` and `hinted` exist for databases where that matters.
   A **row-level security policy** on an ordinary table is in the same class: its
   expression runs on every read and can change session state or the rows of a re-run;
   claim 2 excludes tables with policies (see "Narrowed claims").
2. Values that depend on session settings (`TimeZone`, `DateStyle`, `lc_*`,
   `extra_float_digits`) are assumed equal across the pool's connections: every lease ends
   with a reset to the pool's defaults.
3. A typed literal such as `date '2020-01-01'` fits (rule 4 only refuses a word glued to the
   quote); its string is checked for the relative-time words, which is why `date 'today'`
   does not fit. Other special input values (`'epoch'`, `'infinity'`, `'allballs'`) are
   constants.
4. A text that fits may still be invalid SQL; Postgres then answers with the same error every
   time, which is a shareable outcome.
5. Re-runs repeat the result as a **multiset of rows**. Row order without `ORDER BY`,
   aggregates that depend on input order (`string_agg`, `array_agg` without an inner
   `ORDER BY`) and floating-point aggregates (`sum`/`avg` over `double precision`) may
   differ between two valid runs once Postgres picks a parallel plan (a table past 8 MB by
   default). Sharing hands every identical read in flight one of those valid results.
6. The benchmark's hot-key read `SELECT v FROM kv WHERE k = '…'` fits for any key made of
   letters, digits, spaces and `_ , * = < > ( )` that holds none of the four time words.
   Qualified names (`kv.v`, `public.kv`), casts (`::`), comments, multi-line text, `$1`
   parameters, `IN`-lists of strings with `'now'`-like words, window functions and `JOIN (…)`
   do not fit; they are shared only in `hinted` mode.

**The courtesy list** (`docs/reject-list.md`):

1. It is best effort. The classifier is a lexer with some pattern matching, not a parser. A
   function, procedure, `DO` block, view, rule, trigger, default or type input function that
   performs a session change is not seen; neither is any spelling not in the corpus or the
   spelling generator. Such a statement is forwarded, works until the end of its
   transaction, and is removed by the reset (D8).
2. The lexer assumes `standard_conforming_strings = on` and the pool's client encoding (the
   reset restores both; changing either is on the reject list).
3. The first build's lexer limits 4–8 still hold (ASCII case folding, 63-byte names,
   Postgres 16+ numbers, splitting on every `;`, the listed over-rejections) —
   `parked/NOTES.md`, "Stated limits of the classifier".

## Courtesy gaps known

Forwarded although they change session state (all cleaned by the reset; none is a defect in
session safety):

- `DO $$ BEGIN EXECUTE 'SET search_path = x'; END $$` and any function or procedure that
  calls `set_config`, `SET`, `LISTEN`, `PREPARE` or takes a session advisory lock.
- `CREATE FUNCTION … SET search_path = …` then calling it (the setting is per call, but the
  function is persistent — not session state at all; listed because it is often mistaken
  for one).
- `setseed()` and the sequence cache behind `currval`/`lastval`; large-object descriptors
  (transaction-scoped anyway); `ALTER ROLE/DATABASE/SYSTEM … SET` (persistent).
- `SELECT set_config(…)` reached through a view or a rule.
- Any reject-list spelling not covered by the corpus or `classify_spellings_test.go`.
- A `UESCAPE` literal continued across a newline in a Unicode-escaped name:
  `SELECT U&"pg_advisory_!006cock" UESCAPE '!'\n'' (1)` takes a session advisory lock and is
  forwarded. The classifier reads the escape character from the raw `UESCAPE` token (three
  bytes `'!'`), and a continued literal is longer, so the name is not decoded (Neon's
  courtesy gap 1; claim 7; no new reject rule in this chunk, RECONCILIATION R11).

## Test ledger

First build: 46 test and fuzz functions in `internal/pgwire` and `internal/work` (plus the
stopped probe `TestClassifierArbProbe`); the adversary: 32 attacks. "Kept" tests stay in
their file unless a new location is given.

| Test | Old file | Outcome | New location | Reason (if not simply kept) |
| --- | --- | --- | --- | --- |
| `TestScenarioClassifierRejectList` | `classify_e2e_test.go` | kept | same file, `package pgwire_test` | import cycle (the fake imports `pgwire`); scenario in `clsworld` |
| `TestScenarioClassifierRejectOnly` | `classify_e2e_test.go` | kept | same | as above |
| `TestClassifierPlantedSetForwardableIsCaught` | `classify_e2e_test.go` | kept | same | plant through `PlantSetForwardable` |
| `TestClassifierRejectOnlyFlushNumbers` | `classify_e2e_test.go` | kept, renamed `TestClassifierRejectOnlyReachesNothing` | same | `ClassifierMetrics` is gone; the flushed-number half is the SQL files, run by `TestCSVAssertBaselineCountsFromFlushedFiles` and G7 |
| `TestClassifierSnapshotCountersMoveAndConserve` | `classify_obs_test.go` | kept | same | may-leave-open, read-only and coalescable assertions cut; safe-read, shareable, keys added |
| `TestReplyMetricsCoverEveryRow` | `classify_obs_test.go` | kept, moved | `internal/obs/clssrc/clssrc_test.go` | `ReplyMetrics` replaced by the `clssrc` mapping; since the repair round it builds every row (row k, k+1 times) and checks `replies{reason}` for every row, `replies_built` and `reply_failures` (before, it built one row only) |
| `TestClassifierReadOnlyCorpusLeavesTheFakeUnchanged` | `classify_obs_test.go` | kept, renamed `TestClassifierSafeReadCorpusLeavesTheFakeUnchanged` | `classify_obs_ext_test.go` | re-aimed at `SafeRead`; external for the import cycle |
| `TestClassifierSchemaQualifiedKeywordNamesAreCalls` | `classify_repair_test.go` | kept | same | restated: never safe-read/shareable; the positive half asserts "forwarded" only |
| `TestClassifierAttributeCallAfterExpressionFrom` | `classify_repair_test.go` | kept | same | restated as above |
| `TestClassifierSearchPathStringNamingTempSchema` | `classify_repair_test.go` | kept | same | |
| `TestClassifierExplainQuotedAnalyzeOption` | `classify_repair_test.go` | kept | same | restated: never safe-read/shareable |
| `TestClassifierTimeDependentLiteralsAreNotCoalescable` | `classify_repair_test.go` | kept | same | restated: never shareable and never safe-read |
| `TestLexOperatorTrailingSignFollowsPostgres` | `classify_repair_test.go` | kept | same | read-only assertion cut |
| `TestClassifierSpellingsNeverProveUnlistedCalls` | `classify_spellings_test.go` | kept | same | restated: never safe-read/shareable; first-build lists from `classify_firstbuild_test.go` |
| `TestClassifierSpellingsNeverCoalesceNonCoalescableCalls` | `classify_spellings_test.go` | kept | same | restated as above |
| `TestClassifierSpellingsAlwaysRejectTheRejectList` | `classify_spellings_test.go` | kept | same | |
| `TestClassifierCorpus` | `classify_test.go` | kept | same | corpus restated (`fits`, `was-co`, `new`); transaction column dropped; "never unsafe": no record without `was-co`/`new` may fit |
| `TestClassifierGeneratedStatements` | `classify_test.go` | kept | same | expectations restated; two fitting long cases added |
| `TestClassifierEmptyMessages` | `classify_test.go` | kept | same | transaction assertions cut |
| `TestClassifierShareKeyAndScope` | `classify_test.go` | kept | same | hint cases added |
| `TestClassifierPaddingAndCaseProperty` | `classify_test.go` | kept | same | split, see Deviations |
| `TestClassifierConservativeWhenUntokenisable` | `classify_test.go` | kept | same | restated: opaque, never safe-read/shareable |
| `TestClassifierInvariantsOverMutants` | `classify_test.go` | kept | same | invariants restated for the new fields |
| `TestClassifierMemoryIsBounded` | `classify_test.go` | kept | same | |
| `FuzzClassify` | `classify_test.go` | kept | same | |
| `TestTierRClassifierDifferential` | `classify_tierr_test.go` | kept | same | "leaves no transaction open" half retired with the flag; read-only half re-aimed at `TestTierRSafeShapeDifferential`; session oracle and repair canaries kept |
| `TestLexTokenKinds` | `lex_test.go` | kept | same | |
| `TestLexUnicodeDecode` | `lex_test.go` | kept | same | |
| `TestLexSpansCoverTheCorpus` | `lex_test.go` | kept | same | |
| `FuzzLex` | `lex_test.go` | kept | same | |
| `TestRejectRulesExamplesClassifyAsDocumented` | `rejectlist_test.go` | kept | same | |
| `TestAllowListIsSortedAndCoalesceIsASubset` | `rejectlist_test.go` | kept, renamed `TestShareAllowListIsSortedAndExcludesPerCallFunctions` | same | the wide list is gone; the first build's non-coalescable names must stay off the share list |
| `TestClassifierWriteContractDocs` | `rejectlist_test.go` | kept | same | also writes `safe-shape.md` |
| `TestClassifierContractDocsMatchTheCode` | `rejectlist_test.go` | kept | same | also holds `safe-shape.md` |
| `TestErrorRowsAreWellFormed` | `errors_test.go` | kept | same | |
| `TestErrRowForEveryRefusal` | `errors_test.go` | kept | same | |
| `TestReplyGoldenBytes` | `errors_test.go` | kept | same | |
| `TestReplyBytesMatchAHandEncoding` | `errors_test.go` | kept | same | |
| `TestReplyEveryRowIsWellFormedWithTheRightMarker` | `errors_test.go` | kept | same | |
| `TestReplyRefusesBadInput` | `errors_test.go` | kept | same | |
| `TestSyncDiscardRule` | `errors_test.go` | kept | same | |
| `TestSyncDiscardRepliesEndWithReadyAfterSync` | `errors_test.go` | kept | same | |
| `TestWorkImportsOnlyTheStandardLibrary` | `work_test.go` | kept | same | |
| `TestNamesCoverEveryValue` | `work_test.go` | kept | same | transaction-effect loop cut |
| `TestMayLeaveOpen` | `work_test.go` | kept, renamed `TestClassForwards` | same | the `MayLeaveOpen` half retired with the flag; the `Forwards` half kept |
| `TestShareKeyEqual` | `work_test.go` | kept | same | |
| `TestClassifierArbProbe` | `parked/arbprobe_test.go.txt` | hoisted | `lex_continue_test.go`: `TestLexStringContinuesAcrossANewline`, `TestClassifierRejectsWhatHidesBehindAStringContinuation` | a logging probe; its inputs became assertions: B1–B4 and B6–B8 rejected or never shared, B5 (`SELECT E'ok'\n'\' ; SELECT ' ; BEGIN ; --'`) never shared (since `5356992`); E1–E3 rejected, E4 (`SET LOCAL search_path = 'pg_'\n'temp'`) rejected as its own case (since `5356992`) and inside B6; E0 and E5–E9 and M1–M9 never shared; `'a' 'b'` dropped: it fits and is a plain syntax error |
| `TestAdv_1_UnicodeFunctionNames` | `attacks_test.go` | hoisted | `classify_attacks_test.go: TestClassifierRejectsUnicodeAndQuotedAdvisoryLockNames` | |
| `TestAdv_1_WholeMessageRejection` | `attacks_test.go` | hoisted | `TestClassifierRejectsTheWholeMessageForOneRejectedStatement` | |
| `TestAdv_1_EscapeContinuation` | `attacks_test.go` | hoisted | `TestClassifierEscapeContinuationFollowsPostgres` + tier R `shrTierRContinuation` | restated, see Deviations |
| `TestAdv_2_UnknownCallsInGrammar` | `attacks_test.go` | hoisted | `TestClassifierNeverSharesUnknownCallsInGrammarPositions` | restated: never safe-read/shareable |
| `TestAdv_2_NestedWriteAndLock` | `attacks_test.go` | hoisted | `TestClassifierNeverSharesNestedWritesAndLocks` | restated as above |
| `TestAdv_2_TimeDependentLiterals` | `attacks_test.go` | hoisted | `TestClassifierNeverSharesTimeDependentLiterals` + tier R oracle in `shrTierRCanaries` | |
| `TestAdv_2_ShareKeyOwnershipAndScope` | `attacks_test.go` | hoisted | `TestClassifierShareKeyIsACopyAndScoped` | |
| `TestAdv_3_TruncatedRead` | `attacks_test.go` | hoisted | `TestClassifierForwardsTruncatedMessagesAsOpaque` | |
| `TestAdv_3_RejectBeforeLexError` | `attacks_test.go` | hoisted | `TestClassifierRejectWinsOverUntokenisable` | asserts the narrowed claim the orchestrator accepted (it was skipped) |
| `TestAdv_3_BadByteBetweenStatements` | `attacks_test.go` | hoisted | `TestClassifierForwardsBadBytesBetweenStatementsAsOpaque` | |
| `TestAdv_4_TransactionPadding` | `attacks_test.go` | hoisted | `TestClassifierPaddingInvariantTransactionControl` | |
| `TestAdv_4_LockPadding` | `attacks_test.go` | hoisted | `TestClassifierPaddingInvariantLockingClause` | |
| `TestAdv_4_FunctionPadding` | `attacks_test.go` | hoisted | `TestClassifierPaddingInvariantQualifiedLockFunction` | |
| `TestAdv_4_PostgresUnaryOperatorBoundary` | `attacks_test.go` | hoisted | `TestClassifierUnaryOperatorBoundaryMatchesWhitespaceForm`; Postgres half already in `clsTierRUnaryOperatorBoundary` | |
| `TestAdv_5_AllBytePairs` | `attacks_test.go` | hoisted | `TestClassifierEveryBytePairNeverPanicsAndCoversTheInput` | also exercises `ShareFits` |
| `TestAdv_5_SeededHostileBytes` | `attacks_test.go` | hoisted | `TestClassifierSeededHostileBytesNeverPanic` | |
| `TestAdv_5_AllocationCeiling` | `attacks_test.go` | hoisted | `TestClassifierAllocationStaysUnderTheCeiling` | |
| `TestAdv_7_AllRowsWithPrefixAndStatus` | `attacks_test.go` | hoisted | `TestReplyEveryRowWithPrefixAndStatusIsWellFormed` | |
| `TestAdv_7_InvalidInputsDoNotPoisonReuse` | `attacks_test.go` | hoisted | `TestReplyInvalidInputsDoNotPoisonReuse` | |
| `TestAdv_7_DiscardAllBytesAndRecover` | `attacks_test.go` | hoisted | `TestSyncDiscardDropsEveryByteUntilSyncAndRecovers` | |
| `TestAdv_8_ErrorDocumentExact` | `attacks_test.go` | hoisted (merged) | `rejectlist_test.go: TestClassifierContractDocsMatchTheCode` | the same byte comparison of the same file |
| `TestAdv_8_RejectDocumentExact` | `attacks_test.go` | hoisted (merged) | same | as above |
| `TestAdv_8_GeneratorsTrackTableMutation` | `attacks_test.go` | hoisted | `TestContractGeneratorsTrackTableMutation` | the allow-list mutation now targets `ShareAllowList` and `ShareStopWords` in `safe-shape.md` |
| `TestAdv_9_ImportClosureIsStandard` | `attacks_test.go` | hoisted | `internal/work/work_test.go: TestWorkImportClosureIsTheStandardLibrary` | |
| `TestAdv_9_OutputTypeClosure` | `attacks_test.go` | hoisted | `TestClassifierOutputIsExpressedInWorkTypesOnly` | |
| `TestAdv_9_SourceHasNoProtocolImports` | `attacks_test.go` | hoisted (merged) | `internal/work/work_test.go: TestWorkImportsOnlyTheStandardLibrary` | the same import check |
| `TestAdv_6_ChainAndNoChain` | `database_test.go` | retired | — | tests the transaction-effect flag, removed |
| `TestAdv_6_FaultBeforeAndAfterBegin` | `database_test.go` | retired | — | as above |
| `TestAdv_6_UnicodeWhitespaceAndControl` | `database_test.go` | retired | — | as above |
| `TestAdv_10_MultiStatementRejectAtomicity` | `database_test.go` | hoisted | `TestClassifierMultiStatementRejectNeverReachesTheFake` | |
| `TestAdv_10_NestedFunctionReject` | `database_test.go` | hoisted | `TestClassifierNestedFunctionRejectNeverReachesTheFake` | |
| `TestAdv_10_EscapeContinuationLeak` | `database_test.go` | hoisted | `TestClassifierSetHiddenBehindAnEscapeContinuationNeverReachesTheFake` | restated, see Deviations |
| `run-tier-r.sh` (script) | `agent_work/adversarial/c05-classifier/` | retired as a script | — | its Postgres checks run inside `scripts/tier-r.sh` (`TestTierRSafeShapeDifferential`, `TestTierRClassifierDifferential`) |

Counts: **kept 46** (first-build functions, 6 renamed or moved), **hoisted 30** (29 adversary
attacks, 3 of them merged into an existing test, plus the stopped probe), **retired 3** (the
three transaction-flag oracles; plus the adversary's tier R script and the transaction half
of `TestTierRClassifierDifferential`).

Test lines: before, 2,827 in the shipped suite (`internal/pgwire` 2,733, `internal/work` 94)
plus 636 in the adversary's harness and 50 in the parked probe (3,513). After, 4,453 in the
same areas and the new ones (`internal/pgwire` 4,025 including 1,117 lines of new safe-shape
and continuation tests, `internal/work` 115, `internal/obs/clssrc` 141,
`internal/obs/simrun/classifier_test.go` 172), plus the 571-line `clsworld` harness (non-test Go). Corpus: 562 → 599 records.

## Product size

`internal/work` + the chunk's `internal/pgwire` files: 2,152 → 1,931 lines (`classify.go`
853 → 576, `rejectlist.go` 167 → 58, `share.go` 196 and `share_doc.go` 54 new). Plus
`internal/obs/clssrc` 64 and `internal/obs/simrun/classifier.go` 55. The scenario harness
`clsworld` (571) is harness code under `pgwiretest`.

## Commands

```
export PATH=/usr/local/go/bin:$PATH
go test -count=1 ./internal/work ./internal/pgwire/... ./internal/obs/clssrc     # tier S
go test -count=1 -run 'SafeShape' -v ./internal/pgwire                         # the recogniser
go test -count=1 -run 'Scenario|Planted' -v ./internal/pgwire                  # e2e and canary
MG_REQUIRE_DUCKDB=1 go test -count=1 -run 'Classifier|CSVAssert' ./internal/obs/simrun   # flush + SQL
go test -run '^$' -fuzz '^FuzzSafeShape$' -fuzztime 60s ./internal/pgwire
go test -run '^$' -fuzz '^FuzzClassify$' -fuzztime 60s ./internal/pgwire
go test -run '^$' -fuzz '^FuzzLex$' -fuzztime 60s ./internal/pgwire
scripts/gen-docs.sh            # regenerate error-contract.md, reject-list.md, safe-shape.md, csv-schema.md
scripts/gen-docs.sh --check    # gate G8
scripts/tier-r.sh              # includes TestTierRClassifierDifferential and TestTierRSafeShapeDifferential
MG_TIER_R=1 MG_PG_ADDR=127.0.0.1:<port> go test -count=1 -run 'TestTierR(Classifier|SafeShape)' ./internal/pgwire   # against your own Postgres 17
agent_work/chunks/c05-classifier/gate/extra.sh
scripts/gate.sh c05-classifier
```

## Repair round

Opus, 2026-11-26, from `/root/briefs/zgateway-p2-c05r-repair.md`, bound by
`RECONCILIATION.md` (rows R1–R11), `REVIEW-1.md` (NITS) and the two adversarial reports
(Astra: `agent_work/adversarial/c05-classifier/`, Neon:
`agent_work/adversarial/c05-classifier-neon/`). Every finding was reproduced first.

| Row | Finding / attack | Outcome |
| --- | --- | --- |
| R1 | Whole-span rescans per `pg_settings`/`client_encoding`/`standard_conforming_strings` (Astra `TestAdv_5_WholeSpanRescanning`, Neon `TestAdv_5_QuadraticSpanScanDoS`) | **fixed** `205a75f`: the span's `update`/`set`/`reset` words are read once (`spanWords`). Regression test `classify_test.go: TestClassifierSpanKeywordChecksGrowLinearly` (from `parked/c05r-repair-stopped`): ratios 117–249 before, 9–21 after. Both attacks pass. |
| R2 | Operator-run suffix rescans (Astra `TestAdv_5_OperatorSuffixRescanning`) | **fixed** `a6341d8`: the lexer scans an operator run once (`lexOpRun`: end, last keep-sign byte, start of the trailing sign run) and cuts every operator from it in O(1); `lexOperatorLen` removed. Tokens unchanged: `TestLexOperatorRunsMatchTheOneRunAtATimeRule` and `TestLexOperatorTrailingSignFollowsPostgres` pass. Regression test `classify_test.go: TestLexOperatorRunsGrowLinearly` (from the parked branch): `+` 222 and `+-` 252 before (34 s and 36 s at 64 KiB), 15 and 29 after. The attack passes. |
| R3 | RLS policy changes session / re-run rows (Astra `TestAdv_2_PlainTablePolicyChangesSession`, `…ChangesRepeatedRows`) | **narrowed** (claim 2 excludes tables with row-level security policies; "Narrowed claims", "Stated limits" 1). No code change. Both attacks still fail against the old wording, as expected. |
| R4 | Parallel plan: order, `string_agg`, float `sum` differ on re-run (Neon `TestAdv_2_ParallelScanDifferentRowsOnRerun`, `TestAdv_2_FloatSumParallelNonAssociativity`) | **narrowed** (multiset of rows, defined values only; "Narrowed claims", "Stated limits" 5). Safe shape unchanged. Tier R oracle extended `cba8dd3`: table `big` (200,000 rows, 5 of 6 texts get `Parallel Seq Scan`, asserted), re-runs compared as multisets (`shrRowMultiset`), exact types only; `TestTierRSafeShapeDifferential` passes on Postgres 17 (81 + 400 + 6 texts). Order varying between runs confirmed by hand (`SELECT a FROM big WHERE mod(a, 50000) = 0`, 6 runs, 3 orders). Both attacks still fail against the old wording, as expected. `docs/safe-shape.md` lists no limits, so its generator is unchanged (the brief's condition). |
| R5 | `set_config` with unreadable name/path refused (Astra `TestAdv_6_UnreadableLocalArguments`) | **narrowed** (claim 6 precise form, "Narrowed claims"). No code change. The attack still fails against the old wording, as expected. |
| R6 | `SELECT a FROM local temp` and three like it refused as `temp-object` (Neon `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused`) | **fixed** `c3d8417`: `temp`/`temporary` refuse only after `CREATE`, `CREATE OR REPLACE` or `INTO` (not `INSERT`/`MERGE INTO`), with an optional `GLOBAL`/`LOCAL` between (`makesTemp`). Regression test `classify_repair_test.go: TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade` (16 refused forms, 8 forwarded reads). Corpus (190 reject records), documented examples and spellings still refused. The attack passes. |
| R7 | Refused `Build` counted in `Total`/`ByRow` (Neon `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts`) | **fixed** `da290fe`: `Build` counts `ByRow`/`Total` only for a reply it built; every refusal counts in `Failed` only. Regression test `errors_test.go: TestReplyRefusesBadInput` (it pinned `ByRow[shed] == 3` before; now asserts zero, and one count for a real build). Product tests pinning the old count corrected: `classify_attacks_test.go: TestReplyEveryRowWithPrefixAndStatusIsWellFormed` (`da290fe`), `errors_test.go: TestReplyEveryRowIsWellFormedWithTheRightMarker` (`62ee8e3`). The attack passes. |
| R8 | Review 1 finding 1: ledger row for `TestClassifierArbProbe` | **fixed** (this file): the row now says B5 and E4 are asserted (since `5356992`), E1–E3 rejected. |
| R9 | Review 1 finding 2: dead `clsTierRReadOnly` | **fixed** `066ac76`: deleted. |
| R10 | `TestReplyMetricsCoverEveryRow` exercises one row | **fixed** `d1ecdf6`: builds every row (row k, k+1 times), checks `replies{reason}` per row, `replies_built` and `reply_failures` (> 0); ledger row corrected. It also fails under the old R7 counting. |
| R11 | `UESCAPE` literal continued across a newline hides an advisory-lock call | **listed** under "Courtesy gaps known". No new reject rule. |

**Adversarial tests that are now wrong** (they assert something the reconciliation ruled a
defect; not edited, per the brief):

- Astra `TestAdv_8_ErrorRowsRandomOrderAccounting` (held before) now fails: it expects
  `ByRow[pass-through] == 9` and `Total == 9 × ErrRowCount` after nine refused pass-through
  builds, i.e. the old counting that R7 corrects. Neon's
  `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts` asserts the opposite and now passes; the
  two cannot both pass. No claim says a refused build is a built reply (`replies_built`:
  "Replies the proxy built itself").
- Neon `TestAdv_1_ShareableContractWithRejectAndTx` (held before) now fails: its
  "safe yet rejected" precondition list includes `SELECT local temp`, `SELECT global temp`,
  `SELECT local temporary`, `SELECT global temporary`, `SELECT replace temp`,
  `SELECT replace temporary` and asserts they are refused. That is the over-rejection R6
  removes (Neon's own `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused` asserts
  the opposite). The claim it tests (claim 1: a rejected text is never shareable or keyed)
  still holds for its other inputs (`pg_temp`, `pg_temp_1`, …) and in its random part,
  which pass.

**Adversarial runs after the repair** (Go 1.26.8, Postgres 17 `postgres:17`):

- Astra, `go test -count=1 -v ./agent_work/adversarial/c05-classifier/` (starts its own
  Postgres): 32 pass, 4 fail — `TestAdv_2_PlainTablePolicyChangesSession`,
  `TestAdv_2_PlainTablePolicyChangesRepeatedRows` (R3, narrowed), `TestAdv_6_UnreadableLocalArguments`
  (R5, narrowed), `TestAdv_8_ErrorRowsRandomOrderAccounting` (wrong after R7, above).
  `TestAdv_5_OperatorSuffixRescanning` and `TestAdv_5_WholeSpanRescanning` pass. (In one run
  under the whole-tree `go test ./...`, `TestAdv_2_PlainTableCoercionAndAggregates` failed
  with "the database system is starting up" from its own container; it passes alone.)
- Neon default, `go test -count=1 -v ./agent_work/adversarial/c05-classifier-neon/`: 33
  pass, 1 fail (`TestAdv_1_ShareableContractWithRejectAndTx`, wrong after R6, above), 4 tier
  R attacks skipped. `TestAdv_5_QuadraticSpanScanDoS`, `TestAdv_6_PlainTable…NotRefused`,
  `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts` pass.
- Neon tier R, `ADV_TIER_R=1 ADV_PG_ADDR=… go test -run '^TestAdv_2' …`: 2 pass, 2 fail
  (`TestAdv_2_ParallelScanDifferentRowsOnRerun`, `TestAdv_2_FloatSumParallelNonAssociativity`:
  R4, narrowed).

**Test counts after the repair round** (ledger): test and fuzz functions in
`internal/pgwire`, `internal/work`, `internal/obs/clssrc` and
`internal/obs/simrun/classifier_test.go` 173 → 176 (+3: `TestLexOperatorRunsGrowLinearly`,
`TestClassifierSpanKeywordChecksGrowLinearly`,
`TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade`); test lines in those areas
7,954 → 8,084. Ledger rows above are unchanged in count (kept 46, hoisted 30, retired 3);
the three new tests are additions, not ledger rows. Product Go changed: `lex.go` +34/−18,
`classify.go` +32/−13, `reply.go` +11/−6.

**Timing note.** In one whole-tree `go test ./...` run, `TestClassifierTimeGrowsLinearly`
measured 104 for the `'a'\n` shape (0.43 ms → 45 ms, under load from parallel packages);
alone it measures 16. The shape's code was not touched in this round.

## Index rows

Added to `agent_work/INDEX.md` (section "Chunk 5 — statement classifier and the contracts,
reworked under ruling 9").

## Gate result

`scripts/gate.sh c05-classifier` at the build stage on commit `2bf9340` (clean tree),
2026-11-26: **overall pass, counts_for_landing true**. G0 build, G1 lints (detlint on 19
package patterns, importlint, boundlint, paniccheck), G2 comments, G3 unit (195 s) and race,
G4 markers and sweep (200 seeds per scenario, 398 s), G5 fuzz (14 targets, 20 s each), G6
tier R (Postgres 17.11, 86 s), G7 csv (DuckDB 1.5.5, 52 run directories, 25 scenarios with
SQL files, the two classifier scenarios among them), G8 gen-docs, G9 site, G10 extra (42
reject-list runs replayed across processes with identical digests; both canaries caught;
the classifier SQL files catch planted numbers). `result.json` was deleted, not committed.
Only this file changed after the gated commit. Tier R counts quoted above are from a run of
the same code against a local Postgres 17.11 container: the classifier differential ran 407
forwarded corpus records (192 rejected or `noexec`, not run).
