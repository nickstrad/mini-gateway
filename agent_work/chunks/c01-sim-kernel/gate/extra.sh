#!/bin/bash
# Chunk 1 extras for gate check G10: the end-to-end list and the planted-bug canary.
#   1. Every scenario `mgsim list` names passes TestScenario on the pinned seeds (each seed
#      run twice: the replay check), and has its own PASS line.
#   2. The canary: the toy's planted bug makes the chunking scenarios fail
#      (TestPlantedBugCanary) and the shrinker reduces a 40-fault schedule to a minimal one
#      (TestPlantedBugCaughtAndShrunk).
#   3. Cross-process replay through the built binary: each scenario run in two processes
#      prints identical digests.
# Receives CHUNK and RUN_DIR from scripts/gate.sh. Exit 0 green, 1 red.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 1
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
work=$(mktemp -d /tmp/c01-extra.XXXXXX) || exit 1
trap 'rm -rf "$work"' EXIT

go build -o "$work/mgsim" ./cmd/mgsim || exit 1
names=$("$work/mgsim" list) || exit 1
[ -n "$names" ] || { echo "mgsim list printed nothing"; exit 1; }

DST_SEEDS=0 go test -count=1 -v -run '^(TestScenario|TestPlantedBugCanary|TestPlantedBugCaughtAndShrunk)$' ./internal/sim/simtoy >"$work/e2e.log" 2>&1 || { tail -n 40 "$work/e2e.log"; exit 1; }
rc=0
for n in $names; do
  grep -q -- "--- PASS: TestScenario/$n " "$work/e2e.log" || { echo "scenario $n has no PASS line"; rc=1; }
  for seed in 1 2; do
    a=$("$work/mgsim" run "$n" -seed "$seed") || { echo "mgsim run $n -seed $seed failed"; rc=1; continue; }
    b=$("$work/mgsim" run "$n" -seed "$seed")
    [ "$a" = "$b" ] || { echo "cross-process replay differs: $n seed $seed"; rc=1; }
  done
done
for t in TestPlantedBugCanary TestPlantedBugCaughtAndShrunk; do
  grep -q -- "--- PASS: $t " "$work/e2e.log" || { echo "$t did not pass"; rc=1; }
done
echo "NOTE: $(wc -w <<<"$names") toy scenarios on pinned seeds, canary and cross-process replay"
exit $rc
