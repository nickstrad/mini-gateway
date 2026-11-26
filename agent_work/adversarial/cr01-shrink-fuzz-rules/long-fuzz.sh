#!/bin/bash
set -u
set -o pipefail
[ "${ADV_LONG:-}" = 1 ] || { echo 'Set ADV_LONG=1'; exit 2; }
export PATH=/usr/local/go/bin:$PATH
root=$(git rev-parse --show-toplevel)
out="$root/agent_work/adversarial/cr01-shrink-fuzz-rules"
scratch=$(mktemp -d "$out/fuzz-work.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
rc=0
for spec in 'internal/sim/world FuzzWorldSchedule' 'internal/sim/simtoy FuzzToySchedule' 'internal/pgwire FuzzClientSession'; do
 read -r pkg target <<<"$spec"
 GOMAXPROCS=2 go test -c -o "$scratch/target.test" "./$pkg" || exit 1
 # Run away from product directories: crashers and cache can only land under the allowed tree.
 (
 cd "$scratch" || exit 1
 GOMAXPROCS=2 ./target.test -test.run='^$' -test.fuzz="^$target$" -test.fuzztime=20s -test.parallel=2 -test.fuzzcachedir="$scratch/cache"
 ) > "$out/long-$target.log" 2>&1 || rc=1
 if [ -d "$scratch/testdata" ]; then cp -R "$scratch/testdata" "$out/reproduced-$target"; fi
done
exit "$rc"
