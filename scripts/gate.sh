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
#                          the baseline commit. The builder writes no comments; the prose
#                          seat writes them later.
#   GATE_BASE=<commit>     the baseline of that rule (default: the merge base with main).
#                          It must be a strict ancestor of HEAD; a baseline equal to HEAD is
#                          a setup error (exit 2), never a pass.
#   GATE_STAGE=final       after the prose pass. Needs GATE_PREPROSE=<commit>, the last
#                          commit before the prose pass; G2 proves with astdiff that the
#                          prose pass changed no code between that commit and this tree.
#   GATE_STAGE=foundation  G2 is skipped. Accepted for the chunk c00-foundation only.
#   GATE_ONLY=G0,G3        run a subset; the result is marked partial and never counts.
#   GATE_SHORT=1           G3 runs `go test -short` (the CI set); never counts.
#   GATE_OUT=<dir>         write the result somewhere else; never counts.
#   SWEEP_SEEDS=200        G4 seed budget per scenario.
#   FUZZ_SECONDS=20        G5 time per fuzz target.
#
# The checks, in order:
#   G0  build     tools build; gofmt; go build; go vet; the nested module tools/detlint
#                 (vet and tests); go mod tidy -diff
#   G1  lints     detlint (tools/lint-rules/det-packages.txt), importlint, boundlint,
#                 paniccheck
#   G2  comments  see GATE_STAGE
#   G3  tests     go test -count=1 ./... ; go test -race for tools/lint-rules/race-packages.txt
#   G4  sweep     scripts/sweep.sh
#   G5  fuzz      scripts/fuzz.sh
#   G6  tier-r    scripts/tier-r.sh
#   G7  csv       scripts/csv-assert.sh over the run directories G4 and G6 wrote
#   G8  gen-docs  scripts/gen-docs.sh --check
#   G9  site      scripts/site.sh into a scratch folder
#   G10 extra     agent_work/chunks/<chunk-dir>/gate/extra.sh, if the chunk has one
#
# Stages that are not built yet. G4-G8 run a script that starts life as a skeleton exiting
# 3 with "not built yet". That outcome is recorded as skip-not-built ONLY while the marker
# file scripts/.not-built/<name> exists. With the marker gone the same outcome is red; and
# while the marker exists any other outcome is red too (delete the marker when you build
# the script). So a stage cannot be skipped silently once it exists.
#
# Fail closed: a check that cannot run is red. Check statuses: pass, fail, skip-not-built,
# skip-foundation (G2 at the foundation stage), skip-none (G10 when the chunk has no
# extra.sh). overall is "pass" when no check is "fail".
#
# counts_for_landing is true only when ALL of these hold: every check ran (no GATE_ONLY),
# overall is pass, the tree was clean when the gate started (changes under the chunk's own
# gate/ folder do not count), HEAD is a full commit id, the stage is valid, and none of
# GATE_SHORT, GATE_OUT, a SWEEP_SEEDS under 200 or a FUZZ_SECONDS under 20 was used. The
# file is evidence for an honest operator, not a tamper-proof certificate;
# agent_work/orch/land.sh binds it to the branch tip.
#
# Exit status: 0 every requested check green (or skipped as described), 1 a check is red,
# 2 usage or setup error.
set -u
set -o pipefail

chunk=${1:-}
[ $# = 1 ] && [[ "$chunk" =~ ^[a-z0-9][a-z0-9-]*$ ]] || { echo "usage: scripts/gate.sh <chunk-dir>   (for example c01-sim-kernel)" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"
export PATH
for tool in go gofmt jq git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "gate: $tool is not on PATH" >&2; exit 2; }
done
unset GOFLAGS GOWORK
export GOFLAGS=-buildvcs=false

stage=${GATE_STAGE:-build}
case "$stage" in
  build|final) ;;
  foundation) [ "$chunk" = c00-foundation ] || { echo "gate: GATE_STAGE=foundation is for the chunk c00-foundation only" >&2; exit 2; } ;;
  *) echo "gate: GATE_STAGE must be build, final or foundation, got '$stage'" >&2; exit 2 ;;
