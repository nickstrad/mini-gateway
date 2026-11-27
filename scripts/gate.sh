#!/bin/bash
# The local landing gate: checks G0-G10 for one chunk, on the host, with the host Go
# toolchain. This script is the landing authority (PLAN.md sections 6.3 and 12.3); CI runs
# only the fast subset (scripts/ci-fast.sh).
#
# Usage: scripts/gate.sh <chunk-dir>
#   <chunk-dir> is the chunk's folder name under agent_work/chunks/, for example
#   c01-sim-kernel. Writes agent_work/chunks/<chunk-dir>/gate/result.json and one log per
#   check beside it (the logs are gitignored), and prints one line per check.
#
# Environment:
#   GATE_STAGE=build       (default) G2: product Go has no comments beyond those already in
#                          the baseline commit, Go files in testdata folders included. The
#                          builder writes no comments; the prose seat writes them later.
#   GATE_BASE=<commit>     the baseline of that rule and of the marker rule below (default:
#                          the merge base with main). For G2 it must be a strict ancestor of
#                          HEAD; a baseline equal to HEAD is a setup error (exit 2), never a
#                          pass.
#   GATE_STAGE=final       after the prose pass. Needs GATE_PREPROSE=<commit>, the last
#                          commit before the prose pass. G2 proves two things: with astdiff,
#                          that no Go code changed between that commit and this tree; and
#                          that every path of `git diff --name-only GATE_PREPROSE HEAD` is one
#                          the prose pass may change (tools/lint-rules/prose-paths.txt, read
#                          from the GATE_PREPROSE commit).
#   GATE_STAGE=foundation  G2 is skipped. Accepted for the chunk c00-foundation only, and
#                          only while the tree holds no product Go: with any .go file
#                          outside tools/ and agent_work/ the run is refused (exit 2).
#   GATE_ONLY=G0,G3        run a subset; the result is marked partial and never counts.
#   GATE_SHORT=1           G3 runs `go test -short` without agent_work/adversarial/... (the CI
#                          set: attack harnesses need vm-1 tools); never counts.
#   GATE_OUT=<dir>         write the result somewhere else; never counts.
#   SWEEP_SEEDS=200        G4 seed budget per scenario.
#   FUZZ_SECONDS=20        G5 time per fuzz target.
#   FUZZ_RATE_FLOOR=100    G5 rate floor of a simulation-level target (scripts/fuzz.sh); a
#                          floor under 100 never counts.
#
# The checks, in order:
#   G0  build     tools build; gofmt; go build; go vet; the nested module tools/detlint
#                 (vet and tests); go mod tidy -diff
#   G1  lints     detlint (tools/lint-rules/det-packages.txt), importlint, boundlint,
#                 paniccheck
#   G2  comments  see GATE_STAGE
#   G3  tests     go test -count=1 ./... ; scripts/counted-growth.sh (the growth tests under
#                 -tags clscount) ; go test -race for tools/lint-rules/race-packages.txt
#   G4  markers   every scripts/.not-built/ marker is legitimate (see below)
#   G4  sweep     scripts/sweep.sh
#   G5  fuzz      scripts/fuzz.sh, with its rate table written to gate/G5-fuzz.tsv; red
#                 when fuzz.sh passes without writing the table. result.json records
#                 fuzz_targets and fuzz_seed_sweeps from the table (null when G5 did not
#                 run). A seed sweep is reported, not failed.
#   G6  tier-r    scripts/tier-r.sh
#   G7  csv       scripts/csv-assert.sh over the run directories G4 and G6 wrote
#   G8  gen-docs  scripts/gen-docs.sh --check
#   G9  site      scripts/site.sh into a scratch folder
#   G10 extra     agent_work/chunks/<chunk-dir>/gate/extra.sh, if the chunk has one. It
#                 must be a regular executable file (a symbolic link, dangling or not, is
#                 red, not skipped). Untracked or modified, it makes the tree dirty.
#
# Stages that are not built yet. G4-G8 run a script that starts life as a skeleton exiting
# 3 with "not built yet". That outcome is recorded as skip-not-built ONLY while the marker
# file scripts/.not-built/<name> exists. With the marker gone the same outcome is red; and
# while the marker exists any other outcome is red too (delete the marker when you build
# the script). So a stage cannot be skipped silently once it exists.
# A marker may only ever be removed. A marker in the tree is red (its own stage, and the
# G4 markers row for every marker) when a commit in the history of HEAD removed it, or
# when it is absent at the baseline commit (GATE_BASE, default the merge base with main).
# The baseline half is not applied at the foundation stage: chunk 0 creates the markers.
#
# Fail closed: a check that cannot run is red. Check statuses: pass, fail, skip-not-built,
# skip-foundation (G2 at the foundation stage), skip-none (G10 when the chunk has no
# extra.sh). overall is "pass" when no check is "fail".
#
# counts_for_landing is true only when ALL of these hold: every check ran (no GATE_ONLY),
# overall is pass, the tree was clean when the gate started (the chunk's own
# gate/result.json is the one path that does not count), HEAD is a full commit id, the
# stage is valid, and none of GATE_SHORT, GATE_OUT, a SWEEP_SEEDS under 200 or a
# FUZZ_SECONDS under 20 or a FUZZ_RATE_FLOOR under 100 was used. The
# file is evidence for an honest operator, not a tamper-proof certificate;
# agent_work/orch/land.sh binds it to the branch tip.
#
# Exit status: 0 every requested check green (or skipped as described), 1 a check is red,
# 2 usage or setup error. A run that is refused (exit 2) first removes any result.json
# already in its output folder, and a run that starts removes it before the first check,
# so the file never describes anything but the last run that finished.
set -u
set -o pipefail

