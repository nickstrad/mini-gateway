#!/bin/bash
# Chunk 2 extra check (G10): cross-process replay of every world scenario.
# Builds cmd/mgsim once and runs each chunk 2 scenario on seeds 1 and 42 in two separate
# processes; the trace and state digests must be identical, and every run must pass.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
bin=$(mktemp -d /tmp/c02-extra.XXXXXX) || exit 2
trap 'rm -rf "$bin"' EXIT
go build -o "$bin/mgsim" ./cmd/mgsim || exit 1
n=0
for sc in direct-500-connections direct-reconnect-burst direct-1000-identical-reads direct-open-loop-overload direct-mixed-transactions; do
  for seed in 1 42; do
    a=$("$bin/mgsim" run "$sc" -seed "$seed" | grep -E '^(trace|state)-digest:') || { echo "$sc seed $seed failed"; exit 1; }
    b=$("$bin/mgsim" run "$sc" -seed "$seed" | grep -E '^(trace|state)-digest:') || { echo "$sc seed $seed failed"; exit 1; }
    [ "$a" = "$b" ] || { echo "$sc seed $seed: digests differ across processes"; echo "$a"; echo "$b"; exit 1; }
    n=$((n + 1))
  done
done
echo "NOTE: $n scenario runs replayed across processes with identical digests"
