# CR-3 — linear-growth tests without the wall clock: builder notes

Built by Opus on 2026-11-27 on branch `work/cr03-growth-tests` (worktree
`/root/Software/mini-gateway-work/cr03`) from `/root/briefs/zgateway-p2-cr03-growth-tests.md`
and `CHANGE-REQUEST.md` in this directory. Request item 1 (count work) was used; item 2
(a stronger timing form) was not needed.

## What was built

The two tests now count work instead of timing it. Each test builds the same `SELECT ` plus
4 KiB and plus 64 KiB inputs as before. It runs the lexer over each input with a `Lexer` and
the classifier with `clsRunCounted`, then asserts that the 64 KiB count is **at most 17 times**
the 4 KiB count (`clsWorkGrowthLimit`). No test in the default run reads the clock to reach
a verdict on these two properties.

| Piece | Where |
| --- | --- |
| Counter 1, `Lexer.opRunBytes`: bytes in each operator run the lexer scans. One add per scanned run, inside the branch of `operator()` that scans a new run | `internal/pgwire/lex.go` (field and one line in `operator`) |
| Counter 2, `clsMsg.keywordChecks`: calls to `clsMsg.is`, the keyword comparison every span or keyword search goes through (`isAny`, `spanWords`, `leadReject`, `tokenReject`, ...) | `internal/pgwire/classify.go` (field and one line in `is`) |
| Seam `clsRunCounted(text, inTx, plant) (work.Unit, int)`: the old `clsRun` body with the count returned alongside the unit. `clsRun` is now a two-line wrapper the compiler inlines (`-gcflags=-m`: "can inline clsRun", inlined at both callers) | `internal/pgwire/classify.go` |
| Tests `TestLexOperatorRunsGrowLinearly` (asserts on `opRunBytes`), `TestClassifierSpanKeywordChecksGrowLinearly` (asserts on `keywordChecks`); helpers `clsMeasureWork`, `clsCheckLinearShapes`, `clsHasKind`, `clsCheckGrowth`; constants `clsWorkSmallInput`, `clsWorkLargeInput`, `clsWorkGrowthLimit` | `internal/pgwire/classify_test.go` |

Each test asserts the growth bound on **both** counters for every one of its shapes. It
also asserts that its own counter moved, wherever the input contains the token kind that
counter's path handles (`TokenOperator` for the lexer test, `TokenWord` for the classifier
test).

The old comments on the two tests ("linear time (16x input <= 96x time)") were no longer
true, so they were deleted. (First build: `clsFastest` and `clsTimeGrowthLimit` stayed for
`TestClassifierTimeGrowsLinearly`. The follow-up build below removed them with that test's
timing form.)

## Decisions the brief left open

1. **What to count.** I counted the two paths the repair made linear, as the request
   suggests. The lexer counter sits where the old code rescanned: bytes scanned per
   operator run. The classifier counter is broader than `spanWords` alone. It counts every
   keyword comparison, because the old defect (`spanHas` once per matching name) and any
   other rescan of a span by keyword go through `is`. A per-byte counter for the whole
   lexer was rejected because it would touch every scan loop.
2. **Bound: 17 times, not 16.** Measured growth is 15.98 to 16.003 for every shape (table
   below). The extra 1 covers the constant `SELECT ` prefix and the rounding of the repeat
   count (`4096/len(unit)` against `65536/len(unit)`). The two quadratic defects give 256
   and 251 to 252 (see Proof), so the bound separates the two cases by a factor of about 15.
3. **Short mode.** The old tests skipped under `testing.Short()` because they timed. The new
   ones are deterministic and fast (0.13 s and 0.03 s), so they run in `-short` and in
   `scripts/ci-fast.sh` too.
4. **Shape `"-"`.** Repeated `-` gives `SELECT ----…`, which lexes as **one line comment**.
   It never reached the operator path, under the old timing test either. It stays in the
   list, as the request asks, and the "counter moved" check exempts it because its input has
   no operator token. I **added `"-+"`**, a long operator run that starts with `-`,
   so a minus-led run really is covered. Flag for the prose seat: chunk 5's notes and book
   text that say "eight operator-run shapes" should treat `"-"` as a comment shape. The
   list now has nine shapes, eight of which exercise operator runs.
