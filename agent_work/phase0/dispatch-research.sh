#!/bin/bash
# Phase 0 research conductor (mini-zgateway). Launches the four bucket
# research agents headlessly (fresh Fable each, scratch cwd outside the
# repo, brief as a file), waits for all with a deadline, writes one exit
# file per agent under /root/runs/, fails closed if an output is missing.
# Usage: dispatch-research.sh [bucket ...]   (default: b1 b2 b3 b4)
export PATH="$HOME/.local/bin:$HOME/.nvm/versions/node/v22.20.0/bin:/usr/local/go/bin:$PATH"
DEADLINE_MIN=${DEADLINE_MIN:-50}
declare -A NAME=( [b1]=b1-spec-named [b2]=b2-prior-projects [b3]=b3-industry-systems [b4]=b4-foundations )
BUCKETS=("$@"); [ ${#BUCKETS[@]} -eq 0 ] && BUCKETS=(b1 b2 b3 b4)
SUFFIX=${SUFFIX:-}
for b in "${BUCKETS[@]}"; do
  n=${NAME[$b]}
  brief=/root/briefs/zgateway-phase0-$n$SUFFIX.md
  [ -f "$brief" ] || brief=/root/briefs/zgateway-phase0-$n.md
  mkdir -p /tmp/zgateway-phase0/$b /tmp/zgateway-phase0/out
  rm -f /root/runs/zgateway-phase0-$b.exit
  (
    cd /tmp/zgateway-phase0/$b || exit 1
    IS_SANDBOX=1 timeout ${DEADLINE_MIN}m claude -p --model claude-fable-5-2 \
      --dangerously-skip-permissions < "$brief" \
      > /root/runs/zgateway-phase0-$b$SUFFIX.log 2>&1
    echo $? > /root/runs/zgateway-phase0-$b.exit
  ) &
  echo "$b pid $! brief $brief"
done
wait
rc=0
for b in "${BUCKETS[@]}"; do
  n=${NAME[$b]}
  e=$(cat /root/runs/zgateway-phase0-$b.exit 2>/dev/null || echo missing)
  out=/tmp/zgateway-phase0/out/$n.md
  sz=$(stat -c %s "$out" 2>/dev/null || echo 0)
  echo "$b exit=$e output_bytes=$sz"
  { [ "$e" = 0 ] && [ "$sz" -gt 2000 ]; } || rc=1
done
exit $rc
