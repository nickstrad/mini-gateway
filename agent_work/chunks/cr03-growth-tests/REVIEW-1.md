VERDICT: FAIL

## Findings

1. **MAJOR** — `internal/pgwire/lex.go:68-74`, `internal/pgwire/lex.go:175-179`, `internal/pgwire/lex.go:243-273`, `internal/pgwire/classify_test.go:581-583`: `TestClassifierWorkGrowsLinearly` has zero scaling coverage on two of its nine listed shapes (`"'x'"` and `"-- c\n"`), so quadratic defects in `Lexer.quoted()` and `Lexer.scan()` line comments — paths the old `TestClassifierTimeGrowsLinearly` covered with those shapes — pass silently.
   - **How I know:**
     - Measured counts on the unmutated tip (`65ddfff`):
       - `"'x'"`: 4 KiB `{opRunBytes:0 lookahead:0 keywordChecks:15 tokenVisits:24 literalBytes:0}` → 64 KiB `{opRunBytes:0 lookahead:0 keywordChecks:15 tokenVisits:24 literalBytes:0}`. Every counter is constant or zero.
       - `"-- c\n"`: 4 KiB `{opRunBytes:0 lookahead:0 keywordChecks:0 tokenVisits:0 literalBytes:0}` → 64 KiB `{opRunBytes:0 lookahead:0 keywordChecks:0 tokenVisits:0 literalBytes:0}`. Every counter is zero on both sizes, and `clsHasKind(smallUnits, TokenKind.Significant)` returns `false`, so the counter-moved guard at `classify_test.go:549` does not run either (`"-- c\n"` is a complete no-op checking `0 <= 17*0` five times). (Similarly, `"-"` in `TestLexOperatorRunsGrowLinearly` has constant/zero counts `{0, 0, 17, 26, 6}` on both sizes.)
     - **Mutation M5** (in `Lexer.quoted` at `lex.go:253`, scan `start..l.pos` on every quote byte `c == q` — a classic quote-escape scan and the exact class of defect `CHANGE-REQUEST.md` Addition line 88 cites): `TestClassifierCorpus` and all `TestLex*` pass (semantics unchanged); `TestClassifierWorkGrowsLinearly` slows 145× (0.03 s → 4.37 s) yet **PASSES** all three `Linearly` tests.
     - **Mutation M6** (in `Lexer.scan` at `lex.go:175`, scan `0..l.pos` on every `--` line comment): `TestClassifierCorpus` and all `TestLex*` pass; `TestClassifierWorkGrowsLinearly` slows 30× (0.03 s → 0.89 s) yet **PASSES** all three `Linearly` tests with all five counters `0 → 0` on `"-- c\n"`.
     - This falsifies `NOTES.md` Claim 6 ("The whole classification of an input does linear work: for each of … `'x'` … `-- c\n` …") and leaves two paths covered by the old timing test's listed shapes unable to fail.
   - **What a fix must achieve:** Either (a) under CR-3 Addition item 1, count lexer byte/step work across all lexer scan paths (e.g., via an inlined per-call byte-read/step helper or per-loop step counter on `Lexer` used in `scan`, `quoted`, `blockComment`, `number`, `dollar`, `scanOpRun`, `lexContinuation`) so every non-empty shape — including `"'x'"`, `"-- c\n"`, and `"-"` — moves a counter that scales linearly with input length, require that counter to be non-zero on every non-empty shape, and verify hot-path cost via `-gcflags=-S`/`-gcflags=-m`; or (b) if per-byte lexer counting distorts the code or has measurable hot-path cost, adopt CR-3 Addition item 2 for the uncounted lexer paths: keep an opt-in timing test behind an explicit environment switch (off by default) and narrow Claim 6 and `NOTES.md` Limits to state plainly that string-body (`'x'`) and line-comment (`-- c\n`, `-`) lexing are not guarded by the default counted tests.

