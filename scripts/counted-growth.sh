#!/bin/bash
# The linear-growth tests with work counting compiled in. Counting exists only under the
# build tag clscount, so the default `go test ./...` cannot run these checks.
#
# Usage: scripts/counted-growth.sh
#   COUNTED_GO=<command>   run in place of `go` (the tests in scripts/ use a stub)
#
# Runs `go test -tags clscount -count=1 -run Linearly -v ./internal/pgwire/` and fails when
# that run fails or when any test named in $required did not pass in it.
#
# Exit status: 0 green, 1 the run failed or a required test did not run.
set -u
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
go_cmd=${COUNTED_GO:-go}
required="TestCountedWorkGrowsLinearly TestCountedCheckRejectsWorkThatDoesNotGrowLinearly TestLexOperatorRunsGrowLinearly TestClassifierSpanKeywordChecksGrowLinearly TestClassifierWorkGrowsLinearly"
rc=0
out=$("$go_cmd" test -tags clscount -count=1 -run Linearly -v ./internal/pgwire/ 2>&1) || rc=$?
passed=$(grep -c -- '^--- PASS: ' <<<"$out" || true)
echo "NOTE: -tags clscount -run Linearly: $passed tests passed"
printf '%s\n' "$out"
if [ "$rc" != 0 ]; then
  echo "counted growth: the tagged run failed (exit $rc)"
  exit 1
fi
missing=0
for t in $required; do
  if ! grep -q -- "^--- PASS: $t " <<<"$out"; then
    echo "counted growth: $t did not pass in the tagged run (not found under -tags clscount?)"
    missing=1
  fi
done
exit $missing
