#!/bin/bash
# Generated contract documents: runs every executable scripts/gen-docs.d/*.sh in order.
#
# Usage: scripts/gen-docs.sh            regenerate the documents in place
#        scripts/gen-docs.sh --check    regenerate into a scratch folder and diff against
#                                       the committed files; any difference is a failure
#
# Each generator owns one document and takes the same arguments. Exit status: 0 green,
# 1 a check failed.
set -eu
cd "$(dirname "$0")/.."
for f in scripts/gen-docs.d/*.sh; do [ -x "$f" ] && "$f" "$@" || exit 1; done
