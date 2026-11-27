#!/bin/bash
# Timed fuzzing: every fuzz target in the module, one after another, with the executions
# per second of each and its class (PLAN.md section 6.6).
#
# Usage: scripts/fuzz.sh
#   FUZZ_SECONDS=20       time per target (the gate's budget; on demand use minutes)
#   FUZZ_RATE_FLOOR=100   executions per second below which a simulation-level target is
#                         classed as a seed sweep
#   FUZZ_MAX_RETRIES=2    maximum number of targets retried once on Go's own deadline
#                         error (0 turns the retry off; the gate refuses to count a run
#                         above 2 for landing)
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
# The rate. Go prints `fuzz: elapsed: <d>, execs: <N> (<M>/sec)` as it goes; the last line
# of a target's run that starts `fuzz: elapsed:` is its summary, and it must have that form:
# a rate is never taken from an earlier line when the last one cannot be read. The rate reported is N divided by the elapsed whole
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
# Deadline retry (CR-2). Go's fuzz tool occasionally ends a target at -fuzztime with a
# bare `context deadline exceeded` under `--- FAIL: <target>` and no failing input. A
# target is retried once when, and only when, ALL five conditions hold: non-zero exit, a
# `--- FAIL: <target>` block whose only message is `context deadline exceeded`, no
# "Failing input written to" line, no panic or other failure text, and no new file under
# the package's testdata/fuzz/<target>/. At most FUZZ_MAX_RETRIES targets are retried in
# one run; the second run's result and rate stand.
#
# Output: the go test output of each target, a `fuzz: RETRY: <dir> <target>: go's own
# deadline, no failing input` line for each retry, then a table (package, target,
# executions, executions per second, class, retried) and a NOTE line with the counts.
# When the run fails, its last line names every failed target:
# `fuzz: FAILED: <dir> <target>, ...`.
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
max_retries=${FUZZ_MAX_RETRIES:-2}
report=${FUZZ_REPORT:-}
fuzz_go=${FUZZ_GO:-go}
[[ "$secs" =~ ^[1-9][0-9]*$ ]] || { echo "fuzz.sh: FUZZ_SECONDS must be a positive whole number" >&2; exit 2; }
[[ "$floor" =~ ^[1-9][0-9]*$ ]] || { echo "fuzz.sh: FUZZ_RATE_FLOOR must be a positive whole number" >&2; exit 2; }
[[ "$max_retries" =~ ^(0|[1-9][0-9]{0,8})$ ]] || { echo "fuzz.sh: FUZZ_MAX_RETRIES must be a non-negative whole number" >&2; exit 2; }
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

