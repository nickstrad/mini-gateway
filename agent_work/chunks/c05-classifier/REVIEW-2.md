VERDICT: FAIL

## Findings

1. **[BLOCKER] `internal/pgwire/lex.go:140-154`, `internal/pgwire/lex.go:201-223`, `internal/pgwire/classify.go:500-517, 540-562`** — Missing SQL-standard string-literal continuation across newlines (`E'...' \n '...'` and `'...' \n '...'`) causes lexer desynchronization against PostgreSQL 17, falsifying Claims 1, 2, and 6 (and leaving a bypass in Review 1 Finding 3's `search_path` fix).
   - **What is wrong:** In PostgreSQL's scanner (`scan.l`), two single-quoted string literals separated by whitespace/comments containing at least one newline (`\n` or `\r`) form a single string token, and when the first segment is an escape string (`E'...'`), the continued segment remains in escape-string mode (`\'` is an escaped quote). `Lexer.scan` (`lex.go:140-154`) and `Lexer.quoted` (`lex.go:201-223`) scan each `'...'` independently, treating the continuation segment as a plain `standard_conforming_strings=on` string (`backslash=false`). As a result:
     - With `E'ok'\n'\' ...`, `lex.go` closes the continuation string at the quote after `\`, while Postgres 17 treats `\'` as an escaped quote inside the string and executes everything after the next `'`, which `lex.go` thinks is inside a string literal. This allows arbitrary reject-list statements, writes, sequence advances, or `BEGIN` to be hidden from the classifier on default Postgres 17 (`standard_conforming_strings=on`, `UTF8`, no user functions, no custom settings, and no stated limit excludes SQL string continuation).
     - For plain `'...' \n '...'` continuation, `localPathMayNameTemp` (`classify.go:500-517`) and `stringMayContain` (`classify.go:540-562`) inspect each string token separately, so `'pg_'\n'temp'` is not matched as containing `pg_temp`, and `'n'\n'ow'` is not matched as containing `now`.
   - **How I know:** Reproduced over the Postgres wire protocol (raw `Query` frame, bypassing `psql`'s semicolon splitter) against Postgres 17.11 (`postgres:17`, default config) and `pgwire.ClassifyQuery` in `/tmp/zgw-c05-classifier-review2/repo`:
     - **Claim 1 (reject list bypassed, multi-statement):** `SELECT E'ok'\n'\' ; SELECT ' ; SET search_path = hidden ; --'` → `ClassifyQuery`: `Class=start-lease Reason=none ReadOnly=true Coalescable=false Tx=none`; Postgres 17: `status=I errs=0`, subsequent `SHOW search_path` returned `hidden`.
     - **Claims 1 & 2 (reject list bypassed AND marked `Coalescable=true`, single-statement):** `SELECT E'ok'\n'\' || ' || set_config('search_path', 'hidden_1stmt', false) --'` → `ClassifyQuery`: `Class=start-lease Reason=none ReadOnly=true Coalescable=true Tx=none`; Postgres 17: `status=I errs=0`, subsequent `SHOW search_path` returned `hidden_1stmt`.
     - **Claim 2 (write classified `ReadOnly=true`):** `SELECT E'ok'\n'\' ; SELECT ' ; INSERT INTO w VALUES (42) ; --'` → `ClassifyQuery`: `Class=start-lease ReadOnly=true Tx=none`; Postgres 17: `status=I errs=0`, `SELECT count(*) FROM w` changed from `0` to `1`.
     - **Claim 2 (sequence write classified `ReadOnly=true Coalescable=true`):** `SELECT E'ok'\n'\' || ' || nextval('s')::text --'` → `ClassifyQuery`: `Class=start-lease ReadOnly=true Coalescable=true Tx=none`; Postgres 17: `status=I errs=0`, returned `ok' || 1` on call 1 and `ok' || 2` on call 2.
     - **Claim 6 (`MayLeaveOpen=no` leaves transaction open `T`):** `SELECT E'ok'\n'\' ; SELECT ' ; BEGIN ; --'` → `ClassifyQuery`: `Class=start-lease ReadOnly=true Tx=none MayLeaveOpen=no`; Postgres 17: `status=T errs=0`.
     - **Claim 1 / Review 1 Finding 3 (`SET LOCAL search_path` and `SET LOCAL SCHEMA` temp-table bypass):** `BEGIN; SET LOCAL search_path = 'pg_'\n'temp'; CREATE TABLE sneaky_c1 (a int); COMMIT` and `BEGIN; SET LOCAL SCHEMA 'pg_'\n'temp'; CREATE TABLE sneaky_c2 (a int); COMMIT` → `ClassifyQuery`: `Class=start-lease Reason=none` for both; Postgres 17: `status=I errs=0`, both `sneaky_c1` and `sneaky_c2` present in `pg_my_temp_schema()` after `COMMIT`.
     - **Claim 2 (relative-time literal via string continuation):** `SELECT a FROM t_ts WHERE ts < 'n'\n'ow'` → `ClassifyQuery`: `ReadOnly=true Coalescable=true`; Postgres 17: concatenates `'n'\n'ow'` to `'now'` (`SELECT 'n'\n'ow' = 'now'` returned `t`) and coerces it to `timestamptz`.
   - **What a fix must achieve:** Implement PostgreSQL's string-literal continuation in `Lexer.quoted` (or combine continued string segments in `lex.go`): after a closing `'` of `'...'`, `E'...'`, `U&'...'`, `N'...'`, `B'...'`, or `X'...'`, if whitespace/comments containing a newline (`\n` or `\r`) are immediately followed by `'`, continue lexing as part of the same string token with the same `backslash` escape mode (maintaining exact non-overlapping token spans, or if `TokenString` stays single-token across continuations, update `stringMayContain` to strip continuation breaks / reject any continued `E'...'` or `U&'...'` string or scan logical string contents across continuations so `'pg_'\n'temp'` and `'n'\n'ow'` are caught). Add unit tests, corpus entries, and Tier R wire-protocol canaries for all shapes above (`E'...' \n '\'...'` hiding `SET`, `INSERT`, `nextval`, `set_config`, `BEGIN`; `'pg_'\n'temp'` in `SET LOCAL search_path`/`SCHEMA`; `'n'\n'ow'` in `WHERE`).

2. **[MAJOR] `internal/pgwire/rejectlist.go:101`, `internal/pgwire/classify.go:840-853`** — Built-in PostgreSQL temporal array, range, and multirange types bypass the repair-round D10 fix and are classified `Coalescable=true`, falsifying Claim 2.
   - **What is wrong:** `rejTemporalTypes` (`rejectlist.go:101`) only lists the five scalar names `date`, `time`, `timetz`, `timestamp`, `timestamptz`. PostgreSQL's built-in `pg_catalog` range, multirange, and internal array type names (`tstzrange`, `tsrange`, `daterange`, `tstzmultirange`, `tsmultirange`, `datemultirange`, `_timestamptz`, `_timestamp`, `_date`, `_time`, `_timetz`, `_tstzrange`, `_tsrange`, `_daterange`, `_tstzmultirange`, `_tsmultirange`, `_datemultirange`) invoke the same temporal input parsing for `'now'`, `'today'`, `'tomorrow'`, `'yesterday'` when cast from computed text (e.g. `('{' || 'n' || 'ow}')::_timestamptz`), returning per-call timestamps with no user-defined type or domain involved (outside stated limit 13).
   - **How I know:** Reproduced against Postgres 17.11 and `ClassifyQuery` in `/tmp/zgw-c05-classifier-review2/repo`:
     - `SELECT ('[' || 'n' || 'ow,)')::tstzrange::text` → `ClassifyQuery`: `ReadOnly=true Coalescable=true`; Postgres 17 returned `["2026-11-25 04:05:46.288492+00",)` and 15 ms later `["2026-11-25 04:05:46.305107+00",)`.
     - `SELECT ('[' || 'n' || 'ow,)')::tsrange::text` → `ReadOnly=true Coalescable=true`; different values 15 ms apart on Postgres 17.
     - `SELECT ('{[' || 'n' || 'ow,)}')::tstzmultirange::text` → `ReadOnly=true Coalescable=true`; different values 15 ms apart on Postgres 17.
     - `SELECT ('{' || 'n' || 'ow}')::_timestamptz::text`, `::_timestamp::text`, `::_time::text`, `::_timetz::text` → all `ReadOnly=true Coalescable=true`; all returned different values 15 ms apart on Postgres 17.
     - `SELECT ('[' || 't' || 'oday,)')::daterange::text` and `SELECT ('{' || 't' || 'oday}')::_date::text` → `ReadOnly=true Coalescable=true`; both evaluated `current_date` (`[2026-11-25,)` and `{2026-11-25}`).
   - **What a fix must achieve:** Add all built-in PostgreSQL temporal range, multirange, and array type names (`daterange`, `tsrange`, `tstzrange`, `datemultirange`, `tsmultirange`, `tstzmultirange`, and the `_`-prefixed array type names for all of them and the five scalar types) to `rejTemporalTypes` (or match them in `coalescable`), regenerate `docs/reject-list.md`, and add regression tests and Tier R canaries.

3. **[MINOR] `agent_work/chunks/c05-classifier/NOTES.md:530-532, 648-657`** — `NOTES.md` still states that `TestAdv_3_RejectBeforeLexError` is left failing in `go test ./...` and causes G3 to fail, whereas commit `295625e` skipped `TestAdv_3_RejectBeforeLexError` with the narrowed Claim 3 pointer so `go test -count=1 ./...` passes.
   - **What is wrong:** Operational notes lag commit `295625e` (the orchestrator's post-repair acceptance commit).
   - **How I know:** `go test -count=1 ./...` passed across all 17 packages; `agent_work/adversarial/c05-classifier/attacks_test.go:103-105` calls `t.Skip(...)`.
   - **What a fix must achieve:** Update the "Narrowed claims" / "Gate result" sentences in `NOTES.md` to reflect that `TestAdv_3_RejectBeforeLexError` is skipped and `go test -count=1 ./...` is green.

## Mutation checks

All fix-revert checks and property mutations were applied one at a time in a scratch clone at `/tmp/zgw-c05-classifier-review2/repo` and run with `go test -count=1 ./internal/pgwire` (and `./agent_work/adversarial/c05-classifier/...` where noted).

### Review 1 and adversarial repair-fix revert checks

1. **REVERT-F1 (schema-qualified keyword-named calls — delete `case m.isPunct(i-1, '.'): return false` in `classify.go:828-829`):** CAUGHT by `TestClassifierSchemaQualifiedKeywordNamesAreCalls`, `TestClassifierSpellingsNeverProveUnlistedCalls`, `TestClassifierCorpus`.
2. **REVERT-F2 (attribute calls after expression-position `FROM` — drop `&& m.tableRefPosition(s, j-1)` in `classify.go:786`):** CAUGHT by `TestClassifierAttributeCallAfterExpressionFrom`, `TestClassifierSpellingsNeverProveUnlistedCalls`, `TestClassifierCorpus`.
3. **REVERT-F3a (`SET LOCAL search_path` string literal — delete `localPathMayNameTemp` check in `classify.go:405-407`):** CAUGHT by `TestClassifierSearchPathStringNamingTempSchema`, `TestClassifierSpellingsAlwaysRejectTheRejectList`, `TestClassifierCorpus`. (Fix works for single-token strings; bypassed by string continuation — see Finding 1.)
4. **REVERT-F3b (`set_config('search_path', ..., true)` — delete `setConfigPathMayNameTemp` case in `classify.go:494-495`):** CAUGHT by `TestClassifierSearchPathStringNamingTempSchema`, `TestClassifierSpellingsAlwaysRejectTheRejectList`, `TestClassifierCorpus`.
5. **REVERT-F4 (quoted `EXPLAIN ("analyze")` — delete `explainOptionsAnalyze` check in `classify.go:702-704`):** CAUGHT by `TestClassifierExplainQuotedAnalyzeOption`, `TestClassifierCorpus`.
6. **REVERT-A2 (time-dependent literals — remove `rejTemporalTypes` and `rejRelativeTimeWords` from `classify.go:845`):** CAUGHT by `TestClassifierTimeDependentLiteralsAreNotCoalescable`, `TestClassifierSpellingsNeverCoalesceNonCoalescableCalls`, `TestClassifierCorpus`, and `TestAdv_2_TimeDependentLiterals`. (Fix works for the 5 scalar types; incomplete for range/multirange/array temporal types — see Finding 2.)
7. **REVERT-A4 (Postgres trailing-sign operator rule — bypass `lexOperatorLen` in `lex.go:315`):** CAUGHT by `TestLexOperatorTrailingSignFollowsPostgres`, `TestClassifierCorpus`, and `TestAdv_4_PostgresUnaryOperatorBoundary`.

(Review 1 Finding 5 verified directly with DuckDB: both SQL files under `test/analysis/c05-reject-list/` emit 1 violation row on an empty `runs` table and 0 rows on a passing `classifier-reject-only` run, matching `clsScenario.name()`. Review 1 Finding 6 verified directly: corpus counts in `NOTES.md` match `562` records, `186` rejected, `376` forwarded, `165` read-only, `116` coalescable, `2` `noexec`. Narrowed Claim 3 in `ADVERSARIAL.md` matches `classify.go:124-140`.)

### Property mutation checks

1. **M1 — Treat `SET` as forwardable (`classify.go:402-404`):** CAUGHT by `TestScenarioClassifierRejectList`, `TestScenarioClassifierRejectOnly`, `TestClassifierPlantedSetForwardableIsCaught`, `TestClassifierRejectOnlyFlushNumbers`, `TestClassifierSearchPathStringNamingTempSchema`, `TestClassifierSpellingsAlwaysRejectTheRejectList`, `TestClassifierCorpus`, `TestClassifierGeneratedStatements`, `TestRejectRulesExamplesClassifyAsDocumented`.
2. **M2 — Allow `FOR UPDATE` locking clause as read-only (`classify.go:752-754` deleted):** CAUGHT by `TestClassifierCorpus`.
3. **M3 — Mark `now()` coalescable (`rejectlist.go:73`):** CAUGHT by `TestClassifierCorpus`, `TestClassifierShareKeyAndScope`, `TestAllowListIsSortedAndCoalesceIsASubset`, `TestClassifierContractDocsMatchTheCode`.
4. **M4 — Drop conservative untokenisable fallback (`classify.go:131-135` deleted):** CAUGHT by `TestClassifierCorpus`, `TestClassifierGeneratedStatements`, `TestClassifierConservativeWhenUntokenisable`, `FuzzClassify`.
5. **M5 — Append `ReadyForQuery` after `FATAL` (`reply.go:70-71` deleted):** CAUGHT by `TestReplyGoldenBytes`, `TestReplyEveryRowIsWellFormedWithTheRightMarker`.
6. **M6 — Skip `ByReason` counter increment (`classify.go:63-65` deleted):** CAUGHT by `TestScenarioClassifierRejectList`, `TestScenarioClassifierRejectOnly`, `TestClassifierPlantedSetForwardableIsCaught`, `TestClassifierRejectOnlyFlushNumbers`, `TestClassifierSnapshotCountersMoveAndConserve`.
7. **M7 — Drift generated document (`docs/reject-list.md` modified):** CAUGHT by `TestClassifierContractDocsMatchTheCode`.

## Release It! checklist

- **Integration points:** Not applicable here because this chunk is pure sans-I/O lexing, classification, and reply encoding with no network I/O (only the e2e test harness in `classify_e2e_test.go` dials `simnet`).
- **Blocked threads:** Not applicable here because this chunk starts no goroutines, locks, channels, or database connections; the e2e harness closes both ends and asserts `s.CheckQuiesce()`.
- **Slow responses:** Classification runs in linear time and reply building is O(1) over fixed rows, so rejections are built in bounded time without touching the database.
- **Cascading failures:** Not applicable here because there is no pool, dialer, or health endpoint in this chunk; `ErrRowBreakerOpen` (`53M03`) is defined in `ErrorRows` for chunk 7.
- **Chain reactions:** Not applicable here because queuing and shedding live in chunks 8 and 13; `ErrRowShed` (`53M01`) and `ErrRowDraining` (`53M05`) are defined in `ErrorRows`.
- **Dogpile:** Not applicable here because this chunk performs no dials, retries, or recovery loops.
- **Self-denial attacks:** Not applicable here because this chunk has no periodic, batched, or synchronized actions.
- **Force multiplier:** Not applicable here because this chunk has no automatic feedback or control loop.
- **Unbalanced capacities:** Not applicable here because this chunk has no producer/consumer queues; per-call slices in `clsMsg` (`toks`, `parens`, `stack`, `stmts`) are exact-sized by counting passes.
- **Scaling effects:** Classification and reply encoding are stateless per message and hold at N instances; all exported metrics (`ClassifierMetrics`, `ReplyMetrics`) use bounded enum labels (`class`, `reason`, `row id`, `may_leave_open`) and never emit statement text, client address, or error text.
- **Users:** Not applicable for persistent per-client/per-tenant resources because `Classifier` and `ReplyBuilder` retain zero per-client or per-tenant state; `SyncDiscard` is one fixed-size struct per client connection.
- **Unbounded result sets:** Per-classification allocation is bounded by `ClassifMemoryFactor` (80) bytes/input byte + `ClassifMemoryConstant` (64 KiB) and tested at 1 MiB across 11 adversarial shapes (`TestClassifierMemoryIsBounded`, measured worst case 56 bytes/input byte); `ErrRowPartialReply` (`ErrorRowCloseOnly`, `Close: true`) is fixed in the error table for chunk 6.
- **Compounds:** None internal to this chunk, but Finding 1 compounds directly with chunk 11's coalescer and chunk 12's retry policy: a query using `E'...' \n '\'...'` continuation to mutate session state or write data is classified `ReadOnly=true, Coalescable=true, Tx=none`, so it can be shared across clients, retried after a lost connection, or leave a pooled connection with modified session state / an open transaction. Finding 2 compounds with chunk 11's coalescer by deduplicating per-call temporal range/array queries across clients.

## Observability

- **`Snapshot()` present:** Yes — `Classifier.Snapshot() ClassifierSnapshot` (`classify.go:41`), `ReplyBuilder.Snapshot() ReplySnapshot` (`reply.go:40`), and `SyncDiscard.Snapshot() SyncDiscardSnapshot` (`reply.go:124`), all returning plain value copies of fixed-size integer counters/arrays with no time and no maps.
- **Exporter mapping & CSV registry:** `ClassifierMetrics` and `ReplyMetrics` (`snapshot_classifier.go:18-50`) produce flush-ready metric rows; chunk 3 (`internal/obs` collector/CSV adapter and `docs/csv-schema.md`) is built concurrently and integration wiring is deferred to chunk 6 as permitted by the brief.
- **Counters move and conservation holds:** Verified by `TestClassifierSnapshotCountersMoveAndConserve` (`classify_obs_test.go:14-59`: exercises every produced class, reject reason, `MayLeaveOpen` value, and boolean counter; asserts `ClassifierSnapshot.Check()` conservation `Classified == sum(ByClass) == sum(MayLeaveOpen)`, `ByClass[ClassReject] == sum(ByReason)`, `Coalescable <= ReadOnly`, plus two negative checks), `TestReplyEveryRowIsWellFormedWithTheRightMarker` (`errors_test.go:196-262`: `Total == sum(ByRow)` across all rows), `TestReplyRefusesBadInput` (`errors_test.go:264-284`: `Failed` incremented on error paths), and `TestSyncDiscardRule` (`errors_test.go:311-317`).
- **CSV-flush assertions:** `test/analysis/c05-reject-list/fakepg-statements-zero.sql` and `rejects-equal-sent.sql` check schema version 1, enforce the scenario assertions, and fail closed when no `classifier-reject-only` run exists (verified with DuckDB); Go-level equivalent in `TestClassifierRejectOnlyFlushNumbers` (`classify_e2e_test.go:539-564`).
- **Health rule:** Classifier and reply builders introduce no `degraded`/`failed` health transitions.

## What I ran

- `gofmt -l .`: empty output (0 files).
- `go build ./... && go vet ./...`: passed (exit 0).
- `scripts/gen-docs.sh --check`: passed (exit 0).
- `go test -count=1 ./internal/work ./internal/pgwire`: passed (`internal/work` 0.006s, `internal/pgwire` 4.156s).
- `go test -v -count=1 -run 'TestClassifierMemoryIsBounded|TestClassifierSpellings' ./internal/pgwire`: passed (2.945s; 8,172 distinct generated unlisted-call statements, 420 non-D10 statements, worst allocation at 1 MiB: 56 bytes/input byte vs bound `80*n + 65536`).
- `go test -count=1 ./...`: passed (all 17 packages green; `internal/pgwire` 5.697s, `internal/work` 0.005s, `agent_work/adversarial/c05-classifier` 1.135s with `TestAdv_3_RejectBeforeLexError` skipped and the 3 Tier R attacks skipped in default mode).
- `scripts/ci-fast.sh`: passed (G0 tools/gofmt/build/vet/tools-vet/tools-test/tidy, G1 detlint/importlint/boundlint/paniccheck, G3 unit 32s).
- `agent_work/chunks/c05-classifier/gate/extra.sh`: passed (`40 reject-list scenario runs replayed across processes with identical digests; SET-forwardable canary caught`).
- `DST_SEEDS=50 go test -count=1 -run '^TestScenarioClassifier' ./internal/pgwire`: passed (1.030s, 50 seeds × 2 scenarios).
- `go test -run '^$' -fuzz '^FuzzLex$' -fuzztime 15s ./internal/pgwire`: passed (343,164 execs, 15.173s).
- `go test -run '^$' -fuzz '^FuzzClassify$' -fuzztime 15s ./internal/pgwire`: passed (116,906 execs, 16.120s).
- `scripts/tier-r.sh`: passed against Postgres 17.11 (5 passed, 0 failed, 0 skipped; `374 forwarded`, `327 leaves no transaction open`, `163 read-only-provable`, `188 not run`).
- `agent_work/adversarial/c05-classifier/run-tier-r.sh`: passed against Postgres 17 (5 passed: `TestAdv_2_TimeDependentLiterals`, `TestAdv_4_PostgresUnaryOperatorBoundary`, `TestAdv_6_ChainAndNoChain`, `TestAdv_6_FaultBeforeAndAfterBegin`, `TestAdv_6_UnicodeWhitespaceAndControl`; restored `tier-r.json` afterward so repo remained untouched).
- AST identifier-prefix check over `internal/pgwire/*.go`: 0 violations across 17 files; no `TestMain`, no `doc.go`.
- Corpus and line-count check: 562 corpus records (186 rejected, 376 forwarded, 165 read-only, 116 coalescable, 2 `noexec`, 16 `bad`), 2,152 product lines, 2,827 test lines.
- DuckDB check (`/root/.local/bin/duckdb -dark-mode -batch`) over both SQL files in `test/analysis/c05-reject-list/`: 1 violation row on empty `runs`, 0 rows on a matching passing run.
- Postgres 17.11 keyword & volatility checks: 0 of 106 words in `rejNonCallWords` have `catcode NOT IN ('R','C')`; 0 `Coalesce: true` functions in `AllowList` have `provolatile = 'v'`; `SELECT 7%-3` confirmed `%-` is one operator in Postgres 17.
- Live Postgres 17.11 wire-protocol probes and `ClassifyQuery` probes in `/tmp/zgw-c05-classifier-review2/`: reproduced Findings 1 and 2, and verified Review 1 Findings 1–4 and adversarial fixes A2 and A4.
- 7 fix-revert checks (REVERT-F1, F2, F3a, F3b, F4, A2, A4) and 7 property mutation checks (M1–M7) in `/tmp/zgw-c05-classifier-review2/repo`: all 14 caught.
