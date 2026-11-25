#!/usr/bin/env bash
# Independent conversations. No transcript or product files are written.
set -euo pipefail
cd "$(dirname "$0")/../../.."
export PATH="/usr/local/go/bin:$PATH"
name="mg-tier-r-c02-adv-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
trap 'exit 2' INT TERM
docker run --rm -d --name "$name" -p 127.0.0.1::5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17 >/dev/null
ready=0
for ((i=0;i<120;i++)); do
 if docker exec "$name" psql -h 127.0.0.1 -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; then ready=1; break; fi
 sleep 0.5
done
[[ "$ready" == 1 ]] || { docker logs "$name"; exit 2; }
docker exec "$name" psql -U postgres -tAc 'SHOW server_version'
export MG_TIER_R=1
MG_PG_ADDR=$(docker port "$name" 5432/tcp)
export MG_PG_ADDR
go test -count=1 -v -run '^TestAdv_1_' ./agent_work/adversarial/c02-sim-world/...