5. **The seam's shape.** The first version moved `clsRun`'s body into a method
   `(*clsMsg).unit` that read `m.src`. That broke the chunk 5 adversary's syntax check
   `TestAdv_3_GatesUseRawArgument`, which requires the literal `u.SafeRead =
   ShareFits(text)`. The second commit (`0fcebe3`) keeps the body unchanged as
   `clsRunCounted` and only changes its two `return` lines. The adversary's tests were not
   edited.

## Hot-path cost (justification of the product seam)

- **Code emitted** (`go build -gcflags=-S`, `(*clsMsg).is` and `(*Lexer).operator`, base
  `4cf67c6` against this branch): `is` gains one `INCQ 144(AX)`. `operator` gains one
  `SUBQ` and one `ADDQ` on the branch that scans a new run, which runs once per operator
  run, not per byte. No new calls, branches, allocations or loads on a shared location.
- **No shared state.** Both counters are fields of per-call values (`Lexer`, `clsMsg`),
  so there is no data race and nothing reaches a metric. `ClassifierSnapshot`, `Unit`,
  `Token` and every exported signature are unchanged. The counters are unexported and no
  product code reads them.
- **Timing could not resolve it on this machine.** Benchmarks of `ClassifyQuery` on 2.3 KiB
  of mixed SQL and `LexAll` on 2 KiB, 30 interleaved rounds, base against seam. ns/op, min
  and median:
  - `ClassifyQuery`: min 27,214 against 28,155; median 39,218 against 37,792.
  - `LexAll`: min 114,501 against 129,325; median 205,815 against 187,864.

  The machine was at load 7 to 15 on 4 cores, and single runs varied 4 to 8 times. The
  medians favour the seam and the minimums favour the base, both well inside the noise.
  `perf stat -e instructions:u` is not supported in this VM, so the instruction-level
  argument above is the evidence. The benchmark file was scratch only (`/tmp`), not
  committed.

## Claims and their tests

| # | Claim | Test |
| --- | --- | --- |
| 1 | Operator runs lex in linear work: for each of `+ - -+ +- *+ =- <+- "+ " 1+` the lexer's operator-run bytes on 64 KiB are at most 17 times those on 4 KiB | `internal/pgwire/classify_test.go: TestLexOperatorRunsGrowLinearly` |
| 2 | Repeated setting names classify in linear work, with no rescan of earlier spans: for each of `pg_settings `, `standard_conforming_strings `, `client_encoding `, `"pg_settings" `, `pg_settings, `, `SELECT pg_settings ` the classifier's keyword checks on 64 KiB are at most 17 times those on 4 KiB | `internal/pgwire/classify_test.go: TestClassifierSpanKeywordChecksGrowLinearly` |
| 3 | Each of the two tests also holds the other counter linear on its shapes, and fails if its own counter stays at zero on an input that has the relevant token kind | both tests above (`clsCheckLinearShapes`) |
| 4 | The verdict does not depend on time or load: same counts on every run, under load | Proof 2 below |
| 5 | Each test can fail: putting back each repaired defect fails the matching test | Proof 1 below; M2 repeated on the follow-up tree (see "Follow-up build") |
| 6 | The whole classification of an input does linear work: for each of `(`, `a `, `;`, `'x'`, `set_config(1,2,`, `U&"a" `, `SELECT abs(t.a) FROM t; `, `'a'\n`, `-- c\n` (no prefix), every counter (lexer operator-run and lookahead bytes, classifier keyword checks, token visits and literal bytes) on 64 KiB is at most 17 times its count on 4 KiB; token visits must be non-zero wherever the input has a significant token | `internal/pgwire/classify_test.go: TestClassifierWorkGrowsLinearly` (was `TestClassifierTimeGrowsLinearly`) |
| 7 | All three tests hold all five counters linear on their shapes | the three tests (`clsCheckLinearShapes`) |
| 8 | The whole-call test can fail on a quadratic defect outside the first build's two counted paths: a `matchParens` that scans forward for each `(` | "Follow-up build", proof F1 |
| 9 | No test in the package reads the clock: `grep -n 'time\.Now\|time\.Since' internal/pgwire/classify_test.go` prints nothing, and `time` is no longer imported | "Follow-up build", proof F3 |
| 10 | Verdict independent of load for all three tests: same counts in every run of `-count=100 -run Linearly` under load | "Follow-up build", proof F2 |

