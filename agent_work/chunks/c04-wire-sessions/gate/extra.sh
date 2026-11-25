#!/bin/bash
# Chunk 4 extra check (G10).
# 1. The CSV-flush assertions of the wire scenarios: every scenario is run (seed 1) through the
#    collector and the CSV adapter (internal/obs/simrun.Wire), test/analysis/<scenario>/*.sql
#    are run on the flushed files with scripts/csv-assert.sh; each must return zero rows, and
#    the runner must exit non-zero when a wrong number is planted in the final flush.
# 2. The planted-bug canary: with the "no bytes between S and the handshake result" rule
#    switched off, the smuggled-bytes scenario must fail its assertion.
# 3. Cross-process replay: the wire scenarios on seed 42 in two separate test processes give
#    the same trace and state digests.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v duckdb >/dev/null 2>&1 || PATH="/root/.local/bin:$PATH"
command -v duckdb >/dev/null 2>&1 || { echo "duckdb is not installed"; exit 1; }
MG_REQUIRE_DUCKDB=1 go test -count=1 -run '^(TestWireSQLCatchesPlantedNumbers|TestCSVAssertBaselineCountsFromFlushedFiles)$' ./internal/obs/simrun || { echo "SQL assertions failed"; exit 1; }
go test -count=1 -run '^TestPlantedEarlyBytesBugFailsTheSmuggleScenario$' ./internal/pgwire || { echo "canary failed"; exit 1; }
tmp=$(mktemp -d /tmp/c04-extra.XXXXXX) || exit 2
trap 'rm -rf "$tmp"' EXIT
for i in 1 2; do
  MG_WIRE_DIGESTS="$tmp/digests.$i" go test -count=1 -run '^TestWireDigests$' ./internal/pgwire >"$tmp/log.$i" 2>&1 || { cat "$tmp/log.$i"; exit 1; }
done
[ -s "$tmp/digests.1" ] || { echo "no digests written"; exit 1; }
cmp -s "$tmp/digests.1" "$tmp/digests.2" || { echo "digests differ across processes"; diff "$tmp/digests.1" "$tmp/digests.2"; exit 1; }
echo "NOTE: SQL assertions on $(ls test/analysis | grep -c '^wire-') wire scenarios, canary, and $(wc -l <"$tmp/digests.1") scenario digests identical across processes"
