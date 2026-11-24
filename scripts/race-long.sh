#!/bin/bash
# Long race run: the shell and engine tests repeated under the race detector.
#
# Usage: scripts/race-long.sh
#   RACE_COUNT=20       repetitions of each package's tests
# On demand only; the gate's G3 runs the listed race packages once.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/race-long exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 6) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "race-long.sh: not built yet" >&2
exit 3
