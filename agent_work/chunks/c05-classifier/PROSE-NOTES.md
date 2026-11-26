# Chunk 5 (c05-classifier) — prose pass notes

Prose pass by Neon on 2026-11-26 in `/root/Software/mini-gateway-work/c05r` (branch
`work/c05r-classifier`, pre-prose commit `cd8fa20`). No Go code, tokens, imports, tests'
logic, testdata, generated contract documents, `PLAN.md`, `STATE.md`, or `docs/c4/` files
were changed.

## What was written

### 1. Go comments (comments only, verified by `astdiff`)

- **Production files:**
  - `internal/work/work.go` — package doc comment and doc comments on every exported type, constant, and method (strictly protocol-free; no Postgres terms or package names in comments so `TestWorkImportsOnlyTheStandardLibrary` and `TestAdv_10_NoPostgresSymbolsOrIdentifiersInWork` remain green).
  - `internal/pgwire/share.go`, `internal/pgwire/share_doc.go` — doc comments on every exported constant, table, and function, plus inline invariants explaining zero imports/lexer independence, `ShareParenKeywords` category `R`/`C` selection, raw-span time-word scanning, and strict leading-`select` handling.
  - `internal/pgwire/errors.go`, `internal/pgwire/errors_doc.go` — doc comments on `ErrRowID` (all 20 rows), `ErrorRowReply`, `ErrorRowConn`, `ErrorRow`, `ErrorRows`, `ErrRowFor`, and `ErrorRowsMarkdown`.
  - `internal/pgwire/reply.go` — doc comments on `ReplyDetailPrefix`, error sentinels, `ReplyEnd`, `ReplyAction`, `ReplySnapshot`, `ReplyBuilder`, `ReplyValidStatus`, `ReplyReady`, `SyncDiscardStep`, `SyncDiscardSnapshot`, `SyncDiscard`, `SyncDiscardExtended`, and inline invariants for failed-build counting, `FATAL` omitting `ReadyForQuery`, local encoding, and `Sync`/`FunctionCall`/`Terminate` handling.
  - `internal/pgwire/lex.go` — doc comments on `TokenKind`, `Token`, `Lexer`, `LexerFor`, `LexAll`, `Next`, and inline invariants for `lexOpRun`, `lexContinuation`, and `scanOpRun`.
  - `internal/pgwire/classify.go`, `internal/pgwire/snapshot_classifier.go` — doc comments on memory-ceiling constants, `ClassifErrConservation`, `ClassifierConfig`, `Classifier`, `ClassifierSnapshot`, `Classify`, `ClassifyQuery`, `Check`, and inline invariants for `SafeRead`/`Hinted` raw-byte decoupling, two-pass `lex()` sizing, `makesTemp`, and `spanWords`.
  - `internal/pgwire/rejectlist.go`, `internal/pgwire/rejectlist_doc.go` — doc comments on `RejectRule`, `RejectRules`, `RejectAllowedForm`, `RejectAllowedForms`, and `RejectListMarkdown`.
  - `internal/pgwire/pgwiretest/clsworld/clsworld.go` — package doc comment and doc comments on every exported constant, type, function, and method.
  - `internal/obs/clssrc/clssrc.go` — package doc comment and doc comments on `Snapshot`, `Metrics`, `Source`, and `Map`.
  - `internal/obs/simrun/classifier.go` and `internal/obs/simrun/simrun.go` — doc comments on `ClassifierFingerprint`, `Classifier`, and the added `Run.Classifier` field.
- **Test files (brief comment per test/fuzz function stating the property proved):**
  - `internal/work/work_test.go`
  - `internal/pgwire/lex_test.go`, `lex_continue_test.go`
  - `internal/pgwire/classify_test.go`, `classify_attacks_test.go`, `classify_e2e_test.go`, `classify_export_test.go`, `classify_firstbuild_test.go`, `classify_obs_test.go`, `classify_obs_ext_test.go`, `classify_repair_test.go`, `classify_spellings_test.go`, `classify_tierr_test.go`
  - `internal/pgwire/errors_test.go`, `rejectlist_test.go`, `share_test.go`, `share_tierr_test.go`
  - `internal/obs/clssrc/clssrc_test.go`
  - `internal/obs/simrun/classifier_test.go`

### 2. Book chapter and manifest

- `book/chapters/05-classifier.md` — Chapter 5: *What the proxy says and what it will not guess: the error contract, a courtesy list, and a safe shape for sharing*. Covers the rework history (first build's 11 holes over 3 rounds, parking, 19-project canvas, options memo, 3-stage Postgres 17 lease experiment, Nick's Ruling 9 / D8), the architecture and code excerpts (`ShareFits`, `ReplyBuilder.Build`), observability and DuckDB CSV assertions, test retention ledger (46 kept, 30 hoisted, 3 retired; 599 corpus records), verification (Review 1 NITS, Astra 31 held / 5 broken, Neon 33 held / 5 broken, 11 reconciled decisions, one repair round, Review 2 FAIL on `makesTemp`, Astra arbitration PASS after one-line fix), all three narrowed claims, and stated limits.
- `book/manifest.json` — updated chapter 5's `title`, `file` (`book/chapters/05-classifier.md`), and `status` (`live`).
- `book/index.md` — updated live chapter summary to Chapters 1 through 5.

