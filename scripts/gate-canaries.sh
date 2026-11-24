#!/bin/bash
# Gate canaries: testing the tester. Each canary plants one violation in a throwaway copy
# of this tree, runs scripts/gate.sh there, and requires the right check to go red with the
# expected message in its log. A gate that cannot fail proves nothing.
#
# Usage: scripts/gate-canaries.sh
#   CANARY_ONLY=<id>,<id>   run only these canaries (the result is then not written)
#   CANARY_KEEP=1           keep the throwaway copy and print its path
#
# How it works:
#   1. The tracked and untracked-but-not-ignored files of this tree are copied to a new
#      folder under /tmp and committed there as branch `main`. On a branch `canary` a small
#      clean product package (internal/canary, no comments, one passing test) is committed:
#      the canaries need product Go, and the real tree has none at chunk 0.
#   2. The clean copy must be green first: a full build-stage gate run, overall pass,
#      counts_for_landing true. If it is not, the canaries stop: nothing could be concluded.
#   3. For each canary the copy is reset to the clean commit, the violation is planted and
#      committed, and the gate runs (limited with GATE_ONLY to the gate under test, to keep
#      the run short). Kinds of canary:
#        red     the gate exits 1, the named check has status "fail", its log matches
#        setup   the gate refuses to run: exit 2 and the message on standard error matches
#        green   a control: the named check passes (and, where given, a result field holds)
#      Each plant creates the minimal package it needs inside the copy.
#
# Writes agent_work/chunks/c00-foundation/gate/canaries.json (committed) and prints one
# line per canary: CAUGHT or MISSED.
#
# Exit status: 0 every canary caught, 1 a canary was missed or the clean copy is not
# green, 2 setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
export PATH
for tool in go jq git tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "canaries: $tool is not on PATH" >&2; exit 2; }
done
unset GATE_ONLY GATE_STAGE GATE_BASE GATE_PREPROSE GATE_SHORT GATE_OUT SWEEP_SEEDS FUZZ_SECONDS

module=github.com/nickstrad/mini-gateway
result_file=agent_work/chunks/c00-foundation/gate/canaries.json
only=${CANARY_ONLY:-}
sha=$(git rev-parse HEAD 2>/dev/null || echo unknown)
dirty=false; [ -z "$(git status --porcelain --untracked-files=all | grep -v ' agent_work/chunks/c00-foundation/gate/' || true)" ] || dirty=true

# The site check needs the Python package `markdown`. Fetch it once here (scripts/site.sh
# caches it in .cache/pylib) and point every run in the copy at that folder.
if ! python3 -c 'import markdown' 2>/dev/null; then
  if [ ! -f .cache/pylib/markdown/__init__.py ]; then
    pre=$(mktemp -d /tmp/gate-canaries-site.XXXXXX) || exit 2
    scripts/site.sh "$pre/site" >/dev/null 2>&1 || { echo "canaries: cannot prepare the markdown package (scripts/site.sh failed)" >&2; rm -rf "$pre"; exit 2; }
    rm -rf "$pre"
  fi
  export SITE_PYLIB="$root/.cache/pylib"
fi

work=$(mktemp -d /tmp/gate-canaries.XXXXXX) || exit 2
cleanup() { if [ "${CANARY_KEEP:-}" = 1 ]; then echo "canaries: copy kept at $work"; else rm -rf "$work"; fi; }
trap cleanup EXIT
copy="$work/repo"
mkdir "$copy"
git ls-files -co --exclude-standard -z | tar --null -T - -cf - | tar -xf - -C "$copy" || { echo "canaries: cannot copy the tree" >&2; exit 2; }
g() { git -C "$copy" -c user.name=canary -c user.email=canary@example.invalid -c commit.gpgsign=false "$@"; }
commit() { g add -A && g commit -q --allow-empty -m "$1"; }

# put <path>: write standard input to a file in the copy, creating its folder.
put() { mkdir -p "$copy/$(dirname "$1")" && cat > "$copy/$1"; }

