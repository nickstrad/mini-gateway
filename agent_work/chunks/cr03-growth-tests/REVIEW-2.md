VERDICT: NITS

## Review 1 findings — status

- **Finding 1 (MAJOR — `'x'` and `-- c\n` moved no counter; M5, M6 passed):** **FIXED** (`1c9bca3`, `702c187`). Under `-tags clscount`, every lexer loop counts its steps; `TestCountedWorkGrowsLinearly` requires `lexer reads` to grow 15×–17× and `token steps` > 0 on all 26 shapes; M5 and M6 fail `scripts/counted-growth.sh` (counted) or `TestLexLoopsCountTheirSteps` (raw).
- **Finding 2 (MAJOR — `scanOpRun`/`lexContinuation` counted endpoints; `clsMsg.lex` uncounted; side lexer run; M7, M8, M9 passed):** **FIXED** (`1c9bca3`, `702c187`). Loop steps are counted inside `scanOpRun`, `lexContinuation`, `lexHasPrefix`, and both loops of `(*clsMsg).lex`; `countPass(&l)` aggregates `ClassifyQuery`'s own two lexer passes; M7, M8, and M9 fail `scripts/counted-growth.sh` (counted) or the AST guards (raw).
- **Finding 3 (MINOR — `setConfigLocal` skipped `literalBytes` on continued literals):** **FIXED** (`95f9996`). `internal/pgwire/classify.go:655-660` now calls `m.isString(open + 1)` and adds `lit.End - lit.Start` to `m.counts.literalBytes` before `clsContinued`, held by `TestClassifierCountsTheSetConfigLiteralItScans`.
- **Finding 4 (MINOR — `dollar()` lookahead counter never exercised; M11 passed):** **FIXED** (`1c9bca3`). Shapes `"$a$x$a$ "` and `"$a "` added to `clsWholeCallShapes`; M11 now fails both `TestClassifierWorkGrowsLinearly` (default) and `TestCountedWorkGrowsLinearly` (`scripts/counted-growth.sh`).
- **Finding 5 (MINOR — broken path `c05-classifier-neon/ADVERSARIAL.md` in skip message):** **FIXED** by orchestrator (`e6e1306`). `agent_work/adversarial/c05-classifier-neon/bounds_and_courtesy_test.go:34` now points to `agent_work/chunks/c05-classifier/ADVERSARIAL.md`.

## Findings

