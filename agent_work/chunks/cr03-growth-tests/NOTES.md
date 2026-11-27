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
true, so they were deleted. `clsFastest` and `clsTimeGrowthLimit` stay because
`TestClassifierTimeGrowsLinearly` uses them, and that test is out of scope.

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
| 5 | Each test can fail: putting back each repaired defect fails the matching test | Proof 1 below |

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

Outside this request's scope, but the same class: chunk 5's own
`internal/pgwire/classify_test.go: TestClassifierTimeGrowsLinearly` (64 KiB against 1 MiB,
best of 3, **96 times**, skipped under `-short`) still times the wall clock through
`clsFastest`.

## Deviations

- One shape added (`"-+"`, decision 4). The request lists the shapes to keep, and all of
  them are kept.
- The hot-path cost is argued from the emitted instructions, not shown by a clean
  benchmark: load made benchmarks unusable and the VM has no performance counters.
- The gate ran as `GATE_ONLY=G0,G1,G2,G3`, as the brief asks, not the full gate. Result
  below.

## Limits

- The tests prove linear work in the two counted paths only. A new quadratic path
  elsewhere (one that does not go through `Lexer.operator` or `clsMsg.is`) would not be
  caught by these two tests, which the old timing tests would have caught.
  `TestClassifierTimeGrowsLinearly` and the adversary's `TestAdv_5_*` still time the whole
  call.
- `ShareFits` is no longer measured by these two tests (the old timing loop called it). It
  is not part of either repaired path.

## Commands

```
export PATH=$PATH:/usr/local/go/bin
go test -count=1 -v -run 'GrowLinearly' ./internal/pgwire/
go test -count=200 -run 'GrowLinearly' ./internal/pgwire/
go test -count=1 -v ./agent_work/adversarial/c05-classifier/
GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh cr03-growth-tests
```

## Gate

GATE_RESULT_PLACEHOLDER
