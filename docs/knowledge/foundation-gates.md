# Foundation: gates, lints, markers and the site (chunk 0, 2026-11-24)

What a builder needs to know before the first line of product Go. The scripts' own headers
are the reference; this page is the map. Tool rules in full: `tools/detlint/README-tools.txt`.

## The three commands

| Command | What it is | Time at chunk 0 |
| --- | --- | --- |
| `scripts/gate.sh <chunk-dir>` | The local landing gate, G0 to G10. The landing authority. Writes `agent_work/chunks/<chunk-dir>/gate/result.json`. | about 10 s |
| `scripts/ci-fast.sh` (`make ci`) | What CI runs: G0, G1 and G3 with `go test -short`. Never counts for landing. | about 5 s |
| `scripts/gate-canaries.sh` | Plants one violation per check in a throwaway copy and requires the gate to go red. | about 2 min |

Everything runs on the host with the host Go toolchain (`/usr/local/go/bin`). Docker is used
only by tier R, later. `<chunk-dir>` is the folder name under `agent_work/chunks/`, for
example `c01-sim-kernel`.

## The comment rule and the three stages (G2)

The builder writes Go with **no comments**. All comments are written later, once, by the
prose seat (Neon, using the Clarity skill; PLAN.md section 19.4). "Product Go" is every `.go` file outside `tools/` and `agent_work/`. Go files in
`testdata` folders are checked too (a fixture that must not parse needs a name that does
not end in `.go`). Allowed in product Go at any time: `//go:build` (before the
package clause), `//go:embed`, `//go:generate`, `//go:noinline`, and `// Output:` blocks
inside `Example` functions in test files.

| `GATE_STAGE` | When | What G2 does |
| --- | --- | --- |
| `build` (default) | The builder's and the reviewer's gate runs, before the prose pass | `nocomments -baseline`: no comment in product Go beyond those in the baseline commit. The baseline is `GATE_BASE`, default the merge base with `main`. A baseline equal to HEAD is refused with exit 2 (a commit cannot certify itself), so on `main` itself name an earlier commit. |
| `final` | After the prose pass | Needs `GATE_PREPROSE=<last commit before the prose pass>`. Two proofs in the one check `G2 astdiff`. (1) `astdiff` between that commit's tree and the working tree: every Go file must have the same syntax tree, so the prose pass changed comments only; `go.mod` and `go.sum` must be byte-identical. (2) Every path of `git diff --name-only --no-renames GATE_PREPROSE HEAD` must be allowed by `tools/lint-rules/prose-paths.txt`, or it is a `prose-path` finding. |
| `foundation` | Chunk 0 only | G2 is skipped. The gate refuses this stage (exit 2) for any chunk other than `c00-foundation`, and for `c00-foundation` too as soon as the tree holds a `.go` file outside `tools/` and `agent_work/`. |

The stage is recorded in `result.json`.

What the prose pass may change, and nothing else: comments in Go files (not under `tools/`,
`agent_work/`, `vendor/` or a `testdata` folder), files under `book/`, Markdown under `docs/`
except `docs/c4/` and the generated contract documents, `README.md`,
`agent_work/chunks/<chunk-dir>/PROSE-NOTES.md`, and the gate's own
`agent_work/chunks/<chunk-dir>/gate/result.json` (so a final-stage run can be repeated after
its result was committed). The list is `tools/lint-rules/prose-paths.txt`; the gate reads it
from the `GATE_PREPROSE` commit, so a prose pass cannot widen it. A test fixture, a script, a
rules file or a `NOTES.md` changed during the prose pass is red. **The chunk that adds a
generated contract document adds its `deny` line to that file.**

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
- **A marker may only ever be removed.** A marker in the tree is red when a commit in the
  history of HEAD removed it (`git log --full-history -m --diff-filter=D`), or when it is
  absent at the baseline commit (`GATE_BASE`, default the merge base with `main`), which
  means this branch added it. The stage of that marker goes red, and the row `G4 markers`
  judges every file in `scripts/.not-built/`, including the markers of scripts the gate
  does not run. So a built stage cannot be turned back into a skeleton, and no new
  skippable stage can be invented after chunk 0. The baseline half is not applied at the
  foundation stage, because chunk 0 creates the markers. Limit: the history half reads
  the history git has; a branch whose history was rewritten (squashed) so that the
  removing commit is gone is caught only by the baseline half, that is, once the removal
  is on `main`.
