#!/bin/bash
# SQL assertions over flushed CSV metric files, with DuckDB (PLAN.md sections 6.5, 7.7, 7.8).
#
# Usage:
#   scripts/csv-assert.sh <run-dir> <sql-file>...   run the SQL files over every run under <run-dir>
#   scripts/csv-assert.sh <root>...                 run every test/analysis/<scenario>/*.sql over the
#                                                   runs of that scenario under the roots
#
# Layout. A run directory holds metrics.csv, histograms.csv and run.csv (schema version 1). Under
# a root, a run lives at <root>/<scenario>/<seed>/<arm>/, which is its run_id
# (`<scenario>/<seed>/<arm>`). <run-dir> may be one run directory or a root.
# The scenario tests write their runs under $RUN_DIR when it is set; the gate's G7 passes the
# sweep's and tier R's run directories as roots.
#
# A SQL file returns the rows that VIOLATE its claim: zero rows is a pass. Its header names the
# metrics it needs, one or more `component.metric` after `-- requires:`; the runner checks that
# the final flush of the runs has rows for each before running the query.
#
# Fails closed (exit 1), never skips: a returned row; a run directory without run.csv; a file
# whose header or schema_version is not version 1; a required metric with no rows; a SQL file
# without a `-- requires:` line; a scenario with SQL files and no run; no run at all; a root
# that does not exist; DuckDB missing or not the pinned version family (1.5.x).
# Exit status: 0 green, 1 a check failed, 2 usage or setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
analysis="$root/test/analysis"
views="$analysis/views.sql"
pinned='^v1\.5\.'
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"

die() { echo "csv-assert: $*" >&2; exit 1; }
[ $# -ge 1 ] || { sed -n '2,24p' "$0" >&2; exit 2; }
case "$1" in -h|-help|--help) sed -n '2,24p' "$0"; exit 0 ;; esac

command -v duckdb >/dev/null 2>&1 || die "FAIL: duckdb is not installed (the pinned family is 1.5.x)"
version=$(duckdb --version 2>&1 | head -n1)
[[ "$version" =~ $pinned ]] || die "FAIL: duckdb version '$version' is not the pinned family 1.5.x"
[ -f "$views" ] || die "FAIL: $views is missing"

metrics_header='schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value'
hist_header='schema_version,run_id,flush_seq,t_ns,instance,component,metric,tenant,outcome,reason,mode,unit,bucket_lo,bucket_hi,count'
run_header='schema_version,run_id,scenario,tier,seed,arm,instances,build,config_fingerprint,flushes,duration_ns'

# run_dirs <dir>: every directory at or below <dir> holding one of the three files, sorted.
run_dirs() {
  find "$1" \( -name metrics.csv -o -name histograms.csv -o -name run.csv \) -type f -printf '%h\n' | LC_ALL=C sort -u
}

# check_run <dir>: run.csv present, every header and every schema_version is version 1.
check_run() {
  local d=$1 f want
  [ -f "$d/run.csv" ] || { echo "FAIL: $d: missing run.csv (the run did not finish)"; return 1; }
  for f in metrics.csv histograms.csv run.csv; do
    [ -f "$d/$f" ] || { echo "FAIL: $d: missing $f"; return 1; }
    case $f in metrics.csv) want=$metrics_header ;; histograms.csv) want=$hist_header ;; run.csv) want=$run_header ;; esac
    [ "$(head -n1 "$d/$f")" = "$want" ] || { echo "FAIL: $d/$f: unknown schema (header is not schema version 1)"; return 1; }
    if awk -F, 'NR > 1 && $1 != "1" { bad = 1 } END { exit !bad }' "$d/$f"; then
      echo "FAIL: $d/$f: unknown schema version $(awk -F, 'NR > 1 && $1 != "1" { print $1; exit }' "$d/$f")"
      return 1
    fi
  done
  [ "$(wc -l <"$d/run.csv")" -eq 2 ] || { echo "FAIL: $d/run.csv: want exactly one row"; return 1; }
}


