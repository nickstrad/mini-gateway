VERDICT: FAIL

## Findings

1. **[MAJOR] `internal/pgwire/classify.go:447-448`, `agent_work/chunks/c05-classifier/NOTES.md:543`, `docs/knowledge/classifier.md:99-100`** — The R6 repair (`makesTemp`) checks `OR REPLACE` without requiring `CREATE`, so valid plain-table safe-shape `SELECT` queries using unreserved names `replace` and `temp`/`temporary` are still falsely refused as `temp-object`.
   - **What is wrong:** `makesTemp` (`internal/pgwire/classify.go:439-453`) handles `replace` with:
     ```go
     case m.is(j, "replace"):
         return j > s.lo && m.is(j-1, "or")
     ```
     It never checks that token `j-2` is `create`. As a result, `SELECT true OR replace temp FROM probe_r6` and `SELECT true OR replace temporary FROM probe_r6` (where `replace` is a boolean column and `temp`/`temporary` is an unquoted output column alias without `AS`) are refused with `ClassReject` / `RejectTempObject` while `ShareFits` returns `true` (`SafeRead=true`, `Shareable=false`). This leaves a live instance of the exact R6 Claim 6 defect (`RECONCILIATION.md` R6: valid plain-table reads with unreserved names refused outside the documented reject list while `SafeRead` is true) and contradicts `NOTES.md:543` and `docs/knowledge/classifier.md:99-100`, both of which state that `temp`/`temporary` refuse only after `CREATE`, `CREATE OR REPLACE`, or `INTO`.
   - **How I know:**
     1. Verified on live Postgres 17.11 (`postgres:17` container): `CREATE TEMP TABLE probe_r6 (replace bool, local int, global int); INSERT INTO probe_r6 VALUES (false, 1, 2); SELECT true OR replace temp FROM probe_r6; SELECT true OR replace temporary FROM probe_r6;` — both execute cleanly and return 1 row (`t`) aliased as `temp` / `temporary`.
     2. Verified in a scratch test against `ClassifyQuery` at commit `4c7d7a67`: both `"SELECT true OR replace temp FROM probe_r6"` and `"SELECT true OR replace temporary FROM probe_r6"` return `class=reject reason=temp-object safeRead=true shareable=false`.
   - **What a fix must achieve:** Require `CREATE` before `OR REPLACE` in `makesTemp` (`case m.is(j, "replace"): return j >= s.lo+2 && m.is(j-1, "or") && m.is(j-2, "create")`), and add regression cases (`SELECT true OR replace temp FROM t`, `SELECT true OR replace temporary FROM t`, plus `CREATE OR REPLACE [LOCAL|GLOBAL] TEMP|TEMPORARY VIEW ...` still rejected) to `TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade` in `internal/pgwire/classify_repair_test.go`.

### Reconciliation R1–R11 audit

- **R1 (span-word quadratic rescan):** Fixed (`205a75f`, `spanWords`). Verified linear ratios 8–21 in `TestClassifierSpanKeywordChecksGrowLinearly`; Astra and Neon R1 attacks pass; mutation caught.
- **R2 (operator-run quadratic rescan):** Fixed (`a6341d8`, `lexOpRun`). Verified linear ratios 14–23 in `TestLexOperatorRunsGrowLinearly`; Astra R2 attack passes; 200,000 random inputs plus corpus confirm 100% identical operator tokens vs pre-R2 `lex.go` (`7abe26a`); mutation caught.
- **R3 (Claim 2 narrowed for RLS policies):** Done. Stated in full in `NOTES.md` ("Narrowed claims", "Stated limits" 1), `docs/knowledge/classifier.md:78-80`, and `ADVERSARIAL.md:26-31`.
- **R4 (Claim 2 narrowed for parallel-plan re-runs):** Done. Stated in full in `NOTES.md` ("Narrowed claims", "Stated limits" 5), `docs/knowledge/classifier.md:81-85`, and `ADVERSARIAL.md:32-38`; `TestTierRSafeShapeDifferential` runs on 200,000-row table `big`, confirms 5 of 6 queries receive a `Parallel Seq Scan` plan on Postgres 17.11, and compares re-runs as row multisets (`shrRowMultiset`). `docs/safe-shape.md` lists no limits section so its generator is unchanged.
- **R5 (Claim 6 narrowed to precise form):** Done. Stated in full in `NOTES.md` ("Narrowed claims", "Deviations") and `ADVERSARIAL.md:39-42`; consistent with `docs/reject-list.md`.
- **R6 (`temp`/`temporary` refused only where a temporary object is made):** Incomplete — see Finding 1 above. All 190 corpus reject entries (unchanged from `7abe26a`), all `docs/reject-list.md` examples, and the 8 reads tested in `TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade` behave as expected, and the pre-R6 mutation is caught; only the missing `CREATE` check in the `OR REPLACE` branch remains.
- **R7 (refused `ReplyBuilder.Build` counted as `Failed` only):** Fixed (`da290fe`, `62ee8e3`). `TestReplyRefusesBadInput`, `TestReplyEveryRowIsWellFormedWithTheRightMarker`, `TestReplyEveryRowWithPrefixAndStatusIsWellFormed`, `TestReplyMetricsCoverEveryRow`, and Neon's `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts` all pass; mutation caught.
- **R8 (Review 1 finding 1: `TestClassifierArbProbe` ledger row):** Fixed in `NOTES.md:457` (B5 and E4 asserted since `5356992`, E1–E3 rejected, matches `parked/arbprobe_test.go.txt` and `lex_continue_test.go`).
- **R9 (Review 1 finding 2: dead helper `clsTierRReadOnly`):** Fixed (`066ac76`, deleted).
- **R10 (`TestReplyMetricsCoverEveryRow` exercises one row):** Fixed (`d1ecdf6`, builds row `k` `k+1` times for every `ErrRowID`, asserts `replies{reason}` per row, `replies_built`, and `reply_failures > 0`; `NOTES.md:416` updated).
- **R11 (courtesy gap: continued `UESCAPE` literal):** Listed in `NOTES.md:397-401` and `ADVERSARIAL.md:59-60` (not a finding, Claim 7).

