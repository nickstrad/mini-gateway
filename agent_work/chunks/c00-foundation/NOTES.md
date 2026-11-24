# Chunk 0 — Foundation: builder notes

Built by Fable on 2026-11-24 on branch `work/c00-foundation`, from the brief
`/root/briefs/zgateway-p2-c00-foundation.md`. No product logic. The first gated commit was
`7a19c14`; after the adversarial pass (87 held, 16 broken) one repair round followed, brief
`/root/briefs/zgateway-p2-c00-repair.md`, recorded under "Repair round" below. The gated
commit now is the `sha` in `gate/result.json`: the parent of the commit that adds that file.
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

All run on vm-1, Go 1.26.8. The first three rows are from the repair round, on the
committed tree; the other three are from `7a19c14` and were rerun unchanged in the repair
round where the brief asks for it (`ci-fast.sh`, the tools' tests).

| Command | Result | Time |
| --- | --- | --- |
| `GATE_STAGE=foundation scripts/gate.sh c00-foundation` | overall pass, `counts_for_landing` true, clean tree. 13 checks pass; G2 `skip-foundation`; G4 sweep, G5 fuzz, G6 tier-r, G7 csv, G8 gen-docs `skip-not-built`; G10 `skip-none` | 8 s |
| `scripts/gate-canaries.sh` | 51 of 51 CAUGHT: 42 red, 5 refused with exit 2, 4 controls. The clean copy was green and counted for landing first | 116 s |
| `scripts/ci-fast.sh` | green | 7 s; target is under 300 s |
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
`partial-does-not-count` (control). All twelve the brief names are among them. The repair
round added sixteen, listed under "Repair round".

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
5. **Dirty tree.** Decided when the gate starts, with untracked files counted. Since the
   repair round the one path ignored is the chunk's own `gate/result.json`; `gate/extra.sh`
   and `gate/canaries.json` count like any other file.
6. **`GATE_STAGE=foundation`** is refused (exit 2) for any chunk but `c00-foundation`, and
   since the repair round for `c00-foundation` too once the tree holds product Go.
7. **G2 at the final stage** compares the `GATE_PREPROSE` tree with the working tree
   (astdiff) and, since the repair round, holds every path changed between `GATE_PREPROSE`
   and HEAD to `tools/lint-rules/prose-paths.txt`. `GATE_PREPROSE` must be a strict
   ancestor of HEAD.
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
   lives in `internal/report.SkipDir`, which `astdiff` and `paniccheck` share, so they
   skip `agent_work`, `.git`, `testdata`, `vendor`, `tools`. Since the repair round
   `nocomments` and `importlint` read `testdata` folders (the tools' own fixtures are under
   `tools/`, which stays skipped).
4. **CI has not run on GitHub.** The brief forbids pushing. `scripts/ci-fast.sh` is green
   locally and `ci.yml` parses as YAML; the first real run is the orchestrator's push.
   One thing to watch there: `actions/setup-go` reads `go 1.26` from `go.mod` and installs
   the newest 1.26 patch release, which may be newer than vm-1's 1.26.8.
5. **`importcheck` is kept but unused.** It was copied with its tests as the brief asks;
   the gate runs `importlint`, which covers the same ground with this project's rules.
6. **README.md and `docs/index.md` still say nothing is built.** Only the site footer was
   changed, as the brief asks; the status wording on those pages is the prose seat's.

## Repair round (after the adversarial pass, 2026-11-24)

Brief: `/root/briefs/zgateway-p2-c00-repair.md`. Each of the 16 broken attacks was reproduced
first (16 of 16 BROKEN on `6e949d6`), then closed. The attack script rewrites its own
`RESULTS.md`, `evidence.json` and `STATE.md`, and the brief forbids edits under
`agent_work/adversarial/`, so every run of it in this round was made in a scratch copy of
the tree under `/tmp/zgw-c00-repair/`; the adversary's files in this branch are as they
were.

**The whole suite after the repair, on the tree of the docs commit (the parent of the commit
that records `canaries.json`; nothing the suite exercises changed after it): 102 held,
1 broken.**

| Attack | Outcome | Commit | Canary |
| --- | --- | --- | --- |
| `dirty-extra` | fixed: only `gate/result.json` is ignored by the dirty check, so an untracked or modified `extra.sh` makes the tree dirty and the result does not count | `a4a6657` | `uncommitted-extra-does-not-count` |
| `extra-dangling` | fixed: `extra.sh` that exists in any form must be a regular file, or G10 is red (a dangling link was `skip-none`) | `a4a6657` | `g10-extra-symlink` |
| `resurrect-marker` | fixed: a marker that a commit in the history of HEAD removed, or that the baseline commit does not have, is red; new row `G4 markers` | `a4a6657` | `g4-marker-restored`, `g4-marker-added` |
| `prose-testdata` | fixed: path allow-list at the final stage, `tools/lint-rules/prose-paths.txt` | `a4a6657` | `g2-prose-changed-testdata` |
| `prose-script` | fixed: the same allow-list | `a4a6657` | `g2-prose-changed-script`, `g2-prose-changed-generated`; control `g2-prose-only` now also changes `book/index.md` and `PROSE-NOTES.md` |
| `foundation-reuse` | fixed as the brief directs: `GATE_STAGE=foundation` is refused with exit 2 once the tree holds product Go. **The attack still reports BROKEN**, see below | `a4a6657` | `g2-foundation-with-product-go` |
| `env-stale` | fixed: every refusal goes through one function that removes `result.json` from the output folder | `a4a6657` | `stale-result-removed`; all five refusal canaries plant a stale result first |
| `comment-testdata` | fixed: `nocomments` reads `testdata` folders | `6f25a5a` | `g2-comment-testdata` |
| `import-reachable-testdata` | fixed: `importlint` reads `testdata` folders, and importing a package in one is the finding `testdata-import` | `bac0f41` | `g1-testdata-imports-wire`, `g1-testdata-imported` |
| `bound-alias` | fixed: growth through a pointer dereference, a package variable or a map not made locally counts | `c5eaf81` | `g1-growth-through-pointer` |
| `bound-helper` | fixed: calls to functions and methods of the same package are followed | `c5eaf81` | `g1-growth-in-helper` |
| `site-html-link` | fixed: `href` and `src` of every tag, any quote style | `6e18146` | `g9-broken-html-link`, `g9-broken-img-src` |
| `land-bypass` | fixed: `LAND_NO_REVIEW=1` is refused for any chunk but `c00-foundation` | `c0deb3b` | none: `land.sh` is not the gate's; held by the attack |
| `land-inconsistent-partial` | fixed: `partial` and `dirty_tree` must each be false | `c0deb3b` | none, as above |
| `land-wrong-chunk` | fixed: the result's `chunk` must be the chunk landed; stage `final`, or `foundation` for c00 only | `c0deb3b` | none, as above |
| `land-extra-after` | fixed: exact list of paths that may change after the gated commit | `c0deb3b` | none, as above |

**The one attack that still reports BROKEN, for the adversary's judgement.**
`foundation-reuse` commits product Go with a comment, runs
`GATE_STAGE=foundation scripts/gate.sh c00-foundation`, and expects exit 1 with `G2 comments`
red. The gate now exits 2 before any check, writes no result and removes an earlier one:
`gate: GATE_STAGE=foundation is refused: the tree holds product Go (a .go file outside tools/
and agent_work/), so this is not chunk 0 any more: ./internal/probe/comment.go
./internal/probe/probe.go`. The claim under attack ("later chunks cannot bypass G2 with
foundation stage") holds; the expectation in `attack.sh` (`red(o,'G2','comments','comment')`)
does not match a refusal. The script was not edited.

Three more points where an attack holds for a reason worth knowing:

- `land.sh` checks the stage last. Every landing fixture in `attack.sh` carries
  `"stage":"build"`, which now never lands, so with the stage checked first all 14 landing
  attacks would be refused with the stage message and those that look for their own message
  would report BROKEN. Checked last, each fault is named first; the order changes nothing
  about what can land.
- `dirty-extra` expects exit 0 with `dirty_tree` true. See deviation 1 below.
- `resurrect-marker` restores the marker inside one branch, where the baseline commit
  still has it. The baseline comparison the brief describes does not catch that; the
  history check does.

Checks after the repair: `scripts/gate-canaries.sh` 51 of 51 caught (116 s);
`scripts/ci-fast.sh` green (7 s); `cd tools/detlint && go vet ./... && go test ./...` green;
the site output for the current pages is byte-identical before and after the link change.

New canaries (16): `g1-growth-through-pointer`, `g1-growth-in-helper`,
`g1-testdata-imports-wire`, `g1-testdata-imported`, `g2-prose-changed-script`,
`g2-prose-changed-testdata`, `g2-prose-changed-generated`, `g2-comment-testdata`,
`g2-foundation-with-product-go` (exit 2), `stale-result-removed` (exit 2),
`g4-marker-restored`, `g4-marker-added`, `g9-broken-html-link`, `g9-broken-img-src`,
`g10-extra-symlink`, `uncommitted-extra-does-not-count` (control).

### Decisions and deviations of the repair round

1. **An untracked or modified `extra.sh` makes the tree dirty; it does not make G10 red.**
   The brief says both ("regular, tracked, committed, executable, or G10 is red" and "an
   untracked or modified one makes the tree dirty"). Implemented: not a regular file, or
   not executable, is red; untracked or modified, it runs, G10 carries a note, and the
   result does not count. Reason: the adversary's `dirty-extra` expects a green exit with
   `dirty_tree` true, a builder can then try an `extra.sh` before committing it, and
   landing is equally closed either way (`counts_for_landing` false; `land.sh` refuses an
   `extra.sh` changed after the gated commit). If red is wanted, it is one line in
   `g10_extra` in `scripts/gate.sh`.
2. **`canaries.json` is committed before the gate run, not with `result.json`.** The brief
   asks for both in the last commit, and also has `land.sh` refuse anything but
   `result.json` and Markdown after the gated commit. Both cannot hold, so the canaries
   were run on the clean tree of the docs commit, `canaries.json` was committed alone,
   the gate ran on that commit, and the last commit holds `result.json` only.
3. **The prose allow-list also names `agent_work/chunks/<chunk>/gate/result.json`.** The
   brief's list does not. Without it a final-stage run could not be repeated once its
   result was committed (the file would be a changed path outside the list).
4. **The allow-list is read from the `GATE_PREPROSE` commit**, not from the tree, so a
   prose pass that edits `prose-paths.txt` is judged by the rules it found.
5. **Generated contract documents are named in `prose-paths.txt` from PLAN.md**:
   `error-contract`, `timeout-budget`, `capacity-table`, `csv-schema`, `fleet-guarantees`,
   and `reject-list` (PLAN.md gives that one no file name; the name is a guess to be
   corrected by chunk 5). A chunk that adds another adds its `deny` line.
6. **Markers: two rules, not one.** Besides the baseline comparison the brief describes,
   a marker is red when any commit in the history of HEAD removed it. At the foundation
   stage only the history rule applies, because chunk 0 adds every marker and the baseline
   has none.
7. **boundlint went further than the two attacks**: package variables and maps that are
   not provably local are targets too, since the same alias can be made without a pointer
   (`m := b.seen`). The channel direction in the brief (type aliases, a `make` in a helper)
   already held; fixtures were added to show it (`chans.go`, lines 53 to 64).
8. **`land.sh`** refuses `LAND_NO_REVIEW=1` for another chunk (exit 2) instead of ignoring
   it, and has no canary in `gate-canaries.sh`, which tests the gate only.

### Narrowed claims

Claims that are now stated with their limit, in the same words as
`docs/knowledge/foundation-gates.md` and `tools/detlint/README-tools.txt`:

1. **Bounded growth (boundlint, `unbounded-growth`).** Covered: append and map insertion,
   in a `for { }` or a `range` over a channel, into a struct field, through any pointer
   dereference, into a package variable or a map the function did not make itself,
   directly or through calls to functions and methods of the same package. Not seen, in
   principle: a call through an interface or a function value; a call into another
   package (the standard library included); growth that is neither append nor a map
   insertion (`bytes.Buffer`, a list, re-slicing); a slice element; a local slice declared
   outside the loop; loops with a condition. The length check is only required to exist
   in the function, not proved to guard the insertion. Full list: `README-tools.txt`
   section 7.
2. **Code under `testdata` (importlint).** Nothing may import a package in a `testdata`
   folder, and Go files there obey the import rules. importlint reads import declarations
   only: a program kept in `testdata` and run by a script or a `go:generate` line is not
   seen.
3. **Comments in `testdata`.** Checked like product Go. A fixture that must not parse
   cannot be named `.go` (it would be a `parse` finding).
4. **Markers may only be removed.** The history rule reads the history git has. If a
   branch is squashed so the removing commit is gone, the restored marker is caught only
   by the baseline rule, that is, once the removal is on `main`. A deliberate revert of a
   built stage is red by design and needs a human decision.
5. **A refused run leaves no result.** True for every refusal except a missing or
   malformed chunk name, where there is no output folder to clean.
6. **Site links.** Every `href` and `src` attribute of every tag is checked. Not read:
   `srcset`, URLs in CSS, links a script builds.
7. **`result.json`** stays what the gate header says: evidence for an honest operator, not
   a certificate. `land.sh` binds it to the chunk, the stage and the branch tip, but a
   forged file with the right fields is not detected by anything in this repository.

## Left for the orchestrator

- `main` moved while this chunk was built (it is at `90e417c`; this branch starts from `72dc559`, which is not on `main`). `land.sh` wants the branch rebased on `main`; a rebase changes the commit ids, so rerun `GATE_STAGE=foundation scripts/gate.sh c00-foundation` on the rebased tip (8 s) and commit the new `result.json`. The builder did not rebase: the brief says not to merge.
- Land with `LAND_NO_REVIEW=1 agent_work/orch/land.sh c00-foundation work/c00-foundation <message-file>`. Since the repair round `land.sh` refuses a branch where anything but `gate/result.json`, the chunk's `.md` files, `agent_work/INDEX.md`, `STATE.md` and `docs/knowledge/*.md` changed after the gated commit. So after the rebase: run the gate, commit `result.json` alone, and add nothing else after it (an `ADVERSARIAL.md` in the chunk folder is fine; a regenerated `agent_work/adversarial/` or `canaries.json` is not, commit those before the gate run).
- The adversary's `RESULTS.md` and `evidence.json` in this branch still show the first pass (87 held, 16 broken); the repair brief forbids editing them. To refresh them run `agent_work/adversarial/c00-foundation/attack.sh` from the repository root (about 5 minutes; expect 102 held, 1 broken) and commit the result before the gate run.
- After the push: the first CI run, and `agent_work/orch/pages-check.sh <sha> "" book/index.html`.
- STATE.md and PLAN.md were not edited (the brief forbids it).
