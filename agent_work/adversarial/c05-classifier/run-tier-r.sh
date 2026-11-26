#!/bin/bash
set -euo pipefail
export PATH=/usr/local/go/bin:$PATH
name="mg-c05-adversary-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
# Same image, trust authentication, loopback-only binding as scripts/tier-r.sh.
docker run --rm -d --name "$name" -e POSTGRES_HOST_AUTH_METHOD=trust -p 127.0.0.1::5432 postgres:17 -c shared_buffers=32MB >/dev/null
ready=0
for ((i=0;i<100;i++)); do
 if docker exec "$name" psql -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; then ready=1; break; fi
 sleep 0.2
done
[ "$ready" = 1 ] || exit 2
export MG_TIER_R=1
export MG_PG_ADDR=$(docker port "$name" 5432/tcp)
go test -count=1 -json -run '^TestAdv_(6_|2_TimeDependentLiterals|4_PostgresUnaryOperatorBoundary)' ./agent_work/adversarial/c05-classifier/... > agent_work/adversarial/c05-classifier/tier-r.json
