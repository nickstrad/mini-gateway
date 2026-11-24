#!/bin/bash
# Seed sweep: every simulator scenario over many seeds (tier S).
#
# Usage: scripts/sweep.sh [-seeds N] [-run REGEXP]
#   -seeds N            seeds per scenario (default: $SWEEP_SEEDS, else 200)
#   -run REGEXP         go test -run pattern selecting the scenario tests
#                       (default ^TestScenario: every test whose name starts with TestScenario)
#   SWEEP_SEEDS=200     seeds per scenario (the gate's budget; on demand use thousands)
#   SWEEP_SEED_BASE=1   first seed
#   RUN_DIR=<dir>       where the run writes its log (sweep.log); the gate passes one and
#                       hands it to csv-assert.sh afterwards
# The scenario tests read the seeds through internal/sim/simtest (DST_SEEDS and
# DST_SEED_BASE): each scenario runs its pinned seeds plus N consecutive seeds, twice per
# seed (the replay check). Every failure prints a REPLAY: line that reproduces it; this
# script prints the first one again at the end.
#
# Exit status: 0 green, 1 a check failed (or no scenario test ran), 2 usage or setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"

seeds=${SWEEP_SEEDS:-200}
base=${SWEEP_SEED_BASE:-1}
pattern='^TestScenario'
while [ $# -gt 0 ]; do
  case "$1" in
    -seeds) [ $# -ge 2 ] || { echo "sweep.sh: -seeds needs a value" >&2; exit 2; }; seeds=$2; shift 2 ;;
    -run) [ $# -ge 2 ] || { echo "sweep.sh: -run needs a value" >&2; exit 2; }; pattern=$2; shift 2 ;;
    -h|-help|--help) sed -n '2,17p' "$0"; exit 0 ;;
    *) echo "sweep.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done
[[ "$seeds" =~ ^[1-9][0-9]*$ ]] || { echo "sweep.sh: seeds must be a positive whole number, got '$seeds'" >&2; exit 2; }
[[ "$base" =~ ^-?[0-9]+$ ]] || { echo "sweep.sh: SWEEP_SEED_BASE must be a whole number" >&2; exit 2; }

dir=${RUN_DIR:-}
if [ -z "$dir" ]; then
  dir=$(mktemp -d /tmp/sweep.XXXXXX) || exit 2
fi
mkdir -p "$dir" || exit 2
log="$dir/sweep.log"

echo "NOTE: $seeds seeds per scenario from $base, tests matching $pattern"
unset DST_SEED
rc=0
DST_SEEDS=$seeds DST_SEED_BASE=$base go test -count=1 -timeout 60m -v -run "$pattern" ./... >"$log" 2>&1 || rc=$?

passed=$(grep -cE -- '--- PASS: TestScenario[^ /]*/[^ ]+/seed=' "$log" || true)
failed=$(grep -cE -- '--- FAIL: ' "$log" || true)
echo "sweep: $passed seed runs passed, $failed failures; log $log"
if [ "$rc" != 0 ] || [ "$failed" != 0 ]; then
  grep -E -- '--- FAIL: ' "$log" | head -n 20
  first=$(grep -m1 -E '^[[:space:]]*REPLAY: ' "$log" | sed 's/^[[:space:]]*//')
  if [ -n "$first" ]; then echo "first failure: $first"; else tail -n 30 "$log"; fi
  exit 1
fi
if [ "$passed" = 0 ]; then
  echo "sweep: no scenario test matched $pattern; nothing was swept" >&2
  exit 1
fi
exit 0
