#!/bin/bash
# Tier R: the scenarios against the real binary and a real Postgres 17 in Docker.
#
# Usage: scripts/tier-r.sh
#   RUN_DIR=<dir>       where each scenario writes its run directory; the gate passes one
#                       and hands it to csv-assert.sh afterwards
# Needs Docker. Without Docker it fails; it never skips.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/tier-r exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 6) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "tier-r.sh: not built yet" >&2
exit 3
