# Foundation: gates, lints, markers and the site (chunk 0, 2026-11-24)

What a builder needs to know before the first line of product Go. The scripts' own headers
are the reference; this page is the map. Tool rules in full: `tools/detlint/README-tools.txt`.

## The three commands

| Command | What it is | Time at chunk 0 |
| --- | --- | --- |
| `scripts/gate.sh <chunk-dir>` | The local landing gate, G0 to G10. The landing authority. Writes `agent_work/chunks/<chunk-dir>/gate/result.json`. | about 10 s |
| `scripts/ci-fast.sh` (`make ci`) | What CI runs: G0, G1 and G3 with `go test -short`. Never counts for landing. | about 5 s |
| `scripts/gate-canaries.sh` | Plants one violation per check in a throwaway copy and requires the gate to go red. | about 2.5 min |

Everything runs on the host with the host Go toolchain (`/usr/local/go/bin`). Docker is used
only by tier R, later. `<chunk-dir>` is the folder name under `agent_work/chunks/`, for
example `c01-sim-kernel`.

## The comment rule and the three stages (G2)

The builder writes Go with **no comments**. All comments are written later, once, by the
prose seat. "Product Go" is every `.go` file outside `tools/` and `agent_work/`; `testdata`
folders are not checked. Allowed in product Go at any time: `//go:build` (before the
package clause), `//go:embed`, `//go:generate`, `//go:noinline`, and `// Output:` blocks
inside `Example` functions in test files.

| `GATE_STAGE` | When | What G2 does |
| --- | --- | --- |
| `build` (default) | The builder's and the reviewer's gate runs, before the prose pass | `nocomments -baseline`: no comment in product Go beyond those in the baseline commit. The baseline is `GATE_BASE`, default the merge base with `main`. A baseline equal to HEAD is refused with exit 2 (a commit cannot certify itself), so on `main` itself name an earlier commit. |
| `final` | After the prose pass | Needs `GATE_PREPROSE=<last commit before the prose pass>`. `astdiff` between that commit's tree and the working tree: every Go file must have the same syntax tree, so the prose pass is proved to have changed comments only. `go.mod` and `go.sum` must be byte-identical. |
| `foundation` | Chunk 0 only | G2 is skipped. The gate refuses this stage for any chunk other than `c00-foundation`. |

The stage is recorded in `result.json`.

## Stages that are not built yet: the marker files

G4 (sweep), G5 (fuzz), G6 (tier R), G7 (CSV assertions) and G8 (generated documents) each
run a script that starts as a skeleton printing `not built yet` and exiting 3.

- Each has a marker file `scripts/.not-built/<name>` (`sweep`, `fuzz`, `tier-r`,
  `csv-assert`, `gen-docs`; also `race-long`, `soak`, `bench`, `obs-chain` for the scripts
  the gate does not run, and `make-test`, `make-chaos` for the Makefile targets).
- While the marker exists and the script exits 3 saying `not built yet`, the gate records
  `skip-not-built`. That does not make the gate red, and the skipped stages are listed in
  `result.json` under `skip_not_built`.
- **The chunk that builds a script deletes its marker in the same commit.** From then on
  the stage must run and pass.
- Marker gone but the script still says `not built yet`: red. Marker present but the
  script ran (any other exit status, including 0): red. A stage cannot be skipped silently
  once it exists, and cannot exist while still marked as missing.
- `make test`, `make chaos` and `make bench` fail with `not built yet` on the same markers.
  After `make-test` is deleted `make test` runs `scripts/ci-fast.sh`. The chunk that deletes
  `make-chaos` writes the recipe in the Makefile.

Contract for the scripts when they are built (their headers repeat it): the gate calls
`SWEEP_SEEDS=<n> RUN_DIR=<dir> scripts/sweep.sh`, `FUZZ_SECONDS=<n> scripts/fuzz.sh`,
`RUN_DIR=<dir> scripts/tier-r.sh`, `scripts/csv-assert.sh <sweep-run-dir> <tier-r-run-dir>`
and `scripts/gen-docs.sh --check`. Exit 0 is green; anything else is red. A first line
`NOTE: text` in a script's output becomes the note of its row in `result.json`.

## When a result counts for landing

`counts_for_landing` is true only when: every check ran (no `GATE_ONLY`); `overall` is
`pass`; the tree was clean when the gate started (changes under the chunk's own `gate/`
folder are ignored, so the result file can be rewritten); HEAD is a full commit id; and
none of `GATE_SHORT`, `GATE_OUT`, `SWEEP_SEEDS` under 200 or `FUZZ_SECONDS` under 20 was
used. The gated commit is the one before the commit that adds `result.json`; after it only
`agent_work/`, `STATE.md` and `docs/knowledge/` may change (`agent_work/orch/land.sh`
checks this).

