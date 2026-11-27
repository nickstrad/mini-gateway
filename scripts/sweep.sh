#!/bin/bash
# Seed sweep: every simulator scenario over many seeds (tier S).
#
# Usage: scripts/sweep.sh [-seeds N] [-run REGEXP]
#   -seeds N            seeds per scenario (default: $SWEEP_SEEDS, else 200)
#   -run REGEXP         go test -run pattern selecting the scenario tests
#                       (default ^TestScenario: every test whose name starts with TestScenario)
#   SWEEP_SEEDS=200     seeds per scenario (the gate's budget; on demand use thousands)
#   SWEEP_SEED_BASE=1   first seed
#   RUN_DIR=<dir>       where the run writes its log (sweep.log) and the scenarios their CSV
#                       files; the gate passes one and hands it to csv-assert.sh afterwards
#                       (default: a new folder under /tmp)
#   VERBOSE=1           also print the raw go test output
#   SWEEP_GO=<cmd>      the command run as `go` for the go test step (default go; the script
#                       tests set a stub)
# Concise by default: one line per scenario (package, test path without its seed=N element,
# seeds run, pass or FAIL, its files or the log), then the summary line. A failed scenario is
# followed by every failing seed's --- FAIL line, its error lines and its REPLAY line; other
# failures are listed after the summary, so concise mode never hides a failure.
# The scenario tests read the seeds through internal/sim/simtest (DST_SEEDS and
# DST_SEED_BASE): each scenario runs its pinned seeds plus N consecutive seeds, twice per
# seed (the replay check). Every failure prints a REPLAY: line that reproduces it; this
# script prints the first one again at the end.
#
# Exit status: 0 green, 1 a check failed (or no scenario test ran), 2 usage or setup error.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"

seeds=${SWEEP_SEEDS:-200}
base=${SWEEP_SEED_BASE:-1}
pattern='^TestScenario'
while [ $# -gt 0 ]; do
  case "$1" in
    -seeds) [ $# -ge 2 ] || { echo "sweep.sh: -seeds needs a value" >&2; exit 2; }; seeds=$2; shift 2 ;;
    -run) [ $# -ge 2 ] || { echo "sweep.sh: -run needs a value" >&2; exit 2; }; pattern=$2; shift 2 ;;
    -h|-help|--help) awk 'NR > 1 && /^#/ { print; next } NR > 1 { exit }' "$0"; exit 0 ;;
    *) echo "sweep.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done
[[ "$seeds" =~ ^[1-9][0-9]*$ ]] || { echo "sweep.sh: seeds must be a positive whole number, got '$seeds'" >&2; exit 2; }
[[ "$base" =~ ^-?[0-9]+$ ]] || { echo "sweep.sh: SWEEP_SEED_BASE must be a whole number" >&2; exit 2; }

dir=${RUN_DIR:-}
if [ -z "$dir" ]; then
  dir=$(mktemp -d /tmp/sweep.XXXXXX) || exit 2
fi
mkdir -p "$dir" || exit 2
log="$dir/sweep.log"

gocmd=${SWEEP_GO:-go}
module=$(awk '$1 == "module" { print $2; exit }' go.mod 2>/dev/null)

echo "NOTE: $seeds seeds per scenario from $base, tests matching $pattern"
unset DST_SEED
rc=0
if [ "${VERBOSE:-0}" = 1 ]; then
  DST_SEEDS=$seeds DST_SEED_BASE=$base RUN_DIR="$dir" "$gocmd" test -count=1 -timeout 60m -v -run "$pattern" ./... 2>&1 | tee "$log" || rc=$?
else
  DST_SEEDS=$seeds DST_SEED_BASE=$base RUN_DIR="$dir" "$gocmd" test -count=1 -timeout 60m -v -run "$pattern" ./... >"$log" 2>&1 || rc=$?
fi

