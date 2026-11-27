#!/bin/bash
# Land one chunk on main: refuse unless the landing evidence is on the branch, then merge
# --no-ff and push over SSH (the gh token has no workflow scope, so HTTPS pushes that touch
# .github/workflows are refused).
# Usage (from the MAIN checkout): agent_work/orch/land.sh <chunk-dir> <branch> <message-file>
#   <chunk-dir> is the chunk's folder name under agent_work/chunks/, for example c01-sim-kernel.
# Requires in the branch tip's tree:
#   agent_work/chunks/<chunk-dir>/gate/result.json  with ALL of: chunk = <chunk-dir>,
#       overall = pass, counts_for_landing = true, partial = false, dirty_tree = false,
#       stage = final (a build-stage result never lands: every chunk lands after its prose
#       pass; stage = foundation is accepted for the chunk c00-foundation only), and sha = a
#       commit on the branch (the gated commit) after which ONLY these paths changed:
#           agent_work/chunks/<chunk-dir>/gate/result.json
#           agent_work/chunks/<chunk-dir>/*.md
#           agent_work/INDEX.md, STATE.md, docs/knowledge/*.md
#       Nothing else: not gate/extra.sh, not gate/canaries.json, not agent_work/adversarial/.
#   agent_work/chunks/<chunk-dir>/REVIEW-<n>.md     newest starts with "VERDICT: PASS", or
#       "VERDICT: NITS" plus a line starting "NITS CLOSED"; a newest ARBITRATION-<n>.md wins
#   agent_work/chunks/<chunk-dir>/ADVERSARIAL.md    held and broken counts
# LAND_NO_REVIEW=1 skips the review and adversarial files. It is honoured for the chunk
# c00-foundation only (it has no Opus build); for any other chunk it is refused.
# Exit status: 0 merged and pushed, 1 the evidence does not allow landing, 2 usage or setup.
set -eu
set -o pipefail
[ $# = 3 ] || { echo "usage: land.sh <chunk-dir> <branch> <message-file>" >&2; exit 2; }
chunk=$1; branch=$2; msg=$(readlink -f "$3")
[[ "$chunk" =~ ^[a-z0-9][a-z0-9-]*$ ]] || { echo "land: '$chunk' is not a chunk folder name" >&2; exit 2; }
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "land: not on main" >&2; exit 2; }
[ -z "$(git status --porcelain)" ] || { echo "land: main checkout is dirty" >&2; exit 2; }
if [ "${LAND_NO_REVIEW:-}" = 1 ] && [ "$chunk" != c00-foundation ]; then
  echo "land: LAND_NO_REVIEW=1 is honoured for the chunk c00-foundation only, not for $chunk" >&2; exit 2
fi
tip=$(git rev-parse --verify -q "$branch^{commit}") || { echo "land: no such branch: $branch" >&2; exit 2; }
git merge-base --is-ancestor main "$tip" || { echo "land: $branch is not rebased on main" >&2; exit 1; }
show() { git show "$tip:$1" 2>/dev/null; }
dir="agent_work/chunks/$chunk"
res=$(show "$dir/gate/result.json") || { echo "land: no gate result on the branch" >&2; exit 1; }
field() { jq -r "$1 | tostring" <<<"$res" 2>/dev/null || echo unreadable; }
full="$(field .overall) $(field .counts_for_landing) $(field .partial) $(field .dirty_tree)"
[ "$full" = "pass true false false" ] \
  || { echo "land: the gate result is not a full pass that counts for landing (overall, counts_for_landing, partial, dirty_tree = $full; want pass true false false)" >&2; exit 1; }
rchunk=$(field .chunk)
[ "$rchunk" = "$chunk" ] || { echo "land: the gate result is for the chunk '$rchunk', not for $chunk" >&2; exit 1; }
gsha=$(field .sha)
[[ "$gsha" =~ ^[0-9a-f]{40}$ ]] || { echo "land: gate result names no commit ('$gsha')" >&2; exit 1; }
git merge-base --is-ancestor "$gsha" "$tip" || { echo "land: gated commit $gsha is not on $branch" >&2; exit 1; }
may_change="^agent_work/chunks/$chunk/gate/result\\.json\$|^agent_work/chunks/$chunk/[^/]+\\.md\$|^agent_work/INDEX\\.md\$|^STATE\\.md\$|^docs/knowledge/[^/]+\\.md\$"
later=$(git -c core.quotePath=false diff --name-only --no-renames "$gsha" "$tip" | grep -vE "$may_change" || true)
[ -z "$later" ] || { echo "land: files changed after the gated commit $gsha:"; echo "$later"; exit 1; } >&2
if [ "${LAND_NO_REVIEW:-}" != 1 ]; then
  notes=$(git ls-tree --name-only "$tip" "$dir/")
  rev=$({ grep -E 'ARBITRATION-[0-9]+\.md$' <<<"$notes" || true; } | sort -V | tail -1)
  [ -n "$rev" ] || rev=$({ grep -E 'REVIEW-[0-9]+\.md$' <<<"$notes" || true; } | sort -V | tail -1)
  [ -n "$rev" ] || { echo "land: no review file" >&2; exit 1; }
  verdict=$(show "$rev" | sed -n 1p | tr -d "\r")
  case "$verdict" in
    "VERDICT: PASS") ;;
    "VERDICT: NITS") show "$rev" | grep '^NITS CLOSED' >/dev/null || { echo "land: $rev is NITS without a NITS CLOSED line" >&2; exit 1; } ;;
    *) echo "land: $rev says '$verdict'" >&2; exit 1 ;;
  esac
  show "$dir/ADVERSARIAL.md" >/dev/null || { echo "land: no ADVERSARIAL.md" >&2; exit 1; }
fi
# Checked last, so that every other fault is named first.
rstage=$(field .stage)
case "$rstage" in
  final) ;;
  foundation) [ "$chunk" = c00-foundation ] || { echo "land: the gate result has stage foundation, which only the chunk c00-foundation may land with" >&2; exit 1; } ;;
  *) echo "land: the gate result has stage '$rstage'; a chunk lands with a final-stage result (GATE_STAGE=final, after its prose pass)" >&2; exit 1 ;;
esac
git merge --no-ff -F "$msg" "$branch"
git push git@github.com:nickstrad/mini-gateway.git main
git fetch -q origin main || true
echo "land: $chunk merged at $(git rev-parse --short HEAD)"