Check statuses: `pass`, `fail`, `skip-not-built`, `skip-foundation` (G2 at the foundation
stage), `skip-none` (G10 when the chunk has no `gate/extra.sh`).

## The rules files (`tools/lint-rules/`)

| File | Read by | How to change it |
| --- | --- | --- |
| `det-packages.txt` | G1 detlint | **Add a package to the determinism lint:** one line, a directory relative to the repository root, `internal/pool` or `internal/sim/...`. A line whose directory does not exist is red. Only the listed packages are checked; a helper package they import is not checked unless it is listed too. |
| `detlint-allow.txt` | G1 detlint | `file<TAB>Func<TAB>rule<TAB>justification`. An unused line is red. |
| `race-packages.txt` | G3 race, and CI | **Add a package to the race run:** one line, a `go test` pattern, `./internal/shell/...`. A line that matches no package is red. |
| `shell-packages.txt` | G1 boundlint | Starts with `internal/shell/...`; a directory that does not exist yet is skipped, so the lint starts with the shell. |
| `boundlint-allow.txt` | G1 boundlint | Same four fields; the rule is `unbuffered` or `unbounded-growth`. `chan-cap` cannot be allowed: name the number. |
| `panic-allow.txt` | G1 paniccheck (`internal/`, `cmd/`) | `file<TAB>Func<TAB>justification` for every `panic(`. |
| `imports.txt` | G1 importlint | `deny`, `only` and `require` lines; syntax at the top of the file. The test-client package is written there as `internal/testclient/...`, a placeholder: the chunk that creates the test client puts its real directory there. |

Facts about the lints that are easy to trip over:

- detlint bans all floating point, all concurrency, the wall clock and most of the standard
  library in the packages it checks; its import list is in `README-tools.txt`.
- importlint reads every `.go` file whatever its build tags, test files included. A
  third-party import is allowed only if an `only` rule names it; today that is `pgproto3`
  alone. `go.mod` may have no requirement, or exactly `github.com/jackc/pgx/v5`; indirect
  lines are accepted only beside it and only as `go mod tidy` writes them (G0 runs
  `go mod tidy -diff`).
- A directory named `tools`, `agent_work` or `vendor` below the top level is an importlint
  finding, because the comment, panic and import checks skip those names anywhere.
  `testdata` is skipped anywhere, as the go tool does.
- boundlint accepts a channel capacity only if it is a named constant or a field of a
  struct whose type name contains `config` (or that is declared in a package named
  `config`). `make(chan T, 2*cap)` and `make(chan T, int(cfg.N))` are findings.
- An empty module is green: with no product Go, `gofmt`, `go build`, `go vet` and `go test`
  are recorded as passed with the note "no Go packages in the module yet".

## Chunk extras (G10)

If `agent_work/chunks/<chunk-dir>/gate/extra.sh` exists it must be executable and exit 0.
It is where a chunk puts its own end-to-end list and its planted-bug canary. It receives
`CHUNK` and `RUN_DIR`. Without the file G10 is recorded as `skip-none`.

## Building the site locally

`scripts/site.sh [output-dir]` (`make site` builds into `_site/`). The system Python on
vm-1 has no pip, so the script downloads the pinned `Markdown` wheel once, checks its
SHA-256 and unpacks it into `.cache/pylib` in the checkout (gitignored). On a machine whose
Python can already import `markdown`, and in CI, nothing is downloaded. `SITE_PYLIB=<dir>`
points at an unpacked copy elsewhere.

## The book

- `book/manifest.json` lists every chapter: `number`, `chunk`, `title`, `file`
  (`book/chapters/NN-<slug>.md`, `NN` equal to the number) and `status` (`planned` or
  `live`). Entry 0 is the preface that chunk 16 writes.
- To publish a chapter: add its file and set its status to `live` in the same commit. The
  site build fails if a live chapter's file is missing, if a planned chapter's file exists,
  or if `book/chapters/` holds a file the manifest does not list.
- `book/index.md` is the front page; the build appends the table of contents from the
  manifest (live chapters as links, planned chapters as plain text). The page is published
  at `book/index.html`, chapters at `book/NN-<slug>.html`, and both are listed in
  `pages.txt`.
- A link in any published page to a chapter that is not live yet is a broken link and
  fails the build.