g init -q -b main || exit 2
commit "base: the tree under test" || exit 2
g checkout -q -b canary || exit 2
put internal/canary/ok.go <<'EOF'
package canary

func Add(a, b int) int {
	return a + b
}
EOF
put internal/canary/ok_test.go <<'EOF'
package canary

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("Add(2, 3) is not 5")
	}
}
EOF
commit "seed: a clean product package" || exit 2
clean=$(g rev-parse HEAD)

started=$(date -u +%FT%TZ)
t_start=$SECONDS
rows='[]'
missed=0
total=0
res="$copy/agent_work/chunks/canary/gate/result.json"

# run_gate <env assignments...>: run the gate in the copy for the chunk "canary".
run_gate() { (cd "$copy" && env "$@" scripts/gate.sh canary) > "$work/gate.out" 2> "$work/gate.err"; }

# verdict <id> <kind> <what was planted> <gate> <check> <expected> <outcome> <detail>
verdict() {
  total=$((total + 1))
  [ "$7" = CAUGHT ] || missed=$((missed + 1))
  printf '%-7s %-26s %-3s %-11s %s\n' "$7" "$1" "$4" "$5" "$8"
  rows=$(jq -c --arg id "$1" --arg kind "$2" --arg plant "$3" --arg gate "$4" --arg check "$5" --arg expect "$6" \
    --arg outcome "$7" --arg detail "$8" \
    '. + [{id:$id, kind:$kind, planted:$plant, gate:$gate, check:$check, expect:$expect, outcome:$outcome, detail:$detail}]' <<<"$rows")
}

selected() { [ -z "$only" ] || [[ ",$only," == *",$1,"* ]]; }
reset_copy() { g checkout -q -f canary && g reset -q --hard "$clean" && g clean -q -fdx; }

# canary <id> <kind> <description> <gate> <check> <expected regex> <plant function> [env...]
canary() {
  local id=$1 kind=$2 desc=$3 gate=$4 check=$5 expect=$6 plant=$7; shift 7
  selected "$id" || return 0
  reset_copy || { verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "cannot reset the copy"; return; }
  canary_env=()
  "$plant" || { verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "the plant itself failed"; return; }
  local rc=0 status log="$copy/agent_work/chunks/canary/gate/$gate-$check.log"
  rm -f "$res"
  run_gate GATE_ONLY="$gate" "$@" "${canary_env[@]}" || rc=$?
  case "$kind" in
    red)
      status=$(jq -r --arg g "$gate" --arg c "$check" '.checks[] | select(.gate == $g and .check == $c) | .status' "$res" 2>/dev/null || true)
      if [ $rc != 1 ]; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "gate exit $rc, want 1"
      elif [ "$status" != fail ]; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "check status '${status:-absent}', want fail"
      elif [ "$(jq -r .overall "$res")" != fail ] || [ "$(jq -r .counts_for_landing "$res")" != false ]; then
        verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "result does not say overall=fail, counts_for_landing=false"
      elif ! grep -Eq -- "$expect" "$log"; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "the log does not match the expected message"
      else verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" CAUGHT "red: $(grep -Em1 -- "$expect" "$log" | cut -c1-150)"; fi ;;
    setup)
      if [ $rc != 2 ]; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "gate exit $rc, want 2"
      elif [ -e "$res" ]; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "a result file was written by a refused run"
      elif ! grep -Eq -- "$expect" "$work/gate.err"; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "the refusal does not match the expected message"
      else verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" CAUGHT "refused: $(grep -Em1 -- "$expect" "$work/gate.err" | cut -c1-150)"; fi ;;
    green)
      status=$(jq -r --arg g "$gate" --arg c "$check" '.checks[] | select(.gate == $g and .check == $c) | .status' "$res" 2>/dev/null || true)
      if [ $rc != 0 ] || [ "$status" != pass ]; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "gate exit $rc, check status '${status:-absent}', want 0 and pass"
      elif [ "$(jq -r "$expect" "$res")" != true ]; then verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" MISSED "the result does not satisfy: $expect"
      else verdict "$id" "$kind" "$desc" "$gate" "$check" "$expect" CAUGHT "control held: $expect"; fi ;;
  esac
}

