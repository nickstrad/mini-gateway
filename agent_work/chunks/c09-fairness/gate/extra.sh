#!/bin/bash
# Chunk 9 gate extra (G10): the fairness chunk's own list.
# 1. scripts/fuzz-plant.sh: every operation fuzz target, FuzzFairnessOps included, finds its
#    planted ordering bug within the budget (claim 15).
# 2. The planted-bug canaries of the fair dispatcher, run on their own so a skip under -short
#    elsewhere cannot hide them: the share bound, the round-robin step, the Unpop restore (core
#    property runs), held transactions counted and the Engine.Invariants block (engine and
#    scenario).
set -u
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
scripts/fuzz-plant.sh || exit 1
go test -count=1 -run '^(TestCanary|TestPlanted).*' ./internal/admission || exit 1
go test -count=1 -run '^(TestCanaryFair.*|TestFairEngineInvariantsCatchInFlightThatSkipsHeldTransactions)$' ./internal/proxy ./test/e2e || exit 1
echo "NOTE: fairness canaries and planted fuzz bugs caught"
