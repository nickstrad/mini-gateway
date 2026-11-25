#!/bin/bash
# Finish a chunk after its prose pass: rebase on main if needed, run the final-stage gate,
# commit the result, land, and check the pages.
# Usage (from the MAIN checkout): agent_work/orch/finish.sh <short> <chunk-dir> <message-file> <site-path>...
# The pre-prose commit is the parent of the oldest commit on the branch whose subject starts
# with "prose:" or "book:" (the prose seat's commits come last on a branch).
# Exit 0 only when the chunk is merged, pushed, every site path returns 200 and the CI run
# of the merge commit on GitHub is green.
set -eu
set -o pipefail
short=$1; chunk=$2; msg=$(readlink -f "$3"); shift 3
wt=/root/Software/mini-gateway-work/$short
main=$(pwd -P)
export PATH=/usr/local/go/bin:/root/.local/bin:$PATH
branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD)
[ -z "$(git -C "$wt" status --porcelain)" ] || { echo "finish: $wt is dirty" >&2; exit 2; }
git merge-base --is-ancestor main "$branch" || git -C "$wt" rebase main >/dev/null
first=$(git -C "$wt" log --reverse --format='%H %s' main..HEAD | grep -E '^[0-9a-f]+ (prose|book):' | head -1 | cut -d' ' -f1)
[ -n "$first" ] || { echo "finish: no prose commit on $branch" >&2; exit 2; }
pre=$(git -C "$wt" rev-parse "$first^")
echo "finish: pre-prose commit $pre"
( cd "$wt" && GATE_STAGE=final GATE_PREPROSE=$pre scripts/gate.sh "$chunk" ) | grep -vE ' pass +[0-9]+s' || true
res=$wt/agent_work/chunks/$chunk/gate/result.json
[ "$(jq -r '.overall + " " + (.counts_for_landing|tostring)' "$res")" = "pass true" ] || { echo "finish: gate not green" >&2; exit 1; }
git -C "$wt" add -f "agent_work/chunks/$chunk/gate/result.json"
git -C "$wt" commit -qm "$short: record the final-stage gate result

Co-Authored-By: Claude <noreply@anthropic.com>"
cd "$main"
agent_work/orch/land.sh "$chunk" "$branch" "$msg" | tail -1
agent_work/orch/pages-check.sh "$(git rev-parse HEAD)" "$@"
sha=$(git rev-parse HEAD); ci=
for _ in $(seq 60); do
  ci=$(gh run list -R nickstrad/mini-gateway --workflow ci.yml --commit "$sha" --json status,conclusion --jq '.[0] | "\(.status) \(.conclusion)"' 2>/dev/null || true)
  case "$ci" in completed\ *) break ;; esac
  sleep 10
done
echo "finish: CI on GitHub: ${ci:-no run found}"
[ "$ci" = "completed success" ] || { echo "finish: CI is not green for $sha — the chunk is merged; fix CI before the next chunk" >&2; exit 1; }
git worktree remove "$wt" && git branch -q -d "$branch"
echo "finish: $chunk landed at $(git rev-parse --short HEAD)"
