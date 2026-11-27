# Change request CR-3 — the classifier's linear-growth tests must not depend on the wall clock

Filed by the orchestrator, 2026-11-27. Small; tests only if possible.

## The problem

`internal/pgwire/classify_test.go` has two tests added by chunk 5's repair round,
`TestLexOperatorRunsGrowLinearly` and `TestClassifierSpanKeywordChecksGrowLinearly`. Each
times `ClassifyQuery`, `ShareFits` and `LexAll` on a 4 KiB and a 64 KiB input (best of three
runs, `time.Now`) and fails when 16 times the input takes more than 96 times as long
(`clsTimeGrowthLimit`). The code they guard is linear. The tests are not deterministic: on a
loaded machine the 4 KiB run takes about 100 µs and one scheduling delay in the 64 KiB run
moves the ratio past 96.

Evidence, both on 2026-11-27 with four agents building on vm-1 (load average 13 to 18):

- Chunk 7's build-stage gate: ratios 98.6 and 187; the same tree passed when G3 was re-run.
- CR-2's landing gate (a 65-minute run, lost): `"+ "` ratio 127; `"pg_settings "` 140,
  `"\"pg_settings\" "` 146, `"SELECT pg_settings "` 98. Log:
  `/root/Software/mini-gateway-work/cr02n/agent_work/chunks/cr02-fuzz-deadline-retry/gate/G3-unit.log`
  (may be gone; the numbers above are from it).

This project's rule is that a test's verdict does not depend on time or load. A gate that
fails on machine load costs an hour per occurrence and teaches people to re-run red gates.

## The request

Make the two properties — "operator runs lex in linear work" and "repeated setting names
classify in linear work, with no rescan of earlier spans" — hold their verdict under any
machine load, and keep them able to fail:

1. Preferred: measure WORK, not time. Count the steps the lexer and the classifier take (for
   example bytes examined, or loop iterations in the two paths the repair made linear) and
   assert the count for a 64 KiB input is at most a fixed small multiple of the count for
   4 KiB (16 times, with a stated constant slack), for every shape the two tests list today.
   If a counter is needed in product code it must cost nothing measurable on the hot path,
   must not change any exported behaviour or any metric, and is justified in NOTES.md; a
   test-only seam is better if one can be had.
2. If counting work is not possible without distorting the code, say why in NOTES.md and
   make the timing form robust instead (larger sizes, many repetitions with the minimum
   taken, a ratio between two growth steps rather than one) — and show by measurement under
   load that it holds (see "Proof").
3. Either way no test in the default run may read the wall clock to reach a pass/fail verdict
   for these two properties unless item 2 applies.

## Proof (the builder shows each; the reviewer repeats them)

- Both quadratic defects that chunk 5's repair removed are put back in a scratch copy, one
  at a time (the commits are in `git log --oneline -- internal/pgwire/lex.go
  internal/pgwire/classify.go`, repair round of chunk 5: operator-run lexing and the span
  keyword check): each makes the matching test fail.
- `go test -count=200 -run 'GrowLinearly' ./internal/pgwire/` passes while the machine is
  loaded (run `stress`-like load yourself: for example eight `go test -count=1 ./internal/sim/...`
  loops in the background, or `yes > /dev/null` on every core), and the result of each
  run is identical if the test counts work.
- Chunk 5's adversarial tests that cover the same two paths
  (`agent_work/adversarial/c05-classifier/`) still pass, unchanged. If any of them times the
  wall clock in the default run with a tight bound, list it in NOTES.md for the orchestrator
  (do not edit the adversary's tests).
- Nothing else changes: `git diff --stat` shows `internal/pgwire/` test files, at most a
  minimal seam in `lex.go`/`classify.go`, and this chunk's notes.

## Not in scope

The fuzz deadline (CR-2), the tier R timing margins, any other test.

## Addition, 2026-11-27 (after the first build) — the third wall-clock test

While the first build ran, CR-2's second landing gate failed again, and this time a third
test of the same kind failed with the other two: `TestClassifierTimeGrowsLinearly`
(`internal/pgwire/classify_test.go`; shapes `"a "`, `"'x'"`, `"U&\"a\" "`, `"'a'\n"`; ratios
158 to 220 against the limit of 96, at load 15). It is in scope from here on. It runs in the
gate (the gate does not pass `-short`), so it must not reach its verdict from the clock:

1. Preferred: hold the general property — the whole classification of an input does work
   linear in its length, for every shape the test lists — by counting work at a level that
   covers the whole call, not only the two paths of the first build (for example every byte
   the lexer advances over or looks back at, and every token the classifier visits), with
   the same 16-times-input, at-most-17-times-work form. The counters of the first build may
   be widened or joined for this; the cost rule is unchanged (nothing measurable on the hot
   path, no exported behaviour, no metric).
2. If a whole-call count cannot be had without distorting the code, keep the test's timing
   form but take it out of every run that gives a verdict for the gate (an explicit
   environment switch, off by default), say so in NOTES.md with the reason, and state as a
   limit which quadratic defects the counted tests would not catch.

Proof for this addition: a quadratic defect planted in a path the first build's two counters
do NOT cover (the builder chooses one, for example rescanning from the start of the input
for each string literal) fails a counted test under item 1; `go test -count=100 -run
'Linearly' ./internal/pgwire/` passes under load with identical counts; `grep -n 'time\.Now\|time\.Since'
internal/pgwire/classify_test.go` shows no clock read on a verdict path of the default run.

The two adversarial tests of chunk 5 that the first build listed
(`TestAdv_5_OperatorSuffixRescanning`, `TestAdv_5_WholeSpanRescanning`) are the
orchestrator's to settle; do not edit them.