# preamble <dir>...: the SQL that defines the views over the given run directories.
preamble() {
  local m='' h='' r='' d e
  for d in "$@"; do
    e=${d//\'/\'\'}
    m="$m${m:+, }'$e/metrics.csv'"; h="$h${h:+, }'$e/histograms.csv'"; r="$r${r:+, }'$e/run.csv'"
  done
  printf "SET VARIABLE metrics_files = [%s];\nSET VARIABLE histograms_files = [%s];\nSET VARIABLE run_files = [%s];\n" "$m" "$h" "$r"
  cat "$views"
}

failures=0
checked=0
fail() { echo "$1"; failures=$((failures + 1)); }

# assert <sql-file> <dir>...: run one SQL file over the given runs.
assert() {
  local sql=$1; shift
  local req out name rc
  req=$(grep -m1 -E '^-- requires:' "$sql" | sed -E 's/^-- requires:[[:space:]]*//')
  [ -n "$req" ] || { fail "FAIL: $sql: no '-- requires: component.metric' line (counters must move)"; return; }
  for name in $req; do
    [[ "$name" =~ ^[a-z]+\.[a-z0-9_]+$ ]] || { fail "FAIL: $sql: bad requirement '$name'"; return; }
    out=$( { preamble "$@"; echo "SELECT count(*) FROM final WHERE component = '${name%%.*}' AND metric = '${name#*.}';"; } \
      | duckdb -batch -bail -csv -noheader :memory: 2>&1) || { fail "FAIL: $sql: duckdb: $out"; return; }
    [ "$out" -gt 0 ] 2>/dev/null || { fail "FAIL: $sql: no rows for the metric under test $name in the final flush ($out)"; return; }
  done
  rc=0
  out=$( { preamble "$@"; cat "$sql"; } | duckdb -batch -bail -csv -noheader :memory: 2>&1) || rc=$?
  checked=$((checked + 1))
  if [ $rc != 0 ]; then fail "FAIL: $sql: duckdb exit $rc: $out"; return; fi
  if [ -n "$out" ]; then
    fail "FAIL: $sql: $(printf '%s\n' "$out" | grep -c '') violating rows over $# runs:"
    printf '%s\n' "$out" | head -n 20 | sed 's/^/  /'
    return
  fi
  echo "ok: ${sql#"$root"/} over $# runs"
}

mode=all
for a in "$@"; do case "$a" in *.sql) mode=explicit ;; esac; done

if [ $mode = explicit ]; then
  dir=$1; shift
  [ -d "$dir" ] || die "FAIL: $dir is not a directory"
  mapfile -t dirs < <(run_dirs "$dir")
  echo "NOTE: $version; ${#dirs[@]} run directories under $dir; $# SQL files"
  [ ${#dirs[@]} -gt 0 ] || die "FAIL: no run directory under $dir"
  for d in "${dirs[@]}"; do check_run "$d" || failures=$((failures + 1)); done
  [ $failures = 0 ] || die "FAIL: $failures run directories are not complete schema version 1 runs"
  for sql in "$@"; do
    [ -f "$sql" ] || { fail "FAIL: $sql: no such SQL file"; continue; }
    assert "$sql" "${dirs[@]}"
  done
else
  all=()
  for r in "$@"; do
    [ -d "$r" ] || die "FAIL: root $r does not exist"
    mapfile -t found < <(run_dirs "$r")
    [ ${#found[@]} -gt 0 ] || echo "note: no run directory under $r"
    all+=("${found[@]}")
  done
  mapfile -t scenarios < <(find "$analysis" -mindepth 2 -maxdepth 2 -name '*.sql' -printf '%h\n' | LC_ALL=C sort -u)
  echo "NOTE: $version; ${#all[@]} run directories; ${#scenarios[@]} scenarios with SQL files"
  [ ${#all[@]} -gt 0 ] || die "FAIL: no run directory under $*"
  for d in "${all[@]}"; do check_run "$d" || failures=$((failures + 1)); done
  [ $failures = 0 ] || die "FAIL: $failures run directories are not complete schema version 1 runs"
  for s in "${scenarios[@]}"; do
    sc=$(basename "$s")
    mine=()
    for r in "$@"; do
      for d in "${all[@]}"; do
        rel=${d#"${r%/}"/}
        [ "$rel" != "$d" ] && [ "${rel%%/*}" = "$sc" ] && mine+=("$d")
      done
    done
    if [ ${#mine[@]} = 0 ]; then fail "FAIL: scenario $sc has SQL files but no run under $*"; continue; fi
    for sql in "$s"/*.sql; do assert "$sql" "${mine[@]}"; done
  done
fi

echo "csv-assert: $checked SQL files run, $failures failures; $version"
[ $failures = 0 ] || exit 1
[ $checked -gt 0 ] || die "FAIL: no SQL file was run"
exit 0
