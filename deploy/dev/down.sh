#!/bin/bash
# make run-down: stop the dev gateway and remove the stack's containers, network and
# volumes. CSV runs under $MG_DEV_DIR/csv and $MG_DEV_DIR/load are kept for `make report`;
# binaries, configs and the pid file are removed. Safe to run when nothing is up.
# Exit status: 0 nothing is left, 1 something could not be removed.
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
if command -v docker >/dev/null 2>&1; then
  "${dev_compose[@]}" down -v --remove-orphans --timeout 10 >/dev/null 2>&1 || true
  left=$(docker ps -aq --filter "name=^${MG_DEV_PROJECT}-")
  [ -n "$left" ] && { docker rm -f -v $left >/dev/null 2>&1 || rc=1; }
  for n in $(docker network ls -q --filter "name=^${MG_DEV_PROJECT}_"); do docker network rm "$n" >/dev/null 2>&1 || rc=1; done
  for v in $(docker volume ls -q --filter "name=^${MG_DEV_PROJECT}_"); do docker volume rm "$v" >/dev/null 2>&1 || rc=1; done
fi
rm -rf "$MG_DEV_DIR/bin" "$MG_DEV_DIR/gateway.json" "$MG_DEV_DIR/prometheus.yml" "$MG_DEV_DIR/compose.log"
[ $rc = 0 ] && echo "run-down: the dev stack is down (CSV runs kept under $MG_DEV_DIR)"
exit $rc
