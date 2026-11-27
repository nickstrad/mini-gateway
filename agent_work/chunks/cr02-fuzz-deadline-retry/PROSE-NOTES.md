# CR-2 — one retry in `scripts/fuzz.sh` for Go's own deadline error: prose notes

Prose pass by Neon on 2026-11-27 on branch `work/cr02n-fuzz-deadline-retry`
(pre-prose commit `5d00285`).

## What was written

### A. Comments in Go files (`scripts/fuzz_test.go` only; comments only; AST unchanged)

Added one brief property comment above each of the 13 `Test*` functions introduced by CR-2
in `scripts/fuzz_test.go`:

- `TestFuzzShRetriesDeadlineSignatureAndUsesSecondRunRate`
- `TestFuzzShFailsWhenDeadlineSignatureRepeats`
- `TestFuzzShFailsWhenSecondRunFailsWithOtherMessage`
- `TestFuzzShDoesNotRetryNearMisses`
- `TestFuzzShDoesNotRetryWhenNewCorpusFileAppears`
- `TestFuzzShDoesNotRetryOnCorpusChangesOrSnapshotErrors`
- `TestFuzzShCapsRetriesAtTwoByDefault`
- `TestFuzzShMaxRetriesZeroDisablesRetry`
- `TestFuzzShRejectsBadMaxRetries`
- `TestFuzzShRetryUnreadableSecondRunFailsClosed`
- `TestGateShRecordsNullFuzzRetriesWhenG5SkippedAndRejectsBadMaxRetries`
- `TestGateShLandingCapBlocksWidenedMaxRetriesAlone`
- `TestGateShCountsFuzzRetriesFromColumnSix`

Left the 7 pre-existing CR-1 tests in `scripts/fuzz_test.go` and every other Go file in
the repository untouched.

### B. Book chapter update (`book/chapters/01-simulator-kernel.md`)

- Appended one short section after CR-1: `## Change request CR-2: one retry for Go's own fuzz deadline` (338 words, written with the Clarity skill).
- Covers: what Go 1.26.8's fuzz coordinator does on deadline exit, all five retry
  conditions, single retry per target, default/landing two-target cap, visibility
  (`fuzz: RETRY:`, sixth `retried` column, `NOTE:` count, `fuzz_retries` in `result.json`),
  why real failures cannot be hidden by the whitelist and corpus snapshot, and all stated
  limits.
- Changed no other sentence of Chapter 1, created no new chapter, and did not modify
  `book/manifest.json`.
- Excluded all making-of history (seats, reviews, attacks, repair rounds); linked only
  files listed by `git ls-files` (`CHANGE-REQUEST.md`, `scripts/fuzz.sh`, `scripts/gate.sh`).

### C. Other reader-facing pages (`docs/knowledge/foundation-gates.md`)

- Updated only the `- **Deadline-only retry (CR-2).**` bullet under `## G5: fuzz rates and seed sweeps` to match the final code (SHA-256 NUL-safe symlink snapshot, explicit
  snapshot-error cases, `FUZZ_MAX_RETRIES` 9-digit integer validation and cap wording) and
  stated both limits recorded in `NOTES.md` (unsearchable corpus parent before and after;
  `gawk` vs `mawk` NUL-byte truncation).
- Left every other line of `docs/knowledge/foundation-gates.md` unchanged.

## Claims narrowed or left out for lack of evidence

- **Claim 2 (narrowed):** written as a plain limit of the software in both Chapter 1 and
  `docs/knowledge/foundation-gates.md` — `FUZZ_MAX_RETRIES` defaults to `2` and the
  two-target ceiling holds for defaults and every run that counts for landing
  (`counts_for_landing` becomes `false` above `2`), while standalone runs may raise it.
- **Incident count wording (`CHANGE-REQUEST.md`):** the narrative text wrote "four
  different targets", while its evidence table lists four occurrences across three distinct
  targets (`internal/pgwire FuzzClientSession` appeared twice). Wrote "four times on
  unchanged targets over two days" so the text matches the table without overclaiming.
- **Corpus snapshot fail-closed scope:** narrowed from an unqualified "fails closed if the
  snapshot cannot be taken" to state the unsearchable-parent case (when a parent of
  `testdata/fuzz/<target>/` cannot be searched before and after the run, the snapshot
  treats the directory as absent; `go test` runs as the same user and cannot write there
  either, and the log whitelist still applies).
- **Awk dependency:** stated plainly that the signature parser assumes `gawk` (`awk` on the
  gate machine) and that under `mawk` a NUL byte ends a line early and can admit forged
  logs.
- **No final `gate/result.json` yet / no live deadline hit during build G5 runs:**
  `agent_work/chunks/cr02-fuzz-deadline-retry/gate/result.json` is not committed on this
  branch (the orchestrator runs the full landing gate), and neither build-stage nor
  review-stage G5 runs encountered a live deadline error. Made no claim of a live retry in
  G5 or a final-stage gate pass.

## Commands run

- `go version` -> `go version go1.26.8 linux/amd64`
- `gofmt -l .` -> empty
- `go build ./... && go vet ./...` -> pass
- `go test -count=1 -v ./scripts/` -> PASS (20 tests, 30 subtests, 133.303s)
- `scripts/site.sh /tmp/opencode/cr02n-site` -> `built 24 pages into /tmp/opencode/cr02n-site`; confirmed `book/01-simulator-kernel.html` in `pages.txt` and `book/index.html`, with `Change request CR-2: one retry for Go's own fuzz deadline` in the rendered chapter and TOC
- `awk '/^## Change request CR-2:/{flag=1} flag' book/chapters/01-simulator-kernel.md | wc -w` -> `338`
- `GATE_ONLY=G0,G2,G9 GATE_STAGE=final GATE_PREPROSE=5d00285 GATE_OUT=/tmp/opencode/cr02n-prose-gate scripts/gate.sh cr02-fuzz-deadline-retry` -> verified `G0` (tools, gofmt, build, vet, tools-vet, tools-test, tidy), `G2 astdiff` (AST identical, all changed paths allowed by `prose-paths.txt`), and `G9 site` all pass
