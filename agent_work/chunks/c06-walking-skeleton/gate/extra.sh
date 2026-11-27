#!/bin/bash
# Chunk 6 extras for the landing gate (G10): the planted-bug canaries and ablations of the
# walking skeleton, the shrunk fuzz scenario, the planted ordering bug found by the fuzz
# target within the fuzz budget, and the simrun baseline SQL assertions with DuckDB required.
set -u
set -o pipefail
cd "$(dirname "$0")/../../../.." || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
fail=0
run() { echo "extra: $*"; "$@" || fail=1; }
run go test -count=1 -run 'Canary|Ablation|TestShrunkQueuedVanish|TestResultsThroughTheProxyEqualDirect|TestOpDecoding|TestFuzzEngineOpsCostIsBounded|TestSeedCorpus' ./test/e2e
run scripts/fuzz-plant.sh
run env MG_REQUIRE_DUCKDB=1 go test -count=1 -run 'TestCSVAssertBaselineCountsFromFlushedFiles|TestEveryDeclaredMetric' ./internal/obs/simrun
exit $fail