1. **MINOR** — `internal/pgwire/count_guard_test.go:27-47`, `internal/pgwire/count_guard_test.go:80-104`, `internal/pgwire/count_guard_test.go:152-170`, `internal/pgwire/count_guard_test.go:181-213`: The AST loop guards (`TestLexLoopsCountTheirSteps`, `TestClassifierLexLoopsCountTheirSteps`) have four concrete escapes where an uncounted loop in `lex.go` or `(*clsMsg).lex` passes both the default suite and `scripts/counted-growth.sh` without using `goto`, recursion, or function values:
   - **Transitive helper call in `lex.go` (`count_guard_test.go:195,204`):** `cntCallProblems` rejects a direct call to a non-scanner function only when `cntHasLoop(callee)` is true, and `cntLexProblems` never runs `cntCallProblems` on non-scanner functions. A 1-line non-looping wrapper `lexWrapRescan` in `lex.go` calling a looping helper (a custom loop, `lexUnquote`, or `bytes.IndexByte`) passes silently (mutation N6 below).
   - **No call check in `TestClassifierLexLoopsCountTheirSteps` (`count_guard_test.go:40`):** The test calls only `cntLoopProblems(fn)` and never checks callees of `(*clsMsg).lex()`. Extracting M9's O(n²) token-slice scan into a helper method `m.rescanToks()` in `classify.go` passes both the AST guard and `scripts/counted-growth.sh` (mutation N7 below).
   - **Untargeted `.Bump()` acceptance (`count_guard_test.go:168-169`):** `cntIsCount` checks only `sel.Sel.Name == "Bump"` without verifying the receiver (`l.reads`, `reads`, or `m.counts.lex.steps`). A loop bumping a local `var dead clscount.Tally` passes both the guard and `scripts/counted-growth.sh` (mutation N8 below).
   - **Bare-name `decls` map collision (`count_guard_test.go:83-88,199`):** `decls[fn.Name.Name] = fn` ignores receiver type, so adding a method with the same name on another type (`func (lexDummy) scan() {}`) overwrites `(*Lexer).scan` in `decls` and disables all checks on it, letting raw uncounted M6 pass (mutation N9 below). Similarly, `case *ast.SelectorExpr` checks `decls[fun.Sel.Name]` without matching receiver type, allowing calls to unrelated methods sharing a name with `TokenKind.String`, `Token.Text`, or `lexOpRun.cut`.
   - **How I know:** Mutations N6, N7, N8, and N9 in scratch copies under `/tmp/zgw-cr03-growth-tests-review2/` each preserved exact corpus/lex output (`sem_ok=True`) while passing both `go test -count=1 -run 'Linearly|CountTheirSteps' ./internal/pgwire/` and `scripts/counted-growth.sh` (exit 0).
   - **What a fix must achieve:** Key `decls` by `(receiver, name)`; transitively inspect every function reachable from any `cntScanner` (rejecting any non-scanner transitive loop or out-of-file call); add a callee allowlist/check to `TestClassifierLexLoopsCountTheirSteps`; verify the `Bump()` receiver expression matches the expected tally (`l.reads` / `reads` / `m.counts.lex.steps`); and add canary cases to `TestLexLoopGuardRejectsUncountedWork` for a transitive wrapper and a dead tally. (Note: no existing code in `lex.go` or `(*clsMsg).lex` triggers these holes — every existing loop counts properly — so this is a defensive-guard gap rather than a broken product path.)

2. **MINOR** — `scripts/gate.sh:362-365` (and `scripts/gate-canaries.sh:41`): `g3_counted()` runs `scripts/counted-growth.sh` without unsetting the test seam `COUNTED_GO`, unlike `g5_fuzz()` (`scripts/gate.sh:226`) which explicitly runs `env -u FUZZ_GO` so the test seam cannot leak into a gate run.
   - **How I know:** Running `g3_counted` with `COUNTED_GO=/tmp/zgw-cr03-growth-tests-review2/fake_pass_go.sh` exited 0 (`NOTE: -tags clscount -run Linearly: 5 tests passed`) without invoking `go`. `grep COUNTED_GO scripts/gate.sh` returns no matches.
   - **What a fix must achieve:** Change `scripts/gate.sh:364` to `env -u COUNTED_GO scripts/counted-growth.sh` (and add `COUNTED_GO` to the `unset` line at `scripts/gate-canaries.sh:41`).

3. **MINOR** — `internal/pgwire/clscount/off.go:1-19`, `internal/pgwire/clscount/on.go:1-19`, `tools/lint-rules/det-packages.txt`: `internal/pgwire/clscount` is imported by the deterministic package `internal/pgwire` while sitting outside `det-packages.txt`, and no test or `importlint` rule locks down its zero-import purity.
   - **How I know:** Auditing all 22 patterns in `tools/lint-rules/det-packages.txt` via `go list` shows only two unlisted internal dependencies (`internal/obs/catalog -> internal/obs/csvflush` and `internal/pgwire -> internal/pgwire/clscount`). `detlint` loads unlisted imported packages via `build.Default` without linting their bodies (`tools/detlint/README-tools.txt:24`), and `tools/lint-rules/imports.txt` does not restrict stdlib imports in `internal/pgwire/clscount`. Today both files are 19 lines with zero imports and pure integer methods, so no determinism defect exists now, but a future import of `time`, `sync/atomic`, or `os` in `clscount` would pass G1 silently.
   - **What a fix must achieve:** Add a small AST test (e.g. in `internal/pgwire/count_guard_test.go` or `internal/pgwire/clscount`) parsing both `off.go` and `on.go` and asserting zero imports, no package-level `var`, and no `go`/`chan`/float constructs.

