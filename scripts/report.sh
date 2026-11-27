#!/bin/bash
# The story report over flushed CSV runs, with DuckDB (PLAN.md sections 6.5, 7.7, 7.8 and
# 19.3 items 1 and 2). The assertions of csv-assert.sh stay the gate; this is for people.
#
# Usage: scripts/report.sh <run directory or root>...        (make report RUNS=<dir...>)
#
# Reads metrics.csv, histograms.csv and run.csv of every run directory at or below each
# argument and prints, in order: 1 what the runs prove (the `-- claim:` line of every check
# that applies), 2 how the data was collected, 3 the inputs (one row per scenario), 4 coverage
# by scenario, statement class and outcome, 5 every failed assertion in full, 6 every oracle
# mismatch in full, 7 a verdict table (check, claim, observed, expected, pass) that ends in
# exactly one line:  VERDICT: PASS (N checks over R runs)  or  VERDICT: FAIL (k of N ...).
#
# A run's scenario is the `scenario` column of its run.csv; test/analysis/<scenario>/*.sql run
# over that scenario's runs, and the built-in checks test/analysis/report/sections/check-*.sql
# over the scenarios their `-- applies-to:` lines name (the load tool's conservation checks).
# A SQL file whose `-- requires:` line names metrics of more than one component compares the
# proxy's numbers with an independent record (workload, fakepg, loadgen): an ORACLE check,
# listed in section 6. One naming a single component is an ASSERTION, listed in section 5.
#
# Fails closed, never skips: no run directory; a run directory without run.csv, metrics.csv or
# histograms.csv; a header that is not schema version 1; a row test/analysis/guard.sql rejects;
# a required metric missing from a final flush; a violating row; a DuckDB error; DuckDB missing
# or not the pinned family 1.5.x. Each is a failed check. A scenario with runs and no check is
# listed as "no assertions": shown, and neither a pass nor a failure.
# Stable: the same run directories give byte-identical output. Paths are relative to the
# arguments (rootN/ when there are several), rows are sorted, no times, hosts or full versions.
#
# Exit status: 0 every check passed, 1 a check failed, 2 usage error.
# REPORT_PLANT=ignore-failures is a test-only canary used by test/analysis/report: the verdict
# then ignores failed assertion and oracle files. Never set it by hand.
set -u
set -o pipefail
export LC_ALL=C
repo=$(cd "$(dirname "$0")/.." && pwd -P)
analysis="$repo/test/analysis"
sections="$analysis/report/sections"
views="$analysis/views.sql"
guard_sql="$analysis/guard.sql"
pinned='^v1\.5\.'
command -v duckdb >/dev/null 2>&1 || PATH="$PATH:$HOME/.local/bin"
plant=${REPORT_PLANT:-}

header() { awk 'NR > 1 && /^#/ { print; next } NR > 1 { exit }' "$0"; }
usage() { header >&2; exit 2; }
[ $# -ge 1 ] || usage
for a in "$@"; do
  case "$a" in -h|-help|--help) header; exit 0 ;; -*) echo "report.sh: unknown option $a" >&2; usage ;; esac
done

tmp=$(mktemp -d /tmp/report.XXXXXX) || exit 2
trap 'rm -rf "$tmp"' EXIT
out="$tmp/out"
: >"$out"
: >"$tmp/assertion"
: >"$tmp/oracle"
p() { printf '%s\n' "$@" >>"$out"; }

# ---- verdict rows
v_check=() v_claim=() v_obs=() v_exp=() v_pass=()
check() { v_check+=("$1"); v_claim+=("$2"); v_obs+=("$3"); v_exp+=("$4"); v_pass+=("$5"); }
pf() { if [ "$1" = 1 ]; then echo PASS; else echo FAIL; fi; }
pfc() { if "$@"; then echo PASS; else echo FAIL; fi; }

# table <title-less TSV with a header row>: a padded pipe table.
table() {
  awk -F'\t' '
    { n = NR; if (NF > nf) nf = NF; for (i = 1; i <= NF; i++) { c[NR, i] = $i; if (length($i) > w[i]) w[i] = length($i) } }
    END {
      for (r = 1; r <= n; r++) {
        line = "|"; for (i = 1; i <= nf; i++) line = line sprintf(" %-" w[i] "s |", c[r, i]); print line
        if (r == 1) { line = "|"; for (i = 1; i <= nf; i++) { s = ""; for (j = 0; j < w[i] + 2; j++) s = s "-"; line = line s "|" } print line }
      }
    }'
}

