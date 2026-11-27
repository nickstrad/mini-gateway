#!/bin/bash
# Shows that each operation fuzz target finds its planted ordering bug within FUZZ_SECONDS
# (default 20, the gate's fuzz budget):
#   FuzzEngineOps (chunk 6): with MG_FUZZ_PLANT=queued-vanish the engine leaves a queue slot
#     taken when a client vanishes while its request is queued (proxy.PlantQueuedVanishKeepsSlot).
#     The minimised input is test/e2e/testdata/shrunk/queued-vanish.faults, replayed by
#     TestShrunkQueuedVanishScenarioReplays.
#   FuzzPoolOps (chunk 7): with MG_FUZZ_PLANT=reset-clears-suspect the pool core clears the
#     suspect flag when a reset completes, so a connection marked suspect while it was lent is
#     lent again without a check (pool.PlantResetClearsSuspect). The reference model reports
#     the lost flag as soon as it is lost, also while it settles the run, so ordinary inputs
#     reach it; no committed seed fails on its own. The shrunk operations are
#     resetClearsSuspectOps, replayed by TestShrunkResetClearsSuspectOrderingBugReplays.
#   FuzzAdmissionOps (chunk 8): with MG_FUZZ_PLANT=admission-remove-order a removal from the
#     middle of a tenant queue swaps the tail into the hole (admission.PlantRemoveSwapsTail), so
#     the queue order no longer matches the reference model. The seed corpus does not trigger
#     it; the fuzzer must.
# Every switch is reachable from tests only.
#
# Usage: scripts/fuzz-plant.sh
# Each run starts from the committed seed corpus with an empty fuzz cache, so earlier runs
# cannot change how fast it finds the bug. Any failing input a run writes under the package's
# testdata/fuzz/<target>/ is removed afterwards (it is a planted bug, not a finding).
# Exit status: 0 every planted bug was found in time, 1 one was not, 2 setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
secs=${FUZZ_SECONDS:-20}
notes=()

plant() {
  local target=$1 pkg=$2 value=$3 pattern=$4 what=$5
  local corpus="$pkg/testdata/fuzz/$target"
  local before log cache start rc took
  before=$(ls "$corpus" 2>/dev/null | sort)
  log=$(mktemp)
  cache=$(mktemp -d)
  start=$(date +%s)
  MG_FUZZ_PLANT=$value go test -count=1 -run '^$' -fuzz "^${target}\$" -fuzztime "${secs}s" "./$pkg" -args -test.fuzzcachedir="$cache" >"$log" 2>&1
  rc=$?
  took=$(( $(date +%s) - start ))
  rm -rf "$cache"
  for f in $(ls "$corpus" 2>/dev/null | sort); do
    grep -qxF "$f" <<<"$before" || rm -f "$corpus/$f"
  done
  [ -d "$corpus" ] && [ -z "$(ls -A "$corpus")" ] && rmdir "$corpus" "$(dirname "$corpus")" "$(dirname "$(dirname "$corpus")")" 2>/dev/null
  if [ "$rc" = 0 ] || ! grep -qE "$pattern" "$log"; then
    tail -n 20 "$log"
    rm -f "$log"
    echo "fuzz-plant: $target did not find the planted $what bug in ${secs}s" >&2
    return 1
  fi
  rm -f "$log"
  notes+=("$target found the planted $what bug after ${took}s")
  return 0
}

fail=0
plant FuzzEngineOps test/e2e queued-vanish 'queue holds' queue-slot || fail=1
plant FuzzPoolOps internal/pool reset-clears-suspect 'lent suspect' reset-clears-suspect || fail=1
plant FuzzAdmissionOps internal/admission admission-remove-order 'position [0-9]+: core|reference' remove-order || fail=1
[ "$fail" = 0 ] || exit 1
note=$(printf '%s; ' "${notes[@]}")
echo "NOTE: ${note%; } (budget ${secs}s each)"
