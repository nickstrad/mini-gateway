# Chunk 0 — Foundation: builder notes

Built by Fable on 2026-11-24 on branch `work/c00-foundation`, from the brief
`/root/briefs/zgateway-p2-c00-foundation.md`. No product logic. Gated commit: `7a19c14`.
Durable facts for later builders are in `docs/knowledge/foundation-gates.md`; this file is
the record of the build.

## What was built

| Deliverable | Where |
| --- | --- |
| Module | `go.mod` (`github.com/nickstrad/mini-gateway`, `go 1.26`, no requirements), `.gitignore` |
| Lint tools, nested module | `tools/detlint/` — `detlint`, `astdiff`, `nocomments`, `importcheck`, `paniccheck` copied from object-store `060bb3e` (`SOURCE.md`); `importlint` and `boundlint` written here; `README-tools.txt` |
| Rules and lists | `tools/lint-rules/` — `imports.txt`, `det-packages.txt`, `race-packages.txt`, `shell-packages.txt`, `detlint-allow.txt`, `boundlint-allow.txt`, `panic-allow.txt` |
| Landing gate | `scripts/gate.sh` (G0 to G10, result in `agent_work/chunks/<chunk-dir>/gate/result.json`) |
| CI | `scripts/ci-fast.sh`, `.github/workflows/ci.yml`; `pages.yml` unchanged |
| Makefile | `test`, `chaos`, `bench` (fail with `not built yet`), `gate`, `ci`, `lint`, `site` |
| Script skeletons | `scripts/{sweep,fuzz,race-long,soak,tier-r,bench,csv-assert,obs-chain,gen-docs}.sh`, markers in `scripts/.not-built/` |
| Book and site | `book/manifest.json`, `book/index.md`, `.github/scripts/build_site.py` extended, `scripts/site.sh` |
| Gate canaries | `scripts/gate-canaries.sh`, result `agent_work/chunks/c00-foundation/gate/canaries.json` |
| Notes and index | this file, `agent_work/INDEX.md`, `docs/knowledge/foundation-gates.md` |

## Commands and results

All run on vm-1, Go 1.26.8, on the committed tree at `7a19c14`.

| Command | Result | Time |
| --- | --- | --- |
| `GATE_STAGE=foundation scripts/gate.sh c00-foundation` | overall pass, `counts_for_landing` true, clean tree. 13 checks pass; G2 `skip-foundation`; G4 sweep, G5 fuzz, G6 tier-r, G7 csv, G8 gen-docs `skip-not-built`; G10 `skip-none` | 8 s |
| `scripts/gate-canaries.sh` | 35 of 35 CAUGHT: 30 red, 3 refused with exit 2, 2 controls. The clean copy was green and counted for landing first | 153 s |
| `scripts/ci-fast.sh` | green | 8 s (4.5 s with a warm build cache); target is under 300 s |
| `make test`, `make chaos`, `make bench` | each fails: `make test: not built yet`, `make chaos: not built yet`, `bench.sh: not built yet` | under 1 s |
| `scripts/site.sh /tmp/zgw-c00/site` | 18 pages: the 17 that existed before, unchanged in name, and `book/index.html`; `pages.txt` lists all 18 | 1 s |
| `cd tools/detlint && go vet ./... && go test ./...` | green, 7 packages with tests | about 7 s cold |

Other gate commands a later chunk uses:

```
scripts/gate.sh c01-sim-kernel                                   # build stage, baseline = merge base with main
GATE_BASE=<commit> scripts/gate.sh c01-sim-kernel                # name the baseline
GATE_STAGE=final GATE_PREPROSE=<commit> scripts/gate.sh c01-sim-kernel   # after the prose pass
GATE_ONLY=G1 scripts/gate.sh c01-sim-kernel                      # one gate; never counts
CANARY_ONLY=g3-failing-test,g1-wallclock scripts/gate-canaries.sh  # a few canaries; result not written
```

