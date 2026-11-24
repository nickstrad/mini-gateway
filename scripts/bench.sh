#!/bin/bash
# Benchmarks: the load generator against the real binary (PLAN.md section 11).
#
# Usage: scripts/bench.sh
#   RUN_DIR=<dir>       where the run directories are written
# On demand only; `make bench` runs this.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/bench exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 14) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "bench.sh: not built yet" >&2
exit 3
