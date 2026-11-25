#!/bin/bash
# Chunk 3 extra check (G10).
#  1. The DuckDB end-to-end and the assertion runner's self-tests, with MG_REQUIRE_DUCKDB=1 so
#     a missing duckdb is a failure, never a skip.
#  2. The planted-bug canaries: the CSV round trip must catch a dropped bucket row and a stale
#     value, on the fixture and on a baseline scenario run.
#  3. Cross-process byte identity of the flushed files: every baseline scenario, seed 1, flushed
#     by two separate test processes into two directories; the files must be identical.
#  4. The async CSV writer under the race detector, ten times.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"
tmp=$(mktemp -d /tmp/c03-extra.XXXXXX) || exit 2
trap 'rm -rf "$tmp"' EXIT

MG_REQUIRE_DUCKDB=1 go test -count=1 -run '^TestCSVAssert' -v ./internal/obs/simrun >"$tmp/duckdb.log" 2>&1 \
  || { tail -n 40 "$tmp/duckdb.log"; echo "DuckDB end-to-end or runner self-tests failed"; exit 1; }
grep -q -- '--- SKIP' "$tmp/duckdb.log" && { echo "a DuckDB test skipped"; exit 1; }
go test -count=1 -run 'PlantedBug' -v ./internal/obs/csvflush >"$tmp/canary.log" 2>&1 \
  || { tail -n 40 "$tmp/canary.log"; echo "planted-bug canaries failed"; exit 1; }
for i in 1 2; do
  RUN_DIR="$tmp/runs$i" go test -count=1 -run '^TestScenarioFlush$' ./internal/obs/simrun -seed=1 >"$tmp/flush$i.log" 2>&1 \
    || { tail -n 40 "$tmp/flush$i.log"; echo "flush run $i failed"; exit 1; }
done
n=$(find "$tmp/runs1" -name '*.csv' | wc -l)
[ "$n" -ge 15 ] || { echo "expected at least 15 flushed files, found $n"; exit 1; }
diff -r "$tmp/runs1" "$tmp/runs2" >/dev/null || { echo "flushed files differ between two processes"; exit 1; }
go test -race -count=10 -run 'Async' ./internal/obs/csvflush >"$tmp/race.log" 2>&1 \
  || { tail -n 40 "$tmp/race.log"; echo "async writer race run failed"; exit 1; }
echo "NOTE: DuckDB e2e and runner self-tests ($(duckdb --version | head -n1)), canaries, $n files byte-identical across processes, async writer x10 under -race"
