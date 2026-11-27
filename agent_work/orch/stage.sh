#!/bin/bash
# Start one stage of a chunk's chain: fills the brief template, makes the worktree, launches.
# Usage (from the main checkout):
#   agent_work/orch/stage.sh review <short> <chunk-dir> <builder-brief> <round> [extra-text]
#   agent_work/orch/stage.sh adv    <short> <chunk-dir> <builder-brief> [extra-text]
#   agent_work/orch/stage.sh repair <short> <chunk-dir> <builder-brief> <round> [extra-text]
#   agent_work/orch/stage.sh prose  <short> <chunk-dir> <builder-brief> <packages> <chapter-file> <chapter-title> <other-prose> [extra-text]
# <short> is the chunk's short name (c02): the build branch is work/<chunk-dir> minus its
# number prefix convention is NOT assumed — the branch is read from the worktree
# /root/Software/mini-gateway-work/<short>.
#   review  detached worktree <short>-review at the build tip; output /root/runs/zgateway-p2-<short>-REVIEW-<round>.md; run <short>-review<round> (Neon)
#   adv     worktree <short>-adv on branch work/<short>-adv; run <short>-adv (Astra)
#   repair  in the build worktree; run <short>-repair (Opus)
#   NEON_SEAT=neon-oc sends the review or prose stage down the prepaid OpenCode route (use when the Antigravity pool is empty; note it).
#   prose   in the build worktree; records the pre-prose commit in /root/runs/zgateway-p2-<short>-preprose.sha; run <short>-prose (Neon)
# Briefs are written to /root/briefs/zgateway-p2-<short>-<stage>.md.
set -eu
W=/root/Software/mini-gateway-work
here=$(cd "$(dirname "$0")" && pwd -P)
T=$here/templates
kind=$1; short=$2; chunk=$3; brief=$(readlink -f "$4"); shift 4
wt=$W/$short
branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD)
tip=$(git -C "$wt" rev-parse HEAD)
base=$(git merge-base main "$tip")
[ -z "$(git -C "$wt" status --porcelain)" ] || { echo "stage: $wt is dirty" >&2; exit 2; }
case "$kind" in
  review)
    round=$1; extra=${2:-}
    git worktree add -q --detach "$W/$short-review" "$tip"
    out=/root/runs/zgateway-p2-$short-REVIEW-$round.md; rm -f "$out"
    "$T/mk.sh" review "/root/briefs/zgateway-p2-$short-review$round.md" CHUNK="$chunk" ROUND="$round" WT="$W/$short-review" TIP="$tip" BASE="$base" BRIEF="$brief" OUT="$out" EXTRA="$extra"
    "$here/launch.sh" "$short-review$round" "${NEON_SEAT:-neon}" "$W/$short-review" "/root/briefs/zgateway-p2-$short-review$round.md" ;;
  adv)
    extra=${1:-}
    git worktree add -q -b "work/$short-adv" "$W/$short-adv" "$tip"
    "$T/mk.sh" adversarial "/root/briefs/zgateway-p2-$short-adversarial.md" CHUNK="$chunk" WT="$W/$short-adv" BRANCH="work/$short-adv" TIP="$tip" BRIEF="$brief" EXTRA="$extra"
    "$here/launch.sh" "$short-adv" astra "$W/$short-adv" "/root/briefs/zgateway-p2-$short-adversarial.md" ;;
  repair)
    round=$1; extra=${2:-}
    "$T/mk.sh" repair "/root/briefs/zgateway-p2-$short-repair.md" CHUNK="$chunk" ROUND="$round" WT="$wt" BRANCH="$branch" BRIEF="$brief" EXTRA="$extra"
    "$here/launch.sh" "$short-repair" opus "$wt" "/root/briefs/zgateway-p2-$short-repair.md" ;;
  prose)
    packages=$1; chfile=$2; chtitle=$3; other=$4; extra=${5:-}
    echo "$tip" > "/root/runs/zgateway-p2-$short-preprose.sha"
    std="6. The gate rule for your pass: read 'The final stage' in docs/knowledge/foundation-gates.md and tools/lint-rules/prose-paths.txt. You may change ONLY comments in .go files (outside tools/, agent_work/ and testdata/), files under book/, Markdown under docs/ outside docs/c4/ and the generated contract documents, README.md, and agent_work/chunks/$chunk/PROSE-NOTES.md. Any other changed path fails the gate. Go files of chunks that already landed have their comments: add comments only where this chunk added or changed code."
    "$T/mk.sh" prose "/root/briefs/zgateway-p2-$short-prose.md" CHUNK="$chunk" WT="$wt" BRANCH="$branch" BRIEF="$brief" PACKAGES="$packages" CHAPTER_FILE="$chfile" CHAPTER_TITLE="$chtitle" OTHER_PROSE="$other" EXTRA="$std $extra"
    "$here/launch.sh" "$short-prose" "${NEON_SEAT:-neon}" "$wt" "/root/briefs/zgateway-p2-$short-prose.md" ;;
  *) echo "stage: unknown kind $kind" >&2; exit 2 ;;
esac
