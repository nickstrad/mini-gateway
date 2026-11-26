#!/bin/bash
# Chunk 5 extra check (G10): the reject-list scenarios end to end, their cross-process
# replay, the planted-bug canaries and the SQL assertions over flushed files.
# 1. Runs TestScenarioClassifierRejectList and TestScenarioClassifierRejectOnly (pinned
#    seeds plus 20 more) in two separate processes; every run must pass and the trace and
#    state digests printed by each seed must be identical across the two processes.
# 2. Runs the canary TestClassifierPlantedSetForwardableIsCaught: with SET treated as
#    forwardable the scenario must fail because a rejected statement reached the fake
#    Postgres and its session state changed.
# 3. Runs the safe-shape canary TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails:
#    with the alphabet check skipped, the "any byte outside the alphabet breaks a fit"
#    property must fail.
# 4. Runs the SQL assertions of test/analysis/classifier-*/ over freshly flushed runs and
#    shows each file fails on a planted wrong number (DuckDB required: MG_REQUIRE_DUCKDB=1).
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
run() {
  DST_SEEDS=20 go test -count=1 -v -run '^TestScenarioClassifier(RejectList|RejectOnly)$' ./internal/pgwire 2>&1 | tee "${RUN_DIR:-/tmp}/c05-extra-$1.log" | grep -oE 'digests TestScenarioClassifier[^ ]* seed=[0-9]+ trace=[0-9a-f]+ state=[0-9a-f]+' | sort
}
a=$(run 1) || { echo "scenario run 1 failed"; exit 1; }
b=$(run 2) || { echo "scenario run 2 failed"; exit 1; }
n=$(grep -c . <<<"$a" || true)
[ "$n" -ge 40 ] || { echo "only $n scenario digests"; exit 1; }
[ "$a" = "$b" ] || { echo "digests differ across processes"; diff <(echo "$a") <(echo "$b") | head; exit 1; }
go test -count=1 -run '^(TestClassifierPlantedSetForwardableIsCaught|TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails)$' ./internal/pgwire >/dev/null || { echo "canary failed"; exit 1; }
MG_REQUIRE_DUCKDB=1 go test -count=1 -run '^(TestClassifierSQLCatchesPlantedNumbers|TestClassifierRunsUseTheScenarioRunIDs|TestClassifierSQLFilesAreTheOnesInTheTree)$' ./internal/obs/simrun >/dev/null || { echo "SQL assertions over flushed files failed"; exit 1; }
echo "NOTE: $n reject-list scenario runs replayed across processes with identical digests; SET-forwardable and alphabet canaries caught; classifier SQL files catch planted numbers"
