#!/bin/bash
# Timed fuzzing: every fuzz target in the module, one after another.
#
# Usage: scripts/fuzz.sh
#   FUZZ_SECONDS=20     time per target (the gate's budget; on demand use minutes)
# Targets are found by name: every `func FuzzXxx(` in a _test.go file outside tools/,
# agent_work/ and testdata/. Each runs with
#   go test -run '^$' -fuzz '^FuzzXxx$' -fuzztime <FUZZ_SECONDS>s ./<package>
# A crasher makes go test fail and writes the input under the package's
# testdata/fuzz/FuzzXxx/, which then runs as a plain test from then on.
# A run that finds no fuzz target at all is a failure, not a pass.
#
# Exit status: 0 green, 1 a target failed or no target exists, 2 usage or setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"

secs=${FUZZ_SECONDS:-20}
[[ "$secs" =~ ^[1-9][0-9]*$ ]] || { echo "fuzz.sh: FUZZ_SECONDS must be a positive whole number" >&2; exit 2; }

targets=$(grep -rHoE --include='*_test.go' '^func Fuzz[A-Za-z0-9_]*\(' . \
  | grep -vE '^\./(tools|agent_work|\.git)/|/testdata/' \
  | sed -E 's|^\./(.*)/[^/]*_test\.go:func (Fuzz[A-Za-z0-9_]*)\($|\1 \2|' | sort -u)
count=$(grep -c . <<<"$targets" || true)
if [ "$count" = 0 ]; then
  echo "fuzz.sh: no fuzz target found" >&2
  exit 1
fi
echo "NOTE: $count fuzz targets, ${secs}s each"
rc=0
while read -r dir name; do
  echo "fuzz: ./$dir $name"
  if ! go test -run '^$' -fuzz "^${name}\$" -fuzztime "${secs}s" "./$dir"; then
    echo "fuzz: $name in ./$dir FAILED" >&2
    rc=1
  fi
done <<<"$targets"
exit $rc