The canary list (ids as in `canaries.json`): `g0-gofmt`, `g0-compile-error`, `g0-vet`,
`g0-untidy-gomod`, `g0-tools-test`, `g1-wallclock`, `g1-detlist-typo`,
`g1-core-imports-wire`, `g1-engine-imports-adapter`, `g1-pgproto-outside`,
`g1-third-party`, `g1-second-require`, `g1-unbuffered-chan`, `g1-literal-chan-cap`,
`g1-unbounded-growth`, `g1-panic`, `g2-comment`, `g2-baseline-is-head` (exit 2),
`g2-prose-changed-code`, `g2-prose-only` (control), `g2-final-needs-preprose` (exit 2),
`g2-foundation-other-chunk` (exit 2), `g3-failing-test`, `g3-data-race`,
`g3-racelist-typo`, `g4-marker-deleted`, `g5-built-with-marker`, `g6-built-stage-fails`,
`g9-broken-link`, `g9-live-missing`, `g9-planned-exists`, `g10-extra-fails`,
`g10-extra-not-executable`, `dirty-tree-does-not-count` (control),
`partial-does-not-count` (control). All twelve the brief names are among them.

The harness itself was checked once by hand: with `g3_unit` weakened to ignore the test
result, `CANARY_ONLY=g3-failing-test` reported MISSED. The change was reverted.

## Decisions the brief left open

1. **Marker names.** One marker per skeleton, named after the script without `.sh`
   (`scripts/.not-built/sweep`, …), plus `make-test` and `make-chaos`. `make bench` runs
   `scripts/bench.sh`, so it shares the `bench` marker.
2. **Skeletons do not read their own marker.** A skeleton always prints `not built yet`
   and exits 3; the gate alone decides what that means. Exit 3 with the marker is
   `skip-not-built`; exit 3 without it is red; any other outcome with the marker still
   present is red too.
3. **Check statuses.** `pass`, `fail`, `skip-not-built`, `skip-foundation`, `skip-none`.
   `overall` is `pass` when nothing is `fail`. `result.json` also carries
   `skip_not_built` (the list), `only`, `short`, `sweep_seeds`, `fuzz_seconds`,
   `comment_baseline`, `preprose_commit` and `finished`.
4. **`counts_for_landing`** has the brief's conditions and four more that keep a weakened
   run from counting: no `GATE_SHORT`, no `GATE_OUT`, `SWEEP_SEEDS` at least 200,
   `FUZZ_SECONDS` at least 20.
5. **Dirty tree.** Decided when the gate starts, with untracked files counted. Paths under
   the chunk's own `gate/` folder are ignored, so `result.json` and `canaries.json` can be
   rewritten.
6. **`GATE_STAGE=foundation`** is refused (exit 2) for any chunk but `c00-foundation`.
7. **G2 at the final stage** compares the `GATE_PREPROSE` tree with the working tree.
   `GATE_PREPROSE` must be a strict ancestor of HEAD.
8. **G0 checks.** Seven rows: `tools` (build the lint tools), `gofmt`, `build`, `vet`,
   `tools-vet`, `tools-test`, `tidy` (root module and the nested one). An empty module is
   judged by looking for product `.go` files, not by the go tool's "no packages" message.
9. **detlint checks only the listed packages**, not the packages they import. The
   object-store gate added the import closure; here the collector (`internal/obs`) may
   need concurrency that detlint bans, so the list is explicit. A core that hides a clock
   in a helper package is caught only if the helper is listed.
10. **Package lists fail on typos.** A `det-packages.txt` line with no directory, or a
    `race-packages.txt` line matching no package, is red. `shell-packages.txt` is the
    exception: `internal/shell/...` is listed from the start and skipped until it exists.
11. **importlint rules file syntax**: `module`, `require`, `deny A : B`, `only B : A`,
    `!pattern` exclusions. A third-party import is legal only if an `only` rule names it.
    Core rules cover sub-packages (`internal/pool/...`) and test files.
12. **importlint also reports** a nested `go.mod` in the product tree and a directory named
    `tools`, `agent_work` or `vendor` below the top level. The copied tools skip those
    names at any depth, which would otherwise be a place to hide code.