## Mutation checks

All 9 mutations were applied one at a time in a scratch clone at `/tmp/zgw-c05-classifier-review2/repo` at `4c7d7a67`:

1. **M1-R1 — Revert `spanWords` hoist to per-token whole-span scan (`internal/pgwire/classify.go:328-330`):** CAUGHT by `TestClassifierSpanKeywordChecksGrowLinearly` (ratios 207–329 vs bound 96 across all six shapes).
2. **M2-R2 — Disable `lexOpRun` cache so `operator()` rescans every time (`internal/pgwire/lex.go:343-348`):** CAUGHT by `TestAdv_5_OperatorSuffixRescanning` (ratio 239.9 in 2.46s) and `TestLexOperatorRunsGrowLinearly` (exceeded 45s timeout inside `clsFastest`).
3. **M3-R6 — Revert `makesTemp` to pre-R6 `m.isAny(i-1, []string{"create", "replace", "into", "global", "local"})` (`internal/pgwire/classify.go:405`):** CAUGHT by `TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade` (all 7 alias-read cases rejected).
4. **M4-R7 — Revert `ReplyBuilder.Build` to increment `ByRow[id]` and `Total` even on error (`internal/pgwire/reply.go:40-49`):** CAUGHT by `TestReplyRefusesBadInput`, `TestReplyEveryRowIsWellFormedWithTheRightMarker`, `TestReplyEveryRowWithPrefixAndStatusIsWellFormed`, and `TestReplyMetricsCoverEveryRow` (`replies{pass-through} = 20, want 0`; `replies_built = 210, want 190`).
5. **M5 — Skip safe-shape alphabet check (`internal/pgwire/share.go:49`):** CAUGHT by `TestSafeShapeTable`, `TestSafeShapePropertyAnyByteOutsideTheAlphabetBreaksAFit`, and `TestSafeShapePropertyFitsAgreesWithTheReferenceChecker`.
6. **M6 — Allow unlisted function calls in safe shape (`internal/pgwire/share.go:195`):** CAUGHT by `TestSafeShapeTable`, `TestSafeShapeEveryStopWordAndUnlistedCallBreaksAFit`, and `TestSafeShapePropertyFitsAgreesWithTheReferenceChecker`.
7. **M7 — Make share hint widen `SafeRead` (`internal/pgwire/classify.go:107`):** CAUGHT by `TestClassifierShareKeyAndScope` and `TestClassifierInvariantsOverMutants`.
8. **M8 — Violate structural independence of `share.go` by calling `lexIsDigit` (`internal/pgwire/share.go:57`):** CAUGHT by `TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier`.
9. **M9 — Disable string continuation across newlines (`internal/pgwire/lex.go:226`):** CAUGHT by `TestLexStringContinuesAcrossANewline` and `TestClassifierRejectsWhatHidesBehindAStringContinuation`.

## Release It! checklist

