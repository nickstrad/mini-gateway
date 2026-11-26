HELD: 33  BROKEN: 5

Adversarial pass against `c05-classifier` rework (worktree `/root/Software/mini-gateway-work/c05r-nadv`, branch `work/c05r-nadv`, base commit `ac8ff7a9db530b082a5807dd8cc21514e8641391`). All 38 attacks are independent external tests in `package adv_test`, deterministic under seed `2026112605` (overridable via `ADV_SEED`), and derived directly from the 11 claims in `/root/briefs/zgateway-p2-c05r-classifier.md` and `agent_work/chunks/c05-classifier/NOTES.md`.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_LengthAndAlphabetBoundaries` | held |
| 1 | `TestAdv_1_QuoteTogglePrefixAndTimeWordEdges` | held |
| 1 | `TestAdv_1_StopListParenListAndLeadEdges` | held |
| 1 | `TestAdv_1_ShareableContractWithRejectAndTx` | held |
| 2 | `TestAdv_2_ParallelScanDifferentRowsOnRerun` | BROKEN |
| 2 | `TestAdv_2_FloatSumParallelNonAssociativity` | BROKEN |
| 2 | `TestAdv_2_CatalogPurityOfAllowListOperatorsAndKeywords` | held |
| 2 | `TestAdv_2_HostileSafeShapePlainTableSafety` | held |
| 3 | `TestAdv_3_ShareGoZeroExternalIdentifiersAST` | held |
| 3 | `TestAdv_3_SafeShapeImpliesCleanSingleLexedStatement` | held |
| 3 | `TestAdv_3_LexerTricksCannotWidenSafeReadOrShareable` | held |
| 4 | `TestAdv_4_HintExactPrefixOnly` | held |
| 4 | `TestAdv_4_HintNeverSetsSafeReadOrShareable` | held |
| 4 | `TestAdv_4_HintKeyScopeAndImmutability` | held |
| 5 | `TestAdv_5_QuadraticSpanScanDoS` | BROKEN |
| 5 | `TestAdv_5_MemoryCeilingHostileShapes` | held |
| 5 | `TestAdv_5_ExtremeAndPathologicalInputsNoPanic` | held |
| 5 | `TestAdv_5_SeededHostileFuzzCampaign` | held |
| 6 | `TestAdv_6_UntokenisableWithoutRejectAlwaysForwarded` | held |
| 6 | `TestAdv_6_UnmodelledSQLAndNonsenseForwarded` | held |
| 6 | `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused` | BROKEN |
| 7 | `TestAdv_7_DocsRejectListExamplesAllRefusedUnderAdversarialPadding` | held |
| 7 | `TestAdv_7_CorpusRejectEntriesRefusedUnderAdversarialPadding` | held |
| 7 | `TestAdv_7_DocumentedFeaturesAndAllowedFormsBoundary` | held |
| 8 | `TestAdv_8_AllRowsWireDecodeAndEndMarkers` | held |
| 8 | `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts` | BROKEN |
| 8 | `TestAdv_8_SyncDiscardStateMachineExhaustive` | held |
| 8 | `TestAdv_8_BufferReuseAndReplyReadyBoundaries` | held |
| 9 | `TestAdv_9_CommittedDocsExactMatchGenerators` | held |
| 9 | `TestAdv_9_GenDocsScriptCheckAndMutationSensitivity` | held |
| 9 | `TestAdv_9_SafeShapeAndContractDocsCompleteness` | held |
| 10 | `TestAdv_10_WorkASTAndBuildClosureStandardOnly` | held |
| 10 | `TestAdv_10_NoPostgresSymbolsOrIdentifiersInWork` | held |
| 10 | `TestAdv_10_ClassifierPublicAPIReturnTypesWorkOnly` | held |
| 11 | `TestAdv_11_HostileRejectsNeverReachFakeWithFragmentation` | held |
| 11 | `TestAdv_11_FlushedCSVMatchesSnapshotsAndSQLAssertionsPass` | held |
| 11 | `TestAdv_11_AblationAndSQLFailClosedOnTamperedFiles` | held |
| 11 | `TestAdv_11_PlantedBugCanaryRemainsUnreachableFromExternalPackages` | held |

## Broken attacks

All five failing attacks remain in place and failing. Default seed: **`2026112605`**.

### 1. Claim 2 — `TestAdv_2_ParallelScanDifferentRowsOnRerun`

- **Command and seed:**
  ```sh
  ADV_SEED=2026112605 agent_work/adversarial/c05-classifier-neon/run-tier-r.sh
  # or against an existing Postgres 17 instance:
  PATH=/usr/local/go/bin:$PATH ADV_SEED=2026112605 ADV_TIER_R=1 ADV_PG_ADDR=127.0.0.1:<port> \
    go test -count=1 -v -run '^TestAdv_2_ParallelScanDifferentRowsOnRerun$' ./agent_work/adversarial/c05-classifier-neon/...
  ```
- **Expected from Claim 2:** On Postgres 17 with a schema of plain tables only, no text that fits the safe shape writes, changes session state, or returns different rows on an immediate re-run.
- **Observed (Postgres 17.11, stock defaults, single plain table `big (a int, b text)` with 200,000 rows / 22 MB, nothing else running):**
  - `SELECT md5(string_agg(to_hex(a), ',')) FROM (SELECT a FROM big WHERE mod(a, 10000) = 0) s` (`ShareFits == true`, `SafeRead == true`, `Shareable == true`) returned a single-cell scalar MD5 hash that differed on immediate re-run #2:
    - Run 1: `D|9880bfe1821630b100475f67352a0436;C:SELECT 1;`
    - Run 2: `D|3f2beeb7a7b04a5e850bba7f3432e042;C:SELECT 1;`
  - `SELECT a FROM big WHERE mod(a, 50000) = 0` (`ShareFits == true`, `SafeRead == true`, `Shareable == true`) returned rows in a different order on immediate re-run #2:
    - Run 1: `D|50000;D|150000;D|100000;D|200000;C:SELECT 4;`
    - Run 2: `D|50000;D|100000;D|150000;D|200000;C:SELECT 4;`
  - Root cause: once a plain table exceeds Postgres 17's default `min_parallel_table_scan_size` (8 MB), the planner picks `Gather -> Parallel Seq Scan`, whose workers return blocks in race order. The builder's own Tier R tables have 2–4 rows and never crossed the 8 MB threshold.
- **Judgment:** **Over-broad claim** (and for `string_agg` without `ORDER BY`, an unstated limit of `ShareAllowList`). SQL does not define row order without `ORDER BY` or input order to `string_agg` without `ORDER BY`; coalescing such a query still returns a valid SQL result, so Claim 2's literal promise ("never returns different rows on an immediate re-run") needs either a deterministic-order caveat or exclusion of order-sensitive aggregates like `string_agg`.

### 2. Claim 2 — `TestAdv_2_FloatSumParallelNonAssociativity`

- **Command and seed:**
  ```sh
  ADV_SEED=2026112605 agent_work/adversarial/c05-classifier-neon/run-tier-r.sh
  # or against an existing Postgres 17 instance:
  PATH=/usr/local/go/bin:$PATH ADV_SEED=2026112605 ADV_TIER_R=1 ADV_PG_ADDR=127.0.0.1:<port> \
    go test -count=1 -v -run '^TestAdv_2_FloatSumParallelNonAssociativity$' ./agent_work/adversarial/c05-classifier-neon/...
  ```
- **Expected from Claim 2:** On Postgres 17 with plain tables only, no text that fits the safe shape returns different rows on an immediate re-run.
- **Observed (Postgres 17.11, stock defaults, single plain table `bigf (a int, x double precision, pad text)` with 200,000 rows):**
  - `SELECT sum(x) FROM bigf WHERE a > 0` (`ShareFits == true`, `SafeRead == true`, `Shareable == true`, single scalar row, no `string_agg`, no multi-row ordering) returned different IEEE-754 float8 totals on immediate re-run #2:
    - Run 1: `-9.999999176946144e+15`
    - Run 2: `-9.999999176945808e+15`
  - Root cause: Postgres 17 runs `Finalize Aggregate -> Gather -> Partial Aggregate -> Parallel Seq Scan`, and IEEE-754 floating-point addition is non-associative across worker completion orders.
- **Judgment:** **Over-broad claim.** Like the parallel-scan ordering break above, this is inherent to parallel IEEE-754 aggregation in Postgres 17 on plain tables and is safe to coalesce in practice (every run is a valid Postgres evaluation), so Claim 2's literal re-run equality wording is broader than Postgres 17 guarantees on tables > 8 MB.

### 3. Claim 5 — `TestAdv_5_QuadraticSpanScanDoS`

- **Command and seed:**
  ```sh
  PATH=/usr/local/go/bin:$PATH ADV_SEED=2026112605 \
    go test -count=1 -v -run '^TestAdv_5_QuadraticSpanScanDoS$' ./agent_work/adversarial/c05-classifier-neon/...
  ```
- **Expected from Claim 5:** Lexer, classifier and recogniser never panic and use memory and time bounded by a stated multiple of the input length, on any bytes (`NOTES.md` states linear time: 16× input takes at most 96× as long).
- **Observed:** On 16× input growth (16 KiB → 256 KiB):
  - `"SELECT " + strings.Repeat("pg_settings ", N) + "1"`: `12.73ms -> 2.68s` (**210.4×**, bound ≤ 96×)
  - `"SELECT " + strings.Repeat("client_encoding ", N) + "1"`: `11.48ms -> 3.07s` (**267.6×**, bound ≤ 96×)
  - `"SELECT " + strings.Repeat("standard_conforming_strings ", N) + "1"`: `4.53ms -> 1.11s` (**244.5×**, bound ≤ 96×)
  - Root cause: in `internal/pgwire/classify.go` lines 421–426, `tokenReject(s, i)` is invoked for every token `i` in statement span `s` and calls `m.spanHas(s, "update")` on every `pg_settings` token and `m.spanHas(s, "set") || m.spanHas(s, "reset")` on every `client_encoding` / `standard_conforming_strings` token. Each `spanHas` rescans the entire span `[s.lo, s.hi)`, making `ClassifyQuery` **Θ(N²)** when those identifiers repeat without `update`/`set`/`reset`. A single 1 MiB message takes tens of seconds of CPU on one core.
- **Judgment:** **Product defect.** Straightforward to fix in O(N) by computing per-span booleans (`hasUpdate`, `hasSetOrReset`) once per span in `rejectReason` instead of scanning the full span inside `tokenReject`.

### 4. Claim 6 — `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused`

- **Command and seed:**
  ```sh
  PATH=/usr/local/go/bin:$PATH ADV_SEED=2026112605 \
    go test -count=1 -v -run '^TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused$' ./agent_work/adversarial/c05-classifier-neon/...
  ```
- **Expected from Claim 6:** Nothing is refused for being unknown; a valid plain-table `SELECT` with no reject-list feature (and outside the documented over-rejections in `docs/reject-list.md` and `NOTES.md`) is forwarded, never rejected.
- **Observed:** All four valid Postgres 17 plain-table queries (`SELECT a FROM local temp`, `SELECT a FROM global temporary`, `SELECT a FROM replace temp`, `SELECT a FROM local temporary` — where `local`, `global`, `replace`, `temp`, `temporary` are unreserved keywords and `SELECT a FROM local temp` executes cleanly on Postgres 17 in `TestAdv_2_HostileSafeShapePlainTableSafety`) are rejected with `Class=reject Reason=temp-object`, even while `ShareFits` simultaneously marks them `SafeRead=true`.
  - Root cause: `internal/pgwire/classify.go` line 404 matches `temp`/`temporary` after `local`/`global`/`replace` anywhere in any statement without requiring `CREATE` or `INTO` in the statement, and neither `docs/reject-list.md` nor `NOTES.md` documents this over-rejection.
- **Judgment:** **Product defect** (minor over-rejection outside the documented exceptions; also creates the odd state `SafeRead=true` + `Class=reject` on a valid plain-table `SELECT`).

### 5. Claim 8 — `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts`

- **Command and seed:**
  ```sh
  PATH=/usr/local/go/bin:$PATH ADV_SEED=2026112605 \
    go test -count=1 -v -run '^TestAdv_8_FailedBuildDoesNotInflateBuiltCounts$' ./agent_work/adversarial/c05-classifier-neon/...
  ```
- **Expected from Claim 8 (and `clssrc.go` metric definitions):** Every proxy-made reply is well formed and accurately counted on every path; calls to `ReplyBuilder.Build` that return an error (`ReplyErrNoMessage` for `ErrRowPassThrough`, `ReplyErrBadStatus` for invalid status `'X'`) and emit zero bytes should increment `Failed` (`reply_failures`: "Reply builds refused for bad input") and not inflate `Total` (`replies_built`: "Replies the proxy built itself, all rows") or `ByRow` (`replies{reason=...}`).
- **Observed:** After three refused `Build` calls (`ErrRowPassThrough` with `'I'`, `ErrRowShed` with `'X'`, `ErrRowCount` with `'I'`), all three returned errors and emitted 0 bytes, yet `ReplyBuilder.Snapshot()` reported `Total=2, ByRow[pass-through]=1, ByRow[shed]=1, Failed=3`.
  - Root cause: `internal/pgwire/reply.go` lines 46–47 increment `b.snap.ByRow[id]++` and `b.snap.Total++` *before* checking `ErrorRowPassThrough` (line 51) and `ReplyValidStatus(status)` (line 55), whereas `id >= ErrRowCount` (line 41) returns before incrementing them. (Note: `errors_test.go` line 275 currently locks in `ByRow[ErrRowShed] == 3` on bad status.)
- **Judgment:** **Product defect** in counter semantics/consistency (`replies_built` and `replies{reason=pass-through}` count refused builds that produced no reply).

## Claims not attacked

None. All 11 claims were attacked with 3 to 4 distinct named attacks each (38 total). All 4 Claim 2 attacks were executed against a real Postgres 17.11 Docker container (`run-tier-r.sh`); none was counted as held from a skipped default run.

## Courtesy gaps

Per the scope note (ruling 9, PLAN.md D8 and section 9.4), forwarded spellings of session features do not affect the verdict or HELD/BROKEN counts and are recorded separately here:

1. **String-continued `UESCAPE` literal in unicode-escaped advisory-lock call:** `SELECT U&"pg_advisory_!006cock" UESCAPE '!'\n'' (1)` is forwarded because `classify.go` line 278 checks `len(body) != 3` on the raw token slice rather than unquoting a continued string literal (Postgres 17 joins `'!'\n''` into `'!'`).
2. **Indirect session changes (already noted in `NOTES.md`):** `DO` blocks, user functions/procedures, views, rules, and triggers that invoke `SET`, `set_config`, `LISTEN`, `PREPARE`, or session advisory locks are forwarded and rely on chunk 6's `DISCARD ALL` lease reset.

## Test ledger audit (`NOTES.md`)

Audited the 80-row test ledger in `agent_work/chunks/c05-classifier/NOTES.md`:

- **Kept (46) and hoisted (30):** Every kept and hoisted test function exists in the tree (`internal/pgwire/*_test.go`, `internal/work/work_test.go`, `internal/obs/clssrc/clssrc_test.go`, `internal/obs/simrun/classifier_test.go`) and continues to assert its surviving property. Note that `TestReplyMetricsCoverEveryRow` in `clssrc_test.go` now only exercises `ErrRowShed` directly, while full-table row coverage is asserted by its immediate neighbours `TestZeroSnapshotEmitsEveryLabelValue` and `TestMappingMovesEveryMetricAndExportsEveryField` in the same file.
- **Retired (3 tests + 1 script):** `TestAdv_6_ChainAndNoChain`, `TestAdv_6_FaultBeforeAndAfterBegin`, and `TestAdv_6_UnicodeWhitespaceAndControl` all tested the first build's Claim 6 (`TxEffect` / `MayLeaveOpen`), which was removed; `run-tier-r.sh` was superseded by `scripts/tier-r.sh`. No retired test's behaviour still exists in the product.

## Execution evidence

- **Default run (`PATH=/usr/local/go/bin:$PATH:$HOME/.local/bin go test -count=1 -v ./agent_work/adversarial/c05-classifier-neon/...`):** 33.78s total (well under the 3-minute budget). 31 passed, 3 failed (`TestAdv_5_QuadraticSpanScanDoS`, `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused`, `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts`), 4 Claim 2 Tier R attacks skipped without `ADV_TIER_R=1`.
- **Tier R run (`agent_work/adversarial/c05-classifier-neon/run-tier-r.sh`, Postgres 17.11):** 2.89s. 2 passed (`TestAdv_2_CatalogPurityOfAllowListOperatorsAndKeywords`, `TestAdv_2_HostileSafeShapePlainTableSafety`), 2 failed (`TestAdv_2_ParallelScanDifferentRowsOnRerun`, `TestAdv_2_FloatSumParallelNonAssociativity`).
- **Long campaign (`PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -v -run '^(TestAdv_3_SafeShapeImpliesCleanSingleLexedStatement|TestAdv_5_SeededHostileFuzzCampaign)$' ./agent_work/adversarial/c05-classifier-neon/...`):** 1.27s. Passed (20,000 safe-shape/lexer agreement checks + 100,000 seeded hostile fuzz inputs, 0 panics, 0 invariant breaks).
- **Cleanliness:** `gofmt -l agent_work/adversarial/c05-classifier-neon/` empty; `go vet ./agent_work/adversarial/c05-classifier-neon/...` clean; no files modified outside `agent_work/adversarial/c05-classifier-neon/`.