Counts measured on this branch (identical in every run). Each cell is the 4 KiB count →
the 64 KiB count; `-` means zero on both.

| Shape | Operator-run bytes | Keyword checks |
| --- | --- | --- |
| `+` | 4,096 → 65,536 | 16,401 → 262,161 |
| `-` | - | 17 → 17 |
| `-+` | 4,096 → 65,536 | 16,401 → 262,161 |
| `+-` | 4,096 → 65,536 | 16,401 → 262,161 |
| `*+` | 4,096 → 65,536 | 25 → 25 |
| `=-` | 4,096 → 65,536 | 25 → 25 |
| `<+-` | 4,095 → 65,535 | 29 → 29 |
| `"+ "` | 2,048 → 32,768 | 8,209 → 131,089 |
| `1+` | 2,048 → 32,768 | 16,401 → 262,161 |
| `pg_settings ` | - | 2,063 → 32,783 |
| `standard_conforming_strings ` | - | 893 → 14,057 |
| `client_encoding ` | - | 1,553 → 24,593 |
| `"pg_settings" ` | - | 1,185 → 18,741 |
| `pg_settings, ` | - | 3,167 → 50,427 |
| `SELECT pg_settings ` | - | 2,597 → 41,405 |

## Proof

All runs were in the scratch folder `/tmp/zgw-cr03-growth-tests/` (deleted at the end), on
copies of this branch's final tree (`0fcebe3`).

### 1. The two mutations

**Mutation M1: the lexer rescans each operator run** (undoes `a6341d8`). The old
`operator()` and `lexOperatorLen` were put back verbatim in a copy of `lex.go`, with the
counter added after the old loop: `l.opRunBytes += l.pos - start`. That counts the bytes the
old loop examined before it rewound.

```
cd <copy> && go test -count=1 -v -run GrowLinearly ./internal/pgwire/
--- FAIL: TestLexOperatorRunsGrowLinearly (356.55s)
    "+":  16 times the input took 2147516416 operator-run bytes against 8390656; want at most 17 times as many
    "-+": same numbers
    "+-": same numbers
--- PASS: TestClassifierSpanKeywordChecksGrowLinearly (0.02s)
```

**Caught by `TestLexOperatorRunsGrowLinearly`** (ratio 256). With the old code, `*+`, `=-`,
`<+-`, `"+ "` and `1+` stay linear, because their runs end in a keep-sign byte or are one
or two bytes long. That matches chunk 5's notes: only the pure sign runs were quadratic.
Under load the mutated test takes 6 to 7 minutes, because it really does 2.1 billion byte
steps. The correct code runs it in 0.13 s.

**Mutation M2: the classifier searches the span once per matching name** (undoes
`205a75f`). In a copy of `classify.go`, `w.update` became `m.spanHas(s, "update")` and
`w.setOrReset` became `(m.spanHas(s, "set") || m.spanHas(s, "reset"))`, with `spanHas` put
back verbatim. `spanHas` calls `is`, so no extra counting was needed.

```
--- PASS: TestLexOperatorRunsGrowLinearly (0.31s)
--- FAIL: TestClassifierSpanKeywordChecksGrowLinearly (4.37s)
    "pg_settings ":                 29860765 keyword checks against 118685 (251.6x)
    "standard_conforming_strings ": 10969937 against 43817 (250.4x)
    "client_encoding ":             33587217 against 133137 (252.3x)
    "\"pg_settings\" ":              21935183 against 86741 (252.9x)
    "pg_settings, ":                50878830 against 201932 (252.0x)
    "SELECT pg_settings ":          23836056 against 95262 (250.2x)
```

**Caught by `TestClassifierSpanKeywordChecksGrowLinearly`**, on all six shapes.

I ran both mutations on the first seam (`ef87017`) and again on the final one (`0fcebe3`),
with the same results.

### 2. The loaded run

```
go test -c -o /tmp/zgw-cr03-growth-tests/pgwire.test ./internal/pgwire/
cd internal/pgwire && /tmp/zgw-cr03-growth-tests/pgwire.test -test.count=200 -test.v -test.run GrowLinearly
```

The load was `yes > /dev/null` on all 4 cores, started 60 s before the run. On top of that
ran mutation M1's 6-minute test, plus the other agents' work on vm-1: two `go test ./...`
runs, a tier R run, and a `simrun` test. Load average was 15.61 at the start and 15.93 at
the end on 4 cores. In the first, lighter run the test process got 43% of a CPU.