2. **MAJOR** — `internal/pgwire/lex.go:260-265`, `internal/pgwire/lex.go:384-414`, `internal/pgwire/classify.go:128-187`, `internal/pgwire/classify_test.go:523-532`: Lexer counters record endpoint differences (`r.end - r.start`, `next - l.pos`) rather than loop work inside `scanOpRun` and `lexContinuation`, and `clsMsg.lex()` is completely uncounted, allowing quadratic regressions inside `scanOpRun()`, `lexContinuation()`, and `clsMsg.lex()` — all paths covered by the old timing tests' listed shapes — to pass silently.
   - **How I know:**
     - **Mutation M8** (`Lexer.scanOpRun` at `lex.go:404`: make the `lexKeepsTrailingSign` scan O(run²)): `TestLexOperatorRunsGrowLinearly` burns **228.94 s** (2,081× slower than 0.11 s unmutated), yet `l.opRunBytes += l.run.end - l.run.start` still records `4096 → 65536` and all three `Linearly` tests **PASS**.
     - **Mutation M7** (`lexContinuation` at `lex.go:293`: scan forward past the first matching `'` to `len(src)` before returning `(found, true)`): `TestClassifierCorpus` and `TestLex*` pass; `TestClassifierWorkGrowsLinearly` takes **1.88 s** (63× slower than 0.03 s) on `"'a'\n"`, yet `l.lookahead += next - l.pos` still records `2047 → 32767` and all three `Linearly` tests **PASS**.
     - **Mutation M9** (`clsMsg.lex` at `classify.go:183`: scan `m.toks` on each appended token): `clsRunCounted` discards both `Lexer` instances created by `m.lex()` (`clsMeasureWork` runs a separate `LexerFor(q)` on the side) and increments nothing in `m.lex()`. `TestLexOperatorRunsGrowLinearly` slows 88× (0.11 s → 9.71 s) and `TestClassifierWorkGrowsLinearly` slows 207× (0.03 s → 6.23 s), yet all three `Linearly` tests **PASS**.
   - **What a fix must achieve:** Count actual loop iterations/reads inside `scanOpRun` and `lexContinuation` (which falls out naturally if Finding 1 routes lexer byte access through a counted step/read helper), propagate `m.lex()`'s lexer counts into `clsCounts` so `clsRunCounted` measures the lexer passes it actually performs, and count token-slice work in `clsMsg.lex()` (or, under CR-3 Addition item 2, document these exact blind spots as limits backed by the opt-in timing switch).

3. **MINOR** — `internal/pgwire/classify.go:628-632`: `setConfigLocal` calls `clsContinued(m.src[lit.Start:lit.End])` and indexes `m.toks[open+1]` directly before incrementing `m.counts.literalBytes` and without calling `m.isString(open+1)`.
   - **How I know:** Reading `classify.go:628-632`: on a continued string literal in the first argument (e.g. `set_config('a'\n'b', 'x', true)`), `clsContinued` scans the literal bytes and line 630 returns `false`, skipping line 632 (`m.counts.literalBytes += lit.End - lit.Start`).
   - **What a fix must achieve:** Check `m.isString(open+1)` and add `lit.End - lit.Start` to `m.counts.literalBytes` before calling `clsContinued` whenever the token is a single-quoted string.

