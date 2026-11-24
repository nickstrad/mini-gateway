#!/bin/bash
# Generated contract documents: the error, timeout, capacity and reject tables.
#
# Usage: scripts/gen-docs.sh            regenerate the documents in place
#        scripts/gen-docs.sh --check    regenerate into a scratch folder and diff against
#                                       the committed files; any difference is a failure
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/gen-docs exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 5) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "gen-docs.sh: not built yet" >&2
exit 3