- `make test`, `make chaos` and `make bench` fail with `not built yet` on the same markers.
  After `make-test` is deleted `make test` runs `scripts/ci-fast.sh`. The chunk that deletes
  `make-chaos` writes the recipe in the Makefile.

Contract for the scripts when they are built (their headers repeat it): the gate calls
`SWEEP_SEEDS=<n> RUN_DIR=<dir> scripts/sweep.sh`, `FUZZ_SECONDS=<n> scripts/fuzz.sh`,
`RUN_DIR=<dir> scripts/tier-r.sh`, `scripts/csv-assert.sh <sweep-run-dir> <tier-r-run-dir>`
and `scripts/gen-docs.sh --check`. Exit 0 is green; anything else is red. A first line
`NOTE: text` in a script's output becomes the note of its row in `result.json`.

## G5: fuzz rates and seed sweeps (CR-1, 2026-11-26; CR-2, 2026-11-27)

- `scripts/fuzz.sh` prints, after all targets, a table: package, target, executions,
  executions per second, class, retried (`0` or `1`; the first five columns are unchanged).
  The rate is the executions of Go's last `fuzz: elapsed: <d>, execs: <N> (<M>/sec)` line
  divided by its elapsed seconds (Go's own `M` covers only the last interval and is often 0
  on the final line). The rate comes from the run that stands.
- A target is simulation-level when `go list -deps -test ./<pkg>` lists `internal/sim`
  (the package itself counts). Simulation-level and below `FUZZ_RATE_FLOOR` (default 100
  per second) is class `seed-sweep`: reported, not failed, and not fuzzing in any claim.
- A target without a readable rate is red (class `unreadable`). This happens when the
  target never gets past Go's baseline coverage pass in the time budget (a large cached
  corpus on a slow target), or when the go tool fuzzes nothing (a `//go:build` line that
  excludes the file).
- **Deadline-only retry (CR-2).** Go's fuzz coordinator sometimes ends a target at
  `-fuzztime` with `context deadline exceeded` and writes no failing input. `scripts/fuzz.sh`
  retries a target once when — and only when — all five conditions hold: (1) non-zero exit,
  (2) a `--- FAIL: <target>` block whose only message is bare `context deadline exceeded`,
  (3) no `Failing input written to` line, (4) no panic or other failure text anywhere in
  the output, and (5) no change to the package's `testdata/fuzz/<target>/` corpus tree
  (files added, removed, or changed in content, compared NUL-safely through symlinks using
  SHA-256 hashes; snapshot errors such as broken symlinks, symlink loops, non-directory
  roots, special files, unreadable entries, or tool errors fail closed and block the retry).
  The second run's result and rate stand, whatever its message. At most `FUZZ_MAX_RETRIES`
  targets are retried per run (default `2`; `0` disables the retry; accepts `0` or positive
  integers up to 9 digits and exits 2 otherwise; a standalone run may set it above `2`, but
  any value above `2` sets `counts_for_landing` to `false`). Each retry prints
  `fuzz: RETRY: <dir> <target>: go's own deadline, no failing input`, sets `retried` to `1`
  in the table, and is counted in the `NOTE:` line (`<N> retried`). Any other failure, a
  second failure on the retried target, or another target hitting the signature once
  `FUZZ_MAX_RETRIES` targets have already been retried (a third target under the default
  cap of `2`) fails the run. Limits: (a) if a parent of `testdata/fuzz/<target>/` cannot be
  searched at all before and after the run, the corpus directory is snapshotted as absent
  rather than as a snapshot error, so the snapshot does not block the retry (`go test`,
  running as the same user, cannot write a failing input there either, and the log checks
  still apply); (b) the signature check is written for `gawk` (`awk` on the gate machine),
  whereas under `mawk` a NUL byte ends a line early and can let forged logs through that
  `gawk` rejects.
