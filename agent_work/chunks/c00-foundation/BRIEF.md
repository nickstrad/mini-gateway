# mini-gateway — chunk 0 (Foundation) — builder brief

You are a fresh builder agent. Work ONLY in the current directory, the git worktree
`/root/Software/mini-gateway-work/c00` (branch `work/c00-foundation`). Never touch the main
checkout `/root/Software/mini-gateway`. Commit on this branch as you go (one logical change
per commit, subject `<area>: <imperative summary>`, each message ending with the line
`Co-Authored-By: Claude <noreply@anthropic.com>`). Do NOT push, do NOT merge, do NOT edit
`PLAN.md` or `STATE.md`. Create no files in `/root` outside this worktree except scratch
under `/tmp/zgw-c00/` (delete it when done).

Read first: `PLAN.md` — the "Chunk 0 — Foundation" section, section 6.3 (gates), section 12
(build flow and landing gate), section 15 (the book) — and `docs/knowledge/phase1-findings.md`.
`agent_work/orch/` already exists in this worktree (launch, wait, land, pages-check, usage
scripts written by the orchestrator); do not rewrite them, but do index them.

Chunk 0 contains NO product logic. It delivers everything a builder needs before the first
line of product Go: the module, the gates, CI, the scripts, and a book site.

Environment: Go 1.26.8 at `/usr/local/go/bin` (add to PATH), `jq`, `gh`, Docker, DuckDB CLI
at `/root/.local/bin/duckdb`. The system Python has no pip: to build the site locally use the
recipe in `docs/knowledge/phase1-findings.md` (Markdown wheel unzipped, `PYTHONPATH`).
The reference implementation of most of this is the sister project at
`/root/Software/object-store` (read-only for you): `agent_work/gates/` (gate.sh, canaries.sh,
ci-fast.sh, detlint/) and `.github/workflows/`. Adapt, do not copy its Docker-container
design: here every gate runs directly on the host with the host Go toolchain, and Docker is
used only later, for real Postgres (tier R).

## Deliverables

1. **Module.** `go.mod`: `module github.com/nickstrad/mini-gateway`, `go 1.26`, no
   requirements. A `doc.go`-free empty module must still pass `go build ./...` and
   `go vet ./...` (handle "no packages" gracefully in the scripts — an empty module is green).
   `.gitignore` for build outputs, `_site/`, gate logs (`agent_work/chunks/*/gate/*.log`).

2. **Lint tools, nested module `tools/detlint`** (own `go.mod`, standard library only),
   copied from `/root/Software/object-store/agent_work/gates/detlint` — the determinism lint
   `detlint` and the commands `astdiff`, `nocomments`, `importcheck`, `paniccheck` with their
   tests and testdata. Record the source repository and commit (`git -C
   /root/Software/object-store rev-parse HEAD`) in `tools/detlint/SOURCE.md` and keep its
   `README-tools.txt`. Their tests must pass here (`cd tools/detlint && go test ./...`).
   Adjust only what the move requires. In `nocomments` the skipped directory names must fit
   this repository (`agent_work`, `.git`, `testdata`, `vendor`, `tools`).
   Add two new commands in the same nested module, each with tests and testdata:
   - `importlint -root DIR -rules FILE`: enforces the import rules of PLAN.md section 6.3,
     read from a rules file `tools/lint-rules/imports.txt` so later chunks extend it without
     code changes. Rules needed from the start (packages that do not exist yet are skipped
     silently): the core packages `internal/pool`, `internal/admission`, `internal/coalesce`,
     `internal/retry`, `internal/work` may not import `internal/pgwire`, `pgproto3`, or any
     package under `internal/obs/` other than `internal/obs` itself; `internal/proxy` may not
     import any adapter package under `internal/obs/`; `github.com/jackc/pgx/v5/pgproto3` may
     be imported only by `internal/pgwire/...`, `internal/sim/fakepg/...` and the test-client
     packages named in the rules file; no other third-party import anywhere in product or
     test code. It also checks the root `go.mod`: zero requirements, or exactly the one
     requirement `github.com/jackc/pgx/v5` (indirect lines that pgx itself forces are allowed
     only if `go mod tidy` produces them; state the exact rule you implement).
   - `boundlint -root DIR PKGDIR...`: the bounded-channel lint for the goroutine shell
     (`internal/shell/...` when it exists). Findings: `make(chan T)` with no capacity or a
     literal-zero capacity unless the line is in an allow file with a justification; a
     channel capacity that is not a named constant or a named config field; a `for { ... }`
     that appends to a slice field or inserts into a map field of a struct with no length
     check in the same function, unless allow-listed. State the exact rules and their
     limits in `README-tools.txt`. Same output format and exit codes as the other tools.