## Mutation checks

All mutations were applied in isolated `git archive HEAD` (`7c4ca977ae23ddcfd2b6ed7c189a4a08828216a0`) copies under `/tmp/zgw-cr03-growth-tests-review2/` and verified against both `go test -count=1 -run 'Linearly|CountTheirSteps' ./internal/pgwire/` (default build) and `scripts/counted-growth.sh` (tagged run), plus `TestClassifierCorpus`/`TestLexSpansCoverTheCorpus`/`TestLexTokenKinds` (`sem_ok`).

### Review 1 mutations (M5–M9, M11)

| ID | Planted defect | `sem_ok` | Default build (`Linearly\|CountTheirSteps`) | `scripts/counted-growth.sh` (`-tags clscount`) |
| --- | --- | --- | --- | --- |
| M5 (counted) | `lex.go` `quoted()`: on `c == q`, scan `start..l.pos` with `l.reads.Bump()` | true | PASS (9.26 s, no per-byte counter by design) | **FAIL** (18.68 s): `TestCountedWorkGrowsLinearly` (`"'x'"`: 1,431,677,612 vs 5,593,772 reads, 255.9×) and `TestCountedCheckRejectsWorkThatDoesNotGrowLinearly` |
| M5 (raw) | Same without `Bump()` | true | **FAIL** (10.02 s): `TestLexLoopsCountTheirSteps` (`quoted: a loop does not start by counting its step`) | PASS (9.40 s) |
| M6 (counted) | `lex.go` `scan()`: on `--`, scan `0..l.pos` with `l.reads.Bump()` | true | PASS (7.13 s) | **FAIL** (10.04 s): `TestCountedWorkGrowsLinearly` (`"-- c\n"`: 859,085,208 vs 3,361,176 reads, 255.6×) |
| M6 (raw) | Same without `Bump()` | true | **FAIL** (8.24 s): `TestLexLoopsCountTheirSteps` (`scan: a loop does not start by counting its step`) | PASS (7.04 s) |
| M7 (counted) | `lex.go` `lexContinuation()`: before returning `(i+1, true)`, scan `i..len(src)` with `reads.Bump()` | true | PASS (6.37 s) | **FAIL** (11.55 s): `TestCountedWorkGrowsLinearly` (`"'a'\n"`: 1,073,807,364 vs 4,198,404 reads, 255.8×) |
| M7 (raw) | Same without `Bump()` | true | **FAIL** (12.89 s): `TestLexLoopsCountTheirSteps` (`lexContinuation: a loop does not start by counting its step`) | PASS (19.09 s) |
| M8 (counted) | `lex.go` `scanOpRun()`: nested `j := r.start..k` inside `lastKeep` loop with `l.reads.Bump()` | true | n/a (`CountTheirSteps` PASS) | **FAIL** (150.65 s): `TestCountedWorkGrowsLinearly` (`"+"`: 4,295,426,064 vs 16,805,904 reads, 255.6×) |
| M8 (raw) | Same without `Bump()` | true | **FAIL**: `TestLexLoopsCountTheirSteps` (`scanOpRun: a loop does not start by counting its step`) | n/a |
| M9 (counted) | `classify.go` `(*clsMsg).lex()`: after append, scan `m.toks` with `m.counts.lex.steps.Bump()` | true | n/a (`CountTheirSteps` PASS) | **FAIL** (107.57 s): `TestCountedWorkGrowsLinearly` (`"+"`: 2,147,713,031 vs 8,402,951 token steps, 255.6×) |
| M9 (raw) | Same without `Bump()` | true | **FAIL**: `TestClassifierLexLoopsCountTheirSteps` (`lex: a loop does not start by counting its step`) | n/a |
| M11 | `lex.go` `dollar()`: tag loop changed to `for end < len(l.src)` (keeps `l.reads.Bump()`) | false | **FAIL** (15.22 s): `TestClassifierWorkGrowsLinearly` (`"$a$x$a$ "`: 268,468,224 vs 1,050,624 lookahead bytes, 255.5×) | **FAIL** (16.81 s): `TestCountedWorkGrowsLinearly` (`"$a$x$a$ "`: 537,083,904 vs 2,110,464 reads, 254.5×) and `TestClassifierWorkGrowsLinearly` |

