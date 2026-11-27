#!/bin/bash
# make load RATE=… DURATION=… PROFILE=… SEED=…: run the load tool against the dev stack's
# gateway (started by make run-up). The load tool's own snapshot is flushed to CSV under
# $MG_DEV_DIR/load/ (read it with `make report RUNS=$MG_DEV_DIR/load`).
#   RATE=100 requests per second; DURATION=30s; PROFILE=simple|errors; SEED= (random, printed)
#   CONNS=8; LOAD_ARGS= extra loadgen flags
# Exit status: loadgen's (0 on schedule, 3 SHORTFALL, 1 failure, 2 usage).
set -u
set -o pipefail
. "$(dirname "$0")/env.sh"
dev_pid_alive || { echo "load: no dev gateway is running; make run-up first" >&2; exit 2; }
args=(-addr "127.0.0.1:$MG_DEV_GW_PORT" -password "$MG_DEV_TENANT_PASSWORD" -tenants "$MG_DEV_TENANTS" -conns "${CONNS:-8}"
  -rate "${RATE:-100}" -duration "${DURATION:-30s}" -profile "${PROFILE:-simple}" -csv-dir "$MG_DEV_DIR/load")
[ -n "${SEED:-}" ] && args+=(-seed "$SEED")
# shellcheck disable=SC2086
exec "$MG_DEV_DIR/bin/loadgen" "${args[@]}" ${LOAD_ARGS:-}
