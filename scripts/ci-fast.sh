#!/bin/bash
# The CI set: what .github/workflows/ci.yml runs on every push, and a quick local check
# before a push. Target: under five minutes.
#
# Usage: scripts/ci-fast.sh
#
# Runs, through scripts/gate.sh:
#   G0  tools build, gofmt, go build, go vet, the nested module tools/detlint (vet and
#       tests), go mod tidy -diff
#   G1  detlint, importlint, boundlint, paniccheck
#   G3  go test -short -count=1 ./... (a package that honours -short trims itself; fuzz
#       seed corpora run as plain tests, which is what `go test` does with them), then the
#       packages of tools/lint-rules/race-packages.txt under the race detector
#
# Deliberately NOT here (PLAN.md section 6.3: CI runs fast tests only): the comment rule
# G2 (it needs the branch's baseline commit, which a push to main does not have), the seed
# sweep, timed fuzzing, tier R, the CSV assertions, the generated documents, the site and
# the chunk extras. No Docker, no DuckDB. All of that is the local landing gate:
#       scripts/gate.sh <chunk-dir>
#
# The result goes to agent_work/ci/gate/result.json (gitignored). It is marked partial and
# never counts for landing.
#
# Exit status: as scripts/gate.sh (0 green, 1 a check is red, 2 usage or setup error).
set -u
here=$(cd "$(dirname "$0")" && pwd -P)
unset SWEEP_SEEDS FUZZ_SECONDS GATE_BASE GATE_PREPROSE
GATE_ONLY=G0,G1,G3 GATE_SHORT=1 GATE_STAGE=build GATE_OUT=agent_work/ci/gate exec "$here/gate.sh" ci
