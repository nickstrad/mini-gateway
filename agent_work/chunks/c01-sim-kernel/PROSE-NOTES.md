# Chunk 1 — Simulator kernel: prose pass notes

Prose seat: Gemini Neon, on branch `work/c01-sim-kernel` (worktree `/root/Software/mini-gateway-work/c01`), starting from pre-prose commit `43f4efa7ed2ecda4858fd5bfda3a976e2267edb4`.

## What was written

### A. Go code comments (comments only; AST unchanged)

Added package doc comments, doc comments on every exported identifier (types, constants, variables, functions, and methods), targeted inline comments explaining invariants or non-obvious choices, and one-sentence property comments on every test and fuzz function across all 29 Go files in Chunk 1:

- `internal/sim`: `sim.go`, `time.go`, `stream.go`, `queue.go`, `errors.go`, `trace.go`, `fault.go`, `sim_test.go`, `fault_test.go`
- `internal/sim/simnet`: `net.go`, `options.go`, `conn.go`, `fault.go`, `snapshot.go`, `simnet_test.go`, `semantics_test.go`, `random_test.go`, `closewake_test.go`, `latency_test.go`, `target_test.go`
- `internal/sim/simtest`: `seeds.go`, `determinism.go`, `simtest_test.go`
- `internal/sim/simtoy`: `toy.go`, `scenarios.go`, `scenario_test.go`, `ends_test.go`
- `internal/obs`: `histogram.go`, `snapshot.go`, `obs_test.go`
- `internal/obs/obstest`: `obstest.go`, `obstest_test.go`
- `cmd/mgsim`: `main.go`, `main_test.go`

Files copied or adapted from `/root/Software/object-store` record commit `25fb899317955eb7d87186f940702e55dc0e3cde` in their header/package comments, matching `docs/knowledge/sim-kernel-provenance.md`. Go files under `agent_work/adversarial/` and `tools/` were left untouched.

### B. Book chapter and manifest

- Created `book/chapters/01-simulator-kernel.md` ("1. A world that repeats: the simulator kernel").
- Updated `book/manifest.json`: set chapter 1's `status` from `"planned"` to `"live"`.

### C. Reader-facing pages

- Updated `book/index.md` to replace the "no chapter is live yet" paragraph with Chapter 1 live.
- Updated `README.md` status line, Book link, and "Running what exists today" section (`make test`, `go test ./...`, `go run ./cmd/mgsim list` / `run`, `scripts/sweep.sh`, `scripts/fuzz.sh`).
- Updated `docs/index.md` status line and site table row to mark Chunk 1 landed and link to Chapter 1.
- Left `docs/c4/`, `PLAN.md`, `STATE.md`, and all other files under `agent_work/` untouched.

## Claims narrowed or omitted for lack of evidence

- **No builder claims were narrowed** in `NOTES.md`, `REVIEW-2.md`, or `ADVERSARIAL.md` after the repair round.
- **Replay scope** is stated explicitly as scoped to the same commit, same Go toolchain (`go1.26.8`), and `linux/amd64` (never claimed across architectures or Go versions).
- **Adversarial coverage** is reported with exact counts (**HELD: 30, BROKEN: 4** pre-repair; **HELD: 34, BROKEN: 0** post-repair under `ADV_LONG=1`), framed as attacks that found no counterexample after repair rather than a universal proof.
- **`agent_work/chunks/c01-sim-kernel/gate/result.json` was not present** on disk when this prose pass began (only the individual `G0`–`G10` log files were present under `agent_work/chunks/c01-sim-kernel/gate/`). No fields from a missing `result.json` were invented or quoted; every gate figure cited in Chapter 1 comes directly from `NOTES.md`, `REVIEW-1.md`, `REVIEW-2.md`, the `G*.log` files, or commands run directly during this pass.
- **Runtime durations** from other runs (e.g., 1,000-seed sweep in 17 s, 3,000-seed random-ops run) are attributed to build verification rather than claimed as host-independent guarantees.

## Commands run

With `PATH=/usr/local/go/bin:$PATH` (`go version go1.26.8 linux/amd64`):

- `go run ./cmd/mgsim list` (11 scenarios)
- `go run ./cmd/mgsim run echo-reset -seed 7` (`steps=153 status=pass`, quoted verbatim)
- `go run ./cmd/mgsim run echo-reset -seed 7 -trace` (head and fault/reset/outcome lines quoted verbatim)
- `go test -v ./internal/sim/simtoy -run '^TestPlantedBugCaughtAndShrunk$'` (40 faults shrunk to 1 in 41 runs across seeds 1, 4, 12, quoted verbatim)
- `ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c01-sim-kernel/...` (pass)
- `gofmt -l .` (empty)
- `astdiff` comparing pre-prose commit `43f4efa` against the working tree (exit 0, no findings)
- `go build ./... && go vet ./... && go test -count=1 ./...` (all 8 packages pass)
- `scripts/site.sh /tmp/c01-prose-site` (built 19 pages; verified `book/01-simulator-kernel.html` built and linked in `book/index.html` and `index.html`)
- Path check against `tools/lint-rules/prose-paths.txt` (every changed path allowed)
