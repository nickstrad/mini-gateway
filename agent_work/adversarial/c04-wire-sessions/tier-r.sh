#!/usr/bin/env bash
set -euo pipefail
export PATH=/usr/local/go/bin:$PATH
cd "$(dirname "$0")/../../.."
name="mg-c04-adversary-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
# Same image, SCRAM settings, and opt-in gate as scripts/tier-r.sh, but only
# one database and the three independent attacks, to spare the shared machine.
docker run -d --rm --name "$name" -p 127.0.0.1::5432 -e POSTGRES_HOST_AUTH_METHOD=scram-sha-256 -e POSTGRES_PASSWORD=mg-tier-r-scram postgres:17 -c shared_buffers=32MB >/dev/null
ready=0
for ((i=0;i<120;i++)); do
 if docker exec "$name" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; then ready=1; break; fi
 sleep 0.5
done
[[ "$ready" == 1 ]] || { echo 'Postgres did not become ready' >&2; exit 2; }
export MG_PG_SCRAM_ADDR="$(docker port "$name" 5432/tcp)"
export ADV_PG_VERSION="$(docker exec "$name" psql -U postgres -tAc 'SHOW server_version')"
export MG_TIER_R=1 ADV_RECORD=1
go test -count=1 -timeout 60s -json -run '^TestAdv_11_' ./agent_work/adversarial/c04-wire-sessions/...
