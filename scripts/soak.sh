#!/bin/bash
# Soak: a long steady-state run that asserts nothing grows (memory, goroutines, queues).
#
# Usage: scripts/soak.sh
#   SOAK_MINUTES=30     length of the run
#   RUN_DIR=<dir>       where the run directory is written
# On demand only.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/soak exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 14) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "soak.sh: not built yet" >&2
exit 3
