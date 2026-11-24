#!/bin/bash
# SQL assertions over flushed CSV metric files, with DuckDB (PLAN.md section 6.5).
#
# Usage: scripts/csv-assert.sh <run-dir>...
#   Each <run-dir> holds the run directories an earlier stage wrote (sweep.sh, tier-r.sh).
# Needs the DuckDB command-line tool. A run directory with no rows is a failure.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/csv-assert exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 3) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "csv-assert.sh: not built yet" >&2
exit 3