4. **MINOR** — `internal/pgwire/lex.go:355`: `l.lookahead += end - l.pos` in `Lexer.dollar()` is never exercised by any of the three `Linearly` tests.
   - **How I know:** None of the 24 shapes in `TestLexOperatorRunsGrowLinearly`, `TestClassifierSpanKeywordChecksGrowLinearly`, or `TestClassifierWorkGrowsLinearly` contains `$`. **Mutation M11** (replacing `dollar()`'s tag loop with `for end < len(l.src) { end++ }`) passes `go test -count=1 -run 'Linearly' ./internal/pgwire/` with `lookahead: 0 → 0` on every non-`'a'\n` shape.
   - **What a fix must achieve:** Either add a `$`-bearing shape (e.g. `"$a "` or `"$a$x$a$ "`) to `TestClassifierWorkGrowsLinearly` so the `dollar()` lookahead counter moves and can fail, or remove the untested counter line from `dollar()`.

5. **MINOR** — `agent_work/adversarial/c05-classifier-neon/bounds_and_courtesy_test.go:34`: the orchestrator's `t.Skip` message ends with `See agent_work/chunks/c05-classifier-neon/ADVERSARIAL.md.`, which does not exist.
   - **How I know:** `ls agent_work/chunks/c05-classifier-neon/` fails (`No such file or directory`). Both chunk 5 adversarial harnesses are recorded in `agent_work/chunks/c05-classifier/ADVERSARIAL.md`, which every other `t.Skip` in `agent_work/adversarial/c05-classifier-neon/` cites.
   - **What a fix must achieve:** Change the trailing reference on line 34 to `agent_work/chunks/c05-classifier/ADVERSARIAL.md`.

## Mutation checks

All mutations were applied in isolated copies of tip `65ddfff6dc289fa4444a206f670f8bdff8b4b97e` under `/tmp/zgw-cr03-growth-tests-review1/` and tested with `go test -count=1 -run 'Linearly' ./internal/pgwire/`.

| ID | Path & planted defect | Caught? | Which test / counter / numbers |
| --- | --- | --- | --- |
| M1a | Chunk 5 R2 (`lex.go:384`): revert `a6341d8` (old `operator()` + `lexOperatorLen`, counting `l.opRunBytes += l.pos - start`) | **CAUGHT** | `TestLexOperatorRunsGrowLinearly` (250.78 s): `"+"`, `"-+"`, `"+-"` each `2,147,516,416` vs `8,390,656` `opRunBytes` (255.9× > 17×) |
| M2 | Chunk 5 R1 (`classify.go:462,465`): revert `205a75f` (per-token `spanHas` in `tokenReject`) | **CAUGHT** | `TestClassifierSpanKeywordChecksGrowLinearly` (1.27 s): all 6 shapes fail on `keywordChecks` (250.2×–252.9×) and `tokenVisits` (240.1×–245.5×) |
| M3 | Classifier (`classify.go:238`): quadratic `split()` rescanning `0..i` via `m.isPunct(j, ';')` | **CAUGHT** | `TestClassifierWorkGrowsLinearly` (8.18 s): `";"` `2,147,909,632` vs `8,415,232` `tokenVisits` (255.2×); `"SELECT abs(t.a) FROM t; "` `37,676,730` vs `170,170` (221.4×) |
| M10 | Classifier (`classify.go:194`): quadratic `matchParens()` forward scan via `m.isPunct` (reproducing builder F1) | **CAUGHT** | `TestClassifierWorkGrowsLinearly` (41.51 s): `"("` `4,295,491,595` vs `16,809,995` `tokenVisits` (255.5×); `"set_config(1,2,"` `162,443,805` vs `645,661` (251.6×) |
| M12 | Classifier (`classify.go:455`): `tokenReject` rescanning `s.lo..i` via `m.isName`/`m.name` | **CAUGHT** | `TestClassifierSpanKeywordChecksGrowLinearly` (all 6 shapes, `tokenVisits` + `literalBytes` ~235×–256×) and `TestClassifierWorkGrowsLinearly` (`"a "` 252.4× `tokenVisits` / 255.9× `literalBytes`; `"U&\"a\" "` 253.3× `keywordChecks` / 251.6× `tokenVisits` / 256.1× `literalBytes`) |
| M5 | Lexer (`lex.go:253`): `quoted()` scans `start..l.pos` on every quote `c == q` (`"'x'"` and `"'a'\n"`) | **MISSED (MAJOR, Finding 1)** | All 3 tests **PASS** (`TestClassifierWorkGrowsLinearly` took 4.37 s vs 0.03 s; `TestClassifierCorpus` + `TestLex*` pass). `"'x'"` counts stay constant `{0,0,15,24,0}` |
| M6 | Lexer (`lex.go:175`): `--` line comment scans `0..l.pos` on each comment (`"-- c\n"`) | **MISSED (MAJOR, Finding 1)** | All 3 tests **PASS** (`TestClassifierWorkGrowsLinearly` took 0.89 s vs 0.03 s; `TestClassifierCorpus` + `TestLex*` pass). `"-- c\n"` counts are `{0,0,0,0,0}` on both sizes |
| M7 | Lexer (`lex.go:293`): `lexContinuation()` scans past first matching `'` to `len(src)` before returning `(found, true)` | **MISSED (MAJOR, Finding 2)** | All 3 tests **PASS** (`TestClassifierWorkGrowsLinearly` took 1.88 s vs 0.03 s; `TestClassifierCorpus` + `TestLex*` pass). `"'a'\n"` `lookahead` stays `2,047 → 32,767` |
| M8 | Lexer (`lex.go:404`): `scanOpRun()` quadratic nested loop over `r.start..r.end-1` | **MISSED (MAJOR, Finding 2)** | All 3 tests **PASS** (`TestLexOperatorRunsGrowLinearly` took **228.94 s** vs 0.11 s). `opRunBytes` stays `4,096 → 65,536` |
| M9 | Classifier (`classify.go:183`): `clsMsg.lex()` scans `m.toks` on every append | **MISSED (MAJOR, Finding 2)** | All 3 tests **PASS** (`TestLexOperatorRunsGrowLinearly` took 9.71 s, `TestClassifierWorkGrowsLinearly` took 6.23 s) |
| M11 | Lexer (`lex.go:350`): `dollar()` tag scan unconditionally scans to `len(l.src)` | **MISSED (MINOR, Finding 4)** | All 3 tests **PASS** (0.59 s): no shape in any `Linearly` test contains `$` |

## Counting seam & hot-path cost

- **Exported API, behaviour, and metrics:** Unchanged. `ClassifierSnapshot`, `Unit`, `Token`, `Lexer`, `Classify`, `ClassifyQuery`, `LexAll`, and `ShareFits` signatures and observable outputs are identical (`git diff ac91a01..65ddfff -- internal/pgwire/`). All counters are unexported `int` fields on per-call values (`Lexer.opRunBytes`, `Lexer.lookahead`, `clsMsg.counts`).
- **Concurrency / shared state:** No global or shared counters. Every counter lives on a stack-local `Lexer` or `clsMsg`. Verified with 8 concurrent goroutines under `go test -race` (pass) and `-gcflags=-m` (`l does not escape`, `m does not escape`).
- **Allocations:** Identical between base `ac91a01` and tip `65ddfff` across all five tested inputs (`testing.AllocsPerRun`: `ClassifyQuery` 3.0–4.0, `LexAll` 1.0–5.0, `ShareFits` 0.0 on both base and tip).
- **Compiled code (`-gcflags=-m` and `-gcflags=-S`, `ac91a01` vs `65ddfff`):**
  - Inlining is preserved on every accessor (`(*clsMsg).isPunct`, `word`, `isName`, `isString`), and the two-line wrapper `clsRun` is inlined into both `(*Classifier).Classify` and `ClassifyQuery`.
  - Leaf methods stay `nosplit` with minimal size deltas: `word` 36→43 B, `isPunct` 74→79 B, `isName` 55→62 B, `isString` 53→57 B, `is` 136→154 B, `operator` 147→165 B (adds `SUBQ AX, BX` + `ADDQ BX, 64(DX)` only on the `scanOpRun` slow branch), `quoted` 325→335 B, `dollar` 497→508 B, `lexContinuation` 184→186 B; `clsRunCounted` frame locals grow by 24 B (`0x120` → `0x138`, the three `int` fields of `clsCounts`).

## Wall-clock independence

- `grep -n 'time\.Now\|time\.Since' internal/pgwire/classify_test.go` exits 1 (no matches; `time` is not imported).
- Under 4-core CPU saturation (`yes > /dev/null` × 4; load average 11.98 → 11.95 on 4 cores), `pgwire.test -test.count=100 -test.v -test.run Linearly` exited 0 with **300 PASS, 0 FAIL**, producing exactly 24 distinct measurement lines each repeated 100 times with bit-identical counts across every run and matching every table value in `NOTES.md`.

## Ratification of skipped chunk 5 adversarial wall-clock tests

**RATIFY** the skip of all three wall-clock attack tests in commit `65ddfff6dc289fa4444a206f670f8bdff8b4b97e` (`agent_work/chunks/c05-classifier/ADVERSARIAL.md`), **conditional on closing Finding 2 in this CR's repair round** (and fixing the minor doc-path typo in Finding 5):

- **`TestAdv_5_OperatorSuffixRescanning`** (`agent_work/adversarial/c05-classifier/attacks_test.go:291`): timed `ClassifyQuery("SELECT " + strings.Repeat("+", n))` for `n = 512` vs `8192` against a 96× wall-clock ratio (~150–270 µs small run, inherently flaky under load in G3). `TestLexOperatorRunsGrowLinearly` holds the exact chunk 5 R2 defect (`a6341d8`, suffix rescanning across operator cuts) deterministically on `"+"` (plus 8 more shapes; M1a caught at 255.9× vs 17×). However, because the old test timed the full `ClassifyQuery` call, it would also have caught an inner-loop regression inside `scanOpRun()` (M8, 228.94 s) or `clsMsg.lex()` (M9, 9.71 s) on that same input — Finding 2 must close those gaps so the superseding claim holds in full.
- **`TestAdv_5_WholeSpanRescanning`** (`agent_work/adversarial/c05-classifier/attacks_test.go:295`): timed `ClassifyQuery("SELECT " + strings.Repeat("pg_settings ", n))` for `n = 512` vs `8192` against 96×. `TestClassifierSpanKeywordChecksGrowLinearly` holds the exact chunk 5 R1 defect (`205a75f`, per-token `spanHas`) on `"pg_settings "` (M2 caught at 251.6× `keywordChecks` and 245.0× `tokenVisits`, plus M12 caught on `tokenVisits`/`literalBytes`), subject only to the same `clsMsg.lex()` blind spot tracked in Finding 2.
- **`TestAdv_5_QuadraticSpanScanDoS`** (`agent_work/adversarial/c05-classifier-neon/bounds_and_courtesy_test.go:33`): timed `ClassifyQuery("SELECT " + strings.Repeat(word, size/len(word)) + "1")` for 16 KiB vs 256 KiB against 96× across `"pg_settings "`, `"client_encoding "`, and `"standard_conforming_strings "`. `TestClassifierSpanKeywordChecksGrowLinearly` holds R1 on all three shapes (M2 caught at 250.4×–252.3× `keywordChecks`, M12 caught on all three), subject to Finding 2 (`clsMsg.lex()`) and Finding 5 (nonexistent `agent_work/chunks/c05-classifier-neon/ADVERSARIAL.md` in line 34's skip message).

All three skipped tests read `time.Now()` with a 96× wall-clock ratio and cannot remain active in G3 without recreating CR-3's exact failure mode. Keeping them skipped (and still runnable by hand by removing the skip line) is the right call once Finding 2 closes the inner-loop blind spots.

## Release It! checklist

Per the review brief, CR-3 is a test/counting-seam change request rather than a component chunk, so the *Release It!* anti-patterns do not add new runtime failure modes here:

- **Integration points:** Not applicable here because CR-3 adds no network, socket, or external I/O paths (only unexported per-call counters in `lex.go`/`classify.go` and tests).
- **Blocked threads:** Not applicable here because CR-3 introduces no goroutines, locks, channels, or database connections.
- **Slow responses:** Not applicable here because CR-3 adds no connection handling or request lifecycle paths; linear-work guarantees are the subject of Findings 1–2.
- **Cascading failures:** Not applicable here because CR-3 touches no backend/pool/health paths.
- **Chain reactions:** Not applicable here because CR-3 touches no multi-instance or admission/shedding paths.
- **Dogpile:** Not applicable here because CR-3 touches no dialing or fault-recovery paths.
- **Self-denial attacks:** Not applicable here because CR-3 introduces no periodic, retry, or batch operations.
- **Force multiplier:** Not applicable here because CR-3 introduces no feedback or control loops.
- **Unbalanced capacities:** Not applicable here because CR-3 introduces no queues or producer/consumer buffers.
- **Scaling effects:** Not applicable here because CR-3 adds no exported metrics, labels, or per-instance state.
- **Users:** Not applicable here because CR-3 adds no per-client or per-tenant state; `clsMsg` stack size grows by 24 bytes per call.
- **Unbounded result sets:** Not applicable here because CR-3 does not alter reply streaming or allocation bounds (`TestClassifierMemoryIsBounded` still passes).
- **Compounds:** Not applicable here because none of the above mechanisms are added or modified by CR-3.

## Observability

- Per the review brief, the chunk observability gate (PLAN.md 7.6) does not apply to this test/counting-seam CR.
- Checked regardless: `Classifier.Snapshot()` and `ClassifierSnapshot.Check()` are untouched; no exported counter, metric, or CSV schema row changed; `TestClassifierShareKeyAndScope`, `TestClassifierInvariantsOverMutants`, and `TestAdv_11_FlushedCSVMatchesSnapshotsAndSQLAssertionsPass` all pass.
- Fuzz targets (PLAN.md 6.6): inspected `FuzzLex` (`internal/pgwire/lex_test.go:154`), `FuzzClassify` (`internal/pgwire/classify_test.go:499`), and `FuzzSafeShape` (`internal/pgwire/share_test.go:659`) — none runs the simulator or derives a seed from a hash of its input.

## What I ran

- `gofmt -l .`: clean (0 files).
- `go build ./... && go vet ./...`: exit 0.
- `go test -count=1 -v -run 'Linearly' ./internal/pgwire/`: exit 0 (3 PASS: `TestLexOperatorRunsGrowLinearly` 0.11 s, `TestClassifierSpanKeywordChecksGrowLinearly` 0.02 s, `TestClassifierWorkGrowsLinearly` 0.03 s; all 24 shape counts match `NOTES.md`).
- `go test -count=1 ./internal/pgwire/`: exit 0.
- `go test -count=1 -v ./agent_work/adversarial/c05-classifier/ ./agent_work/adversarial/c05-classifier-neon/`: exit 0 (`c05-classifier-neon` 65.69 s; 3 newly settled tests skipped cleanly, all active attacks pass).
- `grep -n 'time\.Now\|time\.Since' internal/pgwire/classify_test.go`: exit 1 (0 matches).
- Loaded run (`yes > /dev/null` × 4, load avg 11.98 → 11.95): `pgwire.test -test.count=100 -test.v -test.run Linearly`: exit 0, 300 PASS, 0 FAIL, 24 distinct measurement lines × 100 exact repeats.
- Allocation & race check (`base` `ac91a01` vs `tip` `65ddfff`, 5 inputs + 8 concurrent goroutines under `-race`): exit 0 on both; identical `AllocsPerRun` on every input.
- Compiler & assembly comparison (`go build -gcflags=-m` and `-gcflags=-S`, `ac91a01` vs `65ddfff`): inlining preserved on all accessors and `clsRun`; +2 to +18 B per instrumented method; zero new escapes.
- 11 scratch mutations (M1a, M2, M3, M5, M6, M7, M8, M9, M10, M11, M12) in `/tmp/zgw-cr03-growth-tests-review1/`: results in `## Mutation checks`.
- Partial gate: `GATE_OUT=/tmp/zgw-cr03-growth-tests-review1/gate GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh cr03-growth-tests`: exit 0, `overall=pass`, `dirty_tree=false`, `counts_for_landing=false` (G0 tools 2 s, gofmt 1 s, build 2 s, vet 1 s, tools-vet 1 s, tools-test 19 s, tidy 1 s; G1 detlint 8 s / 22 patterns, importlint 0 s, boundlint 11 s, paniccheck 0 s; G2 comments 1 s vs `ac91a01`; G3 unit 641 s, race 62 s).
- `git status -sb`: clean (no repository files modified).