### New quadratic defects (N1–N4 idiomatic paths; N5 documented limit; N6–N9 AST guard escapes)

| ID | Path & planted defect | `sem_ok` | Default build | `scripts/counted-growth.sh` |
| --- | --- | --- | --- | --- |
| N1 (counted) | `lex.go` `digits()` (exercised by `"1+"`, `"set_config(1,2,"`): scan `0..l.pos` with `l.reads.Bump()` | true | PASS (14.54 s) | **FAIL** (26.20 s): `TestCountedWorkGrowsLinearly` (`"1+"`: 2,148,204,562 vs 8,433,682 reads, 254.7×) |
| N1 (raw) | Same without `Bump()` | true | **FAIL** (8.71 s): `TestLexLoopsCountTheirSteps` (`digits`) | PASS (9.71 s) |
| N2 (counted) | `lex.go` `dollar()` body close (exercised by `"$a$x$a$ "`): scan `0..i` with `l.reads.Bump()` | true | PASS (7.50 s) | **FAIL** (8.76 s): `TestCountedWorkGrowsLinearly` (`"$a$x$a$ "`: 537,018,368 vs 2,106,368 reads, 254.9×) |
| N3 (counted) | `lex.go` `scan()` word branch (exercised by `"pg_settings "`, `"a "`): scan `0..l.pos` with `l.reads.Bump()` | true | PASS (10.69 s) | **FAIL** (34.80 s): `TestCountedWorkGrowsLinearly` (`"pg_settings "`: 358,034,100 vs 1,405,620 reads, 254.7×) |
| N4 (idiomatic) | `classify.go` `spanWords()` (exercised by `"pg_settings "`): inner `for j := s.lo; j < i; j++ { m.is(j, "never") }` on `"pg_settings"` | true | **FAIL** (6.65 s): `TestClassifierSpanKeywordChecksGrowLinearly` (14,952,236 vs 60,716 keyword checks, 246.3×) | **FAIL** (7.16 s): `TestCountedWorkGrowsLinearly` & `TestClassifierSpanKeywordChecksGrowLinearly` |
| N5 (documented limit) | `classify.go` `tokenReject()` (exercised by `"pg_settings "`): direct `m.toks[j].Kind` scan `s.lo..i` without accessor (`NOTES.md:375-378,670-672`) | true | PASS (6.51 s) | PASS (5.52 s) |
| N6 (guard escape) | `lex.go` `quoted()` -> `lexWrapRescan` -> `lexInnerRescan` (2-hop uncounted loop; Finding 1) | true | PASS (11.06 s) | PASS (15.36 s) |
| N7 (guard escape) | `classify.go` `(*clsMsg).lex()` -> `m.rescanToks()` (uncounted helper method; Finding 1) | true | PASS (11.80 s) | PASS (15.46 s) |
| N8 (guard escape) | `lex.go` `quoted()` loop bumping local `var dead clscount.Tally` (Finding 1) | true | PASS (25.31 s) | PASS (20.89 s) |
| N9 (guard escape) | `lex.go` raw M6 + `type lexDummy struct{}; func (lexDummy) scan() {}` overwriting `decls["scan"]` (Finding 1) | true | PASS (8.01 s) | PASS (8.58 s) |

## Repair design review

