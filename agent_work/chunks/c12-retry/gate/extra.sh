#!/bin/bash
# Chunk 12 gate extra (G10): the retry chunk's own end-to-end list.
# 1. scripts/fuzz-plant.sh: every operation fuzz target, FuzzRetryOps included, finds its planted
#    ordering bug (retry: the budget withdrawn before the eligibility checks).
# 2. The retry scenario canaries and the ablation arm: the storm and the no-double-apply sweep fail
#    with the reached-no condition removed, and the retry-everything arm writes twice.
set -u
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
scripts/fuzz-plant.sh || exit 1
go test -count=1 -run '^TestCanaryRetry' ./test/e2e/ || exit 1
DST_SEEDS=1 go test -count=1 -run '^TestScenarioRetry$/.*/everything$' ./test/e2e/ || exit 1
