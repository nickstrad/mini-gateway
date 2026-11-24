#!/bin/bash
# Wait for launched agents to stop.
# Usage: agent_work/orch/wait.sh <deadline-seconds> <run-name>...
# Returns when EVERY named run has stopped, or at the deadline. Prints one line per run:
#   WAIT <name>: exit=<code> | died (no exit file) | still running
# Exit 0 when all exited 0; 1 when any exited nonzero; 3 when any died with no exit file;
# 4 on deadline with a run still alive. Callers that are capped at ten minutes per command
# call it again with the same names until it stops returning 4.
set -u
[ $# -ge 2 ] || { echo "usage: wait.sh <deadline-seconds> <run-name>..." >&2; exit 2; }
RUNS=${RUNS:-/root/runs}
deadline=$(( $(date +%s) + $1 )); shift
while :; do
  alive=0
  for n in "$@"; do
    base="$RUNS/zgateway-p2-$n"
    [ -f "$base.exit" ] && continue
    pid=$(cat "$base.pid" 2>/dev/null || true)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then alive=1; fi
  done
  [ $alive = 0 ] && { sleep 3; break; }
  [ "$(date +%s)" -ge "$deadline" ] && break
  sleep 15
done
rc=0
for n in "$@"; do
  base="$RUNS/zgateway-p2-$n"
  if [ -f "$base.exit" ]; then
    c=$(cat "$base.exit"); echo "WAIT $n: exit=$c"
    [ "$c" = 0 ] || { [ $rc -ge 3 ] || rc=1; }
  else
    pid=$(cat "$base.pid" 2>/dev/null || true)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then echo "WAIT $n: still running"; rc=4
    else echo "WAIT $n: died (no exit file)"; [ $rc = 4 ] || rc=3; fi
  fi
done
exit $rc