# ---- the clean copy must be green first
echo "canaries: copy at $work/repo, clean commit $clean"
rc=0
run_gate GATE_STAGE=build || rc=$?
if [ $rc != 0 ] || [ "$(jq -r '.overall + " " + (.counts_for_landing|tostring)' "$res" 2>/dev/null)" != "pass true" ]; then
  echo "canaries: the clean copy is not green (gate exit $rc); nothing can be concluded" >&2
  cat "$work/gate.out" "$work/gate.err" >&2
  exit 1
fi
echo "canaries: the clean copy is green and counts for landing ($(jq -r '.skip_not_built | length' "$res") stages skip-not-built)"
clean_skips=$(jq -c .skip_not_built "$res")

# ---- plants
plant_gofmt() {
  printf 'package canary\n\nfunc  Unformatted( )   int {\nreturn 1\n}\n' | put internal/canary/unformatted.go && commit plant
}
plant_compile() {
  printf 'package canary\n\nfunc Broken() int {\n\treturn "not a number"\n}\n' | put internal/canary/broken.go && commit plant
}
plant_untidy() {
  printf 'module %s\n\ngo 1.26\n\nrequire github.com/jackc/pgx/v5 v5.13.0\n' "$module" > "$copy/go.mod" && commit plant
}
plant_toolstest() {
  printf 'package report\n\nimport "testing"\n\nfunc TestPlanted(t *testing.T) {\n\tt.Fatal("PLANTED-TOOLS-FAILURE")\n}\n' | put tools/detlint/internal/report/planted_test.go && commit plant
}
plant_wallclock() {
  printf 'package detcanary\n\nimport "time"\n\nfunc Stamp() int64 {\n\treturn time.Now().UnixNano()\n}\n' | put internal/detcanary/clock.go \
    && echo internal/detcanary >> "$copy/tools/lint-rules/det-packages.txt" && commit plant
}
plant_detlist_typo() { echo internal/no-such-package >> "$copy/tools/lint-rules/det-packages.txt" && commit plant; }
plant_core_import() {
  printf 'package pgwire\n\nconst Version = 3\n' | put internal/pgwire/pgwire.go \
    && printf 'package pool\n\nimport "%s/internal/pgwire"\n\nconst Wire = pgwire.Version\n' "$module" | put internal/pool/pool.go && commit plant
}
plant_adapter_import() {
  printf 'package csv\n\nconst Name = "csv"\n' | put internal/obs/csv/csv.go \
    && printf 'package proxy\n\nimport "%s/internal/obs/csv"\n\nconst Adapter = csv.Name\n' "$module" | put internal/proxy/proxy.go && commit plant
}
plant_pgproto_outside() {
  printf 'package canary\n\nimport _ "github.com/jackc/pgx/v5/pgproto3"\n' | put internal/canary/wire.go && commit plant
}
plant_third_party() {
  printf 'package canary_test\n\nimport _ "pgregory.net/rapid"\n' | put internal/canary/prop_test.go && commit plant
}
plant_second_require() {
  printf 'module %s\n\ngo 1.26\n\nrequire (\n\tgithub.com/google/uuid v1.6.0\n\tgithub.com/jackc/pgx/v5 v5.13.0\n)\n' "$module" > "$copy/go.mod" && commit plant
}
plant_unbuffered() {
  printf 'package shell\n\nfunc Mailbox() chan int {\n\treturn make(chan int)\n}\n' | put internal/shell/mailbox.go && commit plant
}
plant_literal_cap() {
  printf 'package shell\n\nfunc Mailbox() chan int {\n\treturn make(chan int, 64)\n}\n' | put internal/shell/mailbox.go && commit plant
}
plant_growth() {
  printf 'package shell\n\ntype Box struct {\n\tqueue []int\n\tin    chan int\n}\n\nfunc (b *Box) Run() {\n\tfor v := range b.in {\n\t\tb.queue = append(b.queue, v)\n\t}\n}\n' | put internal/shell/box.go && commit plant
}
plant_panic() {
  printf 'package canary\n\nfunc Must(ok bool) {\n\tif !ok {\n\t\tpanic("planted")\n\t}\n}\n' | put internal/canary/must.go && commit plant
}
plant_comment() {
  printf 'package canary\n\n// Sub is a planted comment.\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n' | put internal/canary/sub.go && commit plant
}
plant_baseline_is_head() { canary_env=(GATE_BASE="$clean"); }
plant_prose_changed_code() {
  printf 'package canary\n\n// Add adds.\nfunc Add(a, b int) int {\n\treturn a + b + 1\n}\n' | put internal/canary/ok.go && commit "prose pass that also changed code"
}
plant_prose_only() {
  printf 'package canary\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n' | put internal/canary/ok.go && commit "prose pass"
}
plant_nothing() { :; }
plant_failing_test() {
  printf 'package canary\n\nimport "testing"\n\nfunc TestPlanted(t *testing.T) {\n\tt.Fatal("PLANTED-TEST-FAILURE")\n}\n' | put internal/canary/planted_test.go && commit plant
}
plant_race() {
  printf 'package racy\n\nvar counter int\n\nfunc Bump() {\n\tcounter++\n}\n' | put internal/racy/racy.go \
    && printf 'package racy\n\nimport (\n\t"sync"\n\t"testing"\n)\n\nfunc TestBump(t *testing.T) {\n\tvar wg sync.WaitGroup\n\tfor range 8 {\n\t\twg.Add(1)\n\t\tgo func() {\n\t\t\tdefer wg.Done()\n\t\t\tfor range 1000 {\n\t\t\t\tBump()\n\t\t\t}\n\t\t}()\n\t}\n\twg.Wait()\n}\n' | put internal/racy/racy_test.go \
    && echo ./internal/racy/... >> "$copy/tools/lint-rules/race-packages.txt" && commit plant
}
plant_racelist_typo() { echo ./internal/no-such-package/... >> "$copy/tools/lint-rules/race-packages.txt" && commit plant; }
plant_marker_deleted() { rm "$copy/scripts/.not-built/sweep" && commit plant; }
plant_built_with_marker() { printf '#!/bin/bash\necho "fuzz.sh: ran"\nexit 0\n' > "$copy/scripts/fuzz.sh" && commit plant; }
plant_stage_fails() {
  rm "$copy/scripts/.not-built/tier-r" && printf '#!/bin/bash\necho "PLANTED-STAGE-FAILURE"\nexit 1\n' > "$copy/scripts/tier-r.sh" && commit plant
}
plant_extra_fails() {
  printf '#!/bin/bash\necho "PLANTED-EXTRA-FAILURE"\nexit 1\n' | put agent_work/chunks/canary/gate/extra.sh \
    && chmod +x "$copy/agent_work/chunks/canary/gate/extra.sh" && commit plant
}
plant_extra_not_executable() {
  printf '#!/bin/bash\nexit 0\n' | put agent_work/chunks/canary/gate/extra.sh && chmod -x "$copy/agent_work/chunks/canary/gate/extra.sh" && commit plant
}
plant_broken_link() { printf '\nSee [the missing page](no-such-page.md).\n' >> "$copy/docs/index.md" && commit plant; }
plant_live_missing() {
  jq '.chapters |= map(if .number == 1 then .status = "live" else . end)' "$copy/book/manifest.json" > "$work/manifest.json" \
    && cp "$work/manifest.json" "$copy/book/manifest.json" && commit plant
}
plant_planned_exists() { printf '# Too early\n' | put book/chapters/02-simulated-world.md && commit plant; }
plant_untracked_file() { echo "not committed" > "$copy/untracked.txt"; }