esac
only=${GATE_ONLY:-}
[[ "$only" =~ ^(G(10|[0-9])(,G(10|[0-9]))*)?$ ]] || { echo "gate: GATE_ONLY must look like G0,G3 (G0 to G10), got '$only'" >&2; exit 2; }
want() { [ -z "$only" ] || [[ ",$only," == *",$1,"* ]]; }
sweep_seeds=${SWEEP_SEEDS:-200}
fuzz_seconds=${FUZZ_SECONDS:-20}
[[ "$sweep_seeds" =~ ^[1-9][0-9]*$ ]] || { echo "gate: SWEEP_SEEDS must be a positive whole number" >&2; exit 2; }
[[ "$fuzz_seconds" =~ ^[1-9][0-9]*$ ]] || { echo "gate: FUZZ_SECONDS must be a positive whole number" >&2; exit 2; }
short=false; short_flag=()
if [ "${GATE_SHORT:-}" = 1 ]; then short=true; short_flag=(-short); fi

out_default="agent_work/chunks/$chunk/gate"
out=${GATE_OUT:-$out_default}
custom_out=false; [ -z "${GATE_OUT:-}" ] || custom_out=true

sha=$(git rev-parse --verify -q 'HEAD^{commit}' 2>/dev/null || true)
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || sha=unknown
dirty=true
if status=$(git status --porcelain --untracked-files=all 2>/dev/null); then
  [ -n "$(grep -v " $out_default/" <<<"$status" || true)" ] || dirty=false
fi

scratch=$(mktemp -d /tmp/gate.XXXXXX) || exit 2
trap 'rm -rf "$scratch"' EXIT
bin="$scratch/bin"; runs="$scratch/runs"
mkdir -p "$bin" "$runs/sweep" "$runs/tier-r"

# ---- G2 setup: done before anything is written, so a setup error leaves no result behind.
base_sha=none; preprose_sha=none
if want G2 && [ "$stage" = build ]; then
  [ "$sha" != unknown ] || { echo "gate: a build-stage run needs git history for its comment baseline" >&2; exit 2; }
  base=${GATE_BASE:-}
  if [ -z "$base" ]; then
    for ref in main origin/main; do
      if git rev-parse -q --verify "$ref^{commit}" >/dev/null 2>&1; then base=$(git merge-base HEAD "$ref" 2>/dev/null || true); break; fi
    done
  fi
  [ -n "$base" ] || { echo "gate: no comment baseline: there is no main branch to compare with; set GATE_BASE=<commit>" >&2; exit 2; }
  base_sha=$(git rev-parse --verify -q "$base^{commit}") || { echo "gate: GATE_BASE '$base' is not a commit" >&2; exit 2; }
  if [ "$base_sha" = "$sha" ]; then
    echo "gate: HEAD is its own comment baseline (this is main, or a branch with no commits of its own)." >&2
    echo "gate: a build-stage run judges a chunk branch against the commit it started from; name an" >&2
    echo "gate: earlier commit with GATE_BASE, or use GATE_STAGE=final with GATE_PREPROSE after the prose pass." >&2
    exit 2
  fi
  git merge-base --is-ancestor "$base_sha" "$sha" || { echo "gate: baseline $base_sha is not an ancestor of HEAD" >&2; exit 2; }
  mkdir "$scratch/baseline"
  git archive "$base_sha" | tar -x -C "$scratch/baseline" || { echo "gate: cannot export the baseline $base_sha" >&2; exit 2; }
fi
if want G2 && [ "$stage" = final ]; then
  [ -n "${GATE_PREPROSE:-}" ] || { echo "gate: GATE_STAGE=final needs GATE_PREPROSE=<the last commit before the prose pass>" >&2; exit 2; }
  [ "$sha" != unknown ] || { echo "gate: a final-stage run needs git history" >&2; exit 2; }
  preprose_sha=$(git rev-parse --verify -q "$GATE_PREPROSE^{commit}") || { echo "gate: GATE_PREPROSE '$GATE_PREPROSE' is not a commit" >&2; exit 2; }
  [ "$preprose_sha" != "$sha" ] || { echo "gate: GATE_PREPROSE is HEAD itself; nothing can be certified against itself" >&2; exit 2; }
  git merge-base --is-ancestor "$preprose_sha" "$sha" || { echo "gate: GATE_PREPROSE $preprose_sha is not an ancestor of HEAD" >&2; exit 2; }
  mkdir "$scratch/preprose"
  git archive "$preprose_sha" | tar -x -C "$scratch/preprose" || { echo "gate: cannot export $preprose_sha" >&2; exit 2; }