- Result: exit 0, **400 PASS, 0 FAIL** (200 of each test), in 1 min 21 s wall time.
- Identical results: the log has exactly 15 distinct measurement lines, one per shape,
  and each appears exactly 200 times (`grep classify_test.go loaded.log | sort | uniq -c`).
  Every run gave the same counts.

A first loaded run on `ef87017` gave the same result: 400 PASS, 15 distinct lines × 200.

### 3. Chunk 5's adversarial tests, unchanged

```
go test -count=1 -v -timeout 30m ./agent_work/adversarial/c05-classifier/
ok  github.com/nickstrad/mini-gateway/agent_work/adversarial/c05-classifier  10.926s
```

32 PASS and 4 SKIP. The four skips are `SETTLED by the orchestrator`, as on main:
`TestAdv_2_PlainTablePolicyChangesSession`, `TestAdv_2_PlainTablePolicyChangesRepeatedRows`,
`TestAdv_6_UnreadableLocalArguments`, `TestAdv_8_ErrorRowsRandomOrderAccounting`. No file
under `agent_work/adversarial/` was edited. `TestAdv_3_GatesUseRawArgument` failed on the
first seam; see decision 5.

### 4. Nothing else changed

`git diff --stat 4cf67c6` shows `internal/pgwire/classify.go`, `internal/pgwire/lex.go`,
`internal/pgwire/classify_test.go` and this NOTES.md, nothing else.

## Follow-up build

Built by Opus on 2026-11-27, same branch and worktree, from
`/root/briefs/zgateway-p2-cr03b-growth-tests.md` and the section "Addition, 2026-11-27" of
`CHANGE-REQUEST.md`. **Addition item 1 (count work for the whole call) was used**; item 2
(an environment switch around the timing form) was not needed. Code commit: `e45b9b8`.

### What changed