- The gate writes the table to `gate/G5-fuzz.tsv` (gitignored) and records
  `fuzz_targets`, `fuzz_seed_sweeps`, `fuzz_retries`, `fuzz_rate_floor` and
  `fuzz_max_retries` in `result.json` (counts are `null` when G5 did not run).
  `fuzz_retries` does not change `counts_for_landing` by itself; a `FUZZ_RATE_FLOOR` under
  100 or a `FUZZ_MAX_RETRIES` above 2 never counts for landing. `FUZZ_GO` is a test seam of
  `fuzz.sh` (`scripts/fuzz_test.go`); the gate unsets it.
- Canaries: `g5-unreadable-rate` (red), `g5-seed-sweep-reported` (green control),
  `g5-retry-reported` (green: planted first-run deadline signature is retried, reported and
  counted), and `g5-crasher-not-retried` (red: planted failing input is never retried and
  fails G5). The older `g5-built-with-marker` cannot be caught any more: chunk 1 deleted
  the `fuzz` marker, so no stage the gate runs has a marker left.

## When a result counts for landing

`counts_for_landing` is true only when: every check ran (no `GATE_ONLY`); `overall` is
`pass`; the tree was clean when the gate started (the chunk's own `gate/result.json` is
the one path that is ignored, so the result file can be rewritten; `gate/extra.sh` and
`gate/canaries.json` are inputs and count); HEAD is a full commit id; and none of
`GATE_SHORT`, `GATE_OUT`, `SWEEP_SEEDS` under 200, `FUZZ_SECONDS` under 20, `FUZZ_RATE_FLOOR` under 100 or `FUZZ_MAX_RETRIES` above 2 was used.

A run that is refused (exit 2: bad stage, bad `GATE_ONLY`, no baseline, a tool missing)
removes any `result.json` already in its output folder before it exits, and a run that
starts removes it before its first check. The file therefore describes the last run that
finished, or does not exist. The one refusal that cannot do this is a missing or malformed
chunk name: there is then no output folder to clean.

## What `agent_work/orch/land.sh` accepts

`land.sh <chunk-dir> <branch> <message-file>` reads the evidence from the branch tip and
refuses unless all of this holds for `agent_work/chunks/<chunk-dir>/gate/result.json`:

- `chunk` equals `<chunk-dir>`; `overall` is `pass`; `counts_for_landing` is `true`;
  `partial` is `false`; `dirty_tree` is `false` (each checked on its own).
- `stage` is `final`. A `build`-stage result never lands: every chunk lands after its prose
  pass. `foundation` is accepted for `c00-foundation` only.
- `sha` is a commit on the branch (the gated commit), and since that commit only these
  paths changed: `agent_work/chunks/<chunk-dir>/gate/result.json`,
  `agent_work/chunks/<chunk-dir>/*.md`, `agent_work/INDEX.md`, `STATE.md`,
  `docs/knowledge/*.md`. Nothing else: a changed `gate/extra.sh`, `gate/canaries.json`
  or anything under `agent_work/adversarial/` after the gated commit is refused. Commit
  such files first, then run the gate, then commit `result.json`.
- The review and adversarial files are present and passing. `LAND_NO_REVIEW=1` skips them
  for `c00-foundation` only; for any other chunk the script refuses to run.

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
| `prose-paths.txt` | G2 at the final stage | `allow <regex>` and `deny <regex>` lines over repository paths; a deny wins; `{chunk}` is the chunk folder. Read from the `GATE_PREPROSE` commit. |
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
- `testdata`: the go tool, gofmt in G0, detlint, paniccheck and boundlint leave `testdata`
  folders out. Two rules make that safe. importlint reads the Go files in them like any
  other (a file under `internal/pool/testdata/` obeys the rules for `internal/pool/...`),
  and **importing a package whose path has a `testdata` element is a finding
  (`testdata-import`)**, so code there cannot be reached from product or test code. The
  comment rule reads them as well. Limit: importlint sees imports, not a script or a
  `go:generate` line that runs a program kept in `testdata`.
- boundlint accepts a channel capacity only if it is a named constant or a field of a
  struct whose type name contains `config` (or that is declared in a package named
  `config`). `make(chan T, 2*cap)` and `make(chan T, int(cfg.N))` are findings. A channel
  type behind an alias or a named type is still a channel, and a `make` inside a helper is
  checked where it is written.
