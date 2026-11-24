#!/bin/bash
# Seed sweep: every simulator scenario over many seeds (tier S).
#
# Usage: scripts/sweep.sh
#   SWEEP_SEEDS=200     seeds per scenario (the gate's budget; on demand use thousands)
#   SWEEP_SEED_BASE=1   first seed
#   RUN_DIR=<dir>       where each scenario writes its run directory (flushed CSV files);
#                       the gate passes one and hands it to csv-assert.sh afterwards
# Every failure prints a REPLAY: line that reproduces it.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/sweep exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 1) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "sweep.sh: not built yet" >&2
exit 3