- **Integration points:** Not applicable here because product code in this chunk (`internal/work`, `internal/pgwire` classifier/recogniser/replies, `internal/obs/clssrc`) performs pure sans-I/O computation with no network I/O; the scenario harness `clsworld` dials only `simnet`.
- **Blocked threads:** Not applicable here because this chunk starts no goroutines, locks, channels, or database connections; `clsworld` closes both `simnet` ends on teardown and asserts `w.Sim.CheckQuiesce()`.
- **Slow responses:** `ShareFits` scans at most `ShareMaxLen` (4,096) bytes with zero allocations; after R1 and R2, `LexAll`/`ClassifyQuery` run in linear time across all 9 general shapes (ratios 15–26), 8 operator-run shapes (ratios 14–23), and 6 span-keyword shapes (ratios 8–21); `ReplyBuilder.Build` is O(1), so refusals complete in bounded time without contacting the database.
- **Cascading failures:** Not applicable here because there is no pool, dialer, or health endpoint in this chunk; `ErrRowBreakerOpen` (`53M03`) is fixed in `ErrorRows` for chunk 7.
- **Chain reactions:** Not applicable here because queuing and shedding live in chunks 8 and 13; `ErrRowShed` (`53M01`) and `ErrRowDraining` (`53M05`) are fixed in `ErrorRows`.
- **Dogpile:** Not applicable here because this chunk performs no dials, retries, or recovery loops.
- **Self-denial attacks:** Not applicable here because this chunk has no periodic, batched, or synchronized actions.
- **Force multiplier:** Not applicable here because this chunk has no automatic feedback or control loop.
- **Unbalanced capacities:** Not applicable for product code (no queues); `clsMsg` sizes `toks`, `parens`, `stack`, and `stmts` from counting passes; `clsworld` caps per-direction buffers at `MaxBuffer` (1 MiB) and closes/fails on overflow.
- **Scaling effects:** Classification, safe-shape recognition, and reply building are stateless per message and hold identically at N instances; all 15 exported metrics under component `classifier` use bounded enum labels (`outcome`, `reason`) and never emit query text, client addresses, or error text.
- **Users:** `Classifier` and `ReplyBuilder` hold zero per-client or per-tenant state; `SyncDiscard` is one fixed-size struct per client connection.
- **Unbounded result sets:** Per-classification allocation is bounded by `ClassifMemoryFactor` (80) bytes/input byte + `ClassifMemoryConstant` (64 KiB) (`TestClassifierMemoryIsBounded`: worst measured 52 B/byte at 1 MiB across 11 shapes; `TestClassifierAllocationStaysUnderTheCeiling` passes); `ShareFits` allocates 0 bytes (`TestSafeShapeAllocatesNothing`); `ErrRowPartialReply` (`ErrorRowCloseOnly`) is fixed in `ErrorRows` for chunk 6.
- **Compounds:** No compound failure paths exist inside this chunk; `SafeRead`/`Shareable` are structurally decoupled from the lexer (`TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier`), so a lexer defect cannot widen sharing or change `08007` to `08006`, and session safety relies on the post-lease reset (D8), not the reject list.

## Observability

- **`Snapshot()` present:** Yes — `Classifier.Snapshot() ClassifierSnapshot` (`internal/pgwire/classify.go:39`), `ReplyBuilder.Snapshot() ReplySnapshot` (`internal/pgwire/reply.go:36`), and `SyncDiscard.Snapshot() SyncDiscardSnapshot` (`internal/pgwire/reply.go:121`), all returning plain value copies of fixed-size integer counters/arrays with no wall clock and no maps.
- **Exporter mapping and CSV registry:** `internal/obs/clssrc` maps all fields of all three snapshots under `obs.ComponentClassifier` (15 counters, zero-valued label rows emitted), declared in `internal/obs/catalog/catalog.go:31`, documented in `docs/csv-schema.md`, and verified by `TestMappingMovesEveryMetricAndExportsEveryField`, `TestZeroSnapshotEmitsEveryLabelValue`, `TestReplyMetricsCoverEveryRow` (now exercising every row), `TestClassifierCountsReachTheExport`, and `TestCSVAssertBaselineCountsFromFlushedFiles`. Import direction is enforced by `tools/lint-rules/imports.txt` and `internal/obs/simrun/imports_test.go`.
- **Counters move and conservation holds:** Verified by `TestClassifierSnapshotCountersMoveAndConserve` (`ClassifierSnapshot.Check()` conservation plus 3 negative checks), `TestReplyEveryRowIsWellFormedWithTheRightMarker` (`Total == sum(ByRow)`), `TestReplyRefusesBadInput` (after R7: refused builds increment `Failed` only, never `Total` or `ByRow`), `TestReplyMetricsCoverEveryRow`, and `TestSyncDiscardRule`.
- **CSV-flush assertions:** `simrun.Classifier` flushes `classifier-reject-list` and `classifier-reject-only` through the collector and CSV adapter; `TestScenarioClassifierFlush` checks CSV round-trip, byte-identical files/adapters across runs, and equal trace digests. All 3 SQL files under `test/analysis/classifier-reject-only/` and `test/analysis/classifier-reject-list/` declare `-- requires:`, check `schema_version = 1`, fail on missing runs, pass under DuckDB, and catch all 8 planted wrong numbers (`TestClassifierSQLCatchesPlantedNumbers`).
- **Health rule:** Classifier, reply builder, and `SyncDiscard` introduce no `degraded`/`failed` health transitions.