# ---- the canaries
canary g0-gofmt            red "unformatted Go in a product package" G0 gofmt 'internal/canary/unformatted\.go' plant_gofmt
canary g0-compile-error    red "a compile error in a product package" G0 build 'cannot use "not a number"' plant_compile
canary g0-vet              red "the same compile error, seen by go vet" G0 vet 'cannot use "not a number"' plant_compile
canary g0-untidy-gomod     red "a go.mod requirement that go mod tidy would remove" G0 tidy 'not tidy' plant_untidy
canary g0-tools-test       red "a failing test in the nested module tools/detlint" G0 tools-test 'PLANTED-TOOLS-FAILURE' plant_toolstest
canary g1-wallclock        red "time.Now() in a package listed for detlint" G1 detlint 'clock\.go:6:[0-9]+: wallclock' plant_wallclock
canary g1-detlist-typo     red "det-packages.txt names a directory that does not exist" G1 detlint 'no such directory: internal/no-such-package' plant_detlist_typo
canary g1-core-imports-wire red "internal/pool imports internal/pgwire" G1 importlint 'internal/pool/pool\.go:3:[0-9]+: deny' plant_core_import
canary g1-engine-imports-adapter red "internal/proxy imports an adapter under internal/obs/" G1 importlint 'internal/proxy/proxy\.go:3:[0-9]+: deny' plant_adapter_import
canary g1-pgproto-outside  red "pgproto3 imported outside the wire layer, the fake and the test clients" G1 importlint 'wire\.go:3:[0-9]+: only' plant_pgproto_outside
canary g1-third-party      red "a third-party import in test code" G1 importlint 'prop_test\.go:3:[0-9]+: third-party' plant_third_party
canary g1-second-require   red "a second go.mod requirement" G1 importlint 'go\.mod:6:1: gomod: requirement github.com/google/uuid is not allowed' plant_second_require
canary g1-unbuffered-chan  red "an unbuffered channel in internal/shell" G1 boundlint 'mailbox\.go:4:[0-9]+: unbuffered' plant_unbuffered
canary g1-literal-chan-cap red "a channel capacity written as a number in internal/shell" G1 boundlint 'mailbox\.go:4:[0-9]+: chan-cap' plant_literal_cap
canary g1-unbounded-growth red "a slice field appended to in a receive loop with no length check" G1 boundlint 'box\.go:10:[0-9]+: unbounded-growth' plant_growth
canary g1-panic            red "a panic that is not in the allow file" G1 paniccheck 'must\.go:5:[0-9]+: panic' plant_panic
canary g2-comment          red "a comment in product Go at the build stage" G2 comments 'internal/canary/sub\.go:3:1: comment' plant_comment GATE_STAGE=build
canary g2-baseline-is-head setup "a build-stage run whose comment baseline is HEAD" G2 comments 'HEAD is its own comment baseline' plant_baseline_is_head GATE_STAGE=build
canary g2-prose-changed-code red "a code change between GATE_PREPROSE and HEAD at the final stage" G2 astdiff 'internal/canary/ok\.go:[0-9]+:[0-9]+: astdiff' plant_prose_changed_code GATE_STAGE=final GATE_PREPROSE="$clean"
canary g2-prose-only       green "control: a prose pass that only added a comment passes the final stage" G2 astdiff '.overall == "pass"' plant_prose_only GATE_STAGE=final GATE_PREPROSE="$clean"
canary g2-final-needs-preprose setup "a final-stage run without GATE_PREPROSE" G2 astdiff 'needs GATE_PREPROSE' plant_nothing GATE_STAGE=final
canary g2-foundation-other-chunk setup "GATE_STAGE=foundation for a chunk other than c00-foundation" G2 comments 'for the chunk c00-foundation only' plant_nothing GATE_STAGE=foundation
canary g3-failing-test     red "a failing test" G3 unit 'PLANTED-TEST-FAILURE' plant_failing_test
canary g3-data-race        red "a data race in a package listed for the race detector" G3 race 'WARNING: DATA RACE' plant_race
canary g3-racelist-typo    red "race-packages.txt names a package that does not exist" G3 race 'matches no package' plant_racelist_typo
canary g4-marker-deleted   red "a not-built marker deleted while the script still says not built yet" G4 sweep 'a stage that exists cannot be skipped' plant_marker_deleted
canary g5-built-with-marker red "a stage script that runs while its not-built marker still exists" G5 fuzz 'delete the marker' plant_built_with_marker
canary g6-built-stage-fails red "a built stage (marker deleted) whose script fails" G6 tier-r 'PLANTED-STAGE-FAILURE' plant_stage_fails
canary g9-broken-link      red "a broken link in a site page" G9 site 'broken link: no-such-page\.md' plant_broken_link
canary g9-live-missing     red "a chapter marked live whose file is missing" G9 site 'chapter 1 is live but' plant_live_missing
canary g9-planned-exists   red "a chapter file that exists while the manifest says planned" G9 site 'chapter 2 is planned but' plant_planned_exists
canary g10-extra-fails     red "a failing gate/extra.sh" G10 extra 'PLANTED-EXTRA-FAILURE' plant_extra_fails
canary g10-extra-not-executable red "a gate/extra.sh that is not executable" G10 extra 'not executable' plant_extra_not_executable

