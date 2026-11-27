#!/bin/bash
# make run-up: build the binaries, start Postgres 17 and Prometheus (docker compose), start the
# gateway on the host, wait until Prometheus reports the gateway's /metrics target as up, and
# print the URLs. Settings: deploy/dev/env.sh. Stop everything with `make run-down`.
# Exit status: 0 the stack is up and scraped, 1 a step failed (the stack is taken down
# again), 2 usage or setup error.
set -u
set -o pipefail
. "$(dirname "$0")/env.sh"
cd "$dev_root" || exit 2
command -v docker >/dev/null 2>&1 || { echo "run-up: docker is not on PATH" >&2; exit 2; }
docker info >/dev/null 2>&1 || { echo "run-up: the Docker daemon is not reachable" >&2; exit 2; }
if dev_pid_alive; then echo "run-up: a gateway is already running (pid $(cat "$MG_DEV_DIR/gateway.pid")); make run-down first" >&2; exit 2; fi
mkdir -p "$MG_DEV_DIR/bin" "$MG_DEV_DIR/csv" "$MG_DEV_DIR/load" || exit 2

fail() { echo "run-up: $*" >&2; "$dev_root/deploy/dev/down.sh" >&2; exit 1; }

echo "run-up: building mini-gateway, mgctl and loadgen into $MG_DEV_DIR/bin"
for c in mini-gateway mgctl loadgen; do
  go build -o "$MG_DEV_DIR/bin/$c" "./cmd/$c" || { echo "run-up: go build ./cmd/$c failed" >&2; exit 1; }
done

cat >"$MG_DEV_DIR/prometheus.yml" <<PROM
global:
  scrape_interval: 1s
  evaluation_interval: 1s
scrape_configs:
  - job_name: mini-gateway
    metrics_path: /metrics
    static_configs:
      - targets: ["127.0.0.1:$MG_DEV_ADMIN_PORT"]
PROM

tenants=""
for i in $(seq 0 $((MG_DEV_TENANTS - 1))); do
  v=$(printf '%s\n' "$MG_DEV_TENANT_PASSWORD" | "$MG_DEV_DIR/bin/mgctl" verifier -password -) || { echo "run-up: mgctl verifier failed" >&2; exit 1; }
  tenants="$tenants${tenants:+, }{\"user\": \"t$i\", \"verifier\": \"$v\"}"
done
cat >"$MG_DEV_DIR/gateway.json" <<CFG
{
  "listen": "127.0.0.1:$MG_DEV_GW_PORT",
  "admin_listen": "127.0.0.1:$MG_DEV_ADMIN_PORT",
  "instance": "gw-dev",
  "database": {"address": "127.0.0.1:$MG_DEV_PG_PORT", "name": "app", "user": "gateway", "password": "$MG_DEV_GATEWAY_PASSWORD"},
  "client_tls": {"mode": "off"},
  "backend_tls": {"mode": "disable"},
  "tenants": [$tenants],
  "pool": {"connections": 8},
  "queue": {"capacity": 256},
  "obs": {"publish_interval": "1s", "detail": true, "csv": {"dir": "$MG_DEV_DIR/csv", "scenario": "dev"}}
}
CFG
"$MG_DEV_DIR/bin/mgctl" config check "$MG_DEV_DIR/gateway.json" >/dev/null || { echo "run-up: the generated gateway config is invalid" >&2; exit 1; }

echo "run-up: starting Postgres 17 and Prometheus (compose project $MG_DEV_PROJECT)"
"${dev_compose[@]}" up -d --quiet-pull >"$MG_DEV_DIR/compose.log" 2>&1 || { cat "$MG_DEV_DIR/compose.log" >&2; fail "docker compose up failed"; }

pg="$MG_DEV_PROJECT-postgres"
for i in $(seq 1 120); do
  if docker exec -e PGPASSWORD="$MG_DEV_GATEWAY_PASSWORD" "$pg" psql -h 127.0.0.1 -U gateway -d app -tAc 'SELECT count(*) FROM kv' 2>/dev/null | grep -qx 1000; then
    break
  fi
  [ "$i" = 120 ] && fail "Postgres did not become ready"
  sleep 0.5
done

echo "run-up: starting the gateway (log $MG_DEV_DIR/gateway.log)"
nohup "$MG_DEV_DIR/bin/mini-gateway" -config "$MG_DEV_DIR/gateway.json" >"$MG_DEV_DIR/gateway.log" 2>&1 </dev/null &
echo $! >"$MG_DEV_DIR/gateway.pid"
for i in $(seq 1 60); do
  "$MG_DEV_DIR/bin/mgctl" -admin "127.0.0.1:$MG_DEV_ADMIN_PORT" health >/dev/null 2>&1 && break
  dev_pid_alive || { cat "$MG_DEV_DIR/gateway.log" >&2; fail "the gateway exited"; }
  [ "$i" = 60 ] && fail "the gateway's health did not become ok"
  sleep 0.5
done

prom="http://127.0.0.1:$MG_DEV_PROM_PORT"
echo "run-up: waiting for Prometheus to report the gateway target as up"
for i in $(seq 1 120); do
  t=$(curl -fsS "$prom/api/v1/targets?state=active" 2>/dev/null || true)
  if printf '%s' "$t" | grep -q "\"scrapeUrl\":\"http://127.0.0.1:$MG_DEV_ADMIN_PORT/metrics\"" \
    && printf '%s' "$t" | grep -q '"health":"up"'; then
    break
  fi
  [ "$i" = 120 ] && fail "Prometheus did not report the target up: $t"
  sleep 0.5
done

graph() { printf '%s/query?g0.expr=%s&g0.tab=graph&g0.range_input=5m' "$prom" "$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1")"; }
cat <<MSG

The stack is up.
  gateway        postgres://t0:$MG_DEV_TENANT_PASSWORD@127.0.0.1:$MG_DEV_GW_PORT/app (tenants t0..t$((MG_DEV_TENANTS - 1)), client TLS off)
  admin          http://127.0.0.1:$MG_DEV_ADMIN_PORT/healthz  /stats  /metrics
  Prometheus     $prom/
  targets        $prom/targets
  graphs
    completed/s  $(graph 'rate(mg_engine_requests_completed_total[10s])')
    replies/s    $(graph 'sum by (reason) (rate(mg_engine_replies_total[10s]))')
    queued       $(graph 'mg_engine_requests_queued')
Next: make load RATE=200 DURATION=60s PROFILE=errors SEED=1   (then: make run-down)
      $MG_DEV_DIR/bin/mgctl -admin 127.0.0.1:$MG_DEV_ADMIN_PORT stats -watch -match requests_
MSG