## What I ran

- `gofmt -l .`: empty output (0 files).
- `go build ./... && go vet ./...`: passed.
- `scripts/gen-docs.sh --check`: passed (`docs/error-contract.md`, `docs/reject-list.md`, `docs/safe-shape.md`, `docs/csv-schema.md` all current).
- Lints built from `tools/detlint`: `detlint` (86 files in 19 packages), `importlint` (208 files; `go.mod` has 1 external requirement `github.com/jackc/pgx/v5`), `paniccheck` — all passed.
- `go test -count=1 ./...`: all 26 product/internal/cmd packages and `agent_work/adversarial/c05-classifier-neon` passed (`internal/work` 0.080s, `internal/pgwire` 26.829s, `internal/obs/clssrc` 0.010s, `internal/obs/simrun` 211.886s); `agent_work/adversarial/c05-classifier` hit a container-startup race on `TestAdv_2_PlainTableCoercionAndAggregates` under parallel load (noted in `ADVERSARIAL.md:64-66`) and passed standalone (`go test -count=1 -v ./agent_work/adversarial/c05-classifier/`: 32 pass, 4 settled skips, 8.952s).
- `agent_work/chunks/c05-classifier/gate/extra.sh`: passed (`42 reject-list scenario runs replayed across processes with identical digests; SET-forwardable and alphabet canaries caught; classifier SQL files catch planted numbers`).
- `MG_REQUIRE_DUCKDB=1 go test -count=1 -v -run 'Classifier|CSVAssert' ./internal/obs/simrun`: passed (114.600s; all 8 tests green including 8/8 planted SQL failures).
- `DST_SEEDS=50 go test -count=1 -run '^TestScenarioClassifier' ./internal/pgwire`: passed (0.756s, 50 seeds × 2 scenarios).
- `go test -count=1 -v -run '^(TestLexOperatorRunsGrowLinearly|TestClassifierSpanKeywordChecksGrowLinearly|TestClassifierTimeGrowsLinearly|TestClassifierCorpus|TestClassifierMemoryIsBounded|TestSafeShapeAllocatesNothing|TestSafeShapePropertyFitsAgreesWithTheReferenceChecker|TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails)$' ./internal/pgwire`: passed (6.818s; operator ratios 14–23, span-keyword ratios 8–21, general ratios 15–26; worst memory 52 B/byte at 1 MiB; 599 corpus records, 60 fit, 40/116 `was-co` fit; 15,817/200,000 random fits agreed; alphabet canary caught 113,896 leaks; `ShareFits` 0 allocs).
- Fuzz targets (none runs the simulator or hashes input for a seed):
  - `FuzzSafeShape` 10s: passed (48,358 execs, 11.129s).
  - `FuzzClassify` 10s: passed (15,936 execs, 10.519s).
  - `FuzzLex` 10s: passed (41,878 execs, 10.261s).
- Live Postgres 17.11 (`postgres:17` throwaway container):
  - `TestTierRSafeShapeDifferential`: passed (87.330s; 5 of 6 texts on `big` got `Parallel Seq Scan`; 81 corpus + 400 generated + 6 parallel-plan texts, 37 identical Postgres errors).
  - `TestTierRClassifierDifferential`: passed (45.527s; 407 forwarded, 192 not run).
  - `ADV_TIER_R=1 go test -count=1 -v -run '^TestAdv_2' ./agent_work/adversarial/c05-classifier-neon/`: 2 passed, 2 settled skips (0.654s).
  - Live Postgres 17 R6 probe: `SELECT true OR replace temp FROM probe_r6` and `SELECT true OR replace temporary FROM probe_r6` succeeded on Postgres 17 (returning `t`), while `ClassifyQuery` returned `ClassReject` (`RejectTempObject`) with `SafeRead=true`.
- R2 exact equivalence check in scratch repo: 200,000 random inputs produced 100% identical operator tokens against pre-R2 `lex.go` (`7abe26a`).
- Corpus diff against `7abe26a`: empty (all 190 reject entries preserved).
- 9 mutation checks (M1-R1, M2-R2, M3-R6, M4-R7, M5–M9) in `/tmp/zgw-c05-classifier-review2/repo`: 9/9 caught.