chunk=${1:-}
[ $# = 1 ] && [[ "$chunk" =~ ^[a-z0-9][a-z0-9-]*$ ]] || { echo "usage: scripts/gate.sh <chunk-dir>   (for example c01-sim-kernel)" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"
export PATH
out_default="agent_work/chunks/$chunk/gate"
out=${GATE_OUT:-$out_default}
custom_out=false; [ -z "${GATE_OUT:-}" ] || custom_out=true
# die <message>: refuse to run. An earlier result.json in the output folder is removed first,
# so a refused run can never leave an older result looking like the answer to this one.
die() { echo "gate: $*" >&2; rm -f "$out/result.json"; exit 2; }

for tool in go gofmt jq git; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
done
unset GOFLAGS GOWORK
export GOFLAGS=-buildvcs=false

stage=${GATE_STAGE:-build}
case "$stage" in
  build|final) ;;
  foundation)
    [ "$chunk" = c00-foundation ] || die "GATE_STAGE=foundation is for the chunk c00-foundation only"
    product=$(find . -name '*.go' -not -path './tools/*' -not -path './agent_work/*' -not -path './.git/*' \
      -not -path './_site/*' -not -path './.cache/*' | sort | head -n 5)
    [ -z "$product" ] || die "GATE_STAGE=foundation is refused: the tree holds product Go (a .go file outside tools/ and agent_work/), so this is not chunk 0 any more: $(tr '\n' ' ' <<<"$product")" ;;
  *) die "GATE_STAGE must be build, final or foundation, got '$stage'" ;;
esac
only=${GATE_ONLY:-}
[[ "$only" =~ ^(G(10|[0-9])(,G(10|[0-9]))*)?$ ]] || die "GATE_ONLY must look like G0,G3 (G0 to G10), got '$only'"
want() { [ -z "$only" ] || [[ ",$only," == *",$1,"* ]]; }
sweep_seeds=${SWEEP_SEEDS:-200}
fuzz_seconds=${FUZZ_SECONDS:-20}
fuzz_floor=${FUZZ_RATE_FLOOR:-100}
[[ "$sweep_seeds" =~ ^[1-9][0-9]*$ ]] || die "SWEEP_SEEDS must be a positive whole number"
[[ "$fuzz_seconds" =~ ^[1-9][0-9]*$ ]] || die "FUZZ_SECONDS must be a positive whole number"
[[ "$fuzz_floor" =~ ^[1-9][0-9]*$ ]] || die "FUZZ_RATE_FLOOR must be a positive whole number"
short=false; short_flag=()
if [ "${GATE_SHORT:-}" = 1 ]; then short=true; short_flag=(-short); fi