# digest: from the go test -v log, one S line per scenario (a test path without its seed=N
# element, keyed by package), D lines with the failing seeds' details, O lines with the other
# failures (a failing test that is no seed and has no failing seed below it).
digest() {
  awk -v module="$module" '
    function seedpos(name,   n, p, i) { n = split(name, p, "/"); for (i = n; i >= 2; i--) if (p[i] ~ /^seed=-?[0-9]+$/) return i; return 0 }
    function interesting(l) { return l ~ /\.go:[0-9]+: / || l ~ /^[ \t]*(error|seed): / || l ~ /REPLAY: / }
    /^=== (RUN|PAUSE|CONT|NAME) / { cur = $3; next }
    /^[ \t]*--- (PASS|FAIL|SKIP): / {
      t = $3; st[t] = $2; cur = t; if (!(t in seen)) { seen[t] = 1; order[++nt] = t; pend[++np] = t }
      if ($2 == "FAIL:") { fl[t] = $0; sub(/^[ \t]+/, "", fl[t]) }
      next
    }
    /^(ok|FAIL|\?)[ \t]+[^ \t]+\t/ {
      pk = $2; if (module != "" && index(pk, module) == 1) pk = "." substr(pk, length(module) + 1)
      for (i = 1; i <= np; i++) pkg[pend[i]] = pk; np = 0
      if ($1 == "FAIL") other[++no] = $0
      next
    }
    /^(PASS|FAIL)$/ { next }
    { if (cur != "") out[cur] = out[cur] $0 "\n" }
    END {
      for (i = 1; i <= np; i++) pkg[pend[i]] = "?"
      for (i = 1; i <= nt; i++) {
        t = order[i]; s = seedpos(t); if (!s) continue
        n = split(t, p, "/"); if (s != n) continue
        key = ""; for (j = 1; j < s; j++) key = key (j > 1 ? "/" : "") p[j]
        k = pkg[t] "\t" key
        if (!(k in runs)) korder[++nk] = k
        runs[k]++; if (st[t] == "FAIL:") { fails[k]++; failed_seed[t] = 1 }
      }
      for (i = 1; i <= nk; i++) { k = korder[i]; print "S\t" k "\t" runs[k] "\t" fails[k] + 0 }
      for (i = 1; i <= nt; i++) {
        t = order[i]; if (!(t in failed_seed)) continue
        n = split(t, p, "/"); key = ""; for (j = 1; j < n; j++) key = key (j > 1 ? "/" : "") p[j]
        k = pkg[t] "\t" key
        shown = 0
        for (m = 1; m <= nt; m++) {
          u = order[m]; if (u != t && index(u, t "/") != 1) continue
          if (st[u] == "FAIL:") print "D\t" k "\t" fl[u]
          c = split(out[u], ls, "\n")
          for (x = 1; x <= c; x++) if (interesting(ls[x])) { l = ls[x]; sub(/^[ \t]+/, "", l); print "D\t" k "\t  " l; shown++ }
        }
        if (!shown) { c = split(out[t], ls, "\n"); for (x = 1; x <= c; x++) if (ls[x] != "") print "D\t" k "\t  " ls[x] }
      }
      for (i = 1; i <= nt; i++) {
        t = order[i]; if (st[t] != "FAIL:" || seedpos(t)) continue
        below = 0; for (u in failed_seed) if (index(u, t "/") == 1) { below = 1; break }
        if (below) continue
        print "O\t" pkg[t] " " fl[t]
        c = split(out[t], ls, "\n")
        for (x = 1; x <= c; x++) if (interesting(ls[x])) { l = ls[x]; sub(/^[ \t]+/, "", l); print "O\t  " l }
      }
      for (i = 1; i <= no; i++) print "O\t" other[i]
    }' "$log"
}
digest >"$dir/sweep.digest"
while IFS=$'\t' read -r tag pk sc n nf; do
  [ "$tag" = S ] || continue
  verdict=pass; [ "$nf" = 0 ] || verdict=FAIL
  where="log $log"; [ -d "$dir/${sc##*/}" ] && where="files $dir/${sc##*/}"
  printf '%-22s %-48s seeds %-5s %-4s %s\n' "$pk" "$sc" "$n" "$verdict" "$where"
  awk -F'\t' -v pk="$pk" -v sc="$sc" '$1 == "D" && $2 == pk && $3 == sc { print "    " $4 }' "$dir/sweep.digest"
done < <(sort -t$'\t' -k2,2 -k3,3 "$dir/sweep.digest")

passed=$(grep -cE -- '--- PASS: TestScenario[^ ]*/seed=' "$log" || true)
failed=$(grep -cE -- '--- FAIL: ' "$log" || true)
echo "sweep: $passed seed runs passed, $failed failures; log $log"
if [ "$rc" != 0 ] || [ "$failed" != 0 ]; then
  if grep -q '^O' "$dir/sweep.digest"; then echo "other failures:"; sed -n 's/^O\t/  /p' "$dir/sweep.digest"; fi
  first=$(grep -m1 -E '^[[:space:]]*REPLAY: ' "$log" | sed 's/^[[:space:]]*//')
  if [ -n "$first" ]; then echo "first failure: $first"; else tail -n 30 "$log"; fi
  exit 1
fi
if [ "$passed" = 0 ]; then
  echo "sweep: no scenario test matched $pattern; nothing was swept" >&2
  exit 1
fi
exit 0
