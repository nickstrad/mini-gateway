#!/bin/bash
# The named chaos scenarios: tier S (the engine on the simulator), then tier R (the real
# binary and a real Postgres 17) when Docker is present, each followed by the SQL assertions
# of test/analysis/<scenario>/ over the flushed CSV files.
#
# Usage: scripts/chaos.sh            (make chaos)
#   CHAOS_SEEDS=5     seeds per tier S scenario (pinned seeds are always run too)
#   RUN_DIR=<dir>     where the runs go (default: a new folder under /tmp)
#   VERBOSE=1         print the raw go test and csv-assert output
#   CHAOS_TIER_R=0    skip tier R even when Docker is present
#   CHAOS_GO=<cmd>    the command run as `go` for the go test steps (default go; the script
#                     tests set a stub)
# Concise by default: one line per scenario (tier, scenario, seeds, pass or fail, its run
# folder), then one summary line. A failed scenario is followed by its --- FAIL lines, error
# lines and REPLAY lines; a failed SQL assertion by the FAIL lines and violating rows of its
# .sql.log (csv-assert.sh prints up to 20 rows per file; scripts/report.sh prints all of them).
# Exit status: 0 green, 1 a scenario or an assertion failed, 2 setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
seeds=${CHAOS_SEEDS:-5}
dir=${RUN_DIR:-}
if [ -z "$dir" ]; then dir=$(mktemp -d /tmp/chaos.XXXXXX) || exit 2; fi
mkdir -p "$dir/sweep" "$dir/tier-r" || exit 2
verbose=${VERBOSE:-0}
failed=0
gocmd=${CHAOS_GO:-go}
show() { if [ "$verbose" = 1 ]; then cat "$1"; fi; }
# details <log> <test>: the --- FAIL lines of <test> and the tests below it, with their error
# and REPLAY lines, indented; concise mode prints them under the scenario's line.
details() {
  awk -v t="$2" '
    /^=== (RUN|PAUSE|CONT|NAME) / { cur = $3; next }
    /^[ \t]*--- (PASS|FAIL|SKIP): / { cur = $3; if ($2 == "FAIL:" && (cur == t || index(cur, t "/") == 1)) { l = $0; sub(/^[ \t]+/, "", l); print "    " l } ; next }
    (cur == t || index(cur, t "/") == 1) && ($0 ~ /\.go:[0-9]+: / || $0 ~ /^[ \t]*(error|seed): / || $0 ~ /REPLAY: /) { l = $0; sub(/^[ \t]+/, "", l); print "      " l }' "$1"
}
# sql_details <sql.log>: the FAIL lines and violating rows csv-assert.sh wrote.
sql_details() { grep -E '^(FAIL: |  )' "$1" | sed 's/^/    /'; }

slog="$dir/tier-s.log"
DST_SEEDS=$seeds RUN_DIR="$dir/sweep" "$gocmd" test -count=1 -timeout 60m -v -run '^TestScenarioE2E$' ./test/e2e >"$slog" 2>&1
show "$slog"
for sc in $(grep -oE -- '--- (PASS|FAIL): TestScenarioE2E/[a-z0-9-]+ ' "$slog" | awk '{print $3}' | sed 's|TestScenarioE2E/||' | sort -u); do
  pass=$(grep -cE -- "--- PASS: TestScenarioE2E/$sc/seed=" "$slog" || true)
  fail=$(grep -cE -- "--- FAIL: TestScenarioE2E/$sc/seed=" "$slog" || true)
  verdict=pass
  if [ "$fail" != 0 ]; then verdict=FAIL; failed=$((failed + 1)); fi
  if [ "$verdict" = pass ] && [ -d "$dir/sweep/$sc" ] && [ -d "test/analysis/$sc" ]; then
    alog="$dir/sweep/$sc.sql.log"
    if ! scripts/csv-assert.sh "$dir/sweep/$sc" test/analysis/"$sc"/*.sql >"$alog" 2>&1; then verdict="FAIL(sql)"; failed=$((failed + 1)); fi
    show "$alog"
  fi
  printf 'S  %-28s seeds %-4s %-9s %s\n' "$sc" "$((pass + fail))" "$verdict" "$dir/sweep/$sc"
  case "$verdict" in
    FAIL) details "$slog" "TestScenarioE2E/$sc" ;;
    "FAIL(sql)") sql_details "$alog" ;;
  esac
done
if ! grep -qE -- '--- PASS: TestScenarioE2E' "$slog"; then
  echo "chaos: no tier S scenario ran; see $slog" >&2
  tail -n 30 "$slog" | sed 's/^/    /' >&2
  failed=$((failed + 1))
fi

if [ "${CHAOS_TIER_R:-1}" != 0 ] && command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  rlog="$dir/tier-r.log"
  prefix="mg-chaos-$$"
  trap 'for n in $(docker ps -aq --filter "name=^${prefix}-" 2>/dev/null); do docker rm -f -v "$n" >/dev/null 2>&1; done' EXIT
  MG_TIER_R=1 MG_TIER_R_PREFIX="$prefix" RUN_DIR="$dir/tier-r" "$gocmd" test -count=1 -timeout 30m -v -run '^TestTierR' ./test/e2e >"$rlog" 2>&1
  show "$rlog"
  for t in $(grep -oE -- '--- (PASS|FAIL): TestTierR[A-Za-z]+' "$rlog" | awk '{print $3}'); do
    verdict=pass
    if grep -qE -- "--- FAIL: $t( |$)" "$rlog"; then verdict=FAIL; failed=$((failed + 1)); fi
    printf 'R  %-50s %-9s %s\n' "$t" "$verdict" "$rlog"
    [ "$verdict" = FAIL ] && details "$rlog" "$t"
  done
  for s in "$dir"/tier-r/tierr-*; do
    [ -d "$s" ] || continue
    sc=$(basename "$s")
    verdict=pass
    if ! scripts/csv-assert.sh "$s" test/analysis/"$sc"/*.sql >"$s.sql.log" 2>&1; then verdict="FAIL(sql)"; failed=$((failed + 1)); fi
    show "$s.sql.log"
    printf 'R  %-50s %-9s %s\n' "$sc (SQL)" "$verdict" "$s"
    [ "$verdict" = pass ] || sql_details "$s.sql.log"
  done
else
  echo "R  (skipped: no Docker)"
fi

if [ "$failed" != 0 ]; then
  echo "chaos: $failed failures; runs under $dir"
  exit 1
fi
echo "chaos: all green; runs under $dir"