sha=$(git rev-parse --verify -q 'HEAD^{commit}' 2>/dev/null || true)
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || sha=unknown
# The tree is dirty when git status lists anything but the gate's own result file. The
# chunk's gate/extra.sh is an input like any other: untracked or modified, it makes the
# tree dirty.
dirty=true
if status=$(git status --porcelain --untracked-files=all 2>/dev/null); then
  [ -n "$(grep -vE "^.. $out_default/result\.json\$" <<<"$status" || true)" ] || dirty=false
fi

# The baseline commit: GATE_BASE, or the merge base with main. The comment rule (G2, build
# stage) and the marker rule (G4-G8) compare the tree with it.
base=${GATE_BASE:-}
if [ -z "$base" ] && [ "$sha" != unknown ]; then
  for ref in main origin/main; do
    if git rev-parse -q --verify "$ref^{commit}" >/dev/null 2>&1; then base=$(git merge-base HEAD "$ref" 2>/dev/null || true); break; fi
  done
fi
marker_base=none
[ -z "$base" ] || marker_base=$(git rev-parse --verify -q "$base^{commit}" 2>/dev/null || echo none)

scratch=$(mktemp -d /tmp/gate.XXXXXX) || die "cannot create a scratch folder under /tmp"
trap 'rm -rf "$scratch"' EXIT
bin="$scratch/bin"; runs="$scratch/runs"
mkdir -p "$bin" "$runs/sweep" "$runs/tier-r"

# ---- G2 setup: done before anything is written, so a setup error leaves no result behind.
base_sha=none; preprose_sha=none
if want G2 && [ "$stage" = build ]; then
  [ "$sha" != unknown ] || die "a build-stage run needs git history for its comment baseline"
  [ -n "$base" ] || die "no comment baseline: there is no main branch to compare with; set GATE_BASE=<commit>"
  base_sha=$(git rev-parse --verify -q "$base^{commit}") || die "GATE_BASE '$base' is not a commit"
  if [ "$base_sha" = "$sha" ]; then
    echo "gate: HEAD is its own comment baseline (this is main, or a branch with no commits of its own)." >&2
    echo "gate: a build-stage run judges a chunk branch against the commit it started from; name an" >&2
    die "earlier commit with GATE_BASE, or use GATE_STAGE=final with GATE_PREPROSE after the prose pass."
  fi
  git merge-base --is-ancestor "$base_sha" "$sha" || die "baseline $base_sha is not an ancestor of HEAD"
  mkdir "$scratch/baseline"
  git archive "$base_sha" | tar -x -C "$scratch/baseline" || die "cannot export the baseline $base_sha"
fi
if want G2 && [ "$stage" = final ]; then
  [ -n "${GATE_PREPROSE:-}" ] || die "GATE_STAGE=final needs GATE_PREPROSE=<the last commit before the prose pass>"
  [ "$sha" != unknown ] || die "a final-stage run needs git history"
  preprose_sha=$(git rev-parse --verify -q "$GATE_PREPROSE^{commit}") || die "GATE_PREPROSE '$GATE_PREPROSE' is not a commit"
  [ "$preprose_sha" != "$sha" ] || die "GATE_PREPROSE is HEAD itself; nothing can be certified against itself"
  git merge-base --is-ancestor "$preprose_sha" "$sha" || die "GATE_PREPROSE $preprose_sha is not an ancestor of HEAD"
  mkdir "$scratch/preprose"
  git archive "$preprose_sha" | tar -x -C "$scratch/preprose" || die "cannot export $preprose_sha"
fi

mkdir -p "$out" || die "cannot create $out"
rm -f "$out"/G*.log "$out"/G*.tsv "$out/result.json"
started=$(date -u +%FT%TZ)
t_start=$SECONDS
checks_json='[]'
overall=pass