- **(a) Default build hot path, exported behaviour, metrics, allocations vs `main`:** **Sound.** Comparing `go build -a -gcflags=github.com/nickstrad/mini-gateway/internal/pgwire=-S` machine-code hex and relocations:
  - `HEAD` (`7c4ca97`) vs pre-repair (`e6e1306`): **only 1 function differs** across all of `internal/pgwire` (`(*clsMsg).setConfigLocal`, 300 → 307 instructions, the intentional Finding 3 fix). All 32 lexer functions (`scan`, `quoted`, `blockComment`, `number`, `digits`, `dollar`, `scanOpRun`, `lexContinuation`, `lexHasPrefix`, `operator`, `Next`, `LexAll`, `LexerFor`), `(*clsMsg).lex`, `ClassifyQuery`, `clsRun`, and `clsRunCounted` are byte-identical; every `if clscount.On { ... }` block is dead-code-eliminated and `(*clsMsg).countPass` is uncalled.
  - `HEAD` (`7c4ca97`) vs `main` base (`ac91a01`): zero per-byte lexer loops differ (`scan`, `blockComment`, `number`, `digits`, `scanOpRun`, `lexHasPrefix`, `Next` are byte-identical to `main`). The 25 differing functions carry only the first two builds' per-run/per-token always-on counters (required to remain always-on by Astra's uneditable harness).
  - Exported API (`go doc ./internal/pgwire`), `Classify`/`ClassifyQuery`/`LexAll`/`ShareFits` outputs, `Classifier.Snapshot()`, and `testing.AllocsPerRun` across 8 representative queries are identical between `ac91a01` and `7c4ca97`.
- **(b) `internal/pgwire/clscount` outside `det-packages.txt`:** **Sound exception today, with a small guard gap (Finding 3).** `detlint`'s `[buildtag]` rule unconditionally rejects `//go:build` in checked packages and type-checks both files of a tag pair together, so isolating the 19-line zero-import switch in `internal/pgwire/clscount` is clean and justified. Locking down its zero-import purity with a test closes the future-regression gap.
- **(c) `scripts/counted-growth.sh` in G3 and `scripts/ci-fast.sh`:** **Mostly sound, one hygiene gap (Finding 2).** `scripts/counted-growth.sh` fails on non-zero `go test` exit, fails on `no tests to run`, fails on any omitted required test, and fails when the two tagged tests emit `--- SKIP:` (verified empirically with a stub printing `--- SKIP:`, exit 1). `scripts/gate.sh` calls `g3_counted` unconditionally in G3 (without `-short`), and `scripts/ci-fast.sh` includes G3. However, `g3_counted()` fails to unset `COUNTED_GO` (Finding 2).
- **(d) AST guard `TestLexLoopsCountTheirSteps`:** **Catches direct uncounted loops, `lexUnquote`, `bytes.IndexByte`, and missing `scan` counts, but has four concrete escapes (Finding 1: N6–N9).** Tightening `count_guard_test.go` to check transitive callees, `(*clsMsg).lex` callees, `.Bump()` receiver identity, and `(receiver, name)` keys closes all four cleanly.
- **(e) Narrowed Claim 6 and deviations in `NOTES.md`:**
  - **Narrowed Claim 6 (`NOTES.md:621,631-636`):** **ACCEPT.** Always-on per-byte counting regressed `LexAll` by 38%–60%, justifying form (B); the full whole-call property holds under `-tags clscount` (Claim 11) in G3 and `ci-fast.sh`.
  - **Deviation 1 (`NOTES.md:640-643`, no `//go:build` on `classify_count_test.go`; runtime skip when `!clscount.On`):** **ACCEPT.** Required by `detlint`'s `[buildtag]` rule, and `scripts/counted-growth.sh` rejects `--- SKIP:` by matching `^--- PASS: $t `.
  - **Deviation 2 (`NOTES.md:644-654`, keeping the three original tests in default build alongside `TestCountedWorkGrowsLinearly`):** **ACCEPT.** Required so Astra's uneditable `TestAdv_3_*` dead-counter attacks stay green on the default build, while `scripts/counted-growth.sh` runs and requires all 5 tests under `-tags clscount`.
  - **Deviation 3 (`NOTES.md:655-659`, no new canary in `scripts/gate-canaries.sh`):** **ACCEPT.** Avoids regenerating `c00-foundation/gate/canaries.json` via a full gate run on the shared host; `scripts/counted_growth_test.go` holds the red/green behaviour of `scripts/counted-growth.sh` and its wiring in `gate.sh`/`ci-fast.sh`.
  - **Deviation 4 (`NOTES.md:660-662`, counting per loop step rather than per byte read):** **ACCEPT.** Each loop step reads a bounded constant (1–2 bytes), preserving linear vs quadratic growth separation (15×–17× vs ~256×).
