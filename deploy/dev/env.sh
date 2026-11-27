# Shared settings of the dev stack, sourced by up.sh, load.sh and down.sh. Every value can be
# overridden from the environment (the tier R smoke test does, to run beside other stacks).
#   MG_DEV_DIR         state directory: binaries, configs, pid, logs, CSV runs (default /tmp/mg-dev)
#   MG_DEV_PROJECT     compose project and container-name prefix (default mg-dev; must start with mg-)
#   MG_DEV_PG_PORT     Postgres on 127.0.0.1 (default 55432)
#   MG_DEV_PROM_PORT   Prometheus on 127.0.0.1 (default 59090)
#   MG_DEV_GW_PORT     the gateway's client port on 127.0.0.1 (default 56432)
#   MG_DEV_ADMIN_PORT  the gateway's admin port on 127.0.0.1 (default 56433)
export MG_DEV_DIR=${MG_DEV_DIR:-/tmp/mg-dev}
export MG_DEV_PROJECT=${MG_DEV_PROJECT:-mg-dev}
export MG_DEV_PG_PORT=${MG_DEV_PG_PORT:-55432}
export MG_DEV_PROM_PORT=${MG_DEV_PROM_PORT:-59090}
export MG_DEV_GW_PORT=${MG_DEV_GW_PORT:-56432}
export MG_DEV_ADMIN_PORT=${MG_DEV_ADMIN_PORT:-56433}
export MG_DEV_PG_SUPERPASS=${MG_DEV_PG_SUPERPASS:-mg-dev-super}
MG_DEV_TENANTS=4
MG_DEV_TENANT_PASSWORD=mg-dev-load
MG_DEV_GATEWAY_PASSWORD=mg-dev-gateway
dev_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
dev_compose=(docker compose -p "$MG_DEV_PROJECT" -f "$dev_root/deploy/dev/docker-compose.yml")
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
case "$MG_DEV_PROJECT" in mg-*) ;; *) echo "dev: MG_DEV_PROJECT must start with mg- (got $MG_DEV_PROJECT)" >&2; exit 2 ;; esac
dev_pid_alive() { [ -f "$MG_DEV_DIR/gateway.pid" ] && kill -0 "$(cat "$MG_DEV_DIR/gateway.pid")" 2>/dev/null; }
