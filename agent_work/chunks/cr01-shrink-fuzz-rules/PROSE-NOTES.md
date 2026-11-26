# CR-1 — value lowering in the shrinker, and the fuzz rules in the tooling: prose notes

Prose pass by Neon on 2026-11-26 on branch `work/cr01-shrink-fuzz-rules`
(pre-prose commit `c037c84`).

## What was written

### A. Comments in Go files (comments only; AST unchanged)

Kept strictly to the files specified in Part A of the brief (`internal/sim` files added by
CR-1 and the file CR-1 added to `internal/sim/simtoy`, and nothing else):

- `internal/sim/lower.go`: doc comments on every exported identifier (`FaultFloor`,
  `LowerInts`, `LowerValues`, `Minimize`), documenting contracts, floors, non-mutation,
  one-step minimality for monotone and non-monotone predicates, and exact predicate-call
  bounds; targeted inline comments on `plantAcceptUnchecked` (canary hook), `Minimize`'s
  `isolated` wrapper and stopping invariant, and `lowerOne`'s overflow-free bisection
  invariant. (Package `sim` already has its package doc comment in `internal/sim/sim.go`,
  which was left untouched.)
- `internal/sim/schedtext.go`: doc comments on every exported identifier (`KindName`,
  `ErrScheduleText`, `FormatSchedule`, `ParseSchedule`), documenting the grammar,
  `MaxScheduleLen` (1,024) bound, canonical rules, and error contract; inline comments on
  pre-split line counting, canonical integer round-trip check, and rejecting numeric
  `fault-<id>` for named kinds.
- `internal/sim/lower_test.go`: one brief property comment per `Test*` function (9 tests).
- `internal/sim/schedtext_test.go`: one brief property comment per `Test*`/`Fuzz*` function
  (4 tests, 1 fuzz target).
- `internal/sim/shrink_freeze_test.go`: one brief property comment per `Test*` function
  (2 tests).
- `internal/sim/simtoy/shrunk_test.go`: one brief property comment per `Test*` function
  (3 tests).

Left untouched per Part A's explicit file scope: `internal/sim/world/property_test.go`
(repaired test-only file outside `internal/sim` and `internal/sim/simtoy`, no exported
identifiers) and `scripts/fuzz_test.go` (outside `internal/sim` and `internal/sim/simtoy`,
no exported identifiers).

### B. Book chapter update

- `book/chapters/01-simulator-kernel.md`: appended one short section
  ("Change request CR-1: value lowering, scenario files, and fuzz rate rules") — four
  paragraphs plus one verbatim `go test` output block showing `Shrink` vs `Minimize` value
  sums (2,213,137 -> 1,021,423), scenario replay, and `LowerInts` workload lowering
  (`[3 20 40] -> [3 1 1]`). Left the rest of Chapter 1 unchanged because CR-1 was strictly
  additive and made no existing sentence untrue. Did not create a new chapter and did not
  edit `book/manifest.json`.

### C. Other reader-facing pages

None modified, per the brief.

## Claims narrowed or left out for lack of evidence

- **Claim 5 (narrowed, accepted by the orchestrator):** stated everywhere (code comments
  and Chapter 1) as round-tripping schedules of at most `MaxScheduleLen` (1,024) faults,
  never as unbounded schedules.
- **No final-stage `gate/result.json` yet:** did not claim a green final-stage gate run;
  reported the review, adversarial, and repair facts from `NOTES.md`, `REVIEW-1.md`,
  `REVIEW-2.md`, `RESULTS.md`, and `ADVERSARIAL.md`, including the open host-load
  `context deadline exceeded` caveat in G5.
- **Adversarial provenance:** stated plainly that Astra wrote the 24-attack harness, was
  stopped twice by its provider's content filter before writing a report, and that the
  orchestrator ran the tests unchanged and assembled `RESULTS.md` (with sufficiency still
  an open question in `STATE.md`).
- **Bloat-row cap limit:** reported the 256-byte `fuzzMaxBloatRowBytes` cap in
  `FuzzWorldSchedule`'s test helper and the exact observed numbers (4,069,926 steps /
  11–14 s before; 0.05 s replay on `FuzzWorldSchedule/7edb6a746e048b6e` and <= 100,000-step
  budget after).

## Commands run

- `go test -count=1 -v ./internal/sim -run 'Lower|Minimize|Planted|Shrink|ScheduleText'` (16/16 pass)
- `go test -count=1 -v ./internal/sim/simtoy -run 'PlantedBugMinimised|ShrunkScenario|WorkloadSizes'` (3/3 pass; verbatim output quoted in Chapter 1)
- `go test -count=1 -v ./internal/sim/world -run 'TestFuzzWorldScheduleCostIsBounded|FuzzWorldSchedule'` (pass; `FuzzWorldSchedule/7edb6a746e048b6e` in 0.05s)
- `go test -count=1 -v ./scripts/` (7/7 pass)
- `gofmt -l .` (empty)
- `go build ./... && go vet ./... && go test -count=1 ./...` (all green)
- `GATE_ONLY=G0,G1,G2 GATE_STAGE=final GATE_PREPROSE=c037c84 GATE_OUT=/tmp/cr01-prose-gate scripts/gate.sh cr01-shrink-fuzz-rules` (verifies `astdiff` and `prose-paths.txt`)
- `scripts/site.sh /tmp/cr01-site` (verifies site build, Chapter 1 HTML, book contents listing, and all links/anchors)