- **(f) Wall-clock independence:** **PASS.** `grep -n 'time\.Now\|time\.Since\|"time"' internal/pgwire/classify_test.go internal/pgwire/classify_count_test.go internal/pgwire/count_guard_test.go` prints nothing. Running the compiled `-tags clscount` test binary with `-test.count=20 -test.v -test.run Linearly` exited 0 (100 PASS, 0 FAIL) with 52 distinct measurement lines each repeated exactly 20 times.

## Ratification of skipped chunk 5 adversarial wall-clock tests

**RATIFY** (unconditionally) the skips of all three wall-clock attack tests recorded in `agent_work/chunks/c05-classifier/ADVERSARIAL.md` (last section):

1. `TestAdv_5_OperatorSuffixRescanning` (`agent_work/adversarial/c05-classifier/attacks_test.go:291`)
2. `TestAdv_5_WholeSpanRescanning` (`agent_work/adversarial/c05-classifier/attacks_test.go:295`)
3. `TestAdv_5_QuadraticSpanScanDoS` (`agent_work/adversarial/c05-classifier-neon/bounds_and_courtesy_test.go:33`)

Both conditions from Review 1 are now satisfied: (1) `TestCountedWorkGrowsLinearly` runs in G3 `counted` and `scripts/ci-fast.sh` over `ClassifyQuery`'s own two lexer passes and deterministically catches both chunk 5 defects (M1a, M2) and the inner-loop regressions in `scanOpRun` (M8) and `(*clsMsg).lex` (M9) at any machine load; (2) the broken path in `bounds_and_courtesy_test.go:34` was fixed in `e6e1306`. All three tests remain in the tree for manual execution if desired.

## Release It! checklist

CR-3 is a change request (tests, build-tagged work counting, and gate plumbing) rather than a component chunk; the *Release It!* anti-patterns do not apply directly, checked for completeness:

- **Integration points:** Not applicable here because CR-3 adds no network, socket, or external I/O paths.
- **Blocked threads:** Not applicable here because CR-3 adds no goroutines, locks, channels, or connections.
- **Slow responses:** Not applicable here because CR-3 adds no request-handling paths; linear-work guarantees are held deterministically by the default and `-tags clscount` suites.
- **Cascading failures:** Not applicable here because CR-3 touches no backend/pool/health paths.
- **Chain reactions:** Not applicable here because CR-3 touches no multi-instance or admission/shedding paths.
- **Dogpile:** Not applicable here because CR-3 touches no dialing or recovery paths.
- **Self-denial attacks:** Not applicable here because CR-3 adds no periodic, retry, or batch loops.
- **Force multiplier:** Not applicable here because CR-3 adds no control/feedback loops.
- **Unbalanced capacities:** Not applicable here because CR-3 adds no queues or buffers.
- **Scaling effects:** Not applicable here because CR-3 adds no exported metrics, labels, or shared state (`clscount.Tally` is zero-sized in default builds and per-call in tagged builds).
- **Users:** Not applicable here because CR-3 adds no per-client/per-tenant state.
- **Unbounded result sets:** Not applicable here because CR-3 does not change allocation bounds (`TestClassifierMemoryIsBounded` passes in both default and `-tags clscount` builds).
- **Compounds:** Not applicable here because none of the above mechanisms are added or modified.

