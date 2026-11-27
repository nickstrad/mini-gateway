#!/bin/bash
# Chunk 7 extras for the landing gate (G10): the pool core's reference-model property tests at
# full size, the planted-bug canaries and ablations of the pool scenarios (cap, creation rate,
# check deadline, suspect rule, on-switch tenant tag, breaker, jitter, the night trickle with
# every remedy off and each remedy alone), the fault driver's self-test, and both fuzz targets
# finding their planted ordering bugs within the fuzz budget.
set -u
set -o pipefail
cd "$(dirname "$0")/../../../.." || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
fail=0
run() { echo "extra: $*"; "$@" || fail=1; }
run go test -count=1 ./internal/pool
run go test -count=1 -timeout 40m -run 'TestCanaryPool|TestCanaryJoin|TestAblationColdStart|TestAblationRestart|TestAblationLifetimes|TestAblationNightTrickle|TestNightTrickleEachRemedy|TestPathFault' ./test/e2e
run go test -count=1 -run 'TestCore|TestJoin' ./internal/proxy
run scripts/fuzz-plant.sh
exit $fail
