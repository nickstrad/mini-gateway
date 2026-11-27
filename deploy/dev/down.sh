#!/bin/bash
# make run-down: stop the dev gateway and remove the stack's containers, network and
# volumes. CSV runs under $MG_DEV_DIR/csv and $MG_DEV_DIR/load are kept for `make report`;
# binaries, configs and the pid file are removed. Safe to run when nothing is up.
# Exit status: 0 nothing is left, 1 something could not be removed or Docker could not list
# what is there.
set -u
. "$(dirname "$0")/env.sh"
rc=0
if [ -f "$MG_DEV_DIR/gateway.pid" ]; then
  pid=$(cat "$MG_DEV_DIR/gateway.pid")
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null
    for _ in $(seq 1 60); do kill -0 "$pid" 2>/dev/null || break; sleep 0.25; done
    kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null
  fi
  kill -0 "$pid" 2>/dev/null && { echo "run-down: gateway pid $pid is still running" >&2; rc=1; }
  rm -f "$MG_DEV_DIR/gateway.pid"
fi
# listed <kind> <docker list command...>: the ids it prints; the caller fails run-down when a
# listing fails, since nothing can then be removed or shown to be gone.
listed() {
  local kind=$1 out st=0
  shift
  out=$("$@" 2>/dev/null) || st=$?
  if [ $st != 0 ]; then
    echo "run-down: cannot list the stack's ${kind} (docker exit status $st: $("$@" 2>&1 >/dev/null))" >&2
    return 1
  fi
  printf '%s\n' "$out"
}
if command -v docker >/dev/null 2>&1; then
  "${dev_compose[@]}" down -v --remove-orphans --timeout 10 >/dev/null 2>&1 || echo "run-down: docker compose down failed; removing what is left one by one" >&2
  if left=$(listed containers docker ps -aq --filter "name=^${MG_DEV_PROJECT}-"); then
    [ -n "$left" ] && { docker rm -f -v $left >/dev/null 2>&1 || rc=1; }
  else
    rc=1
  fi
  if nets=$(listed networks docker network ls -q --filter "name=^${MG_DEV_PROJECT}_"); then
    for n in $nets; do docker network rm "$n" >/dev/null 2>&1 || rc=1; done
  else
    rc=1
  fi
  if vols=$(listed volumes docker volume ls -q --filter "name=^${MG_DEV_PROJECT}_"); then
    for v in $vols; do docker volume rm "$v" >/dev/null 2>&1 || rc=1; done
  else
    rc=1
  fi
fi
rm -rf "$MG_DEV_DIR/bin" "$MG_DEV_DIR/gateway.json" "$MG_DEV_DIR/prometheus.yml" "$MG_DEV_DIR/compose.log"
[ $rc = 0 ] && echo "run-down: the dev stack is down (CSV runs kept under $MG_DEV_DIR)"
exit $rc
