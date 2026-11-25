# Chunk 2 — Simulated world: prose pass notes

Prose seat (Neon), 2026-11-25, branch `work/c02-sim-world` (from pre-prose commit `bb53cf8`).

## What was written

1. **Go doc and inline comments (comments only, AST-identical to `bb53cf8`):**
   - `internal/randdist/`: package doc comment, doc comments on `MaxGapFactor`, `ExpGap`, `NewZipf`, and test comments in `randdist_test.go`.
   - `internal/testclient/`: package doc comment (`session.go`), doc comments on all exported constants, errors, types, functions, and methods (`frame.go`, `msg.go`, `session.go`), inline note on deterministic slice-order startup encoding (`EncodeStartup`), and per-test comments in `imports_test.go` and `session_test.go`.
   - `internal/sim/fakepg/`: package doc comment (`server.go`), doc comments on every exported symbol across `config.go`, `server.go`, `exec.go`, `fault.go`, `frame.go`, `log.go`, `match.go`, `proto.go`, `settings.go`, `snapshot.go`, and `export_test.go`; focused inline why/invariant comments on dead-end refusal connections (`server.go`), back-pressure pump and refusal ordering (`backend.go`), statement-start snapshot, `RowDescription`-at-start, stall-window boundary overlap, and `peakRowsAfter` (`exec.go`), `FaultMaxConn` clamp and back-pressured `terminate` framing (`fault.go`), pre-multiply overflow guard (`match.go`), and `pgproto3` isolation (`proto.go`); one brief property comment per test/fuzz function across all 9 `*_test.go` files.
   - `internal/sim/workload/`: package doc comment (`workload.go`), doc comments on every exported symbol across `config.go`, `workload.go`, `keys.go`, `fault.go`, `probe.go`, `snapshot.go`; inline comments on retry-aware backlog admission, `slices.Clip` preserving `r.orig`, and intended vs from-send latency recording (`client.go`); per-test comments across all 6 `*_test.go` files.
   - `internal/sim/world/`: package doc comment (`driver.go`), doc comments on every exported symbol across `driver.go`, `scenario.go`, `baselines.go`, `check.go`, `fidelity.go`; per-test comments across all 6 `*_test.go` files.
   - `cmd/mgsim/`: comments on Chunk 2's added functions in `world.go` and updated the `TestListNamesEveryScenario` comment in `main_test.go` for Chunk 2's changed line (`+len(world.Scenarios())`). Chunk 1's untouched code was left alone.

2. **Book chapter and manifest:**
   - Added `book/chapters/02-simulated-world.md` ("A fake Postgres and hostile clients").
   - Set chapter 2's `status` to `"live"` in `book/manifest.json`.
   - Updated `book/index.md` to reflect Chapters 1 and 2 live.

3. **Other reader-facing pages:**
   - Updated `README.md` (Chunk 2 landed, Chapter 2 live, 5 baseline scenarios via `mgsim`, `scripts/tier-r.sh`, 7 fuzz targets).
   - Updated `docs/index.md` (Chunk 2 landed, Chapter 2 live with link).
   - Left `PLAN.md`, `STATE.md`, `docs/c4/`, and all other `agent_work/` files untouched.

## Claims narrowed or left out for lack of evidence

- **Claim 7 (open-loop generation):** Written as narrowed per `ADVERSARIAL.md` and `NOTES.md`—arrivals follow the schedule independent of server speed and latency is measured from intended send time, while wire sends wait for the previous reply on a single-session client connection so multi-statement transactions never interleave.
- **Claim 11 (`pgproto3` imports):** Written as narrowed per `ADVERSARIAL.md`—holds for every Go file outside `tools/` (including tests, nested modules, and `testdata`), noting that `tools/detlint`'s own importlint fixtures under `tools/` must import `pgproto3` to test the rule.
- **Arbitration and Review 2:** Reported plainly from `REVIEW-2.md` and `ARBITRATION-1.md`—two failed reviews, the `rowsAfter` -> `peakRowsAfter` commit-time blocker (Claim 3) and the retry-backlog minor finding (including the arbiter's `NotYetSent` gauge reproduction), and the arbiter's two fix commits and `PASS` verdict.
- **Gate `result.json`:** `agent_work/chunks/c02-sim-world/gate/result.json` was not present on disk (removed after arbitration's sandboxed run as documented in `ARBITRATION-1.md`), while the post-arbitration gate check logs (`G3-unit.log`, `G4-sweep.log`, `G5-fuzz.log`, `G6-tier-r.log`, `G10-extra.log`) were present in `agent_work/chunks/c02-sim-world/gate/`. Quoted only the numbers verified directly from those logs, `NOTES.md`, `REVIEW-2.md`, `ARBITRATION-1.md`, and commands executed in this pass; did not claim a final-stage `result.json` exists yet.
- **Tier R:** Did not re-run Docker tier R in this prose pass; cited the recorded Postgres 17.11 results from `G6-tier-r.log`, `REVIEW-2.md`, and `NOTES.md`, and verified CI's `TestFidelityAgainstRecordedPostgres17` directly.

## Commands run

All with `PATH=/usr/local/go/bin:$PATH` in `/root/Software/mini-gateway-work/c02`:

- `go run ./cmd/mgsim list`
- `go run ./cmd/mgsim run direct-500-connections -seed 1`
- `go run ./cmd/mgsim run direct-reconnect-burst -seed 7`
- `go run ./cmd/mgsim run direct-1000-identical-reads -seed 1`
- `go run ./cmd/mgsim run direct-open-loop-overload -seed 1`
- `go run ./cmd/mgsim run direct-mixed-transactions -seed 1`
- `go test -v -count=1 ./internal/sim/workload -run '^(TestLatencyIsMeasuredFromIntendedSendTime|TestZipfSharesMatchTheCalculation|TestPoissonInterArrivalMean)$'`
- `go test -v -count=1 ./internal/sim/world -run '^(TestOverloadLatencyGrowsWithTheBacklog|TestFidelityAgainstRecordedPostgres17)$'`
- `go test -v -count=1 ./internal/sim/fakepg -run '^TestPlantedBug'`
- `gofmt -l .`
- `go build ./... && go vet ./... && go test -count=1 ./...`
- `scripts/site.sh _site` (and confirmed `_site/book/02-simulated-world.html` and `_site/book/index.html` listing)
- `astdiff` against pre-prose commit `bb53cf8` and `prose-paths.txt` check