## Observability

- Per the brief, the chunk observability gate (PLAN.md 7.6) does not apply to this CR.
- Verified regardless: `Classifier.Snapshot()` and `ClassifierSnapshot.Check()` are unchanged; exported snapshot values match `ac91a01` identically; `internal/obs/clssrc` passes.
- Fuzz targets (PLAN.md 6.6): `FuzzLex` (`internal/pgwire/lex_test.go:154`), `FuzzClassify` (`internal/pgwire/classify_test.go:499`), and `FuzzSafeShape` (`internal/pgwire/share_test.go:659`) are unchanged and none runs the simulator or hashes its input for a seed.

## What I ran

- `gofmt -l .`: clean (0 files).
- `go build ./... && go vet ./...`: exit 0.
- `grep -n 'time\.Now\|time\.Since\|"time"' internal/pgwire/classify_test.go internal/pgwire/classify_count_test.go internal/pgwire/count_guard_test.go`: exit 1 (0 matches).
- `go test -count=1 -v -run 'Linearly|CountTheirSteps|UncountedWork|SetConfigLiteral' ./internal/pgwire/`: exit 0 (3 growth tests PASS, 2 tagged tests SKIP as expected in default build, 3 guard tests PASS, 1 `setConfig` regression test PASS).
- `scripts/counted-growth.sh`: exit 0 (`NOTE: -tags clscount -run Linearly: 5 tests passed`, 0.269 s).
- `go test -count=1 -v -run Counted ./scripts/`: exit 0 (5 PASS, 0.299 s).
- `go test -tags clscount -count=1 ./internal/pgwire/`: exit 0 (7.925 s).
- Compiled tagged determinism check (`pgwire_counted.test -test.count=20 -test.v -test.run Linearly`): exit 0, 100 PASS, 0 FAIL, 52 distinct measurement lines × 20 exact repeats.
- Assembly comparison (`go build -a -gcflags=github.com/nickstrad/mini-gateway/internal/pgwire=-S`, `ac91a01` vs `e6e1306` vs `7c4ca97`): 1 differing function vs `e6e1306` (`setConfigLocal`), 0 differing per-byte lexer loops vs `ac91a01`.
- Exported API, behaviour, snapshot, and `testing.AllocsPerRun` equivalence (`ac91a01` vs `7c4ca97`, 8 inputs): bit-identical outputs, snapshot, allocations, and `go doc ./internal/pgwire`.
- `scripts/counted-growth.sh` edge cases: stub emitting `--- SKIP:` for tagged tests -> exit 1 (rejected); `g3_counted` with `COUNTED_GO` set -> exit 0 (Finding 2).
- `det-packages.txt` import audit via `go list`: 2 unlisted repo imports (`internal/obs/csvflush`, `internal/pgwire/clscount`).
- 19 scratch mutation runs (M5–M9 counted & raw, M11, N1–N9) in `/tmp/zgw-cr03-growth-tests-review2/`: results in `## Mutation checks`.
- Partial gate (once): `GATE_OUT=/tmp/zgw-cr03-growth-tests-review2/gate GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh cr03-growth-tests`: exit 0, `overall=pass`, `dirty_tree=false`, `counts_for_landing=false` (G0 tools 4 s, gofmt 1 s, build 4 s, vet 1 s, tools-vet 1 s, tools-test 7 s, tidy 1 s; G1 detlint 6 s / 22 patterns, importlint 0 s, boundlint 9 s, paniccheck 1 s; G2 comments 1 s vs `ac91a01`; G3 unit 374 s including `cr03-growth-tests` 100.1 s, `c05-classifier` 11.6 s, `c05-classifier-neon` 56.4 s; G3 counted 2 s / 5 tests; G3 race 28 s / 3 patterns).
- `git status -s`: clean (no repository files modified).
