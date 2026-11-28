#!/bin/bash
# Chunk 11 gate extra (G10): the coalescer chunk's own list.
# 1. scripts/fuzz-plant.sh: every operation fuzz target, FuzzCoalesceOps included, finds its planted bug.
# 2. The core's reference-model property test at full size and the six planted-bug canaries.
# 3. The engine's coalescer tests (bytes shared, strict hold, leaver, handoff, Invariants block, quiesce)
#    and the join tests with chunks 9 and 12 (TestJoin*: flights, fairness, retries, the default rule).
# 4. The 1,000-client hot-key scenarios in both join modes (pinned seeds, replay, ablation, direct arm)
#    and the headline numbers test, which -short skips everywhere else.
set -u
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
scripts/fuzz-plant.sh || exit 1
go test -count=1 ./internal/coalesce/ || exit 1
go test -count=1 -run 'Coalesc|OnSwitch' ./internal/config/ || exit 1
go test -count=1 -run 'Follower|Strict|Leaver|Owner|InvariantsCover|Quiesces|QueuedFlightAtItsDeadline|ShutdownSendsNothing|TestJoin' ./internal/proxy/ || exit 1
MG_C11_HEAVY=1 MG_C11_FULL_SWEEP=1 DST_SEEDS=3 DST_SEED_BASE=1 go test -count=1 -timeout 30m -run 'TestScenarioCoalesce/coalesce-hot-key|TestCoalesceHotKeyNumbers' ./test/e2e/ || exit 1
echo "NOTE: fuzz plants found; coalescer core, engine tests and the 1,000-client hot-key scenarios green"