# Controls on counts_for_landing: a full green run on a dirty tree must not count.
if selected dirty-tree-does-not-count; then
  reset_copy && plant_untracked_file
  rc=0; run_gate GATE_STAGE=build || rc=$?
  if [ $rc = 0 ] && [ "$(jq -r '.overall + " " + (.dirty_tree|tostring) + " " + (.counts_for_landing|tostring)' "$res")" = "pass true false" ]; then
    verdict dirty-tree-does-not-count green "control: a full green run with an uncommitted file" all - 'dirty_tree true, counts_for_landing false' CAUGHT "green but does not count"
  else
    verdict dirty-tree-does-not-count green "control: a full green run with an uncommitted file" all - 'dirty_tree true, counts_for_landing false' MISSED "gate exit $rc; $(jq -c '{overall, dirty_tree, counts_for_landing}' "$res" 2>/dev/null)"
  fi
fi
if selected partial-does-not-count; then
  reset_copy
  rc=0; run_gate GATE_STAGE=build GATE_ONLY=G0,G3 || rc=$?
  if [ $rc = 0 ] && [ "$(jq -r '.overall + " " + (.partial|tostring) + " " + (.counts_for_landing|tostring)' "$res")" = "pass true false" ]; then
    verdict partial-does-not-count green "control: a green GATE_ONLY run on a clean tree" G0,G3 - 'partial true, counts_for_landing false' CAUGHT "green but does not count"
  else
    verdict partial-does-not-count green "control: a green GATE_ONLY run on a clean tree" G0,G3 - 'partial true, counts_for_landing false' MISSED "gate exit $rc; $(jq -c '{overall, partial, counts_for_landing}' "$res" 2>/dev/null)"
  fi
fi

caught=$((total - missed))
echo "canaries: $caught of $total caught, $missed missed, $((SECONDS - t_start))s"
if [ -n "$only" ]; then
  echo "canaries: CANARY_ONLY was set; $result_file not written"
else
  mkdir -p "$(dirname "$result_file")"
  jq -n --arg sha "$sha" --argjson dirty "$dirty" --arg started "$started" --argjson seconds "$((SECONDS - t_start))" \
    --arg go "$(go version)" --argjson total "$total" --argjson caught "$caught" --argjson missed "$missed" \
    --argjson skips "$clean_skips" --argjson rows "$rows" \
    '{tree_sha:$sha, tree_dirty:$dirty, started:$started, seconds:$seconds, go_version:$go,
      clean_copy:{overall:"pass", counts_for_landing:true, skip_not_built:$skips},
      total:$total, caught:$caught, missed:$missed,
      all_caught:($missed == 0 and $total > 0), canaries:$rows}' > "$result_file" || exit 2
  echo "canaries: wrote $result_file"
fi
[ "$missed" = 0 ] && [ "$total" -gt 0 ]