- boundlint's growth rule (`unbounded-growth`) covers, in a `for { }` or a `range` over a
  channel: append to, or a map insertion into, a struct field, anything behind a pointer
  dereference (`q := &b.queue; *q = append(*q, v)`), a package variable, and a map the
  function did not make itself (`m := b.seen; m[k] = v`). It follows calls to functions and
  methods of the same package (`for { b.push(v) }`). The fix is a `len()` comparison in the
  function that grows, or in the looping function for the same field. What it cannot see
  in principle: calls through an interface or a function value, calls into another
  package, and growth that is neither append nor a map insertion (a `bytes.Buffer`, a
  list). The full list is in `README-tools.txt`, section 7.
- An empty module is green: with no product Go, `gofmt`, `go build`, `go vet` and `go test`
  are recorded as passed with the note "no Go packages in the module yet".

## G3 also runs the counted growth tests (2026-11-27)

G3 runs three checks: `unit` (`go test -count=1 ./...`), `counted`, and `race`. `counted`
invokes `scripts/counted-growth.sh`, which runs
`go test -tags clscount -count=1 -run Linearly -v ./internal/pgwire/`. The check fails if
that run exits non-zero or if any of the five growth tests the script names does not report
`PASS` (so a renamed test, a skipped tagged test, a dropped build tag, or `"no tests to
run"` turns red). `COUNTED_GO` is a test seam of `counted-growth.sh`
(`scripts/counted_growth_test.go`); `scripts/gate.sh` unsets it before running the script.
`scripts/ci-fast.sh` gets `counted` through the gate's G3, and
`scripts/counted_growth_test.go` verifies the script's arguments, failure modes, and
`gate.sh`/`ci-fast.sh` wiring with a stub `go`.

Why a build tag: the lexer and classifier count every loop step and lexer-pass token step
only when `clscount.On` is true (`internal/pgwire/clscount/on.go`, tag `clscount`). In the
default build the constant is false and the compiler removes every `if clscount.On { … }`
block at compile time. The switch sits in its own package, `internal/pgwire/clscount`,
because detlint's `[buildtag]` rule rejects any `//go:build` line inside a package on
`det-packages.txt` (test files included) and type-checks both files of a tag pair together.
`internal/pgwire/clscount` is deliberately omitted from `det-packages.txt`; it imports
nothing and holds only a constant and integer tallies. Because the tagged tests in
`internal/pgwire` skip when `clscount.On` is false, a plain `go test ./...` does not check
per-step lexer work (including string bodies and line comments). Run the full counted suite
by hand with `scripts/counted-growth.sh`.

## Chunk extras (G10)

If `agent_work/chunks/<chunk-dir>/gate/extra.sh` exists it must be a regular file (a
symbolic link, dangling or not, is red, never skipped), executable, and exit 0. It is where
a chunk puts its own end-to-end list and its planted-bug canary. It receives `CHUNK` and
`RUN_DIR`. Without the file G10 is recorded as `skip-none`. It is an input of the gate:
untracked or modified, it still runs, but the tree is dirty and the result does not count;
and `land.sh` refuses a branch on which it changed after the gated commit. Commit it before
the gate run that is meant to count.

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
- The link check reads every `href` and `src` attribute of every tag in a page, Markdown
  links and raw HTML alike, in double quotes, single quotes or none. Not read: `srcset`,
  URLs inside CSS, and links a script builds at run time.
- A relative link must name a file (or a folder holding a file) in `git ls-files`: a file
  that exists only in the worktree, such as an ignored gate log, fails the build (chunk 6a,
  canary `g9-ignored-link`).

## CI leaves out the attack harnesses (2026-11-25)

`scripts/ci-fast.sh` (the gate with `GATE_SHORT=1`) runs `go test -short` on every package
except those under `agent_work/adversarial/`. The adversaries' tests call tools that exist
on vm-1 and not on a GitHub runner (the DuckDB CLI, Docker, Go at `/usr/local/go/bin/go`),
and CI is the fast set only (PLAN.md section 6.3). The local landing gate runs them all, on
every chunk. CI was red from the chunk 1 landing (`d21b26d`) to `b342e48` for this reason
only; no product test failed. After a landing, check the CI run as well as the Pages run:
`gh run list -R nickstrad/mini-gateway --workflow ci.yml --limit 1`.