13. **Test-client package name.** PLAN.md does not name it. `imports.txt` uses
    `internal/testclient/...` as a placeholder, with a comment telling the chunk that
    creates the test client to replace it.
14. **boundlint is type-checked** (go/types), so "named constant" and "config field" are
    decided from declarations, not spelling. "Config field" means a field of a struct type
    whose name contains `config`, or one declared in a package named `config`. "Unbounded
    loop" means a `for` with no condition or a `range` over a channel. Non-test files only.
15. **paniccheck** runs over `internal` and `cmd`.
16. **The book's last row** (PLAN.md section 15, "—", chunk 16) is manifest entry number 0,
    `book/chapters/00-preface.md`, listed first in the contents without a number.
17. **Book title** in the manifest is `mini-gateway: the book`, a placeholder for the
    prose seat like the chapter titles.
18. **Site extras.** Chapter pages get previous / contents / next links. The build also
    fails on a file in `book/chapters/` that the manifest does not list. The "Book" entry
    in the navigation is marked current on every book page.
19. **`scripts/site.sh`** was added so the gate can build the site on a machine without
    pip: it fetches the pinned Markdown 3.11.3 wheel (SHA-256 checked) into `.cache/pylib`
    inside the checkout, which is gitignored.
20. **`make lint`** runs gate check G1 alone and writes to `agent_work/ci/lint/`
    (gitignored). `scripts/ci-fast.sh` writes to `agent_work/ci/gate/`.
21. **Canary copy.** The throwaway copy is its own git repository (`main` = the tree,
    `canary` = one more commit with a clean product package), so the build-stage comment
    rule has a real baseline. Each canary run is limited with `GATE_ONLY` to the gate
    under test; the clean run and the dirty-tree control are full runs.
22. **G7** hands `csv-assert.sh` two folders the gate creates under its scratch folder,
    one for the sweep and one for tier R; both scripts receive theirs as `RUN_DIR`.

## Deviations

1. **boundlint's allow file names a function, not a line.** The brief says "unless the
   line is in an allow file". Line numbers move with every edit, so an entry is
   `file<TAB>Func<TAB>rule<TAB>justification`, the same shape as detlint's, and covers
   every finding of that rule in that function. `chan-cap` cannot be allowed at all.
2. **"Indirect lines only if `go mod tidy` produces them" is enforced by two checks, not
   by importlint alone.** importlint is standard library only and does not run the go
   tool: it accepts an `// indirect` requirement only when the allowed direct requirement
   is present. Whether tidy would write that line is decided by G0 `tidy`
   (`go mod tidy -diff`). Stated in `README-tools.txt` section 6.
3. **Shared skip list.** The brief asks for the skipped names in `nocomments`; the list
   lives in `internal/report.SkipDir`, which `astdiff` and `paniccheck` share, so all
   three now skip `agent_work`, `.git`, `testdata`, `vendor`, `tools`.
4. **CI has not run on GitHub.** The brief forbids pushing. `scripts/ci-fast.sh` is green
   locally and `ci.yml` parses as YAML; the first real run is the orchestrator's push.
   One thing to watch there: `actions/setup-go` reads `go 1.26` from `go.mod` and installs
   the newest 1.26 patch release, which may be newer than vm-1's 1.26.8.
5. **`importcheck` is kept but unused.** It was copied with its tests as the brief asks;
   the gate runs `importlint`, which covers the same ground with this project's rules.
6. **README.md and `docs/index.md` still say nothing is built.** Only the site footer was
   changed, as the brief asks; the status wording on those pages is the prose seat's.

## Left for the orchestrator

- Land with `LAND_NO_REVIEW=1 agent_work/orch/land.sh c00-foundation work/c00-foundation <message-file>`.
- After the push: the first CI run, and `agent_work/orch/pages-check.sh <sha> "" book/index.html`.
- STATE.md and PLAN.md were not edited (the brief forbids it).
