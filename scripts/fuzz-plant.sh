#!/bin/bash
# Shows that FuzzEngineOps finds a planted ordering bug: with MG_FUZZ_PLANT=queued-vanish the
# engine leaves a queue slot taken when a client vanishes while its request is queued
# (proxy.PlantQueuedVanishKeepsSlot, reachable from tests only). The target must fail with
# the queue invariant within FUZZ_SECONDS (default 20, the gate's fuzz budget).
#
# Usage: scripts/fuzz-plant.sh
# Each run starts from the committed seed corpus with an empty fuzz cache, so earlier runs
# cannot change how fast it finds the bug.
# Any failing input the run writes under test/e2e/testdata/fuzz/FuzzEngineOps/ is removed
# afterwards (it is a planted bug, not a finding); the minimised form of one such input is
# kept as test/e2e/testdata/shrunk/queued-vanish.faults and replayed by
# TestShrunkQueuedVanishScenarioReplays.
# Exit status: 0 the bug was found in time, 1 it was not, 2 setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
secs=${FUZZ_SECONDS:-20}
corpus=test/e2e/testdata/fuzz/FuzzEngineOps
before=$(ls "$corpus" 2>/dev/null | sort)
log=$(mktemp)
cache=$(mktemp -d)
start=$(date +%s)
MG_FUZZ_PLANT=queued-vanish go test -count=1 -run '^$' -fuzz '^FuzzEngineOps$' -fuzztime "${secs}s" ./test/e2e -args -test.fuzzcachedir="$cache" >"$log" 2>&1
rc=$?
took=$(( $(date +%s) - start ))
rm -rf "$cache"
for f in $(ls "$corpus" 2>/dev/null | sort); do
  grep -qxF "$f" <<<"$before" || rm -f "$corpus/$f"
done
[ -d "$corpus" ] && [ -z "$(ls -A "$corpus")" ] && rmdir "$corpus" "$(dirname "$corpus")" 2>/dev/null
if [ "$rc" = 0 ] || ! grep -q 'queue holds' "$log"; then
  tail -n 20 "$log"
  rm -f "$log"
  echo "fuzz-plant: FuzzEngineOps did not find the planted queue-slot bug in ${secs}s" >&2
  exit 1
fi
rm -f "$log"
echo "NOTE: FuzzEngineOps found the planted queue-slot bug after ${took}s (budget ${secs}s)"
