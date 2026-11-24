#!/bin/bash
# Timed fuzzing: every fuzz target in the module, one after another.
#
# Usage: scripts/fuzz.sh
#   FUZZ_SECONDS=20     time per target (the gate's budget; on demand use minutes)
# A run that finds no fuzz target at all is a failure, not a pass.
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/fuzz exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (the first chunk with a fuzz target, chunk 2 or 4) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "fuzz.sh: not built yet" >&2
exit 3
