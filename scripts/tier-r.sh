#!/bin/bash
# Tier R: the tests that need a real Postgres 17, run against throwaway Docker containers.
#
# Usage: scripts/tier-r.sh
#   RUN_DIR=<dir>          where the run log (tier-r.log) is written; the gate passes one and
#                          hands it to csv-assert.sh afterwards
#   MG_TIER_R_UPDATE=1     also rewrite the recorded transcript that CI replays
#                          (internal/sim/world/testdata/fidelity/postgres17.txt)
# Needs Docker and the image postgres:17. Without Docker it fails; it never skips.
#
# What it does: starts two containers from postgres:17, each bound to 127.0.0.1 on a free
# port, named mg-tier-r-<pid>-main and mg-tier-r-<pid>-burst, with trust authentication.
# The main one has max_connections=600 and pg_stat_statements loaded; the burst one has
# max_connections=100. A third, mg-tier-r-<pid>-scram, uses scram-sha-256 authentication
# (password $scram_pw for postgres) with ssl=on (the image's snakeoil certificate), for the
# chunk 4 wire tests. Chunk 6's tests (test/e2e) start a fourth, <prefix>-c06, themselves:
# Postgres 17 with ssl=on and scram-sha-256, its server certificate issued by a test CA that
# the tests generate with the standard library, and pg_stat_statements; they also build the
# mini-gateway binary and run it with client TLS on and obs.csv.dir under RUN_DIR (one run per
# scenario under RUN_DIR/<scenario>/, read by csv-assert.sh with test/analysis/tierr-*/).
# All are removed on exit, also on failure or interrupt. Then it
# runs the tier R tests (go test -run '^TestTierR' with MG_TIER_R=1) and prints a summary.
# The tests themselves skip unless MG_TIER_R=1, so `go test ./...` never needs Docker.
#
# Exit status: 0 green, 1 a test failed, 2 usage or setup error (no Docker, no image,
# a container that does not become ready).
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
command -v docker >/dev/null 2>&1 || { echo "tier-r.sh: docker is not on PATH" >&2; exit 2; }
docker info >/dev/null 2>&1 || { echo "tier-r.sh: the Docker daemon is not reachable" >&2; exit 2; }

image=postgres:17
dir=${RUN_DIR:-}
if [ -z "$dir" ]; then dir=$(mktemp -d /tmp/tier-r.XXXXXX) || exit 2; fi
mkdir -p "$dir" || exit 2
log="$dir/tier-r.log"
prefix="mg-tier-r-$$"
names=()
cleanup() {
  local n
  for n in ${names[@]+"${names[@]}"}; do docker rm -f -v "$n" >/dev/null 2>&1 || true; done
  for n in $(docker ps -aq --filter "name=^${prefix}-" 2>/dev/null); do docker rm -f -v "$n" >/dev/null 2>&1 || true; done
}
trap cleanup EXIT
trap 'exit 2' INT TERM

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

start() {
  local name=$1 port=$2; shift 2
  names+=("$name")
  docker run -d --rm --name "$name" -p "127.0.0.1:$port:5432" \
    -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_PASSWORD=unused \
    "$image" -c shared_buffers=32MB "$@" >/dev/null || { echo "tier-r.sh: cannot start $name from $image" >&2; return 1; }
}

start_scram() {
  local name=$1 port=$2
  names+=("$name")
  docker run -d --rm --name "$name" -p "127.0.0.1:$port:5432" \
    -e POSTGRES_HOST_AUTH_METHOD=scram-sha-256 -e POSTGRES_PASSWORD="$scram_pw" \
    "$image" -c shared_buffers=32MB -c ssl=on -c ssl_cert_file=/etc/ssl/certs/ssl-cert-snakeoil.pem \
    -c ssl_key_file=/etc/ssl/private/ssl-cert-snakeoil.key >/dev/null || { echo "tier-r.sh: cannot start $name from $image" >&2; return 1; }
}

wait_ready() {
  local name=$1 i
  for i in $(seq 1 120); do
    if docker exec "$name" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1 \
      && docker exec -e PGPASSWORD="$scram_pw" "$name" psql -h 127.0.0.1 -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "tier-r.sh: $name did not become ready" >&2
  docker logs "$name" 2>&1 | tail -n 20 >&2
  return 1
}

scram_pw=mg-tier-r-scram
main_port=$(free_port) && burst_port=$(free_port) && scram_port=$(free_port) || { echo "tier-r.sh: cannot find a free port" >&2; exit 2; }
start "$prefix-main" "$main_port" -c max_connections=600 -c shared_preload_libraries=pg_stat_statements || exit 2
start "$prefix-burst" "$burst_port" -c max_connections=100 -c superuser_reserved_connections=0 || exit 2
start_scram "$prefix-scram" "$scram_port" || exit 2
wait_ready "$prefix-main" || exit 2
wait_ready "$prefix-burst" || exit 2
wait_ready "$prefix-scram" || exit 2
version=$(docker exec "$prefix-main" psql -h 127.0.0.1 -U postgres -tAc 'SHOW server_version' 2>/dev/null)
echo "NOTE: Postgres $version in $prefix-main (127.0.0.1:$main_port), $prefix-burst (127.0.0.1:$burst_port) and $prefix-scram (127.0.0.1:$scram_port)"

rc=0
MG_TIER_R=1 MG_TIER_R_PREFIX="$prefix" RUN_DIR="$dir" MG_PG_ADDR="127.0.0.1:$main_port" MG_PG_BURST_ADDR="127.0.0.1:$burst_port" \
  MG_PG_SCRAM_ADDR="127.0.0.1:$scram_port" MG_PG_SCRAM_PASSWORD="$scram_pw" \
  go test -p 1 -count=1 -timeout 30m -v -run '^TestTierR' ./... >"$log" 2>&1 || rc=$?

passed=$(grep -cE -- '^\s*--- PASS: TestTierR' "$log" || true)
failed=$(grep -cE -- '^\s*--- FAIL: ' "$log" || true)
skipped=$(grep -cE -- '^\s*--- SKIP: TestTierR' "$log" || true)
echo "tier-r: $passed passed, $failed failed, $skipped skipped; log $log"
grep -E 'tier R: ' "$log" | sed 's/^[[:space:]]*//' || true
if [ "$rc" != 0 ] || [ "$failed" != 0 ]; then
  grep -E -- '--- FAIL: ' "$log" | head -n 20
  tail -n 40 "$log"
  exit 1
fi
if [ "$passed" = 0 ] || [ "$skipped" != 0 ]; then
  echo "tier-r.sh: no tier R test ran, or one skipped" >&2
  exit 1
fi
exit 0
