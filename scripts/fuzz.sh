#!/bin/bash
# Timed fuzzing: every fuzz target in the module, one after another, with the executions
# per second of each and its class (PLAN.md section 6.6).
#
# Usage: scripts/fuzz.sh
#   FUZZ_SECONDS=20       time per target (the gate's budget; on demand use minutes)
#   FUZZ_RATE_FLOOR=100   executions per second below which a simulation-level target is
#                         classed as a seed sweep
#   FUZZ_REPORT=<file>    also write the summary table there as TSV (header row first)
#   FUZZ_GO=<command>     the command run in place of `go` for the fuzzing step only (the
#                         test seam of scripts/fuzz_test.go; the gate unsets it)
# Targets are found by name: every `func FuzzXxx(` in a _test.go file outside tools/,
# agent_work/ and testdata/. Each runs with
#   go test -run '^$' -fuzz '^FuzzXxx$' -fuzztime <FUZZ_SECONDS>s ./<package>
# A crasher makes go test fail and writes the input under the package's
# testdata/fuzz/FuzzXxx/, which then runs as a plain test from then on.
# A run that finds no fuzz target at all is a failure, not a pass.
#
# The rate. Go prints `fuzz: elapsed: <d>, execs: <N> (<M>/sec)` as it goes; the last such
# line of a target's run is its summary. The rate reported is N divided by the elapsed whole
# seconds of that line (at least 1). Go's own M is the rate over the last interval only and
# can be 0 on the final line, so it is not used. A target with no such line (its run never
# got past the baseline coverage, or the go tool fuzzed nothing) has no rate: that target
# is a failure (fail closed), class `unreadable`.
#
# The class. A target is simulation-level when its test package depends on internal/sim
# (`go list -deps -test`, the package itself included). A simulation-level target below
# FUZZ_RATE_FLOOR is a `seed-sweep`: it still runs and can still fail, but it does not count
# as fuzzing. Every other target with a rate is `fuzz`. A seed sweep is not a failure.
#
# Output: the go test output of each target, then a table (package, target, executions,
# executions per second, class) and a NOTE line with the counts.
#
# Exit status: 0 green, 1 a target failed, has no readable rate, or cannot be classed, or
# no target exists, 2 usage or setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"

secs=${FUZZ_SECONDS:-20}
floor=${FUZZ_RATE_FLOOR:-100}
report=${FUZZ_REPORT:-}
fuzz_go=${FUZZ_GO:-go}
[[ "$secs" =~ ^[1-9][0-9]*$ ]] || { echo "fuzz.sh: FUZZ_SECONDS must be a positive whole number" >&2; exit 2; }
[[ "$floor" =~ ^[1-9][0-9]*$ ]] || { echo "fuzz.sh: FUZZ_RATE_FLOOR must be a positive whole number" >&2; exit 2; }
module=$(go list -m 2>/dev/null) || { echo "fuzz.sh: cannot read the module path (go list -m)" >&2; exit 2; }
work=$(mktemp -d /tmp/fuzz-sh.XXXXXX) || exit 2
trap 'rm -rf "$work"' EXIT

targets=$(grep -rHoE --include='*_test.go' '^func Fuzz[A-Za-z0-9_]*\(' . \
  | grep -vE '^\./(tools|agent_work|\.git)/|/testdata/' \
  | sed -E 's|^\./(.*)/[^/]*_test\.go:func (Fuzz[A-Za-z0-9_]*)\($|\1 \2|' | sort -u)
count=$(grep -c . <<<"$targets" || true)
if [ "$count" = 0 ]; then
  echo "fuzz.sh: no fuzz target found" >&2
  exit 1
fi
echo "fuzz.sh: $count fuzz targets, ${secs}s each, rate floor ${floor}/s for simulation-level targets"

# elapsed_seconds <go duration rounded to the second, like 20s, 1m0s, 1h2m3s>
elapsed_seconds() {
  [[ "$1" =~ ^(([0-9]+)h)?(([0-9]+)m)?([0-9]+)s$ ]] || return 1
  echo $(( 10#${BASH_REMATCH[2]:-0} * 3600 + 10#${BASH_REMATCH[4]:-0} * 60 + 10#${BASH_REMATCH[5]} ))
}

# read_rate <log>: prints "<execs> <execs per second>" from the last summary line.
read_rate() {
  local line el
  line=$(grep -E '^fuzz: elapsed: [0-9hms]+, execs: [0-9]+ \([0-9]+/sec\)' "$1" | tail -n 1)
  [[ "$line" =~ ^fuzz:\ elapsed:\ ([0-9hms]+),\ execs:\ ([0-9]+)\ \( ]] || return 1
  el=$(elapsed_seconds "${BASH_REMATCH[1]}") || return 1
  [ "$el" -ge 1 ] || el=1
  echo "${BASH_REMATCH[2]} $(( ${BASH_REMATCH[2]} / el ))"
}

# sim_level <dir>: 0 when the test package in <dir> depends on internal/sim, 1 when not,
# 2 when go list fails.
declare -A level_of=()
sim_level() {
  if [ -z "${level_of[$1]:-}" ]; then
    local deps
    deps=$(go list -deps -test "./$1" 2>"$work/golist.err") || { cat "$work/golist.err" >&2; return 2; }
    if grep -qxF "$module/internal/sim" <<<"$deps"; then level_of[$1]=sim; else level_of[$1]=plain; fi
  fi
  [ "${level_of[$1]}" = sim ]
}

rc=0
rows=()
sweeps=0
fuzzed=0
i=0
while read -r dir name; do
  i=$((i + 1))
  log="$work/$i.log"
  echo "fuzz: ./$dir $name"
  if ! "$fuzz_go" test -run '^$' -fuzz "^${name}\$" -fuzztime "${secs}s" "./$dir" 2>&1 | tee "$log"; then
    echo "fuzz: $name in ./$dir FAILED" >&2
    rc=1
  fi
  if ! rate=$(read_rate "$log"); then
    echo "fuzz: $name in ./$dir: no execution rate in the go test output (no 'fuzz: elapsed: ..., execs: N (M/sec)' line)" >&2
    rows+=("$dir	$name	-	-	unreadable")
    rc=1
    continue
  fi
  read -r execs per_sec <<<"$rate"
  lvl=0; sim_level "$dir" || lvl=$?
  if [ $lvl = 2 ]; then
    echo "fuzz: $name in ./$dir: cannot list the dependencies of its test package" >&2
    rows+=("$dir	$name	$execs	$per_sec	unclassified")
    rc=1
    continue
  fi
  class=fuzz
  if [ $lvl = 0 ] && [ "$per_sec" -lt "$floor" ]; then class=seed-sweep; sweeps=$((sweeps + 1)); else fuzzed=$((fuzzed + 1)); fi
  rows+=("$dir	$name	$execs	$per_sec	$class")
done <<<"$targets"

table=$(printf 'package\ttarget\texecs\texecs_per_sec\tclass\n'; printf '%s\n' "${rows[@]}")
echo "fuzz: summary"
column -t -s $'\t' <<<"$table" 2>/dev/null || printf '%s\n' "$table"
echo "NOTE: $count fuzz targets, ${secs}s each: $fuzzed fuzz, $sweeps seed-sweep (floor ${floor}/s), $((count - fuzzed - sweeps)) without a rate"
if [ -n "$report" ]; then
  printf '%s\n' "$table" > "$report" || { echo "fuzz.sh: cannot write FUZZ_REPORT $report" >&2; exit 2; }
fi
exit $rc