# record <gate> <name> <status> <seconds> <note>
record() {
  printf '%-3s %-12s %-15s %4ss %s\n' "$1" "$2" "$3" "$4" "$5"
  if [ "$3" = fail ]; then overall=fail; tail -n 15 "$out/$1-$2.log" 2>/dev/null | sed 's/^/      | /'; fi
  checks_json=$(jq -c --arg g "$1" --arg n "$2" --arg s "$3" --argjson d "$4" --arg note "$5" \
    '. + [{gate:$g, check:$n, status:$s, seconds:$d, note:$note}]' <<<"$checks_json")
}
# check <gate> <name> <command...>: run one check, log it, record pass or fail. A line
# "NOTE: text" in the log becomes the check's note.
check() {
  local gate=$1 name=$2; shift 2
  local log="$out/$gate-$name.log" t0=$SECONDS status=pass
  "$@" >"$log" 2>&1 || status=fail
  record "$gate" "$name" "$status" "$((SECONDS - t0))" "$(grep -m1 '^NOTE: ' "$log" | cut -c7- || true)"
}
# marker_problem <marker-path>: a not-built marker may only ever be removed. Prints the reason
# and returns 0 when the marker is in the tree but must not be:
#   - a commit in the history of HEAD removed it (it was built once, and is back); or
#   - it is absent at the baseline commit (GATE_BASE, default the merge base with main), so
#     this branch added it. Not applied at the foundation stage: chunk 0 creates the markers.
# Returns 1 when the marker is absent or legitimate.
marker_problem() {
  local m=$1 c
  [ -e "$m" ] || [ -L "$m" ] || return 1
  [ ! -L "$m" ] && [ -f "$m" ] || { echo "$m is not a regular file"; return 0; }
  [ "$sha" != unknown ] || { echo "$m: there is no git history, so it cannot be shown that this marker was never removed"; return 0; }
  c=$(git log -1 --format=%h --full-history -m --diff-filter=D HEAD -- "$m" 2>/dev/null) || { echo "$m: cannot read the git history"; return 0; }
  [ -z "$c" ] || { echo "$m was removed in commit $c and is back: a not-built marker may only ever be removed"; return 0; }
  [ "$stage" != foundation ] || return 1
  [ "$marker_base" != none ] || { echo "$m: there is no baseline commit to compare the markers with (no main branch; set GATE_BASE=<commit>)"; return 0; }
  git cat-file -e "$marker_base:$m" 2>/dev/null || { echo "$m is absent at the baseline commit $marker_base and present now: a not-built marker may only ever be removed"; return 0; }
  return 1
}
# g5_fuzz: scripts/fuzz.sh with its rate table beside the logs. FUZZ_GO (the test seam of
# fuzz.sh) is never passed on. A pass without a table is red: the counts would be missing.
fuzz_report="$out/G5-fuzz.tsv"
g5_fuzz() {
  local rc=0
  env -u FUZZ_GO FUZZ_SECONDS="$fuzz_seconds" FUZZ_RATE_FLOOR="$fuzz_floor" FUZZ_REPORT="$fuzz_report" scripts/fuzz.sh || rc=$?
  if [ $rc = 0 ] && [ ! -s "$fuzz_report" ]; then echo "GATE: fuzz.sh passed but wrote no rate table to $fuzz_report"; rc=1; fi
  return $rc
}
# g4_markers: every file in scripts/.not-built/ is judged, also the markers of scripts the
# gate does not run.
g4_markers() {
  local m rc=0 n=0
  for m in scripts/.not-built/* scripts/.not-built/.[!.]*; do
    [ -e "$m" ] || [ -L "$m" ] || continue
    n=$((n + 1))
    if marker_problem "$m"; then rc=1; fi
  done
  echo "NOTE: $n not-built markers, compared with baseline $marker_base and the history of HEAD"
  [ $rc = 0 ] || echo "GATE: a marker listed above was added or restored"
  return $rc
}
# stage_check <gate> <name> <marker> <command...>: a check that may not be built yet.
stage_check() {
  local gate=$1 name=$2 marker="scripts/.not-built/$3"; shift 3
  local log="$out/$gate-$name.log" t0=$SECONDS status note='' rc=0 why
  "$@" >"$log" 2>&1 || rc=$?
  if why=$(marker_problem "$marker"); then
    status=fail; note="its not-built marker was added or restored"
    echo "GATE: $why" >>"$log"
  elif [ $rc = 3 ] && grep -q 'not built yet' "$log"; then
    if [ -e "$marker" ]; then status=skip-not-built; note="not built yet (marker $marker)"
    else status=fail; note="says not built yet but its marker is gone"; echo "GATE: the script says \"not built yet\" but $marker does not exist: a stage that exists cannot be skipped" >>"$log"; fi
  elif [ -e "$marker" ]; then
    status=fail; note="ran (exit $rc) while its not-built marker exists"
    echo "GATE: the script ran (exit $rc) while $marker still exists; delete the marker in the commit that builds the script" >>"$log"
  elif [ $rc = 0 ]; then status=pass; note=$(grep -m1 '^NOTE: ' "$log" | cut -c7- || true)
  else status=fail; note="exit $rc"; fi
  record "$gate" "$name" "$status" "$((SECONDS - t0))" "$note"
}

# Product Go: every .go file outside tools/, agent_work/ and the scratch folders.
product_go_files() {
  find . -name '*.go' -not -path './tools/*' -not -path './agent_work/*' -not -path './.git/*' \
    -not -path './_site/*' -not -path './.cache/*' -not -path '*/testdata/*' | sort
}
# list_file <file>: the lines of a package list, without comments and blank lines.
list_file() { [ -f "$1" ] || { echo "missing list file $1" >&2; return 1; }; grep -vE '^[[:space:]]*(#|$)' "$1" || true; }
# An empty module is green: with no product Go there is nothing to build, vet or test.
no_go() { [ -z "$(product_go_files)" ] && echo "NOTE: no Go packages in the module yet"; }
tool() { [ -x "$bin/$1" ] || { echo "the tool $1 was not built (see G0 tools)"; return 1; }; }

# ---- G0 build
g0_gofmt() {
  local o files; files=$(product_go_files)
  o=$( { [ -z "$files" ] || gofmt -l $files; gofmt -l tools/detlint; } 2>&1 ); echo "$o"
  [ -z "$o" ] || { echo "gofmt: the files above are not formatted (or do not parse)"; return 1; }
  [ -n "$files" ] || echo "NOTE: no Go packages in the module yet (tools/detlint is formatted)"
}
g0_build() { no_go || go build ./...; }
g0_vet()   { no_go || go vet ./...; }
g0_toolsvet()  { (cd tools/detlint && go vet ./...); }
g0_toolstest() { (cd tools/detlint && go test -count=1 ./...); }
g0_tidy() {
  go mod tidy -diff || { echo "go.mod or go.sum is not tidy (run go mod tidy)"; return 1; }
  (cd tools/detlint && go mod tidy -diff) || { echo "tools/detlint/go.mod is not tidy"; return 1; }
}

# ---- G1 lints
g1_detlint() {
  tool detlint || return 1
  local pkgs p rc=0
  pkgs=$(list_file tools/lint-rules/det-packages.txt) || return 1
  if [ -z "$pkgs" ]; then
    [ -z "$(list_file tools/lint-rules/detlint-allow.txt)" ] || { echo "detlint-allow.txt has entries but det-packages.txt lists no package"; return 1; }
    echo "NOTE: det-packages.txt lists no package yet"; return 0
  fi
  for p in $pkgs; do [ -d "${p%/...}" ] || { echo "det-packages.txt: no such directory: ${p%/...}"; rc=1; }; done
  [ $rc = 0 ] || return 1
  echo "NOTE: $(wc -w <<<"$pkgs") package patterns"
  "$bin/detlint" -root "$root" -allow tools/lint-rules/detlint-allow.txt $pkgs
}
g1_importlint() { tool importlint && "$bin/importlint" -root "$root" -rules tools/lint-rules/imports.txt; }
g1_boundlint() {
  tool boundlint || return 1
  local pkgs; pkgs=$(list_file tools/lint-rules/shell-packages.txt) || return 1
  [ -n "$pkgs" ] || { echo "shell-packages.txt lists no package"; return 1; }
  "$bin/boundlint" -root "$root" -allow tools/lint-rules/boundlint-allow.txt $pkgs
}
g1_paniccheck() { tool paniccheck && "$bin/paniccheck" -root "$root" -allow tools/lint-rules/panic-allow.txt internal cmd; }

# ---- G2 comments
g2_comments() {
  tool nocomments || return 1
  echo "NOTE: comments compared with baseline $base_sha"
  "$bin/nocomments" -root "$root" -baseline "$scratch/baseline"
}
# The prose pass may change only the paths of tools/lint-rules/prose-paths.txt. The rules
# are read from the pre-prose commit, so the prose pass cannot widen them for itself.
g2_prose_paths() {
  local rules="$scratch/preprose/tools/lint-rules/prose-paths.txt" kind re p ok bad=0 n=0
  local allow=() deny=()
  [ -f "$rules" ] || { echo "tools/lint-rules/prose-paths.txt is missing at the pre-prose commit $preprose_sha"; return 1; }
  while read -r kind re; do
    case "$kind" in
      ''|'#'*) ;;
      allow) [ -n "$re" ] || { echo "prose-paths.txt: an allow line with no pattern"; return 1; }; allow+=("${re//\{chunk\}/$chunk}") ;;
      deny)  [ -n "$re" ] || { echo "prose-paths.txt: a deny line with no pattern"; return 1; }; deny+=("${re//\{chunk\}/$chunk}") ;;
      *) echo "prose-paths.txt: cannot read the line: $kind $re"; return 1 ;;
    esac
  done <"$rules"
  while IFS= read -r -d '' p; do
    n=$((n + 1)); ok=false
    for re in ${allow[@]+"${allow[@]}"}; do [[ "$p" =~ $re ]] && ok=true; done
    for re in ${deny[@]+"${deny[@]}"}; do [[ "$p" =~ $re ]] && ok=false; done
    [ "$ok" = true ] || { echo "$p:1:1: prose-path: changed between GATE_PREPROSE and HEAD, and the prose pass may not change this path (tools/lint-rules/prose-paths.txt)"; bad=1; }
  done < <(git diff -z --name-only --no-renames "$preprose_sha" HEAD || printf 'git diff failed\0')
  echo "$n paths changed between $preprose_sha and HEAD"
  return $bad
}
g2_astdiff() {
  tool astdiff || return 1
  echo "NOTE: code compared with pre-prose commit $preprose_sha"
  local rc=0
  "$bin/astdiff" "$scratch/preprose" "$root" || rc=1
  g2_prose_paths || rc=1
  [ $rc = 0 ] || echo "G2 astdiff: the prose pass changed more than comments and prose (findings above)"
  return $rc
}

# ---- G3 tests
g3_unit() {
  no_go && return 0
  [ "$short" = false ] || echo "NOTE: short run (go test -short); does not count for landing"
  if [ "$short" = true ]; then
    echo "NOTE: the CI set leaves out agent_work/adversarial/... (attack harnesses use vm-1 tools: DuckDB, Docker, the host Go path); the landing gate runs them"
    go test -count=1 -timeout 30m "${short_flag[@]}" $(go list ./... | grep -v '/agent_work/adversarial/')
  else
    go test -count=1 -timeout 30m ./...
  fi
}
g3_counted() {
  no_go && return 0
  scripts/counted-growth.sh
}
g3_race() {
  local pkgs p; pkgs=$(list_file tools/lint-rules/race-packages.txt) || return 1
  [ -n "$pkgs" ] || { echo "NOTE: race-packages.txt lists no package yet"; return 0; }
  for p in $pkgs; do
    [ -n "$(go list "$p" 2>/dev/null)" ] || { echo "race-packages.txt: $p matches no package"; return 1; }
  done
  echo "NOTE: $(wc -w <<<"$pkgs") package patterns under the race detector"
  go test -count=1 -timeout 45m -race "${short_flag[@]}" $pkgs
}

# ---- G9 site, G10 chunk extras
g9_site() { scripts/site.sh "$scratch/site" && [ -s "$scratch/site/book/index.html" ] && [ -s "$scratch/site/pages.txt" ]; }
extra="$out_default/extra.sh"
g10_extra() {
  [ ! -L "$extra" ] && [ -f "$extra" ] || { echo "$extra exists but is not a regular file (a symbolic link or a folder cannot be the chunk's extra check)"; return 1; }
  [ -x "$extra" ] || { echo "$extra exists but is not executable"; return 1; }
  if ! git ls-files --error-unmatch -- "$extra" >/dev/null 2>&1 || ! git diff --quiet HEAD -- "$extra" 2>/dev/null; then
    echo "NOTE: gate/extra.sh is not committed as it stands: the tree is dirty and this run does not count"
  fi
  CHUNK="$chunk" RUN_DIR="$runs" "$extra"
}

echo "gate: chunk=$chunk sha=$sha stage=$stage dirty_tree=$dirty only=${only:-all}"
t0=$SECONDS
tools_status=pass
(cd tools/detlint && go build -o "$bin/" ./...) >"$out/G0-tools.log" 2>&1 || tools_status=fail
ls "$bin" >>"$out/G0-tools.log" 2>&1
if want G0; then
  record G0 tools "$tools_status" "$((SECONDS - t0))" ""
  check G0 gofmt g0_gofmt
  check G0 build g0_build
  check G0 vet g0_vet
  check G0 tools-vet g0_toolsvet
  check G0 tools-test g0_toolstest
  check G0 tidy g0_tidy
fi
if want G1; then
  check G1 detlint g1_detlint
  check G1 importlint g1_importlint
  check G1 boundlint g1_boundlint
  check G1 paniccheck g1_paniccheck
fi
if want G2; then
  case "$stage" in
    build) check G2 comments g2_comments ;;
    final) check G2 astdiff g2_astdiff ;;
    foundation) record G2 comments skip-foundation 0 "stage foundation: chunk 0 has no product Go" ;;
  esac
fi
if want G3; then
  check G3 unit g3_unit
  check G3 counted g3_counted
  check G3 race g3_race
fi
want G4 && check G4 markers g4_markers
want G4 && stage_check G4 sweep sweep env SWEEP_SEEDS="$sweep_seeds" RUN_DIR="$runs/sweep" scripts/sweep.sh
want G5 && stage_check G5 fuzz fuzz g5_fuzz
fuzz_targets=null; fuzz_sweeps=null
if want G5 && [ -s "$fuzz_report" ]; then
  fuzz_targets=$(tail -n +2 "$fuzz_report" | grep -c . || true)
  fuzz_sweeps=$(tail -n +2 "$fuzz_report" | awk -F '\t' '$5 == "seed-sweep"' | grep -c . || true)
fi
want G6 && stage_check G6 tier-r tier-r env RUN_DIR="$runs/tier-r" scripts/tier-r.sh
want G7 && stage_check G7 csv csv-assert scripts/csv-assert.sh "$runs/sweep" "$runs/tier-r"
want G8 && stage_check G8 gen-docs gen-docs scripts/gen-docs.sh --check
want G9 && check G9 site g9_site
if want G10; then
  if [ -e "$extra" ] || [ -L "$extra" ]; then check G10 extra g10_extra
  else record G10 extra skip-none 0 "the chunk has no gate/extra.sh"; fi
fi

partial=false; [ -z "$only" ] || partial=true
counts=false
if [ "$overall" = pass ] && [ "$partial" = false ] && [ "$dirty" = false ] && [ "$sha" != unknown ] \
   && [ "$short" = false ] && [ "$custom_out" = false ] && [ "$sweep_seeds" -ge 200 ] && [ "$fuzz_seconds" -ge 20 ] \
   && [ "$fuzz_floor" -ge 100 ]; then counts=true; fi
jq -n --arg chunk "$chunk" --arg sha "$sha" --arg stage "$stage" --arg started "$started" \
  --arg finished "$(date -u +%FT%TZ)" --argjson seconds "$((SECONDS - t_start))" \
  --arg go "$(go version 2>&1)" --arg duckdb "$(duckdb --version 2>/dev/null | head -n1 || true)" \
  --arg overall "$overall" --argjson partial "$partial" --argjson dirty "$dirty" --argjson counts "$counts" \
  --argjson short "$short" --argjson sweep "$sweep_seeds" --argjson fuzz "$fuzz_seconds" \
  --argjson fuzz_floor "$fuzz_floor" --argjson fuzz_targets "$fuzz_targets" --argjson fuzz_sweeps "$fuzz_sweeps" \
  --arg cbase "$base_sha" --arg preprose "$preprose_sha" --arg only "$only" --argjson checks "$checks_json" \
  '{chunk:$chunk, sha:$sha, dirty_tree:$dirty, stage:$stage, partial:$partial, only:$only, overall:$overall,
    counts_for_landing:$counts, go_version:$go,
    duckdb_version:(if $duckdb == "" then "not found" else $duckdb end),
    started:$started, finished:$finished, seconds:$seconds,
    comment_baseline:$cbase, preprose_commit:$preprose, short:$short, sweep_seeds:$sweep, fuzz_seconds:$fuzz,
    fuzz_rate_floor:$fuzz_floor, fuzz_targets:$fuzz_targets, fuzz_seed_sweeps:$fuzz_sweeps,
    skip_not_built:[$checks[] | select(.status == "skip-not-built") | .gate + " " + .check],
    checks:$checks}' > "$out/result.json" || die "cannot write $out/result.json"
echo "gate: overall=$overall counts_for_landing=$counts -> $out/result.json"
[ "$overall" = pass ]