`TestClassifierTimeGrowsLinearly` is now **`TestClassifierWorkGrowsLinearly`**. It no
longer measures time, so it was renamed (it still matches `-run Linearly`; it does not
match `GrowLinearly`). It keeps the nine shapes and the unprefixed inputs of the old test,
but uses 4 KiB against 64 KiB (the other two tests' sizes) in place of 64 KiB against
1 MiB: counts do not need large inputs to rise above noise, and the test takes 0.04 s. Its
old comment described the timing form, so it was deleted. `clsFastest`,
`clsTimeGrowthLimit` and the `time` import are gone. It no longer skips under `-short`.

All three tests now go through one helper, `clsCheckLinearShapes(t, prefix, measuredKind,
measured, units)`. It asserts the 17-times bound on **every** counter, not only the test's
own, and asserts that the test's own counter moved wherever the input has a token that
`measuredKind` accepts (`TokenOperator`, `TokenWord`, or for the whole-call test
`TokenKind.Significant`).

The counters, all unexported fields of per-call values:

| Counter | Counts | Where |
| --- | --- | --- |
| `Lexer.opRunBytes` (first build, unchanged) | bytes of each operator run scanned | `lex.go: operator` |
| `Lexer.lookahead` (new) | bytes `lexContinuation` looked at past a closing `'`, and bytes of a `$tag` scanned before deciding whether it is a dollar quote | `lex.go: quoted`, `dollar` |
| `clsCounts.keywordChecks` (first build, moved into `clsCounts`) | calls to `clsMsg.is` | `classify.go: is` |
| `clsCounts.tokenVisits` (new) | calls to the four token accessors `word`, `isPunct`, `isName`, `isString`. `is` and `isAny` go through `word`, so every keyword check is also a visit | `classify.go` |
| `clsCounts.literalBytes` (new) | bytes of a token whose text the classifier reads: `name`, `stringMayContain`, `setConfigLocal` | `classify.go` |

`clsRunCounted` now returns `(work.Unit, clsCounts)`. The test still runs the lexer
itself (`clsMeasureWork`), as in the first build.

### Why this covers the whole call

- **Lexer.** `Next` always moves forward, and its tokens are contiguous and cover every
  byte, so the bytes the lexer advances over sum to exactly `len(src)` for every input.
  Counting them would add nothing. The lexer can only do more than linear work where it
  reads bytes it does not consume: the operator run (`opRunBytes`), the look-ahead after a
  string for a continuation, and the tag of a would-be dollar quote (both `lookahead`).
  Every other read is `peek` at a fixed offset. A dollar-quote body search ends by
  consuming what it scanned (pos moves to the close or to the end of the input).
- **Classifier.** Every loop over tokens in `classify.go` (`matchParens`, `split`,
  `rejectReason`, `spanWords`, `declareReject`, `localPathMayNameTemp`, `lead`) tests
  tokens through the four accessors, so a loop that visits tokens again is counted
  without anyone remembering to count it. The work inside one token (case folding,
  unquoting, Unicode decoding, substring search) is counted by `literalBytes` where the
  token's text is read.
- **The lexer pass in `clsMsg.lex`** is two plain loops over `Next`, so it is linear
  whenever the lexer is. The test measures one lexer pass directly.

Small code changes needed for this: `lexContinuation` now returns the index where it
stopped on failure (it returned 0). Its only caller ignores the value unless `ok`, so
behaviour is unchanged. The `if next, ok := ...; ok` in `quoted` became two statements so
the look-ahead can be added. `word` went from one line to three.

### Hot-path cost

The same argument as the first build, from the emitted code (`go build -gcflags=-S`):
each new count is one `INCQ` or `ADDQ` on a field of the per-call `clsMsg` or `Lexer`
(`isName`: `INCQ 152(AX)`). They run once per token visit, once per continuation check
and once per `$`, never per byte. No new calls, branches, allocations or shared state.
`word`, `isPunct`, `isName`, `isString` and `clsRun` are still inlined (`-gcflags=-m`:
"can inline" for each). No exported type, signature, `Snapshot` field or metric changed.
No benchmark was run: the machine was at load 12 to 15, and the first build showed that
benchmarks here vary 4 to 8 times between runs.

### Measured counts (identical in every run)

`TestClassifierWorkGrowsLinearly`, 4 KiB → 64 KiB. Counters not shown are 0 on both
sizes, except `literalBytes`, shown where non-zero.

| Shape | Token visits | Keyword checks | Other |
| --- | --- | --- | --- |
| `(` | 40,971 → 655,371 | 16,395 → 262,155 | |
| `a ` | 32,778 → 524,298 | 12,299 → 196,619 | literal 2,048 → 32,768 |
| `;` | 28,672 → 458,752 | 0 → 0 | |
| `'x'` | 24 → 24 | 15 → 15 | one literal: `'x''x'…` is a single string |
| `set_config(1,2,` | 15,032 → 240,312 | 4,928 → 78,656 | literal 10 → 10 |
| `U&"a" ` | 10,238 → 163,838 | 3,419 → 54,619 | literal 3,410 → 54,610 |
| `SELECT abs(t.a) FROM t; ` | 24,990 → 401,310 | 10,030 → 161,070 | literal 2,720 → 43,680 |
| `'a'\n` | 24 → 24 | 15 → 15 | lookahead 2,047 → 32,767 (one continued string) |
| `-- c\n` | 0 → 0 | 0 → 0 | all comment: no significant token, so no visit check |

For the first build's 15 shapes the counters it reported are unchanged. The new counters on
those shapes: token visits 53,275 → 851,995 on the pure operator shapes (`+`, `-+`, `+-`,
`1+`), 26,651 → 426,011 on `"+ "`, constant (26 to 66) on `-`, `*+`, `=-`, `<+-`; on the
setting-name shapes 2,362 → 37,466 up to 8,847 → 141,175, and literal bytes 3,471 → 55,457
up to 3,948 → 63,186. Lookahead is 0 on all of them. The highest ratio of any counter on
any shape is 16.06 (`SELECT abs(t.a) FROM t; `: 170 against 2,730 repeats).

### Proof

All runs used the scratch folder `/tmp/zgw-cr03-growth-tests/` (deleted at the end), on
copies of `e45b9b8` made with `git archive`.

**F1. A planted defect outside the first build's two counted paths.** In a copy of
`classify.go`, `matchParens` was rewritten to find each `(`'s match by scanning forward
from it (depth counting, stopping at `;`, recording the first two commas at depth 0),
in place of the one-pass stack. It gives the same `parens` table, so
`TestClassifierCorpus` still passes on the copy, but it is quadratic on unclosed `(`. (A
run of the copy's whole package suite was stopped after about 6 minutes, since the defect
makes the package's 1 MiB inputs quadratic too. Only the corpus result is claimed.) It
uses `isPunct`, as the real code does, and adds no counting of its own.

```
cd /tmp/zgw-cr03-growth-tests/d1 && go test -count=1 -v -run 'Linearly' ./internal/pgwire/
--- PASS: TestLexOperatorRunsGrowLinearly (0.22s)
--- PASS: TestClassifierSpanKeywordChecksGrowLinearly (0.03s)
    "(": 16 times the input took 4295491595 token visits against 16809995; want at most 17 times as many
    "set_config(1,2,": 16 times the input took 162443805 token visits against 645661; want at most 17 times as many
--- FAIL: TestClassifierWorkGrowsLinearly (29.28s)
```

**Caught by `TestClassifierWorkGrowsLinearly`, through `tokenVisits`** (ratio 255.5 and
251.6). The first build's counters did not move: `opRunBytes` and `keywordChecks` passed
on every shape, and the first build's two tests passed. So the defect is in a path the
first build did not cover, and only the new counter catches it.

**M2 again on the follow-up tree.** The first build's mutation M2 (`spanHas` per matching
name) was re-planted on a copy of `e45b9b8`. `TestClassifierSpanKeywordChecksGrowLinearly`
fails on all six shapes as before (the same keyword-check numbers as in Proof 1, for
example 29,860,765 against 118,685), and now also on `tokenVisits` (29,915,384 against
122,104). The other two tests pass. M1 was not re-run: `operator` and `opRunBytes` are
unchanged since the first build, which ran M1 on two seams.

**F2. Loaded run.**

```
go test -c -o /tmp/zgw-cr03-growth-tests/pgwire.test ./internal/pgwire/
cd internal/pgwire && /tmp/zgw-cr03-growth-tests/pgwire.test -test.count=100 -test.v -test.run Linearly
```

Load: `yes > /dev/null` on all 4 cores, started 60 s before the run. On top of that ran
F1's mutated copy of the whole `internal/pgwire` suite and other agents' work on vm-1. Load
average 14.52 at the start and 13.81 at the end, on 4 cores.

- Result: exit 0, **300 PASS, 0 FAIL** (100 of each of the three tests), 50 s.
- Identical results: the log has exactly 24 distinct measurement lines (9 + 6 + 9 shapes),
  and each appears exactly 100 times.

**F3. No clock on a verdict path.** `grep -n 'time\.Now\|time\.Since'
internal/pgwire/classify_test.go` prints nothing (exit 1). The file no longer imports
`time`.

**F4. Chunk 5's adversarial tests, unchanged.** `go test -count=1
./agent_work/adversarial/c05-classifier/...` passes (`ok`, 10.5 s) on `e45b9b8`. No file
under `agent_work/adversarial/` was edited.

**F5. Nothing else changed.** `git diff --stat 5d03368 e45b9b8`: `internal/pgwire/classify.go`
+20 −5, `internal/pgwire/classify_test.go` +24 −50, `internal/pgwire/lex.go` +7 −3. The
rest of `internal/pgwire` passes (`go test -count=1 ./internal/pgwire/`, 26.6 s).

### Limits of the whole-call count

- **Shapes.** The bound is checked only on the listed shapes. A quadratic path that none
  of the 24 shapes reaches is not caught; the old timing tests had the same limit. For
  example, M2 is caught only by the span test: the whole-call shapes have no setting
  names.
- **Byte loops inside one counted step are not counted per byte.** Not counted: the
  compare in `lexHasPrefix` while searching a dollar-quote body (linear: a compare at
  one `$` stops at the next `$`, because a tag has no `$` inside), the three loops of
  `scanOpRun` (each at most the run length, so within a constant of `opRunBytes`), and
  the work inside `clsLowerASCII`, `clsInList`, `clsContinued` and `lexDecodeUnicode` on
  one name or literal (bounded by `literalBytes` of the same call). A defect that put new
  per-byte work inside one of these, without visiting more tokens or reading more literal
  bytes, would not be caught.
- **Direct token reads.** `name`, `transactionScopedSet`, `setConfigPathMayNameTemp`,
  `setConfigLocal` and `tokenReject` also index `m.toks` directly, after an accessor
  check on the same or a neighbouring token. A new loop that read `m.toks[i]` without an
  accessor would not be counted.
- **`ShareFits`** is not counted. It is bounded by `ShareMaxLen` (see "Limits" above).
  `share.go` must reference nothing from the lexer or classifier (an AST test enforces
  it), so a counter there would need shared state.
- **`clsMsg.lex`** runs the lexer twice; the test counts one lexer pass of its own.

### Flags for the prose seat

- `book/chapters/05-classifier.md` (lines 177, 204, 214) and chunk 5's `NOTES.md`,
  `PROSE-NOTES.md` and `REVIEW-1.md` name `TestClassifierTimeGrowsLinearly` and describe
  timing ratios against a bound of 96. The test is now `TestClassifierWorkGrowsLinearly`,
  and all three growth tests count work with a bound of 17. The book's `-run` list
  (line 177) no longer matches the third test. The builder rules forbid editing `book/`,
  so it was not changed.

## For the orchestrator: wall-clock tests with tight bounds in the default run

Deliverable 3. Nothing in this list was edited.

| Test | Where | Bound |
| --- | --- | --- |
| `TestAdv_5_OperatorSuffixRescanning` | `agent_work/adversarial/c05-classifier/attacks_test.go` (via `scaling`) | `SELECT ` + 512 against 8,192 `+`, 2 repetitions, best of 3, `time.Now`. Fails above **96 times**. Measured today: 18.8 and 12.5 |
| `TestAdv_5_WholeSpanRescanning` | same file | `SELECT ` + 512 against 8,192 `pg_settings `, same method, **96 times**. Measured: 14.9 and 17.3 |

Both run in the landing gate's G3: the adversarial packages are left out only with
`GATE_SHORT=1`. They do not skip under `-short`. Their small input runs in about 150 to
270 µs, the same order as the runs that flaked in CR-3's evidence, so they are open to
the same failure under load.

Chunk 5's own `TestClassifierTimeGrowsLinearly`, the third test of this class, was brought
into scope by the addition and no longer times anything (see "Follow-up build"). After the
follow-up, these two adversary tests are the only wall-clock growth tests left in the
default run that cover the classifier.

## Deviations

- One shape added (`"-+"`, decision 4). The request lists the shapes to keep, and all of
  them are kept.
- The hot-path cost is argued from the emitted instructions, not shown by a clean
  benchmark: load made benchmarks unusable and the VM has no performance counters.
- The gate ran as `GATE_ONLY=G0,G1,G2,G3`, as the brief asks, not the full gate. Result
  below.
- Follow-up build: the whole-call test uses 4 KiB against 64 KiB, not the old 64 KiB
  against 1 MiB (see "Follow-up build"). The book's mention of the old test name was not
  updated (`book/` is not the builder's to edit).

## Limits

- First build: the two tests proved linear work in the two counted paths only. The
  follow-up build widens the counters to the whole call; its limits are listed under
  "Follow-up build", "Limits of the whole-call count".
- `ShareFits` is not counted by any of the three tests (the old timing loops called it).
  It returns at once for input over `ShareMaxLen` (4,096 bytes), so it does a bounded amount
  of work for every input, and at 64 KiB and 1 MiB the old timing test measured only that
  length check too.

## Commands

```
export PATH=$PATH:/usr/local/go/bin
go test -count=1 -v -run 'GrowLinearly' ./internal/pgwire/
go test -count=200 -run 'GrowLinearly' ./internal/pgwire/
go test -count=100 -run 'Linearly' ./internal/pgwire/
grep -n 'time\.Now\|time\.Since' internal/pgwire/classify_test.go
go test -count=1 -v ./agent_work/adversarial/c05-classifier/
GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh cr03-growth-tests
```

## Gate

This was a partial gate, not the full gate, as the brief asks (another branch's 65-minute landing gate may be
running on this machine): `GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh cr03-growth-tests` on the clean
tree at `06a1ba1` (`dirty_tree=false`, stage build). **overall=pass**, partial, so
`counts_for_landing=false`; 566 s. G0 tools/gofmt/build/vet/tools-vet/tools-test/tidy pass;
G1 detlint (22 package patterns)/importlint/boundlint/paniccheck pass; G2 comments pass
(baseline `ac91a01`); G3 unit pass (524 s, includes `agent_work/adversarial/...`), G3 race
pass (19 s). `gate/result.json` is left uncommitted, as the brief asks. The only change after the gate run is
this section of NOTES.md.