# read_rate <log>: prints "<execs> <execs per second>" from the last progress line.
read_rate() {
  local line el
  line=$(grep -E '^fuzz: elapsed: ' "$1" | tail -n 1)
  [[ "$line" =~ ^fuzz:\ elapsed:\ ([0-9hms]+),\ execs:\ ([0-9]+)\ \([0-9]+/sec\) ]] || return 1
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

# list_corpus <dir> <target>: sorted paths under <dir>/testdata/fuzz/<target>/, if present.
list_corpus() {
  local d="$1/testdata/fuzz/$2"
  if [ -d "$d" ]; then
    (cd "$d" && find . -mindepth 1 | LC_ALL=C sort)
  fi
}

# is_deadline_signature <log> <target>: 0 iff the log is Go's own deadline error for
# <target> with no failing input, no panic and no other failure text.
is_deadline_signature() {
  awk -v target="$2" '
    BEGIN {
      state = 0
      bad = 0
      saw_exit = 0
      saw_pkg_fail = 0
      fail_prefix = "--- FAIL: " target
      run_line = "=== RUN   " target
    }
    {
      line = $0
      if (state == 0) {
        if (line == "" ||
            line == "warning: starting with empty corpus" ||
            line == "warning: the test binary was not built with coverage instrumentation, so fuzzing will run without coverage guidance and may be inefficient" ||
            line == "waiting for fuzzing process to terminate..." ||
            line == run_line ||
            line ~ /^fuzz: elapsed: [0-9hms]+, (gathering baseline coverage|testing seed corpus): [0-9]+\/[0-9]+ completed(, now fuzzing with [0-9]+ workers)?$/ ||
            line ~ /^fuzz: elapsed: [0-9hms]+, execs: [0-9]+ \([0-9]+\/sec\)(, new interesting: [0-9]+ \(total: [0-9]+\))?$/) {
          next
        }
        if (line == fail_prefix ||
            (substr(line, 1, length(fail_prefix)) == fail_prefix &&
             substr(line, length(fail_prefix) + 1) ~ /^ \(([0-9]+h)?([0-9]+m)?[0-9]+(\.[0-9]+)?s\)$/)) {
          state = 1
          next
        }
        bad = 1
        exit 1
      } else if (state == 1) {
        if (line ~ /^[ \t]+context deadline exceeded$/) {
          state = 2
          next
        }
        bad = 1
        exit 1
      } else if (state == 2) {
        if (line == "FAIL") {
          state = 3
          next
        }
        bad = 1
        exit 1
      } else {
        if (line == "") {
          next
        }
        if (!saw_exit && !saw_pkg_fail && line == "exit status 1") {
          saw_exit = 1
          next
        }
        if (!saw_pkg_fail && line ~ /^FAIL[ \t]+[^ \t]+[ \t]+([0-9]+h)?([0-9]+m)?[0-9]+(\.[0-9]+)?s$/) {
          saw_pkg_fail = 1
          next
        }
        bad = 1
        exit 1
      }
    }
    END {
      if (bad || state != 3) exit 1
    }
  ' "$1"
}

# fail_target <dir> <name>: marks the run failed and lists the target once.
fail_target() {
  rc=1
  [ "$last_failed" = "$1 $2" ] || failed+=("$1 $2")
  last_failed="$1 $2"
}

rc=0
rows=()
failed=()
last_failed=
sweeps=0
fuzzed=0
unreadable=0
unclassified=0
retried_total=0
i=0
while read -r dir name; do
  i=$((i + 1))
  log="$work/$i.log"
  retried=0
  list_corpus "$dir" "$name" > "$work/corpus.before"
  echo "fuzz: ./$dir $name"
  run_rc=0
  "$fuzz_go" test -run '^$' -fuzz "^${name}\$" -fuzztime "${secs}s" "./$dir" 2>&1 | tee "$log" || run_rc=$?
  if [ "$run_rc" != 0 ]; then
    list_corpus "$dir" "$name" > "$work/corpus.after"
    new_corpus=$(LC_ALL=C comm -13 "$work/corpus.before" "$work/corpus.after")
    if [ -z "$new_corpus" ] && [ "$retried_total" -lt "$max_retries" ] && is_deadline_signature "$log" "$name"; then
      retried=1
      retried_total=$((retried_total + 1))
      echo "fuzz: RETRY: $dir $name: go's own deadline, no failing input"
      log="$work/$i.retry.log"
      run_rc=0
      "$fuzz_go" test -run '^$' -fuzz "^${name}\$" -fuzztime "${secs}s" "./$dir" 2>&1 | tee "$log" || run_rc=$?
      if [ "$run_rc" != 0 ]; then
        echo "fuzz: $name in ./$dir FAILED" >&2
        fail_target "$dir" "$name"
      fi
    else
      echo "fuzz: $name in ./$dir FAILED" >&2
      fail_target "$dir" "$name"
    fi
  fi
  if ! rate=$(read_rate "$log"); then
    echo "fuzz: $name in ./$dir: no execution rate in the go test output (no 'fuzz: elapsed: ..., execs: N (M/sec)' line)" >&2
    rows+=("$dir	$name	-	-	unreadable	$retried")
    unreadable=$((unreadable + 1))
    fail_target "$dir" "$name"
    continue
  fi
  read -r execs per_sec <<<"$rate"
  lvl=0; sim_level "$dir" || lvl=$?
  if [ $lvl = 2 ]; then
    echo "fuzz: $name in ./$dir: cannot list the dependencies of its test package" >&2
    rows+=("$dir	$name	$execs	$per_sec	unclassified	$retried")
    unclassified=$((unclassified + 1))
    fail_target "$dir" "$name"
    continue
  fi
  class=fuzz
  if [ $lvl = 0 ] && [ "$per_sec" -lt "$floor" ]; then class=seed-sweep; sweeps=$((sweeps + 1)); else fuzzed=$((fuzzed + 1)); fi
  rows+=("$dir	$name	$execs	$per_sec	$class	$retried")
done <<<"$targets"

table=$(printf 'package\ttarget\texecs\texecs_per_sec\tclass\tretried\n'; printf '%s\n' "${rows[@]}")
echo "fuzz: summary"
column -t -s $'\t' <<<"$table" 2>/dev/null || printf '%s\n' "$table"
echo "NOTE: $count fuzz targets, ${secs}s each: $fuzzed fuzz, $sweeps seed-sweep (floor ${floor}/s), $unreadable without a rate, $unclassified unclassified, $retried_total retried"
if [ -n "$report" ]; then
  printf '%s\n' "$table" > "$report" || { echo "fuzz.sh: cannot write FUZZ_REPORT $report" >&2; exit 2; }
fi
if [ $rc != 0 ]; then
  list=$(printf '%s, ' "${failed[@]}")
  echo "fuzz: FAILED: ${list%, }"
fi
exit $rc
