# CR-3 — linear-growth tests without the wall clock: prose notes

## What was written

1. **Go comments (comments only; no code, import, or formatting token changes):**
   - `internal/pgwire/clscount/off.go` and `internal/pgwire/clscount/on.go`: package doc
     comment (after the `//go:build` constraint and blank line) and doc comments on every
     exported identifier (`On`, `Tally`, `Ref`, `(*Tally).Bump`, `(*Tally).Add`,
     `(*Tally).Value`, `(*Tally).Ref`, `(Ref).Bump`) in both build-tag files.
   - `internal/pgwire/lex.go`: comments on the CR-3 fields in `Lexer` (`reads` placement as
     first field, `opRunBytes`, `lookahead`), on `(*Lexer).scan` (explaining the AST guard
     invariant), on `lexContinuation` (return index on failure and `clscount.Ref`), and on
     `lexHasPrefix` (why it takes `clscount.Ref`).
   - `internal/pgwire/classify.go`: comments on `clsCounts`, `clsLexCounts` (register/SSA
     reason for grouping the four tagged tallies), `clsRunCounted`, `(*clsMsg).lex`
     (loop-step guard and `countPass`), and the ordering invariant in `setConfigLocal`.
   - `internal/pgwire/classify_test.go`: comment on the 4 KiB / 64 KiB / 17× constants and
     per-test property comments on `TestLexOperatorRunsGrowLinearly`,
     `TestClassifierSpanKeywordChecksGrowLinearly`, `TestClassifierWorkGrowsLinearly`, and
     `TestClassifierCountsTheSetConfigLiteralItScans`.
   - `internal/pgwire/classify_count_test.go`: comments on `clsReadsGrowthFloor`,
     `clsSkipUncounted`, `TestCountedWorkGrowsLinearly`, and
     `TestCountedCheckRejectsWorkThatDoesNotGrowLinearly`.
   - `internal/pgwire/count_guard_test.go`: per-test property comments on
     `TestLexLoopsCountTheirSteps`, `TestClassifierLexLoopsCountTheirSteps`, and
     `TestLexLoopGuardRejectsUncountedWork`.
   - `scripts/counted_growth_test.go`: per-test property comments on all five tests.
2. **Book (`book/chapters/05-classifier.md`; `book/manifest.json` untouched):**
   - Updated the quoted `go test` command and output (replacing `TestClassifierTimeGrowsLinearly`
     with `TestClassifierWorkGrowsLinearly` and quoting real output from this branch).
   - Replaced the outdated sentence in the `Fuzzing and bounded work` bullet ("Across all 23
     linear-work shapes, 16× input growth measures 7–34× time (bound: 96×)") with the
     operation-counting bound (26 shapes, 16× input, at most 17× work, independent of machine
     load).
   - Added one short paragraph describing the `-tags clscount` build, `scripts/counted-growth.sh`,
     `TestCountedWorkGrowsLinearly`, the AST loop guards, and the default-build limit, followed
     by real output from `scripts/counted-growth.sh`. No other sentences in Chapter 5 were
     changed.
3. **Knowledge pages (`docs/knowledge/foundation-gates.md`, `docs/knowledge/classifier.md`):**
   - Rewrote the CR-3 sections in Clarity rewrite mode, correcting "per byte" to "per loop
     step" in `foundation-gates.md`, recording that `gate.sh` unsets `COUNTED_GO`, adding
     `TestClassifierWorkGrowsLinearly` to the linear-work bullet in `classifier.md`, and
     stating the default-build limit plainly in both pages.

## Narrowed claims and omitted details

- **Default build vs `-tags clscount` (Claim 6 narrowed):** Stated plainly in Chapter 5,
  `classify_test.go`, `docs/knowledge/classifier.md`, and `docs/knowledge/foundation-gates.md`
  that string bodies (`'x'`) and line comments (`-- c\n`, `-`) are not counted in the default
  build, and that the whole-call linear-work claim holds in the tagged `-tags clscount` run.
- **Per-loop-step counting, not per-byte:** Stated as loop steps and lexer-pass token steps
  rather than claiming every individual byte read is separately incremented.
- **Hot-path cost:** Did not claim benchmark-measured timing parity (load on vm-1 made
  microbenchmarks noisy); only stated the compile-time elimination of `if clscount.On` blocks
  supported by the assembly comparison in `NOTES.md`.
- **AST guard limits:** Described what `TestLexLoopsCountTheirSteps` and
  `TestClassifierLexLoopsCountTheirSteps` check without claiming they prevent every possible
  syntactic evasion (two defensive guard gaps remain open in `STATE.md`).
- **Making-of omitted:** Published prose in `book/chapters/05-classifier.md` describes only the
  software and its tests, with no mention of CR-3, seats, reviews, attacks, or repair rounds.
- **Links verified:** Every linked path in Chapter 5 (`../../internal/pgwire/clscount/on.go`,
  `../../scripts/counted-growth.sh`) is tracked by `git ls-files`.

## Commands run

```sh
export PATH="/usr/local/go/bin:$PATH"
gofmt -l .
go doc -all ./internal/pgwire/clscount
scripts/counted-growth.sh
scripts/counted-growth.sh | grep -E '^(NOTE:|=== RUN|--- PASS:|PASS|ok)'
go test -count=1 -v -run '^(TestSafeShapeTable|TestSafeShapePropertyFitsAgreesWithTheReferenceChecker|TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails|TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier|TestSafeShapeAllocatesNothing|TestClassifierCorpus|TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade|TestClassifierPlantedSetForwardableIsCaught|TestClassifierMemoryIsBounded|TestLexOperatorRunsGrowLinearly|TestClassifierSpanKeywordChecksGrowLinearly|TestClassifierWorkGrowsLinearly)$' ./internal/pgwire
go build ./... && go vet ./...
go test -count=1 ./...
go test -tags clscount -count=1 ./internal/pgwire/
go test -count=1 ./scripts/
scripts/site.sh /tmp/cr03-prose-site
GATE_OUT=/tmp/cr03-prose-g2 GATE_STAGE=final GATE_PREPROSE=2f6a2ba GATE_ONLY=G2 scripts/gate.sh cr03-growth-tests
```