# ---- duckdb
duck_ok=0
if ! command -v duckdb >/dev/null 2>&1; then
  duck_obs="missing (not on PATH or in ~/.local/bin)"
else
  version=$(duckdb --version 2>&1 | head -n1)
  if [[ "$version" =~ $pinned ]]; then duck_ok=1; duck_obs="1.5.x"; else duck_obs="version '${version%% *}', not 1.5.x"; fi
fi
db="$tmp/runs.duckdb"
ddb() { duckdb -no-init -batch -bail -list -separator $'\t' -noheader -nullvalue '' "$db" 2>&1; }

# narrow <scenario>: the views over that scenario's runs; '' selects every run.
narrow() {
  if [ -n "$1" ]; then printf "SET VARIABLE scenario = '%s';\n" "${1//\'/\'\'}"; else echo "SET VARIABLE scenario = NULL;"; fi
  cat "$sections/select.sql"
}

# preamble <dir>...: the views over the given run directories (read once, by the load).
preamble() {
  local m='' h='' r='' d e
  for d in "$@"; do
    e=${d//\'/\'\'}
    m="$m${m:+, }'$e/metrics.csv'"; h="$h${h:+, }'$e/histograms.csv'"; r="$r${r:+, }'$e/run.csv'"
  done
  printf "SET VARIABLE metrics_files = [%s];\nSET VARIABLE histograms_files = [%s];\nSET VARIABLE run_files = [%s];\n" "$m" "$h" "$r"
  cat "$views" "$sections/preamble.sql"
}

# ---- find the run directories
metrics_header='schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value'
hist_header='schema_version,run_id,flush_seq,t_ns,instance,component,metric,tenant,outcome,reason,mode,unit,bucket_lo,bucket_hi,count'
run_header='schema_version,run_id,scenario,tier,seed,arm,instances,build,config_fingerprint,flushes,duration_ns'
nroots=$#
roots_ok=0 with_runs=0
: >"$tmp/no_runs"
roots=() rlabels=()
dirs=() disp=()
i=0
for a in "$@"; do
  i=$((i + 1))
  if [ "$nroots" = 1 ]; then pre=''; else pre="root$i/"; fi
  if [ ! -d "$a" ]; then
    printf '%s\n' "argument $i is not a directory" >>"$tmp/bad_roots"
    continue
  fi
  roots_ok=$((roots_ok + 1))
  abs=$(cd "$a" && pwd -P)
  roots+=("$abs"); rlabels+=("$pre")
  before=${#dirs[@]}
  while IFS= read -r d; do
    [ -n "$d" ] || continue
    dirs+=("$d")
    if [ "$d" = "$abs" ]; then rel=.; else rel=${d#"$abs"/}; fi
    if [ -n "$pre" ]; then disp+=("$pre$rel"); else disp+=("$rel"); fi
  done < <(find "$abs" \( -name metrics.csv -o -name histograms.csv -o -name run.csv \) -type f -printf '%h\n' | sort -u)
  if [ ${#dirs[@]} = "$before" ]; then
    printf '%s\n' "argument $i (${pre:-.}): no metrics.csv, histograms.csv or run.csv at or below it" >>"$tmp/no_runs"
  else
    with_runs=$((with_runs + 1))
  fi
done
R=${#dirs[@]}

# ---- structural checks, per run directory
declare -A hdr=()
files=()
for d in "${dirs[@]}"; do
  for f in metrics.csv histograms.csv run.csv; do [ -f "$d/$f" ] && files+=("$d/$f"); done
done
if [ ${#files[@]} -gt 0 ]; then
  # shellcheck disable=SC2016
  while IFS=$'\t' read -r f line; do hdr[$f]=$line; done < <(printf '%s\0' "${files[@]}" | xargs -0 awk 'FNR == 1 { print FILENAME "\t" $0; nextfile }')
fi
complete=() cdisp=()
n_run=0 n_files=0 n_hdr=0 n_hdr_total=0
: >"$tmp/no_run"; : >"$tmp/no_files"; : >"$tmp/bad_hdr"
for k in "${!dirs[@]}"; do
  d=${dirs[$k]}; ok=1
  if [ -f "$d/run.csv" ]; then n_run=$((n_run + 1)); else echo "${disp[$k]}: no run.csv (the run did not finish)" >>"$tmp/no_run"; ok=0; fi
  miss=''
  for f in metrics.csv histograms.csv; do [ -f "$d/$f" ] || miss="$miss${miss:+, }$f"; done
  if [ -z "$miss" ]; then n_files=$((n_files + 1)); else echo "${disp[$k]}: missing $miss" >>"$tmp/no_files"; ok=0; fi
  for f in metrics.csv histograms.csv run.csv; do
    [ -f "$d/$f" ] || continue
    n_hdr_total=$((n_hdr_total + 1))
    case $f in metrics.csv) want=$metrics_header ;; histograms.csv) want=$hist_header ;; *) want=$run_header ;; esac
    if [ "${hdr[$d/$f]:-}" = "$want" ]; then n_hdr=$((n_hdr + 1)); else echo "${disp[$k]}/$f: the header is not schema version 1" >>"$tmp/bad_hdr"; ok=0; fi
  done
  if [ $ok = 1 ]; then complete+=("$d"); cdisp+=("${disp[$k]}"); fi
done
C=${#complete[@]}

# structure_detail <check> <claim> <file>: the offending items of a failed structural check, in full.
structure_detail() {
  { echo "- $1"; echo "  claim: $2"; echo "  $(grep -c '' "$3") items:"; sort "$3" | sed 's/^/    /'; echo; } >>"$tmp/assertion"
}

check "structure: DuckDB" "DuckDB is installed and is the pinned family 1.5.x (it reads the files)" "$duck_obs" "1.5.x" "$(pf $duck_ok)"
c="every argument is an existing directory"
check "structure: arguments" "$c" "$roots_ok of $nroots" "$nroots of $nroots" "$(pfc [ "$roots_ok" = "$nroots" ])"
[ "$roots_ok" = "$nroots" ] || structure_detail "structure: arguments" "$c" "$tmp/bad_roots"
c="at least one run directory was found at or below every directory argument"
runs_ok=0
[ "$R" -gt 0 ] && [ "$with_runs" = "$roots_ok" ] && runs_ok=1
check "structure: runs" "$c" "$R runs; $with_runs of $roots_ok arguments" "at least 1 run; $roots_ok of $roots_ok arguments" "$(pf $runs_ok)"
[ "$R" -gt 0 ] || echo "no metrics.csv, histograms.csv or run.csv at or below any argument" >"$tmp/no_runs"
[ $runs_ok = 1 ] || structure_detail "structure: runs" "$c" "$tmp/no_runs"
c="every run directory has run.csv (the run finished)"
check "structure: finished" "$c" "$n_run of $R" "$R of $R" "$(pfc [ "$n_run" = "$R" ])"
[ "$n_run" = "$R" ] || structure_detail "structure: finished" "$c" "$tmp/no_run"
c="every run directory has metrics.csv and histograms.csv"
check "structure: files" "$c" "$n_files of $R" "$R of $R" "$(pfc [ "$n_files" = "$R" ])"
[ "$n_files" = "$R" ] || structure_detail "structure: files" "$c" "$tmp/no_files"
c="every file starts with the schema version 1 header"
check "structure: schema header" "$c" "$n_hdr of $n_hdr_total files" "$n_hdr_total of $n_hdr_total files" "$(pfc [ "$n_hdr" = "$n_hdr_total" ])"
[ "$n_hdr" = "$n_hdr_total" ] || structure_detail "structure: schema header" "$c" "$tmp/bad_hdr"

usable=0
c="the complete runs pass test/analysis/guard.sql: schema version 1 in every row, no empty required field, one run.csv row and a distinct run id per directory, metric rows in every run, and every flush run.csv declares is in metrics.csv"
if [ $duck_ok != 1 ]; then
  check "structure: schema guard" "$c" "not run: DuckDB is not usable" "0 violations" FAIL
elif [ "$C" = 0 ]; then
  check "structure: schema guard" "$c" "not run: no complete run directory" "0 violations" FAIL
else
  rc=0
  g=$( { preamble "${complete[@]}"; cat "$sections/load.sql"; echo "SET VARIABLE expected_runs = $C;"; cat "$guard_sql" "$sections/flushes.sql"; } | ddb) || rc=$?
  usable=1
  if [ $rc != 0 ]; then
    usable=0
    check "structure: schema guard" "$c" "DuckDB error" "0 violations" FAIL
    printf '%s\n' "DuckDB error: $g" >"$tmp/guard"
    structure_detail "structure: schema guard" "$c" "$tmp/guard"
  elif [ -n "$g" ]; then
    printf '%s\n' "$g" >"$tmp/guard"
    check "structure: schema guard" "$c" "$(grep -c '' "$tmp/guard") violations" "0 violations" FAIL
    structure_detail "structure: schema guard" "$c" "$tmp/guard"
  else
    check "structure: schema guard" "$c" "0 violations over $C runs" "0 violations" PASS
  fi
fi

# ---- scenarios and their checks
declare -A sc_comps=() sc_n=()
scen=()
noassert=()
if [ $usable = 1 ]; then
  rc=0
  { narrow ''; cat "$sections/scenarios.sql"; } | ddb >"$tmp/scenarios" || rc=$?
  if [ $rc != 0 ]; then
    { echo "DuckDB error:"; cat "$tmp/scenarios"; } >"$tmp/scerr"
    check "structure: scenarios" "every complete run names its scenario in run.csv" "DuckDB error" "a scenario per run" FAIL
    structure_detail "structure: scenarios" "every complete run names its scenario in run.csv" "$tmp/scerr"
    usable=0
  else
    while IFS=$'\t' read -r sc file comps; do
      [ -n "$file" ] || continue
      sc_n[$sc]=$(( ${sc_n[$sc]:-0} + 1 ))
      sc_comps[$sc]+=",$comps,"
    done <"$tmp/scenarios"
    mapfile -t scen < <(for sc in "${!sc_n[@]}"; do printf '%s\n' "$sc"; done | sort)
  fi
fi

# H[<file>|<key>]: the text after `-- <key>:` on the first such line of every SQL file that
# may run (requires, claim, applies-to, applies-to-scenarios), read once.
declare -A H=()
sqlfiles=()
for f in "$analysis"/*/*.sql "$sections"/check-*.sql; do [ -f "$f" ] && sqlfiles+=("$f"); done
if [ ${#sqlfiles[@]} -gt 0 ]; then
  while IFS=$'\t' read -r f k v; do H[$f|$k]=$v; done < <(awk '
    /^-- (requires|claim|applies-to|applies-to-scenarios):/ {
      k = $2; sub(/:$/, "", k)
      if (!((FILENAME, k) in seen)) { seen[FILENAME, k] = 1; v = $0; sub(/^-- [a-z-]+:[ \t]*/, "", v); print FILENAME "\t" k "\t" v }
    }' "${sqlfiles[@]}")
fi

# item_sql <sql file>: the requirement checks of one SQL file, a marker, then the file.
item_sql() {
  local nm
  for nm in ${H[$1|requires]:-}; do
    printf "SELECT '@REQ', '%s', count(*) FROM runs r WHERE NOT EXISTS (SELECT 1 FROM final f WHERE f.run_id = r.run_id AND f.component = '%s' AND f.metric = '%s') AND NOT EXISTS (SELECT 1 FROM final_hist h WHERE h.run_id = r.run_id AND h.component = '%s' AND h.metric = '%s') HAVING count(*) > 0;\n" \
      "$nm" "${nm%%.*}" "${nm#*.}" "${nm%%.*}" "${nm#*.}"
  done
  echo "SELECT '@SQL';"
  cat "$1"
  echo
}

# judge <idx>: the verdict row, and the details of a failure, of check item <idx> from its
# DuckDB output $tmp/res.<idx> and exit status $tmp/rc.<idx>.
judge() {
  local k=$1 sc=${it_sc[$1]} name=${it_name[$1]} sql=${it_sql[$1]} n=${it_n[$1]} kind=${it_kind[$1]}
  local claim=${it_claim[$1]} relsql=${it_sql[$1]#"$repo"/} out="$tmp/res.$1" rc dest miss nrows pass nm
  rc=$(cat "$tmp/rc.$k")
  dest="$tmp/$kind"
  if [ "$rc" != 0 ]; then
    check "$name" "$claim" "DuckDB error" "0 violating rows" FAIL
    { echo "- $name ($relsql) over $n runs"; echo "  claim: $claim"; echo "  DuckDB error:"; grep -v '^@' "$out" | sed 's/^/    /'; echo; } >>"$dest"
    return
  fi
  miss=$(sed -n '/^@SQL$/q;/^@REQ\t/p' "$out" | cut -f2- | sort)
  if [ -n "$miss" ]; then
    printf '%s\n' "$miss" | awk -F'\t' -v n="$n" '{ print $1 ": no rows in the final flush of " $2 " of " n " runs" }' >>"$tmp/req.$sc"
    check "$name" "$claim" "not run: a required metric is missing (see $sc: required metrics)" "0 violating rows" FAIL
    { echo "- $name ($relsql) over $n runs"; echo "  claim: $claim"; echo "  not run: required metrics missing from the final flush:"
      printf '%s\n' "$miss" | awk -F'\t' -v n="$n" '{ print "    " $1 ": no rows in " $2 " of " n " runs" }'; echo; } >>"$dest"
    return
  fi
  for nm in ${H[$sql|requires]:-}; do echo "$nm" >>"$tmp/reqok.$sc"; done
  nrows=$(awk 'f { n++ } /^@SQL$/ { f = 1 } END { print n + 0 }' "$out")
  if [ "$nrows" = 0 ]; then
    check "$name" "$claim" "0 violating rows over $n runs" "0 violating rows" PASS
    return
  fi
  pass=FAIL
  [ "$plant" = ignore-failures ] && pass=PASS
  check "$name" "$claim" "$nrows violating rows over $n runs" "0 violating rows" "$pass"
  { echo "- $name ($relsql) over $n runs"; echo "  claim: $claim"; echo "  $nrows violating rows:"
    awk 'f { if ($0 == "") $0 = "(an empty row)"; gsub("\t", " | "); print } /^@SQL$/ { f = 1 }' "$out" | sort | sed 's/^/    /'; echo; } >>"$dest"
}

# Plan: every (scenario, SQL file) and (scenario, built-in check) item, in report order.
builtins=()
for f in "$sections"/check-*.sql; do [ -f "$f" ] && builtins+=("$f"); done
it_sc=() it_name=() it_sql=() it_n=() it_kind=() it_claim=() it_ok=()
for sc in "${scen[@]}"; do
  list=()
  if [[ "$sc" =~ ^[A-Za-z0-9._-]+$ ]] && [ "$sc" != . ] && [ "$sc" != .. ] && [ "$sc" != report ] && [ -d "$analysis/$sc" ]; then
    for f in "$analysis/$sc"/*.sql; do [ -f "$f" ] && list+=("$sc/$(basename "$f")"$'\t'"$f"); done
  fi
  for f in "${builtins[@]}"; do
    comp=${H[$f|applies-to]:-}; glob=${H[$f|applies-to-scenarios]:-}
    # shellcheck disable=SC2053
    if { [ -n "$comp" ] && [[ "${sc_comps[$sc]}" == *",$comp,"* ]]; } || { [ -n "$glob" ] && [[ "$sc" == $glob ]]; }; then
      b=$(basename "$f" .sql); list+=("$sc/report:${b#check-}"$'\t'"$f")
    fi
  done
  if [ ${#list[@]} = 0 ]; then noassert+=("$sc"); continue; fi
  : >"$tmp/req.$sc"; : >"$tmp/reqok.$sc"; : >"$tmp/claims.$sc"
  for item in "${list[@]}"; do
    f=${item#*$'\t'}
    req=${H[$f|requires]:-}; claim=${H[$f|claim]:-}
    [ -n "$claim" ] || claim="(no -- claim: line in ${f#"$repo"/})"
    ncomp=$(for nm in $req; do echo "${nm%%.*}"; done | sort -u | grep -c '')
    if [ "$ncomp" -gt 1 ]; then kind=oracle; else kind=assertion; fi
    ok=1
    [ -n "$req" ] || ok=no-requires
    for nm in $req; do [[ "$nm" =~ ^[a-z]+\.[a-z0-9_]+$ ]] || { ok="bad requirement '$nm'"; break; }; done
    it_sc+=("$sc"); it_name+=("${item%%$'\t'*}"); it_sql+=("$f"); it_n+=("${sc_n[$sc]}")
    it_kind+=("$kind"); it_claim+=("$claim"); it_ok+=("$ok")
    echo "$kind"$'\t'"${item%%$'\t'*}"$'\t'"$claim" >>"$tmp/claims.$sc"
  done
done

# Run: every runnable item in one DuckDB session; if that session fails, each item again in
# its own session, so a DuckDB error is reported against the file that caused it.
run=()
for k in "${!it_sc[@]}"; do [ "${it_ok[$k]}" = 1 ] && run+=("$k"); done
if [ ${#run[@]} -gt 0 ]; then
  rc=0
  { cur=''
    for k in "${run[@]}"; do
      if [ "${it_sc[$k]}" != "$cur" ]; then cur=${it_sc[$k]}; narrow "$cur"; fi
      echo "SELECT '@BEGIN', '$k';"
      item_sql "${it_sql[$k]}"
    done; } | ddb >"$tmp/batch" || rc=$?
  if [ $rc = 0 ]; then
    awk -v d="$tmp" '/^@BEGIN\t/ { if (f) close(f); f = d "/res." $2; printf "" > f; next } f { print > f }' "$tmp/batch"
    for k in "${run[@]}"; do echo 0 >"$tmp/rc.$k"; done
  else
    for k in "${run[@]}"; do
      rc=0
      { narrow "${it_sc[$k]}"; item_sql "${it_sql[$k]}"; } | ddb >"$tmp/res.$k" || rc=$?
      echo "$rc" >"$tmp/rc.$k"
    done
  fi
fi

# Judge, in plan order, then one required-metrics row per scenario.
for k in "${!it_sc[@]}"; do
  sc=${it_sc[$k]}
  case "${it_ok[$k]}" in
    1) judge "$k" ;;
    no-requires)
      check "${it_name[$k]}" "${it_claim[$k]}" "no '-- requires:' line (counters must move)" "a -- requires: line" FAIL
      { echo "- ${it_name[$k]} (${it_sql[$k]#"$repo"/}) over ${it_n[$k]} runs"; echo "  claim: ${it_claim[$k]}"
        echo "  the file has no '-- requires: component.metric' line"; echo; } >>"$tmp/assertion" ;;
    *)
      check "${it_name[$k]}" "${it_claim[$k]}" "${it_ok[$k]}" "0 violating rows" FAIL
      { echo "- ${it_name[$k]} (${it_sql[$k]#"$repo"/}) over ${it_n[$k]} runs"; echo "  claim: ${it_claim[$k]}"
        echo "  ${it_ok[$k]} in its -- requires: line"; echo; } >>"$tmp/${it_kind[$k]}" ;;
  esac
  next=$((k + 1))
  if [ "$next" -ge ${#it_sc[@]} ] || [ "${it_sc[$next]}" != "$sc" ]; then
    nreq=$(cat "$tmp/reqok.$sc" "$tmp/req.$sc" | cut -d: -f1 | sort -u | grep -c '')
    c="the final flush of every run of $sc has rows for every metric its checks require (the -- requires: rule)"
    if [ -s "$tmp/req.$sc" ]; then
      nmiss=$(cut -d: -f1 "$tmp/req.$sc" | sort -u | grep -c '')
      check "$sc: required metrics" "$c" "$nmiss of $nreq required metrics missing in some run" "all $nreq in all ${sc_n[$sc]} runs" FAIL
      sort -u "$tmp/req.$sc" >"$tmp/req.$sc.u"
      structure_detail "$sc: required metrics" "$c" "$tmp/req.$sc.u"
    else
      check "$sc: required metrics" "$c" "all $nreq in all ${sc_n[$sc]} runs" "all $nreq in all ${sc_n[$sc]} runs" PASS
    fi
  fi
done

# The sections' queries, in one session.
if [ $usable = 1 ]; then
  rc=0
  { narrow ''
    for q in collected inputs coverage; do echo "SELECT '@SECTION', '$q';"; cat "$sections/$q.sql"; done; } | ddb >"$tmp/sections" || rc=$?
  if [ $rc = 0 ]; then
    awk -v d="$tmp" '/^@SECTION\t/ { if (f) close(f); f = d "/" $2; printf "" > f; next } f { print > f }' "$tmp/sections"
  else
    for q in collected inputs coverage; do { echo "DuckDB error:"; cat "$tmp/sections"; } >"$tmp/$q.err"; done
  fi
fi

# ---- 1. What the runs prove
p "# Story report" ""
p "1. What the runs prove" ""
p "Each claim below is checked over the flushed files; the verdict table (section 7) says whether it held."
p "An assertion checks one component's own numbers; an oracle check compares the proxy's numbers with an"
p "independent record (the simulated clients, the fake Postgres or the load tool)." ""
p "The report's structural checks:"
for k in "${!v_check[@]}"; do
  case "${v_check[$k]}" in "structure: "*) p "- ${v_check[$k]#structure: }: ${v_claim[$k]}" ;; esac
done
p ""
if [ $usable != 1 ]; then
  p "Scenario claims: not available (no run could be read: see the verdict table)." ""
else
  for sc in "${scen[@]}"; do
    [ -s "$tmp/claims.$sc" ] || continue
    p "Scenario $sc:"
    p "- every metric its checks require is in the final flush of every run"
    while IFS=$'\t' read -r kind name claim; do p "- $kind ${name#"$sc"/}: $claim"; done <"$tmp/claims.$sc"
    p ""
  done
  if [ ${#noassert[@]} -gt 0 ]; then
    p "Scenarios with runs and no assertions (shown, neither a pass nor a failure): $(printf '%s\n' "${noassert[@]}" | paste -sd, - | sed 's/,/, /g')" ""
  fi
fi

# ---- 2. How the data was collected
p "2. How the data was collected" ""
if [ $usable != 1 ]; then
  p "Not available: no run could be read (see the verdict table). Run directories found: $R." ""
else
  [ -f "$tmp/collected.err" ] && p "$(cat "$tmp/collected.err")"
  tiers=$(awk -F'\t' '$1 == "tier" {
      d = ($2 == "S") ? "S (simulator: the real engine on the event loop with the fake Postgres and simulated clients)" : \
          ($2 == "R") ? "R (real: the real binary or tool against a real Postgres)" : \
          ($2 == "bench") ? "bench (benchmark runs)" : $2 " (unknown tier)"
      printf "%s%s, %s runs, %s flushes", sep, d, $3, $4; sep = "; " }' "$tmp/collected")
  schema=$(awk -F'\t' '$1 == "schema" { print $2 }' "$tmp/collected")
  comps=$(awk -F'\t' '$1 == "components" { gsub(",", ", ", $2); print $2 }' "$tmp/collected")
  IFS=$'\t' read -r _ t_runs t_fl t_flmin t_flmax t_imin t_imax t_mrows t_hrows t_sc < <(awk -F'\t' '$1 == "totals"' "$tmp/collected")
  p "- Tiers: $tiers."
  p "- Schema version: $schema (every file header and every row; any other version fails the report)."
  p "- Runs: ${t_runs:-0} complete run directories of $R found under $nroots argument(s), ${t_sc:-0} scenarios; ${t_fl:-0} flushes in all, ${t_flmin:-0} to ${t_flmax:-0} per run; ${t_imin:-0} to ${t_imax:-0} instances per run."
  p "- Rows: ${t_mrows:-0} metric rows and ${t_hrows:-0} histogram bucket rows."
  p "- Components in the final flushes: $comps."
  p "- Source: each run directory holds the CSV flush of PLAN.md section 7.7 (metrics.csv, histograms.csv, run.csv;"
  p "  rows appended at every publish interval and once more at the end, run.csv written last). The files are read"
  p "  with DuckDB 1.5.x through test/analysis/views.sql; \"final flush\" is the last flush_seq of a run. A run's"
  p "  scenario is the scenario column of its run.csv."
  [ "$nroots" -gt 1 ] && p "- Paths: rootN/ is the Nth argument."
  p ""
fi

# ---- 3. Inputs
p "3. Inputs" ""
if [ $usable != 1 ]; then
  p "Not available." ""
else
  if [ -f "$tmp/inputs.err" ]; then p "$(cat "$tmp/inputs.err")" ""; else
    { printf 'scenario\ttier\truns\tseeds\tarms\tflushes\tinstances\n'
      awk -F'\t' '{ s = ($4 == $5) ? $4 : $4 "-" $5; print $1 "\t" $2 "\t" $3 "\t" s "\t" $6 "\t" $7 "\t" $8 }' "$tmp/inputs"; } | table >>"$out"
    p "" "Legend: one row per scenario (run.csv scenario), aggregated over its runs. tier: S simulator, R real, bench."
    p "runs: complete run directories. seeds: lowest-highest seed. arms: the arms present. flushes: flushes written,"
    p "summed over the runs. instances: instances per run (the distinct values)." ""
  fi
fi

# ---- 4. Coverage
p "4. Coverage by scenario, statement class and outcome" ""
if [ $usable != 1 ]; then
  p "Not available." ""
else
  if [ -f "$tmp/coverage.err" ]; then p "$(cat "$tmp/coverage.err")" ""; else
    cov() {
      local tbl=$1 title=$2 col=$3 legend=$4
      p "4.$5 $title" ""
      if awk -F'\t' -v t="$tbl" '$1 == t { f = 1 } END { exit !f }' "$tmp/coverage"; then
        { printf 'scenario\t%s\ttotal\truns\n' "$col"
          awk -F'\t' -v t="$tbl" '$1 == t { print $2 "\t" $4 "\t" $5 "\t" $6 " of " $7 }' "$tmp/coverage"; } | table >>"$out"
      else
        p "(no rows: no run exports it)"
      fi
      p "" "Legend: $legend" ""
    }
    cov engine-requests "Proxy requests by outcome (engine)" outcome \
      "engine requests_<outcome> in the final flush, summed over the scenario's runs. received = accepted + rejected; accepted = completed + failed + shed. runs: runs where it is not 0, of the scenario's runs. Every outcome is shown." 1
    cov engine-replies "Proxy-made replies by error-contract row (engine replies)" "error-contract row" \
      "engine replies by reason, the row of the error contract (docs/error-contract.md) the proxy answered with. Rows that are 0 in every run are left out." 2
    cov classifier-class "Statements by class (classifier statements_by_class)" class \
      "Query messages the classifier classified, by statement class (the outcome label). Classes that are 0 in every run are left out." 3
    cov classifier-rejects "Statements refused by the reject list (classifier rejects)" reason \
      "Query messages refused by the reject list, by reason. Reasons that are 0 in every run are left out." 4
    cov workload-requests "Simulated clients: requests by outcome (workload requests)" outcome \
      "What the simulated clients saw, the independent record the oracle checks compare the proxy with. Outcomes that are 0 in every run are left out." 5
    cov workload-errors "Simulated clients: errors by SQLSTATE (workload errors_by_code)" SQLSTATE \
      "Error replies the simulated clients received, by SQLSTATE. Codes that are 0 in every run are left out." 6
    cov loadgen-totals "Load tool: what was intended and what was sent (loadgen)" value \
      "The load tool's own counters in the final flush. achieved on time = sent - late, to read against intended; a shortfall run is one with late + could_not_send > 0 (informative, not a check). queued and in_flight are gauges. Every value is shown." 7
    cov loadgen-completed "Load tool: sent requests by final outcome (loadgen completed)" outcome \
      "ok, db-error (the database answered with an error), refused (the proxy answered with an error), lost (no answer). Every outcome is shown." 8
    cov loadgen-errors "Load tool: error replies by SQLSTATE class (loadgen errors_by_class)" class \
      "Error replies by the two-character SQLSTATE class. Classes that are 0 in every run are left out." 9
  fi
fi

# ---- 5 and 6. Failures in full
p "5. Failed assertions, in full" ""
if [ -s "$tmp/assertion" ]; then cat "$tmp/assertion" >>"$out"; else p "None." ""; fi
p "6. Oracle mismatches, in full" ""
if [ -s "$tmp/oracle" ]; then cat "$tmp/oracle" >>"$out"; else p "None." ""; fi

# ---- 7. Verdict
p "7. Verdict" ""
for sc in "${noassert[@]}"; do check "$sc" "no assertions: the scenario has runs and no SQL file or built-in check" "${sc_n[$sc]} runs" "-" "no assertions"; done
{ printf 'check\tclaim\tobserved\texpected\tpass\n'
  for k in "${!v_check[@]}"; do printf '%s\t%s\t%s\t%s\t%s\n' "${v_check[$k]}" "${v_claim[$k]}" "${v_obs[$k]}" "${v_exp[$k]}" "${v_pass[$k]}"; done; } | table >>"$out"
p "" "Legend: one row per check. structure: the report's own checks of the files. <scenario>/<file>.sql: that scenario's"
p "SQL file from test/analysis/<scenario>/ (zero violating rows is a pass). <scenario>/report:<name>: a built-in check from"
p "test/analysis/report/sections/check-<name>.sql. \"no assertions\" rows are not counted." ""
total=0 failed=0
for k in "${!v_pass[@]}"; do
  case "${v_pass[$k]}" in PASS) total=$((total + 1)) ;; FAIL) total=$((total + 1)); failed=$((failed + 1)) ;; esac
done
extra=''
if [ ${#noassert[@]} = 1 ]; then extra="; 1 scenario has no assertions"; elif [ ${#noassert[@]} -gt 1 ]; then extra="; ${#noassert[@]} scenarios have no assertions"; fi
if [ "$R" = 1 ]; then runs_word="1 run"; else runs_word="$R runs"; fi
if [ $failed = 0 ]; then p "VERDICT: PASS ($total checks over $runs_word$extra)"; else p "VERDICT: FAIL ($failed of $total checks failed over $runs_word$extra)"; fi

# Paths are printed relative to the arguments; a DuckDB message may still name a file.
subst=$(for k in "${!roots[@]}"; do printf '%s\t%s\t%s\n' "${#roots[$k]}" "${roots[$k]}/" "${rlabels[$k]}"; done | sort -t$'\t' -k1,1nr)
awk -v repo="$repo/" -v subst="$subst" '
  BEGIN { n = split(subst, l, "\n"); for (i = 1; i <= n; i++) { split(l[i], f, "\t"); from[i] = f[2]; to[i] = f[3] } }
  { for (i = 1; i <= n; i++) if (from[i] != "") while ((j = index($0, from[i])) > 0) $0 = substr($0, 1, j - 1) to[i] substr($0, j + length(from[i]))
    while ((j = index($0, repo)) > 0) $0 = substr($0, 1, j - 1) substr($0, j + length(repo))
    print }' "$out"
[ $failed = 0 ] || exit 1
exit 0
