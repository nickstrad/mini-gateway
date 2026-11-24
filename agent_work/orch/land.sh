#!/bin/bash
# Land one chunk on main: refuse unless the landing evidence is on the branch, then merge
# --no-ff and push over SSH (the gh token has no workflow scope, so HTTPS pushes that touch
# .github/workflows are refused).
# Usage (from the MAIN checkout): agent_work/orch/land.sh <chunk-dir> <branch> <message-file>
#   <chunk-dir> is the chunk's folder name under agent_work/chunks/, for example c01-sim-kernel.
# Requires in the branch tip's tree:
#   agent_work/chunks/<chunk-dir>/gate/result.json  overall=pass, counts_for_landing=true, for a
#       commit on the branch after which only agent_work/, STATE.md and docs/knowledge/ changed
#   agent_work/chunks/<chunk-dir>/REVIEW-<n>.md     newest starts with "VERDICT: PASS", or
#       "VERDICT: NITS" plus a line starting "NITS CLOSED"; a newest ARBITRATION-<n>.md wins
#   agent_work/chunks/<chunk-dir>/ADVERSARIAL.md    held and broken counts
# LAND_NO_REVIEW=1 skips the review and adversarial files (chunk 0 only: it has no Opus build).
set -eu
set -o pipefail
[ $# = 3 ] || { echo "usage: land.sh <chunk-dir> <branch> <message-file>" >&2; exit 2; }
chunk=$1; branch=$2; msg=$(readlink -f "$3")
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "land: not on main" >&2; exit 2; }
[ -z "$(git status --porcelain)" ] || { echo "land: main checkout is dirty" >&2; exit 2; }
tip=$(git rev-parse "$branch")
git merge-base --is-ancestor main "$tip" || { echo "land: $branch is not rebased on main" >&2; exit 1; }
show() { git show "$tip:$1" 2>/dev/null; }
dir="agent_work/chunks/$chunk"
res=$(show "$dir/gate/result.json") || { echo "land: no gate result on the branch" >&2; exit 1; }
[ "$(jq -r '.overall + " " + (.counts_for_landing|tostring)' <<<"$res")" = "pass true" ] \
  || { echo "land: the gate result is not a full pass that counts for landing" >&2; exit 1; }
gsha=$(jq -r .sha <<<"$res")
[[ "$gsha" =~ ^[0-9a-f]{40}$ ]] || { echo "land: gate result names no commit ('$gsha')" >&2; exit 1; }
git merge-base --is-ancestor "$gsha" "$tip" || { echo "land: gated commit $gsha is not on $branch" >&2; exit 1; }
later=$(git diff --name-only "$gsha" "$tip" | grep -vE '^agent_work/|^STATE\.md$|^docs/knowledge/' || true)
[ -z "$later" ] || { echo "land: files changed after the gated commit $gsha:"; echo "$later"; exit 1; } >&2
if [ "${LAND_NO_REVIEW:-}" != 1 ]; then
  notes=$(git ls-tree --name-only "$tip" "$dir/")
  rev=$({ grep -E 'ARBITRATION-[0-9]+\.md$' <<<"$notes" || true; } | sort -V | tail -1)
  [ -n "$rev" ] || rev=$({ grep -E 'REVIEW-[0-9]+\.md$' <<<"$notes" || true; } | sort -V | tail -1)
  [ -n "$rev" ] || { echo "land: no review file" >&2; exit 1; }
  verdict=$(show "$rev" | head -1 | tr -d '\r')
  case "$verdict" in
    "VERDICT: PASS") ;;
    "VERDICT: NITS") show "$rev" | grep -q '^NITS CLOSED' || { echo "land: $rev is NITS without a NITS CLOSED line" >&2; exit 1; } ;;
    *) echo "land: $rev says '$verdict'" >&2; exit 1 ;;
  esac
  show "$dir/ADVERSARIAL.md" >/dev/null || { echo "land: no ADVERSARIAL.md" >&2; exit 1; }
fi
git merge --no-ff -F "$msg" "$branch"
git push git@github.com:nickstrad/mini-gateway.git main
git fetch -q origin main || true
echo "land: $chunk merged at $(git rev-parse --short HEAD)"
