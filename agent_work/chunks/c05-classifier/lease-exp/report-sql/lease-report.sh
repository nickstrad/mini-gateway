#!/bin/bash
# Lease-lab report (plan section 9): run every report SQL file over a campaign directory.
#
# Usage: lease-report.sh <campaign-dir> <out-dir> [name=value ...]
#
#   <campaign-dir>  root of the run directories (any depth; NOTES-E2.md section 3 contract)
#   <out-dir>       created if needed; gets report.duckdb, one <table>.csv and <table>.md per
#                   SQL file, and index.md
#   name=value      a report parameter, as `SET VARIABLE name = value;` (value is SQL:
#                   quote text, e.g. verdict_load=80 aa_arm="'base-aa'"); see
#                   params.sql in this folder and the top of each table file
#
# Environment: LEASE_REPORT_SQL_DIR (default this script's own folder; the self-test pointed
# it at a planted-bug copy), LEASE_REPORT_CALIBRATION (default ../calibration.json from here).
#
# Table 00 runs first. Exit status: 0 every table written and table 00 is empty; 1 every table
# written but table 00 has rows (the campaign is not readable as is); 2 usage or DuckDB error.
set -u
set -o pipefail
here=$(cd "$(dirname "$0")" && pwd -P)
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"
die() { echo "lease-report: $*" >&2; exit 2; }
[ $# -ge 2 ] || { sed -n '2,19p' "$0" >&2; exit 2; }
command -v duckdb >/dev/null 2>&1 || die "duckdb is not installed"
[[ "$(duckdb --version 2>&1 | head -n1)" =~ ^v1\.5\. ]] || die "duckdb is not the pinned family 1.5.x"
[ -d "$1" ] || die "campaign directory $1 does not exist"
camp=$(cd "$1" && pwd -P)
mkdir -p "$2" || die "cannot create $2"
out=$(cd "$2" && pwd -P)
shift 2
sqldir=${LEASE_REPORT_SQL_DIR:-$here}
calib=${LEASE_REPORT_CALIBRATION:-$(dirname "$here")/calibration.json}
[ -f "$sqldir/params.sql" ] && [ -f "$sqldir/views.sql" ] || die "$sqldir has no params.sql / views.sql"
[ -f "$calib" ] || die "calibration file $calib does not exist"
case "$camp/" in "$out"/*) die "the output directory may not contain the campaign" ;; esac
case "$out/" in "$camp"/*) die "the output directory may not be inside the campaign" ;; esac

q() { printf "'%s'" "${1//\'/\'\'}"; }
sets="SET VARIABLE root = $(q "$camp");
SET VARIABLE sentinel = $(q "$sqldir/empty");
SET VARIABLE calibration = $(q "$calib");"
for a in "$@"; do
  [[ "$a" =~ ^[a-z_][a-z0-9_]*=.+$ ]] || die "bad parameter '$a' (want name=value)"
  sets="$sets
SET VARIABLE ${a%%=*} = ${a#*=};"
done

db="$out/report.duckdb"
rm -f "$db" "$db.wal" "$out"/*.csv "$out"/*.md
build=$( { echo "$sets"; cat "$sqldir/params.sql" "$sqldir/views.sql"; } | duckdb -batch -bail "$db" 2>&1) \
  || die "building the report database failed: $build"

tables=()
for f in "$sqldir"/[0-9]*.sql; do tables+=("$f"); done
[ ${#tables[@]} -gt 0 ] || die "no table SQL file in $sqldir"
{
  echo "# Lease-lab report tables"
  echo
  echo "Campaign: \`$camp\`. SQL: \`$sqldir\`. Calibration: \`$calib\`."
  [ $# -gt 0 ] && echo "Parameters: \`$*\`."
  echo
  echo "| Table | File | Rows | What it prints |"
  echo "| --- | --- | ---: | --- |"
} > "$out/index.md"
unreadable=0
for f in "${tables[@]}"; do
  name=$(basename "$f" .sql)
  res=$( { echo "$sets"; cat "$sqldir/params.sql" "$f"; } | duckdb -batch -bail -readonly -csv "$db" 2>&1) \
    || die "$name failed: $res"
  printf '%s\n' "$res" > "$out/$name.csv"
  rows=$(( $(printf '%s\n' "$res" | grep -c '') - 1 ))
  if [ "$rows" -gt 0 ]; then
    duckdb -batch -markdown :memory: -c "SELECT * FROM read_csv($(q "$out/$name.csv"), header = true, all_varchar = true)" > "$out/$name.md" \
      || die "$name: markdown conversion failed"
  else
    printf '%s\n\n(no rows)\n' "$(head -n1 "$out/$name.csv")" > "$out/$name.md"
  fi
  title=$(awk '/^-- Table/{p=1} p&&/^-- (Parameters|Reads):/{exit} p{sub(/^-- */,""); printf "%s ", $0}' "$f" | sed -E 's/ +$//; s/\|/\\|/g')
  echo "| $name | [$name.csv]($name.csv), [$name.md]($name.md) | $rows | $title |" >> "$out/index.md"
  if [[ "$name" == 00* ]] && [ "$rows" -gt 0 ]; then unreadable=$rows; fi
done
if [ "$unreadable" -gt 0 ]; then
  printf '\n**NOT READABLE:** table 00 has %s rows; read it before any other table.\n' "$unreadable" >> "$out/index.md"
  echo "lease-report: ${#tables[@]} tables in $out; table 00 has $unreadable rows: the campaign is not readable as is" >&2
  exit 1
fi
echo "lease-report: ${#tables[@]} tables in $out; table 00 empty"