fi

mkdir -p "$out" || exit 2
rm -f "$out"/G*.log "$out/result.json"
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
# stage_check <gate> <name> <marker> <command...>: a check that may not be built yet.
stage_check() {
  local gate=$1 name=$2 marker="scripts/.not-built/$3"; shift 3
  local log="$out/$gate-$name.log" t0=$SECONDS status note='' rc=0
  "$@" >"$log" 2>&1 || rc=$?
  if [ $rc = 3 ] && grep -q 'not built yet' "$log"; then
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
g2_astdiff() {
  tool astdiff || return 1
  echo "NOTE: code compared with pre-prose commit $preprose_sha"
  "$bin/astdiff" "$scratch/preprose" "$root"
}

# ---- G3 tests
g3_unit() {
  no_go && return 0
  [ "$short" = false ] || echo "NOTE: short run (go test -short); does not count for landing"
  go test -count=1 -timeout 30m "${short_flag[@]}" ./...
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
g10_extra() { [ -x "$extra" ] || { echo "$extra exists but is not executable"; return 1; }; CHUNK="$chunk" RUN_DIR="$runs" "$extra"; }

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
  check G3 race g3_race
fi
want G4 && stage_check G4 sweep sweep env SWEEP_SEEDS="$sweep_seeds" RUN_DIR="$runs/sweep" scripts/sweep.sh
want G5 && stage_check G5 fuzz fuzz env FUZZ_SECONDS="$fuzz_seconds" scripts/fuzz.sh
want G6 && stage_check G6 tier-r tier-r env RUN_DIR="$runs/tier-r" scripts/tier-r.sh
want G7 && stage_check G7 csv csv-assert scripts/csv-assert.sh "$runs/sweep" "$runs/tier-r"
want G8 && stage_check G8 gen-docs gen-docs scripts/gen-docs.sh --check
want G9 && check G9 site g9_site
if want G10; then
  if [ -e "$extra" ]; then check G10 extra g10_extra
  else record G10 extra skip-none 0 "the chunk has no gate/extra.sh"; fi
fi

partial=false; [ -z "$only" ] || partial=true
counts=false
if [ "$overall" = pass ] && [ "$partial" = false ] && [ "$dirty" = false ] && [ "$sha" != unknown ] \
   && [ "$short" = false ] && [ "$custom_out" = false ] && [ "$sweep_seeds" -ge 200 ] && [ "$fuzz_seconds" -ge 20 ]; then counts=true; fi
jq -n --arg chunk "$chunk" --arg sha "$sha" --arg stage "$stage" --arg started "$started" \
  --arg finished "$(date -u +%FT%TZ)" --argjson seconds "$((SECONDS - t_start))" \
  --arg go "$(go version 2>&1)" --arg duckdb "$(duckdb --version 2>/dev/null | head -n1 || true)" \
  --arg overall "$overall" --argjson partial "$partial" --argjson dirty "$dirty" --argjson counts "$counts" \
  --argjson short "$short" --argjson sweep "$sweep_seeds" --argjson fuzz "$fuzz_seconds" \
  --arg cbase "$base_sha" --arg preprose "$preprose_sha" --arg only "$only" --argjson checks "$checks_json" \
  '{chunk:$chunk, sha:$sha, dirty_tree:$dirty, stage:$stage, partial:$partial, only:$only, overall:$overall,
    counts_for_landing:$counts, go_version:$go,
    duckdb_version:(if $duckdb == "" then "not found" else $duckdb end),
    started:$started, finished:$finished, seconds:$seconds,
    comment_baseline:$cbase, preprose_commit:$preprose, short:$short, sweep_seeds:$sweep, fuzz_seconds:$fuzz,
    skip_not_built:[$checks[] | select(.status == "skip-not-built") | .gate + " " + .check],
    checks:$checks}' > "$out/result.json" || { echo "gate: cannot write $out/result.json" >&2; exit 2; }
echo "gate: overall=$overall counts_for_landing=$counts -> $out/result.json"
[ "$overall" = pass ]