3. **The local landing gate, `scripts/gate.sh <chunk-dir>`** — the landing authority. Runs
   on the host, writes `agent_work/chunks/<chunk-dir>/gate/result.json` plus one log per
   check (logs gitignored), prints one line per check, fails closed (any check that cannot
   run is red; exit 0 only when every requested check is green). Result fields at least:
   `chunk`, `sha` (full HEAD sha), `dirty_tree`, `stage`, `partial`, `overall`
   (`pass`/`fail`), `counts_for_landing`, `go_version`, `duckdb_version`, `started`,
   `seconds`, and `checks[]` (`gate`, `check`, `status`, `seconds`, `note`).
   `counts_for_landing` is true only for a full run (no `GATE_ONLY`), on a clean tree, all
   green, with a valid stage. Checks, in order:
   - **G0 build**: `gofmt -l` empty; `go build ./...`; `go vet ./...`; the same for the
     nested module `tools/detlint` including its `go test ./...`; `go.mod`/`go.sum` tidy
     (`go mod tidy -diff`).
   - **G1 lints**: `detlint` over the packages listed in `tools/lint-rules/det-packages.txt`
     (one package pattern per line; empty at chunk 0) with the allow file
     `tools/lint-rules/detlint-allow.txt`; `importlint`; `boundlint`; `paniccheck` with
     `tools/lint-rules/panic-allow.txt`.
   - **G2 comments**. Rule of this project: the builder (Opus) writes Go with NO comments;
     all comments are written later by the prose seat. `GATE_STAGE=build` (default): product
     Go (everything outside `tools/` and `agent_work/`) has no comments beyond those in the
     baseline commit (`GATE_BASE`, default the merge base with `main`), using `nocomments
     -baseline`. `GATE_STAGE=final`: requires `GATE_PREPROSE=<commit>` and runs `astdiff`
     between that commit's tree and HEAD so the prose pass is proved to have changed no
     code. A build-stage run whose baseline equals HEAD is a setup error (exit 2), as in the
     object-store gate. `GATE_STAGE=foundation` skips G2 (chunk 0 only; nothing else may use
     it — the result records the stage).
   - **G3 tests**: `go test ./...` with `-count=1`, and `go test -race` for the packages
     listed in `tools/lint-rules/race-packages.txt` (empty at chunk 0).
   - **G4 simulation**: `scripts/sweep.sh` with the gate's seed budget (`SWEEP_SEEDS`,
     default 200) — at chunk 0 the skeleton reports "not built yet" and G4 is recorded as
     `skip-not-built`, which is allowed ONLY while the marker file `scripts/.not-built/sweep`
     exists; the chunk that builds the script deletes the marker. Use this same marker
     mechanism for every not-yet-built stage below, so a stage can never be skipped silently
     once it exists, and list in the result which stages were `skip-not-built`.
   - **G5 fuzz**: `scripts/fuzz.sh` with `FUZZ_SECONDS` (default 20) per target.
   - **G6 tier R**: `scripts/tier-r.sh` (real Postgres via Docker).
   - **G7 CSV assertions**: `scripts/csv-assert.sh` over the run directories the earlier
     stages produced.
   - **G8 generated documents**: `scripts/gen-docs.sh --check` (regenerates the generated
     contract documents into a scratch folder and diffs; "not built yet" marker at chunk 0).
   - **G9 site**: builds the site with `.github/scripts/build_site.py` into a scratch folder
     (must succeed, including the book pages).
   - **G10 chunk extras**: if `agent_work/chunks/<chunk-dir>/gate/extra.sh` exists and is
     executable it is run and must exit 0 (the chunk's own e2e list and canary).
   `GATE_ONLY=G0,G3` runs a subset (result marked partial, never counts).

4. **`scripts/ci-fast.sh`** — the CI set only, target under five minutes: G0, G1, G2 is NOT
   run in CI, `go test ./...` with `-short` where a package honours it, race packages, fuzz
   seed corpora as plain tests (that is what `go test` does). No Docker, no DuckDB, no
   sweeps. **`.github/workflows/ci.yml`**: on push to main, pull requests and manual
   dispatch; `actions/setup-go` with the version from `go.mod`; runs `scripts/ci-fast.sh`;
   15-minute timeout. Leave `.github/workflows/pages.yml` working (see item 7).

5. **Makefile**: `make test`, `make chaos`, `make bench` exist and each FAILS with the
   message `not built yet` (exit status non-zero) until its chunk lands — use the same
   marker files — so a green target is never a stub. Also `make gate CHUNK=<chunk-dir>`,
   `make ci` (runs `scripts/ci-fast.sh`), `make site`, `make lint`.

6. **On-demand script skeletons** in `scripts/`, each with a usage header, each exiting 3
   with `not built yet` while its marker exists: `sweep.sh`, `fuzz.sh`, `race-long.sh`,
   `soak.sh`, `tier-r.sh`, `bench.sh`, `csv-assert.sh`, `obs-chain.sh`, `gen-docs.sh`.

7. **Book scaffold and site.** `book/manifest.json`: the book title, and the chapter list of
   PLAN.md section 15 — for each: `number`, `chunk`, `title` (the working title), `file`
   (`book/chapters/NN-<slug>.md`), `status` (`planned` | `live`). No chapter files yet.
   `book/index.md`: a placeholder front page containing only a title, one sentence saying
   what the book is, and the marker line `<!-- FRONT-PAGE: written by the prose seat -->`
   (the prose seat replaces the body; keep it minimal and plain). Extend
   `.github/scripts/build_site.py`: publish `book/index.md` → `book/index.html` with a
   generated table of contents from the manifest (live chapters are links, planned chapters
   are plain text marked "planned"); publish every chapter whose status is `live` →
   `book/NN-<slug>.html` and fail the build if a live chapter's file is missing or a planned
   chapter's file exists; add "Book" to the navigation; change the footer to
   `Build in progress. Source: …` ; keep every existing page and URL working. `pages.txt`
   must list the new pages. Build it locally and confirm.

8. **Gate canaries, `scripts/gate-canaries.sh`**: testing the tester. For each check that
   can run at chunk 0, plant one violation in a throwaway copy of the tree (under
   `/tmp`), run the gate there, and require the right check to go red with the expected
   message in its log; a clean copy must be green first. At minimum: unformatted Go (G0);
   a compile error (G0); a planted `time.Now()` in a package listed for detlint (G1); a
   forbidden import in a core package (G1, importlint); a second `go.mod` requirement (G1);
   an unbounded channel in `internal/shell` (G1, boundlint); a comment in product Go at the
   build stage (G2); a code change between `GATE_PREPROSE` and HEAD at the final stage (G2);
   a failing test (G3); a stage whose marker was deleted while its script still says "not
   built yet" (must be red, not skipped); a failing `extra.sh` (G10); a broken link in a
   site page (G9). Writes `agent_work/chunks/c00-foundation/gate/canaries.json` (commit this
   file) and exits 0 only when every canary is caught. Because the canaries need product
   packages that do not exist yet, each plant creates the minimal package it needs inside
   the throwaway copy.

9. **Notes and index.** `agent_work/chunks/c00-foundation/NOTES.md`: what was built, every
   decision you made that the brief left open, exact commands to run each gate, and
   timings. Add rows to `agent_work/INDEX.md` for every script and tool (path, what it does,
   how to run it, last result). `docs/knowledge/foundation-gates.md`: the durable facts a
   later builder needs (marker mechanism, comment rule and stages, rules files, how to add
   a package to detlint/race lists, how to build the site locally).

## Acceptance (run these yourself; all must hold before you finish)

- `GATE_STAGE=foundation scripts/gate.sh c00-foundation` on a clean committed tree: green,
  `counts_for_landing` true, the not-built stages listed as `skip-not-built`. Commit the
  `result.json`. (The gated commit is the one before the commit that adds result.json;
  only `agent_work/` may change after it.)
- `scripts/gate-canaries.sh`: every canary CAUGHT; `canaries.json` committed.
- `scripts/ci-fast.sh` green and under five minutes; record the time.
- `make test`, `make chaos`, `make bench` each fail with `not built yet`.
- The site builds locally with the book pages; every previously existing page is still built.
- `cd tools/detlint && go vet ./... && go test ./...` green.
- `git status` clean; nothing pushed.

## Limits

If something in this brief cannot be done as written, do the closest correct thing and say
exactly what and why in NOTES.md under "Deviations". Do not ask questions; there is nobody
to answer. Keep shell scripts readable: a usage header per script, plain names.

Finish by printing a short summary (commits, gate result, canary counts, deviations) and
then a final line containing exactly AGENT-DONE.
