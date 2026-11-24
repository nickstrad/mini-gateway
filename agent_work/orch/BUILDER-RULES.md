# mini-gateway — rules for every builder agent (Opus seat)

These rules apply to every chunk build and every repair. Your chunk brief names this file;
where the chunk brief and this file disagree, the chunk brief wins.

## Where you work

- ONLY in the git worktree and branch your brief names. Never touch the main checkout
  `/root/Software/mini-gateway`, another worktree, or anything under `/root` outside your
  worktree except a scratch folder `/tmp/zgw-<chunk>/` (delete it when done).
- Commit on your branch as you go: one logical change per commit, subject
  `<area>: <imperative summary>`, message ending with the line
  `Co-Authored-By: Claude <noreply@anthropic.com>`.
- Do NOT push, merge, rebase onto other branches, tag, or edit `PLAN.md`, `STATE.md`, `README.md`,
  `book/`, `docs/c4/` or `agent_work/orch/`. There is nobody to answer questions: when the
  brief leaves something open, decide, and record the decision in your NOTES.md.
- PATH needs `/usr/local/go/bin` (Go 1.26.8). DuckDB CLI: `/root/.local/bin/duckdb`. Docker is
  available for real Postgres 17 where your brief asks for tier R.

## What you write

- **Go logic and its tests only.** You write NO comments in Go files — none: no doc
  comments, no inline comments, no TODOs (allowed: `//go:build`, `//go:embed`,
  `//go:generate`, `//go:noinline` directives and `// Output:` blocks in examples). All
  comments and all reader-facing prose (book, README, docs pages) are written later by a
  different seat. The gate rejects comments. Make the code readable without them: plain
  names, small functions, named constants.
- Operational notes ARE yours: `agent_work/chunks/<chunk-dir>/NOTES.md` — what you built,
  each decision the brief left open, each claim in the brief with the test that proves it
  (file and test name), anything you could not do and why ("Deviations"), every stated
  limit of the code, and the exact commands to run the tests. The prose seat and the
  reviewer work from this file, so it must be accurate and complete. Generated contract
  documents (from code, held by a test) are also yours where the brief lists them.
- Durable facts a later builder needs go in `docs/knowledge/<topic>.md` (short, factual);
  scripts and tools get a row in `agent_work/INDEX.md`. Commit these separately from code.

## How you build

- **Tests first.** For each deliverable write the test, see it fail, then the code.
- **One planted-bug canary per chunk** (more where the brief says): a switch reachable only
  from tests (never from config) that plants a named bug, and a test asserting that the
  chunk's own scenario or property test FAILS with the bug planted. A check that cannot
  fail proves nothing.
- **Ablation, recovery, quiesce, counters-must-move** (PLAN.md section 6.2) apply to every
  end-to-end scenario your brief lists: mechanism off must fail the invariant; goodput must
  return after the fault ends; after the scenario nothing is in use, queued, pending or
  leaked; and the scenario must show non-zero counters for the thing it exercises.
- **Replay law.** Every simulator scenario you add runs twice with the same seed and must
  give bit-identical trace and state digests; failures print a `REPLAY:` line.
- **Determinism in core and simulator packages** (the packages listed in
  `tools/lint-rules/det-packages.txt`; add yours as the brief says): no wall clock, no
  goroutines, channels, locks or atomics, no global random source, no map iteration order
  reaching a decision or an output, no floating point, no pointer formatting, standard
  library imports only from the lint's list. Time is an argument; randomness comes from a
  named seeded stream.
- **Bounded everything.** Every queue, map, buffer and slice that can grow has a named
  capacity constant or config field and a defined behaviour when full.
- **Observability is a landing gate.** Every component you add or change has a `Snapshot()`
  returning a plain struct copy (integer counters, gauges, fixed-size integer histograms; no
  time, no maps iterated in order-dependent ways), with a test that its counters move and
  that conservation holds. Outcome counters are incremented on every exit path.
- **Dependencies.** Standard library only. The single allowed external module is
  `github.com/jackc/pgx/v5` for its `pgproto3` package, and only in the packages the import
  rules name (`tools/lint-rules/imports.txt`). Never add another requirement.
- **Size.** If the product code (non-test Go) of your chunk passes about 2,000 lines beyond
  the brief's estimate and is still not complete, finish the coherent part that is done, make
  it green, and write a `SPLIT-REQUEST` section at the top of NOTES.md saying exactly what
  remains. Do not hand in a red tree.

## Before you finish

1. `gofmt -l .` prints nothing; `go build ./... && go vet ./...` pass.
2. Read `docs/knowledge/foundation-gates.md`. Add your packages to the rules files it names
   (determinism list, race list), delete the "not built yet" marker of any script you
   build, and keep `scripts/ci-fast.sh` under five minutes (long runs go behind the
   on-demand scripts and `testing.Short()`).
3. Run the local gate at the build stage on a clean, committed tree:
   `scripts/gate.sh <chunk-dir>`. It must be green. Do not commit `result.json` yourself
   unless the brief says so — the orchestrator runs the landing gate.
4. NOTES.md is complete (see above), `git status` is clean, nothing is pushed.
5. Print a short summary: commits, test counts, gate result, product and test line counts
   (`find … -name '*.go'` split by `_test.go`), deviations. Then print a final line
   containing exactly `AGENT-DONE`.
