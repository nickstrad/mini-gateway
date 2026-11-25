#!/bin/bash
# Generated contract documents: runs every executable scripts/gen-docs.d/*.sh in turn.
#
# Usage: scripts/gen-docs.sh            regenerate the documents in place
#        scripts/gen-docs.sh --check    regenerate into a scratch folder and diff against
#                                       the committed files; any difference is a failure
# Each part receives the same arguments. Exit status: 0 green, 1 a part failed.
set -eu
cd "$(dirname "$0")/.."
for f in scripts/gen-docs.d/*.sh; do [ -x "$f" ] && "$f" "$@" || exit 1; done
