#!/bin/bash
# Run Claim 2 adversarial attacks against a throwaway real Postgres 17 container.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v docker >/dev/null 2>&1 || { echo "run-tier-r.sh: docker not found" >&2; exit 2; }
docker info >/dev/null 2>&1 || { echo "run-tier-r.sh: docker daemon unreachable" >&2; exit 2; }

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()') || exit 2
name="mg-c05-nadv-pg17-$$"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
trap 'exit 2' INT TERM

docker run -d --rm --name "$name" -p "127.0.0.1:$port:5432" \
  -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_PASSWORD=unused \
  postgres:17 -c shared_buffers=32MB >/dev/null || exit 2

for i in $(seq 1 120); do
  if docker exec "$name" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1 \
    && docker exec "$name" psql -h 127.0.0.1 -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 0.3
done

ver=$(docker exec "$name" psql -h 127.0.0.1 -U postgres -tAc 'SHOW server_version' 2>/dev/null)
echo "NOTE: Postgres $ver on 127.0.0.1:$port ($name)"
ADV_TIER_R=1 ADV_PG_ADDR="127.0.0.1:$port" \
  go test -count=1 -v -run '^TestAdv_2_' ./agent_work/adversarial/c05-classifier-neon/...
