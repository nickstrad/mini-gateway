#!/bin/bash
# Prints the chunk 8 admission head-to-head table: one DuckDB query
# (test/analysis/admission-head-to-head/head-to-head.sql) over the flushed runs of
# admission-h2h-l120, -l200 and -l1000 found under a root.
#
# Usage:
#   scripts/admission-head-to-head.sh <root>            print the table (markdown)
#   scripts/admission-head-to-head.sh --run [<root>]     run the scenarios into <root> (default a
#                                                        temporary directory), then print the table
#   scripts/admission-head-to-head.sh --check            run the scenarios and require the output to
#                                                        equal the committed head-to-head.md
# Exit status: 0 ok, 1 a check failed or the query found no runs, 2 usage or setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"
command -v duckdb >/dev/null 2>&1 || { echo "admission-head-to-head: duckdb is not installed" >&2; exit 2; }
dir=test/analysis/admission-head-to-head
committed=$dir/head-to-head.md

run() {
  RUN_DIR="$1" DST_SEEDS=1 go test -count=1 -timeout 30m -run '^TestScenarioAdmissionHeadToHead$' ./test/e2e >"$1/h2h.log" 2>&1 \
    || { tail -n 20 "$1/h2h.log" >&2; echo "admission-head-to-head: the scenarios failed" >&2; exit 1; }
}

table() {
  local runs=() d m='' h='' r=''
  for d in "$1"/admission-h2h-l*/*/*; do [ -f "$d/run.csv" ] && runs+=("$d"); done
  [ ${#runs[@]} -gt 0 ] || { echo "admission-head-to-head: no admission-h2h runs under $1" >&2; exit 1; }
  for d in "${runs[@]}"; do
    m="$m${m:+, }'$d/metrics.csv'"; h="$h${h:+, }'$d/histograms.csv'"; r="$r${r:+, }'$d/run.csv'"
  done
  { printf "SET VARIABLE metrics_files = [%s];\nSET VARIABLE histograms_files = [%s];\nSET VARIABLE run_files = [%s];\n" "$m" "$h" "$r"
    cat test/analysis/views.sql "$dir/head-to-head.sql"; } | duckdb -batch -bail -markdown :memory:
}

case "${1:-}" in
  --run)
    out=${2:-$(mktemp -d /tmp/h2h.XXXXXX)}; mkdir -p "$out"; run "$out"; table "$out" ;;
  --check)
    out=$(mktemp -d /tmp/h2h.XXXXXX); run "$out"
    if ! diff -u "$committed" <(table "$out"); then echo "admission-head-to-head: the table differs from $committed" >&2; exit 1; fi
    rm -rf "$out"; echo "NOTE: the head-to-head table is reproduced exactly from the flushed runs" ;;
  ''|-h|--help) sed -n '2,12p' "$0"; [ -n "${1:-}" ]; exit $? ;;
  *) table "$1" ;;
esac
