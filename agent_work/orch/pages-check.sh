#!/bin/bash
# After a push: wait for the Pages workflow run of a commit to finish, then require HTTP 200
# for each given site path.
# Usage: agent_work/orch/pages-check.sh <commit-sha> <site-path>...
#   site paths are relative to https://nickstrad.github.io/mini-gateway/ ("" is the front page).
# Exit 0 when the run succeeded and every path returned 200; 1 otherwise. Waits up to
# PAGES_WAIT seconds (default 540) for the run and for the new content to be served.
set -u
[ $# -ge 2 ] || { echo "usage: pages-check.sh <commit-sha> <site-path>..." >&2; exit 2; }
sha=$1; shift
base=https://nickstrad.github.io/mini-gateway
deadline=$(( $(date +%s) + ${PAGES_WAIT:-540} ))
concl=
while [ "$(date +%s)" -lt "$deadline" ]; do
  line=$(gh run list -R nickstrad/mini-gateway --workflow pages.yml --commit "$sha" --json status,conclusion,databaseId --jq '.[0] | "\(.status) \(.conclusion) \(.databaseId)"' 2>/dev/null || true)
  case "$line" in
    completed\ success\ *) concl=success; echo "pages-check: run ${line##* } succeeded"; break ;;
    completed\ *) echo "pages-check: run finished as: $line"; exit 1 ;;
  esac
  sleep 10
done
[ "$concl" = success ] || { echo "pages-check: no finished Pages run for $sha before the deadline (last: ${line:-none})"; exit 1; }
rc=0
for p in "$@"; do
  code=000
  while :; do
    code=$(curl -s -o /dev/null -w '%{http_code}' "$base/$p?cb=$(date +%s)")
    [ "$code" = 200 ] && break
    [ "$(date +%s)" -ge "$deadline" ] && break
    sleep 5
  done
  echo "pages-check: $code $base/$p"
  [ "$code" = 200 ] || rc=1
done
exit $rc