### 3. Reader-facing documentation

- `README.md` — updated Status (Chunk 5 landed, Chapter 5 live, removed paused note, explicitly noted no runnable proxy binary or `DISCARD ALL` lease reset yet), Where to read (added Chapter 5 and generated contract docs links), and Running what exists today (`TestScenarioClassifierFlush`, 65 SQL assertions, 14 fuzz targets, all four generated docs in `gen-docs.sh --check`).
- `docs/index.md` — updated Status and "On this site" table for Chunk 5 landed and Chapter 5 live; removed the sentence saying Chunk 5 is paused.

## Claims narrowed or bounded by evidence

1. **Claim 2 — Row-level security policies (R3):** written strictly as plain tables *without active row-level security policies*; databases using RLS policies must use `coalesce: off` or `hinted`.
2. **Claim 2 — Multiset repeatability under parallel plans (R4):** written strictly as repeating *as a multiset of rows* for queries whose value SQL defines; row order without `ORDER BY`, order-dependent aggregates (`string_agg`, `array_agg` without inner `ORDER BY`), and non-associative IEEE-754 floating-point aggregates (`sum`, `avg` over `real`/`double precision`) may vary across parallel-plan runs past `min_parallel_table_scan_size` (8 MB).
3. **Claim 6 — Unreadable arguments to a recognised reject-list feature (R5):** written in its exact narrowed form — never refused merely for being untokenisable or unmodelled, with the three deliberate exceptions (reject-list feature in the readable prefix of an untokenisable message; `set_config` whose setting name is not one plain `'...'` literal; transaction-local `search_path`/`SCHEMA` value the classifier cannot read).
4. **Reset not built yet:** stated plainly in Chapter 5 and `README.md` that `DISCARD ALL` lease reset lands in Chunk 6 (and pool policies in Chunk 7), not Chunk 5.
5. **Lease experiment real-tier p99 wait:** reported as inconclusive on real Postgres 17 (inside the A/A noise floor) and resting on simulator evidence, matching `lease-exp/REPORT.md`.
6. **Build-stage gate record:** stated plainly that no post-arbitration build-stage `gate/result.json` exists; cited the pre-arbitration run recorded in `NOTES.md` (`ec18a84`) and noted the final-stage gate runs after this pass.
7. **Courtesy gaps:** noted the continued-`UESCAPE` advisory-lock gap (`R11`) and indirect session changes (functions, views, `DO` blocks, rules, triggers) explicitly as best-effort courtesy limits.

## Commands run

- `PATH=/usr/local/go/bin:$HOME/.local/bin:$PATH go test -count=1 -v -run '^(TestSafeShapeTable|TestSafeShapePropertyFitsAgreesWithTheReferenceChecker|TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails|TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier|TestSafeShapeAllocatesNothing|TestClassifierCorpus|TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade|TestClassifierPlantedSetForwardableIsCaught|TestClassifierMemoryIsBounded|TestLexOperatorRunsGrowLinearly|TestClassifierSpanKeywordChecksGrowLinearly|TestClassifierTimeGrowsLinearly)$' ./internal/pgwire` — PASS (8.835 s; 599 corpus records, 60 fit, 40/116 `was-co` fit; worst memory 52 B/byte at 1 MiB; linearity ratios 11–29, 7–18, 13–34 vs bound 96; 15,817/200,000 random fits agreed; alphabet canary 113,896 leaking texts; `ShareFits` 0 allocs).
- `scripts/gen-docs.sh --check` — PASS (`NOTE: docs/csv-schema.md is current`).
- `RUN_DIR=/tmp/c05-all-runs go test -count=1 -run '^(TestScenarioFlush|TestScenarioWireFlush|TestScenarioClassifierFlush)$' ./internal/obs/simrun -seed=1 && scripts/csv-assert.sh /tmp/c05-all-runs` — PASS (14.248 s; 26 run directories; 65 SQL files run, 0 failures; DuckDB v1.5.5).
- `gofmt -l .` — empty.
- `scripts/site.sh /tmp/c05r-site` — PASS (`built 23 pages into /tmp/c05r-site`; `book/05-classifier.html` present and linked in `book/index.html` and `pages.txt`).
- `go build ./... && go vet ./... && go test -count=1 ./...` — PASS (all 30 packages with tests green, including both Chunk 5 adversarial packages).
- Final-stage AST and path verification (`GATE_STAGE=final GATE_PREPROSE=cd8fa20 GATE_ONLY=G0,G1,G2,G8,G9 GATE_OUT=/tmp/c05r-g2-check scripts/gate.sh c05-classifier`) — verified after commit.
