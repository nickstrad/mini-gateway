#!/bin/bash
# Rolling conductor for the c05 canvas: at most $MAXPAR Opus agents at once,
# one re-dispatch per failed agent, per-agent deadline, fails closed.
set -u
RUNS=/root/runs; ORCH=/root/Software/mini-gateway/agent_work/orch
WD=/root/scratch/c05-canvas; MAXPAR=${MAXPAR:-4}; DEADLINE=${DEADLINE:-5400}
queue=("$@"); declare -A started tries state
log(){ echo "$(date -u +%H:%M:%S) $*" | tee -a $RUNS/zgateway-c05-canvas-conductor.log; }
launch(){ local s=$1 brief=/root/briefs/zgateway-c05-canvas-$1.md
  [ "${tries[$s]:-0}" -ge 1 ] && brief=/root/briefs/zgateway-c05-canvas-$s-retry.md
  tries[$s]=$(( ${tries[$s]:-0} + 1 )); started[$s]=$(date +%s); state[$s]=run
  log "launch $s try ${tries[$s]}: $(bash $ORCH/launch.sh c05-canvas-$s opus $WD $brief 2>&1 | tail -1)"; }
finish(){ local s=$1 rc=$2 base=$RUNS/zgateway-p2-c05-canvas-$1
  if [ "$rc" = 0 ] && [ -s $WD/findings/$s.md ]; then state[$s]=ok; log "done $s"; return; fi
  if [ "${tries[$s]}" -ge 2 ]; then state[$s]=failed; log "FAILED $s rc=$rc (second failure)"; return; fi
  { cat /root/briefs/zgateway-c05-canvas-$s.md
    echo; echo "## RETRY CONTEXT"; echo "A previous run of this brief stopped with status $rc. Its findings file may exist partially at $WD/findings/$s.md — read it, keep what is sound, and finish it. Last lines of its log:"; echo '```'; tail -n 25 $base.log | cut -c1-400; echo '```'; } > /root/briefs/zgateway-c05-canvas-$s-retry.md
  log "retry $s after rc=$rc"; queue+=("$s"); state[$s]=queued; }
while :; do
  running=0
  for s in "${!state[@]}"; do [ "${state[$s]}" = run ] || continue
    base=$RUNS/zgateway-p2-c05-canvas-$s; pid=$(cat $base.pid 2>/dev/null)
    if [ -f $base.exit ]; then finish $s "$(cat $base.exit)"
    elif [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then sleep 3; [ -f $base.exit ] && finish $s "$(cat $base.exit)" || finish $s died
    elif [ $(( $(date +%s) - ${started[$s]} )) -gt $DEADLINE ]; then kill -- -"$pid" 2>/dev/null; sleep 2; finish $s deadline
    else running=$((running+1)); fi
  done
  while [ $running -lt $MAXPAR ] && [ ${#queue[@]} -gt 0 ]; do s=${queue[0]}; queue=("${queue[@]:1}"); launch $s; running=$((running+1)); done
  [ $running = 0 ] && [ ${#queue[@]} = 0 ] && break
  sleep 20
done
rc=0; for s in "${!state[@]}"; do log "FINAL $s ${state[$s]}"; [ "${state[$s]}" = ok ] || rc=1; done
echo $rc > $RUNS/zgateway-c05-canvas-conductor.exit; exit $rc
